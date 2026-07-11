package usage_test

import (
	"testing"

	"moonbridge/internal/service/server/usage"
	"moonbridge/internal/service/stats"
)

func newStatsWithPricing(model string, p stats.ModelPricing) *stats.SessionStats {
	s := stats.NewSessionStats()
	s.SetPricing(map[string]stats.ModelPricing{model: p})
	return s
}

func TestStatsTrackerRecordBilling(t *testing.T) {
	s := newStatsWithPricing("alias", stats.ModelPricing{InputPrice: 1, OutputPrice: 2})
	tr := usage.NewStatsTracker(s)

	tr.RecordBilling("alias", "upstream-model", stats.BillingUsage{
		FreshInputTokens: 1_000_000,
		OutputTokens:     500_000,
	})

	summary := s.Summary()
	if summary.Requests != 1 {
		t.Fatalf("Requests = %d, want 1", summary.Requests)
	}
	if summary.InputTokens != 1_000_000 {
		t.Errorf("InputTokens = %d, want 1000000", summary.InputTokens)
	}
	if summary.OutputTokens != 500_000 {
		t.Errorf("OutputTokens = %d, want 500000", summary.OutputTokens)
	}
	// 1M fresh input * 1/1M + 500k output * 2/1M = 1 + 1 = 2
	if got := summary.TotalCost; got != 2 {
		t.Errorf("TotalCost = %v, want 2", got)
	}
	if summary.ActualModelNames["alias"] != "upstream-model" {
		t.Errorf("ActualModelNames[alias] = %q, want upstream-model", summary.ActualModelNames["alias"])
	}
}

func TestStatsTrackerRecordBillingNilStats(t *testing.T) {
	tr := usage.NewStatsTracker(nil)
	// Should be a no-op and must not panic.
	tr.RecordBilling("alias", "upstream", stats.BillingUsage{FreshInputTokens: 10})
}

func TestStatsTrackerCostForRequest(t *testing.T) {
	s := newStatsWithPricing("alias", stats.ModelPricing{
		InputPrice:      3,
		OutputPrice:     6,
		CacheWritePrice: 4,
		CacheReadPrice:  1,
	})
	tr := usage.NewStatsTracker(s)

	cost := tr.CostForRequest("alias", "upstream", "provider", stats.BillingUsage{
		FreshInputTokens:         1_000_000,
		OutputTokens:             1_000_000,
		CacheCreationInputTokens: 1_000_000,
		CacheReadInputTokens:     1_000_000,
	})

	// 3 + 6 + 4 + 1 = 14
	if cost != 14 {
		t.Errorf("CostForRequest = %v, want 14", cost)
	}

	// CostForRequest must not record anything into the stats.
	if reqs := s.Summary().Requests; reqs != 0 {
		t.Errorf("CostForRequest should not record usage, Requests = %d", reqs)
	}
}

func TestStatsTrackerCostForRequestUnknownModel(t *testing.T) {
	s := newStatsWithPricing("alias", stats.ModelPricing{InputPrice: 3})
	tr := usage.NewStatsTracker(s)

	if cost := tr.CostForRequest("unknown", "upstream", "provider", stats.BillingUsage{FreshInputTokens: 1_000_000}); cost != 0 {
		t.Errorf("CostForRequest for unknown model = %v, want 0", cost)
	}
}

func TestStatsTrackerCostForRequestNilStats(t *testing.T) {
	tr := usage.NewStatsTracker(nil)
	if cost := tr.CostForRequest("alias", "upstream", "provider", stats.BillingUsage{FreshInputTokens: 1_000_000}); cost != 0 {
		t.Errorf("CostForRequest with nil stats = %v, want 0", cost)
	}
}

// Ensure StatsTracker satisfies the Tracker interface.
var _ usage.Tracker = (*usage.StatsTracker)(nil)
