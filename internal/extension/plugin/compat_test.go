package plugin_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"moonbridge/internal/extension/plugin"
	"moonbridge/internal/foundation/openai"
	"moonbridge/internal/protocol/anthropic"
)

// --- ThinkingPrepender tests ---

type testThinkingPrepender struct {
	testPlugin
	prependedToolUse   bool
	prependedAssistant bool
}

func (p *testThinkingPrepender) PrependThinkingForToolUse(msgs []anthropic.Message, toolCallID string, pendingSummary []openai.ReasoningItemSummary, sessionState any) []anthropic.Message {
	p.prependedToolUse = true
	return append(msgs, anthropic.Message{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "thinking", Thinking: "cached:" + toolCallID}}})
}

func (p *testThinkingPrepender) PrependThinkingForAssistant(blocks []anthropic.ContentBlock, pendingSummary []openai.ReasoningItemSummary, sessionState any) []anthropic.ContentBlock {
	p.prependedAssistant = true
	return append([]anthropic.ContentBlock{{Type: "thinking", Thinking: "cached_assistant"}}, blocks...)
}

func TestCompatPrependThinkingToMessages(t *testing.T) {
	r := plugin.NewRegistry(nil)
	tp := &testThinkingPrepender{testPlugin: testPlugin{name: "tp", enabled: true}}
	r.Register(tp)

	msgs := []anthropic.Message{{Role: "user", Content: []anthropic.ContentBlock{{Type: "text", Text: "hi"}}}}
	result := r.PrependThinkingToMessages("model", msgs, "call_123", nil, map[string]any{"tp": "state"})
	if !tp.prependedToolUse {
		t.Fatal("PrependThinkingForToolUse not called")
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(result))
	}
}

func TestCompatPrependThinkingToAssistant(t *testing.T) {
	r := plugin.NewRegistry(nil)
	tp := &testThinkingPrepender{testPlugin: testPlugin{name: "tp", enabled: true}}
	r.Register(tp)

	blocks := []anthropic.ContentBlock{{Type: "text", Text: "hello"}}
	result := r.PrependThinkingToAssistant("model", blocks, nil, map[string]any{"tp": "state"})
	if !tp.prependedAssistant {
		t.Fatal("PrependThinkingForAssistant not called")
	}
	if len(result) != 2 || result[0].Type != "thinking" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestCompatPrependThinkingDisabled(t *testing.T) {
	r := plugin.NewRegistry(nil)
	tp := &testThinkingPrepender{testPlugin: testPlugin{name: "tp", enabled: false}}
	r.Register(tp)

	msgs := []anthropic.Message{{Role: "user"}}
	result := r.PrependThinkingToMessages("model", msgs, "call_1", nil, nil)
	if tp.prependedToolUse {
		t.Fatal("should not call disabled plugin")
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result))
	}
}

func TestCompatExtractReasoningFromSummary(t *testing.T) {
	r := plugin.NewRegistry(nil)
	r.Register(&testPlugin{name: "p", enabled: true})

	text := r.ExtractReasoningFromSummary("model", []openai.ReasoningItemSummary{{Type: "summary_text", Text: "reasoning"}})
	if text != "reasoning" {
		t.Fatalf("unexpected: %s", text)
	}

	empty := r.ExtractReasoningFromSummary("model", nil)
	if empty != "" {
		t.Fatalf("expected empty, got: %s", empty)
	}
}

func TestCompatOnStreamBlockStartStop(t *testing.T) {
	r := plugin.NewRegistry(nil)
	si := &testStreamInterceptor{testPlugin: testPlugin{name: "si", enabled: true}}
	r.Register(si)

	states := r.NewStreamStates("model")

	// block_start with thinking should be consumed
	consumed := r.OnStreamBlockStart("model", 0, &anthropic.ContentBlock{Type: "thinking"}, states)
	if !consumed {
		t.Fatal("should consume thinking block_start")
	}

	// block_start with text should not be consumed
	consumed2 := r.OnStreamBlockStart("model", 1, &anthropic.ContentBlock{Type: "text"}, states)
	if consumed2 {
		t.Fatal("should not consume text block_start")
	}

	// block_stop
	consumed3, _ := r.OnStreamBlockStop("model", 0, states)
	if consumed3 {
		t.Fatal("mock doesn't consume block_stop")
	}
}

func TestCompatOnStreamBlockDelta(t *testing.T) {
	r := plugin.NewRegistry(nil)
	si := &testStreamInterceptor{testPlugin: testPlugin{name: "si", enabled: true}}
	r.Register(si)

	states := r.NewStreamStates("model")
	consumed := r.OnStreamBlockDelta("model", 0, anthropic.StreamDelta{}, states)
	if consumed {
		t.Fatal("mock doesn't consume deltas")
	}
}

func TestCompatOnStreamToolCall(t *testing.T) {
	r := plugin.NewRegistry(nil)
	si := &testStreamInterceptor{testPlugin: testPlugin{name: "si", enabled: true}}
	r.Register(si)

	states := r.NewStreamStates("model")
	// Should not panic
	r.OnStreamToolCall("model", "call_1", states)
}

func TestCompatResetStreamBlock(t *testing.T) {
	r := plugin.NewRegistry(nil)
	si := &testStreamInterceptor{testPlugin: testPlugin{name: "si", enabled: true}}
	r.Register(si)

	states := r.NewStreamStates("model")
	// Should not panic
	r.ResetStreamBlock("model", 0, states)
}

func TestCompatRememberResponseContent(t *testing.T) {
	r := plugin.NewRegistry(nil)
	cr := &testContentRememberer{testPlugin: testPlugin{name: "cr", enabled: true}}
	r.Register(cr)

	r.RememberResponseContent("model", []anthropic.ContentBlock{{Type: "text", Text: "hi"}}, map[string]any{"cr": "state"})
	if !cr.called {
		t.Fatal("RememberContent not called")
	}
}

type testContentRememberer struct {
	testPlugin
	called bool
}

func (p *testContentRememberer) RememberContent(_ *plugin.RequestContext, _ []anthropic.ContentBlock) {
	p.called = true
}

func TestRegistryHasEnabled(t *testing.T) {
	r := plugin.NewRegistry(nil)
	r.Register(&testPlugin{name: "p1", enabled: false})
	if r.HasEnabled("model") {
		t.Fatal("should not have enabled plugins")
	}
	r.Register(&testPlugin{name: "p2", enabled: true})
	if !r.HasEnabled("model") {
		t.Fatal("should have enabled plugins")
	}
}

func TestRegistryShutdownAll(t *testing.T) {
	r := plugin.NewRegistry(nil)
	r.Register(&testPlugin{name: "p1", enabled: true})
	r.Register(&testPlugin{name: "p2", enabled: true})
	// Should not panic
	r.ShutdownAll()
}

func TestRegistryOnStreamComplete(t *testing.T) {
	r := plugin.NewRegistry(nil)
	si := &testStreamInterceptor{testPlugin: testPlugin{name: "si", enabled: true}}
	r.Register(si)

	states := r.NewStreamStates("model")
	sessionData := map[string]any{"si": "sess"}
	// Should not panic
	r.OnStreamComplete("model", states, "output text", sessionData)
}

func TestCompatOnStreamBlockStopWithThinkingText(t *testing.T) {
	r := plugin.NewRegistry(nil)
	si := &consumingStreamInterceptor{testPlugin: testPlugin{name: "csi", enabled: true}}
	r.Register(si)

	states := r.NewStreamStates("model")
	// Set completed thinking text on the mock state.
	if ms, ok := states["csi"].(*consumingMockState); ok {
		ms.completed = "deep thought"
	}
	consumed, text := r.OnStreamBlockStop("model", 0, states)
	if !consumed {
		t.Fatal("should consume")
	}
	if text != "deep thought" {
		t.Fatalf("unexpected text: %s", text)
	}
}

type consumingStreamInterceptor struct {
	testPlugin
}

func (p *consumingStreamInterceptor) NewStreamState() any {
	return &consumingMockState{}
}

func (p *consumingStreamInterceptor) OnStreamEvent(_ *plugin.StreamContext, event plugin.StreamEvent) (bool, []openai.StreamEvent) {
	if event.Type == "block_stop" {
		return true, nil
	}
	return false, nil
}

func (p *consumingStreamInterceptor) OnStreamComplete(_ *plugin.StreamContext, _ string) {}

type consumingMockState struct {
	completed string
}

func (s *consumingMockState) CompletedThinkingText() string { return s.completed }
func (s *consumingMockState) RecordToolCall(string)         {}
func (s *consumingMockState) Reset(int)                     {}

func TestRegistryWrapProvider(t *testing.T) {
	r := plugin.NewRegistry(nil)
	wp := &testProviderWrapper{testPlugin: testPlugin{name: "wp", enabled: true}}
	r.Register(wp)

	ctx := &plugin.RequestContext{ModelAlias: "test"}
	result := r.WrapProvider(ctx, "original")
	if result != "wrapped" {
		t.Fatalf("unexpected: %v", result)
	}
}

type testProviderWrapper struct {
	testPlugin
}

func (p *testProviderWrapper) WrapProvider(_ *plugin.RequestContext, _ any) any {
	return "wrapped"
}

func TestRegistryRewriteMessages(t *testing.T) {
	r := plugin.NewRegistry(nil)
	mr := &testMessageRewriter{testPlugin: testPlugin{name: "mr", enabled: true}}
	r.Register(mr)

	ctx := &plugin.RequestContext{ModelAlias: "test"}
	msgs := []anthropic.Message{{Role: "user"}}
	result := r.RewriteMessages(ctx, msgs)
	if len(result) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(result))
	}
}

type testMessageRewriter struct {
	testPlugin
}

func (p *testMessageRewriter) RewriteMessages(_ *plugin.RequestContext, msgs []anthropic.Message) []anthropic.Message {
	return append(msgs, anthropic.Message{Role: "assistant"})
}

func TestRegistryPostProcessResponse(t *testing.T) {
	r := plugin.NewRegistry(nil)
	pp := &testResponsePostProcessor{testPlugin: testPlugin{name: "pp", enabled: true}}
	r.Register(pp)

	ctx := &plugin.RequestContext{ModelAlias: "test"}
	resp := &openai.Response{Status: "completed"}
	r.PostProcessResponse(ctx, resp)
	if !pp.called {
		t.Fatal("PostProcessResponse not called")
	}
}

type testResponsePostProcessor struct {
	testPlugin
	called bool
}

func (p *testResponsePostProcessor) PostProcessResponse(_ *plugin.RequestContext, _ *openai.Response) {
	p.called = true
}

func TestRequestContextSessionState(t *testing.T) {
	ctx := &plugin.RequestContext{
		SessionData: map[string]any{"p1": "state1"},
	}
	if ctx.SessionState("p1") != "state1" {
		t.Fatal("unexpected session state")
	}
	if ctx.SessionState("missing") != nil {
		t.Fatal("should return nil for missing plugin")
	}

	ctx2 := &plugin.RequestContext{}
	if ctx2.SessionState("p1") != nil {
		t.Fatal("should return nil for nil session data")
	}
}

type mockAppCfg struct{}

func (m *mockAppCfg) PluginConfig(name string) map[string]any {
	return map[string]any{"key": "value"}
}

func TestRegistryInitAll(t *testing.T) {
	r := plugin.NewRegistry(nil)
	r.Register(&testPlugin{name: "p1", enabled: true})
	err := r.InitAll(&mockAppCfg{})
	if err != nil {
		t.Fatalf("InitAll failed: %v", err)
	}
}

func TestRegistryInitAllNilConfig(t *testing.T) {
	r := plugin.NewRegistry(nil)
	r.Register(&testPlugin{name: "p1", enabled: true})
	err := r.InitAll(nil)
	if err != nil {
		t.Fatalf("InitAll with nil config failed: %v", err)
	}
}

func TestRegistryInitAllError(t *testing.T) {
	r := plugin.NewRegistry(nil)
	r.Register(&failingPlugin{testPlugin: testPlugin{name: "fail", enabled: true}})
	err := r.InitAll(nil)
	if err == nil {
		t.Fatal("expected error")
	}
}

type failingPlugin struct {
	testPlugin
}

func (p *failingPlugin) Init(plugin.PluginContext) error {
	return fmt.Errorf("init failed")
}

func (p *failingPlugin) Shutdown() error {
	return fmt.Errorf("shutdown failed")
}

func TestRegistryShutdownWithError(t *testing.T) {
	r := plugin.NewRegistry(nil)
	r.Register(&failingPlugin{testPlugin: testPlugin{name: "fail", enabled: true}})
	// Should not panic, just log the error.
	r.ShutdownAll()
}

func TestBasePluginDefaults(t *testing.T) {
	var bp plugin.BasePlugin
	if err := bp.Init(plugin.PluginContext{}); err != nil {
		t.Fatal(err)
	}
	if err := bp.Shutdown(); err != nil {
		t.Fatal(err)
	}
	if bp.EnabledForModel("any") {
		t.Fatal("BasePlugin should not be enabled")
	}
}

func TestRegistryNilSafeCompat(t *testing.T) {
	var r *plugin.Registry
	r.PostConvertRequest("m", &anthropic.MessageRequest{}, nil)
	r.RememberResponseContent("m", nil, nil)
	r.PrependThinkingToMessages("m", nil, "", nil, nil)
	r.PrependThinkingToAssistant("m", nil, nil, nil)
	r.ExtractReasoningFromSummary("m", nil)
	r.OnStreamBlockStart("m", 0, nil, nil)
	r.OnStreamBlockDelta("m", 0, anthropic.StreamDelta{}, nil)
	r.OnStreamBlockStop("m", 0, nil)
	r.OnStreamToolCall("m", "", nil)
	r.ResetStreamBlock("m", 0, nil)
	r.OnResponseContent("m", anthropic.ContentBlock{})
}

// multiCapPlugin implements many capabilities to cover Register branches.
type multiCapPlugin struct {
	testPlugin
}

func (p *multiCapPlugin) PreprocessInput(_ *plugin.RequestContext, raw json.RawMessage) json.RawMessage {
	return raw
}
func (p *multiCapPlugin) MutateRequest(_ *plugin.RequestContext, _ *anthropic.MessageRequest) {}
func (p *multiCapPlugin) InjectTools(_ *plugin.RequestContext) []anthropic.Tool               { return nil }
func (p *multiCapPlugin) RewriteMessages(_ *plugin.RequestContext, m []anthropic.Message) []anthropic.Message {
	return m
}
func (p *multiCapPlugin) WrapProvider(_ *plugin.RequestContext, prov any) any { return prov }
func (p *multiCapPlugin) FilterContent(_ *plugin.RequestContext, _ anthropic.ContentBlock) (bool, []openai.OutputItem) {
	return false, nil
}
func (p *multiCapPlugin) PostProcessResponse(_ *plugin.RequestContext, _ *openai.Response)     {}
func (p *multiCapPlugin) RememberContent(_ *plugin.RequestContext, _ []anthropic.ContentBlock) {}
func (p *multiCapPlugin) NewStreamState() any                                                  { return nil }
func (p *multiCapPlugin) OnStreamEvent(_ *plugin.StreamContext, _ plugin.StreamEvent) (bool, []openai.StreamEvent) {
	return false, nil
}
func (p *multiCapPlugin) OnStreamComplete(_ *plugin.StreamContext, _ string)         {}
func (p *multiCapPlugin) TransformError(_ *plugin.RequestContext, msg string) string { return msg }
func (p *multiCapPlugin) NewSessionState() any                                       { return nil }
func (p *multiCapPlugin) PrependThinkingForToolUse(m []anthropic.Message, _ string, _ []openai.ReasoningItemSummary, _ any) []anthropic.Message {
	return m
}
func (p *multiCapPlugin) PrependThinkingForAssistant(b []anthropic.ContentBlock, _ []openai.ReasoningItemSummary, _ any) []anthropic.ContentBlock {
	return b
}

func TestRegistryRegisterMultiCap(t *testing.T) {
	r := plugin.NewRegistry(nil)
	r.Register(&multiCapPlugin{testPlugin: testPlugin{name: "multi", enabled: true}})
	// Just verify it doesn't panic and all dispatch paths work.
	ctx := &plugin.RequestContext{ModelAlias: "test"}
	r.PreprocessInput("test", json.RawMessage(`{}`))
	r.MutateRequest(ctx, &anthropic.MessageRequest{})
	r.InjectTools(ctx)
	r.RewriteMessages(ctx, nil)
	r.WrapProvider(ctx, nil)
	r.FilterContent(ctx, anthropic.ContentBlock{})
	r.PostProcessResponse(ctx, &openai.Response{})
	r.RememberContent(ctx, nil)
	r.TransformError("test", "msg")
	r.NewSessionData()
	r.NewStreamStates("test")
}

func TestRegistryOnStreamCompleteWithSessionData(t *testing.T) {
	r := plugin.NewRegistry(nil)
	si := &testStreamInterceptor{testPlugin: testPlugin{name: "si", enabled: true}}
	sp := &testSessionProvider{testPlugin: testPlugin{name: "si", enabled: true}}
	r.Register(si)
	r.Register(sp)

	states := r.NewStreamStates("model")
	sessionData := r.NewSessionData()
	r.OnStreamComplete("model", states, "output", sessionData)
}

func TestRegistryDisabledStreamInterceptor(t *testing.T) {
	r := plugin.NewRegistry(nil)
	si := &testStreamInterceptor{testPlugin: testPlugin{name: "si", enabled: false}}
	r.Register(si)

	states := r.NewStreamStates("model")
	if states != nil {
		t.Fatal("disabled plugin should not create stream states")
	}
	consumed, _ := r.OnStreamEvent("model", plugin.StreamEvent{Type: "block_start"}, nil)
	if consumed {
		t.Fatal("disabled plugin should not consume events")
	}
	r.OnStreamComplete("model", nil, "", nil)
}

// Test the enabled() helper indirectly via multiple plugins.
func TestRegistryMultiplePluginsMixedEnabled(t *testing.T) {
	r := plugin.NewRegistry(nil)
	p1 := &testPreprocessor{testPlugin: testPlugin{name: "p1", enabled: false}}
	p2 := &testPreprocessor{testPlugin: testPlugin{name: "p2", enabled: true}}
	r.Register(p1)
	r.Register(p2)

	raw := json.RawMessage(`test`)
	result := r.PreprocessInput("model", raw)
	if p1.called {
		t.Fatal("disabled plugin should not be called")
	}
	if !p2.called {
		t.Fatal("enabled plugin should be called")
	}
	if string(result) != "test_processed" {
		t.Fatalf("unexpected: %s", result)
	}
}

// Test OnStreamBlockStop with non-consuming interceptor.
func TestCompatOnStreamBlockStopNotConsumed(t *testing.T) {
	r := plugin.NewRegistry(nil)
	si := &testStreamInterceptor{testPlugin: testPlugin{name: "si", enabled: true}}
	r.Register(si)

	states := r.NewStreamStates("model")
	// block_stop is not consumed by testStreamInterceptor
	consumed, text := r.OnStreamBlockStop("model", 0, states)
	if consumed {
		t.Fatal("should not consume")
	}
	if text != "" {
		t.Fatalf("unexpected text: %s", text)
	}
}

// Test compat methods with stream state that doesn't implement optional interfaces.
func TestCompatStreamMethodsWithPlainState(t *testing.T) {
	r := plugin.NewRegistry(nil)
	si := &plainStreamInterceptor{testPlugin: testPlugin{name: "psi", enabled: true}}
	r.Register(si)

	states := r.NewStreamStates("model")
	// These should not panic even though the state doesn't implement RecordToolCall/Reset.
	r.OnStreamToolCall("model", "call_1", states)
	r.ResetStreamBlock("model", 0, states)
}

type plainStreamInterceptor struct {
	testPlugin
}

func (p *plainStreamInterceptor) NewStreamState() any { return "plain" }
func (p *plainStreamInterceptor) OnStreamEvent(_ *plugin.StreamContext, _ plugin.StreamEvent) (bool, []openai.StreamEvent) {
	return false, nil
}
func (p *plainStreamInterceptor) OnStreamComplete(_ *plugin.StreamContext, _ string) {}
