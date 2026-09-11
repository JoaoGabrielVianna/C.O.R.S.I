import { describe, it, expect, vi, afterEach } from "vitest";
import { checkBackendIdentity, EXPECTED_APPLICATION } from "./backendIdentity";

/**
 * The guard's whole job is telling three situations apart, because they
 * license three different behaviours: render, render, and refuse.
 */

function respondWith(body: string, ok = true) {
  return vi.fn().mockResolvedValue({
    ok,
    text: async () => body,
  } as unknown as Response);
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("backend identity", () => {
  it("accepts the real C.O.R.S.I. API", async () => {
    vi.stubGlobal("fetch", respondWith(JSON.stringify({ status: "ok", application: "corsi" })));
    await expect(checkBackendIdentity("")).resolves.toEqual({ kind: "ok" });
  });

  // The exact shape the foreign API on :8080 returned. 200 OK, valid JSON,
  // plausible field — and not this product.
  it("rejects another product answering healthily", async () => {
    vi.stubGlobal("fetch", respondWith(JSON.stringify({ status: "alive" })));
    const result = await checkBackendIdentity("");
    expect(result.kind).toBe("mismatch");
    if (result.kind === "mismatch") expect(result.received).toBe("unidentified");
  });

  it("names a different application when it identifies itself", async () => {
    vi.stubGlobal("fetch", respondWith(JSON.stringify({ application: "saas-api" })));
    const result = await checkBackendIdentity("");
    expect(result.kind).toBe("mismatch");
    if (result.kind === "mismatch") expect(result.received).toBe("saas-api");
  });

  // The Vite SPA fallback returns index.html when no proxy rule matches.
  // That is its own bug and deserves the same loud treatment, not a shrug.
  it("treats an HTML body as a mismatch rather than a success", async () => {
    vi.stubGlobal("fetch", respondWith("<!doctype html><html></html>"));
    expect((await checkBackendIdentity("")).kind).toBe("mismatch");
  });

  // Nothing there is the normal state while the backend compiles. It must
  // not blank the UI: only a positive identification of something else does.
  it("reports an absent backend as unreachable, not as a mismatch", async () => {
    vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new Error("ECONNREFUSED")));
    expect((await checkBackendIdentity("")).kind).toBe("unreachable");
  });

  // The constant is half of a contract whose other half is a Go const.
  it("expects the same string the backend reports", () => {
    expect(EXPECTED_APPLICATION).toBe("corsi");
  });
});
