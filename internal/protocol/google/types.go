// Package google implements the Google Generative AI (Gemini) ProviderAdapter for MoonBridge.
package google

import "encoding/json"

// ============================================================================
// Request DTOs
// ============================================================================

// GenerateContentRequest maps to Gemini's generateContent request body.
// https://ai.google.dev/api/generate-content
type GenerateContentRequest struct {
	Contents          []Content         `json:"contents"`
	SystemInstruction *Content          `json:"systemInstruction,omitempty"`
	SafetySettings    []SafetySetting   `json:"safetySettings,omitempty"`
	GenerationConfig  *GenerationConfig `json:"generationConfig,omitempty"`
	Tools             []Tool            `json:"tools,omitempty"`
	ToolConfig        json.RawMessage   `json:"toolConfig,omitempty"`
	// CachedContent references a CachedContent resource for prompt caching.
	// When set, system_instruction, tools, and tool_config must not be set
	// (Gemini API constraint — they become part of the cached content).
	CachedContent string `json:"cachedContent,omitempty"`
}

// Content represents a single message content in Gemini's format.
// Role is "user" or "model" (not "assistant").
type Content struct {
	Role  string `json:"role,omitempty"` // "user" | "model"
	Parts []Part `json:"parts"`
}

// Part represents a single part within Content.
type Part struct {
	Text             string            `json:"text,omitempty"`
	InlineData       *Blob             `json:"inlineData,omitempty"`
	FileData         *FileData         `json:"fileData,omitempty"`
	FunctionCall     *FunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *FunctionResponse `json:"functionResponse,omitempty"`
}

// Blob represents inline binary data.
type Blob struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

// FileData represents a reference to external file data.
type FileData struct {
	MimeType string `json:"mimeType"`
	FileURI  string `json:"fileUri"`
}

// FunctionCall represents a function call request from the model.
type FunctionCall struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

// FunctionResponse represents a function call response from the user.
type FunctionResponse struct {
	Name     string          `json:"name"`
	Response json.RawMessage `json:"response"`
}

// SafetySetting configures content safety filtering.
type SafetySetting struct {
	Category  string `json:"category"`
	Threshold string `json:"threshold"`
}

// GenerationConfig controls text generation parameters.
type GenerationConfig struct {
	Temperature      *float64 `json:"temperature,omitempty"`
	TopP             *float64 `json:"topP,omitempty"`
	TopK             *float64 `json:"topK,omitempty"`
	MaxOutputTokens  int      `json:"maxOutputTokens,omitempty"`
	StopSequences    []string `json:"stopSequences,omitempty"`
	ResponseMimeType string   `json:"responseMimeType,omitempty"`
	CandidateCount   int      `json:"candidateCount,omitempty"`
}

// Tool represents a tool available to the model.
type Tool struct {
	FunctionDeclarations []FunctionDeclaration `json:"functionDeclarations,omitempty"`
}

// FunctionDeclaration declares a function that the model may call.
type FunctionDeclaration struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

// ============================================================================
// Response DTOs
// ============================================================================

// GenerateContentResponse maps to a Gemini streaming or non-streaming response chunk.
// In streaming mode (streamGenerateContent), each SSE data: line contains one
// complete GenerateContentResponse snapshot.
type GenerateContentResponse struct {
	Candidates     []Candidate     `json:"candidates"`
	PromptFeedback *PromptFeedback `json:"promptFeedback,omitempty"`
	UsageMetadata  *UsageMetadata  `json:"usageMetadata,omitempty"`
}

// Candidate represents a single response candidate.
type Candidate struct {
	Index         int            `json:"index"`
	Content       Content        `json:"content"`
	FinishReason  string         `json:"finishReason"` // STOP, MAX_TOKENS, SAFETY, RECITATION, OTHER
	SafetyRatings []SafetyRating `json:"safetyRatings,omitempty"`
}

// SafetyRating represents a safety rating for a category.
type SafetyRating struct {
	Category    string `json:"category"`
	Probability string `json:"probability"`
	Blocked     bool   `json:"blocked,omitempty"`
}

// PromptFeedback contains feedback on the prompt's safety.
type PromptFeedback struct {
	SafetyRatings []SafetyRating `json:"safetyRatings,omitempty"`
	BlockReason   string         `json:"blockReason,omitempty"`
}

// UsageMetadata contains token count information.
type UsageMetadata struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
	// CachedContentTokenCount is the number of tokens served from context cache.
	// Maps to CoreUsage.CachedInputTokens.
	CachedContentTokenCount int `json:"cachedContentTokenCount,omitempty"`
}

// ============================================================================
// CachedContent DTOs
// ============================================================================

// CachedContentUsageMetadata contains token count info for a CachedContent resource.
type CachedContentUsageMetadata struct {
	TotalTokenCount int `json:"totalTokenCount"`
}

// CachedContent represents a Google Gemini CachedContent resource.
type CachedContent struct {
	Name              string                      `json:"name,omitempty"`
	Model             string                      `json:"model"`
	DisplayName       string                      `json:"displayName,omitempty"`
	Contents          []Content                   `json:"contents"`
	SystemInstruction *Content                    `json:"systemInstruction,omitempty"`
	Tools             []Tool                      `json:"tools,omitempty"`
	ToolConfig        json.RawMessage             `json:"toolConfig,omitempty"`
	TTL               string                      `json:"ttl,omitempty"`
	ExpireTime        string                      `json:"expireTime,omitempty"`
	CreateTime        string                      `json:"createTime,omitempty"`
	UpdateTime        string                      `json:"updateTime,omitempty"`
	UsageMetadata     *CachedContentUsageMetadata `json:"usageMetadata,omitempty"`
}

// CreateCachedContentRequest is the request body for POST /cachedContents.
type CreateCachedContentRequest CachedContent

// UpdateCachedContentRequest is the request body for PATCH /cachedContents/{name}.
type UpdateCachedContentRequest struct {
	TTL        string `json:"ttl"`
	ExpireTime string `json:"expireTime,omitempty"`
}
