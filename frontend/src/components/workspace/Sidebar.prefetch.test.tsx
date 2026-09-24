// @vitest-environment jsdom

import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";

import { I18nFixture } from "@/lib/i18n";
import { AuthFixture } from "@/lib/auth/AuthFixture";
import { WorkspaceProvider } from "@/lib/workspace";
import { stubViewport, VIEWPORTS } from "@/lib/workspace/testing";

import { Sidebar } from "./Sidebar";

/**
 * Pointing at a destination warms its code. Nothing else.
 *
 * ── The cost being paid down ───────────────────────────────────────────
 * A click used to start the chunk download; the page then mounted and only
 * then asked for data. Measured at 120ms RTT on `agents → palace`: chunk at
 * 3ms taking 133ms, API at 141ms taking 129ms. Two round trips, in series,
 * both after the click.
 *
 * ── Why focus and not only hover ───────────────────────────────────────
 * Because the keyboard expresses the same intent. A preload wired to the
 * pointer alone would hand the speed-up to one input device and quietly
 * leave the other one slower, which is the kind of accessibility gap that
 * never shows up in a demo.
 *
 * ── What must NOT happen ───────────────────────────────────────────────
 * No navigation, no URL change, no request. `lazyRoutes.test.ts` owns the
 * request side; this file owns the wiring and the "hover is not a click"
 * part.
 */

const preloadRoute = vi.fn();

vi.mock("@/lib/lazyRoutes", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/lazyRoutes")>()),
  preloadRoute: (path: string) => preloadRoute(path),
}));

const COLLAPSED_KEY = "corsi.workspace.sidebar.collapsed";

function LocationProbe() {
  const location = useLocation();
  return <output data-testid="path">{location.pathname}</output>;
}

function renderSidebar() {
  return render(
    <MemoryRouter initialEntries={["/app/modules/agents"]}>
      <I18nFixture lang="pt">
        <AuthFixture>
          <WorkspaceProvider>
            <Sidebar />
            <LocationProbe />
            <Routes>
              <Route path="*" element={null} />
            </Routes>
          </WorkspaceProvider>
        </AuthFixture>
      </I18nFixture>
    </MemoryRouter>,
  );
}

beforeEach(() => {
  preloadRoute.mockClear();
  window.localStorage.clear();
  window.localStorage.setItem(COLLAPSED_KEY, "0");
  stubViewport(VIEWPORTS.desktopWide);
  vi.stubGlobal("fetch", vi.fn());
});

afterEach(() => {
  cleanup();
  window.localStorage.clear();
  vi.unstubAllGlobals();
});

const palaceLink = () => screen.getByRole("link", { name: "Palace" });

it("warms the chunk when a pointer enters a destination", async () => {
  const user = userEvent.setup();
  renderSidebar();

  await user.hover(palaceLink());

  expect(preloadRoute).toHaveBeenCalledWith("/app/modules/palace");
});

it("warms the same chunk when the keyboard reaches it", async () => {
  renderSidebar();

  palaceLink().focus();

  expect(preloadRoute).toHaveBeenCalledWith("/app/modules/palace");
});

it("asks again on every hover, and lets the registry decide", async () => {
  const user = userEvent.setup();
  renderSidebar();

  await user.hover(palaceLink());
  await user.unhover(palaceLink());
  await user.hover(palaceLink());
  palaceLink().focus();

  // The component does not keep its own idea of what has been loaded:
  // deciding that is `preloadRoute`'s job, in one place, for every caller.
  expect(preloadRoute.mock.calls.every(([p]) => p === "/app/modules/palace")).toBe(true);
  expect(preloadRoute.mock.calls.length).toBeGreaterThan(1);
});

it("does not navigate, and does not touch the URL", async () => {
  const user = userEvent.setup();
  renderSidebar();

  await user.hover(palaceLink());
  palaceLink().focus();

  expect(screen.getByTestId("path").textContent).toBe("/app/modules/agents");
});

it("makes no request on hover or focus", async () => {
  const user = userEvent.setup();
  renderSidebar();

  await user.hover(palaceLink());
  palaceLink().focus();

  expect(fetch).not.toHaveBeenCalled();
});

it("still navigates on click", async () => {
  const user = userEvent.setup();
  renderSidebar();

  await user.click(palaceLink());

  expect(screen.getByTestId("path").textContent).toBe("/app/modules/palace");
});

it("covers every destination the rail offers", async () => {
  const user = userEvent.setup();
  renderSidebar();

  for (const link of screen.getAllByRole("link")) {
    const href = link.getAttribute("href");
    if (!href?.startsWith("/app")) continue;
    preloadRoute.mockClear();
    await user.hover(link);
    expect(preloadRoute, `no preload wired for ${href}`).toHaveBeenCalledWith(href);
  }
});
