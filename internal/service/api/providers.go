package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"moonbridge/internal/protocol/anthropic"
	"moonbridge/internal/service/store"
)

// ---- Providers ----

// GET /providers
func (r *Router) handleListProviders(w http.ResponseWriter, req *http.Request) {
	p := parsePagination(req)

	cfg := r.runtime.Current()
	providerKeys := make([]string, 0, len(cfg.Config.ProviderDefs))
	for key := range cfg.Config.ProviderDefs {
		providerKeys = append(providerKeys, key)
	}
	sortStrings(providerKeys)

	total := len(providerKeys)

	sliceEnd := p.Offset + p.Limit
	if p.Offset > len(providerKeys) {
		p.Offset = len(providerKeys)
	}
	if sliceEnd > len(providerKeys) {
		sliceEnd = len(providerKeys)
	}
	page := providerKeys[p.Offset:sliceEnd]

	type providerItem struct {
		Key          string `json:"key"`
		Protocol     string `json:"protocol"`
		OfferCount   int    `json:"offer_count"`
		BaseURL      string `json:"base_url"`
		HealthStatus string `json:"health_status"`
	}

	items := make([]providerItem, 0, len(page))
	for _, key := range page {
		def := cfg.Config.ProviderDefs[key]
		items = append(items, providerItem{
			Key:          key,
			Protocol:     def.Protocol,
			OfferCount:   len(def.Offers),
			BaseURL:      def.BaseURL,
			HealthStatus: "unknown",
		})
	}

	respondJSON(w, http.StatusOK, paginatedResponse{
		Data:   items,
		Total:  total,
		Limit:  p.Limit,
		Offset: p.Offset,
	})
}

// GET /providers/{key}
func (r *Router) handleGetProvider(w http.ResponseWriter, req *http.Request) {
	key := req.PathValue("key")
	if key == "" {
		respondError(w, http.StatusBadRequest, "invalid_key", "无效的 provider key")
		return
	}

	cfg := r.runtime.Current()
	def, ok := cfg.Config.ProviderDefs[key]
	if !ok {
		respondError(w, http.StatusNotFound, "not_found", fmt.Sprintf("provider %q 不存在", key))
		return
	}

	type offerItem struct {
		Model        string  `json:"model"`
		UpstreamName string  `json:"upstream_name,omitempty"`
		Priority     int     `json:"priority"`
		InputPrice   float64 `json:"input_price"`
		OutputPrice  float64 `json:"output_price"`
		CacheWrite   float64 `json:"cache_write"`
		CacheRead    float64 `json:"cache_read"`
	}

	offers := make([]offerItem, 0, len(def.Offers))
	for _, offer := range def.Offers {
		offers = append(offers, offerItem{
			Model:        offer.Model,
			UpstreamName: offer.UpstreamName,
			Priority:     offer.Priority,
			InputPrice:   offer.Pricing.InputPrice,
			OutputPrice:  offer.Pricing.OutputPrice,
			CacheWrite:   offer.Pricing.CacheWritePrice,
			CacheRead:    offer.Pricing.CacheReadPrice,
		})
	}

	resp := map[string]any{
		"key":                 key,
		"base_url":            def.BaseURL,
		"api_key":             maskAPIKey(def.APIKey),
		"version":             def.Version,
		"protocol":            def.Protocol,
		"user_agent":          def.UserAgent,
		"offers":              offers,
		"offer_count":         len(offers),
		"web_search":          string(def.WebSearchSupport),
		"web_search_max_uses": def.WebSearchMaxUses,
	}

	respondJSON(w, http.StatusOK, resp)
}

// PUT /providers/{key}
func (r *Router) handlePutProvider(w http.ResponseWriter, req *http.Request) {
	key := req.PathValue("key")
	if key == "" {
		respondError(w, http.StatusBadRequest, "invalid_key", "无效的 provider key")
		return
	}

	var body struct {
		BaseURL   string `json:"base_url"`
		APIKey    string `json:"api_key"`
		Version   string `json:"version"`
		Protocol  string `json:"protocol"`
		UserAgent string `json:"user_agent"`
	}

	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		respondError(w, http.StatusBadRequest, "invalid_json", "无效的 JSON 请求体")
		return
	}
	if body.BaseURL == "" {
		respondError(w, http.StatusBadRequest, "validation_error", "base_url 不能为空")
		return
	}
	if body.APIKey == "" {
		respondError(w, http.StatusBadRequest, "validation_error", "api_key 不能为空")
		return
	}
	if body.Protocol == "" {
		body.Protocol = "anthropic"
	}

	afterJSON, _ := json.Marshal(map[string]any{
		"base_url":   body.BaseURL,
		"api_key":    body.APIKey,
		"version":    body.Version,
		"protocol":   body.Protocol,
		"user_agent": body.UserAgent,
	})

	chID, err := r.store.StageChange(store.ChangeRow{
		Action:    "create",
		Resource:  "provider",
		TargetKey: key,
		After:     string(afterJSON),
	})
	if err != nil {
		respondError(w, http.StatusInternalServerError, "stage_error", fmt.Sprintf("暂存变更失败: %v", err))
		return
	}

	respondJSON(w, http.StatusAccepted, map[string]any{
		"change_id": chID,
		"status":    "pending",
		"message":   "变更已暂存，请调用 POST /changes/apply 使其生效",
	})
}

// PATCH /providers/{key}
func (r *Router) handlePatchProvider(w http.ResponseWriter, req *http.Request) {
	key := req.PathValue("key")
	if key == "" {
		respondError(w, http.StatusBadRequest, "invalid_key", "无效的 provider key")
		return
	}

	var body struct {
		BaseURL   string `json:"base_url"`
		APIKey    string `json:"api_key"`
		Version   string `json:"version"`
		Protocol  string `json:"protocol"`
		UserAgent string `json:"user_agent"`
	}

	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		respondError(w, http.StatusBadRequest, "invalid_json", "无效的 JSON 请求体")
		return
	}

	cfg := r.runtime.Current()
	def, exists := cfg.Config.ProviderDefs[key]

	apiKey := body.APIKey
	if apiKey == "******" && exists {
		apiKey = def.APIKey
	}

	baseURL := body.BaseURL
	if baseURL == "" && exists {
		baseURL = def.BaseURL
	}

	version := body.Version
	if version == "" && exists {
		version = def.Version
	}

	protocol := body.Protocol
	if protocol == "" && exists {
		protocol = def.Protocol
	}

	userAgent := body.UserAgent
	if userAgent == "" && exists {
		userAgent = def.UserAgent
	}

	action := "update"
	if !exists {
		action = "create"
	}

	afterJSON, _ := json.Marshal(map[string]any{
		"base_url":   baseURL,
		"api_key":    apiKey,
		"version":    version,
		"protocol":   protocol,
		"user_agent": userAgent,
	})

	chID, err := r.store.StageChange(store.ChangeRow{
		Action:    action,
		Resource:  "provider",
		TargetKey: key,
		After:     string(afterJSON),
	})
	if err != nil {
		respondError(w, http.StatusInternalServerError, "stage_error", fmt.Sprintf("暂存变更失败: %v", err))
		return
	}

	respondJSON(w, http.StatusAccepted, map[string]any{
		"change_id": chID,
		"status":    "pending",
		"message":   "变更已暂存，请调用 POST /changes/apply 使其生效",
	})
}

// DELETE /providers/{key}
func (r *Router) handleDeleteProvider(w http.ResponseWriter, req *http.Request) {
	key := req.PathValue("key")
	if key == "" {
		respondError(w, http.StatusBadRequest, "invalid_key", "无效的 provider key")
		return
	}

	cfg := r.runtime.Current()
	if _, ok := cfg.Config.ProviderDefs[key]; !ok {
		respondError(w, http.StatusNotFound, "not_found", fmt.Sprintf("provider %q 不存在", key))
		return
	}

	chID, err := r.store.StageChange(store.ChangeRow{
		Action:    "delete",
		Resource:  "provider",
		TargetKey: key,
	})
	if err != nil {
		respondError(w, http.StatusInternalServerError, "stage_error", fmt.Sprintf("暂存删除失败: %v", err))
		return
	}

	respondJSON(w, http.StatusAccepted, map[string]any{
		"change_id": chID,
		"status":    "pending",
		"message":   "删除已暂存，请调用 POST /changes/apply 使其生效",
	})
}

// POST /providers/{key}/test
func (r *Router) handleTestProvider(w http.ResponseWriter, req *http.Request) {
	key := req.PathValue("key")
	if key == "" {
		respondError(w, http.StatusBadRequest, "invalid_key", "无效的 provider key")
		return
	}

	cfg := r.runtime.Current()
	def, ok := cfg.Config.ProviderDefs[key]
	if !ok {
		respondError(w, http.StatusNotFound, "not_found", fmt.Sprintf("provider %q 不存在", key))
		return
	}

	probe := anthropic.MessageRequest{
		Model:     "claude-3-haiku-20240307",
		MaxTokens: 1,
		Messages: []anthropic.Message{
			{Role: "user", Content: []anthropic.ContentBlock{{Type: "text", Text: "hi"}}},
		},
	}

	client := anthropic.NewClient(anthropic.ClientConfig{
		BaseURL: def.BaseURL,
		APIKey:  def.APIKey,
		Version: def.Version,
	})

	ctx, cancel := context.WithTimeout(req.Context(), 5*time.Second)
	defer cancel()

	start := time.Now()
	_, err := client.CreateMessage(ctx, probe)
	duration := time.Since(start)

	result := map[string]any{
		"provider":  key,
		"base_url":  def.BaseURL,
		"duration":  duration.String(),
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	}

	if err != nil {
		result["success"] = false
		result["error"] = err.Error()
		respondJSON(w, http.StatusOK, result)
		return
	}

	result["success"] = true
	respondJSON(w, http.StatusOK, result)
}

// POST /providers/{key}/web-search/reprobe
func (r *Router) handleReprobeProviderWebSearch(w http.ResponseWriter, req *http.Request) {
	key := req.PathValue("key")
	if key == "" {
		respondError(w, http.StatusBadRequest, "invalid_key", "无效的 provider key")
		return
	}

	cfg := r.runtime.Current()
	if _, ok := cfg.Config.ProviderDefs[key]; !ok {
		respondError(w, http.StatusNotFound, "not_found", fmt.Sprintf("provider %q 不存在", key))
		return
	}

	ctx, cancel := context.WithTimeout(req.Context(), 35*time.Second)
	defer cancel()

	resolved, err := r.server.ReprobeWebSearch(ctx, key)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "reprobe_failed", err.Error())
		return
	}

	respondJSON(w, http.StatusOK, map[string]any{
		"provider":  key,
		"resolved":  resolved,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
}
