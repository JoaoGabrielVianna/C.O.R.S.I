import { useEffect, useRef, type ComponentType, type ReactNode, type SVGProps } from "react";
import { cn } from "@/lib/utils";

/**
 * The list that hangs off the composer.
 *
 * ── Why one component for `/` and `@` ──────────────────────────────────
 * They mean different things — one runs an action, the other attaches a
 * reference — but they are operated identically: the same arrows, the same
 * Enter, the same Escape, the same fight with the blur, the same
 * `aria-activedescendant` dance. That behaviour is the part that is easy to
 * get subtly wrong and hard to notice, so it exists once. What differs
 * between the two menus is what a row SAYS, which is why the item is data
 * and the rendering is here.
 *
 * ── Why it is anchored and not a modal ─────────────────────────────────
 * It answers "what can I do from here", and the answer belongs next to the
 * thing being typed into. A modal would take the screen away from the
 * conversation the entries are about, and would put a focus trap between
 * the user and the textarea they are still writing in.
 *
 * ── Why focus never leaves the textarea ────────────────────────────────
 * Because the user is still typing. Moving focus onto each row as the
 * arrows move would end composition, break the caret, and make the next
 * keystroke go somewhere else. The list is described with
 * `aria-activedescendant` instead: the textarea keeps focus and keeps
 * announcing which option is active, which is what the combobox pattern is
 * for.
 *
 * ── Why the rows fight the blur ────────────────────────────────────────
 * `preventDefault` on pointer-down stops the browser from moving focus out
 * of the textarea before the click lands. Without it, clicking a row blurs
 * the composer, the menu unmounts on the way down, and the click arrives
 * at nothing.
 */

export interface ComposerMenuItem {
  /** Stable identity for React and for the caller's own bookkeeping. */
  key: string;
  label: string;
  /** One line, shown under the label. Replaced by `reason` when disabled. */
  description: string;
  icon: ComponentType<SVGProps<SVGSVGElement>>;
  /**
   * Rendered at the end of the label row: the trigger for a command, a
   * check for an already-attached reference. A node rather than a string so
   * a caller can put a glyph there without this file enumerating cases.
   */
  trailing?: ReactNode;
  /** True when the row cannot be chosen. `reason` is shown in its place. */
  disabled?: boolean;
  reason?: string;
}

interface Props {
  /** Rendered in order; the list is already filtered. */
  items: readonly ComposerMenuItem[];
  activeIndex: number;
  /** The id the textarea points `aria-activedescendant` at. */
  listId: string;
  optionId: (index: number) => string;
  /** Names the list for a screen reader: "Comandos", "Integrações". */
  ariaLabel: string;
  /**
   * What to say when the list is empty.
   *
   * Said out loud rather than by disappearing: a menu that vanishes
   * mid-typing reads as a bug, and the user cannot tell whether the thing
   * exists and was misspelled or never existed at all.
   */
  empty: ReactNode;
  onHover: (index: number) => void;
  onSelect: (index: number) => void;
}

export function ComposerMenu({
  items,
  activeIndex,
  listId,
  optionId,
  ariaLabel,
  empty,
  onHover,
  onSelect,
}: Props) {
  return (
    <div
      className={cn(
        // Above the composer and inside its stacking context: no portal, no
        // z-index negotiation with drawers and dialogs, and it moves with
        // the composer when the textarea grows.
        "absolute bottom-full left-0 right-0 z-30 mb-2 overflow-hidden rounded-xl",
        "border border-(--color-border) bg-(--color-card) shadow-(--shadow-card)",
      )}
    >
      {items.length === 0 ? (
        <div className="px-3 py-2.5 text-[11.5px] leading-relaxed text-(--color-muted-foreground)">
          {empty}
        </div>
      ) : (
        <ul
          id={listId}
          role="listbox"
          aria-label={ariaLabel}
          className="max-h-64 overflow-y-auto py-1"
        >
          {items.map((item, i) => (
            <Option
              key={item.key}
              item={item}
              id={optionId(i)}
              active={i === activeIndex}
              onHover={() => onHover(i)}
              onSelect={() => onSelect(i)}
            />
          ))}
        </ul>
      )}
    </div>
  );
}

function Option({
  item,
  id,
  active,
  onHover,
  onSelect,
}: {
  item: ComposerMenuItem;
  id: string;
  active: boolean;
  onHover: () => void;
  onSelect: () => void;
}) {
  const ref = useRef<HTMLLIElement>(null);
  const Icon = item.icon;

  // Keeps the active row on screen.
  useEffect(() => {
    if (active) ref.current?.scrollIntoView({ block: "nearest" });
  }, [active]);

  return (
    <li
      ref={ref}
      id={id}
      role="option"
      aria-selected={active}
      aria-disabled={item.disabled || undefined}
      onMouseMove={onHover}
      // Pointer-down is where focus would be lost, so that is where it is
      // refused. The click still happens, with the caret intact.
      onMouseDown={(e) => e.preventDefault()}
      onClick={item.disabled ? undefined : onSelect}
      className={cn(
        "flex items-start gap-2.5 px-3 py-2",
        item.disabled ? "cursor-not-allowed opacity-55" : "cursor-pointer",
        active && !item.disabled && "bg-(--color-muted)",
      )}
    >
      <span className="mt-0.5 flex size-5 shrink-0 items-center justify-center rounded-md bg-(--color-brand-50) text-(--color-brand-700)">
        <Icon className="size-3" />
      </span>
      <span className="min-w-0 flex-1">
        <span className="flex items-baseline gap-2">
          <span className="text-[12.5px] font-medium text-(--color-foreground)">{item.label}</span>
          {item.trailing ? (
            <span className="ml-auto shrink-0 font-mono text-[10px] text-(--color-muted-foreground)">
              {item.trailing}
            </span>
          ) : null}
        </span>
        <span className="mt-0.5 block text-[11px] leading-relaxed text-(--color-muted-foreground)">
          {item.disabled ? (item.reason ?? item.description) : item.description}
        </span>
      </span>
    </li>
  );
}
