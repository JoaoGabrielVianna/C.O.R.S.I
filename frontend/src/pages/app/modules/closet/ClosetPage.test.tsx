// @vitest-environment jsdom

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { I18nFixture } from "@/lib/i18n/testing";

import type { Catalog, ClosetItem, ItemImage, Look, Page } from "@/modules/closet/api/types";
import { __resetAssetCache } from "@/modules/closet/hooks/useAssetObjectURL";

import { ClosetPage } from "./index";

/**
 * The Closet, driven the way a person drives it.
 *
 * ── What is real here and what is not ──────────────────────────────────
 * Real: the page, the category rail, the grid, the stage, the local
 * composition, the dialog, the query hooks and their invalidation. Faked:
 * the REST client, because the backend's own suite already proves the rules
 * against Postgres and what these assertions are about is the interaction.
 *
 * The fake is a small in-memory server rather than a pile of `mockResolved`
 * calls, and deliberately so: half of what this file has to show is that a
 * write reaches the screen after it lands — that a saved look reopens with
 * the same pieces — and a mock that returns a fixed value cannot be wrong
 * about that in either direction.
 */

/* ── the fake server ─────────────────────────────────────────────────── */

const CATALOG: Catalog = {
  categories: [
    {
      category: "tops",
      slot: "top",
      views: ["open", "hanger_front", "folded"],
      composition: ["folded", "open", "hanger_front"],
    },
    { category: "bottoms", slot: "bottom", views: ["open", "folded"], composition: ["folded", "open"] },
    { category: "shoes", slot: "shoes", views: ["side", "front"], composition: ["side", "front"] },
    { category: "watches", slot: "watch", views: ["front", "detail"], composition: ["front", "detail"] },
  ],
  slots: [
    { slot: "top", capacity: 1 },
    { slot: "bottom", capacity: 1 },
    { slot: "shoes", capacity: 1 },
    { slot: "watch", capacity: 1 },
  ],
  occasions: ["casual", "work", "other"],
  max_look_items: 4,
};

interface Server {
  items: ClosetItem[];
  looks: Look[];
  failCatalog: boolean;
  failItems: boolean;
  uploadError: string | null;
  uploads: { itemId: string; view: string; fileName: string }[];
}

let server: Server;
let nextId = 0;

function id(prefix: string) {
  nextId += 1;
  return `${prefix}-${nextId}`;
}

function makeItem(name: string, category: string, extra: Partial<ClosetItem> = {}): ClosetItem {
  return {
    id: id("item"),
    name,
    category,
    primary_color: "preto",
    favorite: false,
    status: "active",
    images: [],
    created_at: "",
    updated_at: "",
    ...extra,
  };
}

/** The server's own composition rule, mirrored: slot capacity is one. */
function compose(itemIDs: string[]): Look["items"] {
  const bySlot = new Map<string, { slot: string; item: ClosetItem }>();
  for (const itemID of itemIDs) {
    const item = server.items.find((i) => i.id === itemID);
    if (!item) continue;
    const slot = CATALOG.categories.find((c) => c.category === item.category)?.slot;
    if (!slot) continue;
    bySlot.set(slot, { slot, item });
  }
  return CATALOG.slots
    .filter((def) => bySlot.has(def.slot))
    .map((def) => {
      const entry = bySlot.get(def.slot)!;
      return {
        slot: entry.slot,
        position: 0,
        item_id: entry.item.id,
        item: entry.item,
        created_at: "",
      };
    });
}

vi.mock("@/modules/closet/api/closet", () => ({
  getCatalog: async (): Promise<Catalog> => {
    if (server.failCatalog) throw new Error("catalog is down");
    return CATALOG;
  },
  listItems: async (q: Record<string, unknown> = {}): Promise<Page<ClosetItem>> => {
    if (server.failItems) throw new Error("items are down");
    let items = server.items.filter((i) => i.status === "active");
    if (q.category) items = items.filter((i) => i.category === q.category);
    if (q.favorite) items = items.filter((i) => i.favorite);
    if (q.search) {
      const needle = String(q.search).toLowerCase();
      items = items.filter((i) => i.name.toLowerCase().includes(needle));
    }
    return { items, total: items.length };
  },
  getItem: async (itemID: string) => server.items.find((i) => i.id === itemID)!,
  createItem: async (body: Record<string, unknown>) => {
    const created = makeItem(String(body.name), String(body.category), {
      primary_color: String(body.primary_color),
      favorite: Boolean(body.favorite),
    });
    server.items.push(created);
    return created;
  },
  updateItem: async (itemID: string, body: Record<string, unknown>) => {
    const item = server.items.find((i) => i.id === itemID)!;
    Object.assign(item, body);
    return item;
  },
  archiveItem: async (itemID: string) => {
    const item = server.items.find((i) => i.id === itemID)!;
    item.status = "archived";
    return { item, looks_affected: 0 };
  },
  restoreItem: async (itemID: string) => {
    const item = server.items.find((i) => i.id === itemID)!;
    item.status = "active";
    return item;
  },
  uploadImage: async (itemID: string, view: string, file: File): Promise<ItemImage> => {
    if (server.uploadError) throw new Error(server.uploadError);
    server.uploads.push({ itemId: itemID, view, fileName: file.name });
    const image: ItemImage = {
      id: id("image"),
      item_id: itemID,
      view,
      asset_id: id("asset"),
      content_type: "image/png",
      byte_size: 10,
      width: 40,
      height: 50,
      created_at: "",
      updated_at: "",
    };
    const item = server.items.find((i) => i.id === itemID)!;
    item.images = [...item.images.filter((img) => img.view !== view), image];
    return image;
  },
  removeImage: async (itemID: string, view: string) => {
    const item = server.items.find((i) => i.id === itemID)!;
    item.images = item.images.filter((img) => img.view !== view);
  },
  fetchAssetBlob: async () => new Blob(["png"], { type: "image/png" }),
  listLooks: async (): Promise<Page<Look>> => ({
    items: server.looks,
    total: server.looks.length,
  }),
  getLook: async (lookID: string) => server.looks.find((l) => l.id === lookID)!,
  createLook: async (body: Record<string, unknown>) => {
    const look: Look = {
      id: id("look"),
      name: String(body.name),
      occasion: String(body.occasion ?? "other"),
      favorite: Boolean(body.favorite),
      status: "active",
      items: compose((body.item_ids as string[]) ?? []),
      created_at: "",
      updated_at: "",
    };
    server.looks.push(look);
    return look;
  },
  updateLook: async (lookID: string, body: Record<string, unknown>) => {
    const look = server.looks.find((l) => l.id === lookID)!;
    Object.assign(look, body);
    return look;
  },
  setLookItems: async (lookID: string, itemIDs: string[]) => {
    const look = server.looks.find((l) => l.id === lookID)!;
    look.items = compose(itemIDs);
    return look;
  },
  archiveLook: async (lookID: string) => {
    const look = server.looks.find((l) => l.id === lookID)!;
    look.status = "archived";
    return look;
  },
  restoreLook: async (lookID: string) => {
    const look = server.looks.find((l) => l.id === lookID)!;
    look.status = "active";
    return look;
  },
}));

/* ── harness ─────────────────────────────────────────────────────────── */

function renderPage() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <I18nFixture lang="pt">
      <QueryClientProvider client={client}>
        <ClosetPage />
      </QueryClientProvider>
    </I18nFixture>,
  );
}

/** The stage row for one slot, addressed by the attribute it publishes. */
function slotRow(slot: string): HTMLElement {
  const row = document.querySelector(`[data-slot="${slot}"]`);
  if (!row) throw new Error(`no stage row for slot ${slot}`);
  return row as HTMLElement;
}

beforeEach(() => {
  nextId = 0;
  server = {
    items: [],
    looks: [],
    failCatalog: false,
    failItems: false,
    uploadError: null,
    uploads: [],
  };
  // jsdom has neither, and the image component calls both.
  globalThis.URL.createObjectURL = vi.fn(() => "blob:fake");
  globalThis.URL.revokeObjectURL = vi.fn();
  __resetAssetCache();
});

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

/* ── the wardrobe ────────────────────────────────────────────────────── */

describe("the wardrobe pane", () => {
  it("says the wardrobe is empty rather than showing an empty grid", async () => {
    renderPage();
    expect(await screen.findByText("Guarda-roupa vazio")).toBeDefined();
  });

  it("distinguishes an empty wardrobe from a filter that matched nothing", async () => {
    // Two different facts, and telling the operator the wrong one sends
    // them off to catalogue a shirt they already own.
    server.items = [makeItem("Camiseta preta", "tops")];
    const user = userEvent.setup();
    renderPage();

    await screen.findByRole("button", { name: /^Camiseta preta/ });
    await user.type(screen.getByLabelText("Buscar peças"), "jaqueta");

    expect(await screen.findByText("Nenhuma peça aqui")).toBeDefined();
  });

  it("narrows the grid to one category", async () => {
    server.items = [makeItem("Camiseta preta", "tops"), makeItem("Calça bege", "bottoms")];
    const user = userEvent.setup();
    renderPage();

    await screen.findByRole("button", { name: /^Camiseta preta/ });
    await user.click(await screen.findByRole("button", { name: /Partes de baixo/ }));

    await waitFor(() => {
      expect(screen.queryByRole("button", { name: /^Camiseta preta/ })).toBeNull();
    });
    expect(screen.getByRole("button", { name: /^Calça bege/ })).toBeDefined();
  });

  it("reports a failed read instead of rendering an empty closet", async () => {
    server.failItems = true;
    renderPage();
    expect(await screen.findByRole("alert")).toHaveProperty(
      "textContent",
      "Não foi possível carregar as peças.",
    );
  });

  it("reports a failed catalogue read", async () => {
    server.failCatalog = true;
    renderPage();
    await waitFor(() => {
      expect(
        screen.getAllByRole("alert").some((el) =>
          el.textContent?.includes("vocabulário"),
        ),
      ).toBe(true);
    });
  });
});

/* ── the builder ─────────────────────────────────────────────────────── */

describe("building a look", () => {
  it("puts a clicked piece into its slot immediately", async () => {
    server.items = [makeItem("Camiseta preta", "tops")];
    const user = userEvent.setup();
    renderPage();

    // Awaited first: the stage rows are the catalogue's slots, so there is
    // no `top` row to look at until the catalogue has arrived.
    const tile = await screen.findByRole("button", { name: /^Camiseta preta/ });
    expect(within(slotRow("top")).getByText("vazio")).toBeDefined();

    await user.click(tile);
    expect(within(slotRow("top")).getByText("Camiseta preta")).toBeDefined();
    expect(within(slotRow("bottom")).getByText("vazio")).toBeDefined();
  });

  it("replaces the piece when another of the same kind is clicked", async () => {
    // ══════════════════════════════════════════════════════════════════
    // The interaction the whole module exists to get right.
    // ══════════════════════════════════════════════════════════════════
    server.items = [makeItem("Camiseta preta", "tops"), makeItem("Camiseta branca", "tops")];
    const user = userEvent.setup();
    renderPage();

    await user.click(await screen.findByRole("button", { name: /^Camiseta preta/ }));
    await user.click(screen.getByRole("button", { name: /^Camiseta branca/ }));

    const top = slotRow("top");
    expect(within(top).getByText("Camiseta branca")).toBeDefined();
    expect(within(top).queryByText("Camiseta preta")).toBeNull();
  });

  it("marks the selected piece in the grid", async () => {
    server.items = [makeItem("Camiseta preta", "tops")];
    const user = userEvent.setup();
    renderPage();

    const tile = await screen.findByRole("button", { name: /^Camiseta preta/ });
    expect(tile.getAttribute("aria-pressed")).toBe("false");
    await user.click(tile);
    expect(tile.getAttribute("aria-pressed")).toBe("true");
  });

  it("removes a piece from the look without touching the wardrobe", async () => {
    server.items = [makeItem("Camiseta preta", "tops")];
    const user = userEvent.setup();
    renderPage();

    await user.click(await screen.findByRole("button", { name: /^Camiseta preta/ }));
    await user.click(
      within(slotRow("top")).getByRole("button", { name: "Tirar Camiseta preta do look" }),
    );

    expect(within(slotRow("top")).getByText("vazio")).toBeDefined();
    // Still in the drawer. Removing from a look is not removing from the
    // closet.
    expect(screen.getByRole("button", { name: /^Camiseta preta/ })).toBeDefined();
  });

  it("refuses to save a look with no name", async () => {
    server.items = [makeItem("Camiseta preta", "tops")];
    const user = userEvent.setup();
    renderPage();

    await user.click(await screen.findByRole("button", { name: /^Camiseta preta/ }));
    await user.click(screen.getByRole("button", { name: "Salvar look" }));

    expect(await screen.findByRole("alert")).toHaveProperty(
      "textContent",
      "Dê um nome ao look antes de salvar.",
    );
    expect(server.looks).toHaveLength(0);
  });

  it("opening a piece's details does not swap it into the look", async () => {
    // The pencil sits on top of the tile. Without stopPropagation the click
    // bubbles and editing a garment silently dresses you in it.
    server.items = [makeItem("Camiseta preta", "tops")];
    const user = userEvent.setup();
    renderPage();

    await screen.findByRole("button", { name: /^Camiseta preta/ });
    await user.click(screen.getByRole("button", { name: "Editar Camiseta preta" }));

    expect(within(slotRow("top")).getByText("vazio")).toBeDefined();
    expect(await screen.findByRole("dialog")).toBeDefined();
  });
});

/* ── saving, leaving, coming back ────────────────────────────────────── */

describe("saving and reopening a look", () => {
  it("saves the composition and finds it in the gallery", async () => {
    server.items = [
      makeItem("Camiseta preta", "tops"),
      makeItem("Calça bege", "bottoms"),
      makeItem("Tênis branco", "shoes"),
    ];
    const user = userEvent.setup();
    renderPage();

    await user.click(await screen.findByRole("button", { name: /^Camiseta preta/ }));
    await user.click(screen.getByRole("button", { name: /^Calça bege/ }));
    await user.click(screen.getByRole("button", { name: /^Tênis branco/ }));
    await user.type(screen.getByLabelText("Nome do look"), "Sexta-feira");
    await user.click(screen.getByRole("button", { name: "Salvar look" }));

    await waitFor(() => expect(server.looks).toHaveLength(1));
    expect(server.looks[0].items).toHaveLength(3);

    // ══════════════════════════════════════════════════════════════════
    // The stop condition of the sprint: leave the builder, come back,
    // get exactly the same composition.
    // ══════════════════════════════════════════════════════════════════
    await user.click(screen.getByRole("button", { name: "Meus looks" }));
    const card = await screen.findByText("Sexta-feira");
    expect(card).toBeDefined();

    await user.click(screen.getByRole("button", { name: "Editar Sexta-feira" }));

    expect(within(slotRow("top")).getByText("Camiseta preta")).toBeDefined();
    expect(within(slotRow("bottom")).getByText("Calça bege")).toBeDefined();
    expect(within(slotRow("shoes")).getByText("Tênis branco")).toBeDefined();
    expect(screen.getByLabelText("Nome do look")).toHaveProperty("value", "Sexta-feira");
  });

  it("edits a saved look in place rather than creating a second one", async () => {
    server.items = [
      makeItem("Camiseta preta", "tops"),
      makeItem("Camiseta branca", "tops"),
    ];
    const user = userEvent.setup();
    renderPage();

    await user.click(await screen.findByRole("button", { name: /^Camiseta preta/ }));
    await user.type(screen.getByLabelText("Nome do look"), "Base");
    await user.click(screen.getByRole("button", { name: "Salvar look" }));
    await waitFor(() => expect(server.looks).toHaveLength(1));

    // The button changes meaning once the look exists, and so does the save.
    await user.click(screen.getByRole("button", { name: /^Camiseta branca/ }));
    await user.click(await screen.findByRole("button", { name: "Salvar alterações" }));

    await waitFor(() => {
      expect(server.looks[0].items[0].item?.name).toBe("Camiseta branca");
    });
    expect(server.looks).toHaveLength(1);
  });

  it("starts a variation without overwriting the original", async () => {
    server.items = [makeItem("Camiseta preta", "tops")];
    const user = userEvent.setup();
    renderPage();

    await user.click(await screen.findByRole("button", { name: /^Camiseta preta/ }));
    await user.type(screen.getByLabelText("Nome do look"), "Base");
    await user.click(screen.getByRole("button", { name: "Salvar look" }));
    await waitFor(() => expect(server.looks).toHaveLength(1));

    await user.click(screen.getByRole("button", { name: "Meus looks" }));
    await user.click(await screen.findByRole("button", { name: /Começar um novo a partir de Base/ }));

    // Loaded WITHOUT the look's identity, so saving creates a second row.
    expect(screen.getByLabelText("Nome do look")).toHaveProperty("value", "Base (variação)");
    await user.click(screen.getByRole("button", { name: "Salvar look" }));
    await waitFor(() => expect(server.looks).toHaveLength(2));
    expect(server.looks[0].name).toBe("Base");
  });

  it("surfaces a backend refusal instead of pretending the look was saved", async () => {
    server.items = [makeItem("Camiseta preta", "tops")];
    const user = userEvent.setup();
    renderPage();

    await user.click(await screen.findByRole("button", { name: /^Camiseta preta/ }));
    await user.type(screen.getByLabelText("Nome do look"), "Vai falhar");

    const closet = await import("@/modules/closet/api/closet");
    vi.spyOn(closet, "createLook").mockRejectedValueOnce(
      new Error("peça arquivada não entra em look"),
    );

    await user.click(screen.getByRole("button", { name: "Salvar look" }));

    expect(await screen.findByText("peça arquivada não entra em look")).toBeDefined();
  });

  it("says the gallery is empty rather than drawing nothing", async () => {
    const user = userEvent.setup();
    renderPage();
    await user.click(screen.getByRole("button", { name: "Meus looks" }));
    expect(await screen.findByText("Nenhum look salvo")).toBeDefined();
  });
});

/* ── cataloguing a piece ─────────────────────────────────────────────── */

describe("cataloguing a piece", () => {
  it("creates the piece and then offers the angles its category allows", async () => {
    const user = userEvent.setup();
    renderPage();

    await user.click(await screen.findByRole("button", { name: "Nova peça" }));
    const dialog = await screen.findByRole("dialog");

    await user.type(within(dialog).getByLabelText("Nome"), "Camiseta preta");
    await user.selectOptions(within(dialog).getByLabelText("Categoria"), "tops");
    await user.type(within(dialog).getByLabelText("Cor principal"), "preto");
    await user.click(within(dialog).getByRole("button", { name: "Cadastrar" }));

    await waitFor(() => expect(server.items).toHaveLength(1));

    // The dialog stays open on the new piece so the photographs can be
    // attached without finding the tile again — and it offers exactly the
    // angles a `tops` may carry.
    expect(await within(dialog).findByText("Dobrada")).toBeDefined();
    expect(within(dialog).queryByText("Detalhe")).toBeNull();
  });

  it("offers a watch its own angles and never a hanger", async () => {
    server.items = [makeItem("Seiko", "watches")];
    const user = userEvent.setup();
    renderPage();

    await screen.findByRole("button", { name: /^Seiko/ });
    await user.click(screen.getByRole("button", { name: "Editar Seiko" }));
    const dialog = await screen.findByRole("dialog");

    expect(within(dialog).getByText("Frente")).toBeDefined();
    expect(within(dialog).getByText("Detalhe")).toBeDefined();
    expect(within(dialog).queryByText("Cabide frente")).toBeNull();
  });

  it("refuses to submit without the required fields", async () => {
    const user = userEvent.setup();
    renderPage();

    await user.click(await screen.findByRole("button", { name: "Nova peça" }));
    const dialog = await screen.findByRole("dialog");
    await user.click(within(dialog).getByRole("button", { name: "Cadastrar" }));

    expect(
      await within(dialog).findByText("Nome, categoria e cor principal são obrigatórios."),
    ).toBeDefined();
    expect(server.items).toHaveLength(0);
  });

  it("uploads a photograph into the angle it was dropped on", async () => {
    server.items = [makeItem("Camiseta preta", "tops")];
    const user = userEvent.setup();
    renderPage();

    await screen.findByRole("button", { name: /^Camiseta preta/ });
    await user.click(screen.getByRole("button", { name: "Editar Camiseta preta" }));
    const dialog = await screen.findByRole("dialog");

    const file = new File(["png-bytes"], "camiseta.png", { type: "image/png" });
    const input = dialog.querySelector('input[type="file"]') as HTMLInputElement;
    await user.upload(input, file);

    await waitFor(() => expect(server.uploads).toHaveLength(1));
    // The first angle a `tops` declares is `open`, and that is the slot the
    // first input belongs to.
    expect(server.uploads[0]).toMatchObject({ view: "open", fileName: "camiseta.png" });
  });

  it("reports a refused upload on the angle that refused it", async () => {
    server.items = [makeItem("Camiseta preta", "tops")];
    server.uploadError = "a imagem tem mais de 8 MB";
    const user = userEvent.setup();
    renderPage();

    await screen.findByRole("button", { name: /^Camiseta preta/ });
    await user.click(screen.getByRole("button", { name: "Editar Camiseta preta" }));
    const dialog = await screen.findByRole("dialog");

    const input = dialog.querySelector('input[type="file"]') as HTMLInputElement;
    await user.upload(input, new File(["x"], "huge.png", { type: "image/png" }));

    expect(await within(dialog).findByText("a imagem tem mais de 8 MB")).toBeDefined();
  });

  it("archives a piece and takes it out of the selector", async () => {
    server.items = [makeItem("Casaco doado", "tops")];
    const user = userEvent.setup();
    renderPage();

    await screen.findByRole("button", { name: /^Casaco doado/ });
    await user.click(screen.getByRole("button", { name: "Editar Casaco doado" }));
    const dialog = await screen.findByRole("dialog");
    await user.click(within(dialog).getByRole("button", { name: "Arquivar" }));

    await waitFor(() => expect(server.items[0].status).toBe("archived"));
    await user.click(within(dialog).getByRole("button", { name: "Cancelar" }));

    await waitFor(() => {
      expect(screen.queryByRole("button", { name: /^Casaco doado/ })).toBeNull();
    });
  });
});
