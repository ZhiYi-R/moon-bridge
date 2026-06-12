package metrics_test

import (
	"context"
	"database/sql"
	"net/http"
	"testing"
	"time"

	"moonbridge/internal/config"
	"moonbridge/internal/db"
	mbtrics "moonbridge/internal/extension/metrics"
	"moonbridge/internal/extension/plugin"

	_ "modernc.org/sqlite"
)

func TestName(t *testing.T) {
	p := mbtrics.NewPlugin()
	if p.Name() != "metrics" {
		t.Fatalf("Name() = %q, want %q", p.Name(), "metrics")
	}
}

func TestEnabledForModel(t *testing.T) {
	p := mbtrics.NewPlugin()
	// EnabledForModel should be false when disabled (no AppConfig set)
	if p.EnabledForModel("any-model") {
		t.Fatal("EnabledForModel should be false when disabled via config")
	}
}

func TestConfigSpecs(t *testing.T) {
	specs := mbtrics.ConfigSpecs()
	if len(specs) != 1 {
		t.Fatalf("ConfigSpecs returned %d specs, want 1", len(specs))
	}
	spec := specs[0]
	if spec.Name != "metrics" {
		t.Fatalf("spec.Name = %q, want %q", spec.Name, "metrics")
	}
	if spec.Factory == nil {
		t.Fatal("spec.Factory should not be nil")
	}
	cfg := spec.Factory()
	if _, ok := cfg.(*mbtrics.Config); !ok {
		t.Fatalf("Factory returned %T, want *Config", cfg)
	}
}

func TestDBConsumerAlwaysNonNil(t *testing.T) {
	p := mbtrics.NewPlugin()
	if p.DBConsumer() == nil {
		t.Fatal("DBConsumer() should always return a consumer; DB registry handles availability")
	}
}

func TestTables(t *testing.T) {
	tables := mbtrics.MetricsTable()
	if tables.Name != "request_metrics" {
		t.Fatalf("Table name = %q, want %q", tables.Name, "request_metrics")
	}
	if tables.Schema == "" {
		t.Fatal("Schema should not be empty")
	}
	if len(tables.Indexes) != 3 {
		t.Fatalf("expected 3 indexes, got %d", len(tables.Indexes))
	}
}

func TestInitNoError(t *testing.T) {
	p := mbtrics.NewPlugin()
	ctx := plugin.PluginContext{
		AppConfig: config.Config{},
	}
	if err := p.Init(ctx); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
}

func TestShutdownNoError(t *testing.T) {
	p := mbtrics.NewPlugin()
	if err := p.Shutdown(); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
}

func TestInterfaceCompliance(t *testing.T) {
	p := mbtrics.NewPlugin()
	var _ plugin.Plugin = p
	var _ plugin.ConfigSpecProvider = p
	var _ plugin.RequestCompletionHook = p
	var _ plugin.RouteRegistrar = p
	var _ plugin.DBConsumer = p
}

func TestOnRequestCompletedNilStore(t *testing.T) {
	p := mbtrics.NewPlugin()
	p.OnRequestCompleted(nil, plugin.RequestResult{
		Model:       "test",
		InputTokens: 100,
		Status:      "success",
	})
}

func TestOnRequestCompletedRecordsRawTelemetry(t *testing.T) {
	p := mbtrics.NewPlugin()
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open error = %v", err)
	}
	t.Cleanup(func() { database.Close() })
	table := mbtrics.MetricsTable()
	if _, err := database.ExecContext(context.Background(), renderMetricsDDL(table.Schema)); err != nil {
		t.Fatalf("create metrics table error = %v", err)
	}
	if err := p.BindStore(&testStore{db: database}); err != nil {
		t.Fatalf("BindStore error = %v", err)
	}
	p.OnRequestCompleted(nil, plugin.RequestResult{
		Model:         "kimi",
		ActualModel:   "kimi-for-coding",
		ProviderKey:   "deepseek",
		InputTokens:   85822,
		OutputTokens:  145,
		CacheRead:     85248,
		Status:        "success",
		Duration:      15 * time.Millisecond,
		CacheCreation: 0,
		Usage: plugin.RequestUsage{
			Protocol:                "anthropic",
			UsageSource:             "anthropic_stream",
			RawInputTokens:          85822,
			RawOutputTokens:         145,
			RawCacheRead:            85248,
			NormalizedInputTokens:   85822,
			NormalizedOutputTokens:  145,
			NormalizedCacheRead:     85248,
			RawUsageJSON:            []byte(`{"input_tokens":85822,"cache_read_input_tokens":85248}`),
			NormalizedCacheCreation: 0,
			RawCacheCreation:        0,
		},
	})
	store := mbtrics.NewStore(&testStore{db: database})
	records, err := store.Query(mbtrics.QueryOptions{Limit: 1})
	if err != nil {
		t.Fatalf("Query error = %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("records len = %d", len(records))
	}
	got := records[0]
	if got.UsageSource != "anthropic_stream" || got.RawCacheRead != 85248 || got.NormalizedInputTokens != 85822 || got.ProviderKey != "deepseek" {
		t.Fatalf("record = %+v", got)
	}
}

func TestRegisterRoutesNilStore(t *testing.T) {
	p := mbtrics.NewPlugin()
	called := false
	p.RegisterRoutes(func(pattern string, handler http.Handler) {
		called = true
	})
	if called {
		t.Fatal("RegisterRoutes should not register when store is nil")
	}
}

func TestDisablePersistence(t *testing.T) {
	p := mbtrics.NewPlugin()
	p.DisablePersistence(db.ErrNoProvider)
	// EnabledForModel should now return false
	if p.EnabledForModel("test") {
		t.Fatal("EnabledForModel should be false after DisablePersistence")
	}
}
