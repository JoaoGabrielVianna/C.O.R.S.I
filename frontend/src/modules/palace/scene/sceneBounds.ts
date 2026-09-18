/**
 * How much scene there is to show.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   THIS IS WHAT MAKES RESPONSIVENESS FREE OF THE GEOMETRY
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * The room's `viewBox` is derived from the LAYOUT, never from the screen.
 * The browser then scales that box into whatever element it was given, so
 * a phone and a desktop receive the same arrangement at different sizes
 * rather than two arrangements.
 *
 * This function is the only thing standing between those two facts, and
 * it is pure: it reads cells that `layout()` already decided, expands each
 * by the footprint its drawing needs, and unions the result. It moves
 * nothing. Given the same LayoutOutput it returns the same box, on any
 * device, forever.
 *
 * ── Why the shell is part of the union ─────────────────────────────────
 * Because an almost-empty room would otherwise produce a box the size of
 * one notebook, and the browser would blow that one object up to fill a
 * desktop. The floor and walls are always there, so they are always in the
 * bounds, and a room with one note looks like a room with one note rather
 * than like a note.
 */

import {
  project,
  type ScenePoint,
} from "../layout/iso";
import type { LayoutOutput } from "../layout/engine";
import type { Cell } from "../layout/kinds";
import {
  AFFORDANCES,
  BASE_DEPTH,
  FLOOR_MAX,
  FLOOR_MIN,
  MEMORY_FOOTPRINT,
  WALL_TOP_ELEVATION,
  anchorPointOf,
  floorCorners,
  type Footprint,
} from "./presentation";

export interface SceneBounds {
  readonly minX: number;
  readonly minY: number;
  readonly width: number;
  readonly height: number;
}

/** Breathing room around the whole scene, in scene units. */
const PADDING = 48;

interface Box {
  minX: number;
  minY: number;
  maxX: number;
  maxY: number;
}

function boxOf(point: ScenePoint, footprint: Footprint): Box {
  return {
    minX: point.x - footprint.width / 2,
    maxX: point.x + footprint.width / 2,
    minY: point.y - footprint.height,
    maxY: point.y + BASE_DEPTH,
  };
}

function include(into: Box, box: Box): void {
  into.minX = Math.min(into.minX, box.minX);
  into.minY = Math.min(into.minY, box.minY);
  into.maxX = Math.max(into.maxX, box.maxX);
  into.maxY = Math.max(into.maxY, box.maxY);
}

function includePoint(into: Box, point: ScenePoint): void {
  into.minX = Math.min(into.minX, point.x);
  into.minY = Math.min(into.minY, point.y);
  into.maxX = Math.max(into.maxX, point.x);
  into.maxY = Math.max(into.maxY, point.y);
}

/**
 * The architecture's own extent: the floor slab and the tops of the two
 * walls. Constant, so an empty room still reads as a room.
 */
function shellBox(): Box {
  const box: Box = {
    minX: Infinity,
    minY: Infinity,
    maxX: -Infinity,
    maxY: -Infinity,
  };
  for (const corner of floorCorners()) includePoint(box, project(corner));
  // The two walls rise from the far edges.
  const wallTops: Cell[] = [
    { u: FLOOR_MIN, v: FLOOR_MIN },
    { u: FLOOR_MAX, v: FLOOR_MIN },
    { u: FLOOR_MIN, v: FLOOR_MAX },
  ];
  for (const top of wallTops) includePoint(box, project(top, WALL_TOP_ELEVATION));
  return box;
}

/**
 * The box the scene needs.
 *
 * Reads only the LayoutOutput. It has no parameter for a width, a height,
 * a device or a container, and there is nowhere for one to be added
 * without the signature saying so.
 */
export function sceneBounds(out: LayoutOutput): SceneBounds {
  const box = shellBox();

  for (const container of out.containers) {
    const affordance = AFFORDANCES[container.kind];
    for (const slot of container.slots) {
      include(box, boxOf(anchorPointOf(slot.cell, container.anchor, affordance.plane), affordance));
    }
    if (container.pile) {
      include(
        box,
        boxOf(anchorPointOf(container.pile.cell, container.anchor, affordance.plane), affordance),
      );
    }
  }

  if (out.memorySurface) {
    // The memory surface stands on the floor, and its cell came from S5.1.
    // `anchorPointOf` is given its own cell as the group anchor because it
    // belongs to no group; on the floor plane the anchor is unused.
    include(
      box,
      boxOf(anchorPointOf(out.memorySurface.cell, out.memorySurface.cell, "floor"), MEMORY_FOOTPRINT),
    );
  }

  return {
    minX: box.minX - PADDING,
    minY: box.minY - PADDING,
    width: box.maxX - box.minX + PADDING * 2,
    height: box.maxY - box.minY + PADDING * 2,
  };
}

/** The `viewBox` attribute, as SVG wants it. */
export function viewBoxOf(bounds: SceneBounds): string {
  return `${bounds.minX} ${bounds.minY} ${bounds.width} ${bounds.height}`;
}
