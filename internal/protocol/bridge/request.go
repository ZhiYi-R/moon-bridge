package bridge

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"moonbridge/internal/extension/codex"
	"moonbridge/internal/extension/websearch"
	"moonbridge/internal/foundation/logger"
	"moonbridge/internal/foundation/openai"
	"moonbridge/internal/protocol/anthropic"
	"moonbridge/internal/protocol/cache"
)

func (bridge *Bridge) convertInput(raw json.RawMessage, context codex.ConversionContext, extData map[string]any, modelAlias string) ([]anthropic.Message, []anthropic.ContentBlock, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil, nil
	}
	raw = bridge.hooks.PreprocessInput(modelAlias, raw)
	trimmed := strings.TrimSpace(string(raw))
	if strings.HasPrefix(trimmed, "\"") {
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return nil, nil, invalidRequest("input must be a string or array", "input", "invalid_request_error")
		}
		return []anthropic.Message{{Role: "user", Content: []anthropic.ContentBlock{{Type: "text", Text: text}}}}, nil, nil
	}
	if !strings.HasPrefix(trimmed, "[") {
		return nil, nil, invalidRequest("input must be a string or array", "input", "invalid_request_error")
	}

	var items []codex.InputItem
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, nil, invalidRequest("input array is invalid", "input", "invalid_request_error")
	}

	messages := make([]anthropic.Message, 0, len(items))
	system := make([]anthropic.ContentBlock, 0)
	seenToolHistory := false
	var pendingReasoningSummary []openai.ReasoningItemSummary
	for _, item := range items {
		// Let codex package handle Codex-specific input types first.
		if cx := codex.ConvertInputItem(item, context); cx.Handled {
			if cx.MarksToolHistory {
				seenToolHistory = true
			}
			if cx.ToolUse != nil {
				codex.AppendAssistantBlock(&messages, *cx.ToolUse)
				if cx.ConsumesReasoning {
					messages = bridge.applyReasoningBeforeToolUse(modelAlias, messages, pendingReasoningSummary, cx.ToolUse.ID, extData)
					pendingReasoningSummary = nil
				}
			}
			continue
		}
		switch {
		case item.Type == "reasoning":
			pendingReasoningSummary = item.Summary
			continue
		case item.Type == "function_call":
			seenToolHistory = true
			toolName := context.AnthropicFunctionToolName(item.Namespace, item.Name)
			toolInput := codex.ToolInputFromArguments(item.Arguments)
			codex.AppendAssistantBlock(&messages, anthropic.ContentBlock{
				Type:  "tool_use",
				ID:    firstNonEmpty(item.CallID, item.ID),
				Name:  toolName,
				Input: toolInput,
			})
			messages = bridge.applyReasoningBeforeToolUse(modelAlias, messages, pendingReasoningSummary, firstNonEmpty(item.CallID, item.ID), extData)
			pendingReasoningSummary = nil
		case strings.HasSuffix(item.Type, "_output") || item.Type == "function_call_output":
			seenToolHistory = true
			codex.AppendToolResultBlock(&messages, anthropic.ContentBlock{
				Type:      "tool_result",
				ToolUseID: firstNonEmpty(item.CallID, item.ID),
				Content:   item.Output,
			})
			pendingReasoningSummary = nil
		case item.Role == "system" || item.Role == "developer":
			system = append(system, contentBlocksFromRaw(item.Content)...)
		case item.Role == "assistant":
			blocks := contentBlocksFromRaw(item.Content)
			if len(blocks) == 0 || codex.IsEmptyWebSearchPreludeBlocks(blocks) {
				continue
			}
			if len(pendingReasoningSummary) > 0 || seenToolHistory {
				blocks = bridge.hooks.PrependThinkingToAssistant(modelAlias, blocks, pendingReasoningSummary, extData)
				pendingReasoningSummary = nil
			}
			messages = append(messages, anthropic.Message{Role: "assistant", Content: blocks})
		default:
			// New turn boundary: clear stale reasoning from the previous round.
			pendingReasoningSummary = nil
			role := item.Role
			if role == "" {
				role = "user"
			}
			blocks := contentBlocksFromRaw(item.Content)
			if len(blocks) == 0 {
				continue
			}
			messages = append(messages, anthropic.Message{Role: role, Content: blocks})
		}
	}
	return messages, system, nil
}

func (bridge *Bridge) convertTools(tools []openai.Tool, opt RequestOptions) ([]anthropic.Tool, error) {
	converted := make([]anthropic.Tool, 0, len(tools))
	for index, tool := range tools {
		// Check if this is a Codex-specific type first.
		if codexTools, handled := codex.ConvertCodexTool(tool); handled {
			converted = append(converted, codexTools...)
			continue
		}
		switch tool.Type {
		case "function":
			converted = append(converted, codex.AnthropicToolFromOpenAIFunction(tool.Name, tool.Description, tool.Parameters))
		case "web_search", "web_search_preview":
			wsMode := opt.WebSearchMode
			if wsMode == "" {
				wsMode = bridge.defaultWebSearchMode()
			}
			wsTools := websearch.Tools(websearch.ToolOptions{
				Mode:            wsMode,
				MaxUses:         opt.WebSearchMaxUses,
				DefaultMaxUses:  bridge.webSearchMaxUses(),
				FirecrawlAPIKey: firstNonEmpty(opt.FirecrawlAPIKey, bridge.cfg.FirecrawlAPIKey),
			})
			if len(wsTools) == 0 {
				logger.L().With("tool_type", tool.Type).Debug("skipping web_search tool because provider support is disabled")
				continue
			}
			converted = append(converted, wsTools...)
		default:
			return nil, &RequestError{
				Status:  http.StatusBadRequest,
				Message: "Unsupported tool type: " + tool.Type,
				Param:   fmt.Sprintf("tools[%d].type", index),
				Code:    "unsupported_parameter",
			}
		}
	}
	return converted, nil
}

func (bridge *Bridge) webSearchMaxUses() int {
	if bridge.cfg.WebSearchMaxUses > 0 {
		return bridge.cfg.WebSearchMaxUses
	}
	return 8
}

func (bridge *Bridge) defaultWebSearchMode() string {
	if bridge.cfg.WebSearchInjected() {
		return "injected"
	}
	if bridge.cfg.WebSearchEnabled() {
		return "enabled"
	}
	return "disabled"
}

func (bridge *Bridge) ConversionContext(request openai.ResponsesRequest) codex.ConversionContext {
	return codex.ConversionContext{
		CustomTools:   codex.CustomToolSpecs(request.Tools, ""),
		FunctionTools: codex.FunctionToolSpecs(request.Tools, ""),
	}
}

func (bridge *Bridge) convertToolChoice(raw json.RawMessage, context codex.ConversionContext) (anthropic.ToolChoice, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return anthropic.ToolChoice{Type: "auto"}, nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err == nil {
		switch value {
		case "auto", "none":
			return anthropic.ToolChoice{Type: value}, nil
		case "required":
			return anthropic.ToolChoice{Type: "any"}, nil
		default:
			return anthropic.ToolChoice{}, invalidRequest("unsupported tool_choice", "tool_choice", "unsupported_parameter")
		}
	}
	var object struct {
		Type      string `json:"type"`
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
		Function  struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if err := json.Unmarshal(raw, &object); err != nil {
		return anthropic.ToolChoice{}, invalidRequest("invalid tool_choice", "tool_choice", "invalid_request_error")
	}
	// Try Codex-specific tool_choice mapping (namespace, custom tool names).
	if mapped, ok := codex.ConvertCodexToolChoice(object, context); ok {
		return mapped, nil
	}
	name := object.Name
	if name == "" {
		name = object.Function.Name
	}
	if name != "" {
		return anthropic.ToolChoice{Type: "tool", Name: name}, nil
	}
	return anthropic.ToolChoice{}, invalidRequest("unsupported tool_choice", "tool_choice", "unsupported_parameter")
}

func (bridge *Bridge) planCache(request openai.ResponsesRequest, converted anthropic.MessageRequest) (cache.CacheCreationPlan, error) {
	cfg := cache.PlanCacheConfig{
		Mode:                     bridge.cfg.Cache.Mode,
		TTL:                      bridge.cfg.Cache.TTL,
		PromptCaching:            bridge.cfg.Cache.PromptCaching,
		AutomaticPromptCache:     bridge.cfg.Cache.AutomaticPromptCache,
		ExplicitCacheBreakpoints: bridge.cfg.Cache.ExplicitCacheBreakpoints,
		AllowRetentionDowngrade:  bridge.cfg.Cache.AllowRetentionDowngrade,
		MaxBreakpoints:           bridge.cfg.Cache.MaxBreakpoints,
		MinCacheTokens:           bridge.cfg.Cache.MinCacheTokens,
		ExpectedReuse:            bridge.cfg.Cache.ExpectedReuse,
		MinimumValueScore:        bridge.cfg.Cache.MinimumValueScore,
		MinBreakpointTokens:      bridge.cfg.Cache.MinBreakpointTokens,
	}
	return cache.PlanCache(cfg, bridge.registry, request, converted)
}

// applyReasoningBeforeToolUse delegates provider-specific reasoning replay to
// plugins before emitting a tool_use message.
func (bridge *Bridge) applyReasoningBeforeToolUse(modelAlias string, messages []anthropic.Message, pendingSummary []openai.ReasoningItemSummary, toolCallID string, extData map[string]any) []anthropic.Message {
	return bridge.hooks.PrependThinkingToMessages(modelAlias, messages, toolCallID, pendingSummary, extData)
}

func contentBlocksFromRaw(raw json.RawMessage) []anthropic.ContentBlock {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	trimmed := strings.TrimSpace(string(raw))
	if strings.HasPrefix(trimmed, "\"") {
		var text string
		_ = json.Unmarshal(raw, &text)
		if text == "" {
			return nil
		}
		return []anthropic.ContentBlock{{Type: "text", Text: text}}
	}
	var parts []openAIContentPart
	if err := json.Unmarshal(raw, &parts); err == nil && len(parts) > 0 {
		blocks := make([]anthropic.ContentBlock, 0, len(parts))
		for _, part := range parts {
			switch part.Type {
			case "input_text", "text", "output_text":
				if part.Text == "" {
					continue
				}
				blocks = append(blocks, anthropic.ContentBlock{Type: "text", Text: part.Text})
			case "input_image", "image", "image_url":
				if source := imageSourceFromContentPart(part); source != nil {
					blocks = append(blocks, anthropic.ContentBlock{Type: "image", Source: source})
				}
			}
		}
		return blocks
	}
	if trimmed == "" {
		return nil
	}
	return []anthropic.ContentBlock{{Type: "text", Text: trimmed}}
}

type openAIContentPart struct {
	Type     string          `json:"type"`
	Text     string          `json:"text"`
	ImageURL json.RawMessage `json:"image_url"`
}

func imageSourceFromContentPart(part openAIContentPart) *anthropic.ImageSource {
	url := imageURLString(part.ImageURL)
	if url == "" {
		return nil
	}
	return imageSourceFromURL(url)
}

func imageURLString(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var url string
	if err := json.Unmarshal(raw, &url); err == nil {
		return strings.TrimSpace(url)
	}
	var object struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(raw, &object); err == nil {
		return strings.TrimSpace(object.URL)
	}
	return ""
}

func imageSourceFromURL(url string) *anthropic.ImageSource {
	url = strings.TrimSpace(url)
	if url == "" {
		return nil
	}
	lower := strings.ToLower(url)
	if strings.HasPrefix(lower, "data:") {
		return imageSourceFromDataURL(url)
	}
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return &anthropic.ImageSource{Type: "url", URL: url}
	}
	return nil
}

func imageSourceFromDataURL(value string) *anthropic.ImageSource {
	header, data, ok := strings.Cut(value, ",")
	if !ok || strings.TrimSpace(data) == "" {
		return nil
	}
	mediaType := strings.TrimPrefix(header, "data:")
	if semicolon := strings.IndexByte(mediaType, ';'); semicolon >= 0 {
		mediaType = mediaType[:semicolon]
	}
	if mediaType == "" {
		mediaType = "image/png"
	}
	return &anthropic.ImageSource{Type: "base64", MediaType: mediaType, Data: data}
}

func parseStopSequences(raw json.RawMessage) []string {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		return []string{single}
	}
	var multiple []string
	if err := json.Unmarshal(raw, &multiple); err == nil {
		return multiple
	}
	return nil
}
