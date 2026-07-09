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
    expect(getMaterialFilterChip(document.body, "This session")).toHaveAttribute("aria-pressed", "true");
    expect(getMaterialFilterChip(document.body, "24h")).toHaveAttribute("aria-pressed", "false");

    fireEvent.click(getMaterialFilterChip(document.body, "24h"));

    expect(getMaterialFilterChip(document.body, "This session")).toHaveAttribute("aria-pressed", "false");
    expect(getMaterialFilterChip(document.body, "24h")).toHaveAttribute("aria-pressed", "true");
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

    expect(getMaterialFilterChip(document.body, "24h")).toHaveAttribute("aria-pressed", "true");
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
  const element = Array.from(container.querySelectorAll("button.bh-chip")).find(
    (chip) => chip.textContent?.trim() === label
  );
  if (!element) {
    throw new Error(`Expected a Material Web filter chip labelled "${label}".`);
  }
  return element as HTMLElement & { selected: boolean };
}

function getMaterialTextField(container: ParentNode, label: string) {
  const element = Array.from(container.querySelectorAll<HTMLElement>(".bh-field:not(.bh-select)")).find(
    (candidate) => materialElementLabel(candidate) === label && !candidate.querySelector("select")
  );
  if (!element) {
    throw new Error(`Expected a text field labelled "${label}".`);
  }
  const control = element.querySelector("input, textarea") as HTMLInputElement | HTMLTextAreaElement | null;
  Object.defineProperty(element, "label", { configurable: true, get: () => materialElementLabel(element) });
  Object.defineProperty(element, "value", {
    configurable: true,
    get: () => control?.value ?? "",
    set: (v: string) => {
      if (!control) return;
      const proto = Object.getPrototypeOf(control);
      const desc = Object.getOwnPropertyDescriptor(proto, "value");
      desc?.set?.call(control, v);
    }
  });
  Object.defineProperty(element, "supportingText", {
    configurable: true,
    get: () => element.querySelector(".bh-field__support")?.textContent?.trim() ?? ""
  });
  Object.defineProperty(element, "type", {
    configurable: true,
    get: () => (control && "type" in control ? (control as HTMLInputElement).type : element.getAttribute("data-type") ?? "text")
  });
  Object.defineProperty(element, "spellcheck", {
    configurable: true,
    get: () => control?.spellcheck ?? false
  });
  // attribute-style accessors used by testing-library toHaveAttribute
  const originalGetAttribute = element.getAttribute.bind(element);
  element.getAttribute = ((name: string) => {
    if (name === "spellcheck" || name === "spellCheck") {
      return control ? String(control.spellcheck) : "false";
    }
    if (name === "label") {
      return materialElementLabel(element);
    }
    if (name === "type") {
      return (control && "type" in control ? (control as HTMLInputElement).type : null);
    }
    return originalGetAttribute(name);
  }) as typeof element.getAttribute;
  return element as any;
}

function getMaterialIconButton(container: ParentNode, label: string) {
  const element = Array.from(container.querySelectorAll("button.bh-icon-button")).find(
    (iconButton) => iconButton.getAttribute("aria-label") === label
  );
  if (!element) {
    throw new Error(`Expected an icon button labelled "${label}".`);
  }
  return element as HTMLElement;
}

function setMaterialTextFieldValue(element: HTMLElement, value: string) {
  const control = (element.matches("input, textarea") ? element : element.querySelector("input, textarea")) as HTMLInputElement | HTMLTextAreaElement | null;
  if (!control) {
    throw new Error("Expected input/textarea in text field");
  }
  act(() => {
    fireEvent.input(control, { target: { value } });
    fireEvent.change(control, { target: { value } });
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


function materialElementLabel(element: HTMLElement & { label?: string }) {
  const dataLabel = element.getAttribute("data-label");
  if (dataLabel) {
    return dataLabel;
  }
  const labelledBy = element.getAttribute("aria-labelledby");
  if (labelledBy) {
    return labelledBy
      .split(/\s+/)
      .map((id) => document.getElementById(id)?.textContent?.trim() ?? "")
      .filter(Boolean)
      .join(" ");
  }
  const labelEl = element.querySelector("label");
  if (labelEl?.textContent) {
    return labelEl.textContent.replace(/\s*\*$/, "").trim();
  }
  return element.label || element.getAttribute("aria-label") || element.getAttribute("label") || "";
}
