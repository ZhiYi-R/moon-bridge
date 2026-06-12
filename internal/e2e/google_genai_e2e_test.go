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
	"moonbridge/internal/protocol/google"
	"moonbridge/internal/protocol/openai"
)

// ============================================================================
// TestGoogleGenaiE2E_TextRoundTrip
// ============================================================================

func TestGoogleGenaiE2E_TextRoundTrip(t *testing.T) {
	if apiKey := os.Getenv("TEST_GEMINI_API_KEY"); apiKey != "" {
		t.Run("real", func(t *testing.T) { testGoogleGenaiRealClient(t, apiKey) })
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
	provider, ok := reg.GetProvider(configGoogleGenAI)
	if !ok {
		t.Fatal("provider adapter not found")
	}

	// Mock upstream Gemini server.
	mockSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("method = %q, want POST", r.Method)
		}
		if !strings.Contains(r.URL.Path, ":generateContent") {
			t.Errorf("path = %q, want to contain ':generateContent'", r.URL.Path)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{
			"candidates": [{"index":0,"content":{"role":"model","parts":[{"text":"Hello from Gemini mock!"}]},"finish_reason":"STOP"}],
			"usage_metadata": {"prompt_token_count":5,"candidates_token_count":10,"total_token_count":15}
		}`)
	}))
	defer mockSrv.Close()

	// Step 1: Build OpenAI Responses request.
	openAIReq := openai.ResponsesRequest{
		Model:           "gemini-2.0-flash",
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
	upstreamReq := upstreamAny.(*google.GenerateContentRequest)

	// Verify upstream request fields.
	if len(upstreamReq.Contents) != 1 {
		t.Fatalf("expected 1 content, got %d", len(upstreamReq.Contents))
	}
	if upstreamReq.Contents[0].Role != "user" {
		t.Errorf("content role = %q, want %q", upstreamReq.Contents[0].Role, "user")
	}
	if len(upstreamReq.Contents[0].Parts) == 0 {
		t.Fatal("expected parts in content, got none")
	}
	if upstreamReq.Contents[0].Parts[0].Text != "Hello" {
		t.Errorf("part text = %q, want %q", upstreamReq.Contents[0].Parts[0].Text, "Hello")
	}

	// Step 4: Call mock upstream using google client.
	geminiClient := google.NewClient(google.ClientConfig{
		BaseURL: mockSrv.URL,
		APIKey:  "test-key",
		Client:  mockSrv.Client(),
	})
	upstreamResp, err := geminiClient.GenerateContent(ctx, "gemini-2.0-flash", upstreamReq)
	if err != nil {
		t.Fatalf("GenerateContent: %v", err)
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
	if oaiResp.OutputText != "Hello from Gemini mock!" {
		t.Errorf("OutputText = %q, want %q", oaiResp.OutputText, "Hello from Gemini mock!")
	}

	// Verify upstream request was properly converted.
	if upstreamReq.GenerationConfig == nil {
		t.Fatal("expected GenerationConfig")
	}
	if upstreamReq.GenerationConfig.MaxOutputTokens != 100 {
		t.Errorf("MaxOutputTokens = %d, want %d", upstreamReq.GenerationConfig.MaxOutputTokens, 100)
	}
}

// ============================================================================
// TestGoogleGenaiE2E_MultiTurnRoundTrip
// ============================================================================

func TestGoogleGenaiE2E_MultiTurnRoundTrip(t *testing.T) {
	if apiKey := os.Getenv("TEST_GEMINI_API_KEY"); apiKey != "" {
		t.Run("real", func(t *testing.T) { testGoogleGenaiRealClient(t, apiKey) })
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
	provider, ok := reg.GetProvider(configGoogleGenAI)
	if !ok {
		t.Fatal("provider adapter not found")
	}

	mockSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{
			"candidates": [{"index":0,"content":{"role":"model","parts":[{"text":"I'm doing great, thanks!"}]},"finish_reason":"STOP"}],
			"usage_metadata": {"prompt_token_count":20,"candidates_token_count":8,"total_token_count":28}
		}`)
	}))
	defer mockSrv.Close()

	// Multi-turn: user → model → user.
	openAIReq := openai.ResponsesRequest{
		Model: "gemini-2.0-flash",
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
	upstreamReq := upstreamAny.(*google.GenerateContentRequest)

	if len(upstreamReq.Contents) != 3 {
		t.Fatalf("expected 3 google contents, got %d", len(upstreamReq.Contents))
	}
	// Gemini: "assistant" → "model", "user" → "user".
	if upstreamReq.Contents[0].Role != "user" || upstreamReq.Contents[1].Role != "model" || upstreamReq.Contents[2].Role != "user" {
		t.Errorf("unexpected google content roles: %q, %q, %q",
			upstreamReq.Contents[0].Role, upstreamReq.Contents[1].Role, upstreamReq.Contents[2].Role)
	}

	geminiClient := google.NewClient(google.ClientConfig{
		BaseURL: mockSrv.URL,
		APIKey:  "test-key",
		Client:  mockSrv.Client(),
	})
	upstreamResp, err := geminiClient.GenerateContent(ctx, "gemini-2.0-flash", upstreamReq)
	if err != nil {
		t.Fatalf("GenerateContent: %v", err)
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
// TestGoogleGenaiE2E_ToolUseRoundTrip
// ============================================================================

func TestGoogleGenaiE2E_ToolUseRoundTrip(t *testing.T) {
	if apiKey := os.Getenv("TEST_GEMINI_API_KEY"); apiKey != "" {
		t.Run("real", func(t *testing.T) { testGoogleGenaiRealClient(t, apiKey) })
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
	provider, ok := reg.GetProvider(configGoogleGenAI)
	if !ok {
		t.Fatal("provider adapter not found")
	}

	mockSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{
			"candidates": [{"index":0,"content":{"role":"model","parts":[{"function_call":{"name":"get_weather","args":{"city":"Paris"}}}]},"finish_reason":"STOP"}],
			"usage_metadata": {"prompt_token_count":15,"candidates_token_count":10,"total_token_count":25}
		}`)
	}))
	defer mockSrv.Close()

	openAIReq := openai.ResponsesRequest{
		Model: "gemini-2.0-flash",
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
	upstreamReq := upstreamAny.(*google.GenerateContentRequest)

	// Verify upstream request has tools.
	if len(upstreamReq.Tools) == 0 {
		t.Fatal("expected tools in google request, got none")
	}
	if len(upstreamReq.Tools[0].FunctionDeclarations) == 0 {
		t.Fatal("expected function declarations in tool")
	}
	if upstreamReq.Tools[0].FunctionDeclarations[0].Name != "get_weather" {
		t.Errorf("function declaration name = %q, want %q",
			upstreamReq.Tools[0].FunctionDeclarations[0].Name, "get_weather")
	}

	geminiClient := google.NewClient(google.ClientConfig{
		BaseURL: mockSrv.URL,
		APIKey:  "test-key",
		Client:  mockSrv.Client(),
	})
	upstreamResp, err := geminiClient.GenerateContent(ctx, "gemini-2.0-flash", upstreamReq)
	if err != nil {
		t.Fatalf("GenerateContent: %v", err)
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
			break
		}
	}
	if !foundFunctionCall {
		t.Error("expected function_call output item, not found")
	}
}

// ============================================================================
// TestGoogleGenaiE2E_Streaming
// ============================================================================

func TestGoogleGenaiE2E_Streaming(t *testing.T) {
	if apiKey := os.Getenv("TEST_GEMINI_API_KEY"); apiKey != "" {
		t.Run("real", func(t *testing.T) { testGoogleGenaiRealClient(t, apiKey) })
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
	providerStream, ok := reg.GetProviderStream(configGoogleGenAI)
	if !ok {
		t.Fatal("provider stream adapter not found")
	}

	// Step 1: Build streaming OpenAI request.
	openAIReq := openai.ResponsesRequest{
		Model:           "gemini-2.0-flash",
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

	// Step 3: Construct GenerateContentResponse channel directly.
	// Google streaming returns full snapshot per event; delta computed by adapter.
	respCh := make(chan google.GenerateContentResponse, 3)
	respCh <- google.GenerateContentResponse{
		Candidates: []google.Candidate{
			{
				Index:        0,
				Content:      google.Content{Role: "model", Parts: []google.Part{{Text: "Hel"}}},
				FinishReason: "",
			},
		},
		UsageMetadata: nil,
	}
	respCh <- google.GenerateContentResponse{
		Candidates: []google.Candidate{
			{
				Index:        0,
				Content:      google.Content{Role: "model", Parts: []google.Part{{Text: "Hello world"}}},
				FinishReason: "STOP",
			},
		},
		UsageMetadata: &google.UsageMetadata{
			PromptTokenCount:     5,
			CandidatesTokenCount: 3,
			TotalTokenCount:      8,
		},
	}
	close(respCh)

	// Step 4: ProviderStreamAdapter.ToCoreStream.
	coreEvents, err := providerStream.ToCoreStream(ctx, (<-chan google.GenerateContentResponse)(respCh))
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
// TestGoogleGenaiE2E_ErrorResponse
// ============================================================================

func TestGoogleGenaiE2E_ErrorResponse(t *testing.T) {
	if apiKey := os.Getenv("TEST_GEMINI_API_KEY"); apiKey != "" {
		t.Run("real", func(t *testing.T) { testGoogleGenaiRealClient(t, apiKey) })
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
	provider, ok := reg.GetProvider(configGoogleGenAI)
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
		Model:           "gemini-2.0-flash",
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
	upstreamReq := upstreamAny.(*google.GenerateContentRequest)

	geminiClient := google.NewClient(google.ClientConfig{
		BaseURL: mockSrv.URL,
		APIKey:  "test-key",
		Client:  mockSrv.Client(),
	})
	_, err = geminiClient.GenerateContent(ctx, "gemini-2.0-flash", upstreamReq)
	if err == nil {
		t.Fatal("expected error from GenerateContent, got nil")
	}

	// Verify the error message contains expected information.
	errStr := err.Error()
	if !strings.Contains(errStr, "400") {
		t.Errorf("error should contain 400 status code, got: %s", errStr)
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

func realGeminiConfig(apiKey string) google.ClientConfig {
	baseURL := os.Getenv("TEST_GEMINI_BASE_URL")
	return google.ClientConfig{
		APIKey:   apiKey,
		BaseURL:  baseURL,
		Project:  os.Getenv("TEST_GEMINI_PROJECT"),
		Location: os.Getenv("TEST_GEMINI_LOCATION"),
		Version:  os.Getenv("TEST_GEMINI_VERSION"),
	}
}

func realGeminiModel() string {
	if m := os.Getenv("TEST_GEMINI_MODEL"); m != "" {
		return m
	}
	return "gemini-2.0-flash"
}

// testGoogleGenaiRealClient runs a real Gemini API call to verify the client
// works end-to-end. It makes a simple text request and checks for a non-empty response.
func testGoogleGenaiRealClient(t *testing.T, apiKey string) {
	t.Helper()

	ctx := context.Background()
	model := realGeminiModel()
	client := google.NewClient(realGeminiConfig(apiKey))

	req := &google.GenerateContentRequest{
		Contents: []google.Content{
			{
				Role:  "user",
				Parts: []google.Part{{Text: "say hello in one word"}},
			},
		},
	}

	resp, err := client.GenerateContent(ctx, model, req)
	if err != nil {
		t.Fatalf("GenerateContent: %v", err)
	}
	if len(resp.Candidates) == 0 {
		t.Fatal("response has no candidates")
	}
	candidate := resp.Candidates[0]
	if len(candidate.Content.Parts) == 0 {
		t.Fatal("response candidate has no parts")
	}
	if candidate.Content.Parts[0].Text == "" {
		t.Error("response text is empty")
	}
	t.Logf("Real response: model=%s text=%q", model, candidate.Content.Parts[0].Text)
}
