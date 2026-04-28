// Package websearchinjected extracts the "injected" web search mode into a
// self-contained extension. When enabled, the bridge injects tavily_search
// and firecrawl_fetch as function-type tools instead of relying on the
// upstream Anthropic provider's web_search_20250305 server tool.
//
// The extension:
//   - Provides tool definitions for the bridge to inject
//   - Wraps the Anthropic client with the Orchestrator for server-side search execution
package websearchinjected

import (
	"moonbridge/internal/extension/websearch"
	"moonbridge/internal/protocol/anthropic"
)

// IsEnabled checks whether the injected web search extension should activate.
// cfg must expose WebSearchInjected() bool.
func IsEnabled(cfg interface{ WebSearchInjected() bool }) bool {
	return cfg.WebSearchInjected()
}

// InjectTools returns function-type tools to inject into the Anthropic request
// when the bridge encounters a web_search / web_search_preview tool from Codex.
// Delegates to websearch.InjectedTools for the actual tool definitions.
func InjectTools(firecrawlKey string) []anthropic.Tool {
	return websearch.InjectedTools(firecrawlKey)
}

// WrapProvider wraps an Anthropic client with the injected search orchestrator.
// The returned *websearch.Orchestrator implements the same CreateMessage /
// StreamMessage interface as *anthropic.Client.
func WrapProvider(client *anthropic.Client, tavilyKey, firecrawlKey string, maxRounds int) *websearch.Orchestrator {
	return websearch.NewInjectedOrchestrator(websearch.OrchestratorConfig{
		Anthropic:       client,
		TavilyKey:       tavilyKey,
		FirecrawlKey:    firecrawlKey,
		SearchMaxRounds: maxRounds,
	})
}
