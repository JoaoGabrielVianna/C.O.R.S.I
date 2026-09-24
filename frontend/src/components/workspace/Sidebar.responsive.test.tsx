// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { act, cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";

import { I18nFixture } from "@/lib/i18n";
import { AuthFixture } from "@/lib/auth/AuthFixture";
import { WorkspaceProvider, useWorkspace } from "@/lib/workspace";
import { stubViewport, resizeViewport, VIEWPORTS } from "@/lib/workspace/testing";
import { Sidebar } from "./Sidebar";

/**
 * The sidebar's three presentation states, and the line between them.
 *
 * ── The bug this file exists to hold down ──────────────────────────────
 * `collapsed` is the desktop rail's preference, but the content was gated
 * on it directly, so a rail narrowed on a monitor also stripped the labels
 * out of a 260px drawer on a phone — a column of icons with no words. The
 * second half was the persistence: a first visit under 1024px derived
 * `collapsed = true` from the viewport and an effect wrote it straight to
 * localStorage, so the same desktop later came up collapsed for a choice
 * nobody made.
 *
 * The two tests that matter most are "mobile + desktop preference
 * collapsed" and "first visit on a phone": one proves the rail preference
 * stops at the breakpoint, the other proves it is never invented.
 *
 * ── What jsdom can and cannot prove here ───────────────────────────────
 * There is no layout engine, so nothing below asserts that a panel fits on
 * screen or that an element is painted. What it does assert is which
 * branch rendered: whether the label text reached the DOM, whether the
 * item still carries an accessible name without it, and where the footer
 * menu was placed. Geometry needs a real browser and is declared unproven.
 */

const COLLAPSED_KEY = "corsi.workspace.sidebar.collapsed";

/**
 * The signed-in shell arrives through `AuthFixture` rather than a seeded
 * storage key. The session is a server-issued cookie now, so there is no
 * longer a blob a test can write to forge one — and this file's subject is
 * the sidebar's layout, not how a login happens.
 */

/**
 * Opens the drawer through the workspace context rather than the header's
 * hamburger. The trigger lives in `Header`, which drags in the theme,
 * command and notification providers; none of them have anything to do
 * with what the drawer renders, and mounting them would make this file
 * fail for reasons unrelated to its subject.
 */
function DrawerTrigger() {
  const { openMobile } = useWorkspace();
  return (
    <button type="button" onClick={openMobile}>
      open-drawer
    </button>
  );
}

function renderShell() {
  return render(
    <MemoryRouter initialEntries={["/app/modules/agents"]}>
      <I18nFixture lang="pt">
        <AuthFixture>
          <WorkspaceProvider>
            <DrawerTrigger />
            <Sidebar />
          </WorkspaceProvider>
        </AuthFixture>
      </I18nFixture>
    </MemoryRouter>,
  );
}

/** Every module the navigation currently advertises. */
const VISIBLE_MODULES = ["Job Radar", "Agents"];

beforeEach(() => {
  window.localStorage.clear();
  stubViewport(VIEWPORTS.desktopWide);
});

afterEach(() => {
  cleanup();
  window.localStorage.clear();
});

// ── 1. Desktop expanded ─────────────────────────────────────────────────

describe("sidebar · desktop expanded (1440px)", () => {
  beforeEach(() => {
    stubViewport(VIEWPORTS.desktopWide);
    window.localStorage.setItem(COLLAPSED_KEY, "0");
  });

  it("renders every module as icon + label", () => {
    renderShell();
    for (const label of VISIBLE_MODULES) {
      expect(screen.getByText(label)).toBeDefined();
      expect(screen.getByRole("link", { name: label })).toBeDefined();
    }
  });

  it("keeps the section headings and the user block", () => {
    renderShell();
    expect(screen.getByText("Módulos")).toBeDefined();
    expect(screen.getByText("Visão geral")).toBeDefined();
    expect(screen.getByText("João Corsi")).toBeDefined();
  });
});

// ── 2. Desktop collapsed ────────────────────────────────────────────────

describe("sidebar · desktop collapsed (1440px)", () => {
  beforeEach(() => {
    stubViewport(VIEWPORTS.desktopWide);
    window.localStorage.setItem(COLLAPSED_KEY, "1");
  });

  it("drops the visible label text", () => {
    renderShell();
    // The rail is 64px here, so this is the intended state, not the bug.
    for (const label of VISIBLE_MODULES) {
      expect(screen.queryByText(label)).toBeNull();
    }
  });

  it("still names every item for assistive tech and for a tooltip", () => {
    renderShell();
    for (const label of VISIBLE_MODULES) {
      const link = screen.getByRole("link", { name: label });
      // `title` is the tooltip; `aria-label` is what actually names the
      // link rather than relying on the browser's `title` fallback.
      expect(link.getAttribute("title")).toBe(label);
      expect(link.getAttribute("aria-label")).toBe(label);
    }
  });

  it("keeps the items focusable and in the tab order", () => {
    renderShell();
    const link = screen.getByRole("link", { name: "Job Radar" });
    act(() => (link as HTMLElement).focus());
    expect(document.activeElement).toBe(link);
  });
});

// ── 3. Phone, no stored preference ──────────────────────────────────────

describe("sidebar · phone (390px), no stored preference", () => {
  beforeEach(() => stubViewport(VIEWPORTS.phone));

  it("shows the labels once the drawer is open", async () => {
    renderShell();
    await userEvent.click(screen.getByText("open-drawer"));
    for (const label of VISIBLE_MODULES) {
      expect(screen.getByText(label)).toBeDefined();
    }
  });

  it("does not record a rail preference nobody chose", async () => {
    renderShell();
    await userEvent.click(screen.getByText("open-drawer"));
    // The old provider derived `collapsed` from the viewport and an effect
    // persisted it on mount. Visiting on a phone must leave the desktop
    // preference untouched, which means unwritten.
    expect(window.localStorage.getItem(COLLAPSED_KEY)).toBeNull();
  });
});

// ── 4. Phone WITH a desktop collapsed preference ────────────────────────

describe("sidebar · phone (390px) with desktop preference collapsed", () => {
  beforeEach(() => {
    stubViewport(VIEWPORTS.phone);
    window.localStorage.setItem(COLLAPSED_KEY, "1");
  });

  it("shows the labels anyway", async () => {
    renderShell();
    await userEvent.click(screen.getByText("open-drawer"));
    // This is the reported bug. The drawer is ~260px and has room for
    // text; the 64px rail preference has no authority down here.
    for (const label of VISIBLE_MODULES) {
      expect(screen.getByText(label)).toBeDefined();
    }
  });

  it("keeps the headings and the user block legible", async () => {
    renderShell();
    await userEvent.click(screen.getByText("open-drawer"));
    expect(screen.getByText("Módulos")).toBeDefined();
    expect(screen.getByText("João Corsi")).toBeDefined();
  });

  it("does not fall back to the icon-only tooltip treatment", async () => {
    renderShell();
    await userEvent.click(screen.getByText("open-drawer"));
    const link = screen.getByRole("link", { name: "Job Radar" });
    // A tooltip here would be redundant with a label that is already on
    // screen, and an aria-label would be shadowing that visible text.
    expect(link.getAttribute("title")).toBeNull();
    expect(link.getAttribute("aria-label")).toBeNull();
  });

  it("leaves the stored desktop preference alone", async () => {
    renderShell();
    await userEvent.click(screen.getByText("open-drawer"));
    // Rendering the drawer expanded must not be mistaken for the user
    // un-collapsing their desktop rail.
    expect(window.localStorage.getItem(COLLAPSED_KEY)).toBe("1");
  });
});

// ── 5. Phone first, desktop later ───────────────────────────────────────

describe("sidebar · first visit on a phone, next visit on a desktop", () => {
  it("does not leave the desktop rail collapsed", async () => {
    stubViewport(VIEWPORTS.phone);
    renderShell();
    await userEvent.click(screen.getByText("open-drawer"));
    cleanup();

    // Same browser, same storage, bigger screen.
    stubViewport(VIEWPORTS.desktopWide);
    renderShell();

    for (const label of VISIBLE_MODULES) {
      expect(screen.getByText(label)).toBeDefined();
    }
    expect(window.localStorage.getItem(COLLAPSED_KEY)).toBeNull();
  });
});

// ── 6. Footer ───────────────────────────────────────────────────────────

describe("sidebar · footer menu placement", () => {
  it("opens the menu above the button inside the drawer", async () => {
    stubViewport(VIEWPORTS.phone);
    window.localStorage.setItem(COLLAPSED_KEY, "1");
    renderShell();
    await userEvent.click(screen.getByText("open-drawer"));

    await userEvent.click(screen.getByRole("button", { expanded: false }));
    const menu = screen.getByRole("menu");

    // `left-full` pushes the panel past the drawer's right edge, which on a
    // 390px screen puts it off the viewport entirely. jsdom cannot measure
    // that, so this asserts the placement branch instead of the geometry.
    expect(menu.className).not.toContain("left-full");
    expect(menu.className).toContain("bottom-[calc(100%+6px)]");
  });

  it("keeps the settings entries reachable from the drawer", async () => {
    stubViewport(VIEWPORTS.phone);
    window.localStorage.setItem(COLLAPSED_KEY, "1");
    renderShell();
    await userEvent.click(screen.getByText("open-drawer"));

    await userEvent.click(screen.getByRole("button", { expanded: false }));
    expect(screen.getByRole("menuitem", { name: "Perfil" })).toBeDefined();
  });

  it("still opens sideways for a collapsed desktop rail", async () => {
    stubViewport(VIEWPORTS.desktopWide);
    window.localStorage.setItem(COLLAPSED_KEY, "1");
    renderShell();

    await userEvent.click(screen.getByRole("button", { expanded: false }));
    const menu = screen.getByRole("menu");
    // 64px of rail has no room above the button, so sideways is correct.
    expect(menu.className).toContain("left-full");
  });
});

// ── Breakpoint crossing ─────────────────────────────────────────────────

describe("sidebar · crossing the breakpoint", () => {
  it("restores the collapsed rail when a phone-sized window grows", async () => {
    stubViewport(VIEWPORTS.phone);
    window.localStorage.setItem(COLLAPSED_KEY, "1");
    renderShell();
    await userEvent.click(screen.getByText("open-drawer"));
    expect(screen.getByText("Job Radar")).toBeDefined();

    // The preference was never lost, only ignored while it did not apply.
    act(() => resizeViewport(VIEWPORTS.desktopWide));
    expect(screen.queryByText("Job Radar")).toBeNull();
    expect(screen.getByRole("link", { name: "Job Radar" })).toBeDefined();
  });

  it("shows labels at the tablet width, where there is no rail", () => {
    stubViewport(VIEWPORTS.tablet);
    window.localStorage.setItem(COLLAPSED_KEY, "1");
    renderShell();
    // 768px is below `lg`, so the sidebar is a drawer and the rail
    // preference does not reach it.
    for (const label of VISIBLE_MODULES) {
      expect(screen.getByText(label)).toBeDefined();
    }
  });

  it("treats exactly 1024px as desktop", () => {
    stubViewport(VIEWPORTS.desktop);
    window.localStorage.setItem(COLLAPSED_KEY, "1");
    renderShell();
    // `(min-width: 1024px)` matches, so the collapsed rail applies.
    expect(screen.queryByText("Job Radar")).toBeNull();
    expect(screen.getByRole("link", { name: "Job Radar" })).toBeDefined();
  });
});

// ── Explicit toggle still persists ──────────────────────────────────────

describe("sidebar · the collapse toggle", () => {
  it("persists the preference when the user actually chooses it", async () => {
    stubViewport(VIEWPORTS.desktopWide);
    renderShell();
    expect(window.localStorage.getItem(COLLAPSED_KEY)).toBeNull();

    await userEvent.click(screen.getByRole("button", { name: "Recolher menu lateral" }));

    expect(window.localStorage.getItem(COLLAPSED_KEY)).toBe("1");
    expect(screen.queryByText("Job Radar")).toBeNull();
  });

  it("expands again from the collapsed rail", async () => {
    stubViewport(VIEWPORTS.desktopWide);
    window.localStorage.setItem(COLLAPSED_KEY, "1");
    renderShell();

    await userEvent.click(screen.getByRole("button", { name: "Expandir menu lateral" }));

    expect(window.localStorage.getItem(COLLAPSED_KEY)).toBe("0");
    expect(screen.getByText("Job Radar")).toBeDefined();
  });
});
