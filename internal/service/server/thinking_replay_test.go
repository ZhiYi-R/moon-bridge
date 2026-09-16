package server

import (
	"encoding/json"
	"testing"

	deepseekv4 "moonbridge/internal/extension/deepseek_v4"
	"moonbridge/internal/extension/plugin"
	"moonbridge/internal/format"
	"moonbridge/internal/protocol/anthropic"
	"moonbridge/internal/session"
)

func newThinkingSession(t *testing.T) *session.Session {
	t.Helper()
	registry := plugin.NewRegistry(nil)
	registry.Register(deepseekv4.NewPlugin(func(string) bool { return true }))
	if err := registry.InitAll(nil); err != nil {
		t.Fatalf("InitAll() error = %v", err)
	}
	sess := session.New()
	sess.InitExtensions(registry.NewSessionData())
	return sess
}

// DeepSeek rejects a thinking-enabled conversation whose assistant turn carries
// no thinking block, including turns that only contain text. Every assistant
// message must therefore leave the replay step with one.
func TestPrependCachedThinkingCoversPlainAssistantMessages(t *testing.T) {
	sess := newThinkingSession(t)

	req := &anthropic.MessageRequest{
		Messages: []anthropic.Message{
			{Role: "user", Content: []anthropic.ContentBlock{{Type: "text", Text: "question"}}},
			{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "Outcome: allow."}}},
			{Role: "user", Content: []anthropic.ContentBlock{{Type: "text", Text: "next"}}},
		},
	}

	prependCachedThinking(req, sess)

	if !hasThinkingBlock(req.Messages[1].Content) {
		t.Fatalf("assistant text turn left without thinking: %+v", req.Messages[1].Content)
	}
	if hasThinkingBlock(req.Messages[0].Content) || hasThinkingBlock(req.Messages[2].Content) {
		t.Fatalf("non-assistant turns must not gain thinking: %+v", req.Messages)
	}
}

func TestPrependCachedThinkingKeepsAnExistingThinkingBlock(t *testing.T) {
	sess := newThinkingSession(t)

	req := &anthropic.MessageRequest{
		Messages: []anthropic.Message{
			{
				Role: "assistant",
				Content: []anthropic.ContentBlock{
					{Type: "thinking", Thinking: "original reasoning", Signature: "sig-1"},
					{Type: "text", Text: "answer"},
				},
			},
		},
	}

	prependCachedThinking(req, sess)

	content := req.Messages[0].Content
	if len(content) != 2 || content[0].Thinking != "original reasoning" {
		t.Fatalf("existing thinking block was disturbed: %+v", content)
	}
}

// A tool-call turn replays the thinking cached for its tool call, not the empty
// boundary block used as the last resort.
func TestPrependCachedThinkingPrefersToolCallCache(t *testing.T) {
	registry := plugin.NewRegistry(nil)
	registry.Register(deepseekv4.NewPlugin(func(string) bool { return true }))
	if err := registry.InitAll(nil); err != nil {
		t.Fatalf("InitAll() error = %v", err)
	}
	sess := session.New()
	sess.InitExtensions(registry.NewSessionData())

	rememberAdapterResponseContent(registry, sess, "deepseek-v4-flash", &format.CoreResponse{
		Messages: []format.CoreMessage{{
			Role: "assistant",
			Content: []format.CoreContentBlock{
				{Type: "reasoning", ReasoningText: "trace reasoning", ReasoningSignature: "sig-trace"},
				{
					Type:      "tool_use",
					ToolUseID: "call-1",
					ToolName:  "exec_command",
					ToolInput: json.RawMessage(`{"cmd":"pwd"}`),
				},
			},
		}},
	})

	req := &anthropic.MessageRequest{
		Messages: []anthropic.Message{{
			Role: "assistant",
			Content: []anthropic.ContentBlock{
				{Type: "tool_use", ID: "call-1", Name: "exec_command", Input: json.RawMessage(`{"cmd":"pwd"}`)},
			},
		}},
	}

	prependCachedThinking(req, sess)

	content := req.Messages[0].Content
	if len(content) < 2 || content[0].Type != "thinking" {
		t.Fatalf("tool call turn did not gain a thinking block: %+v", content)
	}
	if content[0].Thinking != "trace reasoning" {
		t.Fatalf("cached thinking not replayed, got %q", content[0].Thinking)
	}
}
