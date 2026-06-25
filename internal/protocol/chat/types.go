// Package chat implements the OpenAI Chat Completions ProviderAdapter for MoonBridge.
package chat

import "encoding/json"

// ============================================================================
// Request DTOs
// ============================================================================

// ChatRequest maps to OpenAI Chat Completions request body.
// https://platform.openai.com/docs/api-reference/chat/create
type ChatRequest struct {
	Model             string          `json:"model"`
	Messages          []ChatMessage   `json:"messages"`
	Temperature       *float64        `json:"temperature,omitempty"`
	TopP              *float64        `json:"top_p,omitempty"`
	MaxTokens         int             `json:"max_completion_tokens,omitempty"`
	Stop              []string        `json:"stop,omitempty"`
	Stream            bool            `json:"stream,omitempty"`
	Tools             []ChatTool      `json:"tools,omitempty"`
	ToolChoice        json.RawMessage `json:"tool_choice,omitempty"`
	ParallelToolCalls *bool           `json:"parallel_tool_calls,omitempty"`
	StreamOptions     *StreamOptions  `json:"stream_options,omitempty"`
	Metadata          map[string]any  `json:"metadata,omitempty"`
	User              string          `json:"user,omitempty"`
	// ReasoningEffort is the OpenAI Chat Completions reasoning_effort field
	// ("low" | "medium" | "high" | "minimal"). Populated from the inbound
	// OpenAI Responses request's reasoning.effort.
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
}

// ChatMessage represents a single message in the conversation.
type ChatMessage struct {
	Role       string     `json:"role"`              // "system" | "user" | "assistant" | "tool"
	Content    any        `json:"content,omitempty"` // string or []ContentPart
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
	// ReasoningContent is a non-standard field used by providers like DeepSeek
	// to return chain-of-thought reasoning. When present, it must be echoed
	// back in follow-up assistant messages.
	ReasoningContent string `json:"reasoning_content,omitempty"`
	// EmitEmptyReasoningContent forces JSON serialization of reasoning_content
	// as an explicit empty string when ReasoningContent == "". This is used by
	// DeepSeek fallback replay for assistant tool-call messages.
	EmitEmptyReasoningContent bool `json:"-"`
}

// MarshalJSON preserves normal omitempty behavior, except when
// EmitEmptyReasoningContent is true — then reasoning_content is emitted as "".
func (message ChatMessage) MarshalJSON() ([]byte, error) {
	type alias ChatMessage
	base := struct {
		alias
		ReasoningContent *string `json:"reasoning_content,omitempty"`
	}{
		alias: alias(message),
	}
	base.alias.EmitEmptyReasoningContent = false
	if message.ReasoningContent != "" || message.EmitEmptyReasoningContent {
		value := message.ReasoningContent
		base.ReasoningContent = &value
	}
	return json.Marshal(base)
}

// ContentPart is a multimodal content part (text or image_url).
type ContentPart struct {
	Type     string    `json:"type"` // "text" | "image_url"
	Text     string    `json:"text,omitempty"`
	ImageURL *ImageURL `json:"image_url,omitempty"`
}

// ImageURL represents an image URL in a content part.
type ImageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

// ChatTool describes a tool that the model may call.
type ChatTool struct {
	Type     string      `json:"type"` // "function"
	Function FunctionDef `json:"function"`
}

// FunctionDef describes a function definition for tool calling.
type FunctionDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
	Strict      *bool          `json:"strict,omitempty"`
}

// ToolCall represents a tool call in a response or streaming delta.
type ToolCall struct {
	Index    *int         `json:"index,omitempty"` // tool call position in streaming deltas
	ID       string       `json:"id"`
	Type     string       `json:"type"` // "function"
	Function ToolCallFunc `json:"function"`
}

// ToolCallFunc contains the function name and arguments for a tool call.
type ToolCallFunc struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// StreamOptions controls streaming behavior.
type StreamOptions struct {
	IncludeUsage bool `json:"include_usage,omitempty"`
}

// ============================================================================
// Non-Streaming Response DTOs
// ============================================================================

// ChatResponse is the non-streaming response from the Chat Completions API.
type ChatResponse struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
	Usage   *Usage   `json:"usage,omitempty"`
}

// Choice represents a single completion choice in a non-streaming response.
type Choice struct {
	Index        int         `json:"index"`
	Message      ChatMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

// Usage reports token usage from the API response.
type Usage struct {
	PromptTokens           int                    `json:"prompt_tokens"`
	CompletionTokens       int                    `json:"completion_tokens"`
	TotalTokens            int                    `json:"total_tokens"`
	PromptTokensDetails    *PromptTokensDetails    `json:"prompt_tokens_details,omitempty"`
	CompletionTokensDetails *CompletionTokensDetails `json:"completion_tokens_details,omitempty"`
}

// PromptTokensDetails provides a breakdown of prompt token counts.
type PromptTokensDetails struct {
	// CachedTokens is the number of tokens retrieved from the prompt cache.
	// Maps to CoreUsage.CachedInputTokens.
	CachedTokens int `json:"cached_tokens,omitempty"`
}

// CompletionTokensDetails provides a breakdown of completion token counts.
type CompletionTokensDetails struct {
	// ReasoningTokens is the number of tokens used for reasoning/thinking.
	ReasoningTokens int `json:"reasoning_tokens,omitempty"`
}

// ============================================================================
// Streaming Response DTOs (SSE data payloads)
// ============================================================================

// ChatStreamChunk is the SSE data payload for each streaming chunk.
// Each data: line contains one ChatStreamChunk.
// The final chunk before the data: [DONE] marker may contain usage.
type ChatStreamChunk struct {
	ID      string         `json:"id"`
	Object  string         `json:"object"`
	Created int64          `json:"created"`
	Model   string         `json:"model"`
	Choices []StreamChoice `json:"choices"`
	Usage   *Usage         `json:"usage,omitempty"` // only on final chunk when stream_options.include_usage
}

// StreamChoice represents a single streaming choice with delta content.
type StreamChoice struct {
	Index        int    `json:"index"`
	Delta        Delta  `json:"delta"`
	FinishReason string `json:"finish_reason,omitempty"`
}

// Delta contains the incremental content for a streaming choice.
type Delta struct {
	Role      string     `json:"role,omitempty"`
	Content   string     `json:"content,omitempty"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	// ReasoningContent is used by providers like DeepSeek to stream
	// chain-of-thought reasoning. Must be passed back in follow-up messages.
	ReasoningContent string `json:"reasoning_content,omitempty"`
}
