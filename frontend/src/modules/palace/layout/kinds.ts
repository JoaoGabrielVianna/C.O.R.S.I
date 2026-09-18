/**
 * The structural vocabulary the layout works over.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   THIS FILE DECIDES WHERE A GROUP SITS. IT DECIDES NOTHING ABOUT HOW IT LOOKS
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * There are no sprites here, no colours, no assets, no component names and
 * no furniture. The mapping from a kind to a table, a drawer, a shelf or a
 * board is an approved CONCEPT and an unfrozen DESIGN, and writing it into
 * code now would freeze it by accident: the first renderer to import a
 * constant called `DRAWER` has decided what a list looks like, and every
 * later argument about it becomes a refactor instead of a discussion.
 *
 * What is here is the part the geometry needs and nothing else: which
 * groups exist, in what order, and where each one's origin is.
 *
 * ── A layout group is not a Container ──────────────────────────────────
 * These four groups are DERIVED from `Artifact.kind` on every render. They
 * have no id, no row, no lifecycle and no identity that survives a reload.
 * Nothing may start treating one as a thing the operator owns: that is the
 * `Container` abstraction, it is deliberately not in this version, and the
 * decision about it waits for evidence of use.
 */

import type { ArtifactKind } from "../api/types";

/** A logical cell on the room's grid. Not pixels. See `iso.ts`. */
export interface Cell {
  readonly u: number;
  readonly v: number;
}

/**
 * The groups, in the domain's declaration order.
 *
 * ── Why the order is the domain's and not this file's ──────────────────
 * Because it is also the reading order a screen reader will follow, and
 * two places deciding "which comes first" is one place too many. The
 * domain declares `project, list, plan, note`; so does this.
 */
export const LAYOUT_KINDS = ["project", "list", "plan", "note"] as const;

/**
 * How many cells wide a group's grid is.
 *
 * Four, so twelve individual slots fill three tidy rows and the pile
 * starts a fourth. Nothing outside this file should need to know it: the
 * engine asks for the cell of an index.
 */
export const CONTAINER_COLUMNS = 4;

/**
 * Where each group's grid begins.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   FIXED. NEVER COMPACTED. AN EMPTY GROUP LEAVES A GAP
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * ── Why a gap is the right answer ──────────────────────────────────────
 * Compacting looks tidier and is wrong. If the shelf slid into the table's
 * place whenever there were no projects, then creating the FIRST project
 * would push every other group across the room, and a person who added one
 * note would come back to a space they no longer recognise. That is I-A
 * ("editing meaning must not move space") failing at the group level, and
 * it fails on the one action most likely to be somebody's first.
 *
 * The cost is a sparser room when there is little in it. That is honest:
 * the Palace grows with what is in it, and a room with one note should
 * look like a room with one note.
 *
 * ── The stride ─────────────────────────────────────────────────────────
 * Six on both axes: four cells of grid plus two of gap. A group can reach
 * four rows (twelve slots plus a pile), so six is the smallest stride that
 * keeps two groups from ever touching.
 */
export const CONTAINER_ANCHORS: Readonly<Record<ArtifactKind, Cell>> = {
  project: { u: 0, v: 0 },
  list: { u: 6, v: 0 },
  plan: { u: 0, v: 6 },
  note: { u: 6, v: 6 },
};

/**
 * The cell an index lands on inside a group.
 *
 * Row-major from the anchor: index 0 is the anchor itself, index 4 starts
 * the second row. Pure arithmetic on the index, so the cell of the third
 * object does not depend on how many objects there are.
 */
export function cellAt(anchor: Cell, index: number): Cell {
  return {
    u: anchor.u + (index % CONTAINER_COLUMNS),
    v: anchor.v + Math.floor(index / CONTAINER_COLUMNS),
  };
}

/**
 * Where the room's memory surface sits.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   RESERVED. THE GEOMETRY OWNS THIS POSITION, NOT THE RENDERER
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * A room with at least one eligible memory gets one surface for them. It
 * is a DERIVED SCENE AFFORDANCE and nothing else: no row, no id, no
 * lifecycle, no entity. It exists in the output because it has a place,
 * and having a place is this module's entire job.
 *
 * ── Why it lives here and not in whatever draws ────────────────────────
 * Because a renderer that picked its own cell would be a second thing
 * deciding where. Two places computing position is how the notebook ends
 * up under a cabinet after somebody widens a group by one column, and the
 * failure would be visual, silent and impossible to attribute. The rule
 * is `S5 decides where`, and an exception for one object is the rule
 * being false.
 *
 * ── Why the centre, and why it cannot collide ──────────────────────────
 * The four groups sit at u,v ∈ {0, 6} and each spans four cells: twelve
 * slots in three rows of four, plus the pile opening a fourth row. So
 * every artifact cell that can EVER exist falls inside
 *
 *	u ∈ [0,3] ∪ [6,9]      v ∈ [0,3] ∪ [6,9]
 *
 * and the two-cell gap the stride leaves — u,v ∈ {4,5} — is unreachable
 * by any group at any size. `{5, 5}` is in that gap on both axes, which
 * puts the surface in the middle of the room, equidistant from all four
 * groups, and makes the collision impossible rather than unlikely.
 *
 * A test proves it exhaustively instead of trusting this paragraph: every
 * kind, every count from zero past the overflow, and the cell is never
 * taken.
 *
 * ── What it is NOT ─────────────────────────────────────────────────────
 * Not one cell per memory. Individual memories never become spatial
 * objects: hundreds of them would make a room unreadable, and the layout
 * is told only THAT there are some, never how many.
 */
export const MEMORY_SURFACE_CELL: Cell = { u: 5, v: 5 };
