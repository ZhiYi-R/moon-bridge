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
// Known plugins get a typed schema; unknown plugins get a generic open-object schema.
func generatePluginSchema(name string) ([]byte, error) {
	r := &jsonschema.Reflector{}

	var schema *jsonschema.Schema
	switch name {
	case "deepseek_v4":
		schema = r.Reflect(&deepSeekV4PluginSchema{})
	default:
		schema = r.Reflect(&genericPluginSchema{})
	}

	raw := schemaToMap(schema)
	raw["$metadata"] = map[string]any{
		"schemaVersion": SchemaVersion,
	}
	return json.MarshalIndent(raw, "", "  ")
}

// Known plugin config types — one struct per plugin, all fields optional.

type deepSeekV4PluginSchema struct {
	ReinforceInstructions *bool   `json:"reinforce_instructions,omitempty"`
	ReinforcePrompt       *string `json:"reinforce_prompt,omitempty"`
}

// genericPluginSchema is used for any plugin whose config shape is unknown.
type genericPluginSchema struct {
	// AdditionalProperties captures arbitrary key-value pairs.
	AdditionalProperties struct{} `json:"additionalProperties,omitempty"`
}

func schemaToMap(s *jsonschema.Schema) map[string]any {
	data, _ := json.Marshal(s)
	var raw map[string]any
	json.Unmarshal(data, &raw)
	return raw
}

// writeSchemaIfStale writes data to path only if the existing file has a
// different or missing schema version.
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
