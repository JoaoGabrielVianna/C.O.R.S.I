/**
 * Streaming reader for POST /chat/conversations/{id}/messages.
 *
 * ── Why not EventSource ────────────────────────────────────────────────
 * The browser's `EventSource` can only issue a GET and cannot set request
 * headers, so it can carry neither the message body nor `X-Workspace-Id`.
 * Every request in this app is workspace-scoped, so the built-in client is
 * unusable here. `fetch` + `ReadableStream` gives us POST, headers, and
 * cancellation — at the cost of parsing the event framing ourselves, which
 * is the small block at the bottom of this file.
 *
 * ── The backend's two error modes ──────────────────────────────────────
 * The stream opens lazily, on the model's first token. Anything that fails
 * before then (unknown conversation, missing key, provider refusing the
 * request) arrives as an ordinary JSON error with a real status code, and
 * is thrown as `ApiError` exactly like any other endpoint. Once tokens are
 * flowing there is no status left to send, so a later failure arrives as an
 * `error` frame inside the stream and surfaces through `onError`.
 */

import { API_BASE, ApiError, type ApiErrorBody } from "@/lib/api/client";
import { getApiWorkspaceId } from "@/lib/api/workspace";
import type { ApiMessage } from "./conversations";

/**
 * One tool call's progress, live.
 *
 * Two frames per call: `running` when it starts, then the outcome —
 * `ok`, `error`, or `not_executed` for a call the runtime refused before
 * it ran. The same vocabulary the audit row uses, so the live view and a
 * reload cannot describe one event differently.
 * Correlate them by `call_id` — a round may run several tools at once, and
 * their frames interleave.
 *
 * No arguments and no result: the live channel says what is happening, and
 * the payloads are behind the audit read the user asks for.
 */
export interface ToolFrame {
  call_id: string;
  name: string;
  status: "running" | "ok" | "error" | "not_executed";
  error_code?: string;
  duration_ms?: number;
}

/**
 * What the SYSTEM knows a turn did, as opposed to what the model said.
 *
 * A live financial agent once answered "8 transações importadas" in a turn
 * that called nothing, against a ledger holding zero transactions. The
 * runtime was right and the person was misinformed, because the absence of
 * a tool call is invisible in prose.
 *
 * So the answer is explicit for every assistant turn, including the turns
 * where nothing ran: `executed === 0` is a statement, not a missing field.
 * Anything that tells a user a change happened must read this, never the
 * text beside it.
 */
export interface WriteExecution {
  tool_call_id: string;
  capability: string;
  status: "REQUESTED" | "EXECUTED" | "FAILED" | "NOT_EXECUTED";
  occurred_at: string;
  duration_ms: number;
  error_code?: string;
}

export interface WriteReceipt {
  message_id: string;
  writes: WriteExecution[];
  executed: number;
  failed: number;
  refused: number;
}

/** One call to a system this product does not own. Metadata only — never
 *  what the call returned. See domain/read_receipt.go. */
export interface ExternalRead {
  tool_call_id: string;
  capability: string;
  /** The capability's owner, derived from its name: "meta_threads". */
  source: string;
  status: "VERIFIED_EXTERNAL_READ" | "FAILED_EXTERNAL_READ" | "NO_EXTERNAL_READ";
  occurred_at: string;
  duration_ms: number;
  error_code?: string;
}

/**
 * Whether this turn read anything outside the product.
 *
 * The read-side twin of WriteReceipt, and the answer to a turn that
 * reported a follower count having called nothing. Derived from execution
 * records; it never depends on what the model wrote.
 */
export interface ReadReceipt {
  message_id: string;
  reads: ExternalRead[];
  status: "VERIFIED_EXTERNAL_READ" | "FAILED_EXTERNAL_READ" | "NO_EXTERNAL_READ";
  verified: number;
  failed: number;
  /** Whether the agent could have read externally at all. A presentation
   *  gate, not a claim about the turn. */
  available: boolean;
}

export interface StreamHandlers {
  /**
   * One increment of the model's chain of thought. Only reasoning models
   * emit these, and they all arrive before the first `onDelta`.
   */
  onReasoning: (text: string) => void;
  /** One increment of answer text. Append it; do not replace. */
  onDelta: (text: string) => void;
  /**
   * A tool call starting or finishing. Optional, and absent handlers are
   * simply not called — a turn with no tools never emits one.
   */
  onTool?: (frame: ToolFrame) => void;
  /**
   * Clean finish. Carries the persisted message and the turn's write
   * receipt, so a live client never has to read the prose to find out
   * whether anything changed.
   */
  onDone: (
    message: ApiMessage | null,
    receipt?: WriteReceipt,
    readReceipt?: ReadReceipt,
  ) => void;
  /** Failure after the stream opened. The turn is over. */
  onError: (error: ApiError) => void;
}

/**
 * Sends a message and streams the reply.
 *
 * Resolves when the stream ends, for any reason. Throws only for failures
 * that happened before the stream opened — those still have a status code
 * and belong to the caller's normal error handling. Aborting via `signal`
 * resolves quietly: the backend persists whatever text it had already sent.
 */
/**
 * One capability attached to the turn being sent.
 *
 * Identity only. The label of the stored record is written by the server
 * from its own registry — sending one is rejected outright rather than
 * ignored, because a client that could name a capability must not also get
 * to author how the transcript will describe it forever after.
 */
/**
 * One thing the user attached to a turn, as the wire states it.
 *
 * Identity only, and never a label: the words that reach the permanent
 * record are written by the server from its own registry — see
 * chat/domain/reference.go. The decoder rejects unknown fields, so sending
 * a label is a 400 rather than a value silently discarded.
 *
 * `integration` names a PROVIDER by the namespace its capabilities are
 * named under (`github`). The server expands it against this agent's
 * grants and freezes the individual capabilities that were really exposed,
 * which is what keeps an old turn from appearing to have reached a
 * capability that shipped after it ran.
 */
export interface SendReference {
  kind: "integration";
  id: string;
}

export async function streamMessage(
  conversationId: string,
  content: string,
  handlers: StreamHandlers,
  signal?: AbortSignal,
  references?: readonly SendReference[],
): Promise<void> {
  return streamTurn(
    `/chat/conversations/${conversationId}/messages`,
    // The field is omitted entirely when nothing was attached. An explicit
    // `[]` means the same thing to the server, but a body that carries only
    // what it has to is the one a pre-Batch-4 backend would also accept.
    references && references.length > 0 ? { content, references } : { content },
    handlers,
    signal,
  );
}

/**
 * Continue a turn that stopped with work already done.
 *
 * ── Why this is not `streamMessage("Try again")` ───────────────────────
 * Because that is what caused the first User Beta incident. A turn created
 * a Room and a list, hit the tool-round ceiling before adding the items,
 * and the only route the product offered was typing into the composer.
 * "Try again" is a new question, and "again" means start over: the next
 * turn created a SECOND Room and a SECOND list.
 *
 * A resume adds no question. It names the interrupted turn, and the server
 * answers the ORIGINAL one again — this time knowing which entities the
 * first attempt already created. See chat/app/resume.go.
 */
export async function streamResume(
  conversationId: string,
  messageId: string,
  handlers: StreamHandlers,
  signal?: AbortSignal,
): Promise<void> {
  return streamTurn(
    `/chat/conversations/${conversationId}/resume`,
    { message_id: messageId },
    handlers,
    signal,
  );
}

/**
 * The transport both turn-producing routes share.
 *
 * One function because the SSE handling below is subtle — partial frames,
 * multi-byte boundaries, abort-versus-failure — and a second copy of it
 * would be where a resume quietly stopped delivering `done`, which is the
 * frame carrying the receipts.
 */
async function streamTurn(
  path: string,
  body: unknown,
  handlers: StreamHandlers,
  signal?: AbortSignal,
): Promise<void> {
  const res = await fetch(`${API_BASE}${path}`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "X-Workspace-Id": getApiWorkspaceId(),
      Accept: "text/event-stream",
    },
    body: JSON.stringify(body),
    signal,
  });

  if (!res.ok) {
    // Pre-stream failure: a normal JSON error body with a status code.
    const text = await res.text();
    let body: ApiErrorBody | null = null;
    try {
      const parsed: unknown = JSON.parse(text);
      if (parsed && typeof parsed === "object" && "error" in parsed) {
        body = parsed as ApiErrorBody;
      }
    } catch {
      /* non-JSON error body — fall through to the status text */
    }
    throw new ApiError(res.status, body, res.statusText);
  }

  if (!res.body) {
    throw new ApiError(0, null, "this browser cannot read streamed responses");
  }

  const reader = res.body.getReader();
  // `stream: true` is what makes multi-byte characters safe: a chunk
  // boundary can land in the middle of an "ã", and a stateless decoder
  // would emit a replacement character for each half.
  const decoder = new TextDecoder("utf-8");
  let buffer = "";

  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;

      buffer += decoder.decode(value, { stream: true });

      // Frames are separated by a blank line. The trailing element is
      // whatever arrived after the last separator — an incomplete frame
      // that stays buffered until the rest of it shows up.
      const frames = buffer.split("\n\n");
      buffer = frames.pop() ?? "";

      for (const frame of frames) {
        dispatch(frame, handlers);
      }
    }
    // A well-formed stream ends on a frame boundary, so the remainder is
    // normally empty. Flush it anyway rather than dropping a final event.
    if (buffer.trim()) dispatch(buffer, handlers);
  } catch (err) {
    // An abort is the user pressing stop, not a failure to report.
    if (signal?.aborted) return;
    throw err;
  } finally {
    reader.releaseLock();
  }
}

/* ── frame parsing ───────────────────────────────────────────────────── */

interface DeltaPayload {
  text: string;
}
interface DonePayload {
  message: ApiMessage | null;
  write_receipt?: WriteReceipt;
  read_receipt?: ReadReceipt;
}
type ErrorPayload = ApiErrorBody;

/** Parses one SSE frame and routes it to the matching handler. */
function dispatch(frame: string, handlers: StreamHandlers): void {
  let event = "message";
  const dataLines: string[] = [];

  for (const line of frame.split("\n")) {
    if (line.startsWith(":")) continue; // keep-alive comment
    if (line.startsWith("event:")) {
      event = line.slice(6).trim();
    } else if (line.startsWith("data:")) {
      dataLines.push(line.slice(5).trim());
    }
  }
  if (dataLines.length === 0) return;

  let payload: unknown;
  try {
    payload = JSON.parse(dataLines.join("\n"));
  } catch {
    return; // a frame we can't read shouldn't kill a working stream
  }

  switch (event) {
    case "reasoning":
      handlers.onReasoning((payload as DeltaPayload).text ?? "");
      break;
    case "delta":
      handlers.onDelta((payload as DeltaPayload).text ?? "");
      break;
    case "tool":
      handlers.onTool?.(payload as ToolFrame);
      break;
    case "done": {
      const done = payload as DonePayload;
      handlers.onDone(done.message ?? null, done.write_receipt, done.read_receipt);
      break;
    }
    case "error": {
      const body = payload as ErrorPayload;
      handlers.onError(new ApiError(502, body, "the stream failed"));
      break;
    }
  }
}
