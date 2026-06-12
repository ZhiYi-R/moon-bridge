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

// ---- FileConfig types ----

type FileConfig struct {
	Mode        string                           `yaml:"mode" json:"mode"`
	Trace       TraceFileConfig                  `yaml:"trace,omitempty" json:"trace,omitempty"`
	Log         LogFileConfig                    `yaml:"log,omitempty" json:"log,omitempty"`
	Server      ServerFileConfig                 `yaml:"server,omitempty" json:"server,omitempty"`
	Defaults    DefaultsFileConfig               `yaml:"defaults,omitempty" json:"defaults,omitempty"`
	Models      map[string]ModelDefFileConfig    `yaml:"models,omitempty" json:"models,omitempty"`
	Providers   map[string]ProviderDefFileConfig `yaml:"providers,omitempty" json:"providers,omitempty"`
	Routes      map[string]RouteFileConfig       `yaml:"routes,omitempty" json:"routes,omitempty"`
	WebSearch   WebSearchFileConfig              `yaml:"web_search,omitempty" json:"web_search,omitempty"`
	Cache       CacheFileConfig                  `yaml:"cache,omitempty" json:"cache,omitempty"`
	Persistence PersistenceFileConfig            `yaml:"persistence,omitempty" json:"persistence,omitempty"`
	Extensions  map[string]ExtensionFileConfig   `yaml:"extensions,omitempty" json:"extensions,omitempty"`
	Proxy       ProxyFileConfig                  `yaml:"proxy,omitempty" json:"proxy,omitempty"`
}

// UnmarshalYAML supports the old trace_requests field for backward compatibility.
func (fc *FileConfig) UnmarshalYAML(value *yaml.Node) error {
	// Re-encode the node and decode with KnownFields to preserve strict checking.
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(value); err != nil {
		return err
	}
	enc.Close()
	strictDec := yaml.NewDecoder(&buf)
	strictDec.KnownFields(true)

	type rawFileConfig struct {
		Mode          string                           `yaml:"mode"`
		Trace         TraceFileConfig                  `yaml:"trace,omitempty"`
		TraceRequests *bool                            `yaml:"trace_requests,omitempty"`
		Log           LogFileConfig                    `yaml:"log,omitempty"`
		Server        ServerFileConfig                 `yaml:"server,omitempty"`
		Defaults      DefaultsFileConfig               `yaml:"defaults,omitempty"`
		Models        map[string]ModelDefFileConfig    `yaml:"models,omitempty"`
		Providers     map[string]ProviderDefFileConfig `yaml:"providers,omitempty"`
		Routes        map[string]RouteFileConfig       `yaml:"routes,omitempty"`
		WebSearch     WebSearchFileConfig              `yaml:"web_search,omitempty"`
		Cache         CacheFileConfig                  `yaml:"cache,omitempty"`
		Persistence   PersistenceFileConfig            `yaml:"persistence,omitempty"`
		Extensions    map[string]ExtensionFileConfig   `yaml:"extensions,omitempty"`
		Proxy         ProxyFileConfig                  `yaml:"proxy,omitempty"`
	}
	var raw rawFileConfig
	if err := strictDec.Decode(&raw); err != nil {
		return err
	}
	*fc = FileConfig{
		Mode:        raw.Mode,
		Trace:       raw.Trace,
		Log:         raw.Log,
		Server:      raw.Server,
		Defaults:    raw.Defaults,
		Models:      raw.Models,
		Providers:   raw.Providers,
		Routes:      raw.Routes,
		WebSearch:   raw.WebSearch,
		Cache:       raw.Cache,
		Persistence: raw.Persistence,
		Extensions:  raw.Extensions,
		Proxy:       raw.Proxy,
	}
	if raw.TraceRequests != nil && *raw.TraceRequests {
		fc.Trace.Enabled = true
	}
	return nil
}

type TraceFileConfig struct {
	Enabled bool `yaml:"enabled" json:"enabled"`
}

type ServerFileConfig struct {
	Addr        string `yaml:"addr" json:"addr,omitempty"`
	AuthToken   string `yaml:"auth_token" json:"auth_token,omitempty"`
	MaxSessions int    `yaml:"max_sessions"`
	SessionTTL  string `yaml:"session_ttl"`
}

type LogFileConfig struct {
	Level  string `yaml:"level" json:"level,omitempty"`
	Format string `yaml:"format" json:"format,omitempty"`
}

type DefaultsFileConfig struct {
	Model        string `yaml:"model" json:"model"`
	MaxTokens    int    `yaml:"max_tokens" json:"max_tokens,omitempty"`
	SystemPrompt string `yaml:"system_prompt" json:"system_prompt,omitempty"`
}

type ModelDefFileConfig struct {
	ContextWindow               int                              `yaml:"context_window,omitempty" json:"context_window,omitempty"`
	MaxOutputTokens             int                              `yaml:"max_output_tokens,omitempty" json:"max_output_tokens,omitempty"`
	DisplayName                 string                           `yaml:"display_name,omitempty" json:"display_name,omitempty"`
	Description                 string                           `yaml:"description,omitempty" json:"description,omitempty"`
	BaseInstructions            string                           `yaml:"base_instructions,omitempty" json:"base_instructions,omitempty"`
	DefaultReasoningLevel       string                           `yaml:"default_reasoning_level,omitempty" json:"default_reasoning_level,omitempty"`
	SupportedReasoningLevels    []ReasoningLevelPresetFileConfig `yaml:"supported_reasoning_levels,omitempty" json:"supported_reasoning_levels,omitempty"`
	SupportsReasoningSummaries  *bool                            `yaml:"supports_reasoning_summaries,omitempty" json:"supports_reasoning_summaries,omitempty"`
	DefaultReasoningSummary     string                           `yaml:"default_reasoning_summary,omitempty" json:"default_reasoning_summary,omitempty"`
	InputModalities             []string                         `yaml:"input_modalities,omitempty" json:"input_modalities,omitempty"`
	SupportsImageDetailOriginal *bool                            `yaml:"supports_image_detail_original,omitempty" json:"supports_image_detail_original,omitempty"`
	WebSearch                   WebSearchFileConfig              `yaml:"web_search,omitempty" json:"web_search,omitempty"`
	Extensions                  map[string]ExtensionFileConfig   `yaml:"extensions,omitempty" json:"extensions,omitempty"`
}

type OfferFileConfig struct {
	Model        string                 `yaml:"model" json:"model"`
	UpstreamName string                 `yaml:"upstream_name,omitempty" json:"upstream_name,omitempty"`
	Priority     int                    `yaml:"priority,omitempty" json:"priority,omitempty"`
	Pricing      ModelPricingFileConfig `yaml:"pricing,omitempty" json:"pricing,omitempty"`
	Overrides    *ModelDefFileConfig    `yaml:"overrides,omitempty" json:"overrides,omitempty"`
}

type ProviderDefFileConfig struct {
	BaseURL    string                         `yaml:"base_url" json:"base_url"`
	APIKey     string                         `yaml:"api_key" json:"api_key"`
	Version    string                         `yaml:"version,omitempty" json:"version,omitempty"`
	UserAgent  string                         `yaml:"user_agent,omitempty" json:"user_agent,omitempty"`
	Protocol   string                         `yaml:"protocol,omitempty" json:"protocol,omitempty"`
	WebSearch  WebSearchFileConfig            `yaml:"web_search,omitempty" json:"web_search,omitempty"`
	Extensions map[string]ExtensionFileConfig `yaml:"extensions,omitempty" json:"extensions,omitempty"`
	Offers     []OfferFileConfig              `yaml:"offers,omitempty" json:"offers,omitempty"`
}

type RouteFileConfig struct {
	To            string                         `yaml:"to,omitempty" json:"to,omitempty"` // backward compat "provider/model"
	Model         string                         `yaml:"model,omitempty" json:"model,omitempty"`
	Provider      string                         `yaml:"provider,omitempty" json:"provider,omitempty"`
	DisplayName   string                         `yaml:"display_name,omitempty" json:"display_name,omitempty"`
	Description   string                         `yaml:"description,omitempty" json:"description,omitempty"`
	ContextWindow int                            `yaml:"context_window,omitempty" json:"context_window,omitempty"`
	WebSearch     WebSearchFileConfig            `yaml:"web_search,omitempty" json:"web_search,omitempty"`
	Extensions    map[string]ExtensionFileConfig `yaml:"extensions,omitempty" json:"extensions,omitempty"`
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
		// If To is set and Model/Provider are empty, parse To.
		if cfg.To != "" && cfg.Model == "" {
			provider, model := parseRouteSpec(cfg.To)
			cfg.Provider = provider
			cfg.Model = model
		}
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

type PersistenceFileConfig struct {
	ActiveProvider string `yaml:"active_provider" json:"active_provider,omitempty"`
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

type ProxyFileConfig struct {
	Response  ProxyTargetFileConfig `yaml:"response,omitempty" json:"response,omitempty"`
	Anthropic ProxyTargetFileConfig `yaml:"anthropic,omitempty" json:"anthropic,omitempty"`
}

type ProxyTargetFileConfig struct {
	BaseURL string `yaml:"base_url" json:"base_url"`
	APIKey  string `yaml:"api_key" json:"api_key"`
	Model   string `yaml:"model,omitempty" json:"model,omitempty"`
	Version string `yaml:"version,omitempty" json:"version,omitempty"`
}

// ---- Loading functions ----

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
	cfg, err := FromFileConfigWithOptions(fileConfig, opts)
	if err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// LoadFromYAML parses YAML bytes into a Config.
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

	// Trace: prefer new trace.enabled, fall back to old trace_requests.
	traceEnabled := fileConfig.Trace.Enabled

	// Web search: top-level or default.
	webSearchSupport, err := parseWebSearchSupport(fileConfig.WebSearch.Support)
	if err != nil {
		return Config{}, err
	}

	// Defaults.
	defaults := Defaults{
		Model:        strings.TrimSpace(fileConfig.Defaults.Model),
		MaxTokens:    fileConfig.Defaults.MaxTokens,
		SystemPrompt: strings.TrimSpace(fileConfig.Defaults.SystemPrompt),
	}

	// Models (top-level).
	models, err := fromModelDefFileConfig(fileConfig.Models, specs)
	if err != nil {
		return Config{}, err
	}

	// Top-level extensions.
	topExtensions, err := decodeExtensionSettings("config", ExtensionScopeGlobal, fileConfig.Extensions, specs)
	if err != nil {
		return Config{}, err
	}

	// Providers (top-level, no longer under provider.providers).
	providerDefs, err := fromProviderDefFileConfig(fileConfig.Providers, specs, models)
	if err != nil {
		return Config{}, err
	}

	// Routes.
	routes, err := buildRoutes(fileConfig.Routes, providerDefs, models, specs)
	if err != nil {
		return Config{}, err
	}

	// Proxy (flattened, replaces developer.proxy).
	responseProxy := FromResponseProxyFileConfig(fileConfig.Proxy.Response)
	anthropicProxy := FromAnthropicProxyFileConfig(fileConfig.Proxy.Anthropic)

	cfg := Config{
		Mode:             mode,
		Addr:             valueOrDefault(strings.TrimSpace(fileConfig.Server.Addr), DefaultAddr),
		AuthToken:        strings.TrimSpace(fileConfig.Server.AuthToken),
		MaxSessions:      intOrDefault(fileConfig.Server.MaxSessions, 0),
		SessionTTL:       valueOrDefault(strings.TrimSpace(fileConfig.Server.SessionTTL), "24h"),
		TraceRequests:    traceEnabled,
		LogLevel:         valueOrDefault(strings.TrimSpace(fileConfig.Log.Level), "info"),
		LogFormat:        valueOrDefault(strings.TrimSpace(fileConfig.Log.Format), "text"),
		SystemPrompt:     defaults.SystemPrompt,
		DefaultModel:     defaults.Model,
		Defaults:         defaults,
		Models:           models,
		Routes:           routes,
		ProviderDefs:     providerDefs,
		WebSearchSupport: webSearchSupport,
		WebSearchMaxUses: intOrDefault(fileConfig.WebSearch.MaxUses, 8),
		TavilyAPIKey:     strings.TrimSpace(fileConfig.WebSearch.TavilyAPIKey),
		FirecrawlAPIKey:  strings.TrimSpace(fileConfig.WebSearch.FirecrawlAPIKey),
		SearchMaxRounds:  intOrDefault(fileConfig.WebSearch.SearchMaxRounds, 5),
		DefaultMaxTokens: intOrDefault(defaults.MaxTokens, 1024),
		Cache:            fromCacheFileConfig(fileConfig.Cache),
		Persistence:      FromPersistenceFileConfig(fileConfig.Persistence),
		ResponseProxy:    responseProxy,
		AnthropicProxy:   anthropicProxy,
		Extensions:       topExtensions,
		extensionSpecs:   specs,
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// ---- Conversion helpers ----

func fromModelDefFileConfig(fileConfig map[string]ModelDefFileConfig, specs extensionSpecIndex) (map[string]ModelDef, error) {
	if len(fileConfig) == 0 {
		return nil, nil
	}
	models := make(map[string]ModelDef, len(fileConfig))
	for slug, m := range fileConfig {
		trimmedSlug := strings.TrimSpace(slug)
		if trimmedSlug == "" {
			continue
		}
		modelExtensions, err := decodeExtensionSettings("models."+trimmedSlug, ExtensionScopeModel, m.Extensions, specs)
		if err != nil {
			return nil, err
		}
		ws := WebSearchConfig{}
		if m.WebSearch.Support != "" {
			wsSupport, _ := parseWebSearchSupport(m.WebSearch.Support)
			ws = WebSearchConfig{
				Support:         wsSupport,
				MaxUses:         m.WebSearch.MaxUses,
				TavilyAPIKey:    strings.TrimSpace(m.WebSearch.TavilyAPIKey),
				FirecrawlAPIKey: strings.TrimSpace(m.WebSearch.FirecrawlAPIKey),
				SearchMaxRounds: m.WebSearch.SearchMaxRounds,
			}
		}
		var reasoningPresets []ReasoningLevelPreset
		for _, p := range m.SupportedReasoningLevels {
			reasoningPresets = append(reasoningPresets, ReasoningLevelPreset{
				Effort:      strings.TrimSpace(p.Effort),
				Description: strings.TrimSpace(p.Description),
			})
		}
		models[trimmedSlug] = ModelDef{
			ContextWindow:               m.ContextWindow,
			MaxOutputTokens:             m.MaxOutputTokens,
			DisplayName:                 strings.TrimSpace(m.DisplayName),
			Description:                 strings.TrimSpace(m.Description),
			BaseInstructions:            strings.TrimSpace(m.BaseInstructions),
			DefaultReasoningLevel:       strings.TrimSpace(m.DefaultReasoningLevel),
			SupportedReasoningLevels:    reasoningPresets,
			SupportsReasoningSummaries:  boolOrDefault(m.SupportsReasoningSummaries, false),
			DefaultReasoningSummary:     strings.TrimSpace(m.DefaultReasoningSummary),
			InputModalities:             m.InputModalities,
			SupportsImageDetailOriginal: boolOrDefault(m.SupportsImageDetailOriginal, false),
			WebSearch:                   ws,
			Extensions:                  modelExtensions,
		}
	}
	return models, nil
}

func fromProviderDefFileConfig(fileConfig map[string]ProviderDefFileConfig, specs extensionSpecIndex, models map[string]ModelDef) (map[string]ProviderDef, error) {
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
		providerExtensions, err := decodeExtensionSettings("providers."+trimmedKey, ExtensionScopeProvider, def.Extensions, specs)
		if err != nil {
			return nil, err
		}

		// Build Offers.
		offers := make([]OfferEntry, 0, len(def.Offers))
		for _, offer := range def.Offers {
			trimmedModel := strings.TrimSpace(offer.Model)
			if trimmedModel == "" {
				continue
			}
			entry := OfferEntry{
				Model:        trimmedModel,
				UpstreamName: strings.TrimSpace(offer.UpstreamName),
				Priority:     offer.Priority,
				Pricing: ModelPricing{
					InputPrice:      offer.Pricing.InputPrice,
					OutputPrice:     offer.Pricing.OutputPrice,
					CacheWritePrice: offer.Pricing.CacheWritePrice,
					CacheReadPrice:  offer.Pricing.CacheReadPrice,
				},
			}
			// Handle overrides: merge model def with overrides.
			if offer.Overrides != nil {
				base := ModelDef{}
				if m, ok := models[trimmedModel]; ok {
					base = m
				}
				merged := mergeModelDefOverrides(base, *offer.Overrides)
				entry.Overrides = &merged
			}
			offers = append(offers, entry)
		}

		// Build ProviderDef.Models from offers + model defs (backward compat).
		providerModels := make(map[string]ModelMeta, len(offers))
		for _, offer := range offers {
			upstreamName := offer.UpstreamName
			if upstreamName == "" {
				upstreamName = offer.Model
			}
			modelDef, ok := models[offer.Model]
			if !ok {
				// Model not defined in top-level models; skip metadata.
				providerModels[upstreamName] = ModelMeta{
					InputPrice:      offer.Pricing.InputPrice,
					OutputPrice:     offer.Pricing.OutputPrice,
					CacheWritePrice: offer.Pricing.CacheWritePrice,
					CacheReadPrice:  offer.Pricing.CacheReadPrice,
				}
				continue
			}
			meta := ModelMeta{
				ContextWindow:               modelDef.ContextWindow,
				MaxOutputTokens:             modelDef.MaxOutputTokens,
				InputPrice:                  offer.Pricing.InputPrice,
				OutputPrice:                 offer.Pricing.OutputPrice,
				CacheWritePrice:             offer.Pricing.CacheWritePrice,
				CacheReadPrice:              offer.Pricing.CacheReadPrice,
				DisplayName:                 modelDef.DisplayName,
				Description:                 modelDef.Description,
				BaseInstructions:            modelDef.BaseInstructions,
				DefaultReasoningLevel:       modelDef.DefaultReasoningLevel,
				SupportedReasoningLevels:    modelDef.SupportedReasoningLevels,
				SupportsReasoningSummaries:  modelDef.SupportsReasoningSummaries,
				DefaultReasoningSummary:     modelDef.DefaultReasoningSummary,
				InputModalities:             modelDef.InputModalities,
				SupportsImageDetailOriginal: modelDef.SupportsImageDetailOriginal,
				WebSearch:                   modelDef.WebSearch,
				Extensions:                  modelDef.Extensions,
			}
			// Apply offer overrides.
			if offer.Overrides != nil {
				applyModelOverrides(&meta, *offer.Overrides)
			}
			providerModels[upstreamName] = meta
		}

		pd := ProviderDef{
			BaseURL:          strings.TrimRight(strings.TrimSpace(def.BaseURL), "/"),
			APIKey:           strings.TrimSpace(def.APIKey),
			Version:          valueOrDefault(strings.TrimSpace(def.Version), "2023-06-01"),
			UserAgent:        strings.TrimSpace(def.UserAgent),
			Protocol:         strings.TrimSpace(def.Protocol),
			WebSearchSupport: wsSupport,
			WebSearchMaxUses: def.WebSearch.MaxUses,
			TavilyAPIKey:     strings.TrimSpace(def.WebSearch.TavilyAPIKey),
			FirecrawlAPIKey:  strings.TrimSpace(def.WebSearch.FirecrawlAPIKey),
			SearchMaxRounds:  def.WebSearch.SearchMaxRounds,
			Extensions:       providerExtensions,
			Models:           providerModels,
			Offers:           offers,
		}
		defs[trimmedKey] = pd
	}
	return defs, nil
}

// mergeModelDefOverrides applies ModelDefFileConfig overrides on top of a base ModelDef.
func mergeModelDefOverrides(base ModelDef, override ModelDefFileConfig) ModelDef {
	out := base
	if override.ContextWindow > 0 {
		out.ContextWindow = override.ContextWindow
	}
	if override.MaxOutputTokens > 0 {
		out.MaxOutputTokens = override.MaxOutputTokens
	}
	if v := strings.TrimSpace(override.DisplayName); v != "" {
		out.DisplayName = v
	}
	if v := strings.TrimSpace(override.Description); v != "" {
		out.Description = v
	}
	if v := strings.TrimSpace(override.BaseInstructions); v != "" {
		out.BaseInstructions = v
	}
	if v := strings.TrimSpace(override.DefaultReasoningLevel); v != "" {
		out.DefaultReasoningLevel = v
	}
	if len(override.SupportedReasoningLevels) > 0 {
		var presets []ReasoningLevelPreset
		for _, p := range override.SupportedReasoningLevels {
			presets = append(presets, ReasoningLevelPreset{
				Effort:      strings.TrimSpace(p.Effort),
				Description: strings.TrimSpace(p.Description),
			})
		}
		out.SupportedReasoningLevels = presets
	}
	if override.SupportsReasoningSummaries != nil {
		out.SupportsReasoningSummaries = *override.SupportsReasoningSummaries
	}
	if v := strings.TrimSpace(override.DefaultReasoningSummary); v != "" {
		out.DefaultReasoningSummary = v
	}
	if override.InputModalities != nil {
		out.InputModalities = override.InputModalities
	}
	if override.SupportsImageDetailOriginal != nil {
		out.SupportsImageDetailOriginal = *override.SupportsImageDetailOriginal
	}
	if override.WebSearch.Support != "" {
		wsSupport, _ := parseWebSearchSupport(override.WebSearch.Support)
		out.WebSearch = WebSearchConfig{
			Support:         wsSupport,
			MaxUses:         override.WebSearch.MaxUses,
			TavilyAPIKey:    strings.TrimSpace(override.WebSearch.TavilyAPIKey),
			FirecrawlAPIKey: strings.TrimSpace(override.WebSearch.FirecrawlAPIKey),
			SearchMaxRounds: override.WebSearch.SearchMaxRounds,
		}
	}
	if override.Extensions != nil {
		if out.Extensions == nil {
			out.Extensions = make(map[string]ExtensionSettings)
		}
		for k, v := range override.Extensions {
			enabled := v.Enabled
			out.Extensions[k] = ExtensionSettings{
				Enabled:   enabled,
				RawConfig: cloneAnyMap(v.Config),
			}
		}
	}
	return out
}

// applyModelOverrides applies ModelDef overrides to a ModelMeta.
func applyModelOverrides(meta *ModelMeta, override ModelDef) {
	if override.ContextWindow > 0 {
		meta.ContextWindow = override.ContextWindow
	}
	if override.MaxOutputTokens > 0 {
		meta.MaxOutputTokens = override.MaxOutputTokens
	}
	if v := strings.TrimSpace(override.DisplayName); v != "" {
		meta.DisplayName = v
	}
	if v := strings.TrimSpace(override.Description); v != "" {
		meta.Description = v
	}
	if v := strings.TrimSpace(override.BaseInstructions); v != "" {
		meta.BaseInstructions = v
	}
	if v := strings.TrimSpace(override.DefaultReasoningLevel); v != "" {
		meta.DefaultReasoningLevel = v
	}
	if len(override.SupportedReasoningLevels) > 0 {
		meta.SupportedReasoningLevels = override.SupportedReasoningLevels
	}
	// Note: for bool fields we only override when true because ModelDef uses
	// plain bool (not *bool), so we can't distinguish "unset" from "false".
	if override.SupportsReasoningSummaries {
		meta.SupportsReasoningSummaries = true
	}
	if v := strings.TrimSpace(override.DefaultReasoningSummary); v != "" {
		meta.DefaultReasoningSummary = v
	}
	if len(override.InputModalities) > 0 {
		meta.InputModalities = override.InputModalities
	}
	if override.SupportsImageDetailOriginal {
		meta.SupportsImageDetailOriginal = true
	}
}

// buildRoutes parses route specs and merges model metadata.
func buildRoutes(rawRoutes map[string]RouteFileConfig, providerDefs map[string]ProviderDef, models map[string]ModelDef, specs extensionSpecIndex) (map[string]RouteEntry, error) {
	if len(rawRoutes) == 0 {
		return nil, nil
	}
	routes := make(map[string]RouteEntry, len(rawRoutes))
	for alias, routeCfg := range rawRoutes {
		trimmedAlias := strings.TrimSpace(alias)
		if trimmedAlias == "" {
			continue
		}

		// Resolve model slug and provider key.
		modelSlug := strings.TrimSpace(routeCfg.Model)
		providerKey := strings.TrimSpace(routeCfg.Provider)

		// Backward compat: if To is set and model is empty, parse it.
		if modelSlug == "" && routeCfg.To != "" {
			providerKey, modelSlug = parseRouteSpec(routeCfg.To)
		}
		if modelSlug == "" {
			return nil, fmt.Errorf("routes.%s: model is required", trimmedAlias)
		}

		// If no provider specified, find the first provider that offers this model.
		if providerKey == "" {
			for pk, def := range providerDefs {
				for _, offer := range def.Offers {
					if offer.Model == modelSlug {
						providerKey = pk
						break
					}
				}
				if providerKey != "" {
					break
				}
			}
		}

		routeExtensions, err := decodeExtensionSettings("routes."+trimmedAlias, ExtensionScopeRoute, routeCfg.Extensions, specs)
		if err != nil {
			return nil, err
		}

		entry := RouteEntry{
			Provider:   providerKey,
			Model:      modelSlug, // will be overridden with upstream name below
			Extensions: routeExtensions,
		}

		// Look up upstream model name from provider offer.
		if providerKey != "" {
			if def, ok := providerDefs[providerKey]; ok {
				for _, offer := range def.Offers {
					if offer.Model == modelSlug {
						if offer.UpstreamName != "" {
							entry.Model = offer.UpstreamName
						}
						entry.InputPrice = offer.Pricing.InputPrice
						entry.OutputPrice = offer.Pricing.OutputPrice
						entry.CacheWritePrice = offer.Pricing.CacheWritePrice
						entry.CacheReadPrice = offer.Pricing.CacheReadPrice
						break
					}
				}
			}
		}

		// Merge model def metadata into route entry.
		if modelDef, ok := models[modelSlug]; ok {
			entry.ContextWindow = modelDef.ContextWindow
			entry.MaxOutputTokens = modelDef.MaxOutputTokens
			_ = modelDef.DisplayName // route DisplayName comes from explicit config, not model def
			entry.Description = modelDef.Description
			entry.BaseInstructions = modelDef.BaseInstructions
			entry.DefaultReasoningLevel = modelDef.DefaultReasoningLevel
			entry.SupportedReasoningLevels = modelDef.SupportedReasoningLevels
			entry.SupportsReasoningSummaries = modelDef.SupportsReasoningSummaries
			entry.DefaultReasoningSummary = modelDef.DefaultReasoningSummary
			entry.InputModalities = modelDef.InputModalities
			entry.SupportsImageDetailOriginal = modelDef.SupportsImageDetailOriginal
			entry.WebSearch = modelDef.WebSearch
		}

		// Route-level overrides.
		if routeCfg.DisplayName != "" {
			entry.DisplayName = strings.TrimSpace(routeCfg.DisplayName)
		}
		if routeCfg.Description != "" {
			entry.Description = strings.TrimSpace(routeCfg.Description)
		}
		if routeCfg.ContextWindow > 0 {
			entry.ContextWindow = routeCfg.ContextWindow
		}
		if routeCfg.WebSearch.Support != "" {
			wsSupport, _ := parseWebSearchSupport(routeCfg.WebSearch.Support)
			entry.WebSearch = WebSearchConfig{
				Support:         wsSupport,
				MaxUses:         routeCfg.WebSearch.MaxUses,
				TavilyAPIKey:    strings.TrimSpace(routeCfg.WebSearch.TavilyAPIKey),
				FirecrawlAPIKey: strings.TrimSpace(routeCfg.WebSearch.FirecrawlAPIKey),
				SearchMaxRounds: routeCfg.WebSearch.SearchMaxRounds,
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
		return "", fmt.Errorf("invalid web_search.support %q", value)
	}
}

func FromResponseProxyFileConfig(fileConfig ProxyTargetFileConfig) ResponseProxyConfig {
	return ResponseProxyConfig{
		Model:           strings.TrimSpace(fileConfig.Model),
		ProviderBaseURL: strings.TrimRight(strings.TrimSpace(fileConfig.BaseURL), "/"),
		ProviderAPIKey:  strings.TrimSpace(fileConfig.APIKey),
	}
}

func FromAnthropicProxyFileConfig(fileConfig ProxyTargetFileConfig) AnthropicProxyConfig {
	return AnthropicProxyConfig{
		Model:           strings.TrimSpace(fileConfig.Model),
		ProviderBaseURL: strings.TrimRight(strings.TrimSpace(fileConfig.BaseURL), "/"),
		ProviderAPIKey:  strings.TrimSpace(fileConfig.APIKey),
		ProviderVersion: valueOrDefault(strings.TrimSpace(fileConfig.Version), "2023-06-01"),
	}
}

func FromPersistenceFileConfig(fileConfig PersistenceFileConfig) PersistenceConfig {
	return PersistenceConfig{
		ActiveProvider: strings.TrimSpace(fileConfig.ActiveProvider),
	}
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
