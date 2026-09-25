import { cn } from "@/lib/utils";

import type { ClosetItem, ItemImage } from "../api/types";
import { useAssetObjectURL } from "../hooks/useAssetObjectURL";

/**
 * Draws one photograph of one piece, with the three states it actually has.
 *
 * ── Why "no photograph" is a first-class state and not an error ────────
 * Cataloguing a garment and photographing it are separate acts, and forcing
 * them into one means a wardrobe nobody finishes entering. A piece with no
 * image is drawn as its name on a plain surface: it is still selectable, it
 * still fills its slot, and the closet still works while the operator is
 * halfway through shooting it.
 *
 * ── Why the box is reserved before the bytes arrive ────────────────────
 * The asset's dimensions come down with the item read, so the aspect ratio
 * is known before the image is. Without that, a grid of forty pieces
 * reflows as each one lands — which is the single thing that makes a
 * gallery feel cheap.
 */
export function PieceImage({
  item,
  image,
  className,
  imageClassName,
  namedFallback = true,
}: {
  item: ClosetItem;
  image: ItemImage | undefined;
  className?: string;
  imageClassName?: string;
  /**
   * Whether the fallback prints the garment's name.
   *
   * False where the name is ALREADY on screen next to the thumbnail — the
   * stage rows, which print it in full. Two copies of the same word inside
   * one row is not just noise: it makes a screen reader say the garment
   * twice, and it makes every query for that name ambiguous.
   */
  namedFallback?: boolean;
}) {
  const asset = useAssetObjectURL(image?.asset_id);

  return (
    <div
      className={cn(
        "relative flex items-center justify-center overflow-hidden",
        className,
      )}
      style={
        image
          ? { aspectRatio: `${image.width} / ${image.height}` }
          : undefined
      }
    >
      {asset.status === "ready" ? (
        <img
          src={asset.url}
          // The garment's own name. A closet is not decorative imagery —
          // somebody navigating by screen reader needs to know which shirt
          // this is, and "closet item" would tell them nothing.
          alt={item.name}
          loading="lazy"
          draggable={false}
          className={cn("size-full object-contain", imageClassName)}
        />
      ) : asset.status === "loading" ? (
        <div className="size-full animate-pulse bg-(--color-muted)" />
      ) : (
        // Both `idle` (never photographed) and `error` (this one did not
        // load) land here. They are shown the same way on purpose: from the
        // operator's side both mean "there is no picture of this right
        // now", and an error banner for one image would be noise.
        <span
          aria-hidden={!namedFallback}
          className="px-2 text-center font-mono text-[10px] uppercase leading-tight tracking-[0.14em] text-(--color-muted-foreground)"
        >
          {namedFallback ? item.name : "—"}
        </span>
      )}
    </div>
  );
}
