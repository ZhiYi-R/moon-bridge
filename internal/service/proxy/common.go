package proxy

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	mbtrace "moonbridge/internal/service/trace"
)

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
		if isHopByHopHeader(key) || strings.EqualFold(key, "host") || strings.EqualFold(key, "accept-encoding") {
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
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
				return err
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
			return readErr
		}
	}
}
