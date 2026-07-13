package dbsqlite_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ZhiYi-R/moon-bridge/internal/config"
	dbsqlite "github.com/ZhiYi-R/moon-bridge/internal/extension/db/sqlite"
	"github.com/ZhiYi-R/moon-bridge/internal/extension/plugin"
)

func TestName(t *testing.T) {
	p := dbsqlite.NewPlugin()
	if p.Name() != "db_sqlite" {
		t.Fatalf("Name() = %q, want %q", p.Name(), "db_sqlite")
	}
}

func TestDBProviderNilWhenDisabled(t *testing.T) {
	p := dbsqlite.NewPlugin()
	if p.DBProvider() != nil {
		t.Fatal("DBProvider() should be nil before Init")
	}
}

func TestDBProviderNilWhenPathEmpty(t *testing.T) {
	p := dbsqlite.NewPlugin()
	cfg := &dbsqlite.Config{Path: ""}
	ctx := plugin.PluginContext{
		Config:    cfg,
		AppConfig: config.Config{},
	}
	if err := p.Init(ctx); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if p.DBProvider() != nil {
		t.Fatal("DBProvider() should be nil when path is empty")
	}
}

func TestOpenAndClose(t *testing.T) {
	p := dbsqlite.NewPlugin()
	wal := false
	cfg := &dbsqlite.Config{
		Path: t.TempDir() + "/test.db",
		WAL:  &wal,
	}
	ctx := plugin.PluginContext{Config: cfg, AppConfig: config.Config{}}
	if err := p.Init(ctx); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	prov := p.DBProvider()
	if prov == nil {
		t.Fatal("DBProvider() should not be nil")
	}

	if err := prov.Open(context.Background()); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer prov.Close()

	if err := prov.Ping(context.Background()); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}

	if prov.Dialect() != "sqlite" {
		t.Fatalf("Dialect() = %q, want %q", prov.Dialect(), "sqlite")
	}

	feat := prov.Features()
	if !feat.SupportsPragma {
		t.Fatal("Features().SupportsPragma should be true")
	}
	if feat.WorkerBound {
		t.Fatal("Features().WorkerBound should be false")
	}
}

func TestOpenCreatesParentDirectories(t *testing.T) {
	p := dbsqlite.NewPlugin()
	wal := false
	dir := filepath.Join(t.TempDir(), "nested", "data")
	dbPath := filepath.Join(dir, "moonbridge.db")
	cfg := &dbsqlite.Config{
		Path: dbPath,
		WAL:  &wal,
	}
	ctx := plugin.PluginContext{Config: cfg, AppConfig: config.Config{}}
	if err := p.Init(ctx); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	prov := p.DBProvider()
	if prov == nil {
		t.Fatal("DBProvider() should not be nil")
	}

	if err := prov.Open(context.Background()); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer prov.Close()

	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("stat db path after Open(): %v", err)
	}
}

func TestConfigSpecs(t *testing.T) {
	specs := dbsqlite.ConfigSpecs()
	if len(specs) != 1 {
		t.Fatalf("ConfigSpecs returned %d specs, want 1", len(specs))
	}
	if specs[0].Name != "db_sqlite" {
		t.Fatalf("spec.Name = %q, want %q", specs[0].Name, "db_sqlite")
	}
}

func TestInterfaceCompliance(t *testing.T) {
	p := dbsqlite.NewPlugin()
	var _ plugin.Plugin = p
	var _ plugin.ConfigSpecProvider = p
	var _ plugin.DBProvider = p
}
