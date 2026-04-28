package codex

import (
	"encoding/json"
	"strings"

	"moonbridge/internal/foundation/openai"
	"moonbridge/internal/protocol/anthropic"
)

func CustomToolSpecs(tools []openai.Tool, namespace string) map[string]CustomToolSpec {
	specs := map[string]CustomToolSpec{}
	for _, tool := range tools {
		switch tool.Type {
		case "custom":
			definition := customToolGrammarDefinition(tool)
			name := namespacedToolName(namespace, tool.Name)
			kind := CustomToolKindFromGrammar(definition)
			specs[name] = CustomToolSpec{
				GrammarDefinition: definition,
				Kind:              kind,
				OpenAIName:        name,
			}
			if kind == CustomToolKindApplyPatch {
				for _, action := range applyPatchToolActions() {
					specs[applyPatchToolName(name, action)] = CustomToolSpec{
						GrammarDefinition: definition,
						Kind:              kind,
						OpenAIName:        name,
						ApplyPatchAction:  action,
					}
				}
			}
		case "namespace":
			for name, spec := range CustomToolSpecs(tool.Tools, namespacedToolName(namespace, tool.Name)) {
				specs[name] = spec
			}
		}
	}
	return specs
}

func FunctionToolSpecs(tools []openai.Tool, namespace string) map[string]FunctionToolSpec {
	specs := map[string]FunctionToolSpec{}
	for _, tool := range tools {
		switch tool.Type {
		case "function":
			if namespace == "" {
				continue
			}
			name := namespacedToolName(namespace, tool.Name)
			specs[name] = FunctionToolSpec{
				Namespace: namespace,
				Name:      tool.Name,
			}
		case "namespace":
			for name, spec := range FunctionToolSpecs(tool.Tools, namespacedToolName(namespace, tool.Name)) {
				specs[name] = spec
			}
		}
	}
	return specs
}

func AnthropicToolFromOpenAIFunction(name string, description string, parameters map[string]any) anthropic.Tool {
	if parameters == nil {
		parameters = map[string]any{"type": "object"}
	}
	return anthropic.Tool{
		Name:        name,
		Description: description,
		InputSchema: parameters,
	}
}

func AnthropicCustomToolsFromOpenAI(name string, tool openai.Tool) []anthropic.Tool {
	definition := customToolGrammarDefinition(tool)
	kind := CustomToolKindFromGrammar(definition)
	if kind == CustomToolKindApplyPatch {
		return anthropicApplyPatchProxyTools(name)
	}
	if kind == CustomToolKindExec {
		return []anthropic.Tool{{
			Name:        name,
			Description: execProxyDescription(),
			InputSchema: execProxySchema(),
		}}
	}
	inputDescription := customToolInputDescription(tool)
	return []anthropic.Tool{{
		Name:        name,
		Description: customToolDescription(tool),
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{"input": map[string]any{
				"type":        "string",
				"description": inputDescription,
			}},
			"required": []string{"input"},
		},
	}}
}

func customToolDescription(tool openai.Tool) string {
	parts := []string{}
	if strings.TrimSpace(tool.Description) != "" {
		parts = append(parts, strings.TrimSpace(tool.Description))
	}
	if definition := customToolGrammarDefinition(tool); definition != "" {
		parts = append(parts, "OpenAI custom tool grammar:\n"+definition)
	}
	if len(parts) == 0 {
		return "Use this custom tool with its raw freeform input in the input field."
	}
	return strings.Join(parts, "\n\n")
}

func customToolInputDescription(tool openai.Tool) string {
	description := "Raw freeform input for this custom tool. Put only the tool input text here, not a JSON string or markdown wrapper."
	if definition := customToolGrammarDefinition(tool); definition != "" {
		if IsApplyPatchGrammar(definition) {
			description = "Raw apply_patch patch text. It must start with '*** Begin Patch' and end with a bare '*** End Patch' line. Use Codex apply_patch headers such as '*** Add File:', '*** Delete File:' or '*** Update File:'. In Add File hunks, file content lines start with '+', but patch metadata lines like '*** End Patch' must not be prefixed with '+'. Do not use unified diff headers like 'diff --git', '---', '+++' and do not wrap the patch in markdown fences."
		}
		description += "\n\nGrammar:\n" + definition
	}
	return description
}

func customToolGrammarDefinition(tool openai.Tool) string {
	if tool.Format == nil {
		return ""
	}
	definition, _ := tool.Format["definition"].(string)
	return strings.TrimSpace(definition)
}

func CustomToolKindFromGrammar(definition string) CustomToolKind {
	switch {
	case IsApplyPatchGrammar(definition):
		return CustomToolKindApplyPatch
	case IsExecGrammar(definition):
		return CustomToolKindExec
	default:
		return CustomToolKindRaw
	}
}

func IsApplyPatchGrammar(definition string) bool {
	return strings.Contains(definition, `begin_patch: "*** Begin Patch"`) &&
		strings.Contains(definition, `end_patch: "*** End Patch"`) &&
		strings.Contains(definition, `add_hunk: "*** Add File: "`)
}

func IsExecGrammar(definition string) bool {
	return strings.Contains(definition, "@exec") ||
		(strings.Contains(definition, "pragma_source") && strings.Contains(definition, "plain_source"))
}

func applyPatchProxyDescription() string {
	return strings.Join([]string{
		"Edit files by providing structured JSON patch operations. Moon Bridge reconstructs the raw Codex apply_patch grammar before returning the tool call to Codex.",
		"Use this tool for file edits instead of shell redirection when the change can be represented as operations.",
		"Operation types: add_file creates a new file with content; delete_file removes a file; replace_file replaces an entire file with content; update_file edits an existing file with structured hunks.",
		"For update_file hunks, each line uses op=context, add, or remove. For whole-file replacement, prefer replace_file or update_file with content.",
	}, "\n\n")
}

func execProxyDescription() string {
	return "Run the Codex Code Mode exec custom tool by providing structured JSON with a source string. Put the JavaScript source in source, including any // @exec pragmas if needed. Moon Bridge returns that source as the raw Codex custom tool input."
}

func anthropicApplyPatchProxyTools(name string) []anthropic.Tool {
	return []anthropic.Tool{
		{
			Name:        applyPatchToolName(name, "add_file"),
			Description: applyPatchActionDescription("add_file"),
			InputSchema: applyPatchSingleOperationSchema("add_file"),
		},
		{
			Name:        applyPatchToolName(name, "delete_file"),
			Description: applyPatchActionDescription("delete_file"),
			InputSchema: applyPatchSingleOperationSchema("delete_file"),
		},
		{
			Name:        applyPatchToolName(name, "update_file"),
			Description: applyPatchActionDescription("update_file"),
			InputSchema: applyPatchSingleOperationSchema("update_file"),
		},
		{
			Name:        applyPatchToolName(name, "replace_file"),
			Description: applyPatchActionDescription("replace_file"),
			InputSchema: applyPatchSingleOperationSchema("replace_file"),
		},
		{
			Name:        applyPatchToolName(name, "batch"),
			Description: applyPatchProxyDescription(),
			InputSchema: applyPatchProxySchema(),
		},
	}
}

func applyPatchToolActions() []string {
	return []string{"add_file", "delete_file", "update_file", "replace_file", "batch"}
}

func applyPatchToolName(base string, action string) string {
	if action == "" {
		return base
	}
	return base + "_" + action
}

func applyPatchActionDescription(action string) string {
	switch action {
	case "add_file":
		return "Create one new file by providing a target path and full file content. Moon Bridge reconstructs the raw Codex apply_patch grammar before returning the tool call to Codex."
	case "delete_file":
		return "Delete one file by providing a target path. Moon Bridge reconstructs the raw Codex apply_patch grammar before returning the tool call to Codex."
	case "update_file":
		return "Edit one existing file with structured hunks. Each hunk line uses op=context, add, or remove. For whole-file replacement, use apply_patch_replace_file."
	case "replace_file":
		return "Replace one existing file by providing a target path and full new file content. Moon Bridge reconstructs this as a Codex delete plus add patch."
	default:
		return applyPatchProxyDescription()
	}
}

func applyPatchSingleOperationSchema(action string) map[string]any {
	properties := map[string]any{
		"path": map[string]any{"type": "string", "description": "Target file path."},
	}
	required := []string{"path"}
	switch action {
	case "add_file", "replace_file":
		properties["content"] = map[string]any{"type": "string", "description": "Full file content without patch prefixes."}
		required = append(required, "content")
	case "delete_file":
	case "update_file":
		properties["move_to"] = map[string]any{
			"type":        "string",
			"description": "Optional destination path for move operations.",
		}
		properties["hunks"] = applyPatchHunksSchema()
		required = append(required, "hunks")
	}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           properties,
		"required":             required,
	}
}

func applyPatchProxySchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"operations": map[string]any{
				"type":        "array",
				"description": "Structured patch operations. Moon Bridge reconstructs Codex apply_patch grammar from these operations.",
				"minItems":    1,
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"type": map[string]any{
							"type":        "string",
							"enum":        []string{"add_file", "delete_file", "update_file", "replace_file"},
							"description": "Patch operation type.",
						},
						"path": map[string]any{"type": "string", "description": "Target file path."},
						"move_to": map[string]any{
							"type":        "string",
							"description": "Optional destination path for update_file move operations.",
						},
						"content": map[string]any{
							"type":        "string",
							"description": "For add_file or replace_file: full file content without leading '+'. For update_file, use content only when replacing the whole file; Moon Bridge will reconstruct this as Delete File plus Add File.",
						},
						"hunks": map[string]any{
							"type":        "array",
							"description": "For update_file: structured hunks.",
							"items":       applyPatchHunkSchema(),
						},
					},
					"required": []string{"type", "path"},
				},
			},
		},
		"required": []string{"operations"},
	}
}

func applyPatchHunksSchema() map[string]any {
	return map[string]any{
		"type":        "array",
		"description": "Structured update hunks.",
		"minItems":    1,
		"items":       applyPatchHunkSchema(),
	}
}

func applyPatchHunkSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"context": map[string]any{"type": "string", "description": "Optional @@ context header text."},
			"lines": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"op":   map[string]any{"type": "string", "enum": []string{"context", "add", "remove"}},
						"text": map[string]any{"type": "string"},
					},
					"required": []string{"op", "text"},
				},
			},
		},
		"required": []string{"lines"},
	}
}

func execProxySchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"source": map[string]any{
				"type":        "string",
				"description": "JavaScript source code, including any // @exec pragmas if needed.",
			},
		},
		"required": []string{"source"},
	}
}

func CustomToolItemID(toolUseID string) string {
	if toolUseID == "" {
		return "ctc_generated"
	}
	if strings.HasPrefix(toolUseID, "ctc_") {
		return toolUseID
	}
	return "ctc_" + toolUseID
}

func customToolInputObject(input string) json.RawMessage {
	data, err := json.Marshal(map[string]string{"input": input})
	if err != nil {
		return json.RawMessage(`{"input":""}`)
	}
	return data
}

func customToolInputFromRaw(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err == nil {
		if value, ok := object["input"]; ok {
			var input string
			if err := json.Unmarshal(value, &input); err == nil {
				return input
			}
			return string(value)
		}
	}
	var input string
	if err := json.Unmarshal(raw, &input); err == nil {
		return input
	}
	return string(raw)
}

type applyPatchProxyInput struct {
	Operations []applyPatchOperation `json:"operations"`
	RawPatch   string                `json:"raw_patch"`
	Input      string                `json:"input"`
	Patch      string                `json:"patch"`
}

type applyPatchOperation struct {
	Type    string             `json:"type"`
	Path    string             `json:"path"`
	MoveTo  string             `json:"move_to"`
	Content string             `json:"content"`
	Hunks   []applyPatchHunk   `json:"hunks"`
	Changes string             `json:"changes"`
	Lines   []applyPatchLineOp `json:"lines"`
}

type applyPatchHunk struct {
	Context string             `json:"context"`
	Lines   []applyPatchLineOp `json:"lines"`
}

type applyPatchLineOp struct {
	Op   string `json:"op"`
	Text string `json:"text"`
}

func applyPatchInputFromProxyRaw(raw json.RawMessage, action string) string {
	if action != "" && action != "batch" {
		if operation, ok := applyPatchOperationFromSingleProxyRaw(raw, action); ok {
			return buildApplyPatchInput([]applyPatchOperation{operation})
		}
	}
	var input applyPatchProxyInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return normalizeApplyPatchInput(customToolInputFromRaw(raw))
	}
	switch {
	case len(input.Operations) > 0:
		return buildApplyPatchInput(input.Operations)
	case input.RawPatch != "":
		return normalizeApplyPatchInput(input.RawPatch)
	case input.Patch != "":
		return normalizeApplyPatchInput(input.Patch)
	case input.Input != "":
		return normalizeApplyPatchInput(input.Input)
	default:
		return normalizeApplyPatchInput(customToolInputFromRaw(raw))
	}
}

func applyPatchOperationFromSingleProxyRaw(raw json.RawMessage, action string) (applyPatchOperation, bool) {
	var operation applyPatchOperation
	if err := json.Unmarshal(raw, &operation); err != nil {
		return applyPatchOperation{}, false
	}
	if operation.Type == "" {
		operation.Type = action
	}
	if operation.Path == "" {
		return applyPatchOperation{}, false
	}
	return operation, true
}

func applyPatchProxyInputFromGrammar(input string, action string) json.RawMessage {
	if operations, ok := parseApplyPatchOperations(input); ok {
		if action != "" && action != "batch" && len(operations) == 1 {
			data, err := json.Marshal(applyPatchSingleProxyInput(operations[0], action))
			if err == nil {
				return data
			}
		}
		data, err := json.Marshal(applyPatchProxyInput{Operations: operations})
		if err == nil {
			return data
		}
	}
	data, err := json.Marshal(applyPatchProxyInput{RawPatch: input})
	if err != nil {
		return json.RawMessage(`{"raw_patch":""}`)
	}
	return data
}

func applyPatchSingleProxyInput(operation applyPatchOperation, action string) map[string]any {
	input := map[string]any{"path": operation.Path}
	if operation.MoveTo != "" {
		input["move_to"] = operation.MoveTo
	}
	switch action {
	case "add_file", "replace_file":
		input["content"] = operation.Content
	case "update_file":
		if len(operation.Hunks) > 0 {
			input["hunks"] = operation.Hunks
		} else if len(operation.Lines) > 0 {
			input["hunks"] = []applyPatchHunk{{Lines: operation.Lines}}
		} else if strings.TrimSpace(operation.Changes) != "" {
			input["hunks"] = parseApplyPatchHunksFromChanges(operation.Changes)
		}
	}
	return input
}

func applyPatchToolNameAndActionForGrammar(name string, input string) (string, string) {
	operations, ok := parseApplyPatchOperations(input)
	if !ok {
		return applyPatchToolName(name, "batch"), "batch"
	}
	action := applyPatchActionForOperations(operations)
	return applyPatchToolName(name, action), action
}

func applyPatchActionForOperations(operations []applyPatchOperation) string {
	if len(operations) != 1 {
		return "batch"
	}
	operation := operations[0]
	switch operation.Type {
	case "add_file", "add":
		return "add_file"
	case "delete_file", "delete":
		return "delete_file"
	case "replace_file", "replace":
		return "replace_file"
	case "update_file", "update":
		if operation.Content != "" && len(operation.Hunks) == 0 && len(operation.Lines) == 0 && strings.TrimSpace(operation.Changes) == "" && operation.MoveTo == "" {
			return "replace_file"
		}
		return "update_file"
	default:
		return "batch"
	}
}

func parseApplyPatchHunksFromChanges(changes string) []applyPatchHunk {
	lines := strings.Split(strings.TrimRight(strings.ReplaceAll(changes, "\r\n", "\n"), "\n"), "\n")
	hunks := make([]applyPatchHunk, 0)
	current := applyPatchHunk{}
	hasCurrent := false
	for _, line := range lines {
		if strings.HasPrefix(line, "@@") {
			if hasCurrent {
				hunks = append(hunks, current)
			}
			current = applyPatchHunk{Context: strings.TrimSpace(strings.TrimPrefix(line, "@@"))}
			hasCurrent = true
			continue
		}
		if !hasCurrent {
			current = applyPatchHunk{}
			hasCurrent = true
		}
		lineOp := applyPatchLineOp{Op: "context", Text: line}
		if strings.HasPrefix(line, "+") {
			lineOp = applyPatchLineOp{Op: "add", Text: strings.TrimPrefix(line, "+")}
		} else if strings.HasPrefix(line, "-") {
			lineOp = applyPatchLineOp{Op: "remove", Text: strings.TrimPrefix(line, "-")}
		} else if strings.HasPrefix(line, " ") {
			lineOp.Text = strings.TrimPrefix(line, " ")
		}
		current.Lines = append(current.Lines, lineOp)
	}
	if hasCurrent {
		hunks = append(hunks, current)
	}
	return hunks
}

func buildApplyPatchInput(operations []applyPatchOperation) string {
	var builder strings.Builder
	builder.WriteString("*** Begin Patch\n")
	for _, operation := range operations {
		switch operation.Type {
		case "add_file", "add":
			writeApplyPatchAddFile(&builder, operation.Path, operation.Content)
		case "delete_file", "delete":
			builder.WriteString("*** Delete File: ")
			builder.WriteString(operation.Path)
			builder.WriteByte('\n')
		case "replace_file", "replace":
			writeApplyPatchReplaceFile(&builder, operation.Path, operation.Content)
		case "update_file", "update":
			if operation.Content != "" && len(operation.Hunks) == 0 && len(operation.Lines) == 0 && strings.TrimSpace(operation.Changes) == "" {
				targetPath := operation.Path
				if operation.MoveTo != "" {
					targetPath = operation.MoveTo
				}
				writeApplyPatchDeleteFile(&builder, operation.Path)
				writeApplyPatchAddFile(&builder, targetPath, operation.Content)
				continue
			}
			builder.WriteString("*** Update File: ")
			builder.WriteString(operation.Path)
			builder.WriteByte('\n')
			if operation.MoveTo != "" {
				builder.WriteString("*** Move to: ")
				builder.WriteString(operation.MoveTo)
				builder.WriteByte('\n')
			}
			writeApplyPatchHunks(&builder, operation)
		}
	}
	builder.WriteString("*** End Patch")
	return normalizeApplyPatchInput(builder.String())
}

func writeApplyPatchReplaceFile(builder *strings.Builder, path string, content string) {
	writeApplyPatchDeleteFile(builder, path)
	writeApplyPatchAddFile(builder, path, content)
}

func writeApplyPatchDeleteFile(builder *strings.Builder, path string) {
	builder.WriteString("*** Delete File: ")
	builder.WriteString(path)
	builder.WriteByte('\n')
}

func writeApplyPatchAddFile(builder *strings.Builder, path string, content string) {
	builder.WriteString("*** Add File: ")
	builder.WriteString(path)
	builder.WriteByte('\n')
	for _, line := range splitPatchContentLines(content) {
		builder.WriteByte('+')
		builder.WriteString(line)
		builder.WriteByte('\n')
	}
}

func writeApplyPatchHunks(builder *strings.Builder, operation applyPatchOperation) {
	if len(operation.Hunks) > 0 {
		for _, hunk := range operation.Hunks {
			if hunk.Context == "" {
				builder.WriteString("@@\n")
			} else {
				builder.WriteString("@@ ")
				builder.WriteString(hunk.Context)
				builder.WriteByte('\n')
			}
			for _, line := range hunk.Lines {
				writeApplyPatchLine(builder, line)
			}
		}
		return
	}
	if len(operation.Lines) > 0 {
		builder.WriteString("@@\n")
		for _, line := range operation.Lines {
			writeApplyPatchLine(builder, line)
		}
		return
	}
	if strings.TrimSpace(operation.Changes) == "" {
		return
	}
	changes := strings.TrimRight(operation.Changes, "\n")
	if !strings.HasPrefix(changes, "@@") {
		builder.WriteString("@@\n")
	}
	builder.WriteString(changes)
	builder.WriteByte('\n')
}

func writeApplyPatchLine(builder *strings.Builder, line applyPatchLineOp) {
	switch line.Op {
	case "add":
		builder.WriteByte('+')
	case "remove", "delete":
		builder.WriteByte('-')
	default:
		builder.WriteByte(' ')
	}
	builder.WriteString(line.Text)
	builder.WriteByte('\n')
}

func splitPatchContentLines(content string) []string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.TrimSuffix(content, "\n")
	if content == "" {
		return []string{""}
	}
	return strings.Split(content, "\n")
}

func parseApplyPatchOperations(input string) ([]applyPatchOperation, bool) {
	lines := strings.Split(strings.ReplaceAll(input, "\r\n", "\n"), "\n")
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) < 2 || strings.TrimSpace(lines[0]) != "*** Begin Patch" || strings.TrimSpace(lines[len(lines)-1]) != "*** End Patch" {
		return nil, false
	}
	operations := make([]applyPatchOperation, 0)
	for index := 1; index < len(lines)-1; {
		line := lines[index]
		switch {
		case strings.HasPrefix(line, "*** Add File: "):
			operation := applyPatchOperation{Type: "add_file", Path: strings.TrimPrefix(line, "*** Add File: ")}
			index++
			contentLines := make([]string, 0)
			for index < len(lines)-1 && !strings.HasPrefix(lines[index], "*** ") {
				contentLines = append(contentLines, strings.TrimPrefix(lines[index], "+"))
				index++
			}
			operation.Content = strings.Join(contentLines, "\n")
			operations = append(operations, operation)
		case strings.HasPrefix(line, "*** Delete File: "):
			operations = append(operations, applyPatchOperation{Type: "delete_file", Path: strings.TrimPrefix(line, "*** Delete File: ")})
			index++
		case strings.HasPrefix(line, "*** Update File: "):
			operation := applyPatchOperation{Type: "update_file", Path: strings.TrimPrefix(line, "*** Update File: ")}
			index++
			changes := make([]string, 0)
			if index < len(lines)-1 && strings.HasPrefix(lines[index], "*** Move to: ") {
				operation.MoveTo = strings.TrimPrefix(lines[index], "*** Move to: ")
				index++
			}
			for index < len(lines)-1 && !strings.HasPrefix(lines[index], "*** ") {
				changes = append(changes, lines[index])
				index++
			}
			operation.Changes = strings.Join(changes, "\n")
			operations = append(operations, operation)
		default:
			return nil, false
		}
	}
	return operations, len(operations) > 0
}

func execInputFromProxyRaw(raw json.RawMessage) string {
	var input struct {
		Source string `json:"source"`
		Input  string `json:"input"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return customToolInputFromRaw(raw)
	}
	if input.Source != "" {
		return input.Source
	}
	if input.Input != "" {
		return input.Input
	}
	return customToolInputFromRaw(raw)
}

func execProxyInputFromGrammar(input string) json.RawMessage {
	data, err := json.Marshal(map[string]string{"source": input})
	if err != nil {
		return json.RawMessage(`{"source":""}`)
	}
	return data
}

func normalizeApplyPatchInput(input string) string {
	lines := strings.Split(input, "\n")
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return input
	}
	lastIndex := len(lines) - 1
	if strings.TrimSpace(lines[lastIndex]) == "+*** End Patch" {
		lines[lastIndex] = "*** End Patch"
		if lastIndex > 0 && strings.TrimSpace(lines[lastIndex-1]) == "+*** End of File" {
			lines = append(lines[:lastIndex-1], lines[lastIndex])
		}
		return strings.Join(lines, "\n")
	}
	return input
}
