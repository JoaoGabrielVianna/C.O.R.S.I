/**
 * One glyph per model family.
 *
 * Deliberately geometric — a family cue, not a traced logo. They are
 * decorative in the accessibility sense: every place that renders one also
 * renders the model id as text next to it, so identity never rests on the
 * shape or the color alone.
 *
 * Which id maps to which glyph lives in `@/modules/agents/models`; this file
 * holds nothing but components, so fast refresh keeps working.
 */

export type GlyphProps = { className?: string };

/** Anthropic — radiating spokes. */
export function Sunburst({ className }: GlyphProps) {
  return (
    <svg viewBox="0 0 16 16" className={className} fill="none" aria-hidden>
      {[0, 45, 90, 135].map((a) => (
        <line
          key={a}
          x1="8"
          y1="1.8"
          x2="8"
          y2="14.2"
          transform={`rotate(${a} 8 8)`}
          stroke="currentColor"
          strokeWidth="1.5"
          strokeLinecap="round"
        />
      ))}
    </svg>
  );
}

/** OpenAI — interlocking rings. */
export function Knot({ className }: GlyphProps) {
  return (
    <svg viewBox="0 0 16 16" className={className} fill="none" aria-hidden>
      {[0, 60, 120].map((a) => (
        <ellipse
          key={a}
          cx="8"
          cy="8"
          rx="2.9"
          ry="6.3"
          transform={`rotate(${a} 8 8)`}
          stroke="currentColor"
          strokeWidth="1.2"
        />
      ))}
    </svg>
  );
}

/** Gemini — the four-point spark. */
export function Spark({ className }: GlyphProps) {
  return (
    <svg viewBox="0 0 16 16" className={className} aria-hidden>
      <path
        d="M8 1c.6 4 2.4 6.4 7 7-4.6.6-6.4 3-7 7-.6-4-2.4-6.4-7-7 4.6-.6 6.4-3 7-7Z"
        fill="currentColor"
      />
    </svg>
  );
}

/** Meta — the two joined loops. */
export function Loops({ className }: GlyphProps) {
  return (
    <svg viewBox="0 0 16 16" className={className} fill="none" aria-hidden>
      <circle cx="5.5" cy="8" r="3.6" stroke="currentColor" strokeWidth="1.4" />
      <circle cx="10.5" cy="8" r="3.6" stroke="currentColor" strokeWidth="1.4" />
    </svg>
  );
}

/** Mistral — the banded grid. */
export function Grid({ className }: GlyphProps) {
  const rows = [0, 1, 2];
  return (
    <svg viewBox="0 0 16 16" className={className} aria-hidden>
      {rows.map((r) =>
        rows.map((c) => (
          <rect
            key={`${r}-${c}`}
            x={1.5 + c * 4.6}
            y={1.5 + r * 4.6}
            width="3.8"
            height="3.8"
            rx="0.6"
            fill="currentColor"
            opacity={1 - r * 0.3}
          />
        )),
      )}
    </svg>
  );
}

/** xAI — the crossed strokes. */
export function Cross({ className }: GlyphProps) {
  return (
    <svg viewBox="0 0 16 16" className={className} fill="none" aria-hidden>
      <path
        d="M2.6 2.6 13.4 13.4M13.4 2.6 2.6 13.4"
        stroke="currentColor"
        strokeWidth="1.6"
        strokeLinecap="round"
      />
    </svg>
  );
}

/** Fallback for families without a geometry we can draw honestly. */
export function Monogram({ letter, className }: GlyphProps & { letter: string }) {
  return (
    <svg viewBox="0 0 16 16" className={className} aria-hidden>
      <circle cx="8" cy="8" r="7" fill="none" stroke="currentColor" strokeWidth="1.2" />
      <text x="8" y="11.4" textAnchor="middle" fontSize="8.5" fontWeight="700" fill="currentColor">
        {letter}
      </text>
    </svg>
  );
}
