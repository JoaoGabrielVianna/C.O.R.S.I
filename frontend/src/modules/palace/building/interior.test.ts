import { describe, expect, it } from "vitest";

import interiorSource from "./interior.ts?raw";

import {
  INTERIOR_PITCH,
  INTERIOR_SLOTS,
  MEMORY_SLOT,
  PILE_SLOT,
  SLOT_OFFSETS,
  VISIBLE_OBJECTS,
  furnishRoom,
  interiorCellOf,
  type InteriorArtifact,
  type InteriorOutput,
} from "./interior";
import {
  MIN_OBJECT_HIT_SCENE,
  OBJECT_FOOTPRINT,

  worldCellOf,
  type Box,
} from "./bounds";
import { BUILDING_VERSION, ROOM_SPAN, placeBuilding } from "./placement";
import { houseItems, focusOrder, paintOrder } from "./houseModel";
import { interiorArtifactsByRoom } from "./fromOverview";
import type { ArtifactRow } from "../api/types";

/**
 * What stands inside a room, proved rather than eyeballed.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   THE RULE THESE EXIST TO DEFEND MAKES THE PICTURE WORSE WHEN KEPT
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * A house with a cabinet in every room looks better than a house with a
 * cabinet only where a list exists. That is exactly why the rule needs
 * tests rather than care: the tempting change is the wrong one, and it
 * would look like an improvement in review.
 */

function codeOf(source: string): string {
  return source.replace(/\/\*[\s\S]*?\*\//g, " ").replace(/(^|[^:])\/\/.*$/gm, "$1");
}

function artifact(i: number, kind: InteriorArtifact["kind"] = "list"): InteriorArtifact {
  return {
    id: `a-${String(i).padStart(3, "0")}`,
    kind,
    createdAt: `2026-03-01T00:${String(i).padStart(2, "0")}:00Z`,
  };
}

function furnish(
  artifacts: readonly InteriorArtifact[],
  over: { artifactTotal?: number; hasMemories?: boolean } = {},
): InteriorOutput {
  return furnishRoom({
    roomId: "room-1",
    artifacts,
    artifactTotal: over.artifactTotal ?? artifacts.length,
    hasMemories: over.hasMemories ?? false,
  });
}

/** Do two boxes share area? Touching along an edge does not count. */
function overlaps(a: Box, b: Box): boolean {
  return a.minX < b.maxX && b.minX < a.maxX && a.minY < b.maxY && b.minY < a.maxY;
}

/* ══════════════════════════════════════════════════════════════════════
   1. Real artifacts only
   ══════════════════════════════════════════════════════════════════════ */

describe("every object is a real artifact", () => {
  it("furnishes an empty room with nothing at all", () => {
    const out = furnish([]);
    expect(out.objects).toEqual([]);
    expect(out.pile).toBeUndefined();
    expect(out.memory).toBeUndefined();
  });

  it("draws exactly one object per artifact it was given", () => {
    for (const n of [1, 2, 3, 5, 7]) {
      const out = furnish(Array.from({ length: n }, (_, i) => artifact(i)));
      expect(out.objects).toHaveLength(n);
      expect(out.objects.map((o) => o.artifactId)).toEqual(
        Array.from({ length: n }, (_, i) => artifact(i).id),
      );
    }
  });

  it("carries each artifact's own kind, so the silhouette cannot be invented", () => {
    const out = furnish([
      artifact(0, "project"),
      artifact(1, "list"),
      artifact(2, "plan"),
      artifact(3, "note"),
    ]);
    expect(out.objects.map((o) => o.kind)).toEqual(["project", "list", "plan", "note"]);
  });

  it("never emits an object without an artifact id", () => {
    const out = furnish([artifact(0), artifact(1)], { artifactTotal: 9, hasMemories: true });
    for (const object of out.objects) {
      expect(object.artifactId).toBeTruthy();
    }
    // The pile and the memory surface are not artifacts and do not claim
    // to be: neither carries an artifact id.
    expect(out.pile).not.toHaveProperty("artifactId");
    expect(out.memory).not.toHaveProperty("artifactId");
  });

  it("has no notion of a decorative or placeholder object", () => {
    const code = codeOf(interiorSource);
    for (const forbidden of ["placeholder", "decorative", "filler", "dummy", "ghost"]) {
      expect(code.toLowerCase()).not.toContain(forbidden);
    }
  });
});

/* ══════════════════════════════════════════════════════════════════════
   2. Honest overflow
   ══════════════════════════════════════════════════════════════════════ */

describe("the pile is honest", () => {
  it("does not exist when everything is drawn", () => {
    expect(furnish(Array.from({ length: VISIBLE_OBJECTS }, (_, i) => artifact(i))).pile)
      .toBeUndefined();
  });

  it("counts what is not drawn individually, from the read surface's total", () => {
    const out = furnish(Array.from({ length: VISIBLE_OBJECTS + 3 }, (_, i) => artifact(i)), {
      artifactTotal: VISIBLE_OBJECTS + 3,
    });
    expect(out.objects).toHaveLength(VISIBLE_OBJECTS);
    expect(out.pile?.count).toBe(3);
  });

  it("stays right when the caller's page did not reach every artifact", () => {
    // ══════════════════════════════════════════════════════════════
    //   THE CASE THAT MAKES ONE REQUEST FOR THE WHOLE HOUSE HONEST
    // ══════════════════════════════════════════════════════════════
    //
    // Above the backend's 100-row ceiling some rooms get a short page.
    // The pile counts from `artifactTotal`, which the backend computed in
    // full under the same visibility predicate, so the remainder is still
    // exactly "how many are not drawn individually" — whether they were
    // capped by VISIBLE_OBJECTS or never fetched at all.
    const out = furnish([artifact(0), artifact(1)], { artifactTotal: 40 });
    expect(out.objects).toHaveLength(2);
    expect(out.pile?.count).toBe(38);
  });

  it("never renders a negative pile from a stale count", () => {
    const out = furnish([artifact(0), artifact(1), artifact(2)], { artifactTotal: 1 });
    expect(out.pile).toBeUndefined();
  });

  it("keeps its own position instead of borrowing the last object's", () => {
    // A5's lesson, one scale up: if the pile took the seventh slot, going
    // from seven artifacts to eight would move something already drawn.
    const seven = furnish(Array.from({ length: 7 }, (_, i) => artifact(i)));
    const eight = furnish(Array.from({ length: 8 }, (_, i) => artifact(i)));
    expect(eight.objects.map((o) => o.slot)).toEqual(seven.objects.map((o) => o.slot));
    expect(eight.objects[6].artifactId).toBe(seven.objects[6].artifactId);
    expect(eight.pile?.slot).toBe(PILE_SLOT);
  });
});

/* ══════════════════════════════════════════════════════════════════════
   3. The memory surface
   ══════════════════════════════════════════════════════════════════════ */

describe("the memory surface", () => {
  it("exists exactly when the room has an eligible memory", () => {
    expect(furnish([artifact(0)], { hasMemories: false }).memory).toBeUndefined();
    expect(furnish([artifact(0)], { hasMemories: true }).memory?.slot).toBe(MEMORY_SLOT);
  });

  it("appears in a room with no artifacts at all", () => {
    const out = furnish([], { hasMemories: true });
    expect(out.objects).toEqual([]);
    expect(out.memory).toBeTruthy();
  });

  it("does not move a single piece of furniture when it appears", () => {
    // S5.1's lesson: the notebook's place belongs to the geometry, and
    // geometry does not negotiate with content. Its slot is reserved
    // whether or not the room has memories, so writing the first memory
    // moves nothing.
    const artifacts = Array.from({ length: VISIBLE_OBJECTS }, (_, i) => artifact(i));
    const without = furnish(artifacts, { hasMemories: false });
    const with_ = furnish(artifacts, { hasMemories: true });
    expect(with_.objects).toEqual(without.objects);
  });

  it("says nothing about how many memories there are", () => {
    const out = furnish([], { hasMemories: true });
    expect(Object.keys(out.memory!).sort()).toEqual(["local", "slot"]);
    // A count in the geometry is a number that eventually decides
    // something, and each of those is a room that rearranges itself when
    // somebody writes one more thing down.
    expect(codeOf(interiorSource)).not.toContain("memoryCount");
    expect(codeOf(interiorSource)).not.toContain("memory_count");
  });
});

/* ══════════════════════════════════════════════════════════════════════
   4. Stability inside the room
   ══════════════════════════════════════════════════════════════════════ */

describe("stability inside the room", () => {
  it("gives the same furnishing for the same input, in any order", () => {
    const artifacts = Array.from({ length: 5 }, (_, i) => artifact(i));
    const expected = furnish(artifacts);
    expect(furnish([...artifacts].reverse())).toEqual(expected);
    expect(furnish([artifacts[3], artifacts[0], artifacts[4], artifacts[1], artifacts[2]]))
      .toEqual(expected);
  });

  it("adding an artifact never moves one already standing", () => {
    for (let n = 0; n < VISIBLE_OBJECTS + 3; n += 1) {
      const before = furnish(Array.from({ length: n }, (_, i) => artifact(i)));
      const after = furnish(Array.from({ length: n + 1 }, (_, i) => artifact(i)));
      for (const object of before.objects) {
        const same = after.objects.find((o) => o.artifactId === object.artifactId);
        expect(same?.slot, `artifact ${object.artifactId} moved at n=${n + 1}`).toBe(object.slot);
      }
    }
  });

  it("changing an artifact's kind changes the shape and not the place", () => {
    const before = furnish([artifact(0, "list"), artifact(1, "note")]);
    const after = furnish([artifact(0, "plan"), artifact(1, "note")]);
    expect(after.objects.map((o) => o.slot)).toEqual(before.objects.map((o) => o.slot));
    expect(after.objects[0].kind).toBe("plan");
  });

  it("does not group by kind, so a note cannot displace a list", () => {
    const lists = [artifact(0, "list"), artifact(1, "list")];
    const withNote = furnish([...lists, artifact(2, "note")]);
    expect(withNote.objects.slice(0, 2).map((o) => o.slot)).toEqual([0, 1]);
    expect(withNote.objects[2].kind).toBe("note");
    expect(withNote.objects[2].slot).toBe(2);
  });

  it("shifts the artifacts after a removed one, and that is the accepted cost", () => {
    const artifacts = Array.from({ length: 5 }, (_, i) => artifact(i));
    const before = furnish(artifacts);
    const after = furnish(artifacts.filter((_, i) => i !== 1));
    // Before the removal: untouched.
    expect(after.objects[0].slot).toBe(before.objects[0].slot);
    // After it: one place forward. Same trade the room grid makes.
    expect(after.objects[1].artifactId).toBe(before.objects[2].artifactId);
    expect(after.objects[1].slot).toBe(before.objects[1].slot);
  });

  it("drops a repeated artifact instead of standing it in two places", () => {
    const artifacts = [artifact(0), artifact(1)];
    expect(furnish([...artifacts, artifacts[0]], { artifactTotal: 2 })).toEqual(
      furnish(artifacts),
    );
  });
});

/* ══════════════════════════════════════════════════════════════════════
   5. The lattice: no two targets ever overlap
   ══════════════════════════════════════════════════════════════════════ */

describe("the object lattice", () => {
  it("makes a room exactly three pitches wide", () => {
    // What turns the room-local grid into one house-wide lattice: the gap
    // across a shared wall is one pitch, exactly like the gap inside a
    // room. Break this and objects in neighbouring rooms collide.
    expect(ROOM_SPAN).toBeCloseTo(3 * INTERIOR_PITCH, 10);
    expect(SLOT_OFFSETS).toHaveLength(3);
    SLOT_OFFSETS.forEach((offset, i) => {
      expect(offset).toBeCloseTo((i + 0.5) * INTERIOR_PITCH, 10);
    });
  });

  it("clears an object's box on at least one axis for every lattice step", () => {
    // The argument in the header, checked. A step of `(a, b)` pitches is
    // 32·|a − b| apart in x and 16·|a + b| apart in y.
    for (let a = -4; a <= 4; a += 1) {
      for (let b = -4; b <= 4; b += 1) {
        if (a === 0 && b === 0) continue;
        const dx = Math.abs(32 * (a - b) * INTERIOR_PITCH);
        const dy = Math.abs(16 * (a + b) * INTERIOR_PITCH);
        expect(
          dx >= OBJECT_FOOTPRINT.width || dy >= MIN_OBJECT_HIT_SCENE,
          `lattice step (${a}, ${b}) puts two objects on top of each other`,
        ).toBe(true);
      }
    }
  });

  it("never overlaps two targets anywhere in the house, at any size", () => {
    for (const n of [1, 2, 3, 5, 6, 7, 8, 12, 20, 50]) {
      const layout = placeBuilding({
        rooms: Array.from({ length: n }, (_, i) => ({
          id: `r${String(i).padStart(3, "0")}`,
          createdAt: `2026-01-01T00:${String(i).padStart(2, "0")}:00Z`,
        })),
        buildingVersion: BUILDING_VERSION,
        hasUnfiled: true,
      });

      // Worst case: every room filled to all nine positions.
      const interiors = new Map(
        layout.rooms.map((room) => [
          room.roomId,
          furnishRoom({
            roomId: room.roomId,
            artifacts: Array.from({ length: VISIBLE_OBJECTS }, (_, i) => ({
              id: `${room.roomId}-a${i}`,
              kind: "list" as const,
              createdAt: `2026-02-01T00:0${i}:00Z`,
            })),
            artifactTotal: 99,
            hasMemories: true,
          }),
        ]),
      );

      const items = houseItems(layout, interiors);
      expect(items.length).toBe(n * INTERIOR_SLOTS + 1);

      for (let i = 0; i < items.length; i += 1) {
        for (let j = i + 1; j < items.length; j += 1) {
          expect(
            overlaps(items[i].box, items[j].box),
            `n=${n}: ${items[i].key} overlaps ${items[j].key}; a press would open the wrong thing`,
          ).toBe(false);
        }
      }
    }
  });

  it("keeps every object inside the room it belongs to", () => {
    const layout = placeBuilding({
      rooms: [{ id: "r0", createdAt: "2026-01-01T00:00:00Z" }],
      buildingVersion: BUILDING_VERSION,
      hasUnfiled: false,
    });
    const room = layout.rooms[0];
    for (let slot = 0; slot < INTERIOR_SLOTS; slot += 1) {
      const cell = worldCellOf(room, interiorCellOf(slot));
      expect(cell.u).toBeGreaterThanOrEqual(room.origin.u);
      expect(cell.u).toBeLessThanOrEqual(room.origin.u + ROOM_SPAN);
      expect(cell.v).toBeGreaterThanOrEqual(room.origin.v);
      expect(cell.v).toBeLessThanOrEqual(room.origin.v + ROOM_SPAN);
    }
  });

  it("gives every target the same size, so size means nothing", () => {
    const layout = placeBuilding({
      rooms: [{ id: "r0", createdAt: "2026-01-01T00:00:00Z" }],
      buildingVersion: BUILDING_VERSION,
      hasUnfiled: true,
    });
    const interiors = new Map([
      [
        "r0",
        furnishRoom({
          roomId: "r0",
          artifacts: [artifact(0, "project"), artifact(1, "note")],
          artifactTotal: 9,
          hasMemories: true,
        }),
      ],
    ]);
    // Rounded to the nearest hundredth: the lattice offsets are not whole
    // numbers, so two boxes that are the same size differ in the last bit
    // of a double. That is float noise, not a difference a reader could
    // ever measure.
    const sizes = houseItems(layout, interiors).map(
      (i) =>
        `${Math.round((i.box.maxX - i.box.minX) * 100)}x${Math.round((i.box.maxY - i.box.minY) * 100)}`,
    );
    expect(new Set(sizes).size).toBe(1);
    expect(sizes[0]).toBe(`${OBJECT_FOOTPRINT.width * 100}x${MIN_OBJECT_HIT_SCENE * 100}`);
  });
});

/* ══════════════════════════════════════════════════════════════════════
   6. Reading order
   ══════════════════════════════════════════════════════════════════════ */

describe("reading order", () => {
  const layout = placeBuilding({
    rooms: [
      { id: "r0", createdAt: "2026-01-01T00:00:00Z" },
      { id: "r1", createdAt: "2026-01-01T00:01:00Z" },
    ],
    buildingVersion: BUILDING_VERSION,
    hasUnfiled: true,
  });
  const interiors = new Map([
    ["r0", furnishRoom({ roomId: "r0", artifacts: [artifact(0), artifact(1)], artifactTotal: 5, hasMemories: true })],
    ["r1", furnishRoom({ roomId: "r1", artifacts: [artifact(2)], artifactTotal: 1, hasMemories: false })],
  ]);
  const items = houseItems(layout, interiors);

  it("walks room by room, contents then memories, tray last", () => {
    expect(focusOrder(items).map((i) => i.key)).toEqual([
      "artifact:a-000",
      "artifact:a-001",
      "pile:r0",
      "memory:r0",
      "artifact:a-002",
      "unfiled",
    ]);
  });

  it("paints back to front, which is a different order", () => {
    const painted = paintOrder(items).map((i) => i.key);
    expect(painted).not.toEqual(focusOrder(items).map((i) => i.key));
    const zs = paintOrder(items).map((i) => i.z);
    expect([...zs].sort((a, b) => a - b)).toEqual(zs);
  });

  it("makes rooms scenery and never items", () => {
    // The whole point of C1.1: pressing things means pressing what is
    // inside. A room in this list would be the giant navigation card the
    // human gate rejected.
    expect(items.some((i) => i.type === ("room" as never))).toBe(false);
    for (const item of items) {
      expect(["artifact", "pile", "memory", "unfiled"]).toContain(item.type);
    }
  });
});

/* ══════════════════════════════════════════════════════════════════════
   7. The wire grouping
   ══════════════════════════════════════════════════════════════════════ */

describe("grouping the wire", () => {
  function row(over: Partial<ArtifactRow> & { artifact_id: string }): ArtifactRow {
    return {
      kind: "list",
      title: "t",
      status: "active",
      sensitivity: "normal",
      room_id: "r0",
      item_count: 0,
      item_done_count: 0,
      body_excerpt: "",
      body_truncated: false,
      created_at: "2026-03-01T00:00:00Z",
      updated_at: "2026-06-01T00:00:00Z",
      ...over,
    };
  }

  it("files each artifact under its own room", () => {
    const byRoom = interiorArtifactsByRoom([
      row({ artifact_id: "a", room_id: "r0" }),
      row({ artifact_id: "b", room_id: "r1" }),
      row({ artifact_id: "c", room_id: "r0" }),
    ]);
    expect(byRoom.get("r0")?.map((a) => a.id)).toEqual(["a", "c"]);
    expect(byRoom.get("r1")?.map((a) => a.id)).toEqual(["b"]);
  });

  it("drops unfiled rows rather than inventing a room for them", () => {
    const byRoom = interiorArtifactsByRoom([
      row({ artifact_id: "a", room_id: null }),
      row({ artifact_id: "b", room_id: "r0" }),
    ]);
    expect([...byRoom.keys()]).toEqual(["r0"]);
    expect(byRoom.get("r0")?.map((a) => a.id)).toEqual(["b"]);
  });

  it("narrows each row to an id, a kind and an instant", () => {
    const byRoom = interiorArtifactsByRoom([row({ artifact_id: "a" })]);
    expect(Object.keys(byRoom.get("r0")![0]).sort()).toEqual(["createdAt", "id", "kind"]);
  });
});
