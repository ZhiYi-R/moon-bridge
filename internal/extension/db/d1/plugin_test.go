package dbd1_test

import (
	"context"
	"database/sql"
	"testing"

	"moonbridge/internal/config"
	"moonbridge/internal/db"
	dbd1 "moonbridge/internal/extension/db/d1"
	"moonbridge/internal/extension/plugin"

	_ "modernc.org/sqlite"
)

func TestName(t *testing.T) {
	p := dbd1.NewPlugin()
	if p.Name() != "db_d1" {
		t.Fatalf("Name() = %q, want %q", p.Name(), "db_d1")
	}
}

func TestDBProviderNilWithoutInject(t *testing.T) {
	p := dbd1.NewPlugin()
	cfg := &dbd1.Config{Binding: "MY_DB"}
	ctx := plugin.PluginContext{Config: cfg, AppConfig: config.Config{}}
	if err := p.Init(ctx); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if p.DBProvider() != nil {
		t.Fatal("DBProvider() should be nil when not injected in non-Worker env")
	}
}

func TestInjectAndOpen(t *testing.T) {
	memDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open error = %v", err)
	}
	defer memDB.Close()

	p := dbd1.NewPlugin()
	p.InjectDB(memDB)

	cfg := &dbd1.Config{Binding: "MY_DB"}
	ctx := plugin.PluginContext{Config: cfg, AppConfig: config.Config{}}
	if err := p.Init(ctx); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	prov := p.DBProvider()
	if prov == nil {
		t.Fatal("DBProvider() should not be nil after InjectDB")
	}

	if prov.Dialect() != db.DialectD1 {
		t.Fatalf("Dialect() = %q, want %q", prov.Dialect(), "d1")
	}

	if err := prov.Open(context.Background()); err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	if err := prov.Ping(context.Background()); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}

	feat := prov.Features()
	if feat.SupportsPragma {
		t.Fatal("Features().SupportsPragma should be false for D1")
	}
	if !feat.WorkerBound {
		t.Fatal("Features().WorkerBound should be true for D1")
	}
}

func TestConfigSpecs(t *testing.T) {
	specs := dbd1.ConfigSpecs()
	if len(specs) != 1 {
		t.Fatalf("ConfigSpecs returned %d specs, want 1", len(specs))
	}
	if specs[0].Name != "db_d1" {
		t.Fatalf("spec.Name = %q, want %q", specs[0].Name, "db_d1")
	}
}

func TestInterfaceCompliance(t *testing.T) {
	p := dbd1.NewPlugin()
	var _ plugin.Plugin = p
	var _ plugin.ConfigSpecProvider = p
	var _ plugin.DBProvider = p
}
