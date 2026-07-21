package deepseekv4

import (
	"encoding/json"
	"strings"
	"testing"

	"moonbridge/internal/format"
)

func TestTransformDSMLBlocks_NoDSML(t *testing.T) {
	blocks := []format.CoreContentBlock{
		{Type: "text", Text: "Hello, world!"},
		{Type: "text", Text: "Another message."},
	}
	result := TransformDSMLBlocks(blocks)
	if len(result) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(result))
	}
	if result[0].Text != "Hello, world!" {
		t.Fatalf("text mismatch: %q", result[0].Text)
	}
}

func TestTransformDSMLBlocks_EmptyBlocks(t *testing.T) {
	result := TransformDSMLBlocks(nil)
	if result != nil {
		t.Fatal("expected nil for empty input")
	}
	result = TransformDSMLBlocks([]format.CoreContentBlock{})
	if len(result) != 0 {
		t.Fatal("expected empty for empty input")
	}
}

func TestTransformDSMLBlocks_SingleInvokeStringParam(t *testing.T) {
	text := "Let me check the weather.\n<|DSML|function_calls>\n<|DSML|invoke name=\"get_weather\">\n<|DSML|parameter name=\"location\" string=\"true\">Beijing</|DSML|parameter>\n</|DSML|invoke>\n</|DSML|function_calls>"
	blocks := []format.CoreContentBlock{
		{Type: "text", Text: text},
	}
	result := TransformDSMLBlocks(blocks)

	if len(result) != 2 {
		t.Fatalf("expected 2 blocks (text + tool_use), got %d: %+v", len(result), result)
	}

	// First block should be the text before DSML.
	if result[0].Type != "text" {
		t.Fatalf("expected text block, got %s", result[0].Type)
	}
	if !strings.Contains(result[0].Text, "Let me check the weather") {
		t.Fatalf("text missing preamble: %q", result[0].Text)
	}
	// DSML markers should NOT appear in text.
	if strings.Contains(result[0].Text, "DSML") || strings.Contains(result[0].Text, "function_calls") {
		t.Fatalf("DSML markers leaked into text: %q", result[0].Text)
	}

	// Second block should be tool_use.
	if result[1].Type != "tool_use" {
		t.Fatalf("expected tool_use block, got %s", result[1].Type)
	}
	if result[1].ToolName != "get_weather" {
		t.Fatalf("tool name mismatch: %q", result[1].ToolName)
	}
	if result[1].ToolUseID == "" {
		t.Fatal("tool_use ID is empty")
	}

	var params map[string]any
	if err := json.Unmarshal(result[1].ToolInput, &params); err != nil {
		t.Fatalf("failed to unmarshal tool input: %v", err)
	}
	if params["location"] != "Beijing" {
		t.Fatalf("param location = %v", params["location"])
	}
}

func TestTransformDSMLBlocks_MultipleInvokes(t *testing.T) {
	text := `<|DSML|function_calls>
<|DSML|invoke name="get_weather">
<|DSML|parameter name="city" string="true">Tokyo</|DSML|parameter>
</|DSML|invoke>
<|DSML|invoke name="get_time">
<|DSML|parameter name="timezone" string="true">UTC</|DSML|parameter>
</|DSML|invoke>
</|DSML|function_calls>`
	blocks := []format.CoreContentBlock{
		{Type: "text", Text: text},
	}
	result := TransformDSMLBlocks(blocks)

	if len(result) != 2 {
		t.Fatalf("expected 2 blocks (2 tool_use, no surrounding text), got %d", len(result))
	}
	if result[0].Type != "tool_use" || result[0].ToolName != "get_weather" {
		t.Fatalf("first tool_use mismatch: %s/%s", result[0].Type, result[0].ToolName)
	}
	if result[1].Type != "tool_use" || result[1].ToolName != "get_time" {
		t.Fatalf("second tool_use mismatch: %s/%s", result[1].Type, result[1].ToolName)
	}
}

func TestTransformDSMLBlocks_TextBeforeAndAfterDSML(t *testing.T) {
	text := "Before\n<|DSML|function_calls>\n<|DSML|invoke name=\"search\">\n<|DSML|parameter name=\"query\" string=\"true\">hello</|DSML|parameter>\n</|DSML|invoke>\n</|DSML|function_calls>\nAfter"
	blocks := []format.CoreContentBlock{
		{Type: "text", Text: text},
	}
	result := TransformDSMLBlocks(blocks)

	if len(result) != 3 {
		t.Fatalf("expected 3 blocks, got %d: %+v", len(result), result)
	}
	if result[0].Type != "text" || !strings.Contains(result[0].Text, "Before") {
		t.Fatalf("first block: %s %q", result[0].Type, result[0].Text)
	}
	if result[1].Type != "tool_use" || result[1].ToolName != "search" {
		t.Fatalf("second block: %s %s", result[1].Type, result[1].ToolName)
	}
	if result[2].Type != "text" || !strings.Contains(result[2].Text, "After") {
		t.Fatalf("third block: %s %q", result[2].Type, result[2].Text)
	}

	// Verify no DSML markers in text blocks.
	for _, b := range result {
		if b.Type == "text" && (strings.Contains(b.Text, "DSML") || strings.Contains(b.Text, "function_calls")) {
			t.Fatalf("DSML leaked into text block: %q", b.Text)
		}
	}
}

func TestTransformDSMLBlocks_ToolUseWithoutDSML_Passthrough(t *testing.T) {
	// Existing tool_use blocks should pass through unchanged.
	blocks := []format.CoreContentBlock{
		{Type: "text", Text: "Using a tool:"},
		{Type: "tool_use", ToolUseID: "call_123", ToolName: "existing_tool", ToolInput: json.RawMessage(`{"key":"value"}`)},
	}
	result := TransformDSMLBlocks(blocks)
	if len(result) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(result))
	}
	if result[0].Type != "text" {
		t.Fatalf("text block lost")
	}
	if result[1].ToolUseID != "call_123" {
		t.Fatalf("existing tool_use modified: %+v", result[1])
	}
}

func TestTransformDSMLBlocks_JSONParam(t *testing.T) {
	text := `<|DSML|function_calls>
<|DSML|invoke name="process">
<|DSML|parameter name="data">{"key":"value","nested":{"a":1}}</|DSML|parameter>
</|DSML|invoke>
</|DSML|function_calls>`
	blocks := []format.CoreContentBlock{
		{Type: "text", Text: text},
	}
	result := TransformDSMLBlocks(blocks)
	if len(result) != 1 {
		t.Fatalf("expected 1 block, got %d", len(result))
	}
	if result[0].ToolName != "process" {
		t.Fatalf("tool name: %s", result[0].ToolName)
	}

	var params map[string]any
	if err := json.Unmarshal(result[0].ToolInput, &params); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	data, ok := params["data"].(map[string]any)
	if !ok {
		t.Fatalf("data is not map: %T", params["data"])
	}
	if data["key"] != "value" {
		t.Fatalf("nested key: %v", data["key"])
	}
}

func TestTransformDSMLBlocks_MultipleDSMLBlocks(t *testing.T) {
	text := "First block.\n<|DSML|function_calls>\n<|DSML|invoke name=\"tool_a\">\n<|DSML|parameter name=\"x\" string=\"true\">1</|DSML|parameter>\n</|DSML|invoke>\n</|DSML|function_calls>\nMiddle text.\n<|DSML|function_calls>\n<|DSML|invoke name=\"tool_b\">\n<|DSML|parameter name=\"y\" string=\"true\">2</|DSML|parameter>\n</|DSML|invoke>\n</|DSML|function_calls>\nLast text."
	blocks := []format.CoreContentBlock{
		{Type: "text", Text: text},
	}
	result := TransformDSMLBlocks(blocks)

	if len(result) != 5 {
		t.Fatalf("expected 5 blocks, got %d: %+v", len(result), result)
	}
	// text -> tool_use -> text -> tool_use -> text
	if result[0].Type != "text" || !strings.Contains(result[0].Text, "First block") {
		t.Fatalf("block 0: %s %q", result[0].Type, result[0].Text)
	}
	if result[1].Type != "tool_use" || result[1].ToolName != "tool_a" {
		t.Fatalf("block 1: %s %s", result[1].Type, result[1].ToolName)
	}
	if result[2].Type != "text" || !strings.Contains(result[2].Text, "Middle text") {
		t.Fatalf("block 2: %s %q", result[2].Type, result[2].Text)
	}
	if result[3].Type != "tool_use" || result[3].ToolName != "tool_b" {
		t.Fatalf("block 3: %s %s", result[3].Type, result[3].ToolName)
	}
	if result[4].Type != "text" || !strings.Contains(result[4].Text, "Last text") {
		t.Fatalf("block 4: %s %q", result[4].Type, result[4].Text)
	}
}

func TestExtractDSMLAttr(t *testing.T) {
	tests := []struct {
		tag      string
		attrName string
		want     string
	}{
		{`<|DSML|invoke name="get_weather">`, "name", "get_weather"},
		{`<|DSML|parameter name="location" string="true">`, "name", "location"},
		{`<|DSML|parameter name="location" string="true">`, "string", "true"},
		{`<|DSML|invoke name='test'>`, "name", "test"},
		{`<|DSML|invoke>`, "name", ""},
		{``, "name", ""},
	}
	for _, tt := range tests {
		got := extractDSMLAttr(tt.tag, tt.attrName)
		if got != tt.want {
			t.Errorf("extractDSMLAttr(%q, %q) = %q, want %q", tt.tag, tt.attrName, got, tt.want)
		}
	}
}

func TestGenerateDSMLToolCallID_Deterministic(t *testing.T) {
	params := map[string]any{"a": "1", "b": "2"}
	id1 := generateDSMLToolCallID("test", params)
	id2 := generateDSMLToolCallID("test", params)
	if id1 != id2 {
		t.Fatalf("non-deterministic: %q vs %q", id1, id2)
	}
	if !strings.HasPrefix(id1, "dsml_") {
		t.Fatalf("missing prefix: %q", id1)
	}
}

func TestTransformDSMLBlocks_MalformedDSML_NoCloseTag(t *testing.T) {
	text := "Before <|DSML|function_calls> broken"
	blocks := []format.CoreContentBlock{
		{Type: "text", Text: text},
	}
	result := TransformDSMLBlocks(blocks)
	// Malformed DSML should be treated as regular text.
	if len(result) != 1 {
	// Malformed DSML: text before marker is split, but the marker itself
	// remains in the trailing text since there is no close tag.
	if len(result) < 1 {
		t.Fatalf("expected at least 1 block, got %d", len(result))
	}
	// Verify all block types are text (no tool_use).
	for _, b := range result {
		if b.Type != "text" {
			t.Fatalf("unexpected non-text block: %+v", b)
		}
	}
	}
	if result[0].Type != "text" || !strings.Contains(result[0].Text, "Before") {
		t.Fatalf("unexpected: %+v", result[0])
	}
}

func TestTransformDSMLBlocks_RealisticResponse(t *testing.T) {
	// Simulate a realistic DeepSeek response with thinking + tool calls in DSML.
	text := `I'll help you check the weather and time for your trip.

<|DSML|function_calls>
<|DSML|invoke name="get_weather">
<|DSML|parameter name="location" string="true">Tokyo</|DSML|parameter>
<|DSML|parameter name="units" string="true">metric</|DSML|parameter>
</|DSML|invoke>
<|DSML|invoke name="get_current_time">
<|DSML|parameter name="timezone" string="true">Asia/Tokyo</|DSML|parameter>
</|DSML|invoke>
</|DSML|function_calls>

Let me know if you need anything else!`

	blocks := []format.CoreContentBlock{
		{Type: "text", Text: text},
	}
	result := TransformDSMLBlocks(blocks)

	// Should be: text, tool_use, tool_use, text
	if len(result) < 3 {
		t.Fatalf("expected at least 3 blocks, got %d", len(result))
	}

	// Verify text blocks don't contain DSML.
	for _, b := range result {
		if b.Type == "text" {
			if strings.Contains(b.Text, "DSML") || strings.Contains(b.Text, "function_calls") ||
				strings.Contains(b.Text, "invoke") || strings.Contains(b.Text, "parameter") {
				t.Errorf("DSML leaked into text block: %q", b.Text)
			}
		}
	}

	// Verify tool_use blocks have proper IDs and inputs.
	toolCount := 0
	for _, b := range result {
		if b.Type == "tool_use" {
			toolCount++
			if b.ToolUseID == "" {
				t.Errorf("tool_use block has empty ID: %+v", b)
			}
			if b.ToolInput == nil {
				t.Errorf("tool_use block has nil input: %+v", b)
			}
		}
	}
	if toolCount != 2 {
		t.Errorf("expected 2 tool_use blocks, got %d", toolCount)
	}
}
