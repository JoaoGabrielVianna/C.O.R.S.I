import { X } from "lucide-react";

import { cn } from "@/lib/utils";
import { useT } from "@/lib/i18n";

import type { Catalog, ClosetItem, Slot } from "../api/types";
import { compositionImage, inSlot, type Composition } from "../composition";
import { slotLabel } from "../labels";
import { PieceImage } from "./PieceImage";

/**
 * The stage: the look as it is right now, one row per slot.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   THE ROWS ARE THE SERVER'S SLOTS, IN THE SERVER'S ORDER
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * Both come from `GET /closet/catalog`. This component names no slot and
 * decides no order — which is what makes a look saved today and re-opened
 * next year draw the same way, and what makes an added slot appear here
 * without touching this file.
 *
 * ── Why empty slots are drawn ──────────────────────────────────────────
 * An outfit with no shoes has to LOOK like an outfit with no shoes. Hiding
 * empty rows would make a half-built look indistinguishable from a
 * finished one, and the row is also the affordance: it is where the eye
 * goes when the operator wonders what is still missing.
 */
export function LookStage({
  catalog,
  composition,
  onRemove,
  className,
}: {
  catalog: Catalog | undefined;
  composition: Composition;
  onRemove: (itemId: string) => void;
  className?: string;
}) {
  const t = useT();
  const labels = t.app.modules.closet;

  return (
    <div className={cn("flex flex-col gap-1.5", className)}>
      {catalog?.slots.map((def) => {
        const filled = inSlot(composition, def.slot);
        return (
          <SlotRow
            key={def.slot}
            slot={def.slot}
            label={slotLabel(t, def.slot)}
            emptyLabel={labels.slotEmpty}
            catalog={catalog}
            filled={filled.map((p) => p.item)}
            onRemove={onRemove}
            removeLabel={labels.removePiece}
          />
        );
      })}
    </div>
  );
}

function SlotRow({
  slot,
  label,
  emptyLabel,
  catalog,
  filled,
  onRemove,
  removeLabel,
}: {
  slot: Slot;
  label: string;
  emptyLabel: string;
  catalog: Catalog | undefined;
  filled: ClosetItem[];
  onRemove: (itemId: string) => void;
  removeLabel: string;
}) {
  return (
    <div
      data-slot={slot}
      className={cn(
        "flex items-center gap-3 rounded-xl border px-3 py-2",
        filled.length > 0
          ? "border-(--color-border) bg-(--color-card)"
          : "border-dashed border-(--color-border) bg-(--color-card)/40",
      )}
    >
      <span className="w-16 shrink-0 font-mono text-[10px] uppercase leading-tight tracking-[0.14em] text-(--color-muted-foreground)">
        {label}
      </span>

      {filled.length === 0 ? (
        <span className="text-[12.5px] text-(--color-muted-foreground)">{emptyLabel}</span>
      ) : (
        <ul className="flex min-w-0 flex-1 items-center gap-2 overflow-x-auto [scrollbar-width:none] [&::-webkit-scrollbar]:hidden">
          {filled.map((item) => (
            <li key={item.id} className="flex min-w-0 shrink-0 items-center gap-2">
              <span className="flex size-11 shrink-0 items-center justify-center overflow-hidden rounded-lg bg-(--color-muted)/40">
                <PieceImage
                  item={item}
                  image={compositionImage(catalog, item)}
                  className="size-full"
                  imageClassName="p-1"
                  // The row prints the name in full right beside this.
                  namedFallback={false}
                />
              </span>
              <span className="min-w-0 truncate text-[12.5px] text-(--color-foreground)">
                {item.name}
              </span>
              <button
                type="button"
                onClick={() => onRemove(item.id)}
                aria-label={removeLabel.replace("{name}", item.name)}
                className="flex size-5 shrink-0 items-center justify-center rounded-md text-(--color-muted-foreground) transition-colors hover:bg-(--color-muted) hover:text-(--color-foreground)"
              >
                <X className="size-3" />
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
