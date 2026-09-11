import { useContext } from "react";
import {
  NotificationsContext,
  type NotificationsContextValue,
} from "./context";

export function useNotifications(): NotificationsContextValue {
  const ctx = useContext(NotificationsContext);
  if (!ctx)
    throw new Error(
      "useNotifications must be used inside <NotificationsProvider>",
    );
  return ctx;
}
