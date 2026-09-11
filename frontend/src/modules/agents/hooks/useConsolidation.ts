import { useCallback, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";

import { ApiError } from "@/lib/api/client";
import { getApiWorkspaceId } from "@/lib/api/workspace";
import {
  MEMORY_CONSOLIDATION_DISABLED,
  confirmMemoryCandidates,
  consolidateMemory,
} from "@/modules/agents/api/memories";
import { memoriesKey } from "@/modules/agents/hooks/useMemories";
import type { ConsolidationPhase } from "@/modules/agents/components/memory/MemoryCandidatesDialog";

/**
 * The `/lembrar` flow, held outside the transcript.
 *
 * ── Why this is not a TanStack mutation ────────────────────────────────
 * A mutation would be a fine fit for the request itself, but the thing that
 * has to be modelled here is a dialog with four terminal states, one of
 * which (a refusal by policy) is not a failure and needs its own way out.
 * Keeping the phase explicit is what lets the dialog render the difference
 * instead of showing every non-success as an error.
 *
 * ── What it does with an abandoned request ─────────────────────────────
 * Aborts it, and says plainly what that does and does not mean: the browser
 * stops waiting, and the operation on the other side is not undone. If the
 * provider had already answered, the tokens were bought and the backend has
 * already written the receipt — closing a dialog cannot un-spend money, and
 * pretending otherwise would be the one dishonest thing this flow could do.
 */
export interface Consolidation {
  /** Null when no dialog is open. */
  phase: ConsolidationPhase | null;
  saving: boolean;
  saveError: string | null;
  start: (upToSeq: number) => void;
  confirm: (contents: string[], upToSeq: number) => Promise<void>;
  close: () => void;
}

export function useConsolidation(conversationId: string, agentId: string | undefined): Consolidation {
  const qc = useQueryClient();
  const [phase, setPhase] = useState<ConsolidationPhase | null>(null);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);
  const abort = useRef<AbortController | null>(null);

  const close = useCallback(() => {
    abort.current?.abort();
    abort.current = null;
    setPhase(null);
    setSaveError(null);
  }, []);

  const start = useCallback(
    (upToSeq: number) => {
      abort.current?.abort();
      const controller = new AbortController();
      abort.current = controller;
      setSaveError(null);
      setPhase({ status: "loading" });

      void consolidateMemory(conversationId, upToSeq, controller.signal)
        .then((data) => {
          if (controller.signal.aborted) return;
          setPhase({ status: "ready", data });
        })
        .catch((err: unknown) => {
          if (controller.signal.aborted) return;
          setPhase({ status: "error", ...describe(err) });
        });
    },
    [conversationId],
  );

  const confirm = useCallback(
    async (contents: string[], upToSeq: number) => {
      if (contents.length === 0) return;
      setSaving(true);
      setSaveError(null);
      try {
        await confirmMemoryCandidates(conversationId, contents, upToSeq);
        // The Memory page has to show them without a reload, exactly as a
        // capture from a message does.
        if (agentId) {
          void qc.invalidateQueries({ queryKey: memoriesKey(getApiWorkspaceId(), agentId) });
        }
        setPhase(null);
      } catch (err) {
        // The dialog stays open, holding the selection and the edits. A
        // failed save must never cost the user what they just reviewed.
        setSaveError(err instanceof ApiError ? err.message : String(err));
      } finally {
        setSaving(false);
      }
    },
    [agentId, conversationId, qc],
  );

  return { phase, saving, saveError, start, confirm, close };
}

/**
 * Sorts a failure into what the dialog can act on.
 *
 * `policy` is not an error in the ordinary sense: it is a setting doing
 * what it was set to do, and the only useful response is a link to the
 * setting. `budget` is a limit the user chose. Everything else is a
 * failure, shown as one.
 */
function describe(err: unknown): { message: string; kind: "policy" | "budget" | "other" } {
  if (err instanceof ApiError) {
    if (err.code === MEMORY_CONSOLIDATION_DISABLED) {
      return { message: err.message, kind: "policy" };
    }
    if (err.status === 429) return { message: err.message, kind: "budget" };
    return { message: err.message, kind: "other" };
  }
  return { message: String(err), kind: "other" };
}
