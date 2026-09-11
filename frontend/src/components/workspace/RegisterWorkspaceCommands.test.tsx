// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { cleanup, render } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";

import { I18nFixture } from "@/lib/i18n";
import { AuthProvider } from "@/lib/auth";
import { ThemeProvider } from "@/lib/theme";
import { CommandProvider, useCommand, type Command } from "@/lib/command";
import { stubViewport, VIEWPORTS } from "@/lib/workspace/testing";
import { HIDDEN_ROUTES } from "@/lib/navVisibility";
import { RegisterWorkspaceCommands } from "./RegisterWorkspaceCommands";

/**
 * ⌘K, as the other half of the navigation's visibility boundary.
 *
 * ── Why this file exists at all ────────────────────────────────────────
 * The sidebar suite next door proves what the RAIL advertises, and for the
 * palette it could only assert the predicate: `isRouteHidden(route) ===
 * false`. That is a true statement about a constant and says nothing about
 * whether ⌘K reads it — a palette that had stopped filtering entirely, or
 * one that had never registered Finance in the first place, passes it.
 *
 * So this renders the real registration component into a real command
 * provider and reads back the commands it actually pushed. The two
 * surfaces are supposed to answer the same question, and now each one is
 * asked directly.
 *
 * ── Why it reads commands out through a rendering probe ────────────────
 * `RegisterWorkspaceCommands` renders null and reports through context, so
 * there is nothing of its own to query. The probe below renders the
 * registry into the DOM and the assertions read it back from there.
 *
 * Writing the commands into a module-scope variable from the probe would
 * have been shorter and is a lint error, correctly: assigning to an outer
 * binding during render is a side effect, and a test that performs one is
 * testing a render it also perturbed. Rendering the registry is both legal
 * and closer to what the palette itself does with it.
 */

/** The registry, rendered so the DOM can be asked about it. */
function Probe() {
  const { commands } = useCommand();
  return (
    <ul data-testid="registry">
      {commands.map((c) => (
        <li
          key={c.id}
          data-id={c.id}
          data-category={c.category}
          data-route={(c as Command & { route?: string }).route ?? ""}
        >
          {c.title}
        </li>
      ))}
    </ul>
  );
}

/** One registry row, read back from the DOM. */
type Entry = { id: string; category: string; route: string; title: string };

function registry(): Entry[] {
  return Array.from(document.querySelectorAll("[data-testid='registry'] li")).map((el) => ({
    id: el.getAttribute("data-id") ?? "",
    category: el.getAttribute("data-category") ?? "",
    route: el.getAttribute("data-route") ?? "",
    title: el.textContent ?? "",
  }));
}

function renderCommands() {
  return render(
    <MemoryRouter initialEntries={["/app/modules/agents"]}>
      <I18nFixture lang="pt">
        <ThemeProvider>
          <AuthProvider>
            <CommandProvider>
              <RegisterWorkspaceCommands />
              <Probe />
            </CommandProvider>
          </AuthProvider>
        </ThemeProvider>
      </I18nFixture>
    </MemoryRouter>,
  );
}

beforeEach(() => {
  // jsdom ships no matchMedia, so start from the shared viewport stub the
  // sidebar suites use.
  stubViewport(VIEWPORTS.desktopWide);

  // ── Why this file adds one query on top of it ───────────────────────
  // `stubViewport` answers min-width/max-width and THROWS on anything
  // else, on purpose: a media query it cannot parse would otherwise be
  // silently answered `false`, which is how a responsive test comes to
  // prove nothing. That rule is right and is left alone.
  //
  // ThemeProvider asks `(prefers-color-scheme: dark)`, which is not a
  // viewport question at all. So it is answered here, in the one file that
  // needs it, rather than by teaching the shared helper about colour —
  // which would widen exactly the surface its comment argues for keeping
  // narrow. The value is irrelevant to every assertion below; what matters
  // is that the provider mounts.
  const viewportMatchMedia = window.matchMedia;
  window.matchMedia = ((query: string) =>
    query.includes("prefers-color-scheme")
      ? {
          matches: false,
          media: query,
          onchange: null,
          addEventListener: () => {},
          removeEventListener: () => {},
          addListener: () => {},
          removeListener: () => {},
          dispatchEvent: () => false,
        }
      : viewportMatchMedia(query)) as typeof window.matchMedia;
});

afterEach(() => {
  cleanup();
});

/** The modules that have a screen and an entry point, with their routes. */
const ADVERTISED = [
  { id: "mod.jobRadar", route: "/app/modules/job-radar" },
  { id: "mod.finance",  route: "/app/modules/finance" },
  { id: "mod.agents",   route: "/app/modules/agents" },
] as const;

describe("command palette · which modules it offers", () => {
  it.each(ADVERTISED)("registers $id", ({ id }) => {
    renderCommands();
    expect(registry().find((c) => c.id === id)).toBeDefined();
  });

  it.each(ADVERTISED)("points $id at its canonical route", ({ id, route }) => {
    renderCommands();
    expect(registry().find((c) => c.id === id)?.route).toBe(route);
  });

  it.each(ADVERTISED)("gives $id a title a person can search for", ({ id }) => {
    renderCommands();
    // A command with no title is a row the palette renders blank — findable
    // only by someone who already knows it is there.
    expect(registry().find((c) => c.id === id)?.title.trim()).toBeTruthy();
  });

  it("files them under Modules rather than inventing a category", () => {
    renderCommands();
    for (const { id } of ADVERTISED) {
      expect(registry().find((c) => c.id === id)?.category).toBe("Modules");
    }
  });

  it("offers no command for a route the navigation hides", () => {
    renderCommands();
    // The palette filters through the same predicate the rail does. This is
    // the assertion that would fail if someone unhid a module in one
    // surface and forgot the other — in either direction.
    const routes = new Set(registry().map((c) => c.route).filter(Boolean));
    for (const hidden of HIDDEN_ROUTES) {
      expect(routes.has(hidden)).toBe(false);
    }
  });

  it("does not offer Finance twice", () => {
    renderCommands();
    // A second navigation entry bolted on beside the registry one would
    // show up here as a duplicate, and in the rail as two rows.
    expect(registry().filter((c) => c.id === "mod.finance")).toHaveLength(1);
  });
});
