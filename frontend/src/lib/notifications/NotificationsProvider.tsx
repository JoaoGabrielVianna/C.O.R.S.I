import { useCallback, useEffect, useMemo, useState, type ReactNode } from "react";
import {
  NotificationsContext,
  type Notification,
} from "./context";

/**
 * NotificationsProvider — workspace notification feed.
 *
 * Persists to localStorage so unread state survives reloads. Seeds three
 * honest mock items on first visit (real build events from the conversation
 * that birthed this workspace, not fabricated metrics).
 *
 * Backend insertion point: when a real event bus exists, the seed block is
 * replaced by a subscription that pushes items via `push()`. The consumer
 * surface (bell, popover, mark-read) stays unchanged.
 */

const STORAGE_KEY = "corsi.notifications";
const SEED_FLAG_KEY = "corsi.notifications.seeded";

const SEED: ReadonlyArray<Omit<Notification, "id" | "createdAt" | "read">> = [
  {
    title: "App shell created",
    body: "Sidebar, header, command palette and notification center are live.",
    level: "success",
  },
  {
    title: "Job Radar in planning",
    body: "First module is being scoped. Status: building.",
    level: "info",
  },
  {
    title: "Auth provider: mock",
    body: "Sessions are local-only until Keycloak is wired.",
    level: "warning",
  },
];

function readStored(): Notification[] {
  if (typeof window === "undefined") return [];
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY);
    if (!raw) return [];
    const parsed = JSON.parse(raw) as Notification[];
    if (Array.isArray(parsed)) return parsed;
  } catch {
    /* corrupted — fall through */
  }
  return [];
}

function writeStored(items: Notification[]) {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify(items));
  } catch {
    /* ignore */
  }
}

function makeId() {
  return `n_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 8)}`;
}

export function NotificationsProvider({ children }: { children: ReactNode }) {
  const [notifications, setNotifications] = useState<Notification[]>(() => {
    if (typeof window === "undefined") return [];
    const stored = readStored();
    const alreadySeeded = window.localStorage.getItem(SEED_FLAG_KEY) === "1";
    if (stored.length > 0 || alreadySeeded) return stored;
    const now = Date.now();
    const seeded: Notification[] = SEED.map((s, i) => ({
      ...s,
      id: makeId(),
      createdAt: now - i * 1000 * 60 * 7,
      read: false,
    }));
    try {
      window.localStorage.setItem(SEED_FLAG_KEY, "1");
    } catch {
      /* ignore */
    }
    return seeded;
  });

  useEffect(() => {
    writeStored(notifications);
  }, [notifications]);

  const push = useCallback(
    (n: Omit<Notification, "id" | "createdAt" | "read">) => {
      setNotifications((prev) => [
        { ...n, id: makeId(), createdAt: Date.now(), read: false },
        ...prev,
      ]);
    },
    [],
  );

  const markRead = useCallback((id: string) => {
    setNotifications((prev) =>
      prev.map((n) => (n.id === id ? { ...n, read: true } : n)),
    );
  }, []);

  const markAllRead = useCallback(() => {
    setNotifications((prev) => prev.map((n) => ({ ...n, read: true })));
  }, []);

  const clear = useCallback(() => setNotifications([]), []);

  const value = useMemo(
    () => ({
      notifications,
      unreadCount: notifications.filter((n) => !n.read).length,
      push,
      markRead,
      markAllRead,
      clear,
    }),
    [notifications, push, markRead, markAllRead, clear],
  );

  return (
    <NotificationsContext.Provider value={value}>
      {children}
    </NotificationsContext.Provider>
  );
}
