package configgraph

import (
	"context"
	"errors"
	"testing"

	"github.com/ZhiYi-R/moon-bridge/internal/config"
	dbsqlite "github.com/ZhiYi-R/moon-bridge/internal/extension/db/sqlite"
	runtimepkg "github.com/ZhiYi-R/moon-bridge/internal/service/runtime"
)

func TestBuildGraphIncludesAllConfigSections(t *testing.T) {
	cfg := testConfig()
	graph := BuildGraph(cfg, "rev-1")

	assertResource(t, graph, ResourceMode, "main")
	assertResource(t, graph, ResourceTrace, "main")
	assertResource(t, graph, ResourceLog, "main")
	assertResource(t, graph, ResourceServer, "main")
	assertResource(t, graph, ResourceDefaults, "main")
	assertResource(t, graph, ResourceModel, "claude-sonnet")
	assertResource(t, graph, ResourceProvider, "anthropic")
	assertResource(t, graph, ResourceProviderOffer, "anthropic/claude-sonnet")
	assertResource(t, graph, ResourceRoute, "claude-sonnet")
	assertResource(t, graph, ResourceWebSearch, "main")
	assertResource(t, graph, ResourceCache, "main")
	assertResource(t, graph, ResourcePersistence, "main")
	assertResource(t, graph, ResourceProxy, "main")
}

func TestBuildGraphMasksSecrets(t *testing.T) {
	graph := BuildGraph(testConfig(), "rev-1")
	provider := assertResource(t, graph, ResourceProvider, "anthropic")
	if provider.Value["api_key"] == "sk-ant-test-key" {
		t.Fatal("provider api_key leaked")
	}

	server := assertResource(t, graph, ResourceServer, "main")
	if server.Value["auth_token"] == "console-token" {
		t.Fatal("server auth_token leaked")
	}

	search := assertResource(t, graph, ResourceWebSearch, "main")
	if search.Value["tavily_api_key"] == "tvly-test-key" {
		t.Fatal("web_search tavily_api_key leaked")
	}
}

func TestBuildGraphReportsRevisionAndCapabilities(t *testing.T) {
	graph := BuildGraph(testConfig(), "rev-1")
	if graph.Revision != "rev-1" {
		t.Fatalf("Revision = %q, want rev-1", graph.Revision)
	}
	if !graph.Validation.Valid {
		t.Fatal("Validation.Valid = false, want true")
	}
	if !graph.Capabilities.Autosave {
		t.Fatal("Capabilities.Autosave = false, want true")
	}
	if !graph.Capabilities.Logs {
		t.Fatal("Capabilities.Logs = false, want true")
	}
}

func TestServiceGraphReturnsStoreRevisionAndRuntimeResources(t *testing.T) {
	svc, _, _ := newServiceForTest(testConfig(), "rev-1")

	graph, err := svc.Graph(context.Background())
	if err != nil {
		t.Fatalf("Graph() error = %v", err)
	}
	if graph.Revision != "rev-1" {
		t.Fatalf("Graph().Revision = %q, want rev-1", graph.Revision)
	}
	assertResource(t, graph, ResourceDefaults, "main")
	assertResource(t, graph, ResourceProvider, "anthropic")
}

func TestServicePatchRejectsStaleRevisionWithoutSaveOrReload(t *testing.T) {
	svc, store, rt := newServiceForTest(testConfig(), "rev-current")

	resp, err := svc.Patch(context.Background(), PatchRequest{
		BaseRevision: "rev-stale",
		Changes: []PatchOp{
			{Kind: ResourceDefaults, ID: mainResourceID, Field: "max_tokens", Value: 8192},
		},
	})

	if err != nil {
		t.Fatalf("Patch() error = %v", err)
	}
	if resp.Result != ResultRevisionConflict {
		t.Fatalf("Patch().Result = %q, want %q", resp.Result, ResultRevisionConflict)
	}
	if resp.Revision != "rev-current" {
		t.Fatalf("Patch().Revision = %q, want rev-current", resp.Revision)
	}
	if store.saveCalls != 0 {
		t.Fatalf("SaveConfig calls = %d, want 0", store.saveCalls)
	}
	if rt.validateCalls != 0 || rt.reloadCalls != 0 {
		t.Fatalf("runtime calls validate=%d reload=%d, want 0/0", rt.validateCalls, rt.reloadCalls)
	}
}

func TestServicePatchRejectsInvalidSchemaWithoutSaveOrReload(t *testing.T) {
	svc, store, rt := newServiceForTest(testConfig(), "rev-1")

	resp, err := svc.Patch(context.Background(), PatchRequest{
		BaseRevision: "rev-1",
		Changes: []PatchOp{
			{Kind: ResourceRoute, ID: "claude-sonnet", Field: "priority", Value: 1},
		},
	})

	if err != nil {
		t.Fatalf("Patch() error = %v", err)
	}
	if resp.Result != ResultValidationRejected {
		t.Fatalf("Patch().Result = %q, want %q", resp.Result, ResultValidationRejected)
	}
	if len(resp.Errors) != 1 {
		t.Fatalf("Patch().Errors length = %d, want 1", len(resp.Errors))
	}
	if store.saveCalls != 0 || rt.reloadCalls != 0 {
		t.Fatalf("save/reload calls = %d/%d, want 0/0", store.saveCalls, rt.reloadCalls)
	}
}

func TestServicePatchCommitsHotReloadableChange(t *testing.T) {
	svc, store, rt := newServiceForTest(testConfig(), "rev-1")
	store.nextRevision = "rev-2"

	resp, err := svc.Patch(context.Background(), PatchRequest{
		BaseRevision: "rev-1",
		Changes: []PatchOp{
			{Kind: ResourceDefaults, ID: mainResourceID, Field: "max_tokens", Value: 8192},
		},
	})

	if err != nil {
		t.Fatalf("Patch() error = %v", err)
	}
	if resp.Result != ResultCommitted {
		t.Fatalf("Patch().Result = %q, want %q", resp.Result, ResultCommitted)
	}
	if resp.Revision != "rev-2" {
		t.Fatalf("Patch().Revision = %q, want rev-2", resp.Revision)
	}
	if store.saveCalls != 1 || rt.validateCalls != 1 || rt.reloadCalls != 1 {
		t.Fatalf("save/validate/reload calls = %d/%d/%d, want 1/1/1", store.saveCalls, rt.validateCalls, rt.reloadCalls)
	}
	if store.cfg.Defaults.MaxTokens != 8192 {
		t.Fatalf("stored Defaults.MaxTokens = %d, want 8192", store.cfg.Defaults.MaxTokens)
	}
	if rt.current.Defaults.MaxTokens != 8192 {
		t.Fatalf("runtime Defaults.MaxTokens = %d, want 8192", rt.current.Defaults.MaxTokens)
	}
}

func TestServicePatchReturnsRuntimeRejectedForCriticalRuntimeFailure(t *testing.T) {
	svc, store, rt := newServiceForTest(testConfig(), "rev-1")
	rt.validateErr = errors.New("candidate rejected")
	before := rt.Current()

	resp, err := svc.Patch(context.Background(), PatchRequest{
		BaseRevision: "rev-1",
		Changes: []PatchOp{
			{Kind: ResourceProvider, ID: "anthropic", Field: "base_url", Value: "https://rejected.example.test"},
		},
	})

	if err != nil {
		t.Fatalf("Patch() error = %v", err)
	}
	if resp.Result != ResultRuntimeRejected {
		t.Fatalf("Patch().Result = %q, want %q", resp.Result, ResultRuntimeRejected)
	}
	if resp.Revision != "rev-1" {
		t.Fatalf("Patch().Revision = %q, want rev-1", resp.Revision)
	}
	if len(resp.Errors) != 1 {
		t.Fatalf("Patch().Errors length = %d, want 1", len(resp.Errors))
	}
	if store.saveCalls != 0 || rt.reloadCalls != 0 {
		t.Fatalf("save/reload calls = %d/%d, want 0/0", store.saveCalls, rt.reloadCalls)
	}
	if rt.Current().Config.ProviderDefs["anthropic"].BaseURL != before.Config.ProviderDefs["anthropic"].BaseURL {
		t.Fatal("runtime current config changed after rejected critical candidate")
	}
}

func TestServicePatchReturnsDraftRejectedForNormalRuntimeFailure(t *testing.T) {
	svc, store, rt := newServiceForTest(testConfig(), "rev-1")
	rt.validateErr = errors.New("candidate rejected")

	resp, err := svc.Patch(context.Background(), PatchRequest{
		BaseRevision: "rev-1",
		Changes: []PatchOp{
			{Kind: ResourceDefaults, ID: mainResourceID, Field: "max_tokens", Value: 8192},
		},
	})

	if err != nil {
		t.Fatalf("Patch() error = %v", err)
	}
	if resp.Result != ResultDraftRejected {
		t.Fatalf("Patch().Result = %q, want %q", resp.Result, ResultDraftRejected)
	}
	if store.saveCalls != 0 || rt.reloadCalls != 0 {
		t.Fatalf("save/reload calls = %d/%d, want 0/0", store.saveCalls, rt.reloadCalls)
	}
}

func TestServicePatchSavesRestartRequiredChangeWithoutReload(t *testing.T) {
	cfg := testConfig()
	svc, store, rt := newServiceForTest(cfg, "rev-1")
	store.nextRevision = "rev-2"

	resp, err := svc.Patch(context.Background(), PatchRequest{
		BaseRevision: "rev-1",
		Changes: []PatchOp{
			{Kind: ResourceServer, ID: mainResourceID, Field: "auth_token", Value: "new-console-token"},
		},
	})

	if err != nil {
		t.Fatalf("Patch() error = %v", err)
	}
	if resp.Result != ResultRestartRequired {
		t.Fatalf("Patch().Result = %q, want %q", resp.Result, ResultRestartRequired)
	}
	if resp.Revision != "rev-2" {
		t.Fatalf("Patch().Revision = %q, want rev-2", resp.Revision)
	}
	if store.saveCalls != 1 || rt.validateCalls != 1 {
		t.Fatalf("save/validate calls = %d/%d, want 1/1", store.saveCalls, rt.validateCalls)
	}
	if rt.reloadCalls != 0 {
		t.Fatalf("Reload calls = %d, want 0", rt.reloadCalls)
	}
	if store.cfg.AuthToken != "new-console-token" {
		t.Fatalf("stored AuthToken = %q, want new-console-token", store.cfg.AuthToken)
	}
	if rt.current.AuthToken != cfg.AuthToken {
		t.Fatalf("runtime AuthToken = %q, want unchanged %q", rt.current.AuthToken, cfg.AuthToken)
	}
}

func TestServiceCreateResourceAcceptsExistingRegisteredExtensionConfig(t *testing.T) {
	cfg := testConfig()
	enabled := true
	cfg.Extensions = map[string]config.ExtensionSettings{
		dbsqlite.PluginName: {
			Enabled: &enabled,
			RawConfig: map[string]any{
				"path": ":memory:",
			},
		},
	}
	svc, store, rt := newServiceForTest(cfg, "rev-1")
	store.nextRevision = "rev-2"
	svc.WithExtensionSpecs(dbsqlite.ConfigSpecs())

	resp, err := svc.CreateResource(context.Background(), ResourceModel, "review-model", map[string]any{
		"display_name":   "Review Model",
		"context_window": 128000,
	})

	if err != nil {
		t.Fatalf("CreateResource() error = %v", err)
	}
	if resp.Result != ResultCommitted {
		t.Fatalf("CreateResource().Result = %q, want %q; errors=%v", resp.Result, ResultCommitted, resp.Errors)
	}
	if resp.Revision != "rev-2" {
		t.Fatalf("CreateResource().Revision = %q, want rev-2", resp.Revision)
	}
	if _, ok := store.cfg.Models["review-model"]; !ok {
		t.Fatal("stored config missing created model")
	}
	if _, ok := rt.current.Models["review-model"]; !ok {
		t.Fatal("runtime config missing created model after reload")
	}
}

func assertResource(t *testing.T, graph Graph, kind ResourceKind, id string) Resource {
	t.Helper()
	for _, r := range graph.Resources {
		if r.Kind == kind && r.ID == id {
			return r
		}
	}
	t.Fatalf("missing resource %s/%s", kind, id)
	return Resource{}
}

func newServiceForTest(cfg config.Config, revision string) (*Service, *fakeStore, *fakeRuntime) {
	store := &fakeStore{cfg: cfg, revision: revision}
	rt := &fakeRuntime{current: cfg}
	return NewService(store, rt, nil), store, rt
}

type fakeStore struct {
	cfg          config.Config
	revision     string
	nextRevision string
	saveCalls    int
	loadCalls    int
}

func (s *fakeStore) LoadAll() (*config.Config, error) {
	s.loadCalls++
	cfg := s.cfg
	return &cfg, nil
}

func (s *fakeStore) SaveConfig(_ context.Context, cfg *config.Config) (string, error) {
	s.saveCalls++
	s.cfg = *cfg
	if s.nextRevision != "" {
		s.revision = s.nextRevision
	} else {
		s.revision = s.revision + "-next"
	}
	return s.revision, nil
}

func (s *fakeStore) CurrentRevision() (string, error) {
	return s.revision, nil
}

type fakeRuntime struct {
	current       config.Config
	validateErr   error
	reloadErr     error
	validateCalls int
	reloadCalls   int
}

func (r *fakeRuntime) Current() *runtimepkg.ConfigSnapshot {
	return &runtimepkg.ConfigSnapshot{Config: r.current}
}

func (r *fakeRuntime) ValidateCandidate(config.Config) error {
	r.validateCalls++
	return r.validateErr
}

func (r *fakeRuntime) Reload(cfg config.Config) error {
	r.reloadCalls++
	if r.reloadErr != nil {
		return r.reloadErr
	}
	r.current = cfg
	return nil
}

func testConfig() config.Config {
	return config.Config{
		Mode:          config.ModeTransform,
		Addr:          "127.0.0.1:38440",
		AuthToken:     "console-token",
		TraceRequests: true,
		LogLevel:      "debug",
		LogFormat:     "text",
		Defaults: config.Defaults{
			Model:        "claude-sonnet",
			MaxTokens:    4096,
			SystemPrompt: "system",
		},
		Models: map[string]config.ModelDef{
			"claude-sonnet": {
				DisplayName:   "Claude Sonnet",
				ContextWindow: 200000,
			},
		},
		ProviderDefs: map[string]config.ProviderDef{
			"anthropic": {
				BaseURL:  "https://api.anthropic.com",
				APIKey:   "sk-ant-test-key",
				Version:  "2023-06-01",
				Protocol: config.ProtocolAnthropic,
				Offers: []config.OfferEntry{
					{
						Model:    "claude-sonnet",
						Priority: 1,
						Pricing: config.ModelPricing{
							InputPrice:  3.0,
							OutputPrice: 15.0,
						},
					},
				},
			},
		},
		Routes: map[string]config.RouteEntry{
			"claude-sonnet": {
				Provider:      "anthropic",
				Model:         "claude-sonnet",
				DisplayName:   "Claude Sonnet",
				ContextWindow: 200000,
			},
		},
		WebSearchSupport: config.WebSearchSupportEnabled,
		WebSearchMaxUses: 8,
		TavilyAPIKey:     "tvly-test-key",
		SearchMaxRounds:  5,
		Cache: config.CacheConfig{
			Mode:                    "automatic",
			TTL:                     "5m",
			PromptCaching:           true,
			AutomaticPromptCache:    true,
			AllowRetentionDowngrade: true,
		},
		Persistence: config.PersistenceConfig{
			ActiveProvider: "db_sqlite",
		},
		ResponseProxy: config.ResponseProxyConfig{
			ProviderBaseURL: "https://responses.example.test",
			ProviderAPIKey:  "response-key",
			Model:           "resp-model",
		},
		AnthropicProxy: config.AnthropicProxyConfig{
			ProviderBaseURL: "https://anthropic.example.test",
			ProviderAPIKey:  "anthropic-key",
			ProviderVersion: "2023-06-01",
			Model:           "anthropic-model",
		},
	}
}
