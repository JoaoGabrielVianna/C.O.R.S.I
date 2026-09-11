package repo

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/corsi/backend/internal/finance/domain"
	"github.com/corsi/backend/internal/finance/ports"
	"github.com/corsi/backend/internal/platform/postgres"
)

type CategoryRepo struct {
	pool *pgxpool.Pool
}

func NewCategoryRepo(pool *pgxpool.Pool) *CategoryRepo {
	return &CategoryRepo{pool: pool}
}

const categoryCols = `id, workspace_id, name, type, color, icon, created_at, updated_at, deleted_at`

func scanCategory(row pgx.Row) (*domain.Category, error) {
	var c domain.Category
	if err := row.Scan(&c.ID, &c.WorkspaceID, &c.Name, &c.Type, &c.Color, &c.Icon,
		&c.CreatedAt, &c.UpdatedAt, &c.DeletedAt); err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *CategoryRepo) Create(ctx context.Context, c *domain.Category) error {
	q := `INSERT INTO finance.categories (id, workspace_id, name, type, color, icon)
	      VALUES ($1, $2, $3, $4, $5, $6)
	      RETURNING ` + categoryCols
	row := postgres.Conn(ctx, r.pool).QueryRow(ctx, q, c.ID, c.WorkspaceID, c.Name, c.Type, c.Color, c.Icon)
	got, err := scanCategory(row)
	if err != nil {
		return mapPgError(err, "category name already exists in workspace")
	}
	*c = *got
	return nil
}

func (r *CategoryRepo) Update(ctx context.Context, c *domain.Category) error {
	q := `UPDATE finance.categories
	      SET name = $3, color = $4, icon = $5, updated_at = now()
	      WHERE id = $1 AND workspace_id = $2 AND deleted_at IS NULL
	      RETURNING ` + categoryCols
	// type changes are not allowed: a transaction may already reference this
	// category and the composite FK would otherwise need orchestration.
	row := postgres.Conn(ctx, r.pool).QueryRow(ctx, q, c.ID, c.WorkspaceID, c.Name, c.Color, c.Icon)
	got, err := scanCategory(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.NotFound("category")
		}
		return mapPgError(err, "category name already exists in workspace")
	}
	*c = *got
	return nil
}

func (r *CategoryRepo) SoftDelete(ctx context.Context, workspaceID, id uuid.UUID) error {
	q := `UPDATE finance.categories SET deleted_at = now()
	      WHERE id = $1 AND workspace_id = $2 AND deleted_at IS NULL`
	tag, err := postgres.Conn(ctx, r.pool).Exec(ctx, q, id, workspaceID)
	if err != nil {
		return fmt.Errorf("soft delete category: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.NotFound("category")
	}
	return nil
}

func (r *CategoryRepo) FindByID(ctx context.Context, workspaceID, id uuid.UUID) (*domain.Category, error) {
	q := `SELECT ` + categoryCols + ` FROM finance.categories
	      WHERE id = $1 AND workspace_id = $2 AND deleted_at IS NULL`
	row := postgres.Conn(ctx, r.pool).QueryRow(ctx, q, id, workspaceID)
	c, err := scanCategory(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.NotFound("category")
	}
	return c, err
}

func (r *CategoryRepo) List(ctx context.Context, workspaceID uuid.UUID, f ports.CategoryFilter) ([]domain.Category, error) {
	q := `SELECT ` + categoryCols + ` FROM finance.categories
	      WHERE workspace_id = $1 AND deleted_at IS NULL
	        AND ($2::finance.entry_type IS NULL OR type = $2)
	      ORDER BY lower(name) ASC
	      LIMIT $3 OFFSET $4`
	rows, err := postgres.Conn(ctx, r.pool).Query(ctx, q, workspaceID, f.Type, f.Limit, f.Offset)
	if err != nil {
		return nil, fmt.Errorf("list categories: %w", err)
	}
	defer rows.Close()
	out := make([]domain.Category, 0)
	for rows.Next() {
		c, err := scanCategory(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// mapPgError translates Postgres unique-violation into a domain Conflict.
// Anything else is returned as-is for the service layer to wrap.
func mapPgError(err error, conflictMsg string) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return domain.Conflict(conflictMsg)
	}
	return err
}
