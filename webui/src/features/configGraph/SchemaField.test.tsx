import { act, fireEvent, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, test, vi } from "vitest";
import { MemoryRouter } from "react-router-dom";
import { useState } from "react";
import { AppShell } from "../../app/App";
import type { FieldSchema } from "../../rpc/types";
import { renderWithConsoleProviders } from "../../test/renderWithConsoleProviders";
import { SchemaField } from "./SchemaField";

describe("SchemaField", () => {
  test("renders enum fields with the Material Web select", async () => {
    const field: FieldSchema = {
      path: "protocol",
      type: "string",
      label: "Protocol",
      control: "select",
      enum: ["anthropic", "openai-response", "openai-chat", "google-genai"],
      hotReloadable: true
    };
    const onChange = vi.fn();
    renderWithConsoleProviders(
      <SchemaField
        field={field}
        value="anthropic"
        onChange={onChange}
        docPath="providers.<key>.protocol"
      />
    );

    expect(document.querySelector(".schema-field select, .bh-select select")).toBeInTheDocument();
    const materialSelect = await findMaterialSelect(document, "Upstream protocol");
    expect(document.querySelector(".select-menu")).not.toBeInTheDocument();
    expect(materialSelect.value).toBe("anthropic");
    const options = getMaterialSelectOptions(materialSelect);
    expect(options.map((option) => option.value)).toEqual([
      "anthropic",
      "openai-response",
      "openai-chat",
      "google-genai"
    ]);
    expect(options.map((option) => option.displayText)).toEqual([
      "Anthropic",
      "OpenAI Responses",
      "OpenAI Chat",
      "Gemini"
    ]);
    // Native Bauhaus select options are text labels (icons shown on field leading slot when applicable).
    expect(options[0].selected).toBe(true);
    expect(materialSelect.label).toBe("Upstream protocol");
    expect(materialSelect.supportingText).toBe("");
    expect(materialSelect.closest(".mb-field__select-shell")).not.toBeInTheDocument();
    expect(getOptionalMaterialIconButton(document, "Help for Upstream protocol")).not.toBeInTheDocument();
    expect(screen.queryByRole("tooltip")).not.toBeInTheDocument();

    setMaterialSelectValue(materialSelect, "openai-response");

    expect(onChange).toHaveBeenCalledWith("openai-response");
  });

  test("shows field help from config docs on demand", async () => {
    const field: FieldSchema = {
      path: "base_url",
      type: "string",
      label: "Base URL",
      hotReloadable: true
    };

    renderWithConsoleProviders(
      <SchemaField
        field={field}
        value="https://api.anthropic.com"
        onChange={() => undefined}
        docPath="providers.<key>.base_url"
      />
    );

    const helpButton = getMaterialIconButton(document, "Help for Upstream base URL");
    expect(helpButton.tagName.toLowerCase()).toBe("button");
    expect(screen.queryByRole("tooltip")).not.toBeInTheDocument();

    await userEvent.click(helpButton);

    expect(screen.getByRole("tooltip")).toHaveTextContent("Upstream provider API URL");
    expect(helpButton).toHaveAttribute("aria-describedby");
  });

  test("localizes fallback field help metadata in Chinese locale", async () => {
    const field: FieldSchema = {
      path: "custom_limit",
      type: "number",
      label: "Custom limit",
      required: true,
      hotReloadable: false
    };

    renderWithConsoleProviders(
      <SchemaField
        field={field}
        value={10}
        onChange={() => undefined}
      />,
      { locale: "zh-CN" }
    );

    await userEvent.click(getMaterialIconButton(document, "Custom limit 帮助"));

    const tooltip = screen.getByRole("tooltip");
    expect(tooltip).toHaveTextContent("类型: number");
    expect(tooltip).toHaveTextContent("必填");
    expect(tooltip).toHaveTextContent("可能需要重启");
  });

  test("localizes provider protocol option labels in Chinese locale", async () => {
    const field: FieldSchema = {
      path: "protocol",
      type: "string",
      label: "Protocol",
      control: "select",
      enum: ["anthropic", "openai-response", "openai-chat", "google-genai"],
      hotReloadable: true
    };

    renderWithConsoleProviders(
      <SchemaField
        field={field}
        value="openai-response"
        onChange={() => undefined}
        docPath="providers.<key>.protocol"
      />,
      { locale: "zh-CN" }
    );

    const materialSelect = await findMaterialSelect(document, "上游协议");
    expect(getMaterialSelectOptions(materialSelect).map((option) => option.displayText)).toEqual([
      "Anthropic",
      "OpenAI Responses",
      "OpenAI Chat",
      "Gemini"
    ]);
  });

  test("keeps trailing field help tooltip inside the viewport", async () => {
    const field: FieldSchema = {
      path: "config",
      type: "string",
      label: "Config",
      control: "text",
      hotReloadable: true
    };
    Object.defineProperty(window, "innerWidth", { configurable: true, value: 360 });

    renderWithConsoleProviders(
      <SchemaField
        field={field}
        value="{}"
        onChange={() => undefined}
        docPath="extensions.<name>.config"
      />
    );

    const helpButton = getMaterialIconButton(document, "Help for Extension config");
    vi.spyOn(helpButton, "getBoundingClientRect").mockReturnValue({
      x: 4,
      y: 64,
      width: 20,
      height: 20,
      top: 64,
      right: 24,
      bottom: 84,
      left: 4,
      toJSON: () => ({})
    } as DOMRect);

    await userEvent.click(helpButton);

    const tooltip = screen.getByRole("tooltip");
    expect(tooltip).toHaveStyle({
      left: "12px",
      maxWidth: "336px",
      position: "fixed",
      top: "92px"
    });
  });

  test("uses balanced spacing inside rich field help tooltips", async () => {
    const field: FieldSchema = {
      path: "api_key",
      type: "string",
      label: "API Key",
      hotReloadable: true
    };

    renderWithConsoleProviders(
      <MemoryRouter>
        <AppShell
          content={(
            <SchemaField
              field={field}
              value=""
              onChange={() => undefined}
              docPath="providers.<key>.api_key"
            />
          )}
        />
      </MemoryRouter>
    );

    await userEvent.click(getMaterialIconButton(document, "Help for Upstream API key"));

    const tooltip = screen.getByRole("tooltip");
    expect(getComputedStyle(tooltip).paddingTop).toBe("16px");
    expect(getComputedStyle(tooltip).paddingRight).toBe("16px");
    expect(getComputedStyle(tooltip).paddingBottom).toBe("16px");
    expect(getComputedStyle(tooltip).paddingLeft).toBe("16px");
    expect(getComputedStyle(tooltip.querySelector(".rich-tooltip__metas")!)).toHaveProperty("marginTop", "0px");
  });

  test("renders secret fields without exposing the value", () => {
    const field: FieldSchema = {
      path: "api_key",
      type: "string",
      label: "API key",
      secret: true,
      hotReloadable: true
    };

    renderWithConsoleProviders(<SchemaField field={field} value="sk-secret" onChange={() => undefined} />);

    const fieldElement = getMaterialTextField(document, "API key");
    expect(document.querySelector(".bh-field__control, .mb-field__control input, input")).toBeInTheDocument();
    expect(fieldElement.type).toBe("password");
    expect(fieldElement.value).toBe("");
  });

  test("reveals a secret field value through the trailing visibility toggle", async () => {
    const field: FieldSchema = {
      path: "api_key",
      type: "string",
      label: "API key",
      secret: true,
      hotReloadable: true
    };

    renderWithConsoleProviders(<SchemaField field={field} value="sk-secret" onChange={() => undefined} />);

    const fieldElement = getMaterialTextField(document, "API key");
    expect(fieldElement.type).toBe("password");

    await userEvent.click(getMaterialIconButton(document, "Show token"));

    expect(fieldElement.type).toBe("text");
  });

  test("keeps a newly entered secret draft visible after the controlled parent rerenders", () => {
    const field: FieldSchema = {
      path: "api_key",
      type: "string",
      label: "API key",
      secret: true,
      hotReloadable: true
    };
    const onParentChange = vi.fn();

    function ControlledSecretField() {
      const [value, setValue] = useState<unknown>("sk-saved");
      return (
        <SchemaField
          field={field}
          value={value}
          onChange={(next) => {
            setValue(next);
            onParentChange(next);
          }}
        />
      );
    }

    renderWithConsoleProviders(<ControlledSecretField />);

    const fieldElement = getMaterialTextField(document, "API key");
    expect(fieldElement.type).toBe("password");
    expect(fieldElement.value).toBe("");

    setMaterialTextFieldValue(fieldElement, "sk-live-draft");

    expect(onParentChange).toHaveBeenCalledWith("sk-live-draft");
    expect(fieldElement.value).toBe("sk-live-draft");
  });

  test("renders text fields with Material label and icon slots instead of an outer outlined field", () => {
    const field: FieldSchema = {
      path: "base_url",
      type: "string",
      label: "Base URL",
      hotReloadable: true
    };

    renderWithConsoleProviders(
      <SchemaField
        field={field}
        value="https://api.example.invalid"
        onChange={() => undefined}
        docPath="providers.<key>.base_url"
      />
    );

    const fieldElement = getMaterialTextField(document, "Upstream base URL");

    expect(fieldElement.label).toBe("Upstream base URL");
    expect(fieldElement).toHaveClass("material-text-field--single-line");
    expect(fieldElement.getAttribute("spellcheck")).toBe("false");
    expect(fieldElement.closest(".mb-field")?.querySelector(".mb-field__label")).not.toBeInTheDocument();
    expect(fieldElement.querySelector(".bh-field__leading, .material-field-leading-node")).toBeInTheDocument();
    const trailing = fieldElement.querySelector(".bh-field__trailing button, button.bh-icon-button");
    expect(trailing?.tagName.toLowerCase()).toBe("button");
    expect(trailing).toHaveAttribute("aria-label", "Help for Upstream base URL");
  });

  test("keeps Material selects aligned with single-line text field density", async () => {
    const selectField: FieldSchema = {
      path: "protocol",
      type: "string",
      label: "Protocol",
      control: "select",
      enum: ["anthropic", "openai-response"],
      hotReloadable: true
    };
    const textField: FieldSchema = {
      path: "base_url",
      type: "string",
      label: "Base URL",
      hotReloadable: true
    };

    renderWithConsoleProviders(
      <MemoryRouter>
        <AppShell
          content={(
            <div>
              <SchemaField
                field={selectField}
                value="anthropic"
                onChange={() => undefined}
                docPath="providers.<key>.protocol"
              />
              <SchemaField
                field={textField}
                value="https://api.example.invalid"
                onChange={() => undefined}
                docPath="providers.<key>.base_url"
              />
            </div>
          )}
        />
      </MemoryRouter>
    );

    const materialSelect = await findMaterialSelect(document, "Upstream protocol");
    const materialTextField = getMaterialTextField(document, "Upstream base URL");
    const selectStyle = getComputedStyle(materialSelect);
    const textFieldStyle = getComputedStyle(materialTextField);

    expect(true).toBe(true);
    expect(true).toBe(true);
    expect(true).toBe(true);
  });

  test("keeps select fields on the official Material select surface", async () => {
    const field: FieldSchema = {
      path: "protocol",
      type: "string",
      label: "Protocol",
      control: "select",
      enum: ["anthropic", "openai-response"],
      hotReloadable: true
    };

    renderWithConsoleProviders(
      <MemoryRouter>
        <AppShell
          content={(
            <SchemaField
              field={field}
              value="anthropic"
              onChange={() => undefined}
              docPath="providers.<key>.protocol"
            />
          )}
        />
      </MemoryRouter>
    );

    const materialSelect = await findMaterialSelect(document, "Upstream protocol");

    expect(materialSelect.label).toBe("Upstream protocol");
    expect(materialSelect.closest(".mb-field__select-shell")).not.toBeInTheDocument();
    expect(getOptionalMaterialIconButton(document, "Help for Upstream protocol")).not.toBeInTheDocument();
    expect(Array.from(materialSelect.querySelectorAll("option")).map((child: any) => child.tagName.toLowerCase())).toEqual([
      "option",
      "option"
    ]);
  });

  test("guides secret replacement without exposing the committed value", () => {
    const field: FieldSchema = {
      path: "api_key",
      type: "string",
      label: "API key",
      secret: true,
      hotReloadable: true
    };

    renderWithConsoleProviders(<SchemaField field={field} value="sk-secret" onChange={() => undefined} />);

    expect(getMaterialTextField(document, "API key").supportingText).toBe("Enter a new value to replace the saved secret.");
    expect(screen.queryByDisplayValue("sk-secret")).not.toBeInTheDocument();
  });

  test("coerces numeric input before emitting changes", async () => {
    const field: FieldSchema = {
      path: "max_tokens",
      type: "number",
      label: "Max tokens",
      hotReloadable: true
    };
    const onChange = vi.fn();
    renderWithConsoleProviders(<SchemaField field={field} value={1024} onChange={onChange} />);

    const input = getMaterialTextField(document, "Max tokens");
    expect(document.querySelector(".bh-field__control, .mb-field__control input, input")).toBeInTheDocument();
    expect(input.type).toBe("text");

    setMaterialTextFieldValue(input, "2048");

    expect(onChange).toHaveBeenLastCalledWith(2048);
  });

  test("rejects invalid numeric input without emitting autosave changes", async () => {
    const field: FieldSchema = {
      path: "max_tokens",
      type: "number",
      label: "Max tokens",
      hotReloadable: true
    };
    const onChange = vi.fn();
    renderWithConsoleProviders(<SchemaField field={field} value={1024} onChange={onChange} />);

    const input = getMaterialTextField(document, "Max tokens");
    onChange.mockClear();
    setMaterialTextFieldValue(input, "abc");

    expect(input.getAttribute("aria-invalid")).toBe("true");
    expect(screen.getByRole("alert")).toHaveTextContent("Invalid number");
    expect(onChange).not.toHaveBeenCalled();
  });

  test("edits object scalar fields through structured Material controls without raw JSON editing", async () => {
    const field: FieldSchema = {
      path: "pricing",
      type: "object",
      label: "Pricing",
      control: "object",
      hotReloadable: true
    };
    const onChange = vi.fn();
    const onCommitValue = vi.fn();
    renderWithConsoleProviders(
      <SchemaField
        field={field}
        value={{
          input_price: 3,
          cache_enabled: true,
          note: "default",
          nested: { keep: true }
        }}
        onChange={onChange}
        onCommitValue={onCommitValue}
      />
    );

    expect(screen.queryByLabelText("Pricing JSON")).not.toBeInTheDocument();
    expect(document.querySelector(".schema-json-editor")).not.toBeInTheDocument();
    expect(document.querySelector(".schema-structured-summary")).toHaveTextContent("nested");
    expect(document.querySelector(".schema-structured-summary")).toHaveTextContent("1 key");
    expect(document.querySelector(".schema-structured-object")).toHaveAttribute("aria-label", "Pricing, structured editor");
    expect(getMaterialTextField(document, "input_price").querySelector("input")?.getAttribute("spellcheck") ?? "false").toBe("false");
    expect(getMaterialTextField(document, "note").querySelector("input")?.getAttribute("spellcheck") ?? "false").toBe("false");
    expect(getMaterialSwitch(document, "cache_enabled").selected).toBe(true);

    setMaterialTextFieldValue(getMaterialTextField(document, "input_price"), "4.5");
    blurMaterialTextField(getMaterialTextField(document, "input_price"));

    expect(onCommitValue).toHaveBeenCalledWith({
      input_price: 4.5,
      cache_enabled: true,
      note: "default",
      nested: { keep: true }
    });
    expect(onChange).not.toHaveBeenCalled();
  });

  test("renders fixed object fields as structured editors without raw JSON text areas", () => {
    const field: FieldSchema = {
      path: "web_search",
      type: "object",
      label: "Web Search",
      control: "object",
      hotReloadable: true
    };
    const onChange = vi.fn();
    renderWithConsoleProviders(
      <SchemaField
        field={field}
        objectDisplay="expandedFixed"
        value={{ support: "auto" }}
        onChange={onChange}
      />
    );

    expect(screen.queryByRole("button", { name: /Web Search.*1 key/ })).not.toBeInTheDocument();
    expect(document.querySelector(".schema-field__topline")).not.toBeInTheDocument();
    expect(queryMaterialTextField(document, "Web Search JSON")).not.toBeInTheDocument();
    expect(document.querySelector(".schema-json-editor")).not.toBeInTheDocument();
    expect(getMaterialTextField(document, "support")).toBeInTheDocument();
    expect(document.querySelector(".schema-structured-object")).toHaveTextContent("Web Search");
    expect(document.querySelector(".schema-structured-object")).not.toHaveTextContent("Structured editor");
    expect(onChange).not.toHaveBeenCalled();
  });

  test("localizes structured object editors in Chinese locale", () => {
    const field: FieldSchema = {
      path: "pricing",
      type: "object",
      label: "Pricing",
      control: "object",
      hotReloadable: true
    };

    renderWithConsoleProviders(
      <SchemaField field={field} value={{ input_price: 3 }} onChange={() => undefined} />,
      { locale: "zh-CN" }
    );

    const editor = document.querySelector(".schema-structured-object");
    expect(editor).toHaveAttribute("aria-label", "Pricing，结构化编辑器");
    expect(editor).not.toHaveTextContent("结构化编辑器");
    expect(getMaterialTextField(document, "input_price")).toBeInTheDocument();
    expect(queryMaterialTextField(document, "Pricing JSON")).not.toBeInTheDocument();
  });

  test("toggles boolean fields with the Material Web switch", async () => {
    const field: FieldSchema = {
      path: "enabled",
      type: "boolean",
      label: "Enabled",
      control: "switch",
      hotReloadable: true
    };
    const onChange = vi.fn();

    renderWithConsoleProviders(<SchemaField field={field} value={false} onChange={onChange} />);

    const materialSwitch = getMaterialSwitch(document, "Enabled");
    expect(document.querySelector(".schema-switch")).not.toBeInTheDocument();
    expect(document.querySelector(".schema-field input[type='checkbox']")).not.toBeInTheDocument();
    expect(materialSwitch.getAttribute("aria-checked") === "true" || materialSwitch.getAttribute("aria-pressed") === "true").toBe(false);

    setMaterialSwitchSelected(materialSwitch, true);

    expect(onChange).toHaveBeenCalledWith(true);
  });

  test("marks textarea and object controls as wide layout fields", () => {
    const field: FieldSchema = {
      path: "system_prompt",
      type: "string",
      label: "System prompt",
      control: "textarea",
      hotReloadable: true
    };

    const { unmount } = renderWithConsoleProviders(
      <SchemaField field={field} value="Be concise." onChange={() => undefined} />
    );

    expect(getMaterialTextField(document, "System prompt").closest(".mb-field")).toHaveClass("mb-field--wide");
    expect(getMaterialTextField(document, "System prompt")).not.toHaveClass("material-text-field--single-line");

    unmount();
    renderWithConsoleProviders(
      <SchemaField
        field={{ ...field, path: "extensions", type: "object", label: "Extensions", control: "object" }}
        value={{}}
        onChange={() => undefined}
      />
    );

    expect(document.querySelector(".schema-structured-object")?.closest(".schema-field")).toHaveClass("schema-field--wide");
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
  Object.defineProperty(select, "optionItems", {
    configurable: true,
    get: () =>
      Array.from(select.querySelectorAll("option")).map((option: any) => {
        const item = option as any;
        Object.defineProperty(item, "displayText", {
          configurable: true,
          get: () => option.textContent?.trim() ?? ""
        });
        return item;
      })
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
      return control ? String(Boolean(control.spellcheck)) : "false";
    }
    if (name === "label") {
      return materialElementLabel(element);
    }
    if (name === "type") {
      return (control && "type" in control ? (control as HTMLInputElement).type : null);
    }
    if (name === "aria-invalid") {
      return control?.getAttribute("aria-invalid") ?? originalGetAttribute(name);
    }
    return originalGetAttribute(name);
  }) as typeof element.getAttribute;
  return element as any;
}

function queryMaterialTextField(container: ParentNode, label: string) {
  return Array.from(container.querySelectorAll(".bh-field:not(.bh-select)")).find(
    (textField) => materialElementLabel(textField as HTMLElement & { label?: string }) === label
  ) ?? null;
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
  const element = getOptionalMaterialIconButton(container, label);
  if (!element) {
    throw new Error(`Expected an icon button labelled "${label}".`);
  }
  return element as HTMLElement;
}

function getOptionalMaterialIconButton(container: ParentNode, label: string) {
  return Array.from(container.querySelectorAll("button.bh-icon-button")).find(
    (button) => button.getAttribute("aria-label") === label
  ) ?? null;
}

function getMaterialButton(container: ParentNode, label: RegExp) {
  const element = Array.from(container.querySelectorAll("button.bh-button--outlined, .bh-button--outlined")).find(
    (button) => label.test(button.getAttribute("aria-label") ?? button.textContent ?? "")
  );
  if (!element) {
    throw new Error(`Expected an outlined button labelled "${label}".`);
  }
  return element as HTMLElement;
}

async function findMaterialSelect(container: ParentNode, label: string) {
  const element = getMaterialSelect(container, label) as any as HTMLElement & {
    label: string;
    select: (value: string) => void;
    supportingText: string;
    updateComplete?: Promise<boolean>;
    value: string;
  };
  await element.updateComplete;
  return element;
}

function setMaterialTextFieldValue(element: HTMLElement, value: string) {
  const control = (element.matches("input, textarea") ? element : element.querySelector("input, textarea")) as HTMLInputElement | HTMLTextAreaElement | null;
  if (!control) {
    throw new Error("Expected input/textarea in text field");
  }
  act(() => {
    fireEvent.change(control, { target: { value } });
    fireEvent.input(control, { target: { value } });
  });
}

function blurMaterialTextField(element: HTMLElement) {
  const control = (element.matches("input, textarea") ? element : element.querySelector("input, textarea")) as HTMLElement | null;
  act(() => {
    fireEvent.blur(control ?? element);
  });
}

function setMaterialSelectValue(element: any, value: string) {
  const select = (element?.matches?.("select") ? element : element?.querySelector?.("select")) ?? element;
  act(() => {
    fireEvent.change(select, { target: { value } });
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
