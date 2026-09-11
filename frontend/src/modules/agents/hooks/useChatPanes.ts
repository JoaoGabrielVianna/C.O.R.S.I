/*
 * FROZEN — part of Comparison Mode, which is out of scope for Agents v1.0.0
 * (owner decision D8).
 *
 * The multi-column layout state, persisted per browser. The v1 routes
 * address one conversation at a time, so nothing reads this any more. Kept
 * intact rather than deleted: D8 preserves the code and leaves removal as a
 * separate, explicit decision.
 */
import { useCallback, useMemo, useState } from "react";
import { useMediaQuery } from "./useMediaQuery";

/**
 * The open chat columns.
 *
 * A pane is a slot on screen, not a conversation: it keeps its identity
 * while you swap threads through it, which is what lets React keep the
 * scroll position and any in-flight stream attached to the right column.
 * `conversationId` is null while a freshly opened pane waits for you to
 * pick a thread.
 *
 * Layout is remembered per browser. Reopening the module to the same two
 * columns you left is the whole point of having them.
 */

export interface Pane {
  id: string;
  conversationId: string | null;
}

const KEY = "corsi.agents.panes";

/**
 * How many columns actually fit.
 *
 * Bounded by width rather than by preference: three panes on a laptop
 * would be three unusable slivers. The breakpoints match Tailwind's `lg`
 * and `2xl`, so the grid and this count never disagree.
 */
function useMaxPanes(): number {
  const isLg = useMediaQuery("(min-width: 1024px)");
  const is2xl = useMediaQuery("(min-width: 1536px)");
  if (is2xl) return 3;
  if (isLg) return 2;
  return 1;
}

let paneSeq = 0;
function newPaneId(): string {
  paneSeq += 1;
  return `pane-${paneSeq}`;
}

function readStored(): (string | null)[] {
  try {
    const raw = localStorage.getItem(KEY);
    if (!raw) return [];
    const parsed: unknown = JSON.parse(raw);
    if (!Array.isArray(parsed)) return [];
    return parsed.filter((v): v is string | null => v === null || typeof v === "string");
  } catch {
    return [];
  }
}

function persist(panes: Pane[]): void {
  try {
    localStorage.setItem(KEY, JSON.stringify(panes.map((p) => p.conversationId)));
  } catch {
    // private mode / storage disabled — the layout just won't persist.
  }
}

export interface ChatPanes {
  panes: Pane[];
  /** The pane that sidebar clicks and keyboard focus act on. */
  focusedId: string;
  maxPanes: number;
  canAddPane: boolean;
  /** Conversation ids currently on screen, for marking them in the list. */
  openConversationIds: Set<string>;
  addPane: () => void;
  closePane: (paneId: string) => void;
  focusPane: (paneId: string) => void;
  setPaneConversation: (paneId: string, conversationId: string | null) => void;
  /** Points the focused pane at a conversation — what the sidebar does. */
  openInFocusedPane: (conversationId: string) => void;
}

/*
 * There is deliberately no "reconcile deleted conversations" step. The page
 * resolves each pane's conversation while rendering, so a pane pointing at
 * a deleted thread simply falls back to the picker. Cleaning the stored id
 * would mean a setState inside an effect, and an extra render, to fix
 * something nobody can see.
 */

export function useChatPanes(): ChatPanes {
  const maxPanes = useMaxPanes();

  const [panes, setPanes] = useState<Pane[]>(() => {
    const stored = readStored();
    if (stored.length === 0) return [{ id: newPaneId(), conversationId: null }];
    return stored.map((conversationId) => ({ id: newPaneId(), conversationId }));
  });
  const [focusedId, setFocusedId] = useState<string>(() => panes[0]?.id ?? "");

  // Narrowing the window must not lose a pane: the extras stay in state and
  // come back when there is room again. Only the visible slice is rendered.
  const visible = useMemo(() => panes.slice(0, maxPanes), [panes, maxPanes]);

  // If the focused pane scrolled out of the visible slice, focus falls back
  // to the last one still on screen rather than pointing at nothing.
  const effectiveFocusedId = visible.some((p) => p.id === focusedId)
    ? focusedId
    : (visible.at(-1)?.id ?? "");

  const update = useCallback((next: Pane[]) => {
    setPanes(next);
    persist(next);
  }, []);

  const addPane = useCallback(() => {
    setPanes((current) => {
      if (current.length >= 4) return current; // absolute ceiling
      const pane = { id: newPaneId(), conversationId: null };
      const next = [...current, pane];
      persist(next);
      setFocusedId(pane.id);
      return next;
    });
  }, []);

  const closePane = useCallback(
    (paneId: string) => {
      setPanes((current) => {
        // Never close the last one: an empty module with no way back would
        // be a dead end. It resets to the picker instead.
        if (current.length <= 1) {
          const next = [{ id: current[0]?.id ?? newPaneId(), conversationId: null }];
          persist(next);
          return next;
        }
        const next = current.filter((p) => p.id !== paneId);
        persist(next);
        if (paneId === focusedId) setFocusedId(next[0].id);
        return next;
      });
    },
    [focusedId],
  );

  const focusPane = useCallback((paneId: string) => setFocusedId(paneId), []);

  const setPaneConversation = useCallback(
    (paneId: string, conversationId: string | null) => {
      update(panes.map((p) => (p.id === paneId ? { ...p, conversationId } : p)));
    },
    [panes, update],
  );

  const openInFocusedPane = useCallback(
    (conversationId: string) => {
      const target = effectiveFocusedId || panes[0]?.id;
      if (!target) return;
      update(panes.map((p) => (p.id === target ? { ...p, conversationId } : p)));
    },
    [effectiveFocusedId, panes, update],
  );

  const openConversationIds = useMemo(
    () => new Set(visible.map((p) => p.conversationId).filter((id): id is string => id !== null)),
    [visible],
  );

  return {
    panes: visible,
    focusedId: effectiveFocusedId,
    maxPanes,
    canAddPane: panes.length < maxPanes,
    openConversationIds,
    addPane,
    closePane,
    focusPane,
    setPaneConversation,
    openInFocusedPane,
  };
}
