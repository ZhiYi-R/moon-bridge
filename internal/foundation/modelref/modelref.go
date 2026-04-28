// Package modelref provides a single authoritative parser for model references
// that may appear in "provider/model" or "model(provider)" format.
package modelref

import "strings"

// Parse extracts a provider key and model name from a model reference string.
// Supports two formats:
//
//	provider/model   → (provider, model)
//	model(provider)  → (provider, model)
//
// Returns ("", ref) when neither pattern matches.
func Parse(ref string) (provider, model string) {
	trimmed := strings.TrimSpace(ref)

	// Try model(provider) format first (e.g. "claude-opus-4-6(kiro)").
	if idx := strings.LastIndex(trimmed, "("); idx > 0 && strings.HasSuffix(trimmed, ")") {
		maybeModel := trimmed[:idx]
		maybeProvider := trimmed[idx+1 : len(trimmed)-1]
		if maybeProvider != "" && maybeModel != "" {
			return strings.TrimSpace(maybeProvider), strings.TrimSpace(maybeModel)
		}
	}

	// Fall back to provider/model format.
	before, after, found := strings.Cut(trimmed, "/")
	if !found {
		return "", ref
	}
	return strings.TrimSpace(before), strings.TrimSpace(after)
}
