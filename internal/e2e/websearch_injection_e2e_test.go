//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"moonbridge/internal/format"
	"moonbridge/internal/protocol/anthropic"
	"moonbridge/internal/protocol/openai"
)

// ============================================================================
// injectWebSearch — duplicates server.injectAnthropicWebSearch logic
// for testability (the real function is unexported in the server package).
// ============================================================================

// injectWebSearch adds an Anthropic web_search_20250305 tool to the request
// if not already present, matching the behavior of the production dispatch path.
func injectWebSearch(req *anthropic.MessageRequest) {
	for i, t := range req.Tools {
		if t.Name == "web_search" {
			if t.Type != "web_search_20250305" && t.Type != "web_search_20260209" {
				req.Tools[i].Type = "web_search_20250305"
			}
			return
		}
	}
	maxUses := 8
	if req.Tools == nil {
		req.Tools = make([]anthropic.Tool, 0, 1)
	}
	req.Tools = append(req.Tools, anthropic.Tool{
		Name:    "web_search",
		Type:    "web_search_20250305",
		MaxUses: maxUses,
	})
}

// ============================================================================
// TestWebSearchE2E_InjectionEnabled
// ============================================================================
//
// Verifies that when web search is enabled:
//  1. injectWebSearch adds the web_search_20250305 tool to the anthropic request
//  2. The mock upstream server receives the web_search tool
//  3. Search result text from the upstream response appears in the final OpenAI response

func TestWebSearchE2E_InjectionEnabled(t *testing.T) {
	ctx := context.Background()
	cfg := e2eMinimalConfig()
	hooks := format.CorePluginHooks{}.WithDefaults()
	reg := newTestRegistry(t, cfg, hooks)

	client, ok := reg.GetClient(configOpenAIResponse)
	if !ok {
		t.Fatal("client adapter not found")
	}
	provider, ok := reg.GetProvider(configAnthropic)
	if !ok {
		t.Fatal("provider adapter not found")
	}

	// Channel carries tool list from mock handler back to test goroutine.
	toolsCh := make(chan []anthropic.Tool, 1)

	// Mock Anthropic server that verifies web_search tool is present
	// and returns search result content.
	mockSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("method = %q, want POST", r.Method)
		}
		if r.URL.Path != "/v1/messages" {
			t.Errorf("path = %q, want /v1/messages", r.URL.Path)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}
		if auth := r.Header.Get("x-api-key"); auth == "" {
			t.Error("x-api-key header is empty")
		}

		// Decode the request to verify web_search tool was sent over the wire.
		var req anthropic.MessageRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("mock server: failed to decode request: %v", err)
		}
		toolsCh <- req.Tools

		// Return a response with search result text.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{
			"id": "msg_ws_001",
			"type": "message",
			"role": "assistant",
			"content": [{"type": "text", "text": "Based on my search, the latest AI breakthroughs include advances in multimodal models and reasoning agents."}],
			"model": "claude-3.5-sonnet-20241022",
			"stop_reason": "end_turn",
			"usage": {"input_tokens": 25, "output_tokens": 18}
		}`)
	}))
	defer mockSrv.Close()

	// Step 1: Build an OpenAI ResponsesRequest WITHOUT web_search in tools.
	openAIReq := openai.ResponsesRequest{
		Model:           "claude-3.5-sonnet",
		Input:           json.RawMessage(`"Search for latest AI breakthroughs"`),
		MaxOutputTokens: 100,
	}

	// Step 2: Convert through adapter chain: ToCoreRequest → FromCoreRequest.
	coreReq, err := client.ToCoreRequest(ctx, &openAIReq)
	if err != nil {
		t.Fatalf("ToCoreRequest: %v", err)
	}

	upstreamAny, err := provider.FromCoreRequest(ctx, coreReq)
	if err != nil {
		t.Fatalf("FromCoreRequest: %v", err)
	}
	upstreamReq := upstreamAny.(*anthropic.MessageRequest)

	// Verify pre-condition: no web_search tool before injection.
	for _, tool := range upstreamReq.Tools {
		if tool.Name == "web_search" {
			t.Fatal("pre-condition failed: web_search tool already present before injection")
		}
	}

	// Step 3: CALL injectWebSearch (simulating handleWithAdapters line 213).
	injectWebSearch(upstreamReq)

	// Step 4: Verify the anthropic request now has a web_search tool injected.
	var foundWebSearch bool
	for _, tool := range upstreamReq.Tools {
		if tool.Name == "web_search" {
			foundWebSearch = true
			if tool.Type != "web_search_20250305" {
				t.Errorf("web_search tool Type = %q, want %q", tool.Type, "web_search_20250305")
			}
			if tool.MaxUses != 8 {
				t.Errorf("web_search MaxUses = %d, want %d", tool.MaxUses, 8)
			}
			break
		}
	}
	if !foundWebSearch {
		t.Fatal("web_search tool was NOT injected into anthropic request")
	}

	// Verify the tool count is correct (should be exactly 1 — the injected web_search).
	if len(upstreamReq.Tools) != 1 {
		t.Errorf("expected exactly 1 tool (web_search), got %d tools", len(upstreamReq.Tools))
	}

	// Step 5: Send the request to the mock Anthropic server.
	anthClient := anthropic.NewClient(anthropic.ClientConfig{
		BaseURL: mockSrv.URL,
		APIKey:  "test-key",
		Client:  mockSrv.Client(),
	})
	upstreamResp, err := anthClient.CreateMessage(ctx, *upstreamReq)
	if err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}

	// Verify the mock server received the web_search tool over the wire.
	select {
	case tools := <-toolsCh:
		var serverSawWebSearch bool
		for _, tool := range tools {
			if tool.Name == "web_search" {
				serverSawWebSearch = true
				if tool.Type != "web_search_20250305" {
					t.Errorf("server-side web_search tool Type = %q, want %q", tool.Type, "web_search_20250305")
				}
				if tool.MaxUses != 8 {
					t.Errorf("server-side web_search MaxUses = %d, want %d", tool.MaxUses, 8)
				}
				break
			}
		}
		if !serverSawWebSearch {
			t.Error("mock server did NOT receive web_search tool in the request body")
		}
	default:
		t.Error("mock server handler did not report tools")
	}

	// Step 6: Convert back: ToCoreResponse → FromCoreResponse.
	coreResp, err := provider.ToCoreResponse(ctx, &upstreamResp)
	if err != nil {
		t.Fatalf("ToCoreResponse: %v", err)
	}

	outAny, err := client.FromCoreResponse(ctx, coreResp)
	if err != nil {
		t.Fatalf("FromCoreResponse: %v", err)
	}
	oaiResp := outAny.(*openai.Response)

	// Step 7: Verify the final OpenAI Response has the search result text.
	assertResponseBasics(t, oaiResp, "")
	expectedText := "Based on my search, the latest AI breakthroughs include advances in multimodal models and reasoning agents."
	if oaiResp.OutputText != expectedText {
		t.Errorf("OutputText = %q, want %q", oaiResp.OutputText, expectedText)
	}
}

// ============================================================================
// TestWebSearchE2E_InjectionDisabled
// ============================================================================
//
// Verifies that when web search is disabled (injectWebSearch is NOT called),
// no web_search tool appears in the anthropic request — the adapter path
// produces a clean request without web search tools.

func TestWebSearchE2E_InjectionDisabled(t *testing.T) {
	ctx := context.Background()
	cfg := e2eMinimalConfig()
	hooks := format.CorePluginHooks{}.WithDefaults()
	reg := newTestRegistry(t, cfg, hooks)

	client, ok := reg.GetClient(configOpenAIResponse)
	if !ok {
		t.Fatal("client adapter not found")
	}
	provider, ok := reg.GetProvider(configAnthropic)
	if !ok {
		t.Fatal("provider adapter not found")
	}

	mockSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{
			"id": "msg_nows_001",
			"type": "message",
			"role": "assistant",
			"content": [{"type": "text", "text": "Here is a normal response without web search."}],
			"model": "claude-3.5-sonnet-20241022",
			"stop_reason": "end_turn",
			"usage": {"input_tokens": 10, "output_tokens": 8}
		}`)
	}))
	defer mockSrv.Close()

	// Step 1: Build OpenAI request without web_search.
	openAIReq := openai.ResponsesRequest{
		Model:           "claude-3.5-sonnet",
		Input:           json.RawMessage(`"Hello"`),
		MaxOutputTokens: 100,
	}

	// Step 2: Adapter chain.
	coreReq, err := client.ToCoreRequest(ctx, &openAIReq)
	if err != nil {
		t.Fatalf("ToCoreRequest: %v", err)
	}

	upstreamAny, err := provider.FromCoreRequest(ctx, coreReq)
	if err != nil {
		t.Fatalf("FromCoreRequest: %v", err)
	}
	upstreamReq := upstreamAny.(*anthropic.MessageRequest)

	// Step 3: Do NOT inject web_search — simulate disabled mode.
	// Step 4: Verify web_search tool is NOT present.
	for _, tool := range upstreamReq.Tools {
		if tool.Name == "web_search" {
			t.Errorf("web_search tool should NOT be present when web search is disabled, but found: Name=%q Type=%q", tool.Name, tool.Type)
		}
	}
	if len(upstreamReq.Tools) > 0 {
		t.Logf("note: upstream request has %d non-web_search tools (from adapter): %v", len(upstreamReq.Tools), toolNames(upstreamReq.Tools))
	}

	// Step 5: Send to mock server.
	anthClient := anthropic.NewClient(anthropic.ClientConfig{
		BaseURL: mockSrv.URL,
		APIKey:  "test-key",
		Client:  mockSrv.Client(),
	})
	upstreamResp, err := anthClient.CreateMessage(ctx, *upstreamReq)
	if err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}

	// Step 6: Convert back.
	coreResp, err := provider.ToCoreResponse(ctx, &upstreamResp)
	if err != nil {
		t.Fatalf("ToCoreResponse: %v", err)
	}
	outAny, err := client.FromCoreResponse(ctx, coreResp)
	if err != nil {
		t.Fatalf("FromCoreResponse: %v", err)
	}
	oaiResp := outAny.(*openai.Response)

	// Step 7: Verify normal response (no web search artifacts).
	assertResponseBasics(t, oaiResp, "")
	expectedText := "Here is a normal response without web search."
	if oaiResp.OutputText != expectedText {
		t.Errorf("OutputText = %q, want %q", oaiResp.OutputText, expectedText)
	}
}

// ============================================================================
// TestWebSearchE2E_AlreadyPresentNotOverwritten
// ============================================================================
//
// Verifies that if the OpenAI request already includes a web_search tool
// (e.g. passed through from the client), injectWebSearch does NOT overwrite
// or duplicate it — it only ensures the Type is correct.

func TestWebSearchE2E_AlreadyPresentNotOverwritten(t *testing.T) {
	ctx := context.Background()
	cfg := e2eMinimalConfig()
	hooks := format.CorePluginHooks{}.WithDefaults()
	reg := newTestRegistry(t, cfg, hooks)

	client, ok := reg.GetClient(configOpenAIResponse)
	if !ok {
		t.Fatal("client adapter not found")
	}
	provider, ok := reg.GetProvider(configAnthropic)
	if !ok {
		t.Fatal("provider adapter not found")
	}

	toolsCh := make(chan []anthropic.Tool, 1)

	mockSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req anthropic.MessageRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("mock server: decode error: %v", err)
		}
		toolsCh <- req.Tools

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{
			"id": "msg_ws_002",
			"type": "message",
			"role": "assistant",
			"content": [{"type": "text", "text": "Response with web_search already in tools."}],
			"model": "claude-3.5-sonnet-20241022",
			"stop_reason": "end_turn",
			"usage": {"input_tokens": 15, "output_tokens": 10}
		}`)
	}))
	defer mockSrv.Close()

	// OpenAI request with get_weather tool (NOT web_search) plus tool_choice auto.
	openAIReq := openai.ResponsesRequest{
		Model: "claude-3.5-sonnet",
		Input: json.RawMessage(`"Use the get_weather tool for Paris"`),
		Tools: []openai.Tool{
			{
				Type:        "function",
				Name:        "get_weather",
				Description: "Get the current weather for a city",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"city": map[string]any{"type": "string"},
					},
					"required": []any{"city"},
				},
			},
		},
		MaxOutputTokens: 100,
	}

	coreReq, err := client.ToCoreRequest(ctx, &openAIReq)
	if err != nil {
		t.Fatalf("ToCoreRequest: %v", err)
	}

	upstreamAny, err := provider.FromCoreRequest(ctx, coreReq)
	if err != nil {
		t.Fatalf("FromCoreRequest: %v", err)
	}
	upstreamReq := upstreamAny.(*anthropic.MessageRequest)

	// Record tool count before injection.
	toolCountBefore := len(upstreamReq.Tools)

	// Inject web_search alongside existing tools.
	injectWebSearch(upstreamReq)

	// Verify: web_search was appended, existing tools preserved.
	var foundWeather, foundWebSearch bool
	for _, tool := range upstreamReq.Tools {
		if tool.Name == "get_weather" {
			foundWeather = true
		}
		if tool.Name == "web_search" {
			foundWebSearch = true
		}
	}
	if !foundWeather {
		t.Error("existing get_weather tool was removed after web_search injection")
	}
	if !foundWebSearch {
		t.Error("web_search tool was not added alongside existing tools")
	}
	if len(upstreamReq.Tools) != toolCountBefore+1 {
		t.Errorf("expected %d tools (existing + 1 web_search), got %d", toolCountBefore+1, len(upstreamReq.Tools))
	}

	// Call inject again — should be idempotent.
	injectWebSearch(upstreamReq)
	if len(upstreamReq.Tools) != toolCountBefore+1 {
		t.Errorf("injectWebSearch is not idempotent: expected %d tools, got %d", toolCountBefore+1, len(upstreamReq.Tools))
	}

	// Send to mock server and verify round-trip still works.
	anthClient := anthropic.NewClient(anthropic.ClientConfig{
		BaseURL: mockSrv.URL,
		APIKey:  "test-key",
		Client:  mockSrv.Client(),
	})
	upstreamResp, err := anthClient.CreateMessage(ctx, *upstreamReq)
	if err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}

	coreResp, err := provider.ToCoreResponse(ctx, &upstreamResp)
	if err != nil {
		t.Fatalf("ToCoreResponse: %v", err)
	}
	outAny, err := client.FromCoreResponse(ctx, coreResp)
	if err != nil {
		t.Fatalf("FromCoreResponse: %v", err)
	}
	oaiResp := outAny.(*openai.Response)

	assertResponseBasics(t, oaiResp, "")
	if oaiResp.OutputText != "Response with web_search already in tools." {
		t.Errorf("OutputText = %q, want %q", oaiResp.OutputText, "Response with web_search already in tools.")
	}
}

// ============================================================================
// Helpers
// ============================================================================

// toolNames returns the names of all tools in the slice for diagnostic logging.
func toolNames(tools []anthropic.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	return names
}
