// @vitest-environment jsdom

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

import { I18nFixture } from "@/lib/i18n/testing";

import { PalaceMap } from "./PalaceMap";

/**
 * Overview ↔ focus, driven against the real page.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   THE CLAIM UNDER TEST: ATTENTION MOVED, THE PALACE DID NOT
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * Every one of these asks a behavioural question — does the world move,
 * is the rest of the Palace still drawn, does the address change, where
 * does focus land, does the backend get asked anything new — and none of
 * them asks whether the picture is nice, which is the human gate's job.
 *
 * ── What jsdom cannot answer, stated rather than faked ─────────────────
 * There is no layout engine here, so nothing below measures a pixel. What
 * it CAN see is the transform the page applied, the `viewBox` the drawing
 * kept, which elements exist, what is in the tab order, and which
 * requests went out. The apparent size of a focused room, whether the
 * camera move looks like a camera move, and whether the reduced-motion
 * media query is honoured by the browser are measured in Chrome and
 * recorded in the private checkpoint.
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
  memory_count: 0,
  archived_count: 0,
};
const ROOM_B = {
  ...ROOM_A,
  room_id: "bbbbbbbb-2222-2222-2222-222222222222",
  name: "Dojang",
  created_at: "2026-09-05T00:00:00Z",
  updated_at: "2026-09-30T00:00:00Z",
  artifact_count: 1,
  memory_count: 0,
};

const ARTIFACTS = [
  {
    artifact_id: "art-1",
    kind: "list" as const,
    title: "Ferramentas & Insumos",
    status: "active" as const,
    sensitivity: "normal" as const,
    room_id: ROOM_A.room_id,
    item_count: 1,
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
  {
    artifact_id: "art-3",
    kind: "plan" as const,
    title: "Faixa preta",
    status: "active" as const,
    sensitivity: "normal" as const,
    room_id: ROOM_B.room_id,
    item_count: 0,
    item_done_count: 0,
    body_excerpt: "",
    body_truncated: false,
    created_at: "2026-09-06T00:00:00Z",
    updated_at: "2026-09-06T00:00:00Z",
  },
];

const DETAIL = {
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

const OBJECT_A = `Nota: Uma nota na parede, em ${ROOM_A.name}`;
const LIST_A = `Lista: Ferramentas & Insumos, em ${ROOM_A.name}`;
const OBJECT_B = `Plano: Faixa preta, em ${ROOM_B.name}`;

let fetchMock: ReturnType<typeof vi.fn>;

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

  fetchMock = vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    const body = url.includes("/palace/overview")
      ? {
          rooms: [ROOM_B, ROOM_A],
          room_total: 2,
          unfiled: { artifact_count: 0, archived_count: 0 },
        }
      : url.includes("/palace/artifacts/")
        ? DETAIL
        : url.includes("/palace/artifacts")
          ? { items: ARTIFACTS, total: ARTIFACTS.length, limit: 100, offset: 0 }
          : { items: [], total: 0, limit: 25, offset: 0 };
    return new Response(JSON.stringify(body), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    });
  });
  vi.stubGlobal("fetch", fetchMock);
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

const camera = () => screen.getByTestId("palace-camera");
const transform = () => camera().style.transform;
const viewBox = () => screen.getByTestId("palace-building").getAttribute("viewBox");
const objects = () =>
  screen.getAllByRole("button").filter((b) => b.hasAttribute("data-hit-key"));

/** Opens an object and focuses the room it stands in, from its inspector. */
async function focusRoomOf(user: ReturnType<typeof userEvent.setup>, objectName: string) {
  await user.click(screen.getByRole("button", { name: objectName }));
  const panel = await screen.findByTestId("house-inspector");
  await user.click(within(panel).getByTestId("focus-room-action"));
  await screen.findByTestId("palace-focus-bar");
}

/* ══════════════════════════════════════════════════════════════════════
   A · B. Focus is a camera. The world does not move
   ══════════════════════════════════════════════════════════════════════ */

it("starts looking at the whole Palace, with no transform at all", async () => {
  mountMap();
  await screen.findByTestId("palace-building");

  expect(transform()).toBe("translate(0px, 0px) scale(1)");
  expect(screen.queryByTestId("palace-focus-bar")).toBeNull();
});

it("moves the camera without moving a single thing in the world", async () => {
  const user = userEvent.setup();
  mountMap();
  await screen.findByTestId("palace-building");

  // Everything the geometry produced, before.
  const box = viewBox();
  const shells = [...document.querySelectorAll("[data-room-shell]")].map((el) =>
    el.getAttribute("data-room-shell"),
  );
  const positions = objects().map((b) => `${b.dataset.hitKey}@${b.style.left},${b.style.top}`);

  await focusRoomOf(user, OBJECT_A);

  // ══════════════════════════════════════════════════════════════════
  //   THE PROOF, AND IT IS ONE LINE: THE viewBox DID NOT CHANGE
  // ══════════════════════════════════════════════════════════════════
  //
  // The `viewBox` is `buildingBounds` of the placement. If focusing had
  // recomputed a layout, re-fitted the scene, or "zoomed" by narrowing the
  // box to one room, this would differ. It does not: the drawing is the
  // same drawing, and a transform was applied over it.
  expect(viewBox()).toBe(box);
  expect(
    [...document.querySelectorAll("[data-room-shell]")].map((el) =>
      el.getAttribute("data-room-shell"),
    ),
  ).toEqual(shells);
  expect(objects().map((b) => `${b.dataset.hitKey}@${b.style.left},${b.style.top}`)).toEqual(
    positions,
  );

  // And the camera is no longer the identity.
  expect(transform()).not.toBe("translate(0px, 0px) scale(1)");
  expect(Number(camera().dataset.cameraScale)).toBeGreaterThan(1);
});

it("keeps the address of the Palace, focused or not", async () => {
  const user = userEvent.setup();
  mountMap();
  await screen.findByTestId("palace-building");

  await focusRoomOf(user, OBJECT_A);
  expect(screen.getByTestId("where").textContent).toBe("/app/modules/palace");
});

/* ══════════════════════════════════════════════════════════════════════
   C. The rest of the Palace is still there
   ══════════════════════════════════════════════════════════════════════ */

it("keeps every other room drawn, quieter and still in place", async () => {
  const user = userEvent.setup();
  mountMap();
  await screen.findByTestId("palace-building");

  await focusRoomOf(user, OBJECT_A);

  // Both rooms, both still drawn. Not hidden, not unmounted, not turned
  // into cards: the reader has to keep the answer to "where in my Palace
  // is this", and the answer is the architecture around it.
  expect(screen.getAllByTestId("building-room")).toHaveLength(2);
  expect(screen.getByRole("button", { name: OBJECT_B })).toBeTruthy();

  // Quieter, and legibly so rather than nearly gone.
  const shellGroups = [...document.querySelectorAll("[data-room-shell]")].map(
    (el) => el.parentElement!,
  );
  const opacities = shellGroups.map((g) => Number(g.getAttribute("opacity")));
  expect(opacities).toContain(1);
  for (const value of opacities) expect(value).toBeGreaterThanOrEqual(0.5);
});

it("narrows the tab order to the focused room, and gives it back on return", async () => {
  const user = userEvent.setup();
  mountMap();
  await screen.findByTestId("palace-building");

  expect(objects().every((b) => b.tabIndex === 0)).toBe(true);

  await focusRoomOf(user, OBJECT_A);

  const far = screen.getByRole("button", { name: OBJECT_B });
  expect(far.tabIndex).toBe(-1);
  expect(screen.getByRole("button", { name: OBJECT_A }).tabIndex).toBe(0);
  // Still drawn and still named: out of the sequence is not hidden.
  expect(far.getAttribute("aria-label")).toBe(OBJECT_B);

  await user.click(screen.getByTestId("palace-overview-button"));
  await waitFor(() => expect(screen.queryByTestId("palace-focus-bar")).toBeNull());
  expect(objects().every((b) => b.tabIndex === 0)).toBe(true);
});

/* ══════════════════════════════════════════════════════════════════════
   D. Coming back
   ══════════════════════════════════════════════════════════════════════ */

it("restores the overview transform exactly", async () => {
  const user = userEvent.setup();
  mountMap();
  await screen.findByTestId("palace-building");
  const before = transform();

  await focusRoomOf(user, OBJECT_A);
  await user.click(screen.getByTestId("palace-overview-button"));

  await waitFor(() => expect(transform()).toBe(before));
  expect(transform()).toBe("translate(0px, 0px) scale(1)");
});

it("frames another room from the same overview", async () => {
  const user = userEvent.setup();
  mountMap();
  await screen.findByTestId("palace-building");

  await focusRoomOf(user, OBJECT_A);
  const first = transform();

  await user.click(screen.getByTestId("palace-overview-button"));
  await waitFor(() => expect(screen.queryByTestId("palace-focus-bar")).toBeNull());
  await focusRoomOf(user, OBJECT_B);

  expect(transform()).not.toBe(first);
  expect(viewBox()).toBe(screen.getByTestId("palace-building").getAttribute("viewBox"));
  expect(screen.getAllByTestId("building-room")).toHaveLength(2);
});

it("moves attention straight from one room to the next", async () => {
  const user = userEvent.setup();
  mountMap();
  await screen.findByTestId("palace-building");

  await focusRoomOf(user, OBJECT_A);
  const first = transform();

  // An object of ANOTHER room is still drawn and still pressable, and its
  // panel still offers that room. So attention can move sideways without
  // going back to the overview first.
  await user.click(screen.getByRole("button", { name: OBJECT_B }));
  const panel = await screen.findByTestId("house-inspector");
  await user.click(within(panel).getByTestId("focus-room-action"));

  await waitFor(() => expect(transform()).not.toBe(first));
  expect(screen.getByTestId("palace-focus-status").textContent).toContain(ROOM_B.name);
  expect(screen.getAllByTestId("building-room")).toHaveLength(2);
  expect(screen.getByTestId("where").textContent).toBe("/app/modules/palace");
});

it("offers no way to look at the room the reader is already looking at", async () => {
  // An affordance that would do nothing. The house refuses those in the
  // domain — no cabinet without a list — and refuses this one for the same
  // reason.
  const user = userEvent.setup();
  mountMap();
  await screen.findByTestId("palace-building");

  await focusRoomOf(user, OBJECT_A);
  await user.click(screen.getByRole("button", { name: LIST_A }));
  const panel = await screen.findByTestId("house-inspector");

  expect(within(panel).queryByTestId("focus-room-action")).toBeNull();
  // The other actions are untouched.
  expect(within(panel).getByRole("link", { name: /Abrir detalhes/ })).toBeTruthy();
});

/* ══════════════════════════════════════════════════════════════════════
   E · F. Artifacts while focused: same objects, same inspector
   ══════════════════════════════════════════════════════════════════════ */

it("opens the same inspector from a focused room, in place", async () => {
  const user = userEvent.setup();
  mountMap();
  await screen.findByTestId("palace-building");

  await focusRoomOf(user, OBJECT_A);
  await user.click(screen.getByRole("button", { name: LIST_A }));

  const panel = await screen.findByTestId("house-inspector");
  expect(panel.getAttribute("role")).toBe("dialog");
  // Still focused, still the whole Palace behind it, still no navigation.
  expect(screen.getByTestId("palace-focus-bar")).toBeTruthy();
  expect(screen.getAllByTestId("building-room")).toHaveLength(2);
  expect(screen.getByTestId("where").textContent).toBe("/app/modules/palace");
});

it("keeps the panel out of the transformed layer, so its type never zooms", async () => {
  // ══════════════════════════════════════════════════════════════════
  //   WORLD LAYER AND INSPECTION LAYER ARE DIFFERENT LAYERS
  // ══════════════════════════════════════════════════════════════════
  //
  // A panel of prose inside a 2x camera is a panel of unreadable prose.
  // It is positioned in stage coordinates and told where its object went,
  // which is why it can be anchored to the object and still be set in the
  // same type at every zoom.
  const user = userEvent.setup();
  mountMap();
  await screen.findByTestId("palace-building");

  await focusRoomOf(user, OBJECT_A);
  await user.click(screen.getByRole("button", { name: LIST_A }));
  const panel = await screen.findByTestId("house-inspector");

  expect(camera().contains(panel)).toBe(false);
  expect(screen.getByTestId("house-inspector-anchor").style.transform).toBe("");
});

/* ══════════════════════════════════════════════════════════════════════
   G. Escape precedence
   ══════════════════════════════════════════════════════════════════════ */

it("closes the inspector on the first Escape and returns to the overview on the second", async () => {
  const user = userEvent.setup();
  mountMap();
  await screen.findByTestId("palace-building");

  await focusRoomOf(user, OBJECT_A);
  const object = screen.getByRole("button", { name: LIST_A });
  await user.click(object);
  await screen.findByTestId("house-inspector");

  // First Escape: the panel closes, focus returns to the exact object,
  // and the camera does NOT move. One press undoes one thing.
  const focused = transform();
  await user.keyboard("{Escape}");
  await waitFor(() => expect(screen.queryByTestId("house-inspector")).toBeNull());
  await waitFor(() => expect(document.activeElement).toBe(object));
  expect(screen.getByTestId("palace-focus-bar")).toBeTruthy();
  expect(transform()).toBe(focused);

  // Second Escape: back to the whole Palace.
  await user.keyboard("{Escape}");
  await waitFor(() => expect(screen.queryByTestId("palace-focus-bar")).toBeNull());
  expect(transform()).toBe("translate(0px, 0px) scale(1)");
});

it("does nothing on Escape in the overview", async () => {
  const user = userEvent.setup();
  mountMap();
  await screen.findByTestId("palace-building");

  await user.click(screen.getByRole("button", { name: OBJECT_A }));
  await screen.findByTestId("house-inspector");
  await user.keyboard("{Escape}");
  await waitFor(() => expect(screen.queryByTestId("house-inspector")).toBeNull());

  await user.keyboard("{Escape}");
  expect(transform()).toBe("translate(0px, 0px) scale(1)");
  expect(screen.getByTestId("palace-building")).toBeTruthy();
});

/* ══════════════════════════════════════════════════════════════════════
   Accessibility: the state is said, and focus lands somewhere
   ══════════════════════════════════════════════════════════════════════ */

it("says which room is focused, in a region that was already there", async () => {
  const user = userEvent.setup();
  mountMap();
  await screen.findByTestId("palace-building");

  // Present from the start and empty, so the announcement is a change of
  // text rather than a region appearing with text already in it.
  const status = screen.getByTestId("palace-focus-status");
  expect(status.getAttribute("role")).toBe("status");
  expect(status.textContent).toBe("");

  await focusRoomOf(user, OBJECT_A);
  expect(status.textContent).toContain(ROOM_A.name);

  await user.click(screen.getByTestId("palace-overview-button"));
  await waitFor(() => expect(status.textContent).toBe(""));
});

it("offers one way back and no second control beside it", async () => {
  // ══════════════════════════════════════════════════════════════════
  //   CONTEXT AND A WAY OUT, NOT TWO TABS
  // ══════════════════════════════════════════════════════════════════
  //
  // The first version sat the room's name next to the return inside one
  // bordered pill, and it read as a segmented control: two things of
  // equal weight, one of them apparently selected. The Palace has no
  // modes to switch between. The name is context; exactly one thing in
  // here is pressable.
  const user = userEvent.setup();
  mountMap();
  await screen.findByTestId("palace-building");

  await focusRoomOf(user, OBJECT_A);
  const bar = screen.getByTestId("palace-focus-bar");

  expect(within(bar).getAllByRole("button")).toHaveLength(1);
  expect(within(bar).queryAllByRole("link")).toHaveLength(0);

  const name = screen.getByTestId("palace-focus-name");
  expect(name.tagName).toBe("SPAN");
  expect(name.textContent).toBe(ROOM_A.name);
  expect(name.getAttribute("aria-hidden")).toBe("true");
  expect(name.closest("button")).toBeNull();

  // The one action names the room it is leaving, so it is unambiguous
  // even for somebody who never sees the caption under it.
  expect(screen.getByTestId("palace-overview-button").getAttribute("aria-label")).toContain(
    ROOM_A.name,
  );
});

it("puts focus on the way out, and back on the object it came from", async () => {
  const user = userEvent.setup();
  mountMap();
  await screen.findByTestId("palace-building");

  const origin = screen.getByRole("button", { name: OBJECT_A });
  await user.click(origin);
  const panel = await screen.findByTestId("house-inspector");
  await user.click(within(panel).getByTestId("focus-room-action"));

  // The panel the button lived in is gone, so focus has to be placed. It
  // goes to the one control that is always present in this state and is
  // also the way back.
  await waitFor(() =>
    expect(document.activeElement).toBe(screen.getByTestId("palace-overview-button")),
  );

  await user.keyboard("{Escape}");
  await waitFor(() => expect(document.activeElement).toBe(origin));
});

/* ══════════════════════════════════════════════════════════════════════
   J. Focus is free
   ══════════════════════════════════════════════════════════════════════ */

it("asks the backend for nothing at all when the camera moves", async () => {
  const user = userEvent.setup();
  mountMap();
  await screen.findByTestId("palace-building");
  await waitFor(() => expect(fetchMock.mock.calls.length).toBeGreaterThan(0));

  // The inspector reads the artifact it opens, which is C1.1 behaviour and
  // not the camera's. So the count is taken AFTER the panel has settled,
  // and the only thing measured is what focusing adds. Which is nothing:
  // the camera is arithmetic over data the page already had.
  await user.click(screen.getByRole("button", { name: OBJECT_A }));
  const panel = await screen.findByTestId("house-inspector");
  await waitFor(() => expect(screen.getByText("O texto real da nota.")).toBeTruthy());
  const before = fetchMock.mock.calls.length;

  await user.click(within(panel).getByTestId("focus-room-action"));
  await screen.findByTestId("palace-focus-bar");
  await user.click(screen.getByTestId("palace-overview-button"));
  await waitFor(() => expect(screen.queryByTestId("palace-focus-bar")).toBeNull());

  expect(fetchMock.mock.calls.length).toBe(before);
});

/* ══════════════════════════════════════════════════════════════════════
   H. Reduced motion
   ══════════════════════════════════════════════════════════════════════ */

it("moves the camera with a CSS transition, which the global reduce rule owns", async () => {
  // ══════════════════════════════════════════════════════════════════
  //   WHAT THIS CAN AND CANNOT PROVE, SAID PLAINLY
  // ══════════════════════════════════════════════════════════════════
  //
  // jsdom does not evaluate media queries, so nothing here can watch
  // `prefers-reduced-motion` take effect; Chrome does that, and the
  // measurement is in the checkpoint. What it CAN prove is the mechanism
  // the preference acts on: the move is one CSS transition on one
  // element, so `index.css`'s global rule — which forces
  // `transition-duration: 0.001ms !important` on everything — reaches it
  // without this component knowing the preference exists.
  //
  // And that the STATE does not depend on the animation: the transform is
  // a value, correct on the frame it is set, so a reader with motion
  // turned off gets the focused view instantly rather than not at all.
  const user = userEvent.setup();
  mountMap();
  await screen.findByTestId("palace-building");

  expect(camera().style.transition).toContain("transform");

  await focusRoomOf(user, OBJECT_A);
  expect(transform()).toMatch(/^translate\(-?[\d.]+px, -?[\d.]+px\) scale\([\d.]+\)$/);
});

/* ══════════════════════════════════════════════════════════════════════
   I. D5 and the fallback are untouched
   ══════════════════════════════════════════════════════════════════════ */

it("offers no focus at all where there is no house to move a camera in", async () => {
  // Below the touch floor the surface is the list of rooms, which has no
  // spatial focus to offer and must not grow one: the room links there are
  // the fallback's own, unchanged since C1.1.
  stubContainer(180, 260);
  mountMap();

  await waitFor(() => expect(screen.getByText("O prédio não cabe nesta tela")).toBeTruthy());
  expect(screen.queryByTestId("palace-camera")).toBeNull();
  expect(screen.queryByTestId("palace-focus-bar")).toBeNull();
  expect(screen.getAllByRole("link", { name: /Abrir a sala/ }).length).toBeGreaterThan(0);
});
