/**
 * What stands inside a room, at house scale.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   EVERY STORAGE-LOOKING OBJECT IS A REAL ARTIFACT. THERE IS NO PROP
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * This is the rule the whole surface stands on, and it is the one a future
 * change is most likely to break by accident, because breaking it makes
 * the picture look BETTER.
 *
 * A drawer cabinet in this house is an Artifact of kind `list`. If a room
 * holds no list, no cabinet is drawn: not a dimmed one, not an empty one,
 * not a decorative one standing in the corner. A cabinet somebody can
 * press that corresponds to nothing is an affordance that lies, and a
 * cabinet they cannot press but can see is a claim that the room holds
 * something it does not.
 *
 * Decoration still exists on this surface (floor treatment, wall tone) and
 * is still seeded only by `room_id`. It is unmistakably NOT storage and
 * never interactive. The boundary is: if it looks like it holds something,
 * it holds something.
 *
 * ── I-A at object scale ────────────────────────────────────────────────
 * `InteriorArtifact` carries an id, a kind and a creation instant. There
 * is no title, no body, no excerpt, no `updatedAt`, no item count and no
 * status. Renaming an artifact cannot move its furniture, because a title
 * cannot be expressed to this function.
 *
 * `kind` is here and the others are not, and that is not an inconsistency:
 * kind decides WHICH SHAPE is drawn and never WHERE it stands. Changing a
 * list into a plan changes a cabinet into a shelf in the same spot.
 * Positions come from the canonical order alone.
 *
 * ── Why position does not group by kind, unlike the room's interior ────
 * `layout()` gives each kind its own anchor because a room's own scene has
 * forty cells to spend and four groups read better than one heap. A room
 * at house scale has NINE positions. Reserving a quadrant per kind would
 * mean a room with four lists shows one list and three empty quadrants.
 *
 * So objects fill the positions in canonical order and the kind decides
 * the drawing. This is strictly MORE stable than grouping: adding a note
 * to a room full of lists cannot displace a list, because there is no
 * per-kind region for it to grow into.
 */

import type { ArtifactKind } from "../api/types";
import { canonical } from "./placement";

/* ── the room's own grid ─────────────────────────────────────────────── */

/**
 * How many positions a room has, on each axis.
 *
 * Three by three. Not a page size and not a capacity anybody owns: it is
 * how many objects fit in a room without their screen-space
 * boxes touching, which `interior.test.ts` proves exhaustively rather than
 * trusting.
 */
export const INTERIOR_COLUMNS = 3;

/**
 * The distance between two neighbouring object positions, in cells.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   ONE LATTICE FOR THE WHOLE HOUSE, WALLS INCLUDED
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * `ROOM_SPAN` is exactly three pitches and every object sits at the centre
 * of its third, so the distance from the last object in one room to the
 * first object in the next is also exactly one pitch. Every object in the
 * Palace therefore lands on ONE global lattice of this pitch, and whether
 * two objects share a room stops mattering to the geometry.
 *
 * That is what makes the disjointness argument short enough to trust. An
 * isometric diamond is twice as wide as it is tall, so the gap that
 * matters is in the PROJECTION, not in the grid. For any two distinct
 * lattice points separated by `(a, b)`, both multiples of the pitch:
 *
 *	a = b    the points differ only in depth, and are 32·|a| apart in y
 *	a ≠ b    they are 32·|a − b| apart in x, and |a − b| >= one pitch
 *
 * With a pitch of 1.8 both cases give at least 57.6 scene units, against
 * an object box of 52. Every pair in the house clears by 5.6 units on at
 * least one axis, and `interior.test.ts` checks every pair exhaustively
 * rather than trusting this paragraph.
 *
 * ── Do not "tidy" this number ──────────────────────────────────────────
 * Rounding the pitch down to 1.5, or the span up to 6, breaks the lattice
 * and produces objects whose targets overlap across a shared wall. The
 * symptom is a press landing on the neighbouring room's furniture, on some
 * screen sizes only.
 */
export const INTERIOR_PITCH = 1.8;

/**
 * Where each position sits inside the room, in cells from its origin.
 *
 * The centre of each third: half a pitch in, then one pitch apart. Being
 * centred is what leaves furniture clear of the walls on both sides.
 */
export const SLOT_OFFSETS = [0.5, 1.5, 2.5].map((n) => n * INTERIOR_PITCH);

/** Every position a room has, in row-major order. */
export const INTERIOR_SLOTS = INTERIOR_COLUMNS * INTERIOR_COLUMNS;

/**
 * How many artifacts a room shows individually before the rest becomes a
 * pile.
 *
 * Seven, which is `INTERIOR_SLOTS` minus the pile's own position and minus
 * the memory surface's reserved one.
 */
export const VISIBLE_OBJECTS = 7;

/**
 * The pile's position, and the memory surface's.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   BOTH RESERVED. NEITHER IS EVER BORROWED FROM AN ARTIFACT
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * The pile gets a position of its own rather than taking the last
 * artifact's, for the reason amendment A5 records one scale down: if the
 * pile borrowed the seventh slot, going from seven artifacts to eight
 * would take the seventh object's place away and drop it into the pile,
 * moving something that was already drawn.
 *
 * The memory surface's position is reserved WHETHER OR NOT the room has
 * memories. If artifacts could spread into it when a room had none, then
 * writing the room's first memory would move a piece of furniture, which
 * is S5.1's lesson exactly: the notebook's place belongs to the geometry,
 * and geometry does not negotiate with content.
 */
export const PILE_SLOT = 7;
export const MEMORY_SLOT = 8;

/** A position inside a room, in cells from the room's origin. */
export interface InteriorCell {
  readonly du: number;
  readonly dv: number;
}

/**
 * The cell a position index lands on.
 *
 * Row-major: 0, 1, 2 across the back, then the next row toward the viewer.
 * Pure arithmetic on the index, so the position of the third object does
 * not depend on how many objects there are.
 */
export function interiorCellOf(slot: number): InteriorCell {
  return {
    du: SLOT_OFFSETS[slot % INTERIOR_COLUMNS],
    dv: SLOT_OFFSETS[Math.floor(slot / INTERIOR_COLUMNS)],
  };
}

/* ── input ───────────────────────────────────────────────────────────── */

/**
 * One artifact, as the room's furniture is allowed to know it.
 *
 * See the header for why `kind` is here and `title` is not.
 */
export interface InteriorArtifact {
  readonly id: string;
  readonly kind: ArtifactKind;
  readonly createdAt: string;
}

export interface InteriorInput {
  readonly roomId: string;
  /**
   * The eligible active artifacts of this room that the caller actually
   * holds. May be fewer than `artifactTotal`: see below.
   */
  readonly artifacts: readonly InteriorArtifact[];
  /**
   * How many eligible active artifacts this room has, according to the
   * read surface.
   *
   * ══════════════════════════════════════════════════════════════════
   *
   *	THE PILE COUNTS FROM HERE, NOT FROM THE ARRAY ABOVE
   *
   * ══════════════════════════════════════════════════════════════════
   *
   * This is `OverviewRoom.artifact_count`, computed by the backend under
   * the same visibility predicate as the listing itself, so it is a fact
   * about what the reader could reach and never a count of withheld
   * content.
   *
   * Counting the pile from here rather than from `artifacts.length` is
   * what keeps the pile honest when the caller's page did not reach every
   * artifact: the remainder is still exactly "how many are not drawn
   * individually", whether they were capped by `VISIBLE_OBJECTS` or never
   * fetched at all.
   */
  readonly artifactTotal: number;
  /**
   * Whether this room has at least one eligible memory.
   *
   * A boolean, never a count, for the reason `LayoutInput.hasMemories`
   * gives: the geometry decides whether a surface exists and must never
   * learn how many memories are on it. A count here would eventually
   * decide something — a bigger notebook, a second one — and each of
   * those is a room that rearranges itself when somebody writes one more
   * thing down.
   */
  readonly hasMemories: boolean;
}

/* ── output ──────────────────────────────────────────────────────────── */

export interface InteriorObject {
  readonly artifactId: string;
  readonly kind: ArtifactKind;
  readonly slot: number;
  readonly local: InteriorCell;
}

export interface InteriorPile {
  /** How many artifacts are not drawn individually. Always at least one. */
  readonly count: number;
  readonly slot: number;
  readonly local: InteriorCell;
}

export interface InteriorMemory {
  readonly slot: number;
  readonly local: InteriorCell;
}

export interface InteriorOutput {
  readonly objects: readonly InteriorObject[];
  readonly pile?: InteriorPile;
  readonly memory?: InteriorMemory;
}

/**
 * Furnishes one room.
 *
 * ── Canonical order, and what it costs ─────────────────────────────────
 * `createdAt` ascending, ties by `id`, the same comparator the room grid
 * uses. Appending is free: a new artifact is newer, sorts last, takes the
 * next free position, and nothing already drawn moves.
 *
 * Removing is not free, and the cost is recorded rather than hidden:
 * archiving the second of five artifacts moves the three after it one
 * position forward. That is the same trade the room grid makes, for the
 * same reason — the alternative is a persisted coordinate — and
 * `interior.test.ts` pins both halves.
 *
 * ── Absent rather than empty ───────────────────────────────────────────
 * A room with no artifacts produces no objects and no pile. A room with no
 * memories produces no surface. Nothing is emitted as a placeholder,
 * because an affordance that opens onto nothing is what this design
 * refuses, and because a placeholder is indistinguishable from a claim
 * that something is being withheld.
 */
export function furnishRoom(input: InteriorInput): InteriorOutput {
  const ordered = dedupe([...input.artifacts].sort(canonical));
  const visible = ordered.slice(0, VISIBLE_OBJECTS);

  const objects: InteriorObject[] = visible.map((artifact, slot) => ({
    artifactId: artifact.id,
    kind: artifact.kind,
    slot,
    local: interiorCellOf(slot),
  }));

  // From the read surface's count, never from the array. See the field.
  // Clamped at zero: a caller holding more rows than the count claims is a
  // caller with stale data, and a negative pile would render as nonsense.
  const remainder = Math.max(0, input.artifactTotal - objects.length);

  const out: InteriorOutput = {
    objects,
    ...(remainder > 0
      ? { pile: { count: remainder, slot: PILE_SLOT, local: interiorCellOf(PILE_SLOT) } }
      : {}),
    ...(input.hasMemories
      ? { memory: { slot: MEMORY_SLOT, local: interiorCellOf(MEMORY_SLOT) } }
      : {}),
  };

  return out;
}

/**
 * Drops repeated ids, keeping the first in CANONICAL order.
 *
 * The same object standing in two places in one room would make the room
 * show one thing as two and make the pile's arithmetic wrong.
 */
function dedupe(sorted: readonly InteriorArtifact[]): InteriorArtifact[] {
  const seen = new Set<string>();
  return sorted.filter((artifact) => {
    if (seen.has(artifact.id)) return false;
    seen.add(artifact.id);
    return true;
  });
}
