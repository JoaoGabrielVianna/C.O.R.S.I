// @vitest-environment jsdom

import { lazy, Suspense, useEffect, type ComponentType } from "react";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Link, MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, describe, expect, it } from "vitest";

import appSource from "./App.tsx?raw";
import lazyRoutesSource from "./lib/lazyRoutes.ts?raw";
import layoutSource from "./components/workspace/WorkspaceLayout.tsx?raw";

/**
 * ONE Suspense boundary, in a position that does not move.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   A BOUNDARY THAT MOUNTS ALREADY SUSPENDED HAS NO CONTENT TO KEEP
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * ── The defect ─────────────────────────────────────────────────────────
 * `App.tsx` declared a `<Suspense>` beside each lazy route: six siblings.
 * Their position in the element tree changed with the route — `settings`
 * nests one level deeper than `modules/finance`, `releases/*` is a splat
 * where `finance` is static — so React did not reconcile the old boundary
 * with the new one. It MOUNTED a new boundary, already suspended, with no
 * previous children to hold on screen, and drew the fallback.
 *
 * Measured in Chrome against the production build with latency emulated at
 * ZERO and the chunk served in 1 to 3ms: 17 frames, 277 to 284ms, of the
 * route area replaced by a loading bar. That is a floor, not a download,
 * and it is what reads as "the application reloaded".
 *
 * ── Why a structural test AND a behavioural one ────────────────────────
 * The behavioural test below pins the MECHANISM: a stable boundary keeps
 * the previous page visible while the next chunk loads, and boundaries at
 * shifting positions do not. It is a real difference and jsdom shows it.
 *
 * What jsdom cannot show is the cost: React's reveal throttle and the
 * chunk request do not exist here, so the fallback it renders lasts zero
 * milliseconds. The 300ms a person waits is only measurable in a browser,
 * and it is measured there — this file does not simulate it and must not
 * be read as proof of it.
 *
 * The structural test is what catches a regression written in a shape the
 * behavioural one happens not to exercise. It counts boundaries across the
 * whole source tree, because the defect was never a bad boundary: it was
 * the SECOND one.
 */

afterEach(cleanup);

/* ── structure ──────────────────────────────────────────────────────── */

/** Source with comments removed, so prose about Suspense is not a match. */
function code(src: string): string {
  return src.replace(/\/\*[\s\S]*?\*\//g, " ").replace(/(^|[^:])\/\/.*$/gm, "$1");
}

const sources = import.meta.glob("./**/*.tsx", {
  query: "?raw",
  import: "default",
  eager: true,
}) as Record<string, string>;

describe("Suspense topology", () => {
  it("declares exactly one boundary in the whole source tree", () => {
    const declaring = Object.entries(sources)
      .filter(([path]) => !path.endsWith(".test.tsx"))
      .map(([path, src]) => [path, (code(src).match(/<Suspense[\s>]/g) ?? []).length] as const)
      .filter(([, count]) => count > 0);

    // One file, one boundary. A second entry here is the defect returning,
    // whatever shape it arrives in — per route, per module, per group.
    expect(declaring).toEqual([["./components/workspace/WorkspaceLayout.tsx", 1]]);
  });

  it("puts that boundary around the Outlet, inside the stable transition node", () => {
    const shell = code(layoutSource);
    expect(shell).toMatch(/<PageTransition>\s*<Suspense fallback=\{<RouteFallback \/>\}>\s*<Outlet \/>/);
  });

  it("keeps every module page lazy", () => {
    // The blank frames could also be removed by loading everything eagerly.
    // That trades a 300ms gap for a permanently larger initial bundle, and
    // it is not what this slice did. The `lazy()` calls moved to
    // `lib/lazyRoutes` when the preloader needed to name the same imports;
    // they are still dynamic.
    //
    // The number is the count of lazy pages and it MOVES when a module is
    // added — it was six before the Closet. What the assertion is really
    // for is the pairing below it: every `lazy()` must be backed by an
    // `import()`, because a `lazy()` wrapping a static import is the one
    // way to look lazy and ship eagerly.
    const registry = code(lazyRoutesSource);
    const lazyCalls = (registry.match(/lazy\(\(\) =>/g) ?? []).length;
    const dynamicImports = (registry.match(/=> import\(/g) ?? []).length;
    expect(lazyCalls).toBe(7);
    expect(dynamicImports).toBe(lazyCalls);

    const app = code(appSource);
    expect(app, "the router must not declare a boundary again").not.toContain("<Suspense");
    expect(app, "a page must not be imported statically").not.toMatch(
      /^import .*from "@\/pages\/app\/modules\/(finance|agents|palace|job-radar|closet)"/m,
    );
  });
});

/* ── mechanism ──────────────────────────────────────────────────────── */

/** A lazy component whose module never arrives until the test says so. */
function pendingLazy(text: string) {
  let release!: () => void;
  const arrived = new Promise<{ default: ComponentType }>((resolve) => {
    release = () => resolve({ default: () => <p>{text}</p> });
  });
  return { Component: lazy(() => arrived), release };
}

function Origin() {
  return (
    <>
      <p>origin content</p>
      <Link to="/app/settings/profile">go</Link>
    </>
  );
}

describe("a stable boundary keeps the previous page on screen", () => {
  it("does not show the fallback when the next chunk is still loading", async () => {
    const user = userEvent.setup();
    const { Component, release } = pendingLazy("destination");

    render(
      <MemoryRouter initialEntries={["/app/modules/finance"]}>
        {/* The shape this slice ships: one boundary above the routes. */}
        <Suspense fallback={<p>FALLBACK</p>}>
          <Routes>
            <Route path="/app/modules/finance" element={<Origin />} />
            <Route path="/app/settings">
              <Route path="profile" element={<Component />} />
            </Route>
          </Routes>
        </Suspense>
      </MemoryRouter>,
    );

    await user.click(screen.getByText("go"));

    // The navigation is a transition and the boundary was already mounted,
    // so what a person sees is still the page they were on.
    expect(screen.queryByText("FALLBACK")).toBeNull();
    expect(screen.getByText("origin content")).toBeTruthy();

    release();
    expect(await screen.findByText("destination")).toBeTruthy();
  });

  it("shows the fallback when each route brings its own boundary", async () => {
    const user = userEvent.setup();
    const { Component, release } = pendingLazy("destination");

    render(
      <MemoryRouter initialEntries={["/app/modules/finance"]}>
        {/* The shape this slice removed: a boundary per route, at a
            position that moves when the route does. */}
        <Routes>
          <Route
            path="/app/modules/finance"
            element={
              <Suspense fallback={<p>FALLBACK</p>}>
                <Origin />
              </Suspense>
            }
          />
          <Route path="/app/settings">
            <Route
              path="profile"
              element={
                <Suspense fallback={<p>FALLBACK</p>}>
                  <Component />
                </Suspense>
              }
            />
          </Route>
        </Routes>
      </MemoryRouter>,
    );

    await user.click(screen.getByText("go"));

    // Known content gone, loading state in its place. In a browser this is
    // where the 300ms lives.
    expect(screen.getByText("FALLBACK")).toBeTruthy();
    expect(screen.queryByText("origin content")).toBeNull();

    release();
    expect(await screen.findByText("destination")).toBeTruthy();
  });
});

/* ── the fallback still exists, and is still reachable ──────────────── */

describe("RouteFallback", () => {
  it("is rendered by the boundary on a cold entrance", async () => {
    const { Component, release } = pendingLazy("destination");
    let mounted = 0;

    function Counting() {
      useEffect(() => {
        mounted += 1;
      }, []);
      return <Component />;
    }

    render(
      <MemoryRouter initialEntries={["/app/modules/finance"]}>
        <Suspense fallback={<p>FALLBACK</p>}>
          <Routes>
            <Route path="/app/modules/finance" element={<Counting />} />
          </Routes>
        </Suspense>
      </MemoryRouter>,
    );

    // Nothing was on screen to preserve, so the fallback is the honest
    // answer and this slice does not remove it.
    expect(screen.getByText("FALLBACK")).toBeTruthy();

    release();
    expect(await screen.findByText("destination")).toBeTruthy();
    expect(mounted).toBe(1);
  });
});
