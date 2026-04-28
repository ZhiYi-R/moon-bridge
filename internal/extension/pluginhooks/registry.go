package pluginhooks

import (
	"moonbridge/internal/extension/plugin"
	"moonbridge/internal/foundation/openai"
	"moonbridge/internal/protocol/anthropic"
	"moonbridge/internal/protocol/bridge"
)

// PluginHooksFromRegistry wraps a *plugin.Registry into a bridge.PluginHooks struct,
// allowing the bridge package to use plugin functionality without importing plugin directly.
// Returns a zero-value PluginHooks if registry is nil.
func PluginHooksFromRegistry(registry *plugin.Registry) bridge.PluginHooks {
	if registry == nil {
		return bridge.PluginHooks{}
	}
	return bridge.PluginHooks{
		PreprocessInput: registry.PreprocessInput,
		RewriteMessages: func(ctx bridge.HookContext, messages []anthropic.Message) []anthropic.Message {
			return registry.RewriteMessages(toPluginContext(ctx), messages)
		},
		InjectTools: func(ctx bridge.HookContext) []anthropic.Tool {
			return registry.InjectTools(toPluginContext(ctx))
		},
		MutateRequest: func(ctx bridge.HookContext, req *anthropic.MessageRequest) {
			registry.MutateRequest(toPluginContext(ctx), req)
		},
		RememberResponseContent: registry.RememberResponseContent,
		OnResponseContent:       registry.OnResponseContent,
		PostProcessResponse: func(ctx bridge.HookContext, resp *openai.Response) {
			registry.PostProcessResponse(toPluginContext(ctx), resp)
		},
		TransformError:             registry.TransformError,
		NewSessionData:             registry.NewSessionData,
		PrependThinkingToAssistant: registry.PrependThinkingToAssistant,
		PrependThinkingToMessages:  registry.PrependThinkingToMessages,
		NewStreamStates:            registry.NewStreamStates,
		ResetStreamBlock:           registry.ResetStreamBlock,
		OnStreamBlockStart:         registry.OnStreamBlockStart,
		OnStreamBlockDelta:         registry.OnStreamBlockDelta,
		OnStreamBlockStop:          registry.OnStreamBlockStop,
		OnStreamToolCall:           registry.OnStreamToolCall,
		OnStreamComplete:           registry.OnStreamComplete,
	}
}

func toPluginContext(ctx bridge.HookContext) *plugin.RequestContext {
	return &plugin.RequestContext{
		ModelAlias:  ctx.ModelAlias,
		SessionData: ctx.SessionData,
		Reasoning:   ctx.Reasoning,
		WebSearch: plugin.WebSearchInfo{
			Mode:         ctx.WebSearch.Mode,
			MaxUses:      ctx.WebSearch.MaxUses,
			FirecrawlKey: ctx.WebSearch.FirecrawlKey,
		},
	}
}
