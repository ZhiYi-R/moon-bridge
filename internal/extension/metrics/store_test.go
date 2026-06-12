package metrics_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"moonbridge/internal/db"
	mbtrics "moonbridge/internal/extension/metrics"

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

func renderMetricsDDL(schema string) string {
	out, _ := db.RenderDDL(schema, "metrics_request_metrics", "")
	return out
}
