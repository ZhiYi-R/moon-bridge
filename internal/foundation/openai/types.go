package openai

import "encoding/json"

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

type Tool struct {
	Type               string         `json:"type"`
	Name               string         `json:"name,omitempty"`
	Description        string         `json:"description,omitempty"`
	Parameters         map[string]any `json:"parameters,omitempty"`
	Format             map[string]any `json:"format,omitempty"`
	Tools              []Tool         `json:"tools,omitempty"`
	ExternalWebAccess  *bool          `json:"external_web_access,omitempty"`
	SearchContentTypes []string       `json:"search_content_types,omitempty"`
}

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

type ContentPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type Usage struct {
	InputTokens        int                `json:"input_tokens,omitempty"`
	OutputTokens       int                `json:"output_tokens,omitempty"`
	TotalTokens        int                `json:"total_tokens,omitempty"`
	InputTokensDetails InputTokensDetails `json:"input_tokens_details,omitempty"`
}

type InputTokensDetails struct {
	CachedTokens int `json:"cached_tokens"`
}

type IncompleteDetails struct {
	Reason string `json:"reason"`
}

type ErrorResponse struct {
	Error ErrorObject `json:"error"`
}

type ErrorObject struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Param   string `json:"param,omitempty"`
	Code    string `json:"code,omitempty"`
}

type StreamEvent struct {
	Event string
	Data  any
}

type ResponseLifecycleEvent struct {
	Type           string   `json:"type"`
	SequenceNumber int64    `json:"sequence_number"`
	Response       Response `json:"response"`
}

type OutputItemEvent struct {
	Type           string     `json:"type"`
	SequenceNumber int64      `json:"sequence_number"`
	OutputIndex    int        `json:"output_index"`
	Item           OutputItem `json:"item"`
}

type ContentPartEvent struct {
	Type           string      `json:"type"`
	SequenceNumber int64       `json:"sequence_number"`
	ItemID         string      `json:"item_id,omitempty"`
	OutputIndex    int         `json:"output_index"`
	ContentIndex   int         `json:"content_index"`
	Part           ContentPart `json:"part"`
}

type OutputTextDeltaEvent struct {
	Type           string `json:"type"`
	SequenceNumber int64  `json:"sequence_number"`
	ItemID         string `json:"item_id,omitempty"`
	OutputIndex    int    `json:"output_index"`
	ContentIndex   int    `json:"content_index"`
	Delta          string `json:"delta"`
}

type OutputTextDoneEvent struct {
	Type           string `json:"type"`
	SequenceNumber int64  `json:"sequence_number"`
	ItemID         string `json:"item_id,omitempty"`
	OutputIndex    int    `json:"output_index"`
	ContentIndex   int    `json:"content_index"`
	Text           string `json:"text"`
}

type FunctionCallArgumentsDeltaEvent struct {
	Type           string `json:"type"`
	SequenceNumber int64  `json:"sequence_number"`
	ItemID         string `json:"item_id,omitempty"`
	OutputIndex    int    `json:"output_index"`
	Delta          string `json:"delta"`
}

type FunctionCallArgumentsDoneEvent struct {
	Type           string `json:"type"`
	SequenceNumber int64  `json:"sequence_number"`
	ItemID         string `json:"item_id,omitempty"`
	OutputIndex    int    `json:"output_index"`
	Arguments      string `json:"arguments"`
}

type CustomToolCallInputDeltaEvent struct {
	Type           string `json:"type"`
	SequenceNumber int64  `json:"sequence_number"`
	ItemID         string `json:"item_id,omitempty"`
	CallID         string `json:"call_id,omitempty"`
	OutputIndex    int    `json:"output_index"`
	Delta          string `json:"delta"`
}

type ReasoningItemSummary struct {
	Type string `json:"type"`
	Text string `json:"text"`
}
