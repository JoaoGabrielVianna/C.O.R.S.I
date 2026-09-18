// @vitest-environment jsdom

import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Link, MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, expect, it } from "vitest";

import { PageTransition } from "./WorkspaceLayout";
import layoutSource from "./WorkspaceLayout.tsx?raw";

/**
 * A transition may communicate movement. It may not withhold information.
 *
 * ── What was measured ──────────────────────────────────────────────────
 * The entrance was `opacity: 0 → 1` over 180ms, re-run on every path
 * change. A warm navigation puts the destination on screen in 4 to 23ms —
 * so for the remaining ~160ms the page was fully rendered, laid out, and
 * deliberately invisible. The wait was ours.
 *
 * ── What this pins ─────────────────────────────────────────────────────
 * That opacity is never written. Not as a starting value, not as a
 * keyframe, not "briefly". A shorter fade would still be a fade, and the
 * only honest version of this animation is one that cannot hide anything
 * regardless of how long it takes.
 *
 * The companion guard in `WorkspaceLayout.navigation.test.tsx` stays
 * untouched: it owns the S6.1 property (one mount per navigation, stable
 * node), and this file must not be read as replacing it.
 */

afterEach(cleanup);

function tree() {
  return (
    <MemoryRouter initialEntries={["/a"]}>
      <PageTransition>
        <Routes>
          <Route
            path="/a"
            element={
              <>
                <p data-testid="page">page a</p>
                <Link to="/b">go</Link>
              </>
            }
          />
          <Route path="/b" element={<p data-testid="page">page b</p>} />
        </Routes>
      </PageTransition>
    </MemoryRouter>
  );
}

it("never renders the arriving page at reduced opacity", async () => {
  const user = userEvent.setup();
  render(tree());

  const wrapper = screen.getByTestId("page").parentElement!;
  expect(wrapper.style.opacity === "" || wrapper.style.opacity === "1").toBe(true);

  await user.click(screen.getByText("go"));
  await screen.findByText("page b");

  // The destination is on screen AND readable in the same frame it
  // arrived. `opacity: 0` here is the defect, whatever its duration.
  expect(wrapper.style.opacity === "" || wrapper.style.opacity === "1").toBe(true);
  expect(screen.getByText("page b")).toBeTruthy();
});

it("does not write opacity anywhere in the transition, and stays under 120ms", () => {
  const code = layoutSource
    .replace(/\/\*[\s\S]*?\*\//g, " ")
    .replace(/(^|[^:])\/\/.*$/gm, "$1");

  expect(code, "the entrance hides the page again").not.toMatch(/opacity:\s*0/);
  expect(code, "opacity is animated at all").not.toMatch(/opacity:\s*[01]\b/);

  // Movement only, and short: long enough to read as arrival, short enough
  // that nobody is waiting for it to finish.
  const duration = code.match(/duration:\s*([\d.]+)/);
  expect(duration, "the transition lost its duration").not.toBeNull();
  expect(Number(duration![1])).toBeGreaterThan(0);
  expect(Number(duration![1])).toBeLessThanOrEqual(0.12);
});

it("skips the movement when the device asks for reduced motion", () => {
  const code = layoutSource
    .replace(/\/\*[\s\S]*?\*\//g, " ")
    .replace(/(^|[^:])\/\/.*$/gm, "$1");

  // Nothing is hidden, so there is no fade to substitute: the setting is a
  // request for no motion, and the correct answer is no motion.
  expect(code).toContain("useReducedMotion");
  expect(code).toMatch(/if \(reducedMotion\) return;/);
});
