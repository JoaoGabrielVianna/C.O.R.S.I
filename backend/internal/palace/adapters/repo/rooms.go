package repo

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/corsi/backend/internal/palace/domain"
	"github.com/corsi/backend/internal/palace/ports"
	"github.com/corsi/backend/internal/platform/postgres"
)

type RoomRepo struct{ pool *pgxpool.Pool }

const roomCols = `id, workspace_id, name, description, status, sensitivity, created_at, updated_at`

func scanRoom(row pgx.Row) (*domain.Room, error) {
	var r domain.Room
	if err := row.Scan(&r.ID, &r.WorkspaceID, &r.Name, &r.Description,
		&r.Status, &r.Sensitivity, &r.CreatedAt, &r.UpdatedAt); err != nil {
		return nil, err
	}
	return &r, nil
}

// roomWhere builds the predicate List and Count share.
//
// ── Why they share it, rather than each writing its own ────────────────
// So a listing and the total it reports cannot be answering two different
// questions. That matters most for the sensitivity rule: a Count that
// forgot it would report "23 salas" above a list of 22, which announces
// the existence of the very row being withheld.
func roomWhere(workspaceID uuid.UUID, f ports.RoomFilter) (string, []any) {
	args := []any{workspaceID}
	clauses := []string{"workspace_id = $1", "deleted_at IS NULL"}

	bind := func(value any) int {
		args = append(args, value)
		return len(args)
	}

	if !f.IncludeHighlySensitive {
		// The default. Named after the level it withholds, and applied as
		// SQL rather than as a filter over the result: a row that is never
		// selected is one that cannot be logged, counted or accidentally
		// serialised on its way past.
		clauses = append(clauses, fmt.Sprintf("sensitivity <> $%d",
			bind(string(domain.SensitivityHighlySensitive))))
	}
	if f.Status != nil {
		clauses = append(clauses, fmt.Sprintf("status = $%d", bind(string(*f.Status))))
	}
	if s := strings.TrimSpace(f.Search); s != "" {
		n := bind(s)
		clauses = append(clauses, fmt.Sprintf(
			"(name ILIKE '%%' || $%d || '%%' OR description ILIKE '%%' || $%d || '%%')", n, n))
	}
	return strings.Join(clauses, " AND "), args
}

func (r *RoomRepo) List(ctx context.Context, workspaceID uuid.UUID, f ports.RoomFilter) ([]*domain.Room, error) {
	predicate, args := roomWhere(workspaceID, f)

	limit := bound(f.Limit)
	args = append(args, limit)
	limitAt := len(args)
	args = append(args, f.Offset)
	offsetAt := len(args)

	// updated_at DESC: a room is returned to, so the one touched an hour
	// ago is the one being asked about. `id` breaks the tie so two
	// identical reads return an identical order.
	rows, err := postgres.Conn(ctx, r.pool).Query(ctx, fmt.Sprintf(`
		SELECT %s
		FROM palace.rooms
		WHERE %s
		ORDER BY updated_at DESC, id
		LIMIT $%d OFFSET $%d`,
		roomCols, predicate, limitAt, offsetAt), args...)
	if err != nil {
		return nil, safeDBError("list rooms", err)
	}
	defer rows.Close()

	out := make([]*domain.Room, 0, limit)
	for rows.Next() {
		room, err := scanRoom(rows)
		if err != nil {
			return nil, safeDBError("scan room", err)
		}
		out = append(out, room)
	}
	if err := rows.Err(); err != nil {
		return nil, safeDBError("list rooms", err)
	}
	return out, nil
}

func (r *RoomRepo) Count(ctx context.Context, workspaceID uuid.UUID, f ports.RoomFilter) (int64, error) {
	predicate, args := roomWhere(workspaceID, f)
	var n int64
	if err := postgres.Conn(ctx, r.pool).QueryRow(ctx, fmt.Sprintf(`
		SELECT count(*) FROM palace.rooms WHERE %s`, predicate), args...).Scan(&n); err != nil {
		return 0, safeDBError("count rooms", err)
	}
	return n, nil
}

// FindByID reads one live room of this workspace.
//
// The workspace is in the lookup, not in a check afterwards, and a miss
// is `not_found` whether the row never existed, was deleted, or belongs
// to somebody else. See domain.KindNotFound.
//
// Note what is NOT here: a sensitivity condition. A caller holding an id
// already knows the row exists, and withholding it would be a puzzle
// rather than a protection. The rule is about broad listings.
func (r *RoomRepo) FindByID(ctx context.Context, workspaceID, id uuid.UUID) (*domain.Room, error) {
	room, err := scanRoom(postgres.Conn(ctx, r.pool).QueryRow(ctx, `
		SELECT `+roomCols+`
		FROM palace.rooms
		WHERE workspace_id = $1 AND id = $2 AND deleted_at IS NULL`,
		workspaceID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.NotFound("room %s was not found", id)
	}
	if err != nil {
		return nil, safeDBError("read room", err)
	}
	return room, nil
}

// Exists answers the reference question without reading the row.
//
// ── Why not FindByID with the result discarded ─────────────────────────
// Because the caller wants one bit. Loading the room would pull its name
// and description into a code path whose only job is to decide whether a
// memory may point at it, and a row in hand is a row that can be logged
// by accident. The cheapest safe read is the one that never selects the
// content.
func (r *RoomRepo) Exists(ctx context.Context, workspaceID, id uuid.UUID) (bool, error) {
	var found bool
	err := postgres.Conn(ctx, r.pool).QueryRow(ctx, `
		SELECT TRUE FROM palace.rooms
		WHERE workspace_id = $1 AND id = $2 AND deleted_at IS NULL`,
		workspaceID, id).Scan(&found)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, safeDBError("check room", err)
	}
	return found, nil
}

// StatusOf reads one word instead of a whole room.
//
// The relation path needs to know whether an endpoint is live, and
// nothing else. Selecting the description to answer that would put a
// paragraph in a code path whose output is a comparison. Same argument as
// ArtifactRepo.KindOf.
func (r *RoomRepo) StatusOf(ctx context.Context, workspaceID, id uuid.UUID) (domain.Lifecycle, error) {
	var status domain.Lifecycle
	err := postgres.Conn(ctx, r.pool).QueryRow(ctx, `
		SELECT status FROM palace.rooms
		WHERE workspace_id = $1 AND id = $2 AND deleted_at IS NULL`,
		workspaceID, id).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", domain.NotFound("room %s was not found", id)
	}
	if err != nil {
		return "", safeDBError("read room status", err)
	}
	return status, nil
}

func (r *RoomRepo) Create(ctx context.Context, workspaceID uuid.UUID, room *domain.Room) error {
	if err := assertWorkspace("insert room", workspaceID, room.WorkspaceID); err != nil {
		return err
	}
	// The workspace written is the ARGUMENT, never the entity's field.
	// They were just proven equal, and binding the argument keeps the
	// authority in one place even if that stops being true.
	if err := postgres.Conn(ctx, r.pool).QueryRow(ctx, `
		INSERT INTO palace.rooms (workspace_id, name, description, status, sensitivity)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, created_at, updated_at`,
		workspaceID, room.Name, room.Description,
		string(room.Status), string(room.Sensitivity),
	).Scan(&room.ID, &room.CreatedAt, &room.UpdatedAt); err != nil {
		return safeDBError("insert room", err)
	}
	room.WorkspaceID = workspaceID
	return nil
}

// Update writes the mutable fields and stamps updated_at.
//
// ── Why updated_at is stamped in SQL and read back ─────────────────────
// Because it orders every listing, and a value computed up in the
// application layer could differ from the transaction's own clock.
// Reading it back means the entity in memory and the row on disk agree
// about when the edit happened.
func (r *RoomRepo) Update(ctx context.Context, workspaceID uuid.UUID, room *domain.Room) error {
	if err := assertWorkspace("update room", workspaceID, room.WorkspaceID); err != nil {
		return err
	}
	err := postgres.Conn(ctx, r.pool).QueryRow(ctx, `
		UPDATE palace.rooms
		SET name = $3, description = $4, status = $5, sensitivity = $6, updated_at = now()
		WHERE workspace_id = $1 AND id = $2 AND deleted_at IS NULL
		RETURNING updated_at`,
		workspaceID, room.ID, room.Name, room.Description,
		string(room.Status), string(room.Sensitivity)).Scan(&room.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		// The UPDATE matched nothing. Reported as not-found rather than as
		// success, which is what distinguishes "I changed it" from an
		// UPDATE that silently affected zero rows.
		return domain.NotFound("room %s was not found", room.ID)
	}
	if err != nil {
		return safeDBError("update room", err)
	}
	return nil
}
