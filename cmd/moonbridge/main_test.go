package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"moonbridge/internal/extension/codex"
	"moonbridge/internal/foundation/config"
)

func TestPrintCodexConfigTomlDoesNotSetServiceTier(t *testing.T) {
	var output bytes.Buffer
	err := codex.GenerateConfigToml(&output, "moonbridge", "http://127.0.0.1:38440/v1", "", config.Config{
		Routes: map[string]config.RouteEntry{
			"moonbridge": {
				Provider:      "openai",
				Model:         "gpt-5.4",
				ContextWindow: 200000,
			},
		},
	})
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
provider:
  providers:
    openai:
      base_url: https://openai.example.test
      api_key: openai-key
      protocol: openai
      models:
        gpt-image-1.5: {}
  routes:
    image: "openai/gpt-image-1.5"
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
		"providers.openai.protocol must be \"anthropic\" or \"openai-response\"",
		"Responses 直通请使用 openai-response",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("stderr missing %q:\n%s", want, output)
		}
	}
}

func TestRunUsesXDGDefaultConfigPath(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	configDir := filepath.Join(configHome, "moonbridge")
	if err := os.Mkdir(configDir, 0755); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}
	configPath := filepath.Join(configDir, "config.yml")
	err := os.WriteFile(configPath, []byte(`
mode: CaptureResponse
developer:
  proxy:
    response:
      model: gpt-capture
      provider:
        base_url: https://api.openai.example.test
        api_key: upstream-openai-key
`), 0644)
	if err != nil {
		t.Fatalf("WriteFile() error = %v", err)
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
}
