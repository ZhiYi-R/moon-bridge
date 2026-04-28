package logger

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
)

var defaultLogger *slog.Logger
var defaultOutput io.Writer
var defaultBuffer LogBuffer

func init() {
	defaultOutput = os.Stderr
	defaultLogger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
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

// Init initializes the default logger from config.
func Init(cfg Config) error {
	lvl, err := ParseLevel(string(cfg.Level))
	if err != nil {
		return err
	}
	if cfg.Output == nil {
		cfg.Output = os.Stderr
	}
	defaultOutput = cfg.Output
	oldBuffer := defaultBuffer
	defaultBuffer = NewMemoryBuffer(cfg.Output)
	if oldBuffer != nil {
		if ob, ok := oldBuffer.(*memoryBuffer); ok {
			if nb, ok := defaultBuffer.(*memoryBuffer); ok {
				nb.consumeFunc = ob.consumeFunc
			}
		}
	}
	opts := &slog.HandlerOptions{Level: lvl}
	var handler slog.Handler
	switch strings.ToLower(strings.TrimSpace(cfg.Format)) {
	case "json":
		handler = slog.NewJSONHandler(cfg.Output, opts)
	default:
		handler = slog.NewTextHandler(cfg.Output, opts)
	}
	defaultLogger = slog.New(handler)
	return nil
}

// L returns the default logger.
func L() *slog.Logger {
	return defaultLogger
}

// Output returns the writer used by the default logger.
// After the unified logging system is enabled, this returns the buffer's
// Writer; call Buffer().Flush() to write accumulated entries.
func Output() io.Writer {
	if defaultBuffer != nil {
		return defaultBuffer.Writer()
	}
	return defaultOutput
}

// Buffer returns the default LogBuffer instance.
func Buffer() LogBuffer {
	return defaultBuffer
}

// Flush flushes the default log buffer. No-op if buffer is nil.
func Flush() {
	if defaultBuffer != nil {
		defaultBuffer.Flush(nil)
	}
}

// SetConsumeFunc sets a consume callback on the default buffer.
func SetConsumeFunc(fn func([]LogEntry) []LogEntry) {
	if b, ok := defaultBuffer.(*memoryBuffer); ok {
		b.SetConsumeFunc(fn)
	}
}

// Debug logs a debug message.
func Debug(msg string, args ...any) {
	defaultLogger.Debug(msg, args...)
}

// Info logs an info message.
func Info(msg string, args ...any) {
	defaultLogger.Info(msg, args...)
}

// Warn logs a warning message.
func Warn(msg string, args ...any) {
	defaultLogger.Warn(msg, args...)
}

// Error logs an error message.
func Error(msg string, args ...any) {
	defaultLogger.Error(msg, args...)
}
