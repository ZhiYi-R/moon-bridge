package visual

import (
	"strings"
	"testing"

	"github.com/ZhiYi-R/moon-bridge/internal/protocol/chat"
)

func TestStripImagesFromChat_StripsImageURL(t *testing.T) {
	req := chat.ChatRequest{
		Model: "qwen-vl-plus",
		Messages: []chat.ChatMessage{{
			Role: "user",
			Content: []chat.ContentPart{
				{Type: "text", Text: "描述图片"},
				{Type: "image_url", ImageURL: &chat.ImageURL{URL: "data:image/png;base64,AAAA", Detail: "auto"}},
			},
		}},
	}

	stripped, modified := StripImagesFromChat(req)

	if !modified {
		t.Fatal("modified = false, want true")
	}
	parts, ok := stripped.Messages[0].Content.([]chat.ContentPart)
	if !ok {
		t.Fatalf("Content = %T, want []chat.ContentPart", stripped.Messages[0].Content)
	}
	for _, p := range parts {
		if p.Type == "image_url" {
			t.Fatal("image_url part still present after stripping")
		}
	}
	if len(parts) != 2 {
		t.Fatalf("parts = %d, want 2 (original text + placeholder)", len(parts))
	}
	if parts[0].Text != "描述图片" {
		t.Errorf("part[0].Text = %q, want original prompt preserved", parts[0].Text)
	}
	if !strings.Contains(parts[1].Text, "Image #1") {
		t.Errorf("part[1].Text = %q, want placeholder referencing Image #1", parts[1].Text)
	}
}

func TestStripImagesFromChat_TextOnlyUnchanged(t *testing.T) {
	req := chat.ChatRequest{
		Model: "deepseek",
		Messages: []chat.ChatMessage{{
			Role:    "user",
			Content: "hello",
		}},
	}

	stripped, modified := StripImagesFromChat(req)

	if modified {
		t.Fatal("modified = true, want false for text-only request")
	}
	if got := stripped.Messages[0].Content.(string); got != "hello" {
		t.Errorf("content = %q, want %q (string content untouched)", got, "hello")
	}
}

func TestStripImagesFromChat_MultipleImagesAcrossTurns(t *testing.T) {
	req := chat.ChatRequest{
		Model: "qwen-vl-plus",
		Messages: []chat.ChatMessage{
			{
				Role: "user",
				Content: []chat.ContentPart{
					{Type: "text", Text: "first turn"},
					{Type: "image_url", ImageURL: &chat.ImageURL{URL: "data:image/png;base64,AAA"}},
				},
			},
			{
				Role: "assistant",
				Content: []chat.ContentPart{
					{Type: "text", Text: "ack"},
				},
			},
			{
				Role: "user",
				Content: []chat.ContentPart{
					{Type: "image_url", ImageURL: &chat.ImageURL{URL: "data:image/png;base64,BBB"}},
					{Type: "text", Text: "and this one?"},
				},
			},
		},
	}

	stripped, _ := StripImagesFromChat(req)

	// Indexes must continue across messages so the model can reference each
	// stripped attachment uniquely (Image #1, Image #2, ...).
	p0 := stripped.Messages[0].Content.([]chat.ContentPart)
	p2 := stripped.Messages[2].Content.([]chat.ContentPart)
	if !strings.Contains(p0[1].Text, "Image #1") {
		t.Errorf("first stripped placeholder = %q, want Image #1", p0[1].Text)
	}
	if !strings.Contains(p2[0].Text, "Image #2") {
		t.Errorf("second stripped placeholder = %q, want Image #2", p2[0].Text)
	}
}

func TestStripImagesFromChat_DoesNotMutateInput(t *testing.T) {
	original := chat.ChatRequest{
		Model: "qwen-vl-plus",
		Messages: []chat.ChatMessage{{
			Role: "user",
			Content: []chat.ContentPart{
				{Type: "image_url", ImageURL: &chat.ImageURL{URL: "data:image/png;base64,AAA"}},
			},
		}},
	}
	originalParts := original.Messages[0].Content.([]chat.ContentPart)

	_, _ = StripImagesFromChat(original)

	if originalParts[0].Type != "image_url" {
		t.Fatal("StripImagesFromChat mutated the caller's slice — must operate on a copy")
	}
}
