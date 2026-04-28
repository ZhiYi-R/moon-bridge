package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type FileConfig struct {
	Mode          string                         `yaml:"mode" json:"mode"`
	TraceRequests bool                           `yaml:"trace_requests" json:"trace_requests,omitempty"`
	Log           LogFileConfig                  `yaml:"log" json:"log,omitempty"`
	Server        ServerFileConfig               `yaml:"server" json:"server,omitempty"`
	Provider      ProviderFileConfig             `yaml:"provider" json:"provider,omitempty"`
	Cache         CacheFileConfig                `yaml:"cache" json:"cache,omitempty"`
	SystemPrompt  string                         `yaml:"system_prompt" json:"system_prompt,omitempty"`
	Developer     DeveloperFileConfig            `yaml:"developer" json:"developer,omitempty"`
	Plugins       map[string]any                 `yaml:"plugins" json:"plugins,omitempty"`
	Extensions    map[string]ExtensionFileConfig `yaml:"extensions" json:"extensions,omitempty"`
}

type ServerFileConfig struct {
	Addr      string `yaml:"addr" json:"addr,omitempty"`
	AuthToken string `yaml:"auth_token" json:"auth_token,omitempty"`
}

type ProviderFileConfig struct {
	BaseURL          string                           `yaml:"base_url" json:"base_url,omitempty"`
	APIKey           string                           `yaml:"api_key" json:"api_key,omitempty"`
	Version          string                           `yaml:"version" json:"version,omitempty"`
	UserAgent        string                           `yaml:"user_agent" json:"user_agent,omitempty"`
	WebSearch        WebSearchFileConfig              `yaml:"web_search" json:"web_search,omitempty"`
	DefaultMaxTokens int                              `yaml:"default_max_tokens" json:"default_max_tokens,omitempty"`
	DefaultModel     string                           `yaml:"default_model" json:"default_model,omitempty"`
	Providers        map[string]ProviderDefFileConfig `yaml:"providers" json:"providers,omitempty"`
	Routes           map[string]RouteFileConfig       `yaml:"routes" json:"routes,omitempty"`
}

type CacheFileConfig struct {
	Mode                     string `yaml:"mode" json:"mode,omitempty"`
	TTL                      string `yaml:"ttl" json:"ttl,omitempty"`
	PromptCaching            *bool  `yaml:"prompt_caching" json:"prompt_caching,omitempty"`
	AutomaticPromptCache     *bool  `yaml:"automatic_prompt_cache" json:"automatic_prompt_cache,omitempty"`
	ExplicitCacheBreakpoints *bool  `yaml:"explicit_cache_breakpoints" json:"explicit_cache_breakpoints,omitempty"`
	AllowRetentionDowngrade  *bool  `yaml:"allow_retention_downgrade" json:"allow_retention_downgrade,omitempty"`
	MaxBreakpoints           int    `yaml:"max_breakpoints" json:"max_breakpoints,omitempty"`
	MinCacheTokens           int    `yaml:"min_cache_tokens" json:"min_cache_tokens,omitempty"`
	ExpectedReuse            int    `yaml:"expected_reuse" json:"expected_reuse,omitempty"`
	MinimumValueScore        int    `yaml:"minimum_value_score" json:"minimum_value_score,omitempty"`
	MinBreakpointTokens      int    `yaml:"min_breakpoint_tokens" json:"min_breakpoint_tokens,omitempty"`
}

// ProviderModelFileConfig defines metadata for a model in a provider's catalog.
// The map key is the upstream model name.
type ProviderModelFileConfig struct {
	ContextWindow   int                    `yaml:"context_window" json:"context_window,omitempty"`
	MaxOutputTokens int                    `yaml:"max_output_tokens" json:"max_output_tokens,omitempty"`
	Pricing         ModelPricingFileConfig `yaml:"pricing" json:"pricing,omitempty"`
	// Codex model catalog metadata (injected into /v1/models responses).
	DisplayName                string                           `yaml:"display_name" json:"display_name,omitempty"`
	Description                string                           `yaml:"description" json:"description,omitempty"`
	DefaultReasoningLevel      string                           `yaml:"default_reasoning_level" json:"default_reasoning_level,omitempty"`
	SupportedReasoningLevels   []ReasoningLevelPresetFileConfig `yaml:"supported_reasoning_levels" json:"supported_reasoning_levels,omitempty"`
	SupportsReasoningSummaries *bool                            `yaml:"supports_reasoning_summaries" json:"supports_reasoning_summaries,omitempty"`
	DefaultReasoningSummary    string                           `yaml:"default_reasoning_summary" json:"default_reasoning_summary,omitempty"`
	WebSearch                  WebSearchFileConfig              `yaml:"web_search" json:"web_search,omitempty"`
	Extensions                 map[string]ExtensionFileConfig   `yaml:"extensions" json:"extensions,omitempty"`
}

type ProviderDefFileConfig struct {
	BaseURL    string                             `yaml:"base_url" json:"base_url"`
	APIKey     string                             `yaml:"api_key" json:"api_key"`
	Version    string                             `yaml:"version" json:"version,omitempty"`
	UserAgent  string                             `yaml:"user_agent" json:"user_agent,omitempty"`
	Protocol   string                             `yaml:"protocol" json:"protocol,omitempty"`
	WebSearch  WebSearchFileConfig                `yaml:"web_search" json:"web_search,omitempty"`
	Extensions map[string]ExtensionFileConfig     `yaml:"extensions" json:"extensions,omitempty"`
	Models     map[string]ProviderModelFileConfig `yaml:"models" json:"models,omitempty"`
}

type RouteFileConfig struct {
	To         string                         `yaml:"to" json:"to,omitempty"`
	Extensions map[string]ExtensionFileConfig `yaml:"extensions" json:"extensions,omitempty"`
}

func (cfg *RouteFileConfig) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		cfg.To = value.Value
		return nil
	case yaml.MappingNode:
		type routeFileConfig RouteFileConfig
		var out routeFileConfig
		if err := value.Decode(&out); err != nil {
			return err
		}
		*cfg = RouteFileConfig(out)
		return nil
	default:
		return fmt.Errorf("route must be a string or mapping")
	}
}

type ModelPricingFileConfig struct {
	InputPrice      float64 `yaml:"input_price" json:"input_price,omitempty"`
	OutputPrice     float64 `yaml:"output_price" json:"output_price,omitempty"`
	CacheWritePrice float64 `yaml:"cache_write_price" json:"cache_write_price,omitempty"`
	CacheReadPrice  float64 `yaml:"cache_read_price" json:"cache_read_price,omitempty"`
}

// ReasoningLevelPresetFileConfig maps to Codex's ReasoningEffortPreset.
type ReasoningLevelPresetFileConfig struct {
	Effort      string `yaml:"effort" json:"effort,omitempty"`
	Description string `yaml:"description" json:"description,omitempty"`
}

type WebSearchFileConfig struct {
	Support         string `yaml:"support" json:"support,omitempty"`
	MaxUses         int    `yaml:"max_uses" json:"max_uses,omitempty"`
	TavilyAPIKey    string `yaml:"tavily_api_key" json:"tavily_api_key,omitempty"`
	FirecrawlAPIKey string `yaml:"firecrawl_api_key" json:"firecrawl_api_key,omitempty"`
	SearchMaxRounds int    `yaml:"search_max_rounds" json:"search_max_rounds,omitempty"`
}

type DeveloperFileConfig struct {
	Proxy DeveloperProxyFileConfig `yaml:"proxy" json:"proxy,omitempty"`
}

type DeveloperProxyFileConfig struct {
	Response  ProxyFileConfig `yaml:"response" json:"response,omitempty"`
	Anthropic ProxyFileConfig `yaml:"anthropic" json:"anthropic,omitempty"`
}

type ProxyFileConfig struct {
	Model    string                  `yaml:"model" json:"model,omitempty"`
	Provider ProxyProviderFileConfig `yaml:"provider" json:"provider,omitempty"`
}

type ProxyProviderFileConfig struct {
	BaseURL string `yaml:"base_url" json:"base_url,omitempty"`
	APIKey  string `yaml:"api_key" json:"api_key,omitempty"`
	Version string `yaml:"version" json:"version,omitempty"`
}

type LogFileConfig struct {
	Level  string `yaml:"level" json:"level,omitempty"`
	Format string `yaml:"format" json:"format,omitempty"`
}

func LoadFromFile(path string) (Config, error) {
	return LoadFromFileWithOptions(path, LoadOptions{})
}

func LoadFromFileWithOptions(path string, opts LoadOptions) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config %s: %w", path, err)
	}
	fileConfig, err := decodeFileConfig(data)
	if err != nil {
		return Config{}, err
	}
	if err := loadPluginConfigFiles(path, &fileConfig); err != nil {
		return Config{}, err
	}
	cfg, err := FromFileConfigWithOptions(fileConfig, opts)
	if err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// LoadFromYAML parses YAML bytes into a Config. Unlike LoadFromFile, it does
// not discover split plugin config files from the plugins/ directory; it only
// processes the inline plugins: section of the provided YAML content.
func LoadFromYAML(data []byte) (Config, error) {
	return LoadFromYAMLWithOptions(data, LoadOptions{})
}

func LoadFromYAMLWithOptions(data []byte, opts LoadOptions) (Config, error) {
	fileConfig, err := decodeFileConfig(data)
	if err != nil {
		return Config{}, err
	}
	return FromFileConfigWithOptions(fileConfig, opts)
}

func decodeFileConfig(data []byte) (FileConfig, error) {
	var fileConfig FileConfig
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&fileConfig); err != nil {
		return FileConfig{}, err
	}
	return fileConfig, nil
}

func ResolveConfigPath(explicitPath string) (string, error) {
	if path := strings.TrimSpace(explicitPath); path != "" {
		return path, nil
	}
	return XDGDefaultConfigPath()
}

func XDGDefaultConfigPath() (string, error) {
	base := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME"))
	if base == "" {
		home := strings.TrimSpace(os.Getenv("HOME"))
		if home == "" {
			return "", errors.New("HOME is not set and XDG_CONFIG_HOME is empty")
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, AppConfigDirName, DefaultConfigFileName), nil
}

func loadPluginConfigFiles(configPath string, fileConfig *FileConfig) error {
	pluginDir := filepath.Join(filepath.Dir(configPath), DefaultPluginConfigDirName)
	entries, err := os.ReadDir(pluginDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read plugin config dir %s: %w", pluginDir, err)
	}
	seen := make(map[string]bool, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !isYAMLFile(entry.Name()) {
			continue
		}
		baseName := strings.TrimSuffix(strings.TrimSuffix(entry.Name(), ".yaml"), ".yml")
		if strings.TrimSpace(baseName) == "" {
			continue
		}
		// Deduplicate: skip if we already processed a .yml variant of this base name.
		if seen[baseName] {
			continue
		}
		seen[baseName] = true
		pluginPath := filepath.Join(pluginDir, entry.Name())
		raw, err := os.ReadFile(pluginPath)
		if err != nil {
			return fmt.Errorf("read plugin config %s: %w", pluginPath, err)
		}
		pluginConfig, err := decodePluginConfig(raw)
		if err != nil {
			return fmt.Errorf("parse plugin config %s: %w", pluginPath, err)
		}
		// Skip empty or whitespace-only plugin files.
		if len(pluginConfig) == 0 {
			continue
		}
		mergePluginConfig(fileConfig, baseName, pluginConfig)
	}
	return nil
}

func mergePluginConfig(fileConfig *FileConfig, pluginName string, pluginConfig map[string]any) {
	if fileConfig.Plugins == nil {
		fileConfig.Plugins = make(map[string]any)
	}
	if existing, ok := fileConfig.Plugins[pluginName].(map[string]any); ok {
		merged := make(map[string]any, len(existing)+len(pluginConfig))
		for key, value := range existing {
			merged[key] = value
		}
		for key, value := range pluginConfig {
			merged[key] = value
		}
		fileConfig.Plugins[pluginName] = merged
		return
	}
	fileConfig.Plugins[pluginName] = pluginConfig
}

func decodePluginConfig(data []byte) (map[string]any, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return map[string]any{}, nil
	}
	var pluginConfig map[string]any
	if err := yaml.Unmarshal(data, &pluginConfig); err != nil {
		return nil, err
	}
	if pluginConfig == nil {
		return map[string]any{}, nil
	}
	return pluginConfig, nil
}

func isYAMLFile(name string) bool {
	return strings.HasSuffix(name, ".yml") || strings.HasSuffix(name, ".yaml")
}

func FromFileConfig(fileConfig FileConfig) (Config, error) {
	return FromFileConfigWithOptions(fileConfig, LoadOptions{})
}

func FromFileConfigWithOptions(fileConfig FileConfig, opts LoadOptions) (Config, error) {
	specs, err := newExtensionSpecIndex(opts.ExtensionSpecs)
	if err != nil {
		return Config{}, err
	}
	mode, err := parseMode(fileConfig.Mode)
	if err != nil {
		return Config{}, err
	}
	webSearchSupport, err := parseWebSearchSupport(fileConfig.Provider.WebSearch.Support)
	if err != nil {
		return Config{}, err
	}
	topExtensions, err := decodeExtensionSettings("config", ExtensionScopeGlobal, fileConfig.Extensions, specs)
	if err != nil {
		return Config{}, err
	}
	providerDefs, err := fromProviderDefFileConfig(fileConfig.Provider.Providers, specs)
	if err != nil {
		return Config{}, err
	}
	routes, err := buildRoutes(fileConfig.Provider.Routes, providerDefs, specs)
	if err != nil {
		return Config{}, err
	}

	legacyBaseURL := strings.TrimRight(strings.TrimSpace(fileConfig.Provider.BaseURL), "/")
	legacyAPIKey := strings.TrimSpace(fileConfig.Provider.APIKey)
	legacyVersion := valueOrDefault(strings.TrimSpace(fileConfig.Provider.Version), "2023-06-01")
	legacyUserAgent := strings.TrimSpace(fileConfig.Provider.UserAgent)

	cfg := Config{
		Mode:              mode,
		Addr:              valueOrDefault(strings.TrimSpace(fileConfig.Server.Addr), DefaultAddr),
		AuthToken:          strings.TrimSpace(fileConfig.Server.AuthToken),
		TraceRequests:     fileConfig.TraceRequests,
		LogLevel:          valueOrDefault(strings.TrimSpace(fileConfig.Log.Level), "info"),
		LogFormat:         valueOrDefault(strings.TrimSpace(fileConfig.Log.Format), "text"),
		SystemPrompt:      strings.TrimSpace(fileConfig.SystemPrompt),
		DefaultModel:      strings.TrimSpace(fileConfig.Provider.DefaultModel),
		ProviderBaseURL:   legacyBaseURL,
		ProviderAPIKey:    legacyAPIKey,
		ProviderVersion:   legacyVersion,
		ProviderUserAgent: legacyUserAgent,
		WebSearchSupport:  webSearchSupport,
		WebSearchMaxUses:  intOrDefault(fileConfig.Provider.WebSearch.MaxUses, 8),
		TavilyAPIKey:      strings.TrimSpace(fileConfig.Provider.WebSearch.TavilyAPIKey),
		FirecrawlAPIKey:   strings.TrimSpace(fileConfig.Provider.WebSearch.FirecrawlAPIKey),
		SearchMaxRounds:   intOrDefault(fileConfig.Provider.WebSearch.SearchMaxRounds, 5),
		DefaultMaxTokens:  intOrDefault(fileConfig.Provider.DefaultMaxTokens, 1024),
		Routes:            routes,
		ProviderDefs:      providerDefs,
		Cache:             fromCacheFileConfig(fileConfig.Cache),
		ResponseProxy:     FromResponseProxyFileConfig(fileConfig.Developer.Proxy.Response),
		AnthropicProxy:    FromAnthropicProxyFileConfig(fileConfig.Developer.Proxy.Anthropic),
		Plugins:           pluginsFromFileConfig(fileConfig.Plugins),
		Extensions:        topExtensions,
		extensionSpecs:    specs,
	}

	// In multi-provider mode, derive ProviderBaseURL/ProviderAPIKey from the
	// configured providers for backward-compatible lookup.
	if len(cfg.ProviderDefs) > 0 {
		if def, ok := cfg.ProviderDefs["default"]; ok && def.BaseURL != "" {
			cfg.ProviderBaseURL = def.BaseURL
			cfg.ProviderAPIKey = def.APIKey
			cfg.ProviderVersion = def.Version
		} else if len(cfg.ProviderDefs) == 1 {
			for _, def := range cfg.ProviderDefs {
				cfg.ProviderBaseURL = def.BaseURL
				cfg.ProviderAPIKey = def.APIKey
				cfg.ProviderVersion = def.Version
			}
		}
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func parseMode(value string) (Mode, error) {
	switch mode := Mode(strings.TrimSpace(value)); mode {
	case ModeCaptureAnthropic, ModeCaptureResponse, ModeTransform:
		return mode, nil
	case "":
		return "", errors.New("mode is required")
	default:
		return "", fmt.Errorf("invalid mode %q", value)
	}
}

func parseWebSearchSupport(value string) (WebSearchSupport, error) {
	switch support := WebSearchSupport(strings.TrimSpace(value)); support {
	case "":
		return WebSearchSupportAuto, nil
	case WebSearchSupportAuto, WebSearchSupportEnabled, WebSearchSupportDisabled, WebSearchSupportInjected:
		return support, nil
	default:
		return "", fmt.Errorf("invalid provider.web_search.support %q", value)
	}
}

func FromResponseProxyFileConfig(fileConfig ProxyFileConfig) ResponseProxyConfig {
	return ResponseProxyConfig{
		Model:           strings.TrimSpace(fileConfig.Model),
		ProviderBaseURL: strings.TrimRight(strings.TrimSpace(fileConfig.Provider.BaseURL), "/"),
		ProviderAPIKey:  strings.TrimSpace(fileConfig.Provider.APIKey),
	}
}

func FromAnthropicProxyFileConfig(fileConfig ProxyFileConfig) AnthropicProxyConfig {
	return AnthropicProxyConfig{
		Model:           strings.TrimSpace(fileConfig.Model),
		ProviderBaseURL: strings.TrimRight(strings.TrimSpace(fileConfig.Provider.BaseURL), "/"),
		ProviderAPIKey:  strings.TrimSpace(fileConfig.Provider.APIKey),
		ProviderVersion: valueOrDefault(strings.TrimSpace(fileConfig.Provider.Version), "2023-06-01"),
	}
}

// buildRoutes parses the "provider/model" route specs and merges model metadata
// from provider definitions.
func buildRoutes(rawRoutes map[string]RouteFileConfig, providerDefs map[string]ProviderDef, specs extensionSpecIndex) (map[string]RouteEntry, error) {
	if len(rawRoutes) == 0 {
		return nil, nil
	}
	routes := make(map[string]RouteEntry, len(rawRoutes))
	for alias, routeCfg := range rawRoutes {
		trimmedAlias := strings.TrimSpace(alias)
		if trimmedAlias == "" {
			continue
		}
		providerKey, modelName := parseRouteSpec(routeCfg.To)
		routeExtensions, err := decodeExtensionSettings("provider.routes."+trimmedAlias, ExtensionScopeRoute, routeCfg.Extensions, specs)
		if err != nil {
			return nil, err
		}
		entry := RouteEntry{
			Provider:   providerKey,
			Model:      modelName,
			Extensions: routeExtensions,
		}
		// Merge metadata from provider's model catalog if available.
		if def, ok := providerDefs[providerKey]; ok {
			if meta, ok := def.Models[modelName]; ok {
				entry.ContextWindow = meta.ContextWindow
				entry.MaxOutputTokens = meta.MaxOutputTokens
				entry.InputPrice = meta.InputPrice
				entry.OutputPrice = meta.OutputPrice
				entry.CacheWritePrice = meta.CacheWritePrice
				entry.CacheReadPrice = meta.CacheReadPrice
				entry.DisplayName = meta.DisplayName
				entry.Description = meta.Description
				entry.DefaultReasoningLevel = meta.DefaultReasoningLevel
				entry.SupportedReasoningLevels = meta.SupportedReasoningLevels
				entry.SupportsReasoningSummaries = meta.SupportsReasoningSummaries
				entry.DefaultReasoningSummary = meta.DefaultReasoningSummary
				entry.WebSearch = meta.WebSearch
			}
		}
		routes[trimmedAlias] = entry
	}
	return routes, nil
}

// parseRouteSpec splits "provider/model" into (provider, model).
// If no slash is present, the whole string is treated as the model name
// with provider defaulting to "default".
func parseRouteSpec(spec string) (string, string) {
	spec = strings.TrimSpace(spec)
	slash := strings.IndexByte(spec, '/')
	if slash < 0 {
		return "default", spec
	}
	return strings.TrimSpace(spec[:slash]), strings.TrimSpace(spec[slash+1:])
}

func fromProviderDefFileConfig(fileConfig map[string]ProviderDefFileConfig, specs extensionSpecIndex) (map[string]ProviderDef, error) {
	if len(fileConfig) == 0 {
		return nil, nil
	}
	defs := make(map[string]ProviderDef, len(fileConfig))
	for key, def := range fileConfig {
		trimmedKey := strings.TrimSpace(key)
		if trimmedKey == "" {
			continue
		}
		wsSupport, _ := parseWebSearchSupport(def.WebSearch.Support)
		providerExtensions, err := decodeExtensionSettings("provider.providers."+trimmedKey, ExtensionScopeProvider, def.Extensions, specs)
		if err != nil {
			return nil, err
		}
		models := make(map[string]ModelMeta, len(def.Models))
		for name, m := range def.Models {
			trimmedName := strings.TrimSpace(name)
			modelExtensions, err := decodeExtensionSettings("provider.providers."+trimmedKey+".models."+trimmedName, ExtensionScopeModel, m.Extensions, specs)
			if err != nil {
				return nil, err
			}
			meta := ModelMeta{
				ContextWindow:              m.ContextWindow,
				MaxOutputTokens:            m.MaxOutputTokens,
				InputPrice:                 m.Pricing.InputPrice,
				OutputPrice:                m.Pricing.OutputPrice,
				CacheWritePrice:            m.Pricing.CacheWritePrice,
				CacheReadPrice:             m.Pricing.CacheReadPrice,
				DisplayName:                strings.TrimSpace(m.DisplayName),
				Description:                strings.TrimSpace(m.Description),
				DefaultReasoningLevel:      strings.TrimSpace(m.DefaultReasoningLevel),
				SupportsReasoningSummaries: boolOrDefault(m.SupportsReasoningSummaries, false),
				DefaultReasoningSummary:    strings.TrimSpace(m.DefaultReasoningSummary),
				Extensions:                 modelExtensions,
			}
			// Parse model-level web_search config.
			if m.WebSearch.Support != "" {
				modelWS, _ := parseWebSearchSupport(m.WebSearch.Support)
				meta.WebSearch = WebSearchConfig{
					Support:         modelWS,
					MaxUses:         m.WebSearch.MaxUses,
					TavilyAPIKey:    strings.TrimSpace(m.WebSearch.TavilyAPIKey),
					FirecrawlAPIKey: strings.TrimSpace(m.WebSearch.FirecrawlAPIKey),
					SearchMaxRounds: m.WebSearch.SearchMaxRounds,
				}
			}
			for _, preset := range m.SupportedReasoningLevels {
				meta.SupportedReasoningLevels = append(meta.SupportedReasoningLevels, ReasoningLevelPreset{
					Effort:      strings.TrimSpace(preset.Effort),
					Description: strings.TrimSpace(preset.Description),
				})
			}
			models[trimmedName] = meta
		}
		pd := ProviderDef{
			BaseURL:          strings.TrimRight(strings.TrimSpace(def.BaseURL), "/"),
			APIKey:           strings.TrimSpace(def.APIKey),
			Version:          strings.TrimSpace(def.Version),
			UserAgent:        strings.TrimSpace(def.UserAgent),
			Protocol:         strings.TrimSpace(def.Protocol),
			WebSearchSupport: wsSupport,
			WebSearchMaxUses: def.WebSearch.MaxUses,
			TavilyAPIKey:     strings.TrimSpace(def.WebSearch.TavilyAPIKey),
			FirecrawlAPIKey:  strings.TrimSpace(def.WebSearch.FirecrawlAPIKey),
			SearchMaxRounds:  def.WebSearch.SearchMaxRounds,
			Extensions:       providerExtensions,
			Models:           models,
		}
		defs[trimmedKey] = pd
	}
	return defs, nil
}

func fromCacheFileConfig(fileConfig CacheFileConfig) CacheConfig {
	return CacheConfig{
		Mode:                     valueOrDefault(strings.TrimSpace(fileConfig.Mode), "automatic"),
		TTL:                      valueOrDefault(strings.TrimSpace(fileConfig.TTL), "5m"),
		PromptCaching:            boolOrDefault(fileConfig.PromptCaching, true),
		AutomaticPromptCache:     boolOrDefault(fileConfig.AutomaticPromptCache, true),
		ExplicitCacheBreakpoints: boolOrDefault(fileConfig.ExplicitCacheBreakpoints, true),
		AllowRetentionDowngrade:  boolOrDefault(fileConfig.AllowRetentionDowngrade, false),
		MaxBreakpoints:           intOrDefault(fileConfig.MaxBreakpoints, 4),
		MinCacheTokens:           intOrDefault(fileConfig.MinCacheTokens, 1024),
		ExpectedReuse:            intOrDefault(fileConfig.ExpectedReuse, 2),
		MinimumValueScore:        intOrDefault(fileConfig.MinimumValueScore, 2048),
		MinBreakpointTokens:      intOrDefault(fileConfig.MinBreakpointTokens, 1024),
	}
}

func pluginsFromFileConfig(raw map[string]any) map[string]map[string]any {
	if len(raw) == 0 {
		return nil
	}
	result := make(map[string]map[string]any, len(raw))
	for name, cfg := range raw {
		switch v := cfg.(type) {
		case map[string]any:
			result[name] = v
		default:
			// Skip non-map entries.
		}
	}
	return result
}
