package openai_test

import (
	"context"
	"encoding/json"
	"testing"

	"moonbridge/internal/format"
	"moonbridge/internal/protocol/anthropic"
	"moonbridge/internal/protocol/chat"
	"moonbridge/internal/protocol/google"
	"moonbridge/internal/protocol/openai"
)

type noopOpenAIChainCacheManager struct{}

func (n noopOpenAIChainCacheManager) PlanAndInject(_ context.Context, _ *anthropic.MessageRequest, _ *format.CoreRequest) (key, ttl string) {
	return "", ""
}

func (n noopOpenAIChainCacheManager) UpdateRegistry(_ context.Context, _, _ string, _ anthropic.Usage) {
}

func TestAdapterChain_ServerSideToolOnlyFallsBack(t *testing.T) {
	openAIReq := openai.ResponsesRequest{
		Model:      "gpt-4o",
		Input:      json.RawMessage(`"make an image"`),
		ToolChoice: json.RawMessage(`{"type":"image_generation"}`),
		Tools: []openai.Tool{
			{Type: "image_generation"},
		},
	}

	assertCoreServerSideTool(t, openAIReq, 1)
	assertAnthropicServerSideFallback(t, openAIReq, 0)
	assertChatServerSideFallback(t, openAIReq, 0)
	assertGoogleServerSideFallback(t, openAIReq, 0)
}

func TestAdapterChain_ServerSideToolMixedWithFunctionFallsBack(t *testing.T) {
	openAIReq := openai.ResponsesRequest{
		Model:      "gpt-4o",
		Input:      json.RawMessage(`"make an image, then check weather"`),
		ToolChoice: json.RawMessage(`{"type":"image_generation"}`),
		Tools: []openai.Tool{
			{Type: "image_generation"},
			{
				Type:        "function",
				Name:        "get_weather",
				Description: "Get weather",
				Parameters:  map[string]any{"type": "object"},
			},
		},
	}

	assertCoreServerSideTool(t, openAIReq, 2)
	assertAnthropicServerSideFallback(t, openAIReq, 1)
	assertChatServerSideFallback(t, openAIReq, 1)
	assertGoogleServerSideFallback(t, openAIReq, 1)
}

func TestAdapterChain_NamespaceComputerUseRequiredIsNormalized(t *testing.T) {
	openAIReq := openai.ResponsesRequest{
		Model: "gpt-4o",
		Input: json.RawMessage(`"use the computer"`),
		Tools: []openai.Tool{
			{
				Type: "namespace",
				Name: "mcp__computer_use",
				Tools: []openai.Tool{
					{
						Type:        "function",
						Name:        "click",
						Description: "Click an element",
						Parameters: map[string]any{
							"type":     "object",
							"required": []any{"app", "element_index", "action"},
							"properties": map[string]any{
								"action":        map[string]any{"type": "string"},
								"app":           map[string]any{"type": "string"},
								"element_index": map[string]any{"type": "integer"},
							},
						},
					},
				},
			},
		},
	}

	coreReq := coreFromOpenAIRequest(t, openAIReq)
	if len(coreReq.Tools) != 1 {
		t.Fatalf("Core tools: got %d, want 1", len(coreReq.Tools))
	}

	assertProviderToolRequired(t, coreReq.Tools[0].InputSchema, []string{"action", "app", "element_index"})

	anthropicProvider := anthropic.NewAnthropicProviderAdapter(0, noopOpenAIChainCacheManager{}, format.CorePluginHooks{})
	anthropicResult, err := anthropicProvider.FromCoreRequest(context.Background(), coreReq)
	if err != nil {
		t.Fatalf("Anthropic FromCoreRequest: %v", err)
	}
	anthropicReq := anthropicResult.(*anthropic.MessageRequest)
	if len(anthropicReq.Tools) != 1 {
		t.Fatalf("Anthropic tools: got %d, want 1", len(anthropicReq.Tools))
	}
	assertProviderToolRequired(t, anthropicReq.Tools[0].InputSchema, []string{"action", "app", "element_index"})

	chatProvider := chat.NewChatProviderAdapter(0, nil, format.CorePluginHooks{})
	chatResult, err := chatProvider.FromCoreRequest(context.Background(), coreReq)
	if err != nil {
		t.Fatalf("Chat FromCoreRequest: %v", err)
	}
	chatReq := chatResult.(*chat.ChatRequest)
	if len(chatReq.Tools) != 1 {
		t.Fatalf("Chat tools: got %d, want 1", len(chatReq.Tools))
	}
	assertProviderToolRequired(t, chatReq.Tools[0].Function.Parameters, []string{"action", "app", "element_index"})

	googleProvider := google.NewGeminiProviderAdapter(0, nil, format.CorePluginHooks{}, nil, nil)
	googleResult, err := googleProvider.FromCoreRequest(context.Background(), coreReq)
	if err != nil {
		t.Fatalf("Google FromCoreRequest: %v", err)
	}
	googleReq := googleResult.(*google.GenerateContentRequest)
	if len(googleReq.Tools) != 1 {
		t.Fatalf("Google tools: got %d, want 1", len(googleReq.Tools))
	}
	assertProviderToolRequired(t, googleReq.Tools[0].FunctionDeclarations[0].Parameters, []string{"action", "app", "element_index"})
}

func TestAdapterChain_DuplicateToolOutputsAreMergedForChatProvider(t *testing.T) {
	openAIReq := openai.ResponsesRequest{
		Model: "gpt-4o",
		Input: json.RawMessage(`[
			{"type":"function_call","call_id":"call_dup","name":"lookup","arguments":"{\"query\":\"moon\"}"},
			{"type":"function_call_output","call_id":"call_dup","output":"first"},
			{"type":"function_call_output","call_id":"call_dup","output":"second"}
		]`),
	}

	coreReq := coreFromOpenAIRequest(t, openAIReq)
	if len(coreReq.Messages) != 3 {
		t.Fatalf("Core messages: got %d, want 3", len(coreReq.Messages))
	}

	provider := chat.NewChatProviderAdapter(0, nil, format.CorePluginHooks{})
	result, err := provider.FromCoreRequest(context.Background(), coreReq)
	if err != nil {
		t.Fatalf("Chat FromCoreRequest: %v", err)
	}
	upstreamReq := result.(*chat.ChatRequest)

	var toolMessages []chat.ChatMessage
	for _, msg := range upstreamReq.Messages {
		if msg.Role == "tool" {
			toolMessages = append(toolMessages, msg)
		}
	}
	if len(toolMessages) != 1 {
		t.Fatalf("tool messages: got %d, want 1: %+v", len(toolMessages), toolMessages)
	}
	if toolMessages[0].ToolCallID != "call_dup" {
		t.Fatalf("tool_call_id = %q, want call_dup", toolMessages[0].ToolCallID)
	}
	if content, ok := toolMessages[0].Content.(string); !ok || content != "first\nsecond" {
		t.Fatalf("tool content = %#v (%T), want merged output", toolMessages[0].Content, toolMessages[0].Content)
	}
}

func TestAdapterChain_ReusedToolCallIDsAreRenamedForChatProvider(t *testing.T) {
	openAIReq := openai.ResponsesRequest{
		Model: "gpt-4o",
		Input: json.RawMessage(`[
			{"type":"function_call","call_id":"call_reused","name":"lookup","arguments":"{\"query\":\"first\"}"},
			{"type":"function_call_output","call_id":"call_reused","output":"first"},
			{"type":"function_call","call_id":"call_reused","name":"lookup","arguments":"{\"query\":\"second\"}"},
			{"type":"function_call_output","call_id":"call_reused","output":"second"}
		]`),
	}

	coreReq := coreFromOpenAIRequest(t, openAIReq)
	if len(coreReq.Messages) != 4 {
		t.Fatalf("Core messages: got %d, want 4", len(coreReq.Messages))
	}

	provider := chat.NewChatProviderAdapter(0, nil, format.CorePluginHooks{})
	result, err := provider.FromCoreRequest(context.Background(), coreReq)
	if err != nil {
		t.Fatalf("Chat FromCoreRequest: %v", err)
	}
	upstreamReq := result.(*chat.ChatRequest)
	if len(upstreamReq.Messages) != 4 {
		t.Fatalf("Chat messages: got %d, want 4: %+v", len(upstreamReq.Messages), upstreamReq.Messages)
	}

	firstID := upstreamReq.Messages[0].ToolCalls[0].ID
	firstToolID := upstreamReq.Messages[1].ToolCallID
	secondID := upstreamReq.Messages[2].ToolCalls[0].ID
	secondToolID := upstreamReq.Messages[3].ToolCallID
	if firstID != "call_reused" || firstToolID != firstID {
		t.Fatalf("first pair IDs: assistant=%q tool=%q", firstID, firstToolID)
	}
	if secondID == "" || secondID == "call_reused" || secondToolID != secondID {
		t.Fatalf("second pair IDs: assistant=%q tool=%q", secondID, secondToolID)
	}
	if firstID == secondID {
		t.Fatalf("tool_call_id was reused: %q", firstID)
	}
	if upstreamReq.Messages[1].Content != "first" || upstreamReq.Messages[3].Content != "second" {
		t.Fatalf("tool contents changed: %+v", upstreamReq.Messages)
	}
}

func coreFromOpenAIRequest(t *testing.T, req openai.ResponsesRequest) *format.CoreRequest {
	t.Helper()

	client := openai.NewOpenAIAdapter(format.CorePluginHooks{})
	coreReq, err := client.ToCoreRequest(context.Background(), &req)
	if err != nil {
		t.Fatalf("ToCoreRequest: %v", err)
	}
	return coreReq
}

func assertCoreServerSideTool(t *testing.T, req openai.ResponsesRequest, wantTools int) {
	t.Helper()

	coreReq := coreFromOpenAIRequest(t, req)
	if len(coreReq.Tools) != wantTools {
		t.Fatalf("Core tools: got %d, want %d", len(coreReq.Tools), wantTools)
	}
	if len(coreReq.Tools) == 0 {
		t.Fatal("Core tools is empty")
	}
	if coreReq.Tools[0].Name != "" {
		t.Errorf("Core server-side tool Name = %q, want empty", coreReq.Tools[0].Name)
	}
	if !format.IsServerSideTool(coreReq.Tools[0]) {
		t.Fatalf("Core tools[0] was not recognized as a server-side tool: %#v", coreReq.Tools[0])
	}
	sourceType, _ := coreReq.Tools[0].Extensions["source_type"].(string)
	if sourceType != "image_generation" {
		t.Errorf("Core tools[0] source_type = %q, want image_generation", sourceType)
	}
	if coreReq.ToolChoice == nil {
		t.Fatal("Core ToolChoice is nil")
	}
	if coreReq.ToolChoice.Mode != "image_generation" {
		t.Errorf("Core ToolChoice.Mode = %q, want image_generation", coreReq.ToolChoice.Mode)
	}
	if coreReq.ToolChoice.Name != "" {
		t.Errorf("Core ToolChoice.Name = %q, want empty", coreReq.ToolChoice.Name)
	}
	if len(coreReq.ToolChoice.Raw) == 0 {
		t.Fatal("Core ToolChoice.Raw is empty")
	}
}

func assertAnthropicServerSideFallback(t *testing.T, req openai.ResponsesRequest, wantTools int) {
	t.Helper()

	coreReq := coreFromOpenAIRequest(t, req)
	provider := anthropic.NewAnthropicProviderAdapter(0, noopOpenAIChainCacheManager{}, format.CorePluginHooks{})
	result, err := provider.FromCoreRequest(context.Background(), coreReq)
	if err != nil {
		t.Fatalf("Anthropic FromCoreRequest: %v", err)
	}
	upstreamReq := result.(*anthropic.MessageRequest)
	if len(upstreamReq.Tools) != wantTools {
		t.Fatalf("Anthropic tools: got %d, want %d", len(upstreamReq.Tools), wantTools)
	}
	if wantTools > 0 {
		if upstreamReq.Tools[0].Name != "get_weather" {
			t.Errorf("Anthropic tool name = %q, want get_weather", upstreamReq.Tools[0].Name)
		}
	}
	for _, tool := range upstreamReq.Tools {
		if tool.Name == "" {
			t.Fatal("Anthropic emitted an empty-name tool")
		}
	}
	if wantTools == 0 {
		if upstreamReq.ToolChoice != nil {
			t.Fatalf("Anthropic ToolChoice = %+v, want nil", upstreamReq.ToolChoice)
		}
	} else {
		if upstreamReq.ToolChoice == nil {
			t.Fatal("Anthropic ToolChoice is nil")
		}
		if upstreamReq.ToolChoice.Type != "auto" {
			t.Errorf("Anthropic ToolChoice.Type = %q, want auto", upstreamReq.ToolChoice.Type)
		}
	}
}

func assertChatServerSideFallback(t *testing.T, req openai.ResponsesRequest, wantTools int) {
	t.Helper()

	coreReq := coreFromOpenAIRequest(t, req)
	provider := chat.NewChatProviderAdapter(0, nil, format.CorePluginHooks{})
	result, err := provider.FromCoreRequest(context.Background(), coreReq)
	if err != nil {
		t.Fatalf("Chat FromCoreRequest: %v", err)
	}
	upstreamReq := result.(*chat.ChatRequest)
	if len(upstreamReq.Tools) != wantTools {
		t.Fatalf("Chat tools: got %d, want %d", len(upstreamReq.Tools), wantTools)
	}
	if wantTools > 0 {
		if upstreamReq.Tools[0].Function.Name != "get_weather" {
			t.Errorf("Chat tool name = %q, want get_weather", upstreamReq.Tools[0].Function.Name)
		}
	}
	for _, tool := range upstreamReq.Tools {
		if tool.Function.Name == "" {
			t.Fatal("Chat emitted an empty-name function tool")
		}
	}
	if wantTools == 0 {
		if len(upstreamReq.ToolChoice) != 0 {
			t.Fatalf("Chat ToolChoice = %s, want omitted", string(upstreamReq.ToolChoice))
		}
	} else {
		var choice string
		if err := json.Unmarshal(upstreamReq.ToolChoice, &choice); err != nil {
			t.Fatalf("Chat ToolChoice not string JSON: %s", string(upstreamReq.ToolChoice))
		}
		if choice != "auto" {
			t.Errorf("Chat ToolChoice = %q, want auto", choice)
		}
	}
}

func assertGoogleServerSideFallback(t *testing.T, req openai.ResponsesRequest, wantTools int) {
	t.Helper()

	coreReq := coreFromOpenAIRequest(t, req)
	provider := google.NewGeminiProviderAdapter(0, nil, format.CorePluginHooks{}, nil, nil)
	result, err := provider.FromCoreRequest(context.Background(), coreReq)
	if err != nil {
		t.Fatalf("Google FromCoreRequest: %v", err)
	}
	upstreamReq := result.(*google.GenerateContentRequest)
	if len(upstreamReq.Tools) != wantTools {
		t.Fatalf("Google tools: got %d, want %d", len(upstreamReq.Tools), wantTools)
	}
	if wantTools > 0 {
		if len(upstreamReq.Tools[0].FunctionDeclarations) != 1 {
			t.Fatalf("Google function declarations: got %d, want 1", len(upstreamReq.Tools[0].FunctionDeclarations))
		}
		if upstreamReq.Tools[0].FunctionDeclarations[0].Name != "get_weather" {
			t.Errorf("Google tool name = %q, want get_weather", upstreamReq.Tools[0].FunctionDeclarations[0].Name)
		}
	}
	for _, tool := range upstreamReq.Tools {
		for _, fn := range tool.FunctionDeclarations {
			if fn.Name == "" {
				t.Fatal("Google emitted an empty-name function declaration")
			}
		}
	}
	if len(upstreamReq.ToolConfig) != 0 {
		t.Errorf("Google ToolConfig = %s, want empty", string(upstreamReq.ToolConfig))
	}
}

func assertProviderToolRequired(t *testing.T, schema map[string]any, want []string) {
	t.Helper()

	oneOf, ok := schema["oneOf"].([]any)
	if ok {
		if len(oneOf) != 1 {
			t.Fatalf("oneOf: got %d branches, want 1", len(oneOf))
		}
		branch, ok := oneOf[0].(map[string]any)
		if !ok {
			t.Fatalf("oneOf[0] = %T, want map[string]any", oneOf[0])
		}
		schema = branch
	}
	if oneOf, ok := schema["oneOf"].([]map[string]any); ok {
		if len(oneOf) != 1 {
			t.Fatalf("oneOf: got %d branches, want 1", len(oneOf))
		}
		schema = oneOf[0]
	}

	required, ok := schema["required"].([]any)
	if !ok {
		if strings, ok := schema["required"].([]string); ok {
			required = make([]any, 0, len(strings))
			for _, item := range strings {
				required = append(required, item)
			}
		} else {
			t.Fatalf("required = %T, want []any or []string", schema["required"])
		}
	}
	if len(required) != len(want) {
		t.Fatalf("required = %#v, want %#v", required, want)
	}
	for i := range want {
		item, ok := required[i].(string)
		if !ok {
			t.Fatalf("required[%d] = %T, want string", i, required[i])
		}
		if item != want[i] {
			t.Fatalf("required = %#v, want %#v", required, want)
		}
	}
}
