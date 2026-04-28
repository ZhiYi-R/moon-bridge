package visual

import (
	"moonbridge/internal/extension/plugin"
	"moonbridge/internal/protocol/anthropic"
)

const PluginName = "visual"

type EnabledFunc func(modelAlias string) bool

// Plugin injects the Visual tools for models that opt in.
type Plugin struct {
	plugin.BasePlugin
	isEnabled EnabledFunc
}

func NewPlugin(isEnabled EnabledFunc) *Plugin {
	return &Plugin{isEnabled: isEnabled}
}

func (p *Plugin) Name() string { return PluginName }

func (p *Plugin) EnabledForModel(model string) bool {
	return p.isEnabled != nil && p.isEnabled(model)
}

func (p *Plugin) InjectTools(_ *plugin.RequestContext) []anthropic.Tool {
	return Tools()
}

var (
	_ plugin.Plugin       = (*Plugin)(nil)
	_ plugin.ToolInjector = (*Plugin)(nil)
)
