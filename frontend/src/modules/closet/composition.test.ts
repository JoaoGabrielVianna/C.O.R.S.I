import { describe, expect, it } from "vitest";

import type { Catalog, ClosetItem, ItemImage } from "./api/types";
import {
  clearSlot,
  compositionImage,
  fromLookItems,
  inSlot,
  isSelected,
  place,
  remove,
  slotOf,
  toItemIDs,
  type Composition,
} from "./composition";

/**
 * The builder's local composition.
 *
 * These assertions are the client-side half of the rules that
 * `internal/closet/domain.Composition` enforces on save. They exist because
 * the screen has to show the answer before the server gives it — and they
 * are written as the same cases so the two can be read side by side.
 *
 * Note what the catalogue below proves: this file contains no category
 * name, no slot name and no capacity of its own. Everything comes from the
 * fixture, which stands in for `GET /closet/catalog`.
 */

const catalog: Catalog = {
  categories: [
    {
      category: "tops",
      slot: "top",
      views: ["open", "hanger_front", "folded"],
      composition: ["folded", "open", "hanger_front"],
    },
    { category: "bottoms", slot: "bottom", views: ["open", "folded"], composition: ["folded", "open"] },
    { category: "shoes", slot: "shoes", views: ["side", "front"], composition: ["side", "front"] },
    {
      category: "accessories",
      slot: "accessory",
      views: ["front", "detail"],
      composition: ["front", "detail"],
    },
  ],
  slots: [
    { slot: "top", capacity: 1 },
    { slot: "bottom", capacity: 1 },
    { slot: "shoes", capacity: 1 },
    { slot: "accessory", capacity: 3 },
  ],
  occasions: ["casual", "other"],
  max_look_items: 6,
};

let seq = 0;
function piece(category: string, images: Partial<ItemImage>[] = []): ClosetItem {
  seq += 1;
  return {
    id: `item-${seq}`,
    name: `Peça ${seq}`,
    category,
    primary_color: "preto",
    favorite: false,
    status: "active",
    images: images.map((img, i) => ({
      id: `img-${seq}-${i}`,
      item_id: `item-${seq}`,
      view: "open",
      asset_id: `asset-${seq}-${i}`,
      content_type: "image/png",
      byte_size: 1,
      width: 10,
      height: 10,
      created_at: "",
      updated_at: "",
      ...img,
    })),
    created_at: "",
    updated_at: "",
  };
}

describe("placing a piece", () => {
  it("puts it in the slot its category declares", () => {
    const top = piece("tops");
    const composition = place(catalog, [], top);
    expect(composition).toHaveLength(1);
    expect(composition[0].slot).toBe("top");
    expect(slotOf(catalog, top)).toBe("top");
  });

  it("replaces the occupant of a single slot", () => {
    // The interaction the whole module exists to get right: clicking a
    // second shirt swaps the shirt. It does not stack.
    const first = piece("tops");
    const second = piece("tops");
    const composition = place(catalog, place(catalog, [], first), second);

    expect(composition).toHaveLength(1);
    expect(composition[0].item.id).toBe(second.id);
  });

  it("leaves the other slots alone when it replaces one", () => {
    const shoes = piece("shoes");
    let composition: Composition = place(catalog, [], shoes);
    composition = place(catalog, composition, piece("tops"));
    composition = place(catalog, composition, piece("tops"));

    expect(inSlot(composition, "shoes")).toHaveLength(1);
    expect(inSlot(composition, "shoes")[0].item.id).toBe(shoes.id);
    expect(inSlot(composition, "top")).toHaveLength(1);
  });

  it("accumulates in a multi-occupancy slot until it is full", () => {
    let composition: Composition = [];
    const kept = [piece("accessories"), piece("accessories"), piece("accessories")];
    for (const item of kept) composition = place(catalog, composition, item);
    expect(inSlot(composition, "accessory")).toHaveLength(3);

    // One past capacity evicts the OLDEST, so adding a fourth accessory
    // gives you a fourth accessory rather than a refusal to resolve.
    const overflow = piece("accessories");
    composition = place(catalog, composition, overflow);
    const ids = inSlot(composition, "accessory").map((p) => p.item.id);

    expect(ids).toHaveLength(3);
    expect(ids).not.toContain(kept[0].id);
    expect(ids).toContain(overflow.id);
  });

  it("does not file the same piece twice", () => {
    const item = piece("accessories");
    const composition = place(catalog, place(catalog, [], item), item);
    expect(composition).toHaveLength(1);
  });

  it("ignores a piece whose category the catalogue does not know", () => {
    // Reaching this means the client and the server disagree about the
    // vocabulary. Dropping the click is better than inventing a slot.
    const composition = place(catalog, [], piece("hats"));
    expect(composition).toEqual([]);
  });

  it("keeps click order, because that is what goes up on save", () => {
    const shoes = piece("shoes");
    const top = piece("tops");
    const composition = place(catalog, place(catalog, [], shoes), top);
    expect(toItemIDs(composition)).toEqual([shoes.id, top.id]);
  });
});

describe("removing a piece", () => {
  it("removes it and reports selection correctly", () => {
    const top = piece("tops");
    let composition = place(catalog, [], top);
    expect(isSelected(composition, top.id)).toBe(true);

    composition = remove(composition, top.id);
    expect(composition).toEqual([]);
    expect(isSelected(composition, top.id)).toBe(false);
  });

  it("is a no-op for a piece that is not there", () => {
    const composition = place(catalog, [], piece("tops"));
    expect(remove(composition, "nothing")).toHaveLength(1);
  });

  it("clears a whole slot without touching the others", () => {
    let composition: Composition = [];
    composition = place(catalog, composition, piece("accessories"));
    composition = place(catalog, composition, piece("accessories"));
    const top = piece("tops");
    composition = place(catalog, composition, top);

    const cleared = clearSlot(composition, "accessory");
    expect(cleared).toHaveLength(1);
    expect(cleared[0].item.id).toBe(top.id);
  });
});

describe("choosing which image to draw", () => {
  it("walks the category's fallback chain", () => {
    // Tops prefer folded, then open. A piece shot only on the hanger still
    // draws.
    const hangerOnly = piece("tops", [{ view: "hanger_front" }]);
    expect(compositionImage(catalog, hangerOnly)?.view).toBe("hanger_front");

    const withOpen = piece("tops", [{ view: "hanger_front" }, { view: "open" }]);
    expect(compositionImage(catalog, withOpen)?.view).toBe("open");

    const withFolded = piece("tops", [{ view: "open" }, { view: "folded" }]);
    expect(compositionImage(catalog, withFolded)?.view).toBe("folded");
  });

  it("returns nothing for a piece with no photographs", () => {
    // A real state, not an error: cataloguing a garment and photographing
    // it are separate acts.
    expect(compositionImage(catalog, piece("tops"))).toBeUndefined();
  });

  it("falls back to whatever the piece has when the chain matches nothing", () => {
    const stranger = piece("tops", [{ view: "detail" }]);
    expect(compositionImage(catalog, stranger)?.view).toBe("detail");
  });
});

describe("loading a saved look", () => {
  it("uses the slot the look RECORDED, not the one the category names now", () => {
    // A stored slot is the decision that was made. Re-deriving would move a
    // piece inside a look the operator has not opened since.
    const top = piece("tops");
    const composition = fromLookItems([{ slot: "accessory", item: top }]);
    expect(composition[0].slot).toBe("accessory");
  });

  it("drops entries whose piece did not resolve", () => {
    expect(fromLookItems([{ slot: "top", item: undefined }])).toEqual([]);
  });
});
