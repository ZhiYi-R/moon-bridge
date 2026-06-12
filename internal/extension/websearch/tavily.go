package websearch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const tavilyBaseURL = "https://api.tavily.com"

// TavilyClient is an HTTP client for the Tavily Search API.
type TavilyClient struct {
	httpClient *http.Client
	apiKey     string
}

// NewTavilyClient creates a new TavilyClient.
func NewTavilyClient(apiKey string) *TavilyClient {
	return &TavilyClient{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		apiKey:     apiKey,
	}
}

// Search executes a search query against the Tavily API with retry on transient errors.
func (c *TavilyClient) Search(ctx context.Context, req SearchRequest) (*SearchResult, error) {
	if req.MaxResults <= 0 {
		req.MaxResults = 5
	}
	if req.SearchDepth == "" {
		req.SearchDepth = "basic"
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal search request: %w", err)
	}

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(1<<attempt) * time.Second):
			}
		}

		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, tavilyBaseURL+"/search", bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("create search request: %w", err)
		}
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

		resp, err := c.httpClient.Do(httpReq)
		if err != nil {
			lastErr = fmt.Errorf("search request failed: %w", err)
			continue
		}
		respBody, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()

		if readErr != nil {
			lastErr = fmt.Errorf("read search response: %w", readErr)
			continue
		}

		if resp.StatusCode == http.StatusOK {
			var result SearchResult
			if err := json.Unmarshal(respBody, &result); err != nil {
				return nil, fmt.Errorf("unmarshal search response: %w", err)
			}
			return &result, nil
		}

		lastErr = &SearchError{
			StatusCode: resp.StatusCode,
			Message:    fmt.Sprintf("Tavily API error %d: %s", resp.StatusCode, string(respBody)),
		}
		// Retry on 429 (rate limited) or 5xx (server error)
		if resp.StatusCode != http.StatusTooManyRequests && (resp.StatusCode < 500 || resp.StatusCode >= 600) {
			return nil, lastErr
		}
	}
	return nil, fmt.Errorf("tavily search failed after 3 attempts: %w", lastErr)
}
