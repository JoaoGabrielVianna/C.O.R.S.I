// @vitest-environment jsdom

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Outlet, Route, Routes } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import { AgentToolsPage } from "./AgentTools";
import { I18nFixture } from "@/lib/i18n";

/**
 * The Agent Tools page, against the catalogue the backend actually returns.
 *
 * ── What this file exists to prevent ───────────────────────────────────
 * The bug it was written from was a stale backend binary: the process
 * serving `/chat/agents/{id}/tools` predated the GitHub integration, so it
 * answered with one tool and the page truthfully drew one tool. The page
 * was never wrong — but nothing in the suite pinned what it draws when the
 * seven ARE there, which is why the report and the screen could disagree
 * for hours without a test noticing.
 *
 * So the fixture below is the real response shape, copied from a live
 * `curl` against the current build.
 */

/** Exactly what `GET /chat/agents/{id}/tools` returns on this build. */
const CATALOGUE = {
  authorized_count: 0,
  items: [
    ["github.code.search", "GitHub · Buscar código"],
    ["github.commit.get", "GitHub · Ler commit"],
    ["github.commit.list", "GitHub · Commits"],
    ["github.file.get", "GitHub · Ler arquivo"],
    ["github.pull_request.get", "GitHub · Ler pull request"],
    ["github.pull_request.list", "GitHub · Pull requests"],
    ["github.repository.list", "GitHub · Repositórios"],
  ].map(([name, title]) => ({
    name,
    title,
    description: "…",
    effect: "read" as const,
    internal: false,
    schema: { properties: { x: { type: "string" as const } } },
    authorized: false,
  })),
};

const AGENT = { id: "a1", name: "Scout" };

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

/** Routes a stubbed fetch by path and records the writes. */
function stubFetch(routes: Record<string, (init?: RequestInit) => Response>) {
  const calls: { url: string; method: string; body?: string }[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = typeof input === "string" ? input : input.toString();
      calls.push({
        url,
        method: init?.method ?? "GET",
        body: init?.body ? String(init.body) : undefined,
      });
      for (const [needle, respond] of Object.entries(routes)) {
        if (url.includes(needle)) return respond(init);
      }
      return new Response("not found", { status: 404 });
    }),
  );
  return calls;
}

function renderPage() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <I18nFixture lang="pt">
        <MemoryRouter initialEntries={["/tools"]}>
          <Routes>
            <Route element={<Outlet context={{ agent: AGENT }} />}>
              <Route path="/tools" element={<AgentToolsPage />} />
            </Route>
          </Routes>
        </MemoryRouter>
      </I18nFixture>
    </QueryClientProvider>,
  );
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

const disconnected = { connected: false, repositories: [], organizations: [] };
const connected = {
  connected: true,
  connection: { account_login: "joaocorsi", token_hint: "...oken" },
  repositories: [{ full_name: "joaocorsi/c.o.r.s.i" }],
  organizations: [],
};

describe("AgentToolsPage", () => {
  it("shows every GitHub capability the backend returned, under one heading", async () => {
    stubFetch({
      "/chat/agents/a1/tools": () => jsonResponse(CATALOGUE),
      "/integrations/github/connection": () => jsonResponse(disconnected),
    });
    renderPage();

    // The heading is derived from the backend's own title, not written here.
    expect(await screen.findByRole("heading", { name: "GitHub" })).toBeTruthy();

    // All seven, by the capability label with the provider stripped.
    for (const label of [
      "Repositórios",
      "Commits",
      "Ler commit",
      "Buscar código",
      "Ler arquivo",
      "Pull requests",
      "Ler pull request",
    ]) {
      expect(screen.getByText(label)).toBeTruthy();
    }

    // And the canonical names are still on the page, because that is what a
    // person needs when the audit trail names one.
    expect(screen.getByText("github.code.search")).toBeTruthy();

    // The counter reflects the group, not the whole catalogue.
    expect(screen.getByText("0/7")).toBeTruthy();
  });

  it("says the integration is not connected without hiding anything", async () => {
    // The honest behaviour: the backend is the authority on what EXISTS, so
    // a missing credential adds a sentence rather than removing rows.
    stubFetch({
      "/chat/agents/a1/tools": () => jsonResponse(CATALOGUE),
      "/integrations/github/connection": () => jsonResponse(disconnected),
    });
    renderPage();

    expect(await screen.findByText(/Integração não conectada/i)).toBeTruthy();
    expect(screen.getByRole("link", { name: /conectar github/i })).toBeTruthy();
    // Nothing was hidden.
    expect(screen.getByText("Buscar código")).toBeTruthy();
    expect(screen.getAllByRole("button", { name: "Liberar" })).toHaveLength(7);
  });

  it("drops the notice once the integration is connected", async () => {
    stubFetch({
      "/chat/agents/a1/tools": () => jsonResponse(CATALOGUE),
      "/integrations/github/connection": () => jsonResponse(connected),
    });
    renderPage();

    await screen.findByRole("heading", { name: "GitHub" });
    await waitFor(() => {
      expect(screen.queryByText(/Integração não conectada/i)).toBeNull();
    });
  });

  it("authorizes by canonical name, not by the label it draws", async () => {
    // The grouping rewrites the DISPLAY. If it ever rewrote the identity,
    // the page would toggle the wrong tool — and the request body is the
    // only place that is observable.
    let authorized = false;
    const calls = stubFetch({
      "/chat/agents/a1/tools": (init) => {
        if (init?.method === "POST") {
          authorized = true;
          return jsonResponse({
            ...CATALOGUE,
            authorized_count: 1,
            items: CATALOGUE.items.map((t) =>
              t.name === "github.code.search" ? { ...t, authorized: true } : t,
            ),
          });
        }
        return jsonResponse(
          authorized
            ? {
                ...CATALOGUE,
                authorized_count: 1,
                items: CATALOGUE.items.map((t) =>
                  t.name === "github.code.search" ? { ...t, authorized: true } : t,
                ),
              }
            : CATALOGUE,
        );
      },
      "/integrations/github/connection": () => jsonResponse(connected),
    });

    const user = userEvent.setup();
    renderPage();
    await screen.findByText("Buscar código");

    // The row for "Buscar código" — found through its own article, so the
    // click cannot land on a neighbour.
    const row = screen.getByText("Buscar código").closest("article");
    expect(row).toBeTruthy();
    await user.click(within(row as HTMLElement).getByRole("button", { name: "Liberar" }));

    await waitFor(() => {
      const post = calls.find((c) => c.method === "POST");
      expect(post).toBeTruthy();
      expect(post?.body).toContain("github.code.search");
    });

    // And the row flips to the authorized state.
    await waitFor(() => {
      expect(screen.getByText("1/7")).toBeTruthy();
    });
  });

  it("groups a provider nobody has written code for here", async () => {
    // The page must not contain a list of providers. A capability from an
    // integration that does not exist yet still groups.
    stubFetch({
      "/chat/agents/a1/tools": () =>
        jsonResponse({
          authorized_count: 0,
          items: [
            { ...CATALOGUE.items[0], name: "linear.issue.list", title: "Linear · Issues" },
          ],
        }),
      "/integrations/github/connection": () => jsonResponse(disconnected),
    });
    renderPage();

    expect(await screen.findByRole("heading", { name: "Linear" })).toBeTruthy();
    expect(screen.getByText("Issues")).toBeTruthy();
    // And it gets no connection notice, because nothing here knows its state.
    expect(screen.queryByText(/Integração não conectada/i)).toBeNull();
  });

  it("says the version has no tools rather than drawing an empty group", async () => {
    stubFetch({
      "/chat/agents/a1/tools": () => jsonResponse({ authorized_count: 0, items: [] }),
      "/integrations/github/connection": () => jsonResponse(disconnected),
    });
    renderPage();

    expect(await screen.findByText(/não tem nenhuma ferramenta/i)).toBeTruthy();
  });
});
