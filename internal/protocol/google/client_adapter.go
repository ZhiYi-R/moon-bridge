// Package google implements the Google Generative AI (Gemini) client adapter for MoonBridge.
//
// GeminiClientAdapter implements format.ClientAdapter and format.ClientStreamAdapter,
// enabling MoonBridge to accept inbound Gemini-format requests at /v1beta/models/
// and convert them through the Core intermediate format to upstream providers.
//
// Clean room design: no imports from moonbridge/internal/extension/codextool/
// or any protocol-specific packages other than the Gemini DTOs defined in this package.
package google

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"moonbridge/internal/format"
)

// ============================================================================
// GeminiClientAdapter
// ============================================================================

// GeminiClientAdapter converts inbound Gemini API requests to/from the Core
// intermediate format.
//
// It implements the inbound (client) side of the bridge:
//   - ClientAdapter:       DecodeRequest / ToCoreRequest / FromCoreResponse
//   - ClientStreamAdapter: FromCoreStream
type GeminiClientAdapter struct {
	hooks format.CorePluginHooks
}

// NewGeminiClientAdapter creates a new GeminiClientAdapter.
func NewGeminiClientAdapter(hooks format.CorePluginHooks) *GeminiClientAdapter {
	return &GeminiClientAdapter{
		hooks: hooks.WithDefaults(),
	}
}

// ClientProtocol returns the inbound protocol identifier.
func (a *GeminiClientAdapter) ClientProtocol() string {
	return "google-genai"
}

// ============================================================================
// DecodeRequest — raw HTTP body + pathModel + endpoint → *GenerateContentRequest
// ============================================================================

// DecodeRequest unmarshals the raw HTTP body into a Gemini GenerateContentRequest.
//
// Unlike other protocols, Gemini encodes the model in the URL path and determines
// streaming from the endpoint suffix:
//   - pathModel: extracted from /v1beta/models/{model}:generateContent
//   - endpoint:  the URL path; streaming when suffix is ":streamGenerateContent"
//
// The model from the path is injected into the request body if the body does not
// already contain one, so that ToCoreRequest can always read req.Model.
func (a *GeminiClientAdapter) DecodeRequest(raw []byte, pathModel, endpoint string) (any, bool, error) {
	if len(raw) == 0 {
		return nil, false, fmt.Errorf("empty request body")
	}

	// Determine streaming from the endpoint suffix.
	isStream := strings.HasSuffix(endpoint, ":streamGenerateContent")

	var req GenerateContentRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, false, fmt.Errorf("decode gemini request: %w", err)
	}

	// Inject model from URL path if not already in the body.
	// Gemini APIs typically send the model in the URL, not the body,
	// but some SDKs may include it in both places.
	if req.Contents == nil {
		req.Contents = []Content{}
	}

	return &req, isStream, nil
}

// extractModelFromEndpoint parses the model name from a Gemini URL path.
//
// Input:  /v1beta/models/gemini-2.0-flash:generateContent
// Output: "gemini-2.0-flash"
//
// Input:  /v1beta/models/gemini-2.0-flash:streamGenerateContent
// Output: "gemini-2.0-flash"
func extractModelFromEndpoint(endpoint string) string {
	// Expected pattern: /v1beta/models/{model}:generateContent
	// or /v1beta/models/{model}:streamGenerateContent
	const prefix = "/v1beta/models/"
	idx := strings.Index(endpoint, prefix)
	if idx < 0 {
		return ""
	}
	rest := endpoint[idx+len(prefix):]
	// Split on ':' to separate model name from action.
	if colonIdx := strings.Index(rest, ":"); colonIdx > 0 {
		return rest[:colonIdx]
	}
	return rest
}

// ============================================================================
// ToCoreRequest — *GenerateContentRequest → *CoreRequest
// ============================================================================

// ToCoreRequest converts an inbound Gemini GenerateContentRequest into a CoreRequest.
//
// Supported mappings:
//   - Contents → CoreRequest.Messages (with role mapping)
//   - SystemInstruction → CoreRequest.System
//   - Tools → CoreRequest.Tools
//   - SafetySettings → CoreRequest.SafetySettings
//   - GenerationConfig → Temperature, TopP, TopK, MaxTokens, StopSequences
func (a *GeminiClientAdapter) ToCoreRequest(ctx context.Context, req any) (*format.CoreRequest, error) {
	geminiReq, ok := req.(*GenerateContentRequest)
	if !ok {
		direct, ok2 := req.(GenerateContentRequest)
		if !ok2 {
			return nil, fmt.Errorf("unexpected request type %T; expected *GenerateContentRequest", req)
		}
		geminiReq = &direct
	}

	coreReq := &format.CoreRequest{
		Model:    "",
		Messages: make([]format.CoreMessage, 0, len(geminiReq.Contents)),
	}

	// System instruction.
	if geminiReq.SystemInstruction != nil {
		coreReq.System = fromGeminiParts(geminiReq.SystemInstruction.Parts)
	}

	// Convert contents → messages.
	// Google Gemini's FunctionCall has no ID field — matching depends on
	// the function name. We do a two-pass approach:
	//   1. Collect all FunctionCall names and assign unique IDs.
	//   2. Convert parts, applying the ID mapping to both tool_use and
	//      tool_result blocks so the pair stays linked.
	funcCallIDs := make(map[string]string) // function name → assigned tool_use ID
	funcCallIdx := 0
	for _, content := range geminiReq.Contents {
		for _, p := range content.Parts {
			if p.FunctionCall != nil {
				id := fmt.Sprintf("toolu_gemini_%s_%d", p.FunctionCall.Name, funcCallIdx)
				funcCallIDs[p.FunctionCall.Name] = id
				funcCallIdx++
			}
		}
	}
	for _, content := range geminiReq.Contents {
		role := mapGeminiRoleToCore(content.Role)
		blocks := fromGeminiParts(content.Parts)
		// Patch tool_use / tool_result IDs.
		for i := range blocks {
			switch blocks[i].Type {
			case "tool_use":
				if id, ok := funcCallIDs[blocks[i].ToolName]; ok {
					blocks[i].ToolUseID = id
				}
			case "tool_result":
				if id, ok := funcCallIDs[blocks[i].ToolUseID]; ok {
					blocks[i].ToolUseID = id
				}
			}
		}
		coreReq.Messages = append(coreReq.Messages, format.CoreMessage{
			Role:    role,
			Content: blocks,
		})
	}

	// Tools.
	if len(geminiReq.Tools) > 0 {
		coreReq.Tools = make([]format.CoreTool, 0, len(geminiReq.Tools))
		for _, t := range geminiReq.Tools {
			for _, fd := range t.FunctionDeclarations {
				coreReq.Tools = append(coreReq.Tools, format.CoreTool{
					Name:        fd.Name,
					Description: fd.Description,
					InputSchema: normalizeSchemaTypes(fd.Parameters),
				})
			}
		}
	}

	// Safety settings.
	if len(geminiReq.SafetySettings) > 0 {
		coreReq.SafetySettings = make(map[string]any, len(geminiReq.SafetySettings))
		for _, s := range geminiReq.SafetySettings {
			coreReq.SafetySettings[s.Category] = s.Threshold
		}
	}

	// Generation config.
	if geminiReq.GenerationConfig != nil {
		gc := geminiReq.GenerationConfig
		coreReq.Temperature = gc.Temperature
		coreReq.TopP = gc.TopP
		if gc.TopK != nil {
			k := int(*gc.TopK)
			coreReq.TopK = &k
		}
		if gc.MaxOutputTokens > 0 {
			coreReq.MaxTokens = gc.MaxOutputTokens
		}
		coreReq.StopSequences = gc.StopSequences
	}

	// Apply hooks.
	if injected := a.hooks.InjectTools(format.ContextWithCoreRequest(ctx, coreReq)); len(injected) > 0 {
		coreReq.Tools = append(coreReq.Tools, injected...)
	}
	a.hooks.MutateCoreRequest(ctx, coreReq)

	return coreReq, nil
}

// ============================================================================
// FromCoreResponse — *CoreResponse → *GenerateContentResponse
// ============================================================================

// FromCoreResponse converts a CoreResponse back into a Gemini GenerateContentResponse.
func (a *GeminiClientAdapter) FromCoreResponse(ctx context.Context, resp *format.CoreResponse) (any, error) {
	if resp == nil {
		return nil, fmt.Errorf("core response is nil")
	}

	a.hooks.PostProcessCoreResponse(ctx, resp)

	geminiResp := &GenerateContentResponse{
		Candidates: make([]Candidate, 0, len(resp.Messages)),
	}

	// Convert assistant messages to candidates.
	for _, msg := range resp.Messages {
		if msg.Role != "assistant" {
			continue
		}
		parts := toGeminiParts(msg.Content)
		finishReason := mapCoreStopReasonToGemini(resp.StopReason, resp.Status)
		candidate := Candidate{
			Index:        0,
			Content:      Content{Role: "model", Parts: parts},
			FinishReason: finishReason,
		}
		geminiResp.Candidates = append(geminiResp.Candidates, candidate)
	}

	// Map usage.
	if resp.Usage.InputTokens > 0 || resp.Usage.OutputTokens > 0 {
		geminiResp.UsageMetadata = &UsageMetadata{
			PromptTokenCount:        resp.Usage.InputTokens,
			CandidatesTokenCount:    resp.Usage.OutputTokens,
			TotalTokenCount:         resp.Usage.InputTokens + resp.Usage.OutputTokens,
			CachedContentTokenCount: resp.Usage.CachedInputTokens,
		}
	}

	return geminiResp, nil
}

// ============================================================================
// FromCoreStream — CoreStreamEvent channel → Gemini SSE stream
// ============================================================================

// FromCoreStream consumes a channel of CoreStreamEvent and produces a Gemini
// SSE stream result that implements SSEStream.
//
// Gemini SSE format uses unnamed events — only "data:" lines with JSON payloads.
// No [DONE] marker; the stream ends by closing the connection.
//
// The result channel is closed when the input channel is exhausted.
func (a *GeminiClientAdapter) FromCoreStream(ctx context.Context, req *format.CoreRequest, events <-chan format.CoreStreamEvent) (any, error) {
	out := make(chan GenerateContentResponse)
	bufReady := make(chan struct{})

	var buf []GenerateContentResponse
	var bufMu sync.Mutex

	go func() {
		defer close(out)
		defer close(bufReady)
		a.streamLoop(ctx, req, events, out, &buf, &bufMu)
	}()

	return &GeminiStreamResult{
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
func (a *GeminiClientAdapter) streamLoop(
	ctx context.Context,
	coreReq *format.CoreRequest,
	events <-chan format.CoreStreamEvent,
	out chan<- GenerateContentResponse,
	buf *[]GenerateContentResponse,
	bufMu *sync.Mutex,
) {
	send := func(ev GenerateContentResponse) {
		bufMu.Lock()
		if len(*buf) < 1024 {
			*buf = append(*buf, ev)
		}
		bufMu.Unlock()
		out <- ev
	}

	// State tracked during streaming.
	var currentText string
	var currentParts []Part
	var accumulatedUsage *UsageMetadata
	var hasContent bool
	blockIndex := 0
	textBlockIndex := -1 // index of the current text block within parts

	for event := range events {
		// Let hooks skip events.
		if a.hooks.OnStreamEvent(ctx, event) {
			continue
		}

		switch event.Type {
		// ==================================================================
		// Created → no direct Gemini equivalent; reset state.
		// ==================================================================
		case format.CoreEventCreated:
			currentText = ""
			currentParts = nil
			hasContent = false
			blockIndex = 0
			textBlockIndex = -1

		// ==================================================================
		// Content block started → prepare for new text/tool block
		// ==================================================================
		case format.CoreContentBlockStarted:
			if event.ContentBlock == nil {
				continue
			}
			switch event.ContentBlock.Type {
			case "text", "reasoning":
				textBlockIndex = blockIndex
			case "tool_use":
				currentParts = append(currentParts, Part{
					FunctionCall: &FunctionCall{
						Name: event.ContentBlock.ToolName,
						Args: event.ContentBlock.ToolInput,
					},
				})
				blockIndex++
			}
			hasContent = true

		// ==================================================================
		// Text delta → accumulate text content
		// ==================================================================
		case format.CoreTextDelta:
			currentText += event.Delta

		// ==================================================================
		// Text done → flush accumulated text to parts
		// ==================================================================
		case format.CoreTextDone:
			if currentText != "" && textBlockIndex >= 0 {
				currentParts = append(currentParts, Part{Text: currentText})
				textBlockIndex = -1
				blockIndex++
			} else if textBlockIndex >= 0 {
				// Emit text even if empty to keep candidate structure valid
				// when there is no text content but we still had a content block start.
				blockIndex++
			}
			currentText = ""

		// ==================================================================
		// Tool call args delta → accumulate into the current FunctionCall
		// ==================================================================
		case format.CoreToolCallArgsDelta:
			if len(currentParts) > 0 {
				last := &currentParts[len(currentParts)-1]
				if last.FunctionCall != nil {
					if last.FunctionCall.Args == nil {
						last.FunctionCall.Args = json.RawMessage(event.Delta)
					} else {
						last.FunctionCall.Args = json.RawMessage(string(last.FunctionCall.Args) + event.Delta)
					}
				}
			}

		// ==================================================================
		// Tool call args done → finalize
		// ==================================================================
		case format.CoreToolCallArgsDone:
			// No special handling needed; the args are accumulated via delta.

		// ==================================================================
		// Content block done → finalize current block
		// ==================================================================
		case format.CoreContentBlockDone:
			if currentText != "" && textBlockIndex >= 0 {
				currentParts = append(currentParts, Part{Text: currentText})
				currentText = ""
				textBlockIndex = -1
				blockIndex++
			}

		// ==================================================================
		// Completed → emit final snapshot
		// ==================================================================
		case format.CoreEventCompleted:
			if currentText != "" {
				currentParts = append(currentParts, Part{Text: currentText})
				currentText = ""
			}
			if event.Usage != nil {
				accumulatedUsage = &UsageMetadata{
					PromptTokenCount:        event.Usage.InputTokens,
					CandidatesTokenCount:    event.Usage.OutputTokens,
					TotalTokenCount:         event.Usage.InputTokens + event.Usage.OutputTokens,
					CachedContentTokenCount: event.Usage.CachedInputTokens,
				}
			}
			stopReason := mapCoreStopReasonToGemini(event.StopReason, event.Status)
			if !hasContent && len(currentParts) == 0 {
				currentParts = append(currentParts, Part{Text: ""})
			}
			send(GenerateContentResponse{
				Candidates: []Candidate{
					{
						Index:        0,
						Content:      Content{Role: "model", Parts: currentParts},
						FinishReason: stopReason,
					},
				},
				UsageMetadata: accumulatedUsage,
			})

		// ==================================================================
		// Incomplete → emit with MAX_TOKENS
		// ==================================================================
		case format.CoreEventIncomplete:
			if currentText != "" {
				currentParts = append(currentParts, Part{Text: currentText})
				currentText = ""
			}
			if event.Usage != nil {
				accumulatedUsage = &UsageMetadata{
					PromptTokenCount:        event.Usage.InputTokens,
					CandidatesTokenCount:    event.Usage.OutputTokens,
					TotalTokenCount:         event.Usage.InputTokens + event.Usage.OutputTokens,
					CachedContentTokenCount: event.Usage.CachedInputTokens,
				}
			}
			if !hasContent && len(currentParts) == 0 {
				currentParts = append(currentParts, Part{Text: ""})
			}
			send(GenerateContentResponse{
				Candidates: []Candidate{
					{
						Index:        0,
						Content:      Content{Role: "model", Parts: currentParts},
						FinishReason: "MAX_TOKENS",
					},
				},
				UsageMetadata: accumulatedUsage,
			})

		// ==================================================================
		// Failed → emit with SAFETY or error
		// ==================================================================
		case format.CoreEventFailed:
			finishReason := "OTHER"
			if event.Error != nil {
				switch event.Error.Type {
				case "content_filter":
					finishReason = "SAFETY"
				default:
					finishReason = "OTHER"
				}
			}
			if !hasContent && len(currentParts) == 0 {
				currentParts = append(currentParts, Part{Text: ""})
			}
			send(GenerateContentResponse{
				Candidates: []Candidate{
					{
						Index:        0,
						Content:      Content{Role: "model", Parts: currentParts},
						FinishReason: finishReason,
					},
				},
				UsageMetadata: accumulatedUsage,
			})

		// ==================================================================
		// Ping → no Gemini equivalent; skip
		// ==================================================================
		case format.CorePing:
			// No Gemini equivalent.

		// ==================================================================
		// Misc events → skip
		// ==================================================================
		case format.CoreEventInProgress, format.CoreItemAdded, format.CoreItemDone:
			// No Gemini equivalent.
		}
	}

	// Notify stream completion hook.
	a.hooks.OnStreamComplete(ctx, coreReq.Model, currentText)
}

// ============================================================================
// GeminiStreamResult
// ============================================================================

// GeminiStreamResult wraps the Gemini stream channel with per-stream buffer
// access and SSEFrame conversion.
type GeminiStreamResult struct {
	ch  <-chan GenerateContentResponse
	buf func() []any
}

// Chan returns the underlying channel of GenerateContentResponse.
func (r *GeminiStreamResult) Chan() <-chan GenerateContentResponse {
	return r.ch
}

// Frames returns a channel of protocol-agnostic SSE frames.
//
// Gemini SSE uses unnamed events (no "event:" line), just JSON payload on
// "data:" lines. No [DONE] marker — the stream simply ends.
func (r *GeminiStreamResult) Frames() <-chan format.SSEFrame {
	out := make(chan format.SSEFrame)
	go func() {
		defer close(out)
		for ev := range r.ch {
			out <- format.SSEFrame{
				Event: "", // Gemini has no event names
				Data:  ev,
			}
		}
	}()
	return out
}

// Buffer returns the captured stream events for post-stream processing.
func (r *GeminiStreamResult) Buffer() []any {
	if r.buf == nil {
		return nil
	}
	return r.buf()
}

// ============================================================================
// Content block conversion helpers
// ============================================================================

// fromGeminiParts converts Gemini Parts to CoreContentBlock slice.
func fromGeminiParts(parts []Part) []format.CoreContentBlock {
	result := make([]format.CoreContentBlock, 0, len(parts))
	for _, p := range parts {
		result = append(result, fromGeminiPart(p))
	}
	return result
}

// fromGeminiPart converts a single Gemini Part to CoreContentBlock.
func fromGeminiPart(p Part) format.CoreContentBlock {
	switch {
	case p.Text != "":
		return format.CoreContentBlock{
			Type: "text",
			Text: p.Text,
		}
	case p.FunctionCall != nil:
		return format.CoreContentBlock{
			Type:      "tool_use",
			ToolName:  p.FunctionCall.Name,
			ToolInput: p.FunctionCall.Args,
		}
	case p.FunctionResponse != nil:
		// ToolUseID is set to the function name as a placeholder; the
		// caller (ToCoreRequest) replaces it with the corresponding
		// tool_use ID via the funcCallIDs mapping.
		return format.CoreContentBlock{
			Type:      "tool_result",
			ToolUseID: p.FunctionResponse.Name,
			ToolResultContent: []format.CoreContentBlock{
				{Type: "text", Text: string(p.FunctionResponse.Response)},
			},
		}
	case p.InlineData != nil:
		return format.CoreContentBlock{
			Type:      "image",
			ImageData: p.InlineData.Data,
			MediaType: p.InlineData.MimeType,
		}
	default:
		return format.CoreContentBlock{Type: "text"}
	}
}

// toGeminiParts converts CoreContentBlock slice to Gemini Parts.
func toGeminiParts(blocks []format.CoreContentBlock) []Part {
	parts := make([]Part, 0, len(blocks))
	for _, b := range blocks {
		switch b.Type {
		case "text":
			parts = append(parts, Part{Text: b.Text})
		case "reasoning":
			if b.ReasoningText != "" {
				parts = append(parts, Part{Text: b.ReasoningText})
			}
		case "image":
			parts = append(parts, Part{
				InlineData: &Blob{
					MimeType: b.MediaType,
					Data:     b.ImageData,
				},
			})
		case "tool_use":
			parts = append(parts, Part{
				FunctionCall: &FunctionCall{
					Name: b.ToolName,
					Args: b.ToolInput,
				},
			})
		case "tool_result":
			parts = append(parts, Part{
				FunctionResponse: &FunctionResponse{
					Name: b.ToolUseID,
				},
			})
		default:
			if b.Text != "" {
				parts = append(parts, Part{Text: b.Text})
			}
		}
	}
	return parts
}

// mapGeminiRoleToCore converts a Gemini role to a Core role.
// Gemini "model" → Core "assistant". Everything else → Core "user".
// normalizeSchemaTypes recursively converts uppercase JSON Schema type values
// (e.g. "STRING", "NUMBER", "BOOLEAN", "ARRAY", "OBJECT", "INTEGER") to
// lowercase. The Google GenAI SDK uses uppercase types internally, but the
// OpenAI Chat / DeepSeek API expects lowercase.
func normalizeSchemaTypes(schema map[string]any) map[string]any {
	out := make(map[string]any, len(schema))
	for k, v := range schema {
		switch k {
		case "type":
			if s, ok := v.(string); ok {
				out[k] = strings.ToLower(s)
			} else {
				out[k] = v
			}
		case "properties", "definitions", "patternProperties":
			if m, ok := v.(map[string]any); ok {
				props := make(map[string]any, len(m))
				for pk, pv := range m {
					if pm, ok2 := pv.(map[string]any); ok2 {
						props[pk] = normalizeSchemaTypes(pm)
					} else {
						props[pk] = pv
					}
				}
				out[k] = props
			} else {
				out[k] = v
			}
		case "items", "additionalProperties", "allOf", "anyOf", "oneOf", "not":
			if m, ok := v.(map[string]any); ok {
				out[k] = normalizeSchemaTypes(m)
			} else {
				out[k] = v
			}
		default:
			out[k] = v
		}
	}
	return out
}

func mapGeminiRoleToCore(role string) string {
	switch role {
	case "model":
		return "assistant"
	case "user":
		return "user"
	default:
		return "user"
	}
}

// mapCoreStopReasonToGemini converts a Core stop reason to a Gemini finish_reason.
func mapCoreStopReasonToGemini(stopReason, status string) string {
	if stopReason != "" {
		switch stopReason {
		case "end_turn", "stop_sequence":
			return "STOP"
		case "max_tokens":
			return "MAX_TOKENS"
		case "content_filter":
			return "SAFETY"
		case "error":
			return "OTHER"
		}
	}
	switch status {
	case "completed":
		return "STOP"
	case "incomplete":
		return "MAX_TOKENS"
	case "failed":
		return "OTHER"
	default:
		return "STOP"
	}
}

// Ensure compile-time interface compliance.
var _ format.ClientAdapter = (*GeminiClientAdapter)(nil)
var _ format.ClientStreamAdapter = (*GeminiClientAdapter)(nil)

// Ensure GeminiStreamResult implements SSEStream.
var _ format.SSEStream = (*GeminiStreamResult)(nil)
