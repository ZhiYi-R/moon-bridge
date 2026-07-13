package visual

import (
	"testing"

	"github.com/ZhiYi-R/moon-bridge/internal/config"
)

func boolPtr(v bool) *bool {
	return &v
}

func TestConfigForModelFromResolvedConfig_UsesRouteScope(t *testing.T) {
	cfg := config.Config{
		ProviderDefs: map[string]config.ProviderDef{
			"anthropic": {
				BaseURL: "https://api.anthropic.com",
				APIKey:  "sk-ant-test",
				Models: map[string]config.ModelMeta{
					"claude-sonnet-20241022": {},
				},
			},
			"vision": {
				BaseURL: "https://vision.example.com",
				APIKey:  "sk-vision-test",
				Models: map[string]config.ModelMeta{
					"vision-route": {},
				},
			},
		},
		Routes: map[string]config.RouteEntry{
			"claude-sonnet": {
				Provider: "anthropic",
				Model:    "claude-sonnet-20241022",
				Extensions: map[string]config.ExtensionSettings{
					PluginName: {
						Enabled: boolPtr(true),
						RawConfig: map[string]any{
							"provider":   "vision",
							"model":      "vision-route",
							"max_rounds": 7,
							"max_tokens": 3333,
						},
					},
				},
			},
		},
		Extensions: map[string]config.ExtensionSettings{
			PluginName: {
				Enabled: boolPtr(true),
				RawConfig: map[string]any{
					"provider":   "vision",
					"model":      "vision-global",
					"max_rounds": 2,
					"max_tokens": 1000,
				},
			},
		},
	}

	got, ok := ConfigForModelFromResolvedConfig(cfg, "claude-sonnet")
	if !ok {
		t.Fatal("expected visual config to be enabled")
	}
	if got.Provider != "vision" || got.Model != "vision-route" {
		t.Fatalf("visual config provider/model = %s/%s", got.Provider, got.Model)
	}
	if got.MaxRounds != 7 || got.MaxTokens != 3333 {
		t.Fatalf("visual config rounds/tokens = %d/%d", got.MaxRounds, got.MaxTokens)
	}
}
