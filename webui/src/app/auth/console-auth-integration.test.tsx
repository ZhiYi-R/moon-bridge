import { useQuery } from "@tanstack/react-query";
import { act, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, test, vi } from "vitest";
import { apiFetch, clearStoredToken } from "../../rpc/http";
import { renderWithConsoleProviders } from "../../test/renderWithConsoleProviders";
import { AppShell } from "../App";
import { ConsoleAuthGate } from "./ConsoleAuthGate";

type MaterialTextFieldElement = HTMLElement & { label: string; value: string };

function jsonResponse(body: unknown, init?: ResponseInit) {
  return new Response(JSON.stringify(body), {
    headers: { "Content-Type": "application/json" },
    ...init
  });
}

function StatusPage() {
  const { data } = useQuery({
    queryKey: ["status"],
    queryFn: () => apiFetch<{ ok: boolean }>("/status")
  });
  return <div>{data?.ok ? "status ok" : "status pending"}</div>;
}

function getOutlinedTextField(label: string) {
  const element = Array.from(
    document.querySelectorAll<HTMLElement>(".bh-field:not(.bh-select)")
  ).find((candidate) => candidate.getAttribute("data-label") === label);
  if (!element) {
    throw new Error(`Expected a text field labelled "${label}".`);
  }
  return element as MaterialTextFieldElement;
}

function setTextFieldValue(element: HTMLElement, value: string) {
  const control = element.querySelector("input, textarea") as HTMLInputElement | HTMLTextAreaElement | null;
  if (!control) {
    throw new Error("Expected input inside text field");
  }
  act(() => {
    const proto = Object.getPrototypeOf(control);
    const desc = Object.getOwnPropertyDescriptor(proto, "value");
    desc?.set?.call(control, value);
    control.dispatchEvent(new InputEvent("input", { bubbles: true }));
    control.dispatchEvent(new Event("change", { bubbles: true }));
  });
}

async function submitAuthCard() {
  const button = Array.from(document.querySelectorAll("button.bh-button--filled, .bh-button--filled")).find(
    (candidate) => candidate.textContent?.trim() === "Save token"
  );
  if (!button) {
    throw new Error("Expected the Save token button.");
  }
  const form = button.closest("form");
  if (!form) {
    throw new Error("Expected the submit button inside the auth form.");
  }
  let submitted = false;
  form.addEventListener("submit", () => {
    submitted = true;
  }, { once: true });
  await userEvent.click(button);
  await new Promise((resolve) => setTimeout(resolve, 0));
  if (!submitted) {
    await act(async () => {
      form.requestSubmit();
      await Promise.resolve();
    });
  }
}

describe("console auth integration", () => {
  afterEach(() => {
    vi.restoreAllMocks();
    clearStoredToken();
    sessionStorage.clear();
    localStorage.clear();
  });

  test("a 401 locks the console, then a valid token unlocks it via a Bearer refetch", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockImplementation((input, init) => {
      const headers = new Headers(init?.headers);
      if (headers.get("Authorization") === "Bearer good") {
        return Promise.resolve(jsonResponse({ ok: true }));
      }
      return Promise.resolve(
        jsonResponse(
          { error: { code: "invalid_auth", message: "missing or invalid token" } },
          { status: 401 }
        )
      );
    });

    renderWithConsoleProviders(
      <MemoryRouter>
        <ConsoleAuthGate>
          <AppShell content={<StatusPage />} />
        </ConsoleAuthGate>
      </MemoryRouter>
    );

    // Initial 401 locks the console and shows the Material login card.
    await waitFor(() => expect(document.querySelector(".auth-card")).toBeInTheDocument());

    // The Material text field's `.label` reflects once Lit upgrades the custom
    // element, which happens slightly after the card mounts.
    const tokenField = await waitFor(() => getOutlinedTextField("Token"));
    setTextFieldValue(tokenField, "good");
    await submitAuthCard();

    // Valid token unlocks; the page renders with verified data.
    await waitFor(() => expect(document.querySelector(".auth-card")).not.toBeInTheDocument());
    await waitFor(() => expect(screen.getByText("status ok")).toBeInTheDocument());

    const sentGood = fetchMock.mock.calls.some(([, init]) => {
      const headers = new Headers(init?.headers);
      return headers.get("Authorization") === "Bearer good";
    });
    expect(sentGood).toBe(true);
  });
});
