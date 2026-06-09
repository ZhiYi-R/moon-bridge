package anthropic

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"

	"moonbridge/internal/format"
)

// ---------------------------------------------------------------------------
// CacheManager — local interface to avoid import cycle
// ---------------------------------------------------------------------------
// The cache package imports anthropic (breakpoint.go, planner_ext.go), so
// anthropic cannot import cache.  This interface lets the dispatch layer
// inject cache planning/registry logic without a direct dependency.
//
// Implementations live outside the anthropic package (e.g. in the server
// dispatch or an adapter wiring layer) and wrap the real cache package.

// CacheManager handles prompt cache planning, injection, and registry
// updates for Anthropic requests.
type CacheManager interface {
	// PlanAndInject plans cache breakpoints and injects cache_control
	// into the anthropic MessageRequest.  Returns a stable key + TTL
	// that can be used later to update the registry.
	PlanAndInject(ctx context.Context, req *MessageRequest, coreReq *format.CoreRequest) (key, ttl string)

	// UpdateRegistry updates the in-memory cache registry from upstream
	// usage signals after a response is received.
	UpdateRegistry(ctx context.Context, key, ttl string, usage Usage)
}

// ---------------------------------------------------------------------------
// AnthropicProviderAdapter — implements format.ProviderAdapter + format.ProviderStreamAdapter
// ---------------------------------------------------------------------------

// AnthropicProviderAdapter converts Core format requests/responses to/from
// the Anthropic Messages API format.
//
// Clean room: no dependency on internal/protocol/bridge/.
// Only references: config, format, and anthropic types.
type AnthropicProviderAdapter struct {
	cfgMaxTokens int
	cacheMgr     CacheManager
	hooks        format.CorePluginHooks

	streamMu     sync.Mutex
	streamEvents []StreamEvent
}

// NewAnthropicProviderAdapter creates a new AnthropicProviderAdapter.
//
// cacheMgr handles prompt cache planning.  Pass a no-op implementation
// if caching is not needed.
func NewAnthropicProviderAdapter(cfgMaxTokens int, cacheMgr CacheManager, hooks format.CorePluginHooks) *AnthropicProviderAdapter {
	return &AnthropicProviderAdapter{
		cfgMaxTokens: cfgMaxTokens,
		cacheMgr:     cacheMgr,
		hooks:        hooks.WithDefaults(),
	}
}

// ProviderProtocol returns "anthropic".
func (a *AnthropicProviderAdapter) ProviderProtocol() string {
	return "anthropic"
}

// =========================================================================
// Delegate methods — anthropic.MessageRequest ↔ CoreRequest compatibility bridge
// =========================================================================

// FromAnthropicRequest converts an anthropic.MessageRequest to CoreRequest,
// then delegates to FromCoreRequest. This is the compatibility bridge during
// migration. DELETE after all callers use CoreRequest.
func (a *AnthropicProviderAdapter) FromAnthropicRequest(ctx context.Context, req *MessageRequest) (any, error) {
	coreReq := a.anthropicToCoreRequest(req)
	return a.FromCoreRequest(ctx, coreReq)
}

// ToAnthropicResponse converts an anthropic.MessageResponse to CoreResponse,
// then delegates to ToCoreResponse. DELETE after all callers use CoreResponse.
func (a *AnthropicProviderAdapter) ToAnthropicResponse(ctx context.Context, resp *MessageResponse) (*format.CoreResponse, error) {
	return a.ToCoreResponse(ctx, resp)
}

// anthropicToCoreRequest converts anthropic.MessageRequest → CoreRequest (private helper).
func (a *AnthropicProviderAdapter) anthropicToCoreRequest(req *MessageRequest) *format.CoreRequest {
	if req == nil {
		return nil
	}

	coreReq := &format.CoreRequest{
		Model:         req.Model,
		MaxTokens:     req.MaxTokens,
		Messages:      make([]format.CoreMessage, 0, len(req.Messages)),
		System:        a.fromContentBlocks(req.System),
		Temperature:   req.Temperature,
		TopP:          req.TopP,
		TopK:          req.TopK,
		StopSequences: req.StopSequences,
		Stream:        req.Stream,
		Metadata:      req.Metadata,
		Thinking:      a.toCoreThinkingConfig(req.Thinking),
		Output:        a.toCoreOutputConfig(req.OutputConfig),
		CacheControl:  a.toCoreCacheControl(req.CacheControl),
	}

	// Messages
	for _, msg := range req.Messages {
		coreReq.Messages = append(coreReq.Messages, format.CoreMessage{
			Role:    a.mapRole(msg.Role),
			Content: a.fromContentBlocks(msg.Content),
		})
	}

	// Tools
	if len(req.Tools) > 0 {
		coreReq.Tools = make([]format.CoreTool, 0, len(req.Tools))
		for _, t := range req.Tools {
			name := t.Name
			if name == "" {
				name = repairEmptyToolName(t.Description)
				if name == "" {
					slog.Warn("anthropic adapter: skipping tool with empty name", "description", t.Description)
					continue
				}
			}
			coreReq.Tools = append(coreReq.Tools, format.CoreTool{
				Name:        name,
				Description: t.Description,
				InputSchema: t.InputSchema,
			})
		}
	}

	// ToolChoice
	if req.ToolChoice != nil {
		coreReq.ToolChoice = &format.CoreToolChoice{
			Mode: req.ToolChoice.Type,
			Name: req.ToolChoice.Name,
		}
	}

	return coreReq
}

// toCoreThinkingConfig converts anthropic *ThinkingConfig to *format.CoreThinkingConfig.
func (a *AnthropicProviderAdapter) toCoreThinkingConfig(tc *ThinkingConfig) *format.CoreThinkingConfig {
	if tc == nil {
		return nil
	}
	return &format.CoreThinkingConfig{
		Type:         tc.Type,
		BudgetTokens: tc.BudgetTokens,
	}
}

// toCoreOutputConfig converts anthropic *OutputConfig to *format.CoreOutputConfig.
func (a *AnthropicProviderAdapter) toCoreOutputConfig(oc *OutputConfig) *format.CoreOutputConfig {
	if oc == nil {
		return nil
	}
	return &format.CoreOutputConfig{
		Effort: oc.Effort,
	}
}

// toCoreCacheControl converts anthropic *CacheControl to *format.CoreCacheControl.
func (a *AnthropicProviderAdapter) toCoreCacheControl(cc *CacheControl) *format.CoreCacheControl {
	if cc == nil {
		return nil
	}
	return &format.CoreCacheControl{
		Enabled:    true,
		TTLSeconds: parseTTLSeconds(cc.TTL),
		Strategy:   "auto",
	}
}

// extractContentText extracts text content from an Anthropic web_search_tool_result
// Content field, which can be a plain string or a list of content blocks.
func extractContentText(content any) string {
	if content == nil {
		return ""
	}
	switch v := content.(type) {
	case string:
		return v
	case []any:
		var b strings.Builder
		for _, item := range v {
			if m, ok := item.(map[string]any); ok {
				if t, ok := m["text"].(string); ok {
					if b.Len() > 0 {
						b.WriteByte('\n')
					}
					b.WriteString(t)
				}
			}
		}
		return b.String()
	case []ContentBlock:
		var b strings.Builder
		for _, block := range v {
			if block.Type == "text" && block.Text != "" {
				if b.Len() > 0 {
					b.WriteByte('\n')
				}
				b.WriteString(block.Text)
			}
		}
		return b.String()
	}
	return fmt.Sprintf("%v", content)
}

// parseTTLSeconds parses a duration string like "300s" or "5m" into seconds.
func parseTTLSeconds(ttl string) int {
	if ttl == "" {
		return 0
	}
	var value int
	var unit string
	if n, err := fmt.Sscanf(ttl, "%d%s", &value, &unit); err == nil && n >= 1 {
		switch unit {
		case "s":
			return value
		case "m":
			return value * 60
		case "h":
			return value * 3600
		default:
			// If unit is empty or unrecognized but value was parsed, treat as seconds.
			if n == 1 {
				return value
			}
		}
	}
	return 0
}

// =========================================================================
// FromCoreRequest — CoreRequest → *MessageRequest
// =========================================================================

// FromCoreRequest converts a CoreRequest into an *MessageRequest.
//
// Conversion steps:
//  1. Call hooks.MutateCoreRequest (plugin modifications to CoreRequest)
//  2. Map all CoreRequest fields to anthropic.MessageRequest fields
//  3. Cache planning via CacheManager (PlanAndInject)
func (a *AnthropicProviderAdapter) FromCoreRequest(ctx context.Context, req *format.CoreRequest) (any, error) {
	if req == nil {
		return nil, fmt.Errorf("anthropic adapter: core request is nil")
	}

	// Step 1: Allow plugins to mutate the CoreRequest before conversion.
	a.hooks.RewriteMessages(ctx, req)
	a.hooks.MutateCoreRequest(ctx, req)

	// Strip base64 image data from all text content to prevent token waste.
	format.StripContentBlocks(req.System)
	for i := range req.Messages {
		format.StripContentBlocks(req.Messages[i].Content)
	}

	// Ensure cache manager is available before proceeding.
	if a.cacheMgr == nil {
		return nil, fmt.Errorf("anthropic adapter: cache manager is nil")
	}

	// Step 2: Build the anthropic MessageRequest.
	anthropicReq := MessageRequest{
		Model:         req.Model,
		MaxTokens:     a.defaultMaxTokens(req.MaxTokens),
		Messages:      make([]Message, 0, len(req.Messages)),
		Temperature:   req.Temperature,
		TopP:          req.TopP,
		StopSequences: req.StopSequences,
		Stream:        req.Stream,
		TopK:          req.TopK,
		Thinking:      a.coreThinkingConfig(req.Thinking),
		OutputConfig:  a.coreOutputConfig(req.Output),
		CacheControl:  a.coreCacheControl(req.CacheControl),
		Metadata:      req.Metadata,
	}

	// System
	if len(req.System) > 0 {
		anthropicReq.System = a.toContentBlocks(req.System)
	}

	// Messages
	// Messages — merge consecutive user messages that contain tool_result blocks
	// into a single user message. Anthropic requires all tool_result blocks from
	// one assistant turn to be in one user message immediately after tool_use.
	for _, msg := range req.Messages {
		anthroMsg := Message{
			Role:    a.mapRole(msg.Role),
			Content: a.toContentBlocks(msg.Content),
		}
		last := len(anthropicReq.Messages) - 1
		if last >= 0 && anthroMsg.Role == "user" &&
			anthropicReq.Messages[last].Role == "user" &&
			isToolResultOnly(anthroMsg.Content) &&
			isToolResultOnly(anthropicReq.Messages[last].Content) {
			anthropicReq.Messages[last].Content = append(
				anthropicReq.Messages[last].Content, anthroMsg.Content...)
		} else {
			anthropicReq.Messages = append(anthropicReq.Messages, anthroMsg)
		}
	}

	// Tools
	if len(req.Tools) > 0 {
		anthropicReq.Tools = make([]Tool, 0, len(req.Tools))
		for _, t := range req.Tools {
			name := t.Name
			if name == "" {
				name = repairEmptyToolName(t.Description)
				if name == "" {
					slog.Warn("anthropic adapter: skipping tool with empty name in FromCoreRequest", "description", t.Description)
					continue
				}
			}
			schema := cleanSchema(t.InputSchema)
			if schema == nil {
				schema = map[string]any{"type": "object"}
			}
			anthropicReq.Tools = append(anthropicReq.Tools, Tool{
				Name:        name,
				Description: t.Description,
				InputSchema: schema,
			})
		}
	}

	// ToolChoice
	if req.ToolChoice != nil {
		tc := a.toAnthropicToolChoice(*req.ToolChoice)
		anthropicReq.ToolChoice = &tc
	} else {
		anthropicReq.ToolChoice = &ToolChoice{Type: "auto"}
	}

	// Step 3: Cache planning via CacheManager.
	// PlanAndInject may modify anthropicReq in-place by setting cache_control
	// on tools, system blocks, messages, or the request-level field.
	a.cacheMgr.PlanAndInject(ctx, &anthropicReq, req)

	return &anthropicReq, nil
}

// =========================================================================
// ToCoreResponse — *MessageResponse → *format.CoreResponse
// =========================================================================

// ToCoreResponse converts an *MessageResponse into a *format.CoreResponse.
//
// The response content blocks become a single assistant message. Cache registry
// is updated from usage signals via CacheManager.
func (a *AnthropicProviderAdapter) ToCoreResponse(ctx context.Context, resp any) (*format.CoreResponse, error) {
	msgResp, err := normalizeAnthropicMessageResponse(resp)
	if err != nil {
		return nil, fmt.Errorf("anthropic adapter: %w", err)
	}
	ctx = coreHookContext(ctx, msgResp.Model)

	// Map stop_reason to Core status.
	status := a.mapStopReasonToStatus(msgResp.StopReason)

	// Convert content blocks to Core message.
	coreContent := a.fromContentBlocks(msgResp.Content)

	coreResp := &format.CoreResponse{
		ID:     msgResp.ID,
		Status: status,
		Model:  msgResp.Model,
		Messages: []format.CoreMessage{
			{
				Role:    "assistant",
				Content: coreContent,
			},
		},
		Usage:      a.toCoreUsage(msgResp.Usage),
		StopReason: msgResp.StopReason,
	}
	a.hooks.RememberContent(ctx, coreContent)

	// Map error-like stop reasons.
	if msgResp.StopReason == "content_filtered" {
		coreResp.Error = &format.CoreError{
			Type:    "content_filter",
			Message: "response filtered by content moderation",
		}
		coreResp.Status = "failed"
	}

	// Update cache registry from usage signals via CacheManager.
	// The key/ttl were computed during PlanAndInject and must be accessible
	// through the CacheManager's own state (or context).
	if a.cacheMgr != nil {
		a.cacheMgr.UpdateRegistry(ctx, "", "", msgResp.Usage)
	}

	return coreResp, nil
}

func normalizeAnthropicMessageResponse(resp any) (*MessageResponse, error) {
	switch v := resp.(type) {
	case MessageResponse:
		msgResp := v
		return &msgResp, nil
	case *MessageResponse:
		if v == nil {
			return nil, fmt.Errorf("expected anthropic.MessageResponse, got nil *anthropic.MessageResponse")
		}
		return v, nil
	default:
		return nil, fmt.Errorf("expected anthropic.MessageResponse, got %T", resp)
	}
}

// =========================================================================
// ToCoreStream — anthropic.Stream → <-chan format.CoreStreamEvent
// =========================================================================

// streamConverterState tracks state across a stream conversion.
type streamConverterState struct {
	seqNum          int64
	msgID           string
	model           string
	blockTypes      map[int]string            // content index → block type
	blockSignatures map[int]string            // content index → reasoning signature (from signature_delta)
	finalUsage      *format.CoreUsage         // tracked from message_delta, passed to message_stop
	adapter         *AnthropicProviderAdapter // for buffering raw stream events (trace)
	suppressText    map[int]bool              // text indices to suppress (server-side search status, etc.)
}

// ToCoreStream consumes an anthropic.Stream and returns a channel of CoreStreamEvent.
//
// The adapter owns the read-loop goroutine. The returned channel is closed when
// the stream ends, context is cancelled, or an error occurs.
func (a *AnthropicProviderAdapter) ToCoreStream(ctx context.Context, src any) (<-chan format.CoreStreamEvent, error) {
	stream, ok := src.(Stream)
	if !ok {
		return nil, fmt.Errorf("anthropic adapter: expected anthropic.Stream, got %T", src)
	}
	ctx = coreHookContext(ctx, "")
	events := make(chan format.CoreStreamEvent, 64)

	// Initialize stream event buffer for trace capture.
	a.streamMu.Lock()
	a.streamEvents = make([]StreamEvent, 0, 64)
	a.streamMu.Unlock()

	go func() {
		defer close(events)
		defer stream.Close()

		state := &streamConverterState{
			blockTypes:      make(map[int]string),
			blockSignatures: make(map[int]string),
			adapter:         a,
			suppressText:    make(map[int]bool),
		}

		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			ev, err := stream.Next()
			if err != nil {
				if err == io.EOF {
					// Stream ended normally.
					return
				}
				// Context cancellation is clean shutdown, not a failure.
				if err == context.Canceled || err == context.DeadlineExceeded {
					return
				}
				state.emit(events, format.CoreStreamEvent{
					Type: format.CoreEventFailed,
					Error: &format.CoreError{
						Message: err.Error(),
					},
				})
				return
			}

			state.convertEvent(events, ev)
		}
	}()

	return events, nil
}

// =========================================================================
// Stream event conversion
// =========================================================================

func (s *streamConverterState) nextSeq() int64 {
	s.seqNum++
	return s.seqNum
}

func (s *streamConverterState) emit(events chan<- format.CoreStreamEvent, ev format.CoreStreamEvent) {
	ev.SeqNum = s.nextSeq()
	events <- ev
}

func (s *streamConverterState) convertEvent(events chan<- format.CoreStreamEvent, ev StreamEvent) {
	// Buffer the original event for trace (max 4MB).
	if s.adapter != nil {
		s.adapter.bufferStreamEvent(ev)
	}
	switch ev.Type {
	case "message_start":
		if ev.Message != nil {
			s.msgID = ev.Message.ID
			s.model = ev.Message.Model

			s.emit(events, format.CoreStreamEvent{
				Type:   format.CoreEventCreated,
				Status: "in_progress",
				Model:  s.model,
			})
			s.emit(events, format.CoreStreamEvent{
				Type:   format.CoreItemAdded,
				ItemID: s.msgID,
			})
		}

	case "content_block_start":
		if ev.ContentBlock == nil {
			return
		}
		index := ev.Index
		blockType := ev.ContentBlock.Type
		s.blockTypes[index] = blockType

		switch blockType {
		case "text":
			s.emit(events, format.CoreStreamEvent{
				Type:  format.CoreContentBlockStarted,
				Index: index,
				ContentBlock: &format.CoreContentBlock{
					Type: "text",
				},
			})

		case "tool_use":
			s.emit(events, format.CoreStreamEvent{
				Type:  format.CoreContentBlockStarted,
				Index: index,
				ContentBlock: &format.CoreContentBlock{
					Type:      "tool_use",
					ToolUseID: ev.ContentBlock.ID,
					ToolName:  ev.ContentBlock.Name,
				},
			})
			s.emit(events, format.CoreStreamEvent{
				Type:   format.CoreItemAdded,
				ItemID: ev.ContentBlock.ID,
			})

		case "thinking":
			s.emit(events, format.CoreStreamEvent{
				Type:  format.CoreContentBlockStarted,
				Index: index,
				ContentBlock: &format.CoreContentBlock{
					Type:               "reasoning",
					ReasoningText:      ev.ContentBlock.Thinking,
					ReasoningSignature: ev.ContentBlock.Signature,
				},
			})

		case "server_tool_use":
			// Anthropic server-side tool usage marker. Do NOT forward — the tool_use
			// would become an orphan in subsequent requests (no matching tool_result),
			// causing API rejection. Server-side tools are transparent to the client.
			s.blockTypes[index] = "server_tool_use"

		case "web_search_tool_result":
			// Anthropic server-side tool result. Results are consumed internally by
			// the model — do NOT forward raw search data as Core text blocks.
			// Emit a tool_result so the stream state machine stays consistent.
			s.blockTypes[index] = "web_search_tool_result"
		}

	case "content_block_delta":
		index := ev.Index
		blockType := s.blockTypes[index]

		switch {
		case ev.Delta.Type == "text_delta" || blockType == "text":
			// Suppress server-side search status messages (e.g. "Search results for query: ...").
			// These are infrastructure noise from the Anthropic provider, not model output.
			if strings.HasPrefix(ev.Delta.Text, "Search results for query:") {
				s.suppressText[index] = true
				break
			}
			s.emit(events, format.CoreStreamEvent{
				Type:  format.CoreTextDelta,
				Index: index,
				Delta: ev.Delta.Text,
			})

		case ev.Delta.Type == "input_json_delta" || blockType == "tool_use":
			s.emit(events, format.CoreStreamEvent{
				Type:  format.CoreToolCallArgsDelta,
				Index: index,
				Delta: ev.Delta.PartialJSON,
			})

		case ev.Delta.Type == "thinking_delta" || blockType == "thinking":
			s.emit(events, format.CoreStreamEvent{
				Type:  format.CoreTextDelta,
				Index: index,
				Delta: ev.Delta.Thinking,
				ContentBlock: &format.CoreContentBlock{
					Type: "reasoning",
				},
			})

		case ev.Delta.Type == "signature_delta":
			if sig := ev.Delta.Signature; sig != "" {
				s.blockSignatures[index] = sig
			}
		}

	case "content_block_stop":
		index := ev.Index
		blockType := s.blockTypes[index]

		// Skip suppressed blocks (server-side search status text, etc.)
		if s.suppressText[index] {
			delete(s.suppressText, index)
			break
		}

		if blockType == "thinking" {
			s.emit(events, format.CoreStreamEvent{
				Type:  format.CoreContentBlockDone,
				Index: index,
				ContentBlock: &format.CoreContentBlock{
					Type:               "reasoning",
					ReasoningSignature: s.blockSignatures[index],
				},
			})
		} else {
			s.emit(events, format.CoreStreamEvent{
				Type:  format.CoreContentBlockDone,
				Index: index,
			})
		}
		s.emit(events, format.CoreStreamEvent{
			Type: format.CoreItemDone,
		})

	case "message_delta":
		if ev.Usage != nil {
			totalInput := ev.Usage.InputTokens + ev.Usage.CacheReadInputTokens
			s.finalUsage = &format.CoreUsage{
				// Core format InputTokens = total input (fresh + cache), matching OpenAI API semantics.
				// Anthropic input_tokens is fresh-only, so we add CacheReadInputTokens.
				InputTokens:       totalInput,
				OutputTokens:      ev.Usage.OutputTokens,
				CachedInputTokens: ev.Usage.CacheReadInputTokens,
			}
			s.emit(events, format.CoreStreamEvent{
				Type:       format.CoreEventInProgress,
				Usage:      s.finalUsage,
				StopReason: ev.Delta.StopReason,
			})
		}

	case "message_stop":
		s.emit(events, format.CoreStreamEvent{
			Type:   format.CoreEventCompleted,
			Usage:  s.finalUsage,
			Status: "completed",
			Model:  s.model,
		})

	case "error":
		errMsg := "unknown error"
		errType := "api_error"
		if ev.Error != nil {
			errMsg = ev.Error.Message
			errType = ev.Error.Type
		}
		s.emit(events, format.CoreStreamEvent{
			Type: format.CoreEventFailed,
			Error: &format.CoreError{
				Message: errMsg,
				Type:    errType,
			},
		})

	case "ping":
		s.emit(events, format.CoreStreamEvent{
			Type: format.CorePing,
		})
	}

}

// =========================================================================
// Helpers: Core → Anthropic
// =========================================================================

// toContentBlocks converts []CoreContentBlock to []ContentBlock.
func (a *AnthropicProviderAdapter) toContentBlocks(blocks []format.CoreContentBlock) []ContentBlock {
	result := make([]ContentBlock, 0, len(blocks))
	for _, b := range blocks {
		result = append(result, a.toContentBlock(b))
	}
	return result
}

// toContentBlock converts a single CoreContentBlock to anthropic ContentBlock.
func (a *AnthropicProviderAdapter) toContentBlock(b format.CoreContentBlock) ContentBlock {
	switch b.Type {
	case "text":
		block := ContentBlock{
			Type: "text",
			Text: b.Text,
		}
		if cc := a.extractCacheControl(b.Extensions); cc != nil {
			block.CacheControl = cc
		}
		return block

	case "image":
		return ContentBlock{
			Type: "image",
			Source: &ImageSource{
				Type:      "base64",
				Data:      b.ImageData,
				MediaType: b.MediaType,
			},
		}

	case "tool_use":
		return ContentBlock{
			Type:  "tool_use",
			ID:    b.ToolUseID,
			Name:  b.ToolName,
			Input: b.ToolInput,
		}

	case "tool_result":
		var content any
		if len(b.ToolResultContent) > 0 {
			content = a.toContentBlocks(b.ToolResultContent)
		}
		return ContentBlock{
			Type:      "tool_result",
			ToolUseID: b.ToolUseID,
			Content:   content,
		}

	case "reasoning":
		block := ContentBlock{
			Type:      "thinking",
			Thinking:  b.ReasoningText,
			Signature: b.ReasoningSignature,
		}
		if cc := a.extractCacheControl(b.Extensions); cc != nil {
			block.CacheControl = cc
		}
		return block

	default:
		// Fallback: treat unknown types as text.
		return ContentBlock{
			Type: "text",
			Text: b.Text,
		}
	}
}

// toAnthropicToolChoice converts CoreToolChoice to anthropic ToolChoice.
func (a *AnthropicProviderAdapter) toAnthropicToolChoice(tc format.CoreToolChoice) ToolChoice {
	switch tc.Mode {
	case "none":
		return ToolChoice{Type: "none"}
	case "auto":
		return ToolChoice{Type: "auto"}
	case "any", "required":
		// Note: Anthropic API does not have a "required" mode (force at least one tool call).
		// The closest equivalent is "any" (let the model choose any of the provided tools).
		// This means the original "required" semantics is approximated but not exact.
		if tc.Name != "" {
			return ToolChoice{Type: "tool", Name: tc.Name}
		}
		return ToolChoice{Type: "any"}
	default:
		if tc.Name != "" {
			return ToolChoice{Type: "tool", Name: tc.Name}
		}
		return ToolChoice{Type: "auto"}
	}
}

// defaultMaxTokens ensures MaxTokens has a legal value.
// If requested is 0 or negative, falls back to config default, then 1024.
func (a *AnthropicProviderAdapter) defaultMaxTokens(requested int) int {
	if requested > 0 {
		return requested
	}
	if a.cfgMaxTokens > 0 {
		return a.cfgMaxTokens
	}
	return 1024
}

// mapRole maps a CoreMessage role to an Anthropic Messages API role.
// Anthropic does not accept "tool" as a message role; tool results must be
// sent as "user" messages with tool_result content blocks.
func (a *AnthropicProviderAdapter) mapRole(role string) string {
	switch role {
	case "tool":
		return "user"
	default:
		return role
	}
}

// isToolResultOnly checks if all content blocks in a message are tool_result type.
// Used to identify consecutive user-tool-result messages that should be merged.
func isToolResultOnly(blocks []ContentBlock) bool {
	if len(blocks) == 0 {
		return false
	}
	for _, b := range blocks {
		if b.Type != "tool_result" {
			return false
		}
	}
	return true
}

// extractCacheControl reads cache_control from a CoreContentBlock.Extensions map.
func (a *AnthropicProviderAdapter) extractCacheControl(ext map[string]any) *CacheControl {
	if ext == nil {
		return nil
	}
	raw, ok := ext["cache_control"]
	if !ok {
		return nil
	}
	switch v := raw.(type) {
	case map[string]any:
		ttl, _ := v["ttl"].(string)
		if ctype, ok := v["type"].(string); ok && ctype == "ephemeral" {
			return &CacheControl{Type: "ephemeral", TTL: ttl}
		}
		return nil
	case string:
		if v == "ephemeral" {
			return &CacheControl{Type: "ephemeral"}
		}
		return nil
	default:
		return nil
	}
}

// =========================================================================
// Helpers: Anthropic → Core
// =========================================================================

// fromContentBlocks converts []anthropic.ContentBlock to []CoreContentBlock.
func (a *AnthropicProviderAdapter) fromContentBlocks(blocks []ContentBlock) []format.CoreContentBlock {
	result := make([]format.CoreContentBlock, 0, len(blocks))
	for _, b := range blocks {
		result = append(result, a.fromContentBlock(b))
	}
	return result
}

// fromContentBlock converts a single anthropic ContentBlock to CoreContentBlock.
func (a *AnthropicProviderAdapter) fromContentBlock(b ContentBlock) format.CoreContentBlock {
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
				cb.ToolResultContent = a.fromContentBlocks(content)
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
		return format.CoreContentBlock{
			Type: "text",
		}
	}
}

// toCoreUsage converts anthropic Usage to CoreUsage.
func (a *AnthropicProviderAdapter) toCoreUsage(u Usage) format.CoreUsage {
	cached := u.CacheReadInputTokens
	if cached == 0 {
		cached = u.CacheCreationInputTokens
	}
	totalInput := u.InputTokens + cached
	return format.CoreUsage{
		InputTokens:       totalInput, // Total input (fresh + cache) matching OpenAI API semantics
		OutputTokens:      u.OutputTokens,
		TotalTokens:       totalInput + u.OutputTokens,
		CachedInputTokens: cached,
	}
}

// mapStopReasonToStatus maps anthropic stop_reason to Core status string.
func (a *AnthropicProviderAdapter) mapStopReasonToStatus(reason string) string {
	switch reason {
	case "end_turn", "stop_sequence", "tool_use":
		return "completed"
	case "max_tokens":
		return "incomplete"
	case "content_filtered":
		return "failed"
	case "":
		return "in_progress"
	default:
		return "completed"
	}
}

// bufferStreamEvent buffers the raw anthropic stream event for trace capture,
// up to the 4MB limit. The event is JSON-marshalled to estimate its size.
func (a *AnthropicProviderAdapter) bufferStreamEvent(ev StreamEvent) {
	a.streamMu.Lock()
	defer a.streamMu.Unlock()
	// Cap buffer at 1024 events (~4MB estimation) to prevent unbounded memory growth.
	if len(a.streamEvents) >= 1024 {
		return
	}
	a.streamEvents = append(a.streamEvents, ev)
}

// StreamBuffer returns the buffered stream events for trace capture.
func (a *AnthropicProviderAdapter) StreamBuffer() []StreamEvent {
	a.streamMu.Lock()
	defer a.streamMu.Unlock()
	return a.streamEvents
}

// RememberStreamContent stores response content from a stream for plugin state tracking.
func (a *AnthropicProviderAdapter) RememberStreamContent(ctx context.Context, blocks []format.CoreContentBlock) {
	if len(blocks) == 0 {
		return
	}
	a.hooks.RememberContent(ctx, blocks)
}

func coreHookContext(ctx context.Context, model string) context.Context {
	if model == "" {
		return ctx
	}
	return format.WithCoreHookModelAlias(ctx, model)
}

// =========================================================================
// Helpers: Core -> Anthropic (new type conversions)
// =========================================================================

// coreThinkingConfig converts *format.CoreThinkingConfig to *ThinkingConfig.
func (a *AnthropicProviderAdapter) coreThinkingConfig(c *format.CoreThinkingConfig) *ThinkingConfig {
	if c == nil {
		return nil
	}
	return &ThinkingConfig{
		Type:         c.Type,
		BudgetTokens: c.BudgetTokens,
	}
}

// coreOutputConfig converts *format.CoreOutputConfig to *OutputConfig.
func (a *AnthropicProviderAdapter) coreOutputConfig(c *format.CoreOutputConfig) *OutputConfig {
	if c == nil {
		return nil
	}
	return &OutputConfig{
		Effort: c.Effort,
	}
}

// coreCacheControl converts *format.CoreCacheControl to *CacheControl.
// The per-block CacheControl only supports ephemeral; level-up mapping:
// - enabled + "auto" strategy -> ephemeral
// - otherwise -> nil (provider defaults)
func (a *AnthropicProviderAdapter) coreCacheControl(c *format.CoreCacheControl) *CacheControl {
	if c == nil || !c.Enabled {
		return nil
	}
	cc := &CacheControl{Type: "ephemeral"}
	if c.TTLSeconds > 0 {
		cc.TTL = fmt.Sprintf("%ds", c.TTLSeconds)
	}
	return cc
}

// repairEmptyToolName attempts to infer a tool name from its description
// when the name field is empty. Returns the repaired name, or "" if
// inference fails — the caller should skip the tool.
func repairEmptyToolName(description string) string {
	if description != "" && strings.Contains(description, "tool_search") {
		return "tool_search"
	}
	return ""
}

// cleanSchema recursively removes nil values from a JSON schema map.
// DeepSeek rejects null values in schema properties.
// Empty maps are preserved as-is (e.g. properties:{}) to avoid corrupting
// the JSON Schema structure. Returns nil when the entire result is empty;
// callers must supply a {"type":"object"} fallback when needed.
func cleanSchema(schema map[string]any) map[string]any {
	if schema == nil {
		return nil
	}
	result := make(map[string]any, len(schema))
	for k, v := range schema {
		switch val := v.(type) {
		case nil:
			continue // skip nil values
		case map[string]any:
			if len(val) == 0 {
				// Preserve empty maps (e.g. properties: {}) instead of
				// recursing, which would turn {} into {"type":"object"}.
				result[k] = val
			} else {
				cleaned := cleanSchema(val)
				result[k] = cleaned
			}
		case []any:
			cleaned := make([]any, 0, len(val))
			for _, item := range val {
				if itemMap, ok := item.(map[string]any); ok {
					if c := cleanSchema(itemMap); len(c) > 0 {
						cleaned = append(cleaned, c)
					}
				} else if item != nil {
					cleaned = append(cleaned, item)
				}
			}
			if len(cleaned) > 0 {
				result[k] = cleaned
			}
		default:
			result[k] = v
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}
