// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";

import { I18nFixture } from "@/lib/i18n";
import { AuthProvider } from "@/lib/auth";
import { WorkspaceProvider, useWorkspace } from "@/lib/workspace";
import { stubViewport, VIEWPORTS } from "@/lib/workspace/testing";
import { Sidebar } from "./Sidebar";
import { UserMenu } from "./UserMenu";

/**
 * The account menu, and the two places that draw it.
 *
 * ── What was actually broken ───────────────────────────────────────────
 * The product had two account menus that disagreed. The header's avatar
 * offered "Conta" (→ /app/account), "Configurações" (→ settings/profile)
 * and Sign out. The sidebar's user block offered "Perfil" (→ the SAME
 * settings/profile) and "Configurações" (→ settings/preferences), and no
 * way to sign out at all. So the word "Configurações" named two different
 * pages depending on which corner you clicked, and "Perfil" and
 * "Configurações" named one page depending on the same thing.
 *
 * Neither menu mentioned Integrations, which is the reason this batch
 * exists: a working connector page with no entry point anywhere in the
 * product is not a page a person can find.
 *
 * ── Why these tests query both surfaces with the same list ─────────────
 * Because the fix is that there is only one list. Asserting the same
 * expectations against `UserMenu` and against `Sidebar` is what makes a
 * future edit to one of them fail rather than silently re-open the drift.
 */

const SESSION_KEY = "corsi.session";
const COLLAPSED_KEY = "corsi.workspace.sidebar.collapsed";

/** Every destination the menu is required to offer, and where it goes. */
const EXPECTED_ITEMS: ReadonlyArray<readonly [string, string]> = [
  ["Perfil", "/app/settings/profile"],
  ["Integrações", "/app/settings/integrations"],
  ["Preferências", "/app/settings/preferences"],
];

function seedSession() {
  window.localStorage.setItem(
    SESSION_KEY,
    JSON.stringify({
      id: "u_joao_corsi",
      email: "joao@corsi.dev",
      name: "João Corsi",
      roles: ["operator"],
      provider: "mock",
    }),
  );
}

function DrawerTrigger() {
  const { openMobile } = useWorkspace();
  return (
    <button type="button" onClick={openMobile}>
      open-drawer
    </button>
  );
}

function renderHeaderMenu() {
  return render(
    <MemoryRouter initialEntries={["/app/modules/agents"]}>
      <I18nFixture lang="pt">
        <AuthProvider>
          <WorkspaceProvider>
            <UserMenu />
          </WorkspaceProvider>
        </AuthProvider>
      </I18nFixture>
    </MemoryRouter>,
  );
}

function renderSidebar() {
  return render(
    <MemoryRouter initialEntries={["/app/modules/agents"]}>
      <I18nFixture lang="pt">
        <AuthProvider>
          <WorkspaceProvider>
            <DrawerTrigger />
            <Sidebar />
          </WorkspaceProvider>
        </AuthProvider>
      </I18nFixture>
    </MemoryRouter>,
  );
}

beforeEach(() => {
  window.localStorage.clear();
  seedSession();
  stubViewport(VIEWPORTS.desktopWide);
  window.localStorage.setItem(COLLAPSED_KEY, "0");
});

afterEach(() => {
  cleanup();
  window.localStorage.clear();
});

describe("account menu · the header avatar", () => {
  it("names its trigger, rather than leaving initials to do it", async () => {
    renderHeaderMenu();
    // "JC" is not a name. Assistive tech gets the word "Conta".
    expect(screen.getByRole("button", { name: "Conta" })).toBeDefined();
  });

  it("offers every account destination, Integrations included", async () => {
    renderHeaderMenu();
    await userEvent.click(screen.getByRole("button", { name: "Conta" }));

    for (const [label, href] of EXPECTED_ITEMS) {
      const item = screen.getByRole("menuitem", { name: label });
      expect(item.getAttribute("href")).toBe(href);
    }
  });

  it("offers sign out", async () => {
    renderHeaderMenu();
    await userEvent.click(screen.getByRole("button", { name: "Conta" }));
    expect(screen.getByRole("menuitem", { name: "Sair" })).toBeDefined();
  });

  it("has no two items pointing at the same page", async () => {
    renderHeaderMenu();
    await userEvent.click(screen.getByRole("button", { name: "Conta" }));

    const hrefs = screen
      .getAllByRole("menuitem")
      .map((el) => el.getAttribute("href"))
      .filter((h): h is string => h !== null);
    // The old pair had "Perfil" and "Configurações" both landing on
    // settings/profile, which is what made the words meaningless.
    expect(new Set(hrefs).size).toBe(hrefs.length);
  });
});

describe("account menu · the sidebar user block", () => {
  it("offers exactly what the header offers", async () => {
    renderSidebar();
    await userEvent.click(screen.getByRole("button", { name: "Conta" }));

    for (const [label, href] of EXPECTED_ITEMS) {
      expect(screen.getByRole("menuitem", { name: label }).getAttribute("href")).toBe(href);
    }
  });

  it("can sign out, which it previously could not", async () => {
    renderSidebar();
    await userEvent.click(screen.getByRole("button", { name: "Conta" }));
    expect(screen.getByRole("menuitem", { name: "Sair" })).toBeDefined();
  });

  it("reaches Integrations from the phone drawer too", async () => {
    cleanup();
    stubViewport(VIEWPORTS.phone);
    window.localStorage.setItem(COLLAPSED_KEY, "1");
    renderSidebar();

    await userEvent.click(screen.getByText("open-drawer"));
    await userEvent.click(screen.getByRole("button", { name: "Conta" }));

    // The collapsed *desktop* rail preference must not reach a 390px
    // drawer — the class of bug this codebase has already shipped once.
    expect(
      screen.getByRole("menuitem", { name: "Integrações" }).getAttribute("href"),
    ).toBe("/app/settings/integrations");
  });
});

describe("account menu · what the sidebar nav must NOT become", () => {
  it("keeps Integrations out of the module rail", () => {
    renderSidebar();
    // Integrations is configuration, not a module. It belongs to the
    // account menu; putting it beside Job Radar would claim otherwise.
    const railLinks = document.querySelectorAll("aside nav a");
    const hrefs = [...railLinks].map((a) => a.getAttribute("href"));
    expect(hrefs).not.toContain("/app/settings/integrations");
  });
});
