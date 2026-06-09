// Package chat implements the OpenAI Chat Completions ProviderAdapter for MoonBridge.
//
// ChatProviderAdapter converts between Core format and OpenAI Chat Completions
// REST API DTOs. It implements format.ProviderAdapter (non-streaming) and
// format.ProviderStreamAdapter (streaming).
package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"moonbridge/internal/format"
)

// ============================================================================
// ChatProviderAdapter
// ============================================================================

// ChatProviderAdapter converts Core format requests/responses to/from
// the OpenAI Chat Completions API format.
//
// Clean room: no dependency on protocol-specific packages beyond chat/.
// Only references: config, format, and chat types.
type ChatProviderAdapter struct {
	cfgMaxTokens int
	client       *Client
	hooks        format.CorePluginHooks

	streamMu     sync.Mutex
	streamEvents []ChatStreamChunk
}

type chatStreamChoiceState struct {
	started          bool
	blockIndex       int          // monotonically increasing content block index
	textStarted      bool         // whether a text block is active
	hasReasoning     bool         // whether a reasoning block is active
	reasonIndex      int          // block index for the reasoning content block
	toolCallIdx      int          // next tool call content block index
	callStarted      map[int]bool // tracks which tool call indices have been started
	toolCallSlot     map[int]int  // tool_call delta index -> content block index
	reasoningContent string       // accumulated reasoning content for the current reasoning block
	thinkActive      bool         // whether a <think>...</think> text tag is open
	thinkBuffer      string       // buffered text for split tag detection
}

// NewChatProviderAdapter creates a new ChatProviderAdapter.
//
// client is the HTTP client for Chat API calls. May be nil if the adapter
// is registered for type conversion only (dispatch layer manages the client).
func NewChatProviderAdapter(cfgMaxTokens int, client *Client, hooks format.CorePluginHooks) *ChatProviderAdapter {
	return &ChatProviderAdapter{
		cfgMaxTokens: cfgMaxTokens,
		client:       client,
		hooks:        hooks.WithDefaults(),
	}
}

// ProviderProtocol returns "openai-chat".
func (a *ChatProviderAdapter) ProviderProtocol() string {
	return "openai-chat"
}

// =========================================================================
// FromCoreRequest — CoreRequest → *ChatRequest
// =========================================================================

// FromCoreRequest converts a CoreRequest into a *ChatRequest.
//
// Conversion steps:
//  1. Call hooks.MutateCoreRequest (plugin modifications to CoreRequest)
//  2. Map CoreRequest fields to ChatRequest fields
//  3. System instruction, messages, tools, sampling params
func (a *ChatProviderAdapter) FromCoreRequest(ctx context.Context, req *format.CoreRequest) (any, error) {
	if req == nil {
		return nil, fmt.Errorf("chat adapter: core request is nil")
	}

	// Step 1: Allow plugins to mutate the CoreRequest before conversion.
	a.hooks.RewriteMessages(ctx, req)
	a.hooks.MutateCoreRequest(ctx, req)

	// Strip base64 image data from all text content to prevent token waste.
	format.StripContentBlocks(req.System)
	for i := range req.Messages {
		format.StripContentBlocks(req.Messages[i].Content)
	}

	// Step 2: Build the Chat request.
	chatReq := &ChatRequest{
		Model:    req.Model,
		Messages: make([]ChatMessage, 0, len(req.Messages)+1),
	}

	// System instruction → first message with role "system".
	if len(req.System) > 0 {
		sysContent := a.toChatSystemContent(req.System)
		if sysContent != "" {
			chatReq.Messages = append(chatReq.Messages, ChatMessage{
				Role:    "system",
				Content: sysContent,
			})
		}
	}

	// Messages.
	for _, msg := range req.Messages {
		chatMsg := a.toChatMessage(msg)
		chatReq.Messages = append(chatReq.Messages, chatMsg)
	}

	// Sampling parameters.
	if req.Temperature != nil {
		chatReq.Temperature = req.Temperature
	}
	if req.TopP != nil {
		chatReq.TopP = req.TopP
	}
	if req.MaxTokens > 0 {
		chatReq.MaxTokens = req.MaxTokens
	} else if a.cfgMaxTokens > 0 {
		chatReq.MaxTokens = a.cfgMaxTokens
	}
	if len(req.StopSequences) > 0 {
		chatReq.Stop = req.StopSequences
	}

	// Tools.
	if len(req.Tools) > 0 {
		chatReq.Tools = make([]ChatTool, 0, len(req.Tools))
		for _, t := range req.Tools {
			chatReq.Tools = append(chatReq.Tools, ChatTool{
				Type: "function",
				Function: FunctionDef{
					Name:        t.Name,
					Description: t.Description,
					Parameters:  defaultToolParameters(t.InputSchema),
				},
			})
		}
	}

	// ToolChoice.
	if req.ToolChoice != nil {
		if req.ToolChoice.Raw != nil {
			chatReq.ToolChoice = req.ToolChoice.Raw
		} else {
			chatReq.ToolChoice = a.toChatToolChoice(*req.ToolChoice)
		}
	}

	return chatReq, nil
}

// =========================================================================
// ToCoreResponse — *ChatResponse → *CoreResponse
// =========================================================================

// ToCoreResponse converts a *ChatResponse into a *CoreResponse.
//
// Each choice in the response becomes a separate assistant message in
// Messages. Token usage is extracted from Usage.
func (a *ChatProviderAdapter) ToCoreResponse(ctx context.Context, resp any) (*format.CoreResponse, error) {
	chatResp, ok := resp.(*ChatResponse)
	if !ok {
		return nil, fmt.Errorf("chat adapter: expected *ChatResponse, got %T", resp)
	}

	messages := make([]format.CoreMessage, 0, len(chatResp.Choices))
	for _, choice := range chatResp.Choices {
		msg := a.choiceToCoreMessage(choice)
		messages = append(messages, msg)
	}

	// Determine overall status and stop reason from the first choice.
	status := "completed"
	var stopReason string
	if len(chatResp.Choices) > 0 {
		stopReason = a.mapFinishReason(chatResp.Choices[0].FinishReason)
		switch chatResp.Choices[0].FinishReason {
		case "length":
			status = "incomplete"
		case "content_filter":
			status = "failed"
		}
	}

	coreResp := &format.CoreResponse{
		ID:         chatResp.ID,
		Model:      chatResp.Model,
		Status:     status,
		Messages:   messages,
		StopReason: stopReason,
	}

	if chatResp.Usage != nil {
		coreResp.Usage = format.CoreUsage{
			InputTokens:  chatResp.Usage.PromptTokens,
			OutputTokens: chatResp.Usage.CompletionTokens,
			TotalTokens:  chatResp.Usage.TotalTokens,
		}
		if chatResp.Usage.PromptTokensDetails != nil {
			coreResp.Usage.CachedInputTokens = chatResp.Usage.PromptTokensDetails.CachedTokens
		}
	}

	return coreResp, nil
}

// =========================================================================
// bufferStreamEvent buffers raw ChatStreamChunk for trace capture.
func (a *ChatProviderAdapter) bufferStreamEvent(ev ChatStreamChunk) {
	a.streamMu.Lock()
	defer a.streamMu.Unlock()
	a.streamEvents = append(a.streamEvents, ev)
}

// StreamBuffer returns the buffered stream events for trace capture.
func (a *ChatProviderAdapter) StreamBuffer() []ChatStreamChunk {
	a.streamMu.Lock()
	defer a.streamMu.Unlock()
	return a.streamEvents
}

// ToCoreStream — <-chan ChatStreamChunk → <-chan CoreStreamEvent
// =========================================================================

// ToCoreStream consumes a channel of ChatStreamChunk (from streaming Chat
// Completions) and returns a channel of CoreStreamEvent.
//
// OpenAI Chat streaming uses delta-based SSE (unlike Gemini's full snapshots),
// so no delta computation is needed — each chunk's delta is directly mapped.
//
// Emitted event sequence per choice:
//   - core.content_block.started (first chunk with role="assistant")
//   - core.text.delta (chunks with content delta)
//   - core.content_block.done (chunk with finish_reason set)
//   - core.completed (final chunk with Usage)
func (a *ChatProviderAdapter) ToCoreStream(ctx context.Context, src any) (<-chan format.CoreStreamEvent, error) {
	ch, ok := src.(<-chan ChatStreamChunk)
	if !ok {
		return nil, fmt.Errorf("chat adapter: expected <-chan ChatStreamChunk, got %T", src)
	}

	events := make(chan format.CoreStreamEvent, 64)

	// Initialize stream event buffer for trace capture.
	a.streamMu.Lock()
	a.streamEvents = make([]ChatStreamChunk, 0, 64)
	a.streamMu.Unlock()

	go func() {
		defer close(events)

		// Per-choice state for streaming.
		choices := make(map[int]*chatStreamChoiceState)
		var seqNum int64
		var finalUsage *format.CoreUsage
		var lastModel string
		var seenCompletion bool

		emit := func(ev format.CoreStreamEvent) {
			seqNum++
			ev.SeqNum = seqNum
			select {
			case events <- ev:
			case <-ctx.Done():
			}
		}

		for {
			select {
			case <-ctx.Done():
				return
			case chunk, ok := <-ch:
				a.bufferStreamEvent(chunk)
				if !ok {
					// Channel closed — emit completion if not already done.
					if !seenCompletion {
						if finalUsage != nil {
							emit(format.CoreStreamEvent{
								Type:   format.CoreEventCompleted,
								Status: "completed",
								Model:  lastModel,
								Usage:  finalUsage,
							})
						} else {
							emit(format.CoreStreamEvent{
								Type:   format.CoreEventCompleted,
								Status: "completed",
								Model:  lastModel,
							})
						}
					}
					return
				}

				if chunk.Model != "" {
					lastModel = chunk.Model
				}

				// Process each choice in the chunk.
				for _, sc := range chunk.Choices {
					state := choices[sc.Index]
					if state == nil {
						state = &chatStreamChoiceState{blockIndex: sc.Index * 2}
						choices[sc.Index] = state
					}

					ci := sc.Index

					// Mark the assistant turn on first appearance with role. The
					// concrete block is started lazily when text/reasoning/tool
					// content arrives, which avoids emitting empty text items for
					// tool-only or reasoning-only turns.
					if !state.started && sc.Delta.Role == "assistant" {
						state.started = true
					}

					startText := func() {
						if state.textStarted {
							return
						}
						state.textStarted = true
						emit(format.CoreStreamEvent{
							Type:         format.CoreContentBlockStarted,
							Index:        state.blockIndex,
							ChoiceIndex:  &ci,
							ContentBlock: &format.CoreContentBlock{Type: "text"},
						})
					}
					closeText := func(stopReason string) {
						if !state.textStarted {
							return
						}
						emit(format.CoreStreamEvent{
							Type:        format.CoreContentBlockDone,
							Index:       state.blockIndex,
							StopReason:  stopReason,
							ChoiceIndex: &ci,
						})
						state.textStarted = false
						state.blockIndex++
					}
					startReasoning := func() {
						if state.hasReasoning {
							return
						}
						closeText("")
						state.hasReasoning = true
						state.reasonIndex = state.blockIndex
						emit(format.CoreStreamEvent{
							Type:         format.CoreContentBlockStarted,
							Index:        state.reasonIndex,
							ChoiceIndex:  &ci,
							ContentBlock: &format.CoreContentBlock{Type: "reasoning"},
						})
					}
					closeReasoning := func(stopReason string) {
						if !state.hasReasoning {
							return
						}
						emit(format.CoreStreamEvent{
							Type:        format.CoreContentBlockDone,
							Index:       state.reasonIndex,
							StopReason:  stopReason,
							ChoiceIndex: &ci,
							ContentBlock: &format.CoreContentBlock{
								Type:          "reasoning",
								ReasoningText: state.reasoningContent,
							},
						})
						state.reasoningContent = ""
						state.hasReasoning = false
						state.blockIndex = state.reasonIndex + 1
					}
					emitTextDelta := func(delta string) {
						if delta == "" {
							return
						}
						startText()
						emit(format.CoreStreamEvent{
							Type:        format.CoreTextDelta,
							Index:       state.blockIndex,
							Delta:       delta,
							ChoiceIndex: &ci,
						})
					}
					emitReasoningDelta := func(delta string) {
						if delta == "" {
							return
						}
						startReasoning()
						state.reasoningContent += delta
						emit(format.CoreStreamEvent{
							Type:        format.CoreTextDelta,
							Index:       state.reasonIndex,
							Delta:       delta,
							ChoiceIndex: &ci,
						})
					}

					// Emit provider-native reasoning_content as reasoning deltas.
					// Note: reasoning_content may appear AFTER the text block has started
					// (DeepSeek first sends role=assistant, then reasoning_content in subsequent chunks).
					if sc.Delta.ReasoningContent != "" {
						emitReasoningDelta(sc.Delta.ReasoningContent)
					}

					// Transition from reasoning block to text block.
					if sc.Delta.Content != "" && state.hasReasoning && !state.thinkActive && state.thinkBuffer == "" {
						closeReasoning("")
					}
					if sc.Delta.Content != "" {
						state.consumeThinkTaggedDelta(sc.Delta.Content, emitTextDelta, emitReasoningDelta, closeReasoning)
					}

					// Emit tool call content blocks and args deltas.
					for toolPos, tc := range sc.Delta.ToolCalls {
						callPos := toolPos
						if tc.Index != nil && *tc.Index >= 0 {
							callPos = *tc.Index
						}
						if state.callStarted == nil {
							state.callStarted = make(map[int]bool)
							// Start tool call indices after any active text/reasoning block.
							if state.textStarted || state.hasReasoning {
								state.toolCallIdx = state.blockIndex + 1
							} else {
								state.toolCallIdx = state.blockIndex
							}
						}
						if state.toolCallSlot == nil {
							state.toolCallSlot = make(map[int]int)
						}
						slot, hasSlot := state.toolCallSlot[callPos]
						// Emit content_block.started for first occurrence of each tool call slot.
						if !hasSlot {
							slot = state.toolCallIdx
							state.toolCallSlot[callPos] = slot
							state.toolCallIdx++
						}
						if !state.callStarted[slot] && (tc.ID != "" || tc.Function.Name != "") {
							state.callStarted[slot] = true
							emit(format.CoreStreamEvent{
								Type:        format.CoreContentBlockStarted,
								Index:       slot,
								ChoiceIndex: &ci,
								ContentBlock: &format.CoreContentBlock{
									Type:      "tool_use",
									ToolUseID: tc.ID,
									ToolName:  tc.Function.Name,
								},
							})
						}
						// Skip empty argument deltas (avoids accumulating "" at the start).
						// Decode each JSON string chunk to get the actual content (strips surrounding quotes).
						// Raw bytes include JSON string quotes (e.g. "" -> empty, "{" -> {, "\"" -> ").
						if !state.callStarted[slot] {
							continue
						}
						if len(tc.Function.Arguments) > 0 {
							var decoded string
							if err := json.Unmarshal(tc.Function.Arguments, &decoded); err == nil {
								if decoded != "" {
									emit(format.CoreStreamEvent{
										Type:        format.CoreToolCallArgsDelta,
										Index:       slot,
										Delta:       decoded,
										ChoiceIndex: &ci,
									})
								}
							} else {
								emit(format.CoreStreamEvent{
									Type:        format.CoreToolCallArgsDelta,
									Index:       slot,
									Delta:       string(tc.Function.Arguments),
									ChoiceIndex: &ci,
								})
							}
						}
					}

					// Emit content_block.done when finish_reason is set.
					if sc.FinishReason != "" {
						stopReason := a.mapFinishReason(sc.FinishReason)
						state.flushThinkTaggedContent(emitTextDelta, emitReasoningDelta, closeReasoning)
						if state.hasReasoning {
							closeReasoning(stopReason)
						} else if state.textStarted {
							closeText(stopReason)
						}
						// Complete tool call blocks.
						for idx := 0; idx < state.toolCallIdx; idx++ {
							if !state.callStarted[idx] {
								continue
							}
							emit(format.CoreStreamEvent{
								Type:        format.CoreToolCallArgsDone,
								Index:       idx,
								ChoiceIndex: &ci,
							})
							emit(format.CoreStreamEvent{
								Type:        format.CoreContentBlockDone,
								Index:       idx,
								ChoiceIndex: &ci,
							})
						}
					}
				}

				// Track usage from the last chunk.
				if chunk.Usage != nil {
					finalUsage = &format.CoreUsage{
						InputTokens:  chunk.Usage.PromptTokens,
						OutputTokens: chunk.Usage.CompletionTokens,
						TotalTokens:  chunk.Usage.TotalTokens,
					}
					if chunk.Usage.PromptTokensDetails != nil {
						finalUsage.CachedInputTokens = chunk.Usage.PromptTokensDetails.CachedTokens
					}
				}
			}
		}
	}()

	return events, nil
}

// =========================================================================
// Helpers: Core → Chat
// =========================================================================

// toChatSystemContent combines Core system content blocks into a single string.
func (a *ChatProviderAdapter) toChatSystemContent(blocks []format.CoreContentBlock) string {
	var text string
	for _, b := range blocks {
		switch b.Type {
		case "text", "input_text", "output_text":
			text += b.Text
		case "reasoning":
			continue // skip reasoning content in system
		default:
			if b.Text != "" {
				text += b.Text
			}
		}
	}
	return text
}

// toChatMessage converts a CoreMessage to a ChatMessage.
func (a *ChatProviderAdapter) toChatMessage(msg format.CoreMessage) ChatMessage {
	chatMsg := ChatMessage{
		Role: a.mapRoleToChat(msg.Role),
	}

	// Separate content blocks and tool calls.
	var textBlocks []format.CoreContentBlock
	var toolUseBlocks []format.CoreContentBlock
	for _, b := range msg.Content {
		switch b.Type {
		case "text", "image", "input_text", "output_text":
			textBlocks = append(textBlocks, b)
		case "tool_use":
			toolUseBlocks = append(toolUseBlocks, b)
		case "tool_result":
			textBlocks = append(textBlocks, b)
		case "reasoning":
			// Reasoning blocks become ReasoningContent for providers like DeepSeek
			// that require it to be echoed back in follow-up requests.
			if b.ReasoningText != "" {
				chatMsg.ReasoningContent = b.ReasoningText
			}
			continue
		default:
			if b.Text != "" {
				textBlocks = append(textBlocks, b)
			}
		}
	}

	// Set content (string for text-only, ContentPart array for multimodal).
	if len(textBlocks) > 0 {
		content := a.toChatContent(textBlocks)
		// For assistant messages with tool calls, set content to nil if
		// the content text is empty (OpenAI Chat API requires content=null
		// when tool_calls are present, not empty string).
		if len(toolUseBlocks) > 0 {
			if str, ok := content.(string); ok && str == "" {
				chatMsg.Content = nil
			} else {
				chatMsg.Content = content
			}
		} else {
			chatMsg.Content = content
		}
	}

	// Set tool calls for assistant messages.
	if len(toolUseBlocks) > 0 {
		chatMsg.ToolCalls = make([]ToolCall, 0, len(toolUseBlocks))
		for _, b := range toolUseBlocks {
			argsStr, _ := json.Marshal(string(b.ToolInput))
			chatMsg.ToolCalls = append(chatMsg.ToolCalls, ToolCall{
				ID:   b.ToolUseID,
				Type: "function",
				Function: ToolCallFunc{
					Name:      b.ToolName,
					Arguments: json.RawMessage(argsStr),
				},
			})
		}
	}

	// Set tool_call_id for tool result messages.
	if msg.Role == "tool" && len(toolUseBlocks) == 0 {
		// Look for tool_call_id in the content blocks.
		for _, b := range msg.Content {
			if b.Type == "tool_result" {
				chatMsg.ToolCallID = b.ToolUseID
				// Convert tool_result content to string.
				var toolResultText string
				for _, tc := range b.ToolResultContent {
					toolResultText += tc.Text
				}
				chatMsg.Content = toolResultText
				break
			}
		}
	}

	return chatMsg
}

// toChatContent converts []CoreContentBlock to a Chat content value.
// Returns a string for text-only content, or []ContentPart for multimodal.
func (a *ChatProviderAdapter) toChatContent(blocks []format.CoreContentBlock) any {
	hasImage := false
	for _, b := range blocks {
		if b.Type == "image" {
			hasImage = true
			break
		}
	}

	if !hasImage {
		// Combine text blocks into a single string.
		var text string
		for _, b := range blocks {
			switch b.Type {
			case "text":
				text += b.Text
			case "tool_result":
				for _, tc := range b.ToolResultContent {
					text += tc.Text
				}
			default:
				text += b.Text
			}
		}
		return text
	}

	// Build ContentPart array for multimodal content.
	parts := make([]ContentPart, 0, len(blocks))
	for _, b := range blocks {
		switch b.Type {
		case "text", "input_text", "output_text":
			parts = append(parts, ContentPart{Type: "text", Text: b.Text})
		case "image":
			// CoreContentBlock.ImageData may be either a full URL/data URL or
			// raw base64 (with MediaType set separately, e.g. after the visual
			// extension's CoreSource splits an incoming data URL). Reconstruct
			// a "data:<mime>;base64,<data>" URL when needed so chat upstreams
			// receive a well-formed image_url reference.
			imgURL := b.ImageData
			if !strings.HasPrefix(imgURL, "http://") && !strings.HasPrefix(imgURL, "https://") && !strings.HasPrefix(imgURL, "data:") {
				mediaType := b.MediaType
				if mediaType == "" {
					mediaType = "image/png"
				}
				imgURL = "data:" + mediaType + ";base64," + imgURL
			}
			parts = append(parts, ContentPart{
				Type:     "image_url",
				ImageURL: &ImageURL{URL: imgURL, Detail: "auto"},
			})
		case "tool_result":
			var resultText string
			for _, tc := range b.ToolResultContent {
				resultText += tc.Text
			}
			if resultText != "" {
				parts = append(parts, ContentPart{Type: "text", Text: resultText})
			}
		default:
			if b.Text != "" {
				parts = append(parts, ContentPart{Type: "text", Text: b.Text})
			}
		}
	}
	return parts
}

// mapRoleToChat maps a Core role to a Chat API role.
// Core "assistant" → Chat "assistant" (same mapping).
// Other roles pass through directly.
func (a *ChatProviderAdapter) mapRoleToChat(role string) string {
	// OpenAI Chat uses "assistant" for model responses (same as Core).
	// Other roles: "user", "system", "tool" — pass through directly.
	switch role {
	case "assistant":
		return "assistant"
	case "user":
		return "user"
	case "tool":
		return "tool"
	case "system":
		return "system"
	default:
		return "user"
	}
}

// toChatToolChoice converts CoreToolChoice to json.RawMessage for Chat API.
func (a *ChatProviderAdapter) toChatToolChoice(tc format.CoreToolChoice) json.RawMessage {
	switch tc.Mode {
	case "none":
		data, _ := json.Marshal("none")
		return data
	case "auto":
		data, _ := json.Marshal("auto")
		return data
	case "required":
		if tc.Name != "" {
			choice := map[string]any{
				"type": "function",
				"function": map[string]string{
					"name": tc.Name,
				},
			}
			data, _ := json.Marshal(choice)
			return data
		}
		data, _ := json.Marshal("required")
		return data
	case "any":
		if tc.Name != "" {
			choice := map[string]any{
				"type": "function",
				"function": map[string]string{
					"name": tc.Name,
				},
			}
			data, _ := json.Marshal(choice)
			return data
		}
		data, _ := json.Marshal("auto")
		return data
	default:
		if tc.Name != "" {
			choice := map[string]any{
				"type": "function",
				"function": map[string]string{
					"name": tc.Name,
				},
			}
			data, _ := json.Marshal(choice)
			return data
		}
		data, _ := json.Marshal("auto")
		return data
	}
}

// =========================================================================
// Helpers: Chat → Core
// =========================================================================

// choiceToCoreMessage converts a Choice to a CoreMessage.
func (a *ChatProviderAdapter) choiceToCoreMessage(choice Choice) format.CoreMessage {
	content := a.fromChatContent(choice.Message.Content)

	// Prepend reasoning block if reasoning_content is present (DeepSeek etc.).
	if choice.Message.ReasoningContent != "" {
		content = append([]format.CoreContentBlock{{
			Type:          "reasoning",
			ReasoningText: choice.Message.ReasoningContent,
		}}, content...)
	}

	// Add tool calls as tool_use content blocks.
	if len(choice.Message.ToolCalls) > 0 {
		for _, tc := range choice.Message.ToolCalls {
			content = append(content, format.CoreContentBlock{
				Type:      "tool_use",
				ToolUseID: tc.ID,
				ToolName:  tc.Function.Name,
				ToolInput: unquoteArguments(tc.Function.Arguments),
			})
		}
	}

	return format.CoreMessage{
		Role:    "assistant",
		Content: content,
	}
}

// fromChatContent converts Chat content (string or []ContentPart) to CoreContentBlocks.
func (a *ChatProviderAdapter) fromChatContent(content any) []format.CoreContentBlock {
	if content == nil {
		return nil
	}

	switch v := content.(type) {
	case string:
		if v == "" {
			return nil
		}
		return splitThinkTaggedText(v)
	case []any:
		blocks := make([]format.CoreContentBlock, 0, len(v))
		for _, item := range v {
			if m, ok := item.(map[string]any); ok {
				block := a.fromContentPartMap(m)
				if block.Type == "text" && block.Text != "" {
					blocks = append(blocks, splitThinkTaggedText(block.Text)...)
				} else {
					blocks = append(blocks, block)
				}
			}
		}
		return blocks
	default:
		return nil
	}
}

// fromContentPartMap converts a ContentPart map to a CoreContentBlock.
func (a *ChatProviderAdapter) fromContentPartMap(m map[string]any) format.CoreContentBlock {
	typ, _ := m["type"].(string)
	switch typ {
	case "text":
		text, _ := m["text"].(string)
		return format.CoreContentBlock{Type: "text", Text: text}
	case "image_url":
		imageURL, ok := m["image_url"].(map[string]any)
		if !ok {
			return format.CoreContentBlock{Type: "text"}
		}
		url, _ := imageURL["url"].(string)
		return format.CoreContentBlock{
			Type:      "image",
			ImageData: url,
		}
	default:
		text, _ := m["text"].(string)
		if text != "" {
			return format.CoreContentBlock{Type: "text", Text: text}
		}
		return format.CoreContentBlock{Type: "text"}
	}
}

// mapFinishReason maps OpenAI Chat finish_reason to Core stop_reason.
func (a *ChatProviderAdapter) mapFinishReason(reason string) string {
	switch reason {
	case "stop":
		return "end_turn"
	case "length":
		return "max_tokens"
	case "tool_calls":
		return "tool_use"
	case "content_filter":
		return "content_filter"
	default:
		return reason
	}
}

// unquoteArguments unwraps a JSON-string-encoded tool call argument.
// Chat Completions API returns function.arguments as a JSON string
// (e.g. `"{\"city\":\"Paris\"}"`). When decoded as json.RawMessage,
// this retains the outer string quotes. unquoteArguments strips them
// so Core's ToolCallArguments contains a raw JSON object.
func unquoteArguments(raw json.RawMessage) json.RawMessage {
	if len(raw) < 2 || raw[0] != '"' {
		return raw
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return raw
	}
	return json.RawMessage(s)
}

func defaultToolParameters(schema map[string]any) map[string]any {
	if len(schema) > 0 {
		return schema
	}
	return map[string]any{"type": "object"}
}

const (
	thinkOpenTag  = "<think>"
	thinkCloseTag = "</think>"
)

func splitThinkTaggedText(text string) []format.CoreContentBlock {
	var blocks []format.CoreContentBlock
	remaining := text
	for remaining != "" {
		open := indexFold(remaining, thinkOpenTag)
		if open < 0 {
			appendTextBlock(&blocks, remaining)
			break
		}
		appendTextBlock(&blocks, remaining[:open])
		remaining = remaining[open+len(thinkOpenTag):]
		close := indexFold(remaining, thinkCloseTag)
		if close < 0 {
			appendReasoningBlock(&blocks, remaining)
			break
		}
		appendReasoningBlock(&blocks, remaining[:close])
		remaining = trimOneLeadingLineBreak(remaining[close+len(thinkCloseTag):])
	}
	return blocks
}

func appendTextBlock(blocks *[]format.CoreContentBlock, text string) {
	if text == "" {
		return
	}
	*blocks = append(*blocks, format.CoreContentBlock{Type: "text", Text: text})
}

func appendReasoningBlock(blocks *[]format.CoreContentBlock, text string) {
	if text == "" {
		return
	}
	*blocks = append(*blocks, format.CoreContentBlock{
		Type:          "reasoning",
		ReasoningText: text,
	})
}

func (state *chatStreamChoiceState) consumeThinkTaggedDelta(
	delta string,
	emitText func(string),
	emitReasoning func(string),
	closeReasoning func(string),
) {
	state.thinkBuffer += delta
	for state.thinkBuffer != "" {
		if state.thinkActive {
			close := indexFold(state.thinkBuffer, thinkCloseTag)
			if close >= 0 {
				emitReasoning(state.thinkBuffer[:close])
				state.thinkBuffer = trimOneLeadingLineBreak(state.thinkBuffer[close+len(thinkCloseTag):])
				state.thinkActive = false
				closeReasoning("")
				continue
			}
			emit, keep := splitSafeForTag(state.thinkBuffer, thinkCloseTag)
			emitReasoning(emit)
			state.thinkBuffer = keep
			return
		}

		open := indexFold(state.thinkBuffer, thinkOpenTag)
		if open >= 0 {
			emitText(state.thinkBuffer[:open])
			state.thinkBuffer = state.thinkBuffer[open+len(thinkOpenTag):]
			state.thinkActive = true
			continue
		}
		emit, keep := splitSafeForTag(state.thinkBuffer, thinkOpenTag)
		emitText(emit)
		state.thinkBuffer = keep
		return
	}
}

func (state *chatStreamChoiceState) flushThinkTaggedContent(
	emitText func(string),
	emitReasoning func(string),
	closeReasoning func(string),
) {
	if state.thinkBuffer == "" {
		return
	}
	if state.thinkActive {
		emitReasoning(state.thinkBuffer)
		state.thinkBuffer = ""
		state.thinkActive = false
		closeReasoning("")
		return
	}
	emitText(state.thinkBuffer)
	state.thinkBuffer = ""
}

func splitSafeForTag(text string, tag string) (emit string, keep string) {
	keepLen := longestTagPrefixSuffix(text, tag)
	if keepLen == 0 {
		return text, ""
	}
	return text[:len(text)-keepLen], text[len(text)-keepLen:]
}

func longestTagPrefixSuffix(text string, tag string) int {
	maxLen := min(len(text), len(tag)-1)
	lowerText := strings.ToLower(text)
	lowerTag := strings.ToLower(tag)
	for n := maxLen; n > 0; n-- {
		if strings.HasSuffix(lowerText, lowerTag[:n]) {
			return n
		}
	}
	return 0
}

func indexFold(s string, substr string) int {
	return strings.Index(strings.ToLower(s), strings.ToLower(substr))
}

func trimOneLeadingLineBreak(s string) string {
	if strings.HasPrefix(s, "\r\n") {
		return s[2:]
	}
	if strings.HasPrefix(s, "\n") || strings.HasPrefix(s, "\r") {
		return s[1:]
	}
	return s
}
