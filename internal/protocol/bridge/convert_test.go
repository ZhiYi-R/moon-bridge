package bridge_test

import (
	"encoding/json"
	"testing"

	deepseekv4 "moonbridge/internal/extension/deepseek_v4"
	"moonbridge/internal/extension/plugin"
	"moonbridge/internal/extension/pluginhooks"
	"moonbridge/internal/extension/visual"
	"moonbridge/internal/foundation/config"
	"moonbridge/internal/foundation/openai"
	"moonbridge/internal/protocol/anthropic"
	"moonbridge/internal/protocol/bridge"
	"moonbridge/internal/protocol/cache"
)

func testBridge() *bridge.Bridge {
	return testBridgeWithConfig(config.Config{
		DefaultMaxTokens: 1024,
		Routes:           map[string]config.RouteEntry{"gpt-test": {Provider: "default", Model: "claude-test"}},
		Cache: config.CacheConfig{
			Mode:                     "explicit",
			TTL:                      "1h",
			PromptCaching:            true,
			ExplicitCacheBreakpoints: true,
			MaxBreakpoints:           4,
			MinCacheTokens:           1,
			MinBreakpointTokens:      1,
		},
	})
}

func testBridgeWithConfig(cfg config.Config) *bridge.Bridge {
	plugins := plugin.NewRegistry(nil)
	plugins.Register(deepseekv4.NewPlugin())
	plugins.Register(visual.NewPlugin())
	if err := plugins.InitAll(&cfg); err != nil {
		panic(err)
	}
	return bridge.New(cfg, cache.NewMemoryRegistry(), pluginhooks.PluginHooksFromRegistry(plugins))
}

func extensionEnabled(enabled bool) config.ExtensionSettings {
	return config.ExtensionSettings{Enabled: &enabled}
}

func testBridgeWithWebSearchDisabled() *bridge.Bridge {
	cfg := config.Config{
		DefaultMaxTokens: 1024,
		Routes:           map[string]config.RouteEntry{"gpt-test": {Provider: "default", Model: "claude-test"}},
		WebSearchSupport: config.WebSearchSupportDisabled,
		Cache: config.CacheConfig{
			Mode:                     "explicit",
			TTL:                      "1h",
			PromptCaching:            true,
			ExplicitCacheBreakpoints: true,
			MaxBreakpoints:           4,
			MinCacheTokens:           1,
			MinBreakpointTokens:      1,
		},
	}
	return testBridgeWithConfig(cfg)
}

func mustMarshalRaw(t *testing.T, value any) json.RawMessage {
	t.Helper()

	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	return data
}
func TestToAnthropicConvertsTextToolsToolChoiceAndCache(t *testing.T) {
	request := openai.ResponsesRequest{
		Model:           "gpt-test",
		Instructions:    "You are helpful.",
		Input:           json.RawMessage(`"hello"`),
		MaxOutputTokens: 50,
		PromptCacheKey:  "tenant-docs",
		Tools: []openai.Tool{{
			Type:        "function",
			Name:        "lookup",
			Description: "Lookup a record",
			Parameters:  map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string"}}},
		}},
		ToolChoice: json.RawMessage(`"required"`),
	}

	converted, plan, err := testBridge().ToAnthropic(request, nil)
	if err != nil {
		t.Fatalf("ToAnthropic(, nil) error = %v", err)
	}
	if converted.Model != "claude-test" || converted.MaxTokens != 50 {
		t.Fatalf("converted model/max = %s/%d", converted.Model, converted.MaxTokens)
	}
	if converted.System[0].Text != "You are helpful." {
		t.Fatalf("system = %+v", converted.System)
	}
	if converted.Messages[0].Content[0].Text != "hello" {
		t.Fatalf("messages = %+v", converted.Messages)
	}
	if converted.Tools[0].Name != "lookup" {
		t.Fatalf("tools = %+v", converted.Tools)
	}
	if converted.ToolChoice.Type != "any" {
		t.Fatalf("tool choice = %+v", converted.ToolChoice)
	}
	if plan.Mode != "explicit" || len(plan.Breakpoints) == 0 {
		t.Fatalf("cache plan = %+v", plan)
	}
	if converted.Tools[0].CacheControl == nil || converted.System[0].CacheControl == nil {
		t.Fatalf("cache controls not injected: tools=%+v system=%+v", converted.Tools, converted.System)
	}
}

func TestToAnthropicInjectsVisualToolsForOptInModel(t *testing.T) {
	cfg := config.Config{
		DefaultMaxTokens: 1024,
		Routes: map[string]config.RouteEntry{"gpt-test": {
			Provider: "default",
			Model:    "claude-test",
			Extensions: map[string]config.ExtensionSettings{
				visual.PluginName: extensionEnabled(true),
			},
		}},
		Cache: config.CacheConfig{Mode: "off"},
	}
	converted, _, err := testBridgeWithConfig(cfg).ToAnthropic(openai.ResponsesRequest{
		Model: "gpt-test",
		Input: json.RawMessage(`"describe this screenshot"`),
	}, nil)
	if err != nil {
		t.Fatalf("ToAnthropic() error = %v", err)
	}
	var names []string
	for _, tool := range converted.Tools {
		names = append(names, tool.Name)
	}
	if len(names) != 2 || names[0] != visual.ToolVisualBrief || names[1] != visual.ToolVisualQA {
		t.Fatalf("visual tools = %v, want [%s %s]", names, visual.ToolVisualBrief, visual.ToolVisualQA)
	}
}

func TestToAnthropicConvertsInputImageDataURL(t *testing.T) {
	converted, _, err := testBridge().ToAnthropic(openai.ResponsesRequest{
		Model: "gpt-test",
		Input: json.RawMessage(`[
			{"role":"user","content":[
				{"type":"input_text","text":"what is in this image?"},
				{"type":"input_image","image_url":"data:image/png;base64,abc123"}
			]}
		]`),
	}, nil)
	if err != nil {
		t.Fatalf("ToAnthropic() error = %v", err)
	}
	blocks := converted.Messages[0].Content
	if len(blocks) != 2 {
		t.Fatalf("content blocks = %+v", blocks)
	}
	if blocks[0].Type != "text" || blocks[0].Text != "what is in this image?" {
		t.Fatalf("text block = %+v", blocks[0])
	}
	source := blocks[1].Source
	if blocks[1].Type != "image" || source == nil {
		t.Fatalf("image block = %+v", blocks[1])
	}
	if source.Type != "base64" || source.MediaType != "image/png" || source.Data != "abc123" {
		t.Fatalf("image source = %+v", source)
	}
}

func TestToAnthropicAutomaticCacheAddsExplicitBreakpoints(t *testing.T) {
	bridgeUnderTest := bridge.New(config.Config{
		DefaultMaxTokens: 1024,
		Routes:           map[string]config.RouteEntry{"gpt-test": {Provider: "default", Model: "claude-test"}},
		Cache: config.CacheConfig{
			Mode:                     "automatic",
			TTL:                      "5m",
			PromptCaching:            true,
			AutomaticPromptCache:     true,
			ExplicitCacheBreakpoints: true,
			MaxBreakpoints:           4,
			MinCacheTokens:           1,
			MinBreakpointTokens:      1,
		},
	}, cache.NewMemoryRegistry(), bridge.PluginHooks{})

	converted, plan, err := bridgeUnderTest.ToAnthropic(openai.ResponsesRequest{
		Model:        "gpt-test",
		Instructions: "stable system prompt",
		Input:        json.RawMessage(`"current question"`),
	}, nil)
	if err != nil {
		t.Fatalf("ToAnthropic(, nil) error = %v", err)
	}

	if plan.Mode != "hybrid" {
		t.Fatalf("plan.Mode = %q, want hybrid", plan.Mode)
	}
	if converted.CacheControl == nil {
		t.Fatal("top-level cache_control is nil")
	}
	if converted.System[0].CacheControl == nil {
		t.Fatalf("system cache_control not injected: %+v", converted.System)
	}
	if converted.Messages[0].Content[0].CacheControl == nil {
		t.Fatalf("message cache_control not injected: %+v", converted.Messages)
	}
}

func TestToAnthropicCanDisableTopLevelAutomaticCache(t *testing.T) {
	bridgeUnderTest := bridge.New(config.Config{
		DefaultMaxTokens: 1024,
		Routes:           map[string]config.RouteEntry{"gpt-test": {Provider: "default", Model: "claude-test"}},
		Cache: config.CacheConfig{
			Mode:                     "automatic",
			TTL:                      "5m",
			PromptCaching:            true,
			AutomaticPromptCache:     false,
			ExplicitCacheBreakpoints: true,
			MaxBreakpoints:           4,
			MinCacheTokens:           1,
			MinBreakpointTokens:      1,
		},
	}, cache.NewMemoryRegistry(), bridge.PluginHooks{})

	converted, plan, err := bridgeUnderTest.ToAnthropic(openai.ResponsesRequest{
		Model:        "gpt-test",
		Instructions: "stable system prompt",
		Input:        json.RawMessage(`"current question"`),
	}, nil)
	if err != nil {
		t.Fatalf("ToAnthropic() error = %v", err)
	}

	if plan.Mode != "explicit" {
		t.Fatalf("plan.Mode = %q, want explicit", plan.Mode)
	}
	if converted.CacheControl != nil {
		t.Fatalf("top-level cache_control = %+v, want nil", converted.CacheControl)
	}
	if converted.System[0].CacheControl == nil {
		t.Fatalf("system cache_control not injected: %+v", converted.System)
	}
	if converted.Messages[0].Content[0].CacheControl == nil {
		t.Fatalf("message cache_control not injected: %+v", converted.Messages)
	}
}

func TestToAnthropicSpreadsMessageBreakpointsAcrossLongHistory(t *testing.T) {
	converted, plan, err := testBridge().ToAnthropic(openai.ResponsesRequest{
		Model:        "gpt-test",
		Instructions: "stable system prompt",
		Tools: []openai.Tool{{
			Type:       "function",
			Name:       "lookup",
			Parameters: map[string]any{"type": "object"},
		}},
		Input: json.RawMessage(`[
			{"role":"user","content":[{"type":"input_text","text":"u1"}]},
			{"role":"assistant","content":[{"type":"output_text","text":"a1"}]},
			{"role":"user","content":[{"type":"input_text","text":"u2"}]},
			{"role":"assistant","content":[{"type":"output_text","text":"a2"}]},
			{"role":"user","content":[{"type":"input_text","text":"u3"}]}
		]`),
	}, nil)
	if err != nil {
		t.Fatalf("ToAnthropic() error = %v", err)
	}

	if len(plan.Breakpoints) != 4 {
		t.Fatalf("breakpoints = %+v", plan.Breakpoints)
	}
	if converted.Tools[0].CacheControl == nil || converted.System[0].CacheControl == nil {
		t.Fatalf("stable prefixes not cached: tools=%+v system=%+v", converted.Tools, converted.System)
	}
	if converted.Messages[0].Content[0].CacheControl != nil {
		t.Fatalf("unexpected cache_control on oldest user message: %+v", converted.Messages[0])
	}
	if converted.Messages[2].Content[0].CacheControl == nil || converted.Messages[4].Content[0].CacheControl == nil {
		t.Fatalf("message cache controls not spread across user history: %+v", converted.Messages)
	}
}

func TestToAnthropicConvertsFunctionCallOutput(t *testing.T) {
	request := openai.ResponsesRequest{
		Model: "gpt-test",
		Input: json.RawMessage(`[
			{"type":"function_call_output","call_id":"toolu_123","output":"{\"ok\":true}"}
		]`),
	}

	converted, _, err := testBridge().ToAnthropic(request, nil)
	if err != nil {
		t.Fatalf("ToAnthropic() error = %v", err)
	}
	block := converted.Messages[0].Content[0]
	if block.Type != "tool_result" || block.ToolUseID != "toolu_123" {
		t.Fatalf("block = %+v", block)
	}
}

func TestFromAnthropicNormalizesUsageAndToolUse(t *testing.T) {
	response := anthropic.MessageResponse{
		ID:         "msg_123",
		Type:       "message",
		Role:       "assistant",
		StopReason: "tool_use",
		Content: []anthropic.ContentBlock{
			{Type: "text", Text: "Need a lookup."},
			{Type: "tool_use", ID: "toolu_123", Name: "lookup", Input: json.RawMessage(`{"id":"42"}`)},
		},
		Usage: anthropic.Usage{
			InputTokens:              10,
			CacheReadInputTokens:     90,
			CacheCreationInputTokens: 30,
			OutputTokens:             12,
		},
	}

	converted := testBridge().FromAnthropic(response, "gpt-test")
	if converted.Status != "completed" {
		t.Fatalf("status = %q", converted.Status)
	}
	if converted.OutputText != "Need a lookup." {
		t.Fatalf("OutputText = %q", converted.OutputText)
	}
	if converted.Output[1].CallID != "toolu_123" || converted.Output[1].Arguments != `{"id":"42"}` {
		t.Fatalf("function call = %+v", converted.Output[1])
	}
	if converted.Usage.InputTokens != 130 {
		t.Fatalf("InputTokens = %d", converted.Usage.InputTokens)
	}
	if converted.Usage.InputTokensDetails.CachedTokens != 90 {
		t.Fatalf("CachedTokens = %d", converted.Usage.InputTokensDetails.CachedTokens)
	}
}

func TestFromAnthropicMapsMaxTokensToIncomplete(t *testing.T) {
	converted := testBridge().FromAnthropic(anthropic.MessageResponse{
		ID:         "msg_123",
		Type:       "message",
		Role:       "assistant",
		StopReason: "max_tokens",
		Content:    []anthropic.ContentBlock{{Type: "text", Text: "cut"}},
	}, "gpt-test")

	if converted.Status != "incomplete" {
		t.Fatalf("status = %q", converted.Status)
	}
	if converted.IncompleteDetails == nil || converted.IncompleteDetails.Reason != "max_output_tokens" {
		t.Fatalf("incomplete_details = %+v", converted.IncompleteDetails)
	}
}
