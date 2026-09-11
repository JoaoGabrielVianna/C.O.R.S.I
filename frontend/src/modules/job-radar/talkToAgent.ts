import { useCallback, useState } from "react";
import { useNavigate } from "react-router-dom";

import { listAgents, type ApiAgent } from "@/modules/agents/api/agents";
import { createConversation } from "@/modules/agents/api/conversations";
import type { ContextReferenceInput } from "@/modules/agents/context-references/contract";

/**
 * "Conversar com o agente" — opening a thread ABOUT an opportunity.
 *
 * ── Why this file is in job-radar and not in agents ────────────────────
 * Because it is Job Radar deciding to start a conversation, not Agents
 * learning what an opportunity is. It uses two published pieces of the
 * Agents module — list the agents, create a conversation — and passes an
 * opaque `{type, id}`. Agents never learns the word "opportunity"; Job
 * Radar never learns what an agent's instructions are.
 *
 * ── Why the user picks the agent ───────────────────────────────────────
 * There is no such thing as "the Scout agent" in this system, and inventing
 * one would have meant either of the two couplings the architecture
 * forbids:
 *
 *   agent.name === "Scout"        a heuristic on a display string, which
 *                                 breaks the moment the agent is renamed
 *                                 and silently picks the wrong one if a
 *                                 second agent is ever called that.
 *   a "job radar agent" setting   Agents learning that Job Radar exists,
 *                                 or Job Radar storing an agent id — the
 *                                 Module → Module dependency the
 *                                 architecture rules out.
 *
 * So the choice is the user's, made once and remembered in the browser.
 * That is a PREFERENCE, not a domain fact: it lives where preferences live,
 * it is per-device, and losing it costs one click. When the workspace has
 * exactly one agent there is nothing to ask and the click goes straight
 * through.
 *
 * A workspace-level default agent is the natural next step — an inbound
 * WhatsApp message will need one, since there is no user present to ask —
 * but that is a decision about the Agents module and is deliberately not
 * being made here as a side effect of a Job Radar button.
 */

/** Where the remembered choice lives. Per browser, per workspace-agnostic. */
const PREFERRED_AGENT_KEY = "corsi.module.jobradar.preferredAgentId";

export function readPreferredAgentId(): string | null {
  if (typeof window === "undefined") return null;
  return window.localStorage.getItem(PREFERRED_AGENT_KEY);
}

export function writePreferredAgentId(id: string) {
  if (typeof window === "undefined") return;
  window.localStorage.setItem(PREFERRED_AGENT_KEY, id);
}

/**
 * Picks the agent to open the conversation with, or reports that the user
 * has to choose.
 *
 * The remembered id is verified against the live list rather than trusted:
 * an agent that was deleted, or that belongs to a workspace this browser is
 * no longer in, must fall back to asking rather than to a failed request.
 */
export function resolveAgent(
  agents: readonly ApiAgent[],
  preferredId: string | null,
): { agent: ApiAgent } | { mustChoose: true } {
  if (agents.length === 0) return { mustChoose: true };
  if (preferredId) {
    const remembered = agents.find((a) => a.id === preferredId);
    if (remembered) return { agent: remembered };
  }
  if (agents.length === 1) return { agent: agents[0] };
  return { mustChoose: true };
}

export interface TalkToAgentState {
  /** The agents to choose between. Non-empty only while the picker is open. */
  choices: ApiAgent[];
  busy: boolean;
  error: string | null;
  /** Starts the flow for one entity. Opens the picker only if it has to. */
  start: (reference: ContextReferenceInput) => Promise<void>;
  /** Completes the flow with the agent the user picked. */
  choose: (agentId: string) => Promise<void>;
  cancel: () => void;
}

/**
 * Drives the whole interaction: resolve the agent, create the thread with
 * the entity attached, navigate to it.
 *
 * The reference travels as `{type, id}` and nothing else. The label the
 * chat will show is written by the backend from Job Radar's own data, so
 * the card in the conversation cannot disagree with the record — and a
 * client cannot put words of its own into the agent's context.
 */
export function useTalkToAgent(): TalkToAgentState {
  const navigate = useNavigate();
  const [choices, setChoices] = useState<ApiAgent[]>([]);
  const [pending, setPending] = useState<ContextReferenceInput | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const open = useCallback(
    async (agentId: string, reference: ContextReferenceInput) => {
      const conversation = await createConversation(agentId, "", [reference]);
      setChoices([]);
      setPending(null);
      navigate(`/app/modules/agents/${agentId}/c/${conversation.id}`);
    },
    [navigate],
  );

  const start = useCallback(
    async (reference: ContextReferenceInput) => {
      setBusy(true);
      setError(null);
      try {
        const agents = await listAgents();
        const resolved = resolveAgent(agents, readPreferredAgentId());
        if ("agent" in resolved) {
          await open(resolved.agent.id, reference);
          return;
        }
        if (agents.length === 0) {
          // Said plainly rather than navigating somewhere that will also be
          // empty. There is nothing to converse with yet.
          setError("Nenhum agente configurado ainda. Crie um em Agents primeiro.");
          return;
        }
        setChoices(agents);
        setPending(reference);
      } catch (e) {
        setError(e instanceof Error ? e.message : "não foi possível abrir a conversa");
      } finally {
        setBusy(false);
      }
    },
    [open],
  );

  const choose = useCallback(
    async (agentId: string) => {
      if (!pending) return;
      setBusy(true);
      setError(null);
      try {
        // Remembered only after an explicit choice, never after a fallback:
        // "the only agent that existed" is not a preference, and storing it
        // would silently become one the day a second agent is created.
        writePreferredAgentId(agentId);
        await open(agentId, pending);
      } catch (e) {
        setError(e instanceof Error ? e.message : "não foi possível abrir a conversa");
      } finally {
        setBusy(false);
      }
    },
    [open, pending],
  );

  const cancel = useCallback(() => {
    setChoices([]);
    setPending(null);
    setError(null);
  }, []);

  return { choices, busy, error, start, choose, cancel };
}
