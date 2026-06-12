// Package google implements the Google Generative AI (Gemini) ProviderAdapter for MoonBridge.
//
// Client implements HTTP communication with the Google Gemini API.
package google

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
)

// ClientConfig configures the Gemini API HTTP client.
type ClientConfig struct {
	BaseURL   string
	APIKey    string
	Project   string // Vertex AI project ID (optional, for Vertex AI endpoint)
	Location  string // Vertex AI location (optional, default "us-central1")
	Version   string // API version (default "v1")
	UserAgent string
	Client    *http.Client
}

// Client is an HTTP client for the Google Gemini API.
type Client struct {
	baseURL   string
	apiKey    string
	project   string
	location  string
	version   string
	userAgent string
	client    *http.Client
}

// NewClient creates a new Gemini API client.
//
// If cfg.Client is nil, http.DefaultClient is used.
// If cfg.Version is empty, "v1" is used.
// If cfg.Location is empty, "us-central1" is used.
//
// BaseURL is determined by cfg.Project:
//   - If Project is set (Vertex AI mode): https://{location}-aiplatform.googleapis.com
//   - Otherwise (Gemini API mode): https://generativelanguage.googleapis.com
func NewClient(cfg ClientConfig) *Client {
	httpClient := cfg.Client
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	if cfg.Version == "" {
		cfg.Version = "v1"
	}
	if cfg.Location == "" {
		cfg.Location = "us-central1"
	}

	baseURL := cfg.BaseURL
	if baseURL == "" {
		if cfg.Project != "" {
			baseURL = "https://" + cfg.Location + "-aiplatform.googleapis.com"
		} else {
			baseURL = "https://generativelanguage.googleapis.com"
		}
	}

	return &Client{
		baseURL:   strings.TrimRight(baseURL, "/"),
		apiKey:    cfg.APIKey,
		project:   cfg.Project,
		location:  cfg.Location,
		version:   cfg.Version,
		userAgent: strings.TrimSpace(cfg.UserAgent),
		client:    httpClient,
	}
}

// GenerateContent sends a non-streaming generateContent request.
func (c *Client) GenerateContent(ctx context.Context, model string, req *GenerateContentRequest) (*GenerateContentResponse, error) {
	log := slog.Default().With("model", model)
	log.Debug("sending generateContent request", "contents", len(req.Contents))

	httpReq, err := c.newRequest(ctx, model, ":generateContent", req)
	if err != nil {
		return nil, err
	}

	response, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("google API request failed: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode > 299 {
		body, _ := io.ReadAll(response.Body)
		return nil, fmt.Errorf("google API error: status=%d body=%s", response.StatusCode, string(body))
	}

	var result GenerateContentResponse
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("google API response decode: %w", err)
	}
	log.Info("generateContent completed",
		"candidates", len(result.Candidates),
		"prompt_tokens", safeUsage(result.UsageMetadata).PromptTokenCount,
		"output_tokens", safeUsage(result.UsageMetadata).CandidatesTokenCount,
	)
	return &result, nil
}

// StreamGenerateContent sends a streaming streamGenerateContent request and
// returns a channel of GenerateContentResponse chunks.
//
// The caller MUST consume the channel until it is closed. The read-loop
// goroutine terminates when the HTTP body is fully read, the context is
// cancelled, or an unrecoverable error occurs.
func (c *Client) StreamGenerateContent(ctx context.Context, model string, req *GenerateContentRequest) (<-chan GenerateContentResponse, error) {
	log := slog.Default().With("model", model)
	log.Debug("starting streamGenerateContent", "contents", len(req.Contents))

	httpReq, err := c.newRequest(ctx, model, ":streamGenerateContent", req)
	if err != nil {
		return nil, err
	}

	response, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("google API stream request failed: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		return nil, fmt.Errorf("google API stream error: status=%d body=%s", response.StatusCode, string(body))
	}

	ch := make(chan GenerateContentResponse, 64)
	go c.readStream(ctx, response.Body, ch)
	return ch, nil
}

// Close implements io.Closer. The Gemini client has no persistent resources
// to close (connections are managed by http.Client), so this is a no-op.
func (c *Client) Close() error { return nil }

// ============================================================================
// CachedContent API methods
// ============================================================================

// CreateCachedContent creates a new CachedContent resource.
func (c *Client) CreateCachedContent(ctx context.Context, cc *CachedContent) (*CachedContent, error) {
	data, _ := json.Marshal(cc)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/"+c.version+"/cachedContents", bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("create cached content: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", c.apiKey)
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("create cached content: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("create cached content: %s", resp.Status)
	}
	var result CachedContent
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("create cached content decode: %w", err)
	}
	return &result, nil
}

// GetCachedContent retrieves a CachedContent resource by name.
func (c *Client) GetCachedContent(ctx context.Context, name string) (*CachedContent, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.baseURL+"/"+c.version+"/"+name, nil)
	if err != nil {
		return nil, fmt.Errorf("get cached content: %w", err)
	}
	req.Header.Set("x-goog-api-key", c.apiKey)
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get cached content: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get cached content: %s", resp.Status)
	}
	var result CachedContent
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("get cached content decode: %w", err)
	}
	return &result, nil
}

// UpdateCachedContent updates the TTL of a CachedContent resource.
func (c *Client) UpdateCachedContent(ctx context.Context, name, ttl string) (*CachedContent, error) {
	reqBody := UpdateCachedContentRequest{TTL: ttl}
	data, _ := json.Marshal(reqBody)
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch,
		c.baseURL+"/"+c.version+"/"+name, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("update cached content: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", c.apiKey)
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("update cached content: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("update cached content: %s", resp.Status)
	}
	var result CachedContent
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("update cached content decode: %w", err)
	}
	return &result, nil
}

// DeleteCachedContent deletes a CachedContent resource.
func (c *Client) DeleteCachedContent(ctx context.Context, name string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete,
		c.baseURL+"/"+c.version+"/"+name, nil)
	if err != nil {
		return fmt.Errorf("delete cached content: %w", err)
	}
	req.Header.Set("x-goog-api-key", c.apiKey)
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("delete cached content: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("delete cached content: %s", resp.Status)
	}
	return nil
}

// ============================================================================
// Internal helpers
// ============================================================================

// newRequest builds an HTTP POST request for the Gemini API.
//
// Gemini API mode (no project): {baseURL}/{version}/models/{model}:{action}?key={apiKey}
// Vertex AI mode (project set): {baseURL}/{version}/projects/{project}/locations/{location}/publishers/google/models/{model}:{action}
func (c *Client) newRequest(ctx context.Context, model, action string, req *GenerateContentRequest) (*http.Request, error) {
	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("google API request marshal: %w", err)
	}

	var url string
	if c.project != "" {
		// Vertex AI: Bearer token auth (via APIKey field as OAuth token)
		url = fmt.Sprintf("%s/%s/projects/%s/locations/%s/publishers/google/models/%s%s",
			c.baseURL, c.version, c.project, c.location, model, action)
	} else {
		// Gemini API: API key in query param
		url = fmt.Sprintf("%s/%s/models/%s%s?key=%s",
			c.baseURL, c.version, model, action, c.apiKey)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("google API request build: %w", err)
	}
	httpReq.Header.Set("content-type", "application/json")
	if c.userAgent != "" {
		httpReq.Header.Set("user-agent", c.userAgent)
	}
	if c.project != "" && c.apiKey != "" {
		// Vertex AI uses Bearer token (APIKey field holds the OAuth token)
		httpReq.Header.Set("authorization", "Bearer "+c.apiKey)
	}
	return httpReq, nil
}

// readStream reads SSE lines from the HTTP response body and sends parsed
// GenerateContentResponse chunks into the channel. Closes the channel when done.
func (c *Client) readStream(ctx context.Context, body io.ReadCloser, ch chan<- GenerateContentResponse) {
	defer close(ch)
	defer body.Close()

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)

	for scanner.Scan() {
		line := scanner.Text()

		// Skip empty lines and non-data lines.
		if !strings.HasPrefix(line, "data:") {
			continue
		}

		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}

		var chunk GenerateContentResponse
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			slog.Warn("google API stream parse error", "error", err, "data", data[:min(len(data), 200)])
			continue
		}

		select {
		case ch <- chunk:
		case <-ctx.Done():
			return
		}
	}

	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		slog.Warn("google API stream scanner error", "error", err)
	}
}

// safeUsage returns a non-nil UsageMetadata pointer for safe field access.
func safeUsage(u *UsageMetadata) UsageMetadata {
	if u == nil {
		return UsageMetadata{}
	}
	return *u
}
