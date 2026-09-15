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

type ArtifactRepo struct{ pool *pgxpool.Pool }

const artifactCols = `id, workspace_id, room_id, kind, title, body, status, sensitivity,
	created_at, updated_at`

func scanArtifact(row pgx.Row) (*domain.Artifact, error) {
	var a domain.Artifact
	if err := row.Scan(&a.ID, &a.WorkspaceID, &a.RoomID, &a.Kind, &a.Title, &a.Body,
		&a.Status, &a.Sensitivity, &a.CreatedAt, &a.UpdatedAt); err != nil {
		return nil, err
	}
	return &a, nil
}

// artifactWhere builds the predicate List and Count share, for the reason
// roomWhere gives.
func artifactWhere(workspaceID uuid.UUID, f ports.ArtifactFilter) (string, []any) {
	args := []any{workspaceID}
	clauses := []string{"workspace_id = $1", "deleted_at IS NULL"}

	bind := func(value any) int {
		args = append(args, value)
		return len(args)
	}

	if !f.IncludeHighlySensitive {
		clauses = append(clauses, fmt.Sprintf("sensitivity <> $%d",
			bind(string(domain.SensitivityHighlySensitive))))
	}
	if f.Kind != nil {
		clauses = append(clauses, fmt.Sprintf("kind = $%d", bind(string(*f.Kind))))
	}
	if f.Status != nil {
		clauses = append(clauses, fmt.Sprintf("status = $%d", bind(string(*f.Status))))
	}
	if f.RoomID != nil {
		clauses = append(clauses, fmt.Sprintf("room_id = $%d", bind(*f.RoomID)))
	}
	if s := strings.TrimSpace(f.Search); s != "" {
		n := bind(s)
		clauses = append(clauses, fmt.Sprintf(
			"(title ILIKE '%%' || $%d || '%%' OR body ILIKE '%%' || $%d || '%%')", n, n))
	}
	return strings.Join(clauses, " AND "), args
}

func (r *ArtifactRepo) List(ctx context.Context, workspaceID uuid.UUID, f ports.ArtifactFilter) ([]*domain.Artifact, error) {
	predicate, args := artifactWhere(workspaceID, f)

	limit := bound(f.Limit)
	args = append(args, limit)
	limitAt := len(args)
	args = append(args, f.Offset)
	offsetAt := len(args)

	rows, err := postgres.Conn(ctx, r.pool).Query(ctx, fmt.Sprintf(`
		SELECT %s
		FROM palace.artifacts
		WHERE %s
		ORDER BY updated_at DESC, id
		LIMIT $%d OFFSET $%d`,
		artifactCols, predicate, limitAt, offsetAt), args...)
	if err != nil {
		return nil, safeDBError("list artifacts", err)
	}
	defer rows.Close()

	out := make([]*domain.Artifact, 0, limit)
	for rows.Next() {
		a, err := scanArtifact(rows)
		if err != nil {
			return nil, safeDBError("scan artifact", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, safeDBError("list artifacts", err)
	}
	return out, nil
}

func (r *ArtifactRepo) Count(ctx context.Context, workspaceID uuid.UUID, f ports.ArtifactFilter) (int64, error) {
	predicate, args := artifactWhere(workspaceID, f)
	var n int64
	if err := postgres.Conn(ctx, r.pool).QueryRow(ctx, fmt.Sprintf(`
		SELECT count(*) FROM palace.artifacts WHERE %s`, predicate), args...).Scan(&n); err != nil {
		return 0, safeDBError("count artifacts", err)
	}
	return n, nil
}

func (r *ArtifactRepo) FindByID(ctx context.Context, workspaceID, id uuid.UUID) (*domain.Artifact, error) {
	a, err := scanArtifact(postgres.Conn(ctx, r.pool).QueryRow(ctx, `
		SELECT `+artifactCols+`
		FROM palace.artifacts
		WHERE workspace_id = $1 AND id = $2 AND deleted_at IS NULL`,
		workspaceID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.NotFound("artifact %s was not found", id)
	}
	if err != nil {
		return nil, safeDBError("read artifact", err)
	}
	return a, nil
}

// Exists answers the reference question without reading the row. See
// RoomRepo.Exists.
func (r *ArtifactRepo) Exists(ctx context.Context, workspaceID, id uuid.UUID) (bool, error) {
	var found bool
	err := postgres.Conn(ctx, r.pool).QueryRow(ctx, `
		SELECT TRUE FROM palace.artifacts
		WHERE workspace_id = $1 AND id = $2 AND deleted_at IS NULL`,
		workspaceID, id).Scan(&found)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, safeDBError("check artifact", err)
	}
	return found, nil
}

// KindOf reads one word instead of a whole artifact.
//
// The entry path needs the kind and nothing else, because
// ArtifactKind.AcceptsItems is the rule that decides whether an entry may
// be written. Selecting the body to answer that would put a document in a
// code path whose output is a boolean.
func (r *ArtifactRepo) KindOf(ctx context.Context, workspaceID, id uuid.UUID) (domain.ArtifactKind, error) {
	var kind domain.ArtifactKind
	err := postgres.Conn(ctx, r.pool).QueryRow(ctx, `
		SELECT kind FROM palace.artifacts
		WHERE workspace_id = $1 AND id = $2 AND deleted_at IS NULL`,
		workspaceID, id).Scan(&kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", domain.NotFound("artifact %s was not found", id)
	}
	if err != nil {
		return "", safeDBError("read artifact kind", err)
	}
	return kind, nil
}

// StatusOf reads one word instead of a whole artifact. See
// RoomRepo.StatusOf.
func (r *ArtifactRepo) StatusOf(ctx context.Context, workspaceID, id uuid.UUID) (domain.Lifecycle, error) {
	var status domain.Lifecycle
	err := postgres.Conn(ctx, r.pool).QueryRow(ctx, `
		SELECT status FROM palace.artifacts
		WHERE workspace_id = $1 AND id = $2 AND deleted_at IS NULL`,
		workspaceID, id).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", domain.NotFound("artifact %s was not found", id)
	}
	if err != nil {
		return "", safeDBError("read artifact status", err)
	}
	return status, nil
}

func (r *ArtifactRepo) Create(ctx context.Context, workspaceID uuid.UUID, a *domain.Artifact) error {
	if err := assertWorkspace("insert artifact", workspaceID, a.WorkspaceID); err != nil {
		return err
	}
	if err := postgres.Conn(ctx, r.pool).QueryRow(ctx, `
		INSERT INTO palace.artifacts (workspace_id, room_id, kind, title, body, status, sensitivity)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, created_at, updated_at`,
		workspaceID, a.RoomID, string(a.Kind), a.Title, a.Body,
		string(a.Status), string(a.Sensitivity),
	).Scan(&a.ID, &a.CreatedAt, &a.UpdatedAt); err != nil {
		return safeDBError("insert artifact", err)
	}
	a.WorkspaceID = workspaceID
	return nil
}

// Update writes the mutable fields and stamps updated_at.
//
// `kind` is absent from the SET clause, and that is the enforcement
// rather than a convention: the column cannot be moved through this
// repository at all, so an ArtifactChange that grew a Kind field by
// accident would change nothing until somebody also edited this
// statement. See ArtifactChange on why re-filing is create plus archive.
func (r *ArtifactRepo) Update(ctx context.Context, workspaceID uuid.UUID, a *domain.Artifact) error {
	if err := assertWorkspace("update artifact", workspaceID, a.WorkspaceID); err != nil {
		return err
	}
	err := postgres.Conn(ctx, r.pool).QueryRow(ctx, `
		UPDATE palace.artifacts
		SET room_id = $3, title = $4, body = $5, status = $6, sensitivity = $7,
		    updated_at = now()
		WHERE workspace_id = $1 AND id = $2 AND deleted_at IS NULL
		RETURNING updated_at`,
		workspaceID, a.ID, a.RoomID, a.Title, a.Body,
		string(a.Status), string(a.Sensitivity)).Scan(&a.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.NotFound("artifact %s was not found", a.ID)
	}
	if err != nil {
		return safeDBError("update artifact", err)
	}
	return nil
}
