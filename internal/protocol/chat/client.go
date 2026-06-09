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

	"moonbridge/internal/config"
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
	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("chat API request marshal: %w", err)
	}

	url := c.baseURL + "/v1/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("chat API request build: %w", err)
	}
	httpReq.Header.Set("content-type", "application/json")
	httpReq.Header.Set("authorization", "Bearer "+c.effectiveAPIKey(ctx))
	if c.userAgent != "" {
		httpReq.Header.Set("user-agent", c.userAgent)
	}
	return httpReq, nil
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

// effectiveAPIKey returns the transformed auth token from context if available,
// otherwise falls back to the client's configured API key.
func (c *Client) effectiveAPIKey(ctx context.Context) string {
	if token, ok := config.TransformAuthTokenFromContext(ctx); ok {
		return token
	}
	return c.apiKey
}
