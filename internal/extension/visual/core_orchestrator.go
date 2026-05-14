package visual

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"log/slog"

	"moonbridge/internal/format"
)

// CoreUpstreamProvider wraps any CoreProvider to be used as the upstream
// (text-only model) in the visual orchestration loop.
type CoreUpstreamProvider interface {
	CreateCore(ctx context.Context, req *format.CoreRequest) (*format.CoreResponse, error)
}

// CoreOrchestrator implements the visual tool loop on protocol-agnostic Core types.
// It intercepts tool_use responses from the upstream model, routes visual tools
// (visual_brief, visual_qa) to the vision model, injects tool_results, and loops
// until the upstream model produces a non-tool_use response.
type CoreOrchestrator struct {
	upstream  CoreUpstreamProvider
	client    VisionClient
	maxRounds int
}

type CoreOrchestratorConfig struct {
	Upstream  CoreUpstreamProvider
	Client    VisionClient
	MaxRounds int
}

// NewCoreOrchestrator creates a CoreOrchestrator.
// If Client is nil, it falls back to creating a BridgeCoreClient from the config.
func NewCoreOrchestrator(cfg CoreOrchestratorConfig) *CoreOrchestrator {
	maxRounds := cfg.MaxRounds
	if maxRounds <= 0 {
		maxRounds = 4
	}
	return &CoreOrchestrator{
		upstream:  cfg.Upstream,
		client:    cfg.Client,
		maxRounds: maxRounds,
	}
}

// CreateCore runs the visual orchestration loop on Core types.
// It strips image blocks from messages, passes them to the upstream model,
// intercepts visual tool calls, executes them via the vision client, and loops.
func (o *CoreOrchestrator) CreateCore(ctx context.Context, req *format.CoreRequest) (*format.CoreResponse, error) {
	if o == nil || o.upstream == nil {
		return nil, fmt.Errorf("visual upstream provider is nil")
	}
	req, availableImages := prepareCoreRequestForVisual(req)
	log := slog.Default()

	for round := 0; round <= o.maxRounds; round++ {
		resp, err := o.upstream.CreateCore(ctx, req)
		if err != nil {
			return nil, err
		}
		if resp == nil {
			return nil, fmt.Errorf("upstream returned nil response")
		}

		// If not a tool_use stop, we're done.
		if resp.StopReason != "tool_use" {
			return resp, nil
		}

		// Find visual tool uses in the last assistant message.
		lastAssistant := findLastAssistantMessage(resp.Messages)
		if lastAssistant == nil {
			return resp, nil
		}

		toolUses, nonVisual := coreSplitVisualToolUses(lastAssistant.Content)
		if len(nonVisual) > 0 || len(toolUses) == 0 {
			return resp, nil
		}

		// Execute each visual tool via the vision client.
		toolResults := make([]format.CoreContentBlock, 0, len(toolUses))
		for _, toolUse := range toolUses {
			result := o.executeCoreVisualTool(ctx, toolUse, availableImages)
			toolResults = append(toolResults, format.CoreContentBlock{
				Type:              "tool_result",
				ToolUseID:         toolUse.ToolUseID,
				ToolResultContent: []format.CoreContentBlock{{Type: "text", Text: result}},
			})
		}

		// Append assistant message and tool_result message for next round.
		req.Messages = append(req.Messages, *lastAssistant)
		req.Messages = append(req.Messages, format.CoreMessage{
			Role:    "user",
			Content: toolResults,
		})

		// Reset tool_choice to auto for follow-up rounds.
		if req.ToolChoice != nil && req.ToolChoice.Mode != "auto" {
			req.ToolChoice = &format.CoreToolChoice{Mode: "auto"}
		}

		log.Debug("Core visual tool loop completed", "round", round+1, "tools_executed", len(toolUses))
	}

	return nil, fmt.Errorf("visual loop exceeded max rounds (%d)", o.maxRounds)
}

// prepareCoreRequestForVisual strips image blocks from the Core request and
// replaces them with text placeholders, returning the collected images.
func prepareCoreRequestForVisual(req *format.CoreRequest) (*format.CoreRequest, []ImageInput) {
	availableImages := make([]ImageInput, 0)
	for messageIndex := range req.Messages {
		content := req.Messages[messageIndex].Content
		if len(content) == 0 {
			continue
		}
		rewritten := make([]format.CoreContentBlock, 0, len(content))
		for _, block := range content {
			rewritten = append(rewritten, prepareCoreBlockForVisual(block, &availableImages))
		}
		req.Messages[messageIndex].Content = rewritten
	}
	return req, availableImages
}

func prepareCoreBlocksForVisual(blocks []format.CoreContentBlock, availableImages *[]ImageInput) []format.CoreContentBlock {
	rewritten := make([]format.CoreContentBlock, 0, len(blocks))
	for _, block := range blocks {
		rewritten = append(rewritten, prepareCoreBlockForVisual(block, availableImages))
	}
	return rewritten
}

func prepareCoreBlockForVisual(block format.CoreContentBlock, availableImages *[]ImageInput) format.CoreContentBlock {
	if block.Type == "tool_result" {
		block.ToolResultContent = prepareCoreBlocksForVisual(block.ToolResultContent, availableImages)
		return block
	}
	if block.Type != "image" {
		return block
	}
	image, ok := imageInputFromCoreBlock(block)
	if !ok {
		return format.CoreContentBlock{
			Type: "text",
			Text: "[Image was attached but could not be processed.]",
		}
	}
	*availableImages = append(*availableImages, image)
	return format.CoreContentBlock{
		Type: "text",
		Text: visualAttachmentText(len(*availableImages)),
	}
}

// imageInputFromCoreBlock converts a Core image content block to ImageInput.
func imageInputFromCoreBlock(block format.CoreContentBlock) (ImageInput, bool) {
	if block.ImageData == "" {
		return ImageInput{}, false
	}
	if block.MediaType != "" {
		// base64-encoded image
		return ImageInput{Data: block.ImageData, MimeType: block.MediaType}, true
	}
	// URL-based image (ImageData holds the URL when MediaType is empty)
	url := strings.TrimSpace(block.ImageData)
	if !isSupportedImageURL(url) {
		return ImageInput{}, false
	}
	return ImageInput{URL: url}, true
}

// findLastAssistantMessage finds the last assistant message in a slice.
func findLastAssistantMessage(messages []format.CoreMessage) *format.CoreMessage {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "assistant" {
			return &messages[i]
		}
	}
	return nil
}

// coreSplitVisualToolUses separates visual tool_use blocks from non-visual ones.
func coreSplitVisualToolUses(blocks []format.CoreContentBlock) (visualUses, nonVisualToolUses []format.CoreContentBlock) {
	for _, block := range blocks {
		if block.Type != "tool_use" {
			continue
		}
		if IsVisualTool(block.ToolName) {
			visualUses = append(visualUses, block)
		} else {
			nonVisualToolUses = append(nonVisualToolUses, block)
		}
	}
	return visualUses, nonVisualToolUses
}

// executeCoreVisualTool runs the vision model and returns a formatted result string.
func (o *CoreOrchestrator) executeCoreVisualTool(ctx context.Context, toolUse format.CoreContentBlock, availableImages []ImageInput) string {
	request, err := coreAnalysisRequestFromToolUse(toolUse, availableImages)
	if err != nil {
		return "Visual error: " + err.Error()
	}
	result, err := o.client.Analyze(ctx, request)
	if err != nil {
		slog.Default().Warn("Visual tool execution failed", "tool", toolUse.ToolName, "error", err)
		return "Visual error: " + err.Error()
	}
	slog.Default().Info("Visual tool executed", "tool", toolUse.ToolName, "images", len(request.Images))
	switch toolUse.ToolName {
	case ToolVisualBrief:
		return "Visual Brief result:\n" + strings.TrimSpace(result)
	case ToolVisualQA:
		return "Visual QA result:\n" + strings.TrimSpace(result)
	default:
		return strings.TrimSpace(result)
	}
}

// coreAnalysisRequestFromToolUse parses a tool_use CoreContentBlock into an
// AnalysisRequest for the vision client. The structure mirrors the old
// anthropic-specific version but uses CoreContentBlock fields.
func coreAnalysisRequestFromToolUse(toolUse format.CoreContentBlock, availableImages []ImageInput) (AnalysisRequest, error) {
	switch toolUse.ToolName {
	case ToolVisualBrief:
		var input briefInput
		if err := json.Unmarshal(toolUse.ToolInput, &input); err != nil {
			return AnalysisRequest{}, fmt.Errorf("parse visual_brief input: %w", err)
		}
		images := normalizeImages(input.ImageURL, input.ImageURLs, input.Images, input.ImageRefs, availableImages)
		if len(images) == 0 {
			return AnalysisRequest{}, fmt.Errorf("visual_brief requires valid image URLs/data/images or attached images")
		}
		return AnalysisRequest{
			Tool:   ToolVisualBrief,
			Prompt: buildBriefPrompt(input),
			Images: images,
		}, nil
	case ToolVisualQA:
		var input qaInput
		if err := json.Unmarshal(toolUse.ToolInput, &input); err != nil {
			return AnalysisRequest{}, fmt.Errorf("parse visual_qa input: %w", err)
		}
		if strings.TrimSpace(input.Question) == "" {
			return AnalysisRequest{}, fmt.Errorf("visual_qa requires question")
		}
		return AnalysisRequest{
			Tool:   ToolVisualQA,
			Prompt: buildQAPrompt(input),
			Images: normalizeImages(input.ImageURL, input.ImageURLs, input.Images, input.ImageRefs, availableImages),
		}, nil
	default:
		return AnalysisRequest{}, fmt.Errorf("unknown visual tool %q", toolUse.ToolName)
	}
}
