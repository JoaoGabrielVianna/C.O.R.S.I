// Package repo is the Postgres implementation of the Closet storage ports.
//
// Every query is workspace-first, for the reason the ports package states:
// an id that arrives from a URL must not be able to reach a row it does not
// own, and the predicate that guarantees it belongs in the SQL rather than
// in a check somebody has to remember to write.
//
// ── The one query that matters most ────────────────────────────────────
// AssetRepo.Get. It returns bytes that a browser renders directly, so it is
// the read where a missing workspace predicate would be an actual data
// leak rather than a confusing listing. It is written workspace-first like
// everything else here, and the integration suite asserts it by asking for
// another workspace's asset id by hand.
package repo

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/corsi/backend/internal/closet/domain"
	"github.com/corsi/backend/internal/closet/ports"
	"github.com/corsi/backend/internal/platform/postgres"
)

// Repos bundles the implementations so the module wires one value.
type Repos struct {
	Items  ports.ItemRepo
	Images ports.ImageRepo
	Assets ports.AssetStore
	Looks  ports.LookRepo
}

func New(pool *pgxpool.Pool) *Repos {
	txm := postgres.NewTxManager(pool)
	return &Repos{
		Items:  &ItemRepo{pool: pool},
		Images: &ImageRepo{pool: pool},
		Assets: &AssetRepo{pool: pool},
		Looks:  &LookRepo{pool: pool, txm: txm},
	}
}

// defaultLimit bounds a listing that did not ask for one, and maxLimit is
// the ceiling a caller cannot argue past.
//
// A hundred is a generous working set for a selector that draws one
// category at a time; the whole wardrobe is a few hundred pieces, and an
// unbounded default would mean the first render pulling all of them.
const (
	defaultLimit = 100
	maxLimit     = 400
)

func boundLimit(n int) int {
	switch {
	case n <= 0:
		return defaultLimit
	case n > maxLimit:
		return maxLimit
	default:
		return n
	}
}

func boundOffset(n int) int {
	if n < 0 {
		return 0
	}
	return n
}

/* ── items ───────────────────────────────────────────────────────────── */

type ItemRepo struct{ pool *pgxpool.Pool }

const itemCols = `
	id, workspace_id, name, category, subtype,
	primary_color, secondary_color, brand, notes,
	favorite, status, created_at, updated_at`

func scanItem(row pgx.Row) (*domain.ClosetItem, error) {
	var (
		item     domain.ClosetItem
		category string
		status   string
	)
	if err := row.Scan(
		&item.ID, &item.WorkspaceID, &item.Name, &category, &item.Subtype,
		&item.PrimaryColor, &item.SecondaryColor, &item.Brand, &item.Notes,
		&item.Favorite, &status, &item.CreatedAt, &item.UpdatedAt,
	); err != nil {
		return nil, err
	}
	item.Category = domain.Category(category)
	item.Status = domain.Status(status)
	// `[]` rather than `null` for a piece with no photographs, so a client
	// never has to tell two spellings of nothing apart. The service
	// overwrites this with the real set; the default is what a read that
	// skipped hydration still returns.
	item.Images = []domain.ItemImage{}
	return &item, nil
}

// itemPredicates builds the WHERE clause shared by List and Count.
//
// One function for both because a filter that narrowed the page and not the
// total would make "showing 50 of 130" a lie, and the two would drift the
// first time a filter was added to one of them.
func itemPredicates(workspaceID uuid.UUID, f ports.ItemFilter) (string, []any) {
	where := []string{"workspace_id = $1"}
	args := []any{workspaceID}
	add := func(clause string, value any) {
		args = append(args, value)
		where = append(where, fmt.Sprintf(clause, len(args)))
	}

	switch {
	case f.Status != nil:
		add("status = $%d", string(*f.Status))
	case !f.IncludeArchived:
		// The default, and the important one: a selector must not offer a
		// garment that left the wardrobe.
		add("status = $%d", string(domain.StatusActive))
	}
	if f.Category != nil {
		add("category = $%d", string(*f.Category))
	}
	if f.Favorite != nil {
		add("favorite = $%d", *f.Favorite)
	}
	if s := strings.TrimSpace(f.Search); s != "" {
		args = append(args, "%"+strings.ToLower(s)+"%")
		idx := len(args)
		// Case-insensitive substring across the fields a person would type:
		// what it is called, who made it, what kind it is, what colour it
		// is. Not `notes`, deliberately — notes are long, and matching them
		// would make a search for "black" return every piece whose note
		// mentions a black pair of trousers it goes with.
		where = append(where, fmt.Sprintf(`(
			lower(name) LIKE $%[1]d OR lower(brand) LIKE $%[1]d OR
			lower(subtype) LIKE $%[1]d OR lower(primary_color) LIKE $%[1]d OR
			lower(secondary_color) LIKE $%[1]d)`, idx))
	}
	return strings.Join(where, " AND "), args
}

func (r *ItemRepo) List(ctx context.Context, workspaceID uuid.UUID, f ports.ItemFilter) ([]*domain.ClosetItem, error) {
	where, args := itemPredicates(workspaceID, f)
	args = append(args, boundLimit(f.Limit), boundOffset(f.Offset))

	rows, err := postgres.Conn(ctx, r.pool).Query(ctx, `
		SELECT `+itemCols+`
		FROM closet.items
		WHERE `+where+`
		ORDER BY favorite DESC, created_at DESC, id
		LIMIT $`+itoa(len(args)-1)+` OFFSET $`+itoa(len(args)), args...)
	if err != nil {
		return nil, fmt.Errorf("closet: list items: %w", err)
	}
	defer rows.Close()

	var out []*domain.ClosetItem
	for rows.Next() {
		item, err := scanItem(rows)
		if err != nil {
			return nil, fmt.Errorf("closet: scan item: %w", err)
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *ItemRepo) Count(ctx context.Context, workspaceID uuid.UUID, f ports.ItemFilter) (int64, error) {
	where, args := itemPredicates(workspaceID, f)
	var total int64
	if err := postgres.Conn(ctx, r.pool).QueryRow(ctx,
		`SELECT count(*) FROM closet.items WHERE `+where, args...).Scan(&total); err != nil {
		return 0, fmt.Errorf("closet: count items: %w", err)
	}
	return total, nil
}

func (r *ItemRepo) FindByID(ctx context.Context, workspaceID, id uuid.UUID) (*domain.ClosetItem, error) {
	item, err := scanItem(postgres.Conn(ctx, r.pool).QueryRow(ctx, `
		SELECT `+itemCols+`
		FROM closet.items
		WHERE workspace_id = $1 AND id = $2`, workspaceID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.NotFound("no piece %s in this closet", id)
	}
	if err != nil {
		return nil, fmt.Errorf("closet: read item: %w", err)
	}
	return item, nil
}

func (r *ItemRepo) FindManyByID(ctx context.Context, workspaceID uuid.UUID, ids []uuid.UUID) ([]*domain.ClosetItem, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := postgres.Conn(ctx, r.pool).Query(ctx, `
		SELECT `+itemCols+`
		FROM closet.items
		WHERE workspace_id = $1 AND id = ANY($2)`, workspaceID, ids)
	if err != nil {
		return nil, fmt.Errorf("closet: read items: %w", err)
	}
	defer rows.Close()

	var out []*domain.ClosetItem
	for rows.Next() {
		item, err := scanItem(rows)
		if err != nil {
			return nil, fmt.Errorf("closet: scan item: %w", err)
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *ItemRepo) Create(ctx context.Context, item *domain.ClosetItem) error {
	_, err := postgres.Conn(ctx, r.pool).Exec(ctx, `
		INSERT INTO closet.items (
			id, workspace_id, name, category, subtype,
			primary_color, secondary_color, brand, notes,
			favorite, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
		item.ID, item.WorkspaceID, item.Name, string(item.Category), item.Subtype,
		item.PrimaryColor, item.SecondaryColor, item.Brand, item.Notes,
		item.Favorite, string(item.Status), item.CreatedAt, item.UpdatedAt)
	if err != nil {
		return fmt.Errorf("closet: insert item: %w", err)
	}
	return nil
}

// Update writes the mutable fields. `status` is absent on purpose — see the
// port's note: archiving is a different decision with different
// consequences, and a generic update that carried it would let a caller
// fixing a typo retire a garment.
func (r *ItemRepo) Update(ctx context.Context, item *domain.ClosetItem) error {
	tag, err := postgres.Conn(ctx, r.pool).Exec(ctx, `
		UPDATE closet.items SET
			name = $3, category = $4, subtype = $5,
			primary_color = $6, secondary_color = $7, brand = $8, notes = $9,
			favorite = $10, updated_at = $11
		WHERE workspace_id = $1 AND id = $2`,
		item.WorkspaceID, item.ID, item.Name, string(item.Category), item.Subtype,
		item.PrimaryColor, item.SecondaryColor, item.Brand, item.Notes,
		item.Favorite, item.UpdatedAt)
	if err != nil {
		return fmt.Errorf("closet: update item: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.NotFound("no piece %s in this closet", item.ID)
	}
	return nil
}

func (r *ItemRepo) SetStatus(ctx context.Context, workspaceID, id uuid.UUID, status domain.Status) (*domain.ClosetItem, error) {
	item, err := scanItem(postgres.Conn(ctx, r.pool).QueryRow(ctx, `
		UPDATE closet.items SET status = $3, updated_at = now()
		WHERE workspace_id = $1 AND id = $2
		RETURNING `+itemCols, workspaceID, id, string(status)))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.NotFound("no piece %s in this closet", id)
	}
	if err != nil {
		return nil, fmt.Errorf("closet: set item status: %w", err)
	}
	return item, nil
}

func (r *ItemRepo) CountLooksUsing(ctx context.Context, workspaceID, itemID uuid.UUID) (int64, error) {
	var total int64
	if err := postgres.Conn(ctx, r.pool).QueryRow(ctx, `
		SELECT count(*)
		FROM closet.look_items li
		JOIN closet.looks l ON l.id = li.look_id
		WHERE li.workspace_id = $1 AND li.item_id = $2 AND l.status = 'active'`,
		workspaceID, itemID).Scan(&total); err != nil {
		return 0, fmt.Errorf("closet: count looks using item: %w", err)
	}
	return total, nil
}

/* ── images ──────────────────────────────────────────────────────────── */

type ImageRepo struct{ pool *pgxpool.Pool }

// imageCols joins the asset's metadata in rather than denormalising it onto
// the image row. Width, size and content-type are properties OF THE BYTES:
// copying them here would mean two rows that both claim to know how big an
// image is, and nothing to stop them disagreeing.
const imageCols = `
	i.id, i.item_id, i.view, i.asset_id,
	a.content_type, a.byte_size, a.width, a.height,
	i.created_at, i.updated_at`

func scanImage(row pgx.Row) (*domain.ItemImage, error) {
	var (
		img  domain.ItemImage
		view string
	)
	if err := row.Scan(
		&img.ID, &img.ItemID, &view, &img.AssetID,
		&img.ContentType, &img.ByteSize, &img.Width, &img.Height,
		&img.CreatedAt, &img.UpdatedAt,
	); err != nil {
		return nil, err
	}
	img.View = domain.ImageView(view)
	return &img, nil
}

// Attach upserts on (item_id, view).
//
// ── Why the conflict target is the unique index and not a read-first ───
// A read-then-insert would have a window in which two uploads of the same
// view both find nothing and both insert, and the unique index would then
// fail the second with a constraint error the caller has to interpret.
// `ON CONFLICT DO UPDATE` makes replacement the normal path, which is what
// it actually is: re-shooting a folded photo is routine.
func (r *ImageRepo) Attach(ctx context.Context, img *domain.ItemImage, workspaceID uuid.UUID) (*domain.ItemImage, error) {
	if _, err := postgres.Conn(ctx, r.pool).Exec(ctx, `
		INSERT INTO closet.item_images (
			id, workspace_id, item_id, view, asset_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (item_id, view) DO UPDATE
			SET asset_id = EXCLUDED.asset_id, updated_at = EXCLUDED.updated_at`,
		img.ID, workspaceID, img.ItemID, string(img.View), img.AssetID,
		img.CreatedAt, img.UpdatedAt); err != nil {
		return nil, fmt.Errorf("closet: attach image: %w", err)
	}

	stored, err := scanImage(postgres.Conn(ctx, r.pool).QueryRow(ctx, `
		SELECT `+imageCols+`
		FROM closet.item_images i
		JOIN closet.assets a ON a.id = i.asset_id
		WHERE i.workspace_id = $1 AND i.item_id = $2 AND i.view = $3`,
		workspaceID, img.ItemID, string(img.View)))
	if err != nil {
		return nil, fmt.Errorf("closet: read attached image: %w", err)
	}
	return stored, nil
}

func (r *ImageRepo) Remove(ctx context.Context, workspaceID, itemID uuid.UUID, view domain.ImageView) error {
	tag, err := postgres.Conn(ctx, r.pool).Exec(ctx, `
		DELETE FROM closet.item_images
		WHERE workspace_id = $1 AND item_id = $2 AND view = $3`,
		workspaceID, itemID, string(view))
	if err != nil {
		return fmt.Errorf("closet: remove image: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.NotFound("that piece has no %s image", view)
	}
	return nil
}

func (r *ImageRepo) ListForItems(ctx context.Context, workspaceID uuid.UUID, itemIDs []uuid.UUID) (map[uuid.UUID][]domain.ItemImage, error) {
	out := map[uuid.UUID][]domain.ItemImage{}
	if len(itemIDs) == 0 {
		return out, nil
	}
	rows, err := postgres.Conn(ctx, r.pool).Query(ctx, `
		SELECT `+imageCols+`
		FROM closet.item_images i
		JOIN closet.assets a ON a.id = i.asset_id
		WHERE i.workspace_id = $1 AND i.item_id = ANY($2)`, workspaceID, itemIDs)
	if err != nil {
		return nil, fmt.Errorf("closet: list images: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		img, err := scanImage(rows)
		if err != nil {
			return nil, fmt.Errorf("closet: scan image: %w", err)
		}
		out[img.ItemID] = append(out[img.ItemID], *img)
	}
	return out, rows.Err()
}

/* ── assets ──────────────────────────────────────────────────────────── */

type AssetRepo struct{ pool *pgxpool.Pool }

// Put inserts the bytes, or resolves to the row that already holds them.
//
// ── Why the insert comes first ─────────────────────────────────────────
// Same shape as jobradar's FindOrCreate, for the same reason: a
// read-then-write has a window in which two uploads of the same file both
// find nothing and both insert. `ON CONFLICT DO NOTHING` closes it in the
// database — the insert either wins or does nothing, and the SELECT that
// follows returns the winner either way.
//
// `created` is derived from whether the insert returned a row, which is the
// only honest source for it: a caller that inferred it from a prior read
// would be reporting what was true before the race.
func (r *AssetRepo) Put(ctx context.Context, workspaceID uuid.UUID, asset domain.Asset) (*domain.Asset, bool, error) {
	conn := postgres.Conn(ctx, r.pool)

	var (
		id      uuid.UUID
		created bool
	)
	err := conn.QueryRow(ctx, `
		INSERT INTO closet.assets (
			workspace_id, sha256, content_type, byte_size, width, height, bytes)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (workspace_id, sha256) DO NOTHING
		RETURNING id`,
		workspaceID, asset.SHA256, asset.ContentType,
		asset.ByteSize, asset.Width, asset.Height, asset.Bytes).Scan(&id)
	switch {
	case err == nil:
		created = true
	case errors.Is(err, pgx.ErrNoRows):
		// The row already existed. Its id is what the caller needs.
		if err := conn.QueryRow(ctx, `
			SELECT id FROM closet.assets
			WHERE workspace_id = $1 AND sha256 = $2`,
			workspaceID, asset.SHA256).Scan(&id); err != nil {
			return nil, false, fmt.Errorf("closet: resolve existing asset: %w", err)
		}
	default:
		return nil, false, fmt.Errorf("closet: insert asset: %w", err)
	}

	stored := asset
	stored.ID = id
	stored.WorkspaceID = workspaceID
	return &stored, created, nil
}

// Get returns one asset WITH its bytes.
//
// The workspace predicate is the whole security boundary for the image
// route: this is the only read in the module that hands raw bytes to a
// browser, and an id-only lookup would serve another workspace's
// photographs to anyone holding a uuid.
func (r *AssetRepo) Get(ctx context.Context, workspaceID, id uuid.UUID) (*domain.Asset, error) {
	var asset domain.Asset
	err := postgres.Conn(ctx, r.pool).QueryRow(ctx, `
		SELECT id, workspace_id, sha256, content_type, byte_size, width, height, bytes, created_at
		FROM closet.assets
		WHERE workspace_id = $1 AND id = $2`, workspaceID, id).Scan(
		&asset.ID, &asset.WorkspaceID, &asset.SHA256, &asset.ContentType,
		&asset.ByteSize, &asset.Width, &asset.Height, &asset.Bytes, &asset.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.NotFound("no image %s in this closet", id)
	}
	if err != nil {
		return nil, fmt.Errorf("closet: read asset: %w", err)
	}
	return &asset, nil
}

/* ── looks ───────────────────────────────────────────────────────────── */

type LookRepo struct {
	pool *pgxpool.Pool
	txm  *postgres.TxManager
}

const lookCols = `
	id, workspace_id, name, occasion, favorite, notes, status,
	created_at, updated_at`

func scanLook(row pgx.Row) (*domain.Look, error) {
	var (
		look     domain.Look
		occasion string
		status   string
	)
	if err := row.Scan(
		&look.ID, &look.WorkspaceID, &look.Name, &occasion,
		&look.Favorite, &look.Notes, &status,
		&look.CreatedAt, &look.UpdatedAt,
	); err != nil {
		return nil, err
	}
	look.Occasion = domain.Occasion(occasion)
	look.Status = domain.Status(status)
	look.Items = []domain.LookItem{}
	return &look, nil
}

func lookPredicates(workspaceID uuid.UUID, f ports.LookFilter) (string, []any) {
	where := []string{"workspace_id = $1"}
	args := []any{workspaceID}
	add := func(clause string, value any) {
		args = append(args, value)
		where = append(where, fmt.Sprintf(clause, len(args)))
	}

	if !f.IncludeArchived {
		add("status = $%d", string(domain.StatusActive))
	}
	if f.Occasion != nil {
		add("occasion = $%d", string(*f.Occasion))
	}
	if f.Favorite != nil {
		add("favorite = $%d", *f.Favorite)
	}
	if s := strings.TrimSpace(f.Search); s != "" {
		add("lower(name) LIKE $%d", "%"+strings.ToLower(s)+"%")
	}
	return strings.Join(where, " AND "), args
}

func (r *LookRepo) List(ctx context.Context, workspaceID uuid.UUID, f ports.LookFilter) ([]*domain.Look, error) {
	where, args := lookPredicates(workspaceID, f)
	args = append(args, boundLimit(f.Limit), boundOffset(f.Offset))

	rows, err := postgres.Conn(ctx, r.pool).Query(ctx, `
		SELECT `+lookCols+`
		FROM closet.looks
		WHERE `+where+`
		ORDER BY favorite DESC, updated_at DESC, id
		LIMIT $`+itoa(len(args)-1)+` OFFSET $`+itoa(len(args)), args...)
	if err != nil {
		return nil, fmt.Errorf("closet: list looks: %w", err)
	}
	defer rows.Close()

	var out []*domain.Look
	for rows.Next() {
		look, err := scanLook(rows)
		if err != nil {
			return nil, fmt.Errorf("closet: scan look: %w", err)
		}
		out = append(out, look)
	}
	return out, rows.Err()
}

func (r *LookRepo) Count(ctx context.Context, workspaceID uuid.UUID, f ports.LookFilter) (int64, error) {
	where, args := lookPredicates(workspaceID, f)
	var total int64
	if err := postgres.Conn(ctx, r.pool).QueryRow(ctx,
		`SELECT count(*) FROM closet.looks WHERE `+where, args...).Scan(&total); err != nil {
		return 0, fmt.Errorf("closet: count looks: %w", err)
	}
	return total, nil
}

func (r *LookRepo) FindByID(ctx context.Context, workspaceID, id uuid.UUID) (*domain.Look, error) {
	look, err := scanLook(postgres.Conn(ctx, r.pool).QueryRow(ctx, `
		SELECT `+lookCols+`
		FROM closet.looks
		WHERE workspace_id = $1 AND id = $2`, workspaceID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.NotFound("no look %s in this closet", id)
	}
	if err != nil {
		return nil, fmt.Errorf("closet: read look: %w", err)
	}
	return look, nil
}

func (r *LookRepo) Create(ctx context.Context, look *domain.Look) error {
	_, err := postgres.Conn(ctx, r.pool).Exec(ctx, `
		INSERT INTO closet.looks (
			id, workspace_id, name, occasion, favorite, notes, status,
			created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		look.ID, look.WorkspaceID, look.Name, string(look.Occasion),
		look.Favorite, look.Notes, string(look.Status),
		look.CreatedAt, look.UpdatedAt)
	if err != nil {
		return fmt.Errorf("closet: insert look: %w", err)
	}
	return nil
}

func (r *LookRepo) Update(ctx context.Context, look *domain.Look) error {
	tag, err := postgres.Conn(ctx, r.pool).Exec(ctx, `
		UPDATE closet.looks SET
			name = $3, occasion = $4, favorite = $5, notes = $6, updated_at = $7
		WHERE workspace_id = $1 AND id = $2`,
		look.WorkspaceID, look.ID, look.Name, string(look.Occasion),
		look.Favorite, look.Notes, look.UpdatedAt)
	if err != nil {
		return fmt.Errorf("closet: update look: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.NotFound("no look %s in this closet", look.ID)
	}
	return nil
}

func (r *LookRepo) SetStatus(ctx context.Context, workspaceID, id uuid.UUID, status domain.Status) (*domain.Look, error) {
	look, err := scanLook(postgres.Conn(ctx, r.pool).QueryRow(ctx, `
		UPDATE closet.looks SET status = $3, updated_at = now()
		WHERE workspace_id = $1 AND id = $2
		RETURNING `+lookCols, workspaceID, id, string(status)))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.NotFound("no look %s in this closet", id)
	}
	if err != nil {
		return nil, fmt.Errorf("closet: set look status: %w", err)
	}
	return look, nil
}

func scanLookItem(row pgx.Row) (domain.LookItem, uuid.UUID, error) {
	var (
		entry  domain.LookItem
		lookID uuid.UUID
		slot   string
	)
	if err := row.Scan(&lookID, &slot, &entry.Position, &entry.ItemID, &entry.CreatedAt); err != nil {
		return domain.LookItem{}, uuid.Nil, err
	}
	entry.Slot = domain.Slot(slot)
	return entry, lookID, nil
}

func (r *LookRepo) FindItems(ctx context.Context, workspaceID, lookID uuid.UUID) ([]domain.LookItem, error) {
	byLook, err := r.FindItemsForLooks(ctx, workspaceID, []uuid.UUID{lookID})
	if err != nil {
		return nil, err
	}
	return byLook[lookID], nil
}

func (r *LookRepo) FindItemsForLooks(ctx context.Context, workspaceID uuid.UUID, lookIDs []uuid.UUID) (map[uuid.UUID][]domain.LookItem, error) {
	out := map[uuid.UUID][]domain.LookItem{}
	if len(lookIDs) == 0 {
		return out, nil
	}
	rows, err := postgres.Conn(ctx, r.pool).Query(ctx, `
		SELECT look_id, slot, position, item_id, created_at
		FROM closet.look_items
		WHERE workspace_id = $1 AND look_id = ANY($2)
		ORDER BY look_id, slot, position`, workspaceID, lookIDs)
	if err != nil {
		return nil, fmt.Errorf("closet: list look items: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		entry, lookID, err := scanLookItem(rows)
		if err != nil {
			return nil, fmt.Errorf("closet: scan look item: %w", err)
		}
		out[lookID] = append(out[lookID], entry)
	}
	return out, rows.Err()
}

// ReplaceItems writes a look's composition as a whole, in one transaction.
//
// Delete-then-insert rather than a computed diff, for the reason the port
// gives: the domain already decided the answer, and a repository that
// re-derived a minimal change set would be deciding it a second time in
// SQL. A look is at most nine rows.
//
// The look's `updated_at` moves in the same transaction, because a gallery
// ordered by it must not show a recomposed look as untouched.
func (r *LookRepo) ReplaceItems(ctx context.Context, workspaceID, lookID uuid.UUID, items []domain.LookItem) error {
	return r.txm.WithinTx(ctx, func(ctx context.Context) error {
		conn := postgres.Conn(ctx, r.pool)

		if _, err := conn.Exec(ctx, `
			DELETE FROM closet.look_items
			WHERE workspace_id = $1 AND look_id = $2`, workspaceID, lookID); err != nil {
			return fmt.Errorf("closet: clear look items: %w", err)
		}

		for _, entry := range items {
			if _, err := conn.Exec(ctx, `
				INSERT INTO closet.look_items (
					workspace_id, look_id, slot, position, item_id, created_at)
				VALUES ($1, $2, $3, $4, $5, $6)`,
				workspaceID, lookID, string(entry.Slot), entry.Position,
				entry.ItemID, entry.CreatedAt); err != nil {
				return fmt.Errorf("closet: insert look item: %w", err)
			}
		}

		tag, err := conn.Exec(ctx, `
			UPDATE closet.looks SET updated_at = now()
			WHERE workspace_id = $1 AND id = $2`, workspaceID, lookID)
		if err != nil {
			return fmt.Errorf("closet: touch look: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return domain.NotFound("no look %s in this closet", lookID)
		}
		return nil
	})
}

/* ── helpers ─────────────────────────────────────────────────────────── */

// itoa names the placeholder index of a paginated query.
//
// It exists so the LIMIT and OFFSET indices are DERIVED from the argument
// slice rather than typed as `$7` and `$8`, which is what stops them going
// stale the day a filter is added above them.
func itoa(n int) string { return strconv.Itoa(n) }

// Compile-time proof that the four repositories satisfy their ports. A
// mismatch is a build error in this package rather than a confusing one at
// the wiring site.
var (
	_ ports.ItemRepo   = (*ItemRepo)(nil)
	_ ports.ImageRepo  = (*ImageRepo)(nil)
	_ ports.AssetStore = (*AssetRepo)(nil)
	_ ports.LookRepo   = (*LookRepo)(nil)
)
