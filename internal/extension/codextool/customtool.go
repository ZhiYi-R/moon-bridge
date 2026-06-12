package codextool

import (
	"encoding/json"
	"fmt"
	"strings"

	"moonbridge/internal/format"
)

// Grammar helpers

// CustomToolGrammar extracts the grammar definition from a custom tool's Format field.
func CustomToolGrammar(formatMap map[string]any) string {
	if formatMap == nil {
		return ""
	}
	d, _ := formatMap["definition"].(string)
	return strings.TrimSpace(d)
}

// IsApplyPatchGrammar checks if a grammar definition is for the apply_patch tool.
func IsApplyPatchGrammar(definition string) bool {
	return strings.Contains(definition, `begin_patch: "*** Begin Patch"`) &&
		strings.Contains(definition, `end_patch: "*** End Patch"`) &&
		strings.Contains(definition, `add_hunk: "*** Add File: "`)
}

// IsExecGrammar checks if a grammar definition is for the exec tool.
func IsExecGrammar(definition string) bool {
	return strings.Contains(definition, "@exec") ||
		(strings.Contains(definition, "pragma_source") && strings.Contains(definition, "plain_source"))
}

// NamespacedToolName joins namespace and name with underscore.
func NamespacedToolName(namespace, name string) string {
	if namespace == "" {
		return name
	}
	if name == "" {
		return namespace
	}
	return namespace + "_" + name
}

// Simple tool input reading

// InputFromRaw extracts the raw input string from a tool input JSON.
// It tries {"input":"..."} first, then falls back to parsing the whole message.
func InputFromRaw(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err == nil {
		if val, ok := obj["input"]; ok {
			var s string
			if err := json.Unmarshal(val, &s); err == nil {
				return s
			}
			return string(val)
		}
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return string(raw)
}

// ToolMap building from format.CoreTool

// BuildToolMapFromCore builds a ToolMap from a slice of format.CoreTool.
// This is called by the adapter after flattening openai.Tools to CoreTools.
// coreTools must be the flattened list; original is the pre-flattening list
// used to recover namespace/function info.
func BuildToolMapFromCore(original []format.CoreTool) ToolMap {
	m := make(ToolMap)
	for _, t := range original {
		if t.Extensions == nil {
			continue
		}
		kind, _ := t.Extensions["codex_tool_kind"].(string)
		openaiName, _ := t.Extensions["codex_openai_name"].(string)
		ns, _ := t.Extensions["codex_namespace"].(string)
		if kind == "" {
			continue
		}
		m[t.Name] = ToolSpec{
			Kind:       ToolKind(kind),
			OpenAIName: openaiName,
			Namespace:  ns,
		}
	}
	return m
}

// OutputItemFromBlock constructs an output item from a tool_use block using tool metadata.
// Returns the item type ("function_call", "custom_tool_call", "local_shell_call"),
// name, namespace, input/arguments string, and action pointer.
func OutputItemFromBlock(
	blockName string,
	toolInput json.RawMessage,
	toolMap ToolMap,
) (itemType, itemName, itemNamespace, toolInputStr string, isLocalShell bool, actionJSON json.RawMessage) {
	spec, ok := toolMap.Lookup(blockName)
	if !ok {
		return "function_call", blockName, "", string(toolInput), false, nil
	}
	switch spec.Kind {
	case ToolLocalShell:
		return "local_shell_call", "", "", "", true, toolInput
	case ToolApplyPatch, ToolExec:
		return "custom_tool_call", spec.OpenAIName, "", RebuildGrammar(blockName, toolInput), false, nil
	case ToolRaw:
		return "custom_tool_call", spec.OpenAIName, "", InputFromRaw(toolInput), false, nil
	case ToolNestedOneOf, ToolNestedAnyOf:
		action, paramsStr, err := DecodeNestedCall(toolInput, spec.Kind)
		if err != nil || action == "" {
			// Fallback: return raw input with blockName as the tool Name.
			return "function_call", blockName, spec.Namespace, string(toolInput), false, nil
		}
		return "function_call", action, spec.Namespace, string(paramsStr), false, nil
	case ToolFunction:
		return "function_call", spec.OpenAIName, spec.Namespace, string(toolInput), false, nil
	default:
		return "function_call", blockName, "", string(toolInput), false, nil
	}
}

// Proxy schema builders (return map[string]any for format.CoreTool.InputSchema)

func ApplyPatchToolActions() []string {
	return []string{"add_file", "delete_file", "update_file", "replace_file", "batch"}
}

func ApplyPatchToolName(base, action string) string {
	if action == "" {
		return base
	}
	return base + "_" + action
}

func ApplyPatchToolNames(name string) []string {
	return []string{
		ApplyPatchToolName(name, "add_file"),
		ApplyPatchToolName(name, "delete_file"),
		ApplyPatchToolName(name, "update_file"),
		ApplyPatchToolName(name, "replace_file"),
		ApplyPatchToolName(name, "batch"),
	}
}

func ApplyPatchProxyCoreTools(name string) []format.CoreTool {
	return []format.CoreTool{
		{
			Name:        ApplyPatchToolName(name, "add_file"),
			Description: "Create one new file. Moon Bridge reconstructs the raw Codex apply_patch grammar from the result.",
			InputSchema: ApplyPatchSingleOpSchema("add_file"),
		},
		{
			Name:        ApplyPatchToolName(name, "delete_file"),
			Description: "Delete one file. Moon Bridge reconstructs the raw Codex apply_patch grammar from the result.",
			InputSchema: ApplyPatchSingleOpSchema("delete_file"),
		},
		{
			Name:        ApplyPatchToolName(name, "update_file"),
			Description: "Edit one existing file with structured hunks.",
			InputSchema: ApplyPatchSingleOpSchema("update_file"),
		},
		{
			Name:        ApplyPatchToolName(name, "replace_file"),
			Description: "Replace one file entirely.",
			InputSchema: ApplyPatchSingleOpSchema("replace_file"),
		},
		{
			Name:        ApplyPatchToolName(name, "batch"),
			Description: "Edit files by providing structured JSON patch operations. Moon Bridge reconstructs the raw Codex apply_patch grammar from the result.",
			InputSchema: ApplyPatchProxySchema(),
		},
	}
}

func ApplyPatchSingleOpSchema(action string) map[string]any {
	properties := map[string]any{
		"path": map[string]any{"type": "string", "description": "Target file path."},
	}
	required := []string{"path"}
	switch action {
	case "add_file", "replace_file":
		properties["content"] = map[string]any{"type": "string", "description": "Full file content."}
		required = append(required, "content")
	case "update_file":
		properties["move_to"] = map[string]any{"type": "string", "description": "Optional destination for moves."}
		properties["hunks"] = ApplyPatchHunksSchema()
		required = append(required, "hunks")
	}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           properties,
		"required":             required,
	}
}

func ApplyPatchProxySchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"operations": map[string]any{
				"type":        "array",
				"description": "Structured patch operations.",
				"minItems":    1,
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"type":    map[string]any{"type": "string", "enum": []string{"add_file", "delete_file", "update_file", "replace_file"}, "description": "Operation type."},
						"path":    map[string]any{"type": "string", "description": "Target file path."},
						"move_to": map[string]any{"type": "string", "description": "Optional destination for moves."},
						"content": map[string]any{"type": "string", "description": "File content."},
						"hunks":   ApplyPatchHunksSchema(),
					},
					"required": []string{"type", "path"},
				},
			},
		},
		"required": []string{"operations"},
	}
}

func ApplyPatchHunksSchema() map[string]any {
	return map[string]any{
		"type":        "array",
		"description": "Structured update hunks.",
		"minItems":    1,
		"items": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"context": map[string]any{"type": "string", "description": "Context header text."},
				"lines": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type":                 "object",
						"additionalProperties": false,
						"properties": map[string]any{
							"op":   map[string]any{"type": "string", "enum": []string{"context", "add", "remove"}},
							"text": map[string]any{"type": "string"},
						},
						"required": []string{"op", "text"},
					},
				},
			},
			"required": []string{"lines"},
		},
	}
}

func ExecProxySchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"source": map[string]any{
				"type":        "string",
				"description": "JavaScript source code, including any // @exec pragmas if needed.",
			},
		},
		"required": []string{"source"},
	}
}

func ExecProxyDescription() string {
	return "Run the Codex Code Mode exec custom tool by providing structured JSON with a source string."
}

func LocalShellSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Command and arguments to execute",
			},
			"working_directory": map[string]any{"type": "string", "description": "Working directory"},
			"timeout_ms":        map[string]any{"type": "integer", "description": "Timeout in milliseconds"},
			"env": map[string]any{
				"type":                 "object",
				"additionalProperties": map[string]any{"type": "string"},
				"description":          "Environment variables",
			},
		},
		"required": []string{"command"},
	}
}

func CustomToolInputSchema(grammar string) map[string]any {
	description := "Raw freeform input for this custom tool."
	if grammar != "" {
		description += "\n\nGrammar:\n" + grammar
	}
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"input": map[string]any{
				"type":        "string",
				"description": description,
			},
		},
		"required": []string{"input"},
	}
}

// InputSchemaWithFallback returns a schema for known Codex tool names.
func InputSchemaWithFallback(params map[string]any, toolName string) map[string]any {
	if len(params) > 0 {
		return params
	}
	switch toolName {
	case "apply_patch", "patch":
		return map[string]any{
			"type": "object",
			"properties": map[string]any{
				"input": map[string]any{"type": "string", "description": "The patch content or operation to apply"},
			},
			"required": []string{"input"},
		}
	case "exec", "exec_command", "local_shell", "shell":
		return map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Command and arguments to execute",
				},
				"description": map[string]any{"type": "string"},
			},
			"required": []string{"command"},
		}
	case "read", "view", "view_image":
		return map[string]any{
			"type": "object",
			"properties": map[string]any{
				"file_path": map[string]any{"type": "string"},
			},
			"required": []string{"file_path"},
		}
	default:
		return map[string]any{"type": "object"}
	}
}

type applyPatchLine struct {
	Op   string `json:"op"`
	Text string `json:"text"`
}

type applyPatchHunk struct {
	Context string           `json:"context"`
	Lines   []applyPatchLine `json:"lines"`
}

type applyPatchSingleOp struct {
	Type    string           `json:"type"`
	Path    string           `json:"path"`
	MoveTo  string           `json:"move_to"`
	Content string           `json:"content"`
	Hunks   []applyPatchHunk `json:"hunks"`
}

type applyPatchBatch struct {
	Operations []applyPatchSingleOp `json:"operations"`
}

// ToolKindForGrammar returns the ToolKind for a custom tool grammar definition.
func ToolKindForGrammar(grammar string, toolName string) (ToolKind, bool) {
	if toolName == "local_shell" {
		return ToolLocalShell, true
	}
	if IsApplyPatchGrammar(grammar) {
		return ToolApplyPatch, true
	}
	if IsExecGrammar(grammar) {
		return ToolExec, true
	}
	return ToolRaw, true
}

// AnnotateCoreTool sets codex metadata extensions on a CoreTool for reverse mapping.
func AnnotateCoreTool(t *format.CoreTool, kind ToolKind, openAIName, namespace string) {
	if t.Extensions == nil {
		t.Extensions = make(map[string]any)
	}
	t.Extensions["codex_tool_kind"] = string(kind)
	t.Extensions["codex_openai_name"] = openAIName
	t.Extensions["codex_namespace"] = namespace
}

func appendApplyPatchOperation(b *strings.Builder, op applyPatchSingleOp) bool {
	if strings.TrimSpace(op.Path) == "" {
		return false
	}

	switch op.Type {
	case "", "batch":
		return false
	case "add_file", "replace_file":
		fmt.Fprintln(b, "*** Begin Patch")
		if op.Type == "replace_file" {
			fmt.Fprintf(b, "*** Update File: %s\n", op.Path)
			if strings.TrimSpace(op.MoveTo) != "" {
				fmt.Fprintf(b, "*** Move to: %s\n", op.MoveTo)
			}
			if op.Content != "" {
				for _, line := range strings.Split(op.Content, "\n") {
					fmt.Fprintf(b, "+%s\n", line)
				}
			}
		} else {
			fmt.Fprintf(b, "*** Add File: %s\n", op.Path)
			if op.Content != "" {
				for _, line := range strings.Split(op.Content, "\n") {
					fmt.Fprintf(b, "+%s\n", line)
				}
			}
		}
		fmt.Fprintln(b, "*** End Patch")
		return true
	case "delete_file":
		fmt.Fprintln(b, "*** Begin Patch")
		fmt.Fprintf(b, "*** Delete File: %s\n", op.Path)
		fmt.Fprintln(b, "*** End Patch")
		return true
	case "update_file":
		fmt.Fprintln(b, "*** Begin Patch")
		fmt.Fprintf(b, "*** Update File: %s\n", op.Path)
		if strings.TrimSpace(op.MoveTo) != "" {
			fmt.Fprintf(b, "*** Move to: %s\n", op.MoveTo)
		}
		for _, hunk := range op.Hunks {
			if strings.TrimSpace(hunk.Context) == "" {
				fmt.Fprintln(b, "@@")
			} else {
				fmt.Fprintf(b, "@@ %s\n", hunk.Context)
			}
			for _, line := range hunk.Lines {
				switch line.Op {
				case "add":
					fmt.Fprintf(b, "+%s\n", line.Text)
				case "remove":
					fmt.Fprintf(b, "-%s\n", line.Text)
				default:
					fmt.Fprintf(b, " %s\n", line.Text)
				}
			}
		}
		fmt.Fprintln(b, "*** End Patch")
		return true
	default:
		return false
	}
}

// RebuildApplyPatchGrammar reconstructs the raw apply_patch grammar from
// a proxy tool name and its structured input, for use as custom_tool_call.input.
func RebuildApplyPatchGrammar(proxyName string, input json.RawMessage) string {
	action := strings.TrimPrefix(proxyName, "apply_patch_")
	if action == proxyName {
		action = "batch"
	}
	switch action {
	case "add_file", "replace_file":
		var p applyPatchSingleOp
		if err := json.Unmarshal(input, &p); err != nil {
			return string(input)
		}
		p.Type = action
		var b strings.Builder
		if !appendApplyPatchOperation(&b, p) {
			return string(input)
		}
		return b.String()
	case "delete_file":
		var p applyPatchSingleOp
		if err := json.Unmarshal(input, &p); err != nil {
			return string(input)
		}
		p.Type = action
		var b strings.Builder
		if !appendApplyPatchOperation(&b, p) {
			return string(input)
		}
		return b.String()
	case "update_file":
		var p applyPatchSingleOp
		if err := json.Unmarshal(input, &p); err != nil {
			return string(input)
		}
		var b strings.Builder
		p.Type = action
		if !appendApplyPatchOperation(&b, p) {
			return string(input)
		}
		return b.String()
	case "batch":
		var p applyPatchBatch
		if err := json.Unmarshal(input, &p); err != nil {
			return string(input)
		}
		var b strings.Builder
		wrote := false
		for _, op := range p.Operations {
			if appendApplyPatchOperation(&b, op) {
				wrote = true
			}
		}
		if !wrote {
			return string(input)
		}
		return b.String()
	default:
		return string(input)
	}
}

// RebuildExecGrammar reconstructs the raw exec grammar from structured input.
func RebuildExecGrammar(input json.RawMessage) string {
	var p struct {
		Source string `json:"source"`
	}
	if err := json.Unmarshal(input, &p); err != nil || p.Source == "" {
		return string(input)
	}
	return p.Source
}

// RebuildGrammar auto-detects the tool type from the proxy tool name and
// reconstructs the raw grammar input for custom_tool_call from structured arguments.
func RebuildGrammar(proxyName string, input json.RawMessage) string {
	// If the model calls the proxy-expanded name (e.g. apply_patch_add_file),
	// reconstruct grammar from structured params. If calling the original name
	// (apply_patch), input is already raw grammar — extract via InputFromRaw.
	if strings.HasPrefix(proxyName, "apply_patch_") {
		return RebuildApplyPatchGrammar(proxyName, input)
	}
	if proxyName == "apply_patch" {
		return InputFromRaw(input)
	}
	if proxyName == "exec" {
		return InputFromRaw(input)
	}
	if strings.HasPrefix(proxyName, "exec_") {
		return RebuildExecGrammar(input)
	}
	return InputFromRaw(input)
}
