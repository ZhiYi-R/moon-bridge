package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"moonbridge/internal/config"
	"moonbridge/internal/format"
	"moonbridge/internal/protocol/openai"
	"moonbridge/internal/service/provider"
	"moonbridge/internal/service/runtime"
)

type canceledReader struct{}

func (canceledReader) Read([]byte) (int, error) {
	return 0, context.Canceled
}

type canceledWriter struct{}

func (canceledWriter) Write([]byte) (int, error) {
	return 0, context.Canceled
}

func TestCoreResponseToCoreStreamEmitsUsageOnCompleted(t *testing.T) {
	resp := &format.CoreResponse{
		ID:     "resp_test",
		Status: "completed",
		Model:  "claude-test",
		Messages: []format.CoreMessage{
			{
				Role: "assistant",
				Content: []format.CoreContentBlock{
					{Type: "text", Text: "hello"},
					{Type: "tool_use", ToolUseID: "call_1", ToolName: "exec_command", ToolInput: []byte(`{"cmd":"ls"}`)},
					{Type: "reasoning", ReasoningText: "think", ReasoningSignature: "sig_1"},
				},
			},
		},
		Usage: format.CoreUsage{
			InputTokens:       11,
			OutputTokens:      7,
			CachedInputTokens: 3,
		},
		StopReason: "end_turn",
	}

	stream := coreResponseToCoreStream(context.Background(), resp)
	var events []format.CoreStreamEvent
	for ev := range stream {
		events = append(events, ev)
	}

	if len(events) == 0 {
		t.Fatal("no stream events emitted")
	}
	if events[0].Type != format.CoreEventCreated {
		t.Fatalf("first event type = %s, want %s", events[0].Type, format.CoreEventCreated)
	}

	var completed *format.CoreStreamEvent
	for i := range events {
		if events[i].Type == format.CoreEventCompleted {
			completed = &events[i]
			break
		}
	}
	if completed == nil {
		t.Fatal("missing core.completed event")
	}
	if completed.Usage == nil {
		t.Fatal("completed usage is nil")
	}
	if completed.Usage.InputTokens != 11 || completed.Usage.OutputTokens != 7 || completed.Usage.CachedInputTokens != 3 {
		t.Fatalf("completed usage = %+v", completed.Usage)
	}
	if completed.Usage.TotalTokens != 18 {
		t.Fatalf("completed usage total_tokens = %d, want 18", completed.Usage.TotalTokens)
	}

	var sawToolStarted bool
	var sawToolArgsDone bool
	for _, ev := range events {
		if ev.Type == format.CoreContentBlockStarted && ev.ContentBlock != nil && ev.ContentBlock.Type == "tool_use" {
			sawToolStarted = true
		}
		if ev.Type == format.CoreToolCallArgsDone && ev.Delta == `{"cmd":"ls"}` {
			sawToolArgsDone = true
		}
	}
	if !sawToolStarted {
		t.Fatal("missing tool_use block start event")
	}
	if !sawToolArgsDone {
		t.Fatal("missing tool args done event")
	}
}

func TestCoreResponseToStreamEventsEmitsToolUse(t *testing.T) {
	resp := &format.CoreResponse{
		ID:     "resp_test",
		Status: "completed",
		Model:  "chat-test",
		Messages: []format.CoreMessage{
			{
				Role: "assistant",
				Content: []format.CoreContentBlock{{
					Type:      "tool_use",
					ToolUseID: "call_spawn",
					ToolName:  "multi_agent_v1",
					ToolInput: json.RawMessage(`{"action":"spawn_agent","message":"plan"}`),
				}},
			},
		},
	}

	stream := coreResponseToStreamEvents(context.Background(), resp)
	var events []format.CoreStreamEvent
	for ev := range stream {
		events = append(events, ev)
	}

	var sawToolStarted bool
	var sawToolArgsDone bool
	var sawToolDone bool
	for _, ev := range events {
		if ev.Type == format.CoreContentBlockStarted && ev.ContentBlock != nil &&
			ev.ContentBlock.Type == "tool_use" &&
			ev.ContentBlock.ToolUseID == "call_spawn" &&
			ev.ContentBlock.ToolName == "multi_agent_v1" {
			sawToolStarted = true
		}
		if ev.Type == format.CoreToolCallArgsDone && ev.Delta == `{"action":"spawn_agent","message":"plan"}` {
			sawToolArgsDone = true
		}
		if ev.Type == format.CoreContentBlockDone && ev.Index == 0 {
			sawToolDone = true
		}
	}
	if !sawToolStarted {
		t.Fatal("missing tool_use block start event")
	}
	if !sawToolArgsDone {
		t.Fatal("missing tool args done event")
	}
	if !sawToolDone {
		t.Fatal("missing tool content block done event")
	}
}

func TestStreamOutputItemToCoreBlocksRewrapsNamespaceFunctionCall(t *testing.T) {
	blocks := streamOutputItemToCoreBlocks(openai.OutputItem{
		Type:      "function_call",
		CallID:    "call_spawn",
		Namespace: "multi_agent_v1",
		Name:      "spawn_agent",
		Arguments: `{"message":"plan"}`,
	})

	if len(blocks) != 1 {
		t.Fatalf("blocks len=%d, want 1: %+v", len(blocks), blocks)
	}
	block := blocks[0]
	if block.Type != "tool_use" || block.ToolUseID != "call_spawn" || block.ToolName != "multi_agent_v1" {
		t.Fatalf("tool block = %+v", block)
	}
	var args map[string]any
	if err := json.Unmarshal(block.ToolInput, &args); err != nil {
		t.Fatalf("tool input invalid: %s: %v", string(block.ToolInput), err)
	}
	if args["action"] != "spawn_agent" || args["message"] != "plan" {
		t.Fatalf("tool input = %#v, want action and message", args)
	}
}

func TestClientCanceledCopyErrorRequiresWriteSideOrRequestCancel(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	if _, err := copyResponseBody(canceledWriter{}, strings.NewReader("chunk")); err == nil {
		t.Fatal("copyResponseBody() write-side error = nil")
	} else if !isClientCanceledCopyError(request, err) {
		t.Fatal("write-side context canceled was not classified as client cancellation")
	}

	if _, err := copyResponseBody(&bytes.Buffer{}, canceledReader{}); err == nil {
		t.Fatal("copyResponseBody() read-side error = nil")
	} else if isClientCanceledCopyError(request, err) {
		t.Fatal("read-side context canceled was classified as client cancellation")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	canceledRequest := request.WithContext(ctx)
	if _, err := copyResponseBody(&bytes.Buffer{}, canceledReader{}); err == nil {
		t.Fatal("copyResponseBody() canceled request read-side error = nil")
	} else if !isClientCanceledCopyError(canceledRequest, err) {
		t.Fatal("request cancellation did not classify read-side context canceled as client cancellation")
	}
}

func TestResolvedSearchConfig_UsesModelOverrides(t *testing.T) {
	rt := runtime.NewRuntime(config.Config{
		SearchMaxRounds: 4,
		TavilyAPIKey:    "global-tv",
		FirecrawlAPIKey: "global-fc",
		ProviderDefs: map[string]config.ProviderDef{
			"main": {
				SearchMaxRounds: 6,
				TavilyAPIKey:    "provider-tv",
				FirecrawlAPIKey: "provider-fc",
				Models: map[string]config.ModelMeta{
					"gpt-4o-mini": {
						WebSearch: config.WebSearchConfig{
							Support:         config.WebSearchSupportInjected,
							TavilyAPIKey:    "model-tv",
							FirecrawlAPIKey: "model-fc",
							SearchMaxRounds: 9,
						},
					},
				},
			},
		},
		Routes: map[string]config.RouteEntry{
			"assistant-mini": {Provider: "main", Model: "gpt-4o-mini"},
		},
	}, nil, nil)
	srv := &Server{runtime: rt}

	cfg := srv.resolvedSearchConfig("main", "assistant-mini")
	if cfg.tavilyKey != "model-tv" {
		t.Fatalf("tavily=%q, want model-tv", cfg.tavilyKey)
	}
	if cfg.firecrawlKey != "model-fc" {
		t.Fatalf("firecrawl=%q, want model-fc", cfg.firecrawlKey)
	}
	if cfg.maxRounds != 9 {
		t.Fatalf("maxRounds=%d, want 9", cfg.maxRounds)
	}
}

func TestResolvedSearchConfig_FallsBackToProvider(t *testing.T) {
	rt := runtime.NewRuntime(config.Config{
		SearchMaxRounds: 5,
		TavilyAPIKey:    "global-tv",
		FirecrawlAPIKey: "global-fc",
		ProviderDefs: map[string]config.ProviderDef{
			"main": {
				SearchMaxRounds:  8,
				TavilyAPIKey:     "provider-tv",
				FirecrawlAPIKey:  "provider-fc",
				WebSearchSupport: config.WebSearchSupportInjected,
			},
		},
	}, nil, nil)
	srv := &Server{runtime: rt}

	cfg := srv.resolvedSearchConfig("main", "")
	if cfg.tavilyKey != "provider-tv" {
		t.Fatalf("tavily=%q, want provider-tv", cfg.tavilyKey)
	}
	if cfg.firecrawlKey != "provider-fc" {
		t.Fatalf("firecrawl=%q, want provider-fc", cfg.firecrawlKey)
	}
	if cfg.maxRounds != 8 {
		t.Fatalf("maxRounds=%d, want 8", cfg.maxRounds)
	}
}

func TestResolvedWebSearchModePrefersCandidateOverProvider(t *testing.T) {
	pm, err := provider.NewProviderManager(
		map[string]provider.ProviderConfig{
			"deepseek": {
				BaseURL: "https://deepseek.example.test",
				APIKey:  "key-deepseek",
			},
		},
		map[string]provider.ModelRoute{
			"deepseek-v4-flash": {Provider: "deepseek", Name: "deepseek-v4-flash"},
		},
	)
	if err != nil {
		t.Fatalf("NewProviderManager() error = %v", err)
	}
	pm.SetResolvedWebSearch("deepseek", "disabled")
	pm.SetResolvedWebSearch(provider.WebSearchCandidateKey("deepseek", "deepseek-v4-flash"), "enabled")

	mode := resolvedWebSearchMode(pm, "deepseek-v4-flash", provider.ProviderCandidate{
		ProviderKey:   "deepseek",
		UpstreamModel: "deepseek-v4-flash",
	})
	if mode != "enabled" {
		t.Fatalf("resolvedWebSearchMode() = %q, want enabled", mode)
	}
}

func TestResolvedWebSearchModePrefersExplicitModelAliasOverCandidate(t *testing.T) {
	pm, err := provider.NewProviderManager(
		map[string]provider.ProviderConfig{
			"deepseek": {
				BaseURL: "https://deepseek.example.test",
				APIKey:  "key-deepseek",
			},
		},
		map[string]provider.ModelRoute{
			"alias-off": {Provider: "deepseek", Name: "deepseek-v4-flash"},
		},
	)
	if err != nil {
		t.Fatalf("NewProviderManager() error = %v", err)
	}
	pm.SetResolvedWebSearch("model:alias-off", "disabled")
	pm.SetResolvedWebSearch(provider.WebSearchCandidateKey("deepseek", "deepseek-v4-flash"), "enabled")

	mode := resolvedWebSearchMode(pm, "alias-off", provider.ProviderCandidate{
		ProviderKey:   "deepseek",
		UpstreamModel: "deepseek-v4-flash",
	})
	if mode != "disabled" {
		t.Fatalf("resolvedWebSearchMode() = %q, want disabled", mode)
	}
}

func TestInjectCoreWebSearchAutoInjectedAddsToolsWithoutExplicitRequestTools(t *testing.T) {
	pm, err := provider.NewProviderManager(
		map[string]provider.ProviderConfig{
			"opencode": {
				BaseURL: "https://opencode.example.test",
				APIKey:  "key-opencode",
			},
		},
		map[string]provider.ModelRoute{
			"deepseek-v4-pro": {Provider: "opencode", Name: "deepseek-v4-pro"},
		},
	)
	if err != nil {
		t.Fatalf("NewProviderManager() error = %v", err)
	}
	rt := runtime.NewRuntime(config.Config{
		TavilyAPIKey: "tavily-key",
		ProviderDefs: map[string]config.ProviderDef{
			"opencode": {
				TavilyAPIKey: "tavily-key",
			},
		},
		Routes: map[string]config.RouteEntry{
			"deepseek-v4-pro": {Provider: "opencode", Model: "deepseek-v4-pro"},
		},
	}, pm, nil)
	srv := &Server{providerMgr: pm, runtime: rt}

	coreReq := &format.CoreRequest{Model: "deepseek-v4-pro"}
	openAIReq := openai.ResponsesRequest{
		Model: "deepseek-v4-pro",
		Input: json.RawMessage(`"搜索互联网获取今天的日期"`),
	}
	ok := srv.injectCoreWebSearch(context.Background(), coreReq, provider.ProviderCandidate{
		ProviderKey:   "opencode",
		UpstreamModel: "deepseek-v4-pro",
	}, openAIReq, "injected")
	if !ok {
		t.Fatal("injectCoreWebSearch() = false, want true")
	}
	if len(coreReq.Tools) != 1 {
		t.Fatalf("len(coreReq.Tools) = %d, want 1", len(coreReq.Tools))
	}
	if coreReq.Tools[0].Name != "tavily_search" {
		t.Fatalf("tool[0].Name = %q, want tavily_search", coreReq.Tools[0].Name)
	}
	if coreReq.ToolChoice == nil || coreReq.ToolChoice.Mode != "auto" {
		t.Fatalf("tool_choice = %+v, want auto", coreReq.ToolChoice)
	}
}

func TestInjectCoreWebSearchSkipsWhenCandidateHasNativeSearch(t *testing.T) {
	pm, err := provider.NewProviderManager(
		map[string]provider.ProviderConfig{
			"deepseek": {
				BaseURL: "https://deepseek.example.test",
				APIKey:  "key-deepseek",
			},
		},
		map[string]provider.ModelRoute{
			"deepseek-v4-flash": {Provider: "deepseek", Name: "deepseek-v4-flash"},
		},
	)
	if err != nil {
		t.Fatalf("NewProviderManager() error = %v", err)
	}
	srv := &Server{providerMgr: pm}

	coreReq := &format.CoreRequest{Model: "deepseek-v4-flash"}
	openAIReq := openai.ResponsesRequest{
		Model: "deepseek-v4-flash",
		Input: json.RawMessage(`"搜索互联网获取今天的日期"`),
	}
	ok := srv.injectCoreWebSearch(context.Background(), coreReq, provider.ProviderCandidate{
		ProviderKey:   "deepseek",
		UpstreamModel: "deepseek-v4-flash",
	}, openAIReq, "enabled")
	if ok {
		t.Fatal("injectCoreWebSearch() = true, want false for native search candidate")
	}
	if len(coreReq.Tools) != 0 {
		t.Fatalf("len(coreReq.Tools) = %d, want 0", len(coreReq.Tools))
	}
}
