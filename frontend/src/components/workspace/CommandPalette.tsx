import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { AnimatePresence, motion } from "framer-motion";
import { CornerDownLeft, Search, X } from "lucide-react";
import { cn } from "@/lib/utils";
import { useCommand, type Command } from "@/lib/command";
import { useT } from "@/lib/i18n";
import { matchesQuery, type SearchCategory } from "@/lib/search";

/**
 * CommandPalette — global ⌘K modal.
 *
 * Lifts the React Router stack out of the way — commands `perform()` whatever
 * they were registered to do (navigate, toggle theme, sign out, etc.). The
 * palette itself is a controlled list with arrow-key navigation, Enter to
 * run, Escape to close, click-outside to dismiss.
 */

const ease = [0.16, 1, 0.3, 1] as const;

const CATEGORY_ORDER: readonly SearchCategory[] = [
  "Navigation",
  "Modules",
  "Settings",
  "Actions",
  "Account",
  "Jobs",
  "Companies",
  "Expenses",
  "Notes",
];

function matchCommand(cmd: Command, q: string): boolean {
  return matchesQuery([cmd.title, cmd.subtitle ?? "", ...(cmd.keywords ?? [])], q);
}

export function CommandPalette() {
  const t = useT();
  const { commands, isOpen, close } = useCommand();
  const [query, setQuery] = useState("");
  const [activeIdx, setActiveIdx] = useState(0);
  const inputRef = useRef<HTMLInputElement | null>(null);
  const listRef = useRef<HTMLDivElement | null>(null);

  const filtered = useMemo(() => {
    const q = query.trim();
    const matched = commands.filter((c) => matchCommand(c, q));
    return matched.slice().sort((a, b) => {
      const ai = CATEGORY_ORDER.indexOf(a.category);
      const bi = CATEGORY_ORDER.indexOf(b.category);
      if (ai !== bi) return ai - bi;
      const aw = a.weight ?? 0;
      const bw = b.weight ?? 0;
      if (aw !== bw) return bw - aw;
      return a.title.localeCompare(b.title);
    });
  }, [commands, query]);

  const grouped = useMemo(() => {
    const out: Array<{ category: SearchCategory; items: Command[] }> = [];
    const byCat = new Map<SearchCategory, Command[]>();
    for (const c of filtered) {
      const bucket = byCat.get(c.category) ?? [];
      bucket.push(c);
      byCat.set(c.category, bucket);
    }
    for (const cat of CATEGORY_ORDER) {
      const items = byCat.get(cat);
      if (items && items.length) out.push({ category: cat, items });
    }
    return out;
  }, [filtered]);

  const flat = useMemo(() => grouped.flatMap((g) => g.items), [grouped]);
  const safeActiveIdx = flat.length === 0 ? 0 : Math.min(activeIdx, flat.length - 1);

  const handleClose = useCallback(() => {
    setQuery("");
    setActiveIdx(0);
    close();
  }, [close]);

  const runCommand = useCallback(
    async (cmd: Command) => {
      setQuery("");
      setActiveIdx(0);
      close();
      await cmd.perform();
    },
    [close],
  );

  useEffect(() => {
    if (!isOpen) return;
    const id = window.requestAnimationFrame(() => inputRef.current?.focus());
    return () => window.cancelAnimationFrame(id);
  }, [isOpen]);

  useEffect(() => {
    if (!isOpen) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.preventDefault();
        handleClose();
      } else if (e.key === "ArrowDown") {
        e.preventDefault();
        setActiveIdx((i) => (flat.length ? (i + 1) % flat.length : 0));
      } else if (e.key === "ArrowUp") {
        e.preventDefault();
        setActiveIdx((i) => (flat.length ? (i - 1 + flat.length) % flat.length : 0));
      } else if (e.key === "Enter") {
        const cmd = flat[safeActiveIdx];
        if (!cmd) return;
        e.preventDefault();
        runCommand(cmd);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [isOpen, flat, safeActiveIdx, handleClose, runCommand]);

  useEffect(() => {
    if (!isOpen) return;
    const node = listRef.current?.querySelector<HTMLElement>(
      `[data-cmd-index="${safeActiveIdx}"]`,
    );
    node?.scrollIntoView({ block: "nearest" });
  }, [safeActiveIdx, isOpen]);

  return (
    <AnimatePresence>
      {isOpen ? (
        <div className="fixed inset-0 z-[100] flex items-start justify-center px-4 pt-[12vh] sm:pt-[16vh]">
          <motion.div
            initial={{ opacity: 0 }}
            animate={{ opacity: 1 }}
            exit={{ opacity: 0 }}
            transition={{ duration: 0.15 }}
            onClick={handleClose}
            className="absolute inset-0 bg-black/45 backdrop-blur-sm"
            aria-hidden
          />
          <motion.div
            role="dialog"
            aria-modal="true"
            aria-label={t.app.palette.placeholder}
            initial={{ opacity: 0, y: -12, scale: 0.98 }}
            animate={{ opacity: 1, y: 0, scale: 1 }}
            exit={{ opacity: 0, y: -8, scale: 0.98 }}
            transition={{ duration: 0.18, ease }}
            className={cn(
              "relative w-full max-w-xl overflow-hidden rounded-2xl",
              "border border-(--color-border) bg-(--color-card) shadow-(--shadow-lift)",
            )}
          >
            <header className="flex items-center gap-3 border-b border-(--color-border) px-4">
              <Search aria-hidden className="size-4 shrink-0 text-(--color-muted-foreground)" />
              <input
                ref={inputRef}
                value={query}
                onChange={(e) => {
                  setQuery(e.target.value);
                  setActiveIdx(0);
                }}
                placeholder={t.app.palette.placeholder}
                aria-label={t.app.palette.placeholder}
                className="h-12 flex-1 bg-transparent text-[14px] text-(--color-foreground) outline-none placeholder:text-(--color-muted-foreground)"
              />
              <button
                type="button"
                onClick={handleClose}
                aria-label={t.app.palette.footer.close}
                className="flex size-7 items-center justify-center rounded-md text-(--color-muted-foreground) transition-colors hover:bg-(--color-muted) hover:text-(--color-foreground)"
              >
                <X className="size-3.5" />
              </button>
            </header>

            <div ref={listRef} className="max-h-[55vh] overflow-y-auto py-2">
              {flat.length === 0 ? (
                <div className="px-5 py-10 text-center">
                  <p className="text-sm font-medium text-(--color-foreground)">
                    {t.app.palette.empty.title}
                  </p>
                  <p className="mt-1 text-[12.5px] text-(--color-muted-foreground)">
                    {t.app.palette.empty.body}
                  </p>
                </div>
              ) : (
                grouped.map((group) => (
                  <div key={group.category} className="px-2 pb-1">
                    <p className="px-3 py-1.5 font-mono text-[10px] uppercase tracking-[0.16em] text-(--color-muted-foreground)">
                      {t.app.palette.categories[group.category]}
                    </p>
                    <ul>
                      {group.items.map((cmd) => {
                        const idx = flat.indexOf(cmd);
                        const active = idx === safeActiveIdx;
                        const Icon = cmd.icon;
                        return (
                          <li key={cmd.id}>
                            <button
                              type="button"
                              data-cmd-index={idx}
                              onMouseEnter={() => setActiveIdx(idx)}
                              onClick={() => runCommand(cmd)}
                              className={cn(
                                "flex w-full items-center gap-3 rounded-lg px-3 py-2 text-left",
                                "transition-colors duration-150",
                                active
                                  ? "bg-(--color-muted) text-(--color-foreground)"
                                  : "text-(--color-foreground)/85 hover:bg-(--color-muted)/60",
                              )}
                            >
                              {Icon ? (
                                <span
                                  className={cn(
                                    "flex size-7 items-center justify-center rounded-md border border-(--color-border) bg-(--color-card)",
                                    active
                                      ? "border-(--color-brand-300) text-(--color-brand-600) dark:border-(--color-brand-700) dark:text-(--color-brand-400)"
                                      : "text-(--color-muted-foreground)",
                                  )}
                                >
                                  <Icon className="size-3.5" />
                                </span>
                              ) : null}
                              <span className="min-w-0 flex-1">
                                <span className="block truncate text-[13.5px] font-medium">
                                  {cmd.title}
                                </span>
                                {cmd.subtitle ? (
                                  <span className="block truncate text-[12px] text-(--color-muted-foreground)">
                                    {cmd.subtitle}
                                  </span>
                                ) : null}
                              </span>
                              {cmd.shortcut ? (
                                <kbd className="rounded border border-(--color-border) bg-(--color-muted) px-1.5 py-0.5 font-mono text-[10px] text-(--color-muted-foreground)">
                                  {cmd.shortcut}
                                </kbd>
                              ) : null}
                              {active ? (
                                <CornerDownLeft className="size-3.5 text-(--color-muted-foreground)" />
                              ) : null}
                            </button>
                          </li>
                        );
                      })}
                    </ul>
                  </div>
                ))
              )}
            </div>

            <footer className="flex items-center justify-between gap-4 border-t border-(--color-border) px-4 py-2 font-mono text-[10.5px] text-(--color-muted-foreground)">
              <span className="inline-flex items-center gap-3">
                <Kbd label="↑↓" /> {t.app.palette.footer.navigate}
                <Kbd label="↵" />  {t.app.palette.footer.execute}
              </span>
              <span className="inline-flex items-center gap-1.5">
                <Kbd label="esc" /> {t.app.palette.footer.close}
              </span>
            </footer>
          </motion.div>
        </div>
      ) : null}
    </AnimatePresence>
  );
}

function Kbd({ label }: { label: string }) {
  return (
    <kbd className="inline-flex h-4 min-w-4 items-center justify-center rounded border border-(--color-border) bg-(--color-muted) px-1 font-mono text-[9.5px] uppercase tracking-wide text-(--color-muted-foreground)">
      {label}
    </kbd>
  );
}
