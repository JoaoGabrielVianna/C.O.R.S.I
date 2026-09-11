package repo

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/corsi/backend/internal/finance/domain"
	"github.com/corsi/backend/internal/platform/postgres"
)

type ImportSourceRepo struct {
	pool *pgxpool.Pool
}

func NewImportSourceRepo(pool *pgxpool.Pool) *ImportSourceRepo {
	return &ImportSourceRepo{pool: pool}
}

const importSourceCols = `id, workspace_id, kind, institution, label, last4,
	card_id, created_at, updated_at, deleted_at`

func scanImportSource(row pgx.Row) (*domain.ImportSource, error) {
	var s domain.ImportSource
	if err := row.Scan(&s.ID, &s.WorkspaceID, &s.Kind, &s.Institution, &s.Label,
		&s.Last4, &s.CardID, &s.CreatedAt, &s.UpdatedAt, &s.DeletedAt); err != nil {
		return nil, err
	}
	return &s, nil
}

func (r *ImportSourceRepo) Create(ctx context.Context, s *domain.ImportSource) error {
	row := postgres.Conn(ctx, r.pool).QueryRow(ctx, `
		INSERT INTO finance.import_sources (workspace_id, kind, institution, label, last4, card_id)
		VALUES ($1,$2,$3,$4,$5,$6) RETURNING `+importSourceCols,
		s.WorkspaceID, s.Kind, s.Institution, s.Label, s.Last4, s.CardID)
	got, err := scanImportSource(row)
	if err != nil {
		if isUniqueViolation(err) {
			return domain.Conflict("an import source with this label already exists")
		}
		if isFKViolation(err) {
			return domain.Invalid("card_id does not reference an existing card")
		}
		return err
	}
	*s = *got
	return nil
}

// FindByID is workspace-scoped in the predicate, not checked afterwards: a
// source belonging to somebody else must be indistinguishable from one
// that never existed, or the refusal becomes an existence oracle.
func (r *ImportSourceRepo) FindByID(ctx context.Context, workspaceID, id uuid.UUID) (*domain.ImportSource, error) {
	row := postgres.Conn(ctx, r.pool).QueryRow(ctx,
		`SELECT `+importSourceCols+` FROM finance.import_sources
		  WHERE id=$1 AND workspace_id=$2 AND deleted_at IS NULL`, id, workspaceID)
	s, err := scanImportSource(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.NotFound("import source not found")
		}
		return nil, err
	}
	return s, nil
}

func (r *ImportSourceRepo) List(ctx context.Context, workspaceID uuid.UUID, limit int) ([]domain.ImportSource, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := postgres.Conn(ctx, r.pool).Query(ctx,
		`SELECT `+importSourceCols+` FROM finance.import_sources
		  WHERE workspace_id=$1 AND deleted_at IS NULL
		  ORDER BY lower(label) LIMIT $2`, workspaceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.ImportSource{}
	for rows.Next() {
		var s domain.ImportSource
		if err := rows.Scan(&s.ID, &s.WorkspaceID, &s.Kind, &s.Institution, &s.Label,
			&s.Last4, &s.CardID, &s.CreatedAt, &s.UpdatedAt, &s.DeletedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
