package visual

import (
	"fmt"
	"strings"

	"moonbridge/internal/extension/plugin"
	"moonbridge/internal/foundation/config"
	"moonbridge/internal/protocol/anthropic"
)

const PluginName = "visual"

type EnabledFunc func(modelAlias string) bool

type Config struct {
	Provider  string `json:"provider,omitempty" yaml:"provider"`
	Model     string `json:"model,omitempty" yaml:"model"`
	MaxRounds int    `json:"max_rounds,omitempty" yaml:"max_rounds"`
	MaxTokens int    `json:"max_tokens,omitempty" yaml:"max_tokens"`
}

// Plugin injects the Visual tools for models that opt in.
type Plugin struct {
	plugin.BasePlugin
	isEnabled EnabledFunc
	appCfg    config.Config
}

func NewPlugin(isEnabled ...EnabledFunc) *Plugin {
	var enabled EnabledFunc
	if len(isEnabled) > 0 {
		enabled = isEnabled[0]
	}
	return &Plugin{isEnabled: enabled}
}

func (p *Plugin) Name() string { return PluginName }

func (p *Plugin) ConfigSpecs() []config.ExtensionConfigSpec { return ConfigSpecs() }

func (p *Plugin) Init(ctx plugin.PluginContext) error {
	p.appCfg = ctx.AppConfig
	return nil
}

func (p *Plugin) EnabledForModel(model string) bool {
	if p.isEnabled != nil {
		return p.isEnabled(model)
	}
	return p.appCfg.ExtensionEnabled(PluginName, model)
}

func (p *Plugin) InjectTools(_ *plugin.RequestContext) []anthropic.Tool {
	return Tools()
}

func ConfigSpecs() []config.ExtensionConfigSpec {
	return []config.ExtensionConfigSpec{{
		Name: PluginName,
		Scopes: []config.ExtensionScope{
			config.ExtensionScopeGlobal,
			config.ExtensionScopeProvider,
			config.ExtensionScopeModel,
			config.ExtensionScopeRoute,
		},
		Factory:  func() any { return &Config{} },
		Validate: ValidateConfig,
	}}
}

func ConfigForModel(appCfg config.Config, modelAlias string) (Config, bool) {
	if !appCfg.ExtensionEnabled(PluginName, modelAlias) {
		return Config{}, false
	}
	cfg, _ := appCfg.ExtensionConfig(PluginName, modelAlias).(*Config)
	if cfg == nil {
		return Config{}, true
	}
	return cfg.Normalized(), true
}

func (cfg Config) Normalized() Config {
	cfg.Provider = strings.TrimSpace(cfg.Provider)
	cfg.Model = strings.TrimSpace(cfg.Model)
	if cfg.MaxRounds <= 0 {
		cfg.MaxRounds = 4
	}
	if cfg.MaxTokens <= 0 {
		cfg.MaxTokens = 2048
	}
	return cfg
}

func ValidateConfig(appCfg config.Config) error {
	for alias := range appCfg.Routes {
		if err := validateModelConfig(appCfg, alias); err != nil {
			return err
		}
	}
	for providerKey, def := range appCfg.ProviderDefs {
		for modelName := range def.Models {
			if err := validateModelConfig(appCfg, providerKey+"/"+modelName); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateModelConfig(appCfg config.Config, modelAlias string) error {
	cfg, ok := ConfigForModel(appCfg, modelAlias)
	if !ok {
		return nil
	}
	if cfg.Provider == "" {
		return fmt.Errorf("extensions.%s.config.provider is required when visual is enabled for %s", PluginName, modelAlias)
	}
	if cfg.Model == "" {
		return fmt.Errorf("extensions.%s.config.model is required when visual is enabled for %s", PluginName, modelAlias)
	}
	def, ok := appCfg.ProviderDefs[cfg.Provider]
	if !ok {
		return fmt.Errorf("extensions.%s.config.provider references unknown provider %q", PluginName, cfg.Provider)
	}
	if def.Protocol != "" && def.Protocol != config.ProtocolAnthropic {
		return fmt.Errorf("extensions.%s.config.provider %q requires anthropic protocol (uses %s)", PluginName, cfg.Provider, def.Protocol)
	}
	return nil
}

var (
	_ plugin.Plugin             = (*Plugin)(nil)
	_ plugin.ConfigSpecProvider = (*Plugin)(nil)
	_ plugin.ToolInjector       = (*Plugin)(nil)
)
