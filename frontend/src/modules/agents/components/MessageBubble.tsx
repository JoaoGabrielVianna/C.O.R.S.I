import { memo, useEffect, useRef, useState } from "react";

import { namespaceOf, providerLabelFrom } from "@/modules/agents/toolGroups";
import {
  AlertTriangle,
  Brain,
  Check,
  ChevronRight,
  Copy,
  Gauge,
  Pencil,
  RefreshCw,
  Wrench,
  X,
} from "lucide-react";
import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/Button";
import type { ApiMessage } from "@/modules/agents/api/conversations";
import type { TurnPhase } from "@/modules/agents/hooks/useChatStream";
import { useCopy } from "@/modules/agents/hooks/useCopy";
import type { LiveToolCall } from "@/modules/agents/hooks/useChatStream";
import { LiveToolActivity, ToolActivity } from "./ToolActivity";
import { Markdown } from "./Markdown";
import { ExternalReadStrip } from "./ExternalReadStrip";
import { WriteReceiptStrip } from "./WriteReceiptStrip";
import { useT } from "@/lib/i18n";
import type { ReadReceipt, WriteReceipt } from "@/modules/agents/api/stream";

/**
 * One turn in the transcript.
 *
 * The user's turn is a contained bubble, the agent's runs full width. The
 * asymmetry is the point: a model answer is often long and reads better as
 * a document than as a speech balloon.
 */

/* ── user ────────────────────────────────────────────────────────────── */

export function UserMessage({
  message,
  onEdit,
  editable,
  onSaveToMemory,
  notice,
}: {
  message: Pick<ApiMessage, "content"> &
    Partial<Pick<ApiMessage, "seq" | "created_at" | "references">>;
  onEdit?: (text: string) => void;
  editable?: boolean;
  /** Opens the memory form with this turn's text. Absent while streaming. */
  onSaveToMemory?: (text: string) => void;
  /** The confirmation line, shown where the saving happened. */
  notice?: React.ReactNode;
}) {
  const t = useT();
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(message.content);
  const textareaRef = useRef<HTMLTextAreaElement>(null);

  useEffect(() => {
    if (!editing) return;
    const el = textareaRef.current;
    if (!el) return;
    el.focus();
    // Cursor at the end, and sized to the content rather than a fixed rows.
    el.setSelectionRange(el.value.length, el.value.length);
    el.style.height = "0px";
    el.style.height = `${Math.min(el.scrollHeight, 240)}px`;
  }, [editing]);

  if (editing) {
    return (
      <div className="flex justify-end">
        <div className="w-full max-w-[min(92%,48rem)] rounded-2xl border border-(--color-brand-500) bg-(--color-card) p-2.5 shadow-(--shadow-card)">
          <textarea
            ref={textareaRef}
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Escape") setEditing(false);
              if (e.key === "Enter" && !e.shiftKey) {
                e.preventDefault();
                if (draft.trim()) onEdit?.(draft.trim());
                setEditing(false);
              }
            }}
            className="max-h-60 w-full resize-none bg-transparent px-1 text-[13.5px] leading-[1.7] text-(--color-foreground) outline-none"
          />
          <div className="mt-1.5 flex items-center justify-end gap-1.5">
            <span className="mr-auto pl-1 text-[10.5px] text-(--color-muted-foreground)">
              {t.app.modules.agents.message.resendWarning}
            </span>
            <Button
              size="sm"
              variant="ghost"
              onClick={() => {
                setDraft(message.content);
                setEditing(false);
              }}
            >
              <X />
              {t.app.modules.agents.message.cancel}
            </Button>
            <Button
              size="sm"
              disabled={!draft.trim() || draft.trim() === message.content}
              onClick={() => {
                onEdit?.(draft.trim());
                setEditing(false);
              }}
            >
              {t.app.modules.agents.message.resend}
            </Button>
          </div>
        </div>
      </div>
    );
  }

  return (
    <div className="group">
      <div className="flex justify-end gap-2">
        {onSaveToMemory ? (
          <HoverAction
            label={t.app.modules.agents.message.saveToMemory}
            icon={Brain}
            onClick={() => onSaveToMemory(message.content)}
            className="self-center"
          />
        ) : null}
        {editable && onEdit ? (
          <HoverAction
            label={t.app.modules.agents.message.editAndResend}
            icon={Pencil}
            onClick={() => {
              setDraft(message.content);
              setEditing(true);
            }}
            className="self-center"
          />
        ) : null}
        <div className="max-w-[min(85%,44rem)] rounded-2xl rounded-br-md bg-(--color-accent) px-3.5 py-2 text-(--color-accent-foreground) shadow-(--shadow-card)">
          <p className="whitespace-pre-wrap break-words text-[13.5px] leading-[1.7]">
            {message.content}
          </p>
        </div>
      </div>
      <SelectedReferences references={message.references} />
      {notice ? <div className="mt-1 flex justify-end">{notice}</div> : null}
    </div>
  );
}

/**
 * What the user attached to this turn.
 *
 * ── Why it is visually unlike the tool activity on the answer ──────────
 * Because they are different facts, and a reader who confuses them draws a
 * false conclusion. This says "I made these available"; the badges under
 * the agent's reply say "it ran these". A turn can attach a capability the
 * model never reaches for, and the transcript has to be able to show that
 * without implying it was used.
 *
 * So this sits on the question, quiet and outlined, and ToolActivity sits
 * on the answer with an outcome and a duration. Neither one is derived from
 * the other.
 *
 * ── Why the label is rendered as stored ────────────────────────────────
 * It is a snapshot. Looking the name up in today's catalogue would make a
 * turn from last week describe a tool that has since been renamed, or make
 * a revoked one vanish from a turn it was demonstrably part of. The stored
 * words are what was true; the catalogue is what is true now.
 */
function SelectedReferences({ references }: { references?: ApiMessage["references"] }) {
  if (!references || references.length === 0) return null;

  // Grouped for display, exactly as the menu presents them: the person
  // attached GitHub, not seven capabilities, and the transcript has to read
  // back the way the choice was made.
  //
  // What is STORED stays one row per capability, and that is the point —
  // the tooltip lists them, so the turn's real scope is recoverable here
  // and not only in the database. A turn from before an eighth capability
  // shipped goes on naming the seven it actually had.
  const groups = new Map<string, { label: string; ids: string[] }>();
  for (const r of references) {
    const key = namespaceOf(r.id);
    const group = groups.get(key) ?? { label: providerLabelFrom(r.label, key), ids: [] };
    group.ids.push(r.id);
    groups.set(key, group);
  }

  return (
    <ul className="mt-1 flex flex-wrap justify-end gap-1">
      {[...groups].map(([key, group]) => (
        <li
          key={key}
          title={group.ids.join("\n")}
          className="inline-flex items-center gap-1 rounded-full border border-(--color-border) px-1.5 py-0.5 text-[10.5px] text-(--color-muted-foreground)"
        >
          <Wrench className="size-2.5 shrink-0" />
          {group.label}
          {group.ids.length > 1 ? (
            <span className="tabular-nums opacity-60">{group.ids.length}</span>
          ) : null}
        </li>
      ))}
    </ul>
  );
}

/* ── assistant ───────────────────────────────────────────────────────── */

export function AssistantMessage({
  content,
  reasoning,
  reasoningMs,
  agentName,
  message,
  phase,
  elapsedMs,
  onRegenerate,
  onSaveToMemory,
  onInspect,
  notice,
  liveTools,
  receipt,
  readReceipt,
}: {
  content: string;
  reasoning?: string;
  reasoningMs?: number;
  agentName?: string;
  message?: ApiMessage;
  /**
   * Tool calls of the turn currently streaming. Only the live bubble passes
   * them; a persisted turn reads its own from the message.
   */
  liveTools?: LiveToolCall[];
  /**
   * What the SYSTEM knows this turn changed. Rendered instead of inferring
   * anything from `content`: a model once reported an import that never
   * happened, and the words were the only thing on screen.
   */
  receipt?: WriteReceipt;
  readReceipt?: ReadReceipt;
  /** Set only on the turn currently streaming. */
  phase?: TurnPhase;
  elapsedMs?: number;
  onRegenerate?: () => void;
  /** Opens the memory form with this answer's text. Absent while streaming. */
  onSaveToMemory?: (text: string) => void;
  /** Opens the context inspector for this turn. Absent while streaming. */
  onInspect?: () => void;
  /** The confirmation line, shown where the saving happened. */
  notice?: React.ReactNode;
}) {
  const t = useT();
  const streaming = phase !== undefined && phase !== "idle" && phase !== "error";
  const { copied, copy } = useCopy(content);

  const failed = Boolean(message?.error);
  const aborted = message?.finish_reason === "aborted";
  const truncated = message?.finish_reason === "length";
  const hasReasoning = Boolean(reasoning?.trim());

  return (
    <div className="group">
      <div className="mb-1 flex items-center gap-2">
        <span className="text-[11.5px] font-medium text-(--color-foreground)">
          {agentName ?? "Agente"}
        </span>
        {message?.model ? (
          <span className="font-mono text-[10px] text-(--color-muted-foreground)">
            {message.model}
          </span>
        ) : null}

        {/* Actions sit in the header row so they never overlap the text and
            never shift it: the row exists whether or not they are shown. */}
        {!streaming && content ? (
          <div className="ml-auto flex items-center gap-0.5 opacity-0 transition-opacity duration-200 group-hover:opacity-100 focus-within:opacity-100">
            <HoverAction
              label={copied ? "Copiado" : "Copiar resposta"}
              icon={copied ? Check : Copy}
              onClick={copy}
            />
            {onSaveToMemory ? (
              <HoverAction
                label={t.app.modules.agents.message.saveToMemory}
                icon={Brain}
                onClick={() => onSaveToMemory(content)}
              />
            ) : null}
            {onInspect ? (
              <HoverAction label={t.app.modules.agents.message.seeContext} icon={Gauge} onClick={onInspect} />
            ) : null}
            {onRegenerate ? (
              <HoverAction label={t.app.modules.agents.message.regenerate} icon={RefreshCw} onClick={onRegenerate} />
            ) : null}
          </div>
        ) : null}
      </div>

      {/* Above the answer, because that is when it happened: the agent ran
          this on the way to what it is about to say. Two sources, never
          merged — the live frames while the turn is in flight, the turn's
          own record afterwards. */}
      {streaming ? (
        <LiveToolActivity calls={liveTools ?? []} />
      ) : message ? (
        <ToolActivity message={message} />
      ) : null}

      {hasReasoning || phase === "reasoning" ? (
        <ReasoningBlock
          text={reasoning ?? ""}
          durationMs={reasoningMs}
          live={phase === "reasoning"}
          elapsedMs={elapsedMs}
        />
      ) : null}

      {/* Before the first token there is nothing to read, so the state
          itself is the content. `sending` means the request is out and the
          model has not said anything at all yet. */}
      {phase === "sending" ? <ThinkingLabel label={t.app.modules.agents.message.connecting} elapsedMs={elapsedMs} /> : null}

      {/* Below the answer, because it is the answer being qualified: what
          the model said, and then what the system knows actually happened.
          It reads only the receipt — there is no path here through which
          the text above can produce a confirmation. */}
      {content ? (
        <div className="text-(--color-foreground)">
          <Markdown text={content} />
          {phase === "writing" ? <Caret /> : null}
          {!streaming ? <WriteReceiptStrip receipt={receipt} /> : null}
          {/* The read-side twin. Same rule: it reads only the receipt, so
              no sentence can produce a verification. Unlike the write
              strip it renders the ABSENCE of evidence too — that is the
              state the invented follower count was produced in. */}
          {!streaming ? <ExternalReadStrip receipt={readReceipt} /> : null}
        </div>
      ) : phase === "writing" ? (
        <Caret />
      ) : null}

      {aborted ? <Notice>{t.app.modules.agents.message.interrupted}</Notice> : null}
      {truncated ? <Notice>{t.app.modules.agents.message.truncated}</Notice> : null}
      {failed ? (
        <div className="mt-2 flex items-start gap-2 rounded-xl border border-(--color-destructive)/30 bg-(--color-destructive)/5 px-3 py-2">
          <AlertTriangle className="mt-0.5 size-3.5 shrink-0 text-(--color-destructive)" />
          <p className="text-[11.5px] leading-relaxed text-(--color-destructive)">
            {message?.error}
          </p>
        </div>
      ) : null}

      {notice ? <div className="mt-1.5">{notice}</div> : null}

      {message && message.completion_tokens > 0 ? (
        <p className="mt-1.5 font-mono text-[10px] text-(--color-muted-foreground) opacity-0 transition-opacity duration-200 group-hover:opacity-100">
          {message.prompt_tokens} in · {message.completion_tokens} out
        </p>
      ) : null}
    </div>
  );
}

/**
 * "Saved to memory", said where the saving happened.
 *
 * Discreet and local rather than a global toast: the act belonged to one
 * message, and a banner at the top of the window would make the reader look
 * away from the thing they just acted on to be told about it.
 */
export function MemorySavedNotice({
  onOpen,
  extra,
}: {
  onOpen: () => void;
  /** Extra sentence for the /lembrar path, where nothing was sent. */
  extra?: string;
}) {
  return (
    <p className="inline-flex flex-wrap items-center gap-1.5 text-[11px] text-(--color-muted-foreground)">
      <Brain className="size-3 shrink-0" />
      Salvo na memória.
      {extra ? <span>{extra}</span> : null}
      <button
        type="button"
        onClick={onOpen}
        className="rounded-md underline underline-offset-2 transition-colors hover:text-(--color-foreground)"
      >
        ver
      </button>
    </p>
  );
}

/* ── reasoning ───────────────────────────────────────────────────────── */

/**
 * The model's chain of thought, collapsed by default.
 *
 * Open while it is being written, because watching the model work is the
 * whole appeal; collapsed once the answer starts, because by then the
 * answer is what you came for. Re-opening is one click and the text is
 * kept, so nothing is lost by the auto-collapse.
 */
const ReasoningBlock = memo(function ReasoningBlock({
  text,
  durationMs,
  live,
  elapsedMs,
}: {
  text: string;
  durationMs?: number;
  live?: boolean;
  elapsedMs?: number;
}) {
  const [manuallyOpen, setManuallyOpen] = useState<boolean | null>(null);
  const scrollRef = useRef<HTMLDivElement>(null);

  // Open while live unless the user explicitly closed it.
  const open = manuallyOpen ?? Boolean(live);

  // Follow the thought as it is written.
  useEffect(() => {
    if (!open || !live) return;
    const el = scrollRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [text, open, live]);

  const seconds = durationMs ? durationMs / 1000 : elapsedMs ? elapsedMs / 1000 : 0;

  return (
    <div className="mb-2">
      <button
        type="button"
        onClick={() => setManuallyOpen(!open)}
        className={cn(
          "inline-flex items-center gap-1.5 rounded-lg py-0.5 pr-2 text-[11.5px]",
          "text-(--color-muted-foreground) transition-colors hover:text-(--color-foreground)",
        )}
      >
        <ChevronRight
          className={cn("size-3 transition-transform duration-200", open && "rotate-90")}
        />
        <Brain className="size-3" />
        {live ? (
          <span className="text-shimmer font-medium">
            Pensando{seconds >= 1 ? ` · ${seconds.toFixed(1)}s` : "…"}
          </span>
        ) : (
          <span>Pensou por {seconds.toFixed(1)}s</span>
        )}
      </button>

      {open ? (
        <div
          ref={scrollRef}
          className={cn(
            "mt-1.5 max-h-56 overflow-y-auto rounded-xl border border-(--color-border)",
            "bg-(--color-muted)/50 px-3 py-2",
          )}
        >
          <p className="whitespace-pre-wrap break-words text-[12px] leading-[1.65] text-(--color-muted-foreground)">
            {text}
            {live ? <Caret subtle /> : null}
          </p>
        </div>
      ) : null}
    </div>
  );
});

/* ── small parts ─────────────────────────────────────────────────────── */

function ThinkingLabel({ label, elapsedMs }: { label: string; elapsedMs?: number }) {
  const seconds = (elapsedMs ?? 0) / 1000;
  return (
    <div className="flex items-center gap-2 py-0.5">
      <span className="text-shimmer text-[13px] font-medium">{label}</span>
      {seconds >= 1 ? (
        <span className="font-mono text-[10px] text-(--color-muted-foreground)">
          {seconds.toFixed(1)}s
        </span>
      ) : null}
    </div>
  );
}

function HoverAction({
  label,
  icon: Icon,
  onClick,
  className,
}: {
  label: string;
  icon: React.ComponentType<React.SVGProps<SVGSVGElement>>;
  onClick: () => void;
  className?: string;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      title={label}
      aria-label={label}
      className={cn(
        "rounded-lg p-1.5 text-(--color-muted-foreground) transition-colors",
        "hover:bg-(--color-muted) hover:text-(--color-foreground)",
        "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-(--color-ring)/50",
        "opacity-0 group-hover:opacity-100 focus-visible:opacity-100",
        className,
      )}
    >
      <Icon className="size-3.5" />
    </button>
  );
}

function Notice({ children }: { children: React.ReactNode }) {
  return <p className="mt-2 text-[11.5px] text-(--color-muted-foreground)">{children}</p>;
}

/** Blinking caret shown while tokens are still arriving. */
function Caret({ subtle }: { subtle?: boolean }) {
  return (
    <span
      className={cn(
        "ml-0.5 inline-block h-[1em] w-[2px] translate-y-[0.15em] animate-pulse",
        subtle ? "bg-(--color-muted-foreground)" : "bg-(--color-accent)",
      )}
    />
  );
}
