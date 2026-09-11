import { forwardRef, type ButtonHTMLAttributes } from "react";
import { Slot } from "@radix-ui/react-slot";
import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "@/lib/utils";

const buttonVariants = cva(
  [
    "inline-flex items-center justify-center gap-2 whitespace-nowrap rounded-xl",
    "font-medium select-none outline-none",
    "transition-[transform,box-shadow,background-color,color] duration-[250ms]",
    "[transition-timing-function:var(--ease-premium)]",
    "focus-visible:ring-2 focus-visible:ring-(--color-ring)/50 focus-visible:ring-offset-2 focus-visible:ring-offset-(--color-background)",
    "disabled:pointer-events-none disabled:opacity-50",
    "[&_svg]:size-4 [&_svg]:shrink-0",
  ],
  {
    variants: {
      variant: {
        primary:
          "shine-on-hover bg-(--color-accent) text-(--color-accent-foreground) shadow-(--shadow-card) hover:bg-(--color-brand-600) hover:shadow-(--shadow-glow) hover:-translate-y-px active:translate-y-0",
        solid:
          "bg-(--color-primary) text-(--color-primary-foreground) shadow-(--shadow-card) hover:bg-(--color-slate-900) hover:-translate-y-px",
        outline:
          "border border-(--color-border) bg-(--color-card) text-(--color-foreground) hover:bg-(--color-muted) hover:-translate-y-px",
        ghost:
          "text-(--color-foreground) hover:bg-(--color-muted)",
        link:
          "text-(--color-brand-600) underline-offset-4 hover:underline",
        destructive:
          "bg-(--color-destructive) text-(--color-destructive-foreground) shadow-(--shadow-card) hover:brightness-95 hover:-translate-y-px",
        subtle:
          "bg-(--color-brand-50) text-(--color-brand-700) hover:bg-(--color-brand-100)",
      },
      size: {
        sm: "h-8 px-3 text-sm",
        md: "h-10 px-4 text-sm",
        lg: "h-12 px-6 text-base",
        icon: "size-10 p-0",
      },
    },
    defaultVariants: { variant: "primary", size: "md" },
  },
);

export interface ButtonProps
  extends ButtonHTMLAttributes<HTMLButtonElement>,
    VariantProps<typeof buttonVariants> {
  asChild?: boolean;
}

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(
  ({ className, variant, size, asChild = false, ...props }, ref) => {
    const Comp = asChild ? Slot : "button";
    return (
      <Comp
        ref={ref}
        className={cn(buttonVariants({ variant, size }), className)}
        {...props}
      />
    );
  },
);
Button.displayName = "Button";

// eslint-disable-next-line react-refresh/only-export-components
export { buttonVariants };
