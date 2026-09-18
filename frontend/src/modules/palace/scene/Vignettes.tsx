/**
 * The two shapes the entrance draws.
 *
 * ── A room's vignette draws architecture, never contents ───────────────
 * Floor, two walls, and the room's own deterministic decoration. No desks,
 * no cabinets, no miniature objects: a picture with three things in it,
 * standing for a room with fourteen, is a picture somebody will count.
 *
 * ── A tray is not a room, and must not look like one ───────────────────
 * The unfiled staging area has no floor slab, no walls and no decoration.
 * Its silhouette belongs to a different class on purpose: the one thing it
 * must never do is read as a place, because it is not one.
 *
 * Both are `aria-hidden`: the link that wraps them carries the name.
 */

import { project } from "../layout/iso";
import type { Cell } from "../layout/kinds";
import type { Decoration } from "./decoration";

const WALL_TONE_OPACITY: Record<Decoration["wall"], number> = {
  light: 0.35,
  mid: 0.55,
  deep: 0.75,
};

const FLOOR_FILL: Record<Decoration["floor"], number> = {
  plank: 0.9,
  herringbone: 0.75,
  tile: 0.6,
};

function pts(...p: { x: number; y: number }[]): string {
  return p.map((q) => `${q.x},${q.y}`).join(" ");
}

export function RoomVignette({ decoration }: { decoration: Decoration }) {
  const at = (u: number, v: number, e = 0) => project({ u, v } as Cell, e);
  const S = 4; // the vignette's grid, in cells
  const H = 4;

  const floor = [at(0, 0), at(S, 0), at(S, S), at(0, S)];
  const back = [at(0, 0), at(S, 0), at(S, 0, H), at(0, 0, H)];
  const side = [at(0, 0), at(0, S), at(0, S, H), at(0, 0, H)];

  return (
    <svg
      aria-hidden="true"
      viewBox="-150 -110 300 190"
      preserveAspectRatio="xMidYMid meet"
      className="h-32 w-full bg-(--color-muted)/50 text-(--color-foreground)"
      data-testid="room-vignette"
    >
      <polygon
        points={pts(...back)}
        fill="var(--palace-scene-wall)"
        fillOpacity={WALL_TONE_OPACITY[decoration.wall]}
        stroke="var(--color-border)"
      />
      <polygon
        points={pts(...side)}
        fill="var(--palace-scene-wall)"
        fillOpacity={WALL_TONE_OPACITY[decoration.wall] * 0.78}
        stroke="var(--color-border)"
      />
      <polygon
        points={pts(...floor)}
        fill="var(--palace-scene-floor)"
        fillOpacity={FLOOR_FILL[decoration.floor]}
        stroke="var(--color-border-strong)"
      />
    </svg>
  );
}

export function UnfiledTray() {
  return (
    <svg
      aria-hidden="true"
      viewBox="-150 -110 300 190"
      preserveAspectRatio="xMidYMid meet"
      className="h-32 w-full text-(--color-muted-foreground)"
      data-testid="unfiled-vignette"
    >
      {/* A shallow open tray. No slab, no walls, no decoration: nothing
          that could be mistaken for architecture. */}
      <polygon
        points="-96,10 0,-38 96,10 0,58"
        fill="none"
        stroke="currentColor"
        strokeOpacity="0.5"
        strokeDasharray="6 5"
        strokeWidth="1.5"
      />
      <polygon
        points="-96,10 0,58 0,70 -96,22"
        fill="currentColor"
        fillOpacity="0.08"
        stroke="currentColor"
        strokeOpacity="0.35"
      />
      <polygon
        points="96,10 0,58 0,70 96,22"
        fill="currentColor"
        fillOpacity="0.05"
        stroke="currentColor"
        strokeOpacity="0.35"
      />
    </svg>
  );
}
