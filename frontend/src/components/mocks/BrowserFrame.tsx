import { Lock } from "lucide-react";
import { cn } from "@/lib/utils";

type BrowserFrameProps = {
  children: React.ReactNode;
  url?: string;
  className?: string;
};

export function BrowserFrame({
  children,
  url = "app.corsi.dev/workspace",
  className,
}: BrowserFrameProps) {
  return (
    <div
      className={cn(
        "overflow-hidden rounded-2xl border border-(--color-border) bg-(--color-card) shadow-(--shadow-lift)",
        className,
      )}
    >
      <div className="flex items-center gap-3 border-b border-(--color-border) bg-(--color-muted)/60 px-4 py-2.5">
        <div className="flex items-center gap-1.5">
          <span className="size-2.5 rounded-full bg-rose-300/70" />
          <span className="size-2.5 rounded-full bg-amber-300/70" />
          <span className="size-2.5 rounded-full bg-emerald-300/70" />
        </div>
        <div className="flex flex-1 items-center justify-center">
          <div className="inline-flex items-center gap-1.5 rounded-full border border-(--color-border) bg-(--color-card) px-3 py-1 font-mono text-[11px] text-(--color-muted-foreground)">
            <Lock className="size-3 text-(--color-brand-600)" />
            {url}
          </div>
        </div>
      </div>
      <div className="bg-(--color-background)">{children}</div>
    </div>
  );
}
