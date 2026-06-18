package openai_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"moonbridge/internal/format"
	"moonbridge/internal/protocol/openai"
)

func TestToCoreRequest_BasicText(t *testing.T) {
	adapter := openai.NewOpenAIAdapter(format.CorePluginHooks{})

	req := &openai.ResponsesRequest{
		Model: "gpt-4o",
		Input: json.RawMessage(`"hello"`),
	}

	result, err := adapter.ToCoreRequest(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}

	if result.Model != "gpt-4o" {
		t.Errorf("Model = %q", result.Model)
	}
	if len(result.Messages) != 1 {
		t.Fatalf("got %d messages", len(result.Messages))
	}
	if result.Messages[0].Role != "user" {
		t.Errorf("Role = %q", result.Messages[0].Role)
	}
	if len(result.Messages[0].Content) != 1 {
		t.Fatalf("got %d content blocks", len(result.Messages[0].Content))
	}
	if result.Messages[0].Content[0].Text != "hello" {
		t.Errorf("Text = %q", result.Messages[0].Content[0].Text)
	}
}

func TestToCoreRequest_WithInstructions(t *testing.T) {
	adapter := openai.NewOpenAIAdapter(format.CorePluginHooks{})

	req := &openai.ResponsesRequest{
		Model:        "gpt-4o",
		Input:        json.RawMessage(`"hello"`),
		Instructions: "Be concise.",
	}

	result, err := adapter.ToCoreRequest(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.System) == 0 {
		t.Fatal("expected system blocks")
	}
	if result.System[0].Text != "Be concise." {
		t.Errorf("System text = %q", result.System[0].Text)
	}
}

func TestToCoreRequest_AppendsInjectedTools(t *testing.T) {
	adapter := openai.NewOpenAIAdapter(format.CorePluginHooks{
		InjectTools: func(context.Context) []format.CoreTool {
			return []format.CoreTool{{
				Name:        "visual_brief",
				Description: "inspect attached image",
				InputSchema: map[string]any{"type": "object"},
			}}
		},
	})

	req := &openai.ResponsesRequest{
		Model: "gpt-4o",
		Input: json.RawMessage(`"describe the attached image"`),
	}

	result, err := adapter.ToCoreRequest(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Tools) != 1 {
		t.Fatalf("got %d tools, want 1: %+v", len(result.Tools), result.Tools)
	}
	if result.Tools[0].Name != "visual_brief" {
		t.Fatalf("tool name = %q, want visual_brief", result.Tools[0].Name)
	}
}

func TestToCoreRequest_FunctionCallOutputImage(t *testing.T) {
	adapter := openai.NewOpenAIAdapter(format.CorePluginHooks{})

	req := &openai.ResponsesRequest{
		Model: "gpt-4o",
		Input: json.RawMessage(`[
			{"type":"function_call","call_id":"call_view","name":"view_image","arguments":"{\"path\":\"dog.jpg\"}"},
			{"type":"function_call_output","call_id":"call_view","output":[
				{"type":"input_image","image_url":"data:image/jpeg;base64,abc123","detail":"original"}
			]}
		]`),
	}

	result, err := adapter.ToCoreRequest(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Messages) != 2 {
		t.Fatalf("messages = %d, want 2: %+v", len(result.Messages), result.Messages)
	}
	toolResult := result.Messages[1].Content[0]
	if toolResult.Type != "tool_result" || toolResult.ToolUseID != "call_view" {
		t.Fatalf("tool result = %+v", toolResult)
	}
	if len(toolResult.ToolResultContent) != 1 {
		t.Fatalf("tool result content = %+v", toolResult.ToolResultContent)
	}
	image := toolResult.ToolResultContent[0]
	if image.Type != "image" || image.ImageData != "data:image/jpeg;base64,abc123" || image.MediaType != "image/jpeg" {
		t.Fatalf("image block = %+v", image)
	}
}

func TestToCoreRequest_NamespaceFunctionCallRewrapsAction(t *testing.T) {
	adapter := openai.NewOpenAIAdapter(format.CorePluginHooks{})

	req := &openai.ResponsesRequest{
		Model: "gpt-4o",
		Input: json.RawMessage(`[
			{"type":"function_call","call_id":"call_spawn","namespace":"multi_agent_v1","name":"spawn_agent","arguments":"{\"message\":\"plan\"}"}
		]`),
	}

	result, err := adapter.ToCoreRequest(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Messages) != 1 {
		t.Fatalf("messages len=%d, want 1: %+v", len(result.Messages), result.Messages)
	}
	block := result.Messages[0].Content[0]
	if block.Type != "tool_use" || block.ToolName != "multi_agent_v1" || block.ToolUseID != "call_spawn" {
		t.Fatalf("tool block = %+v", block)
	}
	var args map[string]any
	if err := json.Unmarshal(block.ToolInput, &args); err != nil {
		t.Fatalf("tool input invalid: %s: %v", string(block.ToolInput), err)
	}
	if args["action"] != "spawn_agent" || args["message"] != "plan" {
		t.Fatalf("tool input = %#v, want action and message", args)
	}
}

func TestFromCoreResponse_Basic(t *testing.T) {
	adapter := openai.NewOpenAIAdapter(format.CorePluginHooks{})

	coreResp := &format.CoreResponse{
		ID:     "resp_123",
		Status: "completed",
		Model:  "gpt-4o",
		Messages: []format.CoreMessage{
			{Role: "assistant", Content: []format.CoreContentBlock{{Type: "text", Text: "Hello!"}}},
		},
		Usage: format.CoreUsage{InputTokens: 10, OutputTokens: 20, TotalTokens: 30},
	}

	result, err := adapter.FromCoreResponse(context.Background(), coreResp)
	if err != nil {
		t.Fatal(err)
	}

	resp, ok := result.(*openai.Response)
	if !ok {
		t.Fatal("expected *openai.Response")
	}

	if resp.ID != "resp_123" {
		t.Errorf("ID = %q", resp.ID)
	}
	if resp.Status != "completed" {
		t.Errorf("Status = %q", resp.Status)
	}
}

func TestFromCoreResponse_NamespaceToolArgumentsOmitAction(t *testing.T) {
	adapter := openai.NewOpenAIAdapter(format.CorePluginHooks{})

	coreResp := &format.CoreResponse{
		ID:         "resp_123",
		Status:     "completed",
		Model:      "gpt-4o",
		Extensions: namespaceToolMapExtension("multi_agent_v1"),
		Messages: []format.CoreMessage{
			{
				Role: "assistant",
				Content: []format.CoreContentBlock{{
					Type:      "tool_use",
					ToolUseID: "call_spawn",
					ToolName:  "multi_agent_v1",
					ToolInput: json.RawMessage(`{"action":"spawn_agent","message":"plan","fork_context":true}`),
				}},
			},
		},
	}

	result, err := adapter.FromCoreResponse(context.Background(), coreResp)
	if err != nil {
		t.Fatal(err)
	}

	resp := result.(*openai.Response)
	if len(resp.Output) != 1 {
		t.Fatalf("output len=%d, want 1: %+v", len(resp.Output), resp.Output)
	}
	item := resp.Output[0]
	if item.Type != "function_call" || item.Name != "spawn_agent" || item.Namespace != "multi_agent_v1" {
		t.Fatalf("item identity = type:%q name:%q namespace:%q", item.Type, item.Name, item.Namespace)
	}
	assertNoActionArgument(t, item.Arguments)
}

func TestFromCoreResponse_Error(t *testing.T) {
	adapter := openai.NewOpenAIAdapter(format.CorePluginHooks{})

	coreResp := &format.CoreResponse{
		Status: "failed",
		Error:  &format.CoreError{Message: "upstream error", Code: "api_error"},
	}

	result, err := adapter.FromCoreResponse(context.Background(), coreResp)
	if err != nil {
		t.Fatal(err)
	}
	resp := result.(*openai.Response)

	if resp.Status != "failed" {
		t.Errorf("Status = %q", resp.Status)
	}
	if resp.Error == nil {
		t.Fatal("expected error")
	}
	if resp.Error.Message != "upstream error" {
		t.Errorf("Error.Message = %q", resp.Error.Message)
	}
}

func TestFromCoreStream_NamespaceToolArgumentsOmitActionFromDeltaAndDone(t *testing.T) {
	adapter := openai.NewOpenAIAdapter(format.CorePluginHooks{})
	coreReq := &format.CoreRequest{
		Model:      "gpt-4o",
		Extensions: namespaceToolMapExtension("multi_agent_v1"),
	}
	evCh := make(chan format.CoreStreamEvent, 8)
	evCh <- format.CoreStreamEvent{
		Type:  format.CoreContentBlockStarted,
		Index: 2,
		ContentBlock: &format.CoreContentBlock{
			Type:      "tool_use",
			ToolUseID: "call_spawn",
			ToolName:  "multi_agent_v1",
		},
	}
	evCh <- format.CoreStreamEvent{Type: format.CoreToolCallArgsDelta, Index: 2, Delta: `{"action":"spawn_agent",`}
	evCh <- format.CoreStreamEvent{Type: format.CoreToolCallArgsDelta, Index: 2, Delta: `"message":"plan"}`}
	evCh <- format.CoreStreamEvent{Type: format.CoreToolCallArgsDone, Index: 2, Delta: `{"action":"spawn_agent","message":"plan"}`}
	evCh <- format.CoreStreamEvent{Type: format.CoreContentBlockDone, Index: 2}
	evCh <- format.CoreStreamEvent{Type: format.CoreEventCompleted, Status: "completed"}
	close(evCh)

	events := collectOpenAIStreamEvents(t, adapter, coreReq, evCh)
	itemDone, argsDone := finalToolStreamEvents(t, events)
	if itemDone.Item.Name != "spawn_agent" || itemDone.Item.Namespace != "multi_agent_v1" {
		t.Fatalf("item identity = type:%q name:%q namespace:%q", itemDone.Item.Type, itemDone.Item.Name, itemDone.Item.Namespace)
	}
	assertNamespaceDeltasParamsOnly(t, events, map[string]any{"message": "plan"})
	assertNoActionArgument(t, itemDone.Item.Arguments)
	assertNoActionArgument(t, argsDone.Arguments)
}

func TestFromCoreStream_NamespaceToolArgumentsOmitActionFromDoneOnly(t *testing.T) {
	adapter := openai.NewOpenAIAdapter(format.CorePluginHooks{})
	coreReq := &format.CoreRequest{
		Model:      "gpt-4o",
		Extensions: namespaceToolMapExtension("multi_agent_v1"),
	}
	evCh := make(chan format.CoreStreamEvent, 8)
	evCh <- format.CoreStreamEvent{
		Type:  format.CoreContentBlockStarted,
		Index: 2,
		ContentBlock: &format.CoreContentBlock{
			Type:      "tool_use",
			ToolUseID: "call_spawn",
			ToolName:  "multi_agent_v1",
		},
	}
	evCh <- format.CoreStreamEvent{Type: format.CoreToolCallArgsDone, Index: 2, Delta: `{"action":"spawn_agent","message":"plan"}`}
	evCh <- format.CoreStreamEvent{Type: format.CoreContentBlockDone, Index: 2}
	evCh <- format.CoreStreamEvent{Type: format.CoreEventCompleted, Status: "completed"}
	close(evCh)

	events := collectOpenAIStreamEvents(t, adapter, coreReq, evCh)
	itemDone, argsDone := finalToolStreamEvents(t, events)
	if itemDone.Item.Name != "spawn_agent" || itemDone.Item.Namespace != "multi_agent_v1" {
		t.Fatalf("item identity = type:%q name:%q namespace:%q", itemDone.Item.Type, itemDone.Item.Name, itemDone.Item.Namespace)
	}
	assertNoActionArgument(t, itemDone.Item.Arguments)
	assertNoActionArgument(t, argsDone.Arguments)
}

func TestToCoreRequest_NilInput(t *testing.T) {
	adapter := openai.NewOpenAIAdapter(format.CorePluginHooks{})

	req := &openai.ResponsesRequest{
		Model: "gpt-4o",
		Input: nil,
	}

	_, err := adapter.ToCoreRequest(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
}

func TestToCoreRequest_ReasoningModelInjectsEmptyReasoningBeforeFunctionCall(t *testing.T) {
	adapter := openai.NewOpenAIAdapter(format.CorePluginHooks{})
	req := &openai.ResponsesRequest{
		Model: "o3-mini",
		Input: json.RawMessage(`[
			{"type":"function_call","id":"fc_1","call_id":"call_1","name":"get_weather","arguments":"{\"city\":\"Paris\"}"}
		]`),
	}
	result, err := adapter.ToCoreRequest(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Messages) != 1 {
		t.Fatalf("messages len=%d, want 1", len(result.Messages))
	}
	if len(result.Messages[0].Content) < 2 {
		t.Fatalf("assistant content len=%d, want >=2", len(result.Messages[0].Content))
	}
	if result.Messages[0].Content[0].Type != "reasoning" {
		t.Fatalf("first content type=%q, want reasoning", result.Messages[0].Content[0].Type)
	}
	if result.Messages[0].Content[1].Type != "tool_use" {
		t.Fatalf("second content type=%q, want tool_use", result.Messages[0].Content[1].Type)
	}
}

func TestToCoreRequest_KeepsToolUseAdjacentToToolResultWhenReasoningPrecedesOutput(t *testing.T) {
	adapter := openai.NewOpenAIAdapter(format.CorePluginHooks{})
	req := &openai.ResponsesRequest{
		Model: "gpt-5.4",
		Input: json.RawMessage(`[
			{"type":"function_call","id":"fc_1","call_id":"call_1","name":"tool_a","arguments":"{\"a\":1}"},
			{"type":"reasoning","summary":[{"type":"text","text":"thinking after tool call"}]},
			{"type":"function_call_output","call_id":"call_1","output":"ok"}
		]`),
	}

	result, err := adapter.ToCoreRequest(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Messages) != 2 {
		t.Fatalf("messages len=%d, want 2; got %+v", len(result.Messages), result.Messages)
	}

	assistant := result.Messages[0]
	if assistant.Role != "assistant" {
		t.Fatalf("messages[0].Role=%q, want assistant", assistant.Role)
	}
	if len(assistant.Content) != 2 {
		t.Fatalf("assistant content len=%d, want 2; got %+v", len(assistant.Content), assistant.Content)
	}
	if assistant.Content[0].Type != "reasoning" || assistant.Content[0].ReasoningText != "thinking after tool call" {
		t.Fatalf("assistant.Content[0]=%+v, want merged reasoning", assistant.Content[0])
	}
	if assistant.Content[1].Type != "tool_use" || assistant.Content[1].ToolUseID != "call_1" {
		t.Fatalf("assistant.Content[1]=%+v, want tool_use call_1", assistant.Content[1])
	}

	toolResult := result.Messages[1]
	if toolResult.Role != "tool" {
		t.Fatalf("messages[1].Role=%q, want tool", toolResult.Role)
	}
	if len(toolResult.Content) != 1 || toolResult.Content[0].Type != "tool_result" || toolResult.Content[0].ToolUseID != "call_1" {
		t.Fatalf("tool result message=%+v", toolResult)
	}
}

func TestToCoreRequest_BatchesCustomToolCallsAndOutputsIntoSingleRound(t *testing.T) {
	adapter := openai.NewOpenAIAdapter(format.CorePluginHooks{})
	req := &openai.ResponsesRequest{
		Model: "gpt-5.4",
		Input: json.RawMessage(`[
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"before tools"}]},
			{"type":"custom_tool_call","call_id":"call_a","name":"apply_patch","input":"patch a","arguments":"{\"input\":\"patch a\"}"},
			{"type":"custom_tool_call_output","call_id":"call_a","output":"ok a"},
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"between tools"}]},
			{"type":"custom_tool_call","call_id":"call_b","name":"apply_patch","input":"patch b","arguments":"{\"input\":\"patch b\"}"},
			{"type":"custom_tool_call_output","call_id":"call_b","output":"ok b"},
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"between tools 2"}]},
			{"type":"custom_tool_call","call_id":"call_c","name":"apply_patch","input":"patch c","arguments":"{\"input\":\"patch c\"}"},
			{"type":"custom_tool_call_output","call_id":"call_c","output":"ok c"},
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"after tools"}]}
		]`),
	}

	result, err := adapter.ToCoreRequest(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}

	if len(result.Messages) != 10 {
		t.Fatalf("messages len=%d, want 10; got %+v", len(result.Messages), result.Messages)
	}

	if result.Messages[0].Role != "assistant" || len(result.Messages[0].Content) != 1 || result.Messages[0].Content[0].Text != "before tools" {
		t.Fatalf("messages[0]=%+v, want pre-tool assistant text", result.Messages[0])
	}

	for i, want := range []struct {
		assistantTextIdx int
		msgIdx           int
		callID           string
		outcome          string
	}{
		{0, 1, "call_a", "ok a"},
		{3, 4, "call_b", "ok b"},
		{6, 7, "call_c", "ok c"},
	} {
		if result.Messages[want.assistantTextIdx].Role != "assistant" {
			t.Fatalf("assistant commentary turn %d = %+v", i, result.Messages[want.assistantTextIdx])
		}
		assistant := result.Messages[want.msgIdx]
		if assistant.Role != "assistant" || len(assistant.Content) != 1 || assistant.Content[0].Type != "tool_use" || assistant.Content[0].ToolUseID != want.callID {
			t.Fatalf("assistant tool turn %d = %+v", i, assistant)
		}
		toolResult := result.Messages[want.msgIdx+1]
		if toolResult.Role != "tool" || len(toolResult.Content) != 1 || toolResult.Content[0].Type != "tool_result" || toolResult.Content[0].ToolUseID != want.callID {
			t.Fatalf("tool result turn %d = %+v", i, toolResult)
		}
		if got := toolResult.Content[0].ToolResultContent[0].Text; got != want.outcome {
			t.Fatalf("tool result text turn %d = %q, want %q", i, got, want.outcome)
		}
	}

	if result.Messages[9].Role != "assistant" || len(result.Messages[9].Content) != 1 || result.Messages[9].Content[0].Text != "after tools" {
		t.Fatalf("messages[9]=%+v, want trailing assistant text", result.Messages[9])
	}
}

func TestFromCoreStream_NoDuplicateDoneForToolUse(t *testing.T) {
	adapter := openai.NewOpenAIAdapter(format.CorePluginHooks{})
	coreReq := &format.CoreRequest{Model: "gpt-4o"}
	evCh := make(chan format.CoreStreamEvent, 8)
	evCh <- format.CoreStreamEvent{
		Type:  format.CoreContentBlockStarted,
		Index: 5,
		ContentBlock: &format.CoreContentBlock{
			Type:      "tool_use",
			ToolUseID: "call_1",
			ToolName:  "exec_command",
		},
	}
	evCh <- format.CoreStreamEvent{Type: format.CoreToolCallArgsDelta, Index: 5, Delta: `{"cmd":"ls"}`}
	evCh <- format.CoreStreamEvent{Type: format.CoreToolCallArgsDone, Index: 5, Delta: `{"cmd":"ls"}`}
	evCh <- format.CoreStreamEvent{Type: format.CoreContentBlockDone, Index: 5}
	evCh <- format.CoreStreamEvent{Type: format.CoreEventCompleted, Status: "completed"}
	close(evCh)

	streamAny, err := adapter.FromCoreStream(context.Background(), coreReq, evCh)
	if err != nil {
		t.Fatal(err)
	}
	var stream <-chan openai.StreamEvent
	oaiResult, ok := streamAny.(*openai.OpenAIStreamResult)
	if ok {
		stream = oaiResult.Chan()
	} else {
		stream = streamAny.(<-chan openai.StreamEvent)
	}
	var argsDone int
	var itemDone int
	for ev := range stream {
		if ev.Event == "response.function_call_arguments.done" {
			argsDone++
		}
		if ev.Event == "response.output_item.done" {
			if data, ok := ev.Data.(openai.OutputItemEvent); ok && strings.HasPrefix(data.Item.CallID, "call_") {
				itemDone++
			}
		}
	}
	if argsDone != 1 {
		t.Fatalf("function_call_arguments.done count=%d, want 1", argsDone)
	}
	if itemDone != 1 {
		t.Fatalf("output_item.done (tool) count=%d, want 1", itemDone)
	}
}

func namespaceToolMapExtension(namespace string) map[string]any {
	return map[string]any{
		"codex_tool_map": map[string]any{
			namespace: map[string]any{
				"kind":        "nested_oneof",
				"openai_name": namespace,
				"namespace":   namespace,
			},
		},
	}
}

func collectOpenAIStreamEvents(t *testing.T, adapter *openai.OpenAIAdapter, coreReq *format.CoreRequest, evCh <-chan format.CoreStreamEvent) []openai.StreamEvent {
	t.Helper()

	streamAny, err := adapter.FromCoreStream(context.Background(), coreReq, evCh)
	if err != nil {
		t.Fatal(err)
	}
	var stream <-chan openai.StreamEvent
	if oaiResult, ok := streamAny.(*openai.OpenAIStreamResult); ok {
		stream = oaiResult.Chan()
	} else {
		stream = streamAny.(<-chan openai.StreamEvent)
	}
	var events []openai.StreamEvent
	for ev := range stream {
		events = append(events, ev)
	}
	return events
}

func finalToolStreamEvents(t *testing.T, events []openai.StreamEvent) (openai.OutputItemEvent, openai.FunctionCallArgumentsDoneEvent) {
	t.Helper()

	var itemDone *openai.OutputItemEvent
	var argsDone *openai.FunctionCallArgumentsDoneEvent
	for _, ev := range events {
		switch ev.Event {
		case "response.function_call_arguments.done":
			data, ok := ev.Data.(openai.FunctionCallArgumentsDoneEvent)
			if !ok {
				t.Fatalf("args done data type = %T", ev.Data)
			}
			argsDone = &data
		case "response.output_item.done":
			data, ok := ev.Data.(openai.OutputItemEvent)
			if !ok {
				t.Fatalf("item done data type = %T", ev.Data)
			}
			if data.Item.Type == "function_call" {
				itemDone = &data
			}
		}
	}
	if itemDone == nil {
		t.Fatalf("missing function_call output_item.done in events: %+v", events)
	}
	if argsDone == nil {
		t.Fatalf("missing function_call_arguments.done in events: %+v", events)
	}
	return *itemDone, *argsDone
}

func assertNoActionArgument(t *testing.T, raw string) {
	t.Helper()

	if raw == "" {
		t.Fatal("arguments are empty")
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		t.Fatalf("arguments are not JSON object: %q: %v", raw, err)
	}
	if _, exists := args["action"]; exists {
		t.Fatalf("arguments leaked action discriminator: %s", raw)
	}
}

func assertNamespaceDeltasParamsOnly(t *testing.T, events []openai.StreamEvent, want map[string]any) {
	t.Helper()

	var combined strings.Builder
	for _, ev := range events {
		if ev.Event != "response.function_call_arguments.delta" {
			continue
		}
		data, ok := ev.Data.(openai.FunctionCallArgumentsDeltaEvent)
		if !ok {
			t.Fatalf("delta data type = %T", ev.Data)
		}
		if strings.Contains(data.Delta, `"action"`) || strings.Contains(data.Delta, "spawn_agent") {
			t.Fatalf("delta leaked namespace action: %q", data.Delta)
		}
		combined.WriteString(data.Delta)
	}
	if combined.Len() == 0 {
		t.Fatal("missing function_call_arguments.delta events")
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(combined.String()), &got); err != nil {
		t.Fatalf("combined deltas are not valid JSON: %q: %v", combined.String(), err)
	}
	if len(got) != len(want) {
		t.Fatalf("combined deltas = %#v, want %#v", got, want)
	}
	for key, expected := range want {
		if got[key] != expected {
			t.Fatalf("combined deltas[%q] = %#v, want %#v; full=%#v", key, got[key], expected, got)
		}
	}
}
