import type { ReactNode } from "react";
import { cn } from "@/lib/utils";

type Props = {
  title: string;
  description?: string;
  children: ReactNode;
  className?: string;
};

/**
 * SettingsPanel — vertical settings-row layout (label/help on the left, control
 * column on the right). Sits inside a SectionCard or stands alone.
 */
export function SettingsPanel({ title, description, children, className }: Props) {
  return (
    <section className={cn("space-y-5", className)}>
      <header className="space-y-1">
        <h2 className="font-display text-[15px] font-semibold tracking-tight text-(--color-foreground)">
          {title}
        </h2>
        {description ? (
          <p className="text-[13px] text-(--color-muted-foreground)">{description}</p>
        ) : null}
      </header>
      <div className="divide-y divide-(--color-border) rounded-2xl border border-(--color-border) bg-(--color-card)">
        {children}
      </div>
    </section>
  );
}

type RowProps = {
  label: string;
  hint?: string;
  children?: ReactNode;
  className?: string;
};

/**
 * SettingsRow — single row inside a SettingsPanel. Label + hint on the left,
 * control (input, toggle, badge, etc.) on the right.
 */
export function SettingsRow({ label, hint, children, className }: RowProps) {
  return (
    <div
      className={cn(
        "grid gap-3 px-5 py-4 sm:grid-cols-[minmax(0,1fr)_minmax(0,360px)] sm:items-center sm:gap-6",
        className,
      )}
    >
      <div className="min-w-0">
        <p className="text-sm font-medium text-(--color-foreground)">{label}</p>
        {hint ? (
          <p className="mt-0.5 text-[12.5px] text-(--color-muted-foreground)">{hint}</p>
        ) : null}
      </div>
      {children ? <div className="min-w-0">{children}</div> : null}
    </div>
  );
}
