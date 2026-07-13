package metrics_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/ZhiYi-R/moon-bridge/internal/db"
	mbtrics "github.com/ZhiYi-R/moon-bridge/internal/extension/metrics"

	_ "modernc.org/sqlite"
)

type testStore struct {
	db *sql.DB
}

func newMetricsStore(t *testing.T) *mbtrics.Store {
	t.Helper()
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open error = %v", err)
	}
	t.Cleanup(func() { database.Close() })
	store := &testStore{db: database}
	table := mbtrics.MetricsTable()
	if _, err := database.ExecContext(context.Background(), renderMetricsDDL(table.Schema)); err != nil {
		t.Fatalf("create metrics table error = %v", err)
	}
	return mbtrics.NewStore(store)
}

func (s *testStore) ConsumerName() string { return "metrics" }
func (s *testStore) Dialect() db.Dialect  { return db.DialectSQLite }
func (s *testStore) Table(localName string) (string, error) {
	if localName != "request_metrics" {
		return "", db.ErrTableNotRegistered
	}
	return "metrics_request_metrics", nil
}
func (s *testStore) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return s.db.ExecContext(ctx, query, args...)
}
func (s *testStore) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return s.db.QueryContext(ctx, query, args...)
}
func (s *testStore) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return s.db.QueryRowContext(ctx, query, args...)
}
func (s *testStore) WithTx(context.Context, func(db.Tx) error) error { return db.ErrNotSupported }

func TestStoreRecordsRawAndNormalizedUsage(t *testing.T) {
	store := newMetricsStore(t)
	err := store.Record(mbtrics.Record{
		Timestamp:               time.Unix(10, 0),
		Model:                   "kimi",
		ActualModel:             "kimi-for-coding",
		ProviderKey:             "deepseek",
		InputTokens:             130,
		OutputTokens:            12,
		CacheCreation:           30,
		CacheRead:               90,
		Protocol:                "anthropic",
		UsageSource:             "anthropic_response",
		RawInputTokens:          10,
		RawOutputTokens:         12,
		RawCacheCreation:        30,
		RawCacheRead:            90,
		NormalizedInputTokens:   130,
		NormalizedOutputTokens:  12,
		NormalizedCacheCreation: 30,
		NormalizedCacheRead:     90,
		RawUsageJSON:            `{"input_tokens":10}`,
		Cost:                    1.25,
		ResponseTime:            20 * time.Millisecond,
		Status:                  "success",
	})
	if err != nil {
		t.Fatalf("Record error = %v", err)
	}
	records, err := store.Query(mbtrics.QueryOptions{Limit: 1})
	if err != nil {
		t.Fatalf("Query error = %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("records len = %d", len(records))
	}
	got := records[0]
	if got.RawInputTokens != 10 || got.NormalizedInputTokens != 130 || got.Protocol != "anthropic" || got.UsageSource != "anthropic_response" || got.ProviderKey != "deepseek" {
		t.Fatalf("record = %+v", got)
	}
	if got.RawUsageJSON != `{"input_tokens":10}` {
		t.Fatalf("RawUsageJSON = %q", got.RawUsageJSON)
	}
}

func TestStoreAggregateUsage(t *testing.T) {
	store := newMetricsStore(t)
	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	mustRecord := func(model string, ts time.Time, in, out, cw, cr int64, cost float64) {
		t.Helper()
		if err := store.Record(mbtrics.Record{
			Timestamp: ts, Model: model, ActualModel: model + "-up",
			InputTokens: in, OutputTokens: out, CacheCreation: cw, CacheRead: cr,
			Cost: cost, Status: "success",
		}); err != nil {
			t.Fatalf("Record error = %v", err)
		}
	}
	mustRecord("alpha", base, 100, 50, 10, 20, 0.5)
	mustRecord("alpha", base.Add(time.Hour), 200, 80, 0, 40, 0.7)
	mustRecord("beta", base.Add(2*time.Hour), 300, 90, 30, 60, 1.0)
	// Outside the queried window (excluded by the since bound).
	mustRecord("alpha", base.Add(-48*time.Hour), 999, 999, 999, 999, 9.0)

	agg, err := store.AggregateUsage(base.Add(-time.Hour), base.Add(3*time.Hour))
	if err != nil {
		t.Fatalf("AggregateUsage error = %v", err)
	}
	if agg.Requests != 3 {
		t.Fatalf("expected 3 requests in window, got %d", agg.Requests)
	}
	if agg.InputTokens != 600 || agg.OutputTokens != 220 {
		t.Fatalf("unexpected token totals: in=%d out=%d", agg.InputTokens, agg.OutputTokens)
	}
	if agg.CacheCreation != 40 || agg.CacheRead != 120 {
		t.Fatalf("unexpected cache totals: cw=%d cr=%d", agg.CacheCreation, agg.CacheRead)
	}
	if agg.Cost < 2.19 || agg.Cost > 2.21 {
		t.Fatalf("expected total cost ~2.2, got %v", agg.Cost)
	}
	if len(agg.ByModel) != 2 {
		t.Fatalf("expected 2 model rows, got %d", len(agg.ByModel))
	}
	byModel := map[string]mbtrics.UsageModelAggregate{}
	for _, m := range agg.ByModel {
		byModel[m.Model] = m
	}
	if alpha := byModel["alpha"]; alpha.Requests != 2 || alpha.InputTokens != 300 {
		t.Fatalf("unexpected alpha aggregate: %+v", alpha)
	}
	if beta := byModel["beta"]; beta.Requests != 1 || beta.Cost != 1.0 {
		t.Fatalf("unexpected beta aggregate: %+v", beta)
	}
	if !agg.Earliest.Equal(base) {
		t.Fatalf("expected earliest %v, got %v", base, agg.Earliest)
	}
	if !agg.Latest.Equal(base.Add(2 * time.Hour)) {
		t.Fatalf("expected latest %v, got %v", base.Add(2*time.Hour), agg.Latest)
	}
}

func renderMetricsDDL(schema string) string {
	out, _ := db.RenderDDL(schema, "metrics_request_metrics", "")
	return out
}
