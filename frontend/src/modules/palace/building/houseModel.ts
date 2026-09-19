/**
 * The house, flattened into things to draw and things to press.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   PAINTING ORDER AND FOCUS ORDER ARE DIFFERENT QUESTIONS
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * Painting must go back to front, or a near cabinet is drawn under a far
 * one. Focus must go in the order a person would be walked through the
 * house, which an isometric arrangement is not: "third from the left on
 * the second row" is not something a screen reader can usefully say, and a
 * reader who cannot see the house has no left.
 *
 * So this module produces ONE list of items and TWO orderings of it:
 *
 *	paintOrder  by `z`, which is `u + v`: the painter's algorithm
 *	focusOrder  by room, then by the position inside the room
 *
 * Neither ordering invents anything. Both are permutations of what
 * `placeBuilding` and `furnishRoom` produced.
 *
 * ── Why the focus order is safe to be the DOM order here ───────────────
 * The room's own scene needs a roving tabindex because its objects overlap
 * and must be emitted in paint order for the pointer to resolve them
 * correctly. The house does not have that problem: every object sits on
 * one lattice whose pitch is wider than an object's box, so no two targets
 * share any area (`interior.test.ts` proves it over every pair). With
 * disjoint targets the DOM order is free, so it is the semantic one and
 * every object is an ordinary tab stop.
 *
 * ── Why this is a pure module and not logic inside a component ─────────
 * Because the two orderings are the accessibility contract, and a contract
 * that lives inside JSX is one nobody can test without a DOM.
 */

import type { ArtifactKind } from "../api/types";
import { depth, type ScenePoint } from "../layout/iso";
import { objectBox, objectPoint, worldCellOf, type Box } from "./bounds";
import type { InteriorOutput } from "./interior";
import type { BuildingLayout, RoomPlacement } from "./placement";

export type HouseItemType = "artifact" | "pile" | "memory" | "unfiled";

export interface HouseItem {
  readonly key: string;
  readonly type: HouseItemType;
  /** Absent only for the tray, which belongs to no room. */
  readonly roomId?: string;
  /** The artifact this stands for. Present only for `artifact`. */
  readonly artifactId?: string;
  /** Which silhouette to draw. Present only for `artifact`. */
  readonly kind?: ArtifactKind;
  /** How many artifacts a pile holds. Present only for `pile`. */
  readonly count?: number;
  readonly at: ScenePoint;
  readonly box: Box;
  /** Painter's depth. Larger is nearer. */
  readonly z: number;
  /** Position in the semantic reading order. */
  readonly room: number;
  readonly order: number;
}

/**
 * Every pressable thing in the house.
 *
 * ── What is NOT in here ────────────────────────────────────────────────
 * Room shells, floors, walls, doorways and decoration. Those are scenery:
 * they are painted, they are `aria-hidden`, and they are not reachable. A
 * room is an environment in this surface, not a control, and the whole
 * point of C1.1 is that pressing things means pressing what is inside.
 */
export function houseItems(
  layout: BuildingLayout,
  interiors: ReadonlyMap<string, InteriorOutput>,
): HouseItem[] {
  const items: HouseItem[] = [];

  for (const room of layout.rooms) {
    const interior = interiors.get(room.roomId);
    if (!interior) continue;

    for (const object of interior.objects) {
      items.push(itemAt(room, object.local, {
        key: `artifact:${object.artifactId}`,
        type: "artifact",
        artifactId: object.artifactId,
        kind: object.kind,
        order: object.slot,
      }));
    }

    if (interior.pile) {
      items.push(itemAt(room, interior.pile.local, {
        key: `pile:${room.roomId}`,
        type: "pile",
        count: interior.pile.count,
        // After the objects it stands in for: reading it last is reading
        // the room's contents in order.
        order: interior.pile.slot,
      }));
    }

    if (interior.memory) {
      items.push(itemAt(room, interior.memory.local, {
        key: `memory:${room.roomId}`,
        type: "memory",
        // After every artifact: a room is described by what is in it, and
        // then by what is known about it.
        order: interior.memory.slot,
      }));
    }
  }

  if (layout.unfiled) {
    const at = objectPoint(layout.unfiled.cell);
    items.push({
      key: "unfiled",
      type: "unfiled",
      at,
      box: objectBox(layout.unfiled.cell),
      z: layout.unfiled.z,
      // Last, and outside every room: it is not part of the house.
      room: Number.MAX_SAFE_INTEGER,
      order: 0,
    });
  }

  return items;
}

function itemAt(
  room: RoomPlacement,
  local: { du: number; dv: number },
  rest: {
    key: string;
    type: HouseItemType;
    artifactId?: string;
    kind?: ArtifactKind;
    count?: number;
    order: number;
  },
): HouseItem {
  const cell = worldCellOf(room, local);
  return {
    ...rest,
    roomId: room.roomId,
    at: objectPoint(cell),
    box: objectBox(cell),
    z: depth(cell),
    room: room.index,
  };
}

/** Back to front, so a near object is drawn over a far one. */
export function paintOrder(items: readonly HouseItem[]): HouseItem[] {
  return [...items].sort(
    (a, b) =>
      a.z - b.z ||
      // A total order, so two identical renders paint identically.
      (a.key < b.key ? -1 : a.key > b.key ? 1 : 0),
  );
}

/** The order a person would be walked through the house. Never geometric. */
export function focusOrder(items: readonly HouseItem[]): HouseItem[] {
  return [...items].sort(
    (a, b) =>
      a.room - b.room ||
      a.order - b.order ||
      (a.key < b.key ? -1 : a.key > b.key ? 1 : 0),
  );
}
