package app

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"testing"

	"moonbridge/internal/config"
	"moonbridge/internal/extension/plugin"
)

// 这些测试验证 BootstrapPersistence 的核心契约：SQLite 中的配置能覆盖 YAML 配置。
// 使用真实 SQLite 文件（经 db.Registry + ConfigStore 流程），而非 mock，以验证
// 完整逻辑链（db.Registry 装配 → Init → LoadAll → cfg 覆盖）。
//
// cfg 通过 LoadFromYAMLWithOptions 构造，注入 builtin extension specs，使
// db_sqlite 插件能正确解码 path 字段（模拟 main.go 的真实加载路径）。

// discardLogger 返回一个只输出 Error 级别的 slog logger，避免测试日志噪音。
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(&discardWriter{}, &slog.HandlerOptions{Level: slog.LevelError}))
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

// cfgFromYAML 用 builtin extension specs 解析 YAML，返回注入了 specs 的 config.Config。
func cfgFromYAML(t *testing.T, yaml string) config.Config {
	t.Helper()
	cfg, err := config.LoadFromYAMLWithOptions([]byte(yaml), config.LoadOptions{
		ExtensionSpecs: BuiltinExtensions().ConfigSpecs(),
	})
	if err != nil {
		t.Fatalf("LoadFromYAMLWithOptions: %v\nyaml:\n%s", err, yaml)
	}
	return cfg
}

// cfgWithSQLiteAt 构造一份启用 db_sqlite、库文件指向 absPath 的 Transform cfg。
// cfgWithSQLiteAt 构造一份启用 db_sqlite、库文件指向 absPath 的 Transform cfg。
// 含一个占位 provider（yaml-placeholder）以通过 Transform 模式的 providers 必填校验；
// 它与 DB 中的 db-provider 内容不同，用于验证覆盖是否生效。
func cfgWithSQLiteAt(t *testing.T, absPath string) config.Config {
	t.Helper()
	yaml := fmt.Sprintf(`
mode: Transform
server:
  addr: "127.0.0.1:0"
persistence:
  active_provider: db_sqlite
extensions:
  db_sqlite:
    enabled: true
    config:
      path: %q
models:
  yaml-model:
    context_window: 128000
providers:
  yaml-placeholder:
    base_url: "https://yaml.example.test"
    api_key: "yaml-key"
    protocol: anthropic
    offers:
      - model: yaml-model
routes:
  yaml-alias:
    provider: yaml-placeholder
    model: yaml-model
`, absPath)
	return cfgFromYAML(t, yaml)
}

// dbStoredCfgYAML 是要写入 SQLite 的配置内容，与 YAML 源不同，用于验证覆盖生效。
// 通过 SaveConfig 注入，使 DB 中的 provider/route 与 YAML 源的空 provider 不同。
const dbStoredCfgYAML = `
mode: Transform
server:
  addr: "127.0.0.1:9999"
models:
  db-model:
    context_window: 200000
providers:
  db-provider:
    base_url: "https://db.example.test"
    api_key: "db-key"
    protocol: anthropic
    offers:
      - model: db-model
        upstream_name: db-upstream-model
routes:
  db-alias:
    provider: db-provider
    model: db-upstream-model
`

// newPluginsForCfg 构建 plugin.Registry 并 InitAll，注册 cleanup 调 ShutdownAll。
func newPluginsForCfg(t *testing.T, cfg config.Config) *plugin.Registry {
	t.Helper()
	plugins := BuiltinExtensions().NewRegistry(discardLogger(), cfg)
	if err := plugins.InitAll(&cfg); err != nil {
		t.Fatalf("plugins.InitAll: %v", err)
	}
	t.Cleanup(plugins.ShutdownAll)
	return plugins
}

// TestBootstrapPersistence_OverridesCfgFromSQLite 验证核心修复：
// 当 SQLite 中存在配置时，BootstrapPersistence 用 DB 配置覆盖输入 cfg。
func TestBootstrapPersistence_OverridesCfgFromSQLite(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	abs, err := filepath.Abs(dbPath)
	if err != nil {
		t.Fatalf("abs: %v", err)
	}

	// 第一遍：空库 → BootstrapPersistence 自动从 seedCfg 建 seed。
	seedCfg := cfgWithSQLiteAt(t, abs)
	plugins1 := newPluginsForCfg(t, seedCfg)
	r1, err := BootstrapPersistence(context.Background(), seedCfg, plugins1)
	if err != nil {
		t.Fatalf("first BootstrapPersistence (seed): %v", err)
	}
	if r1.Store == nil {
		t.Fatal("first run Store = nil; cannot inject DB content")
	}
	// 用 dbStoredCfg 覆盖库内容（模拟 WebUI 编辑后的持久化状态）。
	dbStoredCfg := cfgFromYAML(t, dbStoredCfgYAML)
	if _, err := r1.Store.SaveConfig(context.Background(), &dbStoredCfg); err != nil {
		t.Fatalf("SaveConfig to inject DB content: %v", err)
	}
	if r1.Shutdown != nil {
		r1.Shutdown()
	}

	// 第二遍：DB 现在有 db-provider/db-alias（取代了 seed 的 yaml-placeholder），
	// 应覆盖输入 cfg（YAML 自带 yaml-placeholder，但 DB 整体替换后只剩 db-provider）。
	yamlCfg := cfgWithSQLiteAt(t, abs)
	plugins2 := newPluginsForCfg(t, yamlCfg)
	result, err := BootstrapPersistence(context.Background(), yamlCfg, plugins2)
	if err != nil {
		t.Fatalf("second BootstrapPersistence: %v", err)
	}
	if result.Shutdown != nil {
		defer result.Shutdown()
	}

	if !result.Overridden {
		t.Fatal("Overridden = false, want true (DB should override cfg)")
	}
	if got := len(result.Cfg.ProviderDefs); got != 1 {
		t.Fatalf("ProviderDefs count = %d, want 1 (from DB); got %+v", got, result.Cfg.ProviderDefs)
	}
	def, ok := result.Cfg.ProviderDefs["db-provider"]
	if !ok {
		t.Fatalf("ProviderDefs missing db-provider; got keys %+v", result.Cfg.ProviderDefs)
	}
	if def.BaseURL != "https://db.example.test" {
		t.Errorf("db-provider BaseURL = %q, want https://db.example.test", def.BaseURL)
	}
	if _, ok := result.Cfg.Routes["db-alias"]; !ok {
		t.Errorf("Routes missing db-alias; got %+v", result.Cfg.Routes)
	}
	if result.Store == nil {
		t.Error("Store = nil, want non-nil (server runtime needs it)")
	}
}

// TestBootstrapPersistence_SeedsWhenDBEmpty 验证空库分支：
// DB 未初始化时，从 YAML seed 到 DB，cfg 不变。
func TestBootstrapPersistence_SeedsWhenDBEmpty(t *testing.T) {
	abs, err := filepath.Abs(filepath.Join(t.TempDir(), "empty.db"))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	inCfg := cfgWithSQLiteAt(t, abs)

	plugins := newPluginsForCfg(t, inCfg)
	result, err := BootstrapPersistence(context.Background(), inCfg, plugins)
	if err != nil {
		t.Fatalf("BootstrapPersistence: %v", err)
	}
	if result.Shutdown != nil {
		defer result.Shutdown()
	}

	if result.Overridden {
		t.Error("Overridden = true, want false (empty DB should not override)")
	}
	// cfg 应保持输入内容，未被覆盖。
	if result.Cfg.Addr != inCfg.Addr {
		t.Errorf("cfg.Addr = %q, want original %q", result.Cfg.Addr, inCfg.Addr)
	}
	// store 应已 seed：再次 LoadAll 应能拿到数据。
	if result.Store == nil {
		t.Fatal("Store = nil; want non-nil after seed")
	}
	loaded, loadErr := result.Store.LoadAll()
	if loadErr != nil {
		t.Fatalf("post-seed LoadAll: %v", loadErr)
	}
	if len(loaded.ProviderDefs) == 0 && len(loaded.Routes) == 0 {
		t.Error("post-seed DB still empty; seed did not populate config")
	}
}

// TestBootstrapPersistence_DisabledPersistenceReturnsOriginalCfg 验证：
// 未启用持久化（无 db_sqlite extension）时，Store 为 nil，cfg 不变。
func TestBootstrapPersistence_DisabledPersistenceReturnsOriginalCfg(t *testing.T) {
	// 无 Persistence.ActiveProvider，无 db_sqlite extension。
	inCfg := cfgFromYAML(t, `
mode: Transform
server:
  addr: "127.0.0.1:0"
models:
  yaml-model:
    context_window: 128000
providers:
  yaml-placeholder:
    base_url: "https://yaml.example.test"
    api_key: "yaml-key"
    protocol: anthropic
    offers:
      - model: yaml-model
routes:
  yaml-alias:
    provider: yaml-placeholder
    model: yaml-model
`)

	plugins := newPluginsForCfg(t, inCfg)
	result, err := BootstrapPersistence(context.Background(), inCfg, plugins)
	if err != nil {
		t.Fatalf("BootstrapPersistence: %v", err)
	}
	if result.Shutdown != nil {
		defer result.Shutdown()
	}

	if result.Overridden {
		t.Error("Overridden = true, want false when persistence disabled")
	}
	if result.Store != nil {
		t.Errorf("Store = %v, want nil when persistence disabled", result.Store)
	}
	if result.Cfg.Addr != inCfg.Addr {
		t.Errorf("cfg.Addr = %q, want %q (unchanged)", result.Cfg.Addr, inCfg.Addr)
	}
}
