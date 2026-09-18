// @vitest-environment jsdom

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { I18nFixture } from "@/lib/i18n/testing";
import { MIN_TARGET_PX, RESTORE_TARGET_PX } from "@/modules/palace/scene/fit";

import { RoomView } from "./RoomView";

/**
 * The fit verdict must not be an input to itself.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   WHAT IS MEASURED MUST NOT CONTAIN WHAT THE MEASUREMENT DECIDES
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * ── The loop ───────────────────────────────────────────────────────────
 * The `ResizeObserver` watched the box that held the presenter. Falling
 * back to the Library changed that box's height, the next measurement came
 * from the changed box, and the decision fed itself. Measured in Chrome:
 * 1440x900 gave a 762px-tall box in spatial and a 640px-tall box in
 * library, so a window that had just supported the room refused to support
 * it again until it grew past where it started.
 *
 * ── What these tests can and cannot see ────────────────────────────────
 * jsdom performs NO layout: nothing here has a width, a height or a
 * position, and the sizes below are fed to the observer by hand. So these
 * cannot prove the geometry — they prove the STRUCTURE that makes the
 * geometry impossible to corrupt: that the observed element is empty, that
 * it is not the element the presenter lives in, and that it survives a
 * verdict flip unchanged.
 *
 * The geometric acceptance — "a window that supported the room supports it
 * again after a fallback, at the SAME size" — is proven in a real browser,
 * where a box actually has a height.
 */

afterEach(cleanup);

const ROOM = {
  room_id: "aaaaaaaa-1111-1111-1111-111111111111",
  name: "Ateliê de Marcenaria",
  description: "O que está em construção.",
  status: "active" as const,
  sensitivity: "normal" as const,
  created_at: "2026-09-01T00:00:00Z",
  updated_at: "2026-09-20T00:00:00Z",
  artifact_count: 2,
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
    room_id: ROOM.room_id,
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
    room_id: ROOM.room_id,
    item_count: 0,
    item_done_count: 0,
    body_excerpt: "",
    body_truncated: false,
    created_at: "2026-09-03T00:00:00Z",
    updated_at: "2026-09-03T00:00:00Z",
  },
];

/** A window that comfortably fits the room, and one that cannot. */
const LARGE = { width: 1400, height: 900 };
const SMALL = { width: 180, height: 140 };

/* ── a ResizeObserver a test can drive ──────────────────────────────── */

type Watcher = { el: Element; cb: ResizeObserverCallback };
let watchers: Watcher[] = [];
let current = LARGE;

function emit(size: { width: number; height: number }) {
  current = size;
  act(() => {
    for (const w of watchers) {
      w.cb(
        [{ contentRect: { ...size } } as ResizeObserverEntry],
        null as unknown as ResizeObserver,
      );
    }
  });
}

beforeEach(() => {
  watchers = [];
  current = LARGE;

  vi.stubGlobal(
    "ResizeObserver",
    class {
      cb: ResizeObserverCallback;
      constructor(cb: ResizeObserverCallback) {
        this.cb = cb;
      }
      observe(el: Element) {
        watchers.push({ el, cb: this.cb });
        this.cb(
          [{ contentRect: { ...current } } as ResizeObserverEntry],
          this as unknown as ResizeObserver,
        );
      }
      unobserve() {}
      disconnect() {}
    },
  );

  Element.prototype.getBoundingClientRect = function () {
    return {
      width: current.width,
      height: current.height,
      top: 0,
      left: 0,
      bottom: current.height,
      right: current.width,
      x: 0,
      y: 0,
      toJSON: () => ({}),
    };
  };

  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      const body = url.includes("/palace/rooms/")
        ? ROOM
        : url.includes("/palace/artifacts")
          ? { items: ARTIFACTS, total: ARTIFACTS.length, limit: 100, offset: 0 }
          : { items: [], total: 0, limit: 25, offset: 0 };
      return new Response(JSON.stringify(body), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    }),
  );
});

afterEach(() => vi.unstubAllGlobals());

function mountRoom(entry = `/app/modules/palace/rooms/${ROOM.room_id}`) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <MemoryRouter initialEntries={[entry]}>
      <QueryClientProvider client={client}>
        <I18nFixture lang="pt">
          <Routes>
            <Route path="/app/modules/palace/rooms/:roomId" element={<RoomView />} />
          </Routes>
        </I18nFixture>
      </QueryClientProvider>
    </MemoryRouter>,
  );
}

const measurer = () => screen.getByTestId("scene-measure");
const presenter = () => screen.getByTestId("scene-presenter");
const isSpatial = () => screen.queryByTestId("room-scene") !== null;
const isLibrary = () =>
  screen.queryByText(/A sala não cabe nesta tela|does not fit this screen/i) !== null;

/* ── the structure ──────────────────────────────────────────────────── */

describe("the measurement source", () => {
  it("is an element that holds nothing, and is not the one the presenter lives in", async () => {
    mountRoom();
    await screen.findByTestId("room-scene");

    // Exactly one element is observed, and it is the empty one.
    expect(watchers).toHaveLength(1);
    expect(watchers[0].el).toBe(measurer());
    expect(measurer().childElementCount).toBe(0);
    expect(measurer().textContent).toBe("");
    expect(measurer()).not.toBe(presenter());
    expect(presenter().contains(measurer())).toBe(false);
  });

  it("keeps the same element, still empty, when the verdict flips to Library", async () => {
    mountRoom();
    await screen.findByTestId("room-scene");
    const observedBefore = watchers[0].el;

    emit(SMALL);
    expect(isLibrary()).toBe(true);
    expect(isSpatial()).toBe(false);

    // The presenter changed. The measured box did not: same node, still
    // empty. Whatever the Library draws, it cannot be what is measured.
    expect(watchers[0].el).toBe(observedBefore);
    expect(watchers[0].el).toBe(measurer());
    expect(measurer().childElementCount).toBe(0);
    expect(measurer().textContent).toBe("");
  });

  it("is out of flow, and so is the presenter, so neither can resize the area", async () => {
    mountRoom();
    await screen.findByTestId("room-scene");

    // The area's only children are two absolutely positioned boxes. With
    // no in-flow child it has no content height to grow by, which is the
    // structural reason the measurement cannot be moved by the verdict.
    const area = screen.getByTestId("scene-container");
    expect([...area.children]).toEqual([measurer(), presenter()]);
    for (const el of [measurer(), presenter()]) {
      expect(el.className).toContain("absolute");
      expect(el.className).toContain("inset-0");
    }
  });

  it("does not let the inspector take height from the area it is measured in", async () => {
    const user = userEvent.setup();
    mountRoom();
    await screen.findByTestId("room-scene");

    // The hit layer arrives once there is a fit to place it with, so it is
    // awaited by name rather than read straight out of the DOM.
    const object = await screen.findByRole("button", { name: /Nota: Uma nota na parede/ });
    object.focus();
    await user.keyboard("{Enter}");
    const dialog = await screen.findByRole("dialog");

    // The panel lives INSIDE the stable area and is out of flow. As a
    // sibling below the scene it was an in-flow card: the column gave it
    // height, the area measured smaller, D5 handed over, and the object
    // that had just been activated disappeared with the focus on it.
    const area = screen.getByTestId("scene-container");
    expect(area.contains(dialog)).toBe(true);
    expect(dialog.parentElement!.className).toContain("absolute");

    // The room is still there, behind it, and so is the object.
    expect(isSpatial()).toBe(true);
    expect(document.querySelectorAll("[data-hit-key]").length).toBeGreaterThan(0);
    expect(measurer().childElementCount).toBe(0);
    expect(watchers[0].el).toBe(measurer());
  });
});

/* ── the verdict, driven by size alone ──────────────────────────────── */

describe("the verdict", () => {
  it("hands over to the Library when the targets would be too small", async () => {
    mountRoom();
    await screen.findByTestId("room-scene");

    emit(SMALL);
    expect(isLibrary()).toBe(true);
  });

  it("supports the room again at the SAME size that supported it before", async () => {
    mountRoom();
    await screen.findByTestId("room-scene");
    expect(isSpatial()).toBe(true);

    emit(SMALL);
    expect(isLibrary()).toBe(true);

    // The acceptance, in the only form jsdom can state it: the same size,
    // not a larger one. In a browser this is where the old shape failed,
    // because the box it measured had been shrunk by the Library.
    emit(LARGE);
    expect(isSpatial()).toBe(true);
    expect(isLibrary()).toBe(false);
  });

  it("still obeys an explicit request for the room, at any size", async () => {
    mountRoom(`/app/modules/palace/rooms/${ROOM.room_id}?view=spatial`);
    await screen.findByTestId("room-scene");

    emit(SMALL);
    // The rule protects somebody from a surface they cannot use. It does
    // not overrule them when they ask for it.
    expect(isSpatial()).toBe(true);
    expect(isLibrary()).toBe(false);
    // And the override lives in the URL only: nothing about the verdict,
    // the view or the room is written to storage.
    const stored = Object.keys(window.localStorage);
    expect(stored.filter((k) => /palace|view|spatial|scene/i.test(k))).toEqual([]);
  });

  it("leaves the thresholds exactly where D5 put them", () => {
    // This slice changed where the measurement comes from. It did not
    // change the numbers, and the hysteresis arithmetic stays proven in
    // `scene/scene.test.ts` against the pure `fitScene`.
    expect(MIN_TARGET_PX).toBe(44);
    expect(RESTORE_TARGET_PX).toBe(52);
  });
});
