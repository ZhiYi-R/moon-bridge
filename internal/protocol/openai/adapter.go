// Package openai contains the OpenAI Responses protocol adapter.
//
// OpenAIAdapter implements format.ClientAdapter and format.ClientStreamAdapter,
// converting between OpenAI Responses DTOs and the Core intermediate format.
//
// Clean room design: no imports from moonbridge/internal/protocol/bridge/,
// moonbridge/internal/protocol/anthropic/, or any protocol-specific packages
// other than the OpenAI DTOs defined in this package.
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
// OpenAIAdapter
// ============================================================================

// OpenAIAdapter converts between OpenAI Responses DTOs and the Core format.
//
// It implements the inbound (client) side of the bridge:
//   - ClientAdapter:  ToCoreRequest / FromCoreResponse
//   - ClientStreamAdapter: FromCoreStream
//
// The adapter is stateless; all configuration is injected via the constructor.
type OpenAIAdapter struct {
	hooks format.CorePluginHooks

	disablePatchProxy func(string) bool
	nsStrategy        codextool.NamespaceStrategy
}

// NewOpenAIAdapter creates a new OpenAIAdapter with the given config and hooks.
func NewOpenAIAdapter(hooks format.CorePluginHooks, nsStrategy ...codextool.NamespaceStrategy) *OpenAIAdapter {
	strategy := codextool.NestedOneOf
	if len(nsStrategy) > 0 {
		strategy = nsStrategy[0]
	}
	return &OpenAIAdapter{
		hooks:             hooks.WithDefaults(),
		disablePatchProxy: hooks.DisablePatchProxy,
		nsStrategy:        strategy,
	}
}

// ClientProtocol returns the inbound protocol identifier.
func (a *OpenAIAdapter) ClientProtocol() string {
	return "openai-response"
}

// ============================================================================
// ToCoreRequest — OpenAI ResponsesRequest → CoreRequest
// ============================================================================

// ToCoreRequest converts an inbound OpenAI Responses request into a CoreRequest.
//
// Supported mappings:
//   - Model, Temperature, TopP, MaxOutputTokens, Stream, Metadata → direct copy
//   - Input (string | array) → Messages + System
//   - Instructions → prepended to System
//   - Tools → CoreTool (function → name/desc/schema; web_search → extensions)
//   - ToolChoice → CoreToolChoice (with raw JSON preserved)
//   - PromptCacheKey / PromptCacheRetention → Extensions["cache"]
//
// Error handling: all conversion errors are returned to the caller with
// the original message preserved — no error wrapping, no side effects.
func (a *OpenAIAdapter) ToCoreRequest(ctx context.Context, req any) (*format.CoreRequest, error) {
	openaiReq, ok := req.(*ResponsesRequest)
	if !ok {
		// Accept non-pointer value as well
		direct, ok2 := req.(ResponsesRequest)
		if !ok2 {
			return nil, fmt.Errorf("unexpected request type %T; expected *ResponsesRequest", req)
		}
		openaiReq = &direct
	}

	// 1. Apply PreprocessInput hook (operates on raw JSON before parsing).
	preprocessed := a.hooks.PreprocessInput(ctx, openaiReq.Model, openaiReq.Input)
	openaiReq.Input = preprocessed

	// 2. Parse Input → Messages + System.
	messages, system, err := convertInput(openaiReq.Input, openaiReq.Model)
	if err != nil {
		return nil, fmt.Errorf("invalid input: %w", err)
	}

	// 3. Prepend Instructions to System (highest priority).
	if openaiReq.Instructions != "" {
		system = append([]format.CoreContentBlock{{
			Type: "text",
			Text: openaiReq.Instructions,
		}}, system...)
	}

	// 4. Build CoreRequest with direct scalar mappings.
	coreReq := &format.CoreRequest{
		Model:       openaiReq.Model,
		Messages:    messages,
		System:      system,
		Temperature: openaiReq.Temperature,
		TopP:        openaiReq.TopP,
		MaxTokens:   openaiReq.MaxOutputTokens,
		Stream:      openaiReq.Stream,
		Metadata:    openaiReq.Metadata,
	}

	// 5. Convert tools.
	if len(openaiReq.Tools) > 0 {
		coreReq.Tools = flattenToolsWithNamespace(openaiReq.Tools, "", a.disablePatchProxy, a.nsStrategy)
	}
	if injected := a.hooks.InjectTools(format.ContextWithCoreRequest(ctx, coreReq)); len(injected) > 0 {
		coreReq.Tools = append(coreReq.Tools, injected...)
	}

	// 6. Convert tool choice.
	if len(openaiReq.ToolChoice) > 0 && string(openaiReq.ToolChoice) != "null" {
		tc, err := convertToolChoice(openaiReq.ToolChoice)
		if err != nil {
			return nil, fmt.Errorf("invalid tool_choice: %w", err)
		}
		coreReq.ToolChoice = tc
	}

	// 7. Cache metadata passthrough.
	if coreReq.Extensions == nil {
		coreReq.Extensions = make(map[string]any)
	}
	// 5b. Store tool map for response-side reverse mapping.
	coreReq.Extensions["codex_tool_map"] = codextool.BuildToolMapFromCore(coreReq.Tools).Encode()
	if openaiReq.PromptCacheKey != "" || openaiReq.PromptCacheRetention != "" {
		cacheMeta := make(map[string]string)
		if openaiReq.PromptCacheKey != "" {
			cacheMeta["prompt_cache_key"] = openaiReq.PromptCacheKey
		}
		if openaiReq.PromptCacheRetention != "" {
			cacheMeta["prompt_cache_retention"] = openaiReq.PromptCacheRetention
		}
		coreReq.Extensions["cache"] = cacheMeta
	}

	// 8. OpenAI-specific extension fields.
	openaiExt := make(map[string]any)
	if openaiReq.ParallelToolCalls != nil {
		openaiExt["parallel_tool_calls"] = *openaiReq.ParallelToolCalls
	}
	if len(openaiReq.Include) > 0 {
		openaiExt["include"] = openaiReq.Include
	}
	if len(openaiReq.Reasoning) > 0 {
		openaiExt["reasoning"] = openaiReq.Reasoning
	}
	if len(openaiReq.Text) > 0 {
		openaiExt["text"] = openaiReq.Text
	}
	if openaiReq.ServiceTier != "" {
		openaiExt["service_tier"] = openaiReq.ServiceTier
	}
	if openaiReq.PreviousResponseID != "" {
		openaiExt["previous_response_id"] = openaiReq.PreviousResponseID
	}
	if openaiReq.Store != nil {
		openaiExt["store"] = *openaiReq.Store
	}
	if len(openaiExt) > 0 {
		coreReq.Extensions["openai"] = openaiExt
	}

	a.hooks.MutateCoreRequest(ctx, coreReq)

	return coreReq, nil
}

// ============================================================================
// FromCoreResponse — CoreResponse → OpenAI Response
// ============================================================================

// FromCoreResponse converts a CoreResponse back into an OpenAI Response.
//
// The conversion extracts assistant messages as OutputItem("message") items,
// tool_use content blocks as function_call items, and reasoning blocks as
// reasoning items. The output_text field is built by concatenating text parts.
func (a *OpenAIAdapter) FromCoreResponse(ctx context.Context, resp *format.CoreResponse) (any, error) {
	if resp == nil {
		return nil, fmt.Errorf("core response is nil")
	}

	// Apply PostProcessCoreResponse hook.
	a.hooks.PostProcessCoreResponse(ctx, resp)

	response := &Response{
		ID:     resp.ID,
		Object: "response",
		Status: resp.Status,
		Model:  resp.Model,
	}

	// Convert Messages → Output items.
	var output []OutputItem
	for _, msg := range resp.Messages {
		if msg.Role != "assistant" {
			continue
		}
		// Collect consecutive text blocks into a single message item.
		textParts := make([]ContentPart, 0)
		for _, block := range msg.Content {
			switch block.Type {
			case "text":
				textParts = append(textParts, ContentPart{Type: "text", Text: block.Text})

			case "reasoning":
				output = append(output, OutputItem{
					Type:   "reasoning",
					Status: "completed",
					Summary: []ReasoningItemSummary{
						{Type: "text", Text: block.ReasoningText, Signature: block.ReasoningSignature},
					},
				})

			case "tool_use":
				// Flush accumulated text parts before the tool call item.
				if len(textParts) > 0 {
					output = append(output, OutputItem{
						Type:    "message",
						Status:  "completed",
						Role:    "assistant",
						Content: copyContentParts(textParts),
					})
					textParts = textParts[:0]
				}
				output = append(output, buildToolOutputItem(block, resp.Extensions))

			case "tool_result":
				// Tool results don't translate to output items in the response.
				// They are input-side artifacts.

			case "image":
				textParts = append(textParts, ContentPart{Type: "text", Text: "[Image]"})
			}
		}
		// Flush remaining text parts.
		if len(textParts) > 0 {
			output = append(output, OutputItem{
				Type:    "message",
				Status:  "completed",
				Role:    "assistant",
				Content: copyContentParts(textParts),
			})
		}
	}
	response.Output = output

	// Build output_text from message items.
	var texts []string
	for _, item := range output {
		if item.Type == "message" {
			for _, part := range item.Content {
				if part.Type == "text" {
					texts = append(texts, part.Text)
				}
			}
		}
	}
	response.OutputText = strings.Join(texts, "")

	// Map usage.
	usage := Usage{
		InputTokens:  resp.Usage.InputTokens,
		OutputTokens: resp.Usage.OutputTokens,
		TotalTokens:  resp.Usage.TotalTokens,
		InputTokensDetails: InputTokensDetails{
			CachedTokens: resp.Usage.CachedInputTokens,
		},
	}
	// Extract OutputTokensDetails from extensions if available.
	if resp.Extensions != nil {
		if otRaw, ok := resp.Extensions["output_tokens_details"]; ok {
			if otMap, ok := otRaw.(map[string]any); ok {
				if rt, ok := otMap["reasoning_tokens"]; ok {
					if rtVal, ok := rt.(float64); ok {
						usage.OutputTokensDetails = OutputTokensDetails{
							ReasoningTokens: int(rtVal),
						}
					}
				}
			}
		}
	}
	response.Usage = usage

	// Map error.
	if resp.Error != nil {
		response.Error = &ErrorObject{
			Message: resp.Error.Message,
			Type:    resp.Error.Type,
			Code:    resp.Error.Code,
		}
		if response.Status == "" || response.Status == "completed" {
			response.Status = "failed"
		}
	}

	return response, nil
}

// ============================================================================
// FromCoreStream — CoreStreamEvent channel → OpenAI StreamEvent channel
// ============================================================================

// FromCoreStream consumes a channel of CoreStreamEvent and produces a channel
// of StreamEvent suitable for SSE serialization downstream.
//
// The returned channel is closed when the input channel is exhausted.
// The adapter manages internal state (output index tracking, text accumulation)
// to produce correct OpenAI stream semantics.
func (a *OpenAIAdapter) FromCoreStream(ctx context.Context, req *format.CoreRequest, events <-chan format.CoreStreamEvent) (any, error) {
	out := make(chan StreamEvent)
	bufReady := make(chan struct{})

	var buf []StreamEvent
	var bufMu sync.Mutex

	go func() {
		defer close(bufReady)
		a.streamLoopWithBuf(ctx, req, events, out, &buf, &bufMu)
	}()

	return &OpenAIStreamResult{
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

// bufferStreamEvent buffers the OpenAI stream event for trace capture,
// up to the 4MB limit. The event is JSON-marshalled to estimate its size.
func (a *OpenAIAdapter) bufferStreamEvent(ev StreamEvent) {
	// No-op: per-stream buffer is captured by streamLoop's closure.
}

// StreamBuffer returns the buffered stream events for trace capture.
func (a *OpenAIAdapter) StreamBuffer() []StreamEvent {
	// Deprecated: use StreamResult.StreamBuffer instead.
	return nil
}

// openaiStreamResult wraps the OpenAI stream channel with per-stream buffer access.
type OpenAIStreamResult struct {
	ch  <-chan StreamEvent
	buf func() []any
}

// Chan returns the underlying channel of StreamEvents.
func (r *OpenAIStreamResult) Chan() <-chan StreamEvent {
	return r.ch
}

// Buffer returns the captured stream events for post-stream processing.
func (r *OpenAIStreamResult) Buffer() []any {
	if r.buf == nil {
		return nil
	}
	return r.buf()
}

// streamLoop is the goroutine body for FromCoreStream.
// nestedBufferState tracks two-level buffering for nested namespace tool calls.
type nestedBufferState struct {
	toolUseID   string
	toolName    string          // original namespace-expanded name
	actionName  string          // extracted sub-tool action name
	namespace   string          // item namespace
	outputIndex int             // index in response.Output
	emitted     bool            // whether output_item.added has been sent
	buf         strings.Builder // accumulated raw JSON arguments
	sequence    func() int64    // event sequencer (captures next func)
}

func (a *OpenAIAdapter) streamLoopWithBuf(ctx context.Context, coreReq *format.CoreRequest, events <-chan format.CoreStreamEvent, out chan<- StreamEvent, buf *[]StreamEvent, bufMu *sync.Mutex) {
	defer close(out)

	// send buffers the event for trace capture before writing to the output channel.
	send := func(ev StreamEvent) {
		bufMu.Lock()
		if len(*buf) < 1024 {
			*buf = append(*buf, ev)
		}
		bufMu.Unlock()
		out <- ev
	}

	seqNum := int64(0)
	next := func() int64 {
		seqNum++
		return seqNum
	}

	// State tracked during streaming.
	var response = &Response{
		Object: "response",
		Status: "in_progress",
	}
	contentText := make(map[int]string)
	toolCallArgs := make(map[int]string)
	toolBlockNames := make(map[int]string)
	outputIndexes := make(map[int]int)
	itemIDs := make(map[int]string)
	reasonIndexes := make(map[int]bool)
	toolCallFinalized := make(map[int]bool)
	nestedBuffers := make(map[int]*nestedBufferState)

	for event := range events {
		// Let hooks skip events.
		if a.hooks.OnStreamEvent(ctx, event) {
			continue
		}

		switch event.Type {
		// ==================================================================
		// Lifecycle: created
		// ==================================================================
		case format.CoreEventCreated:
			// Use ItemID as the response ID if set; otherwise keep the current one.
			if event.ItemID != "" {
				response.ID = event.ItemID
			}
			response.Status = "in_progress"

			send(StreamEvent{
				Event: "response.created",
				Data: ResponseLifecycleEvent{
					Type:           "response.created",
					SequenceNumber: next(),
					Response:       cloneResponse(response),
				},
			})

		// ==================================================================
		// Lifecycle: in_progress
		// ==================================================================
		case format.CoreEventInProgress:
			response.Status = "in_progress"
			send(StreamEvent{
				Event: "response.in_progress",
				Data: ResponseLifecycleEvent{
					Type:           "response.in_progress",
					SequenceNumber: next(),
					Response:       cloneResponse(response),
				},
			})

		// ==================================================================
		// Content block started
		// ==================================================================
		case format.CoreContentBlockStarted:
			if event.ContentBlock == nil {
				continue
			}
			index := event.Index

			switch event.ContentBlock.Type {
			case "text":
				id := fmt.Sprintf("msg_item_%d", index)
				itemIDs[index] = id
				contentText[index] = ""

			case "reasoning":
				id := fmt.Sprintf("rs_item_%d", index)
				itemIDs[index] = id
				contentText[index] = ""
				reasonIndexes[index] = true
				io := len(response.Output)
				outputIndexes[index] = io
				response.Output = append(response.Output, OutputItem{
					Type:    "reasoning",
					ID:      id,
					Status:  "in_progress",
					Summary: []ReasoningItemSummary{},
				})
				send(StreamEvent{
					Event: "response.output_item.added",
					Data: OutputItemEvent{
						Type:           "response.output_item.added",
						SequenceNumber: next(),
						OutputIndex:    io,
						Item:           response.Output[io],
					},
				})
				send(StreamEvent{
					Event: "response.reasoning_summary_part.added",
					Data: ReasoningSummaryPartAddedEvent{
						Type:           "response.reasoning_summary_part.added",
						SequenceNumber: next(),
						ItemID:         id,
						OutputIndex:    io,
						SummaryIndex:   0,
					},
				})
				contentText[index] = ""
			case "tool_use":
				toolUseID := event.ContentBlock.ToolUseID
				if toolUseID == "" {
					toolUseID = fmt.Sprintf("call_%d", index)
				}
				itemIDs[index] = fmt.Sprintf("fc_item_%d", index)
				toolBlockNames[index] = event.ContentBlock.ToolName

				// Check if this tool is a nested namespace (NestedOneOf/NestedAnyOf).
				// If so, defer output_item.added until we extract the action from args.
				toolMap := codextool.DecodeToolMapFromExtensions(coreReq.Extensions)
				spec, hasSpec := toolMap.Lookup(event.ContentBlock.ToolName)
				isNested := hasSpec && (spec.Kind == codextool.ToolNestedOneOf || spec.Kind == codextool.ToolNestedAnyOf)

				if isNested {
					// Defer emission: buffer args until action is extracted.
					nestedBuffers[index] = &nestedBufferState{
						toolUseID:   toolUseID,
						toolName:    event.ContentBlock.ToolName,
						namespace:   spec.Namespace,
						emitted:     false,
						outputIndex: -1,
						sequence:    next,
					}
				} else {
					item := buildToolOutputItemStreaming(event.ContentBlock, coreReq.Extensions, toolUseID)
					outputIndexes[index] = len(response.Output)
					response.Output = append(response.Output, item)
					send(StreamEvent{
						Event: "response.output_item.added",
						Data: OutputItemEvent{
							Type:           "response.output_item.added",
							SequenceNumber: next(),
							OutputIndex:    outputIndexes[index],
							Item:           item,
						},
					})
				}
			}

		// ==================================================================
		// Text delta
		// ==================================================================
		case format.CoreTextDelta:
			index := event.Index
			contentText[index] += event.Delta

			// Reasoning blocks use separate SSE events.
			if reasonIndexes[index] {
				send(StreamEvent{
					Event: "response.reasoning_summary_text.delta",
					Data: ReasoningSummaryTextDeltaEvent{
						Type:           "response.reasoning_summary_text.delta",
						SequenceNumber: next(),
						ItemID:         itemIDs[index],
						OutputIndex:    outputIndexes[index],
						SummaryIndex:   0,
						Delta:          event.Delta,
					},
				})
				break
			}

			// Ensure the output item and content part exist.
			if _, exists := outputIndexes[index]; !exists {
				id, ok := itemIDs[index]
				if !ok {
					id = fmt.Sprintf("msg_item_%d", index)
					itemIDs[index] = id
				}
				item := OutputItem{
					Type:    "message",
					ID:      id,
					Status:  "in_progress",
					Role:    "assistant",
					Content: []ContentPart{{Type: "output_text"}},
				}
				outputIndexes[index] = len(response.Output)
				response.Output = append(response.Output, item)
				send(StreamEvent{
					Event: "response.output_item.added",
					Data: OutputItemEvent{
						Type:           "response.output_item.added",
						SequenceNumber: next(),
						OutputIndex:    outputIndexes[index],
						Item:           item,
					},
				})
				send(StreamEvent{
					Event: "response.content_part.added",
					Data: ContentPartEvent{
						Type:           "response.content_part.added",
						SequenceNumber: next(),
						ItemID:         id,
						OutputIndex:    outputIndexes[index],
						ContentIndex:   0,
						Part:           ContentPart{Type: "output_text"},
					},
				})
			}

			send(StreamEvent{
				Event: "response.output_text.delta",
				Data: OutputTextDeltaEvent{
					Type:           "response.output_text.delta",
					SequenceNumber: next(),
					ItemID:         itemIDs[index],
					OutputIndex:    outputIndexes[index],
					ContentIndex:   0,
					Delta:          event.Delta,
				},
			})

		// ==================================================================
		// Text done
		// ==================================================================
		case format.CoreTextDone:
			index := event.Index
			text := contentText[index]
			delete(contentText, index)

			// Ensure output item exists (may not be created if no deltas arrived).
			if _, hasOutput := outputIndexes[index]; !hasOutput {
				id := itemIDs[index]
				if id == "" {
					id = fmt.Sprintf("msg_item_%d", index)
					itemIDs[index] = id
				}
				outputIndexes[index] = len(response.Output)
				response.Output = append(response.Output, OutputItem{
					Type:    "message",
					ID:      id,
					Status:  "in_progress",
					Role:    "assistant",
					Content: []ContentPart{{Type: "output_text"}},
				})
			}

			outIdx := outputIndexes[index]

			// Store accumulated text in response output for final completed event.
			if outIdx < len(response.Output) && len(response.Output[outIdx].Content) > 0 {
				response.Output[outIdx].Content[0].Text = text
			}

			send(StreamEvent{
				Event: "response.output_text.done",
				Data: OutputTextDoneEvent{
					Type:           "response.output_text.done",
					SequenceNumber: next(),
					ItemID:         itemIDs[index],
					OutputIndex:    outIdx,
					ContentIndex:   0,
					Text:           text,
				},
			})

			// Mark item as completed.
			if idx, ok := outputIndexes[index]; ok && idx < len(response.Output) {
				response.Output[idx].Status = "completed"
			}
			send(StreamEvent{
				Event: "response.output_item.done",
				Data: OutputItemEvent{
					Type:           "response.output_item.done",
					SequenceNumber: next(),
					OutputIndex:    outIdx,
					Item:           response.Output[outIdx],
				},
			})

		// ==================================================================
		// Tool call arguments delta
		// ==================================================================
		case format.CoreToolCallArgsDelta:
			index := event.Index
			toolCallArgs[index] += event.Delta

			// Check if this is a buffered nested namespace tool call.
			if nBuf, isBuffered := nestedBuffers[index]; isBuffered {
				nBuf.buf.WriteString(event.Delta)

				if !nBuf.emitted {
					if action, ok := codextool.TryExtractAction(nBuf.buf.String()); ok {
						nBuf.actionName = action
						nBuf.emitted = true

						// Emit output_item.added with the correct action name.
						item := OutputItem{
							Type:   "function_call",
							ID:     nBuf.toolUseID,
							CallID: nBuf.toolUseID,
							Name:   action,
							Status: "in_progress",
						}
						if nBuf.namespace != "" {
							item.Namespace = nBuf.namespace
						}
						outputIndexes[index] = len(response.Output)
						nBuf.outputIndex = outputIndexes[index]
						response.Output = append(response.Output, item)
						send(StreamEvent{
							Event: "response.output_item.added",
							Data: OutputItemEvent{
								Type:           "response.output_item.added",
								SequenceNumber: next(),
								OutputIndex:    outputIndexes[index],
								Item:           item,
							},
						})

						// Replay already-buffered params (minus the action prefix).
						replayNestedBuffer(nBuf, send, next, index, itemIDs)
					}
				} else {
					// Already emitted: pass through deltas directly.
					emitNestedDelta(nBuf, event.Delta, send, next, index, itemIDs, outputIndexes)
				}
				break
			}

			send(StreamEvent{
				Event: "response.function_call_arguments.delta",
				Data: FunctionCallArgumentsDeltaEvent{
					Type:           "response.function_call_arguments.delta",
					SequenceNumber: next(),
					ItemID:         itemIDs[index],
					OutputIndex:    outputIndexes[index],
					Delta:          event.Delta,
				},
			})

			// ==================================================================
			// Tool call arguments done
			// ==================================================================
		case format.CoreToolCallArgsDone:
			index := event.Index
			if toolCallFinalized[index] {
				break
			}
			finalArgs := event.Delta

			// Check if this is a buffered nested namespace tool call that hasn't
			// emitted yet (action never extracted — flush all buffered data).
			if nBuf, isBuffered := nestedBuffers[index]; isBuffered {
				if !nBuf.emitted {
					// Action never extracted — flush everything as the original name.
					nBuf.actionName = nBuf.toolName
					finalCombined := nBuf.buf.String()
					if finalArgs != "" && finalCombined == "" {
						finalCombined = finalArgs
					}
					item := OutputItem{
						Type:   "function_call",
						ID:     nBuf.toolUseID,
						CallID: nBuf.toolUseID,
						Name:   nBuf.toolName,
						Status: "completed",
					}
					if nBuf.namespace != "" {
						item.Namespace = nBuf.namespace
					}
					item.Arguments = finalCombined
					outputIndexes[index] = len(response.Output)
					nBuf.outputIndex = outputIndexes[index]
					response.Output = append(response.Output, item)
					nBuf.emitted = true
					send(StreamEvent{
						Event: "response.output_item.added",
						Data: OutputItemEvent{
							Type:           "response.output_item.added",
							SequenceNumber: next(),
							OutputIndex:    outputIndexes[index],
							Item:           item,
						},
					})
					send(StreamEvent{
						Event: "response.function_call_arguments.done",
						Data: FunctionCallArgumentsDoneEvent{
							Type:           "response.function_call_arguments.done",
							SequenceNumber: next(),
							ItemID:         itemIDs[index],
							OutputIndex:    outputIndexes[index],
							Arguments:      finalCombined,
						},
					})
					send(StreamEvent{
						Event: "response.output_item.done",
						Data: OutputItemEvent{
							Type:           "response.output_item.done",
							SequenceNumber: next(),
							OutputIndex:    outputIndexes[index],
							Item:           response.Output[outputIndexes[index]],
						},
					})
					delete(nestedBuffers, index)
					break
				}
				// Already emitted — use existing output index.
				delete(nestedBuffers, index)
			}
			if finalArgs == "" {
				finalArgs = toolCallArgs[index]
			}
			if idx, ok := outputIndexes[index]; ok && idx < len(response.Output) {
				response.Output[idx].Arguments = finalArgs
				response.Output[idx].Status = "completed"
				if response.Output[idx].Type == "custom_tool_call" {
					if bn, ok := toolBlockNames[index]; ok && finalArgs != "" {
						response.Output[idx].Input = codextool.RebuildGrammar(bn, json.RawMessage(finalArgs))
					}
				}
			}
			toolCallFinalized[index] = true
			send(StreamEvent{
				Event: "response.function_call_arguments.done",
				Data: FunctionCallArgumentsDoneEvent{
					Type:           "response.function_call_arguments.done",
					SequenceNumber: next(),
					ItemID:         itemIDs[index],
					OutputIndex:    outputIndexes[index],
					Arguments:      finalArgs,
				},
			})
			send(StreamEvent{
				Event: "response.output_item.done",
				Data: OutputItemEvent{
					Type:           "response.output_item.done",
					SequenceNumber: next(),
					OutputIndex:    outputIndexes[index],
					Item:           response.Output[outputIndexes[index]],
				},
			})

		// ==================================================================
		// Lifecycle: completed
		// ==================================================================
		case format.CoreEventCompleted:
			// Build output_text from message items, same as FromCoreResponse.
			var texts []string
			for _, item := range response.Output {
				if item.Type == "message" {
					for _, part := range item.Content {
						if part.Type == "output_text" || part.Type == "text" {
							texts = append(texts, part.Text)
						}
					}
				}
			}
			response.OutputText = strings.Join(texts, "")
			response.Status = "completed"
			if event.Usage != nil {
				response.Usage = Usage{
					InputTokens:  event.Usage.InputTokens,
					OutputTokens: event.Usage.OutputTokens,
					TotalTokens:  event.Usage.InputTokens + event.Usage.OutputTokens,
					InputTokensDetails: InputTokensDetails{
						CachedTokens: event.Usage.CachedInputTokens,
					},
				}
			}
			send(StreamEvent{
				Event: "response.completed",
				Data: ResponseLifecycleEvent{
					Type:           "response.completed",
					SequenceNumber: next(),
					Response:       cloneResponse(response),
				},
			})

		// ==================================================================
		// Lifecycle: incomplete
		// ==================================================================
		case format.CoreEventIncomplete:
			response.Status = "incomplete"
			send(StreamEvent{
				Event: "response.incomplete",
				Data: ResponseLifecycleEvent{
					Type:           "response.incomplete",
					SequenceNumber: next(),
					Response:       cloneResponse(response),
				},
			})

		// ==================================================================
		// Lifecycle: failed
		// ==================================================================
		case format.CoreEventFailed:
			response.Status = "failed"
			if event.Error != nil {
				response.Error = &ErrorObject{
					Message: event.Error.Message,
					Type:    event.Error.Type,
					Code:    event.Error.Code,
				}
			}
			send(StreamEvent{
				Event: "response.failed",
				Data: ResponseLifecycleEvent{
					Type:           "response.failed",
					SequenceNumber: next(),
					Response:       cloneResponse(response),
				},
			})

		// ==================================================================
		// Content block done
		// ==================================================================
		case format.CoreContentBlockDone:
			index := event.Index

			// Reasoning block done — emit reasoning summary part done.
			if reasonIndexes[index] {
				if idx, ok := outputIndexes[index]; ok && idx < len(response.Output) {
					response.Output[idx].Status = "completed"
					sig := ""
					if event.ContentBlock != nil {
						sig = event.ContentBlock.ReasoningSignature
					}
					response.Output[idx].Summary = []ReasoningItemSummary{{
						Type:      "text",
						Text:      contentText[index],
						Signature: sig,
					}}
				}
				send(StreamEvent{
					Event: "response.reasoning_summary_part.done",
					Data: ReasoningSummaryPartDoneEvent{
						Type:           "response.reasoning_summary_part.done",
						SequenceNumber: next(),
						ItemID:         itemIDs[index],
						OutputIndex:    outputIndexes[index],
						SummaryIndex:   0,
					},
				})
				delete(contentText, index)
				delete(itemIDs, index)
				delete(outputIndexes, index)
				delete(reasonIndexes, index)
				break
			}
			// 1. Text/reasoning block done — emit output_text.done + content_part.done + output_item.done.
			if text, ok := contentText[index]; ok {
				itemID := itemIDs[index]
				// Ensure output item exists (may not be created if no deltas arrived).
				if _, hasOutput := outputIndexes[index]; !hasOutput {
					if itemID == "" {
						itemID = fmt.Sprintf("msg_item_%d", index)
						itemIDs[index] = itemID
					}
					outputIndexes[index] = len(response.Output)
					response.Output = append(response.Output, OutputItem{
						Type:    "message",
						ID:      itemID,
						Status:  "in_progress",
						Role:    "assistant",
						Content: []ContentPart{{Type: "output_text"}},
					})
				}
				outputIndex := outputIndexes[index]

				// Store accumulated text in response output for final completed event.
				if outputIndex < len(response.Output) && len(response.Output[outputIndex].Content) > 0 {
					response.Output[outputIndex].Content[0].Text = text
				}

				// output_text.done
				send(StreamEvent{
					Event: "response.output_text.done",
					Data: OutputTextDoneEvent{
						Type:           "response.output_text.done",
						SequenceNumber: next(),
						ItemID:         itemID,
						OutputIndex:    outputIndex,
						ContentIndex:   0,
						Text:           text,
					},
				})

				// content_part.done
				send(StreamEvent{
					Event: "response.content_part.done",
					Data: ContentPartEvent{
						Type:           "response.content_part.done",
						SequenceNumber: next(),
						ItemID:         itemID,
						OutputIndex:    outputIndex,
						ContentIndex:   0,
						Part:           ContentPart{Type: "output_text"},
					},
				})

				// Mark item as completed.
				if idx, ok := outputIndexes[index]; ok && idx < len(response.Output) {
					response.Output[idx].Status = "completed"
				}

				// output_item.done
				send(StreamEvent{
					Event: "response.output_item.done",
					Data: OutputItemEvent{
						Type:           "response.output_item.done",
						SequenceNumber: next(),
						OutputIndex:    outputIndex,
						Item:           response.Output[outputIndexes[index]],
					},
				})

				// Clean up state.
				delete(contentText, index)

			} else if _, ok := outputIndexes[index]; ok {
				if toolCallFinalized[index] {
					break
				}
				// 2. Tool_use block done — emit function_call_arguments.done + output_item.done.
				itemID := itemIDs[index]
				outputIndex := outputIndexes[index]

				// Update item with final accumulated arguments.
				finalArgs := toolCallArgs[index]
				if idx, ok := outputIndexes[index]; ok && idx < len(response.Output) {
					if finalArgs != "" {
						response.Output[idx].Arguments = finalArgs
					}
					response.Output[idx].Status = "completed"
					// Rebuild raw grammar for custom_tool_call items from accumulated args.
					if response.Output[idx].Type == "custom_tool_call" {
						if bn, ok := toolBlockNames[index]; ok && finalArgs != "" {
							response.Output[idx].Input = codextool.RebuildGrammar(bn, json.RawMessage(finalArgs))
						}
					}
				}

				// function_call_arguments.done
				send(StreamEvent{
					Event: "response.function_call_arguments.done",
					Data: FunctionCallArgumentsDoneEvent{
						Type:           "response.function_call_arguments.done",
						SequenceNumber: next(),
						ItemID:         itemID,
						OutputIndex:    outputIndex,
						Arguments:      finalArgs,
					},
				})

				// output_item.done
				send(StreamEvent{
					Event: "response.output_item.done",
					Data: OutputItemEvent{
						Type:           "response.output_item.done",
						SequenceNumber: next(),
						OutputIndex:    outputIndex,
						Item:           response.Output[outputIndexes[index]],
					},
				})
				toolCallFinalized[index] = true

				// Clean up state.
				delete(toolCallArgs, index)
				delete(toolBlockNames, index)
			}

		case format.CoreItemAdded:
			// Item added is handled by content_block_start for tool_use
			// and by first text delta for messages.

		case format.CoreItemDone:
			// Item completion is handled by text_done / tool_call_args_done / content_block_done.

		// ==================================================================
		// Ping
		// ==================================================================
		case format.CorePing:
			// Anthropic keepalive — no OpenAI equivalent. Silently skip.
		}
	}

	// Notify stream completion hook.
	outputText := response.OutputText
	a.hooks.OnStreamComplete(ctx, coreReq.Model, outputText)
}

// ============================================================================
// Input Conversion Helpers
// ============================================================================

// inputItem is a lightweight struct for unmarshalling OpenAI input array items.
type inputItem struct {
	Type      string          `json:"type"`
	Role      string          `json:"role"`
	Content   json.RawMessage `json:"content"`
	Summary   json.RawMessage `json:"summary"`
	CallID    string          `json:"call_id"`
	Name      string          `json:"name"`
	Arguments string          `json:"arguments"`
	Output    json.RawMessage `json:"output"`
	Input     string          `json:"input"`
	Action    *ToolAction     `json:"action,omitempty"`
	ID        string          `json:"id"`
	Status    string          `json:"status"`
}

// convertInput parses OpenAI Input (string or array) into Core messages and system blocks.
//
// Behaviour:
//   - If Input is a JSON string → single user message.
//   - If Input is a JSON array → iterate items, group by role.
//   - Items with role "system" or "developer" → system blocks.
//   - Items with role "assistant" → assistant messages (including tool_use blocks
//     from function_call items).
//   - Items with tool-call output types → tool_result user messages.
//   - Items with tool-call input types → tool_use within assistant messages.
func convertInput(raw json.RawMessage, model string) ([]format.CoreMessage, []format.CoreContentBlock, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil, nil
	}

	trimmed := strings.TrimSpace(string(raw))

	// String case: single user message.
	if strings.HasPrefix(trimmed, "\"") {
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return nil, nil, fmt.Errorf("invalid input string: %w", err)
		}
		if text == "" {
			return nil, nil, nil
		}
		return []format.CoreMessage{
			{
				Role:    "user",
				Content: []format.CoreContentBlock{{Type: "text", Text: text}},
			},
		}, nil, nil
	}

	// Array case.
	if !strings.HasPrefix(trimmed, "[") {
		return nil, nil, fmt.Errorf("input must be a string or array")
	}

	var items []inputItem
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, nil, fmt.Errorf("invalid input array: %w", err)
	}

	messages := make([]format.CoreMessage, 0, len(items))
	system := make([]format.CoreContentBlock, 0)
	var pendingReasoning []format.CoreContentBlock
	var pendingFCBlocks []format.CoreContentBlock // batch consecutive function_calls

	for _, item := range items {
		if isToolCallOutputType(item.Type) {
			// Keep any pending reasoning within the same assistant tool-call turn.
			// Emitting a standalone assistant reasoning message here would break
			// the required adjacency of assistant tool_use -> immediate tool_result.
			if len(pendingFCBlocks) > 0 && len(pendingReasoning) > 0 {
				pendingFCBlocks = mergeReasoningBeforeToolUse(pendingFCBlocks, pendingReasoning)
				pendingReasoning = pendingReasoning[:0]
			}
			// Flush pending function_calls before tool results.
			if len(pendingFCBlocks) > 0 {
				flushed := make([]format.CoreContentBlock, len(pendingFCBlocks))
				copy(flushed, pendingFCBlocks)
				messages = append(messages, format.CoreMessage{
					Role:    "assistant",
					Content: flushed,
				})
				pendingFCBlocks = pendingFCBlocks[:0]
			}
			// Flush pending reasoning before tool results.
			if len(pendingReasoning) > 0 {
				flushedReasoning := make([]format.CoreContentBlock, len(pendingReasoning))
				copy(flushedReasoning, pendingReasoning)
				messages = append(messages, format.CoreMessage{
					Role:    "assistant",
					Content: flushedReasoning,
				})
				pendingReasoning = pendingReasoning[:0]
			}
			// Each tool result → separate tool-role Core message.
			messages = append(messages, format.CoreMessage{
				Role: "tool",
				Content: []format.CoreContentBlock{{
					Type:              "tool_result",
					ToolUseID:         item.CallID,
					ToolResultContent: outputToContentBlocks(item.Output),
				}},
			})
			continue
		}
		// Flush pending function_calls before non-function-call items.
		// Don't flush between consecutive function_call items — they should
		// be batched into a single assistant message.
		if !isToolCallInputType(item.Type) && item.Type != "reasoning" && len(pendingFCBlocks) > 0 {
			flushed := make([]format.CoreContentBlock, len(pendingFCBlocks))
			copy(flushed, pendingFCBlocks)
			messages = append(messages, format.CoreMessage{
				Role:    "assistant",
				Content: flushed,
			})
			pendingFCBlocks = pendingFCBlocks[:0]
		}

		role := item.Role
		if role == "" {
			role = "user"
		}

		// Handle reasoning input items — convert to thinking blocks for the next assistant message.
		if item.Type == "reasoning" {
			blocks := reasoningBlocksFromSummary(item.Summary)
			if len(pendingFCBlocks) > 0 {
				pendingFCBlocks = mergeReasoningBeforeToolUse(pendingFCBlocks, blocks)
				continue
			}
			pendingReasoning = append(pendingReasoning, blocks...)
			continue
		}

		switch {
		case item.Type == "function_call":
			// NOTE: Reasoning alignment for inference models (o3/o4-mini):
			// OpenAI requires a "reasoning" input item before each "function_call" item
			// when using reasoning models. Currently, pendingReasoning blocks (from preceding
			// reasoning input items) are merged into the next assistant message, but no
			// dummy reasoning block is injected if pendingReasoning is empty.
			// A future fix should add a dummy reasoning block here when:
			// (a) the model is a reasoning model, and (b) pendingReasoning is empty.
			if len(pendingReasoning) == 0 && isReasoningModel(model) {
				pendingReasoning = append(pendingReasoning, format.CoreContentBlock{
					Type:          "reasoning",
					ReasoningText: "",
				})
			}

			// function_call in input → tool_use assistant block.
			// Collect into pendingFCBlocks to batch consecutive calls into a single assistant message.
			if len(pendingReasoning) > 0 {
				pendingFCBlocks = append(pendingFCBlocks, pendingReasoning...)
				pendingReasoning = pendingReasoning[:0]
			}
			toolInput := json.RawMessage(item.Arguments)
			if !json.Valid([]byte(item.Arguments)) {
				toolInput = json.RawMessage(`{}`)
			}
			pendingFCBlocks = append(pendingFCBlocks, format.CoreContentBlock{
				Type:      "tool_use",
				ToolUseID: firstNonEmpty(item.CallID, item.ID),
				ToolName:  item.Name,
				ToolInput: toolInput,
			})

		case item.Type == "custom_tool_call" || item.Type == "local_shell_call":
			if len(pendingReasoning) > 0 {
				pendingFCBlocks = append(pendingFCBlocks, pendingReasoning...)
				pendingReasoning = pendingReasoning[:0]
			}
			toolInput := json.RawMessage(item.Arguments)
			if !json.Valid([]byte(item.Arguments)) {
				if item.Input != "" {
					toolInput, _ = json.Marshal(map[string]string{"input": item.Input})
				} else {
					toolInput = json.RawMessage(`{}`)
				}
			}
			if item.Type == "local_shell_call" && item.Action != nil {
				data, _ := json.Marshal(item.Action)
				toolInput = data
			}
			pendingFCBlocks = append(pendingFCBlocks, format.CoreContentBlock{
				Type:      "tool_use",
				ToolUseID: firstNonEmpty(item.CallID, item.ID),
				ToolName:  item.Name,
				ToolInput: toolInput,
			})

		case role == "system" || role == "developer":
			blocks := contentBlocksFromRaw(item.Content)
			if len(blocks) > 0 {
				system = append(system, blocks...)
			}

		case role == "assistant":
			blocks := contentBlocksFromRaw(item.Content)
			// Prepend any pending reasoning blocks (from previous reasoning input items)
			// before the assistant message content.
			if len(pendingReasoning) > 0 {
				blocks = append(pendingReasoning, blocks...)
				pendingReasoning = pendingReasoning[:0]
			}
			if len(blocks) > 0 {
				messages = append(messages, format.CoreMessage{
					Role:    "assistant",
					Content: blocks,
				})
			}

		default:
			blocks := contentBlocksFromRaw(item.Content)
			if len(blocks) > 0 {
				messages = append(messages, format.CoreMessage{
					Role:    "user",
					Content: blocks,
				})
			}
		}
	}

	// Flush remaining reasoning blocks (no following assistant message).
	if len(pendingReasoning) > 0 {
		messages = append(messages, format.CoreMessage{
			Role:    "assistant",
			Content: pendingReasoning,
		})
	}

	// Flush any remaining batched function_calls.
	if len(pendingFCBlocks) > 0 {
		flushed := make([]format.CoreContentBlock, len(pendingFCBlocks))
		copy(flushed, pendingFCBlocks)
		messages = append(messages, format.CoreMessage{
			Role:    "assistant",
			Content: flushed,
		})
	}

	return messages, system, nil
}

// contentPartRaw is a lightweight struct for content part JSON parsing.
type contentPartRaw struct {
	Type     string          `json:"type"`
	Text     string          `json:"text"`
	ImageURL json.RawMessage `json:"image_url"`
}

// contentBlocksFromRaw parses an item's Content JSON into CoreContentBlocks.
//
// Supports:
//   - string content → single text block
//   - array of content parts → text/image blocks
func contentBlocksFromRaw(raw json.RawMessage) []format.CoreContentBlock {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}

	trimmed := strings.TrimSpace(string(raw))

	// String content.
	if strings.HasPrefix(trimmed, "\"") {
		var text string
		if err := json.Unmarshal(raw, &text); err != nil || text == "" {
			return nil
		}
		return []format.CoreContentBlock{{Type: "text", Text: text}}
	}

	// Array of content parts.
	var parts []contentPartRaw
	if err := json.Unmarshal(raw, &parts); err == nil && len(parts) > 0 {
		blocks := make([]format.CoreContentBlock, 0, len(parts))
		for _, part := range parts {
			switch part.Type {
			case "input_text", "text", "output_text":
				if part.Text != "" {
					blocks = append(blocks, format.CoreContentBlock{Type: "text", Text: part.Text})
				}
			case "input_image", "image", "image_url":
				// Image content — extract URL or data URI.
				if src := imageSourceFromRaw(part.ImageURL); src != "" {
					// Determine media type from the source.
					mediaType := "image/png"
					if strings.HasPrefix(src, "data:") {
						if header, _, ok := strings.Cut(src, ","); ok {
							mt := strings.TrimPrefix(header, "data:")
							if semicolon := strings.IndexByte(mt, ';'); semicolon >= 0 {
								mt = mt[:semicolon]
							}
							if mt != "" {
								mediaType = mt
							}
						}
					}
					blocks = append(blocks, format.CoreContentBlock{
						Type:      "image",
						ImageData: src,
						MediaType: mediaType,
					})
				}
			}
		}
		return blocks
	}

	// Fallback: raw text.
	if trimmed != "" {
		return []format.CoreContentBlock{{Type: "text", Text: trimmed}}
	}
	return nil
}

// imageSourceFromRaw extracts an image URL string from a JSON raw message
// that may be a plain string URL or an object with "url" field.
func imageSourceFromRaw(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var url string
	if err := json.Unmarshal(raw, &url); err == nil {
		return strings.TrimSpace(url)
	}
	var obj struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil {
		return strings.TrimSpace(obj.URL)
	}
	return ""
}

// ============================================================================
// Tool Conversion
// ============================================================================

// convertToolChoice parses an OpenAI tool_choice JSON value into a CoreToolChoice.

func convertToolChoice(raw json.RawMessage) (*format.CoreToolChoice, error) {
	tc := &format.CoreToolChoice{
		Raw: raw,
	}

	// Try string.
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

	// Try object.
	var obj struct {
		Type     string `json:"type"`
		Name     string `json:"name"`
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		// Preserve raw on parse failure; return partial choice.
		return tc, nil
	}

	tc.Mode = obj.Type
	tc.Name = obj.Name
	if tc.Name == "" {
		tc.Name = obj.Function.Name
	}
	return tc, nil
}

// ============================================================================
// Utility
// ============================================================================

// firstNonEmpty returns the first non-empty string from the list.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// copyContentParts returns a shallow copy of a ContentPart slice.
func copyContentParts(parts []ContentPart) []ContentPart {
	out := make([]ContentPart, len(parts))
	copy(out, parts)
	return out
}

// reasoningSummaryItem is a lightweight struct for unmarshalling reasoning summary JSON.
type reasoningSummaryItem struct {
	Type      string `json:"type"`
	Text      string `json:"text"`
	Signature string `json:"signature,omitempty"`
}

// reasoningBlocksFromSummary parses a reasoning summary JSON array and converts
// each item to a CoreContentBlock of type "reasoning".
// This preserves the thinking text and optional signature for upstream replay.
func reasoningBlocksFromSummary(raw json.RawMessage) []format.CoreContentBlock {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var items []reasoningSummaryItem
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil
	}
	blocks := make([]format.CoreContentBlock, 0, len(items))
	for _, item := range items {
		if item.Text == "" {
			continue
		}
		blocks = append(blocks, format.CoreContentBlock{
			Type:          "reasoning",
			ReasoningText: item.Text,
			// Use Signature from the item if present (adapter-created "text" type).
			// This preserves the provider-specific thinking signature needed for
			// continuing reasoning chains across conversation turns.
			ReasoningSignature: item.Signature,
		})
	}
	return blocks
}

func mergeReasoningBeforeToolUse(blocks []format.CoreContentBlock, reasoning []format.CoreContentBlock) []format.CoreContentBlock {
	if len(reasoning) == 0 {
		return blocks
	}
	insertAt := 0
	for insertAt < len(blocks) && blocks[insertAt].Type == "reasoning" {
		insertAt++
	}
	merged := make([]format.CoreContentBlock, 0, len(blocks)+len(reasoning))
	merged = append(merged, blocks[:insertAt]...)
	merged = append(merged, reasoning...)
	merged = append(merged, blocks[insertAt:]...)
	return merged
}

func isToolCallInputType(itemType string) bool {
	switch itemType {
	case "function_call", "custom_tool_call", "local_shell_call":
		return true
	default:
		return false
	}
}

func isToolCallOutputType(itemType string) bool {
	switch itemType {
	case "function_call_output", "custom_tool_call_output", "local_shell_call_output":
		return true
	default:
		return false
	}
}

// cloneResponse creates a shallow copy of a Response for use in stream events.
func cloneResponse(r *Response) Response {
	if r == nil {
		return Response{}
	}
	return *r
}

func isReasoningModel(model string) bool {
	m := strings.TrimSpace(strings.ToLower(model))
	if m == "" {
		return false
	}
	return strings.HasPrefix(m, "o1") ||
		strings.HasPrefix(m, "o3") ||
		strings.HasPrefix(m, "o4") ||
		strings.Contains(m, "reasoning")
}

// toolInputString converts a json.RawMessage tool input to a string,
// defaulting to "{}" when the input is nil or null.
func toolInputString(input json.RawMessage) string {
	if len(input) == 0 || string(input) == "null" {
		return "{}"
	}
	return string(input)
}

// outputToString converts a json.RawMessage Output field to a string.
// The output can be a plain string or an array of content parts (multi-modal).
func outputToString(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	// Try string first.
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	// Fallback: return the raw JSON as a string (array/object).
	return string(raw)
}

// localShellActionFromRaw parses tool input JSON into a ToolAction for local_shell.
func localShellActionFromRaw(raw json.RawMessage) *ToolAction {
	var input struct {
		Command          []string          `json:"command"`
		WorkingDirectory string            `json:"working_directory"`
		TimeoutMS        int               `json:"timeout_ms"`
		Env              map[string]string `json:"env"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return &ToolAction{Type: "exec"}
	}
	return &ToolAction{
		Type:             "exec",
		Command:          input.Command,
		WorkingDirectory: input.WorkingDirectory,
		TimeoutMS:        input.TimeoutMS,
		Env:              input.Env,
	}
}

// ============================================================================
// Response-side Tool Output Item Builder
// ============================================================================

// buildToolOutputItem constructs an OutputItem using the codex_tool_map
// to determine the correct output item type (function_call, custom_tool_call, local_shell_call).
func buildToolOutputItem(block format.CoreContentBlock, extensions map[string]any) OutputItem {
	toolMap := codextool.DecodeToolMapFromExtensions(extensions)
	itemT, itemN, itemNS, itemInput, isLS, actionJSON := codextool.OutputItemFromBlock(block.ToolName, block.ToolInput, toolMap)
	if isLS {
		return OutputItem{
			Type:   "local_shell_call",
			ID:     block.ToolUseID,
			CallID: block.ToolUseID,
			Status: "completed",
			Action: localShellActionFromRaw(actionJSON),
		}
	}
	return OutputItem{
		Type:      itemT,
		ID:        block.ToolUseID,
		CallID:    block.ToolUseID,
		Name:      itemN,
		Namespace: itemNS,
		Arguments: toolInputString(block.ToolInput),
		Input:     itemInput,
		Status:    "completed",
	}
}

// buildToolOutputItemStreaming constructs a streaming OutputItem for a tool_use content block start.
// The item is created with "in_progress" status.

// replayNestedBuffer emits the accumulated params from a nested namespace buffer
// as function_call_arguments.delta events, stripping the action prefix.
func replayNestedBuffer(nBuf *nestedBufferState, send func(StreamEvent), next func() int64, index int, itemIDs map[int]string) {
	if nBuf.buf.Len() == 0 {
		return
	}
	paramsOnly := stripPrefixActionFromJSON(nBuf.buf.String(), nBuf.actionName)
	if paramsOnly != "" {
		send(StreamEvent{
			Event: "response.function_call_arguments.delta",
			Data: FunctionCallArgumentsDeltaEvent{
				Type:           "response.function_call_arguments.delta",
				SequenceNumber: next(),
				ItemID:         itemIDs[index],
				OutputIndex:    nBuf.outputIndex,
				Delta:          paramsOnly,
			},
		})
	}
}

// emitNestedDelta sends a function_call_arguments.delta for a nested namespace tool
// that has already emitted its output_item.added.
func emitNestedDelta(nBuf *nestedBufferState, delta string, send func(StreamEvent), next func() int64, index int, itemIDs map[int]string, outputIndexes map[int]int) {
	cleanedDelta := stripPrefixActionFromJSON(delta, nBuf.actionName)
	if cleanedDelta == "" {
		return
	}
	oi := nBuf.outputIndex
	if oi < 0 {
		oi = outputIndexes[index]
	}
	send(StreamEvent{
		Event: "response.function_call_arguments.delta",
		Data: FunctionCallArgumentsDeltaEvent{
			Type:           "response.function_call_arguments.delta",
			SequenceNumber: next(),
			ItemID:         itemIDs[index],
			OutputIndex:    oi,
			Delta:          cleanedDelta,
		},
	})
}

// stripPrefixActionFromJSON removes the "action": "value" portion from the start
// of a partial JSON string. Uses a position-constrained scan that only looks for
// "action" as a top-level key (before any nested "{" or after the first "," that
// signals the end of the first key-value pair). Falls back to full JSON parse
// when the buffer is syntactically complete.
func stripPrefixActionFromJSON(raw string, action string) string {
	if raw == "" {
		return ""
	}

	// First, try a full JSON parse — if the buffer is complete, this is the
	// most robust path.
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &parsed); err == nil {
		delete(parsed, "action")
		if len(parsed) == 0 {
			return ""
		}
		data, _ := json.Marshal(parsed)
		result := string(data)
		// Strip outer braces for streaming delta context.
		result = strings.TrimPrefix(result, "{")
		result = strings.TrimSuffix(result, "}")
		return strings.TrimSpace(result)
	}

	// Fallback: position-constrained scan. Only look for "action" in the
	// first object level — roughly the content before a nested "{" or after
	// the first top-level comma that follows the action key-value pair.
	//
	// Strategy: find "action" at the top level, extract its value, remove
	// the key-value pair, and return the remaining JSON fragment.
	idx := strings.Index(raw, `"action"`)
	if idx < 0 {
		return raw
	}

	// Only treat as top-level action if it appears before any nested object
	// (the namespace tool schema is flat — action is at the root).
	firstBrace := strings.IndexByte(raw, '{')
	if firstBrace >= 0 && firstBrace < idx {
		// "action" is not in the first object — return raw unchanged.
		return raw
	}

	// Find the colon after the key.
	afterKey := raw[idx+8:]
	colonIdx := strings.IndexByte(afterKey, ':')
	if colonIdx < 0 {
		return raw
	}

	// Skip whitespace and colon.
	afterColon := strings.TrimSpace(afterKey[colonIdx+1:])
	if len(afterColon) == 0 {
		return raw
	}

	// Must start with a quote (action is always a string).
	if afterColon[0] != '"' {
		return raw
	}
	afterColon = afterColon[1:] // skip opening quote
	endQuote := strings.IndexByte(afterColon, '"')
	if endQuote < 0 {
		return raw
	}

	// Extract the portion after the action value.
	afterValue := strings.TrimSpace(afterColon[endQuote+1:])
	// Strip trailing comma.
	afterValue = strings.TrimLeft(afterValue, ", ")

	// Combine the part before the action key with the part after the value.
	prefix := strings.TrimRight(raw[:idx], ", ")
	if prefix == "" || prefix == "{" || strings.TrimSpace(prefix) == "{" {
		return afterValue
	}
	return prefix + ", " + afterValue
}
func buildToolOutputItemStreaming(block *format.CoreContentBlock, extensions map[string]any, toolUseID string) OutputItem {
	toolMap := codextool.DecodeToolMapFromExtensions(extensions)
	itemT, itemN, itemNS, itemInput, isLS, actionJSON := codextool.OutputItemFromBlock(block.ToolName, block.ToolInput, toolMap)
	_ = itemInput
	if isLS {
		return OutputItem{
			Type:   "local_shell_call",
			ID:     toolUseID,
			CallID: toolUseID,
			Status: "in_progress",
			Action: localShellActionFromRaw(actionJSON),
		}
	}
	return OutputItem{
		Type:      itemT,
		ID:        toolUseID,
		CallID:    toolUseID,
		Name:      itemN,
		Namespace: itemNS,
		Arguments: toolInputString(block.ToolInput),
		Status:    "in_progress",
	}
}

// ============================================================================
// Tool Conversion — custom/namespace/apply_patch
// ============================================================================

// convertToolWithNamespace converts a single OpenAI Tool to one or more CoreTools.
// Function/web_search/file_search/code_interpreter/computer_use_preview pass through.
// Custom tools are expanded using codex package helpers.
// Namespace tools are recursively flattened.
func convertToolWithNamespace(tool Tool, namespace string, disablePatchProxy func(string) bool, nsStrategy codextool.NamespaceStrategy) []format.CoreTool {
	name := namespacedToolName(namespace, tool.Name)
	ext := make(map[string]any)

	switch tool.Type {
	case "function":
		ct := format.CoreTool{
			Name:        name,
			Description: tool.Description,
			InputSchema: tool.Parameters,
		}
		codextool.AnnotateCoreTool(&ct, codextool.ToolFunction, tool.Name, namespace)
		return []format.CoreTool{ct}

	case "web_search", "web_search_preview":
		ext["source_type"] = tool.Type
		return []format.CoreTool{{
			Name:        tool.Type,
			Description: "Search the web for up-to-date information.",
			Extensions:  ext,
		}}
	case "file_search":
		ext["source_type"] = tool.Type
		ext["max_num_results"] = tool.MaxNumResults
		return []format.CoreTool{{
			Name:        tool.Type,
			Description: "Search files in the user's file system.",
			Extensions:  ext,
		}}

	case "code_interpreter":
		ext["source_type"] = tool.Type
		return []format.CoreTool{{
			Name:        tool.Type,
			Description: "Execute code in a sandboxed interpreter.",
			Extensions:  ext,
		}}

	case "computer_use_preview":
		ext["source_type"] = tool.Type
		ext["display_width"] = tool.DisplayWidth
		ext["display_height"] = tool.DisplayHeight
		return []format.CoreTool{{
			Name:        tool.Type,
			Description: "Use a computer to perform actions.",
			Extensions:  ext,
		}}

	case "namespace":
		ns := namespacedToolName(namespace, tool.Name)
		// Build sub-tool map for BuildNamespaceTools.
		subMap := make(map[string]format.CoreTool)
		var subNames []string
		for _, sub := range tool.Tools {
			subNames = append(subNames, sub.Name)
			subMap[sub.Name] = format.CoreTool{
				Name:        sub.Name,
				Description: sub.Description,
				InputSchema: sub.Parameters,
			}
		}
		tools, err := codextool.BuildNamespaceTools(subNames, subMap, ns, nsStrategy)
		if err != nil || len(tools) == 0 {
			// Fallback to flat expansion.
			return flattenToolsWithNamespace(tool.Tools, ns, disablePatchProxy, nsStrategy)
		}
		return tools

	case "custom":
		grammar := codextool.CustomToolGrammar(tool.Format)
		if tool.Name == "local_shell" {
			ct := format.CoreTool{
				Name:        name,
				Description: tool.Description,
				InputSchema: codextool.LocalShellSchema(),
			}
			codextool.AnnotateCoreTool(&ct, codextool.ToolLocalShell, tool.Name, "")
			return []format.CoreTool{ct}
		}
		if codextool.IsApplyPatchGrammar(grammar) {
			if disablePatchProxy == nil || !disablePatchProxy(tool.Name) {
				proxyTools := codextool.ApplyPatchProxyCoreTools(name)
				for i := range proxyTools {
					codextool.AnnotateCoreTool(&proxyTools[i], codextool.ToolApplyPatch, tool.Name, "")
				}
				return proxyTools
			}
			// Proxy disabled: fall through to ToolRaw.
		}
		if codextool.IsExecGrammar(grammar) {
			ct := format.CoreTool{
				Name:        name,
				Description: codextool.ExecProxyDescription(),
				InputSchema: codextool.ExecProxySchema(),
			}
			codextool.AnnotateCoreTool(&ct, codextool.ToolExec, tool.Name, "")
			return []format.CoreTool{ct}
		}
		// Other custom tools: keep original name with raw input schema
		ct := format.CoreTool{
			Name:        name,
			Description: customToolDescription(tool, grammar),
			InputSchema: codextool.CustomToolInputSchema(grammar),
		}
		codextool.AnnotateCoreTool(&ct, codextool.ToolRaw, tool.Name, "")
		return []format.CoreTool{ct}

	default:
		ext["source_type"] = tool.Type
		return []format.CoreTool{{
			Name:        name,
			Description: tool.Description,
			InputSchema: tool.Parameters,
			Extensions:  ext,
		}}
	}
}

// flattenToolsWithNamespace recursively flattens namespace tools and converts
// individual tools, building a flat list of CoreTools suitable for upstream providers.
func flattenToolsWithNamespace(openaiTools []Tool, namespace string, disablePatchProxy func(string) bool, nsStrategy codextool.NamespaceStrategy) []format.CoreTool {
	var result []format.CoreTool
	for _, t := range openaiTools {
		converted := convertToolWithNamespace(t, namespace, disablePatchProxy, nsStrategy)
		result = append(result, converted...)
	}
	// Deduplicate by name: Codex may send the same tool both as a namespace member
	// and as an independently-injected function tool (e.g. MCP tools that inject themselves
	// after first use). Prefer tools with a codex_namespace annotation (comes from namespace
	// expansion) over flat function tools with the same name.
	seen := make(map[string]int, len(result)) // name → index in deduped
	deduped := make([]format.CoreTool, 0, len(result))
	for _, t := range result {
		if existing, exists := seen[t.Name]; exists {
			existingNS, _ := deduped[existing].Extensions["codex_namespace"].(string)
			newNS, _ := t.Extensions["codex_namespace"].(string)
			if existingNS == "" && newNS != "" {
				deduped[existing] = t
			}
			continue
		}
		seen[t.Name] = len(deduped)
		deduped = append(deduped, t)
	}
	result = deduped
	return result
}

// namespacedToolName joins namespace and name.
func namespacedToolName(namespace, name string) string {
	return codextool.NamespacedToolName(namespace, name)
}

// customToolDescription builds a description for a custom tool including grammar.
func customToolDescription(tool Tool, grammar string) string {
	parts := []string{}
	if strings.TrimSpace(tool.Description) != "" {
		parts = append(parts, strings.TrimSpace(tool.Description))
	}
	if grammar != "" {
		parts = append(parts, "OpenAI custom tool grammar:\n"+grammar)
	}
	if len(parts) == 0 {
		return "Use this custom tool with its raw freeform input in the input field."
	}
	return strings.Join(parts, "\n\n")
}

func outputToContentBlocks(raw json.RawMessage) []format.CoreContentBlock {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	blocks := contentBlocksFromRaw(raw)
	if len(blocks) > 0 {
		return blocks
	}
	if text := outputToString(raw); text != "" {
		return []format.CoreContentBlock{{Type: "text", Text: text}}
	}
	return nil
}
