package config

import (
	"encoding/json"
	"fmt"
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
}

type ExtensionSettings struct {
	Enabled   *bool
	RawConfig map[string]any
}

type extensionSpecIndex map[string]ExtensionConfigSpec

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

func decodeTypedExtensionConfig(spec ExtensionConfigSpec, raw map[string]any) any {
	if spec.Factory == nil {
		return cloneAnyMap(raw)
	}
	typed := spec.Factory()
	if typed == nil {
		return cloneAnyMap(raw)
	}
	data, _ := json.Marshal(raw)
	_ = json.Unmarshal(data, typed)
	return typed
}
