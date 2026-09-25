-- Closet owns its own Postgres schema and its own migration timeline
-- (`schema_migrations_closet`).
--
-- ── What this context is ───────────────────────────────────────────────
-- The operator's real wardrobe, as pieces they can see and combine. Two
-- entities and one asset store: a ClosetItem is a garment that exists in a
-- drawer; a Look is a set of those pieces chosen together. Nothing here
-- generates an image, recognises a garment or recommends anything — every
-- row is something a person entered on purpose.
--
-- ── Why a MODULE and not an integration ────────────────────────────────
-- It talks to no external system. The PNGs arrive already cut out, from
-- whatever tool the operator used outside this product, and once they are
-- here they are ours. There is no vendor to name and no credential to
-- hold, which is the whole difference.
CREATE SCHEMA IF NOT EXISTS closet;

-- ── assets ─────────────────────────────────────────────────────────────
--
-- The bytes of one image, stored in Postgres.
--
-- ══════════════════════════════════════════════════════════════════════
--
--   WHY BLOBS ARE IN THE DATABASE, WHICH IS NORMALLY THE WRONG ANSWER
--
-- ══════════════════════════════════════════════════════════════════════
--
-- Because every alternative available to THIS deployment is worse, and the
-- reasons are specific rather than general:
--
--   - There is no object storage. Adding one is a new external dependency
--     with a credential, a bucket policy and a second backup story, for a
--     wardrobe that is a few hundred files.
--   - The container filesystem does not survive a deploy. EasyPanel
--     rebuilds the image on every push, so a file written to /app/data is
--     gone the next time the operator ships anything. Making it survive
--     means mounting a volume, which is a change to production
--     infrastructure — and one nobody would remember to reproduce when the
--     VPS is rebuilt.
--   - `make backup` already dumps this database, and that restore path has
--     been exercised end to end. A wardrobe stored anywhere else would be
--     the one part of the system the proven backup does not cover.
--
-- The cost is real and bounded: a personal closet is hundreds of images,
-- not millions. Postgres TOASTs this column out of the main heap, so a
-- listing that never selects `bytes` does not pay for it.
--
-- The exit is cheap because nothing above this table knows where bytes
-- live: `ports.AssetStore` is the whole interface, and an S3 implementation
-- would replace one file.
CREATE TABLE closet.assets (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,

    -- The digest of the bytes, and the reason uploads deduplicate.
    --
    -- Raw 32 bytes rather than hex text: it is a fixed-width value that is
    -- never read by a human, and storing the spelling of a number invites
    -- two spellings.
    sha256       BYTEA NOT NULL CHECK (octet_length(sha256) = 32),

    -- What the bytes are. Written from what the server DETECTED, never from
    -- what the upload claimed — see domain.DecodeImage. A declared
    -- content-type is a string the client chose, and this column is served
    -- straight back in a response header.
    --
    -- PNG only, and the CHECK says so rather than leaving room for a format
    -- nothing can produce. The image contract for this closet is a cut-out
    -- with an alpha channel, which is what a PNG is; admitting a second
    -- format here would mean admitting one the decoder cannot open, because
    -- the standard library decodes PNG and not WebP. Adding one later is a
    -- migration, a decoder and a deliberate decision — which is the right
    -- price for changing what the system accepts.
    content_type TEXT NOT NULL CHECK (content_type = 'image/png'),

    byte_size    INTEGER NOT NULL CHECK (byte_size > 0),
    -- Pixel dimensions, decoded from the image header at upload. Stored so
    -- a grid can reserve the right box before the bytes arrive, which is
    -- what stops a wardrobe from reflowing as it loads.
    width        INTEGER NOT NULL CHECK (width > 0),
    height       INTEGER NOT NULL CHECK (height > 0),

    bytes        BYTEA NOT NULL,

    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One row per distinct image per workspace.
--
-- Uploading the same file twice — the same PNG attached to `folded` on two
-- colourways, or a retry after a dropped connection — resolves to the row
-- that already exists instead of storing the megabytes again. The workspace
-- is part of the key on purpose: deduplication must never let one tenant's
-- upload resolve to another tenant's bytes, which is what a global content
-- address would do.
CREATE UNIQUE INDEX assets_content_idx
    ON closet.assets (workspace_id, sha256);

-- ── items ──────────────────────────────────────────────────────────────
--
-- One real garment.
--
-- ── Why `category` has no value CHECK, when jobradar.stage does ─────────
-- Because the two vocabularies age differently. The pipeline stages are a
-- closed model of a process; a wardrobe taxonomy grows with the wardrobe —
-- hats, bags, belts, jewellery — and a CHECK here would make each addition
-- a migration that has to deploy in lockstep with the screen that offers
-- it. The vocabulary is owned by internal/closet/domain, which is the only
-- thing that validates a write, and where a new category also declares the
-- slot it fills and the views it can carry. The CHECK below bounds LENGTH,
-- which is the part that protects the row.
CREATE TABLE closet.items (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,

    name         TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 120),
    category     TEXT NOT NULL CHECK (length(category) BETWEEN 1 AND 40),
    -- A free-form refinement under the category: 'oversized tee', 'chelsea
    -- boot'. Not a second taxonomy — nothing queries it as one, and it is
    -- deliberately not validated against a list.
    subtype      TEXT NOT NULL DEFAULT '' CHECK (length(subtype) <= 60),

    -- Colour as the operator would say it, not as a renderer would need it.
    -- There is no hex here and no swatch: the item carries photographs, and
    -- the photograph is a better answer about colour than any code would
    -- be. These two exist so "the black one" is a question with an answer.
    primary_color   TEXT NOT NULL CHECK (length(primary_color) BETWEEN 1 AND 40),
    secondary_color TEXT NOT NULL DEFAULT '' CHECK (length(secondary_color) <= 40),

    brand        TEXT NOT NULL DEFAULT '' CHECK (length(brand) <= 80),
    notes        TEXT NOT NULL DEFAULT '' CHECK (length(notes) <= 4000),
    favorite     BOOLEAN NOT NULL DEFAULT false,

    -- Archived is how a garment leaves the closet. There is no delete, and
    -- that is what makes a saved look from last winter still resolvable:
    -- the coat you gave away is still the coat that was in the look.
    status       TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'archived')),

    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The selector's read: one workspace's live pieces in one category.
CREATE INDEX items_category_idx
    ON closet.items (workspace_id, category, created_at DESC)
    WHERE status = 'active';

CREATE INDEX items_workspace_idx
    ON closet.items (workspace_id, created_at DESC);

-- ── item_images ────────────────────────────────────────────────────────
--
-- Which asset shows this item from which angle.
--
-- ── Why `view` and not an ordered images[] ─────────────────────────────
-- Because the angle is the meaning. A look builder asks for the folded
-- shot, a detail panel asks for the one on the hanger, and a list that only
-- knew "image 1, image 2" would make both of those a guess about upload
-- order. The allowed views are a property of the CATEGORY, declared in the
-- domain: a watch offers `front` and `detail` and can never be given
-- `hanger_front`, because a watch does not go on a hanger.
--
-- ── Why there is no position column ────────────────────────────────────
-- Order would be a third source of truth. The category already declares its
-- views in order, so display order and composition preference are both
-- derived from the vocabulary rather than stored per row and able to
-- disagree with it.
CREATE TABLE closet.item_images (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    item_id      UUID NOT NULL REFERENCES closet.items (id) ON DELETE CASCADE,

    view         TEXT NOT NULL CHECK (length(view) BETWEEN 1 AND 40),
    -- RESTRICT, not CASCADE: an asset is shared by every image row that
    -- resolved to the same bytes, so deleting one must never be able to
    -- blank another item's picture.
    asset_id     UUID NOT NULL REFERENCES closet.assets (id) ON DELETE RESTRICT,

    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One image per view per item. Re-attaching a view REPLACES it, which is
-- what "I shot a better folded photo" means; without this, the item would
-- accumulate two folded shots and the builder would pick whichever the
-- query happened to return first.
CREATE UNIQUE INDEX item_images_view_idx
    ON closet.item_images (item_id, view);

CREATE INDEX item_images_asset_idx
    ON closet.item_images (asset_id);

-- ── looks ──────────────────────────────────────────────────────────────
--
-- A set of pieces chosen together.
--
-- A look holds NO copy of a garment: not its name, not its colour and above
-- all not its images. Re-opening a look reads today's items through
-- look_items, so renaming a shirt renames it everywhere and replacing one
-- piece does not rebuild the look. A denormalised snapshot would drift, and
-- the drift would be silent.
CREATE TABLE closet.looks (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,

    name         TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 120),
    -- Occasion is a label, not a rule. Nothing filters a wardrobe by it and
    -- nothing refuses a combination because of it — it is how the operator
    -- finds "the one for the wedding" in a gallery. Open vocabulary for the
    -- same reason `category` is: the domain owns the list.
    occasion     TEXT NOT NULL DEFAULT 'other' CHECK (length(occasion) BETWEEN 1 AND 40),
    favorite     BOOLEAN NOT NULL DEFAULT false,
    notes        TEXT NOT NULL DEFAULT '' CHECK (length(notes) <= 4000),
    status       TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'archived')),

    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX looks_workspace_idx
    ON closet.looks (workspace_id, updated_at DESC);

-- ── look_items ─────────────────────────────────────────────────────────
--
-- Which item fills which slot of which look.
--
-- ── Slot, and why it is not the item's category ────────────────────────
-- The slot is the POSITION in a composition; the category is what the
-- garment IS. They are one-to-one today and will stop being so the first
-- time a category is added — a hat and a scarf are different categories
-- that both fill `accessory`. Storing the slot here means a look records
-- the decision that was made, not one re-derived later from a taxonomy that
-- has since moved.
CREATE TABLE closet.look_items (
    workspace_id UUID NOT NULL,
    look_id      UUID NOT NULL REFERENCES closet.looks (id) ON DELETE CASCADE,

    slot         TEXT NOT NULL CHECK (length(slot) BETWEEN 1 AND 40),
    -- Only slots the domain marks as multi-occupancy ever go past 0 —
    -- `accessory` today. A single-occupancy slot is enforced above this
    -- line; the primary key is what makes it unforgeable below it.
    position     INTEGER NOT NULL DEFAULT 0 CHECK (position >= 0),

    -- RESTRICT because an item is archived, never deleted: a look that
    -- pointed at a removed row would be a look that silently lost a piece.
    item_id      UUID NOT NULL REFERENCES closet.items (id) ON DELETE RESTRICT,

    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),

    PRIMARY KEY (look_id, slot, position)
);

-- The same garment cannot be in one look twice. Wearing two of the same
-- shirt is not a thing, and without this a double-click on a piece would
-- file it into `accessory:0` and `accessory:1` as though they were two.
CREATE UNIQUE INDEX look_items_unique_item_idx
    ON closet.look_items (look_id, item_id);

-- Backs "which looks use this piece", which is what makes archiving a
-- garment an informed decision rather than a surprise.
CREATE INDEX look_items_item_idx
    ON closet.look_items (workspace_id, item_id);
