package bridge

import (
	"errors"
	"fmt"
	"moonbridge/internal/extension/codex"
	"net/http"
	"strings"
	"time"

	"moonbridge/internal/foundation/config"
	"moonbridge/internal/foundation/logger"
	"moonbridge/internal/foundation/openai"
	"moonbridge/internal/protocol/anthropic"
	"moonbridge/internal/protocol/cache"
)

type Bridge struct {
	cfg      config.Config
	registry *cache.MemoryRegistry
	hooks    PluginHooks
}

type RequestError struct {
	Status  int
	Message string
	Param   string
	Code    string
}

func (err *RequestError) Error() string {
	return err.Message
}

func New(cfg config.Config, registry *cache.MemoryRegistry, hooks PluginHooks) *Bridge {
	if registry == nil {
		registry = cache.NewMemoryRegistry()
	}
	return &Bridge{
		cfg:      cfg,
		registry: registry,
		hooks:    hooks.WithDefaults(),
	}
}

// RequestOptions carries per-request overrides resolved by the server layer.
type RequestOptions struct {
	WebSearchMode    string
	WebSearchMaxUses int
	FirecrawlAPIKey  string
}

// ToAnthropic converts an OpenAI Responses request to an Anthropic MessageRequest.
func (bridge *Bridge) ToAnthropic(request openai.ResponsesRequest, extData map[string]any, opts ...RequestOptions) (anthropic.MessageRequest, cache.CacheCreationPlan, error) {
	var opt RequestOptions
	if len(opts) > 0 {
		opt = opts[0]
	}
	hookCtx := bridge.hookContext(request.Model, extData, request.Reasoning, opt)
	log := logger.L().With("model", request.Model)
	log.Debug("正在将 OpenAI 请求转换为 Anthropic 格式")
	if request.Model == "" {
		log.Warn("模型名称是必需的")
		return anthropic.MessageRequest{}, cache.CacheCreationPlan{}, invalidRequest("模型名称是必需的", "model", "missing_required_parameter")
	}

	conversionContext := bridge.ConversionContext(request)
	messages, system, err := bridge.convertInput(request.Input, conversionContext, extData, request.Model)
	if err != nil {
		return anthropic.MessageRequest{}, cache.CacheCreationPlan{}, err
	}
	if request.Instructions != "" {
		system = append([]anthropic.ContentBlock{{Type: "text", Text: request.Instructions}}, system...)
	}
	if bridge.cfg.SystemPrompt != "" {
		system = append([]anthropic.ContentBlock{{Type: "text", Text: bridge.cfg.SystemPrompt}}, system...)
	}
	messages = bridge.hooks.RewriteMessages(hookCtx, messages)
	if len(messages) == 0 {
		messages = []anthropic.Message{{Role: "user", Content: []anthropic.ContentBlock{{Type: "text", Text: " "}}}}
	}

	tools, err := bridge.convertTools(request.Tools, opt)
	if err != nil {
		return anthropic.MessageRequest{}, cache.CacheCreationPlan{}, err
	}
	tools = append(tools, bridge.hooks.InjectTools(hookCtx)...)
	toolChoice, err := bridge.convertToolChoice(request.ToolChoice, conversionContext)
	if err != nil {
		return anthropic.MessageRequest{}, cache.CacheCreationPlan{}, err
	}

	maxTokens := request.MaxOutputTokens
	if maxTokens == 0 {
		maxTokens = bridge.cfg.DefaultMaxTokens
	}
	if maxTokens == 0 {
		maxTokens = 1024
	}

	converted := anthropic.MessageRequest{
		Model:         bridge.cfg.ModelFor(request.Model),
		MaxTokens:     maxTokens,
		System:        system,
		Messages:      messages,
		Tools:         tools,
		ToolChoice:    toolChoice,
		Temperature:   request.Temperature,
		TopP:          request.TopP,
		StopSequences: parseStopSequences(request.Stop),
		Stream:        request.Stream,
		Metadata:      request.Metadata,
	}

	bridge.hooks.MutateRequest(hookCtx, &converted)

	plan, err := bridge.planCache(request, converted)
	if err != nil {
		log.Warn("缓存规划失败", "error", err)
		return anthropic.MessageRequest{}, cache.CacheCreationPlan{}, err
	}
	cache.InjectCacheControl(&converted, plan)
	log.Debug("请求已转换", "anthropic_model", converted.Model, "max_tokens", converted.MaxTokens, "messages", len(converted.Messages), "tools", len(converted.Tools), "cache_mode", plan.Mode)

	return converted, plan, nil
}

func (bridge *Bridge) FromAnthropic(response anthropic.MessageResponse, model string) openai.Response {
	return bridge.FromAnthropicWithPlan(response, model, cache.CacheCreationPlan{})
}

func (bridge *Bridge) FromAnthropicWithContext(response anthropic.MessageResponse, model string, context codex.ConversionContext) openai.Response {
	return bridge.FromAnthropicWithPlanAndContext(response, model, cache.CacheCreationPlan{}, context, nil)
}

func (bridge *Bridge) FromAnthropicWithPlan(response anthropic.MessageResponse, model string, plan cache.CacheCreationPlan) openai.Response {
	return bridge.FromAnthropicWithPlanAndContext(response, model, plan, codex.ConversionContext{}, nil)
}

func (bridge *Bridge) UpdateRegistryFromUsage(plan cache.CacheCreationPlan, signals cache.UsageSignals, inputTokens int) {
	cache.UpdateRegistryFromUsage(bridge.registry, plan, signals, inputTokens)
}

func (bridge *Bridge) FromAnthropicWithPlanAndContext(response anthropic.MessageResponse, model string, plan cache.CacheCreationPlan, context codex.ConversionContext, extData map[string]any) openai.Response {
	log := logger.L().With("model", model)
	log.Debug("正在将 Anthropic 响应转换为 OpenAI 格式", "provider_id", response.ID, "stop_reason", response.StopReason)
	cache.UpdateRegistryFromUsage(bridge.registry, plan, cache.UsageSignals{
		InputTokens:              response.Usage.InputTokens,
		CacheCreationInputTokens: response.Usage.CacheCreationInputTokens,
		CacheReadInputTokens:     response.Usage.CacheReadInputTokens,
	}, response.Usage.InputTokens)
	log.Debug("缓存注册表已更新", "plan_mode", plan.Mode)
	if extData != nil {
		bridge.hooks.RememberResponseContent(model, response.Content, extData)
	}

	output := make([]openai.OutputItem, 0, len(response.Content))
	var outputText strings.Builder
	messageContent := make([]openai.ContentPart, 0)
	var thinkingText string
	hasToolCalls := false

	for index, block := range response.Content {
		skip, rt := bridge.hooks.OnResponseContent(model, block)
		thinkingText += rt
		if skip {
			continue
		}
		switch block.Type {
		case "text":
			part := openai.ContentPart{Type: "output_text", Text: block.Text}
			messageContent = append(messageContent, part)
			outputText.WriteString(block.Text)
		case "tool_use":
			hasToolCalls = true
			if len(messageContent) > 0 {
				output = append(output, openai.OutputItem{
					Type:    "message",
					ID:      fmt.Sprintf("msg_item_%d", index),
					Status:  "completed",
					Role:    "assistant",
					Content: messageContent,
				})
				messageContent = nil
			}
			if item, ok := codex.ConvertToolUseOutput(block, context); ok {
				output = append(output, item)
			}
		case "server_tool_use":
			if item, ok := codex.ConvertServerToolUseOutput(block); ok {
				output = append(output, item)
			}
		}
	}
	if len(messageContent) > 0 {
		output = append(output, openai.OutputItem{
			Type:    "message",
			ID:      "msg_item_0",
			Status:  "completed",
			Role:    "assistant",
			Content: messageContent,
		})
	}

	if thinkingText != "" && hasToolCalls {
		reasoningItem := openai.OutputItem{
			Type: "reasoning",
			Summary: []openai.ReasoningItemSummary{{
				Type: "summary_text",
				Text: thinkingText,
			}},
		}
		output = append([]openai.OutputItem{reasoningItem}, output...)
	}

	status, incomplete := statusFromStopReason(response.StopReason)
	usage := normalizeUsage(response.Usage)

	metadata := map[string]any{
		"provider_message_id": response.ID,
	}
	if response.Usage.CacheCreationInputTokens > 0 || response.Usage.CacheReadInputTokens > 0 || response.Usage.CacheCreation != nil {
		metadata["provider_usage"] = response.Usage
	}

	openAIResponse := openai.Response{
		ID:                responseID(response.ID),
		Object:            "response",
		CreatedAt:         time.Now().Unix(),
		Status:            status,
		Model:             model,
		Output:            output,
		OutputText:        outputText.String(),
		Usage:             usage,
		Metadata:          metadata,
		IncompleteDetails: incomplete,
	}
	bridge.hooks.PostProcessResponse(bridge.hookContext(model, extData, nil, RequestOptions{}), &openAIResponse)
	log.Info("响应已转换", "output_items", len(openAIResponse.Output), "status", openAIResponse.Status)
	return openAIResponse
}

func (bridge *Bridge) ErrorResponse(err error) (int, openai.ErrorResponse) {
	return bridge.errorResponse(err, "")
}

func (bridge *Bridge) ErrorResponseForModel(model string, err error) (int, openai.ErrorResponse) {
	return bridge.errorResponse(err, model)
}

func (bridge *Bridge) errorResponse(err error, model string) (int, openai.ErrorResponse) {
	var requestError *RequestError
	if errors.As(err, &requestError) {
		return requestError.Status, openai.ErrorResponse{Error: openai.ErrorObject{
			Message: requestError.Message,
			Type:    "invalid_request_error",
			Param:   requestError.Param,
			Code:    requestError.Code,
		}}
	}
	var cachePlanErr *cache.CachePlanError
	if errors.As(err, &cachePlanErr) {
		return cachePlanErr.Status, openai.ErrorResponse{Error: openai.ErrorObject{
			Message: cachePlanErr.Message,
			Type:    "invalid_request_error",
			Param:   cachePlanErr.Param,
			Code:    cachePlanErr.Code,
		}}
	}
	if providerError, ok := anthropic.IsProviderError(err); ok {
		msg := providerError.Error()
		msg = bridge.hooks.TransformError(model, msg)
		return providerError.OpenAIStatus(), openai.ErrorResponse{Error: openai.ErrorObject{
			Message: msg,
			Type:    providerError.OpenAIType(),
			Code:    providerError.OpenAICode(),
		}}
	}
	return http.StatusInternalServerError, openai.ErrorResponse{Error: openai.ErrorObject{
		Message: err.Error(),
		Type:    "server_error",
		Code:    "internal_error",
	}}
}

func (bridge *Bridge) ProviderFor(modelAlias string) string {
	return bridge.cfg.ProviderFor(modelAlias)
}

func (bridge *Bridge) NewExtensionData() map[string]any {
	return bridge.hooks.NewSessionData()
}

func (bridge *Bridge) hookContext(model string, extData map[string]any, reasoning map[string]any, opt RequestOptions) HookContext {
	return HookContext{
		ModelAlias:  model,
		SessionData: extData,
		Reasoning:   reasoning,
		WebSearch: HookWebSearchInfo{
			Mode:         opt.WebSearchMode,
			MaxUses:      opt.WebSearchMaxUses,
			FirecrawlKey: opt.FirecrawlAPIKey,
		},
	}
}

// MarkCacheAttempt marks the cache registry as warming for the given plan,
// preventing concurrent streaming requests from treating the prefix as cold.
func (bridge *Bridge) ResetCacheWarming(plan cache.CacheCreationPlan) {
	if plan.PrefixKey == "" || bridge.registry == nil {
		return
	}
	bridge.registry.ResetWarming(plan.PrefixKey)
}

func (bridge *Bridge) MarkCacheAttempt(plan cache.CacheCreationPlan) {
	if plan.PrefixKey == "" || bridge.registry == nil {
		return
	}
	bridge.registry.MarkWarming(plan.PrefixKey)
}
