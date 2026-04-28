// Package plugin defines the capability-based plugin system for Moon Bridge.
//
// Plugins implement the base Plugin interface plus zero or more capability
// interfaces. The Registry detects capabilities via type assertions at
// registration time and dispatches only to plugins that implement each
// capability.
package plugin

import (
	"log/slog"

	"moonbridge/internal/foundation/config"
)

// Plugin is the base interface all plugins must implement.
type Plugin interface {
	// Name returns a short unique identifier (e.g. "deepseek_v4").
	Name() string

	// Init is called once after registration. The plugin should validate
	// its configuration and acquire any resources it needs.
	Init(ctx PluginContext) error

	// Shutdown is called when the server is stopping. Release resources.
	Shutdown() error

	// EnabledForModel reports whether this plugin is active for the given
	// model alias. Called per-request.
	EnabledForModel(modelAlias string) bool
}

// PluginContext is passed to Plugin.Init.
type PluginContext struct {
	// Config is the decoded typed config struct for the plugin, or nil if
	// the plugin has not registered a config type. Populated by Registry.InitAll.
	// Use plugin.Config[T](ctx) to retrieve a typed struct.
	Config    any
	AppConfig config.Config // read-only global config
	Logger    *slog.Logger  // logger prefixed with the plugin name
}

// ConfigSpecProvider lets plugins declare extension-owned configuration across
// global/provider/model/route scopes.
type ConfigSpecProvider interface {
	ConfigSpecs() []config.ExtensionConfigSpec
}

// ConfigTypeProvider is an optional interface plugins may implement to
// declare their config structure for schema generation and runtime decoding.
// ConfigType returns a pointer to a zero-valued config struct (e.g. &Config{}).
type ConfigTypeProvider interface {
	ConfigType() any
}

// BasePlugin provides no-op defaults for the Plugin interface.
type BasePlugin struct{}

// Config returns the decoded typed config from PluginContext.
// Returns nil when the plugin has no registered config type or the type
// does not match.
func Config[T any](ctx PluginContext) *T {
	t, _ := ctx.Config.(*T)
	return t
}

func (BasePlugin) Init(PluginContext) error    { return nil }
func (BasePlugin) Shutdown() error             { return nil }
func (BasePlugin) EnabledForModel(string) bool { return false }
