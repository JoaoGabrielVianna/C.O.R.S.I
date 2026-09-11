import { useEffect, useRef, useState, type KeyboardEvent } from "react";
import { AnimatePresence, motion } from "framer-motion";
import { Check, Filter as FilterIcon, Search, X } from "lucide-react";
import { cn } from "@/lib/utils";
import { Input } from "@/components/ui/Input";
import { useT } from "@/lib/i18n";
import type { JobRadarStore } from "./store";
import { type Filters, type WorkMode } from "./types";

const ease = [0.16, 1, 0.3, 1] as const;

type Props = {
  store: JobRadarStore;
};

/**
 * FilterBar — search + filter popover. Four filters only: stack, remote,
 * salary, country. Active filters render as inline chips. No saved-filters
 * UI surface for v0.0.0 — the store still holds the array for future use.
 */
export function FilterBar({ store }: Props) {
  const t = useT();
  const labels = t.app.modules.jobRadar.filters;
  const { state, setFilters, clearFilters } = store;
  const f = state.filters;

  const chips: ActiveChip[] = [];
  if (f.workMode !== "any") {
    chips.push({
      id: "workMode",
      label: labels.remote.label,
      value: labels.remote[f.workMode],
      onRemove: () => setFilters({ workMode: "any" }),
    });
  }
  if (f.country.trim()) {
    chips.push({
      id: "country",
      label: labels.country.label,
      value: f.country.trim(),
      onRemove: () => setFilters({ country: "" }),
    });
  }
  for (const s of f.stack) {
    chips.push({
      id: `stack-${s}`,
      label: labels.stack.label,
      value: s,
      onRemove: () => setFilters({ stack: f.stack.filter((x) => x !== s) }),
    });
  }
  if (f.salaryMin.trim() || f.salaryMax.trim()) {
    chips.push({
      id: "salary",
      label: labels.salary.label,
      value: `${f.salaryMin || "—"} → ${f.salaryMax || "—"}`,
      onRemove: () => setFilters({ salaryMin: "", salaryMax: "" }),
    });
  }
  const hasActive = chips.length > 0;

  return (
    <div className="flex flex-wrap items-center gap-2">
      <div className="relative min-w-[200px] flex-1 sm:max-w-md">
        <Search aria-hidden className="pointer-events-none absolute left-3 top-1/2 size-3.5 -translate-y-1/2 text-(--color-muted-foreground)" />
        <input
          type="text"
          value={f.query}
          onChange={(e) => setFilters({ query: e.target.value })}
          placeholder={labels.queryPlaceholder}
          aria-label={labels.query}
          className={cn(
            "h-9 w-full rounded-lg border border-(--color-border) bg-(--color-card) pl-9 pr-3 text-sm",
            "text-(--color-foreground) placeholder:text-(--color-muted-foreground)",
            "shadow-(--shadow-soft) outline-none",
            "transition-[border-color,box-shadow] duration-[250ms] [transition-timing-function:var(--ease-premium)]",
            "focus:border-(--color-brand-500) focus:ring-2 focus:ring-(--color-brand-500)/20",
          )}
        />
      </div>

      <FiltersPopover store={store} />

      {hasActive ? (
        <>
          <div className="hidden h-5 w-px bg-(--color-border) sm:block" aria-hidden />
          <div className="flex flex-wrap items-center gap-1">
            {chips.map((chip) => (
              <button
                key={chip.id}
                type="button"
                onClick={chip.onRemove}
                className="inline-flex items-center gap-1 rounded-full border border-(--color-border) bg-(--color-muted) px-2 py-0.5 text-[11px] font-medium text-(--color-foreground) transition-colors hover:border-rose-300 hover:bg-rose-50 hover:text-rose-700 dark:hover:border-rose-700 dark:hover:bg-rose-500/10 dark:hover:text-rose-300"
              >
                <span className="font-mono uppercase tracking-wide text-[9.5px] text-(--color-muted-foreground)">
                  {chip.label}
                </span>
                <span>{chip.value}</span>
                <X className="size-2.5" />
              </button>
            ))}
          </div>
          <button
            type="button"
            onClick={clearFilters}
            className="ml-auto inline-flex items-center gap-1 font-mono text-[10.5px] uppercase tracking-[0.14em] text-(--color-muted-foreground) transition-colors hover:text-(--color-foreground)"
          >
            <X className="size-3" />
            {labels.clear}
          </button>
        </>
      ) : null}
    </div>
  );
}

// ── Filters popover ──────────────────────────────────────────────────────

function FiltersPopover({ store }: { store: JobRadarStore }) {
  const t = useT();
  const labels = t.app.modules.jobRadar.filters;
  const { state, setFilters } = store;
  const f = state.filters;

  const [open, setOpen] = useState(false);
  const [stackDraft, setStackDraft] = useState("");
  const rootRef = useRef<HTMLDivElement | null>(null);

  useClickOutside(rootRef, open, () => setOpen(false));

  const onStackKey = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key !== "Enter" && e.key !== ",") return;
    e.preventDefault();
    const v = stackDraft.trim().replace(/,$/, "");
    if (!v) return;
    if (f.stack.includes(v)) {
      setStackDraft("");
      return;
    }
    setFilters({ stack: [...f.stack, v] });
    setStackDraft("");
  };

  const filterCount = countActive(f);

  return (
    <div ref={rootRef} className="relative">
      <button
        type="button"
        onClick={() => setOpen((s) => !s)}
        aria-haspopup="dialog"
        aria-expanded={open}
        className={cn(
          "inline-flex h-9 items-center gap-1.5 rounded-lg border px-3 text-[12.5px] font-medium",
          "transition-colors duration-150",
          filterCount > 0
            ? "border-(--color-brand-300) bg-(--color-brand-50) text-(--color-brand-700) dark:border-(--color-brand-700) dark:bg-(--color-brand-500)/10 dark:text-(--color-brand-300)"
            : "border-(--color-border) bg-(--color-card) text-(--color-foreground) hover:bg-(--color-muted)",
        )}
      >
        <FilterIcon className="size-3.5" />
        {labels.title}
        {filterCount > 0 ? (
          <span className="rounded-full bg-(--color-brand-500) px-1.5 font-mono text-[10px] text-white">
            {filterCount}
          </span>
        ) : null}
      </button>

      <AnimatePresence>
        {open ? (
          <motion.div
            initial={{ opacity: 0, y: -4, scale: 0.98 }}
            animate={{ opacity: 1, y: 0, scale: 1 }}
            exit={{ opacity: 0, y: -4, scale: 0.98 }}
            transition={{ duration: 0.16, ease }}
            role="dialog"
            className="absolute left-0 top-[calc(100%+6px)] z-40 w-[340px] origin-top-left overflow-hidden rounded-xl border border-(--color-border) bg-(--color-card) shadow-(--shadow-lift)"
          >
            <div className="max-h-[60vh] overflow-y-auto p-4">
              <div className="space-y-4">
                <PopoverField label={labels.stack.label}>
                  <Input
                    value={stackDraft}
                    onChange={(e) => setStackDraft(e.target.value)}
                    onKeyDown={onStackKey}
                    placeholder={labels.stack.placeholder}
                    className="h-9"
                  />
                  {f.stack.length > 0 ? (
                    <div className="mt-1.5 flex flex-wrap gap-1">
                      {f.stack.map((s) => (
                        <button
                          key={s}
                          type="button"
                          onClick={() => setFilters({ stack: f.stack.filter((x) => x !== s) })}
                          className="inline-flex items-center gap-1 rounded-full border border-(--color-border) bg-(--color-muted) px-2 py-0.5 font-mono text-[10px] uppercase tracking-wide text-(--color-foreground) hover:border-rose-300 hover:text-rose-700 dark:hover:border-rose-700 dark:hover:text-rose-300"
                        >
                          {s}
                          <X className="size-2.5" />
                        </button>
                      ))}
                    </div>
                  ) : null}
                </PopoverField>

                <PopoverField label={labels.remote.label}>
                  <Segmented
                    options={[
                      { value: "any",    label: labels.remote.any },
                      { value: "remote", label: labels.remote.remote },
                      { value: "hybrid", label: labels.remote.hybrid },
                      { value: "onsite", label: labels.remote.onsite },
                    ]}
                    value={f.workMode}
                    onChange={(v) => setFilters({ workMode: v as WorkMode })}
                  />
                </PopoverField>

                <PopoverField label={labels.salary.label}>
                  <div className="grid grid-cols-2 gap-2">
                    <Input
                      value={f.salaryMin}
                      onChange={(e) => setFilters({ salaryMin: e.target.value })}
                      placeholder="min"
                      className="h-9 font-mono text-xs"
                    />
                    <Input
                      value={f.salaryMax}
                      onChange={(e) => setFilters({ salaryMax: e.target.value })}
                      placeholder="max"
                      className="h-9 font-mono text-xs"
                    />
                  </div>
                </PopoverField>

                <PopoverField label={labels.country.label}>
                  <Input
                    value={f.country}
                    onChange={(e) => setFilters({ country: e.target.value })}
                    placeholder={labels.country.placeholder}
                    className="h-9"
                  />
                </PopoverField>
              </div>
            </div>
            <footer className="flex items-center justify-between border-t border-(--color-border) px-4 py-2">
              <button
                type="button"
                onClick={() => store.clearFilters()}
                className="inline-flex items-center gap-1 font-mono text-[10.5px] uppercase tracking-[0.14em] text-(--color-muted-foreground) transition-colors hover:text-(--color-foreground)"
              >
                <X className="size-3" />
                {labels.clear}
              </button>
              <button
                type="button"
                onClick={() => setOpen(false)}
                className="inline-flex items-center gap-1 rounded-md bg-(--color-accent) px-2.5 py-1 text-[11.5px] font-medium text-(--color-accent-foreground) shadow-(--shadow-soft) transition-[transform,box-shadow] hover:-translate-y-px"
              >
                <Check className="size-3" /> {labels.done}
              </button>
            </footer>
          </motion.div>
        ) : null}
      </AnimatePresence>
    </div>
  );
}

// ── Helpers ──────────────────────────────────────────────────────────────

function PopoverField({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="space-y-1.5">
      <p className="font-mono text-[10px] uppercase tracking-[0.16em] text-(--color-muted-foreground)">
        {label}
      </p>
      {children}
    </div>
  );
}

type SegmentedOption = { value: string; label: string };
function Segmented({
  options,
  value,
  onChange,
}: {
  options: SegmentedOption[];
  value: string;
  onChange: (next: string) => void;
}) {
  return (
    <div className="flex flex-wrap gap-1">
      {options.map((o) => {
        const active = o.value === value;
        return (
          <button
            key={o.value}
            type="button"
            onClick={() => onChange(o.value)}
            className={cn(
              "rounded-md border px-2 py-1 text-[11.5px] font-medium transition-colors",
              active
                ? "border-(--color-brand-500) bg-(--color-brand-500)/10 text-(--color-brand-700) dark:text-(--color-brand-300)"
                : "border-(--color-border) bg-(--color-card) text-(--color-muted-foreground) hover:border-(--color-border-strong) hover:text-(--color-foreground)",
            )}
          >
            {o.label}
          </button>
        );
      })}
    </div>
  );
}

function useClickOutside(
  ref: React.RefObject<HTMLDivElement | null>,
  open: boolean,
  onClose: () => void,
) {
  useEffect(() => {
    if (!open) return;
    const onMouseDown = (e: MouseEvent) => {
      if (!ref.current?.contains(e.target as Node)) onClose();
    };
    const onKey = (e: KeyboardEvent) => {
      if ((e as unknown as { key: string }).key === "Escape") onClose();
    };
    document.addEventListener("mousedown", onMouseDown);
    document.addEventListener("keydown", onKey as unknown as EventListener);
    return () => {
      document.removeEventListener("mousedown", onMouseDown);
      document.removeEventListener("keydown", onKey as unknown as EventListener);
    };
  }, [open, onClose, ref]);
}

function countActive(f: Filters): number {
  let n = 0;
  if (f.query.trim()) n++;
  if (f.workMode !== "any") n++;
  if (f.country.trim()) n++;
  if (f.stack.length > 0) n += f.stack.length;
  if (f.salaryMin.trim()) n++;
  if (f.salaryMax.trim()) n++;
  return n;
}

type ActiveChip = { id: string; label: string; value: string; onRemove: () => void };
