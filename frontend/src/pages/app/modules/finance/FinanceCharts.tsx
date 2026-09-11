import { cn } from "@/lib/utils";

/**
 * FinanceCharts — minimal CSS/SVG primitives. No external chart lib.
 *
 *   BarChart        · vertical bars with labels under each
 *   DonutChart      · single-ring donut composed of arc segments
 *   LineChart       · smooth line + soft area fill
 *   StackedBar      · single horizontal bar with multiple coloured segments
 */

type Segment = {
  key: string;
  label: string;
  value: number;
  /** Tailwind color token base, e.g. "emerald". */
  color: string;
};

/* ── Bar chart ───────────────────────────────────────────────────────── */

export function BarChart({
  segments,
  height = 96,
  format = (v) => String(v),
}: {
  segments: Segment[];
  height?: number;
  format?: (value: number) => string;
}) {
  if (segments.length === 0) return <ChartEmpty />;
  const max = Math.max(1, ...segments.map((s) => s.value));
  return (
    <div className="flex items-end gap-2" style={{ height }}>
      {segments.map((s) => (
        <div key={s.key} className="flex flex-1 flex-col items-stretch gap-1">
          <div className="relative flex flex-1 items-end">
            <span
              className={cn("w-full rounded-t-sm transition-colors", `bg-${s.color}-500`)}
              style={{ height: `${Math.max(4, (s.value / max) * 100)}%` }}
              title={`${s.label}: ${format(s.value)}`}
            />
          </div>
          <span className="truncate text-center font-mono text-[9.5px] uppercase tracking-wide text-(--color-muted-foreground)">
            {s.label}
          </span>
        </div>
      ))}
    </div>
  );
}

/* ── Donut chart ─────────────────────────────────────────────────────── */

export function DonutChart({
  segments,
  size = 160,
  thickness = 16,
  centerLabel,
  centerValue,
}: {
  segments: Segment[];
  size?: number;
  thickness?: number;
  centerLabel?: string;
  centerValue?: string;
}) {
  const total = segments.reduce((acc, s) => acc + s.value, 0);
  const radius = (size - thickness) / 2;
  const circumference = 2 * Math.PI * radius;

  if (total === 0) return <ChartEmpty />;

  // Pre-compute each segment's start offset so the render body stays pure.
  const computed = segments.reduce<Array<{ key: string; color: string; length: number; offset: number }>>(
    (acc, s) => {
      const previous = acc[acc.length - 1];
      const startOffset = previous ? previous.offset + previous.length : 0;
      acc.push({
        key: s.key,
        color: s.color,
        length: (s.value / total) * circumference,
        offset: startOffset,
      });
      return acc;
    },
    [],
  );

  return (
    <div className="flex items-center gap-4">
      <svg
        width={size}
        height={size}
        viewBox={`0 0 ${size} ${size}`}
        className="-rotate-90"
      >
        <circle
          cx={size / 2}
          cy={size / 2}
          r={radius}
          fill="none"
          strokeWidth={thickness}
          className="stroke-(--color-muted)"
        />
        {computed.map((c) => (
          <circle
            key={c.key}
            cx={size / 2}
            cy={size / 2}
            r={radius}
            fill="none"
            strokeWidth={thickness}
            strokeDasharray={`${c.length} ${circumference - c.length}`}
            strokeDashoffset={-c.offset}
            className={cn("transition-[stroke-dasharray]", `stroke-${c.color}-500`)}
          />
        ))}
      </svg>
      <div className="flex-1 space-y-1.5">
        {centerLabel || centerValue ? (
          <div className="border-b border-(--color-border) pb-2">
            {centerLabel ? (
              <p className="font-mono text-[10px] uppercase tracking-[0.16em] text-(--color-muted-foreground)">
                {centerLabel}
              </p>
            ) : null}
            {centerValue ? (
              <p className="font-display text-lg font-semibold tracking-tight text-(--color-foreground)">
                {centerValue}
              </p>
            ) : null}
          </div>
        ) : null}
        <ul className="space-y-1">
          {segments.map((s) => {
            const pct = total === 0 ? 0 : Math.round((s.value / total) * 100);
            return (
              <li key={s.key} className="flex items-center gap-2">
                <span aria-hidden className={cn("size-2 shrink-0 rounded-full", `bg-${s.color}-500`)} />
                <span className="min-w-0 flex-1 truncate text-[12px] text-(--color-foreground)">{s.label}</span>
                <span className="font-mono text-[10px] text-(--color-muted-foreground)">{pct}%</span>
              </li>
            );
          })}
        </ul>
      </div>
    </div>
  );
}

/* ── Line chart (single series, optional comparison) ─────────────────── */

export function LineChart({
  points,
  comparison,
  height = 120,
  format = (v) => String(v),
}: {
  points: { x: string; y: number }[];
  comparison?: { x: string; y: number }[];
  height?: number;
  format?: (value: number) => string;
}) {
  if (points.length === 0) return <ChartEmpty />;
  const width = 320;
  const all = [...points.map((p) => p.y), ...(comparison?.map((p) => p.y) ?? [])];
  const max = Math.max(1, ...all);
  const min = Math.min(0, ...all);
  const span = Math.max(1, max - min);

  const toCoord = (y: number, i: number, total: number) => {
    const x = (i / Math.max(1, total - 1)) * width;
    const norm = (y - min) / span;
    const yy = height - norm * (height - 8) - 4;
    return [x, yy] as const;
  };

  const buildPath = (series: { y: number }[]) =>
    series.length === 0
      ? ""
      : series
          .map((p, i) => {
            const [x, y] = toCoord(p.y, i, series.length);
            return `${i === 0 ? "M" : "L"} ${x.toFixed(1)} ${y.toFixed(1)}`;
          })
          .join(" ");

  const buildArea = (series: { y: number }[]) => {
    if (series.length === 0) return "";
    const path = series
      .map((p, i) => {
        const [x, y] = toCoord(p.y, i, series.length);
        return `${i === 0 ? "M" : "L"} ${x.toFixed(1)} ${y.toFixed(1)}`;
      })
      .join(" ");
    const [, lastY] = toCoord(series[series.length - 1].y, series.length - 1, series.length);
    return `${path} L ${width.toFixed(1)} ${lastY.toFixed(1)} L ${width.toFixed(1)} ${height} L 0 ${height} Z`;
  };

  return (
    <div className="space-y-1.5">
      <svg viewBox={`0 0 ${width} ${height}`} className="h-auto w-full" preserveAspectRatio="none">
        {/* baseline */}
        <line x1={0} x2={width} y1={height - 4} y2={height - 4} className="stroke-(--color-border)" strokeWidth="1" />
        {/* comparison area */}
        {comparison ? (
          <>
            <path d={buildArea(comparison)} className="fill-(--color-muted)/40" />
            <path d={buildPath(comparison)} className="fill-none stroke-(--color-muted-foreground)/60" strokeWidth="1.5" strokeDasharray="3 3" />
          </>
        ) : null}
        {/* primary area + line */}
        <path d={buildArea(points)} className="fill-(--color-brand-500)/15" />
        <path d={buildPath(points)} className="fill-none stroke-(--color-brand-500)" strokeWidth="2" />
      </svg>
      <div className="flex items-baseline justify-between gap-2 font-mono text-[10px] text-(--color-muted-foreground)">
        <span>{points[0]?.x ?? ""}</span>
        <span title={`max: ${format(max)} · min: ${format(min)}`}>
          {format(min)} → {format(max)}
        </span>
        <span>{points[points.length - 1]?.x ?? ""}</span>
      </div>
    </div>
  );
}

/* ── Stacked bar (horizontal) ────────────────────────────────────────── */

export function StackedBar({
  segments,
  format = (v) => String(v),
}: {
  segments: Segment[];
  format?: (value: number) => string;
}) {
  const total = segments.reduce((acc, s) => acc + s.value, 0);
  if (total === 0) return <ChartEmpty />;
  return (
    <div className="space-y-2">
      <div className="flex h-2 overflow-hidden rounded-full bg-(--color-muted)">
        {segments.map((s) => (
          <span
            key={s.key}
            className={cn("h-full", `bg-${s.color}-500`)}
            style={{ width: `${(s.value / total) * 100}%` }}
            title={`${s.label}: ${format(s.value)}`}
          />
        ))}
      </div>
      <ul className="flex flex-wrap gap-x-3 gap-y-1">
        {segments.map((s) => {
          const pct = total === 0 ? 0 : Math.round((s.value / total) * 100);
          return (
            <li key={s.key} className="inline-flex items-center gap-1.5">
              <span aria-hidden className={cn("size-1.5 rounded-full", `bg-${s.color}-500`)} />
              <span className="text-[11.5px] text-(--color-foreground)">{s.label}</span>
              <span className="font-mono text-[10px] text-(--color-muted-foreground)">{pct}%</span>
            </li>
          );
        })}
      </ul>
    </div>
  );
}

/* ── Horizontal bars (ranked list) ───────────────────────────────────── */

export function HorizontalBars({
  segments,
  format = (v) => String(v),
  labelWidth = "min-w-[88px] max-w-[140px]",
}: {
  segments: Segment[];
  format?: (value: number) => string;
  labelWidth?: string;
}) {
  if (segments.length === 0) return <ChartEmpty />;
  const max = Math.max(1, ...segments.map((s) => s.value));
  return (
    <ul className="space-y-2">
      {segments.map((s) => (
        <li key={s.key} className="flex items-center gap-2.5">
          <span className={cn("truncate text-[12px] text-(--color-foreground)", labelWidth)}>
            {s.label}
          </span>
          <span className="relative h-1.5 flex-1 overflow-hidden rounded-full bg-(--color-muted)">
            <span
              className={cn("absolute inset-y-0 left-0 rounded-full", `bg-${s.color}-500`)}
              style={{ width: `${(s.value / max) * 100}%` }}
              title={`${s.label}: ${format(s.value)}`}
            />
          </span>
          <span className="w-20 shrink-0 text-right font-mono text-[10.5px] text-(--color-muted-foreground)">
            {format(s.value)}
          </span>
        </li>
      ))}
    </ul>
  );
}

/* ── Empty ───────────────────────────────────────────────────────────── */

function ChartEmpty() {
  return (
    <div className="flex h-24 items-center justify-center rounded-md border border-dashed border-(--color-border) text-[11px] text-(--color-muted-foreground)">
      —
    </div>
  );
}
