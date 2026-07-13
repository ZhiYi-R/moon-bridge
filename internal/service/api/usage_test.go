package api

import (
	"testing"
	"time"

	"github.com/ZhiYi-R/moon-bridge/internal/extension/plugin"
)

func TestUsageWindow(t *testing.T) {
	t.Run("no params stays in session scope", func(t *testing.T) {
		if _, _, cross := usageWindow("", "", ""); cross {
			t.Fatal("expected session scope (cross=false) with no params")
		}
	})
	t.Run("explicit session range stays in session scope", func(t *testing.T) {
		if _, _, cross := usageWindow("session", "", ""); cross {
			t.Fatal("expected session scope for range=session")
		}
	})
	t.Run("unknown range falls back to session scope", func(t *testing.T) {
		if _, _, cross := usageWindow("bogus", "", ""); cross {
			t.Fatal("expected session scope for an unknown range")
		}
	})
	t.Run("24h is a ~24h cross-session window", func(t *testing.T) {
		since, until, cross := usageWindow("24h", "", "")
		if !cross {
			t.Fatal("expected cross-session for range=24h")
		}
		if d := until.Sub(since); d < 23*time.Hour || d > 25*time.Hour {
			t.Fatalf("expected ~24h window, got %s", d)
		}
	})
	t.Run("all opens the lower bound", func(t *testing.T) {
		since, _, cross := usageWindow("all", "", "")
		if !cross || !since.IsZero() {
			t.Fatalf("expected open-ended cross-session window, cross=%v since=%v", cross, since)
		}
	})
	t.Run("explicit since/until is cross-session", func(t *testing.T) {
		since, until, cross := usageWindow("", "2026-01-01T00:00:00Z", "2026-01-02T00:00:00Z")
		if !cross {
			t.Fatal("expected cross-session for explicit timestamps")
		}
		if since.IsZero() || until.IsZero() {
			t.Fatalf("expected parsed since/until, got since=%v until=%v", since, until)
		}
	})
}

func TestUsageStatsFromAggregate(t *testing.T) {
	earliest := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	latest := earliest.Add(2 * time.Hour)
	agg := plugin.UsageAggregate{
		Requests:      10,
		InputTokens:   1000,
		OutputTokens:  500,
		CacheCreation: 200,
		CacheRead:     300,
		Cost:          1.5,
		Earliest:      earliest,
		Latest:        latest,
		ByModel: []plugin.UsageModelAggregate{
			{Model: "cheap", ActualModel: "c", Requests: 4, InputTokens: 400, OutputTokens: 200, CacheRead: 100, Cost: 0.4},
			{Model: "pricey", ActualModel: "p", Requests: 6, InputTokens: 600, OutputTokens: 300, CacheRead: 200, Cost: 1.1},
		},
	}

	resp := usageStatsFromAggregate(agg)

	if resp.Totals.Requests != 10 || resp.Totals.InputTokens != 1000 {
		t.Fatalf("unexpected totals: %+v", resp.Totals)
	}
	if resp.Totals.TotalCost != 1.5 {
		t.Fatalf("expected total cost 1.5, got %v", resp.Totals.TotalCost)
	}
	// cache hit rate = cacheRead / inputTokens * 100 = 300/1000*100 = 30
	if resp.Totals.CacheHitRate != 30 {
		t.Fatalf("expected cache hit rate 30, got %v", resp.Totals.CacheHitRate)
	}
	// duration = latest - earliest = 2h
	if resp.Totals.Duration != "2h0m0s" {
		t.Fatalf("expected duration 2h0m0s, got %s", resp.Totals.Duration)
	}
	// rows are sorted by cost descending, so the pricier model comes first.
	if len(resp.ByModel) != 2 || resp.ByModel[0].Model != "pricey" {
		t.Fatalf("expected pricey model first, got %+v", resp.ByModel)
	}
}

func TestUsageStatsFromAggregateEmpty(t *testing.T) {
	resp := usageStatsFromAggregate(plugin.UsageAggregate{})
	if resp.Totals.Requests != 0 || resp.Totals.CacheHitRate != 0 {
		t.Fatalf("expected zeroed totals, got %+v", resp.Totals)
	}
	if resp.Totals.Duration != "0s" {
		t.Fatalf("expected 0s duration, got %s", resp.Totals.Duration)
	}
	if len(resp.ByModel) != 0 {
		t.Fatalf("expected no model rows, got %d", len(resp.ByModel))
	}
}
