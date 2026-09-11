import { Bot, FileSpreadsheet, MessageCircle, PenLine, Plug } from "lucide-react";
import { useT } from "@/lib/i18n";

/**
 * SourcesPanel — read-only preview of future ingestion paths.
 *
 *   manual   · today
 *   whatsapp · planned (AI parser on top of a connected number)
 *   import   · planned (CSV / OFX statements)
 *   ai       · planned (agent-created transactions from chat or other sources)
 *
 * No wiring. Just architecture-aware placeholders so the user (and any future
 * collaborator) sees where the data plug points are.
 */
export function SourcesPanel() {
  const t = useT();
  const labels = t.app.modules.finance.sources;

  const items = [
    { icon: PenLine,         label: labels.manual.label,   hint: labels.manual.hint,   status: "live"     as const },
    { icon: MessageCircle,   label: labels.whatsapp.label, hint: labels.whatsapp.hint, status: "planned"  as const },
    { icon: Bot,             label: labels.ai.label,       hint: labels.ai.hint,       status: "planned"  as const },
    { icon: FileSpreadsheet, label: labels.import.label,   hint: labels.import.hint,   status: "planned"  as const },
  ];

  return (
    <section className="rounded-2xl border border-(--color-border) bg-(--color-card) shadow-(--shadow-soft)">
      <header className="flex items-start justify-between gap-3 border-b border-(--color-border) px-5 py-3">
        <div>
          <p className="inline-flex items-center gap-1.5 font-mono text-[10.5px] uppercase tracking-[0.16em] text-(--color-foreground)">
            <Plug className="size-3 text-(--color-brand-600) dark:text-(--color-brand-400)" />
            {labels.title}
          </p>
          <p className="mt-0.5 text-[12px] text-(--color-muted-foreground)">{labels.subtitle}</p>
        </div>
      </header>
      <div className="grid gap-2 p-5 grid-cols-[repeat(auto-fit,minmax(220px,1fr))]">
        {items.map((entry) => {
          const Icon = entry.icon;
          return (
            <div
              key={entry.label}
              className="flex items-start gap-2.5 rounded-xl border border-(--color-border) bg-(--color-background)/40 p-3"
            >
              <span className="mt-0.5 flex size-7 shrink-0 items-center justify-center rounded-md border border-(--color-border) bg-(--color-card) text-(--color-muted-foreground)">
                <Icon className="size-3.5" />
              </span>
              <div className="min-w-0 flex-1">
                <div className="flex items-center gap-1.5">
                  <p className="truncate text-[12.5px] font-medium text-(--color-foreground)">{entry.label}</p>
                  <span
                    className={
                      entry.status === "live"
                        ? "inline-flex items-center gap-1 rounded-full border border-emerald-200 bg-emerald-50 px-1.5 py-px font-mono text-[9.5px] uppercase tracking-wide text-emerald-700 dark:border-emerald-700 dark:bg-emerald-500/10 dark:text-emerald-300"
                        : "inline-flex items-center gap-1 rounded-full border border-(--color-border) bg-(--color-muted) px-1.5 py-px font-mono text-[9.5px] uppercase tracking-wide text-(--color-muted-foreground)"
                    }
                  >
                    {entry.status === "live" ? labels.status.live : labels.status.planned}
                  </span>
                </div>
                <p className="mt-0.5 text-[11.5px] leading-snug text-(--color-muted-foreground)">{entry.hint}</p>
              </div>
            </div>
          );
        })}
      </div>
      <footer className="border-t border-(--color-border) px-5 py-2.5">
        <p className="font-mono text-[10px] text-(--color-muted-foreground)">{labels.note}</p>
      </footer>
    </section>
  );
}
