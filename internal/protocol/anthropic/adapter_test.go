package anthropic_test

import (
	"context"
	"encoding/json"
	"testing"

	"moonbridge/internal/format"
	"moonbridge/internal/protocol/anthropic"
)

// ---------------------------------------------------------------------------
// noopCacheManager — no-op implementation of anthropic.CacheManager
// ---------------------------------------------------------------------------

type noopCacheManager struct{}

func (n noopCacheManager) PlanAndInject(_ context.Context, _ *anthropic.MessageRequest, _ *format.CoreRequest) (key, ttl string) {
	return "", ""
}

func (n noopCacheManager) UpdateRegistry(_ context.Context, _, _ string, _ anthropic.Usage) {}

func newTestAdapter() *anthropic.AnthropicProviderAdapter {
	return anthropic.NewAnthropicProviderAdapter(0, noopCacheManager{}, format.CorePluginHooks{})
}

func TestFromCoreRequest_BasicTextMessage(t *testing.T) {
	// 测试纯文本 CoreRequest → *anthropic.MessageRequest
	adapter := newTestAdapter()

	coreReq := &format.CoreRequest{
		Model: "claude-sonnet-4",
		Messages: []format.CoreMessage{
			{Role: "user", Content: []format.CoreContentBlock{{Type: "text", Text: "hello"}}},
		},
	}

	result, err := adapter.FromCoreRequest(context.Background(), coreReq)
	if err != nil {
		t.Fatal(err)
	}

	msgReq, ok := result.(*anthropic.MessageRequest)
	if !ok {
		t.Fatal("expected *anthropic.MessageRequest")
	}

	if msgReq.Model != "claude-sonnet-4" {
		t.Errorf("model = %q, want %q", msgReq.Model, "claude-sonnet-4")
	}
	if len(msgReq.Messages) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgReq.Messages))
	}
	if msgReq.Messages[0].Role != "user" {
		t.Errorf("role = %q, want %q", msgReq.Messages[0].Role, "user")
	}
	if len(msgReq.Messages[0].Content) != 1 {
		t.Fatalf("got %d content blocks", len(msgReq.Messages[0].Content))
	}
	if msgReq.Messages[0].Content[0].Type != "text" {
		t.Errorf("content type = %q", msgReq.Messages[0].Content[0].Type)
	}
	if msgReq.Messages[0].Content[0].Text != "hello" {
		t.Errorf("text = %q", msgReq.Messages[0].Content[0].Text)
	}
}

func TestFromCoreRequest_SystemField(t *testing.T) {
	adapter := newTestAdapter()

	coreReq := &format.CoreRequest{
		Model:  "claude-sonnet-4",
		System: []format.CoreContentBlock{{Type: "text", Text: "You are helpful."}},
		Messages: []format.CoreMessage{
			{Role: "user", Content: []format.CoreContentBlock{{Type: "text", Text: "hi"}}},
		},
	}

	result, err := adapter.FromCoreRequest(context.Background(), coreReq)
	if err != nil {
		t.Fatal(err)
	}

	msgReq := result.(*anthropic.MessageRequest)
	if len(msgReq.System) != 1 {
		t.Errorf("got %d system blocks", len(msgReq.System))
	}
	if msgReq.System[0].Text != "You are helpful." {
		t.Errorf("system text = %q", msgReq.System[0].Text)
	}
}

func TestFromCoreRequest_ToolChoice(t *testing.T) {
	adapter := newTestAdapter()

	tests := []struct {
		name     string
		mode     string
		expected string
	}{
		{"auto", "auto", "auto"},
		{"any", "any", "any"},
		{"none", "none", "none"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			coreReq := &format.CoreRequest{
				Model:      "test",
				ToolChoice: &format.CoreToolChoice{Mode: tt.mode},
				Messages: []format.CoreMessage{
					{Role: "user", Content: []format.CoreContentBlock{{Type: "text", Text: "hi"}}},
				},
			}
			result, err := adapter.FromCoreRequest(context.Background(), coreReq)
			if err != nil {
				t.Fatal(err)
			}
			msgReq := result.(*anthropic.MessageRequest)
			if msgReq.ToolChoice.Type != tt.expected {
				t.Errorf("ToolChoice.Type = %q, want %q", msgReq.ToolChoice.Type, tt.expected)
			}
		})
	}
}

func TestFromCoreRequest_ToolChoiceForced(t *testing.T) {
	adapter := newTestAdapter()

	coreReq := &format.CoreRequest{
		Model:      "test",
		ToolChoice: &format.CoreToolChoice{Mode: "any", Name: "get_weather"},
		Messages: []format.CoreMessage{
			{Role: "user", Content: []format.CoreContentBlock{{Type: "text", Text: "weather?"}}},
		},
	}

	result, err := adapter.FromCoreRequest(context.Background(), coreReq)
	if err != nil {
		t.Fatal(err)
	}
	msgReq := result.(*anthropic.MessageRequest)
	if msgReq.ToolChoice.Type != "tool" {
		t.Errorf("ToolChoice.Type = %q, want %q", msgReq.ToolChoice.Type, "tool")
	}
	if msgReq.ToolChoice.Name != "get_weather" {
		t.Errorf("ToolChoice.Name = %q, want %q", msgReq.ToolChoice.Name, "get_weather")
	}
}

func TestFromCoreRequest_Tools(t *testing.T) {
	adapter := newTestAdapter()

	coreReq := &format.CoreRequest{
		Model: "claude-sonnet-4",
		Messages: []format.CoreMessage{
			{Role: "user", Content: []format.CoreContentBlock{{Type: "text", Text: "call a tool"}}},
		},
		Tools: []format.CoreTool{
			{Name: "get_weather", Description: "Get the weather", InputSchema: map[string]any{"type": "object"}},
		},
	}

	result, err := adapter.FromCoreRequest(context.Background(), coreReq)
	if err != nil {
		t.Fatal(err)
	}
	msgReq := result.(*anthropic.MessageRequest)
	if len(msgReq.Tools) != 1 {
		t.Fatalf("got %d tools, want 1", len(msgReq.Tools))
	}
	if msgReq.Tools[0].Name != "get_weather" {
		t.Errorf("tool name = %q", msgReq.Tools[0].Name)
	}
	if msgReq.Tools[0].Type != "" {
		t.Errorf("tool type = %q, want empty (Anthropic custom tools have no type field)", msgReq.Tools[0].Type)
	}
}
func TestFromCoreRequest_ImageMessage(t *testing.T) {
	adapter := newTestAdapter()

	coreReq := &format.CoreRequest{
		Model: "claude-sonnet-4",
		Messages: []format.CoreMessage{
			{
				Role: "user",
				Content: []format.CoreContentBlock{
					{Type: "text", Text: "what's in this image?"},
					{Type: "image", ImageData: "base64data", MediaType: "image/png"},
				},
			},
		},
	}

	result, err := adapter.FromCoreRequest(context.Background(), coreReq)
	if err != nil {
		t.Fatal(err)
	}
	msgReq := result.(*anthropic.MessageRequest)

	blocks := msgReq.Messages[0].Content
	if len(blocks) != 2 {
		t.Fatalf("got %d content blocks, want 2", len(blocks))
	}
	if blocks[0].Type != "text" {
		t.Errorf("block[0] type = %q", blocks[0].Type)
	}
	if blocks[1].Type != "image" {
		t.Errorf("block[1] type = %q", blocks[1].Type)
	}
	if blocks[1].Source == nil {
		t.Fatal("image block has nil Source")
	}
	if blocks[1].Source.Data != "base64data" {
		t.Errorf("image data = %q", blocks[1].Source.Data)
	}
	if blocks[1].Source.MediaType != "image/png" {
		t.Errorf("media type = %q", blocks[1].Source.MediaType)
	}
	if blocks[1].Source.Type != "base64" {
		t.Errorf("source type = %q", blocks[1].Source.Type)
	}
}

func TestFromCoreRequest_ToolUseContent(t *testing.T) {
	adapter := newTestAdapter()

	coreReq := &format.CoreRequest{
		Model: "claude-sonnet-4",
		Messages: []format.CoreMessage{
			{
				Role: "assistant",
				Content: []format.CoreContentBlock{
					{
						Type:      "tool_use",
						ToolUseID: "call_abc",
						ToolName:  "get_weather",
						ToolInput: json.RawMessage(`{"location":"NYC"}`),
					},
				},
			},
			{
				Role: "user",
				Content: []format.CoreContentBlock{
					{
						Type:      "tool_result",
						ToolUseID: "call_abc",
						ToolResultContent: []format.CoreContentBlock{
							{Type: "text", Text: "Sunny"},
						},
					},
				},
			},
		},
	}

	result, err := adapter.FromCoreRequest(context.Background(), coreReq)
	if err != nil {
		t.Fatal(err)
	}
	msgReq := result.(*anthropic.MessageRequest)

	if len(msgReq.Messages) != 3 {
		t.Fatalf("got %d messages, want 3", len(msgReq.Messages))
	}

	// First message is the inserted user placeholder.
	if msgReq.Messages[0].Role != "user" || len(msgReq.Messages[0].Content) != 1 || msgReq.Messages[0].Content[0].Text != "_" {
		t.Fatalf("expected user placeholder as first message")
	}

	// assistant tool_use block
	blocks0 := msgReq.Messages[1].Content
	if len(blocks0) != 1 || blocks0[0].Type != "tool_use" {
		t.Fatalf("expected tool_use block in assistant message")
	}
	if blocks0[0].ID != "call_abc" {
		t.Errorf("tool_use id = %q", blocks0[0].ID)
	}
	if blocks0[0].Name != "get_weather" {
		t.Errorf("tool_use name = %q", blocks0[0].Name)
	}

	// user tool_result block (index 2 because placeholder is at 0)
	blocks1 := msgReq.Messages[2].Content
	if len(blocks1) != 1 || blocks1[0].Type != "tool_result" {
		t.Fatalf("expected tool_result block in user message")
	}
	if blocks1[0].ToolUseID != "call_abc" {
		t.Errorf("tool_result tool_use_id = %q", blocks1[0].ToolUseID)
	}
}

func TestFromCoreRequest_Reasoning(t *testing.T) {
	adapter := newTestAdapter()

	coreReq := &format.CoreRequest{
		Model: "claude-sonnet-4",
		Messages: []format.CoreMessage{
			{
				Role: "assistant",
				Content: []format.CoreContentBlock{
					{
						Type:               "reasoning",
						ReasoningText:      "thinking step by step",
						ReasoningSignature: "sig123",
					},
					{Type: "text", Text: "final answer"},
				},
			},
			{Role: "user", Content: []format.CoreContentBlock{{Type: "text", Text: "ok"}}},
		},
	}

	result, err := adapter.FromCoreRequest(context.Background(), coreReq)
	if err != nil {
		t.Fatal(err)
	}
	msgReq := result.(*anthropic.MessageRequest)
	// First message is the inserted user placeholder.
	if len(msgReq.Messages) != 3 {
		t.Fatalf("got %d messages, want 3", len(msgReq.Messages))
	}
	if msgReq.Messages[0].Role != "user" || len(msgReq.Messages[0].Content) != 1 || msgReq.Messages[0].Content[0].Text != "_" {
		t.Fatalf("expected user placeholder as first message, got role=%q content=%v", msgReq.Messages[0].Role, msgReq.Messages[0].Content)
	}
	blocks := msgReq.Messages[1].Content

	if len(blocks) != 2 {
		t.Fatalf("got %d blocks, want 2", len(blocks))
	}
	if blocks[0].Type != "thinking" {
		t.Errorf("block[0] type = %q, want thinking", blocks[0].Type)
	}
	if blocks[0].Thinking != "thinking step by step" {
		t.Errorf("block[0] thinking = %q", blocks[0].Thinking)
	}
	if blocks[0].Signature != "sig123" {
		t.Errorf("block[0] signature = %q", blocks[0].Signature)
	}
	if blocks[1].Type != "text" {
		t.Errorf("block[1] type = %q, want text", blocks[1].Type)
	}
}

func TestFromCoreRequest_NilRequest(t *testing.T) {
	adapter := newTestAdapter()

	_, err := adapter.FromCoreRequest(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for nil request")
	}
}

func TestFromCoreRequest_DefaultParameters(t *testing.T) {
	adapter := newTestAdapter()

	coreReq := &format.CoreRequest{
		Model: "claude-sonnet-4",
		Messages: []format.CoreMessage{
			{Role: "user", Content: []format.CoreContentBlock{{Type: "text", Text: "hello"}}},
		},
		MaxTokens:     4096,
		StopSequences: []string{"\n\n"},
		Stream:        true,
		Metadata:      map[string]any{"session": "abc"},
	}

	result, err := adapter.FromCoreRequest(context.Background(), coreReq)
	if err != nil {
		t.Fatal(err)
	}
	msgReq := result.(*anthropic.MessageRequest)

	if msgReq.MaxTokens != 4096 {
		t.Errorf("MaxTokens = %d, want 4096", msgReq.MaxTokens)
	}
	if len(msgReq.StopSequences) != 1 || msgReq.StopSequences[0] != "\n\n" {
		t.Errorf("StopSequences = %v", msgReq.StopSequences)
	}
	if !msgReq.Stream {
		t.Error("Stream should be true")
	}
	v, ok := msgReq.Metadata["session"].(string)
	if !ok || v != "abc" {
		t.Errorf("Metadata = %v", msgReq.Metadata)
	}
}

func TestToCoreResponse_BasicText(t *testing.T) {
	adapter := newTestAdapter()

	anthResp := &anthropic.MessageResponse{
		ID:   "msg_123",
		Type: "message",
		Role: "assistant",
		Content: []anthropic.ContentBlock{
			{Type: "text", Text: "Hello!"},
		},
		StopReason: "end_turn",
		Usage:      anthropic.Usage{InputTokens: 10, OutputTokens: 20},
	}

	result, err := adapter.ToCoreResponse(context.Background(), anthResp)
	if err != nil {
		t.Fatal(err)
	}

	if result.ID != "msg_123" {
		t.Errorf("ID = %q", result.ID)
	}
	if result.Status != "completed" {
		t.Errorf("Status = %q", result.Status)
	}
	if result.Usage.InputTokens != 10 {
		t.Errorf("InputTokens = %d", result.Usage.InputTokens)
	}
	if result.Usage.OutputTokens != 20 {
		t.Errorf("OutputTokens = %d", result.Usage.OutputTokens)
	}
}

func TestToCoreResponse_BasicTextValueResponse(t *testing.T) {
	adapter := newTestAdapter()

	anthResp := anthropic.MessageResponse{
		ID:   "msg_value_123",
		Type: "message",
		Role: "assistant",
		Content: []anthropic.ContentBlock{
			{Type: "text", Text: "Hello from value"},
		},
		Model:      "claude-sonnet-4",
		StopReason: "end_turn",
		Usage:      anthropic.Usage{InputTokens: 7, OutputTokens: 11},
	}

	result, err := adapter.ToCoreResponse(context.Background(), anthResp)
	if err != nil {
		t.Fatal(err)
	}

	if result.ID != "msg_value_123" {
		t.Errorf("ID = %q", result.ID)
	}
	if result.Model != "claude-sonnet-4" {
		t.Errorf("Model = %q", result.Model)
	}
	if len(result.Messages) != 1 {
		t.Fatalf("got %d messages, want 1", len(result.Messages))
	}
	if len(result.Messages[0].Content) != 1 {
		t.Fatalf("got %d content blocks, want 1", len(result.Messages[0].Content))
	}
	if result.Messages[0].Content[0].Text != "Hello from value" {
		t.Errorf("text = %q", result.Messages[0].Content[0].Text)
	}
}

func TestToCoreResponse_Incomplete(t *testing.T) {
	adapter := newTestAdapter()

	anthResp := &anthropic.MessageResponse{
		Content: []anthropic.ContentBlock{
			{Type: "text", Text: "partial"},
		},
		StopReason: "max_tokens",
		Usage:      anthropic.Usage{InputTokens: 5, OutputTokens: 50},
	}

	result, err := adapter.ToCoreResponse(context.Background(), anthResp)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "incomplete" {
		t.Errorf("Status = %q, want incomplete", result.Status)
	}
}

func TestToCoreResponse_ContentFiltered(t *testing.T) {
	adapter := newTestAdapter()

	anthResp := &anthropic.MessageResponse{
		StopReason: "content_filtered",
		Usage:      anthropic.Usage{InputTokens: 3, OutputTokens: 0},
	}

	result, err := adapter.ToCoreResponse(context.Background(), anthResp)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "failed" {
		t.Errorf("Status = %q, want failed", result.Status)
	}
	if result.Error == nil || result.Error.Type != "content_filter" {
		t.Errorf("Error = %v, want content_filter", result.Error)
	}
}

func TestToCoreResponse_ToolUse(t *testing.T) {
	adapter := newTestAdapter()

	anthResp := &anthropic.MessageResponse{
		Content: []anthropic.ContentBlock{
			{Type: "text", Text: "calling tool"},
			{Type: "tool_use", ID: "tu_1", Name: "get_weather", Input: json.RawMessage(`{"loc":"NYC"}`)},
		},
		StopReason: "tool_use",
		Usage:      anthropic.Usage{InputTokens: 5, OutputTokens: 10},
	}

	result, err := adapter.ToCoreResponse(context.Background(), anthResp)
	if err != nil {
		t.Fatal(err)
	}

	if result.Status != "completed" {
		t.Errorf("Status = %q, want completed", result.Status)
	}
	if len(result.Messages) != 1 {
		t.Fatalf("got %d messages, want 1", len(result.Messages))
	}
	if result.Messages[0].Role != "assistant" {
		t.Errorf("role = %q", result.Messages[0].Role)
	}
	blocks := result.Messages[0].Content
	if len(blocks) != 2 {
		t.Fatalf("got %d content blocks, want 2", len(blocks))
	}
	if blocks[0].Type != "text" || blocks[0].Text != "calling tool" {
		t.Errorf("block[0] = %+v", blocks[0])
	}
	if blocks[1].Type != "tool_use" || blocks[1].ToolUseID != "tu_1" || blocks[1].ToolName != "get_weather" {
		t.Errorf("block[1] = %+v", blocks[1])
	}
}

func TestToCoreResponse_Thinking(t *testing.T) {
	adapter := newTestAdapter()

	anthResp := &anthropic.MessageResponse{
		Content: []anthropic.ContentBlock{
			{Type: "thinking", Thinking: "let me think", Signature: "sig_abc"},
			{Type: "text", Text: "final"},
		},
		StopReason: "end_turn",
		Usage:      anthropic.Usage{InputTokens: 10, OutputTokens: 20},
	}

	result, err := adapter.ToCoreResponse(context.Background(), anthResp)
	if err != nil {
		t.Fatal(err)
	}

	blocks := result.Messages[0].Content
	if len(blocks) != 2 {
		t.Fatalf("got %d blocks, want 2", len(blocks))
	}
	if blocks[0].Type != "reasoning" {
		t.Errorf("block[0] type = %q, want reasoning", blocks[0].Type)
	}
	if blocks[0].ReasoningText != "let me think" {
		t.Errorf("block[0] reasoning text = %q", blocks[0].ReasoningText)
	}
	if blocks[0].ReasoningSignature != "sig_abc" {
		t.Errorf("block[0] signature = %q", blocks[0].ReasoningSignature)
	}
	if blocks[1].Type != "text" || blocks[1].Text != "final" {
		t.Errorf("block[1] = %+v", blocks[1])
	}
}

func TestToCoreResponse_WrongType(t *testing.T) {
	adapter := newTestAdapter()

	_, err := adapter.ToCoreResponse(context.Background(), "not a response")
	if err == nil {
		t.Fatal("expected error for wrong type")
	}
}

func TestFromCoreRequest_PluginHooksCalled(t *testing.T) {
	called := false
	hooks := format.CorePluginHooks{
		MutateCoreRequest: func(_ context.Context, req *format.CoreRequest) {
			called = true
			req.Model = "mutated-model"
		},
	}
	adapter := anthropic.NewAnthropicProviderAdapter(0, noopCacheManager{}, hooks)

	coreReq := &format.CoreRequest{
		Model: "claude-sonnet-4",
		Messages: []format.CoreMessage{
			{Role: "user", Content: []format.CoreContentBlock{{Type: "text", Text: "hi"}}},
		},
	}

	result, err := adapter.FromCoreRequest(context.Background(), coreReq)
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Error("MutateCoreRequest was not called")
	}
	msgReq := result.(*anthropic.MessageRequest)
	if msgReq.Model != "mutated-model" {
		t.Errorf("Model = %q, want mutated-model", msgReq.Model)
	}
}

func TestFromCoreRequest_TemperatureAndTopP(t *testing.T) {
	adapter := newTestAdapter()

	temp := 0.7
	topP := 0.9
	coreReq := &format.CoreRequest{
		Model: "claude-sonnet-4",
		Messages: []format.CoreMessage{
			{Role: "user", Content: []format.CoreContentBlock{{Type: "text", Text: "hello"}}},
		},
		Temperature: &temp,
		TopP:        &topP,
	}

	result, err := adapter.FromCoreRequest(context.Background(), coreReq)
	if err != nil {
		t.Fatal(err)
	}
	msgReq := result.(*anthropic.MessageRequest)

	if msgReq.Temperature == nil || *msgReq.Temperature != 0.7 {
		t.Errorf("Temperature = %v, want 0.7", msgReq.Temperature)
	}
	if msgReq.TopP == nil || *msgReq.TopP != 0.9 {
		t.Errorf("TopP = %v, want 0.9", msgReq.TopP)
	}
}

// ============================================================================
// Regression tests for bug fixes
// ============================================================================

func TestFromCoreRequest_MergesConsecutiveToolResultMessages(t *testing.T) {
	adapter := newTestAdapter()

	req := &format.CoreRequest{
		Model: "claude-sonnet-4",
		Messages: []format.CoreMessage{
			{
				Role: "assistant",
				Content: []format.CoreContentBlock{
					{
						Type:      "tool_use",
						ToolUseID: "call_1",
						ToolName:  "get_weather",
						ToolInput: json.RawMessage(`{"city":"Paris"}`),
					},
				},
			},
			{
				Role: "user",
				Content: []format.CoreContentBlock{
					{
						Type:      "tool_result",
						ToolUseID: "call_1",
						ToolResultContent: []format.CoreContentBlock{
							{Type: "text", Text: "Sunny, 25°C"},
						},
					},
				},
			},
			{
				Role: "tool",
				Content: []format.CoreContentBlock{
					{
						Type:      "tool_result",
						ToolUseID: "call_2",
						ToolResultContent: []format.CoreContentBlock{
							{Type: "text", Text: "Windy, 15°C"},
						},
					},
				},
			},
		},
	}

	result, err := adapter.FromCoreRequest(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}

	msgReq, ok := result.(*anthropic.MessageRequest)
	if !ok {
		t.Fatalf("expected *MessageRequest, got %T", result)
	}

	// We should have: placeholder user + assistant + merged user
	if len(msgReq.Messages) != 3 {
		t.Fatalf("expected 3 messages (placeholder + assistant + merged user), got %d", len(msgReq.Messages))
	}

	// First message is the inserted user placeholder.
	if msgReq.Messages[0].Role != "user" || len(msgReq.Messages[0].Content) != 1 || msgReq.Messages[0].Content[0].Text != "_" {
		t.Fatalf("expected user placeholder as first message, got role=%q", msgReq.Messages[0].Role)
	}

	// Second message should be assistant with tool_use
	if msgReq.Messages[1].Role != "assistant" {
		t.Errorf("messages[1].Role = %q, want assistant", msgReq.Messages[1].Role)
	}

	// Third message should be user with 2 tool_result blocks (merged)
	merged := msgReq.Messages[2]
	if merged.Role != "user" {
		t.Errorf("merged message role = %q, want user", merged.Role)
	}
	if len(merged.Content) != 2 {
		t.Fatalf("merged user message has %d content blocks, want 2", len(merged.Content))
	}
	for i, block := range merged.Content {
		if block.Type != "tool_result" {
			t.Errorf("merged.Content[%d].Type = %q, want tool_result", i, block.Type)
		}
	}
}
