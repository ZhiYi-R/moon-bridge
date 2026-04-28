package websearch

import (
	"moonbridge/internal/protocol/anthropic"
)

// ToolOptions configures web search tool generation.
type ToolOptions struct {
	// Mode is "enabled", "injected", or "disabled".
	Mode string
	// MaxUses overrides the default max uses for native web_search.
	MaxUses int
	// DefaultMaxUses is the fallback when MaxUses <= 0.
	DefaultMaxUses int
	// FirecrawlAPIKey enables the firecrawl_fetch injected tool when non-empty.
	FirecrawlAPIKey string
}

// Tools generates Anthropic tool definitions for web search based on the mode.
// Returns nil for disabled mode.
func Tools(opts ToolOptions) []anthropic.Tool {
	switch opts.Mode {
	case "injected":
		return InjectedTools(opts.FirecrawlAPIKey)
	case "enabled":
		maxUses := opts.MaxUses
		if maxUses <= 0 {
			maxUses = opts.DefaultMaxUses
		}
		if maxUses <= 0 {
			maxUses = 8
		}
		return []anthropic.Tool{{
			Name:    "web_search",
			Type:    "web_search_20250305",
			MaxUses: maxUses,
		}}
	default:
		return nil
	}
}

// InjectedTools returns function-type tools for injected web search.
// If firecrawlKey is empty, only tavily_search is returned.
func InjectedTools(firecrawlKey string) []anthropic.Tool {
	tools := []anthropic.Tool{
		{
			Name:        "tavily_search",
			Description: "Search the web using Tavily. Returns search results with titles, URLs, and content snippets. Call this when you need up-to-date information from the internet.",
			InputSchema: tavilySearchSchema(),
		},
	}
	if firecrawlKey != "" {
		tools = append(tools, anthropic.Tool{
			Name:        "firecrawl_fetch",
			Description: "Fetch and extract the full content of a web page as clean markdown using Firecrawl. Use this when you need the complete text of a specific URL, such as a blog post or documentation page.",
			InputSchema: firecrawlFetchSchema(),
		})
	}
	return tools
}

func tavilySearchSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "Search query (required).",
			},
			"search_depth": map[string]any{
				"type":        "string",
				"enum":        []string{"basic", "advanced"},
				"description": "Depth of search. Basic is faster; advanced provides more comprehensive results.",
			},
			"max_results": map[string]any{
				"type":        "integer",
				"description": "Maximum number of results to return (1-20).",
				"default":     5,
			},
			"topic": map[string]any{
				"type":        "string",
				"enum":        []string{"general", "news", "finance"},
				"description": "Search topic category.",
			},
			"time_range": map[string]any{
				"type":        "string",
				"enum":        []string{"day", "week", "month", "year"},
				"description": "Time range filter for results.",
			},
			"include_answer": map[string]any{
				"type":        "boolean",
				"description": "Include an AI-generated answer summarizing the search results.",
			},
			"include_domains": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Only include results from these domains.",
			},
			"exclude_domains": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Exclude results from these domains.",
			},
		},
		"required": []string{"query"},
	}
}

func firecrawlFetchSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"url": map[string]any{
				"type":        "string",
				"description": "URL of the web page to fetch.",
			},
		},
		"required": []string{"url"},
	}
}
