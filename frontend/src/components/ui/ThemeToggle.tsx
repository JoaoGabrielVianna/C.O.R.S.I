import { Monitor, Moon, Sun } from "lucide-react";
import { cn } from "@/lib/utils";
import { useTheme, type Theme } from "@/lib/theme";
import { useT } from "@/lib/i18n";

/**
 * ThemeToggle — Apple/Linear-style segmented control (Sun · Monitor · Moon).
 * Pill background slides between the three options with a smooth transition.
 * The page-wide background+color transition (400ms) lives in index.css.
 */

const ORDER: Theme[] = ["light", "system", "dark"];
const ICONS: Record<Theme, typeof Sun> = {
  light: Sun,
  system: Monitor,
  dark: Moon,
};

export function ThemeToggle({ className }: { className?: string }) {
  const t = useT();
  const { theme, setTheme } = useTheme();

  return (
    <div
      role="group"
      aria-label={t.theme.label}
      className={cn(
        "relative inline-flex items-center rounded-full p-0.5",
        "border border-(--color-border) bg-(--color-card) shadow-(--shadow-soft)",
        "transition-[background-color,border-color,box-shadow] duration-[400ms] [transition-timing-function:var(--ease-premium)]",
        className,
      )}
    >
      {ORDER.map((mode) => {
        const Icon = ICONS[mode];
        const active = theme === mode;
        return (
          <button
            key={mode}
            type="button"
            onClick={() => setTheme(mode)}
            aria-pressed={active}
            aria-label={t.theme[mode]}
            title={t.theme[mode]}
            className={cn(
              "relative z-10 inline-flex h-6 w-6 items-center justify-center rounded-full",
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
                style={{ transition: "inherit" }}
              />
            ) : null}
            <Icon className="h-3.5 w-3.5" />
          </button>
        );
      })}
    </div>
  );
}
