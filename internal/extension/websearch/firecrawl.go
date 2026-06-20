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

// NewFirecrawlClientWithHTTP creates a new FirecrawlClient with a custom HTTP client.
func NewFirecrawlClientWithHTTP(apiKey string, httpClient *http.Client) *FirecrawlClient {
	if httpClient == nil {
		return NewFirecrawlClient(apiKey)
	}
	return &FirecrawlClient{
		httpClient: httpClient,
		apiKey:     apiKey,
	}
}

// Fetch scrapes a URL and returns its content as markdown with retry on transient errors.
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

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(1<<attempt) * time.Second):
			}
		}

		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, firecrawlBaseURL+"/v1/scrape", bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("create fetch request: %w", err)
		}
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

		resp, err := c.httpClient.Do(httpReq)
		if err != nil {
			lastErr = fmt.Errorf("fetch request failed: %w", err)
			continue
		}
		respBody, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()

		if readErr != nil {
			lastErr = fmt.Errorf("read fetch response: %w", readErr)
			continue
		}

		if resp.StatusCode == http.StatusOK {
			var result FetchResult
			if err := json.Unmarshal(respBody, &result); err != nil {
				return nil, fmt.Errorf("unmarshal fetch response: %w", err)
			}
			return &result, nil
		}

		lastErr = &SearchError{
			StatusCode: resp.StatusCode,
			Message:    fmt.Sprintf("Firecrawl API error %d: %s", resp.StatusCode, string(respBody)),
		}
		// Retry on 429 (rate limited) or 5xx (server error)
		if resp.StatusCode != http.StatusTooManyRequests && (resp.StatusCode < 500 || resp.StatusCode >= 600) {
			return nil, lastErr
		}
	}
	return nil, fmt.Errorf("firecrawl fetch failed after 3 attempts: %w", lastErr)
}

// Enabled returns whether the Firecrawl client is configured with a valid API key.
func (c *FirecrawlClient) Enabled() bool {
	return c != nil && c.apiKey != ""
}
