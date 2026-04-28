package codex

import (
	"encoding/json"
	"io"
	"strings"

	"moonbridge/internal/foundation/openai"
	"moonbridge/internal/protocol/anthropic"
)

// InputItem represents a single item in an OpenAI Responses input array,
// including Codex-specific extensions such as custom_tool_call, local_shell_call,
// reasoning summaries, and web_search_call.
type InputItem struct {
	Type      string                        `json:"type"`
	ID        string                        `json:"id"`
	Role      string                        `json:"role"`
	Phase     string                        `json:"phase"`
	Content   json.RawMessage               `json:"content"`
	CallID    string                        `json:"call_id"`
	Name      string                        `json:"name"`
	Namespace string                        `json:"namespace"`
	Arguments string                        `json:"arguments"`
	Input     string                        `json:"input"`
	Action    *openai.ToolAction            `json:"action"`
	Summary   []openai.ReasoningItemSummary `json:"summary,omitempty"`
	Output    string                        `json:"output"`
}

// ToolInputFromArguments recovers histories poisoned by concatenated tool
// argument objects and replaces other malformed JSON with a valid sentinel.
func ToolInputFromArguments(arguments string) json.RawMessage {
	trimmed := strings.TrimSpace(arguments)
	if trimmed == "" {
		return json.RawMessage(`{}`)
	}
	if json.Valid([]byte(trimmed)) {
		return json.RawMessage(trimmed)
	}
	if recovered, ok := lastConcatenatedJSONValue(trimmed); ok {
		return recovered
	}
	return json.RawMessage(`{"invalid_argument":true}`)
}

func lastConcatenatedJSONValue(value string) (json.RawMessage, bool) {
	decoder := json.NewDecoder(strings.NewReader(value))
	var last json.RawMessage
	count := 0
	for {
		var raw json.RawMessage
		err := decoder.Decode(&raw)
		if err == io.EOF {
			return last, count > 1
		}
		if err != nil {
			return nil, false
		}
		last = append(json.RawMessage(nil), raw...)
		count++
	}
}

// IsEmptyWebSearchPreludeBlocks checks if the content blocks consist of only
// the empty web search prelude text (from injected web search).
func IsEmptyWebSearchPreludeBlocks(blocks []anthropic.ContentBlock) bool {
	if len(blocks) != 1 || blocks[0].Type != "text" {
		return false
	}
	return IsEmptyWebSearchPrelude(blocks[0].Text)
}

// IsEmptyWebSearchPrelude checks for the injected web search "Search results for query:" prelude text.
// This is a Codex/web-search bridge compatibility filter.
func IsEmptyWebSearchPrelude(text string) bool {
	return strings.TrimSpace(text) == "Search results for query:"
}

// AppendAssistantBlock appends a content block to the last assistant message,
// creating a new assistant message if needed.
func AppendAssistantBlock(messages *[]anthropic.Message, block anthropic.ContentBlock) {
	lastIndex := len(*messages) - 1
	if lastIndex >= 0 && (*messages)[lastIndex].Role == "assistant" {
		(*messages)[lastIndex].Content = append((*messages)[lastIndex].Content, block)
		return
	}
	*messages = append(*messages, anthropic.Message{Role: "assistant", Content: []anthropic.ContentBlock{block}})
}

// AppendToolResultBlock appends a tool_result block to the last user message
// that already contains only tool_result blocks, creating a new user message otherwise.
func AppendToolResultBlock(messages *[]anthropic.Message, block anthropic.ContentBlock) {
	lastIndex := len(*messages) - 1
	if lastIndex >= 0 && (*messages)[lastIndex].Role == "user" && AllContentBlocksHaveType((*messages)[lastIndex].Content, "tool_result") {
		(*messages)[lastIndex].Content = append((*messages)[lastIndex].Content, block)
		return
	}
	*messages = append(*messages, anthropic.Message{Role: "user", Content: []anthropic.ContentBlock{block}})
}

// AllContentBlocksHaveType checks if all blocks in a slice have the given type.
func AllContentBlocksHaveType(blocks []anthropic.ContentBlock, blockType string) bool {
	if len(blocks) == 0 {
		return false
	}
	for _, block := range blocks {
		if block.Type != blockType {
			return false
		}
	}
	return true
}

// InputItemConversion describes the result of converting a Codex-specific input item.
type InputItemConversion struct {
	Handled           bool
	Skip              bool
	ToolUse           *anthropic.ContentBlock
	ConsumesReasoning bool
	MarksToolHistory  bool
}

// ConvertInputItem converts a Codex-specific InputItem (custom_tool_call, local_shell_call,
// commentary, web_search_call) into an InputItemConversion that bridge can use.
// Returns a zero-value InputItemConversion (Handled=false) for unrecognized types
// that should be handled by bridge's standard input conversion logic.
func ConvertInputItem(item InputItem, context ConversionContext) InputItemConversion {
	switch {
	case item.Phase == "commentary":
		return InputItemConversion{Handled: true, Skip: true}

	case item.Type == "web_search_call":
		return InputItemConversion{Handled: true, Skip: true}

	case item.Type == "custom_tool_call":
		toolName := item.Name
		toolInput := json.RawMessage(item.Arguments)
		if strings.TrimSpace(item.Arguments) == "" {
			toolName, toolInput = context.AnthropicToolUseForCustomTool(item.Name, item.Input)
		} else {
			toolInput = ToolInputFromArguments(item.Arguments)
		}
		return InputItemConversion{
			Handled: true,
			ToolUse: &anthropic.ContentBlock{
				Type:  "tool_use",
				ID:    firstNonEmptyString(item.CallID, item.ID),
				Name:  toolName,
				Input: toolInput,
			},
			ConsumesReasoning: true,
			MarksToolHistory:  true,
		}

	case item.Type == "local_shell_call":
		return InputItemConversion{
			Handled: true,
			ToolUse: &anthropic.ContentBlock{
				Type:  "tool_use",
				ID:    firstNonEmptyString(item.CallID, item.ID),
				Name:  "local_shell",
				Input: LocalShellInputFromAction(item.Action),
			},
			ConsumesReasoning: true,
			MarksToolHistory:  true,
		}

	default:
		return InputItemConversion{}
	}
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
