package deepseekv4

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"moonbridge/internal/extension/plugin"
	"moonbridge/internal/foundation/config"
	"moonbridge/internal/foundation/openai"
	"moonbridge/internal/protocol/anthropic"
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
	isEnabled EnabledFunc
	appCfg    config.Config
	logger    *slog.Logger
	cfg       *Config
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
func (p *DSPlugin) ConfigType() any                           { return &Config{} }
func (p *DSPlugin) ConfigSpecs() []config.ExtensionConfigSpec { return ConfigSpecs() }
func (p *DSPlugin) EnabledForModel(model string) bool {
	if p.isEnabled != nil {
		return p.isEnabled(model)
	}
	return p.appCfg.ExtensionEnabled(PluginName, model)
}
func (p *DSPlugin) Init(ctx plugin.PluginContext) error {
	p.appCfg = ctx.AppConfig
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
	for alias, route := range cfg.Routes {
		if !cfg.ExtensionEnabled(PluginName, alias) {
			continue
		}
		if err := validateAnthropicProvider(cfg, route.Provider, alias); err != nil {
			return err
		}
	}
	for providerKey, def := range cfg.ProviderDefs {
		for modelName := range def.Models {
			alias := providerKey + "/" + modelName
			if !cfg.ExtensionEnabled(PluginName, alias) {
				continue
			}
			if err := validateAnthropicProvider(cfg, providerKey, alias); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateAnthropicProvider(cfg config.Config, providerKey string, modelAlias string) error {
	def, ok := cfg.ProviderDefs[providerKey]
	if !ok {
		return nil
	}
	if def.Protocol != "" && def.Protocol != config.ProtocolAnthropic {
		return fmt.Errorf("extensions.%s enabled for %s requires anthropic protocol (provider %s uses %s)", PluginName, modelAlias, providerKey, def.Protocol)
	}
	return nil
}

// --- InputPreprocessor ---

func (p *DSPlugin) PreprocessInput(_ *plugin.RequestContext, raw json.RawMessage) json.RawMessage {
	return StripReasoningContent(raw)
}

// --- RequestMutator ---

func (p *DSPlugin) MutateRequest(ctx *plugin.RequestContext, req *anthropic.MessageRequest) {
	var reasoning map[string]any
	if ctx != nil {
		reasoning = ctx.Reasoning
	}
	ToAnthropicRequest(req, reasoning)
}

// --- MessageRewriter ---

func (p *DSPlugin) RewriteMessages(ctx *plugin.RequestContext, messages []anthropic.Message) []anthropic.Message {
	if p.cfg == nil || p.cfg.ReinforceInstructions == nil || !*p.cfg.ReinforceInstructions {
		return messages
	}
	prompt := DefaultReinforcePrompt
	if p.cfg.ReinforcePrompt != nil && *p.cfg.ReinforcePrompt != "" {
		prompt = *p.cfg.ReinforcePrompt
	}
	// Inject a reinforcement message before the last real user message.
	// Skip tool_result messages (they have Role="user" but are tool responses).
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" && !isToolResultMessage(messages[i]) {
			reinforcement := anthropic.Message{
				Role: "user",
				Content: []anthropic.ContentBlock{{
					Type: "text",
					Text: prompt,
				}},
			}
			// Insert before position i.
			messages = append(messages[:i], append([]anthropic.Message{reinforcement}, messages[i:]...)...)
			break
		}
	}
	return messages
}

// isToolResultMessage checks if a user message contains only tool_result blocks.
func isToolResultMessage(msg anthropic.Message) bool {
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

func (p *DSPlugin) PrependThinkingForToolUse(messages []anthropic.Message, toolCallID string, pendingSummary []openai.ReasoningItemSummary, sessionState any) []anthropic.Message {
	if block, ok := p.thinkingBlockFromSummary(pendingSummary); ok {
		PrependThinkingBlockForToolUse(&messages, block)
		return messages
	}
	state, _ := sessionState.(*State)
	if state != nil {
		state.PrependCachedForToolUse(&messages, toolCallID)
	}
	if PrependRequiredThinkingForToolUse(&messages) {
		p.warnRequiredThinkingFallback("tool_use", "tool_call_id", toolCallID)
	}
	return messages
}

func (p *DSPlugin) PrependThinkingForAssistant(blocks []anthropic.ContentBlock, pendingSummary []openai.ReasoningItemSummary, sessionState any) []anthropic.ContentBlock {
	if block, ok := p.thinkingBlockFromSummary(pendingSummary); ok {
		blocks, _ = PrependThinkingBlockForAssistantText(blocks, block)
		return blocks
	}
	state, _ := sessionState.(*State)
	if state != nil {
		blocks = state.PrependCachedForAssistantText(blocks)
	}
	blocks, inserted := PrependRequiredThinkingForAssistantText(blocks)
	if inserted {
		p.warnRequiredThinkingFallback("assistant_text", "content_blocks", len(blocks)-1)
	}
	return blocks
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
	return p.ExtractThinkingBlock(&plugin.RequestContext{}, summary)
}

// --- ContentFilter ---

func (p *DSPlugin) FilterContent(_ *plugin.RequestContext, block anthropic.ContentBlock) (skip bool, extra []openai.OutputItem) {
	switch block.Type {
	case "thinking", "reasoning_content":
		text := EncodeThinkingSummary(block)
		if text == "" {
			text = ExtractReasoningContent([]anthropic.ContentBlock{block})
		}
		if text != "" {
			extra = append(extra, openai.OutputItem{
				Type: "reasoning",
				Summary: []openai.ReasoningItemSummary{{
					Type: "summary_text",
					Text: text,
				}},
			})
		}
		return true, extra
	case "text":
		if IsReasoningContentBlock(&block) {
			return true, nil
		}
	}
	return false, nil
}

// --- ContentRememberer ---

func (p *DSPlugin) RememberContent(ctx *plugin.RequestContext, content []anthropic.ContentBlock) {
	state, _ := ctx.SessionState(PluginName).(*State)
	if state == nil {
		return
	}
	state.RememberFromContent(content)
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

// --- ReasoningExtractor ---

func (p *DSPlugin) ExtractThinkingBlock(_ *plugin.RequestContext, summary []openai.ReasoningItemSummary) (anthropic.ContentBlock, bool) {
	for _, item := range summary {
		if item.Type != "summary_text" {
			continue
		}
		if block, ok := DecodeThinkingSummary(item.Text); ok {
			return block, true
		}
	}
	return anthropic.ContentBlock{}, false
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
