package websearchinjected

import (
	"testing"

	"moonbridge/internal/extension/plugin"
	"moonbridge/internal/protocol/anthropic"
)

func TestNewPluginReturnsCorrectName(t *testing.T) {
	p := NewPlugin(func(model string) bool { return true })
	if p.Name() != PluginName {
		t.Fatalf("expected plugin name %q, got %q", PluginName, p.Name())
	}
}

func TestPluginEnabledForModel(t *testing.T) {
	p := NewPlugin(func(model string) bool { return true })
	if !p.EnabledForModel("any-model") {
		t.Fatal("plugin should be enabled")
	}

	p2 := NewPlugin(func(model string) bool { return false })
	if p2.EnabledForModel("any-model") {
		t.Fatal("plugin should not be enabled")
	}
}

func TestInjectToolsReturnsTools(t *testing.T) {
	p := NewPlugin(func(model string) bool { return true })
	ctx := &plugin.RequestContext{
		WebSearch: plugin.WebSearchInfo{FirecrawlKey: "fc-test-key"},
	}
	tools := p.InjectTools(ctx)
	if len(tools) < 1 {
		t.Fatal("InjectTools should return at least one tool")
	}
}

func TestInjectToolsWithEmptyKey(t *testing.T) {
	p := NewPlugin(func(model string) bool { return true })
	ctx := &plugin.RequestContext{
		WebSearch: plugin.WebSearchInfo{FirecrawlKey: ""},
	}
	tools := p.InjectTools(ctx)
	if len(tools) == 0 {
		t.Fatal("InjectTools should return tools even with empty key")
	}
}

func TestWrapProviderWrapsAnthropicClient(t *testing.T) {
	p := NewPlugin(func(model string) bool { return true })
	client := &anthropic.Client{}
	ctx := &plugin.RequestContext{
		WebSearch: plugin.WebSearchInfo{FirecrawlKey: "fc-key"},
	}
	wrapped := p.WrapProvider(ctx, client)
	if wrapped == client {
		t.Fatal("WrapProvider should return a wrapped orchestrator, not raw client")
	}
}

func TestWrapProviderPassesNonClient(t *testing.T) {
	p := NewPlugin(func(model string) bool { return true })
	unknown := "not-a-client"
	ctx := &plugin.RequestContext{
		WebSearch: plugin.WebSearchInfo{FirecrawlKey: "fc-key"},
	}
	result := p.WrapProvider(ctx, unknown)
	if result != unknown {
		t.Fatal("WrapProvider should pass through non-Anthropic client unchanged")
	}
}
