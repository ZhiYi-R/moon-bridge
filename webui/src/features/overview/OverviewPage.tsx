import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { motion } from "motion/react";
import { LoadingState } from "../../components/LoadingState";
import { MaterialFilterChip } from "../../components/MaterialFilterChip";
import { MaterialSwitch } from "../../components/MaterialSwitch";
import { ChipSet } from "../../components/ui/Chip";
import { Icon } from "../../components/ui/Icon";
import { useI18n } from "../../i18n/I18nProvider";
import type { MessageKey } from "../../i18n/messages";
import { getUsageStats, type UsageRange } from "../../rpc/management";
import { queryKeys } from "../../rpc/queryKeys";
import type { UsageStats, UsageStatsModelRow } from "../../rpc/types";
import { listContainer, listItem } from "../../theme/motion";
import { useConfigGraph } from "../configGraph/useConfigGraph";
import { LogPanel } from "../logs/LogPanel";
import { mockUsageStats } from "./mockUsage";
import { PageHeader, QueryErrorState } from "../shared";

const usageRanges: UsageRange[] = ["session", "24h", "7d", "30d", "all"];

const usageRangeLabelKeys: Record<UsageRange, MessageKey> = {
  session: "overview.range.session",
  "24h": "overview.range.24h",
  "7d": "overview.range.7d",
  "30d": "overview.range.30d",
  all: "overview.range.all"
};

export function OverviewPage() {
  const { t } = useI18n();
  const graph = useConfigGraph();
  const [range, setRange] = useState<UsageRange>("session");
  const [demoData, setDemoData] = useState(false);
  const usage = useQuery({
    queryKey: [...queryKeys.usageStats, range, demoData ? "demo" : "live"],
    queryFn: () => (demoData ? mockUsageStats(range) : getUsageStats(range)),
    placeholderData: keepPreviousData
  });
  const liveDuration = useLiveUsageDuration(
    usage.data?.totals.duration,
    range === "session" && !usage.isPlaceholderData && !demoData
  );

  return (
    <section className="page-stack" aria-labelledby="overview-title">
      <PageHeader eyebrow={t("pageEyebrow.analytics")} title={t("nav.overview")}>
        {t("overview.description")}
      </PageHeader>

      {graph.error ? (
        <section className="state-panel state-panel--inline" role="status">
          <p className="eyebrow">{t("common.error")}</p>
          <h2>{t("overview.graphUnavailableTitle")}</h2>
          <p>{t("overview.graphUnavailableDescription")}</p>
        </section>
      ) : null}

      <section className="usage-dashboard" aria-labelledby="usage-title">
        <div className="panel-heading">
          <div>
            <h2 id="usage-title">{t("overview.usageTitle")}</h2>
            <p>{t("overview.usageDescription")}</p>
          </div>
          <div className="usage-heading-controls">
            <ChipSet className="usage-range" role="group" aria-label={t("overview.rangeLabel")}>
              {usageRanges.map((option) => (
                <MaterialFilterChip
                  key={option}
                  value={option}
                  selected={range === option}
                  onSelect={setRange}
                >
                  {t(usageRangeLabelKeys[option])}
                </MaterialFilterChip>
              ))}
            </ChipSet>
            {usage.data ? <span className="status-pill status-pill--muted">{liveDuration}</span> : null}
            {import.meta.env.DEV ? (
              <label className="usage-demo-toggle">
                <MaterialSwitch label={t("overview.demoData")} selected={demoData} onChange={setDemoData} />
                <span>{t("overview.demoData")}</span>
              </label>
            ) : null}
          </div>
        </div>

        {usage.isLoading ? (
          <LoadingState label={t("common.loading")} />
        ) : usage.error ? (
          <QueryErrorState error={usage.error} />
        ) : usage.data ? (
          <UsageDashboard stats={usage.data} />
        ) : null}
      </section>

      <section id="logs" className="overview-logs">
        <div className="panel-heading">
          <div>
            <h2 id="overview-logs-title">{t("logs.panelTitle")}</h2>
            <p>{t("logs.description")}</p>
          </div>
        </div>
        <LogPanel labelledBy="overview-logs-title" embedded />
      </section>
    </section>
  );
}

function useLiveUsageDuration(rawDuration: string | undefined, active: boolean) {
  const { t } = useI18n();
  const [elapsedSeconds, setElapsedSeconds] = useState(0);
  const baseSeconds = rawDuration ? parseDurationSeconds(rawDuration) : undefined;

  useEffect(() => {
    setElapsedSeconds(0);
    if (!active || baseSeconds === undefined) {
      return;
    }
    const intervalId = window.setInterval(() => {
      setElapsedSeconds((current) => current + 1);
    }, 1000);
    return () => window.clearInterval(intervalId);
  }, [active, baseSeconds, rawDuration]);

  if (!rawDuration) {
    return "";
  }
  if (!active || baseSeconds === undefined) {
    return formatDuration(rawDuration, translateDurationPart);
  }
  return formatDurationSeconds(baseSeconds + elapsedSeconds, translateDurationPart);

  function translateDurationPart(key: MessageKey, count: number) {
    return t(key, { count });
  }
}

function UsageDashboard({ stats }: { stats: UsageStats }) {
  const { t } = useI18n();
  const hasUsage = stats.totals.requests > 0 || stats.by_model.length > 0;

  return (
    <>
      {!hasUsage ? (
        <div className="usage-empty-state">
          <p>{t("overview.usageEmpty")}</p>
        </div>
      ) : null}

      <motion.div
        className="usage-summary-grid"
        variants={listContainer}
        initial="hidden"
        animate="show"
      >
        <UsageMetric icon="swap_horiz" tone="primary" value={t("overview.requestsValue", { count: stats.totals.requests })} label={t("overview.requests")} />
        <UsageMetric icon="south_west" tone="primary" value={t("overview.inputValue", { count: formatTokenValue(stats.totals.input_tokens) })} label={t("overview.inputTokens")} />
        <UsageMetric icon="north_east" tone="tertiary" value={t("overview.outputValue", { count: formatTokenValue(stats.totals.output_tokens) })} label={t("overview.outputTokens")} />
        <UsageMetric icon="bolt" tone="secondary" value={t("overview.cacheHitValue", { rate: formatPercent(stats.totals.cache_hit_rate) })} label={t("overview.cacheHit")} />
        <UsageMetric icon="sync_alt" tone="secondary" value={t("overview.cacheRatioValue", { ratio: formatRatio(stats.totals.cache_rw_ratio) })} label={t("overview.cacheReadWrite")} />
        <UsageMetric icon="payments" tone="tertiary" value={t("overview.totalCostValue", { cost: formatCurrency(stats.totals.total_cost) })} label={t("overview.totalCost")} />
      </motion.div>

      <div className="usage-chart-grid">
        <UsageBarChart
          ariaLabel={chartAriaLabel(t, t("overview.tokenSplitChart"), [
            [t("overview.inputTokens"), stats.totals.input_tokens],
            [t("overview.outputTokens"), stats.totals.output_tokens]
          ], t("overview.noData"))}
          title={t("overview.tokenSplit")}
          segments={[
            { label: t("overview.inputTokens"), value: stats.totals.input_tokens, className: "usage-segment--input" },
            { label: t("overview.outputTokens"), value: stats.totals.output_tokens, className: "usage-segment--output" }
          ]}
        />
        <UsageBarChart
          ariaLabel={chartAriaLabel(t, t("overview.cacheSplitChart"), [
            [t("overview.cacheWrite"), stats.totals.cache_creation],
            [t("overview.cacheRead"), stats.totals.cache_read]
          ], t("overview.noData"))}
          title={t("overview.cacheSplit")}
          segments={[
            { label: t("overview.cacheWrite"), value: stats.totals.cache_creation, className: "usage-segment--cache-write" },
            { label: t("overview.cacheRead"), value: stats.totals.cache_read, className: "usage-segment--cache-read" }
          ]}
        />
        <UsageBarChart
          ariaLabel={chartAriaLabel(
            t,
            t("overview.costByModelChart"),
            stats.by_model.map((row) => [row.model, row.cost]),
            t("overview.noData")
          )}
          title={t("overview.costByModel")}
          segments={stats.by_model.map((row, index) => ({
            label: row.model,
            value: row.cost,
            className: `usage-segment--cost-${(index % 6) + 1}`
          }))}
        />
      </div>

      <div className="table-scroll">
        <table className="resource-table usage-table" aria-label={t("overview.modelUsageTable")}>
          <thead>
            <tr>
              <th>{t("overview.model")}</th>
              <th>{t("overview.actualModel")}</th>
              <th>{t("overview.requests")}</th>
              <th>{t("overview.inputTokens")}</th>
              <th>{t("overview.outputTokens")}</th>
              <th>{t("overview.cacheWrite")}</th>
              <th>{t("overview.cacheRead")}</th>
              <th>{t("overview.cacheHit")}</th>
              <th>{t("overview.cost")}</th>
              <th>{t("overview.avgCost")}</th>
            </tr>
          </thead>
          <tbody>
            {stats.by_model.map((row) => (
              <UsageModelRow row={row} key={row.model} />
            ))}
          </tbody>
        </table>
      </div>
    </>
  );
}

function UsageMetric({
  label,
  value,
  icon,
  tone = "primary"
}: {
  label: string;
  value: string;
  icon: string;
  tone?: "primary" | "tertiary" | "secondary";
}) {
  return (
    <motion.article className={`usage-metric usage-metric--${tone}`} variants={listItem}>
      <span className="usage-metric__icon" aria-hidden="true">
        <Icon className="material-symbol" name={icon} size={18} />
      </span>
      <span className="usage-metric__label">{label}</span>
      <strong>{value}</strong>
    </motion.article>
  );
}

function UsageBarChart({
  ariaLabel,
  title,
  segments
}: {
  ariaLabel: string;
  title: string;
  segments: Array<{ label: string; value: number; className: string }>;
}) {
  const total = segments.reduce((sum, segment) => sum + Math.max(0, segment.value), 0);
  return (
    <section className="usage-chart" role="img" aria-label={ariaLabel} tabIndex={0}>
      <div className="usage-chart__header">
        <h3>{title}</h3>
        <span>{formatNumber(total)}</span>
      </div>
      <div className="usage-chart__bar" aria-hidden="true">
        {segments.map((segment) => (
          <span
            className={`usage-chart__segment ${segment.className}`}
            key={segment.label}
            style={{ inlineSize: `${total > 0 ? (Math.max(0, segment.value) / total) * 100 : 0}%` }}
          />
        ))}
      </div>
      <ul className="usage-chart__legend">
        {segments.map((segment) => (
          <li key={segment.label}>
            <span className={`usage-chart__dot ${segment.className}`} aria-hidden="true" />
            <span>{segment.label}</span>
            <strong>{formatNumber(segment.value)}</strong>
          </li>
        ))}
      </ul>
    </section>
  );
}

function UsageModelRow({ row }: { row: UsageStatsModelRow }) {
  const { t } = useI18n();
  return (
    <tr aria-label={t("overview.modelUsageRow", { model: row.model })}>
      <td>{row.model}</td>
      <td>{row.actual_model || "-"}</td>
      <td>{formatNumber(row.requests)}</td>
      <td>{formatTokenValue(row.input_tokens)}</td>
      <td>{formatTokenValue(row.output_tokens)}</td>
      <td>{formatTokenValue(row.cache_creation)}</td>
      <td>{formatTokenValue(row.cache_read)}</td>
      <td>{formatPercent(row.cache_hit_rate)}</td>
      <td>{formatCurrency(row.cost)}</td>
      <td>{formatCurrency(row.avg_cost_per_mtoken)}{t("overview.costPerMillionSuffix")}</td>
    </tr>
  );
}

function formatNumber(value: number) {
  return new Intl.NumberFormat().format(value);
}

function formatDuration(
  raw: string,
  translatePart: (key: MessageKey, count: number) => string
) {
  if (!raw || raw === "N/A") {
    return "—";
  }
  const seconds = parseDurationSeconds(raw);
  if (seconds === undefined) {
    return raw.trim();
  }
  return formatDurationSeconds(seconds, translatePart);
}

function parseDurationSeconds(raw: string) {
  const match = /^(?:(\d+)h)?(?:(\d+)m)?(?:([\d.]+)s)?$/.exec(raw.trim());
  if (!match || (!match[1] && !match[2] && !match[3])) {
    return undefined;
  }
  const hours = match[1] ? parseInt(match[1], 10) : 0;
  const minutes = match[2] ? parseInt(match[2], 10) : 0;
  const seconds = match[3] ? Math.round(parseFloat(match[3])) : 0;
  return hours * 3600 + minutes * 60 + seconds;
}

function formatDurationSeconds(
  totalSeconds: number,
  translatePart: (key: MessageKey, count: number) => string
) {
  const roundedSeconds = Math.max(0, Math.round(totalSeconds));
  const hours = Math.floor(roundedSeconds / 3600);
  const minutes = Math.floor((roundedSeconds % 3600) / 60);
  const seconds = roundedSeconds % 60;
  const parts: string[] = [];
  if (hours) {
    parts.push(translatePart("overview.duration.hoursShort", hours));
  }
  if (minutes) {
    parts.push(translatePart("overview.duration.minutesShort", minutes));
  }
  if (seconds || parts.length === 0) {
    parts.push(translatePart("overview.duration.secondsShort", seconds));
  }
  return parts.join(" ");
}

function formatTokenValue(value: number) {
  return formatNumber(value);
}

function formatPercent(value: number) {
  return `${new Intl.NumberFormat(undefined, { maximumFractionDigits: 1 }).format(value)}%`;
}

function formatRatio(value: number) {
  return new Intl.NumberFormat(undefined, {
    maximumFractionDigits: 2
  }).format(value);
}

function formatCurrency(value: number) {
  return new Intl.NumberFormat(undefined, {
    style: "currency",
    currency: "CNY",
    currencyDisplay: "narrowSymbol",
    minimumFractionDigits: 2,
    maximumFractionDigits: 2
  }).format(value);
}

function chartAriaLabel(
  t: (key: MessageKey, values?: Record<string, string | number>) => string,
  title: string,
  values: Array<[string, number]>,
  emptySummary: string
) {
  const summary = values.length > 0
    ? values
      .map(([label, value]) => t("overview.chartAriaItem", { label, value: formatNumber(value) }))
      .join(t("overview.chartAriaSeparator"))
    : emptySummary;
  return t("overview.chartAriaLabel", { title, summary });
}
