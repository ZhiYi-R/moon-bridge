package logger

import (
	"io"
	"log/slog"
	"sync"
	"time"
)

// LogEntry represents a single log entry in the buffer.
// Raw takes precedence over Message/Attrs: if Raw is non-nil, it is written
// directly during Flush without formatting.
type LogEntry struct {
	Timestamp time.Time
	Level     slog.Level
	Message   string
	Attrs     []slog.Attr
	Raw       []byte
}

// LogBuffer buffers log entries and writes them in batch on Flush.
// Non-slog output (via Output().Write / Emit) goes through it.
// Structured slog output bypasses the buffer and writes directly.
type LogBuffer interface {
	// Emit adds a single structured log entry.
	Emit(entry LogEntry)

	// Flush writes all buffered entries to the underlying writer and clears
	// the buffer. An optional consume callback may transform entries before
	// they are written.
	Flush(consume func([]LogEntry) []LogEntry) error

	// Writer returns an io.Writer that wraps each Write call into a raw
	// LogEntry. Suitable for passing to fmt.Fprintln etc.
	Writer() io.Writer
}

// ConsumeFunc is the signature for a log consumer callback.
// It receives a snapshot of buffered entries and returns the entries to write.
type ConsumeFunc func(entries []LogEntry) []LogEntry

// memoryBuffer is a simple in-memory implementation of LogBuffer.
type memoryBuffer struct {
	mu          sync.Mutex
	entries     []LogEntry
	output      io.Writer
	outputMu    sync.Mutex
	consumeFunc func([]LogEntry) []LogEntry
}

// NewMemoryBuffer creates a LogBuffer that writes to the given output.
func NewMemoryBuffer(output io.Writer) LogBuffer {
	return &memoryBuffer{
		entries: make([]LogEntry, 0, 64),
		output:  output,
	}
}

// SetConsumeFunc sets a global consume callback called on every Flush.
func (b *memoryBuffer) SetConsumeFunc(fn func([]LogEntry) []LogEntry) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.consumeFunc = fn
}

func (b *memoryBuffer) Emit(entry LogEntry) {
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now()
	}
	// Copy Raw to avoid caller modifying the underlying slice.
	if len(entry.Raw) > 0 {
		raw := make([]byte, len(entry.Raw))
		copy(raw, entry.Raw)
		entry.Raw = raw
	}
	b.mu.Lock()
	b.entries = append(b.entries, entry)
	b.mu.Unlock()
}

func (b *memoryBuffer) Flush(consume func([]LogEntry) []LogEntry) error {
	b.mu.Lock()
	snapshot := b.entries
	b.entries = make([]LogEntry, 0, 64)
	callback := b.consumeFunc
	b.mu.Unlock()

	// Serialize consumer + writes to prevent interleaving and race.
	b.outputMu.Lock()
	defer b.outputMu.Unlock()

	if consume != nil {
		snapshot = consume(snapshot)
	} else if callback != nil {
		snapshot = callback(snapshot)
	}
	for _, entry := range snapshot {
		if entry.Raw != nil {
			if _, err := b.output.Write(entry.Raw); err != nil {
				return err
			}
		} else if entry.Message != "" {
			line := entry.Message + "\n"
			if _, err := io.WriteString(b.output, line); err != nil {
				return err
			}
		}
	}
	return nil
}

func (b *memoryBuffer) Writer() io.Writer {
	return &bufferWriter{buffer: b}
}

// bufferWriter wraps a memoryBuffer and implements io.Writer.
type bufferWriter struct {
	buffer *memoryBuffer
}

func (w *bufferWriter) Write(p []byte) (int, error) {
	// Copy to avoid holding caller's buffer.
	raw := make([]byte, len(p))
	copy(raw, p)
	w.buffer.Emit(LogEntry{
		Timestamp: time.Now(),
		Raw:       raw,
	})
	return len(p), nil
}
