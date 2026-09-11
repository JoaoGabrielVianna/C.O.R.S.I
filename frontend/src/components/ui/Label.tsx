import { forwardRef, type LabelHTMLAttributes } from "react";
import { cn } from "@/lib/utils";

/**
 * Label — ERP-exact form label.
 *
 * Mirror of `erp-lucas/frontend/src/shared/ui/label.tsx`. Always sits above
 * its Input with `space-y-1.5` gap.
 *   · text-sm · font-medium · leading-none · text-(--color-slate-700)
 */
export const Label = forwardRef<HTMLLabelElement, LabelHTMLAttributes<HTMLLabelElement>>(
  ({ className, ...props }, ref) => (
    <label
      ref={ref}
      className={cn(
        "text-sm font-medium leading-none text-(--color-slate-700)",
        "dark:text-(--color-slate-300)",
        className,
      )}
      {...props}
    />
  ),
);
Label.displayName = "Label";
