package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/ZhiYi-R/moon-bridge/internal/service/configgraph"
)

type createConfigResourceRequest struct {
	BaseRevision string         `json:"baseRevision"`
	ID           string         `json:"id"`
	Value        map[string]any `json:"value"`
}

type deleteConfigResourceRequest struct {
	BaseRevision string `json:"baseRevision"`
}

func (r *Router) configGraphService() *configgraph.Service {
	svc := configgraph.NewService(r.store, r.runtime, slog.Default())
	if r.registry != nil {
		svc.WithExtensionSpecs(r.registry.ConfigSpecs())
	}
	return svc
}

func (r *Router) handleGetConfigGraph(w http.ResponseWriter, req *http.Request) {
	graph, err := r.configGraphService().Graph(req.Context())
	if err != nil {
		slog.Default().Error("get config graph failed", "error", err)
		respondError(w, http.StatusInternalServerError, "config_graph_error", fmt.Sprintf("读取配置图失败: %v", err))
		return
	}
	respondJSON(w, http.StatusOK, graph)
}

func (r *Router) handlePatchConfigGraph(w http.ResponseWriter, req *http.Request) {
	var body configgraph.PatchRequest
	if err := decodeStrictJSON(req, &body); err != nil {
		respondError(w, http.StatusBadRequest, "invalid_json", fmt.Sprintf("无效的 JSON 请求体: %v", err))
		return
	}

	resp, err := r.configGraphService().Patch(req.Context(), body)
	if err != nil {
		slog.Default().Error("patch config graph failed", "error", err)
		respondError(w, http.StatusInternalServerError, "config_graph_error", fmt.Sprintf("更新配置图失败: %v", err))
		return
	}
	respondConfigGraphPatch(w, resp)
}

func (r *Router) handleValidateConfigGraph(w http.ResponseWriter, req *http.Request) {
	var body configgraph.PatchRequest
	if err := decodeStrictJSON(req, &body); err != nil {
		respondError(w, http.StatusBadRequest, "invalid_json", fmt.Sprintf("无效的 JSON 请求体: %v", err))
		return
	}

	resp, err := r.configGraphService().Validate(req.Context(), body)
	if err != nil {
		slog.Default().Error("validate config graph failed", "error", err)
		respondError(w, http.StatusInternalServerError, "config_graph_error", fmt.Sprintf("验证配置图失败: %v", err))
		return
	}
	respondConfigGraphPatch(w, resp)
}

func (r *Router) handleCreateConfigResource(w http.ResponseWriter, req *http.Request) {
	kind, ok := parseResourceKind(req.PathValue("kind"))
	if !ok {
		respondError(w, http.StatusBadRequest, "invalid_resource_kind", "无效的配置资源类型")
		return
	}

	var body createConfigResourceRequest
	if err := decodeStrictJSON(req, &body); err != nil {
		respondError(w, http.StatusBadRequest, "invalid_json", fmt.Sprintf("无效的 JSON 请求体: %v", err))
		return
	}
	if body.Value == nil {
		body.Value = map[string]any{}
	}
	if conflict, handled := r.rejectCreateConflict(kind, body.ID, body.BaseRevision); handled {
		respondConfigGraphPatch(w, conflict)
		return
	}

	resp, err := r.configGraphService().CreateResource(req.Context(), kind, body.ID, body.Value)
	if err != nil {
		slog.Default().Error("create config graph resource failed", "kind", kind, "id", body.ID, "error", err)
		respondError(w, http.StatusInternalServerError, "config_graph_error", fmt.Sprintf("创建配置资源失败: %v", err))
		return
	}
	respondConfigGraphPatch(w, resp)
}

func (r *Router) handleDeleteConfigResource(w http.ResponseWriter, req *http.Request) {
	kind, ok := parseResourceKind(req.PathValue("kind"))
	if !ok {
		respondError(w, http.StatusBadRequest, "invalid_resource_kind", "无效的配置资源类型")
		return
	}
	id := req.PathValue("id")
	if id == "" {
		respondError(w, http.StatusBadRequest, "invalid_resource_id", "无效的配置资源 ID")
		return
	}

	baseRevision, err := deleteBaseRevision(req)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid_json", fmt.Sprintf("无效的 JSON 请求体: %v", err))
		return
	}
	resp, err := r.configGraphService().DeleteResource(req.Context(), kind, id, baseRevision)
	if err != nil {
		slog.Default().Error("delete config graph resource failed", "kind", kind, "id", id, "error", err)
		respondError(w, http.StatusInternalServerError, "config_graph_error", fmt.Sprintf("删除配置资源失败: %v", err))
		return
	}
	respondConfigGraphPatch(w, resp)
}

func respondConfigGraphPatch(w http.ResponseWriter, resp configgraph.PatchResponse) {
	switch resp.Result {
	case configgraph.ResultRevisionConflict:
		respondJSON(w, http.StatusConflict, resp)
	case configgraph.ResultValidationRejected, configgraph.ResultRuntimeRejected, configgraph.ResultDraftRejected:
		respondJSON(w, http.StatusBadRequest, resp)
	default:
		respondJSON(w, http.StatusOK, resp)
	}
}

func parseResourceKind(value string) (configgraph.ResourceKind, bool) {
	kind := configgraph.ResourceKind(value)
	for _, def := range configgraph.ResourceDefinitions() {
		if def.Kind == string(kind) {
			return kind, true
		}
	}
	return "", false
}

func (r *Router) rejectCreateConflict(kind configgraph.ResourceKind, id, baseRevision string) (configgraph.PatchResponse, bool) {
	revision, err := r.store.CurrentRevision()
	if err != nil {
		return configgraph.PatchResponse{}, false
	}
	if baseRevision != "" && baseRevision == revision {
		return configgraph.PatchResponse{}, false
	}
	return configgraph.PatchResponse{
		Result:   configgraph.ResultRevisionConflict,
		Revision: revision,
		Errors: []configgraph.FieldError{
			{
				ResourceKind: kind,
				ResourceID:   id,
				Code:         "revisionConflict",
				Message:      fmt.Sprintf("base revision %q does not match current revision %q", baseRevision, revision),
			},
		},
	}, true
}

func deleteBaseRevision(req *http.Request) (string, error) {
	baseRevision := req.URL.Query().Get("baseRevision")
	if baseRevision != "" {
		return baseRevision, nil
	}
	if req.ContentLength == 0 {
		return "", nil
	}

	var body deleteConfigResourceRequest
	if err := decodeStrictJSON(req, &body); err != nil {
		return "", err
	}
	return body.BaseRevision, nil
}

func decodeStrictJSON(req *http.Request, dst any) error {
	dec := json.NewDecoder(req.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	var trailing struct{}
	if err := dec.Decode(&trailing); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
	return errors.New("unexpected trailing JSON")
}
