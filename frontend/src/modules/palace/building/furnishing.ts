/**
 * What a room is furnished with, beyond what it CONTAINS.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   DECORATION MAY MAKE THE PALACE FEEL ALIVE.
 *   IT MAY NEVER PRETEND TO HOLD KNOWLEDGE.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * Three layers, and this file is the third:
 *
 *	DOMAIN              Room, Artifact, Memory. The source of truth, and
 *	                    nothing here can read it.
 *	FUNCTIONAL OBJECTS  `furnishRoom()` projects real artifacts onto the
 *	                    room's lattice. Pressable, named, counted.
 *	DECORATION          this file. Painted, silent, unreachable. It exists
 *	                    so a room reads as a place rather than a plan.
 *
 * The boundary is not a convention, it is the invariant the whole Palace
 * rests on: **visual affordance = semantic meaning.** A drawer somebody can
 * press must open a real list; therefore a drawer that opens nothing must
 * not be drawn. Everything below is chosen so that no decorative piece can
 * be mistaken for something that holds the operator's knowledge.
 *
 * ── Why decoration stands on the SEMANTIC lattice ──────────────────────
 * A room is three interior pitches square and every one of its nine
 * positions belongs to the furnishing: seven artifacts, the pile, the
 * memory surface. There is no spare floor. Inventing a second coordinate
 * system for decoration would mean two things deciding where objects go in
 * one room, which is the mistake amendment A7 recorded one scale down.
 *
 * So decoration takes the positions the room's own furnishing LEFT FREE,
 * and takes them in a fixed order. That gives three properties for free:
 *
 *	collisions are impossible    the lattice already proves every pair
 *	                             of positions disjoint
 *	meaning has priority         a new artifact claims its slot from
 *	                             `furnishRoom`, and decoration simply is
 *	                             not drawn there any more
 *	nothing semantic ever moves  because decoration is computed AFTER
 *	                             the furnishing and cannot influence it
 *
 * The cost is stated rather than hidden: a room that fills up loses its
 * decoration, one piece at a time, until a full room is bare. That is the
 * right way round. A Palace that kept its plants by pushing a list
 * somewhere else would be a Palace that lies about where things are.
 *
 * ── Determinism, and what it must be blind to ──────────────────────────
 * The seed is the room's id and a version, and nothing else exists in the
 * signature to pass. Same room, same furniture, forever: across reloads,
 * across machines, across sessions. No `Math.random`, no `Date`, no
 * persisted coordinate, no network, no model.
 *
 * What it must never read is the same list `decorationFor` already refuses,
 * and for the same reasons: name and description are the operator's words,
 * so a room called Dojang that grows a dojo is the system deciding what
 * somebody's life means; `updatedAt` would make an edit redecorate; and
 * sensitivity or status would publish themselves in the furniture.
 *
 * ── What occupancy may and may not leak ────────────────────────────────
 * Decoration yields to objects that are DRAWN, and drawn objects are
 * already on screen and already counted honestly. So the amount of
 * furniture repeats information the reader can see and adds none.
 *
 * Content the surface withholds never reaches this module at all: a
 * `HIGHLY_SENSITIVE` room has no shell to furnish, and a withheld artifact
 * is excluded from both the listing and `artifact_count`, so it claims no
 * slot and displaces no plant. There is no gap anywhere to infer one from.
 */

import type { InteriorCell, InteriorOutput } from "./interior";
import { interiorCellOf } from "./interior";

/**
 * The furnishing arrangement's version.
 *
 * Part of the seed, so that a future slice can redecorate the whole Palace
 * deliberately, in one reviewed change, rather than by a rule drifting
 * under somebody. It is NOT `BUILDING_VERSION`: where a room stands and
 * what is standing in it are different decisions, and bumping one must not
 * be forced to bump the other.
 */
export const FURNISHING_VERSION = 1;

/**
 * The decorative vocabulary. Closed, small, and equally weighted.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   NONE OF THESE MAY LOOK LIKE SOMETHING THAT HOLDS THINGS
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * The functional silhouettes are RESERVED: a desk with a raised back is a
 * `project`, a cabinet with drawer fronts is a `list`, a divided shelf is a
 * `plan`, a board on an easel is a `note`, stacked crates are the overflow
 * pile, a closed notebook is the memory surface. A decorative piece that
 * borrowed any of those would be an affordance that lies twice over: it
 * would invite a press, and the press would do nothing.
 *
 * So the vocabulary here is deliberately made of things nobody stores
 * knowledge in, and each one is a different class of shape from every
 * functional object:
 *
 *	plant     organic, tall, irregular. No flat surface at all
 *	armchair  soft, angled, seat-shaped
 *	bench     low, wide, unbroken. No fronts, no dividers, no lid
 *	lamp      thin stem and a shade. Almost no volume
 *	column    architectural, symmetric, floor to above the wall line
 *
 * Five rather than thirty, on purpose. A small vocabulary drawn carefully
 * reads as a furnished Palace; a large one drawn quickly reads as clip art.
 *
 * Equally weighted so that no combination means anything: which chair a
 * room has tells a reader the room's identity and nothing whatever about
 * its contents, which is the whole and only point.
 */
export const DECOR_PIECES = ["plant", "armchair", "bench", "lamp", "column"] as const;
export type DecorPiece = (typeof DECOR_PIECES)[number];

/** Rug treatments. `none` is a real outcome, so not every room has one. */
export const RUG_STYLES = ["none", "bordered", "banded"] as const;
export type RugStyle = (typeof RUG_STYLES)[number];

/**
 * How many decorative pieces a room may hold.
 *
 * Three, and the number is a judgement about two different risks.
 *
 * Too few and an empty room is still a diagram. Too many and the room is
 * clutter: the eye stops finding the objects that matter, which is the one
 * thing this surface exists to make easy, and the floor stops reading as
 * somewhere a person could walk. The second risk is also the one that
 * ambient life inherits later — a room packed to its nine positions leaves
 * nowhere for anybody to stand.
 *
 * It is a CEILING, not a target. A room with seven artifacts, a pile and a
 * memory surface gets none, and that is correct.
 */
export const MAX_DECOR_PIECES = 3;

/**
 * The order decoration claims free positions in.
 *
 * Corners first, then edges, and the centre last:
 *
 *	0  1  2        0, 2, 6, 8   corners, against the walls
 *	3  4  5        1, 3, 5, 7   edges
 *	6  7  8        4            the middle of the room
 *
 * Furniture belongs around the outside of a room; the middle is where the
 * rug goes and where a person would walk. Taking the centre last also
 * means the first thing a filling room gives up is its most crowded piece.
 *
 * Fixed, and never derived from how many positions happen to be free: an
 * order that depended on the count would reshuffle the decoration every
 * time an artifact appeared.
 */
export const DECOR_SLOT_ORDER = [0, 2, 6, 8, 1, 3, 5, 7, 4] as const;

export interface DecorPlacement {
  readonly slot: number;
  readonly piece: DecorPiece;
  readonly local: InteriorCell;
}

export interface RoomDecor {
  readonly rug: RugStyle;
  readonly pieces: readonly DecorPlacement[];
}

/**
 * FNV-1a, 32 bit.
 *
 * The same hash `decorationFor` uses, for the same reason: it has to be
 * stable across processes and versions and spread a UUID evenly, and it
 * must be boring enough that anybody reading this file can predict what it
 * does. It is not, and does not need to be, cryptographic.
 */
function hash(seed: string): number {
  let h = 0x811c9dc5;
  for (let i = 0; i < seed.length; i += 1) {
    h ^= seed.charCodeAt(i);
    h = Math.imul(h, 0x01000193);
  }
  return h >>> 0;
}

/**
 * Which piece stands at one position of one room.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   A FUNCTION OF (ROOM, SLOT). DELIBERATELY NOT OF THE ROOM'S CONTENTS
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * The tempting alternative is to deal pieces out without repetition, so a
 * room never shows two plants. It is rejected: dealing without replacement
 * makes each piece depend on which OTHER positions were free, so adding one
 * artifact would silently change the furniture standing somewhere else.
 *
 * Independence buys the stronger property. Adding an artifact can only ever
 * REMOVE a decorative piece; it can never change one. The price is that a
 * room can show the same piece twice, which is cosmetic and occasionally
 * even true of real rooms.
 */
function pieceFor(roomId: string, slot: number): DecorPiece {
  const h = hash(`${FURNISHING_VERSION}:${roomId}:piece:${slot}`);
  return DECOR_PIECES[h % DECOR_PIECES.length];
}

/**
 * How a room is furnished, given what it already contains.
 *
 * Pure in `(roomId, interior)`. The interior arrives already computed by
 * `furnishRoom`, which is what makes the priority structural rather than
 * polite: this function cannot move a semantic object because it never
 * produces one, and it cannot claim a taken position because it reads the
 * taken ones first.
 */
export function decorateRoom(roomId: string, interior: InteriorOutput): RoomDecor {
  const taken = new Set<number>(interior.objects.map((object) => object.slot));
  if (interior.pile) taken.add(interior.pile.slot);
  if (interior.memory) taken.add(interior.memory.slot);

  const pieces = DECOR_SLOT_ORDER.filter((slot) => !taken.has(slot))
    .slice(0, MAX_DECOR_PIECES)
    .map((slot) => ({
      slot,
      piece: pieceFor(roomId, slot),
      local: interiorCellOf(slot),
    }));

  return {
    rug: RUG_STYLES[hash(`${FURNISHING_VERSION}:${roomId}:rug`) % RUG_STYLES.length],
    pieces,
  };
}
