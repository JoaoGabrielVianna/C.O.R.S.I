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

type MemoryRepo struct{ pool *pgxpool.Pool }

const memoryCols = `id, workspace_id, kind, content, summary, importance, confidence,
	occurred_at, room_id, artifact_id, status, sensitivity, created_at, updated_at`

// scanMemory reads one row.
//
// `artifact_id` is scanned even though no application use case sets one
// in this slice: the column exists, the domain field exists, and a
// repository that ignored it would quietly drop the value the moment
// Artifact arrives. Reading a column costs nothing; discovering later
// that reads have been lossy costs a lot.
func scanMemory(row pgx.Row) (*domain.Memory, error) {
	var m domain.Memory
	if err := row.Scan(&m.ID, &m.WorkspaceID, &m.Kind, &m.Content, &m.Summary,
		&m.Importance, &m.Confidence, &m.OccurredAt, &m.RoomID, &m.ArtifactID,
		&m.Status, &m.Sensitivity, &m.CreatedAt, &m.UpdatedAt); err != nil {
		return nil, err
	}
	return &m, nil
}

// memoryAlias is the name the memory table carries in every statement
// built from memoryWhere. See artifactAlias, and visibility.go, which
// states why nothing in these predicates is left unqualified.
const memoryAlias = "m"

// memoryWhere builds the predicate List, Count and CountByRoom share, for
// the reason roomWhere gives.
func memoryWhere(workspaceID uuid.UUID, f ports.MemoryFilter) (string, []any) {
	const m = memoryAlias
	args := []any{workspaceID}
	clauses := []string{m + ".workspace_id = $1", m + ".deleted_at IS NULL"}

	bind := func(value any) int {
		args = append(args, value)
		return len(args)
	}

	var c containment
	if !f.IncludeHighlySensitive {
		c.levelBind = fmt.Sprintf("$%d", bind(withheldLevel()))
		clauses = append(clauses, m+".sensitivity <> "+c.levelBind)
	}
	if f.Kind != nil {
		clauses = append(clauses, fmt.Sprintf(m+".kind = $%d", bind(string(*f.Kind))))
	}
	if f.Status != nil {
		clauses = append(clauses, fmt.Sprintf(m+".status = $%d", bind(string(*f.Status))))
	}
	switch {
	case f.RoomID != nil:
		clauses = append(clauses, fmt.Sprintf(m+".room_id = $%d", bind(*f.RoomID)))
	case f.Unfiled:
		clauses = append(clauses, m+".room_id IS NULL")
	}
	if f.ArtifactID != nil {
		clauses = append(clauses, fmt.Sprintf(m+".artifact_id = $%d", bind(*f.ArtifactID)))
	}
	if f.MinImportance > 0 {
		clauses = append(clauses, fmt.Sprintf(m+".importance >= $%d", bind(f.MinImportance)))
	}
	if s := strings.TrimSpace(f.Search); s != "" {
		n := bind(s)
		// Content and summary both, because a person searching for
		// "reunião" may be remembering the sentence they wrote or the line
		// they wrote about it, and a search that only looked at one would
		// fail on a correct question.
		clauses = append(clauses, fmt.Sprintf(
			"("+m+".content ILIKE '%%' || $%d || '%%' OR "+m+".summary ILIKE '%%' || $%d || '%%')", n, n))
	}
	if f.InheritRoomVisibility {
		// D1 on the memory's own room, and D1.1 on the artifact it is
		// about. Both in one clause, because a surface that applied one hop
		// and not the other would be a surface with a hole in it. See
		// visibility.go.
		clauses = append(clauses, c.memoryContainment(m))
	}
	return strings.Join(clauses, " AND "), args
}

func (r *MemoryRepo) List(ctx context.Context, workspaceID uuid.UUID, f ports.MemoryFilter) ([]*domain.Memory, error) {
	predicate, args := memoryWhere(workspaceID, f)

	limit := bound(f.Limit)
	args = append(args, limit)
	limitAt := len(args)
	args = append(args, f.Offset)
	offsetAt := len(args)

	// updated_at DESC and not importance: see ports.MemoryFilter on why
	// importance is a floor rather than an ordering.
	rows, err := postgres.Conn(ctx, r.pool).Query(ctx, fmt.Sprintf(`
		SELECT %[1]s
		FROM palace.memories %[2]s
		WHERE %[3]s
		ORDER BY %[2]s.updated_at DESC, %[2]s.id
		LIMIT $%[4]d OFFSET $%[5]d`,
		memoryCols, memoryAlias, predicate, limitAt, offsetAt), args...)
	if err != nil {
		return nil, safeDBError("list memories", err)
	}
	defer rows.Close()

	out := make([]*domain.Memory, 0, limit)
	for rows.Next() {
		m, err := scanMemory(rows)
		if err != nil {
			return nil, safeDBError("scan memory", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, safeDBError("list memories", err)
	}
	return out, nil
}

// Count is List's predicate, applied to a count. See ArtifactRepo.Count,
// which states why the sharing is the mechanism and not a convenience.
func (r *MemoryRepo) Count(ctx context.Context, workspaceID uuid.UUID, f ports.MemoryFilter) (int64, error) {
	predicate, args := memoryWhere(workspaceID, f)
	var n int64
	if err := postgres.Conn(ctx, r.pool).QueryRow(ctx, fmt.Sprintf(`
		SELECT count(*) FROM palace.memories %s WHERE %s`,
		memoryAlias, predicate), args...).Scan(&n); err != nil {
		return 0, safeDBError("count memories", err)
	}
	return n, nil
}

// CountByRoom is Count, grouped. See ArtifactRepo.CountByRoom.
func (r *MemoryRepo) CountByRoom(ctx context.Context, workspaceID uuid.UUID, f ports.MemoryFilter) ([]ports.RoomCount, error) {
	f.RoomID, f.Unfiled = nil, false
	f.Limit, f.Offset = 0, 0
	predicate, args := memoryWhere(workspaceID, f)

	rows, err := postgres.Conn(ctx, r.pool).Query(ctx, fmt.Sprintf(`
		SELECT %[1]s.room_id, count(*)
		FROM palace.memories %[1]s
		WHERE %[2]s
		GROUP BY %[1]s.room_id`, memoryAlias, predicate), args...)
	if err != nil {
		return nil, safeDBError("count memories by room", err)
	}
	defer rows.Close()

	return scanRoomCounts(rows, "count memories by room")
}

// ListByIDs resolves a known set of ids under a surface's rules.
//
// It carries the transitive rule with it, because it builds the same
// filter the listing builds: a memory about an artifact in a withheld
// room is not selected here either. See ArtifactRepo.ListByIDs.
func (r *MemoryRepo) ListByIDs(ctx context.Context, workspaceID uuid.UUID, ids []uuid.UUID, v ports.Visibility) ([]*domain.Memory, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	predicate, args := memoryWhere(workspaceID, ports.MemoryFilter{
		Sensitive:             ports.Sensitive{IncludeHighlySensitive: v.IncludeHighlySensitive},
		InheritRoomVisibility: v.InheritRoomVisibility,
	})
	args = append(args, ids)
	idsAt := len(args)

	rows, err := postgres.Conn(ctx, r.pool).Query(ctx, fmt.Sprintf(`
		SELECT %[1]s
		FROM palace.memories %[2]s
		WHERE %[3]s AND %[2]s.id = ANY($%[4]d)
		ORDER BY %[2]s.updated_at DESC, %[2]s.id`,
		memoryCols, memoryAlias, predicate, idsAt), args...)
	if err != nil {
		return nil, safeDBError("resolve memories", err)
	}
	defer rows.Close()

	out := make([]*domain.Memory, 0, len(ids))
	for rows.Next() {
		m, err := scanMemory(rows)
		if err != nil {
			return nil, safeDBError("scan memory", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, safeDBError("resolve memories", err)
	}
	return out, nil
}

func (r *MemoryRepo) FindByID(ctx context.Context, workspaceID, id uuid.UUID) (*domain.Memory, error) {
	m, err := scanMemory(postgres.Conn(ctx, r.pool).QueryRow(ctx, `
		SELECT `+memoryCols+`
		FROM palace.memories
		WHERE workspace_id = $1 AND id = $2 AND deleted_at IS NULL`,
		workspaceID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.NotFound("memory %s was not found", id)
	}
	if err != nil {
		return nil, safeDBError("read memory", err)
	}
	return m, nil
}

// StatusOf reads one word instead of a whole memory. See
// RoomRepo.StatusOf.
func (r *MemoryRepo) StatusOf(ctx context.Context, workspaceID, id uuid.UUID) (domain.Lifecycle, error) {
	var status domain.Lifecycle
	err := postgres.Conn(ctx, r.pool).QueryRow(ctx, `
		SELECT status FROM palace.memories
		WHERE workspace_id = $1 AND id = $2 AND deleted_at IS NULL`,
		workspaceID, id).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", domain.NotFound("memory %s was not found", id)
	}
	if err != nil {
		return "", safeDBError("read memory status", err)
	}
	return status, nil
}

// KindOf reads one word instead of a whole memory.
//
// The decision_for rule turns on this word, and loading the memory to
// find it would pull the operator's own sentence into a path whose
// output is a yes or a no.
func (r *MemoryRepo) KindOf(ctx context.Context, workspaceID, id uuid.UUID) (domain.MemoryKind, error) {
	var kind domain.MemoryKind
	err := postgres.Conn(ctx, r.pool).QueryRow(ctx, `
		SELECT kind FROM palace.memories
		WHERE workspace_id = $1 AND id = $2 AND deleted_at IS NULL`,
		workspaceID, id).Scan(&kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", domain.NotFound("memory %s was not found", id)
	}
	if err != nil {
		return "", safeDBError("read memory kind", err)
	}
	return kind, nil
}

func (r *MemoryRepo) Create(ctx context.Context, workspaceID uuid.UUID, m *domain.Memory) error {
	if err := assertWorkspace("insert memory", workspaceID, m.WorkspaceID); err != nil {
		return err
	}
	if err := postgres.Conn(ctx, r.pool).QueryRow(ctx, `
		INSERT INTO palace.memories
			(workspace_id, kind, content, summary, importance, confidence,
			 occurred_at, room_id, artifact_id, status, sensitivity)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING id, created_at, updated_at`,
		workspaceID, string(m.Kind), m.Content, m.Summary, m.Importance,
		string(m.Confidence), m.OccurredAt, m.RoomID, m.ArtifactID,
		string(m.Status), string(m.Sensitivity),
	).Scan(&m.ID, &m.CreatedAt, &m.UpdatedAt); err != nil {
		return safeDBError("insert memory", err)
	}
	m.WorkspaceID = workspaceID
	return nil
}

func (r *MemoryRepo) Update(ctx context.Context, workspaceID uuid.UUID, m *domain.Memory) error {
	if err := assertWorkspace("update memory", workspaceID, m.WorkspaceID); err != nil {
		return err
	}
	err := postgres.Conn(ctx, r.pool).QueryRow(ctx, `
		UPDATE palace.memories
		SET kind = $3, content = $4, summary = $5, importance = $6, confidence = $7,
		    occurred_at = $8, room_id = $9, artifact_id = $10,
		    status = $11, sensitivity = $12, updated_at = now()
		WHERE workspace_id = $1 AND id = $2 AND deleted_at IS NULL
		RETURNING updated_at`,
		workspaceID, m.ID, string(m.Kind), m.Content, m.Summary, m.Importance,
		string(m.Confidence), m.OccurredAt, m.RoomID, m.ArtifactID,
		string(m.Status), string(m.Sensitivity)).Scan(&m.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.NotFound("memory %s was not found", m.ID)
	}
	if err != nil {
		return safeDBError("update memory", err)
	}
	return nil
}
