/**
 * Owns one in-flight turn: the optimistic user bubble, the reasoning and
 * answer arriving token by token, which phase the turn is in, and the
 * handoff back to the cache when it ends.
 *
 * ── Why the text is not in TanStack Query ──────────────────────────────
 * A streaming reply changes dozens of times a second. Writing each frame
 * into the query cache would rewrite the whole transcript on every token.
 * The in-flight turn lives here as local state and the persisted history
 * stays in `useMessages`; the two meet exactly once, when the turn ends
 * and the transcript is refetched.
 *
 * ── Why deltas are batched through a frame ─────────────────────────────
 * A fast model emits tokens far quicker than the screen refreshes.
 * Calling setState per token queues renders the user cannot perceive and
 * makes a long answer visibly stutter. Deltas accumulate in a ref and are
 * flushed once per animation frame — the text still appears to stream,
 * at a rate the display can actually show.
 */

import { useCallback, useEffect, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";

import { ApiError } from "@/lib/api/client";
import { getApiWorkspaceId } from "@/lib/api/workspace";
import { truncateFrom } from "@/modules/agents/api/conversations";
import {
  streamMessage,
  streamResume,
  type SendReference,
  type ToolFrame,
} from "@/modules/agents/api/stream";
import { conversationsRootKey, messagesKey } from "./useConversations";

/**
 * Where the turn is right now. The distinction that matters to the user is
 * `sending` (nothing has come back yet) versus `reasoning` (the model is
 * working and we can show what it is thinking) versus `writing` (the
 * answer is landing) — three very different things to look at.
 */
export type TurnPhase = "idle" | "sending" | "reasoning" | "writing" | "error";

/**
 * A tool call in the turn that is streaming right now.
 *
 * It exists because a tool round is dead air: the model stops producing
 * tokens while the backend runs something, and without this the reader sees
 * a stalled stream. Once the turn ends these are dropped — the persisted
 * turn carries the same facts, from the server, and keeping a second copy
 * would let the two disagree.
 */
export interface LiveToolCall {
  callId: string;
  name: string;
  /**
   * `not_executed` is a call the runtime refused before running it. It
   * carries the same vocabulary the audit row does, so the live view and
   * a reload cannot describe one event differently. Anything that only
   * needs to know the call did not succeed should test `!== "ok"`.
   */
  status: "running" | "ok" | "error" | "not_executed";
  errorCode?: string;
  durationMs?: number;
}

export interface ChatStream {
  /** The user's turn, shown before the server has confirmed it. */
  pendingUserMessage: string | null;
  /** Chain of thought so far. Empty unless the model is a reasoning one. */
  reasoningText: string;
  /** Answer text so far. Empty until the first answer token. */
  streamingText: string;
  /** Tool calls of the turn in flight, in the order they started. */
  liveTools: LiveToolCall[];
  phase: TurnPhase;
  /** Ms since the request went out. Drives the live "thinking for 3s". */
  elapsedMs: number;
  error: ApiError | null;
  isStreaming: boolean;
  /**
   * Runs a turn. `references` is the capability selection the composer
   * attached to it: omitted or empty means no explicit selection, which is
   * the behaviour every turn had before this existed.
   */
  send: (content: string, references?: readonly SendReference[]) => Promise<void>;
  /**
   * Replaces the turn at `fromSeq` with `content`. Backs both regenerate
   * (same text) and edit (new text): the old turn is deleted server-side
   * first, so the thread never shows the same question twice.
   *
   * It carries no references, and that is a decision rather than an
   * oversight. The turn it replaces has been hard-deleted, taking its
   * selection with it; silently re-attaching what that row used to say
   * would be the client reconstructing a choice the user did not make this
   * time. Regenerating is a fresh turn, scoped the way a fresh turn is.
   */
  resend: (fromSeq: number, content: string) => Promise<void>;
  /** Continue an interrupted turn. See the callback for why it is not a send. */
  resume: (messageId: string, label: string) => Promise<void>;
  /** Ends the turn early. The server keeps whatever text it already sent. */
  stop: () => void;
  clearError: () => void;
}

/** How often the live elapsed counter updates while waiting. */
const TICK_MS = 100;

export function useChatStream(conversationId: string | null): ChatStream {
  const qc = useQueryClient();
  const [pendingUserMessage, setPendingUserMessage] = useState<string | null>(null);
  const [reasoningText, setReasoningText] = useState("");
  const [streamingText, setStreamingText] = useState("");
  const [liveTools, setLiveTools] = useState<LiveToolCall[]>([]);
  const [phase, setPhase] = useState<TurnPhase>("idle");
  const [elapsedMs, setElapsedMs] = useState(0);
  const [error, setError] = useState<ApiError | null>(null);

  const abortRef = useRef<AbortController | null>(null);
  const answerBuf = useRef("");
  const reasoningBuf = useRef("");
  const frameRef = useRef<number | null>(null);

  const cancelFrame = useCallback(() => {
    if (frameRef.current !== null) {
      cancelAnimationFrame(frameRef.current);
      frameRef.current = null;
    }
  }, []);

  // Abort an in-flight turn if the component goes away mid-stream; the
  // backend still persists the partial answer.
  useEffect(() => {
    return () => {
      abortRef.current?.abort();
      cancelFrame();
    };
  }, [cancelFrame]);

  // The live counter runs only while the user is actually waiting. Once
  // the answer starts landing there is something to read, and a ticking
  // number next to it is just noise.
  const waiting = phase === "sending" || phase === "reasoning";
  useEffect(() => {
    if (!waiting) return;
    const startedAt = Date.now() - elapsedMs;
    const id = setInterval(() => setElapsedMs(Date.now() - startedAt), TICK_MS);
    return () => clearInterval(id);
    // elapsedMs is intentionally out of the dep list: including it would
    // tear down and rebuild the interval on every tick.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [waiting]);

  /** Flushes both buffers on the next frame. */
  const scheduleFlush = useCallback(() => {
    if (frameRef.current !== null) return;
    frameRef.current = requestAnimationFrame(() => {
      frameRef.current = null;
      setReasoningText(reasoningBuf.current);
      setStreamingText(answerBuf.current);
    });
  }, []);

  const runTurn = useCallback(
    async (
      content: string,
      references?: readonly SendReference[],
      /**
       * When set, this is a CONTINUATION of the named turn rather than a
       * new question. `content` is then only what the composer shows while
       * the stream runs — nothing is sent, and no user message is written.
       */
      resumeMessageId?: string,
    ) => {
      if (!conversationId) return;

      const controller = new AbortController();
      abortRef.current = controller;
      answerBuf.current = "";
      reasoningBuf.current = "";

      setPendingUserMessage(content);
      setReasoningText("");
      setStreamingText("");
      setLiveTools([]);
      setElapsedMs(0);
      setError(null);
      setPhase("sending");

      let streamError: ApiError | null = null;

      try {
        const handlers = {
          onReasoning: (text: string) => {
              reasoningBuf.current += text;
              setPhase((p) => (p === "sending" ? "reasoning" : p));
              scheduleFlush();
            },
          onDelta: (text: string) => {
              answerBuf.current += text;
              // React bails out when the value is unchanged, so this is a
              // no-op on every token after the first.
              setPhase("writing");
              scheduleFlush();
            },
          onTool: (frame: ToolFrame) => {
              // Correlated by call_id, not appended blindly: a round can run
              // several tools at once, so the `ok` frame of one must update
              // its own row rather than land after the `running` of another.
              setLiveTools((current) => {
                const next: LiveToolCall = {
                  callId: frame.call_id,
                  name: frame.name,
                  status: frame.status,
                  errorCode: frame.error_code,
                  durationMs: frame.duration_ms,
                };
                const at = current.findIndex((c) => c.callId === frame.call_id);
                if (at === -1) return [...current, next];
                const copy = [...current];
                copy[at] = next;
                return copy;
              });
            },
          onDone: () => {
              /* the refetch below is what renders the final message */
            },
          onError: (err: ApiError) => {
            streamError = err;
          },
        };
        // Same transport, same frames, same receipts — a continuation IS
        // the turn arriving, just later. The only difference is that it
        // sends no question.
        await (resumeMessageId
          ? streamResume(conversationId, resumeMessageId, handlers, controller.signal)
          : streamMessage(conversationId, content, handlers, controller.signal, references));
      } catch (err) {
        // Thrown only for failures that happened before the stream opened,
        // which still carry a status code.
        streamError = err instanceof ApiError ? err : new ApiError(0, null, String(err));
      }

      cancelFrame();

      // Pull the persisted transcript *before* dropping the local copies.
      // Clearing first would blank the just-written answer for a frame,
      // and a reply that flickers out reads as a bug.
      const workspaceId = getApiWorkspaceId();
      await qc.invalidateQueries({ queryKey: messagesKey(workspaceId, conversationId) });
      void qc.invalidateQueries({ queryKey: conversationsRootKey(workspaceId) });

      setPendingUserMessage(null);
      setReasoningText("");
      setStreamingText("");
      // The persisted turn carries the same tool outcomes, from the server.
      // Keeping the live copy after that would be a second source for one
      // fact, and the two would eventually disagree.
      setLiveTools([]);
      answerBuf.current = "";
      reasoningBuf.current = "";
      abortRef.current = null;

      if (streamError) {
        setError(streamError);
        setPhase("error");
      } else {
        setPhase("idle");
      }
    },
    [cancelFrame, conversationId, qc, scheduleFlush],
  );

  const send = useCallback(
    async (content: string, references?: readonly SendReference[]) => {
      if (phase === "sending" || phase === "reasoning" || phase === "writing") return;
      await runTurn(content, references);
    },
    [phase, runTurn],
  );

  const resend = useCallback(
    async (fromSeq: number, content: string) => {
      if (!conversationId) return;
      if (phase === "sending" || phase === "reasoning" || phase === "writing") return;
      try {
        await truncateFrom(conversationId, fromSeq);
      } catch (err) {
        // The old turn is still there, so sending now would duplicate the
        // question. Report and stop rather than making a mess of the thread.
        setError(err instanceof ApiError ? err : new ApiError(0, null, String(err)));
        setPhase("error");
        return;
      }
      await qc.invalidateQueries({
        queryKey: messagesKey(getApiWorkspaceId(), conversationId),
      });
      await runTurn(content);
    },
    [conversationId, phase, qc, runTurn],
  );

  /**
   * Continue an interrupted turn instead of asking again.
   *
   * Takes the id of the turn that stopped. Nothing is typed and nothing is
   * sent: the server re-answers the question already in the transcript,
   * knowing which entities the first attempt created. See streamResume.
   */
  const resume = useCallback(
    async (messageId: string, label: string) => {
      if (phase === "sending" || phase === "reasoning" || phase === "writing") return;
      await runTurn(label, undefined, messageId);
    },
    [phase, runTurn],
  );

  const stop = useCallback(() => {
    abortRef.current?.abort();
  }, []);

  const clearError = useCallback(() => {
    setError(null);
    setPhase("idle");
  }, []);

  return {
    pendingUserMessage,
    reasoningText,
    liveTools,
    streamingText,
    phase,
    elapsedMs,
    error,
    isStreaming: phase === "sending" || phase === "reasoning" || phase === "writing",
    send,
    resend,
    resume,
    stop,
    clearError,
  };
}
