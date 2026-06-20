// Package chat_test provides unit tests for the OpenAI Chat Completions protocol package.
//
// Covers types.go (DTO JSON round-trip), client.go (HTTP/mock server),
// and adapter.go (Core format conversion). External test package.
package chat_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"moonbridge/internal/format"
	"moonbridge/internal/protocol/chat"
)

// ============================================================================
// Helper factories
// ============================================================================

// newTestClient creates a chat.Client wired to an httptest.Server.
func newTestClient(t *testing.T, srv *httptest.Server) *chat.Client {
	t.Helper()
	return chat.NewClient(chat.ClientConfig{
		BaseURL: srv.URL,
		APIKey:  "test-key",
		Client:  srv.Client(),
	})
}

// newTestAdapter creates a ChatProviderAdapter with nil client and no hooks.
func newTestAdapter() *chat.ChatProviderAdapter {
	return chat.NewChatProviderAdapter(0, nil, format.CorePluginHooks{})
}

// ============================================================================
// Types: JSON round-trip
// ============================================================================

func TestTypes_ChatRequest_JSON(t *testing.T) {
	temp := 0.7
	topP := 0.9
	in := chat.ChatRequest{
		Model: "gpt-4o",
		Messages: []chat.ChatMessage{
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "hi there"},
		},
		Temperature: &temp,
		TopP:        &topP,
		MaxTokens:   4096,
		Stop:        []string{"\n"},
		Stream:      false,
		Tools: []chat.ChatTool{
			{
				Type: "function",
				Function: chat.FunctionDef{
					Name:        "get_weather",
					Description: "Get weather for a city",
					Parameters:  map[string]any{"type": "object"},
				},
			},
		},
		ToolChoice: json.RawMessage(`{"type":"function","function":{"name":"get_weather"}}`),
	}

	data, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}

	var out chat.ChatRequest
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}

	if out.Model != "gpt-4o" {
		t.Errorf("Model = %q, want gpt-4o", out.Model)
	}
	if len(out.Messages) != 2 {
		t.Fatalf("Messages: got %d, want 2", len(out.Messages))
	}
	if out.Messages[0].Role != "user" {
		t.Errorf("Messages[0].Role = %q, want user", out.Messages[0].Role)
	}
	if out.Messages[0].Content != "hello" {
		t.Errorf("Messages[0].Content = %v, want hello", out.Messages[0].Content)
	}
	if out.Temperature == nil || *out.Temperature != 0.7 {
		t.Errorf("Temperature = %v, want 0.7", out.Temperature)
	}
	if out.MaxTokens != 4096 {
		t.Errorf("MaxTokens = %d, want 4096", out.MaxTokens)
	}
	if len(out.Stop) != 1 || out.Stop[0] != "\n" {
		t.Errorf("Stop = %v, want [\"\\n\"]", out.Stop)
	}
	if len(out.Tools) != 1 || out.Tools[0].Function.Name != "get_weather" {
		t.Errorf("Tools[0] = %+v", out.Tools[0])
	}
	if len(out.ToolChoice) == 0 {
		t.Error("ToolChoice is empty after round-trip")
	}
	if out.Stream {
		t.Error("Stream should be false")
	}
}

func TestTypes_ChatMessage_MarshalJSON_EmitsEmptyReasoningContentWhenForced(t *testing.T) {
	message := chat.ChatMessage{
		Role:                      "assistant",
		ToolCalls:                 []chat.ToolCall{{ID: "call_1", Type: "function", Function: chat.ToolCallFunc{Name: "exec_command", Arguments: json.RawMessage(`{}`)}}},
		ReasoningContent:          "",
		EmitEmptyReasoningContent: true,
	}

	data, err := json.Marshal(message)
	if err != nil {
		t.Fatalf("Marshal(ChatMessage) error = %v", err)
	}
	if !strings.Contains(string(data), `"reasoning_content":""`) {
		t.Fatalf("expected reasoning_content to be explicitly present, got %s", string(data))
	}

	message.EmitEmptyReasoningContent = false
	data2, err := json.Marshal(message)
	if err != nil {
		t.Fatalf("Marshal(ChatMessage) error = %v", err)
	}
	if strings.Contains(string(data2), `"reasoning_content":""`) {
		t.Fatalf("reasoning_content should be omitted without force flag, got %s", string(data2))
	}
}

func TestTypes_ChatResponse_JSON(t *testing.T) {
	in := chat.ChatResponse{
		ID:      "chatcmpl-123",
		Object:  "chat.completion",
		Created: 1716500000,
		Model:   "gpt-4o",
		Choices: []chat.Choice{
			{
				Index: 0,
				Message: chat.ChatMessage{
					Role:    "assistant",
					Content: "Hello!",
				},
				FinishReason: "stop",
			},
		},
		Usage: &chat.Usage{
			PromptTokens:     5,
			CompletionTokens: 10,
			TotalTokens:      15,
		},
	}

	data, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}

	var out chat.ChatResponse
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}

	if out.ID != "chatcmpl-123" {
		t.Errorf("ID = %q, want chatcmpl-123", out.ID)
	}
	if out.Object != "chat.completion" {
		t.Errorf("Object = %q", out.Object)
	}
	if len(out.Choices) != 1 {
		t.Fatalf("Choices: got %d, want 1", len(out.Choices))
	}
	content, ok := out.Choices[0].Message.Content.(string)
	if !ok || content != "Hello!" {
		t.Errorf("Content = %v (%T), want Hello!", out.Choices[0].Message.Content, out.Choices[0].Message.Content)
	}
	if out.Choices[0].FinishReason != "stop" {
		t.Errorf("FinishReason = %q, want stop", out.Choices[0].FinishReason)
	}
	if out.Usage == nil || out.Usage.TotalTokens != 15 {
		t.Errorf("Usage = %+v", out.Usage)
	}
	if out.Created != 1716500000 {
		t.Errorf("Created = %d, want 1716500000", out.Created)
	}
}

func TestTypes_ChatStreamChunk_JSON(t *testing.T) {
	usage := chat.Usage{
		PromptTokens:     5,
		CompletionTokens: 10,
		TotalTokens:      15,
	}
	in := chat.ChatStreamChunk{
		ID:      "chunk_1",
		Object:  "chat.completion.chunk",
		Created: 1716500000,
		Model:   "gpt-4o",
		Choices: []chat.StreamChoice{
			{
				Index: 0,
				Delta: chat.Delta{
					Role:    "assistant",
					Content: "Hello",
				},
			},
		},
		Usage: &usage,
	}

	data, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}

	var out chat.ChatStreamChunk
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}

	if len(out.Choices) != 1 {
		t.Fatalf("Choices: got %d, want 1", len(out.Choices))
	}
	if out.Choices[0].Delta.Role != "assistant" {
		t.Errorf("Delta.Role = %q, want assistant", out.Choices[0].Delta.Role)
	}
	if out.Choices[0].Delta.Content != "Hello" {
		t.Errorf("Delta.Content = %q, want Hello", out.Choices[0].Delta.Content)
	}
	if out.Usage == nil || out.Usage.TotalTokens != 15 {
		t.Errorf("Usage = %+v", out.Usage)
	}
	if out.Object != "chat.completion.chunk" {
		t.Errorf("Object = %q", out.Object)
	}
}

func TestTypes_ContentPart_Text(t *testing.T) {
	in := chat.ContentPart{
		Type: "text",
		Text: "hello world",
	}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}

	var out chat.ContentPart
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}

	if out.Type != "text" {
		t.Errorf("Type = %q, want text", out.Type)
	}
	if out.Text != "hello world" {
		t.Errorf("Text = %q, want hello world", out.Text)
	}
	if out.ImageURL != nil {
		t.Error("ImageURL should be nil for text part")
	}
}

func TestTypes_ContentPart_Image(t *testing.T) {
	in := chat.ContentPart{
		Type: "image_url",
		ImageURL: &chat.ImageURL{
			URL:    "https://example.com/image.png",
			Detail: "auto",
		},
	}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}

	var out chat.ContentPart
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}

	if out.Type != "image_url" {
		t.Errorf("Type = %q, want image_url", out.Type)
	}
	if out.ImageURL == nil {
		t.Fatal("ImageURL is nil")
	}
	if out.ImageURL.URL != "https://example.com/image.png" {
		t.Errorf("ImageURL.URL = %q", out.ImageURL.URL)
	}
	if out.ImageURL.Detail != "auto" {
		t.Errorf("ImageURL.Detail = %q", out.ImageURL.Detail)
	}
}

func TestTypes_ToolCall_JSON(t *testing.T) {
	in := chat.ToolCall{
		ID:   "call_xyz",
		Type: "function",
		Function: chat.ToolCallFunc{
			Name:      "get_weather",
			Arguments: json.RawMessage(`{"city":"Paris"}`),
		},
	}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}

	var out chat.ToolCall
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}

	if out.ID != "call_xyz" {
		t.Errorf("ID = %q, want call_xyz", out.ID)
	}
	if out.Type != "function" {
		t.Errorf("Type = %q, want function", out.Type)
	}
	if out.Function.Name != "get_weather" {
		t.Errorf("Function.Name = %q, want get_weather", out.Function.Name)
	}
	if string(out.Function.Arguments) != `{"city":"Paris"}` {
		t.Errorf("Function.Arguments = %s, want {\"city\":\"Paris\"}", string(out.Function.Arguments))
	}
}

func TestTypes_StreamOptions_JSON(t *testing.T) {
	in := chat.StreamOptions{
		IncludeUsage: true,
	}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}

	var out chat.StreamOptions
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}

	if !out.IncludeUsage {
		t.Error("IncludeUsage = false, want true")
	}
}

// TestAdapter_ReasoningEffort_FromOpenAIExtensions verifies that
// req.Extensions["openai"]["reasoning"]["effort"] (set by the OpenAI Responses
// adapter when the client sends {"reasoning":{"effort":"high"}}) is propagated
// to the outbound Chat Completions body as the standard reasoning_effort field.
func TestAdapter_ReasoningEffort_FromOpenAIExtensions(t *testing.T) {
	adapter := newTestAdapter()
	core := &format.CoreRequest{
		Model:    "deepseek-v4-pro",
		Messages: []format.CoreMessage{{Role: "user", Content: []format.CoreContentBlock{{Type: "text", Text: "hi"}}}},
		Extensions: map[string]any{
			"openai": map[string]any{
				"reasoning": map[string]any{"effort": "high"},
			},
		},
	}
	upstream, err := adapter.FromCoreRequest(context.Background(), core)
	if err != nil {
		t.Fatalf("FromCoreRequest: %v", err)
	}
	chatReq, ok := upstream.(*chat.ChatRequest)
	if !ok {
		t.Fatalf("upstream type = %T, want *chat.ChatRequest", upstream)
	}
	if chatReq.ReasoningEffort != "high" {
		t.Errorf("ReasoningEffort = %q, want %q", chatReq.ReasoningEffort, "high")
	}
	// Confirm the field serializes to the OpenAI-standard JSON key.
	data, err := json.Marshal(chatReq)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(data), `"reasoning_effort":"high"`) {
		t.Errorf("outbound body missing reasoning_effort: %s", data)
	}
}

func TestAdapter_ReasoningEffort_Absent_OmitsField(t *testing.T) {
	adapter := newTestAdapter()
	core := &format.CoreRequest{
		Model:    "deepseek-v4-pro",
		Messages: []format.CoreMessage{{Role: "user", Content: []format.CoreContentBlock{{Type: "text", Text: "hi"}}}},
	}
	upstream, err := adapter.FromCoreRequest(context.Background(), core)
	if err != nil {
		t.Fatalf("FromCoreRequest: %v", err)
	}
	chatReq := upstream.(*chat.ChatRequest)
	if chatReq.ReasoningEffort != "" {
		t.Errorf("ReasoningEffort = %q, want empty when client did not request it", chatReq.ReasoningEffort)
	}
	data, _ := json.Marshal(chatReq)
	if strings.Contains(string(data), "reasoning_effort") {
		t.Errorf("body should not include reasoning_effort when unset: %s", data)
	}
}

func TestTypes_FunctionDef_Strict(t *testing.T) {
	strict := true
	in := chat.FunctionDef{
		Name:        "get_weather",
		Description: "Get weather",
		Parameters:  map[string]any{"type": "object"},
		Strict:      &strict,
	}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}

	var out chat.FunctionDef
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}

	if out.Name != "get_weather" {
		t.Errorf("Name = %q", out.Name)
	}
	if out.Strict == nil || !*out.Strict {
		t.Error("Strict should be true")
	}
}

// ============================================================================
// Client: NewClient defaults
// ============================================================================

func TestNewClient_Defaults(t *testing.T) {
	client := chat.NewClient(chat.ClientConfig{
		APIKey: "test-key",
	})
	if client == nil {
		t.Fatal("client is nil")
	}
	if err := client.Close(); err != nil {
		t.Errorf("Close() = %v, want nil", err)
	}
}

func TestClient_RequestHeaders(t *testing.T) {
	var authHeader, contentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		contentType = r.Header.Get("Content-Type")
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"x","object":"chat.completion","choices":[]}`))
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	_, err := client.CreateChat(context.Background(), &chat.ChatRequest{
		Model:    "gpt-4o",
		Messages: []chat.ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	if authHeader != "Bearer test-key" {
		t.Errorf("Authorization = %q, want Bearer test-key", authHeader)
	}
	if contentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", contentType)
	}
}

func TestClient_UserAgent(t *testing.T) {
	var userAgent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userAgent = r.Header.Get("User-Agent")
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"x","object":"chat.completion","choices":[]}`))
	}))
	defer srv.Close()

	client := chat.NewClient(chat.ClientConfig{
		BaseURL:   srv.URL,
		APIKey:    "test-key",
		UserAgent: "MoonBridge-Test/1.0",
		Client:    srv.Client(),
	})

	_, err := client.CreateChat(context.Background(), &chat.ChatRequest{
		Model:    "gpt-4o",
		Messages: []chat.ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	if userAgent != "MoonBridge-Test/1.0" {
		t.Errorf("User-Agent = %q, want MoonBridge-Test/1.0", userAgent)
	}
}

func TestClient_NilClientDefault(t *testing.T) {
	client := chat.NewClient(chat.ClientConfig{
		APIKey: "test-key",
	})
	if err := client.Close(); err != nil {
		t.Errorf("Close() = %v", err)
	}
}

// ============================================================================
// Client: non-streaming (CreateChat)
// ============================================================================

func TestCreateChat_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"chatcmpl-123","object":"chat.completion","created":1716500000,"model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"Hello!"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":10,"total_tokens":15}}`))
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	resp, err := client.CreateChat(context.Background(), &chat.ChatRequest{
		Model:    "gpt-4o",
		Messages: []chat.ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	if resp.ID != "chatcmpl-123" {
		t.Errorf("ID = %q, want chatcmpl-123", resp.ID)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("Choices: got %d, want 1", len(resp.Choices))
	}
	content, ok := resp.Choices[0].Message.Content.(string)
	if !ok || content != "Hello!" {
		t.Errorf("Content = %v, want Hello!", content)
	}
	if resp.Choices[0].FinishReason != "stop" {
		t.Errorf("FinishReason = %q, want stop", resp.Choices[0].FinishReason)
	}
	if resp.Usage == nil || resp.Usage.TotalTokens != 15 {
		t.Errorf("Usage = %+v", resp.Usage)
	}
}

func TestCreateChat_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":{"code":401,"message":"Invalid API key"}}`))
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	_, err := client.CreateChat(context.Background(), &chat.ChatRequest{
		Model:    "gpt-4o",
		Messages: []chat.ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error for 401 response")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error = %v, want 401 in message", err)
	}
}

func TestCreateChat_5xxError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	_, err := client.CreateChat(context.Background(), &chat.ChatRequest{
		Model:    "gpt-4o",
		Messages: []chat.ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error for 502 response")
	}
	if !strings.Contains(err.Error(), "502") {
		t.Errorf("error = %v, want 502 in message", err)
	}
}

func TestCreateChat_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{bad json`))
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	_, err := client.CreateChat(context.Background(), &chat.ChatRequest{
		Model:    "gpt-4o",
		Messages: []chat.ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestCreateChat_ContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"x","object":"chat.completion","choices":[]}`))
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before the request

	_, err := client.CreateChat(ctx, &chat.ChatRequest{
		Model:    "gpt-4o",
		Messages: []chat.ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

// ============================================================================
// Client: streaming (StreamChat)
// ============================================================================

func TestStreamChat_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		w.Header().Set("content-type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("ResponseWriter does not implement http.Flusher")
		}

		events := []string{
			`data: {"id":"x","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":""}}]}`,
			`data: {"id":"x","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"Hello"}}]}`,
			`data: {"id":"x","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":" world"}}]}`,
			`data: {"id":"x","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
			`data: {"id":"x","object":"chat.completion.chunk","choices":[{"index":0,"delta":{}}],"usage":{"prompt_tokens":5,"completion_tokens":10,"total_tokens":15}}`,
			`data: [DONE]`,
		}
		for _, e := range events {
			w.Write([]byte(e + "\n"))
			flusher.Flush()
		}
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	ch, err := client.StreamChat(context.Background(), &chat.ChatRequest{
		Model:    "gpt-4o",
		Messages: []chat.ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	var chunks []chat.ChatStreamChunk
	for c := range ch {
		chunks = append(chunks, c)
	}

	if len(chunks) != 5 {
		t.Fatalf("got %d chunks, want 5", len(chunks))
	}
	// First chunk: role = assistant
	if len(chunks[0].Choices) == 0 {
		t.Fatal("chunk[0] has no choices")
	}
	if chunks[0].Choices[0].Delta.Role != "assistant" {
		t.Errorf("chunk[0] Role = %q, want assistant", chunks[0].Choices[0].Delta.Role)
	}
	// Second chunk: content = "Hello"
	if chunks[1].Choices[0].Delta.Content != "Hello" {
		t.Errorf("chunk[1] Content = %q, want Hello", chunks[1].Choices[0].Delta.Content)
	}
	// Third chunk: content = " world"
	if chunks[2].Choices[0].Delta.Content != " world" {
		t.Errorf("chunk[2] Content = %q, want ' world'", chunks[2].Choices[0].Delta.Content)
	}
	// Fourth chunk: finish_reason
	if chunks[3].Choices[0].FinishReason != "stop" {
		t.Errorf("chunk[3] FinishReason = %q, want stop", chunks[3].Choices[0].FinishReason)
	}
	// Last chunk: usage
	if chunks[4].Usage == nil || chunks[4].Usage.TotalTokens != 15 {
		t.Errorf("chunk[4] Usage = %+v", chunks[4].Usage)
	}
}

func TestStreamChat_EmptyLine(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("no flusher")
		}
		w.Write([]byte("\n"))
		flusher.Flush()
		w.Write([]byte(`data: {"id":"x","choices":[{"index":0,"delta":{"role":"assistant","content":"A"}}]}` + "\n"))
		flusher.Flush()
		w.Write([]byte("\n"))
		flusher.Flush()
		w.Write([]byte("data: [DONE]\n"))
		flusher.Flush()
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	ch, err := client.StreamChat(context.Background(), &chat.ChatRequest{
		Model:    "gpt-4o",
		Messages: []chat.ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	var chunks []chat.ChatStreamChunk
	for c := range ch {
		chunks = append(chunks, c)
	}

	if len(chunks) != 1 {
		t.Fatalf("got %d chunks, want 1 (empty lines should be skipped)", len(chunks))
	}
}

func TestStreamChat_NonDataLine(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("no flusher")
		}
		w.Write([]byte(": comment\n"))
		flusher.Flush()
		w.Write([]byte(`data: {"id":"x","choices":[{"index":0,"delta":{"role":"assistant","content":"B"}}]}` + "\n"))
		flusher.Flush()
		w.Write([]byte("data: [DONE]\n"))
		flusher.Flush()
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	ch, err := client.StreamChat(context.Background(), &chat.ChatRequest{
		Model:    "gpt-4o",
		Messages: []chat.ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	var chunks []chat.ChatStreamChunk
	for c := range ch {
		chunks = append(chunks, c)
	}

	if len(chunks) != 1 {
		t.Fatalf("got %d chunks, want 1 (non-data lines skipped)", len(chunks))
	}
}

func TestStreamChat_StreamingHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"bad request"}`))
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	_, err := client.StreamChat(context.Background(), &chat.ChatRequest{
		Model:    "gpt-4o",
		Messages: []chat.ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error for streaming 400 response")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("error = %v, want 400 in message", err)
	}
}

func TestStreamChat_ParseError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("no flusher")
		}
		w.Write([]byte(`data: {"id":"x","choices":[{"index":0,"delta":{"role":"assistant","content":"valid"}}]}` + "\n"))
		flusher.Flush()
		// Invalid JSON — should be skipped
		w.Write([]byte("data: {{{{not json\n"))
		flusher.Flush()
		w.Write([]byte(`data: {"id":"y","choices":[{"index":0,"delta":{"content":"after error"}}]}` + "\n"))
		flusher.Flush()
		w.Write([]byte("data: [DONE]\n"))
		flusher.Flush()
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	ch, err := client.StreamChat(context.Background(), &chat.ChatRequest{
		Model:    "gpt-4o",
		Messages: []chat.ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	var chunks []chat.ChatStreamChunk
	for c := range ch {
		chunks = append(chunks, c)
	}

	if len(chunks) != 2 {
		t.Fatalf("got %d chunks, want 2 (parse error line should be skipped)", len(chunks))
	}
}

func TestStreamChat_ContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("no flusher")
		}
		for i := 0; i < 100; i++ {
			w.Write([]byte(`data: {"id":"x","choices":[{"index":0,"delta":{"content":"chunk"}}]}` + "\n"))
			flusher.Flush()
		}
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	ctx, cancel := context.WithCancel(context.Background())

	ch, err := client.StreamChat(ctx, &chat.ChatRequest{
		Model:    "gpt-4o",
		Messages: []chat.ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	cancel() // cancel immediately

	for range ch { // drain
	}
}

func TestStreamChat_DeltaToolCall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("no flusher")
		}
		w.Write([]byte(`data: {"id":"x","choices":[{"index":0,"delta":{"tool_calls":[{"id":"call_xyz","function":{"name":"get_weather","arguments":""}}]}}]}` + "\n"))
		flusher.Flush()
		w.Write([]byte(`data: {"id":"x","choices":[{"index":0,"delta":{"tool_calls":[{"function":{"arguments":"{\"city\":\"Paris\"}"}}]}}]}` + "\n"))
		flusher.Flush()
		w.Write([]byte("data: [DONE]\n"))
		flusher.Flush()
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	ch, err := client.StreamChat(context.Background(), &chat.ChatRequest{
		Model:    "gpt-4o",
		Messages: []chat.ChatMessage{{Role: "user", Content: "Use weather tool"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	var chunks []chat.ChatStreamChunk
	for c := range ch {
		chunks = append(chunks, c)
	}

	if len(chunks) != 2 {
		t.Fatalf("got %d chunks, want 2", len(chunks))
	}
	if len(chunks[0].Choices[0].Delta.ToolCalls) == 0 {
		t.Fatal("chunk[0] has no tool calls")
	}
	if chunks[0].Choices[0].Delta.ToolCalls[0].Function.Name != "get_weather" {
		t.Errorf("chunk[0] tool name = %q", chunks[0].Choices[0].Delta.ToolCalls[0].Function.Name)
	}
}

// ============================================================================
// ProviderProtocol
// ============================================================================

func TestProviderProtocol(t *testing.T) {
	adapter := newTestAdapter()
	if got := adapter.ProviderProtocol(); got != "openai-chat" {
		t.Errorf("ProviderProtocol = %q, want openai-chat", got)
	}
}

// ============================================================================
// FromCoreRequest
// ============================================================================

func TestFromCoreRequest_Nil(t *testing.T) {
	adapter := newTestAdapter()
	_, err := adapter.FromCoreRequest(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for nil request")
	}
}

func TestTypes_ChatMessage_JSON_MultimodalContent(t *testing.T) {
	raw := `{"role":"user","content":[{"type":"text","text":"Describe this"},{"type":"image_url","image_url":{"url":"https://example.com/img.jpg","detail":"high"}}]}`
	var msg chat.ChatMessage
	if err := json.Unmarshal([]byte(raw), &msg); err != nil {
		t.Fatal(err)
	}
	if msg.Role != "user" {
		t.Errorf("Role = %q, want user", msg.Role)
	}
	parts, ok := msg.Content.([]any)
	if !ok {
		t.Fatalf("Content type = %T, want []any", msg.Content)
	}
	if len(parts) != 2 {
		t.Fatalf("len(parts) = %d, want 2", len(parts))
	}
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	var msg2 chat.ChatMessage
	if err := json.Unmarshal(data, &msg2); err != nil {
		t.Fatal(err)
	}
	if msg2.Role != "user" {
		t.Errorf("after re-marshal Role = %q, want user", msg2.Role)
	}
}

func TestTypes_Usage_JSON(t *testing.T) {
	in := chat.Usage{PromptTokens: 100, CompletionTokens: 50, TotalTokens: 150}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out chat.Usage
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	if out.PromptTokens != 100 || out.CompletionTokens != 50 || out.TotalTokens != 150 {
		t.Errorf("Usage = %+v", out)
	}
}

// ============================================================================
// FromCoreRequest — CoreRequest -> *ChatRequest
// ============================================================================

func TestFromCoreRequest_Empty(t *testing.T) {
	adapter := newTestAdapter()
	result, err := adapter.FromCoreRequest(context.Background(), &format.CoreRequest{
		Model: "gpt-4o",
	})
	if err != nil {
		t.Fatal(err)
	}
	chatReq, ok := result.(*chat.ChatRequest)
	if !ok {
		t.Fatalf("expected *ChatRequest, got %T", result)
	}
	if chatReq.Model != "gpt-4o" {
		t.Errorf("Model = %q, want gpt-4o", chatReq.Model)
	}
	if chatReq.Messages == nil {
		t.Error("Messages should be non-nil empty slice")
	}
	if len(chatReq.Messages) != 0 {
		t.Errorf("Messages: got %d, want empty", len(chatReq.Messages))
	}
}

func TestFromCoreRequest_BasicText(t *testing.T) {
	adapter := newTestAdapter()
	result, err := adapter.FromCoreRequest(context.Background(), &format.CoreRequest{
		Model: "gpt-4o",
		Messages: []format.CoreMessage{
			{Role: "user", Content: []format.CoreContentBlock{{Type: "text", Text: "hello"}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	chatReq := result.(*chat.ChatRequest)
	if len(chatReq.Messages) != 1 {
		t.Fatalf("Messages: got %d, want 1", len(chatReq.Messages))
	}
	if chatReq.Messages[0].Role != "user" {
		t.Errorf("Role = %q, want user", chatReq.Messages[0].Role)
	}
	content, ok := chatReq.Messages[0].Content.(string)
	if !ok || content != "hello" {
		t.Errorf("Content = %v, want hello", chatReq.Messages[0].Content)
	}
}

func TestFromCoreRequest_MultiTurn(t *testing.T) {
	adapter := newTestAdapter()
	result, err := adapter.FromCoreRequest(context.Background(), &format.CoreRequest{
		Model: "gpt-4o",
		Messages: []format.CoreMessage{
			{Role: "user", Content: []format.CoreContentBlock{{Type: "text", Text: "hi"}}},
			{Role: "assistant", Content: []format.CoreContentBlock{{Type: "text", Text: "hello"}}},
			{Role: "user", Content: []format.CoreContentBlock{{Type: "text", Text: "again"}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	chatReq := result.(*chat.ChatRequest)
	if len(chatReq.Messages) != 3 {
		t.Fatalf("Messages: got %d, want 3", len(chatReq.Messages))
	}
	if chatReq.Messages[0].Role != "user" || chatReq.Messages[1].Role != "assistant" || chatReq.Messages[2].Role != "user" {
		t.Errorf("Roles = %q, %q, %q", chatReq.Messages[0].Role, chatReq.Messages[1].Role, chatReq.Messages[2].Role)
	}
}

// TestFromCoreRequest_ImageContent_DataURLReconstructed proves the chat
// adapter rebuilds a "data:<mime>;base64,<data>" URL from a CoreContentBlock
// whose ImageData is raw base64 with a separate MediaType field. Without the
// reconstruction the chat upstream receives a bare base64 string in
// image_url.url which DashScope (and other OpenAI-compatible providers)
// reject as malformed.
func TestFromCoreRequest_ImageContent_DataURLReconstructed(t *testing.T) {
	adapter := newTestAdapter()
	result, err := adapter.FromCoreRequest(context.Background(), &format.CoreRequest{
		Model: "qwen-vl-plus",
		Messages: []format.CoreMessage{{
			Role: "user",
			Content: []format.CoreContentBlock{
				{Type: "text", Text: "describe"},
				{Type: "image", ImageData: "abc123base64==", MediaType: "image/png"},
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	chatReq := result.(*chat.ChatRequest)
	parts, ok := chatReq.Messages[0].Content.([]chat.ContentPart)
	if !ok {
		t.Fatalf("Content = %T, want []ContentPart", chatReq.Messages[0].Content)
	}
	if len(parts) != 2 {
		t.Fatalf("parts = %d, want 2", len(parts))
	}
	if parts[1].Type != "image_url" || parts[1].ImageURL == nil {
		t.Fatalf("part[1] = %+v, want image_url with ImageURL set", parts[1])
	}
	if got, want := parts[1].ImageURL.URL, "data:image/png;base64,abc123base64=="; got != want {
		t.Errorf("ImageURL.URL = %q, want %q", got, want)
	}
}

func TestFromCoreRequest_ImageContent_FullDataURLPreserved(t *testing.T) {
	adapter := newTestAdapter()
	original := "data:image/jpeg;base64,xyz"
	result, err := adapter.FromCoreRequest(context.Background(), &format.CoreRequest{
		Model: "qwen-vl-plus",
		Messages: []format.CoreMessage{{
			Role: "user",
			Content: []format.CoreContentBlock{
				{Type: "image", ImageData: original, MediaType: "image/png"},
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	parts := result.(*chat.ChatRequest).Messages[0].Content.([]chat.ContentPart)
	if got := parts[0].ImageURL.URL; got != original {
		t.Errorf("ImageURL.URL = %q, want %q (existing data URL must pass through unchanged)", got, original)
	}
}

func TestFromCoreRequest_ImageContent_HTTPURLPreserved(t *testing.T) {
	adapter := newTestAdapter()
	original := "https://example.com/cat.png"
	result, err := adapter.FromCoreRequest(context.Background(), &format.CoreRequest{
		Model: "qwen-vl-plus",
		Messages: []format.CoreMessage{{
			Role: "user",
			Content: []format.CoreContentBlock{
				{Type: "image", ImageData: original},
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	parts := result.(*chat.ChatRequest).Messages[0].Content.([]chat.ContentPart)
	if got := parts[0].ImageURL.URL; got != original {
		t.Errorf("ImageURL.URL = %q, want %q (http(s) URL must pass through unchanged)", got, original)
	}
}

func TestFromCoreRequest_SystemInstruction(t *testing.T) {
	adapter := newTestAdapter()
	result, err := adapter.FromCoreRequest(context.Background(), &format.CoreRequest{
		Model: "gpt-4o",
		System: []format.CoreContentBlock{
			{Type: "text", Text: "You are a helpful assistant."},
		},
		Messages: []format.CoreMessage{
			{Role: "user", Content: []format.CoreContentBlock{{Type: "text", Text: "hi"}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	chatReq := result.(*chat.ChatRequest)
	if len(chatReq.Messages) != 2 {
		t.Fatalf("Messages: got %d, want 2", len(chatReq.Messages))
	}
	if chatReq.Messages[0].Role != "system" {
		t.Errorf("Messages[0].Role = %q, want system", chatReq.Messages[0].Role)
	}
	sysContent, ok := chatReq.Messages[0].Content.(string)
	if !ok || sysContent != "You are a helpful assistant." {
		t.Errorf("System content = %v", chatReq.Messages[0].Content)
	}
}

func TestFromCoreRequest_Tools(t *testing.T) {
	adapter := newTestAdapter()
	result, err := adapter.FromCoreRequest(context.Background(), &format.CoreRequest{
		Model: "gpt-4o",
		Messages: []format.CoreMessage{
			{Role: "user", Content: []format.CoreContentBlock{{Type: "text", Text: "weather?"}}},
		},
		Tools: []format.CoreTool{
			{Name: "get_weather", Description: "Get weather", InputSchema: map[string]any{"type": "object"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	chatReq := result.(*chat.ChatRequest)
	if len(chatReq.Tools) != 1 {
		t.Fatalf("Tools: got %d, want 1", len(chatReq.Tools))
	}
	if chatReq.Tools[0].Type != "function" {
		t.Errorf("Tools[0].Type = %q, want function", chatReq.Tools[0].Type)
	}
	if chatReq.Tools[0].Function.Name != "get_weather" {
		t.Errorf("Tools[0].Function.Name = %q", chatReq.Tools[0].Function.Name)
	}
}

func TestFromCoreRequest_ToolChoice(t *testing.T) {
	adapter := newTestAdapter()
	tests := []struct {
		name  string
		mode  string
		tname string
	}{
		{"none", "none", ""},
		{"auto", "auto", ""},
		{"required without name", "required", ""},
		{"required with name", "required", "get_weather"},
		{"any without name", "any", ""},
		{"any with name", "any", "get_weather"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &format.CoreRequest{
				Model:      "gpt-4o",
				Messages:   []format.CoreMessage{{Role: "user", Content: []format.CoreContentBlock{{Type: "text", Text: "hi"}}}},
				ToolChoice: &format.CoreToolChoice{Mode: tt.mode, Name: tt.tname},
			}
			result, err := adapter.FromCoreRequest(context.Background(), r)
			if err != nil {
				t.Fatal(err)
			}
			chatReq := result.(*chat.ChatRequest)
			if chatReq.ToolChoice == nil {
				t.Fatal("ToolChoice is nil")
			}
			var tc any
			if err := json.Unmarshal(chatReq.ToolChoice, &tc); err != nil {
				t.Fatalf("ToolChoice not valid JSON: %s", string(chatReq.ToolChoice))
			}
			switch tt.mode {
			case "none":
				if s, ok := tc.(string); !ok || s != "none" {
					t.Errorf("ToolChoice = %v, want \"none\"", tc)
				}
			case "auto":
				if s, ok := tc.(string); !ok || s != "auto" {
					t.Errorf("ToolChoice = %v, want \"auto\"", tc)
				}
			case "required":
				if tt.tname == "" {
					if s, ok := tc.(string); !ok || s != "required" {
						t.Errorf("ToolChoice = %v, want \"required\"", tc)
					}
				} else {
					obj, ok := tc.(map[string]any)
					if !ok {
						t.Errorf("ToolChoice = %T, want map[string]any for named function", tc)
					} else if obj["type"] != "function" {
						t.Errorf("ToolChoice type = %v, want function", obj["type"])
					}
				}
			case "any":
				if tt.tname == "" {
					if s, ok := tc.(string); !ok || s != "auto" {
						t.Errorf("ToolChoice = %v, want \"auto\"", tc)
					}
				} else {
					obj, ok := tc.(map[string]any)
					if !ok {
						t.Errorf("ToolChoice = %T, want map[string]any for named function", tc)
					} else if obj["type"] != "function" {
						t.Errorf("ToolChoice type = %v, want function", obj["type"])
					}
				}
			}
		})
	}
}

func TestFromCoreRequest_ToolUseAndToolResult(t *testing.T) {
	adapter := newTestAdapter()
	result, err := adapter.FromCoreRequest(context.Background(), &format.CoreRequest{
		Model: "gpt-4o",
		Messages: []format.CoreMessage{
			{
				Role: "assistant",
				Content: []format.CoreContentBlock{
					{Type: "text", Text: "Let me check."},
					{Type: "tool_use", ToolUseID: "call_xyz", ToolName: "get_weather", ToolInput: json.RawMessage(`{"city":"Paris"}`)},
				},
			},
			{
				Role: "tool",
				Content: []format.CoreContentBlock{
					{Type: "tool_result", ToolUseID: "call_xyz", ToolResultContent: []format.CoreContentBlock{
						{Type: "text", Text: "Sunny, 25C"},
					}},
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	chatReq := result.(*chat.ChatRequest)
	if len(chatReq.Messages) != 2 {
		t.Fatalf("Messages: got %d, want 2", len(chatReq.Messages))
	}
	if chatReq.Messages[0].Role != "assistant" {
		t.Errorf("Messages[0].Role = %q, want assistant", chatReq.Messages[0].Role)
	}
	if len(chatReq.Messages[0].ToolCalls) != 1 {
		t.Fatalf("ToolCalls: got %d, want 1", len(chatReq.Messages[0].ToolCalls))
	}
	if chatReq.Messages[0].ToolCalls[0].ID != "call_xyz" {
		t.Errorf("ToolCalls[0].ID = %q, want call_xyz", chatReq.Messages[0].ToolCalls[0].ID)
	}
	if chatReq.Messages[1].Role != "tool" {
		t.Errorf("Messages[1].Role = %q, want tool", chatReq.Messages[1].Role)
	}
	if chatReq.Messages[1].ToolCallID != "call_xyz" {
		t.Errorf("Messages[1].ToolCallID = %q, want call_xyz", chatReq.Messages[1].ToolCallID)
	}
}

func TestFromCoreRequest_ImageContent(t *testing.T) {
	adapter := newTestAdapter()
	result, err := adapter.FromCoreRequest(context.Background(), &format.CoreRequest{
		Model: "gpt-4o",
		Messages: []format.CoreMessage{
			{
				Role: "user",
				Content: []format.CoreContentBlock{
					{Type: "text", Text: "What is this?"},
					{Type: "image", ImageData: "https://example.com/img.jpg"},
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	chatReq := result.(*chat.ChatRequest)
	if len(chatReq.Messages) != 1 {
		t.Fatalf("Messages: got %d, want 1", len(chatReq.Messages))
	}
	parts, ok := chatReq.Messages[0].Content.([]chat.ContentPart)
	if !ok {
		t.Fatalf("Content type = %T, want []chat.ContentPart", chatReq.Messages[0].Content)
	}
	if len(parts) != 2 {
		t.Fatalf("parts: got %d, want 2", len(parts))
	}
	if parts[0].Type != "text" || parts[0].Text != "What is this?" {
		t.Errorf("parts[0] = %+v", parts[0])
	}
	if parts[1].Type != "image_url" || parts[1].ImageURL == nil || parts[1].ImageURL.URL != "https://example.com/img.jpg" {
		t.Errorf("parts[1] = %+v", parts[1])
	}
}

func TestFromCoreRequest_TextAndImageMultimodal(t *testing.T) {
	adapter := newTestAdapter()
	result, err := adapter.FromCoreRequest(context.Background(), &format.CoreRequest{
		Model: "gpt-4o",
		Messages: []format.CoreMessage{
			{
				Role: "user",
				Content: []format.CoreContentBlock{
					{Type: "text", Text: "Describe this image"},
					{Type: "image", ImageData: "data:image/png;base64,iVBORw0KGgo="},
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	chatReq := result.(*chat.ChatRequest)
	parts, ok := chatReq.Messages[0].Content.([]chat.ContentPart)
	if !ok {
		t.Fatalf("Content type = %T, want []chat.ContentPart", chatReq.Messages[0].Content)
	}
	if len(parts) != 2 {
		t.Fatalf("got %d parts, want 2", len(parts))
	}
}

func TestFromCoreRequest_TemperatureAndTopP(t *testing.T) {
	temp := 0.7
	topP := 0.95
	adapter := newTestAdapter()
	result, err := adapter.FromCoreRequest(context.Background(), &format.CoreRequest{
		Model:       "gpt-4o",
		Messages:    []format.CoreMessage{{Role: "user", Content: []format.CoreContentBlock{{Type: "text", Text: "hi"}}}},
		Temperature: &temp,
		TopP:        &topP,
		MaxTokens:   2048,
	})
	if err != nil {
		t.Fatal(err)
	}
	chatReq := result.(*chat.ChatRequest)
	if chatReq.Temperature == nil || *chatReq.Temperature != 0.7 {
		t.Errorf("Temperature = %v, want 0.7", chatReq.Temperature)
	}
	if chatReq.TopP == nil || *chatReq.TopP != 0.95 {
		t.Errorf("TopP = %v, want 0.95", chatReq.TopP)
	}
	if chatReq.MaxTokens != 2048 {
		t.Errorf("MaxTokens = %d, want 2048", chatReq.MaxTokens)
	}
}

func TestFromCoreRequest_StopSequences(t *testing.T) {
	adapter := newTestAdapter()
	result, err := adapter.FromCoreRequest(context.Background(), &format.CoreRequest{
		Model:         "gpt-4o",
		Messages:      []format.CoreMessage{{Role: "user", Content: []format.CoreContentBlock{{Type: "text", Text: "hi"}}}},
		StopSequences: []string{"\n", "STOP"},
	})
	if err != nil {
		t.Fatal(err)
	}
	chatReq := result.(*chat.ChatRequest)
	if len(chatReq.Stop) != 2 || chatReq.Stop[0] != "\n" || chatReq.Stop[1] != "STOP" {
		t.Errorf(`Stop = %v, want ["\n", "STOP"]`, chatReq.Stop)
	}
}

func TestFromCoreRequest_UnknownRole(t *testing.T) {
	adapter := newTestAdapter()
	result, err := adapter.FromCoreRequest(context.Background(), &format.CoreRequest{
		Model: "gpt-4o",
		Messages: []format.CoreMessage{
			{Role: "unknown_role", Content: []format.CoreContentBlock{{Type: "text", Text: "test"}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	chatReq := result.(*chat.ChatRequest)
	if len(chatReq.Messages) != 1 {
		t.Fatalf("Messages: got %d, want 1", len(chatReq.Messages))
	}
	// Unknown role falls through to "user" per mapRoleToChat.
	if chatReq.Messages[0].Role != "user" {
		t.Errorf("Role = %q, want user (fallthrough)", chatReq.Messages[0].Role)
	}
}

func TestFromCoreRequest_RawToolChoice(t *testing.T) {
	adapter := newTestAdapter()
	raw := json.RawMessage(`{"type":"function","function":{"name":"my_tool"}}`)
	result, err := adapter.FromCoreRequest(context.Background(), &format.CoreRequest{
		Model:      "gpt-4o",
		Messages:   []format.CoreMessage{{Role: "user", Content: []format.CoreContentBlock{{Type: "text", Text: "hi"}}}},
		ToolChoice: &format.CoreToolChoice{Raw: raw},
	})
	if err != nil {
		t.Fatal(err)
	}
	chatReq := result.(*chat.ChatRequest)
	if chatReq.ToolChoice == nil {
		t.Fatal("ToolChoice is nil")
	}
	if string(chatReq.ToolChoice) != string(raw) {
		t.Errorf("ToolChoice = %s, want %s", string(chatReq.ToolChoice), string(raw))
	}
}

func TestFromCoreRequest_PluginHooks(t *testing.T) {
	var hookCalled bool
	adapter := chat.NewChatProviderAdapter(0, nil, format.CorePluginHooks{
		MutateCoreRequest: func(ctx context.Context, req *format.CoreRequest) {
			hookCalled = true
			req.Model = "mutated-model"
		},
	})
	result, err := adapter.FromCoreRequest(context.Background(), &format.CoreRequest{
		Model: "original",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hookCalled {
		t.Error("MutateCoreRequest hook was not called")
	}
	chatReq := result.(*chat.ChatRequest)
	if chatReq.Model != "mutated-model" {
		t.Errorf("Model = %q, want mutated-model", chatReq.Model)
	}
}

// ============================================================================
// ToCoreResponse — *ChatResponse -> *CoreResponse
// ============================================================================

func TestToCoreResponse_BasicText(t *testing.T) {
	adapter := newTestAdapter()
	chatResp := &chat.ChatResponse{
		ID: "chatcmpl-1",
		Choices: []chat.Choice{{
			Index:        0,
			Message:      chat.ChatMessage{Role: "assistant", Content: "Hello!"},
			FinishReason: "stop",
		}},
		Usage: &chat.Usage{PromptTokens: 5, CompletionTokens: 10, TotalTokens: 15},
	}
	result, err := adapter.ToCoreResponse(context.Background(), chatResp)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "completed" {
		t.Errorf("Status = %q, want completed", result.Status)
	}
	if result.StopReason != "end_turn" {
		t.Errorf("StopReason = %q, want end_turn", result.StopReason)
	}
	if len(result.Messages) != 1 {
		t.Fatalf("Messages: got %d, want 1", len(result.Messages))
	}
	if len(result.Messages[0].Content) == 0 {
		t.Fatal("Messages[0].Content is empty")
	}
	if result.Messages[0].Content[0].Text != "Hello!" {
		t.Errorf("Text = %q, want Hello!", result.Messages[0].Content[0].Text)
	}
	if result.Usage.InputTokens != 5 || result.Usage.OutputTokens != 10 || result.Usage.TotalTokens != 15 {
		t.Errorf("Usage = %+v", result.Usage)
	}
	if result.ID != "chatcmpl-1" {
		t.Errorf("ID = %q", result.ID)
	}
}

func TestToCoreResponse_ToolCalls(t *testing.T) {
	adapter := newTestAdapter()
	chatResp := &chat.ChatResponse{
		ID: "chatcmpl-2",
		Choices: []chat.Choice{{
			Index: 0,
			Message: chat.ChatMessage{
				Role:    "assistant",
				Content: "Let me check.",
				ToolCalls: []chat.ToolCall{{
					ID: "call_xyz", Type: "function",
					Function: chat.ToolCallFunc{Name: "get_weather", Arguments: json.RawMessage(`{"city":"Paris"}`)},
				}},
			},
			FinishReason: "tool_calls",
		}},
	}
	result, err := adapter.ToCoreResponse(context.Background(), chatResp)
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != "tool_use" {
		t.Errorf("StopReason = %q, want tool_use", result.StopReason)
	}
	if len(result.Messages) != 1 {
		t.Fatalf("Messages: got %d, want 1", len(result.Messages))
	}
	var toolBlock *format.CoreContentBlock
	for i, b := range result.Messages[0].Content {
		if b.Type == "tool_use" {
			toolBlock = &result.Messages[0].Content[i]
			break
		}
	}
	if toolBlock == nil {
		t.Fatal("no tool_use content block found")
	}
	if toolBlock.ToolUseID != "call_xyz" {
		t.Errorf("ToolUseID = %q, want call_xyz", toolBlock.ToolUseID)
	}
	if toolBlock.ToolName != "get_weather" {
		t.Errorf("ToolName = %q, want get_weather", toolBlock.ToolName)
	}
	if string(toolBlock.ToolInput) != `{"city":"Paris"}` {
		t.Errorf("ToolInput = %s", string(toolBlock.ToolInput))
	}
}

func TestToCoreResponseWithRequest_DecodesNestedNamespaceToolCall(t *testing.T) {
	adapter := newTestAdapter()
	req := &format.CoreRequest{
		Extensions: map[string]any{
			"codex_tool_map": map[string]any{
				"multi_agent_v1": map[string]any{
					"kind":        "nested_namespace",
					"openai_name": "multi_agent_v1",
					"namespace":   "multi_agent_v1",
				},
			},
		},
	}
	chatResp := &chat.ChatResponse{
		ID: "chatcmpl-ns",
		Choices: []chat.Choice{{
			Index: 0,
			Message: chat.ChatMessage{
				Role: "assistant",
				ToolCalls: []chat.ToolCall{{
					ID: "call_wait", Type: "function",
					Function: chat.ToolCallFunc{
						Name:      "multi_agent_v1",
						Arguments: json.RawMessage(`{"action":"wait_agent","targets":["a"],"timeout_ms":1000}`),
					},
				}},
			},
			FinishReason: "tool_calls",
		}},
	}

	result, err := adapter.ToCoreResponseWithRequest(context.Background(), req, chatResp)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Messages) != 1 || len(result.Messages[0].Content) != 1 {
		t.Fatalf("unexpected messages: %+v", result.Messages)
	}
	block := result.Messages[0].Content[0]
	if block.ToolName != "wait_agent" || block.ToolNamespace != "multi_agent_v1" {
		t.Fatalf("tool fields = name %q namespace %q", block.ToolName, block.ToolNamespace)
	}
	if string(block.ToolInput) != `{"targets":["a"],"timeout_ms":1000}` {
		t.Fatalf("tool input = %s", block.ToolInput)
	}
}

func TestToCoreResponse_FinishReasonVariants(t *testing.T) {
	adapter := newTestAdapter()
	tests := []struct {
		in  string
		out string
	}{
		{"stop", "end_turn"},
		{"length", "max_tokens"},
		{"content_filter", "content_filter"},
		{"tool_calls", "tool_use"},
		{"unknown", "unknown"},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			chatResp := &chat.ChatResponse{
				Choices: []chat.Choice{{
					Index:        0,
					Message:      chat.ChatMessage{Role: "assistant", Content: "x"},
					FinishReason: tt.in,
				}},
			}
			result, err := adapter.ToCoreResponse(context.Background(), chatResp)
			if err != nil {
				t.Fatal(err)
			}
			if result.StopReason != tt.out {
				t.Errorf("StopReason = %q, want %q", result.StopReason, tt.out)
			}
		})
	}
}

func TestToCoreResponse_EmptyChoices(t *testing.T) {
	adapter := newTestAdapter()
	chatResp := &chat.ChatResponse{
		ID:      "empty",
		Choices: []chat.Choice{},
	}
	result, err := adapter.ToCoreResponse(context.Background(), chatResp)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Messages) != 0 {
		t.Errorf("Messages: got %d, want 0", len(result.Messages))
	}
	if result.Status != "completed" {
		t.Errorf("Status = %q, want completed", result.Status)
	}
}

func TestToCoreResponse_MultipleChoices(t *testing.T) {
	adapter := newTestAdapter()
	chatResp := &chat.ChatResponse{
		Choices: []chat.Choice{
			{Index: 0, Message: chat.ChatMessage{Role: "assistant", Content: "First"}, FinishReason: "stop"},
			{Index: 1, Message: chat.ChatMessage{Role: "assistant", Content: "Second"}, FinishReason: "stop"},
		},
	}
	result, err := adapter.ToCoreResponse(context.Background(), chatResp)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Messages) != 2 {
		t.Fatalf("Messages: got %d, want 2", len(result.Messages))
	}
}

func TestToCoreResponse_NoUsage(t *testing.T) {
	adapter := newTestAdapter()
	chatResp := &chat.ChatResponse{
		Choices: []chat.Choice{{
			Index: 0, Message: chat.ChatMessage{Role: "assistant", Content: "x"}, FinishReason: "stop",
		}},
	}
	result, err := adapter.ToCoreResponse(context.Background(), chatResp)
	if err != nil {
		t.Fatal(err)
	}
	// Should not panic; Usage is zero-value.
	_ = result
}

func TestToCoreResponse_WrongType(t *testing.T) {
	adapter := newTestAdapter()
	_, err := adapter.ToCoreResponse(context.Background(), "not-a-chat-response")
	if err == nil {
		t.Fatal("expected error for wrong type")
	}
}

func TestToCoreResponse_LengthStatus(t *testing.T) {
	adapter := newTestAdapter()
	chatResp := &chat.ChatResponse{
		Choices: []chat.Choice{{
			Index: 0, Message: chat.ChatMessage{Role: "assistant", Content: "x"}, FinishReason: "length",
		}},
	}
	result, err := adapter.ToCoreResponse(context.Background(), chatResp)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "incomplete" {
		t.Errorf("Status = %q, want incomplete for length finish", result.Status)
	}
}

func TestToCoreResponse_ContentFilterStatus(t *testing.T) {
	adapter := newTestAdapter()
	chatResp := &chat.ChatResponse{
		Choices: []chat.Choice{{
			Index: 0, Message: chat.ChatMessage{Role: "assistant", Content: "x"}, FinishReason: "content_filter",
		}},
	}
	result, err := adapter.ToCoreResponse(context.Background(), chatResp)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "failed" {
		t.Errorf("Status = %q, want failed for content_filter", result.Status)
	}
}

func TestToCoreResponse_EmptyContent(t *testing.T) {
	adapter := newTestAdapter()
	chatResp := &chat.ChatResponse{
		Choices: []chat.Choice{{
			Index: 0, Message: chat.ChatMessage{Role: "assistant"}, FinishReason: "stop",
		}},
	}
	result, err := adapter.ToCoreResponse(context.Background(), chatResp)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Messages) != 1 {
		t.Fatalf("Messages: got %d, want 1", len(result.Messages))
	}
	if len(result.Messages[0].Content) != 0 {
		t.Errorf("Content = %+v, want empty for nil content", result.Messages[0].Content)
	}
}

// ============================================================================
// ToCoreStream — <-chan ChatStreamChunk -> <-chan CoreStreamEvent
// ============================================================================

func TestToCoreStream_BasicDelta(t *testing.T) {
	adapter := newTestAdapter()
	src := make(chan chat.ChatStreamChunk, 4)
	src <- chat.ChatStreamChunk{
		ID: "chunk1", Choices: []chat.StreamChoice{{Index: 0, Delta: chat.Delta{Role: "assistant"}}},
	}
	src <- chat.ChatStreamChunk{
		Choices: []chat.StreamChoice{{Index: 0, Delta: chat.Delta{Content: "Hello"}}},
	}
	src <- chat.ChatStreamChunk{
		Choices: []chat.StreamChoice{{Index: 0, Delta: chat.Delta{}, FinishReason: "stop"}},
	}
	src <- chat.ChatStreamChunk{
		Usage: &chat.Usage{PromptTokens: 5, CompletionTokens: 10, TotalTokens: 15},
	}
	close(src)

	events, err := adapter.ToCoreStream(context.Background(), (<-chan chat.ChatStreamChunk)(src))
	if err != nil {
		t.Fatal(err)
	}

	var evts []format.CoreStreamEvent
	for e := range events.Events {
		evts = append(evts, e)
	}

	if len(evts) < 4 {
		t.Fatalf("got %d events, want at least 4", len(evts))
	}
	if evts[0].Type != format.CoreContentBlockStarted {
		t.Errorf("evts[0].Type = %q, want %q", evts[0].Type, format.CoreContentBlockStarted)
	}
	if evts[1].Type != format.CoreTextDelta {
		t.Errorf("evts[1].Type = %q, want %q", evts[1].Type, format.CoreTextDelta)
	}
	if evts[2].Type != format.CoreContentBlockDone {
		t.Errorf("evts[2].Type = %q, want %q", evts[2].Type, format.CoreContentBlockDone)
	}
	if evts[len(evts)-1].Type != format.CoreEventCompleted {
		t.Errorf("evts[-1].Type = %q, want %q", evts[len(evts)-1].Type, format.CoreEventCompleted)
	}
	if evts[len(evts)-1].Usage == nil {
		t.Error("completed event should have usage")
	}
}

func TestToCoreStream_ToolCallArgsDelta(t *testing.T) {
	adapter := newTestAdapter()
	src := make(chan chat.ChatStreamChunk, 4)
	src <- chat.ChatStreamChunk{
		Choices: []chat.StreamChoice{{
			Index: 0,
			Delta: chat.Delta{
				Role: "assistant",
				ToolCalls: []chat.ToolCall{{
					ID: "call_xyz", Type: "function",
					Function: chat.ToolCallFunc{Name: "get_weather", Arguments: json.RawMessage(``)},
				}},
			},
		}},
	}
	src <- chat.ChatStreamChunk{
		Choices: []chat.StreamChoice{{
			Index: 0,
			Delta: chat.Delta{
				ToolCalls: []chat.ToolCall{{
					Function: chat.ToolCallFunc{Arguments: json.RawMessage(`{"city":"Paris"}`)},
				}},
			},
		}},
	}
	src <- chat.ChatStreamChunk{
		Choices: []chat.StreamChoice{{Index: 0, Delta: chat.Delta{}, FinishReason: "stop"}},
	}
	close(src)

	events, err := adapter.ToCoreStream(context.Background(), (<-chan chat.ChatStreamChunk)(src))
	if err != nil {
		t.Fatal(err)
	}

	var evts []format.CoreStreamEvent
	for e := range events.Events {
		evts = append(evts, e)
	}

	var toolCallDeltaCount int
	for _, e := range evts {
		if e.Type == format.CoreToolCallArgsDelta {
			toolCallDeltaCount++
		}
	}
	if toolCallDeltaCount != 1 {
		t.Errorf("tool_call_args.delta count = %d, want 1", toolCallDeltaCount)
	}
}

func TestToCoreStream_EmptyChunk(t *testing.T) {
	adapter := newTestAdapter()
	src := make(chan chat.ChatStreamChunk, 2)
	src <- chat.ChatStreamChunk{
		Usage: &chat.Usage{PromptTokens: 3, CompletionTokens: 6, TotalTokens: 9},
	}
	close(src)

	events, err := adapter.ToCoreStream(context.Background(), (<-chan chat.ChatStreamChunk)(src))
	if err != nil {
		t.Fatal(err)
	}

	var evts []format.CoreStreamEvent
	for e := range events.Events {
		evts = append(evts, e)
	}

	if len(evts) != 1 {
		t.Fatalf("got %d events, want 1 (completed only)", len(evts))
	}
	if evts[0].Type != format.CoreEventCompleted {
		t.Errorf("event.Type = %q, want %q", evts[0].Type, format.CoreEventCompleted)
	}
}

func TestToCoreStream_NoContent(t *testing.T) {
	adapter := newTestAdapter()
	src := make(chan chat.ChatStreamChunk)
	close(src)

	events, err := adapter.ToCoreStream(context.Background(), (<-chan chat.ChatStreamChunk)(src))
	if err != nil {
		t.Fatal(err)
	}

	var evts []format.CoreStreamEvent
	for e := range events.Events {
		evts = append(evts, e)
	}

	if len(evts) != 1 {
		t.Fatalf("got %d events, want 1 (completed)", len(evts))
	}
	if evts[0].Type != format.CoreEventCompleted {
		t.Errorf("event.Type = %q, want %q", evts[0].Type, format.CoreEventCompleted)
	}
}

func TestToCoreStream_ContextCancel(t *testing.T) {
	adapter := newTestAdapter()
	src := make(chan chat.ChatStreamChunk, 1)
	defer close(src)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	events, err := adapter.ToCoreStream(ctx, (<-chan chat.ChatStreamChunk)(src))
	if err != nil {
		t.Fatal(err)
	}

	// Events channel should close immediately due to cancelled context.
	var evts []format.CoreStreamEvent
	for e := range events.Events {
		evts = append(evts, e)
	}
	if len(evts) != 0 {
		t.Errorf("got %d events, want 0 for cancelled context", len(evts))
	}
}

func TestToCoreStream_WrongType(t *testing.T) {
	adapter := newTestAdapter()
	_, err := adapter.ToCoreStream(context.Background(), "not-a-channel")
	if err == nil {
		t.Fatal("expected error for wrong type")
	}
}

func TestToCoreStream_MultiChoice(t *testing.T) {
	adapter := newTestAdapter()
	src := make(chan chat.ChatStreamChunk, 4)
	// Send chunks with 2 choices.
	src <- chat.ChatStreamChunk{
		Choices: []chat.StreamChoice{
			{Index: 0, Delta: chat.Delta{Role: "assistant"}},
			{Index: 1, Delta: chat.Delta{Role: "assistant"}},
		},
	}
	src <- chat.ChatStreamChunk{
		Choices: []chat.StreamChoice{
			{Index: 0, Delta: chat.Delta{Content: "Alpha"}},
			{Index: 1, Delta: chat.Delta{Content: "Beta"}},
		},
	}
	src <- chat.ChatStreamChunk{
		Choices: []chat.StreamChoice{
			{Index: 0, Delta: chat.Delta{}, FinishReason: "stop"},
			{Index: 1, Delta: chat.Delta{}, FinishReason: "stop"},
		},
	}
	close(src)

	events, err := adapter.ToCoreStream(context.Background(), (<-chan chat.ChatStreamChunk)(src))
	if err != nil {
		t.Fatal(err)
	}

	var evts []format.CoreStreamEvent
	for e := range events.Events {
		evts = append(evts, e)
	}

	// Each choice: started, delta, done = 3 events per choice = 6 content events + 1 completed = 7
	if len(evts) < 7 {
		t.Fatalf("got %d events, want at least 7", len(evts))
	}
	startedCount := 0
	deltaCount := 0
	doneCount := 0
	for _, e := range evts {
		switch e.Type {
		case format.CoreContentBlockStarted:
			startedCount++
		case format.CoreTextDelta:
			deltaCount++
		case format.CoreContentBlockDone:
			doneCount++
		case format.CoreEventCompleted:
			// ok
		default:
			t.Errorf("unexpected event type: %q", e.Type)
		}
	}
	if startedCount != 2 {
		t.Errorf("started count = %d, want 2", startedCount)
	}
	if deltaCount != 2 {
		t.Errorf("delta count = %d, want 2", deltaCount)
	}
	if doneCount != 2 {
		t.Errorf("done count = %d, want 2", doneCount)
	}
}

// ============================================================================
// Coverage edge cases for ≥95%
// ============================================================================

func TestFromCoreRequest_SystemMessage(t *testing.T) {
	adapter := newTestAdapter()
	result, err := adapter.FromCoreRequest(context.Background(), &format.CoreRequest{
		Model: "gpt-4o",
		Messages: []format.CoreMessage{
			{Role: "system", Content: []format.CoreContentBlock{{Type: "text", Text: "You are a helpful assistant."}}},
			{Role: "user", Content: []format.CoreContentBlock{{Type: "text", Text: "hi"}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	chatReq := result.(*chat.ChatRequest)
	if len(chatReq.Messages) < 2 {
		t.Fatalf("Messages: got %d, want 2", len(chatReq.Messages))
	}
	if chatReq.Messages[0].Role != "system" {
		t.Errorf("Messages[0].Role = %q, want system", chatReq.Messages[0].Role)
	}
}

func TestFromCoreRequest_SystemContentUnknownBlock(t *testing.T) {
	adapter := newTestAdapter()
	result, err := adapter.FromCoreRequest(context.Background(), &format.CoreRequest{
		Model: "gpt-4o",
		System: []format.CoreContentBlock{
			{Type: "text", Text: "Be helpful."},
			{Type: "reasoning", Text: "think step by step"},
			{Type: "custom_block", Text: "fallback"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	chatReq := result.(*chat.ChatRequest)
	if len(chatReq.Messages) < 1 || chatReq.Messages[0].Role != "system" {
		t.Fatalf("expected system message, got %+v", chatReq.Messages)
	}
	content, ok := chatReq.Messages[0].Content.(string)
	if !ok {
		t.Fatalf("Content type = %T, want string", chatReq.Messages[0].Content)
	}
	if content != "Be helpful.fallback" {
		t.Errorf("Content = %q, want 'Be helpful.fallback'", content)
	}
}

func TestFromCoreRequest_UnknownContentBlockType(t *testing.T) {
	adapter := newTestAdapter()
	result, err := adapter.FromCoreRequest(context.Background(), &format.CoreRequest{
		Model: "gpt-4o",
		Messages: []format.CoreMessage{
			{
				Role: "user",
				Content: []format.CoreContentBlock{
					{Type: "text", Text: "hello"},
					{Type: "unknown_type", Text: "fallback_text"},
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	chatReq := result.(*chat.ChatRequest)
	content, ok := chatReq.Messages[0].Content.(string)
	if !ok {
		t.Fatalf("Content type = %T, want string", chatReq.Messages[0].Content)
	}
	if content != "hellofallback_text" {
		t.Errorf("Content = %q, want 'hellofallback_text'", content)
	}
}

func TestFromCoreRequest_UnknownContentBlockInImage(t *testing.T) {
	adapter := newTestAdapter()
	result, err := adapter.FromCoreRequest(context.Background(), &format.CoreRequest{
		Model: "gpt-4o",
		Messages: []format.CoreMessage{
			{
				Role: "user",
				Content: []format.CoreContentBlock{
					{Type: "text", Text: "What is this?"},
					{Type: "image", ImageData: "https://example.com/img.jpg"},
					{Type: "unknown_block", Text: "extra"},
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	chatReq := result.(*chat.ChatRequest)
	parts, ok := chatReq.Messages[0].Content.([]chat.ContentPart)
	if !ok {
		t.Fatalf("Content type = %T, want []chat.ContentPart", chatReq.Messages[0].Content)
	}
	if len(parts) != 3 {
		t.Fatalf("parts: got %d, want 3", len(parts))
	}
	if parts[2].Type != "text" || parts[2].Text != "extra" {
		t.Errorf("parts[2] = %+v, want text/extra", parts[2])
	}
}

func TestFromCoreRequest_ToolChoiceUnknownMode(t *testing.T) {
	adapter := newTestAdapter()
	t.Run("unknown mode without name", func(t *testing.T) {
		result, err := adapter.FromCoreRequest(context.Background(), &format.CoreRequest{
			Model:      "gpt-4o",
			Messages:   []format.CoreMessage{{Role: "user", Content: []format.CoreContentBlock{{Type: "text", Text: "hi"}}}},
			ToolChoice: &format.CoreToolChoice{Mode: "unknown_mode"},
		})
		if err != nil {
			t.Fatal(err)
		}
		chatReq := result.(*chat.ChatRequest)
		var tc any
		if err := json.Unmarshal(chatReq.ToolChoice, &tc); err != nil {
			t.Fatalf("ToolChoice not valid JSON: %s", string(chatReq.ToolChoice))
		}
		s, ok := tc.(string)
		if !ok || s != "auto" {
			t.Errorf("ToolChoice = %v, want auto", tc)
		}
	})
	t.Run("unknown mode with name", func(t *testing.T) {
		result, err := adapter.FromCoreRequest(context.Background(), &format.CoreRequest{
			Model:      "gpt-4o",
			Messages:   []format.CoreMessage{{Role: "user", Content: []format.CoreContentBlock{{Type: "text", Text: "hi"}}}},
			ToolChoice: &format.CoreToolChoice{Mode: "unknown_mode", Name: "my_tool"},
		})
		if err != nil {
			t.Fatal(err)
		}
		chatReq := result.(*chat.ChatRequest)
		var tc map[string]any
		if err := json.Unmarshal(chatReq.ToolChoice, &tc); err != nil {
			t.Fatalf("ToolChoice not valid JSON: %s", string(chatReq.ToolChoice))
		}
		if tc["type"] != "function" {
			t.Errorf("ToolChoice type = %v, want function", tc["type"])
		}
	})
}

func TestFromCoreRequest_DefaultMaxTokens(t *testing.T) {
	adapter := chat.NewChatProviderAdapter(4096, nil, format.CorePluginHooks{})
	result, err := adapter.FromCoreRequest(context.Background(), &format.CoreRequest{
		Model:    "gpt-4o",
		Messages: []format.CoreMessage{{Role: "user", Content: []format.CoreContentBlock{{Type: "text", Text: "hi"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	chatReq := result.(*chat.ChatRequest)
	if chatReq.MaxTokens != 4096 {
		t.Errorf("MaxTokens = %d, want 4096", chatReq.MaxTokens)
	}
}

func TestToCoreResponse_ContentSlice(t *testing.T) {
	adapter := newTestAdapter()
	chatResp := &chat.ChatResponse{
		ID: "chatcmpl-slice",
		Choices: []chat.Choice{{
			Index: 0,
			Message: chat.ChatMessage{
				Role:    "assistant",
				Content: []any{map[string]any{"type": "text", "text": "Hello from slice"}},
			},
			FinishReason: "stop",
		}},
	}
	result, err := adapter.ToCoreResponse(context.Background(), chatResp)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Messages) != 1 {
		t.Fatalf("Messages: got %d, want 1", len(result.Messages))
	}
	if len(result.Messages[0].Content) < 1 {
		t.Fatal("Content is empty")
	}
	if result.Messages[0].Content[0].Text != "Hello from slice" {
		t.Errorf("Text = %q, want 'Hello from slice'", result.Messages[0].Content[0].Text)
	}
}

func TestToCoreResponse_ContentSliceImage(t *testing.T) {
	adapter := newTestAdapter()
	chatResp := &chat.ChatResponse{
		Choices: []chat.Choice{{
			Index: 0,
			Message: chat.ChatMessage{
				Role:    "assistant",
				Content: []any{map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://example.com/img.png"}}},
			},
			FinishReason: "stop",
		}},
	}
	result, err := adapter.ToCoreResponse(context.Background(), chatResp)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Messages) != 1 {
		t.Fatalf("Messages: got %d, want 1", len(result.Messages))
	}
	if result.Messages[0].Content[0].Type != "image" {
		t.Errorf("Content[0].Type = %q, want image", result.Messages[0].Content[0].Type)
	}
	if result.Messages[0].Content[0].ImageData != "https://example.com/img.png" {
		t.Errorf("ImageData = %q", result.Messages[0].Content[0].ImageData)
	}
}

func TestToCoreResponse_ContentSliceUnknownType(t *testing.T) {
	adapter := newTestAdapter()
	chatResp := &chat.ChatResponse{
		Choices: []chat.Choice{{
			Index: 0,
			Message: chat.ChatMessage{
				Role:    "assistant",
				Content: []any{map[string]any{"type": "unknown", "text": "fallback"}},
			},
			FinishReason: "stop",
		}},
	}
	result, err := adapter.ToCoreResponse(context.Background(), chatResp)
	if err != nil {
		t.Fatal(err)
	}
	if result.Messages[0].Content[0].Text != "fallback" {
		t.Errorf("Text = %q, want fallback", result.Messages[0].Content[0].Text)
	}
}

func TestToCoreResponse_ContentSliceUnknownTypeNoText(t *testing.T) {
	adapter := newTestAdapter()
	chatResp := &chat.ChatResponse{
		Choices: []chat.Choice{{
			Index: 0,
			Message: chat.ChatMessage{
				Role:    "assistant",
				Content: []any{map[string]any{"type": "unknown"}},
			},
			FinishReason: "stop",
		}},
	}
	result, err := adapter.ToCoreResponse(context.Background(), chatResp)
	if err != nil {
		t.Fatal(err)
	}
	_ = result
}

func TestToCoreResponse_ContentSliceImageWithoutURL(t *testing.T) {
	adapter := newTestAdapter()
	chatResp := &chat.ChatResponse{
		Choices: []chat.Choice{{
			Index: 0,
			Message: chat.ChatMessage{
				Role:    "assistant",
				Content: []any{map[string]any{"type": "image_url", "image_url": "not-a-map"}},
			},
			FinishReason: "stop",
		}},
	}
	result, err := adapter.ToCoreResponse(context.Background(), chatResp)
	if err != nil {
		t.Fatal(err)
	}
	_ = result
}

func TestToCoreResponse_ContentNil(t *testing.T) {
	adapter := newTestAdapter()
	chatResp := &chat.ChatResponse{
		Choices: []chat.Choice{{
			Index: 0,
			Message: chat.ChatMessage{
				Role:    "assistant",
				Content: nil,
			},
			FinishReason: "stop",
		}},
	}
	result, err := adapter.ToCoreResponse(context.Background(), chatResp)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Messages[0].Content) != 0 {
		t.Errorf("Content = %+v, want empty for nil", result.Messages[0].Content)
	}
}

func TestToCoreResponse_ContentWrongType(t *testing.T) {
	adapter := newTestAdapter()
	chatResp := &chat.ChatResponse{
		Choices: []chat.Choice{{
			Index: 0,
			Message: chat.ChatMessage{
				Role:    "assistant",
				Content: 42,
			},
			FinishReason: "stop",
		}},
	}
	result, err := adapter.ToCoreResponse(context.Background(), chatResp)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Messages[0].Content) != 0 {
		t.Errorf("Content = %+v, want empty for int type", result.Messages[0].Content)
	}
}

func TestToCoreStream_WithModel(t *testing.T) {
	adapter := newTestAdapter()
	src := make(chan chat.ChatStreamChunk, 2)
	src <- chat.ChatStreamChunk{
		ID: "chunk1", Model: "gpt-4o",
		Choices: []chat.StreamChoice{{Index: 0, Delta: chat.Delta{Role: "assistant", Content: "Hello"}}},
	}
	src <- chat.ChatStreamChunk{
		Choices: []chat.StreamChoice{{Index: 0, Delta: chat.Delta{}, FinishReason: "stop"}},
	}
	close(src)

	events, err := adapter.ToCoreStream(context.Background(), (<-chan chat.ChatStreamChunk)(src))
	if err != nil {
		t.Fatal(err)
	}

	var evts []format.CoreStreamEvent
	for e := range events.Events {
		evts = append(evts, e)
	}
	if len(evts) < 4 {
		t.Fatalf("got %d events, want at least 4", len(evts))
	}
	if evts[len(evts)-1].Model != "gpt-4o" {
		t.Errorf("completed event Model = %q, want gpt-4o", evts[len(evts)-1].Model)
	}
}

func TestToCoreStream_ContentBlockStartedNoRole(t *testing.T) {
	adapter := newTestAdapter()
	src := make(chan chat.ChatStreamChunk, 2)
	src <- chat.ChatStreamChunk{
		Choices: []chat.StreamChoice{{Index: 0, Delta: chat.Delta{Content: "Hello"}}},
	}
	src <- chat.ChatStreamChunk{
		Choices: []chat.StreamChoice{{Index: 0, Delta: chat.Delta{}, FinishReason: "stop"}},
	}
	close(src)

	events, err := adapter.ToCoreStream(context.Background(), (<-chan chat.ChatStreamChunk)(src))
	if err != nil {
		t.Fatal(err)
	}

	var evts []format.CoreStreamEvent
	for e := range events.Events {
		evts = append(evts, e)
	}
	if len(evts) < 3 {
		t.Fatalf("got %d events, want at least 3", len(evts))
	}
}

func TestToCoreStream_ToolCallArgsDeltaByPosition(t *testing.T) {
	adapter := newTestAdapter()
	src := make(chan chat.ChatStreamChunk, 4)
	idx0 := 0
	idx1 := 1
	src <- chat.ChatStreamChunk{
		Choices: []chat.StreamChoice{{
			Index: 0,
			Delta: chat.Delta{
				Role: "assistant",
				ToolCalls: []chat.ToolCall{
					{
						Index: &idx0, ID: "call_a", Type: "function",
						Function: chat.ToolCallFunc{Name: "tool_a", Arguments: json.RawMessage(``)},
					},
					{
						Index: &idx1, ID: "call_b", Type: "function",
						Function: chat.ToolCallFunc{Name: "tool_b", Arguments: json.RawMessage(``)},
					},
				},
			},
		}},
	}
	src <- chat.ChatStreamChunk{
		Choices: []chat.StreamChoice{{
			Index: 0,
			Delta: chat.Delta{
				ToolCalls: []chat.ToolCall{
					{Index: &idx0, Function: chat.ToolCallFunc{Arguments: json.RawMessage(`"{\"a\":1}"`)}},
					{Index: &idx1, Function: chat.ToolCallFunc{Arguments: json.RawMessage(`"{\"b\":2}"`)}},
				},
			},
		}},
	}
	src <- chat.ChatStreamChunk{
		Choices: []chat.StreamChoice{{Index: 0, FinishReason: "tool_calls"}},
	}
	close(src)

	events, err := adapter.ToCoreStream(context.Background(), (<-chan chat.ChatStreamChunk)(src))
	if err != nil {
		t.Fatal(err)
	}
	var deltas []format.CoreStreamEvent
	for e := range events.Events {
		if e.Type == format.CoreToolCallArgsDelta {
			deltas = append(deltas, e)
		}
	}
	if len(deltas) != 2 {
		t.Fatalf("tool_call_args.delta count = %d, want 2", len(deltas))
	}
	if deltas[0].Index == deltas[1].Index {
		t.Fatalf("tool deltas should map to different indices, got both at %d", deltas[0].Index)
	}
}

func TestToCoreStream_ToolCallArgsDeltaRespectsExplicitToolIndex(t *testing.T) {
	adapter := newTestAdapter()
	src := make(chan chat.ChatStreamChunk, 4)
	idx0 := 0
	idx1 := 1
	src <- chat.ChatStreamChunk{
		Choices: []chat.StreamChoice{{
			Index: 0,
			Delta: chat.Delta{
				Role: "assistant",
				ToolCalls: []chat.ToolCall{
					{
						Index: &idx0, ID: "call_a", Type: "function",
						Function: chat.ToolCallFunc{Name: "tool_a", Arguments: json.RawMessage(``)},
					},
					{
						Index: &idx1, ID: "call_b", Type: "function",
						Function: chat.ToolCallFunc{Name: "tool_b", Arguments: json.RawMessage(``)},
					},
				},
			},
		}},
	}
	src <- chat.ChatStreamChunk{
		Choices: []chat.StreamChoice{{
			Index: 0,
			Delta: chat.Delta{
				ToolCalls: []chat.ToolCall{
					{Index: &idx1, Function: chat.ToolCallFunc{Arguments: json.RawMessage(`"{\"b\":2}"`)}},
					{Index: &idx0, Function: chat.ToolCallFunc{Arguments: json.RawMessage(`"{\"a\":1}"`)}},
				},
			},
		}},
	}
	src <- chat.ChatStreamChunk{
		Choices: []chat.StreamChoice{{Index: 0, FinishReason: "tool_calls"}},
	}
	close(src)

	events, err := adapter.ToCoreStream(context.Background(), (<-chan chat.ChatStreamChunk)(src))
	if err != nil {
		t.Fatal(err)
	}
	var started []format.CoreStreamEvent
	var deltas []format.CoreStreamEvent
	for e := range events.Events {
		if e.Type == format.CoreContentBlockStarted && e.ContentBlock != nil && e.ContentBlock.Type == "tool_use" {
			started = append(started, e)
		}
		if e.Type == format.CoreToolCallArgsDelta {
			deltas = append(deltas, e)
		}
	}
	if len(started) != 2 || len(deltas) != 2 {
		t.Fatalf("started=%d deltas=%d, want 2/2", len(started), len(deltas))
	}
	toolIndexByID := map[string]int{
		started[0].ContentBlock.ToolUseID: started[0].Index,
		started[1].ContentBlock.ToolUseID: started[1].Index,
	}
	idxA, okA := toolIndexByID["call_a"]
	idxB, okB := toolIndexByID["call_b"]
	if !okA || !okB {
		t.Fatalf("missing tool start mapping: %+v", toolIndexByID)
	}
	if idxA == idxB {
		t.Fatalf("tool indices should differ, both=%d", idxA)
	}
	for _, d := range deltas {
		if d.Delta == `{"a":1}` && d.Index != idxA {
			t.Fatalf("delta for call_a routed to %d, want %d", d.Index, idxA)
		}
		if d.Delta == `{"b":2}` && d.Index != idxB {
			t.Fatalf("delta for call_b routed to %d, want %d", d.Index, idxB)
		}
	}
}

func TestFromCoreRequest_ToolResultInImagePath(t *testing.T) {
	adapter := newTestAdapter()
	result, err := adapter.FromCoreRequest(context.Background(), &format.CoreRequest{
		Model: "gpt-4o",
		Messages: []format.CoreMessage{
			{
				Role: "user",
				Content: []format.CoreContentBlock{
					{Type: "image", ImageData: "https://example.com/img.jpg"},
					{Type: "tool_result", ToolUseID: "call_123", ToolResultContent: []format.CoreContentBlock{
						{Type: "text", Text: "Result data"},
					}},
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	chatReq := result.(*chat.ChatRequest)
	parts, ok := chatReq.Messages[0].Content.([]chat.ContentPart)
	if !ok {
		t.Fatalf("Content type = %T, want []chat.ContentPart", chatReq.Messages[0].Content)
	}
	if len(parts) != 2 {
		t.Fatalf("parts: got %d, want 2", len(parts))
	}
}

func TestFromCoreRequest_ToolResultEmptyResultInImagePath(t *testing.T) {
	adapter := newTestAdapter()
	result, err := adapter.FromCoreRequest(context.Background(), &format.CoreRequest{
		Model: "gpt-4o",
		Messages: []format.CoreMessage{
			{
				Role: "user",
				Content: []format.CoreContentBlock{
					{Type: "image", ImageData: "https://example.com/img.jpg"},
					{Type: "tool_result", ToolUseID: "call_123", ToolResultContent: []format.CoreContentBlock{}},
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	chatReq := result.(*chat.ChatRequest)
	parts, ok := chatReq.Messages[0].Content.([]chat.ContentPart)
	if !ok {
		t.Fatalf("Content type = %T, want []chat.ContentPart", chatReq.Messages[0].Content)
	}
	if len(parts) != 1 {
		t.Fatalf("parts: got %d, want 1 (only image, tool_result skipped due to empty text)", len(parts))
	}
	if parts[0].Type != "image_url" {
		t.Errorf("parts[0].Type = %q, want image_url", parts[0].Type)
	}
}

func TestFromCoreRequest_DefaultContentBlockNoText(t *testing.T) {
	adapter := newTestAdapter()
	result, err := adapter.FromCoreRequest(context.Background(), &format.CoreRequest{
		Model: "gpt-4o",
		Messages: []format.CoreMessage{
			{
				Role: "user",
				Content: []format.CoreContentBlock{
					{Type: "text", Text: "hello"},
					{Type: "unknown_no_text"},
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	chatReq := result.(*chat.ChatRequest)
	content, ok := chatReq.Messages[0].Content.(string)
	if !ok {
		t.Fatalf("Content type = %T, want string", chatReq.Messages[0].Content)
	}
	if content != "hello" {
		t.Errorf("Content = %q, want 'hello'", content)
	}
}

// ============================================================================
// Regression tests for bug fixes
// ============================================================================

func TestNewClient_NormalizesBaseURL(t *testing.T) {
	tests := []struct {
		name      string
		urlSuffix string // appended to srv.URL
		wantPath  string // expected request path
	}{
		{"no /v1", "", "/v1/chat/completions"},
		{"with /v1", "/v1", "/v1/chat/completions"},
		{"deep /v1", "/zen/go/v1", "/zen/go/v1/chat/completions"},
		{"trailing slash", "/", "/v1/chat/completions"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotPath string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				w.Header().Set("content-type", "application/json")
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`{"id":"x","object":"chat.completion","choices":[]}`))
			}))
			defer srv.Close()

			client := chat.NewClient(chat.ClientConfig{
				BaseURL: srv.URL + tt.urlSuffix,
				APIKey:  "test-key",
				Client:  srv.Client(),
			})
			_, err := client.CreateChat(context.Background(), &chat.ChatRequest{
				Model:    "test",
				Messages: []chat.ChatMessage{{Role: "user", Content: "hi"}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if gotPath != tt.wantPath {
				t.Errorf("request path = %q, want %q", gotPath, tt.wantPath)
			}
		})
	}
}

func TestFromCoreRequest_ToolCallArgumentsAreJSONString(t *testing.T) {
	adapter := newTestAdapter()

	req := &format.CoreRequest{
		Model: "test-model",
		Messages: []format.CoreMessage{
			{
				Role: "assistant",
				Content: []format.CoreContentBlock{
					{
						Type:      "tool_use",
						ToolUseID: "call_123",
						ToolName:  "get_weather",
						ToolInput: json.RawMessage(`{"city":"Paris"}`),
					},
				},
			},
		},
	}

	result, err := adapter.FromCoreRequest(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}

	chatReq, ok := result.(*chat.ChatRequest)
	if !ok {
		t.Fatalf("expected *ChatRequest, got %T", result)
	}

	if len(chatReq.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(chatReq.Messages))
	}

	msg := chatReq.Messages[0]
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(msg.ToolCalls))
	}

	tc := msg.ToolCalls[0]
	if tc.ID != "call_123" {
		t.Errorf("ToolCall.ID = %q, want call_123", tc.ID)
	}
	if tc.Function.Name != "get_weather" {
		t.Errorf("ToolCall.Function.Name = %q, want get_weather", tc.Function.Name)
	}

	// CRITICAL: arguments must be a JSON string (including quotes), not a raw object
	argsStr := string(tc.Function.Arguments)
	if !strings.HasPrefix(argsStr, `"`) {
		t.Errorf("arguments should start with double-quote character (JSON string encoding), got: %s", argsStr)
	}
	// Parse as JSON string to get inner JSON object
	var innerStr string
	if err := json.Unmarshal(tc.Function.Arguments, &innerStr); err != nil {
		t.Fatalf("arguments is not a valid JSON string: %v (raw: %s)", err, argsStr)
	}
	// Inner string must be valid JSON object
	var obj map[string]any
	if err := json.Unmarshal([]byte(innerStr), &obj); err != nil {
		t.Fatalf("inner arguments is not valid JSON: %v (inner: %s)", err, innerStr)
	}
	if obj["city"] != "Paris" {
		t.Errorf("unexpected city value: %v", obj["city"])
	}
}
