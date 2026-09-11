import { useCallback, useEffect, useId, useLayoutEffect, useMemo, useRef, useState } from "react";
import { ArrowUp, Square, X } from "lucide-react";
import { Button } from "@/components/ui/Button";
import { cn } from "@/lib/utils";
import {
  availabilityOf,
  filterCommands,
  type ComposerCommand,
  type ComposerContext,
} from "@/modules/agents/composer/commands";
import {
  filterReferences,
  referenceKey,
  type ComposerReference,
} from "@/modules/agents/composer/references";
import {
  readReferenceToken,
  readTriggerToken,
  removeTriggerToken,
  type TriggerToken,
} from "@/modules/agents/composer/trigger";
import { ComposerCommandMenu } from "./ComposerCommandMenu";
import { ComposerReferenceMenu } from "./ComposerReferenceMenu";
import { useT } from "@/lib/i18n";

/**
 * The input box.
 *
 * Enter sends, Shift+Enter breaks the line — the convention every chat
 * client shares, and the one users' fingers already expect. The textarea
 * grows with its content up to a ceiling, after which it scrolls, so a
 * long paste cannot swallow the transcript above it.
 *
 * ── Two triggers, two promises ─────────────────────────────────────────
 * `/` opens a list of actions, and choosing one CARRIES IT OUT. `@` opens a
 * list of capabilities, and choosing one attaches a chip and does nothing
 * else — no execution, no request, no message. The draft is text plus
 * attached references, and both go to `onSend` together.
 *
 * Only one menu is ever open. The caret is in one token or in neither, and
 * `/` at the head of the draft cannot also be an `@` at a word boundary, so
 * the two rules cannot both match — but the composer still resolves them in
 * a fixed order rather than relying on that, because a rule that happens to
 * be exclusive is not the same as one that is.
 */

interface Props {
  /**
   * Takes the draft and whatever was attached to it. Returning `false`
   * means it was not accepted as something to send or act on, and the
   * composer keeps both the text and the references instead of clearing
   * them — the case that exists is a command typed without its argument,
   * where clearing would erase the draft and do nothing else.
   */
  onSend: (text: string, references: ComposerReference[]) => void | boolean;
  onStop: () => void;
  isStreaming: boolean;
  disabled?: boolean;
  placeholder?: string;
  /**
   * A non-blocking note about what the current draft will do. Given the
   * draft, returns the note or null. It lives here rather than in the page
   * because only this component holds the draft, and it is a function
   * rather than a value so the composer stays ignorant of what is being
   * warned about.
   */
  hint?: (draft: string) => string | null;
  /**
   * Runs a command the user picked from the menu.
   *
   * The composer decides WHEN a command was chosen — that is a question
   * about the draft and the caret, which only it can answer — and knows
   * nothing about what any of them do. The page owns the flows.
   */
  onCommand?: (command: ComposerCommand) => void;
  /** What the registry needs to decide whether a command can run now. */
  commandContext?: ComposerContext;
  /**
   * The capabilities `@` may offer, already resolved by the page from the
   * agent's authorized tools. Absent disables the trigger entirely; an
   * empty array keeps it, and the menu says there is nothing to attach —
   * which is a true and useful thing to be told.
   */
  references?: readonly ComposerReference[];
  /**
   * Shown when `references` is empty. The page knows whether that means
   * "this agent has none" or something else; the composer does not.
   */
  emptyReferencesMessage?: React.ReactNode;
}

const MAX_HEIGHT_PX = 200;

/**
 * Whether a dismissal still applies to the token under the caret.
 *
 * Same trigger, same position, same text. Position is part of it because
 * `@` at 0 and `@` at 12 are two things a person typed, and text is part of
 * it because editing the query is asking again.
 */
function isSameOccurrence(dismissed: TriggerToken | null, token: TriggerToken): boolean {
  return (
    dismissed !== null &&
    dismissed.trigger === token.trigger &&
    dismissed.start === token.start &&
    dismissed.query === token.query
  );
}

export function Composer({
  onSend,
  onStop,
  isStreaming,
  disabled,
  placeholder,
  hint,
  onCommand,
  commandContext,
  references,
  emptyReferencesMessage,
}: Props) {
  const t = useT();
  const [value, setValue] = useState("");
  /**
   * What is attached to the turn being written.
   *
   * It lives beside the text and not inside it, which is the point: the
   * token that opened the menu is removed from the draft, and what remains
   * is structured. `@git` never reaches the model as characters for it to
   * interpret — the selection reaches the backend as identity, and the
   * backend decides what it means.
   */
  const [attached, setAttached] = useState<ComposerReference[]>([]);
  const ref = useRef<HTMLTextAreaElement>(null);

  /**
   * Where the caret is, tracked because the menus are a question about the
   * caret and not about the text. React does not re-render on selection
   * changes, so every event that can move it reports in.
   */
  const [caret, setCaret] = useState(0);
  const [activeIndex, setActiveIndex] = useState(0);
  /**
   * The trigger occurrence the user closed, or null.
   *
   * ── Why this is a token and not a query string ─────────────────────────
   * It WAS a query string, and that was the bug: dismissing `@` stored the
   * empty query, so every later `@` — a different occurrence, in a
   * different place, typed on purpose — matched the dismissal and opened to
   * nothing. The same held for `/`, and blurring an empty composer poisoned
   * both, because `token?.query ?? ""` stored a dismissal for a token that
   * did not exist.
   *
   * Dismissal belongs to the OCCURRENCE. Two `@` are the same occurrence
   * only while they are the same token, in the same place, still being the
   * one the caret is in — and the moment the caret leaves every token, the
   * occurrence is over and is forgotten by the effect below. Typing a new
   * trigger therefore opens, always, which is the whole fix.
   *
   * The query is kept in the comparison on purpose: editing `@gi` into
   * `@git` after Escape is the user asking a new question and should
   * reopen. What must not reopen is the same token, unchanged, right after
   * it was closed.
   */
  const [dismissed, setDismissed] = useState<TriggerToken | null>(null);

  const listId = useId();
  const optionId = useCallback((i: number) => `${listId}-option-${i}`, [listId]);

  const commandsEnabled = !disabled && Boolean(onCommand);
  const referencesEnabled = !disabled && references !== undefined;

  // Resolved in a fixed order. The two rules cannot both match today, and
  // the order is written down anyway so that a future change to either one
  // cannot silently produce two open menus.
  const token: TriggerToken | null = useMemo(() => {
    if (commandsEnabled) {
      const command = readTriggerToken(value, caret);
      if (command) return command;
    }
    if (referencesEnabled) return readReferenceToken(value, caret);
    return null;
  }, [commandsEnabled, referencesEnabled, value, caret]);

  const attachedKeys = useMemo(() => new Set(attached.map(referenceKey)), [attached]);

  const commandMatches = useMemo(
    () => (token?.trigger === "/" ? filterCommands(token.query) : []),
    [token],
  );
  const referenceMatches = useMemo(
    () => (token?.trigger === "@" ? filterReferences(token.query, references ?? []) : []),
    [token, references],
  );
  const matchCount = token?.trigger === "/" ? commandMatches.length : referenceMatches.length;

  /**
   * A dismissal outlives nothing.
   *
   * The moment the caret is not in any token, the occurrence the user
   * closed is over: they deleted it, moved away, or sent the message. What
   * they type next is a NEW `@` or `/`, and it must open. Without this, the
   * comparison below would still match a re-typed token that happens to sit
   * in the same place with the same query — which is exactly how somebody
   * reproduces the bug: type `@`, Escape, backspace, type `@`.
   *
   * Adjusted during render rather than in an effect. React supports this
   * for state that is a function of a prop or of other state, re-running
   * the render without committing the first one — so there is no frame in
   * which a stale dismissal is live, and no cascading update. An effect
   * would commit, then set, then render again for the same result.
   */
  if (dismissed !== null && token === null) setDismissed(null);

  const menuOpen = token !== null && !isSameOccurrence(dismissed, token);
  const boundedIndex = matchCount === 0 ? -1 : Math.min(activeIndex, matchCount - 1);
  const activeId = menuOpen && boundedIndex >= 0 ? optionId(boundedIndex) : undefined;

  const syncCaret = useCallback((el: HTMLTextAreaElement) => {
    setCaret(el.selectionStart ?? 0);
  }, []);

  /**
   * Where the caret has to be put once React has rewritten the draft.
   *
   * A ref and a layout effect rather than a frame callback, and the
   * difference is not stylistic: `requestAnimationFrame` lands AFTER the
   * next keystrokes in a fast typist's sequence, so the caret would jump
   * back to where the token used to be in the middle of a word. A layout
   * effect runs in the same commit as the new value, before anything can be
   * typed into it.
   */
  const pendingCaret = useRef<number | null>(null);

  useLayoutEffect(() => {
    const at = pendingCaret.current;
    if (at === null) return;
    pendingCaret.current = null;
    const el = ref.current;
    if (!el) return;
    el.focus();
    const bounded = Math.min(at, el.value.length);
    el.setSelectionRange(bounded, bounded);
    setCaret(bounded);
  }, [value]);

  /**
   * Removes the token that opened the menu and puts the caret where it was.
   *
   * Shared by both triggers, because the reason is the same either way: the
   * token was how the person reached the menu, the menu has answered, and
   * what is left in the box has to be what they meant to write. The caret
   * lands where the token started — mid-sentence for `analise @git`, which
   * is where the person was writing.
   */
  const consumeToken = useCallback(
    (t: TriggerToken) => {
      const next = removeTriggerToken(value, t);
      pendingCaret.current = t.start;
      setValue(next);
      setDismissed(null);
      setActiveIndex(0);
    },
    [value],
  );

  const chooseCommand = useCallback(
    (command: ComposerCommand) => {
      if (!token || !onCommand) return;
      if (!availabilityOf(command, commandContext ?? { hasAgent: true }).available) return;
      // The token was an instruction and it is about to be carried out, so
      // it goes. Anything else the person had typed is theirs and stays.
      consumeToken(token);
      onCommand(command);
    },
    [commandContext, consumeToken, onCommand, token],
  );

  const chooseReference = useCallback(
    (reference: ComposerReference) => {
      if (!token) return;
      consumeToken(token);
      // Attaching the same capability twice is attaching it once. Silently,
      // because the person asked for a true thing they had already asked
      // for — refusing it would be a message about nothing.
      setAttached((current) =>
        current.some((r) => referenceKey(r) === referenceKey(reference))
          ? current
          : [...current, reference],
      );
    },
    [consumeToken, token],
  );

  const detach = useCallback((key: string) => {
    setAttached((current) => current.filter((r) => referenceKey(r) !== key));
    requestAnimationFrame(() => ref.current?.focus());
  }, []);

  // Auto-resize: collapse to nothing, then grow to the content's real
  // height. Reading scrollHeight without the reset would ratchet upward,
  // since a shrinking textarea never reports a smaller scrollHeight.
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    el.style.height = "0px";
    el.style.height = `${Math.min(el.scrollHeight, MAX_HEIGHT_PX)}px`;
  }, [value]);

  const submit = useCallback(() => {
    const text = value.trim();
    if (!text || isStreaming || disabled) return;
    const accepted = onSend(text, attached) !== false;
    // Keep focus either way, so the next question can be typed straight
    // away.
    if (accepted) {
      setValue("");
      setCaret(0);
      // The attachments belonged to the turn that has just gone. Carrying
      // them into the next one would silently scope a turn nobody scoped —
      // and a scope the user cannot see is the one thing worse than none.
      setAttached([]);
    }
    setDismissed(null);
    requestAnimationFrame(() => ref.current?.focus());
  }, [attached, disabled, isStreaming, onSend, value]);

  const canSend = value.trim().length > 0 && !isStreaming && !disabled;
  const note = hint?.(value) ?? null;

  return (
    <div className="relative shrink-0">
      {menuOpen && token?.trigger === "/" ? (
        <ComposerCommandMenu
          commands={commandMatches}
          activeIndex={boundedIndex}
          context={commandContext ?? { hasAgent: true }}
          listId={listId}
          optionId={optionId}
          onHover={setActiveIndex}
          onSelect={chooseCommand}
        />
      ) : null}
      {menuOpen && token?.trigger === "@" ? (
        <ComposerReferenceMenu
          references={referenceMatches}
          selectedKeys={attachedKeys}
          activeIndex={boundedIndex}
          listId={listId}
          optionId={optionId}
          emptyMessage={
            (references?.length ?? 0) === 0
              ? (emptyReferencesMessage ?? "Nenhuma integração disponível para este agente.")
              : "Nenhuma integração encontrada"
          }
          onHover={setActiveIndex}
          onSelect={chooseReference}
        />
      ) : null}
      <div
        className={cn(
          // shrink-0 keeps the composer pinned: it is the last item in the
          // chat's flex column, and without it a long transcript would
          // squeeze it instead of scrolling.
          "flex shrink-0 flex-col gap-1.5 rounded-2xl border border-(--color-border) bg-(--color-card) p-2 shadow-(--shadow-card)",
          "transition-[border-color,box-shadow] duration-[250ms] [transition-timing-function:var(--ease-premium)]",
          "focus-within:border-(--color-brand-500) focus-within:ring-2 focus-within:ring-(--color-brand-500)/20",
        )}
      >
        {/* Above the textarea and wrapping, never scrolling sideways: a
            horizontal scroller would hide attachments behind a gesture, and
            what is attached to a turn has to be visible without one. The
            textarea keeps its own line whatever happens here. */}
        {attached.length > 0 ? (
          <ul className="flex flex-wrap gap-1.5 px-1 pt-0.5">
            {attached.map((r) => (
              <li key={referenceKey(r)}>
                <Chip reference={r} onRemove={() => detach(referenceKey(r))} />
              </li>
            ))}
          </ul>
        ) : null}

        <div className="flex items-end gap-2">
          <textarea
            ref={ref}
            rows={1}
            value={value}
            disabled={disabled}
            placeholder={placeholder ?? "Pergunte alguma coisa…"}
            role={commandsEnabled || referencesEnabled ? "combobox" : undefined}
            aria-expanded={commandsEnabled || referencesEnabled ? menuOpen : undefined}
            aria-controls={menuOpen ? listId : undefined}
            aria-activedescendant={activeId}
            aria-autocomplete={commandsEnabled || referencesEnabled ? "list" : undefined}
            onChange={(e) => {
              setValue(e.target.value);
              syncCaret(e.target);
              setActiveIndex(0);
            }}
            onSelect={(e) => syncCaret(e.currentTarget)}
            onClick={(e) => syncCaret(e.currentTarget)}
            onFocus={(e) => syncCaret(e.currentTarget)}
            // Focus leaving the composer is one of the ways the menu stops
            // making sense: it is anchored to a draft nobody is writing any
            // more. The draft itself is untouched, so returning to the
            // textarea and typing brings the menu back.
            //
            // This is what makes the rows' pointer-down guard load-bearing:
            // without it, clicking an option would blur first, close the menu
            // on the way down, and the click would arrive at nothing.
            onBlur={() => setDismissed(token)}
            onKeyDown={(e) => {
              // Mid-composition keys belong to the IME. Intercepting Enter
              // here would commit a candidate as a message, which is the
              // classic way a chat box becomes unusable in Japanese, Korean
              // or Chinese.
              if (e.nativeEvent.isComposing) return;

              if (menuOpen) {
                if (e.key === "ArrowDown" || e.key === "ArrowUp") {
                  e.preventDefault();
                  if (matchCount === 0) return;
                  const step = e.key === "ArrowDown" ? 1 : -1;
                  setActiveIndex((i) => (i + step + matchCount) % matchCount);
                  return;
                }
                // Enter and Tab both take the highlighted row. For `/` that
                // runs the command; for `@` it attaches a chip and hands the
                // keyboard straight back — it never sends the turn.
                // Shift+Enter is not a selection either way: it is a
                // newline, and it stays one whether or not a menu is open.
                if ((e.key === "Enter" && !e.shiftKey) || e.key === "Tab") {
                  if (boundedIndex < 0) return;
                  e.preventDefault();
                  if (token?.trigger === "/") chooseCommand(commandMatches[boundedIndex]);
                  else chooseReference(referenceMatches[boundedIndex]);
                  return;
                }
                if (e.key === "Escape") {
                  e.preventDefault();
                  // Closes the menu and nothing else. The draft belongs to
                  // the user; dismissing a suggestion is not deleting it.
                  setDismissed(token);
                  return;
                }
              }

              // Backspace in an empty box removes the last attachment.
              //
              // Only when the box is empty and only when the caret has
              // nothing to its left, so it can never eat a chip instead of a
              // character. Nothing else claimed this key: the composer had
              // no Backspace handling before, so there is nothing to
              // contradict.
              if (
                e.key === "Backspace" &&
                value === "" &&
                attached.length > 0 &&
                e.currentTarget.selectionStart === 0
              ) {
                e.preventDefault();
                setAttached((current) => current.slice(0, -1));
                return;
              }

              if (e.key === "Enter" && !e.shiftKey) {
                e.preventDefault();
                submit();
              }
            }}
            className={cn(
              "max-h-[200px] flex-1 resize-none bg-transparent px-2 py-1.5 text-sm leading-relaxed",
              "text-(--color-foreground) placeholder:text-(--color-muted-foreground)",
              "outline-none disabled:cursor-not-allowed disabled:opacity-60",
            )}
          />
          {isStreaming ? (
            <Button size="icon" variant="outline" onClick={onStop} aria-label={t.app.modules.agents.chat.stopReply}>
              <Square className="fill-current" />
            </Button>
          ) : (
            <Button size="icon" onClick={submit} disabled={!canSend} aria-label={t.app.modules.agents.chat.sendMessage}>
              <ArrowUp />
            </Button>
          )}
        </div>
      </div>

      {/* Non-blocking on purpose: it never refuses the message, it only says
          what the message will and will not do. */}
      {note ? (
        <p className="mt-1.5 px-1 text-[11px] leading-relaxed text-(--color-muted-foreground)">
          {note}
        </p>
      ) : null}
    </div>
  );
}

/**
 * One attached capability, in the composer.
 *
 * The × is a real button with a real name, because "remove this" has to be
 * reachable by something other than aiming at a six-pixel glyph. The label
 * is inside the button's accessible name rather than beside it, so a screen
 * reader hears "Remover Echo" and not "button".
 */
function Chip({ reference, onRemove }: { reference: ComposerReference; onRemove: () => void }) {
  const Icon = reference.icon;
  return (
    <span
      className={cn(
        "inline-flex max-w-full items-center gap-1 rounded-full border border-(--color-border)",
        "bg-(--color-muted)/50 py-0.5 pl-1.5 pr-0.5 text-[11px] text-(--color-foreground)",
      )}
    >
      <Icon className="size-3 shrink-0 text-(--color-brand-700)" />
      <span className="truncate">{reference.label}</span>
      <button
        type="button"
        onClick={onRemove}
        // Same guard as the menu rows: removing a chip must not cost the
        // textarea its focus on the way down.
        onMouseDown={(e) => e.preventDefault()}
        aria-label={`Remover ${reference.label}`}
        className={cn(
          "flex size-4 shrink-0 items-center justify-center rounded-full",
          "text-(--color-muted-foreground) transition-colors",
          "hover:bg-(--color-muted) hover:text-(--color-foreground)",
          "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-(--color-ring)/50",
        )}
      >
        <X className="size-2.5" />
      </button>
    </span>
  );
}
