package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ZhiYi-R/moon-bridge/internal/config"
	"github.com/ZhiYi-R/moon-bridge/internal/extension/codex"
	"github.com/ZhiYi-R/moon-bridge/internal/service/app"
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
