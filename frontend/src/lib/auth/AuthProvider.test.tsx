// @vitest-environment jsdom

import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { cleanup } from "@testing-library/react";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import { AuthProvider } from "./AuthProvider";
import { useAuth } from "./hooks";
import { apiFetch } from "@/lib/api/client";

/**
 * The provider that replaced the mock.
 *
 * ── What the mock made untestable ──────────────────────────────────────
 * It did `void password` and wrote a user to `localStorage`, so "logged in"
 * was a fact the browser owned. There was no server to disagree with, which
 * meant there was nothing to assert about a session ending, a wrong
 * password, or a refusal arriving from somewhere else in the app. All three
 * are what this file pins.
 */

vi.mock("@/lib/api/client", async () => {
  const actual = await vi.importActual<typeof import("@/lib/api/client")>("@/lib/api/client");
  return { ...actual, apiFetch: vi.fn() };
});

const fetchMock = vi.mocked(apiFetch);

function Probe() {
  const { status, user, signIn, signOut } = useAuth();
  return (
    <div>
      <span data-testid="status">{status}</span>
      <span data-testid="email">{user?.email ?? "—"}</span>
      <button onClick={() => void signIn("a@b.com", "pw").catch(() => {})}>in</button>
      <button onClick={() => void signOut()}>out</button>
    </div>
  );
}

function renderProbe() {
  return render(
    <AuthProvider>
      <Probe />
    </AuthProvider>,
  );
}

const session = { email: "joao@corsi.dev", expires_at: "2030-01-01T00:00:00Z" };

beforeEach(() => fetchMock.mockReset());
afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe("AuthProvider", () => {
  it("starts in loading and never flashes unauthenticated before the server answers", async () => {
    // A provider that started `unauthenticated` would make RequireAuth
    // redirect on every hard refresh, bouncing a logged-in operator out of
    // their own app once per reload.
    let resolve!: (v: unknown) => void;
    fetchMock.mockReturnValueOnce(new Promise((r) => (resolve = r)));

    renderProbe();
    expect(screen.getByTestId("status").textContent).toBe("loading");

    resolve(session);
    await waitFor(() => expect(screen.getByTestId("status").textContent).toBe("authenticated"));
  });

  it("adopts the session the server reports on boot", async () => {
    fetchMock.mockResolvedValueOnce(session);
    renderProbe();

    await waitFor(() => expect(screen.getByTestId("email").textContent).toBe("joao@corsi.dev"));
    expect(fetchMock).toHaveBeenCalledWith("/auth/session");
  });

  it("resolves a refused bootstrap to unauthenticated rather than to an error", async () => {
    fetchMock.mockRejectedValueOnce(new Error("401"));
    renderProbe();

    await waitFor(() => expect(screen.getByTestId("status").textContent).toBe("unauthenticated"));
    expect(screen.getByTestId("email").textContent).toBe("—");
  });

  it("signs in through the API and never persists anything locally", async () => {
    fetchMock.mockRejectedValueOnce(new Error("401")); // boot: nobody
    fetchMock.mockResolvedValueOnce(session); // login
    const user = userEvent.setup();
    renderProbe();
    await waitFor(() => expect(screen.getByTestId("status").textContent).toBe("unauthenticated"));

    await user.click(screen.getByText("in"));

    await waitFor(() => expect(screen.getByTestId("status").textContent).toBe("authenticated"));
    expect(fetchMock).toHaveBeenLastCalledWith("/auth/login", {
      method: "POST",
      body: JSON.stringify({ email: "a@b.com", password: "pw" }),
    });
    // The session is an HttpOnly cookie. Anything this code wrote down
    // would be a second, forgeable copy of the answer.
    expect(window.localStorage.length).toBe(0);
    expect(document.cookie).not.toContain("corsi_session");
  });

  it("rethrows a failed sign-in so the login screen can show one generic message", async () => {
    fetchMock.mockRejectedValueOnce(new Error("401")); // boot
    fetchMock.mockRejectedValueOnce(new Error("invalid")); // login
    const user = userEvent.setup();
    renderProbe();
    await waitFor(() => expect(screen.getByTestId("status").textContent).toBe("unauthenticated"));

    await user.click(screen.getByText("in"));

    await waitFor(() => expect(screen.getByTestId("status").textContent).toBe("unauthenticated"));
    expect(screen.getByTestId("email").textContent).toBe("—");
  });

  it("signs out through the API and drops the local view even when that call fails", async () => {
    fetchMock.mockResolvedValueOnce(session); // boot: signed in
    const user = userEvent.setup();
    renderProbe();
    await waitFor(() => expect(screen.getByTestId("status").textContent).toBe("authenticated"));

    // A logout the server never heard still has to let go locally,
    // otherwise the one state the operator cannot leave is the broken one.
    fetchMock.mockRejectedValueOnce(new Error("network"));
    await user.click(screen.getByText("out"));

    await waitFor(() => expect(screen.getByTestId("status").textContent).toBe("unauthenticated"));
    expect(fetchMock).toHaveBeenCalledWith("/auth/logout", { method: "POST" });
  });
});

describe("the 401 channel", () => {
  it("logs the UI out when any request reports the session is gone", async () => {
    // `importActual` reaches past this file's module mock, so the wiring
    // between apiFetch's 401 handling and its listeners is what runs —
    // not a mock of it. It is `importActual` rather than `vi.unmock`
    // because unmock is HOISTED to the top of the file and would cancel
    // the mock for every test above.
    const real = await vi.importActual<typeof import("@/lib/api/client")>("@/lib/api/client");

    const listener = vi.fn();
    const off = real.onUnauthorized(listener);

    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({ error: { code: "unauthenticated", message: "no" } }), {
        status: 401,
        headers: { "Content-Type": "application/json" },
      }),
    );

    await expect(real.apiFetch("/finance/summary")).rejects.toThrow();
    expect(listener).toHaveBeenCalledTimes(1);

    // A wrong password is also a 401 and is NOT a lapsed session.
    listener.mockClear();
    await expect(real.apiFetch("/auth/login", { method: "POST" })).rejects.toThrow();
    expect(listener).not.toHaveBeenCalled();

    off();
    fetchSpy.mockRestore();
  });

  it("sends credentials on every request", async () => {
    const real = await vi.importActual<typeof import("@/lib/api/client")>("@/lib/api/client");
    const fetchSpy = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValue(new Response("{}", { status: 200, headers: { "Content-Type": "application/json" } }));

    await real.apiFetch("/finance/summary");

    expect(fetchSpy.mock.calls[0]?.[1]).toMatchObject({ credentials: "include" });
    fetchSpy.mockRestore();
  });
});
