package api

import (
	"net/http"
	"testing"

	"github.com/ZhiYi-R/moon-bridge/internal/config"
	dbsqlite "github.com/ZhiYi-R/moon-bridge/internal/extension/db/sqlite"
	"github.com/ZhiYi-R/moon-bridge/internal/extension/plugin"
	"github.com/ZhiYi-R/moon-bridge/internal/service/configgraph"
)

func TestGetConfigGraphReturnsCurrentResources(t *testing.T) {
	f := newFixture(t)

	resp := f.request("GET", "/config/graph", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("GET /config/graph returned %d: %s", resp.Code, resp.Body.String())
	}

	var graph configgraph.Graph
	f.decode(resp, &graph)
	if graph.Revision == "" {
		t.Fatal("graph revision is empty")
	}
	assertGraphResource(t, graph, configgraph.ResourceMode, "main")
	assertGraphResource(t, graph, configgraph.ResourceProvider, "anthropic")
	assertGraphResource(t, graph, configgraph.ResourceModel, "claude-sonnet")
	assertGraphResource(t, graph, configgraph.ResourceRoute, "claude-sonnet")
}

func TestPatchConfigGraphCommitsDefaultsChange(t *testing.T) {
	f := newFixture(t)
	revision := currentGraphRevision(t, f)

	resp := f.request("PATCH", "/config/graph", configgraph.PatchRequest{
		BaseRevision: revision,
		Changes: []configgraph.PatchOp{
			{Kind: configgraph.ResourceDefaults, ID: "main", Field: "max_tokens", Value: 8192},
		},
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("PATCH /config/graph returned %d: %s", resp.Code, resp.Body.String())
	}

	var patch configgraph.PatchResponse
	f.decode(resp, &patch)
	if patch.Result != configgraph.ResultCommitted {
		t.Fatalf("Patch result = %q, want %q", patch.Result, configgraph.ResultCommitted)
	}
	if patch.Revision == "" || patch.Revision == revision {
		t.Fatalf("Patch revision = %q, want non-empty changed revision", patch.Revision)
	}

	graphResp := f.request("GET", "/config/graph", nil)
	if graphResp.Code != http.StatusOK {
		t.Fatalf("GET /config/graph after patch returned %d: %s", graphResp.Code, graphResp.Body.String())
	}
	var graph configgraph.Graph
	f.decode(graphResp, &graph)
	defaults := assertGraphResource(t, graph, configgraph.ResourceDefaults, "main")
	if got := int(defaults.Value["max_tokens"].(float64)); got != 8192 {
		t.Fatalf("defaults max_tokens = %d, want 8192", got)
	}
}

func TestPatchConfigGraphReturnsConflictForStaleRevision(t *testing.T) {
	f := newFixture(t)

	resp := f.request("PATCH", "/config/graph", configgraph.PatchRequest{
		BaseRevision: "stale-revision",
		Changes: []configgraph.PatchOp{
			{Kind: configgraph.ResourceDefaults, ID: "main", Field: "max_tokens", Value: 8192},
		},
	})
	if resp.Code != http.StatusConflict {
		t.Fatalf("PATCH /config/graph stale returned %d: %s", resp.Code, resp.Body.String())
	}

	var patch configgraph.PatchResponse
	f.decode(resp, &patch)
	if patch.Result != configgraph.ResultRevisionConflict {
		t.Fatalf("Patch result = %q, want %q", patch.Result, configgraph.ResultRevisionConflict)
	}
}

func TestPatchConfigGraphReturnsValidationErrorForInvalidRouteProvider(t *testing.T) {
	f := newFixture(t)
	revision := currentGraphRevision(t, f)

	resp := f.request("PATCH", "/config/graph", configgraph.PatchRequest{
		BaseRevision: revision,
		Changes: []configgraph.PatchOp{
			{Kind: configgraph.ResourceRoute, ID: "claude-sonnet", Field: "provider", Value: "missing-provider"},
		},
	})
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("PATCH /config/graph invalid route returned %d: %s", resp.Code, resp.Body.String())
	}

	var patch configgraph.PatchResponse
	f.decode(resp, &patch)
	if patch.Result != configgraph.ResultValidationRejected {
		t.Fatalf("Patch result = %q, want %q", patch.Result, configgraph.ResultValidationRejected)
	}
	if len(patch.Errors) == 0 {
		t.Fatal("Patch errors is empty")
	}
}

func TestValidateConfigGraphReturnsCandidateWithoutCommit(t *testing.T) {
	f := newFixture(t)
	revision := currentGraphRevision(t, f)

	resp := f.request("POST", "/config/graph/validate", configgraph.PatchRequest{
		BaseRevision: revision,
		Changes: []configgraph.PatchOp{
			{Kind: configgraph.ResourceDefaults, ID: "main", Field: "model", Value: "gpt-4o"},
		},
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("POST /config/graph/validate returned %d: %s", resp.Code, resp.Body.String())
	}

	var patch configgraph.PatchResponse
	f.decode(resp, &patch)
	if patch.Result != configgraph.ResultCommitted {
		t.Fatalf("Validate result = %q, want %q", patch.Result, configgraph.ResultCommitted)
	}
	if patch.Revision != revision {
		t.Fatalf("Validate revision = %q, want unchanged %q", patch.Revision, revision)
	}
	if patch.Graph == nil {
		t.Fatal("Validate graph is nil")
	}
	defaults := assertGraphResource(t, *patch.Graph, configgraph.ResourceDefaults, "main")
	if got := defaults.Value["model"]; got != "gpt-4o" {
		t.Fatalf("candidate defaults model = %q, want gpt-4o", got)
	}

	graphResp := f.request("GET", "/config/graph", nil)
	if graphResp.Code != http.StatusOK {
		t.Fatalf("GET /config/graph after validate returned %d: %s", graphResp.Code, graphResp.Body.String())
	}
	var graph configgraph.Graph
	f.decode(graphResp, &graph)
	if graph.Revision != revision {
		t.Fatalf("Graph revision after validate = %q, want unchanged %q", graph.Revision, revision)
	}
	defaults = assertGraphResource(t, graph, configgraph.ResourceDefaults, "main")
	if got := defaults.Value["model"]; got != "claude-sonnet" {
		t.Fatalf("committed defaults model = %q, want unchanged claude-sonnet", got)
	}
}

func TestCreateConfigResourceUsesRegistryExtensionSpecs(t *testing.T) {
	registry := plugin.NewRegistry(nil)
	registry.Register(dbsqlite.NewPlugin())
	enabled := true
	f := newFixtureWithOptions(t, fixtureOptions{
		registry: registry,
		mutateConfig: func(cfg *config.Config) {
			cfg.Extensions = map[string]config.ExtensionSettings{
				dbsqlite.PluginName: {
					Enabled: &enabled,
					RawConfig: map[string]any{
						"path": ":memory:",
					},
				},
			}
		},
	})
	revision := currentGraphRevision(t, f)

	resp := f.request("POST", "/config/resources/model", map[string]any{
		"baseRevision": revision,
		"id":           "review-model",
		"value": map[string]any{
			"display_name":   "Review Model",
			"context_window": 128000,
		},
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("POST /config/resources/model returned %d: %s", resp.Code, resp.Body.String())
	}

	var patch configgraph.PatchResponse
	f.decode(resp, &patch)
	if patch.Result != configgraph.ResultCommitted {
		t.Fatalf("Create resource result = %q, want %q; errors=%v", patch.Result, configgraph.ResultCommitted, patch.Errors)
	}
	if _, ok := f.rt.Current().Config.Models["review-model"]; !ok {
		t.Fatal("runtime config missing created model")
	}
}

func currentGraphRevision(t *testing.T, f *testFixture) string {
	t.Helper()
	resp := f.request("GET", "/config/graph", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("GET /config/graph returned %d: %s", resp.Code, resp.Body.String())
	}
	var graph configgraph.Graph
	f.decode(resp, &graph)
	return graph.Revision
}

func assertGraphResource(t *testing.T, graph configgraph.Graph, kind configgraph.ResourceKind, id string) configgraph.Resource {
	t.Helper()
	for _, resource := range graph.Resources {
		if resource.Kind == kind && resource.ID == id {
			return resource
		}
	}
	t.Fatalf("missing graph resource %s/%s", kind, id)
	return configgraph.Resource{}
}
