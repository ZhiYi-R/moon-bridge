package codex_test

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"moonbridge/internal/extension/codex"
	"moonbridge/internal/foundation/config"
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
	models := codex.BuildModelInfosFromConfig(config.Config{
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
	})
	if len(models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(models))
	}
	if models[0].Slug != "gpt-4o(openai)" {
		t.Fatalf("slug = %q, want gpt-4o(openai)", models[0].Slug)
	}
}

func TestBuildModelInfoPreservesReasoningLevels(t *testing.T) {
	info := codex.BuildModelInfoFromProviderModel("deepseek-v4-pro(deepseek)", "deepseek", config.ModelMeta{
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

func TestGenerateConfigTomlDoesNotSetServiceTier(t *testing.T) {
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
	var output bytes.Buffer
	err := codex.GenerateConfigToml(&output, "test", "http://127.0.0.1:38440/v1", "", config.Config{
		Routes: map[string]config.RouteEntry{
			"test": {Provider: "openai", Model: "gpt-4o"},
		},
	})
	if err != nil {
		t.Fatalf("GenerateConfigToml() error = %v", err)
	}
	generated := output.String()
	if !strings.Contains(generated, "[mcp_servers.deepwiki]") {
		t.Fatalf("missing deepwiki MCP server in generated config:\n%s", generated)
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
	if err := codex.WriteModelsCatalog(path, cfg); err != nil {
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
