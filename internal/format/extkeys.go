// Package format defines protocol-agnostic Core types and Extensions key constants.
//
// Extensions keys provide a typed, discoverable registry for protocol-specific
// metadata that travels through CoreIR without a dedicated struct field.
package format

// Well-known Extensions map keys used across all protocol adapters.
//
// When adding a new key:
//  1. Add a constant here with a comment describing its value type and purpose.
//  2. Use the constant (not a string literal) in all adapter code.
//  3. Update GetExtensionsMap/SetExtension if the value type needs special handling.
const (
	// Codex tool map (serialized codextool.ToolMap for response-side reverse mapping).
	// Value type: map[string]any (from codextool.ToolMap.Encode()).
	ExtKeyCodexToolMap = "codex_tool_map"

	// OpenAI-specific fields bag.
	// Value type: map[string]any with keys: parallel_tool_calls, include, reasoning,
	// text, service_tier, previous_response_id, store.
	ExtKeyOpenAI = "openai"

	// DeepSeek thinking configuration.
	// Value type: map[string]any (e.g. {"budget_tokens": 4096}).
	ExtKeyThinking = "thinking"

	// Reasoning effort string (OpenAI Chat).
	// Value type: string ("low"|"medium"|"high"|"minimal").
	ExtKeyReasoningEffort = "reasoning_effort"

	// Cache metadata (prompt_cache_key, prompt_cache_retention).
	// Value type: map[string]string.
	ExtKeyCache = "cache"

	// Response creation timestamp.
	// Value type: int64 (Unix seconds, seconds precision is the common denominator).
	ExtKeyCreatedAt = "created_at"

	// Incomplete response reason (OpenAI Responses IncompleteDetails.Reason).
	// Value type: string ("max_output_tokens"|"content_filter" etc.).
	ExtKeyIncompleteReason = "incomplete_reason"

	// Protocol discriminator — identifies the protocol that produced the response.
	// Value type: string ("message"|"response"|"chat.completion"|"generateContentResponse").
	ExtKeyProtocol = "protocol"

	// Google Gemini PromptFeedback (safety ratings + block reason at response level).
	// Value type: map[string]any (serialized from *google.PromptFeedback).
	ExtKeyPromptFeedback = "prompt_feedback"

	// Google Gemini per-candidate SafetyRatings.
	// Value type: []map[string]any (serialized from []google.SafetyRating).
	ExtKeySafetyRatings = "safety_ratings"

	// Output item status (OpenAI Responses output item state).
	// Value type: string ("in_progress"|"completed").
	ExtKeyStatus = "status"

	// Per-content-block cache control.
	// Value type: map[string]any (e.g. {"type": "ephemeral"}).
	ExtKeyCacheControl = "cache_control"

	// End-user identifier (OpenAI both protocols).
	// Value type: string.
	ExtKeyUser = "user"

	// Tool strict schema enforcement flag.
	// Value type: bool.
	ExtKeyStrict = "strict"

	// Per-message reasoning content (DeepSeek-specific).
	// Value type: string.
	ExtKeyReasoningContent = "reasoning_content"

	// Stream event delta type discriminator.
	// Value type: string ("text"|"tool_args"|"reasoning").
	ExtKeyDeltaType = "delta_type"

	// Tool source type (OpenAI built-in tool types).
	// Value type: string ("web_search_preview"|"file_search"|"code_interpreter"|"computer_use_preview").
	ExtKeySourceType = "source_type"

	// Output tokens details breakdown (OpenAI Responses).
	// Value type: map[string]any (e.g. {"reasoning_tokens": 123}).
	ExtKeyOutputTokensDetails = "output_tokens_details"
)

// GetExtensionsMap returns the extensions map for a CoreRequest, creating it if nil.
func (req *CoreRequest) GetExtensionsMap() map[string]any {
	if req.Extensions == nil {
		req.Extensions = make(map[string]any)
	}
	return req.Extensions
}

// GetExtensionsMap returns the extensions map for a CoreResponse, creating it if nil.
func (resp *CoreResponse) GetExtensionsMap() map[string]any {
	if resp.Extensions == nil {
		resp.Extensions = make(map[string]any)
	}
	return resp.Extensions
}

// SetExtension sets a single extension value on a CoreRequest.
func SetExtension(req *CoreRequest, key string, value any) {
	req.GetExtensionsMap()[key] = value
}

// SetExtension sets a single extension value on a CoreResponse.
func (resp *CoreResponse) SetExtension(key string, value any) {
	resp.GetExtensionsMap()[key] = value
}
