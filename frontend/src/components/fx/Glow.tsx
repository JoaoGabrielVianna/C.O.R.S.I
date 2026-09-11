import { cn } from "@/lib/utils";

type GlowProps = {
  className?: string;
  intensity?: "soft" | "strong";
};

/**
 * Two-layer aurora — cyan + deep teal — for the hero and section accents.
 */
export function Aurora({ className, intensity = "soft" }: GlowProps) {
  return (
    <div
      aria-hidden
      className={cn("pointer-events-none absolute inset-0 -z-10 overflow-hidden", className)}
    >
      <div
        className={cn(
          "absolute left-1/2 top-[-20%] h-[600px] w-[800px] -translate-x-1/2 rounded-full blur-[120px] animate-aurora",
          intensity === "strong" ? "opacity-80" : "opacity-50",
        )}
        style={{
          background:
            "radial-gradient(closest-side, rgba(6,182,212,0.45), rgba(6,182,212,0) 70%)",
        }}
      />
      <div
        className={cn(
          "absolute right-[-10%] top-[20%] h-[420px] w-[520px] rounded-full blur-[100px] animate-aurora-2",
          intensity === "strong" ? "opacity-70" : "opacity-40",
        )}
        style={{
          background:
            "radial-gradient(closest-side, rgba(34,211,238,0.32), rgba(34,211,238,0) 70%)",
        }}
      />
      <div
        className="absolute left-[-5%] top-[40%] h-[360px] w-[440px] rounded-full opacity-30 blur-[110px] animate-aurora"
        style={{
          background:
            "radial-gradient(closest-side, rgba(8,145,178,0.4), rgba(8,145,178,0) 70%)",
        }}
      />
    </div>
  );
}

type SpotGlowProps = {
  className?: string;
  size?: number;
  color?: string;
};

export function SpotGlow({
  className,
  size = 400,
  color = "rgba(6,182,212,0.25)",
}: SpotGlowProps) {
  return (
    <div
      aria-hidden
      className={cn("pointer-events-none absolute rounded-full blur-[80px]", className)}
      style={{
        width: size,
        height: size,
        background: `radial-gradient(closest-side, ${color}, transparent 70%)`,
      }}
    />
  );
}
