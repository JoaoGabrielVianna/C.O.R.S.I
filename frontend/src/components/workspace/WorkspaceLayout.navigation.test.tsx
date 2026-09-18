// @vitest-environment jsdom

import { useEffect } from "react";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Link, MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, expect, it } from "vitest";

import { PageTransition } from "./WorkspaceLayout";
import layoutSource from "./WorkspaceLayout.tsx?raw";

/**
 * A route subtree must mount ONCE per navigation.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   THIS IS AN ACCESSIBILITY TEST WEARING A ROUTING TEST'S CLOTHES
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * ── The defect ─────────────────────────────────────────────────────────
 * The shell used to wrap `<Outlet />` in `<AnimatePresence mode="wait">`
 * with a `motion.div` keyed by `location.pathname`. AnimatePresence keeps
 * the OUTGOING wrapper mounted while it animates away, and that wrapper's
 * children are re-evaluated on every render — so `<Outlet />` inside it
 * was already rendering the INCOMING route. The page mounted under the
 * outgoing key, then again ~180ms later under the incoming one.
 *
 * The second mount throws away everything the first had: state, measured
 * sizes, an open panel, the focused element. Anything a person did inside
 * that window was simply lost. In the Palace that read as "Enter on an
 * object does nothing", while the mouse — which always arrived after the
 * window closed — worked perfectly.
 *
 * ── Why both a behavioural and a structural check ──────────────────────
 * The behavioural one proves the property. The structural one names the
 * exact mechanism, because the property can be broken again in a way that
 * a mount counter in jsdom might not schedule the same way a browser does
 * — which is precisely how this shipped in the first place.
 */

afterEach(cleanup);

let mounts: string[] = [];

function Probe({ name }: { name: string }) {
  useEffect(() => {
    mounts.push(name);
  }, [name]);
  return <p>{name}</p>;
}

it("mounts a route subtree once when navigating to it", async () => {
  mounts = [];
  const user = userEvent.setup();

  render(
    <MemoryRouter initialEntries={["/a"]}>
      <PageTransition>
        <Routes>
          <Route
            path="/a"
            element={
              <>
                <Probe name="a" />
                <Link to="/b">go</Link>
              </>
            }
          />
          <Route path="/b" element={<Probe name="b" />} />
        </Routes>
      </PageTransition>
    </MemoryRouter>,
  );

  expect(mounts).toEqual(["a"]);
  await user.click(screen.getByText("go"));
  expect(await screen.findByText("b")).toBeTruthy();

  // One mount for the page that was left, one for the page arrived at.
  // Two entries for "b" would be the defect: the second one discards the
  // first's state, and with it anybody's keystroke and focus.
  expect(mounts).toEqual(["a", "b"]);
});

it("keeps the same DOM node across a navigation, so focus survives", async () => {
  const user = userEvent.setup();

  render(
    <MemoryRouter initialEntries={["/a"]}>
      <PageTransition>
        <Routes>
          <Route
            path="/a"
            element={
              <>
                <p data-testid="page">a</p>
                <Link to="/b">go</Link>
              </>
            }
          />
          <Route path="/b" element={<p data-testid="page">b</p>} />
        </Routes>
      </PageTransition>
    </MemoryRouter>,
  );

  const wrapper = screen.getByTestId("page").parentElement;
  await user.click(screen.getByText("go"));
  await screen.findByText("b");
  // The transition wrapper is one stable element. A keyed one is replaced,
  // and anything focused inside it falls back to <body>.
  expect(screen.getByTestId("page").parentElement).toBe(wrapper);
});

it("does not key the route wrapper, and does not wrap the Outlet in AnimatePresence", () => {
  // The mechanism, named. `key` IS a remount — that is what a key means —
  // and AnimatePresence exists to keep an outgoing subtree alive, which is
  // the stale tree that caused the defect above.
  const code = layoutSource
    .replace(/\/\*[\s\S]*?\*\//g, " ")
    .replace(/(^|[^:])\/\/.*$/gm, "$1");

  expect(code, "AnimatePresence is back around the route content").not.toContain(
    "AnimatePresence",
  );
  expect(code, "the route wrapper is keyed by the path again").not.toMatch(
    /key=\{\s*location\.pathname/,
  );
  expect(code, "an exit animation means keeping a dead page mounted").not.toContain("exit=");
});
