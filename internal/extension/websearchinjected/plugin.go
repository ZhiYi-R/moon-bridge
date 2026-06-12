package websearchinjected

import (
	"moonbridge/internal/extension/plugin"
	"moonbridge/internal/extension/websearch"
	"moonbridge/internal/format"
	"moonbridge/internal/protocol/anthropic"
	"moonbridge/internal/service/provider"
)

const PluginName = "web_search_injected"

// WSInjectedPlugin implements the plugin system for injected web search.
type WSInjectedPlugin struct {
	plugin.BasePlugin
	isEnabled func(modelAlias string) bool
}

// NewPlugin creates a web search injected plugin.
// isEnabled should return true when the resolved web search mode for the
// model is "injected".
func NewPlugin(isEnabled func(string) bool) *WSInjectedPlugin {
	return &WSInjectedPlugin{isEnabled: isEnabled}
}

func (p *WSInjectedPlugin) Name() string                      { return PluginName }
func (p *WSInjectedPlugin) EnabledForModel(model string) bool { return p.isEnabled(model) }

// --- ToolInjector ---

func (p *WSInjectedPlugin) InjectTools(ctx *plugin.RequestContext) []format.CoreTool {
	return CoreTools(ctx.WebSearch.FirecrawlKey)
}

// --- ProviderWrapper ---

func (p *WSInjectedPlugin) WrapProvider(ctx *plugin.RequestContext, wrapped any) any {
	// Try AnthropicClientAccessor first (for ProviderClient-wrapped clients).
	if acc, ok := wrapped.(provider.AnthropicClientAccessor); ok {
		client := acc.AnthropicClient()
		return websearch.NewInjectedOrchestrator(websearch.OrchestratorConfig{
			Anthropic:       client,
			TavilyKey:       "", // resolved from config at call site
			FirecrawlKey:    ctx.WebSearch.FirecrawlKey,
			SearchMaxRounds: 5,
		})
	}
	// Fall back to direct *anthropic.Client assertion.
	if client, ok := wrapped.(*anthropic.Client); ok {
		return websearch.NewInjectedOrchestrator(websearch.OrchestratorConfig{
			Anthropic:       client,
			TavilyKey:       "", // resolved from config at call site
			FirecrawlKey:    ctx.WebSearch.FirecrawlKey,
			SearchMaxRounds: 5,
		})
	}
	return wrapped
}

// Compile-time interface checks.
var (
	_ plugin.Plugin          = (*WSInjectedPlugin)(nil)
	_ plugin.ToolInjector    = (*WSInjectedPlugin)(nil)
	_ plugin.ProviderWrapper = (*WSInjectedPlugin)(nil)
)
