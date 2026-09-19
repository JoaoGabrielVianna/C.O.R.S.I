/**
 * Fitting the building into a box, under the same rule as a room.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   D5, ONE SCALE UP. THE THRESHOLDS ARE IMPORTED, NEVER RE-CHOSEN
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * Scaled far enough down, the smallest OBJECT becomes something a finger
 * cannot hit. A target too small to press is an affordance that lies, and
 * this design refuses those in the domain, so it refuses them here too.
 * Below the floor the Palace hands over to the vertical list of rooms,
 * says so, and offers the house explicitly. That is a different surface,
 * not a broken one.
 *
 * ── Why the OBJECT and not the room ────────────────────────────────────
 * C1 made the room the button, so the room's box was what D5 measured.
 * C1.1 moved the primary interaction to the furniture inside, so the
 * number compared against the 44px floor moved with it. Measuring the room
 * would let a house full of unpressable furniture pass the rule by having
 * large rooms, which is exactly the affordance-that-lies the rule exists
 * to catch.
 *
 * ── Why this is a sibling of `fitScene` and not a change to it ─────────
 * `fitScene` measures `MIN_HIT_SCENE`, the smallest artifact footprint
 * inside a single room's own scene. The house draws its objects at a
 * different size, so feeding building bounds to `fitScene` would compute
 * the verdict from a number that does not describe anything on this
 * surface.
 *
 * Generalising `fitScene` to take a target size would change its
 * signature, and keeping that signature is an explicit acceptance
 * criterion: D5 is closed, browser-validated work, and C1 does not reopen
 * it. So this file repeats eight lines of arithmetic and imports the two
 * numbers that matter. `MIN_TARGET_PX` and `RESTORE_TARGET_PX` are
 * imported rather than written down again precisely so that they cannot
 * drift: there is exactly one 44 and one 52 in this product.
 */

import {
  MIN_TARGET_PX,
  RESTORE_TARGET_PX,
  type Fit,
  type SceneMode,
  type Size,
} from "../scene/fit";
import { MIN_OBJECT_HIT_SCENE, type BuildingBounds } from "./bounds";

/**
 * Works out the fit and the verdict for the whole building.
 *
 * Pure, and deterministic in `(bounds, container, previous, forced)`.
 *
 * `previous` is the mode currently on screen, which is what gives the
 * hysteresis something to be hysteretic about: falling back at 44 and
 * returning at 52 means the boundary has width, so dragging a window
 * across it settles instead of strobing. `forced` is the reader saying
 * "show me the building anyway", and it wins outright — the rule exists to
 * protect somebody from a surface they cannot use, not to overrule them
 * when they ask for it.
 */
export function fitBuilding(
  bounds: BuildingBounds,
  container: Size,
  previous: SceneMode,
  forced: boolean,
): Fit {
  const usableWidth = Math.max(0, container.width);
  const usableHeight = Math.max(0, container.height);

  const scale =
    bounds.width > 0 && bounds.height > 0
      ? Math.min(usableWidth / bounds.width, usableHeight / bounds.height)
      : 0;

  const smallestTargetPx = MIN_OBJECT_HIT_SCENE * scale;
  const threshold = previous === "spatial" ? MIN_TARGET_PX : RESTORE_TARGET_PX;

  return {
    scale,
    offsetX: (usableWidth - bounds.width * scale) / 2,
    offsetY: (usableHeight - bounds.height * scale) / 2,
    smallestTargetPx,
    mode: forced || smallestTargetPx >= threshold ? "spatial" : "library",
  };
}

/**
 * Where a scene point lands inside the container, in CSS px.
 *
 * The same job `toContainerPoint` does for a room, against the building's
 * own bounds. The overlay of interactive targets is HTML rather than SVG
 * so that a target is a real element with real focus behaviour, and this
 * is the one function keeping the two layers aligned.
 */
export function toBuildingPoint(
  point: { x: number; y: number },
  bounds: BuildingBounds,
  fit: Fit,
): { left: number; top: number } {
  return {
    left: fit.offsetX + (point.x - bounds.minX) * fit.scale,
    top: fit.offsetY + (point.y - bounds.minY) * fit.scale,
  };
}
