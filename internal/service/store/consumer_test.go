package store_test

import (
	"context"
	"database/sql"
	"log/slog"
	"os"
	"testing"

	"moonbridge/internal/db"
	"moonbridge/internal/service/store"

	_ "modernc.org/sqlite"
)

// testDBStore implements db.Store backed by an in-memory SQLite database.
type testDBStore struct {
	db       *sql.DB
	consumer string
	tables   map[string]string
}

func newTestStore(t *testing.T, consumer string, tables []db.TableSpec) *testDBStore {
	t.Helper()
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open error = %v", err)
	}
	t.Cleanup(func() { database.Close() })

	ts := &testDBStore{
		db:       database,
		consumer: consumer,
		tables:   make(map[string]string, len(tables)),
	}

	// Create all tables.
	for _, tbl := range tables {
		realName := consumer + "_" + tbl.Name
		ts.tables[tbl.Name] = realName

		ddl := tbl.Schema
		// Replace {{table}} placeholder.
		ddl = replacePlaceholder(ddl, "{{table}}", realName)

		if _, err := database.ExecContext(context.Background(), ddl); err != nil {
			t.Fatalf("create table %q: %v", realName, err)
		}
	}

	return ts
}

func (s *testDBStore) ConsumerName() string { return s.consumer }

func (s *testDBStore) Dialect() db.Dialect { return db.DialectSQLite }

func (s *testDBStore) Table(localName string) (string, error) {
	realName, ok := s.tables[localName]
	if !ok {
		return "", db.ErrTableNotRegistered
	}
	return realName, nil
}

func (s *testDBStore) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return s.db.ExecContext(ctx, query, args...)
}

func (s *testDBStore) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return s.db.QueryContext(ctx, query, args...)
}

func (s *testDBStore) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return s.db.QueryRowContext(ctx, query, args...)
}

func (s *testDBStore) WithTx(ctx context.Context, fn func(db.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	btx := &testTx{tx: tx, consumer: s.consumer, tables: s.tables}
	if err := fn(btx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

type testTx struct {
	tx       *sql.Tx
	consumer string
	tables   map[string]string
}

func (t *testTx) Table(localName string) (string, error) {
	realName, ok := t.tables[localName]
	if !ok {
		return "", db.ErrTableNotRegistered
	}
	return realName, nil
}

func (t *testTx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return t.tx.ExecContext(ctx, query, args...)
}

func (t *testTx) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return t.tx.QueryContext(ctx, query, args...)
}

func (t *testTx) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return t.tx.QueryRowContext(ctx, query, args...)
}

func replacePlaceholder(s, placeholder, value string) string {
	result := make([]byte, 0, len(s))
	i := 0
	for i < len(s) {
		if i+len(placeholder) <= len(s) && s[i:i+len(placeholder)] == placeholder {
			result = append(result, value...)
			i += len(placeholder)
		} else {
			result = append(result, s[i])
			i++
		}
	}
	return string(result)
}

func testLogger(t *testing.T) *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

func TestConsumerBasics(t *testing.T) {
	logger := testLogger(t)
	c := store.NewConfigStoreConsumer(logger)

	if c.Name() != "config_store" {
		t.Fatalf("Name() = %q, want %q", c.Name(), "config_store")
	}

	tables := c.Tables()
	expected := []string{"providers", "offers", "models", "routes", "settings", "websearch_probe", "changes", "schema_migrations"}
	if len(tables) != len(expected) {
		t.Fatalf("Tables() returned %d tables, want %d", len(tables), len(expected))
	}
	seen := make(map[string]bool)
	for _, tbl := range tables {
		seen[tbl.Name] = true
	}
	for _, name := range expected {
		if !seen[name] {
			t.Fatalf("missing table %q", name)
		}
	}

	// Validate.
	if err := db.ValidateConsumerTables(c.Name(), c.Tables()); err != nil {
		t.Fatalf("ValidateConsumerTables() error = %v", err)
	}
}

func TestConsumerBindStore(t *testing.T) {
	logger := testLogger(t)
	c := store.NewConfigStoreConsumer(logger)

	// BindStore with a real store.
	ts := newTestStore(t, "config_store", c.Tables())
	if err := c.BindStore(ts); err != nil {
		t.Fatalf("BindStore() error = %v", err)
	}

	if c.Store() == nil {
		t.Fatal("Store() should not be nil after BindStore")
	}

	// DisablePersistence.
	c.DisablePersistence(db.ErrNoProvider)
	if c.Store() != nil {
		t.Fatal("Store() should be nil after DisablePersistence")
	}
}
