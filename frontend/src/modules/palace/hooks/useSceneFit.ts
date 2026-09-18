/**
 * Measuring the container, and nothing else.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   THE ONLY PLACE IN PALACE THAT KNOWS HOW BIG THE SCREEN IS
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * `layout()` is pure and `sceneBounds()` is pure; both would be poisoned
 * by a width. So the measurement stops here: a `ResizeObserver` reports
 * the element's size, `fitScene` turns that into a scale and a verdict,
 * and nothing flows back into the geometry. Resizing a window changes how
 * large the room is drawn. It never changes the room.
 */

import { useEffect, useRef, useState } from "react";

import { fitScene, type Fit, type SceneMode, type Size } from "../scene/fit";
import type { SceneBounds } from "../scene/sceneBounds";

export function useSceneFit(
  bounds: SceneBounds,
  forced: boolean,
): { ref: (el: HTMLElement | null) => void; fit: Fit | null } {
  const [size, setSize] = useState<Size | null>(null);
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
  const observed = useRef<HTMLElement | null>(null);

  useEffect(() => {
    const el = observed.current;
    if (!el || typeof ResizeObserver === "undefined") return;

    // Measured FIRST, so the room exists on the very first paint. The
    // observer fires immediately afterwards and its value wins: doing it
    // the other way round let a stale rect overwrite a live measurement,
    // which made the room ignore a narrow container entirely.
    const first = el.getBoundingClientRect();
    setSize({ width: first.width, height: first.height });

    const observer = new ResizeObserver((entries) => {
      const rect = entries[0]?.contentRect;
      if (!rect) return;
      setSize((prev) =>
        prev && prev.width === rect.width && prev.height === rect.height
          ? prev
          : { width: rect.width, height: rect.height },
      );
    });
    observer.observe(el);
    return () => observer.disconnect();
  }, []);

  const fit = size ? fitScene(bounds, size, mode, forced) : null;
  if (fit && fit.mode !== mode) setMode(fit.mode);

  return {
    ref: (el) => {
      observed.current = el;
    },
    fit,
  };
}
