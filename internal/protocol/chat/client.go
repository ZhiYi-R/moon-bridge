// Package chat implements the OpenAI Chat Completions ProviderAdapter for MoonBridge.
//
// Client implements HTTP communication with the OpenAI Chat Completions API.
package chat

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"moonbridge/internal/format"
)

// ClientConfig configures the OpenAI Chat Completions HTTP client.
type ClientConfig struct {
	BaseURL   string
	APIKey    string
	Version   string
	UserAgent string
	Client    *http.Client
}

// Client is an HTTP client for the OpenAI Chat Completions API.
type Client struct {
	baseURL   string
	apiKey    string
	version   string
	userAgent string
	client    *http.Client
}

// NewClient creates a new OpenAI Chat API client.
//
// If cfg.Client is nil, http.DefaultClient is used.
// If cfg.BaseURL is empty, "https://api.openai.com" is used.
// If cfg.Version is empty, "" is used (Chat API has no version parameter).
func NewClient(cfg ClientConfig) *Client {
	httpClient := cfg.Client
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}
	baseURL = strings.TrimRight(baseURL, "/")
	// Normalize: if base URL already ends with /v1, strip it since
	// newRequest always appends /v1/chat/completions.
	if strings.HasSuffix(baseURL, "/v1") {
		baseURL = baseURL[:len(baseURL)-3]
	}

	return &Client{
		baseURL:   baseURL,
		apiKey:    cfg.APIKey,
		version:   strings.TrimSpace(cfg.Version),
		userAgent: strings.TrimSpace(cfg.UserAgent),
		client:    httpClient,
	}
}

// CreateChat sends a non-streaming chat completion request.
func (c *Client) CreateChat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	log := slog.Default().With("model", req.Model)
	log.Debug("sending chat completion request", "messages", len(req.Messages))

	// Ensure stream is false for non-streaming requests.
	req.Stream = false

	httpReq, err := c.newRequest(ctx, req)
	if err != nil {
		return nil, err
	}

	response, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("chat API request failed: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode > 299 {
		body, _ := io.ReadAll(response.Body)
		return nil, fmt.Errorf("chat API error: status=%d body=%s", response.StatusCode, string(body))
	}

	var result ChatResponse
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("chat API response decode: %w", err)
	}

	log.Info("chat completion completed",
		"id", result.ID,
		"choices", len(result.Choices),
		"prompt_tokens", safeUsage(result.Usage).PromptTokens,
		"completion_tokens", safeUsage(result.Usage).CompletionTokens,
	)
	return &result, nil
}

// StreamChat sends a streaming chat completion request and returns a channel
// of ChatStreamChunk.
//
// stream_options.include_usage is always set to true so that the final chunk
// contains token usage information.
//
// The caller MUST consume the channel until it is closed. The read-loop
// goroutine terminates when the HTTP body is fully read (data: [DONE] marker),
// the context is cancelled, or an unrecoverable error occurs.
func (c *Client) StreamChat(ctx context.Context, req *ChatRequest) (<-chan ChatStreamChunk, error) {
	log := slog.Default().With("model", req.Model)
	log.Debug("starting streaming chat completion", "messages", len(req.Messages))

	// Configure streaming.
	req.Stream = true
	req.StreamOptions = &StreamOptions{
		IncludeUsage: true,
	}

	httpReq, err := c.newRequest(ctx, req)
	if err != nil {
		return nil, err
	}

	response, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("chat API stream request failed: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		return nil, fmt.Errorf("chat API stream error: status=%d body=%s", response.StatusCode, string(body))
	}

	ch := make(chan ChatStreamChunk, 64)
	go c.readStream(ctx, response.Body, ch)
	return ch, nil
}

// Close implements io.Closer. The Chat client has no persistent resources
// to close (connections are managed by http.Client), so this is a no-op.
func (c *Client) Close() error { return nil }

// ============================================================================
// Internal helpers
// ============================================================================

// newRequest builds an HTTP POST request for the Chat Completions API.
func (c *Client) newRequest(ctx context.Context, req *ChatRequest) (*http.Request, error) {
	wireReq := *req
	wireReq.Messages = legalizeChatToolCallWireMessages(req.Messages)
	wireReq.Tools = normalizeChatToolSchemas(req.Tools)
	data, err := json.Marshal(&wireReq)
	if err != nil {
		return nil, fmt.Errorf("chat API request marshal: %w", err)
	}

	url := c.baseURL + "/v1/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("chat API request build: %w", err)
	}
	httpReq.Header.Set("content-type", "application/json")
	httpReq.Header.Set("authorization", "Bearer "+c.apiKey)
	if c.userAgent != "" {
		httpReq.Header.Set("user-agent", c.userAgent)
	}
	return httpReq, nil
}

func legalizeChatToolCallWireMessages(messages []ChatMessage) []ChatMessage {
	if len(messages) == 0 {
		return messages
	}

	normalized := make([]ChatMessage, 0, len(messages))
	usedToolCallIDs := make(map[string]struct{})
	activeToolCallIDRewrite := make(map[string]string)
	toolResultIndexByID := make(map[string]int)

	for _, msg := range messages {
		if len(msg.ToolCalls) > 0 {
			activeToolCallIDRewrite = make(map[string]string, len(msg.ToolCalls))
			toolResultIndexByID = make(map[string]int)
			msg.ToolCalls = legalizeChatToolCalls(msg.ToolCalls, usedToolCallIDs, activeToolCallIDRewrite)
		}
		if msg.Role != "tool" || msg.ToolCallID == "" {
			normalized = append(normalized, msg)
			continue
		}
		if rewrittenID, ok := activeToolCallIDRewrite[msg.ToolCallID]; ok {
			msg.ToolCallID = rewrittenID
		}
		if existingIndex, ok := toolResultIndexByID[msg.ToolCallID]; ok {
			normalized[existingIndex].Content = mergeChatToolResultContent(normalized[existingIndex].Content, msg.Content)
			continue
		}
		normalized = append(normalized, msg)
		toolResultIndexByID[msg.ToolCallID] = len(normalized) - 1
	}

	return normalized
}

func legalizeChatToolCalls(
	toolCalls []ToolCall,
	usedToolCallIDs map[string]struct{},
	activeToolCallIDRewrite map[string]string,
) []ToolCall {
	if len(toolCalls) == 0 {
		return toolCalls
	}
	legalized := make([]ToolCall, len(toolCalls))
	copy(legalized, toolCalls)
	for i := range legalized {
		originalID := legalized[i].ID
		emittedID := allocateChatToolCallID(originalID, usedToolCallIDs)
		if originalID != "" {
			activeToolCallIDRewrite[originalID] = emittedID
			legalized[i].ID = emittedID
		}
	}
	return legalized
}

func allocateChatToolCallID(id string, used map[string]struct{}) string {
	if id == "" {
		return id
	}
	if _, ok := used[id]; !ok {
		used[id] = struct{}{}
		return id
	}
	for suffix := 2; ; suffix++ {
		candidate := fmt.Sprintf("%s__mb%d", id, suffix)
		if _, ok := used[candidate]; !ok {
			used[candidate] = struct{}{}
			return candidate
		}
	}
}

func mergeChatToolResultContent(existing, incoming any) any {
	if existingParts, ok := existing.([]ContentPart); ok {
		return mergeChatContentParts(existingParts, incoming)
	}
	if incomingParts, ok := incoming.([]ContentPart); ok {
		return mergeChatContentParts(contentPartsFromAny(existing), incomingParts)
	}
	if existingItems, ok := existing.([]any); ok {
		return mergeChatAnyContent(existingItems, incoming)
	}
	if incomingItems, ok := incoming.([]any); ok {
		return mergeChatAnyContent(anyContentFromValue(existing), incomingItems)
	}

	existingText := chatToolResultContentText(existing)
	incomingText := chatToolResultContentText(incoming)
	if existingText == "" {
		return incomingText
	}
	if incomingText == "" {
		return existingText
	}
	return existingText + "\n" + incomingText
}

func mergeChatContentParts(existing []ContentPart, incoming any) []ContentPart {
	merged := append([]ContentPart(nil), existing...)
	incomingParts := contentPartsFromAny(incoming)
	if len(merged) > 0 && len(incomingParts) > 0 {
		merged = append(merged, ContentPart{Type: "text", Text: "\n"})
	}
	merged = append(merged, incomingParts...)
	return merged
}

func contentPartsFromAny(content any) []ContentPart {
	switch v := content.(type) {
	case nil:
		return nil
	case string:
		if v == "" {
			return nil
		}
		return []ContentPart{{Type: "text", Text: v}}
	case []ContentPart:
		return append([]ContentPart(nil), v...)
	case []any:
		parts := make([]ContentPart, 0, len(v))
		for _, item := range v {
			switch typed := item.(type) {
			case ContentPart:
				parts = append(parts, typed)
			case map[string]any:
				parts = append(parts, contentPartFromMap(typed))
			default:
				if text := chatToolResultContentText(typed); text != "" {
					parts = append(parts, ContentPart{Type: "text", Text: text})
				}
			}
		}
		return parts
	default:
		if text := chatToolResultContentText(v); text != "" {
			return []ContentPart{{Type: "text", Text: text}}
		}
		return nil
	}
}

func contentPartFromMap(item map[string]any) ContentPart {
	part := ContentPart{}
	if value, ok := item["type"].(string); ok {
		part.Type = value
	}
	if value, ok := item["text"].(string); ok {
		part.Text = value
	}
	if rawImageURL, ok := item["image_url"]; ok {
		if imageURL, ok := rawImageURL.(map[string]any); ok {
			if value, ok := imageURL["url"].(string); ok {
				part.ImageURL = &ImageURL{URL: value}
				if detail, ok := imageURL["detail"].(string); ok {
					part.ImageURL.Detail = detail
				}
			}
		}
	}
	return part
}

func mergeChatAnyContent(existing []any, incoming any) []any {
	merged := append([]any(nil), existing...)
	incomingItems := anyContentFromValue(incoming)
	if len(merged) > 0 && len(incomingItems) > 0 {
		merged = append(merged, map[string]any{"type": "text", "text": "\n"})
	}
	merged = append(merged, incomingItems...)
	return merged
}

func anyContentFromValue(content any) []any {
	switch v := content.(type) {
	case nil:
		return nil
	case string:
		if v == "" {
			return nil
		}
		return []any{map[string]any{"type": "text", "text": v}}
	case []any:
		return append([]any(nil), v...)
	case []ContentPart:
		items := make([]any, 0, len(v))
		for _, part := range v {
			items = append(items, part)
		}
		return items
	default:
		return []any{v}
	}
}

func chatToolResultContentText(content any) string {
	switch v := content.(type) {
	case nil:
		return ""
	case string:
		return v
	case []ContentPart:
		var text string
		for _, part := range v {
			if part.Text != "" {
				text += part.Text
			}
		}
		return text
	case []any:
		var text string
		for _, item := range v {
			if part, ok := item.(map[string]any); ok {
				if value, ok := part["text"].(string); ok {
					text += value
				}
			}
		}
		return text
	default:
		data, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		return string(data)
	}
}

func normalizeChatToolSchemas(tools []ChatTool) []ChatTool {
	if len(tools) == 0 {
		return tools
	}
	normalized := make([]ChatTool, len(tools))
	copy(normalized, tools)
	for i := range normalized {
		if normalized[i].Function.Parameters != nil {
			normalized[i].Function.Parameters = normalizeProviderSchemaForSend(normalized[i].Function.Parameters)
		}
	}
	return normalized
}

func normalizeProviderSchemaForSend(schema map[string]any) map[string]any {
	if normalized := format.NormalizeFunctionToolSchema(schema); normalized != nil {
		return normalized
	}
	return map[string]any{"type": "object"}
}

// readStream reads SSE lines from the HTTP response body and sends parsed
// ChatStreamChunk into the channel. Closes the channel when the stream ends.
func (c *Client) readStream(ctx context.Context, body io.ReadCloser, ch chan<- ChatStreamChunk) {
	defer close(ch)
	defer body.Close()

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)

	for scanner.Scan() {
		line := scanner.Text()

		// Skip empty lines.
		if line == "" {
			continue
		}

		// Check for the terminal DONE marker.
		if line == "data: [DONE]" {
			return
		}

		// Only process data: lines.
		if !strings.HasPrefix(line, "data:") {
			continue
		}

		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" {
			continue
		}

		var chunk ChatStreamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			slog.Warn("chat API stream parse error", "error", err, "data", data[:min(len(data), 200)])
			continue
		}

		select {
		case ch <- chunk:
		case <-ctx.Done():
			return
		}
	}

	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		slog.Warn("chat API stream scanner error", "error", err)
	}
}

// safeUsage returns a non-nil Usage pointer for safe field access.
func safeUsage(u *Usage) Usage {
	if u == nil {
		return Usage{}
	}
	return *u
}
