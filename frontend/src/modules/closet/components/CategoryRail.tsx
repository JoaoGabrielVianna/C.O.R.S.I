import { cn } from "@/lib/utils";
import { useT } from "@/lib/i18n";

import type { Catalog, Category } from "../api/types";
import { categoryLabel } from "../labels";

/**
 * The category rail — the first move in the whole interaction.
 *
 * ── Why the labels are looked up and the list is not ───────────────────
 * The categories come from `GET /closet/catalog`, so the rail cannot
 * disagree with the server about what exists. The LABELS are translated,
 * because "tops" is an identifier and "Partes de cima" is copy. A category
 * the dictionary does not know yet renders its identifier rather than
 * nothing: a rail with a blank button would be worse than one showing a
 * word in English, and the missing key is a translation task, not an
 * outage.
 */
export function CategoryRail({
  catalog,
  active,
  onSelect,
  counts,
}: {
  catalog: Catalog | undefined;
  active: Category | undefined;
  onSelect: (category: Category | undefined) => void;
  /** How many live pieces each category holds. Absent while loading. */
  counts?: Record<Category, number>;
}) {
  const t = useT();
  const labels = t.app.modules.closet;

  return (
    <nav
      aria-label={labels.categories}
      className="flex gap-1.5 overflow-x-auto pb-1 [scrollbar-width:none] [&::-webkit-scrollbar]:hidden"
    >
      <RailButton
        label={labels.allCategories}
        active={active === undefined}
        onClick={() => onSelect(undefined)}
      />
      {catalog?.categories.map((def) => (
        <RailButton
          key={def.category}
          label={categoryLabel(t, def.category)}
          count={counts?.[def.category]}
          active={active === def.category}
          onClick={() => onSelect(def.category)}
        />
      ))}
    </nav>
  );
}

function RailButton({
  label,
  count,
  active,
  onClick,
}: {
  label: string;
  count?: number;
  active: boolean;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      // `aria-pressed` rather than a role of tab: this is a filter that
      // narrows a grid in place, not a tab that swaps a panel, and calling
      // it one would promise keyboard behaviour it does not have.
      aria-pressed={active}
      className={cn(
        "inline-flex shrink-0 items-center gap-1.5 rounded-full border px-3 py-1.5",
        "text-[12.5px] font-medium transition-colors",
        active
          ? "border-(--color-accent) bg-(--color-accent) text-(--color-accent-foreground)"
          : "border-(--color-border) bg-(--color-card) text-(--color-muted-foreground) hover:bg-(--color-muted) hover:text-(--color-foreground)",
      )}
    >
      {label}
      {count !== undefined ? (
        <span
          className={cn(
            "font-mono text-[10px] tabular-nums",
            active ? "opacity-80" : "opacity-60",
          )}
        >
          {count}
        </span>
      ) : null}
    </button>
  );
}
