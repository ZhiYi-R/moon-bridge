// Package dbsqlite implements a Moon Bridge extension that provides a SQLite
// database backend for persistence consumers.
//
// Configuration (in extensions.db_sqlite):
//
//	extensions:
//	  db_sqlite:
//	    enabled: true
//	    config:
//	      path: ./data/moonbridge.db
//	      wal: true
//	      busy_timeout_ms: 5000
//	      max_open_conns: 1
package dbsqlite

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"

	"moonbridge/internal/config"
	"moonbridge/internal/db"
	"moonbridge/internal/extension/plugin"
)

const PluginName = "db_sqlite"

// Config holds the SQLite provider configuration.
type Config struct {
	Path          string `json:"path,omitempty" yaml:"path"`
	WAL           *bool  `json:"wal,omitempty" yaml:"wal"`
	BusyTimeoutMS int    `json:"busy_timeout_ms,omitempty" yaml:"busy_timeout_ms"`
	MaxOpenConns  int    `json:"max_open_conns,omitempty" yaml:"max_open_conns"`
}

// sqliteProvider implements db.Provider for SQLite.
type sqliteProvider struct {
	cfg    Config
	db     *sql.DB
	closed bool
}

func (p *sqliteProvider) Name() string { return PluginName }

func (p *sqliteProvider) Dialect() db.Dialect { return db.DialectSQLite }

func (p *sqliteProvider) Open(ctx context.Context) error {
	absPath, err := filepath.Abs(p.cfg.Path)
	if err != nil {
		return fmt.Errorf("resolve path: %w", err)
	}
	d, err := sql.Open("sqlite", absPath)
	if err != nil {
		return fmt.Errorf("sql open: %w", err)
	}
	p.db = d

	maxOpen := p.cfg.MaxOpenConns
	if maxOpen <= 0 {
		maxOpen = 1
	}
	p.db.SetMaxOpenConns(maxOpen)

	bt := p.cfg.BusyTimeoutMS
	if bt <= 0 {
		bt = 5000
	}
	if _, err := p.db.ExecContext(ctx, fmt.Sprintf("PRAGMA busy_timeout=%d", bt)); err != nil {
		p.db.Close()
		return fmt.Errorf("set busy_timeout: %w", err)
	}

	if p.cfg.WAL == nil || *p.cfg.WAL {
		if _, err := p.db.ExecContext(ctx, "PRAGMA journal_mode=WAL"); err != nil {
			p.db.Close()
			return fmt.Errorf("set WAL mode: %w", err)
		}
	}
	return nil
}

func (p *sqliteProvider) DB() *sql.DB { return p.db }

func (p *sqliteProvider) Ping(ctx context.Context) error {
	if p.db == nil {
		return fmt.Errorf("database not open")
	}
	return p.db.PingContext(ctx)
}

func (p *sqliteProvider) Close() error {
	if p.db != nil && !p.closed {
		p.closed = true
		return p.db.Close()
	}
	return nil
}

func (p *sqliteProvider) Features() db.Features {
	return db.Features{
		SupportsPragma: true,
		SupportsWAL:    true,
		WorkerBound:    false,
	}
}

// Plugin is the SQLite provider plugin.
type Plugin struct {
	plugin.BasePlugin
	provider  *sqliteProvider
	pluginCfg config.PluginConfig
	addr      string
	enabled   bool
}

func NewPlugin() *Plugin {
	return &Plugin{}
}

func (p *Plugin) Name() string { return PluginName }

func (p *Plugin) ConfigSpecs() []config.ExtensionConfigSpec { return ConfigSpecs() }

func ConfigSpecs() []config.ExtensionConfigSpec {
	return []config.ExtensionConfigSpec{{
		Name:           PluginName,
		DefaultEnabled: true,
		Scopes: []config.ExtensionScope{
			config.ExtensionScopeGlobal,
		},
		Factory: func() any { return &Config{} },
	}}
}

func (p *Plugin) Init(ctx plugin.PluginContext) error {
	p.pluginCfg = config.PluginFromGlobalConfig(&ctx.AppConfig)
	p.addr = ctx.AppConfig.Addr
	cfg := plugin.Config[Config](ctx)
	if cfg == nil || cfg.Path == "" {
		return nil
	}
	p.provider = &sqliteProvider{cfg: *cfg}
	p.enabled = true
	return nil
}

func (p *Plugin) Shutdown() error {
	if p.provider != nil {
		return p.provider.Close()
	}
	return nil
}

func (p *Plugin) EnabledForModel(string) bool {
	return p.enabled && pluginExtensionEnabled(p.pluginCfg, PluginName)
}

func (p *Plugin) DBProvider() db.Provider {
	if !p.enabled || p.provider == nil {
		return nil
	}
	// Respect extensions.db_sqlite.enabled flag.
	if p.addr != "" && !pluginExtensionEnabled(p.pluginCfg, PluginName) {
		return nil
	}
	return p.provider
}

func pluginExtensionEnabled(pluginCfg config.PluginConfig, name string) bool {
	if setting, ok := pluginCfg.Extensions[name]; ok && setting.Enabled != nil {
		return *setting.Enabled
	}
	return false
}

var (
	_ plugin.Plugin             = (*Plugin)(nil)
	_ plugin.ConfigSpecProvider = (*Plugin)(nil)
	_ plugin.DBProvider         = (*Plugin)(nil)
)
