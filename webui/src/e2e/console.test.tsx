import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, test, vi } from "vitest";
import { AppShell } from "../app/App";
import { DefaultsPage } from "../features/defaults/DefaultsPage";
import { LogsPage } from "../features/logs/LogsPage";
import { ModelsProvidersPage } from "../features/modelProviders/ModelsProvidersPage";
import { OverviewPage } from "../features/overview/OverviewPage";
import { SecurityPage } from "../features/security/SecurityPage";
import { CONSOLE_THEME_STORAGE_KEY } from "../theme/ThemeProvider";
import { configGraphFixture } from "../test/configGraphFixtures";
import { renderWithConsoleProviders } from "../test/renderWithConsoleProviders";
import type { ConfigGraph, FieldError, PatchResponse } from "../rpc/types";

type FetchCall = {
  url: string;
  init?: RequestInit;
  body?: unknown;
};

describe("config graph console smoke flow", () => {
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    localStorage.clear();
  });

  test("primary navigation has no config file or apply entry points", () => {
    renderWithConsoleProviders(
      <MemoryRouter>
        <AppShell content={<div>Console content</div>} />
      </MemoryRouter>
    );

    const nav = screen.getByRole("navigation", { name: /console sections/i });
    expect(nav).toHaveTextContent("Overview");
    expect(nav).toHaveTextContent("Models & Providers");
    expect(nav).toHaveTextContent("Search & Tools");
    expect(nav).not.toHaveTextContent("Logs");
    expect(nav).not.toHaveTextContent("Config");
    expect(nav).not.toHaveTextContent("YAML");
    expect(nav).not.toHaveTextContent("Diagnostics");
    expect(screen.getByRole("main", { name: "Console route content" })).toHaveTextContent("Console content");
    expect(screen.queryByRole("button", { name: /^apply$/i })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /apply changes/i })).not.toBeInTheDocument();
  });

  test("models and providers render shared resource cards", async () => {
    mockFetch({
      graph: configGraphFixture()
    });

    renderWithConsoleProviders(<ModelsProvidersPage />);

    expect(await screen.findByRole("heading", { level: 2, name: "Providers (1)" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { level: 3, name: "anthropic" })).toBeInTheDocument();
  });

  test("overview loads model usage and embedded logs", async () => {
    mockFetch({
      graph: configGraphFixture(),
      logs: [
        {
          timestamp: "2026-06-07T00:00:00Z",
          level: "INFO",
          message: "server started",
          raw: "time=2026-06-07T00:00:00Z level=INFO msg=server-started"
        }
      ],
      usage: usageFixture()
    });

    renderWithConsoleProviders(<OverviewPage />);

    expect(document.documentElement.dataset.theme).toBe("bauhaus-dark");
    expect(localStorage.getItem(CONSOLE_THEME_STORAGE_KEY)).toBe("bauhaus-dark");
    expect(await screen.findByRole("heading", { name: "Usage Analytics" })).toBeInTheDocument();
    const requestsLabel = (await screen.findAllByText("Requests")).find((el) =>
      el.classList.contains("usage-metric__label")
    );
    expect(within(requestsLabel!.closest(".usage-metric") as HTMLElement).getByText("2")).toBeInTheDocument();
    expect(screen.getByRole("img", { name: /Token split chart/ })).toBeInTheDocument();
    expect(screen.getByRole("region", { name: "Backend logs" })).toBeInTheDocument();
    expect(screen.getByText(/server started/)).toBeInTheDocument();
    expect(screen.queryByText("Transform")).not.toBeInTheDocument();
    expect(screen.queryByText("rev-1")).not.toBeInTheDocument();
  });

  test("editing a field patches the config graph directly", async () => {
    const graph = configGraphFixture();
    const { calls } = mockFetch({
      graph,
      patch: {
        result: "committed",
        revision: "rev-2",
        graph: configGraphFixture({ revision: "rev-2" })
      }
    });

    renderWithConsoleProviders(<DefaultsPage />);

    await screen.findByLabelText("Defaults main");
    const modelField = getMaterialTextField(document, "Default model");
    setMaterialTextFieldValue(modelField, "gpt-4o");
    (() => { const __c = modelField.querySelector?.("input, textarea, select") as HTMLElement | null; fireEvent.blur(__c ?? modelField); })();

    await waitFor(() => {
      expect(findPatch(calls)?.body).toEqual({
        baseRevision: "rev-1",
        changes: [
          {
            kind: "defaults",
            id: "main",
            field: "model",
            value: "gpt-4o"
          }
        ]
      });
    });
  });

  test("draft rejection keeps the edited value and shows the field error", async () => {
    const error = fieldError("defaults", "main", "model", "draftRejected", "Model is invalid");
    mockFetch({
      graph: configGraphFixture(),
      patch: {
        result: "draftRejected",
        revision: "rev-1",
        errors: [error]
      }
    });

    renderWithConsoleProviders(<DefaultsPage />);

    await screen.findByLabelText("Defaults main");
    const model = getMaterialTextField(document, "Default model");
    setMaterialTextFieldValue(model, "invalid-model");
    (() => { const __c = model.querySelector?.("input, textarea, select") as HTMLElement | null; fireEvent.blur(__c ?? model); })();

    expect(model.value).toBe("invalid-model");
    expect(await screen.findByRole("alert")).toHaveTextContent("Model is invalid");
  });

  test("runtime rejection rolls back a critical field", async () => {
    const error = fieldError("server", "main", "addr", "runtimeRejected", "Runtime rejected");
    mockFetch({
      graph: configGraphFixture(),
      patch: {
        result: "runtimeRejected",
        revision: "rev-1",
        errors: [error],
        rollbackValue: ":38440"
      }
    });

    renderWithConsoleProviders(<SecurityPage />);

    await screen.findByLabelText("Server main");
    const address = getMaterialTextField(document, "Listen address");
    setMaterialTextFieldValue(address, ":9999");
    (() => { const __c = address.querySelector?.("input, textarea, select") as HTMLElement | null; fireEvent.blur(__c ?? address); })();

    await waitFor(() => {
      expect(address.value).toBe(":38440");
    });
    expect(await screen.findByRole("alert")).toHaveTextContent("Runtime rejected");
  });

  test("logs page renders recent backend log lines and level filters", async () => {
    mockFetch({
      graph: configGraphFixture(),
      logs: [
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
      ]
    });

    renderWithConsoleProviders(<LogsPage />);

    expect(await screen.findByText(/server started/)).toBeInTheDocument();
    expect(screen.getByText(/database unavailable/)).toBeInTheDocument();

    fireEvent.click(getMaterialFilterChip(document, "ERROR"));

    expect(screen.queryByText(/server started/)).not.toBeInTheDocument();
    expect(screen.getByText(/database unavailable/)).toBeInTheDocument();
    expect(screen.getByText("1 of 2 logs")).toBeInTheDocument();
  });
});

function mockFetch({
  graph,
  patch,
  logs = [],
  usage = emptyUsageFixture()
}: {
  graph: ConfigGraph;
  patch?: PatchResponse;
  logs?: unknown[];
  usage?: unknown;
}) {
  const calls: FetchCall[] = [];
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    const method = init?.method ?? "GET";
    const body = parseBody(init?.body);
    calls.push({ url, init, body });

    if (url === "/api/v1/config/graph" && method === "GET") {
      return jsonResponse(graph);
    }
    if (url === "/api/v1/config/graph" && method === "PATCH") {
      if (!patch) {
        throw new Error("Unexpected config graph patch");
      }
      return jsonResponse(patch);
    }
    if (url.startsWith("/api/v1/logs/recent")) {
      return jsonResponse(logs);
    }
    if (url === "/api/v1/logs/stream") {
      return new Response(new ReadableStream<Uint8Array>(), { status: 200 });
    }
    if (url === "/api/v1/stats/usage") {
      return jsonResponse(usage);
    }
    throw new Error(`Unexpected fetch: ${method} ${url}`);
  });
  vi.stubGlobal("fetch", fetchMock);
  return { calls, fetchMock };
}

function jsonResponse(payload: unknown) {
  return new Response(JSON.stringify(payload), {
    status: 200,
    headers: { "Content-Type": "application/json" }
  });
}

function parseBody(body: BodyInit | null | undefined) {
  return typeof body === "string" ? JSON.parse(body) as unknown : undefined;
}

function findPatch(calls: FetchCall[]) {
  return calls.find((call) => call.url === "/api/v1/config/graph" && call.init?.method === "PATCH");
}

type MaterialTextFieldElement = HTMLElement & {
  label: string;
  value: string;
};

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

function materialElementLabel(element: HTMLElement & { label?: string }) {
  const labelledBy = element.getAttribute("aria-labelledby");
  if (labelledBy) {
    return labelledBy
      .split(/\s+/)
      .map((id) => document.getElementById(id)?.textContent?.trim() ?? "")
      .filter(Boolean)
      .join(" ");
  }
  return element.getAttribute("data-label") || element.label || element.getAttribute("aria-label") || element.getAttribute("label") || element.querySelector("label")?.textContent?.replace(/\s*\*$/, "").trim() || "";
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

function getMaterialFilterChip(container: ParentNode, label: string) {
  const element = Array.from(container.querySelectorAll("button.bh-chip")).find(
    (candidate) => candidate.textContent?.trim() === label
  );
  if (!element) {
    throw new Error(`Expected a Material Web filter chip labelled "${label}".`);
  }
  return element as HTMLElement;
}

function fieldError(
  resourceKind: FieldError["resourceKind"],
  resourceId: string,
  field: string,
  code: string,
  message: string
): FieldError {
  return { resourceKind, resourceId, field, code, message };
}

function usageFixture() {
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
      duration: "1m"
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

function emptyUsageFixture() {
  return {
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
  };
}
