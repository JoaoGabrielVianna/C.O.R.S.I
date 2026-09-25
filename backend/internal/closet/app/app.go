// Package app is the Closet application layer: the use cases every writer
// shares.
//
// ── Why this layer exists with only one writer today ───────────────────
// Because there will be two. This sprint ships screens and no agent; the
// stylist that comes later reaches the same operations, through the same
// seam, with the rules already stated once. A module whose rules live in
// its HTTP handlers is a module where adding a second caller means copying
// them — and the copy is where "an archived garment cannot enter a new
// look" quietly stops being true on one path.
//
// Nothing outside this package holds a repository, so there is no path that
// skips what is below this line.
package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/closet/domain"
	"github.com/corsi/backend/internal/closet/ports"
)

type Service struct {
	items  ports.ItemRepo
	images ports.ImageRepo
	assets ports.AssetStore
	looks  ports.LookRepo
	log    *slog.Logger
	// now is the clock, injectable so a test can assert on a timestamp
	// instead of asserting that one exists.
	now func() time.Time
}

func NewService(
	items ports.ItemRepo,
	images ports.ImageRepo,
	assets ports.AssetStore,
	looks ports.LookRepo,
	log *slog.Logger,
) *Service {
	return &Service{items: items, images: images, assets: assets, looks: looks, log: log, now: time.Now}
}

// WithClock returns a copy that reads time from fn. Used by tests.
func (s *Service) WithClock(fn func() time.Time) *Service {
	cp := *s
	cp.now = fn
	return &cp
}

/* ── items: reads ────────────────────────────────────────────────────── */

// ListItems returns matching pieces WITH their images, and the unbounded
// total.
//
// The total is returned alongside for the reason every listing here does:
// a selector showing 50 of 130 has to be able to say so.
func (s *Service) ListItems(ctx context.Context, workspaceID uuid.UUID, f ports.ItemFilter) ([]*domain.ClosetItem, int64, error) {
	items, err := s.items.List(ctx, workspaceID, f)
	if err != nil {
		return nil, 0, err
	}
	if err := s.attachImages(ctx, workspaceID, items); err != nil {
		return nil, 0, err
	}
	total, err := s.items.Count(ctx, workspaceID, f)
	if err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

func (s *Service) GetItem(ctx context.Context, workspaceID, id uuid.UUID) (*domain.ClosetItem, error) {
	item, err := s.items.FindByID(ctx, workspaceID, id)
	if err != nil {
		return nil, err
	}
	if err := s.attachImages(ctx, workspaceID, []*domain.ClosetItem{item}); err != nil {
		return nil, err
	}
	return item, nil
}

// attachImages fills in the images of a set of pieces with ONE query, and
// sorts each piece's images into its category's declared order.
//
// A loop of per-item reads would be the classic N+1, and it would show:
// the selector's whole job is to draw forty pieces at once.
func (s *Service) attachImages(ctx context.Context, workspaceID uuid.UUID, items []*domain.ClosetItem) error {
	if len(items) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	byItem, err := s.images.ListForItems(ctx, workspaceID, ids)
	if err != nil {
		return err
	}
	for _, item := range items {
		// A piece with no photographs gets `[]` rather than `null`, so a
		// client never has to tell two spellings of nothing apart.
		item.Images = domain.SortImages(item.Category, byItem[item.ID])
		if item.Images == nil {
			item.Images = []domain.ItemImage{}
		}
	}
	return nil
}

/* ── items: writes ───────────────────────────────────────────────────── */

// CreateItemInput is one new garment, as any caller states it.
type CreateItemInput struct {
	Name           string
	Category       domain.Category
	Subtype        string
	PrimaryColor   string
	SecondaryColor string
	Brand          string
	Notes          string
	Favorite       bool
}

func (s *Service) CreateItem(ctx context.Context, workspaceID uuid.UUID, in CreateItemInput) (*domain.ClosetItem, error) {
	now := s.now()
	item := &domain.ClosetItem{
		ID:             uuid.New(),
		WorkspaceID:    workspaceID,
		Name:           in.Name,
		Category:       in.Category,
		Subtype:        in.Subtype,
		PrimaryColor:   in.PrimaryColor,
		SecondaryColor: in.SecondaryColor,
		Brand:          in.Brand,
		Notes:          in.Notes,
		Favorite:       in.Favorite,
		// A new piece is in the wardrobe. Creating something already
		// archived is not a state anybody wants and not one this layer
		// offers.
		Status:    domain.StatusActive,
		Images:    []domain.ItemImage{},
		CreatedAt: now,
		UpdatedAt: now,
	}
	item.Normalize()
	if err := item.Validate(); err != nil {
		return nil, err
	}
	if err := s.items.Create(ctx, item); err != nil {
		return nil, err
	}
	return item, nil
}

// UpdateItemInput carries only what was sent. A nil field is "leave it
// alone", which is what makes a detail panel able to save one edited field
// without echoing back the five it did not touch.
type UpdateItemInput struct {
	Name           *string
	Category       *domain.Category
	Subtype        *string
	PrimaryColor   *string
	SecondaryColor *string
	Brand          *string
	Notes          *string
	Favorite       *bool
}

// UpdateItem writes the mutable fields of a piece.
//
// ── Changing a category is allowed, and what it does NOT do ────────────
// It changes which slot the piece fills FROM NOW ON, and it changes which
// views the piece may be given. It does not touch any look that already
// used it: those rows recorded the slot they were filed under, so a shirt
// recategorised as a layer stays where it was in yesterday's outfit. That
// is the whole reason look_items stores a slot instead of joining to the
// category.
//
// It also does not delete images the new category cannot carry. A `folded`
// shot on a piece moved to `watches` stops being offered and keeps being
// shown — see domain.SortImages. Deleting the operator's photograph because
// a dropdown changed would be the system destroying work to tidy itself up.
func (s *Service) UpdateItem(ctx context.Context, workspaceID, id uuid.UUID, in UpdateItemInput) (*domain.ClosetItem, error) {
	item, err := s.items.FindByID(ctx, workspaceID, id)
	if err != nil {
		return nil, err
	}

	if in.Name != nil {
		item.Name = *in.Name
	}
	if in.Category != nil {
		item.Category = *in.Category
	}
	if in.Subtype != nil {
		item.Subtype = *in.Subtype
	}
	if in.PrimaryColor != nil {
		item.PrimaryColor = *in.PrimaryColor
	}
	if in.SecondaryColor != nil {
		item.SecondaryColor = *in.SecondaryColor
	}
	if in.Brand != nil {
		item.Brand = *in.Brand
	}
	if in.Notes != nil {
		item.Notes = *in.Notes
	}
	if in.Favorite != nil {
		item.Favorite = *in.Favorite
	}

	item.Normalize()
	if err := item.Validate(); err != nil {
		return nil, err
	}
	item.UpdatedAt = s.now()
	if err := s.items.Update(ctx, item); err != nil {
		return nil, err
	}
	if err := s.attachImages(ctx, workspaceID, []*domain.ClosetItem{item}); err != nil {
		return nil, err
	}
	return item, nil
}

// ArchiveResult is a completed archive, with what it touched.
type ArchiveResult struct {
	Item *domain.ClosetItem
	// LooksAffected is how many live looks still reference this piece.
	//
	// It is information, never a veto: the garment left the wardrobe
	// whatever the gallery says, and a system that refused to record that
	// would be arguing with the operator about their own closet. What it
	// buys is a screen that can say "this is in 3 looks" instead of the
	// operator discovering it later.
	LooksAffected int64
}

func (s *Service) ArchiveItem(ctx context.Context, workspaceID, id uuid.UUID) (ArchiveResult, error) {
	affected, err := s.items.CountLooksUsing(ctx, workspaceID, id)
	if err != nil {
		return ArchiveResult{}, err
	}
	item, err := s.items.SetStatus(ctx, workspaceID, id, domain.StatusArchived)
	if err != nil {
		return ArchiveResult{}, err
	}
	if err := s.attachImages(ctx, workspaceID, []*domain.ClosetItem{item}); err != nil {
		return ArchiveResult{}, err
	}
	s.log.Info("closet: item archived",
		"workspace_id", workspaceID, "item_id", id, "looks_affected", affected)
	return ArchiveResult{Item: item, LooksAffected: affected}, nil
}

func (s *Service) RestoreItem(ctx context.Context, workspaceID, id uuid.UUID) (*domain.ClosetItem, error) {
	item, err := s.items.SetStatus(ctx, workspaceID, id, domain.StatusActive)
	if err != nil {
		return nil, err
	}
	if err := s.attachImages(ctx, workspaceID, []*domain.ClosetItem{item}); err != nil {
		return nil, err
	}
	return item, nil
}

/* ── images ──────────────────────────────────────────────────────────── */

// AttachImage stores an uploaded file and files it under one view of one
// piece.
//
// ── The order of operations, and why it is this one ────────────────────
//  1. The piece is resolved IN THIS WORKSPACE. An upload aimed at an id
//     that is not yours fails here, before any bytes are stored.
//  2. The view is parsed AGAINST THE PIECE'S CATEGORY, so a watch refuses
//     `hanger_front` rather than accepting it and rendering nonsense.
//  3. The bytes are decoded and verified — never trusted from what the
//     request claimed about them.
//  4. Only then are they stored, and the row written.
//
// Storing first and validating after would leave orphaned megabytes behind
// every rejected upload.
func (s *Service) AttachImage(
	ctx context.Context,
	workspaceID, itemID uuid.UUID,
	rawView string,
	raw []byte,
) (*domain.ItemImage, error) {
	item, err := s.items.FindByID(ctx, workspaceID, itemID)
	if err != nil {
		return nil, err
	}
	view, err := domain.ParseView(item.Category, rawView)
	if err != nil {
		return nil, err
	}
	decoded, err := domain.DecodeImage(raw)
	if err != nil {
		return nil, err
	}

	asset, created, err := s.assets.Put(ctx, workspaceID, decoded)
	if err != nil {
		return nil, err
	}

	now := s.now()
	img := &domain.ItemImage{
		ID:          uuid.New(),
		ItemID:      itemID,
		View:        view,
		AssetID:     asset.ID,
		ContentType: asset.ContentType,
		ByteSize:    asset.ByteSize,
		Width:       asset.Width,
		Height:      asset.Height,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	stored, err := s.images.Attach(ctx, img, workspaceID)
	if err != nil {
		return nil, err
	}
	s.log.Info("closet: image attached",
		"workspace_id", workspaceID, "item_id", itemID, "view", view,
		"bytes", asset.ByteSize, "asset_created", created)
	return stored, nil
}

// RemoveImage clears one view of one piece. The asset survives: another
// piece may have resolved to the same bytes.
func (s *Service) RemoveImage(ctx context.Context, workspaceID, itemID uuid.UUID, rawView string) error {
	item, err := s.items.FindByID(ctx, workspaceID, itemID)
	if err != nil {
		return err
	}
	view, err := domain.ParseView(item.Category, rawView)
	if err != nil {
		return err
	}
	return s.images.Remove(ctx, workspaceID, itemID, view)
}

// GetAsset returns the bytes of one image, scoped to the workspace.
func (s *Service) GetAsset(ctx context.Context, workspaceID, id uuid.UUID) (*domain.Asset, error) {
	return s.assets.Get(ctx, workspaceID, id)
}

/* ── looks: reads ────────────────────────────────────────────────────── */

// ListLooks returns matching looks with their slots hydrated, and the
// unbounded total.
func (s *Service) ListLooks(ctx context.Context, workspaceID uuid.UUID, f ports.LookFilter) ([]*domain.Look, int64, error) {
	looks, err := s.looks.List(ctx, workspaceID, f)
	if err != nil {
		return nil, 0, err
	}
	if err := s.hydrateLooks(ctx, workspaceID, looks); err != nil {
		return nil, 0, err
	}
	total, err := s.looks.Count(ctx, workspaceID, f)
	if err != nil {
		return nil, 0, err
	}
	return looks, total, nil
}

func (s *Service) GetLook(ctx context.Context, workspaceID, id uuid.UUID) (*domain.Look, error) {
	look, err := s.looks.FindByID(ctx, workspaceID, id)
	if err != nil {
		return nil, err
	}
	if err := s.hydrateLooks(ctx, workspaceID, []*domain.Look{look}); err != nil {
		return nil, err
	}
	return look, nil
}

// hydrateLooks fills in each look's slots AND the piece in each slot, in a
// bounded number of queries regardless of how many looks there are.
//
// Three reads total: the slots of every look, the pieces every slot names,
// and the images of every one of those pieces. A gallery of thirty looks is
// therefore three queries, not ninety.
//
// ── A slot whose piece did not resolve ─────────────────────────────────
// It is kept, with a nil Item. That can only happen if a piece was hard
// deleted, which nothing in this module does — but if it ever does, a look
// that quietly lost a slot would be a look that lies about what it was.
func (s *Service) hydrateLooks(ctx context.Context, workspaceID uuid.UUID, looks []*domain.Look) error {
	if len(looks) == 0 {
		return nil
	}
	lookIDs := make([]uuid.UUID, 0, len(looks))
	for _, look := range looks {
		lookIDs = append(lookIDs, look.ID)
	}
	slotsByLook, err := s.looks.FindItemsForLooks(ctx, workspaceID, lookIDs)
	if err != nil {
		return err
	}

	// Collect the distinct pieces every look references, so they are read
	// once even when five looks share the same jacket.
	wanted := map[uuid.UUID]struct{}{}
	for _, entries := range slotsByLook {
		for _, entry := range entries {
			wanted[entry.ItemID] = struct{}{}
		}
	}
	itemIDs := make([]uuid.UUID, 0, len(wanted))
	for id := range wanted {
		itemIDs = append(itemIDs, id)
	}

	items, err := s.items.FindManyByID(ctx, workspaceID, itemIDs)
	if err != nil {
		return err
	}
	if err := s.attachImages(ctx, workspaceID, items); err != nil {
		return err
	}
	byID := make(map[uuid.UUID]*domain.ClosetItem, len(items))
	for _, item := range items {
		byID[item.ID] = item
	}

	for _, look := range looks {
		entries := domain.NewComposition(slotsByLook[look.ID]).Items()
		for idx := range entries {
			entries[idx].Item = byID[entries[idx].ItemID]
		}
		if entries == nil {
			entries = []domain.LookItem{}
		}
		look.Items = entries
	}
	return nil
}

/* ── looks: writes ───────────────────────────────────────────────────── */

// CreateLookInput is one new look. The composition is optional: saving an
// empty look is how "start one and come back to it" works.
type CreateLookInput struct {
	Name     string
	Occasion domain.Occasion
	Favorite bool
	Notes    string
	ItemIDs  []uuid.UUID
}

func (s *Service) CreateLook(ctx context.Context, workspaceID uuid.UUID, in CreateLookInput) (*domain.Look, error) {
	now := s.now()
	look := &domain.Look{
		ID:          uuid.New(),
		WorkspaceID: workspaceID,
		Name:        in.Name,
		Occasion:    in.Occasion,
		Favorite:    in.Favorite,
		Notes:       in.Notes,
		Status:      domain.StatusActive,
		Items:       []domain.LookItem{},
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	look.Normalize()
	if err := look.Validate(); err != nil {
		return nil, err
	}

	// The composition is built and VALIDATED before the look row exists, so
	// a rejected piece leaves nothing behind.
	entries, err := s.compose(ctx, workspaceID, in.ItemIDs, now)
	if err != nil {
		return nil, err
	}

	if err := s.looks.Create(ctx, look); err != nil {
		return nil, err
	}
	if len(entries) > 0 {
		if err := s.looks.ReplaceItems(ctx, workspaceID, look.ID, entries); err != nil {
			return nil, err
		}
	}
	return s.GetLook(ctx, workspaceID, look.ID)
}

type UpdateLookInput struct {
	Name     *string
	Occasion *domain.Occasion
	Favorite *bool
	Notes    *string
}

func (s *Service) UpdateLook(ctx context.Context, workspaceID, id uuid.UUID, in UpdateLookInput) (*domain.Look, error) {
	look, err := s.looks.FindByID(ctx, workspaceID, id)
	if err != nil {
		return nil, err
	}
	if in.Name != nil {
		look.Name = *in.Name
	}
	if in.Occasion != nil {
		look.Occasion = *in.Occasion
	}
	if in.Favorite != nil {
		look.Favorite = *in.Favorite
	}
	if in.Notes != nil {
		look.Notes = *in.Notes
	}
	look.Normalize()
	if err := look.Validate(); err != nil {
		return nil, err
	}
	look.UpdatedAt = s.now()
	if err := s.looks.Update(ctx, look); err != nil {
		return nil, err
	}
	return s.GetLook(ctx, workspaceID, id)
}

// SetLookItems replaces a look's whole composition.
//
// ══════════════════════════════════════════════════════════════════════
//
//	THE SERVER RE-RUNS EVERY COMPOSITION RULE. THE CLIENT'S ORDER IS AN
//	INTENT, NOT A RESULT
//
// ══════════════════════════════════════════════════════════════════════
//
// The ids arrive in the order they were chosen and are placed one at a
// time through domain.Composition.Place, which is what applies slot
// capacity, eviction, de-duplication and position numbering. A payload with
// two shirts stores one shirt — the second — exactly as clicking two shirts
// in the builder does, because it is the same code deciding.
//
// Sending the ids rather than the slots is deliberate: the slot is derived
// from each piece's category, so a client cannot file a watch under
// `bottom` by naming it.
func (s *Service) SetLookItems(ctx context.Context, workspaceID, lookID uuid.UUID, itemIDs []uuid.UUID) (*domain.Look, error) {
	look, err := s.looks.FindByID(ctx, workspaceID, lookID)
	if err != nil {
		return nil, err
	}

	now := s.now()
	entries, err := s.compose(ctx, workspaceID, itemIDs, now)
	if err != nil {
		return nil, err
	}
	if err := s.looks.ReplaceItems(ctx, workspaceID, look.ID, entries); err != nil {
		return nil, err
	}
	return s.GetLook(ctx, workspaceID, look.ID)
}

// MaxLookItems bounds one composition.
//
// It is the sum of every slot's capacity, computed rather than typed, so
// adding a slot or widening one cannot leave a stale number behind.
func MaxLookItems() int {
	total := 0
	for _, def := range domain.Slots() {
		total += def.Capacity
	}
	return total
}

// compose turns a list of item ids into a validated composition.
//
// ── The three refusals, and why each is here rather than in SQL ────────
//   - A piece that is not in this workspace does not resolve. The lookup is
//     workspace-scoped, so an id from another tenant is simply not found —
//     the same answer as an id that never existed, which is what stops the
//     error message from confirming another workspace's row.
//   - An ARCHIVED piece is refused. A foreign key would happily accept it:
//     the row exists. "You cannot put the coat you gave away into a new
//     look" is a rule about the wardrobe, not about referential integrity.
//   - Anything past the total capacity is refused before any row is
//     written, so an oversized payload costs one read instead of a partial
//     composition somebody has to notice.
func (s *Service) compose(
	ctx context.Context,
	workspaceID uuid.UUID,
	itemIDs []uuid.UUID,
	now time.Time,
) ([]domain.LookItem, error) {
	if len(itemIDs) == 0 {
		return nil, nil
	}
	if max := MaxLookItems(); len(itemIDs) > max {
		return nil, domain.Invalid("a look holds at most %d pieces", max)
	}

	items, err := s.items.FindManyByID(ctx, workspaceID, itemIDs)
	if err != nil {
		return nil, err
	}
	byID := make(map[uuid.UUID]*domain.ClosetItem, len(items))
	for _, item := range items {
		byID[item.ID] = item
	}

	composition := domain.NewComposition(nil)
	// Placed one at a time, in the order the caller chose them, so the
	// eviction a later piece causes is the one the operator would have seen
	// clicking through the builder.
	//
	// `now` advances by one nanosecond per placement. Without it every
	// entry would carry the same instant, and the ordering inside a
	// multi-occupancy slot — which is "the order they were chosen" — would
	// depend on a tie-break the sort does not promise.
	for offset, id := range itemIDs {
		item, ok := byID[id]
		if !ok {
			return nil, domain.NotFound("no piece %s in this closet", id)
		}
		if item.Status == domain.StatusArchived {
			return nil, domain.Conflict(
				"%q is archived and cannot be put into a look; restore it first", item.Name)
		}
		slot, ok := item.Slot()
		if !ok {
			return nil, domain.Invalid(
				"%q is in category %q, which fills no slot", item.Name, item.Category)
		}
		if _, err := composition.Place(slot, item.ID, now.Add(time.Duration(offset))); err != nil {
			return nil, err
		}
	}
	return composition.Items(), nil
}

func (s *Service) ArchiveLook(ctx context.Context, workspaceID, id uuid.UUID) (*domain.Look, error) {
	if _, err := s.looks.SetStatus(ctx, workspaceID, id, domain.StatusArchived); err != nil {
		return nil, err
	}
	return s.GetLook(ctx, workspaceID, id)
}

func (s *Service) RestoreLook(ctx context.Context, workspaceID, id uuid.UUID) (*domain.Look, error) {
	if _, err := s.looks.SetStatus(ctx, workspaceID, id, domain.StatusActive); err != nil {
		return nil, err
	}
	return s.GetLook(ctx, workspaceID, id)
}
