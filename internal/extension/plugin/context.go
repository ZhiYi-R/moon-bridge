package plugin

// RequestContext carries per-request information for plugin calls.
type RequestContext struct {
	ModelAlias  string
	SessionData map[string]any // per-session plugin state, keyed by plugin name
	Reasoning   map[string]any // OpenAI reasoning config from the request
	WebSearch   WebSearchInfo  // resolved web search config for this request
}

// WebSearchInfo carries resolved web search parameters.
type WebSearchInfo struct {
	Mode         string // "enabled", "disabled", "injected", ""
	MaxUses      int
	FirecrawlKey string
}

// SessionState returns the session state for a specific plugin.
func (ctx *RequestContext) SessionState(pluginName string) any {
	if ctx.SessionData == nil {
		return nil
	}
	return ctx.SessionData[pluginName]
}

// StreamContext extends RequestContext with streaming state.
type StreamContext struct {
	RequestContext
	StreamState any // the plugin's per-stream state from NewStreamState
}
