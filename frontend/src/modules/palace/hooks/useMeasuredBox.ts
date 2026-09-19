/**
 * Measuring one box, and nothing else.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   THE ONLY PLACE IN PALACE THAT KNOWS HOW BIG THE SCREEN IS
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * Lifted out of `useSceneFit` unchanged when the building needed the same
 * measurement with a different target size. It is one hook rather than two
 * copies on purpose: this is subtle code — the order of the first
 * `getBoundingClientRect` against the observer's first callback was a real
 * defect once — and two copies of subtle code is one copy that silently
 * stops matching the one that was tested.
 *
 * ── The N3 rule, which this hook cannot enforce alone ──────────────────
 * The element handed to `ref` must be an EMPTY box that does not contain
 * whatever the verdict renders. Give it a box that holds the presenter and
 * the measurement feeds back into itself: the verdict changes the content,
 * the content changes the height, and the next measurement comes from a
 * box the previous verdict resized. That loop is what N3 closed, and
 * `RoomView.measure.test.tsx` fails if the `ref` moves back onto such a
 * box.
 */

import { useEffect, useRef, useState } from "react";

import type { Size } from "../scene/fit";

export function useMeasuredBox(): {
  ref: (el: HTMLElement | null) => void;
  size: Size | null;
} {
  const [size, setSize] = useState<Size | null>(null);
  const observed = useRef<HTMLElement | null>(null);

  useEffect(() => {
    const el = observed.current;
    if (!el || typeof ResizeObserver === "undefined") return;

    // Measured FIRST, so the surface exists on the very first paint. The
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

  return {
    ref: (el) => {
      observed.current = el;
    },
    size,
  };
}
