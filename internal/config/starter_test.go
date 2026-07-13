package config_test

import (
	"path/filepath"
	"testing"

	"github.com/ZhiYi-R/moon-bridge/internal/config"
	dbsqlite "github.com/ZhiYi-R/moon-bridge/internal/extension/db/sqlite"
)

func TestStarterConfigYAMLBuildsLoadableTransformSQLiteConfig(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "moonbridge", "data", "moonbridge.db")

	data, err := config.StarterConfigYAML(dbPath, config.LoadOptions{
		ExtensionSpecs: dbsqlite.ConfigSpecs(),
	})
	if err != nil {
		t.Fatalf("StarterConfigYAML() error = %v", err)
	}

	cfg, err := config.LoadFromYAMLWithOptions(data, config.LoadOptions{
		ExtensionSpecs: dbsqlite.ConfigSpecs(),
	})
	if err != nil {
		t.Fatalf("LoadFromYAMLWithOptions(starter) error = %v", err)
	}
	if cfg.Mode != config.ModeTransform {
		t.Fatalf("Mode = %q, want Transform", cfg.Mode)
	}
	if len(cfg.ProviderDefs) == 0 {
		t.Fatal("ProviderDefs is empty, want starter provider")
	}
	if len(cfg.Routes) == 0 {
		t.Fatal("Routes is empty, want starter route")
	}
	if cfg.Persistence.ActiveProvider != "db_sqlite" {
		t.Fatalf("Persistence.ActiveProvider = %q, want db_sqlite", cfg.Persistence.ActiveProvider)
	}
	sqliteConfig, ok := cfg.Extensions["db_sqlite"]
	if !ok {
		t.Fatalf("Extensions missing db_sqlite: %+v", cfg.Extensions)
	}
	if sqliteConfig.Enabled == nil || !*sqliteConfig.Enabled {
		t.Fatalf("db_sqlite enabled = %v, want true", sqliteConfig.Enabled)
	}
	gotPath, ok := sqliteConfig.RawConfig["path"].(string)
	if !ok {
		t.Fatalf("db_sqlite path = %#v, want string", sqliteConfig.RawConfig["path"])
	}
	if gotPath != dbPath {
		t.Fatalf("db_sqlite path = %q, want %q", gotPath, dbPath)
	}
	if !filepath.IsAbs(gotPath) {
		t.Fatalf("db_sqlite path = %q, want absolute path", gotPath)
	}
}

func TestStarterConfigYAMLRejectsRelativeSQLitePath(t *testing.T) {
	_, err := config.StarterConfigYAML("./data/moonbridge.db", config.LoadOptions{
		ExtensionSpecs: dbsqlite.ConfigSpecs(),
	})
	if err == nil {
		t.Fatal("StarterConfigYAML() error = nil, want relative path rejection")
	}
}
