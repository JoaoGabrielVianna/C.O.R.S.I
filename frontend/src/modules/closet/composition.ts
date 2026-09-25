/**
 * The builder's local composition.
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 *   THIS IS A PREVIEW OF THE SERVER'S ANSWER, NOT A SECOND AUTHORITY
 *
 * ══════════════════════════════════════════════════════════════════════
 *
 * Clicking a second shirt has to swap the shirt on screen in the same frame,
 * and a round trip per click would make the closet feel like a form. So the
 * builder keeps the chosen pieces locally and saves once.
 *
 * What that costs is a second implementation of "placing into a full slot
 * evicts the occupant", and the cost is bounded deliberately:
 *
 *   - The RULES are not duplicated. Which slot a category fills and how many
 *     pieces a slot holds both arrive from `GET /closet/catalog`. This file
 *     contains no category, no slot name and no capacity.
 *   - The result is not trusted. Saving sends the ids in click order and the
 *     server re-runs `domain.Composition.Place` over them, so the stored
 *     composition is always the server's. A divergence here can be visible
 *     for a moment; it cannot be persisted.
 *
 * The mechanic below is deliberately the same shape as the Go one, so the
 * two can be read side by side.
 */

import type { Catalog, ClosetItem, Slot } from "./api/types";

/** One chosen piece, with the slot it was placed into. */
export interface Placement {
  slot: Slot;
  item: ClosetItem;
}

/**
 * The composition, in CLICK ORDER.
 *
 * Order matters and is not cosmetic: it is what goes up on save, and the
 * server places them one at a time in exactly this sequence — so the
 * eviction the operator saw is the eviction that gets stored.
 */
export type Composition = Placement[];

/** Which slot a piece fills, according to the server's catalogue. */
export function slotOf(catalog: Catalog | undefined, item: ClosetItem): Slot | undefined {
  return catalog?.categories.find((c) => c.category === item.category)?.slot;
}

function capacityOf(catalog: Catalog | undefined, slot: Slot): number {
  return catalog?.slots.find((s) => s.slot === slot)?.capacity ?? 1;
}

/**
 * Places a piece, returning a NEW composition.
 *
 * Returns the input unchanged when the piece cannot be placed — which today
 * means only "the catalogue does not know this category", a state that
 * requires the server and the client to disagree about the vocabulary.
 */
export function place(
  catalog: Catalog | undefined,
  composition: Composition,
  item: ClosetItem,
): Composition {
  const slot = slotOf(catalog, item);
  if (!slot) return composition;

  // The same piece twice is a double click, not a composition. Placing it
  // again MOVES it: the operator asked for this garment in this position.
  const withoutItem = composition.filter((p) => p.item.id !== item.id);

  const capacity = capacityOf(catalog, slot);
  const occupants = withoutItem.filter((p) => p.slot === slot);

  let kept = withoutItem;
  if (occupants.length >= capacity) {
    // Evict from the front: for a single slot that is the one occupant, and
    // for a multi-occupancy slot it is the piece chosen longest ago. A
    // person adding a fifth accessory gets a fifth accessory rather than a
    // refusal they have to go and resolve.
    const evict = new Set(
      occupants.slice(0, occupants.length - capacity + 1).map((p) => p.item.id),
    );
    kept = withoutItem.filter((p) => !evict.has(p.item.id));
  }

  return [...kept, { slot, item }];
}

/** Removes a piece. Removing one that is not there is a no-op. */
export function remove(composition: Composition, itemId: string): Composition {
  return composition.filter((p) => p.item.id !== itemId);
}

/** Empties a whole slot — "take all the accessories off". */
export function clearSlot(composition: Composition, slot: Slot): Composition {
  return composition.filter((p) => p.slot !== slot);
}

/** The pieces currently in one slot, in the order they were chosen. */
export function inSlot(composition: Composition, slot: Slot): Placement[] {
  return composition.filter((p) => p.slot === slot);
}

export function isSelected(composition: Composition, itemId: string): boolean {
  return composition.some((p) => p.item.id === itemId);
}

/** What goes up on save: the ids, in click order. */
export function toItemIDs(composition: Composition): string[] {
  return composition.map((p) => p.item.id);
}

/**
 * Rebuilds a local composition from a saved look.
 *
 * ── Why it reads the SERVER's slot and not the category's ──────────────
 * A saved slot records the decision that was made. A category can be
 * corrected afterwards, and re-deriving would silently move a piece inside
 * a look the operator has not opened since — which is the difference a
 * stored slot exists to preserve.
 *
 * Entries whose piece did not resolve are dropped: there is nothing to draw
 * and nothing to save.
 */
export function fromLookItems(
  items: { slot: Slot; item?: ClosetItem }[],
): Composition {
  return items
    .filter((entry): entry is { slot: Slot; item: ClosetItem } => Boolean(entry.item))
    .map((entry) => ({ slot: entry.slot, item: entry.item }));
}

/**
 * Picks the image to draw for a piece, by walking its category's fallback
 * chain and taking the first angle that exists.
 *
 * The chain comes from the catalogue — `folded` first for garments, `side`
 * for shoes — so this function contains no preference of its own. Returning
 * undefined is a real answer: a piece with no photographs yet is drawn by
 * name, because cataloguing a garment and photographing it are separate
 * acts.
 */
export function compositionImage(catalog: Catalog | undefined, item: ClosetItem) {
  const def = catalog?.categories.find((c) => c.category === item.category);
  const chain = def?.composition ?? [];
  for (const view of chain) {
    const found = item.images.find((img) => img.view === view);
    if (found) return found;
  }
  // No chain, or none of its angles exist. Anything the piece does carry is
  // a better answer than nothing — the images arrive in the category's own
  // order, so the first is the most representative one available.
  return item.images[0];
}
