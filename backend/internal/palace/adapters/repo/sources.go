package repo

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/corsi/backend/internal/palace/domain"
	"github.com/corsi/backend/internal/platform/postgres"
)

// SourceRepo stores evidence.
//
// ══════════════════════════════════════════════════════════════════════
//
//	THERE IS NO UPDATE STATEMENT IN THIS FILE
//
// ══════════════════════════════════════════════════════════════════════
//
// Not an UPDATE that is only called from one place, not one guarded by a
// flag: none. A source answers "what was actually said or seen", and
// evidence that can be rewritten after the fact answers nothing.
//
// The enforcement is the absence itself. Nothing above can reach a
// statement that edits a source's content or its sensitivity, because no
// such statement exists to be reached, and adding one is a visible change
// to this file rather than an argument somebody passes.
type SourceRepo struct{ pool *pgxpool.Pool }

const sourceCols = `id, workspace_id, kind, content, external_ref, captured_at,
	sensitivity, created_at, updated_at`

func scanSource(row pgx.Row) (*domain.Source, error) {
	var s domain.Source
	if err := row.Scan(&s.ID, &s.WorkspaceID, &s.Kind, &s.Content, &s.ExternalRef,
		&s.CapturedAt, &s.Sensitivity, &s.CreatedAt, &s.UpdatedAt); err != nil {
		return nil, err
	}
	return &s, nil
}

func (r *SourceRepo) FindByID(ctx context.Context, workspaceID, id uuid.UUID) (*domain.Source, error) {
	s, err := scanSource(postgres.Conn(ctx, r.pool).QueryRow(ctx, `
		SELECT `+sourceCols+`
		FROM palace.sources
		WHERE workspace_id = $1 AND id = $2 AND deleted_at IS NULL`,
		workspaceID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.NotFound("source %s was not found", id)
	}
	if err != nil {
		return nil, safeDBError("read source", err)
	}
	return s, nil
}

// SensitivityOf reads one word instead of a whole transcript.
//
// The provenance path needs the source's level to establish the floor
// under a memory, and nothing else. Loading the content to read that word
// would put a recording in a code path whose output is a comparison. Same
// argument as ArtifactRepo.KindOf.
func (r *SourceRepo) SensitivityOf(ctx context.Context, workspaceID, id uuid.UUID) (domain.Sensitivity, error) {
	var level domain.Sensitivity
	err := postgres.Conn(ctx, r.pool).QueryRow(ctx, `
		SELECT sensitivity FROM palace.sources
		WHERE workspace_id = $1 AND id = $2 AND deleted_at IS NULL`,
		workspaceID, id).Scan(&level)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", domain.NotFound("source %s was not found", id)
	}
	if err != nil {
		return "", safeDBError("read source sensitivity", err)
	}
	return level, nil
}

func (r *SourceRepo) Create(ctx context.Context, workspaceID uuid.UUID, s *domain.Source) error {
	if err := assertWorkspace("insert source", workspaceID, s.WorkspaceID); err != nil {
		return err
	}
	if err := postgres.Conn(ctx, r.pool).QueryRow(ctx, `
		INSERT INTO palace.sources
			(workspace_id, kind, content, external_ref, captured_at, sensitivity)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, created_at, updated_at`,
		workspaceID, string(s.Kind), s.Content, s.ExternalRef,
		s.CapturedAt, string(s.Sensitivity),
	).Scan(&s.ID, &s.CreatedAt, &s.UpdatedAt); err != nil {
		return safeDBError("insert source", err)
	}
	s.WorkspaceID = workspaceID
	return nil
}
