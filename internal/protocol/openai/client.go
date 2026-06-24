// Package openai contains the OpenAI Responses protocol types, adapters, and client.
package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
)

// ============================================================================
// Client — HTTP client for the OpenAI Responses API
// ============================================================================

// ClientConfig configures the OpenAI Responses API HTTP client.
type ClientConfig struct {
	BaseURL   string
	APIKey    string
	UserAgent string
	Client    *http.Client
}

// Client is an HTTP client for the OpenAI Responses API.
type Client struct {
	baseURL   string
	apiKey    string
	userAgent string
	client    *http.Client
}

// NewClient creates a new OpenAI Responses API client.
//
// If cfg.Client is nil, http.DefaultClient is used.
// If cfg.BaseURL is empty, "https://api.openai.com" is used.
func NewClient(cfg ClientConfig) *Client {
	httpClient := cfg.Client
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}
	return &Client{
		baseURL:   baseURL,
		apiKey:    cfg.APIKey,
		userAgent: strings.TrimSpace(cfg.UserAgent),
		client:    httpClient,
	}
}

// Close implements io.Closer. No persistent resources to close.
func (c *Client) Close() error { return nil }

// CreateResponse sends a non-streaming OpenAI Responses API request.
func (c *Client) CreateResponse(ctx context.Context, req *ResponsesRequest) (*Response, error) {
	log := slog.Default().With("model", req.Model)
	log.Debug("sending responses request")

	// Ensure stream is false for non-streaming.
	req.Stream = false

	httpReq, err := c.buildRequest(ctx, req)
	if err != nil {
		return nil, err
	}

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("responses API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("responses API error: status=%d body=%s", resp.StatusCode, string(body))
	}

	var result Response
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("responses API response decode: %w", err)
	}

	log.Info("responses completion completed",
		"id", result.ID,
		"output_items", len(result.Output),
		"input_tokens", result.Usage.InputTokens,
		"output_tokens", result.Usage.OutputTokens,
	)
	return &result, nil
}

// StreamResponse sends a streaming Responses API request and returns a channel
// of raw StreamEvent values.
//
// The caller MUST consume the channel until it is closed. The read-loop
// goroutine terminates when the stream ends or the context is cancelled.
func (c *Client) StreamResponse(ctx context.Context, req *ResponsesRequest) (<-chan StreamEvent, error) {
	log := slog.Default().With("model", req.Model)
	log.Debug("starting streaming responses request")

	req.Stream = true

	httpReq, err := c.buildRequest(ctx, req)
	if err != nil {
		return nil, err
	}

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("responses API stream request failed: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("responses API stream error: status=%d body=%s", resp.StatusCode, string(body))
	}

	ch := make(chan StreamEvent, 64)
	go c.readStream(ctx, resp.Body, ch)
	return ch, nil
}

// ============================================================================
// Internal helpers
// ============================================================================

// buildRequest creates an HTTP POST request for the Responses API.
func (c *Client) buildRequest(ctx context.Context, req *ResponsesRequest) (*http.Request, error) {
	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("responses API request marshal: %w", err)
	}

	url := c.baseURL + "/v1/responses"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("responses API request build: %w", err)
	}
	httpReq.Header.Set("content-type", "application/json")
	httpReq.Header.Set("authorization", "Bearer "+c.apiKey)
	if c.userAgent != "" {
		httpReq.Header.Set("user-agent", c.userAgent)
	}
	return httpReq, nil
}

// readStream reads SSE lines from the HTTP response body and sends parsed
// StreamEvent into the channel. Handles the OpenAI Responses SSE format with
// named events (event: + data:).
func (c *Client) readStream(ctx context.Context, body io.ReadCloser, ch chan<- StreamEvent) {
	defer close(ch)
	defer body.Close()

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)

	var currentEvent string

	for scanner.Scan() {
		line := scanner.Text()

		// Track event type line.
		if strings.HasPrefix(line, "event:") {
			currentEvent = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}

		// Data line.
		if strings.HasPrefix(line, "data:") {
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "" {
				currentEvent = ""
				continue
			}

			ev := StreamEvent{
				Event: currentEvent,
				Data:  json.RawMessage(data),
			}
			currentEvent = ""

			select {
			case ch <- ev:
			case <-ctx.Done():
				return
			}
			continue
		}

		// Skip empty lines and comments.
	}

	if err := scanner.Err(); err != nil {
		slog.Warn("responses API stream scanner error", "error", err)
	}
}

