import type { ReactNode } from "react";
import { cn } from "@/lib/utils";

type Props = {
  title?: string;
  description?: string;
  actions?: ReactNode;
  children?: ReactNode;
  className?: string;
  bodyClassName?: string;
};

/**
 * SectionCard — bordered surface used to group related content inside a page.
 * Title + optional description in the header strip, content slot below.
 */
export function SectionCard({
  title,
  description,
  actions,
  children,
  className,
  bodyClassName,
}: Props) {
  return (
    <section
      className={cn(
        "rounded-2xl border border-(--color-border) bg-(--color-card) shadow-(--shadow-soft)",
        className,
      )}
    >
      {title || actions ? (
        <header className="flex items-start justify-between gap-4 border-b border-(--color-border) px-5 py-4">
          <div className="min-w-0">
            {title ? (
              <h2 className="font-display text-[15px] font-semibold tracking-tight text-(--color-foreground)">
                {title}
              </h2>
            ) : null}
            {description ? (
              <p className="mt-0.5 text-[13px] text-(--color-muted-foreground)">
                {description}
              </p>
            ) : null}
          </div>
          {actions ? <div className="flex shrink-0 items-center gap-2">{actions}</div> : null}
        </header>
      ) : null}
      <div className={cn("p-5", bodyClassName)}>{children}</div>
    </section>
  );
}
