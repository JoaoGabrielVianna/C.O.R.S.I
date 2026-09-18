/**
 * How the geometry is presented. Not where it is.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   S5 DECIDES WHERE. THIS FILE DECIDES WHAT THAT PLACE LOOKS LIKE
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * Every cell in here arrived from `layout()`. Nothing is chosen, nudged,
 * compacted or re-derived: the only thing this module adds is a FOOTPRINT
 * (how much room a drawing needs) and a PLANE (which surface of the room
 * it is mounted on).
 *
 * ── The wall transform, and why it is not repositioning ────────────────
 * `note` is a wall board, and a board on the floor is not a board. So the
 * note group's cells are read as (column, row) inside their own group and
 * mounted on the back wall in that same order.
 *
 * That is a change of COORDINATE SYSTEM, not a decision about position:
 *
 *	· it is a pure function of (cell, anchor) and nothing else;
 *	· it depends on no title, count, content, status or viewport;
 *	· it is injective, so no two notes land on the same spot;
 *	· it preserves the canonical order S5 produced, exactly;
 *	· it never touches slot identity: the same object keeps the same
 *	  slot, the same `objectId` and the same `z`.
 *
 * If any of those stopped being true it would be S6 choosing a place, and
 * the boundary would be gone. A test asserts each one.
 */

import type { ArtifactKind } from "../api/types";
import { project, type ScenePoint } from "../layout/iso";
import { CONTAINER_COLUMNS, type Cell } from "../layout/kinds";

export type Plane = "floor" | "backWall";

/**
 * How much space a drawing needs around its anchor point, in scene units.
 *
 * `width` spreads either side of the point; `height` rises above it. These
 * describe the DRAWING, which is why they live here and not in the
 * geometry: the engine does not know that a cabinet is taller than a
 * notebook, and should not.
 */
export interface Footprint {
  readonly width: number;
  readonly height: number;
}

export interface Affordance extends Footprint {
  readonly plane: Plane;
}

/**
 * The frozen mapping.
 *
 * All four represent an Artifact and all four open the Artifact Inspector.
 * `NOTE.AcceptsItems() === false` is a rule about what a note may CONTAIN;
 * it says nothing about whether a note can be opened, and an earlier draft
 * of this design confused the two.
 */
/**
 * ── Why no dimension is below 44 ───────────────────────────────────────
 * Because `MIN_HIT_SCENE` is derived from the smallest of these, and D5
 * compares it against a 44px floor. With a notebook 28 units tall in a
 * room ~800 units wide, reaching 44px needed a container over 1250px wide
 * AND 880 tall — so the spatial view was unreachable on an ordinary
 * desktop and every room quietly handed over to its list. A browser smoke
 * found it; no amount of jsdom would have.
 *
 * The fix is to draw the objects large enough for the rule the design
 * already made, not to lower the rule. A target too small to press is an
 * affordance that lies, and moving the floor would be agreeing to that.
 */
export const AFFORDANCES: Readonly<Record<ArtifactKind, Affordance>> = {
  project: { plane: "floor", width: 60, height: 50 },
  list: { plane: "floor", width: 48, height: 66 },
  plan: { plane: "floor", width: 62, height: 52 },
  note: { plane: "backWall", width: 52, height: 46 },
};

/** The room's one memory surface. Position comes from S5.1. */
export const MEMORY_FOOTPRINT: Footprint = { width: 48, height: 44 };

/**
 * The smallest interactive dimension any target can have, in scene units.
 *
 * Derived from the footprints above rather than written down twice, so
 * shrinking an affordance cannot silently lower the touch-size floor that
 * D5 is measured against.
 */
export const MIN_HIT_SCENE = Math.min(
  MEMORY_FOOTPRINT.width,
  MEMORY_FOOTPRINT.height,
  ...Object.values(AFFORDANCES).flatMap((a) => [a.width, a.height]),
);

/** How far below its anchor point a drawing's contact shadow reaches. */
export const BASE_DEPTH = 8;

/* ── the back wall ───────────────────────────────────────────────────── */

/**
 * Where the back wall's mounting grid starts.
 *
 * The wall rises from the line `v = WALL_V`, just behind the floor, and a
 * group's rows climb it from the bottom: local row 0 sits highest, so
 * reading the wall top-to-bottom is the canonical order.
 */
const WALL_V = -1;
const WALL_U0 = 1;
const WALL_ELEVATION = 2;
/** Rows a group can reach: three of slots plus the pile's fourth. */
const WALL_ROWS = 4;

/**
 * Turns one cell into the point its drawing is anchored at.
 *
 * For `floor` this is the projection, unchanged. For `backWall` it is the
 * mounting position described above. Both are pure.
 */
export function anchorPointOf(cell: Cell, groupAnchor: Cell, plane: Plane): ScenePoint {
  if (plane === "floor") return project(cell);

  const column = cell.u - groupAnchor.u;
  const row = cell.v - groupAnchor.v;
  return project(
    { u: WALL_U0 + column, v: WALL_V },
    WALL_ELEVATION + (WALL_ROWS - 1 - row),
  );
}

/* ── the room's architecture ─────────────────────────────────────────── */

/**
 * The cutaway's extent, in cells.
 *
 * The four groups occupy `0..9` on both axes (anchors at 0 and 6, four
 * cells of span), so the floor runs one cell wider on each side and the
 * walls rise behind it. These are drawing constants: they describe the
 * room, not where anything in it goes.
 */
export const FLOOR_MIN = -1;
export const FLOOR_MAX = 10;
export const WALL_TOP_ELEVATION = 7;

/** Every corner of the floor slab, in order, for a polygon. */
export function floorCorners(): readonly Cell[] {
  return [
    { u: FLOOR_MIN, v: FLOOR_MIN },
    { u: FLOOR_MAX, v: FLOOR_MIN },
    { u: FLOOR_MAX, v: FLOOR_MAX },
    { u: FLOOR_MIN, v: FLOOR_MAX },
  ];
}

/**
 * How many columns a group spans. Re-exported so the scene can reason
 * about a group's shape without importing the geometry's internals a
 * second time.
 */
export { CONTAINER_COLUMNS };
