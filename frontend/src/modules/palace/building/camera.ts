/**
 * Where the reader is looking, and how far in.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   A CAMERA. NOT A SECOND ARRANGEMENT
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * `placeBuilding` says where every room stands and `furnishRoom` says what
 * stands in it. Nothing in this file is allowed to disagree with either.
 * What it produces is a VIEW: a scale and a translation applied to the
 * finished drawing, in CSS pixels, after the fit has already decided how
 * the whole house maps onto the stage.
 *
 *	world geometry   +   camera
 *	(untouched)          (this file)
 *
 * The distinction is the whole of C2 and it is not a matter of taste.
 * Recomputing the layout for a focused room would mean the Palace has two
 * arrangements, that a room is somewhere else depending on what the reader
 * is attending to, and that "editing meaning must not move space" holds
 * only in one of the two. A camera cannot do any of that: it multiplies
 * pixels that were already decided.
 *
 * ── What this means in practice, and it is testable ────────────────────
 * The SVG `viewBox` is derived from `buildingBounds` and does NOT change
 * when a room is focused. The cells, the boxes, the painting order, the
 * focus order and the D5 verdict are all computed exactly as they were
 * before this file existed. Focusing multiplies a transform; it does not
 * re-enter the geometry.
 *
 * ── Why the camera is derived from the room's SHELL alone ──────────────
 * `roomBox` is the room's own world extent: the floor and the two cutaway
 * walls, from the placement. It contains every object the room can hold,
 * because the interior lattice sits inside the span and an object is
 * shorter than the wall it stands against. Deriving from the shell rather
 * than from a union with the furniture is what makes the camera immune to
 * content: adding an artifact, archiving one, or renaming every one of
 * them leaves the target rectangle bit for bit identical. A union with the
 * furniture would mean the Palace re-frames itself when somebody writes
 * something down, which is the same defect as a layout that moves.
 *
 * ── Pure ───────────────────────────────────────────────────────────────
 * No React, no DOM, no measurement, no time, no random. The stage size
 * arrives through `Fit`, which is the one place in Palace that is allowed
 * to know how big the screen is.
 */

import type { Fit } from "../scene/fit";
import { roomBox, type BuildingBounds } from "./bounds";
import { toBuildingPoint } from "./fit";
import type { RoomPlacement } from "./placement";

/**
 * A view transform over the drawing, in CSS px.
 *
 * Read as `translate(x, y) scale(scale)` about the stage's top-left
 * corner, which is exactly what `cameraTransform` emits and exactly what
 * `applyCamera` computes. One reading, two consumers, no chance of the
 * overlay and the picture disagreeing.
 */
export interface Camera {
  readonly scale: number;
  readonly x: number;
  readonly y: number;
}

/** Looking at the whole Palace: the identity. */
export const OVERVIEW_CAMERA: Camera = { scale: 1, x: 0, y: 0 };

/**
 * How much of the stage the focused room is asked to take.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   THE REST OF THE STAGE IS NOT WASTE. IT IS THE REST OF THE PALACE
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * A focused room that filled the frame would be a Room page with an
 * animation in front of it, and the reader would have left the Palace
 * after all. So the room takes most of the stage and never all of it: what
 * is left over is where the rooms around it stay visible, and the reader
 * keeps the answer to "where in my Palace is this".
 *
 * Chosen against the browser, not by taste. At 0.66 the move read as a
 * nudge rather than as attention arriving somewhere; at 0.78 the room is
 * unmistakably the subject and the neighbouring rooms still reach the
 * edges of the frame on both axes. The fit is height-bound at the stage's
 * shape, so this number is effectively about vertical room.
 */
const FOCUS_FILL = 0.78;

/**
 * How far in the camera may ever go.
 *
 * A Palace with one room would otherwise ask for an enormous zoom on a
 * shell that is already most of the stage, and the surrounding Palace
 * would be a zoom on nothing. It is a ceiling, not a target: at three
 * columns the fill above lands around 2, well inside it.
 */
const MAX_FOCUS_SCALE = 2.6;

/**
 * The camera never zooms OUT.
 *
 * The overview already fits the whole house into the stage, so a scale
 * below 1 would show less of the Palace than the overview does while
 * claiming to be a closer look at one room.
 */
const MIN_FOCUS_SCALE = 1;

/**
 * The view that brings one room forward, derived from where it already is.
 *
 * Pure in `(room, bounds, fit)`. Takes the room's existing world box,
 * asks the fit where that box already lands on the stage, and returns the
 * scale and translation that put its centre in the middle of the stage at
 * a size worth inspecting.
 *
 * `fit` is null before the stage has been measured; there is no camera to
 * compute then, and the overview is the honest answer.
 */
export function cameraForRoom(
  room: RoomPlacement,
  bounds: BuildingBounds,
  fit: Fit | null,
): Camera {
  if (!fit || fit.scale <= 0) return OVERVIEW_CAMERA;

  const box = roomBox(room);
  const topLeft = toBuildingPoint({ x: box.minX, y: box.minY }, bounds, fit);
  const bottomRight = toBuildingPoint({ x: box.maxX, y: box.maxY }, bounds, fit);

  const roomWidth = bottomRight.left - topLeft.left;
  const roomHeight = bottomRight.top - topLeft.top;
  if (roomWidth <= 0 || roomHeight <= 0) return OVERVIEW_CAMERA;

  // The stage, recovered from the fit rather than measured a second time:
  // the letterbox offsets and the scaled bounds are exactly the box the
  // fit was given. A second measurement here would be a second source of
  // truth for the same number.
  const stageWidth = fit.offsetX * 2 + bounds.width * fit.scale;
  const stageHeight = fit.offsetY * 2 + bounds.height * fit.scale;

  const scale = clamp(
    Math.min((stageWidth * FOCUS_FILL) / roomWidth, (stageHeight * FOCUS_FILL) / roomHeight),
    MIN_FOCUS_SCALE,
    MAX_FOCUS_SCALE,
  );

  const centreX = (topLeft.left + bottomRight.left) / 2;
  const centreY = (topLeft.top + bottomRight.top) / 2;

  return {
    scale,
    x: stageWidth / 2 - scale * centreX,
    y: stageHeight / 2 - scale * centreY,
  };
}

/** The camera as CSS, about the stage's top-left corner. */
export function cameraTransform(camera: Camera): string {
  return `translate(${camera.x}px, ${camera.y}px) scale(${camera.scale})`;
}

/**
 * Where a point of the overview lands once the camera is applied.
 *
 * The inspector is NOT inside the transformed layer — a panel of prose
 * inheriting a 2x scale is a panel of unreadably large prose, and one
 * inheriting a 0.5x scale is worse. So it is positioned in stage
 * coordinates and has to be told where its object went. Same arithmetic as
 * the CSS above, written once.
 */
export function applyCamera(
  camera: Camera,
  point: { left: number; top: number },
): { left: number; top: number } {
  return {
    left: camera.x + camera.scale * point.left,
    top: camera.y + camera.scale * point.top,
  };
}

/** Whether this camera is the overview. */
export function isOverviewCamera(camera: Camera): boolean {
  return camera.scale === 1 && camera.x === 0 && camera.y === 0;
}

function clamp(value: number, min: number, max: number): number {
  return Math.min(Math.max(value, min), max);
}
