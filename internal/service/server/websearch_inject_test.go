package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/ZhiYi-R/moon-bridge/internal/protocol/chat"
)

type wsInjectRTFunc func(*http.Request) (*http.Response, error)

func (fn wsInjectRTFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func newTestChatClient(t *testing.T, rt http.RoundTripper) *chat.Client {
	t.Helper()
	return chat.NewClient(chat.ClientConfig{
		BaseURL: "https://chat.example.test",
		APIKey:  "chat-key",
		Client:  &http.Client{Transport: rt},
	})
}

func sseBody(lines ...string) string {
	return strings.Join(lines, "\n") + "\n"
}

func TestCollectStreamToolCalls_ReconstructsAcrossChunks(t *testing.T) {
	idx0 := 0
	idx1 := 1
	events := []chat.ChatStreamChunk{
		{
			Choices: []chat.StreamChoice{{
				Index: 0,
				Delta: chat.Delta{
					Role: "assistant",
					ToolCalls: []chat.ToolCall{
						{Index: &idx0, ID: "call_a", Type: "function", Function: chat.ToolCallFunc{Name: "tool_a", Arguments: json.RawMessage(``)}},
						{Index: &idx1, ID: "call_b", Type: "function", Function: chat.ToolCallFunc{Name: "tool_b", Arguments: json.RawMessage(``)}},
					},
				},
			}},
		},
		{
			Choices: []chat.StreamChoice{{
				Index: 0,
				Delta: chat.Delta{
					ToolCalls: []chat.ToolCall{
						{Index: &idx1, Function: chat.ToolCallFunc{Arguments: json.RawMessage(`"{\"b\":2}"`)}},
						{Index: &idx0, Function: chat.ToolCallFunc{Arguments: json.RawMessage(`"{\"a\":1}"`)}},
					},
				},
			}},
		},
		{
			Choices: []chat.StreamChoice{{
				Index: 0,
				Delta: chat.Delta{
					ToolCalls: []chat.ToolCall{
						{Index: &idx0, Function: chat.ToolCallFunc{Arguments: json.RawMessage(`""`)}},
					},
				},
			}},
		},
	}

	got := collectStreamToolCalls(events)
	if len(got) != 2 {
		t.Fatalf("tool calls=%d, want 2", len(got))
	}
	if got[0].ID != "call_a" || got[0].Function.Name != "tool_a" {
		t.Fatalf("tool[0]=%+v", got[0])
	}
	if got[1].ID != "call_b" || got[1].Function.Name != "tool_b" {
		t.Fatalf("tool[1]=%+v", got[1])
	}
	var a map[string]any
	if err := json.Unmarshal(unquoteRawJSON(got[0].Function.Arguments), &a); err != nil {
		t.Fatalf("tool[0] args invalid json: %v (%s)", err, string(got[0].Function.Arguments))
	}
	var b map[string]any
	if err := json.Unmarshal(unquoteRawJSON(got[1].Function.Arguments), &b); err != nil {
		t.Fatalf("tool[1] args invalid json: %v (%s)", err, string(got[1].Function.Arguments))
	}
	if fmt.Sprint(a["a"]) != "1" {
		t.Fatalf("tool[0] arg a=%v", a["a"])
	}
	if fmt.Sprint(b["b"]) != "2" {
		t.Fatalf("tool[1] arg b=%v", b["b"])
	}
}

func TestChatSearchBufferedStream_NoToolCallPassThrough(t *testing.T) {
	client := newTestChatClient(t, wsInjectRTFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("method=%s", r.Method)
		}
		body := sseBody(
			`data: {"id":"x","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":"Hello"}}]}`,
			`data: {"id":"x","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":" world"}}]}`,
			`data: {"id":"x","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
			`data: [DONE]`,
		)
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		}, nil
	}))

	srv := &Server{}
	req := &chat.ChatRequest{
		Model:    "gpt-test",
		Messages: []chat.ChatMessage{{Role: "user", Content: "hi"}},
	}
	ch, err := srv.chatSearchBufferedStream(context.Background(), client, req, "", "", 2)
	if err != nil {
		t.Fatalf("chatSearchBufferedStream error: %v", err)
	}
	var chunks []chat.ChatStreamChunk
	for c := range ch {
		chunks = append(chunks, c)
	}
	if len(chunks) != 3 {
		t.Fatalf("chunks=%d, want 3", len(chunks))
	}
	if len(req.Messages) != 1 {
		t.Fatalf("messages should not be expanded on no-tool path, got %d", len(req.Messages))
	}
}

func TestChatSearchBufferedStream_ExceedsMaxRounds(t *testing.T) {
	attempt := 0
	client := newTestChatClient(t, wsInjectRTFunc(func(r *http.Request) (*http.Response, error) {
		attempt++
		sse := sseBody(
			`data: {"id":"x","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"tavily_search","arguments":""}}]}}]}`,
			`data: {"id":"x","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"query\":\"\"}"}}]}}]}`,
			`data: {"id":"x","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
			`data: [DONE]`,
		)
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(sse)),
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		}, nil
	}))

	srv := &Server{}
	req := &chat.ChatRequest{
		Model:    "gpt-test",
		Messages: []chat.ChatMessage{{Role: "user", Content: "search hi"}},
	}
	_, err := srv.chatSearchBufferedStream(context.Background(), client, req, "", "", 1)
	if err == nil {
		t.Fatal("expected max-rounds error, got nil")
	}
	if !strings.Contains(err.Error(), "exceeded max rounds") {
		t.Fatalf("unexpected err: %v", err)
	}
	if attempt != 1 {
		t.Fatalf("attempts=%d, want 1", attempt)
	}
}
