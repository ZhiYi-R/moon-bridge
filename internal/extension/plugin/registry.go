package plugin

import (
	"encoding/json"
	"fmt"
	"log/slog"

	"moonbridge/internal/foundation/config"
	"moonbridge/internal/foundation/logger"
	"moonbridge/internal/foundation/openai"
	"moonbridge/internal/protocol/anthropic"
)

// Registry holds registered plugins and dispatches to their capabilities.
// Capability lists are populated at registration time via type assertions.
type Registry struct {
	plugins            []Plugin
	inputPreprocessors []InputPreprocessor
	requestMutators    []RequestMutator
	toolInjectors      []ToolInjector
	messageRewriters   []MessageRewriter
	providerWrappers   []ProviderWrapper
	contentFilters     []ContentFilter
	responsePostProcs  []ResponsePostProcessor
	contentRememberers []ContentRememberer
	streamInterceptors []StreamInterceptor
	errorTransformers  []ErrorTransformer
	sessionProviders   []SessionStateProvider
	logConsumers       []LogConsumer
	logger             *slog.Logger
}

// NewRegistry creates an empty plugin registry.
func NewRegistry(logger *slog.Logger) *Registry {
	if logger == nil {
		logger = slog.Default()
	}
	return &Registry{logger: logger}
}

// Register adds a plugin and detects its capabilities.
func (r *Registry) Register(p Plugin) {
	r.plugins = append(r.plugins, p)
	if ctp, ok := p.(ConfigTypeProvider); ok {
		config.RegisterPluginConfigType(p.Name(), ctp.ConfigType)
	}
	if v, ok := p.(InputPreprocessor); ok {
		r.inputPreprocessors = append(r.inputPreprocessors, v)
	}
	if v, ok := p.(RequestMutator); ok {
		r.requestMutators = append(r.requestMutators, v)
	}
	if v, ok := p.(ToolInjector); ok {
		r.toolInjectors = append(r.toolInjectors, v)
	}
	if v, ok := p.(MessageRewriter); ok {
		r.messageRewriters = append(r.messageRewriters, v)
	}
	if v, ok := p.(ProviderWrapper); ok {
		r.providerWrappers = append(r.providerWrappers, v)
	}
	if v, ok := p.(ContentFilter); ok {
		r.contentFilters = append(r.contentFilters, v)
	}
	if v, ok := p.(ResponsePostProcessor); ok {
		r.responsePostProcs = append(r.responsePostProcs, v)
	}
	if v, ok := p.(ContentRememberer); ok {
		r.contentRememberers = append(r.contentRememberers, v)
	}
	if v, ok := p.(StreamInterceptor); ok {
		r.streamInterceptors = append(r.streamInterceptors, v)
	}
	if v, ok := p.(ErrorTransformer); ok {
		r.errorTransformers = append(r.errorTransformers, v)
	}
	if v, ok := p.(SessionStateProvider); ok {
		r.sessionProviders = append(r.sessionProviders, v)
	}
	if v, ok := p.(LogConsumer); ok {
		r.logConsumers = append(r.logConsumers, v)
	}
}

// InitAll calls Init on all registered plugins.
func (r *Registry) InitAll(appCfg interface {
	PluginConfig(name string) map[string]any
}) error {
	for _, p := range r.plugins {
		var pluginCfg map[string]any
		if appCfg != nil {
			pluginCfg = appCfg.PluginConfig(p.Name())
		}
		ctx := PluginContext{
			Config: config.DecodePluginConfig(p.Name(), pluginCfg),
			Logger: r.logger.With("plugin", p.Name()),
		}
		if err := p.Init(ctx); err != nil {
			return fmt.Errorf("plugin %s init failed: %w", p.Name(), err)
		}
		r.logger.Info("插件已初始化", "name", p.Name())
	}
	return nil
}

// ShutdownAll calls Shutdown on all registered plugins in reverse order.
func (r *Registry) ShutdownAll() {
	for i := len(r.plugins) - 1; i >= 0; i-- {
		if err := r.plugins[i].Shutdown(); err != nil {
			r.logger.Warn("插件关闭出错", "name", r.plugins[i].Name(), "error", err)
		}
	}
}

// --- Dispatch methods ---

// PreprocessInput chains InputPreprocessor across enabled plugins.
func (r *Registry) PreprocessInput(model string, raw json.RawMessage) json.RawMessage {
	if r == nil {
		return raw
	}
	for _, p := range r.inputPreprocessors {
		if p.(Plugin).EnabledForModel(model) {
			raw = p.PreprocessInput(&RequestContext{ModelAlias: model}, raw)
		}
	}
	return raw
}

// MutateRequest chains RequestMutator across enabled plugins.
func (r *Registry) MutateRequest(ctx *RequestContext, req *anthropic.MessageRequest) {
	if r == nil {
		return
	}
	for _, p := range r.requestMutators {
		if p.(Plugin).EnabledForModel(ctx.ModelAlias) {
			p.MutateRequest(ctx, req)
		}
	}
}

// InjectTools collects tools from all enabled ToolInjectors.
func (r *Registry) InjectTools(ctx *RequestContext) []anthropic.Tool {
	if r == nil {
		return nil
	}
	var tools []anthropic.Tool
	for _, p := range r.toolInjectors {
		if p.(Plugin).EnabledForModel(ctx.ModelAlias) {
			tools = append(tools, p.InjectTools(ctx)...)
		}
	}
	return tools
}

// RewriteMessages chains MessageRewriter across enabled plugins.
func (r *Registry) RewriteMessages(ctx *RequestContext, messages []anthropic.Message) []anthropic.Message {
	if r == nil {
		return messages
	}
	for _, p := range r.messageRewriters {
		if p.(Plugin).EnabledForModel(ctx.ModelAlias) {
			messages = p.RewriteMessages(ctx, messages)
		}
	}
	return messages
}

// WrapProvider chains ProviderWrapper across enabled plugins.
func (r *Registry) WrapProvider(ctx *RequestContext, provider any) any {
	if r == nil {
		return provider
	}
	for _, p := range r.providerWrappers {
		if p.(Plugin).EnabledForModel(ctx.ModelAlias) {
			provider = p.WrapProvider(ctx, provider)
		}
	}
	return provider
}

// FilterContent calls each enabled ContentFilter. Returns skip=true if any
// filter says skip, and collects all extra output items.
func (r *Registry) FilterContent(ctx *RequestContext, block anthropic.ContentBlock) (skip bool, extra []openai.OutputItem) {
	if r == nil {
		return false, nil
	}
	for _, p := range r.contentFilters {
		if p.(Plugin).EnabledForModel(ctx.ModelAlias) {
			s, e := p.FilterContent(ctx, block)
			if s {
				skip = true
			}
			extra = append(extra, e...)
		}
	}
	return
}

// PostProcessResponse chains ResponsePostProcessor across enabled plugins.
func (r *Registry) PostProcessResponse(ctx *RequestContext, resp *openai.Response) {
	if r == nil {
		return
	}
	for _, p := range r.responsePostProcs {
		if p.(Plugin).EnabledForModel(ctx.ModelAlias) {
			p.PostProcessResponse(ctx, resp)
		}
	}
}

// RememberContent chains ContentRememberer across enabled plugins.
func (r *Registry) RememberContent(ctx *RequestContext, content []anthropic.ContentBlock) {
	if r == nil {
		return
	}
	for _, p := range r.contentRememberers {
		if p.(Plugin).EnabledForModel(ctx.ModelAlias) {
			p.RememberContent(ctx, content)
		}
	}
}

// NewStreamStates creates per-request stream state for all enabled StreamInterceptors.
func (r *Registry) NewStreamStates(model string) map[string]any {
	if r == nil {
		return nil
	}
	var states map[string]any
	for _, p := range r.streamInterceptors {
		if p.(Plugin).EnabledForModel(model) {
			if s := p.NewStreamState(); s != nil {
				if states == nil {
					states = make(map[string]any)
				}
				states[p.(Plugin).Name()] = s
			}
		}
	}
	return states
}

// OnStreamEvent dispatches to enabled StreamInterceptors.
// Returns consumed=true if any interceptor consumed the event.
func (r *Registry) OnStreamEvent(model string, event StreamEvent, streamStates map[string]any) (consumed bool, emit []openai.StreamEvent) {
	if r == nil {
		return false, nil
	}
	for _, p := range r.streamInterceptors {
		pp := p.(Plugin)
		if !pp.EnabledForModel(model) {
			continue
		}
		ctx := &StreamContext{
			RequestContext: RequestContext{ModelAlias: model},
			StreamState:    streamStates[pp.Name()],
		}
		c, e := p.OnStreamEvent(ctx, event)
		if c {
			consumed = true
		}
		emit = append(emit, e...)
	}
	return
}

// OnStreamComplete notifies all enabled StreamInterceptors.
func (r *Registry) OnStreamComplete(model string, streamStates map[string]any, outputText string, sessionData map[string]any) {
	if r == nil {
		return
	}
	for _, p := range r.streamInterceptors {
		pp := p.(Plugin)
		if !pp.EnabledForModel(model) {
			continue
		}
		ctx := &StreamContext{
			RequestContext: RequestContext{
				ModelAlias:  model,
				SessionData: sessionData,
			},
			StreamState: streamStates[pp.Name()],
		}
		p.OnStreamComplete(ctx, outputText)
	}
}

// TransformError chains ErrorTransformer across enabled plugins.
func (r *Registry) TransformError(model string, msg string) string {
	if r == nil {
		return msg
	}
	ctx := &RequestContext{ModelAlias: model}
	for _, p := range r.errorTransformers {
		if p.(Plugin).EnabledForModel(model) {
			msg = p.TransformError(ctx, msg)
		}
	}
	return msg
}

// NewSessionData creates session state for all registered plugins.
func (r *Registry) NewSessionData() map[string]any {
	if r == nil {
		return nil
	}
	var data map[string]any
	for _, p := range r.sessionProviders {
		if s := p.NewSessionState(); s != nil {
			if data == nil {
				data = make(map[string]any)
			}
			data[p.(Plugin).Name()] = s
		}
	}
	return data
}

// ConsumeLog dispatches to all enabled LogConsumer plugins.
// Returns the modified entries, or the original if no consumers.
func (r *Registry) ConsumeLog(ctx *RequestContext, entries []logger.LogEntry) []logger.LogEntry {
	if r == nil || len(r.logConsumers) == 0 {
		return entries
	}
	result := entries
	for _, p := range r.logConsumers {
		if p.(Plugin).EnabledForModel(ctx.ModelAlias) {
			result = p.ConsumeLog(ctx, result)
		}
	}
	return result
}

// HasEnabled reports whether any plugin is enabled for the given model.
func (r *Registry) HasEnabled(model string) bool {
	if r == nil {
		return false
	}
	for _, p := range r.plugins {
		if p.EnabledForModel(model) {
			return true
		}
	}
	return false
}
