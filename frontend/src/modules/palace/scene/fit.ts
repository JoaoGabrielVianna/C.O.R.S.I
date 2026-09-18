/**
 * Fitting the scene into a box, and the touch-size rule that follows.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   THE GEOMETRY NEVER LEARNS THE VIEWPORT. THIS FILE DOES THE LEARNING
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * `layout()` and `sceneBounds()` are pure and know nothing about a screen.
 * A container's measured size arrives here and nowhere else, and what
 * comes out is a scale, an offset and a verdict. Nothing flows back: no
 * cell is recomputed, no object is moved, no arrangement changes because a
 * window was resized.
 *
 * ── D5, and why the fallback is not a degradation ──────────────────────
 * Scaled far enough down, the smallest object becomes something a finger
 * cannot hit. A target too small to press is an affordance that lies, and
 * this design refuses those in the domain, so it refuses them here too.
 * Below the floor the room hands over to its Library representation, says
 * so, and offers the spatial view explicitly. That is a different
 * surface, not a broken one.
 *
 * ── Why hysteresis ─────────────────────────────────────────────────────
 * With one threshold, dragging a window across the boundary flips the two
 * surfaces on every frame. Falling back at 44 and returning at 52 means
 * the boundary has width, so the room settles instead of strobing.
 */

import { MIN_HIT_SCENE } from "./presentation";
import type { SceneBounds } from "./sceneBounds";

/** The smallest a target may be before the room hands over, in CSS px. */
export const MIN_TARGET_PX = 44;
/** And the size it must reach before the room takes over again. */
export const RESTORE_TARGET_PX = 52;

export type SceneMode = "spatial" | "library";

export interface Fit {
  /** Scene units to CSS px. What `preserveAspectRatio="meet"` will do. */
  readonly scale: number;
  /** Letterbox offsets, so an overlay can sit exactly over the drawing. */
  readonly offsetX: number;
  readonly offsetY: number;
  /** The smallest interactive target at this scale, in CSS px. */
  readonly smallestTargetPx: number;
  readonly mode: SceneMode;
}

export interface Size {
  readonly width: number;
  readonly height: number;
}

/**
 * Works out the fit and the verdict.
 *
 * Pure, and deterministic in `(bounds, container, previous, forced)`, which
 * is what makes D5 testable without a DOM: the same four values always
 * give the same answer.
 *
 * `previous` is the mode currently on screen, which is what gives the
 * hysteresis something to be hysteretic about. `forced` is the reader
 * saying "show me the room anyway", and it wins outright: the rule exists
 * to protect somebody from a surface they cannot use, not to overrule them
 * when they ask for it.
 */
export function fitScene(
  bounds: SceneBounds,
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

  const smallestTargetPx = MIN_HIT_SCENE * scale;
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
 * The overlay of interactive targets is HTML rather than SVG, so that a
 * target is a real `<button>` with real focus behaviour. This is the one
 * function keeping the two layers aligned, which is why both the drawing
 * and the overlay derive from it rather than each doing its own sums.
 */
export function toContainerPoint(
  point: { x: number; y: number },
  bounds: SceneBounds,
  fit: Fit,
): { left: number; top: number } {
  return {
    left: fit.offsetX + (point.x - bounds.minX) * fit.scale,
    top: fit.offsetY + (point.y - bounds.minY) * fit.scale,
  };
}
