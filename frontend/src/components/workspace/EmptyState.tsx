import type { ComponentType, ReactNode, SVGProps } from "react";
import { cn } from "@/lib/utils";

type Props = {
  icon?: ComponentType<SVGProps<SVGSVGElement>>;
  title: string;
  description?: string;
  action?: ReactNode;
  className?: string;
};

/**
 * EmptyState — honest day-zero state. No fake fixtures.
 * Bordered surface so it reads as a real placeholder, not absence.
 */
export function EmptyState({ icon: Icon, title, description, action, className }: Props) {
  return (
    <div
      className={cn(
        "flex flex-col items-center justify-center gap-3 rounded-2xl border border-dashed border-(--color-border) bg-(--color-card)/40 px-6 py-12 text-center",
        className,
      )}
    >
      {Icon ? (
        <span className="flex size-10 items-center justify-center rounded-full border border-(--color-border) bg-(--color-card) text-(--color-muted-foreground)">
          <Icon className="size-4" />
        </span>
      ) : null}
      <div className="space-y-1">
        <p className="text-sm font-medium text-(--color-foreground)">{title}</p>
        {description ? (
          <p className="mx-auto max-w-sm text-[13px] text-(--color-muted-foreground)">
            {description}
          </p>
        ) : null}
      </div>
      {action ? <div className="pt-1">{action}</div> : null}
    </div>
  );
}
