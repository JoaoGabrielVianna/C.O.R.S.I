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

// RelationRepo stores the controlled links between Palace entities.
//
// ══════════════════════════════════════════════════════════════════════
//
//	EVERY STATEMENT HERE IS FLAT. NONE OF THEM WALKS THE GRAPH
//
// ══════════════════════════════════════════════════════════════════════
//
// No recursive CTE, no self-join, no lateral, no depth. The reads answer
// "which edges touch this node", once, and following an edge is the
// caller's act.
//
// ── Why that is worth a banner in an adapter ───────────────────────────
// Because the traversal version is one WITH RECURSIVE away, it looks
// helpful, and its cost is unbounded in a way that does not show up
// until the graph is big enough to matter. A read that quietly became a
// traversal is also a read that can return an entity a caller never
// asked about, through a chain of edges nobody reviewed.
type RelationRepo struct{ pool *pgxpool.Pool }

const relationCols = `id, workspace_id, from_type, from_id, kind, to_type, to_id, created_at`

func scanRelation(row pgx.Row) (*domain.Relation, error) {
	var r domain.Relation
	if err := row.Scan(&r.ID, &r.WorkspaceID, &r.FromType, &r.FromID,
		&r.Kind, &r.ToType, &r.ToID, &r.CreatedAt); err != nil {
		return nil, err
	}
	return &r, nil
}

// relationWhere builds the predicate List and Count share, for the reason
// roomWhere gives.
func relationWhere(workspaceID uuid.UUID, f ports.RelationFilter) (string, []any) {
	args := []any{workspaceID}
	clauses := []string{"workspace_id = $1"}

	bind := func(value any) int {
		args = append(args, value)
		return len(args)
	}

	if f.Kind != nil {
		clauses = append(clauses, fmt.Sprintf("kind = $%d", bind(string(*f.Kind))))
	}
	if f.Anchor != nil {
		t := bind(string(f.Anchor.Type))
		id := bind(f.Anchor.ID)
		from := fmt.Sprintf("(from_type = $%d AND from_id = $%d)", t, id)
		to := fmt.Sprintf("(to_type = $%d AND to_id = $%d)", t, id)

		switch f.Direction {
		case ports.DirectionFrom:
			clauses = append(clauses, from)
		case ports.DirectionTo:
			clauses = append(clauses, to)
		default:
			// Both ends. This is an OR over two indexed predicates,
			// relations_from_idx and relations_to_idx, not a join: the
			// planner reads each side once and unions them.
			clauses = append(clauses, "("+from+" OR "+to+")")
		}
	}
	return strings.Join(clauses, " AND "), args
}

func (r *RelationRepo) List(ctx context.Context, workspaceID uuid.UUID, f ports.RelationFilter) ([]*domain.Relation, error) {
	predicate, args := relationWhere(workspaceID, f)

	limit := bound(f.Limit)
	args = append(args, limit)
	limitAt := len(args)
	args = append(args, f.Offset)
	offsetAt := len(args)

	// created_at DESC: the most recently asserted link first, which is
	// what somebody asking "what did I connect to this" means. `id`
	// breaks the tie so two identical reads return an identical order.
	//
	// Relations carry no updated_at, because there is no update.
	rows, err := postgres.Conn(ctx, r.pool).Query(ctx, fmt.Sprintf(`
		SELECT %s
		FROM palace.relations
		WHERE %s
		ORDER BY created_at DESC, id
		LIMIT $%d OFFSET $%d`,
		relationCols, predicate, limitAt, offsetAt), args...)
	if err != nil {
		return nil, safeDBError("list relations", err)
	}
	defer rows.Close()

	out := make([]*domain.Relation, 0, limit)
	for rows.Next() {
		rel, err := scanRelation(rows)
		if err != nil {
			return nil, safeDBError("scan relation", err)
		}
		out = append(out, rel)
	}
	if err := rows.Err(); err != nil {
		return nil, safeDBError("list relations", err)
	}
	return out, nil
}

func (r *RelationRepo) Count(ctx context.Context, workspaceID uuid.UUID, f ports.RelationFilter) (int64, error) {
	predicate, args := relationWhere(workspaceID, f)
	var n int64
	if err := postgres.Conn(ctx, r.pool).QueryRow(ctx, fmt.Sprintf(`
		SELECT count(*) FROM palace.relations WHERE %s`, predicate), args...).Scan(&n); err != nil {
		return 0, safeDBError("count relations", err)
	}
	return n, nil
}

func (r *RelationRepo) FindByID(ctx context.Context, workspaceID, id uuid.UUID) (*domain.Relation, error) {
	rel, err := scanRelation(postgres.Conn(ctx, r.pool).QueryRow(ctx, `
		SELECT `+relationCols+`
		FROM palace.relations
		WHERE workspace_id = $1 AND id = $2`,
		workspaceID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.NotFound("relation %s was not found", id)
	}
	if err != nil {
		return nil, safeDBError("read relation", err)
	}
	return rel, nil
}

// Create writes the edge, or hands back the one that was already there.
//
// ── Why one statement and not "look, then insert" ──────────────────────
// Because the check and the write would leave a gap in which another
// caller inserts the same edge, and the unique index is the thing that
// actually decides. The CTE lets the index decide and still returns a
// row either way: the inserted one when there was none, the existing one
// when there was. A caller therefore always gets an id it can remove,
// and never has to handle "it already existed, go and find it yourself".
func (r *RelationRepo) Create(ctx context.Context, workspaceID uuid.UUID, rel *domain.Relation) (bool, error) {
	if err := assertWorkspace("insert relation", workspaceID, rel.WorkspaceID); err != nil {
		return false, err
	}

	var created bool
	err := postgres.Conn(ctx, r.pool).QueryRow(ctx, `
		WITH inserted AS (
			INSERT INTO palace.relations
				(workspace_id, from_type, from_id, kind, to_type, to_id)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (workspace_id, from_type, from_id, kind, to_type, to_id) DO NOTHING
			RETURNING id, created_at
		)
		SELECT id, created_at, TRUE FROM inserted
		UNION ALL
		SELECT id, created_at, FALSE
		FROM palace.relations
		WHERE workspace_id = $1 AND from_type = $2 AND from_id = $3
		  AND kind = $4 AND to_type = $5 AND to_id = $6
		  AND NOT EXISTS (SELECT 1 FROM inserted)
		LIMIT 1`,
		workspaceID, string(rel.FromType), rel.FromID,
		string(rel.Kind), string(rel.ToType), rel.ToID,
	).Scan(&rel.ID, &rel.CreatedAt, &created)
	if err != nil {
		return false, safeDBError("insert relation", err)
	}
	rel.WorkspaceID = workspaceID
	return created, nil
}

// Delete removes one edge, physically.
//
// ── Why DELETE and not a deleted_at ────────────────────────────────────
// A relation is an assertion, not content. Archiving exists so somebody
// can say "not this, for now" about something they made; an edge that
// should not have been filed is not a thing to retire, and a tombstone
// for it would mean every reader having to know which edges are real.
//
// ── What this statement cannot touch ───────────────────────────────────
// The entities. It names one table, and the endpoints are plain uuids in
// columns of it. There is no cascade in either direction: removing an
// edge cannot reach a memory, and removing a memory cannot reach an
// edge, because `palace.relations` carries no foreign key at all.
func (r *RelationRepo) Delete(ctx context.Context, workspaceID, id uuid.UUID) error {
	tag, err := postgres.Conn(ctx, r.pool).Exec(ctx, `
		DELETE FROM palace.relations
		WHERE workspace_id = $1 AND id = $2`, workspaceID, id)
	if err != nil {
		return safeDBError("remove relation", err)
	}
	if tag.RowsAffected() == 0 {
		// Removing twice is not-found rather than a silent success, which
		// is what lets a caller tell "I removed it" from "it was already
		// gone". It is also the answer a relation from another workspace
		// gets, on the same terms as everything else here.
		return domain.NotFound("relation %s was not found", id)
	}
	return nil
}

// HasDecisionFor answers whether a memory governs anything as a decision.
//
// One bit. It does not load the relations and does not join: the guard on
// reclassification needs to know that at least one exists, and reading
// them would be a list nobody asked for.
func (r *RelationRepo) HasDecisionFor(ctx context.Context, workspaceID, memoryID uuid.UUID) (bool, error) {
	var found bool
	err := postgres.Conn(ctx, r.pool).QueryRow(ctx, `
		SELECT TRUE FROM palace.relations
		WHERE workspace_id = $1 AND from_type = $2 AND from_id = $3 AND kind = $4
		LIMIT 1`,
		workspaceID, string(domain.EntityMemory), memoryID, string(domain.RelationDecisionFor),
	).Scan(&found)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, safeDBError("check decision relations", err)
	}
	return found, nil
}
