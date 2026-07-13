package provider

import (
	"github.com/ZhiYi-R/moon-bridge/internal/config"
	"github.com/ZhiYi-R/moon-bridge/internal/service/stats"
)

// BuildProviderDefsFromConfig converts config into provider definition map.
func BuildProviderDefsFromConfig(cfg config.ProviderConfig) map[string]ProviderConfig {
	defs := make(map[string]ProviderConfig, len(cfg.Providers))
	for key, def := range cfg.Providers {
		modelNames := make([]string, 0, len(def.Models))
		for name := range def.Models {
			modelNames = append(modelNames, name)
		}
		models := make(map[string]ModelMeta, len(def.Models))
		for name, meta := range def.Models {
			models[name] = ModelMeta(meta)
		}
		defs[key] = ProviderConfig{
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
	return defs
}

// BuildModelRoutesFromConfig converts config model entries into route definitions.
func BuildModelRoutesFromConfig(cfg config.ProviderConfig) map[string]ModelRoute {
	routes := make(map[string]ModelRoute, len(cfg.Routes))
	for alias, route := range cfg.Routes {
		routes[alias] = ModelRoute{
			Provider: route.Provider,
			Name:     route.Model,
		}
	}
	return routes
}

// BuildPricingFromConfig computes a pricing map from routes and provider models.
func BuildPricingFromConfig(cfg config.ProviderConfig) map[string]stats.ModelPricing {
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
	for providerKey, def := range cfg.Providers {
		for modelName, meta := range def.Models {
			slug := providerKey + "/" + modelName
			newSlug := modelName + "(" + providerKey + ")"
			if _, exists := pricing[slug]; exists {
				if _, exists := pricing[newSlug]; !exists {
					pricing[newSlug] = pricing[slug]
				}
				continue
			}
			if meta.InputPrice > 0 || meta.OutputPrice > 0 || meta.CacheWritePrice > 0 || meta.CacheReadPrice > 0 {
				p := stats.ModelPricing{
					InputPrice:      meta.InputPrice,
					OutputPrice:     meta.OutputPrice,
					CacheWritePrice: meta.CacheWritePrice,
					CacheReadPrice:  meta.CacheReadPrice,
				}
				pricing[slug] = p
				pricing[newSlug] = p
				if _, exists := pricing[modelName]; !exists {
					pricing[modelName] = p
				}
			}
		}
	}
	return pricing
}
