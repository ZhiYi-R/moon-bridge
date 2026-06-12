// Package codex provides Codex CLI model catalog and tool compatibility logic.
//
// This package centralizes Codex-specific knowledge:
// - catalog/config generation for Codex CLI
// - custom tool (namespace, custom, local_shell) conversions
// - apply_patch/exec proxy expansion for upstream models
// - response-side reconstruction of Codex output item types
package codextool

// ToolKind categorizes an expanded Codex tool for response-side reconstruction.
type ToolKind string

const (
	ToolApplyPatch  ToolKind = "apply_patch"
	ToolExec        ToolKind = "exec"
	ToolRaw         ToolKind = "raw"
	ToolFunction    ToolKind = "function"
	ToolLocalShell  ToolKind = "local_shell"
	ToolNestedOneOf ToolKind = "nested_oneof"
	ToolNestedAnyOf ToolKind = "nested_namespace"
	ToolUnknown     ToolKind = "unknown"
)

// ToolSpec describes an expanded tool entry for reverse mapping.
type ToolSpec struct {
	Kind       ToolKind `json:"kind"`
	OpenAIName string   `json:"openai_name"`
	Namespace  string   `json:"namespace,omitempty"`
}

// ToolMap maps from expanded (upstream-facing) tool names back to
// their original Codex metadata for response-side reconstruction.
type ToolMap map[string]ToolSpec

// DecodeToolMap decodes a ToolMap from a map[string]any (e.g. from Extensions).
func DecodeToolMap(raw map[string]any) ToolMap {
	if raw == nil {
		return nil
	}
	m := make(ToolMap, len(raw))
	for k, val := range raw {
		if specMap, ok := val.(map[string]any); ok {
			spec := ToolSpec{}
			if kind, ok := specMap["kind"].(string); ok {
				spec.Kind = ToolKind(kind)
			}
			if name, ok := specMap["openai_name"].(string); ok {
				spec.OpenAIName = name
			}
			if ns, ok := specMap["namespace"].(string); ok {
				spec.Namespace = ns
			}
			m[k] = spec
		}
	}
	return m
}

// Encode serialises a ToolMap to map[string]any for transport in Extensions.
func (m ToolMap) Encode() map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, spec := range m {
		out[k] = map[string]any{
			"kind":        string(spec.Kind),
			"openai_name": spec.OpenAIName,
			"namespace":   spec.Namespace,
		}
	}
	return out
}

// Lookup returns the ToolSpec for an expanded tool name.
func (m ToolMap) Lookup(name string) (ToolSpec, bool) {
	if m == nil {
		return ToolSpec{}, false
	}
	spec, ok := m[name]
	return spec, ok
}

// DecodeToolMapFromExtensions extracts the "codex_tool_map" entry from
// CoreRequest/CoreResponse Extensions and decodes it into a ToolMap.
// Returns nil when the entry is missing or malformed.
func DecodeToolMapFromExtensions(extensions map[string]any) ToolMap {
	if extensions == nil {
		return nil
	}
	raw, ok := extensions["codex_tool_map"]
	if !ok {
		return nil
	}
	tmMap, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	return DecodeToolMap(tmMap)
}
