package repo

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/corsi/backend/internal/finance/domain"
	"github.com/corsi/backend/internal/finance/ports"
	"github.com/corsi/backend/internal/platform/postgres"
)

type RecurringEntryRepo struct {
	pool *pgxpool.Pool
}

func NewRecurringEntryRepo(pool *pgxpool.Pool) *RecurringEntryRepo {
	return &RecurringEntryRepo{pool: pool}
}

const recurringCols = `id, workspace_id, description, amount_cents, category_id, person_id,
                   due_day, recurrence, due_month, status, amount_varies,
                   starts_at, ends_at, notes, created_at, updated_at, deleted_at`

func scanRecurring(row pgx.Row) (*domain.RecurringEntry, error) {
	var f domain.RecurringEntry
	if err := row.Scan(&f.ID, &f.WorkspaceID, &f.Description, &f.AmountCents, &f.CategoryID,
		&f.PersonID, &f.DueDay, &f.Recurrence, &f.DueMonth, &f.Status, &f.AmountVaries,
		&f.StartsAt, &f.EndsAt, &f.Notes,
		&f.CreatedAt, &f.UpdatedAt, &f.DeletedAt); err != nil {
		return nil, err
	}
	return &f, nil
}

func (r *RecurringEntryRepo) Create(ctx context.Context, f *domain.RecurringEntry) error {
	q := `INSERT INTO finance.recurring_entries
	      (id, workspace_id, description, amount_cents, category_id, person_id,
	       due_day, recurrence, due_month, status, amount_varies, starts_at, ends_at, notes)
	      VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
	      RETURNING ` + recurringCols
	row := postgres.Conn(ctx, r.pool).QueryRow(ctx, q,
		f.ID, f.WorkspaceID, f.Description, f.AmountCents, f.CategoryID, f.PersonID,
		f.DueDay, f.Recurrence, f.DueMonth, f.Status, f.AmountVaries, f.StartsAt, f.EndsAt, f.Notes)
	got, err := scanRecurring(row)
	if err != nil {
		return mapRecurringFK(err)
	}
	*f = *got
	return nil
}

func (r *RecurringEntryRepo) Update(ctx context.Context, f *domain.RecurringEntry) error {
	q := `UPDATE finance.recurring_entries
	      SET description = $3, amount_cents = $4, category_id = $5, person_id = $6,
	          due_day = $7, recurrence = $8, due_month = $9, status = $10,
	          amount_varies = $11, starts_at = $12,
	          ends_at = $13, notes = $14, updated_at = now()
	      WHERE id = $1 AND workspace_id = $2 AND deleted_at IS NULL
	      RETURNING ` + recurringCols
	row := postgres.Conn(ctx, r.pool).QueryRow(ctx, q,
		f.ID, f.WorkspaceID, f.Description, f.AmountCents, f.CategoryID, f.PersonID,
		f.DueDay, f.Recurrence, f.DueMonth, f.Status, f.AmountVaries,
		f.StartsAt, f.EndsAt, f.Notes)
	got, err := scanRecurring(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.NotFound("recurring entry")
		}
		return mapRecurringFK(err)
	}
	*f = *got
	return nil
}

func (r *RecurringEntryRepo) SoftDelete(ctx context.Context, workspaceID, id uuid.UUID) error {
	q := `UPDATE finance.recurring_entries SET deleted_at = now()
	      WHERE id = $1 AND workspace_id = $2 AND deleted_at IS NULL`
	tag, err := postgres.Conn(ctx, r.pool).Exec(ctx, q, id, workspaceID)
	if err != nil {
		return fmt.Errorf("soft delete recurring entry: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.NotFound("recurring entry")
	}
	return nil
}

func (r *RecurringEntryRepo) FindByID(ctx context.Context, workspaceID, id uuid.UUID) (*domain.RecurringEntry, error) {
	q := `SELECT ` + recurringCols + ` FROM finance.recurring_entries
	      WHERE id = $1 AND workspace_id = $2 AND deleted_at IS NULL`
	f, err := scanRecurring(postgres.Conn(ctx, r.pool).QueryRow(ctx, q, id, workspaceID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.NotFound("recurring entry")
	}
	return f, err
}

func (r *RecurringEntryRepo) List(ctx context.Context, workspaceID uuid.UUID, f ports.RecurringEntryFilter) ([]domain.RecurringEntry, error) {
	where := []string{"workspace_id = $1", "deleted_at IS NULL"}
	args := []any{workspaceID}
	if f.CategoryID != nil {
		args = append(args, *f.CategoryID)
		where = append(where, fmt.Sprintf("category_id = $%d", len(args)))
	}
	if f.ActiveOnly {
		// "Running now" is the same predicate the domain applies — active,
		// started, not ended. Expressed in SQL so a listing does not have to
		// read rows it will discard.
		where = append(where, "status = 'active'", "starts_at <= now()",
			"(ends_at IS NULL OR ends_at > now())")
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 200
	}
	args = append(args, limit)
	q := `SELECT ` + recurringCols + ` FROM finance.recurring_entries
	      WHERE ` + strings.Join(where, " AND ") + `
	      ORDER BY due_day, description
	      LIMIT $` + fmt.Sprint(len(args))
	args = append(args, f.Offset)
	q += ` OFFSET $` + fmt.Sprint(len(args))

	rows, err := postgres.Conn(ctx, r.pool).Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list recurring entries: %w", err)
	}
	defer rows.Close()
	out := make([]domain.RecurringEntry, 0)
	for rows.Next() {
		f, err := scanRecurring(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *f)
	}
	return out, rows.Err()
}

// mapRecurringFK turns a foreign-key violation into the domain error that says
// which reference did not resolve. Two FKs exist here, so the message names
// both rather than guessing.
func mapRecurringFK(err error) error {
	if isFKViolation(err) {
		return domain.Invalid("category_id or person_id does not exist in this workspace")
	}
	return err
}
