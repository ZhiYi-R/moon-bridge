package app

import (
	"context"
	"database/sql"
	"log/slog"
	"testing"

	"github.com/ZhiYi-R/moon-bridge/internal/config"
	"github.com/ZhiYi-R/moon-bridge/internal/db"
	dbd1 "github.com/ZhiYi-R/moon-bridge/internal/extension/db/d1"
	dbsqlite "github.com/ZhiYi-R/moon-bridge/internal/extension/db/sqlite"
	"github.com/ZhiYi-R/moon-bridge/internal/extension/deepseek_v4"
	kimiworkaround "github.com/ZhiYi-R/moon-bridge/internal/extension/kimi_workaround"
	"github.com/ZhiYi-R/moon-bridge/internal/extension/plugin"
	"github.com/ZhiYi-R/moon-bridge/internal/extension/visual"
	"github.com/ZhiYi-R/moon-bridge/internal/format"
)

func ptrBool(v bool) *bool { return &v }

// TestDeepSeekPlugin_EnabledForModelViaResolver verifies that the isEnabled
// resolver passed to deepseekv4.NewPlugin correctly delegates to
// config.ExtensionEnabled, which resolves "enabled: true" from model-level
// config through the ProviderDefs catalog.
func TestDeepSeekPlugin_EnabledForModelViaResolver(t *testing.T) {
	// Simulate the config structure used in production:
	// a provider offering a model with deepseek_v4 extension enabled.
	cfg := config.Config{
		ProviderDefs: map[string]config.ProviderDef{
			"test-provider": {
				Protocol: "anthropic",
				BaseURL:  "https://test.example.com",
				Models: map[string]config.ModelMeta{
					"enabled-model": {
						Extensions: map[string]config.ExtensionSettings{
							"deepseek_v4": {Enabled: ptrBool(true)},
						},
					},
					"disabled-model": {
						Extensions: map[string]config.ExtensionSettings{
							"deepseek_v4": {Enabled: ptrBool(false)},
						},
					},
				},
			},
		},
	}

	reg := BuiltinExtensions().NewRegistry(slog.Default(), cfg)
	if reg == nil {
		t.Fatal("NewRegistry returned nil")
	}

	p := reg.Plugin(deepseekv4.PluginName)
	if p == nil {
		t.Fatalf("plugin %q not found", deepseekv4.PluginName)
	}

	if !p.EnabledForModel("enabled-model") {
		t.Error("EnabledForModel('enabled-model') = false, want true")
	}
	if p.EnabledForModel("disabled-model") {
		t.Error("EnabledForModel('disabled-model') = true, want false")
	}
	if p.EnabledForModel("unknown-model") {
		t.Error("EnabledForModel('unknown-model') = true, want false")
	}
}

func TestPluginsReadCurrentRuntimeConfig(t *testing.T) {
	initialCfg := config.Config{
		ProviderDefs: map[string]config.ProviderDef{
			"anthropic": {
				BaseURL: "https://anthropic.example.test",
				APIKey:  "test-key",
				Models: map[string]config.ModelMeta{
					"claude-3-5-sonnet": {},
				},
			},
			"vision": {
				BaseURL: "https://vision.example.test",
				APIKey:  "vision-key",
				Models: map[string]config.ModelMeta{
					"vision-model": {},
				},
			},
		},
		Routes: map[string]config.RouteEntry{
			"moonbridge": {
				Provider: "anthropic",
				Model:    "claude-3-5-sonnet",
				Extensions: map[string]config.ExtensionSettings{
					deepseekv4.PluginName:     {Enabled: ptrBool(false)},
					kimiworkaround.PluginName: {Enabled: ptrBool(false)},
					visual.PluginName:         {Enabled: ptrBool(false)},
				},
			},
		},
		Extensions: map[string]config.ExtensionSettings{
			deepseekv4.PluginName:     {Enabled: ptrBool(false)},
			kimiworkaround.PluginName: {Enabled: ptrBool(false)},
			visual.PluginName:         {Enabled: ptrBool(false)},
		},
	}
	runtimeCfg := initialCfg
	runtimeCfg.Routes["moonbridge"] = config.RouteEntry{
		Provider: "anthropic",
		Model:    "claude-3-5-sonnet",
		Extensions: map[string]config.ExtensionSettings{
			deepseekv4.PluginName: {
				Enabled: ptrBool(true),
				RawConfig: map[string]any{
					"reinforce_instructions": true,
					"reinforce_prompt":       "runtime reinforce",
				},
			},
			kimiworkaround.PluginName: {
				Enabled: ptrBool(true),
				RawConfig: map[string]any{
					"max_tool_rounds":    3,
					"convergence_margin": 0.5,
				},
			},
			visual.PluginName: {
				Enabled: ptrBool(true),
				RawConfig: map[string]any{
					"provider":   "vision",
					"model":      "vision-model",
					"max_rounds": 6,
					"max_tokens": 4096,
				},
			},
		},
	}

	reg := BuiltinExtensions().NewRegistry(slog.Default(), initialCfg)
	reg.SetCurrentConfigProvider(func() config.Config { return runtimeCfg })
	if err := reg.InitAll(&initialCfg); err != nil {
		t.Fatalf("InitAll() error = %v", err)
	}

	dsPlugin, ok := reg.Plugin(deepseekv4.PluginName).(*deepseekv4.DSPlugin)
	if !ok {
		t.Fatalf("plugin %q is not *DSPlugin", deepseekv4.PluginName)
	}
	if !dsPlugin.EnabledForModel("moonbridge") {
		t.Fatal("deepseek_v4 should read enabled state from current config")
	}
	rewritten := dsPlugin.RewriteMessages(&plugin.RequestContext{ModelAlias: "moonbridge"}, []format.CoreMessage{{
		Role:    "user",
		Content: []format.CoreContentBlock{{Type: "text", Text: "hi"}},
	}})
	if len(rewritten) != 2 || rewritten[0].Content[0].Text != "runtime reinforce" {
		t.Fatalf("deepseek rewrite did not use runtime prompt: %+v", rewritten)
	}

	kimiPlugin, ok := reg.Plugin(kimiworkaround.PluginName).(*kimiworkaround.Plugin)
	if !ok {
		t.Fatalf("plugin %q is not *Plugin", kimiworkaround.PluginName)
	}
	if !kimiPlugin.EnabledForModel("moonbridge") {
		t.Fatal("kimi_workaround should read enabled state from current config")
	}
	kimiMsgs := []format.CoreMessage{
		{Role: "user", Content: []format.CoreContentBlock{{Type: "text", Text: "hi"}}},
		{Role: "assistant", Content: []format.CoreContentBlock{{Type: "tool_use", ToolUseID: "call_1", ToolName: "exec"}}},
		{Role: "tool", Content: []format.CoreContentBlock{{Type: "tool_result", ToolUseID: "call_1", ToolResultContent: []format.CoreContentBlock{{Type: "text", Text: "ok"}}}}},
		{Role: "assistant", Content: []format.CoreContentBlock{{Type: "tool_use", ToolUseID: "call_2", ToolName: "exec"}}},
		{Role: "tool", Content: []format.CoreContentBlock{{Type: "tool_result", ToolUseID: "call_2", ToolResultContent: []format.CoreContentBlock{{Type: "text", Text: "ok"}}}}},
	}
	limited := kimiPlugin.RewriteMessages(&plugin.RequestContext{ModelAlias: "moonbridge"}, kimiMsgs)
	lastResult := limited[len(limited)-1].Content[0].ToolResultContent
	if len(lastResult) != 3 || lastResult[2].Text != kimiworkaround.DefaultLimitPrompt {
		t.Fatalf("kimi rewrite did not use runtime limit config: %+v", lastResult)
	}

	visualPlugin, ok := reg.Plugin(visual.PluginName).(*visual.Plugin)
	if !ok {
		t.Fatalf("plugin %q is not *Plugin", visual.PluginName)
	}
	if !visualPlugin.EnabledForModel("moonbridge") {
		t.Fatal("visual should read enabled state from current config")
	}
	visCfg, enabled := visual.ConfigForModelFromResolvedConfig(runtimeCfg, "moonbridge")
	if !enabled || visCfg.Provider != "vision" || visCfg.Model != "vision-model" {
		t.Fatalf("visual runtime config = %+v, enabled=%v", visCfg, enabled)
	}
}

func TestResolvePersistenceActiveProvider(t *testing.T) {
	sqliteProvider := &stubDBProvider{name: dbsqlite.PluginName, workerBound: false}
	d1Provider := &stubDBProvider{name: dbd1.PluginName, workerBound: true}

	if got := ResolvePersistenceActiveProvider("custom", []db.Provider{sqliteProvider, d1Provider}); got != "custom" {
		t.Fatalf("explicit active provider = %q, want custom", got)
	}
	if got := ResolvePersistenceActiveProvider("", []db.Provider{sqliteProvider}); got != dbsqlite.PluginName {
		t.Fatalf("single provider auto-select = %q", got)
	}
	if got := ResolvePersistenceActiveProvider("", []db.Provider{sqliteProvider, d1Provider}); got != dbd1.PluginName {
		t.Fatalf("worker-bound provider auto-select = %q", got)
	}
	if got := ResolvePersistenceActiveProvider("", []db.Provider{sqliteProvider, &stubDBProvider{name: "other", workerBound: false}}); got != "" {
		t.Fatalf("ambiguous providers should stay empty, got %q", got)
	}
}

type stubDBProvider struct {
	name        string
	workerBound bool
}

func (p *stubDBProvider) Name() string               { return p.name }
func (p *stubDBProvider) Dialect() db.Dialect        { return "" }
func (p *stubDBProvider) Open(context.Context) error { return nil }
func (p *stubDBProvider) DB() *sql.DB                { return nil }
func (p *stubDBProvider) Ping(context.Context) error { return nil }
func (p *stubDBProvider) Close() error               { return nil }
func (p *stubDBProvider) Features() db.Features      { return db.Features{WorkerBound: p.workerBound} }
