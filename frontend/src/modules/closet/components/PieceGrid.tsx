import { Check, Heart, Pencil } from "lucide-react";

import { cn } from "@/lib/utils";
import { useT } from "@/lib/i18n";

import type { Catalog, ClosetItem } from "../api/types";
import { compositionImage } from "../composition";
import { PieceImage } from "./PieceImage";

/**
 * The visual selector: a wardrobe drawer, one category at a time.
 *
 * ── Why the whole tile is the select and editing is a corner ───────────
 * Because the primary act is choosing, not administering. A grid where
 * every tile offers "select" and "edit" with equal weight is a table with
 * pictures — the thing this module exists not to be. The pencil is a small
 * affordance in the corner, and it stops the click from reaching the tile
 * so opening the detail panel never also swaps a garment into the look.
 */
export function PieceGrid({
  catalog,
  items,
  selectedIDs,
  onSelect,
  onInspect,
}: {
  catalog: Catalog | undefined;
  items: ClosetItem[];
  selectedIDs: ReadonlySet<string>;
  onSelect: (item: ClosetItem) => void;
  onInspect: (item: ClosetItem) => void;
}) {
  return (
    <ul className="grid grid-cols-2 gap-2.5 sm:grid-cols-3 xl:grid-cols-4">
      {items.map((item) => (
        <li key={item.id}>
          <PieceTile
            catalog={catalog}
            item={item}
            selected={selectedIDs.has(item.id)}
            onSelect={() => onSelect(item)}
            onInspect={() => onInspect(item)}
          />
        </li>
      ))}
    </ul>
  );
}

function PieceTile({
  catalog,
  item,
  selected,
  onSelect,
  onInspect,
}: {
  catalog: Catalog | undefined;
  item: ClosetItem;
  selected: boolean;
  onSelect: () => void;
  onInspect: () => void;
}) {
  const t = useT();
  const labels = t.app.modules.closet;
  const image = compositionImage(catalog, item);

  return (
    <div className="group relative">
      <button
        type="button"
        onClick={onSelect}
        // ── Why the name is spelled out instead of read off the markup ──
        // Without it the accessible name is whatever the caption happens to
        // concatenate, which is both unstable and — because the caption
        // renders `brand · colour` — indistinguishable between two shirts
        // from the same brand. Naming it explicitly gives a screen reader
        // the three facts that identify the garment, in a fixed order.
        aria-label={[item.name, item.brand, item.primary_color]
          .filter(Boolean)
          .join(", ")}
        // The state, because the ring that shows it is purely visual.
        // Without this a screen reader hears forty tiles and no way to tell
        // which ones are in the look.
        aria-pressed={selected}
        className={cn(
          "flex w-full flex-col overflow-hidden rounded-xl border bg-(--color-card) text-left",
          "transition-[border-color,box-shadow,transform] duration-200",
          "[transition-timing-function:var(--ease-premium)]",
          "hover:-translate-y-px hover:shadow-(--shadow-soft)",
          "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-(--color-ring)/50",
          selected
            ? "border-(--color-accent) shadow-(--shadow-glow)"
            : "border-(--color-border)",
        )}
      >
        <div className="relative w-full bg-(--color-muted)/30">
          <PieceImage
            item={item}
            image={image}
            className="h-32 w-full sm:h-36"
            imageClassName="p-2"
          />
          {selected ? (
            <span
              aria-hidden
              className="absolute right-1.5 top-1.5 flex size-5 items-center justify-center rounded-full bg-(--color-accent) text-(--color-accent-foreground)"
            >
              <Check className="size-3" />
            </span>
          ) : null}
          {item.favorite ? (
            <span
              aria-hidden
              className="absolute left-1.5 top-1.5 text-(--color-accent)"
            >
              <Heart className="size-3.5 fill-current" />
            </span>
          ) : null}
        </div>
        <span className="flex flex-col gap-0.5 px-2.5 py-2">
          <span className="truncate text-[12.5px] font-medium text-(--color-foreground)">
            {item.name}
          </span>
          <span className="truncate font-mono text-[10px] uppercase tracking-[0.14em] text-(--color-muted-foreground)">
            {[item.brand, item.primary_color].filter(Boolean).join(" · ")}
          </span>
        </span>
      </button>

      <button
        type="button"
        onClick={(event) => {
          // Without this the click bubbles to the tile and opening the
          // detail panel also swaps the piece into the look — which is the
          // exact behaviour the sprint asks not to happen.
          event.stopPropagation();
          onInspect();
        }}
        aria-label={labels.editPiece.replace("{name}", item.name)}
        className={cn(
          "absolute bottom-1.5 right-1.5 flex size-6 items-center justify-center rounded-lg",
          "border border-(--color-border) bg-(--color-card) text-(--color-muted-foreground)",
          "opacity-0 transition-opacity hover:text-(--color-foreground)",
          // Always visible on touch, where there is no hover to reveal it.
          "group-hover:opacity-100 focus-visible:opacity-100 [@media(hover:none)]:opacity-100",
        )}
      >
        <Pencil className="size-3" />
      </button>
    </div>
  );
}
