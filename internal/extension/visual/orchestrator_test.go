package visual

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"moonbridge/internal/protocol/anthropic"
)

type fakeUpstream struct {
	responses []anthropic.MessageResponse
	streams   [][]anthropic.StreamEvent
	requests  []anthropic.MessageRequest
}

func (f *fakeUpstream) CreateMessage(_ context.Context, req anthropic.MessageRequest) (anthropic.MessageResponse, error) {
	f.requests = append(f.requests, req)
	if len(f.responses) == 0 {
		return anthropic.MessageResponse{}, io.EOF
	}
	resp := f.responses[0]
	f.responses = f.responses[1:]
	return resp, nil
}

func (f *fakeUpstream) StreamMessage(_ context.Context, req anthropic.MessageRequest) (anthropic.Stream, error) {
	f.requests = append(f.requests, req)
	if len(f.streams) == 0 {
		return &testStream{}, nil
	}
	events := f.streams[0]
	f.streams = f.streams[1:]
	return &testStream{events: events}, nil
}

type testStream struct {
	events []anthropic.StreamEvent
	index  int
}

func (s *testStream) Next() (anthropic.StreamEvent, error) {
	if s.index >= len(s.events) {
		return anthropic.StreamEvent{}, io.EOF
	}
	event := s.events[s.index]
	s.index++
	return event, nil
}

func (s *testStream) Close() error { return nil }

type fakeVisionClient struct {
	requests []AnalysisRequest
}

func (f *fakeVisionClient) Analyze(_ context.Context, req AnalysisRequest) (string, error) {
	f.requests = append(f.requests, req)
	return "a concise visual result", nil
}

func TestOrchestratorExecutesVisualBriefAndContinues(t *testing.T) {
	upstream := &fakeUpstream{responses: []anthropic.MessageResponse{
		{
			ID:         "msg_tool",
			StopReason: "tool_use",
			Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_visual",
				Name:  ToolVisualBrief,
				Input: json.RawMessage(`{"image_urls":["https://example.test/a.png"],"context":"inspect UI"}`),
			}},
		},
		{
			ID:         "msg_final",
			StopReason: "end_turn",
			Content:    []anthropic.ContentBlock{{Type: "text", Text: "done"}},
		},
	}}
	vision := &fakeVisionClient{}
	orchestrator := NewOrchestrator(OrchestratorConfig{Upstream: upstream, Client: vision})

	resp, err := orchestrator.CreateMessage(context.Background(), anthropic.MessageRequest{
		ToolChoice: &anthropic.ToolChoice{Type: "tool", Name: ToolVisualBrief},
	})
	if err != nil {
		t.Fatalf("CreateMessage() error = %v", err)
	}
	if resp.ID != "msg_final" {
		t.Fatalf("response ID = %q, want msg_final", resp.ID)
	}
	if len(vision.requests) != 1 || vision.requests[0].Tool != ToolVisualBrief {
		t.Fatalf("vision requests = %+v", vision.requests)
	}
	if len(upstream.requests) != 2 {
		t.Fatalf("upstream requests = %d, want 2", len(upstream.requests))
	}
	if upstream.requests[1].ToolChoice.Name != "" || upstream.requests[1].ToolChoice.Type != "auto" {
		t.Fatalf("follow-up tool choice = %+v, want auto", upstream.requests[1].ToolChoice)
	}
	messages := upstream.requests[1].Messages
	if len(messages) != 2 || messages[1].Content[0].Type != "tool_result" {
		t.Fatalf("follow-up messages = %+v", messages)
	}
	result, _ := messages[1].Content[0].Content.(string)
	if !strings.Contains(result, "Visual Brief result:") || !strings.Contains(result, "a concise visual result") {
		t.Fatalf("tool result = %q", result)
	}
}

func TestOrchestratorResolvesAttachedImageReference(t *testing.T) {
	upstream := &fakeUpstream{responses: []anthropic.MessageResponse{
		{
			ID:         "msg_tool",
			StopReason: "tool_use",
			Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_visual",
				Name:  ToolVisualBrief,
				Input: json.RawMessage(`{"image_urls":["Image #1"],"context":"attached image"}`),
			}},
		},
		{
			ID:         "msg_final",
			StopReason: "end_turn",
			Content:    []anthropic.ContentBlock{{Type: "text", Text: "done"}},
		},
	}}
	vision := &fakeVisionClient{}
	orchestrator := NewOrchestrator(OrchestratorConfig{Upstream: upstream, Client: vision})

	_, err := orchestrator.CreateMessage(context.Background(), anthropic.MessageRequest{
		Messages: []anthropic.Message{{
			Role: "user",
			Content: []anthropic.ContentBlock{
				{Type: "text", Text: "what is in this image?"},
				{Type: "image", Source: &anthropic.ImageSource{Type: "base64", MediaType: "image/png", Data: "abc123"}},
			},
		}},
	})
	if err != nil {
		t.Fatalf("CreateMessage() error = %v", err)
	}
	if len(vision.requests) != 1 || len(vision.requests[0].Images) != 1 {
		t.Fatalf("vision requests = %+v", vision.requests)
	}
	image := vision.requests[0].Images[0]
	if image.Data != "abc123" || image.MimeType != "image/png" {
		t.Fatalf("resolved image = %+v", image)
	}
	firstRequestBlocks := upstream.requests[0].Messages[0].Content
	for _, block := range firstRequestBlocks {
		if block.Type == "image" {
			t.Fatalf("upstream received image block: %+v", firstRequestBlocks)
		}
	}
	if !strings.Contains(firstRequestBlocks[1].Text, "Image #1") {
		t.Fatalf("attachment notice = %+v", firstRequestBlocks)
	}
}

func TestOrchestratorLeavesNonVisualToolUseForBridge(t *testing.T) {
	upstream := &fakeUpstream{responses: []anthropic.MessageResponse{{
		ID:         "msg_tool",
		StopReason: "tool_use",
		Content: []anthropic.ContentBlock{{
			Type:  "tool_use",
			ID:    "toolu_lookup",
			Name:  "lookup",
			Input: json.RawMessage(`{"id":"1"}`),
		}},
	}}}
	vision := &fakeVisionClient{}
	orchestrator := NewOrchestrator(OrchestratorConfig{Upstream: upstream, Client: vision})

	resp, err := orchestrator.CreateMessage(context.Background(), anthropic.MessageRequest{})
	if err != nil {
		t.Fatalf("CreateMessage() error = %v", err)
	}
	if resp.ID != "msg_tool" {
		t.Fatalf("response ID = %q, want msg_tool", resp.ID)
	}
	if len(vision.requests) != 0 {
		t.Fatalf("vision requests = %+v, want none", vision.requests)
	}
	if len(upstream.requests) != 1 {
		t.Fatalf("upstream requests = %d, want 1", len(upstream.requests))
	}
}

func TestOrchestratorCollectsStreamToolInput(t *testing.T) {
	upstream := &fakeUpstream{streams: [][]anthropic.StreamEvent{
		{
			{Type: "message_start", Message: &anthropic.MessageResponse{ID: "msg_1", Type: "message", Role: "assistant"}},
			{Type: "content_block_start", Index: 0, ContentBlock: &anthropic.ContentBlock{Type: "tool_use", ID: "toolu_visual", Name: ToolVisualQA}},
			{Type: "content_block_delta", Index: 0, Delta: anthropic.StreamDelta{Type: "input_json_delta", PartialJSON: `{"question":"what text?",`}},
			{Type: "content_block_delta", Index: 0, Delta: anthropic.StreamDelta{Type: "input_json_delta", PartialJSON: `"image_urls":["https://example.test/a.png"]}`}},
			{Type: "content_block_stop", Index: 0},
			{Type: "message_delta", Delta: anthropic.StreamDelta{StopReason: "tool_use"}},
			{Type: "message_stop"},
		},
		{
			{Type: "message_start", Message: &anthropic.MessageResponse{ID: "msg_2", Type: "message", Role: "assistant"}},
			{Type: "content_block_start", Index: 0, ContentBlock: &anthropic.ContentBlock{Type: "text"}},
			{Type: "content_block_delta", Index: 0, Delta: anthropic.StreamDelta{Type: "text_delta", Text: "final"}},
			{Type: "content_block_stop", Index: 0},
			{Type: "message_delta", Delta: anthropic.StreamDelta{StopReason: "end_turn"}},
			{Type: "message_stop"},
		},
	}}
	vision := &fakeVisionClient{}
	orchestrator := NewOrchestrator(OrchestratorConfig{Upstream: upstream, Client: vision})

	stream, err := orchestrator.StreamMessage(context.Background(), anthropic.MessageRequest{})
	if err != nil {
		t.Fatalf("StreamMessage() error = %v", err)
	}
	defer stream.Close()
	var events []anthropic.StreamEvent
	for {
		event, err := stream.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("stream.Next() error = %v", err)
		}
		events = append(events, event)
	}
	if len(vision.requests) != 1 || vision.requests[0].Tool != ToolVisualQA {
		t.Fatalf("vision requests = %+v", vision.requests)
	}
	if len(vision.requests[0].Images) != 1 || vision.requests[0].Images[0].URL != "https://example.test/a.png" {
		t.Fatalf("vision images = %+v", vision.requests[0].Images)
	}
	if len(events) == 0 || events[0].Message.ID != "msg_2" {
		t.Fatalf("stream events = %+v", events)
	}
}

// ============================================================================
// Regression tests: image stripping (visual leak fix)
// ============================================================================

func TestStripImagesFromAnthropic_StripsBase64(t *testing.T) {
	req := anthropic.MessageRequest{
		Model: "test-model",
		Messages: []anthropic.Message{{
			Role: "user",
			Content: []anthropic.ContentBlock{
				{Type: "text", Text: "描述图片"},
				{Type: "image", Source: &anthropic.ImageSource{Type: "base64", MediaType: "image/png", Data: "verylongbase64data1234567890"}},
			},
		}},
	}

	stripped, modified := StripImagesFromAnthropic(req)

	if !modified {
		t.Fatal("StripImagesFromAnthropic: modified = false, want true")
	}
	for _, msg := range stripped.Messages {
		for _, block := range msg.Content {
			if block.Type == "image" {
				t.Fatal("StripImagesFromAnthropic: image block still present after stripping")
			}
		}
	}
	// Verify text placeholder was inserted
	foundPlaceholder := false
	for _, msg := range stripped.Messages {
		for _, block := range msg.Content {
			if block.Type == "text" && block.Text != "描述图片" {
				foundPlaceholder = true
			}
		}
	}
	if !foundPlaceholder {
		t.Fatal("StripImagesFromAnthropic: no text placeholder found after stripping")
	}
}

func TestStripImagesFromAnthropic_TextOnlyUnchanged(t *testing.T) {
	req := anthropic.MessageRequest{
		Model: "test-model",
		Messages: []anthropic.Message{{
			Role:    "user",
			Content: []anthropic.ContentBlock{{Type: "text", Text: "hello"}},
		}},
	}

	stripped, modified := StripImagesFromAnthropic(req)

	if modified {
		t.Fatal("StripImagesFromAnthropic: modified = true, want false for text-only request")
	}
	if len(stripped.Messages) != 1 || stripped.Messages[0].Content[0].Text != "hello" {
		t.Fatal("StripImagesFromAnthropic: text-only request was modified")
	}
}

func TestStripImagesFromAnthropic_MixedContent(t *testing.T) {
	req := anthropic.MessageRequest{
		Model: "test-model",
		Messages: []anthropic.Message{{
			Role: "user",
			Content: []anthropic.ContentBlock{
				{Type: "text", Text: "first"},
				{Type: "image", Source: &anthropic.ImageSource{Type: "base64", MediaType: "image/png", Data: "b64_1"}},
				{Type: "text", Text: "between"},
				{Type: "image", Source: &anthropic.ImageSource{Type: "base64", MediaType: "image/jpeg", Data: "b64_2"}},
				{Type: "text", Text: "last"},
			},
		}},
	}

	stripped, modified := StripImagesFromAnthropic(req)

	if !modified {
		t.Fatal("StripImagesFromAnthropic: modified = false, want true")
	}
	for _, msg := range stripped.Messages {
		for _, block := range msg.Content {
			if block.Type == "image" {
				t.Fatal("image block still present")
			}
		}
	}
	// Should have 5 text blocks (original 3 text + 2 image placeholders)
	textCount := 0
	placeholderCount := 0
	for _, msg := range stripped.Messages {
		for _, block := range msg.Content {
			if block.Type == "text" {
				textCount++
				if block.Text != "first" && block.Text != "between" && block.Text != "last" {
					placeholderCount++
				}
			}
		}
	}
	if textCount != 5 {
		t.Fatalf("expected 5 text blocks (3 original + 2 placeholders), got %d", textCount)
	}
	if placeholderCount != 2 {
		t.Fatalf("expected 2 placeholder blocks, got %d", placeholderCount)
	}
}

func TestStripImagesFromAnthropic_MultiTurnDoesNotRestoreImages(t *testing.T) {
	// Simulate a multi-turn conversation constructed manually (as the orchestrator would):
	// Turn 1: user with images → assistant with tool_use → user with tool_result
	// Turn 2: user (continues) with more text
	// After stripping once, image blocks must not reappear in any turn.

	req := anthropic.MessageRequest{
		Model: "test-model",
		Messages: []anthropic.Message{
			// Turn 1: user sends image
			{
				Role: "user",
				Content: []anthropic.ContentBlock{
					{Type: "text", Text: "第一轮问题"},
					{Type: "image", Source: &anthropic.ImageSource{Type: "base64", MediaType: "image/png", Data: "b64_data"}},
				},
			},
			// Turn 1: assistant calls tool
			{
				Role: "assistant",
				Content: []anthropic.ContentBlock{
					{Type: "tool_use", ID: "call_1", Name: ToolVisualBrief},
				},
			},
			// Turn 1: user returns tool_result (no images here, this is text)
			{
				Role: "user",
				Content: []anthropic.ContentBlock{
					{Type: "tool_result", ToolUseID: "call_1", Content: "visual brief result"},
				},
			},
			// Turn 2: user asks follow-up
			{
				Role: "user",
				Content: []anthropic.ContentBlock{
					{Type: "text", Text: "第二轮追问"},
				},
			},
		},
	}

	stripped, modified := StripImagesFromAnthropic(req)

	if !modified {
		t.Fatal("StripImagesFromAnthropic: modified = false, want true (had image)")
	}

	// Verify: ALL messages have NO image blocks
	for i, msg := range stripped.Messages {
		for _, block := range msg.Content {
			if block.Type == "image" {
				t.Fatalf("message %d (role=%s): image block still present after stripping", i, msg.Role)
			}
		}
	}

	// Verify: the image was replaced with a placeholder in the first user message
	firstTexts := 0
	for _, block := range stripped.Messages[0].Content {
		if block.Type == "text" {
			firstTexts++
		}
	}
	// Original: 1 text + 1 image → After strip: 2 texts (original + placeholder)
	if firstTexts != 2 {
		t.Fatalf("first message content blocks = %d, want 2 (1 original text + 1 placeholder)",
			firstTexts)
	}

	// Verify: other messages are completely unchanged
	if len(stripped.Messages[1].Content) != 1 || stripped.Messages[1].Content[0].Type != "tool_use" {
		t.Fatal("assistant message was modified")
	}
	if len(stripped.Messages[2].Content) != 1 || stripped.Messages[2].Content[0].Type != "tool_result" {
		t.Fatal("tool_result message was modified")
	}
	if stripped.Messages[3].Content[0].Text != "第二轮追问" {
		t.Fatal("second user message was modified")
	}
}
