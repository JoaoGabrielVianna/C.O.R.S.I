/**
 * How much house there is to show, and how big an object is on screen.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   DERIVED FROM THE PLACEMENT, NEVER FROM THE SCREEN
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * The same shape `sceneBounds` has, one scale up and for the same reason:
 * the `viewBox` comes from the arrangement, the browser scales that box
 * into whatever element it was given, and a phone and a desktop receive
 * the same house at different sizes rather than two houses.
 *
 * Every function here is pure. None of them takes a width, a height, a
 * container or a device, and there is nowhere for one to be added without
 * the signature saying so.
 */

import { project, type ScenePoint } from "../layout/iso";
import type { Cell } from "../layout/kinds";
import { BASE_DEPTH } from "../scene/presentation";
import type { InteriorCell, InteriorOutput } from "./interior";
import {
  ROOM_SPAN,
  ROOM_WALL_ELEVATION,
  type BuildingLayout,
  type RoomPlacement,
  type UnfiledPlacement,
} from "./placement";

export interface BuildingBounds {
  readonly minX: number;
  readonly minY: number;
  readonly width: number;
  readonly height: number;
}

/** A box in scene units. Not screen pixels. */
export interface Box {
  readonly minX: number;
  readonly minY: number;
  readonly maxX: number;
  readonly maxY: number;
}

/**
 * Breathing room around the whole house, in scene units.
 *
 * ── Why it kept shrinking ──────────────────────────────────────────────
 * Padding is dead space on every side, and the house is WIDTH-bound: the
 * isometric projection makes any block of rooms about twice as wide as it
 * is tall, wider than the container it is fitted into, so the horizontal
 * margin is the only slack there is. Measured in Chrome at 1440x950, a
 * padding of 40 spent 94 of the container's 1088 pixels on nothing.
 *
 * Twenty leaves about 24 screen pixels of margin at the scales that
 * matter, which is enough that the walls do not touch the frame.
 */
const PADDING = 20;

/**
 * Extra room above the house, for the layer that is not in the drawing.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   THE BOUNDS MUST RESERVE SPACE FOR THE ROOM NAMES
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * Room names are HTML, positioned just above the top of each room's back
 * wall, and the surface they sit in has `overflow: hidden`. They are NOT
 * part of the SVG and so contribute nothing to this box by themselves.
 *
 * Which means shrinking the padding clips them: the topmost room's wall
 * top would land within a label's height of the frame, and the name of the
 * first room would be cut in half. This is the geometry paying for a layer
 * it cannot see, and it is asymmetric on purpose — names go above rooms,
 * so only the top needs it.
 */
const LABEL_HEADROOM = 30;

/**
 * How much room one object's drawing needs, in scene units.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   ONE FOOTPRINT FOR ALL FOUR KINDS, AND THAT IS DELIBERATE
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * The room's own interior gives each kind its own footprint, because there
 * a cabinet really is taller than a desk and there is space to say so. At
 * house scale a difference in SIZE would be a difference a reader
 * measures, and the only honest thing size could encode here is nothing.
 * So the kinds differ in SILHOUETTE and never in extent: a cabinet and a
 * desk occupy the same box and look nothing alike.
 *
 * It also keeps the disjointness argument to one case instead of sixteen.
 *
 * `width` spreads either side of the anchor point; `height` rises above
 * it; `BASE_DEPTH` is the contact shadow below, shared with the room's own
 * scene so the two surfaces cannot disagree about where an object ends.
 */
export const OBJECT_FOOTPRINT = { width: 52, height: 44 } as const;

/**
 * The smallest dimension an interactive target can have, in scene units.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   THE TARGET IS THE OBJECT NOW, NOT THE ROOM
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * In C1 the room was the button, so the room's box was what D5 measured.
 * C1.1 moved the primary interaction to the objects inside, so the number
 * the 44px floor is compared against has to move with it. Measuring the
 * room would let a house full of unpressable furniture pass the rule by
 * having large rooms, which is precisely the affordance-that-lies this
 * design refuses.
 *
 * Derived from the footprint rather than written down twice, so shrinking
 * an object cannot silently lower the floor.
 */
export const MIN_OBJECT_HIT_SCENE = Math.min(
  OBJECT_FOOTPRINT.width,
  OBJECT_FOOTPRINT.height + BASE_DEPTH,
);

/** Where an object standing at `cell` is anchored, in scene units. */
export function objectPoint(cell: Cell): ScenePoint {
  return project(cell);
}

/** The box one object occupies, and therefore its interactive target. */
export function objectBox(cell: Cell): Box {
  const at = project(cell);
  return {
    minX: at.x - OBJECT_FOOTPRINT.width / 2,
    maxX: at.x + OBJECT_FOOTPRINT.width / 2,
    minY: at.y - OBJECT_FOOTPRINT.height,
    maxY: at.y + BASE_DEPTH,
  };
}

/** The world cell of a position inside a room. */
export function worldCellOf(room: RoomPlacement, local: InteriorCell): Cell {
  return { u: room.origin.u + local.du, v: room.origin.v + local.dv };
}

/**
 * The box one room's shell occupies.
 *
 * Used for the drawing's extent only. It is NOT an interactive target:
 * the room shell is scenery in C1.1, and pressing a whole room would be
 * the giant navigation card the human gate rejected.
 */
export function roomBox(room: RoomPlacement): Box {
  const { u, v } = room.origin;
  return boxOfPoints([
    project({ u, v }),
    project({ u: u + ROOM_SPAN, v }),
    project({ u: u + ROOM_SPAN, v: v + ROOM_SPAN }),
    project({ u, v: v + ROOM_SPAN }),
    project({ u, v }, ROOM_WALL_ELEVATION),
    project({ u: u + ROOM_SPAN, v }, ROOM_WALL_ELEVATION),
    project({ u, v: v + ROOM_SPAN }, ROOM_WALL_ELEVATION),
  ]);
}

/**
 * The box the unfiled tray occupies.
 *
 * Exactly an object's box. The tray is interactive, so the touch floor
 * applies to it; giving it the same extent as an object means whether
 * anything is unfiled can never change the spatial verdict.
 */
export function unfiledBox(unfiled: UnfiledPlacement): Box {
  return objectBox(unfiled.cell);
}

/**
 * The box the whole house needs.
 *
 * Reads the placement and the furnishings. An empty Palace still gets a
 * finite box, because a surface with no extent cannot be scaled into a
 * container and the caller would be dividing by zero to find out.
 */
export function buildingBounds(
  layout: BuildingLayout,
  interiors: ReadonlyMap<string, InteriorOutput>,
): BuildingBounds {
  const boxes: Box[] = [];

  for (const room of layout.rooms) {
    boxes.push(roomBox(room));

    const interior = interiors.get(room.roomId);
    if (!interior) continue;
    for (const object of interior.objects) {
      boxes.push(objectBox(worldCellOf(room, object.local)));
    }
    if (interior.pile) boxes.push(objectBox(worldCellOf(room, interior.pile.local)));
    if (interior.memory) boxes.push(objectBox(worldCellOf(room, interior.memory.local)));
  }

  if (layout.unfiled) boxes.push(unfiledBox(layout.unfiled));

  if (boxes.length === 0) {
    const a = project({ u: 0, v: 0 });
    const b = project({ u: ROOM_SPAN, v: ROOM_SPAN });
    boxes.push({
      minX: Math.min(a.x, b.x),
      minY: Math.min(a.y, b.y),
      maxX: Math.max(a.x, b.x),
      maxY: Math.max(a.y, b.y),
    });
  }

  const union = boxes.reduce((into, box) => ({
    minX: Math.min(into.minX, box.minX),
    minY: Math.min(into.minY, box.minY),
    maxX: Math.max(into.maxX, box.maxX),
    maxY: Math.max(into.maxY, box.maxY),
  }));

  return {
    minX: union.minX - PADDING,
    minY: union.minY - PADDING - LABEL_HEADROOM,
    width: union.maxX - union.minX + PADDING * 2,
    height: union.maxY - union.minY + PADDING * 2 + LABEL_HEADROOM,
  };
}

/** The `viewBox` attribute, as SVG wants it. */
export function buildingViewBox(bounds: BuildingBounds): string {
  return `${bounds.minX} ${bounds.minY} ${bounds.width} ${bounds.height}`;
}

/** The polygon of a cell rectangle, for a floor slab. */
export function slabPoints(origin: Cell, span: number): string {
  return [
    project(origin),
    project({ u: origin.u + span, v: origin.v }),
    project({ u: origin.u + span, v: origin.v + span }),
    project({ u: origin.u, v: origin.v + span }),
  ]
    .map((p) => `${p.x},${p.y}`)
    .join(" ");
}

function boxOfPoints(points: readonly ScenePoint[]): Box {
  return {
    minX: Math.min(...points.map((p) => p.x)),
    minY: Math.min(...points.map((p) => p.y)),
    maxX: Math.max(...points.map((p) => p.x)),
    maxY: Math.max(...points.map((p) => p.y)),
  };
}
