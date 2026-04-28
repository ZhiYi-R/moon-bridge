package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"moonbridge/internal/extension/codex"
	"moonbridge/internal/extension/visual"
	"moonbridge/internal/extension/websearchinjected"
	"moonbridge/internal/foundation/config"
	"moonbridge/internal/foundation/logger"
	"moonbridge/internal/foundation/openai"
	"moonbridge/internal/foundation/session"
	"moonbridge/internal/protocol/anthropic"
	"moonbridge/internal/protocol/bridge"
	"moonbridge/internal/protocol/cache"
	"moonbridge/internal/service/provider"
	"moonbridge/internal/service/stats"
	mbtrace "moonbridge/internal/service/trace"
)

// Provider defines the upstream interface for creating messages.
type Provider interface {
	CreateMessage(context.Context, anthropic.MessageRequest) (anthropic.MessageResponse, error)
	StreamMessage(context.Context, anthropic.MessageRequest) (anthropic.Stream, error)
}

type Config struct {
	Bridge           *bridge.Bridge
	Provider         Provider
	ProviderMgr      *provider.ProviderManager // optional; used for multi-provider routing
	OpenAIHTTPClient *http.Client
	Tracer           *mbtrace.Tracer
	TraceErrors      io.Writer
	Stats            *stats.SessionStats
	AppConfig        config.Config // full app config for per-provider resolution
}

type Server struct {
	bridge           *bridge.Bridge
	provider         Provider
	providerMgr      *provider.ProviderManager
	openAIHTTP       *http.Client
	tracer           *mbtrace.Tracer
	traceErrors      io.Writer
	stats            *stats.SessionStats
	mux              *http.ServeMux
	sessionsMu       sync.Mutex
	sessions         map[string]serverSession
	sessionPruneStop chan struct{}
	onceClose        sync.Once
	appConfig        config.Config
}

type serverSession struct {
	sess     *session.Session
	lastUsed time.Time
}

const sessionTTL = 24 * time.Hour

func New(cfg Config) *Server {
	server := &Server{
		bridge:           cfg.Bridge,
		provider:         cfg.Provider,
		providerMgr:      cfg.ProviderMgr,
		openAIHTTP:       cfg.OpenAIHTTPClient,
		tracer:           cfg.Tracer,
		traceErrors:      cfg.TraceErrors,
		stats:            cfg.Stats,
		mux:              http.NewServeMux(),
		sessions:         map[string]serverSession{},
		sessionPruneStop: make(chan struct{}),
		appConfig:        cfg.AppConfig,
	}
	server.mux.HandleFunc("/v1/responses", server.handleResponses)
	server.mux.HandleFunc("/responses", server.handleResponses)
	server.mux.HandleFunc("/v1/models", server.handleModels)
	server.mux.HandleFunc("/models", server.handleModels)
	go server.startSessionPruning()
	return server
}

func (server *Server) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if token := server.appConfig.AuthToken; token != "" {
		if !checkAuth(request, token) {
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(writer).Encode(openai.ErrorResponse{Error: openai.ErrorObject{
				Message: "未提供有效的认证令牌，请在 Authorization header 中使用 Bearer 方案",
				Type:    "authentication_error",
				Code:    "invalid_auth",
			}})
			return
		}
	}
	server.mux.ServeHTTP(writer, request)
}

func (server *Server) handleModels(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writeOpenAIError(writer, http.StatusMethodNotAllowed, openai.ErrorResponse{Error: openai.ErrorObject{
			Message: "仅支持 GET 请求",
			Type:    "invalid_request_error",
			Code:    "method_not_allowed",
		}})
		return
	}
	models := server.listModels()
	resp := struct {
		Models []codex.ModelInfo `json:"models"`
	}{
		Models: models,
	}
	writer.Header().Set("Content-Type", "application/json")
	json.NewEncoder(writer).Encode(resp)
}

func (server *Server) listModels() []codex.ModelInfo {
	return codex.BuildModelInfosFromConfig(server.appConfig)
}

func (server *Server) handleResponses(writer http.ResponseWriter, request *http.Request) {
	log := logger.L().With("path", request.URL.Path, "method", request.Method, "remote", request.RemoteAddr)
	log.Debug("收到请求")
	if request.Method != http.MethodPost {
		log.Warn("方法不允许", "method", request.Method)
		writeOpenAIError(writer, http.StatusMethodNotAllowed, openai.ErrorResponse{Error: openai.ErrorObject{
			Message: "方法不允许",
			Type:    "invalid_request_error",
			Code:    "method_not_allowed",
		}})
		return
	}

	sess := server.sessionForRequest(request)

	body, err := io.ReadAll(request.Body)
	record := mbtrace.Record{HTTPRequest: mbtrace.NewHTTPRequest(request), OpenAIRequest: mbtrace.RawJSONOrString(body)}
	if err != nil {
		log.Error("读取请求体失败", "error", err)
		payload := openai.ErrorResponse{Error: openai.ErrorObject{
			Message: "读取请求体失败",
			Type:    "invalid_request_error",
			Code:    "invalid_request_body",
		}}
		record.Error = traceError("read_openai_request", err)
		record.OpenAIResponse = payload
		server.writeTrace(record)
		writeOpenAIError(writer, http.StatusBadRequest, payload)
		return
	}

	var responsesRequest openai.ResponsesRequest
	if err := json.Unmarshal(body, &responsesRequest); err != nil {
		log.Warn("无效的 JSON 请求体", "error", err)
		payload := openai.ErrorResponse{Error: openai.ErrorObject{
			Message: "无效的 JSON 请求体",
			Type:    "invalid_request_error",
			Code:    "invalid_json",
		}}
		record.Error = traceError("decode_openai_request", err)
		record.OpenAIResponse = payload
		server.writeTrace(record)
		writeOpenAIError(writer, http.StatusBadRequest, payload)
		return
	}

	record.Model = responsesRequest.Model
	// Check if this model routes to an OpenAI Responses provider (skip Anthropic conversion).
	providerKey := server.bridge.ProviderFor(responsesRequest.Model)
	if server.providerMgr != nil && server.providerMgr.ProtocolForModel(responsesRequest.Model) == config.ProtocolOpenAIResponse {
		server.handleOpenAIResponse(writer, request, responsesRequest, providerKey, record)
		return
	}

	// Resolve per-provider web search mode.
	reqOpts := server.resolveRequestOptions(responsesRequest.Model, providerKey)

	anthropicRequest, plan, err := server.bridge.ToAnthropic(responsesRequest, sess.ExtensionData, reqOpts)
	conversionContext := server.bridge.ConversionContext(responsesRequest)
	record.AnthropicRequest = anthropicRequest
	if err != nil {
		log.Warn("转换为 Anthropic 格式失败", "error", err)
		status, payload := server.bridge.ErrorResponseForModel(responsesRequest.Model, err)
		record.Error = traceError("convert_to_anthropic", err)
		record.OpenAIResponse = payload
		server.writeTrace(record)
		writeOpenAIError(writer, status, payload)
		return
	}

	// Resolve the provider for this request.
	effectiveProvider := server.resolveProvider(responsesRequest.Model, server.bridge.ProviderFor(responsesRequest.Model))
	if effectiveProvider == nil {
		log.Error("模型无可用提供商", "model", responsesRequest.Model)
		writeOpenAIError(writer, http.StatusBadGateway, openai.ErrorResponse{Error: openai.ErrorObject{
			Message: fmt.Sprintf("no upstream provider configured for model %q", responsesRequest.Model),
			Type:    "server_error",
			Code:    "provider_error",
		}})
		return
	}

	if responsesRequest.Stream {
		log.Debug("处理流式请求", "model", responsesRequest.Model)
		server.handleStream(writer, request, responsesRequest, anthropicRequest, plan, record, conversionContext, sess, effectiveProvider)
		return
	}

	log.Debug("发送非流式请求到提供商", "model", anthropicRequest.Model)
	anthropicResponse, err := effectiveProvider.CreateMessage(request.Context(), anthropicRequest)
	if err != nil {
		status, payload := server.bridge.ErrorResponseForModel(responsesRequest.Model, err)
		errLine := stats.FormatErrorLine(stats.ErrorLineParams{
			RequestModel: responsesRequest.Model,
			ActualModel:  anthropicRequest.Model,
			StatusCode:   status,
			Message:      payload.Error.Message,
		})
		fmt.Fprintln(logger.Output(), errLine)
		log.Error("提供商请求失败", "status", status)
		record.Error = traceError("provider_create_message", err)
		record.OpenAIResponse = payload
		server.writeTrace(record)
		writeOpenAIError(writer, status, payload)
		return
	}

	openAIResponse := server.bridge.FromAnthropicWithPlanAndContext(anthropicResponse, responsesRequest.Model, plan, conversionContext, sess.ExtensionData)
	usage := anthropicResponse.Usage
	if server.stats != nil {
		server.stats.Record(responsesRequest.Model, anthropicRequest.Model, stats.Usage{
			InputTokens:              usage.InputTokens + usage.CacheCreationInputTokens + usage.CacheReadInputTokens,
			OutputTokens:             usage.OutputTokens,
			CacheCreationInputTokens: usage.CacheCreationInputTokens,
			CacheReadInputTokens:     usage.CacheReadInputTokens,
		})
	}
	logUsageLine(responsesRequest.Model, anthropicRequest.Model, stats.Usage{InputTokens: usage.InputTokens + usage.CacheCreationInputTokens + usage.CacheReadInputTokens, OutputTokens: usage.OutputTokens, CacheCreationInputTokens: usage.CacheCreationInputTokens, CacheReadInputTokens: usage.CacheReadInputTokens}, server.stats)
	record.AnthropicResponse = anthropicResponse
	record.OpenAIResponse = openAIResponse
	server.writeTrace(record)
	writeJSON(writer, http.StatusOK, openAIResponse)
}

func (server *Server) handleStream(writer http.ResponseWriter, request *http.Request, responsesRequest openai.ResponsesRequest, anthropicRequest anthropic.MessageRequest, plan cache.CacheCreationPlan, record mbtrace.Record, context codex.ConversionContext, sess *session.Session, provider Provider) {
	log := logger.L().With("model", responsesRequest.Model)
	log.Debug("开始流式传输")
	server.bridge.MarkCacheAttempt(plan)
	stream, err := provider.StreamMessage(request.Context(), anthropicRequest)
	if err != nil {
		status, payload := server.bridge.ErrorResponseForModel(responsesRequest.Model, err)
		errLine := stats.FormatErrorLine(stats.ErrorLineParams{
			RequestModel: responsesRequest.Model,
			ActualModel:  anthropicRequest.Model,
			StatusCode:   status,
			Message:      payload.Error.Message,
		})
		fmt.Fprintln(logger.Output(), errLine)
		log.Error("提供商流式传输失败", "status", status)
		record.Error = traceError("provider_stream_message", err)
		record.OpenAIResponse = payload
		server.writeTrace(record)
		writeOpenAIError(writer, status, payload)
		server.bridge.ResetCacheWarming(plan)
		logger.Flush()
		return
	}
	defer stream.Close()

	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.WriteHeader(http.StatusOK)

	var events []anthropic.StreamEvent
	for {
		event, err := stream.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			events = append(events, anthropic.StreamEvent{Type: "error", Error: &anthropic.ErrorObject{Type: "provider_stream_error", Message: err.Error()}})
			record.Error = traceError("provider_stream_next", err)
			log.Error("流式读取错误", "error", err)
			break
		}
		events = append(events, event)
	}

	openAIEvents := server.bridge.ConvertStreamEventsWithContext(events, responsesRequest.Model, context, sess.ExtensionData, bridge.StreamOptions{
		PersistFinalTextReasoning: hasToolHistory(anthropicRequest.Messages),
	})
	record.AnthropicStreamEvents = events
	record.OpenAIStreamEvents = openAIEvents
	server.writeTrace(record)

	for _, event := range openAIEvents {
		writeSSE(writer, event)
	}
	// Extract usage from message_delta event
	var usage anthropic.Usage
	for _, ev := range events {
		switch {
		case ev.Type == "message_start" && ev.Message != nil:
			// message_start carries cache_creation / cache_read token counts
			usage.InputTokens = ev.Message.Usage.InputTokens
			usage.CacheCreationInputTokens = ev.Message.Usage.CacheCreationInputTokens
			usage.CacheReadInputTokens = ev.Message.Usage.CacheReadInputTokens
		case ev.Type == "message_delta" && ev.Usage != nil:
			usage.OutputTokens = ev.Usage.OutputTokens
			// message_delta may also carry cache fields in some providers
			if ev.Usage.CacheCreationInputTokens > 0 {
				usage.CacheCreationInputTokens = ev.Usage.CacheCreationInputTokens
			}
			if ev.Usage.CacheReadInputTokens > 0 {
				usage.CacheReadInputTokens = ev.Usage.CacheReadInputTokens
			}
		}
	}
	if server.stats != nil {
		server.stats.Record(responsesRequest.Model, anthropicRequest.Model, stats.Usage{
			InputTokens:              usage.InputTokens + usage.CacheCreationInputTokens + usage.CacheReadInputTokens,
			OutputTokens:             usage.OutputTokens,
			CacheCreationInputTokens: usage.CacheCreationInputTokens,
			CacheReadInputTokens:     usage.CacheReadInputTokens,
		})
	}
	logUsageLine(responsesRequest.Model, anthropicRequest.Model, stats.Usage{InputTokens: usage.InputTokens + usage.CacheCreationInputTokens + usage.CacheReadInputTokens, OutputTokens: usage.OutputTokens, CacheCreationInputTokens: usage.CacheCreationInputTokens, CacheReadInputTokens: usage.CacheReadInputTokens}, server.stats)
	// Update cache registry from streaming usage signals.
	server.bridge.UpdateRegistryFromUsage(plan, cache.UsageSignals{
		InputTokens:              usage.InputTokens,
		CacheCreationInputTokens: usage.CacheCreationInputTokens,
		CacheReadInputTokens:     usage.CacheReadInputTokens,
	}, usage.InputTokens)
}

// resolveProvider selects the correct Provider for a given model alias.
// If a ProviderManager is configured, it uses it for routing.
// Otherwise it falls back to the single default provider.

func (server *Server) sessionForRequest(request *http.Request) *session.Session {
	key := sessionKeyFromRequest(request)
	if key == "" {
		sess := session.New()
		sess.InitExtensions(server.bridge.NewExtensionData())
		return sess
	}

	now := time.Now()
	server.sessionsMu.Lock()
	defer server.sessionsMu.Unlock()

	server.pruneSessionsLocked(now)
	if entry, ok := server.sessions[key]; ok {
		entry.lastUsed = now
		server.sessions[key] = entry
		return entry.sess
	}

	sess := session.NewWithID(key)
	sess.InitExtensions(server.bridge.NewExtensionData())
	server.sessions[key] = serverSession{sess: sess, lastUsed: now}
	return sess
}

func (server *Server) pruneSessionsLocked(now time.Time) {
	for key, entry := range server.sessions {
		if now.Sub(entry.lastUsed) > sessionTTL {
			delete(server.sessions, key)
		}
	}
}

func sessionKeyFromRequest(request *http.Request) string {
	if value := strings.TrimSpace(request.Header.Get("Session_id")); value != "" {
		return "session:" + value
	}
	if value := strings.TrimSpace(request.Header.Get("X-Codex-Window-Id")); value != "" {
		return "codex-window:" + value
	}
	return ""
}

func hasToolHistory(messages []anthropic.Message) bool {
	for _, message := range messages {
		for _, block := range message.Content {
			if block.Type == "tool_use" || block.Type == "tool_result" {
				return true
			}
		}
	}
	return false
}

func (server *Server) writeTrace(record mbtrace.Record) {
	if server.tracer == nil || !server.tracer.Enabled() {
		return
	}
	requestNumber := server.tracer.NextRequestNumber()
	if shouldWriteResponseTrace(record) {
		server.writeTraceCategory("Response", requestNumber, mbtrace.Record{
			HTTPRequest:        record.HTTPRequest,
			OpenAIRequest:      record.OpenAIRequest,
			Model:              record.Model,
			OpenAIResponse:     record.OpenAIResponse,
			OpenAIStreamEvents: record.OpenAIStreamEvents,
			Error:              record.Error,
		})
	}
	if shouldWriteAnthropicTrace(record) {
		server.writeTraceCategory("Anthropic", requestNumber, mbtrace.Record{
			HTTPRequest:           record.HTTPRequest,
			AnthropicRequest:      record.AnthropicRequest,
			Model:                 record.Model,
			AnthropicResponse:     record.AnthropicResponse,
			AnthropicStreamEvents: record.AnthropicStreamEvents,
			Error:                 record.Error,
		})
	}
}

func (server *Server) writeTraceCategory(category string, requestNumber uint64, record mbtrace.Record) {
	if _, err := server.tracer.WriteNumbered(category, requestNumber, record); err != nil && server.traceErrors != nil {
		fmt.Fprintf(server.traceErrors, "跟踪 %s 写入失败: %v\n", category, err)
	}
}

func shouldWriteResponseTrace(record mbtrace.Record) bool {
	return record.OpenAIRequest != nil || record.OpenAIResponse != nil || record.OpenAIStreamEvents != nil
}

func shouldWriteAnthropicTrace(record mbtrace.Record) bool {
	return record.AnthropicRequest != nil || record.AnthropicResponse != nil || record.AnthropicStreamEvents != nil
}

func traceError(stage string, err error) map[string]string {
	return map[string]string{"stage": stage, "message": err.Error()}
}

func writeJSON(writer http.ResponseWriter, status int, payload any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(payload)
}

func writeOpenAIError(writer http.ResponseWriter, status int, payload openai.ErrorResponse) {
	writeJSON(writer, status, payload)
}

func writeSSE(writer http.ResponseWriter, event openai.StreamEvent) {
	data, _ := json.Marshal(event.Data)
	_, _ = writer.Write([]byte("event: " + event.Event + "\n"))
	_, _ = writer.Write([]byte("data: " + string(data) + "\n\n"))
	if flusher, ok := writer.(http.Flusher); ok {
		flusher.Flush()
	}
}

// anthropicClientWrapper adapts *anthropic.Client to the Provider interface.
type anthropicClientWrapper struct {
	client *anthropic.Client
}

func (w *anthropicClientWrapper) CreateMessage(ctx context.Context, request anthropic.MessageRequest) (anthropic.MessageResponse, error) {
	return w.client.CreateMessage(ctx, request)
}

func (w *anthropicClientWrapper) StreamMessage(ctx context.Context, request anthropic.MessageRequest) (anthropic.Stream, error) {
	return w.client.StreamMessage(ctx, request)
}

// handleOpenAIResponse proxies a request directly to an OpenAI Responses upstream
// without Anthropic protocol conversion. It handles both streaming and non-streaming.
func (server *Server) handleOpenAIResponse(writer http.ResponseWriter, request *http.Request, responsesRequest openai.ResponsesRequest, providerKey string, record mbtrace.Record) {
	log := logger.L().With("path", request.URL.Path, "method", request.Method)
	if server.providerMgr == nil {
		log.Error("未配置 OpenAI Responses 直通的提供商管理器")
		payload := openai.ErrorResponse{Error: openai.ErrorObject{
			Message: "提供商路由未配置",
			Type:    "server_error",
			Code:    "internal_error",
		}}
		record.Error = map[string]string{"stage": "openai_provider_config", "message": "provider manager not configured"}
		record.OpenAIResponse = payload
		server.writeTrace(record)
		writeOpenAIError(writer, http.StatusBadGateway, payload)
		return
	}

	// Resolve provider key from model alias when Bridge.ProviderFor returned empty.
	resolvedKey := providerKey
	if resolvedKey == "" {
		resolvedKey = server.providerMgr.ProviderKeyForModel(responsesRequest.Model)
	}
	baseURL := server.providerMgr.ProviderBaseURL(resolvedKey)
	apiKey := server.providerMgr.ProviderAPIKey(resolvedKey)
	if baseURL == "" {
		log.Error("OpenAI 提供商缺少 base_url", "provider", providerKey)
		payload := openai.ErrorResponse{Error: openai.ErrorObject{
			Message: "提供商未配置",
			Type:    "server_error",
			Code:    "internal_error",
		}}
		record.Error = map[string]string{"stage": "openai_provider_config", "message": "missing base_url"}
		record.OpenAIResponse = payload
		server.writeTrace(record)
		writeOpenAIError(writer, http.StatusBadGateway, payload)
		return
	}

	// Build upstream URL: baseURL + /v1/responses
	upstreamURL := strings.TrimRight(baseURL, "/")
	if !strings.HasSuffix(upstreamURL, "/v1/responses") && !strings.HasSuffix(upstreamURL, "/responses") {
		upstreamURL += "/v1/responses"
	}

	upstreamRequest := responsesRequest
	upstreamRequest.Model = server.providerMgr.UpstreamModelFor(responsesRequest.Model)

	// Inject web_search tool if enabled for this model.
	if server.providerMgr != nil && server.providerMgr.ResolvedWebSearchForModel(responsesRequest.Model) == "enabled" {
		upstreamRequest.Tools = InjectWebSearchTool(upstreamRequest.Tools)
	}

	body, err := json.Marshal(upstreamRequest)
	if err != nil {
		log.Error("序列化请求失败", "error", err)
		payload := openai.ErrorResponse{Error: openai.ErrorObject{
			Message: "内部错误",
			Type:    "server_error",
			Code:    "internal_error",
		}}
		record.Error = traceError("encode_openai_upstream_request", err)
		record.OpenAIResponse = payload
		server.writeTrace(record)
		writeOpenAIError(writer, http.StatusInternalServerError, payload)
		return
	}

	// Create upstream request
	upstreamReq, err := http.NewRequestWithContext(request.Context(), http.MethodPost, upstreamURL, bytes.NewReader(body))
	if err != nil {
		log.Error("创建上游请求失败", "error", err)
		payload := openai.ErrorResponse{Error: openai.ErrorObject{
			Message: "上游请求失败",
			Type:    "server_error",
			Code:    "internal_error",
		}}
		record.Error = traceError("create_openai_upstream_request", err)
		record.OpenAIResponse = payload
		server.writeTrace(record)
		writeOpenAIError(writer, http.StatusBadGateway, payload)
		return
	}
	upstreamReq.Header.Set("Content-Type", "application/json")
	upstreamReq.Header.Set("Authorization", "Bearer "+apiKey)

	client := server.openAIHTTP
	if client == nil {
		client = &http.Client{Timeout: 0} // no timeout for streaming
	}
	upstreamResp, err := client.Do(upstreamReq)
	if err != nil {
		errLine := stats.FormatErrorLine(stats.ErrorLineParams{
			RequestModel: responsesRequest.Model,
			ActualModel:  upstreamRequest.Model,
			StatusCode:   http.StatusBadGateway,
			Message:      err.Error(),
		})
		fmt.Fprintln(logger.Output(), errLine)
		log.Error("OpenAI 上游请求失败", "status", http.StatusBadGateway)
		payload := openai.ErrorResponse{Error: openai.ErrorObject{
			Message: err.Error(),
			Type:    "server_error",
			Code:    "provider_error",
		}}
		record.Error = traceError("openai_upstream", err)
		record.OpenAIResponse = payload
		server.writeTrace(record)
		writeOpenAIError(writer, http.StatusBadGateway, payload)
		return
	}
	defer upstreamResp.Body.Close()

	// Copy response headers and status
	for key, values := range upstreamResp.Header {
		for _, v := range values {
			writer.Header().Add(key, v)
		}
	}
	writer.WriteHeader(upstreamResp.StatusCode)

	traceEnabled := server.tracer != nil && server.tracer.Enabled()
	statsEnabled := server.stats != nil && upstreamResp.StatusCode >= 200 && upstreamResp.StatusCode <= 299
	shouldCapture := traceEnabled || statsEnabled

	var captured bytes.Buffer
	target := io.Writer(writer)
	if shouldCapture {
		target = io.MultiWriter(writer, &captured)
	}
	if _, err := io.Copy(target, upstreamResp.Body); err != nil {
		log.Error("复制上游响应失败", "error", err)
		return
	}

	if traceEnabled {
		record.OpenAIResponse = mbtrace.RawJSONOrString(captured.Bytes())
		server.writeTrace(record)
	}
	if statsEnabled {
		if usage, ok := openAIUsageFromResponse(captured.Bytes(), responsesRequest.Stream); ok {
			server.stats.Record(responsesRequest.Model, upstreamRequest.Model, usage)
			logUsageLine(responsesRequest.Model, upstreamRequest.Model, usage, server.stats)
		}
	}
}

func logUsageLine(requestModel, actualModel string, usage stats.Usage, sessionStats *stats.SessionStats) {
	var requestCost float64
	var summary stats.Summary
	if sessionStats != nil {
		requestCost = sessionStats.ComputeCost(requestModel, usage)
		summary = sessionStats.Summary()
	}
	fmt.Fprintln(logger.Output(), stats.FormatUsageLine(stats.UsageLineParams{
		RequestModel:   requestModel,
		ActualModel:    actualModel,
		Usage:          usage,
		RequestCost:    requestCost,
		TotalCost:      summary.TotalCost,
		CacheHitRate:   summary.CacheHitRate,
		CacheWriteRate: summary.CacheWriteRate,
	}))
	if sessionStats != nil {
		fmt.Fprintln(logger.Output(), "---")
		stats.WriteSummary(logger.Output(), summary)
	}
	logger.Flush()
}

func openAIUsageFromResponse(data []byte, stream bool) (stats.Usage, bool) {
	if len(data) == 0 {
		return stats.Usage{}, false
	}
	if stream {
		return openAIUsageFromSSE(data)
	}
	var payload struct {
		Usage openai.Usage `json:"usage"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return stats.Usage{}, false
	}
	return statsUsageFromOpenAIUsage(payload.Usage)
}

func openAIUsageFromSSE(data []byte) (stats.Usage, bool) {
	var last stats.Usage
	found := false
	for _, event := range strings.Split(string(data), "\n\n") {
		var payload strings.Builder
		for _, line := range strings.Split(event, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "data:") {
				part := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
				if part == "" || part == "[DONE]" {
					continue
				}
				if payload.Len() > 0 {
					payload.WriteByte('\n')
				}
				payload.WriteString(part)
			}
		}
		if payload.Len() == 0 {
			continue
		}
		var envelope struct {
			Usage    openai.Usage `json:"usage"`
			Response struct {
				Usage openai.Usage `json:"usage"`
			} `json:"response"`
		}
		if err := json.Unmarshal([]byte(payload.String()), &envelope); err != nil {
			continue
		}
		if usage, ok := statsUsageFromOpenAIUsage(envelope.Response.Usage); ok {
			last = usage
			found = true
			continue
		}
		if usage, ok := statsUsageFromOpenAIUsage(envelope.Usage); ok {
			last = usage
			found = true
		}
	}
	return last, found
}

func statsUsageFromOpenAIUsage(usage openai.Usage) (stats.Usage, bool) {
	if usage.InputTokens == 0 && usage.OutputTokens == 0 && usage.InputTokensDetails.CachedTokens == 0 {
		return stats.Usage{}, false
	}
	cacheRead := usage.InputTokensDetails.CachedTokens
	freshInput := usage.InputTokens - cacheRead
	if freshInput < 0 {
		freshInput = 0
	}
	return stats.Usage{
		InputTokens:          usage.InputTokens,
		OutputTokens:         usage.OutputTokens,
		CacheReadInputTokens: cacheRead,
	}, true
}
func (server *Server) resolveProvider(modelAlias string, providerKey string) Provider {
	if server.providerMgr != nil {
		// First, try routing by model alias.
		if _, client, err := server.providerMgr.ClientFor(modelAlias); err == nil && client != nil {
			return server.maybeWrapProvider(client, modelAlias)
		}
		// Fallback: try providerKey directly.
		if providerKey != "" {
			if client, err := server.providerMgr.ClientForKey(providerKey); err == nil && client != nil {
				return server.maybeWrapProvider(client, modelAlias)
			}
		}
		// Last resort: try any available provider.
		for _, k := range server.providerMgr.ProviderKeys() {
			if c, err := server.providerMgr.ClientForKey(k); err == nil && c != nil {
				return server.maybeWrapProvider(c, modelAlias)
			}
		}
	}
	if server.provider != nil {
		return server.provider
	}
	return nil
}

// maybeWrapProvider wraps a client with enabled server-side extension
// orchestrators for the requested model.
func (server *Server) maybeWrapProvider(client *anthropic.Client, modelAlias string) Provider {
	var wrapped Provider = &anthropicClientWrapper{client: client}
	if server.providerMgr == nil {
		return server.maybeWrapVisual(wrapped, modelAlias)
	}
	resolved := server.providerMgr.ResolvedWebSearchForModel(modelAlias)
	if resolved == "injected" {
		tavilyKey := server.appConfig.WebSearchTavilyKeyForModel(modelAlias)
		firecrawlKey := server.appConfig.WebSearchFirecrawlKeyForModel(modelAlias)
		maxRounds := server.appConfig.WebSearchMaxRoundsForModel(modelAlias)
		logger.L().Debug("包装注入式搜索编排器", "model", modelAlias)
		wrapped = websearchinjected.WrapProvider(client, tavilyKey, firecrawlKey, maxRounds)
	}
	return server.maybeWrapVisual(wrapped, modelAlias)
}

func (server *Server) maybeWrapVisual(provider Provider, modelAlias string) Provider {
	visualCfg, ok := visual.ConfigForModel(server.appConfig, modelAlias)
	if !ok {
		return provider
	}
	visualProvider := server.visualProvider(visualCfg)
	logger.L().Debug("Wrapping Visual orchestrator", "model", modelAlias, "visual_model", visualCfg.Model)
	return visual.WrapProvider(provider, visualProvider, visualCfg.Model, visualCfg.MaxRounds, visualCfg.MaxTokens)
}

func (server *Server) visualProvider(cfg visual.Config) Provider {
	if server.providerMgr != nil && cfg.Provider != "" {
		client, err := server.providerMgr.ClientForKey(cfg.Provider)
		if err != nil {
			logger.L().Warn("Visual provider unavailable", "provider", cfg.Provider, "error", err)
			return nil
		}
		return &anthropicClientWrapper{client: client}
	}
	return nil
}

// resolveRequestOptions builds per-request bridge options based on the provider's
// resolved web search support. Uses model-level resolution.
func (server *Server) resolveRequestOptions(modelAlias string, providerKey string) bridge.RequestOptions {
	if server.providerMgr == nil {
		return bridge.RequestOptions{}
	}
	wsMode := server.providerMgr.ResolvedWebSearchForModel(modelAlias)
	if wsMode == "" {
		return bridge.RequestOptions{}
	}
	return bridge.RequestOptions{
		WebSearchMode:    wsMode,
		WebSearchMaxUses: server.appConfig.WebSearchMaxUsesForModel(modelAlias),
		FirecrawlAPIKey:  server.appConfig.WebSearchFirecrawlKeyForModel(modelAlias),
	}
}

// injectWebSearchTool adds a native web_search tool to the tools array if
// one is not already present. OpenAI Responses API supports this as a
// built-in tool type.
func InjectWebSearchTool(tools []openai.Tool) []openai.Tool {
	for _, t := range tools {
		if t.Type == "web_search" {
			return tools // already present
		}
	}
	if tools == nil {
		tools = make([]openai.Tool, 0, 1)
	}
	return append(tools, openai.Tool{Type: "web_search"})
}

// startSessionPruning runs a background goroutine that periodically
// cleans up expired sessions so they don't leak memory over time.
func (server *Server) startSessionPruning() {
	ticker := time.NewTicker(time.Hour) // prune every hour; sessionTTL is 24h
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			server.pruneSessions()
		case <-server.sessionPruneStop:
			return
		}
	}
}

// pruneSessions locks and prunes expired sessions.
func (server *Server) pruneSessions() {
	now := time.Now()
	server.sessionsMu.Lock()
	defer server.sessionsMu.Unlock()
	for key, entry := range server.sessions {
		if now.Sub(entry.lastUsed) > sessionTTL {
			delete(server.sessions, key)
		}
	}
}

// Close stops the background session pruning goroutine.
func (server *Server) Close() error {
	server.onceClose.Do(func() {
		close(server.sessionPruneStop)
	})
	return nil
}

func checkAuth(r *http.Request, expectedToken string) bool {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return false
	}
	return strings.TrimSpace(auth[7:]) == expectedToken
}
