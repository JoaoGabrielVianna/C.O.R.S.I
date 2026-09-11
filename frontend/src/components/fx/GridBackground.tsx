import { cn } from "@/lib/utils";

type GridBackgroundProps = {
  className?: string;
  variant?: "grid" | "dot" | "fine";
  fade?: "radial" | "top" | "bottom" | "none";
};

export function GridBackground({
  className,
  variant = "grid",
  fade = "radial",
}: GridBackgroundProps) {
  const bg =
    variant === "dot"
      ? "bg-dot"
      : variant === "fine"
        ? "bg-grid-fine"
        : "bg-grid";

  const mask =
    fade === "top"
      ? "mask-top-fade"
      : fade === "bottom"
        ? "mask-bottom-fade"
        : fade === "radial"
          ? "mask-radial-fade"
          : "";

  return (
    <div
      aria-hidden
      className={cn(
        "pointer-events-none absolute inset-0 -z-10",
        bg,
        mask,
        className,
      )}
    />
  );
}
