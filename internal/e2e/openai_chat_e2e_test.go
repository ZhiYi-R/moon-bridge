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
	"moonbridge/internal/protocol/chat"
	"moonbridge/internal/protocol/openai"
)

// ============================================================================
// TestOpenAIChatE2E_TextRoundTrip
// ============================================================================

func TestOpenAIChatE2E_TextRoundTrip(t *testing.T) {
	if apiKey := os.Getenv("TEST_OPENAI_API_KEY"); apiKey != "" {
		t.Run("real", func(t *testing.T) { testChatRealClient(t, apiKey) })
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
	provider, ok := reg.GetProvider(configOpenAIChat)
	if !ok {
		t.Fatal("provider adapter not found")
	}

	// Mock upstream Chat Completions server.
	mockSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("method = %q, want POST", r.Method)
		}
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %q, want /v1/chat/completions", r.URL.Path)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}
		if auth := r.Header.Get("authorization"); auth == "" {
			t.Error("authorization header is empty")
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{
			"id": "chatcmpl_mock_001",
			"object": "chat.completion",
			"created": 1717000000,
			"model": "gpt-4o-mini",
			"choices": [{"index":0,"message":{"role":"assistant","content":"Hello from Chat mock!"},"finish_reason":"stop"}],
			"usage": {"prompt_tokens":5,"completion_tokens":5,"total_tokens":10}
		}`)
	}))
	defer mockSrv.Close()

	// Step 1: Build OpenAI Responses request.
	openAIReq := openai.ResponsesRequest{
		Model:           "gpt-4o",
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
	upstreamReq := upstreamAny.(*chat.ChatRequest)

	// Verify upstream request fields.
	if len(upstreamReq.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(upstreamReq.Messages))
	}
	if upstreamReq.Messages[0].Role != "user" {
		t.Errorf("message role = %q, want %q", upstreamReq.Messages[0].Role, "user")
	}

	// Step 4: Call mock upstream using chat client.
	chatClient := chat.NewClient(chat.ClientConfig{
		BaseURL: mockSrv.URL,
		APIKey:  "test-key",
		Client:  mockSrv.Client(),
	})
	upstreamResp, err := chatClient.CreateChat(ctx, upstreamReq)
	if err != nil {
		t.Fatalf("CreateChat: %v", err)
	}

	// Step 5: ProviderAdapter.ToCoreResponse.
	coreResp, err := provider.ToCoreResponse(ctx, upstreamResp)
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
	if oaiResp.OutputText != "Hello from Chat mock!" {
		t.Errorf("OutputText = %q, want %q", oaiResp.OutputText, "Hello from Chat mock!")
	}

	// Verify upstream request was properly converted.
	if upstreamReq.MaxTokens != 100 {
		t.Errorf("MaxTokens = %d, want %d", upstreamReq.MaxTokens, 100)
	}
}

// ============================================================================
// TestOpenAIChatE2E_MultiTurnRoundTrip
// ============================================================================

func TestOpenAIChatE2E_MultiTurnRoundTrip(t *testing.T) {
	if apiKey := os.Getenv("TEST_OPENAI_API_KEY"); apiKey != "" {
		t.Run("real", func(t *testing.T) { testChatRealClient(t, apiKey) })
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
	provider, ok := reg.GetProvider(configOpenAIChat)
	if !ok {
		t.Fatal("provider adapter not found")
	}

	mockSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{
			"id": "chatcmpl_multi_001",
			"object": "chat.completion",
			"created": 1717000001,
			"model": "gpt-4o-mini",
			"choices": [{"index":0,"message":{"role":"assistant","content":"I'm doing great, thanks!"},"finish_reason":"stop"}],
			"usage": {"prompt_tokens":20,"completion_tokens":8,"total_tokens":28}
		}`)
	}))
	defer mockSrv.Close()

	// Multi-turn: user → assistant → user.
	openAIReq := openai.ResponsesRequest{
		Model: "gpt-4o",
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

	// Verify CoreRequest.Messages length and roles.
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
	upstreamReq := upstreamAny.(*chat.ChatRequest)

	if len(upstreamReq.Messages) != 3 {
		t.Fatalf("expected 3 chat messages, got %d", len(upstreamReq.Messages))
	}

	chatClient := chat.NewClient(chat.ClientConfig{
		BaseURL: mockSrv.URL,
		APIKey:  "test-key",
		Client:  mockSrv.Client(),
	})
	upstreamResp, err := chatClient.CreateChat(ctx, upstreamReq)
	if err != nil {
		t.Fatalf("CreateChat: %v", err)
	}

	coreResp, err := provider.ToCoreResponse(ctx, upstreamResp)
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
// TestOpenAIChatE2E_ToolUseRoundTrip
// ============================================================================

func TestOpenAIChatE2E_ToolUseRoundTrip(t *testing.T) {
	if apiKey := os.Getenv("TEST_OPENAI_API_KEY"); apiKey != "" {
		t.Run("real", func(t *testing.T) { testChatRealClient(t, apiKey) })
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
	provider, ok := reg.GetProvider(configOpenAIChat)
	if !ok {
		t.Fatal("provider adapter not found")
	}

	mockSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{
			"id": "chatcmpl_tool_001",
			"object": "chat.completion",
			"created": 1717000002,
			"model": "gpt-4o-mini",
			"choices": [{"index":0,"message":{"role":"assistant","content":"","tool_calls":[{"id":"call_abc","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Paris\"}"}}]},"finish_reason":"tool_calls"}],
			"usage": {"prompt_tokens":15,"completion_tokens":10,"total_tokens":25}
		}`)
	}))
	defer mockSrv.Close()

	openAIReq := openai.ResponsesRequest{
		Model: "gpt-4o",
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
	upstreamReq := upstreamAny.(*chat.ChatRequest)

	// Verify upstream request has tools.
	if len(upstreamReq.Tools) == 0 {
		t.Fatal("expected tools in chat request, got none")
	}
	if upstreamReq.Tools[0].Function.Name != "get_weather" {
		t.Errorf("chat tool name = %q, want %q", upstreamReq.Tools[0].Function.Name, "get_weather")
	}

	chatClient := chat.NewClient(chat.ClientConfig{
		BaseURL: mockSrv.URL,
		APIKey:  "test-key",
		Client:  mockSrv.Client(),
	})
	upstreamResp, err := chatClient.CreateChat(ctx, upstreamReq)
	if err != nil {
		t.Fatalf("CreateChat: %v", err)
	}

	coreResp, err := provider.ToCoreResponse(ctx, upstreamResp)
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
			if item.Arguments == "" {
				t.Error("function_call arguments is empty")
			}
			if item.Arguments != `{"city":"Paris"}` {
				t.Errorf("function_call arguments = %q, want %q", item.Arguments, `"{"city":"Paris"}"`)
			}
			break
		}
	}
	if !foundFunctionCall {
		t.Error("expected function_call output item, not found")
	}
}

// ============================================================================
// TestOpenAIChatE2E_Streaming
// ============================================================================

func TestOpenAIChatE2E_Streaming(t *testing.T) {
	if apiKey := os.Getenv("TEST_OPENAI_API_KEY"); apiKey != "" {
		t.Run("real", func(t *testing.T) { testChatRealClient(t, apiKey) })
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
	providerStream, ok := reg.GetProviderStream(configOpenAIChat)
	if !ok {
		t.Fatal("provider stream adapter not found")
	}

	// Step 1: Build streaming OpenAI request.
	openAIReq := openai.ResponsesRequest{
		Model:           "gpt-4o",
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

	// Step 3: Construct ChatStreamChunk channel directly (no mock server needed
	// since ToCoreStream accepts <-chan ChatStreamChunk).
	chunkCh := make(chan chat.ChatStreamChunk, 4)
	chunkCh <- chat.ChatStreamChunk{
		ID:      "chatcmpl_str_001",
		Object:  "chat.completion.chunk",
		Created: 1717000010,
		Model:   "gpt-4o-mini",
		Choices: []chat.StreamChoice{
			{Index: 0, Delta: chat.Delta{Role: "assistant", Content: "Hello "}, FinishReason: ""},
		},
	}
	chunkCh <- chat.ChatStreamChunk{
		ID:      "chatcmpl_str_001",
		Object:  "chat.completion.chunk",
		Created: 1717000011,
		Model:   "gpt-4o-mini",
		Choices: []chat.StreamChoice{
			{Index: 0, Delta: chat.Delta{Content: "world"}, FinishReason: ""},
		},
	}
	chunkCh <- chat.ChatStreamChunk{
		ID:      "chatcmpl_str_001",
		Object:  "chat.completion.chunk",
		Created: 1717000012,
		Model:   "gpt-4o-mini",
		Choices: []chat.StreamChoice{
			{Index: 0, Delta: chat.Delta{}, FinishReason: "stop"},
		},
		Usage: &chat.Usage{
			PromptTokens:     5,
			CompletionTokens: 3,
			TotalTokens:      8,
		},
	}
	close(chunkCh)

	// Step 4: ProviderStreamAdapter.ToCoreStream.
	coreEvents, err := providerStream.ToCoreStream(ctx, (<-chan chat.ChatStreamChunk)(chunkCh))
	if err != nil {
		t.Fatalf("ToCoreStream: %v", err)
	}

	// Step 5: ClientStreamAdapter.FromCoreStream.
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

	// Verify key events are present.
	expectedEvents := []string{
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
// TestOpenAIChatE2E_ErrorResponse
// ============================================================================

func TestOpenAIChatE2E_ErrorResponse(t *testing.T) {
	if apiKey := os.Getenv("TEST_OPENAI_API_KEY"); apiKey != "" {
		t.Run("real", func(t *testing.T) { testChatRealClient(t, apiKey) })
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
	provider, ok := reg.GetProvider(configOpenAIChat)
	if !ok {
		t.Fatal("provider adapter not found")
	}

	mockSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error": {"message": "Invalid model", "type": "invalid_request_error", "code": "model_not_found"}}`)
	}))
	defer mockSrv.Close()

	openAIReq := openai.ResponsesRequest{
		Model:           "gpt-4o",
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
	upstreamReq := upstreamAny.(*chat.ChatRequest)

	chatClient := chat.NewClient(chat.ClientConfig{
		BaseURL: mockSrv.URL,
		APIKey:  "test-key",
		Client:  mockSrv.Client(),
	})
	_, err = chatClient.CreateChat(ctx, upstreamReq)
	if err == nil {
		t.Fatal("expected error from CreateChat, got nil")
	}

	// Verify the error message contains the expected status code and model error.
	errStr := err.Error()
	if !strings.Contains(errStr, "400") {
		t.Errorf("error should contain 400 status code, got: %s", errStr)
	}
	if !strings.Contains(errStr, "Invalid model") {
		t.Errorf("error should contain 'Invalid model', got: %s", errStr)
	}

	// Additionally test that FromCoreResponse handles error-containing CoreResponse.
	errorCoreResp := &format.CoreResponse{
		ID:     "err_001",
		Status: "failed",
		Error: &format.CoreError{
			Type:    "invalid_request_error",
			Message: "Invalid model",
			Code:    "model_not_found",
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
	if errResp.Error.Type != "invalid_request_error" {
		t.Errorf("Error.Type = %q, want %q", errResp.Error.Type, "invalid_request_error")
	}
	if errResp.Error.Message != "Invalid model" {
		t.Errorf("Error.Message = %q, want %q", errResp.Error.Message, "Invalid model")
	}
	if errResp.Error.Code != "model_not_found" {
		t.Errorf("Error.Code = %q, want %q", errResp.Error.Code, "model_not_found")
	}
	if errResp.Status != "failed" {
		t.Errorf("Status = %q, want %q", errResp.Status, "failed")
	}
}

// ============================================================================
// Real Provider E2E Helpers
// ============================================================================

const (
	defaultChatBaseURL = "https://api.openai.com"
	defaultChatModel   = "gpt-4o-mini"
)

func realChatConfig(apiKey string) chat.ClientConfig {
	baseURL := os.Getenv("TEST_OPENAI_BASE_URL")
	return chat.ClientConfig{
		BaseURL: baseURL,
		APIKey:  apiKey,
	}
}

func realChatModel() string {
	if m := os.Getenv("TEST_OPENAI_MODEL"); m != "" {
		return m
	}
	return defaultChatModel
}

// testChatRealClient runs a real OpenAI Chat API call to verify the client
// works end-to-end. It makes a simple text request and checks for a non-empty response.
func testChatRealClient(t *testing.T, apiKey string) {
	t.Helper()

	ctx := context.Background()
	model := realChatModel()
	client := chat.NewClient(realChatConfig(apiKey))

	resp, err := client.CreateChat(ctx, &chat.ChatRequest{
		Model:     model,
		MaxTokens: 50,
		Messages: []chat.ChatMessage{
			{Role: "user", Content: "say hello in one word"},
		},
	})
	if err != nil {
		t.Fatalf("CreateChat: %v", err)
	}
	if resp.ID == "" {
		t.Error("response ID is empty")
	}
	if len(resp.Choices) == 0 {
		t.Fatal("response has no choices")
	}
	content, ok := resp.Choices[0].Message.Content.(string)
	if !ok || content == "" {
		t.Error("response content is empty or not a string")
	}
	t.Logf("Real response: model=%s id=%s text=%q", resp.Model, resp.ID, content)
}
