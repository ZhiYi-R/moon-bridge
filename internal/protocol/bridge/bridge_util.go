package bridge

import (
	"net/http"
	"strings"

	"moonbridge/internal/foundation/openai"
	"moonbridge/internal/protocol/anthropic"
)

func statusFromStopReason(stopReason string) (string, *openai.IncompleteDetails) {
	switch stopReason {
	case "max_tokens":
		return "incomplete", &openai.IncompleteDetails{Reason: "max_output_tokens"}
	case "model_context_window":
		return "incomplete", &openai.IncompleteDetails{Reason: "max_input_tokens"}
	case "pause_turn":
		return "incomplete", &openai.IncompleteDetails{Reason: "provider_pause"}
	default:
		return "completed", nil
	}
}

func normalizeUsage(usage anthropic.Usage) openai.Usage {
	inputTokens := usage.InputTokens + usage.CacheCreationInputTokens + usage.CacheReadInputTokens
	outputTokens := usage.OutputTokens
	return openai.Usage{
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		TotalTokens:  inputTokens + outputTokens,
		InputTokensDetails: openai.InputTokensDetails{
			CachedTokens: usage.CacheReadInputTokens,
		},
	}
}

func responseID(providerID string) string {
	if providerID == "" {
		return "resp_generated"
	}
	if strings.HasPrefix(providerID, "resp_") {
		return providerID
	}
	return "resp_" + providerID
}

func invalidRequest(message, param, code string) error {
	return &RequestError{Status: http.StatusBadRequest, Message: message, Param: param, Code: code}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
