// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen } from "@testing-library/react";
import { MemoryRouter, Navigate, Route, Routes } from "react-router-dom";

import { I18nFixture } from "@/lib/i18n";
import { AuthProvider } from "@/lib/auth";
import { ThemeProvider } from "@/lib/theme";
import { SettingsLayout } from "./SettingsLayout";
import { ProfileSettingsPage } from "./Profile";
import { PreferencesSettingsPage } from "./Preferences";
import { ModulesSettingsPage } from "./Modules";
import { SecuritySettingsPage } from "./Security";

/**
 * The settings area's navigation.
 *
 * ── What was broken ────────────────────────────────────────────────────
 * Five pages under one URL prefix and nothing else in common. Each drew
 * its own `PageHeader` with the eyebrow "Configurações" and its own panel
 * header with the same title and the same description underneath it, so
 * every screen announced itself twice. None of them linked to any of the
 * others: landing on Profile, the only way to reach Integrations was to
 * already know the URL, or to know the word to type into ⌘K.
 *
 * ── Why Integrations is not mounted here ───────────────────────────────
 * Its cards drive live queries against GitHub and Meta Threads. This file
 * is about whether a person can navigate between sections, so the route
 * exists and the *link to it* is asserted; the card behaviour has its own
 * tests beside the components.
 */

function renderAt(path: string) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[path]}>
        <I18nFixture lang="pt">
          {/* Preferences renders the real ThemeToggle and LanguageSwitcher,
              which read their providers. */}
          <ThemeProvider>
            <AuthProvider>
              <Routes>
                <Route path="/app/settings" element={<SettingsLayout />}>
                  <Route index element={<Navigate to="profile" replace />} />
                  <Route path="profile" element={<ProfileSettingsPage />} />
                  <Route path="integrations" element={<div>integrations-stub</div>} />
                  <Route path="preferences" element={<PreferencesSettingsPage />} />
                  <Route path="modules" element={<ModulesSettingsPage />} />
                  <Route path="security" element={<SecuritySettingsPage />} />
                </Route>
              </Routes>
            </AuthProvider>
          </ThemeProvider>
        </I18nFixture>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

const SECTIONS: ReadonlyArray<readonly [string, string]> = [
  ["Perfil", "/app/settings/profile"],
  ["Integrações", "/app/settings/integrations"],
  ["Preferências", "/app/settings/preferences"],
  ["Módulos", "/app/settings/modules"],
  ["Segurança", "/app/settings/security"],
];

/**
 * `AuthProvider` reads its session during `useState` initialisation, so an
 * unseeded test renders the signed-out placeholder and Profile has no
 * identity to show.
 */
function seedSession() {
  window.localStorage.setItem(
    "corsi.session",
    JSON.stringify({
      id: "u_joao_corsi",
      email: "joao@corsi.dev",
      name: "João Corsi",
      roles: ["operator"],
      provider: "mock",
    }),
  );
}

/**
 * jsdom ships no `matchMedia`, and `ThemeProvider` asks it about
 * `prefers-color-scheme` during `useState` initialisation. The workspace's
 * shared `stubViewport` deliberately throws on queries it cannot parse, and
 * a colour-scheme query is one of them — so this file installs the one
 * answer it needs rather than teaching that helper about themes.
 */
function stubMatchMedia() {
  Object.defineProperty(window, "matchMedia", {
    writable: true,
    configurable: true,
    value: (query: string) => ({
      matches: false,
      media: query,
      onchange: null,
      addEventListener: () => {},
      removeEventListener: () => {},
      addListener: () => {},
      removeListener: () => {},
      dispatchEvent: () => false,
    }),
  });
}

beforeEach(() => {
  window.localStorage.clear();
  seedSession();
  stubMatchMedia();
});
afterEach(() => {
  cleanup();
  window.localStorage.clear();
});

describe("settings shell · persistent navigation", () => {
  it.each(SECTIONS.map(([, href]) => href))(
    "offers every other section from %s",
    (from) => {
      renderAt(from);
      for (const [label, href] of SECTIONS) {
        // No dead ends: from anywhere in the area, everywhere else is a
        // click away. This is the assertion the old pages could not pass.
        expect(screen.getByRole("link", { name: label }).getAttribute("href")).toBe(href);
      }
    },
  );

  it("names the navigation for assistive tech", () => {
    renderAt("/app/settings/profile");
    expect(screen.getByRole("navigation", { name: "Seções das configurações" })).toBeDefined();
  });

  it("marks the section the reader is in", () => {
    renderAt("/app/settings/modules");
    expect(screen.getByRole("link", { name: "Módulos" }).getAttribute("aria-current")).toBe("page");
    expect(screen.getByRole("link", { name: "Perfil" }).getAttribute("aria-current")).toBeNull();
  });

  it("labels every destination in words, not only a glyph", () => {
    renderAt("/app/settings/profile");
    const nav = screen.getByRole("navigation", { name: "Seções das configurações" });
    for (const [label] of SECTIONS) {
      // Scoped to the nav, and matched on painted text rather than on the
      // accessible name: the words have to be visible, not merely
      // announced. A row of five icons is a nav nobody can read at a
      // glance. (The section's own panel heading repeats some of these
      // words outside the nav, which is why this is scoped.)
      expect(
        [...nav.querySelectorAll("a")].some((a) => a.textContent?.trim() === label),
      ).toBe(true);
    }
  });

  it("draws exactly one page title for the whole area", () => {
    renderAt("/app/settings/profile");
    const headings = screen.getAllByRole("heading", { level: 1 });
    expect(headings).toHaveLength(1);
    expect(headings[0].textContent).toBe("Configurações");
  });

  it("stops repeating the section title twice on one screen", () => {
    renderAt("/app/settings/preferences");
    // Before: PageHeader said "Preferências / Aparência, idioma e flags
    // experimentais." and the panel below said it again, verbatim.
    expect(screen.getAllByText("Preferências")).toHaveLength(2); // nav item + panel heading
    expect(
      screen.getAllByText("Aparência, idioma e flags experimentais."),
    ).toHaveLength(1);
  });

  it("lands the area's index on the first section", () => {
    renderAt("/app/settings");
    expect(screen.getByRole("link", { name: "Perfil" }).getAttribute("aria-current")).toBe("page");
  });
});

describe("settings · Profile no longer invents facts", () => {
  it("shows identity as values, not as inputs that refuse every keystroke", () => {
    renderAt("/app/settings/profile");
    expect(screen.getByText("João Corsi")).toBeDefined();
    expect(screen.getByText("joao@corsi.dev")).toBeDefined();
    // Four readOnly <input>s used to sit here. An input a person cannot
    // type into is a promise the page does not keep.
    expect(document.querySelectorAll("input")).toHaveLength(0);
  });

  it("drops the timezone and language it was making up", () => {
    renderAt("/app/settings/profile");
    // The timezone was the literal string "America/Sao_Paulo" and the
    // language said "PT-BR" while this very render is in Portuguese only
    // because the fixture pinned it. Neither was read from anything.
    expect(screen.queryByText("America/Sao_Paulo")).toBeNull();
    expect(screen.queryByText("PT-BR")).toBeNull();
  });

  it("carries the identity the deleted /app/account page used to hold", () => {
    renderAt("/app/settings/profile");
    expect(screen.getByText("operator")).toBeDefined();
    expect(screen.getByText("Mock · sessão local")).toBeDefined();
    expect(screen.getByText("Autenticado")).toBeDefined();
  });
});

describe("settings · Modules reports instead of pretending to control", () => {
  it("offers no switch, because there is nothing behind one", () => {
    renderAt("/app/settings/modules");
    // Four `role="switch"` toggles lived here, backed by `useState` and
    // wired to nothing at all.
    expect(screen.queryAllByRole("switch")).toHaveLength(0);
  });

  it("reports the real navigation state of each module", () => {
    renderAt("/app/settings/modules");
    // Read straight from `HIDDEN_ROUTES`. All three modules that have a
    // screen now have an entry point: Finance was the last one out, and it
    // came back on 2026-08-26.
    //
    // `queryAllByText` for the empty case — `getAllByText` throws on zero
    // matches, so the assertion that nothing is out of the navigation has
    // to be written as a query rather than as a get.
    expect(screen.getAllByText("Na navegação")).toHaveLength(3);
    expect(screen.queryAllByText("Fora da navegação")).toHaveLength(0);
  });

  it("stops naming things that are not modules", () => {
    renderAt("/app/settings/modules");
    // Market Intelligence and Content Engine were never implemented and
    // are not in the architecture. Listing them as toggleable modules was
    // the page's largest untruth.
    expect(screen.queryByText("Market Intelligence")).toBeNull();
    expect(screen.queryByText("Content Engine")).toBeNull();
  });
});
