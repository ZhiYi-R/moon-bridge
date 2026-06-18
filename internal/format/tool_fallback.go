package format

import (
	"encoding/json"
	"strings"
)

// IsServerSideTool checks whether a CoreTool originated from a provider-specific
// server-side / builtin tool type that cannot be represented as a regular
// function-calling tool in downstream providers (e.g. Anthropic, Chat, Gemini).
//
// These tools have no Name in the OpenAI Tool DTO because they are identified
// solely by their "type" field (e.g. "image_generation"). When convertTool
// encounters an unrecognised type, it stores the original type in
// Extensions["source_type"] and leaves Name empty.
//
// ProviderAdapters should skip these tools and fall back tool_choice to "auto"
// to avoid creating broken function-tool definitions that would be rejected.
func IsServerSideTool(t CoreTool) bool {
	if t.Extensions == nil {
		return false
	}
	sourceType, _ := t.Extensions["source_type"].(string)
	if sourceType == "" || sourceType == "function" {
		return false
	}
	// Server-side builtin tools have no Name — they are identified by type only.
	// Custom tools (apply_patch, exec, read, etc.) have a meaningful Name.
	return t.Name == ""
}

// PrepareFunctionProviderTool returns a copy of t that is safe to forward as a
// regular function tool to providers that validate JSON Schema strictly.
//
// The second return value is false when the tool cannot be represented as a
// function tool at all, for example unnamed server-side tools such as
// image_generation. Repairable schema issues, such as duplicate entries in a
// required array, are normalized instead of dropping the tool.
func PrepareFunctionProviderTool(t CoreTool) (CoreTool, bool) {
	if IsServerSideTool(t) || strings.TrimSpace(t.Name) == "" {
		return CoreTool{}, false
	}
	prepared := t
	prepared.InputSchema = NormalizeFunctionToolSchema(t.InputSchema)
	if prepared.InputSchema == nil {
		prepared.InputSchema = map[string]any{"type": "object"}
	}
	return prepared, true
}

// NormalizeFunctionToolSchema deep-copies a JSON schema and deduplicates
// required arrays. It intentionally preserves other schema values, including
// nil/null and empty arrays, so normalization does not silently widen valid JSON
// Schema constraints such as const:null or enum:[null].
func NormalizeFunctionToolSchema(schema map[string]any) map[string]any {
	normalized, ok := normalizeSchemaValue(schema).(map[string]any)
	if !ok || len(normalized) == 0 {
		return nil
	}
	return normalized
}

func normalizeSchemaValue(value any) any {
	switch v := value.(type) {
	case nil:
		return nil
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			if key == "required" {
				required := normalizeRequired(item)
				if len(required) > 0 {
					out[key] = required
				} else {
					out[key] = item
				}
				continue
			}
			out[key] = normalizeSchemaValue(item)
		}
		return out
	case map[string]json.RawMessage:
		out := make(map[string]any, len(v))
		for key, item := range v {
			if key == "required" {
				required := normalizeRequired(item)
				if len(required) > 0 {
					out[key] = required
				} else {
					out[key] = item
				}
				continue
			}
			out[key] = normalizeSchemaValue(item)
		}
		return out
	case []any:
		out := make([]any, 0, len(v))
		for _, item := range v {
			out = append(out, normalizeSchemaValue(item))
		}
		return out
	case []map[string]any:
		out := make([]any, 0, len(v))
		for _, item := range v {
			out = append(out, normalizeSchemaValue(item))
		}
		return out
	case []string:
		out := make([]any, 0, len(v))
		for _, item := range v {
			out = append(out, item)
		}
		return normalizeSchemaValue(out)
	case []json.RawMessage:
		out := make([]any, 0, len(v))
		for _, item := range v {
			out = append(out, normalizeSchemaValue(item))
		}
		return out
	case json.RawMessage:
		var decoded any
		if err := json.Unmarshal(v, &decoded); err != nil {
			return v
		}
		return normalizeSchemaValue(decoded)
	default:
		return v
	}
}

func normalizeRequired(value any) []any {
	var items []any
	switch v := value.(type) {
	case []any:
		items = v
	case []string:
		items = make([]any, 0, len(v))
		for _, item := range v {
			items = append(items, item)
		}
	case []json.RawMessage:
		items = make([]any, 0, len(v))
		for _, item := range v {
			var decoded any
			if err := json.Unmarshal(item, &decoded); err != nil {
				return nil
			}
			items = append(items, decoded)
		}
	case json.RawMessage:
		var decoded any
		if err := json.Unmarshal(v, &decoded); err != nil {
			return nil
		}
		return normalizeRequired(decoded)
	default:
		return nil
	}

	seen := make(map[string]struct{}, len(items))
	required := make([]any, 0, len(items))
	for _, item := range items {
		name, ok := item.(string)
		if !ok || name == "" {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		required = append(required, name)
	}
	return required
}

// ShouldFallbackToolChoiceForSkippedTools reports whether a tool_choice should
// be relaxed to "auto" after one or more server-side tools were removed from a
// provider-bound request.
func ShouldFallbackToolChoiceForSkippedTools(tc *CoreToolChoice, skippedTools []CoreTool) bool {
	if tc == nil || len(skippedTools) == 0 {
		return false
	}
	switch tc.Mode {
	case "auto", "none":
		return false
	}

	skippedIDs := make(map[string]struct{}, len(skippedTools)*2)
	for _, tool := range skippedTools {
		if tool.Name != "" {
			skippedIDs[tool.Name] = struct{}{}
		}
		if tool.Extensions == nil {
			continue
		}
		sourceType, _ := tool.Extensions["source_type"].(string)
		if sourceType != "" {
			skippedIDs[sourceType] = struct{}{}
		}
	}

	if tc.Name != "" {
		_, skipped := skippedIDs[tc.Name]
		return skipped
	}
	if tc.Mode != "" {
		if _, skipped := skippedIDs[tc.Mode]; skipped {
			return true
		}
	}
	if len(tc.Raw) > 0 {
		rawMode, rawName := parseRawToolChoice(tc.Raw)
		if rawName != "" {
			_, skipped := skippedIDs[rawName]
			return skipped
		}
		if rawMode != "" {
			if rawMode == "auto" || rawMode == "none" {
				return false
			}
			if _, skipped := skippedIDs[rawMode]; skipped {
				return true
			}
		}
	}

	// Modes like "required" / "any" force tool use without naming a surviving
	// function. Once server-side tools were removed, that can force the model
	// into the wrong remaining tool, so relax it.
	return true
}

func parseRawToolChoice(raw json.RawMessage) (mode string, name string) {
	var scalar string
	if err := json.Unmarshal(raw, &scalar); err == nil {
		return scalar, ""
	}
	var obj struct {
		Type     string `json:"type"`
		Name     string `json:"name"`
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return "", ""
	}
	name = obj.Name
	if name == "" {
		name = obj.Function.Name
	}
	return obj.Type, name
}
