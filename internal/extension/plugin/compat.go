package plugin

// Compat methods provide backward-compatible dispatch signatures matching
// the old extension.Registry API. These allow the bridge to migrate to the
// new plugin.Registry without changing every call site at once.
// TODO: Remove these once the bridge is fully migrated to use RequestContext.

import (
	"moonbridge/internal/foundation/openai"
	"moonbridge/internal/protocol/anthropic"
)

// PostConvertRequest is a compat wrapper for MutateRequest.
func (r *Registry) PostConvertRequest(model string, req *anthropic.MessageRequest, reasoning map[string]any) {
	if r == nil {
		return
	}
	ctx := &RequestContext{ModelAlias: model, Reasoning: reasoning}
	r.MutateRequest(ctx, req)
}

// RememberResponseContent is a compat wrapper for RememberContent.
func (r *Registry) RememberResponseContent(model string, content []anthropic.ContentBlock, sessionData map[string]any) {
	if r == nil {
		return
	}
	ctx := &RequestContext{ModelAlias: model, SessionData: sessionData}
	r.RememberContent(ctx, content)
}

// OnResponseContent is a compat wrapper for FilterContent.
func (r *Registry) OnResponseContent(model string, block anthropic.ContentBlock) (skip bool, reasoningText string) {
	if r == nil {
		return false, ""
	}
	ctx := &RequestContext{ModelAlias: model}
	s, extra := r.FilterContent(ctx, block)
	for _, item := range extra {
		if item.Type == "reasoning" && len(item.Summary) > 0 {
			reasoningText += item.Summary[0].Text
		}
	}
	return s, reasoningText
}

// PrependThinkingToMessages dispatches provider-specific thinking restoration
// before a tool_use message.
func (r *Registry) PrependThinkingToMessages(model string, msgs []anthropic.Message, toolCallID string, pendingSummary []openai.ReasoningItemSummary, sessionData map[string]any) []anthropic.Message {
	if r == nil {
		return msgs
	}
	for _, p := range r.plugins {
		if !p.EnabledForModel(model) {
			continue
		}
		if tp, ok := p.(ThinkingPrepender); ok {
			msgs = tp.PrependThinkingForToolUse(msgs, toolCallID, pendingSummary, sessionData[p.Name()])
		}
	}
	return msgs
}

// PrependThinkingToAssistant dispatches provider-specific thinking restoration
// before an assistant text message.
func (r *Registry) PrependThinkingToAssistant(model string, blocks []anthropic.ContentBlock, pendingSummary []openai.ReasoningItemSummary, sessionData map[string]any) []anthropic.ContentBlock {
	if r == nil {
		return blocks
	}
	for _, p := range r.plugins {
		if !p.EnabledForModel(model) {
			continue
		}
		if tp, ok := p.(ThinkingPrepender); ok {
			blocks = tp.PrependThinkingForAssistant(blocks, pendingSummary, sessionData[p.Name()])
		}
	}
	return blocks
}

// ExtractReasoningFromSummary extracts reasoning text from summary items.
func (r *Registry) ExtractReasoningFromSummary(model string, summary []openai.ReasoningItemSummary) string {
	block, ok := r.ExtractThinkingBlockFromSummary(model, summary)
	if !ok {
		return ""
	}
	return block.Thinking
}

// ExtractThinkingBlockFromSummary reconstructs a thinking block from reasoning
// summary items.
func (r *Registry) ExtractThinkingBlockFromSummary(model string, summary []openai.ReasoningItemSummary) (anthropic.ContentBlock, bool) {
	if r == nil || len(summary) == 0 {
		return anthropic.ContentBlock{}, false
	}
	for _, p := range r.plugins {
		if !p.EnabledForModel(model) {
			continue
		}
		if extractor, ok := p.(ReasoningExtractor); ok {
			if block, ok := extractor.ExtractThinkingBlock(&RequestContext{ModelAlias: model}, summary); ok {
				return block, true
			}
		}
		for _, item := range summary {
			if item.Text != "" {
				return anthropic.ContentBlock{Type: "thinking", Thinking: item.Text}, true
			}
		}
		return anthropic.ContentBlock{}, false
	}
	return anthropic.ContentBlock{}, false
}

// OnStreamBlockStart is a compat wrapper.
func (r *Registry) OnStreamBlockStart(model string, index int, block *anthropic.ContentBlock, streamStates map[string]any) bool {
	if r == nil {
		return false
	}
	consumed, _ := r.OnStreamEvent(model, StreamEvent{Type: "block_start", Index: index, Block: block}, streamStates)
	return consumed
}

// OnStreamBlockDelta is a compat wrapper.
func (r *Registry) OnStreamBlockDelta(model string, index int, delta anthropic.StreamDelta, streamStates map[string]any) bool {
	if r == nil {
		return false
	}
	consumed, _ := r.OnStreamEvent(model, StreamEvent{Type: "block_delta", Index: index, Delta: delta}, streamStates)
	return consumed
}

// OnStreamBlockStop is a compat wrapper.
func (r *Registry) OnStreamBlockStop(model string, index int, streamStates map[string]any) (bool, string) {
	if r == nil {
		return false, ""
	}
	consumed, _ := r.OnStreamEvent(model, StreamEvent{Type: "block_stop", Index: index}, streamStates)
	if consumed {
		// Extract the completed thinking text from the stream state.
		for _, p := range r.streamInterceptors {
			pp := p.(Plugin)
			if !pp.EnabledForModel(model) {
				continue
			}
			// Check if the stream state has a CompletedThinkingText method.
			if ss, ok := streamStates[pp.Name()]; ok {
				if thinker, ok := ss.(interface{ CompletedThinkingText() string }); ok {
					return true, thinker.CompletedThinkingText()
				}
			}
		}
		return true, ""
	}
	return false, ""
}

// OnStreamToolCall is a compat wrapper.
func (r *Registry) OnStreamToolCall(model string, toolCallID string, streamStates map[string]any) {
	if r == nil {
		return
	}
	for _, p := range r.streamInterceptors {
		pp := p.(Plugin)
		if !pp.EnabledForModel(model) {
			continue
		}
		if ss, ok := streamStates[pp.Name()]; ok {
			if recorder, ok := ss.(interface{ RecordToolCall(string) }); ok {
				recorder.RecordToolCall(toolCallID)
			}
		}
	}
}

// ResetStreamBlock is a compat wrapper.
func (r *Registry) ResetStreamBlock(model string, index int, streamStates map[string]any) {
	if r == nil {
		return
	}
	for _, p := range r.streamInterceptors {
		pp := p.(Plugin)
		if !pp.EnabledForModel(model) {
			continue
		}
		if ss, ok := streamStates[pp.Name()]; ok {
			if resetter, ok := ss.(interface{ Reset(int) }); ok {
				resetter.Reset(index)
			}
		}
	}
}

// PreprocessInput compat — already defined in registry.go, this is a no-op declaration check.
// (The method is already on Registry in registry.go.)

// NewStreamStates compat — already defined in registry.go.

// OnStreamComplete compat — already defined in registry.go.

// TransformError compat — already defined in registry.go.

// NewSessionData compat — already defined in registry.go.

// HasEnabled compat — already defined in registry.go.
