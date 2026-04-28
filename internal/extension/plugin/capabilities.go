package plugin

import (
	"encoding/json"

	"moonbridge/internal/foundation/logger"
	"moonbridge/internal/foundation/openai"
	"moonbridge/internal/protocol/anthropic"
)

// --- Request pipeline capabilities ---

// InputPreprocessor transforms raw input JSON before parsing.
type InputPreprocessor interface {
	PreprocessInput(ctx *RequestContext, raw json.RawMessage) json.RawMessage
}

// RequestMutator modifies the Anthropic request after conversion.
type RequestMutator interface {
	MutateRequest(ctx *RequestContext, req *anthropic.MessageRequest)
}

// ToolInjector injects additional tool definitions into the request.
// Called during tool conversion; returned tools are appended.
type ToolInjector interface {
	InjectTools(ctx *RequestContext) []anthropic.Tool
}

// MessageRewriter rewrites the message list during input conversion.
type MessageRewriter interface {
	RewriteMessages(ctx *RequestContext, messages []anthropic.Message) []anthropic.Message
}

// --- Provider pipeline capabilities ---

// Provider is the interface for upstream API clients.
type Provider interface {
	CreateMessage(req anthropic.MessageRequest) (*anthropic.MessageResponse, error)
	StreamMessage(req anthropic.MessageRequest) (<-chan anthropic.StreamEvent, error)
}

// ProviderWrapper wraps the upstream provider client.
// Used for server-side tool execution, rate limiting, etc.
type ProviderWrapper interface {
	WrapProvider(ctx *RequestContext, provider any) any
}

// --- Response pipeline capabilities ---

// ContentFilter filters or transforms response content blocks.
type ContentFilter interface {
	// FilterContent inspects a content block. Returns:
	//   skip: true to exclude the block from output
	//   extraOutput: additional output items to emit (e.g. reasoning items)
	FilterContent(ctx *RequestContext, block anthropic.ContentBlock) (skip bool, extraOutput []openai.OutputItem)
}

// ResponsePostProcessor modifies the final OpenAI response.
type ResponsePostProcessor interface {
	PostProcessResponse(ctx *RequestContext, resp *openai.Response)
}

// ContentRememberer is called with the full response content for caching.
type ContentRememberer interface {
	RememberContent(ctx *RequestContext, content []anthropic.ContentBlock)
}

// --- Streaming pipeline capabilities ---

// StreamInterceptor handles streaming events.
type StreamInterceptor interface {
	// NewStreamState creates per-request streaming state.
	NewStreamState() any

	// OnStreamEvent is called for each stream event.
	// Returns consumed=true if the plugin handled the event (bridge skips normal processing).
	// emit contains any events to send to the client.
	OnStreamEvent(ctx *StreamContext, event StreamEvent) (consumed bool, emit []openai.StreamEvent)

	// OnStreamComplete is called after the stream finishes.
	OnStreamComplete(ctx *StreamContext, outputText string)
}

// StreamEvent wraps an Anthropic stream event with metadata.
type StreamEvent struct {
	Type  string // "block_start", "block_delta", "block_stop"
	Index int
	Block *anthropic.ContentBlock // for block_start
	Delta anthropic.StreamDelta   // for block_delta
}

// --- Error handling ---

// ErrorTransformer rewrites upstream error messages.
type ErrorTransformer interface {
	TransformError(ctx *RequestContext, msg string) string
}

// --- Session state ---

// SessionStateProvider creates per-session state for the plugin.
type SessionStateProvider interface {
	NewSessionState() any
}

// ThinkingPrepender restores provider-specific thinking blocks into request
// history. The bridge only passes OpenAI reasoning summaries and session state;
// plugins decide how to decode, cache, or fall back for their provider.
type ThinkingPrepender interface {
	PrependThinkingForToolUse(messages []anthropic.Message, toolCallID string, pendingSummary []openai.ReasoningItemSummary, sessionState any) []anthropic.Message
	PrependThinkingForAssistant(blocks []anthropic.ContentBlock, pendingSummary []openai.ReasoningItemSummary, sessionState any) []anthropic.ContentBlock
}

// ReasoningExtractor reconstructs provider-specific thinking blocks from
// OpenAI Responses reasoning summaries.
type ReasoningExtractor interface {
	ExtractThinkingBlock(ctx *RequestContext, summary []openai.ReasoningItemSummary) (anthropic.ContentBlock, bool)
}

// --- Log pipeline capabilities ---

// LogConsumer is called during LogBuffer.Flush before entries are written.
// It can inspect, modify, or append log entries. Returned entries replace
// the original batch for output.
type LogConsumer interface {
	ConsumeLog(ctx *RequestContext, entries []logger.LogEntry) []logger.LogEntry
}
