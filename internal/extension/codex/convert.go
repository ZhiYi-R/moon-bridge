package codex

import (
	"moonbridge/internal/foundation/openai"
	"moonbridge/internal/protocol/anthropic"
)

// ConvertCodexTool converts a single tool into one or more Anthropic tools
// if it is a Codex-specific type (local_shell, custom, namespace, or built-in).
// Returns the converted tools and true if the type was handled by this function.
// For types that should be silently skipped (file_search, etc.), returns empty slice and true.
// For unrecognized types, returns nil and false (caller should handle).
func ConvertCodexTool(tool openai.Tool) ([]anthropic.Tool, bool) {
	switch tool.Type {
	case "local_shell":
		return []anthropic.Tool{{
			Name:        "local_shell",
			Description: "Run a local shell command. Use only when you need command output from the user's workspace.",
			InputSchema: LocalShellSchema(),
		}}, true
	case "custom":
		return AnthropicCustomToolsFromOpenAI(tool.Name, tool), true
	case "namespace":
		var result []anthropic.Tool
		for _, child := range tool.Tools {
			switch child.Type {
			case "function":
				result = append(result, AnthropicToolFromOpenAIFunction(
					NamespacedToolName(tool.Name, child.Name),
					child.Description,
					child.Parameters,
				))
			case "custom":
				result = append(result, AnthropicCustomToolsFromOpenAI(NamespacedToolName(tool.Name, child.Name), child)...)
			}
		}
		return result, true
	case "file_search", "computer_use_preview", "image_generation", "tool_search":
		// Codex native built-in tools: silently skip as unsupported by Anthropic.
		return nil, true
	default:
		// Not a Codex-specific type; let the caller handle it.
		return nil, false
	}
}

// ConvertCodexToolChoice handles Codex-specific tool_choice mappings such as
// namespace tool names and custom tool apply_patch name resolution.
// Returns the converted ToolChoice and true if a mapping was applied.
func ConvertCodexToolChoice(raw struct {
	Type      string `json:"type"`
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Function  struct {
		Name string `json:"name"`
	} `json:"function"`
}, context ConversionContext) (anthropic.ToolChoice, bool) {
	name := raw.Name
	if name == "" {
		name = raw.Function.Name
	}
	if name == "" {
		return anthropic.ToolChoice{}, false
	}
	if raw.Namespace != "" {
		name = context.AnthropicFunctionToolName(raw.Namespace, name)
	}
	if mapped := context.AnthropicToolChoiceName(name); mapped != "" {
		name = mapped
	}
	return anthropic.ToolChoice{Type: "tool", Name: name}, true
}
