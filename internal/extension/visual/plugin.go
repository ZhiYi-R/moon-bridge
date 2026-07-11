package visual

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"moonbridge/internal/config"
	"moonbridge/internal/extension/plugin"
	"moonbridge/internal/format"
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
	isEnabled     EnabledFunc
	pluginCfg     config.PluginConfig
	currentConfig func() config.Config
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
	p.pluginCfg = config.PluginFromGlobalConfig(&ctx.AppConfig)
	p.currentConfig = ctx.CurrentConfig
	return nil
}

func (p *Plugin) EnabledForModel(model string) bool {
	if p.currentConfig != nil {
		return p.currentConfig().ExtensionEnabled(PluginName, model)
	}
	if p.isEnabled != nil {
		return p.isEnabled(model)
	}
	if setting, ok := p.pluginCfg.Extensions[PluginName]; ok && setting.Enabled != nil {
		return *setting.Enabled
	}
	return false
}

func (p *Plugin) InjectTools(_ *plugin.RequestContext) []format.CoreTool {
	return CoreTools()
}

// CoreInjectTools returns Core-format tool definitions for visual analysis.
func (p *Plugin) CoreInjectTools(_ *plugin.RequestContext) []format.CoreTool {
	return CoreTools()
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
		Factory: func() any { return &Config{} },
		Validate: func(cfg config.Config) error {
			return ValidateConfig(
				config.PluginFromGlobalConfig(&cfg),
				config.ProviderFromGlobalConfig(&cfg),
				cfg,
			)
		},
	}}
}

func ConfigForModel(pluginCfg config.PluginConfig, modelAlias string) (Config, bool) {
	// Check if visual extension is enabled globally.
	if !pluginExtensionEnabled(pluginCfg, PluginName) {
		return Config{}, false
	}
	// Decode typed config from global RawConfig.
	var cfg *Config
	if setting, ok := pluginCfg.Extensions[PluginName]; ok && len(setting.RawConfig) > 0 {
		data, err := json.Marshal(setting.RawConfig)
		if err != nil {
			slog.Warn("visual 配置序列化失败，使用默认值", "error", err)
		} else if err := json.Unmarshal(data, &cfg); err != nil {
			slog.Warn("visual 配置解码失败，使用默认值", "error", err)
			cfg = nil
		}
	}
	if cfg == nil {
		return Config{}, true
	}
	return cfg.Normalized(), true
}

// ConfigForModelFromResolvedConfig resolves visual config using the full
// extension scope precedence from config.Config.
func ConfigForModelFromResolvedConfig(fullCfg config.Config, modelAlias string) (Config, bool) {
	if !fullCfg.ExtensionEnabled(PluginName, modelAlias) {
		return Config{}, false
	}
	cfg, err := decodeVisualConfig(fullCfg, modelAlias)
	if err != nil {
		return Config{}, false
	}
	return cfg, true
}

func pluginExtensionEnabled(pluginCfg config.PluginConfig, name string) bool {
	if setting, ok := pluginCfg.Extensions[name]; ok && setting.Enabled != nil {
		return *setting.Enabled
	}
	return false
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

// decodeVisualConfig decodes the visual extension config for a model alias.
func decodeVisualConfig(fullCfg config.Config, modelAlias string) (Config, error) {
	raw := fullCfg.ExtensionRawConfig("visual", modelAlias)
	if len(raw) == 0 {
		return Config{}, fmt.Errorf("visual config not found for %s", modelAlias)
	}
	var cfg Config
	data, err := json.Marshal(raw)
	if err != nil {
		return Config{}, fmt.Errorf("marshal visual config: %w", err)
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("unmarshal visual config: %w", err)
	}
	return cfg.Normalized(), nil
}

func ValidateConfig(pluginCfg config.PluginConfig, providerCfg config.ProviderConfig, fullCfg config.Config) error {
	for alias := range providerCfg.Routes {
		if err := validateModelConfig(pluginCfg, providerCfg, alias, fullCfg); err != nil {
			return err
		}
	}
	for providerKey, def := range providerCfg.Providers {
		for modelName := range def.Models {
			if err := validateModelConfig(pluginCfg, providerCfg, providerKey+"/"+modelName, fullCfg); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateModelConfig(pluginCfg config.PluginConfig, providerCfg config.ProviderConfig, modelAlias string, fullCfg config.Config) error {
	if !fullCfg.ExtensionEnabled("visual", modelAlias) {
		return nil
	}
	// Decode visual config using the full config (model-level + global merge).
	visCfg, err := decodeVisualConfig(fullCfg, modelAlias)
	if err != nil {
		return err
	}
	if visCfg.Provider == "" {
		return fmt.Errorf("extensions.%s.config.provider is required when visual is enabled for %s", PluginName, modelAlias)
	}
	if visCfg.Model == "" {
		return fmt.Errorf("extensions.%s.config.model is required when visual is enabled for %s", PluginName, modelAlias)
	}
	def, ok := providerCfg.Providers[visCfg.Provider]
	if !ok {
		return fmt.Errorf("extensions.%s.config.provider references unknown provider %q", PluginName, visCfg.Provider)
	}
	_ = def
	return nil
}

var (
	_ plugin.Plugin             = (*Plugin)(nil)
	_ plugin.ConfigSpecProvider = (*Plugin)(nil)
)

var (
	_ plugin.Plugin             = (*Plugin)(nil)
	_ plugin.ConfigSpecProvider = (*Plugin)(nil)
	_ plugin.ToolInjector       = (*Plugin)(nil)
)
