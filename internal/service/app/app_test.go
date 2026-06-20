package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"moonbridge/internal/config"
	"moonbridge/internal/service/configgraph"
	"moonbridge/internal/service/provider"
	"moonbridge/internal/service/stats"
	mbtrace "moonbridge/internal/service/trace"
)

type probeWebSearchCandidateFunc func(context.Context, string, string) (bool, error)

func (fn probeWebSearchCandidateFunc) ProbeWebSearchCandidate(ctx context.Context, providerKey, upstreamModel string) (bool, error) {
	return fn(ctx, providerKey, upstreamModel)
}

func TestWelcomeMessage(t *testing.T) {
	want := "欢迎使用 Moon Bridge!"

	if got := WelcomeMessage(); got != want {
		t.Fatalf("WelcomeMessage() = %q, want %q", got, want)
	}
}

func TestRunWritesWelcomeMessage(t *testing.T) {
	var output bytes.Buffer

	Run(&output)

	want := "欢迎使用 Moon Bridge!\n"
	if got := output.String(); got != want {
		t.Fatalf("Run() wrote %q, want %q", got, want)
	}
}

func TestRunServerSeedsEmptyConfigStoreBeforeServingConfigGraph(t *testing.T) {
	addr := freeLoopbackAddr(t)
	dbPath := filepath.Join(t.TempDir(), "moonbridge.db")
	cfg := firstRunSQLiteConfig(t, addr, dbPath)
	var output bytes.Buffer

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- RunServer(ctx, cfg, &output)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-errCh:
			if err != nil {
				t.Fatalf("RunServer() after cancel error = %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("RunServer() did not stop after cancel")
		}
	})

	resp := waitForConfigGraph(t, "http://"+addr+"/api/v1/config/graph")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/v1/config/graph status = %d, want 200", resp.StatusCode)
	}

	var graph configgraph.Graph
	if err := json.NewDecoder(resp.Body).Decode(&graph); err != nil {
		t.Fatalf("decode config graph: %v", err)
	}
	if graph.Revision == "" {
		t.Fatal("config graph revision is empty after first-run seed")
	}
	for _, want := range []string{
		"Moon Bridge 监听于 " + addr,
		"Web Console: http://" + addr + "/console/",
	} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("RunServer output missing %q:\n%s", want, output.String())
		}
	}
}

func TestCaptureTraceDirectoriesUseSession(t *testing.T) {
	responseTracer := mbtrace.New(captureResponseTraceConfig(true))
	if got, want := responseTracer.Directory(), filepath.Join("data", "trace", "Capture", "Response", responseTracer.SessionID()); got != want {
		t.Fatalf("response trace directory = %q, want %q", got, want)
	}

	anthropicTracer := mbtrace.New(captureAnthropicTraceConfig(true))
	if got, want := anthropicTracer.Directory(), filepath.Join("data", "trace", "Capture", "Anthropic", anthropicTracer.SessionID()); got != want {
		t.Fatalf("anthropic trace directory = %q, want %q", got, want)
	}
}

func TestResolvePerProviderWebSearchDisabledByConfig(t *testing.T) {
	cfg := config.Config{
		ProviderDefs: map[string]config.ProviderDef{
			"default": {WebSearchSupport: config.WebSearchSupportDisabled},
		},
	}
	pm, err := provider.NewProviderManager(
		map[string]provider.ProviderConfig{
			"default": {BaseURL: "https://test.example.test", APIKey: "test-key"},
		},
		map[string]provider.ModelRoute{
			"moonbridge": {Name: "claude-test", Provider: "default"},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	resolvePerProviderWebSearch(context.Background(), cfg, pm)
	if got := pm.ResolvedWebSearch("default"); got != "disabled" {
		t.Fatalf("ResolvedWebSearch(default) = %q, want disabled", got)
	}
}

func TestResolvePerProviderWebSearchEnabledByConfig(t *testing.T) {
	cfg := config.Config{
		ProviderDefs: map[string]config.ProviderDef{
			"default": {WebSearchSupport: config.WebSearchSupportEnabled},
		},
	}
	pm, err := provider.NewProviderManager(
		map[string]provider.ProviderConfig{
			"default": {BaseURL: "https://test.example.test", APIKey: "test-key"},
		},
		map[string]provider.ModelRoute{
			"moonbridge": {Name: "claude-test", Provider: "default"},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	resolvePerProviderWebSearch(context.Background(), cfg, pm)
	if got := pm.ResolvedWebSearch("default"); got != "enabled" {
		t.Fatalf("ResolvedWebSearch(default) = %q, want enabled", got)
	}
}

func TestResolvePerProviderWebSearchOpenAIResponseEnabled(t *testing.T) {
	cfg := config.Config{
		WebSearchSupport: config.WebSearchSupportEnabled,
		ProviderDefs: map[string]config.ProviderDef{
			"openai": {},
		},
	}
	pm, err := provider.NewProviderManager(
		map[string]provider.ProviderConfig{
			"openai": {BaseURL: "https://openai.example.test", APIKey: "key", Protocol: config.ProtocolOpenAIResponse},
		},
		map[string]provider.ModelRoute{},
	)
	if err != nil {
		t.Fatal(err)
	}

	resolvePerProviderWebSearch(context.Background(), cfg, pm)
	if got := pm.ResolvedWebSearch("openai"); got != "enabled" {
		t.Fatalf("ResolvedWebSearch(openai) = %q, want enabled for openai-response protocol", got)
	}
}

func TestResolvePerProviderWebSearchFallsBackToGlobal(t *testing.T) {
	cfg := config.Config{
		WebSearchSupport: config.WebSearchSupportDisabled,
		ProviderDefs: map[string]config.ProviderDef{
			"default": {}, // no per-provider override
		},
	}
	pm, err := provider.NewProviderManager(
		map[string]provider.ProviderConfig{
			"default": {BaseURL: "https://test.example.test", APIKey: "test-key"},
		},
		map[string]provider.ModelRoute{
			"moonbridge": {Name: "claude-test", Provider: "default"},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	resolvePerProviderWebSearch(context.Background(), cfg, pm)
	if got := pm.ResolvedWebSearch("default"); got != "disabled" {
		t.Fatalf("ResolvedWebSearch(default) = %q, want disabled (from global fallback)", got)
	}
}

func TestResolvePerProviderWebSearchAppliesProviderCatalogModelOverride(t *testing.T) {
	cfg := config.Config{
		ProviderDefs: map[string]config.ProviderDef{
			"main": {
				WebSearchSupport: config.WebSearchSupportDisabled,
				Models: map[string]config.ModelMeta{
					"claude-test": {
						WebSearch: config.WebSearchConfig{Support: config.WebSearchSupportEnabled},
					},
				},
			},
		},
	}
	pm, err := provider.NewProviderManager(
		map[string]provider.ProviderConfig{
			"main": {
				BaseURL:          "https://test.example.test",
				APIKey:           "test-key",
				WebSearchSupport: string(config.WebSearchSupportDisabled),
				ModelNames:       []string{"claude-test"},
			},
		},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}

	resolvePerProviderWebSearch(context.Background(), cfg, pm)
	if got := pm.ResolvedWebSearch("main"); got != "disabled" {
		t.Fatalf("ResolvedWebSearch(main) = %q, want disabled", got)
	}
	if got := pm.ResolvedWebSearchForModel("main/claude-test"); got != "enabled" {
		t.Fatalf("ResolvedWebSearchForModel(main/claude-test) = %q, want enabled", got)
	}
}

func TestResolveModelWebSearchCandidateFallsBackToInjectedWhenUnsupported(t *testing.T) {
	cfg := config.Config{
		TavilyAPIKey: "tavily-key",
	}
	pm, err := provider.NewProviderManager(
		map[string]provider.ProviderConfig{
			"opencode": {
				BaseURL: "https://opencode.example.test",
				APIKey:  "key-opencode",
			},
		},
		map[string]provider.ModelRoute{
			"deepseek-v4-pro": {Provider: "opencode", Name: "deepseek-v4-pro"},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	resolved := resolveModelWebSearchWithProber(
		context.Background(),
		"deepseek-v4-pro",
		"opencode",
		"deepseek-v4-pro",
		config.WebSearchSupportAuto,
		pm,
		cfg,
		probeWebSearchCandidateFunc(func(context.Context, string, string) (bool, error) {
			return false, nil
		}),
	)

	if resolved != "injected" {
		t.Fatalf("resolveModelWebSearchWithProber() = %q, want injected", resolved)
	}
}

func TestResolveModelWebSearchCandidateKeepsEnabledWhenSupported(t *testing.T) {
	cfg := config.Config{}
	pm, err := provider.NewProviderManager(
		map[string]provider.ProviderConfig{
			"deepseek": {
				BaseURL: "https://deepseek.example.test",
				APIKey:  "key-deepseek",
			},
		},
		map[string]provider.ModelRoute{
			"deepseek-v4-flash": {Provider: "deepseek", Name: "deepseek-v4-flash"},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	resolved := resolveModelWebSearchWithProber(
		context.Background(),
		"deepseek-v4-flash",
		"deepseek",
		"deepseek-v4-flash",
		config.WebSearchSupportAuto,
		pm,
		cfg,
		probeWebSearchCandidateFunc(func(context.Context, string, string) (bool, error) {
			return true, nil
		}),
	)

	if resolved != "enabled" {
		t.Fatalf("resolveModelWebSearchWithProber() = %q, want enabled", resolved)
	}
}

func TestPricingIndexIncludesProviderModelSlugs(t *testing.T) {
	// Simulate the pricing setup from runTransform():
	// pricing should be indexed by both route aliases AND provider/model slugs.
	pricing := make(map[string]stats.ModelPricing)
	routes := map[string]config.RouteEntry{
		"moonbridge": {
			Provider:        "deepseek",
			Model:           "deepseek-v4-pro",
			InputPrice:      2,
			OutputPrice:     8,
			CacheWritePrice: 1,
			CacheReadPrice:  0.2,
		},
	}
	providerDefs := map[string]config.ProviderDef{
		"deepseek": {
			Models: map[string]config.ModelMeta{
				"deepseek-v4-pro": {
					InputPrice:      2,
					OutputPrice:     8,
					CacheWritePrice: 1,
					CacheReadPrice:  0.2,
				},
				"deepseek-v4-flash": {
					InputPrice:      1,
					OutputPrice:     2,
					CacheWritePrice: 0,
					CacheReadPrice:  0.02,
				},
			},
		},
	}

	// Step 1: Build pricing from route aliases (existing logic).
	for alias, route := range routes {
		if route.InputPrice > 0 || route.OutputPrice > 0 || route.CacheWritePrice > 0 || route.CacheReadPrice > 0 {
			pricing[alias] = stats.ModelPricing{
				InputPrice:      route.InputPrice,
				OutputPrice:     route.OutputPrice,
				CacheWritePrice: route.CacheWritePrice,
				CacheReadPrice:  route.CacheReadPrice,
			}
		}
	}

	// Step 2: Also index by provider/model slug (the fix).
	for providerKey, def := range providerDefs {
		for modelName, meta := range def.Models {
			slug := providerKey + "/" + modelName
			newSlug := modelName + "(" + providerKey + ")"
			if _, exists := pricing[slug]; exists {
				// route alias already has pricing; also index new format.
				if _, exists := pricing[newSlug]; !exists {
					pricing[newSlug] = pricing[slug]
				}
				continue
			}
			if meta.InputPrice > 0 || meta.OutputPrice > 0 || meta.CacheWritePrice > 0 || meta.CacheReadPrice > 0 {
				p := stats.ModelPricing{
					InputPrice:      meta.InputPrice,
					OutputPrice:     meta.OutputPrice,
					CacheWritePrice: meta.CacheWritePrice,
					CacheReadPrice:  meta.CacheReadPrice,
				}
				pricing[slug] = p
				pricing[newSlug] = p
			}
		}
	}

	sessionStats := stats.NewSessionStats()
	sessionStats.SetPricing(pricing)

	// Verify route alias pricing works.
	cost := sessionStats.ComputeCost("moonbridge", stats.Usage{
		InputTokens: 1000, OutputTokens: 500,
	})
	if cost <= 0 {
		t.Fatalf("ComputeCost(moonbridge, ...) = %f, want > 0", cost)
	}

	// Verify provider/model slug pricing works (this was the bug).
	cost = sessionStats.ComputeCost("deepseek/deepseek-v4-flash", stats.Usage{
		InputTokens: 1000, OutputTokens: 500,
	})
	if cost <= 0 {
		t.Fatalf("ComputeCost(deepseek/deepseek-v4-flash, ...) = %f, want > 0", cost)
	}

	// Verify deepseek-v4-pro also works via slug (and route pricing takes priority).

	// Verify model(provider) format pricing works.
	cost = sessionStats.ComputeCost("deepseek-v4-flash(deepseek)", stats.Usage{
		InputTokens: 1000, OutputTokens: 500,
	})
	if cost <= 0 {
		t.Fatalf("ComputeCost(deepseek-v4-flash(deepseek), ...) = %f, want > 0", cost)
	}

	cost = sessionStats.ComputeCost("deepseek/deepseek-v4-pro", stats.Usage{
		InputTokens: 1000, OutputTokens: 500,
	})
	if cost <= 0 {
		t.Fatalf("ComputeCost(deepseek/deepseek-v4-pro, ...) = %f, want > 0", cost)
	}

	// Verify Record() accumulates cost for provider/model slug.

	// Verify Record() works with model(provider) format.
	sessionStats.Record("deepseek-v4-flash(deepseek)", "", stats.Usage{
		InputTokens: 1000, OutputTokens: 200,
	})
	summary2 := sessionStats.Summary()
	if _, ok := summary2.ByModel["deepseek-v4-flash(deepseek)"]; !ok {
		t.Fatal("Summary().ByModel missing deepseek-v4-flash(deepseek)")
	}
	if summary2.ByModel["deepseek-v4-flash(deepseek)"].Cost <= 0 {
		t.Fatalf("ByModel[deepseek-v4-flash(deepseek)].Cost = %f, want > 0", summary2.ByModel["deepseek-v4-flash(deepseek)"].Cost)
	}

	sessionStats.Record("deepseek/deepseek-v4-flash", "", stats.Usage{
		InputTokens: 1000, OutputTokens: 200,
	})
	summary := sessionStats.Summary()
	if summary.TotalCost <= 0 {
		t.Fatalf("Summary().TotalCost = %f, want > 0 after Record(deepseek/deepseek-v4-flash)", summary.TotalCost)
	}
	if _, ok := summary.ByModel["deepseek/deepseek-v4-flash"]; !ok {
		t.Fatal("Summary().ByModel missing deepseek/deepseek-v4-flash")
	}
	if summary.ByModel["deepseek/deepseek-v4-flash"].Cost <= 0 {
		t.Fatalf("ByModel[deepseek/deepseek-v4-flash].Cost = %f, want > 0", summary.ByModel["deepseek/deepseek-v4-flash"].Cost)
	}
}

func TestBuildProviderDefsFromConfigKeepsMultiProviderDefinitions(t *testing.T) {
	cfg := config.ProviderConfig{
		Providers: map[string]config.ProviderDef{
			"deepseek": {
				BaseURL: "https://deepseek.example.test",
				APIKey:  "deepseek-key",
			},
			"openai": {
				BaseURL:  "https://openai.example.test",
				APIKey:   "openai-key",
				Protocol: config.ProtocolOpenAIResponse,
			},
		},
	}

	defs := provider.BuildProviderDefsFromConfig(cfg)
	if len(defs) != 2 {
		t.Fatalf("defs = %+v", defs)
	}
	if defs["openai"].BaseURL != "https://openai.example.test" || defs["openai"].Protocol != config.ProtocolOpenAIResponse {
		t.Fatalf("openai def = %+v", defs["openai"])
	}
	if defs["deepseek"].BaseURL != "https://deepseek.example.test" {
		t.Fatalf("deepseek def = %+v", defs["deepseek"])
	}
}

func freeLoopbackAddr(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen free loopback addr: %v", err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("close free loopback listener: %v", err)
	}
	return addr
}

func firstRunSQLiteConfig(t *testing.T, addr string, dbPath string) config.Config {
	t.Helper()
	enabled := true
	cfg, err := config.FromFileConfigWithOptions(config.FileConfig{
		Mode: string(config.ModeTransform),
		Server: config.ServerFileConfig{
			Addr: addr,
		},
		Defaults: config.DefaultsFileConfig{
			Model:     "moonbridge",
			MaxTokens: 1024,
		},
		Models: map[string]config.ModelDefFileConfig{
			"local-test-model": {
				ContextWindow:   128000,
				MaxOutputTokens: 4096,
			},
		},
		Providers: map[string]config.ProviderDefFileConfig{
			"local": {
				BaseURL:  "https://api.example.invalid",
				APIKey:   "test-key",
				Protocol: config.ProtocolOpenAIChat,
				Offers: []config.OfferFileConfig{
					{
						Model:        "local-test-model",
						UpstreamName: "local-test-model",
					},
				},
			},
		},
		Routes: map[string]config.RouteFileConfig{
			"moonbridge": {
				Provider: "local",
				Model:    "local-test-model",
			},
		},
		Persistence: config.PersistenceFileConfig{
			ActiveProvider: "db_sqlite",
		},
		Extensions: map[string]config.ExtensionFileConfig{
			"db_sqlite": {
				Enabled: &enabled,
				Config: map[string]any{
					"path": dbPath,
					"wal":  false,
				},
			},
		},
	}, config.LoadOptions{ExtensionSpecs: BuiltinExtensions().ConfigSpecs()})
	if err != nil {
		t.Fatalf("build first-run sqlite config: %v", err)
	}
	return cfg
}

func waitForConfigGraph(t *testing.T, url string) *http.Response {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			return resp
		}
		lastErr = err
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("GET %s did not become available: %v", url, lastErr)
	return nil
}
