import { describe, expect, it } from "vitest";

import { DEFAULT_AFTER_LOGIN, safeReturnTo } from "./returnTo";

/**
 * The OAuth callback survives a lapsed session — and cannot be turned into
 * an open redirect on the way.
 *
 * ── Why this file exists ───────────────────────────────────────────────
 * Meta redirects to `/app/settings/integrations?code=…&state=…`. If the
 * session has expired, `RequireAuth` bounces to `/login` and the login page
 * decides where to land afterwards. Before this, the guard passed only
 * `pathname` and the login page ignored even that — so the authorization
 * code was discarded in silence, and the operator saw a login form followed
 * by an Integrations page that still said "not connected", with nothing
 * anywhere explaining why.
 */
describe("safeReturnTo · OAuth callback preservation", () => {
  it("keeps the code and state of a Threads callback", () => {
    const callback = "/app/settings/integrations?code=AQBx_authcode&state=abc123";
    // The whole query has to survive, not just the path. The card reads
    // BOTH parameters, and a `state` that failed to come back would be
    // refused as a forged callback even though it was genuine.
    expect(safeReturnTo(callback)).toBe(callback);
  });

  it("keeps a callback that carries an error instead of a code", () => {
    const denied = "/app/settings/integrations?error=access_denied&error_reason=user_denied";
    expect(safeReturnTo(denied)).toBe(denied);
  });

  it("keeps an ordinary path with no query at all", () => {
    expect(safeReturnTo("/app/modules/agents")).toBe("/app/modules/agents");
  });

  /* ── the open redirect this must never become ─────────────────────── */

  it("refuses a protocol-relative path, which the browser reads as another origin", () => {
    // The dangerous one: `//host` is not a path, it is `https://host`. A
    // login page that navigated there would hand the session to whoever
    // sent the link.
    expect(safeReturnTo("//evil.example/app")).toBe(DEFAULT_AFTER_LOGIN);
    expect(safeReturnTo("/\\evil.example/app")).toBe(DEFAULT_AFTER_LOGIN);
  });

  it("refuses an absolute URL", () => {
    expect(safeReturnTo("https://evil.example/app")).toBe(DEFAULT_AFTER_LOGIN);
    expect(safeReturnTo("javascript:alert(1)")).toBe(DEFAULT_AFTER_LOGIN);
  });

  it("refuses anything that is not an absolute path", () => {
    for (const bad of ["", "app/settings", "../app", null, undefined, 42, {}]) {
      expect(safeReturnTo(bad)).toBe(DEFAULT_AFTER_LOGIN);
    }
  });

  it("refuses control characters, which are how a value is smuggled past a log", () => {
    // Built from a char code rather than typed, so the control byte
    // cannot be lost or normalised by whatever edits this file next.
    const newline = String.fromCharCode(10);
    expect(safeReturnTo("/app" + newline + "/settings")).toBe(DEFAULT_AFTER_LOGIN);
    const tab = String.fromCharCode(9);
    expect(safeReturnTo("/app" + tab + "/settings")).toBe(DEFAULT_AFTER_LOGIN);
  });
});
