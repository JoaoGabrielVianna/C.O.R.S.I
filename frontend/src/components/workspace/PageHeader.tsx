import type { ReactNode } from "react";
import { cn } from "@/lib/utils";

type Props = {
  title: string;
  description?: string;
  eyebrow?: string;
  actions?: ReactNode;
  className?: string;
};

/**
 * PageHeader — sits at the top of every page inside the workspace shell.
 * Title + optional description on the left, optional action slot on the right.
 */
export function PageHeader({ title, description, eyebrow, actions, className }: Props) {
  return (
    <header
      className={cn(
        "flex flex-col gap-3 border-b border-(--color-border) pb-6 sm:flex-row sm:items-end sm:justify-between",
        className,
      )}
    >
      <div className="min-w-0">
        {eyebrow ? (
          <p className="font-mono text-[10.5px] uppercase tracking-[0.18em] text-(--color-muted-foreground)">
            {eyebrow}
          </p>
        ) : null}
        <h1 className="mt-1 font-display text-2xl font-semibold tracking-tight text-(--color-foreground) md:text-[28px]">
          {title}
        </h1>
        {description ? (
          <p className="mt-1.5 max-w-2xl text-sm text-(--color-muted-foreground)">
            {description}
          </p>
        ) : null}
      </div>
      {actions ? <div className="flex shrink-0 items-center gap-2">{actions}</div> : null}
    </header>
  );
}
