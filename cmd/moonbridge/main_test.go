package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"moonbridge/internal/config"
	"moonbridge/internal/extension/codex"
	"moonbridge/internal/service/app"
)

func TestPrintCodexConfigTomlDoesNotSetServiceTier(t *testing.T) {
	var output bytes.Buffer
	cfg := config.Config{
		Routes: map[string]config.RouteEntry{
			"moonbridge": {
				Provider:      "openai",
				Model:         "gpt-5.4",
				ContextWindow: 200000,
			},
		},
	}
	err := codex.GenerateConfigToml(&output, "moonbridge", "http://127.0.0.1:38440/v1", "",
		config.ProviderFromGlobalConfig(&cfg), config.PluginFromGlobalConfig(&cfg), config.ServerFromGlobalConfig(&cfg))
	if err != nil {
		t.Fatalf("codex.GenerateConfigToml() error = %v", err)
	}
	generated := output.String()

	for _, notWant := range []string{"service_tier", "flex"} {
		if strings.Contains(generated, notWant) {
			t.Fatalf("generated config should not contain %q:\n%s", notWant, generated)
		}
	}
	for _, want := range []string{
		`model = "moonbridge"`,
		`model_provider = "moonbridge"`,
		`model_context_window = 200000`,
		`wire_api = "responses"`,
	} {
		if !strings.Contains(generated, want) {
			t.Fatalf("generated config missing %q:\n%s", want, generated)
		}
	}
}

func TestRunReturnsStartupErrorWithConfigDetails(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yml")
	err := os.WriteFile(configPath, []byte(`
mode: Transform
models:
  gpt-image-1.5: {}
providers:
  openai:
    base_url: https://openai.example.test
    api_key: openai-key
    protocol: openai
    offers:
      - model: gpt-image-1.5
routes:
  image:
    model: gpt-image-1.5
    provider: openai
`), 0644)
	if err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run([]string{"-config", configPath, "-print-mode"}, &stdout, &stderr)

	if code != exitStartupErr {
		t.Fatalf("run() exit code = %d, want %d", code, exitStartupErr)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	output := stderr.String()
	for _, want := range []string{
		"Moon Bridge 启动失败：配置文件加载失败",
		"配置文件: " + configPath,
		"providers.openai.protocol must be \"anthropic\", \"openai-response\", \"google-genai\", or \"openai-chat\"",
		"Responses 直通请使用 openai-response",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("stderr missing %q:\n%s", want, output)
		}
	}
}

func TestRunUsesHomeMoonBridgeDefaultConfigPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "xdg"))
	configDir := filepath.Join(home, "moonbridge")
	if err := os.Mkdir(configDir, 0755); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}
	configPath := filepath.Join(configDir, "config.yml")
	err := os.WriteFile(configPath, []byte(`
mode: CaptureResponse
proxy:
  response:
    model: gpt-capture
    base_url: https://api.openai.example.test
    api_key: upstream-openai-key
`), 0644)
	if err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	before, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run([]string{"-print-mode"}, &stdout, &stderr)

	if code != exitOK {
		t.Fatalf("run() exit code = %d, want %d; stderr = %s", code, exitOK, stderr.String())
	}
	if got := strings.TrimSpace(stdout.String()); got != "CaptureResponse" {
		t.Fatalf("stdout = %q, want CaptureResponse", got)
	}
	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile() after run error = %v", err)
	}
	if string(after) != string(before) {
		t.Fatalf("existing default config was overwritten\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if strings.Contains(stderr.String(), "已创建默认配置") {
		t.Fatalf("stderr should not mention default config creation for existing config:\n%s", stderr.String())
	}
}

func TestRunCreatesStarterConfigWhenDefaultConfigIsMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "xdg"))

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run([]string{"-print-mode"}, &stdout, &stderr)

	if code != exitOK {
		t.Fatalf("run() exit code = %d, want %d; stderr = %s", code, exitOK, stderr.String())
	}
	if got := strings.TrimSpace(stdout.String()); got != "Transform" {
		t.Fatalf("stdout = %q, want Transform", got)
	}
	configPath := filepath.Join(home, "moonbridge", "config.yml")
	dirInfo, err := os.Stat(filepath.Dir(configPath))
	if err != nil {
		t.Fatalf("stat config dir: %v", err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("created config dir mode = %v, want 0700", got)
	}
	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatalf("stat created config: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("created config mode = %v, want 0600", got)
	}
	cfg, err := config.LoadFromFileWithOptions(configPath, config.LoadOptions{
		ExtensionSpecs: app.BuiltinExtensions().ConfigSpecs(),
	})
	if err != nil {
		t.Fatalf("created config failed to load: %v", err)
	}
	if cfg.Mode != config.ModeTransform {
		t.Fatalf("created config mode = %q, want Transform", cfg.Mode)
	}
	if cfg.Persistence.ActiveProvider != "db_sqlite" {
		t.Fatalf("persistence.active_provider = %q, want db_sqlite", cfg.Persistence.ActiveProvider)
	}
	sqliteConfig, ok := cfg.Extensions["db_sqlite"]
	if !ok {
		t.Fatalf("extensions missing db_sqlite: %+v", cfg.Extensions)
	}
	if sqliteConfig.Enabled == nil || !*sqliteConfig.Enabled {
		t.Fatalf("extensions.db_sqlite.enabled = %v, want true", sqliteConfig.Enabled)
	}
	dbPath, ok := sqliteConfig.RawConfig["path"].(string)
	if !ok {
		t.Fatalf("extensions.db_sqlite.config.path = %#v, want string", sqliteConfig.RawConfig["path"])
	}
	wantDBPath := filepath.Join(home, "moonbridge", "data", "moonbridge.db")
	if dbPath != wantDBPath {
		t.Fatalf("sqlite db path = %q, want %q", dbPath, wantDBPath)
	}
	if !filepath.IsAbs(dbPath) {
		t.Fatalf("sqlite db path = %q, want absolute path", dbPath)
	}
	output := stderr.String()
	if !strings.Contains(output, "已创建默认配置") || !strings.Contains(output, configPath) {
		t.Fatalf("stderr missing starter config creation notice with path:\n%s", output)
	}
}

func TestRunExplicitMissingConfigStillFailsFast(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	missingPath := filepath.Join(t.TempDir(), "missing", "config.yml")

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run([]string{"-config", missingPath, "-print-mode"}, &stdout, &stderr)

	if code != exitStartupErr {
		t.Fatalf("run() exit code = %d, want %d", code, exitStartupErr)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if _, err := os.Stat(missingPath); !os.IsNotExist(err) {
		t.Fatalf("explicit missing config stat error = %v, want not exist", err)
	}
	output := stderr.String()
	for _, want := range []string{
		"Moon Bridge 启动失败：配置文件加载失败",
		"配置文件: " + missingPath,
		"read config " + missingPath,
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("stderr missing %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "已创建默认配置") {
		t.Fatalf("stderr should not mention default config creation:\n%s", output)
	}
}

func TestPublishConfigFileDoesNotOverwriteExistingFinalFile(t *testing.T) {
	dir := t.TempDir()
	finalPath := filepath.Join(dir, "config.yml")
	tempPath := filepath.Join(dir, ".config.yml.tmp")
	if err := os.WriteFile(finalPath, []byte("existing-config"), 0o600); err != nil {
		t.Fatalf("WriteFile(final) error = %v", err)
	}
	if err := os.WriteFile(tempPath, []byte("starter-config"), 0o600); err != nil {
		t.Fatalf("WriteFile(temp) error = %v", err)
	}

	created, err := publishConfigFile(tempPath, finalPath)

	if err != nil {
		t.Fatalf("publishConfigFile() error = %v", err)
	}
	if created {
		t.Fatal("publishConfigFile() created = true, want false")
	}
	got, err := os.ReadFile(finalPath)
	if err != nil {
		t.Fatalf("ReadFile(final) error = %v", err)
	}
	if string(got) != "existing-config" {
		t.Fatalf("final file content = %q, want existing-config", got)
	}
	if _, err := os.Stat(tempPath); !os.IsNotExist(err) {
		t.Fatalf("temp file stat error = %v, want not exist", err)
	}
}

// TestRunPrintCodexConfigReadsSQLiteOverride 是核心回归测试：
// 验证 -print-codex-config 能读到 SQLite 持久化覆盖后的配置，而非 YAML 源。
//
// 场景：config.yml 里是 placeholder provider/model，但 SQLite 中存的是 db-provider。
// 修复前 print 只读 YAML（半成品）；修复后经 TryLoadEffectiveConfig 读到 DB 覆盖配置。
//
// 两阶段：
//  1. 用含 db-provider 的 config.yml 跑一次 → db_sqlite 自动 seed（库获得 db-provider）
//  2. 改写 config.yml 为 placeholder provider，再跑 -print-codex-config db-alias
//     → 应输出 SQLite 里的 db-upstream-model（证明读到了覆盖配置）
func TestRunPrintCodexConfigReadsSQLiteOverride(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "moonbridge.db")
	configPath := filepath.Join(dir, "config.yml")

	// 第一阶段：含 db-provider 的配置，用于 seed SQLite。
	seedConfig := []byte(`
mode: Transform
server:
  addr: "127.0.0.1:0"
persistence:
  active_provider: db_sqlite
extensions:
  db_sqlite:
    enabled: true
    config:
      path: ` + dbPath + `
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
    model: db-model
`)
	if err := os.WriteFile(configPath, seedConfig, 0o600); err != nil {
		t.Fatalf("WriteFile(seed): %v", err)
	}

	// 第一次 run：触发 db_sqlite 初始化 + seed（-print-mode 提前退出，但持久化已 seed）。
	var seedStdout, seedStderr bytes.Buffer
	if code := run([]string{"-config", configPath, "-print-mode"}, &seedStdout, &seedStderr); code != exitOK {
		t.Fatalf("seed run exit = %d, want %d; stderr:\n%s", code, exitOK, seedStderr.String())
	}

	// 确认 SQLite 文件已创建（seed 生效）。
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("seed did not create db file %s: %v", dbPath, err)
	}

	// 第二阶段：改写 config.yml 为完全不同的 placeholder provider/model。
	// SQLite 中仍是第一阶段 seed 的 db-provider。print 应读 DB 而非这份 YAML。
	placeholderConfig := []byte(`
mode: Transform
server:
  addr: "127.0.0.1:0"
persistence:
  active_provider: db_sqlite
extensions:
  db_sqlite:
    enabled: true
    config:
      path: ` + dbPath + `
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
	if err := os.WriteFile(configPath, placeholderConfig, 0o600); err != nil {
		t.Fatalf("WriteFile(placeholder): %v", err)
	}

	// 核心断言：-print-codex-config 应输出 db-alias（DB 里的路由）而非 yaml-alias。
	// db-alias 在 YAML 中已不存在，只有读到 SQLite 覆盖配置才能生成。
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-config", configPath,
		"-print-codex-config", "db-alias",
	}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("print run exit = %d, want %d; stderr:\n%s", code, exitOK, stderr.String())
	}

	output := stdout.String()
	// db-alias 是 SQLite 中的路由别名；YAML（placeholder）里只有 yaml-alias。
	// 输出含 db-alias 即证明 print 读到了 SQLite 覆盖配置。
	if !strings.Contains(output, `model = "db-alias"`) {
		t.Errorf("print output missing model=\"db-alias\" (proves it read YAML not SQLite):\n%s", output)
	}
	// context_window = 200000 来自 DB 的 db-model；YAML 的 yaml-model 是 128000。
	// 这进一步证明读的是 SQLite 数据而非 YAML。
	if !strings.Contains(output, "model_context_window = 200000") {
		t.Errorf("print output missing model_context_window=200000 (DB value; YAML yaml-model is 128000):\n%s", output)
	}
	// 反向验证：YAML 的 placeholder 路由不应出现（证明 DB 完全覆盖 YAML）。
	if strings.Contains(output, "yaml-alias") {
		t.Errorf("print output contains yaml-alias (should not: DB should override YAML):\n%s", output)
	}
}
