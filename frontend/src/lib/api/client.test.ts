import { afterEach, describe, expect, it, vi } from "vitest";

import { ApiError, apiFetch } from "./client";

/**
 * What the HTTP layer does with a body it cannot read.
 *
 * ── The failure this file was written from ─────────────────────────────
 * `/lembrar` reported "non-JSON response from server". The actual response
 * was a plain-text `404 page not found` from a backend that did not have
 * the route — and the status, the single fact that would have diagnosed it,
 * never reached the screen or the console.
 *
 * So there are two properties here, and the first one matters more:
 *
 *   1. an unreadable body is STILL an error, always;
 *   2. the error says what the status was.
 *
 * The first is what stops this from becoming the fix that "solved" the bug
 * by accepting HTML as data. Every relaxation test below is a negative one.
 */

function respondWith(body: string, init: ResponseInit & { headers?: Record<string, string> }) {
  vi.stubGlobal(
    "fetch",
    // `null` for the statuses the Response constructor forbids a body on.
    // 204 is one of them, which is also why the client has a branch for it.
    vi.fn(async () => new Response(init.status === 204 ? null : body, init)),
  );
}

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("apiFetch with an unreadable body", () => {
  it("rejects a plain-text 404 and names the status", async () => {
    // Byte for byte what chi's default NotFound handler used to answer, and
    // what the failing `/lembrar` actually received.
    respondWith("404 page not found", {
      status: 404,
      headers: { "Content-Type": "text/plain; charset=utf-8" },
    });

    const err = await apiFetch("/chat/conversations/x/memory-candidates", {
      method: "POST",
    }).catch((e: unknown) => e);

    expect(err).toBeInstanceOf(ApiError);
    const apiErr = err as ApiError;
    // The status is the diagnosis. Losing it is what cost the hours.
    expect(apiErr.status).toBe(404);
    expect(apiErr.message).toContain("404");
    // And it must not claim to have understood anything.
    expect(apiErr.message).not.toContain("page not found");
  });

  it("says a request answered with HTML probably never reached the API", async () => {
    // The Vite SPA fallback: a prefix missing from the proxy, so the page
    // itself answers where JSON was expected. It happened to Releases once.
    respondWith("<!doctype html><html><body>app</body></html>", {
      status: 200,
      headers: { "Content-Type": "text/html" },
    });

    const err = (await apiFetch("/releases").catch((e: unknown) => e)) as ApiError;

    expect(err).toBeInstanceOf(ApiError);
    expect(err.status).toBe(200);
    expect(err.message).toContain("HTML");
    expect(err.message).toMatch(/never reached the API/);
  });

  it("refuses HTML even when the status says 200", async () => {
    // The property that must never be traded away. A 200 whose body is a
    // page is not a success — accepting it would hand the caller a value it
    // would go on to read fields off.
    respondWith("<html>ok</html>", { status: 200, headers: { "Content-Type": "text/html" } });
    await expect(apiFetch("/chat/agents")).rejects.toBeInstanceOf(ApiError);
  });

  it("refuses any other non-JSON body", async () => {
    respondWith("upstream connect error", {
      status: 502,
      headers: { "Content-Type": "text/plain" },
    });

    const err = (await apiFetch("/chat/agents").catch((e: unknown) => e)) as ApiError;
    expect(err.status).toBe(502);
    expect(err.message).toContain("502");
    expect(err.message).toContain("not JSON");
  });
});

describe("apiFetch with a readable body", () => {
  it("still parses a JSON error into its code and message", async () => {
    // Unchanged, and asserted so the new branch cannot start swallowing the
    // errors the backend states properly.
    respondWith(JSON.stringify({ error: { code: "memory_consolidation_disabled", message: "desligado" } }), {
      status: 409,
      headers: { "Content-Type": "application/json" },
    });

    const err = (await apiFetch("/chat/x").catch((e: unknown) => e)) as ApiError;
    expect(err.status).toBe(409);
    expect(err.code).toBe("memory_consolidation_disabled");
    expect(err.message).toBe("desligado");
  });

  it("still returns a successful JSON body", async () => {
    respondWith(JSON.stringify({ candidates: [], considered_messages: 4 }), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    });

    await expect(apiFetch("/chat/x")).resolves.toEqual({
      candidates: [],
      considered_messages: 4,
    });
  });

  it("still treats 204 as a body-less success", async () => {
    respondWith("", { status: 204 });
    await expect(apiFetch("/chat/x")).resolves.toBeUndefined();
  });
});
