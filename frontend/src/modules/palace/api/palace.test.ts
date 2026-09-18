import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import * as palace from "./palace";

/**
 * The sensitivity opt-in must be inexpressible from the frontend.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   THE BACKEND IS THE AUTHORITY. THIS IS THE SECOND LOCK, NOT THE FIRST
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * Visibility is decided in SQL, and no frontend change can weaken it: the
 * route does not read such a parameter and the predicate does not consult
 * one. So why test here at all?
 *
 * Because "the server would ignore it" is the argument that ends with a
 * client sending it. A request carrying `include_highly_sensitive=true`
 * would be in a browser's history, in a proxy log and in a screenshot of
 * somebody's address bar, and it would tell a future reader that the
 * option exists and merely failed. It does not exist. This asserts that
 * the shape of every Palace request says so.
 *
 * ── Why it drives the real functions instead of reading the source ─────
 * A grep proves the literal is absent today. Driving `fetch` proves that
 * whatever the query builder does with whatever it is given, the key never
 * reaches the wire — including when a caller spreads an object that came
 * from a URL somebody bookmarked.
 */

const FORBIDDEN = [
  "include_highly_sensitive",
  "includeHighlySensitive",
  "include_sensitive",
  "highly_sensitive",
];

let urls: string[] = [];

beforeEach(() => {
  urls = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) => {
      urls.push(String(input));
      return new Response(JSON.stringify({ items: [], total: 0, limit: 25, offset: 0 }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    }),
  );
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("palace api · no sensitivity opt-in", () => {
  it("never puts a sensitivity opt-in on the wire, on any route", async () => {
    await palace.listRooms({ search: "x", status: "active", limit: 25, offset: 0 });
    await palace.getRoom("11111111-1111-1111-1111-111111111111");
    await palace.listArtifacts({
      search: "x",
      status: "archived",
      kind: "list",
      room_id: palace.UNFILED,
      limit: 25,
      offset: 50,
    });
    await palace.getArtifact("22222222-2222-2222-2222-222222222222", {
      item_limit: 100,
      item_offset: 0,
    });
    await palace.listMemories({
      search: "x",
      status: "active",
      kind: "decision",
      room_id: "33333333-3333-3333-3333-333333333333",
      artifact_id: "44444444-4444-4444-4444-444444444444",
      min_importance: 3,
      limit: 25,
      offset: 0,
    });
    await palace.getMemory("55555555-5555-5555-5555-555555555555");

    expect(urls).toHaveLength(6);
    for (const url of urls) {
      for (const key of FORBIDDEN) {
        expect(url.toLowerCase()).not.toContain(key.toLowerCase());
      }
    }
  });

  it("drops a smuggled key instead of forwarding it", async () => {
    // What a caller spreading a params object out of a bookmarked URL
    // would produce. The closed `Query` union already refuses this at
    // compile time; the cast is the test standing in for a future `any`.
    const smuggled = {
      search: "x",
      include_highly_sensitive: "true",
      includeHighlySensitive: true,
    } as unknown as palace.ListArtifactsParams;

    await palace.listArtifacts(smuggled);

    expect(urls[0]).toContain("search=x");
    for (const key of FORBIDDEN) {
      expect(urls[0].toLowerCase()).not.toContain(key.toLowerCase());
    }
  });

  it("exposes only read functions", () => {
    // A write client would be a second way to change the operator's
    // record, with none of the authorization a capability carries.
    const exported = Object.entries(palace).filter(([, v]) => typeof v === "function");
    expect(exported.map(([name]) => name).sort()).toEqual([
      "getArtifact",
      "getMemory",
      "getOverview",
      "getRoom",
      "listArtifacts",
      "listMemories",
      "listRooms",
    ]);
    // Every one of them is a GET. A name that did not start with `get` or
    // `list` would be the first write client, and this list is where it
    // would have to be admitted rather than slipped in.
    for (const [name] of exported) {
      expect(name).toMatch(/^(get|list)/);
    }
  });

  it("omits empty values rather than sending blanks", async () => {
    await palace.listRooms({ search: "" });
    expect(urls[0]).toBe("/palace/rooms");
  });
});
