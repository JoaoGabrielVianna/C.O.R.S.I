import { useMemo } from "react";
import {
  availabilityOf,
  type ComposerCommand,
  type ComposerContext,
} from "@/modules/agents/composer/commands";
import { ComposerMenu, type ComposerMenuItem } from "./ComposerMenu";
import { useT } from "@/lib/i18n";

/**
 * The `/` menu: actions the composer carries out.
 *
 * Nothing here draws anything. The list, the keyboard contract, the
 * accessibility wiring and the pointer-down guard live in ComposerMenu,
 * because `@` operates identically and behaviour that subtle should exist
 * once. What this file owns is what a command row SAYS — its trigger, and
 * whether it can run right now — which is the part `@` has no equivalent
 * of.
 *
 * ── Selecting a command RUNS it ────────────────────────────────────────
 * That is the whole difference from `@`, and it is stated here because it
 * is the thing a reader of either file needs to have straight. A command
 * with no required argument is carried out the moment its row is chosen;
 * typing the trigger and then confirming would be asking twice for one
 * decision. See ComposerReferenceMenu for the opposite promise.
 */
export function ComposerCommandMenu({
  commands,
  activeIndex,
  context,
  listId,
  optionId,
  onHover,
  onSelect,
}: {
  commands: readonly ComposerCommand[];
  activeIndex: number;
  context: ComposerContext;
  listId: string;
  optionId: (index: number) => string;
  onHover: (index: number) => void;
  onSelect: (command: ComposerCommand) => void;
}) {
  const t = useT();
  const copy = t.app.modules.agents.composer;
  const items = useMemo<ComposerMenuItem[]>(
    () =>
      commands.map((c) => {
        const availability = availabilityOf(c, context);
        return {
          // The key is the id, never the label: a translated row must stay
          // the same row.
          key: c.id,
          label: copy.commands[c.id].label,
          description: copy.commands[c.id].description,
          icon: c.icon,
          trailing: c.trigger,
          disabled: !availability.available,
          reason: availability.reason ? copy.unavailable[availability.reason] : undefined,
        };
      }),
    [commands, context, copy],
  );

  return (
    <ComposerMenu
      items={items}
      activeIndex={activeIndex}
      listId={listId}
      optionId={optionId}
      ariaLabel={copy.commandsLabel}
      empty={copy.commandsEmpty}
      onHover={onHover}
      onSelect={(i) => onSelect(commands[i])}
    />
  );
}
