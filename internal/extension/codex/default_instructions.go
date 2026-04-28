package codex

import (
	_ "embed"
	"strings"
)

//go:embed default_instructions.txt
var defaultPromptTemplate string

// defaultBaseInstructions returns the default base_instructions for a model.
// These are derived from the official model catalog with provider-specific
// branding stripped out. {{MODEL_NAME}} in the template is replaced with the
// actual model name extracted from the slug.
func defaultBaseInstructions(slug string) string {
	modelName := extractModelName(slug)
	return strings.ReplaceAll(defaultPromptTemplate, "{{MODEL_NAME}}", modelName)
}

// extractModelName strips the provider suffix from a slug like "gpt-5.5(openai)".
// Returns the slug itself if no parenthesized provider is found.
func extractModelName(slug string) string {
	if idx := strings.Index(slug, "("); idx > 0 {
		return strings.TrimSpace(slug[:idx])
	}
	return slug
}
