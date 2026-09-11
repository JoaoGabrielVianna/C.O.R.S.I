import { useMemo } from "react";
import { Check } from "lucide-react";
import type { ComposerReference } from "@/modules/agents/composer/references";
import { referenceKey } from "@/modules/agents/composer/references";
import { ComposerMenu, type ComposerMenuItem } from "./ComposerMenu";
import { useT } from "@/lib/i18n";

/**
 * The `@` menu: capabilities that can be attached to the next turn.
 *
 * ── Selecting one does NOT run it ──────────────────────────────────────
 * This is the promise the whole batch rests on, and it is the exact
 * opposite of the one `/` makes. Choosing a row here executes nothing,
 * calls no provider, sends no message and spends nothing. It adds a chip to
 * the composer, and the person carries on writing.
 *
 * What the chip then does is narrow: the turn declares only what was
 * attached, out of what the agent was already authorized to use. Attaching
 * never grants — that decision lives in Settings and nowhere else.
 *
 * ── Why an empty menu is better than no menu ───────────────────────────
 * A deployment can legitimately have nothing here: the production registry
 * ships empty, and an agent with no grants has nothing to attach. A `@`
 * that silently refuses to open is indistinguishable from a broken one, and
 * inventing a plausible row to fill the space would be advertising a
 * capability the turn would then be refused for using. So it opens and says
 * so.
 */
export function ComposerReferenceMenu({
  references,
  selectedKeys,
  activeIndex,
  listId,
  optionId,
  emptyMessage,
  onHover,
  onSelect,
}: {
  references: readonly ComposerReference[];
  /** Keys already attached, so a row can say it is in without offering to
   *  add it twice. */
  selectedKeys: ReadonlySet<string>;
  activeIndex: number;
  listId: string;
  optionId: (index: number) => string;
  /** What to say when there is nothing to attach. The caller knows whether
   *  that is "this agent has none" or "your query matched none". */
  emptyMessage: React.ReactNode;
  onHover: (index: number) => void;
  onSelect: (reference: ComposerReference) => void;
}) {
  const t = useT();
  const items = useMemo<ComposerMenuItem[]>(
    () =>
      references.map((r) => {
        const already = selectedKeys.has(referenceKey(r));
        return {
          key: referenceKey(r),
          label: r.label,
          description: r.description,
          icon: r.icon,
          // An attached row stays visible and stays selectable. Choosing it
          // again is a no-op rather than a duplicate — see the composer —
          // which is friendlier than a disabled row the arrows have to skip.
          trailing: already ? (
            <Check className="size-3" aria-label={t.app.modules.agents.chat.alreadyAttached} />
          ) : undefined,
        };
      }),
    // `t` belongs here: the rows carry an accessibility label now, and a
    // memo that ignored the dictionary would keep announcing the previous
    // language after the reader switched.
    [references, selectedKeys, t],
  );

  return (
    <ComposerMenu
      items={items}
      activeIndex={activeIndex}
      listId={listId}
      optionId={optionId}
      ariaLabel="Integrações"
      empty={emptyMessage}
      onHover={onHover}
      onSelect={(i) => onSelect(references[i])}
    />
  );
}
