package proxy

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type canceledReader struct{}

func (canceledReader) Read([]byte) (int, error) {
	return 0, context.Canceled
}

type canceledWriter struct{}

func (canceledWriter) Header() http.Header {
	return make(http.Header)
}

func (canceledWriter) Write([]byte) (int, error) {
	return 0, context.Canceled
}

func (canceledWriter) WriteHeader(int) {}

func TestClientCanceledCopyErrorRequiresWriteSideOrRequestCancel(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	if err := copyStreaming(canceledWriter{}, strings.NewReader("chunk"), &bytes.Buffer{}); err == nil {
		t.Fatal("copyStreaming() write-side error = nil")
	} else if !isClientCanceledCopyError(request, err) {
		t.Fatal("write-side context canceled was not classified as client cancellation")
	}

	if err := copyStreaming(httptest.NewRecorder(), canceledReader{}, &bytes.Buffer{}); err == nil {
		t.Fatal("copyStreaming() read-side error = nil")
	} else if isClientCanceledCopyError(request, err) {
		t.Fatal("read-side context canceled was classified as client cancellation")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	canceledRequest := request.WithContext(ctx)
	if err := copyStreaming(httptest.NewRecorder(), canceledReader{}, &bytes.Buffer{}); err == nil {
		t.Fatal("copyStreaming() canceled request read-side error = nil")
	} else if !isClientCanceledCopyError(canceledRequest, err) {
		t.Fatal("request cancellation did not classify read-side context canceled as client cancellation")
	}
}
