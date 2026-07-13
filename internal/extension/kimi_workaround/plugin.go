// Package kimi_workaround implements an extension that mitigates Kimi models'
// tendency to over-collect information across many tool call rounds.
//
// It tracks the number of tool call rounds in a request, injects a system
// reminder prompt after each tool_result batch, and adds a convergence
// prompt when approaching or reaching the configured max rounds.
package kimi_workaround

import (
	"encoding/json"
	"fmt"

	"github.com/ZhiYi-R/moon-bridge/internal/config"
	"github.com/ZhiYi-R/moon-bridge/internal/extension/plugin"
	"github.com/ZhiYi-R/moon-bridge/internal/format"
)

const PluginName = "kimi_workaround"

// DefaultPrompt is the default prompt injected after each tool call round.
const DefaultPrompt = "[SystemReminder] Notice that the number of tool turns you can use at once is capped; current turn [%d/%d]."

// DefaultLimitPrompt is the prompt injected when approaching or reaching the limit.
const DefaultLimitPrompt = "You are approaching the maximum number of tool turns. Please start wrapping up and prepare to produce your final response."

// DefaultLimitPromptAtLimit is the prompt injected when the max is reached.
const DefaultLimitPromptAtLimit = "You have reached the maximum number of tool turns. Stop gathering information and produce your final response now."

type EnabledFunc func(modelAlias string) bool

// Config holds the kimi_workaround extension configuration.
type Config struct {
	MaxToolRounds     *int     `json:"max_tool_rounds,omitempty" yaml:"max_tool_rounds"`
	ConvergenceMargin *float64 `json:"convergence_margin,omitempty" yaml:"convergence_margin"`
}

// Plugin implements the kimi workaround extension.
type Plugin struct {
	plugin.BasePlugin
	isEnabled     EnabledFunc
	pluginCfg     config.PluginConfig
	currentConfig func() config.Config
	maxRounds     int
	margin        float64
}

func NewPlugin(isEnabled ...EnabledFunc) *Plugin {
	var enabled EnabledFunc
	if len(isEnabled) > 0 {
		enabled = isEnabled[0]
	}
	return &Plugin{isEnabled: enabled, maxRounds: 50, margin: 0.8}
}

func (p *Plugin) Name() string { return PluginName }

func (p *Plugin) ConfigSpecs() []config.ExtensionConfigSpec { return ConfigSpecs() }

func ConfigSpecs() []config.ExtensionConfigSpec {
	return []config.ExtensionConfigSpec{{
		Name: PluginName,
		Scopes: []config.ExtensionScope{
			config.ExtensionScopeGlobal,
			config.ExtensionScopeProvider,
			config.ExtensionScopeModel,
			config.ExtensionScopeRoute,
		},
		Factory: func() any { return &Config{} },
	}}
}

func (p *Plugin) Init(ctx plugin.PluginContext) error {
	p.pluginCfg = config.PluginFromGlobalConfig(&ctx.AppConfig)
	p.currentConfig = ctx.CurrentConfig
	if cfg := plugin.Config[Config](ctx); cfg != nil {
		if cfg.MaxToolRounds != nil && *cfg.MaxToolRounds > 0 {
			p.maxRounds = *cfg.MaxToolRounds
		}
		if cfg.ConvergenceMargin != nil && *cfg.ConvergenceMargin > 0 {
			p.margin = *cfg.ConvergenceMargin
		}
	}
	return nil
}

func (p *Plugin) Shutdown() error { return nil }

func (p *Plugin) EnabledForModel(model string) bool {
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

// RewriteMessages implements plugin.MessageRewriter.
func (p *Plugin) RewriteMessages(ctx *plugin.RequestContext, messages []format.CoreMessage) []format.CoreMessage {
	modelAlias := ""
	if ctx != nil {
		modelAlias = ctx.ModelAlias
	}
	if !p.EnabledForModel(modelAlias) {
		return messages
	}
	maxRounds, margin := p.limitsForModel(modelAlias)

	// Count tool call rounds since the last real user message.
	// Each new user prompt resets the counter — we only care about how many
	// consecutive tool call rounds the current turn has consumed.
	// Injected SystemReminder messages (our own) are treated as infrastructure
	// and skipped — they do NOT reset the counter.
	roundCount := 0
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.Role == "user" && !isToolResultMessage(msg) && !isSystemReminder(msg) {
			break // reached the real user prompt — stop counting
		}
		if msg.Role == "assistant" && hasToolUse(msg.Content) {
			roundCount++
		}
	}

	// No tool calls or first round only — skip injection.
	if roundCount == 0 {
		return messages
	}

	// Find tool_result batch position at the end of messages.
	// Treat our own injected SystemReminder messages as transparent infrastructure
	// (same as tool_result) — they should NOT break the tool_use/tool_result pairing.
	// Only inject after a COMPLETED round (tool_use + matching tool_result).
	// If there's a pending tool_use (no matching tool_result yet), skip injection
	// to avoid breaking the tool_use/tool_result pairing required by the API.
	lastIdx := len(messages)
	for lastIdx > 0 {
		prev := messages[lastIdx-1]
		if prev.Role == "tool" {
			lastIdx--
			continue
		}
		if prev.Role == "user" && isToolResultMessage(prev) {
			lastIdx--
			continue
		}
		if prev.Role == "user" && isSystemReminder(prev) {
			lastIdx--
			continue
		}
		break
	}

	// If there's no tool_result batch, don't inject.
	if lastIdx == len(messages) {
		return messages
	}

	// Safety check: verify the last assistant message before lastIdx has
	// its tool_use IDs matched by tool_results in the batch. If the last
	// assistant message has tool_use(s) that are NOT matched, there's a
	// pending call — skip injection to avoid breaking API pairing.
	if hasUnmatchedToolUse(messages, lastIdx) {
		return messages
	}

	// When approaching limit: append prompts to the last tool_result's content.
	// When AT limit: REPLACE the last tool_result's content with a hard error
	// so the model sees that tools are "broken" and must produce a final answer.
	threshold := int(float64(maxRounds) * margin)
	messagesCopy := make([]format.CoreMessage, len(messages))
	copy(messagesCopy, messages)

	// Find the last tool_result in the tail batch.
	for i := lastIdx; i < len(messagesCopy); i++ {
		msg := &messagesCopy[i]
		if msg.Role == "tool" || (msg.Role == "user" && isToolResultMessage(*msg)) {
			for j := len(msg.Content) - 1; j >= 0; j-- {
				if msg.Content[j].Type == "tool_result" && msg.Content[j].ToolUseID != "" {
					if roundCount >= maxRounds {
						// Hard cap reached — replace tool_result content with an error.
						// The model will see the tool as failed and stop calling it.
						errJSON := fmt.Sprintf(
							"{\"error\":\"MAX_TOOL_ROUNDS\",\"message\":\"Tool call limit reached after %d rounds. Must produce final response immediately without any additional tool calls.\"}",
							maxRounds,
						)
						msg.Content[j].ToolResultContent = []format.CoreContentBlock{{
							Type: "text",
							Text: errJSON,
						}}
						return messagesCopy
					}
					// Approaching limit — append prompts.
					prompt := fmt.Sprintf(DefaultPrompt, roundCount, maxRounds)
					msg.Content[j].ToolResultContent = append(
						msg.Content[j].ToolResultContent,
						format.CoreContentBlock{Type: "text", Text: prompt},
					)
					if roundCount >= threshold {
						msg.Content[j].ToolResultContent = append(
							msg.Content[j].ToolResultContent,
							format.CoreContentBlock{Type: "text", Text: DefaultLimitPrompt},
						)
					}
					return messagesCopy
				}
			}
		}
	}

	// Fallback: if we couldn't find a tool_result to append to, return unchanged.
	return messages
}

func (p *Plugin) limitsForModel(modelAlias string) (int, float64) {
	maxRounds := p.maxRounds
	margin := p.margin
	if maxRounds <= 0 {
		maxRounds = 50
	}
	if margin <= 0 {
		margin = 0.8
	}
	if p.currentConfig == nil {
		return maxRounds, margin
	}
	raw := p.currentConfig().ExtensionRawConfig(PluginName, modelAlias)
	if len(raw) == 0 {
		return maxRounds, margin
	}
	var cfg Config
	data, err := json.Marshal(raw)
	if err != nil {
		return maxRounds, margin
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return maxRounds, margin
	}
	if cfg.MaxToolRounds != nil && *cfg.MaxToolRounds > 0 {
		maxRounds = *cfg.MaxToolRounds
	}
	if cfg.ConvergenceMargin != nil && *cfg.ConvergenceMargin > 0 {
		margin = *cfg.ConvergenceMargin
	}
	return maxRounds, margin
}

// isSystemReminder checks if a user message is one of our injected prompts.
func isSystemReminder(msg format.CoreMessage) bool {
	if msg.Role != "user" || len(msg.Content) == 0 {
		return false
	}
	for _, b := range msg.Content {
		if b.Type == "text" {
			text := b.Text
			if text == DefaultPrompt || text == DefaultLimitPrompt || text == DefaultLimitPromptAtLimit {
				return true
			}
			if len(text) >= 17 && text[:17] == "[SystemReminder]" {
				return true
			}
		}
	}
	return false
}

// hasToolUse checks if any content block in the slice is a tool_use.
func hasToolUse(blocks []format.CoreContentBlock) bool {
	for _, b := range blocks {
		if b.Type == "tool_use" {
			return true
		}
	}
	return false
}

// isToolResultMessage checks if a user message contains only tool_result blocks.
func isToolResultMessage(msg format.CoreMessage) bool {
	if msg.Role != "user" {
		return false
	}
	if len(msg.Content) == 0 {
		return false
	}
	for _, b := range msg.Content {
		if b.Type != "tool_result" {
			return false
		}
	}
	return true
}

// hasUnmatchedToolUse checks whether the batch before lastIdx contains a
// complete tool_use → tool_result round. If an assistant message has tool_use(s)
// whose call IDs don't appear in the subsequent tool_results, skip injection.
func hasUnmatchedToolUse(messages []format.CoreMessage, lastIdx int) bool {
	// Collect tool_call_ids from tool_results in the tail batch (lastIdx..end).
	tailToolIDs := make(map[string]bool)
	for i := lastIdx; i < len(messages); i++ {
		m := messages[i]
		if m.Role == "tool" || (m.Role == "user" && isToolResultMessage(m)) {
			for _, b := range m.Content {
				if b.Type == "tool_result" && b.ToolUseID != "" {
					tailToolIDs[b.ToolUseID] = true
				}
			}
		}
	}
	// Find the last assistant message before lastIdx that has tool_use.
	for i := lastIdx - 1; i >= 0; i-- {
		m := messages[i]
		if m.Role == "assistant" {
			for _, b := range m.Content {
				if b.Type == "tool_use" && b.ToolUseID != "" {
					if !tailToolIDs[b.ToolUseID] {
						return true // unmatched tool_use found
					}
				}
			}
			// Once we find the last assistant message, stop looking further back.
			break
		}
	}
	return false
}

var (
	_ plugin.Plugin             = (*Plugin)(nil)
	_ plugin.ConfigSpecProvider = (*Plugin)(nil)
	_ plugin.MessageRewriter    = (*Plugin)(nil)
)
