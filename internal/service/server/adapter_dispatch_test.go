package server

import (
	"context"
	"encoding/json"
	"testing"

	"moonbridge/internal/config"
	"moonbridge/internal/format"
	"moonbridge/internal/protocol/openai"
	"moonbridge/internal/service/provider"
	"moonbridge/internal/service/runtime"
)

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
	}, openAIReq.Model, "injected")
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
	}, openAIReq.Model, "enabled")
	if ok {
		t.Fatal("injectCoreWebSearch() = true, want false for native search candidate")
	}
	if len(coreReq.Tools) != 0 {
		t.Fatalf("len(coreReq.Tools) = %d, want 0", len(coreReq.Tools))
	}
}
