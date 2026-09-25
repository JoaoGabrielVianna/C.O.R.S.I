import { useRef, useState, type FormEvent, type ReactNode } from "react";
import { Archive, ArchiveRestore, Heart, Trash2, Upload } from "lucide-react";

import { Button } from "@/components/ui/Button";
import { Input } from "@/components/ui/Input";
import { useT } from "@/lib/i18n";
import { cn } from "@/lib/utils";

import type { Catalog, ClosetItem } from "../api/types";
import {
  useArchiveItem,
  useCreateItem,
  useRemoveImage,
  useRestoreItem,
  useUpdateItem,
  useUploadImage,
} from "../hooks/useCloset";
import { categoryLabel, viewLabel } from "../labels";
import { Modal } from "./Modal";
import { PieceImage } from "./PieceImage";

/**
 * Cataloguing and editing a piece.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   THIS IS THE ADMINISTRATIVE SURFACE, AND IT IS DELIBERATELY SECONDARY
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * It is a dialog and not a page, it is reached from a corner of a tile or
 * one button in the header, and closing it returns the operator to the
 * thing the module is actually for. A wardrobe whose main screen is a form
 * is a CRUD with pictures.
 *
 * ── Why the views only appear after the piece exists ───────────────────
 * An image is attached to an item id, so there is nothing to attach to
 * until the row is created. Rather than invent a staging area, creating
 * switches this same dialog into its editing mode with the new piece
 * loaded: the operator types a name, presses save, and the four view slots
 * appear where the form was. One dialog, two states, no upload queue to
 * reconcile if the create fails.
 */
export function PieceDialog({
  open,
  onClose,
  catalog,
  /** The piece being edited. Undefined opens the dialog in create mode. */
  item,
  /** Called with the piece that was just created, so the caller can keep it. */
  onCreated,
}: {
  open: boolean;
  onClose: () => void;
  catalog: Catalog | undefined;
  item?: ClosetItem;
  onCreated?: (item: ClosetItem) => void;
}) {
  const t = useT();
  const labels = t.app.modules.closet;
  const form = labels.form;

  const archive = useArchiveItem();
  const restore = useRestoreItem();

  return (
    <Modal
      open={open}
      onClose={onClose}
      wide={Boolean(item)}
      eyebrow={labels.title}
      title={item ? form.editTitle : form.createTitle}
      description={item ? form.editSubtitle : form.createSubtitle}
      // `close` and not `cancel`: the X and the Cancel button do the same
      // thing and must not share an accessible name, or a dialog announces
      // two identical controls and nothing can address either one.
      closeLabel={form.close}
    >
      <div className="max-h-[70vh] overflow-y-auto">
        {/*
          ── Why the form is keyed and has no reset effect ────────────────
          Its fields are seeded from the piece, and the piece CHANGES
          identity on every cache refresh — attaching an image produces a
          new object for the same garment. An effect that re-seeded on
          `item` would therefore throw away whatever the operator had typed
          the moment a photograph finished uploading.

          Keying by the piece's ID gets both halves right: switching to a
          different garment remounts with fresh fields, and a refresh of the
          SAME garment does not. Closing the dialog unmounts it, so
          reopening is also fresh.
        */}
        <PieceForm
          key={item?.id ?? "new"}
          catalog={catalog}
          item={item}
          onClose={onClose}
          onCreated={onCreated}
        />

        {item ? (
          <>
            <ViewsSection item={item} catalog={catalog} />
            <footer className="flex items-center justify-between gap-3 border-t border-(--color-border) px-5 py-3">
              <p className="text-[11.5px] text-(--color-muted-foreground)">
                {item.status === "archived" ? form.archivedNote : form.archiveNote}
              </p>
              {item.status === "archived" ? (
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  disabled={restore.isPending}
                  onClick={() => restore.mutate(item.id)}
                >
                  <ArchiveRestore className="size-3.5" />
                  {form.restore}
                </Button>
              ) : (
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  disabled={archive.isPending}
                  onClick={() => archive.mutate(item.id)}
                >
                  <Archive className="size-3.5" />
                  {form.archive}
                </Button>
              )}
            </footer>
          </>
        ) : null}
      </div>
    </Modal>
  );
}

/** The fields. Mounted fresh per piece — see the key at the call site. */
function PieceForm({
  catalog,
  item,
  onClose,
  onCreated,
}: {
  catalog: Catalog | undefined;
  item?: ClosetItem;
  onClose: () => void;
  onCreated?: (item: ClosetItem) => void;
}) {
  const t = useT();
  const form = t.app.modules.closet.form;

  const create = useCreateItem();
  const update = useUpdateItem();

  const [name, setName] = useState(item?.name ?? "");
  const [category, setCategory] = useState(
    item?.category ?? catalog?.categories[0]?.category ?? "",
  );
  const [subtype, setSubtype] = useState(item?.subtype ?? "");
  const [primaryColor, setPrimaryColor] = useState(item?.primary_color ?? "");
  const [secondaryColor, setSecondaryColor] = useState(item?.secondary_color ?? "");
  const [brand, setBrand] = useState(item?.brand ?? "");
  const [notes, setNotes] = useState(item?.notes ?? "");
  const [favorite, setFavorite] = useState(item?.favorite ?? false);
  const [error, setError] = useState<string | null>(null);

  const body = {
    name: name.trim(),
    category,
    subtype: subtype.trim(),
    primary_color: primaryColor.trim(),
    secondary_color: secondaryColor.trim(),
    brand: brand.trim(),
    notes: notes.trim(),
    favorite,
  };

  async function handleSubmit(event: FormEvent) {
    event.preventDefault();
    setError(null);
    // Checked here as well as on the server so the operator hears about a
    // blank name without a round trip. The server still refuses it: this is
    // an affordance, not the rule.
    if (!body.name || !body.primary_color || !body.category) {
      setError(form.required);
      return;
    }
    try {
      if (item) {
        await update.mutateAsync({ id: item.id, body });
        onClose();
      } else {
        const created = await create.mutateAsync(body);
        onCreated?.(created);
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : form.failed);
    }
  }

  const saving = create.isPending || update.isPending;

  return (
    <form onSubmit={handleSubmit} className="space-y-3 px-5 py-4">
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <Field label={form.name}>
          <Input value={name} onChange={(e) => setName(e.target.value)} autoFocus />
        </Field>
        <Field label={form.category}>
          <select
            value={category}
            onChange={(e) => setCategory(e.target.value)}
            className={selectClasses}
          >
            {catalog?.categories.map((def) => (
              <option key={def.category} value={def.category}>
                {categoryLabel(t, def.category)}
              </option>
            ))}
          </select>
        </Field>
        <Field label={form.primaryColor}>
          <Input value={primaryColor} onChange={(e) => setPrimaryColor(e.target.value)} />
        </Field>
        <Field label={form.secondaryColor}>
          <Input value={secondaryColor} onChange={(e) => setSecondaryColor(e.target.value)} />
        </Field>
        <Field label={form.brand}>
          <Input value={brand} onChange={(e) => setBrand(e.target.value)} />
        </Field>
        <Field label={form.subtype}>
          <Input value={subtype} onChange={(e) => setSubtype(e.target.value)} />
        </Field>
      </div>

      <Field label={form.notes}>
        <textarea
          value={notes}
          onChange={(e) => setNotes(e.target.value)}
          rows={2}
          className={cn(selectClasses, "h-auto py-2")}
        />
      </Field>

      <button
        type="button"
        onClick={() => setFavorite((v) => !v)}
        aria-pressed={favorite}
        className={cn(
          "inline-flex items-center gap-1.5 rounded-lg border px-2.5 py-1.5 text-[12.5px] transition-colors",
          favorite
            ? "border-(--color-accent) text-(--color-accent)"
            : "border-(--color-border) text-(--color-muted-foreground) hover:text-(--color-foreground)",
        )}
      >
        <Heart className={cn("size-3.5", favorite && "fill-current")} />
        {form.favorite}
      </button>

      {error ? (
        <p role="alert" className="text-[12.5px] text-(--color-destructive)">
          {error}
        </p>
      ) : null}

      <div className="flex items-center justify-end gap-2 pt-1">
        <Button type="button" variant="ghost" size="sm" onClick={onClose}>
          {form.cancel}
        </Button>
        <Button type="submit" size="sm" disabled={saving}>
          {saving ? form.saving : item ? form.save : form.create}
        </Button>
      </div>
    </form>
  );
}

/**
 * The views of one piece: one slot per angle its category allows.
 *
 * ── Why the slots come from the catalogue ──────────────────────────────
 * A watch offers `front` and `detail`, a shoe offers `side`, `top` and
 * `front`, and a shirt offers four. Hard-coding four slots would put a
 * hanger view on a watch, which the server would refuse — so the operator
 * would be offered an upload that cannot succeed.
 */
function ViewsSection({
  item,
  catalog,
}: {
  item: ClosetItem;
  catalog: Catalog | undefined;
}) {
  const t = useT();
  const form = t.app.modules.closet.form;
  const views = catalog?.categories.find((c) => c.category === item.category)?.views ?? [];

  return (
    <section className="border-t border-(--color-border) px-5 py-4">
      <h3 className="font-mono text-[10px] uppercase tracking-[0.16em] text-(--color-muted-foreground)">
        {form.views}
      </h3>
      <p className="mt-1 text-[11.5px] text-(--color-muted-foreground)">{form.viewsNote}</p>
      <ul className="mt-3 grid grid-cols-2 gap-2.5 sm:grid-cols-4">
        {views.map((view) => (
          <li key={view}>
            <ViewSlot item={item} view={view} label={viewLabel(t, view)} />
          </li>
        ))}
      </ul>
    </section>
  );
}

function ViewSlot({
  item,
  view,
  label,
}: {
  item: ClosetItem;
  view: string;
  label: string;
}) {
  const t = useT();
  const form = t.app.modules.closet.form;
  const upload = useUploadImage();
  const remove = useRemoveImage();
  const inputRef = useRef<HTMLInputElement>(null);
  const [error, setError] = useState<string | null>(null);

  const existing = item.images.find((img) => img.view === view);

  return (
    <div className="flex flex-col gap-1.5">
      <div className="relative overflow-hidden rounded-xl border border-(--color-border) bg-(--color-muted)/30">
        <PieceImage
          item={item}
          image={existing}
          className="h-24 w-full"
          imageClassName="p-1.5"
        />
        <button
          type="button"
          onClick={() => inputRef.current?.click()}
          disabled={upload.isPending}
          aria-label={form.attachView.replace("{view}", label)}
          className="absolute inset-0 flex items-center justify-center bg-black/0 text-transparent transition-colors hover:bg-black/45 hover:text-white"
        >
          <Upload className="size-4" />
        </button>
        {existing ? (
          <button
            type="button"
            onClick={() => remove.mutate({ itemId: item.id, view })}
            disabled={remove.isPending}
            aria-label={form.removeView.replace("{view}", label)}
            className="absolute right-1 top-1 flex size-5 items-center justify-center rounded-md border border-(--color-border) bg-(--color-card) text-(--color-muted-foreground) transition-colors hover:text-(--color-destructive)"
          >
            <Trash2 className="size-3" />
          </button>
        ) : null}
      </div>

      <span className="font-mono text-[10px] uppercase tracking-[0.14em] text-(--color-muted-foreground)">
        {label}
      </span>
      {error ? (
        <span role="alert" className="text-[11px] text-(--color-destructive)">
          {error}
        </span>
      ) : null}

      <input
        ref={inputRef}
        type="file"
        // A hint to the picker, not a check. The server decides what a file
        // IS by decoding it, and `accept` is trivially bypassed — its only
        // job is to save the operator from browsing through their videos.
        accept="image/png"
        className="hidden"
        onChange={(event) => {
          const file = event.target.files?.[0];
          // The input is cleared unconditionally so choosing the SAME file
          // again still fires a change event. Without this, re-attaching
          // after a failed upload silently does nothing.
          event.target.value = "";
          if (!file) return;
          setError(null);
          upload.mutate(
            { itemId: item.id, view, file },
            {
              onError: (err) =>
                setError(err instanceof Error ? err.message : form.uploadFailed),
            },
          );
        }}
      />
    </div>
  );
}

const selectClasses = cn(
  "flex h-10 w-full rounded-xl border border-(--color-border) bg-(--color-card) px-3.5 py-2 text-sm",
  "text-(--color-foreground) shadow-(--shadow-soft) outline-none",
  "transition-[border-color,box-shadow] duration-[250ms] [transition-timing-function:var(--ease-premium)]",
  "focus:border-(--color-brand-500) focus:ring-2 focus:ring-(--color-brand-500)/20",
);

/**
 * A labelled control.
 *
 * ── Why the label WRAPS the control ────────────────────────────────────
 * A `<Label>` beside an input with no `htmlFor` is a caption, not a label:
 * clicking it does not focus the field and assistive technology reports the
 * input as unnamed. Wrapping associates the two implicitly, which works for
 * whatever control the caller passes without this component having to mint
 * and thread an id.
 */
function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <label className="block space-y-1.5">
      <span className="text-sm font-medium leading-none text-(--color-slate-700) dark:text-(--color-slate-300)">
        {label}
      </span>
      {children}
    </label>
  );
}
