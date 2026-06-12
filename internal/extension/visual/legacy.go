package visual

import (
	"context"
	"fmt"
	"strings"

	"moonbridge/internal/protocol/anthropic"
)

// Legacy types and functions maintained for backward compatibility.

// ClientConfig configures a BridgeClient (legacy Anthropic-specific path).
type ClientConfig struct {
	Provider  Provider
	Model     string
	MaxTokens int
}

// NewBridgeClient creates a BridgeClient (legacy Anthropic-specific path).
func NewBridgeClient(cfg ClientConfig) *BridgeClient {
	maxTokens := cfg.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 2048
	}
	return &BridgeClient{
		provider:  cfg.Provider,
		model:     strings.TrimSpace(cfg.Model),
		maxTokens: maxTokens,
	}
}

// BridgeClient is the legacy Anthropic-specific vision client.
// Deprecated: Use BridgeCoreClient instead.
type BridgeClient struct {
	provider  Provider
	model     string
	maxTokens int
}

func (client *BridgeClient) Analyze(ctx context.Context, request AnalysisRequest) (string, error) {
	if client == nil {
		return "", fmt.Errorf("visual bridge client is nil")
	}
	if client.provider == nil {
		return "", fmt.Errorf("visual provider is nil")
	}
	if client.model == "" {
		return "", fmt.Errorf("visual model is required")
	}

	resp, err := client.provider.CreateMessage(ctx, anthropic.MessageRequest{
		Model:     client.model,
		MaxTokens: client.maxTokens,
		System: []anthropic.ContentBlock{{
			Type: "text",
			Text: visualSystemPrompt,
		}},
		Messages: []anthropic.Message{{
			Role:    "user",
			Content: anthropicContentParts(request),
		}},
	})
	if err != nil {
		return "", err
	}
	text := textFromContent(resp.Content)
	if text == "" {
		return "", fmt.Errorf("visual provider returned empty content")
	}
	return text, nil
}

func anthropicContentParts(request AnalysisRequest) []anthropic.ContentBlock {
	parts := []anthropic.ContentBlock{{Type: "text", Text: request.Prompt}}
	for _, image := range request.Images {
		source := image.AnthropicSource()
		if source == nil {
			continue
		}
		parts = append(parts, anthropic.ContentBlock{
			Type:   "image",
			Source: source,
		})
	}
	return parts
}

func textFromContent(blocks []anthropic.ContentBlock) string {
	var b strings.Builder
	for _, block := range blocks {
		if block.Type != "text" || strings.TrimSpace(block.Text) == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(strings.TrimSpace(block.Text))
	}
	return strings.TrimSpace(b.String())
}

// HasAnthropicSource checks if ImageInput can produce a valid Anthropic source.
// Deprecated: use image.AnthropicSource() != nil directly.
func (image ImageInput) HasAnthropicSource() bool {
	return image.AnthropicSource() != nil
}

// AnthropicSource converts ImageInput to an Anthropic ImageSource.
func (image ImageInput) AnthropicSource() *anthropic.ImageSource {
	if strings.TrimSpace(image.URL) != "" {
		url := strings.TrimSpace(image.URL)
		if !isSupportedImageURL(url) {
			return nil
		}
		if strings.HasPrefix(url, "data:") {
			return dataURLSource(url)
		}
		return &anthropic.ImageSource{Type: "url", URL: url}
	}
	data := strings.TrimSpace(image.Data)
	if data == "" {
		return nil
	}
	if strings.HasPrefix(data, "data:") {
		return dataURLSource(data)
	}
	mimeType := strings.TrimSpace(image.MimeType)
	if mimeType == "" {
		mimeType = "image/png"
	}
	return &anthropic.ImageSource{Type: "base64", MediaType: mimeType, Data: data}
}

func dataURLSource(value string) *anthropic.ImageSource {
	mediaType, data := splitDataURL(value)
	return &anthropic.ImageSource{Type: "base64", MediaType: mediaType, Data: data}
}

// isSupportedImageURL checks whether a string is a valid image URL.
func isSupportedImageURL(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	return strings.HasPrefix(lower, "http://") ||
		strings.HasPrefix(lower, "https://") ||
		strings.HasPrefix(lower, "data:")
}
