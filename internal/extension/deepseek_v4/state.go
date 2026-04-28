package deepseekv4

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"sync"

	"moonbridge/internal/protocol/anthropic"
)

const persistedThinkingSummaryPrefix = "moonbridge:deepseek_v4_thinking:v1:"

type persistedThinkingSummary struct {
	Thinking  string `json:"thinking"`
	Signature string `json:"signature,omitempty"`
}

type State struct {
	mu          sync.Mutex
	records     map[string]anthropic.ContentBlock
	textRecords map[string]anthropic.ContentBlock
	order       []string
	textOrder   []string
	limit       int
}

type StreamState struct {
	thinkingText      map[int]string
	thinkingSignature map[int]string
	completedThinking anthropic.ContentBlock
	toolCallIDs       []string
}

func NewState() *State {
	return &State{
		records:     map[string]anthropic.ContentBlock{},
		textRecords: map[string]anthropic.ContentBlock{},
		limit:       1024,
	}
}

func NewStreamState() *StreamState {
	return &StreamState{
		thinkingText:      map[int]string{},
		thinkingSignature: map[int]string{},
	}
}

func (stream *StreamState) Reset(index int) {
	if stream == nil {
		return
	}
	delete(stream.thinkingText, index)
	delete(stream.thinkingSignature, index)
}

func (state *State) RememberForToolCalls(toolCallIDs []string, block anthropic.ContentBlock) {
	if state == nil || !hasThinkingPayload(block) || len(toolCallIDs) == 0 {
		return
	}
	block = normalizeThinkingBlock(block)
	state.mu.Lock()
	defer state.mu.Unlock()
	for _, toolCallID := range toolCallIDs {
		if toolCallID == "" {
			continue
		}
		if _, exists := state.records[toolCallID]; !exists {
			state.order = append(state.order, toolCallID)
		}
		state.records[toolCallID] = block
	}
	state.pruneLocked()
}

func (state *State) RememberForAssistantText(text string, block anthropic.ContentBlock) {
	if state == nil || text == "" || !hasThinkingPayload(block) {
		return
	}
	key := thinkingTextKey(text)
	state.mu.Lock()
	defer state.mu.Unlock()
	if _, exists := state.textRecords[key]; !exists {
		state.textOrder = append(state.textOrder, key)
	}
	state.textRecords[key] = normalizeThinkingBlock(block)
	state.pruneLocked()
}

func (state *State) RememberFromContent(blocks []anthropic.ContentBlock) {
	var thinkingBlock anthropic.ContentBlock
	var toolCallIDs []string
	var assistantText string
	for _, block := range blocks {
		switch block.Type {
		case "thinking":
			thinkingBlock = block
		case "reasoning_content":
			thinkingBlock = anthropic.ContentBlock{Type: "thinking", Thinking: block.Text}
		case "tool_use":
			toolCallIDs = append(toolCallIDs, block.ID)
		case "text":
			assistantText += block.Text
		}
	}
	state.RememberForToolCalls(toolCallIDs, thinkingBlock)
	state.RememberForAssistantText(assistantText, thinkingBlock)
}

func (state *State) RememberStreamResult(stream *StreamState, outputText string) {
	if state == nil || stream == nil || !hasThinkingPayload(stream.completedThinking) {
		return
	}
	if len(stream.toolCallIDs) > 0 {
		state.RememberForToolCalls(stream.toolCallIDs, stream.completedThinking)
		return
	}
	state.RememberForAssistantText(outputText, stream.completedThinking)
}

func (state *State) PrependCachedForToolUse(messages *[]anthropic.Message, toolCallID string) {
	block, ok := state.cachedForToolCall(toolCallID)
	if !ok {
		return
	}
	PrependThinkingBlockForToolUse(messages, block)
}

func PrependRequiredThinkingForToolUse(messages *[]anthropic.Message) bool {
	return PrependThinkingBlockForToolUse(messages, RequiredThinkingBlock())
}

func PrependThinkingBlockForToolUse(messages *[]anthropic.Message, block anthropic.ContentBlock) bool {
	block = normalizeThinkingBlock(block)
	lastIndex := len(*messages) - 1
	if lastIndex < 0 || (*messages)[lastIndex].Role != "assistant" {
		// Don't append a bare assistant message; that would break
		// the user/assistant role alternation expected by providers.
		return false
	}
	if HasThinkingBlock((*messages)[lastIndex].Content) {
		return false
	}
	(*messages)[lastIndex].Content = append([]anthropic.ContentBlock{block}, (*messages)[lastIndex].Content...)
	return true
}

func (state *State) PrependCachedForAssistantText(blocks []anthropic.ContentBlock) []anthropic.ContentBlock {
	if HasThinkingBlock(blocks) {
		return blocks
	}
	block, ok := state.cachedForAssistantText(textFromBlocks(blocks))
	if !ok {
		return blocks
	}
	return append([]anthropic.ContentBlock{block}, blocks...)
}

func PrependRequiredThinkingForAssistantText(blocks []anthropic.ContentBlock) ([]anthropic.ContentBlock, bool) {
	return PrependThinkingBlockForAssistantText(blocks, RequiredThinkingBlock())
}

func PrependThinkingBlockForAssistantText(blocks []anthropic.ContentBlock, block anthropic.ContentBlock) ([]anthropic.ContentBlock, bool) {
	if HasThinkingBlock(blocks) {
		return blocks, false
	}
	return append([]anthropic.ContentBlock{normalizeThinkingBlock(block)}, blocks...), true
}

func (stream *StreamState) Start(index int, block *anthropic.ContentBlock) bool {
	if stream == nil || block == nil || !IsReasoningContentBlock(block) {
		return false
	}
	stream.thinkingText[index] = firstNonEmpty(block.Thinking, block.Text)
	stream.thinkingSignature[index] = block.Signature
	return true
}

func (stream *StreamState) Delta(index int, delta anthropic.StreamDelta) bool {
	if stream == nil {
		return false
	}
	switch delta.Type {
	case "thinking_delta", "reasoning_content_delta":
		stream.thinkingText[index] += firstNonEmpty(delta.Thinking, delta.Text)
		return true
	case "signature_delta":
		stream.thinkingSignature[index] += firstNonEmpty(delta.Signature, delta.Text)
		return true
	default:
		return false
	}
}

func (stream *StreamState) CompletedThinkingText() string {
	if stream == nil {
		return ""
	}
	return EncodeThinkingSummary(stream.completedThinking)
}

func (stream *StreamState) Stop(index int) bool {
	if stream == nil {
		return false
	}
	text, ok := stream.thinkingText[index]
	if !ok {
		return false
	}
	stream.completedThinking = anthropic.ContentBlock{
		Type:      "thinking",
		Thinking:  text,
		Signature: stream.thinkingSignature[index],
	}
	return true
}

func (stream *StreamState) RecordToolCall(toolCallID string) {
	if stream == nil || toolCallID == "" {
		return
	}
	stream.toolCallIDs = append(stream.toolCallIDs, toolCallID)
}

func (state *State) cachedForToolCall(toolCallID string) (anthropic.ContentBlock, bool) {
	if state == nil || toolCallID == "" {
		return anthropic.ContentBlock{}, false
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	block, ok := state.records[toolCallID]
	return block, ok
}

func (state *State) cachedForAssistantText(text string) (anthropic.ContentBlock, bool) {
	if state == nil || text == "" {
		return anthropic.ContentBlock{}, false
	}
	key := thinkingTextKey(text)
	state.mu.Lock()
	defer state.mu.Unlock()
	block, ok := state.textRecords[key]
	return block, ok
}

func (state *State) pruneLocked() {
	for len(state.order) > state.limit {
		oldestToolCallID := state.order[0]
		state.order = state.order[1:]
		delete(state.records, oldestToolCallID)
	}
	for len(state.textOrder) > state.limit {
		oldestTextKey := state.textOrder[0]
		state.textOrder = state.textOrder[1:]
		delete(state.textRecords, oldestTextKey)
	}
}

func HasThinkingBlock(blocks []anthropic.ContentBlock) bool {
	for _, block := range blocks {
		if block.Type == "thinking" {
			return true
		}
	}
	return false
}

func RequiredThinkingBlock() anthropic.ContentBlock {
	return anthropic.ContentBlock{Type: "thinking", Thinking: ""}
}

func EncodeThinkingSummary(block anthropic.ContentBlock) string {
	block = normalizeThinkingBlock(block)
	if block.Thinking != "" {
		return block.Thinking
	}
	if block.Signature == "" {
		return ""
	}
	payload, err := json.Marshal(persistedThinkingSummary{
		Thinking:  block.Thinking,
		Signature: block.Signature,
	})
	if err != nil {
		return ""
	}
	return persistedThinkingSummaryPrefix + base64.RawURLEncoding.EncodeToString(payload)
}

func DecodeThinkingSummary(text string) (anthropic.ContentBlock, bool) {
	if text == "" {
		return anthropic.ContentBlock{}, false
	}
	if !strings.HasPrefix(text, persistedThinkingSummaryPrefix) {
		return anthropic.ContentBlock{Type: "thinking", Thinking: text}, true
	}
	encoded := strings.TrimPrefix(text, persistedThinkingSummaryPrefix)
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return anthropic.ContentBlock{Type: "thinking", Thinking: text}, true
	}
	var decoded persistedThinkingSummary
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return anthropic.ContentBlock{Type: "thinking", Thinking: text}, true
	}
	block := anthropic.ContentBlock{
		Type:      "thinking",
		Thinking:  decoded.Thinking,
		Signature: decoded.Signature,
	}
	if !hasThinkingPayload(block) {
		return anthropic.ContentBlock{}, false
	}
	return block, true
}

func hasThinkingPayload(block anthropic.ContentBlock) bool {
	return block.Type == "thinking" && (block.Thinking != "" || block.Signature != "")
}

func normalizeThinkingBlock(block anthropic.ContentBlock) anthropic.ContentBlock {
	return anthropic.ContentBlock{
		Type:      "thinking",
		Thinking:  block.Thinking,
		Signature: block.Signature,
	}
}

func textFromBlocks(blocks []anthropic.ContentBlock) string {
	var builder strings.Builder
	for _, block := range blocks {
		if block.Type == "text" {
			builder.WriteString(block.Text)
		}
	}
	return builder.String()
}

func thinkingTextKey(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}
