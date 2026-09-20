// @vitest-environment jsdom

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

import { I18nFixture } from "@/lib/i18n/testing";

import { PalaceMap } from "./PalaceMap";

/**
 * The furnishing, as the browser receives it.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   ATMOSPHERE MUST BE UNREACHABLE, SILENT AND FREE
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * `furnishing.test.ts` proves the system on its own. These prove the part
 * only a rendered tree can: that not one decorative node is focusable,
 * named, pressable or counted, that the D5 verdict still measures the
 * things a finger has to hit, and that a Palace full of furniture asks the
 * backend for exactly what a bare one asked for.
 *
 * What they deliberately do NOT ask is whether it looks good. jsdom has no
 * layout and no paint; the picture is the operator's gate.
 */

afterEach(cleanup);

const ROOM_A = {
  room_id: "aaaaaaaa-1111-1111-1111-111111111111",
  name: "Ateliê de Marcenaria",
  description: "O que está em construção.",
  sensitivity: "normal" as const,
  created_at: "2026-09-01T00:00:00Z",
  updated_at: "2026-09-20T00:00:00Z",
  artifact_count: 1,
  memory_count: 0,
  archived_count: 0,
};
const ROOM_B = {
  ...ROOM_A,
  room_id: "bbbbbbbb-2222-2222-2222-222222222222",
  name: "Dojang",
  created_at: "2026-09-05T00:00:00Z",
  artifact_count: 0,
  memory_count: 0,
};

const ARTIFACT = {
  artifact_id: "art-1",
  kind: "list" as const,
  title: "Ferramentas & Insumos",
  status: "active" as const,
  sensitivity: "normal" as const,
  room_id: ROOM_A.room_id,
  item_count: 2,
  item_done_count: 0,
  body_excerpt: "",
  body_truncated: false,
  created_at: "2026-09-02T00:00:00Z",
  updated_at: "2026-09-02T00:00:00Z",
};

let fetchMock: ReturnType<typeof vi.fn>;
let overview: unknown;
let artifacts: unknown;

function stubContainer(width: number, height: number) {
  vi.stubGlobal(
    "ResizeObserver",
    class {
      cb: ResizeObserverCallback;
      constructor(cb: ResizeObserverCallback) {
        this.cb = cb;
      }
      observe() {
        this.cb(
          [{ contentRect: { width, height } } as ResizeObserverEntry],
          this as unknown as ResizeObserver,
        );
      }
      unobserve() {}
      disconnect() {}
    },
  );
  Element.prototype.getBoundingClientRect = function () {
    return {
      width,
      height,
      top: 0,
      left: 0,
      bottom: height,
      right: width,
      x: 0,
      y: 0,
      toJSON: () => ({}),
    };
  };
}

beforeEach(() => {
  stubContainer(1400, 900);
  overview = {
    rooms: [ROOM_B, ROOM_A],
    room_total: 2,
    unfiled: { artifact_count: 0, archived_count: 0 },
  };
  artifacts = { items: [ARTIFACT], total: 1, limit: 100, offset: 0 };

  fetchMock = vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    const body = url.includes("/palace/overview")
      ? overview
      : url.includes("/palace/artifacts")
        ? artifacts
        : { items: [], total: 0, limit: 25, offset: 0 };
    return new Response(JSON.stringify(body), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    });
  });
  vi.stubGlobal("fetch", fetchMock);
});

afterEach(() => vi.unstubAllGlobals());

function mountMap() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <MemoryRouter initialEntries={["/app/modules/palace"]}>
      <QueryClientProvider client={client}>
        <I18nFixture lang="pt">
          <Routes>
            <Route path="/app/modules/palace" element={<PalaceMap />} />
          </Routes>
        </I18nFixture>
      </QueryClientProvider>
    </MemoryRouter>,
  );
}

const decor = () => [...document.querySelectorAll("[data-decor]")];
const hits = () => [...document.querySelectorAll("[data-hit-key]")];

/* ══════════════════════════════════════════════════════════════════════
   The Palace is furnished at all
   ══════════════════════════════════════════════════════════════════════ */

it("furnishes every room it draws", async () => {
  mountMap();
  await screen.findByTestId("palace-building");

  expect(decor().length).toBeGreaterThan(0);
  // Both rooms, each with its own furniture, each piece inside the painted
  // layer rather than floating somewhere of its own.
  const rooms = new Set(decor().map((g) => g.getAttribute("data-paint-key")?.split(":")[1]));
  expect(rooms).toEqual(new Set([ROOM_A.room_id, ROOM_B.room_id]));
  expect(document.querySelectorAll("[data-testid='building-rug']").length).toBeGreaterThan(0);
  expect(document.querySelectorAll("[data-testid='building-trim']").length).toBeGreaterThan(0);
});

/* ══════════════════════════════════════════════════════════════════════
   D. Decoration never becomes interactive
   ══════════════════════════════════════════════════════════════════════ */

it("gives decoration nothing a reader could press, reach or hear", async () => {
  mountMap();
  await screen.findByTestId("palace-building");

  for (const piece of decor()) {
    // Not a control, not focusable, not named, not addressable.
    expect(piece.querySelector("button, a, input, [tabindex]")).toBeNull();
    expect(piece.getAttribute("tabindex")).toBeNull();
    expect(piece.getAttribute("aria-label")).toBeNull();
    expect(piece.getAttribute("role")).toBeNull();
    expect(piece.hasAttribute("data-hit-key")).toBe(false);
    // And it lives inside the layer that is hidden from assistive
    // technology and takes no pointer events, as a whole.
    expect(piece.closest("[aria-hidden='true']")).not.toBeNull();
  }

  // The interactive layer is exactly the semantic objects: one button per
  // real thing, and the count matches the Palace, not the furniture.
  expect(hits()).toHaveLength(1);
  expect(hits()[0].getAttribute("data-item-type")).toBe("artifact");
  expect(screen.getAllByRole("button").filter((b) => b.hasAttribute("data-hit-key"))).toHaveLength(1);
});

it("keeps the rug and the trim out of the interactive tree too", async () => {
  mountMap();
  await screen.findByTestId("palace-building");

  for (const id of ["building-rug", "building-trim"]) {
    for (const node of document.querySelectorAll(`[data-testid='${id}']`)) {
      expect(node.closest("[aria-hidden='true']")).not.toBeNull();
      expect(node.querySelector("button, a, [tabindex]")).toBeNull();
    }
  }
});

/* ══════════════════════════════════════════════════════════════════════
   I · H. Meaning wins, and semantic objects never move
   ══════════════════════════════════════════════════════════════════════ */

it("gives up a decorative position when a real object needs it", async () => {
  mountMap();
  await screen.findByTestId("palace-building");

  const bare = decor().filter((g) => g.getAttribute("data-paint-key")?.includes(ROOM_B.room_id));
  const before = hits()
    .map((h) => h.getAttribute("data-hit-key"))
    .sort();
  cleanup();

  /*
    The same Palace, with ROOM_B now holding six artifacts, more than fit
    individually (so it has a pile) and at least one memory (so it has a
    surface). Eight of its nine positions are spoken for.

    The count matters: decoration only has to yield when fewer than three
    positions are left, because three is the ceiling. A room with four
    artifacts still has five free positions and is still fully furnished,
    which is the correct behaviour and would make a weaker test pass
    without proving anything.
  */
  overview = {
    rooms: [{ ...ROOM_B, artifact_count: 8, memory_count: 1 }, ROOM_A],
    room_total: 2,
    unfiled: { artifact_count: 0, archived_count: 0 },
  };
  artifacts = {
    items: [
      ARTIFACT,
      ...Array.from({ length: 6 }, (_, i) => ({
        ...ARTIFACT,
        artifact_id: `b-${i}`,
        room_id: ROOM_B.room_id,
        created_at: `2026-09-1${i}T00:00:00Z`,
      })),
    ],
    total: 7,
    limit: 100,
    offset: 0,
  };

  mountMap();
  await screen.findByTestId("palace-building");
  // six objects, a pile and a memory surface in ROOM_B, plus ROOM_A's one
  await waitFor(() => expect(hits().length).toBe(9));

  const after = decor().filter((g) => g.getAttribute("data-paint-key")?.includes(ROOM_B.room_id));
  // The populated room gave up furniture...
  expect(after.length).toBeLessThan(bare.length);
  // ...and every object that was already drawn is still drawn.
  for (const key of before) {
    expect(hits().some((h) => h.getAttribute("data-hit-key") === key)).toBe(true);
  }
});

/* ══════════════════════════════════════════════════════════════════════
   J. No side-channel
   ══════════════════════════════════════════════════════════════════════ */

it("draws no furniture at all for a room the surface withheld", async () => {
  // A withheld room never reaches this surface: no shell, so no floor, no
  // rug, no trim, no furniture, and no gap where a room would have been.
  overview = {
    rooms: [ROOM_A],
    room_total: 1,
    unfiled: { artifact_count: 0, archived_count: 0 },
  };
  mountMap();
  await screen.findByTestId("palace-building");

  expect(document.querySelectorAll("[data-room-shell]")).toHaveLength(1);
  const rooms = new Set(decor().map((g) => g.getAttribute("data-paint-key")?.split(":")[1]));
  expect(rooms).toEqual(new Set([ROOM_A.room_id]));
  expect(document.body.innerHTML).not.toContain(ROOM_B.room_id);
});

it("says nothing in the furniture about the room it stands in", async () => {
  mountMap();
  await screen.findByTestId("palace-building");

  // The only identifier a decorative node carries is the room's own id, in
  // a paint key — the same id the shell beside it already carries. No
  // name, no count, no sensitivity, no artifact id anywhere.
  for (const piece of decor()) {
    const html = piece.outerHTML;
    expect(html).not.toContain(ROOM_A.name);
    expect(html).not.toContain(ROOM_B.name);
    expect(html).not.toContain(ARTIFACT.title);
    expect(html).not.toContain(ARTIFACT.artifact_id);
  }
});

/* ══════════════════════════════════════════════════════════════════════
   K. Free
   ══════════════════════════════════════════════════════════════════════ */

it("asks the backend for nothing on behalf of the furniture", async () => {
  mountMap();
  await screen.findByTestId("palace-building");
  await waitFor(() => expect(decor().length).toBeGreaterThan(0));

  // Exactly the two reads the house has always made: the overview and one
  // page of artifacts. No asset, no per-room request, no N+1.
  const urls = fetchMock.mock.calls.map((c) => String(c[0]));
  expect(urls).toHaveLength(2);
  expect(urls.some((u) => u.includes("/palace/overview"))).toBe(true);
  expect(urls.filter((u) => u.includes("/palace/artifacts")).length).toBe(1);
});

/* ══════════════════════════════════════════════════════════════════════
   L. D5 still measures what a finger has to hit
   ══════════════════════════════════════════════════════════════════════ */

it("hands over to the list on the semantic targets, never on the furniture", async () => {
  stubContainer(180, 260);
  mountMap();

  // Below the floor the spatial surface is gone entirely — and with it all
  // of its decoration, because the fallback is a list of rooms and a list
  // has no floor to furnish.
  await waitFor(() => expect(screen.getByText("O prédio não cabe nesta tela")).toBeTruthy());
  expect(decor()).toHaveLength(0);
  expect(document.querySelectorAll("[data-testid='building-rug']")).toHaveLength(0);
  expect(screen.queryByTestId("palace-building")).toBeNull();
});

/* ══════════════════════════════════════════════════════════════════════
   Focus is unaffected
   ══════════════════════════════════════════════════════════════════════ */

it("keeps furniture out of the tab order in every state", async () => {
  const user = userEvent.setup();
  mountMap();
  await screen.findByTestId("palace-building");

  await user.click(screen.getByRole("button", { name: /Lista:/ }));
  const panel = await screen.findByTestId("house-inspector");
  await user.click(panel.querySelector("[data-testid='focus-room-action']")!);
  await screen.findByTestId("palace-focus-bar");

  // Focused, with a room brought forward: still no decorative tab stop,
  // and the furniture of the background room is dimmed with its room
  // rather than being removed from the Palace.
  for (const piece of decor()) expect(piece.getAttribute("tabindex")).toBeNull();
  expect(decor().length).toBeGreaterThan(0);
  const dimmed = decor().filter((g) => Number(g.getAttribute("opacity")) < 1);
  expect(dimmed.length).toBeGreaterThan(0);
});
