package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/ZhiYi-R/moon-bridge/internal/service/store"
)

// ---- Routes ----

// GET /routes
func (r *Router) handleListRoutes(w http.ResponseWriter, req *http.Request) {
	p := parsePagination(req)

	cfg := r.runtime.Current()

	type routeItem struct {
		Alias       string `json:"alias"`
		Model       string `json:"model"`
		Provider    string `json:"provider"`
		DisplayName string `json:"display_name,omitempty"`
	}

	aliases := make([]string, 0, len(cfg.Config.Routes))
	for alias := range cfg.Config.Routes {
		aliases = append(aliases, alias)
	}
	sortStrings(aliases)

	total := len(aliases)

	sliceEnd := p.Offset + p.Limit
	if p.Offset > len(aliases) {
		p.Offset = len(aliases)
	}
	if sliceEnd > len(aliases) {
		sliceEnd = len(aliases)
	}
	page := aliases[p.Offset:sliceEnd]

	items := make([]routeItem, 0, len(page))
	for _, alias := range page {
		route := cfg.Config.Routes[alias]
		items = append(items, routeItem{
			Alias:       alias,
			Model:       route.Model,
			Provider:    route.Provider,
			DisplayName: route.DisplayName,
		})
	}

	respondJSON(w, http.StatusOK, paginatedResponse{
		Data:   items,
		Total:  total,
		Limit:  p.Limit,
		Offset: p.Offset,
	})
}

// GET /routes/{alias}
func (r *Router) handleGetRoute(w http.ResponseWriter, req *http.Request) {
	alias := req.PathValue("alias")
	if alias == "" {
		respondError(w, http.StatusBadRequest, "invalid_alias", "无效的 route alias")
		return
	}

	cfg := r.runtime.Current()
	route, ok := cfg.Config.Routes[alias]
	if !ok {
		respondError(w, http.StatusNotFound, "not_found", fmt.Sprintf("route %q 不存在", alias))
		return
	}

	resp := map[string]any{
		"alias":          alias,
		"model":          route.Model,
		"provider":       route.Provider,
		"display_name":   route.DisplayName,
		"context_window": route.ContextWindow,
	}

	respondJSON(w, http.StatusOK, resp)
}

// PUT /routes/{alias}
func (r *Router) handlePutRoute(w http.ResponseWriter, req *http.Request) {
	alias := req.PathValue("alias")
	if alias == "" {
		respondError(w, http.StatusBadRequest, "invalid_alias", "无效的 route alias")
		return
	}

	var body struct {
		Model         string `json:"model"`
		Provider      string `json:"provider"`
		DisplayName   string `json:"display_name"`
		ContextWindow int    `json:"context_window"`
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
		"model_slug":     body.Model,
		"provider_key":   body.Provider,
		"display_name":   body.DisplayName,
		"context_window": body.ContextWindow,
	})

	chID, err := r.store.StageChange(store.ChangeRow{
		Action:    "create",
		Resource:  "route",
		TargetKey: alias,
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

// DELETE /routes/{alias}
func (r *Router) handleDeleteRoute(w http.ResponseWriter, req *http.Request) {
	alias := req.PathValue("alias")
	if alias == "" {
		respondError(w, http.StatusBadRequest, "invalid_alias", "无效的 route alias")
		return
	}

	cfg := r.runtime.Current()
	if _, ok := cfg.Config.Routes[alias]; !ok {
		respondError(w, http.StatusNotFound, "not_found", fmt.Sprintf("route %q 不存在", alias))
		return
	}

	chID, err := r.store.StageChange(store.ChangeRow{
		Action:    "delete",
		Resource:  "route",
		TargetKey: alias,
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
