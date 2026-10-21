package metrics_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	mbtrics "moonbridge/internal/extension/metrics"
)

func TestUnknownCostPersistsWithoutEnteringAggregates(t *testing.T) {
	store := newMetricsStore(t)
	for _, record := range []mbtrics.Record{
		{Timestamp: time.Now(), Model: "mixed", InputTokens: 1000000, Cost: 10},
		{Timestamp: time.Now(), Model: "mixed", InputTokens: 600000, CostUnknown: true},
	} {
		if err := store.Record(record); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := store.Query(mbtrics.QueryOptions{})
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows=%v err=%v", rows, err)
	}
	raw, err := json.Marshal(rows[0])
	if err != nil || !strings.Contains(string(raw), `"cost":null`) || !strings.Contains(string(raw), `"cost_unknown":true`) {
		t.Fatalf("record=%s err=%v", raw, err)
	}
	agg, err := store.AggregateUsage(time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if agg.Cost != 10 || agg.UnpricedRequests != 1 || agg.Requests != 2 || agg.InputTokens != 1600000 || len(agg.ByModel) != 1 || agg.ByModel[0].UnpricedRequests != 1 {
		t.Fatalf("aggregate=%+v", agg)
	}
}
