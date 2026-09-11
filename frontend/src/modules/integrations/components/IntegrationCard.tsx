import type { ComponentType, ReactNode, SVGProps } from "react";
import { cn } from "@/lib/utils";

type Props = {
  icon: ComponentType<SVGProps<SVGSVGElement>>;
  iconTint?: "brand" | "muted";
  title: string;
  tagline: string;
  status?: ReactNode;
  actions?: ReactNode;
  children?: ReactNode;
  muted?: boolean;
  className?: string;
};

export function IntegrationCard({
  icon: Icon,
  iconTint = "brand",
  title,
  tagline,
  status,
  actions,
  children,
  muted = false,
  className,
}: Props) {
  return (
    <section
      className={cn(
        "rounded-2xl border border-(--color-border) bg-(--color-card) shadow-(--shadow-soft)",
        muted && "opacity-80",
        className,
      )}
    >
      {/* Below `sm` the status pill drops to its own line instead of
          competing with the title for a 390px row — squeezed side by side,
          a two-word tagline wrapped into six lines beside a pill. */}
      <header className="flex flex-col gap-3 border-b border-(--color-border) px-5 py-4 sm:flex-row sm:items-start sm:justify-between sm:gap-4">
        <div className="flex min-w-0 items-start gap-3">
          <span
            className={cn(
              "flex size-10 shrink-0 items-center justify-center rounded-xl border",
              iconTint === "brand"
                ? "border-(--color-brand-500)/30 bg-(--color-brand-500)/10 text-(--color-brand-500)"
                : "border-(--color-border) bg-(--color-muted) text-(--color-muted-foreground)",
            )}
          >
            <Icon className="size-5" />
          </span>
          <div className="min-w-0">
            <h3 className="font-display text-[15px] font-semibold tracking-tight text-(--color-foreground)">
              {title}
            </h3>
            <p className="mt-0.5 text-[13px] text-(--color-muted-foreground)">{tagline}</p>
          </div>
        </div>
        {status ? <div className="shrink-0 pl-13 sm:pl-0">{status}</div> : null}
      </header>
      {children ? <div className="px-5 py-4">{children}</div> : null}
      {actions ? (
        <footer className="flex flex-wrap items-center justify-end gap-2 border-t border-(--color-border) px-5 py-3">
          {actions}
        </footer>
      ) : null}
    </section>
  );
}

export function IntegrationStatusPill({
  tone,
  children,
}: {
  tone: "active" | "muted" | "warning";
  children: ReactNode;
}) {
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1.5 rounded-full border px-2.5 py-1 font-mono text-[10.5px] uppercase tracking-[0.14em]",
        tone === "active" &&
          "border-(--color-brand-500)/30 bg-(--color-brand-500)/10 text-(--color-brand-500)",
        tone === "muted" &&
          "border-(--color-border) bg-(--color-muted) text-(--color-muted-foreground)",
        tone === "warning" &&
          "border-amber-400/30 bg-amber-400/10 text-amber-300",
      )}
    >
      <span
        aria-hidden
        className={cn(
          "size-1.5 rounded-full",
          tone === "active" && "bg-(--color-brand-500)",
          tone === "muted" && "bg-(--color-muted-foreground)/60",
          tone === "warning" && "bg-amber-400",
        )}
      />
      {children}
    </span>
  );
}
