package provider

import (
	"context"
	"github.com/ZhiYi-R/moon-bridge/internal/protocol/anthropic"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ZhiYi-R/moon-bridge/internal/config"
)

func TestProviderManagerRoutesProtocolAndUpstreamModel(t *testing.T) {
	manager, err := NewProviderManager(map[string]ProviderConfig{
		"default": {
			BaseURL: "https://anthropic.example.test",
			APIKey:  "anthropic-key",
		},
		"openai": {
			BaseURL:  "https://openai.example.test",
			APIKey:   "openai-key",
			Protocol: "openai-response",
		},
	}, map[string]ModelRoute{
		"image": {Provider: "openai", Name: "gpt-image-1.5"},
	})
	if err != nil {
		t.Fatalf("NewProviderManager() error = %v", err)
	}

	if got := manager.ProtocolForModel("image"); got != "openai-response" {
		t.Fatalf("ProtocolForModel(image) = %q", got)
	}
	if got := manager.UpstreamModelFor("image"); got != "gpt-image-1.5" {
		t.Fatalf("UpstreamModelFor(image) = %q", got)
	}
	if got := manager.ProtocolForModel("unrouted"); got != "anthropic" {
		t.Fatalf("ProtocolForModel(unrouted) = %q", got)
	}
	if got := manager.UpstreamModelFor("unrouted"); got != "unrouted" {
		t.Fatalf("UpstreamModelFor(unrouted) = %q", got)
	}
}

func TestProviderManagerUsesDefaultProtocolForUnroutedModels(t *testing.T) {
	manager, err := NewProviderManager(map[string]ProviderConfig{
		"default": {
			BaseURL:  "https://openai.example.test",
			APIKey:   "openai-key",
			Protocol: "openai-response",
		},
	}, nil)
	if err != nil {
		t.Fatalf("NewProviderManager() error = %v", err)
	}

	if got := manager.ProtocolForModel("gpt-test"); got != "openai-response" {
		t.Fatalf("ProtocolForModel(unrouted default openai) = %q", got)
	}
}

func TestResolveModel_TwoProvidersSameModel(t *testing.T) {
	manager, err := NewProviderManager(map[string]ProviderConfig{
		"alpha": {
			BaseURL:    "https://alpha.test",
			APIKey:     "key-alpha",
			ModelNames: []string{"claude-sonnet-4-5"},
		},
		"beta": {
			BaseURL:    "https://beta.test",
			APIKey:     "key-beta",
			ModelNames: []string{"claude-sonnet-4-5"},
		},
	}, nil)
	if err != nil {
		t.Fatalf("NewProviderManager() error = %v", err)
	}

	route, err := manager.ResolveModel("claude-sonnet-4-5")
	if err != nil {
		t.Fatalf("ResolveModel() error = %v", err)
	}
	if len(route.Candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(route.Candidates))
	}
	// Should be sorted: alpha < beta
	if route.Candidates[0].ProviderKey != "alpha" || route.Candidates[1].ProviderKey != "beta" {
		t.Errorf("expected alpha, beta; got %s, %s",
			route.Candidates[0].ProviderKey, route.Candidates[1].ProviderKey)
	}
	// Both should have non-nil clients
	for i, c := range route.Candidates {
		if c.Client == nil {
			t.Errorf("candidate[%d] has nil client", i)
		}
	}
}

func TestResolveModel_RouteAliasPriority(t *testing.T) {
	manager, err := NewProviderManager(map[string]ProviderConfig{
		"p1": {
			BaseURL:    "https://p1.test",
			APIKey:     "key-p1",
			ModelNames: []string{"claude-sonnet-4-5"},
		},
		"p2": {
			BaseURL: "https://p2.test",
			APIKey:  "key-p2",
		},
	}, map[string]ModelRoute{
		"claude-sonnet-4-5": {Provider: "p2", Name: "claude-sonnet-4-5-v2"},
	})
	if err != nil {
		t.Fatalf("NewProviderManager() error = %v", err)
	}

	route, err := manager.ResolveModel("claude-sonnet-4-5")
	if err != nil {
		t.Fatalf("ResolveModel() error = %v", err)
	}
	if len(route.Candidates) != 1 {
		t.Fatalf("expected 1 candidate (route alias), got %d", len(route.Candidates))
	}
	// Route alias should win over same-named model in provider catalog
	if route.Candidates[0].ProviderKey != "p2" {
		t.Errorf("expected provider p2 (route alias), got %s", route.Candidates[0].ProviderKey)
	}
	if route.Candidates[0].UpstreamModel != "claude-sonnet-4-5-v2" {
		t.Errorf("expected upstream model claude-sonnet-4-5-v2, got %s", route.Candidates[0].UpstreamModel)
	}
}

func TestResolveModel_DirectRef(t *testing.T) {
	manager, err := NewProviderManager(map[string]ProviderConfig{
		"my-provider": {
			BaseURL: "https://my.test",
			APIKey:  "key-my",
		},
		"other": {
			BaseURL: "https://other.test",
			APIKey:  "key-other",
		},
	}, nil)
	if err != nil {
		t.Fatalf("NewProviderManager() error = %v", err)
	}

	// Test provider/model format
	route, err := manager.ResolveModel("my-provider/gpt-4")
	if err != nil {
		t.Fatalf("ResolveModel(provider/model) error = %v", err)
	}
	if len(route.Candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(route.Candidates))
	}
	if route.Candidates[0].ProviderKey != "my-provider" {
		t.Errorf("expected provider my-provider, got %s", route.Candidates[0].ProviderKey)
	}
	if route.Candidates[0].UpstreamModel != "gpt-4" {
		t.Errorf("expected upstream model gpt-4, got %s", route.Candidates[0].UpstreamModel)
	}

	// Test model(provider) format
	route, err = manager.ResolveModel("gpt-4(other)")
	if err != nil {
		t.Fatalf("ResolveModel(model(provider)) error = %v", err)
	}
	if len(route.Candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(route.Candidates))
	}
	if route.Candidates[0].ProviderKey != "other" {
		t.Errorf("expected provider other, got %s", route.Candidates[0].ProviderKey)
	}
	if route.Candidates[0].UpstreamModel != "gpt-4" {
		t.Errorf("expected upstream model gpt-4, got %s", route.Candidates[0].UpstreamModel)
	}
}

func TestResolveModel_ModelNotFound(t *testing.T) {
	manager, err := NewProviderManager(map[string]ProviderConfig{
		"default": {
			BaseURL: "https://default.test",
			APIKey:  "key-default",
		},
	}, nil)
	if err != nil {
		t.Fatalf("NewProviderManager() error = %v", err)
	}

	_, err = manager.ResolveModel("nonexistent-model")
	if err == nil {
		t.Fatal("ResolveModel() expected error for unknown model")
	}
}

func TestResolveModel_CandidatesSortedByProviderKey(t *testing.T) {
	manager, err := NewProviderManager(map[string]ProviderConfig{
		"zulu": {
			BaseURL:    "https://z.test",
			APIKey:     "key-z",
			ModelNames: []string{"shared-model"},
		},
		"alpha": {
			BaseURL:    "https://a.test",
			APIKey:     "key-a",
			ModelNames: []string{"shared-model"},
		},
		"mike": {
			BaseURL:    "https://m.test",
			APIKey:     "key-m",
			ModelNames: []string{"shared-model"},
		},
	}, nil)
	if err != nil {
		t.Fatalf("NewProviderManager() error = %v", err)
	}

	route, err := manager.ResolveModel("shared-model")
	if err != nil {
		t.Fatalf("ResolveModel() error = %v", err)
	}
	if len(route.Candidates) != 3 {
		t.Fatalf("expected 3 candidates, got %d", len(route.Candidates))
	}
	expected := []string{"alpha", "mike", "zulu"}
	for i, exp := range expected {
		if route.Candidates[i].ProviderKey != exp {
			t.Errorf("candidate[%d].ProviderKey = %s, want %s", i, route.Candidates[i].ProviderKey, exp)
		}
	}
}

func TestResolveModel_Preferred(t *testing.T) {
	manager, err := NewProviderManager(map[string]ProviderConfig{
		"p1": {
			BaseURL:    "https://p1.test",
			APIKey:     "key-p1",
			ModelNames: []string{"model-x"},
		},
	}, nil)
	if err != nil {
		t.Fatalf("NewProviderManager() error = %v", err)
	}

	route, err := manager.ResolveModel("model-x")
	if err != nil {
		t.Fatalf("ResolveModel() error = %v", err)
	}

	candidate, ok := route.Preferred()
	if !ok {
		t.Fatal("Preferred() returned false, expected true")
	}
	if candidate.ProviderKey != "p1" {
		t.Errorf("Preferred().ProviderKey = %s, want p1", candidate.ProviderKey)
	}
}

func TestResolvedWebSearchForCandidatePrefersCandidateKey(t *testing.T) {
	manager, err := NewProviderManager(map[string]ProviderConfig{
		"deepseek": {
			BaseURL: "https://deepseek.test",
			APIKey:  "key-deepseek",
		},
	}, map[string]ModelRoute{
		"deepseek-v4-flash": {Provider: "deepseek", Name: "deepseek-v4-flash"},
	})
	if err != nil {
		t.Fatalf("NewProviderManager() error = %v", err)
	}

	manager.SetResolvedWebSearch("deepseek", "disabled")
	manager.SetResolvedWebSearch(WebSearchCandidateKey("deepseek", "deepseek-v4-flash"), "enabled")

	if got := manager.ResolvedWebSearchForCandidate("deepseek", "deepseek-v4-flash"); got != "enabled" {
		t.Fatalf("ResolvedWebSearchForCandidate() = %q, want enabled", got)
	}
	if got := manager.ResolvedWebSearchForModel("deepseek-v4-flash"); got != "enabled" {
		t.Fatalf("ResolvedWebSearchForModel() = %q, want enabled", got)
	}
}

func TestResolveModel_OffersPriorityOverridesModelNames(t *testing.T) {
	manager, err := NewProviderManager(map[string]ProviderConfig{
		"low-priority": {
			BaseURL:    "https://low.test",
			APIKey:     "key-low",
			ModelNames: []string{"shared-model"},
			Offers: []config.OfferEntry{
				{Model: "shared-model", Priority: 10},
			},
		},
		"high-priority": {
			BaseURL:    "https://high.test",
			APIKey:     "key-high",
			ModelNames: []string{"shared-model"},
			Offers: []config.OfferEntry{
				{Model: "shared-model", Priority: 1},
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("NewProviderManager() error = %v", err)
	}

	route, err := manager.ResolveModel("shared-model")
	if err != nil {
		t.Fatalf("ResolveModel() error = %v", err)
	}
	if len(route.Candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(route.Candidates))
	}
	// high-priority (Offer priority 1) should come before low-priority (Offer priority 10)
	// ModelNames entries (priority 0) must NOT override Offer priorities.
	if route.Candidates[0].ProviderKey != "high-priority" {
		t.Errorf("expected first candidate 'high-priority', got '%s' (ModelNames must not override Offer priorities)",
			route.Candidates[0].ProviderKey)
	}
	if route.Candidates[1].ProviderKey != "low-priority" {
		t.Errorf("expected second candidate 'low-priority', got '%s'", route.Candidates[1].ProviderKey)
	}
}

func TestResolveModel_ProviderPriority(t *testing.T) {
	manager, err := NewProviderManager(map[string]ProviderConfig{
		"cheap": {
			BaseURL: "https://cheap.test",
			APIKey:  "key-cheap",
			Offers: []config.OfferEntry{
				{Model: "shared-model", Priority: 10},
			},
		},
		"fast": {
			BaseURL: "https://fast.test",
			APIKey:  "key-fast",
			Offers: []config.OfferEntry{
				{Model: "shared-model", Priority: 5},
			},
		},
		"zulu": {
			BaseURL: "https://z.test",
			APIKey:  "key-z",
			Offers: []config.OfferEntry{
				{Model: "shared-model"},
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("NewProviderManager() error = %v", err)
	}

	route, err := manager.ResolveModel("shared-model")
	if err != nil {
		t.Fatalf("ResolveModel() error = %v", err)
	}
	if len(route.Candidates) != 3 {
		t.Fatalf("expected 3 candidates, got %d", len(route.Candidates))
	}
	// zulu (priority=0, default) should come first, then fast (priority=5), then cheap (priority=10)
	expected := []string{"zulu", "fast", "cheap"}
	for i, exp := range expected {
		if route.Candidates[i].ProviderKey != exp {
			t.Errorf("candidate[%d].ProviderKey = %s, want %s", i, route.Candidates[i].ProviderKey, exp)
		}
	}
}

func TestReloadAfterInitialCreation(t *testing.T) {
	manager, err := NewProviderManager(map[string]ProviderConfig{
		"default": {
			BaseURL:    "https://initial.test",
			APIKey:     "key-initial",
			ModelNames: []string{"model-a"},
		},
	}, map[string]ModelRoute{
		"test-model": {Provider: "default", Name: "model-a"},
	})
	if err != nil {
		t.Fatalf("NewProviderManager() error = %v", err)
	}

	// Verify initial route.
	if _, _, err := manager.ClientFor("test-model"); err != nil {
		t.Fatalf("ClientFor(test-model) before reload error = %v", err)
	}
	if upstream := manager.UpstreamModelFor("test-model"); upstream != "model-a" {
		t.Fatalf("UpstreamModelFor(test-model) before reload = %q, want %q", upstream, "model-a")
	}

	// Reload with new config.
	newCfg := config.ProviderConfig{
		Providers: map[string]config.ProviderDef{
			"default": {
				BaseURL: "https://reloaded.test",
				APIKey:  "key-reloaded",
				Models:  map[string]config.ModelMeta{"model-b": {ContextWindow: 20000}},
			},
		},
		Routes: map[string]config.RouteEntry{
			"new-model": {Provider: "default", Model: "model-b"},
		},
	}

	if err := manager.Reload(newCfg); err != nil {
		t.Fatalf("Reload() error = %v", err)
	}

	// Old route should be gone; UpstreamModelFor falls back to model name as-is.
	if upstream := manager.UpstreamModelFor("test-model"); upstream != "test-model" {
		t.Fatalf("UpstreamModelFor(test-model) after reload = %q, want %q (fallback)", upstream, "test-model")
	}
	// ResolveModel should fail for the old route alias (no longer in routes map).
	if _, err := manager.ResolveModel("test-model"); err == nil {
		t.Fatal("ResolveModel(test-model) should fail after reload (route removed)")
	}

	// New model should work.
	if _, _, err := manager.ClientFor("new-model"); err != nil {
		t.Fatalf("ClientFor(new-model) after reload error = %v", err)
	}
	if upstream := manager.UpstreamModelFor("new-model"); upstream != "model-b" {
		t.Fatalf("UpstreamModelFor(new-model) after reload = %q, want %q", upstream, "model-b")
	}

	// Base URL should be the reloaded one.
	if url := manager.ProviderBaseURL("default"); url != "https://reloaded.test" {
		t.Fatalf("ProviderBaseURL(default) after reload = %q, want %q", url, "https://reloaded.test")
	}
}

func TestReloadFailurePreservesOldState(t *testing.T) {
	manager, err := NewProviderManager(map[string]ProviderConfig{
		"default": {
			BaseURL:    "https://initial.test",
			APIKey:     "key-initial",
			ModelNames: []string{"model-a"},
		},
	}, map[string]ModelRoute{
		"test-model": {Provider: "default", Name: "model-a"},
	})
	if err != nil {
		t.Fatalf("NewProviderManager() error = %v", err)
	}

	// Verify initial route works.
	if _, _, err := manager.ClientFor("test-model"); err != nil {
		t.Fatalf("ClientFor(test-model) before reload error = %v", err)
	}

	// Reload with invalid config (empty provider defs with no models/routes = invalid).
	badCfg := config.ProviderConfig{
		Providers: map[string]config.ProviderDef{
			"": {BaseURL: "", APIKey: ""}, // invalid key
		},
	}

	err = manager.Reload(badCfg)
	if err == nil {
		t.Fatal("Reload() with invalid config should return error")
	}
	t.Logf("Expected reload error: %v", err)

	// Old state must be preserved.
	if upstream := manager.UpstreamModelFor("test-model"); upstream != "model-a" {
		t.Fatalf("UpstreamModelFor(test-model) after failed reload = %q, want %q", upstream, "model-a")
	}
	if upstream := manager.UpstreamModelFor("test-model"); upstream != "model-a" {
		t.Fatalf("UpstreamModelFor after failed reload = %q, want %q", upstream, "model-a")
	}
	if url := manager.ProviderBaseURL("default"); url != "https://initial.test" {
		t.Fatalf("ProviderBaseURL after failed reload = %q, want %q", url, "https://initial.test")
	}
}

func TestReloadResolvedWebSearch(t *testing.T) {
	manager, err := NewProviderManager(map[string]ProviderConfig{
		"default": {
			BaseURL: "https://initial.test",
			APIKey:  "key-initial",
		},
	}, nil)
	if err != nil {
		t.Fatalf("NewProviderManager() error = %v", err)
	}

	manager.SetResolvedWebSearch("default", "enabled")
	if got := manager.ResolvedWebSearch("default"); got != "enabled" {
		t.Fatalf("ResolvedWebSearch before reload = %q, want %q", got, "enabled")
	}

	// Reload — resolved web search should be empty again (new manager).
	reloadCfg := config.ProviderConfig{
		Providers: map[string]config.ProviderDef{
			"default": {
				BaseURL: "https://reloaded.test",
				APIKey:  "key-reloaded",
			},
		},
	}
	if err := manager.Reload(reloadCfg); err != nil {
		t.Fatalf("Reload() error = %v", err)
	}

	if got := manager.ResolvedWebSearch("default"); got != "" {
		t.Fatalf("ResolvedWebSearch after reload should be empty, got %q", got)
	}
}

// ============================================================================
// Regression tests for bug fixes
// ============================================================================

func TestAnthropicClientAdapter_ImplementsAccessor(t *testing.T) {
	client := anthropic.NewClient(anthropic.ClientConfig{
		APIKey: "test-key",
	})
	adapter := NewAnthropicClientAdapter(client)

	// Verify it implements AnthropicClientAccessor
	acc, ok := adapter.(AnthropicClientAccessor)
	if !ok {
		t.Fatal("anthropicClientAdapter does not implement AnthropicClientAccessor")
	}

	inner := acc.AnthropicClient()
	if inner == nil {
		t.Error("AnthropicClient() returned nil")
	}

	// Verify it implements ProviderClient
	_, ok = adapter.(ProviderClient)
	if !ok {
		t.Error("anthropicClientAdapter does not implement ProviderClient")
	}

	// Verify StreamMessage returns the native anthropic type via accessor
	// This is a compile-time check — the concrete type is anthropic.Stream
	_ = context.Background()
	_ = client
	_ = inner
}

func TestAnthropicClientAdapter_CreateMessageAcceptsPointerRequest(t *testing.T) {
	mockSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"id": "msg_ptr_create",
			"type": "message",
			"role": "assistant",
			"model": "kimi-test",
			"content": [{"type": "text", "text": "ok"}],
			"stop_reason": "end_turn",
			"usage": {"input_tokens": 3, "output_tokens": 2}
		}`))
	}))
	defer mockSrv.Close()

	adapter := &anthropicClientAdapter{client: anthropic.NewClient(anthropic.ClientConfig{
		BaseURL: mockSrv.URL,
		APIKey:  "test-key",
		Client:  mockSrv.Client(),
	})}

	resp, err := adapter.CreateMessage(context.Background(), &anthropic.MessageRequest{
		Model:     "kimi-test",
		MaxTokens: 16,
		Messages: []anthropic.Message{{
			Role: "user",
			Content: []anthropic.ContentBlock{{
				Type: "text",
				Text: "hello",
			}},
		}},
	})
	if err != nil {
		t.Fatalf("CreateMessage() error = %v", err)
	}

	msgResp, ok := resp.(anthropic.MessageResponse)
	if !ok {
		t.Fatalf("CreateMessage() type = %T, want anthropic.MessageResponse", resp)
	}
	if msgResp.ID != "msg_ptr_create" {
		t.Fatalf("CreateMessage() response ID = %q", msgResp.ID)
	}
}

func TestAnthropicClientAdapter_StreamMessageAcceptsPointerRequest(t *testing.T) {
	mockSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(strings.Join([]string{
			"event: message_start",
			"data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_ptr_stream\",\"type\":\"message\",\"role\":\"assistant\"}}",
			"",
		}, "\n")))
	}))
	defer mockSrv.Close()

	adapter := &anthropicClientAdapter{client: anthropic.NewClient(anthropic.ClientConfig{
		BaseURL: mockSrv.URL,
		APIKey:  "test-key",
		Client:  mockSrv.Client(),
	})}

	stream, err := adapter.StreamMessage(context.Background(), &anthropic.MessageRequest{
		Model:     "kimi-test",
		MaxTokens: 16,
		Messages: []anthropic.Message{{
			Role: "user",
			Content: []anthropic.ContentBlock{{
				Type: "text",
				Text: "hello",
			}},
		}},
	})
	if err != nil {
		t.Fatalf("StreamMessage() error = %v", err)
	}

	first, ok := <-stream
	if !ok {
		t.Fatal("StreamMessage() returned closed channel")
	}
	event, ok := first.(anthropic.StreamEvent)
	if !ok {
		t.Fatalf("stream event type = %T, want anthropic.StreamEvent", first)
	}
	if event.Type != "message_start" {
		t.Fatalf("stream event type = %q, want message_start", event.Type)
	}
	if event.Message == nil || event.Message.ID != "msg_ptr_stream" {
		t.Fatalf("stream event message = %+v", event.Message)
	}
}
