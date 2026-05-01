package logger

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"
)

// defaultHandler is the root consumeHandler wrapping the configured text/json handler.
// It is referenced by the slog.Default() logger after Init. SetConsumeFunc operates
// on this handler, and all derived sub-loggers share the same consumeState.
var defaultHandler *consumeHandler

// LogEntry represents a single log entry passed through the consume pipeline.
type LogEntry struct {
	Timestamp time.Time
	Level     slog.Level
	Message   string
	Attrs     []slog.Attr
	Raw       []byte
}

func init() {
	h := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})
	defaultHandler = newConsumeHandler(h)
	slog.SetDefault(slog.New(defaultHandler))
}

// Level represents a log level.
type Level string

const (
	LevelDebug Level = "debug"
	LevelInfo  Level = "info"
	LevelWarn  Level = "warn"
	LevelError Level = "error"
)

// ParseLevel parses a level string.
func ParseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, fmt.Errorf("invalid log level %q", s)
	}
}

// Config holds logger configuration.
type Config struct {
	Level  Level
	Format string // "text" or "json"
	Output io.Writer
}

// Init configures slog's default logger.
// The inner handler is wrapped with a consumeHandler so that plugins
// registered via SetConsumeFunc receive every log record.
// After Init, all code using slog.Default() sees the configured handler.
func Init(cfg Config) error {
	lvl, err := ParseLevel(string(cfg.Level))
	if err != nil {
		return err
	}
	out := cfg.Output
	if out == nil {
		out = os.Stderr
	}
	opts := &slog.HandlerOptions{Level: lvl}
	var inner slog.Handler
	switch strings.ToLower(strings.TrimSpace(cfg.Format)) {
	case "json":
		inner = slog.NewJSONHandler(out, opts)
	default:
		inner = slog.NewTextHandler(out, opts)
	}
	defaultHandler = newConsumeHandler(inner)
	slog.SetDefault(slog.New(defaultHandler))
	return nil
}

// SetConsumeFunc registers a consume callback that is invoked for every
// log record before it is serialized. The callback receives a single-entry
// LogEntry slice and may return it modified (or empty to suppress).
func SetConsumeFunc(fn ConsumeFunc) {
	if defaultHandler != nil {
		defaultHandler.SetConsumeFunc(fn)
	}
}
