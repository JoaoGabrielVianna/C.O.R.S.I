import { useEffect, useRef, useState } from "react";
import { AnimatePresence, motion } from "framer-motion";
import { Bell, BellOff, Check, CheckCheck, Trash2 } from "lucide-react";
import { cn } from "@/lib/utils";
import { useNotifications, type NotificationLevel } from "@/lib/notifications";
import { useT } from "@/lib/i18n";

/**
 * NotificationCenter — bell trigger + popover feed.
 * Uses the workspace NotificationsProvider. Empty state honors the day-zero
 * rule: no fabricated activity, just an honest "nothing to report" surface.
 */

const ease = [0.16, 1, 0.3, 1] as const;

const LEVEL_DOT: Record<NotificationLevel, string> = {
  info: "bg-sky-400",
  success: "bg-emerald-400",
  warning: "bg-amber-400",
};

function formatRelative(
  ms: number,
  labels: { now: string; minutes: string; hours: string; days: string },
): string {
  const seconds = Math.max(0, Math.floor(ms / 1000));
  if (seconds < 60) return labels.now;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}${labels.minutes}`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}${labels.hours}`;
  const days = Math.floor(hours / 24);
  return `${days}${labels.days}`;
}

export function NotificationCenter() {
  const t = useT();
  const { notifications, unreadCount, markRead, markAllRead, clear } =
    useNotifications();
  const [open, setOpen] = useState(false);
  const [now, setNow] = useState<number>(() => Date.now());
  const rootRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    const id = window.setInterval(() => setNow(Date.now()), 30_000);
    return () => window.clearInterval(id);
  }, []);

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

  return (
    <div ref={rootRef} className="relative">
      <button
        type="button"
        onClick={() => setOpen((s) => !s)}
        aria-label={t.app.header.notifications}
        aria-haspopup="dialog"
        aria-expanded={open}
        className={cn(
          "relative flex size-9 items-center justify-center rounded-lg text-(--color-muted-foreground)",
          "transition-colors duration-150 hover:bg-(--color-muted) hover:text-(--color-foreground)",
        )}
      >
        <Bell className="size-4" />
        {unreadCount > 0 ? (
          <span
            aria-hidden
            className="absolute right-1.5 top-1.5 inline-flex h-3.5 min-w-3.5 items-center justify-center rounded-full bg-(--color-brand-500) px-1 font-mono text-[9px] font-bold leading-none text-white shadow-(--shadow-soft)"
          >
            {unreadCount > 9 ? "9+" : unreadCount}
          </span>
        ) : null}
      </button>

      <AnimatePresence>
        {open ? (
          <motion.div
            role="dialog"
            aria-label={t.app.notifications.title}
            initial={{ opacity: 0, y: -6, scale: 0.98 }}
            animate={{ opacity: 1, y: 0, scale: 1 }}
            exit={{ opacity: 0, y: -4, scale: 0.98 }}
            transition={{ duration: 0.18, ease }}
            className={cn(
              "absolute right-0 top-[calc(100%+8px)] z-50 w-[340px] origin-top-right overflow-hidden rounded-xl",
              "border border-(--color-border) bg-(--color-card) shadow-(--shadow-lift)",
            )}
          >
            <header className="flex items-center justify-between border-b border-(--color-border) px-4 py-2.5">
              <p className="text-[13px] font-semibold text-(--color-foreground)">
                {t.app.notifications.title}
                {unreadCount > 0 ? (
                  <span className="ml-2 font-mono text-[10px] uppercase tracking-[0.14em] text-(--color-muted-foreground)">
                    {unreadCount} new
                  </span>
                ) : null}
              </p>
              <div className="flex items-center gap-1">
                {notifications.some((n) => !n.read) ? (
                  <button
                    type="button"
                    onClick={markAllRead}
                    title={t.app.notifications.markAllRead}
                    aria-label={t.app.notifications.markAllRead}
                    className="flex size-7 items-center justify-center rounded-md text-(--color-muted-foreground) transition-colors hover:bg-(--color-muted) hover:text-(--color-foreground)"
                  >
                    <CheckCheck className="size-3.5" />
                  </button>
                ) : null}
                {notifications.length > 0 ? (
                  <button
                    type="button"
                    onClick={clear}
                    title={t.app.notifications.clearAll}
                    aria-label={t.app.notifications.clearAll}
                    className="flex size-7 items-center justify-center rounded-md text-(--color-muted-foreground) transition-colors hover:bg-(--color-muted) hover:text-(--color-foreground)"
                  >
                    <Trash2 className="size-3.5" />
                  </button>
                ) : null}
              </div>
            </header>

            <div className="max-h-[60vh] overflow-y-auto">
              {notifications.length === 0 ? (
                <div className="flex flex-col items-center gap-2.5 px-5 py-10 text-center">
                  <span className="flex size-9 items-center justify-center rounded-full border border-(--color-border) bg-(--color-card) text-(--color-muted-foreground)">
                    <BellOff className="size-4" />
                  </span>
                  <p className="text-sm font-medium text-(--color-foreground)">
                    {t.app.notifications.empty.title}
                  </p>
                  <p className="max-w-xs text-[12.5px] text-(--color-muted-foreground)">
                    {t.app.notifications.empty.body}
                  </p>
                </div>
              ) : (
                <ul className="divide-y divide-(--color-border)">
                  {notifications.map((n) => (
                    <li
                      key={n.id}
                      className={cn(
                        "group relative px-4 py-3 transition-colors",
                        !n.read ? "bg-(--color-brand-50)/30 dark:bg-(--color-brand-500)/[0.04]" : "",
                      )}
                    >
                      <div className="flex items-start gap-2.5">
                        <span
                          aria-hidden
                          className={cn("mt-1.5 size-1.5 shrink-0 rounded-full", LEVEL_DOT[n.level])}
                        />
                        <div className="min-w-0 flex-1">
                          <div className="flex items-baseline justify-between gap-2">
                            <p className="truncate text-[13px] font-medium text-(--color-foreground)">
                              {n.title}
                            </p>
                            <span className="shrink-0 font-mono text-[10px] uppercase tracking-[0.12em] text-(--color-muted-foreground)">
                              {formatRelative(now - n.createdAt, t.app.notifications.relative)}
                            </span>
                          </div>
                          {n.body ? (
                            <p className="mt-0.5 text-[12.5px] leading-relaxed text-(--color-muted-foreground)">
                              {n.body}
                            </p>
                          ) : null}
                        </div>
                        {!n.read ? (
                          <button
                            type="button"
                            onClick={() => markRead(n.id)}
                            title={t.app.notifications.markRead}
                            aria-label={t.app.notifications.markRead}
                            className="flex size-6 shrink-0 items-center justify-center rounded-md text-(--color-muted-foreground) opacity-0 transition-opacity hover:bg-(--color-muted) hover:text-(--color-foreground) group-hover:opacity-100"
                          >
                            <Check className="size-3" strokeWidth={3} />
                          </button>
                        ) : null}
                      </div>
                    </li>
                  ))}
                </ul>
              )}
            </div>
          </motion.div>
        ) : null}
      </AnimatePresence>
    </div>
  );
}
