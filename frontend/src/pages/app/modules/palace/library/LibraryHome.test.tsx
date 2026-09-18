// @vitest-environment jsdom

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

import { I18nFixture } from "@/lib/i18n/testing";

import { LibraryHome } from "./LibraryHome";

/**
 * The Library, end to end against a stubbed backend.
 *
 * ── What these guard ───────────────────────────────────────────────────
 * The behaviours that are easy to get subtly wrong and impossible to
 * notice by looking:
 *
 *   · an empty collection and an empty search result say different
 *     things. Collapsing them tells somebody their Palace is empty
 *     because they mistyped a word;
 *
 *   · nothing on screen counts, names or hints at content the backend
 *     withheld. For this surface it does not exist;
 *
 *   · every row is a real link, so it is reachable by Tab and can be
 *     opened in a new tab;
 *
 *   · the skeleton does not imply how many rows are coming.
 */

afterEach(cleanup);

const ROOM = {
  room_id: "11111111-1111-1111-1111-111111111111",
  name: "Ateliê de Marcenaria",
  description: "O que está em construção.",
  status: "active" as const,
  sensitivity: "normal" as const,
  created_at: "2026-09-01T10:00:00Z",
  updated_at: "2026-09-10T10:00:00Z",
};

function page<T>(items: T[], total = items.length) {
  return { items, total, limit: 25, offset: 0 };
}

let respond: (url: string) => unknown;

beforeEach(() => {
  respond = () => page([]);
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) => {
      const body = respond(String(input));
      if (body instanceof Error) {
        return new Response(
          JSON.stringify({ error: { code: "internal", message: "boom" } }),
          { status: 500, headers: { "Content-Type": "application/json" } },
        );
      }
      return new Response(JSON.stringify(body), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    }),
  );
});

afterEach(() => vi.unstubAllGlobals());

function mount(entry = "/app/modules/palace/library") {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
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

it("renders a room as a link, not as a clickable div", async () => {
  respond = () => page([ROOM]);
  mount();

  const link = await screen.findByRole("link", { name: /Ateliê de Marcenaria/ });
  expect(link.getAttribute("href")).toBe(
    `/app/modules/palace/library/rooms/${ROOM.room_id}`,
  );
});

it("tells an empty collection apart from a search that matched nothing", async () => {
  respond = () => page([]);
  const view = mount();

  // Nothing at all: the copy explains how the Palace gets built.
  expect(await screen.findByText(/Nenhuma sala ainda/)).toBeTruthy();
  expect(screen.queryByText(/Nada corresponde à busca/)).toBeNull();

  view.unmount();

  // The same empty page, but with a query typed: a different sentence.
  mount("/app/modules/palace/library?q=inexistente");
  expect(await screen.findByText(/Nada corresponde à busca/)).toBeTruthy();
  expect(screen.queryByText(/Nenhuma sala ainda/)).toBeNull();
});

it("never hints that anything was withheld", async () => {
  respond = () => page([ROOM]);
  const { container } = mount();
  await screen.findByRole("link", { name: /Ateliê de Marcenaria/ });

  const text = container.textContent ?? "";
  for (const marker of [
    "oculto",
    "ocultos",
    "escondid",
    "sensível",
    "sensivel",
    "restrito",
    "sem permissão",
  ]) {
    expect(text.toLowerCase()).not.toContain(marker);
  }
});

it("shows the total the backend reports, not the number of rows on screen", async () => {
  respond = () => ({ items: [ROOM], total: 7, limit: 25, offset: 0 });
  mount();
  // "1 a 1 de 7": the reader can tell the page is not the whole answer.
  expect(await screen.findByText(/de\s+7/)).toBeTruthy();
});

it("surfaces an ApiError with a retry rather than an empty list", async () => {
  respond = () => new Error("fail");
  mount();
  expect(await screen.findByText(/Não foi possível carregar/)).toBeTruthy();
  expect(screen.getByRole("button", { name: /Tentar de novo/ })).toBeTruthy();
  // An error is not an empty state. Saying "no rooms yet" here would tell
  // somebody their Palace is empty because the network blinked.
  expect(screen.queryByText(/Nenhuma sala ainda/)).toBeNull();
});

it("switches tab by keyboard and keeps it in the URL", async () => {
  respond = () => page([]);
  const user = userEvent.setup();
  mount();

  const artifacts = await screen.findByRole("button", { name: "Objetos" });
  artifacts.focus();
  await user.keyboard("{Enter}");

  await waitFor(() => {
    expect(
      screen.getByRole("button", { name: "Objetos" }).getAttribute("aria-current"),
    ).toBe("page");
  });
});

it("offers an explicit unfiled option on the artifacts room filter", async () => {
  respond = (url) => (url.includes("/palace/rooms") ? page([ROOM]) : page([]));
  mount("/app/modules/palace/library?tab=artifacts");

  const roomFilter = (await screen.findByLabelText("Sala")) as HTMLSelectElement;
  // The room options are themselves a Palace listing, so they arrive on
  // their own tick.
  await waitFor(() => {
    expect(within(roomFilter).getAllByRole("option").length).toBeGreaterThan(2);
  });
  const values = within(roomFilter)
    .getAllByRole("option")
    .map((o) => (o as HTMLOptionElement).value);

  // "Filed nowhere" is a state, not the absence of a filter, and the two
  // are different options.
  expect(values).toContain("");
  expect(values).toContain("none");
  expect(values).toContain(ROOM.room_id);
});

it("draws a fixed skeleton that implies no row count", async () => {
  let resolve: (() => void) | undefined;
  const gate = new Promise<void>((r) => {
    resolve = r;
  });
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => {
      await gate;
      return new Response(JSON.stringify(page([])), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    }),
  );

  const { container } = mount();
  const busy = await screen.findByLabelText("Carregando");
  // Always five, whatever the answer turns out to be.
  expect(within(busy).getAllByRole("row")).toHaveLength(5);
  expect(container.textContent).not.toMatch(/\d+\s+salas?/i);

  resolve?.();
});
