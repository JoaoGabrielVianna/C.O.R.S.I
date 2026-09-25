// Package ports declares the driven side of the Closet context.
//
// Every method takes a workspace id and filters on it in SQL. An id that
// arrives from a URL must not be able to select a row it does not own, and
// the only way to guarantee that is for the predicate to be in the query
// rather than in a check the caller is trusted to have run.
//
// That matters more here than in a context made of text. An asset is served
// as raw bytes with a content-type, straight into an `<img>`: a read that
// resolved by id alone would let any authenticated caller who guessed or
// captured a uuid pull another workspace's photographs. `AssetStore.Get`
// takes the workspace for that reason and no other.
package ports

import (
	"context"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/closet/domain"
)

/* ── items ───────────────────────────────────────────────────────────── */

// ItemFilter narrows a listing of the wardrobe.
//
// ── Why these four and not a query language ────────────────────────────
// They are the questions the closet actually asks: one category at a time,
// which is how the selector works; the favourites; a text match for a
// wardrobe too big to scroll; and whether the archived pieces are included.
// The zero value lists the workspace's ACTIVE pieces, newest first — which
// is what a selector wants, and getting the default wrong here would mean
// every screen having to remember to exclude the garments you gave away.
type ItemFilter struct {
	// Category narrows to one category. Nil is every category.
	Category *domain.Category
	// Status narrows to one status. Nil means active only — see the note
	// above; "everything including archived" is Status pointing at nothing
	// meaningful, so it is spelled IncludeArchived instead.
	Status *domain.Status
	// IncludeArchived widens a nil Status to both. It is a separate flag
	// rather than a magic Status value because "either" is not a status,
	// and spelling it as one would put it in the enum.
	IncludeArchived bool
	// Favorite, when set, narrows to the favourites.
	Favorite *bool
	// Search matches name, brand, subtype and the two colours.
	Search string

	Limit  int
	Offset int
}

type ItemRepo interface {
	// List returns matching pieces, newest first, WITHOUT their images.
	//
	// Images are a second query — see ImageRepo.ListForItems — rather than
	// a join, because a join multiplies every piece by its view count and
	// the scan then has to de-duplicate rows it never wanted. The service
	// composes the two; nothing above it sees the seam.
	List(ctx context.Context, workspaceID uuid.UUID, f ItemFilter) ([]*domain.ClosetItem, error)
	// Count is how many rows the same filter matches, ignoring Limit and
	// Offset — so a truncated listing can say how much it is not showing.
	Count(ctx context.Context, workspaceID uuid.UUID, f ItemFilter) (int64, error)
	// FindByID returns one piece, archived or not, or a not-found error.
	//
	// It deliberately sees archived rows: a saved look references them, and
	// a detail panel that could not open the coat you gave away would make
	// that look unreadable.
	FindByID(ctx context.Context, workspaceID, id uuid.UUID) (*domain.ClosetItem, error)
	// FindManyByID resolves a set of ids in one query, for hydrating a
	// look's slots. Missing ids are simply absent from the result: the
	// caller knows which it asked for, and an error per missing row would
	// turn one read into a loop.
	FindManyByID(ctx context.Context, workspaceID uuid.UUID, ids []uuid.UUID) ([]*domain.ClosetItem, error)
	Create(ctx context.Context, item *domain.ClosetItem) error
	// Update writes the mutable fields. It does NOT touch status: archiving
	// is SetStatus, which is a different decision with different
	// consequences, and letting a generic update carry it would mean a
	// caller fixing a typo in a brand could retire a garment.
	Update(ctx context.Context, item *domain.ClosetItem) error
	SetStatus(ctx context.Context, workspaceID, id uuid.UUID, status domain.Status) (*domain.ClosetItem, error)
	// CountLooksUsing reports how many LIVE looks reference this piece.
	//
	// It exists so archiving can tell the operator what it affects. It is
	// not a veto: the garment left the wardrobe whatever the looks say, and
	// a system that refused to record that would be arguing with reality.
	CountLooksUsing(ctx context.Context, workspaceID, itemID uuid.UUID) (int64, error)
}

/* ── images ──────────────────────────────────────────────────────────── */

type ImageRepo interface {
	// Attach files an asset under one view of one piece, REPLACING whatever
	// that view held. Upsert rather than insert because "I shot a better
	// folded photo" is the common case, and an insert would leave the piece
	// with two folded shots and the builder picking whichever came back
	// first.
	Attach(ctx context.Context, img *domain.ItemImage, workspaceID uuid.UUID) (*domain.ItemImage, error)
	// Remove clears one view of one piece. The ASSET survives: other pieces
	// may have resolved to the same bytes, and deleting them would blank a
	// photograph nobody touched.
	Remove(ctx context.Context, workspaceID, itemID uuid.UUID, view domain.ImageView) error
	// ListForItems returns every image of every listed piece, in one query.
	// The result is keyed by item id so the service can attach them without
	// a lookup per row.
	ListForItems(ctx context.Context, workspaceID uuid.UUID, itemIDs []uuid.UUID) (map[uuid.UUID][]domain.ItemImage, error)
}

/* ── assets ──────────────────────────────────────────────────────────── */

// AssetStore is where the bytes live.
//
// ══════════════════════════════════════════════════════════════════════
//
//	THIS INTERFACE IS THE EXIT FROM STORING BLOBS IN POSTGRES
//
// ══════════════════════════════════════════════════════════════════════
//
// Nothing above it knows where an image is kept. The Postgres
// implementation is the right answer for a deployment with no object
// storage, an ephemeral container filesystem and a proven database backup —
// the migration's header argues that at length. When one of those three
// facts changes, an S3 implementation replaces one file and no domain rule,
// no service, no handler and no screen changes.
//
// Which is also why Put takes a whole domain.Asset rather than an io.Reader
// and a length: the caller has already decoded and verified the image, and
// an interface that accepted an unverified stream would be one an
// implementation had to re-verify.
type AssetStore interface {
	// Put stores the bytes, or resolves to the row that already holds them.
	//
	// Deduplication is by content digest within the workspace: uploading
	// the same PNG twice costs one row. `created` reports which happened,
	// which is the difference between "stored" and "recognised" in a log
	// line — the only place it matters.
	Put(ctx context.Context, workspaceID uuid.UUID, asset domain.Asset) (stored *domain.Asset, created bool, err error)
	// Get returns one asset WITH its bytes, scoped to the workspace.
	Get(ctx context.Context, workspaceID, id uuid.UUID) (*domain.Asset, error)
}

/* ── looks ───────────────────────────────────────────────────────────── */

// LookFilter narrows a listing of saved looks.
type LookFilter struct {
	Occasion        *domain.Occasion
	Favorite        *bool
	IncludeArchived bool
	Search          string

	Limit  int
	Offset int
}

type LookRepo interface {
	// List returns matching looks, most recently touched first, WITHOUT
	// their slots. The service fills those in — see FindItems.
	List(ctx context.Context, workspaceID uuid.UUID, f LookFilter) ([]*domain.Look, error)
	Count(ctx context.Context, workspaceID uuid.UUID, f LookFilter) (int64, error)
	FindByID(ctx context.Context, workspaceID, id uuid.UUID) (*domain.Look, error)
	Create(ctx context.Context, look *domain.Look) error
	// Update writes the mutable fields and not the slots, for the reason
	// ItemRepo.Update does not write status: renaming a look and
	// recomposing it are different acts.
	Update(ctx context.Context, look *domain.Look) error
	SetStatus(ctx context.Context, workspaceID, id uuid.UUID, status domain.Status) (*domain.Look, error)

	// FindItems returns one look's filled slots, without hydrating the
	// pieces.
	FindItems(ctx context.Context, workspaceID, lookID uuid.UUID) ([]domain.LookItem, error)
	// FindItemsForLooks returns the filled slots of several looks at once,
	// keyed by look id, so a gallery is two queries rather than one per
	// card.
	FindItemsForLooks(ctx context.Context, workspaceID uuid.UUID, lookIDs []uuid.UUID) (map[uuid.UUID][]domain.LookItem, error)
	// ReplaceItems writes a look's composition as a whole, in ONE
	// transaction, and stamps the look's updated_at.
	//
	// ── Why replace and not a diff ─────────────────────────────────────
	// Because the domain already computed the answer. domain.Composition
	// decided what a placement displaced and where every position landed;
	// a repository that re-derived a minimal diff would be deciding the
	// same thing twice, in SQL, with the chance of disagreeing. A look is
	// at most nine rows — writing all of them is cheaper than being clever
	// and is atomic by construction.
	ReplaceItems(ctx context.Context, workspaceID, lookID uuid.UUID, items []domain.LookItem) error
}
