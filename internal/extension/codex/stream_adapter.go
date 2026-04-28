package codex

import (
	"encoding/json"

	"moonbridge/internal/foundation/openai"
	"moonbridge/internal/protocol/anthropic"
)

// StreamAdapter manages Codex-specific stream state for custom tool calls,
// local shell calls, web search actions, and reasoning emission.
type StreamAdapter struct {
	Context ConversionContext

	customToolInputs        map[int]string
	customToolInitialInputs map[int]string
	customToolNames         map[int]string

	webSearchActions map[int]*openai.ToolAction
	webSearchInputs  map[int]string

	pendingReasoningText string
	pendingReasoningID   int
	emittedReasoningID   int
	hasToolCalls         bool
}

func NewStreamAdapter(context ConversionContext) *StreamAdapter {
	return &StreamAdapter{
		Context:                 context,
		customToolInputs:        make(map[int]string),
		customToolInitialInputs: make(map[int]string),
		customToolNames:         make(map[int]string),
		webSearchActions:        make(map[int]*openai.ToolAction),
		webSearchInputs:         make(map[int]string),
	}
}

func (a *StreamAdapter) ResetBlock(index int) {
	delete(a.customToolInputs, index)
	delete(a.customToolInitialInputs, index)
	delete(a.customToolNames, index)
	delete(a.webSearchActions, index)
	delete(a.webSearchInputs, index)
}

func (a *StreamAdapter) SetPendingReasoning(text string) {
	if text != "" && text != a.pendingReasoningText {
		a.pendingReasoningID++
	}
	a.pendingReasoningText = text
}

func (a *StreamAdapter) HasToolCalls() bool {
	return a.hasToolCalls
}

// StreamToolStartResult is returned by ToolUseStart.
type StreamToolStartResult struct {
	Handled              bool
	Item                 openai.OutputItem
	ItemID               string
	EmitPendingReasoning bool
}

func (a *StreamAdapter) ToolUseStart(index int, block anthropic.ContentBlock) StreamToolStartResult {
	a.hasToolCalls = true
	item := OutputItemForToolUseStart(block, a.Context)
	if item.Type == "custom_tool_call" {
		a.customToolInputs[index] = ""
		a.customToolNames[index] = block.Name
		if len(block.Input) > 0 && string(block.Input) != "{}" {
			a.customToolInitialInputs[index] = string(block.Input)
		}
	}
	return StreamToolStartResult{
		Handled:              true,
		Item:                 item,
		ItemID:               item.ID,
		EmitPendingReasoning: true,
	}
}

// StreamServerToolStartResult is returned by ServerToolUseStart.
type StreamServerToolStartResult struct {
	Handled bool
	ItemID  string
}

func (a *StreamAdapter) ServerToolUseStart(index int, block anthropic.ContentBlock) StreamServerToolStartResult {
	if block.Type != "server_tool_use" || block.Name != "web_search" {
		return StreamServerToolStartResult{}
	}
	a.webSearchActions[index] = WebSearchActionFromRaw(block.Input)
	return StreamServerToolStartResult{
		Handled: true,
		ItemID:  WebSearchItemID(block.ID),
	}
}

// StreamInputDeltaResult is returned by InputJSONDelta.
type StreamInputDeltaResult struct {
	Handled       bool
	SuppressDelta bool
}

func (a *StreamAdapter) InputJSONDelta(index int, partialJSON string) StreamInputDeltaResult {
	if _, ok := a.webSearchActions[index]; ok {
		a.webSearchInputs[index] += partialJSON
		return StreamInputDeltaResult{Handled: true, SuppressDelta: true}
	}
	if _, ok := a.customToolInputs[index]; ok {
		a.customToolInputs[index] += partialJSON
		return StreamInputDeltaResult{Handled: true, SuppressDelta: true}
	}
	return StreamInputDeltaResult{}
}

// StreamStopResult is returned by Stop.
type StreamStopResult struct {
	Handled              bool
	AddOutput            bool
	SetOutput            bool
	Item                 openai.OutputItem
	CustomToolInputDelta string
	SuppressFunctionDone bool
}

func (a *StreamAdapter) Stop(index int, itemID string, currentItem openai.OutputItem, arguments string) StreamStopResult {
	if action, ok := a.webSearchActions[index]; ok {
		if input := a.webSearchInputs[index]; input != "" {
			action = WebSearchActionFromRaw([]byte(compactJSON(input)))
		}
		if !HasWebSearchActionDetails(action) {
			return StreamStopResult{Handled: true}
		}
		item := openai.OutputItem{
			Type:   "web_search_call",
			ID:     itemID,
			Status: "in_progress",
			Action: action,
		}
		return StreamStopResult{
			Handled:   true,
			AddOutput: true,
			Item:      item,
		}
	}

	if inputJSON, ok := a.customToolInputs[index]; ok {
		if inputJSON == "" {
			inputJSON = a.customToolInitialInputs[index]
		}
		toolName := a.customToolNames[index]
		if toolName == "" {
			toolName = currentItem.Name
		}
		item, input := CompleteCustomToolCall(currentItem, itemID, toolName, compactJSON(inputJSON), a.Context)
		if item.CallID == "" {
			item.CallID = trimCTCPrefix(itemID)
		}
		return StreamStopResult{
			Handled:              true,
			SetOutput:            true,
			Item:                 item,
			CustomToolInputDelta: input,
		}
	}

	return StreamStopResult{}
}

// PendingReasoningItem returns a reasoning OutputItem if pending reasoning text exists
// and has not been emitted yet. Returns ok=false if already emitted or no text.
func (a *StreamAdapter) PendingReasoningItem(outputIndex int) (openai.OutputItem, bool) {
	if a.pendingReasoningText == "" || a.emittedReasoningID >= a.pendingReasoningID {
		return openai.OutputItem{}, false
	}
	a.emittedReasoningID = a.pendingReasoningID
	return openai.OutputItem{
		Type: "reasoning",
		ID:   "rsn_" + itoa(outputIndex),
		Summary: []openai.ReasoningItemSummary{{
			Type: "summary_text",
			Text: a.pendingReasoningText,
		}},
	}, true
}

func trimCTCPrefix(id string) string {
	if len(id) > 4 && id[:4] == "ctc_" {
		return id[4:]
	}
	return id
}

func compactJSON(value string) string {
	var raw any
	if err := json.Unmarshal([]byte(value), &raw); err != nil {
		return value
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return value
	}
	return string(data)
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	var digits [20]byte
	index := len(digits)
	for value > 0 {
		index--
		digits[index] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[index:])
}
