// Package dbd1 implements a Moon Bridge extension that provides a Cloudflare D1
// database backend for persistence consumers (Worker environment only).
//
// The D1 provider does not directly import the Cloudflare Workers SDK. Instead,
// the Worker entry point injects a *sql.DB via InjectDB() before InitAll.
//
// In non-Worker environments, the provider is automatically disabled.
//
// Configuration (in extensions.db_d1):
//
//	extensions:
//	  db_d1:
//	    enabled: true
//	    config:
//	      binding: MOONBRIDGE_DB
package dbd1

import (
	"context"
	"database/sql"
	"fmt"

	"moonbridge/internal/config"
	"moonbridge/internal/db"
	"moonbridge/internal/extension/plugin"
)

const PluginName = "db_d1"

// Config holds the D1 provider configuration.
type Config struct {
	Binding string `json:"binding,omitempty" yaml:"binding"`
}

// d1Provider implements db.Provider for Cloudflare D1.
type d1Provider struct {
	cfg    Config
	db     *sql.DB // injected by Worker entry point
	closed bool
}

func (p *d1Provider) Name() string { return PluginName }

func (p *d1Provider) Dialect() db.Dialect { return db.DialectD1 }

func (p *d1Provider) Open(ctx context.Context) error {
	if p.db == nil {
		return fmt.Errorf("D1 database not injected: %w", db.ErrNotSupported)
	}
	return nil
}

func (p *d1Provider) DB() *sql.DB { return p.db }

func (p *d1Provider) Ping(ctx context.Context) error {
	if p.db == nil {
		return fmt.Errorf("D1 database not injected: %w", db.ErrNotSupported)
	}
	// Some D1 runtimes may not support Ping; run a simple SELECT instead.
	if err := p.db.PingContext(ctx); err != nil {
		// Fallback: SELECT 1
		var n int
		if qErr := p.db.QueryRowContext(ctx, "SELECT 1").Scan(&n); qErr != nil {
			return qErr
		}
	}
	return nil
}

func (p *d1Provider) Close() error {
	if p.db != nil && !p.closed {
		p.closed = true
		// D1 connections managed by Worker runtime; Close is a no-op.
		return nil
	}
	return nil
}

func (p *d1Provider) Features() db.Features {
	return db.Features{
		SupportsPragma: false,
		SupportsWAL:    false,
		WorkerBound:    true,
	}
}

// Plugin is the D1 provider plugin.
type Plugin struct {
	plugin.BasePlugin
	provider *d1Provider
	enabled  bool
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

// InjectDB sets the *sql.DB for D1. Must be called before InitAll.
// Only the Worker entry point should call this.
func (p *Plugin) InjectDB(database *sql.DB) {
	p.provider = &d1Provider{db: database}
}

func (p *Plugin) Init(ctx plugin.PluginContext) error {
	// If already injected via InjectDB, mark as enabled.
	if p.provider != nil && p.provider.db != nil {
		p.provider.cfg = *plugin.Config[Config](ctx)
		p.enabled = true
		if ctx.Logger != nil {
			ctx.Logger.Info("D1 持久化已启用", "binding", p.provider.cfg.Binding)
		}
		return nil
	}

	// Not injected — check config.
	cfg := plugin.Config[Config](ctx)
	if cfg != nil && cfg.Binding != "" {
		// Configured but not injected: this is a non-Worker environment.
		if ctx.Logger != nil {
			ctx.Logger.Warn("D1 持久化已配置但当前环境不支持（仅在 Cloudflare Worker 中可用）")
		}
	}
	return nil
}

func (p *Plugin) Shutdown() error {
	if p.provider != nil {
		return p.provider.Close()
	}
	return nil
}

func (p *Plugin) EnabledForModel(string) bool { return p.enabled }

func (p *Plugin) DBProvider() db.Provider {
	if !p.enabled || p.provider == nil {
		return nil
	}
	return p.provider
}

var (
	_ plugin.Plugin             = (*Plugin)(nil)
	_ plugin.ConfigSpecProvider = (*Plugin)(nil)
	_ plugin.DBProvider         = (*Plugin)(nil)
)
