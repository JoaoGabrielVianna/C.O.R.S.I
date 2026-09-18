/**
 * The architecture: a floor slab and two walls, seen from outside.
 *
 * ── Why two walls and not four ─────────────────────────────────────────
 * Because a diorama is looked INTO. Four walls close the box and you are
 * left staring at a roof; the cutaway removes the two nearest and leaves
 * the room open at the front corner, which is the whole visual idea.
 *
 * ── Why this can paint before the data arrives ─────────────────────────
 * The shell is independent of content, so it cannot be wrong about it. A
 * skeleton that guessed at objects would be a claim about how many are
 * coming; walls are a claim about nothing.
 *
 * Everything here is `aria-hidden` and `pointer-events: none`. It is
 * scenery, and scenery a reader could focus would be an affordance that
 * opens onto nothing.
 */

import { project } from "../layout/iso";
import type { Cell } from "../layout/kinds";
import type { Decoration } from "./decoration";
import {
  FLOOR_MAX,
  FLOOR_MIN,
  WALL_TOP_ELEVATION,
  floorCorners,
} from "./presentation";

function points(...pts: { x: number; y: number }[]): string {
  return pts.map((p) => `${p.x},${p.y}`).join(" ");
}

const WALL_TONE_OPACITY: Record<Decoration["wall"], number> = {
  light: 0.35,
  mid: 0.55,
  deep: 0.75,
};

export function RoomShell({ decoration }: { decoration: Decoration }) {
  const corner = (u: number, v: number, e = 0) => project({ u, v } as Cell, e);

  const floor = floorCorners().map((c) => project(c));

  // The back wall rises from the far edge (v = FLOOR_MIN), the side wall
  // from the left edge (u = FLOOR_MIN). The near two are the cutaway.
  const back = [
    corner(FLOOR_MIN, FLOOR_MIN),
    corner(FLOOR_MAX, FLOOR_MIN),
    corner(FLOOR_MAX, FLOOR_MIN, WALL_TOP_ELEVATION),
    corner(FLOOR_MIN, FLOOR_MIN, WALL_TOP_ELEVATION),
  ];
  const side = [
    corner(FLOOR_MIN, FLOOR_MIN),
    corner(FLOOR_MIN, FLOOR_MAX),
    corner(FLOOR_MIN, FLOOR_MAX, WALL_TOP_ELEVATION),
    corner(FLOOR_MIN, FLOOR_MIN, WALL_TOP_ELEVATION),
  ];

  const tone = WALL_TONE_OPACITY[decoration.wall];

  return (
    <g aria-hidden="true" style={{ pointerEvents: "none" }} data-testid="room-shell">
      <defs>
        {/* The floor treatment. Three closed options, none of which mean
            anything: see decoration.ts. */}
        <pattern
          id="palace-floor-plank"
          width="32"
          height="16"
          patternUnits="userSpaceOnUse"
          patternTransform="skewY(-26.57)"
        >
          <rect width="32" height="16" fill="var(--palace-scene-floor)" />
          <line x1="0" y1="16" x2="32" y2="16" stroke="var(--color-border)" strokeWidth="0.75" />
        </pattern>
        <pattern
          id="palace-floor-herringbone"
          width="24"
          height="24"
          patternUnits="userSpaceOnUse"
        >
          <rect width="24" height="24" fill="var(--palace-scene-floor)" />
          <path d="M0 24 L12 12 L24 24" fill="none" stroke="var(--color-border)" strokeWidth="0.75" />
        </pattern>
        <pattern id="palace-floor-tile" width="24" height="12" patternUnits="userSpaceOnUse">
          <rect width="24" height="12" fill="var(--palace-scene-floor)" />
          <rect
            width="24"
            height="12"
            fill="none"
            stroke="var(--color-border)"
            strokeWidth="0.75"
          />
        </pattern>
      </defs>

      {/* Back wall, then side wall, then the floor on top of both: the
          painter's order of a corner seen from outside. */}
      <polygon
        points={points(...back)}
        fill="var(--palace-scene-wall)"
        fillOpacity={tone}
        stroke="var(--color-border)"
        strokeWidth="1"
      />
      <polygon
        points={points(...side)}
        fill="var(--palace-scene-wall)"
        fillOpacity={tone * 0.78}
        stroke="var(--color-border)"
        strokeWidth="1"
      />
      <polygon
        points={points(...floor)}
        fill={`url(#palace-floor-${decoration.floor})`}
        stroke="var(--color-border-strong)"
        strokeWidth="1"
      />
    </g>
  );
}

/**
 * The two props.
 *
 * Fixed positions, fixed size, constant density. They do not multiply with
 * the room's contents, because decoration that varies with quantity
 * communicates quantity.
 */
export function Decor({ decoration }: { decoration: Decoration }) {
  const at = (u: number, v: number) => project({ u, v } as Cell);
  const rug = at(5, 2);
  const plant = at(1, 9);

  return (
    <g aria-hidden="true" style={{ pointerEvents: "none" }} data-testid="room-decor">
      {decoration.props === "rug" ? (
        <ellipse
          cx={rug.x}
          cy={rug.y}
          rx="70"
          ry="34"
          fill="var(--color-brand-400)"
          fillOpacity="0.10"
          stroke="var(--color-brand-400)"
          strokeOpacity="0.22"
        />
      ) : (
        <g>
          <ellipse cx={plant.x} cy={plant.y} rx="16" ry="8" fill="currentColor" fillOpacity="0.08" />
          <path
            d={`M${plant.x - 9} ${plant.y} L${plant.x - 6} ${plant.y - 22} L${plant.x + 6} ${plant.y - 22} L${plant.x + 9} ${plant.y} Z`}
            fill="var(--palace-scene-object)"
            stroke="var(--color-border-strong)"
            strokeWidth="1"
          />
          <path
            d={`M${plant.x} ${plant.y - 22} C${plant.x - 16} ${plant.y - 34} ${plant.x - 10} ${plant.y - 50} ${plant.x} ${plant.y - 46} C${plant.x + 10} ${plant.y - 50} ${plant.x + 16} ${plant.y - 34} ${plant.x} ${plant.y - 22} Z`}
            fill="var(--color-brand-500)"
            fillOpacity="0.30"
          />
        </g>
      )}
    </g>
  );
}
