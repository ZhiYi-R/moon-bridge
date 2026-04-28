// Package provider manages multiple upstream LLM providers and routes
// requests to the correct provider based on the requested model.
package provider

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"moonbridge/internal/foundation/modelref"
	"moonbridge/internal/protocol/anthropic"
)

// HTTPConfig controls the HTTP connection pool for a provider.
type HTTPConfig struct {
	MaxIdleConnsPerHost int    `yaml:"max_idle_conns_per_host"`
	IdleConnTimeout     string `yaml:"idle_conn_timeout"`
}

// ProviderConfig defines how to connect to a single upstream provider.
type ProviderConfig struct {
	BaseURL          string     `yaml:"base_url"`
	APIKey           string     `yaml:"api_key"`
	Version          string     `yaml:"version"`
	UserAgent        string     `yaml:"user_agent"`
	Protocol         string     // "anthropic" (default) or "openai-response"
	HTTP             HTTPConfig `yaml:"http"`
	WebSearchSupport string     // "auto", "enabled", "disabled", "injected", or "" (inherit global)
	ModelNames       []string   // upstream model names for this provider
}

// ModelRoute maps a model alias to a provider and an upstream model name.
type ModelRoute struct {
	Provider string `yaml:"provider"` // key in ProviderConfig map; empty means "default"
	Name     string `yaml:"name"`     // upstream model name
}

// ProviderClient wraps an anthropic.Client (or equivalent) with its key.
type ProviderClient struct {
	Client   *anthropic.Client
	Provider string // provider key
}

// ProviderManager manages multiple upstream provider clients and routes
// model aliases to the appropriate provider.
type ProviderManager struct {
	clients    map[string]*anthropic.Client // provider key -> anthropic.Client
	providers  map[string]ProviderConfig    // provider key -> config (for inspection)
	routes     map[string]ModelRoute        // model alias -> route
	defaultK   string                       // default provider key
	resolvedWS map[string]string            // provider key -> resolved web search support
}

// NewProviderManager creates a ProviderManager from provider configs and model routes.
// providerCfgs: provider key -> ProviderConfig
// routes: model alias -> ModelRoute
func NewProviderManager(providerCfgs map[string]ProviderConfig, routes map[string]ModelRoute) (*ProviderManager, error) {
	pm := &ProviderManager{
		clients:    make(map[string]*anthropic.Client, len(providerCfgs)),
		providers:  providerCfgs,
		routes:     routes,
		resolvedWS: make(map[string]string, len(providerCfgs)),
	}

	// Build clients for each provider config.
	for key, cfg := range providerCfgs {
		if cfg.BaseURL == "" {
			return nil, fmt.Errorf("provider %q: base_url is required", key)
		}
		httpClient := newHTTPClient(cfg.HTTP)
		pm.clients[key] = anthropic.NewClient(anthropic.ClientConfig{
			BaseURL:   cfg.BaseURL,
			APIKey:    cfg.APIKey,
			Version:   cfg.Version,
			UserAgent: cfg.UserAgent,
			Client:    httpClient,
		})
	}

	// Pick the default key.
	if _, hasDefault := providerCfgs["default"]; hasDefault {
		pm.defaultK = "default"
	} else if len(providerCfgs) == 1 {
		for k := range providerCfgs {
			pm.defaultK = k
		}
	}

	if len(pm.clients) == 0 {
		return nil, fmt.Errorf("at least one provider must be configured")
	}
	return pm, nil
}

// ClientFor returns the anthropic.Client and upstream model name for a given model alias.
// It returns the default provider if the alias is not explicitly routed.
func (pm *ProviderManager) ClientFor(modelAlias string) (string, *anthropic.Client, error) {
	// Direct provider/model reference.
	if provider, upstream := ParseModelRef(modelAlias); provider != "" {
		if client, ok := pm.clients[provider]; ok {
			return upstream, client, nil
		}
	}

	route, ok := pm.routes[modelAlias]
	if !ok {
		// No explicit route: use default provider with the model name as-is.
		client, ok := pm.clients[pm.defaultK]
		if !ok {
			return "", nil, fmt.Errorf("no route for model %q and no default provider", modelAlias)
		}
		return modelAlias, client, nil
	}

	providerKey := route.Provider
	if providerKey == "" {
		providerKey = pm.defaultK
	}
	client, ok := pm.clients[providerKey]
	if !ok {
		return "", nil, fmt.Errorf("provider %q (referenced by model %q) not configured", providerKey, modelAlias)
	}
	return route.Name, client, nil
}

// ProbeWebSearch probes a specific model's provider for web_search support.
func (pm *ProviderManager) ProbeWebSearch(ctx context.Context, modelAlias string) (bool, error) {
	upstreamModel, client, err := pm.ClientFor(modelAlias)
	if err != nil {
		return false, err
	}
	return client.ProbeWebSearch(ctx, upstreamModel)
}

// ProviderKeys returns all configured provider keys.
func (pm *ProviderManager) ProviderKeys() []string {
	keys := make([]string, 0, len(pm.clients))
	for k := range pm.clients {
		keys = append(keys, k)
	}
	return keys
}

// DefaultKey returns the default provider key.
func (pm *ProviderManager) DefaultKey() string {
	return pm.defaultK
}

// newHTTPClient creates an *http.Client with connection pooling configured.
func newHTTPClient(cfg HTTPConfig) *http.Client {
	maxIdle := cfg.MaxIdleConnsPerHost
	if maxIdle <= 0 {
		maxIdle = 4
	}

	idleTimeout := 90 * time.Second
	if cfg.IdleConnTimeout != "" {
		if d, err := time.ParseDuration(cfg.IdleConnTimeout); err == nil {
			idleTimeout = d
		}
	}

	return &http.Client{
		Transport: &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: maxIdle,
			IdleConnTimeout:     idleTimeout,
			DisableCompression:  false,
		},
	}
}

// BuildProviderConfigs converts legacy single-provider config and new multi-provider
// config into a unified provider config map.
// If new-style providers are present, they take precedence.
func BuildProviderConfigs(
	legacyBaseURL, legacyAPIKey, legacyVersion, legacyUserAgent string,
	newProviders map[string]ProviderConfig,
) map[string]ProviderConfig {
	if len(newProviders) > 0 {
		// Normalise: ensure every provider has a base URL.
		normalised := make(map[string]ProviderConfig, len(newProviders))
		for k, v := range newProviders {
			v.BaseURL = strings.TrimRight(strings.TrimSpace(v.BaseURL), "/")
			v.APIKey = strings.TrimSpace(v.APIKey)
			v.Version = strings.TrimSpace(v.Version)
			v.UserAgent = strings.TrimSpace(v.UserAgent)
			normalised[k] = v
		}
		return normalised
	}

	// Legacy single-provider mode: build a "default" entry.
	cfg := ProviderConfig{
		BaseURL:   strings.TrimRight(strings.TrimSpace(legacyBaseURL), "/"),
		APIKey:    strings.TrimSpace(legacyAPIKey),
		Version:   valueOrDefault(legacyVersion, "2023-06-01"),
		UserAgent: strings.TrimSpace(legacyUserAgent),
	}
	if cfg.BaseURL == "" {
		return nil
	}
	return map[string]ProviderConfig{"default": cfg}
}

func valueOrDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// ClientForKey returns the anthropic.Client for a given provider key.
func (pm *ProviderManager) ClientForKey(key string) (*anthropic.Client, error) {
	client, ok := pm.clients[key]
	if !ok {
		return nil, fmt.Errorf("provider %q not found", key)
	}
	return client, nil
}

// ProtocolForKey returns the protocol for a given provider key.
// Returns "anthropic" if not configured.
func (pm *ProviderManager) ProtocolForKey(key string) string {
	if pm.providers == nil {
		return "anthropic"
	}
	cfg, ok := pm.providers[key]
	if !ok {
		return "anthropic"
	}
	if cfg.Protocol == "" {
		return "anthropic"
	}
	return cfg.Protocol
}

// ProtocolForModel returns the protocol for the provider serving the given model alias.
// Returns "anthropic" if the model is not explicitly routed.
func (pm *ProviderManager) ProtocolForModel(modelAlias string) string {
	// Direct provider/model reference.
	if provider, _ := ParseModelRef(modelAlias); provider != "" {
		return pm.ProtocolForKey(provider)
	}
	route, ok := pm.routes[modelAlias]
	if !ok {
		return pm.ProtocolForKey(pm.defaultK)
	}
	providerKey := route.Provider
	if providerKey == "" {
		providerKey = pm.defaultK
	}
	return pm.ProtocolForKey(providerKey)
}

// UpstreamModelFor returns the upstream model name for a model alias.
func (pm *ProviderManager) UpstreamModelFor(modelAlias string) string {
	// Direct provider/model reference.
	if provider, upstream := ParseModelRef(modelAlias); provider != "" {
		if _, ok := pm.clients[provider]; ok {
			return upstream
		}
	}
	route, ok := pm.routes[modelAlias]
	if !ok || route.Name == "" {
		return modelAlias
	}
	return route.Name
}

// ProviderBaseURL returns the base URL for a given provider key.
func (pm *ProviderManager) ProviderBaseURL(key string) string {
	cfg, ok := pm.providers[key]
	if !ok {
		return ""
	}
	return cfg.BaseURL
}

// ProviderAPIKey returns the API key for a given provider key.
func (pm *ProviderManager) ProviderAPIKey(key string) string {
	cfg, ok := pm.providers[key]
	if !ok {
		return ""
	}
	return cfg.APIKey
}

// ProviderKeyForModel returns the provider key that serves the given model alias.
// Falls back to defaultK when the model has no explicit route.
func (pm *ProviderManager) ProviderKeyForModel(modelAlias string) string {
	// Direct provider/model reference.
	if provider, _ := ParseModelRef(modelAlias); provider != "" {
		if _, ok := pm.clients[provider]; ok {
			return provider
		}
	}
	route, ok := pm.routes[modelAlias]
	if !ok || route.Provider == "" {
		return pm.defaultK
	}
	return route.Provider
}

// SetResolvedWebSearch stores the resolved web search support for a provider key.
// Also accepts model aliases for per-model resolution.
func (pm *ProviderManager) SetResolvedWebSearch(key string, support string) {
	pm.resolvedWS[key] = support
}

// ResolvedWebSearch returns the resolved web search support for a provider key.
// Returns empty string if not yet resolved.
func (pm *ProviderManager) ResolvedWebSearch(key string) string {
	return pm.resolvedWS[key]
}

// ResolvedWebSearchForModel returns the resolved web search support for a model alias.
// Checks model-level first, then falls back to provider-level.
func (pm *ProviderManager) ResolvedWebSearchForModel(modelAlias string) string {
	// Check model-level resolution first.
	if v, ok := pm.resolvedWS["model:"+modelAlias]; ok {
		return v
	}
	// Fall back to provider-level.
	return pm.resolvedWS[pm.ProviderKeyForModel(modelAlias)]
}

// WebSearchConfigForKey returns the configured web search support for a provider key.
func (pm *ProviderManager) WebSearchConfigForKey(key string) string {
	cfg, ok := pm.providers[key]
	if !ok {
		return ""
	}
	return cfg.WebSearchSupport
}

// FirstUpstreamModelForKey returns the upstream model name for the first model
// alias routed to the given provider key. Falls back to the provider's own
// model list when no route alias references it. Returns empty string if none found.
func (pm *ProviderManager) FirstUpstreamModelForKey(key string) string {
	for _, route := range pm.routes {
		pk := route.Provider
		if pk == "" {
			pk = pm.defaultK
		}
		if pk == key {
			return route.Name
		}
	}

	// Fallback: use the first model from the provider's own catalog.
	if cfg, ok := pm.providers[key]; ok && len(cfg.ModelNames) > 0 {
		return cfg.ModelNames[0]
	}
	return ""
}

// ParseModelRef parses a model reference that may be in "provider/model" or "model(provider)" format.
// Returns (providerKey, modelName). If neither pattern matches, providerKey is "" and modelName is the input.
func ParseModelRef(ref string) (provider, model string) {
	return modelref.Parse(ref)
}
