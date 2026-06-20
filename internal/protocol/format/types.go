// Package format defines protocol-agnostic Core types for MoonBridge.
//
// These types serve as the intermediate representation between protocol-specific
// DTOs (Anthropic, OpenAI, etc.). All Adapter implementations convert to/from
// these Core types, keeping protocol conversion logic isolated.
//
// Clean room design: no imports from anthropic, openai, or any protocol-specific
// packages. Only Go standard library + encoding/json.
package format

import "encoding/json"

// ============================================================================
// Content Block
// ============================================================================

// CoreContentBlock represents a single content block in a message.
//
// It uses a single struct with a Type discriminator field and all payload fields
// flattened. Only fields relevant to the current Type are populated at any time.
//
// Type values:
//   - "text":        Text is populated
//   - "image":       ImageData + MediaType are populated
//   - "tool_use":    ToolUseID + ToolName + optional ToolNamespace + ToolInput are populated
//   - "tool_result": ToolUseID + ToolResultContent are populated
//   - "reasoning":   ReasoningText + ReasoningSignature are populated
type CoreContentBlock struct {
	// Type discriminator: "text" | "image" | "tool_use" | "tool_result" | "reasoning"
	Type string `json:"type"`

	// Text content (type = "text")
	Text string `json:"text,omitempty"`

	// Image content (type = "image")
	ImageData string `json:"image_data,omitempty"`
	MediaType string `json:"media_type,omitempty"`

	// Tool use (type = "tool_use")
	ToolUseID     string          `json:"tool_use_id,omitempty"`
	ToolName      string          `json:"tool_name,omitempty"`
	ToolNamespace string          `json:"tool_namespace,omitempty"`
	ToolInput     json.RawMessage `json:"tool_input,omitempty"`

	// Tool result (type = "tool_result")
	ToolResultContent []CoreContentBlock `json:"tool_result_content,omitempty"`

	// Reasoning (type = "reasoning")
	ReasoningText      string `json:"reasoning_text,omitempty"`
	ReasoningSignature string `json:"reasoning_signature,omitempty"`

	// Protocol-specific extensions (prompt cache, provider hints, etc.)
	Extensions map[string]any `json:"extensions,omitempty"`
}

// ============================================================================
// Message
// ============================================================================

// CoreMessage represents a single message in a conversation.
type CoreMessage struct {
	Role       string             `json:"role"`
	Content    []CoreContentBlock `json:"content"`
	Extensions map[string]any     `json:"extensions,omitempty"`
}

// ============================================================================
// Tool
// ============================================================================

// CoreTool describes a tool that the model may call.
type CoreTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema,omitempty"`
	Extensions  map[string]any `json:"extensions,omitempty"`
}

// ============================================================================
// Tool Call (auxiliary)
// ============================================================================

// CoreToolCall is an auxiliary type for typed tool call access.
//
// The actual payload lives in CoreContentBlock.ToolInput (json.RawMessage).
// This struct provides a convenience representation when the caller needs
// the three fields (ID, Name, Input) as a discrete tuple.
type CoreToolCall struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// ============================================================================
// Tool Choice
// ============================================================================

// CoreToolChoice expresses tool_choice across protocols.
//
// Mode covers common scalar variants; Name supports forced tool selection;
// Raw preserves the original inbound representation for lossless round-tripping.
type CoreToolChoice struct {
	Mode       string          `json:"mode,omitempty"` // "auto" | "none" | "required" | "any"
	Name       string          `json:"name,omitempty"`
	Raw        json.RawMessage `json:"raw,omitempty"`
	Extensions map[string]any  `json:"extensions,omitempty"`
}

// ============================================================================
// Request
// ============================================================================

// CoreThinkingConfig configures extended thinking/reasoning behavior.
// Used by Anthropic (thinking type + budget_tokens) and similar provider features.
// nil = use provider defaults.
type CoreThinkingConfig struct {
	Type         string `json:"type,omitempty"`          // "enabled" | "disabled"
	BudgetTokens int    `json:"budget_tokens,omitempty"` // token budget for thinking
}

// CoreOutputConfig controls output generation behavior.
// Anthropic uses "effort" to control reasoning effort.
// nil = use provider defaults.
type CoreOutputConfig struct {
	Effort string `json:"effort,omitempty"` // "low" | "medium" | "high" — provider-specific
}

// CacheControl enables prompt caching with optional TTL and breakpoint hints.
// Lifted from per-block level to request-level for uniform cache strategy.
// nil = cache disabled or use provider defaults.
type CoreCacheControl struct {
	Enabled    bool   `json:"enabled,omitempty"`
	TTLSeconds int    `json:"ttl_seconds,omitempty"`
	Strategy   string `json:"strategy,omitempty"` // "auto" | "manual" | "disabled"
}

// CoreRequest is the protocol-agnostic representation of an LLM request.
type CoreRequest struct {
	Model    string             `json:"model"`
	Messages []CoreMessage      `json:"messages"`
	System   []CoreContentBlock `json:"system,omitempty"`

	// Tools
	Tools      []CoreTool      `json:"tools,omitempty"`
	ToolChoice *CoreToolChoice `json:"tool_choice,omitempty"`

	// Sampling parameters
	Temperature *float64 `json:"temperature,omitempty"`
	TopP        *float64 `json:"top_p,omitempty"`
	MaxTokens   int      `json:"max_tokens,omitempty"`

	// TopK sampling parameter (Anthropic-specific, optional)
	TopK *int `json:"top_k,omitempty"`

	// Stop conditions
	StopSequences []string `json:"stop_sequences,omitempty"`

	// Streaming
	Stream bool `json:"stream,omitempty"`

	// Phase 5: Gemini safety/generation config support (D-02).
	// SafetySettings holds protocol-normalized safety configuration.
	// ProviderAdapters for Gemini read this and map to Gemini's safetySettings.
	// Zero value (nil) = not set — adapter uses provider defaults.
	SafetySettings map[string]any `json:"safety_settings,omitempty"`

	// GenerationConfig holds protocol-normalized generation configuration.
	// ProviderAdapters for Gemini read this and map to Gemini's generationConfig.
	// Zero value (nil) = not set — adapter uses provider defaults.
	GenerationConfig map[string]any `json:"generation_config,omitempty"`

	// Thinking controls extended thinking/reasoning behavior (e.g. Anthropic extended thinking).
	// nil = use provider defaults.
	Thinking *CoreThinkingConfig `json:"thinking,omitempty"`

	// Output controls output generation behavior (e.g. Anthropic effort level).
	// nil = use provider defaults.
	Output *CoreOutputConfig `json:"output,omitempty"`

	// CacheControl enables prompt caching at request level.
	// nil = cache disabled or use provider defaults.
	CacheControl *CoreCacheControl `json:"cache_control,omitempty"`

	// Metadata and extensions
	Metadata   map[string]any `json:"metadata,omitempty"`
	Extensions map[string]any `json:"extensions,omitempty"`
}

// ============================================================================
// Usage
// ============================================================================

// CoreUsage represents token usage statistics.
type CoreUsage struct {
	InputTokens       int `json:"input_tokens,omitempty"`
	OutputTokens      int `json:"output_tokens,omitempty"`
	TotalTokens       int `json:"total_tokens,omitempty"`
	CachedInputTokens int `json:"cached_input_tokens,omitempty"`
}

// ============================================================================
// Error
// ============================================================================

// CoreError represents an error returned by an LLM provider.
type CoreError struct {
	Message string `json:"message"`
	Type    string `json:"type,omitempty"`
	Code    string `json:"code,omitempty"`
}

// ============================================================================
// Response
// ============================================================================

// CoreResponse is the protocol-agnostic representation of an LLM response.
type CoreResponse struct {
	ID     string `json:"id"`
	Status string `json:"status"` // "completed" | "incomplete" | "failed" | "in_progress"
	Model  string `json:"model,omitempty"`

	Messages   []CoreMessage `json:"messages,omitempty"`
	Usage      CoreUsage     `json:"usage,omitempty"`
	Error      *CoreError    `json:"error,omitempty"`
	StopReason string        `json:"stop_reason,omitempty"`

	Extensions map[string]any `json:"extensions,omitempty"`
}

// ============================================================================
// Stream Event Type
// ============================================================================

// CoreStreamEventType categorises stream events in a protocol-agnostic way.
type CoreStreamEventType string

const (
	// Lifecycle events
	CoreEventCreated    CoreStreamEventType = "core.created"
	CoreEventInProgress CoreStreamEventType = "core.in_progress"
	CoreEventCompleted  CoreStreamEventType = "core.completed"
	CoreEventIncomplete CoreStreamEventType = "core.incomplete"
	CoreEventFailed     CoreStreamEventType = "core.failed"

	// Content block lifecycle
	CoreContentBlockStarted CoreStreamEventType = "core.content_block.started"
	CoreContentBlockDelta   CoreStreamEventType = "core.content_block.delta"
	CoreContentBlockDone    CoreStreamEventType = "core.content_block.done"

	// Text deltas
	CoreTextDelta CoreStreamEventType = "core.text.delta"
	CoreTextDone  CoreStreamEventType = "core.text.done"

	// Tool call arguments
	CoreToolCallArgsDelta CoreStreamEventType = "core.tool_call_args.delta"
	CoreToolCallArgsDone  CoreStreamEventType = "core.tool_call_args.done"

	// Output item
	CoreItemAdded CoreStreamEventType = "core.output_item.added"
	CoreItemDone  CoreStreamEventType = "core.output_item.done"

	// Ping
	CorePing CoreStreamEventType = "core.ping"
)

// ============================================================================
// Stream Event
// ============================================================================

// CoreStreamEvent represents a single stream event in protocol-agnostic form.
//
// Adapters that consume upstream SSE streams produce a <-chan CoreStreamEvent;
// adapters that deliver stream responses consume one.
type CoreStreamEvent struct {
	Type   CoreStreamEventType `json:"type"`
	SeqNum int64               `json:"seq_num,omitempty"`

	// Lifecycle fields
	Status string     `json:"status,omitempty"`
	Model  string     `json:"model,omitempty"`
	Error  *CoreError `json:"error,omitempty"`

	// Content block
	Index        int               `json:"index,omitempty"`
	ContentBlock *CoreContentBlock `json:"content_block,omitempty"`
	Delta        string            `json:"delta,omitempty"`

	// Metadata
	StopReason string     `json:"stop_reason,omitempty"`
	Usage      *CoreUsage `json:"usage,omitempty"`
	ItemID     string     `json:"item_id,omitempty"`

	// ChoiceIndex identifies which candidate/choice this event belongs to.
	// Used by Gemini (multi-candidate streaming) and OpenAI Chat (multi-choice).
	// nil = single candidate / not applicable.
	// Phase 5: multi-candidate streaming support (D-06).
	ChoiceIndex *int `json:"choice_index,omitempty"`

	// Protocol-specific extensions
	Extensions map[string]any `json:"extensions,omitempty"`
}

func isBase64Char(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '+' || c == '/'
}
