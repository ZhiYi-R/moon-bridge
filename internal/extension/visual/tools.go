package visual

import "moonbridge/internal/protocol/anthropic"

const (
	ToolVisualBrief = "visual_brief"
	ToolVisualQA    = "visual_qa"
)

func Tools() []anthropic.Tool {
	return []anthropic.Tool{
		{
			Name:        ToolVisualBrief,
			Description: "Visual Brief. Use this as the first visual pass when image understanding is needed. For attached images, pass image_refs like Image #1 or omit image fields; pass image_urls only for real HTTP(S)/data URLs. It returns a concise first-round scene/object/text/layout brief plus uncertainties and useful follow-up targets.",
			InputSchema: visualBriefSchema(),
		},
		{
			Name:        ToolVisualQA,
			Description: "Visual QA. Use this after a Visual Brief, or for targeted follow-up questions about an image. Ask one clear visual question at a time; use image_refs for attached images, real image_urls for HTTP(S)/data URLs, or prior_visual_context from earlier visual tool results.",
			InputSchema: visualQASchema(),
		},
	}
}

func IsVisualTool(name string) bool {
	return name == ToolVisualBrief || name == ToolVisualQA
}

func visualBriefSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"image_urls": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "HTTP(S) image URLs or data URLs only. Do not put attachment labels like Image #1 here.",
			},
			"image_refs": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Attached image labels such as Image #1. Use this for images already present in the conversation.",
			},
			"images": map[string]any{
				"type":        "array",
				"description": "Structured images. Each item may provide url, or base64 data plus mime_type.",
				"items":       imageInputSchema(),
			},
			"context": map[string]any{
				"type":        "string",
				"description": "User task context that helps decide which visual facts matter.",
			},
			"focus": map[string]any{
				"type":        "string",
				"description": "Optional focus area, such as UI layout, code screenshot, chart, object details, OCR, or anomalies.",
			},
		},
	}
}

func visualQASchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"question": map[string]any{
				"type":        "string",
				"description": "A specific visual question to answer or clarify.",
			},
			"image_urls": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Optional HTTP(S) image URLs or data URLs only. Do not put attachment labels like Image #1 here.",
			},
			"image_refs": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Attached image labels such as Image #1. Use this for images already present in the conversation.",
			},
			"images": map[string]any{
				"type":        "array",
				"description": "Optional structured images. Each item may provide url, or base64 data plus mime_type.",
				"items":       imageInputSchema(),
			},
			"prior_visual_context": map[string]any{
				"type":        "string",
				"description": "Relevant Visual Brief or previous Visual QA result when image data is not repeated.",
			},
			"context": map[string]any{
				"type":        "string",
				"description": "Additional task context.",
			},
			"conversation": map[string]any{
				"type":        "array",
				"description": "Optional short visual clarification history.",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"role":    map[string]any{"type": "string"},
						"content": map[string]any{"type": "string"},
					},
				},
			},
		},
		"required": []string{"question"},
	}
}

func imageInputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"url": map[string]any{
				"type":        "string",
				"description": "HTTP(S) image URL or data URL.",
			},
			"data": map[string]any{
				"type":        "string",
				"description": "Base64 image bytes, or a complete data URL.",
			},
			"mime_type": map[string]any{
				"type":        "string",
				"description": "MIME type for base64 data, for example image/png or image/jpeg.",
			},
			"detail": map[string]any{
				"type":        "string",
				"enum":        []string{"auto", "low", "high"},
				"description": "Vision detail hint.",
			},
		},
	}
}
