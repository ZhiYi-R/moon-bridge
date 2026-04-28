package visual

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"

	"moonbridge/internal/foundation/logger"
	"moonbridge/internal/protocol/anthropic"
)

type Provider interface {
	CreateMessage(context.Context, anthropic.MessageRequest) (anthropic.MessageResponse, error)
	StreamMessage(context.Context, anthropic.MessageRequest) (anthropic.Stream, error)
}

type OrchestratorConfig struct {
	Upstream  Provider
	Client    VisionClient
	Visual    ClientConfig
	MaxRounds int
}

type Orchestrator struct {
	upstream  Provider
	client    VisionClient
	maxRounds int
}

func NewOrchestrator(cfg OrchestratorConfig) *Orchestrator {
	client := cfg.Client
	if client == nil {
		client = NewBridgeClient(cfg.Visual)
	}
	maxRounds := cfg.MaxRounds
	if maxRounds <= 0 {
		maxRounds = 4
	}
	return &Orchestrator{
		upstream:  cfg.Upstream,
		client:    client,
		maxRounds: maxRounds,
	}
}

func WrapProvider(upstream Provider, visualProvider Provider, model string, maxRounds int, maxTokens int) Provider {
	return NewOrchestrator(OrchestratorConfig{
		Upstream:  upstream,
		Visual:    ClientConfig{Provider: visualProvider, Model: model, MaxTokens: maxTokens},
		MaxRounds: maxRounds,
	})
}

func (o *Orchestrator) CreateMessage(ctx context.Context, req anthropic.MessageRequest) (anthropic.MessageResponse, error) {
	if o == nil || o.upstream == nil {
		return anthropic.MessageResponse{}, fmt.Errorf("visual upstream provider is nil")
	}
	req, availableImages := prepareRequestForVisual(req)
	log := logger.L()
	for round := 0; round <= o.maxRounds; round++ {
		resp, err := o.upstream.CreateMessage(ctx, req)
		if err != nil {
			return anthropic.MessageResponse{}, err
		}
		if resp.StopReason != "tool_use" {
			return resp, nil
		}

		toolUses, nonVisual := splitVisualToolUses(resp.Content)
		if len(nonVisual) > 0 || len(toolUses) == 0 {
			return resp, nil
		}

		toolResults := make([]anthropic.ContentBlock, 0, len(toolUses))
		for _, toolUse := range toolUses {
			result := o.executeVisualTool(ctx, toolUse, availableImages)
			toolResults = append(toolResults, anthropic.ContentBlock{
				Type:      "tool_result",
				ToolUseID: toolUse.ID,
				Content:   result,
			})
		}

		req.Messages = append(req.Messages, anthropic.Message{
			Role:    "assistant",
			Content: resp.Content,
		})
		req.Messages = append(req.Messages, anthropic.Message{
			Role:    "user",
			Content: toolResults,
		})
		req.ToolChoice = anthropic.ToolChoice{Type: "auto"}
		log.Debug("Visual tool loop completed", "round", round+1, "tools_executed", len(toolUses))
	}
	return anthropic.MessageResponse{}, fmt.Errorf("visual loop exceeded max rounds (%d)", o.maxRounds)
}

func (o *Orchestrator) StreamMessage(ctx context.Context, req anthropic.MessageRequest) (anthropic.Stream, error) {
	if o == nil || o.upstream == nil {
		return nil, fmt.Errorf("visual upstream provider is nil")
	}
	req, availableImages := prepareRequestForVisual(req)
	log := logger.L()
	var allEvents []anthropic.StreamEvent
	for round := 0; round <= o.maxRounds; round++ {
		stream, err := o.upstream.StreamMessage(ctx, req)
		if err != nil {
			return nil, err
		}
		events, err := collectStream(stream)
		stream.Close()
		if err != nil {
			return nil, err
		}
		allEvents = events

		stopReason, lastUsage := streamStopReason(events)
		if stopReason != "tool_use" {
			if lastUsage != nil {
				allEvents = injectUsageIntoStart(allEvents, *lastUsage)
			}
			return &staticStream{events: allEvents}, nil
		}

		assistantContent := collectContentFromEvents(events)
		toolUses, nonVisual := splitVisualToolUses(assistantContent)
		if len(nonVisual) > 0 || len(toolUses) == 0 {
			if lastUsage != nil {
				allEvents = injectUsageIntoStart(allEvents, *lastUsage)
			}
			return &staticStream{events: allEvents}, nil
		}

		toolResults := make([]anthropic.ContentBlock, 0, len(toolUses))
		for _, toolUse := range toolUses {
			result := o.executeVisualTool(ctx, toolUse, availableImages)
			toolResults = append(toolResults, anthropic.ContentBlock{
				Type:      "tool_result",
				ToolUseID: toolUse.ID,
				Content:   result,
			})
		}

		req.Messages = append(req.Messages, anthropic.Message{
			Role:    "assistant",
			Content: assistantContent,
		})
		req.Messages = append(req.Messages, anthropic.Message{
			Role:    "user",
			Content: toolResults,
		})
		req.ToolChoice = anthropic.ToolChoice{Type: "auto"}
		log.Debug("Visual stream tool loop completed", "round", round+1, "tools_executed", len(toolUses))
	}
	return nil, fmt.Errorf("stream visual loop exceeded max rounds (%d)", o.maxRounds)
}

func prepareRequestForVisual(req anthropic.MessageRequest) (anthropic.MessageRequest, []ImageInput) {
	availableImages := make([]ImageInput, 0)
	for messageIndex := range req.Messages {
		content := req.Messages[messageIndex].Content
		if len(content) == 0 {
			continue
		}
		rewritten := make([]anthropic.ContentBlock, 0, len(content))
		for _, block := range content {
			if block.Type != "image" || block.Source == nil {
				rewritten = append(rewritten, block)
				continue
			}
			image, ok := imageInputFromAnthropicSource(block.Source)
			if !ok {
				continue
			}
			availableImages = append(availableImages, image)
			rewritten = append(rewritten, anthropic.ContentBlock{
				Type: "text",
				Text: visualAttachmentText(len(availableImages)),
			})
		}
		req.Messages[messageIndex].Content = rewritten
	}
	return req, availableImages
}

func imageInputFromAnthropicSource(source *anthropic.ImageSource) (ImageInput, bool) {
	if source == nil {
		return ImageInput{}, false
	}
	switch source.Type {
	case "url":
		if strings.TrimSpace(source.URL) == "" {
			return ImageInput{}, false
		}
		return ImageInput{URL: strings.TrimSpace(source.URL)}, true
	case "base64":
		if strings.TrimSpace(source.Data) == "" {
			return ImageInput{}, false
		}
		return ImageInput{Data: source.Data, MimeType: source.MediaType}, true
	default:
		if strings.TrimSpace(source.URL) != "" {
			return ImageInput{URL: strings.TrimSpace(source.URL)}, true
		}
		if strings.TrimSpace(source.Data) != "" {
			return ImageInput{Data: source.Data, MimeType: source.MediaType}, true
		}
		return ImageInput{}, false
	}
}

func visualAttachmentText(index int) string {
	return fmt.Sprintf("[Image #%d is available to Visual Brief and Visual QA. Use image_refs [\"Image #%d\"] or omit image fields to analyze attached images.]", index, index)
}

func (o *Orchestrator) executeVisualTool(ctx context.Context, toolUse anthropic.ContentBlock, availableImages []ImageInput) string {
	request, err := analysisRequestFromToolUse(toolUse, availableImages)
	if err != nil {
		return "Visual error: " + err.Error()
	}
	result, err := o.client.Analyze(ctx, request)
	if err != nil {
		logger.L().Warn("Visual tool execution failed", "tool", toolUse.Name, "error", err)
		fmt.Fprintln(logger.Output(), "Visual tool execution failed:", toolUse.Name, err.Error())
		logger.Flush()
		return "Visual error: " + err.Error()
	}
	logger.L().Info("Visual tool executed", "tool", toolUse.Name, "images", len(request.Images))
	fmt.Fprintln(logger.Output(), "Visual tool executed:", toolUse.Name)
	logger.Flush()
	switch toolUse.Name {
	case ToolVisualBrief:
		return "Visual Brief result:\n" + strings.TrimSpace(result)
	case ToolVisualQA:
		return "Visual QA result:\n" + strings.TrimSpace(result)
	default:
		return strings.TrimSpace(result)
	}
}

type briefInput struct {
	ImageURL  string       `json:"image_url,omitempty"`
	ImageURLs []string     `json:"image_urls,omitempty"`
	ImageRefs []string     `json:"image_refs,omitempty"`
	Images    []ImageInput `json:"images,omitempty"`
	Context   string       `json:"context,omitempty"`
	Focus     string       `json:"focus,omitempty"`
}

type qaInput struct {
	Question           string             `json:"question,omitempty"`
	ImageURL           string             `json:"image_url,omitempty"`
	ImageURLs          []string           `json:"image_urls,omitempty"`
	ImageRefs          []string           `json:"image_refs,omitempty"`
	Images             []ImageInput       `json:"images,omitempty"`
	PriorVisualContext string             `json:"prior_visual_context,omitempty"`
	Context            string             `json:"context,omitempty"`
	Conversation       []ConversationTurn `json:"conversation,omitempty"`
}

func analysisRequestFromToolUse(toolUse anthropic.ContentBlock, availableImages []ImageInput) (AnalysisRequest, error) {
	switch toolUse.Name {
	case ToolVisualBrief:
		var input briefInput
		if err := json.Unmarshal(toolUse.Input, &input); err != nil {
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
		if err := json.Unmarshal(toolUse.Input, &input); err != nil {
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
		return AnalysisRequest{}, fmt.Errorf("unknown visual tool %q", toolUse.Name)
	}
}

func buildBriefPrompt(input briefInput) string {
	var b strings.Builder
	b.WriteString("Provide a first-round visual brief for the main agent.\n")
	if strings.TrimSpace(input.Context) != "" {
		b.WriteString("\nTask context:\n")
		b.WriteString(strings.TrimSpace(input.Context))
		b.WriteByte('\n')
	}
	if strings.TrimSpace(input.Focus) != "" {
		b.WriteString("\nFocus:\n")
		b.WriteString(strings.TrimSpace(input.Focus))
		b.WriteByte('\n')
	}
	b.WriteString("\nReturn concise sections: overview, important visual details, any readable text/OCR, uncertainties, and useful Visual QA follow-ups. Do not solve beyond the visual evidence.")
	return b.String()
}

func buildQAPrompt(input qaInput) string {
	var b strings.Builder
	b.WriteString("Answer this targeted visual clarification question for the main agent.\n\nQuestion:\n")
	b.WriteString(strings.TrimSpace(input.Question))
	b.WriteByte('\n')
	if strings.TrimSpace(input.Context) != "" {
		b.WriteString("\nTask context:\n")
		b.WriteString(strings.TrimSpace(input.Context))
		b.WriteByte('\n')
	}
	if strings.TrimSpace(input.PriorVisualContext) != "" {
		b.WriteString("\nPrior visual context:\n")
		b.WriteString(strings.TrimSpace(input.PriorVisualContext))
		b.WriteByte('\n')
	}
	if len(input.Conversation) > 0 {
		b.WriteString("\nVisual clarification history:\n")
		for _, turn := range input.Conversation {
			role := strings.TrimSpace(turn.Role)
			if role == "" {
				role = "note"
			}
			content := strings.TrimSpace(turn.Content)
			if content == "" {
				continue
			}
			b.WriteString(role)
			b.WriteString(": ")
			b.WriteString(content)
			b.WriteByte('\n')
		}
	}
	b.WriteString("\nAnswer directly, call out uncertainty, and say what extra image/detail would resolve ambiguity.")
	return b.String()
}

func normalizeImages(single string, urls []string, images []ImageInput, refs []string, availableImages []ImageInput) []ImageInput {
	normalized := make([]ImageInput, 0, len(urls)+len(images)+len(refs)+1)
	if image, ok := resolveImageValue(single, availableImages); ok {
		normalized = append(normalized, image)
	}
	for _, url := range urls {
		if image, ok := resolveImageValue(url, availableImages); ok {
			normalized = append(normalized, image)
		}
	}
	for _, ref := range refs {
		if image, ok := resolveAttachedImage(ref, availableImages); ok {
			normalized = append(normalized, image)
		}
	}
	for _, image := range images {
		if strings.TrimSpace(image.URL) != "" {
			if resolved, ok := resolveImageValue(image.URL, availableImages); ok {
				if resolved.Detail == "" {
					resolved.Detail = image.Detail
				}
				normalized = append(normalized, resolved)
			}
			continue
		}
		if !image.HasAnthropicSource() {
			continue
		}
		normalized = append(normalized, image)
	}
	if len(normalized) == 0 && len(availableImages) > 0 {
		normalized = append(normalized, availableImages...)
	}
	return normalized
}

func resolveImageValue(value string, availableImages []ImageInput) (ImageInput, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return ImageInput{}, false
	}
	if isSupportedImageURL(value) {
		return ImageInput{URL: value}, true
	}
	return resolveAttachedImage(value, availableImages)
}

func resolveAttachedImage(value string, availableImages []ImageInput) (ImageInput, bool) {
	index, ok := imageReferenceIndex(value)
	if !ok || index <= 0 || index > len(availableImages) {
		return ImageInput{}, false
	}
	return availableImages[index-1], true
}

var imageReferencePattern = regexp.MustCompile(`(?i)\bimage\s*#\s*(\d+)\b`)

func imageReferenceIndex(value string) (int, bool) {
	match := imageReferencePattern.FindStringSubmatch(strings.TrimSpace(value))
	if len(match) != 2 {
		return 0, false
	}
	index, err := strconv.Atoi(match[1])
	if err != nil {
		return 0, false
	}
	return index, true
}

func splitVisualToolUses(blocks []anthropic.ContentBlock) (visualUses, nonVisualToolUses []anthropic.ContentBlock) {
	for _, block := range blocks {
		if block.Type != "tool_use" {
			continue
		}
		if IsVisualTool(block.Name) {
			visualUses = append(visualUses, block)
		} else {
			nonVisualToolUses = append(nonVisualToolUses, block)
		}
	}
	return visualUses, nonVisualToolUses
}

func collectStream(stream anthropic.Stream) ([]anthropic.StreamEvent, error) {
	var events []anthropic.StreamEvent
	for {
		event, err := stream.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return events, err
		}
		events = append(events, event)
	}
	return events, nil
}

func streamStopReason(events []anthropic.StreamEvent) (string, *anthropic.Usage) {
	stopReason := "end_turn"
	var lastUsage *anthropic.Usage
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type != "message_delta" {
			continue
		}
		if events[i].Delta.StopReason != "" {
			stopReason = events[i].Delta.StopReason
		}
		lastUsage = events[i].Usage
		break
	}
	return stopReason, lastUsage
}

type streamBlock struct {
	block anthropic.ContentBlock
	input strings.Builder
	text  strings.Builder
}

func collectContentFromEvents(events []anthropic.StreamEvent) []anthropic.ContentBlock {
	active := map[int]*streamBlock{}
	var order []int
	for _, event := range events {
		switch event.Type {
		case "content_block_start":
			if event.ContentBlock == nil {
				continue
			}
			block := *event.ContentBlock
			active[event.Index] = &streamBlock{block: block}
			order = append(order, event.Index)
		case "content_block_delta":
			current := active[event.Index]
			if current == nil {
				continue
			}
			if event.Delta.Text != "" {
				current.text.WriteString(event.Delta.Text)
			}
			if event.Delta.PartialJSON != "" {
				current.input.WriteString(event.Delta.PartialJSON)
			}
		}
	}

	blocks := make([]anthropic.ContentBlock, 0, len(order))
	for _, index := range order {
		current := active[index]
		if current == nil {
			continue
		}
		block := current.block
		if current.text.Len() > 0 {
			block.Text = current.text.String()
		}
		if current.input.Len() > 0 {
			block.Input = json.RawMessage(current.input.String())
		}
		blocks = append(blocks, block)
	}
	return blocks
}

func injectUsageIntoStart(events []anthropic.StreamEvent, usage anthropic.Usage) []anthropic.StreamEvent {
	for i, event := range events {
		if event.Type == "message_start" && event.Message != nil {
			event.Message.Usage = usage
			events[i] = event
			break
		}
	}
	return events
}

type staticStream struct {
	events []anthropic.StreamEvent
	pos    int
}

func (s *staticStream) Next() (anthropic.StreamEvent, error) {
	if s.pos >= len(s.events) {
		return anthropic.StreamEvent{}, io.EOF
	}
	event := s.events[s.pos]
	s.pos++
	return event, nil
}

func (s *staticStream) Close() error {
	return nil
}
