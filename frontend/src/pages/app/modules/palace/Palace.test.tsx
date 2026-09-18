// @vitest-environment jsdom

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

import { I18nFixture } from "@/lib/i18n/testing";

import { PalaceMap } from "./PalaceMap";
import { RoomView } from "./RoomView";

/**
 * The Palace, driven against a stubbed read surface.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   NONE OF THESE ASK WHETHER IT LOOKS GOOD
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * They ask whether the map claims adjacency it does not have, whether a
 * vignette invites somebody to count, whether the archived number leaks
 * onto a surface that is supposed to be the active space, and whether a
 * keyboard reader can reach every object without being told where it is.
 */

afterEach(cleanup);

const ROOM_A = {
  room_id: "aaaaaaaa-1111-1111-1111-111111111111",
  name: "Ateliê de Marcenaria",
  description: "O que está em construção.",
  sensitivity: "normal" as const,
  created_at: "2026-09-01T00:00:00Z",
  updated_at: "2026-09-20T00:00:00Z",
  artifact_count: 2,
  memory_count: 1,
  archived_count: 7,
};
const ROOM_B = {
  ...ROOM_A,
  room_id: "bbbbbbbb-2222-2222-2222-222222222222",
  name: "Dojang",
  created_at: "2026-09-05T00:00:00Z",
  // Edited most recently. If the map sorted on this it would come first.
  updated_at: "2026-09-30T00:00:00Z",
  artifact_count: 4,
  memory_count: 0,
  archived_count: 0,
};

const ARTIFACTS = [
  {
    artifact_id: "art-1",
    kind: "list" as const,
    title: "Ferramentas & Insumos",
    status: "active" as const,
    sensitivity: "normal" as const,
    room_id: ROOM_A.room_id,
    item_count: 3,
    item_done_count: 0,
    body_excerpt: "",
    body_truncated: false,
    created_at: "2026-09-02T00:00:00Z",
    updated_at: "2026-09-02T00:00:00Z",
  },
  {
    artifact_id: "art-2",
    kind: "note" as const,
    title: "Uma nota na parede",
    status: "active" as const,
    sensitivity: "normal" as const,
    room_id: ROOM_A.room_id,
    item_count: 0,
    item_done_count: 0,
    body_excerpt: "",
    body_truncated: false,
    created_at: "2026-09-03T00:00:00Z",
    updated_at: "2026-09-03T00:00:00Z",
  },
];

const ARTIFACT_DETAIL = {
  artifact_id: "art-2",
  kind: "note" as const,
  title: "Uma nota na parede",
  body: "O texto real da nota.",
  status: "active" as const,
  sensitivity: "normal" as const,
  room_id: ROOM_A.room_id,
  room: { room_id: ROOM_A.room_id, name: ROOM_A.name },
  created_at: "2026-09-03T00:00:00Z",
  updated_at: "2026-09-03T00:00:00Z",
  items: [],
  item_total: 0,
  item_limit: 100,
  item_offset: 0,
};

let respond: (url: string) => unknown;

beforeEach(() => {
  // jsdom has no layout, so the scene would measure zero and D5 would hand
  // over. A generous box is the equivalent of a desktop window.
  vi.stubGlobal(
    "ResizeObserver",
    class {
      cb: ResizeObserverCallback;
      constructor(cb: ResizeObserverCallback) {
        this.cb = cb;
      }
      observe() {
        this.cb(
          [{ contentRect: { width: 1400, height: 900 } } as ResizeObserverEntry],
          this as unknown as ResizeObserver,
        );
      }
      unobserve() {}
      disconnect() {}
    },
  );
  Element.prototype.getBoundingClientRect = function () {
    return { width: 1400, height: 900, top: 0, left: 0, bottom: 900, right: 1400, x: 0, y: 0, toJSON: () => ({}) };
  };

  respond = (url) => {
    if (url.includes("/palace/overview")) {
      return { rooms: [ROOM_A, ROOM_B], room_total: 2, unfiled: { artifact_count: 0, archived_count: 0 } };
    }
    if (url.includes("/palace/artifacts/")) return ARTIFACT_DETAIL;
    if (url.includes("/palace/artifacts")) {
      return { items: ARTIFACTS, total: ARTIFACTS.length, limit: 100, offset: 0 };
    }
    if (url.includes("/palace/rooms/")) {
      return { ...ROOM_A, status: "active" };
    }
    return { items: [], total: 0, limit: 25, offset: 0 };
  };

  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) => {
      const body = respond(String(input));
      return new Response(JSON.stringify(body), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    }),
  );
});

afterEach(() => vi.unstubAllGlobals());

function mountMap() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <MemoryRouter initialEntries={["/app/modules/palace"]}>
      <QueryClientProvider client={client}>
        <I18nFixture lang="pt">
          <PalaceMap />
        </I18nFixture>
      </QueryClientProvider>
    </MemoryRouter>,
  );
}

function Location() {
  const l = useLocation();
  return <output data-testid="where">{l.pathname + l.search}</output>;
}

function mountRoom(entry = `/app/modules/palace/rooms/${ROOM_A.room_id}`) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <MemoryRouter initialEntries={[entry]}>
      <QueryClientProvider client={client}>
        <I18nFixture lang="pt">
          <Routes>
            <Route
              path="/app/modules/palace/rooms/:roomId"
              element={
                <>
                  <RoomView />
                  <Location />
                </>
              }
            />
            <Route path="*" element={<Location />} />
          </Routes>
        </I18nFixture>
      </QueryClientProvider>
    </MemoryRouter>,
  );
}

/* ══════════════════════════════════════════════════════════════════════
   The map
   ══════════════════════════════════════════════════════════════════════ */

it("orders rooms by creation, never by the last edit", async () => {
  mountMap();
  const links = await screen.findAllByRole("link", { name: /Abrir a sala/ });
  // ROOM_A was created first and edited least recently. Sorting on
  // `updated_at` would put the Dojang first and the map would rearrange
  // itself every time somebody renamed a room.
  expect(links[0]).toHaveProperty("href", expect.stringContaining(ROOM_A.room_id));
  expect(links[1]).toHaveProperty("href", expect.stringContaining(ROOM_B.room_id));
});

it("draws no miniature artifacts in a vignette", async () => {
  mountMap();
  await screen.findAllByRole("link", { name: /Abrir a sala/ });
  const vignettes = screen.getAllByTestId("room-vignette");
  for (const v of vignettes) {
    // Architecture only: floor and two walls. A picture with objects in it
    // invites counting, and counting a picture is how somebody ends up
    // wrong about their own record.
    expect(v.querySelectorAll("polygon")).toHaveLength(3);
  }
});

it("never shows the archived count on the map", async () => {
  mountMap();
  const cards = await screen.findAllByRole("link", { name: /Abrir a sala/ });

  // ROOM_A holds seven archived artifacts. The map is the ACTIVE space, so
  // that number must not be on the card at all.
  //
  // ── Why this reads the card and not the page ───────────────────────
  // An earlier version scanned `document.body.textContent` for `\b7\b`,
  // and that assertion could not fail: concatenated text runs the count
  // straight into the next room's name, so there is no word boundary
  // after the digit. It passed against a mutation that rendered the
  // number in plain sight. Asserting on the card's own text has no such
  // hole.
  const card = cards[0].textContent ?? "";
  expect(card).toContain("2 objetos");
  expect(card).toContain("1 memórias");
  expect(card).not.toContain("7");
});

it("hides the unfiled tray when nothing is unfiled", async () => {
  mountMap();
  await screen.findAllByRole("link", { name: /Abrir a sala/ });
  expect(screen.queryByTestId("unfiled-tray")).toBeNull();
});

it("shows the unfiled tray with a silhouette that is not a room", async () => {
  respond = (url) =>
    url.includes("/palace/overview")
      ? { rooms: [ROOM_A], room_total: 1, unfiled: { artifact_count: 3, archived_count: 0 } }
      : { items: [], total: 0, limit: 25, offset: 0 };
  mountMap();

  const tray = await screen.findByTestId("unfiled-tray");
  expect(within(tray).queryByTestId("room-vignette")).toBeNull();
  expect(within(tray).getByTestId("unfiled-vignette")).toBeTruthy();
});

/* ══════════════════════════════════════════════════════════════════════
   The room
   ══════════════════════════════════════════════════════════════════════ */

it("gives every object a button named by type and title, never by position", async () => {
  mountRoom();
  const board = await screen.findByRole("button", { name: /Nota: Uma nota na parede/ });
  expect(board).toBeTruthy();
  const label = board.getAttribute("aria-label") ?? "";
  for (const positional of ["esquerda", "direita", "linha", "primeira", "segunda"]) {
    expect(label.toLowerCase()).not.toContain(positional);
  }
});

it("opens the Artifact Inspector from a NOTE", async () => {
  const user = userEvent.setup();
  mountRoom();
  // `AcceptsItems() === false` is a rule about what a note may contain. It
  // does not make a note unreachable, and an earlier draft of this design
  // confused the two.
  const board = await screen.findByRole("button", { name: /Nota: Uma nota na parede/ });
  await user.click(board);
  expect(await screen.findByText("O texto real da nota.")).toBeTruthy();
});

it("opens the Inspector with Enter and with Space", async () => {
  const user = userEvent.setup();
  mountRoom();
  const board = await screen.findByRole("button", { name: /Nota: Uma nota na parede/ });

  // Both keys, because a native <button> answers to both and anything that
  // handled only one would be a control that behaves like a button for
  // some readers and not for others.
  board.focus();
  await user.keyboard("{Enter}");
  expect(await screen.findByRole("dialog")).toBeTruthy();

  await user.keyboard("{Escape}");
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());

  board.focus();
  await user.keyboard(" ");
  expect(await screen.findByRole("dialog")).toBeTruthy();
});

it("closes the inspector on Escape and puts focus back where it was", async () => {
  const user = userEvent.setup();
  mountRoom();
  const board = await screen.findByRole("button", { name: /Nota: Uma nota na parede/ });
  await user.click(board);
  const dialog = await screen.findByRole("dialog");

  // Dispatched to whatever holds focus, which after activating an object
  // is the object itself. That is the real situation: a handler that only
  // worked with focus inside the panel would work for nobody.
  // Focus is deliberately moved into the panel first. Without this the
  // assertion below is hollow: focus never left the object, so it would
  // "return" there even if nothing restored it. An earlier version of this
  // test passed against a restoration that threw and did nothing.
  const close = within(dialog).getByRole("button", { name: /Fechar/ });
  close.focus();
  expect(document.activeElement).toBe(close);

  await user.keyboard("{Escape}");
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  await waitFor(() => expect(document.activeElement).toBe(board));
});

it("keeps one tab stop per group and moves inside it with the arrows", async () => {
  const user = userEvent.setup();
  // Three lists, so a group has somewhere for an arrow to go. With one
  // item per group the arrow correctly does nothing, and a test built on
  // that fixture would prove nothing either way.
  const lists = Array.from({ length: 3 }, (_, i) => ({
    ...ARTIFACTS[0],
    artifact_id: `l-${i}`,
    title: `Lista ${i}`,
    created_at: `2026-09-0${i + 1}T00:00:00Z`,
  }));
  respond = (url) => {
    if (url.includes("/palace/rooms/")) return { ...ROOM_A, status: "active" };
    if (url.includes("/palace/artifacts")) {
      return { items: [...lists, ARTIFACTS[1]], total: 4, limit: 100, offset: 0 };
    }
    return { items: [], total: 0, limit: 25, offset: 0 };
  };
  mountRoom();
  await screen.findByRole("button", { name: /Nota: Uma nota na parede/ });

  const buttons = screen
    .getAllByRole("button")
    .filter((b) => b.hasAttribute("data-hit-key"));
  const stops = buttons.filter((b) => b.tabIndex === 0);
  // Two kinds present plus the memory surface: three groups, three stops.
  expect(stops).toHaveLength(3);
  // A positive tabindex would reorder the whole page for everybody.
  expect(buttons.some((b) => b.tabIndex > 0)).toBe(false);

  const firstList = screen.getByRole("button", { name: "Lista: Lista 0" });
  firstList.focus();
  await user.keyboard("{ArrowRight}");
  expect(document.activeElement).toBe(
    screen.getByRole("button", { name: "Lista: Lista 1" }),
  );

  await user.keyboard("{End}");
  expect(document.activeElement).toBe(
    screen.getByRole("button", { name: "Lista: Lista 2" }),
  );
  await user.keyboard("{Home}");
  expect(document.activeElement).toBe(firstList);
});

it("renders the memory surface only when the room has memories", async () => {
  mountRoom();
  expect(await screen.findByRole("button", { name: /Memórias desta sala/ })).toBeTruthy();

  cleanup();
  respond = (url) => {
    if (url.includes("/palace/rooms/")) return { ...ROOM_A, status: "active", memory_count: 0 };
    if (url.includes("/palace/artifacts")) {
      return { items: ARTIFACTS, total: 2, limit: 100, offset: 0 };
    }
    return { items: [], total: 0, limit: 25, offset: 0 };
  };
  mountRoom();
  await screen.findByRole("button", { name: /Nota: Uma nota na parede/ });
  expect(screen.queryByRole("button", { name: /Memórias desta sala/ })).toBeNull();
});

it("sends a pile to the Library filtered by room and kind", async () => {
  const user = userEvent.setup();
  const many = Array.from({ length: 15 }, (_, i) => ({
    ...ARTIFACTS[0],
    artifact_id: `many-${i}`,
    title: `Lista ${i}`,
    created_at: `2026-09-${String(1 + i).padStart(2, "0")}T00:00:00Z`,
  }));
  respond = (url) => {
    if (url.includes("/palace/rooms/")) return { ...ROOM_A, status: "active" };
    if (url.includes("/palace/artifacts")) {
      return { items: many, total: many.length, limit: 100, offset: 0 };
    }
    return { items: [], total: 0, limit: 25, offset: 0 };
  };
  mountRoom();

  const pile = await screen.findByRole("button", { name: /Mais 3 em Lista/ });
  await user.click(pile);
  await waitFor(() => {
    const where = screen.getByTestId("where").textContent ?? "";
    expect(where).toContain("/app/modules/palace/library");
    expect(where).toContain(`room=${ROOM_A.room_id}`);
    expect(where).toContain("kind=list");
  });
});

it("keeps the shell and the decoration out of the interactive tree", async () => {
  mountRoom();
  await screen.findByRole("button", { name: /Nota: Uma nota na parede/ });
  for (const id of ["room-shell", "room-decor"]) {
    const node = screen.getByTestId(id);
    expect(node.getAttribute("aria-hidden")).toBe("true");
    expect(node.style.pointerEvents).toBe("none");
    expect(node.querySelector("[tabindex]")).toBeNull();
    expect(node.querySelector("button")).toBeNull();
  }
});

it("offers the Library as a visible equivalent, always", async () => {
  mountRoom();
  const link = await screen.findByRole("link", { name: /Ver em lista/ });
  expect(link.getAttribute("href")).toContain(`room=${ROOM_A.room_id}`);
});

it("hands over to the room's list when the scene would be too small", async () => {
  // A narrow container: the smallest target falls under 44px.
  vi.stubGlobal(
    "ResizeObserver",
    class {
      cb: ResizeObserverCallback;
      constructor(cb: ResizeObserverCallback) {
        this.cb = cb;
      }
      observe() {
        this.cb(
          [{ contentRect: { width: 180, height: 140 } } as ResizeObserverEntry],
          this as unknown as ResizeObserver,
        );
      }
      unobserve() {}
      disconnect() {}
    },
  );
  mountRoom();

  expect(await screen.findByText(/A sala não cabe nesta tela/)).toBeTruthy();
  expect(screen.getByRole("button", { name: /Ver a sala mesmo assim/ })).toBeTruthy();
  expect(screen.queryByTestId("room-scene")).toBeNull();
});

it("forces the spatial view through the URL and nothing else", async () => {
  vi.stubGlobal(
    "ResizeObserver",
    class {
      cb: ResizeObserverCallback;
      constructor(cb: ResizeObserverCallback) {
        this.cb = cb;
      }
      observe() {
        this.cb(
          [{ contentRect: { width: 180, height: 140 } } as ResizeObserverEntry],
          this as unknown as ResizeObserver,
        );
      }
      unobserve() {}
      disconnect() {}
    },
  );
  mountRoom(`/app/modules/palace/rooms/${ROOM_A.room_id}?view=spatial`);

  expect(await screen.findByTestId("room-scene")).toBeTruthy();
  expect(screen.queryByText(/A sala não cabe nesta tela/)).toBeNull();
  // Nothing about the view was written anywhere durable: the override is
  // the address and only the address. (The language key belongs to the
  // test fixture, not to Palace.)
  const keys = Object.keys(window.localStorage);
  expect(keys.some((k) => k.toLowerCase().includes("palace"))).toBe(false);
  expect(keys.some((k) => k.toLowerCase().includes("view"))).toBe(false);
});

it("says nothing about content it was not given", async () => {
  mountRoom();
  await screen.findByRole("button", { name: /Nota: Uma nota na parede/ });
  const text = (document.body.textContent ?? "").toLowerCase();
  for (const marker of ["oculto", "escondid", "sensív", "restrito", "sem permissão"]) {
    expect(text).not.toContain(marker);
  }
});
