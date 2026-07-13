package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ZhiYi-R/moon-bridge/internal/config"
	"github.com/ZhiYi-R/moon-bridge/internal/db"
	"github.com/ZhiYi-R/moon-bridge/internal/extension/plugin"
	"github.com/ZhiYi-R/moon-bridge/internal/service/runtime"
	"github.com/ZhiYi-R/moon-bridge/internal/service/stats"
	"github.com/ZhiYi-R/moon-bridge/internal/service/store"

	_ "modernc.org/sqlite"
)

// testFixture provides a test environment with a real store, runtime, and HTTP router.
type testFixture struct {
	t       *testing.T
	store   store.ConfigStore
	rt      *runtime.Runtime
	handler http.Handler
}

type fixtureOptions struct {
	sessionStats *stats.SessionStats
	registry     *plugin.Registry
	mutateConfig func(*config.Config)
}

// newFixture creates a test fixture for API e2e tests.
func newFixture(t *testing.T) *testFixture {
	return newFixtureWithOptions(t, fixtureOptions{})
}

func newFixtureWithStats(t *testing.T, sessionStats *stats.SessionStats) *testFixture {
	return newFixtureWithOptions(t, fixtureOptions{sessionStats: sessionStats})
}

func newFixtureWithOptions(t *testing.T, opts fixtureOptions) *testFixture {
	t.Helper()

	cfg := config.Config{
		Mode: config.ModeTransform,
		Defaults: config.Defaults{
			Model: "claude-sonnet",
		},
		Models: map[string]config.ModelDef{
			"claude-sonnet": {
				ContextWindow: 200000,
				DisplayName:   "Claude Sonnet",
			},
			"gpt-4o": {
				ContextWindow: 128000,
				DisplayName:   "GPT-4o",
			},
		},
		ProviderDefs: map[string]config.ProviderDef{
			"anthropic": {
				BaseURL:  "https://api.anthropic.com",
				APIKey:   "sk-ant-test-key-12345678",
				Version:  "2023-06-01",
				Protocol: "anthropic",
				Models: map[string]config.ModelMeta{
					"claude-sonnet-20241022": {
						ContextWindow: 200000,
						InputPrice:    3.0,
						OutputPrice:   15.0,
					},
				},
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
				Provider: "anthropic",
				Model:    "claude-sonnet-20241022",
			},
		},
	}
	if opts.mutateConfig != nil {
		opts.mutateConfig(&cfg)
	}

	rt := runtime.NewRuntime(cfg, nil, nil)

	// Build in-memory SQLite store.
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open error = %v", err)
	}
	t.Cleanup(func() { database.Close() })

	c := store.NewConfigStoreConsumer(nil)
	if opts.registry != nil {
		c.SetExtensionSpecs(opts.registry.ConfigSpecs())
	}
	tables := c.Tables()
	for _, tbl := range tables {
		realName := "config_store_" + tbl.Name
		ddl := strings.ReplaceAll(tbl.Schema, "{{table}}", realName)
		if _, err := database.ExecContext(context.Background(), ddl); err != nil {
			t.Fatalf("create table %q: %v", realName, err)
		}
	}

	// Create a minimal db.Store wrapper around the in-memory DB.
	ts := &testAPIDB{t: t, db: database, consumer: "config_store", tableNames: buildTableNameMap(tables)}
	if err := c.BindStore(ts); err != nil {
		t.Fatalf("BindStore error = %v", err)
	}

	s := c.Store()
	if s == nil {
		t.Fatal("Store() returned nil")
	}

	if err := s.SeedFromConfig(&cfg); err != nil {
		t.Fatalf("SeedFromConfig error = %v", err)
	}

	// Mock server to satisfy the interface required by NewRouter.
	srv := &testServer{rt: rt}
	handler := NewRouter(s, rt, opts.sessionStats, opts.registry, srv)

	return &testFixture{t: t, store: s, rt: rt, handler: handler}
}

func buildTableNameMap(tables []db.TableSpec) map[string]string {
	m := make(map[string]string, len(tables))
	for _, tbl := range tables {
		m[tbl.Name] = "config_store_" + tbl.Name
	}
	return m
}

// testAPIDB implements db.Store backed by an in-memory SQLite database for API tests.
type testAPIDB struct {
	t          *testing.T
	db         *sql.DB
	consumer   string
	tableNames map[string]string
}

func (s *testAPIDB) ConsumerName() string { return s.consumer }
func (s *testAPIDB) Dialect() db.Dialect  { return db.DialectSQLite }
func (s *testAPIDB) Table(localName string) (string, error) {
	realName, ok := s.tableNames[localName]
	if !ok {
		return "", db.ErrTableNotRegistered
	}
	return realName, nil
}
func (s *testAPIDB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return s.db.ExecContext(ctx, query, args...)
}
func (s *testAPIDB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return s.db.QueryContext(ctx, query, args...)
}
func (s *testAPIDB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return s.db.QueryRowContext(ctx, query, args...)
}
func (s *testAPIDB) WithTx(ctx context.Context, fn func(db.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	btx := &testAPITx{tx: tx, tableNames: s.tableNames}
	if err := fn(btx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

type testAPITx struct {
	tx         *sql.Tx
	tableNames map[string]string
}

func (t *testAPITx) Table(localName string) (string, error) {
	realName, ok := t.tableNames[localName]
	if !ok {
		return "", db.ErrTableNotRegistered
	}
	return realName, nil
}
func (t *testAPITx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return t.tx.ExecContext(ctx, query, args...)
}
func (t *testAPITx) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return t.tx.QueryContext(ctx, query, args...)
}
func (t *testAPITx) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return t.tx.QueryRowContext(ctx, query, args...)
}

// testServer is a minimal mock for the server interface required by NewRouter.
type testServer struct {
	rt *runtime.Runtime
}

func (ts *testServer) ListSessions() []SessionInfo {
	return nil
}

type configAccessorWrapper struct {
	cfg config.Config
}

func (w configAccessorWrapper) AuthToken() string {
	return w.cfg.AuthToken
}

func (ts *testServer) CurrentConfig() ConfigAccessor {
	return configAccessorWrapper{cfg: ts.rt.Current().Config}
}

// request sends an HTTP request to the test server.
func (f *testFixture) request(method, path string, body any) *httptest.ResponseRecorder {
	var reqBody *strings.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		reqBody = strings.NewReader(string(data))
	} else {
		reqBody = strings.NewReader("")
	}
	req := httptest.NewRequest(method, "http://moonbridge.test"+path, reqBody)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, req)
	return w
}

// decode unmarshals a JSON response body into the given destination.
func (f *testFixture) decode(resp *httptest.ResponseRecorder, dest any) {
	if err := json.Unmarshal(resp.Body.Bytes(), dest); err != nil {
		f.t.Fatalf("decode response: %v", err)
	}
}

// TestDeleteProviderStageChange verifies staging a provider delete change works.
func TestDeleteProviderStageChange(t *testing.T) {
	f := newFixture(t)

	resp := f.request("DELETE", "/providers/anthropic", nil)
	if resp.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", resp.Code, resp.Body.String())
	}
}

func TestGetStatsUsageReturnsStableUsageDTO(t *testing.T) {
	sessionStats := stats.NewSessionStats()
	sessionStats.SetPricing(map[string]stats.ModelPricing{
		"claude-sonnet": {
			InputPrice:      3,
			OutputPrice:     15,
			CacheWritePrice: 3.75,
			CacheReadPrice:  0.3,
		},
	})
	sessionStats.RecordBilling("claude-sonnet", "claude-3-5-sonnet-20241022", stats.BillingUsage{
		FreshInputTokens:         100,
		CacheCreationInputTokens: 20,
		CacheReadInputTokens:     80,
		OutputTokens:             50,
	})
	f := newFixtureWithStats(t, sessionStats)

	resp := f.request("GET", "/stats/usage", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}

	var payload struct {
		Totals struct {
			Requests       int64   `json:"requests"`
			InputTokens    int64   `json:"input_tokens"`
			OutputTokens   int64   `json:"output_tokens"`
			CacheCreation  int64   `json:"cache_creation"`
			CacheRead      int64   `json:"cache_read"`
			CacheHitRate   float64 `json:"cache_hit_rate"`
			CacheWriteRate float64 `json:"cache_write_rate"`
			CacheRWRatio   float64 `json:"cache_rw_ratio"`
			TotalCost      float64 `json:"total_cost"`
			Duration       string  `json:"duration"`
		} `json:"totals"`
		ByModel []struct {
			Model             string  `json:"model"`
			ActualModel       string  `json:"actual_model"`
			Requests          int64   `json:"requests"`
			InputTokens       int64   `json:"input_tokens"`
			OutputTokens      int64   `json:"output_tokens"`
			CacheCreation     int64   `json:"cache_creation"`
			CacheRead         int64   `json:"cache_read"`
			CacheHitRate      float64 `json:"cache_hit_rate"`
			Cost              float64 `json:"cost"`
			AvgCostPerMTokens float64 `json:"avg_cost_per_mtoken"`
		} `json:"by_model"`
	}
	f.decode(resp, &payload)

	if payload.Totals.Requests != 1 ||
		payload.Totals.InputTokens != 200 ||
		payload.Totals.OutputTokens != 50 ||
		payload.Totals.CacheCreation != 20 ||
		payload.Totals.CacheRead != 80 {
		t.Fatalf("unexpected totals: %+v", payload.Totals)
	}
	if payload.Totals.CacheHitRate != 40 || payload.Totals.CacheWriteRate != 10 || payload.Totals.CacheRWRatio != 4 {
		t.Fatalf("unexpected cache rates: %+v", payload.Totals)
	}
	if payload.Totals.TotalCost <= 0 {
		t.Fatalf("total_cost should be positive: %+v", payload.Totals)
	}
	if payload.Totals.Duration == "" {
		t.Fatal("duration should be formatted")
	}
	if len(payload.ByModel) != 1 {
		t.Fatalf("expected one model row, got %d", len(payload.ByModel))
	}
	row := payload.ByModel[0]
	if row.Model != "claude-sonnet" || row.ActualModel != "claude-3-5-sonnet-20241022" {
		t.Fatalf("unexpected model names: %+v", row)
	}
	if row.Requests != 1 || row.InputTokens != 200 || row.OutputTokens != 50 || row.CacheCreation != 20 || row.CacheRead != 80 {
		t.Fatalf("unexpected model row totals: %+v", row)
	}
	if row.CacheHitRate != 40 {
		t.Fatalf("unexpected model cache hit rate: %+v", row)
	}
	if row.Cost <= 0 || row.AvgCostPerMTokens <= 0 {
		t.Fatalf("expected model cost metrics: %+v", row)
	}
}

func TestGetStatsUsageWithoutStatsReturnsEmptyModelArray(t *testing.T) {
	f := newFixtureWithStats(t, nil)

	resp := f.request("GET", "/stats/usage", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}

	var payload struct {
		Totals struct {
			Requests int64 `json:"requests"`
		} `json:"totals"`
		ByModel json.RawMessage `json:"by_model"`
	}
	f.decode(resp, &payload)

	if payload.Totals.Requests != 0 {
		t.Fatalf("expected zero totals when stats are unavailable, got %+v", payload.Totals)
	}
	if string(payload.ByModel) != "[]" {
		t.Fatalf("expected by_model to be an empty JSON array, got %s", string(payload.ByModel))
	}
}
