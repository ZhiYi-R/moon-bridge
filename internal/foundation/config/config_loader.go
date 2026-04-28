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
	Mode          string              `yaml:"mode"`
	TraceRequests bool                `yaml:"trace_requests"`
	Log           LogFileConfig       `yaml:"log"`
	Server        ServerFileConfig    `yaml:"server"`
	Provider      ProviderFileConfig  `yaml:"provider"`
	Cache         CacheFileConfig     `yaml:"cache"`
	SystemPrompt  string              `yaml:"system_prompt"`
	Developer     DeveloperFileConfig `yaml:"developer"`
	Plugins       map[string]any      `yaml:"plugins"`
}

type ServerFileConfig struct {
	Addr string `yaml:"addr"`
}

type ProviderFileConfig struct {
	BaseURL          string                           `yaml:"base_url"`
	APIKey           string                           `yaml:"api_key"`
	Version          string                           `yaml:"version"`
	UserAgent        string                           `yaml:"user_agent"`
	WebSearch        WebSearchFileConfig              `yaml:"web_search"`
	DefaultMaxTokens int                              `yaml:"default_max_tokens"`
	DefaultModel     string                           `yaml:"default_model"`
	Providers        map[string]ProviderDefFileConfig `yaml:"providers"`
	Routes           map[string]string                `yaml:"routes"`
}

type CacheFileConfig struct {
	Mode                     string `yaml:"mode"`
	TTL                      string `yaml:"ttl"`
	PromptCaching            *bool  `yaml:"prompt_caching"`
	AutomaticPromptCache     *bool  `yaml:"automatic_prompt_cache"`
	ExplicitCacheBreakpoints *bool  `yaml:"explicit_cache_breakpoints"`
	AllowRetentionDowngrade  *bool  `yaml:"allow_retention_downgrade"`
	MaxBreakpoints           int    `yaml:"max_breakpoints"`
	MinCacheTokens           int    `yaml:"min_cache_tokens"`
	ExpectedReuse            int    `yaml:"expected_reuse"`
	MinimumValueScore        int    `yaml:"minimum_value_score"`
	MinBreakpointTokens      int    `yaml:"min_breakpoint_tokens"`
}

// ProviderModelFileConfig defines metadata for a model in a provider's catalog.
// The map key is the upstream model name.
type ProviderModelFileConfig struct {
	ContextWindow   int                    `yaml:"context_window"`
	MaxOutputTokens int                    `yaml:"max_output_tokens"`
	Pricing         ModelPricingFileConfig `yaml:"pricing"`
	// Codex model catalog metadata (injected into /v1/models responses).
	DisplayName                string                           `yaml:"display_name"`
	Description                string                           `yaml:"description"`
	DefaultReasoningLevel      string                           `yaml:"default_reasoning_level"`
	SupportedReasoningLevels   []ReasoningLevelPresetFileConfig `yaml:"supported_reasoning_levels"`
	SupportsReasoningSummaries *bool                            `yaml:"supports_reasoning_summaries"`
	DefaultReasoningSummary    string                           `yaml:"default_reasoning_summary"`
	WebSearch                  WebSearchFileConfig              `yaml:"web_search"`
	DeepSeekV4                 bool                             `yaml:"deepseek_v4"`
}

type ProviderDefFileConfig struct {
	BaseURL   string                             `yaml:"base_url"`
	APIKey    string                             `yaml:"api_key"`
	Version   string                             `yaml:"version"`
	UserAgent string                             `yaml:"user_agent"`
	Protocol  string                             `yaml:"protocol"`
	WebSearch WebSearchFileConfig                `yaml:"web_search"`
	Models    map[string]ProviderModelFileConfig `yaml:"models"`
}

type ModelPricingFileConfig struct {
	InputPrice      float64 `yaml:"input_price"`
	OutputPrice     float64 `yaml:"output_price"`
	CacheWritePrice float64 `yaml:"cache_write_price"`
	CacheReadPrice  float64 `yaml:"cache_read_price"`
}

// ReasoningLevelPresetFileConfig maps to Codex's ReasoningEffortPreset.
type ReasoningLevelPresetFileConfig struct {
	Effort      string `yaml:"effort"`
	Description string `yaml:"description"`
}

type WebSearchFileConfig struct {
	Support         string `yaml:"support"`
	MaxUses         int    `yaml:"max_uses"`
	TavilyAPIKey    string `yaml:"tavily_api_key"`
	FirecrawlAPIKey string `yaml:"firecrawl_api_key"`
	SearchMaxRounds int    `yaml:"search_max_rounds"`
}

type DeveloperFileConfig struct {
	Proxy DeveloperProxyFileConfig `yaml:"proxy"`
}

type DeveloperProxyFileConfig struct {
	Response  ProxyFileConfig `yaml:"response"`
	Anthropic ProxyFileConfig `yaml:"anthropic"`
}

type ProxyFileConfig struct {
	Model    string                  `yaml:"model"`
	Provider ProxyProviderFileConfig `yaml:"provider"`
}

type ProxyProviderFileConfig struct {
	BaseURL string `yaml:"base_url"`
	APIKey  string `yaml:"api_key"`
	Version string `yaml:"version"`
}

type LogFileConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

func LoadFromFile(path string) (Config, error) {
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
	return FromFileConfig(fileConfig)
}

func LoadFromYAML(data []byte) (Config, error) {
	fileConfig, err := decodeFileConfig(data)
	if err != nil {
		return Config{}, err
	}
	return FromFileConfig(fileConfig)
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
	for _, entry := range entries {
		if entry.IsDir() || !isYAMLFile(entry.Name()) {
			continue
		}
		pluginName := strings.TrimSuffix(strings.TrimSuffix(entry.Name(), ".yaml"), ".yml")
		if strings.TrimSpace(pluginName) == "" {
			continue
		}
		pluginPath := filepath.Join(pluginDir, entry.Name())
		raw, err := os.ReadFile(pluginPath)
		if err != nil {
			return fmt.Errorf("read plugin config %s: %w", pluginPath, err)
		}
		pluginConfig, err := decodePluginConfig(raw)
		if err != nil {
			return fmt.Errorf("parse plugin config %s: %w", pluginPath, err)
		}
		mergePluginConfig(fileConfig, pluginName, pluginConfig)
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
	mode, err := parseMode(fileConfig.Mode)
	if err != nil {
		return Config{}, err
	}
	webSearchSupport, err := parseWebSearchSupport(fileConfig.Provider.WebSearch.Support)
	if err != nil {
		return Config{}, err
	}
	providerDefs := fromProviderDefFileConfig(fileConfig.Provider.Providers)
	routes := buildRoutes(fileConfig.Provider.Routes, providerDefs)

	legacyBaseURL := strings.TrimRight(strings.TrimSpace(fileConfig.Provider.BaseURL), "/")
	legacyAPIKey := strings.TrimSpace(fileConfig.Provider.APIKey)
	legacyVersion := valueOrDefault(strings.TrimSpace(fileConfig.Provider.Version), "2023-06-01")
	legacyUserAgent := strings.TrimSpace(fileConfig.Provider.UserAgent)

	cfg := Config{
		Mode:              mode,
		Addr:              valueOrDefault(strings.TrimSpace(fileConfig.Server.Addr), DefaultAddr),
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

// buildRoutes parses the "provider/model" route strings and merges model metadata
// from provider definitions.
func buildRoutes(rawRoutes map[string]string, providerDefs map[string]ProviderDef) map[string]RouteEntry {
	if len(rawRoutes) == 0 {
		return nil
	}
	routes := make(map[string]RouteEntry, len(rawRoutes))
	for alias, spec := range rawRoutes {
		trimmedAlias := strings.TrimSpace(alias)
		if trimmedAlias == "" {
			continue
		}
		providerKey, modelName := parseRouteSpec(spec)
		entry := RouteEntry{
			Provider: providerKey,
			Model:    modelName,
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
				entry.DeepSeekV4 = meta.DeepSeekV4
			}
		}
		routes[trimmedAlias] = entry
	}
	return routes
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

func fromProviderDefFileConfig(fileConfig map[string]ProviderDefFileConfig) map[string]ProviderDef {
	if len(fileConfig) == 0 {
		return nil
	}
	defs := make(map[string]ProviderDef, len(fileConfig))
	for key, def := range fileConfig {
		trimmedKey := strings.TrimSpace(key)
		if trimmedKey == "" {
			continue
		}
		wsSupport, _ := parseWebSearchSupport(def.WebSearch.Support)
		models := make(map[string]ModelMeta, len(def.Models))
		for name, m := range def.Models {
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
			meta.DeepSeekV4 = m.DeepSeekV4
			for _, preset := range m.SupportedReasoningLevels {
				meta.SupportedReasoningLevels = append(meta.SupportedReasoningLevels, ReasoningLevelPreset{
					Effort:      strings.TrimSpace(preset.Effort),
					Description: strings.TrimSpace(preset.Description),
				})
			}
			models[strings.TrimSpace(name)] = meta
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
			Models:           models,
		}
		defs[trimmedKey] = pd
	}
	return defs
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
