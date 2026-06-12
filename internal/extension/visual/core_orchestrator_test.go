package visual

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"moonbridge/internal/format"
)

// fakeCoreUpstream implements CoreProvider for testing the core orchestrator.
type fakeCoreUpstream struct {
	responses []*format.CoreResponse
	requests  []*format.CoreRequest
}

func (f *fakeCoreUpstream) CreateCore(_ context.Context, req *format.CoreRequest) (*format.CoreResponse, error) {
	f.requests = append(f.requests, cloneCoreRequest(req))
	if len(f.responses) == 0 {
		return nil, io.EOF
	}
	resp := f.responses[0]
	f.responses = f.responses[1:]
	return resp, nil
}

// fakeCoreVision implements CoreProvider for testing the vision client.
type fakeCoreVision struct {
	requests []*format.CoreRequest
	text     string // canned response text
}

func (f *fakeCoreVision) CreateCore(_ context.Context, req *format.CoreRequest) (*format.CoreResponse, error) {
	f.requests = append(f.requests, cloneCoreRequest(req))
	return &format.CoreResponse{
		ID:     "vision_resp",
		Status: "completed",
		Messages: []format.CoreMessage{
			{
				Role: "assistant",
				Content: []format.CoreContentBlock{
					{Type: "text", Text: f.text},
				},
			},
		},
	}, nil
}

type fakeCoreVisionClient struct {
	requests []AnalysisRequest
	text     string
}

func (f *fakeCoreVisionClient) Analyze(_ context.Context, req AnalysisRequest) (string, error) {
	f.requests = append(f.requests, req)
	return f.text, nil
}

type mutatingCoreUpstream struct {
	responses []*format.CoreResponse
	requests  []*format.CoreRequest
}

func (m *mutatingCoreUpstream) CreateCore(_ context.Context, req *format.CoreRequest) (*format.CoreResponse, error) {
	m.requests = append(m.requests, cloneCoreRequest(req))
	if len(req.Messages) > 0 && len(req.Messages[len(req.Messages)-1].Content) > 0 {
		req.Messages[len(req.Messages)-1].Content = append(
			req.Messages[len(req.Messages)-1].Content,
			format.CoreContentBlock{Type: "text", Text: "upstream-side mutation"},
		)
	}
	if len(m.responses) == 0 {
		return nil, io.EOF
	}
	resp := m.responses[0]
	m.responses = m.responses[1:]
	return resp, nil
}

// ============================================================================
// BridgeCoreClient tests
// ============================================================================

func TestCoreSource(t *testing.T) {
	for name, tc := range map[string]struct {
		image         ImageInput
		wantMediaType string
		wantHasSource bool
	}{
		"http url": {
			image:         ImageInput{URL: " https://example.test/image.png "},
			wantMediaType: "",
			wantHasSource: true,
		},
		"data url": {
			image:         ImageInput{Data: "data:image/jpeg;base64,abc"},
			wantMediaType: "image/jpeg",
			wantHasSource: true,
		},
		"base64 with mime": {
			image:         ImageInput{Data: "abc", MimeType: "image/jpeg"},
			wantMediaType: "image/jpeg",
			wantHasSource: true,
		},
		"base64 default mime": {
			image:         ImageInput{Data: "abc"},
			wantMediaType: "image/png",
			wantHasSource: true,
		},
		"attachment label returns nil": {
			image:         ImageInput{URL: "Image #1"},
			wantHasSource: false,
		},
		"empty data returns nil": {
			image:         ImageInput{},
			wantHasSource: false,
		},
	} {
		t.Run(name, func(t *testing.T) {
			source := tc.image.CoreSource()
			if !tc.wantHasSource {
				if source != nil {
					t.Fatalf("CoreSource() = %+v, want nil", source)
				}
				return
			}
			if source == nil {
				t.Fatalf("CoreSource() = nil, want non-nil")
			}
			if source.Type != "image" {
				t.Fatalf("CoreSource().Type = %q, want \"image\"", source.Type)
			}
			if source.MediaType != tc.wantMediaType {
				t.Fatalf("CoreSource().MediaType = %q, want %q", source.MediaType, tc.wantMediaType)
			}
			if source.ImageData == "" {
				t.Fatal("CoreSource().ImageData: empty")
			}
		})
	}
}

func TestBridgeCoreClientAnalyze(t *testing.T) {
	vision := &fakeCoreVision{text: "a scenic mountain view"}
	client := NewBridgeCoreClient(BridgeCoreConfig{
		Provider:  vision,
		Model:     "kimi-for-coding",
		MaxTokens: 512,
	})

	text, err := client.Analyze(context.Background(), AnalysisRequest{
		Tool:   ToolVisualBrief,
		Prompt: "describe this image",
		Images: []ImageInput{{URL: "https://example.test/a.png"}},
	})
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if text != "a scenic mountain view" {
		t.Fatalf("Analyze() = %q", text)
	}
	if len(vision.requests) != 1 {
		t.Fatalf("vision provider requests = %d, want 1", len(vision.requests))
	}
	req := vision.requests[0]
	if req.Model != "kimi-for-coding" || req.MaxTokens != 512 {
		t.Fatalf("model/max = %s/%d", req.Model, req.MaxTokens)
	}
	if len(req.Messages) != 1 || len(req.Messages[0].Content) != 2 {
		t.Fatalf("messages = %+v", req.Messages)
	}
	imageBlock := req.Messages[0].Content[1]
	if imageBlock.Type != "image" || imageBlock.ImageData != "https://example.test/a.png" {
		t.Fatalf("image block = %+v", imageBlock)
	}
}

func TestCoreTextFromResponse(t *testing.T) {
	tests := []struct {
		name string
		resp *format.CoreResponse
		want string
	}{
		{
			name: "multiple text blocks in one message",
			resp: &format.CoreResponse{
				Messages: []format.CoreMessage{{
					Role: "assistant",
					Content: []format.CoreContentBlock{
						{Type: "text", Text: "hello"},
						{Type: "text", Text: "world"},
					},
				}},
			},
			want: "hello\nworld",
		},
		{
			name: "filters out non-text blocks",
			resp: &format.CoreResponse{
				Messages: []format.CoreMessage{{
					Role: "assistant",
					Content: []format.CoreContentBlock{
						{Type: "tool_use", ToolName: "lookup"},
						{Type: "text", Text: "result text"},
					},
				}},
			},
			want: "result text",
		},
		{
			name: "nil response returns empty",
			resp: nil,
			want: "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := coreTextFromResponse(tc.resp)
			if got != tc.want {
				t.Fatalf("coreTextFromResponse() = %q, want %q", got, tc.want)
			}
		})
	}
}

// ============================================================================
// CoreOrchestrator tests
// ============================================================================

func TestCoreOrchestratorExecutesVisualBrief(t *testing.T) {
	upstream := &fakeCoreUpstream{
		responses: []*format.CoreResponse{
			{
				ID: "msg_tool", Status: "completed", StopReason: "tool_use",
				Messages: []format.CoreMessage{{
					Role: "assistant",
					Content: []format.CoreContentBlock{{
						Type: "tool_use", ToolUseID: "toolu_visual", ToolName: ToolVisualBrief,
						ToolInput: json.RawMessage(`{"image_urls":["https://example.test/a.png"],"context":"inspect UI"}`),
					}},
				}},
			},
			{
				ID: "msg_final", Status: "completed", StopReason: "end_turn",
				Messages: []format.CoreMessage{{
					Role:    "assistant",
					Content: []format.CoreContentBlock{{Type: "text", Text: "done"}},
				}},
			},
		},
	}
	vision := &fakeCoreVisionClient{text: "a clean UI layout with centered text"}
	orchestrator := NewCoreOrchestrator(CoreOrchestratorConfig{
		Upstream:  upstream,
		Client:    vision,
		MaxRounds: 3,
	})

	resp, err := orchestrator.CreateCore(context.Background(), &format.CoreRequest{
		Model:      "test-model",
		ToolChoice: &format.CoreToolChoice{Mode: "auto"},
	})
	if err != nil {
		t.Fatalf("CreateCore() error = %v", err)
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
}

func TestCoreOrchestratorPreparesRequest(t *testing.T) {
	upstream := &fakeCoreUpstream{
		responses: []*format.CoreResponse{
			{
				ID: "msg_tool", Status: "completed", StopReason: "tool_use",
				Messages: []format.CoreMessage{{
					Role: "assistant",
					Content: []format.CoreContentBlock{{
						Type: "tool_use", ToolUseID: "toolu_v", ToolName: ToolVisualBrief,
						ToolInput: json.RawMessage(`{"image_refs":["Image #1"],"context":"test"}`),
					}},
				}},
			},
			{
				ID: "msg_final", Status: "completed", StopReason: "end_turn",
				Messages: []format.CoreMessage{{
					Role:    "assistant",
					Content: []format.CoreContentBlock{{Type: "text", Text: "ok"}},
				}},
			},
		},
	}
	vision := &fakeCoreVisionClient{text: "visual result"}
	orchestrator := NewCoreOrchestrator(CoreOrchestratorConfig{
		Upstream:  upstream,
		Client:    vision,
		MaxRounds: 3,
	})

	_, err := orchestrator.CreateCore(context.Background(), &format.CoreRequest{
		Model: "test-model",
		Messages: []format.CoreMessage{{
			Role: "user",
			Content: []format.CoreContentBlock{
				{Type: "text", Text: "what is in this image?"},
				{Type: "image", ImageData: "abc123", MediaType: "image/png"},
			},
		}},
	})
	if err != nil {
		t.Fatalf("CreateCore() error = %v", err)
	}
	if len(vision.requests) != 1 || len(vision.requests[0].Images) != 1 {
		t.Fatalf("vision requests = %+v", vision.requests)
	}
	image := vision.requests[0].Images[0]
	if image.Data != "abc123" || image.MimeType != "image/png" {
		t.Fatalf("resolved image = %+v", image)
	}
	firstMsgContent := upstream.requests[0].Messages[0].Content
	for _, block := range firstMsgContent {
		if block.Type == "image" {
			t.Fatalf("upstream received image block: %+v", firstMsgContent)
		}
	}
	if !strings.Contains(firstMsgContent[1].Text, "Image #1") {
		t.Fatalf("attachment notice missing: %+v", firstMsgContent)
	}
}

func TestCoreOrchestratorLeavesNonVisualToolUse(t *testing.T) {
	upstream := &fakeCoreUpstream{
		responses: []*format.CoreResponse{
			{
				ID: "msg_tool", Status: "completed", StopReason: "tool_use",
				Messages: []format.CoreMessage{{
					Role: "assistant",
					Content: []format.CoreContentBlock{{
						Type: "tool_use", ToolUseID: "toolu_lookup", ToolName: "lookup",
						ToolInput: json.RawMessage(`{"id":"1"}`),
					}},
				}},
			},
		},
	}
	vision := &fakeCoreVisionClient{}
	orchestrator := NewCoreOrchestrator(CoreOrchestratorConfig{
		Upstream:  upstream,
		Client:    vision,
		MaxRounds: 3,
	})

	resp, err := orchestrator.CreateCore(context.Background(), &format.CoreRequest{
		Model: "test-model",
	})
	if err != nil {
		t.Fatalf("CreateCore() error = %v", err)
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

func TestCoreOrchestratorMaxRounds(t *testing.T) {
	// Upstream that always returns tool_use, causing the orchestrator to loop indefinitely.
	toolUseResp := &format.CoreResponse{
		ID: "msg_1", Status: "completed", StopReason: "tool_use",
		Messages: []format.CoreMessage{{
			Role: "assistant",
			Content: []format.CoreContentBlock{{
				Type: "tool_use", ToolUseID: "toolu_v", ToolName: ToolVisualBrief,
				ToolInput: json.RawMessage(`{"image_urls":["https://example.test/a.png"]}`),
			}},
		}},
	}
	upstream := &fakeCoreUpstream{}
	// Fill responses with enough tool_use responses to exceed maxRounds.
	for i := 0; i < 10; i++ {
		upstream.responses = append(upstream.responses, toolUseResp)
	}
	vision := &fakeCoreVisionClient{text: "result"}
	orchestrator := NewCoreOrchestrator(CoreOrchestratorConfig{
		Upstream:  upstream,
		Client:    vision,
		MaxRounds: 1, // maxRounds=1 means at most 1 iteration.
	})

	_, err := orchestrator.CreateCore(context.Background(), &format.CoreRequest{
		Model: "test-model",
	})
	if err == nil || !strings.Contains(err.Error(), "exceeded max rounds") {
		t.Fatalf("expected max rounds error, got: %v", err)
	}
	if len(upstream.requests) != 1 {
		t.Fatalf("upstream requests = %d, want 1", len(upstream.requests))
	}
}

func TestCoreOrchestratorNilClient(t *testing.T) {
	upstream := &fakeCoreUpstream{}
	orchestrator := NewCoreOrchestrator(CoreOrchestratorConfig{
		Upstream:  upstream,
		Client:    nil,
		MaxRounds: 2,
	})

	_, err := orchestrator.CreateCore(context.Background(), &format.CoreRequest{
		Model: "test-model",
	})
	if err == nil || !strings.Contains(err.Error(), "visual client is nil") {
		t.Fatalf("expected visual client error, got: %v", err)
	}
	if len(upstream.requests) != 0 {
		t.Fatalf("upstream requests = %d, want 0", len(upstream.requests))
	}
}

func TestCoreOrchestratorAggregatesUsageAcrossRounds(t *testing.T) {
	upstream := &fakeCoreUpstream{
		responses: []*format.CoreResponse{
			{
				ID: "msg_tool_1", Status: "completed", StopReason: "tool_use",
				Messages: []format.CoreMessage{{
					Role: "assistant",
					Content: []format.CoreContentBlock{{
						Type: "tool_use", ToolUseID: "toolu_1", ToolName: ToolVisualBrief,
						ToolInput: json.RawMessage(`{"image_urls":["https://example.test/a.png"]}`),
					}},
				}},
				Usage: format.CoreUsage{
					InputTokens:       10,
					OutputTokens:      4,
					CachedInputTokens: 3,
				},
			},
			{
				ID: "msg_tool_2", Status: "completed", StopReason: "tool_use",
				Messages: []format.CoreMessage{{
					Role: "assistant",
					Content: []format.CoreContentBlock{{
						Type: "tool_use", ToolUseID: "toolu_2", ToolName: ToolVisualQA,
						ToolInput: json.RawMessage(`{"question":"what color?"}`),
					}},
				}},
				Usage: format.CoreUsage{
					InputTokens:       8,
					OutputTokens:      5,
					CachedInputTokens: 2,
				},
			},
			{
				ID: "msg_final", Status: "completed", StopReason: "end_turn",
				Messages: []format.CoreMessage{{
					Role:    "assistant",
					Content: []format.CoreContentBlock{{Type: "text", Text: "done"}},
				}},
				Usage: format.CoreUsage{
					InputTokens:       6,
					OutputTokens:      7,
					CachedInputTokens: 1,
				},
			},
		},
	}
	vision := &fakeCoreVisionClient{text: "visual result"}
	orchestrator := NewCoreOrchestrator(CoreOrchestratorConfig{
		Upstream:  upstream,
		Client:    vision,
		MaxRounds: 5,
	})

	resp, err := orchestrator.CreateCore(context.Background(), &format.CoreRequest{
		Model: "test-model",
	})
	if err != nil {
		t.Fatalf("CreateCore() error = %v", err)
	}
	if resp == nil {
		t.Fatal("CreateCore() returned nil response")
	}
	if resp.Usage.InputTokens != 24 {
		t.Fatalf("usage input_tokens = %d, want 24", resp.Usage.InputTokens)
	}
	if resp.Usage.OutputTokens != 16 {
		t.Fatalf("usage output_tokens = %d, want 16", resp.Usage.OutputTokens)
	}
	if resp.Usage.CachedInputTokens != 6 {
		t.Fatalf("usage cached_input_tokens = %d, want 6", resp.Usage.CachedInputTokens)
	}
	if resp.Usage.TotalTokens != 40 {
		t.Fatalf("usage total_tokens = %d, want 40", resp.Usage.TotalTokens)
	}
}

// ============================================================================
// Utility tests
// ============================================================================

func TestCoreSplitVisualToolUses(t *testing.T) {
	visual, nonVisual := coreSplitVisualToolUses([]format.CoreContentBlock{
		{Type: "text", Text: "hello"},
		{Type: "tool_use", ToolName: ToolVisualBrief, ToolUseID: "1"},
		{Type: "tool_use", ToolName: "lookup", ToolUseID: "2"},
		{Type: "tool_use", ToolName: ToolVisualQA, ToolUseID: "3"},
	})
	if len(visual) != 2 {
		t.Fatalf("visualUses = %d, want 2", len(visual))
	}
	if len(nonVisual) != 1 {
		t.Fatalf("nonVisualToolUses = %d, want 1", len(nonVisual))
	}
}

func TestFindLastAssistantMessage(t *testing.T) {
	msgs := []format.CoreMessage{
		{Role: "user", Content: []format.CoreContentBlock{{Type: "text", Text: "hi"}}},
		{Role: "assistant", Content: []format.CoreContentBlock{{Type: "text", Text: "hello"}}},
		{Role: "user", Content: []format.CoreContentBlock{{Type: "text", Text: "again"}}},
	}
	last := findLastAssistantMessage(msgs)
	if last == nil {
		t.Fatal("findLastAssistantMessage returned nil")
	}
	if last.Content[0].Text != "hello" {
		t.Fatalf("last = %+v", last)
	}
	msgs2 := []format.CoreMessage{{Role: "user"}}
	if last := findLastAssistantMessage(msgs2); last != nil {
		t.Fatalf("expected nil, got %+v", last)
	}
}

func TestImageInputFromCoreBlock(t *testing.T) {
	tests := []struct {
		name     string
		block    format.CoreContentBlock
		wantOK   bool
		wantURL  string
		wantData string
	}{
		{
			name:    "url-based with empty media type",
			block:   format.CoreContentBlock{Type: "image", ImageData: "https://example.test/a.png"},
			wantOK:  true,
			wantURL: "https://example.test/a.png",
		},
		{
			name:     "base64 with media type",
			block:    format.CoreContentBlock{Type: "image", ImageData: "abc", MediaType: "image/png"},
			wantOK:   true,
			wantData: "abc",
		},
		{
			name:   "empty image data",
			block:  format.CoreContentBlock{Type: "image", ImageData: ""},
			wantOK: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := imageInputFromCoreBlock(tc.block)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && got.URL != tc.wantURL {
				t.Fatalf("URL = %q, want %q", got.URL, tc.wantURL)
			}
			if ok && got.Data != tc.wantData {
				t.Fatalf("Data = %q, want %q", got.Data, tc.wantData)
			}
		})
	}
}

// ============================================================================
// Regression tests: Core-level image stripping (visual leak fix)
// ============================================================================

func TestPrepareCoreRequestForVisual_StripsBase64(t *testing.T) {
	req := &format.CoreRequest{
		Model: "test-model",
		Messages: []format.CoreMessage{{
			Role: "user",
			Content: []format.CoreContentBlock{
				{Type: "text", Text: "描述图片"},
				{Type: "image", ImageData: "verylongbase64data", MediaType: "image/png"},
			},
		}},
	}

	stripped, _ := prepareCoreRequestForVisual(req)

	for _, msg := range stripped.Messages {
		for _, block := range msg.Content {
			if block.Type == "image" {
				t.Fatal("prepareCoreRequestForVisual: image block still present after stripping")
			}
		}
	}
	foundPlaceholder := false
	for _, msg := range stripped.Messages {
		for _, block := range msg.Content {
			if block.Type == "text" && strings.Contains(block.Text, "Image #1") {
				foundPlaceholder = true
			}
		}
	}
	if !foundPlaceholder {
		t.Fatal("prepareCoreRequestForVisual: no Image #1 placeholder found")
	}
}

func TestPrepareCoreRequestForVisual_TextOnlyUnchanged(t *testing.T) {
	req := &format.CoreRequest{
		Model: "test-model",
		Messages: []format.CoreMessage{{
			Role:    "user",
			Content: []format.CoreContentBlock{{Type: "text", Text: "hello"}},
		}},
	}

	stripped, images := prepareCoreRequestForVisual(req)

	if len(images) != 0 {
		t.Fatalf("prepareCoreRequestForVisual: got %d images for text-only request, want 0", len(images))
	}
	if len(stripped.Messages) != 1 || stripped.Messages[0].Content[0].Text != "hello" {
		t.Fatal("prepareCoreRequestForVisual: text-only request was modified")
	}
}

func TestPrepareCoreRequestForVisual_MixedContent(t *testing.T) {
	req := &format.CoreRequest{
		Model: "test-model",
		Messages: []format.CoreMessage{{
			Role: "user",
			Content: []format.CoreContentBlock{
				{Type: "text", Text: "a"},
				{Type: "image", ImageData: "b64_1", MediaType: "image/png"},
				{Type: "text", Text: "b"},
				{Type: "image", ImageData: "b64_2", MediaType: "image/jpeg"},
			},
		}},
	}

	stripped, images := prepareCoreRequestForVisual(req)

	if len(images) != 2 {
		t.Fatalf("prepareCoreRequestForVisual: got %d images, want 2", len(images))
	}
	blockCount := len(stripped.Messages[0].Content)
	if blockCount != 4 {
		t.Fatalf("expected 4 blocks (2 text + 2 placeholders), got %d", blockCount)
	}
	for _, block := range stripped.Messages[0].Content {
		if block.Type == "image" {
			t.Fatal("image block still present")
		}
	}
}

func TestPrepareCoreRequestForVisual_StripsImagesInsideToolResults(t *testing.T) {
	req := &format.CoreRequest{
		Model: "test-model",
		Messages: []format.CoreMessage{{
			Role: "tool",
			Content: []format.CoreContentBlock{{
				Type:      "tool_result",
				ToolUseID: "call_view",
				ToolResultContent: []format.CoreContentBlock{
					{Type: "image", ImageData: "abc123", MediaType: "image/jpeg"},
				},
			}},
		}},
	}

	stripped, images := prepareCoreRequestForVisual(req)

	if len(images) != 1 {
		t.Fatalf("images = %d, want 1", len(images))
	}
	if images[0].Data != "abc123" || images[0].MimeType != "image/jpeg" {
		t.Fatalf("image = %+v", images[0])
	}
	toolResult := stripped.Messages[0].Content[0]
	if toolResult.Type != "tool_result" {
		t.Fatalf("content = %+v", stripped.Messages[0].Content)
	}
	if len(toolResult.ToolResultContent) != 1 || toolResult.ToolResultContent[0].Type != "text" {
		t.Fatalf("tool result content = %+v", toolResult.ToolResultContent)
	}
	if !strings.Contains(toolResult.ToolResultContent[0].Text, "Image #1") {
		t.Fatalf("placeholder missing: %+v", toolResult.ToolResultContent[0])
	}
}

// ============================================================================
// Multi-turn visual orchestration test (regression: strip across rounds)
// ============================================================================

func TestCoreOrchestrator_ImageStrippedAcrossMultipleTurns(t *testing.T) {
	// Simulate a multi-turn conversation where images are in the FIRST user message.
	// After each visual tool round, the orchestrator appends assistant + tool_result
	// messages. On subsequent rounds, the upstream should NEVER see an image block.

	upstream := &fakeCoreUpstream{
		responses: []*format.CoreResponse{
			// Turn 1: upstream calls VisualBrief
			{
				ID: "turn1", Status: "completed", StopReason: "tool_use",
				Messages: []format.CoreMessage{{
					Role: "assistant",
					Content: []format.CoreContentBlock{{
						Type: "tool_use", ToolUseID: "toolu_1", ToolName: ToolVisualBrief,
						ToolInput: json.RawMessage(`{"image_refs":["Image #1"],"context":"describe"}`),
					}},
				}},
			},
			// Turn 2: upstream calls VisualQA (follow-up question about the same image)
			{
				ID: "turn2", Status: "completed", StopReason: "tool_use",
				Messages: []format.CoreMessage{{
					Role: "assistant",
					Content: []format.CoreContentBlock{{
						Type: "tool_use", ToolUseID: "toolu_2", ToolName: ToolVisualQA,
						ToolInput: json.RawMessage(`{"question":"color?","image_refs":["Image #1"]}`),
					}},
				}},
			},
			// Turn 3: final text response
			{
				ID: "turn3", Status: "completed", StopReason: "end_turn",
				Messages: []format.CoreMessage{{
					Role:    "assistant",
					Content: []format.CoreContentBlock{{Type: "text", Text: "final answer"}},
				}},
			},
		},
	}
	vision := &fakeCoreVisionClient{text: "visual analysis result"}
	orchestrator := NewCoreOrchestrator(CoreOrchestratorConfig{
		Upstream:  upstream,
		Client:    vision,
		MaxRounds: 5,
	})

	// First request with 2 images attached
	_, err := orchestrator.CreateCore(context.Background(), &format.CoreRequest{
		Model: "test-model",
		Messages: []format.CoreMessage{{
			Role: "user",
			Content: []format.CoreContentBlock{
				{Type: "text", Text: "描述这些图片"},
				{Type: "image", ImageData: "b64_image_1", MediaType: "image/png"},
				{Type: "image", ImageData: "b64_image_2", MediaType: "image/jpeg"},
			},
		}},
	})
	if err != nil {
		t.Fatalf("CreateCore() error = %v", err)
	}

	// Verify: ALL upstream requests (3 turns) received NO image blocks
	for i, req := range upstream.requests {
		for _, msg := range req.Messages {
			for _, block := range msg.Content {
				if block.Type == "image" {
					t.Fatalf("turn %d: upstream received image block! Messages=%+v", i+1, req.Messages)
				}
			}
		}
	}

	// Verify: all 3 turns were executed
	if len(upstream.requests) != 3 {
		t.Fatalf("upstream requests = %d, want 3", len(upstream.requests))
	}

	// Verify: vision client received 2 requests (VisualBrief + VisualQA)
	if len(vision.requests) != 2 {
		t.Fatalf("vision requests = %d, want 2", len(vision.requests))
	}

	// Verify: both vision requests reference Image #1 (model chose to use only one)
	for i, req := range vision.requests {
		if len(req.Images) != 1 {
			t.Fatalf("vision request %d: images = %d, want 1", i, len(req.Images))
		}
		if req.Images[0].Data != "b64_image_1" {
			t.Fatalf("vision request %d: wrong image data: %+v", i, req.Images)
		}
	}

	// Verify: first upstream request has text placeholders instead of images
	firstMsg := upstream.requests[0].Messages[0]
	placeholderCount := 0
	for _, block := range firstMsg.Content {
		if block.Type == "text" && (block.Text == "[Image #1 is available..." ||
			block.Text == "[Image #2 is available..." ||
			block.Text == "[Image #1 is available to Visual Brief and Visual QA. Use image_refs [\"Image #1\"] or omit image fields to analyze attached images.]" ||
			block.Text == "[Image #2 is available to Visual Brief and Visual QA. Use image_refs [\"Image #2\"] or omit image fields to analyze attached images.]") {
			placeholderCount++
		}
	}
	if placeholderCount != 2 {
		t.Fatalf("expected 2 text placeholders in first upstream request, got %d. Content=%+v",
			placeholderCount, firstMsg.Content)
	}
}

func TestCoreOrchestratorIsolatesRequestsAcrossRounds(t *testing.T) {
	upstream := &mutatingCoreUpstream{
		responses: []*format.CoreResponse{
			{
				ID: "turn1", Status: "completed", StopReason: "tool_use",
				Messages: []format.CoreMessage{{
					Role: "assistant",
					Content: []format.CoreContentBlock{
						{
							Type: "tool_use", ToolUseID: "toolu_1", ToolName: ToolVisualBrief,
							ToolInput: json.RawMessage(`{"image_refs":["Image #1"],"context":"describe"}`),
						},
					},
				}},
			},
			{
				ID: "turn2", Status: "completed", StopReason: "tool_use",
				Messages: []format.CoreMessage{{
					Role: "assistant",
					Content: []format.CoreContentBlock{
						{
							Type: "tool_use", ToolUseID: "toolu_2", ToolName: ToolVisualQA,
							ToolInput: json.RawMessage(`{"question":"what changed?","image_refs":["Image #1"]}`),
						},
					},
				}},
			},
			{
				ID: "turn3", Status: "completed", StopReason: "end_turn",
				Messages: []format.CoreMessage{{
					Role: "assistant",
					Content: []format.CoreContentBlock{
						{Type: "text", Text: "final answer"},
					},
				}},
			},
		},
	}
	vision := &fakeCoreVisionClient{text: "visual analysis result"}
	orchestrator := NewCoreOrchestrator(CoreOrchestratorConfig{
		Upstream:  upstream,
		Client:    vision,
		MaxRounds: 5,
	})

	_, err := orchestrator.CreateCore(context.Background(), &format.CoreRequest{
		Model: "test-model",
		Messages: []format.CoreMessage{{
			Role: "user",
			Content: []format.CoreContentBlock{
				{Type: "text", Text: "look at this"},
				{Type: "image", ImageData: "b64_image_1", MediaType: "image/png"},
			},
		}},
	})
	if err != nil {
		t.Fatalf("CreateCore() error = %v", err)
	}

	if len(upstream.requests) != 3 {
		t.Fatalf("upstream requests = %d, want 3", len(upstream.requests))
	}

	secondReq := upstream.requests[1]
	if len(secondReq.Messages) != 3 {
		t.Fatalf("second upstream request messages = %d, want 3; got %+v", len(secondReq.Messages), secondReq.Messages)
	}

	assistantMsg := secondReq.Messages[1]
	if assistantMsg.Role != "assistant" {
		t.Fatalf("second request assistant role = %q", assistantMsg.Role)
	}
	if len(assistantMsg.Content) != 1 || assistantMsg.Content[0].Type != "tool_use" || assistantMsg.Content[0].ToolUseID != "toolu_1" {
		t.Fatalf("second request assistant content = %+v", assistantMsg.Content)
	}

	toolResultMsg := secondReq.Messages[2]
	if toolResultMsg.Role != "tool" {
		t.Fatalf("tool_result message role = %q", toolResultMsg.Role)
	}
	if len(toolResultMsg.Content) != 1 || toolResultMsg.Content[0].Type != "tool_result" || toolResultMsg.Content[0].ToolUseID != "toolu_1" {
		t.Fatalf("tool_result message content = %+v", toolResultMsg.Content)
	}
}

type finalizingCoreUpstream struct {
	responses []*format.CoreResponse
	requests  []*format.CoreRequest
	finalize  func(*format.CoreRequest)
}

func (f *finalizingCoreUpstream) CreateCore(_ context.Context, req *format.CoreRequest) (*format.CoreResponse, error) {
	cloned := cloneCoreRequest(req)
	if f.finalize != nil {
		f.finalize(cloned)
	}
	f.requests = append(f.requests, cloned)
	if len(f.responses) == 0 {
		return nil, io.EOF
	}
	resp := f.responses[0]
	f.responses = f.responses[1:]
	return resp, nil
}

func TestCoreOrchestratorPreservesFinalizedAssistantHistoryAcrossRounds(t *testing.T) {
	upstream := &finalizingCoreUpstream{
		finalize: func(req *format.CoreRequest) {
			for i := range req.Messages {
				msg := &req.Messages[i]
				if msg.Role != "assistant" || len(msg.Content) == 0 {
					continue
				}
				hasToolUse := false
				hasReasoning := false
				for _, block := range msg.Content {
					if block.Type == "tool_use" {
						hasToolUse = true
					}
					if block.Type == "reasoning" {
						hasReasoning = true
					}
				}
				if hasToolUse && !hasReasoning {
					msg.Content = append([]format.CoreContentBlock{{
						Type:               "reasoning",
						ReasoningText:      "replayed thinking",
						ReasoningSignature: "sig-visual",
					}}, msg.Content...)
				}
			}
		},
		responses: []*format.CoreResponse{
			{
				ID: "turn1", Status: "completed", StopReason: "tool_use",
				Messages: []format.CoreMessage{{
					Role: "assistant",
					Content: []format.CoreContentBlock{{
						Type: "tool_use", ToolUseID: "toolu_1", ToolName: ToolVisualBrief,
						ToolInput: json.RawMessage(`{"image_refs":["Image #1"],"context":"describe"}`),
					}},
				}},
			},
			{
				ID: "turn2", Status: "completed", StopReason: "end_turn",
				Messages: []format.CoreMessage{{
					Role:    "assistant",
					Content: []format.CoreContentBlock{{Type: "text", Text: "done"}},
				}},
			},
		},
	}
	vision := &fakeCoreVisionClient{text: "visual analysis result"}
	orchestrator := NewCoreOrchestrator(CoreOrchestratorConfig{
		Upstream:  upstream,
		Client:    vision,
		MaxRounds: 5,
	})

	_, err := orchestrator.CreateCore(context.Background(), &format.CoreRequest{
		Model: "test-model",
		Messages: []format.CoreMessage{{
			Role: "user",
			Content: []format.CoreContentBlock{
				{Type: "text", Text: "look"},
				{Type: "image", ImageData: "b64_image_1", MediaType: "image/png"},
			},
		}},
	})
	if err != nil {
		t.Fatalf("CreateCore() error = %v", err)
	}

	if len(upstream.requests) != 2 {
		t.Fatalf("upstream requests = %d, want 2", len(upstream.requests))
	}
	secondReq := upstream.requests[1]
	if len(secondReq.Messages) != 3 {
		t.Fatalf("second upstream request messages = %d, want 3; got %+v", len(secondReq.Messages), secondReq.Messages)
	}
	assistantMsg := secondReq.Messages[1]
	if len(assistantMsg.Content) < 2 {
		t.Fatalf("assistant history content too short: %+v", assistantMsg.Content)
	}
	if assistantMsg.Content[0].Type != "reasoning" || assistantMsg.Content[0].ReasoningText != "replayed thinking" || assistantMsg.Content[0].ReasoningSignature != "sig-visual" {
		t.Fatalf("assistant history lost finalized reasoning block: %+v", assistantMsg.Content)
	}
	if assistantMsg.Content[1].Type != "tool_use" || assistantMsg.Content[1].ToolUseID != "toolu_1" {
		t.Fatalf("assistant tool_use misplaced after finalized reasoning: %+v", assistantMsg.Content)
	}
}
