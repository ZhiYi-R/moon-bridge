//go:build js && wasm

package main

import (
	"context"
	"os"

	"github.com/ZhiYi-R/moon-bridge/internal/service/app"

	"log/slog"
	"github.com/ZhiYi-R/moon-bridge/internal/config"
	"github.com/ZhiYi-R/moon-bridge/internal/db"
	"github.com/ZhiYi-R/moon-bridge/internal/logger"
	"github.com/ZhiYi-R/moon-bridge/internal/protocol/anthropic"
	"github.com/ZhiYi-R/moon-bridge/internal/service/provider"
	"github.com/ZhiYi-R/moon-bridge/internal/service/runtime"
	"github.com/ZhiYi-R/moon-bridge/internal/service/server"
	"github.com/ZhiYi-R/moon-bridge/internal/service/stats"

	"database/sql"
	"github.com/syumai/workers"
	"github.com/syumai/workers/cloudflare"

	"github.com/syumai/workers/cloudflare/d1"
)

func main() {
	// Config is injected as a single Wrangler secret containing the full
	// config.yml content. Set with:
	//   wrangler secret put MOONBRIDGE_CONFIG < config.yml
	rawConfig := cloudflare.Getenv("MOONBRIDGE_CONFIG")
	if rawConfig == "" {
		slog.Error("MOONBRIDGE_CONFIG environment variable is not set")
		os.Exit(1)
	}

	cfg, err := config.LoadFromYAMLWithOptions([]byte(rawConfig), config.LoadOptions{
		ExtensionSpecs: app.BuiltinExtensions().ConfigSpecs(),
	})
	if err != nil {
		slog.Error("parse config", "error", err)
		os.Exit(1)
	}

	if cfg.AuthToken == "" && !isDevEnv() {
		slog.Error("Worker 生产环境必须配置认证：请在 server.auth_token 中设置 Bearer token，" +
			"或通过 wrangler secret put MOONBRIDGE_CONFIG 注入包含 auth_token 的配置")
		os.Exit(1)
	}

	// Build provider infrastructure.
	providerDefs := buildProviderDefs(cfg)
	modelRoutes := buildModelRoutes(cfg)
	providerMgr, err := provider.NewProviderManager(providerDefs, modelRoutes)
	if err != nil {
		slog.Error("init provider manager", "error", err)
		os.Exit(1)
	}

	// Resolve a fallback provider client.
	defaultClient := resolveDefaultClient(providerMgr)

	sessionStats := stats.NewSessionStats()
	// Create plugin catalog with D1 DB injection (when configured).
	pricing := buildPricing(cfg)
	if len(pricing) > 0 {
		sessionStats.SetPricing(pricing)
	}
	rt := runtime.NewRuntime(cfg, providerMgr, pricing)
	cat := app.BuiltinExtensionCatalog{}
	if d1Cfg := cfg.ExtensionRawConfig("db_d1", ""); len(d1Cfg) > 0 {
		if binding, ok := d1Cfg["binding"].(string); ok && binding != "" {
			if cfg.ExtensionEnabled("db_d1", "") {
				cat.Opts.D1DB = func() *sql.DB {
					c, err := d1.OpenConnector(binding)
					if err != nil {
						slog.Error("D1 connector", "error", err)
						os.Exit(1)
					}
					return sql.OpenDB(c)
				}()
			}
		}
	}
	plugins := cat.NewRegistry(slog.Default(), cfg)
	plugins.SetCurrentConfigProvider(func() config.Config {
		if rt.Current() != nil {
			return rt.Current().Config
		}
		return cfg
	})
	if err := plugins.InitAll(&cfg); err != nil {
		slog.Error("init plugins", "error", err)
		os.Exit(1)
	}

	defer plugins.ShutdownAll()

	// Wire plugin LogConsumer into the slog consume pipeline.
	logger.AddConsumeFunc(func(entries []logger.LogEntry) []logger.LogEntry {
		return plugins.ConsumeGlobalLog(entries)
	})

	// Initialize persistence layer (db.Registry).
	ctx := context.Background()
	dbRegistry := db.NewRegistry(slog.Default())
	dbProviders := plugins.DBProviders()
	providers := make([]db.Provider, 0, len(dbProviders))
	for _, p := range dbProviders {
		if prov := p.DBProvider(); prov != nil {
			dbRegistry.RegisterProvider(prov)
			providers = append(providers, prov)
		}
	}
	for _, c := range plugins.DBConsumers() {
		if cons := c.DBConsumer(); cons != nil {
			dbRegistry.RegisterConsumer(cons)
		}
	}
	activePersistenceProvider := app.ResolvePersistenceActiveProvider(cfg.Persistence.ActiveProvider, providers)
	if err := dbRegistry.Init(ctx, activePersistenceProvider); err != nil {
		slog.Error("init persistence", "error", err)
		os.Exit(1)
	}
	defer dbRegistry.Shutdown()

	handler := server.New(server.Config{
		Provider:       provider.NewAnthropicClientAdapter(defaultClient),
		ProviderMgr:    providerMgr,
		Stats:          sessionStats,
		PluginRegistry: plugins,
		AppConfig:      config.ServerFromGlobalConfig(&cfg),
		Runtime:        rt,
	})

	workers.Serve(handler)
}

func buildProviderDefs(cfg config.Config) map[string]provider.ProviderConfig {
	defs := make(map[string]provider.ProviderConfig, len(cfg.ProviderDefs))
	for key, def := range cfg.ProviderDefs {
		modelNames := make([]string, 0, len(def.Models))
		for name := range def.Models {
			modelNames = append(modelNames, name)
		}
		defs[key] = provider.ProviderConfig{
			BaseURL:    def.BaseURL,
			APIKey:     def.APIKey,
			Version:    def.Version,
			UserAgent:  def.UserAgent,
			Protocol:   def.Protocol,
			ModelNames: modelNames,
		}
	}
	return defs
}

func buildModelRoutes(cfg config.Config) map[string]provider.ModelRoute {
	routes := make(map[string]provider.ModelRoute, len(cfg.Routes))
	for alias, route := range cfg.Routes {
		routes[alias] = provider.ModelRoute{
			Provider: route.Provider,
			Name:     route.Model,
		}
	}
	return routes
}

func buildPricing(cfg config.Config) map[string]stats.ModelPricing {
	pricing := make(map[string]stats.ModelPricing)
	for alias, route := range cfg.Routes {
		if route.InputPrice > 0 || route.OutputPrice > 0 || route.CacheWritePrice > 0 || route.CacheReadPrice > 0 {
			pricing[alias] = stats.ModelPricing{
				InputPrice:      route.InputPrice,
				OutputPrice:     route.OutputPrice,
				CacheWritePrice: route.CacheWritePrice,
				CacheReadPrice:  route.CacheReadPrice,
			}
		}
	}
	for providerKey, def := range cfg.ProviderDefs {
		for modelName, meta := range def.Models {
			slug := providerKey + "/" + modelName
			newSlug := modelName + "(" + providerKey + ")"
			if _, exists := pricing[slug]; !exists && (meta.InputPrice > 0 || meta.OutputPrice > 0) {
				p := stats.ModelPricing{
					InputPrice:      meta.InputPrice,
					OutputPrice:     meta.OutputPrice,
					CacheWritePrice: meta.CacheWritePrice,
					CacheReadPrice:  meta.CacheReadPrice,
				}
				pricing[slug] = p
				pricing[newSlug] = p
			}
		}
	}
	return pricing
}

func resolveDefaultClient(pm *provider.ProviderManager) *anthropic.Client {
	client, err := pm.ClientForKey("default")
	if err != nil {
		for _, key := range pm.ProviderKeys() {
			c, err := pm.ClientForKey(key)
			if err == nil {
				if acc, ok := c.(provider.AnthropicClientAccessor); ok {
					return acc.AnthropicClient()
				}
			}
		}
		return nil
	}
	if acc, ok := client.(provider.AnthropicClientAccessor); ok {
		return acc.AnthropicClient()
	}
	return nil
}

func isDevEnv() bool {
	return cloudflare.Getenv("WORKER_ENV") == "development"
}
