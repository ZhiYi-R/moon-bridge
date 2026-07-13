package configgraph

import (
	"strings"
	"testing"

	"github.com/ZhiYi-R/moon-bridge/internal/config"
)

func TestApplyPatchToFileConfigDoesNotHandleBaseRevision(t *testing.T) {
	fc := testConfig().ToFileConfig()
	request := PatchRequest{
		BaseRevision: "",
		Changes: []PatchOp{
			{Kind: ResourceDefaults, ID: mainResourceID, Field: "max_tokens", Value: 8192},
		},
	}

	patched, errs := ApplyPatchToFileConfig(fc, request.Changes)

	if len(errs) != 0 {
		t.Fatalf("ApplyPatchToFileConfig returned errors for empty base revision boundary: %+v", errs)
	}
	if patched.Defaults.MaxTokens != 8192 {
		t.Fatalf("Defaults.MaxTokens = %d, want 8192", patched.Defaults.MaxTokens)
	}
}

func TestApplyPatchToFileConfigUpdatesDefaultsMaxTokens(t *testing.T) {
	fc := testConfig().ToFileConfig()

	patched, errs := ApplyPatchToFileConfig(fc, []PatchOp{
		{Kind: ResourceDefaults, ID: mainResourceID, Field: "max_tokens", Value: 16384},
	})

	if len(errs) != 0 {
		t.Fatalf("ApplyPatchToFileConfig returned errors: %+v", errs)
	}
	if patched.Defaults.MaxTokens != 16384 {
		t.Fatalf("Defaults.MaxTokens = %d, want 16384", patched.Defaults.MaxTokens)
	}
}

func TestApplyPatchToFileConfigUpdatesModelReasoningSupport(t *testing.T) {
	fc := testConfig().ToFileConfig()

	patched, errs := ApplyPatchToFileConfig(fc, []PatchOp{
		{Kind: ResourceModel, ID: "claude-sonnet", Field: "supports_reasoning", Value: false},
	})

	if len(errs) != 0 {
		t.Fatalf("ApplyPatchToFileConfig returned errors: %+v", errs)
	}
	if patched.Models["claude-sonnet"].SupportsReasoning == nil {
		t.Fatal("Models[claude-sonnet].SupportsReasoning = nil, want explicit false")
	}
	if *patched.Models["claude-sonnet"].SupportsReasoning {
		t.Fatal("Models[claude-sonnet].SupportsReasoning = true, want false")
	}
}

func TestApplyPatchToFileConfigClearsProviderOfferPricing(t *testing.T) {
	fc := testConfig().ToFileConfig()

	patched, errs := ApplyPatchToFileConfig(fc, []PatchOp{
		{Kind: ResourceProviderOffer, ID: "anthropic/claude-sonnet", Field: "pricing", Value: nil},
	})

	if len(errs) != 0 {
		t.Fatalf("ApplyPatchToFileConfig returned errors: %+v", errs)
	}
	pricing := patched.Providers["anthropic"].Offers[0].Pricing
	if pricing.InputPrice != 0 || pricing.OutputPrice != 0 || pricing.CacheWritePrice != 0 || pricing.CacheReadPrice != 0 {
		t.Fatalf("Pricing = %+v, want zero-value pricing after clearing", pricing)
	}
	data, err := patched.MarshalYAML()
	if err != nil {
		t.Fatalf("MarshalYAML() error = %v", err)
	}
	if strings.Contains(string(data), "pricing:") {
		t.Fatalf("MarshalYAML() retained pricing after clearing:\n%s", string(data))
	}
	cfg, err := config.FromFileConfig(patched)
	if err != nil {
		t.Fatalf("config.FromFileConfig() error = %v", err)
	}
	offer := assertResource(t, BuildGraph(cfg, "rev-2"), ResourceProviderOffer, "anthropic/claude-sonnet")
	if _, ok := offer.Value["pricing"]; ok {
		t.Fatalf("BuildGraph() exposed cleared pricing: %+v", offer.Value["pricing"])
	}
}

func TestApplyPatchToFileConfigKeepsExistingProviderSecretWhenMaskedOrEmpty(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value any
	}{
		{name: "masked", value: secretMask},
		{name: "empty", value: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fc := testConfig().ToFileConfig()

			patched, errs := ApplyPatchToFileConfig(fc, []PatchOp{
				{Kind: ResourceProvider, ID: "anthropic", Field: "api_key", Value: tc.value},
			})

			if len(errs) != 0 {
				t.Fatalf("ApplyPatchToFileConfig returned errors: %+v", errs)
			}
			if patched.Providers["anthropic"].APIKey != "sk-ant-test-key" {
				t.Fatalf("Providers[anthropic].APIKey = %q, want existing secret", patched.Providers["anthropic"].APIKey)
			}
		})
	}
}

func TestApplyPatchToFileConfigPreservesWebSearchExtraFields(t *testing.T) {
	fc := testConfig().ToFileConfig()
	fc.Models["claude-sonnet"] = config.ModelDefFileConfig{
		DisplayName: "Claude Sonnet",
		WebSearch: config.WebSearchFileConfig{
			Support: "auto",
			MaxUses: 8,
			Extra: map[string]any{
				"provider_tool": "preview",
			},
		},
	}

	patched, errs := ApplyPatchToFileConfig(fc, []PatchOp{
		{Kind: ResourceModel, ID: "claude-sonnet", Field: "web_search", Value: map[string]any{
			"support":       "disabled",
			"provider_tool": "preview",
		}},
	})

	if len(errs) != 0 {
		t.Fatalf("ApplyPatchToFileConfig returned errors: %+v", errs)
	}
	if patched.Models["claude-sonnet"].WebSearch.Support != "disabled" {
		t.Fatalf("WebSearch.Support = %q, want disabled", patched.Models["claude-sonnet"].WebSearch.Support)
	}
	if patched.Models["claude-sonnet"].WebSearch.Extra["provider_tool"] != "preview" {
		t.Fatalf("WebSearch.Extra[provider_tool] = %#v, want preview", patched.Models["claude-sonnet"].WebSearch.Extra["provider_tool"])
	}
}

func TestApplyPatchToFileConfigPreservesExtensionExtraFields(t *testing.T) {
	enabled := true
	fc := testConfig().ToFileConfig()
	fc.Models["claude-sonnet"] = config.ModelDefFileConfig{
		DisplayName: "Claude Sonnet",
		Extensions: map[string]config.ExtensionFileConfig{
			"visual": {
				Enabled: &enabled,
				Config:  map[string]any{"model": "gpt-4.1"},
				Extra:   map[string]any{"scope_note": "keep"},
			},
		},
	}

	patched, errs := ApplyPatchToFileConfig(fc, []PatchOp{
		{Kind: ResourceModel, ID: "claude-sonnet", Field: "extensions", Value: map[string]any{
			"visual": map[string]any{
				"enabled":    false,
				"config":     map[string]any{"model": "gpt-4.1"},
				"scope_note": "keep",
			},
		}},
	})

	if len(errs) != 0 {
		t.Fatalf("ApplyPatchToFileConfig returned errors: %+v", errs)
	}
	visual := patched.Models["claude-sonnet"].Extensions["visual"]
	if visual.Enabled == nil || *visual.Enabled {
		t.Fatalf("visual.Enabled = %v, want explicit false", visual.Enabled)
	}
	if visual.Extra["scope_note"] != "keep" {
		t.Fatalf("visual.Extra[scope_note] = %#v, want keep", visual.Extra["scope_note"])
	}
}

func TestApplyPatchToFileConfigLeavesRouteReferenceValidationToConfigLoader(t *testing.T) {
	fc := testConfig().ToFileConfig()

	patched, errs := ApplyPatchToFileConfig(fc, []PatchOp{
		{Kind: ResourceRoute, ID: "claude-sonnet", Field: "provider", Value: "missing-provider"},
	})

	if len(errs) != 0 {
		t.Fatalf("ApplyPatchToFileConfig returned errors: %+v", errs)
	}
	if patched.Routes["claude-sonnet"].Provider != "missing-provider" {
		t.Fatalf("Routes[claude-sonnet].Provider = %q, want missing-provider", patched.Routes["claude-sonnet"].Provider)
	}
	if _, err := config.FromFileConfig(patched); err == nil {
		t.Fatal("config.FromFileConfig succeeded for route with missing provider, want validation error")
	}
}

func TestApplyPatchToFileConfigRejectsUnsupportedRoutePriority(t *testing.T) {
	fc := testConfig().ToFileConfig()

	_, errs := ApplyPatchToFileConfig(fc, []PatchOp{
		{Kind: ResourceRoute, ID: "claude-sonnet", Field: "priority", Value: 1},
	})

	if len(errs) != 1 {
		t.Fatalf("ApplyPatchToFileConfig returned %d errors, want 1: %+v", len(errs), errs)
	}
	if errs[0].ResourceKind != ResourceRoute || errs[0].ResourceID != "claude-sonnet" || errs[0].Field != "priority" {
		t.Fatalf("unexpected error target: %+v", errs[0])
	}
}
