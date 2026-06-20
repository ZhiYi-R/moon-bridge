// Package google implements the Google Generative AI (Gemini) ProviderAdapter for MoonBridge.
//
// GeminiProviderAdapter converts between Core format and Gemini REST API DTOs.
// It implements format.ProviderAdapter (non-streaming) and format.ProviderStreamAdapter (streaming).
package google

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"

	"moonbridge/internal/extension/codextool"
	"moonbridge/internal/format"
	"moonbridge/internal/protocol/cache"
)

// ============================================================================
// GeminiProviderAdapter
// ============================================================================

// GeminiProviderAdapter converts Core format requests/responses to/from
// the Google Gemini API format.
//
// Clean room: no dependency on protocol-specific packages beyond google/.
// Only references: config, format, and google types.
type GeminiProviderAdapter struct {
	cfgMaxTokens int
	client       *Client
	hooks        format.CorePluginHooks

	// Cache config and registry (nil = caching disabled).
	cacheCfg *cache.PlanCacheConfig
	registry *cache.MemoryRegistry
	// currentCacheKey tracks the cache key for the current request.
	currentCacheKey string

	// currentModel tracks the model for the current request (used by cache).
	currentModel string

	prevSnapshots map[int]string // candidate index → previous text for delta computation
}

// NewGeminiProviderAdapter creates a new GeminiProviderAdapter.
//
// client is the HTTP client for Gemini API calls. May be nil if the adapter
// is registered for type conversion only (dispatch layer manages the client).
func NewGeminiProviderAdapter(cfgMaxTokens int, client *Client, hooks format.CorePluginHooks, cacheCfg *cache.PlanCacheConfig, registry *cache.MemoryRegistry) *GeminiProviderAdapter {
	return &GeminiProviderAdapter{
		cfgMaxTokens:  cfgMaxTokens,
		client:        client,
		hooks:         hooks.WithDefaults(),
		cacheCfg:      cacheCfg,
		registry:      registry,
		prevSnapshots: make(map[int]string),
	}

}

// ProviderProtocol returns "google-genai".
func (a *GeminiProviderAdapter) ProviderProtocol() string {
	return "google-genai"
}

// =========================================================================
// FromCoreRequest — CoreRequest → *GenerateContentRequest
// =========================================================================

// FromCoreRequest converts a CoreRequest into a *GenerateContentRequest.
//
// Conversion steps:
//  1. Call hooks.MutateCoreRequest (plugin modifications to CoreRequest)
//  2. Map CoreRequest fields to Gemini GenerateContentRequest fields
//  3. System instruction, messages, safety settings, generation config, tools
func (a *GeminiProviderAdapter) FromCoreRequest(ctx context.Context, req *format.CoreRequest) (any, error) {
	if req == nil {
		return nil, fmt.Errorf("google adapter: core request is nil")
	}

	a.currentModel = req.Model

	// Step 1: Allow plugins to mutate the CoreRequest before conversion.
	a.hooks.RewriteMessages(ctx, req)
	a.hooks.MutateCoreRequest(ctx, req)

	// Strip base64 image data from all text content to prevent token waste.
	format.StripContentBlocks(req.System)
	for i := range req.Messages {
		format.StripContentBlocks(req.Messages[i].Content)
	}

	// Step 2: Build the Gemini request.
	geminiReq := &GenerateContentRequest{
		Contents: make([]Content, 0, len(req.Messages)),
	}

	toolUseIDMap := make(map[string]string)

	// System instruction (D-01): CoreRequest.System → Gemini system_instruction
	if len(req.System) > 0 {
		sysContent := a.blocksToContent(req.System, toolUseIDMap)
		if len(sysContent.Parts) > 0 {
			geminiReq.SystemInstruction = &sysContent
		}
	}

	// Messages → Contents with role merging (G-03):
	// Gemini API requires alternating user/model roles, so consecutive messages
	// with the same role (e.g. tool_result after user text) are merged.
	mergedContents := make([]Content, 0, len(req.Messages))
	for _, msg := range req.Messages {
		content := a.blocksToContent(msg.Content, toolUseIDMap)
		// Skip messages with no content parts — they contribute no semantic value
		// and may cause SDK role-alternating contract violations.
		if len(content.Parts) == 0 {
			continue
		}
		content.Role = a.mapRoleToGemini(msg.Role)
		if len(mergedContents) > 0 && mergedContents[len(mergedContents)-1].Role == content.Role {
			mergedContents[len(mergedContents)-1].Parts = append(mergedContents[len(mergedContents)-1].Parts, content.Parts...)
		} else {
			mergedContents = append(mergedContents, content)
		}
	}
	// Ensure first Content has role "user" — Gemini API requires alternating
	// user/model roles starting with user. Insert a placeholder if needed.
	if len(mergedContents) > 0 && mergedContents[0].Role == "model" {
		mergedContents = append(
			[]Content{{Role: "user", Parts: []Part{{Text: "_"}}}},
			mergedContents...,
		)
	}
	geminiReq.Contents = mergedContents

	// SafetySettings (D-02): CoreRequest.SafetySettings map → Gemini []SafetySetting
	if len(req.SafetySettings) > 0 {
		geminiReq.SafetySettings = a.toSafetySettings(req.SafetySettings)
	}

	// GenerationConfig (D-02): CoreRequest.GenerationConfig map + direct fields
	geminiReq.GenerationConfig = a.toGenerationConfig(req)

	// Tools (D-03): CoreRequest.Tools → Gemini []Tool with FunctionDeclarations
	if len(req.Tools) > 0 {
		geminiReq.Tools = make([]Tool, 0, len(req.Tools))
		for _, t := range req.Tools {
			geminiReq.Tools = append(geminiReq.Tools, Tool{
				FunctionDeclarations: []FunctionDeclaration{
					{
						Name:        t.Name,
						Description: t.Description,
						Parameters:  t.InputSchema,
					},
				},
			})
		}
	}

	// Cache integration — look up or create CachedContent.
	a.prepareCache(ctx, geminiReq)

	a.currentModel = ""

	return geminiReq, nil
}

// =========================================================================
// ToCoreResponse — *GenerateContentResponse → *CoreResponse
// =========================================================================

// ToCoreResponse converts a *GenerateContentResponse into a *CoreResponse.
//
// The first candidate's content becomes a single assistant message.
// Token usage is extracted from UsageMetadata.
func (a *GeminiProviderAdapter) ToCoreResponse(ctx context.Context, resp any) (*format.CoreResponse, error) {
	return a.ToCoreResponseWithRequest(ctx, nil, resp)
}

func (a *GeminiProviderAdapter) ToCoreResponseWithRequest(ctx context.Context, req *format.CoreRequest, resp any) (*format.CoreResponse, error) {
	geminiResp, ok := resp.(*GenerateContentResponse)
	if !ok {
		return nil, fmt.Errorf("google adapter: expected *GenerateContentResponse, got %T", resp)
	}
	toolMap := codextool.DecodeToolMapFromExtensions(nil)
	if req != nil {
		toolMap = codextool.DecodeToolMapFromExtensions(req.Extensions)
	}

	// Map status from candidates.
	status := "completed"
	var stopReason string
	var coreContent []format.CoreContentBlock

	if len(geminiResp.Candidates) > 0 {
		candidate := geminiResp.Candidates[0]
		stopReason = a.mapFinishReason(candidate.FinishReason)
		coreContent = a.fromParts(candidate.Content.Parts)
		for i := range coreContent {
			codextool.DecodeCoreToolBlockFromProvider(&coreContent[i], toolMap)
		}
		switch candidate.FinishReason {
		case "MAX_TOKENS":
			status = "incomplete"
		case "SAFETY", "RECITATION", "OTHER":
			status = "failed"
		}
	}

	coreResp := &format.CoreResponse{
		Status: status,
		Messages: []format.CoreMessage{
			{
				Role:    "assistant",
				Content: coreContent,
			},
		},
		StopReason: stopReason,
	}

	if geminiResp.UsageMetadata != nil {
		coreResp.Usage = format.CoreUsage{
			InputTokens:  geminiResp.UsageMetadata.PromptTokenCount,
			OutputTokens: geminiResp.UsageMetadata.CandidatesTokenCount,
			TotalTokens:  geminiResp.UsageMetadata.TotalTokenCount,
			// CachedInputTokens from the cached content count; Google includes
			// cached tokens in prompt_token_count (total), and reports them
			// separately here.
			CachedInputTokens: geminiResp.UsageMetadata.CachedContentTokenCount,
		}
	}

	// Update cache registry from response metadata.
	a.prepareCacheResponse(geminiResp, a.currentCacheKey)
	a.currentCacheKey = ""

	return coreResp, nil
}

// =========================================================================
// bufferStreamEvent buffers raw GenerateContentResponse for trace capture.
func (a *GeminiProviderAdapter) bufferStreamEvent(ev GenerateContentResponse) {
	// No-op: per-stream buffer is captured by goroutine closure.
}

// StreamBuffer returns the buffered stream events for trace capture.
func (a *GeminiProviderAdapter) StreamBuffer() []GenerateContentResponse {
	// Deprecated: use StreamResult.StreamBuffer instead.
	return nil
}

// ToCoreStream — <-chan GenerateContentResponse → <-chan CoreStreamEvent
// =========================================================================

// ToCoreStream consumes a channel of GenerateContentResponse (from a Gemini
// streaming endpoint) and returns a channel of CoreStreamEvent.
//
// Gemini streaming returns full candidate snapshots per event, not deltas.
// The adapter computes text deltas by comparing each chunk against the previous
// snapshot for each candidate index.
//
// Emitted event sequence per candidate:
//   - core.content_block.started (first chunk for a candidate)
//   - core.text.delta (each subsequent chunk with new text)
//   - core.content_block.done (chunk with FinishReason set)
//   - core.completed (final chunk with UsageMetadata)
func (a *GeminiProviderAdapter) ToCoreStream(ctx context.Context, src any) (*format.StreamResult, error) {
	ch, ok := src.(<-chan GenerateContentResponse)
	if !ok {
		return nil, fmt.Errorf("google adapter: expected <-chan GenerateContentResponse, got %T", src)
	}

	events := make(chan format.CoreStreamEvent, 64)

	// Per-stream buffer — local to this call, not shared across concurrent requests.
	var buf []GenerateContentResponse
	var bufMu sync.Mutex
	bufReady := make(chan struct{})

	go func() {
		defer close(events)
		defer close(bufReady)

		// Per-candidate state for delta computation.
		type candidateState struct {
			started    bool
			prevText   string
			blockIndex int // monotonically increasing content block index
		}
		candidates := make(map[int]*candidateState)
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
				// Append to local per-stream buffer with size cap.
				bufMu.Lock()
				if len(buf) < 1024 {
					buf = append(buf, chunk)
				}
				bufMu.Unlock()
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

				// Process each candidate in the chunk.
				for _, candidate := range chunk.Candidates {
					state := candidates[candidate.Index]
					if state == nil {
						state = &candidateState{blockIndex: len(candidates) * 2}
						candidates[candidate.Index] = state
					}

					// Extract current full text from this candidate's content.
					currentText := a.extractText(candidate.Content.Parts)

					// Emit content_block.started on first appearance.
					if !state.started {
						state.started = true
						state.prevText = ""

						ci := candidate.Index
						emit(format.CoreStreamEvent{
							Type:        format.CoreContentBlockStarted,
							Index:       state.blockIndex,
							ChoiceIndex: &ci,
							ContentBlock: &format.CoreContentBlock{
								Type: "text",
							},
						})
					}

					// Compute text delta.
					delta := a.computeDelta(state.prevText, currentText)
					if delta != "" {
						ci := candidate.Index
						emit(format.CoreStreamEvent{
							Type:        format.CoreTextDelta,
							Index:       state.blockIndex,
							Delta:       delta,
							ChoiceIndex: &ci,
						})
					}

					state.prevText = currentText

					// Emit content_block.done if FinishReason is set.
					if candidate.FinishReason != "" {
						stopReason := a.mapFinishReason(candidate.FinishReason)
						ci := candidate.Index
						emit(format.CoreStreamEvent{
							Type:        format.CoreContentBlockDone,
							Index:       state.blockIndex,
							StopReason:  stopReason,
							ChoiceIndex: &ci,
						})
					}
				}

				// Track usage from the last chunk.
				if chunk.UsageMetadata != nil {
					finalUsage = &format.CoreUsage{
						InputTokens:       chunk.UsageMetadata.PromptTokenCount,
						OutputTokens:      chunk.UsageMetadata.CandidatesTokenCount,
						TotalTokens:       chunk.UsageMetadata.TotalTokenCount,
						CachedInputTokens: chunk.UsageMetadata.CachedContentTokenCount,
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

// =========================================================================
// Helpers: Core → Gemini
// =========================================================================

// blocksToContent converts []CoreContentBlock to Content (Gemini format).
func (a *GeminiProviderAdapter) blocksToContent(blocks []format.CoreContentBlock, toolUseIDMap map[string]string) Content {
	parts := make([]Part, 0, len(blocks))
	for _, b := range blocks {
		switch b.Type {
		case "text":
			parts = append(parts, Part{Text: b.Text})
		case "image":
			parts = append(parts, Part{
				InlineData: &Blob{
					MimeType: b.MediaType,
					Data:     b.ImageData,
				},
			})
		case "tool_use":
			// Store ToolUseID -> ToolName mapping for later FunctionResponse resolution (G-01).
			if toolUseIDMap != nil {
				toolUseIDMap[b.ToolUseID] = b.ToolName
			}
			parts = append(parts, Part{
				FunctionCall: &FunctionCall{
					Name: b.ToolName,
					Args: b.ToolInput,
				},
			})
		case "tool_result":
			// Look up the function name from ToolUseID (G-01).
			funcName := b.ToolUseID
			if toolUseIDMap != nil {
				if fn, ok := toolUseIDMap[b.ToolUseID]; ok {
					funcName = fn
				}
			}

			// Combine tool result content into a single text for the response.
			var respText string
			if len(b.ToolResultContent) > 0 {
				for _, tc := range b.ToolResultContent {
					respText += tc.Text
				}
			}
			respMap := map[string]any{"response": respText}
			respRaw, _ := marshalRaw(respMap)
			parts = append(parts, Part{
				FunctionResponse: &FunctionResponse{
					Name:     funcName,
					Response: respRaw,
				},
			})
		case "reasoning":
			// Gemini does not natively support a "reasoning" content type, so
			// convert reasoning text to a regular text Part to retain the content (G-06).
			if b.ReasoningText != "" {
				parts = append(parts, Part{Text: b.ReasoningText})
			}
		default:
			// Fallback: treat unknown types as text.
			if b.Text != "" {
				parts = append(parts, Part{Text: b.Text})
			}
		}
	}
	return Content{Parts: parts}
}

// mapRoleToGemini converts a Core role string to a Gemini role string.
// Core "assistant" → Gemini "model". Other roles ("user", "system") → Gemini "user".
func (a *GeminiProviderAdapter) mapRoleToGemini(role string) string {
	switch role {
	case "assistant":
		return "model"
	case "user", "system":
		return "user"
	default:
		return "user"
	}
}

// toSafetySettings converts CoreRequest.SafetySettings map to []SafetySetting.
func (a *GeminiProviderAdapter) toSafetySettings(ss map[string]any) []SafetySetting {
	result := make([]SafetySetting, 0, len(ss))
	for category, threshold := range ss {
		thresholdStr, ok := threshold.(string)
		if !ok {
			continue
		}
		result = append(result, SafetySetting{
			Category:  category,
			Threshold: thresholdStr,
		})
	}
	return result
}

// toGenerationConfig builds a GenerationConfig from CoreRequest fields.
func (a *GeminiProviderAdapter) toGenerationConfig(req *format.CoreRequest) *GenerationConfig {
	// Start with explicit fields from CoreRequest.
	gc := &GenerationConfig{
		Temperature:   req.Temperature,
		TopP:          req.TopP,
		StopSequences: req.StopSequences,
	}

	if req.MaxTokens > 0 {
		gc.MaxOutputTokens = req.MaxTokens
	} else if a.cfgMaxTokens > 0 {
		gc.MaxOutputTokens = a.cfgMaxTokens
	}

	// Apply GenerationConfig map overrides (D-02).
	if len(req.GenerationConfig) > 0 {
		a.applyGenerationConfigMap(gc, req.GenerationConfig)
	}

	return gc
}

// applyGenerationConfigMap applies map entries to a GenerationConfig struct.
func (a *GeminiProviderAdapter) applyGenerationConfigMap(gc *GenerationConfig, cfg map[string]any) {
	for k, v := range cfg {
		switch k {
		case "temperature":
			if f, ok := toFloat64(v); ok {
				gc.Temperature = &f
			}
		case "topP":
			if f, ok := toFloat64(v); ok {
				gc.TopP = &f
			}
		case "topK":
			if f, ok := toFloat64(v); ok {
				gc.TopK = &f
			}
		case "maxOutputTokens":
			if i, ok := toInt(v); ok {
				gc.MaxOutputTokens = i
			}
		case "stopSequences":
			if ss, ok := toStringSlice(v); ok {
				gc.StopSequences = ss
			}
		case "responseMimeType":
			if s, ok := v.(string); ok {
				gc.ResponseMimeType = s
			}
		case "candidateCount":
			if i, ok := toInt(v); ok {
				gc.CandidateCount = i
			}
		}
	}
}

// =========================================================================
// Helpers: Gemini → Core
// =========================================================================

// fromParts converts Gemini Parts to []CoreContentBlock.
func (a *GeminiProviderAdapter) fromParts(parts []Part) []format.CoreContentBlock {
	result := make([]format.CoreContentBlock, 0, len(parts))
	funcCallSeq := make(map[string]int)
	callIDStacks := make(map[string][]string) // per-function-name call ID stack for FunctionResponse matching
	for _, p := range parts {
		block, stacks := a.fromPartWithSeq(p, funcCallSeq, callIDStacks)
		if stacks != nil {
			callIDStacks = stacks
		}
		result = append(result, block)
	}
	return result
}

// fromPartWithSeq converts a single Gemini Part to CoreContentBlock.
// Returns the block and optionally an updated callIDStacks for FunctionCall tracking.
func (a *GeminiProviderAdapter) fromPartWithSeq(p Part, funcCallSeq map[string]int, callIDStacks map[string][]string) (format.CoreContentBlock, map[string][]string) {
	switch {
	case p.Text != "":
		return format.CoreContentBlock{
			Type: "text",
			Text: p.Text,
		}, nil
	case p.FunctionCall != nil:
		callName := p.FunctionCall.Name
		funcCallSeq[callName]++
		callID := callName + "__call_" + strconv.Itoa(funcCallSeq[callName])
		callIDStacks[callName] = append(callIDStacks[callName], callID)
		return format.CoreContentBlock{
			Type:      "tool_use",
			ToolUseID: callID,
			ToolName:  callName,
			ToolInput: p.FunctionCall.Args,
		}, callIDStacks
	case p.FunctionResponse != nil:
		respName := p.FunctionResponse.Name
		callID := ""
		if stack := callIDStacks[respName]; len(stack) > 0 {
			callID = stack[len(stack)-1]
		} else {
			callID = respName + "__call_1"
		}
		return format.CoreContentBlock{
			Type:      "tool_result",
			ToolUseID: callID,
		}, nil
	case p.InlineData != nil:
		return format.CoreContentBlock{
			Type:      "image",
			ImageData: p.InlineData.Data,
			MediaType: p.InlineData.MimeType,
		}, nil
	default:
		return format.CoreContentBlock{
			Type: "text",
		}, nil
	}
}

// mapFinishReason maps Gemini finish_reason to Core stop_reason.
func (a *GeminiProviderAdapter) mapFinishReason(reason string) string {
	switch reason {
	case "STOP":
		return "end_turn"
	case "MAX_TOKENS":
		return "max_tokens"
	case "SAFETY":
		return "content_filter"
	case "RECITATION":
		return "content_filter"
	case "OTHER":
		return "error"
	case "FINISH_REASON_UNSPECIFIED":
		return ""
	default:
		return reason
	}
}

// =========================================================================
// Delta computation helpers
// =========================================================================

// extractText concatenates all text parts from a Parts list.
func (a *GeminiProviderAdapter) extractText(parts []Part) string {
	var text string
	for _, p := range parts {
		text += p.Text
	}
	return text
}

// computeDelta returns the text difference between prev and current.
// Since Gemini returns full snapshots, we just return the new suffix.
// If prev is empty or current is shorter, returns current.
func (a *GeminiProviderAdapter) computeDelta(prev, current string) string {
	if len(current) <= len(prev) {
		return current
	}
	return current[len(prev):]
}

// =========================================================================
// Type conversion helpers
// =========================================================================

// toFloat64 attempts to convert an any value to float64.
func toFloat64(v any) (float64, bool) {
	switch val := v.(type) {
	case float64:
		return val, true
	case int:
		return float64(val), true
	case int64:
		return float64(val), true
	case json.Number:
		f, err := val.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

// toInt attempts to convert an any value to int.
func toInt(v any) (int, bool) {
	switch val := v.(type) {
	case int:
		return val, true
	case float64:
		return int(val), true
	case int64:
		return int(val), true
	case json.Number:
		i, err := val.Int64()
		return int(i), err == nil
	default:
		return 0, false
	}
}

// toStringSlice attempts to convert an any value to []string.
func toStringSlice(v any) ([]string, bool) {
	switch val := v.(type) {
	case []string:
		return val, true
	case []any:
		result := make([]string, 0, len(val))
		for _, item := range val {
			if s, ok := item.(string); ok {
				result = append(result, s)
			}
		}
		return result, len(result) > 0
	default:
		return nil, false
	}
}

// marshalRaw marshals an any value to json.RawMessage.
func marshalRaw(v any) (json.RawMessage, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(data), nil
}
