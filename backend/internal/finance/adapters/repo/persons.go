package repo

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/corsi/backend/internal/finance/domain"
	"github.com/corsi/backend/internal/finance/ports"
	"github.com/corsi/backend/internal/platform/postgres"
)

type PersonRepo struct {
	pool *pgxpool.Pool
}

func NewPersonRepo(pool *pgxpool.Pool) *PersonRepo {
	return &PersonRepo{pool: pool}
}

const personCols = `id, workspace_id, name, notes, created_at, updated_at, deleted_at`

func scanPerson(row pgx.Row) (*domain.Person, error) {
	var p domain.Person
	if err := row.Scan(&p.ID, &p.WorkspaceID, &p.Name, &p.Notes,
		&p.CreatedAt, &p.UpdatedAt, &p.DeletedAt); err != nil {
		return nil, err
	}
	return &p, nil
}

func (r *PersonRepo) Create(ctx context.Context, p *domain.Person) error {
	q := `INSERT INTO finance.persons (id, workspace_id, name, notes)
	      VALUES ($1, $2, $3, $4)
	      RETURNING ` + personCols
	row := postgres.Conn(ctx, r.pool).QueryRow(ctx, q, p.ID, p.WorkspaceID, p.Name, p.Notes)
	got, err := scanPerson(row)
	if err != nil {
		return mapPgError(err, "person name already exists in workspace")
	}
	*p = *got
	return nil
}

func (r *PersonRepo) Update(ctx context.Context, p *domain.Person) error {
	q := `UPDATE finance.persons
	      SET name = $3, notes = $4, updated_at = now()
	      WHERE id = $1 AND workspace_id = $2 AND deleted_at IS NULL
	      RETURNING ` + personCols
	row := postgres.Conn(ctx, r.pool).QueryRow(ctx, q, p.ID, p.WorkspaceID, p.Name, p.Notes)
	got, err := scanPerson(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.NotFound("person")
		}
		return mapPgError(err, "person name already exists in workspace")
	}
	*p = *got
	return nil
}

func (r *PersonRepo) SoftDelete(ctx context.Context, workspaceID, id uuid.UUID) error {
	q := `UPDATE finance.persons SET deleted_at = now()
	      WHERE id = $1 AND workspace_id = $2 AND deleted_at IS NULL`
	tag, err := postgres.Conn(ctx, r.pool).Exec(ctx, q, id, workspaceID)
	if err != nil {
		return fmt.Errorf("soft delete person: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.NotFound("person")
	}
	return nil
}

func (r *PersonRepo) FindByID(ctx context.Context, workspaceID, id uuid.UUID) (*domain.Person, error) {
	q := `SELECT ` + personCols + ` FROM finance.persons
	      WHERE id = $1 AND workspace_id = $2 AND deleted_at IS NULL`
	row := postgres.Conn(ctx, r.pool).QueryRow(ctx, q, id, workspaceID)
	p, err := scanPerson(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.NotFound("person")
	}
	return p, err
}

func (r *PersonRepo) List(ctx context.Context, workspaceID uuid.UUID, f ports.PersonFilter) ([]domain.Person, error) {
	q := `SELECT ` + personCols + ` FROM finance.persons
	      WHERE workspace_id = $1 AND deleted_at IS NULL
	      ORDER BY lower(name) ASC
	      LIMIT $2 OFFSET $3`
	rows, err := postgres.Conn(ctx, r.pool).Query(ctx, q, workspaceID, f.Limit, f.Offset)
	if err != nil {
		return nil, fmt.Errorf("list persons: %w", err)
	}
	defer rows.Close()
	out := make([]domain.Person, 0)
	for rows.Next() {
		p, err := scanPerson(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}
