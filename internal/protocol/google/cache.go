package google

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/ZhiYi-R/moon-bridge/internal/protocol/cache"
)

// cacheKey computes a stable cache key for Gemini requests.
// Uses system_instruction + tools + first content block.
func (a *GeminiProviderAdapter) cacheKey(geminiReq *GenerateContentRequest) string {
	h := sha256.New()
	if geminiReq.SystemInstruction != nil {
		data, _ := json.Marshal(geminiReq.SystemInstruction)
		h.Write(data)
	}
	if len(geminiReq.Tools) > 0 {
		data, _ := json.Marshal(geminiReq.Tools)
		h.Write(data)
	}
	if len(geminiReq.Contents) > 0 {
		data, _ := json.Marshal(geminiReq.Contents[0])
		h.Write(data)
	}
	return "gemini:" + hex.EncodeToString(h.Sum(nil))
}

// prepareCache checks for existing cached content and modifies the request
// to use it. If no cache exists and the request is cacheable, creates one.
// Must be called after the request is fully built, before returning it.
func (a *GeminiProviderAdapter) prepareCache(ctx context.Context, geminiReq *GenerateContentRequest) {
	if a.cacheCfg == nil || a.registry == nil {
		return
	}
	if a.cacheCfg.Mode == "off" || !a.cacheCfg.PromptCaching {
		return
	}

	key := a.cacheKey(geminiReq)
	log := slog.Default().With("gemini_cache", key)
	// Store key for response processing.
	a.currentCacheKey = key

	// Build the CachedContent resource name from the key hash (G-05).
	// key format is "gemini:hexhash", extract hex part for resource name.
	hexPart := key
	if len(key) > 7 {
		hexPart = key[7:]
	}
	cachedContentName := "cachedContents/" + hexPart

	// Check registry for existing cached content.
	if entry, ok := a.registry.Get(key); ok {
		if entry.State == "warm" && (entry.ExpiresAt.IsZero() || entry.ExpiresAt.After(time.Now())) {
			log.Debug("gemini cache hit")
			geminiReq.CachedContent = cachedContentName
			// Per Gemini API constraint: when using cached_content, must clear
			// system_instruction, tools, and tool_config (they are in the cache).
			geminiReq.SystemInstruction = nil
			geminiReq.Tools = nil
			geminiReq.ToolConfig = nil
			return
		}
	}

	// Cold start — check if content is worth caching.
	estTokens := cache.EstimateTokens(geminiReq)
	if a.cacheCfg.MinCacheTokens > 0 && estTokens < a.cacheCfg.MinCacheTokens {
		log.Debug("gemini cache skip: below min tokens", "estimated", estTokens, "min", a.cacheCfg.MinCacheTokens)
		return
	}

	if a.client == nil {
		return
	}

	// Create CachedContent resource.
	ttl := a.cacheCfg.TTL
	if ttl == "" {
		ttl = "3600s"
	}

	modelName := a.currentModel
	if modelName == "" {
		modelName = "gemini-2.5-flash" // fallback
	}

	cc := &CachedContent{
		Model:             "models/" + modelName,
		Contents:          geminiReq.Contents,
		SystemInstruction: geminiReq.SystemInstruction,
		Tools:             geminiReq.Tools,
		TTL:               ttl,
	}

	result, err := a.client.CreateCachedContent(ctx, cc)
	if err != nil {
		log.Warn("gemini cache create failed", "error", err)
		return
	}

	// Store in registry.
	a.registry.Set(cache.RegistryEntry{
		LocalKey:  key,
		State:     "warm",
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(1 * time.Hour),
	})

	log.Debug("gemini cache created", "name", result.Name)
	geminiReq.CachedContent = result.Name
	// Clear protocol-constrained fields.
	geminiReq.SystemInstruction = nil
	geminiReq.Tools = nil
	geminiReq.ToolConfig = nil
}

// prepareCacheResponse updates the registry from Gemini response usage metadata.
func (a *GeminiProviderAdapter) prepareCacheResponse(resp *GenerateContentResponse, key string) {
	if a.registry == nil || key == "" {
		return
	}
	if resp == nil || resp.UsageMetadata == nil {
		return
	}
	if resp.UsageMetadata.CachedContentTokenCount > 0 {
		// Cache was used — extend TTL.
		a.registry.Set(cache.RegistryEntry{
			LocalKey:  key,
			State:     "warm",
			CreatedAt: time.Now(),
			ExpiresAt: time.Now().Add(1 * time.Hour),
		})
	}
}
