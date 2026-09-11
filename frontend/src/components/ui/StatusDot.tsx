import { cn } from "@/lib/utils";

type StatusDotProps = {
  label?: string;
  pulse?: boolean;
  className?: string;
};

export function StatusDot({ label, pulse = true, className }: StatusDotProps) {
  return (
    <span
      className={cn(
        "inline-flex items-center gap-2 text-[11px] uppercase tracking-[0.18em] text-[var(--color-muted)]",
        className,
      )}
    >
      <span className="relative inline-flex h-2 w-2 items-center justify-center">
        <span
          className={cn(
            "absolute inset-0 rounded-full bg-cyan-400",
            pulse && "animate-pulse-ring",
          )}
        />
        <span className="absolute inset-0 rounded-full bg-cyan-300" />
      </span>
      {label}
    </span>
  );
}
