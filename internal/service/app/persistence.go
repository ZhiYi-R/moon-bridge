package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"moonbridge/internal/config"
	"moonbridge/internal/db"
	"moonbridge/internal/extension/plugin"
	"moonbridge/internal/logger"
	"moonbridge/internal/service/store"
)

// ResolvePersistenceActiveProvider chooses the provider name that should be
// passed into db.Registry.Init. It preserves explicit configuration and only
// auto-selects when the startup environment makes the choice deterministic.
func ResolvePersistenceActiveProvider(configured string, providers []db.Provider) string {
	configured = strings.TrimSpace(configured)
	if configured != "" {
		return configured
	}
	if len(providers) == 1 {
		return providers[0].Name()
	}
	var workerBound db.Provider
	for _, provider := range providers {
		if provider == nil || !provider.Features().WorkerBound {
			continue
		}
		if workerBound != nil {
			return ""
		}
		workerBound = provider
	}
	if workerBound != nil {
		return workerBound.Name()
	}
	return ""
}

// PersistenceResult 是持久化引导的结果。
//
// 引入动机：原 runTransform 内联了 db.Registry 装配 + ConfigStore.LoadAll 覆盖逻辑
// （app.go 历史 125-197 行），但 -print-* 子命令在覆盖发生前就退出，读到的是未经
// SQLite 覆盖的半成品配置。将该逻辑抽成 BootstrapPersistence 后，runTransform 与
// main 的 print 路径共享同一份覆盖语义。
type PersistenceResult struct {
	// Cfg 是覆盖后的有效配置。当 DB 为空或持久化不可用时，等于输入 cfg。
	Cfg config.Config
	// Store 是活跃的 ConfigStore 句柄，runTransform 需要它注入 HTTP server
	// 以支持运行时热重载。nil 表示持久化未启用或 store 不可用。
	Store store.ConfigStore
	// Overridden 指示 Cfg 是否被 SQLite 中的配置覆盖。为 true 时调用方需重建
	// 派生状态（providerMgr/pricing 等）。
	Overridden bool
	// Shutdown 释放 db.Registry 持有的资源（数据库连接等）。无副作用时为 nil。
	// 调用方必须 defer 调用以避免资源泄漏。
	Shutdown func() error
}

// BootstrapPersistence 装配持久化层并用 SQLite 中的配置覆盖 YAML 配置。
//
// 职责：db.Registry 装配（provider + consumer 注册）、dbRegistry.Init、
// ConfigStore.LoadAll 覆盖、空库 seed、ErrConfigNotSeeded 处理。
// 不负责 providerMgr/pricing 重建——那属于 server 运行时构建关注点，由调用方
// 根据 Overridden 自行处理。
//
// cfg 与 plugins 由调用方预先构建：plugins 来自 YAML cfg（与历史行为一致，不重建）。
//
// 失败语义（Fail Fast，与历史 runTransform 内联逻辑等价）：
//   - dbRegistry.Init 失败：返回 error（致命，与历史 app.go:146 一致）。
//   - LoadAll 返回 ErrConfigNotSeeded：从 YAML seed 到 DB，seed 失败返回 error
//     （与历史 app.go:189 一致）。
//   - LoadAll 返回其它错误：记 Warn 后继续（与历史 app.go:192 一致）。
//   - 持久化未启用（无 db provider / store 不可用）：返回原 cfg，Store 为 nil。
func BootstrapPersistence(ctx context.Context, cfg config.Config, plugins *plugin.Registry) (PersistenceResult, error) {
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
		// Init 失败时 dbRegistry 可能已打开部分资源，Shutdown 兜底释放。
		_ = dbRegistry.Shutdown()
		return PersistenceResult{Cfg: cfg}, fmt.Errorf("init persistence: %w", err)
	}

	result := PersistenceResult{
		Cfg:      cfg,
		Store:    configStoreConsumer.Store(),
		Shutdown: dbRegistry.Shutdown,
	}

	// === ConfigStore bootstrap ===
	cs := result.Store
	if cs == nil {
		// store 不可用：持久化 consumer 被禁用或无 provider。保持 cfg 不变。
		logger.Warn("config store 不可用，跳过持久化引导")
		return result, nil
	}

	dbCfg, loadErr := cs.LoadAll()
	switch {
	case loadErr == nil:
		if len(dbCfg.ProviderDefs) > 0 || len(dbCfg.Routes) > 0 {
			// DB has existing configuration: use it as the active config.
			logger.Info("从持久化存储加载配置",
				"providers", len(dbCfg.ProviderDefs),
				"routes", len(dbCfg.Routes))
			result.Cfg = *dbCfg
			result.Overridden = true
		} else {
			// DB is empty: seed from YAML config.
			logger.Info("持久化存储为空，从 YAML 导入种子配置")
			if err := cs.SeedFromConfig(&cfg); err != nil {
				logger.Warn("config store 种子导入失败", "error", err)
			}
		}
	case errors.Is(loadErr, store.ErrConfigNotSeeded):
		logger.Info("持久化存储为空，从 YAML 导入种子配置")
		if err := cs.SeedFromConfig(&cfg); err != nil {
			_ = dbRegistry.Shutdown()
			return PersistenceResult{Cfg: cfg}, fmt.Errorf("seed config store from YAML: %w", err)
		}
	default:
		logger.Warn("config store 加载失败", "error", loadErr)
	}
	return result, nil
}

// TryLoadEffectiveConfig 是 BootstrapPersistence 的轻量入口，供只读场景
// （如 -print-* 子命令）使用。
//
// 引入动机：main.go 的 print 路径在 cfg 加载后就退出，到不了 runTransform 内的
// SQLite 覆盖点。此函数让 print 路径读到覆盖后的有效配置。
//
// 与 BootstrapPersistence 的区别：调用方无需预先构建 plugin.Registry。本函数
// 用 BuiltinExtensions() 构建最小 registry（含必要的 InitAll，因为 DBProvider
// 的可用性依赖插件 Init 注入的状态），引导完成后立即 ShutdownAll + dbRegistry.Shutdown，
// 返回纯 cfg，不留悬空资源。
//
// 失败（持久化未启用、DB 不可用、Init 失败）返回 error，调用方 fallback 到 YAML cfg。
func TryLoadEffectiveConfig(ctx context.Context, cfg config.Config) (config.Config, error) {
	plugins := BuiltinExtensions().NewRegistry(slog.Default(), cfg)
	if err := plugins.InitAll(&cfg); err != nil {
		plugins.ShutdownAll()
		return cfg, fmt.Errorf("init plugins for effective config load: %w", err)
	}
	defer plugins.ShutdownAll()

	result, err := BootstrapPersistence(ctx, cfg, plugins)
	if err != nil {
		if result.Shutdown != nil {
			result.Shutdown()
		}
		return cfg, err
	}
	if result.Shutdown != nil {
		defer result.Shutdown()
	}
	return result.Cfg, nil
}
