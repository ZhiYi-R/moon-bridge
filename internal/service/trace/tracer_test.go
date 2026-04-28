package trace_test

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"moonbridge/internal/service/trace"
)

func TestTracerWritesRedactedRecord(t *testing.T) {
	root := t.TempDir()
	tracer := trace.New(trace.Config{Enabled: true, Root: root, SessionID: "session-test"})

	request, err := http.NewRequest(http.MethodPost, "/v1/responses", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	request.Header.Set("Authorization", "Bearer client-api-key")

	path, err := tracer.Write(trace.Record{
		Model:       "gpt-test",
		HTTPRequest: trace.NewHTTPRequest(request),
		OpenAIRequest: map[string]any{
			"api_key": "payload-api-key",
			"input":   "keep this prompt unchanged",
		},
	})
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if path != filepath.Join(root, "session-test", "gpt-test", "1.json") {
		t.Fatalf("path = %q, want %q", path, filepath.Join(root, "session-test", "gpt-test", "1.json"))
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	content := string(data)
	for _, leaked := range []string{"client-api-key", "payload-api-key"} {
		if strings.Contains(content, leaked) {
			t.Fatalf("trace leaked API key %q: %s", leaked, content)
		}
	}
	for _, want := range []string{"[REDACTED]", "keep this prompt unchanged", `"request_number": 1`, `"session_id": "session-test"`} {
		if !strings.Contains(content, want) {
			t.Fatalf("trace missing %q: %s", want, content)
		}
	}
}

func TestDisabledTracerDoesNotWrite(t *testing.T) {
	root := t.TempDir()
	tracer := trace.New(trace.Config{Enabled: false, Root: root, SessionID: "session-test"})

	path, err := tracer.Write(trace.Record{})
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if path != "" {
		t.Fatalf("path = %q, want empty", path)
	}
	if _, err := os.Stat(filepath.Join(root, "session-test")); !os.IsNotExist(err) {
		t.Fatalf("session dir stat error = %v, want not exists", err)
	}
}

func TestFlatTracerWritesDirectlyUnderRoot(t *testing.T) {
	root := t.TempDir()
	tracer := trace.New(trace.Config{Enabled: true, Root: filepath.Join(root, "Capture", "Response"), Flat: true})

	path, err := tracer.Write(trace.Record{
		Model:         "gpt-test",
		OpenAIRequest: map[string]any{"input": "capture me"},
	})
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if path != filepath.Join(root, "Capture", "Response", "1.json") {
		t.Fatalf("path = %q, want %q", path, filepath.Join(root, "Capture", "Response", "1.json"))
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	content := string(data)
	if strings.Contains(content, "session_id") {
		t.Fatalf("flat trace should not include session_id: %s", content)
	}
}

func TestTracerWritesNumberedCategoryUnderSession(t *testing.T) {
	root := t.TempDir()
	tracer := trace.New(trace.Config{Enabled: true, Root: filepath.Join(root, "Transform"), SessionID: "session-test"})

	requestNumber := tracer.NextRequestNumber()
	responsePath, err := tracer.WriteNumbered("Response", requestNumber, trace.Record{
		Model:         "gpt-test",
		OpenAIRequest: map[string]any{"input": "hello"},
	})
	if err != nil {
		t.Fatalf("WriteNumbered(Response) error = %v", err)
	}
	anthropicPath, err := tracer.WriteNumbered("Anthropic", requestNumber, trace.Record{
		Model:            "gpt-test",
		AnthropicRequest: map[string]any{"messages": []any{}},
	})
	if err != nil {
		t.Fatalf("WriteNumbered(Anthropic) error = %v", err)
	}
	if responsePath != filepath.Join(root, "Transform", "session-test", "gpt-test", "Response", "1.json") {
		t.Fatalf("response path = %q, want %q", responsePath, filepath.Join(root, "Transform", "session-test", "gpt-test", "Response", "1.json"))
	}
	if anthropicPath != filepath.Join(root, "Transform", "session-test", "gpt-test", "Anthropic", "1.json") {
		t.Fatalf("anthropic path = %q, want %q", anthropicPath, filepath.Join(root, "Transform", "session-test", "gpt-test", "Anthropic", "1.json"))
	}
}

func TestSanitizePathSegmentPreservesSimpleNames(t *testing.T) {
	if got := trace.SanitizePathSegment("gpt-test"); got != "gpt-test" {
		t.Fatalf("SanitizePathSegment(gpt-test) = %q, want %q", got, "gpt-test")
	}
}

func TestSanitizePathSegmentReplacesSlashes(t *testing.T) {
	if got := trace.SanitizePathSegment("openai/gpt-5.2"); got != "openai_gpt-5.2" {
		t.Fatalf("SanitizePathSegment(openai/gpt-5.2) = %q, want %q", got, "openai_gpt-5.2")
	}
}

func TestSanitizePathSegmentReplacesColons(t *testing.T) {
	if got := trace.SanitizePathSegment("claude:sonnet"); got != "claude_sonnet" {
		t.Fatalf("SanitizePathSegment(claude:sonnet) = %q, want %q", got, "claude_sonnet")
	}
}

func TestSanitizePathSegmentEmptyReturnsUnderscore(t *testing.T) {
	if got := trace.SanitizePathSegment("  "); got != "_" {
		t.Fatalf("SanitizePathSegment(empty) = %q, want _", got)
	}
}

func TestSanitizePathSegmentDotReturnsUnderscore(t *testing.T) {
	if got := trace.SanitizePathSegment("."); got != "_" {
		t.Fatalf("SanitizePathSegment(dot) = %q, want _", got)
	}
}

func TestSanitizePathSegmentDotDotReturnsUnderscore(t *testing.T) {
	if got := trace.SanitizePathSegment(".."); got != "_" {
		t.Fatalf("SanitizePathSegment(dotdot) = %q, want _", got)
	}
}

func TestSanitizePathSegmentAllDotsReturnsUnderscore(t *testing.T) {
	if got := trace.SanitizePathSegment("..."); got != "..." {
		t.Fatalf("SanitizePathSegment(...) = %q, want ...", got)
	}
}
