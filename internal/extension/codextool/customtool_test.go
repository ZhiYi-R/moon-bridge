package codextool

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRebuildApplyPatchGrammarUpdateFileIncludesValidPatchMarkers(t *testing.T) {
	input := json.RawMessage(`{
		"path":"internal/example.go",
		"move_to":"internal/example_v2.go",
		"hunks":[
			{
				"context":"func demo()",
				"lines":[
					{"op":"context","text":"func demo() {"},
					{"op":"remove","text":"\told()"},
					{"op":"add","text":"\tnew()"},
					{"op":"context","text":"}"}
				]
			}
		]
	}`)

	got := RebuildApplyPatchGrammar("apply_patch_update_file", input)

	for _, want := range []string{
		"*** Begin Patch\n",
		"*** Update File: internal/example.go\n",
		"*** Move to: internal/example_v2.go\n",
		"@@ func demo()\n",
		" func demo() {\n",
		"-\told()\n",
		"+\tnew()\n",
		" }\n",
		"*** End Patch\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("rebuilt patch missing %q:\n%s", want, got)
		}
	}
}

func TestRebuildApplyPatchGrammarBatchPreservesAllOperations(t *testing.T) {
	input := json.RawMessage(`{
		"operations":[
			{"type":"add_file","path":"new.txt","content":"hello\nworld"},
			{"type":"delete_file","path":"old.txt"},
			{
				"type":"update_file",
				"path":"edit.txt",
				"hunks":[
					{
						"context":"header",
						"lines":[
							{"op":"context","text":"same"},
							{"op":"add","text":"added"}
						]
					}
				]
			}
		]
	}`)

	got := RebuildApplyPatchGrammar("apply_patch_batch", input)

	if strings.Count(got, "*** Begin Patch\n") != 3 {
		t.Fatalf("expected 3 begin markers, got:\n%s", got)
	}
	for _, want := range []string{
		"*** Add File: new.txt\n+hello\n+world\n*** End Patch\n",
		"*** Delete File: old.txt\n*** End Patch\n",
		"*** Update File: edit.txt\n@@ header\n same\n+added\n*** End Patch\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("rebuilt batch missing %q:\n%s", want, got)
		}
	}
}

func TestRebuildGrammarUsesRawInputForGenericCustomTools(t *testing.T) {
	got := RebuildGrammar("custom_tool", json.RawMessage(`{"input":"plain freeform body"}`))
	if got != "plain freeform body" {
		t.Fatalf("RebuildGrammar() = %q, want raw input", got)
	}
}

func TestOutputItemFromBlockForToolNestedNamespace(t *testing.T) {
	toolMap := ToolMap{
		"mcp__filesystem": ToolSpec{
			Kind:       ToolNestedNamespace,
			OpenAIName: "mcp__filesystem",
		},
	}

	input := json.RawMessage(`{
		"action": "read_file",
		"params": {
			"path": "/Users/francesco.mosca/Work/explorations/README.md"
		}
	}`)

	itemType, itemName, itemNamespace, toolInputStr, isLocalShell, _ := OutputItemFromBlock(
		"mcp__filesystem",
		input,
		toolMap,
	)

	if itemType != "function_call" {
		t.Errorf("expected itemType function_call, got %s", itemType)
	}
	if itemName != "read_file" {
		t.Errorf("expected itemName read_file, got %s", itemName)
	}
	if itemNamespace != "mcp__filesystem" {
		t.Errorf("expected itemNamespace mcp__filesystem, got %s", itemNamespace)
	}
	if !strings.Contains(toolInputStr, `"/Users/francesco.mosca/Work/explorations/README.md"`) {
		t.Errorf("expected toolInputStr to contain path, got %s", toolInputStr)
	}
	if isLocalShell {
		t.Errorf("expected isLocalShell false, got true")
	}
}
