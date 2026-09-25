/**
 * The Closet wire types, mirroring `internal/closet/domain`.
 *
 * ── Why the vocabularies are `string` and not unions ───────────────────
 * Because the backend owns them and PUBLISHES them, at `GET /closet/catalog`.
 * A union here would be a second declaration of the category list, and its
 * whole purpose would be to go stale: the point of the domain's design is
 * that adding `hats` costs one entry in a Go slice and no migration — and a
 * frontend that had to be recompiled to see it would put the cost straight
 * back.
 *
 * `Slot` is the one exception in spirit and still a string in type: it is a
 * closed vocabulary, but the composition ORDER is the server's too, so the
 * builder reads it from the catalogue rather than spelling it.
 */

export type Category = string;
export type Slot = string;
export type ImageView = string;
export type Occasion = string;

export type Status = "active" | "archived";

/** One angle of one piece. Carries the asset's metadata, never its bytes. */
export interface ItemImage {
  id: string;
  item_id: string;
  view: ImageView;
  asset_id: string;
  content_type: string;
  byte_size: number;
  width: number;
  height: number;
  created_at: string;
  updated_at: string;
}

export interface ClosetItem {
  id: string;
  name: string;
  category: Category;
  subtype?: string;
  primary_color: string;
  secondary_color?: string;
  brand?: string;
  notes?: string;
  favorite: boolean;
  status: Status;
  /** In the order the piece's category declares. Never null. */
  images: ItemImage[];
  created_at: string;
  updated_at: string;
}

/**
 * One filled slot of a look.
 *
 * `item` is the piece as it is RIGHT NOW, joined in by the server. It is
 * optional in the type because the server leaves it out for a slot whose
 * piece did not resolve — which nothing in the product can currently cause,
 * and which a card must still be able to render around.
 */
export interface LookItem {
  slot: Slot;
  position: number;
  item_id: string;
  item?: ClosetItem;
  created_at: string;
}

export interface Look {
  id: string;
  name: string;
  occasion: Occasion;
  favorite: boolean;
  notes?: string;
  status: Status;
  /** In composition order, decided by the server. Never null. */
  items: LookItem[];
  created_at: string;
  updated_at: string;
}

/* ── the catalogue ───────────────────────────────────────────────────── */

export interface CatalogCategory {
  category: Category;
  /** The slot a piece of this category fills in a look. */
  slot: Slot;
  /** Every angle this category may carry, in capture order. */
  views: ImageView[];
  /** The fallback chain the builder walks, first present wins. */
  composition: ImageView[];
}

export interface CatalogSlot {
  slot: Slot;
  /** How many pieces the slot holds. One for all but `accessory`. */
  capacity: number;
}

/**
 * Everything the screens need to know about the vocabulary, from the one
 * place that enforces it.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   NO COMPONENT IN THIS MODULE DECLARES A CATEGORY, A SLOT OR A VIEW
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * The rail, the capture flow, the composition order and the slot
 * capacities all come from here. A hard-coded list would disagree with the
 * server on exactly the day somebody adds a category, which is the day the
 * backend was designed to make cheap.
 */
export interface Catalog {
  categories: CatalogCategory[];
  /** In composition order, bottom of the body upward. */
  slots: CatalogSlot[];
  occasions: Occasion[];
  max_look_items: number;
}

/* ── paged reads ─────────────────────────────────────────────────────── */

export interface Page<T> {
  items: T[];
  /** How many rows the filter matches, ignoring limit and offset. */
  total: number;
}

export interface ArchiveItemResult {
  item: ClosetItem;
  /**
   * How many live looks still reference the piece. Information, never a
   * refusal: the garment left the wardrobe whatever the gallery says.
   */
  looks_affected: number;
}
