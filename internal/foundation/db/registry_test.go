package db_test

import (
	"context"
	"database/sql"
	"log/slog"

	"errors"
	"strings"
	"testing"

	"moonbridge/internal/foundation/db"

	_ "modernc.org/sqlite"
)

// --- Helpers ---

func memoryDB(t *testing.T) *sql.DB {
	t.Helper()
	d, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open(:memory:) error = %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

type mockProvider struct {
	name     string
	dialect  db.Dialect
	database *sql.DB
	openErr  error
	pingErr  error
	closeErr error
}

func (p *mockProvider) Name() string                   { return p.name }
func (p *mockProvider) Dialect() db.Dialect            { return p.dialect }
func (p *mockProvider) Open(ctx context.Context) error { return p.openErr }
func (p *mockProvider) DB() *sql.DB                    { return p.database }
func (p *mockProvider) Ping(ctx context.Context) error { return p.pingErr }
func (p *mockProvider) Close() error                   { return p.closeErr }
func (p *mockProvider) Features() db.Features          { return db.Features{} }

type mockConsumer struct {
	name      string
	tables    []db.TableSpec
	bindErr   error
	disabled  bool
	disableCh chan error
}

func (c *mockConsumer) Name() string               { return c.name }
func (c *mockConsumer) Tables() []db.TableSpec     { return c.tables }
func (c *mockConsumer) BindStore(s db.Store) error { return c.bindErr }
func (c *mockConsumer) DisablePersistence(reason error) {
	c.disabled = true
	if c.disableCh != nil {
		c.disableCh <- reason
	}
}

func newTable(name string) db.TableSpec {
	return db.TableSpec{
		Name: name,
		Schema: `CREATE TABLE IF NOT EXISTS {{table}} (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			value TEXT NOT NULL
		)`,
	}
}

// ======================
// Store / table isolation
// ======================

func TestBoundStoreTable(t *testing.T) {
	d := memoryDB(t)
	r := db.NewRegistry(slog.Default())
	r.RegisterProvider(&mockProvider{name: "p", dialect: db.DialectSQLite, database: d})
	c := &mockConsumer{name: "metrics", tables: []db.TableSpec{newTable("req")}}
	r.RegisterConsumer(c)
	if err := r.Init(context.Background(), ""); err != nil {
		t.Fatalf("Init error = %v", err)
	}
	s, ok := r.StoreForConsumer("metrics")
	if !ok {
		t.Fatal("StoreForConsumer not found")
	}

	realName, err := s.Table("req")
	if err != nil {
		t.Fatalf("Table(req) error = %v", err)
	}
	if realName != "metrics_req" {
		t.Fatalf("Table(req) = %q, want %q", realName, "metrics_req")
	}

	_, err = s.Table("unknown")
	if !errors.Is(err, db.ErrTableNotRegistered) {
		t.Fatalf("Table(unknown) error = %v, want ErrTableNotRegistered", err)
	}
}

func TestBoundStoreExecAndQuery(t *testing.T) {
	d := memoryDB(t)
	r := db.NewRegistry(slog.Default())
	r.RegisterProvider(&mockProvider{name: "p", dialect: db.DialectSQLite, database: d})
	c := &mockConsumer{name: "test", tables: []db.TableSpec{
		{Name: "items", Schema: `CREATE TABLE IF NOT EXISTS {{table}} (id INTEGER PRIMARY KEY, name TEXT)`},
	}}
	r.RegisterConsumer(c)
	if err := r.Init(context.Background(), ""); err != nil {
		t.Fatalf("Init error = %v", err)
	}
	s, _ := r.StoreForConsumer("test")

	tbl, _ := s.Table("items")
	if _, err := s.ExecContext(context.Background(), "INSERT INTO "+tbl+" (name) VALUES (?)", "hello"); err != nil {
		t.Fatalf("ExecContext error = %v", err)
	}

	rows, err := s.QueryContext(context.Background(), "SELECT name FROM "+tbl)
	if err != nil {
		t.Fatalf("QueryContext error = %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("expected a row")
	}
	var name string
	if err := rows.Scan(&name); err != nil {
		t.Fatalf("Scan error = %v", err)
	}
	if name != "hello" {
		t.Fatalf("name = %q, want %q", name, "hello")
	}
}

func TestBoundStoreWithTx(t *testing.T) {
	d := memoryDB(t)
	r := db.NewRegistry(slog.Default())
	r.RegisterProvider(&mockProvider{name: "p", dialect: db.DialectSQLite, database: d})
	c := &mockConsumer{name: "test", tables: []db.TableSpec{
		{Name: "items", Schema: `CREATE TABLE IF NOT EXISTS {{table}} (id INTEGER PRIMARY KEY, val TEXT)`},
	}}
	r.RegisterConsumer(c)
	if err := r.Init(context.Background(), ""); err != nil {
		t.Fatalf("Init error = %v", err)
	}
	s, _ := r.StoreForConsumer("test")
	tbl, _ := s.Table("items")

	// Commit.
	if err := s.WithTx(context.Background(), func(tx db.Tx) error {
		t, _ := tx.Table("items")
		_, err := tx.ExecContext(context.Background(), "INSERT INTO "+t+" (val) VALUES (?)", "tx1")
		return err
	}); err != nil {
		t.Fatalf("WithTx commit error = %v", err)
	}

	// Rollback.
	if err := s.WithTx(context.Background(), func(tx db.Tx) error {
		t, _ := tx.Table("items")
		_, _ = tx.ExecContext(context.Background(), "INSERT INTO "+t+" (val) VALUES (?)", "tx2")
		return errors.New("rollback")
	}); err == nil || err.Error() != "rollback" {
		t.Fatalf("expected rollback error, got %v", err)
	}

	var count int
	if err := s.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM "+tbl).Scan(&count); err != nil {
		t.Fatalf("count error = %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 row (tx1 committed, tx2 rolled back), got %d", count)
	}
}

// ======================
// RenderDDL
// ======================

func TestRenderDDL(t *testing.T) {
	r, err := db.RenderDDL(`CREATE TABLE {{table}} (id INTEGER)`, "metrics_t", "")
	if err != nil {
		t.Fatalf("RenderDDL error = %v", err)
	}
	if !strings.Contains(r, "metrics_t") {
		t.Fatalf("result missing table name: %s", r)
	}
	if strings.Contains(r, "{{") {
		t.Fatalf("result has unresolved placeholder: %s", r)
	}
}

func TestRenderDDLWithIndex(t *testing.T) {
	r, err := db.RenderDDL(`CREATE INDEX {{index}} ON {{table}}(v)`, "t", "idx_t_v")
	if err != nil {
		t.Fatalf("RenderDDL error = %v", err)
	}
	if !strings.Contains(r, "idx_t_v") {
		t.Fatalf("result missing index name: %s", r)
	}
}

func TestRenderDDLUnresolved(t *testing.T) {
	_, err := db.RenderDDL(`CREATE TABLE {{table}} (v TEXT DEFAULT '{{x}}')`, "t", "")
	if err == nil {
		t.Fatal("expected error for unresolved placeholder")
	}
}

// ======================
// ValidateConsumerTables
// ======================

func TestValidateTablesValid(t *testing.T) {
	err := db.ValidateConsumerTables("m", []db.TableSpec{
		{Name: "t", Schema: `CREATE TABLE {{table}} (id INTEGER)`},
	})
	if err != nil {
		t.Fatalf("error = %v", err)
	}
}

func TestValidateTablesEmpty(t *testing.T) {
	err := db.ValidateConsumerTables("m", nil)
	if !errors.Is(err, db.ErrNoTables) {
		t.Fatalf("expected ErrNoTables, got %v", err)
	}
}

func TestValidateTablesBadName(t *testing.T) {
	err := db.ValidateConsumerTables("Bad", []db.TableSpec{
		{Name: "t", Schema: `CREATE TABLE {{table}} (id INTEGER)`},
	})
	if !errors.Is(err, db.ErrTableNameInvalid) {
		t.Fatalf("expected ErrTableNameInvalid, got %v", err)
	}
}

func TestValidateTablesMissingPlaceholder(t *testing.T) {
	err := db.ValidateConsumerTables("m", []db.TableSpec{
		{Name: "t", Schema: `CREATE TABLE t (id INTEGER)`},
	})
	if err == nil || !strings.Contains(err.Error(), "{{table}}") {
		t.Fatalf("expected {{table}} error, got %v", err)
	}
}

func TestValidateTablesDuplicate(t *testing.T) {
	err := db.ValidateConsumerTables("m", []db.TableSpec{
		{Name: "x", Schema: `CREATE TABLE {{table}} (id INTEGER)`},
		{Name: "x", Schema: `CREATE TABLE {{table}} (v TEXT)`},
	})
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("expected duplicate error, got %v", err)
	}
}

func TestValidateTablesIndexPlaceholders(t *testing.T) {
	err := db.ValidateConsumerTables("m", []db.TableSpec{
		{
			Name:   "t",
			Schema: `CREATE TABLE {{table}} (id INTEGER, v TEXT)`,
			Indexes: []db.IndexSpec{
				{Name: "v", SQL: `CREATE INDEX {{index}} ON {{table}}(v)`},
			},
		},
	})
	if err != nil {
		t.Fatalf("error = %v", err)
	}
}

func TestValidateTablesIndexMissingPlaceholder(t *testing.T) {
	err := db.ValidateConsumerTables("m", []db.TableSpec{
		{
			Name:   "t",
			Schema: `CREATE TABLE {{table}} (id INTEGER)`,
			Indexes: []db.IndexSpec{
				{Name: "v", SQL: `CREATE INDEX idx ON t(v)`},
			},
		},
	})
	if err == nil {
		t.Fatal("expected error for missing {{table}} in index SQL")
	}
}

// ======================
// Registry
// ======================

func TestRegistryNoConsumersIsNoop(t *testing.T) {
	r := db.NewRegistry(slog.Default())
	if err := r.Init(context.Background(), ""); err != nil {
		t.Fatalf("Init error = %v", err)
	}
}

func TestRegistryNoProviderDisablesConsumers(t *testing.T) {
	r := db.NewRegistry(slog.Default())
	c := &mockConsumer{name: "test", tables: []db.TableSpec{newTable("t")}}
	r.RegisterConsumer(c)
	if err := r.Init(context.Background(), ""); err != nil {
		t.Fatalf("Init should not block, got %v", err)
	}
	if !c.disabled {
		t.Fatal("consumer should be disabled")
	}
}

func TestRegistrySingleProviderAutoSelect(t *testing.T) {
	d := memoryDB(t)
	r := db.NewRegistry(slog.Default())
	r.RegisterProvider(&mockProvider{name: "p", dialect: db.DialectSQLite, database: d})
	c := &mockConsumer{name: "test", tables: []db.TableSpec{newTable("t")}}
	r.RegisterConsumer(c)
	if err := r.Init(context.Background(), ""); err != nil {
		t.Fatalf("Init error = %v", err)
	}
	if r.ActiveProvider().Name() != "p" {
		t.Fatalf("ActiveProvider = %q, want p", r.ActiveProvider().Name())
	}
	_, ok := r.StoreForConsumer("test")
	if !ok {
		t.Fatal("StoreForConsumer should exist")
	}
}

func TestRegistrySingleProviderWrongActiveName(t *testing.T) {
	d := memoryDB(t)
	r := db.NewRegistry(slog.Default())
	r.RegisterProvider(&mockProvider{name: "p", dialect: db.DialectSQLite, database: d})
	c := &mockConsumer{name: "test", tables: []db.TableSpec{newTable("t")}}
	r.RegisterConsumer(c)
	err := r.Init(context.Background(), "wrong")
	if !errors.Is(err, db.ErrProviderNotFound) {
		t.Fatalf("expected ErrProviderNotFound, got %v", err)
	}
}

func TestRegistryMultipleProvidersNoActiveName(t *testing.T) {
	r := db.NewRegistry(slog.Default())
	r.RegisterProvider(&mockProvider{name: "a"})
	r.RegisterProvider(&mockProvider{name: "b"})
	r.RegisterConsumer(&mockConsumer{name: "t", tables: []db.TableSpec{newTable("x")}})
	err := r.Init(context.Background(), "")
	if !errors.Is(err, db.ErrMultipleProviders) {
		t.Fatalf("expected ErrMultipleProviders, got %v", err)
	}

}
func TestRegistryMultipleProvidersWithActiveName(t *testing.T) {
	d := memoryDB(t)
	r := db.NewRegistry(slog.Default())
	r.RegisterProvider(&mockProvider{name: "a"})
	r.RegisterProvider(&mockProvider{name: "b", dialect: db.DialectSQLite, database: d})
	c := &mockConsumer{name: "test", tables: []db.TableSpec{newTable("t")}}
	r.RegisterConsumer(c)
	if err := r.Init(context.Background(), "b"); err != nil {
		t.Fatalf("Init error = %v", err)
	}
	if r.ActiveProvider().Name() != "b" {
		t.Fatalf("ActiveProvider = %q, want b", r.ActiveProvider().Name())
	}
}

func TestRegistryMultipleProvidersWrongActiveName(t *testing.T) {
	r := db.NewRegistry(slog.Default())
	r.RegisterProvider(&mockProvider{name: "a"})
	r.RegisterProvider(&mockProvider{name: "b"})
	r.RegisterConsumer(&mockConsumer{name: "t", tables: []db.TableSpec{newTable("x")}})
	err := r.Init(context.Background(), "c")
	if !errors.Is(err, db.ErrProviderNotFound) {
		t.Fatalf("expected ErrProviderNotFound, got %v", err)
	}
}

func TestRegistryProviderOpenFails(t *testing.T) {
	r := db.NewRegistry(slog.Default())
	r.RegisterProvider(&mockProvider{name: "p", openErr: errors.New("boom")})
	c := &mockConsumer{name: "test", tables: []db.TableSpec{newTable("t")}}
	r.RegisterConsumer(c)
	err := r.Init(context.Background(), "")
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected open error, got %v", err)
	}
}

func TestRegistryProviderPingFails(t *testing.T) {
	r := db.NewRegistry(slog.Default())
	r.RegisterProvider(&mockProvider{name: "p", pingErr: errors.New("nope")})
	c := &mockConsumer{name: "test", tables: []db.TableSpec{newTable("t")}}
	r.RegisterConsumer(c)
	err := r.Init(context.Background(), "")
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("expected ping error, got %v", err)
	}
}

func TestRegistryBadTableDisablesOnlyThatConsumer(t *testing.T) {
	d := memoryDB(t)
	r := db.NewRegistry(slog.Default())
	r.RegisterProvider(&mockProvider{name: "p", dialect: db.DialectSQLite, database: d})
	cBad := &mockConsumer{name: "bad", tables: []db.TableSpec{
		{Name: "t", Schema: `INVALID SQL {{table}}`},
	}}
	cGood := &mockConsumer{name: "good", tables: []db.TableSpec{newTable("t")}}
	r.RegisterConsumer(cBad)
	r.RegisterConsumer(cGood)
	if err := r.Init(context.Background(), ""); err != nil {
		t.Fatalf("Init should not block, got %v", err)
	}
	if !cBad.disabled {
		t.Fatal("bad consumer should be disabled")
	}
	_, ok := r.StoreForConsumer("good")
	if !ok {
		t.Fatal("good consumer should have store")
	}
}

func TestRegistryBindStoreFailsDisablesOnlyThatConsumer(t *testing.T) {
	d := memoryDB(t)
	r := db.NewRegistry(slog.Default())
	r.RegisterProvider(&mockProvider{name: "p", dialect: db.DialectSQLite, database: d})
	cBad := &mockConsumer{name: "bad", tables: []db.TableSpec{newTable("t")}, bindErr: errors.New("nope")}
	cGood := &mockConsumer{name: "good", tables: []db.TableSpec{newTable("t")}}
	r.RegisterConsumer(cBad)
	r.RegisterConsumer(cGood)
	if err := r.Init(context.Background(), ""); err != nil {
		t.Fatalf("Init should not block, got %v", err)
	}
	if !cBad.disabled {
		t.Fatal("bad consumer should be disabled")
	}
	_, ok := r.StoreForConsumer("good")
	if !ok {
		t.Fatal("good consumer should have store")
	}
}

func TestRegistryDisableAllCallsEveryConsumer(t *testing.T) {
	r := db.NewRegistry(slog.Default())
	ch1 := make(chan error, 1)
	ch2 := make(chan error, 1)
	c1 := &mockConsumer{name: "a", tables: []db.TableSpec{newTable("t")}, disableCh: ch1}
	c2 := &mockConsumer{name: "b", tables: []db.TableSpec{newTable("t")}, disableCh: ch2}
	r.RegisterConsumer(c1)
	r.RegisterConsumer(c2)
	if err := r.Init(context.Background(), ""); err != nil {
		t.Fatalf("Init error = %v", err)
	}
	for _, ch := range []chan error{ch1, ch2} {
		select {
		case reason := <-ch:
			if !errors.Is(reason, db.ErrNoProvider) {
				t.Fatalf("disable reason = %v, want ErrNoProvider", reason)
			}
		default:
			t.Fatal("DisablePersistence not called")
		}
	}
}

func TestRegistryNilProviderSkipped(t *testing.T) {
	r := db.NewRegistry(slog.Default())
	r.RegisterProvider(nil)
	r.RegisterProvider(nil)
	c := &mockConsumer{name: "test", tables: []db.TableSpec{newTable("t")}}
	r.RegisterConsumer(c)
	if err := r.Init(context.Background(), ""); err != nil {
		t.Fatalf("Init error = %v", err)
	}
	if !c.disabled {
		t.Fatal("consumer should be disabled when only nil providers")
	}
}

func TestRegistryShutdown(t *testing.T) {
	d := memoryDB(t)
	r := db.NewRegistry(slog.Default())
	r.RegisterProvider(&mockProvider{name: "p", dialect: db.DialectSQLite, database: d})
	r.RegisterConsumer(&mockConsumer{name: "test", tables: []db.TableSpec{newTable("t")}})
	if err := r.Init(context.Background(), ""); err != nil {
		t.Fatalf("Init error = %v", err)
	}
	if err := r.Shutdown(); err != nil {
		t.Fatalf("Shutdown error = %v", err)
	}
}

func TestRegistryShutdownBeforeInit(t *testing.T) {
	r := db.NewRegistry(slog.Default())
	if err := r.Shutdown(); err != nil {
		t.Fatalf("Shutdown error = %v", err)
	}
}

func TestActiveProviderNilBeforeInit(t *testing.T) {
	r := db.NewRegistry(slog.Default())
	if r.ActiveProvider() != nil {
		t.Fatal("expected nil")
	}
}

func TestStoreForConsumerFalseBeforeInit(t *testing.T) {
	r := db.NewRegistry(slog.Default())
	if _, ok := r.StoreForConsumer("x"); ok {
		t.Fatal("expected false")
	}
}
