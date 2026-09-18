/**
 * The deterministic spatial layout engine.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   MEANING DECIDES WHAT EXISTS. LAYOUT DECIDES WHERE. RENDERING DECIDES HOW
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * This file is the middle one, and only the middle one. It answers "where
 * does each object sit" and nothing else: it draws nothing, it fetches
 * nothing, it stores nothing, and it cannot be told what anything is
 * called.
 *
 * ── I-A, enforced by the compiler rather than by care ──────────────────
 * `LayoutObject` has two fields: an id and a creation instant. There is no
 * `title`, no `body`, no `updatedAt`, no `status`, no `sensitivity`, no
 * item count. Editing meaning cannot move space because editing meaning
 * cannot even be EXPRESSED to this function. A reviewer does not have to
 * check that the engine ignores a title; they have to notice that a title
 * would not type-check.
 *
 * That is deliberately stronger than "the engine ignores those fields". An
 * engine that accepted them and ignored them would be one careless line
 * away from sorting by `updatedAt`, and the symptom would be a room that
 * quietly rearranges itself every time somebody fixes a typo.
 *
 * ── I-C, no coordinate ever persists ───────────────────────────────────
 * Nothing here reads or writes a backend, an API, `localStorage`, the URL
 * or any cache. The arrangement is recomputed from the semantic state
 * every time, which is why deleting the whole spatial surface later loses
 * no knowledge: there is nothing to lose.
 *
 * ── Determinism, and what it forbids ───────────────────────────────────
 * No `Date.now`, no `new Date()`, no `Math.random`, no DOM measurement, no
 * hidden seed. The same input produces a deep-equal output in any process,
 * at any time. A test scans this source for those names, because a comment
 * saying "pure" is not a property.
 */

import type { ArtifactKind } from "../api/types";
import { depth } from "./iso";
import {
  CONTAINER_ANCHORS,
  LAYOUT_KINDS,
  MEMORY_SURFACE_CELL,
  cellAt,
  type Cell,
} from "./kinds";

/**
 * The arrangement's version.
 *
 * Bumping it is how the whole Palace is deliberately rearranged, in one
 * reviewed change, rather than by a layout rule drifting under people.
 * It is carried in the input so a caller cannot forget that rearranging is
 * a decision somebody makes rather than something that happens.
 */
export const LAYOUT_VERSION = 1;

/**
 * How many objects a group shows individually before the rest becomes a
 * pile.
 *
 * Twelve, from the frozen spec. It is a capacity, not a page size: see
 * `layout` for why the twelfth object never loses its slot.
 */
export const VISIBLE_SLOTS = 12;

/* ── input ───────────────────────────────────────────────────────────── */

/**
 * One object, as the geometry is allowed to know it.
 *
 * ── Why `createdAt` and not `updatedAt` ────────────────────────────────
 * Because creation is the one instant about an object that never changes.
 * Ordering on `updatedAt` would mean every edit reshuffles the room, which
 * is the exact failure I-A names.
 */
export interface LayoutObject {
  readonly id: string;
  /**
   * RFC3339 UTC, fixed width, as the Palace surface emits it
   * (`2026-09-14T04:39:33Z`).
   *
   * Compared as a STRING, and that is correct rather than lazy: for one
   * fixed-width UTC format, lexicographic order IS chronological order,
   * and it is total — it cannot produce the `NaN` that an unparseable date
   * would, which would make the sort unstable and the room jump. The id
   * tie-break below makes the order total even if a value were malformed.
   */
  readonly createdAt: string;
}

export interface LayoutContainer {
  readonly kind: ArtifactKind;
  readonly objects: readonly LayoutObject[];
}

export interface LayoutInput {
  /**
   * Which room this is.
   *
   * Carried so a caller cannot accidentally lay out one room's objects
   * under another's identity. It deliberately does NOT influence geometry:
   * two rooms with the same objects produce the same arrangement, because
   * hashing the room id into the positions would make every room subtly
   * different for no reason anybody could explain or predict.
   */
  readonly roomId: string;
  readonly layoutVersion: number;
  readonly containers: readonly LayoutContainer[];
  /**
   * Whether this room has anything to put on its memory surface.
   *
   * ══════════════════════════════════════════════════════════════════
   *
   *	A BOOLEAN, AND IT HAS TO STAY A BOOLEAN
   *
   * ══════════════════════════════════════════════════════════════════
   *
   * The geometry needs to know THAT there are memories, because that
   * decides whether a surface exists. It must never learn HOW MANY,
   * because nothing about the arrangement depends on the number and a
   * count in here would be a number that eventually decides something:
   * a bigger notebook, a second one, a different cell. Each of those is
   * a room that rearranges itself when somebody writes one more thing
   * down.
   *
   * It is also the narrowest thing that can be asked. A count would
   * travel from a listing, through a hook, into the layout, and every
   * step would be one where somebody could start reading it.
   *
   * ── Why required rather than defaulting to false ───────────────────
   * Because a caller that forgot it would silently get a room with no
   * surface, which looks like a room whose memories are gone. A missing
   * field should be a compile error, not a wrong picture.
   */
  readonly hasMemories: boolean;
}

/* ── output ──────────────────────────────────────────────────────────── */

export interface LayoutSlot {
  readonly objectId: string;
  readonly cell: Cell;
  /** Painter's depth. Larger is nearer. See `iso.depth`. */
  readonly z: number;
}

/**
 * The overflow.
 *
 * ── Why it carries a cell ──────────────────────────────────────────────
 * Because a pile is a thing that sits somewhere, and "where" is this
 * engine's whole job. Leaving the position out would push the same cell
 * arithmetic into whatever draws, which is two places computing geometry
 * and one of them eventually getting it wrong.
 *
 * `count` is how many objects are IN the pile. It is derived from what the
 * caller passed and nothing else: this engine has no idea that visibility
 * rules exist, and a pile can never be a count of withheld content because
 * withheld content never reaches the input.
 */
export interface LayoutPile {
  readonly count: number;
  readonly cell: Cell;
  readonly z: number;
}

export interface LayoutContainerOutput {
  readonly kind: ArtifactKind;
  readonly anchor: Cell;
  readonly slots: readonly LayoutSlot[];
  readonly pile?: LayoutPile;
}

/**
 * The room's one surface for its memories.
 *
 * Present exactly when `hasMemories` was true, absent otherwise, with the
 * same `absent rather than empty` rule the pile follows: a surface that
 * was emitted with nothing on it would be an affordance opening onto
 * nothing, which is the one thing this design does not draw.
 *
 * It carries a position and nothing else. No count, no ids, no content:
 * whatever draws it asks the read surface for the memories themselves,
 * under the visibility rules the geometry has never heard of.
 */
export interface MemorySurface {
  readonly cell: Cell;
  readonly z: number;
}

export interface LayoutOutput {
  readonly containers: readonly LayoutContainerOutput[];
  readonly memorySurface?: MemorySurface;
}

/* ── the engine ──────────────────────────────────────────────────────── */

/**
 * Arranges a room.
 *
 * ── The canonical order ────────────────────────────────────────────────
 * `createdAt` ascending, ties broken by `id` ascending. Ascending is what
 * makes appending free: a new object is newer, so it sorts last, so it
 * takes the next free cell and nothing before it moves. Descending would
 * put every new object first and shift the entire group by one, which is
 * I-B failing on the most ordinary action there is.
 *
 * The id tie-break is not decoration. Two objects created in the same
 * second are ordinary, and without a second key their relative order would
 * depend on the order the backend happened to return them — which is
 * `updated_at DESC`, so the room would rearrange when somebody edited one
 * of them. The tie-break is what keeps I-A true in the presence of equal
 * timestamps.
 *
 * ── Overflow, and why the twelfth object keeps its slot ────────────────
 * Up to `VISIBLE_SLOTS` objects each get a cell. Beyond that, the first
 * twelve keep exactly the cells they had and the remainder becomes a pile
 * in the next cell along.
 *
 * The frozen spec described this as "eleven slots and a pile in the
 * twelfth", which is one object cheaper on screen and breaks I-B at the
 * boundary: going from twelve objects to thirteen would take the twelfth
 * object's cell away and drop it into the pile, moving something that was
 * already drawn. Since "slots existentes não mudam" is the normative rule
 * and the capacity of twelve is the normative number, the pile is an
 * additional cell rather than a borrowed one. Reported as an amendment.
 *
 * ── Empty groups ───────────────────────────────────────────────────────
 * A kind with no objects produces no container at all. It does not produce
 * an empty one, and the kinds after it do not move up to fill the space:
 * see `CONTAINER_ANCHORS`.
 */
export function layout(input: LayoutInput): LayoutOutput {
  const byKind = groupByKind(input.containers);
  const containers: LayoutContainerOutput[] = [];

  // Iterated in the vocabulary's order, never the input's, so shuffling
  // the input cannot reorder the output.
  for (const kind of LAYOUT_KINDS) {
    const objects = byKind.get(kind);
    if (!objects || objects.length === 0) continue;

    const anchor = CONTAINER_ANCHORS[kind];
    const ordered = [...objects].sort(canonical);

    const visible = ordered.slice(0, VISIBLE_SLOTS);
    const slots: LayoutSlot[] = visible.map((object, index) => {
      const cell = cellAt(anchor, index);
      return { objectId: object.id, cell, z: depth(cell) };
    });

    const overflow = ordered.length - visible.length;
    if (overflow <= 0) {
      containers.push({ kind, anchor, slots });
      continue;
    }

    const pileCell = cellAt(anchor, VISIBLE_SLOTS);
    containers.push({
      kind,
      anchor,
      slots,
      pile: { count: overflow, cell: pileCell, z: depth(pileCell) },
    });
  }

  // Computed from the flag alone, after the groups and independently of
  // them. Nothing above reads it and nothing below changes because of it,
  // which is what makes adding the first memory to a room a change that
  // moves no artifact: the surface appears in a cell no group can reach.
  if (!input.hasMemories) return { containers };

  return {
    containers,
    memorySurface: {
      cell: MEMORY_SURFACE_CELL,
      z: depth(MEMORY_SURFACE_CELL),
    },
  };
}

/**
 * The canonical comparator.
 *
 * Total by construction: when the instants are equal the ids decide, and
 * ids are unique, so no two objects ever compare equal. A comparator that
 * could return 0 for distinct objects would leave their order up to the
 * sort implementation, which is a different arrangement on a different
 * engine.
 */
function canonical(a: LayoutObject, b: LayoutObject): number {
  if (a.createdAt !== b.createdAt) return a.createdAt < b.createdAt ? -1 : 1;
  if (a.id !== b.id) return a.id < b.id ? -1 : 1;
  return 0;
}

/**
 * Collects the input's objects by kind, de-duplicated by id.
 *
 * ── Why duplicates are dropped rather than trusted ─────────────────────
 * A caller may pass the same kind twice, or the same object twice, and
 * neither is worth refusing. What is worth refusing is the consequence:
 * the same object in two cells, which would make a room show one thing as
 * two and make the pile count wrong. First occurrence in the CANONICAL
 * order wins, not the input order, so the result does not depend on how
 * the caller happened to arrange its arrays.
 */
function groupByKind(
  containers: readonly LayoutContainer[],
): Map<ArtifactKind, LayoutObject[]> {
  const byKind = new Map<ArtifactKind, LayoutObject[]>();

  for (const container of containers) {
    const bucket = byKind.get(container.kind) ?? [];
    bucket.push(...container.objects);
    byKind.set(container.kind, bucket);
  }

  for (const [kind, bucket] of byKind) {
    const seen = new Set<string>();
    const unique = [...bucket].sort(canonical).filter((object) => {
      if (seen.has(object.id)) return false;
      seen.add(object.id);
      return true;
    });
    byKind.set(kind, unique);
  }

  return byKind;
}
