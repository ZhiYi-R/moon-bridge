package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"moonbridge/internal/config"
	"moonbridge/internal/service/store"
)

// ---- Settings ----

// GET /defaults
func (r *Router) handleGetDefaults(w http.ResponseWriter, req *http.Request) {
	cfg := r.runtime.Current()

	resp := map[string]any{
		"model":         cfg.Config.Defaults.Model,
		"max_tokens":    cfg.Config.Defaults.MaxTokens,
		"system_prompt": cfg.Config.Defaults.SystemPrompt,
	}

	respondJSON(w, http.StatusOK, resp)
}

// PUT /defaults
func (r *Router) handlePutDefaults(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Model        string `json:"model"`
		MaxTokens    int    `json:"max_tokens"`
		SystemPrompt string `json:"system_prompt"`
	}

	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		respondError(w, http.StatusBadRequest, "invalid_json", "无效的 JSON 请求体")
		return
	}

	defaultsJSON, _ := json.Marshal(map[string]any{
		"model":         body.Model,
		"max_tokens":    body.MaxTokens,
		"system_prompt": body.SystemPrompt,
	})

	chID, err := r.store.StageChange(store.ChangeRow{
		Action:    "update",
		Resource:  "setting",
		TargetKey: "defaults",
		After:     string(defaultsJSON),
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

// GET /web-search
func (r *Router) handleGetWebSearch(w http.ResponseWriter, req *http.Request) {
	cfg := r.runtime.Current()

	resp := map[string]any{
		"support":           string(cfg.Config.WebSearchSupport),
		"max_uses":          cfg.Config.WebSearchMaxUses,
		"tavily_api_key":    maskAPIKey(cfg.Config.TavilyAPIKey),
		"firecrawl_api_key": maskAPIKey(cfg.Config.FirecrawlAPIKey),
		"search_max_rounds": cfg.Config.SearchMaxRounds,
	}

	respondJSON(w, http.StatusOK, resp)
}

// PUT /web-search
func (r *Router) handlePutWebSearch(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Support         string `json:"support"`
		MaxUses         int    `json:"max_uses"`
		TavilyAPIKey    string `json:"tavily_api_key"`
		FirecrawlAPIKey string `json:"firecrawl_api_key"`
		SearchMaxRounds int    `json:"search_max_rounds"`
	}

	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		respondError(w, http.StatusBadRequest, "invalid_json", "无效的 JSON 请求体")
		return
	}

	// If masked, keep existing values.
	cfg := r.runtime.Current()
	tavilyKey := body.TavilyAPIKey
	if tavilyKey == "******" {
		tavilyKey = cfg.Config.TavilyAPIKey
	}
	firecrawlKey := body.FirecrawlAPIKey
	if firecrawlKey == "******" {
		firecrawlKey = cfg.Config.FirecrawlAPIKey
	}

	wsJSON, _ := json.Marshal(map[string]any{
		"support":           body.Support,
		"max_uses":          body.MaxUses,
		"tavily_api_key":    tavilyKey,
		"firecrawl_api_key": firecrawlKey,
		"search_max_rounds": body.SearchMaxRounds,
	})

	chID, err := r.store.StageChange(store.ChangeRow{
		Action:    "update",
		Resource:  "setting",
		TargetKey: "web_search",
		After:     string(wsJSON),
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

// GET /extensions
func (r *Router) handleListExtensions(w http.ResponseWriter, req *http.Request) {
	if r.registry == nil {
		respondJSON(w, http.StatusOK, []any{})
		return
	}

	names := r.registry.Plugins()
	respondJSON(w, http.StatusOK, names)
}

// GET /extensions/{name}
func (r *Router) handleGetExtension(w http.ResponseWriter, req *http.Request) {
	name := req.PathValue("name")
	if name == "" {
		respondError(w, http.StatusBadRequest, "invalid_name", "无效的 extension name")
		return
	}

	if r.registry == nil {
		respondError(w, http.StatusNotFound, "not_found", fmt.Sprintf("extension %q 不存在", name))
		return
	}

	ext := r.registry.Plugin(name)
	if ext == nil {
		respondError(w, http.StatusNotFound, "not_found", fmt.Sprintf("extension %q 不存在", name))
		return
	}

	respondJSON(w, http.StatusOK, ext)
}

// PUT /extensions/{name}
func (r *Router) handlePutExtension(w http.ResponseWriter, req *http.Request) {
	name := req.PathValue("name")
	if name == "" {
		respondError(w, http.StatusBadRequest, "invalid_name", "无效的 extension name")
		return
	}

	var body map[string]any
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		respondError(w, http.StatusBadRequest, "invalid_json", "无效的 JSON 请求体")
		return
	}

	extJSON, _ := json.Marshal(body)

	chID, err := r.store.StageChange(store.ChangeRow{
		Action:    "update",
		Resource:  "setting",
		TargetKey: "extensions",
		After:     `{"` + name + `":` + string(extJSON) + `}`,
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

// ---- Config ----

// GET /config/effective
func (r *Router) handleGetConfigEffective(w http.ResponseWriter, req *http.Request) {
	cfg := r.runtime.Current()
	fc := cfg.Config.ToFileConfig()
	maskFileConfigSecrets(&fc)
	respondJSON(w, http.StatusOK, fc)
}

// GET /config/export
func (r *Router) handleGetConfigExport(w http.ResponseWriter, req *http.Request) {
	includeSecrets := req.URL.Query().Get("include_secrets") == "true"

	// Require explicit X-Confirm-Secrets header for plaintext secret export.
	if includeSecrets && req.Header.Get("X-Confirm-Secrets") != "true" {
		respondError(w, http.StatusBadRequest, "confirmation_required", "导出包含 secrets 需要设置 X-Confirm-Secrets: true header")
		return
	}

	yamlBytes, err := r.store.ExportYAML(includeSecrets)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "export_error", fmt.Sprintf("导出失败: %v", err))
		return
	}

	w.Header().Set("Content-Type", "application/x-yaml")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=moonbridge-config-%s.yml", time.Now().Format("20060102-150405")))
	w.Write(yamlBytes)
}

// POST /config/import
func (r *Router) handlePostConfigImport(w http.ResponseWriter, req *http.Request) {
	var body struct {
		YAML string `json:"yaml"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		respondError(w, http.StatusBadRequest, "invalid_json", "无效的 JSON 请求体")
		return
	}
	if body.YAML == "" {
		respondError(w, http.StatusBadRequest, "validation_error", "yaml 不能为空")
		return
	}

	// Parse the YAML via LoadFromYAML which validates and returns a full Config.
	cfg, err := config.LoadFromYAML([]byte(body.YAML))
	if err != nil {
		respondError(w, http.StatusBadRequest, "parse_error", fmt.Sprintf("YAML 解析失败: %v", err))
		return
	}

	// Create pending changes from the parsed config so that POST /changes/apply can apply them.
	var changes []map[string]any

	for key, def := range cfg.ProviderDefs {
		afterJSON, _ := json.Marshal(map[string]any{
			"base_url":   def.BaseURL,
			"api_key":    def.APIKey,
			"version":    def.Version,
			"protocol":   def.Protocol,
			"user_agent": def.UserAgent,
		})
		chID, err := r.store.StageChange(store.ChangeRow{
			Action:    "create",
			Resource:  "provider",
			TargetKey: key,
			After:     string(afterJSON),
		})
		if err != nil {
			respondError(w, http.StatusInternalServerError, "stage_error", fmt.Sprintf("暂存 provider %q 失败: %v", key, err))
			return
		}
		changes = append(changes, map[string]any{
			"change_id": chID,
			"resource":  "provider",
			"target":    key,
		})

		// Stage offers for each provider.
		for _, offer := range def.Offers {
			offerJSON, _ := json.Marshal(map[string]any{
				"provider_key":  key,
				"model_slug":    offer.Model,
				"upstream_name": offer.UpstreamName,
				"priority":      offer.Priority,
				"input_price":   offer.Pricing.InputPrice,
				"output_price":  offer.Pricing.OutputPrice,
				"cache_write":   offer.Pricing.CacheWritePrice,
				"cache_read":    offer.Pricing.CacheReadPrice,
			})
			chID, err := r.store.StageChange(store.ChangeRow{
				Action:    "create",
				Resource:  "offer",
				TargetKey: key + "/" + offer.Model,
				After:     string(offerJSON),
			})
			if err != nil {
				respondError(w, http.StatusInternalServerError, "stage_error", fmt.Sprintf("暂存 offer %q 失败: %v", key+"/"+offer.Model, err))
				return
			}
			changes = append(changes, map[string]any{
				"change_id": chID,
				"resource":  "offer",
				"target":    key + "/" + offer.Model,
			})
		}
	}

	for slug, def := range cfg.Models {
		meta := map[string]any{
			"display_name":      def.DisplayName,
			"description":       def.Description,
			"context_window":    def.ContextWindow,
			"max_output_tokens": def.MaxOutputTokens,
		}
		metaJSON, _ := json.Marshal(meta)
		afterJSON, _ := json.Marshal(map[string]any{
			"metadata": string(metaJSON),
		})
		chID, err := r.store.StageChange(store.ChangeRow{
			Action:    "create",
			Resource:  "model",
			TargetKey: slug,
			After:     string(afterJSON),
		})
		if err != nil {
			respondError(w, http.StatusInternalServerError, "stage_error", fmt.Sprintf("暂存 model %q 失败: %v", slug, err))
			return
		}
		changes = append(changes, map[string]any{
			"change_id": chID,
			"resource":  "model",
			"target":    slug,
		})
	}

	for alias, route := range cfg.Routes {
		afterJSON, _ := json.Marshal(map[string]any{
			"model_slug":     route.Model,
			"provider_key":   route.Provider,
			"display_name":   route.DisplayName,
			"context_window": route.ContextWindow,
		})
		chID, err := r.store.StageChange(store.ChangeRow{
			Action:    "create",
			Resource:  "route",
			TargetKey: alias,
			After:     string(afterJSON),
		})
		if err != nil {
			respondError(w, http.StatusInternalServerError, "stage_error", fmt.Sprintf("暂存 route %q 失败: %v", alias, err))
			return
		}
		changes = append(changes, map[string]any{
			"change_id": chID,
			"resource":  "route",
			"target":    alias,
		})
	}

	// Stage defaults if set.
	if cfg.Defaults.Model != "" || cfg.Defaults.MaxTokens > 0 || cfg.Defaults.SystemPrompt != "" {
		defaultsJSON, _ := json.Marshal(map[string]any{
			"model":         cfg.Defaults.Model,
			"max_tokens":    cfg.Defaults.MaxTokens,
			"system_prompt": cfg.Defaults.SystemPrompt,
		})
		chID, err := r.store.StageChange(store.ChangeRow{
			Action:    "update",
			Resource:  "setting",
			TargetKey: "defaults",
			After:     string(defaultsJSON),
		})
		if err != nil {
			respondError(w, http.StatusInternalServerError, "stage_error", fmt.Sprintf("暂存 defaults 失败: %v", err))
			return
		}
		changes = append(changes, map[string]any{
			"change_id": chID,
			"resource":  "setting",
			"target":    "defaults",
		})
	}

	// Stage web_search if set.
	if cfg.WebSearchSupport != "" || cfg.TavilyAPIKey != "" || cfg.FirecrawlAPIKey != "" {
		wsJSON, _ := json.Marshal(map[string]any{
			"support":           string(cfg.WebSearchSupport),
			"max_uses":          cfg.WebSearchMaxUses,
			"tavily_api_key":    cfg.TavilyAPIKey,
			"firecrawl_api_key": cfg.FirecrawlAPIKey,
			"search_max_rounds": cfg.SearchMaxRounds,
		})
		chID, err := r.store.StageChange(store.ChangeRow{
			Action:    "update",
			Resource:  "setting",
			TargetKey: "web_search",
			After:     string(wsJSON),
		})
		if err != nil {
			respondError(w, http.StatusInternalServerError, "stage_error", fmt.Sprintf("暂存 web_search 失败: %v", err))
			return
		}
		changes = append(changes, map[string]any{
			"change_id": chID,
			"resource":  "setting",
			"target":    "web_search",
		})
	}

	respondJSON(w, http.StatusOK, map[string]any{
		"changes": changes,
		"count":   len(changes),
		"message": fmt.Sprintf("配置已通过校验，已创建 %d 个待应用变更，请调用 POST /changes/apply 使其生效", len(changes)),
	})
}

// POST /config/validate
func (r *Router) handlePostConfigValidate(w http.ResponseWriter, req *http.Request) {
	var body struct {
		ConfigJSON string `json:"config"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		respondError(w, http.StatusBadRequest, "invalid_json", "无效的 JSON 请求体")
		return
	}
	if body.ConfigJSON == "" {
		respondError(w, http.StatusBadRequest, "validation_error", "config 不能为空")
		return
	}

	_, err := config.LoadFromYAML([]byte(body.ConfigJSON))
	if err != nil {
		respondJSON(w, http.StatusOK, map[string]any{
			"valid":  false,
			"errors": []string{err.Error()},
		})
		return
	}

	respondJSON(w, http.StatusOK, map[string]any{
		"valid": true,
	})
}

// ---- Changes ----

// GET /changes
func (r *Router) handleListChanges(w http.ResponseWriter, req *http.Request) {
	if r.store == nil {
		respondJSON(w, http.StatusOK, []store.ChangeRow{})
		return
	}
	changes, err := r.store.ListPendingChanges()
	if err != nil {
		respondError(w, http.StatusInternalServerError, "list_error", fmt.Sprintf("查询变更列表失败: %v", err))
		return
	}

	respondJSON(w, http.StatusOK, changes)
}

// POST /changes/apply
func (r *Router) handlePostChangesApply(w http.ResponseWriter, req *http.Request) {
	// Wrap runtime.Reload to convert *config.Config to config.Config.
	err := r.store.ApplyPendingChanges(req.Context(), func(cfg *config.Config) error {
		return r.runtime.Reload(*cfg)
	})
	if err != nil {
		respondError(w, http.StatusInternalServerError, "apply_error", fmt.Sprintf("应用变更失败: %v", err))
		return
	}

	respondJSON(w, http.StatusOK, map[string]any{
		"status":  "success",
		"message": "变更已应用生效",
	})
}

// POST /changes/discard
func (r *Router) handlePostChangesDiscard(w http.ResponseWriter, req *http.Request) {
	if err := r.store.DiscardPendingChanges(); err != nil {
		respondError(w, http.StatusInternalServerError, "discard_error", fmt.Sprintf("丢弃变更失败: %v", err))
		return
	}

	respondJSON(w, http.StatusOK, map[string]any{
		"status":  "success",
		"message": "变更已丢弃",
	})
}
