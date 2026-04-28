package visual

import "testing"

func TestToolsExposeVisualBriefAndQA(t *testing.T) {
	tools := Tools()
	if len(tools) != 2 {
		t.Fatalf("Tools() len = %d, want 2", len(tools))
	}
	if tools[0].Name != ToolVisualBrief || tools[1].Name != ToolVisualQA {
		t.Fatalf("tools = %+v", tools)
	}
	if !IsVisualTool(ToolVisualBrief) || !IsVisualTool(ToolVisualQA) {
		t.Fatal("IsVisualTool() did not recognize visual tools")
	}
	if IsVisualTool("lookup") {
		t.Fatal("IsVisualTool(lookup) = true, want false")
	}
}

func TestImageInputAnthropicSource(t *testing.T) {
	for name, tc := range map[string]struct {
		image         ImageInput
		wantType      string
		wantURL       string
		wantMediaType string
		wantData      string
	}{
		"url": {
			image:    ImageInput{URL: " https://example.test/image.png "},
			wantType: "url",
			wantURL:  "https://example.test/image.png",
		},
		"data url": {
			image:         ImageInput{Data: "data:image/jpeg;base64,abc"},
			wantType:      "base64",
			wantMediaType: "image/jpeg",
			wantData:      "abc",
		},
		"base64 with mime": {
			image:         ImageInput{Data: "abc", MimeType: "image/jpeg"},
			wantType:      "base64",
			wantMediaType: "image/jpeg",
			wantData:      "abc",
		},
		"base64 default mime": {
			image:         ImageInput{Data: "abc"},
			wantType:      "base64",
			wantMediaType: "image/png",
			wantData:      "abc",
		},
	} {
		t.Run(name, func(t *testing.T) {
			source := tc.image.AnthropicSource()
			if source == nil {
				t.Fatal("AnthropicSource() = nil")
			}
			if source.Type != tc.wantType || source.URL != tc.wantURL || source.MediaType != tc.wantMediaType || source.Data != tc.wantData {
				t.Fatalf("AnthropicSource() = %+v", source)
			}
		})
	}
}

func TestImageInputAnthropicSourceRejectsAttachmentLabelAsURL(t *testing.T) {
	if source := (ImageInput{URL: "Image #1"}).AnthropicSource(); source != nil {
		t.Fatalf("AnthropicSource() = %+v, want nil", source)
	}
}
