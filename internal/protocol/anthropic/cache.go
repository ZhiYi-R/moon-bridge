package anthropic

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"moonbridge/internal/config"
	"moonbridge/internal/format"
	"moonbridge/internal/protocol/cache"
	"moonbridge/internal/protocol/openai"
)

// ============================================================================
// CacheManager implementation — concrete type for AnthropicProviderAdapter
// ============================================================================

// adapterCacheManager wraps cache.Planner and cache.MemoryRegistry to provide
// cache planning and registry updates for Anthropic requests.
type adapterCacheManager struct {
	cacheCfg cache.PlanCacheConfig
	registry *cache.MemoryRegistry
}

// NewCacheManager creates a new CacheManager from config and registry.
func NewCacheManager(cacheCfg *config.CacheConfig, registry *cache.MemoryRegistry) CacheManager {
	if cacheCfg == nil || registry == nil {
		return nil
	}
	return &adapterCacheManager{
		cacheCfg: cache.PlanCacheConfig{
			Mode:                     cacheCfg.Mode,
			TTL:                      cacheCfg.TTL,
			PromptCaching:            cacheCfg.PromptCaching,
			AutomaticPromptCache:     cacheCfg.AutomaticPromptCache,
			ExplicitCacheBreakpoints: cacheCfg.ExplicitCacheBreakpoints,
			AllowRetentionDowngrade:  cacheCfg.AllowRetentionDowngrade,
			MaxBreakpoints:           cacheCfg.MaxBreakpoints,
			MinCacheTokens:           cacheCfg.MinCacheTokens,
			ExpectedReuse:            cacheCfg.ExpectedReuse,
			MinimumValueScore:        cacheCfg.MinimumValueScore,
			MinBreakpointTokens:      cacheCfg.MinBreakpointTokens,
		},
		registry: registry,
	}
}

// PlanAndInject implements CacheManager.
func (m *adapterCacheManager) PlanAndInject(ctx context.Context, req *MessageRequest, coreReq *format.CoreRequest) (key, ttl string) {
	promptCacheKey := ""
	promptCacheRetention := ""
	if ext, ok := coreReq.Extensions["cache"]; ok {
		if cacheMeta, ok := ext.(map[string]any); ok {
			if k, ok := cacheMeta["prompt_cache_key"].(string); ok {
				promptCacheKey = k
			}
			if r, ok := cacheMeta["prompt_cache_retention"].(string); ok {
				promptCacheRetention = r
			}
		}
	}

	openaiReq := openai.ResponsesRequest{
		PromptCacheKey:       promptCacheKey,
		PromptCacheRetention: promptCacheRetention,
	}

	plan, err := PlanCache(m.cacheCfg, m.registry, openaiReq, *req)
	if err != nil {
		slog.Warn("adapter cache planning failed", "error", err)
		return "", ""
	}

	InjectCacheControl(req, plan)
	return plan.PrefixKey, plan.TTL
}

// UpdateRegistry implements CacheManager.
func (m *adapterCacheManager) UpdateRegistry(ctx context.Context, key, ttl string, usage Usage) {
	if key == "" {
		return
	}
	signals := cache.UsageSignals{
		InputTokens:              usage.InputTokens,
		CacheCreationInputTokens: usage.CacheCreationInputTokens,
		CacheReadInputTokens:     usage.CacheReadInputTokens,
	}
	cache.UpdateRegistryFromUsage(m.registry, cache.CacheCreationPlan{
		PrefixKey: key,
		TTL:       ttl,
	}, signals, usage.InputTokens)
}

// ============================================================================
// Anthropic-specific cache planning helpers
// ============================================================================

// PlanCache creates a cache creation plan using cache.Planner.
func PlanCache(cfg cache.PlanCacheConfig, registry *cache.MemoryRegistry, request openai.ResponsesRequest, converted MessageRequest) (cache.CacheCreationPlan, error) {
	if request.PromptCacheRetention == "24h" && !cfg.AllowRetentionDowngrade {
		return cache.CacheCreationPlan{}, &cachePlanError{
			Status:  400,
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
	planner := cache.NewPlannerWithRegistry(cfg.PlannerConfig(ttl), registry)
	return planner.Plan(cache.PlanInput{
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

// InjectCacheControl applies a cache creation plan to an MessageRequest.
func InjectCacheControl(request *MessageRequest, plan cache.CacheCreationPlan) {
	if plan.Mode == "off" {
		return
	}
	cacheControl := &CacheControl{Type: "ephemeral"}
	if plan.TTL == "1h" {
		cacheControl.TTL = "1h"
	}
	if plan.Mode == "automatic" || plan.Mode == "hybrid" {
		request.CacheControl = cacheControl
	}
	for _, bp := range plan.Breakpoints {
		switch bp.Scope {
		case "tools":
			if len(request.Tools) > 0 {
				index := bp.ScopeIndex
				if index < 0 || index >= len(request.Tools) {
					index = len(request.Tools) - 1
				}
				request.Tools[index].CacheControl = cacheControl
			}
		case "system":
			if len(request.System) > 0 {
				index := bp.ScopeIndex
				if index < 0 || index >= len(request.System) {
					index = len(request.System) - 1
				}
				request.System[index].CacheControl = cacheControl
			}
		case "messages":
			if len(request.Messages) > 0 {
				messageIndex := bp.ScopeIndex
				if messageIndex < 0 || messageIndex >= len(request.Messages) {
					messageIndex = len(request.Messages) - 1
				}
				contentIndex := len(request.Messages[messageIndex].Content) - 1
				if bp.ContentIndex >= 0 && bp.ContentIndex < len(request.Messages[messageIndex].Content) {
					contentIndex = bp.ContentIndex
				}
				if contentIndex >= 0 {
					request.Messages[messageIndex].Content[contentIndex].CacheControl = cacheControl
				}
			}
		}
	}
}

// CacheMessageBreakpointCandidates computes breakpoint candidates from messages.
func CacheMessageBreakpointCandidates(messages []Message) []cache.MessageBreakpointCandidate {
	candidates := make([]cache.MessageBreakpointCandidate, 0, len(messages))
	for messageIndex, message := range messages {
		contentIndex := lastCacheableContentIndex(message.Content)
		if contentIndex < 0 {
			continue
		}
		blockPath := fmt.Sprintf("messages[%d].content[%d]", messageIndex, contentIndex)
		if contentIndex == len(message.Content)-1 {
			blockPath = fmt.Sprintf("messages[%d].content[last]", messageIndex)
		}
		candidates = append(candidates, cache.MessageBreakpointCandidate{
			MessageIndex: messageIndex,
			ContentIndex: contentIndex,
			BlockPath:    blockPath,
			Role:         message.Role,
		})
	}
	return candidates
}

// lastCacheableContentIndex finds the last non-empty text block index in content.
func lastCacheableContentIndex(content []ContentBlock) int {
	for index := len(content) - 1; index >= 0; index-- {
		block := content[index]
		if block.Type == "text" && strings.TrimSpace(block.Text) == "" {
			continue
		}
		return index
	}
	return -1
}

// estimateTokens estimates the token count for an Anthropic MessageRequest.
func estimateTokens(request any) int {
	return cache.EstimateTokens(request)
}

// estimatePartTokens estimates token count for any JSON-serializable slice.
func estimatePartTokens(part any) int {
	return cache.EstimatePartTokens(part)
}

// canonicalHash computes a SHA256 hash for cache key generation.
func canonicalHash(value any) (string, error) {
	return cache.CanonicalHash(value)
}

// cachePlanError is returned when cache planning fails.
type cachePlanError struct {
	Status  int
	Message string
	Param   string
	Code    string
}

func (e *cachePlanError) Error() string { return e.Message }
