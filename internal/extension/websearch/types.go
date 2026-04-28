package websearch

// SearchRequest is the request to the Tavily search API.
type SearchRequest struct {
	Query          string   `json:"query"`
	SearchDepth    string   `json:"search_depth,omitempty"`
	Topic          string   `json:"topic,omitempty"`
	MaxResults     int      `json:"max_results,omitempty"`
	TimeRange      string   `json:"time_range,omitempty"`
	IncludeDomains []string `json:"include_domains,omitempty"`
	ExcludeDomains []string `json:"exclude_domains,omitempty"`
	IncludeAnswer  bool     `json:"include_answer,omitempty"`
	IncludeRaw     bool     `json:"include_raw_content,omitempty"`
}

// SearchResult is the response from the Tavily search API.
type SearchResult struct {
	Query        string       `json:"query"`
	Results      []SearchItem `json:"results"`
	Answer       string       `json:"answer,omitempty"`
	ResponseTime float64      `json:"response_time"`
	RequestID    string       `json:"request_id"`
}

// SearchItem is a single result item from Tavily.
type SearchItem struct {
	Title      string  `json:"title"`
	URL        string  `json:"url"`
	Content    string  `json:"content"`
	Score      float64 `json:"score"`
	RawContent string  `json:"raw_content,omitempty"`
}

// FetchRequest is the request to the Firecrawl scrape API.
type FetchRequest struct {
	URL             string   `json:"url"`
	Formats         []string `json:"formats,omitempty"`
	OnlyMainContent bool     `json:"onlyMainContent,omitempty"`
	Timeout         int      `json:"timeout,omitempty"`
}

// FetchResult is the response from the Firecrawl scrape API.
type FetchResult struct {
	Success bool      `json:"success"`
	Data    FetchData `json:"data"`
}

// FetchData is the extracted page content from Firecrawl.
type FetchData struct {
	Markdown string        `json:"markdown,omitempty"`
	Metadata FetchMetadata `json:"metadata"`
}

// FetchMetadata contains metadata about the scraped page.
type FetchMetadata struct {
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	SourceURL   string `json:"sourceURL,omitempty"`
	StatusCode  int    `json:"statusCode"`
}

// SearchError represents an error from the Tavily or Firecrawl API.
type SearchError struct {
	StatusCode int
	Message    string
}

func (e *SearchError) Error() string {
	return e.Message
}
