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

const anysearchEndpoint = "https://api.anysearch.com/mcp"

// AnySearchClient is an HTTP client for the AnySearch JSON-RPC API.
type AnySearchClient struct {
	httpClient *http.Client
	apiKey     string
}

// NewAnySearchClient creates a new AnySearchClient.
func NewAnySearchClient(apiKey string) *AnySearchClient {
	return &AnySearchClient{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		apiKey:     apiKey,
	}
}

// jsonrpcRequest is a JSON-RPC 2.0 request payload.
type jsonrpcRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int         `json:"id"`
	Method  string      `json:"method"`
	Params  rpcParams   `json:"params"`
}

type rpcParams struct {
	Name      string         `json:"name"`
	Arguments rpcArguments   `json:"arguments"`
}

type rpcArguments struct {
	Query      string `json:"query"`
	MaxResults int    `json:"max_results,omitempty"`
	Zone       string `json:"zone,omitempty"`
}

// jsonrpcResponse is a JSON-RPC 2.0 response payload.
type jsonrpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  *rpcResult      `json:"result,omitempty"`
	Error   *jsonrpcError   `json:"error,omitempty"`
}

type rpcResult struct {
	Content []rpcContent `json:"content"`
}

type rpcContent struct {
	Type     string         `json:"type"`
	Text     string         `json:"text,omitempty"`
	Resource *rpcResource   `json:"resource,omitempty"`
}

type rpcResource struct {
	Text string `json:"text"`
}

type jsonrpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Search executes a search query against the AnySearch API.
func (c *AnySearchClient) Search(ctx context.Context, req SearchRequest) (*SearchResult, error) {
	if req.MaxResults <= 0 {
		req.MaxResults = 5
	}

	args := rpcArguments{
		Query:      req.Query,
		MaxResults: req.MaxResults,
		Zone:       "web",
	}

	payload := jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params: rpcParams{
			Name:      "search",
			Arguments: args,
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal anysearch request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, anysearchEndpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create anysearch request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("anysearch request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read anysearch response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, &SearchError{
			StatusCode: resp.StatusCode,
			Message:    fmt.Sprintf("AnySearch API error %d: %s", resp.StatusCode, string(respBody)),
		}
	}

	var rpcResp jsonrpcResponse
	if err := json.Unmarshal(respBody, &rpcResp); err != nil {
		return nil, fmt.Errorf("unmarshal anysearch response: %w", err)
	}

	if rpcResp.Error != nil {
		return nil, &SearchError{
			StatusCode: 0,
			Message:    fmt.Sprintf("AnySearch error: %s", rpcResp.Error.Message),
		}
	}

	if rpcResp.Result == nil || len(rpcResp.Result.Content) == 0 {
		return &SearchResult{
			Query:   req.Query,
			Results: []SearchItem{},
		}, nil
	}

	result := &SearchResult{
		Query:   req.Query,
		Results: make([]SearchItem, 0),
	}

	for _, c := range rpcResp.Result.Content {
		text := c.Text
		if c.Resource != nil {
			text = c.Resource.Text
		}
		if text != "" {
			result.Results = append(result.Results, SearchItem{
				Title:   "Search Result",
				URL:     "",
				Content: text,
				Score:   1.0,
			})
		}
	}

	return result, nil
}
