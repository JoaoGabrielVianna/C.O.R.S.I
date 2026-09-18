/**
 * The five silhouettes, plus the pile.
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
 * is separate, in HTML, with real buttons. See `HitLayer`.
 *
 * ── Why no shape encodes a count ───────────────────────────────────────
 * A cabinet with four drawer fronts beside a list of three items is an
 * image that is ALMOST right, and almost-right is worse than textual: a
 * reader who starts counting fronts will eventually be wrong, and will not
 * know it. Anatomy is fixed; the count is a label.
 *
 * ── Why the shading is two stops and not a gradient mesh ───────────────
 * Because isometric reads from flat faces at different values, and one
 * warm light implied from the upper left is enough. Anything richer costs
 * render time for realism a diorama does not want.
 */

import type { ArtifactKind } from "../api/types";
import type { ScenePoint } from "../layout/iso";

const TOP = "var(--palace-scene-object)";
const SIDE = "var(--palace-scene-object-side)";
const EDGE = "var(--color-border-strong)";

function Shadow({ at, rx = 26 }: { at: ScenePoint; rx?: number }) {
  return <ellipse cx={at.x} cy={at.y + 3} rx={rx} ry={rx * 0.38} fill="currentColor" fillOpacity="0.09" />;
}

/** PROJECT: a work desk. A surface with a raised back and four legs. */
function Desk({ at }: { at: ScenePoint }) {
  const { x, y } = at;
  return (
    <g>
      <Shadow at={at} rx={30} />
      <polygon points={`${x - 28},${y - 18} ${x},${y - 32} ${x + 28},${y - 18} ${x},${y - 4}`} fill={TOP} stroke={EDGE} />
      <polygon points={`${x - 28},${y - 18} ${x},${y - 4} ${x},${y + 2} ${x - 28},${y - 12}`} fill={SIDE} stroke={EDGE} />
      <polygon points={`${x + 28},${y - 18} ${x},${y - 4} ${x},${y + 2} ${x + 28},${y - 12}`} fill={SIDE} stroke={EDGE} />
      {/* the raised back */}
      <polygon points={`${x - 26},${y - 19} ${x - 2},${y - 32} ${x - 2},${y - 44} ${x - 26},${y - 31}`} fill={SIDE} stroke={EDGE} />
    </g>
  );
}

/** LIST: a drawer cabinet. Fixed anatomy: three fronts, always. */
function Cabinet({ at }: { at: ScenePoint }) {
  const { x, y } = at;
  const h = 40;
  return (
    <g>
      <Shadow at={at} rx={24} />
      <polygon points={`${x - 22},${y - 12 - h} ${x},${y - 24 - h} ${x + 22},${y - 12 - h} ${x},${y - h}`} fill={TOP} stroke={EDGE} />
      <polygon points={`${x - 22},${y - 12 - h} ${x},${y - h} ${x},${y} ${x - 22},${y - 12}`} fill={SIDE} stroke={EDGE} />
      <polygon points={`${x + 22},${y - 12 - h} ${x},${y - h} ${x},${y} ${x + 22},${y - 12}`} fill={TOP} fillOpacity="0.72" stroke={EDGE} />
      {[0, 1, 2].map((i) => (
        <line
          key={i}
          x1={x + 2}
          y1={y - h + 6 + i * 12}
          x2={x + 20}
          y2={y - h - 4 + i * 12}
          stroke={EDGE}
          strokeWidth="0.9"
        />
      ))}
    </g>
  );
}

/** PLAN: an ordered shelf. Dividers run left to right, like the steps. */
function Shelf({ at }: { at: ScenePoint }) {
  const { x, y } = at;
  const h = 34;
  return (
    <g>
      <Shadow at={at} rx={30} />
      <polygon points={`${x - 30},${y - 16 - h} ${x},${y - 31 - h} ${x + 30},${y - 16 - h} ${x},${y - h}`} fill={TOP} stroke={EDGE} />
      <polygon points={`${x - 30},${y - 16 - h} ${x},${y - h} ${x},${y} ${x - 30},${y - 16}`} fill={SIDE} stroke={EDGE} />
      <polygon points={`${x + 30},${y - 16 - h} ${x},${y - h} ${x},${y} ${x + 30},${y - 16}`} fill={TOP} fillOpacity="0.72" stroke={EDGE} />
      <line x1={x - 30} y1={y - 16 - h / 2} x2={x} y2={y - h / 2} stroke={EDGE} strokeWidth="0.9" />
      <line x1={x} y1={y - h / 2} x2={x + 30} y2={y - 16 - h / 2} stroke={EDGE} strokeWidth="0.9" />
    </g>
  );
}

/** NOTE: a wall board. Mounted, not standing. No contact shadow. */
function Board({ at }: { at: ScenePoint }) {
  const { x, y } = at;
  return (
    <g>
      <polygon points={`${x - 24},${y - 6} ${x - 24},${y - 32} ${x + 24},${y - 44} ${x + 24},${y - 18}`} fill={TOP} stroke={EDGE} strokeWidth="1.2" />
      <polygon points={`${x - 19},${y - 11} ${x - 19},${y - 29} ${x + 19},${y - 38} ${x + 19},${y - 20}`} fill="var(--palace-scene-floor)" fillOpacity="0.55" />
    </g>
  );
}

/** The room's memory surface: a closed notebook on a low rest. */
export function Notebook({ at }: { at: ScenePoint }) {
  const { x, y } = at;
  return (
    <g>
      <Shadow at={at} rx={20} />
      <polygon points={`${x - 18},${y - 8} ${x},${y - 17} ${x + 18},${y - 8} ${x},${y + 1}`} fill={TOP} stroke={EDGE} />
      <polygon points={`${x - 18},${y - 8} ${x},${y + 1} ${x},${y + 5} ${x - 18},${y - 4}`} fill={SIDE} stroke={EDGE} />
      <polygon points={`${x + 18},${y - 8} ${x},${y + 1} ${x},${y + 5} ${x + 18},${y - 4}`} fill={SIDE} stroke={EDGE} />
      <line x1={x - 9} y1={y - 12} x2={x + 9} y2={y - 12} stroke="var(--color-brand-500)" strokeWidth="1.4" strokeOpacity="0.7" />
    </g>
  );
}

const SHAPES: Record<ArtifactKind, (props: { at: ScenePoint }) => React.ReactElement> = {
  project: Desk,
  list: Cabinet,
  plan: Shelf,
  note: Board,
};

export function ArtifactShape({ kind, at }: { kind: ArtifactKind; at: ScenePoint }) {
  const Shape = SHAPES[kind];
  return <Shape at={at} />;
}

/**
 * The overflow: the same silhouette, stacked.
 *
 * It looks like more of the same thing because it IS more of the same
 * thing. A generic box would be an affordance whose contents a reader
 * could not predict.
 */
export function PileShape({ kind, at }: { kind: ArtifactKind; at: ScenePoint }) {
  return (
    <g>
      <g opacity="0.55" transform="translate(-6,-9)">
        <ArtifactShape kind={kind} at={at} />
      </g>
      <g opacity="0.8" transform="translate(-3,-4)">
        <ArtifactShape kind={kind} at={at} />
      </g>
      <ArtifactShape kind={kind} at={at} />
    </g>
  );
}
