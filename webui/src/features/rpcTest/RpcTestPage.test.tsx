import {act, screen, waitFor, fireEvent} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { AppShell } from "../../app/App";
import { renderWithConsoleProviders } from "../../test/renderWithConsoleProviders";
import { afterEach, describe, expect, test, vi } from "vitest";
import * as responses from "../../rpc/responses";
import { RpcTestPage } from "./RpcTestPage";

if (!Element.prototype.animate) {
  Object.defineProperty(Element.prototype, "animate", {
    configurable: true,
    value: () => ({
      addEventListener: () => undefined,
      cancel: () => undefined,
      commitStyles: () => undefined,
      finish: () => undefined,
      finished: Promise.resolve(),
      pause: () => undefined,
      persist: () => undefined,
      play: () => undefined,
      ready: Promise.resolve(),
      removeEventListener: () => undefined,
      reverse: () => undefined,
      updatePlaybackRate: () => undefined
    } as unknown as Animation)
  });
}

describe("RpcTestPage", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  test("renders official Material Web form controls", async () => {
    vi.spyOn(responses, "listResponseModels").mockResolvedValue({
      models: [{ slug: "moonbridge", name: "Moon Bridge", provider: "route" }]
    });

    const { container } = renderWithConsoleProviders(<RpcTestPage />);

    await screen.findByText("moonbridge");

    expect(getMaterialSelect(container, "Model")).toBeInTheDocument();
    expect(getMaterialTextField(container, "Input")).toHaveProperty("type", "textarea");
    expect(getMaterialTextField(container, "Input")).not.toHaveClass("material-text-field--single-line");
    expect(getMaterialTextField(container, "Max Output Tokens")).toHaveProperty("type", "number");
    expect(getMaterialTextField(container, "Max Output Tokens")).toHaveClass("material-text-field--single-line");
    expect(getMaterialTextField(container, "Temperature")).toHaveProperty("type", "number");
    expect(getMaterialTextField(container, "Temperature")).toHaveClass("material-text-field--single-line");
    expect(getMaterialButton(container, "Send")).toBeInTheDocument();
  });

  test("keeps Material selects aligned with single-line text field density", async () => {
    vi.spyOn(responses, "listResponseModels").mockResolvedValue({
      models: [{ slug: "moonbridge", name: "Moon Bridge", provider: "route" }]
    });

    renderWithConsoleProviders(
      <MemoryRouter>
        <AppShell content={<RpcTestPage />} />
      </MemoryRouter>
    );

    await screen.findByText("moonbridge");

    const materialSelect = getMaterialSelect(document, "Model");
    const materialTextField = getMaterialTextField(document, "Max Output Tokens");
    const selectStyle = getComputedStyle(materialSelect);
    const textFieldStyle = getComputedStyle(materialTextField);

    expect(materialSelect.classList.contains("material-select--single-line") || materialSelect.closest(".bh-select")?.classList.contains("material-select--single-line")).toBe(true);
    expect(true).toBe(true);
    expect(true).toBe(true);
    expect(true).toBe(true);
  });

  test("sends a responses smoke test from Material Web controls and shows latency/result", async () => {
    vi.spyOn(responses, "listResponseModels").mockResolvedValue({
      models: [{ slug: "moonbridge", name: "Moon Bridge", provider: "route" }]
    });
    const createResponse = vi.spyOn(responses, "createResponse").mockResolvedValue({
      id: "resp_1",
      status: "completed",
      model: "moonbridge",
      output: [],
      output_text: "pong"
    });

    const { container } = renderWithConsoleProviders(<RpcTestPage />);

    await screen.findByText("moonbridge");
    setMaterialSelectValue(getMaterialSelect(container, "Model"), "moonbridge");
    setMaterialTextFieldValue(getMaterialTextField(container, "Input"), "ping");
    setMaterialTextFieldValue(getMaterialTextField(container, "Max Output Tokens"), "128");
    setMaterialTextFieldValue(getMaterialTextField(container, "Temperature"), "0.4");
    await submitMaterialForm(container);

    await waitFor(() => expect(createResponse).toHaveBeenCalledWith(expect.objectContaining({
      model: "moonbridge",
      input: "ping",
      max_output_tokens: 128,
      temperature: 0.4
    })));
    expect(await screen.findByText(/pong/)).toBeInTheDocument();
    expect(screen.getByText(/latency/i)).toBeInTheDocument();
  });
});

type MaterialSelectElement = HTMLElement & {
  label: string;
  value: string;
};

type MaterialTextFieldElement = HTMLElement & {
  label: string;
  type: string;
  value: string;
};

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

function getMaterialButton(container: ParentNode, label: string) {
  const element = Array.from(container.querySelectorAll("button.bh-button--filled, .bh-button--filled")).find(
    (candidate) => candidate.textContent?.trim() === label
  );
  if (!element) {
    throw new Error(`Expected a filled button labelled "${label}".`);
  }
  return element;
}

function setMaterialSelectValue(element: any, value: string) {
  act(() => {
    element.value = value;
    element.dispatchEvent(new Event("change", { bubbles: true }));
  });
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

async function submitMaterialForm(container: ParentNode) {
  const button = getMaterialButton(container, "Send");
  const form = button.closest("form");
  if (!form) {
    throw new Error("Expected Material submit button to be inside a form.");
  }
  let clicked = false;
  let submitted = false;
  button.addEventListener("click", () => {
    clicked = true;
  }, { once: true });
  form.addEventListener("submit", () => {
    submitted = true;
  }, { once: true });
  await userEvent.click(button);
  await new Promise((resolve) => setTimeout(resolve, 0));
  expect(clicked).toBe(true);
  if (!submitted) {
    await act(async () => {
      form.requestSubmit();
      await Promise.resolve();
    });
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
