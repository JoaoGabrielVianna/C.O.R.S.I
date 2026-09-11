import { forwardRef, type InputHTMLAttributes } from "react";
import { cn } from "@/lib/utils";

/**
 * Input — ERP-exact anatomy.
 *
 * Mirror of `erp-lucas/frontend/src/shared/ui/input.tsx`.
 *   · h-10 · rounded-xl · border-(--color-border) · bg-(--color-card)
 *   · px-3.5 py-2 · text-sm · shadow-(--shadow-soft)
 *   · hover: border-(--color-slate-300) — slight lighten
 *   · focus: border-(--color-brand-500) + 20% ring
 *   · 250ms premium ease on border + box-shadow
 *   · disabled: opacity-60, cursor-not-allowed
 */

export const Input = forwardRef<
  HTMLInputElement,
  InputHTMLAttributes<HTMLInputElement>
>(({ className, type = "text", ...props }, ref) => (
  <input
    ref={ref}
    type={type}
    className={cn(
      "flex h-10 w-full rounded-xl border border-(--color-border) bg-(--color-card) px-3.5 py-2 text-sm",
      "text-(--color-foreground) placeholder:text-(--color-muted-foreground)",
      "shadow-(--shadow-soft) outline-none",
      "transition-[border-color,box-shadow] duration-[250ms] [transition-timing-function:var(--ease-premium)]",
      "hover:border-(--color-slate-300)",
      "focus:border-(--color-brand-500) focus:ring-2 focus:ring-(--color-brand-500)/20",
      "disabled:cursor-not-allowed disabled:opacity-60",
      "file:border-0 file:bg-transparent file:text-sm file:font-medium",
      className,
    )}
    {...props}
  />
));
Input.displayName = "Input";
