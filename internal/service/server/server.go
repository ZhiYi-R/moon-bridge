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

	"log/slog"
	"moonbridge/internal/extension/codex"
	"moonbridge/internal/extension/plugin"
	"moonbridge/internal/extension/visual"
	"moonbridge/internal/extension/websearchinjected"
	"moonbridge/internal/foundation/config"
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
	PluginRegistry   *plugin.Registry
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
	pluginRegistry   *plugin.Registry
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
		pluginRegistry:   cfg.PluginRegistry,
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
	server.registerPluginRoutes()
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

// onRequestCompleted dispatches a RequestCompletionHook event to all enabled
// plugins. No-op when the registry is nil or no plugins implement the hook.

// Only called after the request model is known (JSON parse succeeded).
// Early errors (bad method, read failure, decode failure) are not recorded.
func (server *Server) onRequestCompleted(model, actualModel string, startTime time.Time, usage plugin.RequestUsage, cost float64, status, errMsg string) {
	if server.pluginRegistry == nil {
		return
	}
	inputTokens := usage.NormalizedInputTokens
	outputTokens := usage.NormalizedOutputTokens
	cacheCreation := usage.NormalizedCacheCreation
	cacheRead := usage.NormalizedCacheRead
	server.pluginRegistry.OnRequestCompleted(
		&plugin.RequestContext{ModelAlias: model},
		plugin.RequestResult{
			Model:         model,
			ActualModel:   actualModel,
			InputTokens:   inputTokens,
			OutputTokens:  outputTokens,
			CacheCreation: cacheCreation,
			CacheRead:     cacheRead,
			Cost:          cost,
			Duration:      time.Since(startTime),
			Status:        status,
			ErrorMessage:  errMsg,
			Usage:         usage,
		},
	)
}

// registerPluginRoutes gives each RouteRegistrar plugin the opportunity to
// mount HTTP handlers on the server mux.
func (server *Server) registerPluginRoutes() {
	if server.pluginRegistry == nil {
		return
	}
	server.pluginRegistry.RegisterRoutes(func(pattern string, handler http.Handler) {
		server.mux.Handle(pattern, handler)
	})
}

func usageFromAnthropic(protocol string, source string, usage anthropic.Usage, inputIncludesCache bool) plugin.RequestUsage {
	raw := mustMarshalJSON(usage)
	normalizedInputTokens := anthropicNormalizedInputTokens(usage, inputIncludesCache)
	return plugin.RequestUsage{
		Protocol:    protocol,
		UsageSource: source,

		RawInputTokens:   usage.InputTokens,
		RawOutputTokens:  usage.OutputTokens,
		RawCacheCreation: usage.CacheCreationInputTokens,
		RawCacheRead:     usage.CacheReadInputTokens,

		NormalizedInputTokens:   normalizedInputTokens,
		NormalizedOutputTokens:  usage.OutputTokens,
		NormalizedCacheCreation: usage.CacheCreationInputTokens,
		NormalizedCacheRead:     usage.CacheReadInputTokens,

		RawUsageJSON: raw,
	}
}

func anthropicUsageFromStreamEvents(events []anthropic.StreamEvent) (anthropic.Usage, stats.BillingUsage, bool) {
	var usage anthropic.Usage
	var billing stats.BillingUsage
	inputIncludesCache := false
	for _, ev := range events {
		switch {
		case ev.Type == "message_start" && ev.Message != nil:
			if ev.Message.Usage.InputTokens > 0 {
				billing.FreshInputTokens = ev.Message.Usage.InputTokens
				billing.ProviderInputTokens = ev.Message.Usage.InputTokens
			}
			if ev.Message.Usage.OutputTokens > 0 {
				billing.OutputTokens = ev.Message.Usage.OutputTokens
			}
			if ev.Message.Usage.CacheCreationInputTokens > 0 {
				billing.CacheCreationInputTokens = ev.Message.Usage.CacheCreationInputTokens
			}
			if ev.Message.Usage.CacheReadInputTokens > 0 {
				billing.CacheReadInputTokens = ev.Message.Usage.CacheReadInputTokens
			}
			usage = mergeAnthropicUsage(usage, ev.Message.Usage)
		case ev.Type == "message_delta" && ev.Usage != nil:
			if streamInputIncludesCache(usage, *ev.Usage) {
				inputIncludesCache = true
				billing.FreshInputTokens = ev.Usage.InputTokens
				billing.ProviderInputTokens = usage.InputTokens
			} else if ev.Usage.InputTokens > 0 {
				billing.FreshInputTokens = ev.Usage.InputTokens
				billing.ProviderInputTokens = ev.Usage.InputTokens
			}
			if ev.Usage.OutputTokens > 0 {
				billing.OutputTokens = ev.Usage.OutputTokens
			}
			if ev.Usage.CacheCreationInputTokens > 0 {
				billing.CacheCreationInputTokens = ev.Usage.CacheCreationInputTokens
			}
			if ev.Usage.CacheReadInputTokens > 0 {
				billing.CacheReadInputTokens = ev.Usage.CacheReadInputTokens
			}
			usage = mergeAnthropicUsage(usage, *ev.Usage)
		}
	}
	if billing.ProviderInputTokens == 0 {
		billing.ProviderInputTokens = billing.InputTokens()
	}
	return usage, billing, inputIncludesCache
}

func mergeAnthropicUsage(current anthropic.Usage, updated anthropic.Usage) anthropic.Usage {
	if updated.InputTokens > 0 {
		if streamInputIncludesCache(current, updated) {
			// Some providers put total input on message_start, then fresh/cache
			// split on message_delta. Keep the total input while merging cache fields.
		} else {
			current.InputTokens = updated.InputTokens
		}
	}
	if updated.OutputTokens > 0 {
		current.OutputTokens = updated.OutputTokens
	}
	if updated.CacheCreationInputTokens > 0 {
		current.CacheCreationInputTokens = updated.CacheCreationInputTokens
	}
	if updated.CacheReadInputTokens > 0 {
		current.CacheReadInputTokens = updated.CacheReadInputTokens
	}
	return current
}

func streamInputIncludesCache(current anthropic.Usage, updated anthropic.Usage) bool {
	return updated.InputTokens > 0 &&
		current.InputTokens > updated.InputTokens &&
		current.CacheCreationInputTokens == 0 &&
		current.CacheReadInputTokens == 0 &&
		(updated.CacheCreationInputTokens > 0 || updated.CacheReadInputTokens > 0)
}

func statsUsageFromAnthropic(usage anthropic.Usage, inputIncludesCache bool) stats.Usage {
	return stats.Usage{
		InputTokens:              anthropicNormalizedInputTokens(usage, inputIncludesCache),
		OutputTokens:             usage.OutputTokens,
		CacheCreationInputTokens: usage.CacheCreationInputTokens,
		CacheReadInputTokens:     usage.CacheReadInputTokens,
	}
}

func billingUsageFromAnthropic(usage anthropic.Usage) stats.BillingUsage {
	return stats.BillingUsage{
		FreshInputTokens:         usage.InputTokens,
		OutputTokens:             usage.OutputTokens,
		CacheCreationInputTokens: usage.CacheCreationInputTokens,
		CacheReadInputTokens:     usage.CacheReadInputTokens,
		ProviderInputTokens:      usage.InputTokens + usage.CacheCreationInputTokens + usage.CacheReadInputTokens,
	}
}

func anthropicNormalizedInputTokens(usage anthropic.Usage, inputIncludesCache bool) int {
	if inputIncludesCache {
		return usage.InputTokens
	}
	return usage.InputTokens + usage.CacheCreationInputTokens + usage.CacheReadInputTokens
}

func usageFromStats(protocol string, source string, usage stats.Usage, rawUsage openai.Usage) plugin.RequestUsage {
	return plugin.RequestUsage{
		Protocol:    protocol,
		UsageSource: source,

		RawInputTokens:   usage.InputTokens,
		RawOutputTokens:  usage.OutputTokens,
		RawCacheCreation: usage.CacheCreationInputTokens,
		RawCacheRead:     usage.CacheReadInputTokens,

		NormalizedInputTokens:   usage.InputTokens,
		NormalizedOutputTokens:  usage.OutputTokens,
		NormalizedCacheCreation: usage.CacheCreationInputTokens,
		NormalizedCacheRead:     usage.CacheReadInputTokens,

		RawUsageJSON: mustMarshalJSON(rawUsage),
	}
}

func zeroUsage(protocol string, source string) plugin.RequestUsage {
	return plugin.RequestUsage{Protocol: protocol, UsageSource: source}
}

func mustMarshalJSON(value any) json.RawMessage {
	data, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	return data
}

func (server *Server) handleResponses(writer http.ResponseWriter, request *http.Request) {
	log := slog.Default().With("path", request.URL.Path, "method", request.Method, "remote", request.RemoteAddr)
	log.Debug("收到请求")
	requestStart := time.Now()
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
	// If the model has no route and is not a direct provider/model reference, reject early.
	if providerKey == "" {
		log.Warn("请求了未知模型", "model", responsesRequest.Model)
		payload := openai.ErrorResponse{Error: openai.ErrorObject{
			Message: fmt.Sprintf("unknown model: %q", responsesRequest.Model),
			Type:    "invalid_request_error",
			Code:    "model_not_found",
		}}
		record.Error = traceError("model_not_found", fmt.Errorf("model %q not found", responsesRequest.Model))
		record.OpenAIResponse = payload
		server.writeTrace(record)
		writeOpenAIError(writer, http.StatusNotFound, payload)
		return
	}
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
		server.onRequestCompleted(
			responsesRequest.Model, "", requestStart,
			zeroUsage("anthropic", "none"), 0, "error", payload.Error.Message,
		)
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
		server.onRequestCompleted(
			responsesRequest.Model, "", requestStart,
			zeroUsage("anthropic", "none"), 0, "error", fmt.Sprintf("no upstream provider: %s", responsesRequest.Model),
		)
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
		log.Error("请求失败",
			"request_model", responsesRequest.Model,
			"actual_model", anthropicRequest.Model,
			"status_code", status,
			"error", payload.Error.Message,
			"stage", "provider_create_message",
		)
		record.Error = traceError("provider_create_message", err)
		record.OpenAIResponse = payload
		server.writeTrace(record)
		writeOpenAIError(writer, status, payload)
		server.onRequestCompleted(
			responsesRequest.Model, anthropicRequest.Model, requestStart,
			zeroUsage("anthropic", "none"), 0, "error", payload.Error.Message,
		)
		return
	}

	openAIResponse := server.bridge.FromAnthropicWithPlanAndContext(anthropicResponse, responsesRequest.Model, plan, conversionContext, sess.ExtensionData)
	usage := anthropicResponse.Usage
	billingUsage := billingUsageFromAnthropic(usage)
	if server.stats != nil {
		server.stats.RecordBilling(responsesRequest.Model, anthropicRequest.Model, billingUsage)
	}
	logBillingUsageLine(responsesRequest.Model, anthropicRequest.Model, billingUsage, server.stats)
	record.AnthropicResponse = anthropicResponse
	record.OpenAIResponse = openAIResponse
	server.writeTrace(record)
	writeJSON(writer, http.StatusOK, openAIResponse)
	completionUsage := usageFromAnthropic("anthropic", "anthropic_response", usage, false)
	server.onRequestCompleted(
		responsesRequest.Model, anthropicRequest.Model, requestStart,
		completionUsage,
		func() float64 {
			if server.stats == nil {
				return 0
			}
			return server.stats.ComputeBillingCost(responsesRequest.Model, billingUsage)
		}(),
		"success", "",
	)
}

func (server *Server) handleStream(writer http.ResponseWriter, request *http.Request, responsesRequest openai.ResponsesRequest, anthropicRequest anthropic.MessageRequest, plan cache.CacheCreationPlan, record mbtrace.Record, context codex.ConversionContext, sess *session.Session, provider Provider) {
	log := slog.Default().With("model", responsesRequest.Model)
	log.Debug("开始流式传输")
	streamStart := time.Now()
	server.bridge.MarkCacheAttempt(plan)
	stream, err := provider.StreamMessage(request.Context(), anthropicRequest)
	if err != nil {
		status, payload := server.bridge.ErrorResponseForModel(responsesRequest.Model, err)
		log.Error("流式传输失败",
			"request_model", responsesRequest.Model,
			"actual_model", anthropicRequest.Model,
			"status_code", status,
			"error", payload.Error.Message,
			"stage", "provider_stream_message",
		)
		record.Error = traceError("provider_stream_message", err)
		record.OpenAIResponse = payload
		server.writeTrace(record)
		writeOpenAIError(writer, status, payload)
		server.onRequestCompleted(
			responsesRequest.Model, anthropicRequest.Model, streamStart,
			zeroUsage("anthropic", "none"), 0, "error", payload.Error.Message,
		)
		server.bridge.ResetCacheWarming(plan)
		return
	}
	defer stream.Close()

	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.WriteHeader(http.StatusOK)

	var events []anthropic.StreamEvent
	var streamErr string
	for {
		event, err := stream.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			events = append(events, anthropic.StreamEvent{Type: "error", Error: &anthropic.ErrorObject{Type: "provider_stream_error", Message: err.Error()}})
			record.Error = traceError("provider_stream_next", err)
			log.Error("流式读取错误", "error", err)
			streamErr = err.Error()
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
	usage, billingUsage, inputIncludesCache := anthropicUsageFromStreamEvents(events)
	if server.stats != nil {
		server.stats.RecordBilling(responsesRequest.Model, anthropicRequest.Model, billingUsage)
	}
	logBillingUsageLine(responsesRequest.Model, anthropicRequest.Model, billingUsage, server.stats)
	// Update cache registry from streaming usage signals.
	server.bridge.UpdateRegistryFromUsage(plan, cache.UsageSignals{
		InputTokens:              usage.InputTokens,
		CacheCreationInputTokens: usage.CacheCreationInputTokens,
		CacheReadInputTokens:     usage.CacheReadInputTokens,
	}, usage.InputTokens)
	completionUsage := usageFromAnthropic("anthropic", "anthropic_stream", usage, inputIncludesCache)
	server.onRequestCompleted(
		responsesRequest.Model, anthropicRequest.Model, streamStart,
		completionUsage,
		func() float64 {
			if server.stats == nil {
				return 0
			}
			return server.stats.ComputeBillingCost(responsesRequest.Model, billingUsage)
		}(),
		func() string {
			if streamErr != "" {
				return "error"
			}
			return "success"
		}(),
		streamErr,
	)
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
	proxyStart := time.Now()
	var hookErr string
	defer func() {
		if hookErr != "" {
			server.onRequestCompleted(
				responsesRequest.Model, "", proxyStart,
				zeroUsage(config.ProtocolOpenAIResponse, "none"), 0, "error", hookErr,
			)
		}
	}()
	log := slog.Default().With("path", request.URL.Path, "method", request.Method)
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
		hookErr = "provider manager not configured"
		return
	}

	// Resolve provider key from model alias when Bridge.ProviderFor returned empty.
	// providerKey is guaranteed non-empty by the caller (handleResponses).

	baseURL := server.providerMgr.ProviderBaseURL(providerKey)
	apiKey := server.providerMgr.ProviderAPIKey(providerKey)
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
		hookErr = "missing base_url"
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
		hookErr = "encode upstream request"
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
		hookErr = "create upstream request"
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
		log.Error("OpenAI 上游请求失败",
			"request_model", responsesRequest.Model,
			"actual_model", upstreamRequest.Model,
			"status_code", http.StatusBadGateway,
			"error", err.Error(),
			"stage", "openai_upstream",
		)
		payload := openai.ErrorResponse{Error: openai.ErrorObject{
			Message: err.Error(),
			Type:    "server_error",
			Code:    "provider_error",
		}}
		hookErr = err.Error()
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
	usageEnabled := upstreamResp.StatusCode >= 200 && upstreamResp.StatusCode <= 299 && (server.stats != nil || server.pluginRegistry != nil)
	shouldCapture := traceEnabled || usageEnabled

	var captured bytes.Buffer
	target := io.Writer(writer)
	if shouldCapture {
		target = io.MultiWriter(writer, &captured)
	}
	if _, err := io.Copy(target, upstreamResp.Body); err != nil {
		hookErr = "copy upstream response"
		log.Error("复制上游响应失败", "error", err)
		return
	}

	if traceEnabled {
		record.OpenAIResponse = mbtrace.RawJSONOrString(captured.Bytes())
		server.writeTrace(record)
	}

	// Capture usage for metrics recording, even when stats recording is disabled.
	var billingUsage stats.BillingUsage
	var metricTelemetry plugin.RequestUsage
	if usageEnabled {
		if u, raw, source, ok := openAIUsageFromResponse(captured.Bytes(), responsesRequest.Stream); ok {
			billingUsage = u.BillingUsage()
			metricTelemetry = usageFromStats(config.ProtocolOpenAIResponse, source, u, raw)
			if server.stats != nil {
				server.stats.RecordBilling(responsesRequest.Model, upstreamRequest.Model, billingUsage)
				logBillingUsageLine(responsesRequest.Model, upstreamRequest.Model, billingUsage, server.stats)
			}
		}
	}
	if metricTelemetry.Protocol == "" {
		metricTelemetry = zeroUsage(config.ProtocolOpenAIResponse, "none")
	}

	// Record metrics via plugin hooks.
	status := "success"
	errMsg := ""
	if upstreamResp.StatusCode < 200 || upstreamResp.StatusCode >= 300 {
		status = "error"
		errMsg = fmt.Sprintf("HTTP %d", upstreamResp.StatusCode)
	}
	cost := float64(0)
	if server.stats != nil {
		cost = server.stats.ComputeBillingCost(responsesRequest.Model, billingUsage)
	}
	server.onRequestCompleted(
		responsesRequest.Model, upstreamRequest.Model, proxyStart,
		metricTelemetry,
		cost, status, errMsg,
	)

}

func logUsageLine(requestModel, actualModel string, usage stats.Usage, sessionStats *stats.SessionStats) {
	logBillingUsageLine(requestModel, actualModel, usage.BillingUsage(), sessionStats)
}

func logBillingUsageLine(requestModel, actualModel string, usage stats.BillingUsage, sessionStats *stats.SessionStats) {
	var requestCost float64
	var summary stats.Summary
	if sessionStats != nil {
		requestCost = sessionStats.ComputeBillingCost(requestModel, usage)
		summary = sessionStats.Summary()
	}
	rwRatio := stats.BillingCacheRWRatio(usage)
	slog.Info("请求完成",
		"request_model", requestModel,
		"actual_model", actualModel,
		"input_fresh", usage.FreshInputTokens,
		"input_cache_read", usage.CacheReadInputTokens,
		"input_cache_write", usage.CacheCreationInputTokens,
		"output_tokens", usage.OutputTokens,
		"request_cost", requestCost,
		"total_cost", summary.TotalCost,
		"cache_hit_rate", summary.CacheHitRate,
		"cache_write_rate", summary.CacheWriteRate,
		"cache_rw_ratio", rwRatio,
	)
}

func openAIUsageFromResponse(data []byte, stream bool) (stats.Usage, openai.Usage, string, bool) {
	if len(data) == 0 {
		return stats.Usage{}, openai.Usage{}, "", false
	}
	if stream {
		return openAIUsageFromSSE(data)
	}
	var payload struct {
		Usage openai.Usage `json:"usage"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return stats.Usage{}, openai.Usage{}, "", false
	}
	usage, ok := statsUsageFromOpenAIUsage(payload.Usage)
	return usage, payload.Usage, "openai_response", ok
}

func openAIUsageFromSSE(data []byte) (stats.Usage, openai.Usage, string, bool) {
	var last stats.Usage
	var lastRaw openai.Usage
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
			lastRaw = envelope.Response.Usage
			found = true
			continue
		}
		if usage, ok := statsUsageFromOpenAIUsage(envelope.Usage); ok {
			last = usage
			lastRaw = envelope.Usage
			found = true
		}
	}
	return last, lastRaw, "openai_sse", found
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
		slog.Default().Debug("包装注入式搜索编排器", "model", modelAlias)
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
	slog.Default().Debug("Wrapping Visual orchestrator", "model", modelAlias, "visual_model", visualCfg.Model)
	return visual.WrapProvider(provider, visualProvider, visualCfg.Model, visualCfg.MaxRounds, visualCfg.MaxTokens)
}

func (server *Server) visualProvider(cfg visual.Config) Provider {
	if server.providerMgr != nil && cfg.Provider != "" {
		client, err := server.providerMgr.ClientForKey(cfg.Provider)
		if err != nil {
			slog.Default().Warn("Visual provider unavailable", "provider", cfg.Provider, "error", err)
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
