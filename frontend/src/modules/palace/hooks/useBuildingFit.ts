/**
 * The building's fit, and the D5 verdict that follows.
 *
 * The same shape as `useSceneFit`, sharing the same measurement source and
 * the same two thresholds, differing only in which target size the verdict
 * is computed from. See `building/fit.ts` for why that difference cannot
 * be folded into `fitScene`.
 */

import { useState } from "react";

import { fitBuilding } from "../building/fit";
import type { BuildingBounds } from "../building/bounds";
import type { Fit, SceneMode } from "../scene/fit";
import { useMeasuredBox } from "./useMeasuredBox";

export function useBuildingFit(
  bounds: BuildingBounds,
  forced: boolean,
): { ref: (el: HTMLElement | null) => void; fit: Fit | null } {
  const { ref, size } = useMeasuredBox();
  // State adjusted during render, for the reason spelled out in
  // `useSceneFit`: a ref read during render is unsafe once rendering can
  // be interrupted.
  const [mode, setMode] = useState<SceneMode>("spatial");

  const fit = size ? fitBuilding(bounds, size, mode, forced) : null;
  if (fit && fit.mode !== mode) setMode(fit.mode);

  return { ref, fit };
}
