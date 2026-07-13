package api

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ZhiYi-R/moon-bridge/internal/logger"
)

func TestGetLogsRecentReturnsNewestRawLinesInOrder(t *testing.T) {
	resetLoggerForAPITest(t)
	f := newFixture(t)

	slog.Info("old log")
	slog.Info("recent one")
	slog.Info("recent two")

	resp := f.request("GET", "/logs/recent?limit=2", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("GET /logs/recent returned %d: %s", resp.Code, resp.Body.String())
	}

	var entries []logResponseEntry
	f.decode(resp, &entries)
	if len(entries) != 2 {
		t.Fatalf("recent logs length = %d, want 2", len(entries))
	}
	if entries[0].Message != "recent one" || entries[1].Message != "recent two" {
		t.Fatalf("recent messages = %q/%q, want recent one/recent two", entries[0].Message, entries[1].Message)
	}
	if !strings.Contains(entries[0].Raw, "recent one") || !strings.Contains(entries[1].Raw, "recent two") {
		t.Fatalf("raw lines do not include messages: %+v", entries)
	}
}

func TestGetLogsStreamReturnsSSEFrames(t *testing.T) {
	resetLoggerForAPITest(t)
	f := newFixture(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest("GET", "http://moonbridge.test/logs/stream", nil).WithContext(ctx)
	resp := httptest.NewRecorder()

	done := make(chan struct{}, 1)
	go func() {
		f.handler.ServeHTTP(resp, req)
		done <- struct{}{}
	}()

	time.Sleep(20 * time.Millisecond)
	slog.Info("streamed log")
	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case <-done:
		if resp.Code != http.StatusOK {
			t.Fatalf("GET /logs/stream returned %d: %s", resp.Code, resp.Body.String())
		}
		if got := resp.Header().Get("Content-Type"); got != "text/event-stream" {
			t.Fatalf("Content-Type = %q, want text/event-stream", got)
		}
		body := resp.Body.String()
		if !strings.Contains(body, "data:") || !strings.Contains(body, "streamed log") {
			t.Fatalf("SSE body missing frame/message: %s", body)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for log stream response")
	}
}

func resetLoggerForAPITest(t *testing.T) {
	t.Helper()
	var out bytes.Buffer
	if err := logger.Init(logger.Config{Level: logger.LevelInfo, Format: "json", Output: &out}); err != nil {
		t.Fatalf("logger.Init() error = %v", err)
	}
}

func TestLogResponseEntryJSONShape(t *testing.T) {
	entry := logResponseEntry{
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Level:     "INFO",
		Message:   "shape",
		Attrs:     map[string]any{"k": "v"},
		Raw:       "raw",
	}
	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("json.Marshal(logResponseEntry) error = %v", err)
	}
	if !strings.Contains(string(data), `"message":"shape"`) {
		t.Fatalf("unexpected JSON: %s", data)
	}
}
