package server

import (
	"bytes"
	"strings"
	"testing"

	"moonbridge/internal/foundation/logger"
	"moonbridge/internal/service/stats"
)

func TestLogUsageLine_nilSessionStats(t *testing.T) {
	// Should not panic when sessionStats is nil
	var buf bytes.Buffer
	oldOutput := logger.Output()
	logger.Init(logger.Config{Level: logger.LevelInfo, Output: &buf})
	defer logger.Init(logger.Config{Level: logger.LevelInfo, Output: oldOutput})

	logUsageLine("test-model", "test-actual", stats.Usage{}, nil)

	output := buf.String()
	if !strings.Contains(output, "模型: test-model ➡️ test-actual") {
		t.Fatalf("expected per-request line, got: %s", output)
	}
	if strings.Contains(output, "---") {
		t.Fatalf("expected no session summary when sessionStats is nil, got: %s", output)
	}
}

func TestLogUsageLine_withSessionStats(t *testing.T) {
	var buf bytes.Buffer
	oldOutput := logger.Output()
	logger.Init(logger.Config{Level: logger.LevelInfo, Output: &buf})
	defer logger.Init(logger.Config{Level: logger.LevelInfo, Output: oldOutput})

	s := stats.NewSessionStats()
	s.SetPricing(map[string]stats.ModelPricing{
		"test-model": {InputPrice: 10, OutputPrice: 40, CacheWritePrice: 5, CacheReadPrice: 2},
	})
	s.Record("test-model", "test-actual", stats.Usage{
		InputTokens:              1_000_000,
		OutputTokens:             500_000,
		CacheCreationInputTokens: 400_000,
		CacheReadInputTokens:     300_000,
	})
	s.Record("test-model", "test-actual", stats.Usage{
		InputTokens:              2_000_000,
		OutputTokens:             1_000_000,
		CacheCreationInputTokens: 100_000,
		CacheReadInputTokens:     600_000,
	})

	logUsageLine("test-model", "test-actual", stats.Usage{
		InputTokens:              100_000,
		OutputTokens:             50_000,
		CacheCreationInputTokens: 10_000,
		CacheReadInputTokens:     20_000,
	}, s)

	output := buf.String()

	// Should contain per-request data
	if !strings.Contains(output, "模型: test-model ➡️ test-actual") {
		t.Fatalf("expected per-request model line, got: %s", output)
	}
	// Verify cache write rate appears in per-request section
	if !strings.Contains(output, "写入率") {
		t.Fatalf("expected cache write rate in per-request output, got: %s", output)
	}

	// Should contain session summary separator
	if !strings.Contains(output, "---") {
		t.Fatalf("expected session summary separator '---', got: %s", output)
	}

	// Should contain session summary stats
	if !strings.Contains(output, "会话统计") {
		t.Fatalf("expected session summary '会话统计', got: %s", output)
	}
	if !strings.Contains(output, "累计费用") {
		t.Fatalf("expected '累计费用' in session summary, got: %s", output)
	}
	if !strings.Contains(output, "test-model") {
		t.Fatalf("expected model name in session summary, got: %s", output)
	}

	// Should have cumulative request count (2 records in setup, logUsageLine does not Record)
	if !strings.Contains(output, "2 次请求") {
		t.Fatalf("expected 2 requests in session summary, got: %s", output)
	}
}

func TestLogUsageLine_zeroUsage(t *testing.T) {
	// Edge case: sessionStats is non-nil but all token counts are zero
	// Should not panic with division by zero
	var buf bytes.Buffer
	oldOutput := logger.Output()
	logger.Init(logger.Config{Level: logger.LevelInfo, Output: &buf})
	defer logger.Init(logger.Config{Level: logger.LevelInfo, Output: oldOutput})

	s := stats.NewSessionStats()
	// Record zero usage
	s.Record("test-model", "test-actual", stats.Usage{})

	logUsageLine("test-model", "test-actual", stats.Usage{}, s)

	output := buf.String()

	// Should contain per-request data
	if !strings.Contains(output, "模型: test-model ➡️ test-actual") {
		t.Fatalf("expected per-request line, got: %s", output)
	}
	// Should still show session summary
	if !strings.Contains(output, "---") {
		t.Fatalf("expected session summary separator, got: %s", output)
	}
	if !strings.Contains(output, "1 次请求") {
		t.Fatalf("expected 1 request in session summary, got: %s", output)
	}
	// Should contain zero rates without division errors
	if !strings.Contains(output, "0.00%") || !strings.Contains(output, "0.00%") {
		t.Fatalf("expected zero cache rates in output, got: %s", output)
	}
}
