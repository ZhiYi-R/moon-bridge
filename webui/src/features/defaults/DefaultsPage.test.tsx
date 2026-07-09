import { act, fireEvent, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, test, vi } from "vitest";
import { renderWithConsoleProviders } from "../../test/renderWithConsoleProviders";
import * as configGraph from "../../rpc/configGraph";
import { configGraphFixture } from "../../test/configGraphFixtures";
import { DefaultsPage } from "./DefaultsPage";

describe("DefaultsPage", () => {
  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  test("renders defaults, trace, and log resources", async () => {
    vi.spyOn(configGraph, "getConfigGraph").mockResolvedValue(configGraphFixture());

    renderWithConsoleProviders(<DefaultsPage />);

    expect(await screen.findByRole("heading", { level: 2, name: "Defaults" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { level: 2, name: "Trace" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { level: 2, name: "Log" })).toBeInTheDocument();
    expect(within(screen.getByLabelText("Defaults main status")).getByText("Saved")).toBeInTheDocument();
    expect(within(screen.getByLabelText("Trace main status")).getByText("Saved")).toBeInTheDocument();
    expect(within(screen.getByLabelText("Log main status")).getByText("Saved")).toBeInTheDocument();
    expect(screen.getAllByText("Hot reload").length).toBeGreaterThan(0);
    const defaultModelField = getMaterialTextField(document, "Default model");
    expect(defaultModelField.value).toBe("claude-sonnet");
    expectLobeLeadingIcon(defaultModelField);
    expect(getMaterialSelect(document, "Log level").value).toBe("info");
  });

  test("resolves default model route aliases to their underlying model icon", async () => {
    const graph = configGraphFixture();
    const defaults = graph.resources.find((resource) => resource.kind === "defaults");
    if (!defaults) {
      throw new Error("Fixture is missing defaults resource.");
    }
    defaults.value = { ...defaults.value, model: "primary" };
    vi.spyOn(configGraph, "getConfigGraph").mockResolvedValue(graph);

    renderWithConsoleProviders(<DefaultsPage />);

    const defaultModelField = await findMaterialTextField(document, "Default model");
    expect(defaultModelField.value).toBe("primary");
    expectLobeLeadingIcon(defaultModelField, "Claude");
  });

  test("autosaves defaults through graph patches", async () => {
    vi.spyOn(configGraph, "getConfigGraph").mockResolvedValue(configGraphFixture());
    const patch = vi.spyOn(configGraph, "patchConfigGraph").mockResolvedValue({
      result: "committed",
      revision: "rev-2"
    });

    renderWithConsoleProviders(<DefaultsPage />);

    const defaultsPanel = (await screen.findByRole("heading", { level: 2, name: "Defaults" }))
      .closest("section")!;
    vi.useFakeTimers();
    const modelField = getMaterialTextField(defaultsPanel, "Default model");
    setMaterialTextFieldValue(modelField, "gpt-4o");
    (() => { const __c = modelField.querySelector?.("input, textarea, select") as HTMLElement | null; fireEvent.blur(__c ?? modelField); })();

    await advanceAutosave();

    expect(patch).toHaveBeenCalledWith({
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

  test("does not expose delete actions for singleton default resources", async () => {
    vi.spyOn(configGraph, "getConfigGraph").mockResolvedValue(configGraphFixture());

    renderWithConsoleProviders(<DefaultsPage />);

    expect(await screen.findByRole("heading", { level: 2, name: "Defaults" })).toBeInTheDocument();
    expect(queryMaterialFilledButton(document, "Delete Defaults main")).not.toBeInTheDocument();
    expect(queryMaterialFilledButton(document, "Delete Trace main")).not.toBeInTheDocument();
    expect(queryMaterialFilledButton(document, "Delete Log main")).not.toBeInTheDocument();
  });

  test("localizes singleton resource titles and field labels in Chinese locale", async () => {
    vi.spyOn(configGraph, "getConfigGraph").mockResolvedValue(configGraphFixture());

    renderWithConsoleProviders(<DefaultsPage />, { locale: "zh-CN" });

    expect(await screen.findByRole("heading", { level: 2, name: "默认值" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { level: 2, name: "追踪" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { level: 2, name: "日志" })).toBeInTheDocument();
    expect(within(screen.getByLabelText("默认值 main 状态")).getByText("已保存")).toBeInTheDocument();
    expect(getMaterialTextField(document, "默认模型")).toBeInTheDocument();
    expect(getMaterialTextField(document, "全局系统提示词")).toBeInTheDocument();
    expect(getMaterialSelect(document, "日志级别")).toBeInTheDocument();
  });
});

type MaterialTextFieldElement = HTMLElement & {
  label: string;
  value: string;
};

type MaterialSelectElement = HTMLElement & {
  label: string;
  value: string;
};

async function findMaterialTextField(container: ParentNode, label: string) {
  await screen.findByRole("heading", { level: 2, name: "Defaults" });
  return getMaterialTextField(container, label);
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

function expectLobeLeadingIcon(fieldElement: HTMLElement, title?: string) {
  const leadingIcon = fieldElement.querySelector(".bh-field__leading, .material-field-leading-node, [slot='leading-icon']");
  expect(leadingIcon).toBeInTheDocument();
  expect(leadingIcon?.querySelector("svg")).toBeInTheDocument();
  if (title) {
    expect(leadingIcon?.querySelector("title")).toHaveTextContent(title);
  }
}

function getMaterialSelect(container: ParentNode, label: string) {
  const wrapper = Array.from(container.querySelectorAll<HTMLElement>(".bh-select, .bh-field.bh-select, .bh-field")).find(
    (candidate) => materialElementLabel(candidate) === label && candidate.querySelector("select")
  );
  const select = wrapper?.querySelector("select") as any;
  if (!select || !wrapper) {
    throw new Error(`Expected a select labelled "${label}".`);
  }
  Object.defineProperty(select, "label", { configurable: true, get: () => materialElementLabel(wrapper) });
  Object.defineProperty(select, "supportingText", {
    configurable: true,
    get: () => wrapper.querySelector(".bh-field__support")?.textContent?.trim() ?? ""
  });
  // Non-native alias used by a few tests that previously relied on Material option hosts.
  select.optionItems = Array.from(select.querySelectorAll("option")).map((option: any) => {
    const el = option as any;
    el.displayText = option.textContent?.trim() ?? "";
    return el;
  });
  const originalContains = select.classList.contains.bind(select.classList);
  select.classList.contains = (token: string) => originalContains(token) || wrapper.classList.contains(token);
  return select as any;
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

function queryMaterialFilledButton(container: ParentNode, label: string) {
  return Array.from(container.querySelectorAll("button.bh-button--filled, .bh-button--filled")).find((candidate) => {
    const accessibleLabel = candidate.getAttribute("aria-label") ?? candidate.textContent ?? "";
    return accessibleLabel.includes(label);
  }) ?? null;
}

async function advanceAutosave() {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(450);
    await Promise.resolve();
  });
}
