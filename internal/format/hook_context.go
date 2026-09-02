package format

import "context"

// hookSessionDataKey is the context key used to carry per-session plugin
// state into Core plugin hooks. Hooks are built once at startup, so their
// per-request inputs must flow through the context.
type hookSessionDataKey struct{}

// WithPluginSessionData returns a context that carries the given per-session
// plugin state map (keyed by plugin name). Core hooks read it back via
// PluginSessionDataFromContext to recover each plugin's session state.
func WithPluginSessionData(ctx context.Context, data map[string]any) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, hookSessionDataKey{}, data)
}

// PluginSessionDataFromContext returns the per-session plugin state map
// carried by the context, or nil when none is present.
func PluginSessionDataFromContext(ctx context.Context) map[string]any {
	if ctx == nil {
		return nil
	}
	data, _ := ctx.Value(hookSessionDataKey{}).(map[string]any)
	return data
}
