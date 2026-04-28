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

const firecrawlBaseURL = "https://api.firecrawl.dev"

// FirecrawlClient is an HTTP client for the Firecrawl Scrape API.
type FirecrawlClient struct {
	httpClient *http.Client
	apiKey     string
}

// NewFirecrawlClient creates a new FirecrawlClient.
func NewFirecrawlClient(apiKey string) *FirecrawlClient {
	return &FirecrawlClient{
		httpClient: &http.Client{Timeout: 60 * time.Second},
		apiKey:     apiKey,
	}
}

// Fetch scrapes a URL and returns its content as markdown.
func (c *FirecrawlClient) Fetch(ctx context.Context, req FetchRequest) (*FetchResult, error) {
	if len(req.Formats) == 0 {
		req.Formats = []string{"markdown"}
	}
	if req.Timeout <= 0 {
		req.Timeout = 30000
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal fetch request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, firecrawlBaseURL+"/v1/scrape", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create fetch request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("fetch request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read fetch response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, &SearchError{
			StatusCode: resp.StatusCode,
			Message:    fmt.Sprintf("Firecrawl API error %d: %s", resp.StatusCode, string(respBody)),
		}
	}

	var result FetchResult
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("unmarshal fetch response: %w", err)
	}
	return &result, nil
}

// Enabled returns whether the Firecrawl client is configured with a valid API key.
func (c *FirecrawlClient) Enabled() bool {
	return c != nil && c.apiKey != ""
}
