package deepseekv4

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"sync"

	"github.com/ZhiYi-R/moon-bridge/internal/format"
	"github.com/ZhiYi-R/moon-bridge/internal/protocol/anthropic"
)

const persistedThinkingSummaryPrefix = "moonbridge:deepseek_v4_thinking:v1:"

type persistedThinkingSummary struct {
	Thinking  string `json:"thinking"`
	Signature string `json:"signature,omitempty"`
}

type State struct {
	mu          sync.Mutex
	records     map[string]format.CoreContentBlock
	textRecords map[string]format.CoreContentBlock
	order       []string
	textOrder   []string
	limit       int
}

type StreamState struct {
	thinkingText      map[int]string
	thinkingSignature map[int]string
	completedThinking format.CoreContentBlock
	toolCallIDs       []string
}

func NewState() *State {
	return &State{
		records:     map[string]format.CoreContentBlock{},
		textRecords: map[string]format.CoreContentBlock{},
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

func (state *State) RememberForToolCalls(toolCallIDs []string, block format.CoreContentBlock) {
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

func (state *State) RememberForAssistantText(text string, block format.CoreContentBlock) {
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

func (state *State) RememberFromContent(blocks []format.CoreContentBlock) {
	var thinkingBlock format.CoreContentBlock
	var toolCallIDs []string
	var assistantText string
	for _, block := range blocks {
		switch block.Type {
		case "reasoning":
			thinkingBlock = block
		case "reasoning_content":
			thinkingBlock = format.CoreContentBlock{Type: "reasoning", ReasoningText: block.Text}
		case "tool_use":
			toolCallIDs = append(toolCallIDs, block.ToolUseID)
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
	block, ok := state.CachedForToolCall(toolCallID)
	if !ok {
		return
	}
	PrependThinkingBlockForToolUse(messages, block)
}

func PrependRequiredThinkingForToolUse(messages *[]anthropic.Message) bool {
	return PrependThinkingBlockForToolUse(messages, RequiredThinkingBlock())
}

func PrependThinkingBlockForToolUse(messages *[]anthropic.Message, block format.CoreContentBlock) bool {
	block = normalizeThinkingBlock(block)
	lastIndex := len(*messages) - 1
	if lastIndex < 0 || (*messages)[lastIndex].Role != "assistant" {
		return false
	}
	if HasThinkingBlock(anthropicBlocksToCore((*messages)[lastIndex].Content)) {
		return false
	}
	(*messages)[lastIndex].Content = append([]anthropic.ContentBlock{coreBlockToAnthropic(block)}, (*messages)[lastIndex].Content...)
	return true
}

func (state *State) PrependCachedForAssistantText(blocks []format.CoreContentBlock) []format.CoreContentBlock {
	if HasThinkingBlock(blocks) {
		return blocks
	}
	block, ok := state.cachedForAssistantText(textFromBlocks(blocks))
	if !ok {
		return blocks
	}
	return append([]format.CoreContentBlock{block}, blocks...)
}

func PrependRequiredThinkingForAssistantText(blocks []format.CoreContentBlock) ([]format.CoreContentBlock, bool) {
	return PrependThinkingBlockForAssistantText(blocks, RequiredThinkingBlock())
}

func PrependThinkingBlockForAssistantText(blocks []format.CoreContentBlock, block format.CoreContentBlock) ([]format.CoreContentBlock, bool) {
	if HasThinkingBlock(blocks) {
		return blocks, false
	}
	return append([]format.CoreContentBlock{normalizeThinkingBlock(block)}, blocks...), true
}

func (stream *StreamState) Start(index int, block *format.CoreContentBlock) bool {
	if stream == nil || block == nil || !IsReasoningContentBlock(block) {
		return false
	}
	stream.thinkingText[index] = firstNonEmpty(block.ReasoningText, block.Text)
	stream.thinkingSignature[index] = block.ReasoningSignature
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
	stream.completedThinking = format.CoreContentBlock{
		Type:               "reasoning",
		ReasoningText:      text,
		ReasoningSignature: stream.thinkingSignature[index],
	}
	return true
}

func (stream *StreamState) RecordToolCall(toolCallID string) {
	if stream == nil || toolCallID == "" {
		return
	}
	stream.toolCallIDs = append(stream.toolCallIDs, toolCallID)
}

func (state *State) CachedForToolCall(toolCallID string) (format.CoreContentBlock, bool) {
	if state == nil || toolCallID == "" {
		return format.CoreContentBlock{}, false
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	block, ok := state.records[toolCallID]
	return block, ok
}

func (state *State) cachedForAssistantText(text string) (format.CoreContentBlock, bool) {
	if state == nil || text == "" {
		return format.CoreContentBlock{}, false
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

func HasThinkingBlock(blocks []format.CoreContentBlock) bool {
	for _, block := range blocks {
		if block.Type == "reasoning" {
			return true
		}
	}
	return false
}

func RequiredThinkingBlock() format.CoreContentBlock {
	return format.CoreContentBlock{Type: "reasoning", ReasoningText: ""}
}

func EncodeThinkingSummary(block format.CoreContentBlock) string {
	block = normalizeThinkingBlock(block)
	if block.ReasoningText != "" {
		return block.ReasoningText
	}
	if block.ReasoningSignature == "" {
		return ""
	}
	payload, err := json.Marshal(persistedThinkingSummary{
		Thinking:  block.ReasoningText,
		Signature: block.ReasoningSignature,
	})
	if err != nil {
		return ""
	}
	return persistedThinkingSummaryPrefix + base64.RawURLEncoding.EncodeToString(payload)
}

func DecodeThinkingSummary(text string) (format.CoreContentBlock, bool) {
	if text == "" {
		return format.CoreContentBlock{}, false
	}
	if !strings.HasPrefix(text, persistedThinkingSummaryPrefix) {
		return format.CoreContentBlock{Type: "reasoning", ReasoningText: text}, true
	}
	encoded := strings.TrimPrefix(text, persistedThinkingSummaryPrefix)
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return format.CoreContentBlock{Type: "reasoning", ReasoningText: text}, true
	}
	var decoded persistedThinkingSummary
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return format.CoreContentBlock{Type: "reasoning", ReasoningText: text}, true
	}
	block := format.CoreContentBlock{
		Type:               "reasoning",
		ReasoningText:      decoded.Thinking,
		ReasoningSignature: decoded.Signature,
	}
	if !hasThinkingPayload(block) {
		return format.CoreContentBlock{}, false
	}
	return block, true
}

func hasThinkingPayload(block format.CoreContentBlock) bool {
	return block.Type == "reasoning" && (block.ReasoningText != "" || block.ReasoningSignature != "")
}

func normalizeThinkingBlock(block format.CoreContentBlock) format.CoreContentBlock {
	return format.CoreContentBlock{
		Type:               "reasoning",
		ReasoningText:      block.ReasoningText,
		ReasoningSignature: block.ReasoningSignature,
	}
}

func textFromBlocks(blocks []format.CoreContentBlock) string {
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
