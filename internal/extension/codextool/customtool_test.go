package codextool

import (
	"encoding/json"
	"strings"
	"testing"

	"moonbridge/internal/format"
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

func TestBareNamespaceActionRoundTripsWhenUnambiguous(t *testing.T) {
	tools, err := BuildNamespaceTools(
		[]string{"spawn_agent", "wait_agent"},
		map[string]format.CoreTool{
			"spawn_agent": {
				Name:        "spawn_agent",
				InputSchema: map[string]any{"type": "object"},
			},
			"wait_agent": {
				Name:        "wait_agent",
				InputSchema: map[string]any{"type": "object"},
			},
		},
		"multi_agent_v1",
		NestedAnyOf,
	)
	if err != nil {
		t.Fatal(err)
	}
	toolMap := DecodeToolMap(BuildToolMapFromCore(tools).Encode())

	name, namespace, input := CoreToolCallFromProvider(
		"spawn_agent",
		json.RawMessage(`{"agent_type":"planner"}`),
		toolMap,
	)
	if name != "spawn_agent" || namespace != "multi_agent_v1" {
		t.Fatalf("core fields = name %q namespace %q", name, namespace)
	}
	if string(input) != `{"agent_type":"planner"}` {
		t.Fatalf("input = %s", input)
	}

	itemType, itemName, itemNamespace, itemInput, isLocalShell, _ := OutputItemFromBlock(
		"spawn_agent",
		json.RawMessage(`{"agent_type":"planner"}`),
		toolMap,
	)
	if itemType != "function_call" || itemName != "spawn_agent" || itemNamespace != "multi_agent_v1" || isLocalShell {
		t.Fatalf("output item fields = type %q name %q namespace %q local_shell %v", itemType, itemName, itemNamespace, isLocalShell)
	}
	if itemInput != `{"agent_type":"planner"}` {
		t.Fatalf("item input = %q", itemInput)
	}
}

func TestBareNamespaceActionDoesNotOverrideExactTopLevelTool(t *testing.T) {
	tools, err := BuildNamespaceTools(
		[]string{"spawn_agent"},
		map[string]format.CoreTool{
			"spawn_agent": {
				Name:        "spawn_agent",
				InputSchema: map[string]any{"type": "object"},
			},
		},
		"multi_agent_v1",
		NestedAnyOf,
	)
	if err != nil {
		t.Fatal(err)
	}
	topLevel := format.CoreTool{Name: "spawn_agent"}
	AnnotateCoreTool(&topLevel, ToolFunction, "spawn_agent", "")
	tools = append(tools, topLevel)
	toolMap := BuildToolMapFromCore(tools)

	_, namespace, _ := CoreToolCallFromProvider(
		"spawn_agent",
		json.RawMessage(`{"agent_type":"planner"}`),
		toolMap,
	)
	if namespace != "" {
		t.Fatalf("namespace = %q, want exact top-level tool to win", namespace)
	}
}
