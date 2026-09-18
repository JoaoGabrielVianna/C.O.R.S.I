/**
 * The room, flattened into things to draw and things to press.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   PAINTING ORDER AND FOCUS ORDER ARE DIFFERENT QUESTIONS
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * Painting must go back to front, or a near cabinet is drawn under a far
 * one. Focus must go in the order a person would describe the room, which
 * an isometric arrangement is not: "third from the left on the second row"
 * is not something a screen reader can usefully say, and a reader who
 * cannot see the room has no left.
 *
 * So this module produces ONE list of items and TWO orderings of it:
 *
 *	paintOrder  plane first (wall behind floor), then S5's `z`
 *	focusOrder  group in the vocabulary's order, then S5's canonical
 *	            order inside the group, then the memory surface
 *
 * Neither ordering invents anything. Both are permutations of what
 * `layout()` produced, and `z` is used exactly as S5 stamped it.
 *
 * ── Why this is a pure module and not logic inside a component ─────────
 * Because the two orderings are the accessibility contract, and a contract
 * that lives inside JSX is one nobody can test without a DOM. Everything
 * here is a function of the LayoutOutput.
 */

import type { ArtifactKind } from "../api/types";
import type { LayoutOutput } from "../layout/engine";
import type { ScenePoint } from "../layout/iso";
import { LAYOUT_KINDS } from "../layout/kinds";
import {
  AFFORDANCES,
  MEMORY_FOOTPRINT,
  anchorPointOf,
  type Footprint,
  type Plane,
} from "./presentation";

export type SceneItemType = "artifact" | "pile" | "memory";

export interface SceneItem {
  readonly key: string;
  readonly type: SceneItemType;
  /** Absent only for the memory surface, which belongs to no group. */
  readonly kind?: ArtifactKind;
  /** The artifact this stands for. Absent for a pile and for memories. */
  readonly objectId?: string;
  /** How many objects a pile holds. Absent for everything else. */
  readonly count?: number;
  readonly plane: Plane;
  readonly at: ScenePoint;
  /** S5's depth, carried through untouched. */
  readonly z: number;
  readonly footprint: Footprint;
  /** Position in the semantic reading order. */
  readonly group: number;
  readonly order: number;
}

const PLANE_RANK: Record<Plane, number> = { backWall: 0, floor: 1 };

/**
 * Builds the scene's items from the layout.
 *
 * Reads cells and nothing else. It does not consult a title, a count, a
 * status, a sensitivity or a viewport, and the LayoutOutput it is given
 * does not carry any of those to consult.
 */
export function sceneItems(out: LayoutOutput): SceneItem[] {
  const items: SceneItem[] = [];

  for (const container of out.containers) {
    const group = LAYOUT_KINDS.indexOf(container.kind);
    const affordance = AFFORDANCES[container.kind];

    container.slots.forEach((slot, index) => {
      items.push({
        key: `artifact:${slot.objectId}`,
        type: "artifact",
        kind: container.kind,
        objectId: slot.objectId,
        plane: affordance.plane,
        at: anchorPointOf(slot.cell, container.anchor, affordance.plane),
        z: slot.z,
        footprint: affordance,
        group,
        order: index,
      });
    });

    if (container.pile) {
      items.push({
        key: `pile:${container.kind}`,
        type: "pile",
        kind: container.kind,
        count: container.pile.count,
        plane: affordance.plane,
        at: anchorPointOf(container.pile.cell, container.anchor, affordance.plane),
        z: container.pile.z,
        footprint: affordance,
        group,
        // Last in its group: it holds the objects that come after the
        // ones with slots, so reading it last is reading them in order.
        order: container.slots.length,
      });
    }
  }

  if (out.memorySurface) {
    items.push({
      key: "memory",
      type: "memory",
      plane: "floor",
      // Its cell came from S5.1. The group anchor is unused on the floor
      // plane, and the surface belongs to no group, so it passes its own.
      at: anchorPointOf(out.memorySurface.cell, out.memorySurface.cell, "floor"),
      z: out.memorySurface.z,
      footprint: MEMORY_FOOTPRINT,
      // After every artifact group: a room is described by what is in it,
      // then by what is known about it.
      group: LAYOUT_KINDS.length,
      order: 0,
    });
  }

  return items;
}

/** Back to front. Wall behind floor, then S5's depth inside each plane. */
export function paintOrder(items: readonly SceneItem[]): SceneItem[] {
  return [...items].sort(
    (a, b) =>
      PLANE_RANK[a.plane] - PLANE_RANK[b.plane] ||
      a.z - b.z ||
      // A total order, so two identical renders paint identically.
      (a.key < b.key ? -1 : a.key > b.key ? 1 : 0),
  );
}

/** The order a person would be walked through the room. Never geometric. */
export function focusOrder(items: readonly SceneItem[]): SceneItem[] {
  return [...items].sort(
    (a, b) =>
      a.group - b.group ||
      a.order - b.order ||
      (a.key < b.key ? -1 : a.key > b.key ? 1 : 0),
  );
}

/**
 * The focusable items, grouped, in semantic order.
 *
 * ── Why a Map and not an array ─────────────────────────────────────────
 * Because `item.group` is the KIND's index in the vocabulary, not a
 * position in a list, and a room rarely has all four kinds. An array
 * indexed by position silently loses the mapping the moment one kind is
 * empty: `groups[3]` would be `undefined` for the notes of a room with no
 * projects, and that group would end up with no tab stop at all. The bug
 * was real and this is the shape that cannot have it.
 */
export function groupsOf(items: readonly SceneItem[]): Map<number, SceneItem[]> {
  const byGroup = new Map<number, SceneItem[]>();
  for (const item of focusOrder(items)) {
    byGroup.set(item.group, [...(byGroup.get(item.group) ?? []), item]);
  }
  return byGroup;
}
