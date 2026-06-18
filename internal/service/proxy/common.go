package proxy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"syscall"

	mbtrace "moonbridge/internal/service/trace"
)

type copyErrorSource string

const (
	copyErrorSourceRead  copyErrorSource = "read"
	copyErrorSourceWrite copyErrorSource = "write"
)

type streamingCopyError struct {
	source copyErrorSource
	err    error
}

func (err *streamingCopyError) Error() string {
	return err.err.Error()
}

func (err *streamingCopyError) Unwrap() error {
	return err.err
}

type HeaderOverride func(http.Header)

type ProxyRequest struct {
	Method  string      `json:"method"`
	URL     string      `json:"url"`
	Headers http.Header `json:"headers,omitempty"`
	Body    any         `json:"body,omitempty"`
}

type ProxyResponse struct {
	StatusCode int         `json:"status_code"`
	Headers    http.Header `json:"headers,omitempty"`
	Body       any         `json:"body,omitempty"`
}

func normalizeUpstreamBaseURL(baseURL string, defaultBaseURL string) (string, error) {
	upstreamBaseURL := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if upstreamBaseURL == "" {
		upstreamBaseURL = defaultBaseURL
	}
	if !strings.HasSuffix(upstreamBaseURL, "/v1") {
		upstreamBaseURL += "/v1"
	}
	if _, err := url.ParseRequestURI(upstreamBaseURL); err != nil {
		return "", fmt.Errorf("invalid upstream base URL: %w", err)
	}
	return upstreamBaseURL, nil
}

func newUpstreamRequest(request *http.Request, upstreamURL string, body []byte, override HeaderOverride) (*http.Request, error) {
	upstreamRequest, err := http.NewRequestWithContext(request.Context(), request.Method, upstreamURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	copyHeaders(upstreamRequest.Header, request.Header)
	if override != nil {
		override(upstreamRequest.Header)
	}
	return upstreamRequest, nil
}

func upstreamURL(upstreamBaseURL string, request *http.Request) string {
	path := request.URL.Path
	if strings.HasPrefix(path, "/v1/") {
		path = strings.TrimPrefix(path, "/v1")
	}
	target := upstreamBaseURL + path
	if request.URL.RawQuery != "" {
		target += "?" + request.URL.RawQuery
	}
	return target
}

func writeTrace(tracer *mbtrace.Tracer, traceErrors io.Writer, record mbtrace.Record) {
	if tracer == nil {
		return
	}
	if _, err := tracer.Write(record); err != nil && traceErrors != nil {
		fmt.Fprintf(traceErrors, "代理跟踪写入失败: %v\n", err)
	}
}

func copyHeaders(dst http.Header, src http.Header) {
	for key, values := range src {
		if isHopByHopHeader(key) || isUpstreamManagedHeader(key) ||
			strings.EqualFold(key, "host") || strings.EqualFold(key, "accept-encoding") {
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func isUpstreamManagedHeader(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	normalized = strings.ReplaceAll(normalized, "_", "-")
	switch normalized {
	case "authorization", "proxy-authorization", "x-api-key", "api-key", "apikey", "anthropic-api-key", "openai-api-key":
		return true
	default:
		return false
	}
}

func isHopByHopHeader(key string) bool {
	switch strings.ToLower(key) {
	case "connection", "keep-alive", "proxy-authenticate", "proxy-authorization", "te", "trailer", "transfer-encoding", "upgrade":
		return true
	default:
		return false
	}
}

func copyStreaming(writer http.ResponseWriter, reader io.Reader, capture *bytes.Buffer) error {
	buffer := make([]byte, 32*1024)
	flusher, _ := writer.(http.Flusher)
	for {
		n, readErr := reader.Read(buffer)
		if n > 0 {
			chunk := buffer[:n]
			if _, err := writer.Write(chunk); err != nil {
				return &streamingCopyError{source: copyErrorSourceWrite, err: err}
			}
			_, _ = capture.Write(chunk)
			if flusher != nil {
				flusher.Flush()
			}
		}
		if readErr == io.EOF {
			return nil
		}
		if readErr != nil {
			return &streamingCopyError{source: copyErrorSourceRead, err: readErr}
		}
	}
}

func isClientCanceledCopyError(request *http.Request, err error) bool {
	if err == nil {
		return false
	}
	var copyErr *streamingCopyError
	if errors.As(err, &copyErr) {
		if copyErr.source == copyErrorSourceRead && (request == nil || !errors.Is(request.Context().Err(), context.Canceled)) {
			return false
		}
	}
	if errors.Is(err, http.ErrAbortHandler) ||
		errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, syscall.ECONNRESET) {
		return true
	}
	if request != nil && errors.Is(request.Context().Err(), context.Canceled) {
		return true
	}
	if errors.Is(err, context.Canceled) {
		return copyErr != nil && copyErr.source == copyErrorSourceWrite
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "context canceled") ||
		strings.Contains(message, "broken pipe") ||
		strings.Contains(message, "connection reset by peer") ||
		strings.Contains(message, "client disconnected") ||
		strings.Contains(message, "use of closed network connection")
}
