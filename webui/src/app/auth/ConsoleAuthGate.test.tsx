import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, test, vi } from "vitest";
import { CONSOLE_LOCALE_STORAGE_KEY, I18nProvider } from "../../i18n/I18nProvider";
import { ThemeProvider } from "../../theme/ThemeProvider";
import { ConsoleAuthProvider, useConsoleAuth } from "./ConsoleAuthContext";
import { ConsoleAuthGate } from "./ConsoleAuthGate";

function Harness() {
  const auth = useConsoleAuth();
  return (
    <ConsoleAuthGate>
      <span>App content</span>
      <button type="button" onClick={() => auth.signOut()}>
        lock
      </button>
    </ConsoleAuthGate>
  );
}

function renderHarness() {
  localStorage.setItem(CONSOLE_LOCALE_STORAGE_KEY, "en-US");
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <I18nProvider>
        <ThemeProvider>
          <ConsoleAuthProvider>
            <Harness />
          </ConsoleAuthProvider>
        </ThemeProvider>
      </I18nProvider>
    </QueryClientProvider>
  );
}

describe("ConsoleAuthGate", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  test("renders the app when the console is unlocked", () => {
    renderHarness();

    expect(screen.getByText("App content")).toBeInTheDocument();
    expect(document.querySelector(".bh-field:not(.bh-select)")).not.toBeInTheDocument();
  });

  test("replaces the app with the Material login card when locked", () => {
    renderHarness();

    fireEvent.click(screen.getByText("lock"));

    expect(screen.queryByText("App content")).not.toBeInTheDocument();
    const card = document.querySelector(".auth-card");
    expect(card).toBeInTheDocument();
    // Real Material Web controls drive the login (skill requirement).
    expect(card?.querySelector(".bh-field:not(.bh-select)")).toBeInTheDocument();
    expect(card?.querySelector("button.bh-button--filled, .bh-button--filled")).toBeInTheDocument();
  });
});
