package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
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
// The inbound request is already decoded by the client adapter's DecodeRequest;
// for the OpenAI Responses protocol, decodedReq is *openai.ResponsesRequest.
// Falls back to error when the required adapter is not found in the registry.
func (s *Server) handleWithAdapters(
	w http.ResponseWriter,
	r *http.Request,
	decodedReq any,
	isStream bool,
	rawBody []byte,
	route *provider.ResolvedRoute,
	clientProtocol string,
	model string,
) {
	ctx := r.Context()
	pm := s.activeProviderManager()

	log := slog.Default().With("model", model, "path", "adapter")

	// Defense-in-depth: ensure model is non-empty.
	if model == "" {
		log.Warn("adapter path: empty model")
		payload := openai.ErrorResponse{
			Error: openai.ErrorObject{
				Message: "model is required",
				Type:    "invalid_request_error",
				Code:    "missing_model",
			},
		}
		writeOpenAIError(w, http.StatusBadRequest, payload)
		s.onRequestCompleted(model, "", "", time.Now(), zeroUsage("adapter", "none"), 0, "error", "empty_model")
		return
	}

	// Get or create session for this request.
	requestStart := time.Now()
	sess := s.sessionForRequest(r)
	_ = sess
	var adapterCompleted bool
	var adapterHookErr string

	// Initialize trace record — use raw body bytes instead of re-marshalling.
	record := mbtrace.Record{
		HTTPRequest:    mbtrace.NewHTTPRequest(r),
		OpenAIRequest:  mbtrace.RawJSONOrString(rawBody),
		Model:          model,
		ClientProtocol: clientProtocol,
	}
	defer func() {
		s.writeTrace(record)
	}()

	// ------------------------------------------------------------------
	// 1. Resolve inbound client adapter.
	// ------------------------------------------------------------------
	client, ok := s.adapterRegistry.GetClient(clientProtocol)
	if !ok {
		log.Warn("adapter path: no client adapter", "protocol", clientProtocol)
		payload := openai.ErrorResponse{
			Error: openai.ErrorObject{
				Message: "adapter path precondition failed: no fallback available",
				Type:    "server_error",
				Code:    "adapter_fallback",
			},
		}
		record.Error = traceError("client_adapter", fmt.Errorf("no client adapter for %q", clientProtocol))
		record.OpenAIResponse = payload
		writeOpenAIError(w, http.StatusInternalServerError, payload)
		s.onRequestCompleted(model, "", "", requestStart, zeroUsage("adapter", "none"), 0, "error", "client_adapter")
		return
	}

	// ------------------------------------------------------------------
	// 2. Convert inbound request → CoreRequest.
	// ------------------------------------------------------------------
	coreReq, err := client.ToCoreRequest(ctx, decodedReq)
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
		s.onRequestCompleted(model, "", "", requestStart, zeroUsage("adapter", "none"), 0, "error", "to_core_request")
		return
	}

	// Capture inbound request in protocol-specific trace field so that
	// the corresponding trace category (e.g. "Anthropic") is written even
	// when the outbound protocol differs from the inbound protocol.
	// When outbound uses the same protocol, the upstream assignment in
	// the switch block below overwrites this value — that is intentional
	// (the upstream request carries the resolved model name).
	switch clientProtocol {
	case config.ProtocolAnthropic:
		if anthReq, ok := decodedReq.(*anthropic.MessageRequest); ok {
			record.AnthropicRequest = anthReq
		}
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
		s.onRequestCompleted(model, "", "", requestStart, zeroUsage("adapter", "none"), 0, "error", "no_provider_candidate")
		return
	}
	record.ProviderProtocol = preferred.Protocol

	defer func() {
		if !adapterCompleted {
			errMsg := adapterHookErr
			if errMsg == "" {
				errMsg = "unknown_adapter_error"
			}
			s.onRequestCompleted(
				model, preferred.UpstreamModel, preferred.ProviderKey,
				requestStart,
				zeroUsage(string(preferred.Protocol), "adapter_error"),
				0, "error", errMsg,
			)
		}
	}()
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
		adapterHookErr = "provider_adapter"
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

	wsMode := resolvedWebSearchMode(pm, model, preferred)

	// Inject web search tools at Core level if mode is "injected".
	// This replaces web_search/web_search_preview with tavily_search/firecrawl_fetch tools.
	wsInjected := s.injectCoreWebSearch(ctx, coreReq, preferred, model, wsMode)
	searchCfg := s.resolvedSearchConfig(preferred.ProviderKey, model)

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
		adapterHookErr = "from_core_request"
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
			adapterHookErr = "upstream_type"
			writeOpenAIError(w, http.StatusInternalServerError, payload)
			return
		}
		record.AnthropicRequest = upstreamReq

		// Inject native web_search tool when the resolved candidate supports it.
		if wsMode == "enabled" {
			injectAnthropicWebSearch(upstreamReq)
		}

		// Prepend cached reasoning blocks for DeepSeek thinking chain replay.
		if s.pluginRegistry != nil && sess != nil {
			prependCachedThinking(upstreamReq, sess)
		}

		finalizeAnthropicUpstream := func(_ context.Context, upstream any) (any, error) {
			msgReq, err := normalizeAnthropicRequest(upstream)
			if err != nil {
				return nil, err
			}
			if wsMode == "enabled" {
				injectAnthropicWebSearch(&msgReq)
			}
			if s.pluginRegistry != nil && sess != nil {
				prependCachedThinking(&msgReq, sess)
			}
			return &msgReq, nil
		}

		// If streaming, use streaming path.
		if isStream {
			adapterCompleted = true
			s.handleAdapterStream(w, r, ctx, model, rawBody, coreReq, upstreamReq, preferred, clientProtocol, wsMode, wsInjected)
			record.OpenAIRequest = nil
			return
		}

		// Non-streaming upstream call.
		effectiveProvider := preferred.Client
		if effectiveProvider == nil {
			log.Error("adapter path: no upstream provider resolved")
			payload := openai.ErrorResponse{
				Error: openai.ErrorObject{
					Message: fmt.Sprintf("no upstream provider for model %q", model),
					Type:    "server_error",
					Code:    "provider_error",
				},
			}
			record.Error = traceError("resolve_provider", fmt.Errorf("no upstream provider for %q", model))
			record.OpenAIResponse = payload
			adapterHookErr = "resolve_provider"
			writeOpenAIError(w, http.StatusBadGateway, payload)
			return
		}

		// Wrap provider with search orchestrator if web search is "injected".
		if wsInjected {
			if acc, ok := effectiveProvider.(provider.AnthropicClientAccessor); ok {
				wrapped := websearchinjected.WrapProvider(
					acc.AnthropicClient(),
					searchCfg.tavilyKey, searchCfg.firecrawlKey, searchCfg.maxRounds, s.proxyHTTP,
				)
				effectiveProvider = &searchProviderAdapter{wrapped: wrapped}
			}
		}

		// Wrap with visual orchestrator at Core level if enabled for this model.
		// This uses CoreProvider, which is protocol-agnostic.
		if visProv := s.wrapWithVisual(ctx, model, preferred, providerAdapter, finalizeAnthropicUpstream); visProv != nil {
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
					record.AnthropicResponse = &msgResp
					coreResp, err = providerToCoreResponse(ctx, providerAdapter, coreReq, &msgResp)
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
			adapterHookErr = "create_message"
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
			adapterHookErr = "upstream_type"
			writeOpenAIError(w, http.StatusInternalServerError, payload)
			return
		}

		// Prepend cached reasoning for DeepSeek thinking chain replay.
		if s.pluginRegistry != nil && sess != nil {
			prependCachedReasoningForChat(chatReq, sess)
		}

		if isStream {
			adapterCompleted = true
			s.handleAdapterStream(w, r, ctx, model, rawBody, coreReq, chatReq, preferred, clientProtocol, wsMode, wsInjected)
			record.OpenAIRequest = nil
			return
		}

		chatClientRaw := s.activeChatClient(preferred.ProviderKey)
		if chatClientRaw == nil {
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
			adapterHookErr = "chat_client"
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
			adapterHookErr = "chat_client_type"
			writeOpenAIError(w, http.StatusInternalServerError, payload)
			return
		}

		record.ChatRequest = chatReq

		// finalizeChatUpstream applies per-round mutations (cached reasoning
		// replay) on every orchestrator round. prependCachedReasoningForChat
		// is idempotent so duplicate application against the initial chatReq
		// above is safe.
		finalizeChatUpstream := func(_ context.Context, upstream any) (any, error) {
			req, ok := upstream.(*chat.ChatRequest)
			if !ok {
				return nil, fmt.Errorf("finalizeChatUpstream: expected *chat.ChatRequest, got %T", upstream)
			}
			if s.pluginRegistry != nil && sess != nil {
				prependCachedReasoningForChat(req, sess)
			}
			return req, nil
		}

		// Wrap with visual orchestrator at Core level if enabled for this model.
		// preferred.Client carries an anthropic-shaped adapter; substitute the
		// real chat client so the orchestrator's per-round upstream calls hit
		// the chat-protocol endpoint instead.
		visualCandidate := preferred
		visualCandidate.Client = &chatProviderClient{c: chatClient}
		if visProv := s.wrapWithVisual(ctx, model, visualCandidate, providerAdapter, finalizeChatUpstream); visProv != nil {
			coreResp, err = visProv.CreateCore(ctx, coreReq)
			if err != nil {
				log.Error("adapter path: chat visual CreateCore failed", "error", err)
				payload := openai.ErrorResponse{
					Error: openai.ErrorObject{
						Message: fmt.Sprintf("visual orchestration failed: %v", err),
						Type:    "server_error",
						Code:    "provider_error",
					},
				}
				record.Error = traceError("chat_visual_core", err)
				record.OpenAIResponse = payload
				adapterHookErr = "chat_visual_core"
				writeOpenAIError(w, http.StatusBadGateway, payload)
				return
			}
			break
		}

		var chatResp *chat.ChatResponse
		if wsInjected {
			chatResp, err = s.executeChatSearchLoop(ctx, chatClient, chatReq, searchCfg.tavilyKey, searchCfg.firecrawlKey, searchCfg.maxRounds)
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
			adapterHookErr = "chat_api"
			writeOpenAIError(w, http.StatusBadGateway, payload)
			return
		}
		record.ChatResponse = chatResp

		coreResp, err = providerToCoreResponse(ctx, providerAdapter, coreReq, chatResp)
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
			adapterHookErr = "to_core_response"
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
			adapterHookErr = "upstream_type"
			writeOpenAIError(w, http.StatusInternalServerError, payload)
			return
		}

		if isStream {
			adapterCompleted = true
			s.handleAdapterStream(w, r, ctx, model, rawBody, coreReq, googleReq, preferred, clientProtocol, wsMode, wsInjected)
			record.OpenAIRequest = nil
			return
		}

		googleClientRaw := s.activeGoogleClient(preferred.ProviderKey)
		if googleClientRaw == nil {
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
			adapterHookErr = "google_client"
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
			adapterHookErr = "google_client_type"
			writeOpenAIError(w, http.StatusInternalServerError, payload)
			return
		}

		record.UpstreamRequest = googleReq
		// Wrap with visual orchestrator if enabled for this model.
		googlePreferred := preferred
		googlePreferred.Client = &googleProviderClient{c: googleClient, model: googlePreferred.UpstreamModel}
		if visProv := s.wrapWithVisual(ctx, model, googlePreferred, providerAdapter, nil); visProv != nil {
			var visErr error
			coreResp, visErr = visProv.CreateCore(ctx, coreReq)
			if visErr != nil {
				log.Error("adapter path: google visual CreateCore failed", "error", visErr)
				payload := openai.ErrorResponse{
					Error: openai.ErrorObject{
						Message: fmt.Sprintf("google visual orchestration failed: %v", visErr),
						Type:    "server_error",
						Code:    "provider_error",
					},
				}
				record.Error = traceError("google_visual_core", visErr)
				record.OpenAIResponse = payload
				adapterHookErr = "google_visual_core"
				writeOpenAIError(w, http.StatusBadGateway, payload)
				return
			}
			break
		}

		var googleResp *google.GenerateContentResponse
		if wsInjected {
			googleResp, err = s.executeGoogleSearchLoop(ctx, googleClient, preferred.UpstreamModel, googleReq, searchCfg.tavilyKey, searchCfg.firecrawlKey, searchCfg.maxRounds)
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
			adapterHookErr = "google_api"
			writeOpenAIError(w, http.StatusBadGateway, payload)
			return
		}
		record.UpstreamResponse = googleResp

		coreResp, err = providerToCoreResponse(ctx, providerAdapter, coreReq, googleResp)
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
			adapterHookErr = "to_core_response"
			writeOpenAIError(w, http.StatusInternalServerError, payload)
			return
		}

	case config.ProtocolOpenAIResponse:
		responsesReq, ok := upstreamAny.(*openai.ResponsesRequest)
		if !ok {
			log.Error("adapter path: unexpected openai-response upstream type", "type", fmt.Sprintf("%T", upstreamAny))
			payload := openai.ErrorResponse{
				Error: openai.ErrorObject{
					Message: "unexpected openai-response upstream request type",
					Type:    "server_error",
					Code:    "internal_error",
				},
			}
			record.Error = traceError("upstream_type", fmt.Errorf("unexpected openai-response type %T", upstreamAny))
			record.OpenAIResponse = payload
			adapterHookErr = "upstream_type"
			writeOpenAIError(w, http.StatusInternalServerError, payload)
			return
		}

		if isStream {
			adapterCompleted = true
			s.handleAdapterStream(w, r, ctx, model, rawBody, coreReq, responsesReq, preferred, clientProtocol, wsMode, wsInjected)
			record.OpenAIRequest = nil
			return
		}

		responsesClientRaw := s.activeResponsesClient(preferred.ProviderKey)
		if responsesClientRaw == nil {
			log.Error("adapter path: no responses client", "provider", preferred.ProviderKey)
			payload := openai.ErrorResponse{
				Error: openai.ErrorObject{
					Message: fmt.Sprintf("no responses client for provider %q", preferred.ProviderKey),
					Type:    "server_error",
					Code:    "provider_error",
				},
			}
			record.Error = traceError("responses_client", fmt.Errorf("no responses client for %q", preferred.ProviderKey))
			record.OpenAIResponse = payload
			adapterHookErr = "responses_client"
			writeOpenAIError(w, http.StatusBadGateway, payload)
			return
		}
		responsesClient, ok := responsesClientRaw.(*openai.Client)
		if !ok {
			log.Error("adapter path: invalid responses client type", "provider", preferred.ProviderKey)
			payload := openai.ErrorResponse{
				Error: openai.ErrorObject{
					Message: fmt.Sprintf("invalid responses client for provider %q", preferred.ProviderKey),
					Type:    "server_error",
					Code:    "internal_error",
				},
			}
			record.Error = traceError("responses_client_type", fmt.Errorf("invalid responses client for %q", preferred.ProviderKey))
			record.OpenAIResponse = payload
			adapterHookErr = "responses_client_type"
			writeOpenAIError(w, http.StatusInternalServerError, payload)
			return
		}

		var responsesResp *openai.Response
		responsesResp, err = responsesClient.CreateResponse(ctx, responsesReq)
		if err != nil {
			log.Error("adapter path: Responses API call failed", "error", err)
			payload := openai.ErrorResponse{
				Error: openai.ErrorObject{
					Message: fmt.Sprintf("responses upstream error: %v", err),
					Type:    "server_error",
					Code:    "provider_error",
				},
			}
			record.Error = traceError("responses_api", err)
			record.OpenAIResponse = payload
			adapterHookErr = "responses_api"
			writeOpenAIError(w, http.StatusBadGateway, payload)
			return
		}
		record.UpstreamRequest = responsesReq
		record.UpstreamResponse = responsesResp

		coreResp, err = providerToCoreResponse(ctx, providerAdapter, coreReq, responsesResp)
		if err != nil {
			log.Error("adapter path: Responses ToCoreResponse failed", "error", err)
			payload := openai.ErrorResponse{
				Error: openai.ErrorObject{
					Message: fmt.Sprintf("responses response conversion failed: %v", err),
					Type:    "server_error",
					Code:    "conversion_error",
				},
			}
			record.Error = traceError("to_core_response", err)
			record.OpenAIResponse = payload
			adapterHookErr = "to_core_response"
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
		adapterHookErr = "unsupported_protocol"
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
		adapterHookErr = "to_core_response"
		writeOpenAIError(w, http.StatusInternalServerError, payload)
		return
	}

	// ------------------------------------------------------------------

	// Propagate codex_tool_map from CoreRequest to CoreResponse.
	if coreReq != nil && coreReq.Extensions != nil && coreResp != nil {
		for _, key := range []string{"codex_tool_map"} {
			if val, ok := coreReq.Extensions[key]; ok {
				if coreResp.Extensions == nil {
					coreResp.Extensions = make(map[string]any)
				}
				coreResp.Extensions[key] = val
			}
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
		adapterHookErr = "from_core_response"
		writeOpenAIError(w, http.StatusInternalServerError, payload)
		return
	}
	rememberAdapterResponseContent(s.pluginRegistry, sess, model, coreResp)

	// ------------------------------------------------------------------
	// 8. Write the response in the inbound protocol format.
	// ------------------------------------------------------------------
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	switch clientProtocol {
	case config.ProtocolOpenAIResponse:
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
			adapterHookErr = "output_type"
			writeOpenAIError(w, http.StatusInternalServerError, payload)
			return
		}
		json.NewEncoder(w).Encode(out)
		record.OpenAIResponse = out

	case config.ProtocolAnthropic:
		out, ok := outAny.(*anthropic.MessageResponse)
		if !ok {
			log.Error("adapter path: unexpected output type for anthropic", "type", fmt.Sprintf("%T", outAny))
			writeAnthropicError(w, http.StatusInternalServerError, "server_error", "unexpected output response type")
			return
		}
		json.NewEncoder(w).Encode(out)
		record.OpenAIResponse = out

	case config.ProtocolOpenAIChat:
		out, ok := outAny.(*chat.ChatResponse)
		if !ok {
			log.Error("adapter path: unexpected output type for chat", "type", fmt.Sprintf("%T", outAny))
			payload := openai.ErrorResponse{
				Error: openai.ErrorObject{
					Message: "unexpected output response type",
					Type:    "server_error",
					Code:    "internal_error",
				},
			}
			record.Error = traceError("output_type", fmt.Errorf("unexpected output type %T", outAny))
			record.OpenAIResponse = payload
			adapterHookErr = "output_type"
			writeOpenAIError(w, http.StatusInternalServerError, payload)
			return
		}
		json.NewEncoder(w).Encode(out)
		record.OpenAIResponse = out

	case config.ProtocolGoogleGenAI:
		out, ok := outAny.(*google.GenerateContentResponse)
		if !ok {
			log.Error("adapter path: unexpected output type for gemini", "type", fmt.Sprintf("%T", outAny))
			writeGeminiError(w, http.StatusInternalServerError, "Internal Error", "unexpected output response type", "INTERNAL")
			return
		}
		json.NewEncoder(w).Encode(out)
		record.OpenAIResponse = out

	default:
		log.Error("adapter path: unsupported client protocol for output", "protocol", clientProtocol)
		payload := openai.ErrorResponse{
			Error: openai.ErrorObject{
				Message: fmt.Sprintf("unsupported client protocol %q", clientProtocol),
				Type:    "server_error",
				Code:    "internal_error",
			},
		}
		record.Error = traceError("output_type", fmt.Errorf("unsupported client protocol %q", clientProtocol))
		record.OpenAIResponse = payload
		adapterHookErr = "output_type"
		writeOpenAIError(w, http.StatusInternalServerError, payload)
		return
	}
	adapterCompleted = true

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
		reqCost := computeCostWithProviderPricing(pm, s.stats, model, preferred.UpstreamModel, preferred.ProviderKey, billingUsage)
		log.Info("请求完成",
			"request_model", model,
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
			model, preferred.UpstreamModel, preferred.ProviderKey,
			requestStart, usage,
			reqCost, "success", "",
		)

		// Record usage statistics.
		if s.stats != nil {
			s.stats.Record(model, preferred.UpstreamModel, stats.Usage{
				InputTokens:              coreResp.Usage.InputTokens,
				OutputTokens:             coreResp.Usage.OutputTokens,
				CacheReadInputTokens:     coreResp.Usage.CachedInputTokens,
				CacheCreationInputTokens: 0,
			})
		}
	}
}

func rememberAdapterResponseContent(registry *plugin.Registry, sess *session.Session, model string, coreResp *format.CoreResponse) {
	if registry == nil || sess == nil || coreResp == nil {
		return
	}
	reqCtx := &plugin.RequestContext{
		ModelAlias:  model,
		SessionData: sess.ExtensionData,
	}
	for _, msg := range coreResp.Messages {
		if msg.Role != "assistant" || len(msg.Content) == 0 {
			continue
		}
		registry.RememberContent(reqCtx, msg.Content)
	}
}

func rememberStreamResponseContent(registry *plugin.Registry, sess *session.Session, model string, resp *openai.Response) bool {
	if registry == nil || sess == nil || resp == nil {
		return false
	}
	reqCtx := &plugin.RequestContext{
		ModelAlias:  model,
		SessionData: sess.ExtensionData,
	}
	var pending []format.CoreContentBlock
	remembered := false
	flush := func() {
		if len(pending) == 0 {
			return
		}
		registry.RememberContent(reqCtx, pending)
		pending = nil
		remembered = true
	}

	for _, item := range resp.Output {
		blocks := streamOutputItemToCoreBlocks(item)
		if len(blocks) == 0 {
			continue
		}
		if item.Type == "reasoning" && len(pending) > 0 {
			flush()
		}
		pending = append(pending, blocks...)
	}
	flush()
	return remembered
}

func streamOutputItemToCoreBlocks(item openai.OutputItem) []format.CoreContentBlock {
	switch item.Type {
	case "reasoning":
		return reasoningBlocksFromStreamOutput(item.Summary)
	case "function_call", "custom_tool_call", "local_shell_call":
		toolUseID := firstNonEmptyString(item.CallID, item.ID)
		if toolUseID == "" {
			return nil
		}
		return []format.CoreContentBlock{{
			Type:          "tool_use",
			ToolUseID:     toolUseID,
			ToolName:      item.Name,
			ToolNamespace: item.Namespace,
			ToolInput:     streamOutputToolInput(item),
		}}
	case "message":
		blocks := make([]format.CoreContentBlock, 0, len(item.Content))
		for _, part := range item.Content {
			if (part.Type == "text" || part.Type == "output_text") && part.Text != "" {
				blocks = append(blocks, format.CoreContentBlock{
					Type: "text",
					Text: part.Text,
				})
			}
		}
		return blocks
	default:
		return nil
	}
}

func reasoningBlocksFromStreamOutput(summary []openai.ReasoningItemSummary) []format.CoreContentBlock {
	blocks := make([]format.CoreContentBlock, 0, len(summary))
	for _, item := range summary {
		if item.Text == "" && item.Signature == "" {
			continue
		}
		if block, ok := deepseekv4.DecodeThinkingSummary(item.Text); ok {
			if block.ReasoningSignature == "" && item.Signature != "" {
				block.ReasoningSignature = item.Signature
			}
			blocks = append(blocks, block)
			continue
		}
		blocks = append(blocks, format.CoreContentBlock{
			Type:               "reasoning",
			ReasoningText:      item.Text,
			ReasoningSignature: item.Signature,
		})
	}
	return blocks
}

func streamOutputToolInput(item openai.OutputItem) json.RawMessage {
	if item.Arguments != "" && json.Valid([]byte(item.Arguments)) {
		return json.RawMessage(item.Arguments)
	}
	if item.Input == "" {
		return nil
	}
	payload, err := json.Marshal(map[string]string{"input": item.Input})
	if err != nil {
		return nil
	}
	return payload
}

func firstNonEmptyString(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// handleAdapterStream handles the streaming path through adapter dispatch.
// interceptCoreUsage wraps a CoreStreamEvent channel to capture usage from
// CoreEventCompleted at the CoreIR layer, before protocol-specific conversion.
func interceptCoreUsage(events <-chan format.CoreStreamEvent, usage *format.CoreUsage) <-chan format.CoreStreamEvent {
	out := make(chan format.CoreStreamEvent, 64)
	go func() {
		defer close(out)
		for ev := range events {
			if ev.Type == format.CoreEventCompleted && ev.Usage != nil {
				usage.InputTokens = ev.Usage.InputTokens
				usage.OutputTokens = ev.Usage.OutputTokens
				usage.CachedInputTokens = ev.Usage.CachedInputTokens
				usage.CacheCreationInputTokens = ev.Usage.CacheCreationInputTokens
				usage.CacheReadInputTokens = ev.Usage.CacheReadInputTokens
				usage.ReasoningTokens = ev.Usage.ReasoningTokens
			}
			out <- ev
		}
	}()
	return out
}

func (s *Server) handleAdapterStream(
	w http.ResponseWriter,
	r *http.Request,
	ctx context.Context,
	model string,
	rawBody []byte,
	coreReq *format.CoreRequest,
	upstreamReq any,
	candidate provider.ProviderCandidate,
	clientProtocol string,
	wsMode string,
	wsInjected bool,
) {
	log := slog.Default().With("model", model, "path", "adapter_stream")
	pm := s.activeProviderManager()

	// Track when the request started for latency measurement.
	requestStart := time.Now()

	// Get or create session for this request.
	sess := s.sessionForRequest(r)
	_ = sess

	// Initialize trace record.
	streamRecord := mbtrace.Record{
		HTTPRequest:    mbtrace.NewHTTPRequest(r),
		OpenAIRequest:  mbtrace.RawJSONOrString(rawBody),
		Model:          model,
		ClientProtocol: clientProtocol,
	}
	defer func() {
		s.writeTrace(streamRecord)
	}()

	if candidate.Protocol == config.ProtocolAnthropic && coreRequestHasImage(coreReq) {
		if providerAdapter := s.adapterRegistryProvider(config.ProtocolAnthropic); providerAdapter != nil {
			finalizeAnthropicUpstream := func(_ context.Context, upstream any) (any, error) {
				msgReq, err := normalizeAnthropicRequest(upstream)
				if err != nil {
					return nil, err
				}
				if wsMode == "enabled" {
					injectAnthropicWebSearch(&msgReq)
				}
				if s.pluginRegistry != nil && sess != nil {
					prependCachedThinking(&msgReq, sess)
				}
				return &msgReq, nil
			}
			if visProv := s.wrapWithVisual(ctx, model, candidate, providerAdapter, finalizeAnthropicUpstream); visProv != nil {
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
				s.writeCoreResponseAsOpenAIStream(w, ctx, model, coreReq, coreResp, candidate, clientProtocol, requestStart, &streamRecord)
				return
			}
		}
	}

	// Protocol-specific upstream streaming: get stream + convert to CoreStreamEvent.
	var coreEvents <-chan format.CoreStreamEvent
	var streamUsage format.CoreUsage
	var providerStream format.ProviderStreamAdapter
	var sr *format.StreamResult  // result from ToCoreStream, captures events + buffer
	var providerBuf func() []any // per-request provider stream buffer (from ToCoreStream StreamResult)
	var clientBuf func() []any   // per-request client stream buffer (from OpenAI FromCoreStream)

	streamRecord.ProviderProtocol = candidate.Protocol

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
					Message: fmt.Sprintf("no upstream provider for model %q", model),
					Type:    "server_error",
					Code:    "provider_error",
				},
			}
			streamRecord.Error = traceError("stream_resolve_provider", fmt.Errorf("no upstream provider for %q", model))
			streamRecord.OpenAIResponse = payload
			writeOpenAIError(w, http.StatusBadGateway, payload)
			return
		}

		var visCoreProvider visualpkg.CoreProvider
		hasImage := coreRequestHasImage(coreReq)
		if hasImage {
			if provAdapter, ok := s.adapterRegistry.GetProvider(candidate.Protocol); ok {
				finalizeAnthropicUpstream := func(_ context.Context, upstream any) (any, error) {
					msgReq, err := normalizeAnthropicRequest(upstream)
					if err != nil {
						return nil, err
					}
					if wsMode == "enabled" {
						injectAnthropicWebSearch(&msgReq)
					}
					if s.pluginRegistry != nil && sess != nil {
						prependCachedThinking(&msgReq, sess)
					}
					return &msgReq, nil
				}
				if visProv := s.wrapWithVisual(ctx, model, candidate, provAdapter, finalizeAnthropicUpstream); visProv != nil {
					visCoreProvider = visProv
				}
			}
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
		// Strip image blocks from anthropic request if visual extension is enabled
		// and images are present. This prevents base64 image data from being sent to
		// text-only models while keeping pure-text requests on the real streaming path.
		if hasImage && s.pluginRegistry != nil && s.runtime != nil && model != "" {
			cfgV := s.runtime.Current().Config
			visCfg, visOk := visualpkg.ConfigForModelFromResolvedConfig(cfgV, model)
			if visOk && visCfg.Provider != "" && visCfg.Model != "" {
				strippedReq, _ := visualpkg.StripImagesFromAnthropic(*anthReq)
				anthReq = &strippedReq
			}
		}
		if visCoreProvider != nil {
			coreResp, err := visCoreProvider.CreateCore(ctx, coreReq)
			if err != nil {
				log.Error("adapter stream: visual CreateCore failed", "error", err)
				payload := openai.ErrorResponse{
					Error: openai.ErrorObject{
						Message: fmt.Sprintf("visual stream orchestration failed: %v", err),
						Type:    "server_error",
						Code:    "provider_error",
					},
				}
				streamRecord.Error = traceError("stream_visual_core", err)
				streamRecord.OpenAIResponse = payload
				writeOpenAIError(w, http.StatusBadGateway, payload)
				return
			}
			coreEvents = coreResponseToCoreStream(ctx, coreResp)
		} else {
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
			sr, err = providerToCoreStream(ctx, providerStream, coreReq, stream)
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
			coreEvents = sr.Events
			if sr.StreamBuffer != nil {
				providerBuf = sr.StreamBuffer
			}
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

		// Strip image blocks from chat request when the visual extension is
		// enabled for this model. The visual orchestrator does not run on the
		// streaming path; without stripping, raw base64 image data would be
		// forwarded to a text-only upstream that cannot consume it and would
		// burn input tokens. Mirrors the anthropic streaming behavior above.
		if s.pluginRegistry != nil && s.runtime != nil && model != "" {
			cfgV := s.runtime.Current().Config
			visCfg, visOk := visualpkg.ConfigForModelFromResolvedConfig(cfgV, model)
			if visOk && visCfg.Provider != "" && visCfg.Model != "" {
				strippedReq, _ := visualpkg.StripImagesFromChat(*chatReq)
				chatReq = &strippedReq
			}
		}

		// Prepend cached reasoning for DeepSeek thinking chain replay.
		if s.pluginRegistry != nil && sess != nil {
			prependCachedReasoningForChat(chatReq, sess)
		}

		chatClientRaw := s.activeChatClient(candidate.ProviderKey)
		if chatClientRaw == nil {
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
			log.Error("adapter stream: invalid chat client type", "provider", candidate.ProviderKey)
			payload := openai.ErrorResponse{
				Error: openai.ErrorObject{
					Message: fmt.Sprintf("invalid chat client for provider %q", candidate.ProviderKey),
					Type:    "server_error",
					Code:    "internal_error",
				},
			}
			streamRecord.Error = traceError("stream_chat_client_type", fmt.Errorf("invalid chat client for %q", candidate.ProviderKey))
			streamRecord.OpenAIResponse = payload
			writeOpenAIError(w, http.StatusInternalServerError, payload)
			return
		}

		streamRecord.ChatRequest = chatReq
		streamRecord.UpstreamRequest = chatReq
		var chatStream <-chan chat.ChatStreamChunk
		var err error

		providerAdapter, ok := s.adapterRegistry.GetProvider(config.ProtocolOpenAIChat)
		if !ok {
			log.Warn("adapter stream: no chat provider adapter for visual path")
		}

		// Visual orchestrator for streaming path: non-streaming orchestration
		// → synthetic stream events, matching the anthropic streaming pattern.
		if s.pluginRegistry != nil && s.runtime != nil && model != "" && ok && providerAdapter != nil && coreRequestHasImage(coreReq) {
			cfgV := s.runtime.Current().Config
			visCfg, visOk := visualpkg.ConfigForModelFromResolvedConfig(cfgV, model)
			if visOk && visCfg.Provider != "" && visCfg.Model != "" {
				finalizeUpstream := func(_ context.Context, upstream any) (any, error) {
					req, ok := upstream.(*chat.ChatRequest)
					if !ok {
						return nil, fmt.Errorf("chat visual finalize: expected *chat.ChatRequest, got %T", upstream)
					}
					if s.pluginRegistry != nil && sess != nil {
						prependCachedReasoningForChat(req, sess)
					}
					return req, nil
				}
				visCandidate := candidate
				visCandidate.Client = &chatProviderClient{c: chatClient}
				if visProv := s.wrapWithVisual(ctx, model, visCandidate, providerAdapter, finalizeUpstream); visProv != nil {
					coreResp, visErr := visProv.CreateCore(ctx, coreReq)
					if visErr != nil {
						log.Error("adapter stream: chat visual CreateCore failed", "error", visErr)
						payload := openai.ErrorResponse{
							Error: openai.ErrorObject{
								Message: fmt.Sprintf("chat visual orchestration failed: %v", visErr),
								Type:    "server_error",
								Code:    "provider_error",
							},
						}
						streamRecord.Error = traceError("stream_chat_visual", visErr)
						streamRecord.OpenAIResponse = payload
						writeOpenAIError(w, http.StatusBadGateway, payload)
						return
					}
					coreEvents = coreResponseToCoreStream(ctx, coreResp)
					break
				}
			}
		}

		if wsInjected {
			searchCfg := s.resolvedSearchConfig(candidate.ProviderKey, model)
			chatStream, err = s.chatSearchBufferedStream(ctx, chatClient, chatReq, searchCfg.tavilyKey, searchCfg.firecrawlKey, searchCfg.maxRounds)
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
		sr, err = providerToCoreStream(ctx, providerStream, coreReq, chatStream)
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
		coreEvents = sr.Events
		if sr.StreamBuffer != nil {
			providerBuf = sr.StreamBuffer
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

		googleClientRaw := s.activeGoogleClient(candidate.ProviderKey)
		if googleClientRaw == nil {
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
			log.Error("adapter stream: invalid google client type", "provider", candidate.ProviderKey)
			payload := openai.ErrorResponse{
				Error: openai.ErrorObject{
					Message: fmt.Sprintf("invalid google client for provider %q", candidate.ProviderKey),
					Type:    "server_error",
					Code:    "internal_error",
				},
			}
			streamRecord.Error = traceError("stream_google_client_type", fmt.Errorf("invalid google client for %q", candidate.ProviderKey))
			streamRecord.OpenAIResponse = payload
			writeOpenAIError(w, http.StatusInternalServerError, payload)
			return
		}

		streamRecord.UpstreamRequest = googleReq

		// Visual orchestrator for streaming path: non-streaming orchestration
		// → synthetic stream events, matching the anthropic/chat streaming pattern.
		providerAdapter, ok := s.adapterRegistry.GetProvider(config.ProtocolGoogleGenAI)
		if ok && providerAdapter != nil && s.runtime != nil && model != "" {
			cfgV := s.runtime.Current().Config
			visCfg, visOk := visualpkg.ConfigForModelFromResolvedConfig(cfgV, model)
			if visOk && visCfg.Provider != "" && visCfg.Model != "" {
				visCandidate := candidate
				visCandidate.Client = &googleProviderClient{c: googleClient, model: candidate.UpstreamModel}
				if visProv := s.wrapWithVisual(ctx, model, visCandidate, providerAdapter, nil); visProv != nil {
					coreResp, visErr := visProv.CreateCore(ctx, coreReq)
					if visErr != nil {
						log.Error("adapter stream: google visual CreateCore failed", "error", visErr)
						payload := openai.ErrorResponse{
							Error: openai.ErrorObject{
								Message: fmt.Sprintf("google visual orchestration failed: %v", visErr),
								Type:    "server_error",
								Code:    "provider_error",
							},
						}
						streamRecord.Error = traceError("stream_google_visual", visErr)
						streamRecord.OpenAIResponse = payload
						writeOpenAIError(w, http.StatusBadGateway, payload)
						return
					}
					coreEvents = coreResponseToCoreStream(ctx, coreResp)
					break
				}
			}
		}

		if wsInjected {
			searchCfg := s.resolvedSearchConfig(candidate.ProviderKey, model)
			googleResp, err := s.executeGoogleSearchLoop(ctx, googleClient, candidate.UpstreamModel, googleReq, searchCfg.tavilyKey, searchCfg.firecrawlKey, searchCfg.maxRounds)
			if err != nil {
				log.Error("adapter stream: injected google search loop failed", "error", err)
				payload := openai.ErrorResponse{
					Error: openai.ErrorObject{
						Message: fmt.Sprintf("google stream error: %v", err),
						Type:    "server_error",
						Code:    "provider_error",
					},
				}
				streamRecord.Error = traceError("stream_google_injected", err)
				streamRecord.OpenAIResponse = payload
				writeOpenAIError(w, http.StatusBadGateway, payload)
				return
			}
			streamRecord.UpstreamResponse = googleResp
			googleProvAdapter, ok := s.adapterRegistry.GetProvider(config.ProtocolGoogleGenAI)
			if !ok {
				log.Error("adapter stream: no google provider adapter for injected path")
				payload := openai.ErrorResponse{
					Error: openai.ErrorObject{
						Message: "google stream adapter not available",
						Type:    "server_error",
						Code:    "adapter_fallback",
					},
				}
				streamRecord.Error = traceError("stream_google_adapter", fmt.Errorf("no google provider adapter"))
				streamRecord.OpenAIResponse = payload
				writeOpenAIError(w, http.StatusInternalServerError, payload)
				return
			}
			googleAdapter, ok := googleProvAdapter.(interface {
				ToCoreResponse(context.Context, any) (*format.CoreResponse, error)
			})
			if !ok {
				log.Error("adapter stream: google adapter lacks ToCoreResponse for injected path")
				payload := openai.ErrorResponse{
					Error: openai.ErrorObject{
						Message: "google stream conversion failed",
						Type:    "server_error",
						Code:    "conversion_error",
					},
				}
				streamRecord.Error = traceError("stream_google_injected_type", fmt.Errorf("google adapter type mismatch"))
				streamRecord.OpenAIResponse = payload
				writeOpenAIError(w, http.StatusInternalServerError, payload)
				return
			}
			coreFinal, convErr := toCoreResponseWithOptionalRequest(ctx, googleAdapter, coreReq, googleResp)
			if convErr != nil {
				log.Error("adapter stream: injected google ToCoreResponse failed", "error", convErr)
				payload := openai.ErrorResponse{
					Error: openai.ErrorObject{
						Message: fmt.Sprintf("google stream conversion failed: %v", convErr),
						Type:    "server_error",
						Code:    "conversion_error",
					},
				}
				streamRecord.Error = traceError("stream_google_injected_tocore", convErr)
				streamRecord.OpenAIResponse = payload
				writeOpenAIError(w, http.StatusInternalServerError, payload)
				return
			}
			coreEvents = coreResponseToCoreStream(ctx, coreFinal)
			break
		}
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
		sr, err = providerToCoreStream(ctx, providerStream, coreReq, googleStream)
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
		coreEvents = sr.Events
		if sr.StreamBuffer != nil {
			providerBuf = sr.StreamBuffer
		}

	case config.ProtocolOpenAIResponse:
		responsesReq, ok := upstreamReq.(*openai.ResponsesRequest)
		if !ok {
			log.Error("adapter stream: unexpected openai-response type")
			payload := openai.ErrorResponse{
				Error: openai.ErrorObject{
					Message: "unexpected openai-response upstream type",
					Type:    "server_error",
					Code:    "internal_error",
				},
			}
			streamRecord.Error = traceError("stream_type", fmt.Errorf("unexpected openai-response type"))
			streamRecord.OpenAIResponse = payload
			writeOpenAIError(w, http.StatusInternalServerError, payload)
			return
		}

		responsesClientRaw := s.activeResponsesClient(candidate.ProviderKey)
		if responsesClientRaw == nil {
			log.Error("adapter stream: no responses client", "provider", candidate.ProviderKey)
			payload := openai.ErrorResponse{
				Error: openai.ErrorObject{
					Message: fmt.Sprintf("no responses client for provider %q", candidate.ProviderKey),
					Type:    "server_error",
					Code:    "provider_error",
				},
			}
			streamRecord.Error = traceError("stream_responses_client", fmt.Errorf("no responses client for %q", candidate.ProviderKey))
			streamRecord.OpenAIResponse = payload
			writeOpenAIError(w, http.StatusBadGateway, payload)
			return
		}
		responsesClient, ok := responsesClientRaw.(*openai.Client)
		if !ok {
			log.Error("adapter stream: invalid responses client type", "provider", candidate.ProviderKey)
			payload := openai.ErrorResponse{
				Error: openai.ErrorObject{
					Message: fmt.Sprintf("invalid responses client for provider %q", candidate.ProviderKey),
					Type:    "server_error",
					Code:    "internal_error",
				},
			}
			streamRecord.Error = traceError("stream_responses_client_type", fmt.Errorf("invalid responses client for %q", candidate.ProviderKey))
			streamRecord.OpenAIResponse = payload
			writeOpenAIError(w, http.StatusInternalServerError, payload)
			return
		}

		streamRecord.UpstreamRequest = responsesReq
		responsesStream, sErr := responsesClient.StreamResponse(ctx, responsesReq)
		if sErr != nil {
			log.Error("adapter stream: StreamResponse failed", "error", sErr)
			payload := openai.ErrorResponse{
				Error: openai.ErrorObject{
					Message: fmt.Sprintf("responses stream error: %v", sErr),
					Type:    "server_error",
					Code:    "provider_error",
				},
			}
			streamRecord.Error = traceError("stream_responses", sErr)
			streamRecord.OpenAIResponse = payload
			writeOpenAIError(w, http.StatusBadGateway, payload)
			return
		}

		providerStream, ok = s.adapterRegistry.GetProviderStream(config.ProtocolOpenAIResponse)
		if !ok {
			log.Warn("adapter stream: no openai-response provider stream adapter")
			payload := openai.ErrorResponse{
				Error: openai.ErrorObject{
					Message: "responses stream adapter not available",
					Type:    "server_error",
					Code:    "adapter_fallback",
				},
			}
			streamRecord.Error = traceError("stream_responses_adapter", fmt.Errorf("no openai-response provider stream adapter"))
			streamRecord.OpenAIResponse = payload
			writeOpenAIError(w, http.StatusInternalServerError, payload)
			return
		}
		sr, sErr = providerToCoreStream(ctx, providerStream, coreReq, responsesStream)
		if sErr != nil {
			log.Error("adapter stream: Responses ToCoreStream failed", "error", sErr)
			payload := openai.ErrorResponse{
				Error: openai.ErrorObject{
					Message: fmt.Sprintf("responses stream conversion failed: %v", sErr),
					Type:    "server_error",
					Code:    "conversion_error",
				},
			}
			streamRecord.Error = traceError("stream_responses_tocore", sErr)
			streamRecord.OpenAIResponse = payload
			writeOpenAIError(w, http.StatusInternalServerError, payload)
			return
		}
		coreEvents = sr.Events
		if sr.StreamBuffer != nil {
			providerBuf = sr.StreamBuffer
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

	// Get client stream adapter for the inbound protocol.
	// NOTE: CorePluginHooks.OnStreamEvent/OnStreamComplete/NewStreamState are
	// no-ops in all current adapters — no plugin wires into them via
	// CorePluginHooks(). Post-stream content remembering (below) provides
	// protocol-specific thinking replay via providerBuf.
	clientStream, ok := s.adapterRegistry.GetClientStream(clientProtocol)
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

	// Obtain per-stream buffer before iterating SSE frames.
	if oaiResult, ok := streamChanAny.(*openai.OpenAIStreamResult); ok {
		clientBuf = oaiResult.Buffer
	}

	// Use the protocol-agnostic SSEStream interface for writing.
	sseStream, ok := streamChanAny.(format.SSEStream)
	if !ok {
		log.Error("adapter stream: result does not implement SSEStream", "type", fmt.Sprintf("%T", streamChanAny))
		payload := openai.ErrorResponse{
			Error: openai.ErrorObject{
				Message: "unexpected stream channel type",
				Type:    "server_error",
				Code:    "internal_error",
			},
		}
		streamRecord.Error = traceError("stream_sse_type", fmt.Errorf("result %T does not implement SSEStream", streamChanAny))
		streamRecord.OpenAIResponse = payload
		writeOpenAIError(w, http.StatusInternalServerError, payload)
		return
	}

	// Write SSE events using generic frames.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)

	// Track usage from the final response.completed event.
	var finalUsage openai.Usage
	var finalResp *openai.Response
	for frame := range sseStream.Frames() {
		if frame.Event == "response.completed" {
			if lf, ok := frame.Data.(openai.ResponseLifecycleEvent); ok {
				finalUsage = lf.Response.Usage
				lfResp := lf.Response
				finalResp = &lfResp
			}
		}
		if frame.Event == "message_delta" {
			if ev, ok := frame.Data.(anthropic.StreamEvent); ok && ev.Usage != nil {
				finalUsage.InputTokens = ev.Usage.InputTokens
				finalUsage.OutputTokens = ev.Usage.OutputTokens
			}
		}
		if err := writeSSEFrame(w, frame); err != nil {
			log.Warn("adapter stream: SSE write failed, aborting stream", "error", err)
			break
		}
	}

	// Use CoreIR-level usage as fallback when protocol-specific frames didn't carry usage.
	if streamUsage.InputTokens > 0 || streamUsage.OutputTokens > 0 {
		finalUsage.InputTokens = streamUsage.InputTokens
		finalUsage.OutputTokens = streamUsage.OutputTokens
	}

	// Record usage statistics after stream completes.

	// Remember reasoning content for DeepSeek thinking replay via StreamInterceptor.
	// This must not depend on trace being enabled.
	if s.pluginRegistry != nil && sess != nil {
		remembered := rememberStreamResponseContent(s.pluginRegistry, sess, model, finalResp)
		if !remembered {
			if anthProvider, ok := s.adapterRegistry.GetProvider(config.ProtocolAnthropic); ok {
				if _, ok := anthProvider.(*anthropic.AnthropicProviderAdapter); ok {
					var events []anthropic.StreamEvent
					if providerBuf != nil {
						raw := providerBuf()
						events = make([]anthropic.StreamEvent, len(raw))
						for i, r := range raw {
							events[i], _ = r.(anthropic.StreamEvent)
						}
					}
					if len(events) > 0 {
						states := s.pluginRegistry.NewStreamStates(model)
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
							s.pluginRegistry.OnStreamEvent(model, plugin.StreamEvent{
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
						s.pluginRegistry.OnStreamComplete(model, states, outputText, sess.ExtensionData)
					}
				}
			}
		}
	}

	// Cache reasoning from Chat stream for DeepSeek thinking replay.
	// This must not depend on trace being enabled.
	if sess != nil {
		if chatProvider, ok := s.adapterRegistry.GetProvider(config.ProtocolOpenAIChat); ok {
			if _, ok := chatProvider.(*chat.ChatProviderAdapter); ok {
				var chatEvents []chat.ChatStreamChunk
				if providerBuf != nil {
					raw := providerBuf()
					chatEvents = make([]chat.ChatStreamChunk, 0, len(raw))
					for _, r := range raw {
						if ev, ok := r.(chat.ChatStreamChunk); ok {
							chatEvents = append(chatEvents, ev)
						}
					}
				}
				events := chatEvents
				if len(events) > 0 {
					var streamReasoning string
					seenToolCallIDs := make(map[string]struct{})
					streamToolCallIDs := make([]string, 0, 4)
					for _, ev := range events {
						for _, sc := range ev.Choices {
							if sc.Delta.ReasoningContent != "" {
								streamReasoning += sc.Delta.ReasoningContent
							}
							for _, tc := range sc.Delta.ToolCalls {
								if tc.ID == "" {
									continue
								}
								if _, ok := seenToolCallIDs[tc.ID]; ok {
									continue
								}
								seenToolCallIDs[tc.ID] = struct{}{}
								streamToolCallIDs = append(streamToolCallIDs, tc.ID)
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

		// Google post-stream content remembering.
		if sess != nil {
			if googleProvider, ok := s.adapterRegistry.GetProvider(config.ProtocolGoogleGenAI); ok {
				if _, ok := googleProvider.(*google.GeminiProviderAdapter); ok {
					// Google GenerateContentResponse has no thinking/reasoning field today.
					// This block is a placeholder for future use when/if Google adds
					// reasoning support, maintaining symmetry with Anthropic/Chat blocks.
					_ = providerBuf
				}
			}
		}

	// Capture stream events for trace.
	if s.tracer != nil && s.tracer.Enabled() {
		// Provider stream buffer (anthropic/chat/google raw events)
		if providerBuf != nil {
			raw := providerBuf()
			var anthBuf []anthropic.StreamEvent
			for _, r := range raw {
				if ev, ok := r.(anthropic.StreamEvent); ok {
					anthBuf = append(anthBuf, ev)
				}
			}
			if len(anthBuf) > 0 {
				streamRecord.AnthropicStreamEvents = anthBuf
			}
			var chatBuf []chat.ChatStreamChunk
			for _, r := range raw {
				if ev, ok := r.(chat.ChatStreamChunk); ok {
					chatBuf = append(chatBuf, ev)
				}
			}
			if len(chatBuf) > 0 {
				streamRecord.ChatStreamEvents = chatBuf
			}
			var googleBuf []google.GenerateContentResponse
			for _, r := range raw {
				if ev, ok := r.(google.GenerateContentResponse); ok {
					googleBuf = append(googleBuf, ev)
				}
			}
			if len(googleBuf) > 0 {
				streamRecord.UpstreamStreamEvents = googleBuf
			}
		}
		// Client stream buffer (OpenAI stream events)
		if clientBuf != nil {
			raw := clientBuf()
			var openAIBuf []openai.StreamEvent
			for _, r := range raw {
				if ev, ok := r.(openai.StreamEvent); ok {
					openAIBuf = append(openAIBuf, ev)
				}
			}
			if len(openAIBuf) > 0 {
				streamRecord.OpenAIStreamEvents = openAIBuf
			}
		}
	}
	if s.stats != nil && (finalUsage.InputTokens > 0 || finalUsage.OutputTokens > 0) {
		s.stats.Record(model, candidate.UpstreamModel, stats.Usage{
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
	reqCost := computeCostWithProviderPricing(pm, s.stats, model, candidate.UpstreamModel, candidate.ProviderKey, billingUsage)
	log.Info("流式请求完成",
		"model", model,
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
	} else if clientProtocol == config.ProtocolOpenAIResponse {
		streamRecord.OpenAIResponse = &openai.Response{
			Model:  model,
			Status: "completed",
			Usage:  finalUsage,
		}
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
		reqCost := computeCostWithProviderPricing(pm, s.stats, model, candidate.UpstreamModel, candidate.ProviderKey, billingUsage)
		s.onRequestCompleted(
			model, candidate.UpstreamModel, candidate.ProviderKey,
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
	model string,
	coreReq *format.CoreRequest,
	coreResp *format.CoreResponse,
	candidate provider.ProviderCandidate,
	clientProtocol string,
	requestStart time.Time,
	streamRecord *mbtrace.Record,
) {
	log := slog.Default().With("model", model, "path", "adapter_stream_visual")

	clientStream, ok := s.adapterRegistry.GetClientStream(clientProtocol)
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

	streamChanAny, err := clientStream.FromCoreStream(ctx, coreReq, coreResponseToStreamEvents(ctx, coreResp))
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
	// Use the protocol-agnostic SSEStream interface for writing.
	sseStream, ok := streamChanAny.(format.SSEStream)
	if !ok {
		payload := openai.ErrorResponse{
			Error: openai.ErrorObject{
				Message: "unexpected stream channel type",
				Type:    "server_error",
				Code:    "internal_error",
			},
		}
		streamRecord.Error = traceError("stream_sse_type", fmt.Errorf("result %T does not implement SSEStream", streamChanAny))
		streamRecord.OpenAIResponse = payload
		writeOpenAIError(w, http.StatusInternalServerError, payload)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)

	var finalResp *openai.Response
	for frame := range sseStream.Frames() {
		if frame.Event == "response.completed" {
			if lf, ok := frame.Data.(openai.ResponseLifecycleEvent); ok {
				lfResp := lf.Response
				finalResp = &lfResp
			}
		}
		if err := writeSSEFrame(w, frame); err != nil {
			log.Warn("adapter stream visual fallback: SSE write failed", "error", err)
			break
		}
	}

	if finalResp != nil {
		streamRecord.OpenAIResponse = finalResp
	} else if clientProtocol == config.ProtocolOpenAIResponse {
		streamRecord.OpenAIResponse = &openai.Response{Model: model, Status: "completed"}
	}

	usage := coreResp.Usage
	billingUsage := billingUsageFromAnthropic(usage)
	if s.stats != nil && (usage.InputTokens > 0 || usage.OutputTokens > 0) {
		s.stats.Record(model, candidate.UpstreamModel, statsUsageFromAnthropic(usage, true))
	}
	reqCost := computeCostWithProviderPricing(s.providerMgr, s.stats, model, candidate.UpstreamModel, candidate.ProviderKey, billingUsage)
	log.Info("流式视觉请求完成",
		"actual_model", candidate.UpstreamModel,
		"provider", candidate.ProviderKey,
		"input_total", usage.InputTokens,
		"output_tokens", usage.OutputTokens,
		"duration", time.Since(requestStart),
	)
	if s.pluginRegistry != nil {
		s.onRequestCompleted(
			model, candidate.UpstreamModel, candidate.ProviderKey,
			requestStart, usageFromAnthropic(string(config.ProtocolAnthropic), "core_visual_stream", usage, true),
			reqCost, "success", "",
		)
	}
}

func coreResponseToStreamEvents(ctx context.Context, resp *format.CoreResponse) <-chan format.CoreStreamEvent {
	out := make(chan format.CoreStreamEvent, 16)
	go func() {
		defer close(out)

		send := func(ev format.CoreStreamEvent) bool {
			select {
			case <-ctx.Done():
				return false
			case out <- ev:
				return true
			}
		}

		if resp == nil {
			send(format.CoreStreamEvent{
				Type: format.CoreEventFailed,
				Error: &format.CoreError{
					Message: "core response is nil",
					Type:    "server_error",
				},
			})
			return
		}
		if !send(format.CoreStreamEvent{Type: format.CoreEventCreated, ItemID: resp.ID, Model: resp.Model}) {
			return
		}
		index := 0
		for _, msg := range resp.Messages {
			if msg.Role != "assistant" {
				continue
			}
			for _, block := range msg.Content {
				switch block.Type {
				case "reasoning":
					if !send(format.CoreStreamEvent{Type: format.CoreContentBlockStarted, Index: index, ContentBlock: &format.CoreContentBlock{Type: "reasoning"}}) {
						return
					}
					if block.ReasoningText != "" {
						if !send(format.CoreStreamEvent{Type: format.CoreTextDelta, Index: index, Delta: block.ReasoningText}) {
							return
						}
					}
					if !send(format.CoreStreamEvent{Type: format.CoreContentBlockDone, Index: index, ContentBlock: &format.CoreContentBlock{
						Type:               "reasoning",
						ReasoningSignature: block.ReasoningSignature,
					}}) {
						return
					}
					index++
				case "text":
					if !send(format.CoreStreamEvent{Type: format.CoreContentBlockStarted, Index: index, ContentBlock: &format.CoreContentBlock{Type: "text"}}) {
						return
					}
					if block.Text != "" {
						if !send(format.CoreStreamEvent{Type: format.CoreTextDelta, Index: index, Delta: block.Text}) {
							return
						}
					}
					if !send(format.CoreStreamEvent{Type: format.CoreContentBlockDone, Index: index}) {
						return
					}
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
		send(format.CoreStreamEvent{
			Type:   eventType,
			Status: status,
			Model:  resp.Model,
			Usage:  &resp.Usage,
			Error:  resp.Error,
		})
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
	adapter  format.ProviderAdapter
	client   provider.ProviderClient
	finalize func(ctx context.Context, upstream any) (any, error)
}

func newAdapterCoreProvider(adapter format.ProviderAdapter, client provider.ProviderClient) *adapterCoreProvider {
	return &adapterCoreProvider{adapter: adapter, client: client}
}

func newFinalizingAdapterCoreProvider(
	adapter format.ProviderAdapter,
	client provider.ProviderClient,
	finalize func(ctx context.Context, upstream any) (any, error),
) *adapterCoreProvider {
	return &adapterCoreProvider{adapter: adapter, client: client, finalize: finalize}
}

func (p *adapterCoreProvider) CreateCore(ctx context.Context, req *format.CoreRequest) (*format.CoreResponse, error) {
	upstreamAny, err := p.adapter.FromCoreRequest(ctx, req)
	if err != nil {
		return nil, err
	}
	if p.finalize != nil {
		upstreamAny, err = p.finalize(ctx, upstreamAny)
		if err != nil {
			return nil, err
		}
	}
	rawResp, err := p.client.CreateMessage(ctx, upstreamAny)
	if err != nil {
		return nil, err
	}
	if msgResp, ok := rawResp.(anthropic.MessageResponse); ok {
		rawResp = &msgResp
	}
	return providerToCoreResponse(ctx, p.adapter, req, rawResp)
}

func providerToCoreResponse(ctx context.Context, adapter format.ProviderAdapter, req *format.CoreRequest, resp any) (*format.CoreResponse, error) {
	return toCoreResponseWithOptionalRequest(ctx, adapter, req, resp)
}

func toCoreResponseWithOptionalRequest(ctx context.Context, adapter interface {
	ToCoreResponse(context.Context, any) (*format.CoreResponse, error)
}, req *format.CoreRequest, resp any) (*format.CoreResponse, error) {
	if aware, ok := adapter.(format.ProviderRequestAwareAdapter); ok {
		return aware.ToCoreResponseWithRequest(ctx, req, resp)
	}
	return adapter.ToCoreResponse(ctx, resp)
}

func providerToCoreStream(ctx context.Context, adapter format.ProviderStreamAdapter, req *format.CoreRequest, src any) (*format.StreamResult, error) {
	if aware, ok := adapter.(format.ProviderRequestAwareStreamAdapter); ok {
		return aware.ToCoreStreamWithRequest(ctx, req, src)
	}
	return adapter.ToCoreStream(ctx, src)
}

// coreResponseToCoreStream converts a CoreResponse into a synthetic Core stream.
// This keeps the stream output contract when a plugin path only provides
// non-streaming CreateCore semantics.
func coreResponseToCoreStream(ctx context.Context, resp *format.CoreResponse) <-chan format.CoreStreamEvent {
	out := make(chan format.CoreStreamEvent)
	go func() {
		defer close(out)

		send := func(ev format.CoreStreamEvent) bool {
			select {
			case <-ctx.Done():
				return false
			case out <- ev:
				return true
			}
		}

		if resp == nil {
			send(format.CoreStreamEvent{
				Type:   format.CoreEventFailed,
				Status: "failed",
				Error:  &format.CoreError{Message: "nil core response"},
			})
			return
		}

		if !send(format.CoreStreamEvent{
			Type:   format.CoreEventCreated,
			Status: "in_progress",
			Model:  resp.Model,
			ItemID: resp.ID,
		}) {
			return
		}
		if !send(format.CoreStreamEvent{
			Type:   format.CoreEventInProgress,
			Status: "in_progress",
			Model:  resp.Model,
		}) {
			return
		}

		blockIndex := 0
		for _, msg := range resp.Messages {
			if msg.Role != "assistant" {
				continue
			}
			for _, block := range msg.Content {
				switch block.Type {
				case "reasoning":
					if !send(format.CoreStreamEvent{
						Type:  format.CoreContentBlockStarted,
						Index: blockIndex,
						ContentBlock: &format.CoreContentBlock{
							Type: "reasoning",
						},
					}) {
						return
					}
					if block.ReasoningText != "" {
						if !send(format.CoreStreamEvent{
							Type:  format.CoreTextDelta,
							Index: blockIndex,
							Delta: block.ReasoningText,
						}) {
							return
						}
					}
					if !send(format.CoreStreamEvent{
						Type:  format.CoreContentBlockDone,
						Index: blockIndex,
						ContentBlock: &format.CoreContentBlock{
							Type:               "reasoning",
							ReasoningSignature: block.ReasoningSignature,
						},
					}) {
						return
					}
				case "tool_use":
					if !send(format.CoreStreamEvent{
						Type:  format.CoreContentBlockStarted,
						Index: blockIndex,
						ContentBlock: &format.CoreContentBlock{
							Type:      "tool_use",
							ToolUseID: block.ToolUseID,
							ToolName:  block.ToolName,
						},
					}) {
						return
					}
					if !send(format.CoreStreamEvent{
						Type:  format.CoreToolCallArgsDone,
						Index: blockIndex,
						Delta: string(block.ToolInput),
					}) {
						return
					}
					if !send(format.CoreStreamEvent{
						Type:  format.CoreContentBlockDone,
						Index: blockIndex,
					}) {
						return
					}
				default:
					text := block.Text
					if block.Type != "text" && text == "" {
						// Unknown non-text block without textual payload.
						blockIndex++
						continue
					}
					if !send(format.CoreStreamEvent{
						Type:  format.CoreContentBlockStarted,
						Index: blockIndex,
						ContentBlock: &format.CoreContentBlock{
							Type: "text",
						},
					}) {
						return
					}
					if text != "" {
						if !send(format.CoreStreamEvent{
							Type:  format.CoreTextDelta,
							Index: blockIndex,
							Delta: text,
						}) {
							return
						}
					}
					if !send(format.CoreStreamEvent{
						Type:  format.CoreContentBlockDone,
						Index: blockIndex,
					}) {
						return
					}
				}
				blockIndex++
			}
		}

		usage := resp.Usage
		usage.TotalTokens = usage.InputTokens + usage.OutputTokens
		status := resp.Status
		if status == "" {
			status = "completed"
		}
		switch status {
		case "failed":
			send(format.CoreStreamEvent{
				Type:   format.CoreEventFailed,
				Status: "failed",
				Model:  resp.Model,
				Error:  resp.Error,
			})
		case "incomplete":
			send(format.CoreStreamEvent{
				Type:       format.CoreEventIncomplete,
				Status:     "incomplete",
				Model:      resp.Model,
				StopReason: resp.StopReason,
				Usage:      &usage,
			})
		default:
			send(format.CoreStreamEvent{
				Type:       format.CoreEventCompleted,
				Status:     "completed",
				Model:      resp.Model,
				StopReason: resp.StopReason,
				Usage:      &usage,
			})
		}
	}()
	return out
}

// wrapWithVisual returns a CoreProvider that wraps the upstream provider with
// visual orchestration, or nil when visual is not applicable for this model.
func (s *Server) wrapWithVisual(
	ctx context.Context,
	modelAlias string,
	preferred provider.ProviderCandidate,
	providerAdapter format.ProviderAdapter,
	finalizeUpstream func(ctx context.Context, upstream any) (any, error),
) visualpkg.CoreProvider {
	pm := s.activeProviderManager()
	if s.pluginRegistry == nil || s.runtime == nil || modelAlias == "" || pm == nil {
		return nil
	}

	cfg := s.runtime.Current().Config
	visCfg, ok := visualpkg.ConfigForModelFromResolvedConfig(cfg, modelAlias)
	if !ok || visCfg.Provider == "" || visCfg.Model == "" {
		return nil
	}

	effectiveClient := preferred.Client
	if effectiveClient == nil {
		slog.Default().Warn("visual: no upstream client resolved")
		return nil
	}

	// Upstream CoreProvider = adapter + client.
	upstreamCP := newFinalizingAdapterCoreProvider(providerAdapter, effectiveClient, finalizeUpstream)

	// Visual provider CoreProvider.
	visProtocol := pm.ProtocolForKey(visCfg.Provider)
	if visProtocol == "" {
		slog.Default().Warn("visual: cannot resolve visual provider protocol")
		return nil
	}
	visAdapter, ok := s.adapterRegistry.GetProvider(visProtocol)
	if !ok {
		slog.Default().Warn("visual: no provider adapter for visual protocol", "protocol", visProtocol)
		return nil
	}
	// Resolve a protocol-appropriate ProviderClient for the visual provider.
	// pm.ClientForKey always returns an anthropic-shaped adapter; for
	// chat-protocol visual providers, wrap the dedicated chat client so the
	// visual call uses the chat protocol end-to-end.
	var visClient provider.ProviderClient
	switch visProtocol {
	case config.ProtocolOpenAIChat:
		chatClient, ok := s.chatClients[visCfg.Provider].(*chat.Client)
		if !ok || chatClient == nil {
			slog.Default().Warn("visual: no chat client for visual provider", "visual_provider", visCfg.Provider, "model", modelAlias)
			return nil
		}
		visClient = &chatProviderClient{c: chatClient}
	case config.ProtocolGoogleGenAI:
		gcRaw := s.activeGoogleClient(visCfg.Provider)
		if gcRaw == nil {
			slog.Default().Warn("visual: no google client for visual provider", "visual_provider", visCfg.Provider, "model", modelAlias)
			return nil
		}
		gc, ok := gcRaw.(*google.Client)
		if !ok || gc == nil {
			slog.Default().Warn("visual: google client type mismatch", "visual_provider", visCfg.Provider)
			return nil
		}
		visModel := pm.FirstUpstreamModelForKey(visCfg.Provider)
		if visModel == "" {
			visModel = visCfg.Model
		}
		visClient = &googleProviderClient{c: gc, model: visModel}
	default:
		c, err := pm.ClientForKey(visCfg.Provider)
		if err != nil || c == nil {
			slog.Default().Warn("visual: provider not found", "visual_provider", visCfg.Provider, "model", modelAlias)
			return nil
		}
		visClient = c
	}
	visCP := newAdapterCoreProvider(visAdapter, visClient)

	return visualpkg.NewCoreBridge(upstreamCP, visCP, visCfg.Model, visCfg.MaxRounds, visCfg.MaxTokens)
}

// chatProviderClient adapts *chat.Client to provider.ProviderClient so the
// adapter-based CoreProvider machinery (used by the visual orchestrator) can
// drive a chat-protocol upstream uniformly across protocols.
//
// pm.ClientForKey only constructs anthropic-shaped clients; chat-protocol
// providers keep their dedicated *chat.Client in s.chatClients. This adapter
// bridges the two when visual orchestration needs to call into a chat upstream.
// googleProviderClient adapts *google.Client to provider.ProviderClient so the
// adapter-based CoreProvider machinery can drive a google-genai protocol
// upstream uniformly across protocols. Google's GenerateContent requires
// a model parameter in the call signature (unlike anthropic/chat), so we
// capture the model name at construction time.
type googleProviderClient struct {
	c     *google.Client
	model string
}

func (p *googleProviderClient) CreateMessage(ctx context.Context, req any) (any, error) {
	googleReq, ok := req.(*google.GenerateContentRequest)
	if !ok {
		return nil, fmt.Errorf("googleProviderClient: expected *google.GenerateContentRequest, got %T", req)
	}
	return p.c.GenerateContent(ctx, p.model, googleReq)
}

func (p *googleProviderClient) StreamMessage(ctx context.Context, req any) (<-chan any, error) {
	// Not used by visual orchestrator (uses CreateCore non-streaming path).
	return nil, fmt.Errorf("googleProviderClient: streaming not supported via ProviderClient interface")
}

type chatProviderClient struct{ c *chat.Client }

func (p *chatProviderClient) CreateMessage(ctx context.Context, req any) (any, error) {
	chatReq, ok := req.(*chat.ChatRequest)
	if !ok {
		return nil, fmt.Errorf("chatProviderClient: expected *chat.ChatRequest, got %T", req)
	}
	return p.c.CreateChat(ctx, chatReq)
}

func (p *chatProviderClient) StreamMessage(ctx context.Context, req any) (<-chan any, error) {
	chatReq, ok := req.(*chat.ChatRequest)
	if !ok {
		return nil, fmt.Errorf("chatProviderClient: expected *chat.ChatRequest, got %T", req)
	}
	stream, err := p.c.StreamChat(ctx, chatReq)
	if err != nil {
		return nil, err
	}
	out := make(chan any)
	go func() {
		defer close(out)
		for chunk := range stream {
			select {
			case <-ctx.Done():
				return
			default:
			}
			select {
			case out <- chunk:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

func normalizeAnthropicRequest(upstream any) (anthropic.MessageRequest, error) {
	switch v := upstream.(type) {
	case anthropic.MessageRequest:
		return v, nil
	case *anthropic.MessageRequest:
		if v == nil {
			return anthropic.MessageRequest{}, fmt.Errorf("expected anthropic.MessageRequest, got nil *anthropic.MessageRequest")
		}
		return *v, nil
	default:
		return anthropic.MessageRequest{}, fmt.Errorf("expected anthropic.MessageRequest, got %T", upstream)
	}
}

// injectCoreWebSearch replaces web_search tools in coreReq.Tools with injected
// tavily_search/firecrawl_fetch tools when the resolved web search mode is "injected".
// Returns true if injection was applied.
func (s *Server) injectCoreWebSearch(ctx context.Context, coreReq *format.CoreRequest, preferred provider.ProviderCandidate, model string, wsMode string) bool {
	_ = ctx
	// Strip Anthropic server-side web_search tools whenever the upstream
	// doesn't support them natively (disabled mode) or when using injected
	// search (injected mode without API keys configured).
	// Claude CLI sends WebSearch/WebFetch tool definitions that must be
	// removed to prevent the model from generating unsupported tool calls.
	stripWebSearchTools := func() {
		filtered := make([]format.CoreTool, 0, len(coreReq.Tools))
		for _, t := range coreReq.Tools {
			if t.Name == "web_search" || t.Name == "web_search_preview" || t.Name == "WebSearch" || t.Name == "WebFetch" {
				continue
			}
			filtered = append(filtered, t)
		}
		if len(filtered) < len(coreReq.Tools) {
			coreReq.Tools = filtered
		}
	}

	if wsMode == "disabled" || wsMode == "" {
		stripWebSearchTools()
		return false
	}
	if s.runtime == nil {
		return false
	}
	searchCfg := s.resolvedSearchConfig(preferred.ProviderKey, model)
	if searchCfg.tavilyKey == "" && searchCfg.firecrawlKey == "" {
		stripWebSearchTools()
		return false
	}

	// Replace coreReq.Tools: keep non-web_search tools, add injected search tools.
	filtered := make([]format.CoreTool, 0, len(coreReq.Tools)+2)
	for _, t := range coreReq.Tools {
		if t.Name != "web_search" && t.Name != "web_search_preview" {
			filtered = append(filtered, t)
		}
	}
	injected := websearchinjected.CoreTools(searchCfg.firecrawlKey)
	filtered = append(filtered, injected...)
	coreReq.Tools = filtered
	// Set tool_choice to auto so the model has freedom to call tavily_search.
	if coreReq.ToolChoice == nil {
		coreReq.ToolChoice = &format.CoreToolChoice{Mode: "auto"}
	}
	return true
}

func resolvedWebSearchMode(pm *provider.ProviderManager, modelAlias string, preferred provider.ProviderCandidate) string {
	if pm == nil {
		return ""
	}
	if preferred.ProviderKey != "" && preferred.UpstreamModel != "" {
		if mode := pm.ResolvedWebSearchForCandidate(preferred.ProviderKey, preferred.UpstreamModel); mode != "" {
			return mode
		}
	}
	if modelAlias != "" {
		return pm.ResolvedWebSearchForModel(modelAlias)
	}
	return ""
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
			select {
			case <-ctx.Done():
				return
			default:
			}
			ev, err := stream.Next()
			if err != nil {
				if err == io.EOF {
					return
				}
				return
			}
			select {
			case out <- ev:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

func (a *searchProviderAdapter) AnthropicClient() *anthropic.Client { return nil }

type searchConfig struct {
	tavilyKey    string
	firecrawlKey string
	maxRounds    int
}

func (s *Server) resolvedSearchConfig(providerKey, modelAlias string) searchConfig {
	// Keep a conservative fallback to existing global/runtime behavior.
	cfg := searchConfig{
		tavilyKey:    "",
		firecrawlKey: "",
		maxRounds:    s.maxSearchRounds(),
	}
	if s.runtime == nil {
		return cfg
	}
	fullCfg := s.runtime.Current().Config
	cfg.tavilyKey = fullCfg.TavilyAPIKey
	cfg.firecrawlKey = fullCfg.FirecrawlAPIKey

	// Prefer model-level resolved config; then provider-level fallback.
	if modelAlias != "" {
		if key := fullCfg.WebSearchTavilyKeyForModel(modelAlias); key != "" {
			cfg.tavilyKey = key
		}
		if key := fullCfg.WebSearchFirecrawlKeyForModel(modelAlias); key != "" {
			cfg.firecrawlKey = key
		}
		if rounds := fullCfg.WebSearchMaxRoundsForModel(modelAlias); rounds > 0 {
			cfg.maxRounds = rounds
		}
		return cfg
	}

	if providerKey != "" {
		if key := fullCfg.WebSearchTavilyKeyForProvider(providerKey); key != "" {
			cfg.tavilyKey = key
		}
		if key := fullCfg.WebSearchFirecrawlKeyForProvider(providerKey); key != "" {
			cfg.firecrawlKey = key
		}
		if rounds := fullCfg.WebSearchMaxRoundsForProvider(providerKey); rounds > 0 {
			cfg.maxRounds = rounds
		}
	}
	return cfg
}

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
	if upstreamReq == nil || sess == nil || sess.ExtensionData == nil {
		return
	}
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
		// Only tool-call assistant messages require thinking replay fallback.
		hasToolUse := false
		for _, block := range msg.Content {
			if block.Type == "tool_use" {
				hasToolUse = true
				break
			}
		}
		if !hasToolUse {
			continue
		}
		// Check if the message already has a thinking block.
		if hasThinkingBlock(msg.Content) {
			continue
		}
		// Try to prepend cached thinking by tool call ID (for tool_use messages).
		foundCachedThinking := false
		for _, block := range msg.Content {
			if block.Type != "tool_use" || block.ID == "" {
				continue
			}
			if cached, ok := state.CachedForToolCall(block.ID); ok {
				// Prepend thinking block directly to this message, not to the last message.
				msg.Content = append([]anthropic.ContentBlock{normalizeThinkingBlock(cached)}, msg.Content...)
				foundCachedThinking = true
				break
			}
		}
		// Fallback: prepend empty thinking block as response boundary.
		// Prevents model from continuing previous response text.
		if !foundCachedThinking && !hasThinkingBlock(msg.Content) {
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
			msg.EmitEmptyReasoningContent = true
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


// ============================================================================
// Anthropic inbound — /v1/messages
// ============================================================================

// handleAnthropicMessages handles inbound Anthropic-format requests at
// /v1/messages, converting them through the Core adapter dispatch path.
func (s *Server) handleAnthropicMessages(w http.ResponseWriter, r *http.Request) {
	log := slog.Default().With("path", r.URL.Path, "method", r.Method, "remote", r.RemoteAddr)
	log.Debug("anthropic request")

	if r.Method != http.MethodPost {
		writeAnthropicError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Only POST requests are supported")
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", "Failed to read request body")
		return
	}

	// Extract model for routing.
	model := modelFromRawJSON(body)

	resolvedRoute, resolveErr := s.resolveModelOrFallback(model)
	if resolveErr != nil {
		log.Warn("anthropic: unknown model", "model", model)
		writeAnthropicError(w, http.StatusNotFound, "model_not_found", fmt.Sprintf("unknown model: %q", model))
		return
	}

	preferred, ok := resolvedRoute.Preferred()
	if !ok {
		log.Error("anthropic: no provider candidate", "model", model)
		writeAnthropicError(w, http.StatusBadGateway, "provider_error", "No available provider")
		return
	}

	// Only the adapter path is supported for inbound Anthropic.
	if s.adapterRegistry == nil {
		writeAnthropicError(w, http.StatusInternalServerError, "server_error", "Adapter dispatch not configured")
		return
	}
	if _, ok := s.adapterRegistry.GetProvider(preferred.Protocol); !ok {
		log.Error("anthropic: no provider adapter for protocol", "protocol", preferred.Protocol)
		writeAnthropicError(w, http.StatusInternalServerError, "adapter_not_configured", fmt.Sprintf("No adapter for protocol %q", preferred.Protocol))
		return
	}

	// Decode via the Anthropic client adapter.
	client, cok := s.adapterRegistry.GetClient(config.ProtocolAnthropic)
	if !cok {
		log.Error("anthropic: no client adapter for anthropic")
		writeAnthropicError(w, http.StatusInternalServerError, "server_error", "Client adapter not available")
		return
	}
	decodedReq, isStream, dErr := client.DecodeRequest(body, "", r.URL.Path)
	if dErr != nil {
		log.Warn("anthropic: DecodeRequest failed", "error", dErr)
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", "Invalid request body")
		return
	}

	s.handleWithAdapters(w, r, decodedReq, isStream, body, resolvedRoute, config.ProtocolAnthropic, model)
}

// writeAnthropicError writes an Anthropic-format JSON error response.
func writeAnthropicError(w http.ResponseWriter, status int, errType, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{
		"type": "error",
		"error": anthropic.ErrorObject{
			Type:    errType,
			Message: message,
		},
	})
}


// ============================================================================
// OpenAI Chat inbound — /v1/chat/completions
// ============================================================================

// handleChatCompletions handles inbound OpenAI Chat Completions requests at
// /v1/chat/completions, converting them through the Core adapter dispatch path.
func (s *Server) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	log := slog.Default().With("path", r.URL.Path, "method", r.Method, "remote", r.RemoteAddr)
	log.Debug("chat request")

	if r.Method != http.MethodPost {
		writeOpenAIError(w, http.StatusMethodNotAllowed, openai.ErrorResponse{Error: openai.ErrorObject{
			Message: "Only POST requests are supported",
			Type:    "invalid_request_error",
			Code:    "method_not_allowed",
		}})
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, openai.ErrorResponse{Error: openai.ErrorObject{
			Message: "Failed to read request body",
			Type:    "invalid_request_error",
			Code:    "invalid_request_body",
		}})
		return
	}

	// Extract model for routing.
	model := modelFromRawJSON(body)

	resolvedRoute, resolveErr := s.resolveModelOrFallback(model)
	if resolveErr != nil {
		log.Warn("chat: unknown model", "model", model)
		writeOpenAIError(w, http.StatusNotFound, openai.ErrorResponse{Error: openai.ErrorObject{
			Message: fmt.Sprintf("unknown model: %q", model),
			Type:    "invalid_request_error",
			Code:    "model_not_found",
		}})
		return
	}

	preferred, ok := resolvedRoute.Preferred()
	if !ok {
		log.Error("chat: no provider candidate", "model", model)
		writeOpenAIError(w, http.StatusBadGateway, openai.ErrorResponse{Error: openai.ErrorObject{
			Message: "No available provider",
			Type:    "server_error",
			Code:    "provider_error",
		}})
		return
	}

	// Only the adapter path is supported for inbound Chat.
	if s.adapterRegistry == nil {
		writeOpenAIError(w, http.StatusInternalServerError, openai.ErrorResponse{Error: openai.ErrorObject{
			Message: "Adapter dispatch not configured",
			Type:    "server_error",
			Code:    "adapter_not_configured",
		}})
		return
	}
	if _, ok := s.adapterRegistry.GetProvider(preferred.Protocol); !ok {
		log.Error("chat: no provider adapter for protocol", "protocol", preferred.Protocol)
		writeOpenAIError(w, http.StatusInternalServerError, openai.ErrorResponse{Error: openai.ErrorObject{
			Message: fmt.Sprintf("No adapter for protocol %q", preferred.Protocol),
			Type:    "server_error",
			Code:    "adapter_not_configured",
		}})
		return
	}

	// Decode via the Chat client adapter.
	client, cok := s.adapterRegistry.GetClient(config.ProtocolOpenAIChat)
	if !cok {
		log.Error("chat: no client adapter for openai-chat")
		writeOpenAIError(w, http.StatusInternalServerError, openai.ErrorResponse{Error: openai.ErrorObject{
			Message: "Client adapter not available",
			Type:    "server_error",
			Code:    "internal_error",
		}})
		return
	}
	decodedReq, isStream, dErr := client.DecodeRequest(body, "", r.URL.Path)
	if dErr != nil {
		log.Warn("chat: DecodeRequest failed", "error", dErr)
		writeOpenAIError(w, http.StatusBadRequest, openai.ErrorResponse{Error: openai.ErrorObject{
			Message: "Invalid request body",
			Type:    "invalid_request_error",
			Code:    "invalid_json",
		}})
		return
	}

	s.handleWithAdapters(w, r, decodedReq, isStream, body, resolvedRoute, config.ProtocolOpenAIChat, model)
}


// ============================================================================
// Google Gemini inbound — /v1beta/models/{model}:generateContent
// ============================================================================

// handleGeminiGenerate handles inbound Google Gemini API requests at
// /v1beta/models/{model}:generateContent and :streamGenerateContent,
// converting them through the Core adapter dispatch path.
//
// The model name is extracted from the URL path; streaming is signaled by
// the ":streamGenerateContent" suffix (not a body field).
func (s *Server) handleGeminiGenerate(w http.ResponseWriter, r *http.Request) {
	log := slog.Default().With("path", r.URL.Path, "method", r.Method, "remote", r.RemoteAddr)
	log.Debug("gemini request")

	if r.Method != http.MethodPost {
		writeGeminiError(w, http.StatusMethodNotAllowed, "Method Not Allowed", "Only POST requests are supported", "UNSUPPORTED_METHOD")
		return
	}

	// Extract model from URL path: /v1beta/models/{model}:generateContent
	pathModel := extractGeminiModel(r.URL.Path)

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeGeminiError(w, http.StatusBadRequest, "Bad Request", "Failed to read request body", "INVALID_ARGUMENT")
		return
	}

	// Try to extract model from body first (some SDKs send it in the body),
	// falling back to the path-extracted model.
	model := modelFromRawJSON(body)
	if model == "" {
		model = pathModel
	}

	if model == "" {
		log.Warn("gemini: no model specified")
		writeGeminiError(w, http.StatusBadRequest, "Bad Request", "model is required", "INVALID_ARGUMENT")
		return
	}

	resolvedRoute, resolveErr := s.resolveModelOrFallback(model)
	if resolveErr != nil {
		log.Warn("gemini: unknown model", "model", model)
		writeGeminiError(w, http.StatusNotFound, "Not Found", fmt.Sprintf("unknown model: %q", model), "NOT_FOUND")
		return
	}

	preferred, ok := resolvedRoute.Preferred()
	if !ok {
		log.Error("gemini: no provider candidate", "model", model)
		writeGeminiError(w, http.StatusBadGateway, "Bad Gateway", "No available provider", "UNAVAILABLE")
		return
	}

	// Only the adapter path is supported for inbound Gemini.
	if s.adapterRegistry == nil {
		writeGeminiError(w, http.StatusInternalServerError, "Internal Server Error", "Adapter dispatch not configured", "INTERNAL")
		return
	}
	if _, ok := s.adapterRegistry.GetProvider(preferred.Protocol); !ok {
		log.Error("gemini: no provider adapter for protocol", "protocol", preferred.Protocol)
		writeGeminiError(w, http.StatusInternalServerError, "Internal Server Error", fmt.Sprintf("No adapter for protocol %q", preferred.Protocol), "INTERNAL")
		return
	}

	// Decode via the Gemini client adapter, passing pathModel and endpoint.
	client, cok := s.adapterRegistry.GetClient(config.ProtocolGoogleGenAI)
	if !cok {
		log.Error("gemini: no client adapter for google-genai")
		writeGeminiError(w, http.StatusInternalServerError, "Internal Server Error", "Client adapter not available", "INTERNAL")
		return
	}
	decodedReq, isStream, dErr := client.DecodeRequest(body, pathModel, r.URL.Path)
	if dErr != nil {
		log.Warn("gemini: DecodeRequest failed", "error", dErr)
		writeGeminiError(w, http.StatusBadRequest, "Bad Request", "Invalid request body", "INVALID_ARGUMENT")
		return
	}

	s.handleWithAdapters(w, r, decodedReq, isStream, body, resolvedRoute, config.ProtocolGoogleGenAI, model)
}

// extractGeminiModel parses the model name from a Gemini URL path.
//
//	/v1beta/models/gemini-2.0-flash:generateContent → "gemini-2.0-flash"
//	/v1beta/models/gemini-2.0-flash:streamGenerateContent → "gemini-2.0-flash"
func extractGeminiModel(path string) string {
	const prefix = "/v1beta/models/"
	idx := strings.Index(path, prefix)
	if idx < 0 {
		return ""
	}
	rest := path[idx+len(prefix):]
	// Split on ':' to separate model name from action.
	if colonIdx := strings.Index(rest, ":"); colonIdx > 0 {
		return rest[:colonIdx]
	}
	// No colon — the model might be the whole remainder.
	if rest != "" {
		return rest
	}
	return ""
}

// writeGeminiError writes a Gemini-format JSON error response.
//
// Gemini error format:
//
//	{
//	  "error": {
//	    "code": 400,
//	    "message": "...",
//	    "status": "INVALID_ARGUMENT"
//	  }
//	}
func writeGeminiError(w http.ResponseWriter, status int, title, message, statusCode string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"code":    status,
			"message": fmt.Sprintf("%s: %s", title, message),
			"status":  statusCode,
		},
	})
}


// formatContentSliceToAnthropic converts []format.CoreContentBlock to []anthropic.ContentBlock.
func formatContentSliceToAnthropic(blocks []format.CoreContentBlock) []anthropic.ContentBlock {
	result := make([]anthropic.ContentBlock, len(blocks))
	for i, b := range blocks {
		result[i] = formatContentToAnthropic(b)
	}
	return result
}
