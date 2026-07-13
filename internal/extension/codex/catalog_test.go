package codex_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ZhiYi-R/moon-bridge/internal/config"
	"github.com/ZhiYi-R/moon-bridge/internal/extension/codex"
	"github.com/ZhiYi-R/moon-bridge/internal/extension/visual"
)

func TestBuildModelInfoFromRouteEnablesApplyPatchFreeform(t *testing.T) {
	info := codex.BuildModelInfoFromRoute("gpt-test", "default", config.RouteEntry{
		ContextWindow: 100000,
	})
	if info.ApplyPatchToolType == nil || *info.ApplyPatchToolType != "freeform" {
		t.Fatalf("apply_patch_tool_type = %v", info.ApplyPatchToolType)
	}
	if info.TruncationPolicy.Mode != "tokens" || info.TruncationPolicy.Limit != codex.DefaultCatalogTruncationLimit {
		t.Fatalf("truncation_policy = %+v", info.TruncationPolicy)
	}
}

func TestBuildModelInfoFromRouteUsesTokenTruncationPolicy(t *testing.T) {
	info := codex.BuildModelInfoFromRoute("gpt-5.2", "default", config.RouteEntry{
		ContextWindow: 200000,
	})
	if info.TruncationPolicy.Mode != "tokens" || info.TruncationPolicy.Limit != codex.DefaultCatalogTruncationLimit {
		t.Fatalf("truncation_policy = %+v", info.TruncationPolicy)
	}
}

func TestBuildModelInfosFromConfigIncludesProviderModelsBeforeRouteFallback(t *testing.T) {
	models := codex.BuildModelInfosFromConfig(config.ProviderConfig{
		Providers: map[string]config.ProviderDef{
			"openai": {
				BaseURL: "https://api.openai.com",
				APIKey:  "sk-test",
				Models: map[string]config.ModelMeta{
					"gpt-4o": {},
				},
				Protocol: "openai-response",
			},
		},
	}, config.PluginConfig{})
	if len(models) != 1 {
		t.Fatalf("expected 1 model (model(provider) only), got %d", len(models))
	}
	if models[0].Slug != "gpt-4o" {
		t.Fatalf("slug[0] = %q, want gpt-4o", models[0].Slug)
	}
}

func TestBuildModelInfoPreservesReasoningLevels(t *testing.T) {
	info := codex.BuildModelInfoFromProviderModel("deepseek-v4-pro(deepseek)", "deepseek", config.ModelMeta{
		SupportsReasoning:     true,
		ContextWindow:         200000,
		DefaultReasoningLevel: "high",
		SupportedReasoningLevels: []config.ReasoningLevelPreset{
			{Effort: "high", Description: "High reasoning effort"},
			{Effort: "xhigh", Description: "Extra high reasoning effort"},
		},
	})
	if info.DefaultReasoningLevel != "high" {
		t.Fatalf("DefaultReasoningLevel = %q, want high", info.DefaultReasoningLevel)
	}
	if len(info.SupportedReasoningLevels) != 2 {
		t.Fatalf("SupportedReasoningLevels = %+v, want two levels", info.SupportedReasoningLevels)
	}
	if info.SupportedReasoningLevels[0].Effort != "high" || info.SupportedReasoningLevels[1].Effort != "xhigh" {
		t.Fatalf("SupportedReasoningLevels = %+v", info.SupportedReasoningLevels)
	}
}

func TestBuildModelInfoOmitsReasoningMetadataWhenUnsupported(t *testing.T) {
	info := codex.BuildModelInfoFromRoute("plain", "default", config.RouteEntry{
		SupportsReasoning:          false,
		DefaultReasoningLevel:      "medium",
		SupportedReasoningLevels:   []config.ReasoningLevelPreset{{Effort: "medium", Description: "Balanced"}},
		SupportsReasoningSummaries: true,
		DefaultReasoningSummary:    "auto",
	})

	if info.DefaultReasoningLevel != "" {
		t.Fatalf("DefaultReasoningLevel = %q, want empty", info.DefaultReasoningLevel)
	}
	if len(info.SupportedReasoningLevels) != 0 {
		t.Fatalf("SupportedReasoningLevels = %+v, want empty", info.SupportedReasoningLevels)
	}
	if info.SupportsReasoningSummaries {
		t.Fatal("SupportsReasoningSummaries = true, want false")
	}
	if info.DefaultReasoningSummary != "none" {
		t.Fatalf("DefaultReasoningSummary = %q, want none", info.DefaultReasoningSummary)
	}
}

func TestGenerateConfigTomlDoesNotSetServiceTier(t *testing.T) {
	cfg := config.Config{
		Routes: map[string]config.RouteEntry{
			"moonbridge": {
				Provider:      "openai",
				Model:         "gpt-5.4",
				ContextWindow: 200000,
			},
		},
	}
	var output bytes.Buffer
	err := codex.GenerateConfigToml(&output, "moonbridge", "http://127.0.0.1:38440/v1", "",
		config.ProviderFromGlobalConfig(&cfg), config.PluginFromGlobalConfig(&cfg), config.ServerFromGlobalConfig(&cfg))
	if err != nil {
		t.Fatalf("GenerateConfigToml() error = %v", err)
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

func TestGenerateConfigTomlIncludesDeepWikiMCPServer(t *testing.T) {
	cfg := config.Config{
		Routes: map[string]config.RouteEntry{
			"test": {Provider: "openai", Model: "gpt-4o"},
		},
	}
	var output bytes.Buffer
	err := codex.GenerateConfigToml(&output, "test", "http://127.0.0.1:38440/v1", "",
		config.ProviderFromGlobalConfig(&cfg), config.PluginFromGlobalConfig(&cfg), config.ServerFromGlobalConfig(&cfg))
	if err != nil {
		t.Fatalf("GenerateConfigToml() error = %v", err)
	}
	generated := output.String()
	if !strings.Contains(generated, "[mcp_servers.deepwiki]") {
		t.Fatalf("missing deepwiki MCP server in generated config:\n%s", generated)
	}
}

func TestBuildModelInfosFromConfig_DeduplicatesSameModelAcrossProviders(t *testing.T) {
	cfg := config.Config{
		ProviderDefs: map[string]config.ProviderDef{
			"provider-a": {
				Models: map[string]config.ModelMeta{
					"claude-sonnet-4-5": {ContextWindow: 100000, Description: "From A"},
				},
			},
			"provider-b": {
				Models: map[string]config.ModelMeta{
					"claude-sonnet-4-5": {ContextWindow: 200000, Description: "From B"},
				},
			},
		},
	}
	models := codex.BuildModelInfosFromConfig(config.ProviderFromGlobalConfig(&cfg), config.PluginConfig{})
	if len(models) != 1 {
		t.Fatalf("expected 1 deduplicated model, got %d", len(models))
	}
	if models[0].Slug != "claude-sonnet-4-5" {
		t.Fatalf("slug = %q, want claude-sonnet-4-5", models[0].Slug)
	}
	// Preferred provider (provider-a) has description "From A".
	if models[0].Description != "From A" {
		t.Fatalf("description = %q, want From A", models[0].Description)
	}
}

func TestBuildModelInfosFromConfig_DifferentModelsEmittedSeparately(t *testing.T) {
	cfg := config.Config{
		ProviderDefs: map[string]config.ProviderDef{
			"provider-a": {
				Models: map[string]config.ModelMeta{
					"model-alpha": {},
				},
			},
			"provider-b": {
				Models: map[string]config.ModelMeta{
					"model-beta": {},
				},
			},
		},
	}
	models := codex.BuildModelInfosFromConfig(config.ProviderFromGlobalConfig(&cfg), config.PluginConfig{})
	if len(models) != 2 {
		t.Fatalf("expected 2 distinct models, got %d", len(models))
	}
	if models[0].Slug != "model-alpha" || models[1].Slug != "model-beta" {
		t.Fatalf("slugs = %q / %q, want model-alpha / model-beta", models[0].Slug, models[1].Slug)
	}
}

func TestBuildModelInfosFromConfig_MetadataMergeTakesMaxContextWindow(t *testing.T) {
	cfg := config.Config{
		ProviderDefs: map[string]config.ProviderDef{
			"provider-a": {
				Models: map[string]config.ModelMeta{
					"model-x": {ContextWindow: 100000},
				},
			},
			"provider-b": {
				Models: map[string]config.ModelMeta{
					"model-x": {ContextWindow: 500000},
				},
			},
		},
	}
	models := codex.BuildModelInfosFromConfig(config.ProviderFromGlobalConfig(&cfg), config.PluginConfig{})
	if len(models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(models))
	}
	if models[0].ContextWindow == nil || *models[0].ContextWindow != 500000 {
		t.Fatalf("ContextWindow = %v, want 500000", models[0].ContextWindow)
	}
}

func TestBuildModelInfosFromConfig_MetadataMergeUnionModalities(t *testing.T) {
	cfg := config.Config{
		ProviderDefs: map[string]config.ProviderDef{
			"provider-a": {
				Models: map[string]config.ModelMeta{
					"model-x": {InputModalities: []string{"text"}},
				},
			},
			"provider-b": {
				Models: map[string]config.ModelMeta{
					"model-x": {InputModalities: []string{"text", "image"}},
				},
			},
		},
	}
	models := codex.BuildModelInfosFromConfig(config.ProviderFromGlobalConfig(&cfg), config.PluginConfig{})
	if len(models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(models))
	}
	// Union should include both text and image; order is sorted.
	if len(models[0].InputModalities) != 2 {
		t.Fatalf("InputModalities = %v, want 2 entries", models[0].InputModalities)
	}
	hasText, hasImage := false, false
	for _, m := range models[0].InputModalities {
		if m == "text" {
			hasText = true
		}
		if m == "image" {
			hasImage = true
		}
	}
	if !hasText || !hasImage {
		t.Fatalf("InputModalities = %v, missing text and/or image", models[0].InputModalities)
	}
}

func TestBuildModelInfosFromConfig_ReasoningLevelsDeduplicatedByEffort(t *testing.T) {
	cfg := config.Config{
		ProviderDefs: map[string]config.ProviderDef{
			"provider-a": {
				Models: map[string]config.ModelMeta{
					"model-x": {
						SupportsReasoning: true,
						SupportedReasoningLevels: []config.ReasoningLevelPreset{
							{Effort: "high", Description: "A High"},
							{Effort: "low", Description: "A Low"},
						},
					},
				},
			},
			"provider-b": {
				Models: map[string]config.ModelMeta{
					"model-x": {
						SupportsReasoning: true,
						SupportedReasoningLevels: []config.ReasoningLevelPreset{
							{Effort: "high", Description: "B High"},
							{Effort: "xhigh", Description: "B XHigh"},
						},
					},
				},
			},
		},
	}
	models := codex.BuildModelInfosFromConfig(config.ProviderFromGlobalConfig(&cfg), config.PluginConfig{})
	if len(models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(models))
	}
	levels := models[0].SupportedReasoningLevels
	if len(levels) != 3 {
		t.Fatalf("SupportedReasoningLevels = %+v, want 3 levels", levels)
	}
	// High should use preferred provider's description (A High).
	if levels[0].Effort != "high" || levels[0].Description != "A High" {
		t.Fatalf("levels[0] = %+v, want effort=high description=A High", levels[0])
	}
	// Low from preferred provider.
	if levels[1].Effort != "low" || levels[1].Description != "A Low" {
		t.Fatalf("levels[1] = %+v, want effort=low description=A Low", levels[1])
	}
	// Xhigh should come from provider-b since it's a new effort.
	if levels[2].Effort != "xhigh" || levels[2].Description != "B XHigh" {
		t.Fatalf("levels[2] = %+v, want effort=xhigh description=B XHigh", levels[2])
	}
}
func TestGenerateConfigTomlNormalizesDirectProviderModelRef(t *testing.T) {
	var output bytes.Buffer
	cfg := config.Config{
		ProviderDefs: map[string]config.ProviderDef{
			"deepseek": {
				Models: map[string]config.ModelMeta{
					"deepseek-v4-pro": {
						ContextWindow: 200000,
					},
				},
			},
		},
	}
	// No route for "deepseek/deepseek-v4-pro" — it is a direct provider/model reference.
	err := codex.GenerateConfigToml(&output, "deepseek/deepseek-v4-pro", "http://127.0.0.1:38440/v1", "",
		config.ProviderFromGlobalConfig(&cfg), config.PluginFromGlobalConfig(&cfg), config.ServerFromGlobalConfig(&cfg))
	if err != nil {
		t.Fatalf("GenerateConfigToml() error = %v", err)
	}
	generated := output.String()
	// Should normalize to model(provider) format so Codex can match catalog entries.
	if !strings.Contains(generated, `model = "deepseek-v4-pro(deepseek)"`) {
		t.Fatalf("expected normalized model slug, got:\n%s", generated)
	}
	// Context window should still be resolved from the provider catalog.
	if !strings.Contains(generated, "model_context_window = 200000") {
		t.Fatalf("expected context_window from provider meta, got:\n%s", generated)
	}
}

func TestGenerateConfigTomlModelProviderFormatInputStable(t *testing.T) {
	var output bytes.Buffer
	cfg := config.Config{
		ProviderDefs: map[string]config.ProviderDef{
			"deepseek": {
				Models: map[string]config.ModelMeta{
					"deepseek-v4-pro": {
						ContextWindow: 200000,
					},
				},
			},
		},
	}
	// Input already in model(provider) format — should remain unchanged.
	err := codex.GenerateConfigToml(&output, "deepseek-v4-pro(deepseek)", "http://127.0.0.1:38440/v1", "",
		config.ProviderFromGlobalConfig(&cfg), config.PluginFromGlobalConfig(&cfg), config.ServerFromGlobalConfig(&cfg))
	if err != nil {
		t.Fatalf("GenerateConfigToml() error = %v", err)
	}
	generated := output.String()
	if !strings.Contains(generated, `model = "deepseek-v4-pro(deepseek)"`) {
		t.Fatalf("expected model(provider) slug unchanged, got:\n%s", generated)
	}
}

func TestGenerateConfigTomlRouteAliasWithSlashNotNormalized(t *testing.T) {
	var output bytes.Buffer
	cfg := config.Config{
		ProviderDefs: map[string]config.ProviderDef{
			"p1": {
				Models: map[string]config.ModelMeta{
					"model-a": {
						ContextWindow: 1000,
					},
				},
			},
		},
		Routes: map[string]config.RouteEntry{
			"p1/model-a": {Provider: "p1", Model: "model-a", ContextWindow: 1000},
		},
	}
	// p1/model-a is an explicit route alias — should NOT be normalized.
	err := codex.GenerateConfigToml(&output, "p1/model-a", "http://127.0.0.1:38440/v1", "",
		config.ProviderFromGlobalConfig(&cfg), config.PluginFromGlobalConfig(&cfg), config.ServerFromGlobalConfig(&cfg))
	if err != nil {
		t.Fatalf("GenerateConfigToml() error = %v", err)
	}
	generated := output.String()
	if !strings.Contains(generated, `model = "p1/model-a"`) {
		t.Fatalf("expected route alias preserved as-is, got:\n%s", generated)
	}
}

func TestGenerateConfigTomlCodexHomeContainsNormalizedSlug(t *testing.T) {
	codexHome := t.TempDir()
	var output bytes.Buffer
	cfg := config.Config{
		ProviderDefs: map[string]config.ProviderDef{
			"deepseek": {
				Models: map[string]config.ModelMeta{
					"deepseek-v4-pro": {
						ContextWindow: 200000,
					},
				},
			},
		},
	}
	err := codex.GenerateConfigToml(&output, "deepseek/deepseek-v4-pro", "http://127.0.0.1:38440/v1", codexHome,
		config.ProviderFromGlobalConfig(&cfg), config.PluginFromGlobalConfig(&cfg), config.ServerFromGlobalConfig(&cfg))
	if err != nil {
		t.Fatalf("GenerateConfigToml() error = %v", err)
	}
	// Read the generated catalog and verify the normalized slug is present.
	raw, err := os.ReadFile(codexHome + "/models_catalog.json")
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.Contains(string(raw), `"slug": "deepseek-v4-pro"`) {
		t.Fatalf("catalog missing pure model name slug:\n%s", string(raw))
	}
	// The config should reference the normalized slug.
	generated := output.String()
	if !strings.Contains(generated, `model = "deepseek-v4-pro(deepseek)"`) {
		t.Fatalf("config missing normalized model slug:\n%s", generated)
	}
}

func TestWriteModelsCatalogProducesValidJSON(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/models_catalog.json"
	cfg := config.Config{
		ProviderDefs: map[string]config.ProviderDef{
			"openai": {
				BaseURL: "https://api.openai.com",
				APIKey:  "sk-test",
				Models: map[string]config.ModelMeta{
					"gpt-4o": {},
				},
				Protocol: "openai-response",
			},
		},
	}
	if err := codex.WriteModelsCatalog(path, config.ProviderFromGlobalConfig(&cfg), config.PluginConfig{}); err != nil {
		t.Fatalf("WriteModelsCatalog() error = %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	var result struct {
		Models []codex.ModelInfo `json:"models"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("JSON unmarshal error = %v", err)
	}
	if len(result.Models) == 0 {
		t.Fatal("expected at least 1 model in catalog")
	}
}

func TestGenerateConfigTomlCatalogIncludesVisualInjectedImageModality(t *testing.T) {
	enabled := true
	codexHome := t.TempDir()
	var output bytes.Buffer
	cfg := config.Config{
		Models: map[string]config.ModelDef{
			"gpt-text": {InputModalities: []string{"text"}},
		},
		ProviderDefs: map[string]config.ProviderDef{
			"openai": {
				Offers: []config.OfferEntry{{Model: "gpt-text"}},
			},
		},
		Routes: map[string]config.RouteEntry{
			"text-route": {Provider: "openai", Model: "gpt-text"},
		},
		Extensions: map[string]config.ExtensionSettings{
			visual.PluginName: {Enabled: &enabled},
		},
	}

	err := codex.GenerateConfigToml(&output, "text-route", "http://127.0.0.1:38440/v1", codexHome,
		config.ProviderFromGlobalConfig(&cfg), config.PluginFromGlobalConfig(&cfg), config.ServerFromGlobalConfig(&cfg))
	if err != nil {
		t.Fatalf("GenerateConfigToml() error = %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(codexHome, "models_catalog.json"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	var result struct {
		Models []codex.ModelInfo `json:"models"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("JSON unmarshal error = %v", err)
	}
	if len(result.Models) != 2 {
		t.Fatalf("expected provider model plus route alias, got %d", len(result.Models))
	}
	for _, model := range result.Models {
		hasImage := false
		for _, mod := range model.InputModalities {
			if mod == "image" {
				hasImage = true
				break
			}
		}
		if !hasImage {
			t.Fatalf("model %q missing injected image modality: %v", model.Slug, model.InputModalities)
		}
	}
}

func TestGenerateConfigTomlWritesAuthJSONWithOwnerOnlyPermissions(t *testing.T) {
	codexHome := t.TempDir()
	var output bytes.Buffer
	cfg := config.Config{
		Routes: map[string]config.RouteEntry{
			"secure": {Provider: "openai", Model: "gpt-4o"},
		},
		AuthToken: "secret-token",
	}

	err := codex.GenerateConfigToml(&output, "secure", "http://127.0.0.1:38440/v1", codexHome,
		config.ProviderFromGlobalConfig(&cfg), config.PluginFromGlobalConfig(&cfg), config.ServerFromGlobalConfig(&cfg))
	if err != nil {
		t.Fatalf("GenerateConfigToml() error = %v", err)
	}

	info, err := os.Stat(filepath.Join(codexHome, "auth.json"))
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Fatalf("auth.json perm = %04o, want 0600", perm)
	}
}

func TestBuildModelInfoFromProviderModelIncludesInputModalities(t *testing.T) {
	info := codex.BuildModelInfoFromProviderModel("gpt-4o(openai)", "openai", config.ModelMeta{
		ContextWindow:   128000,
		InputModalities: []string{"text", "image"},
	})
	if len(info.InputModalities) != 2 {
		t.Fatalf("InputModalities = %v, want [text image]", info.InputModalities)
	}
	if info.InputModalities[0] != "text" || info.InputModalities[1] != "image" {
		t.Fatalf("InputModalities = %v, want [text image]", info.InputModalities)
	}
}

func TestBuildModelInfoDefaultsInputModalitiesToText(t *testing.T) {
	info := codex.BuildModelInfoFromProviderModel("gpt-4o(openai)", "openai", config.ModelMeta{
		ContextWindow: 128000,
		// InputModalities not set; should default to ["text"]
	})
	if len(info.InputModalities) != 1 || info.InputModalities[0] != "text" {
		t.Fatalf("InputModalities = %v, want [text]", info.InputModalities)
	}
	if info.SupportsImageDetailOriginal {
		t.Fatal("SupportsImageDetailOriginal should be false by default")
	}
}

func TestBuildModelInfoFromRoutePropagatesInputModalities(t *testing.T) {
	info := codex.BuildModelInfoFromRoute("gpt-image", "openai", config.RouteEntry{
		Model:                       "gpt-4o",
		InputModalities:             []string{"text", "image"},
		SupportsImageDetailOriginal: true,
	})
	if len(info.InputModalities) != 2 || info.InputModalities[1] != "image" {
		t.Fatalf("InputModalities = %v, want [text image]", info.InputModalities)
	}
	if !info.SupportsImageDetailOriginal {
		t.Fatal("SupportsImageDetailOriginal should be true")
	}
}
func TestDisplayNameFromSlug(t *testing.T) {
	tests := []struct {
		slug string
		want string
	}{
		{"gpt-5.5", "GPT 5.5"},
		{"gpt-5.5-codex", "GPT 5.5 Codex"},
		{"gpt-4o", "GPT 4o"},
		{"deepseek-v4-pro", "Deepseek V4 Pro"},
		{"gp", "Gp"},
		{"g", "G"},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.slug, func(t *testing.T) {
			got := codex.DisplayNameFromSlug(tt.slug)
			if got != tt.want {
				t.Fatalf("displayNameFromSlug(%q) = %q, want %q", tt.slug, got, tt.want)
			}
		})
	}
}

func TestBuildModelInfoFromRouteExplicitDisplayName(t *testing.T) {
	info := codex.BuildModelInfoFromRoute("gpt-5.5", "openai", config.RouteEntry{
		DisplayName: "My Custom Name",
	})
	if info.DisplayName != "My Custom Name" {
		t.Fatalf("DisplayName = %q, want My Custom Name", info.DisplayName)
	}
	if info.Slug != "gpt-5.5" {
		t.Fatalf("Slug = %q, want gpt-5.5", info.Slug)
	}
}

func TestBuildModelInfoFromRouteAutoDisplayName(t *testing.T) {
	info := codex.BuildModelInfoFromRoute("gpt-5.5-codex", "openai", config.RouteEntry{})
	if info.DisplayName != "GPT 5.5 Codex" {
		t.Fatalf("DisplayName = %q, want GPT 5.5 Codex", info.DisplayName)
	}
}

func TestBuildModelInfoFromRouteDifferentAliasesSameModel(t *testing.T) {
	// Regression: multiple route aliases pointing to the same underlying model
	// must produce different DisplayNames derived from their alias slugs.
	aliases := []struct {
		alias string
		want  string
	}{
		{"gpt-5.4", "GPT 5.4"},
		{"gpt-5.5", "GPT 5.5"},
		{"codex-auto-review", "Codex Auto Review"},
		{"gpt-5.4-mini", "GPT 5.4 Mini"},
		{"gpt-5.3-codex", "GPT 5.3 Codex"},
	}
	for _, tc := range aliases {
		// RouteEntry with empty DisplayName (no explicit config) should fall back to slug.
		info := codex.BuildModelInfoFromRoute(tc.alias, "", config.RouteEntry{})
		if info.DisplayName != tc.want {
			t.Fatalf("BuildModelInfoFromRoute(%q) DisplayName = %q, want %q", tc.alias, info.DisplayName, tc.want)
		}
	}
}

func TestBuildModelInfoForProviderModelsPreserveDisplayName(t *testing.T) {
	// Provider model entries should keep their original display_name from model config.
	meta := config.ModelMeta{DisplayName: "DeepSeek V4 Pro"}
	info := codex.BuildModelInfoFromProviderModel("deepseek-v4-pro", "", meta)
	if info.DisplayName != "DeepSeek V4 Pro" {
		t.Fatalf("DisplayName = %q, want \"DeepSeek V4 Pro\"", info.DisplayName)
	}
}
