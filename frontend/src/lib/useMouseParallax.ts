import { useEffect, useRef, useState } from "react";

/**
 * useMouseParallax — smooth, lerp-eased mouse-relative offset.
 *
 * Returns `{ x, y }` in pixels, computed from the cursor's position relative
 * to viewport center and scaled by `intensity`. Smoothing is done in rAF via
 * a configurable lerp factor (`smoothing`, lower = smoother). Respects
 * `prefers-reduced-motion` — returns `{ x: 0, y: 0 }` when reduced.
 *
 * Layered usage (depth): give nearer elements a larger `intensity`; the
 * smaller-intensity targets feel further away.
 */

type Options = {
  /** Max pixel offset at full deflection (cursor at viewport edge). */
  intensity?: number;
  /** Lerp factor per frame. 1 = no smoothing, 0.05 = very smooth. */
  smoothing?: number;
};

export function useMouseParallax({
  intensity = 12,
  smoothing = 0.08,
}: Options = {}) {
  const [{ x, y }, setOffset] = useState({ x: 0, y: 0 });
  const target = useRef({ x: 0, y: 0 });
  const current = useRef({ x: 0, y: 0 });
  const raf = useRef<number | null>(null);

  useEffect(() => {
    if (typeof window === "undefined") return;

    const reduced = window.matchMedia(
      "(prefers-reduced-motion: reduce)",
    ).matches;
    if (reduced) return;

    const onMove = (e: MouseEvent) => {
      const w = window.innerWidth || 1;
      const h = window.innerHeight || 1;
      const nx = (e.clientX / w) * 2 - 1; // −1 … 1
      const ny = (e.clientY / h) * 2 - 1;
      target.current = { x: nx * intensity, y: ny * intensity };
    };

    const tick = () => {
      const dx = target.current.x - current.current.x;
      const dy = target.current.y - current.current.y;
      current.current = {
        x: current.current.x + dx * smoothing,
        y: current.current.y + dy * smoothing,
      };
      setOffset({
        x: Math.round(current.current.x * 100) / 100,
        y: Math.round(current.current.y * 100) / 100,
      });
      raf.current = window.requestAnimationFrame(tick);
    };

    window.addEventListener("mousemove", onMove, { passive: true });
    raf.current = window.requestAnimationFrame(tick);

    return () => {
      window.removeEventListener("mousemove", onMove);
      if (raf.current !== null) window.cancelAnimationFrame(raf.current);
    };
  }, [intensity, smoothing]);

  return { x, y };
}
