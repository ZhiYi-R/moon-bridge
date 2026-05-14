package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"moonbridge/internal/config"
	deepseekv4 "moonbridge/internal/extension/deepseek_v4"
	"moonbridge/internal/extension/plugin"
	visualpkg "moonbridge/internal/extension/visual"
	"moonbridge/internal/extension/websearchinjected"
	"moonbridge/internal/format"
	"moonbridge/internal/protocol/anthropic"
	"moonbridge/internal/protocol/chat"
	"moonbridge/internal/protocol/google"
	openai "moonbridge/internal/protocol/openai"
	"moonbridge/internal/service/provider"
	"moonbridge/internal/service/stats"
	mbtrace "moonbridge/internal/service/trace"
	"moonbridge/internal/session"
)

// ============================================================================
// Adapter Dispatch — experimental dual-bridge adapter path
// ============================================================================
//
// handleWithAdapters implements the experimental adapter dispatch path:
//
//   OpenAI ResponsesRequest
//     → ClientAdapter.ToCoreRequest()       → format.CoreRequest
//     → ProviderAdapter.FromCoreRequest()   → anthropic.MessageRequest (with cache injection)
//     → upstream provider.CreateMessage()   → anthropic.MessageResponse
//     → ProviderAdapter.ToCoreResponse()    → format.CoreResponse
//     → ClientAdapter.FromCoreResponse()    → openai.Response
//
// Streaming path:
//   OpenAI ResponsesRequest (stream=true)
//     → ClientAdapter.ToCoreRequest()       → format.CoreRequest
//     → ProviderAdapter.FromCoreRequest()   → anthropic.MessageRequest (with cache injection)
//     → upstream provider.StreamMessage()   → anthropic.Stream
//     → ProviderStreamAdapter.ToCoreStream()→ <-chan format.CoreStreamEvent
//     → ClientStreamAdapter.FromCoreStream()→ <-chan openai.StreamEvent
//     → write SSE events to ResponseWriter

// handleWithAdapters dispatches a request through the adapter path.
// Falls back to error when the required adapter is not found in the registry.
func (s *Server) handleWithAdapters(
	w http.ResponseWriter,
	r *http.Request,
	openAIReq openai.ResponsesRequest,
	route *provider.ResolvedRoute,
) {
	ctx := r.Context()
	log := slog.Default().With("model", openAIReq.Model, "path", "adapter")

	// Defense-in-depth: ensure model is non-empty.
	if openAIReq.Model == "" {
		log.Warn("adapter path: empty model")
		payload := openai.ErrorResponse{
			Error: openai.ErrorObject{
				Message: "model is required",
				Type:    "invalid_request_error",
				Code:    "missing_model",
			},
		}
		writeOpenAIError(w, http.StatusBadRequest, payload)
		return
	}

	// Get or create session for this request.
	requestStart := time.Now()
	sess := s.sessionForRequest(r)
	_ = sess

	// Initialize trace record.
	bodyBytes, _ := json.Marshal(openAIReq)
	record := mbtrace.Record{
		HTTPRequest:   mbtrace.NewHTTPRequest(r),
		OpenAIRequest: mbtrace.RawJSONOrString(bodyBytes),
		Model:         openAIReq.Model,
	}
	defer func() {
		s.writeTrace(record)
	}()

	// ------------------------------------------------------------------
	// 1. Resolve inbound client adapter (always openai-response).
	// ------------------------------------------------------------------
	client, ok := s.adapterRegistry.GetClient(config.ProtocolOpenAIResponse)
	if !ok {
		log.Warn("adapter path: no client adapter for openai-response")
		payload := openai.ErrorResponse{
			Error: openai.ErrorObject{
				Message: "adapter path precondition failed: no fallback available",
				Type:    "server_error",
				Code:    "adapter_fallback",
			},
		}
		record.Error = traceError("client_adapter", fmt.Errorf("no client adapter for openai-response"))
		record.OpenAIResponse = payload
		writeOpenAIError(w, http.StatusInternalServerError, payload)
		return
	}

	// ------------------------------------------------------------------
	// 2. Convert inbound OpenAI request → CoreRequest.
	// ------------------------------------------------------------------
	coreReq, err := client.ToCoreRequest(ctx, &openAIReq)
	if err != nil {
		log.Error("adapter path: ToCoreRequest failed", "error", err)
		payload := openai.ErrorResponse{
			Error: openai.ErrorObject{
				Message: fmt.Sprintf("request conversion failed: %v", err),
				Type:    "server_error",
				Code:    "conversion_error",
			},
		}
		record.Error = traceError("to_core_request", err)
		record.OpenAIResponse = payload
		writeOpenAIError(w, http.StatusInternalServerError, payload)
		return
	}

	// ------------------------------------------------------------------
	// 3. Pick upstream provider candidate, resolve ProviderAdapter.
	// ------------------------------------------------------------------
	preferred, ok := route.Preferred()
	if !ok {
		log.Warn("adapter path: no provider candidate")
		payload := openai.ErrorResponse{
			Error: openai.ErrorObject{
				Message: "adapter path precondition failed: no fallback available",
				Type:    "server_error",
				Code:    "adapter_fallback",
			},
		}
		record.Error = traceError("no_candidate", fmt.Errorf("no provider candidate"))
		record.OpenAIResponse = payload
		writeOpenAIError(w, http.StatusInternalServerError, payload)
		return
	}

	providerAdapter, ok := s.adapterRegistry.GetProvider(preferred.Protocol)
	if !ok {
		log.Warn("adapter path: no provider adapter for protocol", "protocol", preferred.Protocol)
		payload := openai.ErrorResponse{
			Error: openai.ErrorObject{
				Message: "adapter path precondition failed: no fallback available",
				Type:    "server_error",
				Code:    "adapter_fallback",
			},
		}
		record.Error = traceError("provider_adapter", fmt.Errorf("no provider adapter for %q", preferred.Protocol))
		record.OpenAIResponse = payload
		writeOpenAIError(w, http.StatusInternalServerError, payload)
		return
	}

	// ------------------------------------------------------------------
	// 4. Convert CoreRequest → upstream request (anthropic.MessageRequest).
	//    Cache planning/injection happens inside FromCoreRequest.
	// ------------------------------------------------------------------
	// Override CoreRequest model alias with upstream model name so
	// the upstream provider receives the correct model identifier.
	coreReq.Model = preferred.UpstreamModel

	// Inject web search tools at Core level if mode is "injected".
	// This replaces web_search with tavily_search/firecrawl_fetch tools.
	wsInjected := s.injectCoreWebSearch(ctx, coreReq, preferred, openAIReq)

	upstreamAny, err := providerAdapter.FromCoreRequest(ctx, coreReq)
	if err != nil {
		log.Error("adapter path: FromCoreRequest failed", "error", err)
		payload := openai.ErrorResponse{
			Error: openai.ErrorObject{
				Message: fmt.Sprintf("upstream conversion failed: %v", err),
				Type:    "server_error",
				Code:    "conversion_error",
			},
		}
		record.Error = traceError("from_core_request", err)
		record.OpenAIResponse = payload
		writeOpenAIError(w, http.StatusInternalServerError, payload)
		return
	}
	// Protocol-specific type assertion and upstream call.
	var coreResp *format.CoreResponse
	switch preferred.Protocol {
	case config.ProtocolAnthropic:
		upstreamReq, ok := upstreamAny.(*anthropic.MessageRequest)
		if !ok {
			log.Error("adapter path: unexpected anthropic upstream type", "type", fmt.Sprintf("%T", upstreamAny))
			payload := openai.ErrorResponse{
				Error: openai.ErrorObject{
					Message: "unexpected anthropic upstream request type",
					Type:    "server_error",
					Code:    "internal_error",
				},
			}
			record.Error = traceError("upstream_type", fmt.Errorf("unexpected anthropic type %T", upstreamAny))
			record.OpenAIResponse = payload
			writeOpenAIError(w, http.StatusInternalServerError, payload)
			return
		}

		// Inject web_search tool if enabled for this model.
		if s.providerMgr.ResolvedWebSearch(preferred.ProviderKey) == "enabled" {
			injectAnthropicWebSearch(upstreamReq)
		}

		// Prepend cached reasoning blocks for DeepSeek thinking chain replay.
		if s.pluginRegistry != nil && sess != nil {
			prependCachedThinking(upstreamReq, sess)
		}

		// If streaming, use streaming path.
		if openAIReq.Stream {
			s.handleAdapterStream(w, r, ctx, openAIReq, coreReq, upstreamReq, preferred, wsInjected)
			record.OpenAIRequest = nil
			return
		}

		// Non-streaming upstream call.
		effectiveProvider := preferred.Client
		if effectiveProvider == nil {
			log.Error("adapter path: no upstream provider resolved")
			payload := openai.ErrorResponse{
				Error: openai.ErrorObject{
					Message: fmt.Sprintf("no upstream provider for model %q", openAIReq.Model),
					Type:    "server_error",
					Code:    "provider_error",
				},
			}
			record.Error = traceError("resolve_provider", fmt.Errorf("no upstream provider for %q", openAIReq.Model))
			record.OpenAIResponse = payload
			writeOpenAIError(w, http.StatusBadGateway, payload)
			return
		}

		// Wrap provider with search orchestrator if web search is "injected".
		if wsInjected {
			if acc, ok := effectiveProvider.(provider.AnthropicClientAccessor); ok && s.runtime != nil {
				cfgC := s.runtime.Current().Config
				wrapped := websearchinjected.WrapProvider(
					acc.AnthropicClient(),
					cfgC.TavilyAPIKey, cfgC.FirecrawlAPIKey, s.maxSearchRounds(),
				)
				effectiveProvider = &searchProviderAdapter{wrapped: wrapped}
			}
		}

		// Wrap with visual orchestrator at Core level if enabled for this model.
		// This uses CoreProvider, which is protocol-agnostic.
		if visProv := s.wrapWithVisual(ctx, openAIReq.Model, preferred, providerAdapter, sess); visProv != nil {
			var coreRespApi *format.CoreResponse
			coreRespApi, err = visProv.CreateCore(ctx, coreReq)
			if err == nil {
				coreResp = coreRespApi
			}
		} else {
			var upstreamRespMsg anthropic.MessageResponse
			var rawResp any
			rawResp, err = effectiveProvider.CreateMessage(ctx, *upstreamReq)
			if err == nil {
				var okt bool
				upstreamRespMsg, okt = rawResp.(anthropic.MessageResponse)
				if !okt {
					err = fmt.Errorf("unexpected anthropic response type %T", rawResp)
				} else {
					// Normal path: convert back to CoreResponse.
					msgResp := upstreamRespMsg
					coreResp, err = providerAdapter.ToCoreResponse(ctx, &msgResp)
				}
			}
		}
		if err != nil {
			log.Error("adapter path: CreateMessage failed", "error", err)
			payload := openai.ErrorResponse{
				Error: openai.ErrorObject{
					Message: fmt.Sprintf("upstream error: %v", err),
					Type:    "server_error",
					Code:    "provider_error",
				},
			}
			record.Error = traceError("create_message", err)
			record.OpenAIResponse = payload
			writeOpenAIError(w, http.StatusBadGateway, payload)
			return
		}

	case config.ProtocolOpenAIChat:
		chatReq, ok := upstreamAny.(*chat.ChatRequest)
		if !ok {
			log.Error("adapter path: unexpected chat upstream type", "type", fmt.Sprintf("%T", upstreamAny))
			payload := openai.ErrorResponse{
				Error: openai.ErrorObject{
					Message: "unexpected chat upstream request type",
					Type:    "server_error",
					Code:    "internal_error",
				},
			}
			record.Error = traceError("upstream_type", fmt.Errorf("unexpected chat type %T", upstreamAny))
			record.OpenAIResponse = payload
			writeOpenAIError(w, http.StatusInternalServerError, payload)
			return
		}

		// Prepend cached reasoning for DeepSeek thinking chain replay.
		if s.pluginRegistry != nil && sess != nil {
			prependCachedReasoningForChat(chatReq, sess)
		}

		if openAIReq.Stream {
			s.handleAdapterStream(w, r, ctx, openAIReq, coreReq, chatReq, preferred, wsInjected)
			record.OpenAIRequest = nil
			return
		}

		chatClientRaw, ok := s.chatClients[preferred.ProviderKey]
		if !ok {
			log.Error("adapter path: no chat client for provider", "provider", preferred.ProviderKey)
			payload := openai.ErrorResponse{
				Error: openai.ErrorObject{
					Message: fmt.Sprintf("no chat client for provider %q", preferred.ProviderKey),
					Type:    "server_error",
					Code:    "provider_error",
				},
			}
			record.Error = traceError("chat_client", fmt.Errorf("no chat client for %q", preferred.ProviderKey))
			record.OpenAIResponse = payload
			writeOpenAIError(w, http.StatusBadGateway, payload)
			return
		}
		chatClient, ok := chatClientRaw.(*chat.Client)
		if !ok {
			log.Error("adapter path: invalid chat client type", "provider", preferred.ProviderKey)
			payload := openai.ErrorResponse{
				Error: openai.ErrorObject{
					Message: fmt.Sprintf("invalid chat client for provider %q", preferred.ProviderKey),
					Type:    "server_error",
					Code:    "internal_error",
				},
			}
			record.Error = traceError("chat_client_type", fmt.Errorf("invalid chat client for %q", preferred.ProviderKey))
			record.OpenAIResponse = payload
			writeOpenAIError(w, http.StatusInternalServerError, payload)
			return
		}

		record.ChatRequest = chatReq
		var chatResp *chat.ChatResponse
		if wsInjected {
			chatResp, err = s.executeChatSearchLoop(ctx, chatClient, chatReq, s.runtime.Current().Config.TavilyAPIKey, s.runtime.Current().Config.FirecrawlAPIKey, s.maxSearchRounds())
		} else {
			chatResp, err = chatClient.CreateChat(ctx, chatReq)
		}
		if err != nil {
			log.Error("adapter path: Chat API call failed", "error", err)
			payload := openai.ErrorResponse{
				Error: openai.ErrorObject{
					Message: fmt.Sprintf("chat upstream error: %v", err),
					Type:    "server_error",
					Code:    "provider_error",
				},
			}
			record.Error = traceError("chat_api", err)
			record.OpenAIResponse = payload
			writeOpenAIError(w, http.StatusBadGateway, payload)
			return
		}
		record.ChatResponse = chatResp

		coreResp, err = providerAdapter.ToCoreResponse(ctx, chatResp)
		if err != nil {
			log.Error("adapter path: Chat ToCoreResponse failed", "error", err)
			payload := openai.ErrorResponse{
				Error: openai.ErrorObject{
					Message: fmt.Sprintf("chat response conversion failed: %v", err),
					Type:    "server_error",
					Code:    "conversion_error",
				},
			}
			record.Error = traceError("to_core_response", err)
			record.OpenAIResponse = payload
			writeOpenAIError(w, http.StatusInternalServerError, payload)
			return
		}

		// Cache reasoning from Chat response for DeepSeek thinking replay.
		// The reasoning_content must be echoed back on follow-up assistant messages.
		if sess != nil {
			for _, choice := range chatResp.Choices {
				if choice.Message.ReasoningContent != "" && len(choice.Message.ToolCalls) > 0 {
					var tcIDs []string
					for _, tc := range choice.Message.ToolCalls {
						tcIDs = append(tcIDs, tc.ID)
					}
					cacheReasoningForChat(sess, tcIDs, choice.Message.ReasoningContent)
				}
			}
		}

	case config.ProtocolGoogleGenAI:
		googleReq, ok := upstreamAny.(*google.GenerateContentRequest)
		if !ok {
			log.Error("adapter path: unexpected google upstream type", "type", fmt.Sprintf("%T", upstreamAny))
			payload := openai.ErrorResponse{
				Error: openai.ErrorObject{
					Message: "unexpected google upstream request type",
					Type:    "server_error",
					Code:    "internal_error",
				},
			}
			record.Error = traceError("upstream_type", fmt.Errorf("unexpected google type %T", upstreamAny))
			record.OpenAIResponse = payload
			writeOpenAIError(w, http.StatusInternalServerError, payload)
			return
		}

		if openAIReq.Stream {
			s.handleAdapterStream(w, r, ctx, openAIReq, coreReq, googleReq, preferred, wsInjected)
			record.OpenAIRequest = nil
			return
		}

		googleClientRaw, ok := s.googleClients[preferred.ProviderKey]
		if !ok {
			log.Error("adapter path: no google client for provider", "provider", preferred.ProviderKey)
			payload := openai.ErrorResponse{
				Error: openai.ErrorObject{
					Message: fmt.Sprintf("no google client for provider %q", preferred.ProviderKey),
					Type:    "server_error",
					Code:    "provider_error",
				},
			}
			record.Error = traceError("google_client", fmt.Errorf("no google client for %q", preferred.ProviderKey))
			record.OpenAIResponse = payload
			writeOpenAIError(w, http.StatusBadGateway, payload)
			return
		}
		googleClient, ok := googleClientRaw.(*google.Client)
		if !ok {
			log.Error("adapter path: invalid google client type", "provider", preferred.ProviderKey)
			payload := openai.ErrorResponse{
				Error: openai.ErrorObject{
					Message: fmt.Sprintf("invalid google client for provider %q", preferred.ProviderKey),
					Type:    "server_error",
					Code:    "internal_error",
				},
			}
			record.Error = traceError("google_client_type", fmt.Errorf("invalid google client for %q", preferred.ProviderKey))
			record.OpenAIResponse = payload
			writeOpenAIError(w, http.StatusInternalServerError, payload)
			return
		}

		record.UpstreamRequest = googleReq
		var googleResp *google.GenerateContentResponse
		if wsInjected {
			googleResp, err = s.executeGoogleSearchLoop(ctx, googleClient, preferred.UpstreamModel, googleReq, s.runtime.Current().Config.TavilyAPIKey, s.runtime.Current().Config.FirecrawlAPIKey, s.maxSearchRounds())
		} else {
			googleResp, err = googleClient.GenerateContent(ctx, preferred.UpstreamModel, googleReq)
		}
		if err != nil {
			log.Error("adapter path: Google API call failed", "error", err)
			payload := openai.ErrorResponse{
				Error: openai.ErrorObject{
					Message: fmt.Sprintf("google upstream error: %v", err),
					Type:    "server_error",
					Code:    "provider_error",
				},
			}
			record.Error = traceError("google_api", err)
			record.OpenAIResponse = payload
			writeOpenAIError(w, http.StatusBadGateway, payload)
			return
		}
		record.UpstreamResponse = googleResp

		coreResp, err = providerAdapter.ToCoreResponse(ctx, googleResp)
		if err != nil {
			log.Error("adapter path: Google ToCoreResponse failed", "error", err)
			payload := openai.ErrorResponse{
				Error: openai.ErrorObject{
					Message: fmt.Sprintf("google response conversion failed: %v", err),
					Type:    "server_error",
					Code:    "conversion_error",
				},
			}
			record.Error = traceError("to_core_response", err)
			record.OpenAIResponse = payload
			writeOpenAIError(w, http.StatusInternalServerError, payload)
			return
		}

	default:
		log.Error("adapter path: unsupported protocol", "protocol", preferred.Protocol)
		payload := openai.ErrorResponse{
			Error: openai.ErrorObject{
				Message: fmt.Sprintf("unsupported protocol %q", preferred.Protocol),
				Type:    "server_error",
				Code:    "adapter_not_configured",
			},
		}
		record.Error = traceError("unsupported_protocol", fmt.Errorf("unsupported protocol %q", preferred.Protocol))
		record.OpenAIResponse = payload
		writeOpenAIError(w, http.StatusInternalServerError, payload)
		return
	}
	if err != nil {
		log.Error("adapter path: ToCoreResponse failed", "error", err)
		payload := openai.ErrorResponse{
			Error: openai.ErrorObject{
				Message: fmt.Sprintf("response conversion failed: %v", err),
				Type:    "server_error",
				Code:    "conversion_error",
			},
		}
		record.Error = traceError("to_core_response", err)
		record.OpenAIResponse = payload
		writeOpenAIError(w, http.StatusInternalServerError, payload)
		return
	}

	// ------------------------------------------------------------------


	// Propagate codex_tool_map from CoreRequest to CoreResponse.
	if coreReq != nil && coreResp != nil && coreReq.Extensions != nil {
		if tm, ok := coreReq.Extensions["codex_tool_map"]; ok {
			if coreResp.Extensions == nil {
				coreResp.Extensions = make(map[string]any)
			}
			coreResp.Extensions["codex_tool_map"] = tm
		}
	}
	// 7. Convert CoreResponse → outbound OpenAI Response.
	// ------------------------------------------------------------------
	outAny, err := client.FromCoreResponse(ctx, coreResp)
	if err != nil {
		log.Error("adapter path: FromCoreResponse failed", "error", err)
		payload := openai.ErrorResponse{
			Error: openai.ErrorObject{
				Message: fmt.Sprintf("output conversion failed: %v", err),
				Type:    "server_error",
				Code:    "conversion_error",
			},
		}
		record.Error = traceError("from_core_response", err)
		record.OpenAIResponse = payload
		writeOpenAIError(w, http.StatusInternalServerError, payload)
		return
	}
	out, ok := outAny.(*openai.Response)
	if !ok {
		log.Error("adapter path: unexpected output type", "type", fmt.Sprintf("%T", outAny))
		payload := openai.ErrorResponse{
			Error: openai.ErrorObject{
				Message: "unexpected output response type",
				Type:    "server_error",
				Code:    "internal_error",
			},
		}
		record.Error = traceError("output_type", fmt.Errorf("unexpected output type %T", outAny))
		record.OpenAIResponse = payload
		writeOpenAIError(w, http.StatusInternalServerError, payload)
		return
	}

	// ------------------------------------------------------------------
	// 8. Write the response.
	// ------------------------------------------------------------------
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(out)

	// Record trace with upstream details and final output.
	record.OpenAIResponse = out

	// Record completion via plugin hooks (placeholder).
	if s.pluginRegistry != nil {
		usage := zeroUsage(string(config.ProtocolAnthropic), "anthropic_response")
		if coreResp.Usage.InputTokens > 0 || coreResp.Usage.OutputTokens > 0 {
			usage = usageFromAnthropic(string(config.ProtocolAnthropic), "core_response", format.CoreUsage{
				InputTokens:       coreResp.Usage.InputTokens,
				OutputTokens:      coreResp.Usage.OutputTokens,
				CachedInputTokens: coreResp.Usage.CachedInputTokens,
			}, true) // input tokens now include cache (normalized at adapter level)
		}

		// Log detailed metrics for non-streaming request.
		inputTotal := coreResp.Usage.InputTokens
		cachedInput := coreResp.Usage.CachedInputTokens
		freshInput := inputTotal - cachedInput
		if freshInput < 0 {
			freshInput = 0
		}
		outputTokens := coreResp.Usage.OutputTokens
		var cacheHitRate float64
		effectiveTotal := freshInput + cachedInput
		if effectiveTotal > 0 {
			cacheHitRate = float64(cachedInput) / float64(effectiveTotal) * 100
		}
		reqDuration := time.Since(requestStart)
		billingUsage := stats.BillingUsage{
			FreshInputTokens:         freshInput,
			OutputTokens:             outputTokens,
			CacheCreationInputTokens: 0,
			CacheReadInputTokens:     cachedInput,
		}
		reqCost := computeCostWithProviderPricing(s.providerMgr, s.stats, openAIReq.Model, preferred.UpstreamModel, preferred.ProviderKey, billingUsage)
		log.Info("请求完成",
			"request_model", openAIReq.Model,
			"actual_model", preferred.UpstreamModel,
			"provider", preferred.ProviderKey,
			"input_total", inputTotal,
			"input_fresh", freshInput,
			"input_cache_read", cachedInput,
			"input_cache_write", 0,
			"output_tokens", outputTokens,
			"cache_hit_rate", fmt.Sprintf("%.1f%%", cacheHitRate),
			"request_cost", reqCost,
			"duration", reqDuration,
		)

		s.onRequestCompleted(
			openAIReq.Model, preferred.UpstreamModel, preferred.ProviderKey,
			requestStart, usage,
			reqCost, "success", "",
		)

		// Record usage statistics.
		if s.stats != nil {
			s.stats.Record(openAIReq.Model, preferred.UpstreamModel, stats.Usage{
				InputTokens:              coreResp.Usage.InputTokens,
				OutputTokens:             coreResp.Usage.OutputTokens,
				CacheReadInputTokens:     coreResp.Usage.CachedInputTokens,
				CacheCreationInputTokens: 0,
			})
		}
	}
}

// handleAdapterStream handles the streaming path through adapter dispatch.
func (s *Server) handleAdapterStream(
	w http.ResponseWriter,
	r *http.Request,
	ctx context.Context,
	openAIReq openai.ResponsesRequest,
	coreReq *format.CoreRequest,
	upstreamReq any,
	candidate provider.ProviderCandidate,
	wsInjected bool,
) {
	log := slog.Default().With("model", openAIReq.Model, "path", "adapter_stream")

	// Track when the request started for latency measurement.
	requestStart := time.Now()

	// Get or create session for this request.
	sess := s.sessionForRequest(r)
	_ = sess

	// Initialize trace record.
	bodyBytes, _ := json.Marshal(openAIReq)
	streamRecord := mbtrace.Record{
		HTTPRequest:   mbtrace.NewHTTPRequest(r),
		OpenAIRequest: mbtrace.RawJSONOrString(bodyBytes),
		Model:         openAIReq.Model,
	}
	defer func() {
		s.writeTrace(streamRecord)
	}()

	if candidate.Protocol == config.ProtocolAnthropic && coreRequestHasImage(coreReq) {
		if providerAdapter := s.adapterRegistryProvider(config.ProtocolAnthropic); providerAdapter != nil {
			if visProv := s.wrapWithVisual(ctx, openAIReq.Model, candidate, providerAdapter, sess); visProv != nil {
				coreResp, err := visProv.CreateCore(ctx, coreReq)
				if err != nil {
					log.Error("adapter stream visual fallback: CreateCore failed", "error", err)
					payload := openai.ErrorResponse{
						Error: openai.ErrorObject{
							Message: fmt.Sprintf("upstream error: %v", err),
							Type:    "server_error",
							Code:    "provider_error",
						},
					}
					streamRecord.Error = traceError("stream_visual_create", err)
					streamRecord.OpenAIResponse = payload
					writeOpenAIError(w, http.StatusBadGateway, payload)
					return
				}
				s.writeCoreResponseAsOpenAIStream(w, ctx, openAIReq, coreReq, coreResp, candidate, requestStart, &streamRecord)
				return
			}
		}
	}

	// Protocol-specific upstream streaming: get stream + convert to CoreStreamEvent.
	var coreEvents <-chan format.CoreStreamEvent
	var providerStream format.ProviderStreamAdapter

	switch candidate.Protocol {
	case config.ProtocolAnthropic:
		anthReq, ok := upstreamReq.(*anthropic.MessageRequest)
		if !ok {
			log.Error("adapter stream: unexpected anthropic type")
			payload := openai.ErrorResponse{
				Error: openai.ErrorObject{
					Message: "unexpected anthropic upstream type",
					Type:    "server_error",
					Code:    "internal_error",
				},
			}
			streamRecord.Error = traceError("stream_type", fmt.Errorf("unexpected anthropic type"))
			streamRecord.OpenAIResponse = payload
			writeOpenAIError(w, http.StatusInternalServerError, payload)
			return
		}
		streamRecord.AnthropicRequest = anthReq
		streamRecord.UpstreamRequest = anthReq

		effectiveProvider := candidate.Client
		if effectiveProvider == nil {
			log.Error("adapter stream: no upstream provider resolved")
			payload := openai.ErrorResponse{
				Error: openai.ErrorObject{
					Message: fmt.Sprintf("no upstream provider for model %q", openAIReq.Model),
					Type:    "server_error",
					Code:    "provider_error",
				},
			}
			streamRecord.Error = traceError("stream_resolve_provider", fmt.Errorf("no upstream provider for %q", openAIReq.Model))
			streamRecord.OpenAIResponse = payload
			writeOpenAIError(w, http.StatusBadGateway, payload)
			return
		}

		// StreamMessage on ProviderClient returns <-chan any, losing the concrete type.
		// Get the inner anthropic.Client directly so ToCoreStream receives anthropic.Stream.
		acc, ok := effectiveProvider.(provider.AnthropicClientAccessor)
		if !ok {
			log.Error("adapter stream: provider does not support AnthropicClientAccessor", "provider", candidate.ProviderKey)
			payload := openai.ErrorResponse{
				Error: openai.ErrorObject{
					Message: "provider does not support anthropic streaming",
					Type:    "server_error",
					Code:    "provider_error",
				},
			}
			streamRecord.Error = traceError("stream_accessor", fmt.Errorf("provider %q not AnthropicClientAccessor", candidate.ProviderKey))
			streamRecord.OpenAIResponse = payload
			writeOpenAIError(w, http.StatusInternalServerError, payload)
			return
		}
		// Strip image blocks from anthropic request if visual extension is enabled.
		// This prevents base64 image data from being sent to text-only models.
		if s.pluginRegistry != nil && s.runtime != nil && openAIReq.Model != "" {
			cfgV := s.runtime.Current().Config
			pluginCfg := config.PluginFromGlobalConfig(&cfgV)
			visCfg, visOk := visualpkg.ConfigForModel(pluginCfg, openAIReq.Model)
			if visOk && visCfg.Provider != "" && visCfg.Model != "" {
				strippedReq, _ := visualpkg.StripImagesFromAnthropic(*anthReq)
				anthReq = &strippedReq
			}
		}
		stream, err := acc.AnthropicClient().StreamMessage(ctx, *anthReq)
		if err != nil {
			log.Error("adapter stream: StreamMessage failed", "error", err)
			payload := openai.ErrorResponse{
				Error: openai.ErrorObject{
					Message: fmt.Sprintf("upstream stream error: %v", err),
					Type:    "server_error",
					Code:    "provider_error",
				},
			}
			streamRecord.Error = traceError("stream_message", err)
			streamRecord.OpenAIResponse = payload
			writeOpenAIError(w, http.StatusBadGateway, payload)
			return
		}
		_ = stream

		providerStream, ok = s.adapterRegistry.GetProviderStream(config.ProtocolAnthropic)
		if !ok {
			log.Warn("adapter stream: no anthropic provider stream adapter")
			payload := openai.ErrorResponse{
				Error: openai.ErrorObject{
					Message: "adapter stream fallback not available",
					Type:    "server_error",
					Code:    "adapter_fallback",
				},
			}
			streamRecord.Error = traceError("stream_provider_adapter", fmt.Errorf("no anthropic provider stream adapter"))
			streamRecord.OpenAIResponse = payload
			writeOpenAIError(w, http.StatusInternalServerError, payload)
			return
		}
		coreEvents, err = providerStream.ToCoreStream(ctx, stream)
		if err != nil {
			log.Error("adapter stream: ToCoreStream failed", "error", err)
			payload := openai.ErrorResponse{
				Error: openai.ErrorObject{
					Message: fmt.Sprintf("stream conversion failed: %v", err),
					Type:    "server_error",
					Code:    "conversion_error",
				},
			}
			streamRecord.Error = traceError("stream_to_core", err)
			streamRecord.OpenAIResponse = payload
			writeOpenAIError(w, http.StatusInternalServerError, payload)
			return
		}

	case config.ProtocolOpenAIChat:
		chatReq, ok := upstreamReq.(*chat.ChatRequest)
		if !ok {
			log.Error("adapter stream: unexpected chat type")
			payload := openai.ErrorResponse{
				Error: openai.ErrorObject{
					Message: "unexpected chat upstream type",
					Type:    "server_error",
					Code:    "internal_error",
				},
			}
			streamRecord.Error = traceError("stream_type", fmt.Errorf("unexpected chat type"))
			streamRecord.OpenAIResponse = payload
			writeOpenAIError(w, http.StatusInternalServerError, payload)
			return
		}

		// Prepend cached reasoning for DeepSeek thinking chain replay.
		if s.pluginRegistry != nil && sess != nil {
			prependCachedReasoningForChat(chatReq, sess)
		}

		chatClientRaw, ok := s.chatClients[candidate.ProviderKey]
		if !ok {
			log.Error("adapter stream: no chat client", "provider", candidate.ProviderKey)
			payload := openai.ErrorResponse{
				Error: openai.ErrorObject{
					Message: fmt.Sprintf("no chat client for provider %q", candidate.ProviderKey),
					Type:    "server_error",
					Code:    "provider_error",
				},
			}
			streamRecord.Error = traceError("stream_chat_client", fmt.Errorf("no chat client for %q", candidate.ProviderKey))
			streamRecord.OpenAIResponse = payload
			writeOpenAIError(w, http.StatusBadGateway, payload)
			return
		}
		chatClient, ok := chatClientRaw.(*chat.Client)
		if !ok {
			log.Error("adapter stream: chat client type assertion failed", "provider", candidate.ProviderKey)
			payload := openai.ErrorResponse{
				Error: openai.ErrorObject{
					Message: fmt.Sprintf("invalid chat client for provider %q", candidate.ProviderKey),
					Type:    "server_error",
					Code:    "internal_error",
				},
			}
			streamRecord.Error = traceError("stream_chat_type", fmt.Errorf("invalid chat client for %q", candidate.ProviderKey))
			streamRecord.OpenAIResponse = payload
			writeOpenAIError(w, http.StatusInternalServerError, payload)
			return
		}

		streamRecord.ChatRequest = chatReq
		var chatStream <-chan chat.ChatStreamChunk
		var err error
		if wsInjected {
			chatStream, err = s.chatSearchBufferedStream(ctx, chatClient, chatReq, s.runtime.Current().Config.TavilyAPIKey, s.runtime.Current().Config.FirecrawlAPIKey, s.maxSearchRounds())
		} else {
			chatStream, err = chatClient.StreamChat(ctx, chatReq)
		}
		if err != nil {
			log.Error("adapter stream: StreamChat failed", "error", err)
			payload := openai.ErrorResponse{
				Error: openai.ErrorObject{
					Message: fmt.Sprintf("chat stream error: %v", err),
					Type:    "server_error",
					Code:    "provider_error",
				},
			}
			streamRecord.Error = traceError("stream_chat", err)
			streamRecord.OpenAIResponse = payload
			writeOpenAIError(w, http.StatusBadGateway, payload)
			return
		}

		providerStream, ok = s.adapterRegistry.GetProviderStream(config.ProtocolOpenAIChat)
		if !ok {
			log.Warn("adapter stream: no chat provider stream adapter")
			payload := openai.ErrorResponse{
				Error: openai.ErrorObject{
					Message: "chat stream adapter not available",
					Type:    "server_error",
					Code:    "adapter_fallback",
				},
			}
			streamRecord.Error = traceError("stream_chat_adapter", fmt.Errorf("no chat provider stream adapter"))
			streamRecord.OpenAIResponse = payload
			writeOpenAIError(w, http.StatusInternalServerError, payload)
			return
		}
		coreEvents, err = providerStream.ToCoreStream(ctx, chatStream)
		if err != nil {
			log.Error("adapter stream: Chat ToCoreStream failed", "error", err)
			payload := openai.ErrorResponse{
				Error: openai.ErrorObject{
					Message: fmt.Sprintf("chat stream conversion failed: %v", err),
					Type:    "server_error",
					Code:    "conversion_error",
				},
			}
			streamRecord.Error = traceError("stream_chat_tocore", err)
			streamRecord.OpenAIResponse = payload
			writeOpenAIError(w, http.StatusInternalServerError, payload)
			return
		}

	case config.ProtocolGoogleGenAI:
		googleReq, ok := upstreamReq.(*google.GenerateContentRequest)
		if !ok {
			log.Error("adapter stream: unexpected google type")
			payload := openai.ErrorResponse{
				Error: openai.ErrorObject{
					Message: "unexpected google upstream type",
					Type:    "server_error",
					Code:    "internal_error",
				},
			}
			streamRecord.Error = traceError("stream_type", fmt.Errorf("unexpected google type"))
			streamRecord.OpenAIResponse = payload
			writeOpenAIError(w, http.StatusInternalServerError, payload)
			return
		}

		googleClientRaw, ok := s.googleClients[candidate.ProviderKey]
		if !ok {
			log.Error("adapter stream: no google client", "provider", candidate.ProviderKey)
			payload := openai.ErrorResponse{
				Error: openai.ErrorObject{
					Message: fmt.Sprintf("no google client for provider %q", candidate.ProviderKey),
					Type:    "server_error",
					Code:    "provider_error",
				},
			}
			streamRecord.Error = traceError("stream_google_client", fmt.Errorf("no google client for %q", candidate.ProviderKey))
			streamRecord.OpenAIResponse = payload
			writeOpenAIError(w, http.StatusBadGateway, payload)
			return
		}
		googleClient, ok := googleClientRaw.(*google.Client)
		if !ok {
			log.Error("adapter stream: google client type assertion failed", "provider", candidate.ProviderKey)
			payload := openai.ErrorResponse{
				Error: openai.ErrorObject{
					Message: fmt.Sprintf("invalid google client for provider %q", candidate.ProviderKey),
					Type:    "server_error",
					Code:    "internal_error",
				},
			}
			streamRecord.Error = traceError("stream_google_type", fmt.Errorf("invalid google client for %q", candidate.ProviderKey))
			streamRecord.OpenAIResponse = payload
			writeOpenAIError(w, http.StatusInternalServerError, payload)
			return
		}

		streamRecord.UpstreamRequest = googleReq
		googleStream, err := googleClient.StreamGenerateContent(ctx, candidate.UpstreamModel, googleReq)
		if err != nil {
			log.Error("adapter stream: StreamGenerateContent failed", "error", err)
			payload := openai.ErrorResponse{
				Error: openai.ErrorObject{
					Message: fmt.Sprintf("google stream error: %v", err),
					Type:    "server_error",
					Code:    "provider_error",
				},
			}
			streamRecord.Error = traceError("stream_google", err)
			streamRecord.OpenAIResponse = payload
			writeOpenAIError(w, http.StatusBadGateway, payload)
			return
		}

		providerStream, ok = s.adapterRegistry.GetProviderStream(config.ProtocolGoogleGenAI)
		if !ok {
			log.Warn("adapter stream: no google provider stream adapter")
			payload := openai.ErrorResponse{
				Error: openai.ErrorObject{
					Message: "google stream adapter not available",
					Type:    "server_error",
					Code:    "adapter_fallback",
				},
			}
			streamRecord.Error = traceError("stream_google_adapter", fmt.Errorf("no google provider stream adapter"))
			streamRecord.OpenAIResponse = payload
			writeOpenAIError(w, http.StatusInternalServerError, payload)
			return
		}
		coreEvents, err = providerStream.ToCoreStream(ctx, googleStream)
		if err != nil {
			log.Error("adapter stream: Google ToCoreStream failed", "error", err)
			payload := openai.ErrorResponse{
				Error: openai.ErrorObject{
					Message: fmt.Sprintf("google stream conversion failed: %v", err),
					Type:    "server_error",
					Code:    "conversion_error",
				},
			}
			streamRecord.Error = traceError("stream_google_tocore", err)
			streamRecord.OpenAIResponse = payload
			writeOpenAIError(w, http.StatusInternalServerError, payload)
			return
		}

	default:
		log.Error("adapter stream: unsupported protocol", "protocol", candidate.Protocol)
		payload := openai.ErrorResponse{
			Error: openai.ErrorObject{
				Message: fmt.Sprintf("unsupported stream protocol %q", candidate.Protocol),
				Type:    "server_error",
				Code:    "adapter_not_configured",
			},
		}
		streamRecord.Error = traceError("stream_unsupported_protocol", fmt.Errorf("unsupported protocol %q", candidate.Protocol))
		streamRecord.OpenAIResponse = payload
		writeOpenAIError(w, http.StatusInternalServerError, payload)
		return
	}

	// Get client stream adapter.
	clientStream, ok := s.adapterRegistry.GetClientStream(config.ProtocolOpenAIResponse)
	if !ok {
		log.Warn("adapter stream: no client stream adapter")
		payload := openai.ErrorResponse{
			Error: openai.ErrorObject{
				Message: "adapter stream fallback not available",
				Type:    "server_error",
				Code:    "adapter_fallback",
			},
		}
		streamRecord.Error = traceError("stream_client_adapter", fmt.Errorf("no client stream adapter"))
		streamRecord.OpenAIResponse = payload
		writeOpenAIError(w, http.StatusInternalServerError, payload)
		return
	}

	// Convert CoreStreamEvent channel → OpenAI stream event channel.
	streamChanAny, err := clientStream.FromCoreStream(ctx, coreReq, coreEvents)
	if err != nil {
		log.Error("adapter stream: FromCoreStream failed", "error", err)
		payload := openai.ErrorResponse{
			Error: openai.ErrorObject{
				Message: fmt.Sprintf("client stream conversion failed: %v", err),
				Type:    "server_error",
				Code:    "conversion_error",
			},
		}
		streamRecord.Error = traceError("stream_from_core", err)
		streamRecord.OpenAIResponse = payload
		writeOpenAIError(w, http.StatusInternalServerError, payload)
		return
	}

	streamChan, ok := streamChanAny.(<-chan openai.StreamEvent)
	if !ok {
		log.Error("adapter stream: unexpected stream channel type", "type", fmt.Sprintf("%T", streamChanAny))
		payload := openai.ErrorResponse{
			Error: openai.ErrorObject{
				Message: "unexpected stream channel type",
				Type:    "server_error",
				Code:    "internal_error",
			},
		}
		streamRecord.Error = traceError("stream_channel_type", fmt.Errorf("unexpected stream channel type %T", streamChanAny))
		streamRecord.OpenAIResponse = payload
		writeOpenAIError(w, http.StatusInternalServerError, payload)
		return
	}

	// Write SSE events.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)

	// Track usage from the final response.completed event.
	var finalUsage openai.Usage
	var finalResp *openai.Response
	for ev := range streamChan {
		if ev.Event == "response.completed" {
			if lf, ok := ev.Data.(openai.ResponseLifecycleEvent); ok {
				finalUsage = lf.Response.Usage
				lfResp := lf.Response
				finalResp = &lfResp
			}
		}
		if err := writeSSE(w, ev); err != nil {
			log.Warn("adapter stream: SSE write failed, aborting stream", "error", err)
			break
		}
	}

	// Record usage statistics after stream completes.

	// Capture stream events for trace.
	if s.tracer != nil && s.tracer.Enabled() {
		// OpenAI stream events from client adapter
		if oaiClient, ok := s.adapterRegistry.GetClient(config.ProtocolOpenAIResponse); ok {
			if oaiAdapter, ok := oaiClient.(*openai.OpenAIAdapter); ok {
				if events := oaiAdapter.StreamBuffer(); len(events) > 0 {
					streamRecord.OpenAIStreamEvents = events
				}
			}
		}
		// Anthropic stream events from provider adapter
		if anthProvider, ok := s.adapterRegistry.GetProvider(config.ProtocolAnthropic); ok {
			if anthAdapter, ok := anthProvider.(*anthropic.AnthropicProviderAdapter); ok {
				if events := anthAdapter.StreamBuffer(); len(events) > 0 {
					streamRecord.AnthropicStreamEvents = events
				}

				// Remember reasoning content for DeepSeek thinking replay via StreamInterceptor.
				if anthProvider2, ok := s.adapterRegistry.GetProvider(config.ProtocolAnthropic); ok {
					if anthAdapter2, ok := anthProvider2.(*anthropic.AnthropicProviderAdapter); ok {
						if s.pluginRegistry != nil && sess != nil {
							events := anthAdapter2.StreamBuffer()
							if len(events) > 0 {
								states := s.pluginRegistry.NewStreamStates(openAIReq.Model)
								for _, ev := range events {
									pluginType := ""
									switch {
									case ev.Type == "content_block_start":
										pluginType = "block_start"
									case ev.Type == "content_block_delta":
										pluginType = "block_delta"
									case ev.Type == "content_block_stop":
										pluginType = "block_stop"
									}
									if pluginType == "" {
										continue
									}
									s.pluginRegistry.OnStreamEvent(openAIReq.Model, plugin.StreamEvent{
										Type:  pluginType,
										Index: ev.Index,
										Block: anthropicContentBlockPtrToFormat(ev.ContentBlock),
										Delta: ev.Delta,
									}, states)
								}
								outputText := ""
								if finalResp != nil {
									outputText = finalResp.OutputText
								}
								s.pluginRegistry.OnStreamComplete(openAIReq.Model, states, outputText, sess.ExtensionData)
							}
						}
					}
				}
				// Chat stream events from provider adapter
				if chatProvider, ok := s.adapterRegistry.GetProvider(config.ProtocolOpenAIChat); ok {
					if chatAdapter, ok := chatProvider.(*chat.ChatProviderAdapter); ok {
						if events := chatAdapter.StreamBuffer(); len(events) > 0 {
							streamRecord.ChatStreamEvents = events

							// Cache reasoning from Chat stream for DeepSeek thinking replay.
							if sess != nil {
								var streamReasoning string
								var streamToolCallIDs []string
								for _, ev := range events {
									for _, sc := range ev.Choices {
										if sc.Delta.ReasoningContent != "" {
											streamReasoning = sc.Delta.ReasoningContent
										}
										for _, tc := range sc.Delta.ToolCalls {
											if tc.ID != "" {
												streamToolCallIDs = append(streamToolCallIDs, tc.ID)
											}
										}
									}
								}
								if streamReasoning != "" && len(streamToolCallIDs) > 0 {
									cacheReasoningForChat(sess, streamToolCallIDs, streamReasoning)
								}
							}
						}
					}
				}
			}
		}
	}
	if s.stats != nil && (finalUsage.InputTokens > 0 || finalUsage.OutputTokens > 0) {
		s.stats.Record(openAIReq.Model, candidate.UpstreamModel, stats.Usage{
			InputTokens:              finalUsage.InputTokens,
			OutputTokens:             finalUsage.OutputTokens,
			CacheCreationInputTokens: 0,
			CacheReadInputTokens:     finalUsage.InputTokensDetails.CachedTokens,
		})
	}

	inputTotal := finalUsage.InputTokens
	cachedInput := finalUsage.InputTokensDetails.CachedTokens
	freshInput := inputTotal - cachedInput
	if freshInput < 0 {
		freshInput = 0
	}
	outputTokens := finalUsage.OutputTokens
	var cacheHitRate float64
	effectiveTotal := freshInput + cachedInput
	if effectiveTotal > 0 {
		cacheHitRate = float64(cachedInput) / float64(effectiveTotal) * 100
	}
	reqDuration := time.Since(requestStart)
	billingUsage := stats.BillingUsage{
		FreshInputTokens:         freshInput,
		OutputTokens:             outputTokens,
		CacheCreationInputTokens: 0,
		CacheReadInputTokens:     cachedInput,
	}
	reqCost := computeCostWithProviderPricing(s.providerMgr, s.stats, openAIReq.Model, candidate.UpstreamModel, candidate.ProviderKey, billingUsage)
	log.Info("流式请求完成",
		"model", openAIReq.Model,
		"actual_model", candidate.UpstreamModel,
		"provider", candidate.ProviderKey,
		"input_total", inputTotal,
		"input_fresh", freshInput,
		"input_cached_tokens", cachedInput,
		"output_tokens", outputTokens,
		"cache_hit_rate", fmt.Sprintf("%.1f%%", cacheHitRate),
		"request_cost", reqCost,
		"duration", reqDuration,
	)

	// Update trace record with the final response data.
	if finalResp != nil {
		streamRecord.OpenAIResponse = finalResp
	} else {
		streamRecord.OpenAIResponse = &openai.Response{Model: openAIReq.Model, Status: "completed"}
	}

	// Notify plugin hooks for metrics tracking.
	if s.pluginRegistry != nil {
		usage := zeroUsage(string(config.ProtocolAnthropic), "anthropic_stream")
		if finalUsage.InputTokens > 0 || finalUsage.OutputTokens > 0 {
			usage = usageFromAnthropic(string(config.ProtocolAnthropic), "core_stream", format.CoreUsage{
				InputTokens:       finalUsage.InputTokens,
				OutputTokens:      finalUsage.OutputTokens,
				CachedInputTokens: finalUsage.InputTokensDetails.CachedTokens,
			}, true) // input tokens now include cache (normalized at adapter level)
		}
		reqCost := computeCostWithProviderPricing(s.providerMgr, s.stats, openAIReq.Model, candidate.UpstreamModel, candidate.ProviderKey, billingUsage)
		s.onRequestCompleted(
			openAIReq.Model, candidate.UpstreamModel, candidate.ProviderKey,
			requestStart, usage,
			reqCost, "success", "",
		)
	}
}

func (s *Server) adapterRegistryProvider(protocol string) format.ProviderAdapter {
	if s.adapterRegistry == nil {
		return nil
	}
	adapter, _ := s.adapterRegistry.GetProvider(protocol)
	return adapter
}

func (s *Server) writeCoreResponseAsOpenAIStream(
	w http.ResponseWriter,
	ctx context.Context,
	openAIReq openai.ResponsesRequest,
	coreReq *format.CoreRequest,
	coreResp *format.CoreResponse,
	candidate provider.ProviderCandidate,
	requestStart time.Time,
	streamRecord *mbtrace.Record,
) {
	log := slog.Default().With("model", openAIReq.Model, "path", "adapter_stream_visual")

	clientStream, ok := s.adapterRegistry.GetClientStream(config.ProtocolOpenAIResponse)
	if !ok {
		payload := openai.ErrorResponse{
			Error: openai.ErrorObject{
				Message: "adapter stream fallback not available",
				Type:    "server_error",
				Code:    "adapter_fallback",
			},
		}
		streamRecord.Error = traceError("stream_client_adapter", fmt.Errorf("no client stream adapter"))
		streamRecord.OpenAIResponse = payload
		writeOpenAIError(w, http.StatusInternalServerError, payload)
		return
	}

	streamChanAny, err := clientStream.FromCoreStream(ctx, coreReq, coreResponseToStreamEvents(coreResp))
	if err != nil {
		payload := openai.ErrorResponse{
			Error: openai.ErrorObject{
				Message: fmt.Sprintf("client stream conversion failed: %v", err),
				Type:    "server_error",
				Code:    "conversion_error",
			},
		}
		streamRecord.Error = traceError("stream_from_core", err)
		streamRecord.OpenAIResponse = payload
		writeOpenAIError(w, http.StatusInternalServerError, payload)
		return
	}
	streamChan, ok := streamChanAny.(<-chan openai.StreamEvent)
	if !ok {
		payload := openai.ErrorResponse{
			Error: openai.ErrorObject{
				Message: "unexpected stream channel type",
				Type:    "server_error",
				Code:    "internal_error",
			},
		}
		streamRecord.Error = traceError("stream_channel_type", fmt.Errorf("unexpected stream channel type %T", streamChanAny))
		streamRecord.OpenAIResponse = payload
		writeOpenAIError(w, http.StatusInternalServerError, payload)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)

	var finalResp *openai.Response
	for ev := range streamChan {
		if ev.Event == "response.completed" {
			if lf, ok := ev.Data.(openai.ResponseLifecycleEvent); ok {
				lfResp := lf.Response
				finalResp = &lfResp
			}
		}
		if err := writeSSE(w, ev); err != nil {
			log.Warn("adapter stream visual fallback: SSE write failed", "error", err)
			break
		}
	}

	if finalResp != nil {
		streamRecord.OpenAIResponse = finalResp
	} else {
		streamRecord.OpenAIResponse = &openai.Response{Model: openAIReq.Model, Status: "completed"}
	}

	usage := coreResp.Usage
	billingUsage := billingUsageFromAnthropic(usage)
	if s.stats != nil && (usage.InputTokens > 0 || usage.OutputTokens > 0) {
		s.stats.Record(openAIReq.Model, candidate.UpstreamModel, statsUsageFromAnthropic(usage, true))
	}
	reqCost := computeCostWithProviderPricing(s.providerMgr, s.stats, openAIReq.Model, candidate.UpstreamModel, candidate.ProviderKey, billingUsage)
	log.Info("流式视觉请求完成",
		"actual_model", candidate.UpstreamModel,
		"provider", candidate.ProviderKey,
		"input_total", usage.InputTokens,
		"output_tokens", usage.OutputTokens,
		"duration", time.Since(requestStart),
	)
	if s.pluginRegistry != nil {
		s.onRequestCompleted(
			openAIReq.Model, candidate.UpstreamModel, candidate.ProviderKey,
			requestStart, usageFromAnthropic(string(config.ProtocolAnthropic), "core_visual_stream", usage, true),
			reqCost, "success", "",
		)
	}
}

func coreResponseToStreamEvents(resp *format.CoreResponse) <-chan format.CoreStreamEvent {
	out := make(chan format.CoreStreamEvent, 16)
	go func() {
		defer close(out)
		if resp == nil {
			out <- format.CoreStreamEvent{
				Type: format.CoreEventFailed,
				Error: &format.CoreError{
					Message: "core response is nil",
					Type:    "server_error",
				},
			}
			return
		}
		out <- format.CoreStreamEvent{Type: format.CoreEventCreated, ItemID: resp.ID, Model: resp.Model}
		index := 0
		for _, msg := range resp.Messages {
			if msg.Role != "assistant" {
				continue
			}
			for _, block := range msg.Content {
				switch block.Type {
				case "reasoning":
					out <- format.CoreStreamEvent{Type: format.CoreContentBlockStarted, Index: index, ContentBlock: &format.CoreContentBlock{Type: "reasoning"}}
					if block.ReasoningText != "" {
						out <- format.CoreStreamEvent{Type: format.CoreTextDelta, Index: index, Delta: block.ReasoningText}
					}
					out <- format.CoreStreamEvent{Type: format.CoreContentBlockDone, Index: index, ContentBlock: &format.CoreContentBlock{
						Type:               "reasoning",
						ReasoningSignature: block.ReasoningSignature,
					}}
					index++
				case "text":
					out <- format.CoreStreamEvent{Type: format.CoreContentBlockStarted, Index: index, ContentBlock: &format.CoreContentBlock{Type: "text"}}
					if block.Text != "" {
						out <- format.CoreStreamEvent{Type: format.CoreTextDelta, Index: index, Delta: block.Text}
					}
					out <- format.CoreStreamEvent{Type: format.CoreContentBlockDone, Index: index}
					index++
				}
			}
		}
		status := "completed"
		if resp.Status != "" {
			status = resp.Status
		}
		eventType := format.CoreEventCompleted
		if status == "failed" {
			eventType = format.CoreEventFailed
		} else if status == "incomplete" {
			eventType = format.CoreEventIncomplete
		}
		out <- format.CoreStreamEvent{
			Type:   eventType,
			Status: status,
			Model:  resp.Model,
			Usage:  &resp.Usage,
			Error:  resp.Error,
		}
	}()
	return out
}

func coreRequestHasImage(req *format.CoreRequest) bool {
	if req == nil {
		return false
	}
	for _, block := range req.System {
		if block.Type == "image" {
			return true
		}
	}
	for _, msg := range req.Messages {
		for _, block := range msg.Content {
			if coreBlockHasImage(block) {
				return true
			}
		}
	}
	return false
}

func coreBlockHasImage(block format.CoreContentBlock) bool {
	if block.Type == "image" {
		return true
	}
	if block.Type != "tool_result" {
		return false
	}
	for _, child := range block.ToolResultContent {
		if coreBlockHasImage(child) {
			return true
		}
	}
	return false
}

// ============================================================================
// Protocol-Agnostic Visual Bridge
// ============================================================================

// adapterCoreProvider wraps a ProviderAdapter + ProviderClient pair into a
// CoreProvider so the visual orchestrator can operate on format.CoreRequest
// without knowing the underlying protocol.
type adapterCoreProvider struct {
	adapter format.ProviderAdapter
	client  provider.ProviderClient
	session *session.Session
}

func newAdapterCoreProvider(adapter format.ProviderAdapter, client provider.ProviderClient) *adapterCoreProvider {
	return &adapterCoreProvider{adapter: adapter, client: client}
}

func (p *adapterCoreProvider) CreateCore(ctx context.Context, req *format.CoreRequest) (*format.CoreResponse, error) {
	upstreamAny, err := p.adapter.FromCoreRequest(ctx, req)
	if err != nil {
		return nil, err
	}
	if msgReq, ok := upstreamAny.(*anthropic.MessageRequest); ok {
		if p.session != nil {
			prependCachedThinking(msgReq, p.session)
		}
		upstreamAny = *msgReq
	}
	rawResp, err := p.client.CreateMessage(ctx, upstreamAny)
	if err != nil {
		return nil, err
	}
	if msgResp, ok := rawResp.(anthropic.MessageResponse); ok {
		rawResp = &msgResp
	}
	return p.adapter.ToCoreResponse(ctx, rawResp)
}

// wrapWithVisual returns a CoreProvider that wraps the upstream provider with
// visual orchestration, or nil when visual is not applicable for this model.
func (s *Server) wrapWithVisual(
	ctx context.Context,
	modelAlias string,
	preferred provider.ProviderCandidate,
	providerAdapter format.ProviderAdapter,
	sess *session.Session,
) visualpkg.CoreProvider {
	if s.pluginRegistry == nil || s.runtime == nil || modelAlias == "" || s.providerMgr == nil {
		return nil
	}

	cfg := s.runtime.Current().Config
	pluginCfg := config.PluginFromGlobalConfig(&cfg)
	visCfg, ok := visualpkg.ConfigForModel(pluginCfg, modelAlias)
	if !ok || visCfg.Provider == "" || visCfg.Model == "" {
		return nil
	}

	effectiveClient := preferred.Client
	if effectiveClient == nil {
		slog.Default().Warn("visual: no upstream client resolved")
		return nil
	}

	// Upstream CoreProvider = adapter + client.
	upstreamCP := newAdapterCoreProvider(providerAdapter, effectiveClient)
	upstreamCP.session = sess

	// Visual provider CoreProvider.
	visClient, err := s.providerMgr.ClientForKey(visCfg.Provider)
	if err != nil || visClient == nil {
		slog.Default().Warn("visual: provider not found", "visual_provider", visCfg.Provider, "model", modelAlias)
		return nil
	}
	visProtocol := s.providerMgr.ProtocolForKey(visCfg.Provider)
	if visProtocol == "" {
		slog.Default().Warn("visual: cannot resolve visual provider protocol")
		return nil
	}
	visAdapter, ok := s.adapterRegistry.GetProvider(visProtocol)
	if !ok {
		slog.Default().Warn("visual: no provider adapter for visual protocol", "protocol", visProtocol)
		return nil
	}
	visCP := newAdapterCoreProvider(visAdapter, visClient)

	return visualpkg.NewCoreBridge(upstreamCP, visCP, visCfg.Model, visCfg.MaxRounds, visCfg.MaxTokens)
}

// injectCoreWebSearch replaces web_search tools in coreReq.Tools with injected
// tavily_search/firecrawl_fetch tools when the provider's web search mode is "injected".
// Returns true if injection was applied.
func (s *Server) injectCoreWebSearch(ctx context.Context, coreReq *format.CoreRequest, preferred provider.ProviderCandidate, openAIReq openai.ResponsesRequest) bool {
	if s.providerMgr == nil {
		return false
	}
	wsMode := s.providerMgr.ResolvedWebSearch(preferred.ProviderKey)
	if wsMode != "injected" && wsMode != "auto" {
		return false
	}
	// For "auto" mode, check if keys are configured (fallback to injected).
	if wsMode == "auto" && s.runtime != nil {
		cfg := s.runtime.Current().Config
		if cfg.TavilyAPIKey == "" && cfg.FirecrawlAPIKey == "" {
			return false // no keys configured for "auto" fallback
		}
	}
	// Check if the request has a web_search tool.
	hasWebSearch := false
	for _, t := range openAIReq.Tools {
		if t.Type == "web_search" || t.Type == "web_search_preview" {
			hasWebSearch = true
			break
		}
	}
	if !hasWebSearch {
		return false
	}
	// Replace coreReq.Tools: keep non-web_search tools, add injected search tools.
	filtered := make([]format.CoreTool, 0, len(coreReq.Tools)+2)
	for _, t := range coreReq.Tools {
		if t.Name != "web_search" {
			filtered = append(filtered, t)
		}
	}
	cfg := s.runtime.Current().Config
	injected := websearchinjected.CoreTools(cfg.FirecrawlAPIKey)
	filtered = append(filtered, injected...)
	coreReq.Tools = filtered
	// Set tool_choice to auto so the model has freedom to call tavily_search.
	if coreReq.ToolChoice == nil {
		coreReq.ToolChoice = &format.CoreToolChoice{Mode: "auto"}
	}
	return true
}

// searchProvider wraps the websearchinjected orchestrator's behavior.
type searchProvider interface {
	CreateMessage(ctx context.Context, req anthropic.MessageRequest) (anthropic.MessageResponse, error)
	StreamMessage(ctx context.Context, req anthropic.MessageRequest) (anthropic.Stream, error)
}

// searchProviderAdapter adapts searchProvider to provider.ProviderClient.
type searchProviderAdapter struct {
	wrapped searchProvider
}

func (a *searchProviderAdapter) CreateMessage(ctx context.Context, req any) (any, error) {
	msgReq, ok := req.(anthropic.MessageRequest)
	if !ok {
		ptr, ok2 := req.(*anthropic.MessageRequest)
		if !ok2 {
			return nil, fmt.Errorf("search adapter: unexpected request type %T", req)
		}
		msgReq = *ptr
	}
	return a.wrapped.CreateMessage(ctx, msgReq)
}

func (a *searchProviderAdapter) StreamMessage(ctx context.Context, req any) (<-chan any, error) {
	msgReq, ok := req.(anthropic.MessageRequest)
	if !ok {
		ptr, ok2 := req.(*anthropic.MessageRequest)
		if !ok2 {
			return nil, fmt.Errorf("search adapter: unexpected request type %T", req)
		}
		msgReq = *ptr
	}
	stream, err := a.wrapped.StreamMessage(ctx, msgReq)
	if err != nil {
		return nil, err
	}
	out := make(chan any)
	go func() {
		defer close(out)
		defer stream.Close()
		for {
			ev, err := stream.Next()
			if err != nil {
				if err == io.EOF {
					return
				}
				return
			}
			out <- ev
		}
	}()
	return out, nil
}

func (a *searchProviderAdapter) AnthropicClient() *anthropic.Client { return nil }

// injectAnthropicWebSearch adds the Anthropic web_search_20250305 server tool
// to an anthropic.MessageRequest if not already present.
func injectAnthropicWebSearch(req *anthropic.MessageRequest) {
	for i, t := range req.Tools {
		if t.Name == "web_search" {
			// Already present — ensure Type is set correctly for Anthropic API.
			if t.Type != "web_search_20250305" && t.Type != "web_search_20260209" {
				req.Tools[i].Type = "web_search_20250305"
			}
			return
		}
	}
	maxUses := 8
	if req.Tools == nil {
		req.Tools = make([]anthropic.Tool, 0, 1)
	}
	req.Tools = append(req.Tools, anthropic.Tool{
		Name:    "web_search",
		Type:    "web_search_20250305",
		MaxUses: maxUses,
	})
}

// prependCachedThinking restores thinking blocks before assistant messages
// for DeepSeek thinking chain replay across conversation turns.
// It looks up cached thinking blocks from the session state and prepends them
// before tool_use and text assistant messages in the upstream request.
//
// Important: unlike PrependThinkingBlockForToolUse (which always targets the
// LAST message), this function targets the SPECIFIC assistant message that
// contains the tool_use, because in follow-up requests the last message
// is typically a user tool_result.
func prependCachedThinking(upstreamReq *anthropic.MessageRequest, sess *session.Session) {
	stateRaw, ok := sess.ExtensionData["deepseek_v4"]
	if !ok {
		return
	}
	state, ok := stateRaw.(*deepseekv4.State)
	if !ok {
		return
	}

	// For each assistant message, prepend cached thinking from the previous turn.
	for i := range upstreamReq.Messages {
		msg := &upstreamReq.Messages[i]
		if msg.Role != "assistant" || len(msg.Content) == 0 {
			continue
		}
		// Check if the message already has a thinking block.
		if hasThinkingBlock(msg.Content) {
			continue
		}
		// Try to prepend cached thinking by tool call ID (for tool_use messages).
		for _, block := range msg.Content {
			if block.Type == "tool_use" && block.ID != "" {
				if cached, ok := state.CachedForToolCall(block.ID); ok {
					// Prepend thinking block directly to this message, not to the last message.
					msg.Content = append([]anthropic.ContentBlock{normalizeThinkingBlock(cached)}, msg.Content...)
				}
				break
			}
		}
		// Fallback: prepend empty thinking block as response boundary.
		// Prevents model from continuing previous response text.
		if !hasThinkingBlock(msg.Content) {
			prepended, _ := deepseekv4.PrependRequiredThinkingForAssistantText(anthropicContentSliceToFormat(msg.Content))
			msg.Content = formatContentSliceToAnthropic(prepended)
		}
	}
}

// normalizeThinkingBlock ensures a thinking block has the correct Type field.
func normalizeThinkingBlock(block format.CoreContentBlock) anthropic.ContentBlock {
	return anthropic.ContentBlock{
		Type:      "thinking",
		Thinking:  block.ReasoningText,
		Signature: block.ReasoningSignature,
	}
}

// hasThinkingBlock checks if anthropic message content contains a thinking block.
func hasThinkingBlock(content []anthropic.ContentBlock) bool {
	for _, block := range content {
		if block.Type == "thinking" {
			return true
		}
	}
	return false
}

// prependCachedReasoningForChat restores reasoning_content on assistant messages
// for DeepSeek thinking chain replay across conversation turns.
// It looks up cached thinking blocks from the session state and sets them
// as reasoning_content on assistant messages that have tool_calls.
//
// For the Chat protocol path, this is the equivalent of prependCachedThinking
// (which operates on Anthropic messages).
func prependCachedReasoningForChat(chatReq *chat.ChatRequest, sess *session.Session) {
	// Session may be nil or missing ExtensionData (e.g., session resume after restart).
	// In that case, we still set reasoning_content to empty string — DeepSeek needs
	// the field present on every assistant message, even if empty.
	var state *deepseekv4.State
	if sess != nil {
		if stateRaw, ok := sess.ExtensionData["deepseek_v4"]; ok {
			state, _ = stateRaw.(*deepseekv4.State)
		}
	}

	for i := range chatReq.Messages {
		msg := &chatReq.Messages[i]
		if msg.Role != "assistant" {
			continue
		}
		// Skip if reasoning_content is already set.
		if msg.ReasoningContent != "" {
			continue
		}
		// Try to find cached thinking by tool call ID.
		if state != nil {
			for _, tc := range msg.ToolCalls {
				if tc.ID == "" {
					continue
				}
				if cached, ok := state.CachedForToolCall(tc.ID); ok {
					thinking := cached.ReasoningText
					if thinking == "" {
						thinking = cached.Text
					}
					if thinking != "" {
						msg.ReasoningContent = thinking
						break
					}
				}
			}
		}
		// Fallback: set empty reasoning_content to satisfy DeepSeek's requirement
		// that the field is present on every assistant message.
		if msg.ReasoningContent == "" && len(msg.ToolCalls) > 0 {
			msg.ReasoningContent = ""
		}
	}
}

// cacheReasoningForChat stores reasoning content from a Chat response
// into the session extension data for replay on subsequent turns.
func cacheReasoningForChat(sess *session.Session, toolCallIDs []string, reasoning string) {
	stateRaw, ok := sess.ExtensionData["deepseek_v4"]
	if !ok {
		return
	}
	state, ok := stateRaw.(*deepseekv4.State)
	if !ok {
		return
	}
	// The State caches thinking blocks by tool call ID.
	formatBlock := format.CoreContentBlock{
		Type:          "reasoning",
		ReasoningText: reasoning,
	}
	state.RememberForToolCalls(toolCallIDs, formatBlock)
}

// anthropicContentToFormat converts an anthropic.ContentBlock to format.CoreContentBlock.
func anthropicContentToFormat(block anthropic.ContentBlock) format.CoreContentBlock {
	out := format.CoreContentBlock{
		Type: block.Type,
		Text: block.Text,
	}
	switch block.Type {
	case "thinking":
		out.Type = "reasoning"
		out.ReasoningText = block.Thinking
		out.ReasoningSignature = block.Signature
	case "tool_use":
		out.ToolUseID = block.ID
		out.ToolName = block.Name
		out.ToolInput = block.Input
	}
	return out
}

// formatContentToAnthropic converts a format.CoreContentBlock to anthropic.ContentBlock.
func formatContentToAnthropic(block format.CoreContentBlock) anthropic.ContentBlock {
	out := anthropic.ContentBlock{
		Type: block.Type,
		Text: block.Text,
	}
	switch block.Type {
	case "reasoning":
		out.Type = "thinking"
		out.Thinking = block.ReasoningText
		out.Signature = block.ReasoningSignature
	case "tool_use":
		out.ID = block.ToolUseID
		out.Name = block.ToolName
		out.Input = block.ToolInput
	}
	return out
}

// anthropicContentBlockPtrToFormat converts *anthropic.ContentBlock to *format.CoreContentBlock.
func anthropicContentBlockPtrToFormat(block *anthropic.ContentBlock) *format.CoreContentBlock {
	if block == nil {
		return nil
	}
	b := anthropicContentToFormat(*block)
	return &b
}

// anthropicContentSliceToFormat converts []anthropic.ContentBlock to []format.CoreContentBlock.
func anthropicContentSliceToFormat(blocks []anthropic.ContentBlock) []format.CoreContentBlock {
	result := make([]format.CoreContentBlock, len(blocks))
	for i, b := range blocks {
		result[i] = anthropicContentToFormat(b)
	}
	return result
}

// formatContentSliceToAnthropic converts []format.CoreContentBlock to []anthropic.ContentBlock.
func formatContentSliceToAnthropic(blocks []format.CoreContentBlock) []anthropic.ContentBlock {
	result := make([]anthropic.ContentBlock, len(blocks))
	for i, b := range blocks {
		result[i] = formatContentToAnthropic(b)
	}
	return result
}
