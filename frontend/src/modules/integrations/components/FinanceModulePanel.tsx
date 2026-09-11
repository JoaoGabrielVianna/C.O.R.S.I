import { ArrowRight, ArrowRightLeft, MinusCircle, PlusCircle, Repeat } from "lucide-react";
import { useT } from "@/lib/i18n";

type Kind = "expense" | "income" | "transfer" | "plan";

const KIND_ICON: Record<Kind, typeof MinusCircle> = {
  expense: MinusCircle,
  income: PlusCircle,
  transfer: ArrowRightLeft,
  plan: Repeat,
};

const KIND_TINT: Record<Kind, string> = {
  expense:  "text-rose-300",
  income:   "text-emerald-300",
  transfer: "text-sky-300",
  plan:     "text-violet-300",
};

export function FinanceModulePanel() {
  const t = useT();
  const f = t.app.settings.integrations.telegram.finance;
  const kinds: Kind[] = ["expense", "income", "transfer", "plan"];

  return (
    <section className="space-y-4">
      <header className="space-y-1">
        <p className="font-mono text-[10.5px] uppercase tracking-[0.16em] text-(--color-muted-foreground)">
          {f.heading}
        </p>
        <p className="text-[13px] text-(--color-muted-foreground)">{f.description}</p>
      </header>

      <ul className="divide-y divide-(--color-border) overflow-hidden rounded-xl border border-(--color-border) bg-(--color-card)">
        {kinds.map((k) => {
          const Icon = KIND_ICON[k];
          return (
            <li
              key={k}
              className="grid grid-cols-1 gap-3 px-4 py-3 sm:grid-cols-[120px_minmax(0,1fr)_auto_minmax(0,1fr)] sm:items-center sm:gap-4"
            >
              <span className="inline-flex items-center gap-2">
                <Icon className={`size-4 ${KIND_TINT[k]}`} aria-hidden />
                <span className="text-[12.5px] font-medium text-(--color-foreground)">
                  {f.kinds[k]}
                </span>
              </span>
              <code className="rounded-md border border-(--color-border) bg-(--color-background)/40 px-2 py-1 font-mono text-[12px] text-(--color-foreground)">
                {f.examples[k]}
              </code>
              <ArrowRight className="hidden size-3.5 text-(--color-muted-foreground) sm:block" aria-hidden />
              <span className="text-[12.5px] text-(--color-muted-foreground)">
                {f.previews[k]}
              </span>
            </li>
          );
        })}
      </ul>

      <p className="text-[12px] text-(--color-muted-foreground)">{f.unknownNote}</p>
    </section>
  );
}
