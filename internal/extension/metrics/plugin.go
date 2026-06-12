// Package metrics implements a Moon Bridge extension that persists per-request
// usage metrics to a database via the foundation/db persistence layer.
//
// It implements:
//   - DBConsumer: declares the request_metrics table schema
//   - RequestCompletionHook: records each request result
//   - RouteRegistrar: exposes GET /v1/admin/metrics
//
// Configuration:
//
//	extensions:
//	  metrics:
//	    enabled: true
//	    config:
//	      default_limit: 100
//	      max_limit: 1000
package metrics

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"moonbridge/internal/config"
	"moonbridge/internal/db"
	"moonbridge/internal/extension/plugin"
)

const PluginName = "metrics"

// Config holds the metrics extension configuration.
type Config struct {
	DefaultLimit int `json:"default_limit,omitempty" yaml:"default_limit"`
	MaxLimit     int `json:"max_limit,omitempty" yaml:"max_limit"`
}

// Plugin implements the metrics extension with DBConsumer for persistence.
type Plugin struct {
	plugin.BasePlugin

	metricsStore        *Store
	pluginCfg           config.PluginConfig
	logger              *slog.Logger
	persistenceDisabled bool
}

func NewPlugin() *Plugin {
	return &Plugin{}
}

func (p *Plugin) Name() string { return PluginName }

func (p *Plugin) ConfigSpecs() []config.ExtensionConfigSpec { return ConfigSpecs() }

func ConfigSpecs() []config.ExtensionConfigSpec {
	return []config.ExtensionConfigSpec{{
		Name: PluginName,
		Scopes: []config.ExtensionScope{
			config.ExtensionScopeGlobal,
		},
		DefaultEnabled: true,
		Factory:        func() any { return &Config{} },
	}}
}

func (p *Plugin) Init(ctx plugin.PluginContext) error {
	p.pluginCfg = config.PluginFromGlobalConfig(&ctx.AppConfig)
	p.logger = ctx.Logger
	return nil
}

func (p *Plugin) Shutdown() error { return nil }

func (p *Plugin) EnabledForModel(string) bool {
	// Metrics is enabled only when the DB store has been initialized.
	// Without a store, there's nothing to record to.
	return p.metricsStore != nil
}

// --- DBConsumer ---

func (p *Plugin) DBConsumer() db.Consumer {
	// DBConsumer registration is always allowed. The DB registry handles
	// availability — without a DB provider, BindStore is never called and
	// metricsStore remains nil, so OnRequestCompleted silently drops records.
	return p
}

func (p *Plugin) Tables() []db.TableSpec {
	return []db.TableSpec{MetricsTable()}
}

func (p *Plugin) BindStore(s db.Store) error {
	p.metricsStore = NewStore(s)
	if p.logger != nil {
		p.logger.Info("指标持久化已启用")
	}
	return nil
}

func (p *Plugin) DisablePersistence(reason error) {
	p.persistenceDisabled = true
	p.metricsStore = nil
	if p.logger != nil {
		p.logger.Error("指标持久化已禁用", "error", reason)
	}
}

// --- RequestCompletionHook ---

func (p *Plugin) OnRequestCompleted(_ *plugin.RequestContext, result plugin.RequestResult) {
	if p.metricsStore == nil {
		return
	}
	r := Record{
		Timestamp:               time.Now(),
		Model:                   result.Model,
		ActualModel:             result.ActualModel,
		ProviderKey:             result.ProviderKey,
		InputTokens:             int64(result.InputTokens),
		OutputTokens:            int64(result.OutputTokens),
		CacheCreation:           int64(result.CacheCreation),
		CacheRead:               int64(result.CacheRead),
		Protocol:                result.Usage.Protocol,
		UsageSource:             result.Usage.UsageSource,
		RawInputTokens:          int64(result.Usage.RawInputTokens),
		RawOutputTokens:         int64(result.Usage.RawOutputTokens),
		RawCacheCreation:        int64(result.Usage.RawCacheCreation),
		RawCacheRead:            int64(result.Usage.RawCacheRead),
		NormalizedInputTokens:   int64(result.Usage.NormalizedInputTokens),
		NormalizedOutputTokens:  int64(result.Usage.NormalizedOutputTokens),
		NormalizedCacheCreation: int64(result.Usage.NormalizedCacheCreation),
		NormalizedCacheRead:     int64(result.Usage.NormalizedCacheRead),
		RawUsageJSON:            string(result.Usage.RawUsageJSON),
		Cost:                    result.Cost,
		ResponseTime:            result.Duration,
		Status:                  result.Status,
		ErrorMessage:            result.ErrorMessage,
	}
	if err := p.metricsStore.Record(r); err != nil && p.logger != nil {
		p.logger.Error("写入指标记录失败", "error", err)
	}
}

// --- RouteRegistrar ---

func (p *Plugin) RegisterRoutes(register func(pattern string, handler http.Handler)) {
	if p.metricsStore == nil {
		return
	}
	register("GET /v1/admin/metrics", http.HandlerFunc(p.handleQuery))
}

func (p *Plugin) handleQuery(w http.ResponseWriter, r *http.Request) {
	if p.metricsStore == nil {
		http.Error(w, `{"error":"metrics disabled"}`, http.StatusNotFound)
		return
	}

	// Decode typed config from PluginConfig directly.
	var cfg *Config
	if setting, ok := p.pluginCfg.Extensions[PluginName]; ok && len(setting.RawConfig) > 0 {
		data, err := json.Marshal(setting.RawConfig)
		if err == nil {
			_ = json.Unmarshal(data, &cfg)
		}
	}
	defaultLimit := 100
	maxLimit := 1000
	if cfg != nil {
		if cfg.DefaultLimit > 0 {
			defaultLimit = cfg.DefaultLimit
		}
		if cfg.MaxLimit > 0 {
			maxLimit = cfg.MaxLimit
		}
	}

	limit := defaultLimit
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= maxLimit {
			limit = parsed
		}
	}

	opts := QueryOptions{Limit: limit, OrderAsc: false}
	if model := r.URL.Query().Get("model"); model != "" {
		opts.Model = model
	}
	if status := r.URL.Query().Get("status"); status != "" {
		opts.Status = status
	}
	if since := r.URL.Query().Get("since"); since != "" {
		if t, err := time.Parse(time.RFC3339Nano, since); err == nil {
			opts.Since = t
		}
	}
	if until := r.URL.Query().Get("until"); until != "" {
		if t, err := time.Parse(time.RFC3339Nano, until); err == nil {
			opts.Until = t
		}
	}
	if offset := r.URL.Query().Get("offset"); offset != "" {
		if parsed, err := strconv.Atoi(offset); err == nil && parsed >= 0 {
			opts.Offset = parsed
		}
	}
	if order := r.URL.Query().Get("order"); order == "asc" {
		opts.OrderAsc = true
	}

	records, err := p.metricsStore.Query(opts)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"records": records,
		"count":   len(records),
	})
}

func pluginExtensionEnabled(pluginCfg config.PluginConfig, name string) bool {
	if setting, ok := pluginCfg.Extensions[name]; ok && setting.Enabled != nil {
		return *setting.Enabled
	}
	return false
}

var (
	_ plugin.Plugin                = (*Plugin)(nil)
	_ plugin.ConfigSpecProvider    = (*Plugin)(nil)
	_ plugin.RequestCompletionHook = (*Plugin)(nil)
	_ plugin.RouteRegistrar        = (*Plugin)(nil)
	_ plugin.DBConsumer            = (*Plugin)(nil)
)
