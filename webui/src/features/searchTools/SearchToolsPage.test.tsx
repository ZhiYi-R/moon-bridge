import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, test, vi } from "vitest";
import { renderWithConsoleProviders } from "../../test/renderWithConsoleProviders";
import * as configGraph from "../../rpc/configGraph";
import * as management from "../../rpc/management";
import { configGraphFixture } from "../../test/configGraphFixtures";
import { SearchToolsPage } from "./SearchToolsPage";

describe("SearchToolsPage", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  test("renders web search, extensions, and proxy graph resources without YAML controls", async () => {
    vi.spyOn(configGraph, "getConfigGraph").mockResolvedValue(configGraphFixture());

    renderWithConsoleProviders(<SearchToolsPage />);

    expect(await screen.findByRole("heading", { level: 2, name: "Web Search" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { level: 2, name: "Extensions" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { level: 2, name: "Proxy" })).toBeInTheDocument();

    const webSearch = screen.getByLabelText("Web Search");
    expect(within(webSearch).getByRole("heading", { level: 3, name: "main" })).toBeInTheDocument();
    expect(within(screen.getByLabelText("Web Search main status")).getByText("Saved")).toBeInTheDocument();
    expect(within(screen.getByLabelText("Extension db_sqlite status")).getByText("Saved")).toBeInTheDocument();
    expect(within(screen.getByLabelText("Proxy main status")).getByText("Critical")).toBeInTheDocument();

    expect(getMaterialSelect(document, "Web search mode").value).toBe("auto");
    expect(screen.getByText("db_sqlite")).toBeInTheDocument();
    expect(getStructuredObject(document, "Extension config")).not.toHaveTextContent("Structured editor");
    expect(getMaterialTextField(document, "path")).toBeTruthy();
    expect(getStructuredObject(document, "OpenAI capture proxy")).not.toHaveTextContent("Structured editor");
    expect(getMaterialTextField(document, "base_url")).toBeTruthy();
    expect((getMaterialTextField(document, "api_key")).querySelector?.("input,textarea")?.spellcheck ?? (getMaterialTextField(document, "api_key")).spellcheck ?? false).toBe(false);
    expect(queryMaterialOutlinedButton(document, /OpenAI capture proxy.*2 keys/)).not.toBeInTheDocument();
    expect(document.querySelector(".schema-json-editor")).not.toBeInTheDocument();
    expect(screen.queryByLabelText(/yaml/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/yaml/i)).not.toBeInTheDocument();
  });

  test("localizes page chrome in Chinese locale", async () => {
    vi.spyOn(configGraph, "getConfigGraph").mockResolvedValue(configGraphFixture());

    renderWithConsoleProviders(<SearchToolsPage />, { locale: "zh-CN" });

    expect(await screen.findByRole("heading", { level: 2, name: "联网搜索" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { level: 2, name: "扩展" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { level: 2, name: "代理" })).toBeInTheDocument();
    expect(within(screen.getByLabelText("代理 main 状态")).getByText("关键运行时")).toBeInTheDocument();
  });

  test("creates an extension from the extensions section", async () => {
    vi.spyOn(configGraph, "getConfigGraph").mockResolvedValue(configGraphFixture());
    vi.spyOn(management, "listExtensions").mockResolvedValue(["db_sqlite", "metrics"]);
    const create = vi.spyOn(configGraph, "createConfigResource").mockResolvedValue({
      result: "committed",
      revision: "rev-2",
      graph: configGraphFixture({ revision: "rev-2" })
    });

    renderWithConsoleProviders(<SearchToolsPage />);

    await waitFor(() => expect(getMaterialButton(document, "Add Extension")).toBeInTheDocument());
    await userEvent.click(getMaterialButton(document, "Add Extension"));
    const form = screen.getByRole("form", { name: "Create Extension" });
    expect(within(form).queryByRole("textbox", { name: "Extension ID" })).not.toBeInTheDocument();
    expect(getMaterialSelect(form, "Extension ID")).toBeInTheDocument();
    setMaterialSelectValue(getMaterialSelect(form, "Extension ID"), "metrics");
    submitMaterialForm(form, "Create Extension");

    await waitFor(() => expect(create).toHaveBeenCalledWith("extension", {
      baseRevision: "rev-1",
      id: "metrics",
      value: {
        enabled: true
      }
    }));
  });

  test("lets users disable a new extension and read create field help", async () => {
    vi.spyOn(configGraph, "getConfigGraph").mockResolvedValue(configGraphFixture());
    vi.spyOn(management, "listExtensions").mockResolvedValue(["db_sqlite", "metrics"]);
    const create = vi.spyOn(configGraph, "createConfigResource").mockResolvedValue({
      result: "committed",
      revision: "rev-2",
      graph: configGraphFixture({ revision: "rev-2" })
    });

    renderWithConsoleProviders(<SearchToolsPage />);

    await waitFor(() => expect(getMaterialButton(document, "Add Extension")).toBeInTheDocument());
    await userEvent.click(getMaterialButton(document, "Add Extension"));
    const form = screen.getByRole("form", { name: "Create Extension" });
    const enabledSwitch = getMaterialSwitch(form, "Enabled");
    expect(form.querySelector(".schema-switch")).not.toBeInTheDocument();
    expect(enabledSwitch.closest(".schema-field__switch-line")).toBeInTheDocument();
    expect(enabledSwitch.closest(".schema-field")).toBeInTheDocument();
    expect(enabledSwitch.getAttribute("aria-checked") === "true" || enabledSwitch.getAttribute("aria-pressed") === "true").toBe(true);
    expect(getMaterialSelect(form, "Extension ID")).toBeInTheDocument();
    await userEvent.click(getMaterialIconButton(form, "Help for Enabled"));
    expect(within(form).getByRole("tooltip")).toHaveTextContent("Turn this extension on or off");
    setMaterialSelectValue(getMaterialSelect(form, "Extension ID"), "metrics");
    setMaterialSwitchSelected(enabledSwitch, false);
    submitMaterialForm(form, "Create Extension");

    await waitFor(() => expect(create).toHaveBeenCalledWith("extension", {
      baseRevision: "rev-1",
      id: "metrics",
      value: {
        enabled: false
      }
    }));
  });

  test("deletes extensions but not singleton search or proxy resources", async () => {
    vi.spyOn(configGraph, "getConfigGraph").mockResolvedValue(configGraphFixture());
    const remove = vi.spyOn(configGraph, "deleteConfigResource").mockResolvedValue({
      result: "committed",
      revision: "rev-2",
      graph: configGraphFixture({
        revision: "rev-2",
        resources: configGraphFixture().resources.filter((resource) => resource.id !== "db_sqlite")
      })
    });

    renderWithConsoleProviders(<SearchToolsPage />);

    expect(await screen.findByRole("heading", { level: 2, name: "Web Search" })).toBeInTheDocument();
    expect(queryMaterialButton(document, "Delete Web Search main")).not.toBeInTheDocument();
    expect(queryMaterialButton(document, "Delete Proxy main")).not.toBeInTheDocument();

    const extensionPanel = screen.getByText("db_sqlite").closest("section")!;
    await userEvent.click(getMaterialButton(extensionPanel, "Delete Extension db_sqlite"));
    await userEvent.click(getMaterialButton(extensionPanel, "Confirm delete db_sqlite"));

    expect(remove).toHaveBeenCalledWith("extension", "db_sqlite", "rev-1");
    expect(screen.queryByText("db_sqlite")).not.toBeInTheDocument();
  });
});

function getMaterialSwitch(container: ParentNode, label: string) {
  const element = Array.from(container.querySelectorAll('button[role="switch"]')).find(
    (switchElement) => switchElement.getAttribute("aria-label") === label
  );
  if (!element) {
    throw new Error(`Missing switch: ${label}`);
  }
  Object.defineProperty(element, "selected", {
    configurable: true,
    get: () => element.getAttribute("aria-checked") === "true"
  });
  return element as HTMLElement & { selected: boolean };
}

type MaterialSelectElement = HTMLElement & {
  label: string;
  value: string;
};

function queryMaterialOutlinedButton(container: ParentNode, label: RegExp) {
  return Array.from(container.querySelectorAll("button.bh-button--outlined, .bh-button--outlined")).find(
    (candidate) => label.test(candidate.getAttribute("aria-label") ?? candidate.textContent ?? "")
  ) ?? null;
}

function getStructuredObject(container: ParentNode, label: string) {
  const element = Array.from(container.querySelectorAll(".schema-structured-object")).find(
    (summary) => summary.getAttribute("aria-label")?.startsWith(`${label},`)
  );
  if (!element) {
    throw new Error(`Expected a structured object editor labelled "${label}".`);
  }
  return element as HTMLElement;
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

function getMaterialIconButton(container: ParentNode, label: string) {
  const element = Array.from(container.querySelectorAll("button.bh-icon-button")).find(
    (candidate) => candidate.getAttribute("aria-label") === label
  );
  if (!element) {
    throw new Error(`Expected an icon button labelled "${label}".`);
  }
  return element as HTMLElement;
}

function getMaterialButton(container: ParentNode, label: string) {
  const element = queryMaterialButton(container, label);
  if (!element) {
    throw new Error(`Expected a filled button labelled "${label}".`);
  }
  return element as HTMLElement;
}

function queryMaterialButton(container: ParentNode, label: string) {
  return Array.from(container.querySelectorAll("button.bh-button--filled, .bh-button--filled")).find((candidate) => {
    const accessibleLabel = candidate.getAttribute("aria-label") ?? candidate.textContent ?? "";
    return accessibleLabel.includes(label);
  }) ?? null;
}

function setMaterialSelectValue(element: any, value: string) {
  act(() => {
    element.value = value;
    element.dispatchEvent(new Event("change", { bubbles: true }));
  });
}

function setMaterialSwitchSelected(element: HTMLElement & { selected?: boolean }, selected: boolean) {
  const isOn = element.getAttribute("aria-checked") === "true";
  if (isOn === selected) {
    return;
  }
  act(() => {
    element.click();
  });
}

function submitMaterialForm(container: ParentNode, submitLabel: string) {
  const button = getMaterialButton(container, submitLabel);
  const form = button.closest("form");
  if (!form) {
    throw new Error("Expected submit button inside a form.");
  }
  fireEvent.submit(form);
}
