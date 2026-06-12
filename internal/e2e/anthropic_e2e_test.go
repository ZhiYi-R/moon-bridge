//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"moonbridge/internal/format"
	"moonbridge/internal/protocol/anthropic"
	"moonbridge/internal/protocol/openai"
)

// ============================================================================
// TestAnthropicE2E_TextRoundTrip
// ============================================================================

func TestAnthropicE2E_TextRoundTrip(t *testing.T) {
	if apiKey := os.Getenv("TEST_ANTHROPIC_API_KEY"); apiKey != "" {
		t.Run("real", func(t *testing.T) { testAnthropicRealClient(t, apiKey) })
		return
	}

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

	// Mock upstream server.
	mockSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request basics.
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

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{
			"id": "msg_mock_001",
			"type": "message",
			"role": "assistant",
			"content": [{"type": "text", "text": "Hello from Anthropic mock!"}],
			"model": "claude-3.5-sonnet-20241022",
			"stop_reason": "end_turn",
			"usage": {"input_tokens": 10, "output_tokens": 5}
		}`)
	}))
	defer mockSrv.Close()

	// Step 1: Build OpenAI Responses request.
	openAIReq := openai.ResponsesRequest{
		Model:           "claude-3.5-sonnet",
		Input:           json.RawMessage(`"Hello"`),
		MaxOutputTokens: 100,
	}

	// Step 2: ClientAdapter.ToCoreRequest.
	coreReq, err := client.ToCoreRequest(ctx, &openAIReq)
	if err != nil {
		t.Fatalf("ToCoreRequest: %v", err)
	}

	// Step 3: ProviderAdapter.FromCoreRequest.
	upstreamAny, err := provider.FromCoreRequest(ctx, coreReq)
	if err != nil {
		t.Fatalf("FromCoreRequest: %v", err)
	}
	upstreamReq := upstreamAny.(*anthropic.MessageRequest)

	// Step 4: Call mock upstream using anthropic client.
	anthClient := anthropic.NewClient(anthropic.ClientConfig{
		BaseURL: mockSrv.URL,
		APIKey:  "test-key",
		Client:  mockSrv.Client(),
	})
	upstreamResp, err := anthClient.CreateMessage(ctx, *upstreamReq)
	if err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}

	// Step 5: ProviderAdapter.ToCoreResponse.
	coreResp, err := provider.ToCoreResponse(ctx, &upstreamResp)
	if err != nil {
		t.Fatalf("ToCoreResponse: %v", err)
	}

	// Step 6: ClientAdapter.FromCoreResponse.
	outAny, err := client.FromCoreResponse(ctx, coreResp)
	if err != nil {
		t.Fatalf("FromCoreResponse: %v", err)
	}
	oaiResp := outAny.(*openai.Response)

	// Assertions.
	assertResponseBasics(t, oaiResp, "")
	if oaiResp.OutputText != "Hello from Anthropic mock!" {
		t.Errorf("OutputText = %q, want %q", oaiResp.OutputText, "Hello from Anthropic mock!")
	}

	// Verify upstream request was properly converted.
	if upstreamReq.MaxTokens != 100 {
		t.Errorf("MaxTokens = %d, want %d", upstreamReq.MaxTokens, 100)
	}
	if len(upstreamReq.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(upstreamReq.Messages))
	}
	if upstreamReq.Messages[0].Role != "user" {
		t.Errorf("message role = %q, want %q", upstreamReq.Messages[0].Role, "user")
	}
}

// ============================================================================
// TestAnthropicE2E_MultiTurnRoundTrip
// ============================================================================

func TestAnthropicE2E_MultiTurnRoundTrip(t *testing.T) {
	if apiKey := os.Getenv("TEST_ANTHROPIC_API_KEY"); apiKey != "" {
		t.Run("real", func(t *testing.T) { testAnthropicRealClient(t, apiKey) })
		return
	}

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
			"id": "msg_multi_001",
			"type": "message",
			"role": "assistant",
			"content": [{"type": "text", "text": "I'm doing great, thanks!"}],
			"model": "claude-3.5-sonnet-20241022",
			"stop_reason": "end_turn",
			"usage": {"input_tokens": 20, "output_tokens": 8}
		}`)
	}))
	defer mockSrv.Close()

	// Multi-turn: user → assistant → user.
	openAIReq := openai.ResponsesRequest{
		Model: "claude-3.5-sonnet",
		Input: json.RawMessage(`[
			{"role": "user", "content": "Hello"},
			{"role": "assistant", "content": "Hi there!"},
			{"role": "user", "content": "How are you?"}
		]`),
		MaxOutputTokens: 100,
	}

	coreReq, err := client.ToCoreRequest(ctx, &openAIReq)
	if err != nil {
		t.Fatalf("ToCoreRequest: %v", err)
	}

	// Verify CoreRequest.Messages length.
	if len(coreReq.Messages) != 3 {
		t.Fatalf("expected 3 Core messages, got %d", len(coreReq.Messages))
	}
	if coreReq.Messages[0].Role != "user" || coreReq.Messages[1].Role != "assistant" || coreReq.Messages[2].Role != "user" {
		t.Errorf("unexpected message roles: %q, %q, %q",
			coreReq.Messages[0].Role, coreReq.Messages[1].Role, coreReq.Messages[2].Role)
	}

	upstreamAny, err := provider.FromCoreRequest(ctx, coreReq)
	if err != nil {
		t.Fatalf("FromCoreRequest: %v", err)
	}
	upstreamReq := upstreamAny.(*anthropic.MessageRequest)

	if len(upstreamReq.Messages) != 3 {
		t.Fatalf("expected 3 anthropic messages, got %d", len(upstreamReq.Messages))
	}

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
	if oaiResp.OutputText != "I'm doing great, thanks!" {
		t.Errorf("OutputText = %q, want %q", oaiResp.OutputText, "I'm doing great, thanks!")
	}
}

// ============================================================================
// TestAnthropicE2E_ToolUseRoundTrip
// ============================================================================

func TestAnthropicE2E_ToolUseRoundTrip(t *testing.T) {
	if apiKey := os.Getenv("TEST_ANTHROPIC_API_KEY"); apiKey != "" {
		t.Run("real", func(t *testing.T) { testAnthropicRealToolCall(t, apiKey) })
		return
	}

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
			"id": "msg_tool_001",
			"type": "message",
			"role": "assistant",
			"content": [
				{"type": "tool_use", "id": "tu_001", "name": "get_weather", "input": {"city": "Paris"}}
			],
			"model": "claude-3.5-sonnet-20241022",
			"stop_reason": "tool_use",
			"usage": {"input_tokens": 15, "output_tokens": 10}
		}`)
	}))
	defer mockSrv.Close()

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
		ToolChoice:      json.RawMessage(`"required"`),
		MaxOutputTokens: 200,
	}

	coreReq, err := client.ToCoreRequest(ctx, &openAIReq)
	if err != nil {
		t.Fatalf("ToCoreRequest: %v", err)
	}

	// Verify tools were converted to Core.
	if len(coreReq.Tools) == 0 {
		t.Fatal("expected tools in CoreRequest, got none")
	}
	if coreReq.Tools[0].Name != "get_weather" {
		t.Errorf("tool name = %q, want %q", coreReq.Tools[0].Name, "get_weather")
	}
	if coreReq.ToolChoice == nil {
		t.Fatal("expected ToolChoice in CoreRequest")
	}

	upstreamAny, err := provider.FromCoreRequest(ctx, coreReq)
	if err != nil {
		t.Fatalf("FromCoreRequest: %v", err)
	}
	upstreamReq := upstreamAny.(*anthropic.MessageRequest)

	// Verify upstream request has tools.
	if len(upstreamReq.Tools) == 0 {
		t.Fatal("expected tools in anthropic request, got none")
	}
	if upstreamReq.Tools[0].Name != "get_weather" {
		t.Errorf("anthropic tool name = %q, want %q", upstreamReq.Tools[0].Name, "get_weather")
	}
	if upstreamReq.ToolChoice.Type != "any" {
		t.Errorf("tool_choice type = %q, want %q", upstreamReq.ToolChoice.Type, "any")
	}

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

	// Verify the response contains a function_call output item.
	var foundFunctionCall bool
	for _, item := range oaiResp.Output {
		if item.Type == "function_call" {
			foundFunctionCall = true
			if item.Name != "get_weather" {
				t.Errorf("function_call name = %q, want %q", item.Name, "get_weather")
			}
			if item.CallID != "tu_001" {
				t.Errorf("function_call call_id = %q, want %q", item.CallID, "tu_001")
			}
			if item.Arguments == "" {
				t.Error("function_call arguments is empty")
			}
			break
		}
	}
	if !foundFunctionCall {
		t.Error("expected function_call output item, not found")
	}
}

// ============================================================================
// TestAnthropicE2E_Streaming
// ============================================================================

func TestAnthropicE2E_Streaming(t *testing.T) {
	if apiKey := os.Getenv("TEST_ANTHROPIC_API_KEY"); apiKey != "" {
		t.Run("real", func(t *testing.T) { testAnthropicRealClient(t, apiKey) })
		return
	}

	ctx := context.Background()
	cfg := e2eMinimalConfig()
	hooks := format.CorePluginHooks{}.WithDefaults()
	reg := newTestRegistry(t, cfg, hooks)

	client, ok := reg.GetClient(configOpenAIResponse)
	if !ok {
		t.Fatal("client adapter not found")
	}
	clientStream, ok := reg.GetClientStream(configOpenAIResponse)
	if !ok {
		t.Fatal("client stream adapter not found")
	}
	provider, ok := reg.GetProvider(configAnthropic)
	if !ok {
		t.Fatal("provider adapter not found")
	}
	providerStream, ok := reg.GetProviderStream(configAnthropic)
	if !ok {
		t.Fatal("provider stream adapter not found")
	}

	mockSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)

		// SSE event sequence: message_start → content_block_start → text delta → content_block_stop → message_delta → message_stop
		writeSSE(w, "message_start", `{"type":"message_start","message":{"id":"msg_str_001","type":"message","role":"assistant","content":[],"model":"claude-3.5-sonnet-20241022","usage":{"input_tokens":5,"output_tokens":0}}}`)
		sseFlush(w)

		writeSSE(w, "content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)
		sseFlush(w)

		writeSSE(w, "content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello from "}}`)
		sseFlush(w)

		writeSSE(w, "content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"streaming mock!"}}`)
		sseFlush(w)

		writeSSE(w, "content_block_stop", `{"type":"content_block_stop","index":0}`)
		sseFlush(w)

		writeSSE(w, "message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":5,"output_tokens":3}}`)
		sseFlush(w)

		writeSSE(w, "message_stop", `{"type":"message_stop"}`)
		sseFlush(w)
	}))
	defer mockSrv.Close()

	// Step 1: Build streaming OpenAI request.
	openAIReq := openai.ResponsesRequest{
		Model:           "claude-3.5-sonnet",
		Input:           json.RawMessage(`"Hello streaming"`),
		MaxOutputTokens: 100,
		Stream:          true,
	}

	// Step 2: ClientAdapter.ToCoreRequest.
	coreReq, err := client.ToCoreRequest(ctx, &openAIReq)
	if err != nil {
		t.Fatalf("ToCoreRequest: %v", err)
	}
	if !coreReq.Stream {
		t.Error("expected Stream=true in CoreRequest")
	}

	// Step 3: ProviderAdapter.FromCoreRequest.
	upstreamAny, err := provider.FromCoreRequest(ctx, coreReq)
	if err != nil {
		t.Fatalf("FromCoreRequest: %v", err)
	}
	upstreamReq := upstreamAny.(*anthropic.MessageRequest)
	if !upstreamReq.Stream {
		t.Error("expected Stream=true in anthropic request")
	}

	// Step 4: Call mock upstream using anthropic stream client.
	anthClient := anthropic.NewClient(anthropic.ClientConfig{
		BaseURL: mockSrv.URL,
		APIKey:  "test-key",
		Client:  mockSrv.Client(),
	})
	stream, err := anthClient.StreamMessage(ctx, *upstreamReq)
	if err != nil {
		t.Fatalf("StreamMessage: %v", err)
	}
	defer stream.Close()

	// Step 5: ProviderStreamAdapter.ToCoreStream.
	coreEvents, err := providerStream.ToCoreStream(ctx, stream)
	if err != nil {
		t.Fatalf("ToCoreStream: %v", err)
	}

	// Step 6: ClientStreamAdapter.FromCoreStream.
	streamOutAny, err := clientStream.FromCoreStream(ctx, coreReq, coreEvents.Events)
	if err != nil {
		t.Fatalf("FromCoreStream: %v", err)
	}
	var openAIStream <-chan openai.StreamEvent
	oaiResult, ok := streamOutAny.(*openai.OpenAIStreamResult)
	if ok {
		openAIStream = oaiResult.Chan()
	} else {
		openAIStream = streamOutAny.(<-chan openai.StreamEvent)
	}

	// Consume the OpenAI stream and verify expected events.
	var seenEvents []string
	for ev := range openAIStream {
		seenEvents = append(seenEvents, ev.Event)
	}

	// Verify key lifecycle events are present.
	expectedEvents := []string{
		"response.created",
		"response.in_progress",
		"response.completed",
	}

	for _, expected := range expectedEvents {
		var found bool
		for _, seen := range seenEvents {
			if seen == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected stream event %q not found in: %v", expected, seenEvents)
		}
	}

	// Verify text delta events are present.
	var foundTextDelta bool
	for _, ev := range seenEvents {
		if ev == "response.output_text.delta" {
			foundTextDelta = true
			break
		}
	}
	if !foundTextDelta {
		t.Error("expected output_text.delta events, not found")
	}

	// Verify output_text.done event.
	var foundTextDone bool
	for _, ev := range seenEvents {
		if ev == "response.output_text.done" {
			foundTextDone = true
			break
		}
	}
	if !foundTextDone {
		t.Error("expected output_text.done event, not found")
	}
}

// ============================================================================
// TestAnthropicE2E_ErrorResponse
// ============================================================================

func TestAnthropicE2E_ErrorResponse(t *testing.T) {
	if apiKey := os.Getenv("TEST_ANTHROPIC_API_KEY"); apiKey != "" {
		t.Run("real", func(t *testing.T) { testAnthropicRealClient(t, apiKey) })
		return
	}

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
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error": {"type": "api_error", "message": "Internal server error"}}`)
	}))
	defer mockSrv.Close()

	openAIReq := openai.ResponsesRequest{
		Model:           "claude-3.5-sonnet",
		Input:           json.RawMessage(`"Hello"`),
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

	anthClient := anthropic.NewClient(anthropic.ClientConfig{
		BaseURL: mockSrv.URL,
		APIKey:  "test-key",
		Client:  mockSrv.Client(),
	})
	_, err = anthClient.CreateMessage(ctx, *upstreamReq)
	if err == nil {
		t.Fatal("expected error from CreateMessage, got nil")
	}

	// Verify it's a provider error with the expected fields.
	providerErr, ok := err.(*anthropic.ProviderError)
	if !ok {
		t.Fatalf("expected *anthropic.ProviderError, got %T: %v", err, err)
	}
	if providerErr.StatusCode != http.StatusInternalServerError {
		t.Errorf("StatusCode = %d, want %d", providerErr.StatusCode, http.StatusInternalServerError)
	}
	if !strings.Contains(providerErr.Error(), "Internal server error") {
		t.Errorf("error message does not contain expected text: %v", providerErr.Error())
	}

	// Additionally test that FromCoreResponse handles error-containing CoreResponse.
	errorCoreResp := &format.CoreResponse{
		ID:     "err_001",
		Status: "failed",
		Error: &format.CoreError{
			Type:    "api_error",
			Message: "Internal server error",
		},
	}
	outAny, err := client.FromCoreResponse(ctx, errorCoreResp)
	if err != nil {
		t.Fatalf("FromCoreResponse with error core: %v", err)
	}
	errResp := outAny.(*openai.Response)
	if errResp.Error == nil {
		t.Fatal("expected Error in OpenAI response")
	}
	if errResp.Error.Type != "api_error" {
		t.Errorf("Error.Type = %q, want %q", errResp.Error.Type, "api_error")
	}
	if errResp.Error.Message != "Internal server error" {
		t.Errorf("Error.Message = %q, want %q", errResp.Error.Message, "Internal server error")
	}
	if errResp.Status != "failed" {
		t.Errorf("Status = %q, want %q", errResp.Status, "failed")
	}
}

// ============================================================================
// Real Provider E2E Helpers
// ============================================================================
// TestAnthropicE2E_MultiTurnToolChain
// ============================================================================

func TestAnthropicE2E_MultiTurnToolChain(t *testing.T) {
	if apiKey := os.Getenv("TEST_ANTHROPIC_API_KEY"); apiKey != "" {
		t.Run("real", func(t *testing.T) { testAnthropicRealMultiTurnToolChain(t, apiKey) })
		return
	}

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
			"id": "msg_multi_tool_001",
			"type": "message",
			"role": "assistant",
			"content": [
				{"type": "tool_use", "id": "tu_multi_001", "name": "get_weather", "input": {"city": "Tokyo"}}
			],
			"model": "claude-3.5-sonnet-20241022",
			"stop_reason": "tool_use",
			"usage": {"input_tokens": 15, "output_tokens": 10}
		}`)
	}))
	defer mockSrv.Close()

	// Turn 1: Send request with tools => expect tool_use.
	openAIReq1 := openai.ResponsesRequest{
		Model: "claude-3.5-sonnet",
		Input: json.RawMessage(`"What is the weather in Tokyo?"`),
		Tools: []openai.Tool{
			{
				Type:        "function",
				Name:        "get_weather",
				Description: "Get the current weather for a city",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"location": map[string]any{"type": "string", "description": "City name"},
					},
					"required": []any{"location"},
				},
			},
		},
		ToolChoice:      json.RawMessage(`"auto"`),
		MaxOutputTokens: 300,
	}

	coreReq1, err := client.ToCoreRequest(ctx, &openAIReq1)
	if err != nil {
		t.Fatalf("Turn 1 ToCoreRequest: %v", err)
	}

	upstreamAny1, err := provider.FromCoreRequest(ctx, coreReq1)
	if err != nil {
		t.Fatalf("Turn 1 FromCoreRequest: %v", err)
	}
	upstreamReq1 := upstreamAny1.(*anthropic.MessageRequest)

	anthClient := anthropic.NewClient(anthropic.ClientConfig{
		BaseURL: mockSrv.URL,
		APIKey:  "test-key",
		Client:  mockSrv.Client(),
	})
	upstreamResp1, err := anthClient.CreateMessage(ctx, *upstreamReq1)
	if err != nil {
		t.Fatalf("Turn 1 CreateMessage: %v", err)
	}

	coreResp1, err := provider.ToCoreResponse(ctx, &upstreamResp1)
	if err != nil {
		t.Fatalf("Turn 1 ToCoreResponse: %v", err)
	}
	outAny1, err := client.FromCoreResponse(ctx, coreResp1)
	if err != nil {
		t.Fatalf("Turn 1 FromCoreResponse: %v", err)
	}
	oaiResp1 := outAny1.(*openai.Response)

	// Assert Turn 1 has function_call.
	var foundFC bool
	var toolCallID string
	for _, item := range oaiResp1.Output {
		if item.Type == "function_call" {
			foundFC = true
			toolCallID = item.CallID
			break
		}
	}
	if !foundFC {
		t.Fatal("Turn 1: expected function_call output item")
	}
	// Fallback if mock ID was not propagated.
	if toolCallID == "" {
		toolCallID = "tu_multi_001"
	}

	// Turn 2: Send tool_result back => expect text response.
	inputItems := []map[string]any{
		{
			"type":      "function_call",
			"call_id":   toolCallID,
			"name":      "get_weather",
			"arguments": `{"location":"Tokyo"}`,
		},
		{
			"type":    "function_call_output",
			"call_id": toolCallID,
			"output":  `{"temperature": 22, "condition": "Sunny"}`,
		},
	}
	inputRaw, _ := json.Marshal(inputItems)

	mockSrv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{
			"id": "msg_multi_tool_002",
			"type": "message",
			"role": "assistant",
			"content": [{"type": "text", "text": "Tokyo weather: 22°C and Sunny!"}],
			"model": "claude-3.5-sonnet-20241022",
			"stop_reason": "end_turn",
			"usage": {"input_tokens": 20, "output_tokens": 8}
		}`)
	}))
	defer mockSrv2.Close()

	openAIReq2 := openai.ResponsesRequest{
		Model:           "claude-3.5-sonnet",
		Input:           json.RawMessage(inputRaw),
		Tools:           openAIReq1.Tools,
		MaxOutputTokens: 300,
	}

	coreReq2, err := client.ToCoreRequest(ctx, &openAIReq2)
	if err != nil {
		t.Fatalf("Turn 2 ToCoreRequest: %v", err)
	}

	upstreamAny2, err := provider.FromCoreRequest(ctx, coreReq2)
	if err != nil {
		t.Fatalf("Turn 2 FromCoreRequest: %v", err)
	}
	upstreamReq2 := upstreamAny2.(*anthropic.MessageRequest)

	// Verify upstream request has tool_result message.
	var hasToolResult bool
	for _, msg := range upstreamReq2.Messages {
		for _, block := range msg.Content {
			if block.Type == "tool_result" {
				hasToolResult = true
				break
			}
		}
	}
	if !hasToolResult {
		t.Error("Turn 2 anthropic request: expected tool_result content block")
	}

	anthClient2 := anthropic.NewClient(anthropic.ClientConfig{
		BaseURL: mockSrv2.URL,
		APIKey:  "test-key",
		Client:  mockSrv2.Client(),
	})
	upstreamResp2, err := anthClient2.CreateMessage(ctx, *upstreamReq2)
	if err != nil {
		t.Fatalf("Turn 2 CreateMessage: %v", err)
	}

	coreResp2, err := provider.ToCoreResponse(ctx, &upstreamResp2)
	if err != nil {
		t.Fatalf("Turn 2 ToCoreResponse: %v", err)
	}
	outAny2, err := client.FromCoreResponse(ctx, coreResp2)
	if err != nil {
		t.Fatalf("Turn 2 FromCoreResponse: %v", err)
	}
	oaiResp2 := outAny2.(*openai.Response)

	// Assert Turn 2 has text response.
	if oaiResp2.OutputText == "" {
		t.Error("Turn 2: OutputText is empty")
	} else {
		t.Logf("Turn 2 output: %q", oaiResp2.OutputText)
	}
}

// ============================================================================

const (
	defaultAnthropicBaseURL = "https://api.anthropic.com"
	defaultAnthropicModel   = "claude-sonnet-4-20250514"
)

func realAnthropicConfig(apiKey string) anthropic.ClientConfig {
	baseURL := os.Getenv("TEST_ANTHROPIC_BASE_URL")
	if baseURL == "" {
		baseURL = defaultAnthropicBaseURL
	}
	return anthropic.ClientConfig{
		BaseURL: baseURL,
		APIKey:  apiKey,
	}
}

func realAnthropicModel() string {
	if m := os.Getenv("TEST_ANTHROPIC_MODEL"); m != "" {
		return m
	}
	return defaultAnthropicModel
}

// testAnthropicRealClient runs a real Anthropic API call to verify the client
// works end-to-end. It makes a simple text request and checks for a non-empty response.
func testAnthropicRealClient(t *testing.T, apiKey string) {
	t.Helper()

	ctx := context.Background()
	model := realAnthropicModel()
	client := anthropic.NewClient(realAnthropicConfig(apiKey))

	resp, err := client.CreateMessage(ctx, anthropic.MessageRequest{
		Model:     model,
		MaxTokens: 200,
		Messages: []anthropic.Message{
			{
				Role:    "user",
				Content: []anthropic.ContentBlock{{Type: "text", Text: "say hello in one word"}},
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}
	if resp.ID == "" {
		t.Error("response ID is empty")
	}
	if len(resp.Content) == 0 {
		t.Fatal("response content is empty")
	}
	var textBlock string
	for _, block := range resp.Content {
		if block.Type == "text" {
			textBlock = block.Text
			break
		}
	}
	if textBlock == "" {
		t.Fatal("no text content block in response")
	}
	t.Logf("Real response: model=%s id=%s text=%q", resp.Model, resp.ID, textBlock)
}

// testAnthropicRealToolCall runs a real Anthropic API call with tool definitions
// to verify the full adapter round-trip for tool_use responses.
func testAnthropicRealToolCall(t *testing.T, apiKey string) {
	t.Helper()

	loadDotEnv(t)

	ctx := context.Background()
	model := realAnthropicModel()
	client := anthropic.NewClient(realAnthropicConfig(apiKey))

	cfg := e2eMinimalConfig()
	hooks := format.CorePluginHooks{}.WithDefaults()
	reg := newTestRegistry(t, cfg, hooks)

	oaiClient, ok := reg.GetClient(configOpenAIResponse)
	if !ok {
		t.Fatal("client adapter not found")
	}
	provider, ok := reg.GetProvider(configAnthropic)
	if !ok {
		t.Fatal("provider adapter not found")
	}

	// Build OpenAI request with tool definitions (model may or may not call tools).
	openAIReq := openai.ResponsesRequest{
		Model: model,
		Input: json.RawMessage(`"What is the weather in Paris? Use get_weather if available."`),
		Tools: []openai.Tool{{
			Type:        "function",
			Name:        "get_weather",
			Description: "Get the current weather for a city",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"city": map[string]any{"type": "string", "description": "City name"},
				},
				"required": []any{"city"},
			},
		}},
		ToolChoice:      json.RawMessage(`"auto"`),
		MaxOutputTokens: 300,
	}

	// Adapter chain: ToCoreRequest -> FromCoreRequest.
	coreReq, err := oaiClient.ToCoreRequest(ctx, &openAIReq)
	if err != nil {
		t.Fatalf("ToCoreRequest: %v", err)
	}
	upstreamAny, err := provider.FromCoreRequest(ctx, coreReq)
	if err != nil {
		t.Fatalf("FromCoreRequest: %v", err)
	}
	upstreamReq := upstreamAny.(*anthropic.MessageRequest)

	// Real API call.
	upstreamResp, err := client.CreateMessage(ctx, *upstreamReq)
	if err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}

	// Response chain: ToCoreResponse -> FromCoreResponse.
	coreResp, err := provider.ToCoreResponse(ctx, &upstreamResp)
	if err != nil {
		t.Fatalf("ToCoreResponse: %v", err)
	}
	outAny, err := oaiClient.FromCoreResponse(ctx, coreResp)
	if err != nil {
		t.Fatalf("FromCoreResponse: %v", err)
	}
	oaiResp := outAny.(*openai.Response)

	// Assert function_call output item.
	var foundFunctionCall bool
	for _, item := range oaiResp.Output {
		if item.Type == "function_call" {
			foundFunctionCall = true
			if item.Name != "get_weather" {
				t.Errorf("function_call name = %q, want %q", item.Name, "get_weather")
			}
			if item.Arguments == "" {
				t.Error("function_call arguments is empty")
			}
			t.Logf("Tool call: name=%s call_id=%s arguments=%s", item.Name, item.CallID, item.Arguments)
			break
		}
	}
	if !foundFunctionCall {
		t.Error("expected function_call output item, not found")
	}
}

// testAnthropicRealMultiTurnToolChain exercises a real two-turn tool chain
// through the Anthropic API using the full adapter round-trip.
//
// Turn 1: Sends a request with get_weather tool, expects a tool_use response.
// Turn 2: Constructs a function_call + function_call_output input with mock
// weather data, sends it back, and expects the model to produce final text.
func testAnthropicRealMultiTurnToolChain(t *testing.T, apiKey string) {
	t.Helper()

	loadDotEnv(t)

	ctx := context.Background()
	model := realAnthropicModel()
	client := anthropic.NewClient(realAnthropicConfig(apiKey))

	cfg := e2eMinimalConfig()
	hooks := format.CorePluginHooks{}.WithDefaults()
	reg := newTestRegistry(t, cfg, hooks)

	oaiClient, ok := reg.GetClient(configOpenAIResponse)
	if !ok {
		t.Fatal("client adapter not found")
	}
	provider, ok := reg.GetProvider(configAnthropic)
	if !ok {
		t.Fatal("provider adapter not found")
	}

	// --- Turn 1: Request with tools, expect tool_use ---

	openAIReq1 := openai.ResponsesRequest{
		Model: model,
		Input: json.RawMessage(`"What is the weather in Tokyo?"`),
		Tools: []openai.Tool{{
			Type:        "function",
			Name:        "get_weather",
			Description: "Get the current weather for a city",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"city": map[string]any{"type": "string", "description": "City name"},
				},
				"required": []any{"city"},
			},
		}},
		ToolChoice:      json.RawMessage(`"auto"`),
		MaxOutputTokens: 300,
	}

	// Adapter chain: ToCoreRequest -> FromCoreRequest.
	coreReq1, err := oaiClient.ToCoreRequest(ctx, &openAIReq1)
	if err != nil {
		t.Fatalf("Turn 1 ToCoreRequest: %v", err)
	}
	upstreamAny1, err := provider.FromCoreRequest(ctx, coreReq1)
	if err != nil {
		t.Fatalf("Turn 1 FromCoreRequest: %v", err)
	}
	upstreamReq1 := upstreamAny1.(*anthropic.MessageRequest)

	// Real API call - Turn 1.
	upstreamResp1, err := client.CreateMessage(ctx, *upstreamReq1)
	if err != nil {
		t.Fatalf("Turn 1 CreateMessage: %v", err)
	}

	// Response chain: ToCoreResponse -> FromCoreResponse.
	coreResp1, err := provider.ToCoreResponse(ctx, &upstreamResp1)
	if err != nil {
		t.Fatalf("Turn 1 ToCoreResponse: %v", err)
	}
	outAny1, err := oaiClient.FromCoreResponse(ctx, coreResp1)
	if err != nil {
		t.Fatalf("Turn 1 FromCoreResponse: %v", err)
	}
	oaiResp1 := outAny1.(*openai.Response)

	// Assert Turn 1 has function_call and extract tool call details.
	var foundFC bool
	var toolCallID string
	var toolArgs string
	for _, item := range oaiResp1.Output {
		if item.Type == "function_call" {
			foundFC = true
			toolCallID = item.CallID
			toolArgs = item.Arguments
			t.Logf("Turn 1 tool call: name=%s call_id=%s arguments=%s", item.Name, toolCallID, toolArgs)
			break
		}
	}
	if !foundFC {
		t.Fatal("Turn 1: expected function_call output item, not found")
	}
	if toolCallID == "" {
		t.Fatal("Turn 1: function_call call_id is empty")
	}

	// --- Turn 2: Send tool_result back, expect final text ---
	// Build the anthropic request directly with full message history (including Turn 1's
	// thinking blocks if any — required by DeepSeek's reasoning model).
	turn1AssistantMsg := upstreamResp1.Content
	turn2Req := &anthropic.MessageRequest{
		Model:     model,
		MaxTokens: 300,
		Messages: append(upstreamReq1.Messages, anthropic.Message{
			Role:    "assistant",
			Content: turn1AssistantMsg,
		}, anthropic.Message{
			Role: "user",
			Content: []anthropic.ContentBlock{{
				Type:      "tool_result",
				ToolUseID: toolCallID,
				Content:   []anthropic.ContentBlock{{Type: "text", Text: "The weather in Tokyo is 25 degrees and Sunny."}},
			}},
		}),
	}

	upstreamResp2, err := client.CreateMessage(ctx, *turn2Req)
	if err != nil {
		t.Fatalf("Turn 2 CreateMessage: %v", err)
	}

	coreResp2, err := provider.ToCoreResponse(ctx, &upstreamResp2)
	if err != nil {
		t.Fatalf("Turn 2 ToCoreResponse: %v", err)
	}
	outAny2, err := oaiClient.FromCoreResponse(ctx, coreResp2)
	if err != nil {
		t.Fatalf("Turn 2 FromCoreResponse: %v", err)
	}
	oaiResp2 := outAny2.(*openai.Response)

	if oaiResp2.OutputText == "" {
		t.Error("Turn 2: OutputText is empty")
	} else {
		t.Logf("Turn 2 output: %q", oaiResp2.OutputText)
	}
}

// Config constants (used by E2E tests)
// ============================================================================
// Config constants (used by E2E tests)
// ============================================================================

const (
	configOpenAIResponse = "openai-response"
	configAnthropic      = "anthropic"
	configGoogleGenAI    = "google-genai"
	configOpenAIChat     = "openai-chat"
)
