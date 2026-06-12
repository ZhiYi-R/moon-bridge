package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"moonbridge/internal/config"
	deepseekv4 "moonbridge/internal/extension/deepseek_v4"
	"moonbridge/internal/extension/visual"
)

func builtinExtensionSpecsForTest() []config.ExtensionConfigSpec {
	specs := append([]config.ExtensionConfigSpec{}, deepseekv4.ConfigSpecs()...)
	specs = append(specs, visual.ConfigSpecs()...)
	return specs
}

func TestLoadFromYAMLParsesTransformConfig(t *testing.T) {
	cfg, err := config.LoadFromYAML([]byte(`
mode: Transform
models:
  claude-test:
    context_window: 200000
    max_output_tokens: 100000
  claude-fast: {}
providers:
  main:
    base_url: https://provider.example.test
    api_key: upstream-key
    user_agent: Bun/1.3.13
    web_search:
      support: auto
    offers:
      - model: claude-test
      - model: claude-fast
routes:
  gpt-test:
    model: claude-test
    provider: main
  gpt-fast:
    model: claude-fast
    provider: main
web_search:
  support: auto
  max_uses: 12
defaults:
  model: gpt-test
cache:
  mode: explicit
  ttl: 1h
  min_breakpoint_tokens: 4096
trace:
  enabled: true
`))
	if err != nil {
		t.Fatalf("LoadFromYAML() error = %v", err)
	}

	if cfg.Mode != config.ModeTransform {
		t.Fatalf("Mode = %q", cfg.Mode)
	}
	if cfg.Addr != "127.0.0.1:38440" {
		t.Fatalf("Addr = %q, want 127.0.0.1:38440", cfg.Addr)
	}
	if def, ok := cfg.ProviderDefs["main"]; !ok || def.UserAgent != "Bun/1.3.13" {
		t.Fatalf("ProviderDefs[main].UserAgent = %+v", cfg.ProviderDefs)
	}
	if cfg.WebSearchMaxUses != 12 {
		t.Fatalf("WebSearchMaxUses = %d", cfg.WebSearchMaxUses)
	}
	if cfg.WebSearchSupport != config.WebSearchSupportAuto {
		t.Fatalf("WebSearchSupport = %q", cfg.WebSearchSupport)
	}
	if !cfg.WebSearchEnabled() {
		t.Fatal("WebSearchEnabled() = false, want true")
	}
	if cfg.DefaultMaxTokens != 1024 {
		t.Fatalf("DefaultMaxTokens = %d", cfg.DefaultMaxTokens)
	}
	if got := cfg.ModelFor("gpt-test"); got != "claude-test" {
		t.Fatalf("ModelFor(gpt-test) = %q", got)
	}
	if got := cfg.DefaultModelAlias(); got != "gpt-test" {
		t.Fatalf("DefaultModelAlias() = %q", got)
	}
	if cfg.Cache.Mode != "explicit" || cfg.Cache.TTL != "1h" {
		t.Fatalf("Cache = %+v", cfg.Cache)
	}
	if cfg.Cache.MinBreakpointTokens != 4096 {
		t.Fatalf("Cache.MinBreakpointTokens = %d", cfg.Cache.MinBreakpointTokens)
	}
	if !cfg.TraceRequests {
		t.Fatal("TraceRequests = false, want true")
	}
	route := cfg.RouteFor("gpt-test")
	if route.Model != "claude-test" || route.ContextWindow != 200000 || route.MaxOutputTokens != 100000 {
		t.Fatalf("RouteFor(gpt-test) = %+v", route)
	}
}

func TestXDGDefaultConfigPathUsesXDGConfigHome(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("HOME", filepath.Join(t.TempDir(), "home"))

	got, err := config.XDGDefaultConfigPath()
	if err != nil {
		t.Fatalf("XDGDefaultConfigPath() error = %v", err)
	}
	want := filepath.Join(configHome, "moonbridge", "config.yml")
	if got != want {
		t.Fatalf("XDGDefaultConfigPath() = %q, want %q", got, want)
	}
}

func TestXDGDefaultConfigPathFallsBackToHome(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", configHome)

	got, err := config.XDGDefaultConfigPath()
	if err != nil {
		t.Fatalf("XDGDefaultConfigPath() error = %v", err)
	}
	want := filepath.Join(configHome, ".config", "moonbridge", "config.yml")
	if got != want {
		t.Fatalf("XDGDefaultConfigPath() = %q, want %q", got, want)
	}
}

func TestLoadFromYAMLCanDisableWebSearch(t *testing.T) {
	cfg, err := config.LoadFromYAML([]byte(`
mode: Transform
models:
  claude-test: {}
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
web_search:
  support: disabled
`))
	if err != nil {
		t.Fatalf("LoadFromYAML() error = %v", err)
	}
	if cfg.WebSearchSupport != config.WebSearchSupportDisabled {
		t.Fatalf("WebSearchSupport = %q", cfg.WebSearchSupport)
	}
	if cfg.WebSearchEnabled() {
		t.Fatal("WebSearchEnabled() = true, want false")
	}
}

func TestLoadFromYAMLParsesMultiProviderProtocol(t *testing.T) {
	cfg, err := config.LoadFromYAMLWithOptions([]byte(`
mode: Transform
models:
  deepseek-v4-pro:
    default_reasoning_level: high
    supported_reasoning_levels:
      - effort: high
        description: High reasoning effort
      - effort: xhigh
        description: Extra high reasoning effort
    extensions:
      deepseek_v4:
        enabled: true
  gpt-image-1.5: {}
providers:
  deepseek:
    base_url: https://deepseek.example.test
    api_key: deepseek-key
    offers:
      - model: deepseek-v4-pro
  openai:
    base_url: https://openai.example.test
    api_key: openai-key
    protocol: openai-response
    offers:
      - model: gpt-image-1.5
routes:
  moonbridge:
    model: deepseek-v4-pro
    provider: deepseek
  image:
    model: gpt-image-1.5
    provider: openai
`), config.LoadOptions{ExtensionSpecs: builtinExtensionSpecsForTest()})
	if err != nil {
		t.Fatalf("LoadFromYAML() error = %v", err)
	}
	if cfg.ProviderDefs["openai"].Protocol != config.ProtocolOpenAIResponse {
		t.Fatalf("openai provider = %+v", cfg.ProviderDefs["openai"])
	}
	if !cfg.ExtensionEnabled(deepseekv4.PluginName, "moonbridge") {
		t.Fatalf("ExtensionEnabled(deepseek_v4, moonbridge) = false, want true")
	}
	if cfg.ExtensionEnabled(deepseekv4.PluginName, "image") {
		t.Fatalf("ExtensionEnabled(deepseek_v4, image) = true, want false")
	}
	if cfg.RouteFor("moonbridge").DefaultReasoningLevel != "high" {
		t.Fatalf("RouteFor(moonbridge).DefaultReasoningLevel = %q", cfg.RouteFor("moonbridge").DefaultReasoningLevel)
	}
	if got := len(cfg.RouteFor("moonbridge").SupportedReasoningLevels); got != 2 {
		t.Fatalf("RouteFor(moonbridge).SupportedReasoningLevels len = %d", got)
	}
	if got := cfg.ModelFor("image"); got != "gpt-image-1.5" {
		t.Fatalf("ModelFor(image) = %q", got)
	}
}

func TestLoadFromYAMLParsesVisualConfig(t *testing.T) {
	cfg, err := config.LoadFromYAMLWithOptions([]byte(`
mode: Transform
models:
  deepseek-v4-pro:
    extensions:
      visual:
        enabled: true
  kimi-vision:
    context_window: 128000
providers:
  deepseek:
    base_url: https://deepseek.example.test
    api_key: deepseek-key
    offers:
      - model: deepseek-v4-pro
  kimi:
    base_url: https://kimi.example.test/v1
    api_key: kimi-key
    offers:
      - model: kimi-vision
routes:
  moonbridge:
    model: deepseek-v4-pro
    provider: deepseek
extensions:
  visual:
    enabled: true
    config:
      provider: kimi
      model: kimi-vision
      max_rounds: 3
      max_tokens: 1024
`), config.LoadOptions{ExtensionSpecs: builtinExtensionSpecsForTest()})
	if err != nil {
		t.Fatalf("LoadFromYAML() error = %v", err)
	}
	if !cfg.ExtensionEnabled(visual.PluginName, "moonbridge") {
		t.Fatal("ExtensionEnabled(visual, moonbridge) = false, want true")
	}
	if !cfg.ExtensionEnabled(visual.PluginName, "deepseek/deepseek-v4-pro") {
		t.Fatal("ExtensionEnabled(visual, deepseek/deepseek-v4-pro) = false, want true")
	}
	resolved, _ := cfg.ExtensionConfig(visual.PluginName, "moonbridge").(*visual.Config)
	if resolved == nil {
		t.Fatal("ExtensionConfig(visual, moonbridge) = nil")
	}
	if resolved.Provider != "kimi" || resolved.Model != "kimi-vision" {
		t.Fatalf("ExtensionConfig(visual, moonbridge) = %+v", resolved)
	}
	if resolved.MaxRounds != 3 || resolved.MaxTokens != 1024 {
		t.Fatalf("ExtensionConfig defaults/overrides = %+v", resolved)
	}
}

func TestLoadFromYAMLRejectsLegacyModelVisualFlag(t *testing.T) {
	_, err := config.LoadFromYAML([]byte(`
mode: Transform
models:
  deepseek-v4-pro:
    visual: true
providers:
  deepseek:
    base_url: https://deepseek.example.test
    api_key: deepseek-key
    offers:
      - model: deepseek-v4-pro
  kimi:
    base_url: https://kimi.example.test/v1
    api_key: kimi-key
    offers:
      - model: kimi-vision
routes:
  moonbridge:
    model: deepseek-v4-pro
    provider: deepseek
`))
	if err == nil {
		t.Fatal("LoadFromYAML() error = nil, want unknown field error for legacy visual flag")
	}
	if !strings.Contains(err.Error(), "field visual not found") {
		t.Fatalf("LoadFromYAML() error = %v, want unknown visual field error", err)
	}
}

func TestLoadFromYAMLAllowsProviderModelCatalogWithoutRoutes(t *testing.T) {
	cfg, err := config.LoadFromYAML([]byte(`
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
defaults:
  model: main/claude-test
`))
	if err != nil {
		t.Fatalf("LoadFromYAML() error = %v", err)
	}
	if got := cfg.ModelFor("main/claude-test"); got != "claude-test" {
		t.Fatalf("ModelFor(main/claude-test) = %q", got)
	}
	route := cfg.RouteFor("main/claude-test")
	if route.Provider != "main" || route.Model != "claude-test" || route.ContextWindow != 200000 {
		t.Fatalf("RouteFor(main/claude-test) = %+v", route)
	}
	if got := cfg.DefaultModelAlias(); got != "main/claude-test" {
		t.Fatalf("DefaultModelAlias() = %q", got)
	}
}

func TestLoadFromYAMLRejectsInvalidMultiProviderConfig(t *testing.T) {
	for name, input := range map[string]string{
		"missing provider base URL": `
mode: Transform
models:
  gpt-image-1.5: {}
providers:
  openai:
    api_key: openai-key
    protocol: openai-response
    offers:
      - model: gpt-image-1.5
routes:
  image:
    model: gpt-image-1.5
    provider: openai
`,
		"invalid protocol": `
mode: Transform
models:
  gpt-image-1.5: {}
providers:
  openai:
    base_url: https://openai.example.test
    api_key: openai-key
    protocol: responses
    offers:
      - model: gpt-image-1.5
routes:
  image:
    model: gpt-image-1.5
    provider: openai
`,
		"old openai protocol name removed": `
mode: Transform
models:
  gpt-image-1.5: {}
providers:
  openai:
    base_url: https://openai.example.test
    api_key: openai-key
    protocol: openai
    offers:
      - model: gpt-image-1.5
routes:
  image:
    model: gpt-image-1.5
    provider: openai
`,
		"missing provider model catalog and routes": `
mode: Transform
providers:
  openai:
    base_url: https://openai.example.test
    api_key: openai-key
    protocol: openai-response
`,
		"empty route model": `
mode: Transform
models:
  gpt-image-1.5: {}
providers:
  openai:
    base_url: https://openai.example.test
    api_key: openai-key
    protocol: openai-response
    offers:
      - model: gpt-image-1.5
routes:
  image:
    model: ""
    provider: openai
`,
		"deepseek extension on openai-response protocol": `
mode: Transform
models:
  gpt-image-1.5: {}
providers:
  openai:
    base_url: https://openai.example.test
    api_key: openai-key
    protocol: openai-response
    offers:
      - model: gpt-image-1.5
routes:
  image:
    model: gpt-image-1.5
    provider: openai
extensions:
  deepseek_v4:
    enabled: true
`,
		"global deepseek extension on openai-response protocol": `
mode: Transform
models:
  gpt-image-1.5: {}
providers:
  openai:
    base_url: https://openai.example.test
    api_key: openai-key
    protocol: openai-response
    offers:
      - model: gpt-image-1.5
routes:
  image:
    model: gpt-image-1.5
    provider: openai
extensions:
  deepseek_v4:
    enabled: true
`,
		"visual provider missing": `
mode: Transform
models:
  deepseek-v4-pro:
    extensions:
      visual:
        enabled: true
providers:
  deepseek:
    base_url: https://deepseek.example.test
    api_key: deepseek-key
    offers:
      - model: deepseek-v4-pro
routes:
  moonbridge:
    model: deepseek-v4-pro
    provider: deepseek
extensions:
  visual:
    config:
      model: kimi-vision
`,
		"visual provider on openai-response protocol": `
mode: Transform
models:
  deepseek-v4-pro: {}
  gpt-4o:
    extensions:
      visual:
        enabled: true
providers:
  deepseek:
    base_url: https://deepseek.example.test
    api_key: deepseek-key
    offers:
      - model: deepseek-v4-pro
  openai:
    base_url: https://openai.example.test
    api_key: openai-key
    protocol: openai-response
    offers:
      - model: gpt-4o
routes:
  moonbridge:
    model: deepseek-v4-pro
    provider: deepseek
extensions:
  visual:
    config:
      provider: openai
      model: gpt-4o
`,
	} {
		t.Run(name, func(t *testing.T) {
			// Cases that are now valid after removing the anthropic protocol constraint from plugins.
			switch name {
			case "deepseek extension on openai-response protocol",
				"global deepseek extension on openai-response protocol",
				"visual provider on openai-response protocol":
				// Plugin protocol constraint removed — plugins operate on protocol-agnostic Core format.
				if _, err := config.LoadFromYAMLWithOptions([]byte(input), config.LoadOptions{ExtensionSpecs: builtinExtensionSpecsForTest()}); err != nil {
					t.Fatalf("LoadFromYAML() error = %v, want nil (protocol constraint removed)", err)
				}
			default:
				if _, err := config.LoadFromYAMLWithOptions([]byte(input), config.LoadOptions{ExtensionSpecs: builtinExtensionSpecsForTest()}); err == nil {
					t.Fatal("LoadFromYAML() error = nil, want validation error")
				}
			}
		})
	}
}

func TestLoadFromYAMLRejectsInvalidWebSearchSupport(t *testing.T) {
	_, err := config.LoadFromYAML([]byte(`
mode: Transform
models:
  claude-test: {}
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
web_search:
  support: sometimes
`))
	if err == nil {
		t.Fatal("LoadFromYAML() error = nil, want invalid web search support error")
	}
}

func TestLoadFromYAMLRequiresMode(t *testing.T) {
	_, err := config.LoadFromYAML([]byte(`{}`))
	if err == nil {
		t.Fatal("LoadFromYAML() error = nil, want missing mode error")
	}
}

func TestLoadFromYAMLRejectsInvalidMode(t *testing.T) {
	_, err := config.LoadFromYAML([]byte(`mode: Proxy`))
	if err == nil {
		t.Fatal("LoadFromYAML() error = nil, want invalid mode error")
	}
}

func TestLoadFromYAMLParsesModelMetadata(t *testing.T) {
	cfg, err := config.LoadFromYAML([]byte(`
mode: Transform
models:
  claude-test:
    context_window: 200000
    max_output_tokens: 100000
    display_name: "Claude Test"
    description: "A test model"
    default_reasoning_level: "medium"
    supported_reasoning_levels:
      - effort: "low"
        description: "Fast"
      - effort: "high"
        description: "Deep"
    supports_reasoning_summaries: true
    default_reasoning_summary: "auto"
providers:
  main:
    base_url: https://provider.example.test
    api_key: upstream-key
    offers:
      - model: claude-test
routes:
  gpt-test:
    model: claude-test
    provider: main
`))
	if err != nil {
		t.Fatalf("LoadFromYAML() error = %v", err)
	}
	route := cfg.RouteFor("gpt-test")
	// Route DisplayName/Description come from model def in new format.
	// Route DisplayName is NOT inherited from model def — empty unless explicitly configured.
	if route.DisplayName != "" {
		t.Fatalf("DisplayName = %q, want empty (not inherited from model def)", route.DisplayName)
	}
	// Route DisplayName is NOT inherited from model def — empty unless explicitly configured.
	if route.DisplayName != "" {
		t.Fatalf("DisplayName = %q, want empty (not inherited from model def)", route.DisplayName)
	}
	// Route DisplayName is NOT inherited from model def — empty unless explicitly configured.
	if route.DisplayName != "" {
		t.Fatalf("DisplayName = %q, want empty (not inherited from model def)", route.DisplayName)
	}
	if route.Description != "A test model" {
		t.Fatalf("Description = %q, want \"A test model\"", route.Description)
	}
	if route.DefaultReasoningLevel != "medium" {
		t.Fatalf("DefaultReasoningLevel = %q", route.DefaultReasoningLevel)
	}
	if len(route.SupportedReasoningLevels) != 2 {
		t.Fatalf("SupportedReasoningLevels len = %d", len(route.SupportedReasoningLevels))
	}
	if route.SupportedReasoningLevels[0].Effort != "low" || route.SupportedReasoningLevels[0].Description != "Fast" {
		t.Fatalf("SupportedReasoningLevels[0] = %+v", route.SupportedReasoningLevels[0])
	}
	if !route.SupportsReasoningSummaries {
		t.Fatal("SupportsReasoningSummaries = false")
	}
	if route.DefaultReasoningSummary != "auto" {
		t.Fatalf("DefaultReasoningSummary = %q", route.DefaultReasoningSummary)
	}
}

func TestLoadFromYAMLRequiresTransformProviderSettings(t *testing.T) {
	_, err := config.LoadFromYAML([]byte(`mode: Transform`))
	if err == nil {
		t.Fatal("LoadFromYAML() error = nil, want missing provider settings error")
	}
}

func TestLoadFromYAMLRejectsInvalidCacheTTL(t *testing.T) {
	_, err := config.LoadFromYAML([]byte(`
mode: Transform
models:
  claude-test: {}
providers:
  main:
    base_url: https://provider.example.test
    api_key: upstream-key
    offers:
      - model: claude-test
routes:
  gpt-test:
    model: claude-test
    provider: main
cache:
  ttl: 24h
`))
	if err == nil {
		t.Fatal("LoadFromYAML() error = nil, want invalid cache TTL error")
	}
}

func TestLoadFromYAMLRejectsEmptyRouteModel(t *testing.T) {
	_, err := config.LoadFromYAML([]byte(`
mode: Transform
providers:
  main:
    base_url: https://provider.example.test
    api_key: upstream-key
routes:
  moonbridge:
    model: ""
    provider: main
`))
	if err == nil {
		t.Fatal("LoadFromYAML() error = nil, want empty route model error")
	}
}

func TestLoadFromYAMLParsesCaptureResponseConfig(t *testing.T) {
	cfg, err := config.LoadFromYAML([]byte(`
mode: CaptureResponse
trace:
  enabled: true
proxy:
  response:
    model: gpt-capture
    base_url: https://api.openai.example.test
    api_key: upstream-openai-key
`))
	if err != nil {
		t.Fatalf("LoadFromYAML() error = %v", err)
	}
	if !cfg.TraceRequests {
		t.Fatal("TraceRequests = false, want true")
	}
	if cfg.ResponseProxy.Model != "gpt-capture" {
		t.Fatalf("Model = %q", cfg.ResponseProxy.Model)
	}
	if cfg.ResponseProxy.ProviderBaseURL != "https://api.openai.example.test" {
		t.Fatalf("ProviderBaseURL = %q", cfg.ResponseProxy.ProviderBaseURL)
	}
	if cfg.ResponseProxy.ProviderAPIKey != "upstream-openai-key" {
		t.Fatalf("ProviderAPIKey = %q", cfg.ResponseProxy.ProviderAPIKey)
	}
}

func TestLoadFromYAMLParsesCaptureAnthropicConfig(t *testing.T) {
	cfg, err := config.LoadFromYAML([]byte(`
mode: CaptureAnthropic
trace:
  enabled: true
proxy:
  anthropic:
    model: claude-test
    base_url: https://provider.example.test
    api_key: upstream-key
    version: 2023-06-01
`))
	if err != nil {
		t.Fatalf("LoadFromYAML() error = %v", err)
	}
	if cfg.AnthropicProxy.Model != "claude-test" {
		t.Fatalf("Model = %q", cfg.AnthropicProxy.Model)
	}
	if !cfg.TraceRequests {
		t.Fatal("TraceRequests = false, want true")
	}
	if cfg.AnthropicProxy.ProviderBaseURL != "https://provider.example.test" {
		t.Fatalf("ProviderBaseURL = %q", cfg.AnthropicProxy.ProviderBaseURL)
	}
	if cfg.AnthropicProxy.ProviderAPIKey != "upstream-key" {
		t.Fatalf("ProviderAPIKey = %q", cfg.AnthropicProxy.ProviderAPIKey)
	}
	if cfg.AnthropicProxy.ProviderVersion != "2023-06-01" {
		t.Fatalf("ProviderVersion = %q", cfg.AnthropicProxy.ProviderVersion)
	}
}

func TestDefaultModelAliasFallsBackToMoonbridge(t *testing.T) {
	cfg := config.Config{Routes: map[string]config.RouteEntry{
		"moonbridge": {Provider: "default", Model: "claude-test"},
		"other":      {Provider: "default", Model: "claude-other"},
	}}
	if got := cfg.DefaultModelAlias(); got != "moonbridge" {
		t.Fatalf("DefaultModelAlias() = %q", got)
	}
}

func TestCodexModelUsesResponseProxyModelInCaptureResponse(t *testing.T) {
	cfg := config.Config{
		Mode:         config.ModeCaptureResponse,
		DefaultModel: "moonbridge",
		ResponseProxy: config.ResponseProxyConfig{
			Model: "gpt-capture",
		},
	}
	if got := cfg.CodexModel(); got != "gpt-capture" {
		t.Fatalf("CodexModel() = %q", got)
	}
}

func TestCodexModelUsesDefaultModelInTransform(t *testing.T) {
	cfg := config.Config{
		Mode:         config.ModeTransform,
		DefaultModel: "moonbridge",
		ResponseProxy: config.ResponseProxyConfig{
			Model: "gpt-capture",
		},
	}
	if got := cfg.CodexModel(); got != "moonbridge" {
		t.Fatalf("CodexModel() = %q", got)
	}
}

func TestLoadFromYAMLRequiresCaptureProvider(t *testing.T) {
	for name, input := range map[string]string{
		"response":  `mode: CaptureResponse`,
		"anthropic": `mode: CaptureAnthropic`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := config.LoadFromYAML([]byte(input)); err == nil {
				t.Fatal("LoadFromYAML() error = nil, want missing proxy provider error")
			}
		})
	}
}

func TestOverrideAddrUsesSharedServerAddr(t *testing.T) {
	for _, mode := range []config.Mode{config.ModeTransform, config.ModeCaptureResponse, config.ModeCaptureAnthropic} {
		cfg := config.Config{Mode: mode}
		cfg.OverrideAddr("127.0.0.1:19999")
		if cfg.Addr != "127.0.0.1:19999" {
			t.Fatalf("OverrideAddr(%s) = %q", mode, cfg.Addr)
		}
	}
}

func TestLoadFromYAMLRejectsProxyAddr(t *testing.T) {
	_, err := config.LoadFromYAML([]byte(`
mode: CaptureResponse
proxy:
  response:
    addr: 127.0.0.1:19180
    base_url: https://api.openai.example.test
    api_key: upstream-openai-key
`))
	if err == nil {
		t.Fatal("LoadFromYAML() error = nil, want unknown proxy addr error")
	}
}

func TestDumpConfigSchemaSkipsUpToDateSchema(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yml")
	if err := os.WriteFile(configPath, []byte("mode: Transform\n"), 0644); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	// First dump.
	if err := config.DumpConfigSchema(configPath, nil); err != nil {
		t.Fatalf("first DumpConfigSchema() error = %v", err)
	}
	schemaPath := filepath.Join(dir, "config.schema.json")
	fi1, _ := os.Stat(schemaPath)

	// Second dump should not modify the file (version matches).
	if err := config.DumpConfigSchema(configPath, nil); err != nil {
		t.Fatalf("second DumpConfigSchema() error = %v", err)
	}
	fi2, _ := os.Stat(schemaPath)
	if !fi1.ModTime().Equal(fi2.ModTime()) {
		t.Fatal("second dump modified an up-to-date schema file")
	}
}

func TestDumpConfigSchemaSkipsMissingPluginDir(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yml")
	if err := os.WriteFile(configPath, []byte("mode: Transform\n"), 0644); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	// No plugins/ dir at all; should not error.
	if err := config.DumpConfigSchema(configPath, nil); err != nil {
		t.Fatalf("DumpConfigSchema() error = %v", err)
	}
	schemaPath := filepath.Join(dir, "config.schema.json")
	if _, err := os.Stat(schemaPath); err != nil {
		t.Fatalf("main schema not found: %v", err)
	}
}

func TestLoadFromYAMLBackwardCompatTraceRequests(t *testing.T) {
	cfg, err := config.LoadFromYAML([]byte(`
mode: Transform
models:
  claude-test: {}
providers:
  main:
    base_url: https://provider.example.test
    api_key: upstream-key
    offers:
      - model: claude-test
routes:
  gpt-test:
    model: claude-test
    provider: main
trace_requests: true
`))
	if err != nil {
		t.Fatalf("LoadFromYAML() error = %v", err)
	}
	if !cfg.TraceRequests {
		t.Fatal("TraceRequests = false, want true")
	}
}

func TestLoadFromYAMLParsesOffersWithPricing(t *testing.T) {
	cfg, err := config.LoadFromYAML([]byte(`
mode: Transform
models:
  claude-sonnet:
    context_window: 200000
    max_output_tokens: 64000
    display_name: "Claude Sonnet"
providers:
  anthropic:
    base_url: https://api.anthropic.com
    api_key: sk-xxx
    offers:
      - model: claude-sonnet
        upstream_name: claude-sonnet-4-20250514
        pricing:
          input_price: 3.0
          output_price: 15.0
          cache_write_price: 3.75
          cache_read_price: 0.30
routes:
  sonnet:
    model: claude-sonnet
    provider: anthropic
`))
	if err != nil {
		t.Fatalf("LoadFromYAML() error = %v", err)
	}
	if cfg.Models["claude-sonnet"].ContextWindow != 200000 {
		t.Fatalf("Model context_window = %d", cfg.Models["claude-sonnet"].ContextWindow)
	}
	if cfg.Models["claude-sonnet"].DisplayName != "Claude Sonnet" {
		t.Fatalf("Model DisplayName = %q", cfg.Models["claude-sonnet"].DisplayName)
	}
	if cfg.ProviderDefs["anthropic"].Offers[0].UpstreamName != "claude-sonnet-4-20250514" {
		t.Fatalf("Offer UpstreamName = %q", cfg.ProviderDefs["anthropic"].Offers[0].UpstreamName)
	}
	if cfg.ProviderDefs["anthropic"].Offers[0].Pricing.InputPrice != 3.0 {
		t.Fatalf("Offer InputPrice = %f", cfg.ProviderDefs["anthropic"].Offers[0].Pricing.InputPrice)
	}
	offerModel, ok := cfg.ProviderDefs["anthropic"].Models["claude-sonnet-4-20250514"]
	if !ok {
		t.Fatal("Provider model claude-sonnet-4-20250514 not found")
	}
	if offerModel.InputPrice != 3.0 {
		t.Fatalf("Provider model InputPrice = %f", offerModel.InputPrice)
	}
	if offerModel.ContextWindow != 200000 {
		t.Fatalf("Provider model ContextWindow = %d", offerModel.ContextWindow)
	}
	// Route should use upstream name.
	route := cfg.RouteFor("sonnet")
	if route.Model != "claude-sonnet-4-20250514" {
		t.Fatalf("Route Model = %q, want claude-sonnet-4-20250514", route.Model)
	}
	if route.InputPrice != 3.0 {
		t.Fatalf("Route InputPrice = %f", route.InputPrice)
	}
	if route.ContextWindow != 200000 {
		t.Fatalf("Route ContextWindow = %d", route.ContextWindow)
	}
}

func TestLoadFromYAMLDefaultsModelConfig(t *testing.T) {
	cfg, err := config.LoadFromYAML([]byte(`
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
  gpt-test:
    model: claude-test
    provider: main
defaults:
  model: gpt-test
  max_tokens: 4096
  system_prompt: "You are a test assistant"
cache:
  mode: off
`))
	if err != nil {
		t.Fatalf("LoadFromYAML() error = %v", err)
	}
	if cfg.Defaults.Model != "gpt-test" {
		t.Fatalf("Defaults.Model = %q", cfg.Defaults.Model)
	}
	if cfg.Defaults.MaxTokens != 4096 {
		t.Fatalf("Defaults.MaxTokens = %d", cfg.Defaults.MaxTokens)
	}
	if cfg.Defaults.SystemPrompt != "You are a test assistant" {
		t.Fatalf("Defaults.SystemPrompt = %q", cfg.Defaults.SystemPrompt)
	}
	if cfg.DefaultMaxTokens != 4096 {
		t.Fatalf("DefaultMaxTokens = %d", cfg.DefaultMaxTokens)
	}
	if cfg.DefaultModel != "gpt-test" {
		t.Fatalf("DefaultModel = %q", cfg.DefaultModel)
	}
	if cfg.SystemPrompt != "You are a test assistant" {
		t.Fatalf("SystemPrompt = %q", cfg.SystemPrompt)
	}
}

func TestLoadFromYAMLRouteBackwardCompatToField(t *testing.T) {
	cfg, err := config.LoadFromYAML([]byte(`
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
  gpt-test: "main/claude-test"
`))
	if err != nil {
		t.Fatalf("LoadFromYAML() error = %v", err)
	}
	if cfg.RouteFor("gpt-test").Provider != "main" {
		t.Fatalf("Route provider = %q", cfg.RouteFor("gpt-test").Provider)
	}
	if cfg.RouteFor("gpt-test").Model != "claude-test" {
		t.Fatalf("Route model = %q", cfg.RouteFor("gpt-test").Model)
	}
	if cfg.RouteFor("gpt-test").ContextWindow != 200000 {
		t.Fatalf("Route ContextWindow = %d", cfg.RouteFor("gpt-test").ContextWindow)
	}
}

func TestRouteDisplayNameNotInheritedFromModelDef(t *testing.T) {
	// Regression: route should NOT inherit display_name from the underlying model def.
	// Multiple routes pointing to the same model must each have their own display_name
	// derived from the alias slug (by downstream consumers like BuildModelInfoFromRoute).
	cfg, err := config.LoadFromYAML([]byte(`
mode: Transform
models:
  deepseek-v4-pro:
    context_window: 1000000
    display_name: "DeepSeek V4 Pro"
providers:
  deepseek:
    base_url: https://api.deepseek.com/anthropic
    api_key: test
    offers:
      - model: deepseek-v4-pro
routes:
  gpt-5.4:
    model: deepseek-v4-pro
    provider: deepseek
  gpt-5.5:
    model: deepseek-v4-pro
    provider: deepseek
  codex-auto-review:
    model: deepseek-v4-pro
    provider: deepseek
`))
	if err != nil {
		t.Fatalf("LoadFromYAML() error = %v", err)
	}

	// All three routes should have empty DisplayName (not inherited from model def).
	for _, alias := range []string{"gpt-5.4", "gpt-5.5", "codex-auto-review"} {
		route := cfg.RouteFor(alias)
		if route.DisplayName != "" {
			t.Fatalf("route %q: DisplayName = %q, want empty (not inherited from model def)", alias, route.DisplayName)
		}
	}

	// Verify other metadata IS still inherited (context_window, etc.)
	route := cfg.RouteFor("gpt-5.4")
	if route.ContextWindow == 0 || route.ContextWindow != 1000000 {
		t.Fatalf("route gpt-5.4: ContextWindow = %d, want 1000000", route.ContextWindow)
	}
}

func TestRouteExplicitDisplayNameStillWorks(t *testing.T) {
	// Routes with explicit display_name in config should keep it.
	cfg, err := config.LoadFromYAML([]byte(`
mode: Transform
models:
  my-model:
    display_name: "Model Default Name"
providers:
  main:
    base_url: https://example.test
    api_key: test
    offers:
      - model: my-model
routes:
  custom-alias:
    model: my-model
    provider: main
    display_name: "My Custom Display Name"
`))
	if err != nil {
		t.Fatalf("LoadFromYAML() error = %v", err)
	}
	route := cfg.RouteFor("custom-alias")
	if route.DisplayName != "My Custom Display Name" {
		t.Fatalf("DisplayName = %q, want \"My Custom Display Name\"", route.DisplayName)
	}
}
