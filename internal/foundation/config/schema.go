package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/invopop/jsonschema"
)

const SchemaVersion = 3

const DefaultMainSchemaName = "config.schema.json"

// pluginConfigTypes is populated by Registry.Register when plugins implement
// plugin.ConfigTypeProvider. It is also populated by SetPluginConfigTypes for
// schema-dump scenarios where no registry is involved.
var pluginConfigTypes = map[string]func() any{}

// RegisterPluginConfigType registers a plugin's config type for schema
// generation and runtime decoding. Called automatically by Registry.Register
// for plugins that implement ConfigTypeProvider.
func RegisterPluginConfigType(name string, factory func() any) {
	pluginConfigTypes[name] = factory
}

type SchemaOptions struct {
	ExtensionSpecs []ExtensionConfigSpec
	ExtraPlugins   map[string]func() any
}

// DumpConfigSchema generates and writes JSON Schema files alongside the
// config file. extraPlugins provides per-plugin config types for typed
// schema generation; may be nil.
func DumpConfigSchema(configPath string, extraPlugins map[string]func() any) error {
	return DumpConfigSchemaWithOptions(configPath, SchemaOptions{ExtraPlugins: extraPlugins})
}

func DumpConfigSchemaWithOptions(configPath string, opts SchemaOptions) error {
	configDir := filepath.Dir(configPath)

	// Main config schema — describes the config format, not individual plugins.
	mainSchema := generateMainSchema()
	mainSchemaPath := filepath.Join(configDir, DefaultMainSchemaName)
	if err := writeSchemaIfStale(mainSchemaPath, mainSchema); err != nil {
		return fmt.Errorf("write schema %s: %w", mainSchemaPath, err)
	}
	ensureSchemaRef(configPath, DefaultMainSchemaName)

	// Merge built-in and externally-provided plugin types.
	allTypes := make(map[string]func() any, len(pluginConfigTypes)+len(opts.ExtraPlugins)+len(opts.ExtensionSpecs))
	for k, v := range pluginConfigTypes {
		allTypes[k] = v
	}
	for k, v := range opts.ExtraPlugins {
		allTypes[k] = v
	}
	for _, spec := range opts.ExtensionSpecs {
		if spec.Factory != nil {
			allTypes[spec.Name] = spec.Factory
		}
	}

	// Per-plugin schema files — each describes its own config structure.
	pluginDir := filepath.Join(configDir, DefaultPluginConfigDirName)
	if len(opts.ExtensionSpecs) > 0 {
		if err := os.MkdirAll(pluginDir, 0755); err != nil {
			return fmt.Errorf("create plugin schema dir: %w", err)
		}
		for _, spec := range opts.ExtensionSpecs {
			data, err := generatePluginSchema(spec.Name, allTypes)
			if err != nil {
				return fmt.Errorf("generate schema for plugin %s: %w", spec.Name, err)
			}
			if data == nil {
				continue
			}
			schemaPath := filepath.Join(pluginDir, spec.Name+".schema.json")
			if err := writeSchemaIfStale(schemaPath, data); err != nil {
				return fmt.Errorf("write plugin schema %s: %w", schemaPath, err)
			}
		}
	}
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
		// Only generate schemas for known plugins with a registered config type.
		data, err := generatePluginSchema(base, allTypes)
		if err != nil {
			return fmt.Errorf("generate schema for plugin %s: %w", base, err)
		}
		if data == nil {
			continue
		}
		schemaPath := filepath.Join(pluginDir, base+".schema.json")
		if err := writeSchemaIfStale(schemaPath, data); err != nil {
			return fmt.Errorf("write plugin schema %s: %w", schemaPath, err)
		}
		ensureSchemaRef(filepath.Join(pluginDir, entry.Name()), base+".schema.json")
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
// reflects its config struct. Returns nil for unknown plugins (caller skips them).
func generatePluginSchema(name string, allTypes map[string]func() any) ([]byte, error) {
	factory, ok := allTypes[name]
	if !ok {
		return nil, nil // unknown plugin, skip
	}
	r := &jsonschema.Reflector{}
	raw := schemaToMap(r.Reflect(factory()))
	raw["$metadata"] = map[string]any{
		"schemaVersion": SchemaVersion,
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

// ensureSchemaRef ensures the YAML config file at configPath has a
// # yaml-language-server: $schema= line pointing to the schema file
// in the same directory. If the line is missing or outdated it is
// added or updated; if already correct nothing is written.
func ensureSchemaRef(configPath, schemaName string) {
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return
	}
	lines := strings.SplitN(string(raw), "\n", 2)
	refLine := "# yaml-language-server: $schema=./" + schemaName
	if len(lines) > 0 && strings.Contains(lines[0], "yaml-language-server: $schema=") {
		if lines[0] == refLine {
			return
		}
		rest := ""
		if len(lines) > 1 {
			rest = lines[1]
		}
		os.WriteFile(configPath, []byte(refLine+"\n"+rest), 0644)
		return
	}
	os.WriteFile(configPath, []byte(refLine+"\n"+string(raw)), 0644)
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
