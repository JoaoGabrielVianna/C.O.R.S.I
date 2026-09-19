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
 * They ask whether the house draws furniture that corresponds to nothing,
 * whether pressing an object leaves the Palace, whether a withheld room
 * leaves a hole somebody could infer it from, whether the archived number
 * leaks onto a surface that is supposed to be the active space, and
 * whether a keyboard reader can reach every object without being told
 * where it is.
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
  // Edited most recently. If the house sorted on this it would come first.
  updated_at: "2026-09-30T00:00:00Z",
  artifact_count: 0,
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
    item_done_count: 2,
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

/**
 * The list's real entries.
 *
 * This fixture is the whole point of the LIST case: pressing the drawer
 * cabinet has to show THESE, read from the artifact's own endpoint, and
 * not a count copied off the row that drew the cabinet.
 */
const LIST_DETAIL = {
  artifact_id: "art-1",
  kind: "list" as const,
  title: "Ferramentas & Insumos",
  body: "O que falta comprar.",
  status: "active" as const,
  sensitivity: "normal" as const,
  room_id: ROOM_A.room_id,
  room: { room_id: ROOM_A.room_id, name: ROOM_A.name },
  created_at: "2026-09-02T00:00:00Z",
  updated_at: "2026-09-02T00:00:00Z",
  items: [
    { item_id: "i1", position: 0, text: "Serra de bancada", done: true },
    { item_id: "i2", position: 1, text: "Lixa 120", done: true },
    { item_id: "i3", position: 2, text: "Cola branca", done: false },
  ],
  item_total: 3,
  item_limit: 100,
  item_offset: 0,
};

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

const MEMORY_ROW = {
  memory_id: "mem-1",
  kind: "fact" as const,
  importance: 3,
  confidence: "high" as const,
  status: "active" as const,
  sensitivity: "normal" as const,
  occurred_at: null,
  room_id: ROOM_A.room_id,
  artifact_id: null,
  summary: "Uma memória desta sala",
  content_excerpt: "",
  content_truncated: false,
  created_at: "2026-09-04T00:00:00Z",
  updated_at: "2026-09-04T00:00:00Z",
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
      // ══════════════════════════════════════════════════════════════
      //   IN THE ORDER THE BACKEND ACTUALLY SENDS IT
      // ══════════════════════════════════════════════════════════════
      //
      // `repo/rooms.go:82` is `ORDER BY updated_at DESC, id`, and ROOM_B
      // was edited most recently, so ROOM_B comes FIRST on the wire while
      // ROOM_A was created first.
      //
      // An earlier version of this fixture listed them in creation order,
      // which made the ordering test pass without the surface doing
      // anything at all: it asserted that a hand-sorted array stayed
      // sorted. Handing over the real order is what gives that test
      // something to prove.
      return { rooms: [ROOM_B, ROOM_A], room_total: 2, unfiled: { artifact_count: 0, archived_count: 0 } };
    }
    if (url.includes("/palace/artifacts/art-1")) return LIST_DETAIL;
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

function Location() {
  const l = useLocation();
  return <output data-testid="where">{l.pathname + l.search}</output>;
}

function mountMap(entry = "/app/modules/palace") {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <MemoryRouter initialEntries={[entry]}>
      <QueryClientProvider client={client}>
        <I18nFixture lang="pt">
          <Routes>
            <Route
              path="/app/modules/palace"
              element={
                <>
                  <PalaceMap />
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

/** Shrinks the measured box so the house hands over to the list. */
function stubNarrowContainer() {
  vi.stubGlobal(
    "ResizeObserver",
    class {
      cb: ResizeObserverCallback;
      constructor(cb: ResizeObserverCallback) {
        this.cb = cb;
      }
      observe() {
        this.cb(
          [{ contentRect: { width: 180, height: 260 } } as ResizeObserverEntry],
          this as unknown as ResizeObserver,
        );
      }
      unobserve() {}
      disconnect() {}
    },
  );
  Element.prototype.getBoundingClientRect = function () {
    return { width: 180, height: 260, top: 0, left: 0, bottom: 260, right: 180, x: 0, y: 0, toJSON: () => ({}) };
  };
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
   The house
   ══════════════════════════════════════════════════════════════════════ */

it("places rooms by creation, never in the order the wire sent them", async () => {
  mountMap();
  await screen.findByTestId("palace-building");

  // The overview handed this surface `[ROOM_B, ROOM_A]`, which is the real
  // `updated_at DESC`. ROOM_A was created first, so it stands in the first
  // cell of the grid. A house that trusted the wire would rearrange itself
  // every time somebody fixed a typo.
  const shells = [...document.querySelectorAll("[data-room-shell]")].map((el) =>
    el.getAttribute("data-room-shell"),
  );
  expect(shells).toEqual([ROOM_A.room_id, ROOM_B.room_id]);

  const labels = screen.getAllByTestId("building-room-label");
  expect(labels[0].getAttribute("data-room-id")).toBe(ROOM_A.room_id);
});

it("reads as one house: rooms sharing walls, with the real objects inside", async () => {
  mountMap();
  await screen.findByTestId("palace-building");

  expect(screen.getAllByTestId("building-room")).toHaveLength(2);
  // ROOM_A holds both fixture artifacts; ROOM_B holds none. Two objects,
  // both of them real.
  const objects = screen
    .getAllByRole("button")
    .filter((b) => b.getAttribute("data-item-type") === "artifact");
  expect(objects).toHaveLength(2);
  // The room is part of every object's accessible name: the caption
  // painted on the room is `aria-hidden`, so this is the only place a
  // screen reader learns which room it is standing in.
  expect(objects.map((b) => b.getAttribute("aria-label"))).toEqual([
    `Lista: Ferramentas & Insumos, em ${ROOM_A.name}`,
    `Nota: Uma nota na parede, em ${ROOM_A.name}`,
  ]);
});

it("draws no furniture in a room that holds none", async () => {
  // ══════════════════════════════════════════════════════════════════
  //   THE RULE WHOSE VIOLATION WOULD MAKE THE PICTURE LOOK BETTER
  // ══════════════════════════════════════════════════════════════════
  //
  // ROOM_B has no artifacts. A cabinet drawn there would be a claim that
  // it holds a list, and a cabinet somebody can press that opens nothing
  // is an affordance that lies. The room is drawn; it is drawn empty.
  mountMap();
  await screen.findByTestId("palace-building");

  // ── Why this counts by room and not by label ──────────────────────
  // The first version filtered buttons whose aria-label mentioned the
  // room's name, and it could not fail: a fabricated object has no
  // artifact behind it, so its label is the empty title of nothing and
  // mentions no room. It passed against a mutation that put a cabinet in
  // every empty room. Attributing each button to its room has no such
  // hole.
  const inRoomB = screen
    .getAllByRole("button")
    .filter((b) => b.getAttribute("data-in-room") === ROOM_B.room_id);
  expect(inRoomB).toHaveLength(0);

  const inRoomA = screen
    .getAllByRole("button")
    .filter((b) => b.getAttribute("data-in-room") === ROOM_A.room_id);
  expect(inRoomA.map((b) => b.getAttribute("data-item-type"))).toEqual([
    "artifact",
    "artifact",
    "memory",
  ]);

  // Both rooms are drawn. One of them is drawn empty, which is the point.
  expect(screen.getAllByTestId("building-room")).toHaveLength(2);
});

it("gives each kind its own silhouette", async () => {
  respond = (url) => {
    if (url.includes("/palace/overview")) {
      return {
        rooms: [{ ...ROOM_A, artifact_count: 4, memory_count: 0 }],
        room_total: 1,
        unfiled: { artifact_count: 0, archived_count: 0 },
      };
    }
    if (url.includes("/palace/artifacts/")) return ARTIFACT_DETAIL;
    if (url.includes("/palace/artifacts")) {
      const kinds = ["project", "list", "plan", "note"] as const;
      return {
        items: kinds.map((kind, i) => ({
          ...ARTIFACTS[0],
          artifact_id: `k-${kind}`,
          kind,
          title: `Objeto ${kind}`,
          created_at: `2026-09-0${i + 1}T00:00:00Z`,
        })),
        total: 4,
        limit: 100,
        offset: 0,
      };
    }
    return { items: [], total: 0, limit: 25, offset: 0 };
  };
  mountMap();
  await screen.findByTestId("palace-building");

  // Four objects, four distinct drawings. Same box, different shapes: the
  // kinds differ in silhouette and never in extent, because a difference
  // in size is a difference a reader would measure.
  const drawn = [...document.querySelectorAll("[data-paint-key]")].map((g) => g.innerHTML);
  expect(drawn).toHaveLength(4);
  expect(new Set(drawn).size).toBe(4);
});

it("never shows a count on the house", async () => {
  mountMap();
  await screen.findByTestId("palace-building");

  // ROOM_A holds seven archived artifacts. The entrance is the ACTIVE
  // space, and the house draws architecture and real objects, never
  // numbers.
  const layer = screen.getByTestId("building-hit-layer");
  expect(layer.textContent).toContain(ROOM_A.name);
  expect(layer.textContent).not.toContain("7");
  expect(layer.textContent).not.toContain("objetos");
});

it("keeps the painted layer out of the interactive tree", async () => {
  mountMap();
  await screen.findByTestId("palace-building");

  const scenery = screen.getAllByTestId("building-room")[0].closest("[aria-hidden]")!;
  expect(scenery.getAttribute("aria-hidden")).toBe("true");
  expect((scenery as HTMLElement).style.pointerEvents).toBe("none");
  expect(scenery.querySelector("a")).toBeNull();
  expect(scenery.querySelector("button")).toBeNull();
  expect(scenery.querySelector("[tabindex]")).toBeNull();
});

it("does not turn a room into a control of any size", async () => {
  // ══════════════════════════════════════════════════════════════════
  //   NOTHING ABOUT A ROOM IS PRESSABLE. THE THINGS INSIDE IT ARE
  // ══════════════════════════════════════════════════════════════════
  //
  // C1 made the whole room a link and the human gate rejected exactly
  // that. The first draft of C1.1 made the room's NAME a link, and the
  // browser showed that chip landing on top of a cabinet: whichever won
  // the z-order, the reader saw one thing and pressed another.
  mountMap();
  await screen.findByTestId("palace-building");

  // No room shell is reachable.
  expect(document.querySelector("[data-room-shell] a")).toBeNull();
  expect(document.querySelector("[data-room-shell] button")).toBeNull();

  // The name is a caption: not focusable, not pressable, not announced.
  const labels = screen.getAllByTestId("building-room-label");
  expect(labels).toHaveLength(2);
  for (const label of labels) {
    expect(label.tagName).toBe("SPAN");
    expect(label.getAttribute("aria-hidden")).toBe("true");
    expect(label.className).toContain("pointer-events-none");
  }

  // And there is no room link anywhere in the house.
  expect(screen.queryAllByRole("link", { name: /Abrir a sala/ })).toHaveLength(0);
});

it("offers the room as a named secondary action inside the inspector", async () => {
  // The house's one way into a room, now that the name is a caption.
  const user = userEvent.setup();
  mountMap();
  await screen.findByTestId("palace-building");

  await user.click(
    screen.getByRole("button", { name: `Nota: Uma nota na parede, em ${ROOM_A.name}` }),
  );
  const panel = await screen.findByTestId("house-inspector");
  const roomLink = within(panel).getByRole("link", { name: `Abrir a sala ${ROOM_A.name}` });
  expect(roomLink.getAttribute("href")).toBe(`/app/modules/palace/rooms/${ROOM_A.room_id}`);
});

it("hides the unfiled tray when nothing is unfiled", async () => {
  mountMap();
  await screen.findByTestId("palace-building");
  expect(
    screen.queryAllByRole("button").filter((b) => b.getAttribute("data-item-type") === "unfiled"),
  ).toHaveLength(0);
});

it("puts the unfiled tray at the entrance, reachable and not a room", async () => {
  respond = (url) => {
    if (url.includes("/palace/overview")) {
      return { rooms: [ROOM_A], room_total: 1, unfiled: { artifact_count: 3, archived_count: 0 } };
    }
    if (url.includes("/palace/artifacts")) {
      return { items: [], total: 0, limit: 100, offset: 0 };
    }
    return { items: [], total: 0, limit: 25, offset: 0 };
  };
  mountMap();
  await screen.findByTestId("palace-building");

  const tray = screen
    .getAllByRole("button")
    .find((b) => b.getAttribute("data-item-type") === "unfiled")!;
  expect(tray).toBeTruthy();
  expect(tray.getAttribute("aria-label")).toBe("Ver o que está sem sala");
  // It has no room shell of its own: it is a tray, not a place.
  expect(screen.getAllByTestId("building-room")).toHaveLength(1);
});

/* ══════════════════════════════════════════════════════════════════════
   Opening an object in place
   ══════════════════════════════════════════════════════════════════════ */

it("opens an artifact over the house, without leaving it", async () => {
  const user = userEvent.setup();
  mountMap();
  await screen.findByTestId("palace-building");

  const cabinet = screen.getByRole("button", { name: `Lista: Ferramentas & Insumos, em ${ROOM_A.name}` });
  await user.click(cabinet);

  const panel = await screen.findByTestId("house-inspector");
  expect(panel).toBeTruthy();
  // ══════════════════════════════════════════════════════════════════
  //   THE POINT OF THE WHOLE SLICE: THE PALACE IS STILL THERE
  // ══════════════════════════════════════════════════════════════════
  expect(screen.getByTestId("palace-building")).toBeTruthy();
  expect(screen.getAllByTestId("building-room")).toHaveLength(2);
  // And the address did not change: opening an object is not navigation.
  expect(screen.getByTestId("where").textContent).toBe("/app/modules/palace");
});

it("shows the real entries of a real list", async () => {
  const user = userEvent.setup();
  mountMap();
  await screen.findByTestId("palace-building");

  await user.click(screen.getByRole("button", { name: `Lista: Ferramentas & Insumos, em ${ROOM_A.name}` }));

  const panel = await screen.findByTestId("house-inspector");
  // The entries come from the artifact's own read, not from a count and
  // not from the row that drew the cabinet.
  for (const entry of ["Serra de bancada", "Lixa 120", "Cola branca"]) {
    expect(await within(panel).findByText(entry)).toBeTruthy();
  }
  expect(within(panel).getByText(/2 de 3 concluídos/)).toBeTruthy();
});

it("opens with Enter and with Space", async () => {
  const user = userEvent.setup();
  mountMap();
  await screen.findByTestId("palace-building");

  const board = screen.getByRole("button", { name: `Nota: Uma nota na parede, em ${ROOM_A.name}` });

  board.focus();
  await user.keyboard("{Enter}");
  expect(await screen.findByRole("dialog")).toBeTruthy();

  await user.keyboard("{Escape}");
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());

  board.focus();
  await user.keyboard(" ");
  expect(await screen.findByRole("dialog")).toBeTruthy();
});

it("closes on Escape and puts focus back on the exact object", async () => {
  const user = userEvent.setup();
  mountMap();
  await screen.findByTestId("palace-building");

  const board = screen.getByRole("button", { name: `Nota: Uma nota na parede, em ${ROOM_A.name}` });
  await user.click(board);
  const panel = await screen.findByRole("dialog");

  // Focus is deliberately moved into the panel first. Without this the
  // assertion below is hollow: focus never left the object, so it would
  // "return" there even if nothing restored it.
  const close = within(panel).getByRole("button", { name: /Fechar/ });
  close.focus();
  expect(document.activeElement).toBe(close);

  await user.keyboard("{Escape}");
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  await waitFor(() => expect(document.activeElement).toBe(board));
});

it("offers navigation as a secondary action, never as the primary one", async () => {
  const user = userEvent.setup();
  mountMap();
  await screen.findByTestId("palace-building");

  await user.click(screen.getByRole("button", { name: `Nota: Uma nota na parede, em ${ROOM_A.name}` }));
  const panel = await screen.findByTestId("house-inspector");

  // Pressing the object did not navigate.
  expect(screen.getByTestId("where").textContent).toBe("/app/modules/palace");
  // Leaving is available, explicit, and the reader's choice.
  const details = within(panel).getByRole("link", { name: /Abrir detalhes/ });
  expect(details.getAttribute("href")).toContain(`/library/artifacts/${ARTIFACT_DETAIL.artifact_id}`);
});

it("sends a pile to the Library filtered by its room", async () => {
  const user = userEvent.setup();
  // Nine artifacts in ROOM_A: seven stand individually, the rest pile.
  respond = (url) => {
    if (url.includes("/palace/overview")) {
      return {
        rooms: [{ ...ROOM_A, artifact_count: 9 }],
        room_total: 1,
        unfiled: { artifact_count: 0, archived_count: 0 },
      };
    }
    if (url.includes("/palace/artifacts/")) return ARTIFACT_DETAIL;
    if (url.includes("/palace/artifacts")) {
      return {
        items: Array.from({ length: 9 }, (_, i) => ({
          ...ARTIFACTS[0],
          artifact_id: `many-${i}`,
          title: `Lista ${i}`,
          created_at: `2026-09-${String(1 + i).padStart(2, "0")}T00:00:00Z`,
        })),
        total: 9,
        limit: 100,
        offset: 0,
      };
    }
    return { items: [], total: 0, limit: 25, offset: 0 };
  };
  mountMap();
  await screen.findByTestId("palace-building");

  // A pile stands for artifacts this surface is not drawing individually,
  // so it has no single object to open: it goes to the list that can show
  // all of them.
  const pile = screen.getByRole("button", { name: /Mais 2 em/ });
  await user.click(pile);
  await waitFor(() => {
    const where = screen.getByTestId("where").textContent ?? "";
    expect(where).toContain("/app/modules/palace/library");
    expect(where).toContain(`room=${ROOM_A.room_id}`);
  });
});

it("opens the room's memories from its surface, and never one object per memory", async () => {
  const user = userEvent.setup();
  respond = (url) => {
    if (url.includes("/palace/overview")) {
      return {
        rooms: [{ ...ROOM_A, artifact_count: 0, memory_count: 4 }],
        room_total: 1,
        unfiled: { artifact_count: 0, archived_count: 0 },
      };
    }
    if (url.includes("/palace/memories")) {
      return {
        items: [
          { ...MEMORY_ROW, memory_id: "m1", summary: "Prefere marcenaria a marcenaria industrial" },
          { ...MEMORY_ROW, memory_id: "m2", summary: "Serra precisa de manutenção" },
        ],
        total: 4,
        limit: 25,
        offset: 0,
      };
    }
    return { items: [], total: 0, limit: 100, offset: 0 };
  };
  mountMap();
  await screen.findByTestId("palace-building");

  // Four memories, ONE surface. The geometry knows THAT there are
  // memories and never how many.
  const surfaces = screen
    .getAllByRole("button")
    .filter((b) => b.getAttribute("data-item-type") === "memory");
  expect(surfaces).toHaveLength(1);

  await user.click(surfaces[0]);
  const panel = await screen.findByTestId("house-inspector");
  expect(await within(panel).findByText(/Serra precisa de manutenção/)).toBeTruthy();
  expect(screen.getByTestId("palace-building")).toBeTruthy();
});

/* ══════════════════════════════════════════════════════════════════════
   Privacy
   ══════════════════════════════════════════════════════════════════════ */

it("draws nothing at all for a room the surface withheld", async () => {
  // A `highly_sensitive` room never reaches this surface. The house of
  // one eligible room is byte-identical to a house that never had a
  // second one: no gap, no placeholder, no hint.
  respond = (url) => {
    if (url.includes("/palace/overview")) {
      return { rooms: [ROOM_A], room_total: 1, unfiled: { artifact_count: 0, archived_count: 0 } };
    }
    if (url.includes("/palace/artifacts")) {
      return { items: [ARTIFACTS[0]], total: 1, limit: 100, offset: 0 };
    }
    return { items: [], total: 0, limit: 25, offset: 0 };
  };
  mountMap();
  await screen.findByTestId("palace-building");

  expect(screen.getAllByTestId("building-room")).toHaveLength(1);
  const text = (document.body.textContent ?? "").toLowerCase();
  for (const marker of ["oculto", "escondid", "sensív", "restrito", "sem permissão", "1 item"]) {
    expect(text).not.toContain(marker);
  }
});

it("draws no object for an artifact the surface withheld, and leaks no count", async () => {
  // ══════════════════════════════════════════════════════════════════
  //   THE COUNT AND THE LISTING COME FROM ONE PREDICATE, IN THE BACKEND
  // ══════════════════════════════════════════════════════════════════
  //
  // `artifact_count` is computed under the same visibility rules as the
  // listing, so a withheld artifact is in neither. Here the room reports
  // one eligible artifact and one arrives: one object, and no pile
  // claiming something else is there.
  respond = (url) => {
    if (url.includes("/palace/overview")) {
      return {
        rooms: [{ ...ROOM_A, artifact_count: 1, memory_count: 0 }],
        room_total: 1,
        unfiled: { artifact_count: 0, archived_count: 0 },
      };
    }
    if (url.includes("/palace/artifacts")) {
      return { items: [ARTIFACTS[0]], total: 1, limit: 100, offset: 0 };
    }
    return { items: [], total: 0, limit: 25, offset: 0 };
  };
  mountMap();
  await screen.findByTestId("palace-building");

  const pressable = screen
    .getAllByRole("button")
    .filter((b) => b.hasAttribute("data-item-type"));
  expect(pressable).toHaveLength(1);
  expect(pressable[0].getAttribute("data-item-type")).toBe("artifact");
});

it("draws no memory surface for a room whose memories were withheld", async () => {
  respond = (url) => {
    if (url.includes("/palace/overview")) {
      return {
        rooms: [{ ...ROOM_A, memory_count: 0 }],
        room_total: 1,
        unfiled: { artifact_count: 0, archived_count: 0 },
      };
    }
    if (url.includes("/palace/artifacts")) {
      return { items: [ARTIFACTS[0]], total: 1, limit: 100, offset: 0 };
    }
    return { items: [], total: 0, limit: 25, offset: 0 };
  };
  mountMap();
  await screen.findByTestId("palace-building");

  expect(
    screen.queryAllByRole("button").filter((b) => b.getAttribute("data-item-type") === "memory"),
  ).toHaveLength(0);
});

it("never asks the backend to include withheld content", async () => {
  mountMap();
  await screen.findByTestId("palace-building");

  const calls = (globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls.map((c) =>
    String(c[0]),
  );
  expect(calls.some((u) => u.includes("/palace/artifacts?"))).toBe(true);
  for (const url of calls) {
    expect(url).not.toContain("include_highly_sensitive");
    expect(url).not.toContain("sensitivity");
  }
});

it("asks for the whole house in one request, never one per room", async () => {
  mountMap();
  await screen.findByTestId("palace-building");

  const listings = (globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls
    .map((c) => String(c[0]))
    .filter((u) => u.includes("/palace/artifacts?"));
  // One listing for every room there is. A request per room would be N+1
  // on the surface the operator opens first.
  expect(listings).toHaveLength(1);
  expect(listings[0]).toContain("status=active");
  expect(listings[0]).not.toContain("room_id");
});

/* ══════════════════════════════════════════════════════════════════════
   Accessibility
   ══════════════════════════════════════════════════════════════════════ */

it("makes every object a keyboard-reachable button named by type and title", async () => {
  const user = userEvent.setup();
  mountMap();
  await screen.findByTestId("palace-building");

  const objects = screen
    .getAllByRole("button")
    .filter((b) => b.hasAttribute("data-item-type"));
  for (const object of objects) {
    expect(object.tagName).toBe("BUTTON");
    // A positive tabindex would reorder the whole page for everybody.
    expect(object.tabIndex).toBeLessThanOrEqual(0);
    const label = (object.getAttribute("aria-label") ?? "").toLowerCase();
    for (const positional of ["esquerda", "direita", "linha", "primeira", "segunda", "posição"]) {
      expect(label).not.toContain(positional);
    }
  }

  await user.tab();
  let guard = 0;
  while (!objects.includes(document.activeElement as HTMLButtonElement) && guard < 20) {
    await user.tab();
    guard += 1;
  }
  expect(objects).toContain(document.activeElement);
});

it("reads the house room by room, never in painting order", async () => {
  mountMap();
  await screen.findByTestId("palace-building");

  // The hit layer is emitted in the SEMANTIC order; the SVG is painted
  // back to front. They are different questions.
  const domOrder = screen
    .getAllByRole("button")
    .filter((b) => b.hasAttribute("data-item-type"))
    .map((b) => b.getAttribute("data-hit-key"));
  // ROOM_A holds two artifacts and has memories, so its surface is read
  // after its contents: a room is described by what is in it, and then by
  // what is known about it. ROOM_B holds nothing and contributes nothing.
  expect(domOrder).toEqual([
    "artifact:art-1",
    "artifact:art-2",
    `memory:${ROOM_A.room_id}`,
  ]);
});

it("no longer explains its own sort key to the reader", async () => {
  // C1 printed "side by side is chronology, not a relationship" on the
  // screen. Creation order is a stability mechanism, not a message, and
  // saying it out loud invites the reading it was denying.
  mountMap();
  await screen.findByTestId("palace-building");
  const text = (document.body.textContent ?? "").toLowerCase();
  expect(text).not.toContain("cronologia");
  expect(text).not.toContain("ordem em que você");
});

/* ══════════════════════════════════════════════════════════════════════
   The hand-over, which is D5 one scale up
   ══════════════════════════════════════════════════════════════════════ */

it("hands over to the room list when the objects would be too small", async () => {
  stubNarrowContainer();
  mountMap();

  expect(await screen.findByText(/O prédio não cabe nesta tela/)).toBeTruthy();
  expect(screen.getByRole("button", { name: /Ver o prédio mesmo assim/ })).toBeTruthy();
  expect(screen.queryByTestId("palace-building")).toBeNull();

  // The same rooms, at the same addresses. Nothing is reachable only
  // through the house.
  const cards = screen.getAllByRole("link", { name: /Abrir a sala/ });
  expect(cards).toHaveLength(2);
  expect(cards[0]).toHaveProperty("href", expect.stringContaining(ROOM_A.room_id));
});

it("keeps the card list honest about counts and silent about archived", async () => {
  stubNarrowContainer();
  mountMap();

  const cards = await screen.findAllByRole("link", { name: /Abrir a sala/ });
  // ── Why this reads the card and not the page ───────────────────────
  // An earlier version scanned `document.body.textContent` for `\b7\b`,
  // and that assertion could not fail: concatenated text runs the count
  // straight into the next room's name, so there is no word boundary
  // after the digit. It passed against a mutation that rendered the
  // number in plain sight.
  const card = cards[0].textContent ?? "";
  expect(card).toContain("2 objetos");
  expect(card).toContain("1 memórias");
  expect(card).not.toContain("7");
});

it("still draws no miniature artifacts in a fallback vignette", async () => {
  stubNarrowContainer();
  mountMap();
  await screen.findAllByRole("link", { name: /Abrir a sala/ });
  for (const v of screen.getAllByTestId("room-vignette")) {
    expect(v.querySelectorAll("polygon")).toHaveLength(3);
  }
});

it("shows the fallback tray with a silhouette that is not a room", async () => {
  stubNarrowContainer();
  respond = (url) => {
    if (url.includes("/palace/overview")) {
      return { rooms: [ROOM_B, ROOM_A], room_total: 2, unfiled: { artifact_count: 3, archived_count: 0 } };
    }
    if (url.includes("/palace/artifacts")) {
      return { items: ARTIFACTS, total: 2, limit: 100, offset: 0 };
    }
    return { items: [], total: 0, limit: 25, offset: 0 };
  };
  mountMap();

  const tray = await screen.findByTestId("unfiled-tray");
  expect(within(tray).queryByTestId("room-vignette")).toBeNull();
  expect(within(tray).getByTestId("unfiled-vignette")).toBeTruthy();
});

it("shows the house anyway when the address says so, and stores nothing", async () => {
  stubNarrowContainer();
  mountMap("/app/modules/palace?view=spatial");

  expect(await screen.findByTestId("palace-building")).toBeTruthy();
  expect(screen.queryByText(/O prédio não cabe nesta tela/)).toBeNull();

  const keys = Object.keys(window.localStorage);
  expect(keys.some((k) => /palace|view|spatial|building/i.test(k))).toBe(false);
});

it("measures a box that holds nothing, so the verdict cannot feed itself", async () => {
  // ══════════════════════════════════════════════════════════════════
  //   THIS TEST HAS TO NAME THE OBSERVED ELEMENT, NOT JUST DESCRIBE IT
  // ══════════════════════════════════════════════════════════════════
  //
  // An earlier version asserted only the SHAPE — an empty measurer, an
  // absolute presenter, the two of them siblings — and it passed against
  // a mutation that moved the `ref` onto the container that holds the
  // presenter, which is precisely the N3 feedback loop. The shape stayed
  // correct while the measurement became wrong.
  const watched: Element[] = [];
  vi.stubGlobal(
    "ResizeObserver",
    class {
      cb: ResizeObserverCallback;
      constructor(cb: ResizeObserverCallback) {
        this.cb = cb;
      }
      observe(el: Element) {
        watched.push(el);
        this.cb(
          [{ contentRect: { width: 1400, height: 900 } } as ResizeObserverEntry],
          this as unknown as ResizeObserver,
        );
      }
      unobserve() {}
      disconnect() {}
    },
  );

  mountMap();
  await screen.findByTestId("palace-building");

  const area = screen.getByTestId("building-container");
  const measure = screen.getByTestId("building-measure");
  const presenter = screen.getByTestId("building-presenter");

  expect(watched).toHaveLength(1);
  // The one assertion the mutation could not survive.
  expect(watched[0]).toBe(measure);
  expect(watched[0]).not.toBe(area);
  expect(watched[0].contains(presenter)).toBe(false);

  expect(measure.childElementCount).toBe(0);
  expect(measure.textContent).toBe("");
  expect(presenter.contains(measure)).toBe(false);
  for (const el of [measure, presenter]) {
    expect(el.className).toContain("absolute");
    expect(el.className).toContain("inset-0");
  }
});

it("shapes the stage from its own width, never from what is in it", async () => {
  // ══════════════════════════════════════════════════════════════════
  //   A WINDOW-DERIVED RULE, NOT A CONTENT-DERIVED ONE
  // ══════════════════════════════════════════════════════════════════
  //
  // The stage takes an aspect close to the house's so the fit stops
  // wasting a third of the frame on letterbox. That is only safe while
  // the ratio is a constant: the moment it followed the room count, the
  // measured box would move with the content and N3's loop would be back
  // by another door.
  mountMap();
  await screen.findByTestId("palace-building");
  // Asserted on the class rather than the computed style: the shape is a
  // responsive rule, and jsdom applies no stylesheet.
  const twoRooms = screen.getByTestId("building-container").className;
  expect(twoRooms).toContain("aspect-[1.78/1]");

  cleanup();
  respond = (url) => {
    if (url.includes("/palace/overview")) {
      return {
        rooms: [{ ...ROOM_A, memory_count: 0 }],
        room_total: 1,
        unfiled: { artifact_count: 0, archived_count: 0 },
      };
    }
    if (url.includes("/palace/artifacts")) {
      return { items: [ARTIFACTS[0]], total: 1, limit: 100, offset: 0 };
    }
    return { items: [], total: 0, limit: 25, offset: 0 };
  };
  mountMap();
  await screen.findByTestId("palace-building");
  expect(screen.getByTestId("building-container").className).toBe(twoRooms);
});

it("opens the inspector beside the object rather than in a fixed corner", async () => {
  const user = userEvent.setup();
  mountMap();
  await screen.findByTestId("palace-building");

  const first = screen.getByRole("button", {
    name: `Lista: Ferramentas & Insumos, em ${ROOM_A.name}`,
  });
  await user.click(first);
  const anchorA = screen.getByTestId("house-inspector-anchor").style.left;

  await user.keyboard("{Escape}");
  await waitFor(() => expect(screen.queryByTestId("house-inspector")).toBeNull());

  const second = screen.getByRole("button", {
    name: `Nota: Uma nota na parede, em ${ROOM_A.name}`,
  });
  await user.click(second);
  const anchorB = screen.getByTestId("house-inspector-anchor").style.left;

  // Two different objects put the panel in two different places. A panel
  // pinned to the bottom-right whatever was pressed is the "generic panel
  // over the Palace" the human gate reported.
  expect(anchorA).toBeTruthy();
  expect(anchorB).toBeTruthy();
  expect(anchorA).not.toBe(anchorB);
});

it("does not let the inspector take height from the measured area", async () => {
  // N3's second defect, one scale up: an in-flow panel would shrink the
  // area, D5 would hand over, and the object just activated would vanish
  // taking the focus with it.
  const user = userEvent.setup();
  mountMap();
  await screen.findByTestId("palace-building");

  await user.click(screen.getByRole("button", { name: `Nota: Uma nota na parede, em ${ROOM_A.name}` }));
  const panel = await screen.findByTestId("house-inspector");

  const area = screen.getByTestId("building-container");
  expect(area.contains(panel)).toBe(true);
  expect(panel.parentElement!.className).toContain("absolute");
  // The house is still drawn, and the object is still there.
  expect(screen.getByTestId("palace-building")).toBeTruthy();
  expect(screen.getByTestId("building-measure").childElementCount).toBe(0);
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
