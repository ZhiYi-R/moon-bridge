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
// config file and its plugins directory. It skips writing if an existing
// schema file already has the current or newer version.
func DumpConfigSchema(configPath string) error {
	configDir := filepath.Dir(configPath)

	// Main config schema.
	mainSchema := generateMainSchema()
	mainSchemaPath := filepath.Join(configDir, DefaultMainSchemaName)
	if err := writeSchemaIfStale(mainSchemaPath, mainSchema); err != nil {
		return err
	}

	// Plugin config schemas.
	pluginDir := filepath.Join(configDir, DefaultPluginConfigDirName)
	entries, err := os.ReadDir(pluginDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read plugin dir for schema dump: %w", err)
	}
	pluginSchema := generatePluginSchema()
	for _, entry := range entries {
		if entry.IsDir() || !isYAMLFile(entry.Name()) {
			continue
		}
		base := strings.TrimSuffix(strings.TrimSuffix(entry.Name(), ".yaml"), ".yml")
		schemaPath := filepath.Join(pluginDir, base+".schema.json")
		if err := writeSchemaIfStale(schemaPath, pluginSchema); err != nil {
			return err
		}
	}
	return nil
}

func generateMainSchema() []byte {
	r := &jsonschema.Reflector{}
	// Prefer Go field names (no json tag dependency for schema output).
	s := r.Reflect(&FileConfig{})
	data, _ := json.MarshalIndent(s, "", "  ")

	// Inject metadata into the raw JSON.
	var raw map[string]any
	json.Unmarshal(data, &raw)
	raw["$metadata"] = map[string]any{
		"schemaVersion": SchemaVersion,
	}
	result, _ := json.MarshalIndent(raw, "", "  ")
	return result
}

func generatePluginSchema() []byte {
	s := map[string]any{
		"$schema":            "https://json-schema.org/draft/2020-12/schema",
		"type":               "object",
		"additionalProperties": map[string]any{"type": "object"},
		"$metadata": map[string]any{
			"schemaVersion": SchemaVersion,
		},
	}
	data, _ := json.MarshalIndent(s, "", "  ")
	return data
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
