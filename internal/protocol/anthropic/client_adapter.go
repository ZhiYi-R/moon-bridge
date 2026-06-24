// Package anthropic contains the Anthropic Messages API protocol adapter.
//
// AnthropicClientAdapter implements format.ClientAdapter and
// format.ClientStreamAdapter, enabling MoonBridge to accept inbound
// Anthropic-format requests at /v1/messages and convert them through
// the Core intermediate format to upstream providers.
//
// Clean room design: no imports from moonbridge/internal/extension/codextool/
// or any protocol-specific packages other than the Anthropic DTOs defined
// in this package.
package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"moonbridge/internal/format"
)

// ============================================================================
// AnthropicClientAdapter
// ============================================================================

// AnthropicClientAdapter converts inbound Anthropic Messages API requests
// to/from the Core intermediate format.
//
// It implements the inbound (client) side of the bridge:
//   - ClientAdapter:       DecodeRequest / ToCoreRequest / FromCoreResponse
//   - ClientStreamAdapter: FromCoreStream
type AnthropicClientAdapter struct {
	hooks format.CorePluginHooks
}

// NewAnthropicClientAdapter creates a new AnthropicClientAdapter.
func NewAnthropicClientAdapter(hooks format.CorePluginHooks) *AnthropicClientAdapter {
	return &AnthropicClientAdapter{
		hooks: hooks.WithDefaults(),
	}
}

// ClientProtocol returns the inbound protocol identifier.
func (a *AnthropicClientAdapter) ClientProtocol() string {
	return "anthropic"
}

// ============================================================================
// DecodeRequest — raw HTTP body → *MessageRequest + stream flag
// ============================================================================

// DecodeRequest unmarshals the raw HTTP body into an Anthropic MessageRequest
// and reports whether the request asks for streaming.
//
// Anthropic carries the model and stream flag in the JSON body, so pathModel
// and endpoint are unused — they exist for protocols like Gemini that encode
// these in the URL path.
func (a *AnthropicClientAdapter) DecodeRequest(raw []byte, _, _ string) (any, bool, error) {
	if len(raw) == 0 {
		return nil, false, fmt.Errorf("empty request body")
	}
	var req MessageRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, false, fmt.Errorf("decode anthropic message request: %w", err)
	}
	return &req, req.Stream, nil
}

// ============================================================================
// ToCoreRequest — *MessageRequest → *CoreRequest
// ============================================================================

// ToCoreRequest converts an inbound Anthropic Messages request into a CoreRequest.
//
// Supported mappings:
//   - Model, MaxTokens, Temperature, TopP, TopK, StopSequences, Stream, Metadata → direct copy
//   - System → CoreRequest.System content blocks
//   - Messages → CoreRequest.Messages
//   - Tools → CoreRequest.Tools
//   - ToolChoice → CoreRequest.ToolChoice
//   - Thinking → CoreRequest.Thinking
func (a *AnthropicClientAdapter) ToCoreRequest(ctx context.Context, req any) (*format.CoreRequest, error) {
	anthReq, ok := req.(*MessageRequest)
	if !ok {
		direct, ok2 := req.(MessageRequest)
		if !ok2 {
			return nil, fmt.Errorf("unexpected request type %T; expected *MessageRequest", req)
		}
		anthReq = &direct
	}

	coreReq := &format.CoreRequest{
		Model:         anthReq.Model,
		MaxTokens:     anthReq.MaxTokens,
		Temperature:   anthReq.Temperature,
		TopP:          anthReq.TopP,
		TopK:          anthReq.TopK,
		StopSequences: anthReq.StopSequences,
		Stream:        anthReq.Stream,
		Metadata:      anthReq.Metadata,
		System:        fromContentBlocks(anthReq.System),
		Messages:      make([]format.CoreMessage, 0, len(anthReq.Messages)),
		Thinking:      toCoreThinkingConfig(anthReq.Thinking),
	}

	// Convert messages.
	for _, msg := range anthReq.Messages {
		role := msg.Role
		if role == "tool" {
			role = "user" // Anthropic tool_result → Core tool-role
		}
		coreReq.Messages = append(coreReq.Messages, format.CoreMessage{
			Role:    role,
			Content: fromContentBlocks(msg.Content),
		})
	}

	// Convert tools.
	if len(anthReq.Tools) > 0 {
		coreReq.Tools = make([]format.CoreTool, 0, len(anthReq.Tools))
		for _, t := range anthReq.Tools {
			coreReq.Tools = append(coreReq.Tools, format.CoreTool{
				Name:        t.Name,
				Description: t.Description,
				InputSchema: t.InputSchema,
			})
		}
	}

	// Convert tool choice.
	if anthReq.ToolChoice != nil {
		coreReq.ToolChoice = &format.CoreToolChoice{
			Mode: anthReq.ToolChoice.Type,
			Name: anthReq.ToolChoice.Name,
		}
	}

	// Apply hooks.
	if injected := a.hooks.InjectTools(format.ContextWithCoreRequest(ctx, coreReq)); len(injected) > 0 {
		coreReq.Tools = append(coreReq.Tools, injected...)
	}
	a.hooks.MutateCoreRequest(ctx, coreReq)

	return coreReq, nil
}

// ============================================================================
// FromCoreResponse — *CoreResponse → *MessageResponse
// ============================================================================

// FromCoreResponse converts a CoreResponse back into an Anthropic MessageResponse.
func (a *AnthropicClientAdapter) FromCoreResponse(ctx context.Context, resp *format.CoreResponse) (any, error) {
	if resp == nil {
		return nil, fmt.Errorf("core response is nil")
	}

	a.hooks.PostProcessCoreResponse(ctx, resp)

	response := &MessageResponse{
		ID:    resp.ID,
		Type:  "message",
		Role:  "assistant",
		Model: resp.Model,
	}

	// Convert assistant messages to content blocks.
	for _, msg := range resp.Messages {
		if msg.Role != "assistant" {
			continue
		}
		for _, block := range msg.Content {
			response.Content = append(response.Content, toContentBlock(block))
		}
	}

	// Map status → stop_reason.
	response.StopReason = mapCoreStatusToStopReason(resp.Status, resp.StopReason)

	// Map usage.
	response.Usage = Usage{
		InputTokens:              resp.Usage.InputTokens,
		OutputTokens:             resp.Usage.OutputTokens,
		CacheReadInputTokens:     resp.Usage.CachedInputTokens,
		CacheCreationInputTokens: 0,
	}

	// Map error.
	if resp.Error != nil {
		// Anthropic doesn't embed errors in MessageResponse; signal via status.
		if resp.Status == "" || resp.Status == "completed" {
			response.StopReason = "error"
		}
	}

	return response, nil
}

// ============================================================================
// FromCoreStream — CoreStreamEvent channel → Anthropic SSE stream
// ============================================================================

// FromCoreStream consumes a channel of CoreStreamEvent and produces an
// Anthropic stream result that implements SSEStream.
//
// The returned channel is closed when the input channel is exhausted.
func (a *AnthropicClientAdapter) FromCoreStream(ctx context.Context, req *format.CoreRequest, events <-chan format.CoreStreamEvent) (any, error) {
	out := make(chan StreamEvent)
	bufReady := make(chan struct{})

	var buf []StreamEvent
	var bufMu sync.Mutex

	go func() {
		defer close(out)
		defer close(bufReady)
		a.streamLoop(ctx, req, events, out, &buf, &bufMu)
	}()

	return &AnthropicStreamResult{
		ch: out,
		buf: func() []any {
			<-bufReady
			bufMu.Lock()
			defer bufMu.Unlock()
			result := make([]any, len(buf))
			for i, ev := range buf {
				result[i] = ev
			}
			return result
		},
	}, nil
}

// streamLoop is the goroutine body for FromCoreStream.
func (a *AnthropicClientAdapter) streamLoop(
	ctx context.Context,
	coreReq *format.CoreRequest,
	events <-chan format.CoreStreamEvent,
	out chan<- StreamEvent,
	buf *[]StreamEvent,
	bufMu *sync.Mutex,
) {
	send := func(ev StreamEvent) {
		bufMu.Lock()
		if len(*buf) < 1024 {
			*buf = append(*buf, ev)
		}
		bufMu.Unlock()
		out <- ev
	}

	// State tracked during streaming.
	var msgID string
	var model string
	contentText := make(map[int]string)
	contentTypes := make(map[int]string)   // content index → block type ("text", "thinking", "tool_use")
	toolUseIDs := make(map[int]string)     // content index → tool_use id
	toolUseNames := make(map[int]string)   // content index → tool_use name
	reasoningSignatures := make(map[int]string)
		blockIndices := make([]int, 0) // ordered list of content block indices
	// Track accumulated state for message_start and message_delta.
	var accumulatedUsage *Usage

	for event := range events {
		// Let hooks skip events.
		if a.hooks.OnStreamEvent(ctx, event) {
			continue
		}

		switch event.Type {
		// ==================================================================
		// Lifecycle: created → message_start
		// ==================================================================
		case format.CoreEventCreated:
			msgID = event.ItemID
			model = event.Model
			if msgID == "" {
				msgID = fmt.Sprintf("msg_%d", len(blockIndices))
			}
			if model == "" {
				model = coreReq.Model
			}
			send(StreamEvent{
				Type: "message_start",
				Message: &MessageResponse{
					ID:    msgID,
					Type:  "message",
					Role:  "assistant",
					Model: model,
				},
			})

		// ==================================================================
		// Lifecycle: in_progress → skip (Anthropic doesn't have this event)
		// ==================================================================
		case format.CoreEventInProgress:
			// No Anthropic equivalent.

		// ==================================================================
		// Content block started → content_block_start
		// ==================================================================
		case format.CoreContentBlockStarted:
			if event.ContentBlock == nil {
				continue
			}
			index := event.Index
			blockIndices = append(blockIndices, index)
			blockType := event.ContentBlock.Type

			switch blockType {
			case "text":
				contentTypes[index] = "text"
				contentText[index] = ""
				send(StreamEvent{
					Type:  "content_block_start",
					Index: index,
					ContentBlock: &ContentBlock{
						Type: "text",
						Text: "",
					},
				})

			case "reasoning":
				contentTypes[index] = "thinking"
				contentText[index] = ""
				reasoningSignatures[index] = event.ContentBlock.ReasoningSignature
				send(StreamEvent{
					Type:  "content_block_start",
					Index: index,
					ContentBlock: &ContentBlock{
						Type:      "thinking",
						Thinking:  "",
						Signature: event.ContentBlock.ReasoningSignature,
					},
				})

			case "tool_use":
				contentTypes[index] = "tool_use"
				toolUseIDs[index] = event.ContentBlock.ToolUseID
				toolUseNames[index] = event.ContentBlock.ToolName
				send(StreamEvent{
					Type:  "content_block_start",
					Index: index,
					ContentBlock: &ContentBlock{
						Type: "tool_use",
						ID:   event.ContentBlock.ToolUseID,
						Name: event.ContentBlock.ToolName,
					},
				})
			}

		// ==================================================================
		// Text delta → content_block_delta (text_delta / thinking_delta)
		// ==================================================================
		case format.CoreTextDelta:
			index := event.Index
			contentText[index] += event.Delta
			blockType := contentTypes[index]

			switch blockType {
			case "text":
				send(StreamEvent{
					Type:  "content_block_delta",
					Index: index,
					Delta: StreamDelta{
						Type: "text_delta",
						Text: event.Delta,
					},
				})

			case "thinking":
				send(StreamEvent{
					Type:  "content_block_delta",
					Index: index,
					Delta: StreamDelta{
						Type:     "thinking_delta",
						Thinking: event.Delta,
					},
				})
			}

		// ==================================================================
		// Text done → content_block_stop (if no separate block done event)
		// ==================================================================
		case format.CoreTextDone:
			index := event.Index
			delete(contentText, index)

		// ==================================================================
		// Tool call args delta → content_block_delta (input_json_delta)
		// ==================================================================
		case format.CoreToolCallArgsDelta:
			index := event.Index
			if _, ok := contentTypes[index]; ok && contentTypes[index] == "tool_use" {
				send(StreamEvent{
					Type:  "content_block_delta",
					Index: index,
					Delta: StreamDelta{
						Type:        "input_json_delta",
						PartialJSON: event.Delta,
					},
				})
			}

		// ==================================================================
		// Tool call args done → track state (stop is handled by block done)
		// ==================================================================
		case format.CoreToolCallArgsDone:
			// No separate Anthropic event; content_block_stop follows.

		// ==================================================================
		// Content block done → content_block_stop
		// ==================================================================
		case format.CoreContentBlockDone:
			index := event.Index
			blockType := contentTypes[index]

			if blockType == "thinking" {
				send(StreamEvent{
					Type:  "content_block_stop",
					Index: index,
				})
			} else {
				send(StreamEvent{
					Type:  "content_block_stop",
					Index: index,
				})
			}
			delete(contentText, index)
			delete(contentTypes, index)

		// ==================================================================
		// Lifecycle: completed → message_delta + message_stop
		// ==================================================================
		case format.CoreEventCompleted:
			stopReason := "end_turn"
			if event.StopReason != "" {
				stopReason = event.StopReason
			}
			if event.Usage != nil {
				accumulatedUsage = &Usage{
					InputTokens:          event.Usage.InputTokens,
					OutputTokens:         event.Usage.OutputTokens,
					CacheReadInputTokens: event.Usage.CachedInputTokens,
				}
			}
			send(StreamEvent{
				Type: "message_delta",
				Delta: StreamDelta{
					Type:       "message_delta",
					StopReason: stopReason,
				},
				Usage: accumulatedUsage,
			})
			send(StreamEvent{
				Type: "message_stop",
			})

		// ==================================================================
		// Lifecycle: incomplete → message_delta (max_tokens) + message_stop
		// ==================================================================
		case format.CoreEventIncomplete:
			send(StreamEvent{
				Type: "message_delta",
				Delta: StreamDelta{
					Type:       "message_delta",
					StopReason: "max_tokens",
				},
				Usage: accumulatedUsage,
			})
			send(StreamEvent{
				Type: "message_stop",
			})

		// ==================================================================
		// Lifecycle: failed → error + message_stop
		// ==================================================================
		case format.CoreEventFailed:
			errMsg := "unknown error"
			errType := "api_error"
			if event.Error != nil {
				errMsg = event.Error.Message
				errType = event.Error.Type
			}
			send(StreamEvent{
				Type: "error",
				Error: &ErrorObject{
					Type:    errType,
					Message: errMsg,
				},
			})
			send(StreamEvent{
				Type: "message_stop",
			})

		// ==================================================================
		// Ping → ping
		// ==================================================================
		case format.CorePing:
			send(StreamEvent{
				Type: "ping",
			})

		// ==================================================================
		// Item events → no Anthropic equivalent; skip
		// ==================================================================
		case format.CoreItemAdded, format.CoreItemDone:
			// No Anthropic equivalent.
		}
	}

	// Notify stream completion hook.
	outputText := ""
	for _, text := range contentText {
		outputText += text
	}
	a.hooks.OnStreamComplete(ctx, coreReq.Model, outputText)
}

// ============================================================================
// AnthropicStreamResult
// ============================================================================

// AnthropicStreamResult wraps the Anthropic stream channel with per-stream
// buffer access and SSEFrame conversion.
type AnthropicStreamResult struct {
	ch  <-chan StreamEvent
	buf func() []any
}

// Chan returns the underlying channel of StreamEvents.
func (r *AnthropicStreamResult) Chan() <-chan StreamEvent {
	return r.ch
}

// Frames returns a channel of protocol-agnostic SSE frames.
// Each StreamEvent is mapped to an SSEFrame with the event name as the
// SSE event type and the event payload as data.
func (r *AnthropicStreamResult) Frames() <-chan format.SSEFrame {
	out := make(chan format.SSEFrame)
	go func() {
		defer close(out)
		for ev := range r.ch {
			out <- format.SSEFrame{
				Event: ev.Type,
				Data:  ev,
			}
		}
	}()
	return out
}

// Buffer returns the captured stream events for post-stream processing.
func (r *AnthropicStreamResult) Buffer() []any {
	if r.buf == nil {
		return nil
	}
	return r.buf()
}

// ============================================================================
// Content block conversion helpers
// ============================================================================

// fromContentBlocks converts anthropic ContentBlock slice to CoreContentBlock slice.
func fromContentBlocks(blocks []ContentBlock) []format.CoreContentBlock {
	result := make([]format.CoreContentBlock, 0, len(blocks))
	for _, b := range blocks {
		result = append(result, fromContentBlock(b))
	}
	return result
}

// fromContentBlock converts a single anthropic ContentBlock to CoreContentBlock.
func fromContentBlock(b ContentBlock) format.CoreContentBlock {
	switch b.Type {
	case "text":
		return format.CoreContentBlock{
			Type: "text",
			Text: b.Text,
		}

	case "image":
		cb := format.CoreContentBlock{
			Type: "image",
		}
		if b.Source != nil {
			cb.MediaType = b.Source.MediaType
			cb.ImageData = b.Source.Data
		}
		return cb

	case "tool_use":
		return format.CoreContentBlock{
			Type:      "tool_use",
			ToolUseID: b.ID,
			ToolName:  b.Name,
			ToolInput: b.Input,
		}

	case "tool_result":
		cb := format.CoreContentBlock{
			Type:      "tool_result",
			ToolUseID: b.ToolUseID,
		}
		if b.Content != nil {
			switch content := b.Content.(type) {
			case string:
				cb.ToolResultContent = []format.CoreContentBlock{
					{Type: "text", Text: content},
				}
			case []ContentBlock:
				cb.ToolResultContent = fromContentBlocks(content)
			}
		}
		return cb

	case "thinking":
		return format.CoreContentBlock{
			Type:               "reasoning",
			ReasoningText:      b.Thinking,
			ReasoningSignature: b.Signature,
		}

	default:
		if b.Text != "" {
			return format.CoreContentBlock{
				Type: "text",
				Text: b.Text,
			}
		}
		return format.CoreContentBlock{Type: "text"}
	}
}

// toContentBlock converts a CoreContentBlock to an anthropic ContentBlock.
func toContentBlock(b format.CoreContentBlock) ContentBlock {
	switch b.Type {
	case "text":
		return ContentBlock{
			Type: "text",
			Text: b.Text,
		}

	case "image":
		return ContentBlock{
			Type: "image",
			Source: &ImageSource{
				Type:      "base64",
				MediaType: b.MediaType,
				Data:      b.ImageData,
			},
		}

	case "tool_use":
		return ContentBlock{
			Type:  "tool_use",
			ID:    b.ToolUseID,
			Name:  b.ToolName,
			Input: b.ToolInput,
		}

	case "reasoning":
		return ContentBlock{
			Type:      "thinking",
			Thinking:  b.ReasoningText,
			Signature: b.ReasoningSignature,
		}

	default:
		return ContentBlock{
			Type: "text",
			Text: b.Text,
		}
	}
}

// toCoreThinkingConfig converts anthropic *ThinkingConfig to *format.CoreThinkingConfig.
func toCoreThinkingConfig(tc *ThinkingConfig) *format.CoreThinkingConfig {
	if tc == nil {
		return nil
	}
	return &format.CoreThinkingConfig{
		Type:         tc.Type,
		BudgetTokens: tc.BudgetTokens,
	}
}

// mapCoreStatusToStopReason converts a Core status string to an Anthropic stop_reason.
func mapCoreStatusToStopReason(status, stopReason string) string {
	if stopReason != "" {
		switch stopReason {
		case "end_turn", "stop_sequence", "max_tokens", "tool_use", "content_filtered":
			return stopReason
		}
	}
	switch status {
	case "completed":
		return "end_turn"
	case "incomplete":
		return "max_tokens"
	case "failed":
		return "content_filtered"
	default:
		return "end_turn"
	}
}

// Ensure compile-time interface compliance.
var _ format.ClientAdapter = (*AnthropicClientAdapter)(nil)
var _ format.ClientStreamAdapter = (*AnthropicClientAdapter)(nil)

// Ensure AnthropicStreamResult implements SSEStream.
var _ format.SSEStream = (*AnthropicStreamResult)(nil)
