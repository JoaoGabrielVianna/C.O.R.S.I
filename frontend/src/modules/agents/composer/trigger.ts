/**
 * Reading the composer draft for a trigger token.
 *
 * ── Why this is a pure function of (text, caret) ───────────────────────
 * Because every question a menu asks is a question about the caret, not
 * about the string: the same draft opens the menu or does not depending on
 * where the cursor is. A predicate over the whole value would answer
 * "somewhere in here there is a slash", which is not the question.
 *
 * It also means the entire open/close rule is testable without a DOM, and
 * the component is left with nothing to decide.
 *
 * ── Two triggers, two rules, and the difference is semantic ────────────
 * `/` is a command TO the composer. `oi /lem` is someone typing a sentence
 * that happens to contain a slash, and `http://x` is a URL — popping a
 * command menu over either would be the interface interrupting a person who
 * was not talking to it. So a command token has to start the draft's
 * logical content: whitespace may precede it, nothing else.
 *
 * `@` is a reference INSIDE what is being written. "Compare isso usando @"
 * is exactly when someone wants to attach a capability, and refusing to
 * open there would make the feature reachable only from an empty box. So it
 * opens wherever a new word starts — after whitespace, or at the very
 * beginning.
 *
 * That boundary rule is also what keeps `joao@corsi.dev` quiet: the `@`
 * there continues a word rather than starting one. It is deliberately a
 * small rule and not an attempt to parse email addresses — the internet's
 * grammar for those is not something a text box should be litigating.
 */

export const COMMAND_TRIGGER = "/";
export const REFERENCE_TRIGGER = "@";

export type Trigger = typeof COMMAND_TRIGGER | typeof REFERENCE_TRIGGER;

export interface TriggerToken {
  trigger: Trigger;
  /**
   * The whole token after the trigger, not just what precedes the caret.
   *
   * Editing the middle of `/lembrar` should keep showing the command it
   * spells, so the filter reads the token as written rather than as far as
   * the cursor happens to be.
   */
  query: string;
  /** Index of the trigger character. */
  start: number;
  /** Index just past the token, where whitespace or the string ends. */
  end: number;
}

/**
 * Returns the token the caret is currently in, or null.
 *
 * Open when, and only when:
 *
 *   - the trigger sits where its rule allows (see above);
 *   - the caret is strictly after the trigger and no further than the end
 *     of the token.
 *
 * The caret sitting exactly on the trigger means the user is about to type
 * BEFORE it, which is not being inside a token.
 */
function readTokenAt(value: string, caret: number, trigger: Trigger, start: number): TriggerToken | null {
  if (value[start] !== trigger) return null;

  // The token ends at the first whitespace, which is also what makes
  // `/lembrar algo` stop being a menu and start being an argument.
  const rest = value.slice(start + 1);
  const boundary = rest.search(/\s/);
  const end = boundary === -1 ? value.length : start + 1 + boundary;

  if (caret <= start || caret > end) return null;

  return { trigger, query: value.slice(start + 1, end), start, end };
}

/** The command token the caret is in, or null. `/` at the head of the draft. */
export function readTriggerToken(value: string, caret: number): TriggerToken | null {
  if (caret < 0 || caret > value.length) return null;
  const start = value.length - value.trimStart().length;
  return readTokenAt(value, caret, COMMAND_TRIGGER, start);
}

/**
 * The reference token the caret is in, or null. `@` at a word boundary.
 *
 * The search runs backwards from the caret to the nearest whitespace, and
 * the token exists only if the `@` is the first character of that word.
 * That single condition is the whole rule: it opens for `@`, for ` @`, for
 * `analise @`, and stays shut for `joao@corsi.dev` and for anything else
 * where the `@` continues a word somebody was already writing.
 */
export function readReferenceToken(value: string, caret: number): TriggerToken | null {
  if (caret < 0 || caret > value.length) return null;

  const before = value.slice(0, caret);
  const lastSpace = before.search(/\s(?=\S*$)/);
  // Where the word the caret sits in begins. -1 from `search` means there
  // is no whitespace before the caret at all, so the word starts at 0.
  const start = lastSpace === -1 ? 0 : lastSpace + 1;

  return readTokenAt(value, caret, REFERENCE_TRIGGER, start);
}

/**
 * Removes a token from the draft, leaving everything else alone.
 *
 * Used when something is chosen from a menu: the token was how the person
 * reached the menu and it has done its job, so it goes — but whatever else
 * they had typed is theirs and stays. A draft left holding only whitespace
 * is cleared, because that is not text anyone wrote.
 *
 * ── Why the space before it goes too, for `@` ──────────────────────────
 * `analise @git` becomes `analise `, not `analise` — the trailing space is
 * where the person's cursor was and where they will keep typing. What must
 * not survive is the token itself: `@git` reaching the model as text would
 * be this feature's central failure, a reference that turned back into a
 * string for the model to guess at.
 */
export function removeTriggerToken(value: string, token: TriggerToken): string {
  const without = value.slice(0, token.start) + value.slice(token.end);
  return without.trim() === "" ? "" : without;
}
