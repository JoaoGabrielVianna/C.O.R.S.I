import { useEffect, useRef, useState } from "react";
import { cn } from "@/lib/utils";
import { useAuth } from "@/lib/auth";
import { useT } from "@/lib/i18n";
import { AccountMenuPanel } from "./AccountMenu";

/**
 * UserMenu — avatar trigger in the header, opening the account menu.
 * Closes on outside click and Escape. No popover lib — small enough to roll.
 *
 * The menu's contents are NOT defined here: they live in `AccountMenu`, so
 * this and the sidebar's user block cannot drift apart the way they had.
 * This component owns the trigger, the open state and the placement.
 *
 * The trigger is the only account entry point that survives every
 * breakpoint — below `lg` the sidebar is an off-canvas drawer, so its user
 * block costs a hamburger tap first. That is why this one carries an
 * `aria-label` instead of leaning on the initials, which name nothing.
 */
export function UserMenu() {
  const t = useT();
  const { user } = useAuth();
  const [open, setOpen] = useState(false);
  const rootRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    if (!open) return;
    const onClick = (e: MouseEvent) => {
      if (!rootRef.current?.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
    };
    document.addEventListener("mousedown", onClick);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onClick);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  const initials = (user?.name ?? "?")
    .split(" ")
    .map((p) => p[0])
    .slice(0, 2)
    .join("")
    .toUpperCase();

  return (
    <div ref={rootRef} className="relative">
      <button
        type="button"
        onClick={() => setOpen((s) => !s)}
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label={t.app.user.account}
        className={cn(
          "flex size-9 items-center justify-center rounded-full border border-(--color-border) bg-(--color-card)",
          "font-mono text-[11px] font-semibold uppercase tracking-wider text-(--color-foreground)",
          "shadow-(--shadow-soft) transition-[border-color,background-color] duration-[250ms]",
          "hover:border-(--color-brand-300) hover:bg-(--color-muted)",
          "dark:hover:border-(--color-brand-700)",
        )}
      >
        <span aria-hidden>{initials}</span>
      </button>

      {open ? (
        <AccountMenuPanel
          onNavigate={() => setOpen(false)}
          className="absolute right-0 top-[calc(100%+8px)] w-64 origin-top-right"
        />
      ) : null}
    </div>
  );
}
