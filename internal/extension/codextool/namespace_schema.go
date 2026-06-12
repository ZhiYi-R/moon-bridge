// Package codextool provides namespace tool flattening and nested schema building.
package codextool

import (
	"encoding/json"
	"fmt"
	"strings"

	"moonbridge/internal/format"
)

// NamespaceStrategy controls how namespace tools are converted for upstream providers.
type NamespaceStrategy string

const (
	NestedOneOf NamespaceStrategy = "nested_oneof"
	NestedAnyOf NamespaceStrategy = "nested_anyof"
	Flat        NamespaceStrategy = "flat"
)

// BuildNamespaceTools converts a namespace tool to one or more CoreTools
// according to the given strategy.
func BuildNamespaceTools(
	toolNames []string,
	toolMap map[string]format.CoreTool,
	parentNamespace string,
	strategy NamespaceStrategy,
) ([]format.CoreTool, error) {
	switch strategy {
	case NestedOneOf:
		return buildNestedOneOf(toolNames, toolMap, parentNamespace), nil
	case NestedAnyOf:
		return buildNestedAnyOf(toolNames, toolMap, parentNamespace), nil
	case Flat:
		return buildFlat(toolNames, toolMap, parentNamespace), nil
	default:
		return buildFlat(toolNames, toolMap, parentNamespace), nil
	}
}

// buildNestedOneOf generates a single tool with a oneOf Schema.
// Each sub-tool becomes a oneOf branch keyed by a single-value "action" enum.
func buildNestedOneOf(toolNames []string, toolMap map[string]format.CoreTool, namespace string) []format.CoreTool {
	if len(toolNames) == 0 {
		return nil
	}

	mergedName := namespace

	oneOf := make([]map[string]any, 0, len(toolNames))
	for _, name := range toolNames {
		sub, ok := toolMap[name]
		if !ok {
			continue
		}
		props := make(map[string]any)
		required := []string{"action"}
		if sub.InputSchema != nil {
			if p, ok := sub.InputSchema["properties"].(map[string]any); ok {
				for k, v := range p {
					props[k] = v
				}
			}
			if r, ok := sub.InputSchema["required"].([]any); ok {
				for _, rv := range r {
					if rs, ok := rv.(string); ok {
						required = append(required, rs)
					}
				}
			}
		}
		// action field with single-value enum
		props["action"] = map[string]any{
			"type": "string",
			"enum": []string{name},
		}
		branch := map[string]any{
			"type":                 "object",
			"title":                name,
			"properties":           props,
			"required":             required,
			"additionalProperties": false,
		}
		oneOf = append(oneOf, branch)
	}

	if len(oneOf) == 0 {
		return nil
	}

	mergedSchema := map[string]any{
		"type":  "object",
		"oneOf": oneOf,
	}

	ct := format.CoreTool{
		Name:        mergedName,
		Description: fmt.Sprintf("Namespace tool with %d sub-tools. Pick the matching action.", len(oneOf)),
		InputSchema: mergedSchema,
	}
	AnnotateCoreTool(&ct, ToolNestedOneOf, mergedName, namespace)
	return []format.CoreTool{ct}
}

// buildNestedAnyOf generates a single tool with an action enum + params anyOf
// (the PR #75 compatible format).
func buildNestedAnyOf(toolNames []string, toolMap map[string]format.CoreTool, namespace string) []format.CoreTool {
	if len(toolNames) == 0 {
		return nil
	}

	mergedName := namespace

	actions := make([]string, 0, len(toolNames))
	anyOf := make([]map[string]any, 0, len(toolNames))
	for _, name := range toolNames {
		sub, ok := toolMap[name]
		if !ok {
			continue
		}
		actions = append(actions, name)
		branch := sub.InputSchema
		if branch == nil {
			branch = map[string]any{"type": "object"}
		}
		anyOf = append(anyOf, branch)
	}

	mergedSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type": "string",
				"enum": actions,
			},
			"params": map[string]any{
				"oneOf": anyOf,
			},
		},
		"required":             []string{"action", "params"},
		"additionalProperties": false,
	}

	ct := format.CoreTool{
		Name:        mergedName,
		Description: fmt.Sprintf("Namespace tool with %d sub-tools. Use action to select, params for arguments.", len(anyOf)),
		InputSchema: mergedSchema,
	}
	AnnotateCoreTool(&ct, ToolNestedAnyOf, mergedName, namespace)
	return []format.CoreTool{ct}
}

// buildFlat flattens namespace tools into individual CoreTools.
func buildFlat(toolNames []string, toolMap map[string]format.CoreTool, namespace string) []format.CoreTool {
	result := make([]format.CoreTool, 0, len(toolNames))
	for _, name := range toolNames {
		sub, ok := toolMap[name]
		if !ok {
			continue
		}
		fullName := NamespacedToolName(namespace, name)
		ct := format.CoreTool{
			Name:        fullName,
			Description: sub.Description,
			InputSchema: sub.InputSchema,
		}
		AnnotateCoreTool(&ct, ToolFunction, name, namespace)
		result = append(result, ct)
	}
	return result
}

// DecodeNestedCall extracts the action name and parameters from a nested
// namespace tool call, regardless of whether the model used the oneOf
// or anyOf schema format.
//
// For oneOf format: {"action": "read_file", "path": "/foo", ...}
//
//	→ action = "read_file", params = {"path": "/foo", ...}
//
// For anyOf format: {"action": "read_file", "params": {"path": "/foo"}}
//
//	→ action = "read_file", params = {"path": "/foo"}
func DecodeNestedCall(input json.RawMessage, schemaKind ToolKind) (action string, params json.RawMessage, err error) {
	if len(input) == 0 || string(input) == "null" {
		return "", nil, fmt.Errorf("empty input")
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(input, &raw); err != nil {
		return "", nil, fmt.Errorf("unmarshal: %w", err)
	}

	// Extract action
	actionBytes, hasAction := raw["action"]
	if !hasAction {
		return "", input, fmt.Errorf("no action field")
	}
	if err := json.Unmarshal(actionBytes, &action); err != nil {
		return "", input, fmt.Errorf("action parse: %w", err)
	}

	// Extract params based on schema format
	switch schemaKind {
	case ToolNestedOneOf:
		// oneOf format: action is inline with other params
		delete(raw, "action")
		if len(raw) == 0 {
			return action, json.RawMessage(`{}`), nil
		}
		params, err = json.Marshal(raw)
		return action, params, err

	case ToolNestedAnyOf:
		// anyOf format: params are in a separate "params" field
		if paramsRaw, ok := raw["params"]; ok {
			return action, paramsRaw, nil
		}
		return action, json.RawMessage(`{}`), nil

	default:
		return action, input, nil
	}
}

// tryExtractAction scans partial JSON for "action": "value" and returns the value.
// Returns ("", false) if the action field is not yet complete.
func TryExtractAction(raw string) (string, bool) {
	idx := strings.Index(raw, `"action"`)
	if idx < 0 {
		return "", false
	}
	rest := raw[idx+8:] // skip past "action"
	// Skip whitespace and colon
	rest = strings.TrimSpace(strings.TrimPrefix(rest, ":"))
	rest = strings.TrimSpace(rest)
	// Find opening quote
	if len(rest) == 0 || rest[0] != '"' {
		return "", false
	}
	rest = rest[1:] // skip opening quote
	// Find closing quote
	q2 := strings.IndexByte(rest, '"')
	if q2 < 0 {
		return "", false
	}
	return rest[:q2], true
}
