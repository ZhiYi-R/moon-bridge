package anthropic_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"moonbridge/internal/protocol/anthropic"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestClientCreateMessageSendsHeadersAndParsesResponse(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v1/messages" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("x-api-key"); got != "upstream-key" {
			t.Fatalf("x-api-key = %q", got)
		}
		if got := r.Header.Get("anthropic-version"); got != "2023-06-01" {
			t.Fatalf("anthropic-version = %q", got)
		}
		if got := r.Header.Get("user-agent"); got != "Bun/1.3.13" {
			t.Fatalf("user-agent = %q", got)
		}

		var request anthropic.MessageRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("Decode request error = %v", err)
		}
		if request.Model != "claude-test" {
			t.Fatalf("request model = %q", request.Model)
		}

		var body strings.Builder
		_ = json.NewEncoder(&body).Encode(anthropic.MessageResponse{
			ID:         "msg_123",
			Type:       "message",
			Role:       "assistant",
			StopReason: "end_turn",
			Content:    []anthropic.ContentBlock{{Type: "text", Text: "Hello"}},
			Usage:      anthropic.Usage{InputTokens: 7, OutputTokens: 2},
		})
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body.String())),
			Header:     http.Header{},
		}, nil
	})}

	client := anthropic.NewClient(anthropic.ClientConfig{
		BaseURL:   "https://provider.example.test",
		APIKey:    "upstream-key",
		Version:   "2023-06-01",
		UserAgent: "Bun/1.3.13",
		Client:    httpClient,
	})

	response, err := client.CreateMessage(context.Background(), anthropic.MessageRequest{
		Model:     "claude-test",
		MaxTokens: 64,
		Messages:  []anthropic.Message{{Role: "user", Content: []anthropic.ContentBlock{{Type: "text", Text: "Hi"}}}},
	})
	if err != nil {
		t.Fatalf("CreateMessage() error = %v", err)
	}
	if response.Content[0].Text != "Hello" {
		t.Fatalf("response content = %+v", response.Content)
	}
}

func TestClientStreamMessageParsesSSEEvents(t *testing.T) {
	body := strings.Join([]string{
		"event: message_start",
		"data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\"}}",
		"",
		"event: message_stop",
		"data: {\"type\":\"message_stop\"}",
		"",
	}, "\n")
	httpClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		}, nil
	})}

	client := anthropic.NewClient(anthropic.ClientConfig{
		BaseURL: "https://provider.example.test",
		APIKey:  "upstream-key",
		Version: "2023-06-01",
		Client:  httpClient,
	})

	stream, err := client.StreamMessage(context.Background(), anthropic.MessageRequest{
		Model:     "claude-test",
		MaxTokens: 64,
		Stream:    true,
		Messages:  []anthropic.Message{{Role: "user", Content: []anthropic.ContentBlock{{Type: "text", Text: "Hi"}}}},
	})
	if err != nil {
		t.Fatalf("StreamMessage() error = %v", err)
	}
	defer stream.Close()

	first, err := stream.Next()
	if err != nil {
		t.Fatalf("Next() first error = %v", err)
	}
	if first.Type != "message_start" || first.Message.ID != "msg_1" {
		t.Fatalf("first event = %+v", first)
	}
	second, err := stream.Next()
	if err != nil {
		t.Fatalf("Next() second error = %v", err)
	}
	if second.Type != "message_stop" {
		t.Fatalf("second event = %+v", second)
	}
}

func TestClientCreateMessageNormalizesProviderError(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Body:       io.NopCloser(strings.NewReader(`{"error":{"type":"rate_limit_error","message":"slow down"}}`)),
			Header:     http.Header{"request-id": []string{"req_123"}},
		}, nil
	})}

	client := anthropic.NewClient(anthropic.ClientConfig{
		BaseURL: "https://provider.example.test",
		APIKey:  "upstream-key",
		Version: "2023-06-01",
		Client:  httpClient,
	})

	_, err := client.CreateMessage(context.Background(), anthropic.MessageRequest{
		Model:     "claude-test",
		MaxTokens: 64,
		Messages:  []anthropic.Message{{Role: "user", Content: []anthropic.ContentBlock{{Type: "text", Text: "Hi"}}}},
	})
	providerError, ok := anthropic.IsProviderError(err)
	if !ok {
		t.Fatalf("error = %T %v, want ProviderError", err, err)
	}
	if providerError.OpenAIStatus() != http.StatusTooManyRequests || providerError.OpenAICode() != "rate_limit_exceeded" {
		t.Fatalf("providerError = %+v", providerError)
	}
}

func TestProbeWebSearchSendsServerTool(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var request anthropic.MessageRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("Decode request error = %v", err)
		}
		if request.Model != "claude-test" {
			t.Fatalf("probe model = %q", request.Model)
		}
		if len(request.Tools) != 1 || request.Tools[0].Name != "web_search" || request.Tools[0].Type != "web_search_20250305" {
			t.Fatalf("probe tools = %+v", request.Tools)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(strings.NewReader(strings.Join([]string{
				"event: message_start",
				"data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_probe\",\"type\":\"message\",\"role\":\"assistant\"}}",
				"",
			}, "\n"))),
			Header: http.Header{"Content-Type": []string{"text/event-stream"}},
		}, nil
	})}

	client := anthropic.NewClient(anthropic.ClientConfig{
		BaseURL: "https://provider.example.test",
		APIKey:  "upstream-key",
		Version: "2023-06-01",
		Client:  httpClient,
	})

	supported, err := client.ProbeWebSearch(context.Background(), "claude-test")
	if err != nil {
		t.Fatalf("ProbeWebSearch() error = %v", err)
	}
	if !supported {
		t.Fatal("ProbeWebSearch() supported = false, want true")
	}
}

func TestProbeWebSearchDetectsUnsupportedProvider(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadRequest,
			Body:       io.NopCloser(strings.NewReader(`{"error":{"type":"invalid_request_error","message":"tool type web_search_20250305 is not supported"}}`)),
			Header:     http.Header{},
		}, nil
	})}

	client := anthropic.NewClient(anthropic.ClientConfig{
		BaseURL: "https://provider.example.test",
		APIKey:  "upstream-key",
		Version: "2023-06-01",
		Client:  httpClient,
	})

	supported, err := client.ProbeWebSearch(context.Background(), "claude-test")
	if err != nil {
		t.Fatalf("ProbeWebSearch() error = %v", err)
	}
	if supported {
		t.Fatal("ProbeWebSearch() supported = true, want false")
	}
}

func TestProbeWebSearchDetectsUnsupportedSSEErrorEvent(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(strings.NewReader(strings.Join([]string{
				"event: error",
				"data: {\"type\":\"error\",\"error\":{\"type\":\"invalid_request_error\",\"message\":\"web_search_20250305 is not supported\"}}",
				"",
			}, "\n"))),
			Header: http.Header{"Content-Type": []string{"text/event-stream"}},
		}, nil
	})}

	client := anthropic.NewClient(anthropic.ClientConfig{
		BaseURL: "https://provider.example.test",
		APIKey:  "upstream-key",
		Version: "2023-06-01",
		Client:  httpClient,
	})

	supported, err := client.ProbeWebSearch(context.Background(), "claude-test")
	if err != nil {
		t.Fatalf("ProbeWebSearch() error = %v", err)
	}
	if supported {
		t.Fatal("ProbeWebSearch() supported = true, want false")
	}
}

func TestProbeWebSearchDetectsProviderThatRequiresInputSchemaForEveryTool(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusUnprocessableEntity,
			Body:       io.NopCloser(strings.NewReader(`{"detail":[{"type":"missing","loc":["body","tools",0,"input_schema"],"msg":"Field required","input":{"name":"web_search","type":"web_search_20250305","max_uses":1}}]}`)),
			Header:     http.Header{},
		}, nil
	})}

	client := anthropic.NewClient(anthropic.ClientConfig{
		BaseURL: "https://provider.example.test",
		APIKey:  "upstream-key",
		Version: "2023-06-01",
		Client:  httpClient,
	})

	supported, err := client.ProbeWebSearch(context.Background(), "claude-test")
	if err != nil {
		t.Fatalf("ProbeWebSearch() error = %v", err)
	}
	if supported {
		t.Fatal("ProbeWebSearch() supported = true, want false")
	}
}
