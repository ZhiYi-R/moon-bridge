package api

import (
	"net/http"

	"moonbridge/internal/extension/plugin"
	"moonbridge/internal/service/runtime"
	"moonbridge/internal/service/stats"
	"moonbridge/internal/service/store"
)

// ConfigStore subset used by the API handlers.
type ConfigStore interface {
	store.ConfigStore
}

// Router holds dependencies for all API handlers.
type Router struct {
	store    store.ConfigStore
	runtime  *runtime.Runtime
	stats    *stats.SessionStats
	registry *plugin.Registry
	server   interface {
		ListSessions() []SessionInfo
		CurrentConfig() ConfigAccessor
	}
}

// configAccessor allows reading the current auth token.
type ConfigAccessor interface {
	AuthToken() string
}

// SessionInfo is a public representation of an active session.
type SessionInfo struct {
	Key       string `json:"key"`
	Model     string `json:"model,omitempty"`
	CreatedAt string `json:"created_at"`
	LastUsed  string `json:"last_used"`
}

// NewRouter creates an HTTP handler that serves the /api/v1 endpoints.
func NewRouter(cfg ConfigStore, rt *runtime.Runtime, st *stats.SessionStats, reg *plugin.Registry, srv interface {
	ListSessions() []SessionInfo
	CurrentConfig() ConfigAccessor
}) http.Handler {
	r := &Router{
		store:    cfg,
		runtime:  rt,
		stats:    st,
		registry: reg,
		server:   srv,
	}

	mux := http.NewServeMux()

	// Auth middleware for all API routes.
	authMW := AuthMiddleware(func() string {
		return r.server.CurrentConfig().AuthToken()
	}, func() bool { return r.store != nil })

	// Register all routes using Go 1.22+ pattern matching.
	registerRoutes(mux, r)

	return authMW(mux)
}

// registerRoutes registers all API endpoints with the mux.
func registerRoutes(mux *http.ServeMux, r *Router) {
	// Provider endpoints
	mux.HandleFunc("GET /providers", r.handleListProviders)
	mux.HandleFunc("GET /providers/{key}", r.handleGetProvider)
	mux.HandleFunc("PUT /providers/{key}", r.handlePutProvider)
	mux.HandleFunc("PATCH /providers/{key}", r.handlePatchProvider)
	mux.HandleFunc("DELETE /providers/{key}", r.handleDeleteProvider)
	mux.HandleFunc("POST /providers/{key}/test", r.handleTestProvider)

	// Offer endpoints
	mux.HandleFunc("POST /providers/{key}/offers", r.handleCreateOffer)
	mux.HandleFunc("PATCH /providers/{key}/offers/{model}", r.handleUpdateOffer)
	mux.HandleFunc("DELETE /providers/{key}/offers/{model}", r.handleDeleteOffer)

	// Model endpoints
	mux.HandleFunc("GET /models", r.handleListModels)
	mux.HandleFunc("GET /models/{slug}", r.handleGetModel)
	mux.HandleFunc("PUT /models/{slug}", r.handlePutModel)
	mux.HandleFunc("DELETE /models/{slug}", r.handleDeleteModel)

	// Route endpoints
	mux.HandleFunc("GET /routes", r.handleListRoutes)
	mux.HandleFunc("GET /routes/{alias}", r.handleGetRoute)
	mux.HandleFunc("PUT /routes/{alias}", r.handlePutRoute)
	mux.HandleFunc("DELETE /routes/{alias}", r.handleDeleteRoute)

	// Settings endpoints
	mux.HandleFunc("GET /defaults", r.handleGetDefaults)
	mux.HandleFunc("PUT /defaults", r.handlePutDefaults)
	mux.HandleFunc("GET /web-search", r.handleGetWebSearch)
	mux.HandleFunc("PUT /web-search", r.handlePutWebSearch)
	mux.HandleFunc("GET /extensions", r.handleListExtensions)
	mux.HandleFunc("GET /extensions/{name}", r.handleGetExtension)
	mux.HandleFunc("PUT /extensions/{name}", r.handlePutExtension)

	// Config endpoints
	mux.HandleFunc("GET /config/effective", r.handleGetConfigEffective)
	mux.HandleFunc("GET /config/export", r.handleGetConfigExport)
	mux.HandleFunc("POST /config/import", r.handlePostConfigImport)
	mux.HandleFunc("POST /config/validate", r.handlePostConfigValidate)

	// Changes endpoints
	mux.HandleFunc("GET /changes", r.handleListChanges)
	mux.HandleFunc("POST /changes/apply", r.handlePostChangesApply)
	mux.HandleFunc("POST /changes/discard", r.handlePostChangesDiscard)

	// Status / Stats / Logs / Version
	mux.HandleFunc("GET /status", r.handleGetStatus)
	mux.HandleFunc("GET /status/providers", r.handleGetStatusProviders)
	mux.HandleFunc("GET /sessions", r.handleGetSessions)
	mux.HandleFunc("GET /stats", r.handleGetStats)
	mux.HandleFunc("GET /stats/summary", r.handleGetStatsSummary)
	mux.HandleFunc("GET /logs", r.handleGetLogs)
	mux.HandleFunc("GET /version", r.handleGetVersion)
}
