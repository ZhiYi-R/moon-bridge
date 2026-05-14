package plugin_test

import (
	"context"
	"encoding/json"
	"moonbridge/internal/format"
	"testing"

	"moonbridge/internal/extension/plugin"
	"moonbridge/internal/protocol/openai"
)

// --- Test helpers ---

func ptrFloat(f float64) *float64 { return &f }

type testPlugin struct {
	plugin.BasePlugin
	name    string
	enabled bool
}

func (p *testPlugin) Name() string                { return p.name }
func (p *testPlugin) EnabledForModel(string) bool { return p.enabled }

type testPreprocessor struct {
	testPlugin
	called bool
}

func (p *testPreprocessor) PreprocessInput(_ *plugin.RequestContext, raw json.RawMessage) json.RawMessage {
	p.called = true
	return append(raw, []byte("_processed")...)
}

type testMutator struct {
	testPlugin
	called bool
}

func (p *testMutator) MutateRequest(_ *plugin.RequestContext, req *format.CoreRequest) {
	p.called = true
	v := 0.5
	req.Temperature = &v
}

type testToolInjector struct {
	testPlugin
}

func (p *testToolInjector) InjectTools(_ *plugin.RequestContext) []format.CoreTool {
	return []format.CoreTool{{Name: "injected_tool"}}
}

type testContentFilter struct {
	testPlugin
}

func (p *testContentFilter) FilterContent(_ *plugin.RequestContext, block format.CoreContentBlock) bool {
	if block.Type == "reasoning" {
		return true
	}
	return false
}

type testErrorTransformer struct {
	testPlugin
}

func (p *testErrorTransformer) TransformError(_ *plugin.RequestContext, msg string) string {
	return "transformed: " + msg
}

type testSessionProvider struct {
	testPlugin
}

func (p *testSessionProvider) NewSessionState() any {
	return "session_state"
}

type testStreamInterceptor struct {
	testPlugin
}

func (p *testStreamInterceptor) NewStreamState() any {
	return &mockStreamState{}
}

func (p *testStreamInterceptor) OnStreamEvent(ctx *plugin.StreamContext, event plugin.StreamEvent) (bool, []openai.StreamEvent) {
	if event.Type == "block_start" && event.Block != nil && event.Block.Type == "reasoning" {
		return true, nil
	}
	return false, nil
}

func (p *testStreamInterceptor) OnStreamComplete(_ *plugin.StreamContext, _ string) {}

type mockStreamState struct {
	completed string
}

func (s *mockStreamState) CompletedThinkingText() string { return s.completed }
func (s *mockStreamState) RecordToolCall(id string)      {}
func (s *mockStreamState) Reset(index int)               {}

// --- Tests ---

func TestRegistryRegisterAndDispatch(t *testing.T) {
	r := plugin.NewRegistry(nil)
	pp := &testPreprocessor{testPlugin: testPlugin{name: "test_pre", enabled: true}}
	r.Register(pp)

	raw := json.RawMessage(`{"hello":"world"}`)
	result := r.PreprocessInput("model", raw)
	if !pp.called {
		t.Fatal("PreprocessInput not called")
	}
	if string(result) != `{"hello":"world"}_processed` {
		t.Fatalf("unexpected result: %s", result)
	}
}

func TestRegistrySkipsDisabledPlugins(t *testing.T) {
	r := plugin.NewRegistry(nil)
	pp := &testPreprocessor{testPlugin: testPlugin{name: "disabled", enabled: false}}
	r.Register(pp)

	raw := json.RawMessage(`test`)
	result := r.PreprocessInput("model", raw)
	if pp.called {
		t.Fatal("should not call disabled plugin")
	}
	if string(result) != "test" {
		t.Fatalf("unexpected result: %s", result)
	}
}

func TestRegistryMutateRequest(t *testing.T) {
	r := plugin.NewRegistry(nil)
	m := &testMutator{testPlugin: testPlugin{name: "mut", enabled: true}}
	r.Register(m)

	req := &format.CoreRequest{Temperature: ptrFloat(1.0)}
	ctx := &plugin.RequestContext{ModelAlias: "test"}
	r.MutateRequest(ctx, req)
	if !m.called {
		t.Fatal("MutateRequest not called")
	}
	if *req.Temperature != 0.5 {
		t.Fatalf("temperature = %v, want 0.5", *req.Temperature)
	}
}

func TestRegistryInjectTools(t *testing.T) {
	r := plugin.NewRegistry(nil)
	r.Register(&testToolInjector{testPlugin: testPlugin{name: "ti", enabled: true}})

	ctx := &plugin.RequestContext{ModelAlias: "test"}
	tools := r.InjectTools(ctx)
	if len(tools) != 1 || tools[0].Name != "injected_tool" {
		t.Fatalf("unexpected tools: %+v", tools)
	}
}

func TestRegistryCorePluginHooksInjectTools(t *testing.T) {
	r := plugin.NewRegistry(nil)
	r.Register(&testToolInjector{testPlugin: testPlugin{name: "ti", enabled: true}})

	hooks := r.CorePluginHooks()
	req := &format.CoreRequest{Model: "test"}
	tools := hooks.InjectTools(format.ContextWithCoreRequest(context.Background(), req))
	if len(tools) != 1 || tools[0].Name != "injected_tool" {
		t.Fatalf("unexpected tools: %+v", tools)
	}
}

func TestRegistryFilterContent(t *testing.T) {
	r := plugin.NewRegistry(nil)
	r.Register(&testContentFilter{testPlugin: testPlugin{name: "cf", enabled: true}})

	ctx := &plugin.RequestContext{ModelAlias: "test"}
	skip := r.FilterContent(ctx, format.CoreContentBlock{Type: "reasoning"})
	if !skip {
		t.Fatal("should skip reasoning block")
	}

	skip2 := r.FilterContent(ctx, format.CoreContentBlock{Type: "text", Text: "hello"})
	if skip2 {
		t.Fatal("should not skip text block")
	}
}

func TestRegistryTransformError(t *testing.T) {
	r := plugin.NewRegistry(nil)
	r.Register(&testErrorTransformer{testPlugin: testPlugin{name: "et", enabled: true}})

	result := r.TransformError("test", "original error")
	if result != "transformed: original error" {
		t.Fatalf("unexpected: %s", result)
	}
}

func TestRegistryNewSessionData(t *testing.T) {
	r := plugin.NewRegistry(nil)
	r.Register(&testSessionProvider{testPlugin: testPlugin{name: "sp", enabled: true}})

	data := r.NewSessionData()
	if data == nil {
		t.Fatal("session data is nil")
	}
	if data["sp"] != "session_state" {
		t.Fatalf("unexpected session state: %v", data["sp"])
	}
}

func TestRegistryNewStreamStates(t *testing.T) {
	r := plugin.NewRegistry(nil)
	r.Register(&testStreamInterceptor{testPlugin: testPlugin{name: "si", enabled: true}})

	states := r.NewStreamStates("model")
	if states == nil {
		t.Fatal("stream states is nil")
	}
	if _, ok := states["si"].(*mockStreamState); !ok {
		t.Fatalf("unexpected stream state type: %T", states["si"])
	}
}

func TestRegistryOnStreamEvent(t *testing.T) {
	r := plugin.NewRegistry(nil)
	r.Register(&testStreamInterceptor{testPlugin: testPlugin{name: "si", enabled: true}})

	states := r.NewStreamStates("model")
	block := &format.CoreContentBlock{Type: "reasoning"}
	consumed, _ := r.OnStreamEvent("model", plugin.StreamEvent{Type: "block_start", Index: 0, Block: block}, states)
	if !consumed {
		t.Fatal("should consume thinking block_start")
	}

	consumed2, _ := r.OnStreamEvent("model", plugin.StreamEvent{Type: "block_start", Index: 1, Block: &format.CoreContentBlock{Type: "text"}}, states)
	if consumed2 {
		t.Fatal("should not consume text block_start")
	}
}

func TestRegistryNilSafe(t *testing.T) {
	var r *plugin.Registry
	// All methods should be nil-safe.
	r.PreprocessInput("m", nil)
	r.MutateRequest(&plugin.RequestContext{}, &format.CoreRequest{})
	r.InjectTools(&plugin.RequestContext{})
	r.FilterContent(&plugin.RequestContext{}, format.CoreContentBlock{})
	r.TransformError("m", "msg")
	r.NewSessionData()
	r.NewStreamStates("m")
	r.OnStreamEvent("m", plugin.StreamEvent{}, nil)
	r.OnStreamComplete("m", nil, "", nil)
	if r.HasEnabled("m") {
		t.Fatal("nil registry should not have enabled plugins")
	}
}

type onStreamCompleteRecorder struct {
	testPlugin
	called bool
}

func (r *onStreamCompleteRecorder) NewStreamState() any {
	return nil
}

func (r *onStreamCompleteRecorder) OnStreamEvent(_ *plugin.StreamContext, _ plugin.StreamEvent) (bool, []openai.StreamEvent) {
	return false, nil
}

func (r *onStreamCompleteRecorder) OnStreamComplete(_ *plugin.StreamContext, _ string) {
	r.called = true
}

func TestRegistryOnStreamCompleteDispatch(t *testing.T) {
	r := plugin.NewRegistry(nil)
	rec := &onStreamCompleteRecorder{testPlugin: testPlugin{name: "sc", enabled: true}}
	r.Register(rec)

	states := r.NewStreamStates("model")
	r.OnStreamComplete("model", states, "hello", nil)
	if !rec.called {
		t.Fatal("OnStreamComplete not called for enabled plugin")
	}

	// Disabled plugin should not be called.
	rec2 := &onStreamCompleteRecorder{testPlugin: testPlugin{name: "sc2", enabled: false}}
	r.Register(rec2)
	r.OnStreamComplete("model", r.NewStreamStates("model"), "hello", nil)
	if rec2.called {
		t.Fatal("OnStreamComplete called for disabled plugin")
	}

}
