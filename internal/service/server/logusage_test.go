package server

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/ZhiYi-R/moon-bridge/internal/logger"
	"github.com/ZhiYi-R/moon-bridge/internal/service/stats"
)

// resetLoggerForTest re-initialises the global logger with a JSON handler
// writing to the given buffer. Call the returned func in defer to restore
// default stderr logger.
func resetLoggerForTest(buf *bytes.Buffer) func() {
	logger.Init(logger.Config{Level: logger.LevelInfo, Format: "json", Output: buf})
	return func() {
		logger.Init(logger.Config{Level: logger.LevelInfo, Format: "text"})
	}
}

func parseSlogJSON(line string) map[string]any {
	var m map[string]any
	json.Unmarshal([]byte(line), &m)
	return m
}

func findSlogEntry(buf *bytes.Buffer, msg string) map[string]any {
	for _, line := range strings.Split(buf.String(), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		m := parseSlogJSON(line)
		if m["msg"] == msg {
			return m
		}
	}
	return nil
}

func assertField(t *testing.T, entry map[string]any, key string, want any) {
	t.Helper()
	got, ok := entry[key]
	if !ok {
		t.Fatalf("missing field %q in entry: %v", key, entry)
	}
	switch w := want.(type) {
	case float64:
		g, ok := got.(float64)
		if !ok {
			t.Fatalf("field %q: want float64 %v, got %T %v", key, w, got, got)
		}
		if math.Abs(g-w) > 0.01 {
			t.Fatalf("field %q: want %v, got %v", key, w, g)
		}
	case int:
		g, ok := got.(float64) // JSON numbers decode as float64
		if !ok {
			t.Fatalf("field %q: want number %v, got %T %v", key, w, got, got)
		}
		if int(g) != w {
			t.Fatalf("field %q: want %v, got %v", key, w, g)
		}
	case string:
		if got != w {
			t.Fatalf("field %q: want %q, got %v", key, w, got)
		}
	default:
		if got != w {
			t.Fatalf("field %q: want %v, got %v", key, w, got)
		}
	}
}

func TestLogUsageLine_nilSessionStats(t *testing.T) {
	var buf bytes.Buffer
	restore := resetLoggerForTest(&buf)
	defer restore()

	logUsageLine("test-model", "test-actual", stats.Usage{}, nil)

	entry := findSlogEntry(&buf, "请求完成")
	if entry == nil {
		t.Fatalf("expected slog entry with msg '请求完成', got: %s", buf.String())
	}
	assertField(t, entry, "request_model", "test-model")
	assertField(t, entry, "actual_model", "test-actual")
	assertField(t, entry, "input_fresh", 0)
	assertField(t, entry, "output_tokens", 0)
	assertField(t, entry, "request_cost", 0.0)
	assertField(t, entry, "total_cost", 0.0)
}

func TestLogUsageLine_withSessionStats(t *testing.T) {
	var buf bytes.Buffer
	restore := resetLoggerForTest(&buf)
	defer restore()

	s := stats.NewSessionStats()
	s.SetPricing(map[string]stats.ModelPricing{
		"test-model": {InputPrice: 10, OutputPrice: 40, CacheWritePrice: 5, CacheReadPrice: 2},
	})
	// Record 1: Input=1M, Output=500K, CacheCreation=400K, CacheRead=300K
	//   FreshInput = 1M - 400K - 300K = 300K
	//   Cost = 300K*10/1M + 400K*5/1M + 300K*2/1M + 500K*40/1M = 3.0 + 2.0 + 0.6 + 20.0 = 25.6
	s.Record("test-model", "test-actual", stats.Usage{
		InputTokens:              1_000_000,
		OutputTokens:             500_000,
		CacheCreationInputTokens: 400_000,
		CacheReadInputTokens:     300_000,
	})
	// Record 2: Input=2M, Output=1M, CacheCreation=100K, CacheRead=600K
	//   FreshInput = 2M - 100K - 600K = 1.3M
	//   Cost = 1300K*10/1M + 100K*5/1M + 600K*2/1M + 1M*40/1M = 13.0 + 0.5 + 1.2 + 40.0 = 54.7
	s.Record("test-model", "test-actual", stats.Usage{
		InputTokens:              2_000_000,
		OutputTokens:             1_000_000,
		CacheCreationInputTokens: 100_000,
		CacheReadInputTokens:     600_000,
	})

	// Current request: Input=100K, Output=50K, CacheCreation=10K, CacheRead=20K
	//   FreshInput = 100K - 10K - 20K = 70K
	//   requestCost = 70K*10/1M + 10K*5/1M + 20K*2/1M + 50K*40/1M = 0.7 + 0.05 + 0.04 + 2.0 = 2.79
	//   totalCost = 25.6 + 54.7 = 80.3
	//   cache_hit_rate = 900K / 3M * 100 = 30.0
	//   cache_write_rate = 500K / 3M * 100 = 16.667
	//   cache_rw_ratio = 20K / 10K = 2.0
	logUsageLine("test-model", "test-actual", stats.Usage{
		InputTokens:              100_000,
		OutputTokens:             50_000,
		CacheCreationInputTokens: 10_000,
		CacheReadInputTokens:     20_000,
	}, s)

	entry := findSlogEntry(&buf, "请求完成")
	if entry == nil {
		t.Fatalf("expected slog entry with msg '请求完成', got: %s", buf.String())
	}

	assertField(t, entry, "request_model", "test-model")
	assertField(t, entry, "actual_model", "test-actual")
	assertField(t, entry, "input_fresh", 70_000)
	assertField(t, entry, "input_cache_read", 20_000)
	assertField(t, entry, "input_cache_write", 10_000)
	assertField(t, entry, "output_tokens", 50_000)
	assertField(t, entry, "request_cost", 2.79)
	assertField(t, entry, "total_cost", 80.3)
	assertField(t, entry, "cache_hit_rate", 30.0)
	assertField(t, entry, "cache_write_rate", 16.667)
	assertField(t, entry, "cache_rw_ratio", 2.0)
}

func TestLogUsageLine_zeroUsage(t *testing.T) {
	var buf bytes.Buffer
	restore := resetLoggerForTest(&buf)
	defer restore()

	s := stats.NewSessionStats()
	s.Record("test-model", "test-actual", stats.Usage{})

	logUsageLine("test-model", "test-actual", stats.Usage{}, s)

	entry := findSlogEntry(&buf, "请求完成")
	if entry == nil {
		t.Fatalf("expected slog entry with msg '请求完成', got: %s", buf.String())
	}
	assertField(t, entry, "request_model", "test-model")
	assertField(t, entry, "actual_model", "test-actual")
	assertField(t, entry, "input_fresh", 0)
	assertField(t, entry, "output_tokens", 0)
	assertField(t, entry, "request_cost", 0.0)
	assertField(t, entry, "total_cost", 0.0)
	assertField(t, entry, "cache_hit_rate", 0.0)
	assertField(t, entry, "cache_write_rate", 0.0)
}
