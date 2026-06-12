package store

import (
	"log/slog"
	"moonbridge/internal/config"

	"moonbridge/internal/db"
)

// ConfigStoreConsumer implements db.Consumer for the config_store tables.
// It provides a ConfigStore interface for configuration persistence.
type ConfigStoreConsumer struct {
	store               db.Store
	persistenceDisabled bool
	extensionSpecs      []config.ExtensionConfigSpec
	logger              *slog.Logger
	configStore         ConfigStore
}

// NewConfigStoreConsumer creates a new ConfigStoreConsumer.
func NewConfigStoreConsumer(logger *slog.Logger) *ConfigStoreConsumer {
	return &ConfigStoreConsumer{logger: logger}
}

// SetExtensionSpecs provides extension config specs for config validation.
func (c *ConfigStoreConsumer) SetExtensionSpecs(specs []config.ExtensionConfigSpec) {
	c.extensionSpecs = specs
}

// Name returns the unique consumer identifier.
func (c *ConfigStoreConsumer) Name() string { return "config_store" }

// Tables returns the table schemas for config_store.
func (c *ConfigStoreConsumer) Tables() []db.TableSpec {
	return []db.TableSpec{
		{
			Name: "providers",
			Schema: `CREATE TABLE IF NOT EXISTS {{table}} (
    key          TEXT PRIMARY KEY,
    base_url     TEXT NOT NULL,
    api_key      TEXT NOT NULL,
    version      TEXT DEFAULT '',
    protocol     TEXT DEFAULT 'anthropic',
    enabled      INTEGER DEFAULT 1,
    user_agent   TEXT DEFAULT '',
    web_search   TEXT,
    extensions   TEXT,
    created_at   TEXT,
    updated_at   TEXT
)`,
		},
		{
			Name: "offers",
			Schema: `CREATE TABLE IF NOT EXISTS {{table}} (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    provider_key  TEXT NOT NULL,
    model_slug    TEXT NOT NULL,
    upstream_name TEXT DEFAULT '',
    priority      INTEGER DEFAULT 0,
    input_price   REAL DEFAULT 0,
    output_price  REAL DEFAULT 0,
    cache_write   REAL DEFAULT 0,
    cache_read    REAL DEFAULT 0,
    overrides     TEXT,
    UNIQUE(provider_key, model_slug),
    FOREIGN KEY (provider_key) REFERENCES config_store_providers(key) ON DELETE CASCADE
)`,
		},
		{
			Name: "models",
			Schema: `CREATE TABLE IF NOT EXISTS {{table}} (
    slug         TEXT PRIMARY KEY,
    metadata     TEXT NOT NULL,
    created_at   TEXT,
    updated_at   TEXT
)`,
		},
		{
			Name: "routes",
			Schema: `CREATE TABLE IF NOT EXISTS {{table}} (
    alias             TEXT PRIMARY KEY,
    model_slug        TEXT NOT NULL,
    provider_key      TEXT DEFAULT '',
    display_name      TEXT DEFAULT '',
    context_window    INTEGER DEFAULT 0,
    max_output_tokens INTEGER DEFAULT 0,
    extensions        TEXT,
    web_search        TEXT,
    created_at        TEXT,
    updated_at        TEXT
)`,
		},
		{
			Name: "settings",
			Schema: `CREATE TABLE IF NOT EXISTS {{table}} (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
)`,
		},
		{
			Name: "changes",
			Schema: `CREATE TABLE IF NOT EXISTS {{table}} (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    batch_id    TEXT,
    action      TEXT NOT NULL,
    resource    TEXT NOT NULL,
    target_key  TEXT NOT NULL,
    before      TEXT,
    after       TEXT,
    applied     INTEGER DEFAULT 0,
    error       TEXT,
    revision    INTEGER DEFAULT 0,
    created_at  TEXT,
    applied_at  TEXT
)`,
		},
		{
			Name: "schema_migrations",
			Schema: `CREATE TABLE IF NOT EXISTS {{table}} (
    version     INTEGER PRIMARY KEY,
    applied_at  TEXT
)`,
		},
	}
}

// BindStore is called by the db.Registry after tables are created.
func (c *ConfigStoreConsumer) BindStore(s db.Store) error {
	c.store = s
	c.configStore = NewSQLiteStore(s, c.logger, c.extensionSpecs...)
	if c.logger != nil {
		c.logger.Info("config_store 持久化已启用")
	}
	return nil
}

// DisablePersistence is called when persistence is unavailable.
func (c *ConfigStoreConsumer) DisablePersistence(reason error) {
	c.persistenceDisabled = true
	c.store = nil
	c.configStore = nil
	if c.logger != nil {
		c.logger.Error("config_store 持久化已禁用", "error", reason)
	}
}

// Store returns the ConfigStore for this consumer.
// Returns nil if persistence is disabled.
func (c *ConfigStoreConsumer) Store() ConfigStore {
	return c.configStore
}

// compile-time interface checks.
var (
	_ db.Consumer = (*ConfigStoreConsumer)(nil)
)
