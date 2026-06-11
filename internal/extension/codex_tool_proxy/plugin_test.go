package codextoolproxy

import (
	"testing"

	"moonbridge/internal/config"
)

func TestDisablePatchProxyForModelDefaultEnabled(t *testing.T) {
	cfg, err := config.LoadFromYAMLWithOptions([]byte(_testYAML), config.LoadOptions{ExtensionSpecs: ConfigSpecs()})
	if err != nil {
		t.Fatal(err)
	}
	if !DisablePatchProxyForModel(cfg, "test-model") {
		t.Fatal("expected proxy disabled by default")
	}
}

func TestDisablePatchProxyForModelWhenExtensionDisabled(t *testing.T) {
	cfg, err := config.LoadFromYAMLWithOptions([]byte(_testYAML+_extDisabled), config.LoadOptions{ExtensionSpecs: ConfigSpecs()})
	if err != nil {
		t.Fatal(err)
	}
	if !DisablePatchProxyForModel(cfg, "test-model") {
		t.Fatal("expected proxy disabled when extension is disabled")
	}
}

func TestDisablePatchProxyForModelRouteOverride(t *testing.T) {
	cfg, err := config.LoadFromYAMLWithOptions([]byte(_testYAML+_routeYAML), config.LoadOptions{ExtensionSpecs: ConfigSpecs()})
	if err != nil {
		t.Fatal(err)
	}
	if !DisablePatchProxyForModel(cfg, "specific") {
		t.Fatal("expected proxy disabled via specific route override")
	}
	if !DisablePatchProxyForModel(cfg, "unmatched-model") {
		t.Fatal("expected proxy disabled for unmatched model (default)")
	}
}

const _testYAML = `
mode: Transform
models:
  test-model:
    context_window: 200000
providers:
  main:
    base_url: https://example.test
    api_key: test
    offers:
      - model: test-model
`

const _extDisabled = `
extensions:
  codex_tool_proxy:
    enabled: false
`

const _routeYAML = `
routes:
  specific:
    model: test-model
    provider: main
    extensions:
      codex_tool_proxy:
        enabled: false
`
