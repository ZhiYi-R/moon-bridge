// Package minimax maps MiniMax reasoning controls to the two supported wire protocols.
package minimax

import (
	"fmt"

	"moonbridge/internal/format"
)

// InvalidControlError identifies invalid client reasoning input before inference.
type InvalidControlError struct{ Message string }

func (e *InvalidControlError) Error() string { return e.Message }
func invalidControl(message string, args ...any) error {
	return &InvalidControlError{Message: fmt.Sprintf(message, args...)}
}

// Reasoning returns wire thinking.type and effort for the exact supported models.
// Other models retain their existing adapter behavior. Native Core controls take
// precedence over Responses extensions. M3 thinking modes are not effort levels.
func Reasoning(req *format.CoreRequest, defaultThinking string) (thinking, effort string, err error) {
	if req.Model != "MiniMax-M3.1-Flash" && req.Model != "MiniMax-M3" {
		return "", "", nil
	}
	openai, _ := req.Extensions["openai"].(map[string]any)
	reasoning, _ := openai["reasoning"].(map[string]any)
	for _, key := range []string{"effort", "type"} {
		if value, exists := reasoning[key]; exists {
			if _, ok := value.(string); !ok {
				return "", "", invalidControl("MiniMax reasoning %s must be a string", key)
			}
		}
	}
	effort, _ = reasoning["effort"].(string)
	thinking, _ = reasoning["type"].(string)
	if req.Output != nil {
		effort = req.Output.Effort
	}
	if req.Thinking != nil {
		thinking = req.Thinking.Type
	}
	if req.Model == "MiniMax-M3" {
		// M3 ignores ordinary effort values. Accept previously advertised mode
		// presets for compatibility, but never send an effort upstream.
		if thinking == "" && (effort == "adaptive" || effort == "disabled") {
			thinking = effort
		}
		if thinking == "" {
			thinking = defaultThinking
		}
		if thinking != "adaptive" && thinking != "disabled" {
			return "", "", invalidControl("MiniMax-M3 thinking type must be adaptive or disabled")
		}
		return thinking, "", nil
	}
	if thinking != "" && thinking != "adaptive" {
		return "", "", invalidControl("MiniMax-M3.1-Flash requires adaptive thinking")
	}
	if effort == "" {
		effort = "max"
	}
	switch effort {
	case "low", "medium", "high", "xhigh", "max":
		return "adaptive", effort, nil
	default:
		return "", "", invalidControl("unsupported MiniMax-M3.1-Flash reasoning effort %q", effort)
	}
}
