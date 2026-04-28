package bridge_test

import (
	"encoding/json"
	"strings"
	"testing"

	"moonbridge/internal/foundation/openai"
	"moonbridge/internal/protocol/anthropic"
)

func applyPatchGrammarForTest() string {
	return "start: begin_patch hunk+ end_patch\nbegin_patch: \"*** Begin Patch\" LF\nend_patch: \"*** End Patch\" LF?\nadd_hunk: \"*** Add File: \" filename LF add_line+\ndelete_hunk: \"*** Delete File: \" filename LF\nupdate_hunk: \"*** Update File: \" filename LF\nfilename: /(.+)/\nadd_line: \"+\" /(.*)/ LF -> line\n"
}

type applyPatchHistoryInput struct {
	Operations []struct {
		Type    string `json:"type"`
		Path    string `json:"path"`
		Content string `json:"content"`
	} `json:"operations"`
}

func TestToAnthropicPreservesCustomToolGrammar(t *testing.T) {
	request := openai.ResponsesRequest{
		Model: "gpt-test",
		Input: json.RawMessage(`"rewrite buffer"`),
		Tools: []openai.Tool{{
			Type:        "custom",
			Name:        "rewrite_buffer",
			Description: "Rewrite a buffer using freeform text.",
			Format: map[string]any{
				"type":       "grammar",
				"syntax":     "lark",
				"definition": "start: line+\nline: /.+/ NEWLINE\n%import common.NEWLINE\n",
			},
		}},
	}

	converted, _, err := testBridge().ToAnthropic(request, nil)
	if err != nil {
		t.Fatalf("ToAnthropic(, nil) error = %v", err)
	}
	if len(converted.Tools) != 1 {
		t.Fatalf("tools = %+v", converted.Tools)
	}
	tool := converted.Tools[0]
	if tool.Name != "rewrite_buffer" {
		t.Fatalf("tool = %+v", tool)
	}
	inputSchema, ok := tool.InputSchema["properties"].(map[string]any)["input"].(map[string]any)
	if !ok {
		t.Fatalf("input schema = %+v", tool.InputSchema)
	}
	description, _ := inputSchema["description"].(string)
	if !strings.Contains(tool.Description, "start: line+") || !strings.Contains(description, "start: line+") {
		t.Fatalf("custom grammar was not preserved: tool=%q input=%q", tool.Description, description)
	}
}

func TestFromAnthropicMapsRequestCustomToolToCustomToolCall(t *testing.T) {
	input := "replace this buffer\nwith this text\n"
	request := openai.ResponsesRequest{
		Model: "gpt-test",
		Tools: []openai.Tool{{Type: "custom", Name: "rewrite_buffer"}},
	}
	response := anthropic.MessageResponse{
		ID:         "msg_123",
		Type:       "message",
		Role:       "assistant",
		StopReason: "tool_use",
		Content: []anthropic.ContentBlock{{
			Type:  "tool_use",
			ID:    "tool_rewrite",
			Name:  "rewrite_buffer",
			Input: mustMarshalRaw(t, map[string]any{"input": input}),
		}},
	}

	bridgeUnderTest := testBridge()
	converted := bridgeUnderTest.FromAnthropicWithContext(response, "gpt-test", bridgeUnderTest.ConversionContext(request))
	if len(converted.Output) != 1 {
		t.Fatalf("output = %+v", converted.Output)
	}
	item := converted.Output[0]
	if item.Type != "custom_tool_call" || item.CallID != "tool_rewrite" || item.Name != "rewrite_buffer" || item.Input != input {
		t.Fatalf("custom tool call = %+v", item)
	}
	if item.Arguments != "" {
		t.Fatalf("Arguments = %q, want empty for custom tool", item.Arguments)
	}
}

func TestFromAnthropicNormalizesApplyPatchGrammarTerminator(t *testing.T) {
	input := "*** Begin Patch\n*** Add File: docs/api.md\n+# API\n+content\n+*** End of File\n+*** End Patch"
	request := openai.ResponsesRequest{
		Model: "gpt-test",
		Tools: []openai.Tool{{
			Type: "custom",
			Name: "patcher",
			Format: map[string]any{
				"type":       "grammar",
				"syntax":     "lark",
				"definition": "begin_patch: \"*** Begin Patch\"\nend_patch: \"*** End Patch\"\nadd_hunk: \"*** Add File: \"\n",
			},
		}},
	}
	response := anthropic.MessageResponse{
		ID:         "msg_123",
		Type:       "message",
		Role:       "assistant",
		StopReason: "tool_use",
		Content: []anthropic.ContentBlock{{
			Type:  "tool_use",
			ID:    "tool_patch",
			Name:  "patcher",
			Input: mustMarshalRaw(t, map[string]any{"input": input}),
		}},
	}

	bridgeUnderTest := testBridge()
	converted := bridgeUnderTest.FromAnthropicWithContext(response, "gpt-test", bridgeUnderTest.ConversionContext(request))
	if len(converted.Output) != 1 {
		t.Fatalf("output = %+v", converted.Output)
	}
	got := converted.Output[0].Input
	if strings.Contains(got, "+*** End Patch") || strings.Contains(got, "+*** End of File") {
		t.Fatalf("patch terminators were not normalized: %q", got)
	}
	if !strings.HasSuffix(got, "*** End Patch") {
		t.Fatalf("patch = %q", got)
	}
}

func TestToAnthropicSplitsApplyPatchGrammarToolIntoSchemaProxyCollection(t *testing.T) {
	request := openai.ResponsesRequest{
		Model: "gpt-test",
		Input: json.RawMessage(`"write docs"`),
		Tools: []openai.Tool{{
			Type:   "custom",
			Name:   "apply_patch",
			Format: map[string]any{"type": "grammar", "syntax": "lark", "definition": applyPatchGrammarForTest()},
		}},
	}

	converted, _, err := testBridge().ToAnthropic(request, nil)
	if err != nil {
		t.Fatalf("ToAnthropic(, nil) error = %v", err)
	}
	if len(converted.Tools) != 5 {
		t.Fatalf("tools = %+v", converted.Tools)
	}
	byName := map[string]anthropic.Tool{}
	for _, tool := range converted.Tools {
		byName[tool.Name] = tool
		if strings.Contains(tool.Description, "FREEFORM") || strings.Contains(tool.Description, "OpenAI custom tool grammar") {
			t.Fatalf("apply_patch proxy description should not expose contradictory raw grammar guidance: %q", tool.Description)
		}
	}
	for _, name := range []string{"apply_patch_add_file", "apply_patch_delete_file", "apply_patch_update_file", "apply_patch_replace_file", "apply_patch_batch"} {
		if _, ok := byName[name]; !ok {
			t.Fatalf("missing split apply_patch tool %q in %+v", name, converted.Tools)
		}
	}
	properties, ok := byName["apply_patch_batch"].InputSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema = %+v", byName["apply_patch_batch"].InputSchema)
	}
	if _, ok := properties["operations"]; !ok {
		t.Fatalf("operations schema missing: %+v", properties)
	}
	if _, ok := properties["input"]; ok {
		t.Fatalf("raw input hole should not be the primary apply_patch schema: %+v", properties)
	}
	if _, ok := properties["raw_patch"]; ok {
		t.Fatalf("raw patch should not be exposed in apply_patch schema: %+v", properties)
	}
	operationSchema := properties["operations"].(map[string]any)["items"].(map[string]any)
	operationProperties := operationSchema["properties"].(map[string]any)
	if _, ok := operationProperties["changes"]; ok {
		t.Fatalf("raw changes should not be exposed in apply_patch operation schema: %+v", operationProperties)
	}
	if byName["apply_patch_batch"].InputSchema["additionalProperties"] != false || operationSchema["additionalProperties"] != false {
		t.Fatalf("schema should reject extra properties: %+v", byName["apply_patch_batch"].InputSchema)
	}
	required, ok := byName["apply_patch_batch"].InputSchema["required"].([]string)
	if !ok || len(required) != 1 || required[0] != "operations" {
		t.Fatalf("required schema = %+v", byName["apply_patch_batch"].InputSchema["required"])
	}
	if _, ok := byName["apply_patch_add_file"].InputSchema["properties"].(map[string]any)["content"]; !ok {
		t.Fatalf("add_file schema = %+v", byName["apply_patch_add_file"].InputSchema)
	}
}

func TestToAnthropicConvertsExecGrammarToolToSourceSchemaWithoutRawGrammarDescription(t *testing.T) {
	request := openai.ResponsesRequest{
		Model: "gpt-test",
		Input: json.RawMessage(`"run js"`),
		Tools: []openai.Tool{{
			Type:        "custom",
			Name:        "exec",
			Description: "FREEFORM code mode tool",
			Format:      map[string]any{"type": "grammar", "syntax": "lark", "definition": "start: pragma_source | plain_source\npragma_source: \"// @exec:\" /.+/\nplain_source: /(.|\\n)+/\n"},
		}},
	}

	converted, _, err := testBridge().ToAnthropic(request, nil)
	if err != nil {
		t.Fatalf("ToAnthropic(, nil) error = %v", err)
	}
	description := converted.Tools[0].Description
	if strings.Contains(description, "FREEFORM") || strings.Contains(description, "OpenAI custom tool grammar") {
		t.Fatalf("exec proxy description should not expose raw grammar guidance: %q", description)
	}
	properties := converted.Tools[0].InputSchema["properties"].(map[string]any)
	if _, ok := properties["source"]; !ok {
		t.Fatalf("source schema missing: %+v", properties)
	}
}

func TestFromAnthropicBuildsApplyPatchGrammarFromProxyOperations(t *testing.T) {
	request := openai.ResponsesRequest{
		Model: "gpt-test",
		Tools: []openai.Tool{{
			Type:   "custom",
			Name:   "apply_patch",
			Format: map[string]any{"type": "grammar", "syntax": "lark", "definition": applyPatchGrammarForTest()},
		}},
	}
	response := anthropic.MessageResponse{
		ID:         "msg_123",
		Type:       "message",
		Role:       "assistant",
		StopReason: "tool_use",
		Content: []anthropic.ContentBlock{{
			Type:  "tool_use",
			ID:    "tool_patch",
			Name:  "apply_patch",
			Input: mustMarshalRaw(t, map[string]any{"operations": []map[string]any{{"type": "add_file", "path": "docs/api.md", "content": "# API\ncontent\n"}}}),
		}},
	}

	bridgeUnderTest := testBridge()
	converted := bridgeUnderTest.FromAnthropicWithContext(response, "gpt-test", bridgeUnderTest.ConversionContext(request))
	if len(converted.Output) != 1 {
		t.Fatalf("output = %+v", converted.Output)
	}
	want := "*** Begin Patch\n*** Add File: docs/api.md\n+# API\n+content\n*** End Patch"
	if converted.Output[0].Input != want {
		t.Fatalf("patch = %q, want %q", converted.Output[0].Input, want)
	}
}

func TestFromAnthropicBuildsApplyPatchGrammarFromSplitAddFileTool(t *testing.T) {
	request := openai.ResponsesRequest{
		Model: "gpt-test",
		Tools: []openai.Tool{{
			Type:   "custom",
			Name:   "apply_patch",
			Format: map[string]any{"type": "grammar", "syntax": "lark", "definition": applyPatchGrammarForTest()},
		}},
	}
	response := anthropic.MessageResponse{
		ID:         "msg_123",
		Type:       "message",
		Role:       "assistant",
		StopReason: "tool_use",
		Content: []anthropic.ContentBlock{{
			Type:  "tool_use",
			ID:    "tool_patch",
			Name:  "apply_patch_add_file",
			Input: mustMarshalRaw(t, map[string]any{"path": "docs/api.md", "content": "# API\ncontent\n"}),
		}},
	}

	bridgeUnderTest := testBridge()
	converted := bridgeUnderTest.FromAnthropicWithContext(response, "gpt-test", bridgeUnderTest.ConversionContext(request))
	if converted.Output[0].Name != "apply_patch" {
		t.Fatalf("output tool name = %q", converted.Output[0].Name)
	}
	want := "*** Begin Patch\n*** Add File: docs/api.md\n+# API\n+content\n*** End Patch"
	if converted.Output[0].Input != want {
		t.Fatalf("patch = %q, want %q", converted.Output[0].Input, want)
	}
}

func TestFromAnthropicBuildsApplyPatchReplacementFromUpdateContent(t *testing.T) {
	request := openai.ResponsesRequest{
		Model: "gpt-test",
		Tools: []openai.Tool{{
			Type:   "custom",
			Name:   "apply_patch",
			Format: map[string]any{"type": "grammar", "syntax": "lark", "definition": applyPatchGrammarForTest()},
		}},
	}
	response := anthropic.MessageResponse{
		ID:         "msg_123",
		Type:       "message",
		Role:       "assistant",
		StopReason: "tool_use",
		Content: []anthropic.ContentBlock{{
			Type:  "tool_use",
			ID:    "tool_patch",
			Name:  "apply_patch",
			Input: mustMarshalRaw(t, map[string]any{"operations": []map[string]any{{"type": "update_file", "path": "internal/app/app.go", "content": "package app\n\nconst Name = \"Moon Bridge\"\n"}}}),
		}},
	}

	bridgeUnderTest := testBridge()
	converted := bridgeUnderTest.FromAnthropicWithContext(response, "gpt-test", bridgeUnderTest.ConversionContext(request))
	want := "*** Begin Patch\n*** Delete File: internal/app/app.go\n*** Add File: internal/app/app.go\n+package app\n+\n+const Name = \"Moon Bridge\"\n*** End Patch"
	if converted.Output[0].Input != want {
		t.Fatalf("patch = %q, want %q", converted.Output[0].Input, want)
	}
}

func TestToAnthropicConvertsApplyPatchHistoryToProxyOperations(t *testing.T) {
	patch := "*** Begin Patch\n*** Add File: docs/api.md\n+# API\n+content\n*** End Patch"
	request := openai.ResponsesRequest{
		Model: "gpt-test",
		Input: mustMarshalRaw(t, []map[string]any{{
			"type":    "custom_tool_call",
			"call_id": "tool_patch",
			"name":    "apply_patch",
			"input":   patch,
		}}),
		Tools: []openai.Tool{{
			Type:   "custom",
			Name:   "apply_patch",
			Format: map[string]any{"type": "grammar", "syntax": "lark", "definition": applyPatchGrammarForTest()},
		}},
	}

	converted, _, err := testBridge().ToAnthropic(request, nil)
	if err != nil {
		t.Fatalf("ToAnthropic() error = %v", err)
	}
	toolUse := converted.Messages[0].Content[0]
	if toolUse.Name != "apply_patch_add_file" {
		t.Fatalf("tool name = %q, want apply_patch_add_file", toolUse.Name)
	}
	var input struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(converted.Messages[0].Content[0].Input, &input); err != nil {
		t.Fatalf("tool input JSON = %s: %v", string(converted.Messages[0].Content[0].Input), err)
	}
	if input.Path != "docs/api.md" || input.Content != "# API\ncontent" {
		t.Fatalf("proxy input = %+v", input)
	}
}

func TestToAnthropicConvertsExecGrammarToolToSourceSchema(t *testing.T) {
	request := openai.ResponsesRequest{
		Model: "gpt-test",
		Input: json.RawMessage(`"run js"`),
		Tools: []openai.Tool{{
			Type:   "custom",
			Name:   "exec",
			Format: map[string]any{"type": "grammar", "syntax": "lark", "definition": "start: pragma_source | plain_source\npragma_source: \"// @exec:\" /.+/\nplain_source: /(.|\\n)+/\n"},
		}},
	}

	converted, _, err := testBridge().ToAnthropic(request, nil)
	if err != nil {
		t.Fatalf("ToAnthropic() error = %v", err)
	}
	properties := converted.Tools[0].InputSchema["properties"].(map[string]any)
	if _, ok := properties["source"]; !ok {
		t.Fatalf("source schema missing: %+v", properties)
	}
}

func TestFromAnthropicBuildsExecGrammarFromSourceProxy(t *testing.T) {
	request := openai.ResponsesRequest{
		Model: "gpt-test",
		Tools: []openai.Tool{{
			Type:   "custom",
			Name:   "exec",
			Format: map[string]any{"type": "grammar", "syntax": "lark", "definition": "start: pragma_source | plain_source\npragma_source: \"// @exec:\" /.+/\nplain_source: /(.|\\n)+/\n"},
		}},
	}
	response := anthropic.MessageResponse{
		ID:         "msg_123",
		Type:       "message",
		Role:       "assistant",
		StopReason: "tool_use",
		Content: []anthropic.ContentBlock{{
			Type:  "tool_use",
			ID:    "tool_exec",
			Name:  "exec",
			Input: mustMarshalRaw(t, map[string]any{"source": "console.log(42);\n"}),
		}},
	}

	bridgeUnderTest := testBridge()
	converted := bridgeUnderTest.FromAnthropicWithContext(response, "gpt-test", bridgeUnderTest.ConversionContext(request))
	if converted.Output[0].Input != "console.log(42);\n" {
		t.Fatalf("exec input = %q", converted.Output[0].Input)
	}
}

func TestToAnthropicConvertsCustomToolCallHistoryToJSONInput(t *testing.T) {
	inputText := "replace this buffer\nwith this text\n"
	request := openai.ResponsesRequest{
		Model: "gpt-test",
		Input: mustMarshalRaw(t, []map[string]any{
			{"type": "custom_tool_call", "call_id": "tool_rewrite", "name": "rewrite_buffer", "input": inputText},
			{"type": "custom_tool_call_output", "call_id": "tool_rewrite", "output": "rewritten\n"},
		}),
	}

	converted, _, err := testBridge().ToAnthropic(request, nil)
	if err != nil {
		t.Fatalf("ToAnthropic() error = %v", err)
	}
	if len(converted.Messages) != 2 {
		t.Fatalf("messages = %+v", converted.Messages)
	}
	toolUse := converted.Messages[0].Content[0]
	if toolUse.Type != "tool_use" || toolUse.Name != "rewrite_buffer" || toolUse.ID != "tool_rewrite" {
		t.Fatalf("tool use = %+v", toolUse)
	}
	var input struct {
		Input string `json:"input"`
	}
	if err := json.Unmarshal(toolUse.Input, &input); err != nil {
		t.Fatalf("tool input is invalid JSON: %s: %v", string(toolUse.Input), err)
	}
	if input.Input != inputText {
		t.Fatalf("input = %q", input.Input)
	}
	if converted.Messages[1].Content[0].ToolUseID != "tool_rewrite" {
		t.Fatalf("tool result = %+v", converted.Messages[1].Content[0])
	}
}

func TestToAnthropicConvertsNamespacedCustomToolWithContext(t *testing.T) {
	request := openai.ResponsesRequest{
		Model: "gpt-test",
		Input: json.RawMessage(`"rewrite buffer"`),
		Tools: []openai.Tool{{
			Type: "namespace",
			Name: "mcp__editor__",
			Tools: []openai.Tool{{
				Type:        "custom",
				Name:        "rewrite_buffer",
				Description: "Rewrite a buffer using freeform text.",
				Format: map[string]any{
					"type":       "grammar",
					"syntax":     "lark",
					"definition": "start: line+\n",
				},
			}},
		}},
	}

	bridgeUnderTest := testBridge()
	convertedRequest, _, err := bridgeUnderTest.ToAnthropic(request, nil)
	if err != nil {
		t.Fatalf("ToAnthropic(, nil) error = %v", err)
	}
	if len(convertedRequest.Tools) != 1 || convertedRequest.Tools[0].Name != "mcp__editor__rewrite_buffer" {
		t.Fatalf("tools = %+v", convertedRequest.Tools)
	}

	response := anthropic.MessageResponse{
		ID:         "msg_123",
		Type:       "message",
		Role:       "assistant",
		StopReason: "tool_use",
		Content: []anthropic.ContentBlock{{
			Type:  "tool_use",
			ID:    "tool_rewrite",
			Name:  "mcp__editor__rewrite_buffer",
			Input: mustMarshalRaw(t, map[string]any{"input": "new text\n"}),
		}},
	}
	convertedResponse := bridgeUnderTest.FromAnthropicWithContext(response, "gpt-test", bridgeUnderTest.ConversionContext(request))
	if len(convertedResponse.Output) != 1 || convertedResponse.Output[0].Type != "custom_tool_call" {
		t.Fatalf("output = %+v", convertedResponse.Output)
	}
}
