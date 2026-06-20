package config

import (
	"gopkg.in/yaml.v3"
)

// ToFileConfig converts a runtime Config back into FileConfig form,
// suitable for serialization or DB storage.
func (cfg Config) ToFileConfig() FileConfig {
	fc := FileConfig{
		Mode: string(cfg.Mode),
		Trace: TraceFileConfig{
			Enabled: cfg.TraceRequests,
		},
		Log: LogFileConfig{
			Level:  cfg.LogLevel,
			Format: cfg.LogFormat,
		},
		Server: ServerFileConfig{
			Addr:        cfg.Addr,
			AuthToken:   cfg.AuthToken,
			MaxSessions: cfg.MaxSessions,
			SessionTTL:  cfg.SessionTTL,
		},
		Defaults: DefaultsFileConfig{
			Model:        cfg.Defaults.Model,
			MaxTokens:    cfg.Defaults.MaxTokens,
			SystemPrompt: cfg.Defaults.SystemPrompt,
		},
		WebSearch: WebSearchFileConfig{
			Support:         string(cfg.WebSearchSupport),
			MaxUses:         cfg.WebSearchMaxUses,
			TavilyAPIKey:    cfg.TavilyAPIKey,
			FirecrawlAPIKey: cfg.FirecrawlAPIKey,
			SearchMaxRounds: cfg.SearchMaxRounds,
			Extra:           cloneAnyMap(cfg.WebSearchExtra),
		},
		Cache:       toCacheFileConfig(cfg.Cache),
		Persistence: PersistenceFileConfig{ActiveProvider: cfg.Persistence.ActiveProvider},
		EgressProxy: cfg.EgressProxy,
		Proxy: ProxyFileConfig{
			Response: ProxyTargetFileConfig{
				BaseURL: cfg.ResponseProxy.ProviderBaseURL,
				APIKey:  cfg.ResponseProxy.ProviderAPIKey,
				Model:   cfg.ResponseProxy.Model,
			},
			Anthropic: ProxyTargetFileConfig{
				BaseURL: cfg.AnthropicProxy.ProviderBaseURL,
				APIKey:  cfg.AnthropicProxy.ProviderAPIKey,
				Model:   cfg.AnthropicProxy.Model,
				Version: cfg.AnthropicProxy.ProviderVersion,
			},
		},
	}

	// Defaults.
	if len(cfg.Defaults.Model) > 0 {
		fc.Defaults.Model = cfg.Defaults.Model
	}

	// Models.
	if len(cfg.Models) > 0 {
		fc.Models = make(map[string]ModelDefFileConfig, len(cfg.Models))
		for slug, def := range cfg.Models {
			fc.Models[slug] = toModelDefFileConfig(def)
		}
	}

	// Providers.
	if len(cfg.ProviderDefs) > 0 {
		fc.Providers = make(map[string]ProviderDefFileConfig, len(cfg.ProviderDefs))
		for key, def := range cfg.ProviderDefs {
			fc.Providers[key] = toProviderDefFileConfig(def)
		}
	}

	// Routes.
	if len(cfg.Routes) > 0 {
		fc.Routes = make(map[string]RouteFileConfig, len(cfg.Routes))
		for alias, entry := range cfg.Routes {
			fc.Routes[alias] = toRouteFileConfig(entry)
		}
	}

	// Extensions.
	if len(cfg.Extensions) > 0 {
		fc.Extensions = make(map[string]ExtensionFileConfig, len(cfg.Extensions))
		for name, settings := range cfg.Extensions {
			fc.Extensions[name] = toExtensionFileConfig(settings)
		}
	}

	return fc
}

// MarshalYAML serializes the FileConfig to YAML bytes.
func (fc FileConfig) MarshalYAML() ([]byte, error) {
	return yaml.Marshal(fc)
}

// --- helper conversions ---

func toModelDefFileConfig(def ModelDef) ModelDefFileConfig {
	var reasoningPresets []ReasoningLevelPresetFileConfig
	for _, p := range def.SupportedReasoningLevels {
		reasoningPresets = append(reasoningPresets, ReasoningLevelPresetFileConfig{
			Effort:      p.Effort,
			Description: p.Description,
		})
	}

	m := ModelDefFileConfig{
		ContextWindow:            def.ContextWindow,
		MaxOutputTokens:          def.MaxOutputTokens,
		DisplayName:              def.DisplayName,
		Description:              def.Description,
		BaseInstructions:         def.BaseInstructions,
		DefaultReasoningLevel:    def.DefaultReasoningLevel,
		SupportedReasoningLevels: reasoningPresets,
		DefaultReasoningSummary:  def.DefaultReasoningSummary,
		InputModalities:          def.InputModalities,
		WebSearch:                toWebSearchFileConfig(def.WebSearch),
	}

	m.SupportsReasoning = boolPtr(def.SupportsReasoning)
	if def.SupportsReasoningSummaries {
		m.SupportsReasoningSummaries = boolPtr(true)
	}
	if def.SupportsImageDetailOriginal {
		m.SupportsImageDetailOriginal = boolPtr(true)
	}

	if len(def.Extensions) > 0 {
		m.Extensions = make(map[string]ExtensionFileConfig, len(def.Extensions))
		for name, s := range def.Extensions {
			m.Extensions[name] = toExtensionFileConfig(s)
		}
	}

	return m
}

func toProviderDefFileConfig(def ProviderDef) ProviderDefFileConfig {
	p := ProviderDefFileConfig{
		BaseURL:   def.BaseURL,
		APIKey:    def.APIKey,
		Version:   def.Version,
		UserAgent: def.UserAgent,
		Protocol:  def.Protocol,
		WebSearch: WebSearchFileConfig{
			Support:         string(def.WebSearchSupport),
			MaxUses:         def.WebSearchMaxUses,
			TavilyAPIKey:    def.TavilyAPIKey,
			FirecrawlAPIKey: def.FirecrawlAPIKey,
			SearchMaxRounds: def.SearchMaxRounds,
			Extra:           cloneAnyMap(def.WebSearchExtra),
		},
	}

	if len(def.Extensions) > 0 {
		p.Extensions = make(map[string]ExtensionFileConfig, len(def.Extensions))
		for name, s := range def.Extensions {
			p.Extensions[name] = toExtensionFileConfig(s)
		}
	}

	// Convert Offers.
	if len(def.Offers) > 0 {
		p.Offers = make([]OfferFileConfig, 0, len(def.Offers))
		for _, offer := range def.Offers {
			p.Offers = append(p.Offers, toOfferFileConfig(offer))
		}
	}

	return p
}

func toOfferFileConfig(offer OfferEntry) OfferFileConfig {
	ofc := OfferFileConfig{
		Model:        offer.Model,
		UpstreamName: offer.UpstreamName,
		Priority:     offer.Priority,
		Pricing: ModelPricingFileConfig{
			InputPrice:      offer.Pricing.InputPrice,
			OutputPrice:     offer.Pricing.OutputPrice,
			CacheWritePrice: offer.Pricing.CacheWritePrice,
			CacheReadPrice:  offer.Pricing.CacheReadPrice,
		},
	}

	if offer.Overrides != nil {
		merged := toModelDefFileConfig(*offer.Overrides)
		ofc.Overrides = &merged
	}

	return ofc
}

func toRouteFileConfig(entry RouteEntry) RouteFileConfig {
	r := RouteFileConfig{
		Model:         entry.Model,
		Provider:      entry.Provider,
		DisplayName:   entry.DisplayName,
		Description:   entry.Description,
		ContextWindow: entry.ContextWindow,
	}

	if hasRuntimeWebSearchConfig(entry.WebSearch) {
		r.WebSearch = toWebSearchFileConfig(entry.WebSearch)
	}

	if len(entry.Extensions) > 0 {
		r.Extensions = make(map[string]ExtensionFileConfig, len(entry.Extensions))
		for name, s := range entry.Extensions {
			r.Extensions[name] = toExtensionFileConfig(s)
		}
	}

	return r
}

func toWebSearchFileConfig(ws WebSearchConfig) WebSearchFileConfig {
	return WebSearchFileConfig{
		Support:         string(ws.Support),
		MaxUses:         ws.MaxUses,
		TavilyAPIKey:    ws.TavilyAPIKey,
		FirecrawlAPIKey: ws.FirecrawlAPIKey,
		SearchMaxRounds: ws.SearchMaxRounds,
		Extra:           cloneAnyMap(ws.Extra),
	}
}

func hasRuntimeWebSearchConfig(ws WebSearchConfig) bool {
	return ws.Support != "" ||
		ws.MaxUses != 0 ||
		ws.TavilyAPIKey != "" ||
		ws.FirecrawlAPIKey != "" ||
		ws.SearchMaxRounds != 0 ||
		len(ws.Extra) > 0
}

func toExtensionFileConfig(s ExtensionSettings) ExtensionFileConfig {
	return ExtensionFileConfig{
		Enabled: s.Enabled,
		Config:  cloneAnyMap(s.RawConfig),
		Extra:   cloneAnyMap(s.Extra),
	}
}

func toCacheFileConfig(c CacheConfig) CacheFileConfig {
	return CacheFileConfig{
		Mode:                     c.Mode,
		TTL:                      c.TTL,
		PromptCaching:            boolPtr(c.PromptCaching),
		AutomaticPromptCache:     boolPtr(c.AutomaticPromptCache),
		ExplicitCacheBreakpoints: boolPtr(c.ExplicitCacheBreakpoints),
		AllowRetentionDowngrade:  boolPtr(c.AllowRetentionDowngrade),
		MaxBreakpoints:           c.MaxBreakpoints,
		MinCacheTokens:           c.MinCacheTokens,
		ExpectedReuse:            c.ExpectedReuse,
		MinimumValueScore:        c.MinimumValueScore,
		MinBreakpointTokens:      c.MinBreakpointTokens,
	}
}

func boolPtr(v bool) *bool {
	return &v
}
