import {act, screen, waitFor, fireEvent} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithConsoleProviders } from "../test/renderWithConsoleProviders";
import { afterEach, describe, expect, test, vi } from "vitest";
import { ApiError } from "../rpc/http";
import { expectPanelElementToBeFlat } from "../test/panelStyleAssertions";
import { AuthGate } from "./AuthGate";

describe("AuthGate", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  test("renders children when there is no auth error", () => {
    renderWithConsoleProviders(<AuthGate>Console content</AuthGate>);

    expect(screen.getByText("Console content")).toBeInTheDocument();
  });

  test("calls onSubmit with the token and remember flag", async () => {
    const onSubmit = vi.fn();

    renderWithConsoleProviders(
      <AuthGate error={new ApiError(401, "invalid_auth", "missing token")} onSubmit={onSubmit}>
        Console content
      </AuthGate>
    );

    const tokenField = getMaterialTextField(document, "Token");
    const submitButton = getMaterialButton(document, "Save token");
    expect(tokenField.type).toBe("password");
    expect(submitButton.type).toBe("submit");

    setMaterialTextFieldValue(tokenField, "secret-token");
    await submitAuthForm(submitButton);

    await waitFor(() => expect(onSubmit).toHaveBeenCalledWith("secret-token", false));
  });

  test("toggles token visibility through the trailing icon button", async () => {
    renderWithConsoleProviders(
      <AuthGate error={new ApiError(401, "invalid_auth", "missing token")}>
        Console content
      </AuthGate>
    );

    const tokenField = getMaterialTextField(document, "Token");
    // Hidden by default; the trailing toggle reveals it.
    expect(tokenField.type).toBe("password");
    const showButton = getMaterialIconButton(document, "Show token");

    await userEvent.click(showButton);

    expect(tokenField.type).toBe("text");
    expect(getMaterialIconButton(document, "Hide token")).toBeInTheDocument();
  });

  test("forwards remember=true when the checkbox is checked", async () => {
    const onSubmit = vi.fn();

    renderWithConsoleProviders(
      <AuthGate error={new ApiError(401, "invalid_auth", "missing token")} onSubmit={onSubmit}>
        Console content
      </AuthGate>
    );

    const tokenField = getMaterialTextField(document, "Token");
    const rememberCheckbox = getMaterialCheckbox(document, "Remember on this device");

    setMaterialTextFieldValue(tokenField, "remembered-token");
    setMaterialCheckboxChecked(rememberCheckbox, true);
    await submitAuthForm(getMaterialButton(document, "Save token"));

    await waitFor(() => expect(onSubmit).toHaveBeenCalledWith("remembered-token", true));
  });

  test("disables and relabels the submit button while pending", () => {
    renderWithConsoleProviders(
      <AuthGate error={new ApiError(401, "invalid_auth", "missing token")} pending>
        Console content
      </AuthGate>
    );

    const submitButton = getMaterialButton(document, "Verifying…");
    expect(submitButton).toBeInTheDocument();
    expect((submitButton as unknown as { disabled: boolean }).disabled).toBe(true);
  });

  test("localizes authentication controls in Chinese locale", () => {
    renderWithConsoleProviders(
      <AuthGate error={new ApiError(401, "invalid_auth", "missing token")}>
        Console content
      </AuthGate>,
      { locale: "zh-CN" }
    );

    expect(getMaterialTextField(document, "Token")).toBeInTheDocument();
    expect(getMaterialCheckbox(document, "在此设备记住")).toBeInTheDocument();
    expect(getMaterialButton(document, "保存 Token")).toBeInTheDocument();
  });

  test("renders the auth background panel without borders or glow", () => {
    renderWithConsoleProviders(
      <AuthGate error={new ApiError(401, "invalid_auth", "missing token")}>
        Console content
      </AuthGate>
    );

    const authCard = document.querySelector(".auth-card");
    expect(authCard).toBeInTheDocument();
    expectPanelElementToBeFlat(authCard!);
  });
});

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
    (button) => button.getAttribute("aria-label") === label
  );
  if (!element) {
    throw new Error(`Expected an icon button labelled "${label}".`);
  }
  return element as HTMLElement;
}

function getMaterialCheckbox(container: ParentNode, label: string) {
  const element = Array.from(container.querySelectorAll('input[type="checkbox"]')).find(
    (checkbox) => checkbox.getAttribute("aria-label") === label
  );
  if (!element) {
    throw new Error(`Expected a Material Web checkbox labelled "${label}".`);
  }
  return element as HTMLElement & { checked: boolean };
}

function getMaterialButton(container: ParentNode, label: string) {
  const element = Array.from(container.querySelectorAll("button.bh-button--filled, .bh-button--filled")).find(
    (button) => button.textContent?.trim() === label
  );
  if (!element) {
    throw new Error(`Expected a filled button labelled "${label}".`);
  }
  return element as HTMLElement & { type: string };
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

function setMaterialCheckboxChecked(element: HTMLInputElement | HTMLElement, checked: boolean) {
  const input = (
    element instanceof HTMLInputElement
      ? element
      : element.querySelector("input[type='checkbox']")
  ) as HTMLInputElement;
  if (!input) {
    throw new Error("Expected checkbox input");
  }
  if (input.checked === checked) {
    return;
  }
  // React checkbox onChange is driven by click, not property assignment.
  fireEvent.click(input);
}

async function submitAuthForm(button: HTMLElement) {
  const form = button.closest("form");
  if (!form) {
    throw new Error("Expected submit button inside AuthGate form.");
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
