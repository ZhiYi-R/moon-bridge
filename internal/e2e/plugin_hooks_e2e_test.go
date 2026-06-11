//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"

	"moonbridge/internal/extension/plugin"
	"moonbridge/internal/format"
	"moonbridge/internal/protocol/anthropic"
	"moonbridge/internal/protocol/openai"
)

// ============================================================================
// Mock Plugin Types
// ============================================================================

// e2eMockPlugin implements CoreRequestMutator, CoreContentFilter and
// CoreContentRememberer via the plugin registry for auto-mapping tests.
type e2eMockPlugin struct {
	plugin.BasePlugin

	mu sync.Mutex

	// MutateCoreRequest tracking
	mutatorCalled        bool
	mutatorModifiedModel string

	// FilterCoreContent tracking
	filterCalled    bool
	filterBlockType string

	// RememberCoreContent tracking
	rememberCalled    bool
	rememberedContent []format.CoreContentBlock
}

func (p *e2eMockPlugin) Name() string { return "e2e-test-plugin" }

func (p *e2eMockPlugin) EnabledForModel(string) bool { return true }

func (p *e2eMockPlugin) MutateCoreRequest(ctx context.Context, req *format.CoreRequest) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.mutatorCalled = true
	p.mutatorModifiedModel = req.Model
	req.Model = req.Model + "-mutated"
}

func (p *e2eMockPlugin) FilterCoreContent(ctx context.Context, block *format.CoreContentBlock) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.filterCalled = true
	p.filterBlockType = block.Type
	return false // do not skip
}

func (p *e2eMockPlugin) RememberCoreContent(ctx context.Context, content []format.CoreContentBlock) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.rememberCalled = true
	p.rememberedContent = content
}

// e2eMockPlugin2 is a second plugin used for testing multi-plugin chaining.
// It implements only CoreRequestMutator.
type e2eMockPlugin2 struct {
	plugin.BasePlugin

	mu sync.Mutex

	mutatorCalled        bool
	mutatorModifiedModel string
	enabled              bool
}

func (p *e2eMockPlugin2) Name() string { return "e2e-test-plugin-2" }

func (p *e2eMockPlugin2) EnabledForModel(string) bool { return p.enabled }

func (p *e2eMockPlugin2) MutateCoreRequest(ctx context.Context, req *format.CoreRequest) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.mutatorCalled = true
	p.mutatorModifiedModel = req.Model
}

// e2eStreamMockPlugin implements plugin.StreamInterceptor for lifecycle tests.
type e2eStreamMockPlugin struct {
	plugin.BasePlugin

	mu sync.Mutex

	initCalled     bool
	eventCalled    bool
	completeCalled bool
	events         []plugin.StreamEvent
	state          any
}

type streamState struct {
	counter int
}

func (p *e2eStreamMockPlugin) Name() string { return "e2e-stream-plugin" }

func (p *e2eStreamMockPlugin) EnabledForModel(string) bool { return true }

func (p *e2eStreamMockPlugin) NewStreamState() any {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.initCalled = true
	p.state = &streamState{counter: 0}
	return p.state
}

func (p *e2eStreamMockPlugin) OnStreamEvent(ctx *plugin.StreamContext, event plugin.StreamEvent) (bool, []openai.StreamEvent) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.eventCalled = true
	p.events = append(p.events, event)
	return false, nil
}

func (p *e2eStreamMockPlugin) OnStreamComplete(ctx *plugin.StreamContext, outputText string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.completeCalled = true
}

// ============================================================================
// Test: Registry auto-mapping for MutateCoreRequest
// ============================================================================

func TestPluginHooks_MutateCoreRequest(t *testing.T) {
	if os.Getenv("TEST_ANTHROPIC_API_KEY") != "" {
		t.Skip("Real mode: skip mock test when TEST_ANTHROPIC_API_KEY is set")
	}

	ctx := context.Background()
	cfg := e2eMinimalConfig()

	mockPlugin := &e2eMockPlugin{}
	pReg := plugin.NewRegistry(nil)
	pReg.Register(mockPlugin)
	hooks := pReg.CorePluginHooks()

	reg := newTestRegistry(t, cfg, hooks)
	client, ok := reg.GetClient(configOpenAIResponse)
	if !ok {
		t.Fatal("client adapter not found")
	}
	provider, ok := reg.GetProvider(configAnthropic)
	if !ok {
		t.Fatal("provider adapter not found")
	}

	mockSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{
			"id": "msg_mut_001",
			"type": "message",
			"role": "assistant",
			"content": [{"type": "text", "text": "MutateCoreRequest test"}],
			"model": "claude-3.5-sonnet-20241022",
			"stop_reason": "end_turn",
			"usage": {"input_tokens": 10, "output_tokens": 5}
		}`)
	}))
	defer mockSrv.Close()

	openAIReq := openai.ResponsesRequest{
		Model:           "claude-3.5-sonnet",
		Input:           json.RawMessage(`"Hello"`),
		MaxOutputTokens: 100,
	}

	coreReq, err := client.ToCoreRequest(ctx, &openAIReq)
	if err != nil {
		t.Fatalf("ToCoreRequest: %v", err)
	}

	upstreamAny, err := provider.FromCoreRequest(ctx, coreReq)
	if err != nil {
		t.Fatalf("FromCoreRequest: %v", err)
	}
	upstreamReq := upstreamAny.(*anthropic.MessageRequest)

	anthClient := anthropic.NewClient(anthropic.ClientConfig{
		BaseURL: mockSrv.URL,
		APIKey:  "test-key",
		Client:  mockSrv.Client(),
	})
	upstreamResp, err := anthClient.CreateMessage(ctx, *upstreamReq)
	if err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}

	coreResp, err := provider.ToCoreResponse(ctx, &upstreamResp)
	if err != nil {
		t.Fatalf("ToCoreResponse: %v", err)
	}

	_, err = client.FromCoreResponse(ctx, coreResp)
	if err != nil {
		t.Fatalf("FromCoreResponse: %v", err)
	}

	mockPlugin.mu.Lock()
	if !mockPlugin.mutatorCalled {
		t.Error("MutateCoreRequest was not called during round-trip")
	}
	mockPlugin.mu.Unlock()
}

// ============================================================================
// Test: Registry auto-mapping for RememberContent
// ============================================================================

func TestPluginHooks_RememberContent(t *testing.T) {
	if os.Getenv("TEST_ANTHROPIC_API_KEY") != "" {
		t.Skip("Real mode: skip mock test when TEST_ANTHROPIC_API_KEY is set")
	}

	ctx := context.Background()
	cfg := e2eMinimalConfig()

	mockPlugin := &e2eMockPlugin{}
	pReg := plugin.NewRegistry(nil)
	pReg.Register(mockPlugin)
	hooks := pReg.CorePluginHooks()

	reg := newTestRegistry(t, cfg, hooks)
	client, ok := reg.GetClient(configOpenAIResponse)
	if !ok {
		t.Fatal("client adapter not found")
	}
	provider, ok := reg.GetProvider(configAnthropic)
	if !ok {
		t.Fatal("provider adapter not found")
	}

	mockSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{
			"id": "msg_rem_001",
			"type": "message",
			"role": "assistant",
			"content": [{"type": "text", "text": "RememberContent test"}],
			"model": "claude-3.5-sonnet-20241022",
			"stop_reason": "end_turn",
			"usage": {"input_tokens": 10, "output_tokens": 5}
		}`)
	}))
	defer mockSrv.Close()

	openAIReq := openai.ResponsesRequest{
		Model:           "claude-3.5-sonnet",
		Input:           json.RawMessage(`"Remember me"`),
		MaxOutputTokens: 100,
	}

	coreReq, err := client.ToCoreRequest(ctx, &openAIReq)
	if err != nil {
		t.Fatalf("ToCoreRequest: %v", err)
	}

	upstreamAny, err := provider.FromCoreRequest(ctx, coreReq)
	if err != nil {
		t.Fatalf("FromCoreRequest: %v", err)
	}
	upstreamReq := upstreamAny.(*anthropic.MessageRequest)

	anthClient := anthropic.NewClient(anthropic.ClientConfig{
		BaseURL: mockSrv.URL,
		APIKey:  "test-key",
		Client:  mockSrv.Client(),
	})
	upstreamResp, err := anthClient.CreateMessage(ctx, *upstreamReq)
	if err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}

	coreResp, err := provider.ToCoreResponse(ctx, &upstreamResp)
	if err != nil {
		t.Fatalf("ToCoreResponse: %v", err)
	}

	_, err = client.FromCoreResponse(ctx, coreResp)
	if err != nil {
		t.Fatalf("FromCoreResponse: %v", err)
	}

	mockPlugin.mu.Lock()
	if !mockPlugin.rememberCalled {
		t.Error("RememberContent was not called during round-trip")
	}
	if len(mockPlugin.rememberedContent) == 0 {
		t.Error("RememberContent was called but content is empty")
	}
	mockPlugin.mu.Unlock()
}

// ============================================================================
// Test: PreprocessInput modifies raw input before parsing
// ============================================================================

func TestPluginHooks_PreprocessInput(t *testing.T) {
	if os.Getenv("TEST_ANTHROPIC_API_KEY") != "" {
		t.Skip("Real mode: skip mock test when TEST_ANTHROPIC_API_KEY is set")
	}

	ctx := context.Background()
	cfg := e2eMinimalConfig()

	hookCalled := false
	hooks := format.CorePluginHooks{}.WithDefaults()
	hooks.PreprocessInput = func(_ context.Context, model string, raw json.RawMessage) json.RawMessage {
		hookCalled = true
		// Append a suffix to the raw input to verify the hook was applied.
		var input string
		if err := json.Unmarshal(raw, &input); err == nil {
			modified, _ := json.Marshal(input + " [PREPROCESSED]")
			return modified
		}
		return raw
	}

	reg := newTestRegistry(t, cfg, hooks)
	client, ok := reg.GetClient(configOpenAIResponse)
	if !ok {
		t.Fatal("client adapter not found")
	}
	provider, ok := reg.GetProvider(configAnthropic)
	if !ok {
		t.Fatal("provider adapter not found")
	}

	mockSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{
			"id": "msg_pp_001",
			"type": "message",
			"role": "assistant",
			"content": [{"type": "text", "text": "Preprocessed response"}],
			"model": "claude-3.5-sonnet-20241022",
			"stop_reason": "end_turn",
			"usage": {"input_tokens": 10, "output_tokens": 5}
		}`)
	}))
	defer mockSrv.Close()

	openAIReq := openai.ResponsesRequest{
		Model:           "claude-3.5-sonnet",
		Input:           json.RawMessage(`"Hello"`),
		MaxOutputTokens: 100,
	}

	coreReq, err := client.ToCoreRequest(ctx, &openAIReq)
	if err != nil {
		t.Fatalf("ToCoreRequest: %v", err)
	}

	if !hookCalled {
		t.Error("PreprocessInput was not called")
	}

	// Verify the hook modified the input by checking the Core message content.
	if len(coreReq.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(coreReq.Messages))
	}
	if len(coreReq.Messages[0].Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(coreReq.Messages[0].Content))
	}
	wantContent := "Hello [PREPROCESSED]"
	if coreReq.Messages[0].Content[0].Text != wantContent {
		t.Errorf("message text = %q, want %q", coreReq.Messages[0].Content[0].Text, wantContent)
	}

	// Complete the round-trip to verify no errors.
	upstreamAny, err := provider.FromCoreRequest(ctx, coreReq)
	if err != nil {
		t.Fatalf("FromCoreRequest: %v", err)
	}
	upstreamReq := upstreamAny.(*anthropic.MessageRequest)

	anthClient := anthropic.NewClient(anthropic.ClientConfig{
		BaseURL: mockSrv.URL,
		APIKey:  "test-key",
		Client:  mockSrv.Client(),
	})
	upstreamResp, err := anthClient.CreateMessage(ctx, *upstreamReq)
	if err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}

	coreResp, err := provider.ToCoreResponse(ctx, &upstreamResp)
	if err != nil {
		t.Fatalf("ToCoreResponse: %v", err)
	}

	_, err = client.FromCoreResponse(ctx, coreResp)
	if err != nil {
		t.Fatalf("FromCoreResponse: %v", err)
	}
}

// ============================================================================
// Test: PostProcessCoreResponse modifies the CoreResponse
// ============================================================================

func TestPluginHooks_PostProcessCoreResponse(t *testing.T) {
	if os.Getenv("TEST_ANTHROPIC_API_KEY") != "" {
		t.Skip("Real mode: skip mock test when TEST_ANTHROPIC_API_KEY is set")
	}

	ctx := context.Background()
	cfg := e2eMinimalConfig()

	hooks := format.CorePluginHooks{}.WithDefaults()
	hooks.PostProcessCoreResponse = func(_ context.Context, resp *format.CoreResponse) {
		if len(resp.Messages) > 0 && len(resp.Messages[0].Content) > 0 {
			for i := range resp.Messages[0].Content {
				if resp.Messages[0].Content[i].Type == "text" {
					resp.Messages[0].Content[i].Text = "[POST-PROCESSED] " + resp.Messages[0].Content[i].Text
				}
			}
		}
	}

	reg := newTestRegistry(t, cfg, hooks)
	client, ok := reg.GetClient(configOpenAIResponse)
	if !ok {
		t.Fatal("client adapter not found")
	}
	provider, ok := reg.GetProvider(configAnthropic)
	if !ok {
		t.Fatal("provider adapter not found")
	}

	mockSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{
			"id": "msg_ppr_001",
			"type": "message",
			"role": "assistant",
			"content": [{"type": "text", "text": "Original response"}],
			"model": "claude-3.5-sonnet-20241022",
			"stop_reason": "end_turn",
			"usage": {"input_tokens": 10, "output_tokens": 5}
		}`)
	}))
	defer mockSrv.Close()

	openAIReq := openai.ResponsesRequest{
		Model:           "claude-3.5-sonnet",
		Input:           json.RawMessage(`"Test post-processing"`),
		MaxOutputTokens: 100,
	}

	coreReq, err := client.ToCoreRequest(ctx, &openAIReq)
	if err != nil {
		t.Fatalf("ToCoreRequest: %v", err)
	}

	upstreamAny, err := provider.FromCoreRequest(ctx, coreReq)
	if err != nil {
		t.Fatalf("FromCoreRequest: %v", err)
	}
	upstreamReq := upstreamAny.(*anthropic.MessageRequest)

	anthClient := anthropic.NewClient(anthropic.ClientConfig{
		BaseURL: mockSrv.URL,
		APIKey:  "test-key",
		Client:  mockSrv.Client(),
	})
	upstreamResp, err := anthClient.CreateMessage(ctx, *upstreamReq)
	if err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}

	coreResp, err := provider.ToCoreResponse(ctx, &upstreamResp)
	if err != nil {
		t.Fatalf("ToCoreResponse: %v", err)
	}

	outAny, err := client.FromCoreResponse(ctx, coreResp)
	if err != nil {
		t.Fatalf("FromCoreResponse: %v", err)
	}
	oaiResp := outAny.(*openai.Response)

	// Verify the PostProcessCoreResponse hook modified the output text.
	wantOutput := "[POST-PROCESSED] Original response"
	if oaiResp.OutputText != wantOutput {
		t.Errorf("OutputText = %q, want %q", oaiResp.OutputText, wantOutput)
	}
}

// ============================================================================
// Test: OnStreamEvent skips/drops events during streaming
// ============================================================================

func TestPluginHooks_OnStreamEvent(t *testing.T) {
	if os.Getenv("TEST_ANTHROPIC_API_KEY") != "" {
		t.Skip("Real mode: skip mock test when TEST_ANTHROPIC_API_KEY is set")
	}

	ctx := context.Background()
	cfg := e2eMinimalConfig()

	deltaCount := 0
	skippedCount := 0
	hooks := format.CorePluginHooks{}.WithDefaults()
	hooks.OnStreamEvent = func(_ context.Context, event format.CoreStreamEvent) bool {
		// Skip only the first text delta to test interception.
		if event.Type == format.CoreTextDelta {
			deltaCount++
			if deltaCount == 1 {
				skippedCount++
				return true
			}
		}
		return false
	}

	reg := newTestRegistry(t, cfg, hooks)
	client, ok := reg.GetClient(configOpenAIResponse)
	if !ok {
		t.Fatal("client adapter not found")
	}
	clientStream, ok := reg.GetClientStream(configOpenAIResponse)
	if !ok {
		t.Fatal("client stream adapter not found")
	}
	provider, ok := reg.GetProvider(configAnthropic)
	if !ok {
		t.Fatal("provider adapter not found")
	}
	providerStream, ok := reg.GetProviderStream(configAnthropic)
	if !ok {
		t.Fatal("provider stream adapter not found")
	}

	mockSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)

		writeSSE(w, "message_start", `{"type":"message_start","message":{"id":"msg_ose_001","type":"message","role":"assistant","content":[],"model":"claude-3.5-sonnet-20241022","usage":{"input_tokens":5,"output_tokens":0}}}`)
		sseFlush(w)

		writeSSE(w, "content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)
		sseFlush(w)

		writeSSE(w, "content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello from "}}`)
		sseFlush(w)

		writeSSE(w, "content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"streaming mock!"}}`)
		sseFlush(w)

		writeSSE(w, "content_block_stop", `{"type":"content_block_stop","index":0}`)
		sseFlush(w)

		writeSSE(w, "message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":5,"output_tokens":3}}`)
		sseFlush(w)

		writeSSE(w, "message_stop", `{"type":"message_stop"}`)
		sseFlush(w)
	}))
	defer mockSrv.Close()

	openAIReq := openai.ResponsesRequest{
		Model:           "claude-3.5-sonnet",
		Input:           json.RawMessage(`"Hello streaming"`),
		MaxOutputTokens: 100,
		Stream:          true,
	}

	coreReq, err := client.ToCoreRequest(ctx, &openAIReq)
	if err != nil {
		t.Fatalf("ToCoreRequest: %v", err)
	}

	upstreamAny, err := provider.FromCoreRequest(ctx, coreReq)
	if err != nil {
		t.Fatalf("FromCoreRequest: %v", err)
	}
	upstreamReq := upstreamAny.(*anthropic.MessageRequest)

	anthClient := anthropic.NewClient(anthropic.ClientConfig{
		BaseURL: mockSrv.URL,
		APIKey:  "test-key",
		Client:  mockSrv.Client(),
	})
	stream, err := anthClient.StreamMessage(ctx, *upstreamReq)
	if err != nil {
		t.Fatalf("StreamMessage: %v", err)
	}
	defer stream.Close()

	coreEvents, err := providerStream.ToCoreStream(ctx, stream)
	if err != nil {
		t.Fatalf("ToCoreStream: %v", err)
	}

	streamOutAny, err := clientStream.FromCoreStream(ctx, coreReq, coreEvents.Events)
	if err != nil {
		t.Fatalf("FromCoreStream: %v", err)
	}
	var openAIStream <-chan openai.StreamEvent
	oaiResult, ok := streamOutAny.(*openai.OpenAIStreamResult)
	if ok {
		openAIStream = oaiResult.Chan()
	} else {
		openAIStream = streamOutAny.(<-chan openai.StreamEvent)
	}

	var seenEvents []string
	for ev := range openAIStream {
		seenEvents = append(seenEvents, ev.Event)
	}

	// Verify output_text.delta events exist (only 1 of 2 was skipped).
	var textDeltaCount int
	for _, ev := range seenEvents {
		if ev == "response.output_text.delta" {
			textDeltaCount++
		}
	}
	if textDeltaCount == 0 {
		t.Error("expected at least 1 text delta event in stream (only first was skipped)")
	}
	if textDeltaCount > 1 {
		t.Errorf("expected exactly 1 text delta (first was skipped), got %d", textDeltaCount)
	}

	// Verify key lifecycle events are present.
	expectedEvents := []string{
		"response.created",
		"response.in_progress",
		"response.completed",
	}
	for _, expected := range expectedEvents {
		var found bool
		for _, seen := range seenEvents {
			if seen == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected lifecycle event %q not found in stream", expected)
		}
	}

	if skippedCount == 0 {
		t.Error("OnStreamEvent skips did not trigger")
	}
	if deltaCount != 2 {
		t.Errorf("expected 2 text delta events total, got %d", deltaCount)
	}
}

func TestPluginHooks_OnStreamComplete(t *testing.T) {
	if os.Getenv("TEST_ANTHROPIC_API_KEY") != "" {
		t.Skip("Real mode: skip mock test when TEST_ANTHROPIC_API_KEY is set")
	}

	ctx := context.Background()
	cfg := e2eMinimalConfig()

	var (
		completeMu     sync.Mutex
		completeCalled bool
	)

	hooks := format.CorePluginHooks{}.WithDefaults()
	hooks.OnStreamComplete = func(_ context.Context, model string, outputText string) {
		completeMu.Lock()
		defer completeMu.Unlock()
		completeCalled = true
	}

	reg := newTestRegistry(t, cfg, hooks)
	client, ok := reg.GetClient(configOpenAIResponse)
	if !ok {
		t.Fatal("client adapter not found")
	}
	clientStream, ok := reg.GetClientStream(configOpenAIResponse)
	if !ok {
		t.Fatal("client stream adapter not found")
	}
	provider, ok := reg.GetProvider(configAnthropic)
	if !ok {
		t.Fatal("provider adapter not found")
	}
	providerStream, ok := reg.GetProviderStream(configAnthropic)
	if !ok {
		t.Fatal("provider stream adapter not found")
	}

	mockSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)

		writeSSE(w, "message_start", `{"type":"message_start","message":{"id":"msg_osc_001","type":"message","role":"assistant","content":[],"model":"claude-3.5-sonnet-20241022","usage":{"input_tokens":5,"output_tokens":0}}}`)
		sseFlush(w)

		writeSSE(w, "content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)
		sseFlush(w)

		writeSSE(w, "content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello from "}}`)
		sseFlush(w)

		writeSSE(w, "content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"streaming mock!"}}`)
		sseFlush(w)

		writeSSE(w, "content_block_stop", `{"type":"content_block_stop","index":0}`)
		sseFlush(w)

		writeSSE(w, "message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":5,"output_tokens":3}}`)
		sseFlush(w)

		writeSSE(w, "message_stop", `{"type":"message_stop"}`)
		sseFlush(w)
	}))
	defer mockSrv.Close()

	openAIReq := openai.ResponsesRequest{
		Model:           "claude-3.5-sonnet",
		Input:           json.RawMessage(`"Hello streaming"`),
		MaxOutputTokens: 100,
		Stream:          true,
	}

	coreReq, err := client.ToCoreRequest(ctx, &openAIReq)
	if err != nil {
		t.Fatalf("ToCoreRequest: %v", err)
	}

	upstreamAny, err := provider.FromCoreRequest(ctx, coreReq)
	if err != nil {
		t.Fatalf("FromCoreRequest: %v", err)
	}
	upstreamReq := upstreamAny.(*anthropic.MessageRequest)

	anthClient := anthropic.NewClient(anthropic.ClientConfig{
		BaseURL: mockSrv.URL,
		APIKey:  "test-key",
		Client:  mockSrv.Client(),
	})
	stream, err := anthClient.StreamMessage(ctx, *upstreamReq)
	if err != nil {
		t.Fatalf("StreamMessage: %v", err)
	}
	defer stream.Close()

	coreEvents, err := providerStream.ToCoreStream(ctx, stream)
	if err != nil {
		t.Fatalf("ToCoreStream: %v", err)
	}

	streamOutAny, err := clientStream.FromCoreStream(ctx, coreReq, coreEvents.Events)
	if err != nil {
		t.Fatalf("FromCoreStream: %v", err)
	}
	var openAIStream <-chan openai.StreamEvent
	oaiResult, ok := streamOutAny.(*openai.OpenAIStreamResult)
	if ok {
		openAIStream = oaiResult.Chan()
	} else {
		openAIStream = streamOutAny.(<-chan openai.StreamEvent)
	}

	// Consume all stream events to trigger completion.
	for ev := range openAIStream {
		_ = ev
	}

	completeMu.Lock()
	if !completeCalled {
		t.Error("OnStreamComplete was not called")
	}
	// Note: OutputText is not populated in the streaming response object
	// until FromCoreResponse is called (non-streaming path), so it's
	// expected to be empty in the streaming-only round-trip.
	completeMu.Unlock()
}

// ============================================================================
// Test: StreamInterceptor lifecycle via plugin.Registry dispatch
// ============================================================================

func TestPluginHooks_StreamInterceptorLifecycle(t *testing.T) {
	mockPlugin := &e2eStreamMockPlugin{}
	pReg := plugin.NewRegistry(nil)
	pReg.Register(mockPlugin)

	// Step 1: Create stream states.
	states := pReg.NewStreamStates("test-model")
	if states == nil {
		t.Fatal("NewStreamStates returned nil")
	}
	state, ok := states["e2e-stream-plugin"]
	if !ok {
		t.Fatal("stream state not found for plugin")
	}
	if _, ok := state.(*streamState); !ok {
		t.Fatalf("stream state has unexpected type %T", state)
	}

	mockPlugin.mu.Lock()
	if !mockPlugin.initCalled {
		t.Error("NewStreamState was not called")
	}
	mockPlugin.mu.Unlock()

	// Step 2: Dispatch an OnStreamEvent.
	event := plugin.StreamEvent{
		Type:  "block_start",
		Index: 0,
	}
	consumed, emit := pReg.OnStreamEvent("test-model", event, states)
	if consumed {
		t.Error("expected consumed=false from mock stream interceptor")
	}
	if len(emit) != 0 {
		t.Errorf("expected 0 emitted events, got %d", len(emit))
	}

	mockPlugin.mu.Lock()
	if !mockPlugin.eventCalled {
		t.Error("OnStreamEvent was not called")
	}
	if len(mockPlugin.events) != 1 {
		t.Fatalf("expected 1 recorded event, got %d", len(mockPlugin.events))
	}
	if mockPlugin.events[0].Type != "block_start" {
		t.Errorf("event type = %q, want %q", mockPlugin.events[0].Type, "block_start")
	}
	mockPlugin.mu.Unlock()

	// Step 3: Complete the stream.
	pReg.OnStreamComplete("test-model", states, "mock output text", nil)

	mockPlugin.mu.Lock()
	if !mockPlugin.completeCalled {
		t.Error("OnStreamComplete was not called")
	}
	mockPlugin.mu.Unlock()
}

// ============================================================================
// Test: Multiple plugins both have hooks called
// ============================================================================

func TestPluginHooks_MultiplePlugins(t *testing.T) {
	if os.Getenv("TEST_ANTHROPIC_API_KEY") != "" {
		t.Skip("Real mode: skip mock test when TEST_ANTHROPIC_API_KEY is set")
	}

	ctx := context.Background()
	cfg := e2eMinimalConfig()

	mock1 := &e2eMockPlugin{}
	mock2 := &e2eMockPlugin2{}
	pReg := plugin.NewRegistry(nil)
	pReg.Register(mock1)
	pReg.Register(mock2)
	hooks := pReg.CorePluginHooks()

	reg := newTestRegistry(t, cfg, hooks)
	client, ok := reg.GetClient(configOpenAIResponse)
	if !ok {
		t.Fatal("client adapter not found")
	}
	provider, ok := reg.GetProvider(configAnthropic)
	if !ok {
		t.Fatal("provider adapter not found")
	}

	mockSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{
			"id": "msg_multi_001",
			"type": "message",
			"role": "assistant",
			"content": [{"type": "text", "text": "Multi-plugin test"}],
			"model": "claude-3.5-sonnet-20241022",
			"stop_reason": "end_turn",
			"usage": {"input_tokens": 10, "output_tokens": 5}
		}`)
	}))
	defer mockSrv.Close()

	openAIReq := openai.ResponsesRequest{
		Model:           "claude-3.5-sonnet",
		Input:           json.RawMessage(`"Test multiple plugins"`),
		MaxOutputTokens: 100,
	}

	coreReq, err := client.ToCoreRequest(ctx, &openAIReq)
	if err != nil {
		t.Fatalf("ToCoreRequest: %v", err)
	}

	upstreamAny, err := provider.FromCoreRequest(ctx, coreReq)
	if err != nil {
		t.Fatalf("FromCoreRequest: %v", err)
	}
	upstreamReq := upstreamAny.(*anthropic.MessageRequest)

	anthClient := anthropic.NewClient(anthropic.ClientConfig{
		BaseURL: mockSrv.URL,
		APIKey:  "test-key",
		Client:  mockSrv.Client(),
	})
	upstreamResp, err := anthClient.CreateMessage(ctx, *upstreamReq)
	if err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}

	coreResp, err := provider.ToCoreResponse(ctx, &upstreamResp)
	if err != nil {
		t.Fatalf("ToCoreResponse: %v", err)
	}

	_, err = client.FromCoreResponse(ctx, coreResp)
	if err != nil {
		t.Fatalf("FromCoreResponse: %v", err)
	}

	mock1.mu.Lock()
	if !mock1.mutatorCalled {
		t.Error("plugin 1 MutateCoreRequest was not called")
	}
	mock1.mu.Unlock()

	mock2.mu.Lock()
	if !mock2.mutatorCalled {
		t.Error("plugin 2 MutateCoreRequest was not called")
	}
	mock2.mu.Unlock()
}

// ============================================================================
// Test: CorePluginHooks auto-mapping for FilterContent and NewStreamState
// ============================================================================

func TestPluginHooks_AdditionalHookAutoMapping(t *testing.T) {
	// Part 1: FilterContent auto-mapping via CoreContentFilter.
	mockPlugin := &e2eMockPlugin{}
	pReg := plugin.NewRegistry(nil)
	pReg.Register(mockPlugin)
	hooks := pReg.CorePluginHooks()

	// FilterContent must be non-nil (auto-mapped from CoreContentFilter).
	if hooks.FilterContent == nil {
		t.Fatal("FilterContent was not set after CorePluginHooks()")
	}

	// Call FilterContent directly and verify it works.
	ctx := format.WithCoreHookModelAlias(context.Background(), "test-model")
	block := &format.CoreContentBlock{Type: "text", Text: "test block"}
	skip := hooks.FilterContent(ctx, block)
	if skip {
		t.Error("expected skip=false from mock plugin")
	}

	mockPlugin.mu.Lock()
	if !mockPlugin.filterCalled {
		t.Error("FilterCoreContent was not called via CorePluginHooks.FilterContent")
	}
	if mockPlugin.filterBlockType != "text" {
		t.Errorf("filterBlockType = %q, want %q", mockPlugin.filterBlockType, "text")
	}
	mockPlugin.mu.Unlock()

	// Part 2: NewStreamState from CorePluginHooks (called directly).
	if hooks.NewStreamState == nil {
		t.Fatal("NewStreamState was not set by WithDefaults()")
	}

	state := hooks.NewStreamState(ctx, "test-model")
	if state != nil {
		t.Errorf("expected nil default NewStreamState, got %v", state)
	}

	// Part 3: Verify the CorePluginHooks was constructed correctly
	// by checking WithDefaults populated all nil fields.
	if hooks.PreprocessInput == nil {
		t.Error("PreprocessInput is nil after WithDefaults()")
	}
	if hooks.RewriteMessages == nil {
		t.Error("RewriteMessages is nil after WithDefaults()")
	}
	if hooks.InjectTools == nil {
		t.Error("InjectTools is nil after WithDefaults()")
	}
	if hooks.TransformError == nil {
		t.Error("TransformError is nil after WithDefaults()")
	}
	if hooks.PrependThinkingToAssistant == nil {
		t.Error("PrependThinkingToAssistant is nil after WithDefaults()")
	}
}

func TestPluginHooks_CoreHooksRespectEnabledForModel(t *testing.T) {
	if os.Getenv("TEST_ANTHROPIC_API_KEY") != "" {
		t.Skip("Real mode: skip mock test when TEST_ANTHROPIC_API_KEY is set")
	}

	ctx := context.Background()
	cfg := e2eMinimalConfig()

	enabledPlugin := &e2eMockPlugin{}
	disabledPlugin := &e2eMockPlugin2{enabled: false}

	pReg := plugin.NewRegistry(nil)
	pReg.Register(enabledPlugin)
	pReg.Register(disabledPlugin)
	hooks := pReg.CorePluginHooks()

	reg := newTestRegistry(t, cfg, hooks)
	client, ok := reg.GetClient(configOpenAIResponse)
	if !ok {
		t.Fatal("client adapter not found")
	}
	provider, ok := reg.GetProvider(configAnthropic)
	if !ok {
		t.Fatal("provider adapter not found")
	}

	mockSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{
			"id": "msg_gate_001",
			"type": "message",
			"role": "assistant",
			"content": [{"type": "text", "text": "EnabledForModel gate test"}],
			"model": "claude-3.5-sonnet-20241022",
			"stop_reason": "end_turn",
			"usage": {"input_tokens": 10, "output_tokens": 5}
		}`)
	}))
	defer mockSrv.Close()

	openAIReq := openai.ResponsesRequest{
		Model:           "claude-3.5-sonnet",
		Input:           json.RawMessage(`"Gate me"`),
		MaxOutputTokens: 100,
	}

	coreReq, err := client.ToCoreRequest(ctx, &openAIReq)
	if err != nil {
		t.Fatalf("ToCoreRequest: %v", err)
	}

	upstreamAny, err := provider.FromCoreRequest(ctx, coreReq)
	if err != nil {
		t.Fatalf("FromCoreRequest: %v", err)
	}
	upstreamReq := upstreamAny.(*anthropic.MessageRequest)

	anthClient := anthropic.NewClient(anthropic.ClientConfig{
		BaseURL: mockSrv.URL,
		APIKey:  "test-key",
		Client:  mockSrv.Client(),
	})
	upstreamResp, err := anthClient.CreateMessage(ctx, *upstreamReq)
	if err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}

	coreResp, err := provider.ToCoreResponse(ctx, &upstreamResp)
	if err != nil {
		t.Fatalf("ToCoreResponse: %v", err)
	}

	_, err = client.FromCoreResponse(ctx, coreResp)
	if err != nil {
		t.Fatalf("FromCoreResponse: %v", err)
	}

	enabledPlugin.mu.Lock()
	if !enabledPlugin.mutatorCalled {
		t.Error("enabled plugin MutateCoreRequest was not called")
	}
	if !enabledPlugin.rememberCalled {
		t.Error("enabled plugin RememberCoreContent was not called")
	}
	enabledPlugin.mu.Unlock()

	disabledPlugin.mu.Lock()
	if disabledPlugin.mutatorCalled {
		t.Error("disabled plugin MutateCoreRequest should not be called")
	}
	disabledPlugin.mu.Unlock()
}
