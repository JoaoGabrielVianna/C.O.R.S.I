import { cn } from "@/lib/utils";

/**
 * Button class composer — shared between <Button> and <ButtonLink>.
 *
 * Variant taxonomy adopted from the Corsi ERP system, translated to dark mode:
 *   · primary  — cyan accent CTA, shine-on-hover + halo
 *   · solid    — high-contrast surface (foreground on background)
 *   · outline  — surface card with hairline ring
 *   · ghost    — transparent, text-only
 *   · subtle   — faint cyan tint surface
 *   · link     — inline anchor with underline
 *
 * Sizes match ERP: sm h-8 · md h-10 · lg h-12 · icon size-10.
 * Single easing: --ease-premium (250 ms).
 */

export type ButtonVariant =
  | "primary"
  | "solid"
  | "outline"
  | "ghost"
  | "subtle"
  | "link";
export type ButtonSize = "sm" | "md" | "lg" | "icon";

const base = [
  "group/btn inline-flex items-center justify-center gap-2 whitespace-nowrap",
  "font-medium tracking-tight select-none outline-none",
  "rounded-md",
  "transition-[transform,box-shadow,background-color,color,border-color]",
  "duration-250 ease-premium",
  "focus-visible:ring-2 focus-visible:ring-accent/40 focus-visible:ring-offset-2 focus-visible:ring-offset-background",
  "disabled:pointer-events-none disabled:opacity-50",
  "[&_svg]:size-4 [&_svg]:shrink-0",
].join(" ");

const variants: Record<ButtonVariant, string> = {
  primary: [
    "shine-on-hover",
    "bg-accent text-white",
    "shadow-cyan-halo",
    "hover:bg-accent-bright hover:shadow-cyan-glow hover:-translate-y-px",
    "active:translate-y-0",
  ].join(" "),
  solid: [
    "bg-foreground text-background",
    "shadow-hairline-strong",
    "hover:bg-white hover:-translate-y-px",
    "active:translate-y-0",
  ].join(" "),
  outline: [
    "bg-surface/60 text-foreground",
    "border border-border",
    "hover:bg-surface-2 hover:border-border-strong hover:-translate-y-px",
    "active:translate-y-0",
  ].join(" "),
  ghost: [
    "bg-transparent text-muted",
    "hover:bg-white/4 hover:text-foreground",
  ].join(" "),
  subtle: [
    "bg-accent/8 text-accent-bright",
    "border border-accent/20",
    "hover:bg-accent/14 hover:border-accent/35",
  ].join(" "),
  link: [
    "text-accent-bright underline-offset-4",
    "hover:underline",
  ].join(" "),
};

const sizes: Record<ButtonSize, string> = {
  sm: "h-8 px-3 text-[13px]",
  md: "h-10 px-4 text-sm",
  lg: "h-12 px-6 text-[15px]",
  icon: "size-10 p-0",
};

export function buttonClasses(
  variant: ButtonVariant = "primary",
  size: ButtonSize = "md",
  className?: string,
) {
  return cn(base, variants[variant], sizes[size], className);
}
