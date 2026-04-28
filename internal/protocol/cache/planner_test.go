package cache

import (
	"testing"
	"time"
)

func TestCanonicalHashIsStableAcrossMapOrder(t *testing.T) {
	first, err := canonicalHash(map[string]any{
		"b": 2,
		"a": map[string]any{"z": true, "c": "ok"},
	})
	if err != nil {
		t.Fatalf("CanonicalHash(first) error = %v", err)
	}

	second, err := canonicalHash(map[string]any{
		"a": map[string]any{"c": "ok", "z": true},
		"b": 2,
	})
	if err != nil {
		t.Fatalf("CanonicalHash(second) error = %v", err)
	}

	if first != second {
		t.Fatalf("hash mismatch: %s != %s", first, second)
	}
}

func TestPlannerCreatesExplicitBreakpointsInPrefixOrder(t *testing.T) {
	planner := NewPlanner(PlannerConfig{
		Mode:                     "explicit",
		TTL:                      "1h",
		PromptCaching:            true,
		ExplicitCacheBreakpoints: true,
		MaxBreakpoints:           4,
		MinCacheTokens:           10,
		ExpectedReuse:            2,
		MinimumValueScore:        20,
	})

	plan, err := planner.Plan(PlanInput{
		ProviderID:            "anthropic",
		UpstreamAPIKeyID:      "key-1",
		Model:                 "claude-test",
		PromptCacheKey:        "tenant-docs",
		ToolsHash:             "tools-hash",
		SystemHash:            "system-hash",
		MessagePrefixHash:     "messages-hash",
		ToolCount:             2,
		SystemBlockCount:      1,
		MessageCount:          3,
		EstimatedTokens:       1000,
		EstimatedToolTokens:   2000,
		EstimatedSystemTokens: 1500,
	})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}

	if plan.Mode != "explicit" {
		t.Fatalf("Mode = %q", plan.Mode)
	}
	wantPaths := []string{"tools[1]", "system[0]", "messages[2].content[last]"}
	if len(plan.Breakpoints) != len(wantPaths) {
		t.Fatalf("breakpoints = %+v", plan.Breakpoints)
	}
	for index, want := range wantPaths {
		if got := plan.Breakpoints[index].BlockPath; got != want {
			t.Fatalf("breakpoint %d path = %q, want %q", index, got, want)
		}
	}
	if plan.LocalKey == "" {
		t.Fatal("LocalKey is empty")
	}
	if plan.PrefixKey == "" {
		t.Fatal("PrefixKey is empty")
	}
	if plan.PrefixKey == plan.LocalKey {
		t.Fatal("PrefixKey should differ from LocalKey when MessagePrefixHash is set")
	}
}

func TestPlannerAutomaticWithExplicitBreakpointsBecomesHybrid(t *testing.T) {
	planner := NewPlanner(PlannerConfig{
		Mode:                     "automatic",
		TTL:                      "5m",
		PromptCaching:            true,
		AutomaticPromptCache:     true,
		ExplicitCacheBreakpoints: true,
		MaxBreakpoints:           4,
		MinCacheTokens:           1,
	})

	plan, err := planner.Plan(PlanInput{
		ProviderID:            "anthropic",
		Model:                 "claude-test",
		ToolsHash:             "tools-hash",
		SystemHash:            "system-hash",
		MessagePrefixHash:     "messages-hash",
		ToolCount:             1,
		SystemBlockCount:      2,
		MessageCount:          3,
		EstimatedTokens:       1000,
		EstimatedToolTokens:   2000,
		EstimatedSystemTokens: 1500,
	})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}

	if plan.Mode != "hybrid" {
		t.Fatalf("Mode = %q, want hybrid", plan.Mode)
	}
	if len(plan.Breakpoints) != 3 {
		t.Fatalf("Breakpoints = %+v", plan.Breakpoints)
	}
}

func TestPlannerAutomaticCanDisableTopLevelCache(t *testing.T) {
	planner := NewPlanner(PlannerConfig{
		Mode:                     "automatic",
		TTL:                      "5m",
		PromptCaching:            true,
		AutomaticPromptCache:     false,
		ExplicitCacheBreakpoints: true,
		MaxBreakpoints:           4,
		MinCacheTokens:           1,
	})

	plan, err := planner.Plan(PlanInput{
		ProviderID:            "anthropic",
		Model:                 "claude-test",
		SystemHash:            "system-hash",
		MessagePrefixHash:     "messages-hash",
		SystemBlockCount:      1,
		MessageCount:          1,
		EstimatedTokens:       1000,
		EstimatedSystemTokens: 2000,
	})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}

	if plan.Mode != "explicit" {
		t.Fatalf("Mode = %q, want explicit", plan.Mode)
	}
	if len(plan.Breakpoints) != 2 {
		t.Fatalf("Breakpoints = %+v", plan.Breakpoints)
	}
}

func TestPlannerUsesRemainingBudgetForMessagePrefixes(t *testing.T) {
	planner := NewPlanner(PlannerConfig{
		Mode:                     "explicit",
		TTL:                      "5m",
		PromptCaching:            true,
		ExplicitCacheBreakpoints: true,
		MaxBreakpoints:           4,
		MinCacheTokens:           1,
	})

	plan, err := planner.Plan(PlanInput{
		ProviderID:            "anthropic",
		Model:                 "claude-test",
		ToolsHash:             "tools-hash",
		SystemHash:            "system-hash",
		MessagePrefixHash:     "messages-hash",
		ToolCount:             1,
		SystemBlockCount:      1,
		MessageCount:          6,
		EstimatedTokens:       5000,
		EstimatedToolTokens:   2000,
		EstimatedSystemTokens: 1500,
		MessageBreakpoints: []MessageBreakpointCandidate{
			{MessageIndex: 1, ContentIndex: 0, BlockPath: "messages[1].content[last]", Role: "user"},
			{MessageIndex: 3, ContentIndex: 0, BlockPath: "messages[3].content[last]", Role: "user"},
			{MessageIndex: 5, ContentIndex: 0, BlockPath: "messages[5].content[last]", Role: "user"},
		},
	})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}

	wantPaths := []string{
		"tools[0]",
		"system[0]",
		"messages[3].content[last]",
		"messages[5].content[last]",
	}
	if len(plan.Breakpoints) != len(wantPaths) {
		t.Fatalf("breakpoints = %+v", plan.Breakpoints)
	}
	for index, want := range wantPaths {
		if got := plan.Breakpoints[index].BlockPath; got != want {
			t.Fatalf("breakpoint %d path = %q, want %q", index, got, want)
		}
	}
}

func TestPlannerPrefersUserMessageBreakpoints(t *testing.T) {
	planner := NewPlanner(PlannerConfig{
		Mode:                     "explicit",
		TTL:                      "5m",
		PromptCaching:            true,
		ExplicitCacheBreakpoints: true,
		MaxBreakpoints:           1,
		MinCacheTokens:           1,
	})

	plan, err := planner.Plan(PlanInput{
		ProviderID:        "anthropic",
		Model:             "claude-test",
		MessagePrefixHash: "messages-hash",
		MessageCount:      3,
		EstimatedTokens:   4000,
		MessageBreakpoints: []MessageBreakpointCandidate{
			{MessageIndex: 1, ContentIndex: 0, BlockPath: "messages[1].content[last]", Role: "user"},
			{MessageIndex: 2, ContentIndex: 0, BlockPath: "messages[2].content[last]", Role: "assistant"},
		},
	})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}

	if len(plan.Breakpoints) != 1 {
		t.Fatalf("breakpoints = %+v", plan.Breakpoints)
	}
	if got := plan.Breakpoints[0].BlockPath; got != "messages[1].content[last]" {
		t.Fatalf("breakpoint path = %q", got)
	}
}

func TestPlannerSkipsShortPrefixes(t *testing.T) {
	planner := NewPlanner(PlannerConfig{
		Mode:           "automatic",
		TTL:            "5m",
		PromptCaching:  true,
		MinCacheTokens: 100,
	})

	plan, err := planner.Plan(PlanInput{
		ProviderID:      "anthropic",
		Model:           "claude-test",
		EstimatedTokens: 20,
	})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if plan.Mode != "off" || plan.Reason != "below_min_cache_tokens" {
		t.Fatalf("Plan() = %+v", plan)
	}
}

func TestRegistryUpdatesFromUsageSignals(t *testing.T) {
	registry := NewMemoryRegistry()

	registry.UpdateFromUsage("key", UsageSignals{CacheCreationInputTokens: 1200}, 1200)
	entry, ok := registry.Get("key")
	if !ok || entry.State != StateWarm {
		t.Fatalf("entry after creation = %+v, ok=%v", entry, ok)
	}

	registry.UpdateFromUsage("key", UsageSignals{CacheReadInputTokens: 900}, 100)
	entry, ok = registry.Get("key")
	if !ok || entry.CacheReadInputTokens != 900 {
		t.Fatalf("entry after read = %+v, ok=%v", entry, ok)
	}

	registry.UpdateFromUsage("short", UsageSignals{}, 5)
	entry, ok = registry.Get("short")
	if !ok || entry.State != StateNotCacheable {
		t.Fatalf("short entry = %+v, ok=%v", entry, ok)
	}
}

func TestRegistryTTLPassthrough(t *testing.T) {
	registry := NewMemoryRegistry()

	registry.UpdateFromUsage("key-1h", UsageSignals{CacheCreationInputTokens: 500}, 500, time.Hour)
	entry, ok := registry.Get("key-1h")
	if !ok || entry.State != StateWarm {
		t.Fatalf("entry = %+v, ok=%v", entry, ok)
	}
	// ExpiresAt should be ~1h from now, not 5m
	if time.Until(entry.ExpiresAt) < 50*time.Minute {
		t.Fatalf("ExpiresAt too soon: %v", entry.ExpiresAt)
	}
}

func TestBreakpointSkipsLowTokenScope(t *testing.T) {
	planner := NewPlanner(PlannerConfig{
		Mode:                     "explicit",
		TTL:                      "5m",
		PromptCaching:            true,
		ExplicitCacheBreakpoints: true,
		MaxBreakpoints:           4,
		MinCacheTokens:           1,
	})

	plan, err := planner.Plan(PlanInput{
		ProviderID:            "anthropic",
		Model:                 "claude-test",
		ToolsHash:             "tools-hash",
		SystemHash:            "system-hash",
		MessagePrefixHash:     "messages-hash",
		ToolCount:             1,
		SystemBlockCount:      1,
		MessageCount:          2,
		EstimatedTokens:       3000,
		EstimatedToolTokens:   50, // below 1024 minimum
		EstimatedSystemTokens: 50, // below 1024 minimum (combined also < 1024)
		MessageBreakpoints: []MessageBreakpointCandidate{
			{MessageIndex: 0, ContentIndex: 0, BlockPath: "messages[0].content[last]", Role: "user"},
		},
	})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}

	// Tools and system should be skipped; only message breakpoint should remain
	if len(plan.Breakpoints) != 1 {
		t.Fatalf("expected 1 breakpoint (messages only), got %+v", plan.Breakpoints)
	}
	if plan.Breakpoints[0].Scope != "messages" {
		t.Fatalf("expected messages scope, got %q", plan.Breakpoints[0].Scope)
	}
}
