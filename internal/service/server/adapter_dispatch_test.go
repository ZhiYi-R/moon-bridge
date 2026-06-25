package server

import (
	"context"
	"encoding/json"
	"testing"

	"moonbridge/internal/config"
	"moonbridge/internal/format"
	"moonbridge/internal/protocol/anthropic"
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

func TestResolvedWebSearchMaxUses_ChainsModelProviderGlobal(t *testing.T) {
	rt := runtime.NewRuntime(config.Config{
		WebSearchMaxUses: 8,
		ProviderDefs: map[string]config.ProviderDef{
			"main": {
				WebSearchMaxUses: 12,
				Models: map[string]config.ModelMeta{
					"gpt-4o-mini": {
						WebSearch: config.WebSearchConfig{
							Support: config.WebSearchSupportInjected,
							MaxUses: 20,
						},
					},
				},
			},
			"other": {},
		},
		Routes: map[string]config.RouteEntry{
			"assistant-mini": {Provider: "main", Model: "gpt-4o-mini"},
		},
	}, nil, nil)
	srv := &Server{runtime: rt}

	// Model-level override wins.
	if n := srv.resolvedWebSearchMaxUses("main", "assistant-mini"); n != 20 {
		t.Fatalf("model max_uses=%d, want 20", n)
	}
	// Provider-level fallback when model has no override.
	if n := srv.resolvedWebSearchMaxUses("main", ""); n != 12 {
		t.Fatalf("provider max_uses=%d, want 12", n)
	}
	// Global fallback when provider has none.
	if n := srv.resolvedWebSearchMaxUses("other", ""); n != 8 {
		t.Fatalf("global max_uses=%d, want 8", n)
	}
	// Default when no runtime config available.
	srvNoRuntime := &Server{}
	if n := srvNoRuntime.resolvedWebSearchMaxUses("main", "assistant-mini"); n != 8 {
		t.Fatalf("default max_uses=%d, want 8", n)
	}
}

func TestInjectAnthropicWebSearch_UsesProvidedMaxUses(t *testing.T) {
	req := &anthropic.MessageRequest{}
	injectAnthropicWebSearch(req, 15)
	if len(req.Tools) != 1 {
		t.Fatalf("tools=%d, want 1", len(req.Tools))
	}
	if req.Tools[0].Name != "web_search" || req.Tools[0].Type != "web_search_20250305" {
		t.Fatalf("tool=%+v", req.Tools[0])
	}
	if req.Tools[0].MaxUses != 15 {
		t.Fatalf("max_uses=%d, want 15", req.Tools[0].MaxUses)
	}

	// maxUses <= 0 falls back to the default of 8.
	reqDefault := &anthropic.MessageRequest{}
	injectAnthropicWebSearch(reqDefault, 0)
	if reqDefault.Tools[0].MaxUses != 8 {
		t.Fatalf("default max_uses=%d, want 8", reqDefault.Tools[0].MaxUses)
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

// TestInjectCoreWebSearchInjectedStripsClientWebSearchTools verifies that the
// injected mode removes the Claude CLI's WebSearch/WebFetch tools (which the
// client would otherwise execute itself, bypassing the server-side search loop)
// and replaces the toolset with the injected tavily_search tool.
func TestInjectCoreWebSearchInjectedStripsClientWebSearchTools(t *testing.T) {
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
			"opencode": {TavilyAPIKey: "tavily-key"},
		},
		Routes: map[string]config.RouteEntry{
			"deepseek-v4-pro": {Provider: "opencode", Model: "deepseek-v4-pro"},
		},
	}, pm, nil)
	srv := &Server{providerMgr: pm, runtime: rt}

	coreReq := &format.CoreRequest{
		Model: "deepseek-v4-pro",
		Tools: []format.CoreTool{
			{Name: "Read"},
			{Name: "WebSearch"},
			{Name: "WebFetch"},
			{Name: "web_search"},
		},
	}
	ok := srv.injectCoreWebSearch(context.Background(), coreReq, provider.ProviderCandidate{
		ProviderKey:   "opencode",
		UpstreamModel: "deepseek-v4-pro",
	}, "deepseek-v4-pro", "injected")
	if !ok {
		t.Fatal("injectCoreWebSearch() = false, want true")
	}
	names := make(map[string]bool)
	for _, tool := range coreReq.Tools {
		names[tool.Name] = true
	}
	for _, stripped := range []string{"WebSearch", "WebFetch", "web_search"} {
		if names[stripped] {
			t.Fatalf("tool %q should have been stripped in injected mode, got tools %v", stripped, names)
		}
	}
	if !names["Read"] {
		t.Fatalf("non-search tool Read should be preserved, got tools %v", names)
	}
	if !names["tavily_search"] {
		t.Fatalf("tavily_search should be injected, got tools %v", names)
	}
}
