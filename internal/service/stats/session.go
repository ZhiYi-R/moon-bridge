package stats

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// ModelPricing holds per-model pricing in RMB per M tokens.
type ModelPricing struct {
	InputPrice      float64
	OutputPrice     float64
	CacheWritePrice float64
	CacheReadPrice  float64
}

// Usage represents legacy total-input usage.
// New request accounting should prefer BillingUsage, which carries explicit
// billing dimensions and does not need to infer fresh input from a total.
type Usage struct {
	InputTokens              int
	OutputTokens             int
	CacheCreationInputTokens int
	CacheReadInputTokens     int
}

// BillingUsage is the provider billing view used for stats, logs and cost.
type BillingUsage struct {
	FreshInputTokens         int
	OutputTokens             int
	CacheCreationInputTokens int
	CacheReadInputTokens     int
	ProviderInputTokens      int
}

func (u Usage) BillingUsage() BillingUsage {
	freshInput := u.InputTokens - u.CacheCreationInputTokens - u.CacheReadInputTokens
	if freshInput < 0 {
		freshInput = 0
	}
	return BillingUsage{
		FreshInputTokens:         freshInput,
		OutputTokens:             u.OutputTokens,
		CacheCreationInputTokens: u.CacheCreationInputTokens,
		CacheReadInputTokens:     u.CacheReadInputTokens,
		ProviderInputTokens:      u.InputTokens,
	}
}

func (u BillingUsage) InputTokens() int {
	sum := u.FreshInputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens
	if u.ProviderInputTokens > sum {
		return u.ProviderInputTokens
	}
	return sum
}

// SessionStats tracks cumulative token usage and cost across a session.
type SessionStats struct {
	mu sync.RWMutex

	startTime time.Time

	// Cumulative counts
	totalRequests      int64
	totalInputTokens   int64
	totalOutputTokens  int64
	totalCacheCreation int64
	totalCacheRead     int64

	// Cumulative cost (RMB)
	totalCost        float64
	unpricedRequests int64

	// Per-model breakdown
	byModel map[string]*ModelStats
	pricing map[string]ModelPricing

	// Maps request model alias -> actual upstream model name
	actualModelNames map[string]string
}

// ModelStats tracks usage and cost for a specific model.
type ModelStats struct {
	Requests         int64
	InputTokens      int64
	OutputTokens     int64
	CacheCreation    int64
	CacheRead        int64
	Cost             float64 `json:"-"` // Known subtotal only; use CostValue for a complete estimate.
	UnpricedRequests int64
}

// CostValue is nil when any included request has no configured CNY price.
func (m ModelStats) CostValue() *float64 {
	if m.UnpricedRequests > 0 {
		return nil
	}
	v := m.Cost
	return &v
}

func (m ModelStats) MarshalJSON() ([]byte, error) {
	type plain ModelStats
	return json.Marshal(struct {
		plain
		Cost        *float64
		CostUnknown bool
	}{plain(m), m.CostValue(), m.UnpricedRequests > 0})
}

// NewSessionStats creates a new session stats tracker.
func NewSessionStats() *SessionStats {
	return &SessionStats{
		startTime:        time.Now(),
		byModel:          make(map[string]*ModelStats),
		pricing:          make(map[string]ModelPricing),
		actualModelNames: make(map[string]string),
	}
}

// SetPricing configures per-model pricing for cost calculation.
func (s *SessionStats) SetPricing(pricing map[string]ModelPricing) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pricing = pricing
}

// Record adds a usage record to the session stats.
// If pricing is configured for the model, cost is computed automatically.
func (s *SessionStats) Record(model string, actualModel string, usage Usage) {
	s.RecordBilling(model, actualModel, usage.BillingUsage())
}

// RecordBilling adds provider billing usage to the session stats.
// If pricing is configured for the model, cost is computed automatically.
func (s *SessionStats) RecordBilling(model string, actualModel string, usage BillingUsage) {
	s.mu.Lock()
	defer s.mu.Unlock()

	inputTokens := usage.InputTokens()
	s.totalRequests++
	s.totalInputTokens += int64(inputTokens)
	s.totalOutputTokens += int64(usage.OutputTokens)
	s.totalCacheCreation += int64(usage.CacheCreationInputTokens)
	s.totalCacheRead += int64(usage.CacheReadInputTokens)

	// Compute cost if pricing is available for this model
	var cost float64
	p, priced := s.pricing[model]
	if priced {
		cost = computeBillingCost(usage, p)
		s.totalCost += cost
	} else {
		s.unpricedRequests++
	}

	if model != "" {
		if s.byModel[model] == nil {
			s.byModel[model] = &ModelStats{}
		}
		s.byModel[model].Requests++
		s.byModel[model].InputTokens += int64(inputTokens)
		s.byModel[model].OutputTokens += int64(usage.OutputTokens)
		s.byModel[model].CacheCreation += int64(usage.CacheCreationInputTokens)
		s.byModel[model].CacheRead += int64(usage.CacheReadInputTokens)
		if priced {
			s.byModel[model].Cost += cost
		} else {
			s.byModel[model].UnpricedRequests++
		}

		// Track the actual model name for display
		if actualModel != "" {
			s.actualModelNames[model] = actualModel
		}
	}
}

// computeCost calculates the cost in RMB for a single usage record.
// All prices are per M tokens.
func computeCost(usage Usage, p ModelPricing) float64 {
	return computeBillingCost(usage.BillingUsage(), p)
}

func computeBillingCost(usage BillingUsage, p ModelPricing) float64 {
	freshInput := float64(usage.FreshInputTokens)
	cacheWrite := float64(usage.CacheCreationInputTokens)
	cacheRead := float64(usage.CacheReadInputTokens)
	output := float64(usage.OutputTokens)

	return freshInput*p.InputPrice/1000000 +
		cacheWrite*p.CacheWritePrice/1000000 +
		cacheRead*p.CacheReadPrice/1000000 +
		output*p.OutputPrice/1000000
}

// Summary returns a summary of the session stats.
func (s *SessionStats) Summary() Summary {
	s.mu.RLock()
	defer s.mu.RUnlock()

	totalInput := s.totalInputTokens
	var cacheHitRate float64
	var cacheWriteRate float64
	if totalInput > 0 {
		cacheHitRate = float64(s.totalCacheRead) / float64(totalInput) * 100
		cacheWriteRate = float64(s.totalCacheCreation) / float64(totalInput) * 100
	}

	return Summary{
		Duration:            time.Since(s.startTime),
		Requests:            s.totalRequests,
		InputTokens:         s.totalInputTokens,
		OutputTokens:        s.totalOutputTokens,
		CacheCreation:       s.totalCacheCreation,
		CacheRead:           s.totalCacheRead,
		CacheHitRate:        cacheHitRate,
		CacheWriteRate:      cacheWriteRate,
		EffectiveInputSaved: s.totalCacheRead,
		TotalCost:           s.totalCost,
		UnpricedRequests:    s.unpricedRequests,
		ByModel:             copyByModel(s.byModel),
		ActualModelNames:    copyStringMap(s.actualModelNames),
	}
}

func copyByModel(src map[string]*ModelStats) map[string]*ModelStats {
	if src == nil {
		return nil
	}
	dst := make(map[string]*ModelStats, len(src))
	for k, v := range src {
		cp := *v
		dst[k] = &cp
	}
	return dst
}

func copyStringMap(src map[string]string) map[string]string {
	if src == nil {
		return nil
	}
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// Summary is a snapshot of session stats.
type Summary struct {
	Duration            time.Duration
	Requests            int64
	InputTokens         int64
	OutputTokens        int64
	CacheCreation       int64
	CacheRead           int64
	CacheHitRate        float64
	CacheWriteRate      float64
	EffectiveInputSaved int64
	TotalCost           float64 `json:"-"` // Known subtotal; not the total when UnpricedRequests > 0.
	UnpricedRequests    int64
	ByModel             map[string]*ModelStats
	ActualModelNames    map[string]string
}

func (s Summary) CostValue() *float64 {
	if s.UnpricedRequests > 0 {
		return nil
	}
	v := s.TotalCost
	return &v
}

func (s Summary) MarshalJSON() ([]byte, error) {
	type plain Summary
	return json.Marshal(struct {
		plain
		TotalCost   *float64
		CostUnknown bool
		Currency    string
	}{plain(s), s.CostValue(), s.UnpricedRequests > 0, "CNY"})
}

// LogValue implements slog.LogValuer for structured logging.
func (s Summary) LogValue() slog.Value {
	attrs := []slog.Attr{
		slog.Int64("requests", s.Requests),
		slog.Int64("input_tokens", s.InputTokens),
		slog.Int64("output_tokens", s.OutputTokens),
		slog.Int64("cache_creation", s.CacheCreation),
		slog.Int64("cache_read", s.CacheRead),
		slog.Float64("cache_hit_rate", s.CacheHitRate),
		slog.Float64("cache_write_rate", s.CacheWriteRate),
		slog.Int64("cache_saved_tokens", s.EffectiveInputSaved),
		slog.Duration("duration", s.Duration),
	}
	cost := s.TotalCost
	if s.UnpricedRequests > 0 {
		cost = -1
	}
	attrs = append(attrs, slog.Any("cost_cny", NullableCost(cost)), slog.Bool("cost_unknown", s.UnpricedRequests > 0))
	return slog.GroupValue(attrs...)
}

// UsageLineParams holds all data needed to format a per-request usage log block.
type UsageLineParams struct {
	RequestModel   string
	ActualModel    string
	Usage          Usage
	BillingUsage   BillingUsage
	RequestCost    float64
	TotalCost      float64
	CacheHitRate   float64
	CacheWriteRate float64
}

// FormatTokenCount formats a token count with adaptive units:
// <1000: raw integer; >=1000: two-decimal K/M/B/T.
func FormatTokenCount(n int64) string {
	if n == 0 {
		return "0"
	}
	if n < 0 {
		// Use uint64 to safely negate, avoiding overflow for math.MinInt64.
		un := uint64(-n)
		if int64(un) > 0 {
			return "-" + FormatTokenCount(int64(un))
		}
		// math.MinInt64: -9223372036854775808
		return "-9.22E"
	}
	switch {
	case n < 1000:
		return fmt.Sprintf("%d", n)
	case n < 1_000_000:
		return fmt.Sprintf("%.2fK", float64(n)/1000)
	case n < 1_000_000_000:
		return fmt.Sprintf("%.2fM", float64(n)/1_000_000)
	case n < 1_000_000_000_000:
		return fmt.Sprintf("%.2fB", float64(n)/1_000_000_000)
	default:
		return fmt.Sprintf("%.2fT", float64(n)/1_000_000_000_000)
	}
}

// FormatAvgCost formats an average cost per token with adaptive units:
// cost/token >= 1 → ¥X.XX/token, >= 0.001 → ¥X.XX/K, else → ¥X.XX/M.
// Returns "¥0.00/M" when tokens <= 0.
func FormatAvgCost(cost float64, tokens int64) string {
	if tokens <= 0 {
		return "¥0.00/M"
	}
	perToken := cost / float64(tokens)
	switch {
	case perToken >= 1:
		return fmt.Sprintf("¥%.2f/token", perToken)
	case perToken >= 0.001:
		return fmt.Sprintf("¥%.2f/K", perToken*1000)
	default:
		return fmt.Sprintf("¥%.2f/M", perToken*1_000_000)
	}
}

// CacheRWRatio returns the cache read/write ratio (reads per write).
// Returns 0 if no cache writes occurred.
func CacheRWRatio(usage Usage) float64 {
	return BillingCacheRWRatio(usage.BillingUsage())
}

func BillingCacheRWRatio(usage BillingUsage) float64 {
	if usage.CacheCreationInputTokens == 0 {
		return 0
	}
	return float64(usage.CacheReadInputTokens) / float64(usage.CacheCreationInputTokens)
}

// Deprecated: FormatUsageLine is retained for backward compatibility.
// Request logging now uses structured slog attrs.
func FormatUsageLine(p UsageLineParams) string {
	usage := p.BillingUsage
	if usage == (BillingUsage{}) {
		usage = p.Usage.BillingUsage()
	}
	rwRatio := BillingCacheRWRatio(usage)
	return fmt.Sprintf(
		"模型: %s ➡️ %s\n"+
			"输入: 读取 %s + 写入 %s + 首次 %s\n"+
			"输出: %s\n"+
			"计费: 本请求 %.4f 元, 累计 %.4f 元\n"+
			"缓存: 命中率 %.2f%%, 写入率 %.2f%%, 读写比 %.2f",
		p.RequestModel, p.ActualModel,
		FormatTokenCount(int64(usage.CacheReadInputTokens)),
		FormatTokenCount(int64(usage.CacheCreationInputTokens)),
		FormatTokenCount(int64(usage.FreshInputTokens)),
		FormatTokenCount(int64(usage.OutputTokens)),
		p.RequestCost, p.TotalCost,
		p.CacheHitRate, p.CacheWriteRate, rwRatio,
	)
}

func FormatSummaryLine(s Summary) string {
	rwRatio := float64(0)
	if s.CacheCreation > 0 {
		rwRatio = float64(s.CacheRead) / float64(s.CacheCreation)
	}
	totalTokens := s.InputTokens + s.OutputTokens
	cost := fmt.Sprintf("%.2f 元", s.TotalCost)
	if s.UnpricedRequests > 0 {
		cost = "unknown（未配置价格）"
	}
	return fmt.Sprintf("统计：缓存命中率 %.1f%%, 读写比 %.2f, 累计计费 %s, 总token %s",
		s.CacheHitRate, rwRatio, cost, FormatTokenCount(totalTokens))
}

// ErrorLineParams holds data needed to format a per-request error log block.
type ErrorLineParams struct {
	RequestModel string
	ActualModel  string
	StatusCode   int
	Message      string
}

// Deprecated: FormatErrorLine is retained for backward compatibility.
// Error logging now uses structured slog attrs.
func FormatErrorLine(p ErrorLineParams) string {
	return fmt.Sprintf(
		"模型: %s ➡️ %s\n"+
			"状态码: %d, 错误: %s",
		p.RequestModel, p.ActualModel,
		p.StatusCode, p.Message,
	)
}

// WriteSummary writes a human-readable summary to the writer.
func WriteSummary(w io.Writer, s Summary) {
	var buf bytes.Buffer
	fmt.Fprintln(&buf, FormatSummaryLine(s))
	fmt.Fprintf(&buf, "会话统计: %d 次请求, 耗时 %s\n", s.Requests, s.Duration.Round(time.Second))
	freshInput := s.InputTokens - s.CacheCreation - s.CacheRead
	if freshInput < 0 {
		freshInput = 0
	}
	fmt.Fprintf(&buf, "  输入: %s (首次 %s, 缓存写入 %s, 缓存读取 %s)\n",
		FormatTokenCount(s.InputTokens),
		FormatTokenCount(freshInput),
		FormatTokenCount(s.CacheCreation),
		FormatTokenCount(s.CacheRead))
	fmt.Fprintf(&buf, "  输出: %s\n", FormatTokenCount(s.OutputTokens))
	if s.CacheHitRate > 0 {
		fmt.Fprintf(&buf, "  缓存命中率: %.1f%% (节省 %s)\n",
			s.CacheHitRate, FormatTokenCount(s.EffectiveInputSaved))
	}
	rwRatio := float64(0)
	if s.CacheCreation > 0 {
		rwRatio = float64(s.CacheRead) / float64(s.CacheCreation)
	}
	if rwRatio > 0 {
		fmt.Fprintf(&buf, "  缓存读写比: %.2f\n", rwRatio)
	}
	if s.UnpricedRequests > 0 {
		fmt.Fprintln(&buf, "  累计费用: unknown（未配置价格）")
	} else {
		fmt.Fprintf(&buf, "  累计费用: ¥%.6f\n", s.TotalCost)
	}
	totalTokens := s.InputTokens + s.OutputTokens
	if totalTokens > 0 && s.UnpricedRequests == 0 {
		fmt.Fprintf(&buf, "  全局平均成本: %s\n", FormatAvgCost(s.TotalCost, totalTokens))
	}
	for model, ms := range s.ByModel {
		displayName := model
		if actual, ok := s.ActualModelNames[model]; ok && actual != model {
			// For direct "provider/model" or "model(provider)" references,
			// show just the model name without the misleading arrow.
			if idx := strings.LastIndex(model, "/"); idx > 0 {
				displayName = model[idx+1:]
			} else if idx := strings.LastIndex(model, "("); idx > 0 && strings.HasSuffix(model, ")") {
				displayName = model[:idx]
			} else {
				displayName = model + " → " + actual
			}
		}
		if ms.UnpricedRequests > 0 {
			fmt.Fprintf(&buf, "    %s: unknown（未配置价格，%d 次请求）\n", displayName, ms.Requests)
		} else if ms.Cost > 0 {
			modelTokens := ms.InputTokens + ms.OutputTokens
			fmt.Fprintf(&buf, "    %s: ¥%.6f (%d 次, %s 输入, %s 输出, %s)\n",
				displayName, ms.Cost, ms.Requests,
				FormatTokenCount(ms.InputTokens),
				FormatTokenCount(ms.OutputTokens),
				FormatAvgCost(ms.Cost, modelTokens))
		}
	}
	buf.WriteTo(w)
}

// ComputeCost returns the cost in CNY, or -1 when no pricing is configured.
// Use NullableCost before exposing its result in logs or JSON.
func (s *SessionStats) ComputeCost(model string, usage Usage) float64 {
	return s.ComputeBillingCost(model, usage.BillingUsage())
}

// ComputeBillingCost returns CNY cost, or -1 when pricing is unknown.
func (s *SessionStats) ComputeBillingCost(model string, usage BillingUsage) float64 {
	if s == nil {
		return -1
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.pricing[model]
	if !ok {
		return -1
	}
	return computeBillingCost(usage, p)
}

// NullableCost converts the internal unknown sentinel for logs and JSON.
func NullableCost(cost float64) any {
	if cost < 0 {
		return nil
	}
	return cost
}
