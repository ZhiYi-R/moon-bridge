package codex

import (
	"encoding/json"
	"strings"

	"moonbridge/internal/foundation/openai"
	"moonbridge/internal/protocol/anthropic"
)

// ConvertToolUseOutput converts an Anthropic tool_use content block to an OpenAI
// OutputItem, handling Codex-specific types (custom_tool_call, local_shell_call)
// as well as standard function_call mappings.
func ConvertToolUseOutput(block anthropic.ContentBlock, context ConversionContext) (openai.OutputItem, bool) {
	if block.Type != "tool_use" {
		return openai.OutputItem{}, false
	}
	return OutputItemFromToolUse(block, context), true
}

// ConvertServerToolUseOutput converts an Anthropic server_tool_use content block
// to an OpenAI OutputItem. Currently only handles web_search.
func ConvertServerToolUseOutput(block anthropic.ContentBlock) (openai.OutputItem, bool) {
	if block.Type != "server_tool_use" || block.Name != "web_search" {
		return openai.OutputItem{}, false
	}
	action := WebSearchActionFromRaw(block.Input)
	if !HasWebSearchActionDetails(action) {
		return openai.OutputItem{}, false
	}
	return openai.OutputItem{
		Type:   "web_search_call",
		ID:     WebSearchItemID(block.ID),
		Status: "completed",
		Action: action,
	}, true
}

// WebSearchItemID generates a web_search_call item ID from a provider ID.
func WebSearchItemID(providerID string) string {
	if providerID == "" {
		return "ws_generated"
	}
	if strings.HasPrefix(providerID, "ws_") {
		return providerID
	}
	return "ws_" + providerID
}

// WebSearchActionFromRaw parses an Anthropic web_search server_tool_use input
// into an OpenAI ToolAction.
func WebSearchActionFromRaw(raw []byte) *openai.ToolAction {
	action := &openai.ToolAction{Type: "search"}
	if len(raw) == 0 || string(raw) == "null" {
		return action
	}
	var input struct {
		Type    string   `json:"type"`
		Query   string   `json:"query"`
		Queries []string `json:"queries"`
		URL     string   `json:"url"`
		Pattern string   `json:"pattern"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return action
	}
	if input.Type != "" {
		action.Type = input.Type
	}
	action.Query = input.Query
	action.Queries = input.Queries
	action.URL = input.URL
	action.Pattern = input.Pattern
	if action.Type == "" {
		action.Type = "search"
	}
	return action
}

// HasWebSearchActionDetails checks if a web search action has any meaningful fields.
func HasWebSearchActionDetails(action *openai.ToolAction) bool {
	if action == nil {
		return false
	}
	return action.Query != "" || len(action.Queries) > 0 || action.URL != "" || action.Pattern != ""
}
