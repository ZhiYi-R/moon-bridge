package config_test

import (
	"testing"

	"github.com/ZhiYi-R/moon-bridge/internal/config"
	"github.com/ZhiYi-R/moon-bridge/internal/extension/visual"
)

func TestConfigToFileConfigRoundtrip(t *testing.T) {
	input := []byte(`
mode: Transform
trace:
  enabled: true
log:
  level: debug
  format: json
server:
  addr: 127.0.0.1:8080
  auth_token: secret-token
defaults:
  model: moonbridge
  max_tokens: 4096
  system_prompt: "You are a helpful assistant"
web_search:
  support: auto
  max_uses: 10
  tavily_api_key: tvly-key
  search_max_rounds: 8
cache:
  mode: explicit
  ttl: 1h
  prompt_caching: true
  max_breakpoints: 2
persistence:
  active_provider: db_sqlite
models:
  claude-sonnet:
    context_window: 200000
    max_output_tokens: 64000
    display_name: "Claude Sonnet"
    supports_reasoning_summaries: true
  claude-fast:
    context_window: 100000
providers:
  anthropic:
    base_url: https://api.anthropic.com
    api_key: sk-ant-xxx
    version: 2023-06-01
    protocol: anthropic
    web_search:
      support: enabled
    offers:
      - model: claude-sonnet
        upstream_name: claude-sonnet-4-20250514
        priority: 1
        pricing:
          input_price: 3.0
          output_price: 15.0
          cache_write_price: 3.75
          cache_read_price: 0.30
      - model: claude-fast
        upstream_name: claude-fast-4-20250501
routes:
  moonbridge:
    model: claude-sonnet
    provider: anthropic
    display_name: "Moonbridge Sonnet"
  fast:
    model: claude-fast
    provider: anthropic
`)

	// Step 1: Load from YAML.
	cfg, err := config.LoadFromYAML(input)
	if err != nil {
		t.Fatalf("LoadFromYAML() error = %v", err)
	}

	// Step 2: Convert back to FileConfig and marshal.
	fc := cfg.ToFileConfig()
	data, err := fc.MarshalYAML()
	if err != nil {
		t.Fatalf("MarshalYAML() error = %v", err)
	}

	// Step 3: Reload from marshaled YAML.
	cfg2, err := config.LoadFromYAML(data)
	if err != nil {
		t.Fatalf("second LoadFromYAML() error = %v\n--- marshaled YAML ---\n%s", err, string(data))
	}

	// Step 4: Verify key fields match.
	checkStringEqual(t, "Mode", string(cfg.Mode), string(cfg2.Mode))
	checkBoolEqual(t, "TraceRequests", cfg.TraceRequests, cfg2.TraceRequests)
	checkStringEqual(t, "LogLevel", cfg.LogLevel, cfg2.LogLevel)
	checkStringEqual(t, "LogFormat", cfg.LogFormat, cfg2.LogFormat)
	checkStringEqual(t, "Addr", cfg.Addr, cfg2.Addr)
	checkStringEqual(t, "AuthToken", cfg.AuthToken, cfg2.AuthToken)
	checkStringEqual(t, "Defaults.Model", cfg.Defaults.Model, cfg2.Defaults.Model)
	checkIntEqual(t, "Defaults.MaxTokens", cfg.Defaults.MaxTokens, cfg2.Defaults.MaxTokens)
	checkStringEqual(t, "Defaults.SystemPrompt", cfg.Defaults.SystemPrompt, cfg2.Defaults.SystemPrompt)
	checkStringEqual(t, "WebSearchSupport", string(cfg.WebSearchSupport), string(cfg2.WebSearchSupport))
	checkIntEqual(t, "WebSearchMaxUses", cfg.WebSearchMaxUses, cfg2.WebSearchMaxUses)
	checkStringEqual(t, "TavilyAPIKey", cfg.TavilyAPIKey, cfg2.TavilyAPIKey)
	checkIntEqual(t, "SearchMaxRounds", cfg.SearchMaxRounds, cfg2.SearchMaxRounds)
	checkStringEqual(t, "Persistence.ActiveProvider", cfg.Persistence.ActiveProvider, cfg2.Persistence.ActiveProvider)

	checkStringEqual(t, "Cache.Mode", cfg.Cache.Mode, cfg2.Cache.Mode)
	checkStringEqual(t, "Cache.TTL", cfg.Cache.TTL, cfg2.Cache.TTL)
	checkBoolEqual(t, "Cache.PromptCaching", cfg.Cache.PromptCaching, cfg2.Cache.PromptCaching)
	checkIntEqual(t, "Cache.MaxBreakpoints", cfg.Cache.MaxBreakpoints, cfg2.Cache.MaxBreakpoints)

	checkStringEqual(t, "ResponseProxy.Model", cfg.ResponseProxy.Model, cfg2.ResponseProxy.Model)
	checkStringEqual(t, "ResponseProxy.ProviderBaseURL", cfg.ResponseProxy.ProviderBaseURL, cfg2.ResponseProxy.ProviderBaseURL)
	checkStringEqual(t, "ResponseProxy.ProviderAPIKey", cfg.ResponseProxy.ProviderAPIKey, cfg2.ResponseProxy.ProviderAPIKey)
	checkStringEqual(t, "AnthropicProxy.Model", cfg.AnthropicProxy.Model, cfg2.AnthropicProxy.Model)
	checkStringEqual(t, "AnthropicProxy.ProviderBaseURL", cfg.AnthropicProxy.ProviderBaseURL, cfg2.AnthropicProxy.ProviderBaseURL)
	checkStringEqual(t, "AnthropicProxy.ProviderAPIKey", cfg.AnthropicProxy.ProviderAPIKey, cfg2.AnthropicProxy.ProviderAPIKey)
	checkStringEqual(t, "AnthropicProxy.ProviderVersion", cfg.AnthropicProxy.ProviderVersion, cfg2.AnthropicProxy.ProviderVersion)

	// Models.
	if len(cfg.Models) != len(cfg2.Models) {
		t.Fatalf("Models count: %d != %d", len(cfg.Models), len(cfg2.Models))
	}
	for slug, m := range cfg.Models {
		m2, ok := cfg2.Models[slug]
		if !ok {
			t.Fatalf("Models[%q] missing in reloaded config", slug)
		}
		checkIntEqual(t, "Models["+slug+"].ContextWindow", m.ContextWindow, m2.ContextWindow)
		checkIntEqual(t, "Models["+slug+"].MaxOutputTokens", m.MaxOutputTokens, m2.MaxOutputTokens)
		checkStringEqual(t, "Models["+slug+"].DisplayName", m.DisplayName, m2.DisplayName)
		checkBoolEqual(t, "Models["+slug+"].SupportsReasoningSummaries", m.SupportsReasoningSummaries, m2.SupportsReasoningSummaries)
	}

	// Providers.
	if len(cfg.ProviderDefs) != len(cfg2.ProviderDefs) {
		t.Fatalf("ProviderDefs count: %d != %d", len(cfg.ProviderDefs), len(cfg2.ProviderDefs))
	}
	for key, def := range cfg.ProviderDefs {
		def2, ok := cfg2.ProviderDefs[key]
		if !ok {
			t.Fatalf("ProviderDefs[%q] missing in reloaded config", key)
		}
		checkStringEqual(t, "ProviderDefs["+key+"].BaseURL", def.BaseURL, def2.BaseURL)
		checkStringEqual(t, "ProviderDefs["+key+"].APIKey", def.APIKey, def2.APIKey)
		checkStringEqual(t, "ProviderDefs["+key+"].Version", def.Version, def2.Version)
		checkStringEqual(t, "ProviderDefs["+key+"].Protocol", def.Protocol, def2.Protocol)
		checkStringEqual(t, "ProviderDefs["+key+"].WebSearchSupport", string(def.WebSearchSupport), string(def2.WebSearchSupport))

		// Offers.
		if len(def.Offers) != len(def2.Offers) {
			t.Fatalf("ProviderDefs[%q].Offers count: %d != %d", key, len(def.Offers), len(def2.Offers))
		}
		for i, offer := range def.Offers {
			o2 := def2.Offers[i]
			checkStringEqual(t, "Offer["+key+"].Model", offer.Model, o2.Model)
			checkStringEqual(t, "Offer["+key+"].UpstreamName", offer.UpstreamName, o2.UpstreamName)
			checkIntEqual(t, "Offer["+key+"].Priority", offer.Priority, o2.Priority)
			checkFloatEqual(t, "Offer["+key+"].Pricing.InputPrice", offer.Pricing.InputPrice, o2.Pricing.InputPrice)
			checkFloatEqual(t, "Offer["+key+"].Pricing.OutputPrice", offer.Pricing.OutputPrice, o2.Pricing.OutputPrice)
			checkFloatEqual(t, "Offer["+key+"].Pricing.CacheWritePrice", offer.Pricing.CacheWritePrice, o2.Pricing.CacheWritePrice)
			checkFloatEqual(t, "Offer["+key+"].Pricing.CacheReadPrice", offer.Pricing.CacheReadPrice, o2.Pricing.CacheReadPrice)
		}
	}

	// Routes.
	if len(cfg.Routes) != len(cfg2.Routes) {
		t.Fatalf("Routes count: %d != %d", len(cfg.Routes), len(cfg2.Routes))
	}
	for alias, route := range cfg.Routes {
		r2, ok := cfg2.Routes[alias]
		if !ok {
			t.Fatalf("Routes[%q] missing in reloaded config", alias)
		}
		checkStringEqual(t, "Routes["+alias+"].Provider", route.Provider, r2.Provider)
		checkStringEqual(t, "Routes["+alias+"].Model", route.Model, r2.Model)
		checkStringEqual(t, "Routes["+alias+"].DisplayName", route.DisplayName, r2.DisplayName)
		checkIntEqual(t, "Routes["+alias+"].ContextWindow", route.ContextWindow, r2.ContextWindow)
	}
}

func TestConfigToFileConfigRoundtripCaptureResponse(t *testing.T) {
	input := []byte(`
mode: CaptureResponse
trace:
  enabled: true
proxy:
  response:
    model: gpt-capture
    base_url: https://api.openai.com
    api_key: sk-openai-xxx
`)

	cfg, err := config.LoadFromYAML(input)
	if err != nil {
		t.Fatalf("LoadFromYAML() error = %v", err)
	}

	fc := cfg.ToFileConfig()
	data, err := fc.MarshalYAML()
	if err != nil {
		t.Fatalf("MarshalYAML() error = %v", err)
	}

	cfg2, err := config.LoadFromYAML(data)
	if err != nil {
		t.Fatalf("second LoadFromYAML() error = %v\n--- marshaled YAML ---\n%s", err, string(data))
	}

	checkStringEqual(t, "Mode", string(cfg.Mode), string(cfg2.Mode))
	checkBoolEqual(t, "TraceRequests", cfg.TraceRequests, cfg2.TraceRequests)
	checkStringEqual(t, "ResponseProxy.Model", cfg.ResponseProxy.Model, cfg2.ResponseProxy.Model)
	checkStringEqual(t, "ResponseProxy.ProviderBaseURL", cfg.ResponseProxy.ProviderBaseURL, cfg2.ResponseProxy.ProviderBaseURL)
	checkStringEqual(t, "ResponseProxy.ProviderAPIKey", cfg.ResponseProxy.ProviderAPIKey, cfg2.ResponseProxy.ProviderAPIKey)
}

func TestConfigToFileConfigRoundtripCaptureAnthropic(t *testing.T) {
	input := []byte(`
mode: CaptureAnthropic
trace:
  enabled: true
proxy:
  anthropic:
    model: claude-capture
    base_url: https://api.anthropic.com
    api_key: sk-ant-xxx
    version: 2023-06-01
`)

	cfg, err := config.LoadFromYAML(input)
	if err != nil {
		t.Fatalf("LoadFromYAML() error = %v", err)
	}

	fc := cfg.ToFileConfig()
	data, err := fc.MarshalYAML()
	if err != nil {
		t.Fatalf("MarshalYAML() error = %v", err)
	}

	cfg2, err := config.LoadFromYAML(data)
	if err != nil {
		t.Fatalf("second LoadFromYAML() error = %v\n--- marshaled YAML ---\n%s", err, string(data))
	}

	checkStringEqual(t, "Mode", string(cfg.Mode), string(cfg2.Mode))
	checkBoolEqual(t, "TraceRequests", cfg.TraceRequests, cfg2.TraceRequests)
	checkStringEqual(t, "AnthropicProxy.Model", cfg.AnthropicProxy.Model, cfg2.AnthropicProxy.Model)
	checkStringEqual(t, "AnthropicProxy.ProviderBaseURL", cfg.AnthropicProxy.ProviderBaseURL, cfg2.AnthropicProxy.ProviderBaseURL)
	checkStringEqual(t, "AnthropicProxy.ProviderAPIKey", cfg.AnthropicProxy.ProviderAPIKey, cfg2.AnthropicProxy.ProviderAPIKey)
}

func TestConfigToFileConfigRoundtripEmptyOptionalFields(t *testing.T) {
	// Minimal config with only required fields.
	input := []byte(`
mode: Transform
models:
  claude-test:
    context_window: 200000
providers:
  main:
    base_url: https://provider.example.test
    api_key: upstream-key
    offers:
      - model: claude-test
routes:
  moonbridge:
    model: claude-test
    provider: main
`)

	cfg, err := config.LoadFromYAML(input)
	if err != nil {
		t.Fatalf("LoadFromYAML() error = %v", err)
	}

	fc := cfg.ToFileConfig()
	data, err := fc.MarshalYAML()
	if err != nil {
		t.Fatalf("MarshalYAML() error = %v", err)
	}

	cfg2, err := config.LoadFromYAML(data)
	if err != nil {
		t.Fatalf("second LoadFromYAML() error = %v\n--- marshaled YAML ---\n%s", err, string(data))
	}

	checkStringEqual(t, "Mode", string(cfg.Mode), string(cfg2.Mode))
	checkStringEqual(t, "Defaults.Model", cfg.Defaults.Model, cfg2.Defaults.Model)
	checkStringEqual(t, "LogLevel", cfg.LogLevel, cfg2.LogLevel)
	checkStringEqual(t, "WebSearchSupport", string(cfg.WebSearchSupport), string(cfg2.WebSearchSupport))

	// Verify providers and routes are preserved.
	if len(cfg2.ProviderDefs) != 1 {
		t.Fatalf("expected 1 provider, got %d", len(cfg2.ProviderDefs))
	}
	if len(cfg2.Routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(cfg2.Routes))
	}
	if cfg2.ProviderDefs["main"].BaseURL != "https://provider.example.test" {
		t.Fatalf("BaseURL = %q", cfg2.ProviderDefs["main"].BaseURL)
	}
	if cfg2.Routes["moonbridge"].Provider != "main" {
		t.Fatalf("Route provider = %q", cfg2.Routes["moonbridge"].Provider)
	}
}

func TestConfigToFileConfigRoundtripPreservesFeatureExtras(t *testing.T) {
	enabled := true
	disabled := false
	input := []byte(`
mode: Transform
web_search:
  provider_tool: global-preview
extensions:
  visual:
    enabled: true
    scope_note: global-extra
    config:
      provider: main
      model: claude-test
models:
  claude-test:
    context_window: 200000
    web_search:
      provider_tool: model-preview
    extensions:
      visual:
        enabled: false
        scope_note: model-extra
        config:
          provider: main
          model: claude-test
providers:
  main:
    base_url: https://provider.example.test
    api_key: upstream-key
    web_search:
      provider_tool: provider-preview
    extensions:
      visual:
        enabled: true
        scope_note: provider-extra
        config:
          provider: main
          model: claude-test
    offers:
      - model: claude-test
routes:
  moonbridge:
    model: claude-test
    provider: main
    web_search:
      provider_tool: route-preview
    extensions:
      visual:
        enabled: true
        scope_note: route-extra
        config:
          provider: main
          model: claude-test
`)

	cfg, err := config.LoadFromYAMLWithOptions(input, config.LoadOptions{ExtensionSpecs: visual.ConfigSpecs()})
	if err != nil {
		t.Fatalf("LoadFromYAML() error = %v", err)
	}

	fc := cfg.ToFileConfig()
	if fc.WebSearch.Extra["provider_tool"] != "global-preview" {
		t.Fatalf("top-level web_search extra = %#v, want global-preview", fc.WebSearch.Extra["provider_tool"])
	}
	checkExtensionExtra(t, fc.Extensions["visual"], &enabled, "global-extra")
	if fc.Models["claude-test"].WebSearch.Extra["provider_tool"] != "model-preview" {
		t.Fatalf("model web_search extra = %#v, want model-preview", fc.Models["claude-test"].WebSearch.Extra["provider_tool"])
	}
	checkExtensionExtra(t, fc.Models["claude-test"].Extensions["visual"], &disabled, "model-extra")
	if fc.Providers["main"].WebSearch.Extra["provider_tool"] != "provider-preview" {
		t.Fatalf("provider web_search extra = %#v, want provider-preview", fc.Providers["main"].WebSearch.Extra["provider_tool"])
	}
	checkExtensionExtra(t, fc.Providers["main"].Extensions["visual"], &enabled, "provider-extra")
	if fc.Routes["moonbridge"].WebSearch.Extra["provider_tool"] != "route-preview" {
		t.Fatalf("route web_search extra = %#v, want route-preview", fc.Routes["moonbridge"].WebSearch.Extra["provider_tool"])
	}
	checkExtensionExtra(t, fc.Routes["moonbridge"].Extensions["visual"], &enabled, "route-extra")

	data, err := fc.MarshalYAML()
	if err != nil {
		t.Fatalf("MarshalYAML() error = %v", err)
	}
	reloaded, err := config.LoadFromYAMLWithOptions(data, config.LoadOptions{ExtensionSpecs: visual.ConfigSpecs()})
	if err != nil {
		t.Fatalf("second LoadFromYAML() error = %v\n--- marshaled YAML ---\n%s", err, string(data))
	}
	fc2 := reloaded.ToFileConfig()
	if fc2.WebSearch.Extra["provider_tool"] != "global-preview" {
		t.Fatalf("reloaded top-level web_search extra = %#v, want global-preview", fc2.WebSearch.Extra["provider_tool"])
	}
	if fc2.Models["claude-test"].WebSearch.Extra["provider_tool"] != "model-preview" {
		t.Fatalf("reloaded model web_search extra = %#v, want model-preview", fc2.Models["claude-test"].WebSearch.Extra["provider_tool"])
	}
	if fc2.Providers["main"].WebSearch.Extra["provider_tool"] != "provider-preview" {
		t.Fatalf("reloaded provider web_search extra = %#v, want provider-preview", fc2.Providers["main"].WebSearch.Extra["provider_tool"])
	}
	if fc2.Routes["moonbridge"].WebSearch.Extra["provider_tool"] != "route-preview" {
		t.Fatalf("reloaded route web_search extra = %#v, want route-preview", fc2.Routes["moonbridge"].WebSearch.Extra["provider_tool"])
	}
}

// --- helpers ---

func checkExtensionExtra(t *testing.T, extension config.ExtensionFileConfig, enabled *bool, wantScopeNote string) {
	t.Helper()
	if enabled == nil {
		if extension.Enabled != nil {
			t.Fatalf("extension.Enabled = %v, want nil", *extension.Enabled)
		}
	} else if extension.Enabled == nil || *extension.Enabled != *enabled {
		t.Fatalf("extension.Enabled = %v, want %v", extension.Enabled, *enabled)
	}
	if extension.Extra["scope_note"] != wantScopeNote {
		t.Fatalf("extension extra scope_note = %#v, want %s", extension.Extra["scope_note"], wantScopeNote)
	}
}

func checkStringEqual(t *testing.T, name, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("%s: got %q, want %q", name, got, want)
	}
}

func checkBoolEqual(t *testing.T, name string, got, want bool) {
	t.Helper()
	if got != want {
		t.Errorf("%s: got %v, want %v", name, got, want)
	}
}

func checkIntEqual(t *testing.T, name string, got, want int) {
	t.Helper()
	if got != want {
		t.Errorf("%s: got %d, want %d", name, got, want)
	}
}

func checkFloatEqual(t *testing.T, name string, got, want float64) {
	t.Helper()
	if got != want {
		t.Errorf("%s: got %f, want %f", name, got, want)
	}
}
