// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";

import { I18nFixture } from "@/lib/i18n";
import { AuthProvider } from "@/lib/auth";
import { WorkspaceProvider } from "@/lib/workspace";
import { stubViewport, VIEWPORTS } from "@/lib/workspace/testing";
import { HIDDEN_ROUTES, isRouteHidden } from "@/lib/navVisibility";
import { Sidebar } from "./Sidebar";

/**
 * The workspace navigation's entry points, and which modules have one.
 *
 * ── What was actually broken, both times ───────────────────────────────
 * Nothing about either module. The routes have always been mounted in
 * `App.tsx`, the pages have always rendered, and the sidebar has always
 * carried the items with their labels and icons. `/app/modules/job-radar`
 * and later `/app/modules/finance` sat in `HIDDEN_ROUTES`, added while the
 * platform was being recorded for content, and the filter in `Sections`
 * dropped the rows before they reached the DOM. Typing the URL worked the
 * whole time, which is exactly what made it read as "the module
 * disappeared" rather than "the module is off".
 *
 * ── Why this asserts on the rendered link and not on the constant ──────
 * `expect(isRouteHidden(...)).toBe(false)` would pass against a sidebar
 * that had stopped reading the list at all. What the operator needs is a
 * row they can click, so the test renders the real component and asks for
 * a link by its accessible name, then checks where it points. The one
 * direct assertion on the constant below covers the *other* consumer,
 * ⌘K, which shares the same gate and would otherwise be untested here.
 *
 * ── Why the viewport and the collapse state are pinned ─────────────────
 * jsdom ships no `matchMedia` and performs no layout, so the sidebar has
 * no viewport to read unless a test gives it one. Pinning 1440px with the
 * rail expanded keeps this file's subject to visibility, and leaves the
 * responsive states to `Sidebar.responsive.test.tsx`.
 */

const COLLAPSED_KEY = "corsi.workspace.sidebar.collapsed";

function renderSidebar() {
  return render(
    <MemoryRouter initialEntries={["/app/modules/agents"]}>
      <I18nFixture lang="pt">
        <AuthProvider>
          <WorkspaceProvider>
            <Sidebar />
          </WorkspaceProvider>
        </AuthProvider>
      </I18nFixture>
    </MemoryRouter>,
  );
}

beforeEach(() => {
  window.localStorage.clear();
  window.localStorage.setItem(COLLAPSED_KEY, "0");
  // lg+ with the rail expanded: the state in which a label is supposed to
  // be on screen. `stubViewport` is shared with Sidebar.responsive.test.tsx.
  stubViewport(VIEWPORTS.desktopWide);
});

afterEach(() => {
  cleanup();
  window.localStorage.clear();
});

/**
 * The modules that have a screen AND an entry point, each with the URL it
 * has always answered on.
 *
 * A table rather than three copies of the same test: the next module to be
 * unhidden is a row, and a row cannot forget to assert the route or the
 * palette the way a hand-copied block can.
 */
const ADVERTISED = [
  { name: "Job Radar", route: "/app/modules/job-radar" },
  { name: "Finance",   route: "/app/modules/finance" },
  { name: "Agents",    route: "/app/modules/agents" },
] as const;

describe("sidebar · which modules have an entry point", () => {
  it.each(ADVERTISED)("offers $name as a navigation item", ({ name }) => {
    renderSidebar();
    expect(screen.getByRole("link", { name })).toBeDefined();
  });

  it.each(ADVERTISED)("points $name at its canonical route", ({ name, route }) => {
    renderSidebar();
    // The URL the module has always answered on. Changing it here without
    // changing `App.tsx` would produce a visible item that leads nowhere.
    expect(screen.getByRole("link", { name }).getAttribute("href")).toBe(route);
  });

  it.each(ADVERTISED)("keeps $name out of the hidden list, so ⌘K offers it too", ({ route }) => {
    // `RegisterWorkspaceCommands` filters the palette through the same
    // predicate. Asserting it directly is what keeps the two surfaces from
    // drifting apart without a test noticing.
    expect(isRouteHidden(route)).toBe(false);
  });

  it("groups them together rather than inventing a section", () => {
    renderSidebar();
    // Same <ul> for all three: each item is in the navigation because the
    // existing registry stopped filtering it, not because a hardcoded
    // exception was bolted onto the shell. A `module === "finance"` branch
    // would pass every assertion above and fail this one.
    const lists = ADVERTISED.map(({ name }) =>
      screen.getByRole("link", { name }).closest("ul"),
    );
    expect(new Set(lists).size).toBe(1);
  });

  it("advertises nothing that is not ready", () => {
    // The batch that brought Finance back must not have quietly advertised
    // the half-built modules alongside it. Market Intelligence and Content
    // Engine were never implemented; the dashboard is still off.
    expect([...HIDDEN_ROUTES].sort()).toEqual([
      "/app/dashboard",
      "/app/modules/content",
      "/app/modules/news",
    ]);

    renderSidebar();
    for (const name of ["Market Intelligence", "Content Engine", "Dashboard"]) {
      expect(screen.queryByRole("link", { name })).toBeNull();
    }
  });
});

describe("sidebar · desktop labels", () => {
  it("shows the label next to the icon on an expanded rail", () => {
    renderSidebar();
    // Every module the navigation advertises is identifiable by name, not
    // by glyph alone.
    for (const { name } of ADVERTISED) {
      expect(screen.getByRole("link", { name })).toBeDefined();
    }
  });
});
