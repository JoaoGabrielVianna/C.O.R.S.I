import { cn } from "@/lib/utils";

type LogoProps = {
  className?: string;
};

/**
 * C.O.R.S.I wordmark — minimal SVG monogram.
 * Designed to read like Linear/Vercel marks.
 */
export function LogoMark({ className }: LogoProps) {
  return (
    <svg
      viewBox="0 0 32 32"
      fill="none"
      xmlns="http://www.w3.org/2000/svg"
      className={cn("h-6 w-6", className)}
      aria-hidden
    >
      <defs>
        <linearGradient id="corsi-grad" x1="0" y1="0" x2="32" y2="32">
          <stop offset="0%" stopColor="#22d3ee" />
          <stop offset="100%" stopColor="#0891b2" />
        </linearGradient>
      </defs>
      <rect
        x="1"
        y="1"
        width="30"
        height="30"
        rx="8"
        stroke="url(#corsi-grad)"
        strokeWidth="1.5"
      />
      <path
        d="M10.5 11.5h7.5a3.5 3.5 0 0 1 3.5 3.5v0a3.5 3.5 0 0 1-3.5 3.5h-7.5"
        stroke="url(#corsi-grad)"
        strokeWidth="1.75"
        strokeLinecap="round"
      />
      <circle cx="10.5" cy="20.5" r="1.5" fill="#22d3ee" />
    </svg>
  );
}

export function Wordmark({ className }: LogoProps) {
  return (
    <span
      className={cn(
        "font-display text-[14px] font-semibold tracking-[0.18em] text-[var(--color-foreground)]",
        className,
      )}
    >
      C.O.R.S.I
    </span>
  );
}

export function LogoLockup({ className }: LogoProps) {
  return (
    <div className={cn("inline-flex items-center gap-2.5", className)}>
      <LogoMark />
      <Wordmark />
    </div>
  );
}
