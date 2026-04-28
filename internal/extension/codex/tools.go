package codex

import (
	"encoding/json"
	"strings"

	"moonbridge/internal/foundation/openai"
	"moonbridge/internal/protocol/anthropic"
)

func LocalShellSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "string"},
			},
			"working_directory": map[string]any{"type": "string"},
			"timeout_ms":        map[string]any{"type": "integer"},
			"env": map[string]any{
				"type":                 "object",
				"additionalProperties": map[string]any{"type": "string"},
			},
		},
		"required": []string{"command"},
	}
}

func LocalShellActionFromRaw(raw json.RawMessage) *openai.ToolAction {
	var input struct {
		Command          []string          `json:"command"`
		WorkingDirectory string            `json:"working_directory"`
		TimeoutMS        int               `json:"timeout_ms"`
		Env              map[string]string `json:"env"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return &openai.ToolAction{Type: "exec"}
	}
	return &openai.ToolAction{
		Type:             "exec",
		Command:          input.Command,
		WorkingDirectory: input.WorkingDirectory,
		TimeoutMS:        input.TimeoutMS,
		Env:              input.Env,
	}
}

func LocalShellInputFromAction(action *openai.ToolAction) json.RawMessage {
	if action == nil {
		return json.RawMessage(`{"command":[]}`)
	}
	data, err := json.Marshal(map[string]any{
		"command":           action.Command,
		"working_directory": action.WorkingDirectory,
		"timeout_ms":        action.TimeoutMS,
		"env":               action.Env,
	})
	if err != nil {
		return json.RawMessage(`{"command":[]}`)
	}
	return data
}

func namespacedToolName(namespace string, name string) string {
	if namespace == "" {
		return name
	}
	if name == "" {
		return namespace
	}
	if strings.HasSuffix(namespace, "_") || strings.HasPrefix(name, "_") {
		return namespace + name
	}
	return namespace + "_" + name
}

func NamespacedToolName(namespace string, name string) string {
	return namespacedToolName(namespace, name)
}

// ToolCodec provides helper methods for encoding/decoding Codex tool items.
type ToolCodec struct {
	Context ConversionContext
}

// NewToolCodec creates a ToolCodec from an OpenAI tool list.
func NewToolCodec(tools []openai.Tool) ToolCodec {
	return ToolCodec{
		Context: ConversionContext{
			CustomTools:   CustomToolSpecs(tools, ""),
			FunctionTools: FunctionToolSpecs(tools, ""),
		},
	}
}

// NewToolCodecWithContext creates a ToolCodec with a pre-built ConversionContext.
func NewToolCodecWithContext(context ConversionContext) ToolCodec {
	return ToolCodec{Context: context}
}

// ToolHistoryItem represents a Codex request history item for tool conversion.
type ToolHistoryItem struct {
	ID        string
	CallID    string
	Name      string
	Namespace string
	Arguments string
	Input     string
	Action    *openai.ToolAction
	Output    string
}

// OutputItemFromToolUse converts an Anthropic tool_use content block into
// the corresponding Codex OutputItem (local_shell_call, custom_tool_call, or function_call).
func OutputItemFromToolUse(block anthropic.ContentBlock, context ConversionContext) openai.OutputItem {
	if block.Name == "local_shell" {
		return openai.OutputItem{
			Type:   "local_shell_call",
			ID:     "lc_" + block.ID,
			CallID: block.ID,
			Status: "completed",
			Action: LocalShellActionFromRaw(block.Input),
		}
	}
	if context.IsCustomTool(block.Name) {
		return openai.OutputItem{
			Type:   "custom_tool_call",
			ID:     CustomToolItemID(block.ID),
			CallID: block.ID,
			Name:   context.OpenAINameForCustomTool(block.Name),
			Input:  context.CustomToolInputFromRaw(block.Name, block.Input),
			Status: "completed",
		}
	}
	name, namespace := context.OpenAIFunctionToolName(block.Name)
	return openai.OutputItem{
		Type:      "function_call",
		ID:        "fc_" + block.ID,
		CallID:    block.ID,
		Name:      name,
		Namespace: namespace,
		Arguments: string(block.Input),
		Status:    "completed",
	}
}

// OutputItemForToolUseStart creates the initial streaming output item for a tool use content block start.
func OutputItemForToolUseStart(block anthropic.ContentBlock, context ConversionContext) openai.OutputItem {
	if block.Name == "local_shell" {
		return openai.OutputItem{
			Type:   "local_shell_call",
			ID:     "lc_" + block.ID,
			CallID: block.ID,
			Status: "in_progress",
			Action: LocalShellActionFromRaw(block.Input),
		}
	}
	if context.IsCustomTool(block.Name) {
		return openai.OutputItem{
			Type:   "custom_tool_call",
			ID:     CustomToolItemID(block.ID),
			CallID: block.ID,
			Name:   context.OpenAINameForCustomTool(block.Name),
			Input:  "",
			Status: "in_progress",
		}
	}
	name, namespace := context.OpenAIFunctionToolName(block.Name)
	return openai.OutputItem{
		Type:      "function_call",
		ID:        "fc_" + block.ID,
		CallID:    block.ID,
		Name:      name,
		Namespace: namespace,
		Arguments: "",
		Status:    "in_progress",
	}
}

// CompleteCustomToolCall finalizes a streaming custom tool call with its accumulated input.
func CompleteCustomToolCall(item openai.OutputItem, itemID string, toolName string, inputJSON string, context ConversionContext) (openai.OutputItem, string) {
	input := context.CustomToolInputFromRaw(toolName, json.RawMessage(inputJSON))
	item.ID = itemID
	item.Name = context.OpenAINameForCustomTool(toolName)
	item.Input = input
	item.Status = "completed"
	return item, input
}

// CompleteLocalShellCall finalizes a streaming local shell call with its accumulated arguments.
func CompleteLocalShellCall(itemID string, arguments string) openai.OutputItem {
	return openai.OutputItem{
		Type:   "local_shell_call",
		ID:     itemID,
		CallID: strings.TrimPrefix(itemID, "lc_"),
		Action: LocalShellActionFromRaw(json.RawMessage(arguments)),
		Status: "completed",
	}
}

// CompleteFunctionCall finalizes a streaming function call with its accumulated arguments.
func CompleteFunctionCall(item openai.OutputItem, itemID string, arguments string) openai.OutputItem {
	item.ID = itemID
	item.Arguments = arguments
	item.Status = "completed"
	return item
}
