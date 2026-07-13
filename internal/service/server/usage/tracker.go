// Package usage provides token usage tracking and billing computation.
//
// It extracts usage tracking from the Server god object into an independent
// service sub-package with a clean Tracker interface.
package usage

import (
	"github.com/ZhiYi-R/moon-bridge/internal/service/stats"
)

// Tracker is the interface for recording request usage and billing.
type Tracker interface {
	// RecordBilling records billing-level usage for cost computation.
	RecordBilling(model, actualModel string, usage stats.BillingUsage)

	// CostForRequest returns the computed cost for a request given billing usage.
	CostForRequest(requestModel, actualModel, providerKey string, usage stats.BillingUsage) float64
}

// ConfigAccessor provides pricing configuration to the tracker.
type ConfigAccessor interface {
	ModelPricing(model string) (stats.ModelPricing, bool)
}

// StatsTracker implements Tracker by delegating to a *stats.SessionStats instance.
type StatsTracker struct {
	stats *stats.SessionStats
}

// NewStatsTracker creates a new StatsTracker that records usage into the given stats.
func NewStatsTracker(s *stats.SessionStats) *StatsTracker {
	return &StatsTracker{stats: s}
}

// RecordBilling records billing-level usage into the stats collector.
func (t *StatsTracker) RecordBilling(model, actualModel string, usage stats.BillingUsage) {
	if t.stats == nil {
		return
	}
	t.stats.RecordBilling(model, actualModel, usage)
}

// CostForRequest computes the cost for a request using the stats collector's pricing.
func (t *StatsTracker) CostForRequest(requestModel, actualModel, providerKey string, usage stats.BillingUsage) float64 {
	if t.stats == nil {
		return 0
	}
	return t.stats.ComputeBillingCost(requestModel, usage)
}
