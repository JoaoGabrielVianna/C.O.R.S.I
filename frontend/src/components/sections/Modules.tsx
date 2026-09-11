import { useState } from "react";
import { AnimatePresence, motion } from "framer-motion";
import {
  Brain,
  CircleCheck,
  CircleDashed,
  DollarSign,
  LoaderCircle,
  Radar,
  Sparkles,
  type LucideIcon,
} from "lucide-react";
import { cn } from "@/lib/utils";
import { useT } from "@/lib/i18n";

const ease = [0.16, 1, 0.3, 1] as const;

type ModuleKey = "applicationTracker" | "finance" | "marketIntel" | "contentEngine";
type ModuleStatus = "building" | "planned";

type ModuleDef = {
  key: ModuleKey;
  icon: LucideIcon;
  status: ModuleStatus;
  version?: string;
};

const order: ModuleDef[] = [
  { key: "applicationTracker", icon: Radar, status: "building", version: "v0.0.0" },
  { key: "finance", icon: DollarSign, status: "building", version: "v0.0.0" },
  { key: "marketIntel", icon: Brain, status: "planned" },
  { key: "contentEngine", icon: Sparkles, status: "planned" },
];

const statusStyle: Record<ModuleStatus, string> = {
  building:
    "border-(--color-brand-300) bg-(--color-brand-50) text-(--color-brand-700) dark:bg-(--color-brand-500)/15 dark:text-(--color-brand-300)",
  planned:
    "border-(--color-border) bg-(--color-muted) text-(--color-muted-foreground)",
};

const statusDot: Record<ModuleStatus, string> = {
  building: "bg-(--color-brand-500)",
  planned: "bg-(--color-muted-foreground)",
};

export function Modules() {
  const t = useT();
  const [selectedKey, setSelectedKey] = useState<ModuleKey>("applicationTracker");
  const selected = order.find((m) => m.key === selectedKey) ?? order[0];
  const detail = t.modules.items[selected.key];

  return (
    <section id="modules" className="relative scroll-mt-24 py-24 md:py-28">
      <div className="container-page">
        <div className="mx-auto max-w-2xl text-center">
          <span className="eyebrow">{t.modules.eyebrow}</span>
          <h2 className="mt-4 font-display text-4xl font-bold tracking-tight text-(--color-foreground) md:text-5xl">
            {t.modules.title}
          </h2>
          <p className="mt-5 text-lg leading-relaxed text-(--color-muted-foreground)">
            {t.modules.body}
          </p>
        </div>

        <div className="mt-14 grid items-start gap-6 lg:grid-cols-[300px_1fr] lg:gap-10">
          {/* List */}
          <ul className="flex flex-col gap-1.5">
            {order.map((m) => {
              const data = t.modules.items[m.key];
              const Icon = m.icon;
              const isActive = m.key === selectedKey;
              return (
                <li key={m.key}>
                  <button
                    type="button"
                    onClick={() => setSelectedKey(m.key)}
                    aria-pressed={isActive}
                    className={cn(
                      "group flex w-full items-center gap-3 rounded-xl border px-3.5 py-3 text-left",
                      "transition-[background-color,border-color,transform,box-shadow]",
                      "duration-[250ms] [transition-timing-function:var(--ease-premium)]",
                      isActive
                        ? "border-(--color-border-strong) bg-(--color-card) shadow-(--shadow-soft)"
                        : "border-transparent bg-transparent hover:bg-(--color-muted) hover:border-(--color-border)",
                    )}
                  >
                    <span
                      className={cn(
                        "flex size-8 shrink-0 items-center justify-center rounded-lg border",
                        "transition-colors duration-[250ms]",
                        isActive
                          ? "border-(--color-brand-300) bg-(--color-brand-50) text-(--color-brand-700) dark:bg-(--color-brand-500)/15 dark:text-(--color-brand-300)"
                          : "border-(--color-border) bg-(--color-card) text-(--color-muted-foreground)",
                      )}
                    >
                      <Icon className="size-4" />
                    </span>

                    <div className="min-w-0 flex-1">
                      <div className="flex items-center gap-2">
                        <span className="truncate text-[14px] font-semibold tracking-tight text-(--color-foreground)">
                          {data.name}
                        </span>
                        <span
                          className={cn(
                            "ml-auto inline-flex items-center gap-1 rounded-full border px-1.5 py-0.5 text-[10px] font-medium",
                            statusStyle[m.status],
                          )}
                        >
                          <span className={cn("size-1 rounded-full", statusDot[m.status])} />
                          {t.modules.statusLabels[m.status]}
                          {m.version ? ` · ${m.version}` : ""}
                        </span>
                      </div>
                    </div>
                  </button>
                </li>
              );
            })}
          </ul>

          {/* Detail */}
          <div className="surface-card rounded-2xl p-7 md:p-9">
            <AnimatePresence mode="wait">
              <motion.div
                key={selectedKey}
                initial={{ opacity: 0, y: 10 }}
                animate={{ opacity: 1, y: 0 }}
                exit={{ opacity: 0, y: -6 }}
                transition={{ duration: 0.35, ease }}
              >
                <div className="flex items-center justify-between gap-4">
                  <div className="flex items-center gap-3">
                    <span
                      className={cn(
                        "flex size-10 items-center justify-center rounded-xl border",
                        "border-(--color-brand-300) bg-(--color-brand-50) text-(--color-brand-700)",
                        "dark:bg-(--color-brand-500)/15 dark:text-(--color-brand-300)",
                      )}
                    >
                      <selected.icon className="size-5" />
                    </span>
                    <h3 className="font-display text-2xl font-bold tracking-tight text-(--color-foreground) md:text-[28px]">
                      {detail.name}
                    </h3>
                  </div>
                  <span
                    className={cn(
                      "inline-flex items-center gap-1.5 rounded-full border px-2.5 py-1 text-[11px] font-medium",
                      statusStyle[selected.status],
                    )}
                  >
                    {selected.status === "building" ? (
                      <LoaderCircle className="size-3 animate-spin" />
                    ) : (
                      <CircleDashed className="size-3" />
                    )}
                    {t.modules.statusLabels[selected.status]}
                    {selected.version ? ` · ${selected.version}` : ""}
                  </span>
                </div>

                <p className="mt-5 text-[16px] leading-relaxed text-(--color-muted-foreground)">
                  {detail.purpose}
                </p>

                <div className="mt-7 grid gap-7 md:grid-cols-2">
                  <div>
                    <p className="text-[11px] font-semibold uppercase tracking-[0.16em] text-(--color-muted-foreground)">
                      {t.modules.sections.evolution}
                    </p>
                    <p className="mt-2.5 text-[14.5px] leading-relaxed text-(--color-foreground)/90">
                      {detail.evolution}
                    </p>
                  </div>

                  <div>
                    <p className="text-[11px] font-semibold uppercase tracking-[0.16em] text-(--color-muted-foreground)">
                      {t.modules.sections.recent}
                    </p>
                    <ul className="mt-3 space-y-2">
                      {detail.recent.length === 0 ? (
                        <li className="text-[13.5px] text-(--color-muted-foreground)">
                          <span className="inline-flex items-center gap-1.5 rounded-full border border-(--color-border) bg-(--color-muted) px-2 py-0.5 text-[11px]">
                            <CircleDashed className="size-3" />
                            {t.modules.statusLabels.planned}
                          </span>
                        </li>
                      ) : (
                        detail.recent.map((r) => (
                          <li
                            key={r}
                            className="flex items-start gap-2.5 text-[14px] leading-relaxed text-(--color-foreground)/85"
                          >
                            <CircleCheck className="mt-0.5 size-4 shrink-0 text-(--color-brand-600)" />
                            {r}
                          </li>
                        ))
                      )}
                    </ul>
                  </div>
                </div>

                <div className="mt-7">
                  <p className="text-[11px] font-semibold uppercase tracking-[0.16em] text-(--color-muted-foreground)">
                    {t.modules.sections.next}
                  </p>
                  <ul className="mt-3 grid gap-2 md:grid-cols-2">
                    {detail.next.map((n) => (
                      <li
                        key={n}
                        className="flex items-start gap-2.5 rounded-xl border border-(--color-border) bg-(--color-background) px-3.5 py-2.5 text-[13.5px] leading-relaxed text-(--color-foreground)/90"
                      >
                        <span className="mt-1.5 size-1.5 shrink-0 rounded-full bg-(--color-brand-500)" />
                        {n}
                      </li>
                    ))}
                  </ul>
                </div>
              </motion.div>
            </AnimatePresence>
          </div>
        </div>
      </div>
    </section>
  );
}
