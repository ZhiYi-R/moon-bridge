package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/invopop/jsonschema"
)

const SchemaVersion = 1

const DefaultMainSchemaName = "config.schema.json"

// pluginConfigTypes is a registry of known plugin config types, keyed by
// plugin name. Plugins register their config structs via init() to enable
// field-level JSON Schema generation.
var pluginConfigTypes = map[string]func() any{}

// RegisterPluginConfigType registers a factory that returns a pointer to a
// zero-valued plugin config struct (e.g. &DeepSeekV4Config{}). The registered
// type is reflected at schema-dump time to produce a typed JSON Schema for
// that plugin.
//
// Called from plugin package init() functions. Not safe for concurrent use
// after startup.
func RegisterPluginConfigType(name string, factory func() any) {
	pluginConfigTypes[name] = factory
}

// DumpConfigSchema generates and writes JSON Schema files alongside the
// config file. It skips writing if an existing schema file already has the
// current or newer version.
func DumpConfigSchema(configPath string) error {
	configDir := filepath.Dir(configPath)

	// Main config schema — describes the config format, not individual plugins.
	mainSchema := generateMainSchema()
	mainSchemaPath := filepath.Join(configDir, DefaultMainSchemaName)
	if err := writeSchemaIfStale(mainSchemaPath, mainSchema); err != nil {
		return fmt.Errorf("write schema %s: %w", mainSchemaPath, err)
	}

	// Per-plugin schema files — each describes its own config structure.
	pluginDir := filepath.Join(configDir, DefaultPluginConfigDirName)
	entries, err := os.ReadDir(pluginDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read plugin dir: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !isYAMLFile(entry.Name()) {
			continue
		}
		base := strings.TrimSuffix(strings.TrimSuffix(entry.Name(), ".yaml"), ".yml")
		data, err := generatePluginSchema(base)
		if err != nil {
			return fmt.Errorf("generate schema for plugin %s: %w", base, err)
		}
		schemaPath := filepath.Join(pluginDir, base+".schema.json")
		if err := writeSchemaIfStale(schemaPath, data); err != nil {
			return fmt.Errorf("write plugin schema %s: %w", schemaPath, err)
		}
	}
	return nil
}

func generateMainSchema() []byte {
	r := &jsonschema.Reflector{}
	s := r.Reflect(&FileConfig{})
	data, _ := json.MarshalIndent(s, "", "  ")

	var raw map[string]any
	json.Unmarshal(data, &raw)
	raw["$metadata"] = map[string]any{
		"schemaVersion": SchemaVersion,
	}
	result, _ := json.MarshalIndent(raw, "", "  ")
	return result
}

// generatePluginSchema returns a JSON Schema for a named plugin config file.
// If the plugin has been registered via RegisterPluginConfigType, the schema
// reflects its config struct. Otherwise a generic open-object schema is used.
func generatePluginSchema(name string) ([]byte, error) {
	factory, ok := pluginConfigTypes[name]
	if ok {
		r := &jsonschema.Reflector{}
		raw := schemaToMap(r.Reflect(factory()))
		raw["$metadata"] = map[string]any{
			"schemaVersion": SchemaVersion,
		}
		return json.MarshalIndent(raw, "", "  ")
	}

	// Unknown plugin — generic open-object schema.
	raw := map[string]any{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"type":                 "object",
		"additionalProperties": map[string]any{"type": "object"},
		"$metadata": map[string]any{
			"schemaVersion": SchemaVersion,
		},
	}
	return json.MarshalIndent(raw, "", "  ")
}

func schemaToMap(s *jsonschema.Schema) map[string]any {
	data, _ := json.Marshal(s)
	var raw map[string]any
	json.Unmarshal(data, &raw)
	return raw
}

// DecodePluginConfig decodes a raw plugin config map into the registered typed
// config struct for the named plugin. Returns nil if the plugin name is unknown.
// writeSchemaIfStale writes data to path only if the existing file has a
// different or missing schema version.
func DecodePluginConfig(name string, raw map[string]any) any {
	factory, ok := pluginConfigTypes[name]
	if !ok || raw == nil {
		return nil
	}
	typed := factory()
	data, _ := json.Marshal(raw)
	json.Unmarshal(data, typed)
	return typed
}

func writeSchemaIfStale(path string, data []byte) error {
	existing, err := os.ReadFile(path)
	if err == nil {
		var meta struct {
			M struct {
				V int `json:"schemaVersion"`
			} `json:"$metadata"`
		}
		if err := json.Unmarshal(existing, &meta); err == nil && meta.M.V >= SchemaVersion {
			return nil
		}
	}
	return os.WriteFile(path, data, 0644)
}
