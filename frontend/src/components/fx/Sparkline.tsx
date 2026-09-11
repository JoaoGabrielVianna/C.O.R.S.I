import { useMemo } from "react";
import { cn } from "@/lib/utils";

type SparklineProps = {
  data: number[];
  className?: string;
  stroke?: string;
  fill?: string;
  height?: number;
};

export function Sparkline({
  data,
  className,
  stroke = "#22d3ee",
  fill = "rgba(34,211,238,0.18)",
  height = 80,
}: SparklineProps) {
  const { line, area, points } = useMemo(() => {
    if (data.length === 0)
      return { line: "", area: "", points: [] as Array<[number, number]> };
    const min = Math.min(...data);
    const max = Math.max(...data);
    const span = Math.max(1, max - min);
    const w = 100;
    const h = 100;
    const step = w / Math.max(1, data.length - 1);
    const pts = data.map<[number, number]>((v, i) => [
      Number((i * step).toFixed(2)),
      Number((h - ((v - min) / span) * h * 0.85 - 7.5).toFixed(2)),
    ]);
    const l = pts
      .map(([x, y], i) => `${i === 0 ? "M" : "L"} ${x} ${y}`)
      .join(" ");
    const a = `${l} L ${w} ${h} L 0 ${h} Z`;
    return { line: l, area: a, points: pts };
  }, [data]);

  const last = points[points.length - 1];

  return (
    <svg
      viewBox="0 0 100 100"
      preserveAspectRatio="none"
      className={cn("w-full", className)}
      style={{ height }}
      aria-hidden
    >
      <defs>
        <linearGradient id="spark-fill" x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stopColor={fill} />
          <stop offset="100%" stopColor="rgba(34,211,238,0)" />
        </linearGradient>
      </defs>
      <path d={area} fill="url(#spark-fill)" />
      <path
        d={line}
        fill="none"
        stroke={stroke}
        strokeWidth="1.4"
        strokeLinejoin="round"
        strokeLinecap="round"
        vectorEffect="non-scaling-stroke"
      />
      {last ? (
        <>
          <circle
            cx={last[0]}
            cy={last[1]}
            r="2.5"
            fill={stroke}
            opacity="0.25"
          />
          <circle cx={last[0]} cy={last[1]} r="1.2" fill={stroke} />
        </>
      ) : null}
    </svg>
  );
}
