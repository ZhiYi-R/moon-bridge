package websearch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"log/slog"
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
	HTTPClient      *http.Client
	ToolHandlers    map[string]ToolHandler
}

// NewOrchestrator creates a new search orchestrator with default
// handlers for web_search and web_fetch tool names.
func NewOrchestrator(cfg OrchestratorConfig) *Orchestrator {
	tavilyClient := NewTavilyClientWithHTTP(cfg.TavilyKey, cfg.HTTPClient)
	o := &Orchestrator{
		anthropic: cfg.Anthropic,
		tavily:    tavilyClient,
		maxRounds: cfg.SearchMaxRounds,
	}
	if cfg.FirecrawlKey != "" {
		o.firecrawl = NewFirecrawlClientWithHTTP(cfg.FirecrawlKey, cfg.HTTPClient)
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
	tavilyClient := NewTavilyClientWithHTTP(cfg.TavilyKey, cfg.HTTPClient)
	o := &Orchestrator{
		anthropic: cfg.Anthropic,
		tavily:    tavilyClient,
		maxRounds: cfg.SearchMaxRounds,
	}
	if cfg.FirecrawlKey != "" {
		o.firecrawl = NewFirecrawlClientWithHTTP(cfg.FirecrawlKey, cfg.HTTPClient)
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
	log := slog.Default()
	for round := 0; round < o.maxRounds; round++ {
		resp, err := o.anthropic.CreateMessage(ctx, req)
		if err != nil {
			return anthropic.MessageResponse{}, err
		}

		if resp.StopReason != "tool_use" {
			return resp, nil
		}

		toolUses, _ := splitToolUses(resp.Content)
		searchUses := o.filterSearchTools(toolUses)
		nonSearchUses := subtractToolUses(toolUses, searchUses)

		// If there are non-search tool_use blocks, execute any pending search
		// tools and return the response with search tool_uses filtered out.
		// This prevents injected search tools (tavily_search, firecrawl_fetch)
		// from leaking to the client through the Bridge.
		if len(nonSearchUses) > 0 {
			// Execute search tools first as a side effect.
			for _, tu := range searchUses {
				_, execErr := o.executeSearch(ctx, tu)
				if execErr != nil {
					log.Warn("搜索执行失败（混合调用）", "tool", tu.Name, "error", execErr)
				}
			}
			// Filter search tool_uses from the response content.
			filtered := make([]anthropic.ContentBlock, 0, len(resp.Content))
			for _, block := range resp.Content {
				if block.Type == "tool_use" && o.isSearchTool(block.Name) {
					continue
				}
				filtered = append(filtered, block)
			}
			resp.Content = filtered
			return resp, nil
		}

		if len(searchUses) == 0 {
			return resp, nil
		}

		toolResults := o.buildToolResults(ctx, searchUses)

		// Append the assistant message (with search tool_use blocks) and
		// user message (with tool_results) to the request for the next round.
		req.Messages = append(req.Messages, anthropic.Message{
			Role:    "assistant",
			Content: resp.Content,
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
	log := slog.Default()
	var allEvents []anthropic.StreamEvent
	for round := 0; round < o.maxRounds; round++ {
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

		if len(searchUses) == 0 {
			allEvents = events
			if lastUsage != nil {
				allEvents = injectUsageIntoStart(allEvents, *lastUsage)
			}
			return &staticStream{events: allEvents}, nil
		}
		if len(nonSearchUses) > 0 {
			// Execute search tools as side effect, but return only non-search content.
			for _, tu := range searchUses {
				_, execErr := o.executeSearch(ctx, tu)
				if execErr != nil {
					log.Warn("流式搜索执行失败（混合调用）", "tool", tu.Name, "error", execErr)
				}
			}
			// Filter search tool_uses from the returned events.
			allEvents = events
			if lastUsage != nil {
				allEvents = injectUsageIntoStart(allEvents, *lastUsage)
			}
			return &staticStream{events: allEvents}, nil
		}

		toolResults := o.buildToolResults(ctx, searchUses)

		req.Messages = append(req.Messages, anthropic.Message{
			Role:    "assistant",
			Content: collectContentFromEvents(events),
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
	return FormatTavilyResults(result), nil
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
	return FormatFirecrawlResult(result), nil
}

// isSearchTool returns true if the tool name is a registered search handler.
func (o *Orchestrator) isSearchTool(name string) bool {
	_, ok := o.toolHandlers[name]
	return ok
}

// buildToolResults executes search/fetch for each tool use and returns tool_result blocks.
func (o *Orchestrator) buildToolResults(ctx context.Context, searchUses []anthropic.ContentBlock) []anthropic.ContentBlock {
	log := slog.Default()
	results := make([]anthropic.ContentBlock, 0, len(searchUses))
	for _, tu := range searchUses {
		result, execErr := o.executeSearch(ctx, tu)
		if execErr != nil {
			log.Warn("搜索执行失败", "tool", tu.Name, "error", execErr)
			results = append(results, anthropic.ContentBlock{
				Type:      "tool_result",
				ToolUseID: tu.ID,
				Content:   json.RawMessage(fmt.Sprintf(`"Search error: %s"`, execErr.Error())),
			})
			continue
		}
		results = append(results, anthropic.ContentBlock{
			Type:      "tool_result",
			ToolUseID: tu.ID,
			Content:   json.RawMessage(fmt.Sprintf(`"%s"`, escapeForJSON(result))),
		})
	}
	return results
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
func FormatTavilyResults(result *SearchResult) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Search results for %q:\n\n", result.Query))

	if result.Answer != "" {
		b.WriteString("Answer: ")
		b.WriteString(Truncate(result.Answer, 2000))
		b.WriteString("\n\n")
	}

	for i, item := range result.Results {
		if i >= 10 {
			break
		}
		b.WriteString(fmt.Sprintf("%d. [%s](%s)\n", i+1, item.Title, item.URL))
		b.WriteString(fmt.Sprintf("   Score: %.2f\n", item.Score))
		b.WriteString(fmt.Sprintf("   %s\n\n", Truncate(item.Content, 500)))
	}
	return b.String()
}

// formatFirecrawlResult formats Firecrawl scrape results as a readable text block.
func FormatFirecrawlResult(result *FetchResult) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Content from %s:\n\n", result.Data.Metadata.SourceURL))
	if result.Data.Metadata.Title != "" {
		b.WriteString(fmt.Sprintf("Title: %s\n\n", result.Data.Metadata.Title))
	}
	b.WriteString(Truncate(result.Data.Markdown, 8000))
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

// collectStream reads all events from a stream into a slice.
func collectStream(stream anthropic.Stream) ([]anthropic.StreamEvent, error) {
	var events []anthropic.StreamEvent
	for {
		event, err := stream.Next()
		if err != nil {
			if err == io.EOF {
				break
			}
			return events, err
		}
		events = append(events, event)
	}
	return events, nil
}

// streamContentBlock accumulates a content block from SSE events.
type streamContentBlock struct {
	block     anthropic.ContentBlock
	input     strings.Builder
	text      strings.Builder
	thinking  strings.Builder
	signature strings.Builder
}

// collectToolUsesFromEvents extracts tool_use blocks from stream events,
// assembling the Input field from content_block_delta partial_json fragments.
func collectToolUsesFromEvents(events []anthropic.StreamEvent) []anthropic.ContentBlock {
	blockMap := make(map[int]*streamContentBlock)
	order := make([]int, 0)

	for _, event := range events {
		switch event.Type {
		case "content_block_start":
			if event.ContentBlock == nil || event.ContentBlock.Type != "tool_use" {
				continue
			}
			if _, exists := blockMap[event.Index]; !exists {
				order = append(order, event.Index)
			}
			block := *event.ContentBlock
			block.Input = nil // Will be assembled from deltas
			blockMap[event.Index] = &streamContentBlock{block: block}

		case "content_block_delta":
			if event.Delta.Type != "input_json_delta" || event.Delta.PartialJSON == "" {
				continue
			}
			if current := blockMap[event.Index]; current != nil {
				current.input.WriteString(event.Delta.PartialJSON)
			}
		}
	}

	toolUses := make([]anthropic.ContentBlock, 0, len(order))
	for _, idx := range order {
		current := blockMap[idx]
		if current == nil {
			continue
		}
		block := current.block
		if current.input.Len() > 0 {
			block.Input = json.RawMessage(current.input.String())
		}
		toolUses = append(toolUses, block)
	}
	return toolUses
}

// collectContentFromEvents reconstructs all content blocks from stream events,
// assembling text from text_delta, thinking/signature from thinking_delta/signature_delta,
// and input from input_json_delta.
func collectContentFromEvents(events []anthropic.StreamEvent) []anthropic.ContentBlock {
	blockMap := make(map[int]*streamContentBlock)
	order := make([]int, 0)

	for _, event := range events {
		switch event.Type {
		case "content_block_start":
			if event.ContentBlock == nil {
				continue
			}
			if _, exists := blockMap[event.Index]; !exists {
				order = append(order, event.Index)
			}
			block := *event.ContentBlock
			// Only clear fields for types we can reconstruct from deltas.
			switch block.Type {
			case "tool_use":
				block.Input = nil
			case "text":
				block.Text = ""
			case "thinking":
				block.Thinking = ""
				block.Signature = ""
			}
			blockMap[event.Index] = &streamContentBlock{block: block}

		case "content_block_delta":
			current := blockMap[event.Index]
			if current == nil {
				continue
			}
			if event.Delta.Text != "" {
				current.text.WriteString(event.Delta.Text)
			}
			if event.Delta.Thinking != "" {
				current.thinking.WriteString(event.Delta.Thinking)
			}
			if event.Delta.Signature != "" {
				current.signature.WriteString(event.Delta.Signature)
			}
			if event.Delta.Type == "input_json_delta" && event.Delta.PartialJSON != "" {
				current.input.WriteString(event.Delta.PartialJSON)
			}
		}
	}

	blocks := make([]anthropic.ContentBlock, 0, len(order))
	for _, idx := range order {
		current := blockMap[idx]
		if current == nil {
			continue
		}
		block := current.block
		switch block.Type {
		case "tool_use":
			if current.input.Len() > 0 {
				block.Input = json.RawMessage(current.input.String())
			}
		case "text":
			if current.text.Len() > 0 {
				block.Text = current.text.String()
			}
		case "thinking":
			if current.thinking.Len() > 0 {
				block.Thinking = current.thinking.String()
			}
			if current.signature.Len() > 0 {
				block.Signature = current.signature.String()
			}
		}
		blocks = append(blocks, block)
	}
	return blocks
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
		return anthropic.StreamEvent{}, io.EOF
	}
	event := s.events[s.pos]
	s.pos++
	return event, nil
}

func (s *staticStream) Close() error {
	return nil
}

func Truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func escapeForJSON(s string) string {
	// Use json.Marshal for proper Unicode/control character escaping.
	// The go standard library handles all escaping rules according to RFC 8259.
	encoded, err := json.Marshal(s)
	if err != nil {
		return s
	}
	// json.Marshal returns a quoted JSON string. Strip the surrounding quotes
	// for embedding in the tool_result content template.
	raw := string(encoded)
	if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' {
		return raw[1 : len(raw)-1]
	}
	return raw
}
