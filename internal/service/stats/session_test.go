package stats

import (
	"bytes"
	"strings"
	"testing"
)

func TestSummaryLogValueAlwaysIncludesCost(t *testing.T) {
	attrs := (Summary{}).LogValue().Group()
	for _, attr := range attrs {
		if attr.Key == "cost_cny" {
			return
		}
	}
	t.Fatalf("cost_cny missing from log attrs: %+v", attrs)
}

func TestWriteSummaryAlwaysIncludesTotalCost(t *testing.T) {
	var output bytes.Buffer

	WriteSummary(&output, Summary{})

	for _, want := range []string{
		"统计：缓存命中率 0.0%",
		"累计计费 0.00 元",
		"累计费用:",
	} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("summary missing %q: %s", want, output.String())
		}
	}
}

func TestFormatUsageLine(t *testing.T) {
	line := FormatUsageLine(UsageLineParams{
		RequestModel: "moonbridge",
		ActualModel:  "deepseek-v4-pro",
		Usage: Usage{
			InputTokens:              2_000_000,
			CacheCreationInputTokens: 500_000,
			CacheReadInputTokens:     500_000,
			OutputTokens:             250_000,
		},
		RequestCost:    6.789,
		TotalCost:      12.345,
		CacheHitRate:   25.00,
		CacheWriteRate: 25.00,
	})

	for _, want := range []string{
		"模型: moonbridge ➡️ deepseek-v4-pro",
		"读取 500.00K",
		"写入 500.00K",
		"首次 1.00M",
		"输出: 250.00K",
		"计费: 本请求 6.7890 元",
		"累计 12.3450 元",
		"命中率 25.00%",
		"写入率 25.00%",
		"读写比 1.00",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("usage line missing %q: %s", want, line)
		}
	}
}

func TestFormatTokenCountNormal(t *testing.T) {
	tests := []struct {
		n    int64
		want string
	}{
		{0, "0"},
		{512, "512"},
		{999, "999"},
		{1000, "1.00K"},
		{1500, "1.50K"},
		{500_000, "500.00K"},
		{999_999, "1000.00K"},
		{1_000_000, "1.00M"},
		{1_234_567, "1.23M"},
		{999_999_999, "1000.00M"},
		{1_000_000_000, "1.00B"},
		{1_234_567_890, "1.23B"},
		{999_999_999_999, "1000.00B"},
		{1_000_000_000_000, "1.00T"},
		{1_234_567_890_123, "1.23T"},
	}
	for _, tt := range tests {
		got := FormatTokenCount(tt.n)
		if got != tt.want {
			t.Errorf("FormatTokenCount(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

func TestFormatTokenCountNegative(t *testing.T) {
	tests := []struct {
		n    int64
		want string
	}{
		{-512, "-512"},
		{-1500, "-1.50K"},
		{-1_000_000, "-1.00M"},
	}
	for _, tt := range tests {
		got := FormatTokenCount(tt.n)
		if got != tt.want {
			t.Errorf("FormatTokenCount(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

func TestFormatTokenCountBoundary(t *testing.T) {
	tests := []struct {
		n    int64
		want string
	}{
		{999, "999"},
		{1000, "1.00K"},
		{999_999, "1000.00K"},
		{1_000_000, "1.00M"},
	}
	for _, tt := range tests {
		got := FormatTokenCount(tt.n)
		if got != tt.want {
			t.Errorf("FormatTokenCount(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

func TestFormatAvgCostNormal(t *testing.T) {
	got := FormatAvgCost(0.01234567, 1000)
	if got != "¥12.35/M" {
		t.Fatalf("FormatAvgCost = %q, want ¥12.35/M", got)
	}
}

func TestFormatAvgCostLarge(t *testing.T) {
	got := FormatAvgCost(5000.0, 100)
	if got != "¥50.00/token" {
		t.Fatalf("FormatAvgCost(5000, 100) = %q, want ¥50.00/token", got)
	}
}

func TestFormatAvgCostTiny(t *testing.T) {
	got := FormatAvgCost(0.001, 1000)
	if got != "¥1.00/M" {
		t.Fatalf("FormatAvgCost(0.001, 1000) = %q, want ¥1.00/M", got)
	}
}

func TestFormatAvgCostZero(t *testing.T) {
	got := FormatAvgCost(100.0, 0)
	if got != "¥0.00/M" {
		t.Fatalf("FormatAvgCost(100, 0) = %q, want ¥0.00/M", got)
	}
	got = FormatAvgCost(100.0, -1)
	if got != "¥0.00/M" {
		t.Fatalf("FormatAvgCost(100, -1) = %q, want ¥0.00/M", got)
	}
}

func TestFormatTokenCountMinInt64(t *testing.T) {
	got := FormatTokenCount(-1 << 63) // math.MinInt64
	if got != "-9.22E" {
		t.Fatalf("FormatTokenCount(MinInt64) = %q, want -9.22E", got)
	}
}

func TestFormatAvgCostKUnit(t *testing.T) {
	got := FormatAvgCost(5.0, 1000)
	if got != "¥5.00/K" {
		t.Fatalf("FormatAvgCost(5, 1000) = %q, want ¥5.00/K", got)
	}
}

func TestFormatAvgCostBoundary(t *testing.T) {
	// Exactly at thresholds
	if got := FormatAvgCost(1000, 1000); got != "¥1.00/token" {
		t.Fatalf("perToken=1: got %q, want ¥1.00/token", got)
	}
	if got := FormatAvgCost(1, 1000); got != "¥1.00/K" {
		t.Fatalf("perToken=0.001: got %q, want ¥1.00/K", got)
	}
}
