package cache

import (
	"net/http"

	"moonbridge/internal/foundation/openai"
	"moonbridge/internal/protocol/anthropic"
)

// PlanCacheConfig holds all configuration needed for PlanCache.
// Mirrors the relevant subset of config.CacheConfig without importing config.
type PlanCacheConfig struct {
	Mode                     string
	TTL                      string
	PromptCaching            bool
	AutomaticPromptCache     bool
	ExplicitCacheBreakpoints bool
	AllowRetentionDowngrade  bool
	MaxBreakpoints           int
	MinCacheTokens           int
	ExpectedReuse            int
	MinimumValueScore        int
	MinBreakpointTokens      int
}

// toPlannerConfig converts PlanCacheConfig to PlannerConfig.
func (cfg PlanCacheConfig) toPlannerConfig(ttl string) PlannerConfig {
	if ttl == "" {
		ttl = cfg.TTL
	}
	return PlannerConfig{
		Mode:                     cfg.Mode,
		TTL:                      ttl,
		PromptCaching:            cfg.PromptCaching,
		AutomaticPromptCache:     cfg.AutomaticPromptCache,
		ExplicitCacheBreakpoints: cfg.ExplicitCacheBreakpoints,
		MaxBreakpoints:           cfg.MaxBreakpoints,
		MinCacheTokens:           cfg.MinCacheTokens,
		ExpectedReuse:            cfg.ExpectedReuse,
		MinimumValueScore:        cfg.MinimumValueScore,
		MinBreakpointTokens:      cfg.MinBreakpointTokens,
	}
}

// InjectCacheControl applies a cache creation plan to an Anthropic MessageRequest,
// setting cache_control on the appropriate scopes (tools, system, messages).
func InjectCacheControl(request *anthropic.MessageRequest, plan CacheCreationPlan) {
	if plan.Mode == "off" {
		return
	}
	cacheControl := &anthropic.CacheControl{Type: "ephemeral"}
	if plan.TTL == "1h" {
		cacheControl.TTL = "1h"
	}
	if plan.Mode == "automatic" || plan.Mode == "hybrid" {
		request.CacheControl = cacheControl
	}
	for _, breakpointValue := range plan.Breakpoints {
		switch breakpointValue.Scope {
		case "tools":
			if len(request.Tools) > 0 {
				index := breakpointValue.ScopeIndex
				if index < 0 || index >= len(request.Tools) {
					index = len(request.Tools) - 1
				}
				request.Tools[index].CacheControl = cacheControl
			}
		case "system":
			if len(request.System) > 0 {
				index := breakpointValue.ScopeIndex
				if index < 0 || index >= len(request.System) {
					index = len(request.System) - 1
				}
				request.System[index].CacheControl = cacheControl
			}
		case "messages":
			if len(request.Messages) > 0 {
				messageIndex := breakpointValue.ScopeIndex
				if messageIndex < 0 || messageIndex >= len(request.Messages) {
					messageIndex = len(request.Messages) - 1
				}
				contentIndex := len(request.Messages[messageIndex].Content) - 1
				if breakpointValue.ContentIndex >= 0 && breakpointValue.ContentIndex < len(request.Messages[messageIndex].Content) {
					contentIndex = breakpointValue.ContentIndex
				}
				if contentIndex >= 0 {
					request.Messages[messageIndex].Content[contentIndex].CacheControl = cacheControl
				}
			}
		}
	}
}

// CachePlanError is returned when cache planning fails due to a client error.
type CachePlanError struct {
	Status  int
	Message string
	Param   string
	Code    string
}

func (err *CachePlanError) Error() string {
	return err.Message
}

// PlanCache creates a cache creation plan for a converted Anthropic request.
// cfg is the cache configuration, registry is used for warm state tracking.
func PlanCache(cfg PlanCacheConfig, registry *MemoryRegistry, request openai.ResponsesRequest, converted anthropic.MessageRequest) (CacheCreationPlan, error) {
	if request.PromptCacheRetention == "24h" && !cfg.AllowRetentionDowngrade {
		return CacheCreationPlan{}, &CachePlanError{
			Status:  http.StatusBadRequest,
			Message: "prompt_cache_retention 24h is not supported by Anthropic prompt caching",
			Param:   "prompt_cache_retention",
			Code:    "unsupported_parameter",
		}
	}

	ttl := cfg.TTL
	if request.PromptCacheRetention == "in_memory" {
		ttl = "5m"
	}
	if request.PromptCacheRetention == "24h" && cfg.AllowRetentionDowngrade {
		ttl = "1h"
	}

	toolsHash, _ := canonicalHash(converted.Tools)
	systemHash, _ := canonicalHash(converted.System)
	messagesHash, _ := canonicalHash(converted.Messages)
	planner := NewPlannerWithRegistry(cfg.toPlannerConfig(ttl), registry)
	return planner.Plan(PlanInput{
		ProviderID:            "anthropic",
		UpstreamAPIKeyID:      "configured-provider-key",
		Model:                 converted.Model,
		PromptCacheKey:        request.PromptCacheKey,
		ToolsHash:             toolsHash,
		SystemHash:            systemHash,
		MessagePrefixHash:     messagesHash,
		MessageBreakpoints:    CacheMessageBreakpointCandidates(converted.Messages),
		ToolCount:             len(converted.Tools),
		SystemBlockCount:      len(converted.System),
		MessageCount:          len(converted.Messages),
		EstimatedTokens:       estimateTokens(converted),
		EstimatedToolTokens:   estimatePartTokens(converted.Tools),
		EstimatedSystemTokens: estimatePartTokens(converted.System),
	})
}

// UpdateRegistryFromUsage updates the in-memory cache registry from upstream usage signals.
// This is a convenience wrapper around MemoryRegistry.UpdateFromUsage.
func UpdateRegistryFromUsage(registry *MemoryRegistry, plan CacheCreationPlan, signals UsageSignals, inputTokens int) {
	if registry == nil {
		return
	}
	key := plan.PrefixKey
	if key == "" {
		key = plan.LocalKey
	}
	if key == "" {
		return
	}
	registry.UpdateFromUsage(key, signals, inputTokens, ParseTTL(plan.TTL))
}
