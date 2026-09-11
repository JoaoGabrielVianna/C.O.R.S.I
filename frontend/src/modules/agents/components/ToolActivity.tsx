import { useState } from "react";
import { AlertTriangle, Check, ChevronDown, Loader2, Wrench } from "lucide-react";
import { cn } from "@/lib/utils";
import type { ApiMessage } from "@/modules/agents/api/conversations";
import { toolErrorLabel, turnToolCalls } from "@/modules/agents/contextReport";
import type { LiveToolCall } from "@/modules/agents/hooks/useChatStream";
import { useConversationToolCalls } from "@/modules/agents/hooks/useTools";
import { useT } from "@/lib/i18n";

/**
 * What the agent actually ran, in the transcript.
 *
 * ── Why a strip and not a message ──────────────────────────────────────
 * A tool call is not something the agent said. It is work it did on the way
 * to saying something, and rendering it as a bubble would put a machine
 * exchange into a reading of a conversation. So it sits above the answer as
 * one quiet line per call: what ran, and how it went.
 *
 * ── No raw JSON on the main surface ────────────────────────────────────
 * The arguments and the result are behind a disclosure, fetched only when
 * somebody opens one. The default reading of a thread should not contain a
 * serialised object.
 *
 * ── Two sources, deliberately not merged ───────────────────────────────
 * While the turn streams, the rows come from the live frames — the backend
 * is the only party that knows a tool is running, and without that the
 * reader sees a stalled stream. Once the turn is persisted they come from
 * the turn's own context report, which is the record. The live copy is
 * dropped at that point rather than reconciled: one fact, one source.
 */

/** The live strip, for the turn currently streaming. */
export function LiveToolActivity({ calls }: { calls: LiveToolCall[] }) {
  if (calls.length === 0) return null;
  return (
    <Strip>
      {calls.map((c) => (
        <Row
          key={c.callId}
          name={c.name}
          state={c.status}
          errorCode={c.errorCode}
          durationMs={c.durationMs}
        />
      ))}
    </Strip>
  );
}

/**
 * The persisted strip, for a turn that already finished.
 *
 * Renders nothing at all for a turn that ran no tools, which is every turn
 * an agent without tools has ever produced — and the query behind the
 * detail is not even issued for those.
 */
export function ToolActivity({ message }: { message: ApiMessage }) {
  const t = useT();
  const calls = turnToolCalls(message.context_report);
  const [openIndex, setOpenIndex] = useState<number | null>(null);

  // Issued only once somebody opens a detail. A thread that used tools
  // still costs nothing to read until the reader asks what a call carried.
  const detail = useConversationToolCalls(message.conversation_id, openIndex !== null);
  const records = (detail.data ?? []).filter((r) => r.message_id === message.id);

  if (calls.length === 0) return null;

  return (
    <Strip>
      {calls.map((c, i) => {
        const open = openIndex === i;
        const record = records[i];
        return (
          <div key={`${c.name}-${i}`}>
            <Row
              name={c.name}
              state={c.ok ? "ok" : "error"}
              errorCode={c.errorCode}
              durationMs={c.durationMS}
              open={open}
              onToggle={() => setOpenIndex(open ? null : i)}
            />
            {open ? (
              <div className="mt-1 space-y-1.5 pl-5">
                {detail.isLoading ? (
                  <p className="text-[11px] text-(--color-muted-foreground)">{t.app.modules.agents.common.loading}</p>
                ) : detail.isError ? (
                  <p className="text-[11px] text-(--color-destructive)">
                    {t.app.modules.agents.toolActivity.loadFailed}
                  </p>
                ) : !record ? (
                  <p className="text-[11px] text-(--color-muted-foreground)">
                    {t.app.modules.agents.toolActivity.noRecord}
                  </p>
                ) : (
                  <>
                    <Payload label={t.app.modules.agents.toolActivity.input} value={record.arguments} />
                    <Payload
                      label={record.status === "ok" ? "Resultado" : "Erro"}
                      value={record.status === "ok" ? record.result : record.error_message}
                    />
                  </>
                )}
              </div>
            ) : null}
          </div>
        );
      })}
    </Strip>
  );
}

function Strip({ children }: { children: React.ReactNode }) {
  return (
    <div className="mb-2 space-y-1 rounded-xl border border-(--color-border) bg-(--color-muted)/30 px-2.5 py-2">
      {children}
    </div>
  );
}

function Row({
  name,
  state,
  errorCode,
  durationMs,
  open,
  onToggle,
}: {
  name: string;
  /**
   * `not_executed` is a call the runtime refused before running it. It is
   * drawn like a failure on purpose: this strip answers "did the tool
   * work?", and both answers are no. The distinction between refused and
   * broken is carried by the error code beside it and by the turn''s write
   * receipt, which is where it changes what a person should do.
   */
  state: "running" | "ok" | "error" | "not_executed";
  errorCode?: string;
  durationMs?: number;
  open?: boolean;
  onToggle?: () => void;
}) {
  const failed = state === "error" || state === "not_executed";
  const Icon = state === "running" ? Loader2 : state === "ok" ? Check : AlertTriangle;
  const label =
    state === "running"
      ? "Consultando ferramenta…"
      : state === "ok"
        ? "Ferramenta concluída"
        : `Ferramenta recusada: ${toolErrorLabel(errorCode ?? "")}`;

  const body = (
    <span className="flex min-w-0 flex-1 items-center gap-1.5">
      <Wrench className="size-3 shrink-0 text-(--color-muted-foreground)" aria-hidden />
      <span className="truncate font-mono text-[10.5px] text-(--color-muted-foreground)">
        {name}
      </span>
      <span
        className={cn(
          "truncate text-[11px]",
          failed ? "text-(--color-destructive)" : "text-(--color-foreground)/70",
        )}
      >
        {label}
      </span>
      {typeof durationMs === "number" && state !== "running" ? (
        <span className="shrink-0 font-mono text-[10px] text-(--color-muted-foreground)">
          {durationMs}ms
        </span>
      ) : null}
      <Icon
        className={cn(
          "ml-auto size-3 shrink-0",
          state === "running" && "animate-spin text-(--color-muted-foreground)",
          state === "ok" && "text-(--color-brand-500)",
          failed && "text-(--color-destructive)",
        )}
        aria-hidden
      />
    </span>
  );

  if (!onToggle) {
    return <span className="flex items-center gap-1.5">{body}</span>;
  }
  return (
    <button
      type="button"
      onClick={onToggle}
      aria-expanded={open}
      className="flex w-full items-center gap-1.5 rounded-lg text-left transition-colors hover:bg-(--color-muted)/60 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-(--color-ring)/50"
    >
      <ChevronDown
        className={cn(
          "size-3 shrink-0 text-(--color-muted-foreground) transition-transform duration-200",
          open && "rotate-180",
        )}
        aria-hidden
      />
      {body}
    </button>
  );
}

/**
 * One payload, shown as the raw text it is.
 *
 * `null` is not "empty": it means the record does not retain it, which is
 * what a future redaction will write. Saying so is the difference between a
 * missing value and a removed one.
 */
function Payload({ label, value }: { label: string; value: string | null | undefined }) {
  const t = useT();
  return (
    <div>
      <span className="font-mono text-[9.5px] uppercase tracking-wider text-(--color-muted-foreground)">
        {label}
      </span>
      {value === null || value === undefined ? (
        <p className="text-[11px] italic text-(--color-muted-foreground)">{t.app.modules.agents.toolActivity.notRetained}</p>
      ) : (
        <pre className="mt-0.5 max-h-40 overflow-auto whitespace-pre-wrap break-all rounded-lg bg-(--color-card) px-2 py-1.5 font-mono text-[10.5px] text-(--color-foreground)/80">
          {value}
        </pre>
      )}
    </div>
  );
}
