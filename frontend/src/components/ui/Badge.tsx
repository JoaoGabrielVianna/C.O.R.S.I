import type { HTMLAttributes } from "react";
import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "@/lib/utils";

const badgeVariants = cva(
  "inline-flex items-center gap-1.5 rounded-full border font-medium transition-colors",
  {
    variants: {
      variant: {
        neutral:
          "border-(--color-border) bg-(--color-muted) text-(--color-slate-700)",
        brand:
          "border-(--color-brand-200) bg-(--color-brand-50) text-(--color-brand-700)",
        solid:
          "border-transparent bg-(--color-primary) text-(--color-primary-foreground)",
        outline:
          "border-(--color-border) bg-transparent text-(--color-foreground)",
        success:
          "border-emerald-200 bg-emerald-50 text-emerald-700",
        warning:
          "border-amber-200 bg-amber-50 text-amber-800",
        info:
          "border-sky-200 bg-sky-50 text-sky-700",
        danger:
          "border-rose-200 bg-rose-50 text-rose-700",
      },
      size: {
        sm: "px-2 py-0.5 text-[11px]",
        md: "px-2.5 py-0.5 text-xs",
        lg: "px-3 py-1 text-sm",
      },
    },
    defaultVariants: { variant: "neutral", size: "md" },
  },
);

export interface BadgeProps
  extends HTMLAttributes<HTMLSpanElement>,
    VariantProps<typeof badgeVariants> {}

export function Badge({ className, variant, size, ...props }: BadgeProps) {
  return (
    <span
      className={cn(badgeVariants({ variant, size }), className)}
      {...props}
    />
  );
}
