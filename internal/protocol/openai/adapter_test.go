package openai_test

import (
	"context"
	"encoding/json"
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
