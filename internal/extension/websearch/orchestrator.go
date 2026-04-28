package websearch

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"moonbridge/internal/foundation/logger"
	"moonbridge/internal/protocol/anthropic"
)

// ToolHandler executes a tool given its input and returns a formatted result string.
type ToolHandler func(context.Context, json.RawMessage) (string, error)

// Orchestrator wraps an Anthropic client and transparently executes
// web_search / web_fetch (or tavily_search / firecrawl_fetch in injected mode)
// tool calls server-side via Tavily / Firecrawl.
// It presents the same interface as anthropic.Client to callers.
type Orchestrator struct {
	anthropic    *anthropic.Client
	tavily       *TavilyClient
	firecrawl    *FirecrawlClient
	maxRounds    int
	toolHandlers map[string]ToolHandler
}

// OrchestratorConfig configures the search orchestrator.
type OrchestratorConfig struct {
	Anthropic       *anthropic.Client
	TavilyKey       string
	FirecrawlKey    string
	SearchMaxRounds int
	ToolHandlers    map[string]ToolHandler
}

// NewOrchestrator creates a new search orchestrator with default
// handlers for web_search and web_fetch tool names.
func NewOrchestrator(cfg OrchestratorConfig) *Orchestrator {
	o := &Orchestrator{
		anthropic: cfg.Anthropic,
		tavily:    NewTavilyClient(cfg.TavilyKey),
		maxRounds: cfg.SearchMaxRounds,
	}
	if cfg.FirecrawlKey != "" {
		o.firecrawl = NewFirecrawlClient(cfg.FirecrawlKey)
	}
	if o.maxRounds <= 0 {
		o.maxRounds = 5
	}
	// Use provided tool handlers or default to web_search/web_fetch
	o.toolHandlers = cfg.ToolHandlers
	if o.toolHandlers == nil {
		o.toolHandlers = map[string]ToolHandler{
			"web_search": o.executeTavilySearch,
			"web_fetch":  o.executeFirecrawlFetch,
		}
		// Only register web_fetch if Firecrawl is configured
		if cfg.FirecrawlKey == "" {
			delete(o.toolHandlers, "web_fetch")
		}
	}
	return o
}

// NewInjectedOrchestrator creates a search orchestrator for "injected" mode,
// where tavily_search and firecrawl_fetch are injected as function tools
// to the provider.
func NewInjectedOrchestrator(cfg OrchestratorConfig) *Orchestrator {
	o := &Orchestrator{
		anthropic: cfg.Anthropic,
		tavily:    NewTavilyClient(cfg.TavilyKey),
		maxRounds: cfg.SearchMaxRounds,
	}
	if cfg.FirecrawlKey != "" {
		o.firecrawl = NewFirecrawlClient(cfg.FirecrawlKey)
	}
	if o.maxRounds <= 0 {
		o.maxRounds = 5
	}
	o.toolHandlers = map[string]ToolHandler{
		"tavily_search":   o.executeTavilySearch,
		"firecrawl_fetch": o.executeFirecrawlFetch,
	}
	if cfg.FirecrawlKey == "" {
		delete(o.toolHandlers, "firecrawl_fetch")
	}
	return o
}

// CreateMessage sends a request and transparently executes search/fetch
// tool loops. The caller receives a fully resolved response.
func (o *Orchestrator) CreateMessage(ctx context.Context, req anthropic.MessageRequest) (anthropic.MessageResponse, error) {
	log := logger.L()
	for round := 0; round <= o.maxRounds; round++ {
		resp, err := o.anthropic.CreateMessage(ctx, req)
		if err != nil {
			return anthropic.MessageResponse{}, err
		}

		if resp.StopReason != "tool_use" {
			return resp, nil
		}

		toolUses, otherBlocks := splitToolUses(resp.Content)
		searchUses := o.filterSearchTools(toolUses)
		nonSearchUses := subtractToolUses(toolUses, searchUses)

		// If there are non-search tool_use blocks, return the response
		// so the caller (Bridge) can handle them as normal tool calls.
		if len(nonSearchUses) > 0 {
			return resp, nil
		}

		if len(searchUses) == 0 {
			return resp, nil
		}

		// Execute search/fetch calls and build tool results.
		toolResults := make([]anthropic.ContentBlock, 0, len(searchUses))
		for _, tu := range searchUses {
			result, execErr := o.executeSearch(ctx, tu)
			if execErr != nil {
				log.Warn("搜索执行失败", "tool", tu.Name, "error", execErr)
				toolResults = append(toolResults, anthropic.ContentBlock{
					Type:      "tool_result",
					ToolUseID: tu.ID,
					Content:   json.RawMessage(fmt.Sprintf(`"Search error: %s"`, execErr.Error())),
				})
				continue
			}
			toolResults = append(toolResults, anthropic.ContentBlock{
				Type:      "tool_result",
				ToolUseID: tu.ID,
				Content:   json.RawMessage(fmt.Sprintf(`"%s"`, escapeForJSON(result))),
			})
		}

		// Append the assistant message (with search tool_use blocks) and
		// user message (with tool_results) to the request for the next round.
		req.Messages = append(req.Messages, anthropic.Message{
			Role:    "assistant",
			Content: toContentBlocks(append(searchUses, otherBlocks...)),
		})
		req.Messages = append(req.Messages, anthropic.Message{
			Role:    "user",
			Content: toolResults,
		})

		log.Debug("搜索循环轮次完成", "round", round+1, "tools_executed", len(searchUses))
	}

	return anthropic.MessageResponse{}, fmt.Errorf("search loop exceeded max rounds (%d)", o.maxRounds)
}

// StreamMessage implements streaming with search loop support.
// All events are collected across rounds and returned as a single stream.
func (o *Orchestrator) StreamMessage(ctx context.Context, req anthropic.MessageRequest) (anthropic.Stream, error) {
	log := logger.L()
	var allEvents []anthropic.StreamEvent
	for round := 0; round <= o.maxRounds; round++ {
		stream, err := o.anthropic.StreamMessage(ctx, req)
		if err != nil {
			return nil, err
		}

		events, err := collectStream(stream)
		stream.Close()
		if err != nil {
			return nil, err
		}

		if round > 0 {
			// Only keep the final round's events for the caller.
			// Earlier rounds were internal search loops.
			allEvents = events
		} else {
			allEvents = events
		}

		// Detect stop_reason from message_delta event.
		stopReason := "end_turn"
		var lastUsage *anthropic.Usage
		for i := len(events) - 1; i >= 0; i-- {
			if events[i].Type == "message_delta" {
				if events[i].Delta.StopReason != "" {
					stopReason = events[i].Delta.StopReason
				}
				lastUsage = events[i].Usage
				break
			}
		}

		if stopReason != "tool_use" {
			// Merge usage from final round into message_start event.
			if lastUsage != nil {
				allEvents = injectUsageIntoStart(allEvents, *lastUsage)
			}
			return &staticStream{events: allEvents}, nil
		}

		// Parse content blocks from events and find search tool calls.
		toolUses := collectToolUsesFromEvents(events)
		searchUses := o.filterSearchTools(toolUses)
		nonSearchUses := subtractToolUses(toolUses, searchUses)

		if len(nonSearchUses) > 0 || len(searchUses) == 0 {
			allEvents = events
			if lastUsage != nil {
				allEvents = injectUsageIntoStart(allEvents, *lastUsage)
			}
			return &staticStream{events: allEvents}, nil
		}

		// Execute searches and build follow-up request.
		toolResults := make([]anthropic.ContentBlock, 0, len(searchUses))
		for _, tu := range searchUses {
			result, execErr := o.executeSearch(ctx, tu)
			if execErr != nil {
				log.Warn("流式搜索执行失败", "tool", tu.Name, "error", execErr)
				toolResults = append(toolResults, anthropic.ContentBlock{
					Type:      "tool_result",
					ToolUseID: tu.ID,
					Content:   json.RawMessage(fmt.Sprintf(`"Search error: %s"`, execErr.Error())),
				})
				continue
			}
			toolResults = append(toolResults, anthropic.ContentBlock{
				Type:      "tool_result",
				ToolUseID: tu.ID,
				Content:   json.RawMessage(fmt.Sprintf(`"%s"`, escapeForJSON(result))),
			})
		}

		req.Messages = append(req.Messages, anthropic.Message{
			Role:    "assistant",
			Content: toolUses,
		})
		req.Messages = append(req.Messages, anthropic.Message{
			Role:    "user",
			Content: toolResults,
		})

		log.Debug("流式搜索循环轮次完成", "round", round+1, "tools_executed", len(searchUses))
	}

	return nil, fmt.Errorf("stream search loop exceeded max rounds (%d)", o.maxRounds)
}

// executeSearch runs a Tavily search or Firecrawl fetch based on the tool_use block.
func (o *Orchestrator) executeSearch(ctx context.Context, tu anthropic.ContentBlock) (string, error) {
	handler, ok := o.toolHandlers[tu.Name]
	if !ok {
		return "", fmt.Errorf("unknown search tool: %s", tu.Name)
	}
	return handler(ctx, tu.Input)
}

func (o *Orchestrator) executeTavilySearch(ctx context.Context, raw json.RawMessage) (string, error) {
	var params struct {
		Query          string   `json:"query"`
		SearchDepth    string   `json:"search_depth,omitempty"`
		Topic          string   `json:"topic,omitempty"`
		MaxResults     int      `json:"max_results,omitempty"`
		TimeRange      string   `json:"time_range,omitempty"`
		IncludeDomains []string `json:"include_domains,omitempty"`
		ExcludeDomains []string `json:"exclude_domains,omitempty"`
		IncludeAnswer  bool     `json:"include_answer,omitempty"`
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return "", fmt.Errorf("parse search params: %w", err)
	}
	if params.Query == "" {
		return "", fmt.Errorf("search: query is required")
	}

	result, err := o.tavily.Search(ctx, SearchRequest{
		Query:          params.Query,
		SearchDepth:    params.SearchDepth,
		Topic:          params.Topic,
		MaxResults:     params.MaxResults,
		TimeRange:      params.TimeRange,
		IncludeDomains: params.IncludeDomains,
		ExcludeDomains: params.ExcludeDomains,
		IncludeAnswer:  params.IncludeAnswer,
	})
	if err != nil {
		return "", err
	}
	return formatTavilyResults(result), nil
}

func (o *Orchestrator) executeFirecrawlFetch(ctx context.Context, raw json.RawMessage) (string, error) {
	var params struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return "", fmt.Errorf("parse fetch params: %w", err)
	}
	if params.URL == "" {
		return "", fmt.Errorf("fetch: url is required")
	}

	result, err := o.firecrawl.Fetch(ctx, FetchRequest{
		URL:             params.URL,
		Formats:         []string{"markdown"},
		OnlyMainContent: true,
	})
	if err != nil {
		return "", err
	}
	return formatFirecrawlResult(result), nil
}

// filterSearchTools returns tool_use blocks that are registered search handlers.
func (o *Orchestrator) filterSearchTools(toolUses []anthropic.ContentBlock) []anthropic.ContentBlock {
	var searchUses []anthropic.ContentBlock
	for _, tu := range toolUses {
		if _, ok := o.toolHandlers[tu.Name]; ok {
			searchUses = append(searchUses, tu)
		}
	}
	return searchUses
}

// formatTavilyResults formats Tavily search results as a readable text block.
func formatTavilyResults(result *SearchResult) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Search results for %q:\n\n", result.Query))

	if result.Answer != "" {
		b.WriteString("Answer: ")
		b.WriteString(truncate(result.Answer, 2000))
		b.WriteString("\n\n")
	}

	for i, item := range result.Results {
		if i >= 10 {
			break
		}
		b.WriteString(fmt.Sprintf("%d. [%s](%s)\n", i+1, item.Title, item.URL))
		b.WriteString(fmt.Sprintf("   Score: %.2f\n", item.Score))
		b.WriteString(fmt.Sprintf("   %s\n\n", truncate(item.Content, 500)))
	}
	return b.String()
}

// formatFirecrawlResult formats Firecrawl scrape results as a readable text block.
func formatFirecrawlResult(result *FetchResult) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Content from %s:\n\n", result.Data.Metadata.SourceURL))
	if result.Data.Metadata.Title != "" {
		b.WriteString(fmt.Sprintf("Title: %s\n\n", result.Data.Metadata.Title))
	}
	b.WriteString(truncate(result.Data.Markdown, 8000))
	return b.String()
}

// splitToolUses separates tool_use blocks from other content blocks.
func splitToolUses(blocks []anthropic.ContentBlock) (toolUses, others []anthropic.ContentBlock) {
	for _, b := range blocks {
		if b.Type == "tool_use" {
			toolUses = append(toolUses, b)
		} else {
			others = append(others, b)
		}
	}
	return
}

// subtractToolUses returns tool_use blocks in a that are not in b.
func subtractToolUses(a, b []anthropic.ContentBlock) []anthropic.ContentBlock {
	bIDs := make(map[string]bool, len(b))
	for _, tu := range b {
		bIDs[tu.ID] = true
	}
	var result []anthropic.ContentBlock
	for _, tu := range a {
		if !bIDs[tu.ID] {
			result = append(result, tu)
		}
	}
	return result
}

// toContentBlocks converts tool_use blocks to generic content blocks.
func toContentBlocks(toolUses []anthropic.ContentBlock) []anthropic.ContentBlock {
	blocks := make([]anthropic.ContentBlock, len(toolUses))
	copy(blocks, toolUses)
	return blocks
}

// collectStream reads all events from a stream into a slice.
func collectStream(stream anthropic.Stream) ([]anthropic.StreamEvent, error) {
	var events []anthropic.StreamEvent
	for {
		event, err := stream.Next()
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			return events, err
		}
		events = append(events, event)
	}
	return events, nil
}

// collectToolUsesFromEvents extracts tool_use blocks from stream events.
func collectToolUsesFromEvents(events []anthropic.StreamEvent) []anthropic.ContentBlock {
	var toolUses []anthropic.ContentBlock
	for _, event := range events {
		if event.Type == "content_block_start" && event.ContentBlock != nil && event.ContentBlock.Type == "tool_use" {
			toolUses = append(toolUses, *event.ContentBlock)
		}
	}
	return toolUses
}

// injectUsageIntoStart adds usage data to the message_start event.
func injectUsageIntoStart(events []anthropic.StreamEvent, usage anthropic.Usage) []anthropic.StreamEvent {
	for i, event := range events {
		if event.Type == "message_start" && event.Message != nil {
			event.Message.Usage = usage
			events[i] = event
			break
		}
	}
	return events
}

// staticStream implements anthropic.Stream for a pre-collected slice of events.
type staticStream struct {
	events []anthropic.StreamEvent
	pos    int
}

func (s *staticStream) Next() (anthropic.StreamEvent, error) {
	if s.pos >= len(s.events) {
		return anthropic.StreamEvent{}, fmt.Errorf("EOF")
	}
	event := s.events[s.pos]
	s.pos++
	return event, nil
}

func (s *staticStream) Close() error {
	return nil
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func escapeForJSON(s string) string {
	// Escape backslashes and double quotes for embedding in JSON strings.
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	s = strings.ReplaceAll(s, "\n", "\\n")
	s = strings.ReplaceAll(s, "\r", "\\r")
	s = strings.ReplaceAll(s, "\t", "\\t")
	return s
}
