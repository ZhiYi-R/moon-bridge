//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"testing"

	"moonbridge/internal/format"
	"moonbridge/internal/protocol/openai"
)

// ============================================================================
// TestOpenAIResponsePassthroughE2E_TextRoundTrip
// ============================================================================

func TestOpenAIResponsePassthroughE2E_TextRoundTrip(t *testing.T) {
	ctx := context.Background()
	cfg := e2eMinimalConfig()
	hooks := format.CorePluginHooks{}.WithDefaults()
	reg := newTestRegistry(t, cfg, hooks)

	client, ok := reg.GetClient(configOpenAIResponse)
	if !ok {
		t.Fatal("client adapter not found")
	}

	// Step 1: Build OpenAI Responses request.
	openAIReq := openai.ResponsesRequest{
		Model:           "gpt-4o",
		Input:           json.RawMessage(`"Hello"`),
		MaxOutputTokens: 100,
	}

	// Step 2: ToCoreRequest.
	coreReq, err := client.ToCoreRequest(ctx, &openAIReq)
	if err != nil {
		t.Fatalf("ToCoreRequest: %v", err)
	}

	// Step 3: Verify CoreRequest fields.
	if len(coreReq.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(coreReq.Messages))
	}
	if coreReq.Messages[0].Role != "user" {
		t.Errorf("message role = %q, want %q", coreReq.Messages[0].Role, "user")
	}
	if len(coreReq.Messages[0].Content) != 1 || coreReq.Messages[0].Content[0].Type != "text" {
		t.Errorf("message content type = %q, want %q", coreReq.Messages[0].Content[0].Type, "text")
	}
	if coreReq.Messages[0].Content[0].Text != "Hello" {
		t.Errorf("message text = %q, want %q", coreReq.Messages[0].Content[0].Text, "Hello")
	}
	if coreReq.Model != "gpt-4o" {
		t.Errorf("Model = %q, want %q", coreReq.Model, "gpt-4o")
	}
	if coreReq.MaxTokens != 100 {
		t.Errorf("MaxTokens = %d, want %d", coreReq.MaxTokens, 100)
	}

	// Step 4: Construct matching CoreResponse.
	coreResp := &format.CoreResponse{
		ID:     "resp_mock_001",
		Status: "completed",
		Model:  "gpt-4o",
		Messages: []format.CoreMessage{
			{
				Role: "assistant",
				Content: []format.CoreContentBlock{
					{Type: "text", Text: "Hello passthrough!"},
				},
			},
		},
		Usage: format.CoreUsage{
			InputTokens:  5,
			OutputTokens: 5,
			TotalTokens:  10,
		},
		StopReason: "end_turn",
	}

	// Step 5: FromCoreResponse.
	outAny, err := client.FromCoreResponse(ctx, coreResp)
	if err != nil {
		t.Fatalf("FromCoreResponse: %v", err)
	}
	oaiResp := outAny.(*openai.Response)

	// Step 6: Assert.
	assertResponseBasics(t, oaiResp, "")
	if oaiResp.OutputText != "Hello passthrough!" {
		t.Errorf("OutputText = %q, want %q", oaiResp.OutputText, "Hello passthrough!")
	}
	if oaiResp.ID != "resp_mock_001" {
		t.Errorf("ID = %q, want %q", oaiResp.ID, "resp_mock_001")
	}
	if oaiResp.Status != "completed" {
		t.Errorf("Status = %q, want %q", oaiResp.Status, "completed")
	}
	if oaiResp.Usage.InputTokens != 5 {
		t.Errorf("InputTokens = %d, want %d", oaiResp.Usage.InputTokens, 5)
	}
	if oaiResp.Usage.OutputTokens != 5 {
		t.Errorf("OutputTokens = %d, want %d", oaiResp.Usage.OutputTokens, 5)
	}
}

// ============================================================================
// TestOpenAIResponsePassthroughE2E_MultiTurn
// ============================================================================

func TestOpenAIResponsePassthroughE2E_MultiTurn(t *testing.T) {
	ctx := context.Background()
	cfg := e2eMinimalConfig()
	hooks := format.CorePluginHooks{}.WithDefaults()
	reg := newTestRegistry(t, cfg, hooks)

	client, ok := reg.GetClient(configOpenAIResponse)
	if !ok {
		t.Fatal("client adapter not found")
	}

	// Multi-turn input.
	openAIReq := openai.ResponsesRequest{
		Model: "gpt-4o",
		Input: json.RawMessage(`[
			{"role": "user", "content": "What's the weather?"},
			{"role": "assistant", "content": "I'll check the weather tool."},
			{"role": "user", "content": "Please do"}
		]`),
		MaxOutputTokens: 200,
	}

	coreReq, err := client.ToCoreRequest(ctx, &openAIReq)
	if err != nil {
		t.Fatalf("ToCoreRequest: %v", err)
	}

	// Verify multi-turn messages.
	if len(coreReq.Messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(coreReq.Messages))
	}
	if coreReq.Messages[0].Role != "user" || coreReq.Messages[1].Role != "assistant" || coreReq.Messages[2].Role != "user" {
		t.Errorf("unexpected roles: %q, %q, %q", coreReq.Messages[0].Role, coreReq.Messages[1].Role, coreReq.Messages[2].Role)
	}

	// Construct multi-turn CoreResponse.
	coreResp := &format.CoreResponse{
		ID:     "resp_multi_001",
		Status: "completed",
		Model:  "gpt-4o",
		Messages: []format.CoreMessage{
			{
				Role: "assistant",
				Content: []format.CoreContentBlock{
					{Type: "text", Text: "The weather is sunny today!"},
				},
			},
		},
		Usage:      format.CoreUsage{InputTokens: 15, OutputTokens: 8, TotalTokens: 23},
		StopReason: "end_turn",
	}

	outAny, err := client.FromCoreResponse(ctx, coreResp)
	if err != nil {
		t.Fatalf("FromCoreResponse: %v", err)
	}
	oaiResp := outAny.(*openai.Response)

	assertResponseBasics(t, oaiResp, "")
	if oaiResp.OutputText != "The weather is sunny today!" {
		t.Errorf("OutputText = %q, want %q", oaiResp.OutputText, "The weather is sunny today!")
	}
	if oaiResp.Usage.InputTokens != 15 {
		t.Errorf("InputTokens = %d, want %d", oaiResp.Usage.InputTokens, 15)
	}
}

// ============================================================================
// TestOpenAIResponsePassthroughE2E_ToolUse
// ============================================================================

func TestOpenAIResponsePassthroughE2E_ToolUse(t *testing.T) {
	ctx := context.Background()
	cfg := e2eMinimalConfig()
	hooks := format.CorePluginHooks{}.WithDefaults()
	reg := newTestRegistry(t, cfg, hooks)

	client, ok := reg.GetClient(configOpenAIResponse)
	if !ok {
		t.Fatal("client adapter not found")
	}

	// Request with tools.
	openAIReq := openai.ResponsesRequest{
		Model: "gpt-4o",
		Input: json.RawMessage(`"Use the get_weather tool"`),
		Tools: []openai.Tool{
			{
				Type:        "function",
				Name:        "get_weather",
				Description: "Get the current weather for a city",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"city": map[string]any{"type": "string"},
					},
					"required": []any{"city"},
				},
			},
		},
		ToolChoice:      json.RawMessage(`"required"`),
		MaxOutputTokens: 200,
	}

	coreReq, err := client.ToCoreRequest(ctx, &openAIReq)
	if err != nil {
		t.Fatalf("ToCoreRequest: %v", err)
	}

	// Verify tools in CoreRequest.
	if len(coreReq.Tools) == 0 {
		t.Fatal("expected tools in CoreRequest, got none")
	}
	if coreReq.Tools[0].Name != "get_weather" {
		t.Errorf("tool name = %q, want %q", coreReq.Tools[0].Name, "get_weather")
	}
	if coreReq.ToolChoice == nil {
		t.Fatal("expected ToolChoice in CoreRequest")
	}
	if coreReq.ToolChoice.Mode != "required" {
		t.Errorf("ToolChoice.Mode = %q, want %q", coreReq.ToolChoice.Mode, "required")
	}

	// Construct CoreResponse with function_call output.
	coreResp := &format.CoreResponse{
		ID:     "resp_tool_001",
		Status: "completed",
		Model:  "gpt-4o",
		Messages: []format.CoreMessage{
			{
				Role: "assistant",
				Content: []format.CoreContentBlock{
					{
						Type:      "tool_use",
						ToolUseID: "call_abc",
						ToolName:  "get_weather",
						ToolInput: json.RawMessage(`{"city":"Paris"}`),
					},
				},
			},
		},
		Usage:      format.CoreUsage{InputTokens: 20, OutputTokens: 10, TotalTokens: 30},
		StopReason: "tool_use",
	}

	outAny, err := client.FromCoreResponse(ctx, coreResp)
	if err != nil {
		t.Fatalf("FromCoreResponse: %v", err)
	}
	oaiResp := outAny.(*openai.Response)

	// Verify function_call output item.
	var foundFunctionCall bool
	for _, item := range oaiResp.Output {
		if item.Type == "function_call" {
			foundFunctionCall = true
			if item.Name != "get_weather" {
				t.Errorf("function_call name = %q, want %q", item.Name, "get_weather")
			}
			if item.CallID != "call_abc" {
				t.Errorf("function_call call_id = %q, want %q", item.CallID, "call_abc")
			}
			if item.Arguments != `{"city":"Paris"}` {
				t.Errorf("function_call arguments = %q, want %q", item.Arguments, `{"city":"Paris"}`)
			}
			break
		}
	}
	if !foundFunctionCall {
		t.Error("expected function_call output item, not found")
	}
}

// ============================================================================
// TestOpenAIResponsePassthroughE2E_Streaming
// ============================================================================

func TestOpenAIResponsePassthroughE2E_Streaming(t *testing.T) {
	ctx := context.Background()
	cfg := e2eMinimalConfig()
	hooks := format.CorePluginHooks{}.WithDefaults()
	reg := newTestRegistry(t, cfg, hooks)

	clientStream, ok := reg.GetClientStream(configOpenAIResponse)
	if !ok {
		t.Fatal("client stream adapter not found")
	}

	// Build a minimal CoreRequest for stream context.
	coreReq := &format.CoreRequest{
		Model: "gpt-4o",
		Messages: []format.CoreMessage{
			{Role: "user", Content: []format.CoreContentBlock{{Type: "text", Text: "Hello"}}},
		},
	}

	// Step 1: Construct CoreStreamEvent channel.
	events := make(chan format.CoreStreamEvent, 6)

	// content_block.started (text type)
	events <- format.CoreStreamEvent{
		Type:         format.CoreContentBlockStarted,
		Index:        0,
		ContentBlock: &format.CoreContentBlock{Type: "text"},
	}
	// text delta
	events <- format.CoreStreamEvent{
		Type:  format.CoreTextDelta,
		Index: 0,
		Delta: "Hello",
	}
	// text delta
	events <- format.CoreStreamEvent{
		Type:  format.CoreTextDelta,
		Index: 0,
		Delta: " world",
	}
	// content_block.done
	events <- format.CoreStreamEvent{
		Type:       format.CoreContentBlockDone,
		Index:      0,
		StopReason: "end_turn",
	}
	// completed
	events <- format.CoreStreamEvent{
		Type:   format.CoreEventCompleted,
		Status: "completed",
		Model:  "gpt-4o",
		Usage:  &format.CoreUsage{InputTokens: 5, OutputTokens: 3, TotalTokens: 8},
	}
	close(events)

	// Step 2: FromCoreStream.
	streamOutAny, err := clientStream.FromCoreStream(ctx, coreReq, events)
	if err != nil {
		t.Fatalf("FromCoreStream: %v", err)
	}
	var openAIStream <-chan openai.StreamEvent
	oaiResult, ok := streamOutAny.(*openai.OpenAIStreamResult)
	if ok {
		openAIStream = oaiResult.Chan()
	} else {
		openAIStream = streamOutAny.(<-chan openai.StreamEvent)
	}

	// Step 3: Consume and verify.
	var seenEvents []string
	for ev := range openAIStream {
		seenEvents = append(seenEvents, ev.Event)
	}

	// Verify key events.
	expectedEvents := []string{
		"response.output_text.delta",
		"response.output_text.done",
		"response.completed",
	}
	for _, expected := range expectedEvents {
		var found bool
		for _, seen := range seenEvents {
			if seen == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected stream event %q not found in: %v", expected, seenEvents)
		}
	}

	// Verify accumulated text matches.
	var textDeltaEvents int
	for _, ev := range seenEvents {
		if ev == "response.output_text.delta" {
			textDeltaEvents++
		}
	}
	if textDeltaEvents != 2 {
		t.Errorf("expected 2 output_text.delta events, got %d", textDeltaEvents)
	}

	// Verify output_item.added and content_part.added are present (created on first text delta).
	var foundOutputItemAdded bool
	for _, ev := range seenEvents {
		if ev == "response.output_item.added" {
			foundOutputItemAdded = true
			break
		}
	}
	if !foundOutputItemAdded {
		t.Error("expected output_item.added event, not found")
	}
}

// ============================================================================
// TestOpenAIResponsePassthroughE2E_ErrorResponse
// ============================================================================

func TestOpenAIResponsePassthroughE2E_ErrorResponse(t *testing.T) {
	ctx := context.Background()
	cfg := e2eMinimalConfig()
	hooks := format.CorePluginHooks{}.WithDefaults()
	reg := newTestRegistry(t, cfg, hooks)

	client, ok := reg.GetClient(configOpenAIResponse)
	if !ok {
		t.Fatal("client adapter not found")
	}

	// Construct CoreResponse with error.
	coreResp := &format.CoreResponse{
		ID:     "resp_err_001",
		Status: "failed",
		Error: &format.CoreError{
			Message: "Test error",
			Type:    "server_error",
			Code:    "test_error",
		},
	}

	outAny, err := client.FromCoreResponse(ctx, coreResp)
	if err != nil {
		t.Fatalf("FromCoreResponse: %v", err)
	}
	oaiResp := outAny.(*openai.Response)

	// Verify error was propagated.
	if oaiResp.Error == nil {
		t.Fatal("expected Error in OpenAI response")
	}
	if oaiResp.Error.Message != "Test error" {
		t.Errorf("Error.Message = %q, want %q", oaiResp.Error.Message, "Test error")
	}
	if oaiResp.Error.Type != "server_error" {
		t.Errorf("Error.Type = %q, want %q", oaiResp.Error.Type, "server_error")
	}
	if oaiResp.Error.Code != "test_error" {
		t.Errorf("Error.Code = %q, want %q", oaiResp.Error.Code, "test_error")
	}
	if oaiResp.Status != "failed" {
		t.Errorf("Status = %q, want %q", oaiResp.Status, "failed")
	}
	if oaiResp.ID != "resp_err_001" {
		t.Errorf("ID = %q, want %q", oaiResp.ID, "resp_err_001")
	}
}
