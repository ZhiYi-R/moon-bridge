package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
	"moonbridge/internal/config"
)

func TestExampleConfigIncludesMiniMaxRegistry(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	example, err := os.ReadFile(filepath.Join(repoRoot, "config.example.yml"))
	if err != nil {
		t.Fatalf("read example config: %v", err)
	}
	var fileConfig config.FileConfig
	if err := yaml.Unmarshal(example, &fileConfig); err != nil {
		t.Fatalf("decode example config: %v", err)
	}
	for _, modelID := range []string{"MiniMax-M3", "MiniMax-M2.7"} {
		if _, ok := fileConfig.Models[modelID]; !ok {
			t.Fatalf("model %q is missing", modelID)
		}
	}

	providerTests := []struct {
		key      string
		protocol string
		baseURL  string
	}{
		{"minimax-global-anthropic", config.ProtocolAnthropic, "https://api.minimax.io/anthropic"},
		{"minimax-global-openai", config.ProtocolOpenAIChat, "https://api.minimax.io/v1"},
		{"minimax-cn-anthropic", config.ProtocolAnthropic, "https://api.minimaxi.com/anthropic"},
		{"minimax-cn-openai", config.ProtocolOpenAIChat, "https://api.minimaxi.com/v1"},
	}
	for _, tt := range providerTests {
		t.Run(tt.key, func(t *testing.T) {
			provider, ok := fileConfig.Providers[tt.key]
			if !ok {
				t.Fatalf("provider %q is missing", tt.key)
			}
			if provider.Protocol != tt.protocol {
				t.Fatalf("protocol = %q, want %q", provider.Protocol, tt.protocol)
			}
			if provider.BaseURL != tt.baseURL {
				t.Fatalf("base URL = %q, want %q", provider.BaseURL, tt.baseURL)
			}
			assertMiniMaxOffers(t, provider.Offers)
		})
	}

	for _, docsRoot := range []string{
		"https://platform.minimax.io/docs",
		"https://platform.minimaxi.com/docs",
	} {
		if !strings.Contains(string(example), docsRoot) {
			t.Fatalf("example config is missing documentation root %q", docsRoot)
		}
	}
}

func TestMiniMaxModelMetadata(t *testing.T) {
	tests := []struct {
		modelID       string
		contextWindow int
		modalities    []string
	}{
		{"MiniMax-M3", 1000000, []string{"text", "image", "video"}},
		{"MiniMax-M2.7", 204800, []string{"text"}},
	}

	for _, tt := range tests {
		t.Run(tt.modelID, func(t *testing.T) {
			path := filepath.Join("..", "..", "metadata", "models", tt.modelID+".json")
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read model metadata: %v", err)
			}
			var metadata config.ModelDefFileConfig
			if err := json.Unmarshal(raw, &metadata); err != nil {
				t.Fatalf("decode model metadata: %v", err)
			}
			if metadata.ContextWindow != tt.contextWindow {
				t.Fatalf("context window = %d, want %d", metadata.ContextWindow, tt.contextWindow)
			}
			if metadata.SupportsReasoning == nil || !*metadata.SupportsReasoning {
				t.Fatal("supports_reasoning = false, want true")
			}
			if !slices.Equal(metadata.InputModalities, tt.modalities) {
				t.Fatalf("input modalities = %v, want %v", metadata.InputModalities, tt.modalities)
			}
		})
	}
}

func assertMiniMaxOffers(t *testing.T, offers []config.OfferFileConfig) {
	t.Helper()
	want := map[string]config.ModelPricingFileConfig{
		"MiniMax-M3": {
			InputPrice:     0.3,
			OutputPrice:    1.2,
			CacheReadPrice: 0.06,
		},
		"MiniMax-M2.7": {
			InputPrice:      0.3,
			OutputPrice:     1.2,
			CacheWritePrice: 0.375,
			CacheReadPrice:  0.06,
		},
	}
	if len(offers) != len(want) {
		t.Fatalf("offer count = %d, want %d", len(offers), len(want))
	}
	for _, offer := range offers {
		pricing, ok := want[offer.Model]
		if !ok {
			t.Fatalf("unexpected model offer %q", offer.Model)
		}
		if offer.Pricing != pricing {
			t.Fatalf("pricing for %q = %+v, want %+v", offer.Model, offer.Pricing, pricing)
		}
	}
}
