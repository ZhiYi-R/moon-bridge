package api

import (
	"net/http"
	"sort"
	"time"

	"github.com/ZhiYi-R/moon-bridge/internal/extension/plugin"
	"github.com/ZhiYi-R/moon-bridge/internal/service/stats"
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

// GET /stats/usage
//
// Optional query parameters:
//   - range: session|24h|7d|30d|all (default "session" = in-memory process stats)
//   - since,until: RFC3339 timestamps for a custom window (cross-session)
//
// When a cross-session window is requested and a persistent usage source is
// available, usage is aggregated from persisted metrics; otherwise it falls
// back to the in-memory per-process session stats.
func (r *Router) handleGetStatsUsage(w http.ResponseWriter, req *http.Request) {
	q := req.URL.Query()
	since, until, crossSession := usageWindow(q.Get("range"), q.Get("since"), q.Get("until"))

	if crossSession && r.registry != nil {
		if source := r.registry.UsageSource(); source != nil {
			if agg, err := source.AggregateUsage(plugin.UsageQuery{Since: since, Until: until}); err == nil {
				respondJSON(w, http.StatusOK, usageStatsFromAggregate(agg))
				return
			}
		}
	}

	if r.stats == nil {
		respondJSON(w, http.StatusOK, emptyUsageStatsResponse())
		return
	}
	respondJSON(w, http.StatusOK, usageStatsFromSummary(r.stats.Summary()))
}

// usageWindow resolves the requested range into a [since, until) window. The
// bool result reports whether a cross-session (persisted) query was requested;
// when false the caller should use the in-memory session stats.
func usageWindow(rangeParam, sinceParam, untilParam string) (time.Time, time.Time, bool) {
	now := time.Now()
	if sinceParam != "" || untilParam != "" {
		since, _ := time.Parse(time.RFC3339, sinceParam)
		until, err := time.Parse(time.RFC3339, untilParam)
		if err != nil {
			until = now
		}
		return since, until, true
	}
	switch rangeParam {
	case "24h":
		return now.Add(-24 * time.Hour), now, true
	case "7d":
		return now.Add(-7 * 24 * time.Hour), now, true
	case "30d":
		return now.Add(-30 * 24 * time.Hour), now, true
	case "all":
		return time.Time{}, now, true
	default:
		return time.Time{}, time.Time{}, false
	}
}

type usageStatsResponse struct {
	Totals  usageTotals     `json:"totals"`
	ByModel []usageModelRow `json:"by_model"`
}

type usageTotals struct {
	Requests       int64   `json:"requests"`
	InputTokens    int64   `json:"input_tokens"`
	OutputTokens   int64   `json:"output_tokens"`
	CacheCreation  int64   `json:"cache_creation"`
	CacheRead      int64   `json:"cache_read"`
	CacheHitRate   float64 `json:"cache_hit_rate"`
	CacheWriteRate float64 `json:"cache_write_rate"`
	CacheRWRatio   float64 `json:"cache_rw_ratio"`
	TotalCost      float64 `json:"total_cost"`
	Duration       string  `json:"duration"`
}

type usageModelRow struct {
	Model             string  `json:"model"`
	ActualModel       string  `json:"actual_model"`
	Requests          int64   `json:"requests"`
	InputTokens       int64   `json:"input_tokens"`
	OutputTokens      int64   `json:"output_tokens"`
	CacheCreation     int64   `json:"cache_creation"`
	CacheRead         int64   `json:"cache_read"`
	CacheHitRate      float64 `json:"cache_hit_rate"`
	Cost              float64 `json:"cost"`
	AvgCostPerMTokens float64 `json:"avg_cost_per_mtoken"`
}

func emptyUsageStatsResponse() usageStatsResponse {
	return usageStatsResponse{
		Totals: usageTotals{
			Duration: "0s",
		},
		ByModel: []usageModelRow{},
	}
}

func usageStatsFromSummary(summary stats.Summary) usageStatsResponse {
	rows := make([]usageModelRow, 0, len(summary.ByModel))
	for model, modelStats := range summary.ByModel {
		if modelStats == nil {
			continue
		}
		rows = append(rows, usageModelRow{
			Model:             model,
			ActualModel:       summary.ActualModelNames[model],
			Requests:          modelStats.Requests,
			InputTokens:       modelStats.InputTokens,
			OutputTokens:      modelStats.OutputTokens,
			CacheCreation:     modelStats.CacheCreation,
			CacheRead:         modelStats.CacheRead,
			CacheHitRate:      rate(modelStats.CacheRead, modelStats.InputTokens),
			Cost:              modelStats.Cost,
			AvgCostPerMTokens: costPerMTokens(modelStats.Cost, modelStats.InputTokens+modelStats.OutputTokens),
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Cost == rows[j].Cost {
			return rows[i].Model < rows[j].Model
		}
		return rows[i].Cost > rows[j].Cost
	})

	return usageStatsResponse{
		Totals: usageTotals{
			Requests:       summary.Requests,
			InputTokens:    summary.InputTokens,
			OutputTokens:   summary.OutputTokens,
			CacheCreation:  summary.CacheCreation,
			CacheRead:      summary.CacheRead,
			CacheHitRate:   summary.CacheHitRate,
			CacheWriteRate: summary.CacheWriteRate,
			CacheRWRatio:   ratio(summary.CacheRead, summary.CacheCreation),
			TotalCost:      summary.TotalCost,
			Duration:       summary.Duration.String(),
		},
		ByModel: rows,
	}
}

// usageStatsFromAggregate converts a cross-session usage aggregate (from
// persisted metrics) into the same response shape used for session stats.
func usageStatsFromAggregate(agg plugin.UsageAggregate) usageStatsResponse {
	rows := make([]usageModelRow, 0, len(agg.ByModel))
	for _, m := range agg.ByModel {
		rows = append(rows, usageModelRow{
			Model:             m.Model,
			ActualModel:       m.ActualModel,
			Requests:          m.Requests,
			InputTokens:       m.InputTokens,
			OutputTokens:      m.OutputTokens,
			CacheCreation:     m.CacheCreation,
			CacheRead:         m.CacheRead,
			CacheHitRate:      rate(m.CacheRead, m.InputTokens),
			Cost:              m.Cost,
			AvgCostPerMTokens: costPerMTokens(m.Cost, m.InputTokens+m.OutputTokens),
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Cost == rows[j].Cost {
			return rows[i].Model < rows[j].Model
		}
		return rows[i].Cost > rows[j].Cost
	})

	var duration time.Duration
	if !agg.Earliest.IsZero() && agg.Latest.After(agg.Earliest) {
		duration = agg.Latest.Sub(agg.Earliest)
	}

	return usageStatsResponse{
		Totals: usageTotals{
			Requests:       agg.Requests,
			InputTokens:    agg.InputTokens,
			OutputTokens:   agg.OutputTokens,
			CacheCreation:  agg.CacheCreation,
			CacheRead:      agg.CacheRead,
			CacheHitRate:   rate(agg.CacheRead, agg.InputTokens),
			CacheWriteRate: rate(agg.CacheCreation, agg.InputTokens),
			CacheRWRatio:   ratio(agg.CacheRead, agg.CacheCreation),
			TotalCost:      agg.Cost,
			Duration:       duration.String(),
		},
		ByModel: rows,
	}
}

func rate(numerator, denominator int64) float64 {
	if denominator <= 0 {
		return 0
	}
	return float64(numerator) / float64(denominator) * 100
}

func ratio(numerator, denominator int64) float64 {
	if denominator <= 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}

func costPerMTokens(cost float64, tokens int64) float64 {
	if tokens <= 0 {
		return 0
	}
	return cost / float64(tokens) * 1_000_000
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
