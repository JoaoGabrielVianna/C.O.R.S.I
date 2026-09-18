/**
 * Isometric projection: a logical cell becomes a point.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   ARITHMETIC ONLY. NO REACT, NO SVG, NO DOM, NO VIEWPORT, NO CAMERA
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * This file knows how to turn `(u, v, w)` into `(x, y)` in an abstract
 * scene space whose origin is the room's own origin. It does not know how
 * big the screen is, where the camera is, what the zoom is, or that a
 * screen exists. A renderer translates and scales the whole scene; that is
 * a decision for the slice that draws, and putting it here would bake one
 * viewport's assumptions into the geometry every viewport shares.
 *
 * ── Why scene units and not pixels ─────────────────────────────────────
 * Because "a phone and a desktop show the same arrangement at different
 * scale" is a property of the design, and it only holds if the projection
 * produces the same numbers for both. The moment this function took a
 * width, two devices would have two layouts and the invariant would be a
 * comment rather than a fact.
 *
 * ── The projection ─────────────────────────────────────────────────────
 * The standard 2:1 isometric basis. Moving one cell along +u goes right
 * and down; one cell along +v goes left and down; elevation lifts.
 *
 *	x = (u − v) · TILE_WIDTH / 2
 *	y = (u + v) · TILE_HEIGHT / 2 − w · TILE_RISE
 *
 * ── Why depth is u + v ─────────────────────────────────────────────────
 * Painter's algorithm. In this basis a cell with a larger `u + v` is
 * nearer the viewer, so drawing in ascending depth puts near things over
 * far things without a z-buffer. Two cells that share a depth lie on the
 * same diagonal and cannot overlap, so their relative order does not
 * matter and nothing here has to invent a tie-break.
 */

import type { Cell } from "./kinds";

/**
 * The projection basis, in scene units.
 *
 * 2:1 is the classic isometric ratio: twice as wide as tall, which is what
 * makes a square cell read as a rhombus rather than as a squashed square.
 * The rise is the height of one elevation step.
 *
 * These are a SHAPE, not a size. A renderer multiplies the whole scene by
 * whatever it needs.
 */
export const TILE_WIDTH = 64;
export const TILE_HEIGHT = 32;
export const TILE_RISE = 16;

/** A point in the room's own scene space. Not screen pixels. */
export interface ScenePoint {
  readonly x: number;
  readonly y: number;
}

/**
 * Projects a cell, optionally lifted by `elevation` steps.
 *
 * Pure: the same cell always gives the same point, in any process, at any
 * time, on any device.
 */
export function project(cell: Cell, elevation = 0): ScenePoint {
  return {
    x: ((cell.u - cell.v) * TILE_WIDTH) / 2,
    y: ((cell.u + cell.v) * TILE_HEIGHT) / 2 - elevation * TILE_RISE,
  };
}

/**
 * The painter's-algorithm depth of a cell. Larger is nearer.
 *
 * The layout engine stamps this on every slot so a renderer can sort
 * without importing the projection. Both compute it the same way, from
 * here, so they cannot disagree about what is in front.
 */
export function depth(cell: Cell): number {
  return cell.u + cell.v;
}
