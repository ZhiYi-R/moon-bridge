package codextool

import (
	"encoding/json"
	"testing"

	"moonbridge/internal/format"
)

func TestBuildNamespaceToolsNestedOneOfDeduplicatesRequiredAction(t *testing.T) {
	tools, err := BuildNamespaceTools(
		[]string{"click", "type"},
		map[string]format.CoreTool{
			"click": {
				Name: "click",
				InputSchema: map[string]any{
					"type":     "object",
					"required": []any{"app", "element_index", "action"},
					"properties": map[string]any{
						"app":           map[string]any{"type": "string"},
						"element_index": map[string]any{"type": "integer"},
						"action":        map[string]any{"type": "string"},
					},
				},
			},
			"type": {
				Name: "type",
				InputSchema: map[string]any{
					"type":     "object",
					"required": []any{"text", "action"},
					"properties": map[string]any{
						"text": map[string]any{"type": "string"},
					},
				},
			},
		},
		"mcp__computer_use",
		NestedOneOf,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 {
		t.Fatalf("tools: got %d, want 1", len(tools))
	}

	oneOf, ok := tools[0].InputSchema["oneOf"].([]map[string]any)
	if !ok {
		t.Fatalf("oneOf = %T, want []map[string]any", tools[0].InputSchema["oneOf"])
	}
	assertStringSlice(t, oneOf[0]["required"], []string{"action", "app", "element_index"})
	assertStringSlice(t, oneOf[1]["required"], []string{"action", "text"})
}

func TestBuildNamespaceToolsNestedOneOfAcceptsRawRequired(t *testing.T) {
	tools, err := BuildNamespaceTools(
		[]string{"click"},
		map[string]format.CoreTool{
			"click": {
				Name: "click",
				InputSchema: map[string]any{
					"type":     "object",
					"required": json.RawMessage(`["action","app","element_index","action"]`),
					"properties": map[string]json.RawMessage{
						"app":           json.RawMessage(`{"type":"string"}`),
						"element_index": json.RawMessage(`{"type":"integer"}`),
					},
				},
			},
		},
		"mcp__computer_use",
		NestedOneOf,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 {
		t.Fatalf("tools: got %d, want 1", len(tools))
	}

	oneOf, ok := tools[0].InputSchema["oneOf"].([]map[string]any)
	if !ok {
		t.Fatalf("oneOf = %T, want []map[string]any", tools[0].InputSchema["oneOf"])
	}
	assertStringSlice(t, oneOf[0]["required"], []string{"action", "app", "element_index"})
}

func TestBuildNamespaceToolsNestedOneOfAcceptsStringRequired(t *testing.T) {
	tools, err := BuildNamespaceTools(
		[]string{"drag"},
		map[string]format.CoreTool{
			"drag": {
				Name: "drag",
				InputSchema: map[string]any{
					"type":     "object",
					"required": []string{"action", "x", "x"},
					"properties": map[string]any{
						"x": map[string]any{"type": "number"},
					},
				},
			},
		},
		"mcp__computer_use",
		NestedOneOf,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 {
		t.Fatalf("tools: got %d, want 1", len(tools))
	}

	oneOf, ok := tools[0].InputSchema["oneOf"].([]map[string]any)
	if !ok {
		t.Fatalf("oneOf = %T, want []map[string]any", tools[0].InputSchema["oneOf"])
	}
	assertStringSlice(t, oneOf[0]["required"], []string{"action", "x"})
}

func assertStringSlice(t *testing.T, value any, want []string) {
	t.Helper()

	got, ok := value.([]string)
	if !ok {
		t.Fatalf("value = %T, want []string", value)
	}
	if len(got) != len(want) {
		t.Fatalf("value = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("value = %#v, want %#v", got, want)
		}
	}
}
