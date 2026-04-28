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

// Search executes a search query against the Tavily API.
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

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, tavilyBaseURL+"/search", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create search request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("search request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read search response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, &SearchError{
			StatusCode: resp.StatusCode,
			Message:    fmt.Sprintf("Tavily API error %d: %s", resp.StatusCode, string(respBody)),
		}
	}

	var result SearchResult
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("unmarshal search response: %w", err)
	}
	return &result, nil
}
