package bridge_test

import (
	"encoding/json"
	"testing"

	deepseekv4 "moonbridge/internal/extension/deepseek_v4"
	"moonbridge/internal/extension/plugin"
	"moonbridge/internal/extension/pluginhooks"
	"moonbridge/internal/foundation/config"
	"moonbridge/internal/foundation/openai"
	"moonbridge/internal/protocol/anthropic"
	"moonbridge/internal/protocol/bridge"
	"moonbridge/internal/protocol/cache"
)

func TestToAnthropicAcceptsCodexLocalShellTool(t *testing.T) {
	request := openai.ResponsesRequest{
		Model: "gpt-test",
		Input: json.RawMessage(`[
			{"role":"user","content":[{"type":"input_text","text":"list files"}]}
		]`),
		Tools: []openai.Tool{{Type: "local_shell"}},
	}
	parallel := false
	request.ParallelToolCalls = &parallel
	request.Reasoning = map[string]any{"effort": "medium"}
	request.Include = []string{"reasoning.encrypted_content"}
	request.ClientMetadata = map[string]any{"originator": "codex_cli"}

	converted, _, err := testBridge().ToAnthropic(request, nil)
	if err != nil {
		t.Fatalf("ToAnthropic(, nil) error = %v", err)
	}
	if len(converted.Tools) != 1 {
		t.Fatalf("tools = %+v", converted.Tools)
	}
	tool := converted.Tools[0]
	if tool.Name != "local_shell" {
		t.Fatalf("tool name = %q", tool.Name)
	}
	if tool.InputSchema["type"] != "object" {
		t.Fatalf("tool schema = %+v", tool.InputSchema)
	}
	if converted.Messages[0].Content[0].Text != "list files" {
		t.Fatalf("messages = %+v", converted.Messages)
	}
}

func TestToAnthropicIgnoresCodexNativeBuiltInTools(t *testing.T) {
	request := openai.ResponsesRequest{
		Model: "gpt-test",
		Input: json.RawMessage(`"hello"`),
		Tools: []openai.Tool{
			{Type: "local_shell"},
			{Type: "file_search"},
		},
	}

	converted, _, err := testBridge().ToAnthropic(request, nil)
	if err != nil {
		t.Fatalf("ToAnthropic(, nil) error = %v", err)
	}
	if len(converted.Tools) != 1 {
		t.Fatalf("tools = %+v", converted.Tools)
	}
	if converted.Tools[0].Name != "local_shell" {
		t.Fatalf("tool = %+v", converted.Tools[0])
	}
}

func TestToAnthropicConvertsCodexWebSearchTool(t *testing.T) {
	request := openai.ResponsesRequest{
		Model: "gpt-test",
		Input: json.RawMessage(`"search the web"`),
		Tools: []openai.Tool{
			{Type: "web_search", SearchContentTypes: []string{"text", "image"}},
		},
	}

	converted, _, err := testBridge().ToAnthropic(request, nil)
	if err != nil {
		t.Fatalf("ToAnthropic(, nil) error = %v", err)
	}
	if len(converted.Tools) != 1 {
		t.Fatalf("tools = %+v", converted.Tools)
	}
	tool := converted.Tools[0]
	if tool.Name != "web_search" || tool.Type != "web_search_20250305" || tool.MaxUses != 8 {
		t.Fatalf("web search tool = %+v", tool)
	}
	if tool.InputSchema != nil {
		t.Fatalf("InputSchema = %+v", tool.InputSchema)
	}
}

func TestToAnthropicSkipsCodexWebSearchToolWhenProviderDisabled(t *testing.T) {
	request := openai.ResponsesRequest{
		Model: "gpt-test",
		Input: json.RawMessage(`"search the web"`),
		Tools: []openai.Tool{
			{Type: "web_search", SearchContentTypes: []string{"text", "image"}},
			{Type: "local_shell"},
		},
	}

	bridgeUnderTest := testBridgeWithWebSearchDisabled()
	converted, _, err := bridgeUnderTest.ToAnthropic(request, nil)
	if err != nil {
		t.Fatalf("ToAnthropic() error = %v", err)
	}
	if len(converted.Tools) != 1 || converted.Tools[0].Name != "local_shell" {
		t.Fatalf("tools = %+v", converted.Tools)
	}
}

func TestToAnthropicKeepsCodexWebSearchToolForModelDecision(t *testing.T) {
	request := openai.ResponsesRequest{
		Model: "gpt-test",
		Input: json.RawMessage(`[
			{"role":"user","content":[{"type":"input_text","text":"根据知识库写一份 README 使用指南"}],"type":"message"}
		]`),
		Tools: []openai.Tool{
			{Type: "web_search", SearchContentTypes: []string{"text", "image"}},
			{Type: "function", Name: "list_mcp_resources", Parameters: map[string]any{"type": "object"}},
		},
	}

	converted, _, err := testBridge().ToAnthropic(request, nil)
	if err != nil {
		t.Fatalf("ToAnthropic() error = %v", err)
	}
	if len(converted.Tools) != 2 {
		t.Fatalf("tools = %+v", converted.Tools)
	}
	if converted.Tools[0].Name != "web_search" || converted.Tools[0].Type != "web_search_20250305" {
		t.Fatalf("web search tool = %+v", converted.Tools[0])
	}
	if converted.Tools[1].Name != "list_mcp_resources" {
		t.Fatalf("tool = %+v", converted.Tools[1])
	}
}

type bridgeToolInjector struct {
	plugin.BasePlugin
	seenWebSearch plugin.WebSearchInfo
}

func (p *bridgeToolInjector) Name() string                      { return "bridge_tool_injector" }
func (p *bridgeToolInjector) EnabledForModel(model string) bool { return model == "gpt-test" }

func (p *bridgeToolInjector) InjectTools(ctx *plugin.RequestContext) []anthropic.Tool {
	p.seenWebSearch = ctx.WebSearch
	return []anthropic.Tool{{
		Name:        "plugin_tool",
		Description: "Injected by plugin",
		InputSchema: map[string]any{"type": "object"},
	}}
}

func TestToAnthropicAppendsPluginInjectedTools(t *testing.T) {
	cfg := config.Config{
		DefaultMaxTokens: 1024,
		Routes:           map[string]config.RouteEntry{"gpt-test": {Provider: "default", Model: "claude-test"}},
		Cache:            config.CacheConfig{Mode: "off"},
	}
	plugins := plugin.NewRegistry(nil)
	injector := &bridgeToolInjector{}
	plugins.Register(injector)
	bridgeUnderTest := bridge.New(cfg, cache.NewMemoryRegistry(), pluginhooks.PluginHooksFromRegistry(plugins))

	converted, _, err := bridgeUnderTest.ToAnthropic(openai.ResponsesRequest{
		Model: "gpt-test",
		Input: json.RawMessage(`"hello"`),
		Tools: []openai.Tool{{Type: "local_shell"}},
	}, nil, bridge.RequestOptions{WebSearchMode: "injected", WebSearchMaxUses: 3, FirecrawlAPIKey: "fc-test"})
	if err != nil {
		t.Fatalf("ToAnthropic() error = %v", err)
	}
	if len(converted.Tools) != 2 {
		t.Fatalf("tools = %+v", converted.Tools)
	}
	if converted.Tools[0].Name != "local_shell" || converted.Tools[1].Name != "plugin_tool" {
		t.Fatalf("tools = %+v", converted.Tools)
	}
	if injector.seenWebSearch.Mode != "injected" || injector.seenWebSearch.MaxUses != 3 || injector.seenWebSearch.FirecrawlKey != "fc-test" {
		t.Fatalf("plugin web search context = %+v", injector.seenWebSearch)
	}
}

func TestToAnthropicFlattensCodexNamespaceTools(t *testing.T) {
	request := openai.ResponsesRequest{
		Model: "gpt-test",
		Input: json.RawMessage(`"hello"`),
		Tools: []openai.Tool{
			{
				Type:        "namespace",
				Name:        "mcp__deepwiki__",
				Description: "DeepWiki tools",
				Tools: []openai.Tool{
					{
						Type:        "function",
						Name:        "ask_question",
						Description: "Ask a repository question",
						Parameters: map[string]any{
							"type":     "object",
							"required": []string{"repoName", "question"},
							"properties": map[string]any{
								"repoName": map[string]any{"type": "string"},
								"question": map[string]any{"type": "string"},
							},
						},
					},
					{
						Type: "function",
						Name: "read_wiki_structure",
					},
				},
			},
		},
	}

	converted, _, err := testBridge().ToAnthropic(request, nil)
	if err != nil {
		t.Fatalf("ToAnthropic() error = %v", err)
	}
	if len(converted.Tools) != 2 {
		t.Fatalf("tools = %+v", converted.Tools)
	}
	if converted.Tools[0].Name != "mcp__deepwiki__ask_question" {
		t.Fatalf("first tool name = %q", converted.Tools[0].Name)
	}
	if converted.Tools[0].Description != "Ask a repository question" {
		t.Fatalf("first tool description = %q", converted.Tools[0].Description)
	}
	if converted.Tools[0].InputSchema["type"] != "object" {
		t.Fatalf("first tool schema = %+v", converted.Tools[0].InputSchema)
	}
	if converted.Tools[1].Name != "mcp__deepwiki__read_wiki_structure" {
		t.Fatalf("second tool name = %q", converted.Tools[1].Name)
	}
	if converted.Tools[1].InputSchema["type"] != "object" {
		t.Fatalf("second tool schema = %+v", converted.Tools[1].InputSchema)
	}
}

func TestToAnthropicConvertsCodexNamespaceFunctionHistoryAndToolChoice(t *testing.T) {
	request := openai.ResponsesRequest{
		Model: "gpt-test",
		Input: json.RawMessage(`[
			{"type":"function_call","call_id":"tool_1","namespace":"mcp__deepwiki__","name":"read_wiki_structure","arguments":"{\"repoName\":\"openai/codex\"}"}
		]`),
		ToolChoice: json.RawMessage(`{"type":"function","namespace":"mcp__deepwiki__","name":"read_wiki_structure"}`),
		Tools: []openai.Tool{{
			Type: "namespace",
			Name: "mcp__deepwiki__",
			Tools: []openai.Tool{{
				Type: "function",
				Name: "read_wiki_structure",
			}},
		}},
	}

	converted, _, err := testBridge().ToAnthropic(request, nil)
	if err != nil {
		t.Fatalf("ToAnthropic(, nil) error = %v", err)
	}
	if converted.Messages[0].Content[0].Name != "mcp__deepwiki__read_wiki_structure" {
		t.Fatalf("history tool name = %+v", converted.Messages[0].Content[0])
	}
	if converted.ToolChoice.Name != "mcp__deepwiki__read_wiki_structure" {
		t.Fatalf("tool choice = %+v", converted.ToolChoice)
	}
}

func TestToAnthropicAppliesDeepSeekV4SamplingQuirksOnlyForRoutedProvider(t *testing.T) {
	cfg := config.Config{
		DefaultMaxTokens: 1024,
		Routes: map[string]config.RouteEntry{
			"deep": {
				Provider: "deepseek",
				Model:    "deepseek-v4-pro",
				Extensions: map[string]config.ExtensionSettings{
					deepseekv4.PluginName: extensionEnabled(true),
				},
			},
			"claude": {Provider: "anthropic", Model: "claude-test"},
		},
		ProviderDefs: map[string]config.ProviderDef{
			"deepseek":  {},
			"anthropic": {},
		},
		Cache: config.CacheConfig{Mode: "off"},
	}
	plugins := plugin.NewRegistry(nil)
	plugins.Register(deepseekv4.NewPlugin())
	if err := plugins.InitAll(&cfg); err != nil {
		t.Fatalf("InitAll() error = %v", err)
	}
	bridgeUnderTest := bridge.New(cfg, cache.NewMemoryRegistry(), pluginhooks.PluginHooksFromRegistry(plugins))
	temperature := 0.2
	topP := 0.9
	base := openai.ResponsesRequest{
		Input:       json.RawMessage(`"hello"`),
		Temperature: &temperature,
		TopP:        &topP,
		Reasoning:   map[string]any{"effort": "xhigh"},
	}

	deepRequest := base
	deepRequest.Model = "deep"
	deepConverted, _, err := bridgeUnderTest.ToAnthropic(deepRequest, nil)
	if err != nil {
		t.Fatalf("ToAnthropic(deep) error = %v", err)
	}
	if deepConverted.Model != "deepseek-v4-pro" || deepConverted.Temperature != nil || deepConverted.TopP != nil || deepConverted.Thinking != nil ||
		deepConverted.OutputConfig == nil || deepConverted.OutputConfig.Effort != "max" {
		t.Fatalf("deep request = %+v", deepConverted)
	}

	claudeRequest := base
	claudeRequest.Model = "claude"
	claudeConverted, _, err := bridgeUnderTest.ToAnthropic(claudeRequest, nil)
	if err != nil {
		t.Fatalf("ToAnthropic(claude) error = %v", err)
	}
	if claudeConverted.Temperature == nil || claudeConverted.TopP == nil ||
		claudeConverted.Thinking != nil || claudeConverted.OutputConfig != nil {
		t.Fatalf("claude request = %+v", claudeConverted)
	}
}

func TestToAnthropicRecoversConcatenatedFunctionCallArgumentsFromHistory(t *testing.T) {
	request := openai.ResponsesRequest{
		Model: "gpt-test",
		Input: json.RawMessage(`[
			{"type":"function_call","call_id":"toolu_1","name":"lookup","arguments":"{\"query\":\"A\"}"},
			{"type":"function_call","call_id":"toolu_2","name":"lookup","arguments":"{\"query\":\"A\"}{\"query\":\"B\"}"},
			{"type":"function_call_output","call_id":"toolu_1","output":"A result"},
			{"type":"function_call_output","call_id":"toolu_2","output":"B result"}
		]`),
	}

	converted, _, err := testBridge().ToAnthropic(request, nil)
	if err != nil {
		t.Fatalf("ToAnthropic(, nil) error = %v", err)
	}
	if _, err := json.Marshal(converted); err != nil {
		t.Fatalf("Marshal converted request error = %v", err)
	}
	assistant := converted.Messages[0]
	if assistant.Role != "assistant" || len(assistant.Content) != 2 {
		t.Fatalf("assistant history = %+v", assistant)
	}
	if string(assistant.Content[0].Input) != `{"query":"A"}` {
		t.Fatalf("first tool input = %s", assistant.Content[0].Input)
	}
	if string(assistant.Content[1].Input) != `{"query":"B"}` {
		t.Fatalf("second tool input = %s", assistant.Content[1].Input)
	}
}

func TestToAnthropicSanitizesMalformedFunctionCallArgumentsFromHistory(t *testing.T) {
	request := openai.ResponsesRequest{
		Model: "gpt-test",
		Input: json.RawMessage(`[
			{"type":"function_call","call_id":"tool_colon","name":"shell","arguments":"{\"command\":[\"powershell.exe\",\"-NoProfile\",\"-Command\":\"bad\"]}"},
			{"type":"function_call","call_id":"tool_brace","name":"shell","arguments":"{\"command\":[\"powershell.exe\",\"-NoProfile\",\"-Command\",\"bad\"}]}"},
			{"type":"function_call","call_id":"tool_at","name":"shell","arguments":"{\"command\":[\"powershell.exe\",\"-NoProfile\",\"-Command\",@\"bad\"]}"}
		]`),
	}

	converted, _, err := testBridge().ToAnthropic(request, nil)
	if err != nil {
		t.Fatalf("ToAnthropic(, nil) error = %v", err)
	}
	if _, err := json.Marshal(converted); err != nil {
		t.Fatalf("Marshal converted request error = %v", err)
	}
	assistant := converted.Messages[0]
	if assistant.Role != "assistant" || len(assistant.Content) != 3 {
		t.Fatalf("assistant history = %+v", assistant)
	}
	for _, block := range assistant.Content {
		if string(block.Input) != `{"invalid_argument":true}` {
			t.Fatalf("tool input = %s, want invalid sentinel", block.Input)
		}
	}
}

func TestToAnthropicSkipsCommentaryPhaseMessages(t *testing.T) {
	request := openai.ResponsesRequest{
		Model: "gpt-test",
		Input: json.RawMessage(`[
			{"role":"user","content":[{"type":"input_text","text":"inspect project"}],"type":"message"},
			{"role":"assistant","phase":"commentary","content":[{"type":"output_text","text":"Collecting from upstream..."}],"type":"message"},
			{"arguments":"{\"cmd\":\"pwd\"}","call_id":"tool_pwd","name":"exec_command","type":"function_call"},
			{"call_id":"tool_pwd","output":"/repo\n","type":"function_call_output"},
			{"role":"assistant","phase":"commentary","content":[{"type":"output_text","text":"Collecting from upstream..."}],"type":"message"},
			{"role":"user","content":[{"type":"input_text","text":"continue"}],"type":"message"}
		]`),
	}

	converted, _, err := testBridge().ToAnthropic(request, nil)
	if err != nil {
		t.Fatalf("ToAnthropic(, nil) error = %v", err)
	}
	if len(converted.Messages) != 4 {
		t.Fatalf("messages = %+v", converted.Messages)
	}
	for _, message := range converted.Messages {
		for _, block := range message.Content {
			if block.Text == "Collecting from upstream..." {
				t.Fatalf("commentary preamble leaked into Anthropic messages: %+v", converted.Messages)
			}
		}
	}
}

func TestToAnthropicConvertsCodexLocalShellHistoryAndOutput(t *testing.T) {
	request := openai.ResponsesRequest{
		Model: "gpt-test",
		Input: json.RawMessage(`[
			{"type":"local_shell_call","id":"lc_1","call_id":"toolu_shell","action":{"type":"exec","command":["bash","-lc","pwd"]}},
			{"type":"local_shell_call_output","call_id":"toolu_shell","output":"/repo\n"}
		]`),
	}

	converted, _, err := testBridge().ToAnthropic(request, nil)
	if err != nil {
		t.Fatalf("ToAnthropic() error = %v", err)
	}
	if converted.Messages[0].Role != "assistant" || converted.Messages[0].Content[0].Type != "tool_use" {
		t.Fatalf("assistant history = %+v", converted.Messages[0])
	}
	if converted.Messages[1].Role != "user" || converted.Messages[1].Content[0].Type != "tool_result" {
		t.Fatalf("tool output = %+v", converted.Messages[1])
	}
}

func TestToAnthropicGroupsParallelFunctionCallsBeforeOutputs(t *testing.T) {
	request := openai.ResponsesRequest{
		Model: "gpt-test",
		Input: json.RawMessage(`[
			{"role":"user","content":[{"type":"input_text","text":"inspect project"}],"type":"message"},
			{"arguments":"{\"cmd\":\"find . -maxdepth 2 -type f\"}","call_id":"tool_find","name":"exec_command","type":"function_call"},
			{"arguments":"{\"cmd\":\"ls -la\"}","call_id":"tool_ls","name":"exec_command","type":"function_call"},
			{"call_id":"tool_find","output":"go.mod\nREADME.md\n","type":"function_call_output"},
			{"call_id":"tool_ls","output":"total 8\n","type":"function_call_output"}
		]`),
	}

	converted, _, err := testBridge().ToAnthropic(request, nil)
	if err != nil {
		t.Fatalf("ToAnthropic(, nil) error = %v", err)
	}
	if len(converted.Messages) != 3 {
		t.Fatalf("messages = %+v", converted.Messages)
	}
	assistant := converted.Messages[1]
	if assistant.Role != "assistant" || len(assistant.Content) != 2 {
		t.Fatalf("assistant tool calls = %+v", assistant)
	}
	if assistant.Content[0].Type != "tool_use" || assistant.Content[0].ID != "tool_find" {
		t.Fatalf("first tool call = %+v", assistant.Content[0])
	}
	if assistant.Content[1].Type != "tool_use" || assistant.Content[1].ID != "tool_ls" {
		t.Fatalf("second tool call = %+v", assistant.Content[1])
	}
	results := converted.Messages[2]
	if results.Role != "user" || len(results.Content) != 2 {
		t.Fatalf("tool results = %+v", results)
	}
	if results.Content[0].Type != "tool_result" || results.Content[0].ToolUseID != "tool_find" {
		t.Fatalf("first tool result = %+v", results.Content[0])
	}
	if results.Content[1].Type != "tool_result" || results.Content[1].ToolUseID != "tool_ls" {
		t.Fatalf("second tool result = %+v", results.Content[1])
	}
}

func TestToAnthropicSkipsEmptyAssistantMessageBeforeToolCall(t *testing.T) {
	request := openai.ResponsesRequest{
		Model: "gpt-test",
		Input: json.RawMessage(`[
			{"role":"user","content":[{"type":"input_text","text":"inspect trace"}],"type":"message"},
			{"role":"assistant","content":[{"type":"output_text","text":""}],"type":"message"},
			{"arguments":"{\"cmd\":\"ls trace\"}","call_id":"call_ls","name":"exec_command","type":"function_call"},
			{"call_id":"call_ls","output":"trace/Transform\n","type":"function_call_output"}
		]`),
	}

	converted, _, err := testBridge().ToAnthropic(request, nil)
	if err != nil {
		t.Fatalf("ToAnthropic(, nil) error = %v", err)
	}
	if len(converted.Messages) != 3 {
		t.Fatalf("messages = %+v", converted.Messages)
	}
	assistant := converted.Messages[1]
	if assistant.Role != "assistant" || len(assistant.Content) != 1 {
		t.Fatalf("assistant = %+v", assistant)
	}
	if assistant.Content[0].Type != "tool_use" || assistant.Content[0].Name != "exec_command" {
		t.Fatalf("assistant content = %+v", assistant.Content)
	}
}

func TestToAnthropicMergesAssistantTextWithFollowingToolCall(t *testing.T) {
	request := openai.ResponsesRequest{
		Model: "gpt-test",
		Input: json.RawMessage(`[
			{"role":"user","content":[{"type":"input_text","text":"inspect project"}],"type":"message"},
			{"role":"assistant","content":[{"type":"output_text","text":"I will inspect the tree."}],"type":"message"},
			{"arguments":"{\"cmd\":\"find . -maxdepth 2 -type f\"}","call_id":"tool_find","name":"exec_command","type":"function_call"},
			{"call_id":"tool_find","output":"go.mod\nREADME.md\n","type":"function_call_output"}
		]`),
	}

	converted, _, err := testBridge().ToAnthropic(request, nil)
	if err != nil {
		t.Fatalf("ToAnthropic(, nil) error = %v", err)
	}
	if len(converted.Messages) != 3 {
		t.Fatalf("messages = %+v", converted.Messages)
	}
	assistant := converted.Messages[1]
	if assistant.Role != "assistant" || len(assistant.Content) != 2 {
		t.Fatalf("assistant = %+v", assistant)
	}
	if assistant.Content[0].Type != "text" || assistant.Content[1].Type != "tool_use" {
		t.Fatalf("assistant content = %+v", assistant.Content)
	}
	if converted.Messages[2].Content[0].ToolUseID != "tool_find" {
		t.Fatalf("tool result = %+v", converted.Messages[2])
	}
}

func TestToAnthropicSkipsCodexWebSearchHistoryItems(t *testing.T) {
	request := openai.ResponsesRequest{
		Model: "gpt-test",
		Input: json.RawMessage(`[
			{"role":"user","content":[{"type":"input_text","text":"search news"}],"type":"message"},
			{"id":"ws_123","type":"web_search_call","status":"completed","action":{"type":"search","query":"Kimi K2.6"}},
			{"role":"assistant","content":[{"type":"output_text","text":"I found news."}],"type":"message"},
			{"role":"user","content":[{"type":"input_text","text":"summarize it"}],"type":"message"}
		]`),
	}

	converted, _, err := testBridge().ToAnthropic(request, nil)
	if err != nil {
		t.Fatalf("ToAnthropic(, nil) error = %v", err)
	}
	if len(converted.Messages) != 3 {
		t.Fatalf("messages = %+v", converted.Messages)
	}
	for _, message := range converted.Messages {
		if len(message.Content) == 1 && message.Content[0].Text == "" {
			t.Fatalf("unexpected empty message from web_search_call history: %+v", converted.Messages)
		}
	}
}

func TestToAnthropicSkipsEmptyWebSearchPreludeHistory(t *testing.T) {
	request := openai.ResponsesRequest{
		Model: "gpt-test",
		Input: json.RawMessage(`[
			{"role":"user","content":[{"type":"input_text","text":"根据知识库写 README"}],"type":"message"},
			{"role":"assistant","content":[{"type":"output_text","text":"Search results for query: "}],"type":"message"},
			{"type":"web_search_call","status":"completed","action":{"type":"search"}},
			{"type":"function_call","call_id":"tool_1","name":"list_mcp_resources","arguments":"{\"server\":\"deepwiki\"}"},
			{"type":"function_call_output","call_id":"tool_1","output":"{\"resources\":[]}"}
		]`),
	}

	converted, _, err := testBridge().ToAnthropic(request, nil)
	if err != nil {
		t.Fatalf("ToAnthropic(, nil) error = %v", err)
	}
	for _, message := range converted.Messages {
		for _, block := range message.Content {
			if block.Text == "Search results for query: " {
				t.Fatalf("dirty search prelude was preserved: %+v", converted.Messages)
			}
		}
	}
}

func TestToAnthropicDefaultsToolChoiceToAuto(t *testing.T) {
	request := openai.ResponsesRequest{
		Model: "gpt-test",
		Input: json.RawMessage(`"hi"`),
	}

	converted, _, err := testBridge().ToAnthropic(request, nil)
	if err != nil {
		t.Fatalf("ToAnthropic() error = %v", err)
	}
	if converted.ToolChoice.Type != "auto" {
		t.Fatalf("tool choice = %+v", converted.ToolChoice)
	}
}

// bridgePluginRecorder records which plugin hooks were called and their order.
type bridgePluginRecorder struct {
	plugin.BasePlugin
	called                     []string
	sawApplyPatchBatchInMutate bool
}

func (p *bridgePluginRecorder) Name() string                      { return "bridge_recorder" }
func (p *bridgePluginRecorder) EnabledForModel(model string) bool { return model == "gpt-test" }

func (p *bridgePluginRecorder) RewriteMessages(ctx *plugin.RequestContext, msgs []anthropic.Message) []anthropic.Message {
	p.called = append(p.called, "RewriteMessages")
	return msgs
}

func (p *bridgePluginRecorder) InjectTools(ctx *plugin.RequestContext) []anthropic.Tool {
	p.called = append(p.called, "InjectTools")
	return []anthropic.Tool{{Name: "plugin_tool", Description: "Injected by plugin", InputSchema: map[string]any{"type": "object"}}}
}

func (p *bridgePluginRecorder) MutateRequest(ctx *plugin.RequestContext, req *anthropic.MessageRequest) {
	p.called = append(p.called, "MutateRequest")
	for _, tool := range req.Tools {
		if tool.Name == "apply_patch_batch" {
			p.sawApplyPatchBatchInMutate = true
		}
	}
	if req.Metadata == nil {
		req.Metadata = map[string]any{}
	}
	req.Metadata["mutated_by_plugin"] = "yes"
}

func TestToAnthropicPluginHooksCalledWithCodexCustomTool(t *testing.T) {
	cfg := config.Config{
		DefaultMaxTokens: 1024,
		Routes:           map[string]config.RouteEntry{"gpt-test": {Provider: "default", Model: "claude-test"}},
		Cache:            config.CacheConfig{Mode: "off"},
	}
	plugins := plugin.NewRegistry(nil)
	recorder := &bridgePluginRecorder{}
	plugins.Register(recorder)
	bridgeUnderTest := bridge.New(cfg, cache.NewMemoryRegistry(), pluginhooks.PluginHooksFromRegistry(plugins))

	// Send a request with apply_patch custom grammar, verifying MutateRequest
	// sees the proxy Anthropic tools.
	converted, _, err := bridgeUnderTest.ToAnthropic(openai.ResponsesRequest{
		Model: "gpt-test",
		Input: json.RawMessage(`"hello"`),
		Tools: []openai.Tool{
			{
				Type: "custom",
				Name: "apply_patch",
				Format: map[string]any{
					"definition": `begin_patch: "*** Begin Patch" end_patch: "*** End Patch" add_hunk: "*** Add File: "`,
				},
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("ToAnthropic() error = %v", err)
	}

	// Verify plugin hooks were called in order
	expectedOrder := []string{"RewriteMessages", "InjectTools", "MutateRequest"}
	if len(recorder.called) != 3 {
		t.Fatalf("plugin hooks called = %v, want %v", recorder.called, expectedOrder)
	}
	for i, name := range expectedOrder {
		if recorder.called[i] != name {
			t.Fatalf("hook %d = %s, want %s", i, recorder.called[i], name)
		}
	}

	// Verify MutateRequest saw the proxy tools (apply_patch_batch etc.)
	if converted.Metadata == nil || converted.Metadata["mutated_by_plugin"] != "yes" {
		t.Fatalf("MutateRequest was not called or did not modify metadata: %+v", converted.Metadata)
	}

	// Verify apply_patch was split into proxy tools
	if !recorder.sawApplyPatchBatchInMutate {
		t.Fatalf("MutateRequest did not see apply_patch_batch in req.Tools")
	}

	// Verify plugin tool is present
	hasPluginTool := false
	for _, tool := range converted.Tools {
		if tool.Name == "plugin_tool" {
			hasPluginTool = true
			break
		}
	}
	if !hasPluginTool {
		t.Fatalf("plugin_tool not found in converted tools: %+v", converted.Tools)
	}
}

func TestToAnthropicNamespaceToolsWithPluginInjectedTools(t *testing.T) {
	cfg := config.Config{
		DefaultMaxTokens: 1024,
		Routes:           map[string]config.RouteEntry{"gpt-test": {Provider: "default", Model: "claude-test"}},
		Cache:            config.CacheConfig{Mode: "off"},
	}
	plugins := plugin.NewRegistry(nil)
	injector := &bridgeToolInjector{}
	plugins.Register(injector)
	bridgeUnderTest := bridge.New(cfg, cache.NewMemoryRegistry(), pluginhooks.PluginHooksFromRegistry(plugins))

	// Send request with namespace tool + plugin tool
	converted, _, err := bridgeUnderTest.ToAnthropic(openai.ResponsesRequest{
		Model: "gpt-test",
		Input: json.RawMessage(`"hello"`),
		Tools: []openai.Tool{
			{
				Type:        "namespace",
				Name:        "mcp__deepwiki__",
				Description: "DeepWiki tools",
				Tools: []openai.Tool{
					{
						Type:        "function",
						Name:        "ask_question",
						Description: "Ask a repository question",
						Parameters: map[string]any{
							"type":     "object",
							"required": []string{"repoName", "question"},
							"properties": map[string]any{
								"repoName": map[string]any{"type": "string"},
								"question": map[string]any{"type": "string"},
							},
						},
					},
				},
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("ToAnthropic() error = %v", err)
	}

	// Verify namespace was flattened and plugin tool is present
	hasNamespaceTool := false
	hasPluginTool := false
	for _, tool := range converted.Tools {
		if tool.Name == "mcp__deepwiki__ask_question" {
			hasNamespaceTool = true
		}
		if tool.Name == "plugin_tool" {
			hasPluginTool = true
		}
	}
	if !hasNamespaceTool {
		t.Fatalf("flattened namespace tool not found: %+v", converted.Tools)
	}
	if !hasPluginTool {
		t.Fatalf("plugin_tool not found: %+v", converted.Tools)
	}
}
