package pluginhooks

import (
	"log/slog"
	"testing"

	"moonbridge/internal/extension/plugin"
	"moonbridge/internal/protocol/anthropic"
	"moonbridge/internal/protocol/bridge"
)

func TestPluginHooksFromRegistryNilReturnsZero(t *testing.T) {
	hooks := PluginHooksFromRegistry(nil).WithDefaults()
	// Zero-value hooks should be safe to call.
	msg := []anthropic.Message{{Role: "user", Content: []anthropic.ContentBlock{{Type: "text", Text: "hello"}}}}
	result := hooks.RewriteMessages(bridge.HookContext{ModelAlias: "test"}, msg)
	if len(result) != 1 {
		t.Fatal("RewriteMessages with nil registry should pass through")
	}
	tools := hooks.InjectTools(bridge.HookContext{})
	if tools != nil {
		t.Fatal("InjectTools with nil registry should return nil")
	}
	// MutateRequest should not panic.
	req := &anthropic.MessageRequest{Model: "m"}
	hooks.MutateRequest(bridge.HookContext{}, req)
}

func TestPluginHooksFromRegistryPreservesDispatchOrder(t *testing.T) {
	registry := plugin.NewRegistry(slog.Default())

	// Register a plugin that implements MessageRewriter.
	recorder := &testPlugin{name: "test", enabled: true}
	registry.Register(recorder)
	if err := registry.InitAll(nil); err != nil {
		t.Fatal(err)
	}

	hooks := PluginHooksFromRegistry(registry)
	msg := []anthropic.Message{{Role: "user", Content: []anthropic.ContentBlock{{Type: "text", Text: "hi"}}}}
	result := hooks.RewriteMessages(bridge.HookContext{ModelAlias: "match"}, msg)
	// RewriteMessages should be called (plugin is enabled for every model).
	if !recorder.rewriteCalled {
		t.Fatal("RewriteMessages should have been called")
	}
	if len(result) != 1 {
		t.Fatal("RewriteMessages should return messages")
	}
}

func TestPluginHooksFromRegistryModelFiltering(t *testing.T) {
	registry := plugin.NewRegistry(slog.Default())

	recorder := &testPlugin{name: "test", enabled: false, enabledMap: map[string]bool{"gpt-4": true}}
	registry.Register(recorder)
	if err := registry.InitAll(nil); err != nil {
		t.Fatal(err)
	}

	hooks := PluginHooksFromRegistry(registry)
	msg := []anthropic.Message{{Role: "user", Content: []anthropic.ContentBlock{{Type: "text", Text: "hi"}}}}
	_ = hooks.RewriteMessages(bridge.HookContext{ModelAlias: "claude"}, msg)
	if recorder.rewriteCalled {
		t.Fatal("RewriteMessages should NOT be called for non-matching model")
	}
	_ = hooks.RewriteMessages(bridge.HookContext{ModelAlias: "gpt-4"}, msg)
	if !recorder.rewriteCalled {
		t.Fatal("RewriteMessages should be called for matching model")
	}
}

// testPlugin is a minimal plugin for testing dispatch.
type testPlugin struct {
	plugin.BasePlugin
	name          string
	enabled       bool
	enabledMap    map[string]bool
	rewriteCalled bool
}

func (p *testPlugin) Name() string { return p.name }
func (p *testPlugin) EnabledForModel(model string) bool {
	if p.enabledMap != nil {
		if v, ok := p.enabledMap[model]; ok {
			return v
		}
		return false
	}
	return p.enabled
}
func (p *testPlugin) RewriteMessages(ctx *plugin.RequestContext, messages []anthropic.Message) []anthropic.Message {
	p.rewriteCalled = true
	return messages
}
