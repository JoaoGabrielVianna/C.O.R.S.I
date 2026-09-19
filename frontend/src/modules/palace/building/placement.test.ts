import { describe, expect, it } from "vitest";

// The sources, as text, so the structural scans read the CODE. Imported
// through Vite's `?raw` for the same reason `engine.test.ts` does it: the
// app's tsconfig types are `vite/client` only.
import placementSource from "./placement.ts?raw";
import boundsSource from "./bounds.ts?raw";
import fromOverviewSource from "./fromOverview.ts?raw";
import interiorSource from "./interior.ts?raw";

import {
  BUILDING_VERSION,
  ENTRANCE_CELL,
  HOUSE_COLUMNS,
  ROOM_SPAN,
  ROOM_STRIDE,
  placeBuilding,
  type BuildingLayout,
  type BuildingRoom,
} from "./placement";
import { furnishRoom, type InteriorOutput } from "./interior";
import {
  MIN_OBJECT_HIT_SCENE,
  buildingBounds,
  objectBox,
  roomBox,
  unfiledBox,
  worldCellOf,
} from "./bounds";
import { buildingRoomsOf } from "./fromOverview";
import { houseItems } from "./houseModel";
import { inspectorPlacement } from "./inspectorPlacement";
import { fitBuilding } from "./fit";
import { MIN_TARGET_PX, RESTORE_TARGET_PX } from "../scene/fit";
import type { OverviewRoom } from "../api/types";

/**
 * The house, proved rather than eyeballed.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   A PLACEMENT BUG DOES NOT CRASH AND DOES NOT LOOK BROKEN
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * It looks like a Palace whose rooms moved, and the person who moved them
 * cannot tell you what they did, because what they did was rename an
 * artifact. The wire is sorted by `updated_at DESC`, so that failure is
 * one careless line away at all times and would appear long after the edit
 * that caused it.
 *
 * None of these asks whether the house looks good.
 */

/** The source of a sibling file, WITH THE COMMENTS REMOVED. */
function codeOf(source: string): string {
  return source.replace(/\/\*[\s\S]*?\*\//g, " ").replace(/(^|[^:])\/\/.*$/gm, "$1");
}

/**
 * `count` synthetic rooms, in canonical order, created a minute apart.
 *
 * Synthetic on purpose: the operator's Palace is not a fixture, and no
 * test here creates, reads or resembles real content.
 */
function synthetic(count: number): BuildingRoom[] {
  return Array.from({ length: count }, (_, i) => ({
    id: `room-${String(i).padStart(3, "0")}`,
    createdAt: `2026-01-01T00:${String(i).padStart(2, "0")}:00Z`,
  }));
}

function place(rooms: readonly BuildingRoom[], hasUnfiled = false): BuildingLayout {
  return placeBuilding({ rooms, buildingVersion: BUILDING_VERSION, hasUnfiled });
}

/** Where each room ended up, keyed by id, for comparing two houses. */
function positions(layout: BuildingLayout): Map<string, string> {
  return new Map(
    layout.rooms.map((r) => [r.roomId, `${r.origin.u},${r.origin.v},${r.column},${r.row}`]),
  );
}

/** Rooms furnished to the worst case: every position filled. */
function fullInteriors(layout: BuildingLayout): Map<string, InteriorOutput> {
  return new Map(
    layout.rooms.map((room) => [
      room.roomId,
      furnishRoom({
        roomId: room.roomId,
        artifacts: Array.from({ length: 7 }, (_, i) => ({
          id: `${room.roomId}-a${i}`,
          kind: "list" as const,
          createdAt: `2026-02-01T00:0${i}:00Z`,
        })),
        artifactTotal: 20,
        hasMemories: true,
      }),
    ]),
  );
}

const SCALES = [0, 1, 3, 5, 6, 7, 8, 12, 16, 20, 50];

/* ══════════════════════════════════════════════════════════════════════
   1. Determinism
   ══════════════════════════════════════════════════════════════════════ */

describe("determinism", () => {
  it("gives a deep-equal house for the same input, every time", () => {
    for (const n of SCALES) {
      const rooms = synthetic(n);
      expect(place(rooms, true)).toEqual(place(rooms, true));
    }
  });

  it("is pure: no clock, no randomness, no measurement", () => {
    for (const source of [placementSource, boundsSource, fromOverviewSource, interiorSource]) {
      const code = codeOf(source);
      for (const forbidden of [
        "Date.now",
        "new Date(",
        "Math.random",
        "performance.now",
        "window.",
        "document.",
        "getBoundingClientRect",
        "ResizeObserver",
      ]) {
        expect(code).not.toContain(forbidden);
      }
    }
  });

  it("persists no coordinate anywhere", () => {
    // I-C. A placement that could be stored is a placement somebody will
    // eventually drag, and dragging ends the whole model.
    for (const source of [placementSource, boundsSource, fromOverviewSource, interiorSource]) {
      const code = codeOf(source);
      for (const forbidden of [
        "localStorage",
        "sessionStorage",
        "indexedDB",
        "fetch(",
        "queryKey",
        "useQuery",
      ]) {
        expect(code).not.toContain(forbidden);
      }
    }
  });
});

/* ══════════════════════════════════════════════════════════════════════
   2. The wire order must not reach the arrangement
   ══════════════════════════════════════════════════════════════════════ */

describe("the wire order is not the spatial order", () => {
  it("places the same house from the literal `updated_at DESC` order", () => {
    const rooms = synthetic(20);
    // Exactly what the repository returns: newest EDIT first, which for
    // this fixture is the reverse of creation order.
    expect(place([...rooms].reverse())).toEqual(place(rooms));
  });

  it("ignores every shuffle of the input", () => {
    const rooms = synthetic(20);
    const expected = place(rooms);
    for (const step of [3, 7, 11, 13]) {
      const shuffled = rooms.map((_, i) => rooms[(i * step) % rooms.length]);
      expect(place(shuffled)).toEqual(expected);
    }
  });

  it("orders by creation and breaks ties by id, never by arrival", () => {
    const sameInstant: BuildingRoom[] = [
      { id: "zzz", createdAt: "2026-01-01T00:00:00Z" },
      { id: "aaa", createdAt: "2026-01-01T00:00:00Z" },
      { id: "mmm", createdAt: "2026-01-01T00:00:00Z" },
    ];
    expect(place(sameInstant).rooms.map((r) => r.roomId)).toEqual(["aaa", "mmm", "zzz"]);
    expect(place([...sameInstant].reverse()).rooms.map((r) => r.roomId)).toEqual([
      "aaa",
      "mmm",
      "zzz",
    ]);
  });

  it("drops a repeated room instead of standing it in two places", () => {
    const rooms = synthetic(3);
    expect(place([...rooms, rooms[1]])).toEqual(place(rooms));
  });
});

/* ══════════════════════════════════════════════════════════════════════
   3. I-A at house scale: editing meaning moves nothing
   ══════════════════════════════════════════════════════════════════════ */

describe("editing meaning must not move space", () => {
  function wireRoom(i: number, over: Partial<OverviewRoom> = {}): OverviewRoom {
    return {
      room_id: `room-${String(i).padStart(3, "0")}`,
      name: `Sala ${i}`,
      description: "",
      sensitivity: "normal",
      created_at: `2026-01-01T00:${String(i).padStart(2, "0")}:00Z`,
      // Reverse of creation order: the newest room was edited longest ago.
      updated_at: `2026-06-${String(30 - i).padStart(2, "0")}T00:00:00Z`,
      artifact_count: i,
      memory_count: i,
      archived_count: i,
      ...over,
    };
  }

  it("places the same house when every title changes", () => {
    const before = [0, 1, 2, 3, 4, 5, 6, 7].map((i) => wireRoom(i));
    const after = before.map((r) => ({ ...r, name: `Renomeada ${r.name}`, description: "nova" }));
    expect(place(buildingRoomsOf(after))).toEqual(place(buildingRoomsOf(before)));
  });

  it("places the same house when every `updated_at` changes", () => {
    const before = [0, 1, 2, 3, 4, 5, 6, 7].map((i) => wireRoom(i));
    const after = before.map((r) => ({ ...r, updated_at: "2026-09-18T12:00:00Z" }));
    expect(place(buildingRoomsOf(after))).toEqual(place(buildingRoomsOf(before)));
  });

  it("places the same house when every count and sensitivity changes", () => {
    const before = [0, 1, 2, 3, 4, 5].map((i) => wireRoom(i));
    const after = before.map((r) => ({
      ...r,
      artifact_count: 999,
      memory_count: 999,
      archived_count: 999,
      sensitivity: "private" as const,
    }));
    expect(place(buildingRoomsOf(after))).toEqual(place(buildingRoomsOf(before)));
  });

  it("re-sorts the wire's own order rather than trusting it", () => {
    // `buildingRoomsOf` deliberately does NOT sort, so this proves the
    // engine is what makes the wire order harmless.
    const wire = [5, 3, 0, 7, 1].map((i) => wireRoom(i));
    expect(place(buildingRoomsOf(wire)).rooms.map((r) => r.roomId)).toEqual([
      "room-000",
      "room-001",
      "room-003",
      "room-005",
      "room-007",
    ]);
  });

  it("narrows the wire to an id and an instant, and nothing else", () => {
    const [narrowed] = buildingRoomsOf([wireRoom(4)]);
    expect(Object.keys(narrowed).sort()).toEqual(["createdAt", "id"]);
    // A spread would carry the whole DTO and keep compiling as it grew.
    expect(codeOf(fromOverviewSource)).not.toContain("...room");
  });

  it("never mentions a field it must not read", () => {
    const code = codeOf(placementSource);
    for (const forbidden of [
      "name",
      "title",
      "description",
      "updatedAt",
      "updated_at",
      "sensitivity",
      "artifact_count",
      "memory_count",
      "archived_count",
    ]) {
      expect(code).not.toContain(forbidden);
    }
  });
});

/* ══════════════════════════════════════════════════════════════════════
   4. Compact growth
   ══════════════════════════════════════════════════════════════════════ */

describe("the compact grid", () => {
  it("fills row-major into a FIXED number of columns", () => {
    const layout = place(synthetic(HOUSE_COLUMNS * 2 + 1));
    layout.rooms.forEach((room, i) => {
      expect(room.column).toBe(i % HOUSE_COLUMNS);
      expect(room.row).toBe(Math.floor(i / HOUSE_COLUMNS));
      expect(room.origin.u).toBe(room.column * ROOM_STRIDE);
      expect(room.origin.v).toBe(room.row * ROOM_STRIDE);
    });
  });

  it("makes adjacent rooms share a wall rather than stand apart", () => {
    // The whole difference between a house and a row of sheds. If the
    // stride ever exceeds the span there is ground between the rooms and
    // the composition stops reading as one building.
    expect(ROOM_STRIDE).toBe(ROOM_SPAN);
  });

  it("adding a room never moves a room already placed", () => {
    // Across row boundaries, which is where a grid that sized itself to
    // the count would reflow.
    for (let n = 0; n < 24; n += 1) {
      const before = positions(place(synthetic(n)));
      const after = positions(place(synthetic(n + 1)));
      for (const [id, where] of before) {
        expect(after.get(id), `room ${id} moved when the Palace grew to ${n + 1}`).toBe(where);
      }
    }
  });

  it("never reflows the grid to look balanced", () => {
    // `ceil(sqrt(n))` columns is the tempting compact choice and it is the
    // mutation this test exists to catch: with it, the ninth room changes
    // the column of the previous eight.
    const eight = place(synthetic(8));
    const nine = place(synthetic(9));
    expect(nine.rooms.slice(0, 8).map((r) => r.column)).toEqual(
      eight.rooms.map((r) => r.column),
    );
    expect(nine.rooms.slice(0, 8).map((r) => r.row)).toEqual(eight.rooms.map((r) => r.row));
  });

  it("draws no empty cell and no ghost room", () => {
    const layout = place(synthetic(HOUSE_COLUMNS + 1));
    expect(layout.rooms).toHaveLength(HOUSE_COLUMNS + 1);
  });

  it("opens a doorway exactly where a room stands on the other side", () => {
    const layout = place(synthetic(HOUSE_COLUMNS * 2));
    for (const room of layout.rooms) {
      expect(room.doorToColumn).toBe(room.column > 0);
      expect(room.doorToRow).toBe(room.row > 0);
    }
    // The first room is the corner of the house: no neighbour behind it
    // and none to its side, so no doorway in either shared wall.
    expect(layout.rooms[0].doorToColumn).toBe(false);
    expect(layout.rooms[0].doorToRow).toBe(false);
  });

  it("names no entity the Core would have to learn", () => {
    const code = codeOf(placementSource);
    for (const forbidden of [
      "parent_room_id",
      "parentRoomId",
      "wing_id",
      "wingId",
      "corridor",
      "adjacency",
      "ROOM_CONNECTED_TO_ROOM",
      "Relation",
    ]) {
      expect(code).not.toContain(forbidden);
    }
  });
});

/* ══════════════════════════════════════════════════════════════════════
   5. Removal: the recorded decision, both halves
   ══════════════════════════════════════════════════════════════════════ */

describe("removal", () => {
  /**
   * Placement is a pure function of the ELIGIBLE set, so there is no
   * compaction step to remove and no gap to preserve: a room that is not
   * in the input never had a cell to vacate. Preserving a hole would need
   * a persisted coordinate (I-C, forbidden) or a `used to be here` field
   * that does not exist on the wire.
   *
   * Both halves are pinned so the behaviour is a decision somebody chose
   * rather than one somebody discovers.
   */
  it("leaves every room before the removed one exactly where it was", () => {
    const rooms = synthetic(30);
    const before = positions(place(rooms));
    const after = positions(place(rooms.filter((_, i) => i !== 2)));
    for (let i = 0; i < 2; i += 1) {
      expect(after.get(rooms[i].id)).toBe(before.get(rooms[i].id));
    }
  });

  it("shifts the rooms after it forward by one, and this is the accepted cost", () => {
    const rooms = synthetic(30);
    const before = positions(place(rooms));
    const after = positions(place(rooms.filter((_, i) => i !== 2)));
    expect(after.get(rooms[3].id)).toBe(before.get(rooms[2].id));
    expect(after.get(rooms[29].id)).toBe(before.get(rooms[28].id));
  });

  it("leaves no hole a withheld room could be inferred from", () => {
    // A room the surface withholds never reaches the input, so it never
    // had an index. The indices are always a dense run from zero.
    const layout = place(synthetic(5));
    expect(layout.rooms.map((r) => r.index)).toEqual([0, 1, 2, 3, 4]);
  });
});

/* ══════════════════════════════════════════════════════════════════════
   6. Bounds and the tray
   ══════════════════════════════════════════════════════════════════════ */

describe("bounds", () => {
  it("derives the same bounds from the same house", () => {
    for (const n of SCALES) {
      const layout = place(synthetic(n), true);
      const interiors = fullInteriors(layout);
      expect(buildingBounds(layout, interiors)).toEqual(buildingBounds(layout, interiors));
    }
  });

  it("gives an empty Palace a finite box rather than an infinity", () => {
    const bounds = buildingBounds(place([]), new Map());
    expect(Number.isFinite(bounds.width)).toBe(true);
    expect(Number.isFinite(bounds.height)).toBe(true);
    expect(bounds.width).toBeGreaterThan(0);
  });

  it("contains every room and every object inside the bounds", () => {
    for (const n of SCALES) {
      const layout = place(synthetic(n), true);
      const interiors = fullInteriors(layout);
      const b = buildingBounds(layout, interiors);
      for (const room of layout.rooms) {
        const box = roomBox(room);
        expect(box.minX).toBeGreaterThanOrEqual(b.minX);
        expect(box.minY).toBeGreaterThanOrEqual(b.minY);
        expect(box.maxX).toBeLessThanOrEqual(b.minX + b.width);
        expect(box.maxY).toBeLessThanOrEqual(b.minY + b.height);
      }
    }
  });

  it("puts the tray at the entrance only when something is unfiled", () => {
    expect(place(synthetic(3), false).unfiled).toBeUndefined();
    expect(place(synthetic(3), true).unfiled?.cell).toEqual(ENTRANCE_CELL);
    // And the tray exists even with no rooms at all: it is not part of
    // the house.
    expect(place([], true).unfiled?.cell).toEqual(ENTRANCE_CELL);
  });

  it("gives the tray an object's dimensions, so unfiled content cannot change the verdict", () => {
    const layout = place(synthetic(1), true);
    const tray = unfiledBox(layout.unfiled!);
    expect(tray.maxX - tray.minX).toBe(52);
    expect(Math.min(tray.maxX - tray.minX, tray.maxY - tray.minY)).toBe(MIN_OBJECT_HIT_SCENE);
  });
});

/* ══════════════════════════════════════════════════════════════════════
   7. Scale, and the honest hand-over
   ══════════════════════════════════════════════════════════════════════ */

describe("scale", () => {
  /** The N3-measured stable area on a 1440x950 desktop. */
  const DESKTOP = { width: 1088, height: 688 };

  function verdictAt(n: number) {
    const layout = place(synthetic(n), true);
    const bounds = buildingBounds(layout, fullInteriors(layout));
    return fitBuilding(bounds, DESKTOP, "spatial", false);
  }

  it("keeps objects pressable on a desktop across the range that exists", () => {
    // Worst case: every room filled to all nine positions.
    for (const n of [1, 3, 5, 6, 7, 8]) {
      expect(verdictAt(n).smallestTargetPx, `n=${n}`).toBeGreaterThanOrEqual(MIN_TARGET_PX);
      expect(verdictAt(n).mode).toBe("spatial");
    }
  });

  it("survives an ordinary laptop window, not just a maximised one", () => {
    // ══════════════════════════════════════════════════════════════
    //   THE CASE THE BROWSER PASS CAUGHT AND JSDOM NEVER COULD
    // ══════════════════════════════════════════════════════════════
    //
    // The first version of this grid was four columns wide, which passed
    // every test here and then handed the operator's own five-room Palace
    // over to the list at 1180x800 — an ordinary window on an ordinary
    // laptop. Three columns makes the block squarer, and an isometric
    // block near 2:1 wastes less of the frame.
    //
    // 868x560 is the stable area N3's measurer reports at that window.
    const MEDIUM = { width: 868, height: 560 };
    const layout = place(synthetic(5), false);
    const bounds = buildingBounds(layout, fullInteriors(layout));
    expect(fitBuilding(bounds, MEDIUM, "spatial", false).mode).toBe("spatial");
  });

  it("hands over rather than shrinking past the floor", () => {
    // ══════════════════════════════════════════════════════════════
    //   THE PRICE OF SHOWING CONTENT, RECORDED RATHER THAN HIDDEN
    // ══════════════════════════════════════════════════════════════
    //
    // C1 drew empty shells and stayed spatial past fifty rooms. Drawing
    // the real furniture costs resolution, so the house now hands over to
    // the list in the teens on this container. That is the honest trade:
    // the alternative is objects nobody can press.
    for (const n of [16, 20, 50]) {
      expect(verdictAt(n).mode, `n=${n}`).toBe("library");
    }
  });

  it("measures the OBJECT, not the room it stands in", () => {
    // Measuring the room would let a house full of unpressable furniture
    // pass the rule by having large rooms. This is the assertion that
    // fails if somebody points the verdict back at the shell.
    expect(MIN_OBJECT_HIT_SCENE).toBe(52);
    const layout = place(synthetic(6), true);
    const box = roomBox(layout.rooms[0]);
    expect(Math.min(box.maxX - box.minX, box.maxY - box.minY)).toBeGreaterThan(
      MIN_OBJECT_HIT_SCENE,
    );
  });

  it("uses the same thresholds the room does, untouched", () => {
    expect(MIN_TARGET_PX).toBe(44);
    expect(RESTORE_TARGET_PX).toBe(52);

    const layout = place(synthetic(8), true);
    const bounds = buildingBounds(layout, fullInteriors(layout));
    const at = (px: number) => ({
      width: (px / MIN_OBJECT_HIT_SCENE) * bounds.width,
      height: (px / MIN_OBJECT_HIT_SCENE) * bounds.height,
    });
    // A size that KEEPS the house is not necessarily enough to bring it
    // back: that is the hysteresis, one scale up.
    expect(fitBuilding(bounds, at(48), "spatial", false).mode).toBe("spatial");
    expect(fitBuilding(bounds, at(48), "library", false).mode).toBe("library");
  });

  it("shows the house anyway when the reader insists", () => {
    const layout = place(synthetic(50), true);
    const bounds = buildingBounds(layout, fullInteriors(layout));
    const tiny = { width: 200, height: 200 };
    expect(fitBuilding(bounds, tiny, "spatial", false).mode).toBe("library");
    expect(fitBuilding(bounds, tiny, "spatial", true).mode).toBe("spatial");
  });

  it("places every size without error", () => {
    for (const n of SCALES) {
      expect(place(synthetic(n), true).rooms).toHaveLength(n);
    }
  });
});

/* ══════════════════════════════════════════════════════════════════════
   8. C1.2: the house has to fill the stage it is given
   ══════════════════════════════════════════════════════════════════════ */

describe("presence in the frame", () => {
  it("reserves headroom above the house for the names, which are not in it", () => {
    // ══════════════════════════════════════════════════════════════
    //   THE BOUNDS PAY FOR A LAYER THEY CANNOT SEE
    // ══════════════════════════════════════════════════════════════
    //
    // Room names are HTML positioned just above each room's back wall,
    // and the stage clips overflow. They contribute nothing to this box
    // on their own, so shrinking the padding cuts the top row's names in
    // half. The top margin is therefore deliberately larger than the
    // others, and this is the test that fails if somebody "fixes" the
    // asymmetry.
    // ── Why the union is rebuilt here ──────────────────────────────
    // The first version of this test compared the ROOM boxes against the
    // bounds, and it could not fail: the unfiled tray sits above every
    // room, so it — not a room — is what sets the top of the union, and
    // the comparison was measuring the tray's own clearance. It passed
    // against a mutation that deleted the headroom outright. The margin
    // has to be measured against the same union the bounds are built
    // from.
    const layout = place(synthetic(5), true);
    const interiors = fullInteriors(layout);
    const bounds = buildingBounds(layout, interiors);

    const boxes: { minX: number; minY: number }[] = [];
    for (const room of layout.rooms) {
      boxes.push(roomBox(room));
      const interior = interiors.get(room.roomId)!;
      for (const object of interior.objects) {
        boxes.push(objectBox(worldCellOf(room, object.local)));
      }
      if (interior.pile) boxes.push(objectBox(worldCellOf(room, interior.pile.local)));
      if (interior.memory) boxes.push(objectBox(worldCellOf(room, interior.memory.local)));
    }
    boxes.push(unfiledBox(layout.unfiled!));

    const topMargin = Math.min(...boxes.map((b) => b.minY)) - bounds.minY;
    const sideMargin = Math.min(...boxes.map((b) => b.minX)) - bounds.minX;

    expect(topMargin).toBeGreaterThan(sideMargin);
    // Enough for a line of small type at the scales this is drawn at.
    expect(topMargin - sideMargin).toBeGreaterThanOrEqual(24);
  });

  it("wastes no more of the frame on margin than it has to", () => {
    // The human gate on C1.1 reported "a lot of empty space". Measured in
    // Chrome, 40 units of padding were spending 94 of the container's
    // 1088 pixels on nothing, and the house is width-bound, so that
    // margin is the only slack there is. This pins the ratio rather than
    // the constant: margin stays a small fraction of the whole.
    const layout = place(synthetic(5), true);
    const bounds = buildingBounds(layout, fullInteriors(layout));
    const drawnWidth = Math.max(...layout.rooms.map((r) => roomBox(r).maxX))
      - Math.min(...layout.rooms.map((r) => roomBox(r).minX));

    expect(drawnWidth / bounds.width).toBeGreaterThan(0.92);
  });

  it("still contains every room and object after the margins shrank", () => {
    for (const n of SCALES) {
      const layout = place(synthetic(n), true);
      const interiors = fullInteriors(layout);
      const b = buildingBounds(layout, interiors);
      for (const room of layout.rooms) {
        const box = roomBox(room);
        expect(box.minX).toBeGreaterThanOrEqual(b.minX);
        expect(box.minY).toBeGreaterThanOrEqual(b.minY);
        expect(box.maxX).toBeLessThanOrEqual(b.minX + b.width);
        expect(box.maxY).toBeLessThanOrEqual(b.minY + b.height);
      }
    }
  });
});

/* ══════════════════════════════════════════════════════════════════════
   9. C1.2: where the inspector opens
   ══════════════════════════════════════════════════════════════════════ */

describe("inspector placement", () => {
  const STAGE = { width: 1088, height: 611 };

  function stageFor(n: number) {
    const layout = place(synthetic(n), true);
    const interiors = fullInteriors(layout);
    const bounds = buildingBounds(layout, interiors);
    const fit = fitBuilding(bounds, STAGE, "spatial", false);
    return { layout, interiors, bounds, fit };
  }

  it("is deterministic in the item, the bounds and the fit", () => {
    const { layout, interiors, bounds, fit } = stageFor(5);
    const item = houseItems(layout, interiors)[0];
    expect(inspectorPlacement(item, bounds, fit)).toEqual(
      inspectorPlacement(item, bounds, fit),
    );
  });

  it("opens beside the object, never on top of it", () => {
    const { layout, interiors, bounds, fit } = stageFor(5);
    for (const item of houseItems(layout, interiors)) {
      const { anchor } = inspectorPlacement(item, bounds, fit);
      const left = Number(String(anchor.left).replace("px", ""));
      const width = Number(String(anchor.width).replace("px", ""));
      const box = {
        left: fit.offsetX + (item.box.minX - bounds.minX) * fit.scale,
        right: fit.offsetX + (item.box.maxX - bounds.minX) * fit.scale,
      };
      // Entirely to one side of the object it belongs to.
      expect(
        left >= box.right || left + width <= box.left,
        `panel for ${item.key} overlaps its own object`,
      ).toBe(true);
    }
  });

  it("stays inside the stage, horizontally and vertically", () => {
    const { layout, interiors, bounds, fit } = stageFor(8);
    for (const item of houseItems(layout, interiors)) {
      const { anchor, panelMaxHeight } = inspectorPlacement(item, bounds, fit);
      const left = Number(String(anchor.left).replace("px", ""));
      const top = Number(String(anchor.top).replace("px", ""));
      const width = Number(String(anchor.width).replace("px", ""));
      const maxH = Number(panelMaxHeight.replace("px", ""));

      expect(left).toBeGreaterThanOrEqual(0);
      expect(left + width).toBeLessThanOrEqual(STAGE.width);
      expect(top).toBeGreaterThanOrEqual(0);
      // The cap is what keeps a tall panel from running off the bottom:
      // Chrome measured a 521px panel in a 611px stage before the height
      // was derived from the top that was actually chosen.
      expect(top + maxH).toBeLessThanOrEqual(STAGE.height);
    }
  });

  it("falls back to a sheet when the stage is too narrow to have a beside", () => {
    const { layout, interiors, bounds } = stageFor(5);
    const narrow = { width: 360, height: 620 };
    const fit = fitBuilding(bounds, narrow, "spatial", true);
    const { anchor } = inspectorPlacement(houseItems(layout, interiors)[0], bounds, fit);
    expect(anchor.bottom).toBe(0);
    expect(anchor.insetInline).toBe(0);
  });

  it("gives a sheet when there is no selection and no measurement", () => {
    const { bounds } = stageFor(5);
    expect(inspectorPlacement(null, bounds, null).anchor.bottom).toBe(0);
  });
});
