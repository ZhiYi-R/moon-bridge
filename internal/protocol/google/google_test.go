// Package google_test provides unit tests for the Gemini protocol package.
//
// Covers types.go (DTO JSON round-trip), client.go (HTTP/mock server),
// and adapter.go (Core format conversion). External test package so we
// test the public API surface.
package google_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"moonbridge/internal/format"
	"moonbridge/internal/protocol/google"
)

// ============================================================================
// Helper factories
// ============================================================================

// newTestClient creates a Client wired to an httptest.Server for mock testing.
func newTestClient(t *testing.T, srv *httptest.Server) *google.Client {
	t.Helper()
	return google.NewClient(google.ClientConfig{
		BaseURL: srv.URL,
		APIKey:  "test-key",
		Client:  srv.Client(),
	})
}

// newTestAdapter creates a GeminiProviderAdapter with nil client and no hooks.
func newTestAdapter() *google.GeminiProviderAdapter {
	return google.NewGeminiProviderAdapter(0, nil, format.CorePluginHooks{}, nil, nil)
}

// ============================================================================
// Types: JSON round-trip
// ============================================================================

func TestTypes_GenerateContentRequest_JSON(t *testing.T) {
	// Build a complete GenerateContentRequest with all sub-types populated.
	topK := float64(40)
	temperature := float64(0.7)
	in := google.GenerateContentRequest{
		Contents: []google.Content{
			{
				Role: "user",
				Parts: []google.Part{
					{Text: "hello"},
				},
			},
		},
		SystemInstruction: &google.Content{
			Parts: []google.Part{
				{Text: "You are helpful."},
			},
		},
		SafetySettings: []google.SafetySetting{
			{Category: "harassment", Threshold: "block_only_high"},
		},
		GenerationConfig: &google.GenerationConfig{
			Temperature:      &temperature,
			TopK:             &topK,
			MaxOutputTokens:  8192,
			StopSequences:    []string{"\n\n"},
			ResponseMimeType: "text/plain",
			CandidateCount:   1,
		},
		Tools: []google.Tool{
			{
				FunctionDeclarations: []google.FunctionDeclaration{
					{
						Name:        "get_weather",
						Description: "Get weather",
						Parameters:  map[string]any{"type": "object"},
					},
				},
			},
		},
		ToolConfig: json.RawMessage(`{"function_calling_config":{"mode":"any"}}`),
	}

	data, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}

	if len(data) == 0 {
		t.Fatal("marshaled JSON is empty")
	}

	var out google.GenerateContentRequest
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}

	if len(out.Contents) != 1 {
		t.Fatalf("Contents: got %d, want 1", len(out.Contents))
	}
	if out.Contents[0].Role != "user" {
		t.Errorf("Content[0].Role = %q, want user", out.Contents[0].Role)
	}
	if out.Contents[0].Parts[0].Text != "hello" {
		t.Errorf("Content[0].Parts[0].Text = %q, want hello", out.Contents[0].Parts[0].Text)
	}

	if out.SystemInstruction == nil {
		t.Fatal("SystemInstruction is nil")
	}
	if out.SystemInstruction.Parts[0].Text != "You are helpful." {
		t.Errorf("SystemInstruction text = %q", out.SystemInstruction.Parts[0].Text)
	}

	if len(out.SafetySettings) != 1 {
		t.Fatalf("SafetySettings: got %d, want 1", len(out.SafetySettings))
	}
	if out.SafetySettings[0].Category != "harassment" {
		t.Errorf("SafetySettings[0].Category = %q", out.SafetySettings[0].Category)
	}

	if out.GenerationConfig == nil {
		t.Fatal("GenerationConfig is nil")
	}
	if out.GenerationConfig.Temperature == nil || *out.GenerationConfig.Temperature != 0.7 {
		t.Errorf("Temperature = %v, want 0.7", out.GenerationConfig.Temperature)
	}
	if out.GenerationConfig.MaxOutputTokens != 8192 {
		t.Errorf("MaxOutputTokens = %d, want 8192", out.GenerationConfig.MaxOutputTokens)
	}

	if len(out.Tools) != 1 {
		t.Fatalf("Tools: got %d, want 1", len(out.Tools))
	}
	if out.Tools[0].FunctionDeclarations[0].Name != "get_weather" {
		t.Errorf("Tool[0].Name = %q", out.Tools[0].FunctionDeclarations[0].Name)
	}

	if len(out.ToolConfig) == 0 {
		t.Error("ToolConfig is empty after round-trip")
	}
}

func TestTypes_GenerateContentResponse_JSON(t *testing.T) {
	in := google.GenerateContentResponse{
		Candidates: []google.Candidate{
			{
				Index:        0,
				Content:      google.Content{Role: "model", Parts: []google.Part{{Text: "Hello!"}}},
				FinishReason: "STOP",
				SafetyRatings: []google.SafetyRating{
					{Category: "harm_category_harassment", Probability: "NEGLIGIBLE"},
				},
			},
		},
		PromptFeedback: &google.PromptFeedback{
			BlockReason: "SAFETY",
		},
		UsageMetadata: &google.UsageMetadata{
			PromptTokenCount:     5,
			CandidatesTokenCount: 10,
			TotalTokenCount:      15,
		},
	}

	data, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}

	var out google.GenerateContentResponse
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}

	if len(out.Candidates) != 1 {
		t.Fatalf("Candidates: got %d, want 1", len(out.Candidates))
	}
	if out.Candidates[0].FinishReason != "STOP" {
		t.Errorf("FinishReason = %q, want STOP", out.Candidates[0].FinishReason)
	}
	if out.Candidates[0].Content.Parts[0].Text != "Hello!" {
		t.Errorf("Text = %q", out.Candidates[0].Content.Parts[0].Text)
	}
	if len(out.Candidates[0].SafetyRatings) != 1 {
		t.Errorf("SafetyRatings: got %d", len(out.Candidates[0].SafetyRatings))
	}

	if out.PromptFeedback == nil {
		t.Fatal("PromptFeedback is nil")
	}
	if out.PromptFeedback.BlockReason != "SAFETY" {
		t.Errorf("BlockReason = %q", out.PromptFeedback.BlockReason)
	}

	if out.UsageMetadata == nil {
		t.Fatal("UsageMetadata is nil")
	}
	if out.UsageMetadata.TotalTokenCount != 15 {
		t.Errorf("TotalTokenCount = %d, want 15", out.UsageMetadata.TotalTokenCount)
	}
}

func TestTypes_Part_Variants(t *testing.T) {
	tests := []struct {
		name string
		part google.Part
	}{
		{"text part", google.Part{Text: "hello"}},
		{"inline data part", google.Part{InlineData: &google.Blob{MimeType: "image/png", Data: "base64=="}}},
		{"file data part", google.Part{FileData: &google.FileData{MimeType: "image/png", FileURI: "gs://bucket/file"}}},
		{"function call part", google.Part{FunctionCall: &google.FunctionCall{Name: "get_weather", Args: json.RawMessage(`{"loc":"NYC"}`)}}},
		{"function response part", google.Part{FunctionResponse: &google.FunctionResponse{Name: "get_weather", Response: json.RawMessage(`{"temp":72}`)}}},
		{"empty part", google.Part{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.part)
			if err != nil {
				t.Fatal(err)
			}
			var out google.Part
			if err := json.Unmarshal(data, &out); err != nil {
				t.Fatal(err)
			}
			// Verify text
			if out.Text != tt.part.Text {
				t.Errorf("Text = %q, want %q", out.Text, tt.part.Text)
			}
			// Verify InlineData
			if (out.InlineData == nil) != (tt.part.InlineData == nil) {
				t.Errorf("InlineData nil mismatch: got %v, want %v", out.InlineData != nil, tt.part.InlineData != nil)
			} else if out.InlineData != nil {
				if out.InlineData.MimeType != tt.part.InlineData.MimeType || out.InlineData.Data != tt.part.InlineData.Data {
					t.Errorf("InlineData = %+v, want %+v", out.InlineData, tt.part.InlineData)
				}
			}
			// Verify FileData
			if (out.FileData == nil) != (tt.part.FileData == nil) {
				t.Errorf("FileData nil mismatch")
			} else if out.FileData != nil {
				if out.FileData.MimeType != tt.part.FileData.MimeType || out.FileData.FileURI != tt.part.FileData.FileURI {
					t.Errorf("FileData = %+v", out.FileData)
				}
			}
			// Verify FunctionCall
			if (out.FunctionCall == nil) != (tt.part.FunctionCall == nil) {
				t.Errorf("FunctionCall nil mismatch")
			} else if out.FunctionCall != nil {
				if out.FunctionCall.Name != tt.part.FunctionCall.Name {
					t.Errorf("FunctionCall.Name = %q", out.FunctionCall.Name)
				}
			}
			// Verify FunctionResponse
			if (out.FunctionResponse == nil) != (tt.part.FunctionResponse == nil) {
				t.Errorf("FunctionResponse nil mismatch")
			} else if out.FunctionResponse != nil {
				if out.FunctionResponse.Name != tt.part.FunctionResponse.Name {
					t.Errorf("FunctionResponse.Name = %q", out.FunctionResponse.Name)
				}
			}
		})
	}
}

func TestTypes_SafetySetting(t *testing.T) {
	in := google.SafetySetting{
		Category:  "harassment",
		Threshold: "block_only_high",
	}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out google.SafetySetting
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	if out.Category != "harassment" {
		t.Errorf("Category = %q", out.Category)
	}
	if out.Threshold != "block_only_high" {
		t.Errorf("Threshold = %q", out.Threshold)
	}
}

func TestTypes_UsageMetadata(t *testing.T) {
	in := google.UsageMetadata{
		PromptTokenCount:     10,
		CandidatesTokenCount: 20,
		TotalTokenCount:      30,
	}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out google.UsageMetadata
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	if out.PromptTokenCount != 10 {
		t.Errorf("PromptTokenCount = %d", out.PromptTokenCount)
	}
	if out.CandidatesTokenCount != 20 {
		t.Errorf("CandidatesTokenCount = %d", out.CandidatesTokenCount)
	}
	if out.TotalTokenCount != 30 {
		t.Errorf("TotalTokenCount = %d", out.TotalTokenCount)
	}
}

// ============================================================================
// Client: non-streaming (GenerateContent)
// ============================================================================

func TestGenerateContent_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if ct := r.Header.Get("content-type"); ct != "application/json" {
			t.Errorf("content-type = %q", ct)
		}
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"candidates":[{"index":0,"content":{"role":"model","parts":[{"text":"Hello!"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":10,"totalTokenCount":15}}`))
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	resp, err := client.GenerateContent(context.Background(), "gemini-2.0-flash", &google.GenerateContentRequest{
		Contents: []google.Content{{Role: "user", Parts: []google.Part{{Text: "hi"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Candidates) != 1 {
		t.Fatalf("Candidates: got %d, want 1", len(resp.Candidates))
	}
	if resp.Candidates[0].Content.Parts[0].Text != "Hello!" {
		t.Errorf("Text = %q, want Hello!", resp.Candidates[0].Content.Parts[0].Text)
	}
	if resp.Candidates[0].FinishReason != "STOP" {
		t.Errorf("FinishReason = %q, want STOP", resp.Candidates[0].FinishReason)
	}
	if resp.UsageMetadata == nil || resp.UsageMetadata.TotalTokenCount != 15 {
		t.Errorf("UsageMetadata = %+v", resp.UsageMetadata)
	}
}

func TestGenerateContent_EmptyCandidate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"candidates":[]}`))
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	resp, err := client.GenerateContent(context.Background(), "gemini-2.0-flash", &google.GenerateContentRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Candidates) != 0 {
		t.Errorf("Candidates = %v, want empty", resp.Candidates)
	}
}

func TestGenerateContent_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":{"code":400,"message":"invalid request"}}`))
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	_, err := client.GenerateContent(context.Background(), "gemini-2.0-flash", &google.GenerateContentRequest{})
	if err == nil {
		t.Fatal("expected error for 400 response")
	}
	if !strings.Contains(err.Error(), "400") && !strings.Contains(err.Error(), "invalid request") {
		t.Errorf("error = %v, want 400/invalid", err)
	}
}

func TestGenerateContent_HTTP5xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	_, err := client.GenerateContent(context.Background(), "gemini-2.0-flash", &google.GenerateContentRequest{})
	if err == nil {
		t.Fatal("expected error for 502 response")
	}
	if !strings.Contains(err.Error(), "502") {
		t.Errorf("error = %v, want 502", err)
	}
}

func TestGenerateContent_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{bad json`))
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	_, err := client.GenerateContent(context.Background(), "gemini-2.0-flash", &google.GenerateContentRequest{})
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestGenerateContent_ContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"candidates":[]}`))
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before the request

	_, err := client.GenerateContent(ctx, "gemini-2.0-flash", &google.GenerateContentRequest{})
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

// ============================================================================
// Client: URL construction
// ============================================================================

func TestClient_URL_GeminiAPI(t *testing.T) {
	var capturedURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedURL = r.URL.String()
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	client := google.NewClient(google.ClientConfig{
		BaseURL: srv.URL,
		APIKey:  "test-key",
		Version: "v1beta",
		Client:  srv.Client(),
	})

	_, _ = client.GenerateContent(context.Background(), "gemini-2.0-flash", &google.GenerateContentRequest{})

	if !strings.Contains(capturedURL, "/v1beta/models/gemini-2.0-flash:generateContent") {
		t.Errorf("URL = %s, missing expected path", capturedURL)
	}
	if !strings.Contains(capturedURL, "key=test-key") {
		t.Errorf("URL = %s, missing API key parameter", capturedURL)
	}
}

func TestClient_URL_VertexAI(t *testing.T) {
	var capturedURL string
	var authHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedURL = r.URL.String()
		authHeader = r.Header.Get("Authorization")
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	client := google.NewClient(google.ClientConfig{
		BaseURL:  srv.URL,
		APIKey:   "test-oauth-token",
		Project:  "my-project",
		Location: "europe-west4",
		Version:  "v1beta",
		Client:   srv.Client(),
	})

	_, _ = client.GenerateContent(context.Background(), "gemini-2.0-flash", &google.GenerateContentRequest{})

	if !strings.Contains(capturedURL, "/v1beta/projects/my-project/locations/europe-west4/publishers/google/models/gemini-2.0-flash:generateContent") {
		t.Errorf("URL = %s, missing expected Vertex AI path", capturedURL)
	}
	if authHeader != "Bearer test-oauth-token" {
		t.Errorf("Authorization = %q, want Bearer test-oauth-token", authHeader)
	}
	if strings.Contains(capturedURL, "key=") {
		t.Errorf("URL = %s, should not contain key query param for Vertex AI", capturedURL)
	}
}

// ============================================================================
// Client: streaming (StreamGenerateContent)
// ============================================================================

func TestStreamGenerateContent_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		w.Header().Set("content-type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("ResponseWriter does not implement http.Flusher")
		}

		// First chunk
		w.Write([]byte("data: {\"candidates\":[{\"index\":0,\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"Hel\"}]}}]}\n"))
		flusher.Flush()

		// Second chunk with finish_reason and usage
		w.Write([]byte("data: {\"candidates\":[{\"index\":0,\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"Hello world\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":5,\"candidatesTokenCount\":10,\"totalTokenCount\":15}}\n"))
		flusher.Flush()
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	ch, err := client.StreamGenerateContent(context.Background(), "gemini-2.0-flash", &google.GenerateContentRequest{
		Contents: []google.Content{{Role: "user", Parts: []google.Part{{Text: "hi"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}

	var events []google.GenerateContentResponse
	for e := range ch {
		events = append(events, e)
	}

	if len(events) != 2 {
		t.Fatalf("got %d events, want 2", len(events))
	}
	if len(events[0].Candidates) == 0 {
		t.Fatal("event[0] has no candidates")
	}
	if events[0].Candidates[0].Content.Parts[0].Text != "Hel" {
		t.Errorf("event[0] text = %q, want Hel", events[0].Candidates[0].Content.Parts[0].Text)
	}
	if events[1].Candidates[0].FinishReason != "STOP" {
		t.Errorf("event[1] FinishReason = %q, want STOP", events[1].Candidates[0].FinishReason)
	}
	if events[1].UsageMetadata == nil || events[1].UsageMetadata.TotalTokenCount != 15 {
		t.Errorf("event[1] UsageMetadata = %+v", events[1].UsageMetadata)
	}
}

func TestStreamGenerateContent_ContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("no flusher")
		}
		// Keep sending events until context is cancelled
		for i := 0; i < 100; i++ {
			w.Write([]byte("data: {\"candidates\":[{\"index\":0,\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"chunk\"}]}}]}\n"))
			flusher.Flush()
		}
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	ctx, cancel := context.WithCancel(context.Background())

	ch, err := client.StreamGenerateContent(ctx, "gemini-2.0-flash", &google.GenerateContentRequest{})
	if err != nil {
		t.Fatal(err)
	}

	// Cancel immediately — the readStream goroutine should exit
	cancel()

	// Channel must close (possibly with 0 or more events already buffered)
	for range ch {
		// drain
	}
}

func TestStreamGenerateContent_NonDataLine(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("no flusher")
		}
		// Send a non-data line (comment) that should be ignored
		w.Write([]byte(":comment\n"))
		flusher.Flush()
		// Then a real data line
		w.Write([]byte("data: {\"candidates\":[{\"index\":0,\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"real\"}]}}]}\n"))
		flusher.Flush()
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	ch, err := client.StreamGenerateContent(context.Background(), "gemini-2.0-flash", &google.GenerateContentRequest{})
	if err != nil {
		t.Fatal(err)
	}

	var events []google.GenerateContentResponse
	for e := range ch {
		events = append(events, e)
	}

	if len(events) != 1 {
		t.Fatalf("got %d events, want 1 (non-data line should be ignored)", len(events))
	}
	if events[0].Candidates[0].Content.Parts[0].Text != "real" {
		t.Errorf("text = %q, want real", events[0].Candidates[0].Content.Parts[0].Text)
	}
}

func TestStreamGenerateContent_EmptyDataLine(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("no flusher")
		}
		// Send an empty data line and a [DONE] marker — both should be ignored
		w.Write([]byte("data:\n"))
		flusher.Flush()
		w.Write([]byte("data: [DONE]\n"))
		flusher.Flush()
		// Then a real event
		w.Write([]byte("data: {\"candidates\":[{\"index\":0,\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"ok\"}]}}]}\n"))
		flusher.Flush()
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	ch, err := client.StreamGenerateContent(context.Background(), "gemini-2.0-flash", &google.GenerateContentRequest{})
	if err != nil {
		t.Fatal(err)
	}

	var events []google.GenerateContentResponse
	for e := range ch {
		events = append(events, e)
	}

	if len(events) != 1 {
		t.Fatalf("got %d events, want 1 (empty data lines should be ignored)", len(events))
	}
	if events[0].Candidates[0].Content.Parts[0].Text != "ok" {
		t.Errorf("text = %q, want ok", events[0].Candidates[0].Content.Parts[0].Text)
	}
}

// ============================================================================
// Adapter: FromCoreRequest — CoreRequest → *GenerateContentRequest
// ============================================================================

func TestFromCoreRequest_Nil(t *testing.T) {
	adapter := newTestAdapter()
	_, err := adapter.FromCoreRequest(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for nil request")
	}
}

func TestFromCoreRequest_Empty(t *testing.T) {
	adapter := newTestAdapter()
	result, err := adapter.FromCoreRequest(context.Background(), &format.CoreRequest{Model: "gemini-2.0-flash"})
	if err != nil {
		t.Fatal(err)
	}
	geminiReq, ok := result.(*google.GenerateContentRequest)
	if !ok {
		t.Fatal("expected *GenerateContentRequest")
	}
	if geminiReq.Contents == nil {
		t.Error("Contents is nil, want non-nil empty slice")
	}
	if len(geminiReq.Contents) != 0 {
		t.Errorf("Contents: got %d, want 0", len(geminiReq.Contents))
	}
	if geminiReq.SystemInstruction != nil {
		t.Error("SystemInstruction should be nil for empty request")
	}
}

func TestFromCoreRequest_BasicText(t *testing.T) {
	adapter := newTestAdapter()
	coreReq := &format.CoreRequest{
		Model: "gemini-2.0-flash",
		Messages: []format.CoreMessage{
			{Role: "user", Content: []format.CoreContentBlock{{Type: "text", Text: "hello"}}},
		},
	}

	result, err := adapter.FromCoreRequest(context.Background(), coreReq)
	if err != nil {
		t.Fatal(err)
	}
	geminiReq := result.(*google.GenerateContentRequest)

	if len(geminiReq.Contents) != 1 {
		t.Fatalf("Contents: got %d, want 1", len(geminiReq.Contents))
	}
	if geminiReq.Contents[0].Role != "user" {
		t.Errorf("Role = %q, want user", geminiReq.Contents[0].Role)
	}
	if geminiReq.Contents[0].Parts[0].Text != "hello" {
		t.Errorf("Text = %q, want hello", geminiReq.Contents[0].Parts[0].Text)
	}
}

func TestFromCoreRequest_MultiTurn(t *testing.T) {
	adapter := newTestAdapter()
	coreReq := &format.CoreRequest{
		Model: "test",
		Messages: []format.CoreMessage{
			{Role: "user", Content: []format.CoreContentBlock{{Type: "text", Text: "hi"}}},
			{Role: "assistant", Content: []format.CoreContentBlock{{Type: "text", Text: "hello"}}},
			{Role: "user", Content: []format.CoreContentBlock{{Type: "text", Text: "again"}}},
		},
	}

	result, err := adapter.FromCoreRequest(context.Background(), coreReq)
	if err != nil {
		t.Fatal(err)
	}
	geminiReq := result.(*google.GenerateContentRequest)

	if len(geminiReq.Contents) != 3 {
		t.Fatalf("Contents: got %d, want 3", len(geminiReq.Contents))
	}
	// user → user
	if geminiReq.Contents[0].Role != "user" {
		t.Errorf("Content[0].Role = %q, want user", geminiReq.Contents[0].Role)
	}
	// assistant → model
	if geminiReq.Contents[1].Role != "model" {
		t.Errorf("Content[1].Role = %q, want model", geminiReq.Contents[1].Role)
	}
	// user → user
	if geminiReq.Contents[2].Role != "user" {
		t.Errorf("Content[2].Role = %q, want user", geminiReq.Contents[2].Role)
	}
}

func TestFromCoreRequest_SystemInstruction(t *testing.T) {
	adapter := newTestAdapter()
	coreReq := &format.CoreRequest{
		Model:  "gemini-2.0-flash",
		System: []format.CoreContentBlock{{Type: "text", Text: "You are helpful."}},
		Messages: []format.CoreMessage{
			{Role: "user", Content: []format.CoreContentBlock{{Type: "text", Text: "hi"}}},
		},
	}

	result, err := adapter.FromCoreRequest(context.Background(), coreReq)
	if err != nil {
		t.Fatal(err)
	}
	geminiReq := result.(*google.GenerateContentRequest)

	if geminiReq.SystemInstruction == nil {
		t.Fatal("SystemInstruction is nil")
	}
	if len(geminiReq.SystemInstruction.Parts) != 1 {
		t.Fatalf("SystemInstruction.Parts: got %d, want 1", len(geminiReq.SystemInstruction.Parts))
	}
	if geminiReq.SystemInstruction.Parts[0].Text != "You are helpful." {
		t.Errorf("System text = %q", geminiReq.SystemInstruction.Parts[0].Text)
	}
}

func TestFromCoreRequest_ToolUseAndToolResult(t *testing.T) {
	adapter := newTestAdapter()
	coreReq := &format.CoreRequest{
		Model: "gemini-2.0-flash",
		Messages: []format.CoreMessage{
			{
				Role: "assistant",
				Content: []format.CoreContentBlock{
					{
						Type:      "tool_use",
						ToolUseID: "call_abc",
						ToolName:  "get_weather",
						ToolInput: json.RawMessage(`{"loc":"NYC"}`),
					},
				},
			},
			{
				Role: "user",
				Content: []format.CoreContentBlock{
					{
						Type:      "tool_result",
						ToolUseID: "call_abc",
						ToolResultContent: []format.CoreContentBlock{
							{Type: "text", Text: "Sunny"},
						},
					},
				},
			},
		},
	}

	result, err := adapter.FromCoreRequest(context.Background(), coreReq)
	if err != nil {
		t.Fatal(err)
	}
	geminiReq := result.(*google.GenerateContentRequest)

	if len(geminiReq.Contents) != 3 {
		t.Fatalf("Contents: got %d, want 3 (placeholder + assistant + user)", len(geminiReq.Contents))
	}

	// First Content is the inserted user placeholder.
	if geminiReq.Contents[0].Role != "user" || len(geminiReq.Contents[0].Parts) != 1 || geminiReq.Contents[0].Parts[0].Text != "_" {
		t.Fatalf("expected user placeholder as first Content, got role=%q parts=%v", geminiReq.Contents[0].Role, geminiReq.Contents[0].Parts)
	}

	// Assistant message: FunctionCall
	astParts := geminiReq.Contents[1].Parts
	if len(astParts) != 1 {
		t.Fatalf("assistant Parts: got %d, want 1", len(astParts))
	}
	if astParts[0].FunctionCall == nil {
		t.Fatal("assistant Parts[0].FunctionCall is nil")
	}
	if astParts[0].FunctionCall.Name != "get_weather" {
		t.Errorf("FunctionCall.Name = %q, want get_weather", astParts[0].FunctionCall.Name)
	}
	if string(astParts[0].FunctionCall.Args) != `{"loc":"NYC"}` {
		t.Errorf("FunctionCall.Args = %s", string(astParts[0].FunctionCall.Args))
	}

	// User message: FunctionResponse
	userParts := geminiReq.Contents[2].Parts
	if len(userParts) != 1 {
		t.Fatalf("user Parts: got %d, want 1", len(userParts))
	}
	if userParts[0].FunctionResponse == nil {
		t.Fatal("user Parts[0].FunctionResponse is nil")
	}
	if userParts[0].FunctionResponse.Name != "get_weather" {
		t.Errorf("FunctionResponse.Name = %q, want get_weather", userParts[0].FunctionResponse.Name)
	}
}

func TestFromCoreRequest_Tools(t *testing.T) {
	adapter := newTestAdapter()
	coreReq := &format.CoreRequest{
		Model: "test",
		Messages: []format.CoreMessage{
			{Role: "user", Content: []format.CoreContentBlock{{Type: "text", Text: "call a tool"}}},
		},
		Tools: []format.CoreTool{
			{Name: "get_weather", Description: "Get the weather", InputSchema: map[string]any{"type": "object"}},
		},
	}

	result, err := adapter.FromCoreRequest(context.Background(), coreReq)
	if err != nil {
		t.Fatal(err)
	}
	geminiReq := result.(*google.GenerateContentRequest)

	if len(geminiReq.Tools) != 1 {
		t.Fatalf("Tools: got %d, want 1", len(geminiReq.Tools))
	}
	if len(geminiReq.Tools[0].FunctionDeclarations) != 1 {
		t.Fatalf("FunctionDeclarations: got %d, want 1", len(geminiReq.Tools[0].FunctionDeclarations))
	}
	if geminiReq.Tools[0].FunctionDeclarations[0].Name != "get_weather" {
		t.Errorf("Name = %q", geminiReq.Tools[0].FunctionDeclarations[0].Name)
	}
}

func TestFromCoreRequest_SafetySettingsMap(t *testing.T) {
	adapter := newTestAdapter()
	coreReq := &format.CoreRequest{
		Model:          "test",
		SafetySettings: map[string]any{"harassment": "block_only_high", "hate_speech": "block_medium_and_above"},
		Messages: []format.CoreMessage{
			{Role: "user", Content: []format.CoreContentBlock{{Type: "text", Text: "hi"}}},
		},
	}

	result, err := adapter.FromCoreRequest(context.Background(), coreReq)
	if err != nil {
		t.Fatal(err)
	}
	geminiReq := result.(*google.GenerateContentRequest)

	if len(geminiReq.SafetySettings) != 2 {
		t.Fatalf("SafetySettings: got %d, want 2", len(geminiReq.SafetySettings))
	}
	// Map iteration order is non-deterministic, so check presence without ordering
	found := 0
	for _, ss := range geminiReq.SafetySettings {
		switch ss.Category {
		case "harassment":
			if ss.Threshold != "block_only_high" {
				t.Errorf("harassment threshold = %q", ss.Threshold)
			}
			found++
		case "hate_speech":
			if ss.Threshold != "block_medium_and_above" {
				t.Errorf("hate_speech threshold = %q", ss.Threshold)
			}
			found++
		}
	}
	if found != 2 {
		t.Errorf("found %d expected safety settings, want 2", found)
	}
}

func TestFromCoreRequest_GenerationConfigMap(t *testing.T) {
	adapter := newTestAdapter()
	coreReq := &format.CoreRequest{
		Model: "test",
		GenerationConfig: map[string]any{
			"temperature":      float64(0.7),
			"topP":             float64(0.9),
			"topK":             float64(40),
			"maxOutputTokens":  float64(8192),
			"stopSequences":    []any{"\n\n", "**"},
			"responseMimeType": "text/plain",
			"candidateCount":   float64(1),
		},
		Messages: []format.CoreMessage{
			{Role: "user", Content: []format.CoreContentBlock{{Type: "text", Text: "hi"}}},
		},
	}

	result, err := adapter.FromCoreRequest(context.Background(), coreReq)
	if err != nil {
		t.Fatal(err)
	}
	geminiReq := result.(*google.GenerateContentRequest)

	gc := geminiReq.GenerationConfig
	if gc == nil {
		t.Fatal("GenerationConfig is nil")
	}
	if gc.Temperature == nil || *gc.Temperature != 0.7 {
		t.Errorf("Temperature = %v", gc.Temperature)
	}
	if gc.TopP == nil || *gc.TopP != 0.9 {
		t.Errorf("TopP = %v", gc.TopP)
	}
	if gc.TopK == nil || *gc.TopK != 40 {
		t.Errorf("TopK = %v", gc.TopK)
	}
	if gc.MaxOutputTokens != 8192 {
		t.Errorf("MaxOutputTokens = %d, want 8192", gc.MaxOutputTokens)
	}
	if len(gc.StopSequences) != 2 || gc.StopSequences[0] != "\n\n" {
		t.Errorf("StopSequences = %v", gc.StopSequences)
	}
	if gc.ResponseMimeType != "text/plain" {
		t.Errorf("ResponseMimeType = %q", gc.ResponseMimeType)
	}
	if gc.CandidateCount != 1 {
		t.Errorf("CandidateCount = %d, want 1", gc.CandidateCount)
	}
}

func TestFromCoreRequest_GenerationConfigMap_NonStringThreshold(t *testing.T) {
	adapter := newTestAdapter()

	// Non-string threshold should be silently skipped.
	coreReq := &format.CoreRequest{
		Model:          "test",
		SafetySettings: map[string]any{"harassment": float64(3)},
		Messages: []format.CoreMessage{
			{Role: "user", Content: []format.CoreContentBlock{{Type: "text", Text: "hi"}}},
		},
	}

	result, err := adapter.FromCoreRequest(context.Background(), coreReq)
	if err != nil {
		t.Fatal(err)
	}
	geminiReq := result.(*google.GenerateContentRequest)

	// Non-string thresholds are skipped, so SafetySettings should be empty
	if len(geminiReq.SafetySettings) != 0 {
		t.Errorf("SafetySettings = %v, want empty (non-string threshold skipped)", geminiReq.SafetySettings)
	}
}

func TestFromCoreRequest_UnknownRole(t *testing.T) {
	adapter := newTestAdapter()

	// "system" is not "assistant" or "user" — mapRoleToGemini fallthrough should give "user"
	coreReq := &format.CoreRequest{
		Model: "test",
		Messages: []format.CoreMessage{
			{Role: "system", Content: []format.CoreContentBlock{{Type: "text", Text: "be helpful"}}},
		},
	}

	result, err := adapter.FromCoreRequest(context.Background(), coreReq)
	if err != nil {
		t.Fatal(err)
	}
	geminiReq := result.(*google.GenerateContentRequest)

	if len(geminiReq.Contents) != 1 {
		t.Fatalf("Contents: got %d, want 1", len(geminiReq.Contents))
	}
	// Fallthrough: unknown role → "user"
	if geminiReq.Contents[0].Role != "user" {
		t.Errorf("Role = %q, want user (fallthrough)", geminiReq.Contents[0].Role)
	}
}

func TestFromCoreRequest_ImageContent(t *testing.T) {
	adapter := newTestAdapter()
	coreReq := &format.CoreRequest{
		Model: "test",
		Messages: []format.CoreMessage{
			{
				Role: "user",
				Content: []format.CoreContentBlock{
					{Type: "text", Text: "what's this?"},
					{Type: "image", ImageData: "base64data", MediaType: "image/png"},
				},
			},
		},
	}

	result, err := adapter.FromCoreRequest(context.Background(), coreReq)
	if err != nil {
		t.Fatal(err)
	}
	geminiReq := result.(*google.GenerateContentRequest)

	if len(geminiReq.Contents[0].Parts) != 2 {
		t.Fatalf("Parts: got %d, want 2", len(geminiReq.Contents[0].Parts))
	}
	if geminiReq.Contents[0].Parts[1].InlineData == nil {
		t.Fatal("Parts[1].InlineData is nil")
	}
	if geminiReq.Contents[0].Parts[1].InlineData.MimeType != "image/png" {
		t.Errorf("MimeType = %q", geminiReq.Contents[0].Parts[1].InlineData.MimeType)
	}
	if geminiReq.Contents[0].Parts[1].InlineData.Data != "base64data" {
		t.Errorf("Data = %q", geminiReq.Contents[0].Parts[1].InlineData.Data)
	}
}

func TestFromCoreRequest_ReasoningBlock(t *testing.T) {
	adapter := newTestAdapter()

	// Reasoning blocks are converted to text parts (Gemini has no native reasoning type).
	coreReq := &format.CoreRequest{
		Model: "test",
		Messages: []format.CoreMessage{
			{
				Role: "assistant",
				Content: []format.CoreContentBlock{
					{
						Type:               "reasoning",
						ReasoningText:      "thinking step by step",
						ReasoningSignature: "sig123",
					},
					{Type: "text", Text: "final answer"},
				},
			},
			{Role: "user", Content: []format.CoreContentBlock{{Type: "text", Text: "ok"}}},
		},
	}

	result, err := adapter.FromCoreRequest(context.Background(), coreReq)
	if err != nil {
		t.Fatal(err)
	}
	geminiReq := result.(*google.GenerateContentRequest)

	// First Content is the inserted user placeholder.
	if len(geminiReq.Contents) != 3 {
		t.Fatalf("Contents: got %d, want 3 (placeholder + assistant + user)", len(geminiReq.Contents))
	}
	if geminiReq.Contents[0].Role != "user" || len(geminiReq.Contents[0].Parts) != 1 || geminiReq.Contents[0].Parts[0].Text != "_" {
		t.Fatalf("expected user placeholder as first Content, got role=%q", geminiReq.Contents[0].Role)
	}

	// Assistant message should have 2 parts (reasoning converted to text).
	if len(geminiReq.Contents[1].Parts) != 2 {
		t.Fatalf("assistant Parts: got %d, want 2 (reasoning block converted to text)", len(geminiReq.Contents[1].Parts))
	}
	if geminiReq.Contents[1].Parts[0].Text != "thinking step by step" {
		t.Errorf("assistant parts[0] reasoning text = %q, want thinking step by step", geminiReq.Contents[1].Parts[0].Text)
	}
	if geminiReq.Contents[1].Parts[1].Text != "final answer" {
		t.Errorf("assistant parts[1] text = %q, want final answer", geminiReq.Contents[1].Parts[1].Text)
	}
}

func TestFromCoreRequest_TemperatureAndTopP(t *testing.T) {
	adapter := newTestAdapter()
	temp := 0.7
	topP := 0.9
	coreReq := &format.CoreRequest{
		Model: "gemini-2.0-flash",
		Messages: []format.CoreMessage{
			{Role: "user", Content: []format.CoreContentBlock{{Type: "text", Text: "hello"}}},
		},
		Temperature: &temp,
		TopP:        &topP,
	}

	result, err := adapter.FromCoreRequest(context.Background(), coreReq)
	if err != nil {
		t.Fatal(err)
	}
	geminiReq := result.(*google.GenerateContentRequest)

	gc := geminiReq.GenerationConfig
	if gc == nil {
		t.Fatal("GenerationConfig is nil")
	}
	if gc.Temperature == nil || *gc.Temperature != 0.7 {
		t.Errorf("Temperature = %v", gc.Temperature)
	}
	if gc.TopP == nil || *gc.TopP != 0.9 {
		t.Errorf("TopP = %v", gc.TopP)
	}
}

func TestFromCoreRequest_DefaultParameters(t *testing.T) {
	adapter := newTestAdapter()
	coreReq := &format.CoreRequest{
		Model: "gemini-2.0-flash",
		Messages: []format.CoreMessage{
			{Role: "user", Content: []format.CoreContentBlock{{Type: "text", Text: "hello"}}},
		},
		MaxTokens:     4096,
		StopSequences: []string{"\n\n"},
	}

	result, err := adapter.FromCoreRequest(context.Background(), coreReq)
	if err != nil {
		t.Fatal(err)
	}
	geminiReq := result.(*google.GenerateContentRequest)

	gc := geminiReq.GenerationConfig
	if gc == nil {
		t.Fatal("GenerationConfig is nil")
	}
	if gc.MaxOutputTokens != 4096 {
		t.Errorf("MaxOutputTokens = %d, want 4096", gc.MaxOutputTokens)
	}
	if len(gc.StopSequences) != 1 || gc.StopSequences[0] != "\n\n" {
		t.Errorf("StopSequences = %v", gc.StopSequences)
	}
}

// ============================================================================
// Adapter: ToCoreResponse — *GenerateContentResponse → CoreResponse
// ============================================================================

func TestToCoreResponse_BasicText(t *testing.T) {
	adapter := newTestAdapter()
	geminiResp := &google.GenerateContentResponse{
		Candidates: []google.Candidate{{
			Index:        0,
			Content:      google.Content{Role: "model", Parts: []google.Part{{Text: "Hello!"}}},
			FinishReason: "STOP",
		}},
		UsageMetadata: &google.UsageMetadata{PromptTokenCount: 5, CandidatesTokenCount: 10, TotalTokenCount: 15},
	}

	result, err := adapter.ToCoreResponse(context.Background(), geminiResp)
	if err != nil {
		t.Fatal(err)
	}

	if result.Status != "completed" {
		t.Errorf("Status = %q, want completed", result.Status)
	}
	if len(result.Messages) != 1 {
		t.Fatalf("Messages: got %d, want 1", len(result.Messages))
	}
	if result.Messages[0].Role != "assistant" {
		t.Errorf("Role = %q, want assistant", result.Messages[0].Role)
	}
	if result.Messages[0].Content[0].Text != "Hello!" {
		t.Errorf("Text = %q, want Hello!", result.Messages[0].Content[0].Text)
	}
	if result.StopReason != "end_turn" {
		t.Errorf("StopReason = %q, want end_turn", result.StopReason)
	}
	if result.Usage.InputTokens != 5 {
		t.Errorf("InputTokens = %d, want 5", result.Usage.InputTokens)
	}
	if result.Usage.OutputTokens != 10 {
		t.Errorf("OutputTokens = %d, want 10", result.Usage.OutputTokens)
	}
	if result.Usage.TotalTokens != 15 {
		t.Errorf("TotalTokens = %d, want 15", result.Usage.TotalTokens)
	}
}

func TestToCoreResponse_EmptyCandidates(t *testing.T) {
	adapter := newTestAdapter()
	geminiResp := &google.GenerateContentResponse{
		Candidates: []google.Candidate{},
	}

	result, err := adapter.ToCoreResponse(context.Background(), geminiResp)
	if err != nil {
		t.Fatal(err)
	}

	if result.Status != "completed" {
		t.Errorf("Status = %q, want completed", result.Status)
	}
	if len(result.Messages[0].Content) != 0 {
		t.Errorf("Content = %v, want empty", result.Messages[0].Content)
	}
}

func TestToCoreResponse_NilUsage(t *testing.T) {
	adapter := newTestAdapter()
	geminiResp := &google.GenerateContentResponse{
		Candidates: []google.Candidate{{
			Index:        0,
			Content:      google.Content{Role: "model", Parts: []google.Part{{Text: "hi"}}},
			FinishReason: "STOP",
		}},
		UsageMetadata: nil,
	}

	result, err := adapter.ToCoreResponse(context.Background(), geminiResp)
	if err != nil {
		t.Fatal(err)
	}

	// Nil usage should not panic, usage should be zero-value
	if result.Usage.InputTokens != 0 || result.Usage.OutputTokens != 0 || result.Usage.TotalTokens != 0 {
		t.Errorf("Usage = %+v, want zero-value", result.Usage)
	}
}

func TestToCoreResponse_FinishReasonVariants(t *testing.T) {
	adapter := newTestAdapter()

	tests := []struct {
		geminiReason string
		wantStatus   string
		wantStop     string
	}{
		{"STOP", "completed", "end_turn"},
		{"MAX_TOKENS", "incomplete", "max_tokens"},
		{"SAFETY", "failed", "content_filter"},
		{"RECITATION", "failed", "content_filter"},
		{"OTHER", "failed", "error"},
		{"FINISH_REASON_UNSPECIFIED", "completed", ""},
		{"UNKNOWN_REASON", "completed", "UNKNOWN_REASON"},
	}

	for _, tt := range tests {
		t.Run(tt.geminiReason, func(t *testing.T) {
			geminiResp := &google.GenerateContentResponse{
				Candidates: []google.Candidate{{
					Index:        0,
					Content:      google.Content{Role: "model", Parts: []google.Part{{Text: "x"}}},
					FinishReason: tt.geminiReason,
				}},
			}
			result, err := adapter.ToCoreResponse(context.Background(), geminiResp)
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != tt.wantStatus {
				t.Errorf("Status = %q, want %q", result.Status, tt.wantStatus)
			}
			if result.StopReason != tt.wantStop {
				t.Errorf("StopReason = %q, want %q", result.StopReason, tt.wantStop)
			}
		})
	}
}

func TestToCoreResponse_ToolCallResponse(t *testing.T) {
	adapter := newTestAdapter()
	geminiResp := &google.GenerateContentResponse{
		Candidates: []google.Candidate{{
			Index: 0,
			Content: google.Content{Role: "model", Parts: []google.Part{
				{Text: "calling tool"},
				{FunctionCall: &google.FunctionCall{Name: "get_weather", Args: json.RawMessage(`{"loc":"NYC"}`)}},
			}},
			FinishReason: "STOP",
		}},
	}

	result, err := adapter.ToCoreResponse(context.Background(), geminiResp)
	if err != nil {
		t.Fatal(err)
	}

	blocks := result.Messages[0].Content
	if len(blocks) != 2 {
		t.Fatalf("Content blocks: got %d, want 2", len(blocks))
	}
	if blocks[0].Type != "text" || blocks[0].Text != "calling tool" {
		t.Errorf("block[0] = %+v", blocks[0])
	}
	if blocks[1].Type != "tool_use" {
		t.Errorf("block[1].Type = %q, want tool_use", blocks[1].Type)
	}
	if blocks[1].ToolName != "get_weather" {
		t.Errorf("ToolName = %q", blocks[1].ToolName)
	}
	if blocks[1].ToolUseID != "get_weather__call_1" {
		t.Errorf("ToolUseID = %q", blocks[1].ToolUseID)
	}
	if string(blocks[1].ToolInput) != `{"loc":"NYC"}` {
		t.Errorf("ToolInput = %s", string(blocks[1].ToolInput))
	}
}

func TestToCoreResponse_ToolCallResponse_DuplicateFunctionNameGetsUniqueIDs(t *testing.T) {
	adapter := newTestAdapter()
	geminiResp := &google.GenerateContentResponse{
		Candidates: []google.Candidate{{
			Index: 0,
			Content: google.Content{Role: "model", Parts: []google.Part{
				{FunctionCall: &google.FunctionCall{Name: "get_weather", Args: json.RawMessage(`{"loc":"NYC"}`)}},
				{FunctionCall: &google.FunctionCall{Name: "get_weather", Args: json.RawMessage(`{"loc":"Paris"}`)}},
			}},
			FinishReason: "STOP",
		}},
	}
	result, err := adapter.ToCoreResponse(context.Background(), geminiResp)
	if err != nil {
		t.Fatal(err)
	}
	blocks := result.Messages[0].Content
	if len(blocks) != 2 {
		t.Fatalf("Content blocks: got %d, want 2", len(blocks))
	}
	if blocks[0].ToolUseID == blocks[1].ToolUseID {
		t.Fatalf("duplicate ToolUseID: %q", blocks[0].ToolUseID)
	}
	if blocks[0].ToolUseID != "get_weather__call_1" {
		t.Errorf("first ToolUseID = %q, want get_weather__call_1", blocks[0].ToolUseID)
	}
	if blocks[1].ToolUseID != "get_weather__call_2" {
		t.Errorf("second ToolUseID = %q, want get_weather__call_2", blocks[1].ToolUseID)
	}
}

func TestToCoreResponse_WrongType(t *testing.T) {
	adapter := newTestAdapter()

	_, err := adapter.ToCoreResponse(context.Background(), "not a response")
	if err == nil {
		t.Fatal("expected error for wrong type")
	}
}

// ============================================================================
// Adapter: ToCoreStream — <-chan GenerateContentResponse → <-chan CoreStreamEvent
// ============================================================================

func TestToCoreStream_SingleCandidate(t *testing.T) {
	adapter := newTestAdapter()
	src := make(chan google.GenerateContentResponse, 2)
	src <- google.GenerateContentResponse{
		Candidates: []google.Candidate{{Index: 0, Content: google.Content{Parts: []google.Part{{Text: "Hel"}}}}},
	}
	src <- google.GenerateContentResponse{
		Candidates:    []google.Candidate{{Index: 0, Content: google.Content{Parts: []google.Part{{Text: "Hello world"}}}, FinishReason: "STOP"}},
		UsageMetadata: &google.UsageMetadata{PromptTokenCount: 5, CandidatesTokenCount: 10, TotalTokenCount: 15},
	}
	close(src)

	events, err := adapter.ToCoreStream(context.Background(), (<-chan google.GenerateContentResponse)(src))
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

	// Check event sequence
	n := 0
	// Event 0: content_block.started
	if evts[n].Type != format.CoreContentBlockStarted {
		t.Errorf("event[%d].Type = %q, want %q", n, evts[n].Type, format.CoreContentBlockStarted)
	}
	if evts[n].ContentBlock == nil || evts[n].ContentBlock.Type != "text" {
		t.Errorf("event[%d].ContentBlock = %+v", n, evts[n].ContentBlock)
	}
	if evts[n].ChoiceIndex == nil || *evts[n].ChoiceIndex != 0 {
		t.Errorf("event[%d].ChoiceIndex = %v", n, evts[n].ChoiceIndex)
	}
	n++

	// Event 1+: text.delta / content_block.done / completed
	var sawDelta, sawDone, sawCompleted bool
	for ; n < len(evts); n++ {
		switch evts[n].Type {
		case format.CoreTextDelta:
			sawDelta = true
		case format.CoreContentBlockDone:
			sawDone = true
			if evts[n].StopReason != "end_turn" {
				t.Errorf("StopReason = %q, want end_turn", evts[n].StopReason)
			}
		case format.CoreEventCompleted:
			sawCompleted = true
			if evts[n].Usage == nil || evts[n].Usage.TotalTokens != 15 {
				t.Errorf("Usage = %+v", evts[n].Usage)
			}
		}
	}

	if !sawDelta {
		t.Error("missing text.delta event")
	}
	if !sawDone {
		t.Error("missing content_block.done event")
	}
	if !sawCompleted {
		t.Error("missing completed event")
	}
}

func TestToCoreStream_MultiCandidate(t *testing.T) {
	adapter := newTestAdapter()
	src := make(chan google.GenerateContentResponse, 1)
	src <- google.GenerateContentResponse{
		Candidates: []google.Candidate{
			{Index: 0, Content: google.Content{Parts: []google.Part{{Text: "A"}}}},
			{Index: 1, Content: google.Content{Parts: []google.Part{{Text: "B"}}}},
		},
	}
	close(src)

	events, err := adapter.ToCoreStream(context.Background(), (<-chan google.GenerateContentResponse)(src))
	if err != nil {
		t.Fatal(err)
	}

	var evts []format.CoreStreamEvent
	for e := range events.Events {
		evts = append(evts, e)
	}

	// We should see events for both candidates, then a completed event
	var candidateIndexes []int
	for _, e := range evts {
		if e.ChoiceIndex != nil {
			candidateIndexes = append(candidateIndexes, *e.ChoiceIndex)
		}
	}

	if len(candidateIndexes) < 2 {
		t.Fatalf("expected events from 2 candidates, got %d candidate events", len(candidateIndexes))
	}

	// Both candidates should appear
	seen := map[int]bool{}
	for _, ci := range candidateIndexes {
		seen[ci] = true
	}
	if !seen[0] {
		t.Error("missing candidate 0 events")
	}
	if !seen[1] {
		t.Error("missing candidate 1 events")
	}
}

func TestToCoreStream_ContextCancel(t *testing.T) {
	adapter := newTestAdapter()
	src := make(chan google.GenerateContentResponse, 1)
	src <- google.GenerateContentResponse{
		Candidates: []google.Candidate{{Index: 0, Content: google.Content{Parts: []google.Part{{Text: "hello"}}}}},
	}

	ctx, cancel := context.WithCancel(context.Background())
	events, err := adapter.ToCoreStream(ctx, (<-chan google.GenerateContentResponse)(src))
	if err != nil {
		t.Fatal(err)
	}

	// Cancel before fully consuming
	cancel()

	// Channel should close cleanly
	for range events.Events {
	}
}

func TestToCoreStream_EmptyChannel(t *testing.T) {
	adapter := newTestAdapter()
	src := make(chan google.GenerateContentResponse)
	close(src)

	events, err := adapter.ToCoreStream(context.Background(), (<-chan google.GenerateContentResponse)(src))
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
		t.Errorf("Type = %q, want %q", evts[0].Type, format.CoreEventCompleted)
	}
}

func TestToCoreStream_WrongType(t *testing.T) {
	adapter := newTestAdapter()

	_, err := adapter.ToCoreStream(context.Background(), "not a channel")
	if err == nil {
		t.Fatal("expected error for wrong type")
	}
}

// ============================================================================
// Plugin hooks
// ============================================================================

func TestFromCoreRequest_PluginHooksCalled(t *testing.T) {
	called := false
	hooks := format.CorePluginHooks{
		MutateCoreRequest: func(_ context.Context, req *format.CoreRequest) {
			called = true
			req.Model = "mutated-model"
		},
	}
	adapter := google.NewGeminiProviderAdapter(0, nil, hooks, nil, nil)

	coreReq := &format.CoreRequest{
		Model: "gemini-2.0-flash",
		Messages: []format.CoreMessage{
			{Role: "user", Content: []format.CoreContentBlock{{Type: "text", Text: "hi"}}},
		},
	}

	result, err := adapter.FromCoreRequest(context.Background(), coreReq)
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Error("MutateCoreRequest was not called")
	}
	geminiReq := result.(*google.GenerateContentRequest)
	if geminiReq.Contents[0].Parts[0].Text != "hi" {
		t.Errorf("Text = %q", geminiReq.Contents[0].Parts[0].Text)
	}
}

// ============================================================================
// Additional coverage edge cases
// ============================================================================

func TestProviderProtocol(t *testing.T) {
	adapter := newTestAdapter()
	if got := adapter.ProviderProtocol(); got != "google-genai" {
		t.Errorf("ProviderProtocol = %q, want google-genai", got)
	}
}

func TestFromCoreRequest_DefaultRoleFallthrough(t *testing.T) {
	// Unknown role ("tool") should hit mapRoleToGemini's default branch.
	adapter := newTestAdapter()
	coreReq := &format.CoreRequest{
		Model: "test",
		Messages: []format.CoreMessage{
			{Role: "tool", Content: []format.CoreContentBlock{{Type: "text", Text: "result"}}},
		},
	}
	result, err := adapter.FromCoreRequest(context.Background(), coreReq)
	if err != nil {
		t.Fatal(err)
	}
	geminiReq := result.(*google.GenerateContentRequest)
	if geminiReq.Contents[0].Role != "user" {
		t.Errorf("Role = %q, want user (default fallthrough)", geminiReq.Contents[0].Role)
	}
}

func TestFromCoreRequest_UnknownContentBlock(t *testing.T) {
	// blocksToContent default: unknown type with text should create a text Part.
	adapter := newTestAdapter()
	coreReq := &format.CoreRequest{
		Model: "test",
		Messages: []format.CoreMessage{
			{
				Role: "user",
				Content: []format.CoreContentBlock{
					{Type: "unknown_custom_type", Text: "fallback_text"},
				},
			},
		},
	}
	result, err := adapter.FromCoreRequest(context.Background(), coreReq)
	if err != nil {
		t.Fatal(err)
	}
	geminiReq := result.(*google.GenerateContentRequest)
	if len(geminiReq.Contents[0].Parts) != 1 {
		t.Fatalf("Parts: got %d, want 1 (default fallback)", len(geminiReq.Contents[0].Parts))
	}
	if geminiReq.Contents[0].Parts[0].Text != "fallback_text" {
		t.Errorf("Text = %q, want fallback_text", geminiReq.Contents[0].Parts[0].Text)
	}
}

func TestFromCoreRequest_EmptyToolResult(t *testing.T) {
	// blocksToContent: tool_result with empty ToolResultContent.
	adapter := newTestAdapter()
	coreReq := &format.CoreRequest{
		Model: "test",
		Messages: []format.CoreMessage{
			{
				Role: "user",
				Content: []format.CoreContentBlock{
					{
						Type:              "tool_result",
						ToolUseID:         "call_xyz",
						ToolResultContent: []format.CoreContentBlock{},
					},
				},
			},
		},
	}
	result, err := adapter.FromCoreRequest(context.Background(), coreReq)
	if err != nil {
		t.Fatal(err)
	}
	geminiReq := result.(*google.GenerateContentRequest)
	if len(geminiReq.Contents[0].Parts) != 1 {
		t.Fatalf("Parts: got %d, want 1", len(geminiReq.Contents[0].Parts))
	}
	if geminiReq.Contents[0].Parts[0].FunctionResponse == nil {
		t.Fatal("Parts[0].FunctionResponse is nil")
	}
	if geminiReq.Contents[0].Parts[0].FunctionResponse.Name != "call_xyz" {
		t.Errorf("FunctionResponse.Name = %q, want call_xyz", geminiReq.Contents[0].Parts[0].FunctionResponse.Name)
	}
	// Response should contain empty response JSON: {"response":""}
	if !strings.Contains(string(geminiReq.Contents[0].Parts[0].FunctionResponse.Response), `"response"`) {
		t.Errorf("FunctionResponse.Response = %s", string(geminiReq.Contents[0].Parts[0].FunctionResponse.Response))
	}
}

func TestFromCoreRequest_DefaultMaxTokens(t *testing.T) {
	// toGenerationConfig: when MaxTokens is 0 but config.DefaultMaxTokens > 0.
	adapter := google.NewGeminiProviderAdapter(
		8192,
		nil,
		format.CorePluginHooks{},
		nil,
		nil,
	)
	coreReq := &format.CoreRequest{
		Model: "test",
		Messages: []format.CoreMessage{
			{Role: "user", Content: []format.CoreContentBlock{{Type: "text", Text: "hi"}}},
		},
		// MaxTokens is 0, so DefaultMaxTokens should be used.
	}
	result, err := adapter.FromCoreRequest(context.Background(), coreReq)
	if err != nil {
		t.Fatal(err)
	}
	geminiReq := result.(*google.GenerateContentRequest)
	if geminiReq.GenerationConfig == nil {
		t.Fatal("GenerationConfig is nil")
	}
	if geminiReq.GenerationConfig.MaxOutputTokens != 8192 {
		t.Errorf("MaxOutputTokens = %d, want 8192 (from DefaultMaxTokens)", geminiReq.GenerationConfig.MaxOutputTokens)
	}
}

func TestFromCoreRequest_GenerationConfigMap_TypeVariants(t *testing.T) {
	// Cover toFloat64 with int, int64, json.Number, and default branches.
	// Cover toInt with int, int64, json.Number, and default branches.
	// Cover toStringSlice with []string branch.
	adapter := newTestAdapter()
	coreReq := &format.CoreRequest{
		Model: "test",
		GenerationConfig: map[string]any{
			"temperature":           int(8) / 10,         // int → toFloat64
			"topP":                  json.Number("0.85"), // json.Number → toFloat64
			"maxOutputTokens":       int(4096),           // json.Number → toInt
			"candidateCount":        json.Number("2"),
			"candidate_count_int64": int64(1),        // int64 → toInt
			"stopSequences":         []string{"END"}, // []string → toStringSlice
			"responseMimeType":      "application/json",
		},
		Messages: []format.CoreMessage{
			{Role: "user", Content: []format.CoreContentBlock{{Type: "text", Text: "hi"}}},
		},
	}
	result, err := adapter.FromCoreRequest(context.Background(), coreReq)
	if err != nil {
		t.Fatal(err)
	}
	geminiReq := result.(*google.GenerateContentRequest)
	gc := geminiReq.GenerationConfig
	if gc == nil {
		t.Fatal("GenerationConfig is nil")
	}
	// temperature was int(0) → 0.0
	if gc.Temperature == nil || *gc.Temperature != 0.0 {
		t.Errorf("Temperature = %v", gc.Temperature)
	}
	// top_p from json.Number
	if gc.TopP == nil || *gc.TopP != 0.85 {
		t.Errorf("TopP = %v", gc.TopP)
	}
	// max_output_tokens from json.Number
	if gc.MaxOutputTokens != 4096 {
		t.Errorf("MaxOutputTokens = %d, want 4096", gc.MaxOutputTokens)
	}
	// candidate_count from int64
	if gc.CandidateCount != 2 {
		t.Errorf("CandidateCount = %d, want 2", gc.CandidateCount)
	}
	// stop_sequences from []string
	if len(gc.StopSequences) != 1 || gc.StopSequences[0] != "END" {
		t.Errorf("StopSequences = %v", gc.StopSequences)
	}
}

func TestFromCoreRequest_GenerationConfigMap_DefaultBranch(t *testing.T) {
	// Cover toFloat64/toInt default branches (unrecognized types → false).
	adapter := newTestAdapter()
	coreReq := &format.CoreRequest{
		Model: "test",
		GenerationConfig: map[string]any{
			"temperature":     "invalid", // string → toFloat64 default → false → skipped
			"maxOutputTokens": "invalid", // string → toInt default → false → skipped
			"unknown_key":     "should be ignored",
		},
		Messages: []format.CoreMessage{
			{Role: "user", Content: []format.CoreContentBlock{{Type: "text", Text: "hi"}}},
		},
	}
	result, err := adapter.FromCoreRequest(context.Background(), coreReq)
	if err != nil {
		t.Fatal(err)
	}
	geminiReq := result.(*google.GenerateContentRequest)
	gc := geminiReq.GenerationConfig
	if gc == nil {
		t.Fatal("GenerationConfig is nil")
	}
	// Temperature unchanged (nil since not set)
	if gc.Temperature != nil {
		t.Errorf("Temperature = %v, want nil (invalid type should be skipped)", gc.Temperature)
	}
	// MaxOutputTokens unchanged (0 since not set)
	if gc.MaxOutputTokens != 0 {
		t.Errorf("MaxOutputTokens = %d, want 0", gc.MaxOutputTokens)
	}
}

func TestFromCoreRequest_DefaultContentBlockNoText(t *testing.T) {
	// blocksToContent default: unknown type with no text should be skipped.
	adapter := newTestAdapter()
	coreReq := &format.CoreRequest{
		Model: "test",
		Messages: []format.CoreMessage{
			{
				Role: "user",
				Content: []format.CoreContentBlock{
					{Type: "unknown_no_text"}, // no text, no other fields
				},
			},
		},
	}
	result, err := adapter.FromCoreRequest(context.Background(), coreReq)
	if err != nil {
		t.Fatal(err)
	}
	geminiReq := result.(*google.GenerateContentRequest)
	// Unknown type with no text produces no Part (default falls through without appending)
	if len(geminiReq.Contents) == 0 {
		t.Logf("Contents empty (all blocks filtered)")
	} else if len(geminiReq.Contents[0].Parts) != 0 {
		t.Errorf("Parts: got %d, want 0 (unknown type should be skipped)", len(geminiReq.Contents[0].Parts))
	}
}

func TestToCoreResponse_FromPartFunctionResponse(t *testing.T) {
	// fromPart: FunctionResponse branch.
	adapter := newTestAdapter()
	geminiResp := &google.GenerateContentResponse{
		Candidates: []google.Candidate{{
			Index: 0,
			Content: google.Content{Role: "model", Parts: []google.Part{
				{FunctionResponse: &google.FunctionResponse{Name: "get_weather", Response: json.RawMessage(`{"temp":72}`)}},
			}},
			FinishReason: "STOP",
		}},
	}
	result, err := adapter.ToCoreResponse(context.Background(), geminiResp)
	if err != nil {
		t.Fatal(err)
	}
	blocks := result.Messages[0].Content
	if len(blocks) != 1 {
		t.Fatalf("Content: got %d, want 1", len(blocks))
	}
	if blocks[0].Type != "tool_result" {
		t.Errorf("Type = %q, want tool_result", blocks[0].Type)
	}
	if blocks[0].ToolUseID != "get_weather__call_1" {
		t.Errorf("ToolUseID = %q", blocks[0].ToolUseID)
	}
}

func TestToCoreResponse_FromPartInlineData(t *testing.T) {
	// fromPart: InlineData branch.
	adapter := newTestAdapter()
	geminiResp := &google.GenerateContentResponse{
		Candidates: []google.Candidate{{
			Index: 0,
			Content: google.Content{Role: "model", Parts: []google.Part{
				{InlineData: &google.Blob{MimeType: "image/png", Data: "base64data"}},
			}},
			FinishReason: "STOP",
		}},
	}
	result, err := adapter.ToCoreResponse(context.Background(), geminiResp)
	if err != nil {
		t.Fatal(err)
	}
	blocks := result.Messages[0].Content
	if len(blocks) != 1 {
		t.Fatalf("Content: got %d, want 1", len(blocks))
	}
	if blocks[0].Type != "image" {
		t.Errorf("Type = %q, want image", blocks[0].Type)
	}
	if blocks[0].ImageData != "base64data" {
		t.Errorf("ImageData = %q", blocks[0].ImageData)
	}
	if blocks[0].MediaType != "image/png" {
		t.Errorf("MediaType = %q", blocks[0].MediaType)
	}
}

func TestToCoreResponse_FromPartEmpty(t *testing.T) {
	// fromPart default branch: empty Part → text block.
	adapter := newTestAdapter()
	geminiResp := &google.GenerateContentResponse{
		Candidates: []google.Candidate{{
			Index: 0,
			Content: google.Content{Role: "model", Parts: []google.Part{
				{}, // empty part — default case
			}},
			FinishReason: "STOP",
		}},
	}
	result, err := adapter.ToCoreResponse(context.Background(), geminiResp)
	if err != nil {
		t.Fatal(err)
	}
	blocks := result.Messages[0].Content
	if len(blocks) != 1 {
		t.Fatalf("Content: got %d, want 1", len(blocks))
	}
	if blocks[0].Type != "text" {
		t.Errorf("Type = %q, want text (empty Part default)", blocks[0].Type)
	}
}

func TestToCoreStream_ComputeDeltaNoChange(t *testing.T) {
	// computeDelta: when current text is shorter than or equal to previous text.
	adapter := newTestAdapter()
	src := make(chan google.GenerateContentResponse, 2)
	// First chunk establishes prevText = "Hello"
	src <- google.GenerateContentResponse{
		Candidates: []google.Candidate{{Index: 0, Content: google.Content{Parts: []google.Part{{Text: "Hello"}}}}},
	}
	// Second chunk has same text — len(current) <= len(prev), so delta should be "Hello" (current)
	src <- google.GenerateContentResponse{
		Candidates: []google.Candidate{{Index: 0, Content: google.Content{Parts: []google.Part{{Text: "Hello"}}}, FinishReason: "STOP"}},
	}
	close(src)

	events, err := adapter.ToCoreStream(context.Background(), (<-chan google.GenerateContentResponse)(src))
	if err != nil {
		t.Fatal(err)
	}

	var evts []format.CoreStreamEvent
	for e := range events.Events {
		evts = append(evts, e)
	}

	// Should have started + delta + done + completed
	// The delta should contain "Hello" (since current is not longer than prev, current is returned)
	var deltaCount int
	for _, e := range evts {
		if e.Type == format.CoreTextDelta {
			deltaCount++
		}
	}
	if deltaCount != 2 {
		t.Errorf("text.delta events: got %d, want 2", deltaCount)
	}
}

func TestStreamGenerateContent_StreamHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"bad request"}`))
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	_, err := client.StreamGenerateContent(context.Background(), "gemini-2.0-flash", &google.GenerateContentRequest{})
	if err == nil {
		t.Fatal("expected error for streaming 400 response")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("error = %v, want 400", err)
	}
}

func TestStreamGenerateContent_ParseError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("no flusher")
		}
		// Send a parseable line followed by unparseable data
		w.Write([]byte("data: {\"candidates\":[{\"index\":0,\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"valid\"}]}}]}\n"))
		flusher.Flush()
		// Unparseable data line — readStream will log a warning and continue
		w.Write([]byte("data: {{{{not json\n"))
		flusher.Flush()
		// Another valid line to confirm we continue after parse error
		w.Write([]byte("data: {\"candidates\":[{\"index\":0,\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"after error\"}]}}]}\n"))
		flusher.Flush()
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	ch, err := client.StreamGenerateContent(context.Background(), "gemini-2.0-flash", &google.GenerateContentRequest{})
	if err != nil {
		t.Fatal(err)
	}

	var events []google.GenerateContentResponse
	for e := range ch {
		events = append(events, e)
	}

	if len(events) != 2 {
		t.Fatalf("got %d events, want 2 (parse error line should be skipped)", len(events))
	}
	if events[0].Candidates[0].Content.Parts[0].Text != "valid" {
		t.Errorf("event[0].text = %q", events[0].Candidates[0].Content.Parts[0].Text)
	}
	if events[1].Candidates[0].Content.Parts[0].Text != "after error" {
		t.Errorf("event[1].text = %q", events[1].Candidates[0].Content.Parts[0].Text)
	}
}

func TestClient_NewClientDefaults(t *testing.T) {
	// This test verifies the NewClient defaults by observing request paths.
	var capturedURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedURL = r.URL.String()
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	// Create a client with minimal config: no Version, no Location, nil Client.
	client := google.NewClient(google.ClientConfig{
		BaseURL: srv.URL,
		APIKey:  "test-key",
		// Version and Location omitted — should use defaults "v1" and "us-central1"
		// Client omitted — should use http.DefaultClient
	})

	_, _ = client.GenerateContent(context.Background(), "gemini-2.0-flash", &google.GenerateContentRequest{})

	if !strings.Contains(capturedURL, "/v1/models/gemini-2.0-flash:generateContent") {
		t.Errorf("URL = %s, missing default version in path", capturedURL)
	}
}

func TestClient_NewRequestUserAgent(t *testing.T) {
	var userAgent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userAgent = r.Header.Get("User-Agent")
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	client := google.NewClient(google.ClientConfig{
		BaseURL:   srv.URL,
		APIKey:    "test-key",
		UserAgent: "MoonBridge-Test/1.0",
		Version:   "v1beta",
		Client:    srv.Client(),
	})

	_, _ = client.GenerateContent(context.Background(), "gemini-2.0-flash", &google.GenerateContentRequest{})

	if userAgent != "MoonBridge-Test/1.0" {
		t.Errorf("User-Agent = %q, want MoonBridge-Test/1.0", userAgent)
	}
}

func TestClient_Close(t *testing.T) {
	client := google.NewClient(google.ClientConfig{
		BaseURL: "http://localhost:1",
		APIKey:  "test-key",
	})
	// Close is a no-op — just verify it doesn't panic.
	if err := client.Close(); err != nil {
		t.Errorf("Close() = %v, want nil", err)
	}
}

// ============================================================================
// Final coverage edge cases for ≥95%
// ============================================================================

func TestFromCoreRequest_GenerationConfigMap_Int64AndDefault(t *testing.T) {
	// Cover toFloat64 int64 branch and toStringSlice default branch.
	adapter := newTestAdapter()
	coreReq := &format.CoreRequest{
		Model: "test",
		GenerationConfig: map[string]any{
			"topK":          int64(30), // int64 → toFloat64
			"stopSequences": 42,        // int (not []any or []string) → toStringSlice default
		},
		Messages: []format.CoreMessage{
			{Role: "user", Content: []format.CoreContentBlock{{Type: "text", Text: "hi"}}},
		},
	}
	result, err := adapter.FromCoreRequest(context.Background(), coreReq)
	if err != nil {
		t.Fatal(err)
	}
	geminiReq := result.(*google.GenerateContentRequest)
	gc := geminiReq.GenerationConfig
	if gc == nil {
		t.Fatal("GenerationConfig is nil")
	}
	// top_k from int64
	if gc.TopK == nil || *gc.TopK != 30 {
		t.Errorf("TopK = %v, want 30", gc.TopK)
	}
	// stop_sequences from int 42 → toStringSlice default → not set
	if len(gc.StopSequences) != 0 {
		t.Errorf("StopSequences = %v, want empty (int value should be skipped)", gc.StopSequences)
	}
}

func TestClient_NewClientNilClient(t *testing.T) {
	// Cover NewClient branch where cfg.Client is nil → http.DefaultClient.
	client := google.NewClient(google.ClientConfig{
		APIKey: "test-key",
	})
	if client == nil {
		t.Fatal("client is nil")
	}
	// We can't easily test the http client, but we can verify Close() works.
	if err := client.Close(); err != nil {
		t.Errorf("Close() = %v", err)
	}
}
