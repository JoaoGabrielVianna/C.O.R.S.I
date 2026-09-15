package repo

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/corsi/backend/internal/palace/domain"
	"github.com/corsi/backend/internal/platform/postgres"
)

// ProvenanceRepo is the memory_sources table.
//
// ══════════════════════════════════════════════════════════════════════
//
//	THERE IS NO UNLINK AND NO UPDATE IN THIS FILE
//
// ══════════════════════════════════════════════════════════════════════
//
// Append-only IN THIS VERSION, enforced the same way SourceRepo enforces
// immutability: by the absence of a statement. See domain/provenance.go
// for why the record is worth having only if it is not routinely tidied,
// and for the note that this is a Palace Core v1 scope decision rather
// than an eternal invariant. A future explicit correction would add a
// statement here, and would have to reckon with the sensitivity floor it
// removes.
type ProvenanceRepo struct{ pool *pgxpool.Pool }

// Link records that a memory rests on a source, idempotently.
//
// ── Why ON CONFLICT DO NOTHING rather than a read then an insert ───────
// Because the check and the write would be two statements with a gap
// between them, and the primary key is the thing that actually decides.
// Letting the key decide makes a repeat a no-op instead of a race, and
// `RowsAffected` is then a truthful answer to "did this call create the
// link", which is what lets a caller say "já estava registrado" instead
// of claiming work it did not do.
//
// ── Why the workspace is in the VALUES rather than only on the key ─────
// The composite foreign keys point at (workspace_id, memory_id) and
// (workspace_id, source_id), so writing the caller's workspace here is
// what makes the database refuse a link whose ends are not both this
// workspace's. The application layer resolves both first and answers
// not-found; this is the backstop under that.
func (r *ProvenanceRepo) Link(ctx context.Context, workspaceID, memoryID, sourceID uuid.UUID) (bool, error) {
	tag, err := postgres.Conn(ctx, r.pool).Exec(ctx, `
		INSERT INTO palace.memory_sources (workspace_id, memory_id, source_id)
		VALUES ($1, $2, $3)
		ON CONFLICT (memory_id, source_id) DO NOTHING`,
		workspaceID, memoryID, sourceID)
	if err != nil {
		return false, safeDBError("link provenance", err)
	}
	return tag.RowsAffected() == 1, nil
}

// SourcesFor returns the evidence behind one memory, oldest link first.
//
// Ordered by when the LINK was made rather than by when the evidence was
// captured: the question is how the belief was built up, and that is the
// order it was built in. A transcript from last year cited today is the
// most recent thing that happened to this memory.
//
// The join carries the workspace on both sides. That is redundant given
// the composite foreign keys, and it is redundant on purpose: this is the
// one read that walks from one table to another, and a join that only
// matched on id would be the single place in this package where a row
// could arrive from somewhere else.
func (r *ProvenanceRepo) SourcesFor(ctx context.Context, workspaceID, memoryID uuid.UUID) ([]*domain.Source, error) {
	rows, err := postgres.Conn(ctx, r.pool).Query(ctx, `
		SELECT s.id, s.workspace_id, s.kind, s.content, s.external_ref, s.captured_at,
		       s.sensitivity, s.created_at, s.updated_at
		FROM palace.memory_sources ms
		JOIN palace.sources s
		  ON s.workspace_id = ms.workspace_id AND s.id = ms.source_id
		WHERE ms.workspace_id = $1 AND ms.memory_id = $2 AND s.deleted_at IS NULL
		ORDER BY ms.created_at, s.id`,
		workspaceID, memoryID)
	if err != nil {
		return nil, safeDBError("read provenance", err)
	}
	defer rows.Close()

	out := make([]*domain.Source, 0, 4)
	for rows.Next() {
		s, err := scanSource(rows)
		if err != nil {
			return nil, safeDBError("scan source", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, safeDBError("read provenance", err)
	}
	return out, nil
}

// SourceSensitivities returns the distinct levels behind one memory.
//
// At most three rows, and deliberately not a maximum: the ORDER of the
// levels is a domain rule, and a `CASE WHEN sensitivity = …` here would
// be a second copy of it, free to disagree the day a fourth level exists.
// This reports what is there; domain.MaxSensitivity decides which is
// highest.
func (r *ProvenanceRepo) SourceSensitivities(ctx context.Context, workspaceID, memoryID uuid.UUID) ([]domain.Sensitivity, error) {
	rows, err := postgres.Conn(ctx, r.pool).Query(ctx, `
		SELECT DISTINCT s.sensitivity
		FROM palace.memory_sources ms
		JOIN palace.sources s
		  ON s.workspace_id = ms.workspace_id AND s.id = ms.source_id
		WHERE ms.workspace_id = $1 AND ms.memory_id = $2 AND s.deleted_at IS NULL`,
		workspaceID, memoryID)
	if err != nil {
		return nil, safeDBError("read provenance sensitivities", err)
	}
	defer rows.Close()

	out := make([]domain.Sensitivity, 0, len(domain.Sensitivities))
	for rows.Next() {
		var level domain.Sensitivity
		if err := rows.Scan(&level); err != nil {
			return nil, safeDBError("scan provenance sensitivity", err)
		}
		out = append(out, level)
	}
	if err := rows.Err(); err != nil {
		return nil, safeDBError("read provenance sensitivities", err)
	}
	return out, nil
}
