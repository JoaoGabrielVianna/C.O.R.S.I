import {
  useCallback,
  useMemo,
  useState,
  useEffect,
  type ReactNode,
} from "react";
import { CommandContext, type Command } from "./context";

/**
 * CommandProvider — global registry + open/close state for the palette.
 *
 * Commands live in plain React state keyed by id, so register/unregister are
 * pure state transitions and the React rules-of-hooks linter is happy.
 *
 * Keyboard: ⌘K / Ctrl+K toggles the palette globally. Escape is handled by
 * the palette component itself so closing also clears the local query state.
 */

export function CommandProvider({ children }: { children: ReactNode }) {
  const [isOpen, setIsOpen] = useState(false);
  const [byId, setById] = useState<Record<string, Command>>({});

  const open = useCallback(() => setIsOpen(true), []);
  const close = useCallback(() => setIsOpen(false), []);
  const toggle = useCallback(() => setIsOpen((s) => !s), []);

  const registerCommand = useCallback((cmd: Command) => {
    setById((prev) => ({ ...prev, [cmd.id]: cmd }));
    return () => {
      setById((prev) => {
        if (!(cmd.id in prev)) return prev;
        const next = { ...prev };
        delete next[cmd.id];
        return next;
      });
    };
  }, []);

  const registerCommands = useCallback((cmds: readonly Command[]) => {
    setById((prev) => {
      const next = { ...prev };
      for (const c of cmds) next[c.id] = c;
      return next;
    });
    return () => {
      setById((prev) => {
        const next = { ...prev };
        let changed = false;
        for (const c of cmds) {
          if (c.id in next) {
            delete next[c.id];
            changed = true;
          }
        }
        return changed ? next : prev;
      });
    };
  }, []);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const isPaletteKey = (e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k";
      if (!isPaletteKey) return;
      e.preventDefault();
      setIsOpen((s) => !s);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  const commands = useMemo<readonly Command[]>(() => Object.values(byId), [byId]);

  const value = useMemo(
    () => ({ commands, isOpen, open, close, toggle, registerCommand, registerCommands }),
    [commands, isOpen, open, close, toggle, registerCommand, registerCommands],
  );

  return <CommandContext.Provider value={value}>{children}</CommandContext.Provider>;
}
