package logger

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// ConsumeFunc transforms a batch of log entries before output.
type ConsumeFunc func(entries []LogEntry) []LogEntry

// consumeState is a shared mutable cell holding the current ConsumeFunc.
// All handlers derived from the same root (via WithAttrs/WithGroup) share
// a pointer to the same consumeState, so a later SetConsumeFunc call on
// the root handler is visible to every derived logger.
type consumeState struct {
	mu sync.RWMutex
	fn ConsumeFunc
}

func (s *consumeState) load() ConsumeFunc {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.fn
}

func (s *consumeState) store(fn ConsumeFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fn = fn
}

// consumeHandler wraps an inner slog.Handler and dispatches every log record
// through a ConsumeFunc pipeline. This allows plugin LogConsumer interfaces
// to inspect, modify, or suppress log entries at runtime.
//
// When no ConsumeFunc is set, the handler delegates directly to the inner
// handler.
//
// All handlers derived from the same root (via WithAttrs/WithGroup) share
// the same consumeState, so a SetConsumeFunc call on the root is visible
// to every derived logger.
//
// Handler-level attributes (from With/WithGroup) are accumulated in
// handlerAttrs / handlerGroups and merged into LogEntry.Attrs before
// dispatching to the consume pipeline, so consumers see the full context.
type consumeHandler struct {
	inner         slog.Handler
	consume       *consumeState
	handlerAttrs  []slog.Attr // attrs from WithAttrs calls
	handlerGroups []string    // groups from WithGroup calls
}

// newConsumeHandler wraps the given handler with consume-function support.
func newConsumeHandler(inner slog.Handler) *consumeHandler {
	return &consumeHandler{
		inner:   inner,
		consume: &consumeState{},
	}
}

func (h *consumeHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *consumeHandler) Handle(ctx context.Context, r slog.Record) error {
	fn := h.consume.load()

	// Fast path: no consume function registered, delegate directly.
	if fn == nil {
		return h.inner.Handle(ctx, r)
	}

	// Build LogEntry from record-level attrs.
	entry := LogEntry{
		Timestamp: r.Time,
		Level:     r.Level,
		Message:   r.Message,
	}

	// Merge handler-level attrs (from WithAttrs) first.
	entry.Attrs = append(entry.Attrs, h.handlerAttrs...)

	// Then record-level attrs.
	r.Attrs(func(a slog.Attr) bool {
		entry.Attrs = append(entry.Attrs, a)
		return true
	})

	// Wrap attrs under handler-level groups (innermost group first).
	for i := len(h.handlerGroups) - 1; i >= 0; i-- {
		args := make([]any, 0, len(entry.Attrs)*2)
		for _, a := range entry.Attrs {
			args = append(args, a.Key, a.Value.Any())
		}
		entry.Attrs = []slog.Attr{slog.Group(h.handlerGroups[i], args...)}
	}

	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now()
	}

	// Run through consume pipeline (single-entry batch).
	result := fn([]LogEntry{entry})
	if len(result) == 0 {
		return nil // consumed/suppressed
	}

	// Reconstruct a slog.Record from the (possibly modified) entry and
	// delegate to the inner handler for actual serialization.
	rebuilt := slog.Record{
		Time:    result[0].Timestamp,
		Level:   result[0].Level,
		Message: result[0].Message,
	}
	for _, a := range result[0].Attrs {
		rebuilt.AddAttrs(a)
	}
	return h.inner.Handle(ctx, rebuilt)
}

// WithAttrs returns a new handler with the given attributes prepended.
// The returned handler shares the same consumeState and accumulates
// handler-level attrs so that consumers see them.
func (h *consumeHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	// Copy and append to avoid mutating the parent's slice.
	combined := make([]slog.Attr, len(h.handlerAttrs), len(h.handlerAttrs)+len(attrs))
	copy(combined, h.handlerAttrs)
	combined = append(combined, attrs...)
	return &consumeHandler{
		inner:         h.inner.WithAttrs(attrs),
		consume:       h.consume,
		handlerAttrs:  combined,
		handlerGroups: h.handlerGroups,
	}
}

// WithGroup returns a new handler with the given group name.
func (h *consumeHandler) WithGroup(name string) slog.Handler {
	groups := make([]string, len(h.handlerGroups), len(h.handlerGroups)+1)
	copy(groups, h.handlerGroups)
	groups = append(groups, name)
	return &consumeHandler{
		inner:         h.inner.WithGroup(name),
		consume:       h.consume,
		handlerAttrs:  h.handlerAttrs,
		handlerGroups: groups,
	}
}

// SetConsumeFunc sets the consume function on this handler.
func (h *consumeHandler) SetConsumeFunc(fn ConsumeFunc) {
	h.consume.store(fn)
}
