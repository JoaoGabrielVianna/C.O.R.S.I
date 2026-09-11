import { Search } from "lucide-react";
import { cn } from "@/lib/utils";
import { useCommand } from "@/lib/command";
import { useT } from "@/lib/i18n";

/**
 * CommandPaletteTrigger — a search-shaped button that opens the palette.
 * Mirrors Linear/Raycast/Vercel: not a real input, the click target is the
 * trigger and the search happens inside the modal.
 */
export function CommandPaletteTrigger({ className }: { className?: string }) {
  const t = useT();
  const { open } = useCommand();
  return (
    <button
      type="button"
      onClick={open}
      aria-label={t.app.palette.placeholder}
      className={cn(
        "group flex h-9 w-full max-w-md items-center gap-2 rounded-lg border border-(--color-border) bg-(--color-card) px-3 text-left",
        "text-(--color-muted-foreground) shadow-(--shadow-soft) outline-none",
        "transition-[border-color,background-color] duration-[250ms] [transition-timing-function:var(--ease-premium)]",
        "hover:border-(--color-brand-300) hover:bg-(--color-muted)/50",
        "dark:hover:border-(--color-brand-700)",
        "focus-visible:border-(--color-brand-500) focus-visible:ring-2 focus-visible:ring-(--color-brand-500)/20",
        className,
      )}
    >
      <Search aria-hidden className="size-4 shrink-0 text-(--color-muted-foreground)" />
      <span className="flex-1 truncate text-sm">{t.app.search.placeholder}</span>
      <kbd className="hidden items-center gap-0.5 rounded border border-(--color-border) bg-(--color-muted) px-1.5 py-0.5 font-mono text-[10px] text-(--color-muted-foreground) sm:inline-flex">
        {t.app.palette.shortcutHint}
      </kbd>
    </button>
  );
}
