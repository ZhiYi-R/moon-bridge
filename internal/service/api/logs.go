package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/ZhiYi-R/moon-bridge/internal/logger"
)

type logResponseEntry struct {
	Timestamp string         `json:"timestamp"`
	Level     string         `json:"level"`
	Message   string         `json:"message"`
	Attrs     map[string]any `json:"attrs,omitempty"`
	Raw       string         `json:"raw,omitempty"`
}

// GET /logs
func (r *Router) handleGetLogs(w http.ResponseWriter, req *http.Request) {
	r.handleGetLogsRecent(w, req)
}

func (r *Router) handleGetLogsRecent(w http.ResponseWriter, req *http.Request) {
	limit := parseLogLimit(req)
	entries := logger.Recent(limit)
	resp := make([]logResponseEntry, 0, len(entries))
	for _, entry := range entries {
		resp = append(resp, toLogResponseEntry(entry))
	}
	respondJSON(w, http.StatusOK, resp)
}

func (r *Router) handleGetLogsStream(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	flusher, ok := w.(http.Flusher)
	if !ok {
		respondError(w, http.StatusInternalServerError, "stream_unavailable", "日志流不可用")
		return
	}

	entries := logger.Subscribe(req.Context())
	for {
		select {
		case <-req.Context().Done():
			return
		case entry, ok := <-entries:
			if !ok {
				return
			}
			data, err := json.Marshal(toLogResponseEntry(entry))
			if err != nil {
				slog.Error("marshal log stream event", "error", err)
				continue
			}
			if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
				slog.Error("write log stream event", "error", err)
				return
			}
			flusher.Flush()
		}
	}
}

func parseLogLimit(req *http.Request) int {
	limit, err := strconv.Atoi(req.URL.Query().Get("limit"))
	if err != nil || limit <= 0 {
		return 100
	}
	if limit > 1000 {
		return 1000
	}
	return limit
}

func toLogResponseEntry(entry logger.LogEntry) logResponseEntry {
	timestamp := entry.Timestamp
	if timestamp.IsZero() {
		timestamp = time.Now()
	}
	return logResponseEntry{
		Timestamp: timestamp.UTC().Format(time.RFC3339Nano),
		Level:     entry.Level.String(),
		Message:   entry.Message,
		Attrs:     attrsToMap(entry.Attrs),
		Raw:       string(entry.Raw),
	}
}

func attrsToMap(attrs []slog.Attr) map[string]any {
	if len(attrs) == 0 {
		return nil
	}
	out := make(map[string]any, len(attrs))
	for _, attr := range attrs {
		out[attr.Key] = attrValueToAny(attr.Value)
	}
	return out
}

func attrValueToAny(value slog.Value) any {
	switch value.Kind() {
	case slog.KindString:
		return value.String()
	case slog.KindInt64:
		return value.Int64()
	case slog.KindUint64:
		return value.Uint64()
	case slog.KindFloat64:
		return value.Float64()
	case slog.KindBool:
		return value.Bool()
	case slog.KindDuration:
		return value.Duration().String()
	case slog.KindTime:
		return value.Time().UTC().Format(time.RFC3339Nano)
	case slog.KindGroup:
		return attrsToMap(value.Group())
	case slog.KindAny:
		return value.Any()
	default:
		return value.String()
	}
}
