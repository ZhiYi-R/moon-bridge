//go:build js && wasm

package main

import (
	"log/slog"
	"os"

	deepseekv4 "moonbridge/internal/extension/deepseek_v4"
	"moonbridge/internal/extension/plugin"
	"moonbridge/internal/extension/pluginhooks"
	"moonbridge/internal/foundation/config"
	"moonbridge/internal/foundation/logger"
	"moonbridge/internal/protocol/anthropic"
	"moonbridge/internal/protocol/bridge"
	"moonbridge/internal/protocol/cache"
	"moonbridge/internal/service/provider"
	"moonbridge/internal/service/server"
	"moonbridge/internal/service/stats"

	"github.com/syumai/workers"
)

func main() {
	// Config is injected as a single Wrangler secret containing the full
	// config.yml content. Set with:
	//   wrangler secret put MOONBRIDGE_CONFIG < config.yml
	rawConfig := os.Getenv("MOONBRIDGE_CONFIG")
	if rawConfig == "" {
		slog.Error("MOONBRIDGE_CONFIG environment variable is not set")
		os.Exit(1)
	}

	cfg, err := config.LoadFromYAML([]byte(rawConfig))
	if err != nil {
		slog.Error("parse config", "error", err)
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
	// Set per-model pricing.
	pricing := buildPricing(cfg)
	if len(pricing) > 0 {
		sessionStats.SetPricing(pricing)
	}

	// Register plugins.
	reg := plugin.NewRegistry(logger.L())
	reg.Register(deepseekv4.NewPlugin(cfg.DeepSeekV4ForModel))
	if err := reg.InitAll(&cfg); err != nil {
		slog.Error("init plugins", "error", err)
		os.Exit(1)
	}

	logger.SetConsumeFunc(func(entries []logger.LogEntry) []logger.LogEntry {
		return reg.ConsumeLog(&plugin.RequestContext{ModelAlias: "*"}, entries)
	})

	handler := server.New(server.Config{
		Bridge:      bridge.New(cfg, cache.NewMemoryRegistry(), pluginhooks.PluginHooksFromRegistry(reg)),
		Provider:    defaultClient,
		ProviderMgr: providerMgr,
		Stats:       sessionStats,
		AppConfig:   cfg,
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
	// Also index by provider/model and model(provider) slug.
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
				return c
			}
		}
		return nil
	}
	return client
}
