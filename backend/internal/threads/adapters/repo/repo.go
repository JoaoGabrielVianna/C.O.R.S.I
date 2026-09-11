// Package repo is the Postgres implementation of the Threads storage port.
//
// Every query is workspace-first, for the reason the ports package states:
// an id that arrives from a model must not be able to reach a row it does
// not own, and the predicate that guarantees it belongs in the SQL rather
// than in a check somebody has to remember to write.
package repo

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/corsi/backend/internal/threads/domain"
	"github.com/corsi/backend/internal/threads/ports"
)

// Repos bundles the implementations so the module wires one value. One
// today; the shape is the module's, not a prediction.
type Repos struct {
	Threads ports.ThreadRepo
}

func New(pool *pgxpool.Pool) *Repos {
	return &Repos{Threads: &ThreadRepo{pool: pool}}
}

// defaultLimit bounds a listing that did not ask for one.
//
// Twenty-five rather than Job Radar's fifty, because a thread carries text:
// every row a `list` returns becomes prompt tokens on the next provider
// call of the same turn, and the excerpt that makes two drafts
// distinguishable is also what makes a row expensive.
const defaultLimit = 25

// maxLimit is the ceiling a caller cannot argue past.
const maxLimit = 100

type ThreadRepo struct{ pool *pgxpool.Pool }

const threadCols = `id, workspace_id, title, content, status, created_at, updated_at`

func scanThread(row pgx.Row) (*domain.Thread, error) {
	var t domain.Thread
	if err := row.Scan(&t.ID, &t.WorkspaceID, &t.Title, &t.Content,
		&t.Status, &t.CreatedAt, &t.UpdatedAt); err != nil {
		return nil, err
	}
	return &t, nil
}

// where builds the shared predicate for List and Count, so a listing and
// the total it reports can never be answering two different questions.
func where(workspaceID uuid.UUID, f ports.ThreadFilter) (string, []any) {
	args := []any{workspaceID}
	clauses := []string{"workspace_id = $1", "deleted_at IS NULL"}

	bind := func(value any) int {
		args = append(args, value)
		return len(args)
	}

	if f.Status != nil {
		clauses = append(clauses, fmt.Sprintf("status = $%d", bind(string(*f.Status))))
	}
	if s := strings.TrimSpace(f.Search); s != "" {
		n := bind(s)
		// Title and content both, because a person searching for "micro-
		// services" may be remembering the handle or a sentence inside the
		// draft, and a search that only looked at one would fail on a
		// correct question.
		clauses = append(clauses, fmt.Sprintf(
			"(title ILIKE '%%' || $%d || '%%' OR content ILIKE '%%' || $%d || '%%')", n, n))
	}
	return strings.Join(clauses, " AND "), args
}

func (r *ThreadRepo) List(ctx context.Context, workspaceID uuid.UUID, f ports.ThreadFilter) ([]*domain.Thread, error) {
	predicate, args := where(workspaceID, f)

	limit := f.Limit
	if limit <= 0 {
		limit = defaultLimit
	}
	if limit > maxLimit {
		limit = maxLimit
	}
	args = append(args, limit)
	limitPlaceholder := len(args)
	args = append(args, f.Offset)
	offsetPlaceholder := len(args)

	// updated_at DESC: content work is returned to, so the thread touched an
	// hour ago is the one being asked about. `id` breaks the tie so two
	// identical reads return an identical order.
	rows, err := r.pool.Query(ctx, fmt.Sprintf(`
		SELECT %s
		FROM threads.threads
		WHERE %s
		ORDER BY updated_at DESC, id
		LIMIT $%d OFFSET $%d`,
		threadCols, predicate, limitPlaceholder, offsetPlaceholder), args...)
	if err != nil {
		return nil, fmt.Errorf("threads: list threads: %w", err)
	}
	defer rows.Close()

	out := make([]*domain.Thread, 0, limit)
	for rows.Next() {
		t, err := scanThread(rows)
		if err != nil {
			return nil, fmt.Errorf("threads: scan thread: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (r *ThreadRepo) Count(ctx context.Context, workspaceID uuid.UUID, f ports.ThreadFilter) (int64, error) {
	predicate, args := where(workspaceID, f)
	var n int64
	if err := r.pool.QueryRow(ctx, fmt.Sprintf(`
		SELECT count(*) FROM threads.threads WHERE %s`, predicate), args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("threads: count threads: %w", err)
	}
	return n, nil
}

func (r *ThreadRepo) FindByID(ctx context.Context, workspaceID, id uuid.UUID) (*domain.Thread, error) {
	t, err := scanThread(r.pool.QueryRow(ctx, `
		SELECT `+threadCols+`
		FROM threads.threads
		WHERE workspace_id = $1 AND id = $2 AND deleted_at IS NULL`,
		workspaceID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		// Deliberately the same answer as "belongs to another workspace".
		// See domain.KindNotFound.
		return nil, domain.NotFound("thread %s was not found", id)
	}
	if err != nil {
		return nil, fmt.Errorf("threads: read thread: %w", err)
	}
	return t, nil
}

func (r *ThreadRepo) Create(ctx context.Context, t *domain.Thread) error {
	if err := r.pool.QueryRow(ctx, `
		INSERT INTO threads.threads (workspace_id, title, content, status)
		VALUES ($1, $2, $3, $4)
		RETURNING id, created_at, updated_at`,
		t.WorkspaceID, t.Title, t.Content, string(t.Status),
	).Scan(&t.ID, &t.CreatedAt, &t.UpdatedAt); err != nil {
		return fmt.Errorf("threads: insert thread: %w", err)
	}
	return nil
}

// Update writes the three mutable fields and stamps updated_at.
//
// ── Why updated_at is stamped in SQL and read back ─────────────────────
// Because it is what orders every listing and what hydration reports as
// "when this last moved", and a value the application layer computed could
// differ from the transaction's own clock. Reading it back means the entity
// in memory and the row on disk agree about when the edit happened.
func (r *ThreadRepo) Update(ctx context.Context, t *domain.Thread) error {
	err := r.pool.QueryRow(ctx, `
		UPDATE threads.threads
		SET title = $3, content = $4, status = $5, updated_at = now()
		WHERE workspace_id = $1 AND id = $2 AND deleted_at IS NULL
		RETURNING updated_at`,
		t.WorkspaceID, t.ID, t.Title, t.Content, string(t.Status)).Scan(&t.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.NotFound("thread %s was not found", t.ID)
	}
	if err != nil {
		return fmt.Errorf("threads: update thread: %w", err)
	}
	return nil
}

// SoftDelete stops a thread appearing anywhere without destroying it.
//
// `deleted_at IS NULL` in the predicate makes deleting twice a not-found
// rather than a silent success, which is what lets a caller tell "I removed
// it" from "it was already gone".
func (r *ThreadRepo) SoftDelete(ctx context.Context, workspaceID, id uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE threads.threads SET deleted_at = now()
		WHERE workspace_id = $1 AND id = $2 AND deleted_at IS NULL`, workspaceID, id)
	if err != nil {
		return fmt.Errorf("threads: delete thread: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.NotFound("thread %s was not found", id)
	}
	return nil
}
