import { Archive, ArchiveRestore, Copy, Heart, Pencil } from "lucide-react";

import { EmptyState } from "@/components/workspace/EmptyState";
import { useT } from "@/lib/i18n";
import { cn } from "@/lib/utils";

import type { Catalog, Look } from "../api/types";
import { compositionImage } from "../composition";
import { occasionLabel } from "../labels";
import { PieceImage } from "./PieceImage";

/**
 * The saved looks.
 *
 * ── Why a card is the pieces and not a name ────────────────────────────
 * A look is recognised by what is in it. A list of names would make the
 * gallery a table of strings the operator has to decode, so the card draws
 * the actual garments, in the server's composition order, and the name is
 * a caption under them.
 *
 * ── Why "start a new one from this" is here ────────────────────────────
 * Because it is the cheapest useful thing in the whole module: it loads the
 * composition into the builder WITHOUT the look's id, so saving creates a
 * second look rather than overwriting the first. Most outfits are a
 * variation on one that worked.
 */
export function LooksGallery({
  catalog,
  looks,
  loading,
  onOpen,
  onDuplicate,
  onToggleFavorite,
  onArchive,
  onRestore,
  onCreateFirst,
}: {
  catalog: Catalog | undefined;
  looks: Look[];
  loading: boolean;
  onOpen: (look: Look) => void;
  onDuplicate: (look: Look) => void;
  onToggleFavorite: (look: Look) => void;
  onArchive: (look: Look) => void;
  onRestore: (look: Look) => void;
  onCreateFirst: () => void;
}) {
  const t = useT();
  const labels = t.app.modules.closet;

  if (loading) {
    return (
      <ul className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-3">
        {[0, 1, 2].map((i) => (
          <li
            key={i}
            className="h-44 animate-pulse rounded-2xl border border-(--color-border) bg-(--color-muted)/30"
          />
        ))}
      </ul>
    );
  }

  if (looks.length === 0) {
    return (
      <EmptyState
        title={labels.looksEmptyTitle}
        description={labels.looksEmptyBody}
        action={
          <button
            type="button"
            onClick={onCreateFirst}
            className="inline-flex items-center gap-1.5 rounded-lg border border-(--color-border) bg-(--color-card) px-3 py-1.5 text-[12.5px] font-medium text-(--color-foreground) transition-colors hover:bg-(--color-muted)"
          >
            {labels.buildLook}
          </button>
        }
      />
    );
  }

  return (
    <ul className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-3">
      {looks.map((look) => (
        <li key={look.id}>
          <article
            className={cn(
              "flex h-full flex-col overflow-hidden rounded-2xl border bg-(--color-card)",
              look.status === "archived"
                ? "border-dashed border-(--color-border) opacity-70"
                : "border-(--color-border)",
            )}
          >
            <button
              type="button"
              onClick={() => onOpen(look)}
              className="flex items-center gap-1.5 overflow-x-auto border-b border-(--color-border) bg-(--color-muted)/20 p-3 text-left [scrollbar-width:none] [&::-webkit-scrollbar]:hidden"
            >
              {look.items.length === 0 ? (
                <span className="py-4 text-[12.5px] text-(--color-muted-foreground)">
                  {labels.lookEmpty}
                </span>
              ) : (
                look.items.map((entry) =>
                  entry.item ? (
                    <span
                      key={`${entry.slot}-${entry.position}`}
                      className="flex size-14 shrink-0 items-center justify-center overflow-hidden rounded-lg bg-(--color-card)"
                    >
                      <PieceImage
                        item={entry.item}
                        image={compositionImage(catalog, entry.item)}
                        className="size-full"
                        imageClassName="p-1"
                      />
                    </span>
                  ) : null,
                )
              )}
            </button>

            <div className="flex flex-1 flex-col gap-2 px-3 py-2.5">
              <div className="min-w-0">
                <p className="truncate text-[13px] font-medium text-(--color-foreground)">
                  {look.name}
                </p>
                <p className="font-mono text-[10px] uppercase tracking-[0.14em] text-(--color-muted-foreground)">
                  {occasionLabel(t, look.occasion)} ·{" "}
                  {labels.pieceCount.replace("{count}", String(look.items.length))}
                </p>
              </div>

              <div className="mt-auto flex items-center gap-1">
                <IconAction
                  label={labels.editLook.replace("{name}", look.name)}
                  onClick={() => onOpen(look)}
                >
                  <Pencil className="size-3.5" />
                </IconAction>
                <IconAction
                  label={labels.duplicateLook.replace("{name}", look.name)}
                  onClick={() => onDuplicate(look)}
                >
                  <Copy className="size-3.5" />
                </IconAction>
                <IconAction
                  label={labels.favoriteLook.replace("{name}", look.name)}
                  pressed={look.favorite}
                  onClick={() => onToggleFavorite(look)}
                >
                  <Heart className={cn("size-3.5", look.favorite && "fill-current")} />
                </IconAction>
                {look.status === "archived" ? (
                  <IconAction
                    label={labels.restoreLook.replace("{name}", look.name)}
                    onClick={() => onRestore(look)}
                  >
                    <ArchiveRestore className="size-3.5" />
                  </IconAction>
                ) : (
                  <IconAction
                    label={labels.archiveLook.replace("{name}", look.name)}
                    onClick={() => onArchive(look)}
                  >
                    <Archive className="size-3.5" />
                  </IconAction>
                )}
              </div>
            </div>
          </article>
        </li>
      ))}
    </ul>
  );
}

function IconAction({
  label,
  pressed,
  onClick,
  children,
}: {
  label: string;
  pressed?: boolean;
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-label={label}
      aria-pressed={pressed}
      className={cn(
        "flex size-7 items-center justify-center rounded-lg border border-(--color-border) transition-colors",
        pressed
          ? "text-(--color-accent)"
          : "text-(--color-muted-foreground) hover:bg-(--color-muted) hover:text-(--color-foreground)",
      )}
    >
      {children}
    </button>
  );
}
