// Package openai contains OpenAI API DTO types.
//
// These types represent the OpenAI Responses API request/response format.
// They are protocol-specific and should only be imported by the OpenAI adapter
// and server dispatch code.
//
// NOTE: This package was migrated from internal/foundation/openai/. The original
// package now serves as a re-export layer for backward compatibility.
package openai

import "encoding/json"

// ============================================================================
// Request
// ============================================================================

// ResponsesRequest represents an OpenAI Responses API request.
type ResponsesRequest struct {
	Model                string          `json:"model"`
	Input                json.RawMessage `json:"input,omitempty"`
	Instructions         string          `json:"instructions,omitempty"`
	MaxOutputTokens      int             `json:"max_output_tokens,omitempty"`
	Temperature          *float64        `json:"temperature,omitempty"`
	TopP                 *float64        `json:"top_p,omitempty"`
	Stop                 json.RawMessage `json:"stop,omitempty"`
	Tools                []Tool          `json:"tools,omitempty"`
	ToolChoice           json.RawMessage `json:"tool_choice,omitempty"`
	ParallelToolCalls    *bool           `json:"parallel_tool_calls,omitempty"`
	Stream               bool            `json:"stream,omitempty"`
	Store                *bool           `json:"store,omitempty"`
	PreviousResponseID   string          `json:"previous_response_id,omitempty"`
	Include              []string        `json:"include,omitempty"`
	Reasoning            map[string]any  `json:"reasoning,omitempty"`
	Text                 map[string]any  `json:"text,omitempty"`
	ServiceTier          string          `json:"service_tier,omitempty"`
	ClientMetadata       map[string]any  `json:"client_metadata,omitempty"`
	Metadata             map[string]any  `json:"metadata,omitempty"`
	User                 string          `json:"user,omitempty"`
	PromptCacheKey       string          `json:"prompt_cache_key,omitempty"`
	PromptCacheRetention string          `json:"prompt_cache_retention,omitempty"`
}

// Tool represents an OpenAI tool definition.
type Tool struct {
	Type               string         `json:"type"`
	Name               string         `json:"name,omitempty"`
	Description        string         `json:"description,omitempty"`
	Parameters         map[string]any `json:"parameters,omitempty"`
	Strict             *bool          `json:"strict,omitempty"`
	Format             map[string]any `json:"format,omitempty"`
	Tools              []Tool         `json:"tools,omitempty"`
	ExternalWebAccess  *bool          `json:"external_web_access,omitempty"`
	SearchContentTypes []string       `json:"search_content_types,omitempty"`
	MaxNumResults      int            `json:"max_num_results,omitempty"`
	DisplayWidth       int            `json:"display_width,omitempty"`
	DisplayHeight      int            `json:"display_height,omitempty"`
}

// ============================================================================
// Response
// ============================================================================

// Response represents an OpenAI Responses API response.
type Response struct {
	ID                string             `json:"id"`
	Object            string             `json:"object"`
	CreatedAt         int64              `json:"created_at,omitempty"`
	Status            string             `json:"status"`
	Model             string             `json:"model,omitempty"`
	Output            []OutputItem       `json:"output"`
	OutputText        string             `json:"output_text,omitempty"`
	Usage             Usage              `json:"usage,omitempty"`
	Metadata          map[string]any     `json:"metadata,omitempty"`
	IncompleteDetails *IncompleteDetails `json:"incomplete_details,omitempty"`
	Error             *ErrorObject       `json:"error,omitempty"`
}

// OutputItem represents an output item in an OpenAI response.
type OutputItem struct {
	Type      string                 `json:"type"`
	ID        string                 `json:"id,omitempty"`
	Status    string                 `json:"status,omitempty"`
	Role      string                 `json:"role,omitempty"`
	Content   []ContentPart          `json:"content,omitempty"`
	CallID    string                 `json:"call_id,omitempty"`
	Name      string                 `json:"name,omitempty"`
	Namespace string                 `json:"namespace,omitempty"`
	Arguments string                 `json:"arguments,omitempty"`
	Input     string                 `json:"input,omitempty"`
	Action    *ToolAction            `json:"action,omitempty"`
	Summary   []ReasoningItemSummary `json:"summary,omitempty"`
}

// ToolAction describes an action associated with a tool.
type ToolAction struct {
	Type             string            `json:"type,omitempty"`
	Command          []string          `json:"command,omitempty"`
	WorkingDirectory string            `json:"working_directory,omitempty"`
	TimeoutMS        int               `json:"timeout_ms,omitempty"`
	Env              map[string]string `json:"env,omitempty"`
	Query            string            `json:"query,omitempty"`
	Queries          []string          `json:"queries,omitempty"`
	URL              string            `json:"url,omitempty"`
	Pattern          string            `json:"pattern,omitempty"`
}

// ContentPart represents a content part within an output item.
type ContentPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// ============================================================================
// Usage & Metadata
// ============================================================================

// Usage represents token usage statistics.
type Usage struct {
	InputTokens         int                 `json:"input_tokens,omitempty"`
	OutputTokens        int                 `json:"output_tokens,omitempty"`
	TotalTokens         int                 `json:"total_tokens"`
	InputTokensDetails  InputTokensDetails  `json:"input_tokens_details,omitempty"`
	OutputTokensDetails OutputTokensDetails `json:"output_tokens_details,omitempty"`
}

// InputTokensDetails provides detailed input token breakdown.
type InputTokensDetails struct {
	CachedTokens int `json:"cached_tokens"`
}

// OutputTokensDetails provides detailed output token breakdown.
type OutputTokensDetails struct {
	ReasoningTokens int `json:"reasoning_tokens"`
}

// IncompleteDetails describes why a response was incomplete.
type IncompleteDetails struct {
	Reason string `json:"reason"`
}

// ============================================================================
// Error
// ============================================================================

// ErrorResponse wraps an error object for error responses.
type ErrorResponse struct {
	Error ErrorObject `json:"error"`
}

// ErrorObject represents an error returned by the API.
type ErrorObject struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Param   string `json:"param,omitempty"`
	Code    string `json:"code,omitempty"`
}

// ============================================================================
// Streaming
// ============================================================================

// StreamEvent is a generic wrapper for SSE stream events.
// Event holds the SSE event type string, Data holds the typed payload.
type StreamEvent struct {
	Event string `json:"event"`
	Data  any    `json:"data"`
}

// ResponseLifecycleEvent is emitted for response lifecycle events
// (response.created, response.in_progress, response.completed, response.failed).
type ResponseLifecycleEvent struct {
	Type           string   `json:"type"`
	SequenceNumber int64    `json:"sequence_number"`
	Response       Response `json:"response"`
}

// OutputItemEvent is emitted when an output item is added or completed.
type OutputItemEvent struct {
	Type           string     `json:"type"`
	SequenceNumber int64      `json:"sequence_number"`
	OutputIndex    int        `json:"output_index"`
	Item           OutputItem `json:"item"`
}

// ContentPartEvent is emitted when a content part is added.
type ContentPartEvent struct {
	Type           string      `json:"type"`
	SequenceNumber int64       `json:"sequence_number"`
	ItemID         string      `json:"item_id,omitempty"`
	OutputIndex    int         `json:"output_index"`
	ContentIndex   int         `json:"content_index"`
	Part           ContentPart `json:"part"`
}

// OutputTextDeltaEvent is emitted for text delta streaming.
type OutputTextDeltaEvent struct {
	Type           string `json:"type"`
	SequenceNumber int64  `json:"sequence_number"`
	ItemID         string `json:"item_id,omitempty"`
	OutputIndex    int    `json:"output_index"`
	ContentIndex   int    `json:"content_index"`
	Delta          string `json:"delta"`
}

// OutputTextDoneEvent is emitted when text output is complete.
type OutputTextDoneEvent struct {
	Type           string `json:"type"`
	SequenceNumber int64  `json:"sequence_number"`
	ItemID         string `json:"item_id,omitempty"`
	OutputIndex    int    `json:"output_index"`
	ContentIndex   int    `json:"content_index"`
	Text           string `json:"text"`
}

// FunctionCallArgumentsDeltaEvent is emitted for function call argument deltas.
type FunctionCallArgumentsDeltaEvent struct {
	Type           string `json:"type"`
	SequenceNumber int64  `json:"sequence_number"`
	ItemID         string `json:"item_id,omitempty"`
	OutputIndex    int    `json:"output_index"`
	Delta          string `json:"delta"`
}

// FunctionCallArgumentsDoneEvent is emitted when function call arguments are complete.
type FunctionCallArgumentsDoneEvent struct {
	Type           string `json:"type"`
	SequenceNumber int64  `json:"sequence_number"`
	ItemID         string `json:"item_id,omitempty"`
	OutputIndex    int    `json:"output_index"`
	Arguments      string `json:"arguments"`
}

// CustomToolCallInputDeltaEvent is emitted for custom tool call input deltas.
type CustomToolCallInputDeltaEvent struct {
	Type           string `json:"type"`
	SequenceNumber int64  `json:"sequence_number"`
	ItemID         string `json:"item_id,omitempty"`
	CallID         string `json:"call_id,omitempty"`
	OutputIndex    int    `json:"output_index"`
	Delta          string `json:"delta"`
}

// ReasoningItemSummary provides a summary of reasoning content.
type ReasoningItemSummary struct {
	// Signature is the provider-specific reasoning signature (e.g. DeepSeek thinking signature)
	// required for continuing thinking chains across conversation turns.
	Type      string `json:"type"`
	Text      string `json:"text"`
	Signature string `json:"signature,omitempty"`
}

// ============================================================================
// Reasoning Streaming Events
// ============================================================================

// ReasoningSummaryPartAddedEvent is emitted when a reasoning summary part is added.
type ReasoningSummaryPartAddedEvent struct {
	Type           string `json:"type"`
	SequenceNumber int64  `json:"sequence_number"`
	ItemID         string `json:"item_id"`
	OutputIndex    int    `json:"output_index"`
	SummaryIndex   int    `json:"summary_index"`
}

// ReasoningSummaryTextDeltaEvent is emitted for reasoning text deltas.
type ReasoningSummaryTextDeltaEvent struct {
	Type           string `json:"type"`
	SequenceNumber int64  `json:"sequence_number"`
	ItemID         string `json:"item_id"`
	OutputIndex    int    `json:"output_index"`
	SummaryIndex   int    `json:"summary_index"`
	Delta          string `json:"delta"`
}

// ReasoningSummaryPartDoneEvent is emitted when a reasoning summary part is complete.
type ReasoningSummaryPartDoneEvent struct {
	Type           string `json:"type"`
	SequenceNumber int64  `json:"sequence_number"`
	ItemID         string `json:"item_id"`
	OutputIndex    int    `json:"output_index"`
	SummaryIndex   int    `json:"summary_index"`
}
