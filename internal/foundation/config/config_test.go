package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	deepseekv4 "moonbridge/internal/extension/deepseek_v4"
	"moonbridge/internal/extension/visual"
	"moonbridge/internal/foundation/config"
)

func builtinExtensionSpecsForTest() []config.ExtensionConfigSpec {
	specs := append([]config.ExtensionConfigSpec{}, deepseekv4.ConfigSpecs()...)
	specs = append(specs, visual.ConfigSpecs()...)
	return specs
}

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

func writeSecretFile(t *testing.T, dir, name, value string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(value), 0600); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", path, err)
	}
	return path
}

func TestLoadFromFileResolvesServerAuthTokenFile(t *testing.T) {
	dir := t.TempDir()
	writeSecretFile(t, dir, "server-token", "  file-token\n")
	configPath := filepath.Join(dir, "config.yml")
	if err := os.WriteFile(configPath, []byte(`
mode: CaptureResponse
server:
  auth_token_file: server-token
developer:
  proxy:
    response:
      model: gpt-capture
      provider:
        base_url: https://api.openai.example.test
        api_key: upstream-openai-key
`), 0644); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	cfg, err := config.LoadFromFile(configPath)
	if err != nil {
		t.Fatalf("LoadFromFile() error = %v", err)
	}
	if cfg.AuthToken != "file-token" {
		t.Fatalf("AuthToken = %q, want file-token", cfg.AuthToken)
	}
}

func TestLoadFromYAMLResolvesLegacyProviderAPIKeyFile(t *testing.T) {
	dir := t.TempDir()
	keyPath := writeSecretFile(t, dir, "provider-key", "\nlegacy-key\n")

	cfg, err := config.LoadFromYAMLWithOptions([]byte(`
mode: Transform
provider:
  base_url: https://provider.example.test
  api_key_file: `+keyPath+`
  routes:
    moonbridge: claude-test
`), config.LoadOptions{})
	if err != nil {
		t.Fatalf("LoadFromYAMLWithOptions() error = %v", err)
	}
	if cfg.ProviderAPIKey != "legacy-key" {
		t.Fatalf("ProviderAPIKey = %q, want legacy-key", cfg.ProviderAPIKey)
	}
}

func TestLoadFromYAMLResolvesMultiProviderAPIKeyFile(t *testing.T) {
	dir := t.TempDir()
	keyPath := writeSecretFile(t, dir, "main-key", "main-key\n")

	cfg, err := config.LoadFromYAMLWithOptions([]byte(`
mode: Transform
provider:
  providers:
    main:
      base_url: https://provider.example.test
      api_key_file: `+keyPath+`
      models:
        claude-test: {}
  routes:
    moonbridge: main/claude-test
`), config.LoadOptions{})
	if err != nil {
		t.Fatalf("LoadFromYAMLWithOptions() error = %v", err)
	}
	if cfg.ProviderDefs["main"].APIKey != "main-key" {
		t.Fatalf("ProviderDefs[main].APIKey = %q, want main-key", cfg.ProviderDefs["main"].APIKey)
	}
}

func TestLoadFromYAMLResolvesCaptureProxyAPIKeyFiles(t *testing.T) {
	dir := t.TempDir()
	responseKeyPath := writeSecretFile(t, dir, "response-key", "response-key")
	anthropicKeyPath := writeSecretFile(t, dir, "anthropic-key", "anthropic-key")

	responseCfg, err := config.LoadFromYAMLWithOptions([]byte(`
mode: CaptureResponse
developer:
  proxy:
    response:
      model: gpt-capture
      provider:
        base_url: https://api.openai.example.test
        api_key_file: `+responseKeyPath+`
`), config.LoadOptions{})
	if err != nil {
		t.Fatalf("LoadFromYAMLWithOptions(response) error = %v", err)
	}
	if responseCfg.ResponseProxy.ProviderAPIKey != "response-key" {
		t.Fatalf("ResponseProxy.ProviderAPIKey = %q, want response-key", responseCfg.ResponseProxy.ProviderAPIKey)
	}

	anthropicCfg, err := config.LoadFromYAMLWithOptions([]byte(`
mode: CaptureAnthropic
developer:
  proxy:
    anthropic:
      model: claude-test
      provider:
        base_url: https://provider.example.test
        api_key_file: `+anthropicKeyPath+`
`), config.LoadOptions{})
	if err != nil {
		t.Fatalf("LoadFromYAMLWithOptions(anthropic) error = %v", err)
	}
	if anthropicCfg.AnthropicProxy.ProviderAPIKey != "anthropic-key" {
		t.Fatalf("AnthropicProxy.ProviderAPIKey = %q, want anthropic-key", anthropicCfg.AnthropicProxy.ProviderAPIKey)
	}
}

func TestLoadFromYAMLResolvesWebSearchAPIKeyFiles(t *testing.T) {
	dir := t.TempDir()
	globalTavily := writeSecretFile(t, dir, "global-tavily", "global-tavily")
	globalFirecrawl := writeSecretFile(t, dir, "global-firecrawl", "global-firecrawl")
	providerTavily := writeSecretFile(t, dir, "provider-tavily", "provider-tavily")
	providerFirecrawl := writeSecretFile(t, dir, "provider-firecrawl", "provider-firecrawl")
	modelTavily := writeSecretFile(t, dir, "model-tavily", "model-tavily")
	modelFirecrawl := writeSecretFile(t, dir, "model-firecrawl", "model-firecrawl")

	cfg, err := config.LoadFromYAMLWithOptions([]byte(`
mode: Transform
provider:
  web_search:
    support: auto
    tavily_api_key_file: `+globalTavily+`
    firecrawl_api_key_file: `+globalFirecrawl+`
  providers:
    main:
      base_url: https://provider.example.test
      api_key: upstream-key
      web_search:
        support: auto
        tavily_api_key_file: `+providerTavily+`
        firecrawl_api_key_file: `+providerFirecrawl+`
      models:
        claude-test:
          web_search:
            support: injected
            tavily_api_key_file: `+modelTavily+`
            firecrawl_api_key_file: `+modelFirecrawl+`
  routes:
    moonbridge: main/claude-test
`), config.LoadOptions{})
	if err != nil {
		t.Fatalf("LoadFromYAMLWithOptions() error = %v", err)
	}
	if cfg.TavilyAPIKey != "global-tavily" || cfg.FirecrawlAPIKey != "global-firecrawl" {
		t.Fatalf("global web search keys = %q/%q", cfg.TavilyAPIKey, cfg.FirecrawlAPIKey)
	}
	if got := cfg.WebSearchTavilyKeyForProvider("main"); got != "provider-tavily" {
		t.Fatalf("WebSearchTavilyKeyForProvider(main) = %q", got)
	}
	if got := cfg.WebSearchFirecrawlKeyForProvider("main"); got != "provider-firecrawl" {
		t.Fatalf("WebSearchFirecrawlKeyForProvider(main) = %q", got)
	}
	if got := cfg.WebSearchTavilyKeyForModel("moonbridge"); got != "model-tavily" {
		t.Fatalf("WebSearchTavilyKeyForModel(moonbridge) = %q", got)
	}
	if got := cfg.WebSearchFirecrawlKeyForModel("moonbridge"); got != "model-firecrawl" {
		t.Fatalf("WebSearchFirecrawlKeyForModel(moonbridge) = %q", got)
	}
}

func TestLoadFromYAMLRejectsInlineAndFileSecretConflict(t *testing.T) {
	dir := t.TempDir()
	keyPath := writeSecretFile(t, dir, "provider-key", "provider-key")

	_, err := config.LoadFromYAMLWithOptions([]byte(`
mode: Transform
provider:
  providers:
    main:
      base_url: https://provider.example.test
      api_key: inline-key
      api_key_file: `+keyPath+`
      models:
        claude-test: {}
  routes:
    moonbridge: main/claude-test
`), config.LoadOptions{})
	if err == nil {
		t.Fatal("LoadFromYAMLWithOptions() error = nil, want inline/file conflict")
	}
	if !strings.Contains(err.Error(), "provider.providers.main.api_key") || !strings.Contains(err.Error(), "provider.providers.main.api_key_file") {
		t.Fatalf("LoadFromYAMLWithOptions() error = %v, want field paths", err)
	}
}

func TestLoadFromYAMLRejectsMissingSecretFile(t *testing.T) {
	_, err := config.LoadFromYAMLWithOptions([]byte(`
mode: CaptureResponse
developer:
  proxy:
    response:
      model: gpt-capture
      provider:
        base_url: https://api.openai.example.test
        api_key_file: /no/such/provider-key
`), config.LoadOptions{})
	if err == nil {
		t.Fatal("LoadFromYAMLWithOptions() error = nil, want missing file error")
	}
	if !strings.Contains(err.Error(), "developer.proxy.response.provider.api_key_file") {
		t.Fatalf("LoadFromYAMLWithOptions() error = %v, want field path", err)
	}
}

func TestLoadFromYAMLRejectsEmptySecretFile(t *testing.T) {
	dir := t.TempDir()
	keyPath := writeSecretFile(t, dir, "empty-key", " \n\t")

	_, err := config.LoadFromYAMLWithOptions([]byte(`
mode: CaptureAnthropic
developer:
  proxy:
    anthropic:
      model: claude-test
      provider:
        base_url: https://provider.example.test
        api_key_file: `+keyPath+`
`), config.LoadOptions{})
	if err == nil {
		t.Fatal("LoadFromYAMLWithOptions() error = nil, want empty file error")
	}
	if !strings.Contains(err.Error(), "developer.proxy.anthropic.provider.api_key_file") {
		t.Fatalf("LoadFromYAMLWithOptions() error = %v, want field path", err)
	}
}

func TestLoadFromYAMLRejectsRelativeSecretFileWithoutConfigDir(t *testing.T) {
	_, err := config.LoadFromYAMLWithOptions([]byte(`
mode: CaptureResponse
developer:
  proxy:
    response:
      model: gpt-capture
      provider:
        base_url: https://api.openai.example.test
        api_key_file: provider-key
`), config.LoadOptions{})
	if err == nil {
		t.Fatal("LoadFromYAMLWithOptions() error = nil, want relative path error")
	}
	if !strings.Contains(err.Error(), "developer.proxy.response.provider.api_key_file") || !strings.Contains(err.Error(), "ConfigDir") {
		t.Fatalf("LoadFromYAMLWithOptions() error = %v, want ConfigDir field path error", err)
	}
}

func TestLoadFromYAMLResolvesRelativeSecretFileWithConfigDir(t *testing.T) {
	dir := t.TempDir()
	writeSecretFile(t, dir, "provider-key", "relative-key")

	cfg, err := config.LoadFromYAMLWithOptions([]byte(`
mode: CaptureResponse
developer:
  proxy:
    response:
      model: gpt-capture
      provider:
        base_url: https://api.openai.example.test
        api_key_file: provider-key
`), config.LoadOptions{ConfigDir: dir})
	if err != nil {
		t.Fatalf("LoadFromYAMLWithOptions() error = %v", err)
	}
	if cfg.ResponseProxy.ProviderAPIKey != "relative-key" {
		t.Fatalf("ResponseProxy.ProviderAPIKey = %q, want relative-key", cfg.ResponseProxy.ProviderAPIKey)
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
	cfg, err := config.LoadFromYAMLWithOptions([]byte(`
mode: Transform
provider:
  providers:
    deepseek:
      base_url: https://deepseek.example.test
      api_key: deepseek-key
      models:
        deepseek-v4-pro:
          extensions:
            deepseek_v4:
              enabled: true
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
extensions:
  visual:
    config:
      provider: kimi
      model: kimi-vision
      max_rounds: 3
      max_tokens: 1024
provider:
  providers:
    deepseek:
      base_url: https://deepseek.example.test
      api_key: deepseek-key
      models:
        deepseek-v4-pro:
          extensions:
            visual:
              enabled: true
    kimi:
      base_url: https://kimi.example.test/v1
      api_key: kimi-key
  routes:
    moonbridge: "deepseek/deepseek-v4-pro"
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
provider:
  providers:
    deepseek:
      base_url: https://deepseek.example.test
      api_key: deepseek-key
      models:
        deepseek-v4-pro:
          visual: true
    kimi:
      base_url: https://kimi.example.test/v1
      api_key: kimi-key
      models:
        kimi-vision: {}
  routes:
    moonbridge: "deepseek/deepseek-v4-pro"
  visual:
    enabled: true
    provider: kimi
    model: kimi-vision
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
      models:
        gpt-image-1.5:
          extensions:
            deepseek_v4:
              enabled: true
  routes:
    image: "openai/gpt-image-1.5"
`,
		"global deepseek extension on openai-response protocol": `
mode: Transform
extensions:
  deepseek_v4:
    enabled: true
provider:
  providers:
    openai:
      base_url: https://openai.example.test
      api_key: openai-key
      protocol: openai-response
      models:
        gpt-image-1.5: {}
  routes:
    image: "openai/gpt-image-1.5"
`,
		"visual provider missing": `
mode: Transform
extensions:
  visual:
    config:
      model: kimi-vision
provider:
  providers:
    deepseek:
      base_url: https://deepseek.example.test
      api_key: deepseek-key
      models:
        deepseek-v4-pro:
          extensions:
            visual:
              enabled: true
  routes:
    moonbridge: "deepseek/deepseek-v4-pro"
`,
		"visual provider on openai-response protocol": `
mode: Transform
extensions:
  visual:
    config:
      provider: openai
      model: gpt-4o
provider:
  providers:
    deepseek:
      base_url: https://deepseek.example.test
      api_key: deepseek-key
      models:
        deepseek-v4-pro:
          extensions:
            visual:
              enabled: true
    openai:
      base_url: https://openai.example.test
      api_key: openai-key
      protocol: openai-response
  routes:
    moonbridge: "deepseek/deepseek-v4-pro"
`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := config.LoadFromYAMLWithOptions([]byte(input), config.LoadOptions{ExtensionSpecs: builtinExtensionSpecsForTest()}); err == nil {
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
	// Route DisplayName/Description are no longer auto-copied from upstream model.
	// They should be empty unless explicitly set in route config.
	if route.DisplayName != "" {
		t.Fatalf("DisplayName = %q, want empty", route.DisplayName)
	}
	if route.Description != "" {
		t.Fatalf("Description = %q, want empty", route.Description)
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
