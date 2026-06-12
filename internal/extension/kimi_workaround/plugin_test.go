package kimi_workaround

import (
	"fmt"
	"strings"
	"testing"

	"moonbridge/internal/format"
)

// enabledPlugin creates a Plugin with EnabledForModel always returning true.
func enabledPlugin() *Plugin {
	return NewPlugin(func(_ string) bool { return true })
}

func TestRewriteMessages_NoToolCalls(t *testing.T) {
	p := NewPlugin()
	msgs := []format.CoreMessage{
		{Role: "user", Content: []format.CoreContentBlock{{Type: "text", Text: "hi"}}},
	}
	result := p.RewriteMessages(nil, msgs)
	if len(result) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result))
	}
}

func TestRewriteMessages_FirstRoundNoToolResults(t *testing.T) {
	p := NewPlugin()
	msgs := []format.CoreMessage{
		{Role: "user", Content: []format.CoreContentBlock{{Type: "text", Text: "hi"}}},
		{Role: "assistant", Content: []format.CoreContentBlock{
			{Type: "tool_use", ToolUseID: "call_1", ToolName: "exec_command"},
		}},
	}
	result := p.RewriteMessages(nil, msgs)
	if len(result) != 2 {
		t.Fatalf("expected 2 messages (no injection), got %d", len(result))
	}
}

func TestRewriteMessages_SingleRound(t *testing.T) {
	p := enabledPlugin()
	msgs := []format.CoreMessage{
		{Role: "user", Content: []format.CoreContentBlock{{Type: "text", Text: "hi"}}},
		{Role: "assistant", Content: []format.CoreContentBlock{
			{Type: "tool_use", ToolUseID: "call_1", ToolName: "exec_command"},
		}},
		{Role: "tool", Content: []format.CoreContentBlock{
			{Type: "tool_result", ToolUseID: "call_1", ToolResultContent: []format.CoreContentBlock{{Type: "text", Text: "result"}}},
		}},
	}
	result := p.RewriteMessages(nil, msgs)
	if len(result) != 3 {
		t.Fatalf("expected 3 messages (no new msg, appended to tool_result), got %d", len(result))
	}
	// Verify the prompt was appended to the tool_result's content
	lastBlock := result[2].Content[0]
	if lastBlock.Type != "tool_result" || len(lastBlock.ToolResultContent) != 2 {
		t.Fatalf("expected 2 blocks in tool_result (original + prompt), got %d: %+v", len(lastBlock.ToolResultContent), lastBlock.ToolResultContent)
	}
	if !strings.HasPrefix(lastBlock.ToolResultContent[1].Text, "[SystemReminder]") {
		t.Fatalf("expected SystemReminder in appended block, got: %s", lastBlock.ToolResultContent[1].Text)
	}
}

func TestRewriteMessages_MultipleRounds(t *testing.T) {
	p := enabledPlugin()
	msgs := buildToolConversation(3)
	result := p.RewriteMessages(nil, msgs)

	// Message count unchanged (appended to tool_result, not new msg)
	if len(result) != 7 {
		t.Fatalf("expected 7 messages (no new msg), got %d", len(result))
	}
	// Verify last tool_result has the prompt appended
	lastBlock := result[len(result)-1].Content[0]
	if lastBlock.Type != "tool_result" || len(lastBlock.ToolResultContent) != 2 {
		t.Fatalf("expected 2 blocks in last tool_result, got %d", len(lastBlock.ToolResultContent))
	}
}

func TestRewriteMessages_ApproachingLimit(t *testing.T) {
	p := enabledPlugin()
	p.maxRounds = 10
	p.margin = 0.8 // Threshold = 8
	msgs := buildToolConversation(8)
	result := p.RewriteMessages(nil, msgs)

	// 8 rounds = 17 messages. Prompts appended to tool_result content.
	if len(result) != 17 {
		t.Fatalf("expected 17 messages (appended, no new msg), got %d", len(result))
	}
	// Last tool_result should have 3 blocks: original + SystemReminder + approaching
	lastBlock := result[len(result)-1].Content[0]
	if lastBlock.Type != "tool_result" || len(lastBlock.ToolResultContent) != 3 {
		t.Fatalf("expected 3 blocks (original + 2 prompts), got %d: %+v", len(lastBlock.ToolResultContent), lastBlock.ToolResultContent)
	}
	if !strings.HasPrefix(lastBlock.ToolResultContent[1].Text, "[SystemReminder]") {
		t.Fatalf("expected SystemReminder at [1], got: %s", lastBlock.ToolResultContent[1].Text)
	}
	if lastBlock.ToolResultContent[2].Text != DefaultLimitPrompt {
		t.Fatalf("expected approaching prompt at [2], got: %s", lastBlock.ToolResultContent[2].Text)
	}
}

func TestRewriteMessages_AtLimit(t *testing.T) {
	p := enabledPlugin()
	p.maxRounds = 10
	p.margin = 0.8
	msgs := buildToolConversation(10)
	result := p.RewriteMessages(nil, msgs)

	// 10 rounds = 21 messages. At limit → tool_result REPLACED with error.
	if len(result) != 21 {
		t.Fatalf("expected 21 messages (replaced, no new msg), got %d", len(result))
	}
	// Last tool_result should have 1 block: replaced error message
	lastBlock := result[len(result)-1].Content[0]
	if lastBlock.Type != "tool_result" || len(lastBlock.ToolResultContent) != 1 {
		t.Fatalf("expected 1 block in replaced tool_result, got %d: %+v", len(lastBlock.ToolResultContent), lastBlock.ToolResultContent)
	}
	// Verify it contains the MAX_TOOL_ROUNDS error
	if !strings.Contains(lastBlock.ToolResultContent[0].Text, "MAX_TOOL_ROUNDS") {
		t.Fatalf("expected MAX_TOOL_ROUNDS error in replaced tool_result, got: %s", lastBlock.ToolResultContent[0].Text)
	}
}

func TestRewriteMessages_DisabledPlugin(t *testing.T) {
	p := NewPlugin()
	// Don't enable — isEnabled is nil and no config
	msgs := []format.CoreMessage{
		{Role: "user", Content: []format.CoreContentBlock{{Type: "text", Text: "hi"}}},
		{Role: "assistant", Content: []format.CoreContentBlock{
			{Type: "tool_use", ToolUseID: "call_1"},
		}},
		{Role: "tool", Content: []format.CoreContentBlock{
			{Type: "tool_result", ToolUseID: "call_1"},
		}},
	}
	result := p.RewriteMessages(nil, msgs)
	if len(result) != 3 {
		t.Fatalf("expected 3 messages (no injection, disabled), got %d", len(result))
	}
}

func TestRewriteMessages_RealUserMessageEnding(t *testing.T) {
	p := NewPlugin()
	// No tool results at the end — should not inject.
	msgs := []format.CoreMessage{
		{Role: "user", Content: []format.CoreContentBlock{{Type: "text", Text: "hi"}}},
		{Role: "assistant", Content: []format.CoreContentBlock{
			{Type: "tool_use", ToolUseID: "call_1"},
		}},
		{Role: "tool", Content: []format.CoreContentBlock{
			{Type: "tool_result", ToolUseID: "call_1"},
		}},
		{Role: "user", Content: []format.CoreContentBlock{{Type: "text", Text: "continue"}}},
	}
	result := p.RewriteMessages(nil, msgs)
	if len(result) != 4 {
		t.Fatalf("expected 4 messages (no injection, real user at end), got %d", len(result))
	}
}

func TestRewriteMessages_CustomConfig(t *testing.T) {
	p := enabledPlugin()
	maxRounds := 10
	margin := 0.5
	p.maxRounds = maxRounds
	p.margin = float64(margin)
	msgs := buildToolConversation(5) // 5 rounds > 10*0.5 = 5
	result := p.RewriteMessages(nil, msgs)

	// Message count unchanged. Tool_result gets 3 blocks.
	if len(result) != 11 {
		t.Fatalf("expected 11 messages (appended), got %d", len(result))
	}
	lastBlock := result[len(result)-1].Content[0]
	if lastBlock.Type != "tool_result" || len(lastBlock.ToolResultContent) != 3 {
		t.Fatalf("expected 3 blocks in last tool_result, got %d", len(lastBlock.ToolResultContent))
	}
}

func TestRewriteMessages_ToolUserMessage(t *testing.T) {
	p := enabledPlugin()
	msgs := []format.CoreMessage{
		{Role: "user", Content: []format.CoreContentBlock{{Type: "text", Text: "hi"}}},
		{Role: "assistant", Content: []format.CoreContentBlock{
			{Type: "tool_use", ToolUseID: "call_1", ToolName: "exec"},
		}},
		{Role: "user", Content: []format.CoreContentBlock{
			{Type: "tool_result", ToolUseID: "call_1", ToolResultContent: []format.CoreContentBlock{{Type: "text", Text: "ok"}}},
		}},
	}
	result := p.RewriteMessages(nil, msgs)
	if len(result) != 3 {
		t.Fatalf("expected 3 messages (appended to tool_result), got %d", len(result))
	}
	// Verify prompt appended to tool_result content
	lastBlock := result[2].Content[0]
	if lastBlock.Type != "tool_result" || len(lastBlock.ToolResultContent) != 2 {
		t.Fatalf("expected 2 blocks in tool_result, got %d", len(lastBlock.ToolResultContent))
	}
	if !strings.HasPrefix(lastBlock.ToolResultContent[1].Text, "[SystemReminder]") {
		t.Fatalf("expected SystemReminder appended, got: %s", lastBlock.ToolResultContent[1].Text)
	}
}

// Helpers

func buildToolConversation(rounds int) []format.CoreMessage {
	msgs := []format.CoreMessage{
		{Role: "user", Content: []format.CoreContentBlock{{Type: "text", Text: "hi"}}},
	}
	for i := 0; i < rounds; i++ {
		callID := fmt.Sprintf("call_%d", i)
		msgs = append(msgs, format.CoreMessage{
			Role: "assistant",
			Content: []format.CoreContentBlock{{
				Type: "tool_use", ToolUseID: callID, ToolName: "exec_command",
			}},
		})
		msgs = append(msgs, format.CoreMessage{
			Role: "tool",
			Content: []format.CoreContentBlock{{
				Type: "tool_result", ToolUseID: callID,
				ToolResultContent: []format.CoreContentBlock{{Type: "text", Text: "r"}},
			}},
		})
	}
	return msgs
}
