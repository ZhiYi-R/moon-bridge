package logger

import (
	"bytes"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestMemoryBufferEmit(t *testing.T) {
	var buf bytes.Buffer
	mb := NewMemoryBuffer(&buf)

	mb.Emit(LogEntry{Message: "hello"})
	mb.Emit(LogEntry{Raw: []byte("raw line\n")})

	if err := mb.Flush(nil); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "hello") {
		t.Fatalf("output missing 'hello': %s", output)
	}
	if !strings.Contains(output, "raw line") {
		t.Fatalf("output missing 'raw line': %s", output)
	}
}

func TestMemoryBufferWriter(t *testing.T) {
	var buf bytes.Buffer
	mb := NewMemoryBuffer(&buf)

	w := mb.Writer()
	fmt.Fprintln(w, "line one")
	fmt.Fprintln(w, "line two")

	if err := mb.Flush(nil); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "line one") {
		t.Fatalf("output missing 'line one': %s", output)
	}
	if !strings.Contains(output, "line two") {
		t.Fatalf("output missing 'line two': %s", output)
	}
}

func TestMemoryBufferFlushEmpty(t *testing.T) {
	var buf bytes.Buffer
	mb := NewMemoryBuffer(&buf)

	if err := mb.Flush(nil); err != nil {
		t.Fatalf("Flush() empty error = %v", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("expected empty output after flush, got %q", buf.String())
	}
}

func TestMemoryBufferConcurrent(t *testing.T) {
	var buf bytes.Buffer
	mb := NewMemoryBuffer(&buf)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			mb.Emit(LogEntry{Message: fmt.Sprintf("%c", 'A'+n)})
			mb.Writer().Write([]byte("raw\n"))
		}(i)
	}
	wg.Wait()

	if err := mb.Flush(nil); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	if buf.Len() == 0 {
		t.Fatal("expected non-empty output after concurrent writes")
	}
}

func TestMemoryBufferConsumeCallback(t *testing.T) {
	var buf bytes.Buffer
	mb := NewMemoryBuffer(&buf)

	mb.Emit(LogEntry{Message: "before"})

	consume := func(entries []LogEntry) []LogEntry {
		return append(entries, LogEntry{Raw: []byte("injected\n")})
	}

	if err := mb.Flush(consume); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "before") {
		t.Fatalf("output missing 'before': %s", output)
	}
	if !strings.Contains(output, "injected") {
		t.Fatalf("output missing 'injected': %s", output)
	}
}

func TestMemoryBufferFlushIdempotent(t *testing.T) {
	var buf bytes.Buffer
	mb := NewMemoryBuffer(&buf)

	mb.Emit(LogEntry{Message: "once"})
	mb.Flush(nil)
	mb.Flush(nil) // second flush should be a no-op

	output := buf.String()
	if strings.Count(output, "once") != 1 {
		t.Fatalf("expected 'once' exactly once, got %q", output)
	}
}
