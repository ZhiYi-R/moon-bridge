// Package chat implements the OpenAI Chat Completions client adapter for MoonBridge.
//
// ChatClientAdapter implements format.ClientAdapter and format.ClientStreamAdapter,
// enabling MoonBridge to accept inbound OpenAI Chat Completions requests at
// /v1/chat/completions and convert them through the Core intermediate format
// to upstream providers.
package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"moonbridge/internal/format"
)

// ============================================================================
// ChatClientAdapter
// ============================================================================

// ChatClientAdapter converts inbound OpenAI Chat Completions requests to/from
// the Core intermediate format.
//
// It implements the inbound (client) side of the bridge:
//   - ClientAdapter:       DecodeRequest / ToCoreRequest / FromCoreResponse
//   - ClientStreamAdapter: FromCoreStream
type ChatClientAdapter struct {
	hooks format.CorePluginHooks
}

// NewChatClientAdapter creates a new ChatClientAdapter.
func NewChatClientAdapter(hooks format.CorePluginHooks) *ChatClientAdapter {
	return &ChatClientAdapter{
		hooks: hooks.WithDefaults(),
	}
}

// ClientProtocol returns the inbound protocol identifier.
func (a *ChatClientAdapter) ClientProtocol() string {
	return "openai-chat"
}

// ============================================================================
// DecodeRequest — raw HTTP body → *ChatRequest + stream flag
// ============================================================================

// DecodeRequest unmarshals the raw HTTP body into a ChatRequest and reports
// whether the request asks for streaming.
func (a *ChatClientAdapter) DecodeRequest(raw []byte, _, _ string) (any, bool, error) {
	if len(raw) == 0 {
		return nil, false, fmt.Errorf("empty request body")
	}
	var req ChatRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, false, fmt.Errorf("decode chat request: %w", err)
	}
	return &req, req.Stream, nil
}

// ============================================================================
// ToCoreRequest — *ChatRequest → *CoreRequest
// ============================================================================

// ToCoreRequest converts an inbound Chat Completions request into a CoreRequest.
func (a *ChatClientAdapter) ToCoreRequest(ctx context.Context, req any) (*format.CoreRequest, error) {
	chatReq, ok := req.(*ChatRequest)
	if !ok {
		direct, ok2 := req.(ChatRequest)
		if !ok2 {
			return nil, fmt.Errorf("unexpected request type %T; expected *ChatRequest", req)
		}
		chatReq = &direct
	}

	coreReq := &format.CoreRequest{
		Model:     chatReq.Model,
		MaxTokens: chatReq.MaxTokens,
		Messages:  make([]format.CoreMessage, 0, len(chatReq.Messages)),
		Stream:    chatReq.Stream,
		Metadata:  chatReq.Metadata,
	}

	// Map sampling parameters.
	if chatReq.Temperature != nil {
		coreReq.Temperature = chatReq.Temperature
	}
	if chatReq.TopP != nil {
		coreReq.TopP = chatReq.TopP
	}
	if len(chatReq.Stop) > 0 {
		coreReq.StopSequences = chatReq.Stop
	}

	// Reasoning effort.
	if chatReq.ReasoningEffort != "" {
		if coreReq.Extensions == nil {
			coreReq.Extensions = make(map[string]any)
		}
		coreReq.Extensions["reasoning_effort"] = chatReq.ReasoningEffort
	}

	// Convert messages.
	for _, msg := range chatReq.Messages {
		coreMsg := format.CoreMessage{
			Role:    msg.Role,
			Name:    msg.Name,
			Content: a.toCoreContent(msg.Content, msg.ToolCalls),
		}
		// Tool_use blocks from tool_calls in assistant messages.
		if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
			for _, tc := range msg.ToolCalls {
				coreMsg.Content = append(coreMsg.Content, format.CoreContentBlock{
					Type:      "tool_use",
					ToolUseID: tc.ID,
					ToolName:  tc.Function.Name,
					ToolInput: tc.Function.Arguments,
				})
			}
		}
		// Tool result from tool_call_id.
		if msg.Role == "tool" && msg.ToolCallID != "" {
			content := a.toCoreToolResultContent(msg.Content)
			coreMsg.Content = []format.CoreContentBlock{{
				Type:              "tool_result",
				ToolUseID:         msg.ToolCallID,
				ToolResultContent: content,
			}}
		}
		// Reasoning content passthrough.
		if msg.ReasoningContent != "" {
			coreMsg.Content = append(coreMsg.Content, format.CoreContentBlock{
				Type:          "reasoning",
				ReasoningText: msg.ReasoningContent,
			})
		}
		coreReq.Messages = append(coreReq.Messages, coreMsg)
	}

	// Convert tools.
	if len(chatReq.Tools) > 0 {
		coreReq.Tools = make([]format.CoreTool, 0, len(chatReq.Tools))
		for _, t := range chatReq.Tools {
			if t.Type != "function" {
				continue
			}
			coreReq.Tools = append(coreReq.Tools, format.CoreTool{
			Name:        t.Function.Name,
				Description: t.Function.Description,
				InputSchema: t.Function.Parameters,
			})
		}
	}

	// Convert tool choice.
	if len(chatReq.ToolChoice) > 0 && string(chatReq.ToolChoice) != "null" {
		tc, err := a.toCoreToolChoice(chatReq.ToolChoice)
		if err != nil {
			return nil, fmt.Errorf("tool_choice: %w", err)
		}
		coreReq.ToolChoice = tc
	}

	// Inject tools via hooks.
	if injected := a.hooks.InjectTools(format.ContextWithCoreRequest(ctx, coreReq)); len(injected) > 0 {
		coreReq.Tools = append(coreReq.Tools, injected...)
	}
	a.hooks.MutateCoreRequest(ctx, coreReq)

	return coreReq, nil
}

// toCoreContent converts Chat message content (string or []ContentPart) to Core blocks.
func (a *ChatClientAdapter) toCoreContent(content any, toolCalls []ToolCall) []format.CoreContentBlock {
	if content == nil {
		// With tool_calls, content may be empty/null.
		if len(toolCalls) > 0 {
			return nil
		}
		return nil
	}

	switch v := content.(type) {
	case string:
		if v == "" {
			return nil
		}
		return []format.CoreContentBlock{{Type: "text", Text: v}}
	case []ContentPart:
		blocks := make([]format.CoreContentBlock, 0, len(v))
		for _, part := range v {
			switch part.Type {
			case "text":
				if part.Text != "" {
					blocks = append(blocks, format.CoreContentBlock{Type: "text", Text: part.Text})
				}
			case "image_url":
				if part.ImageURL != nil && part.ImageURL.URL != "" {
					url := part.ImageURL.URL
					blocks = append(blocks, format.CoreContentBlock{
						Type:      "image",
						ImageData: url,
						MediaType: "image/png",
						ImageURL:  url,
					})
				}
			}
		}
		return blocks
	}
	return nil
}

// toCoreToolResultContent converts Chat tool result content to Core blocks.
func (a *ChatClientAdapter) toCoreToolResultContent(content any) []format.CoreContentBlock {
	if content == nil {
		return nil
	}
	switch v := content.(type) {
	case string:
		if v == "" {
			return nil
		}
		return []format.CoreContentBlock{{Type: "text", Text: v}}
	}
	return nil
}

// toCoreToolChoice parses a Chat tool_choice JSON value into a CoreToolChoice.
func (a *ChatClientAdapter) toCoreToolChoice(raw json.RawMessage) (*format.CoreToolChoice, error) {
	tc := &format.CoreToolChoice{Raw: raw}

	var value string
	if err := json.Unmarshal(raw, &value); err == nil {
		switch value {
		case "auto", "none":
			tc.Mode = value
			return tc, nil
		case "required":
			tc.Mode = "required"
			return tc, nil
		default:
			return nil, fmt.Errorf("unsupported tool_choice value: %q", value)
		}
	}

	var obj struct {
		Type     string `json:"type"`
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return tc, nil // preserve raw on parse failure
	}
	tc.Mode = obj.Type
	tc.Name = obj.Function.Name
	return tc, nil
}

// ============================================================================
// FromCoreResponse — *CoreResponse → *ChatResponse
// ============================================================================

// FromCoreResponse converts a CoreResponse back into a ChatResponse.
func (a *ChatClientAdapter) FromCoreResponse(ctx context.Context, resp *format.CoreResponse) (any, error) {
	if resp == nil {
		return nil, fmt.Errorf("core response is nil")
	}

	a.hooks.PostProcessCoreResponse(ctx, resp)

	chatResp := &ChatResponse{
		ID:      resp.ID,
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   resp.Model,
	}

	for i, msg := range resp.Messages {
		if msg.Role != "assistant" {
			continue
		}
		chatMsg := ChatMessage{
			Role:    "assistant",
			Content: a.fromCoreContentToChat(msg.Content),
		}
		// Extract tool calls from tool_use blocks.
		for _, block := range msg.Content {
			if block.Type == "tool_use" {
				chatMsg.ToolCalls = append(chatMsg.ToolCalls, ToolCall{
					ID:   block.ToolUseID,
					Type: "function",
					Function: ToolCallFunc{
					Name:      block.ToolName,
						Arguments: block.ToolInput,
					},
				})
			}
		}
		chatResp.Choices = append(chatResp.Choices, Choice{
			Index:        i,
			Message:      chatMsg,
			FinishReason: mapCoreStatusToFinishReason(resp.Status),
		})
	}

	if resp.Usage.InputTokens > 0 || resp.Usage.OutputTokens > 0 {
		chatResp.Usage = &Usage{
			PromptTokens:     resp.Usage.InputTokens,
			CompletionTokens: resp.Usage.OutputTokens,
			TotalTokens:      resp.Usage.InputTokens + resp.Usage.OutputTokens,
		}
		if resp.Usage.CachedInputTokens > 0 {
			chatResp.Usage.PromptTokensDetails = &PromptTokensDetails{
				CachedTokens: resp.Usage.CachedInputTokens,
			}
		}
	}

	return chatResp, nil
}

// fromCoreContentToChat converts Core content blocks to a Chat message content value
// (string for simple text, []ContentPart for multi-modal).
func (a *ChatClientAdapter) fromCoreContentToChat(blocks []format.CoreContentBlock) any {
	var textParts []string
	var hasImage bool
	for _, b := range blocks {
		switch b.Type {
		case "text":
			textParts = append(textParts, b.Text)
		case "image":
			hasImage = true
		}
	}
	if hasImage {
		parts := make([]ContentPart, 0, len(blocks))
		for _, b := range blocks {
			switch b.Type {
			case "text":
				parts = append(parts, ContentPart{Type: "text", Text: b.Text})
			case "image":
				parts = append(parts, ContentPart{Type: "image_url", ImageURL: &ImageURL{URL: b.ImageData}})
			}
		}
		return parts
	}
	return stringsJoin(textParts, "")
}

// ============================================================================
// FromCoreStream — CoreStreamEvent channel → Chat SSE stream
// ============================================================================

// FromCoreStream consumes a channel of CoreStreamEvent and produces a Chat
// stream result that implements SSEStream.
//
// The result emits Chat Completions delta chunks followed by the terminal
// data: [DONE] marker via SSEFrame.Done.
func (a *ChatClientAdapter) FromCoreStream(ctx context.Context, req *format.CoreRequest, events <-chan format.CoreStreamEvent) (any, error) {
	out := make(chan ChatStreamChunk)
	done := make(chan struct{})
	bufReady := make(chan struct{})

	var buf []ChatStreamChunk
	var bufMu sync.Mutex

	go func() {
		defer close(out)
		defer close(bufReady)
		defer close(done)
		a.streamLoop(ctx, req, events, out, &buf, &bufMu)
	}()

	return &ChatStreamResult{
		ch:   out,
		done: done,
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
func (a *ChatClientAdapter) streamLoop(
	ctx context.Context,
	coreReq *format.CoreRequest,
	events <-chan format.CoreStreamEvent,
	out chan<- ChatStreamChunk,
	buf *[]ChatStreamChunk,
	bufMu *sync.Mutex,
) {
	send := func(chunk ChatStreamChunk) {
		bufMu.Lock()
		if len(*buf) < 1024 {
			*buf = append(*buf, chunk)
		}
		bufMu.Unlock()
		out <- chunk
	}

	// State tracked during streaming.
	var chunkID string
	var model string
	var created int64
	textAccum := make(map[int]string)
	toolCallAccum := make(map[int]string)
	toolCallNames := make(map[int]string)
	toolCallSlot := make(map[int]int) // content block index → tool call position
	contentTypes := make(map[int]string)
	var finalUsage *Usage
	var seenFirst bool

	if created == 0 {
		created = time.Now().Unix()
	}

	for event := range events {
		if a.hooks.OnStreamEvent(ctx, event) {
			continue
		}

		switch event.Type {
		case format.CoreEventCreated:
			chunkID = event.ItemID
			model = event.Model
			if chunkID == "" {
				chunkID = fmt.Sprintf("chat_%d", created)
			}
			if model == "" {
				model = coreReq.Model
			}

		case format.CoreContentBlockStarted:
			if event.ContentBlock == nil {
				continue
			}
			index := event.Index
			blockType := event.ContentBlock.Type
			contentTypes[index] = blockType

			switch blockType {
			case "text":
				textAccum[index] = ""
				if !seenFirst {
					seenFirst = true
					send(ChatStreamChunk{
						ID:      chunkID,
						Object:  "chat.completion.chunk",
						Created: created,
						Model:   model,
						Choices: []StreamChoice{{
							Index: 0,
							Delta: Delta{Role: "assistant", Content: ""},
						}},
					})
				}

			case "reasoning":
				if !seenFirst {
					seenFirst = true
					send(ChatStreamChunk{
						ID:      chunkID,
						Object:  "chat.completion.chunk",
						Created: created,
						Model:   model,
						Choices: []StreamChoice{{
							Index: 0,
							Delta: Delta{Role: "assistant"},
						}},
					})
				}

			case "tool_use":
				tcIndex := len(toolCallSlot)
				toolCallSlot[index] = tcIndex
				toolCallAccum[index] = ""
				toolCallNames[index] = event.ContentBlock.ToolName
				send(ChatStreamChunk{
					ID:      chunkID,
					Object:  "chat.completion.chunk",
					Created: created,
					Model:   model,
					Choices: []StreamChoice{{
						Index: 0,
						Delta: Delta{ToolCalls: []ToolCall{{
							Index:    &tcIndex,
							ID:       event.ContentBlock.ToolUseID,
							Type:     "function",
							Function: ToolCallFunc{Name: event.ContentBlock.ToolName},
						}}},
					}},
				})
			}

		case format.CoreTextDelta:
			index := event.Index
			textAccum[index] += event.Delta
			blockType := contentTypes[index]

			if blockType == "reasoning" {
				send(ChatStreamChunk{
					ID:      chunkID,
					Object:  "chat.completion.chunk",
					Created: created,
					Model:   model,
					Choices: []StreamChoice{{
						Index: 0,
						Delta: Delta{ReasoningContent: event.Delta},
					}},
				})
			} else {
				send(ChatStreamChunk{
					ID:      chunkID,
					Object:  "chat.completion.chunk",
					Created: created,
					Model:   model,
					Choices: []StreamChoice{{
						Index: 0,
						Delta: Delta{Content: event.Delta},
					}},
				})
			}

		case format.CoreToolCallArgsDelta:
			index := event.Index
			toolCallAccum[index] += event.Delta
			if tcIdx, ok := toolCallSlot[index]; ok {
				send(ChatStreamChunk{
					ID:      chunkID,
					Object:  "chat.completion.chunk",
					Created: created,
					Model:   model,
					Choices: []StreamChoice{{
						Index: 0,
						Delta: Delta{ToolCalls: []ToolCall{{
							Index:    &tcIdx,
							ID:       "",
							Type:     "function",
							Function: ToolCallFunc{Arguments: json.RawMessage(event.Delta)},
						}}},
					}},
				})
			}

		case format.CoreToolCallArgsDone, format.CoreTextDone, format.CoreContentBlockDone:
			// Track state; completion sent via CoreEventCompleted.

		case format.CoreEventCompleted:
			finishReason := "stop"
			if event.StopReason != "" {
				finishReason = event.StopReason
			}
			if event.Usage != nil {
				finalUsage = &Usage{
					PromptTokens:     event.Usage.InputTokens,
					CompletionTokens: event.Usage.OutputTokens,
					TotalTokens:      event.Usage.InputTokens + event.Usage.OutputTokens,
				}
				if event.Usage.CachedInputTokens > 0 {
					finalUsage.PromptTokensDetails = &PromptTokensDetails{
						CachedTokens: event.Usage.CachedInputTokens,
					}
				}
			}
			send(ChatStreamChunk{
				ID:      chunkID,
				Object:  "chat.completion.chunk",
				Created: created,
				Model:   model,
				Choices: []StreamChoice{{
					Index:        0,
					Delta:        Delta{},
					FinishReason: finishReason,
				}},
				Usage: finalUsage,
			})

		case format.CoreEventIncomplete:
			send(ChatStreamChunk{
				ID:      chunkID,
				Object:  "chat.completion.chunk",
				Created: created,
				Model:   model,
				Choices: []StreamChoice{{
					Index:        0,
					Delta:        Delta{},
					FinishReason: "length",
				}},
				Usage: finalUsage,
			})

		case format.CoreEventFailed:
			// Send error chunk.
			send(ChatStreamChunk{
				ID:      chunkID,
				Object:  "chat.completion.chunk",
				Created: created,
				Model:   model,
				Choices: []StreamChoice{{
					Index:        0,
					Delta:        Delta{},
					FinishReason: "content_filter",
				}},
			})

		case format.CorePing:
			// No Chat equivalent.
		}
	}

	// Notify stream completion hook.
	outputText := ""
	for _, t := range textAccum {
		outputText += t
	}
	a.hooks.OnStreamComplete(ctx, coreReq.Model, outputText)
}

// ============================================================================
// ChatStreamResult
// ============================================================================

// ChatStreamResult wraps the Chat stream channel with per-stream buffer
// access and SSEFrame conversion.
type ChatStreamResult struct {
	ch   <-chan ChatStreamChunk
	done chan struct{}
	buf  func() []any
}

// Chan returns the underlying channel of ChatStreamChunk.
func (r *ChatStreamResult) Chan() <-chan ChatStreamChunk {
	return r.ch
}

// Frames returns a channel of protocol-agnostic SSE frames.
//
// OpenAI Chat Completions uses unnamed SSE (no event: line). Each chunk
// becomes an SSEFrame with Event="" and the chunk as Data. After the stream
// completes, a final SSEFrame with Done=true is emitted (data: [DONE]).
func (r *ChatStreamResult) Frames() <-chan format.SSEFrame {
	out := make(chan format.SSEFrame)
	go func() {
		defer close(out)
		for chunk := range r.ch {
			out <- format.SSEFrame{
				Data: chunk,
			}
		}
		// Terminal marker for OpenAI Chat style: data: [DONE].
		out <- format.SSEFrame{Done: true}
	}()
	return out
}

// Buffer returns the captured stream events for post-stream processing.
func (r *ChatStreamResult) Buffer() []any {
	if r.buf == nil {
		return nil
	}
	return r.buf()
}

// ============================================================================
// Helpers
// ============================================================================

// mapCoreStatusToFinishReason maps Core status to Chat finish_reason.
func mapCoreStatusToFinishReason(status string) string {
	switch status {
	case "completed":
		return "stop"
	case "incomplete":
		return "length"
	case "failed":
		return "content_filter"
	default:
		return "stop"
	}
}

// stringsJoin joins strings with an empty separator (avoids importing strings).
func stringsJoin(parts []string, sep string) string {
	if len(parts) == 0 {
		return ""
	}
	if len(parts) == 1 {
		return parts[0]
	}
	n := len(sep) * (len(parts) - 1)
	for _, p := range parts {
		n += len(p)
	}
	b := make([]byte, n)
	bp := copy(b, parts[0])
	for _, s := range parts[1:] {
		bp += copy(b[bp:], sep)
		bp += copy(b[bp:], s)
	}
	return string(b)
}

// Compile-time interface checks.
var _ format.ClientAdapter = (*ChatClientAdapter)(nil)
var _ format.ClientStreamAdapter = (*ChatClientAdapter)(nil)
var _ format.SSEStream = (*ChatStreamResult)(nil)
