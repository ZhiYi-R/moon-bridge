package runtime_test

import (
	"testing"

	"moonbridge/internal/config"
	"moonbridge/internal/service/provider"
	"moonbridge/internal/service/runtime"
	"moonbridge/internal/service/stats"
)

func TestNewRuntimeAndCurrent(t *testing.T) {
	cfg := config.Config{
		Mode:         config.ModeTransform,
		Addr:         "127.0.0.1:38440",
		DefaultModel: "test-model",
		Routes: map[string]config.RouteEntry{
			"test": {Provider: "default", Model: "claude-test"},
		},
		ProviderDefs: map[string]config.ProviderDef{
			"default": {
				BaseURL: "https://api.anthropic.test",
				APIKey:  "test-key",
				Models:  map[string]config.ModelMeta{"claude-test": {ContextWindow: 100000}},
			},
		},
		Cache: config.CacheConfig{Mode: "off"},
	}

	pm, err := provider.NewProviderManager(
		map[string]provider.ProviderConfig{
			"default": {
				BaseURL:    "https://api.anthropic.test",
				APIKey:     "test-key",
				ModelNames: []string{"claude-test"},
			},
		},
		map[string]provider.ModelRoute{
			"test": {Provider: "default", Name: "claude-test"},
		},
	)
	if err != nil {
		t.Fatalf("NewProviderManager() error = %v", err)
	}

	pricing := map[string]stats.ModelPricing{
		"test": {InputPrice: 1.0, OutputPrice: 2.0},
	}

	rt := runtime.NewRuntime(cfg, pm, pricing)
	if rt == nil {
		t.Fatal("NewRuntime() returned nil")
	}

	snap := rt.Current()
	if snap == nil {
		t.Fatal("Current() returned nil")
	}

	// Verify snapshot contents.
	if snap.Config.DefaultModel != "test-model" {
		t.Errorf("Config.DefaultModel = %q, want %q", snap.Config.DefaultModel, "test-model")
	}
	if snap.ProviderMgr == nil {
		t.Fatal("ProviderMgr is nil")
	}
	if snap.Pricing == nil {
		t.Fatal("Pricing is nil")
	}
	if p, ok := snap.Pricing["test"]; !ok || p.InputPrice != 1.0 {
		t.Errorf("Pricing[test].InputPrice = %f, want 1.0", p.InputPrice)
	}
}

func TestReloadAtomicSwap(t *testing.T) {
	cfg := config.Config{
		Mode:         config.ModeTransform,
		Addr:         "127.0.0.1:38440",
		DefaultModel: "v1-model",
		Routes: map[string]config.RouteEntry{
			"v1": {Provider: "default", Model: "claude-v1"},
		},
		ProviderDefs: map[string]config.ProviderDef{
			"default": {
				BaseURL: "https://api.anthropic.test",
				APIKey:  "test-key",
				Models:  map[string]config.ModelMeta{"claude-v1": {ContextWindow: 100000}},
			},
		},
		Cache: config.CacheConfig{Mode: "off"},
	}

	pm, _ := provider.NewProviderManager(
		map[string]provider.ProviderConfig{
			"default": {
				BaseURL:    "https://api.anthropic.test",
				APIKey:     "test-key",
				ModelNames: []string{"claude-v1"},
			},
		},
		map[string]provider.ModelRoute{
			"v1": {Provider: "default", Name: "claude-v1"},
		},
	)

	rt := runtime.NewRuntime(cfg, pm, nil)

	// Verify initial state.
	snap1 := rt.Current()
	if snap1.Config.DefaultModel != "v1-model" {
		t.Errorf("initial DefaultModel = %q, want %q", snap1.Config.DefaultModel, "v1-model")
	}

	// Reload with new config.
	newCfg := config.Config{
		Mode:         config.ModeTransform,
		Addr:         "127.0.0.1:38440",
		DefaultModel: "v2-model",
		Routes: map[string]config.RouteEntry{
			"v2": {Provider: "default", Model: "claude-v2"},
		},
		ProviderDefs: map[string]config.ProviderDef{
			"default": {
				BaseURL: "https://api.anthropic.test",
				APIKey:  "test-key",
				Models:  map[string]config.ModelMeta{"claude-v2": {ContextWindow: 200000}},
			},
		},
		Cache: config.CacheConfig{Mode: "off"},
	}

	if err := rt.Reload(newCfg); err != nil {
		t.Fatalf("Reload() error = %v", err)
	}

	// Verify atomic swap: new snapshot reflects new config.
	snap2 := rt.Current()
	if snap2.Config.DefaultModel != "v2-model" {
		t.Errorf("after reload DefaultModel = %q, want %q", snap2.Config.DefaultModel, "v2-model")
	}
	if _, exists := snap2.Config.Routes["v2"]; !exists {
		t.Error("after reload, route 'v2' not found")
	}
	if _, exists := snap2.Config.Routes["v1"]; exists {
		t.Error("after reload, stale route 'v1' still present")
	}

	// Verify old snapshot pointer still points to old data (immutable).
	if snap1.Config.DefaultModel != "v1-model" {
		t.Errorf("old snapshot mutated: DefaultModel = %q", snap1.Config.DefaultModel)
	}
}

func TestReloadWithInvalidConfigReturnsError(t *testing.T) {
	cfg := config.Config{
		Mode: config.ModeTransform,
		Addr: "127.0.0.1:38440",
		Routes: map[string]config.RouteEntry{
			"test": {Provider: "default", Model: "claude-test"},
		},
		ProviderDefs: map[string]config.ProviderDef{
			"default": {
				BaseURL: "https://api.anthropic.test",
				APIKey:  "test-key",
				Models:  map[string]config.ModelMeta{"claude-test": {ContextWindow: 100000}},
			},
		},
		Cache: config.CacheConfig{Mode: "off"},
	}

	pm, _ := provider.NewProviderManager(
		map[string]provider.ProviderConfig{
			"default": {
				BaseURL:    "https://api.anthropic.test",
				APIKey:     "test-key",
				ModelNames: []string{"claude-test"},
			},
		},
		map[string]provider.ModelRoute{
			"test": {Provider: "default", Name: "claude-test"},
		},
	)

	rt := runtime.NewRuntime(cfg, pm, nil)

	// Save initial snapshot pointer for stale reference verification.
	initialSnap := rt.Current()

	// Reload with invalid mode (empty mode fails validation).
	invalidCfg := config.Config{
		Mode:  config.Mode(""),
		Cache: config.CacheConfig{Mode: "off"},
	}

	err := rt.Reload(invalidCfg)
	if err == nil {
		t.Fatal("Reload() with invalid config should return error")
	}
	t.Logf("Expected error: %v", err)

	// Verify old snapshot is preserved (no nil pointer, same reference).
	snap := rt.Current()
	if snap == nil {
		t.Fatal("Current() returned nil after failed Reload")
	}
	if snap != initialSnap {
		t.Error("snapshot pointer changed after failed Reload")
	}
}
