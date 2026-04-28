package bridge_test

import (
	"encoding/json"
	"strings"
	"testing"

	"moonbridge/internal/extension/codex"
	deepseekv4 "moonbridge/internal/extension/deepseek_v4"
	"moonbridge/internal/foundation/config"
	"moonbridge/internal/foundation/openai"
	"moonbridge/internal/protocol/anthropic"
	"moonbridge/internal/protocol/bridge"
)

func TestConvertStreamEventsConvertsTextLifecycle(t *testing.T) {
	events := []anthropic.StreamEvent{
		{Type: "message_start", Message: &anthropic.MessageResponse{ID: "msg_1", Type: "message", Role: "assistant"}},
		{Type: "content_block_start", Index: 0, ContentBlock: &anthropic.ContentBlock{Type: "text"}},
		{Type: "content_block_delta", Index: 0, Delta: anthropic.StreamDelta{Type: "text_delta", Text: "Hi"}},
		{Type: "content_block_stop", Index: 0},
		{Type: "message_delta", Delta: anthropic.StreamDelta{StopReason: "end_turn"}, Usage: &anthropic.Usage{OutputTokens: 1}},
		{Type: "message_stop"},
	}

	converted := testBridge().ConvertStreamEvents(events, "gpt-test")
	names := make([]string, 0, len(converted))
	for _, event := range converted {
		names = append(names, event.Event)
	}

	want := []string{
		"response.created",
		"response.in_progress",
		"response.output_item.added",
		"response.content_part.added",
		"response.output_text.delta",
		"response.output_text.done",
		"response.content_part.done",
		"response.output_item.done",
		"response.completed",
	}
	if len(names) != len(want) {
		t.Fatalf("events = %v", names)
	}
	for index, wantName := range want {
		if names[index] != wantName {
			t.Fatalf("event %d = %q, want %q; all=%v", index, names[index], wantName, names)
		}
	}

	delta := converted[4].Data.(openai.OutputTextDeltaEvent)
	if delta.Delta != "Hi" {
		t.Fatalf("delta = %+v", delta)
	}
}

func TestConvertStreamEventsConvertsToolArguments(t *testing.T) {
	rawInput := json.RawMessage(`{}`)
	events := []anthropic.StreamEvent{
		{Type: "message_start", Message: &anthropic.MessageResponse{ID: "msg_1", Type: "message", Role: "assistant"}},
		{Type: "content_block_start", Index: 0, ContentBlock: &anthropic.ContentBlock{Type: "tool_use", ID: "toolu_1", Name: "lookup", Input: rawInput}},
		{Type: "content_block_delta", Index: 0, Delta: anthropic.StreamDelta{Type: "input_json_delta", PartialJSON: `{"id"`}},
		{Type: "content_block_delta", Index: 0, Delta: anthropic.StreamDelta{Type: "input_json_delta", PartialJSON: `:"42"}`}},
		{Type: "content_block_stop", Index: 0},
		{Type: "message_delta", Delta: anthropic.StreamDelta{StopReason: "tool_use"}},
		{Type: "message_stop"},
	}

	converted := testBridge().ConvertStreamEvents(events, "gpt-test")
	var done openai.FunctionCallArgumentsDoneEvent
	for _, event := range converted {
		if event.Event == "response.function_call_arguments.done" {
			done = event.Data.(openai.FunctionCallArgumentsDoneEvent)
		}
	}
	if done.Arguments != `{"id":"42"}` {
		t.Fatalf("arguments = %q", done.Arguments)
	}

	completed := streamLifecycleResponse(t, converted, "response.completed")
	if len(completed.Output) != 1 {
		t.Fatalf("completed output = %+v", completed.Output)
	}
	item := completed.Output[0]
	if item.Type != "function_call" || item.Status != "completed" || item.Name != "lookup" || item.CallID != "toolu_1" || item.Arguments != `{"id":"42"}` {
		t.Fatalf("completed function item = %+v", item)
	}
}

func TestConvertStreamEventsTreatsReusedToolUseIndexAsNewBlock(t *testing.T) {
	events := []anthropic.StreamEvent{
		{Type: "message_start", Message: &anthropic.MessageResponse{ID: "msg_1", Type: "message", Role: "assistant"}},
		{Type: "content_block_start", Index: 1, ContentBlock: &anthropic.ContentBlock{Type: "tool_use", ID: "toolu_1", Name: "lookup", Input: json.RawMessage(`{}`)}},
		{Type: "content_block_delta", Index: 1, Delta: anthropic.StreamDelta{Type: "input_json_delta", PartialJSON: `{"query":"A"}`}},
		{Type: "content_block_stop", Index: 1},
		{Type: "content_block_start", Index: 1, ContentBlock: &anthropic.ContentBlock{Type: "tool_use", ID: "toolu_2", Name: "lookup", Input: json.RawMessage(`{}`)}},
		{Type: "content_block_delta", Index: 1, Delta: anthropic.StreamDelta{Type: "input_json_delta", PartialJSON: `{"query":"B"}`}},
		{Type: "content_block_stop", Index: 1},
		{Type: "message_delta", Delta: anthropic.StreamDelta{StopReason: "tool_use"}},
		{Type: "message_stop"},
	}

	converted := testBridge().ConvertStreamEvents(events, "gpt-test")
	var doneEvents []openai.FunctionCallArgumentsDoneEvent
	for _, event := range converted {
		if event.Event != "response.function_call_arguments.done" {
			continue
		}
		done := event.Data.(openai.FunctionCallArgumentsDoneEvent)
		if strings.Contains(done.Arguments, "}{") {
			t.Fatalf("arguments were concatenated: %+v", done)
		}
		doneEvents = append(doneEvents, done)
	}
	if len(doneEvents) != 2 {
		t.Fatalf("done argument events = %+v", doneEvents)
	}
	if doneEvents[0].OutputIndex != 0 || doneEvents[0].Arguments != `{"query":"A"}` {
		t.Fatalf("first done event = %+v", doneEvents[0])
	}
	if doneEvents[1].OutputIndex != 1 || doneEvents[1].Arguments != `{"query":"B"}` {
		t.Fatalf("second done event = %+v", doneEvents[1])
	}

	completed := streamLifecycleResponse(t, converted, "response.completed")
	if len(completed.Output) != 2 {
		t.Fatalf("completed output = %+v", completed.Output)
	}
	if completed.Output[0].CallID != "toolu_1" || completed.Output[0].Arguments != `{"query":"A"}` {
		t.Fatalf("first function item = %+v", completed.Output[0])
	}
	if completed.Output[1].CallID != "toolu_2" || completed.Output[1].Arguments != `{"query":"B"}` {
		t.Fatalf("second function item = %+v", completed.Output[1])
	}
}

func TestConvertStreamEventsSplitsNamespacedFunctionTool(t *testing.T) {
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
	events := []anthropic.StreamEvent{
		{Type: "message_start", Message: &anthropic.MessageResponse{ID: "msg_1", Type: "message", Role: "assistant"}},
		{Type: "content_block_start", Index: 0, ContentBlock: &anthropic.ContentBlock{
			Type:  "tool_use",
			ID:    "tool_deepwiki",
			Name:  "mcp__deepwiki__read_wiki_structure",
			Input: json.RawMessage(`{}`),
		}},
		{Type: "content_block_delta", Index: 0, Delta: anthropic.StreamDelta{Type: "input_json_delta", PartialJSON: `{"repoName":"openai/codex"}`}},
		{Type: "content_block_stop", Index: 0},
		{Type: "message_delta", Delta: anthropic.StreamDelta{StopReason: "tool_use"}},
		{Type: "message_stop"},
	}

	bridgeUnderTest := testBridge()
	converted := bridgeUnderTest.ConvertStreamEventsWithContext(events, "gpt-test", bridgeUnderTest.ConversionContext(request), nil)
	completed := streamLifecycleResponse(t, converted, "response.completed")
	if len(completed.Output) != 1 {
		t.Fatalf("completed output = %+v", completed.Output)
	}
	item := completed.Output[0]
	if item.Type != "function_call" || item.Namespace != "mcp__deepwiki__" || item.Name != "read_wiki_structure" {
		t.Fatalf("namespaced function item = %+v", item)
	}
}

func TestConvertStreamEventsIgnoresEmptyTextDeltasBeforeToolCall(t *testing.T) {
	events := []anthropic.StreamEvent{
		{Type: "message_start", Message: &anthropic.MessageResponse{ID: "msg_1", Type: "message", Role: "assistant"}},
		{Type: "content_block_start", Index: 1, ContentBlock: &anthropic.ContentBlock{Type: "text"}},
		{Type: "content_block_delta", Index: 1, Delta: anthropic.StreamDelta{Type: "text_delta"}},
		{Type: "content_block_start", Index: 2, ContentBlock: &anthropic.ContentBlock{Type: "tool_use", ID: "call_ls", Name: "exec_command", Input: json.RawMessage(`{}`)}},
		{Type: "content_block_delta", Index: 2, Delta: anthropic.StreamDelta{Type: "input_json_delta", PartialJSON: `{"cmd":"ls trace"}`}},
		{Type: "content_block_delta", Index: 1, Delta: anthropic.StreamDelta{Type: "text_delta"}},
		{Type: "content_block_stop", Index: 1},
		{Type: "content_block_stop", Index: 2},
		{Type: "message_delta", Delta: anthropic.StreamDelta{StopReason: "tool_use"}},
		{Type: "message_stop"},
	}

	converted := testBridge().ConvertStreamEvents(events, "gpt-test")
	for _, event := range converted {
		if event.Event != "response.output_item.done" {
			continue
		}
		item := event.Data.(openai.OutputItemEvent).Item
		if item.Type == "message" {
			t.Fatalf("unexpected empty message output item = %+v", item)
		}
	}
	completed := streamLifecycleResponse(t, converted, "response.completed")
	if len(completed.Output) != 1 {
		t.Fatalf("completed output = %+v", completed.Output)
	}
	if completed.Output[0].Type != "function_call" || completed.Output[0].Name != "exec_command" {
		t.Fatalf("completed output = %+v", completed.Output)
	}
}

func TestConvertStreamEventsMapsRequestCustomToolToCustomToolCall(t *testing.T) {
	input := "replace this buffer\nwith this text\n"
	request := openai.ResponsesRequest{
		Model: "gpt-test",
		Tools: []openai.Tool{{Type: "custom", Name: "rewrite_buffer"}},
	}
	events := []anthropic.StreamEvent{
		{Type: "message_start", Message: &anthropic.MessageResponse{ID: "msg_1", Type: "message", Role: "assistant"}},
		{Type: "content_block_start", Index: 0, ContentBlock: &anthropic.ContentBlock{
			Type: "tool_use",
			ID:   "tool_rewrite",
			Name: "rewrite_buffer",
		}},
		{Type: "content_block_delta", Index: 0, Delta: anthropic.StreamDelta{Type: "input_json_delta", PartialJSON: `{"input":`}},
		{Type: "content_block_delta", Index: 0, Delta: anthropic.StreamDelta{Type: "input_json_delta", PartialJSON: string(mustMarshalRaw(t, input)) + `}`}},
		{Type: "content_block_stop", Index: 0},
		{Type: "message_delta", Delta: anthropic.StreamDelta{StopReason: "tool_use"}},
		{Type: "message_stop"},
	}

	bridgeUnderTest := testBridge()
	converted := bridgeUnderTest.ConvertStreamEventsWithContext(events, "gpt-test", bridgeUnderTest.ConversionContext(request), nil)
	var customDelta openai.CustomToolCallInputDeltaEvent
	for _, event := range converted {
		if event.Event == "response.function_call_arguments.delta" || event.Event == "response.function_call_arguments.done" {
			t.Fatalf("unexpected function-call argument event for custom tool: %+v", event)
		}
		if event.Event == "response.custom_tool_call_input.delta" {
			customDelta = event.Data.(openai.CustomToolCallInputDeltaEvent)
		}
	}
	if customDelta.Delta != input || customDelta.CallID != "tool_rewrite" {
		t.Fatalf("custom delta = %+v", customDelta)
	}

	completed := streamLifecycleResponse(t, converted, "response.completed")
	if len(completed.Output) != 1 {
		t.Fatalf("completed output = %+v", completed.Output)
	}
	item := completed.Output[0]
	if item.Type != "custom_tool_call" || item.CallID != "tool_rewrite" || item.Name != "rewrite_buffer" || item.Input != input {
		t.Fatalf("completed custom item = %+v", item)
	}
}

func TestConvertStreamEventsNormalizesApplyPatchGrammarTerminator(t *testing.T) {
	input := "*** Begin Patch\n*** Add File: docs/api.md\n+# API\n+content\n+*** End Patch"
	request := openai.ResponsesRequest{
		Model: "gpt-test",
		Tools: []openai.Tool{{
			Type: "custom",
			Name: "patcher",
			Format: map[string]any{
				"type":       "grammar",
				"syntax":     "lark",
				"definition": "begin_patch: \"*** Begin Patch\"\nend_patch: \"*** End Patch\"\nadd_hunk: \"*** Add File: \"\n",
			},
		}},
	}
	events := []anthropic.StreamEvent{
		{Type: "message_start", Message: &anthropic.MessageResponse{ID: "msg_1", Type: "message", Role: "assistant"}},
		{Type: "content_block_start", Index: 0, ContentBlock: &anthropic.ContentBlock{
			Type: "tool_use",
			ID:   "tool_patch",
			Name: "patcher",
		}},
		{Type: "content_block_delta", Index: 0, Delta: anthropic.StreamDelta{Type: "input_json_delta", PartialJSON: `{"input":`}},
		{Type: "content_block_delta", Index: 0, Delta: anthropic.StreamDelta{Type: "input_json_delta", PartialJSON: string(mustMarshalRaw(t, input)) + `}`}},
		{Type: "content_block_stop", Index: 0},
		{Type: "message_delta", Delta: anthropic.StreamDelta{StopReason: "tool_use"}},
		{Type: "message_stop"},
	}

	bridgeUnderTest := testBridge()
	converted := bridgeUnderTest.ConvertStreamEventsWithContext(events, "gpt-test", bridgeUnderTest.ConversionContext(request), nil)
	var delta openai.CustomToolCallInputDeltaEvent
	for _, event := range converted {
		if event.Event == "response.custom_tool_call_input.delta" {
			delta = event.Data.(openai.CustomToolCallInputDeltaEvent)
		}
	}
	if strings.Contains(delta.Delta, "+*** End Patch") || !strings.HasSuffix(delta.Delta, "*** End Patch") {
		t.Fatalf("custom delta = %q", delta.Delta)
	}
	completed := streamLifecycleResponse(t, converted, "response.completed")
	if strings.Contains(completed.Output[0].Input, "+*** End Patch") || !strings.HasSuffix(completed.Output[0].Input, "*** End Patch") {
		t.Fatalf("custom item = %q", completed.Output[0].Input)
	}
}

func TestConvertStreamEventsBuildsApplyPatchGrammarFromProxyOperations(t *testing.T) {
	request := openai.ResponsesRequest{
		Model: "gpt-test",
		Tools: []openai.Tool{{
			Type:   "custom",
			Name:   "apply_patch",
			Format: map[string]any{"type": "grammar", "syntax": "lark", "definition": applyPatchGrammarForTest()},
		}},
	}
	input := map[string]any{"operations": []map[string]any{{
		"type":    "add_file",
		"path":    "docs/api.md",
		"content": "# API\ncontent\n",
	}}}
	events := []anthropic.StreamEvent{
		{Type: "message_start", Message: &anthropic.MessageResponse{ID: "msg_1", Type: "message", Role: "assistant"}},
		{Type: "content_block_start", Index: 0, ContentBlock: &anthropic.ContentBlock{
			Type: "tool_use",
			ID:   "tool_patch",
			Name: "apply_patch",
		}},
		{Type: "content_block_delta", Index: 0, Delta: anthropic.StreamDelta{Type: "input_json_delta", PartialJSON: string(mustMarshalRaw(t, input))}},
		{Type: "content_block_stop", Index: 0},
		{Type: "message_delta", Delta: anthropic.StreamDelta{StopReason: "tool_use"}},
		{Type: "message_stop"},
	}

	bridgeUnderTest := testBridge()
	converted := bridgeUnderTest.ConvertStreamEventsWithContext(events, "gpt-test", bridgeUnderTest.ConversionContext(request), nil)
	completed := streamLifecycleResponse(t, converted, "response.completed")
	want := "*** Begin Patch\n*** Add File: docs/api.md\n+# API\n+content\n*** End Patch"
	if completed.Output[0].Input != want {
		t.Fatalf("patch = %q, want %q", completed.Output[0].Input, want)
	}
}

func TestConvertStreamEventsBuildsApplyPatchGrammarFromSplitAddFileTool(t *testing.T) {
	request := openai.ResponsesRequest{
		Model: "gpt-test",
		Tools: []openai.Tool{{
			Type:   "custom",
			Name:   "apply_patch",
			Format: map[string]any{"type": "grammar", "syntax": "lark", "definition": applyPatchGrammarForTest()},
		}},
	}
	input := map[string]any{
		"path":    "docs/api.md",
		"content": "# API\ncontent\n",
	}
	events := []anthropic.StreamEvent{
		{Type: "message_start", Message: &anthropic.MessageResponse{ID: "msg_1", Type: "message", Role: "assistant"}},
		{Type: "content_block_start", Index: 0, ContentBlock: &anthropic.ContentBlock{
			Type: "tool_use",
			ID:   "tool_patch",
			Name: "apply_patch_add_file",
		}},
		{Type: "content_block_delta", Index: 0, Delta: anthropic.StreamDelta{Type: "input_json_delta", PartialJSON: string(mustMarshalRaw(t, input))}},
		{Type: "content_block_stop", Index: 0},
		{Type: "message_delta", Delta: anthropic.StreamDelta{StopReason: "tool_use"}},
		{Type: "message_stop"},
	}

	bridgeUnderTest := testBridge()
	converted := bridgeUnderTest.ConvertStreamEventsWithContext(events, "gpt-test", bridgeUnderTest.ConversionContext(request), nil)
	completed := streamLifecycleResponse(t, converted, "response.completed")
	if completed.Output[0].Name != "apply_patch" {
		t.Fatalf("output tool name = %q", completed.Output[0].Name)
	}
	want := "*** Begin Patch\n*** Add File: docs/api.md\n+# API\n+content\n*** End Patch"
	if completed.Output[0].Input != want {
		t.Fatalf("patch = %q, want %q", completed.Output[0].Input, want)
	}
}

func TestConvertStreamEventsBuildsApplyPatchReplacementFromUpdateContent(t *testing.T) {
	request := openai.ResponsesRequest{
		Model: "gpt-test",
		Tools: []openai.Tool{{
			Type:   "custom",
			Name:   "apply_patch",
			Format: map[string]any{"type": "grammar", "syntax": "lark", "definition": applyPatchGrammarForTest()},
		}},
	}
	input := map[string]any{"operations": []map[string]any{{
		"type":    "update_file",
		"path":    "internal/app/app.go",
		"content": "package app\n\nconst Name = \"Moon Bridge\"\n",
	}}}
	events := []anthropic.StreamEvent{
		{Type: "message_start", Message: &anthropic.MessageResponse{ID: "msg_1", Type: "message", Role: "assistant"}},
		{Type: "content_block_start", Index: 0, ContentBlock: &anthropic.ContentBlock{
			Type: "tool_use",
			ID:   "tool_patch",
			Name: "apply_patch",
		}},
		{Type: "content_block_delta", Index: 0, Delta: anthropic.StreamDelta{Type: "input_json_delta", PartialJSON: string(mustMarshalRaw(t, input))}},
		{Type: "content_block_stop", Index: 0},
		{Type: "message_delta", Delta: anthropic.StreamDelta{StopReason: "tool_use"}},
		{Type: "message_stop"},
	}

	bridgeUnderTest := testBridge()
	converted := bridgeUnderTest.ConvertStreamEventsWithContext(events, "gpt-test", bridgeUnderTest.ConversionContext(request), nil)
	completed := streamLifecycleResponse(t, converted, "response.completed")
	want := "*** Begin Patch\n*** Delete File: internal/app/app.go\n*** Add File: internal/app/app.go\n+package app\n+\n+const Name = \"Moon Bridge\"\n*** End Patch"
	if completed.Output[0].Input != want {
		t.Fatalf("patch = %q, want %q", completed.Output[0].Input, want)
	}
}

func TestConvertStreamEventsConvertsWebSearchServerToolUse(t *testing.T) {
	events := []anthropic.StreamEvent{
		{Type: "message_start", Message: &anthropic.MessageResponse{ID: "msg_1", Type: "message", Role: "assistant"}},
		{Type: "content_block_start", Index: 0, ContentBlock: &anthropic.ContentBlock{
			Type:  "server_tool_use",
			ID:    "srvtoolu_1",
			Name:  "web_search",
			Input: json.RawMessage(`{"type":"search","query":"Kimi K2.6"}`),
		}},
		{Type: "content_block_stop", Index: 0},
		{Type: "content_block_start", Index: 1, ContentBlock: &anthropic.ContentBlock{
			Type:      "web_search_tool_result",
			ToolUseID: "srvtoolu_1",
			Content:   []any{map[string]any{"type": "web_search_result", "url": "https://example.test"}},
		}},
		{Type: "content_block_stop", Index: 1},
		{Type: "message_delta", Delta: anthropic.StreamDelta{StopReason: "end_turn"}},
		{Type: "message_stop"},
	}

	converted := testBridge().ConvertStreamEvents(events, "gpt-test")
	completed := streamLifecycleResponse(t, converted, "response.completed")
	if len(completed.Output) != 1 {
		t.Fatalf("completed output = %+v", completed.Output)
	}
	item := completed.Output[0]
	if item.Type != "web_search_call" || item.ID != "ws_srvtoolu_1" || item.Status != "completed" {
		t.Fatalf("web search item = %+v", item)
	}
	if item.Action == nil || item.Action.Type != "search" || item.Action.Query != "Kimi K2.6" {
		t.Fatalf("web search action = %+v", item.Action)
	}
}

func TestConvertStreamEventsKeepsWebSearchInputDeltaAsAction(t *testing.T) {
	events := []anthropic.StreamEvent{
		{Type: "message_start", Message: &anthropic.MessageResponse{ID: "msg_1", Type: "message", Role: "assistant"}},
		{Type: "content_block_start", Index: 0, ContentBlock: &anthropic.ContentBlock{
			Type: "server_tool_use",
			ID:   "srvtoolu_1",
			Name: "web_search",
		}},
		{Type: "content_block_delta", Index: 0, Delta: anthropic.StreamDelta{Type: "input_json_delta", PartialJSON: `{"query":"Kimi`}},
		{Type: "content_block_delta", Index: 0, Delta: anthropic.StreamDelta{Type: "input_json_delta", PartialJSON: ` K2.6"}`}},
		{Type: "content_block_stop", Index: 0},
		{Type: "message_delta", Delta: anthropic.StreamDelta{StopReason: "end_turn"}},
		{Type: "message_stop"},
	}

	converted := testBridge().ConvertStreamEvents(events, "gpt-test")
	for _, event := range converted {
		if event.Event == "response.function_call_arguments.delta" || event.Event == "response.function_call_arguments.done" {
			t.Fatalf("unexpected function argument event for web_search: %+v", event)
		}
	}
	completed := streamLifecycleResponse(t, converted, "response.completed")
	if len(completed.Output) != 1 {
		t.Fatalf("completed output = %+v", completed.Output)
	}
	item := completed.Output[0]
	if item.Type != "web_search_call" || item.Status != "completed" {
		t.Fatalf("web search item = %+v", item)
	}
	if item.Action == nil || item.Action.Query != "Kimi K2.6" {
		t.Fatalf("web search action = %+v", item.Action)
	}
}

func TestConvertStreamEventsSkipsEmptyWebSearchServerToolUse(t *testing.T) {
	events := []anthropic.StreamEvent{
		{Type: "message_start", Message: &anthropic.MessageResponse{ID: "msg_1", Type: "message", Role: "assistant"}},
		{Type: "content_block_start", Index: 0, ContentBlock: &anthropic.ContentBlock{Type: "text"}},
		{Type: "content_block_delta", Index: 0, Delta: anthropic.StreamDelta{Type: "text_delta", Text: "Search results for query: "}},
		{Type: "content_block_stop", Index: 0},
		{Type: "content_block_start", Index: 1, ContentBlock: &anthropic.ContentBlock{
			Type: "server_tool_use",
			Name: "web_search",
		}},
		{Type: "content_block_stop", Index: 1},
		{Type: "content_block_start", Index: 2, ContentBlock: &anthropic.ContentBlock{
			Type:    "web_search_tool_result",
			Content: []any{},
		}},
		{Type: "content_block_stop", Index: 2},
		{Type: "content_block_start", Index: 3, ContentBlock: &anthropic.ContentBlock{
			Type:  "tool_use",
			ID:    "tool_1",
			Name:  "list_mcp_resources",
			Input: json.RawMessage(`{"server":"deepwiki"}`),
		}},
		{Type: "content_block_delta", Index: 3, Delta: anthropic.StreamDelta{Type: "input_json_delta", PartialJSON: `{"server":"deepwiki"}`}},
		{Type: "content_block_stop", Index: 3},
		{Type: "message_delta", Delta: anthropic.StreamDelta{StopReason: "tool_use"}},
		{Type: "message_stop"},
	}

	converted := testBridge().ConvertStreamEvents(events, "gpt-test")
	for _, event := range converted {
		if event.Event == "response.output_item.done" {
			item := event.Data.(openai.OutputItemEvent).Item
			if item.Type == "web_search_call" {
				t.Fatalf("unexpected web_search_call item = %+v", item)
			}
		}
	}
	completed := streamLifecycleResponse(t, converted, "response.completed")
	if len(completed.Output) != 1 {
		t.Fatalf("completed output = %+v", completed.Output)
	}
	if completed.Output[0].Type != "function_call" || completed.Output[0].Name != "list_mcp_resources" {
		t.Fatalf("completed output = %+v", completed.Output)
	}
}

func TestConvertStreamEventsCompactsIgnoredWebSearchResultIndexes(t *testing.T) {
	events := []anthropic.StreamEvent{
		{Type: "message_start", Message: &anthropic.MessageResponse{ID: "msg_1", Type: "message", Role: "assistant"}},
		{Type: "content_block_start", Index: 0, ContentBlock: &anthropic.ContentBlock{
			Type: "server_tool_use",
			ID:   "srvtoolu_1",
			Name: "web_search",
		}},
		{Type: "content_block_delta", Index: 0, Delta: anthropic.StreamDelta{Type: "input_json_delta", PartialJSON: `{"query":"Kimi K2.6"}`}},
		{Type: "content_block_stop", Index: 0},
		{Type: "content_block_start", Index: 1, ContentBlock: &anthropic.ContentBlock{
			Type:      "web_search_tool_result",
			ToolUseID: "srvtoolu_1",
			Content:   []any{map[string]any{"type": "web_search_result", "url": "https://example.test"}},
		}},
		{Type: "content_block_stop", Index: 1},
		{Type: "content_block_start", Index: 2, ContentBlock: &anthropic.ContentBlock{Type: "text"}},
		{Type: "content_block_delta", Index: 2, Delta: anthropic.StreamDelta{Type: "text_delta", Text: "Found results."}},
		{Type: "content_block_stop", Index: 2},
		{Type: "message_delta", Delta: anthropic.StreamDelta{StopReason: "end_turn"}},
		{Type: "message_stop"},
	}

	converted := testBridge().ConvertStreamEvents(events, "gpt-test")
	completed := streamLifecycleResponse(t, converted, "response.completed")
	if len(completed.Output) != 2 {
		t.Fatalf("completed output = %+v", completed.Output)
	}
	if completed.Output[0].Type != "web_search_call" {
		t.Fatalf("first output = %+v", completed.Output[0])
	}
	if completed.Output[1].Type != "message" || completed.Output[1].Content[0].Text != "Found results." {
		t.Fatalf("second output = %+v", completed.Output[1])
	}
	for _, event := range converted {
		outputEvent, ok := event.Data.(openai.OutputItemEvent)
		if !ok || outputEvent.Item.Type != "message" {
			continue
		}
		if outputEvent.OutputIndex != 1 {
			t.Fatalf("message output index = %d, want 1", outputEvent.OutputIndex)
		}
	}
}

func TestConvertStreamEventsMarksTextOutputCompletedInFinalResponse(t *testing.T) {
	events := []anthropic.StreamEvent{
		{Type: "message_start", Message: &anthropic.MessageResponse{ID: "msg_1", Type: "message", Role: "assistant"}},
		{Type: "content_block_start", Index: 0, ContentBlock: &anthropic.ContentBlock{Type: "text"}},
		{Type: "content_block_delta", Index: 0, Delta: anthropic.StreamDelta{Type: "text_delta", Text: "Hi"}},
		{Type: "content_block_stop", Index: 0},
		{Type: "message_delta", Delta: anthropic.StreamDelta{StopReason: "end_turn"}},
		{Type: "message_stop"},
	}

	converted := testBridge().ConvertStreamEvents(events, "gpt-test")
	completed := streamLifecycleResponse(t, converted, "response.completed")
	if len(completed.Output) != 1 {
		t.Fatalf("completed output = %+v", completed.Output)
	}
	item := completed.Output[0]
	if item.Type != "message" || item.Status != "completed" || len(item.Content) != 1 || item.Content[0].Text != "Hi" {
		t.Fatalf("completed message item = %+v", item)
	}
}

func TestDeepSeekThinkingIsStatefullyInjectedOnlyForToolCalls(t *testing.T) {
	bridgeUnderTest := testBridgeWithConfig(config.Config{
		DefaultMaxTokens: 1024,
		Routes: map[string]config.RouteEntry{"gpt-test": {
			Provider: "default",
			Model:    "deepseek-v4-pro",
			Extensions: map[string]config.ExtensionSettings{
				deepseekv4.PluginName: extensionEnabled(true),
			},
		}},
		ProviderDefs: map[string]config.ProviderDef{"default": {}},
		Cache: config.CacheConfig{
			Mode:          "off",
			PromptCaching: true,
		},
	})
	events := []anthropic.StreamEvent{
		{Type: "message_start", Message: &anthropic.MessageResponse{ID: "msg_1", Type: "message", Role: "assistant"}},
		{Type: "content_block_start", Index: 0, ContentBlock: &anthropic.ContentBlock{Type: "thinking"}},
		{Type: "content_block_delta", Index: 0, Delta: anthropic.StreamDelta{Type: "thinking_delta", Thinking: "inspect before listing"}},
		{Type: "content_block_delta", Index: 0, Delta: anthropic.StreamDelta{Type: "signature_delta", Signature: "sig_1"}},
		{Type: "content_block_stop", Index: 0},
		{Type: "content_block_start", Index: 1, ContentBlock: &anthropic.ContentBlock{Type: "tool_use", ID: "call_ls", Name: "exec_command", Input: json.RawMessage(`{}`)}},
		{Type: "content_block_delta", Index: 1, Delta: anthropic.StreamDelta{Type: "input_json_delta", PartialJSON: `{"cmd":"ls"}`}},
		{Type: "message_stop"},
		{Type: "content_block_stop", Index: 1},
		{Type: "message_delta", Delta: anthropic.StreamDelta{StopReason: "tool_use"}},
	}

	extData := bridgeUnderTest.NewExtensionData()
	convertedEvents := bridgeUnderTest.ConvertStreamEventsWithContext(events, "gpt-test", codex.ConversionContext{}, extData)
	completed := streamLifecycleResponse(t, convertedEvents, "response.completed")
	// Reasoning item should be emitted alongside the tool call.
	if len(completed.Output) != 2 {
		t.Fatalf("completed output = %+v", completed.Output)
	}
	if completed.Output[0].Type != "reasoning" || len(completed.Output[0].Summary) != 1 || completed.Output[0].Summary[0].Text != "inspect before listing" {
		t.Fatalf("reasoning item = %+v", completed.Output[0])
	}
	if completed.Output[1].Type != "function_call" || completed.Output[1].CallID != "call_ls" {
		t.Fatalf("function call = %+v", completed.Output[1])
	}
	addedItems := streamOutputItems(t, convertedEvents, "response.output_item.added")
	if len(addedItems) == 0 || addedItems[0].Type != "reasoning" || len(addedItems[0].Summary) != 1 {
		t.Fatalf("first added output item should include required reasoning summary: %+v", addedItems)
	}
	doneItems := streamOutputItems(t, convertedEvents, "response.output_item.done")
	if len(doneItems) == 0 || doneItems[0].Type != "reasoning" || len(doneItems[0].Summary) != 1 || doneItems[0].Summary[0].Text != "inspect before listing" {
		t.Fatalf("first done output item should persist reasoning before tool calls: %+v", doneItems)
	}

	// Follow-up request includes the reasoning item as Codex would replay it.
	followup := openai.ResponsesRequest{
		Model: "gpt-test",
		Input: json.RawMessage(`[
			{"role":"user","content":[{"type":"input_text","text":"inspect"}],"type":"message"},
			{"type":"reasoning","summary":[{"type":"summary_text","text":"inspect before listing"}]},
			{"arguments":"{\"cmd\":\"ls\"}","call_id":"call_ls","name":"exec_command","type":"function_call"},
			{"call_id":"call_ls","output":"README.md\n","type":"function_call_output"}
		]`),
	}
	converted, _, err := bridgeUnderTest.ToAnthropic(followup, extData)
	if err != nil {
		t.Fatalf("ToAnthropic(, nil) error = %v", err)
	}
	if len(converted.Messages) != 3 {
		t.Fatalf("messages = %+v", converted.Messages)
	}
	assistant := converted.Messages[1]
	if len(assistant.Content) != 2 {
		t.Fatalf("assistant content = %+v", assistant.Content)
	}
	if assistant.Content[0].Type != "thinking" || assistant.Content[0].Thinking != "inspect before listing" {
		t.Fatalf("thinking block = %+v", assistant.Content[0])
	}
	if assistant.Content[1].Type != "tool_use" || assistant.Content[1].ID != "call_ls" {
		t.Fatalf("tool use = %+v", assistant.Content[1])
	}

	missingCacheFollowup := openai.ResponsesRequest{
		Model: "gpt-test",
		Input: json.RawMessage(`[
			{"role":"user","content":[{"type":"input_text","text":"inspect"}],"type":"message"},
			{"arguments":"{\"cmd\":\"pwd\"}","call_id":"call_pwd","name":"exec_command","type":"function_call"},
			{"call_id":"call_pwd","output":"/repo\n","type":"function_call_output"}
		]`),
	}
	convertedMissing, _, err := bridgeUnderTest.ToAnthropic(missingCacheFollowup, extData)
	if err != nil {
		t.Fatalf("ToAnthropic(, nil) missing cache error = %v", err)
	}
	missingAssistant := convertedMissing.Messages[1]
	if len(missingAssistant.Content) != 2 || missingAssistant.Content[0].Type != "thinking" || missingAssistant.Content[0].Thinking != "" {
		t.Fatalf("fallback thinking block = %+v", missingAssistant.Content)
	}
}

func TestDeepSeekSignatureOnlyThinkingIsReinjectedForToolCalls(t *testing.T) {
	bridgeUnderTest := testBridgeWithConfig(config.Config{
		DefaultMaxTokens: 1024,
		Routes: map[string]config.RouteEntry{"gpt-test": {
			Provider: "default",
			Model:    "deepseek-v4-pro",
			Extensions: map[string]config.ExtensionSettings{
				deepseekv4.PluginName: extensionEnabled(true),
			},
		}},
		ProviderDefs: map[string]config.ProviderDef{"default": {}},
		Cache: config.CacheConfig{
			Mode:          "off",
			PromptCaching: true,
		},
	})
	events := []anthropic.StreamEvent{
		{Type: "message_start", Message: &anthropic.MessageResponse{ID: "msg_1", Type: "message", Role: "assistant"}},
		{Type: "content_block_start", Index: 0, ContentBlock: &anthropic.ContentBlock{Type: "thinking"}},
		{Type: "content_block_delta", Index: 0, Delta: anthropic.StreamDelta{Type: "signature_delta", Signature: "sig_only"}},
		{Type: "content_block_stop", Index: 0},
		{Type: "content_block_start", Index: 1, ContentBlock: &anthropic.ContentBlock{Type: "tool_use", ID: "call_ls", Name: "exec_command", Input: json.RawMessage(`{}`)}},
		{Type: "content_block_delta", Index: 1, Delta: anthropic.StreamDelta{Type: "input_json_delta", PartialJSON: `{"cmd":"ls"}`}},
		{Type: "content_block_stop", Index: 1},
		{Type: "message_delta", Delta: anthropic.StreamDelta{StopReason: "tool_use"}},
		{Type: "message_stop"},
	}

	extData := bridgeUnderTest.NewExtensionData()
	convertedEvents := bridgeUnderTest.ConvertStreamEventsWithContext(events, "gpt-test", codex.ConversionContext{}, extData)

	completed := streamLifecycleResponse(t, convertedEvents, "response.completed")
	if len(completed.Output) != 2 || completed.Output[0].Type != "reasoning" || completed.Output[1].Type != "function_call" {
		t.Fatalf("completed output = %+v", completed.Output)
	}
	if len(completed.Output[0].Summary) != 1 || !strings.HasPrefix(completed.Output[0].Summary[0].Text, "moonbridge:deepseek_v4_thinking:v1:") {
		t.Fatalf("signature reasoning summary = %+v", completed.Output[0])
	}

	// Resume-style follow-up replays the reasoning item; this must work without
	// the original in-memory session state.
	followup := openai.ResponsesRequest{
		Model: "gpt-test",
		Input: json.RawMessage(`[
			{"role":"user","content":[{"type":"input_text","text":"inspect"}],"type":"message"},
			{"type":"reasoning","summary":[{"type":"summary_text","text":"` + completed.Output[0].Summary[0].Text + `"}]},
			{"arguments":"{\"cmd\":\"ls\"}","call_id":"call_ls","name":"exec_command","type":"function_call"},
			{"call_id":"call_ls","output":"README.md\n","type":"function_call_output"}
		]`),
	}
	converted, _, err := bridgeUnderTest.ToAnthropic(followup, nil)
	if err != nil {
		t.Fatalf("ToAnthropic(, nil) error = %v", err)
	}
	assistant := converted.Messages[1]
	if len(assistant.Content) != 2 {
		t.Fatalf("assistant content = %+v", assistant.Content)
	}
	if assistant.Content[0].Type != "thinking" || assistant.Content[0].Thinking != "" || assistant.Content[0].Signature != "sig_only" {
		t.Fatalf("thinking block = %+v", assistant.Content[0])
	}
	data, err := json.Marshal(assistant.Content[0])
	if err != nil {
		t.Fatalf("Marshal thinking block error = %v", err)
	}
	if !strings.Contains(string(data), `"thinking":""`) {
		t.Fatalf("thinking block JSON should keep empty thinking field: %s", data)
	}
}

func TestDeepSeekThinkingIsInjectedForToolChainFinalAssistantText(t *testing.T) {
	bridgeUnderTest := testBridgeWithConfig(config.Config{
		DefaultMaxTokens: 1024,
		Routes: map[string]config.RouteEntry{"gpt-test": {
			Provider: "default",
			Model:    "deepseek-v4-pro",
			Extensions: map[string]config.ExtensionSettings{
				deepseekv4.PluginName: extensionEnabled(true),
			},
		}},
		ProviderDefs: map[string]config.ProviderDef{"default": {}},
		Cache: config.CacheConfig{
			Mode:          "off",
			PromptCaching: true,
		},
	})
	events := []anthropic.StreamEvent{
		{Type: "message_start", Message: &anthropic.MessageResponse{ID: "msg_1", Type: "message", Role: "assistant"}},
		{Type: "content_block_start", Index: 0, ContentBlock: &anthropic.ContentBlock{Type: "thinking"}},
		{Type: "content_block_delta", Index: 0, Delta: anthropic.StreamDelta{Type: "thinking_delta", Thinking: "summarize after tools"}},
		{Type: "content_block_delta", Index: 0, Delta: anthropic.StreamDelta{Type: "signature_delta", Signature: "sig_text"}},
		{Type: "content_block_stop", Index: 0},
		{Type: "content_block_start", Index: 1, ContentBlock: &anthropic.ContentBlock{Type: "text"}},
		{Type: "content_block_delta", Index: 1, Delta: anthropic.StreamDelta{Type: "text_delta", Text: "Project summary"}},
		{Type: "content_block_stop", Index: 1},
		{Type: "message_delta", Delta: anthropic.StreamDelta{StopReason: "end_turn"}},
		{Type: "message_stop"},
	}

	extData := bridgeUnderTest.NewExtensionData()
	nonPersistedEvents := bridgeUnderTest.ConvertStreamEventsWithContext(events, "gpt-test", codex.ConversionContext{}, extData)
	nonPersistedCompleted := streamLifecycleResponse(t, nonPersistedEvents, "response.completed")
	if len(nonPersistedCompleted.Output) != 1 || nonPersistedCompleted.Output[0].Type != "message" {
		t.Fatalf("non-persisted completed output = %+v", nonPersistedCompleted.Output)
	}
	persistedEvents := bridgeUnderTest.ConvertStreamEventsWithContext(events, "gpt-test", codex.ConversionContext{}, nil, bridge.StreamOptions{
		PersistFinalTextReasoning: true,
	})
	persistedCompleted := streamLifecycleResponse(t, persistedEvents, "response.completed")
	if len(persistedCompleted.Output) != 2 || persistedCompleted.Output[0].Type != "reasoning" || persistedCompleted.Output[1].Type != "message" {
		t.Fatalf("persisted completed output = %+v", persistedCompleted.Output)
	}
	doneItems := streamOutputItems(t, persistedEvents, "response.output_item.done")
	if len(doneItems) != 2 || doneItems[0].Type != "reasoning" || len(doneItems[0].Summary) != 1 || doneItems[0].Summary[0].Text != "summarize after tools" {
		t.Fatalf("persisted done items = %+v", doneItems)
	}

	persistedFollowup := openai.ResponsesRequest{
		Model: "gpt-test",
		Input: json.RawMessage(`[
			{"role":"user","content":[{"type":"input_text","text":"inspect"}],"type":"message"},
			{"arguments":"{\"cmd\":\"ls\"}","call_id":"call_ls","name":"exec_command","type":"function_call"},
			{"call_id":"call_ls","output":"README.md\n","type":"function_call_output"},
			{"type":"reasoning","summary":[{"type":"summary_text","text":"summarize after tools"}]},
			{"role":"assistant","content":[{"type":"output_text","text":"Project summary"}],"type":"message"},
			{"role":"user","content":[{"type":"input_text","text":"update docs"}],"type":"message"}
		]`),
	}
	persistedConverted, _, err := bridgeUnderTest.ToAnthropic(persistedFollowup, nil)
	if err != nil {
		t.Fatalf("ToAnthropic() persisted followup error = %v", err)
	}
	persistedAssistant := persistedConverted.Messages[3]
	if len(persistedAssistant.Content) != 2 || persistedAssistant.Content[0].Type != "thinking" || persistedAssistant.Content[0].Thinking != "summarize after tools" {
		t.Fatalf("persisted assistant = %+v", persistedAssistant)
	}

	followup := openai.ResponsesRequest{
		Model: "gpt-test",
		Input: json.RawMessage(`[
			{"role":"user","content":[{"type":"input_text","text":"inspect"}],"type":"message"},
			{"arguments":"{\"cmd\":\"ls\"}","call_id":"call_ls","name":"exec_command","type":"function_call"},
			{"call_id":"call_ls","output":"README.md\n","type":"function_call_output"},
			{"role":"assistant","content":[{"type":"output_text","text":"Project summary"}],"type":"message"},
			{"role":"user","content":[{"type":"input_text","text":"update docs"}],"type":"message"}
		]`),
	}
	converted, _, err := bridgeUnderTest.ToAnthropic(followup, extData)
	if err != nil {
		t.Fatalf("ToAnthropic() error = %v", err)
	}
	assistant := converted.Messages[3]
	if assistant.Role != "assistant" || len(assistant.Content) != 2 {
		t.Fatalf("assistant = %+v", assistant)
	}
	if assistant.Content[0].Type != "thinking" || assistant.Content[0].Thinking != "summarize after tools" || assistant.Content[0].Signature != "sig_text" {
		t.Fatalf("thinking block = %+v", assistant.Content[0])
	}
	if assistant.Content[1].Type != "text" || assistant.Content[1].Text != "Project summary" {
		t.Fatalf("text block = %+v", assistant.Content[1])
	}

	noToolHistory := openai.ResponsesRequest{
		Model: "gpt-test",
		Input: json.RawMessage(`[
			{"role":"user","content":[{"type":"input_text","text":"hello"}],"type":"message"},
			{"role":"assistant","content":[{"type":"output_text","text":"Project summary"}],"type":"message"},
			{"role":"user","content":[{"type":"input_text","text":"continue"}],"type":"message"}
		]`),
	}
	convertedNoTool, _, err := bridgeUnderTest.ToAnthropic(noToolHistory, extData)
	if err != nil {
		t.Fatalf("ToAnthropic() no tool history error = %v", err)
	}
	if got := convertedNoTool.Messages[1].Content[0].Type; got == "thinking" {
		t.Fatalf("unexpected thinking for prompt-only history: %+v", convertedNoTool.Messages[1].Content)
	}
}

func streamLifecycleResponse(t *testing.T, events []openai.StreamEvent, eventName string) openai.Response {
	t.Helper()

	for _, event := range events {
		if event.Event != eventName {
			continue
		}
		lifecycle, ok := event.Data.(openai.ResponseLifecycleEvent)
		if !ok {
			t.Fatalf("%s data = %T", eventName, event.Data)
		}
		return lifecycle.Response
	}
	t.Fatalf("%s not found in %+v", eventName, events)
	return openai.Response{}
}

func streamOutputItems(t *testing.T, events []openai.StreamEvent, eventName string) []openai.OutputItem {
	t.Helper()

	var items []openai.OutputItem
	for _, event := range events {
		if event.Event != eventName {
			continue
		}
		outputEvent, ok := event.Data.(openai.OutputItemEvent)
		if !ok {
			t.Fatalf("%s data = %T", eventName, event.Data)
		}
		items = append(items, outputEvent.Item)
	}
	return items
}
