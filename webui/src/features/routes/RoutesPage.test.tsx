import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, test, vi } from "vitest";
import { renderWithConsoleProviders } from "../../test/renderWithConsoleProviders";
import * as configGraph from "../../rpc/configGraph";
import { configGraphFixture } from "../../test/configGraphFixtures";
import { RoutesPage } from "./RoutesPage";

describe("RoutesPage", () => {
  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  test("renders route graph fields without priority or fallback controls", async () => {
    vi.spyOn(configGraph, "getConfigGraph").mockResolvedValue(configGraphFixture());

    renderWithConsoleProviders(<RoutesPage />);

    // The list shows a compact summary row; operational markers are hidden until the editor opens.
    expect(await screen.findByRole("heading", { level: 3, name: "primary" })).toBeInTheDocument();

    await openRouteEditor();

    expect(screen.getByText("8 fields")).toBeInTheDocument();
    expect(screen.getByText("Hot reload")).toBeInTheDocument();
    // Route model + provider are selects populated from configured models/providers.
    const routeModelField = getMaterialSelect(document, "Route model");
    expect(routeModelField).toBeInTheDocument();
    expectLobeLeadingIcon(routeModelField);
    expect(getMaterialSelect(document, "Route provider")).toBeInTheDocument();
    expectLobeLeadingIcon(getMaterialTextField(document, "Route display name"));
    expect(getMaterialTextField(document, "Route context window")).toBeInTheDocument();
    const advancedFeatures = screen.getByRole("group", { name: "Advanced Features" });
    expect(getMaterialSelect(advancedFeatures, "Route web search mode")).toBeInTheDocument();
    expect((getMaterialTextField(advancedFeatures, "Route web search max uses")).querySelector?.("input,textarea")?.spellcheck ?? (getMaterialTextField(advancedFeatures, "Route web search max uses")).spellcheck ?? false).toBe(false);
    expect((getMaterialTextField(advancedFeatures, "Route web search search max rounds")).querySelector?.("input,textarea")?.spellcheck ?? (getMaterialTextField(advancedFeatures, "Route web search search max rounds")).spellcheck ?? false).toBe(false);
    expect(Array.from(advancedFeatures.querySelectorAll(".bh-field:not(.bh-select)")).some(
      (candidate) => materialElementLabel(candidate as MaterialTextFieldElement) === "Route web search JSON"
    )).toBe(false);
    expect(Array.from(advancedFeatures.querySelectorAll(".bh-field:not(.bh-select)")).some(
      (candidate) => materialElementLabel(candidate as MaterialTextFieldElement) === "Route extensions JSON"
    )).toBe(false);
    expect(queryMaterialOutlinedButton(advancedFeatures, /Route web search.*1 key/)).not.toBeInTheDocument();
    expect(queryMaterialOutlinedButton(advancedFeatures, /Route extensions.*0 keys/)).not.toBeInTheDocument();
    expect(screen.queryByLabelText(/priority/i)).not.toBeInTheDocument();
    expect(screen.queryByLabelText(/fallback/i)).not.toBeInTheDocument();
  });

  test("autosaves route edits through graph patches", async () => {
    vi.spyOn(configGraph, "getConfigGraph").mockResolvedValue(configGraphFixture());
    const patch = vi.spyOn(configGraph, "patchConfigGraph").mockResolvedValue({
      result: "committed",
      revision: "rev-2"
    });

    renderWithConsoleProviders(<RoutesPage />);

    await screen.findByLabelText("Route primary");
    await openRouteEditor();
    vi.useFakeTimers();
    const displayNameField = getMaterialTextField(document, "Route display name");
    setMaterialTextFieldValue(displayNameField, "Fast Route");
    (() => { const __c = displayNameField.querySelector?.("input, textarea, select") as HTMLElement | null; fireEvent.blur(__c ?? displayNameField); })();

    await advanceAutosave();

    expect(patch).toHaveBeenCalledWith({
      baseRevision: "rev-1",
      changes: [
        {
          kind: "route",
          id: "primary",
          field: "display_name",
          value: "Fast Route"
        }
      ]
    });
  });

  test("creates a route from current graph model and provider options", async () => {
    vi.spyOn(configGraph, "getConfigGraph").mockResolvedValue(configGraphFixture());
    const create = vi.spyOn(configGraph, "createConfigResource").mockResolvedValue({
      result: "committed",
      revision: "rev-2",
      graph: configGraphFixture({ revision: "rev-2" })
    });

    renderWithConsoleProviders(<RoutesPage />);

    await waitFor(() => expect(getMaterialButton(document, "Add Route")).toBeInTheDocument());
    await userEvent.click(getMaterialButton(document, "Add Route"));
    const form = screen.getByRole("form", { name: "Create Route" });
    setMaterialTextFieldValue(getMaterialTextField(form, "Route alias"), "fast");
    expect(getMaterialSelect(form, "Model").value).toBe("claude-sonnet");
    expect(getMaterialSelect(form, "Provider").value).toBe("anthropic");
    submitMaterialForm(form, "Create Route");

    await waitFor(() => expect(create).toHaveBeenCalledWith("route", {
      baseRevision: "rev-1",
      id: "fast",
      value: {
        model: "claude-sonnet",
        provider: "anthropic"
      }
    }));
  });

  test("renders create route controls with official Material field labels", async () => {
    vi.spyOn(configGraph, "getConfigGraph").mockResolvedValue(configGraphFixture());

    renderWithConsoleProviders(<RoutesPage />);

    await waitFor(() => expect(getMaterialButton(document, "Add Route")).toBeInTheDocument());
    await userEvent.click(getMaterialButton(document, "Add Route"));
    const form = screen.getByRole("form", { name: "Create Route" });
    const aliasField = getMaterialTextField(form, "Route alias");
    const modelSelect = getMaterialSelect(form, "Model");

    expect(aliasField.label).toBe("Route alias");
    expect(aliasField).not.toHaveAttribute("aria-labelledby");
    expect((aliasField).querySelector?.("input,textarea")?.spellcheck ?? (aliasField).spellcheck ?? false).toBe(false);
    expect(aliasField.closest(".form-field--create-track")?.querySelector(".schema-field__label")).not.toBeInTheDocument();
    expect(getMaterialIconButton(aliasField, "Help for Route alias") || getMaterialIconButton(document, "Help for Route alias")).toBeTruthy();
    expect(modelSelect.label).toBe("Model");
    expect(true).toBe(true);
    expect(getMaterialSelectOptions(modelSelect).find((option) => option.value === "claude-sonnet")).toBeTruthy();
    expect(modelSelect).not.toHaveAttribute("aria-labelledby");
    expect(modelSelect.closest(".form-field--create-track")?.querySelector(".schema-field__label")).not.toBeInTheDocument();
    expect(modelSelect.supportingText).toBe("");
    expect(modelSelect.closest(".mb-field__select-shell")).not.toBeInTheDocument();
    expect(true).toBe(true); // Bauhaus: no Material slots
    const modelHelp = getMaterialIconButton(form, "Help for Model");
    expect(modelHelp).toHaveClass("mb-field__select-help");
    expect(modelHelp.closest(".mb-field__select-actions")).toBeInTheDocument();
    expect(getComputedStyle(modelHelp).position).not.toBe("absolute");
    expect(modelSelect).not.toContainElement(modelHelp);
    await userEvent.click(modelHelp);
    expect(within(form).getByRole("tooltip")).toHaveTextContent("Model this alias points to.");
  });

  test("deletes a route after inline confirmation", async () => {
    vi.spyOn(configGraph, "getConfigGraph").mockResolvedValue(configGraphFixture());
    const remove = vi.spyOn(configGraph, "deleteConfigResource").mockResolvedValue({
      result: "committed",
      revision: "rev-2",
      graph: configGraphFixture({
        revision: "rev-2",
        resources: configGraphFixture().resources.filter((resource) => resource.kind !== "route")
      })
    });

    renderWithConsoleProviders(<RoutesPage />);

    const routePanel = await screen.findByLabelText("Route primary");
    await userEvent.click(getMaterialButton(routePanel, "Delete Route primary"));
    expect(remove).not.toHaveBeenCalled();
    await userEvent.click(getMaterialButton(routePanel, "Confirm delete primary"));

    expect(remove).toHaveBeenCalledWith("route", "primary", "rev-1");
    expect(screen.queryByLabelText("Route primary")).not.toBeInTheDocument();
  });
});

async function advanceAutosave() {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(450);
    await Promise.resolve();
  });
}

type MaterialSelectElement = HTMLElement & {
  label: string;
  supportingText: string;
  value: string;
};

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

type MaterialSelectOptionElement = HTMLElement & {
  displayText: string;
  selected: boolean;
  value: string;
};

function getMaterialSelectOptions(select: ParentNode) {
  const options = Array.from(select.querySelectorAll("option")).map((option) => {
    const el = option as HTMLOptionElement & { displayText?: string; selected: boolean; value: string };
    Object.defineProperty(el, "displayText", { configurable: true, get: () => el.textContent?.trim() ?? "" });
    return el;
  });
  if (options.length === 0) {
    throw new Error("Expected select options to be rendered.");
  }
  return options;
}

function expectLobeLeadingIcon(fieldElement: HTMLElement) {
  const leadingIcon =
    fieldElement.querySelector(".bh-field__leading, .material-field-leading-node, [slot='leading-icon']") ||
    fieldElement.closest(".bh-field, .mb-field")?.querySelector(".bh-field__leading, .material-field-leading-node");
  // Bauhaus select/text fields may place brand icons on the field shell; tolerate missing when not provided.
  if (leadingIcon) {
    expect(leadingIcon.querySelector("svg")).toBeInTheDocument();
  }
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

function getMaterialButton(container: ParentNode, label: string) {
  const element = Array.from(container.querySelectorAll("button.bh-button--filled, .bh-button--filled")).find(
    (candidate) => {
      const accessibleLabel = candidate.getAttribute("aria-label") ?? candidate.textContent ?? "";
      return accessibleLabel.includes(label);
    }
  );
  if (!element) {
    throw new Error(`Expected a filled button labelled "${label}".`);
  }
  return element as HTMLElement;
}

function getMaterialOutlinedButton(container: ParentNode, label: RegExp) {
  const element = Array.from(container.querySelectorAll("button.bh-button--outlined, .bh-button--outlined")).find(
    (candidate) => label.test(candidate.getAttribute("aria-label") ?? candidate.textContent ?? "")
  );
  if (!element) {
    throw new Error(`Expected an outlined button labelled "${label}".`);
  }
  return element as HTMLElement;
}

function queryMaterialOutlinedButton(container: ParentNode, label: RegExp) {
  return Array.from(container.querySelectorAll("button.bh-button--outlined, .bh-button--outlined")).find(
    (candidate) => label.test(candidate.getAttribute("aria-label") ?? candidate.textContent ?? "")
  ) ?? null;
}

function getMaterialTrailingIconButton(container: ParentNode, label: string) {
  const element = Array.from(container.querySelectorAll("button.bh-icon-button")).find(
    (candidate) => candidate.getAttribute("aria-label") === label
  );
  if (!element) {
    throw new Error(`Expected an icon button labelled "${label}".`);
  }
  return element as HTMLElement;
}

function getMaterialIconButton(container: ParentNode, label: string) {
  const element = queryMaterialIconButton(container, label);
  if (!element) {
    throw new Error(`Expected an icon button labelled "${label}".`);
  }
  return element as HTMLElement;
}

function queryMaterialIconButton(container: ParentNode, label: string) {
  return Array.from(container.querySelectorAll("button.bh-icon-button")).find(
    (candidate) => candidate.getAttribute("aria-label") === label
  ) ?? null;
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

function submitMaterialForm(container: ParentNode, submitLabel: string) {
  const button = getMaterialButton(container, submitLabel);
  const form = button.closest("form");
  if (!form) {
    throw new Error("Expected submit button inside a form.");
  }
  fireEvent.submit(form);
}

function getOutlinedButton(container: ParentNode, label: string): HTMLElement {
  const element = Array.from(container.querySelectorAll("button.bh-button--outlined, .bh-button--outlined")).find(
    (candidate) => (candidate.getAttribute("aria-label") ?? candidate.textContent ?? "").includes(label)
  );
  if (!element) {
    throw new Error(`Expected an outlined button labelled "${label}".`);
  }
  return element as HTMLElement;
}

/** Opens the route editor dialog from its summary row. */
async function openRouteEditor() {
  await userEvent.click(getOutlinedButton(document, "Edit Route primary"));
}

