package bridge_test

import (
	"encoding/json"
	"testing"

	"moonbridge/internal/foundation/openai"
	"moonbridge/internal/protocol/anthropic"
)

func TestFromAnthropicKeepsUnregisteredToolUseAsFunctionCall(t *testing.T) {
	response := anthropic.MessageResponse{
		ID:         "msg_123",
		Type:       "message",
		Role:       "assistant",
		StopReason: "tool_use",
		Content: []anthropic.ContentBlock{{
			Type:  "tool_use",
			ID:    "tool_lookup",
			Name:  "lookup",
			Input: json.RawMessage(`{"id":"42"}`),
		}},
	}

	converted := testBridge().FromAnthropicWithContext(response, "gpt-test", testBridge().ConversionContext(openai.ResponsesRequest{
		Model: "gpt-test",
		Tools: []openai.Tool{{Type: "custom", Name: "rewrite_buffer"}},
	}))
	if len(converted.Output) != 1 {
		t.Fatalf("output = %+v", converted.Output)
	}
	item := converted.Output[0]
	if item.Type != "function_call" || item.Name != "lookup" || item.Arguments != `{"id":"42"}` {
		t.Fatalf("function call = %+v", item)
	}
}

func TestFromAnthropicSplitsNamespacedFunctionToolForCodex(t *testing.T) {
	request := openai.ResponsesRequest{
		Model: "gpt-test",
		Tools: []openai.Tool{{
			Type: "namespace",
			Name: "mcp__deepwiki__",
			Tools: []openai.Tool{{
				Type: "function",
				Name: "read_wiki_structure",
			}},
		}},
	}
	response := anthropic.MessageResponse{
		ID:         "msg_123",
		Type:       "message",
		Role:       "assistant",
		StopReason: "tool_use",
		Content: []anthropic.ContentBlock{{
			Type:  "tool_use",
			ID:    "tool_deepwiki",
			Name:  "mcp__deepwiki__read_wiki_structure",
			Input: json.RawMessage(`{"repoName":"openai/codex"}`),
		}},
	}

	bridgeUnderTest := testBridge()
	converted := bridgeUnderTest.FromAnthropicWithContext(response, "gpt-test", bridgeUnderTest.ConversionContext(request))
	if len(converted.Output) != 1 {
		t.Fatalf("output = %+v", converted.Output)
	}
	item := converted.Output[0]
	if item.Type != "function_call" || item.Namespace != "mcp__deepwiki__" || item.Name != "read_wiki_structure" {
		t.Fatalf("namespaced function call = %+v", item)
	}
}

func TestFromAnthropicMapsLocalShellToolUseForCodex(t *testing.T) {
	response := anthropic.MessageResponse{
		ID:         "msg_123",
		Type:       "message",
		Role:       "assistant",
		StopReason: "tool_use",
		Content: []anthropic.ContentBlock{
			{
				Type:  "tool_use",
				ID:    "toolu_shell",
				Name:  "local_shell",
				Input: json.RawMessage(`{"command":["bash","-lc","pwd"],"working_directory":"/tmp","timeout_ms":1000}`),
			},
		},
	}

	converted := testBridge().FromAnthropic(response, "gpt-test")
	if len(converted.Output) != 1 {
		t.Fatalf("output = %+v", converted.Output)
	}
	item := converted.Output[0]
	if item.Type != "local_shell_call" {
		t.Fatalf("item type = %q", item.Type)
	}
	if item.CallID != "toolu_shell" {
		t.Fatalf("call_id = %q", item.CallID)
	}
	if item.Action == nil || len(item.Action.Command) != 3 || item.Action.Command[2] != "pwd" {
		t.Fatalf("action = %+v", item.Action)
	}
}

func TestFromAnthropicMapsWebSearchServerToolUseForCodex(t *testing.T) {
	response := anthropic.MessageResponse{
		ID:         "msg_123",
		Type:       "message",
		Role:       "assistant",
		StopReason: "end_turn",
		Content: []anthropic.ContentBlock{
			{
				Type:  "server_tool_use",
				ID:    "srvtoolu_123",
				Name:  "web_search",
				Input: json.RawMessage(`{"type":"search","query":"Kimi K2.6","queries":["Kimi K2.6","Moonshot K2.6"]}`),
			},
			{Type: "web_search_tool_result", ToolUseID: "srvtoolu_123", Content: []any{
				map[string]any{"type": "web_search_result", "url": "https://example.test", "title": "Example"},
			}},
			{Type: "text", Text: "Found results."},
		},
	}

	converted := testBridge().FromAnthropic(response, "gpt-test")
	if len(converted.Output) != 2 {
		t.Fatalf("output = %+v", converted.Output)
	}
	item := converted.Output[0]
	if item.Type != "web_search_call" || item.ID != "ws_srvtoolu_123" || item.Status != "completed" {
		t.Fatalf("web search item = %+v", item)
	}
	if item.Action == nil || item.Action.Type != "search" || item.Action.Query != "Kimi K2.6" || len(item.Action.Queries) != 2 {
		t.Fatalf("web search action = %+v", item.Action)
	}
	if converted.Output[1].Type != "message" || converted.OutputText != "Found results." {
		t.Fatalf("message output = %+v text=%q", converted.Output[1], converted.OutputText)
	}
}
