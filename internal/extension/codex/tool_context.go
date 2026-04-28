package codex

import (
	"encoding/json"
	"strings"
)

type ConversionContext struct {
	CustomTools   map[string]CustomToolSpec
	FunctionTools map[string]FunctionToolSpec
}

type CustomToolSpec struct {
	GrammarDefinition string
	Kind              CustomToolKind
	OpenAIName        string
	ApplyPatchAction  string
}

type FunctionToolSpec struct {
	Namespace string
	Name      string
}

type CustomToolKind string

const (
	CustomToolKindRaw        CustomToolKind = "raw"
	CustomToolKindApplyPatch CustomToolKind = "apply_patch"
	CustomToolKindExec       CustomToolKind = "exec"
)

func (context ConversionContext) IsCustomTool(name string) bool {
	if len(context.CustomTools) == 0 {
		return false
	}
	_, ok := context.CustomTools[name]
	return ok
}

func (context ConversionContext) OpenAINameForCustomTool(name string) string {
	spec, ok := context.CustomTools[name]
	if !ok || spec.OpenAIName == "" {
		return name
	}
	return spec.OpenAIName
}

func (context ConversionContext) AnthropicToolChoiceName(name string) string {
	spec, ok := context.CustomTools[name]
	if !ok || spec.Kind != CustomToolKindApplyPatch {
		return name
	}
	return applyPatchToolName(name, "batch")
}

func (context ConversionContext) CustomToolInputFromRaw(name string, raw json.RawMessage) string {
	spec, ok := context.CustomTools[name]
	if !ok {
		return customToolInputFromRaw(raw)
	}
	switch spec.Kind {
	case CustomToolKindApplyPatch:
		return applyPatchInputFromProxyRaw(raw, spec.ApplyPatchAction)
	case CustomToolKindExec:
		return execInputFromProxyRaw(raw)
	default:
		return customToolInputFromRaw(raw)
	}
}

func (context ConversionContext) AnthropicToolUseForCustomTool(name string, input string) (string, json.RawMessage) {
	spec, ok := context.CustomTools[name]
	if !ok {
		return name, customToolInputObject(input)
	}
	switch spec.Kind {
	case CustomToolKindApplyPatch:
		toolName, action := applyPatchToolNameAndActionForGrammar(name, input)
		return toolName, applyPatchProxyInputFromGrammar(input, action)
	case CustomToolKindExec:
		return name, execProxyInputFromGrammar(input)
	default:
		return name, customToolInputObject(input)
	}
}

func (context ConversionContext) NormalizeCustomToolInput(name string, input string) string {
	spec, ok := context.CustomTools[name]
	if !ok {
		return input
	}
	if spec.Kind == CustomToolKindApplyPatch {
		return normalizeApplyPatchInput(input)
	}
	return input
}

func (context ConversionContext) OpenAIFunctionToolName(name string) (string, string) {
	spec, ok := context.FunctionTools[name]
	if !ok {
		return name, ""
	}
	return spec.Name, spec.Namespace
}

func (context ConversionContext) AnthropicFunctionToolName(namespace string, name string) string {
	if namespace == "" {
		return name
	}
	if strings.HasPrefix(name, namespace) {
		return name
	}
	return namespacedToolName(namespace, name)
}
