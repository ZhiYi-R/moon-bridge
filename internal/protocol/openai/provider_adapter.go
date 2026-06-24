// Package openai contains the OpenAI Responses protocol adapter.
package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"moonbridge/internal/extension/codextool"
	"moonbridge/internal/format"
)

// ============================================================================
// OpenAIProviderAdapter — Core → OpenAI Responses (upstream/provider side)
// ============================================================================

// OpenAIProviderAdapter converts Core format to/from the OpenAI Responses API
// format for outbound (provider) communication.
//
// It implements format.ProviderAdapter (non-streaming) and
// format.ProviderStreamAdapter and ProviderRequestAwareAdapter (streaming).
type OpenAIProviderAdapter struct {
	hooks format.CorePluginHooks
}

// NewOpenAIProviderAdapter creates a new OpenAIProviderAdapter.
func NewOpenAIProviderAdapter(hooks format.CorePluginHooks) *OpenAIProviderAdapter {
	return &OpenAIProviderAdapter{
		hooks: hooks.WithDefaults(),
	}
}

// ProviderProtocol returns "openai-response".
func (a *OpenAIProviderAdapter) ProviderProtocol() string {
	return "openai-response"
}

// ============================================================================
// FromCoreRequest — CoreRequest → *ResponsesRequest
// ============================================================================

// FromCoreRequest converts a CoreRequest into a *ResponsesRequest.
func (a *OpenAIProviderAdapter) FromCoreRequest(ctx context.Context, req *format.CoreRequest) (any, error) {
	if req == nil {
		return nil, fmt.Errorf("openai provider adapter: core request is nil")
	}

	// Allow plugins to mutate the CoreRequest before conversion.
	a.hooks.RewriteMessages(ctx, req)
	a.hooks.MutateCoreRequest(ctx, req)

	// Strip base64 image data from all text content.
	format.StripContentBlocks(req.System)
	for i := range req.Messages {
		format.StripContentBlocks(req.Messages[i].Content)
	}

	outReq := &ResponsesRequest{
		Model:           req.Model,
		MaxOutputTokens: req.MaxTokens,
		Temperature:     req.Temperature,
		TopP:            req.TopP,
		Stream:          req.Stream,
	}

	// Stop sequences.
	if len(req.StopSequences) > 0 {
		data, _ := json.Marshal(req.StopSequences)
		outReq.Stop = data
	}

	// Metadata.
	if req.Metadata != nil {
		outReq.Metadata = req.Metadata
	}

	// System → instructions (concatenate text blocks, skip tool results/images).
	if len(req.System) > 0 {
		var sb strings.Builder
		for _, b := range req.System {
			switch b.Type {
			case "text":
				if sb.Len() > 0 {
					sb.WriteString("\n\n")
				}
				sb.WriteString(b.Text)
			}
		}
		outReq.Instructions = sb.String()
	}

	// Thinking/reasoning config.
	if req.Thinking != nil && req.Thinking.BudgetTokens > 0 {
		outReq.Reasoning = map[string]any{
			"effort": "medium",
		}
		if req.Thinking.BudgetTokens > 8192 {
			outReq.Reasoning["effort"] = "high"
		} else if req.Thinking.BudgetTokens > 2048 {
			outReq.Reasoning["effort"] = "medium"
		} else {
			outReq.Reasoning["effort"] = "low"
		}
	}

	// Convert messages → input items.
	inputItems := make([]inputItem, 0, len(req.Messages))
	for _, msg := range req.Messages {
		items := a.messageToInputItems(msg)
		inputItems = append(inputItems, items...)
	}

	// Messages → Input (array of input items).
	if len(inputItems) > 0 {
		data, _ := json.Marshal(inputItems)
		outReq.Input = data
	}

	// Tools.
	if len(req.Tools) > 0 {
		outReq.Tools = a.coreToolsToOpenAI(req.Tools)
	}

	// Tool choice.
	if req.ToolChoice != nil {
		raw, _ := json.Marshal(req.ToolChoice)
		outReq.ToolChoice = raw
	}

	// Cache extensions.
	if req.Extensions != nil {
		if cacheKey, ok := req.Extensions["prompt_cache_key"].(string); ok {
			outReq.PromptCacheKey = cacheKey
		}
		if cacheRet, ok := req.Extensions["prompt_cache_retention"].(string); ok {
			outReq.PromptCacheRetention = cacheRet
		}
	}

	return outReq, nil
}

// messageToInputItems converts a single CoreMessage into one or more Responses input items.
func (a *OpenAIProviderAdapter) messageToInputItems(msg format.CoreMessage) []inputItem {
	switch msg.Role {
	case "user":
		content := make([]inputContent, 0, len(msg.Content))
		for _, block := range msg.Content {
			switch block.Type {
			case "text":
				content = append(content, inputContent{Type: "input_text", Text: block.Text})
			case "image":
				imgURL := block.ImageData
				if imgURL == "" {
					continue
				}
				imgContent := inputContent{
					Type:     "input_image",
					ImageURL: imgURL,
				}
				if block.MediaType != "" {
					imgContent.MediaType = block.MediaType
				}
				content = append(content, imgContent)
			}
		}
		if len(content) == 0 {
			return nil
		}
		return []inputItem{{
			Type:    "message",
			Role:    "user",
			Content: rawMsg(content),
		}}

	case "assistant":
		var items []inputItem
		var textParts []inputContent
		for _, block := range msg.Content {
			switch block.Type {
			case "text":
				textParts = append(textParts, inputContent{Type: "output_text", Text: block.Text})
			case "reasoning":
				if len(textParts) > 0 {
					items = append(items, inputItem{
						Type:    "message",
						Role:    "assistant",
						Content: rawMsg(textParts),
					})
					textParts = nil
				}
				summary := reasoningSummaryFromCore(block)
				items = append(items, inputItem{
					Type:    "reasoning",
					Summary: rawMsg(summary),
				})
			case "tool_use":
				if len(textParts) > 0 {
					items = append(items, inputItem{
						Type:    "message",
						Role:    "assistant",
						Content: rawMsg(textParts),
					})
					textParts = nil
				}
				argsStr := string(block.ToolInput)
				if !json.Valid(block.ToolInput) {
					argsStr = "{}"
				}
				items = append(items, inputItem{
					Type:      "function_call",
					ID:        block.ToolUseID,
					CallID:    block.ToolUseID,
					Name:      block.ToolName,
					Namespace: block.ToolNamespace,
					Arguments: argsStr,
				})
			}
		}
		if len(textParts) > 0 {
			items = append(items, inputItem{
				Type:    "message",
				Role:    "assistant",
				Content: rawMsg(textParts),
			})
		}
		return items

	case "tool":
		var items []inputItem
		for _, block := range msg.Content {
			if block.Type == "tool_result" {
				outputStr := serializeToolResult(block.ToolResultContent)
				items = append(items, inputItem{
					Type:   "function_call_output",
					CallID: block.ToolUseID,
					Output: outputStr,
				})
			}
		}
		return items
	}
	return nil
}

// reasoningSummaryFromCore converts a reasoning content block to a ReasoningItemSummary slice.
func reasoningSummaryFromCore(block format.CoreContentBlock) []ReasoningItemSummary {
	sig := block.ReasoningSignature
	var summaries []ReasoningItemSummary
	if block.ReasoningText != "" {
		summaries = append(summaries, ReasoningItemSummary{
			Type: "text",
			Text: block.ReasoningText,
		})
	}
	if sig != "" {
		summaries = append(summaries, ReasoningItemSummary{
			Type:      "signature",
			Signature: sig,
		})
	}
	return summaries
}

// serializeToolResult converts ToolResultContent ([]CoreContentBlock) to a JSON string.
func serializeToolResult(blocks []format.CoreContentBlock) json.RawMessage {
	for _, b := range blocks {
		if b.Type == "text" {
			data, _ := json.Marshal(b.Text)
			return data
		}
	}
	data, _ := json.Marshal(blocks)
	return data
}

// rawMsg JSON-encodes v to json.RawMessage.
func rawMsg(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("null")
	}
	return data
}

// coreToolsToOpenAI converts Core tools to OpenAI tool definitions.
func (a *OpenAIProviderAdapter) coreToolsToOpenAI(tools []format.CoreTool) []Tool {
	openaiTools := make([]Tool, 0, len(tools))
	for _, t := range tools {
		tool := Tool{
			Type:        "function",
			Name:        t.Name,
			Description: t.Description,
		}
		if t.InputSchema != nil {
			tool.Parameters = t.InputSchema
		}
		openaiTools = append(openaiTools, tool)
	}
	return openaiTools
}

// ============================================================================
// ToCoreResponse — *Response → *CoreResponse
// ============================================================================

// ToCoreResponse converts a *Response into a *CoreResponse.
func (a *OpenAIProviderAdapter) ToCoreResponse(ctx context.Context, resp any) (*format.CoreResponse, error) {
	return a.ToCoreResponseWithRequest(ctx, nil, resp)
}

// ToCoreResponseWithRequest converts *Response → *CoreResponse with request metadata.
func (a *OpenAIProviderAdapter) ToCoreResponseWithRequest(_ context.Context, req *format.CoreRequest, resp any) (*format.CoreResponse, error) {
	openaiResp, ok := resp.(*Response)
	if !ok {
		direct, ok2 := resp.(Response)
		if !ok2 {
			return nil, fmt.Errorf("openai provider adapter: expected *Response, got %T", resp)
		}
		openaiResp = &direct
	}

	coreResp := &format.CoreResponse{
		ID:     openaiResp.ID,
		Status: openaiResp.Status,
		Model:  openaiResp.Model,
	}

	// Map status → stop_reason.
	coreResp.StopReason = mapResponseStopReason(openaiResp.Status, openaiResp.IncompleteDetails)

	// Extract tool map from request if available.
	var toolMap codextool.ToolMap
	if req != nil && req.Extensions != nil {
		if tmRaw, ok := req.Extensions["codex_tool_map"]; ok {
			if tm, ok := tmRaw.(map[string]any); ok {
				toolMap = codextool.DecodeToolMap(tm)
			}
		}
	}

	// Convert Output items → assistant message content blocks.
	coreResp.Messages = outputItemsToCoreMessages(openaiResp.Output, toolMap)

	// Usage.
	coreResp.Usage = format.CoreUsage{
		InputTokens:       openaiResp.Usage.InputTokens,
		OutputTokens:      openaiResp.Usage.OutputTokens,
		TotalTokens:       openaiResp.Usage.TotalTokens,
		CachedInputTokens: openaiResp.Usage.InputTokensDetails.CachedTokens,
	}

	// Extensions for output tokens details.
	if openaiResp.Usage.OutputTokensDetails.ReasoningTokens > 0 {
		if coreResp.Extensions == nil {
			coreResp.Extensions = make(map[string]any)
		}
		coreResp.Extensions["output_tokens_details"] = map[string]any{
			"reasoning_tokens": openaiResp.Usage.OutputTokensDetails.ReasoningTokens,
		}
	}

	// Error.
	if openaiResp.Error != nil {
		coreResp.Error = &format.CoreError{
			Message: openaiResp.Error.Message,
			Type:    openaiResp.Error.Type,
			Code:    openaiResp.Error.Code,
		}
		if coreResp.Status == "" || coreResp.Status == "completed" {
			coreResp.Status = "failed"
		}
	}

	return coreResp, nil
}

// mapResponseStopReason converts OpenAI Responses status to Core stop_reason.
func mapResponseStopReason(status string, details *IncompleteDetails) string {
	switch status {
	case "completed":
		return "end_turn"
	case "incomplete":
		if details != nil {
			switch details.Reason {
			case "max_output_tokens":
				return "length"
			case "content_filter":
				return "content_filter"
			}
		}
		return "stop"
	case "failed":
		return "error"
	default:
		return "stop"
	}
}

// outputItemsToCoreMessages converts OutputItem slice to Core messages.
func outputItemsToCoreMessages(output []OutputItem, toolMap codextool.ToolMap) []format.CoreMessage {
	if len(output) == 0 {
		return nil
	}

	content := make([]format.CoreContentBlock, 0, len(output))

	for _, item := range output {
		switch item.Type {
		case "message":
			for _, part := range item.Content {
				if part.Type == "text" {
					content = append(content, format.CoreContentBlock{
						Type: "text",
						Text: part.Text,
					})
				}
			}

		case "reasoning":
			for _, s := range item.Summary {
				if s.Type == "text" {
					content = append(content, format.CoreContentBlock{
						Type:          "reasoning",
						ReasoningText: s.Text,
					})
				}
				if s.Type == "signature" {
					if len(content) > 0 && content[len(content)-1].Type == "reasoning" {
						content[len(content)-1].ReasoningSignature = s.Signature
					} else {
						content = append(content, format.CoreContentBlock{
							Type:               "reasoning",
							ReasoningText:      "",
							ReasoningSignature: s.Signature,
						})
					}
				}
			}

		case "function_call":
			toolUseID := item.CallID
			if toolUseID == "" {
				toolUseID = item.ID
			}
			toolName := item.Name
			toolInput := json.RawMessage(item.Arguments)
			if toolMap != nil {
				resolvedName, _, resolvedInput := codextool.CoreToolCallFromProvider(toolName, toolInput, toolMap)
				toolName = resolvedName
				toolInput = resolvedInput
			}
			content = append(content, format.CoreContentBlock{
				Type:          "tool_use",
				ToolUseID:     toolUseID,
				ToolName:      toolName,
				ToolNamespace: item.Namespace,
				ToolInput:     toolInput,
			})
		}
	}

	if len(content) == 0 {
		return nil
	}

	return []format.CoreMessage{
		{
			Role:    "assistant",
			Content: content,
		},
	}
}

// ============================================================================
// ToCoreStream — <-chan StreamEvent → <-chan CoreStreamEvent
// ============================================================================

// ToCoreStream consumes a channel of StreamEvent (from streaming Responses API)
// and returns a channel of CoreStreamEvent.
func (a *OpenAIProviderAdapter) ToCoreStream(ctx context.Context, src any) (*format.StreamResult, error) {
	return a.ToCoreStreamWithRequest(ctx, nil, src)
}

// ToCoreStreamWithRequest converts the stream with request metadata for tool map support.
func (a *OpenAIProviderAdapter) ToCoreStreamWithRequest(pctx context.Context, req *format.CoreRequest, src any) (*format.StreamResult, error) {
	ch, ok := src.(<-chan StreamEvent)
	if !ok {
		return nil, fmt.Errorf("openai provider adapter: expected <-chan StreamEvent, got %T", src)
	}

	events := make(chan format.CoreStreamEvent, 64)

	// Per-stream buffer.
	var buf []StreamEvent
	var bufMu sync.Mutex
	bufReady := make(chan struct{})

	// Extract tool map from request.
	var toolMap codextool.ToolMap
	if req != nil && req.Extensions != nil {
		if tmRaw, ok := req.Extensions["codex_tool_map"]; ok {
			if tm, ok := tmRaw.(map[string]any); ok {
				toolMap = codextool.DecodeToolMap(tm)
			}
		}
	}
	_ = toolMap

	go func() {
		defer close(events)
		defer close(bufReady)

		var seqNum int64
		var responseID string
		var finalUsage *format.CoreUsage

		var nextBlockIndex int
		var activeBlocks []bState

		emit := func(ev format.CoreStreamEvent) {
			seqNum++
			ev.SeqNum = seqNum
			select {
			case events <- ev:
			case <-pctx.Done():
			}
		}

		// dataBytes converts StreamEvent.Data (any) to []byte for unmarshaling.
		dataBytes := func(data any) []byte {
			switch v := data.(type) {
			case []byte:
				return v
			case json.RawMessage:
				return []byte(v)
			case string:
				return []byte(v)
			default:
				encoded, _ := json.Marshal(v)
				return encoded
			}
		}

		readLoop:
		for {
			select {
			case <-pctx.Done():
				return
			case ev, ok := <-ch:
				if !ok {
					break readLoop
				}

				// Buffer for trace capture.
				bufMu.Lock()
				if len(buf) < 1024 {
					buf = append(buf, ev)
				}
				bufMu.Unlock()

				raw := dataBytes(ev.Data)

				switch ev.Event {
				case "response.created":
					var payload struct {
						Response *Response `json:"response"`
					}
					if json.Unmarshal(raw, &payload) == nil && payload.Response != nil {
						responseID = payload.Response.ID
						emit(format.CoreStreamEvent{
							Type:   format.CoreEventCreated,
							ItemID: responseID,
						})
					}

				case "response.in_progress":
					emit(format.CoreStreamEvent{
						Type: format.CoreEventInProgress,
					})

				case "response.output_item.added":
					var payload struct {
						Item inputItem `json:"item"`
					}
					if json.Unmarshal(raw, &payload) != nil {
						continue
					}
					item := payload.Item
					switch item.Type {
					case "reasoning":
						idx := nextBlockIndex
						nextBlockIndex++
						activeBlocks = append(activeBlocks, bState{idx, "reasoning"})
						emit(format.CoreStreamEvent{
							Type:  format.CoreContentBlockStarted,
							Index: idx,
							ContentBlock: &format.CoreContentBlock{
								Type: "reasoning",
							},
						})
					case "function_call":
						idx := nextBlockIndex
						nextBlockIndex++
						activeBlocks = append(activeBlocks, bState{idx, "tool_use"})
						emit(format.CoreStreamEvent{
							Type:  format.CoreContentBlockStarted,
							Index: idx,
							ContentBlock: &format.CoreContentBlock{
								Type:          "tool_use",
								ToolUseID:     item.CallID,
								ToolName:      item.Name,
								ToolNamespace: item.Namespace,
							},
						})
					}

				case "response.content_part.added":
					var payload struct {
						Part  inputContent `json:"part"`
						OIndex int        `json:"index"`
					}
					if json.Unmarshal(raw, &payload) != nil {
						continue
					}
					if payload.Part.Type == "text" || payload.Part.Type == "output_text" {
						idx := nextBlockIndex
						nextBlockIndex++
						activeBlocks = append(activeBlocks, bState{idx, "text"})
						emit(format.CoreStreamEvent{
							Type:  format.CoreContentBlockStarted,
							Index: idx,
							ContentBlock: &format.CoreContentBlock{
								Type: "text",
							},
						})
						if payload.Part.Text != "" {
							emit(format.CoreStreamEvent{
								Type:  format.CoreContentBlockDelta,
								Index: idx,
								Delta: payload.Part.Text,
								ContentBlock: &format.CoreContentBlock{
									Type: "text_delta",
								},
							})
						}
					}

				case "response.output_text.delta":
					var payload struct {
						Delta string `json:"delta"`
						BIndex int   `json:"index"`
					}
					if json.Unmarshal(raw, &payload) != nil {
						continue
					}
					idx := lastBlockIdx(activeBlocks, "text")
					emit(format.CoreStreamEvent{
						Type:  format.CoreContentBlockDelta,
						Index: idx,
						Delta: payload.Delta,
						ContentBlock: &format.CoreContentBlock{
							Type: "text_delta",
						},
					})

				case "response.output_text.done":
					var payload struct {
						Text   string `json:"text"`
						BIndex int    `json:"index"`
					}
					if json.Unmarshal(raw, &payload) != nil {
						continue
					}
					idx := lastBlockIdx(activeBlocks, "text")
					emit(format.CoreStreamEvent{
						Type:  format.CoreContentBlockDone,
						Index: idx,
						ContentBlock: &format.CoreContentBlock{
							Type: "text",
							Text: payload.Text,
						},
					})

				case "response.function_call_arguments.delta":
					var payload struct {
						Delta  string `json:"delta"`
						BIndex int    `json:"index"`
					}
					if json.Unmarshal(raw, &payload) != nil {
						continue
					}
					idx := lastBlockIdx(activeBlocks, "tool_use")
					emit(format.CoreStreamEvent{
						Type:  format.CoreContentBlockDelta,
						Index: idx,
						Delta: payload.Delta,
						ContentBlock: &format.CoreContentBlock{
							Type: "input_json_delta",
						},
					})

				case "response.function_call_arguments.done":
					var payload struct {
						Arguments string `json:"arguments"`
						BIndex    int    `json:"index"`
					}
					if json.Unmarshal(raw, &payload) != nil {
						continue
					}
					idx := lastBlockIdx(activeBlocks, "tool_use")
					emit(format.CoreStreamEvent{
						Type:  format.CoreContentBlockDone,
						Index: idx,
						ContentBlock: &format.CoreContentBlock{
							Type:      "tool_use",
							ToolInput: json.RawMessage(payload.Arguments),
						},
					})

				case "response.reasoning_summary_text.delta":
					var payload struct {
						Delta  string `json:"delta"`
						BIndex int    `json:"index"`
					}
					if json.Unmarshal(raw, &payload) != nil {
						continue
					}
					idx := lastBlockIdx(activeBlocks, "reasoning")
					emit(format.CoreStreamEvent{
						Type:  format.CoreContentBlockDelta,
						Index: idx,
						Delta: payload.Delta,
						ContentBlock: &format.CoreContentBlock{
							Type: "thinking_delta",
						},
					})

				case "response.completed":
					var payload struct {
						Response *Response `json:"response"`
					}
					if json.Unmarshal(raw, &payload) == nil && payload.Response != nil {
						if payload.Response.Usage.InputTokens > 0 || payload.Response.Usage.OutputTokens > 0 {
							finalUsage = &format.CoreUsage{
								InputTokens:       payload.Response.Usage.InputTokens,
								OutputTokens:      payload.Response.Usage.OutputTokens,
								TotalTokens:       payload.Response.Usage.TotalTokens,
								CachedInputTokens: payload.Response.Usage.InputTokensDetails.CachedTokens,
							}
						}
					}
					emit(format.CoreStreamEvent{
						Type:   format.CoreEventCompleted,
						Status: "completed",
						Usage:  finalUsage,
					})

				case "response.failed":
					var payload struct {
						Response *Response `json:"response"`
					}
					if json.Unmarshal(raw, &payload) == nil && payload.Response != nil {
						errObj := payload.Response.Error
						emit(format.CoreStreamEvent{
							Type:   format.CoreEventFailed,
							Status: "failed",
							Error: &format.CoreError{
								Message: safeErrMsg(errObj),
								Type:    safeErrType(errObj),
								Code:    safeErrCode(errObj),
							},
						})
					}
				}
			}
		}
	}()

	return &format.StreamResult{
		Events: events,
		StreamBuffer: func() []any {
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

// ============================================================================
// Helper types and functions
// ============================================================================

// inputContent is a content part within a Responses API input item.
type inputContent struct {
	Type      string `json:"type"`
	Text      string `json:"text,omitempty"`
	ImageURL  string `json:"image_url,omitempty"`
	MediaType string `json:"media_type,omitempty"`
	Detail    string `json:"detail,omitempty"`
}

// lastBlockIdx finds the content block index of the last block with the given type.
func lastBlockIdx(blocks []bState, typ string) int {
	for i := len(blocks) - 1; i >= 0; i-- {
		if blocks[i].bType == typ {
			return blocks[i].index
		}
	}
	return 0
}

// bState tracks an active content block during stream conversion.
type bState struct {
	index int
	bType string
}

func safeErrMsg(e *ErrorObject) string {
	if e == nil {
		return ""
	}
	return e.Message
}

func safeErrType(e *ErrorObject) string {
	if e == nil {
		return ""
	}
	return e.Type
}

func safeErrCode(e *ErrorObject) string {
	if e == nil {
		return ""
	}
	return e.Code
}
