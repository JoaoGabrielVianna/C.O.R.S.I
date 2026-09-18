// @vitest-environment jsdom

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

import { I18nFixture } from "@/lib/i18n/testing";

import { LibraryHome } from "./LibraryHome";

/**
 * Known content does not disappear to be re-learned.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   A NEW QUESTION IS NOT A REASON TO TAKE AWAY THE OLD ANSWER
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * ── What was measured ──────────────────────────────────────────────────
 * Every keystroke in the search box changes the query key, and the Library
 * went rows → `isPending` → skeleton → rows. 118ms locally, past 250ms at a
 * real round trip, on every letter. The reader was reading those rows.
 *
 * ── The rule these encode ──────────────────────────────────────────────
 *   NO KNOWN DATA  + a query          → a loading representation is right
 *   KNOWN DATA     + a NEW query      → keep the rows, say they are being
 *                                       refreshed, replace them when the
 *                                       answer lands
 *
 * ── And the line it must not cross ─────────────────────────────────────
 * Kept rows are the PREVIOUS answer. The surface may not let them read as
 * the new one: not silently while fetching, and above all not when the new
 * query FAILS. An error that left the old rows up with no error shown would
 * be the screen quietly answering a question nobody got an answer to.
 */

afterEach(cleanup);

const ROOM_A = {
  room_id: "11111111-1111-1111-1111-111111111111",
  name: "Ateliê de Marcenaria",
  description: "O que está em construção.",
  status: "active" as const,
  sensitivity: "normal" as const,
  created_at: "2026-09-01T10:00:00Z",
  updated_at: "2026-09-10T10:00:00Z",
};
const ROOM_B = { ...ROOM_A, room_id: "22222222-2222-2222-2222-222222222222", name: "Oficina" };

const ARTIFACT = {
  artifact_id: "33333333-3333-3333-3333-333333333333",
  title: "Plano de estudos",
  kind: "plan" as const,
  room_id: null,
  status: "active" as const,
  sensitivity: "normal" as const,
  item_count: 0,
  item_done_count: 0,
  created_at: "2026-09-01T10:00:00Z",
  updated_at: "2026-09-10T10:00:00Z",
};
const ARTIFACT_B = { ...ARTIFACT, artifact_id: "44444444-4444-4444-4444-444444444444", title: "Lista de compras" };

function page<T>(items: T[], total = items.length, offset = 0) {
  return { items, total, limit: 25, offset };
}

/** What the stubbed backend answers, and whether it answers yet. */
let respond: (url: string) => unknown;
let holding: string | null = null;
let pending: (() => void)[] = [];

/** Make the next request for `pattern` hang until `release()`. */
function hold(pattern: string) {
  holding = pattern;
}
function release() {
  holding = null;
  pending.splice(0).forEach((r) => r());
}

beforeEach(() => {
  respond = () => page([]);
  holding = null;
  pending = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (holding && url.includes(holding)) {
        await new Promise<void>((r) => pending.push(r));
      }
      const body = respond(url);
      if (body instanceof Error) {
        return new Response(JSON.stringify({ error: { code: "internal", message: "boom" } }), {
          status: 500,
          headers: { "Content-Type": "application/json" },
        });
      }
      return new Response(JSON.stringify(body), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    }),
  );
});

afterEach(() => {
  release();
  vi.unstubAllGlobals();
});

function mount(entry = "/app/modules/palace/library") {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <MemoryRouter initialEntries={[entry]}>
      <QueryClientProvider client={client}>
        <I18nFixture lang="pt">
          <LibraryHome />
        </I18nFixture>
      </QueryClientProvider>
    </MemoryRouter>,
  );
}

const updating = () => screen.queryByText("Atualizando os resultados");

/* ── first load ─────────────────────────────────────────────────────── */

it("still shows a loading state when there is nothing known yet", async () => {
  hold("/palace/rooms");
  respond = () => page([ROOM_A]);
  mount();

  // No previous answer exists, so a skeleton is the honest representation
  // and continuity has nothing to preserve.
  expect(await screen.findByLabelText("Carregando")).toBeTruthy();
  expect(screen.queryByRole("link", { name: /Ateliê de Marcenaria/ })).toBeNull();
  expect(updating()).toBeNull();

  release();
  expect(await screen.findByRole("link", { name: /Ateliê de Marcenaria/ })).toBeTruthy();
});

/* ── search ─────────────────────────────────────────────────────────── */

it("keeps the known rows on screen while a new search loads", async () => {
  const user = userEvent.setup();
  respond = () => page([ROOM_A]);
  mount();
  await screen.findByRole("link", { name: /Ateliê de Marcenaria/ });

  hold("/palace/rooms");
  await user.type(screen.getByLabelText("Buscar"), "iado");

  // The rows a person was reading are still there, and the surface says
  // out loud that they are the previous answer.
  expect(screen.getByRole("link", { name: /Ateliê de Marcenaria/ })).toBeTruthy();
  expect(screen.queryByLabelText("Carregando")).toBeNull();
  await waitFor(() => expect(updating()).toBeTruthy());

  respond = () => page([ROOM_B]);
  release();

  expect(await screen.findByRole("link", { name: /Oficina/ })).toBeTruthy();
  await waitFor(() => {
    expect(screen.queryByRole("link", { name: /Ateliê de Marcenaria/ })).toBeNull();
    expect(updating()).toBeNull();
  });
});

it("keeps the search box usable while the refresh is in flight", async () => {
  const user = userEvent.setup();
  respond = () => page([ROOM_A]);
  mount();
  await screen.findByRole("link", { name: /Ateliê de Marcenaria/ });

  hold("/palace/rooms");
  const search = screen.getByLabelText("Buscar") as HTMLInputElement;
  await user.type(search, "a");
  await waitFor(() => expect(updating()).toBeTruthy());

  // Nothing is disabled and nothing stole the focus: a person can keep
  // typing through the refresh, which is the whole point of not blocking.
  expect(search.disabled).toBe(false);
  expect(document.activeElement).toBe(search);
  await user.type(search, "b");
  expect(search.value).toBe("ab");
});

/* ── filters ────────────────────────────────────────────────────────── */

it("keeps the known rows while a filter change loads", async () => {
  const user = userEvent.setup();
  respond = (url) => (url.includes("/palace/artifacts") ? page([ARTIFACT]) : page([]));
  mount("/app/modules/palace/library?tab=artifacts");
  await screen.findByRole("link", { name: /Plano de estudos/ });

  hold("/palace/artifacts");
  await user.selectOptions(screen.getByLabelText("Tipo"), "note");

  expect(screen.getByRole("link", { name: /Plano de estudos/ })).toBeTruthy();
  expect(screen.queryByLabelText("Carregando")).toBeNull();
  await waitFor(() => expect(updating()).toBeTruthy());

  respond = (url) => (url.includes("/palace/artifacts") ? page([ARTIFACT_B]) : page([]));
  release();
  expect(await screen.findByRole("link", { name: /Lista de compras/ })).toBeTruthy();
});

/* ── pagination ─────────────────────────────────────────────────────── */

it("keeps page one on screen until page two arrives", async () => {
  const user = userEvent.setup();
  respond = () => page([ROOM_A], 30, 0);
  mount();
  await screen.findByRole("link", { name: /Ateliê de Marcenaria/ });

  hold("/palace/rooms");
  await user.click(screen.getByRole("button", { name: /Próxima/ }));

  // The table does not empty itself between two pages.
  expect(screen.getByRole("link", { name: /Ateliê de Marcenaria/ })).toBeTruthy();
  expect(screen.queryByLabelText("Carregando")).toBeNull();
  await waitFor(() => expect(updating()).toBeTruthy());

  respond = () => page([ROOM_B], 30, 25);
  release();
  expect(await screen.findByRole("link", { name: /Oficina/ })).toBeTruthy();
  expect(screen.queryByRole("link", { name: /Ateliê de Marcenaria/ })).toBeNull();
});

/* ── how the refresh is announced ───────────────────────────────────── */

it("announces the refresh beside the table instead of marking the table unavailable", async () => {
  const user = userEvent.setup();
  respond = () => page([ROOM_A]);
  const { container } = mount();
  await screen.findByRole("link", { name: /Ateliê de Marcenaria/ });

  hold("/palace/rooms");
  await user.type(screen.getByLabelText("Buscar"), "x");
  await waitFor(() => expect(updating()).toBeTruthy());

  const status = screen.getByRole("status");
  expect(status.getAttribute("aria-live")).toBe("polite");

  // The rows are real. Marking the table busy would tell a screen reader
  // that what it is reading is unavailable.
  const table = container.querySelector("table")!;
  expect(table.getAttribute("aria-busy")).toBeNull();
  expect(within(table).getByRole("link", { name: /Ateliê de Marcenaria/ })).toBeTruthy();

  release();
});

/* ── the failure case ───────────────────────────────────────────────── */

it("does not pass the previous rows off as the result of a query that failed", async () => {
  const user = userEvent.setup();
  respond = () => page([ROOM_A]);
  mount();
  await screen.findByRole("link", { name: /Ateliê de Marcenaria/ });

  hold("/palace/rooms");
  await user.type(screen.getByLabelText("Buscar"), "z");
  respond = () => new Error("fail");
  release();

  // The error is shown, and the old rows are NOT left standing as though
  // they answered the new search.
  expect(await screen.findByText(/Não foi possível carregar/)).toBeTruthy();
  expect(screen.queryByRole("link", { name: /Ateliê de Marcenaria/ })).toBeNull();
  expect(updating()).toBeNull();

  // And the reader is not stuck: retry and the filters are both live.
  expect(screen.getByRole("button", { name: /Tentar de novo/ })).toBeTruthy();
  expect((screen.getByLabelText("Buscar") as HTMLInputElement).disabled).toBe(false);
});

/* ── the invariant that predates this slice ─────────────────────────── */

it("offers nothing that opts into withheld content, refreshing or not", async () => {
  const user = userEvent.setup();
  respond = () => page([ROOM_A]);
  const { container } = mount();
  await screen.findByRole("link", { name: /Ateliê de Marcenaria/ });

  hold("/palace/rooms");
  await user.type(screen.getByLabelText("Buscar"), "q");
  await waitFor(() => expect(updating()).toBeTruthy());

  const html = container.innerHTML.toLowerCase();
  for (const forbidden of ["include_highly_sensitive", "highly_sensitive", "highlysensitive"]) {
    expect(html).not.toContain(forbidden);
  }
  const values = Array.from(container.querySelectorAll("option")).map((o) => o.value);
  expect(values.some((v) => v.includes("sensitive"))).toBe(false);

  release();
});
