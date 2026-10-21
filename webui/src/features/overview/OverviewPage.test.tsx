import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, test, vi } from "vitest";
import { renderWithConsoleProviders } from "../../test/renderWithConsoleProviders";
import { ApiError } from "../../rpc/http";
import * as configGraph from "../../rpc/configGraph";
import * as logs from "../../rpc/logs";
import * as management from "../../rpc/management";
import type { LogEntry, UsageStats } from "../../rpc/types";
import { AppShell } from "../../app/App";
import { configGraphFixture } from "../../test/configGraphFixtures";
import {
  expectPanelElementToBeFlat,
  expectPanelRuleToAvoidEdges,
  expectPanelStateRuleToStayFlat
} from "../../test/panelStyleAssertions";
import { OverviewPage } from "./OverviewPage";
import { MemoryRouter } from "react-router-dom";

function metricCard(label: string): HTMLElement {
  const labelEl = screen
    .getAllByText(label)
    .find((el) => el.classList.contains("usage-metric__label"));
  if (!labelEl) {
    throw new Error(`usage metric not found for label: ${label}`);
  }
  return labelEl.closest(".usage-metric") as HTMLElement;
}

function usageDurationPill(): HTMLElement {
  const pill = document.querySelector(".usage-heading-controls .status-pill");
  if (!pill) {
    throw new Error("usage duration pill not found");
  }
  return pill as HTMLElement;
}

describe("OverviewPage", () => {
  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
    restoreNavigatorClipboard();
    restoreURLMethods();
  });

  test("shows unknown rather than zero when pricing is unavailable", async () => {
    const data = usageStats();
    data.totals.total_cost = null;
    data.totals.cost_unknown = true;
    data.by_model[0].cost = null;
    data.by_model[0].avg_cost_per_mtoken = null;
    vi.spyOn(management, "getUsageStats").mockResolvedValue(data);
    vi.spyOn(logs, "getRecentLogs").mockResolvedValue([]);
    vi.spyOn(logs, "createLogStream").mockResolvedValue(new Response(new ReadableStream<Uint8Array>()));
    renderWithConsoleProviders(<OverviewPage />);
    await screen.findAllByText("unknown");
    expect(within(metricCard("Total cost")).getByText("unknown")).toBeInTheDocument();
    expect(screen.getAllByText("unknown").length).toBeGreaterThanOrEqual(3);
    expect(screen.queryByText("¥0.00")).not.toBeInTheDocument();
    const chart = screen.getByRole("img", { name: /Cost by model chart/ });
    expect(chart).toHaveAttribute("aria-label", expect.stringContaining(`${data.by_model[0].model}: unknown`));
    expect(within(chart).getAllByText("unknown")).toHaveLength(2);
    expect(chart.querySelectorAll(".usage-chart__segment")).toHaveLength(0);
  });

  test("preserves known chart amounts alongside unknown costs", async () => {
    const data = usageStats();
    data.totals.total_cost = null;
    data.by_model.push({ ...data.by_model[0], model: "unpriced", cost: null, avg_cost_per_mtoken: null });
    vi.spyOn(management, "getUsageStats").mockResolvedValue(data);
    vi.spyOn(logs, "getRecentLogs").mockResolvedValue([]);
    vi.spyOn(logs, "createLogStream").mockResolvedValue(new Response(new ReadableStream<Uint8Array>()));
    renderWithConsoleProviders(<OverviewPage />);
    const chart = await screen.findByRole("img", { name: /Cost by model chart.*unpriced: unknown/ });
    expect(within(chart).getByText("0.42")).toBeInTheDocument();
    expect(within(chart).getAllByText("unknown")).toHaveLength(2);
    expect(chart.querySelectorAll(".usage-chart__segment")).toHaveLength(1);
    expect(chart.querySelector(".usage-chart__segment")).toHaveStyle({ inlineSize: "100%" });
  });

  test("renders a usage dashboard with model charts and bottom logs instead of runtime status panels", async () => {
    vi.spyOn(configGraph, "getConfigGraph").mockResolvedValue(
      configGraphFixture({
        runtime: {
          status: "runtimeRejected",
          errors: [
            {
              resourceKind: "provider",
              resourceId: "anthropic",
              field: "base_url",
              code: "runtimeReloadRejected",
              message: "upstream rejected reload"
            }
          ]
        },
        validation: {
          valid: false,
          errors: [
            {
              resourceKind: "route",
              resourceId: "primary",
              field: "model",
              code: "missingModel",
              message: "route model missing"
            }
          ]
        }
      })
    );
    vi.spyOn(management, "getUsageStats").mockResolvedValue(usageStats());
    vi.spyOn(logs, "getRecentLogs").mockResolvedValue(logEntries());
    vi.spyOn(logs, "createLogStream").mockResolvedValue(new Response(new ReadableStream<Uint8Array>()));

    renderWithConsoleProviders(
      <MemoryRouter>
        <AppShell content={<OverviewPage />} />
      </MemoryRouter>
    );

    expect(await screen.findByRole("heading", { name: "Usage Analytics" })).toBeInTheDocument();
    await screen.findAllByText("Requests");
    expect(within(metricCard("Requests")).getByText("2")).toBeInTheDocument();
    expect(within(metricCard("Input tokens")).getByText("300")).toBeInTheDocument();
    expect(within(metricCard("Output tokens")).getByText("80")).toBeInTheDocument();
    expect(within(metricCard("Cache hit")).getByText("40%")).toBeInTheDocument();
    expect(within(metricCard("Total cost")).getByText("¥0.42")).toBeInTheDocument();
    expect(screen.getByRole("img", { name: /Token split chart.*Input tokens: 300.*Output tokens: 80/ })).toBeInTheDocument();
    expect(screen.getByRole("img", { name: /Cache split chart.*Cache write: 40.*Cache read: 120/ })).toBeInTheDocument();
    expect(screen.getByRole("img", { name: /Cost by model chart.*claude-sonnet: 0.42/ })).toBeInTheDocument();
    expect(getMaterialFilterChip(document.body, "This session")).toHaveProperty("selected", true);
    expect(getMaterialFilterChip(document.body, "24h")).toHaveProperty("selected", false);

    fireEvent.click(getMaterialFilterChip(document.body, "24h"));

    expect(getMaterialFilterChip(document.body, "This session")).toHaveProperty("selected", false);
    expect(getMaterialFilterChip(document.body, "24h")).toHaveProperty("selected", true);
    await waitFor(() => {
      expect(management.getUsageStats).toHaveBeenCalledWith("24h");
    });

    const modelRow = await screen.findByRole("row", { name: /claude-sonnet/i });
    expect(modelRow).toHaveTextContent("claude-3-5-sonnet");
    expect(modelRow).toHaveTextContent("¥0.42");
    expect(modelRow).toHaveTextContent("¥1,105.26/M");

    expect(screen.getByRole("region", { name: "Backend logs" })).toBeInTheDocument();
    expect(screen.getByText(/server started/)).toBeInTheDocument();
    expect(screen.queryByText("runtimeRejected")).not.toBeInTheDocument();
    expect(screen.queryByText("upstream rejected reload")).not.toBeInTheDocument();
    expect(screen.queryByText("Validation")).not.toBeInTheDocument();
  });

  test("keeps usage background panels tonal without borders, glow, or hover lift", async () => {
    vi.spyOn(configGraph, "getConfigGraph").mockResolvedValue(configGraphFixture());
    vi.spyOn(management, "getUsageStats").mockResolvedValue(usageStats());
    vi.spyOn(logs, "getRecentLogs").mockResolvedValue(logEntries());
    vi.spyOn(logs, "createLogStream").mockResolvedValue(new Response(new ReadableStream<Uint8Array>()));

    renderWithConsoleProviders(
      <MemoryRouter>
        <AppShell content={<OverviewPage />} />
      </MemoryRouter>
    );

    await screen.findAllByText("Requests");

    const panels = [
      document.querySelector(".usage-dashboard"),
      document.querySelector(".overview-logs"),
      ...Array.from(document.querySelectorAll(".usage-metric")),
      ...Array.from(document.querySelectorAll(".usage-chart"))
    ];
    for (const panel of panels) {
      expect(panel).toBeInTheDocument();
      expectPanelElementToBeFlat(panel!);
    }
    expectPanelRuleToAvoidEdges(".usage-metric");
    expectPanelStateRuleToStayFlat(".usage-metric:hover");
    expectPanelRuleToAvoidEdges(".usage-chart");
    expectPanelStateRuleToStayFlat(".usage-chart:focus-visible");
  });

  test("localizes usage units and chart accessibility text in Chinese locale", async () => {
    vi.spyOn(configGraph, "getConfigGraph").mockResolvedValue(configGraphFixture());
    vi.spyOn(management, "getUsageStats").mockResolvedValue(usageStats());
    vi.spyOn(logs, "getRecentLogs").mockResolvedValue(logEntries());
    vi.spyOn(logs, "createLogStream").mockResolvedValue(new Response(new ReadableStream<Uint8Array>()));

    renderWithConsoleProviders(<OverviewPage />, { locale: "zh-CN" });

    await screen.findByRole("heading", { name: "用量分析" });
    await screen.findAllByText("请求");
    expect(screen.getByRole("img", { name: /Token 拆分图表。输入 Token：300；输出 Token：80/ })).toBeInTheDocument();
    expect(screen.getByRole("img", { name: /缓存拆分图表。缓存写入：40；缓存读取：120/ })).toBeInTheDocument();
    const modelRow = await screen.findByRole("row", { name: /claude-sonnet/i });
    expect(modelRow).toHaveTextContent("¥1,105.26/百万");
  });

  test("keeps the current usage dashboard visible while a newly selected range is loading", async () => {
    vi.spyOn(configGraph, "getConfigGraph").mockResolvedValue(configGraphFixture());
    vi.spyOn(logs, "getRecentLogs").mockResolvedValue(logEntries());
    vi.spyOn(logs, "createLogStream").mockResolvedValue(new Response(new ReadableStream<Uint8Array>()));
    const usageRequest = vi.spyOn(management, "getUsageStats").mockImplementation((range = "session") => {
      if (range === "session") {
        return Promise.resolve(usageStats());
      }
      return new Promise<UsageStats>(() => undefined);
    });

    renderWithConsoleProviders(<OverviewPage />);

    await screen.findAllByText("Requests");
    expect(within(metricCard("Requests")).getByText("2")).toBeInTheDocument();

    fireEvent.click(getMaterialFilterChip(document.body, "24h"));

    expect(getMaterialFilterChip(document.body, "24h")).toHaveProperty("selected", true);
    await waitFor(() => {
      expect(usageRequest).toHaveBeenCalledWith("24h");
    });
    expect(screen.queryByRole("heading", { name: "Loading" })).not.toBeInTheDocument();
    expect(within(metricCard("Requests")).getByText("2")).toBeInTheDocument();
    expect(screen.getByRole("table", { name: "Model usage table" })).toBeInTheDocument();
  });

  test("updates the active session usage duration every second without refetching", async () => {
    vi.useFakeTimers();
    vi.spyOn(configGraph, "getConfigGraph").mockResolvedValue(configGraphFixture());
    vi.spyOn(management, "getUsageStats").mockResolvedValue(usageStats());
    vi.spyOn(logs, "getRecentLogs").mockResolvedValue(logEntries());
    vi.spyOn(logs, "createLogStream").mockResolvedValue(new Response(new ReadableStream<Uint8Array>()));

    renderWithConsoleProviders(<OverviewPage />);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
      await Promise.resolve();
    });
    expect(screen.getAllByText("Requests").length).toBeGreaterThan(0);
    expect(usageDurationPill()).toHaveTextContent("1m");

    act(() => {
      vi.advanceTimersByTime(2000);
    });

    expect(usageDurationPill()).toHaveTextContent("1m 2s");
    expect(management.getUsageStats).toHaveBeenCalledTimes(1);
  });

  test("does not increment fixed usage range durations", async () => {
    vi.useFakeTimers();
    vi.spyOn(configGraph, "getConfigGraph").mockResolvedValue(configGraphFixture());
    vi.spyOn(management, "getUsageStats").mockImplementation((range = "session") => {
      if (range === "24h") {
        return Promise.resolve(usageStats({ duration: "24h" }));
      }
      return Promise.resolve(usageStats());
    });
    vi.spyOn(logs, "getRecentLogs").mockResolvedValue(logEntries());
    vi.spyOn(logs, "createLogStream").mockResolvedValue(new Response(new ReadableStream<Uint8Array>()));

    renderWithConsoleProviders(<OverviewPage />);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
    });
    fireEvent.click(getMaterialFilterChip(document.body, "24h"));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
    });

    expect(usageDurationPill()).toHaveTextContent("24h");

    act(() => {
      vi.advanceTimersByTime(2000);
    });

    expect(usageDurationPill()).toHaveTextContent("24h");
  });

  test("does not increment placeholder duration while returning to the active session range", async () => {
    vi.useFakeTimers();
    vi.spyOn(configGraph, "getConfigGraph").mockResolvedValue(configGraphFixture());
    vi.spyOn(management, "getUsageStats").mockImplementation((range = "session") => {
      if (range === "24h") {
        return Promise.resolve(usageStats({ duration: "24h" }));
      }
      return new Promise<UsageStats>(() => undefined);
    });
    vi.spyOn(logs, "getRecentLogs").mockResolvedValue(logEntries());
    vi.spyOn(logs, "createLogStream").mockResolvedValue(new Response(new ReadableStream<Uint8Array>()));

    renderWithConsoleProviders(<OverviewPage />);

    fireEvent.click(getMaterialFilterChip(document.body, "24h"));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
    });
    expect(usageDurationPill()).toHaveTextContent("24h");

    fireEvent.click(getMaterialFilterChip(document.body, "This session"));
    await act(async () => {
      vi.advanceTimersByTime(2000);
    });

    expect(usageDurationPill()).toHaveTextContent("24h");
  });

  test("keeps the embedded log panel searchable and clearable", async () => {
    vi.spyOn(configGraph, "getConfigGraph").mockResolvedValue(configGraphFixture());
    vi.spyOn(management, "getUsageStats").mockResolvedValue(usageStats());
    vi.spyOn(logs, "getRecentLogs").mockResolvedValue(logEntries());
    vi.spyOn(logs, "createLogStream").mockResolvedValue(new Response(new ReadableStream<Uint8Array>()));

    renderWithConsoleProviders(<OverviewPage />);

    expect(await screen.findByText(/server started/)).toBeInTheDocument();

    const searchField = getMaterialTextField(document.body, "Search logs");
    setMaterialTextFieldValue(searchField, "database");

    expect(screen.queryByText(/server started/)).not.toBeInTheDocument();
    expect(screen.getByText(/database unavailable/)).toBeInTheDocument();

    fireEvent.click(getMaterialIconButton(document.body, "Clear log search"));

    expect(screen.getByText(/server started/)).toBeInTheDocument();
    expect(screen.getByText(/database unavailable/)).toBeInTheDocument();
  });

  test("shows usage empty state while keeping logs available", async () => {
    vi.spyOn(configGraph, "getConfigGraph").mockResolvedValue(configGraphFixture());
    vi.spyOn(management, "getUsageStats").mockResolvedValue({
      totals: {
        requests: 0,
        input_tokens: 0,
        output_tokens: 0,
        cache_creation: 0,
        cache_read: 0,
        cache_hit_rate: 0,
        cache_write_rate: 0,
        cache_rw_ratio: 0,
        total_cost: 0,
        duration: "0s"
      },
      by_model: []
    });
    vi.spyOn(logs, "getRecentLogs").mockResolvedValue(logEntries());
    vi.spyOn(logs, "createLogStream").mockResolvedValue(new Response(new ReadableStream<Uint8Array>()));

    renderWithConsoleProviders(<OverviewPage />);

    expect(await screen.findByText("No usage has been recorded yet.")).toBeInTheDocument();
    expect(screen.getByRole("img", { name: /Token split chart/ })).toBeInTheDocument();
    expect(screen.getByRole("img", { name: /Cache split chart/ })).toBeInTheDocument();
    expect(screen.getByRole("img", { name: /Cost by model chart/ })).toBeInTheDocument();
    expect(screen.getByRole("table", { name: "Model usage table" })).toBeInTheDocument();
    expect(screen.getByText(/server started/)).toBeInTheDocument();
  });

  test("keeps usage dashboard and logs visible when graph API store is unavailable", async () => {
    vi.spyOn(configGraph, "getConfigGraph").mockRejectedValue(
      new ApiError(503, "store_unavailable", "配置存储不可用")
    );
    vi.spyOn(management, "getUsageStats").mockResolvedValue(usageStats());
    vi.spyOn(logs, "getRecentLogs").mockResolvedValue([]);
    vi.spyOn(logs, "createLogStream").mockResolvedValue(new Response(new ReadableStream<Uint8Array>()));

    renderWithConsoleProviders(<OverviewPage />);

    expect(await screen.findByRole("heading", { name: "Usage Analytics" })).toBeInTheDocument();
    await screen.findAllByText("Requests");
    expect(within(metricCard("Requests")).getByText("2")).toBeInTheDocument();
    expect(screen.getByRole("region", { name: "Backend logs" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Configuration graph unavailable" })).toBeInTheDocument();
  });
});

function getMaterialFilterChip(container: ParentNode, label: string) {
  const element = Array.from(container.querySelectorAll("md-filter-chip")).find(
    (chip) => chip.textContent?.trim() === label
  );
  if (!element) {
    throw new Error(`Expected a Material Web filter chip labelled "${label}".`);
  }
  return element as HTMLElement & { selected: boolean };
}

function getMaterialTextField(container: ParentNode, label: string) {
  const element = Array.from(container.querySelectorAll("md-outlined-text-field")).find(
    (textField) => (textField as HTMLElement & { label?: string }).label === label
  );
  if (!element) {
    throw new Error(`Expected a Material Web outlined text field labelled "${label}".`);
  }
  return element as HTMLElement & { value: string };
}

function getMaterialIconButton(container: ParentNode, label: string) {
  const element = Array.from(container.querySelectorAll("md-icon-button")).find(
    (iconButton) => iconButton.getAttribute("aria-label") === label
  );
  if (!element) {
    throw new Error(`Expected a Material Web icon button labelled "${label}".`);
  }
  return element as HTMLElement;
}

function setMaterialTextFieldValue(element: HTMLElement & { value: string }, value: string) {
  act(() => {
    element.value = value;
    element.dispatchEvent(new InputEvent("input", { bubbles: true, composed: true }));
  });
}

function usageStats(overrides: Partial<UsageStats["totals"]> = {}): UsageStats {
  return {
    totals: {
      requests: 2,
      input_tokens: 300,
      output_tokens: 80,
      cache_creation: 40,
      cache_read: 120,
      cache_hit_rate: 40,
      cache_write_rate: 13.3,
      cache_rw_ratio: 3,
      total_cost: 0.42,
      duration: "1m",
      ...overrides
    },
    by_model: [
      {
        model: "claude-sonnet",
        actual_model: "claude-3-5-sonnet",
        requests: 2,
        input_tokens: 300,
        output_tokens: 80,
        cache_creation: 40,
        cache_read: 120,
        cache_hit_rate: 40,
        cost: 0.42,
        avg_cost_per_mtoken: 1105.26
      }
    ]
  };
}

function logEntries(): LogEntry[] {
  return [
    {
      timestamp: "2026-06-07T00:00:00Z",
      level: "INFO",
      message: "server started",
      raw: "time=2026-06-07T00:00:00Z level=INFO msg=server-started"
    },
    {
      timestamp: "2026-06-07T00:00:01Z",
      level: "ERROR",
      message: "database unavailable",
      raw: "time=2026-06-07T00:00:01Z level=ERROR msg=database-unavailable"
    }
  ];
}

const clipboardDescriptor = Object.getOwnPropertyDescriptor(Navigator.prototype, "clipboard");
const createObjectURLDescriptor = Object.getOwnPropertyDescriptor(URL, "createObjectURL");
const revokeObjectURLDescriptor = Object.getOwnPropertyDescriptor(URL, "revokeObjectURL");

function restoreNavigatorClipboard() {
  if (clipboardDescriptor) {
    Object.defineProperty(Navigator.prototype, "clipboard", clipboardDescriptor);
  } else {
    Reflect.deleteProperty(navigator, "clipboard");
  }
}

function restoreURLMethods() {
  if (createObjectURLDescriptor) {
    Object.defineProperty(URL, "createObjectURL", createObjectURLDescriptor);
  }
  if (revokeObjectURLDescriptor) {
    Object.defineProperty(URL, "revokeObjectURL", revokeObjectURLDescriptor);
  }
}
