package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"moonbridge/internal/config"
	"moonbridge/internal/extension/plugin"
	"moonbridge/internal/format"
	"moonbridge/internal/logger"
	"moonbridge/internal/protocol/chat"
	"moonbridge/internal/protocol/google"
	"moonbridge/internal/protocol/openai"
	"moonbridge/internal/service/api"
	"moonbridge/internal/service/provider"
	"moonbridge/internal/service/runtime"
	"moonbridge/internal/service/stats"
	"moonbridge/internal/service/store"
	"moonbridge/internal/service/webui"

	"moonbridge/internal/service/server/session"
	"moonbridge/internal/service/server/trace"
	"moonbridge/internal/service/server/usage"

	mbtrace "moonbridge/internal/service/trace"
)

// ChatClient is the interface for OpenAI-chat protocol clients.
// It uses any parameters to avoid importing protocol-specific packages.
type Config struct {
	// ServerCfg is the scoped domain config for the server layer.
	// Used alongside AppConfig for the full config.
	ServerCfg        config.ServerConfig
	AdapterRegistry  *format.Registry        // adapter dispatch path (format registry)
	Provider         provider.ProviderClient // fallback provider for non-adapter path
	ProviderMgr      *provider.ProviderManager
	OpenAIHTTPClient *http.Client
	ProxyHTTPClient  *http.Client
	ChatClients      map[string]any
	GoogleClients    map[string]any
	Tracer           *mbtrace.Tracer
	TraceErrors      io.Writer
	Stats            *stats.SessionStats
	PluginRegistry   *plugin.Registry
	AppConfig        config.ServerConfig
	Runtime          *runtime.Runtime
	Store            store.ConfigStore
	SessionManager   session.Manager
	UsageTracker     usage.Tracker
	TraceWriter      trace.Writer
}

type Server struct {
	adapterRegistry *format.Registry
	provider        provider.ProviderClient
	providerMgr     *provider.ProviderManager
	openAIHTTP      *http.Client
	proxyHTTP       *http.Client
	chatClients     map[string]any
	googleClients   map[string]any
	tracer          *mbtrace.Tracer
	traceErrors     io.Writer
	stats           *stats.SessionStats
	pluginRegistry  *plugin.Registry
	mux             *http.ServeMux
	onceClose       sync.Once
	appConfig       config.ServerConfig
	serverCfg       config.ServerConfig
	runtime         *runtime.Runtime
	store           store.ConfigStore
	sessionManager  session.Manager
	usageTracker    usage.Tracker
	traceWriter     trace.Writer

	// clientCaches holds lazily-created HTTP clients for runtime-reloaded providers.
	// Keyed by provider key, invalidated when Runtime reloads.
	clientCache   map[string]*chat.Client
	googleCache   map[string]*google.Client
	clientCacheMu sync.RWMutex
	googleCacheMu sync.RWMutex
}

func (s *Server) runtimeSnapshot() *runtime.ConfigSnapshot {
	if s.runtime == nil {
		return nil
	}
	return s.runtime.Current()
}

func (s *Server) activeProviderManager() *provider.ProviderManager {
	if snap := s.runtimeSnapshot(); snap != nil && snap.ProviderMgr != nil {
		return snap.ProviderMgr
	}
	return s.providerMgr
}

func (s *Server) activeProviderDefs() map[string]config.ProviderDef {
	if snap := s.runtimeSnapshot(); snap != nil {
		return snap.Config.ProviderDefs
	}
	return nil
}

func (s *Server) activeChatClient(providerKey string) any {
	// Check runtime-driven cache first.
	s.clientCacheMu.RLock()
	if cached, ok := s.clientCache[providerKey]; ok {
		s.clientCacheMu.RUnlock()
		return cached
	}
	s.clientCacheMu.RUnlock()

	if snap := s.runtimeSnapshot(); snap != nil {
		if def, ok := snap.Config.ProviderDefs[providerKey]; ok && def.Protocol == config.ProtocolOpenAIChat {
			client := chat.NewClient(chat.ClientConfig{
				BaseURL:   def.BaseURL,
				APIKey:    def.APIKey,
				UserAgent: def.UserAgent,
			})
			s.clientCacheMu.Lock()
			s.clientCache[providerKey] = client
			s.clientCacheMu.Unlock()
			return client
		}
	}
	return s.chatClients[providerKey]
}

func (s *Server) activeGoogleClient(providerKey string) any {
	// Check runtime-driven cache first.
	s.googleCacheMu.RLock()
	if cached, ok := s.googleCache[providerKey]; ok {
		s.googleCacheMu.RUnlock()
		return cached
	}
	s.googleCacheMu.RUnlock()

	if snap := s.runtimeSnapshot(); snap != nil {
		if def, ok := snap.Config.ProviderDefs[providerKey]; ok && def.Protocol == config.ProtocolGoogleGenAI {
			client := google.NewClient(google.ClientConfig{
				BaseURL:   def.BaseURL,
				APIKey:    def.APIKey,
				Project:   def.Project,
				Location:  def.Location,
				Version:   def.APIVersion,
				UserAgent: def.UserAgent,
			})
			s.googleCacheMu.Lock()
			s.googleCache[providerKey] = client
			s.googleCacheMu.Unlock()
			return client
		}
	}
	return s.googleClients[providerKey]
}

func New(cfg Config) *Server {
	if cfg.SessionManager == nil {
		cfg.SessionManager = newDefaultSessionManager(cfg)
	}
	s := &Server{
		adapterRegistry: cfg.AdapterRegistry,
		provider:        cfg.Provider,
		providerMgr:     cfg.ProviderMgr,
		openAIHTTP:      cfg.OpenAIHTTPClient,
		proxyHTTP:       cfg.ProxyHTTPClient,
		tracer:          cfg.Tracer,
		traceErrors:     cfg.TraceErrors,
		stats:           cfg.Stats,
		pluginRegistry:  cfg.PluginRegistry,
		mux:             http.NewServeMux(),
		appConfig:       cfg.AppConfig,
		serverCfg:       cfg.ServerCfg,
		chatClients:     cfg.ChatClients,
		googleClients:   cfg.GoogleClients,
		runtime:         cfg.Runtime,
		store:           cfg.Store,
		sessionManager:  cfg.SessionManager,
		usageTracker:    cfg.UsageTracker,
		traceWriter:     cfg.TraceWriter,
		clientCache:     make(map[string]*chat.Client),
		googleCache:     make(map[string]*google.Client),
	}
	s.mux.HandleFunc("/v1/responses", s.handleResponses)
	s.mux.HandleFunc("/responses", s.handleResponses)
	s.mux.HandleFunc("/v1/models", s.handleModels)
	s.mux.HandleFunc("/models", s.handleModels)
	s.mux.Handle("/console/", webui.Embedded())
	s.registerPluginRoutes()
	if cfg.Runtime != nil && cfg.Store != nil {
		apiRouter := api.NewRouter(s.store, s.runtime, s.stats, s.pluginRegistry, s)
		s.mux.Handle("/api/v1/", http.StripPrefix("/api/v1", apiRouter))
	}
	return s
}

func (s *Server) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if token := s.currentConfig().AuthToken; token != "" && !isConsoleAssetPath(request.URL.Path) {
		if !checkAuth(request, token) {
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(writer).Encode(openai.ErrorResponse{Error: openai.ErrorObject{
				Message: "未提供有效的认证令牌，请在 Authorization header 中使用 Bearer 方案",
				Type:    "authentication_error",
				Code:    "invalid_auth",
			}})
			return
		}
	}
	s.mux.ServeHTTP(writer, request)
}

func isConsoleAssetPath(path string) bool {
	return path == "/console" || strings.HasPrefix(path, "/console/")
}

func (s *Server) handleModels(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writeOpenAIError(writer, http.StatusMethodNotAllowed, openai.ErrorResponse{Error: openai.ErrorObject{
			Message: "仅支持 GET 请求",
			Type:    "invalid_request_error",
			Code:    "method_not_allowed",
		}})
		return
	}
	models := s.listModels()
	resp := struct {
		Models []map[string]any `json:"models"`
	}{
		Models: models,
	}
	writer.Header().Set("Content-Type", "application/json")
	json.NewEncoder(writer).Encode(resp)
}

func (s *Server) listModels() []map[string]any {
	var models []map[string]any

	// Get provider data from runtime (full config snapshot).
	var providerDefs map[string]config.ProviderDef
	var routes map[string]config.RouteEntry
	if s.runtime != nil {
		fullCfg := s.runtime.Current().Config
		providerDefs = fullCfg.ProviderDefs
		routes = fullCfg.Routes
	}

	for key, def := range providerDefs {
		for modelName := range def.Models {
			models = append(models, map[string]any{
				"slug":     key + "/" + modelName,
				"name":     modelName,
				"provider": key,
			})
		}
	}

	for alias, route := range routes {
		displayName := route.DisplayName
		if displayName == "" {
			// When no explicit display_name is configured for this route,
			// derive from the alias slug (e.g. "gpt-5.4" -> "GPT 5.4").
			// This avoids inheriting the underlying model's DisplayName,
			// which would cause duplicates when multiple routes point to the same model.
			displayName = slugDisplayName(alias)
		}
		models = append(models, map[string]any{
			"slug":     alias,
			"name":     displayName,
			"provider": route.Provider,
			"model":    route.Model,
		})
	}
	return models
}

func (s *Server) currentConfig() config.ServerConfig {
	if snap := s.runtimeSnapshot(); snap != nil {
		return config.ServerFromGlobalConfig(&snap.Config)
	}
	return s.serverCfg
}

func (s *Server) CurrentConfig() api.ConfigAccessor {
	return s
}

func (s *Server) AuthToken() string {
	return s.currentConfig().AuthToken
}

func (s *Server) registerPluginRoutes() {
	if s.pluginRegistry == nil {
		return
	}
	s.pluginRegistry.RegisterRoutes(func(pattern string, handler http.Handler) {
		s.mux.Handle(pattern, handler)
	})
}

func InjectWebSearchTool(tools []openai.Tool) []openai.Tool {
	for _, t := range tools {
		if t.Type == "web_search" {
			return tools
		}
	}
	if tools == nil {
		tools = make([]openai.Tool, 0, 1)
	}
	return append(tools, openai.Tool{Type: "web_search"})
}

func (s *Server) Close() error {
	s.onceClose.Do(func() {
		if s.sessionManager != nil {
			s.sessionManager.Stop()
		}
	})
	return nil
}

func computeCostWithProviderPricing(pm *provider.ProviderManager, stats *stats.SessionStats, requestModel, actualModel, providerKey string, usage stats.BillingUsage) float64 {
	if stats == nil {
		return 0
	}
	if pm != nil {
		if meta, ok := pm.ModelMetaFor(actualModel, providerKey); ok {
			freshInput := float64(usage.FreshInputTokens)
			cacheWrite := float64(usage.CacheCreationInputTokens)
			cacheRead := float64(usage.CacheReadInputTokens)
			output := float64(usage.OutputTokens)
			cost := freshInput*meta.InputPrice/1000000 +
				cacheWrite*meta.CacheWritePrice/1000000 +
				cacheRead*meta.CacheReadPrice/1000000 +
				output*meta.OutputPrice/1000000
			if cost > 0 || meta.InputPrice > 0 || meta.OutputPrice > 0 {
				return cost
			}
		}
	}
	return stats.ComputeBillingCost(requestModel, usage)
}

// slugDisplayName converts a route alias slug to a human-readable display name.
// e.g. "gpt-5.4" -> "GPT 5.4", "codex-auto-review" -> "Codex Auto Review"
func slugDisplayName(slug string) string {
	slug = strings.ReplaceAll(slug, "-", " ")
	words := strings.Fields(slug)
	for i, w := range words {
		lower := strings.ToLower(w)
		if len(lower) >= 3 && lower[:3] == "gpt" {
			words[i] = "GPT" + w[3:]
			continue
		}
		words[i] = strings.ToUpper(w[:1]) + strings.ToLower(w[1:])
	}
	return strings.Join(words, " ")
}

func checkAuth(r *http.Request, expectedToken string) bool {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return false
	}
	return strings.TrimSpace(auth[7:]) == expectedToken
}

func (s *Server) resolveModelOrFallback(modelName string) (*provider.ResolvedRoute, error) {
	if pm := s.activeProviderManager(); pm != nil {
		return pm.ResolveModel(modelName)
	}
	if s.provider != nil {
		return &provider.ResolvedRoute{
			Candidates: []provider.ProviderCandidate{{
				ProviderKey:   "default",
				UpstreamModel: modelName,
				Protocol:      "anthropic",
				Client:        s.provider,
			}},
		}, nil
	}
	return nil, fmt.Errorf("no provider manager configured for model %q", modelName)
}

func requestHasImage(input json.RawMessage) bool {
	if len(input) == 0 || string(input) == "null" {
		return false
	}
	var items []struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(input, &items); err == nil {
		for _, it := range items {
			switch it.Type {
			case "input_image", "image", "image_url":
				return true
			}
		}
		return false
	}
	return false
}

func (s *Server) filterCandidatesByInput(candidates []provider.ProviderCandidate, input json.RawMessage) ([]provider.ProviderCandidate, string) {
	pm := s.activeProviderManager()
	if pm == nil {
		return candidates, ""
	}
	hasImage := requestHasImage(input)
	if !hasImage {
		return candidates, ""
	}
	filtered := make([]provider.ProviderCandidate, 0, len(candidates))
	removedCount := 0
	for _, c := range candidates {
		meta, ok := pm.ModelMetaFor(c.UpstreamModel, c.ProviderKey)
		if !ok || !hasModalityImage(meta.InputModalities) {
			removedCount++
			logger.L().Debug("过滤掉不支持图片的提供商候选", "provider", c.ProviderKey, "model", c.UpstreamModel)
			continue
		}
		filtered = append(filtered, c)
	}
	var reason string
	if removedCount > 0 {
		reason = fmt.Sprintf("请求包含图片输入，已过滤 %d 个不支持图片的提供商候选", removedCount)
	}
	return filtered, reason
}

func hasModalityImage(modalities []string) bool {
	for _, m := range modalities {
		if m == "image" {
			return true
		}
	}
	return false
}

func newDefaultSessionManager(cfg Config) session.Manager {
	return session.NewInMemoryManager(&sessionConfigAdapter{runtime: cfg.Runtime, fallback: cfg.AppConfig}, cfg.PluginRegistry)
}

type sessionConfigAdapter struct {
	runtime  *runtime.Runtime
	fallback config.ServerConfig
}

func (a *sessionConfigAdapter) currentServerConfig() config.ServerConfig {
	if a.runtime != nil {
		snap := a.runtime.Current()
		if snap != nil {
			return config.ServerFromGlobalConfig(&snap.Config)
		}
	}
	return a.fallback
}

func (a *sessionConfigAdapter) SessionTTL() time.Duration {
	raw := a.currentServerConfig().SessionTTL
	if d, err := time.ParseDuration(raw); err == nil && d > 0 {
		return d
	}
	return 24 * time.Hour
}

func (a *sessionConfigAdapter) MaxSessions() int {
	return a.currentServerConfig().MaxSessions
}

func NewSessionConfigAdapter(cfg config.ServerConfig) session.ConfigAccessor {
	return &sessionConfigAdapter{fallback: cfg}
}

func NewSessionConfigAdapterFromRuntime(rt *runtime.Runtime, fallback config.ServerConfig) session.ConfigAccessor {
	return &sessionConfigAdapter{runtime: rt, fallback: fallback}
}
