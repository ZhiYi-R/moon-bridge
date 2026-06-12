// Package provider manages multiple upstream LLM providers and routes
// requests to the correct provider based on the requested model.
package provider

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"

	"moonbridge/internal/config"
	"moonbridge/internal/modelref"
	"moonbridge/internal/protocol/anthropic"
)

// HTTPConfig controls the HTTP connection pool for a provider.
type HTTPConfig struct {
	MaxIdleConnsPerHost int    `yaml:"max_idle_conns_per_host"`
	IdleConnTimeout     string `yaml:"idle_conn_timeout"`
}

// ModelMeta holds metadata for a model offered by a provider.
// Compatible with config.ModelMeta for cross-package usage.
type ModelMeta = config.ModelMeta

// ProviderConfig defines how to connect to a single upstream provider.
type ProviderConfig struct {
	BaseURL          string               `yaml:"base_url"`
	APIKey           string               `yaml:"api_key"`
	Version          string               `yaml:"version"`
	UserAgent        string               `yaml:"user_agent"`
	Protocol         string               // "anthropic" (default) or "openai-response"
	HTTP             HTTPConfig           `yaml:"http"`
	WebSearchSupport string               // "auto", "enabled", "disabled", "injected", or "" (inherit global)
	ModelNames       []string             // upstream model names for this provider
	Models           map[string]ModelMeta // full model metadata (upstream name -> meta) [deprecated: use Offers]
	Offers           []config.OfferEntry  // model offerings with pricing (replaces Models)
}

// modelProviderEntry is a reverse-index entry: provider key + offer priority.
type modelProviderEntry struct {
	providerKey string
	priority    int
}

// ModelRoute maps a model alias to a provider and an upstream model name.
type ModelRoute struct {
	Provider string `yaml:"provider"` // key in ProviderConfig map; empty means "default"
	Name     string `yaml:"name"`     // upstream model name
}

// ProviderCandidate represents a candidate provider for a resolved model.
type ProviderCandidate struct {
	ProviderKey   string
	UpstreamModel string
	Protocol      string // "anthropic" | "openai-response"
	Client        ProviderClient
}

// ResolvedRoute contains the result of model resolution.
type ResolvedRoute struct {
	Candidates []ProviderCandidate
}

// Preferred returns the preferred (first) candidate when available.
func (r *ResolvedRoute) Preferred() (ProviderCandidate, bool) {
	if len(r.Candidates) == 0 {
		return ProviderCandidate{}, false
	}
	return r.Candidates[0], true
}

// ProviderManager manages multiple upstream provider clients and routes
// model aliases to the appropriate provider.

// anthropicClientAdapter wraps an *anthropic.Client to implement ProviderClient.
type anthropicClientAdapter struct {
	client *anthropic.Client
}

func normalizeAnthropicMessageRequest(req any) (anthropic.MessageRequest, error) {
	switch v := req.(type) {
	case anthropic.MessageRequest:
		return v, nil
	case *anthropic.MessageRequest:
		if v == nil {
			return anthropic.MessageRequest{}, fmt.Errorf("expected anthropic.MessageRequest, got nil *anthropic.MessageRequest")
		}
		return *v, nil
	default:
		return anthropic.MessageRequest{}, fmt.Errorf("expected anthropic.MessageRequest, got %T", req)
	}
}

func (a *anthropicClientAdapter) CreateMessage(ctx context.Context, req any) (any, error) {
	msgReq, err := normalizeAnthropicMessageRequest(req)
	if err != nil {
		return nil, err
	}
	return a.client.CreateMessage(ctx, msgReq)
}

func (a *anthropicClientAdapter) StreamMessage(ctx context.Context, req any) (<-chan any, error) {
	msgReq, err := normalizeAnthropicMessageRequest(req)
	if err != nil {
		return nil, err
	}
	stream, err := a.client.StreamMessage(ctx, msgReq)
	if err != nil {
		return nil, err
	}
	out := make(chan any)
	go func() {
		defer close(out)
		for {
			evt, err := stream.Next()
			if err != nil {
				return
			}
			out <- evt
		}
	}()
	return out, nil
}

func (a *anthropicClientAdapter) AnthropicClient() *anthropic.Client {
	return a.client
}

// NewAnthropicClientAdapter wraps an *anthropic.Client to satisfy ProviderClient.
func NewAnthropicClientAdapter(client *anthropic.Client) ProviderClient {
	return &anthropicClientAdapter{client: client}
}

type ProviderManager struct {
	mu             sync.RWMutex // guards field replacement during Reload
	clients        map[string]ProviderClient
	providers      map[string]ProviderConfig       // provider key -> config (for inspection)
	routes         map[string]ModelRoute           // model alias -> route
	defaultK       string                          // default provider key
	resolvedWS     map[string]string               // provider key -> resolved web search support
	modelProviders map[string][]modelProviderEntry // upstream model name -> (provider key, priority) (reverse index)
}

// NewProviderManager creates a ProviderManager from provider configs and model routes.
// providerCfgs: provider key -> ProviderConfig
// routes: model alias -> ModelRoute
func NewProviderManager(providerCfgs map[string]ProviderConfig, routes map[string]ModelRoute) (*ProviderManager, error) {
	pm := &ProviderManager{
		clients:    make(map[string]ProviderClient, len(providerCfgs)),
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
		pm.clients[key] = &anthropicClientAdapter{client: anthropic.NewClient(anthropic.ClientConfig{
			BaseURL:   cfg.BaseURL,
			APIKey:    cfg.APIKey,
			Version:   cfg.Version,
			UserAgent: cfg.UserAgent,
			Client:    httpClient,
		})}
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
	// Build reverse index: model name -> provider keys (for dynamic model routing).
	pm.modelProviders = make(map[string][]modelProviderEntry, len(providerCfgs))
	for key, cfg := range providerCfgs {
		// Build set of model names already covered by Offers (with proper priority).
		offerModels := make(map[string]bool, len(cfg.Offers))
		for _, offer := range cfg.Offers {
			m := offer.Model
			if m == "" {
				m = offer.UpstreamName
			}
			if m != "" {
				offerModels[m] = true
			}
		}
		// Index from ModelNames (backward compat), skipping models already in Offers.
		for _, modelName := range cfg.ModelNames {
			if offerModels[modelName] {
				continue
			}
			pm.modelProviders[modelName] = append(pm.modelProviders[modelName], modelProviderEntry{providerKey: key, priority: 0})
		}
		// Index from Offers with actual priority.
		for _, offer := range cfg.Offers {
			modelName := offer.Model
			if modelName == "" {
				modelName = offer.UpstreamName
			}
			if modelName != "" {
				pm.modelProviders[modelName] = append(pm.modelProviders[modelName], modelProviderEntry{providerKey: key, priority: offer.Priority})
			}
		}
	}
	return pm, nil
}

// Reload atomically replaces the internal state by building a new
// ProviderManager from the given config. If building fails, the
// existing state is preserved and an error is returned.
func (pm *ProviderManager) Reload(cfg config.ProviderConfig) error {
	// Convert config.ProviderDefs to provider.ProviderConfig map.
	providerDefs := make(map[string]ProviderConfig, len(cfg.Providers))
	for key, def := range cfg.Providers {
		modelNames := make([]string, 0, len(def.Models))
		for name := range def.Models {
			modelNames = append(modelNames, name)
		}
		models := make(map[string]ModelMeta, len(def.Models))
		for name, meta := range def.Models {
			models[name] = ModelMeta(meta)
		}
		providerDefs[key] = ProviderConfig{
			BaseURL:          def.BaseURL,
			APIKey:           def.APIKey,
			Version:          def.Version,
			UserAgent:        def.UserAgent,
			Protocol:         def.Protocol,
			WebSearchSupport: string(def.WebSearchSupport),
			ModelNames:       modelNames,
			Models:           models,
			Offers:           def.Offers,
		}
	}

	// Convert config.RouteEntry to provider.ModelRoute map.
	modelRoutes := make(map[string]ModelRoute, len(cfg.Routes))
	for alias, route := range cfg.Routes {
		modelRoutes[alias] = ModelRoute{
			Provider: route.Provider,
			Name:     route.Model,
		}
	}

	// Build the new manager (validates + creates clients).
	newPM, err := NewProviderManager(providerDefs, modelRoutes)
	if err != nil {
		return err
	}

	// Atomically replace fields under lock.
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.clients = newPM.clients
	pm.providers = newPM.providers
	pm.routes = newPM.routes
	pm.defaultK = newPM.defaultK
	pm.resolvedWS = newPM.resolvedWS
	pm.modelProviders = newPM.modelProviders
	return nil
}

// ClientFor returns the anthropic.Client and upstream model name for a given model alias.
// It returns the default provider if the alias is not explicitly routed.
func (pm *ProviderManager) ClientFor(modelAlias string) (string, ProviderClient, error) {
	// Direct provider/model reference.
	pm.mu.RLock()
	defer pm.mu.RUnlock()
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

// ResolveModel resolves a model name to a ResolvedRoute with candidate providers.
// Priority:
//  1. Route alias (explicit routes map)
//  2. Direct ref (provider/model or model(provider))
//  3. Dynamic model (reverse index from provider catalog ModelNames)
//
// Returns error if no candidates are found.
func (pm *ProviderManager) ResolveModel(modelName string) (*ResolvedRoute, error) {
	// 1. Route alias (highest priority)
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	if route, ok := pm.routes[modelName]; ok {
		providerKey := route.Provider
		if providerKey == "" {
			providerKey = pm.defaultK
		}
		client := pm.clients[providerKey]
		if client == nil {
			return nil, fmt.Errorf("provider %q (referenced by route %q) not configured", providerKey, modelName)
		}
		return &ResolvedRoute{
			Candidates: []ProviderCandidate{{
				ProviderKey:   providerKey,
				UpstreamModel: route.Name,
				Protocol:      pm.protocolForKeyInline(providerKey),
				Client:        client,
			}},
		}, nil
	}

	// 2. Direct ref: provider/model or model(provider)
	if providerKey, upstreamModel := ParseModelRef(modelName); providerKey != "" {
		client, ok := pm.clients[providerKey]
		if !ok {
			return nil, fmt.Errorf("provider %q not found for model reference %q", providerKey, modelName)
		}
		return &ResolvedRoute{
			Candidates: []ProviderCandidate{{
				ProviderKey:   providerKey,
				UpstreamModel: upstreamModel,
				Protocol:      pm.protocolForKeyInline(providerKey),
				Client:        client,
			}},
		}, nil
	}

	// 3. Dynamic model: look up reverse index (provider catalog)
	if providerEntries, ok := pm.modelProviders[modelName]; ok && len(providerEntries) > 0 {
		sorted := make([]modelProviderEntry, len(providerEntries))
		copy(sorted, providerEntries)
		sort.Slice(sorted, func(i, j int) bool {
			pi := sorted[i].priority
			pj := sorted[j].priority
			if pi != pj {
				return pi < pj // lower priority = higher precedence (0 is highest)
			}
			return sorted[i].providerKey < sorted[j].providerKey // tiebreaker: provider key dictionary order
		})

		candidates := make([]ProviderCandidate, 0, len(sorted))
		for _, entry := range sorted {
			client := pm.clients[entry.providerKey]
			if client == nil {
				continue
			}
			candidates = append(candidates, ProviderCandidate{
				ProviderKey:   entry.providerKey,
				UpstreamModel: modelName,
				Protocol:      pm.protocolForKeyInline(entry.providerKey),
				Client:        client,
			})
		}
		if len(candidates) == 0 {
			return nil, fmt.Errorf("no available provider for model %q", modelName)
		}
		return &ResolvedRoute{Candidates: candidates}, nil
	}

	// 4. No match
	return nil, fmt.Errorf("no route or provider found for model %q", modelName)
}

// ProbeWebSearch probes a specific model's provider for web_search support.
func (pm *ProviderManager) ProbeWebSearch(ctx context.Context, modelAlias string) (bool, error) {
	upstreamModel, client, err := pm.ClientFor(modelAlias)
	if err != nil {
		return false, err
	}
	accessor, ok := client.(AnthropicClientAccessor)
	if !ok {
		return false, fmt.Errorf("provider client for %q does not support web search probing", modelAlias)
	}
	return accessor.AnthropicClient().ProbeWebSearch(ctx, upstreamModel)
}

// ProbeWebSearchCandidate probes web_search support for a specific provider/model pair.
func (pm *ProviderManager) ProbeWebSearchCandidate(ctx context.Context, providerKey, upstreamModel string) (bool, error) {
	if providerKey == "" {
		return false, fmt.Errorf("provider key is required")
	}
	if upstreamModel == "" {
		return false, fmt.Errorf("upstream model is required")
	}
	client, err := pm.ClientForKey(providerKey)
	if err != nil {
		return false, err
	}
	accessor, ok := client.(AnthropicClientAccessor)
	if !ok {
		return false, fmt.Errorf("provider client for %q does not support web search probing", providerKey)
	}
	return accessor.AnthropicClient().ProbeWebSearch(ctx, upstreamModel)
}

// ProviderKeys returns all configured provider keys.
func (pm *ProviderManager) ProviderKeys() []string {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	keys := make([]string, 0, len(pm.clients))
	for k := range pm.clients {
		keys = append(keys, k)
	}
	return keys
}

// DefaultKey returns the default provider key.
func (pm *ProviderManager) DefaultKey() string {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
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
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   maxIdle,
			IdleConnTimeout:       idleTimeout,
			DisableCompression:    false,
			ResponseHeaderTimeout: 30 * time.Second,
		},
	}
}

func valueOrDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// ClientForKey returns the anthropic.Client for a given provider key.
func (pm *ProviderManager) ClientForKey(key string) (ProviderClient, error) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	client, ok := pm.clients[key]
	if !ok {
		return nil, fmt.Errorf("provider %q not found", key)
	}
	return client, nil
}

// ProtocolForKey returns the protocol for a given provider key.
// Returns "anthropic" if not configured.
// protocolForKeyInline returns the protocol for a provider key.
// Caller must hold pm.mu (read lock).
func (pm *ProviderManager) protocolForKeyInline(key string) string {
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

func (pm *ProviderManager) ProtocolForKey(key string) string {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
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
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	// Direct provider/model reference.
	if provider, _ := ParseModelRef(modelAlias); provider != "" {
		return pm.protocolForKeyInline(provider)
	}
	route, ok := pm.routes[modelAlias]
	if !ok {
		return pm.protocolForKeyInline(pm.defaultK)
	}
	providerKey := route.Provider
	if providerKey == "" {
		providerKey = pm.defaultK
	}
	return pm.protocolForKeyInline(providerKey)
}

// UpstreamModelFor returns the upstream model name for a model alias.
func (pm *ProviderManager) UpstreamModelFor(modelAlias string) string {
	// Direct provider/model reference.
	pm.mu.RLock()
	defer pm.mu.RUnlock()
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
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	cfg, ok := pm.providers[key]
	if !ok {
		return ""
	}
	return cfg.BaseURL
}

// ProviderAPIKey returns the API key for a given provider key.
func (pm *ProviderManager) ProviderAPIKey(key string) string {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
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
	pm.mu.RLock()
	defer pm.mu.RUnlock()
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

// providerKeyForModelInline returns the provider key for a model alias.
// Caller must hold pm.mu (read lock).
func (pm *ProviderManager) providerKeyForModelInline(modelAlias string) string {
	if provider, _ := modelref.Parse(modelAlias); provider != "" {
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

// WebSearchCandidateKey returns the runtime key for a resolved provider/model pair.
func WebSearchCandidateKey(providerKey, upstreamModel string) string {
	return "candidate:" + providerKey + "/" + upstreamModel
}

// SetResolvedWebSearch stores the resolved web search support for a provider key.
// Also accepts model aliases for per-model resolution.
func (pm *ProviderManager) SetResolvedWebSearch(key string, support string) {
	pm.resolvedWS[key] = support
}

// ResolvedWebSearch returns the resolved web search support for a provider key.
// Returns empty string if not yet resolved.
func (pm *ProviderManager) ResolvedWebSearch(key string) string {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	return pm.resolvedWS[key]
}

// ModelMetaFor returns the ModelMeta for a model name within a specific provider.
func (pm *ProviderManager) ModelMetaFor(modelName string, providerKey string) (ModelMeta, bool) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	cfg, ok := pm.providers[providerKey]
	if !ok {
		return ModelMeta{}, false
	}
	meta, ok := cfg.Models[modelName]
	return meta, ok
}

// ProviderDefForKey returns the full ProviderConfig for a given provider key.
func (pm *ProviderManager) ProviderDefForKey(key string) (ProviderConfig, bool) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	cfg, ok := pm.providers[key]
	if !ok {
		return ProviderConfig{}, false
	}
	return cfg, ok
}

// ResolvedWebSearchForModel returns the resolved web search support for a model alias.
// Checks model-level first, then falls back to provider-level.
func (pm *ProviderManager) ResolvedWebSearchForModel(modelAlias string) string {
	// Check model-level resolution first.
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	if v, ok := pm.resolvedWS["model:"+modelAlias]; ok {
		return v
	}
	var providerKey, upstreamModel string
	ok := false
	if provider, upstream := modelref.Parse(modelAlias); provider != "" {
		if _, exists := pm.clients[provider]; exists {
			providerKey, upstreamModel, ok = provider, upstream, true
		}
	}
	if !ok {
		if route, exists := pm.routes[modelAlias]; exists {
			if route.Provider == "" {
				providerKey = pm.defaultK
			} else {
				providerKey = route.Provider
			}
			upstreamModel, ok = route.Name, true
		}
	}
	if !ok {
		if pm.defaultK != "" {
			providerKey, upstreamModel, ok = pm.defaultK, modelAlias, true
		}
	}
	if ok {
		if v, ok := pm.resolvedWS[WebSearchCandidateKey(providerKey, upstreamModel)]; ok {
			return v
		}
	}
	// Fall back to provider-level.
	return pm.resolvedWS[pm.providerKeyForModelInline(modelAlias)]
}

// ResolvedWebSearchForCandidate returns the resolved web search support for a provider/model pair.
// Falls back to provider-level support when no candidate-specific resolution exists.
func (pm *ProviderManager) ResolvedWebSearchForCandidate(providerKey, upstreamModel string) string {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	if providerKey == "" {
		return ""
	}
	if upstreamModel != "" {
		if v, ok := pm.resolvedWS[WebSearchCandidateKey(providerKey, upstreamModel)]; ok {
			return v
		}
	}
	return pm.resolvedWS[providerKey]
}

// WebSearchConfigForKey returns the configured web search support for a provider key.
func (pm *ProviderManager) WebSearchConfigForKey(key string) string {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
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
	pm.mu.RLock()
	defer pm.mu.RUnlock()
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

// ProviderAndUpstreamForModel resolves the provider key and upstream model for a model alias.
func (pm *ProviderManager) ProviderAndUpstreamForModel(modelAlias string) (providerKey string, upstreamModel string, ok bool) {
	// Direct provider/model reference.
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	if provider, upstream := ParseModelRef(modelAlias); provider != "" {
		if _, exists := pm.clients[provider]; exists {
			return provider, upstream, true
		}
	}
	if route, exists := pm.routes[modelAlias]; exists {
		providerKey = route.Provider
		if providerKey == "" {
			providerKey = pm.defaultK
		}
		return providerKey, route.Name, true
	}
	if pm.defaultK == "" {
		return "", "", false
	}
	return pm.defaultK, modelAlias, true
}

// ParseModelRef parses a model reference that may be in "provider/model" or "model(provider)" format.
// Returns (providerKey, modelName). If neither pattern matches, providerKey is "" and modelName is the input.
func ParseModelRef(ref string) (provider, model string) {
	return modelref.Parse(ref)
}
