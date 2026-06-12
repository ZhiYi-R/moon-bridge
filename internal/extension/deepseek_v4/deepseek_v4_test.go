package deepseekv4

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	pluginpkg "moonbridge/internal/extension/plugin"
	"moonbridge/internal/format"
	"moonbridge/internal/protocol/anthropic"
	"moonbridge/internal/protocol/openai"
)

func TestStripReasoningContentStripsField(t *testing.T) {
	input := json.RawMessage(`[{"role":"assistant","content":"hi","reasoning_content":"think"}]`)
	out := StripReasoningContent(input)
	if string(out) == string(input) {
		t.Fatal("expected reasoning_content to be stripped")
	}
	var items []map[string]any
	if err := json.Unmarshal(out, &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %v", items)
	}
	if _, ok := items[0]["reasoning_content"]; ok {
		t.Fatal("reasoning_content should be removed")
	}
}

func TestStripReasoningContentLeavesStringInputAlone(t *testing.T) {
	input := json.RawMessage(`"hello"`)
	out := StripReasoningContent(input)
	if string(out) != `"hello"` {
		t.Fatalf("out = %s", out)
	}
}

func TestExtractReasoningContent(t *testing.T) {
	blocks := []anthropic.ContentBlock{
		{Type: "reasoning_content", Text: "Let me think..."},
		{Type: "text", Text: "Answer"},
	}
	got := ExtractReasoningContent(blocks)
	if got != "Let me think..." {
		t.Fatalf("got = %q", got)
	}
}

func TestInjectReasoningIntoOutput(t *testing.T) {
	output := []openai.OutputItem{
		{Type: "message", Role: "assistant", Content: []openai.ContentPart{{Type: "output_text", Text: "Answer"}}},
	}
	out := InjectReasoningIntoOutput(output, "Let me think...")
	if len(out) != 2 {
		t.Fatalf("len = %d", len(out))
	}
	if out[0].Type != "message" || out[0].Content[0].Text != "Let me think..." {
		t.Fatalf("first item = %+v", out[0])
	}
}

func TestToAnthropicRequestClearsTemperatureAndTopP(t *testing.T) {
	f := 0.5
	req := anthropic.MessageRequest{Temperature: &f, TopP: &f, MaxTokens: 1000}
	ToAnthropicRequest(&req, nil)
	if req.Temperature != nil || req.TopP != nil {
		t.Fatal("expected temperature and top_p to be cleared")
	}
	if req.Thinking != nil {
		t.Fatalf("expected no thinking mapping, got %+v", req.Thinking)
	}
}

func TestToAnthropicRequestMapsReasoningEffort(t *testing.T) {
	tests := map[string]string{
		"high":  "high",
		"xhigh": "max",
		"max":   "max",
	}
	for effort, want := range tests {
		req := anthropic.MessageRequest{Model: "deepseek-v4-pro"}
		ToAnthropicRequest(&req, map[string]any{"effort": effort})
		if req.Model != "deepseek-v4-pro" {
			t.Fatalf("Model = %q", req.Model)
		}
		if req.OutputConfig == nil || req.OutputConfig.Effort != want {
			t.Fatalf("OutputConfig = %+v, want effort %q", req.OutputConfig, want)
		}
		if req.Thinking != nil {
			t.Fatalf("expected no thinking mapping, got %+v", req.Thinking)
		}
	}
}

func TestToAnthropicRequestIgnoresUnsupportedReasoningEffort(t *testing.T) {
	req := anthropic.MessageRequest{Model: "deepseek-v4-pro"}
	ToAnthropicRequest(&req, map[string]any{"effort": "medium"})
	if req.OutputConfig != nil {
		t.Fatalf("OutputConfig = %+v, want nil", req.OutputConfig)
	}
}

func TestEncodeDecodeThinkingSummaryPreservesSignatureOnlyBlock(t *testing.T) {
	encoded := EncodeThinkingSummary(format.CoreContentBlock{Type: "reasoning", ReasoningSignature: "sig_only"})
	if encoded == "" {
		t.Fatal("encoded summary is empty")
	}
	decoded, ok := DecodeThinkingSummary(encoded)
	if !ok {
		t.Fatal("expected encoded summary to decode")
	}
	if decoded.Type != "reasoning" || decoded.ReasoningText != "" || decoded.ReasoningSignature != "sig_only" {
		t.Fatalf("decoded block = %+v", decoded)
	}
}

func TestPrependThinkingWarnsWhenUsingRequiredFallback(t *testing.T) {
	var logs bytes.Buffer
	p := NewPlugin(func(string) bool { return true })
	err := p.Init(pluginpkg.PluginContext{
		Logger: slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})),
	})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	messages := []format.CoreMessage{{
		Role:    "assistant",
		Content: []format.CoreContentBlock{{Type: "tool_use", ToolUseID: "call_missing"}},
	}}
	got := p.PrependThinkingForToolUse(messages, "call_missing", nil, NewState())

	if got[0].Content[0].Type != "reasoning" || got[0].Content[0].ReasoningText != "" {
		t.Fatalf("fallback thinking block = %+v", got[0].Content)
	}
	logText := logs.String()
	if !strings.Contains(logText, "补空 thinking block") || !strings.Contains(logText, "tool_call_id=call_missing") {
		t.Fatalf("warning log = %q", logText)
	}

	logs.Reset()
	summary := []openai.ReasoningItemSummary{{
		Type: "summary_text",
		Text: EncodeThinkingSummary(format.CoreContentBlock{Type: "reasoning", ReasoningSignature: "sig_summary"}),
	}}
	got = p.PrependThinkingForToolUse([]format.CoreMessage{{
		Role:    "assistant",
		Content: []format.CoreContentBlock{{Type: "tool_use", ToolUseID: "call_summary"}},
	}}, "call_summary", summary, NewState())

	if got[0].Content[0].Type != "reasoning" || got[0].Content[0].ReasoningSignature != "sig_summary" {
		t.Fatalf("summary thinking block = %+v", got[0].Content)
	}
	if logs.Len() != 0 {
		t.Fatalf("unexpected warning for summary thinking = %q", logs.String())
	}

	logs.Reset()
	state := NewState()
	state.RememberForToolCalls([]string{"call_cached"}, format.CoreContentBlock{Type: "reasoning", ReasoningSignature: "sig_cached"})
	got = p.PrependThinkingForToolUse([]format.CoreMessage{{
		Role:    "assistant",
		Content: []format.CoreContentBlock{{Type: "tool_use", ToolUseID: "call_cached"}},
	}}, "call_cached", nil, state)

	if got[0].Content[0].Type != "reasoning" || got[0].Content[0].ReasoningSignature != "sig_cached" {
		t.Fatalf("cached thinking block = %+v", got[0].Content)
	}
	if logs.Len() != 0 {
		t.Fatalf("unexpected warning for cached thinking = %q", logs.String())
	}
}
