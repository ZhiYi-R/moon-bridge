package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/ZhiYi-R/moon-bridge/internal/config"
	"github.com/ZhiYi-R/moon-bridge/internal/service/store"
)

func TestSQLiteStoreSeedLoadRoundtrip(t *testing.T) {
	logger := testLogger(t)
	c := store.NewConfigStoreConsumer(logger)

	// Create tables.
	ts := newTestStore(t, "config_store", c.Tables())
	if err := c.BindStore(ts); err != nil {
		t.Fatalf("BindStore() error = %v", err)
	}

	cs := c.Store()
	if cs == nil {
		t.Fatal("Store() returned nil")
	}

	// LoadAll on empty DB should fail (missing mode).
	_, err := cs.LoadAll()
	if err == nil {
		t.Fatal("LoadAll() on empty DB should fail (missing mode)")
	}

	// Seed a config.
	cfg := buildTestConfig()
	if err := cs.SeedFromConfig(cfg); err != nil {
		t.Fatalf("SeedFromConfig() error = %v", err)
	}

	// LoadAll and verify.
	cfg2, err := cs.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll() after seed error = %v", err)
	}
	if cfg2 == nil {
		t.Fatal("LoadAll() returned nil")
	}

	// Verify key fields.
	if cfg.Mode != cfg2.Mode {
		t.Fatalf("Mode: got %q, want %q", cfg2.Mode, cfg.Mode)
	}
	if cfg.TraceRequests != cfg2.TraceRequests {
		t.Fatalf("TraceRequests: got %v, want %v", cfg2.TraceRequests, cfg.TraceRequests)
	}
	if cfg.LogLevel != cfg2.LogLevel {
		t.Fatalf("LogLevel: got %q, want %q", cfg2.LogLevel, cfg.LogLevel)
	}
	if cfg.Addr != cfg2.Addr {
		t.Fatalf("Addr: got %q, want %q", cfg2.Addr, cfg.Addr)
	}
	if cfg.AuthToken != cfg2.AuthToken {
		t.Fatalf("AuthToken: got %q, want %q", cfg2.AuthToken, cfg.AuthToken)
	}
	if cfg.MaxSessions != cfg2.MaxSessions {
		t.Fatalf("MaxSessions: got %d, want %d", cfg2.MaxSessions, cfg.MaxSessions)
	}
	if cfg.SessionTTL != cfg2.SessionTTL {
		t.Fatalf("SessionTTL: got %q, want %q", cfg2.SessionTTL, cfg.SessionTTL)
	}
	if cfg.Defaults.Model != cfg2.Defaults.Model {
		t.Fatalf("Defaults.Model: got %q, want %q", cfg2.Defaults.Model, cfg.Defaults.Model)
	}

	// Models.
	if len(cfg2.Models) != len(cfg.Models) {
		t.Fatalf("Models count: got %d, want %d", len(cfg2.Models), len(cfg.Models))
	}
	for slug := range cfg.Models {
		if _, ok := cfg2.Models[slug]; !ok {
			t.Fatalf("model %q missing after reload", slug)
		}
	}

	// Providers + Offers.
	if len(cfg2.ProviderDefs) != len(cfg.ProviderDefs) {
		t.Fatalf("ProviderDefs count: got %d, want %d", len(cfg2.ProviderDefs), len(cfg.ProviderDefs))
	}
	for key, def := range cfg.ProviderDefs {
		def2, ok := cfg2.ProviderDefs[key]
		if !ok {
			t.Fatalf("provider %q missing after reload", key)
		}
		if def2.BaseURL != def.BaseURL {
			t.Fatalf("provider %q BaseURL: got %q, want %q", key, def2.BaseURL, def.BaseURL)
		}
		if len(def2.Offers) != len(def.Offers) {
			t.Fatalf("provider %q offers count: got %d, want %d", key, len(def2.Offers), len(def.Offers))
		}
	}

	// Routes.
	if len(cfg2.Routes) != len(cfg.Routes) {
		t.Fatalf("Routes count: got %d, want %d", len(cfg2.Routes), len(cfg.Routes))
	}
	for alias, route := range cfg.Routes {
		r2, ok := cfg2.Routes[alias]
		if !ok {
			t.Fatalf("route %q missing after reload", alias)
		}
		if r2.Provider != route.Provider {
			t.Fatalf("route %q Provider: got %q, want %q", alias, r2.Provider, route.Provider)
		}
	}
}

func TestSQLiteStoreExportYAML(t *testing.T) {
	logger := testLogger(t)
	c := store.NewConfigStoreConsumer(logger)

	ts := newTestStore(t, "config_store", c.Tables())
	if err := c.BindStore(ts); err != nil {
		t.Fatalf("BindStore() error = %v", err)
	}
	cs := c.Store()
	if cs == nil {
		t.Fatal("Store() returned nil")
	}

	// Seed and export.
	cfg := buildTestConfig()
	if err := cs.SeedFromConfig(cfg); err != nil {
		t.Fatalf("SeedFromConfig() error = %v", err)
	}

	// Export with secrets.
	data, err := cs.ExportYAML(true)
	if err != nil {
		t.Fatalf("ExportYAML(true) error = %v", err)
	}
	if len(data) == 0 {
		t.Fatal("ExportYAML() returned empty data")
	}

	// Verify the YAML can be loaded back.
	_, err = config.LoadFromYAML(data)
	if err != nil {
		t.Fatalf("LoadFromYAML(export) error = %v", err)
	}

	// Export without secrets — API keys should be masked.
	dataMasked, err := cs.ExportYAML(false)
	if err != nil {
		t.Fatalf("ExportYAML(false) error = %v", err)
	}

	// Verify masking: "sk-ant-test-key-xxx" → "sk-a****xxx"
	exportStr := string(dataMasked)
	if contains(exportStr, "sk-ant-test-key-xxx") {
		t.Fatal("ExportYAML(false) leaked API key")
	}
}

func TestSQLiteStoreStageAndDiscardChanges(t *testing.T) {
	logger := testLogger(t)
	c := store.NewConfigStoreConsumer(logger)

	ts := newTestStore(t, "config_store", c.Tables())
	if err := c.BindStore(ts); err != nil {
		t.Fatalf("BindStore() error = %v", err)
	}
	cs := c.Store()
	if cs == nil {
		t.Fatal("Store() returned nil")
	}

	// Stage a change.
	id, err := cs.StageChange(store.ChangeRow{
		Action:    "update",
		Resource:  "provider",
		TargetKey: "test-provider",
		After:     `{"base_url":"https://example.com","api_key":"sk-xxx"}`,
	})
	if err != nil {
		t.Fatalf("StageChange() error = %v", err)
	}
	if id <= 0 {
		t.Fatalf("StageChange() returned invalid id %d", id)
	}

	// List pending.
	changes, err := cs.ListPendingChanges()
	if err != nil {
		t.Fatalf("ListPendingChanges() error = %v", err)
	}
	if len(changes) != 1 {
		t.Fatalf("ListPendingChanges() returned %d changes, want 1", len(changes))
	}
	if changes[0].Action != "update" {
		t.Fatalf("Change action = %q, want %q", changes[0].Action, "update")
	}
	if changes[0].Applied {
		t.Fatal("Change should not be applied yet")
	}

	// Discard.
	if err := cs.DiscardPendingChanges(); err != nil {
		t.Fatalf("DiscardPendingChanges() error = %v", err)
	}

	changes, err = cs.ListPendingChanges()
	if err != nil {
		t.Fatalf("ListPendingChanges() after discard error = %v", err)
	}
	if len(changes) != 0 {
		t.Fatalf("ListPendingChanges() after discard returned %d changes, want 0", len(changes))
	}
}

func TestSQLiteStoreSaveConfigOverwritesCurrentConfig(t *testing.T) {
	cs := newConfigStoreForTest(t)
	initial := buildTestConfig()
	if err := cs.SeedFromConfig(initial); err != nil {
		t.Fatalf("SeedFromConfig() error = %v", err)
	}
	revBefore, err := cs.CurrentRevision()
	if err != nil {
		t.Fatalf("CurrentRevision() before save error = %v", err)
	}
	if revBefore == "" {
		t.Fatal("CurrentRevision() before save returned empty revision")
	}

	next := buildSavedGraphConfig()
	revAfter, err := cs.SaveConfig(context.Background(), next)
	if err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}
	if revAfter == "" {
		t.Fatal("SaveConfig() returned empty revision")
	}
	if revAfter == revBefore {
		t.Fatalf("SaveConfig() revision = %q, want different from %q", revAfter, revBefore)
	}

	stored, err := cs.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll() after SaveConfig error = %v", err)
	}
	if stored.Defaults.MaxTokens != 8192 {
		t.Fatalf("Defaults.MaxTokens = %d, want 8192", stored.Defaults.MaxTokens)
	}
	if _, ok := stored.ProviderDefs["anthropic"]; ok {
		t.Fatal("old provider anthropic still exists after direct save")
	}
	if _, ok := stored.Routes["moonbridge"]; ok {
		t.Fatal("old route moonbridge still exists after direct save")
	}
	if _, ok := stored.ProviderDefs["openai"]; !ok {
		t.Fatal("new provider openai missing after direct save")
	}
	if _, ok := stored.Routes["saved"]; !ok {
		t.Fatal("new route saved missing after direct save")
	}

	stableRevision, err := cs.CurrentRevision()
	if err != nil {
		t.Fatalf("CurrentRevision() after load error = %v", err)
	}
	if stableRevision != revAfter {
		t.Fatalf("CurrentRevision() = %q, want SaveConfig revision %q", stableRevision, revAfter)
	}
}

func TestSQLiteStoreSaveConfigRejectsInvalidConfigWithoutChangingStoredConfig(t *testing.T) {
	cs := newConfigStoreForTest(t)
	initial := buildTestConfig()
	if err := cs.SeedFromConfig(initial); err != nil {
		t.Fatalf("SeedFromConfig() error = %v", err)
	}
	revBefore, err := cs.CurrentRevision()
	if err != nil {
		t.Fatalf("CurrentRevision() before failed save error = %v", err)
	}

	invalid := buildSavedGraphConfig()
	invalid.Mode = config.Mode("invalid")
	if _, err := cs.SaveConfig(context.Background(), invalid); err == nil {
		t.Fatal("SaveConfig() error = nil for invalid config")
	}

	stored, err := cs.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll() after failed SaveConfig error = %v", err)
	}
	if stored.Mode != initial.Mode {
		t.Fatalf("Mode = %q, want original %q", stored.Mode, initial.Mode)
	}
	if stored.Defaults.MaxTokens != initial.Defaults.MaxTokens {
		t.Fatalf("Defaults.MaxTokens = %d, want original %d", stored.Defaults.MaxTokens, initial.Defaults.MaxTokens)
	}
	if _, ok := stored.ProviderDefs["anthropic"]; !ok {
		t.Fatal("original provider anthropic missing after failed SaveConfig")
	}

	revAfter, err := cs.CurrentRevision()
	if err != nil {
		t.Fatalf("CurrentRevision() after failed save error = %v", err)
	}
	if revAfter != revBefore {
		t.Fatalf("CurrentRevision() after failed save = %q, want unchanged %q", revAfter, revBefore)
	}
}

// --- helpers ---

func newConfigStoreForTest(t *testing.T) store.ConfigStore {
	t.Helper()
	logger := testLogger(t)
	c := store.NewConfigStoreConsumer(logger)
	ts := newTestStore(t, "config_store", c.Tables())
	if err := c.BindStore(ts); err != nil {
		t.Fatalf("BindStore() error = %v", err)
	}
	cs := c.Store()
	if cs == nil {
		t.Fatal("Store() returned nil")
	}
	return cs
}

func buildTestConfig() *config.Config {
	return &config.Config{
		Mode:             config.ModeTransform,
		Addr:             "127.0.0.1:38440",
		AuthToken:        "test-token",
		MaxSessions:      42,
		SessionTTL:       "36h",
		TraceRequests:    true,
		LogLevel:         "debug",
		LogFormat:        "json",
		WebSearchSupport: config.WebSearchSupportAuto,
		WebSearchMaxUses: 12,
		TavilyAPIKey:     "tvly-test-key",
		SearchMaxRounds:  8,
		Defaults: config.Defaults{
			Model:        "moonbridge",
			MaxTokens:    4096,
			SystemPrompt: "You are a test assistant",
		},
		Models: map[string]config.ModelDef{
			"claude-sonnet": {
				ContextWindow:   200000,
				MaxOutputTokens: 64000,
				DisplayName:     "Claude Sonnet",
				Description:     "Claude Sonnet test model",
			},
			"claude-fast": {
				ContextWindow: 100000,
				DisplayName:   "Claude Fast",
			},
		},
		ProviderDefs: map[string]config.ProviderDef{
			"anthropic": {
				BaseURL:          "https://api.anthropic.com",
				APIKey:           "sk-ant-test-key-xxx",
				Version:          "2023-06-01",
				Protocol:         config.ProtocolAnthropic,
				WebSearchSupport: config.WebSearchSupportEnabled,
				WebSearchMaxUses: 10,
				Offers: []config.OfferEntry{
					{
						Model:        "claude-sonnet",
						UpstreamName: "claude-sonnet-4-20250514",
						Priority:     1,
						Pricing: config.ModelPricing{
							InputPrice:      3.0,
							OutputPrice:     15.0,
							CacheWritePrice: 3.75,
							CacheReadPrice:  0.30,
						},
					},
					{
						Model:        "claude-fast",
						UpstreamName: "claude-fast-4-20250501",
					},
				},
			},
			"openai": {
				BaseURL:  "https://api.openai.com",
				APIKey:   "sk-openai-test-key",
				Protocol: config.ProtocolOpenAIResponse,
				Offers: []config.OfferEntry{
					{
						Model: "claude-fast",
					},
				},
			},
		},
		Routes: map[string]config.RouteEntry{
			"moonbridge": {
				Provider:    "anthropic",
				Model:       "claude-sonnet-4-20250514",
				DisplayName: "Moonbridge Sonnet",
			},
			"fast": {
				Provider: "anthropic",
				Model:    "claude-fast-4-20250501",
			},
		},
		ResponseProxy:  config.ResponseProxyConfig{},
		AnthropicProxy: config.AnthropicProxyConfig{},
	}
}

func buildSavedGraphConfig() *config.Config {
	return &config.Config{
		Mode:             config.ModeTransform,
		Addr:             "127.0.0.1:38441",
		AuthToken:        "saved-token",
		MaxSessions:      7,
		SessionTTL:       "12h",
		TraceRequests:    false,
		LogLevel:         "info",
		LogFormat:        "text",
		WebSearchSupport: config.WebSearchSupportDisabled,
		WebSearchMaxUses: 4,
		Defaults: config.Defaults{
			Model:        "saved",
			MaxTokens:    8192,
			SystemPrompt: "Saved graph config",
		},
		Models: map[string]config.ModelDef{
			"gpt-main": {
				ContextWindow:   128000,
				MaxOutputTokens: 16384,
				DisplayName:     "GPT Main",
			},
		},
		ProviderDefs: map[string]config.ProviderDef{
			"openai": {
				BaseURL:  "https://api.openai.com",
				APIKey:   "sk-openai-saved-key",
				Protocol: config.ProtocolOpenAIResponse,
				Offers: []config.OfferEntry{
					{
						Model:        "gpt-main",
						UpstreamName: "gpt-4.1",
						Priority:     2,
						Pricing: config.ModelPricing{
							InputPrice:  2.0,
							OutputPrice: 8.0,
						},
					},
				},
			},
		},
		Routes: map[string]config.RouteEntry{
			"saved": {
				Provider:    "openai",
				Model:       "gpt-main",
				DisplayName: "Saved Route",
			},
		},
		Cache: config.CacheConfig{Mode: "off"},
	}
}

func TestSQLiteStoreApplySuccess(t *testing.T) {
	logger := testLogger(t)
	c := store.NewConfigStoreConsumer(logger)

	ts := newTestStore(t, "config_store", c.Tables())
	if err := c.BindStore(ts); err != nil {
		t.Fatalf("BindStore() error = %v", err)
	}
	cs := c.Store()
	if cs == nil {
		t.Fatal("Store() returned nil")
	}

	// Seed initial config.
	cfg := buildTestConfig()
	if err := cs.SeedFromConfig(cfg); err != nil {
		t.Fatalf("SeedFromConfig() error = %v", err)
	}

	// Load original provider key to verify original value.
	cfg1, err := cs.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll() error = %v", err)
	}
	origBaseURL := cfg1.ProviderDefs["anthropic"].BaseURL

	// Stage a change: update provider base_url.
	afterJSON, _ := json.Marshal(map[string]any{
		"base_url": "https://updated.example.com",
		"api_key":  "sk-updated-key",
		"version":  "2024-01-01",
		"protocol": "anthropic",
	})
	_, err = cs.StageChange(store.ChangeRow{
		Action:    "update",
		Resource:  "provider",
		TargetKey: "anthropic",
		After:     string(afterJSON),
	})
	if err != nil {
		t.Fatalf("StageChange() error = %v", err)
	}

	// Apply changes with a successful applier.
	successApplier := func(cfg *config.Config) error {
		if cfg.ProviderDefs["anthropic"].BaseURL != "https://updated.example.com" {
			return nil // validation success
		}
		return nil
	}

	if err := cs.ApplyPendingChanges(context.Background(), successApplier); err != nil {
		t.Fatalf("ApplyPendingChanges() error = %v", err)
	}

	// LoadAll should reflect the change.
	cfg2, err := cs.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll() after apply error = %v", err)
	}
	newBaseURL := cfg2.ProviderDefs["anthropic"].BaseURL
	if newBaseURL != "https://updated.example.com" {
		t.Fatalf("expected updated base_url %q, got %q", "https://updated.example.com", newBaseURL)
	}
	if newBaseURL == origBaseURL {
		t.Fatal("base_url should have changed after apply")
	}

	// Pending changes should be empty.
	pending, err := cs.ListPendingChanges()
	if err != nil {
		t.Fatalf("ListPendingChanges() error = %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("expected 0 pending changes after apply, got %d", len(pending))
	}
}

func TestSQLiteStoreApplyValidationFail(t *testing.T) {
	logger := testLogger(t)
	c := store.NewConfigStoreConsumer(logger)

	ts := newTestStore(t, "config_store", c.Tables())
	if err := c.BindStore(ts); err != nil {
		t.Fatalf("BindStore() error = %v", err)
	}
	cs := c.Store()
	if cs == nil {
		t.Fatal("Store() returned nil")
	}

	// Seed initial config.
	cfg := buildTestConfig()
	if err := cs.SeedFromConfig(cfg); err != nil {
		t.Fatalf("SeedFromConfig() error = %v", err)
	}

	// Stage a change: update provider base_url.
	afterJSON, _ := json.Marshal(map[string]any{
		"base_url": "https://updated.example.com",
		"api_key":  "sk-updated-key",
		"version":  "2024-01-01",
		"protocol": "anthropic",
	})
	chID, err := cs.StageChange(store.ChangeRow{
		Action:    "update",
		Resource:  "provider",
		TargetKey: "anthropic",
		After:     string(afterJSON),
	})
	if err != nil {
		t.Fatalf("StageChange() error = %v", err)
	}

	// Apply changes with a failing applier.
	failApplier := func(cfg *config.Config) error {
		return errors.New("applier rejected the change")
	}

	err = cs.ApplyPendingChanges(context.Background(), failApplier)
	if err == nil {
		t.Fatal("ApplyPendingChanges() should return error when applier fails")
	}
	t.Logf("Expected apply error: %v", err)
	if !strings.Contains(err.Error(), "DB is consistent") {
		t.Fatalf("error should indicate DB is consistent, got: %v", err)
	}

	// DB changes ARE applied (transaction committed before applier ran).
	cfg2, err := cs.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll() after failed apply error = %v", err)
	}
	if cfg2.ProviderDefs["anthropic"].BaseURL != "https://updated.example.com" {
		t.Fatalf("expected updated base_url %q after committed transaction, got %q", "https://updated.example.com", cfg2.ProviderDefs["anthropic"].BaseURL)
	}

	// The change should be marked as applied (happened inside the transaction).
	pending, err := cs.ListPendingChanges()
	if err != nil {
		t.Fatalf("ListPendingChanges() error = %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("expected 0 pending changes after committed transaction (changes applied), got %d", len(pending))
	}

	// Change ID and applied state: re-check by ID.
	// The change should exist and be applied.
	changesTable := "config_store_changes"
	var applied int
	var errMsg string
	err = ts.QueryRowContext(context.Background(),
		"SELECT applied, COALESCE(error,'') FROM "+changesTable+" WHERE id = ?", chID).Scan(&applied, &errMsg)
	if err != nil {
		t.Fatalf("query change #%d: %v", chID, err)
	}
	if applied != 1 {
		t.Fatal("change should be marked as applied=1 after committed transaction")
	}
	if errMsg != "" {
		t.Fatalf("change should not have an error (applied=1); got error: %s", errMsg)
	}
}

func TestSQLiteStoreApplyProviderCreateAndDelete(t *testing.T) {
	logger := testLogger(t)
	c := store.NewConfigStoreConsumer(logger)

	ts := newTestStore(t, "config_store", c.Tables())
	if err := c.BindStore(ts); err != nil {
		t.Fatalf("BindStore() error = %v", err)
	}
	cs := c.Store()
	if cs == nil {
		t.Fatal("Store() returned nil")
	}

	// Seed initial config.
	cfg := buildTestConfig()
	if err := cs.SeedFromConfig(cfg); err != nil {
		t.Fatalf("SeedFromConfig() error = %v", err)
	}

	// Stage a new provider create.
	afterJSON, _ := json.Marshal(map[string]any{
		"base_url": "https://new-provider.test",
		"api_key":  "sk-new-key",
		"version":  "v1",
		"protocol": "anthropic",
	})
	_, err := cs.StageChange(store.ChangeRow{
		Action:    "create",
		Resource:  "provider",
		TargetKey: "new-provider",
		After:     string(afterJSON),
	})
	if err != nil {
		t.Fatalf("StageChange(create) error = %v", err)
	}

	// Apply with success.
	if err := cs.ApplyPendingChanges(context.Background(), func(cfg *config.Config) error {
		if _, ok := cfg.ProviderDefs["new-provider"]; !ok {
			return nil
		}
		return nil
	}); err != nil {
		t.Fatalf("ApplyPendingChanges() error = %v", err)
	}

	// Verify the new provider exists.
	cfgLoaded, err := cs.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll() error = %v", err)
	}
	if _, ok := cfgLoaded.ProviderDefs["new-provider"]; !ok {
		t.Fatal("new provider should exist after apply")
	}
	if cfgLoaded.ProviderDefs["new-provider"].BaseURL != "https://new-provider.test" {
		t.Fatalf("expected base_url %q, got %q", "https://new-provider.test", cfgLoaded.ProviderDefs["new-provider"].BaseURL)
	}

	// Now stage a delete for the new provider.
	_, err = cs.StageChange(store.ChangeRow{
		Action:    "delete",
		Resource:  "provider",
		TargetKey: "new-provider",
	})
	if err != nil {
		t.Fatalf("StageChange(delete) error = %v", err)
	}

	if err := cs.ApplyPendingChanges(context.Background(), func(cfg *config.Config) error {
		return nil
	}); err != nil {
		t.Fatalf("ApplyPendingChanges() delete error = %v", err)
	}

	cfgLoaded2, err := cs.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll() error = %v", err)
	}
	if _, ok := cfgLoaded2.ProviderDefs["new-provider"]; ok {
		t.Fatal("new provider should have been deleted")
	}
}

func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}
