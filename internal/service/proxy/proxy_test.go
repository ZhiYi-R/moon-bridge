package proxy_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"moonbridge/internal/service/proxy"
	mbtrace "moonbridge/internal/service/trace"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

type canceledResponseWriter struct {
	header http.Header
	code   int
}

func (writer *canceledResponseWriter) Header() http.Header {
	if writer.header == nil {
		writer.header = make(http.Header)
	}
	return writer.header
}

func (writer *canceledResponseWriter) Write([]byte) (int, error) {
	return 0, context.Canceled
}

func (writer *canceledResponseWriter) WriteHeader(statusCode int) {
	writer.code = statusCode
}

func TestResponseProxyPassesHeadersThroughAndCapturesTrace(t *testing.T) {
	var upstreamPath string
	var upstreamAuth string
	var upstreamAPIKey string
	var upstreamOpenAIAPIKey string
	var upstreamAltAPIKey string
	var upstreamUserAgent string
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		upstreamPath = request.URL.RequestURI()
		upstreamAuth = request.Header.Get("Authorization")
		upstreamAPIKey = request.Header.Get("X-Api-Key")
		upstreamOpenAIAPIKey = request.Header.Get("OpenAI-Api-Key")
		upstreamAltAPIKey = request.Header.Get("Api-Key")
		upstreamUserAgent = request.Header.Get("User-Agent")
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"resp_1","object":"response","status":"completed","output":[]}`)),
		}, nil
	})}

	traceRoot := t.TempDir()
	handler, err := proxy.NewResponse(proxy.ResponseConfig{
		UpstreamBaseURL: "https://upstream.example/v1",
		APIKey:          "upstream-openai-key",
		Client:          client,
		Tracer:          mbtrace.New(mbtrace.Config{Enabled: true, Root: traceRoot, SessionID: "native"}),
	})
	if err != nil {
		t.Fatalf("NewResponse() error = %v", err)
	}

	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-test","input":"hello"}`))
	request.Header.Set("Authorization", "Bearer native-openai-key")
	request.Header.Set("X-Api-Key", "client-side-extra-key")
	request.Header.Set("OpenAI-Api-Key", "client-openai-api-key")
	request.Header.Set("Api-Key", "client-generic-api-key")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "codex-test-agent")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if upstreamPath != "/v1/responses" {
		t.Fatalf("upstream path = %q", upstreamPath)
	}
	if upstreamAuth != "Bearer upstream-openai-key" {
		t.Fatalf("upstream auth = %q", upstreamAuth)
	}
	if upstreamAPIKey != "" {
		t.Fatalf("upstream x-api-key = %q, want empty", upstreamAPIKey)
	}
	if upstreamOpenAIAPIKey != "" {
		t.Fatalf("upstream openai-api-key = %q, want empty", upstreamOpenAIAPIKey)
	}
	if upstreamAltAPIKey != "" {
		t.Fatalf("upstream api-key = %q, want empty", upstreamAltAPIKey)
	}
	if upstreamUserAgent != "codex-test-agent" {
		t.Fatalf("upstream user-agent = %q", upstreamUserAgent)
	}

	traceData, err := os.ReadFile(filepath.Join(traceRoot, "native", "1.json"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	traceText := string(traceData)
	for _, leaked := range []string{"native-openai-key", "upstream-openai-key", "client-side-extra-key"} {
		if strings.Contains(traceText, leaked) {
			t.Fatalf("trace leaked API key %q: %s", leaked, traceText)
		}
	}
	for _, want := range []string{"[REDACTED]", `"proxy_request"`, `"upstream_request"`, `"upstream_response"`, `"gpt-test"`, `"resp_1"`} {
		if !strings.Contains(traceText, want) {
			t.Fatalf("trace missing %q: %s", want, traceText)
		}
	}
}

func TestAnthropicProxyPassesHeadersThrough(t *testing.T) {
	var upstreamPath string
	var upstreamAuth string
	var upstreamAPIKey string
	var upstreamAnthropicAPIKey string
	var upstreamAltAPIKey string
	var upstreamVersion string
	var upstreamUserAgent string
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		upstreamPath = request.URL.RequestURI()
		upstreamAuth = request.Header.Get("Authorization")
		upstreamAPIKey = request.Header.Get("X-Api-Key")
		upstreamAnthropicAPIKey = request.Header.Get("Anthropic-Api-Key")
		upstreamAltAPIKey = request.Header.Get("Api-Key")
		upstreamVersion = request.Header.Get("Anthropic-Version")
		upstreamUserAgent = request.Header.Get("User-Agent")
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"msg_1","type":"message","role":"assistant","content":[]}`)),
		}, nil
	})}

	handler, err := proxy.NewAnthropic(proxy.AnthropicConfig{
		UpstreamBaseURL: "https://provider.example/v1",
		APIKey:          "upstream-anthropic-key",
		Version:         "2023-06-01",
		Client:          client,
	})
	if err != nil {
		t.Fatalf("NewAnthropic() error = %v", err)
	}

	request := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude","messages":[]}`))
	request.Header.Set("Authorization", "Bearer client-placeholder-token")
	request.Header.Set("X-Api-Key", "client-anthropic-key")
	request.Header.Set("Anthropic-Api-Key", "client-anthropic-alias-key")
	request.Header.Set("Api-Key", "client-generic-api-key")
	request.Header.Set("Anthropic-Version", "2023-01-01")
	request.Header.Set("User-Agent", "anthropic-sdk-test")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if upstreamPath != "/v1/messages" {
		t.Fatalf("upstream path = %q", upstreamPath)
	}
	if upstreamAuth != "" {
		t.Fatalf("upstream auth = %q, want empty", upstreamAuth)
	}
	if upstreamAPIKey != "upstream-anthropic-key" {
		t.Fatalf("upstream api key = %q", upstreamAPIKey)
	}
	if upstreamAnthropicAPIKey != "" {
		t.Fatalf("upstream anthropic-api-key = %q, want empty", upstreamAnthropicAPIKey)
	}
	if upstreamAltAPIKey != "" {
		t.Fatalf("upstream api-key = %q, want empty", upstreamAltAPIKey)
	}
	if upstreamVersion != "2023-06-01" {
		t.Fatalf("upstream version = %q", upstreamVersion)
	}
	if upstreamUserAgent != "anthropic-sdk-test" {
		t.Fatalf("upstream user-agent = %q", upstreamUserAgent)
	}
}

func TestResponseProxyCapturesStreamingResponse(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader("event: response.completed\ndata: {\"type\":\"response.completed\"}\n\n")),
		}, nil
	})}

	traceRoot := t.TempDir()
	handler, err := proxy.NewResponse(proxy.ResponseConfig{
		UpstreamBaseURL: "https://upstream.example/v1",
		Client:          client,
		Tracer:          mbtrace.New(mbtrace.Config{Enabled: true, Root: traceRoot, SessionID: "stream"}),
	})
	if err != nil {
		t.Fatalf("NewResponse() error = %v", err)
	}

	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"stream":true}`))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if !strings.Contains(recorder.Body.String(), "response.completed") {
		t.Fatalf("stream body = %q", recorder.Body.String())
	}
	traceData, err := os.ReadFile(filepath.Join(traceRoot, "stream", "1.json"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	var record map[string]any
	if err := json.Unmarshal(traceData, &record); err != nil {
		t.Fatalf("trace is invalid JSON: %v", err)
	}
	upstreamResponse := record["upstream_response"].(map[string]any)
	if !strings.Contains(upstreamResponse["body"].(string), "response.completed") {
		t.Fatalf("captured body = %+v", upstreamResponse["body"])
	}
}

func TestResponseProxyClientCancelDuringCopyDoesNotWriteTraceError(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader("data: still-streaming\n\n")),
		}, nil
	})}

	traceRoot := t.TempDir()
	handler, err := proxy.NewResponse(proxy.ResponseConfig{
		UpstreamBaseURL: "https://upstream.example/v1",
		Client:          client,
		Tracer:          mbtrace.New(mbtrace.Config{Enabled: true, Root: traceRoot, SessionID: "cancel"}),
	})
	if err != nil {
		t.Fatalf("NewResponse() error = %v", err)
	}

	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"stream":true}`))
	writer := &canceledResponseWriter{}
	handler.ServeHTTP(writer, request)
	if writer.code != http.StatusOK {
		t.Fatalf("status = %d, want 200", writer.code)
	}

	traceData, err := os.ReadFile(filepath.Join(traceRoot, "cancel", "1.json"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	var record map[string]any
	if err := json.Unmarshal(traceData, &record); err != nil {
		t.Fatalf("trace is invalid JSON: %v", err)
	}
	if value, ok := record["error"]; ok {
		t.Fatalf("trace error = %+v, want no error for client cancellation", value)
	}
}
