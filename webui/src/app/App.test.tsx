import { fireEvent, screen } from "@testing-library/react";
import { afterEach, describe, expect, test, vi } from "vitest";
import { MemoryRouter } from "react-router-dom";
import { renderWithConsoleProviders } from "../test/renderWithConsoleProviders";
import { expectPanelElementToBeFlat, expectPanelRuleToAvoidEdges } from "../test/panelStyleAssertions";
import { AppShell } from "./App";

describe("AppShell", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  test("shows the config graph navigation surface without staged apply", () => {
    renderWithConsoleProviders(
      <MemoryRouter>
        <AppShell />
      </MemoryRouter>
    );

    const labels = Array.from(
      document.querySelectorAll<HTMLAnchorElement>(".navigation-rail a")
    ).map((link) => link.querySelector(".nav-item__label")?.textContent);

    expect(labels).toEqual([
      "Overview",
      "Models & Providers",
      "Routes",
      "Defaults",
      "Search & Tools",
      "Storage",
      "Security"
    ]);
    expect(document.querySelector(".navigation-rail")?.textContent).not.toContain("Config");
    expect(document.querySelector(".navigation-rail")?.textContent).not.toContain("RPC Test");
    expect(document.querySelector(".navigation-rail")?.textContent).not.toContain("Extensions");
    expect(screen.queryByRole("button", { name: /^apply$/i })).not.toBeInTheDocument();
    expect(screen.queryByRole("dialog", { name: /apply changes/i })).not.toBeInTheDocument();
  });

  test("keeps shell actions limited to locale and theme controls", () => {
    renderWithConsoleProviders(
      <MemoryRouter>
        <AppShell content={<div>Console content</div>} />
      </MemoryRouter>
    );

    expect(screen.getByLabelText(/language/i)).toBeInTheDocument();
    expect(screen.getByRole("group", { name: /theme/i })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /apply/i })).not.toBeInTheDocument();
  });

  test("uses a compact custom language select in the app bar", () => {
    renderWithConsoleProviders(
      <MemoryRouter>
        <AppShell content={<div>Console content</div>} />
      </MemoryRouter>
    );

    const languageTrigger = screen.getByRole("button", { name: /language/i });
    expect(languageTrigger).toHaveAttribute("aria-haspopup", "listbox");
    expect(document.querySelector(".locale-switch--select")).toBeInTheDocument();
    expect(document.querySelector(".locale-switch--select select.bh-select__native")).toBeInTheDocument();
  });

  test("changes locale through the custom language menu", () => {
    renderWithConsoleProviders(
      <MemoryRouter>
        <AppShell content={<div>Console content</div>} />
      </MemoryRouter>
    );

    fireEvent.click(screen.getByRole("button", { name: /language/i }));
    fireEvent.mouseDown(screen.getByRole("option", { name: "中文" }));

    expect(screen.getByRole("navigation", { name: "控制台分区" })).toBeInTheDocument();
    expect(document.querySelector(".locale-switch--select select.bh-select__native")).toHaveValue(
      "zh-CN"
    );
  });

  test("changes theme through the multi-theme picker", () => {
    renderWithConsoleProviders(
      <MemoryRouter>
        <AppShell content={<div>Console content</div>} />
      </MemoryRouter>
    );

    expect(document.documentElement).toHaveAttribute("data-theme", "bauhaus-dark");

    const classic = screen.getByRole("button", { name: /classic/i });
    fireEvent.click(classic);

    expect(document.documentElement).toHaveAttribute("data-theme", "bauhaus-classic");
  });

  test("keeps route content in a named main landmark with mobile-safe nav labels", () => {
    renderWithConsoleProviders(
      <MemoryRouter>
        <AppShell content={<div>Console content</div>} />
      </MemoryRouter>
    );

    expect(screen.getByRole("main", { name: "Console route content" })).toHaveTextContent("Console content");
    expect(screen.getByRole("link", { name: /models & providers/i })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /search & tools/i })).toBeInTheDocument();
    expect(screen.getByRole("navigation", { name: /console sections/i })).not.toHaveTextContent("YAML");
    expect(screen.getByRole("navigation", { name: /console sections/i })).not.toHaveTextContent("Diagnostics");
    expect(screen.getByRole("navigation", { name: /console sections/i })).not.toHaveTextContent("Logs");
  });

  test("gives shell panels Bauhaus geometric surfaces", () => {
    renderWithConsoleProviders(
      <MemoryRouter>
        <AppShell
          content={(
            <>
              <section className="content-panel">Console content</section>
              <section className="placeholder-panel">Placeholder content</section>
            </>
          )}
        />
      </MemoryRouter>
    );

    const shellStyle = document.querySelector("style")?.textContent ?? "";
    const railRule = shellStyle.match(/\.navigation-rail \{[^}]+\}/)?.[0] ?? "";
    const rail = document.querySelector(".navigation-rail")!;
    const contentPanel = document.querySelector(".content-panel")!;
    const placeholderPanel = document.querySelector(".placeholder-panel")!;

    expect(railRule).toContain("background: var(--mb-color-surface-container-low)");
    for (const panel of [rail, contentPanel, placeholderPanel]) {
      expectPanelElementToBeFlat(panel);
    }
    expectPanelRuleToAvoidEdges(".navigation-rail");
    expectPanelRuleToAvoidEdges(".content-panel");
    expectPanelRuleToAvoidEdges(".placeholder-panel");
  });
});
