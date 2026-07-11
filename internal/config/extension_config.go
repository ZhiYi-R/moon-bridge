package config

import (
	"encoding/json"
	"fmt"
	"log/slog"

	"gopkg.in/yaml.v3"
)

type ExtensionScope string

const (
	ExtensionScopeGlobal   ExtensionScope = "global"
	ExtensionScopeProvider ExtensionScope = "provider"
	ExtensionScopeModel    ExtensionScope = "model"
	ExtensionScopeRoute    ExtensionScope = "route"
)

// ExtensionConfigSpec describes config owned by an extension. The config
// package stores and resolves these specs without importing extension packages.
type ExtensionConfigSpec struct {
	Name           string
	Scopes         []ExtensionScope
	Factory        func() any
	DefaultEnabled bool
	Validate       func(Config) error
}

type LoadOptions struct {
	ExtensionSpecs []ExtensionConfigSpec
}

type ExtensionFileConfig struct {
	Enabled *bool          `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	Config  map[string]any `yaml:"config,omitempty" json:"config,omitempty"`
	Extra   map[string]any `yaml:",inline" json:"-"`
}

type ExtensionSettings struct {
	Enabled   *bool
	RawConfig map[string]any
	Extra     map[string]any
}

func (cfg *ExtensionFileConfig) UnmarshalYAML(value *yaml.Node) error {
	type plain ExtensionFileConfig
	var out plain
	if err := value.Decode(&out); err != nil {
		return err
	}
	cfg.Enabled = out.Enabled
	cfg.Config = out.Config
	cfg.Extra = removeKnownKeys(out.Extra, extensionKnownKeys)
	return nil
}

func (cfg ExtensionFileConfig) MarshalYAML() (any, error) {
	return mergeKnownFields(cfg.Extra, map[string]any{
		"enabled": cfg.Enabled,
		"config":  emptyMapAsNil(cfg.Config),
	}), nil
}

func (cfg *ExtensionFileConfig) UnmarshalJSON(data []byte) error {
	type plain ExtensionFileConfig
	var out plain
	if err := json.Unmarshal(data, &out); err != nil {
		return err
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	cfg.Enabled = out.Enabled
	cfg.Config = out.Config
	cfg.Extra = removeKnownKeys(raw, extensionKnownKeys)
	return nil
}

func (cfg ExtensionFileConfig) MarshalJSON() ([]byte, error) {
	return json.Marshal(mergeKnownFields(cfg.Extra, map[string]any{
		"enabled": cfg.Enabled,
		"config":  emptyMapAsNil(cfg.Config),
	}))
}

type extensionSpecIndex map[string]ExtensionConfigSpec

var (
	extensionKnownKeys = map[string]struct{}{
		"enabled": {},
		"config":  {},
	}
	webSearchKnownKeys = map[string]struct{}{
		"support":           {},
		"max_uses":          {},
		"tavily_api_key":    {},
		"firecrawl_api_key": {},
		"search_max_rounds": {},
	}
)

func newExtensionSpecIndex(specs []ExtensionConfigSpec) (extensionSpecIndex, error) {
	if len(specs) == 0 {
		return nil, nil
	}
	index := make(extensionSpecIndex, len(specs))
	for _, spec := range specs {
		if spec.Name == "" {
			return nil, fmt.Errorf("extension config spec name cannot be empty")
		}
		if _, ok := index[spec.Name]; ok {
			return nil, fmt.Errorf("duplicate extension config spec %q", spec.Name)
		}
		index[spec.Name] = spec
	}
	return index, nil
}

func (spec ExtensionConfigSpec) supports(scope ExtensionScope) bool {
	for _, s := range spec.Scopes {
		if s == scope {
			return true
		}
	}
	return false
}

func decodeExtensionSettings(path string, scope ExtensionScope, raw map[string]ExtensionFileConfig, specs extensionSpecIndex) (map[string]ExtensionSettings, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	result := make(map[string]ExtensionSettings, len(raw))
	for name, fileCfg := range raw {
		spec, ok := specs[name]
		if !ok {
			return nil, fmt.Errorf("%s.extensions.%s is not a registered extension", path, name)
		}
		if !spec.supports(scope) {
			return nil, fmt.Errorf("%s.extensions.%s does not support %s scope", path, name, scope)
		}
		result[name] = ExtensionSettings{
			Enabled:   fileCfg.Enabled,
			RawConfig: cloneAnyMap(fileCfg.Config),
			Extra:     cloneAnyMap(fileCfg.Extra),
		}
	}
	return result, nil
}

func cloneAnyMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func mergeAnyMaps(maps ...map[string]any) map[string]any {
	var out map[string]any
	for _, m := range maps {
		if len(m) == 0 {
			continue
		}
		if out == nil {
			out = make(map[string]any)
		}
		for k, v := range m {
			out[k] = v
		}
	}
	return out
}

func removeKnownKeys(in map[string]any, known map[string]struct{}) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		if _, ok := known[key]; ok {
			continue
		}
		out[key] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func mergeKnownFields(extra map[string]any, known map[string]any) map[string]any {
	out := cloneAnyMap(extra)
	if out == nil {
		out = make(map[string]any)
	}
	for key, value := range known {
		if value == nil {
			delete(out, key)
			continue
		}
		out[key] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func emptyStringAsNil(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func emptyIntAsNil(value int) any {
	if value == 0 {
		return nil
	}
	return value
}

func emptyMapAsNil(value map[string]any) any {
	if len(value) == 0 {
		return nil
	}
	return value
}

func decodeTypedExtensionConfig(spec ExtensionConfigSpec, raw map[string]any) any {
	if spec.Factory == nil {
		return cloneAnyMap(raw)
	}
	typed := spec.Factory()
	if typed == nil {
		return cloneAnyMap(raw)
	}
	data, err := json.Marshal(raw)
	if err != nil {
		slog.Warn("扩展配置序列化失败，回退到原始配置", "extension", spec.Name, "error", err)
		return cloneAnyMap(raw)
	}
	if err := json.Unmarshal(data, typed); err != nil {
		slog.Warn("扩展配置解码失败，回退到原始配置", "extension", spec.Name, "error", err)
		return cloneAnyMap(raw)
	}
	return typed
}
