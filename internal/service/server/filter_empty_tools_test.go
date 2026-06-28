package server

import (
	"moonbridge/internal/format"
	"testing"
)

func TestFilterEmptyCoreToolNames(t *testing.T) {
	tests := []struct {
		name     string
		tools    []format.CoreTool
		expected int
	}{
		{
			name:     "empty tools",
			tools:    []format.CoreTool{},
			expected: 0,
		},
		{
			name: "no empty names",
			tools: []format.CoreTool{
				{Name: "tool1"},
				{Name: "tool2"},
			},
			expected: 2,
		},
		{
			name: "one empty name at end",
			tools: []format.CoreTool{
				{Name: "tool1"},
				{Name: "tool2"},
				{Name: ""},
			},
			expected: 2,
		},
		{
			name: "one empty name in middle",
			tools: []format.CoreTool{
				{Name: "tool1"},
				{Name: ""},
				{Name: "tool3"},
			},
			expected: 2,
		},
		{
			name: "all empty names",
			tools: []format.CoreTool{
				{Name: ""},
				{Name: ""},
			},
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := filterEmptyCoreToolNames(tt.tools)
			if len(result) != tt.expected {
				t.Errorf("filterEmptyCoreToolNames() returned %d tools, want %d", len(result), tt.expected)
			}
			// Verify no empty names remain
			for _, tool := range result {
				if tool.Name == "" {
					t.Errorf("filtered result still contains tool with empty name")
				}
			}
		})
	}
}
