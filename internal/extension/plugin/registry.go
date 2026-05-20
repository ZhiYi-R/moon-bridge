package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"moonbridge/internal/config"
	"moonbridge/internal/format"
	"moonbridge/internal/logger"
	"moonbridge/internal/protocol/openai"
)

// Registry holds registered plugins and dispatches to their capabilities.
// Capability lists are populated at registration time via type assertions.
type Registry struct {
	plugins                []Plugin
	inputPreprocessors     []InputPreprocessor
	requestMutators        []RequestMutator
	toolInjectors          []ToolInjector
	messageRewriters       []MessageRewriter
	providerWrappers       []ProviderWrapper
	contentFilters         []ContentFilter
	responsePostProcs      []ResponsePostProcessor
	contentRememberers     []ContentRememberer
	streamInterceptors     []StreamInterceptor
	errorTransformers      []ErrorTransformer
	sessionProviders       []SessionStateProvider
	logConsumers           []LogConsumer
	dbProviders            []DBProvider
	dbConsumers            []DBConsumer
	requestCompletionHooks []RequestCompletionHook
	routeRegistrars        []RouteRegistrar
	configSpecs            []config.ExtensionConfigSpec
	logger                 *slog.Logger
	currentConfig          func() config.Config
}

// NewRegistry creates an empty plugin registry.
func NewRegistry(logger *slog.Logger) *Registry {
	if logger == nil {
		logger = slog.Default()
	}
	return &Registry{logger: logger}
}

// SetCurrentConfigProvider installs a callback used by plugins that need the
// latest resolved configuration at request time.
func (r *Registry) SetCurrentConfigProvider(provider func() config.Config) {
	if r == nil {
		return
	}
	r.currentConfig = provider
}

// Register adds a plugin and detects its capabilities.
func (r *Registry) Register(p Plugin) {
	r.plugins = append(r.plugins, p)
	if csp, ok := p.(ConfigSpecProvider); ok {
		specs := csp.ConfigSpecs()
		r.configSpecs = append(r.configSpecs, specs...)
		for _, spec := range specs {
			if spec.Factory != nil {
				config.RegisterPluginConfigType(spec.Name, spec.Factory)
			}
		}
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
	if v, ok := p.(DBProvider); ok {
		r.dbProviders = append(r.dbProviders, v)
	}
	if v, ok := p.(DBConsumer); ok {
		r.dbConsumers = append(r.dbConsumers, v)
	}
	if v, ok := p.(RequestCompletionHook); ok {
		r.requestCompletionHooks = append(r.requestCompletionHooks, v)
	}
	if v, ok := p.(RouteRegistrar); ok {
		r.routeRegistrars = append(r.routeRegistrars, v)
	}
}

func (r *Registry) ConfigSpecs() []config.ExtensionConfigSpec {
	if r == nil || len(r.configSpecs) == 0 {
		return nil
	}
	specs := make([]config.ExtensionConfigSpec, len(r.configSpecs))
	copy(specs, r.configSpecs)
	return specs
}

// InitConfigProvider is the minimal interface InitAll needs from the app config.
type InitConfigProvider interface {
	ExtensionConfig(name string, modelAlias string) any
}

// InitAll calls Init on all registered plugins.
func (r *Registry) InitAll(appCfg InitConfigProvider) error {
	for _, p := range r.plugins {
		var appConfig config.Config
		var typedCfg any

		if appCfg != nil {
			typedCfg = appCfg.ExtensionConfig(p.Name(), "")
			if cfg, ok := any(appCfg).(*config.Config); ok && cfg != nil {
				appConfig = *cfg
			} else if cfg, ok := any(appCfg).(config.Config); ok {
				appConfig = cfg
			}
		}
		ctx := PluginContext{
			Config:        typedCfg,
			AppConfig:     appConfig,
			CurrentConfig: r.currentConfig,
			Logger:        r.logger.With("plugin", p.Name()),
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
func (r *Registry) MutateRequest(ctx *RequestContext, req *format.CoreRequest) {
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
func (r *Registry) InjectTools(ctx *RequestContext) []format.CoreTool {
	if r == nil {
		return nil
	}
	var tools []format.CoreTool
	for _, p := range r.toolInjectors {
		if p.(Plugin).EnabledForModel(ctx.ModelAlias) {
			tools = append(tools, p.InjectTools(ctx)...)
		}
	}
	return tools
}

// RewriteMessages chains MessageRewriter across enabled plugins.
func (r *Registry) RewriteMessages(ctx *RequestContext, messages []format.CoreMessage) []format.CoreMessage {
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
// filter says skip.
func (r *Registry) FilterContent(ctx *RequestContext, block format.CoreContentBlock) bool {
	if r == nil {
		return false
	}
	for _, p := range r.contentFilters {
		if p.(Plugin).EnabledForModel(ctx.ModelAlias) {
			if p.FilterContent(ctx, block) {
				return true
			}
		}
	}
	return false
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
func (r *Registry) RememberContent(ctx *RequestContext, content []format.CoreContentBlock) {
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

// ConsumeGlobalLog dispatches log entries to all registered LogConsumer
// plugins without model-alias filtering. Use for global log pipelines
// not tied to a specific request.
func (r *Registry) ConsumeGlobalLog(entries []logger.LogEntry) []logger.LogEntry {
	if r == nil || len(r.logConsumers) == 0 {
		return entries
	}
	result := entries
	for _, p := range r.logConsumers {
		result = p.ConsumeLog(&RequestContext{}, result)
	}
	return result
}

// OnRequestCompleted notifies all enabled RequestCompletionHook plugins.
func (r *Registry) OnRequestCompleted(ctx *RequestContext, result RequestResult) {
	if r == nil {
		return
	}
	// Use the model alias from the result when ctx is nil.
	model := result.Model
	if ctx != nil {
		model = ctx.ModelAlias
	}
	for _, p := range r.requestCompletionHooks {
		if p.(Plugin).EnabledForModel(model) {
			p.OnRequestCompleted(ctx, result)
		}
	}
}

// RegisterRoutes calls RegisterRoutes on all RouteRegistrar plugins, giving
// each plugin the opportunity to mount HTTP handlers via the provided register function.
func (r *Registry) RegisterRoutes(register func(pattern string, handler http.Handler)) {
	if r == nil {
		return
	}
	for _, p := range r.routeRegistrars {
		p.RegisterRoutes(register)
	}
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

// Plugin returns the registered plugin with the given name, or nil if not found.
func (r *Registry) Plugin(name string) Plugin {
	if r == nil {
		return nil
	}
	for _, p := range r.plugins {
		if p.Name() == name {
			return p
		}
	}
	return nil
}

// DBProviders returns the list of enabled DB provider plugins (nil-filtered).
func (r *Registry) DBProviders() []DBProvider {
	if r == nil {
		return nil
	}
	var result []DBProvider
	for _, p := range r.dbProviders {
		if prov := p.DBProvider(); prov != nil {
			result = append(result, p)
		}
	}
	return result
}

// DBConsumers returns the list of enabled DB consumer plugins (nil-filtered).
func (r *Registry) DBConsumers() []DBConsumer {
	if r == nil {
		return nil
	}
	var result []DBConsumer
	for _, p := range r.dbConsumers {
		if c := p.DBConsumer(); c != nil {
			result = append(result, p)
		}
	}
	return result
}

// Plugins returns the names of all registered plugins.
func (r *Registry) Plugins() []string {
	if r == nil {
		return nil
	}
	names := make([]string, 0, len(r.plugins))
	for _, p := range r.plugins {
		names = append(names, p.Name())
	}
	return names
}

// CorePluginHooks builds a format.CorePluginHooks from all registered plugins.
// Each hook chains all plugins that implement the corresponding capability.
func (r *Registry) CorePluginHooks() format.CorePluginHooks {
	hooks := format.CorePluginHooks{}.WithDefaults()
	for _, p := range r.plugins {
		// MutateCoreRequest
		if m, ok := p.(CoreRequestMutator); ok {
			prev := hooks.MutateCoreRequest
			pluginImpl := p
			hooks.MutateCoreRequest = func(ctx context.Context, req *format.CoreRequest) {
				if prev != nil {
					prev(ctx, req)
				}
				if req == nil || !pluginImpl.EnabledForModel(req.Model) {
					return
				}
				m.MutateCoreRequest(ctx, req)
			}
		}
		// FilterCoreContent
		if f, ok := p.(CoreContentFilter); ok {
			prev := hooks.FilterContent
			pluginImpl := p
			hooks.FilterContent = func(ctx context.Context, block *format.CoreContentBlock) bool {
				if prev != nil && prev(ctx, block) {
					return true
				}
				model := format.ModelAliasFromCoreHookContext(ctx)
				if model == "" || !pluginImpl.EnabledForModel(model) {
					return false
				}
				return f.FilterCoreContent(ctx, block)
			}
		}
		// RewriteMessages — only chain plugins enabled for this model.
		if mw, ok := p.(MessageRewriter); ok {
			prev := hooks.RewriteMessages
			pluginImpl := p
			hooks.RewriteMessages = func(ctx context.Context, req *format.CoreRequest) {
				if prev != nil {
					prev(ctx, req)
				}
				if req == nil {
					return
				}
				pluginCtx := &RequestContext{ModelAlias: req.Model}
				if pluginImpl.EnabledForModel(req.Model) {
					req.Messages = mw.RewriteMessages(pluginCtx, req.Messages)
				}
			}
		}
		// InjectTools — only chain plugins enabled for this model.
		if ti, ok := p.(ToolInjector); ok {
			prev := hooks.InjectTools
			plugin := p.(Plugin) // for EnabledForModel check
			hooks.InjectTools = func(ctx context.Context) []format.CoreTool {
				tools := prev(ctx)
				modelAlias := ""
				if req, ok := format.CoreRequestFromContext(ctx); ok && req != nil {
					modelAlias = req.Model
				}
				if plugin.EnabledForModel(modelAlias) {
					tools = append(tools, ti.InjectTools(&RequestContext{ModelAlias: modelAlias})...)
				}
				return tools
			}
		}
		// RememberCoreContent
		if rmem, ok := p.(CoreContentRememberer); ok {
			prev := hooks.RememberContent
			pluginImpl := p
			hooks.RememberContent = func(ctx context.Context, content []format.CoreContentBlock) {
				if prev != nil {
					prev(ctx, content)
				}
				model := format.ModelAliasFromCoreHookContext(ctx)
				if model == "" || !pluginImpl.EnabledForModel(model) {
					return
				}
				rmem.RememberCoreContent(ctx, content)
			}
		}
	}

	// Wire codex_tool_proxy PatchProxyDecider into DisablePatchProxy hook.
	if ppd, ok := r.Plugin("codex_tool_proxy").(PatchProxyDecider); ok {
		hooks.DisablePatchProxy = ppd.DisablePatchProxy
	}

	return hooks
}
