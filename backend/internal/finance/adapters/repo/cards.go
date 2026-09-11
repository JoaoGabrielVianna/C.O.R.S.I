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

type CardRepo struct {
	pool *pgxpool.Pool
}

func NewCardRepo(pool *pgxpool.Pool) *CardRepo {
	return &CardRepo{pool: pool}
}

const cardCols = `id, workspace_id, name, institution, network, variant, last4,
                  limit_cents, closing_day, due_day, created_at, updated_at, deleted_at`

func scanCard(row pgx.Row) (*domain.Card, error) {
	var c domain.Card
	if err := row.Scan(&c.ID, &c.WorkspaceID, &c.Name, &c.Institution, &c.Network, &c.Variant, &c.Last4,
		&c.LimitCents, &c.ClosingDay, &c.DueDay, &c.CreatedAt, &c.UpdatedAt, &c.DeletedAt); err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *CardRepo) Create(ctx context.Context, c *domain.Card) error {
	q := `INSERT INTO finance.cards
	      (id, workspace_id, name, institution, network, variant, last4, limit_cents, closing_day, due_day)
	      VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	      RETURNING ` + cardCols
	row := postgres.Conn(ctx, r.pool).QueryRow(ctx, q,
		c.ID, c.WorkspaceID, c.Name, c.Institution, c.Network, c.Variant, c.Last4,
		c.LimitCents, c.ClosingDay, c.DueDay)
	got, err := scanCard(row)
	if err != nil {
		return mapPgError(err, "card name already exists in workspace")
	}
	*c = *got
	return nil
}

func (r *CardRepo) Update(ctx context.Context, c *domain.Card) error {
	q := `UPDATE finance.cards
	      SET name = $3, institution = $4, network = $5, variant = $6, last4 = $7,
	          limit_cents = $8, closing_day = $9, due_day = $10, updated_at = now()
	      WHERE id = $1 AND workspace_id = $2 AND deleted_at IS NULL
	      RETURNING ` + cardCols
	row := postgres.Conn(ctx, r.pool).QueryRow(ctx, q,
		c.ID, c.WorkspaceID, c.Name, c.Institution, c.Network, c.Variant, c.Last4,
		c.LimitCents, c.ClosingDay, c.DueDay)
	got, err := scanCard(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.NotFound("card")
		}
		return mapPgError(err, "card name already exists in workspace")
	}
	*c = *got
	return nil
}

func (r *CardRepo) Archive(ctx context.Context, workspaceID, id uuid.UUID) error {
	q := `UPDATE finance.cards SET deleted_at = now()
	      WHERE id = $1 AND workspace_id = $2 AND deleted_at IS NULL`
	tag, err := postgres.Conn(ctx, r.pool).Exec(ctx, q, id, workspaceID)
	if err != nil {
		return fmt.Errorf("archive card: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.NotFound("card")
	}
	return nil
}

func (r *CardRepo) FindByID(ctx context.Context, workspaceID, id uuid.UUID) (*domain.Card, error) {
	q := `SELECT ` + cardCols + ` FROM finance.cards
	      WHERE id = $1 AND workspace_id = $2 AND deleted_at IS NULL`
	row := postgres.Conn(ctx, r.pool).QueryRow(ctx, q, id, workspaceID)
	c, err := scanCard(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.NotFound("card")
	}
	return c, err
}

func (r *CardRepo) List(ctx context.Context, workspaceID uuid.UUID, f ports.CardFilter) ([]domain.Card, error) {
	q := `SELECT ` + cardCols + ` FROM finance.cards
	      WHERE workspace_id = $1 AND deleted_at IS NULL
	      ORDER BY lower(name) ASC
	      LIMIT $2 OFFSET $3`
	rows, err := postgres.Conn(ctx, r.pool).Query(ctx, q, workspaceID, f.Limit, f.Offset)
	if err != nil {
		return nil, fmt.Errorf("list cards: %w", err)
	}
	defer rows.Close()
	out := make([]domain.Card, 0)
	for rows.Next() {
		c, err := scanCard(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}
