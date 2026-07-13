package plugin

import (
	"context"
	"encoding/json"
	"moonbridge/internal/protocol/anthropic"
	"net/http"
	"time"

	"moonbridge/internal/format"
	"moonbridge/internal/logger"
	"moonbridge/internal/protocol/openai"

	foundationdb "moonbridge/internal/db"
)

// --- Request pipeline capabilities ---

// InputPreprocessor transforms raw input JSON before parsing.
type InputPreprocessor interface {
	PreprocessInput(ctx *RequestContext, raw json.RawMessage) json.RawMessage
}

// RequestMutator modifies the Core request after conversion.
type RequestMutator interface {
	MutateRequest(ctx *RequestContext, req *format.CoreRequest)
}

// ToolInjector injects additional tool definitions into the request.
// Called during tool conversion; returned tools are appended.
type ToolInjector interface {
	InjectTools(ctx *RequestContext) []format.CoreTool
}

// MessageRewriter rewrites the message list during input conversion.
type MessageRewriter interface {
	RewriteMessages(ctx *RequestContext, messages []format.CoreMessage) []format.CoreMessage
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
	// FilterContent inspects a content block. Returns skip=true to exclude
	// the block from the response output.
	FilterContent(ctx *RequestContext, block format.CoreContentBlock) bool
}

// ResponsePostProcessor modifies the final OpenAI response.
type ResponsePostProcessor interface {
	PostProcessResponse(ctx *RequestContext, resp *openai.Response)
}

// ContentRememberer is called with the full response content for caching.
type ContentRememberer interface {
	RememberContent(ctx *RequestContext, content []format.CoreContentBlock)
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
	Block *format.CoreContentBlock // for block_start
	Delta anthropic.StreamDelta    // for block_delta
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
	PrependThinkingForToolUse(messages []format.CoreMessage, toolCallID string, pendingSummary []openai.ReasoningItemSummary, sessionState any) []format.CoreMessage
	PrependThinkingForAssistant(blocks []format.CoreContentBlock, pendingSummary []openai.ReasoningItemSummary, sessionState any) []format.CoreContentBlock
}

// ReasoningExtractor reconstructs provider-specific thinking blocks from
// OpenAI Responses reasoning summaries.
type ReasoningExtractor interface {
	ExtractThinkingBlock(ctx *RequestContext, summary []openai.ReasoningItemSummary) (format.CoreContentBlock, bool)
}

// --- Request completion hook ---

// RequestResult carries per-request outcome data for post-request hooks.
type RequestResult struct {
	Model         string
	ActualModel   string
	ProviderKey   string
	InputTokens   int
	OutputTokens  int
	CacheCreation int
	CacheRead     int
	Cost          float64
	Status        string // "success" or "error"
	ErrorMessage  string
	Duration      time.Duration
	Usage         RequestUsage
}

// RequestUsage carries both provider-native usage and normalized display usage.
type RequestUsage struct {
	Protocol    string
	UsageSource string

	RawInputTokens   int
	RawOutputTokens  int
	RawCacheCreation int
	RawCacheRead     int

	NormalizedInputTokens   int
	NormalizedOutputTokens  int
	NormalizedCacheCreation int
	NormalizedCacheRead     int

	RawUsageJSON json.RawMessage
}

// RequestCompletionHook is called after each request completes, regardless
// of success or failure. Use for observability, metrics recording, etc.
type RequestCompletionHook interface {
	OnRequestCompleted(ctx *RequestContext, result RequestResult)
}

// --- HTTP route registration ---

// RouteRegistrar allows plugins to register HTTP handlers on the server's
// mux during initialization. The register function is goroutine-safe during
// init time.
type RouteRegistrar interface {
	RegisterRoutes(register func(pattern string, handler http.Handler))
}

// --- Log pipeline capabilities ---

// LogConsumer is called for every slog log record via the consume pipeline.
// Implementations receive LogEntry slices and may inspect, modify, or suppress them.
type LogConsumer interface {
	ConsumeLog(ctx *RequestContext, entries []logger.LogEntry) []logger.LogEntry
}

// --- Database capabilities ---

// DBProvider is implemented by plugins that provide a database backend.
// The returned Provider may be nil if the plugin is disabled or
// unsupported in the current environment.
type DBProvider interface {
	DBProvider() foundationdb.Provider
}

// DBConsumer is implemented by plugins that need database persistence.
// The returned Consumer may be nil if the plugin is disabled.
type DBConsumer interface {
	DBConsumer() foundationdb.Consumer
}

// --- Usage analytics capabilities ---

// UsageQuery selects a time window of persisted per-request usage. A zero
// Since means "from the earliest record"; a zero Until means "up to now".
type UsageQuery struct {
	Since time.Time
	Until time.Time
}

// UsageModelAggregate holds aggregated usage for a single model.
type UsageModelAggregate struct {
	Model         string
	ActualModel   string
	Requests      int64
	InputTokens   int64
	OutputTokens  int64
	CacheCreation int64
	CacheRead     int64
	Cost          float64
}

// UsageAggregate holds aggregated usage totals plus a per-model breakdown for a
// queried time window, sourced from persisted (cross-session) metrics.
type UsageAggregate struct {
	Requests      int64
	InputTokens   int64
	OutputTokens  int64
	CacheCreation int64
	CacheRead     int64
	Cost          float64
	Earliest      time.Time
	Latest        time.Time
	ByModel       []UsageModelAggregate
}

// UsageSource is implemented by plugins that persist per-request usage and can
// aggregate it over a time window. This powers cross-session usage analytics in
// the console overview, independent of the in-memory per-process session stats.
type UsageSource interface {
	AggregateUsage(query UsageQuery) (UsageAggregate, error)
}

// --- Core format capabilities (Adapter path) ---

// CoreRequestMutator defines a plugin that can modify a CoreRequest.
type CoreRequestMutator interface {
	MutateCoreRequest(ctx context.Context, req *format.CoreRequest)
}

// CoreContentFilter defines a plugin that can filter Core content blocks.
type CoreContentFilter interface {
	FilterCoreContent(ctx context.Context, block *format.CoreContentBlock) bool
}

// CoreContentRememberer defines a plugin that can remember Core content blocks.
type CoreContentRememberer interface {
	RememberCoreContent(ctx context.Context, content []format.CoreContentBlock)
}

// PatchProxyDecider is implemented by plugins that control whether apply_patch
// custom tools are expanded into structured proxy tools for upstream models.
type PatchProxyDecider interface {
	DisablePatchProxy(model string) bool
}

// ResponseContentTransformer is implemented by plugins that transform
// response content blocks after they are received from the provider but
// before they are returned to the client. Used to convert provider-specific
// formats (e.g. DSML tool calls) into standard content blocks.
type ResponseContentTransformer interface {
	TransformResponseBlocks(ctx context.Context, model string, blocks []format.CoreContentBlock) []format.CoreContentBlock
}

// StreamBlocksTransformer is implemented by plugins that transform stream
// events before they reach the client. Used to intercept provider-specific
// formats (e.g. DSML tool calls) in streaming responses.
type StreamBlocksTransformer interface {
	TransformStreamBlocks(ctx context.Context, model string, events <-chan format.CoreStreamEvent) <-chan format.CoreStreamEvent
}
