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
		ToolChoice: anthropic.ToolChoice{Type: "tool", Name: ToolVisualBrief},
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
