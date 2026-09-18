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

// artifactAlias is the name the artifact table carries in every statement
// built from artifactWhere.
//
// Correlated subqueries reach into the outer row, and `palace.rooms`
// carries `workspace_id` and `id` too. An unqualified reference would
// resolve to whichever scope Postgres finds first, which is a bug that
// compiles and returns plausible rows. See visibility.go.
const artifactAlias = "a"

// artifactWhere builds the predicate List, Count and CountByRoom share,
// for the reason roomWhere gives.
//
// ── The clause order is the order of the questions ─────────────────────
// Ownership, liveness and the row's own level first, because they are
// about the row itself and are the cheapest to answer. Containment last,
// because it is the only one that reaches another table. Postgres will
// reorder as it likes; the order here is for the reader.
func artifactWhere(workspaceID uuid.UUID, f ports.ArtifactFilter) (string, []any) {
	const a = artifactAlias
	args := []any{workspaceID}
	clauses := []string{a + ".workspace_id = $1", a + ".deleted_at IS NULL"}

	bind := func(value any) int {
		args = append(args, value)
		return len(args)
	}

	var c containment
	if !f.IncludeHighlySensitive {
		c.levelBind = fmt.Sprintf("$%d", bind(withheldLevel()))
		clauses = append(clauses, a+".sensitivity <> "+c.levelBind)
	}
	if f.Kind != nil {
		clauses = append(clauses, fmt.Sprintf(a+".kind = $%d", bind(string(*f.Kind))))
	}
	if f.Status != nil {
		clauses = append(clauses, fmt.Sprintf(a+".status = $%d", bind(string(*f.Status))))
	}
	switch {
	case f.RoomID != nil:
		clauses = append(clauses, fmt.Sprintf(a+".room_id = $%d", bind(*f.RoomID)))
	case f.Unfiled:
		// The rows filed nowhere. Not expressible as a RoomID, because a
		// nil RoomID already means "every room": see ports.ArtifactFilter.
		clauses = append(clauses, a+".room_id IS NULL")
	}
	if s := strings.TrimSpace(f.Search); s != "" {
		n := bind(s)
		// The search is ANDed with everything above, which is what makes
		// "first eligibility, then the text" true of the result even though
		// one statement decides both. A term that appears only inside a
		// withheld row matches nothing and counts nothing.
		clauses = append(clauses, fmt.Sprintf(
			"("+a+".title ILIKE '%%' || $%d || '%%' OR "+a+".body ILIKE '%%' || $%d || '%%')", n, n))
	}
	if f.InheritRoomVisibility {
		clauses = append(clauses, c.artifactContainment(a))
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
		SELECT %[1]s
		FROM palace.artifacts %[2]s
		WHERE %[3]s
		ORDER BY %[2]s.updated_at DESC, %[2]s.id
		LIMIT $%[4]d OFFSET $%[5]d`,
		artifactCols, artifactAlias, predicate, limitAt, offsetAt), args...)
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

// Count is List's predicate, applied to a count.
//
// It shares `artifactWhere` with List rather than restating it, and that
// sharing is the mechanism rather than a convenience: a count built from
// its own copy of the rules is a count that disagrees with the listing the
// first time one of them is edited, and the disagreement is what
// publishes the existence of a withheld row.
func (r *ArtifactRepo) Count(ctx context.Context, workspaceID uuid.UUID, f ports.ArtifactFilter) (int64, error) {
	predicate, args := artifactWhere(workspaceID, f)
	var n int64
	if err := postgres.Conn(ctx, r.pool).QueryRow(ctx, fmt.Sprintf(`
		SELECT count(*) FROM palace.artifacts %s WHERE %s`,
		artifactAlias, predicate), args...).Scan(&n); err != nil {
		return 0, safeDBError("count artifacts", err)
	}
	return n, nil
}

// CountByRoom is Count, grouped, in one statement.
//
// ── Why the room filters are cleared rather than honoured ──────────────
// Grouping by a column while filtering on it answers a question nobody
// asked: at most one group would come back, and a caller summarising
// every room would get one room. The filter's other fields all apply,
// including the visibility rules, which is the entire reason this is a
// repository method and not a loop over Count in the layer above.
func (r *ArtifactRepo) CountByRoom(ctx context.Context, workspaceID uuid.UUID, f ports.ArtifactFilter) ([]ports.RoomCount, error) {
	f.RoomID, f.Unfiled = nil, false
	f.Limit, f.Offset = 0, 0
	predicate, args := artifactWhere(workspaceID, f)

	rows, err := postgres.Conn(ctx, r.pool).Query(ctx, fmt.Sprintf(`
		SELECT %[1]s.room_id, count(*)
		FROM palace.artifacts %[1]s
		WHERE %[2]s
		GROUP BY %[1]s.room_id`, artifactAlias, predicate), args...)
	if err != nil {
		return nil, safeDBError("count artifacts by room", err)
	}
	defer rows.Close()

	return scanRoomCounts(rows, "count artifacts by room")
}

// ListByIDs resolves a known set of ids under a surface's rules.
//
// ── Why it builds a filter instead of writing its own predicate ────────
// Because the rule it has to apply is the listing's rule, exactly, and a
// hand-written WHERE here would be a third copy free to drift from the
// two that already agree. What it adds is one clause: the id set. No
// status filter, so an archived neighbour comes back and is reported as
// archived rather than silently dropped.
//
// No LIMIT, deliberately: the id set is the bound. See the port.
func (r *ArtifactRepo) ListByIDs(ctx context.Context, workspaceID uuid.UUID, ids []uuid.UUID, v ports.Visibility) ([]*domain.Artifact, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	predicate, args := artifactWhere(workspaceID, ports.ArtifactFilter{
		Sensitive:             ports.Sensitive{IncludeHighlySensitive: v.IncludeHighlySensitive},
		InheritRoomVisibility: v.InheritRoomVisibility,
	})
	args = append(args, ids)
	idsAt := len(args)

	rows, err := postgres.Conn(ctx, r.pool).Query(ctx, fmt.Sprintf(`
		SELECT %[1]s
		FROM palace.artifacts %[2]s
		WHERE %[3]s AND %[2]s.id = ANY($%[4]d)
		ORDER BY %[2]s.updated_at DESC, %[2]s.id`,
		artifactCols, artifactAlias, predicate, idsAt), args...)
	if err != nil {
		return nil, safeDBError("resolve artifacts", err)
	}
	defer rows.Close()

	out := make([]*domain.Artifact, 0, len(ids))
	for rows.Next() {
		a, err := scanArtifact(rows)
		if err != nil {
			return nil, safeDBError("scan artifact", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, safeDBError("resolve artifacts", err)
	}
	return out, nil
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
