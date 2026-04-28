package bridge

import (
	"encoding/json"

	"moonbridge/internal/foundation/openai"
	"moonbridge/internal/protocol/anthropic"
)

// HookContext carries per-request context for plugin hooks.
// This replaces the plugin.RequestContext dependency in the bridge package.
type HookContext struct {
	ModelAlias  string
	SessionData map[string]any
	Reasoning   map[string]any
	WebSearch   HookWebSearchInfo
}

type HookWebSearchInfo struct {
	Mode         string
	MaxUses      int
	FirecrawlKey string
}

// PluginHooks is a struct of function fields that replaces *plugin.Registry
// in the Bridge. It covers the registry methods currently used by the bridge, but not all plugin.Registry methods.
// A zero-value PluginHooks{} is safe to use (all nil fields default to no-ops).
type PluginHooks struct {
	PreprocessInput            func(model string, raw json.RawMessage) json.RawMessage
	RewriteMessages            func(ctx HookContext, messages []anthropic.Message) []anthropic.Message
	InjectTools                func(ctx HookContext) []anthropic.Tool
	MutateRequest              func(ctx HookContext, req *anthropic.MessageRequest)
	RememberResponseContent    func(model string, content []anthropic.ContentBlock, sessionData map[string]any)
	OnResponseContent          func(model string, block anthropic.ContentBlock) (skip bool, reasoningText string)
	PostProcessResponse        func(ctx HookContext, resp *openai.Response)
	TransformError             func(model string, msg string) string
	NewSessionData             func() map[string]any
	PrependThinkingToAssistant func(model string, blocks []anthropic.ContentBlock, pendingSummary []openai.ReasoningItemSummary, sessionData map[string]any) []anthropic.ContentBlock
	PrependThinkingToMessages  func(model string, messages []anthropic.Message, toolCallID string, pendingSummary []openai.ReasoningItemSummary, sessionData map[string]any) []anthropic.Message
	NewStreamStates            func(model string) map[string]any
	ResetStreamBlock           func(model string, index int, streamStates map[string]any)
	OnStreamBlockStart         func(model string, index int, block *anthropic.ContentBlock, streamStates map[string]any) bool
	OnStreamBlockDelta         func(model string, index int, delta anthropic.StreamDelta, streamStates map[string]any) bool
	OnStreamBlockStop          func(model string, index int, streamStates map[string]any) (consumed bool, reasoningText string)
	OnStreamToolCall           func(model string, toolCallID string, streamStates map[string]any)
	OnStreamComplete           func(model string, streamStates map[string]any, outputText string, sessionData map[string]any)
}

// WithDefaults returns a copy of hooks with all nil fields replaced by no-op defaults.
// This ensures PluginHooks{} is safe to use without checking each field for nil.
func (hooks PluginHooks) WithDefaults() PluginHooks {
	if hooks.PreprocessInput == nil {
		hooks.PreprocessInput = func(_ string, raw json.RawMessage) json.RawMessage { return raw }
	}
	if hooks.RewriteMessages == nil {
		hooks.RewriteMessages = func(_ HookContext, messages []anthropic.Message) []anthropic.Message { return messages }
	}
	if hooks.InjectTools == nil {
		hooks.InjectTools = func(HookContext) []anthropic.Tool { return nil }
	}
	if hooks.MutateRequest == nil {
		hooks.MutateRequest = func(HookContext, *anthropic.MessageRequest) {}
	}
	if hooks.RememberResponseContent == nil {
		hooks.RememberResponseContent = func(string, []anthropic.ContentBlock, map[string]any) {}
	}
	if hooks.OnResponseContent == nil {
		hooks.OnResponseContent = func(string, anthropic.ContentBlock) (bool, string) { return false, "" }
	}
	if hooks.PostProcessResponse == nil {
		hooks.PostProcessResponse = func(HookContext, *openai.Response) {}
	}
	if hooks.TransformError == nil {
		hooks.TransformError = func(_ string, msg string) string { return msg }
	}
	if hooks.NewSessionData == nil {
		hooks.NewSessionData = func() map[string]any { return nil }
	}
	if hooks.PrependThinkingToAssistant == nil {
		hooks.PrependThinkingToAssistant = func(_ string, blocks []anthropic.ContentBlock, _ []openai.ReasoningItemSummary, _ map[string]any) []anthropic.ContentBlock {
			return blocks
		}
	}
	if hooks.PrependThinkingToMessages == nil {
		hooks.PrependThinkingToMessages = func(_ string, messages []anthropic.Message, _ string, _ []openai.ReasoningItemSummary, _ map[string]any) []anthropic.Message {
			return messages
		}
	}
	if hooks.NewStreamStates == nil {
		hooks.NewStreamStates = func(string) map[string]any { return nil }
	}
	if hooks.ResetStreamBlock == nil {
		hooks.ResetStreamBlock = func(string, int, map[string]any) {}
	}
	if hooks.OnStreamBlockStart == nil {
		hooks.OnStreamBlockStart = func(string, int, *anthropic.ContentBlock, map[string]any) bool { return false }
	}
	if hooks.OnStreamBlockDelta == nil {
		hooks.OnStreamBlockDelta = func(string, int, anthropic.StreamDelta, map[string]any) bool { return false }
	}
	if hooks.OnStreamBlockStop == nil {
		hooks.OnStreamBlockStop = func(string, int, map[string]any) (bool, string) { return false, "" }
	}
	if hooks.OnStreamToolCall == nil {
		hooks.OnStreamToolCall = func(string, string, map[string]any) {}
	}
	if hooks.OnStreamComplete == nil {
		hooks.OnStreamComplete = func(string, map[string]any, string, map[string]any) {}
	}
	return hooks
}
