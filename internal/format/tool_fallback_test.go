package format

import (
	"encoding/json"
	"testing"
)

func TestIsServerSideTool(t *testing.T) {
	tests := []struct {
		name string
		tool CoreTool
		want bool
	}{
		{
			name: "unnamed source type is server-side",
			tool: CoreTool{Extensions: map[string]any{"source_type": "image_generation"}},
			want: true,
		},
		{
			name: "function source type is not server-side",
			tool: CoreTool{Extensions: map[string]any{"source_type": "function"}},
			want: false,
		},
		{
			name: "named custom tool is not server-side",
			tool: CoreTool{Name: "apply_patch", Extensions: map[string]any{"source_type": "custom"}},
			want: false,
		},
		{
			name: "named builtin tool is not skipped by fallback",
			tool: CoreTool{Name: "web_search", Extensions: map[string]any{"source_type": "web_search"}},
			want: false,
		},
		{
			name: "tool without source type is not server-side",
			tool: CoreTool{},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsServerSideTool(tt.tool); got != tt.want {
				t.Errorf("IsServerSideTool() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestShouldFallbackToolChoiceForSkippedTools(t *testing.T) {
	skippedTools := []CoreTool{
		{Extensions: map[string]any{"source_type": "image_generation"}},
	}

	tests := []struct {
		name string
		tc   *CoreToolChoice
		want bool
	}{
		{
			name: "nil choice does not fallback",
			tc:   nil,
			want: false,
		},
		{
			name: "auto does not fallback",
			tc:   &CoreToolChoice{Mode: "auto"},
			want: false,
		},
		{
			name: "none does not fallback",
			tc:   &CoreToolChoice{Mode: "none"},
			want: false,
		},
		{
			name: "unnamed required fallback",
			tc:   &CoreToolChoice{Mode: "required"},
			want: true,
		},
		{
			name: "skipped named function fallback",
			tc:   &CoreToolChoice{Mode: "function", Name: "image_generation"},
			want: true,
		},
		{
			name: "skipped mode fallback",
			tc:   &CoreToolChoice{Mode: "image_generation"},
			want: true,
		},
		{
			name: "surviving named tool does not fallback",
			tc:   &CoreToolChoice{Mode: "required", Name: "get_weather"},
			want: false,
		},
		{
			name: "raw object pointing to skipped tool falls back",
			tc:   &CoreToolChoice{Raw: []byte(`{"type":"function","function":{"name":"image_generation"}}`)},
			want: true,
		},
		{
			name: "raw object pointing to surviving tool does not fallback",
			tc:   &CoreToolChoice{Raw: []byte(`{"type":"function","function":{"name":"get_weather"}}`)},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ShouldFallbackToolChoiceForSkippedTools(tt.tc, skippedTools)
			if got != tt.want {
				t.Errorf("ShouldFallbackToolChoiceForSkippedTools() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPrepareFunctionProviderTool(t *testing.T) {
	t.Run("server-side tool is skipped", func(t *testing.T) {
		_, ok := PrepareFunctionProviderTool(CoreTool{
			Extensions: map[string]any{"source_type": "image_generation"},
		})
		if ok {
			t.Fatal("PrepareFunctionProviderTool() ok = true, want false")
		}
	})

	t.Run("empty-name tool is skipped", func(t *testing.T) {
		_, ok := PrepareFunctionProviderTool(CoreTool{})
		if ok {
			t.Fatal("PrepareFunctionProviderTool() ok = true, want false")
		}
	})

	t.Run("schema is normalized without mutating original", func(t *testing.T) {
		original := map[string]any{
			"type":     "object",
			"required": []any{"action", "app", "action", "", 12},
			"properties": map[string]any{
				"nested": map[string]any{
					"type":     "object",
					"required": []any{"id", "id"},
					"const":    nil,
				},
				"nullable": map[string]any{"enum": []any{nil}},
				"empty":    map[string]any{"oneOf": []any{}},
			},
			"oneOf": []any{
				map[string]any{
					"type":     "object",
					"required": []any{"x", "x", "y"},
				},
			},
		}

		prepared, ok := PrepareFunctionProviderTool(CoreTool{
			Name:        "mcp__computer_use",
			Description: "Computer use",
			InputSchema: original,
		})
		if !ok {
			t.Fatal("PrepareFunctionProviderTool() ok = false, want true")
		}
		assertRequired(t, prepared.InputSchema, []string{"action", "app"})

		props := prepared.InputSchema["properties"].(map[string]any)
		nested := props["nested"].(map[string]any)
		assertRequired(t, nested, []string{"id"})
		if _, exists := nested["const"]; !exists {
			t.Fatal("const:null constraint was removed")
		}
		nullable := props["nullable"].(map[string]any)
		enum := nullable["enum"].([]any)
		if len(enum) != 1 || enum[0] != nil {
			t.Fatalf("enum null constraint = %#v, want [nil]", enum)
		}
		empty := props["empty"].(map[string]any)
		if oneOf, ok := empty["oneOf"].([]any); !ok || len(oneOf) != 0 {
			t.Fatalf("empty oneOf = %#v, want empty []any", empty["oneOf"])
		}

		oneOf := prepared.InputSchema["oneOf"].([]any)
		assertRequired(t, oneOf[0].(map[string]any), []string{"x", "y"})

		if len(original["required"].([]any)) != 5 {
			t.Fatal("original schema was mutated")
		}
	})

	t.Run("nil schema gets object fallback", func(t *testing.T) {
		prepared, ok := PrepareFunctionProviderTool(CoreTool{Name: "get_weather"})
		if !ok {
			t.Fatal("PrepareFunctionProviderTool() ok = false, want true")
		}
		if prepared.InputSchema["type"] != "object" {
			t.Errorf("fallback schema type = %v, want object", prepared.InputSchema["type"])
		}
	})
}

func TestNormalizeFunctionToolSchemaDeduplicatesRawMessageRequired(t *testing.T) {
	schema := map[string]any{
		"type":     "object",
		"required": json.RawMessage(`["action","app","action"]`),
		"properties": map[string]json.RawMessage{
			"nested": json.RawMessage(`{"type":"object","required":["id","id"]}`),
		},
		"oneOf": []json.RawMessage{
			json.RawMessage(`{"type":"object","required":["x","x","y"]}`),
		},
	}

	normalized := NormalizeFunctionToolSchema(schema)
	assertRequired(t, normalized, []string{"action", "app"})

	props := normalized["properties"].(map[string]any)
	nested := props["nested"].(map[string]any)
	assertRequired(t, nested, []string{"id"})

	oneOf := normalized["oneOf"].([]any)
	assertRequired(t, oneOf[0].(map[string]any), []string{"x", "y"})
	if _, ok := schema["required"].(json.RawMessage); !ok {
		t.Fatal("original raw required was mutated")
	}
}

func assertRequired(t *testing.T, schema map[string]any, want []string) {
	t.Helper()

	raw, ok := schema["required"].([]any)
	if !ok {
		t.Fatalf("required = %T, want []any", schema["required"])
	}
	got := make([]string, 0, len(raw))
	for _, item := range raw {
		name, ok := item.(string)
		if !ok {
			t.Fatalf("required item = %T, want string", item)
		}
		got = append(got, name)
	}
	if len(got) != len(want) {
		t.Fatalf("required = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("required = %#v, want %#v", got, want)
		}
	}
}
