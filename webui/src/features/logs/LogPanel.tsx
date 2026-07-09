import { useEffect, useMemo, useState } from "react";
import { motion } from "motion/react";
import { MaterialIconButton, MaterialOutlinedButton } from "../../components/MaterialButton";
import { MaterialFilterChip } from "../../components/MaterialFilterChip";
import { MaterialOutlinedTextField } from "../../components/MaterialTextField";
import { ChipSet } from "../../components/ui/Chip";
import { useI18n } from "../../i18n/I18nProvider";
import { createLogStream, getRecentLogs } from "../../rpc/logs";
import type { LogEntry } from "../../rpc/types";
import { springs } from "../../theme/motion";

const logLevels = ["ALL", "ERROR", "WARN", "INFO", "DEBUG"] as const;
type LogLevelFilter = (typeof logLevels)[number];

export function LogPanel({ labelledBy, embedded }: { labelledBy?: string; embedded?: boolean }) {
  const { t } = useI18n();
  const [entries, setEntries] = useState<LogEntry[]>([]);
  const [filter, setFilter] = useState("");
  const [levelFilter, setLevelFilter] = useState<LogLevelFilter>("ALL");
  const [follow, setFollow] = useState(true);
  const [streamError, setStreamError] = useState(false);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<unknown>();

  useEffect(() => {
    let cancelled = false;
    getRecentLogs({ limit: 200 })
      .then((recent) => {
        if (!cancelled) {
          setEntries(recent);
          setLoading(false);
        }
      })
      .catch((cause: unknown) => {
        if (!cancelled) {
          setError(cause);
          setLoading(false);
        }
      });
    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    if (!follow) {
      return undefined;
    }
    setStreamError(false);
    const abort = new AbortController();
    void consumeStream(abort.signal, (entry) => {
      setEntries((current) => [...current, entry]);
    }).catch((cause: unknown) => {
      if (!abort.signal.aborted) {
        setStreamError(true);
        console.error("log stream failed", cause);
      }
    });
    return () => abort.abort();
  }, [follow]);

  const visibleEntries = useMemo(() => {
    const needle = filter.trim().toLowerCase();
    return entries.filter((entry) => {
      if (levelFilter !== "ALL" && normalizeLevel(entry.level) !== levelFilter.toLowerCase()) {
        return false;
      }
      if (!needle) {
        return true;
      }
      return logLine(entry).toLowerCase().includes(needle);
    });
  }, [entries, filter, levelFilter]);

  return (
    <section
      aria-label={labelledBy ? undefined : t("logs.panelLabel")}
      aria-labelledby={labelledBy}
      className={embedded ? "logs-panel" : "content-panel logs-panel"}
    >
      <div className="logs-panel__header">
        {labelledBy ? <span aria-hidden="true" /> : <h2>{t("logs.panelTitle")}</h2>}
        <div className="logs-panel__actions">
          <MaterialOutlinedButton
            disabled={visibleEntries.length === 0}
            icon="content_copy"
            onClick={() => copyLogs(visibleEntries)}
          >
            {t("logs.copy")}
          </MaterialOutlinedButton>
          <MaterialOutlinedButton
            disabled={visibleEntries.length === 0}
            icon="download"
            onClick={() => downloadLogs(visibleEntries)}
          >
            {t("logs.download")}
          </MaterialOutlinedButton>
        </div>
      </div>

      <div className="logs-toolbar">
        <div className="logs-toolbar__actions">
          <ChipSet className="logs-chip-set logs-follow-mode" role="group" aria-label={t("logs.followMode")}>
            <MaterialFilterChip value="follow" selected={follow} onSelect={() => setFollow(true)}>
              {t("logs.follow")}
            </MaterialFilterChip>
            <MaterialFilterChip value="pause" selected={!follow} onSelect={() => setFollow(false)}>
              {t("logs.pause")}
            </MaterialFilterChip>
          </ChipSet>
        </div>
        <p className="logs-count">
          {t("logs.visibleCount", { visible: visibleEntries.length, total: entries.length })}
        </p>
      </div>

      <ChipSet className="logs-chip-set log-level-filter" role="group" aria-label={t("logs.levelFilter")}>
        {logLevels.map((level) => (
          <MaterialFilterChip
            key={level}
            value={level}
            selected={levelFilter === level}
            onSelect={setLevelFilter}
          >
            {level === "ALL" ? t("logs.levelAll") : level}
          </MaterialFilterChip>
        ))}
      </ChipSet>

      {streamError ? (
        <p className="logs-stream-status" role="status">
          {t("logs.streamDisconnected")}
        </p>
      ) : null}

      {error ? (
        <p className="logs-stream-status" role="status">
          {error instanceof Error ? error.message : t("error.unknownRequest")}
        </p>
      ) : null}

      <div className="logs-search">
        <MaterialOutlinedTextField
          className="logs-search__field"
          label={t("logs.search")}
          type="search"
          value={filter}
          onInput={setFilter}
        />
        <MaterialIconButton
          disabled={filter.length === 0}
          icon="close"
          label={t("logs.clearSearch")}
          onClick={() => setFilter("")}
        />
      </div>

      <div className="log-output" aria-label={t("logs.output")}>
        {loading ? (
          <p className="log-empty-state" role="status">
            {t("common.loading")}
          </p>
        ) : visibleEntries.length === 0 ? (
          <p className="log-empty-state" role="status">
            {entries.length === 0 ? t("logs.empty") : t("logs.emptyFiltered")}
          </p>
        ) : (
          visibleEntries.map((entry, index) => (
            <LogRow entry={entry} index={index} key={`${entry.timestamp}-${index}-${logLine(entry)}`} />
          ))
        )}
      </div>
    </section>
  );
}

function LogRow({ entry, index }: { entry: LogEntry; index: number }) {
  const { t } = useI18n();
  const level = normalizeLevel(entry.level);
  return (
    <motion.article
      className={`log-row log-row--${level}`}
      aria-label={t("logs.rowLabel", { index: index + 1 })}
      initial={{ opacity: 0, y: 6 }}
      animate={{ opacity: 1, y: 0 }}
      transition={springs.effects}
    >
      <span className={`log-row__level log-row__level--${level}`} aria-hidden="true">{entry.level}</span>
      <time className="log-row__time" dateTime={entry.timestamp}>{compactLogTime(entry.timestamp)}</time>
      <p className="log-row__message">{entry.message || logLine(entry)}</p>
    </motion.article>
  );
}

/** Compact HH:MM:SS view of an ISO timestamp for dense log rows. */
function compactLogTime(timestamp: string): string {
  const time = timestamp.split("T")[1];
  return time ? time.replace(/\.\d{3,}.*$/, "").replace(/Z$/i, "") : timestamp;
}

async function consumeStream(signal: AbortSignal, append: (entry: LogEntry) => void) {
  const response = await createLogStream({ signal });
  const reader = response.body?.getReader();
  if (!reader) {
    throw new Error("log stream response body is empty");
  }

  const decoder = new TextDecoder();
  let buffer = "";
  for (;;) {
    const { done, value } = await reader.read();
    if (done) {
      break;
    }
    buffer += decoder.decode(value, { stream: true });
    const events = buffer.split("\n\n");
    buffer = events.pop() ?? "";
    for (const event of events) {
      const entry = parseSSEEvent(event);
      if (entry) {
        append(entry);
      }
    }
  }
  buffer += decoder.decode();
  const entry = parseSSEEvent(buffer);
  if (entry) {
    append(entry);
  }
}

function parseSSEEvent(event: string): LogEntry | undefined {
  const data = event
    .split("\n")
    .filter((line) => line.startsWith("data:"))
    .map((line) => line.slice(5).trimStart())
    .join("\n");
  if (!data) {
    return undefined;
  }
  return JSON.parse(data) as LogEntry;
}

function logLine(entry: LogEntry) {
  return entry.raw || `${entry.timestamp} ${entry.level} ${entry.message}`;
}

function normalizeLevel(level: string) {
  return level.trim().toLowerCase();
}

function copyLogs(entries: LogEntry[]) {
  const text = entries.map((entry) => logLine(entry)).join("\n");
  if (!navigator.clipboard) {
    console.error("clipboard API unavailable");
    return;
  }
  void navigator.clipboard.writeText(text).catch((cause: unknown) => {
    console.error("copy logs failed", cause);
  });
}

function downloadLogs(entries: LogEntry[]) {
  const blob = new Blob([entries.map((entry) => logLine(entry)).join("\n")], {
    type: "text/plain"
  });
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = "moonbridge-logs.txt";
  anchor.click();
  URL.revokeObjectURL(url);
}
