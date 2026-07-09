/** Query helpers for Bauhaus controls (replacing Material Web host queries). */

export type BauhausTextFieldElement = HTMLElement & {
  value?: string;
  label?: string;
};

export type BauhausSelectElement = HTMLSelectElement & {
  label?: string;
};

export function fieldLabel(element: Element): string {
  const data = element.getAttribute("data-label");
  if (data) {
    return data;
  }
  const label = element.querySelector("label");
  return label?.textContent?.replace(/\s*\*$/, "").trim() ?? "";
}

export function getTextField(container: ParentNode, label: string): HTMLElement {
  const element = Array.from(container.querySelectorAll<HTMLElement>(".bh-field")).find(
    (candidate) => fieldLabel(candidate) === label && !candidate.classList.contains("bh-select")
  );
  if (!element) {
    throw new Error(`Expected a text field labelled "${label}".`);
  }
  return element;
}

export function getTextFieldControl(container: ParentNode, label: string): HTMLInputElement | HTMLTextAreaElement {
  const field = getTextField(container, label);
  const control = field.querySelector("input, textarea") as HTMLInputElement | HTMLTextAreaElement | null;
  if (!control) {
    throw new Error(`Expected control inside text field labelled "${label}".`);
  }
  return control;
}

export function setTextFieldValue(field: HTMLElement, value: string) {
  const control = field.querySelector("input, textarea") as HTMLInputElement | HTMLTextAreaElement | null;
  if (!control) {
    throw new Error("Expected input/textarea inside text field.");
  }
  const prototype = Object.getPrototypeOf(control);
  const descriptor = Object.getOwnPropertyDescriptor(prototype, "value");
  descriptor?.set?.call(control, value);
  control.dispatchEvent(new Event("input", { bubbles: true }));
  control.dispatchEvent(new Event("change", { bubbles: true }));
}

export function getSelect(container: ParentNode, label: string): HTMLSelectElement {
  const wrapper = Array.from(container.querySelectorAll<HTMLElement>(".bh-select, .bh-field.bh-select")).find(
    (candidate) => fieldLabel(candidate) === label
  );
  // also match .bh-field with select child
  const field =
    wrapper ??
    Array.from(container.querySelectorAll<HTMLElement>(".bh-field")).find((candidate) => {
      return fieldLabel(candidate) === label && candidate.querySelector("select");
    });
  const select = field?.querySelector("select");
  if (!select) {
    throw new Error(`Expected a select labelled "${label}".`);
  }
  return select;
}

export function getSelectOptions(select: HTMLSelectElement) {
  return Array.from(select.querySelectorAll("option"));
}

export function getFilledButton(container: ParentNode, label: string): HTMLButtonElement {
  const element = Array.from(container.querySelectorAll<HTMLButtonElement>("button.bh-button--filled, .bh-button--filled")).find(
    (candidate) => (candidate.textContent ?? "").replace(/\s+/g, " ").trim().includes(label)
  );
  if (!element) {
    throw new Error(`Expected a filled button labelled "${label}".`);
  }
  return element;
}

export function getOutlinedButton(container: ParentNode, label: string | RegExp): HTMLButtonElement {
  const element = Array.from(container.querySelectorAll<HTMLButtonElement>("button.bh-button--outlined, .bh-button--outlined")).find(
    (candidate) => {
      const text = (candidate.textContent ?? "").replace(/\s+/g, " ").trim();
      return typeof label === "string" ? text.includes(label) : label.test(text);
    }
  );
  if (!element) {
    throw new Error(`Expected an outlined button labelled "${label}".`);
  }
  return element;
}

export function queryOutlinedButton(container: ParentNode, label: string | RegExp) {
  return Array.from(container.querySelectorAll<HTMLButtonElement>("button.bh-button--outlined, .bh-button--outlined")).find(
    (candidate) => {
      const text = (candidate.textContent ?? "").replace(/\s+/g, " ").trim();
      return typeof label === "string" ? text.includes(label) : label.test(text);
    }
  );
}

export function getIconButton(container: ParentNode, label: string): HTMLButtonElement {
  const element = Array.from(container.querySelectorAll<HTMLButtonElement>("button.bh-icon-button")).find(
    (candidate) => candidate.getAttribute("aria-label") === label
  );
  if (!element) {
    throw new Error(`Expected an icon button labelled "${label}".`);
  }
  return element;
}

export function queryIconButton(container: ParentNode, label: string) {
  return Array.from(container.querySelectorAll<HTMLButtonElement>("button.bh-icon-button")).find(
    (candidate) => candidate.getAttribute("aria-label") === label
  );
}

export function getSwitch(container: ParentNode, label: string): HTMLButtonElement {
  const element = Array.from(container.querySelectorAll<HTMLButtonElement>('button[role="switch"]')).find(
    (candidate) => candidate.getAttribute("aria-label") === label
  );
  if (!element) {
    throw new Error(`Expected a switch labelled "${label}".`);
  }
  return element;
}

export function getCheckbox(container: ParentNode, label: string): HTMLInputElement {
  const element = Array.from(container.querySelectorAll<HTMLInputElement>('input[type="checkbox"]')).find(
    (candidate) => candidate.getAttribute("aria-label") === label
  );
  if (!element) {
    throw new Error(`Expected a checkbox labelled "${label}".`);
  }
  return element;
}

export function getFilterChip(container: ParentNode, label: string): HTMLButtonElement {
  const element = Array.from(container.querySelectorAll<HTMLButtonElement>("button.bh-chip")).find(
    (candidate) => (candidate.textContent ?? "").replace(/\s+/g, " ").trim() === label
  );
  if (!element) {
    throw new Error(`Expected a filter chip labelled "${label}".`);
  }
  return element;
}

export function submitForm(container: ParentNode, submitLabel: string) {
  const button = getFilledButton(container, submitLabel);
  const form = button.closest("form");
  if (!form) {
    throw new Error("Expected submit button inside a form.");
  }
  form.requestSubmit(button);
}
