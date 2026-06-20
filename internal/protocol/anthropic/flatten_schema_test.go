package anthropic

import (
	"context"
	"testing"

	"moonbridge/internal/extension/codextool"
	"moonbridge/internal/format"
)

type flattenNoopCacheManager struct{}

func (flattenNoopCacheManager) PlanAndInject(_ context.Context, _ *MessageRequest, _ *format.CoreRequest) (key, ttl string) {
	return "", ""
}

func (flattenNoopCacheManager) UpdateRegistry(_ context.Context, _, _ string, _ Usage) {}

func TestFlattenSchemaComposition_FlattensOneOf(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"oneOf": []any{
			map[string]any{
				"properties": map[string]any{
					"action": map[string]any{"type": "string", "enum": []any{"a"}},
					"x":      map[string]any{"type": "string"},
				},
				"additionalProperties": false,
				"required":             []any{"action", "x"},
			},
			map[string]any{
				"properties": map[string]any{
					"action": map[string]any{"type": "string", "enum": []any{"b"}},
					"y":      map[string]any{"type": "number"},
				},
				"additionalProperties": false,
				"required":             []any{"action", "y"},
			},
		},
	}

	result := flattenSchemaComposition(schema)
	if result == nil {
		t.Fatal("flattenSchemaComposition returned nil")
	}

	// Should have type: object
	if result["type"] != "object" {
		t.Errorf("type = %v, want object", result["type"])
	}

	// Should NOT have oneOf
	if _, ok := result["oneOf"]; ok {
		t.Error("oneOf should be removed from top level")
	}

	// Should NOT have required
	if _, ok := result["required"]; ok {
		t.Error("required should be removed from top level")
	}

	// Should have additionalProperties: false
	if ap, ok := result["additionalProperties"]; !ok || ap != false {
		t.Error("additionalProperties should be false")
	}

	// Should have merged properties
	props, ok := result["properties"].(map[string]any)
	if !ok {
		t.Fatal("properties not found or wrong type")
	}

	// Action should have merged enum [a, b]
	actionSchema, ok := props["action"].(map[string]any)
	if !ok {
		t.Fatal("action property not found")
	}
	if actionSchema["type"] != "string" {
		t.Errorf("action type = %v, want string", actionSchema["type"])
	}
	enumVals, ok := actionSchema["enum"].([]any)
	if !ok || len(enumVals) != 2 {
		t.Fatalf("action enum should have 2 values, got %v", enumVals)
	}

	// x and y should be present
	if _, ok := props["x"]; !ok {
		t.Error("x property missing from merged properties")
	}
	if _, ok := props["y"]; !ok {
		t.Error("y property missing from merged properties")
	}
}

func TestFlattenSchemaComposition_HandlesNormalSchema(t *testing.T) {
	// Schema without oneOf should return nil (no flattening needed)
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{"type": "string"},
		},
	}

	result := flattenSchemaComposition(schema)
	if result != nil {
		t.Error("normal schema should return nil, got non-nil")
	}
}

func TestFlattenSchemaComposition_DetectsAllOf(t *testing.T) {
	schema := map[string]any{
		"allOf": []any{
			map[string]any{"properties": map[string]any{"a": map[string]any{"type": "string"}}},
			map[string]any{"properties": map[string]any{"b": map[string]any{"type": "number"}}},
		},
	}

	result := flattenSchemaComposition(schema)
	if result == nil {
		t.Fatal("flattenSchemaComposition returned nil for allOf")
	}
	if _, ok := result["allOf"]; ok {
		t.Error("allOf should be removed from top level")
	}
	if result["type"] != "object" {
		t.Errorf("type = %v, want object", result["type"])
	}
}

func TestFlattenSchemaComposition_MergesActionEnums(t *testing.T) {
	// Simulates multi_agent_v1 style
	schema := map[string]any{
		"oneOf": []any{
			map[string]any{
				"properties": map[string]any{
					"action": map[string]any{"enum": []any{"close_agent"}},
					"target": map[string]any{"type": "string"},
				},
			},
			map[string]any{
				"properties": map[string]any{
					"action": map[string]any{"enum": []any{"spawn_agent"}},
					"model":  map[string]any{"type": "string"},
				},
			},
		},
	}

	result := flattenSchemaComposition(schema)
	if result == nil {
		t.Fatal("flattenSchemaComposition returned nil")
	}

	props := result["properties"].(map[string]any)
	actionSchema := props["action"].(map[string]any)
	enumVals := actionSchema["enum"].([]any)

	if len(enumVals) != 2 {
		t.Fatalf("expected 2 enum values, got %d: %v", len(enumVals), enumVals)
	}

	// Verify both action values are present
	vals := make(map[string]bool)
	for _, v := range enumVals {
		vals[v.(string)] = true
	}
	if !vals["close_agent"] || !vals["spawn_agent"] {
		t.Errorf("unexpected enum values: %v", vals)
	}

	// Verify the merged 'action' schema has type string
	if actionSchema["type"] != "string" {
		t.Errorf("action type = %v, want string", actionSchema["type"])
	}
}

func TestFlattenSchemaComposition_FlattensBuildNestedOneOfSchema(t *testing.T) {
	tools, err := codextool.BuildNamespaceTools(
		[]string{"close_agent", "spawn_agent"},
		map[string]format.CoreTool{
			"close_agent": {
				Name: "close_agent",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"target": map[string]any{"type": "string"},
					},
					"required": []any{"target"},
				},
			},
			"spawn_agent": {
				Name: "spawn_agent",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"message": map[string]any{"type": "string"},
					},
					"required": []any{"message"},
				},
			},
		},
		"multi_agent_v1",
		codextool.NestedOneOf,
	)
	if err != nil {
		t.Fatalf("BuildNamespaceTools returned error: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("got %d tools, want 1", len(tools))
	}

	result := flattenSchemaComposition(tools[0].InputSchema)
	if result == nil {
		t.Fatal("flattenSchemaComposition returned nil for BuildNamespaceTools schema")
	}
	if _, ok := result["oneOf"]; ok {
		t.Fatal("oneOf should be removed from top level")
	}

	props, ok := result["properties"].(map[string]any)
	if !ok {
		t.Fatal("properties not found or wrong type")
	}
	if _, ok := props["target"]; !ok {
		t.Fatal("target property missing")
	}
	if _, ok := props["message"]; !ok {
		t.Fatal("message property missing")
	}

	actionSchema, ok := props["action"].(map[string]any)
	if !ok {
		t.Fatal("action property missing")
	}
	enumVals, ok := actionSchema["enum"].([]any)
	if !ok {
		t.Fatalf("action enum type = %T, want []any", actionSchema["enum"])
	}
	if len(enumVals) != 2 {
		t.Fatalf("got %d enum values, want 2: %v", len(enumVals), enumVals)
	}
}

func TestFromCoreRequest_FlattensNamespaceOneOfToolSchema(t *testing.T) {
	namespaceTools, err := codextool.BuildNamespaceTools(
		[]string{"close_agent", "spawn_agent"},
		map[string]format.CoreTool{
			"close_agent": {
				Name: "close_agent",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"target": map[string]any{"type": "string"},
					},
					"required": []any{"target"},
				},
			},
			"spawn_agent": {
				Name: "spawn_agent",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"message": map[string]any{"type": "string"},
					},
					"required": []any{"message"},
				},
			},
		},
		"multi_agent_v1",
		codextool.NestedOneOf,
	)
	if err != nil {
		t.Fatalf("BuildNamespaceTools returned error: %v", err)
	}
	if len(namespaceTools) != 1 {
		t.Fatalf("got %d namespace tools, want 1", len(namespaceTools))
	}

	adapter := NewAnthropicProviderAdapter(0, flattenNoopCacheManager{}, format.CorePluginHooks{})
	coreReq := &format.CoreRequest{
		Model: "claude-sonnet-4",
		Messages: []format.CoreMessage{
			{Role: "user", Content: []format.CoreContentBlock{{Type: "text", Text: "call a namespace tool"}}},
		},
		Tools: namespaceTools,
	}

	result, err := adapter.FromCoreRequest(t.Context(), coreReq)
	if err != nil {
		t.Fatal(err)
	}
	msgReq := result.(*MessageRequest)
	if len(msgReq.Tools) != 1 {
		t.Fatalf("got %d tools, want 1", len(msgReq.Tools))
	}

	schema := msgReq.Tools[0].InputSchema
	if _, ok := schema["oneOf"]; ok {
		t.Fatalf("oneOf should be removed from outgoing Anthropic schema: %v", schema)
	}
	if schema["type"] != "object" {
		t.Fatalf("type = %v, want object", schema["type"])
	}
	if _, ok := schema["properties"].(map[string]any); !ok {
		t.Fatalf("properties missing from outgoing Anthropic schema: %v", schema)
	}
}

func TestDetectCompositionKeyword(t *testing.T) {
	tests := []struct {
		name string
		sch  map[string]any
		want string
	}{
		{"oneOf", map[string]any{"oneOf": []any{}}, "oneOf"},
		{"allOf", map[string]any{"allOf": []any{}}, "allOf"},
		{"anyOf", map[string]any{"anyOf": []any{}}, "anyOf"},
		{"none", map[string]any{"type": "object"}, ""},
		{"empty", map[string]any{}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := detectCompositionKeyword(tt.sch)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFlattenSchemaComposition_NonNestedProperties(t *testing.T) {
	// Properties that aren't maps should be preserved too
	schema := map[string]any{
		"oneOf": []any{
			map[string]any{
				"properties": map[string]any{
					"action": map[string]any{"enum": []any{"ping"}},
					"count":  map[string]any{"type": "integer"},
				},
			},
		},
	}

	result := flattenSchemaComposition(schema)
	if result == nil {
		t.Fatal("flattenSchemaComposition returned nil")
	}

	props := result["properties"].(map[string]any)
	if _, ok := props["count"]; !ok {
		t.Error("count property missing")
	}
}
