/**
 * The building layout engine: where each Room sits in one compact house.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   MEANING BUILDS THE SPACE. THE SPACE NEVER OWNS THE MEANING
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * `layout()` answers "where does each object sit INSIDE a room". This file
 * answers the same question one scale up: where does each ROOM sit inside
 * the Palace. It is the same kind of function, with the same prohibitions.
 *
 * ── What the arrangement is, and what it may NOT say ───────────────────
 * Rooms fill a fixed-width grid in canonical order, row by row, sharing
 * walls. The order is `created_at ASC, id ASC`.
 *
 * That order is a MECHANISM for stability, not a message. Nothing in the
 * interface tells a reader that the third room was made third: that is an
 * implementation detail, and an interface that explains its own sort key
 * has turned a stability trick into a claim about the operator's life.
 * Two rooms sharing a wall assert nothing — not a relation, not a
 * category, not a sequence. A house has rooms next to each other because
 * it is a house.
 *
 * ── I-A, enforced by the compiler rather than by care ──────────────────
 * `BuildingRoom` has two fields: an id and a creation instant. There is no
 * `name`, no `description`, no `updatedAt`, no `sensitivity`, no `status`,
 * no count. Renaming a room cannot move the house, because a name cannot
 * be EXPRESSED to this function.
 *
 * That matters more here than it did one scale down. The read surface
 * returns rooms in `updated_at DESC` (`repo/rooms.go:82`), so a house that
 * trusted the order it was handed would rearrange itself completely every
 * time somebody fixed a typo, silently, long after the edit.
 *
 * ── I-C, no coordinate ever persists ───────────────────────────────────
 * Nothing here reads or writes a backend, an API, `localStorage`, the URL
 * or any cache. Every cell below is recomputed from the eligible set on
 * every render.
 *
 * ── Determinism, and what it forbids ───────────────────────────────────
 * No `Date.now`, no `new Date()`, no `Math.random`, no DOM measurement, no
 * viewport. A test scans this source for those names, because a comment
 * saying "pure" is not a property.
 */

import { depth } from "../layout/iso";
import type { Cell } from "../layout/kinds";

/**
 * The arrangement's version.
 *
 * Bumped to 2 by C1.1: the long single-file wing became a compact grid of
 * rooms sharing walls, so every room's cell changed. Bumping it is how the
 * whole Palace is deliberately rearranged, in one reviewed change, rather
 * than by a placement rule drifting under people.
 */
export const BUILDING_VERSION = 2;

/**
 * How many rooms stand side by side before the next row starts.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   THREE. A CONSTANT OF THE DRAWING, NEVER A FACT ABOUT THE DOMAIN
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * ── What it is not ─────────────────────────────────────────────────────
 * Not a category, not a group, not a capacity anybody owns, not a thing a
 * room belongs to, and not a number the operator can reach, change or see.
 * A row has no name, no id and no meaning.
 *
 * ── Why it must be FIXED and not derived from the count ────────────────
 * The compact-looking choice is `ceil(sqrt(n))`, and it is wrong: the
 * moment the grid's width depends on how many rooms there are, adding the
 * next room reflows the ones already placed. That is I-B failing on the
 * most ordinary action there is, and `placement.test.ts` carries a
 * mutation check for exactly that shape.
 *
 * ── Why three, and how it was chosen ───────────────────────────────────
 * By measuring, not by taste. The binding constraint is the smallest
 * object target against the 44px floor, in the stable area N3 established
 * (1088x688 at a 1440x950 window, 868x560 at 1180x800).
 *
 *	cols   1440x950            1180x800
 *	 3     60px / 51px / 44px  48px  ← five rooms survive a medium window
 *	 4     51px / 51px / 44px  40px  ← five rooms hand over at 1180
 *	       (n = 5 / 8 / 12)
 *
 * Three wins at the sizes that exist because it makes the block SQUARER,
 * and an isometric block near 2:1 wastes less of the frame than a wide
 * strip. Four was the first choice and it made the operator's own
 * five-room Palace fall back to the list on an ordinary laptop window,
 * which the browser pass caught.
 *
 * C1 used `WING_CAPACITY = 6` along a single long axis. That constant is
 * gone with the wing it described; this is not a rename of it.
 */
export const HOUSE_COLUMNS = 3;

/* ── the grid, in cells ──────────────────────────────────────────────── */

/**
 * How many cells a room's floor spans, on each axis.
 *
 * Exactly three interior pitches (see `interior.ts`). The room is not a
 * round number of cells because it is not the primitive: the OBJECT
 * lattice is. A room is three object positions wide, and its walls fall
 * halfway between the last object inside it and the first object of the
 * room next door, which is what keeps furniture clear of the wall it
 * stands against and clear of the neighbour's furniture at the same time.
 */
export const ROOM_SPAN = 5.4;

/**
 * The distance between one room's origin and the next.
 *
 * Equal to `ROOM_SPAN`, so adjacent rooms SHARE a wall line rather than
 * standing apart with ground between them. That is the whole difference
 * between a house and a row of sheds, and it is what the human gate on C1
 * was asking for.
 */
export const ROOM_STRIDE = ROOM_SPAN;

/**
 * How high a room's walls rise at house scale, in elevation steps.
 *
 * Three, not the seven the room's own interior uses. These are cutaway
 * shells seen from outside: full-height walls would hide the furniture of
 * the room behind them, and the entire point of this surface is that the
 * furniture is visible.
 */
export const ROOM_WALL_ELEVATION = 3;

/**
 * Where the unfiled tray waits: just outside the house's far corner.
 *
 * Things that have not been put away sit by the door. It keeps D4 for the
 * reason C1 gave: the tray is at the entrance precisely because it is NOT
 * a room and has nowhere inside the house to be.
 *
 * It is drawn at OBJECT size rather than room size, because the
 * interactive targets on this surface are objects now. A room-sized tray
 * would be a very large affordance that opens onto a filtered list.
 */
export const ENTRANCE_CELL: Cell = { u: -2.5, v: -2.5 };

/* ── input ───────────────────────────────────────────────────────────── */

/**
 * One Room, as the arrangement is allowed to know it.
 *
 * ── Why `createdAt` and not `updatedAt` ────────────────────────────────
 * Because creation is the one instant about a room that never changes, and
 * because `updated_at` is precisely what the wire is sorted by.
 */
export interface BuildingRoom {
  readonly id: string;
  /**
   * RFC3339 UTC, fixed width, as the Palace surface emits it.
   *
   * Compared as a STRING: for one fixed-width UTC format lexicographic
   * order IS chronological order, and it is total, so it cannot produce
   * the `NaN` an unparseable date would.
   */
  readonly createdAt: string;
}

export interface BuildingInput {
  readonly rooms: readonly BuildingRoom[];
  readonly buildingVersion: number;
  /**
   * Whether anything is unfiled.
   *
   * A boolean, and it has to stay a boolean, for the reason
   * `LayoutInput.hasMemories` is one: the arrangement needs to know THAT
   * the tray holds something, never how many. A count in here is a number
   * that eventually decides something, and every one of those is a house
   * that rearranges itself when somebody files one more thing.
   */
  readonly hasUnfiled: boolean;
}

/* ── output ──────────────────────────────────────────────────────────── */

/**
 * Where one Room stands.
 *
 * `column` and `row` are carried because the renderer draws doorways from
 * them, and recomputing the grid arithmetic inside a component would be a
 * second place deciding the arrangement.
 *
 * They are ARITHMETIC, not identity. Nothing may persist them, key a cache
 * on them, send them to a backend, or show them to a reader.
 */
export interface RoomPlacement {
  readonly roomId: string;
  readonly index: number;
  readonly column: number;
  readonly row: number;
  /** The room floor's minimum corner. The floor spans `ROOM_SPAN` cells. */
  readonly origin: Cell;
  /** Painter's depth, taken at the room's centre. Larger is nearer. */
  readonly z: number;
  /**
   * Which shared walls this room has a doorway in.
   *
   * ── A doorway is not a relation and can never become one ───────────
   * It is a consequence of two rooms standing next to each other in a
   * grid, which is a consequence of an index. It carries no id, no pair
   * of ids and nothing anybody could follow: a doorway holding two room
   * ids would be one refactor away from looking like an edge, and an edge
   * is a `Relation`. It never appears in
   * `GET /palace/artifacts/{id}/neighbors`, is never written, and never
   * enters the Core under any name — not `adjacency`, not `parent_room_id`,
   * not `floor`.
   */
  readonly doorToColumn: boolean;
  readonly doorToRow: boolean;
}

/** Where the unfiled tray waits. Present only when something is unfiled. */
export interface UnfiledPlacement {
  readonly cell: Cell;
  readonly z: number;
}

export interface BuildingLayout {
  readonly rooms: readonly RoomPlacement[];
  readonly unfiled?: UnfiledPlacement;
}

/* ── the engine ──────────────────────────────────────────────────────── */

/**
 * Arranges the Palace as one compact house.
 *
 * ── The canonical order ────────────────────────────────────────────────
 * `createdAt` ascending, ties broken by `id` ascending. The same rule
 * `layout()` uses, deliberately: two ordering rules in one product are two
 * ways for the Palace to rearrange itself.
 *
 * Ascending is what makes appending free. A new room is newer, so it sorts
 * last, so it takes the next free cell in the grid and nothing before it
 * moves.
 *
 * ── Growth ─────────────────────────────────────────────────────────────
 * Row-major into a grid `HOUSE_COLUMNS` wide. An empty cell is not drawn:
 * no ghost room, no "put a room here". This surface is read-only, and an
 * affordance that opens onto nothing is the one thing this design does not
 * draw. A house of three rooms is three rooms.
 *
 * ── Removal, and the decision that is NOT compaction ───────────────────
 * The index is a position in the ELIGIBLE list, and this function is pure
 * in that list. It therefore has no compaction step to remove: a room that
 * is not in the input never had a cell to vacate, and nothing here could
 * know a gap had opened. Preserving a gap would require remembering a
 * house that no longer exists, which means a persisted coordinate (I-C,
 * forbidden) or a `used to be here` field that does not exist on the wire.
 *
 * The observable consequence is stated plainly rather than hidden:
 * archiving the third of thirty rooms moves the twenty-seven after it one
 * cell forward, and the two before it do not move at all. That is
 * authorised for artifacts by D2 and extended to the building by the
 * proposal (§4.2); it never happens spontaneously, only ever follows a
 * deliberate act the operator has just performed, and `placement.test.ts`
 * pins both halves.
 *
 * A room the surface withholds is a different case and leaks nothing: it
 * never reaches this input, so it never had an index, so there is no hole
 * anywhere to infer it from.
 */
export function placeBuilding(input: BuildingInput): BuildingLayout {
  const ordered = dedupe([...input.rooms].sort(canonical));

  const rooms: RoomPlacement[] = ordered.map((room, index) => {
    const column = index % HOUSE_COLUMNS;
    const row = Math.floor(index / HOUSE_COLUMNS);
    const origin: Cell = { u: column * ROOM_STRIDE, v: row * ROOM_STRIDE };

    return {
      roomId: room.id,
      index,
      column,
      row,
      origin,
      // Taken at the centre so a room's depth describes its volume rather
      // than its far corner.
      z: depth({ u: origin.u + ROOM_SPAN / 2, v: origin.v + ROOM_SPAN / 2 }),
      // A doorway exists where there is a room on the other side of the
      // wall. Rooms fill row-major, so a room with a column before it
      // always has a neighbour there, and likewise for a row above.
      doorToColumn: column > 0,
      doorToRow: row > 0,
    };
  });

  if (!input.hasUnfiled) return { rooms };
  return { rooms, unfiled: { cell: ENTRANCE_CELL, z: depth(ENTRANCE_CELL) } };
}

/**
 * The canonical comparator.
 *
 * Total by construction: when the instants are equal the ids decide, and
 * ids are unique, so no two entries ever compare equal. A comparator that
 * could return 0 for distinct entries would leave their order up to the
 * sort implementation, which is a different house on a different engine.
 *
 * Exported because the room grid and the room INTERIOR must order by the
 * same rule. Two comparators would be two ways for the Palace to
 * rearrange itself.
 */
export function canonical(
  a: { readonly id: string; readonly createdAt: string },
  b: { readonly id: string; readonly createdAt: string },
): number {
  if (a.createdAt !== b.createdAt) return a.createdAt < b.createdAt ? -1 : 1;
  if (a.id !== b.id) return a.id < b.id ? -1 : 1;
  return 0;
}

/**
 * Drops repeated ids, keeping the first in CANONICAL order.
 *
 * A caller passing the same room twice is not worth refusing; the same
 * room standing in two places in the house is. First occurrence in the
 * canonical order wins, never in the input order, so the result does not
 * depend on how the caller happened to arrange its array.
 */
function dedupe(sorted: readonly BuildingRoom[]): BuildingRoom[] {
  const seen = new Set<string>();
  return sorted.filter((room) => {
    if (seen.has(room.id)) return false;
    seen.add(room.id);
    return true;
  });
}
