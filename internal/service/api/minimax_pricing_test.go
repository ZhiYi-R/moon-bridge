package api

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
	"moonbridge/internal/config"
	"moonbridge/internal/extension/plugin"
	"moonbridge/internal/service/provider"
	"moonbridge/internal/service/stats"
)

func TestMiniMaxExamplePricingUnknown(t *testing.T) {
	data, err := os.ReadFile("../../../config.example.yml")
	if err != nil {
		t.Fatal(err)
	}
	var fc config.FileConfig
	if err := yaml.Unmarshal(data, &fc); err != nil {
		t.Fatal(err)
	}
	fc.Extensions = nil
	for key, model := range fc.Models {
		model.Extensions = nil
		fc.Models[key] = model
	}
	delete(fc.Providers, "kimi") // Existing example references a commented-out model.
	encoded, err := yaml.Marshal(fc)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadFromYAML(encoded)
	if err != nil {
		t.Fatal(err)
	}
	pricing := provider.BuildPricingFromConfig(config.ProviderFromGlobalConfig(&cfg))
	for _, model := range []string{"minimax", "MiniMax-M3", "MiniMax-M3.1-Flash", "minimax-cn-anthropic/MiniMax-M3", "minimax-global-openai/MiniMax-M3.1-Flash"} {
		t.Run(model, func(t *testing.T) {
			session := stats.NewSessionStats()
			session.SetPricing(pricing)
			usage := stats.BillingUsage{FreshInputTokens: 1000000, OutputTokens: 1000000}
			session.RecordBilling(model, model, usage)
			summary := session.Summary()
			if summary.InputTokens != 1000000 || summary.OutputTokens != 1000000 || summary.CostValue() != nil || summary.UnpricedRequests != 1 {
				t.Fatalf("summary=%+v", summary)
			}
			if session.ComputeBillingCost(model, usage) != -1 {
				t.Fatal("missing price reported as numeric cost")
			}
			raw, err := json.Marshal(summary)
			if err != nil || !strings.Contains(string(raw), `"TotalCost":null`) || !strings.Contains(string(raw), `"CostUnknown":true`) {
				t.Fatalf("summary JSON=%s err=%v", raw, err)
			}
			checkUnknownUsageJSON(t, usageStatsFromSummary(summary))
		})
	}
	session := stats.NewSessionStats()
	session.SetPricing(map[string]stats.ModelPricing{"verified": {InputPrice: 2, OutputPrice: 8}})
	session.RecordBilling("verified", "verified", stats.BillingUsage{FreshInputTokens: 1000000, OutputTokens: 1000000})
	if cost := session.Summary().CostValue(); cost == nil || *cost != 10 {
		t.Fatalf("configured CNY contract cost=%v", cost)
	}
	session.RecordBilling("MiniMax-M3", "MiniMax-M3", stats.BillingUsage{FreshInputTokens: 600000})
	checkUnknownUsageJSON(t, usageStatsFromSummary(session.Summary()))
}

func TestPersistedUnknownUsageJSON(t *testing.T) {
	checkUnknownUsageJSON(t, usageStatsFromAggregate(plugin.UsageAggregate{
		Requests: 2, Cost: 10, UnpricedRequests: 1,
		ByModel: []plugin.UsageModelAggregate{{Model: "MiniMax-M3", Requests: 2, Cost: 10, UnpricedRequests: 1}},
	}))
}

func checkUnknownUsageJSON(t *testing.T, response usageStatsResponse) {
	t.Helper()
	raw, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Totals  map[string]any   `json:"totals"`
		ByModel []map[string]any `json:"by_model"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Totals["total_cost"] != nil || decoded.Totals["cost_unknown"] != true || decoded.Totals["currency"] != "CNY" {
		t.Fatalf("totals=%s", raw)
	}
	for _, row := range decoded.ByModel {
		if row["cost_unknown"] == true && (row["cost"] != nil || row["avg_cost_per_mtoken"] != nil) {
			t.Fatalf("unknown row contains numeric cost: %s", raw)
		}
	}
}
