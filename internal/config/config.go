package config

import (
	"errors"
	"fmt"
	"moonbridge/internal/modelref"
	"strings"
)

const (
	DefaultConfigFileName = "config.yml"
	// DefaultConfigPath is kept for callers that need the config file name.
	// Use XDGDefaultConfigPath to resolve the CLI's default config location.
	DefaultConfigPath = DefaultConfigFileName
	AppConfigDirName  = "moonbridge"
	DefaultAddr       = "127.0.0.1:38440"
)

const (
	ProtocolAnthropic      = "anthropic"
	ProtocolOpenAIResponse = "openai-response"
	// Phase 5: New protocol constants (D-08)
	ProtocolGoogleGenAI = "google-genai"
	ProtocolOpenAIChat  = "openai-chat"
)

type Mode string

const (
	ModeCaptureAnthropic Mode = "CaptureAnthropic"
	ModeCaptureResponse  Mode = "CaptureResponse"
	ModeTransform        Mode = "Transform"
)

type WebSearchSupport string

const (
	WebSearchSupportAuto     WebSearchSupport = "auto"
	WebSearchSupportEnabled  WebSearchSupport = "enabled"
	WebSearchSupportDisabled WebSearchSupport = "disabled"
	WebSearchSupportInjected WebSearchSupport = "injected"
)

// WebSearchConfig holds web search settings that can be attached to a model or route.
type WebSearchConfig struct {
	Support         WebSearchSupport
	MaxUses         int
	TavilyAPIKey    string
	FirecrawlAPIKey string
	SearchMaxRounds int
	Extra           map[string]any
}

type Config struct {
	Mode             Mode
	Addr             string
	AuthToken        string
	TraceRequests    bool
	LogLevel         string
	LogFormat        string
	SystemPrompt     string
	DefaultModel     string
	WebSearchSupport WebSearchSupport
	WebSearchMaxUses int
	TavilyAPIKey     string
	FirecrawlAPIKey  string
	SearchMaxRounds  int
	WebSearchExtra   map[string]any
	DefaultMaxTokens int
	MaxSessions      int    `yaml:"max_sessions"` // 0 = unlimited
	SessionTTL       string `yaml:"session_ttl"`  // default "24h"
	// Defaults holds the default configuration values.
	Defaults Defaults
	// Models is the canonical model definition map (shared across providers).
	Models map[string]ModelDef
	// Routes maps a model alias to "provider/upstream_model".
	Routes         map[string]RouteEntry
	ProviderDefs   map[string]ProviderDef
	Cache          CacheConfig
	Persistence    PersistenceConfig
	ResponseProxy  ResponseProxyConfig
	AnthropicProxy AnthropicProxyConfig
	EgressProxy    string
	Extensions     map[string]ExtensionSettings

	extensionSpecs extensionSpecIndex
}

// RouteEntry is a resolved route: alias -> provider key + upstream model name + metadata.
type RouteEntry struct {
	Provider        string
	Model           string // upstream model name
	ContextWindow   int
	MaxOutputTokens int
	InputPrice      float64
	OutputPrice     float64
	CacheWritePrice float64
	CacheReadPrice  float64
	// Codex model catalog metadata.
	DisplayName string
	Description string
	// BaseInstructions overrides the default model instructions for the catalog.
	BaseInstructions           string
	SupportsReasoning          bool
	DefaultReasoningLevel      string
	SupportedReasoningLevels   []ReasoningLevelPreset
	SupportsReasoningSummaries bool
	DefaultReasoningSummary    string
	// InputModalities lists accepted input modalities ("text", "image", etc.)
	// for the Codex models_catalog. If empty, defaults to ["text"].
	InputModalities []string
	// SupportsImageDetailOriginal indicates whether the model supports original
	// (non-compressed) image detail in the Codex models_catalog.
	SupportsImageDetailOriginal bool
	// WebSearch holds route-level web search config (overrides model and provider-level).
	WebSearch  WebSearchConfig
	Extensions map[string]ExtensionSettings
}

// ProviderDef defines a single upstream provider.
type ProviderDef struct {
	BaseURL   string
	APIKey    string
	Version   string
	UserAgent string
	Protocol  string // "anthropic" (default), "openai-response", "google-genai", or "openai-chat"
	// Phase 5: Google GenAI flat fields (D-09).
	// Only relevant when Protocol == ProtocolGoogleGenAI.
	// project: Google Cloud project ID (Vertex AI).
	// location: Google Cloud location (e.g., "us-central1").
	// api_version: Gemini API version (default "v1").
	Project    string `yaml:"project,omitempty"`
	Location   string `yaml:"location,omitempty"`
	APIVersion string `yaml:"api_version,omitempty"`
	// Cache config for this provider. If nil, provider does not use caching.
	Cache            *CacheConfig `yaml:"cache,omitempty"`
	WebSearchSupport WebSearchSupport
	WebSearchMaxUses int
	TavilyAPIKey     string
	FirecrawlAPIKey  string
	SearchMaxRounds  int
	WebSearchExtra   map[string]any
	Extensions       map[string]ExtensionSettings
	// Models is the provider's model catalog: upstream model name -> metadata.
	Models map[string]ModelMeta
	// Offers is the list of model offerings for this provider.
	Offers []OfferEntry
}

// ModelMeta holds metadata for a model offered by a provider.
type ModelMeta struct {
	ContextWindow   int
	MaxOutputTokens int
	InputPrice      float64
	OutputPrice     float64
	CacheWritePrice float64
	CacheReadPrice  float64
	// Codex model catalog metadata.
	DisplayName string
	Description string
	// BaseInstructions overrides the default model instructions for the catalog.
	BaseInstructions           string
	SupportsReasoning          bool
	DefaultReasoningLevel      string
	SupportedReasoningLevels   []ReasoningLevelPreset
	SupportsReasoningSummaries bool
	DefaultReasoningSummary    string
	// InputModalities lists accepted input modalities ("text", "image", etc.)
	// for the Codex models_catalog.
	InputModalities []string
	// SupportsImageDetailOriginal indicates whether the model supports original
	// (non-compressed) image detail.
	SupportsImageDetailOriginal bool
	// WebSearch holds model-level web search config (overrides provider-level).
	WebSearch  WebSearchConfig
	Extensions map[string]ExtensionSettings
}

// ModelPricing holds per-provider model pricing.
type ModelPricing struct {
	InputPrice      float64
	OutputPrice     float64
	CacheWritePrice float64
	CacheReadPrice  float64
}

// Defaults holds the default configuration values.
type Defaults struct {
	Model        string
	MaxTokens    int
	SystemPrompt string
}

// ModelDef holds model metadata (without per-provider pricing).
type ModelDef struct {
	ContextWindow               int
	MaxOutputTokens             int
	DisplayName                 string
	Description                 string
	BaseInstructions            string
	SupportsReasoning           bool
	DefaultReasoningLevel       string
	SupportedReasoningLevels    []ReasoningLevelPreset
	SupportsReasoningSummaries  bool
	DefaultReasoningSummary     string
	InputModalities             []string
	SupportsImageDetailOriginal bool
	WebSearch                   WebSearchConfig
	Extensions                  map[string]ExtensionSettings
}

// OfferEntry declares that a provider offers a model defined in Models.
type OfferEntry struct {
	Model        string // references models.<slug>
	UpstreamName string // optional, upstream model name (empty = same as slug)
	Priority     int    // lower value = higher priority (0 is highest)
	Pricing      ModelPricing
	Overrides    *ModelDef // optional provider-specific overrides
}

type ResponseProxyConfig struct {
	Model           string
	ProviderBaseURL string
	ProviderAPIKey  string
}

type AnthropicProxyConfig struct {
	Model           string
	ProviderBaseURL string
	ProviderAPIKey  string
	ProviderVersion string
}

// ReasoningLevelPreset describes a supported reasoning effort level.
type ReasoningLevelPreset struct {
	Effort      string
	Description string
}

// PersistenceConfig holds the persistence layer configuration.
type PersistenceConfig struct {
	ActiveProvider string
}

type CacheConfig struct {
	Mode                     string
	TTL                      string
	PromptCaching            bool
	AutomaticPromptCache     bool
	ExplicitCacheBreakpoints bool
	AllowRetentionDowngrade  bool
	MaxBreakpoints           int
	MinCacheTokens           int
	ExpectedReuse            int
	MinimumValueScore        int
	MinBreakpointTokens      int `yaml:"min_breakpoint_tokens"`
}

func (cfg Config) Validate() error {
	if err := cfg.Cache.Validate(); err != nil {
		return err
	}
	var err error
	switch cfg.Mode {
	case ModeTransform:
		err = cfg.validateTransform()
	case ModeCaptureResponse:
		err = cfg.ResponseProxy.Validate("developer.proxy.response")
	case ModeCaptureAnthropic:
		err = cfg.AnthropicProxy.Validate("developer.proxy.anthropic")
	default:
		return fmt.Errorf("invalid mode %q", cfg.Mode)
	}
	if err != nil {
		return err
	}
	return cfg.validateExtensions()
}

func (cfg Config) validateTransform() error {
	if err := cfg.validateSearchConfig(); err != nil {
		return err
	}
	if len(cfg.ProviderDefs) == 0 {
		return errors.New("providers are required in transform mode")
	}
	hasProviderModel := false
	for key, def := range cfg.ProviderDefs {
		if key == "" {
			return errors.New("providers cannot contain empty provider keys")
		}
		if def.BaseURL == "" {
			return fmt.Errorf("providers.%s.base_url is required", key)
		}
		if def.APIKey == "" {
			return fmt.Errorf("providers.%s.api_key is required", key)
		}
		switch def.Protocol {
		case "", ProtocolAnthropic, ProtocolOpenAIResponse, ProtocolGoogleGenAI, ProtocolOpenAIChat:
		default:
			return fmt.Errorf("providers.%s.protocol must be \"anthropic\", \"openai-response\", \"google-genai\", or \"openai-chat\"", key)
		}
		for modelName := range def.Models {
			if modelName == "" {
				return fmt.Errorf("providers.%s.models cannot contain empty model names", key)
			}
			hasProviderModel = true
		}
		// Validate Offers reference existing models.
		for i, offer := range def.Offers {
			if offer.Model == "" {
				return fmt.Errorf("providers.%s.offers[%d].model is required", key, i)
			}
			if _, ok := cfg.Models[offer.Model]; !ok {
				return fmt.Errorf("providers.%s.offers[%d].model %q not found in models", key, i, offer.Model)
			}
		}
	}
	if !hasProviderModel && len(cfg.Routes) == 0 {
		return errors.New("provider model catalog or routes must contain at least one model")
	}
	for alias, route := range cfg.Routes {
		if alias == "" || route.Model == "" {
			return errors.New("routes cannot contain empty aliases or models")
		}
		if _, ok := cfg.ProviderDefs[route.Provider]; !ok {
			return fmt.Errorf("routes.%s references unknown provider %q", alias, route.Provider)
		}
	}
	return nil
}

func (cfg Config) validateSearchConfig() error {
	if cfg.WebSearchSupport == WebSearchSupportInjected {
		if cfg.TavilyAPIKey == "" {
			return errors.New("provider.tavily_api_key is required when web_search.support is 'injected'")
		}
		if cfg.SearchMaxRounds <= 0 {
			return errors.New("provider.search_max_rounds must be > 0 when web_search.support is 'injected'")
		}
	}
	for key, def := range cfg.ProviderDefs {
		if def.WebSearchSupport == WebSearchSupportInjected {
			tavilyKey := def.TavilyAPIKey
			if tavilyKey == "" {
				tavilyKey = cfg.TavilyAPIKey
			}
			if tavilyKey == "" {
				return fmt.Errorf("providers.%s.web_search.tavily_api_key is required when web_search.support is 'injected'", key)
			}
			maxRounds := def.SearchMaxRounds
			if maxRounds <= 0 {
				maxRounds = cfg.SearchMaxRounds
			}
			if maxRounds <= 0 {
				return fmt.Errorf("providers.%s.web_search.search_max_rounds must be > 0 when web_search.support is 'injected'", key)
			}
		}
		// Validate per-provider cache config.
		if def.Cache != nil {
			if err := def.Cache.Validate(); err != nil {
				return fmt.Errorf("providers.%s.cache: %w", key, err)
			}
		}
	}
	return nil
}

func (cfg ResponseProxyConfig) Validate(prefix string) error {
	if cfg.ProviderBaseURL == "" {
		return fmt.Errorf("%s.provider.base_url is required", prefix)
	}
	if cfg.ProviderAPIKey == "" {
		return fmt.Errorf("%s.provider.api_key is required", prefix)
	}
	return nil
}

func (cfg AnthropicProxyConfig) Validate(prefix string) error {
	if cfg.ProviderBaseURL == "" {
		return fmt.Errorf("%s.provider.base_url is required", prefix)
	}
	if cfg.ProviderAPIKey == "" {
		return fmt.Errorf("%s.provider.api_key is required", prefix)
	}
	return nil
}

// ModelFor resolves a model alias to the upstream model name via Routes.
// Supports "provider/model" direct reference.
func (cfg Config) ModelFor(model string) string {
	// Direct provider/model reference.
	if provider, upstream := ParseModelRef(model); provider != "" {
		if _, ok := cfg.ProviderDefs[provider]; ok {
			return upstream
		}
	}
	if cfg.Routes == nil {
		return model
	}
	if route, ok := cfg.Routes[model]; ok && route.Model != "" {
		return route.Model
	}
	return model
}

// RouteFor returns the full RouteEntry for a model alias.
// Supports "provider/model" direct reference.
func (cfg Config) RouteFor(model string) RouteEntry {
	// Direct provider/model reference.
	if provider, upstream := ParseModelRef(model); provider != "" {
		if def, ok := cfg.ProviderDefs[provider]; ok {
			entry := RouteEntry{Provider: provider, Model: upstream}
			if meta, ok := def.Models[upstream]; ok {
				entry.ContextWindow = meta.ContextWindow
				entry.MaxOutputTokens = meta.MaxOutputTokens
				entry.InputPrice = meta.InputPrice
				entry.OutputPrice = meta.OutputPrice
				entry.CacheWritePrice = meta.CacheWritePrice
				entry.CacheReadPrice = meta.CacheReadPrice
				entry.DisplayName = meta.DisplayName
				entry.Description = meta.Description
				entry.SupportsReasoning = meta.SupportsReasoning
				entry.DefaultReasoningLevel = meta.DefaultReasoningLevel
				entry.SupportedReasoningLevels = meta.SupportedReasoningLevels
				entry.SupportsReasoningSummaries = meta.SupportsReasoningSummaries
				entry.DefaultReasoningSummary = meta.DefaultReasoningSummary
				entry.BaseInstructions = meta.BaseInstructions
				entry.InputModalities = meta.InputModalities
				entry.SupportsImageDetailOriginal = meta.SupportsImageDetailOriginal
				entry.WebSearch = meta.WebSearch
				entry.Extensions = meta.Extensions
			}
			return entry
		}
	}
	if cfg.Routes == nil {
		return RouteEntry{}
	}
	return cfg.Routes[model]
}

func (cfg Config) DefaultModelAlias() string {
	if cfg.Defaults.Model != "" {
		return cfg.Defaults.Model
	}
	if cfg.DefaultModel != "" {
		return cfg.DefaultModel
	}
	if _, ok := cfg.Routes["moonbridge"]; ok {
		return "moonbridge"
	}
	if len(cfg.Routes) == 1 {
		for alias := range cfg.Routes {
			return alias
		}
	}
	return ""
}

func (cfg Config) CodexModel() string {
	if cfg.Mode == ModeCaptureResponse && cfg.ResponseProxy.Model != "" {
		return cfg.ResponseProxy.Model
	}
	return cfg.DefaultModelAlias()
}

func (cfg Config) WebSearchEnabled() bool {
	return cfg.WebSearchSupport != WebSearchSupportDisabled
}

func (cfg Config) WebSearchInjected() bool {
	return cfg.WebSearchSupport == WebSearchSupportInjected
}

func (cfg Config) WebSearchProbeModel() string {
	alias := cfg.DefaultModelAlias()
	if alias == "" {
		return ""
	}
	return cfg.ModelFor(alias)
}

func (cfg *Config) DisableWebSearch() {
	cfg.WebSearchSupport = WebSearchSupportDisabled
}

func (cfg *Config) OverrideAddr(addr string) {
	if addr == "" {
		return
	}
	cfg.Addr = strings.TrimSpace(addr)
}

// WebSearchForProvider returns the resolved web search support for a given provider key.
func (cfg Config) WebSearchForProvider(providerKey string) WebSearchSupport {
	if def, ok := cfg.ProviderDefs[providerKey]; ok && def.WebSearchSupport != "" {
		return def.WebSearchSupport
	}
	return cfg.WebSearchSupport
}

// WebSearchForModel returns the resolved web search support for a given model alias.
// Resolution order: route -> model (in provider catalog) -> provider -> global ->
// provider catalog fallback for pure model names.
//
// NOTE on independent implementations: WebSearchForModel and ExtensionEnabled share a
// similar topological resolution chain (route → model → provider → global) but each has
// three distinct branches (route alias / direct "provider/model" ref / pure model name)
// with type-specific field access at every level. Encoding this as a generic resolveChain
// with (T, bool) closures would be more verbose than the current explicit code.
// ExtensionRawConfig is a fundamentally different merge-first operation, not first-match.
// These methods are kept as independent implementations for clarity.
func (cfg Config) WebSearchForModel(modelAlias string) WebSearchSupport {
	// 1. Route-level override.
	if route, ok := cfg.Routes[modelAlias]; ok {
		if route.WebSearch.Support != "" {
			return route.WebSearch.Support
		}
		// 2. Model-level override (from provider catalog).
		if def, ok := cfg.ProviderDefs[route.Provider]; ok {
			if meta, ok := def.Models[route.Model]; ok && meta.WebSearch.Support != "" {
				return meta.WebSearch.Support
			}
		}
		// 3. Provider-level.
		if route.Provider != "" {
			return cfg.WebSearchForProvider(route.Provider)
		}
	}
	// Direct provider/model reference.
	if provider, upstream := ParseModelRef(modelAlias); provider != "" {
		if def, ok := cfg.ProviderDefs[provider]; ok {
			if meta, ok := def.Models[upstream]; ok && meta.WebSearch.Support != "" {
				return meta.WebSearch.Support
			}
		}
		return cfg.WebSearchForProvider(provider)
	}
	// Pure model name: check provider catalog for model-level and provider-level fallback.
	for _, def := range cfg.ProviderDefs {
		if meta, ok := def.Models[modelAlias]; ok {
			if meta.WebSearch.Support != "" {
				return meta.WebSearch.Support
			}
			if def.WebSearchSupport != "" {
				return def.WebSearchSupport
			}
		}
	}
	return cfg.WebSearchSupport
}

// WebSearchMaxUsesForProvider returns the max uses for a given provider key.
func (cfg Config) WebSearchMaxUsesForProvider(providerKey string) int {
	if def, ok := cfg.ProviderDefs[providerKey]; ok && def.WebSearchMaxUses > 0 {
		return def.WebSearchMaxUses
	}
	if cfg.WebSearchMaxUses > 0 {
		return cfg.WebSearchMaxUses
	}
	return 8
}

// WebSearchTavilyKeyForProvider returns the Tavily API key for a given provider key.
func (cfg Config) WebSearchTavilyKeyForProvider(providerKey string) string {
	if def, ok := cfg.ProviderDefs[providerKey]; ok && def.TavilyAPIKey != "" {
		return def.TavilyAPIKey
	}
	return cfg.TavilyAPIKey
}

// WebSearchFirecrawlKeyForProvider returns the Firecrawl API key for a given provider key.
func (cfg Config) WebSearchFirecrawlKeyForProvider(providerKey string) string {
	if def, ok := cfg.ProviderDefs[providerKey]; ok && def.FirecrawlAPIKey != "" {
		return def.FirecrawlAPIKey
	}
	return cfg.FirecrawlAPIKey
}

// WebSearchMaxRoundsForProvider returns the search max rounds for a given provider key.
func (cfg Config) WebSearchMaxRoundsForProvider(providerKey string) int {
	if def, ok := cfg.ProviderDefs[providerKey]; ok && def.SearchMaxRounds > 0 {
		return def.SearchMaxRounds
	}
	if cfg.SearchMaxRounds > 0 {
		return cfg.SearchMaxRounds
	}
	return 5
}

// webSearchConfigForModel resolves the full WebSearchConfig for a model alias.
// Resolution: route -> model catalog -> provider -> global.
func (cfg Config) webSearchConfigForModel(modelAlias string) WebSearchConfig {
	var providerKey string
	var upstreamModel string
	if route, ok := cfg.Routes[modelAlias]; ok {
		if route.WebSearch.Support != "" {
			return route.WebSearch
		}
		providerKey = route.Provider
		upstreamModel = route.Model
	} else if p, u := ParseModelRef(modelAlias); p != "" {
		providerKey = p
		upstreamModel = u
	}
	if providerKey != "" {
		if def, ok := cfg.ProviderDefs[providerKey]; ok {
			if meta, ok := def.Models[upstreamModel]; ok && meta.WebSearch.Support != "" {
				return meta.WebSearch
			}
		}
	}
	// Pure model name: check provider catalog.
	for _, def := range cfg.ProviderDefs {
		if meta, ok := def.Models[modelAlias]; ok {
			if meta.WebSearch.Support != "" {
				return meta.WebSearch
			}
		}
	}
	return WebSearchConfig{}
}

// WebSearchMaxUsesForModel returns the max uses for a given model alias.
func (cfg Config) WebSearchMaxUsesForModel(modelAlias string) int {
	if ws := cfg.webSearchConfigForModel(modelAlias); ws.MaxUses > 0 {
		return ws.MaxUses
	}
	providerKey := cfg.providerKeyForModel(modelAlias)
	return cfg.WebSearchMaxUsesForProvider(providerKey)
}

// WebSearchTavilyKeyForModel returns the Tavily API key for a given model alias.
func (cfg Config) WebSearchTavilyKeyForModel(modelAlias string) string {
	if ws := cfg.webSearchConfigForModel(modelAlias); ws.TavilyAPIKey != "" {
		return ws.TavilyAPIKey
	}
	providerKey := cfg.providerKeyForModel(modelAlias)
	return cfg.WebSearchTavilyKeyForProvider(providerKey)
}

// WebSearchFirecrawlKeyForModel returns the Firecrawl API key for a given model alias.
func (cfg Config) WebSearchFirecrawlKeyForModel(modelAlias string) string {
	if ws := cfg.webSearchConfigForModel(modelAlias); ws.FirecrawlAPIKey != "" {
		return ws.FirecrawlAPIKey
	}
	providerKey := cfg.providerKeyForModel(modelAlias)
	return cfg.WebSearchFirecrawlKeyForProvider(providerKey)
}

// WebSearchMaxRoundsForModel returns the search max rounds for a given model alias.
func (cfg Config) WebSearchMaxRoundsForModel(modelAlias string) int {
	if ws := cfg.webSearchConfigForModel(modelAlias); ws.SearchMaxRounds > 0 {
		return ws.SearchMaxRounds
	}
	providerKey := cfg.providerKeyForModel(modelAlias)
	return cfg.WebSearchMaxRoundsForProvider(providerKey)
}

// providerKeyForModel returns the provider key for a model alias.
// Falls back to the first provider that has the model in its catalog for pure model names.
func (cfg Config) providerKeyForModel(modelAlias string) string {
	if route, ok := cfg.Routes[modelAlias]; ok {
		return route.Provider
	}
	if p, _ := ParseModelRef(modelAlias); p != "" {
		return p
	}
	// Pure model name: find the first provider that offers this model.
	for key, def := range cfg.ProviderDefs {
		if _, ok := def.Models[modelAlias]; ok {
			return key
		}
	}
	return ""
}

func (cfg CacheConfig) Validate() error {
	switch cfg.Mode {
	case "", "off", "automatic", "explicit", "hybrid":
	default:
		return fmt.Errorf("invalid cache mode %q", cfg.Mode)
	}
	switch cfg.TTL {
	case "", "5m", "1h":
	default:
		return fmt.Errorf("invalid cache ttl %q", cfg.TTL)
	}
	return nil
}

func valueOrDefault(value string, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func intOrDefault(value int, fallback int) int {
	if value == 0 {
		return fallback
	}
	return value
}

func boolOrDefault(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}

func (cfg Config) validateExtensions() error {
	for _, spec := range cfg.extensionSpecs {
		if spec.Validate == nil {
			continue
		}
		if err := spec.Validate(cfg); err != nil {
			return err
		}
	}
	return nil
}

// ExtensionEnabled returns whether an extension is enabled for a model alias.
// Resolution order is route -> provider model -> provider -> global -> spec default,
// with pure model name fallback to provider catalog.
func (cfg Config) ExtensionEnabled(name string, modelAlias string) bool {
	if modelAlias != "" {
		if route, ok := cfg.Routes[modelAlias]; ok {
			if setting, ok := route.Extensions[name]; ok && setting.Enabled != nil {
				return *setting.Enabled
			}
			if def, ok := cfg.ProviderDefs[route.Provider]; ok {
				if meta, ok := def.Models[route.Model]; ok {
					if setting, ok := meta.Extensions[name]; ok && setting.Enabled != nil {
						return *setting.Enabled
					}
				}
				if setting, ok := def.Extensions[name]; ok && setting.Enabled != nil {
					return *setting.Enabled
				}
			}
		} else if provider, upstream := ParseModelRef(modelAlias); provider != "" {
			if def, ok := cfg.ProviderDefs[provider]; ok {
				if meta, ok := def.Models[upstream]; ok {
					if setting, ok := meta.Extensions[name]; ok && setting.Enabled != nil {
						return *setting.Enabled
					}
				}
				if setting, ok := def.Extensions[name]; ok && setting.Enabled != nil {
					return *setting.Enabled
				}
			}
		} else {
			// Pure model name: check provider catalog.
			for _, def := range cfg.ProviderDefs {
				if meta, ok := def.Models[modelAlias]; ok {
					if setting, ok := meta.Extensions[name]; ok && setting.Enabled != nil {
						return *setting.Enabled
					}
					if setting, ok := def.Extensions[name]; ok && setting.Enabled != nil {
						return *setting.Enabled
					}
				}
			}
		}
	}
	if setting, ok := cfg.Extensions[name]; ok && setting.Enabled != nil {
		return *setting.Enabled
	}
	if spec, ok := cfg.extensionSpecs[name]; ok {
		return spec.DefaultEnabled
	}
	return false
}

// ExtensionRawConfig returns the shallow-merged extension config for a model
// alias. Merge order is global -> provider -> provider model -> route,
// with pure model name fallback to provider catalog.
func (cfg Config) ExtensionRawConfig(name string, modelAlias string) map[string]any {
	var parts []map[string]any
	if setting, ok := cfg.Extensions[name]; ok {
		parts = append(parts, setting.RawConfig)
	}
	if modelAlias != "" {
		if route, ok := cfg.Routes[modelAlias]; ok {
			if def, ok := cfg.ProviderDefs[route.Provider]; ok {
				if setting, ok := def.Extensions[name]; ok {
					parts = append(parts, setting.RawConfig)
				}
				if meta, ok := def.Models[route.Model]; ok {
					if setting, ok := meta.Extensions[name]; ok {
						parts = append(parts, setting.RawConfig)
					}
				}
			}
			if setting, ok := route.Extensions[name]; ok {
				parts = append(parts, setting.RawConfig)
			}
		} else if provider, upstream := ParseModelRef(modelAlias); provider != "" {
			if def, ok := cfg.ProviderDefs[provider]; ok {
				if setting, ok := def.Extensions[name]; ok {
					parts = append(parts, setting.RawConfig)
				}
				if meta, ok := def.Models[upstream]; ok {
					if setting, ok := meta.Extensions[name]; ok {
						parts = append(parts, setting.RawConfig)
					}
				}
			}
		} else {
			// Pure model name: check provider catalog.
			for _, def := range cfg.ProviderDefs {
				if meta, ok := def.Models[modelAlias]; ok {
					if setting, ok := def.Extensions[name]; ok {
						parts = append(parts, setting.RawConfig)
					}
					if setting, ok := meta.Extensions[name]; ok {
						parts = append(parts, setting.RawConfig)
					}
				}
			}
		}
	}
	return mergeAnyMaps(parts...)
}

// ExtensionConfig returns typed extension config for a model alias when the
// extension registered a factory; otherwise it returns the merged raw map.
func (cfg Config) ExtensionConfig(name string, modelAlias string) any {
	raw := cfg.ExtensionRawConfig(name, modelAlias)
	spec, ok := cfg.extensionSpecs[name]
	if !ok {
		return raw
	}
	return decodeTypedExtensionConfig(spec, raw)
}

// ProviderFor returns the provider key that serves the given model alias.
// Supports "provider/model" direct reference.
func (cfg Config) ProviderFor(modelAlias string) string {
	// Direct provider/model reference.
	if provider, _ := ParseModelRef(modelAlias); provider != "" {
		if _, ok := cfg.ProviderDefs[provider]; ok {
			return provider
		}
	}
	if route, ok := cfg.Routes[modelAlias]; ok {
		return route.Provider
	}
	return ""
}

// ProvidersForModel returns all provider keys that offer the given model name
// in their model catalog. Pure model name lookup (not route alias or direct ref).
func (cfg Config) ProvidersForModel(modelName string) []string {
	var keys []string
	for key, def := range cfg.ProviderDefs {
		if _, ok := def.Models[modelName]; ok {
			keys = append(keys, key)
		}
	}
	return keys
}

// ModelMetaFor returns the ModelMeta for a model name within a specific provider.
// Returns (meta, true) if found, (ModelMeta{}, false) otherwise.
func (cfg Config) ModelMetaFor(modelName string, providerKey string) (ModelMeta, bool) {
	def, ok := cfg.ProviderDefs[providerKey]
	if !ok {
		return ModelMeta{}, false
	}
	meta, ok := def.Models[modelName]
	return meta, ok
}

// ParseModelRef parses a model reference that may be in "provider/model" or "model(provider)" format.
// Returns (providerKey, modelName). If neither pattern matches, providerKey is "" and modelName is the input.
func ParseModelRef(ref string) (provider, model string) {
	return modelref.Parse(ref)
}
