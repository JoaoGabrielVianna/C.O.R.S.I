/**
 * The room's fit, and the D5 verdict that follows.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   THE GEOMETRY NEVER LEARNS THE VIEWPORT. THIS FILE DOES THE LEARNING
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * `layout()` is pure and `sceneBounds()` is pure; both would be poisoned
 * by a width. So the measurement stops here: `useMeasuredBox` reports the
 * element's size, `fitScene` turns that into a scale and a verdict, and
 * nothing flows back into the geometry. Resizing a window changes how
 * large the room is drawn. It never changes the room.
 *
 * The measurement itself moved to `useMeasuredBox` when the building
 * needed the same box measured against a different target size. The
 * signature, the behaviour and the thresholds here are unchanged.
 */

import { useState } from "react";

import { fitScene, type Fit, type SceneMode } from "../scene/fit";
import type { SceneBounds } from "../scene/sceneBounds";
import { useMeasuredBox } from "./useMeasuredBox";

export function useSceneFit(
  bounds: SceneBounds,
  forced: boolean,
): { ref: (el: HTMLElement | null) => void; fit: Fit | null } {
  const { ref, size } = useMeasuredBox();
  /**
   * The mode currently on screen, which is what the hysteresis compares
   * against.
   *
   * ── Why state adjusted during render, and not a ref ────────────────
   * A ref read during render is unsafe once rendering can be interrupted:
   * the value a render sees would depend on whether an earlier attempt
   * was thrown away. This is React's documented way to derive state from
   * something that changed — set it during render, and React re-runs this
   * component before committing anything. No effect, no extra frame, no
   * flash of the wrong surface.
   */
  const [mode, setMode] = useState<SceneMode>("spatial");

  const fit = size ? fitScene(bounds, size, mode, forced) : null;
  if (fit && fit.mode !== mode) setMode(fit.mode);

  return { ref, fit };
}
