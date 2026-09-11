import { Brain } from "lucide-react";
import type { ComponentType, SVGProps } from "react";

import { matchesQuery } from "@/lib/search";

/**
 * The commands a conversation's composer offers.
 *
 * ── Why this is not the workspace's ⌘K registry ────────────────────────
 * `lib/command` is a global palette: navigation, settings, account. Its
 * entries are workspace-wide and dynamically registered, and none of them
 * has a trigger, a draft to rewrite, or a conversation to act on. These do:
 * a composer command exists only inside a thread, is typed rather than
 * summoned, and is carried out against the text the user is writing.
 *
 * Sharing one registry would have meant either "Lembrar" appearing in ⌘K
 * with no conversation to consolidate, or a `context` field on the global
 * type that every workspace command would have to ignore. Two registries,
 * one matcher — `matchesQuery` is shared, and that is the part that would
 * actually have drifted.
 *
 * ── Why the registry holds no behaviour ────────────────────────────────
 * A command here is data: identity, how it is typed, how it reads, and
 * whether it can run right now. What it *does* belongs to whoever owns the
 * flows — see the dispatch in ChatView. Putting a closure in this table
 * would drag the conversation, the dialog and the query client into a
 * module whose entire job is to describe a menu.
 *
 * ── Why the copy is not here ───────────────────────────────────────────
 * It used to be: the labels sat in this table in Portuguese, justified at
 * the time by the module carrying no `useT` at all. That is no longer true,
 * and the justification went with it.
 *
 * What stays here is the part that must never move with language — the id,
 * the trigger the user types, the icon, and the search keywords. What a row
 * READS is looked up by id in the dictionary, so translating a label cannot
 * change which command is selected, what gets typed, or what the dispatch
 * switches on.
 */

/**
 * Every command id, as a closed union.
 *
 * The dispatch that runs them switches over this type exhaustively, so
 * registering a command without teaching anything to run it is a compile
 * error rather than a menu entry that does nothing.
 */
export type ComposerCommandId = "memory.consolidate";

export interface ComposerCommand {
  id: ComposerCommandId;
  /**
   * What the user types, including the trigger.
   *
   * Invariant across languages, deliberately: a person who has learned to
   * type `/lembrar` should not have it move under them because the
   * interface language changed. The label beside it does translate.
   */
  trigger: string;
  /**
   * The glyph, from the icon set the module already uses.
   *
   * A component rather than a name, so a future entry can carry a provider
   * or integration mark instead of a lucide icon without this type
   * learning what those are. Never an emoji: emoji are content, not
   * infrastructure, and they render differently on every platform.
   */
  icon: ComponentType<SVGProps<SVGSVGElement>>;
  /** Extra words that should find it. Never shown. */
  keywords: readonly string[];
}

/**
 * What the composer knows about its situation when the menu opens.
 *
 * Deliberately small, and deliberately not the agent's memory policy. The
 * policy is a server-side gate that would need its own request to paint a
 * row with, and a menu that fetches to render is a menu that stutters. A
 * command refused by policy is answered by the backend with a 409 the
 * dialog already knows how to explain, and explaining it there costs
 * nothing and cannot go stale. See the batch record.
 */
export interface ComposerContext {
  /** False when the thread's agent could not be loaded. */
  hasAgent: boolean;
}

export type UnavailableReason = "noAgent";

export interface CommandAvailability {
  available: boolean;
  /**
   * Why not, as a key rather than a sentence. The menu resolves it, so this
   * module stays free of copy and the reason still reads in the user's
   * language.
   */
  reason?: UnavailableReason;
}

export function availabilityOf(
  command: ComposerCommand,
  ctx: ComposerContext,
): CommandAvailability {
  switch (command.id) {
    case "memory.consolidate":
      return ctx.hasAgent ? { available: true } : { available: false, reason: "noAgent" };
  }
}

/**
 * The registry. One command today, and it is a real one.
 *
 * Nothing is listed here that does not work: a menu that advertises what
 * the product cannot do teaches people to stop reading it.
 */
export const COMPOSER_COMMANDS: readonly ComposerCommand[] = [
  {
    id: "memory.consolidate",
    trigger: "/lembrar",
    icon: Brain,
    keywords: ["memória", "memoria", "memory", "lembrar", "consolidar", "salvar"],
  },
];

/**
 * Fails loudly if the registry contradicts itself.
 *
 * A duplicate id or trigger is a wiring mistake, and the two ways it could
 * be resolved silently — first wins, last wins — are both a menu that does
 * something other than what the table says. It throws at module load, so a
 * duplicate cannot reach a build: the test suite and the dev server both
 * import this file.
 */
function assertRegistryIsConsistent(commands: readonly ComposerCommand[]): void {
  const ids = new Set<string>();
  const triggers = new Set<string>();
  for (const c of commands) {
    if (ids.has(c.id)) throw new Error(`composer command id is duplicated: ${c.id}`);
    if (triggers.has(c.trigger)) {
      throw new Error(`composer command trigger is duplicated: ${c.trigger}`);
    }
    ids.add(c.id);
    triggers.add(c.trigger);
  }
}

assertRegistryIsConsistent(COMPOSER_COMMANDS);

/** Exported for the test that proves a duplicate is refused rather than picked. */
export const __assertRegistryIsConsistent = assertRegistryIsConsistent;

/**
 * The commands a query selects, in registry order.
 *
 * The trigger is searched alongside the label and the keywords, because
 * what the user is typing IS the trigger: `/lem` has to find `/lembrar`
 * before it finds anything about the word "lembrar".
 */
/**
 * Filter by what the user is typing.
 *
 * The trigger and the keywords are matched always, and both are
 * language-invariant — which is why `/lembrar` still finds the command
 * whatever the interface is set to, and why the keyword list deliberately
 * carries the words for it in both languages.
 *
 * `labels` is optional and additive: pass the dictionary's labels and the
 * translated wording becomes searchable too. Omitting it narrows what
 * matches; it never changes which command an id refers to.
 */
export function filterCommands(
  query: string,
  commands: readonly ComposerCommand[] = COMPOSER_COMMANDS,
  labels?: Partial<Record<ComposerCommandId, { label: string; description: string }>>,
): ComposerCommand[] {
  return commands.filter((c) => {
    const copy = labels?.[c.id];
    return matchesQuery(
      [c.trigger, ...(copy ? [copy.label, copy.description] : []), ...c.keywords],
      query,
    );
  });
}
