package visual

import (
	"context"
	"fmt"
	"strings"

	"moonbridge/internal/format"
)

const visualSystemPrompt = "You are a vision analysis model behind Moon Bridge Visual. Analyze images carefully, state uncertainty, and do not invent visual facts."

// CoreProvider is a protocol-agnostic LLM provider interface.
// It operates on format.CoreRequest / format.CoreResponse so the visual plugin
// does not depend on any protocol-specific DTO (Anthropic, OpenAI, Chat, Google, etc.).
type CoreProvider interface {
	CreateCore(ctx context.Context, req *format.CoreRequest) (*format.CoreResponse, error)
}

// BridgeCoreClient calls the visual-model CoreProvider with a simple
// user-message containing the analysis prompt and images.
type BridgeCoreClient struct {
	provider  CoreProvider
	model     string
	maxTokens int
}

type BridgeCoreConfig struct {
	Provider  CoreProvider
	Model     string
	MaxTokens int
}

func NewBridgeCoreClient(cfg BridgeCoreConfig) *BridgeCoreClient {
	maxTokens := cfg.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 2048
	}
	return &BridgeCoreClient{
		provider:  cfg.Provider,
		model:     strings.TrimSpace(cfg.Model),
		maxTokens: maxTokens,
	}
}

func (client *BridgeCoreClient) Analyze(ctx context.Context, request AnalysisRequest) (string, error) {
	if client == nil {
		return "", fmt.Errorf("visual bridge client is nil")
	}
	if client.provider == nil {
		return "", fmt.Errorf("visual provider is nil")
	}
	if client.model == "" {
		return "", fmt.Errorf("visual model is required")
	}

	resp, err := client.provider.CreateCore(ctx, &format.CoreRequest{
		Model:     client.model,
		MaxTokens: client.maxTokens,
		System:    []format.CoreContentBlock{{Type: "text", Text: visualSystemPrompt}},
		Messages:  []format.CoreMessage{{Role: "user", Content: coreContentParts(request)}},
	})
	if err != nil {
		return "", err
	}
	text := coreTextFromResponse(resp)
	if text == "" {
		return "", fmt.Errorf("visual provider returned empty content")
	}
	return text, nil
}

func coreContentParts(request AnalysisRequest) []format.CoreContentBlock {
	parts := []format.CoreContentBlock{{Type: "text", Text: request.Prompt}}
	for _, image := range request.Images {
		source := image.CoreSource()
		if source == nil {
			continue
		}
		parts = append(parts, *source)
	}
	return parts
}

func coreTextFromResponse(resp *format.CoreResponse) string {
	if resp == nil {
		return ""
	}
	var b strings.Builder
	for _, msg := range resp.Messages {
		for _, block := range msg.Content {
			if block.Type != "text" || strings.TrimSpace(block.Text) == "" {
				continue
			}
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(strings.TrimSpace(block.Text))
		}
	}
	return strings.TrimSpace(b.String())
}

// CoreSource converts ImageInput to a Core-format content block.
func (image ImageInput) CoreSource() *format.CoreContentBlock {
	if strings.TrimSpace(image.URL) != "" {
		url := strings.TrimSpace(image.URL)
		if !isSupportedImageURL(url) {
			return nil
		}
		if strings.HasPrefix(url, "data:") {
			mediaType, raw := splitDataURL(url)
			return &format.CoreContentBlock{
				Type:      "image",
				ImageData: raw,
				MediaType: mediaType,
			}
		}
		return &format.CoreContentBlock{
			Type:      "image",
			ImageData: url,
			MediaType: "",
		}
	}
	data := strings.TrimSpace(image.Data)
	if data == "" {
		return nil
	}
	if strings.HasPrefix(data, "data:") {
		mediaType, raw := splitDataURL(data)
		return &format.CoreContentBlock{
			Type:      "image",
			ImageData: raw,
			MediaType: mediaType,
		}
	}
	mimeType := strings.TrimSpace(image.MimeType)
	if mimeType == "" {
		mimeType = "image/png"
	}
	return &format.CoreContentBlock{
		Type:      "image",
		ImageData: data,
		MediaType: mimeType,
	}
}

func splitDataURL(value string) (mediaType, data string) {
	header, data, ok := strings.Cut(value, ",")
	if !ok {
		return "", value
	}
	mediaType = strings.TrimPrefix(header, "data:")
	if semicolon := strings.IndexByte(mediaType, ';'); semicolon >= 0 {
		mediaType = mediaType[:semicolon]
	}
	if mediaType == "" {
		mediaType = "image/png"
	}
	return mediaType, data
}

// CoreProviderFunc is a function adapter that implements CoreProvider.
type CoreProviderFunc func(ctx context.Context, req *format.CoreRequest) (*format.CoreResponse, error)

func (f CoreProviderFunc) CreateCore(ctx context.Context, req *format.CoreRequest) (*format.CoreResponse, error) {
	return f(ctx, req)
}

// NewCoreBridge creates a CoreOrchestrator from a CoreProvider upstream and a
// CoreProvider visual model. It's the Core-level equivalent of WrapProvider.
func NewCoreBridge(upstream CoreProvider, visualProvider CoreProvider, model string, maxRounds int, maxTokens int) *CoreOrchestrator {
	visionClient := NewBridgeCoreClient(BridgeCoreConfig{
		Provider:  visualProvider,
		Model:     model,
		MaxTokens: maxTokens,
	})
	return NewCoreOrchestrator(CoreOrchestratorConfig{
		Upstream:  upstream,
		Client:    visionClient,
		MaxRounds: maxRounds,
	})
}
