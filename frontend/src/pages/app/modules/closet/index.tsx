import { useMemo, useState } from "react";
import { Heart, Plus, Shirt } from "lucide-react";

import { EmptyState } from "@/components/workspace/EmptyState";
import { Button } from "@/components/ui/Button";
import { Input } from "@/components/ui/Input";
import { useT } from "@/lib/i18n";
import { cn } from "@/lib/utils";

import type { ClosetItem, Look } from "@/modules/closet/api/types";
import {
  fromLookItems,
  place,
  remove,
  toItemIDs,
  type Composition,
} from "@/modules/closet/composition";
import { CategoryRail } from "@/modules/closet/components/CategoryRail";
import { occasionLabel } from "@/modules/closet/labels";
import { LooksGallery } from "@/modules/closet/components/LooksGallery";
import { LookStage } from "@/modules/closet/components/LookStage";
import { PieceDialog } from "@/modules/closet/components/PieceDialog";
import { PieceGrid } from "@/modules/closet/components/PieceGrid";
import {
  useArchiveLook,
  useCatalog,
  useCreateLook,
  useItems,
  useLooks,
  useRestoreLook,
  useSetLookItems,
  useUpdateLook,
} from "@/modules/closet/hooks/useCloset";

/**
 * Closet — the wardrobe, and the thing you do with it.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   THE PRIMARY SURFACE IS A SELECTOR, NOT A TABLE
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * Two panes. On the left the LOOK, one row per slot, always visible. On the
 * right the wardrobe: a category rail and a grid of photographs. Clicking a
 * piece puts it in its slot immediately; clicking another of the same kind
 * swaps it. Nothing is saved until the operator says so, and the save sends
 * the pieces in the order they were chosen so the server reaches the same
 * composition the screen is showing.
 *
 * Cataloguing a garment lives in a dialog reached from one button and from
 * the corner of a tile. It is deliberately not the main event.
 *
 * ── What is NOT here, by decision of this sprint ───────────────────────
 * No stylist, no generated imagery, no try-on, no recommendation, no
 * weather, no automatic recognition. The wardrobe comes first and every one
 * of those reads it.
 *
 * ── Drag and drop ──────────────────────────────────────────────────────
 * Not implemented, on purpose. Click-to-place already expresses the whole
 * interaction, works on touch without a long-press, and is reachable from
 * the keyboard. Dragging would add a second way to do the same thing and a
 * pile of pointer handling to keep them agreeing.
 */
export function ClosetPage() {
  const t = useT();
  const labels = t.app.modules.closet;

  const catalogQuery = useCatalog();
  const catalog = catalogQuery.data;

  const [tab, setTab] = useState<"build" | "looks">("build");

  /* ── the wardrobe pane ─────────────────────────────────────────────── */

  const [category, setCategory] = useState<string | undefined>(undefined);
  const [search, setSearch] = useState("");
  const [onlyFavorites, setOnlyFavorites] = useState(false);

  const itemsQuery = useItems({
    category,
    search: search.trim() || undefined,
    favorite: onlyFavorites || undefined,
  });
  // Memoised because `?? []` allocates a new array on every render, and
  // that array is a dependency of `liveDialogItem` below — without this the
  // memo recomputes on every render and the dialog's piece is a new object
  // each time.
  const items = useMemo(() => itemsQuery.data?.items ?? [], [itemsQuery.data]);

  // Counts per category, for the rail. Read from an UNFILTERED listing so
  // the numbers describe the wardrobe rather than the current search —
  // a rail whose counts moved as you typed would be telling you about the
  // query, not about your clothes.
  const allItemsQuery = useItems({});
  const counts = useMemo(() => {
    const out: Record<string, number> = {};
    for (const item of allItemsQuery.data?.items ?? []) {
      out[item.category] = (out[item.category] ?? 0) + 1;
    }
    return out;
  }, [allItemsQuery.data]);

  /* ── the look being built ──────────────────────────────────────────── */

  const [composition, setComposition] = useState<Composition>([]);
  const [editing, setEditing] = useState<Look | undefined>(undefined);
  const [lookName, setLookName] = useState("");
  const [occasion, setOccasion] = useState("other");
  const [favorite, setFavorite] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);

  const selectedIDs = useMemo(
    () => new Set(composition.map((p) => p.item.id)),
    [composition],
  );

  const createLook = useCreateLook();
  const updateLook = useUpdateLook();
  const setLookItems = useSetLookItems();
  const archiveLook = useArchiveLook();
  const restoreLook = useRestoreLook();

  const looksQuery = useLooks({ includeArchived: true });
  const looks = looksQuery.data?.items ?? [];

  function resetBuilder() {
    setComposition([]);
    setEditing(undefined);
    setLookName("");
    setOccasion("other");
    setFavorite(false);
    setSaveError(null);
  }

  /**
   * Loads a saved look into the builder.
   *
   * `keepIdentity` is the whole difference between editing and starting a
   * variation: with it the save writes back to the same look, without it
   * the save creates a second one.
   */
  function loadLook(look: Look, keepIdentity: boolean) {
    setComposition(fromLookItems(look.items));
    setEditing(keepIdentity ? look : undefined);
    setLookName(keepIdentity ? look.name : labels.copyOf.replace("{name}", look.name));
    setOccasion(look.occasion);
    setFavorite(keepIdentity ? look.favorite : false);
    setSaveError(null);
    setTab("build");
  }

  async function handleSave() {
    setSaveError(null);
    const name = lookName.trim();
    if (!name) {
      setSaveError(labels.nameRequired);
      return;
    }
    try {
      if (editing) {
        // Two writes, because they are two decisions: the metadata and the
        // composition. The composition goes last so a failure there leaves
        // a renamed look rather than a look renamed to nothing.
        await updateLook.mutateAsync({
          id: editing.id,
          body: { name, occasion, favorite },
        });
        const saved = await setLookItems.mutateAsync({
          id: editing.id,
          itemIDs: toItemIDs(composition),
        });
        // Re-seeded from the SERVER's answer, which is the one that counts:
        // it re-ran capacity and eviction, and the screen should now be
        // showing what is actually stored.
        setComposition(fromLookItems(saved.items));
        setEditing(saved);
      } else {
        const created = await createLook.mutateAsync({
          name,
          occasion,
          favorite,
          item_ids: toItemIDs(composition),
        });
        setComposition(fromLookItems(created.items));
        setEditing(created);
      }
    } catch (err) {
      setSaveError(err instanceof Error ? err.message : labels.saveFailed);
    }
  }

  /* ── the piece dialog ──────────────────────────────────────────────── */

  const [dialogOpen, setDialogOpen] = useState(false);
  const [dialogItem, setDialogItem] = useState<ClosetItem | undefined>(undefined);

  // The dialog renders the piece as the CACHE currently knows it, so an
  // image attached inside it appears in its own view slots without the
  // dialog having to re-fetch or hold a copy.
  const liveDialogItem = useMemo(() => {
    if (!dialogItem) return undefined;
    return (
      items.find((i) => i.id === dialogItem.id) ??
      allItemsQuery.data?.items.find((i) => i.id === dialogItem.id) ??
      dialogItem
    );
  }, [dialogItem, items, allItemsQuery.data]);

  const saving = createLook.isPending || updateLook.isPending || setLookItems.isPending;

  return (
    <div className="flex flex-col gap-4">
      <header className="flex shrink-0 flex-wrap items-end justify-between gap-3 border-b border-(--color-border) pb-3">
        <div className="min-w-0">
          <p className="font-mono text-[10px] uppercase tracking-[0.18em] text-(--color-muted-foreground)">
            {labels.badge}
          </p>
          <h1 className="mt-0.5 font-display text-xl font-semibold tracking-tight text-(--color-foreground) sm:text-2xl">
            {labels.title}
          </h1>
          <p className="mt-1 max-w-2xl text-[12.5px] text-(--color-muted-foreground)">
            {labels.description}
          </p>
        </div>
        <div className="flex shrink-0 items-center gap-2">
          <Tabs tab={tab} onChange={setTab} buildLabel={labels.buildTab} looksLabel={labels.looksTab} />
          <button
            type="button"
            onClick={() => {
              setDialogItem(undefined);
              setDialogOpen(true);
            }}
            className="inline-flex shrink-0 items-center gap-1.5 rounded-lg border border-(--color-border) bg-(--color-card) px-3 py-1.5 text-[12.5px] font-medium text-(--color-foreground) shadow-(--shadow-soft) transition-colors hover:bg-(--color-muted)"
          >
            <Plus className="size-3.5" />
            <span className="hidden sm:inline">{labels.addPiece}</span>
            <span className="sm:hidden">{labels.addPieceShort}</span>
          </button>
        </div>
      </header>

      {catalogQuery.isError ? (
        <p role="alert" className="rounded-lg border border-(--color-border) bg-(--color-card) px-3 py-2 text-[12.5px]">
          {labels.catalogFailed}
        </p>
      ) : null}

      {tab === "looks" ? (
        <LooksGallery
          catalog={catalog}
          looks={looks}
          loading={looksQuery.isLoading}
          onOpen={(look) => loadLook(look, true)}
          onDuplicate={(look) => loadLook(look, false)}
          onToggleFavorite={(look) =>
            updateLook.mutate({ id: look.id, body: { favorite: !look.favorite } })
          }
          onArchive={(look) => archiveLook.mutate(look.id)}
          onRestore={(look) => restoreLook.mutate(look.id)}
          onCreateFirst={() => {
            resetBuilder();
            setTab("build");
          }}
        />
      ) : (
        <div className="grid grid-cols-1 gap-4 lg:grid-cols-[minmax(0,22rem)_minmax(0,1fr)]">
          {/* ── the look ────────────────────────────────────────────── */}
          <section
            aria-label={labels.currentLook}
            className="flex flex-col gap-3 lg:sticky lg:top-4 lg:self-start"
          >
            <div className="rounded-2xl border border-(--color-border) bg-(--color-card) p-3">
              <div className="flex items-center justify-between gap-2">
                <h2 className="font-mono text-[10px] uppercase tracking-[0.16em] text-(--color-muted-foreground)">
                  {editing ? labels.editingLook : labels.newLook}
                </h2>
                {composition.length > 0 || editing ? (
                  <button
                    type="button"
                    onClick={resetBuilder}
                    className="text-[11.5px] text-(--color-muted-foreground) underline-offset-2 hover:text-(--color-foreground) hover:underline"
                  >
                    {labels.startOver}
                  </button>
                ) : null}
              </div>

              <div className="mt-2.5">
                <LookStage
                  catalog={catalog}
                  composition={composition}
                  onRemove={(itemId) => setComposition((c) => remove(c, itemId))}
                />
              </div>

              <div className="mt-3 flex flex-col gap-2 border-t border-(--color-border) pt-3">
                <Input
                  value={lookName}
                  onChange={(e) => setLookName(e.target.value)}
                  placeholder={labels.lookNamePlaceholder}
                  aria-label={labels.lookName}
                />
                <div className="flex items-center gap-2">
                  <select
                    value={occasion}
                    onChange={(e) => setOccasion(e.target.value)}
                    aria-label={labels.occasion}
                    className="h-9 flex-1 rounded-xl border border-(--color-border) bg-(--color-card) px-3 text-[12.5px] text-(--color-foreground) outline-none focus:border-(--color-brand-500)"
                  >
                    {catalog?.occasions.map((value) => (
                      <option key={value} value={value}>
                        {occasionLabel(t, value)}
                      </option>
                    ))}
                  </select>
                  <button
                    type="button"
                    onClick={() => setFavorite((v) => !v)}
                    aria-pressed={favorite}
                    aria-label={labels.favoriteThisLook}
                    className={cn(
                      "flex size-9 items-center justify-center rounded-xl border transition-colors",
                      favorite
                        ? "border-(--color-accent) text-(--color-accent)"
                        : "border-(--color-border) text-(--color-muted-foreground) hover:text-(--color-foreground)",
                    )}
                  >
                    <Heart className={cn("size-4", favorite && "fill-current")} />
                  </button>
                </div>

                {saveError ? (
                  <p role="alert" className="text-[11.5px] text-(--color-destructive)">
                    {saveError}
                  </p>
                ) : null}

                <Button size="sm" disabled={saving} onClick={handleSave}>
                  {saving ? labels.saving : editing ? labels.saveChanges : labels.saveLook}
                </Button>
              </div>
            </div>
          </section>

          {/* ── the wardrobe ────────────────────────────────────────── */}
          <section aria-label={labels.wardrobe} className="flex min-w-0 flex-col gap-3">
            <CategoryRail
              catalog={catalog}
              active={category}
              onSelect={setCategory}
              counts={allItemsQuery.isSuccess ? counts : undefined}
            />

            <div className="flex items-center gap-2">
              <Input
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                placeholder={labels.searchPlaceholder}
                aria-label={labels.searchPieces}
                className="h-9 text-[12.5px]"
              />
              <button
                type="button"
                onClick={() => setOnlyFavorites((v) => !v)}
                aria-pressed={onlyFavorites}
                aria-label={labels.onlyFavorites}
                className={cn(
                  "flex size-9 shrink-0 items-center justify-center rounded-xl border transition-colors",
                  onlyFavorites
                    ? "border-(--color-accent) text-(--color-accent)"
                    : "border-(--color-border) text-(--color-muted-foreground) hover:text-(--color-foreground)",
                )}
              >
                <Heart className={cn("size-4", onlyFavorites && "fill-current")} />
              </button>
            </div>

            {itemsQuery.isLoading ? (
              <ul className="grid grid-cols-2 gap-2.5 sm:grid-cols-3 xl:grid-cols-4">
                {[0, 1, 2, 3, 4, 5].map((i) => (
                  <li
                    key={i}
                    className="h-44 animate-pulse rounded-xl border border-(--color-border) bg-(--color-muted)/30"
                  />
                ))}
              </ul>
            ) : itemsQuery.isError ? (
              <p role="alert" className="rounded-lg border border-(--color-border) bg-(--color-card) px-3 py-2 text-[12.5px]">
                {labels.itemsFailed}
              </p>
            ) : items.length === 0 ? (
              <EmptyState
                icon={Shirt}
                title={
                  search || category || onlyFavorites
                    ? labels.noMatchTitle
                    : labels.emptyTitle
                }
                description={
                  search || category || onlyFavorites ? labels.noMatchBody : labels.emptyBody
                }
                action={
                  <button
                    type="button"
                    onClick={() => {
                      setDialogItem(undefined);
                      setDialogOpen(true);
                    }}
                    className="inline-flex items-center gap-1.5 rounded-lg border border-(--color-border) bg-(--color-card) px-3 py-1.5 text-[12.5px] font-medium text-(--color-foreground) transition-colors hover:bg-(--color-muted)"
                  >
                    <Plus className="size-3.5" />
                    {labels.addPiece}
                  </button>
                }
              />
            ) : (
              <PieceGrid
                catalog={catalog}
                items={items}
                selectedIDs={selectedIDs}
                onSelect={(item) => setComposition((c) => place(catalog, c, item))}
                onInspect={(item) => {
                  setDialogItem(item);
                  setDialogOpen(true);
                }}
              />
            )}
          </section>
        </div>
      )}

      <PieceDialog
        open={dialogOpen}
        onClose={() => setDialogOpen(false)}
        catalog={catalog}
        item={liveDialogItem}
        // Creating keeps the dialog open on the new piece, so its view slots
        // appear where the form was and the operator can attach photographs
        // without finding the tile again.
        onCreated={(created) => setDialogItem(created)}
      />
    </div>
  );
}

function Tabs({
  tab,
  onChange,
  buildLabel,
  looksLabel,
}: {
  tab: "build" | "looks";
  onChange: (tab: "build" | "looks") => void;
  buildLabel: string;
  looksLabel: string;
}) {
  return (
    <div className="inline-flex rounded-lg border border-(--color-border) bg-(--color-card) p-0.5">
      {(
        [
          ["build", buildLabel],
          ["looks", looksLabel],
        ] as const
      ).map(([value, label]) => (
        <button
          key={value}
          type="button"
          onClick={() => onChange(value)}
          aria-pressed={tab === value}
          className={cn(
            "rounded-md px-2.5 py-1 text-[12.5px] font-medium transition-colors",
            tab === value
              ? "bg-(--color-muted) text-(--color-foreground)"
              : "text-(--color-muted-foreground) hover:text-(--color-foreground)",
          )}
        >
          {label}
        </button>
      ))}
    </div>
  );
}
