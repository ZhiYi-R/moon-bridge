package deepseekv4

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/ZhiYi-R/moon-bridge/internal/config"
	"github.com/ZhiYi-R/moon-bridge/internal/extension/plugin"
	"github.com/ZhiYi-R/moon-bridge/internal/format"
	"github.com/ZhiYi-R/moon-bridge/internal/protocol/anthropic"
	"github.com/ZhiYi-R/moon-bridge/internal/protocol/openai"
)

const (
	// DefaultReinforcePrompt is the default prompt injected before user input
	// to reinforce system prompt and AGENTS.md adherence for models that
	// may occasionally ignore them.
	DefaultReinforcePrompt = "[System Reminder]: Please pay close attention to the system instructions, AGENTS.md files, and any other context provided. Follow them carefully and completely in your response.\n[User]:"
)

const PluginName = "deepseek_v4"

// EnabledFunc determines if the plugin is active for a model.

// Config is the configuration structure for the deepseek_v4 plugin.
type Config struct {
	ReinforceInstructions *bool   `json:"reinforce_instructions,omitempty" yaml:"reinforce_instructions"`
	ReinforcePrompt       *string `json:"reinforce_prompt,omitempty" yaml:"reinforce_prompt"`
}

type EnabledFunc func(modelAlias string) bool

// DSPlugin implements the new plugin.Plugin interface plus relevant capabilities.
type DSPlugin struct {
	plugin.BasePlugin
	isEnabled     EnabledFunc
	pluginCfg     config.PluginConfig
	currentConfig func() config.Config
	logger        *slog.Logger
	cfg           *Config
}

// NewPlugin creates a DeepSeek V4 plugin.
func NewPlugin(isEnabled ...EnabledFunc) *DSPlugin {
	var enabled EnabledFunc
	if len(isEnabled) > 0 {
		enabled = isEnabled[0]
	}
	return &DSPlugin{isEnabled: enabled}
}

func (p *DSPlugin) Name() string                              { return PluginName }
func (p *DSPlugin) ConfigSpecs() []config.ExtensionConfigSpec { return ConfigSpecs() }
func (p *DSPlugin) EnabledForModel(model string) bool {
	if p.currentConfig != nil {
		return p.currentConfig().ExtensionEnabled(PluginName, model)
	}
	if p.isEnabled != nil {
		return p.isEnabled(model)
	}
	if setting, ok := p.pluginCfg.Extensions[PluginName]; ok && setting.Enabled != nil {
		return *setting.Enabled
	}
	return false
}
func (p *DSPlugin) Init(ctx plugin.PluginContext) error {
	p.pluginCfg = config.PluginFromGlobalConfig(&ctx.AppConfig)
	p.currentConfig = ctx.CurrentConfig
	p.logger = ctx.Logger
	p.cfg = plugin.Config[Config](ctx)
	return nil
}

func ConfigSpecs() []config.ExtensionConfigSpec {
	return []config.ExtensionConfigSpec{{
		Name: PluginName,
		Scopes: []config.ExtensionScope{
			config.ExtensionScopeGlobal,
			config.ExtensionScopeProvider,
			config.ExtensionScopeModel,
			config.ExtensionScopeRoute,
		},
		Factory:  func() any { return &Config{} },
		Validate: ValidateConfig,
	}}
}

func ValidateConfig(cfg config.Config) error {
	// Protocol constraint removed — plugins operate on protocol-agnostic Core format.
	return nil
}

// --- InputPreprocessor ---

func (p *DSPlugin) PreprocessInput(_ *plugin.RequestContext, raw json.RawMessage) json.RawMessage {
	return StripReasoningContent(raw)
}

// --- RequestMutator ---

func (p *DSPlugin) MutateRequest(ctx *plugin.RequestContext, req *format.CoreRequest) {
	var reasoning map[string]any
	if ctx != nil {
		reasoning = ctx.Reasoning
	}
	// Map reasoning effort to CoreRequest.Output.Effort
	if effort, ok := reasoningEffort(reasoning); ok {
		req.Output = &format.CoreOutputConfig{Effort: effort}
	}
	// Clear incompatible sampling params for DeepSeek V4
	req.Temperature = nil
	req.TopP = nil
}

// --- MessageRewriter ---

func (p *DSPlugin) RewriteMessages(ctx *plugin.RequestContext, messages []format.CoreMessage) []format.CoreMessage {
	cfg := p.configForModel(modelAliasFromRequestContext(ctx))
	if cfg == nil || cfg.ReinforceInstructions == nil || !*cfg.ReinforceInstructions {
		return messages
	}
	prompt := DefaultReinforcePrompt
	if cfg.ReinforcePrompt != nil && *cfg.ReinforcePrompt != "" {
		prompt = *cfg.ReinforcePrompt
	}
	// Inject a reinforcement message before the last real user message.
	// Skip tool_result messages (they have Role="user" but are tool responses).
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" && !isToolResultMessageFormat(messages[i]) {
			reinforcement := format.CoreMessage{
				Role: "user",
				Content: []format.CoreContentBlock{{
					Type: "text",
					Text: prompt,
				}},
			}
			// Insert before position i.
			messages = append(messages[:i], append([]format.CoreMessage{reinforcement}, messages[i:]...)...)
			break
		}
	}
	return messages
}

func (p *DSPlugin) configForModel(modelAlias string) *Config {
	if p.currentConfig != nil {
		current := p.currentConfig()
		cfg, _ := current.ExtensionConfig(PluginName, modelAlias).(*Config)
		if cfg != nil {
			return cfg
		}
		raw := current.ExtensionRawConfig(PluginName, modelAlias)
		if len(raw) > 0 {
			data, err := json.Marshal(raw)
			if err == nil {
				var decoded Config
				if err := json.Unmarshal(data, &decoded); err == nil {
					return &decoded
				}
			}
		}
	}
	return p.cfg
}

func modelAliasFromRequestContext(ctx *plugin.RequestContext) string {
	if ctx == nil {
		return ""
	}
	return ctx.ModelAlias
}

// isToolResultMessageFormat checks if a user message contains only tool_result blocks.
func isToolResultMessageFormat(msg format.CoreMessage) bool {
	if len(msg.Content) == 0 {
		return false
	}
	for _, block := range msg.Content {
		if block.Type != "tool_result" {
			return false
		}
	}
	return true
}

// --- ThinkingPrepender ---

func (p *DSPlugin) PrependThinkingForToolUse(messages []format.CoreMessage, toolCallID string, pendingSummary []openai.ReasoningItemSummary, sessionState any) []format.CoreMessage {
	// Convert to anthropic format for state operations, then back
	anthroMsgs := coreMessagesToAnthropic(messages)
	if block, ok := p.thinkingBlockFromSummary(pendingSummary); ok {
		PrependThinkingBlockForToolUse(&anthroMsgs, anthropicBlockToCore(block))
		return anthropicToCoreMessages(anthroMsgs)
	}
	state, _ := sessionState.(*State)
	if state != nil {
		state.PrependCachedForToolUse(&anthroMsgs, toolCallID)
	}
	if PrependRequiredThinkingForToolUse(&anthroMsgs) {
		p.warnRequiredThinkingFallback("tool_use", "tool_call_id", toolCallID)
	}
	return anthropicToCoreMessages(anthroMsgs)
}

func (p *DSPlugin) PrependThinkingForAssistant(blocks []format.CoreContentBlock, pendingSummary []openai.ReasoningItemSummary, sessionState any) []format.CoreContentBlock {
	// Convert to anthropic format for state operations, then back
	coreBlocks := blocks
	if block, ok := p.thinkingBlockFromSummary(pendingSummary); ok {
		coreBlocks, _ = PrependThinkingBlockForAssistantText(coreBlocks, anthropicBlockToCore(block))
		return coreBlocks
	}
	state, _ := sessionState.(*State)
	if state != nil {
		coreBlocks = state.PrependCachedForAssistantText(coreBlocks)
	}
	coreBlocks, inserted := PrependRequiredThinkingForAssistantText(coreBlocks)
	if inserted {
		p.warnRequiredThinkingFallback("assistant_text", "content_blocks", len(coreBlocks)-1)
	}
	return coreBlocks
}

func (p *DSPlugin) warnRequiredThinkingFallback(target string, attrs ...any) {
	if p.logger == nil {
		return
	}
	args := []any{"target", target}
	args = append(args, attrs...)
	p.logger.Warn("DeepSeek V4 历史缺少可回放 thinking，已在请求侧补空 thinking block", args...)
}

func (p *DSPlugin) thinkingBlockFromSummary(summary []openai.ReasoningItemSummary) (anthropic.ContentBlock, bool) {
	if len(summary) == 0 {
		return anthropic.ContentBlock{}, false
	}
	coreBlock, ok := p.ExtractThinkingBlock(&plugin.RequestContext{}, summary)
	if !ok {
		return anthropic.ContentBlock{}, false
	}
	return coreBlockToAnthropic(coreBlock), true
}

// --- ContentFilter ---

func (p *DSPlugin) FilterContent(_ *plugin.RequestContext, block format.CoreContentBlock) bool {
	switch block.Type {
	case "reasoning":
		return true
	case "text":
		return false
	}
	return false
}

// FilterCoreContent is the Core format equivalent of FilterContent.
// It filters reasoning content blocks from Core responses.
func (p *DSPlugin) FilterCoreContent(ctx context.Context, block *format.CoreContentBlock) bool {
	if block == nil {
		return false
	}
	// In Core format, reasoning blocks have type "reasoning".
	// These should be filtered from the visible content and handled
	// as reasoning items by the adapter layer.
	if block.Type == "reasoning" {
		return true
	}
	return false
}

// --- ContentRememberer ---

func (p *DSPlugin) RememberContent(ctx *plugin.RequestContext, content []format.CoreContentBlock) {
	state, _ := ctx.SessionState(PluginName).(*State)
	if state == nil {
		return
	}
	// Convert Core content blocks to anthropic ContentBlocks for internal state recording.
	anthropicBlocks := make([]anthropic.ContentBlock, 0, len(content))
	for _, block := range content {
		switch block.Type {
		case "reasoning":
			anthropicBlocks = append(anthropicBlocks, anthropic.ContentBlock{
				Type:      "thinking",
				Thinking:  block.ReasoningText,
				Signature: block.ReasoningSignature,
			})
		case "tool_use":
			anthropicBlocks = append(anthropicBlocks, anthropic.ContentBlock{
				Type: "tool_use",
				ID:   block.ToolUseID,
				Name: block.ToolName,
			})
		case "text":
			anthropicBlocks = append(anthropicBlocks, anthropic.ContentBlock{
				Type: "text",
				Text: block.Text,
			})
		}
	}
	state.RememberFromContent(anthropicBlocksToCore(anthropicBlocks))
}

// RememberCoreContent is the Core format equivalent of RememberContent.
// It finds reasoning content blocks and records thinking text
// for later use by PrependThinking operations.
func (p *DSPlugin) RememberCoreContent(ctx context.Context, content []format.CoreContentBlock) {
	// Convert Core content blocks to anthropic ContentBlocks for internal state recording.
	anthropicBlocks := make([]anthropic.ContentBlock, 0, len(content))
	for _, block := range content {
		switch block.Type {
		case "reasoning":
			anthropicBlocks = append(anthropicBlocks, anthropic.ContentBlock{
				Type:      "thinking",
				Thinking:  block.ReasoningText,
				Signature: block.ReasoningSignature,
			})
		case "tool_use":
			anthropicBlocks = append(anthropicBlocks, anthropic.ContentBlock{
				Type: "tool_use",
				ID:   block.ToolUseID,
				Name: block.ToolName,
			})
		case "text":
			anthropicBlocks = append(anthropicBlocks, anthropic.ContentBlock{
				Type: "text",
				Text: block.Text,
			})
		}
	}

	// Note: session-based state recording is deferred to the old path's
	// RememberContent which the dispatch layer calls with proper session context.
	if len(anthropicBlocks) > 0 && p.logger != nil {
		thinkingBlocks := 0
		for _, b := range anthropicBlocks {
			if b.Type == "thinking" && b.Thinking != "" {
				thinkingBlocks++
			}
		}
		p.logger.Debug("RememberCoreContent: converted content blocks",
			"total_blocks", len(anthropicBlocks),
			"thinking_blocks", thinkingBlocks,
		)
	}
}

// --- StreamInterceptor ---

func (p *DSPlugin) NewStreamState() any {
	return NewStreamState()
}

func (p *DSPlugin) OnStreamEvent(ctx *plugin.StreamContext, event plugin.StreamEvent) (consumed bool, emit []openai.StreamEvent) {
	ss, _ := ctx.StreamState.(*StreamState)
	if ss == nil {
		return false, nil
	}

	switch event.Type {
	case "block_start":
		if ss.Start(event.Index, event.Block) {
			return true, nil
		}
		// Track tool_use block IDs so they can be matched to thinking blocks
		// during RememberStreamResult.
		if event.Block != nil && event.Block.Type == "tool_use" && event.Block.ToolUseID != "" {
			ss.RecordToolCall(event.Block.ToolUseID)
		}
	case "block_delta":
		if ss.Delta(event.Index, event.Delta) {
			return true, nil
		}
	case "block_stop":
		if ss.Stop(event.Index) {
			// Thinking block completed; the bridge will handle emitting
			// the reasoning item from CompletedThinkingText().
			return true, nil
		}
	}
	return false, nil
}

func (p *DSPlugin) OnStreamComplete(ctx *plugin.StreamContext, outputText string) {
	ss, _ := ctx.StreamState.(*StreamState)
	state, _ := ctx.SessionState(PluginName).(*State)
	if ss == nil || state == nil {
		return
	}
	state.RememberStreamResult(ss, outputText)
}

// --- ErrorTransformer ---

func (p *DSPlugin) TransformError(_ *plugin.RequestContext, msg string) string {
	if strings.Contains(msg, "content[].thinking") && strings.Contains(msg, "thinking mode") {
		return "Missing required thinking blocks - ensure reasoning items are preserved in conversation history for tool-call turns."
	}
	return msg
}

// --- MutateCoreRequest (Core format equivalent of MutateRequest) ---

// MutateCoreRequest injects DeepSeek thinking configuration into the CoreRequest.
func (p *DSPlugin) MutateCoreRequest(ctx context.Context, req *format.CoreRequest) {
	if req.Extensions == nil {
		req.Extensions = make(map[string]any)
	}

	// Read thinking budget from extension config, default to 4096.
	budgetTokens := 4096
	if setting, ok := p.pluginCfg.Extensions[PluginName]; ok && setting.RawConfig != nil {
		if v, ok := setting.RawConfig["thinking_budget_tokens"]; ok {
			if n, ok := v.(float64); ok {
				budgetTokens = int(n)
			}
		}
	}

	req.Extensions["thinking"] = map[string]any{
		"budget_tokens": budgetTokens,
	}
}

// --- ReasoningExtractor ---

func (p *DSPlugin) ExtractThinkingBlock(_ *plugin.RequestContext, summary []openai.ReasoningItemSummary) (format.CoreContentBlock, bool) {
	for _, item := range summary {
		if item.Type != "summary_text" {
			// Try adapter-created "text" type items with optional Signature field.
			if item.Type == "text" {
				if item.Text != "" || item.Signature != "" {
					return format.CoreContentBlock{
						Type:               "reasoning",
						ReasoningText:      item.Text,
						ReasoningSignature: item.Signature,
					}, true
				}
			}
			continue
		}
		if block, ok := DecodeThinkingSummary(item.Text); ok {
			return block, true
		}
	}
	return format.CoreContentBlock{}, false
}

// --- SessionStateProvider ---

func (p *DSPlugin) NewSessionState() any {
	return NewState()
}

// Compile-time interface checks.
var (
	_ plugin.Plugin               = (*DSPlugin)(nil)
	_ plugin.InputPreprocessor    = (*DSPlugin)(nil)
	_ plugin.RequestMutator       = (*DSPlugin)(nil)
	_ plugin.MessageRewriter      = (*DSPlugin)(nil)
	_ plugin.ContentFilter        = (*DSPlugin)(nil)
	_ plugin.ContentRememberer    = (*DSPlugin)(nil)
	_ plugin.StreamInterceptor    = (*DSPlugin)(nil)
	_ plugin.ErrorTransformer     = (*DSPlugin)(nil)
	_ plugin.SessionStateProvider = (*DSPlugin)(nil)
	_ plugin.ThinkingPrepender    = (*DSPlugin)(nil)
	_ plugin.ReasoningExtractor   = (*DSPlugin)(nil)
)

// =========================================================================
// Conversion helpers: format.Core types ↔ anthropic types
// These bridge format-typed plugin interfaces with internal state that
// operates on anthropic types. DELETE after state migration to format types.
// =========================================================================

// coreMessagesToAnthropic converts []format.CoreMessage to []anthropic.Message.
func coreMessagesToAnthropic(msgs []format.CoreMessage) []anthropic.Message {
	result := make([]anthropic.Message, 0, len(msgs))
	for _, m := range msgs {
		result = append(result, anthropic.Message{
			Role:    m.Role,
			Content: coreBlocksToAnthropic(m.Content),
		})
	}
	return result
}

// anthropicToCoreMessages converts []anthropic.Message to []format.CoreMessage.
func anthropicToCoreMessages(msgs []anthropic.Message) []format.CoreMessage {
	result := make([]format.CoreMessage, 0, len(msgs))
	for _, m := range msgs {
		result = append(result, format.CoreMessage{
			Role:    m.Role,
			Content: anthropicBlocksToCore(m.Content),
		})
	}
	return result
}

// coreBlocksToAnthropic converts []format.CoreContentBlock to []anthropic.ContentBlock.
func coreBlocksToAnthropic(blocks []format.CoreContentBlock) []anthropic.ContentBlock {
	result := make([]anthropic.ContentBlock, 0, len(blocks))
	for _, b := range blocks {
		result = append(result, coreBlockToAnthropic(b))
	}
	return result
}

// anthropicBlocksToCore converts []anthropic.ContentBlock to []format.CoreContentBlock.
func anthropicBlocksToCore(blocks []anthropic.ContentBlock) []format.CoreContentBlock {
	result := make([]format.CoreContentBlock, 0, len(blocks))
	for _, b := range blocks {
		result = append(result, anthropicBlockToCore(b))
	}
	return result
}

// coreBlockToAnthropic converts a single format.CoreContentBlock to anthropic.ContentBlock.
func coreBlockToAnthropic(b format.CoreContentBlock) anthropic.ContentBlock {
	switch b.Type {
	case "reasoning":
		return anthropic.ContentBlock{
			Type:      "thinking",
			Thinking:  b.ReasoningText,
			Signature: b.ReasoningSignature,
		}
	case "text":
		return anthropic.ContentBlock{Type: "text", Text: b.Text}
	case "tool_use":
		return anthropic.ContentBlock{
			Type:  "tool_use",
			ID:    b.ToolUseID,
			Name:  b.ToolName,
			Input: b.ToolInput,
		}
	case "tool_result":
		return anthropic.ContentBlock{
			Type:      "tool_result",
			ToolUseID: b.ToolUseID,
			Content:   coreBlocksToAnthropic(b.ToolResultContent),
		}
	case "image":
		return anthropic.ContentBlock{
			Type: "image",
			Source: &anthropic.ImageSource{
				Type:      "base64",
				Data:      b.ImageData,
				MediaType: b.MediaType,
			},
		}
	default:
		return anthropic.ContentBlock{Type: "text", Text: b.Text}
	}
}

// anthropicBlockToCore converts a single anthropic.ContentBlock to format.CoreContentBlock.
func anthropicBlockToCore(b anthropic.ContentBlock) format.CoreContentBlock {
	switch b.Type {
	case "thinking":
		return format.CoreContentBlock{
			Type:               "reasoning",
			ReasoningText:      b.Thinking,
			ReasoningSignature: b.Signature,
		}
	case "text":
		return format.CoreContentBlock{Type: "text", Text: b.Text}
	case "tool_use":
		return format.CoreContentBlock{
			Type:      "tool_use",
			ToolUseID: b.ID,
			ToolName:  b.Name,
			ToolInput: b.Input,
		}
	case "tool_result":
		cb := format.CoreContentBlock{
			Type:      "tool_result",
			ToolUseID: b.ToolUseID,
		}
		if b.Content != nil {
			switch c := b.Content.(type) {
			case string:
				cb.ToolResultContent = []format.CoreContentBlock{{Type: "text", Text: c}}
			case []anthropic.ContentBlock:
				cb.ToolResultContent = anthropicBlocksToCore(c)
			}
		}
		return cb
	case "image":
		cb := format.CoreContentBlock{Type: "image"}
		if b.Source != nil {
			cb.MediaType = b.Source.MediaType
			cb.ImageData = b.Source.Data
		}
		return cb
	default:
		return format.CoreContentBlock{Type: "text", Text: b.Text}
	}
}
