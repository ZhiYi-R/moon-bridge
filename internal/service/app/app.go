package app

import (
	"context"
	stderrors "errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"sync"
	"time"

	"log/slog"
	"moonbridge/internal/config"
	"moonbridge/internal/db"
	"moonbridge/internal/extension/codextool"
	"moonbridge/internal/format"
	"moonbridge/internal/logger"
	"moonbridge/internal/protocol/anthropic"
	"moonbridge/internal/protocol/cache"
	"moonbridge/internal/protocol/chat"
	"moonbridge/internal/protocol/google"
	"moonbridge/internal/protocol/openai"
	"moonbridge/internal/service/provider"
	"moonbridge/internal/service/proxy"
	"moonbridge/internal/service/runtime"
	"moonbridge/internal/service/server"
	"moonbridge/internal/service/server/session"
	"moonbridge/internal/service/server/trace"
	"moonbridge/internal/service/server/usage"
	"moonbridge/internal/service/stats"
	"moonbridge/internal/service/store"
	mbtrace "moonbridge/internal/service/trace"
)

const Name = "Moon Bridge"

func Run(output io.Writer) {
	fmt.Fprintln(output, WelcomeMessage())
}

func WelcomeMessage() string {
	return "欢迎使用 " + Name + "!"
}

func RunServer(ctx context.Context, cfg config.Config, errors io.Writer) error {
	switch cfg.Mode {
	case config.ModeTransform:
		slog.Info("启动服务器", "mode", cfg.Mode, "addr", cfg.Addr)
		return runTransform(ctx, cfg, errors)
	case config.ModeCaptureResponse:
		slog.Info("启动服务器", "mode", cfg.Mode, "addr", cfg.Addr)
		return runCaptureResponse(ctx, cfg, errors)
	case config.ModeCaptureAnthropic:
		slog.Info("启动服务器", "mode", cfg.Mode, "addr", cfg.Addr)
		return runCaptureAnthropic(ctx, cfg, errors)
	default:
		return fmt.Errorf("unsupported mode %q", cfg.Mode)
	}
}

func runTransform(ctx context.Context, cfg config.Config, errors io.Writer) error {
	var rt *runtime.Runtime

	// Construct domain configs from global config.
	serverCfg := config.ServerFromGlobalConfig(&cfg)
	cacheCfg := config.CacheFromGlobalConfig(&cfg)
	proxyCfg := config.ProxyFromGlobalConfig(&cfg)
	storeCfg := config.StoreFromGlobalConfig(&cfg)
	persistCfg := config.PersistenceFromGlobalConfig(&cfg)
	providerCfg := config.ProviderFromGlobalConfig(&cfg)
	_ = persistCfg // used in db init
	_ = storeCfg   // used in config store
	_ = proxyCfg   // used in proxy mode

	// === Phase 1: Bootstrap from YAML ===

	// Build multi-provider infrastructure from YAML config.
	providerDefs := provider.BuildProviderDefsFromConfig(providerCfg)
	modelRoutes := provider.BuildModelRoutesFromConfig(providerCfg)
	// Build a shared proxy-aware HTTP client when egress proxy is configured.
	var proxyHTTPClient *http.Client
	if cfg.EgressProxy != "" {
		proxyURL, err := url.Parse(cfg.EgressProxy)
		if err != nil {
			return fmt.Errorf("invalid egress_proxy URL %q: %w", cfg.EgressProxy, err)
		}
		transport, ok := http.DefaultTransport.(*http.Transport)
		if !ok {
			transport = &http.Transport{}
		} else {
			transport = transport.Clone()
		}
		transport.Proxy = http.ProxyURL(proxyURL)
		proxyHTTPClient = &http.Client{Transport: transport}
		slog.Info("egress proxy enabled", "url", cfg.EgressProxy)
	}

	// Inject proxy client into provider configs before building provider manager.
	if proxyHTTPClient != nil {
		for key := range providerDefs {
			def := providerDefs[key]
			def.ClientOverride = proxyHTTPClient
			providerDefs[key] = def
		}
	}

	providerMgr, err := provider.NewProviderManager(providerDefs, modelRoutes)
	if err != nil {
		return fmt.Errorf("init provider manager: %w", err)
	}

	// Resolve a fallback client for web search probing and server fallback.
	defaultClient := resolveDefaultClient(providerMgr, errors)
	resolvePerProviderWebSearch(ctx, cfg, providerMgr)

	sessionStats := stats.NewSessionStats()
	pricing := provider.BuildPricingFromConfig(providerCfg)
	if len(pricing) > 0 {
		sessionStats.SetPricing(pricing)
	}

	tracer := mbtrace.New(mbtrace.Config{
		Enabled: cfg.TraceRequests,
		Root:    transformTraceRoot(),
	})
	logTrace(errors, "transform", tracer)

	// Determine the default provider to use as the fallback Provider.
	var fallbackProvider provider.ProviderClient
	if defaultClient != nil {
		fallbackProvider = provider.NewAnthropicClientAdapter(defaultClient)
	}

	// Register plugins.
	plugins := BuiltinExtensions().NewRegistry(slog.Default(), cfg)
	plugins.SetCurrentConfigProvider(func() config.Config {
		if rt != nil && rt.Current() != nil {
			return rt.Current().Config
		}
		return cfg
	})
	if err := plugins.InitAll(&cfg); err != nil {
		return fmt.Errorf("init plugins: %w", err)
	}
	defer plugins.ShutdownAll()

	// Wire plugin LogConsumer into the slog consume pipeline.
	logger.AddConsumeFunc(func(entries []logger.LogEntry) []logger.LogEntry {
		return plugins.ConsumeGlobalLog(entries)
	})

	// Initialize persistence layer (db.Registry).
	dbRegistry := db.NewRegistry(slog.Default())
	dbProviders := plugins.DBProviders()
	providers := make([]db.Provider, 0, len(dbProviders))
	for _, p := range dbProviders {
		if prov := p.DBProvider(); prov != nil {
			dbRegistry.RegisterProvider(prov)
			providers = append(providers, prov)
		}
	}
	for _, c := range plugins.DBConsumers() {
		if cons := c.DBConsumer(); cons != nil {
			dbRegistry.RegisterConsumer(cons)
		}
	}
	// Register the config_store consumer for configuration persistence.
	configStoreConsumer := store.NewConfigStoreConsumer(logger.L())
	configStoreConsumer.SetExtensionSpecs(BuiltinExtensions().ConfigSpecs())
	dbRegistry.RegisterConsumer(configStoreConsumer)
	activePersistenceProvider := ResolvePersistenceActiveProvider(cfg.Persistence.ActiveProvider, providers)
	if err := dbRegistry.Init(ctx, activePersistenceProvider); err != nil {
		return fmt.Errorf("init persistence: %w", err)
	}
	defer dbRegistry.Shutdown()

	// === Phase 2: ConfigStore bootstrap ===
	// Check if the store is available and has existing data.
	cs := configStoreConsumer.Store()
	if cs != nil {
		if dbCfg, loadErr := cs.LoadAll(); loadErr == nil {
			if len(dbCfg.ProviderDefs) > 0 || len(dbCfg.Routes) > 0 {
				// DB has existing configuration: use it as the active config.
				logger.Info("从持久化存储加载配置",
					"providers", len(dbCfg.ProviderDefs),
					"routes", len(dbCfg.Routes))
				cfg = *dbCfg
				dbProviderCfg := config.ProviderFromGlobalConfig(&cfg)

				// Rebuild provider manager and pricing from DB-loaded config.
				providerDefs = provider.BuildProviderDefsFromConfig(dbProviderCfg)
				modelRoutes = provider.BuildModelRoutesFromConfig(dbProviderCfg)
				// Inject proxy client before rebuilding provider manager.
				if proxyHTTPClient != nil {
					for key := range providerDefs {
						def := providerDefs[key]
						def.ClientOverride = proxyHTTPClient
						providerDefs[key] = def
					}
				}
				providerMgr, err = provider.NewProviderManager(providerDefs, modelRoutes)

				if err != nil {
					return fmt.Errorf("rebuild provider manager from DB: %w", err)
				}
				_ = resolveDefaultClient(providerMgr, errors)
				resolvePerProviderWebSearch(ctx, cfg, providerMgr)

				pricing = provider.BuildPricingFromConfig(dbProviderCfg)
				if len(pricing) > 0 {
					sessionStats.SetPricing(pricing)
				}
				serverCfg = config.ServerFromGlobalConfig(&cfg)
			} else {
				// DB is empty: seed from YAML config.
				logger.Info("持久化存储为空，从 YAML 导入种子配置")
				if err := cs.SeedFromConfig(&cfg); err != nil {
					logger.Warn("config store 种子导入失败", "error", err)
				}
			}
		} else if loadErr != nil {
			if stderrors.Is(loadErr, store.ErrConfigNotSeeded) {
				logger.Info("持久化存储为空，从 YAML 导入种子配置")
				if err := cs.SeedFromConfig(&cfg); err != nil {
					return fmt.Errorf("seed config store from YAML: %w", err)
				}
			} else {
				logger.Warn("config store 加载失败", "error", loadErr)
			}
		}
	} else {
		logger.Warn("config store 不可用，跳过持久化引导")
	}

	// === Phase 3: Build Runtime ===
	rt = runtime.NewRuntime(cfg, providerMgr, pricing)

	// === Phase 4: Build Server with Runtime ===
	// Create shared cache registry (used by both Bridge and Adapter paths).
	cacheReg := cache.NewMemoryRegistry()

	// Optionally create the experimental adapter registry.
	// Create the adapter registry for Core format dispatch.
	adapterReg := format.NewRegistry()
	coreHooks := plugins.CorePluginHooks()

	// Inbound: OpenAI Responses client adapter.
	oaiAdapter := openai.NewOpenAIAdapter(coreHooks, codextool.NestedOneOf)
	_ = adapterReg.RegisterClient(oaiAdapter)
	_ = adapterReg.RegisterClientStream(oaiAdapter)

	// Upstream: Anthropic provider adapter with cache manager.
	cacheMgr := anthropic.NewCacheManager(&cfg.Cache, cacheReg)
	anthAdapter := anthropic.NewAnthropicProviderAdapter(cfg.DefaultMaxTokens, cacheMgr, coreHooks)
	_ = adapterReg.RegisterProvider(anthAdapter)
	_ = adapterReg.RegisterProviderStream(anthAdapter)

	// Upstream: Google GenAI provider adapter.
	googleCfg := &cache.PlanCacheConfig{
		Mode:                     cacheCfg.Mode,
		TTL:                      cacheCfg.TTL,
		PromptCaching:            cacheCfg.PromptCaching,
		AutomaticPromptCache:     cacheCfg.AutomaticPromptCache,
		ExplicitCacheBreakpoints: cacheCfg.ExplicitCacheBreakpoints,
		AllowRetentionDowngrade:  cacheCfg.AllowRetentionDowngrade,
		MaxBreakpoints:           cacheCfg.MaxBreakpoints,
		MinCacheTokens:           cacheCfg.MinCacheTokens,
		ExpectedReuse:            cacheCfg.ExpectedReuse,
		MinimumValueScore:        cacheCfg.MinimumValueScore,
		MinBreakpointTokens:      cacheCfg.MinBreakpointTokens,
	}
	googleAdapter := google.NewGeminiProviderAdapter(cfg.DefaultMaxTokens, nil, coreHooks, googleCfg, cacheReg)
	_ = adapterReg.RegisterProvider(googleAdapter)
	_ = adapterReg.RegisterProviderStream(googleAdapter)

	// Upstream: OpenAI Chat provider adapter.
	chatAdapter := chat.NewChatProviderAdapter(cfg.DefaultMaxTokens, nil, coreHooks)
	_ = adapterReg.RegisterProvider(chatAdapter)
	_ = adapterReg.RegisterProviderStream(chatAdapter)

	slog.Info("Adapter dispatch path enabled", "registry", "format.Registry")

	chatClients := make(map[string]any, len(cfg.ProviderDefs))
	googleClients := make(map[string]any, len(cfg.ProviderDefs))
	for key, def := range cfg.ProviderDefs {
		switch def.Protocol {
		case config.ProtocolOpenAIChat:
			chatClients[key] = chat.NewClient(chat.ClientConfig{
				BaseURL:   def.BaseURL,
				APIKey:    def.APIKey,
				Client:    proxyHTTPClient,
				UserAgent: def.UserAgent,
			})
			slog.Debug("chat client created", "provider", key)
		case config.ProtocolGoogleGenAI:
			googleClients[key] = google.NewClient(google.ClientConfig{
				BaseURL:   def.BaseURL,
				APIKey:    def.APIKey,
				Client:    proxyHTTPClient,
				Project:   def.Project,
				Location:  def.Location,
				Version:   def.APIVersion,
				UserAgent: def.UserAgent,
			})
			slog.Debug("google client created", "provider", key)
		}
	}

	// Create sub-package managers for session, usage, and trace.
	sessMgr := session.NewInMemoryManager(server.NewSessionConfigAdapterFromRuntime(rt, serverCfg), plugins)
	usageTrk := usage.NewStatsTracker(sessionStats)
	traceWtr := trace.NewFileWriter(tracer, errors)

	handler := server.New(server.Config{
		ServerCfg:        serverCfg,
		Provider:         fallbackProvider,
		ProviderMgr:      providerMgr,
		ChatClients:      chatClients,
		GoogleClients:    googleClients,
		OpenAIHTTPClient: proxyHTTPClient,
		ProxyHTTPClient:  proxyHTTPClient,
		Tracer:           tracer,
		TraceErrors:      errors,
		Stats:            sessionStats,
		PluginRegistry:   plugins,
		AppConfig:        serverCfg,
		Runtime:          rt,
		Store:            cs,
		AdapterRegistry:  adapterReg,
		SessionManager:   sessMgr,
		UsageTracker:     usageTrk,
		TraceWriter:      traceWtr,
	})

	wrapped := handler
	return runHTTPServer(ctx, cfg.Addr, wrapped, errors, sessionStats)
}

// resolveDefaultClient returns the provider client for the default key.
// Returns nil when no default provider is configured (all models use explicit routing).
func resolveDefaultClient(pm *provider.ProviderManager, errors io.Writer) *anthropic.Client {
	if pm.DefaultKey() == "" {
		slog.Warn("未配置默认提供商，跳过网页搜索探测和服务器回退")
		return nil
	}
	client, err := pm.ClientForKey(pm.DefaultKey())
	if err != nil {
		slog.Warn("默认提供商客户端不可用", "error", err)
		return nil
	}
	if acc, ok := client.(provider.AnthropicClientAccessor); ok {
		return acc.AnthropicClient()
	}
	slog.Warn("默认提供商客户端不支持访问底层客户端")
	return nil
}

type webSearchCandidateProber interface {
	ProbeWebSearchCandidate(context.Context, string, string) (bool, error)
}

// resolvePerProviderWebSearch resolves web_search support for each provider and
// each model that has a model-level override.
func resolvePerProviderWebSearch(ctx context.Context, cfg config.Config, pm *provider.ProviderManager) {
	if pm == nil {
		return
	}
	// Parallelize Anthropic probe goroutines (the slow path) while handling
	// non-probe protocol branches inline.
	var wg sync.WaitGroup
	// 1. Resolve provider-level defaults.
	for _, key := range pm.ProviderKeys() {
		protocol := pm.ProtocolForKey(key)
		support := cfg.WebSearchForProvider(key)
		switch protocol {
		case config.ProtocolAnthropic:
			switch support {
			case config.WebSearchSupportDisabled:
				pm.SetResolvedWebSearch(key, "disabled")
				slog.Info("配置禁用网页搜索", "provider", key)
			case config.WebSearchSupportEnabled:
				pm.SetResolvedWebSearch(key, "enabled")
				slog.Info("配置强制启用网页搜索", "provider", key)
			case config.WebSearchSupportInjected:
				pm.SetResolvedWebSearch(key, "injected")
				slog.Info("网页搜索注入模式已启用", "provider", key)
			default:
				// Launch probe in a goroutine to parallelize across providers.
				keyCopy := key
				wg.Add(1)
				go func() {
					defer wg.Done()
					resolved := probeProviderWebSearch(ctx, keyCopy, pm)
					if resolved == "disabled" && cfg.TavilyAPIKey != "" {
						resolved = "injected"
						slog.Info("网页搜索自动探测失败，回退到注入模式", "provider", keyCopy)
					}
					// Also write the candidate key so model-level dedup can find it.
					if upstreamModel := pm.FirstUpstreamModelForKey(keyCopy); upstreamModel != "" {
						candidateKey := provider.WebSearchCandidateKey(keyCopy, upstreamModel)
						pm.SetResolvedWebSearch(candidateKey, resolved)
					}
					pm.SetResolvedWebSearch(keyCopy, resolved)
				}()
			}
		case config.ProtocolOpenAIResponse:
			switch support {
			case config.WebSearchSupportDisabled, config.WebSearchSupportInjected:
				pm.SetResolvedWebSearch(key, "disabled")
				slog.Info("响应端网页搜索已禁用", "provider", key, "protocol", protocol, "config", support)
			default:
				pm.SetResolvedWebSearch(key, "enabled")
				slog.Info("已启用响应端网页搜索", "provider", key, "protocol", protocol)
			}
		default:
			// openai-chat 和 google-genai 无原生 web_search，有 API key 时启用注入模式
			if cfg.TavilyAPIKey != "" {
				pm.SetResolvedWebSearch(key, "injected")
				slog.Info("注入式网页搜索已启用", "provider", key, "protocol", protocol)
			} else {
				pm.SetResolvedWebSearch(key, "disabled")
				slog.Info("跳过网页搜索：无 Tavily API key", "provider", key, "protocol", protocol)
			}
		}
	}
	// Wait for all parallel Anthropic probes to complete before model-level resolution.
	wg.Wait()
	// 2. Resolve model-level overrides for provider catalog slugs and route aliases.
	for providerKey, def := range cfg.ProviderDefs {
		for modelName := range def.Models {
			alias := providerKey + "/" + modelName
			newAlias := modelName + "(" + providerKey + ")"
			modelWS := cfg.WebSearchForModel(alias)
			resolveModelWebSearch(ctx, alias, providerKey, modelName, modelWS, pm, cfg)
			resolveModelWebSearch(ctx, newAlias, providerKey, modelName, modelWS, pm, cfg)
		}
	}
	for alias, route := range cfg.Routes {
		modelWS := cfg.WebSearchForModel(alias)
		providerKey := route.Provider
		if providerKey == "" {
			providerKey = pm.DefaultKey()
		}
		resolveModelWebSearch(ctx, alias, providerKey, route.Model, modelWS, pm, cfg)
	}
}

func resolveModelWebSearch(ctx context.Context, alias, providerKey, upstreamModel string, modelWS config.WebSearchSupport, pm *provider.ProviderManager, cfg config.Config) {
	if alias == "" || providerKey == "" || upstreamModel == "" {
		return
	}
	modelKey := "model:" + alias
	candidateKey := provider.WebSearchCandidateKey(providerKey, upstreamModel)
	protocol := pm.ProtocolForModel(alias)
	switch protocol {
	case config.ProtocolAnthropic:
	case config.ProtocolOpenAIResponse:
		switch modelWS {
		case config.WebSearchSupportDisabled, config.WebSearchSupportInjected:
			pm.SetResolvedWebSearch(modelKey, "disabled")
			pm.SetResolvedWebSearch(candidateKey, "disabled")
			slog.Info("模型禁用响应端网页搜索", "model", alias, "config", modelWS)
		default:
			pm.SetResolvedWebSearch(modelKey, "enabled")
			pm.SetResolvedWebSearch(candidateKey, "enabled")
			slog.Info("模型启用响应端网页搜索", "model", alias)
		}
		return
	default:
		pm.SetResolvedWebSearch(modelKey, "disabled")
		pm.SetResolvedWebSearch(candidateKey, "disabled")
		slog.Info("跳过模型级网页搜索：不支持的协议", "model", alias, "protocol", protocol)
		return
	}
	switch modelWS {
	case config.WebSearchSupportDisabled:
		pm.SetResolvedWebSearch(modelKey, "disabled")
		pm.SetResolvedWebSearch(candidateKey, "disabled")
		slog.Info("模型配置禁用网页搜索", "model", alias)
	case config.WebSearchSupportEnabled:
		pm.SetResolvedWebSearch(modelKey, "enabled")
		pm.SetResolvedWebSearch(candidateKey, "enabled")
		slog.Info("模型配置强制启用网页搜索", "model", alias)
	case config.WebSearchSupportInjected:
		pm.SetResolvedWebSearch(modelKey, "injected")
		pm.SetResolvedWebSearch(candidateKey, "injected")
		slog.Info("模型配置启用网页搜索注入模式", "model", alias)
	default:
		// Dedup: skip probe if candidate key already resolved (from provider-level probe or earlier alias).
		if existing := pm.ResolvedWebSearch(candidateKey); existing != "" {
			slog.Debug("模型网页搜索已解析，跳过探测",
				"model", alias,
				"candidate", candidateKey,
				"existing", existing,
			)
			pm.SetResolvedWebSearch(modelKey, existing)
			return
		}
		resolved := resolveModelWebSearchWithProber(ctx, alias, providerKey, upstreamModel, modelWS, pm, cfg, pm)
		pm.SetResolvedWebSearch(modelKey, resolved)
		pm.SetResolvedWebSearch(candidateKey, resolved)
	}
}

func probeProviderWebSearch(ctx context.Context, key string, pm *provider.ProviderManager) string {
	pc, err := pm.ClientForKey(key)
	if err != nil {
		slog.Warn("网页搜索探测跳过：客户端不可用", "provider", key, "error", err)
		return "disabled"
	}

	upstreamModel := pm.FirstUpstreamModelForKey(key)
	if upstreamModel == "" {
		slog.Warn("网页搜索自动探测跳过：无模型路由到提供商", "provider", key)
		return "disabled"
	}

	acc, ok := pc.(provider.AnthropicClientAccessor)
	if !ok {
		slog.Warn("网页搜索探测跳过：客户端不支持访问", "provider", key)
		return "disabled"
	}
	client := acc.AnthropicClient()
	probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	supported, err := client.ProbeWebSearch(probeCtx, upstreamModel)
	if err != nil {
		slog.Warn("网页搜索自动探测失败", "provider", key, "error", err)
		return "disabled"
	}
	if !supported {
		slog.Warn("提供商不支持网页搜索", "provider", key, "model", upstreamModel)
		return "disabled"
	}
	slog.Info("提供商支持网页搜索", "provider", key, "model", upstreamModel)
	return "enabled"
}
func resolveModelWebSearchWithProber(ctx context.Context, modelAlias, providerKey, upstreamModel string, modelWS config.WebSearchSupport, pm *provider.ProviderManager, cfg config.Config, prober webSearchCandidateProber) string {
	switch modelWS {
	case config.WebSearchSupportDisabled:
		return "disabled"
	case config.WebSearchSupportEnabled:
		return "enabled"
	case config.WebSearchSupportInjected:
		return "injected"
	}
	if prober == nil {
		if injectedSearchConfigured(cfg, modelAlias, providerKey) {
			return "injected"
		}
		return "disabled"
	}
	probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	supported, err := prober.ProbeWebSearchCandidate(probeCtx, providerKey, upstreamModel)
	if err != nil {
		slog.Warn("网页搜索模型探测失败", "model", modelAlias, "provider", providerKey, "upstream_model", upstreamModel, "error", err)
		if injectedSearchConfigured(cfg, modelAlias, providerKey) {
			slog.Info("网页搜索模型探测失败，回退到注入模式", "model", modelAlias, "provider", providerKey, "upstream_model", upstreamModel)
			return "injected"
		}
		return "disabled"
	}
	if supported {
		slog.Info("模型支持网页搜索", "model", modelAlias, "provider", providerKey, "upstream_model", upstreamModel)
		return "enabled"
	}
	if injectedSearchConfigured(cfg, modelAlias, providerKey) {
		slog.Info("模型不支持原生网页搜索，回退到注入模式", "model", modelAlias, "provider", providerKey, "upstream_model", upstreamModel)
		return "injected"
	}
	slog.Warn("模型不支持网页搜索", "model", modelAlias, "provider", providerKey, "upstream_model", upstreamModel)
	return "disabled"
}

func injectedSearchConfigured(cfg config.Config, modelAlias, providerKey string) bool {
	if cfg.WebSearchTavilyKeyForModel(modelAlias) != "" || cfg.WebSearchFirecrawlKeyForModel(modelAlias) != "" {
		return true
	}
	if providerKey == "" {
		return false
	}
	return cfg.WebSearchTavilyKeyForProvider(providerKey) != "" || cfg.WebSearchFirecrawlKeyForProvider(providerKey) != ""
}

func runCaptureResponse(ctx context.Context, cfg config.Config, errors io.Writer) error {
	tracer := mbtrace.New(captureResponseTraceConfig(cfg.TraceRequests))
	logTrace(errors, "response proxy", tracer)
	handler, err := proxy.NewResponse(proxy.ResponseConfig{
		UpstreamBaseURL: cfg.ResponseProxy.ProviderBaseURL,
		APIKey:          cfg.ResponseProxy.ProviderAPIKey,
		Tracer:          tracer,
		TraceErrors:     errors,
	})
	if err != nil {
		return err
	}
	slog.Info("响应代理已初始化", "upstream", cfg.ResponseProxy.ProviderBaseURL)
	return runHTTPServer(ctx, cfg.Addr, handler, errors, nil)
}

func runCaptureAnthropic(ctx context.Context, cfg config.Config, errors io.Writer) error {
	tracer := mbtrace.New(captureAnthropicTraceConfig(cfg.TraceRequests))
	logTrace(errors, "anthropic proxy", tracer)
	handler, err := proxy.NewAnthropic(proxy.AnthropicConfig{
		UpstreamBaseURL: cfg.AnthropicProxy.ProviderBaseURL,
		APIKey:          cfg.AnthropicProxy.ProviderAPIKey,
		Version:         cfg.AnthropicProxy.ProviderVersion,
		Tracer:          tracer,
		TraceErrors:     errors,
	})
	if err != nil {
		return err
	}
	slog.Info("Anthropic 代理已初始化", "upstream", cfg.AnthropicProxy.ProviderBaseURL)
	return runHTTPServer(ctx, cfg.Addr, handler, errors, nil)
}

func logTrace(errors io.Writer, label string, tracer *mbtrace.Tracer) {
	if !tracer.Enabled() {
		fmt.Fprintf(errors, "%s 跟踪已禁用\n", label)
		return
	}
	slog.Info("跟踪已启用", "label", label, "dir", tracer.Directory())
	fmt.Fprintf(errors, "%s 跟踪已启用于 %s\n", label, tracer.Directory())
}

func transformTraceRoot() string {
	return filepath.Join(mbtrace.DefaultRoot, "Transform")
}

func captureResponseTraceConfig(enabled bool) mbtrace.Config {
	return mbtrace.Config{
		Enabled: enabled,
		Root:    filepath.Join(mbtrace.DefaultRoot, "Capture", "Response"),
	}
}

func captureAnthropicTraceConfig(enabled bool) mbtrace.Config {
	return mbtrace.Config{
		Enabled: enabled,
		Root:    filepath.Join(mbtrace.DefaultRoot, "Capture", "Anthropic"),
	}
}

func runHTTPServer(ctx context.Context, addr string, handler http.Handler, errors io.Writer, sessionStats *stats.SessionStats) error {
	httpServer := &http.Server{Addr: addr, Handler: handler}
	defer func() {
		if closer, ok := handler.(io.Closer); ok {
			_ = closer.Close()
		}
	}()
	errCh := make(chan error, 1)
	go func() {
		fmt.Fprintf(errors, "%s 监听于 %s\n", Name, addr)
		consoleURL := fmt.Sprintf("http://%s/console/", addr)
		fmt.Fprintf(errors, "Web Console: %s\n", consoleURL)
		slog.Info("HTTP 服务器监听中", "addr", addr, "webui", consoleURL)
		errCh <- httpServer.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		if sessionStats != nil {
			summary := sessionStats.Summary()
			slog.Info(stats.FormatSummaryLine(summary))
			fmt.Fprintln(errors)
			stats.WriteSummary(errors, summary)
		}
		shutdownCtx, cancel := context.WithCancel(context.Background())
		defer cancel()
		return httpServer.Shutdown(shutdownCtx)
	case err := <-errCh:
		if err == http.ErrServerClosed {
			return nil
		}
		slog.Error("HTTP 服务器错误", "error", err)
		return err
	}
}

// DumpConfigSchema dumps JSON Schema files alongside the config file,
// including known plugin config types. Call via --dump-config-schema flag.
func DumpConfigSchema(configPath string) error {
	return config.DumpConfigSchemaWithOptions(configPath, config.SchemaOptions{
		ExtensionSpecs: BuiltinExtensions().ConfigSpecs(),
	})
}
