package logger

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestConsumeHandler_SetConsumeFunc_AfterWithAttrs(t *testing.T) {
	// P1 regression: a logger derived via With BEFORE SetConsumeFunc
	// must still route records through the consume pipeline after
	// SetConsumeFunc is called.
	var buf bytes.Buffer
	Init(Config{Level: LevelInfo, Format: "json", Output: &buf})

	// Derive logger BEFORE setting consume func.
	derived := slog.With("plugin", "test-plugin")

	var received []LogEntry
	SetConsumeFunc(func(entries []LogEntry) []LogEntry {
		received = append(received, entries...)
		return entries
	})

	derived.Info("hello from derived")

	if len(received) != 1 || received[0].Message != "hello from derived" {
		t.Fatalf("consume func should have received message; got: %v", received)
	}

	// Verify handler-level attrs (from With) are visible to consumers.
	found := false
	for _, a := range received[0].Attrs {
		if a.Key == "plugin" && a.Value.String() == "test-plugin" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected consumer to see plugin attr, got attrs: %v", received[0].Attrs)
	}

	// Verify output still reaches the inner handler.
	if !strings.Contains(buf.String(), "hello from derived") {
		t.Fatalf("expected output to contain message, got: %s", buf.String())
	}
}

func TestConsumeHandler_SetConsumeFunc_Suppress(t *testing.T) {
	var buf bytes.Buffer
	Init(Config{Level: LevelInfo, Format: "json", Output: &buf})

	SetConsumeFunc(func(entries []LogEntry) []LogEntry {
		return nil // suppress
	})

	slog.Info("should be suppressed")

	if buf.Len() != 0 {
		t.Fatalf("expected empty output after suppression, got: %s", buf.String())
	}
}

func TestConsumeHandler_SetConsumeFunc_Modify(t *testing.T) {
	var buf bytes.Buffer
	Init(Config{Level: LevelInfo, Format: "json", Output: &buf})

	SetConsumeFunc(func(entries []LogEntry) []LogEntry {
		entries[0].Message = "modified"
		return entries
	})

	slog.Info("original")

	if !strings.Contains(buf.String(), "modified") {
		t.Fatalf("expected modified message in output, got: %s", buf.String())
	}
	if strings.Contains(buf.String(), "original") {
		t.Fatalf("expected original message to be replaced, got: %s", buf.String())
	}
}

func TestConsumeHandler_NoFunc_FastPath(t *testing.T) {
	var buf bytes.Buffer
	Init(Config{Level: LevelInfo, Format: "json", Output: &buf})

	// No SetConsumeFunc — should delegate directly.
	slog.Info("fast path")

	if !strings.Contains(buf.String(), "fast path") {
		t.Fatalf("expected output, got: %s", buf.String())
	}
}

func TestConsumeHandler_WithGroup_VisibleToConsumer(t *testing.T) {
	var buf bytes.Buffer
	Init(Config{Level: LevelInfo, Format: "json", Output: &buf})

	derived := slog.Default().WithGroup("request").With("method", "GET")

	var received []LogEntry
	SetConsumeFunc(func(entries []LogEntry) []LogEntry {
		received = append(received, entries...)
		return entries
	})

	derived.Info("grouped")

	if len(received) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(received))
	}

	// The attrs should be wrapped in a "request" group.
	foundGroup := false
	for _, a := range received[0].Attrs {
		if a.Key == "request" {
			foundGroup = true
			// Check that method=GET is inside the group.
			groupAttrs := a.Value.Group()
			for _, ga := range groupAttrs {
				if ga.Key == "method" && ga.Value.String() == "GET" {
					return // success
				}
			}
			t.Fatalf("expected method=GET inside request group, got: %v", groupAttrs)
		}
	}
	if !foundGroup {
		t.Fatalf("expected request group in attrs, got: %v", received[0].Attrs)
	}
}
