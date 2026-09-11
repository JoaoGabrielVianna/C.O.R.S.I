/**
 * Capturing a memory from inside a conversation.
 *
 * ── Why capture is deterministic and not the model's decision ──────────
 * Having the model decide what to remember needs function calling, which is
 * Tools Foundation and out of scope for v1. So the two mechanisms that exist
 * are both explicit acts by the user: an action on a message, and the
 * `/lembrar` command. Both write a row and ask the model nothing.
 *
 * ── The expectation trap this file exists to soften ────────────────────
 * Typing "salve na memória que X" as an ordinary sentence saves nothing.
 * The model will very likely answer "anotado", and nothing will have been
 * noted. That gap cannot be closed in v1, so it is signposted instead:
 * `rememberIntentHint` watches the draft for those phrasings and points at
 * the mechanism that does work.
 *
 * The alternative — instructing the system prompt to claim it saved
 * something — was considered and rejected. Teaching the agent to lie is
 * worse than the gap.
 */

/** The command that captures without sending. */
export const REMEMBER_COMMAND = "/lembrar";

/**
 * What a composer draft is asking for.
 *
 * Three cases, and none of them collapses into another. The bare command
 * used to mean "you forgot the text"; it now means something the user
 * actually wants — read this conversation and tell me what is worth
 * keeping. What has not changed is the rule underneath both readings: a
 * draft that *is* the command never becomes a message to the model.
 */
export type RememberCommand =
  /** Not the command. Send it. */
  | { kind: "none" }
  /** The command with something to store. Capture it, send nothing. */
  | { kind: "capture"; content: string }
  /**
   * The command on its own: consolidate what has already been said.
   *
   * It costs a provider call and the user is asking for it deliberately,
   * which is why it needs no argument to be a complete request.
   */
  | { kind: "consolidate" };

/**
 * Reads a composer draft as a capture command.
 *
 * The boundary rule is deliberate: a separator is required after the
 * command, so `/lembrarei disso` stays an ordinary message. What follows
 * the command with some other punctuation stuck to it — `/lembrar: x` — is
 * a different word by this rule and stays a message too.
 */
export function readRememberCommand(draft: string): RememberCommand {
  const trimmed = draft.trim();
  const lower = trimmed.toLowerCase();
  if (!lower.startsWith(REMEMBER_COMMAND)) return { kind: "none" };

  const rest = trimmed.slice(REMEMBER_COMMAND.length);
  if (rest.length > 0 && !/^\s/.test(rest)) return { kind: "none" };

  const content = rest.trim();
  return content.length > 0 ? { kind: "capture", content } : { kind: "consolidate" };
}

/**
 * Phrasings that mean "remember this" but do not save anything.
 *
 * Matched locally, by prefix, with no model involved. Deliberately narrow:
 * a hint that fires on ordinary sentences is noise, and noise is ignored
 * exactly when it would have mattered.
 */
const INTENT_PREFIXES = [
  "lembre",
  "lembra",
  "lembrar",
  "salve na memória",
  "salva na memória",
  "salvar na memória",
  "guarde",
  "guarda isso",
  "não esqueça",
  "nao esqueca",
  "anota",
  "anote",
];

/** Returns the nudge to show under the composer, or null. */
export function rememberIntentHint(draft: string): string | null {
  const lower = draft.trim().toLowerCase();
  if (!lower || lower.startsWith(REMEMBER_COMMAND)) return null;
  if (!INTENT_PREFIXES.some((p) => lower.startsWith(p))) return null;
  return `Isto vai ser enviado ao agente, e não salvo. Para salvar de verdade, use ${REMEMBER_COMMAND} ou a ação “Salvar na memória” de uma mensagem.`;
}
