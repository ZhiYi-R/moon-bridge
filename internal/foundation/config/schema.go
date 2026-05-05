package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/invopop/jsonschema"
)

const SchemaVersion = 5

const DefaultMainSchemaName = "config.schema.json"

// pluginConfigTypes is populated by Registry.Register from ExtensionConfigSpec.Factory. It is also populated by SetPluginConfigTypes for
// schema-dump scenarios where no registry is involved.
var pluginConfigTypes = map[string]func() any{}

// RegisterPluginConfigType registers a plugin's config type for schema
// generation and runtime decoding. Called automatically by Registry.Register
// for extensions that define a config Factory in their ExtensionConfigSpec.
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

	// Main config schema — describes the config format, not individual plugins.
	mainSchema := generateMainSchema(allTypes)
	mainSchemaPath := filepath.Join(configDir, DefaultMainSchemaName)
	if err := writeSchemaIfStale(mainSchemaPath, mainSchema); err != nil {
		return fmt.Errorf("write schema %s: %w", mainSchemaPath, err)
	}
	ensureSchemaRef(configPath, DefaultMainSchemaName)

	return nil
}

func generateMainSchema(knownTypes map[string]func() any) []byte {
	r := &jsonschema.Reflector{}
	s := r.Reflect(&FileConfig{})
	raw := schemaToMap(s)

	// Inject known extension config types into the 4 extension mount points.
	injectExtensionSchemas(raw, knownTypes)

	raw["$metadata"] = map[string]any{
		"schemaVersion": SchemaVersion,
	}
	result, _ := json.MarshalIndent(raw, "", "  ")
	return result
}

// injectExtensionSchemas replaces the bare ExtensionFileConfig additionalProperties
// in all 4 extension mount points (global, provider, model, route) with a definition
// that lists known extensions as named properties with typed config refs.
func injectExtensionSchemas(raw map[string]any, knownTypes map[string]func() any) {
	if len(knownTypes) == 0 {
		return
	}
	defs, _ := raw["$defs"].(map[string]any)
	if defs == nil {
		return
	}

	for _, structName := range []string{"FileConfig", "ProviderDefFileConfig", "ProviderModelFileConfig", "RouteFileConfig"} {
		sc, ok := defs[structName].(map[string]any)
		if !ok {
			continue
		}
		props, _ := sc["properties"].(map[string]any)
		extBlock, ok := props["extensions"].(map[string]any)
		if !ok {
			continue
		}

		// Build extension config type defs first, then only reference those
		// that successfully produced a schema def — avoids dangling $ref.
		validExtensions := make(map[string]extensionEntry)
		for name, factory := range knownTypes {
			r := &jsonschema.Reflector{}
			s := schemaToMap(r.Reflect(factory()))
			ref, _ := s["$ref"].(string)
			tdef, _ := extractTypeDef(s, ref)
			if tdef == nil {
				continue
			}
			defName := extensionDefName(name)
			validExtensions[name] = extensionEntry{defName: defName}
			defs[defName] = map[string]any{
				"type": "object",
				"properties": map[string]any{
					"enabled": map[string]any{"type": "boolean"},
					"config":  tdef,
				},
				"additionalProperties": false,
			}
		}
		extBlock["properties"] = buildExtensionProperties(validExtensions)
	}
}

type extensionEntry struct {
	defName string
}

func buildExtensionProperties(validExtensions map[string]extensionEntry) map[string]any {
	props := make(map[string]any, len(validExtensions))
	for name := range validExtensions {
		entry := validExtensions[name]
		props[name] = map[string]any{
			"$ref": "#/$defs/" + entry.defName,
		}
	}
	return props
}

func extensionDefName(name string) string {
	// deepseek_v4 -> Deepseek_v4Extension
	if len(name) == 0 {
		return name
	}
	return strings.ToUpper(name[:1]) + name[1:] + "Extension"
}

// extractTypeDef extracts a type definition from a reflected schema by expected def name.
// A reflected struct produces {"$ref":"#/$defs/X","$defs":{"X":{...}}}.
// refPath is the $ref value (e.g. "#/$defs/X") — the def name is the last segment.
func extractTypeDef(raw map[string]any, refPath string) (map[string]any, bool) {
	defs, ok := raw["$defs"].(map[string]any)
	if !ok || len(defs) == 0 {
		return nil, false
	}
	// Extract def name from "#/$defs/X".
	name := refPath
	if idx := strings.LastIndex(refPath, "/"); idx >= 0 {
		name = refPath[idx+1:]
	}
	if name == "" {
		return nil, false
	}
	def, ok := defs[name].(map[string]any)
	return def, ok
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
