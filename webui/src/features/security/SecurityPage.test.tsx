import { screen, within } from "@testing-library/react";
import { afterEach, describe, expect, test, vi } from "vitest";
import { renderWithConsoleProviders } from "../../test/renderWithConsoleProviders";
import * as configGraph from "../../rpc/configGraph";
import { configGraphFixture } from "../../test/configGraphFixtures";
import { SecurityPage } from "./SecurityPage";

describe("SecurityPage", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  test("renders server security fields with write-only auth token", async () => {
    vi.spyOn(configGraph, "getConfigGraph").mockResolvedValue(configGraphFixture());

    renderWithConsoleProviders(<SecurityPage />);

    expect(await screen.findByRole("heading", { level: 2, name: "Server" })).toBeInTheDocument();
    expect(within(screen.getByLabelText("Server")).getByRole("heading", { level: 3, name: "main" })).toBeInTheDocument();
    expect(within(screen.getByLabelText("Server main status")).getByText("Restart required")).toBeInTheDocument();
    expect(within(screen.getByLabelText("Server main status")).getByText("Critical")).toBeInTheDocument();
    expect(screen.getByLabelText("Listen address")).toHaveValue(":38440");
    expect(screen.getByLabelText("Max sessions")).toHaveValue("64");
    expect(screen.getByLabelText("Session TTL")).toHaveValue("24h");
    expect(screen.getByLabelText("Auth token")).toHaveValue("");
    expect(screen.queryByDisplayValue("******")).not.toBeInTheDocument();
    expect(screen.getByText("Restart required")).toBeInTheDocument();
  });

  test("localizes page chrome in Chinese locale", async () => {
    vi.spyOn(configGraph, "getConfigGraph").mockResolvedValue(configGraphFixture());

    renderWithConsoleProviders(<SecurityPage />, { locale: "zh-CN" });

    expect(await screen.findByRole("heading", { level: 2, name: "服务访问" })).toBeInTheDocument();
    expect(within(screen.getByLabelText("服务访问 main 状态")).getByText("需要重启")).toBeInTheDocument();
    expect(getMaterialTextField(document, "认证 Token").supportingText).toBe("输入新值以替换已保存的密钥。");
    expect(getMaterialTextField(document, "认证 Token").querySelector("input")?.getAttribute("aria-label") ?? getMaterialTextField(document, "认证 Token").getAttribute("data-label")).toBe("认证 Token");
    expect(screen.queryByLabelText("Auth Token")).not.toBeInTheDocument();
  });
});

type MaterialTextFieldElement = HTMLElement & {
  label: string;
  supportingText: string;
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
