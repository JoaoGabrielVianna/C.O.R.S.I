/**
 * Where the inspector opens.
 *
 * Lives beside the rest of the building's geometry, and not in the page,
 * because it is the same kind of thing: a pure function of a box, the
 * bounds and the fit. It reads no DOM, measures nothing, and can move
 * nothing in the scene.
 */

import type { Fit } from "../scene/fit";
import type { BuildingBounds } from "./bounds";
import { toBuildingPoint } from "./fit";
import type { HouseItem } from "./houseModel";

/** How wide the anchored panel is, in CSS px. */
export const INSPECTOR_WIDTH_PX = 340;

/**
 * The least room the panel is given before it is moved up the stage.
 *
 * Below this it stops being a panel and becomes a scroll slot, so the
 * placement prefers to shift it upward rather than squeeze it.
 */
export const MIN_INSPECTOR_HEIGHT_PX = 260;

export interface InspectorPlacement {
  readonly anchor: React.CSSProperties;
  readonly panelMaxHeight: string;
}

/**
 * Beside the object it belongs to.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   A PANEL PINNED TO A CORNER IS A MODAL WEARING THE PALACE AS WALLPAPER
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * The first version sat in the bottom-right of the stage whatever was
 * pressed, and the human gate read it as "a generic panel over the
 * Palace". The interaction was right and the placement said otherwise:
 * nothing about a fixed corner belongs to the cabinet somebody just
 * opened.
 *
 * So it opens NEXT TO the selection, on whichever side has room, and its
 * top is aligned with the object's. Together with the accent the object
 * takes while it is open, that is enough to tie the two together without
 * a leader line, an arrow or anything that would need its own geometry.
 *
 * ── Pure, and clamped rather than clever ───────────────────────────────
 * A function of the item's own box, the bounds and the fit. It reads no
 * DOM, measures nothing, and cannot move anything in the scene: it only
 * decides where a panel is pinned. Every result is clamped inside the
 * stage, so the panel is never half off the edge on a small window, and
 * it falls back to the full-width sheet at the bottom when the stage is
 * too narrow to have a "beside".
 */
export function inspectorPlacement(
  item: HouseItem | null,
  bounds: BuildingBounds,
  fit: Fit | null,
): InspectorPlacement {
  /*
    ── Why the cap travels as a value and not as `max-h-full` ──────────
    The panel used to carry `max-height: 100%`, and a percentage
    max-height resolves against the containing block's HEIGHT. The anchor
    is absolutely positioned with `max-height` and `height: auto`, so that
    percentage had nothing definite to resolve against and was dropped:
    Chrome measured a 521px panel inside a 611px stage, running 37px past
    the bottom edge. Handing the number down is the version that cannot
    be silently ignored.
  */
  const SHEET: InspectorPlacement = {
    anchor: { insetInline: 0, bottom: 0, padding: "0.75rem", height: "70%" },
    panelMaxHeight: "100%",
  };

  // No selection, no measurement yet, or a stage too narrow for a side
  // panel: the sheet at the bottom, which is what small viewports get.
  if (!item || !fit) return SHEET;

  const left = toBuildingPoint({ x: item.box.minX, y: item.box.minY }, bounds, fit);
  const right = toBuildingPoint({ x: item.box.maxX, y: item.box.maxY }, bounds, fit);
  const stageWidth = fit.offsetX * 2 + bounds.width * fit.scale;
  const stageHeight = fit.offsetY * 2 + bounds.height * fit.scale;

  const GAP = 14;
  const MARGIN = 12;
  const width = Math.min(INSPECTOR_WIDTH_PX, stageWidth - MARGIN * 2);

  if (stageWidth < INSPECTOR_WIDTH_PX + MARGIN * 2 + GAP) return SHEET;

  // Beside the object: to its right when that fits, otherwise to its left.
  const roomOnRight = stageWidth - right.left - GAP - MARGIN >= width;
  const x = roomOnRight ? right.left + GAP : left.left - GAP - width;

  /*
    Aligned with the object's own top, then pulled up far enough that a
    tall panel still ends inside the stage.

    The height has to be capped from the TOP that was actually chosen, not
    from the stage alone: a panel pinned at the object's top with the
    stage's full height as its maximum runs off the bottom edge by exactly
    however far down the object was. Chrome measured that at 648px of
    panel in a 611px stage before this line existed.
  */
  const top = clamp(left.top, MARGIN, Math.max(MARGIN, stageHeight - MARGIN - MIN_INSPECTOR_HEIGHT_PX));

  const available = Math.max(MIN_INSPECTOR_HEIGHT_PX, stageHeight - top - MARGIN);

  return {
    anchor: {
      left: `${clamp(x, MARGIN, stageWidth - width - MARGIN)}px`,
      top: `${top}px`,
      width: `${width}px`,
      height: `${available}px`,
    },
    panelMaxHeight: `${available}px`,
  };
}



function clamp(value: number, min: number, max: number): number {
  return Math.min(Math.max(value, min), max);
}
