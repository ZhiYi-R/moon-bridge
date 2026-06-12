package api

import (
	"net/http"
	"sort"
	"time"
)

// ---- Status ----

// GET /status
func (r *Router) handleGetStatus(w http.ResponseWriter, req *http.Request) {
	cfg := r.runtime.Current()

	providerCount := len(cfg.Config.ProviderDefs)
	routeCount := len(cfg.Config.Routes)

	resp := map[string]any{
		"uptime":         "N/A", // StartTime not tracked on Runtime
		"version":        version,
		"mode":           string(cfg.Config.Mode),
		"provider_count": providerCount,
		"route_count":    routeCount,
		"addr":           cfg.Config.Addr,
		"timestamp":      time.Now().UTC().Format(time.RFC3339),
	}

	respondJSON(w, http.StatusOK, resp)
}

// GET /status/providers
func (r *Router) handleGetStatusProviders(w http.ResponseWriter, req *http.Request) {
	cfg := r.runtime.Current()

	type providerStatus struct {
		Key          string `json:"key"`
		Protocol     string `json:"protocol"`
		BaseURL      string `json:"base_url"`
		OfferCount   int    `json:"offer_count"`
		HealthStatus string `json:"health_status"`
	}

	providers := make([]providerStatus, 0, len(cfg.Config.ProviderDefs))
	for key, def := range cfg.Config.ProviderDefs {
		providers = append(providers, providerStatus{
			Key:          key,
			Protocol:     def.Protocol,
			BaseURL:      def.BaseURL,
			OfferCount:   len(def.Offers),
			HealthStatus: "unknown",
		})
	}

	sort.Slice(providers, func(i, j int) bool {
		return providers[i].Key < providers[j].Key
	})

	respondJSON(w, http.StatusOK, providers)
}

// GET /sessions
func (r *Router) handleGetSessions(w http.ResponseWriter, req *http.Request) {
	sessions := r.server.ListSessions()
	if sessions == nil {
		respondJSON(w, http.StatusOK, []any{})
		return
	}

	type sessionItem struct {
		Key       string `json:"key"`
		Model     string `json:"model,omitempty"`
		CreatedAt string `json:"created_at"`
		LastUsed  string `json:"last_used"`
	}

	items := make([]sessionItem, 0, len(sessions))
	for _, s := range sessions {
		items = append(items, sessionItem{
			Key:       maskSessionKey(s.Key),
			Model:     s.Model,
			CreatedAt: s.CreatedAt,
			LastUsed:  s.LastUsed,
		})
	}

	respondJSON(w, http.StatusOK, items)
}

// GET /stats
func (r *Router) handleGetStats(w http.ResponseWriter, req *http.Request) {
	if r.stats == nil {
		respondJSON(w, http.StatusOK, map[string]string{"message": "统计信息不可用"})
		return
	}

	summary := r.stats.Summary()
	respondJSON(w, http.StatusOK, summary)
}

// GET /stats/summary
func (r *Router) handleGetStatsSummary(w http.ResponseWriter, req *http.Request) {
	if r.stats == nil {
		respondJSON(w, http.StatusOK, map[string]string{"message": "统计信息不可用"})
		return
	}

	summary := r.stats.Summary()
	respondJSON(w, http.StatusOK, map[string]any{
		"requests":       summary.Requests,
		"input_tokens":   summary.InputTokens,
		"output_tokens":  summary.OutputTokens,
		"cache_hit_rate": summary.CacheHitRate,
		"total_cost":     summary.TotalCost,
		"duration":       summary.Duration.String(),
	})
}

// GET /logs
func (r *Router) handleGetLogs(w http.ResponseWriter, req *http.Request) {
	// The current logger package doesn't expose a ring buffer for recent entries.
	// This endpoint returns a placeholder until a log ring buffer is implemented.
	respondJSON(w, http.StatusOK, []any{})
}

// GET /version
func (r *Router) handleGetVersion(w http.ResponseWriter, req *http.Request) {
	respondJSON(w, http.StatusOK, map[string]any{
		"version":    version,
		"build_time": buildTime,
		"go_version": goVersion,
	})
}

// ---- version variables (set via ldflags) ----
var (
	version   = "dev"
	buildTime = "unknown"
	goVersion = "unknown"
)

// ---- helpers ----

// compile-time check that *configWrapper satisfies ConfigAccessor.
var _ ConfigAccessor = (*configWrapper)(nil)

type configWrapper struct {
	authToken string
}

func (c *configWrapper) AuthToken() string {
	return c.authToken
}
