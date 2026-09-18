import { describe, expect, it } from "vitest";

// The three sources, as text, so the structural scans below can read the
// CODE. Imported through Vite's `?raw` rather than `node:fs`: the app's
// tsconfig types are `vite/client` only, and reaching for a node builtin
// here would mean widening a shared config for one test file.
import engineSource from "./engine.ts?raw";
import isoSource from "./iso.ts?raw";
import kindsSource from "./kinds.ts?raw";

import {
  LAYOUT_VERSION,
  VISIBLE_SLOTS,
  layout,
  type LayoutContainer,
  type LayoutInput,
  type LayoutObject,
  type LayoutOutput,
} from "./engine";
import {
  CONTAINER_ANCHORS,
  CONTAINER_COLUMNS,
  LAYOUT_KINDS,
  MEMORY_SURFACE_CELL,
  cellAt,
} from "./kinds";
import { TILE_HEIGHT, TILE_WIDTH, depth, project } from "./iso";

/**
 * The geometry, proved rather than eyeballed.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   THESE ARE THE ONLY THING STANDING BETWEEN THE ROOM AND A ROOM THAT
 *   REARRANGES ITSELF WHEN SOMEBODY FIXES A TYPO
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * A layout bug does not crash and does not look broken. It looks like a
 * room whose furniture moved, and the person who moved it cannot tell you
 * what they did — they edited a title. So the invariants get tests that
 * fail loudly, not a comment saying the engine is careful.
 */


/**
 * The source of a sibling file, WITH THE COMMENTS REMOVED.
 *
 * ── Why stripping matters ──────────────────────────────────────────────
 * The scans below look for names that must not appear in the code, and
 * every one of those names appears in the prose explaining why it must
 * not appear. A scan over the raw file tests the comments; a scan over the
 * code tests the code. The first version of these three tests failed for
 * exactly that reason, which is the cheapest possible reminder that a
 * grep-shaped test needs to know what it is grepping.
 */
function codeOf(source: string): string {
  return source.replace(/\/\*[\s\S]*?\*\//g, " ").replace(/(^|[^:])\/\/.*$/gm, "$1");
}

/* ── fixtures ────────────────────────────────────────────────────────── */

/** Objects with distinct, ascending instants. */
function objects(count: number, prefix = "a"): LayoutObject[] {
  return Array.from({ length: count }, (_, i) => ({
    id: `${prefix}-${String(i).padStart(3, "0")}`,
    createdAt: `2026-09-${String(1 + i).padStart(2, "0")}T00:00:00Z`,
  }));
}

/**
 * `hasMemories` defaults to false so every test written before S5.1 still
 * describes the room it meant to describe: one with artifacts and nothing
 * else. The memory surface has its own block below.
 */
function input(
  containers: LayoutContainer[],
  roomId = "room-1",
  hasMemories = false,
): LayoutInput {
  return { roomId, layoutVersion: LAYOUT_VERSION, containers, hasMemories };
}

/** A deterministic shuffle, so the permutation test is itself reproducible. */
function rotate<T>(items: readonly T[], by: number): T[] {
  const n = items.length;
  if (n === 0) return [];
  const k = ((by % n) + n) % n;
  return [...items.slice(k), ...items.slice(0, k)];
}

function slotsOf(out: LayoutOutput, kind: string) {
  return out.containers.find((c) => c.kind === kind)?.slots ?? [];
}

function cellOfObject(out: LayoutOutput, objectId: string) {
  for (const container of out.containers) {
    const slot = container.slots.find((s) => s.objectId === objectId);
    if (slot) return slot.cell;
  }
  return undefined;
}

/* ══════════════════════════════════════════════════════════════════════
   Determinism
   ══════════════════════════════════════════════════════════════════════ */

describe("determinism", () => {
  it("gives a deep-equal answer for the same input, twice", () => {
    const given = input([{ kind: "list", objects: objects(5) }]);
    expect(layout(given)).toEqual(layout(given));
  });

  it("does not depend on the order of the objects", () => {
    const base = objects(9);
    const reference = layout(input([{ kind: "plan", objects: base }]));

    // Every rotation, plus the reverse: the arrangement must not move.
    for (let by = 0; by < base.length; by += 1) {
      const shuffled = layout(input([{ kind: "plan", objects: rotate(base, by) }]));
      expect(shuffled).toEqual(reference);
    }
    expect(layout(input([{ kind: "plan", objects: [...base].reverse() }]))).toEqual(
      reference,
    );
  });

  it("does not depend on the order of the containers", () => {
    const groups: LayoutContainer[] = [
      { kind: "note", objects: objects(2, "n") },
      { kind: "project", objects: objects(3, "p") },
      { kind: "list", objects: objects(1, "l") },
    ];
    const reference = layout(input(groups));
    for (let by = 0; by < groups.length; by += 1) {
      expect(layout(input(rotate(groups, by)))).toEqual(reference);
    }
    // And the output is always in the vocabulary's order, never the input's.
    expect(reference.containers.map((c) => c.kind)).toEqual(["project", "list", "note"]);
  });

  it("does not depend on the room id", () => {
    const groups: LayoutContainer[] = [{ kind: "list", objects: objects(4) }];
    const a = layout(input(groups, "room-a"));
    const b = layout(input(groups, "room-b"));
    // Two rooms holding the same objects arrange them identically. Hashing
    // the room id into the positions would make every room subtly
    // different for a reason nobody could predict or explain.
    expect(a).toEqual(b);
  });

  it("merges a kind that arrives twice instead of laying it out twice", () => {
    const split = layout(
      input([
        { kind: "list", objects: objects(2, "x").slice(0, 1) },
        { kind: "list", objects: objects(2, "x").slice(1) },
      ]),
    );
    const whole = layout(input([{ kind: "list", objects: objects(2, "x") }]));
    expect(split).toEqual(whole);
  });

  it("never places the same object in two cells", () => {
    const duplicated = objects(3);
    const out = layout(
      input([{ kind: "list", objects: [...duplicated, ...duplicated] }]),
    );
    const ids = slotsOf(out, "list").map((s) => s.objectId);
    expect(new Set(ids).size).toBe(ids.length);
    expect(ids).toHaveLength(3);
  });
});

/* ══════════════════════════════════════════════════════════════════════
   I-A · editing meaning must not move space
   ══════════════════════════════════════════════════════════════════════ */

describe("I-A · meaning cannot reach the geometry", () => {
  it("ignores every field outside the contract, even when they disagree", () => {
    // The compiler is the first guard: these objects do not type-check
    // without the cast, which is what makes I-A structural rather than
    // aspirational. The cast simulates a future `any` sneaking through.
    //
    // ── Why three objects and not one ──────────────────────────────────
    // With one object there is no order to get wrong, so a single-object
    // version of this test passes against an engine that sorts by
    // `updatedAt`. It did: a mutation adding `updatedAt` to the contract
    // and preferring it survived the whole suite. The three below have
    // `updatedAt` in the exact REVERSE of their `createdAt` order, so any
    // engine that so much as glances at it produces a different room.
    const contaminated = [
      {
        id: "a",
        createdAt: "2026-09-01T00:00:00Z",
        updatedAt: "2026-12-31T00:00:00Z",
        title: "editada por último",
        status: "archived",
        sensitivity: "private",
        itemCount: 42,
      },
      {
        id: "b",
        createdAt: "2026-09-02T00:00:00Z",
        updatedAt: "2026-11-30T00:00:00Z",
        title: "editada no meio",
        status: "active",
        sensitivity: "normal",
        itemCount: 0,
      },
      {
        id: "c",
        createdAt: "2026-09-03T00:00:00Z",
        updatedAt: "2026-10-01T00:00:00Z",
        title: "editada primeiro",
        status: "active",
        sensitivity: "normal",
        itemCount: 7,
      },
    ] as unknown as LayoutObject[];

    const clean: LayoutObject[] = [
      { id: "a", createdAt: "2026-09-01T00:00:00Z" },
      { id: "b", createdAt: "2026-09-02T00:00:00Z" },
      { id: "c", createdAt: "2026-09-03T00:00:00Z" },
    ];

    const dirty = layout(input([{ kind: "list", objects: contaminated }]));
    expect(dirty).toEqual(layout(input([{ kind: "list", objects: clean }])));
    // And spelled out, so a failure says what went wrong: creation order.
    expect(slotsOf(dirty, "list").map((s) => s.objectId)).toEqual(["a", "b", "c"]);
  });

  it("names no excluded field anywhere in its code", () => {
    // The cheap tripwire beside the behavioural one above. A field added
    // to the contract is caught here before anybody has to reason about
    // whether the sort reads it.
    const source = codeOf(engineSource);
    for (const excluded of [
      "updatedAt",
      "title",
      "body",
      "status",
      "sensitivity",
      "itemCount",
      "description",
      "content",
      "viewport",
    ]) {
      expect(source, `engine.ts names ${excluded}`).not.toContain(excluded);
    }
  });

  it("puts nothing in the output that a renderer does not need", () => {
    const out = layout(input([{ kind: "list", objects: objects(13) }]));
    const encoded = JSON.stringify(out);

    // The output carries ids, cells, depths, a kind and a count. Anything
    // semantic in here would be content leaking into geometry, and a
    // renderer would start reading it from the wrong place.
    for (const leak of ["title", "body", "summary", "status", "sensitivity", "updatedAt"]) {
      expect(encoded).not.toContain(leak);
    }
    const slot = slotsOf(out, "list")[0];
    expect(Object.keys(slot).sort()).toEqual(["cell", "objectId", "z"]);
  });

  it("has no clock and no randomness in its source", () => {
    // I5: a comment claiming purity is not a property. This reads the code.
    const source = codeOf(engineSource);
    for (const forbidden of ["Date.now", "new Date(", "Math.random", "performance.now"]) {
      expect(source).not.toContain(forbidden);
    }
    // And nothing that would persist or fetch a coordinate (I-C).
    for (const forbidden of ["localStorage", "sessionStorage", "indexedDB", "fetch(", "window."]) {
      expect(source).not.toContain(forbidden);
    }
  });
});

/* ══════════════════════════════════════════════════════════════════════
   Canonical ordering
   ══════════════════════════════════════════════════════════════════════ */

describe("canonical ordering", () => {
  it("orders by createdAt ascending", () => {
    const out = layout(
      input([
        {
          kind: "list",
          objects: [
            { id: "c", createdAt: "2026-09-03T00:00:00Z" },
            { id: "a", createdAt: "2026-09-01T00:00:00Z" },
            { id: "b", createdAt: "2026-09-02T00:00:00Z" },
          ],
        },
      ]),
    );
    expect(slotsOf(out, "list").map((s) => s.objectId)).toEqual(["a", "b", "c"]);
  });

  it("breaks a tie on id, so equal instants are still a total order", () => {
    const same = "2026-09-01T00:00:00Z";
    const out = layout(
      input([
        {
          kind: "list",
          objects: [
            { id: "zulu", createdAt: same },
            { id: "alpha", createdAt: same },
            { id: "mike", createdAt: same },
          ],
        },
      ]),
    );
    expect(slotsOf(out, "list").map((s) => s.objectId)).toEqual(["alpha", "mike", "zulu"]);
  });

  it("keeps tied objects in place when an unrelated one is edited", () => {
    // Without the id tie-break the order of these two would depend on the
    // order the backend returned them, which is `updated_at DESC` — so an
    // edit to either would swap them on screen.
    const same = "2026-09-01T00:00:00Z";
    const pair: LayoutObject[] = [
      { id: "bbb", createdAt: same },
      { id: "aaa", createdAt: same },
    ];
    const first = layout(input([{ kind: "list", objects: pair }]));
    const afterServerReordered = layout(
      input([{ kind: "list", objects: [...pair].reverse() }]),
    );
    expect(afterServerReordered).toEqual(first);
  });
});

/* ══════════════════════════════════════════════════════════════════════
   I-B · adding must not move what is already there
   ══════════════════════════════════════════════════════════════════════ */

describe("I-B · append is free", () => {
  it("does not move any existing slot when an object is added", () => {
    for (let n = 0; n < VISIBLE_SLOTS + 4; n += 1) {
      const before = objects(n);
      const after = [
        ...before,
        { id: "zz-new", createdAt: "2026-12-31T23:59:59Z" },
      ];

      const outBefore = layout(input([{ kind: "list", objects: before }]));
      const outAfter = layout(input([{ kind: "list", objects: after }]));

      for (const slot of slotsOf(outBefore, "list")) {
        expect(
          cellOfObject(outAfter, slot.objectId),
          `object ${slot.objectId} moved when a newer one was added to a group of ${n}`,
        ).toEqual(slot.cell);
      }
    }
  });

  it("keeps the twelfth object's slot when the thirteenth arrives", () => {
    // The boundary the frozen spec got wrong. With "eleven slots and a
    // pile in the twelfth", this transition would take a drawn object's
    // cell away and drop it into the pile.
    const twelve = objects(VISIBLE_SLOTS);
    const thirteen = [...twelve, { id: "zz", createdAt: "2026-12-31T00:00:00Z" }];

    const before = layout(input([{ kind: "list", objects: twelve }]));
    const after = layout(input([{ kind: "list", objects: thirteen }]));

    expect(slotsOf(before, "list")).toHaveLength(VISIBLE_SLOTS);
    expect(slotsOf(after, "list")).toHaveLength(VISIBLE_SLOTS);
    expect(slotsOf(after, "list")).toEqual(slotsOf(before, "list"));
  });

  it("does not move slots when objects are added on top of an overflow", () => {
    const base = objects(VISIBLE_SLOTS + 5);
    const before = layout(input([{ kind: "list", objects: base }]));

    const grown = [
      ...base,
      ...Array.from({ length: 7 }, (_, i) => ({
        id: `zz-${i}`,
        createdAt: `2026-12-${String(1 + i).padStart(2, "0")}T00:00:00Z`,
      })),
    ];
    const after = layout(input([{ kind: "list", objects: grown }]));

    expect(slotsOf(after, "list")).toEqual(slotsOf(before, "list"));
    // Only the pile grew.
    const container = after.containers.find((c) => c.kind === "list");
    expect(container?.pile?.count).toBe(grown.length - VISIBLE_SLOTS);
    expect(container?.pile?.cell).toEqual(
      layout(input([{ kind: "list", objects: base }])).containers.find(
        (c) => c.kind === "list",
      )?.pile?.cell,
    );
  });
});

/* ══════════════════════════════════════════════════════════════════════
   Anchors
   ══════════════════════════════════════════════════════════════════════ */

describe("anchors", () => {
  it("gives every kind a fixed origin, whatever else is in the room", () => {
    const alone = layout(input([{ kind: "note", objects: objects(1) }]));
    const crowded = layout(
      input([
        { kind: "project", objects: objects(3, "p") },
        { kind: "list", objects: objects(3, "l") },
        { kind: "plan", objects: objects(3, "q") },
        { kind: "note", objects: objects(1) },
      ]),
    );

    const noteAlone = alone.containers.find((c) => c.kind === "note");
    const noteCrowded = crowded.containers.find((c) => c.kind === "note");
    expect(noteAlone?.anchor).toEqual(noteCrowded?.anchor);
    expect(noteAlone?.slots).toEqual(noteCrowded?.slots);
  });

  it("does not compact when a kind is empty", () => {
    // `list` must sit where `list` sits, whether or not `project` exists.
    const withoutProjects = layout(input([{ kind: "list", objects: objects(2) }]));
    expect(withoutProjects.containers.find((c) => c.kind === "list")?.anchor).toEqual(
      CONTAINER_ANCHORS.list,
    );
  });

  it("does not move other kinds when the first object of an empty kind appears", () => {
    const before = layout(
      input([
        { kind: "list", objects: objects(4, "l") },
        { kind: "note", objects: objects(2, "n") },
      ]),
    );
    const after = layout(
      input([
        { kind: "project", objects: [{ id: "p-000", createdAt: "2026-10-01T00:00:00Z" }] },
        { kind: "list", objects: objects(4, "l") },
        { kind: "note", objects: objects(2, "n") },
      ]),
    );

    for (const kind of ["list", "note"] as const) {
      expect(
        after.containers.find((c) => c.kind === kind),
        `${kind} moved when the first project was created`,
      ).toEqual(before.containers.find((c) => c.kind === kind));
    }
  });

  it("omits a kind with no objects rather than emitting an empty group", () => {
    const out = layout(input([{ kind: "list", objects: objects(1) }]));
    expect(out.containers.map((c) => c.kind)).toEqual(["list"]);
    expect(layout(input([])).containers).toEqual([]);
    expect(layout(input([{ kind: "list", objects: [] }])).containers).toEqual([]);
  });

  it("never lets two kinds share a cell", () => {
    const out = layout(
      input(LAYOUT_KINDS.map((kind) => ({ kind, objects: objects(13, kind) }))),
    );
    const seen = new Set<string>();
    for (const container of out.containers) {
      for (const slot of container.slots) {
        const key = `${slot.cell.u},${slot.cell.v}`;
        expect(seen.has(key), `cell ${key} is used twice`).toBe(false);
        seen.add(key);
      }
      if (container.pile) {
        const key = `${container.pile.cell.u},${container.pile.cell.v}`;
        expect(seen.has(key), `pile cell ${key} collides`).toBe(false);
        seen.add(key);
      }
    }
  });
});

/* ══════════════════════════════════════════════════════════════════════
   Overflow
   ══════════════════════════════════════════════════════════════════════ */

describe("overflow", () => {
  it("has no pile at or below capacity", () => {
    for (const n of [0, 1, VISIBLE_SLOTS - 1, VISIBLE_SLOTS]) {
      const out = layout(input([{ kind: "list", objects: objects(n) }]));
      const container = out.containers.find((c) => c.kind === "list");
      if (n === 0) {
        expect(container).toBeUndefined();
        continue;
      }
      expect(container?.slots).toHaveLength(n);
      expect(container?.pile).toBeUndefined();
    }
  });

  it("puts the remainder in a pile beyond capacity", () => {
    const n = VISIBLE_SLOTS + 8;
    const out = layout(input([{ kind: "list", objects: objects(n) }]));
    const container = out.containers.find((c) => c.kind === "list");

    expect(container?.slots).toHaveLength(VISIBLE_SLOTS);
    expect(container?.pile?.count).toBe(n - VISIBLE_SLOTS);
    // The pile sits in the next cell along, not on top of a slot.
    expect(container?.pile?.cell).toEqual(cellAt(CONTAINER_ANCHORS.list, VISIBLE_SLOTS));
  });

  it("gives the pile the oldest objects' cells to nobody: the newest overflow", () => {
    const n = VISIBLE_SLOTS + 3;
    const all = objects(n);
    const out = layout(input([{ kind: "list", objects: all }]));
    const shown = slotsOf(out, "list").map((s) => s.objectId);
    // The canonical order decides who is visible: the oldest twelve.
    expect(shown).toEqual(all.slice(0, VISIBLE_SLOTS).map((o) => o.id));
  });

  it("does not paginate: there is one pile and no page number", () => {
    const out = layout(input([{ kind: "list", objects: objects(200) }]));
    const container = out.containers.find((c) => c.kind === "list");
    expect(container?.pile?.count).toBe(200 - VISIBLE_SLOTS);
    expect(JSON.stringify(container)).not.toContain("page");
  });
});

/* ══════════════════════════════════════════════════════════════════════
   Projection
   ══════════════════════════════════════════════════════════════════════ */

describe("iso projection", () => {
  it("is pure", () => {
    const cell = { u: 3, v: 5 };
    expect(project(cell)).toEqual(project(cell));
    expect(project(cell, 2)).toEqual(project(cell, 2));
  });

  it("puts the origin at the origin", () => {
    expect(project({ u: 0, v: 0 })).toEqual({ x: 0, y: 0 });
  });

  it("uses the 2:1 isometric basis", () => {
    // +u goes right and down by half a tile each.
    expect(project({ u: 1, v: 0 })).toEqual({ x: TILE_WIDTH / 2, y: TILE_HEIGHT / 2 });
    // +v goes left and down.
    expect(project({ u: 0, v: 1 })).toEqual({ x: -TILE_WIDTH / 2, y: TILE_HEIGHT / 2 });
    // The diagonal has no horizontal component.
    expect(project({ u: 1, v: 1 })).toEqual({ x: 0, y: TILE_HEIGHT });
  });

  it("lifts with elevation and nothing else", () => {
    const flat = project({ u: 2, v: 2 });
    const lifted = project({ u: 2, v: 2 }, 1);
    expect(lifted.x).toBe(flat.x);
    expect(lifted.y).toBeLessThan(flat.y);
  });

  it("orders depth so that nearer cells sort later", () => {
    expect(depth({ u: 0, v: 0 })).toBeLessThan(depth({ u: 1, v: 0 }));
    expect(depth({ u: 1, v: 0 })).toBe(depth({ u: 0, v: 1 }));
  });

  it("agrees with the depth the engine stamps on a slot", () => {
    const out = layout(input([{ kind: "plan", objects: objects(6) }]));
    for (const slot of slotsOf(out, "plan")) {
      expect(slot.z).toBe(depth(slot.cell));
    }
  });

  it("touches no browser API", () => {
    const source = codeOf(isoSource).toLowerCase();
    for (const forbidden of ["document", "window", "react", "svg", "canvas", "getboundingclientrect"]) {
      expect(source, `iso.ts reaches for ${forbidden}`).not.toContain(forbidden);
    }
  });
});

/* ══════════════════════════════════════════════════════════════════════
   The vocabulary carries no art direction
   ══════════════════════════════════════════════════════════════════════ */

describe("kinds", () => {
  it("anchors every kind in the domain vocabulary", () => {
    for (const kind of LAYOUT_KINDS) {
      expect(CONTAINER_ANCHORS[kind]).toBeDefined();
    }
    expect(Object.keys(CONTAINER_ANCHORS).sort()).toEqual([...LAYOUT_KINDS].sort());
  });

  it("names no furniture, no sprite and no colour", () => {
    // The concept is approved; the art direction is not frozen. A constant
    // called DRAWER here would decide what a list looks like by accident,
    // and every later conversation about it becomes a refactor.
    const source = codeOf(kindsSource).toLowerCase();
    for (const art of [
      "sprite",
      "asset",
      "colour",
      "#fff",
      "mesa",
      "gaveta",
      "prateleira",
      "quadro",
      "caderno",
      "baú",
    ]) {
      expect(source, `kinds.ts mentions ${art}`).not.toContain(art);
    }
  });
});

/* ══════════════════════════════════════════════════════════════════════
   S5.1 · the room's memory surface
   ══════════════════════════════════════════════════════════════════════ */

describe("memory surface", () => {
  const withMemories = (containers: LayoutContainer[] = []) =>
    layout(input(containers, "room-1", true));
  const withoutMemories = (containers: LayoutContainer[] = []) =>
    layout(input(containers, "room-1", false));

  it("does not exist when the room has no eligible memories", () => {
    expect(withoutMemories().memorySurface).toBeUndefined();
    expect(withoutMemories([{ kind: "list", objects: objects(5) }]).memorySurface)
      .toBeUndefined();
  });

  it("exists exactly once when the room has any", () => {
    const out = withMemories();
    expect(out.memorySurface).toBeDefined();
    // One surface, whatever else is in the room. Never one per memory:
    // hundreds of individual objects would make a room unreadable, which
    // is why the input carries a flag and not a count.
    expect(Object.keys(out).filter((k) => k === "memorySurface")).toHaveLength(1);
  });

  it("sits where the geometry says, not where a renderer decides", () => {
    const out = withMemories();
    expect(out.memorySurface?.cell).toEqual(MEMORY_SURFACE_CELL);
    expect(out.memorySurface?.z).toBe(depth(MEMORY_SURFACE_CELL));
  });

  it("is deterministic, and independent of the room and of the objects", () => {
    const a = layout(input([{ kind: "note", objects: objects(3) }], "room-a", true));
    const b = layout(input([{ kind: "plan", objects: objects(9) }], "room-b", true));
    expect(a.memorySurface).toEqual(b.memorySurface);
    expect(withMemories()).toEqual(withMemories());
  });

  it("carries a position and nothing else", () => {
    // No count, no ids, no content. A cardinality here would be a number
    // that eventually decides something, and the layout has no business
    // knowing how much the operator has written down.
    expect(Object.keys(withMemories().memorySurface ?? {}).sort()).toEqual(["cell", "z"]);
  });

  it("is not in the input contract as a count", () => {
    // The tripwire beside the behavioural tests: a field that carries
    // cardinality would let a future reader size or multiply the surface.
    const source = codeOf(engineSource);
    for (const forbidden of ["memoryCount", "memory_count", "memories.length"]) {
      expect(source, `the input contract names ${forbidden}`).not.toContain(forbidden);
    }
    expect(source).toContain("hasMemories");
  });
});

describe("memory surface · it moves nothing", () => {
  const groups: LayoutContainer[] = [
    { kind: "project", objects: objects(3, "p") },
    { kind: "list", objects: objects(VISIBLE_SLOTS + 4, "l") },
    { kind: "plan", objects: objects(7, "q") },
    { kind: "note", objects: objects(1, "n") },
  ];

  it("does not move an artifact when the first memory appears", () => {
    const before = layout(input(groups, "room-1", false));
    const after = layout(input(groups, "room-1", true));
    expect(after.containers).toEqual(before.containers);
  });

  it("does not move an artifact when the last memory goes", () => {
    const before = layout(input(groups, "room-1", true));
    const after = layout(input(groups, "room-1", false));
    expect(after.containers).toEqual(before.containers);
  });

  it("does not compact anchors, change slots or change piles", () => {
    const with_ = layout(input(groups, "room-1", true));
    const without = layout(input(groups, "room-1", false));
    for (const kind of LAYOUT_KINDS) {
      const a = with_.containers.find((c) => c.kind === kind);
      const b = without.containers.find((c) => c.kind === kind);
      expect(a?.anchor).toEqual(b?.anchor);
      expect(a?.slots).toEqual(b?.slots);
      expect(a?.pile).toEqual(b?.pile);
    }
  });

  it("does not move when an artifact is appended", () => {
    const before = layout(input(groups, "room-1", true));
    const grown = groups.map((g) =>
      g.kind === "list"
        ? { ...g, objects: [...g.objects, { id: "zz", createdAt: "2026-12-31T00:00:00Z" }] }
        : g,
    );
    const after = layout(input(grown, "room-1", true));
    expect(after.memorySurface).toEqual(before.memorySurface);
  });
});

describe("memory surface · the reserved cell cannot be taken", () => {
  /**
   * The exhaustive proof, rather than the paragraph in `kinds.ts`.
   *
   * Every kind, every count from empty to well past the overflow, and the
   * reserved cell is never occupied by a slot or by a pile. This is what
   * makes the collision impossible instead of unlikely: widening a group
   * past the gap would fail here rather than putting a notebook under a
   * cabinet on somebody's screen.
   */
  const reserved = `${MEMORY_SURFACE_CELL.u},${MEMORY_SURFACE_CELL.v}`;

  it("is never used by any group, at any size", () => {
    for (const kind of LAYOUT_KINDS) {
      for (let count = 0; count <= VISIBLE_SLOTS + 40; count += 1) {
        const out = layout(input([{ kind, objects: objects(count, kind) }], "room-1", true));
        const container = out.containers.find((c) => c.kind === kind);
        for (const slot of container?.slots ?? []) {
          expect(`${slot.cell.u},${slot.cell.v}`, `${kind} slot took the reserved cell at ${count}`)
            .not.toBe(reserved);
        }
        if (container?.pile) {
          expect(`${container.pile.cell.u},${container.pile.cell.v}`,
            `${kind} pile took the reserved cell at ${count}`).not.toBe(reserved);
        }
      }
    }
  });

  it("is never used when every group is full at once", () => {
    const out = layout(
      input(
        LAYOUT_KINDS.map((kind) => ({ kind, objects: objects(VISIBLE_SLOTS + 10, kind) })),
        "room-1",
        true,
      ),
    );
    const taken = new Set<string>();
    for (const container of out.containers) {
      for (const slot of container.slots) taken.add(`${slot.cell.u},${slot.cell.v}`);
      if (container.pile) taken.add(`${container.pile.cell.u},${container.pile.cell.v}`);
    }
    expect(taken.has(reserved)).toBe(false);
    expect(out.memorySurface?.cell).toEqual(MEMORY_SURFACE_CELL);
  });

  it("sits in the gap the stride leaves, on both axes", () => {
    // The structural reason the exhaustive test above passes. A group
    // spans CONTAINER_COLUMNS cells from its anchor; the anchors are at
    // 0 and 6; so 4 and 5 are unreachable on both axes.
    const spans = Object.values(CONTAINER_ANCHORS).map((a) => [a.u, a.u + CONTAINER_COLUMNS - 1]);
    for (const [from, to] of spans) {
      const inside = MEMORY_SURFACE_CELL.u >= from && MEMORY_SURFACE_CELL.u <= to;
      const insideV = MEMORY_SURFACE_CELL.v >= from && MEMORY_SURFACE_CELL.v <= to;
      expect(inside && insideV).toBe(false);
    }
  });
});

describe("memory surface · nothing persists", () => {
  it("is recomputed, never stored", () => {
    const source = codeOf(engineSource) + codeOf(kindsSource);
    for (const forbidden of ["localStorage", "sessionStorage", "indexedDB", "fetch("]) {
      expect(source).not.toContain(forbidden);
    }
  });
});
