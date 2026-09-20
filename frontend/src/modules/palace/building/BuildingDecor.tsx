/**
 * The furniture nobody can press.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   A PASSIVE VISUAL LANGUAGE, SO NOTHING HERE ADVERTISES AN INTERACTION
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * `BuildingObjects.tsx` draws the operator's real artifacts. This file
 * draws the room around them. The two have to be told apart at a glance,
 * without a label, by somebody who has never read a word of documentation,
 * and three things do that work together:
 *
 *	SILHOUETTE  functional shapes are reserved. Nothing here has drawer
 *	            fronts, a divided shelf, a work surface with a raised
 *	            back, a board on an easel, or a stack of crates. The
 *	            decorative set is organic, soft, thin or architectural —
 *	            classes of shape nobody keeps knowledge in
 *	CONTRAST    functional objects hold the room's strongest edge
 *	            (`--color-border-strong`) and its brightest face. Decor
 *	            uses the warm, quieter `--palace-decor*` ramp with a
 *	            softer edge, so it sits behind the things that matter
 *	INTERACTION only functional objects have a button over them. Decor
 *	            has no hover, no focus ring, no cursor and no tab stop —
 *	            it is inside the painted layer, which is `aria-hidden`
 *	            and `pointer-events: none` from end to end
 *
 * The rule the whole set obeys: **if it looks like it holds something, it
 * holds something.** A decorative cabinet would break that rule in the
 * worst possible way, because breaking it makes the picture look richer.
 *
 * ── Scale ─────────────────────────────────────────────────────────────
 * Every piece is drawn to sit inside the same 52x44 footprint an artifact
 * object occupies, so decoration can never spill past the room it stands
 * in, never reach the building's bounds, and never change what the camera
 * frames. The bounds do not read this file, and there is nowhere for them
 * to start.
 */

import type { ScenePoint } from "../layout/iso";
import type { DecorPiece } from "./furnishing";

/**
 * Three faces, not two.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   A SOLID STANDING ON THE FLOOR, NOT AN ICON DRAWN OVER IT
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * The first pass gave every piece a bright top and one side value used for
 * both flanks, and the human gate read the result as drawings lying on the
 * floor rather than as objects occupying the room. Two values cannot
 * describe a corner: the two flanks of a box meet at an edge, and if they
 * are the same colour the edge is a line rather than a turn.
 *
 * So there are three: the lit top, the near flank, and the flank turned
 * away. Applied consistently to every primitive, which is what makes them
 * read as one set of objects lit by one light.
 */
const TOP = "var(--palace-decor)";
const SIDE = "var(--palace-decor-side)";
const DEEP = "var(--palace-decor-deep)";
const EDGE = "var(--palace-decor-edge)";
const METAL = "var(--palace-metal)";
const LEAF = "var(--palace-plant)";

/**
 * The contact shadow decoration gets.
 *
 * Lighter than the functional one and a little smaller: shadow is weight,
 * and the heaviest thing in a room should be the thing that means
 * something. It went from 0.06 to 0.09 in C3.1 because the floor came down
 * a step and a shadow that reads on white disappears on wood — without it
 * the piece floats, which is the other half of looking like an icon.
 */
function SoftShadow({ at, rx = 15 }: { at: ScenePoint; rx?: number }) {
  return (
    <ellipse cx={at.x} cy={at.y + 2} rx={rx} ry={rx * 0.4} fill="currentColor" fillOpacity="0.09" />
  );
}

/** A potted plant. Organic and irregular: the least storage-like shape there is. */
function Plant({ at }: { at: ScenePoint }) {
  const { x, y } = at;
  return (
    <g>
      <SoftShadow at={at} rx={13} />
      {/* the pot: a tapered vessel, open at the top */}
      <polygon
        points={`${x - 9},${y - 16} ${x},${y - 14.5} ${x + 3},${y - 1} ${x - 6},${y - 1}`}
        fill={SIDE}
        stroke={EDGE}
        strokeWidth="0.9"
      />
      <polygon
        points={`${x + 9},${y - 16} ${x},${y - 14.5} ${x + 3},${y - 1} ${x + 6},${y - 1}`}
        fill={DEEP}
        stroke={EDGE}
        strokeWidth="0.9"
      />
      {/* the rim, catching the light */}
      <polygon
        points={`${x - 9},${y - 16} ${x},${y - 13} ${x + 9},${y - 16} ${x},${y - 19}`}
        fill={TOP}
        stroke={EDGE}
        strokeWidth="0.8"
      />
      {/* three fronds, drawn as filled leaves rather than strokes so the
          shape survives being scaled down in the overview */}
      <path
        d={`M${x} ${y - 18} C${x - 14} ${y - 26} ${x - 12} ${y - 40} ${x - 2} ${y - 42} C${x - 4} ${y - 32} ${x - 2} ${y - 24} ${x} ${y - 18} Z`}
        fill={LEAF}
        fillOpacity="0.85"
      />
      <path
        d={`M${x} ${y - 18} C${x + 14} ${y - 26} ${x + 13} ${y - 38} ${x + 3} ${y - 44} C${x + 4} ${y - 33} ${x + 2} ${y - 24} ${x} ${y - 18} Z`}
        fill={LEAF}
        fillOpacity="0.7"
      />
      <path
        d={`M${x} ${y - 18} C${x - 4} ${y - 30} ${x - 1} ${y - 44} ${x + 1} ${y - 50} C${x + 5} ${y - 40} ${x + 3} ${y - 26} ${x} ${y - 18} Z`}
        fill={LEAF}
        fillOpacity="0.95"
      />
    </g>
  );
}

/** An armchair. Soft, angled, seat-shaped, and clearly not a container. */
function Armchair({ at }: { at: ScenePoint }) {
  const { x, y } = at;
  return (
    <g>
      <SoftShadow at={at} rx={17} />
      {/* seat */}
      <polygon
        points={`${x - 17},${y - 10} ${x},${y - 18} ${x + 17},${y - 10} ${x},${y - 2}`}
        fill={TOP}
        stroke={EDGE}
        strokeWidth="1"
      />
      <polygon
        points={`${x - 17},${y - 10} ${x},${y - 2} ${x},${y + 3} ${x - 17},${y - 5}`}
        fill={SIDE}
        stroke={EDGE}
        strokeWidth="0.8"
      />
      <polygon
        points={`${x + 17},${y - 10} ${x},${y - 2} ${x},${y + 3} ${x + 17},${y - 5}`}
        fill={DEEP}
        stroke={EDGE}
        strokeWidth="0.8"
      />
      {/* back, leaning away from the viewer */}
      <polygon
        points={`${x - 16},${y - 12} ${x - 1},${y - 19} ${x - 1},${y - 33} ${x - 16},${y - 26}`}
        fill={DEEP}
        stroke={EDGE}
        strokeWidth="0.9"
      />
      {/* the cushion seam, so the seat has a top surface and not just an
          outline */}
      <line
        x1={x - 11}
        y1={y - 12.5}
        x2={x + 1}
        y2={y - 18.5}
        stroke={EDGE}
        strokeWidth="0.7"
        strokeOpacity="0.75"
      />
      {/* one arm, so the shape is legibly a chair and not a block */}
      <polygon
        points={`${x + 2},${y - 18} ${x + 16},${y - 11} ${x + 16},${y - 19} ${x + 2},${y - 26}`}
        fill={TOP}
        stroke={EDGE}
        strokeWidth="0.9"
      />
    </g>
  );
}

/**
 * A bench. Low, narrow, unbroken: no fronts, no dividers, no lid.
 *
 * ── Kept deliberately smaller than the desk ────────────────────────────
 * A `project` is drawn as a work desk, which is the functional shape a
 * seat is closest to. The first browser pass showed the two carrying
 * similar weight from across the house, so the bench lost height and width
 * until the difference is visible at overview scale: a desk is a surface
 * you work at, with a raised back; this is a seat, and it sits lower than
 * everything that holds anything.
 */
function Bench({ at }: { at: ScenePoint }) {
  const { x, y } = at;
  return (
    <g>
      <SoftShadow at={at} rx={16} />
      <polygon
        points={`${x - 16},${y - 9} ${x},${y - 17} ${x + 16},${y - 9} ${x},${y - 1}`}
        fill={TOP}
        stroke={EDGE}
        strokeWidth="1"
      />
      <polygon
        points={`${x - 16},${y - 9} ${x},${y - 1} ${x},${y + 3} ${x - 16},${y - 5}`}
        fill={SIDE}
        stroke={EDGE}
        strokeWidth="0.8"
      />
      <polygon
        points={`${x + 16},${y - 9} ${x},${y - 1} ${x},${y + 3} ${x + 16},${y - 5}`}
        fill={DEEP}
        stroke={EDGE}
        strokeWidth="0.8"
      />
      {/* a seam along the seat, so it reads as upholstery and not as a lid */}
      <line
        x1={x - 10}
        y1={y - 11.5}
        x2={x + 10}
        y2={y - 11.5}
        stroke={EDGE}
        strokeWidth="0.7"
        strokeOpacity="0.8"
      />
      {/* two slim legs, which is what keeps it from reading as a crate */}
      <line x1={x - 11} y1={y - 3} x2={x - 11} y2={y + 2} stroke={EDGE} strokeWidth="1.3" />
      <line x1={x + 11} y1={y - 3} x2={x + 11} y2={y + 2} stroke={EDGE} strokeWidth="1.3" />
    </g>
  );
}

/** A floor lamp. Almost no volume: a stem, a shade, a small base. */
function Lamp({ at }: { at: ScenePoint }) {
  const { x, y } = at;
  return (
    <g>
      <SoftShadow at={at} rx={10} />
      <ellipse cx={x} cy={y - 2} rx="8" ry="3.4" fill={DEEP} stroke={EDGE} strokeWidth="0.8" />
      <ellipse cx={x} cy={y - 3.4} rx="8" ry="3.4" fill={SIDE} stroke={EDGE} strokeWidth="0.8" />
      <line x1={x} y1={y - 4} x2={x} y2={y - 34} stroke={METAL} strokeWidth="1.6" />
      <polygon
        points={`${x - 11},${y - 34} ${x + 11},${y - 34} ${x + 7},${y - 48} ${x - 7},${y - 48}`}
        fill={TOP}
        stroke={EDGE}
        strokeWidth="1"
      />
      {/* the half of the shade turned away from the light */}
      <polygon
        points={`${x + 2},${y - 34} ${x + 11},${y - 34} ${x + 7},${y - 48} ${x + 1},${y - 48}`}
        fill={DEEP}
        fillOpacity="0.85"
      />
      {/* the light the shade throws, as a tone on the shade's lower lip
          rather than as a glow: a glow would be the only emissive thing in
          the Palace and would pull the eye off the objects */}
      <line
        x1={x - 11}
        y1={y - 34}
        x2={x + 11}
        y2={y - 34}
        stroke={METAL}
        strokeWidth="1.2"
        strokeOpacity="0.6"
      />
    </g>
  );
}

/** A column. Architecture standing in the room: symmetric, vertical, plain. */
function Column({ at }: { at: ScenePoint }) {
  const { x, y } = at;
  return (
    <g>
      <SoftShadow at={at} rx={12} />
      {/* base */}
      <polygon
        points={`${x - 12},${y - 5} ${x},${y - 11} ${x + 12},${y - 5} ${x},${y + 1}`}
        fill={TOP}
        stroke={EDGE}
        strokeWidth="0.9"
      />
      {/* shaft, two faces so it turns with the projection */}
      <polygon
        points={`${x - 7},${y - 8} ${x},${y - 11} ${x},${y - 44} ${x - 7},${y - 41}`}
        fill={SIDE}
        stroke={EDGE}
        strokeWidth="0.8"
      />
      <polygon
        points={`${x + 7},${y - 8} ${x},${y - 11} ${x},${y - 44} ${x + 7},${y - 41}`}
        fill={DEEP}
        stroke={EDGE}
        strokeWidth="0.8"
      />
      {/* capital */}
      <polygon
        points={`${x - 11},${y - 44} ${x},${y - 49} ${x + 11},${y - 44} ${x},${y - 39}`}
        fill={TOP}
        stroke={EDGE}
        strokeWidth="0.9"
      />
      <line x1={x} y1={y - 14} x2={x} y2={y - 41} stroke={EDGE} strokeWidth="0.6" strokeOpacity="0.7" />
    </g>
  );
}

const PIECES: Record<DecorPiece, (props: { at: ScenePoint }) => React.ReactElement> = {
  plant: Plant,
  armchair: Armchair,
  bench: Bench,
  lamp: Lamp,
  column: Column,
};

export function BuildingDecorPiece({ piece, at }: { piece: DecorPiece; at: ScenePoint }) {
  const Shape = PIECES[piece];
  return <Shape at={at} />;
}
