import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, test, vi } from "vitest";
import { renderWithConsoleProviders } from "../../test/renderWithConsoleProviders";
import * as logs from "../../rpc/logs";
import type { LogEntry } from "../../rpc/types";
import { LogsPage } from "./LogsPage";

describe("LogsPage", () => {
  afterEach(() => {
    vi.restoreAllMocks();
    restoreNavigatorClipboard();
    restoreURLMethods();
  });

  test("renders recent raw logs, filters visible rows, and exposes log actions", async () => {
    vi.spyOn(logs, "getRecentLogs").mockResolvedValue(logEntries());
    vi.spyOn(logs, "createLogStream").mockResolvedValue(
      new Response(new ReadableStream<Uint8Array>())
    );

    renderWithConsoleProviders(<LogsPage />);

    expect(await screen.findByText(/server started/)).toBeInTheDocument();
    expect(screen.getByText(/database unavailable/)).toBeInTheDocument();
    expect(screen.getByText("3 of 3 logs")).toBeInTheDocument();
    expect(getMaterialFilterChip(document.body, "Pause")).toBeInTheDocument();
    expect(getMaterialButton(document.body, "Copy")).toBeInTheDocument();
    expect(getMaterialButton(document.body, "Download")).toBeInTheDocument();
    expect(getMaterialTextField(document.body, "Search logs")).toHaveClass("material-text-field--single-line");
    const topActions = document.querySelector(".logs-panel__actions");
    const toolbarActions = document.querySelector(".logs-toolbar__actions");
    expect(topActions).toBeInTheDocument();
    expect(toolbarActions).toBeInTheDocument();
    expect(getMaterialButton(topActions!, "Copy")).toBeInTheDocument();
    expect(getMaterialButton(topActions!, "Download")).toBeInTheDocument();
    expect(toolbarActions!.querySelectorAll("button.bh-button--outlined, .bh-button--outlined")).toHaveLength(0);

    setMaterialTextFieldValue(getMaterialTextField(document.body, "Search logs"), "database");

    expect(screen.queryByText(/server started/)).not.toBeInTheDocument();
    expect(screen.getByText(/database unavailable/)).toBeInTheDocument();
    expect(screen.getByText("1 of 3 logs")).toBeInTheDocument();

    fireEvent.click(getMaterialFilterChip(document.body, "Pause"));
    expect(getMaterialFilterChip(document.body, "Follow")).toBeInTheDocument();
  });

  test("filters by level and copies only visible raw lines", async () => {
    vi.spyOn(logs, "getRecentLogs").mockResolvedValue(logEntries());
    vi.spyOn(logs, "createLogStream").mockResolvedValue(
      new Response(new ReadableStream<Uint8Array>())
    );
    const writeText = installClipboard();

    renderWithConsoleProviders(<LogsPage />);

    expect(await screen.findByText(/server started/)).toBeInTheDocument();

    fireEvent.click(getMaterialFilterChip(document.body, "ERROR"));
    expect(screen.queryByText(/server started/)).not.toBeInTheDocument();
    expect(screen.getByText(/database unavailable/)).toBeInTheDocument();
    expect(screen.getByText("1 of 3 logs")).toBeInTheDocument();

    fireEvent.click(getMaterialButton(document.body, "Copy"));

    await waitFor(() => {
      expect(writeText).toHaveBeenCalledWith("time=2026-06-07T00:00:01Z level=ERROR msg=database-unavailable");
    });
  });

  test("uses a segmented follow control with clear pressed state", async () => {
    vi.spyOn(logs, "getRecentLogs").mockResolvedValue(logEntries());
    vi.spyOn(logs, "createLogStream").mockResolvedValue(
      new Response(new ReadableStream<Uint8Array>())
    );

    renderWithConsoleProviders(<LogsPage />);

    expect(await screen.findByText(/server started/)).toBeInTheDocument();

    const followMode = screen.getByRole("group", { name: "Live follow mode" });
    expect(getMaterialFilterChip(followMode, "Follow")).toHaveAttribute("aria-pressed", "true");
    expect(getMaterialFilterChip(followMode, "Pause")).toHaveAttribute("aria-pressed", "false");

    fireEvent.click(getMaterialFilterChip(followMode, "Pause"));

    expect(getMaterialFilterChip(followMode, "Follow")).toHaveAttribute("aria-pressed", "false");
    expect(getMaterialFilterChip(followMode, "Pause")).toHaveAttribute("aria-pressed", "true");
  });

  test("localizes log row labels in Chinese locale", async () => {
    vi.spyOn(logs, "getRecentLogs").mockResolvedValue(logEntries());
    vi.spyOn(logs, "createLogStream").mockResolvedValue(
      new Response(new ReadableStream<Uint8Array>())
    );

    renderWithConsoleProviders(<LogsPage />, { locale: "zh-CN" });

    expect(await screen.findByLabelText("日志 1")).toHaveTextContent("server started");
    expect(screen.getByLabelText("日志 2")).toHaveTextContent("database unavailable");
  });

  test("shows empty feedback and disables log actions when filters hide every row", async () => {
    vi.spyOn(logs, "getRecentLogs").mockResolvedValue(logEntries());
    vi.spyOn(logs, "createLogStream").mockResolvedValue(
      new Response(new ReadableStream<Uint8Array>())
    );

    renderWithConsoleProviders(<LogsPage />);

    expect(await screen.findByText(/server started/)).toBeInTheDocument();

    setMaterialTextFieldValue(getMaterialTextField(document.body, "Search logs"), "no matching backend event");

    expect(screen.getByText("No logs match the current filters.")).toBeInTheDocument();
    expect(getMaterialButton(document.body, "Copy")).toHaveProperty("disabled", true);
    expect(getMaterialButton(document.body, "Download")).toHaveProperty("disabled", true);
    expect(screen.getByText("0 of 3 logs")).toBeInTheDocument();
  });

  test("shows a calm empty state when no recent logs are available", async () => {
    vi.spyOn(logs, "getRecentLogs").mockResolvedValue([]);
    vi.spyOn(logs, "createLogStream").mockResolvedValue(
      new Response(new ReadableStream<Uint8Array>())
    );

    renderWithConsoleProviders(<LogsPage />);

    expect(await screen.findByText("No log entries yet.")).toBeInTheDocument();
    expect(screen.getByText("0 of 0 logs")).toBeInTheDocument();
  });

  test("downloads only visible raw lines", async () => {
    vi.spyOn(logs, "getRecentLogs").mockResolvedValue(logEntries());
    vi.spyOn(logs, "createLogStream").mockResolvedValue(
      new Response(new ReadableStream<Uint8Array>())
    );
    const { getBlob } = installURLMethods();

    renderWithConsoleProviders(<LogsPage />);

    expect(await screen.findByText(/server started/)).toBeInTheDocument();

    fireEvent.click(getMaterialFilterChip(document.body, "WARN"));
    fireEvent.click(getMaterialButton(document.body, "Download"));

    await expect(readBlobText(getBlob())).resolves.toBe("time=2026-06-07T00:00:02Z level=WARN msg=slow-request");
  });

  test("shows a non-blocking status when log streaming fails", async () => {
    vi.spyOn(logs, "getRecentLogs").mockResolvedValue(logEntries());
    vi.spyOn(logs, "createLogStream").mockRejectedValue(new Error("stream unavailable"));
    vi.spyOn(console, "error").mockImplementation(() => undefined);

    renderWithConsoleProviders(<LogsPage />);

    expect(await screen.findByText(/server started/)).toBeInTheDocument();

    await waitFor(() => {
      expect(screen.getByRole("status")).toHaveTextContent("Live stream disconnected");
    });
  });

  test("appends stream events without rewriting raw text", async () => {
    vi.spyOn(logs, "getRecentLogs").mockResolvedValue([]);
    const stream = new ReadableStream<Uint8Array>({
      start(controller) {
        controller.enqueue(
          new TextEncoder().encode(
            'data: {"timestamp":"2026-06-07T00:00:02Z","level":"INFO","message":"streamed","raw":"raw streamed line"}\n\n'
          )
        );
        controller.close();
      }
    });
    vi.spyOn(logs, "createLogStream").mockResolvedValue(new Response(stream));

    renderWithConsoleProviders(<LogsPage />);

    await waitFor(() => {
      expect(screen.getByText("streamed")).toBeInTheDocument();
    });
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

function getMaterialButton(container: ParentNode, label: string) {
  const element = Array.from(container.querySelectorAll("button.bh-button--outlined, .bh-button--outlined")).find(
    (button) => button.textContent?.includes(label)
  );
  if (!element) {
    throw new Error(`Expected an outlined button labelled "${label}".`);
  }
  return element as HTMLElement & { disabled: boolean };
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
    },
    {
      timestamp: "2026-06-07T00:00:02Z",
      level: "WARN",
      message: "slow request",
      raw: "time=2026-06-07T00:00:02Z level=WARN msg=slow-request"
    }
  ];
}

const clipboardDescriptor = Object.getOwnPropertyDescriptor(Navigator.prototype, "clipboard");
const createObjectURLDescriptor = Object.getOwnPropertyDescriptor(URL, "createObjectURL");
const revokeObjectURLDescriptor = Object.getOwnPropertyDescriptor(URL, "revokeObjectURL");

function installClipboard() {
  const writeText = vi.fn().mockResolvedValue(undefined);
  Object.defineProperty(navigator, "clipboard", {
    configurable: true,
    value: { writeText }
  });
  return writeText;
}

function restoreNavigatorClipboard() {
  if (clipboardDescriptor) {
    Object.defineProperty(Navigator.prototype, "clipboard", clipboardDescriptor);
  } else {
    Reflect.deleteProperty(navigator, "clipboard");
  }
}

function installURLMethods() {
  let blob: Blob | undefined;
  const createObjectURL = vi.fn((nextBlob: Blob) => {
    blob = nextBlob;
    return "blob:moonbridge-logs";
  });
  const revokeObjectURL = vi.fn();
  vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => undefined);
  Object.defineProperty(URL, "createObjectURL", {
    configurable: true,
    value: createObjectURL
  });
  Object.defineProperty(URL, "revokeObjectURL", {
    configurable: true,
    value: revokeObjectURL
  });
  return {
    createObjectURL,
    revokeObjectURL,
    getBlob() {
      if (!blob) {
        throw new Error("download did not create a blob URL");
      }
      return blob;
    }
  };
}

function restoreURLMethods() {
  if (createObjectURLDescriptor) {
    Object.defineProperty(URL, "createObjectURL", createObjectURLDescriptor);
  }
  if (revokeObjectURLDescriptor) {
    Object.defineProperty(URL, "revokeObjectURL", revokeObjectURLDescriptor);
  }
}

function readBlobText(blob: Blob) {
  return new Promise<string>((resolve, reject) => {
    const reader = new FileReader();
    reader.addEventListener("load", () => resolve(String(reader.result ?? "")));
    reader.addEventListener("error", () => reject(reader.error ?? new Error("failed to read blob")));
    reader.readAsText(blob);
  });
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
