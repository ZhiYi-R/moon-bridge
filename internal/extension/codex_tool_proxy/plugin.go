// Package codextoolproxy provides the codex_tool_proxy extension, which
// controls whether apply_patch custom tools are expanded into structured
// proxy tools for upstream models.
//
// When disabled, apply_patch is sent to upstream as a raw custom tool (ToolRaw),
// and the response-side reconstruction passes it through as-is.
package codextoolproxy

import (
	"moonbridge/internal/config"
	"moonbridge/internal/extension/plugin"
)

const PluginName = "codex_tool_proxy"

// ProxyPlugin implements the plugin.Plugin interface plus PatchProxyDecider.
type ProxyPlugin struct {
	plugin.BasePlugin
	appCfg config.Config
}

// NewPlugin creates a new codex_tool_proxy plugin.
func NewPlugin() *ProxyPlugin {
	return &ProxyPlugin{}
}

func (p *ProxyPlugin) Name() string { return PluginName }

func (p *ProxyPlugin) ConfigSpecs() []config.ExtensionConfigSpec {
	return ConfigSpecs()
}

func (p *ProxyPlugin) EnabledForModel(model string) bool {
	return p.appCfg.ExtensionEnabled(PluginName, model)
}

func (p *ProxyPlugin) Init(ctx plugin.PluginContext) error {
	p.appCfg = ctx.AppConfig
	return nil
}

// DisablePatchProxy implements plugin.PatchProxyDecider.
func (p *ProxyPlugin) DisablePatchProxy(model string) bool {
	return DisablePatchProxyForModel(p.appCfg, model)
}

// DisablePatchProxyForModel returns true when patch proxy expansion should be
// skipped for a model alias, based on the extension's enabled state.
func DisablePatchProxyForModel(cfg config.Config, model string) bool {
	return !cfg.ExtensionEnabled(PluginName, model)
}

// ConfigSpecs returns the extension config specs for codex_tool_proxy.
func ConfigSpecs() []config.ExtensionConfigSpec {
	return []config.ExtensionConfigSpec{{
		Name:           PluginName,
		DefaultEnabled: false,
		Scopes: []config.ExtensionScope{
			config.ExtensionScopeGlobal,
			config.ExtensionScopeProvider,
			config.ExtensionScopeModel,
			config.ExtensionScopeRoute,
		},
	}}
}
