// @vitest-environment jsdom

import { cleanup, render, screen } from "@testing-library/react";
import { MemoryRouter, Navigate, Route, Routes, useLocation } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import { RequireAuth } from "./RequireAuth";
import { safeReturnTo } from "@/lib/auth/returnTo";

/**
 * The OAuth callback must survive an expired session.
 *
 * ── The failure this is a regression test for ──────────────────────────
 * Meta redirects the browser to
 *
 *     /app/settings/integrations?code=<single-use>&state=<csrf>
 *
 * `RequireAuth` used to send an unauthenticated visitor to `/login` with
 * only `location.pathname`, so both parameters were gone before the login
 * form even rendered. The code is single-use and short-lived: once
 * dropped, the only recovery is to start the whole authorization again —
 * and nothing on screen said so.
 *
 * ── What is asserted, and at which seam ────────────────────────────────
 * Not the mock login form, which is placeholder auth and will be replaced
 * by Keycloak. What is asserted is the CONTRACT between the two halves:
 * the guard records where the browser was going, query and all, and the
 * destination that comes back out is the one the callback needs.
 */

vi.mock("@/lib/auth", () => ({
  useAuth: () => mockAuth,
}));

let mockAuth: { isAuthenticated: boolean; status: string } = {
  isAuthenticated: false,
  status: "ready",
};

const CALLBACK = "/app/settings/integrations?code=AQBx_single_use&state=csrf_abc";

/** Renders whatever router state the login page would have received. */
function LoginProbe() {
  const location = useLocation();
  const from = (location.state as { from?: unknown } | null)?.from;
  return (
    <div>
      <span data-testid="from">{typeof from === "string" ? from : "(none)"}</span>
      <span data-testid="resolved">{safeReturnTo(from)}</span>
    </div>
  );
}

function renderAt(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/login" element={<LoginProbe />} />
        <Route path="/app" element={<RequireAuth />}>
          <Route path="settings/integrations" element={<div>integrations page</div>} />
        </Route>
        <Route path="*" element={<Navigate to="/login" replace />} />
      </Routes>
    </MemoryRouter>,
  );
}

afterEach(() => {
  // Without this, each render stacks on the last one: the duplicate probe
  // makes getByTestId ambiguous, and — worse — a later assertion that the
  // code is ABSENT would read a previous test's DOM and pass or fail for
  // the wrong reason.
  cleanup();
  mockAuth = { isAuthenticated: false, status: "ready" };
  vi.restoreAllMocks();
});

describe("RequireAuth · OAuth callback with an expired session", () => {
  it("carries the authorization code and state through the login redirect", () => {
    mockAuth = { isAuthenticated: false, status: "ready" };
    renderAt(CALLBACK);

    // The guard bounced to /login, and it did NOT truncate at the path.
    const from = screen.getByTestId("from").textContent ?? "";
    expect(from).toBe(CALLBACK);
    expect(from).toContain("code=AQBx_single_use");
    expect(from).toContain("state=csrf_abc");

    // And the login page's own resolution keeps it — this is the value it
    // navigates to once the session exists.
    expect(screen.getByTestId("resolved").textContent).toBe(CALLBACK);
  });

  it("does not send the user anywhere but back to the callback", () => {
    mockAuth = { isAuthenticated: false, status: "ready" };
    renderAt(CALLBACK);
    // The old behaviour landed on /app and lost the flow. If this ever
    // reads "/app" again, the callback is being discarded.
    expect(screen.getByTestId("resolved").textContent).not.toBe("/app");
  });

  it("lets an authenticated visitor straight through, code and all", () => {
    mockAuth = { isAuthenticated: true, status: "ready" };
    renderAt(CALLBACK);
    // The normal path: no bounce at all, so the card reads the query from
    // the address bar exactly as Meta left it.
    expect(screen.getByText("integrations page")).toBeTruthy();
  });

  it("shows neither the code nor the state while the session is hydrating", () => {
    mockAuth = { isAuthenticated: false, status: "loading" };
    renderAt(CALLBACK);
    // A skeleton, not a redirect: bouncing during hydration would discard
    // the callback for a session that was about to be valid anyway.
    const rendered = document.body.textContent ?? "";
    expect(rendered).not.toContain("AQBx_single_use");
    expect(screen.queryByTestId("from")).toBeNull();
  });

  it("keeps an error callback too, so a refusal is still explainable", () => {
    mockAuth = { isAuthenticated: false, status: "ready" };
    const denied = "/app/settings/integrations?error=access_denied";
    renderAt(denied);
    expect(screen.getByTestId("resolved").textContent).toBe(denied);
  });
});
