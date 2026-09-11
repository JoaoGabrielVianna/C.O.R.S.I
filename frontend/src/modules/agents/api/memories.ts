/**
 * Typed REST client for /chat/agents/{id}/memories.
 *
 * Agent Memory is what survives the end of a conversation: facts the user
 * decided the agent should carry into every future thread. It is not the
 * transcript, and it is not a source.
 *
 * Every route is nested under its agent, including the ones addressing a
 * single memory, because memory belongs to the agent and an address that
 * omitted it would disagree with the model.
 */

import { apiFetch } from "@/lib/api/client";

/** How the memory was captured. */
export type MemoryOrigin = "manual" | "conversation";

export interface ApiMemory {
  id: string;
  workspace_id: string;
  agent_id: string;
  content: string;
  origin: MemoryOrigin;
  /**
   * The thread it was captured in. Present only for `conversation` origin,
   * and only until that thread is hard-deleted.
   */
  source_conversation_id?: string;
  /**
   * Resolved server-side at read time. Absent alongside a present
   * `source_conversation_id` means the thread was deleted: the memory
   * outlives its origin by design, and the interface says so instead of
   * failing. See `provenanceOf`.
   */
  source_conversation_title?: string;
  /** A hint at the turn it came from, never a guarantee. */
  source_message_seq?: number;
  enabled: boolean;
  pinned: boolean;
  /**
   * Whether a model composed these words and the user approved them, rather
   * than the user having written them. A different question from `origin`,
   * which says where the capture happened.
   *
   * Always false in this version: nothing proposes memories yet. It is
   * never sent on a write — the server decides it from which operation ran,
   * and the create route refuses a body that mentions it at all.
   */
  model_proposed: boolean;
  /**
   * Whether this memory would reach the model on the next turn, given the
   * character budget. Computed by the backend with the same function the
   * context builder uses, so the count on screen cannot drift from what is
   * actually sent.
   */
  in_context: boolean;
  created_at: string;
  updated_at: string;
}

export interface MemoryPage {
  items: ApiMemory[];
  /** The cap the server applied. `total` above it means the list was cut. */
  limit: number;
  total: number;
  /** What the selected memories contribute to a turn, header included. */
  used_characters: number;
  budget_characters: number;
}

export interface CreateMemoryRequest {
  content: string;
  /** Set when the memory is captured inside a thread. */
  source_conversation_id?: string;
  source_message_seq?: number;
  pinned?: boolean;
}

export interface UpdateMemoryRequest {
  content?: string;
  enabled?: boolean;
  pinned?: boolean;
}

export function listMemories(agentId: string): Promise<MemoryPage> {
  return apiFetch<MemoryPage>(`/chat/agents/${agentId}/memories`);
}

export function createMemory(agentId: string, body: CreateMemoryRequest): Promise<ApiMemory> {
  return apiFetch<ApiMemory>(`/chat/agents/${agentId}/memories`, {
    method: "POST",
    body: JSON.stringify(body),
  });
}

export function updateMemory(
  agentId: string,
  id: string,
  body: UpdateMemoryRequest,
): Promise<ApiMemory> {
  return apiFetch<ApiMemory>(`/chat/agents/${agentId}/memories/${id}`, {
    method: "PATCH",
    body: JSON.stringify(body),
  });
}

export function deleteMemory(agentId: string, id: string): Promise<void> {
  return apiFetch<void>(`/chat/agents/${agentId}/memories/${id}`, { method: "DELETE" });
}

/** Mirrors the backend CHECK constraint, so the counter and the server agree. */
export const MAX_MEMORY_CHARS = 2000;

/**
 * What the interface can say about where a memory came from.
 *
 * The three cases are distinct facts and must not be collapsed:
 *
 *   manual    the user typed it on the Memory page
 *   thread    it came from a conversation that still exists
 *   lost      it came from a conversation that no longer does
 *
 * `lost` is a normal state, not an error. A memory outliving its thread is
 * the rule the schema was shaped around.
 */
export type Provenance =
  | { kind: "manual" }
  | { kind: "thread"; conversationId: string; title: string }
  | { kind: "lost" };

export function provenanceOf(m: ApiMemory): Provenance {
  if (m.origin === "manual") return { kind: "manual" };
  if (m.source_conversation_id && m.source_conversation_title !== undefined) {
    return {
      kind: "thread",
      conversationId: m.source_conversation_id,
      // An untitled thread is one that never got a first message. It still
      // exists, so it is still a link — it just has nothing to be called.
      title: m.source_conversation_title || "Conversa sem título",
    };
  }
  return { kind: "lost" };
}

/* ── consolidation ───────────────────────────────────────────────────── */

/**
 * One thing the model proposed remembering, before anyone agreed to it.
 *
 * It has no id, and that is not an omission: a candidate is not stored
 * anywhere. Asking for candidates writes nothing at all, and only the ones
 * the user confirms become memories, through `createMemory` above.
 */
export interface ApiMemoryCandidate {
  content: string;
  /** Why this would be useful later. Shown, never stored. */
  reason: string;
  /**
   * The id of an existing memory with the same normalized text, when there
   * is one. Exact match only: a paraphrase is not detected, by design.
   */
  duplicate_of: string | null;
}

/** What one auxiliary operation cost. */
export interface ApiOperationUsage {
  model: string;
  prompt_tokens: number;
  completion_tokens: number;
  usage_source: "provider" | "estimated" | "unknown";
  /** Null means the price could not be read. Never render it as zero. */
  cost_usd: number | null;
}

export interface ApiMemoryCandidates {
  candidates: ApiMemoryCandidate[];
  /** How many turns actually reached the model, after the input budget. */
  considered_messages: number;
  /** The newest seq actually considered — what provenance records. */
  effective_up_to_seq: number;
  /** Absent when no provider call was made, which is a real outcome. */
  usage?: ApiOperationUsage;
}

/**
 * Reads a conversation as it stands and proposes what is worth remembering.
 *
 * `upToSeq` is the last message the user could see when they asked. The
 * server treats it as a ceiling and reports back what it actually read, so
 * this is a request about a snapshot rather than a promise about one.
 *
 * It spends tokens and writes no memory. The `signal` is what lets a
 * dialog closed mid-flight stop waiting — the operation itself is not
 * undone by that, and its cost is recorded either way.
 */
export function consolidateMemory(
  conversationId: string,
  upToSeq: number,
  signal?: AbortSignal,
): Promise<ApiMemoryCandidates> {
  return apiFetch<ApiMemoryCandidates>(
    `/chat/conversations/${conversationId}/memory-candidates`,
    { method: "POST", body: JSON.stringify({ up_to_seq: upToSeq }), signal },
  );
}

/** The machine-readable reason behind the 409 this route can answer. */
export const MEMORY_CONSOLIDATION_DISABLED = "memory_consolidation_disabled";

/**
 * Saves the candidates the user kept.
 *
 * A different address from `createMemory` on purpose: this one records that
 * a model proposed the text and the user approved it, and that is a fact
 * about how the memory came to be rather than a field a client may set.
 * Which route ran is what decides it.
 *
 * All or nothing. The user selected a set and pressed one button; half of
 * it saved would leave them comparing a dialog they can no longer see
 * against a list that changed underneath it.
 */
export function confirmMemoryCandidates(
  conversationId: string,
  contents: string[],
  upToSeq: number,
): Promise<ApiMemory[]> {
  return apiFetch<{ items: ApiMemory[] }>(`/chat/conversations/${conversationId}/memories`, {
    method: "POST",
    body: JSON.stringify({ contents, up_to_seq: upToSeq }),
  }).then((page) => page.items ?? []);
}
