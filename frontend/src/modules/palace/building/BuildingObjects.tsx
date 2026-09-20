/**
 * The furniture, at house scale.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   PAINT ONLY. NOTHING HERE IS INTERACTIVE OR REACHABLE
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * Every shape takes an anchor point that came from the geometry and draws
 * around it. None of them decides where it is, none carries a name that
 * could be read, and all of them are `aria-hidden`: the interactive layer
 * is separate, in HTML, with real buttons.
 *
 * ── Why these are not `scene/Affordances.tsx` ──────────────────────────
 * Same vocabulary, different level of detail, and the difference is
 * forced by arithmetic rather than by taste. A room's own scene draws a
 * cabinet 66 units tall because it has one room to spend the frame on. The
 * house draws nine positions per room across up to a dozen rooms, and at
 * that density a 66-unit cabinet needs a lattice so wide that the whole
 * building falls under the 44px floor and hands over to the Library. These
 * are the same four pieces of furniture, drawn to fit a 52x44 box.
 *
 * They deliberately echo the larger set: the same three fills, the same
 * implied light from the upper left, the same anatomy. Somebody who opens
 * a room should recognise the objects they just pressed.
 *
 * ── The one thing that differs, and why ────────────────────────────────
 * A `note` is a WALL board in the room's own scene, mounted on the back
 * wall. Here it stands on an easel on the floor.
 *
 * That is not a change of metaphor, it is the only honest option at this
 * scale. Objects in the house sit on one lattice so that their targets can
 * be proved disjoint; a wall-mounted board would need a second coordinate
 * system whose positions come from the floor index, and the room's back
 * wall here is shared with the room behind it and only three elevation
 * steps tall. A board on a stand is still a board.
 *
 * ── Why no shape encodes a count ───────────────────────────────────────
 * A cabinet with four drawer fronts beside a list of three items is an
 * image that is ALMOST right, and almost-right is worse than textual: a
 * reader who starts counting fronts will eventually be wrong, and will not
 * know it. Anatomy is fixed; the count is a label.
 */

import type { ArtifactKind } from "../api/types";
import type { ScenePoint } from "../layout/iso";

const TOP = "var(--palace-scene-object)";
const SIDE = "var(--palace-scene-object-side)";
const EDGE = "var(--color-border-strong)";

/**
 * The contact shadow a REAL object gets.
 *
 * Raised from 0.10 to 0.14 in C3.1, when the floor came down a step: a
 * shadow tuned against near-white disappears against wood, and an object
 * without one stops sitting on the floor. It stays clearly heavier than
 * the decorative shadow (0.09), because weight is one of the things
 * telling a reader which objects in this room actually mean something.
 */
function Shadow({ at, rx = 18 }: { at: ScenePoint; rx?: number }) {
  return (
    <ellipse
      cx={at.x}
      cy={at.y + 2}
      rx={rx}
      ry={rx * 0.38}
      fill="currentColor"
      fillOpacity="0.14"
    />
  );
}

/** PROJECT: a work desk. A surface with a raised back and legs. */
function Desk({ at }: { at: ScenePoint }) {
  const { x, y } = at;
  return (
    <g>
      <Shadow at={at} rx={22} />
      <polygon
        points={`${x - 22},${y - 14} ${x},${y - 25} ${x + 22},${y - 14} ${x},${y - 3}`}
        fill={TOP}
        stroke={EDGE}
      />
      <polygon
        points={`${x - 22},${y - 14} ${x},${y - 3} ${x},${y + 3} ${x - 22},${y - 8}`}
        fill={SIDE}
        stroke={EDGE}
      />
      <polygon
        points={`${x + 22},${y - 14} ${x},${y - 3} ${x},${y + 3} ${x + 22},${y - 8}`}
        fill={SIDE}
        stroke={EDGE}
      />
      {/* the raised back */}
      <polygon
        points={`${x - 20},${y - 15} ${x - 2},${y - 25} ${x - 2},${y - 38} ${x - 20},${y - 28}`}
        fill={SIDE}
        stroke={EDGE}
      />
    </g>
  );
}

/** LIST: a drawer cabinet. Fixed anatomy: three fronts, always. */
function Cabinet({ at }: { at: ScenePoint }) {
  const { x, y } = at;
  const h = 26;
  return (
    <g>
      <Shadow at={at} rx={16} />
      <polygon
        points={`${x - 15},${y - 8 - h} ${x},${y - 16 - h} ${x + 15},${y - 8 - h} ${x},${y - h}`}
        fill={TOP}
        stroke={EDGE}
      />
      <polygon
        points={`${x - 15},${y - 8 - h} ${x},${y - h} ${x},${y} ${x - 15},${y - 8}`}
        fill={SIDE}
        stroke={EDGE}
      />
      <polygon
        points={`${x + 15},${y - 8 - h} ${x},${y - h} ${x},${y} ${x + 15},${y - 8}`}
        fill={TOP}
        fillOpacity="0.72"
        stroke={EDGE}
      />
      {[0, 1, 2].map((i) => (
        <line
          key={i}
          x1={x + 2}
          y1={y - h + 4 + i * 8}
          x2={x + 13}
          y2={y - h - 2 + i * 8}
          stroke={EDGE}
          strokeWidth="0.8"
        />
      ))}
    </g>
  );
}

/** PLAN: an ordered shelf. Dividers run left to right, like the steps. */
function Shelf({ at }: { at: ScenePoint }) {
  const { x, y } = at;
  const h = 22;
  return (
    <g>
      <Shadow at={at} rx={22} />
      <polygon
        points={`${x - 23},${y - 12 - h} ${x},${y - 23 - h} ${x + 23},${y - 12 - h} ${x},${y - h}`}
        fill={TOP}
        stroke={EDGE}
      />
      <polygon
        points={`${x - 23},${y - 12 - h} ${x},${y - h} ${x},${y} ${x - 23},${y - 12}`}
        fill={SIDE}
        stroke={EDGE}
      />
      <polygon
        points={`${x + 23},${y - 12 - h} ${x},${y - h} ${x},${y} ${x + 23},${y - 12}`}
        fill={TOP}
        fillOpacity="0.72"
        stroke={EDGE}
      />
      <line
        x1={x - 23}
        y1={y - 12 - h / 2}
        x2={x}
        y2={y - h / 2}
        stroke={EDGE}
        strokeWidth="0.8"
      />
      <line
        x1={x}
        y1={y - h / 2}
        x2={x + 23}
        y2={y - 12 - h / 2}
        stroke={EDGE}
        strokeWidth="0.8"
      />
    </g>
  );
}

/** NOTE: a board on an easel. See the header for why it stands. */
function Board({ at }: { at: ScenePoint }) {
  const { x, y } = at;
  return (
    <g>
      <Shadow at={at} rx={14} />
      {/* the easel leg, behind the panel */}
      <line x1={x} y1={y - 12} x2={x - 6} y2={y + 1} stroke={EDGE} strokeWidth="1.4" />
      <line x1={x} y1={y - 12} x2={x + 6} y2={y + 1} stroke={EDGE} strokeWidth="1.4" />
      <polygon
        points={`${x - 19},${y - 16} ${x - 19},${y - 34} ${x + 19},${y - 42} ${x + 19},${y - 24}`}
        fill={TOP}
        stroke={EDGE}
        strokeWidth="1.1"
      />
      <polygon
        points={`${x - 14},${y - 20} ${x - 14},${y - 31} ${x + 14},${y - 37} ${x + 14},${y - 26}`}
        fill="var(--palace-scene-floor)"
        fillOpacity="0.55"
      />
    </g>
  );
}

/** The room's memory surface: a closed notebook on a low rest. */
export function BuildingNotebook({ at }: { at: ScenePoint }) {
  const { x, y } = at;
  return (
    <g>
      <Shadow at={at} rx={14} />
      <polygon
        points={`${x - 13},${y - 6} ${x},${y - 12} ${x + 13},${y - 6} ${x},${y}`}
        fill={TOP}
        stroke={EDGE}
      />
      <polygon
        points={`${x - 13},${y - 6} ${x},${y} ${x},${y + 4} ${x - 13},${y - 2}`}
        fill={SIDE}
        stroke={EDGE}
      />
      <polygon
        points={`${x + 13},${y - 6} ${x},${y} ${x},${y + 4} ${x + 13},${y - 2}`}
        fill={SIDE}
        stroke={EDGE}
      />
      <line
        x1={x - 6}
        y1={y - 9}
        x2={x + 6}
        y2={y - 9}
        stroke="var(--color-brand-500)"
        strokeWidth="1.3"
        strokeOpacity="0.7"
      />
    </g>
  );
}

const SHAPES: Record<ArtifactKind, (props: { at: ScenePoint }) => React.ReactElement> = {
  project: Desk,
  list: Cabinet,
  plan: Shelf,
  note: Board,
};

export function BuildingObject({ kind, at }: { kind: ArtifactKind; at: ScenePoint }) {
  const Shape = SHAPES[kind];
  return <Shape at={at} />;
}

/**
 * The overflow: crates, and deliberately NOT more furniture.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   A PILE HOLDS MIXED KINDS, SO IT CANNOT WEAR ONE OF THEIR SILHOUETTES
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * The room's own scene piles one kind at a time, because it groups by
 * kind, so stacking that kind's silhouette is honest there. The house
 * fills positions in canonical order regardless of kind, so a room's
 * overflow is whatever did not fit: two lists, a note and a plan. Drawing
 * it as three cabinets would claim the remainder is all lists.
 *
 * So it is stacked crates: a shape that belongs to no kind, says "more
 * things", and cannot be mistaken for a fourth piece of furniture. The
 * count is a label, never the number of crates drawn.
 */
export function BuildingPile({ at }: { at: ScenePoint }) {
  const { x, y } = at;
  const crate = (dx: number, dy: number, w: number, fill: string) => (
    <>
      <polygon
        points={`${x + dx - w},${y + dy - 7} ${x + dx},${y + dy - 13} ${x + dx + w},${y + dy - 7} ${x + dx},${y + dy - 1}`}
        fill={fill}
        stroke={EDGE}
      />
      <polygon
        points={`${x + dx - w},${y + dy - 7} ${x + dx},${y + dy - 1} ${x + dx},${y + dy + 6} ${x + dx - w},${y + dy}`}
        fill={SIDE}
        stroke={EDGE}
      />
      <polygon
        points={`${x + dx + w},${y + dy - 7} ${x + dx},${y + dy - 1} ${x + dx},${y + dy + 6} ${x + dx + w},${y + dy}`}
        fill={SIDE}
        stroke={EDGE}
      />
    </>
  );

  return (
    <g>
      <Shadow at={at} rx={20} />
      {crate(0, 0, 17, TOP)}
      {crate(-4, -13, 12, TOP)}
      {crate(6, -21, 9, TOP)}
    </g>
  );
}

/**
 * The unfiled tray, at the entrance.
 *
 * ── D4: it must not read as a room, or as storage ──────────────────────
 * No slab, no walls, no decoration, no drawer fronts. A dashed open
 * outline whose silhouette belongs to a different class on purpose: things
 * that have not been put away sit by the door.
 */
export function BuildingTray({ at }: { at: ScenePoint }) {
  const { x, y } = at;
  return (
    <g>
      <polygon
        points={`${x - 22},${y - 8} ${x},${y - 19} ${x + 22},${y - 8} ${x},${y + 3}`}
        fill="none"
        stroke="currentColor"
        strokeOpacity="0.55"
        strokeDasharray="5 4"
        strokeWidth="1.4"
      />
      <polygon
        points={`${x - 22},${y - 8} ${x},${y + 3} ${x},${y + 10} ${x - 22},${y - 1}`}
        fill="currentColor"
        fillOpacity="0.06"
        stroke="currentColor"
        strokeOpacity="0.35"
        strokeDasharray="5 4"
      />
      <polygon
        points={`${x + 22},${y - 8} ${x},${y + 3} ${x},${y + 10} ${x + 22},${y - 1}`}
        fill="currentColor"
        fillOpacity="0.04"
        stroke="currentColor"
        strokeOpacity="0.35"
        strokeDasharray="5 4"
      />
    </g>
  );
}
