package visual

import (
	"context"
	"testing"

	"moonbridge/internal/protocol/anthropic"
)

func TestBridgeClientUsesExistingProvider(t *testing.T) {
	upstream := &fakeUpstream{responses: []anthropic.MessageResponse{{
		ID:         "msg_visual",
		StopReason: "end_turn",
		Content:    []anthropic.ContentBlock{{Type: "text", Text: "mountain scene"}},
	}}}
	client := NewBridgeClient(ClientConfig{
		Provider:  upstream,
		Model:     "kimi-for-coding",
		MaxTokens: 512,
	})

	text, err := client.Analyze(context.Background(), AnalysisRequest{
		Tool:   ToolVisualBrief,
		Prompt: "describe",
		Images: []ImageInput{{URL: "https://example.test/a.png"}},
	})
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if text != "mountain scene" {
		t.Fatalf("Analyze() = %q", text)
	}
	if len(upstream.requests) != 1 {
		t.Fatalf("provider requests = %d, want 1", len(upstream.requests))
	}
	req := upstream.requests[0]
	if req.Model != "kimi-for-coding" || req.MaxTokens != 512 {
		t.Fatalf("visual request model/max = %s/%d", req.Model, req.MaxTokens)
	}
	if len(req.System) != 1 || req.System[0].Text == "" {
		t.Fatalf("visual system prompt = %+v", req.System)
	}
	if len(req.Messages) != 1 || len(req.Messages[0].Content) != 2 {
		t.Fatalf("visual messages = %+v", req.Messages)
	}
	image := req.Messages[0].Content[1]
	if image.Type != "image" || image.Source == nil || image.Source.Type != "url" || image.Source.URL != "https://example.test/a.png" {
		t.Fatalf("visual image block = %+v", image)
	}
}
