package server

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	deepseekv4 "moonbridge/internal/extension/deepseek_v4"
	"moonbridge/internal/format"
	"moonbridge/internal/protocol/anthropic"
	"moonbridge/internal/session"
)

func TestCoreResponseToStreamEventsEmitsTextAndUsage(t *testing.T) {
	resp := &format.CoreResponse{
		ID:     "msg_1",
		Status: "completed",
		Model:  "deepseek-v4-pro",
		Messages: []format.CoreMessage{{
			Role: "assistant",
			Content: []format.CoreContentBlock{
				{Type: "reasoning", ReasoningText: "looked at visual result", ReasoningSignature: "sig_1"},
				{Type: "text", Text: "two cartoon dogs under a starry sky"},
			},
		}},
		Usage: format.CoreUsage{InputTokens: 11, OutputTokens: 7, CachedInputTokens: 3},
	}

	events := collectCoreStreamEvents(coreResponseToStreamEvents(context.Background(), resp))
	if len(events) == 0 {
		t.Fatal("no stream events emitted")
	}
	var gotText bool
	var gotReasoning bool
	completed := events[len(events)-1]
	for _, ev := range events {
		if ev.Type == format.CoreTextDelta && ev.Delta == "two cartoon dogs under a starry sky" {
			gotText = true
		}
		if ev.Type == format.CoreContentBlockStarted && ev.ContentBlock != nil && ev.ContentBlock.Type == "reasoning" {
			gotReasoning = true
		}
	}
	if !gotText {
		t.Fatalf("text delta not emitted: %+v", events)
	}
	if !gotReasoning {
		t.Fatalf("reasoning block not emitted: %+v", events)
	}
	if completed.Type != format.CoreEventCompleted || completed.Usage == nil {
		t.Fatalf("completed event = %+v", completed)
	}
	if completed.Usage.InputTokens != 11 || completed.Usage.OutputTokens != 7 || completed.Usage.CachedInputTokens != 3 {
		t.Fatalf("completed usage = %+v", completed.Usage)
	}
}

func TestCoreRequestHasImage(t *testing.T) {
	if coreRequestHasImage(&format.CoreRequest{
		Messages: []format.CoreMessage{{
			Role: "user",
			Content: []format.CoreContentBlock{
				{Type: "text", Text: "describe"},
				{Type: "image", ImageData: "abc", MediaType: "image/png"},
			},
		}},
	}) != true {
		t.Fatal("expected image request to be detected")
	}
	if coreRequestHasImage(&format.CoreRequest{
		Messages: []format.CoreMessage{{
			Role:    "user",
			Content: []format.CoreContentBlock{{Type: "text", Text: "hello"}},
		}},
	}) {
		t.Fatal("text-only request should not be treated as image request")
	}
	if !coreRequestHasImage(&format.CoreRequest{
		Messages: []format.CoreMessage{{
			Role: "tool",
			Content: []format.CoreContentBlock{{
				Type: "tool_result",
				ToolResultContent: []format.CoreContentBlock{
					{Type: "image", ImageData: "abc", MediaType: "image/png"},
				},
			}},
		}},
	}) {
		t.Fatal("image inside tool_result should be detected")
	}
}

func collectCoreStreamEvents(events <-chan format.CoreStreamEvent) []format.CoreStreamEvent {
	var collected []format.CoreStreamEvent
	for ev := range events {
		collected = append(collected, ev)
	}
	return collected
}

type fakeAnthropicAdapter struct{}

func (fakeAnthropicAdapter) ProviderProtocol() string { return "anthropic" }

func (fakeAnthropicAdapter) FromCoreRequest(context.Context, *format.CoreRequest) (any, error) {
	return &anthropic.MessageRequest{
		Model:     "claude-test",
		MaxTokens: 64,
		Messages: []anthropic.Message{{
			Role: "user",
			Content: []anthropic.ContentBlock{{
				Type: "text",
				Text: "hello",
			}},
		}},
	}, nil
}

func (fakeAnthropicAdapter) ToCoreResponse(_ context.Context, resp any) (*format.CoreResponse, error) {
	msgResp, ok := resp.(*anthropic.MessageResponse)
	if !ok {
		return nil, fmt.Errorf("expected *anthropic.MessageResponse, got %T", resp)
	}
	return &format.CoreResponse{
		ID:     msgResp.ID,
		Status: "completed",
		Messages: []format.CoreMessage{{
			Role:    "assistant",
			Content: []format.CoreContentBlock{{Type: "text", Text: msgResp.Content[0].Text}},
		}},
	}, nil
}

type fakeStrictAnthropicClient struct {
	got anthropic.MessageRequest
}

func (c *fakeStrictAnthropicClient) CreateMessage(_ context.Context, req any) (any, error) {
	msgReq, err := normalizeAnthropicRequest(req)
	if err != nil {
		return nil, err
	}
	c.got = msgReq
	return anthropic.MessageResponse{
		ID:         "msg_1",
		Content:    []anthropic.ContentBlock{{Type: "text", Text: "ok"}},
		StopReason: "end_turn",
	}, nil
}

func (c *fakeStrictAnthropicClient) StreamMessage(context.Context, any) (<-chan any, error) {
	return nil, nil
}

func TestAdapterCoreProviderDereferencesAnthropicRequestAndResponse(t *testing.T) {
	client := &fakeStrictAnthropicClient{}
	provider := newAdapterCoreProvider(fakeAnthropicAdapter{}, client)

	resp, err := provider.CreateCore(context.Background(), &format.CoreRequest{
		Model: "claude-test",
		Messages: []format.CoreMessage{{
			Role:    "user",
			Content: []format.CoreContentBlock{{Type: "text", Text: "hello"}},
		}},
	})
	if err != nil {
		t.Fatalf("CreateCore() error = %v", err)
	}
	if client.got.Model != "claude-test" {
		t.Fatalf("upstream model = %q, want claude-test", client.got.Model)
	}
	if resp.Messages[0].Content[0].Text != "ok" {
		t.Fatalf("response text = %q, want ok", resp.Messages[0].Content[0].Text)
	}
}

type fakeOpenAIResponseAdapter struct{}

func (fakeOpenAIResponseAdapter) ProviderProtocol() string { return "openai-response" }

func (fakeOpenAIResponseAdapter) FromCoreRequest(context.Context, *format.CoreRequest) (any, error) {
	return map[string]any{"model": "gpt-test"}, nil
}

func (fakeOpenAIResponseAdapter) ToCoreResponse(_ context.Context, resp any) (*format.CoreResponse, error) {
	raw, ok := resp.(json.RawMessage)
	if !ok {
		return nil, fmt.Errorf("expected json.RawMessage, got %T", resp)
	}
	return &format.CoreResponse{
		ID:     "resp_1",
		Status: "completed",
		Messages: []format.CoreMessage{{
			Role:    "assistant",
			Content: []format.CoreContentBlock{{Type: "text", Text: string(raw)}},
		}},
	}, nil
}

type fakeOpenAIResponseClient struct {
	got any
}

func (c *fakeOpenAIResponseClient) CreateMessage(_ context.Context, req any) (any, error) {
	c.got = req
	return json.RawMessage(`{"ok":true}`), nil
}

func (c *fakeOpenAIResponseClient) StreamMessage(context.Context, any) (<-chan any, error) {
	return nil, nil
}

func TestAdapterCoreProviderLeavesNonAnthropicPayloadsUnchanged(t *testing.T) {
	client := &fakeOpenAIResponseClient{}
	provider := newAdapterCoreProvider(fakeOpenAIResponseAdapter{}, client)

	resp, err := provider.CreateCore(context.Background(), &format.CoreRequest{Model: "gpt-test"})
	if err != nil {
		t.Fatalf("CreateCore() error = %v", err)
	}
	if _, ok := client.got.(map[string]any); !ok {
		t.Fatalf("client got %T, want map[string]any", client.got)
	}
	if resp.Messages[0].Content[0].Text != `{"ok":true}` {
		t.Fatalf("response text = %q", resp.Messages[0].Content[0].Text)
	}
}

type fakeAnthropicToolUseAdapter struct{}

func (fakeAnthropicToolUseAdapter) ProviderProtocol() string { return "anthropic" }

func (fakeAnthropicToolUseAdapter) FromCoreRequest(context.Context, *format.CoreRequest) (any, error) {
	return &anthropic.MessageRequest{
		Model:     "deepseek-v4-pro",
		MaxTokens: 64,
		Messages: []anthropic.Message{{
			Role: "assistant",
			Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "call_view_image",
				Name:  "view_image",
				Input: json.RawMessage(`{"path":"line-dog.jpg"}`),
			}},
		}},
	}, nil
}

func (fakeAnthropicToolUseAdapter) ToCoreResponse(_ context.Context, resp any) (*format.CoreResponse, error) {
	msgResp, ok := resp.(*anthropic.MessageResponse)
	if !ok {
		return nil, fmt.Errorf("expected *anthropic.MessageResponse, got %T", resp)
	}
	return &format.CoreResponse{
		ID:     msgResp.ID,
		Status: "completed",
		Messages: []format.CoreMessage{{
			Role:    "assistant",
			Content: []format.CoreContentBlock{{Type: "text", Text: msgResp.Content[0].Text}},
		}},
	}, nil
}

func TestAdapterCoreProviderPrependsDeepSeekThinkingBeforeAnthropicUpstream(t *testing.T) {
	client := &fakeStrictAnthropicClient{}
	sess := session.NewWithID("codex-session-visual")
	sess.InitExtensions(map[string]any{
		"deepseek_v4": deepseekv4.NewState(),
	})
	provider := newFinalizingAdapterCoreProvider(fakeAnthropicToolUseAdapter{}, client,
		func(_ context.Context, upstream any) (any, error) {
			msgReq, err := normalizeAnthropicRequest(upstream)
			if err != nil {
				return nil, err
			}
			prependCachedThinking(&msgReq, sess)
			return &msgReq, nil
		})

	_, err := provider.CreateCore(context.Background(), &format.CoreRequest{Model: "deepseek-v4-pro"})
	if err != nil {
		t.Fatalf("CreateCore() error = %v", err)
	}
	if len(client.got.Messages) != 1 {
		t.Fatalf("messages = %+v", client.got.Messages)
	}
	content := client.got.Messages[0].Content
	if len(content) != 2 {
		t.Fatalf("assistant content = %+v, want thinking + tool_use", content)
	}
	if content[0].Type != "thinking" {
		t.Fatalf("first assistant block = %+v, want thinking", content[0])
	}
	if content[1].Type != "tool_use" || content[1].ID != "call_view_image" {
		t.Fatalf("second assistant block = %+v, want original tool_use", content[1])
	}
}
