package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"log/slog"
	"moonbridge/internal/extension/websearch"
	"moonbridge/internal/protocol/chat"
	"moonbridge/internal/protocol/google"
	openai "moonbridge/internal/protocol/openai"
)

// ============================================================================
// Injected Web Search Orchestration (shared by chat + google protocols)
// ============================================================================

// hasWebSearchTool checks whether the OpenAI request includes web_search tools.
func hasWebSearchTool(req openai.ResponsesRequest) bool {
	for _, t := range req.Tools {
		if t.Type == "web_search" || t.Type == "web_search_preview" {
			return true
		}
	}
	return false
}

// maxSearchRounds returns the configured max search rounds from the server config.
func (s *Server) maxSearchRounds() int {
	rounds := 5
	if s.runtime != nil {
		rounds = s.runtime.Current().Config.SearchMaxRounds
	}
	if rounds <= 0 {
		rounds = 5
	}
	return rounds
}

// ============================================================================
// Chat (openai-chat) protocol injected search
// ============================================================================

// injectChatSearchTools adds tavily_search / firecrawl_fetch function tools
// to the Chat request when the original request requested web_search.
func injectChatSearchTools(req *chat.ChatRequest, firecrawlKey string) {
	// Remove existing web_search tools (they'll be replaced with injected ones).
	filtered := make([]chat.ChatTool, 0, len(req.Tools))
	for _, t := range req.Tools {
		if t.Function.Name == "web_search" || t.Function.Name == "web_search_preview" {
			continue
		}
		filtered = append(filtered, t)
	}
	req.Tools = filtered

	tools := make([]chat.ChatTool, 0, 2)
	tools = append(tools, chat.ChatTool{
		Type: "function",
		Function: chat.FunctionDef{
			Name:        "tavily_search",
			Description: "Search the web using Tavily. Returns search results with titles, URLs, and content snippets. Call this when you need up-to-date information from the internet.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query":       map[string]any{"type": "string", "description": "Search query (required)."},
					"max_results": map[string]any{"type": "integer", "description": "Maximum number of results.", "default": 5},
				},
				"required": []string{"query"},
			},
		},
	})
	if firecrawlKey != "" {
		tools = append(tools, chat.ChatTool{
			Type: "function",
			Function: chat.FunctionDef{
				Name:        "firecrawl_fetch",
				Description: "Fetch and extract the full content of a web page as clean markdown using Firecrawl.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"url": map[string]any{"type": "string", "description": "URL of the web page to fetch."},
					},
					"required": []string{"url"},
				},
			},
		})
	}
	req.Tools = append(req.Tools, tools...)
}

// executeChatSearchLoop implements the multi-round search loop for Chat protocol.
func (s *Server) executeChatSearchLoop(
	ctx context.Context,
	client *chat.Client,
	req *chat.ChatRequest,
	tavilyKey, firecrawlKey string,
	maxRounds int,
) (*chat.ChatResponse, error) {
	log := slog.Default()
	tavily := websearch.NewTavilyClient(tavilyKey)
	var firecrawl *websearch.FirecrawlClient
	if firecrawlKey != "" {
		firecrawl = websearch.NewFirecrawlClient(firecrawlKey)
	}

	for round := 0; round < maxRounds; round++ {
		resp, err := client.CreateChat(ctx, req)
		if err != nil {
			return nil, err
		}
		if len(resp.Choices) == 0 {
			return resp, nil
		}

		msg := resp.Choices[0].Message
		// Defensive: ensure message role is set. Upstream may return empty role
		// in some error-recovery scenarios, which would break the alternating
		// user/assistant/tool contract on subsequent rounds.
		if msg.Role == "" {
			msg.Role = "assistant"
		}
		if len(msg.ToolCalls) == 0 {
			return resp, nil
		}

		// Filter search vs non-search tool calls.
		var searchCalls, nonSearchCalls []chat.ToolCall
		for _, tc := range msg.ToolCalls {
			switch tc.Function.Name {
			case "tavily_search", "firecrawl_fetch":
				searchCalls = append(searchCalls, tc)
			case "web_search", "web_search_preview":
				searchCalls = append(searchCalls, tc)
			default:
				nonSearchCalls = append(nonSearchCalls, tc)
			}
		}
		if len(searchCalls) == 0 {
			return resp, nil
		}
		if len(nonSearchCalls) > 0 {
			// Execute search calls as side effect, return only non-search content.
			var toolResultMsgs []chat.ChatMessage
			for _, tc := range searchCalls {
				result, execErr := executeChatSearchCall(ctx, tavily, firecrawl, tc)
				if execErr != nil {
					log.Warn("Chat搜索执行失败（混合调用）", "tool", tc.Function.Name, "error", execErr)
					result = fmt.Sprintf("Search error: %s", execErr.Error())
				}
				toolResultMsgs = append(toolResultMsgs, chat.ChatMessage{
					Role:       "tool",
					ToolCallID: tc.ID,
					Content:    result,
				})
			}
			req.Messages = append(req.Messages, msg)
			req.Messages = append(req.Messages, toolResultMsgs...)
			return resp, nil
		}

		// Execute search/fetch calls.
		var toolResultMsgs []chat.ChatMessage
		for _, tc := range searchCalls {
			result, execErr := executeChatSearchCall(ctx, tavily, firecrawl, tc)
			if execErr != nil {
				log.Warn("搜索执行失败", "tool", tc.Function.Name, "error", execErr)
				result = fmt.Sprintf("Search error: %s", execErr.Error())
			}
			toolResultMsgs = append(toolResultMsgs, chat.ChatMessage{
				Role:       "tool",
				ToolCallID: tc.ID,
				Content:    result,
			})
		}

		// Append original assistant message (preserving reasoning_content etc.)
		// and tool results for next round.
		req.Messages = append(req.Messages, msg)
		req.Messages = append(req.Messages, toolResultMsgs...)

		log.Debug("Chat 搜索循环轮次", "round", round+1, "tools_executed", len(searchCalls))
	}
	return nil, fmt.Errorf("chat search loop exceeded max rounds (%d)", maxRounds)
}

func executeChatSearchCall(
	ctx context.Context,
	tavily *websearch.TavilyClient,
	firecrawl *websearch.FirecrawlClient,
	tc chat.ToolCall,
) (string, error) {
	// Chat API returns function.arguments as a JSON string. When decoded as
	// json.RawMessage, the outer quotes are preserved. Unquote before parsing.
	args := unquoteRawJSON(tc.Function.Arguments)

	switch tc.Function.Name {
	case "tavily_search", "web_search", "web_search_preview":
		var params struct {
			Query      string `json:"query"`
			MaxResults int    `json:"max_results"`
		}
		if err := json.Unmarshal(args, &params); err != nil {
			return "", fmt.Errorf("parse search params: %w", err)
		}
		if params.Query == "" {
			return "", fmt.Errorf("search: query is required")
		}
		result, err := tavily.Search(ctx, websearch.SearchRequest{
			Query:      params.Query,
			MaxResults: params.MaxResults,
		})
		if err != nil {
			return "", err
		}
		return websearch.FormatTavilyResults(result), nil

	case "firecrawl_fetch":
		if firecrawl == nil {
			return "", fmt.Errorf("firecrawl not configured")
		}
		var params struct {
			URL string `json:"url"`
		}
		if err := json.Unmarshal(args, &params); err != nil {
			return "", fmt.Errorf("parse fetch params: %w", err)
		}
		if params.URL == "" {
			return "", fmt.Errorf("fetch: url is required")
		}
		result, err := firecrawl.Fetch(ctx, websearch.FetchRequest{
			URL:             params.URL,
			Formats:         []string{"markdown"},
			OnlyMainContent: true,
		})
		if err != nil {
			return "", err
		}
		return websearch.FormatFirecrawlResult(result), nil

	default:
		return "", fmt.Errorf("unknown search tool: %s", tc.Function.Name)
	}
}

// ============================================================================
// Google GenAI protocol injected search
// ============================================================================

// injectGoogleSearchTools adds tavily_search / firecrawl_fetch function
// declarations to the Google GenerateContent request.
func injectGoogleSearchTools(req *google.GenerateContentRequest, firecrawlKey string) {
	// Remove any existing tool that has a "web_search" function declaration.
	filtered := make([]google.Tool, 0, len(req.Tools))
	for _, t := range req.Tools {
		hasWebSearch := false
		for _, fd := range t.FunctionDeclarations {
			if fd.Name == "web_search" || fd.Name == "web_search_preview" {
				hasWebSearch = true
				break
			}
		}
		if !hasWebSearch {
			filtered = append(filtered, t)
		}
	}
	req.Tools = filtered

	fds := []google.FunctionDeclaration{
		{
			Name:        "tavily_search",
			Description: "Search the web using Tavily. Returns search results with titles, URLs, and content snippets.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query":       map[string]any{"type": "string", "description": "Search query."},
					"max_results": map[string]any{"type": "integer", "description": "Max results.", "default": 5},
				},
				"required": []string{"query"},
			},
		},
	}
	if firecrawlKey != "" {
		fds = append(fds, google.FunctionDeclaration{
			Name:        "firecrawl_fetch",
			Description: "Fetch a web page content as markdown using Firecrawl.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"url": map[string]any{"type": "string", "description": "Page URL."},
				},
				"required": []string{"url"},
			},
		})
	}
	req.Tools = append(req.Tools, google.Tool{
		FunctionDeclarations: fds,
	})
}

// executeGoogleSearchLoop implements the multi-round search loop for Google GenAI.
func (s *Server) executeGoogleSearchLoop(
	ctx context.Context,
	client *google.Client,
	model string,
	req *google.GenerateContentRequest,
	tavilyKey, firecrawlKey string,
	maxRounds int,
) (*google.GenerateContentResponse, error) {
	log := slog.Default()
	tavily := websearch.NewTavilyClient(tavilyKey)
	var firecrawl *websearch.FirecrawlClient
	if firecrawlKey != "" {
		firecrawl = websearch.NewFirecrawlClient(firecrawlKey)
	}

	for round := 0; round < maxRounds; round++ {
		resp, err := client.GenerateContent(ctx, model, req)
		if err != nil {
			return nil, err
		}
		if len(resp.Candidates) == 0 {
			return resp, nil
		}

		// Collect function calls from ALL candidates, not just the first.
		var funcCalls []google.FunctionCall
		for _, c := range resp.Candidates {
			if len(c.Content.Parts) == 0 {
				continue
			}
			funcCalls = append(funcCalls, googleFuncCalls(c.Content.Parts)...)
		}
		if len(funcCalls) == 0 {
			return resp, nil
		}
		searchCalls := filterGoogleSearchCalls(funcCalls)
		nonSearchCalls := filterGoogleNonSearchCalls(funcCalls)
		if len(searchCalls) == 0 {
			return resp, nil
		}
		if len(nonSearchCalls) > 0 {
			// Execute search calls as side effect, feed results to the model.
			responseParts := make([]google.Part, 0, len(searchCalls))
			for _, fc := range searchCalls {
				result, execErr := executeGoogleSearchCall(ctx, tavily, firecrawl, fc)
				if execErr != nil {
					log.Warn("Google搜索执行失败（混合调用）", "tool", fc.Name, "error", execErr)
					result = execErr.Error()
				}
				respJSON, _ := json.Marshal(map[string]any{"result": result})
				responseParts = append(responseParts, google.Part{
					FunctionResponse: &google.FunctionResponse{
						Name:     fc.Name,
						Response: respJSON,
					},
				})
			}
			for _, c := range resp.Candidates {
				if len(c.Content.Parts) > 0 {
					req.Contents = append(req.Contents, google.Content{
						Role:  "model",
						Parts: c.Content.Parts,
					})
				}
			}
			req.Contents = append(req.Contents, google.Content{
				Role:  "function",
				Parts: responseParts,
			})
			return resp, nil
		}

		// Execute search calls and build function responses.
		responseParts := make([]google.Part, 0, len(searchCalls))
		for _, fc := range searchCalls {
			result, execErr := executeGoogleSearchCall(ctx, tavily, firecrawl, fc)
			if execErr != nil {
				log.Warn("Google 搜索执行失败", "tool", fc.Name, "error", execErr)
				result = fmt.Sprintf("Search error: %s", execErr.Error())
			}
			respJSON, _ := json.Marshal(map[string]any{"result": result})
			responseParts = append(responseParts, google.Part{
				FunctionResponse: &google.FunctionResponse{
					Name:     fc.Name,
					Response: respJSON,
				},
			})
		}

		// Append model response + function response for next round.
		for _, c := range resp.Candidates {
			if len(c.Content.Parts) > 0 {
				req.Contents = append(req.Contents, google.Content{
					Role:  "model",
					Parts: c.Content.Parts,
				})
			}
		}
		req.Contents = append(req.Contents, google.Content{
			Role:  "function",
			Parts: responseParts,
		})

		log.Debug("Google 搜索循环轮次", "round", round+1, "tools_executed", len(searchCalls))
	}
	return nil, fmt.Errorf("google search loop exceeded max rounds (%d)", maxRounds)
}

func googleFuncCalls(parts []google.Part) []google.FunctionCall {
	var calls []google.FunctionCall
	for _, p := range parts {
		if p.FunctionCall != nil {
			calls = append(calls, *p.FunctionCall)
		}
	}
	return calls
}

func filterGoogleSearchCalls(calls []google.FunctionCall) []google.FunctionCall {
	var result []google.FunctionCall
	for _, c := range calls {
		if c.Name == "tavily_search" || c.Name == "firecrawl_fetch" {
			result = append(result, c)
		}
	}
	return result
}

func filterGoogleNonSearchCalls(calls []google.FunctionCall) []google.FunctionCall {
	var result []google.FunctionCall
	for _, c := range calls {
		if c.Name != "tavily_search" && c.Name != "firecrawl_fetch" {
			result = append(result, c)
		}
	}
	return result
}

func executeGoogleSearchCall(
	ctx context.Context,
	tavily *websearch.TavilyClient,
	firecrawl *websearch.FirecrawlClient,
	fc google.FunctionCall,
) (string, error) {
	switch fc.Name {
	case "tavily_search", "web_search", "web_search_preview":
		var params struct {
			Query      string `json:"query"`
			MaxResults int    `json:"max_results"`
		}
		argsJSON, _ := json.Marshal(fc.Args)
		if err := json.Unmarshal(argsJSON, &params); err != nil {
			return "", fmt.Errorf("parse search params: %w", err)
		}
		if params.Query == "" {
			return "", fmt.Errorf("search: query is required")
		}
		result, err := tavily.Search(ctx, websearch.SearchRequest{
			Query:      params.Query,
			MaxResults: params.MaxResults,
		})
		if err != nil {
			return "", err
		}
		return websearch.FormatTavilyResults(result), nil

	case "firecrawl_fetch":
		if firecrawl == nil {
			return "", fmt.Errorf("firecrawl not configured")
		}
		var params struct {
			URL string `json:"url"`
		}
		argsJSON, _ := json.Marshal(fc.Args)
		if err := json.Unmarshal(argsJSON, &params); err != nil {
			return "", fmt.Errorf("parse fetch params: %w", err)
		}
		if params.URL == "" {
			return "", fmt.Errorf("fetch: url is required")
		}
		result, err := firecrawl.Fetch(ctx, websearch.FetchRequest{
			URL:             params.URL,
			Formats:         []string{"markdown"},
			OnlyMainContent: true,
		})
		if err != nil {
			return "", err
		}
		return websearch.FormatFirecrawlResult(result), nil

	default:
		return "", fmt.Errorf("unknown search tool: %s", fc.Name)
	}
}

// Formatting helpers — delegated to exported websearch package functions.

// ============================================================================
// Chat streaming search loop
// ============================================================================

// chatSearchBufferedStream implements a streaming search loop by buffering
// ChatStreamChunk events, detecting search tool calls, executing them, and
// continuing the conversation until no more search tools are called.
// Returns a channel that replays all events from all rounds as a single stream.
func (s *Server) chatSearchBufferedStream(
	ctx context.Context,
	client *chat.Client,
	req *chat.ChatRequest,
	tavilyKey, firecrawlKey string,
	maxRounds int,
) (<-chan chat.ChatStreamChunk, error) {
	log := slog.Default()
	tavily := websearch.NewTavilyClient(tavilyKey)
	var firecrawl *websearch.FirecrawlClient
	if firecrawlKey != "" {
		firecrawl = websearch.NewFirecrawlClient(firecrawlKey)
	}

	allEvents := make([]chat.ChatStreamChunk, 0, 128)
	exhausted := true
	for round := 0; round < maxRounds; round++ {
		stream, err := client.StreamChat(ctx, req)
		if err != nil {
			return nil, err
		}

		events, roundErr := collectChatStream(ctx, stream)
		if roundErr != nil {
			return nil, roundErr
		}

		// Reconstruct tool calls from all streaming chunks.
		toolCalls := collectStreamToolCalls(events)
		if len(toolCalls) == 0 {
			allEvents = append(allEvents, events...)
			exhausted = false
			break
		}

		// Filter search vs non-search tool calls.
		var searchCalls, nonSearchCalls []chat.ToolCall
		for _, tc := range toolCalls {
			switch tc.Function.Name {
			case "tavily_search", "firecrawl_fetch":
				searchCalls = append(searchCalls, tc)
			case "web_search", "web_search_preview":
				searchCalls = append(searchCalls, tc)
			default:
				nonSearchCalls = append(nonSearchCalls, tc)
			}
		}
		if len(searchCalls) == 0 {
			allEvents = append(allEvents, events...)
			exhausted = false
			break
		}
		if len(nonSearchCalls) > 0 {
			// Execute search calls, feed results to next round.
			var toolResultMsgs []chat.ChatMessage
			for _, tc := range searchCalls {
				result, execErr := executeChatSearchCall(ctx, tavily, firecrawl, tc)
				if execErr != nil {
					log.Warn("流式搜索执行失败（混合调用）", "tool", tc.Function.Name, "error", execErr)
					result = fmt.Sprintf("Search error: %s", execErr.Error())
				}
				toolResultMsgs = append(toolResultMsgs, chat.ChatMessage{
					Role:       "tool",
					ToolCallID: tc.ID,
					Content:    result,
				})
			}
			asstContent := collectChatStreamContent(events)
			reasoningContent := collectChatStreamReasoning(events)
			req.Messages = append(req.Messages, chat.ChatMessage{
				Role:             "assistant",
				Content:          asstContent,
				ToolCalls:        toolCalls,
				ReasoningContent: reasoningContent,
			})
			req.Messages = append(req.Messages, toolResultMsgs...)
			allEvents = append(allEvents, events...)
			exhausted = false
			break
		}

		// Execute search calls.
		var toolResultMsgs []chat.ChatMessage
		for _, tc := range searchCalls {
			result, execErr := executeChatSearchCall(ctx, tavily, firecrawl, tc)
			if execErr != nil {
				log.Warn("搜索执行失败", "tool", tc.Function.Name, "error", execErr)
				result = fmt.Sprintf("Search error: %s", execErr.Error())
			}
			toolResultMsgs = append(toolResultMsgs, chat.ChatMessage{
				Role:       "tool",
				ToolCallID: tc.ID,
				Content:    result,
			})
		}

		// Build assistant message from the streaming response.
		asstContent := collectChatStreamContent(events)
		reasoningContent := collectChatStreamReasoning(events)
		req.Messages = append(req.Messages, chat.ChatMessage{
			Role:             "assistant",
			Content:          asstContent,
			ToolCalls:        toolCalls,
			ReasoningContent: reasoningContent,
		})
		req.Messages = append(req.Messages, toolResultMsgs...)

		log.Debug("Chat 流式搜索轮次", "round", round+1, "tools_executed", len(searchCalls))
	}
	if exhausted && maxRounds > 0 {
		return nil, fmt.Errorf("chat streaming search loop exceeded max rounds (%d)", maxRounds)
	}

	// Return all events as a single channel.
	ch := make(chan chat.ChatStreamChunk, len(allEvents))
	for _, ev := range allEvents {
		ch <- ev
	}
	close(ch)
	return ch, nil
}

// collectChatStream collects all events from a Chat stream channel.
func collectChatStream(ctx context.Context, stream <-chan chat.ChatStreamChunk) ([]chat.ChatStreamChunk, error) {
	var events []chat.ChatStreamChunk
	for {
		select {
		case <-ctx.Done():
			return events, ctx.Err()
		case chunk, ok := <-stream:
			if !ok {
				return events, nil
			}
			events = append(events, chunk)
		}
	}
}

// collectStreamToolCalls reconstructs tool calls across streaming chunks.
// Tool call fields (id/name/arguments) may arrive in separate chunks.
func collectStreamToolCalls(events []chat.ChatStreamChunk) []chat.ToolCall {
	choiceCalls := make(map[int][]chat.ToolCall)
	choicePosMap := make(map[int]map[int]int) // choice -> tool call position -> slot index
	choiceOrder := make([]int, 0, 2)

	for _, ev := range events {
		for _, sc := range ev.Choices {
			ci := sc.Index
			if _, ok := choicePosMap[ci]; !ok {
				choicePosMap[ci] = make(map[int]int)
				choiceOrder = append(choiceOrder, ci)
			}
			for idx, tc := range sc.Delta.ToolCalls {
				pos := idx
				if tc.Index != nil && *tc.Index >= 0 {
					pos = *tc.Index
				}

				slot, exists := choicePosMap[ci][pos]
				if !exists {
					slot = len(choiceCalls[ci])
					choicePosMap[ci][pos] = slot
					choiceCalls[ci] = append(choiceCalls[ci], chat.ToolCall{
						Type:     "function",
						Function: chat.ToolCallFunc{},
					})
				}

				current := choiceCalls[ci][slot]
				if tc.ID != "" {
					current.ID = tc.ID
				}
				if tc.Type != "" {
					current.Type = tc.Type
				}
				if tc.Function.Name != "" {
					current.Function.Name = tc.Function.Name
				}
				if len(tc.Function.Arguments) > 0 {
					existing := string(current.Function.Arguments)
					current.Function.Arguments = json.RawMessage(appendToolCallArgs(existing, tc.Function.Arguments))
				}
				choiceCalls[ci][slot] = current
			}
		}
	}

	for _, ci := range choiceOrder {
		merged := choiceCalls[ci]
		if len(merged) == 0 {
			continue
		}
		return merged
	}
	return nil
}

// appendToolCallArgs appends delta arguments to the current accumulated JSON text.
func appendToolCallArgs(current string, delta json.RawMessage) string {
	if len(delta) == 0 {
		return current
	}
	decoded := unquoteRawJSON(delta)
	if len(decoded) == 0 {
		return current
	}
	part := string(decoded)
	if current == "" {
		return part
	}
	// Prefer the newer payload when both sides are complete JSON values.
	// Some providers emit snapshots instead of strict deltas.
	if json.Valid([]byte(current)) && json.Valid([]byte(part)) {
		return part
	}
	// Handle cumulative chunks (new part already contains old prefix/suffix).
	if strings.HasPrefix(part, current) {
		return part
	}
	if strings.HasSuffix(current, part) {
		return current
	}
	return current + part
}

// collectChatStreamContent builds the assistant's full text content from all chunks.
func collectChatStreamContent(events []chat.ChatStreamChunk) string {
	var sb strings.Builder
	for _, ev := range events {
		for _, sc := range ev.Choices {
			sb.WriteString(sc.Delta.Content)
		}
	}
	return sb.String()
}

// collectChatStreamReasoning collects reasoning_content from all streaming chunks.
// Reasoning content is concatenated (DeepSeek streams it in pieces).
func collectChatStreamReasoning(events []chat.ChatStreamChunk) string {
	var sb strings.Builder
	for _, ev := range events {
		for _, sc := range ev.Choices {
			sb.WriteString(sc.Delta.ReasoningContent)
		}
	}
	return sb.String()
}

// unquoteRawJSON unwraps a JSON-string-encoded value.
// Chat API returns function.arguments as a quoted JSON string. When stored as
// json.RawMessage, the outer quotes are preserved. This function strips them
// so the result is a raw JSON object ready for json.Unmarshal.
func unquoteRawJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) < 2 || raw[0] != '"' {
		return raw
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return raw
	}
	return json.RawMessage(s)
}
