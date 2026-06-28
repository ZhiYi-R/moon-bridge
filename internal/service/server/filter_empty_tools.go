package server

import (
	"moonbridge/internal/format"
)

// filterEmptyCoreToolNames removes tools whose name is empty.
// Codex desktop sometimes sends tools with empty names that
// upstream APIs (DeepSeek, Qwen) reject with a 400 error.
func filterEmptyCoreToolNames(tools []format.CoreTool) []format.CoreTool {
	if len(tools) == 0 {
		return tools
	}
	filtered := make([]format.CoreTool, 0, len(tools))
	for _, t := range tools {
		if t.Name != "" {
			filtered = append(filtered, t)
		}
	}
	return filtered
}
