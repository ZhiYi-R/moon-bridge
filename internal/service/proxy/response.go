package proxy

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"

	mbtrace "moonbridge/internal/service/trace"
)

type ResponseConfig struct {
	UpstreamBaseURL string
	APIKey          string
	Client          *http.Client
	Tracer          *mbtrace.Tracer
	TraceErrors     io.Writer
}

type ResponseServer struct {
	upstreamBaseURL string
	apiKey          string
	client          *http.Client
	tracer          *mbtrace.Tracer
	traceErrors     io.Writer
}

func NewResponse(cfg ResponseConfig) (*ResponseServer, error) {
	upstreamBaseURL, err := normalizeUpstreamBaseURL(cfg.UpstreamBaseURL, "https://api.openai.com/v1")
	if err != nil {
		return nil, err
	}
	client := cfg.Client
	if client == nil {
		client = http.DefaultClient
	}
	return &ResponseServer{
		upstreamBaseURL: upstreamBaseURL,
		apiKey:          cfg.APIKey,
		client:          client,
		tracer:          cfg.Tracer,
		traceErrors:     cfg.TraceErrors,
	}, nil
}

func (server *ResponseServer) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	server.serveProxy(writer, request)
}

func (server *ResponseServer) serveProxy(writer http.ResponseWriter, request *http.Request) {
	log := slog.Default().With("path", request.URL.Path, "method", request.Method)
	log.Debug("代理请求已收到")
	requestBody, err := io.ReadAll(request.Body)
	if err != nil {
		log.Error("读取请求体失败", "error", err)
		http.Error(writer, "读取请求体失败", http.StatusBadRequest)
		return
	}

	targetURL := upstreamURL(server.upstreamBaseURL, request)
	upstreamRequest, err := newUpstreamRequest(request, targetURL, requestBody, server.overrideAuth)
	if err != nil {
		http.Error(writer, "创建上游请求失败", http.StatusBadGateway)
		return
	}

	record := mbtrace.Record{
		HTTPRequest: mbtrace.NewHTTPRequest(request),
		ProxyRequest: ProxyRequest{
			Method:  request.Method,
			URL:     request.URL.RequestURI(),
			Headers: request.Header.Clone(),
			Body:    mbtrace.RawJSONOrString(requestBody),
		},
		UpstreamRequest: ProxyRequest{
			Method:  upstreamRequest.Method,
			URL:     targetURL,
			Headers: upstreamRequest.Header.Clone(),
			Body:    mbtrace.RawJSONOrString(requestBody),
		},
	}

	upstreamResponse, err := server.client.Do(upstreamRequest)
	if err != nil {
		log.Error("上游请求失败", "error", err)
		record.Error = map[string]string{"stage": "upstream_request", "message": err.Error()}
		writeTrace(server.tracer, server.traceErrors, record)
		http.Error(writer, err.Error(), http.StatusBadGateway)
		return
	}
	defer upstreamResponse.Body.Close()

	copyHeaders(writer.Header(), upstreamResponse.Header)
	writer.WriteHeader(upstreamResponse.StatusCode)

	var responseBody bytes.Buffer
	copyErr := copyStreaming(writer, upstreamResponse.Body, &responseBody)
	record.UpstreamResponse = ProxyResponse{
		StatusCode: upstreamResponse.StatusCode,
		Headers:    upstreamResponse.Header.Clone(),
		Body:       mbtrace.RawJSONOrString(responseBody.Bytes()),
	}
	if copyErr != nil {
		log.Error("复制上游响应失败", "error", copyErr)
		record.Error = map[string]string{"stage": "copy_upstream_response", "message": copyErr.Error()}
	}
	log.Info("代理响应", "status", upstreamResponse.StatusCode, "bytes", responseBody.Len())
	writeTrace(server.tracer, server.traceErrors, record)
}

func (server *ResponseServer) overrideAuth(headers http.Header) {
	if server.apiKey == "" {
		return
	}
	headers.Set("Authorization", "Bearer "+server.apiKey)
}
