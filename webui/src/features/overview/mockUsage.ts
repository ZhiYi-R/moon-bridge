import type { UsageRange } from "../../rpc/management";
import type { UsageStats, UsageStatsModelRow, UsageStatsTotals } from "../../rpc/types";

/**
 * Dev-only mock usage data so the Overview dashboard can be previewed without a live
 * backend. Eight models spanning three providers (Anthropic / OpenAI / Google).
 *
 * Never referenced in production builds — OverviewPage only enables the demo toggle
 * when `import.meta.env.DEV` is true.
 */

const DEMO_MODELS: Array<{ model: string; actual: string }> = [
  { model: "claude-sonnet", actual: "claude-3-5-sonnet" },
  { model: "claude-opus", actual: "claude-3-opus" },
  { model: "claude-haiku", actual: "claude-3-5-haiku" },
  { model: "gpt-4o", actual: "gpt-4o-2024-08-06" },
  { model: "gpt-4o-mini", actual: "gpt-4o-mini" },
  { model: "o3-mini", actual: "o3-mini" },
  { model: "gemini-pro", actual: "gemini-1.5-pro" },
  { model: "gemini-flash", actual: "gemini-1.5-flash" }
];

const RANGE_SCALE: Record<UsageRange, number> = {
  session: 1,
  "24h": 4,
  "7d": 26,
  "30d": 110,
  all: 340
};

const RANGE_DURATION: Record<UsageRange, string> = {
  session: "1h 12m",
  "24h": "23h 48m",
  "7d": "167h",
  "30d": "718h",
  all: "2400h"
};

export function mockUsageStats(range: UsageRange = "session"): UsageStats {
  const scale = RANGE_SCALE[range] ?? 1;
  const byModel: UsageStatsModelRow[] = DEMO_MODELS.map((entry, index) => {
    const requests = Math.max(1, Math.round((38 + index * 19 + ((index * 7) % 5) * 11) * scale));
    const inputTokens = requests * (1400 + (index % 4) * 520);
    const outputTokens = requests * (260 + (index % 3) * 180);
    const cacheCreation = Math.round(inputTokens * (0.22 + (index % 3) * 0.05));
    const cacheRead = Math.round(inputTokens * (0.7 + (index % 4) * 0.12));
    const cacheHitRate = Math.min(99, Math.round((58 + (index * 6) % 38) * 10) / 10);
    const cost = Math.round(
      (inputTokens * 0.000003 + outputTokens * 0.000015 + cacheRead * 0.0000004) * 100
    ) / 100;
    return {
      model: entry.model,
      actual_model: entry.actual,
      requests,
      input_tokens: inputTokens,
      output_tokens: outputTokens,
      cache_creation: cacheCreation,
      cache_read: cacheRead,
      cache_hit_rate: cacheHitRate,
      cost,
      avg_cost_per_mtoken: Math.round((cost / Math.max(1, inputTokens + outputTokens)) * 1_000_000 * 100) / 100
    };
  });

  return { totals: aggregateTotals(byModel, range), by_model: byModel };
}

function aggregateTotals(byModel: UsageStatsModelRow[], range: UsageRange): UsageStatsTotals {
  const sum = (selector: (row: UsageStatsModelRow) => number) =>
    byModel.reduce((total, row) => total + selector(row), 0);

  const requests = sum((row) => row.requests);
  const inputTokens = sum((row) => row.input_tokens);
  const outputTokens = sum((row) => row.output_tokens);
  const cacheCreation = sum((row) => row.cache_creation);
  const cacheRead = sum((row) => row.cache_read);
  const totalCost = byModel.some((row) => row.cost == null)
    ? null
    : Math.round(sum((row) => row.cost ?? 0) * 100) / 100;

  return {
    requests,
    input_tokens: inputTokens,
    output_tokens: outputTokens,
    cache_creation: cacheCreation,
    cache_read: cacheRead,
    cache_hit_rate: inputTokens + cacheRead > 0
      ? Math.round((cacheRead / (inputTokens + cacheRead)) * 1000) / 10
      : 0,
    cache_write_rate: inputTokens > 0 ? Math.round((cacheCreation / inputTokens) * 1000) / 10 : 0,
    cache_rw_ratio: cacheCreation > 0 ? Math.round((cacheRead / cacheCreation) * 100) / 100 : 0,
    total_cost: totalCost,
    duration: RANGE_DURATION[range] ?? "0s"
  };
}
