import {
  useCallback,
  useEffect,
  useImperativeHandle,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import {
  AlertTriangle,
  ArrowDown,
  Bot,
  Columns2,
  FoldHorizontal,
  PanelLeft,
  UnfoldHorizontal,
  X,
} from "lucide-react";
import { useNavigate } from "react-router-dom";
import { Button } from "@/components/ui/Button";
import { ApiError } from "@/lib/api/client";
import { cn } from "@/lib/utils";
import type { ApiAgent } from "@/modules/agents/api/agents";
import type { ApiConversation, ApiMessage } from "@/modules/agents/api/conversations";
import { readRememberCommand, rememberIntentHint } from "@/modules/agents/memoryCapture";
import type { ComposerCommand } from "@/modules/agents/composer/commands";
import {
  referencesFromTools,
  type ComposerReference,
} from "@/modules/agents/composer/references";
import { ContextReferenceList } from "./ContextReferenceCard";
import { ModelBadge } from "./ModelBadge";
import { useChatStream } from "@/modules/agents/hooks/useChatStream";
import { useChatWidth } from "@/modules/agents/hooks/useChatWidth";
import { useMessages } from "@/modules/agents/hooks/useConversations";
import { useCreateMemory } from "@/modules/agents/hooks/useMemories";
import { useConsolidation } from "@/modules/agents/hooks/useConsolidation";
import { useAgentTools } from "@/modules/agents/hooks/useTools";
import { Composer } from "./Composer";
import { BudgetRefusal } from "./budget/BudgetRefusal";
import { isBudgetRefusal } from "./budget/budgetRefusalReason";
import { ContextInspector } from "./ContextInspector";
import { MemoryDialog } from "./memory/MemoryDialog";
import { MemoryCandidatesDialog } from "./memory/MemoryCandidatesDialog";
import { AssistantMessage, MemorySavedNotice, UserMessage } from "./MessageBubble";
import { useT } from "@/lib/i18n";

/**
 * The thread itself: transcript, in-flight turn, composer.
 *
 * ── Scroll behaviour ───────────────────────────────────────────────────
 * The view follows new text only while the reader is already at the
 * bottom. Scrolling up to re-read something is an explicit act, and
 * yanking the viewport back down mid-sentence because another token
 * arrived is the single most irritating thing a chat UI can do. When the
 * reader is away from the bottom and an answer is being written, the
 * jump-to-bottom button says so rather than appearing silently.
 */

/**
 * What a pane exposes to the page.
 *
 * Only needed by compare mode, where one composer drives every column: the
 * page holds a ref per pane and fans a single message out across them. In
 * normal mode nothing reads this.
 */
export interface ChatViewHandle {
  send: (text: string) => void;
  stop: () => void;
}

interface Props {
  conversation: ApiConversation;
  agent: ApiAgent | undefined;
  /** Opens the thread drawer. Only rendered below `lg`, where the thread
   *  column does not fit and lives behind this button instead. */
  onOpenThreads?: () => void;
  ref?: React.Ref<ChatViewHandle>;

  /* ── multi-column ─────────────────────────────────────────────────── */

  /** False in compare mode, where the shared composer replaces this one. */
  showComposer?: boolean;
  /** Reports whether a turn is in flight, so the shared composer can
   *  switch to "stop" while any column is still answering. */
  onBusyChange?: (busy: boolean) => void;
  /** Present only when more than one column is open. */
  onClose?: () => void;
  /** Present only on the last column, and only when another one fits. */
  onAddPane?: () => void;
  /** Called when this column should become the one sidebar clicks act on. */
  onFocus?: () => void;
  /** Hidden when columns are open: a narrow column has no width to give. */
  showWidthToggle?: boolean;
}

/** How close to the bottom still counts as "at the bottom", in pixels. */
const STICK_THRESHOLD_PX = 80;

export function ChatView({
  conversation,
  agent,
  onOpenThreads,
  ref,
  showComposer = true,
  onBusyChange,
  onClose,
  onAddPane,
  onFocus,
  showWidthToggle = true,
}: Props) {
  const t = useT();
  const messagesQuery = useMessages(conversation.id);
  const {
    pendingUserMessage,
    reasoningText,
    streamingText,
    liveTools,
    phase,
    elapsedMs,
    error,
    isStreaming,
    send,
    resend,
    resume,
    stop,
    clearError,
  } = useChatStream(conversation.id);

  const navigate = useNavigate();
  const createMemory = useCreateMemory(agent?.id ?? "");
  /**
   * The memory form, and where it was opened from.
   *
   * `anchor` is the message the action came from, or "composer" for
   * /lembrar. It is what lets the confirmation appear in the place the act
   * happened rather than as a banner somewhere else on screen.
   */
  const [memoryDraft, setMemoryDraft] = useState<{ content: string; anchor: string } | null>(null);
  const [memoryError, setMemoryError] = useState<string | null>(null);
  const [savedAt, setSavedAt] = useState<{ anchor: string; fromCommand: boolean } | null>(null);
  /**
   * The turn whose context is open in the inspector, held by id rather than
   * by value: the transcript refetches, and a held object would go on
   * describing a turn that has since been regenerated or truncated.
   */
  const [inspectingId, setInspectingId] = useState<string | null>(null);

  /**
   * The consolidation flow, which is a dialog and not a turn.
   *
   * It lives beside the transcript rather than inside it: `/lembrar` asks a
   * question ABOUT the conversation, and an answer that appeared as a
   * message in the conversation would be the first thing the next
   * consolidation read.
   */
  const consolidation = useConsolidation(conversation.id, agent?.id);

  /**
   * What `@` can offer in this thread.
   *
   * One read, when the thread opens, from the endpoint that is already the
   * authority on the question — not a second catalogue, and not a request
   * per keystroke. The cache is shared with the Tools settings page, so
   * granting a tool there and typing `@` here do not disagree.
   *
   * `authorized_count` is deliberately not consulted: the menu shows the
   * authorized rows, and the count is a number for a different surface.
   */
  const toolsQuery = useAgentTools(agent?.id ?? "");
  const references = useMemo<ComposerReference[]>(
    () => referencesFromTools(toolsQuery.data),
    [toolsQuery.data],
  );

  const scrollRef = useRef<HTMLDivElement>(null);
  const [stuckToBottom, setStuckToBottom] = useState(true);
  const [width, setWidth] = useChatWidth();
  // Focus keeps a bounded reading column; wide hands the whole width over.
  // See useChatWidth for why this is a choice rather than a constant.
  //
  // Columns are already narrow, so the preference only applies to a single
  // pane; side by side they always use their full share.
  const wide = width === "wide" || !showWidthToggle;
  const measure = wide ? "max-w-none" : "max-w-3xl";

  useImperativeHandle(ref, () => ({ send: (text) => void send(text), stop }), [send, stop]);

  // Tell the page when this column starts and stops answering. The shared
  // composer needs it to know whether to offer send or stop.
  useEffect(() => {
    onBusyChange?.(isStreaming);
  }, [isStreaming, onBusyChange]);

  const messages = useMemo(() => messagesQuery.data?.items ?? [], [messagesQuery.data]);

  /**
   * The last turn, when it stopped with work already done.
   *
   * ── Why this exists ────────────────────────────────────────────────
   * The first User Beta incident. A turn created a Room and a list, hit the
   * tool-round ceiling before adding the items, and the error banner
   * offered only "close" — so the only way forward was typing into the
   * composer. The user typed "Try again", which is a NEW QUESTION meaning
   * start over, and the next turn created a second Room and a second list.
   *
   * Offering the continuation explicitly is what removes that choice.
   */
  const resumable = useMemo(() => {
    const last = messages[messages.length - 1];
    if (!last || last.role !== "assistant") return null;
    // Matching the server's ResumableFinish exactly. Anything else it
    // refuses, and an affordance that offers a refusal is worse than none.
    //
    // `deadline` joined the ceiling in R2. It is the turn nobody stopped:
    // a clock ended it while it was working, which is how the operator
    // ended up typing "Resposta interrompida. continue" into the composer
    // — the one route this banner exists to replace.
    if (last.finish_reason === "tool_round_limit") return last;
    if (last.finish_reason === "deadline") return last;
    return null;
  }, [messages]);

  /**
   * What the system knows each turn changed, keyed by message id.
   *
   * Read straight from the transcript response rather than derived from
   * anything on screen. A turn that only CLAIMED a change has a receipt
   * saying nothing ran, and that is what the bubble renders against.
   */
  const readReceipts = useMemo(
    () => messagesQuery.data?.read_receipts ?? {},
    [messagesQuery.data],
  );
  const receipts = useMemo(
    () => messagesQuery.data?.write_receipts ?? {},
    [messagesQuery.data],
  );

  // The server returns the trailing N turns and reports the N it applied.
  // A transcript that came back exactly that long was cut, and older turns
  // exist above it — which the reader has to be told, or the thread simply
  // appears to have begun there.
  const transcriptCut =
    messagesQuery.data !== undefined && messages.length >= messagesQuery.data.limit;

  // The last user turn is what regenerate replays. Regenerating means
  // deleting from that turn and asking again with the same text, so the
  // thread ends up with one question and one fresh answer.
  const lastUserMessage = useMemo(() => findLastUser(messages), [messages]);
  const lastMessage = messages.at(-1);
  const canRegenerate =
    !isStreaming && lastUserMessage !== undefined && lastMessage?.role === "assistant";

  const scrollToBottom = useCallback((behavior: ScrollBehavior = "smooth") => {
    const el = scrollRef.current;
    if (el) el.scrollTo({ top: el.scrollHeight, behavior });
  }, []);

  const onScroll = useCallback(() => {
    const el = scrollRef.current;
    if (!el) return;
    const distance = el.scrollHeight - el.scrollTop - el.clientHeight;
    setStuckToBottom(distance <= STICK_THRESHOLD_PX);
  }, []);

  // Follow the stream. useLayoutEffect so the scroll lands in the same
  // frame the new text paints, instead of one frame behind it.
  //
  // This also covers arriving at a thread: the page mounts this component
  // with a `key` of the conversation id, so switching threads remounts it
  // and lands here with `stuckToBottom` back at its initial `true`.
  useLayoutEffect(() => {
    if (stuckToBottom) scrollToBottom("auto");
  }, [streamingText, reasoningText, pendingUserMessage, messages, stuckToBottom, scrollToBottom]);

  const isEmpty = messages.length === 0 && !pendingUserMessage;

  /**
   * The turn the inspector is describing, resolved against the transcript
   * as it is now. A turn that was regenerated away simply stops being
   * found, and the panel closes rather than explaining something that is no
   * longer on screen.
   */
  const inspected = inspectingId ? messages.find((m) => m.id === inspectingId) : undefined;

  /**
   * Writes the memory and confirms it where the act happened.
   *
   * Provenance is always this conversation: both capture mechanisms live
   * inside a thread, and that is the answer to "why does it know this?"
   * later on. Nothing here calls the model.
   */
  const saveMemory = useCallback(
    (content: string, anchor: string, fromCommand: boolean) => {
      setMemoryError(null);
      createMemory.mutate(
        { content, source_conversation_id: conversation.id },
        {
          onSuccess: () => {
            setMemoryDraft(null);
            setSavedAt({ anchor, fromCommand });
          },
          // The dialog stays open on failure and keeps the text; when the
          // save came from /lembrar there is no dialog, so the same message
          // is shown above the composer. Either way the words are not lost.
          onError: (err) =>
            setMemoryError(err instanceof ApiError ? err.message : String(err)),
        },
      );
    },
    [conversation.id, createMemory],
  );

  // The confirmation is a note, not a state: it fades on its own so the
  // transcript does not accumulate a trail of things that already happened.
  useEffect(() => {
    if (!savedAt) return;
    const id = window.setTimeout(() => setSavedAt(null), 8000);
    return () => window.clearTimeout(id);
  }, [savedAt]);

  /**
   * `/lembrar` is intercepted here, before it can become a turn.
   *
   * The whole promise of the command is that it costs nothing: no request
   * to the provider, no tokens, no waiting. Which means the interception
   * has to happen on this side of `send`, and the confirmation has to say
   * out loud that the agent was not asked anything — otherwise the silence
   * reads as a failure.
   *
   * A draft that *is* the command never becomes a turn, complete or not.
   * The incomplete one used to fall through to `send`, so the agent was
   * asked to answer `/lembrar` and said, correctly, that it cannot save
   * anything: the command appeared not to exist. Returning false leaves the
   * text in the composer, where the hint already says what is missing.
   */
  const handleSend = useCallback(
    // A switch over every case with a declared `boolean` return, rather
    // than an if that falls through: deleting a branch is then a compile
    // error instead of a silent decision to send the command after all.
    (text: string, references: ComposerReference[]): boolean => {
      const command = readRememberCommand(text);
      switch (command.kind) {
        // Not a turn, and not a capture either: a question about the
        // conversation so far. The composer is cleared because the command
        // was accepted — what happens next is a dialog, not a message.
        case "consolidate":
          if (!agent) return false;
          consolidation.start(lastSeqOf(messages));
          return true;
        case "capture":
          // Without an agent there is nothing to save to, and sending it
          // instead would be the very leak this branch exists to stop.
          if (agent) saveMemory(command.content, "composer", true);
          return agent !== undefined;
        case "none":
          // The only branch that becomes a turn, and therefore the only one
          // an attachment means anything to. `/lembrar` never reaches the
          // model at all, so scoping a turn that does not happen would be
          // meaningless — the references simply travel with the message
          // that is actually sent.
          void send(
            text,
            references.map((r) => ({ kind: r.kind, id: r.id })),
          );
          return true;
      }
    },
    [agent, consolidation, messages, saveMemory, send],
  );

  /**
   * Runs a command chosen from the composer's menu.
   *
   * ── Why the dispatch lives here and not in the registry ────────────────
   * Because this is where the flows are. `consolidation` is a hook held by
   * this component; a registry entry carrying a closure over it would have
   * to be built during render, in a module whose job is to describe a menu.
   *
   * The switch is exhaustive over `ComposerCommandId`, so registering a
   * command and forgetting to run it is a compile error rather than a menu
   * entry that quietly does nothing.
   *
   * Selecting a command with no required argument RUNS it. The row is the
   * whole action; typing the trigger and then pressing Enter again to
   * confirm would be asking twice for one decision.
   */
  const runCommand = useCallback(
    (command: ComposerCommand) => {
      switch (command.id) {
        case "memory.consolidate":
          // The same flow the typed command reaches. There is one
          // consolidation in this component, and the menu is another door
          // to it rather than a second implementation of it.
          if (agent) consolidation.start(lastSeqOf(messages));
          return;
      }
    },
    [agent, consolidation, messages],
  );

  const openMemoryDraft = useCallback((content: string, anchor: string) => {
    setMemoryError(null);
    setMemoryDraft({ content, anchor });
  }, []);

  const openMemoryPage = useCallback(() => {
    if (agent) navigate(`/app/modules/agents/${agent.id}/memory`);
  }, [agent, navigate]);

  const noticeFor = (anchor: string) =>
    savedAt?.anchor === anchor ? (
      <MemorySavedNotice
        onOpen={openMemoryPage}
        extra={savedAt.fromCommand ? "Nada foi enviado ao agente." : undefined}
      />
    ) : null;

  return (
    // flex-1 rather than h-full: this is a flex child of the chat section,
    // so it claims the leftover height directly instead of resolving a
    // percentage against an ancestor.
    <div
      // Clicking anywhere in a column makes it the one the sidebar acts on.
      // onFocusCapture covers keyboard arrival too, without stealing the
      // click from whatever was actually pressed.
      onMouseDown={onFocus}
      onFocusCapture={onFocus}
      className="flex min-h-0 flex-1 flex-col"
    >
      {/* The rule stretches edge to edge; its contents line up with the
          transcript. Everything in the thread shares one measure, so
          switching to focus does not leave the composer and the header
          hanging off at the far edges of an ultrawide. */}
      <header className="shrink-0 border-b border-(--color-border) pb-2.5">
        <div className={cn("mx-auto flex w-full items-center gap-2.5 px-1", measure)}>
          {onOpenThreads ? (
            <button
              type="button"
              onClick={onOpenThreads}
              aria-label={t.app.modules.agents.chat.openThreads}
              className="-ml-1 rounded-lg p-1.5 text-(--color-muted-foreground) transition-colors hover:bg-(--color-muted) hover:text-(--color-foreground) lg:hidden"
            >
              <PanelLeft className="size-4" />
            </button>
          ) : null}
          <div className="flex size-7 shrink-0 items-center justify-center rounded-lg bg-(--color-brand-50) text-(--color-brand-700)">
            <Bot className="size-3.5" />
          </div>
          <div className="min-w-0 flex-1">
            <p className="truncate text-[13px] font-medium text-(--color-foreground)">
              {conversation.title || "Nova conversa"}
            </p>
            {/* Who is answering, and on what. Discreet on purpose: the
                agent already owns the page around this, and a chat does not
                need its parameters restated above every reply. */}
            {agent ? (
              <div className="mt-0.5 flex min-w-0 items-center gap-1.5">
                <span className="truncate text-[10.5px] text-(--color-muted-foreground)">
                  {agent.name}
                </span>
                <ModelBadge model={agent.model} />
              </div>
            ) : (
              <p className="truncate font-mono text-[10px] text-(--color-muted-foreground)">
                {t.app.modules.agents.chat.agentUnavailable}
              </p>
            )}
          </div>
          {messages.length > 0 ? (
            <span className="shrink-0 font-mono text-[10px] text-(--color-muted-foreground)">
              {messages.length} msgs
            </span>
          ) : null}

          {/* Hidden on small screens: there is no spare width to give, so the
            toggle would be a control that visibly does nothing. */}
          {showWidthToggle ? (
            <button
              type="button"
              onClick={() => setWidth(wide ? "focus" : "wide")}
              title={wide ? "Coluna de leitura" : "Usar toda a largura"}
              aria-label={wide ? "Coluna de leitura" : "Usar toda a largura"}
              aria-pressed={wide}
              className={cn(
                "hidden shrink-0 rounded-lg p-1.5 transition-colors md:inline-flex",
                "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-(--color-ring)/50",
                wide
                  ? "bg-(--color-muted) text-(--color-foreground)"
                  : "text-(--color-muted-foreground) hover:bg-(--color-muted) hover:text-(--color-foreground)",
              )}
            >
              {wide ? (
                <FoldHorizontal className="size-4" />
              ) : (
                <UnfoldHorizontal className="size-4" />
              )}
            </button>
          ) : null}

          {onAddPane ? (
            <HeaderAction label={t.app.modules.agents.chat.openBeside} icon={Columns2} onClick={onAddPane} />
          ) : null}
          {onClose ? <HeaderAction label={t.app.modules.agents.chat.closeColumn} icon={X} onClick={onClose} /> : null}
        </div>

        {/* What this thread is about, when it was opened from an entity.
            In the header rather than in the transcript because it is a
            property of the whole conversation, not of one turn — and
            because it has to stay visible on the fourth message, which is
            exactly when "essa vaga" needs to still mean something.

            Renders nothing for a thread started from the composer. */}
        <ContextReferenceList
          references={conversation.context_references}
          className="mt-2.5"
        />
      </header>

      <div className="relative min-h-0 flex-1">
        <div
          ref={scrollRef}
          onScroll={onScroll}
          className="absolute inset-0 overflow-y-auto px-1 py-4"
        >
          {isEmpty ? (
            <EmptyThread agentName={agent?.name} />
          ) : (
            <div className={cn("mx-auto w-full space-y-5", measure)}>
              {transcriptCut ? (
                <p className="rounded-lg border border-dashed border-(--color-border) px-3 py-2 text-center text-[11px] leading-relaxed text-(--color-muted-foreground)">
                  {t.app.modules.agents.interp.chatTruncated.replace(
                    "{count}",
                    String(messages.length),
                  )}
                </p>
              ) : null}
              {messages.map((m) =>
                m.role === "user" ? (
                  <UserMessage
                    key={m.id}
                    message={m}
                    editable={!isStreaming}
                    onEdit={(text) => void resend(m.seq, text)}
                    onSaveToMemory={
                      agent && !isStreaming
                        ? (text) => openMemoryDraft(text, m.id)
                        : undefined
                    }
                    notice={noticeFor(m.id)}
                  />
                ) : (
                  <AssistantMessage
                    key={m.id}
                    content={m.content}
                    reasoning={m.reasoning}
                    reasoningMs={m.reasoning_ms}
                    agentName={agent?.name}
                    message={m}
                    receipt={receipts[m.id]}
                    readReceipt={readReceipts[m.id]}
                    onRegenerate={
                      canRegenerate && m.id === lastMessage?.id && lastUserMessage
                        ? () => void resend(lastUserMessage.seq, lastUserMessage.content)
                        : undefined
                    }
                    onSaveToMemory={agent ? (text) => openMemoryDraft(text, m.id) : undefined}
                    onInspect={() => setInspectingId(m.id)}
                    notice={noticeFor(m.id)}
                  />
                ),
              )}

              {/* The optimistic pair: shown only while the turn is in flight.
                  Both disappear when the refetched transcript replaces them. */}
              {pendingUserMessage ? (
                <UserMessage message={{ content: pendingUserMessage }} />
              ) : null}
              {isStreaming ? (
                <AssistantMessage
                  content={streamingText}
                  reasoning={reasoningText}
                  agentName={agent?.name}
                  liveTools={liveTools}
                  phase={phase}
                  elapsedMs={elapsedMs}
                />
              ) : null}
            </div>
          )}
        </div>

        {!stuckToBottom ? (
          <button
            type="button"
            onClick={() => scrollToBottom()}
            className={cn(
              "absolute bottom-3 left-1/2 flex -translate-x-1/2 items-center gap-1.5 rounded-full",
              "border border-(--color-border) bg-(--color-card) py-1.5 shadow-(--shadow-card)",
              "text-[11px] text-(--color-muted-foreground) transition-transform hover:-translate-y-px",
              isStreaming ? "px-3" : "px-2",
            )}
          >
            <ArrowDown className="size-3.5" />
            {isStreaming ? <span className="text-shimmer font-medium">{t.app.modules.agents.chat.typing}</span> : null}
          </button>
        ) : null}
      </div>

      {/* Same measure as the transcript. A composer that runs the full
          width of a 34" monitor is a worse text field, not a better one. */}
      <div className={cn("mx-auto w-full shrink-0", measure)}>
        {resumable && !isStreaming ? (
          /* Not an error banner: the turn did real work and stopped part
             way. It reads as unfinished business with a way to finish it,
             which is what "Try again" could never be. */
          <div className="mb-2 flex items-start gap-2 rounded-xl border border-(--color-border) bg-(--color-muted)/40 px-3 py-2.5">
            <AlertTriangle className="mt-0.5 size-4 shrink-0 text-(--color-muted-foreground)" />
            <p className="flex-1 text-[11.5px] leading-relaxed text-(--color-muted-foreground)">
              {/* Two reasons reach this banner and they are not the same
                  news. The ceiling means the turn ran out of steps; a
                  deadline means it never got to finish. Neither sentence
                  names a middleware, a context or a status code. */}
              {resumable.finish_reason === "deadline"
                ? t.app.modules.agents.chat.unfinished
                : t.app.modules.agents.chat.interrupted}
            </p>
            <Button
              size="sm"
              variant="subtle"
              onClick={() =>
                void resume(resumable.id, t.app.modules.agents.chat.continueTurn)
              }
            >
              {t.app.modules.agents.chat.continueTurn}
            </Button>
          </div>
        ) : null}

        {error ? (
          isBudgetRefusal(error) && agent ? (
            /* Not a failure: a limit the user set, doing its job. It reads
               differently from an outage on purpose, and it carries the way
               out rather than only the bad news. */
            <BudgetRefusal error={error} agentId={agent.id} onDismiss={clearError} />
          ) : (
            <div className="mb-2 flex items-start gap-2 rounded-xl border border-(--color-destructive)/30 bg-(--color-destructive)/5 px-3 py-2.5">
              <AlertTriangle className="mt-0.5 size-4 shrink-0 text-(--color-destructive)" />
              {/* Clamped, with the whole thing on hover.

                  A gateway error is not written for a reader: a bad model
                  name comes back as one useful sentence followed by the
                  provider's entire fallback configuration, which on a
                  shared LiteLLM means another product's deployment names
                  scrolling through this banner. The first line is what
                  says what went wrong; the rest is for whoever goes
                  looking, so it stays reachable rather than shown. */}
              <p
                title={error.message}
                className="line-clamp-3 flex-1 text-[11.5px] leading-relaxed text-(--color-destructive)"
              >
                {error.message}
              </p>
              <Button size="sm" variant="ghost" onClick={clearError}>
                {t.app.modules.agents.chat.close}
              </Button>
            </div>
          )
        ) : null}

        {memoryError && !memoryDraft ? (
          <div className="mb-2 flex items-start gap-2 rounded-xl border border-(--color-destructive)/30 bg-(--color-destructive)/5 px-3 py-2.5">
            <AlertTriangle className="mt-0.5 size-4 shrink-0 text-(--color-destructive)" />
            <p className="flex-1 text-[11.5px] leading-relaxed text-(--color-destructive)">
              {t.app.modules.agents.interp.memorySaveFailed} {memoryError}
            </p>
            <Button size="sm" variant="ghost" onClick={() => setMemoryError(null)}>
              {t.app.modules.agents.chat.close}
            </Button>
          </div>
        ) : null}

        {savedAt?.anchor === "composer" ? (
          <div className="mb-2 px-1">
            <MemorySavedNotice onOpen={openMemoryPage} extra="Nada foi enviado ao agente." />
          </div>
        ) : null}

        {showComposer ? (
          <Composer
            onSend={handleSend}
            onStop={stop}
            isStreaming={isStreaming}
            disabled={!agent}
            placeholder={
              agent ? `Falar com ${agent.name}… (/lembrar salva na memória)` : "Agente indisponível"
            }
            hint={rememberIntentHint}
            onCommand={runCommand}
            commandContext={{ hasAgent: agent !== undefined }}
            // Passed only once the catalogue has been read. Before that `@`
            // is inert rather than showing an empty list, because "this
            // agent has nothing" and "we have not looked yet" are different
            // statements and only one of them is true at that moment.
            references={toolsQuery.data ? references : undefined}
            emptyReferencesMessage={
              <>
                {t.app.modules.agents.interp.noIntegrations}{" "}
                {agent ? (
                  <button
                    type="button"
                    onMouseDown={(e) => e.preventDefault()}
                    onClick={() => navigate(`/app/modules/agents/${agent.id}/tools`)}
                    className="underline underline-offset-2 hover:text-(--color-foreground)"
                  >
                    {t.app.modules.agents.chat.manageTools}
                  </button>
                ) : null}
              </>
            }
          />
        ) : null}
      </div>

      {inspected ? (
        <ContextInspector
          message={inspected}
          agentName={agent?.name}
          onClose={() => setInspectingId(null)}
        />
      ) : null}

      {agent && consolidation.phase ? (
        <MemoryCandidatesDialog
          agentName={agent.name}
          phase={consolidation.phase}
          saving={consolidation.saving}
          saveError={consolidation.saveError}
          onConfirm={(contents, upToSeq) => void consolidation.confirm(contents, upToSeq)}
          onCancel={consolidation.close}
          onOpenSettings={() => {
            consolidation.close();
            navigate(`/app/modules/agents/${agent.id}/settings`);
          }}
        />
      ) : null}

      {agent && memoryDraft ? (
        <MemoryDialog
          title={`Salvar na memória de ${agent.name}`}
          agentName={agent.name}
          initialContent={memoryDraft.content}
          busy={createMemory.isPending}
          error={memoryError}
          onCancel={() => {
            setMemoryDraft(null);
            setMemoryError(null);
          }}
          onConfirm={(content) => saveMemory(content, memoryDraft.anchor, false)}
        />
      ) : null}
    </div>
  );
}

function HeaderAction({
  label,
  icon: Icon,
  onClick,
}: {
  label: string;
  icon: React.ComponentType<React.SVGProps<SVGSVGElement>>;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      title={label}
      aria-label={label}
      className={cn(
        "shrink-0 rounded-lg p-1.5 text-(--color-muted-foreground) transition-colors",
        "hover:bg-(--color-muted) hover:text-(--color-foreground)",
        "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-(--color-ring)/50",
      )}
    >
      <Icon className="size-4" />
    </button>
  );
}

/**
 * The last message the reader could see, which is the snapshot
 * consolidation is asked about.
 *
 * Zero for a thread with nothing in it: the server reads that as "as it is
 * now", and a thread with nothing in it has nothing either way.
 */
function lastSeqOf(messages: ApiMessage[]): number {
  return messages.at(-1)?.seq ?? 0;
}

function findLastUser(messages: ApiMessage[]): ApiMessage | undefined {
  for (let i = messages.length - 1; i >= 0; i--) {
    if (messages[i].role === "user") return messages[i];
  }
  return undefined;
}

function EmptyThread({ agentName }: { agentName?: string }) {
  const t = useT();
  return (
    <div className="flex h-full flex-col items-center justify-center gap-2 text-center">
      <div className="flex size-10 items-center justify-center rounded-xl bg-(--color-muted)">
        <Bot className="size-4.5 text-(--color-muted-foreground)" />
      </div>
      <p className="text-[13px] font-medium text-(--color-foreground)">
        {agentName ? `Conversando com ${agentName}` : "Nova conversa"}
      </p>
      <p className="max-w-sm text-[11.5px] leading-relaxed text-(--color-muted-foreground)">
        {t.app.modules.agents.chat.empty}
      </p>
    </div>
  );
}
