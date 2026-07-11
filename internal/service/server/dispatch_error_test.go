package server

import (
	"net/http/httptest"
	"strings"
	"testing"

	"moonbridge/internal/protocol/openai"
)

// writeSSE must propagate marshalling failures instead of silently emitting a
// truncated/empty data frame.
func TestWriteSSEPropagatesMarshalError(t *testing.T) {
	rec := httptest.NewRecorder()
	// channels are not JSON-serializable, forcing json.Marshal to fail.
	event := openai.StreamEvent{Event: "response.output_text.delta", Data: make(chan int)}

	err := writeSSE(rec, event)
	if err == nil {
		t.Fatal("expected error when event data cannot be marshalled, got nil")
	}
	if !strings.Contains(err.Error(), "marshal SSE event") {
		t.Fatalf("expected marshal error, got %q", err.Error())
	}
}

// writeSSE serializes a well-formed event into the expected SSE frame.
func TestWriteSSEWritesFrame(t *testing.T) {
	rec := httptest.NewRecorder()
	event := openai.StreamEvent{Event: "ping", Data: map[string]string{"k": "v"}}

	if err := writeSSE(rec, event); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "event: ping\n") {
		t.Fatalf("missing event line in %q", body)
	}
	if !strings.Contains(body, `data: {"k":"v"}`) {
		t.Fatalf("missing data line in %q", body)
	}
}
