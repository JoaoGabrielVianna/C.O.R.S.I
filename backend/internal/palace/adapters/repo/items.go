package repo

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/corsi/backend/internal/palace/domain"
	"github.com/corsi/backend/internal/palace/ports"
	"github.com/corsi/backend/internal/platform/postgres"
)

// ══════════════════════════════════════════════════════════════════════
//
//	EVERY STATEMENT HERE NAMES THE WORKSPACE AND THE ARTIFACT
//
// ══════════════════════════════════════════════════════════════════════
//
// The primary key alone would find any row. That is exactly why it is not
// enough: an entry id is a plain uuid that a caller can hold from one
// checklist and send against another, and `WHERE id = $1` would tick the
// wrong box, in the wrong list, possibly in a stranger's palace, and
// report success.
//
// So the predicate is `workspace_id = $1 AND artifact_id = $2 AND
// id = $3`, on every read and every write. An entry from the wrong
// artifact and an entry that never existed are then the same answer,
// which is the same answer every other absence in this context gives.

type ItemRepo struct{ pool *pgxpool.Pool }

const itemCols = `id, workspace_id, artifact_id, position, text, done, created_at, updated_at`

func scanItem(row pgx.Row) (*domain.ArtifactItem, error) {
	var i domain.ArtifactItem
	if err := row.Scan(&i.ID, &i.WorkspaceID, &i.ArtifactID, &i.Position,
		&i.Text, &i.Done, &i.CreatedAt, &i.UpdatedAt); err != nil {
		return nil, err
	}
	return &i, nil
}

func (r *ItemRepo) ListByArtifact(ctx context.Context, workspaceID, artifactID uuid.UUID, p ports.Page) ([]*domain.ArtifactItem, error) {
	limit := bound(p.Limit)

	// position ASC, then id: a checklist is read in the order it was
	// arranged, and `position` is deliberately not unique (reordering
	// would otherwise have to dodge itself halfway through), so the tie
	// break is what makes two identical reads return an identical order.
	rows, err := postgres.Conn(ctx, r.pool).Query(ctx, `
		SELECT `+itemCols+`
		FROM palace.artifact_items
		WHERE workspace_id = $1 AND artifact_id = $2 AND deleted_at IS NULL
		ORDER BY position, id
		LIMIT $3 OFFSET $4`,
		workspaceID, artifactID, limit, p.Offset)
	if err != nil {
		return nil, safeDBError("list artifact items", err)
	}
	defer rows.Close()

	out := make([]*domain.ArtifactItem, 0, limit)
	for rows.Next() {
		item, err := scanItem(rows)
		if err != nil {
			return nil, safeDBError("scan artifact item", err)
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, safeDBError("list artifact items", err)
	}
	return out, nil
}

func (r *ItemRepo) CountByArtifact(ctx context.Context, workspaceID, artifactID uuid.UUID) (int64, error) {
	var n int64
	if err := postgres.Conn(ctx, r.pool).QueryRow(ctx, `
		SELECT count(*) FROM palace.artifact_items
		WHERE workspace_id = $1 AND artifact_id = $2 AND deleted_at IS NULL`,
		workspaceID, artifactID).Scan(&n); err != nil {
		return 0, safeDBError("count artifact items", err)
	}
	return n, nil
}

func (r *ItemRepo) FindByID(ctx context.Context, workspaceID, artifactID, itemID uuid.UUID) (*domain.ArtifactItem, error) {
	item, err := scanItem(postgres.Conn(ctx, r.pool).QueryRow(ctx, `
		SELECT `+itemCols+`
		FROM palace.artifact_items
		WHERE workspace_id = $1 AND artifact_id = $2 AND id = $3 AND deleted_at IS NULL`,
		workspaceID, artifactID, itemID))
	if errors.Is(err, pgx.ErrNoRows) {
		// One answer for four absences: never existed, removed, another
		// artifact's, another workspace's.
		return nil, domain.NotFound("item %s was not found", itemID)
	}
	if err != nil {
		return nil, safeDBError("read artifact item", err)
	}
	return item, nil
}

// Create writes a new entry, appending when no slot was requested.
//
// ── Why the append is a subquery and not a read then a write ───────────
// Because reading the current maximum and then inserting it leaves a gap
// in which another caller reads the same maximum. Resolved inside the
// statement, the two are one operation. Positions are not unique by
// design, so a collision under concurrency costs an arbitrary but stable
// order between two entries rather than an error, which is the right
// trade for a checklist.
func (r *ItemRepo) Create(ctx context.Context, workspaceID, artifactID uuid.UUID, item *domain.ArtifactItem, position *int) error {
	if err := assertWorkspace("insert artifact item", workspaceID, item.WorkspaceID); err != nil {
		return err
	}
	if item.ArtifactID != uuid.Nil && item.ArtifactID != artifactID {
		return fmt.Errorf("palace: insert artifact item: the entry belongs to a different artifact than the call")
	}
	if err := postgres.Conn(ctx, r.pool).QueryRow(ctx, `
		INSERT INTO palace.artifact_items (workspace_id, artifact_id, position, text, done)
		VALUES ($1, $2,
		        COALESCE($3::int, (SELECT COALESCE(MAX(position) + 1, 0)
		                           FROM palace.artifact_items
		                           WHERE workspace_id = $1 AND artifact_id = $2
		                             AND deleted_at IS NULL)),
		        $4, $5)
		RETURNING id, position, created_at, updated_at`,
		workspaceID, artifactID, position, item.Text, item.Done,
	).Scan(&item.ID, &item.Position, &item.CreatedAt, &item.UpdatedAt); err != nil {
		return safeDBError("insert artifact item", err)
	}
	item.WorkspaceID = workspaceID
	item.ArtifactID = artifactID
	return nil
}

func (r *ItemRepo) Update(ctx context.Context, workspaceID, artifactID uuid.UUID, item *domain.ArtifactItem) error {
	if err := assertWorkspace("update artifact item", workspaceID, item.WorkspaceID); err != nil {
		return err
	}
	if item.ArtifactID != artifactID {
		return fmt.Errorf("palace: update artifact item: the entry belongs to a different artifact than the call")
	}
	err := postgres.Conn(ctx, r.pool).QueryRow(ctx, `
		UPDATE palace.artifact_items
		SET position = $4, text = $5, done = $6, updated_at = now()
		WHERE workspace_id = $1 AND artifact_id = $2 AND id = $3 AND deleted_at IS NULL
		RETURNING updated_at`,
		workspaceID, artifactID, item.ID, item.Position, item.Text, item.Done).Scan(&item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.NotFound("item %s was not found", item.ID)
	}
	if err != nil {
		return safeDBError("update artifact item", err)
	}
	return nil
}

// SoftDelete takes an entry off the list without destroying it.
//
// `deleted_at IS NULL` in the predicate makes removing twice a not-found
// rather than a silent success, which is what lets a caller tell "I took
// it off" from "it was already gone".
func (r *ItemRepo) SoftDelete(ctx context.Context, workspaceID, artifactID, itemID uuid.UUID) error {
	tag, err := postgres.Conn(ctx, r.pool).Exec(ctx, `
		UPDATE palace.artifact_items SET deleted_at = now()
		WHERE workspace_id = $1 AND artifact_id = $2 AND id = $3 AND deleted_at IS NULL`,
		workspaceID, artifactID, itemID)
	if err != nil {
		return safeDBError("remove artifact item", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.NotFound("item %s was not found", itemID)
	}
	return nil
}
