import { cn } from "@/lib/utils";
import { useLang, useT, type Lang } from "@/lib/i18n";

/**
 * LanguageSwitcher — small segmented toggle (PT / EN).
 * Same anatomy as ThemeToggle so they line up in the Navbar.
 */

const ORDER: Lang[] = ["en", "pt"];

export function LanguageSwitcher({ className }: { className?: string }) {
  const t = useT();
  const [lang, setLang] = useLang();
  return (
    <div
      role="group"
      aria-label={t.lang.label}
      className={cn(
        "relative inline-flex items-center rounded-full p-0.5",
        "border border-(--color-border) bg-(--color-card) shadow-(--shadow-soft)",
        "transition-[background-color,border-color,box-shadow] duration-[400ms] [transition-timing-function:var(--ease-premium)]",
        className,
      )}
    >
      {ORDER.map((code) => {
        const active = lang === code;
        return (
          <button
            key={code}
            type="button"
            onClick={() => setLang(code)}
            aria-pressed={active}
            className={cn(
              "relative z-10 inline-flex h-6 min-w-6 items-center justify-center rounded-full px-2",
              "font-mono text-[10px] uppercase tracking-[0.12em]",
              "transition-colors duration-[300ms] [transition-timing-function:var(--ease-premium)]",
              active
                ? "text-(--color-foreground)"
                : "text-(--color-muted-foreground) hover:text-(--color-foreground)",
            )}
          >
            {active ? (
              <span
                aria-hidden
                className="absolute inset-0 -z-10 rounded-full bg-(--color-muted)"
              />
            ) : null}
            {t.lang[code]}
          </button>
        );
      })}
    </div>
  );
}
