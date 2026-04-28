package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"moonbridge/internal/foundation/config"
)

func TestLoadFromYAMLParsesTransformConfig(t *testing.T) {
	cfg, err := config.LoadFromYAML([]byte(`
mode: Transform
provider:
  providers:
    main:
      base_url: https://provider.example.test
      api_key: upstream-key
      user_agent: Bun/1.3.13
      web_search:
        support: auto
      models:
        claude-test:
          context_window: 200000
          max_output_tokens: 100000
        claude-fast: {}
  routes:
    gpt-test: "main/claude-test"
    gpt-fast: "main/claude-fast"
  web_search:
    support: auto
    max_uses: 12
  default_model: gpt-test
cache:
  mode: explicit
  ttl: 1h
  min_breakpoint_tokens: 4096
trace_requests: true
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
func TestLoadFromFileMergesSplitPluginConfigFiles(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yml")
	pluginDir := filepath.Join(dir, "plugins")
	if err := os.Mkdir(pluginDir, 0755); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "deepseek_v4.yml"), []byte(`
reinforce_instructions: true
reinforce_prompt: split prompt
`), 0644); err != nil {
		t.Fatalf("WriteFile(plugin) error = %v", err)
	}
	if err := os.WriteFile(configPath, []byte(`
mode: Transform
provider:
  providers:
    main:
      base_url: https://provider.example.test
      api_key: upstream-key
      models:
        claude-test: {}
  routes:
    moonbridge: "main/claude-test"
plugins:
  deepseek_v4:
    reinforce_instructions: false
    inline_only: true
`), 0644); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	cfg, err := config.LoadFromFile(configPath)
	if err != nil {
		t.Fatalf("LoadFromFile() error = %v", err)
	}
	pluginCfg := cfg.PluginConfig("deepseek_v4")
	if got, ok := pluginCfg["reinforce_instructions"].(bool); !ok || !got {
		t.Fatalf("reinforce_instructions = %#v, want true", pluginCfg["reinforce_instructions"])
	}
	if got, ok := pluginCfg["reinforce_prompt"].(string); !ok || got != "split prompt" {
		t.Fatalf("reinforce_prompt = %#v, want split prompt", pluginCfg["reinforce_prompt"])
	}
	if got, ok := pluginCfg["inline_only"].(bool); !ok || !got {
		t.Fatalf("inline_only = %#v, want true", pluginCfg["inline_only"])
	}
}



func TestLoadFromFileLoadsSplitPluginOnly(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yml")
	pluginDir := filepath.Join(dir, "plugins")
	if err := os.Mkdir(pluginDir, 0755); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "my_plugin.yml"), []byte(`
key1: value1
key2: 42
`), 0644); err != nil {
		t.Fatalf("WriteFile(plugin) error = %v", err)
	}
	if err := os.WriteFile(configPath, []byte(`
mode: Transform
provider:
  providers:
    main:
      base_url: https://provider.example.test
      api_key: upstream-key
      models:
        claude-test: {}
  routes:
    moonbridge: "main/claude-test"
`), 0644); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	cfg, err := config.LoadFromFile(configPath)
	if err != nil {
		t.Fatalf("LoadFromFile() error = %v", err)
	}
	pluginCfg := cfg.PluginConfig("my_plugin")
	if pluginCfg == nil {
		t.Fatal("PluginConfig(my_plugin) = nil, want non-nil")
	}
	if got, ok := pluginCfg["key1"].(string); !ok || got != "value1" {
		t.Fatalf("key1 = %#v, want value1", pluginCfg["key1"])
	}
}

func TestLoadFromFileDeduplicatesYmlAndYaml(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yml")
	pluginDir := filepath.Join(dir, "plugins")
	if err := os.Mkdir(pluginDir, 0755); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}
	// Both .yml and .yaml exist for the same base name.
	if err := os.WriteFile(filepath.Join(pluginDir, "my_plugin.yml"), []byte(`
version: from_yml
`), 0644); err != nil {
		t.Fatalf("WriteFile(.yml) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "my_plugin.yaml"), []byte(`
version: from_yaml
`), 0644); err != nil {
		t.Fatalf("WriteFile(.yaml) error = %v", err)
	}
	if err := os.WriteFile(configPath, []byte(`
mode: Transform
provider:
  providers:
    main:
      base_url: https://provider.example.test
      api_key: upstream-key
      models:
        claude-test: {}
  routes:
    moonbridge: "main/claude-test"
`), 0644); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	cfg, err := config.LoadFromFile(configPath)
	if err != nil {
		t.Fatalf("LoadFromFile() error = %v", err)
	}
	pluginCfg := cfg.PluginConfig("my_plugin")
	if pluginCfg == nil {
		t.Fatal("PluginConfig(my_plugin) = nil, want non-nil")
	}
	// Exactly one of the two files should have been loaded (no merge).
	version, ok := pluginCfg["version"].(string)
	if !ok {
		t.Fatalf("version missing or wrong type: %#v", pluginCfg["version"])
	}
	if version != "from_yml" && version != "from_yaml" {
		t.Fatalf("version = %q, want either from_yml or from_yaml", version)
	}
	// Only one key should exist.
	if len(pluginCfg) != 1 {
		t.Fatalf("pluginCfg has %d keys, want exactly 1", len(pluginCfg))
	}
}

func TestLoadFromFileSkipsEmptyPluginFile(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yml")
	pluginDir := filepath.Join(dir, "plugins")
	if err := os.Mkdir(pluginDir, 0755); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}
	// Empty plugin file.
	if err := os.WriteFile(filepath.Join(pluginDir, "empty_plugin.yml"), []byte("   \n\n"), 0644); err != nil {
		t.Fatalf("WriteFile(empty plugin) error = %v", err)
	}
	if err := os.WriteFile(configPath, []byte(`
mode: Transform
provider:
  providers:
    main:
      base_url: https://provider.example.test
      api_key: upstream-key
      models:
        claude-test: {}
  routes:
    moonbridge: "main/claude-test"
`), 0644); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	cfg, err := config.LoadFromFile(configPath)
	if err != nil {
		t.Fatalf("LoadFromFile() error = %v", err)
	}
	if got := cfg.PluginConfig("empty_plugin"); got != nil {
		t.Fatalf("PluginConfig(empty_plugin) = %#v, want nil", got)
	}
}
func TestLoadFromYAMLCanDisableWebSearch(t *testing.T) {
	cfg, err := config.LoadFromYAML([]byte(`
mode: Transform
provider:
  providers:
    main:
      base_url: https://provider.example.test
      api_key: upstream-key
      models:
        claude-test: {}
  routes:
    moonbridge: "main/claude-test"
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
	cfg, err := config.LoadFromYAML([]byte(`
mode: Transform
provider:
  providers:
    deepseek:
      base_url: https://deepseek.example.test
      api_key: deepseek-key
      models:
        deepseek-v4-pro:
          deepseek_v4: true
          default_reasoning_level: high
          supported_reasoning_levels:
            - effort: high
              description: High reasoning effort
            - effort: xhigh
              description: Extra high reasoning effort
    openai:
      base_url: https://openai.example.test
      api_key: openai-key
      protocol: openai-response
      models:
        gpt-image-1.5: {}
  routes:
    moonbridge: "deepseek/deepseek-v4-pro"
    image: "openai/gpt-image-1.5"
`))
	if err != nil {
		t.Fatalf("LoadFromYAML() error = %v", err)
	}
	if cfg.ProviderDefs["openai"].Protocol != config.ProtocolOpenAIResponse {
		t.Fatalf("openai provider = %+v", cfg.ProviderDefs["openai"])
	}
	if !cfg.DeepSeekV4ForModel("moonbridge") {
		t.Fatalf("DeepSeekV4ForModel(moonbridge) = false, want true")
	}
	if cfg.DeepSeekV4ForModel("image") {
		t.Fatalf("DeepSeekV4ForModel(image) = true, want false")
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

func TestLoadFromYAMLAllowsProviderModelCatalogWithoutRoutes(t *testing.T) {
	cfg, err := config.LoadFromYAML([]byte(`
mode: Transform
provider:
  providers:
    main:
      base_url: https://provider.example.test
      api_key: upstream-key
      models:
        claude-test:
          context_window: 200000
  default_model: main/claude-test
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
provider:
  providers:
    openai:
      api_key: openai-key
      protocol: openai-response
      models:
        gpt-image-1.5: {}
  routes:
    image: "openai/gpt-image-1.5"
`,
		"invalid protocol": `
mode: Transform
provider:
  providers:
    openai:
      base_url: https://openai.example.test
      api_key: openai-key
      protocol: responses
      models:
        gpt-image-1.5: {}
  routes:
    image: "openai/gpt-image-1.5"
`,
		"old openai protocol name removed": `
mode: Transform
provider:
  providers:
    openai:
      base_url: https://openai.example.test
      api_key: openai-key
      protocol: openai
      models:
        gpt-image-1.5: {}
  routes:
    image: "openai/gpt-image-1.5"
`,
		"missing provider model catalog and routes": `
mode: Transform
provider:
  providers:
    openai:
      base_url: https://openai.example.test
      api_key: openai-key
      protocol: openai-response
`,
		"empty provider model name": `
mode: Transform
provider:
  providers:
    openai:
      base_url: https://openai.example.test
      api_key: openai-key
      protocol: openai-response
      models:
        "": {}
`,
		"empty route model": `
mode: Transform
provider:
  providers:
    openai:
      base_url: https://openai.example.test
      api_key: openai-key
      protocol: openai-response
  routes:
    image: "openai/"
`,
		"deepseek extension on openai-response protocol": `
mode: Transform
provider:
  providers:
    openai:
      base_url: https://openai.example.test
      api_key: openai-key
      protocol: openai-response
      deepseek_v4: true
      models:
        gpt-image-1.5: {}
  routes:
    image: "openai/gpt-image-1.5"
`,
		"global deepseek extension removed": `
mode: Transform
provider:
  providers:
    deepseek:
      base_url: https://deepseek.example.test
      api_key: deepseek-key
      models:
        deepseek-v4-pro: {}
  routes:
    moonbridge: "deepseek/deepseek-v4-pro"
  deepseek_v4: true
`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := config.LoadFromYAML([]byte(input)); err == nil {
				t.Fatal("LoadFromYAML() error = nil, want validation error")
			}
		})
	}
}

func TestLoadFromYAMLRejectsInvalidWebSearchSupport(t *testing.T) {
	_, err := config.LoadFromYAML([]byte(`
mode: Transform
provider:
  providers:
    main:
      base_url: https://provider.example.test
      api_key: upstream-key
      models:
        claude-test: {}
  routes:
    moonbridge: "main/claude-test"
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
provider:
  providers:
    main:
      base_url: https://provider.example.test
      api_key: upstream-key
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
  routes:
    gpt-test: "main/claude-test"
`))
	if err != nil {
		t.Fatalf("LoadFromYAML() error = %v", err)
	}
	route := cfg.RouteFor("gpt-test")
	if route.DisplayName != "Claude Test" {
		t.Fatalf("DisplayName = %q", route.DisplayName)
	}
	if route.Description != "A test model" {
		t.Fatalf("Description = %q", route.Description)
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
provider:
  providers:
    main:
      base_url: https://provider.example.test
      api_key: upstream-key
      models:
        claude-test: {}
  routes:
    gpt-test: "main/claude-test"
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
provider:
  providers:
    main:
      base_url: https://provider.example.test
      api_key: upstream-key
  routes:
    moonbridge: "main/"
`))
	if err == nil {
		t.Fatal("LoadFromYAML() error = nil, want empty route model error")
	}
}
func TestLoadFromYAMLParsesCaptureResponseConfig(t *testing.T) {

	cfg, err := config.LoadFromYAML([]byte(`
mode: CaptureResponse
trace_requests: true
developer:
  proxy:
    response:
      model: gpt-capture
      provider:
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
trace_requests: true
developer:
  proxy:
    anthropic:
      model: claude-test
      provider:
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
developer:
  proxy:
    response:
      addr: 127.0.0.1:19180
      provider:
        base_url: https://api.openai.example.test
        api_key: upstream-openai-key
`))
	if err == nil {
		t.Fatal("LoadFromYAML() error = nil, want unknown proxy addr error")
	}
}


func TestDumpConfigSchemaWritesMainSchemaAndPluginSchemas(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yml")
	pluginDir := filepath.Join(dir, "plugins")
	if err := os.Mkdir(pluginDir, 0755); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "deepseek_v4.yml"), []byte("key: val\n"), 0644); err != nil {
		t.Fatalf("WriteFile(plugin) error = %v", err)
	}
	if err := os.WriteFile(configPath, []byte("mode: Transform\n"), 0644); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	if err := config.DumpConfigSchema(configPath, map[string]func() any{
		"deepseek_v4": func() any { return testDeepSeekV4Config() },
	}); err != nil {
		t.Fatalf("DumpConfigSchema() error = %v", err)
	}

	// Main schema should exist.
	mainSchemaPath := filepath.Join(dir, "config.schema.json")
	if _, err := os.Stat(mainSchemaPath); err != nil {
		t.Fatalf("main schema not found: %v", err)
	}
	mainData, err := os.ReadFile(mainSchemaPath)
	if err != nil {
		t.Fatalf("read main schema: %v", err)
	}
	if !strings.Contains(string(mainData), "$metadata") {
		t.Fatal("main schema missing $metadata")
	}

	// Plugin schema should exist.
	pluginSchemaPath := filepath.Join(pluginDir, "deepseek_v4.schema.json")
	if _, err := os.Stat(pluginSchemaPath); err != nil {
		t.Fatalf("plugin schema not found: %v", err)
	}

	// Plugin file schema should have field-level definitions.
	pluginData, err := os.ReadFile(pluginSchemaPath)
	if err != nil {
		t.Fatalf("read plugin schema: %v", err)
	}
	pluginStr := string(pluginData)
	if !strings.Contains(pluginStr, "reinforce_instructions") {
		t.Fatalf("plugin schema missing reinforce_instructions field; content (first 500 chars): %s", pluginStr[:min(500, len(pluginStr))])
	}
}

func TestDumpConfigSchemaSkipsUpToDateSchema(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yml")
	if err := os.WriteFile(configPath, []byte("mode: Transform\n"), 0644); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	// First dump.
	pluginTypes := map[string]func() any{
		"deepseek_v4": func() any { return testDeepSeekV4Config() },
	}
	if err := config.DumpConfigSchema(configPath, pluginTypes); err != nil {
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


// testPluginConfig is a minimal config struct used for schema generation tests.
type testPluginConfig struct {
	ReinforceInstructions *bool   `json:"reinforce_instructions,omitempty"`
	ReinforcePrompt       *string `json:"reinforce_prompt,omitempty"`
}

func testDeepSeekV4Config() *testPluginConfig {
	return &testPluginConfig{}
}
