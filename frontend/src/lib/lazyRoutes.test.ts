// @vitest-environment jsdom

import { afterAll, beforeAll, expect, it, vi } from "vitest";

import { preloadRoute } from "./lazyRoutes";

/**
 * A prefetch downloads CODE. That is the whole contract.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   HOVER MAY SPEND BANDWIDTH. IT MAY NOT READ THE OPERATOR'S DATA
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * A chunk is versioned, cacheable and identical for everybody, so fetching
 * one on a guess costs bytes and nothing else. A query is not: it would put
 * a read of the Palace in the log, in the metrics and in the audit trail
 * because a pointer crossed a word, indistinguishable from a read the
 * operator asked for. These tests pin that difference rather than the
 * intention behind it.
 *
 * ── How "the module was imported" is observed ──────────────────────────
 * Each page module is mocked with a factory that records being evaluated.
 * A module evaluates at most ONCE per registry, which is also the real
 * guarantee behind "no double download": the guard inside `preloadRoute`
 * saves a promise and a microtask, the module registry is what makes the
 * second hover free. The tests are written against that observable fact,
 * and they run in one registry on purpose — resetting it between them
 * would measure the harness instead.
 */

const { evaluated } = vi.hoisted(() => ({ evaluated: [] as string[] }));

vi.mock("@/pages/app/modules/job-radar", () => {
  evaluated.push("job-radar");
  return { JobRadarPage: () => null };
});
vi.mock("@/pages/app/modules/finance", () => {
  evaluated.push("finance");
  return { FinancePage: () => null };
});
vi.mock("@/pages/app/modules/agents", () => {
  evaluated.push("agents");
  return { AgentsPage: () => null };
});
vi.mock("@/pages/app/modules/palace", () => {
  evaluated.push("palace");
  return { PalacePage: () => null };
});
vi.mock("@/pages/app/releases", () => {
  evaluated.push("releases");
  return { ReleasesPage: () => null };
});
vi.mock("@/pages/app/PersonDetail", () => {
  evaluated.push("person-detail");
  return { PersonDetailPage: () => null };
});

beforeAll(() => {
  vi.stubGlobal("fetch", vi.fn());
});
afterAll(() => vi.unstubAllGlobals());

const settle = () => new Promise((r) => setTimeout(r, 20));

it("ignores everything that is not a sidebar destination", async () => {
  preloadRoute("/app/settings/profile");
  preloadRoute("/app/dashboard");
  preloadRoute("/app/modules/news");
  // Lazy, and deliberately NOT preloadable: nobody hovers their way into a
  // person's page, so guessing there would be spending on a coin flip.
  preloadRoute("/app/people/11111111-1111-1111-1111-111111111111");
  preloadRoute("");

  await settle();
  expect(evaluated).toEqual([]);
});

it("starts the import for every destination the sidebar links to", async () => {
  for (const path of [
    "/app/modules/job-radar",
    "/app/modules/finance",
    "/app/modules/agents",
    "/app/modules/palace",
    "/app/releases",
  ]) {
    preloadRoute(path);
  }

  await vi.waitFor(() =>
    expect([...evaluated].sort()).toEqual([
      "agents",
      "finance",
      "job-radar",
      "palace",
      "releases",
    ]),
  );
});

it("downloads each chunk once, however many times it is pointed at", async () => {
  const before = [...evaluated];

  preloadRoute("/app/modules/palace");
  preloadRoute("/app/modules/palace");
  // A deep link into a module is the same chunk, not a second one.
  preloadRoute("/app/modules/palace/library");
  preloadRoute("/app/modules/palace/rooms/11111111-1111-1111-1111-111111111111");
  preloadRoute("/app/releases/agents/1.6.0");

  await settle();
  expect(evaluated).toEqual(before);
  expect(evaluated.filter((m) => m === "palace")).toHaveLength(1);
});

it("makes no API request, ever", () => {
  // Importing a module evaluates it. It does not render it, so no hook
  // runs, no effect fires and nothing is read on the operator's behalf.
  expect(fetch).not.toHaveBeenCalled();
});

it("never reaches the one lazy page that is not a destination", () => {
  expect(evaluated).not.toContain("person-detail");
});
