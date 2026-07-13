package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/ZhiYi-R/moon-bridge/internal/config"
	"github.com/ZhiYi-R/moon-bridge/internal/service/store"
)

// ---- Offers ----

// POST /providers/{key}/offers
func (r *Router) handleCreateOffer(w http.ResponseWriter, req *http.Request) {
	providerKey := req.PathValue("key")
	if providerKey == "" {
		respondError(w, http.StatusBadRequest, "invalid_key", "无效的 provider key")
		return
	}

	var body struct {
		Model        string  `json:"model"`
		UpstreamName string  `json:"upstream_name"`
		Priority     int     `json:"priority"`
		InputPrice   float64 `json:"input_price"`
		OutputPrice  float64 `json:"output_price"`
		CacheWrite   float64 `json:"cache_write"`
		CacheRead    float64 `json:"cache_read"`
	}

	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		respondError(w, http.StatusBadRequest, "invalid_json", "无效的 JSON 请求体")
		return
	}
	if body.Model == "" {
		respondError(w, http.StatusBadRequest, "validation_error", "model 不能为空")
		return
	}

	afterJSON, _ := json.Marshal(map[string]any{
		"provider_key":  providerKey,
		"model_slug":    body.Model,
		"upstream_name": body.UpstreamName,
		"priority":      body.Priority,
		"input_price":   body.InputPrice,
		"output_price":  body.OutputPrice,
		"cache_write":   body.CacheWrite,
		"cache_read":    body.CacheRead,
	})

	chID, err := r.store.StageChange(store.ChangeRow{
		Action:    "create",
		Resource:  "offer",
		TargetKey: providerKey + "/" + body.Model,
		After:     string(afterJSON),
	})
	if err != nil {
		respondError(w, http.StatusInternalServerError, "stage_error", fmt.Sprintf("暂存变更失败: %v", err))
		return
	}

	respondJSON(w, http.StatusAccepted, map[string]any{
		"change_id": chID,
		"status":    "pending",
	})
}

// PATCH /providers/{key}/offers/{model}
func (r *Router) handleUpdateOffer(w http.ResponseWriter, req *http.Request) {
	providerKey := req.PathValue("key")
	modelSlug := req.PathValue("model")
	if providerKey == "" || modelSlug == "" {
		respondError(w, http.StatusBadRequest, "invalid_path", "路径格式无效")
		return
	}

	// Use pointer types for numeric and optional string fields so that
	// zero-value vs. unset can be distinguished (P1-7).
	var body struct {
		UpstreamName *string  `json:"upstream_name"`
		Priority     *int     `json:"priority"`
		InputPrice   *float64 `json:"input_price"`
		OutputPrice  *float64 `json:"output_price"`
		CacheWrite   *float64 `json:"cache_write"`
		CacheRead    *float64 `json:"cache_read"`
	}

	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		respondError(w, http.StatusBadRequest, "invalid_json", "无效的 JSON 请求体")
		return
	}

	// Read the current offer to fill in omitted fields.
	cfg := r.runtime.Current()
	currentOffer := findOffer(cfg.Config, providerKey, modelSlug)

	after := map[string]any{
		"provider_key": providerKey,
		"model_slug":   modelSlug,
	}

	if body.UpstreamName != nil {
		after["upstream_name"] = *body.UpstreamName
	} else if currentOffer != nil {
		after["upstream_name"] = currentOffer.UpstreamName
	} else {
		after["upstream_name"] = ""
	}

	if body.Priority != nil {
		after["priority"] = *body.Priority
	} else if currentOffer != nil {
		after["priority"] = currentOffer.Priority
	} else {
		after["priority"] = 0
	}

	if body.InputPrice != nil {
		after["input_price"] = *body.InputPrice
	} else if currentOffer != nil {
		after["input_price"] = currentOffer.Pricing.InputPrice
	} else {
		after["input_price"] = 0.0
	}

	if body.OutputPrice != nil {
		after["output_price"] = *body.OutputPrice
	} else if currentOffer != nil {
		after["output_price"] = currentOffer.Pricing.OutputPrice
	} else {
		after["output_price"] = 0.0
	}

	if body.CacheWrite != nil {
		after["cache_write"] = *body.CacheWrite
	} else if currentOffer != nil {
		after["cache_write"] = currentOffer.Pricing.CacheWritePrice
	} else {
		after["cache_write"] = 0.0
	}

	if body.CacheRead != nil {
		after["cache_read"] = *body.CacheRead
	} else if currentOffer != nil {
		after["cache_read"] = currentOffer.Pricing.CacheReadPrice
	} else {
		after["cache_read"] = 0.0
	}

	afterJSON, _ := json.Marshal(after)

	action := "update"
	if currentOffer == nil {
		action = "create"
	}

	chID, err := r.store.StageChange(store.ChangeRow{
		Action:    action,
		Resource:  "offer",
		TargetKey: providerKey + "/" + modelSlug,
		After:     string(afterJSON),
	})
	if err != nil {
		respondError(w, http.StatusInternalServerError, "stage_error", fmt.Sprintf("暂存变更失败: %v", err))
		return
	}

	respondJSON(w, http.StatusAccepted, map[string]any{
		"change_id": chID,
		"status":    "pending",
	})
}

// DELETE /providers/{key}/offers/{model}
func (r *Router) handleDeleteOffer(w http.ResponseWriter, req *http.Request) {
	providerKey := req.PathValue("key")
	modelSlug := req.PathValue("model")
	if providerKey == "" || modelSlug == "" {
		respondError(w, http.StatusBadRequest, "invalid_path", "路径格式无效")
		return
	}

	beforeJSON, _ := json.Marshal(map[string]any{
		"provider_key": providerKey,
		"model_slug":   modelSlug,
	})

	chID, err := r.store.StageChange(store.ChangeRow{
		Action:    "delete",
		Resource:  "offer",
		TargetKey: providerKey + "/" + modelSlug,
		Before:    string(beforeJSON),
	})
	if err != nil {
		respondError(w, http.StatusInternalServerError, "stage_error", fmt.Sprintf("暂存删除失败: %v", err))
		return
	}

	respondJSON(w, http.StatusAccepted, map[string]any{
		"change_id": chID,
		"status":    "pending",
	})
}

// findOffer looks up an OfferEntry in the current config for the given provider+model.
func findOffer(cfg config.Config, providerKey, modelSlug string) *config.OfferEntry {
	def, ok := cfg.ProviderDefs[providerKey]
	if !ok {
		return nil
	}
	for i := range def.Offers {
		if def.Offers[i].Model == modelSlug {
			return &def.Offers[i]
		}
	}
	return nil
}
