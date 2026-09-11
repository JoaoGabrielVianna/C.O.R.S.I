import { createContext } from "react";

export type NotificationLevel = "info" | "success" | "warning";

export type Notification = {
  id: string;
  title: string;
  body?: string;
  level: NotificationLevel;
  createdAt: number;
  read: boolean;
};

export type NotificationsContextValue = {
  notifications: readonly Notification[];
  unreadCount: number;
  push: (n: Omit<Notification, "id" | "createdAt" | "read">) => void;
  markRead: (id: string) => void;
  markAllRead: () => void;
  clear: () => void;
};

export const NotificationsContext =
  createContext<NotificationsContextValue | null>(null);
