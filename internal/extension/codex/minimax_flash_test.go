package codex

import (
	"testing"

	"moonbridge/internal/config"
)

func TestMiniMaxFlashCatalogOrder(t *testing.T) {
	cfg := config.ProviderConfig{
		Models: map[string]config.ModelDef{
			"MiniMax-M3": {}, "MiniMax-M3.1-Flash": {}, "other-model": {},
		},
		Providers: map[string]config.ProviderDef{
			"minimax": {Offers: []config.OfferEntry{{Model: "MiniMax-M3"}, {Model: "MiniMax-M3.1-Flash"}}},
			"other":   {Offers: []config.OfferEntry{{Model: "other-model"}}},
		},
	}
	for i := 0; i < 10; i++ {
		models := BuildModelInfosFromConfig(cfg, config.PluginConfig{})
		if len(models) != 3 || models[0].Slug != "MiniMax-M3.1-Flash" || models[1].Slug != "MiniMax-M3" || models[2].Slug != "other-model" {
			t.Fatalf("catalog order = %+v", models)
		}
		if models[0].Priority >= models[1].Priority {
			t.Fatalf("Flash priority must precede M3: %+v", models)
		}
	}
}
