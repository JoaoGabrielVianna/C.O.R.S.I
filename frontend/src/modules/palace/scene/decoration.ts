/**
 * Deterministic decoration, derived from one thing and nothing else.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   THE SEED IS `room_id`. NOT THE NAME, NOT THE CONTENT, NOT A COUNT
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * A room should be recognisable without the system inventing a meaning for
 * it. So the architecture varies a little, and the variation is a pure
 * function of the room's UUID: an identifier the database generated, which
 * says nothing about what is inside.
 *
 * ── What it must never read, and why each one is dangerous ─────────────
 *
 *	name          content the operator wrote. A "Dojang" that turns into
 *	              a dojo is the system deciding what somebody's life
 *	              means, which is exactly the thing this product does not
 *	              do.
 *	description   same.
 *	counts        density that varies with quantity COMMUNICATES
 *	              quantity, including about content the surface is
 *	              withholding. The one leak that survives every other
 *	              rule.
 *	sensitivity   a room that looks different when it is private has
 *	              published that it is private.
 *	status        same.
 *	updatedAt     decoration that moves when somebody edits is I-A
 *	              failing in the one layer that was supposed to be inert.
 *
 * ── Why the sets are closed and equally weighted ───────────────────────
 * So that no combination means anything. Eighteen equiprobable outcomes
 * carry no information: knowing a room has the herringbone floor tells a
 * reader the room's identity and nothing about its contents, which is the
 * whole and only point.
 *
 * Density is CONSTANT. A room with one object gets exactly the decoration
 * a room with forty gets.
 *
 * Everything this module produces is `aria-hidden`, unfocusable and
 * `pointer-events: none`. Decoration that could be reached would be an
 * affordance, and an affordance that opens onto nothing is the failure
 * this whole design is arranged to avoid.
 */

/** Floor treatments. Neutral, architectural, meaningless. */
export const FLOOR_PATTERNS = ["plank", "herringbone", "tile"] as const;
export type FloorPattern = (typeof FLOOR_PATTERNS)[number];

/** Wall tones, all inside the neutral ramp. */
export const WALL_TONES = ["light", "mid", "deep"] as const;
export type WallTone = (typeof WALL_TONES)[number];

/** Two fixed, non-interactive props. */
export const PROP_SETS = ["rug", "plant"] as const;
export type PropSet = (typeof PROP_SETS)[number];

export interface Decoration {
  readonly floor: FloorPattern;
  readonly wall: WallTone;
  readonly props: PropSet;
}

/**
 * FNV-1a, 32 bit.
 *
 * A named, boring, well-understood hash rather than something clever. It
 * needs to be stable across processes and versions and to spread a UUID's
 * characters evenly; it does not need to be cryptographic, and choosing
 * something exotic would make the decoration a thing nobody can predict
 * from reading the code.
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
 * The room's decoration.
 *
 * Takes a room id. It takes nothing else, and there is nowhere to put
 * anything else: the signature is the guarantee.
 */
export function decorationFor(roomId: string): Decoration {
  const h = hash(roomId);
  return {
    floor: FLOOR_PATTERNS[h % FLOOR_PATTERNS.length],
    wall: WALL_TONES[(h >>> 8) % WALL_TONES.length],
    props: PROP_SETS[(h >>> 16) % PROP_SETS.length],
  };
}
