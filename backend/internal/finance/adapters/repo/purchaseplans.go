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

type PurchasePlanRepo struct {
	pool *pgxpool.Pool
}

func NewPurchasePlanRepo(pool *pgxpool.Pool) *PurchasePlanRepo {
	return &PurchasePlanRepo{pool: pool}
}

const purchasePlanCols = `id, workspace_id, person_id, name, total_amount_cents,
                          installments, remaining_installments,
                          created_at, deleted_at`

func scanPurchasePlan(row pgx.Row) (*domain.PurchasePlan, error) {
	var p domain.PurchasePlan
	if err := row.Scan(&p.ID, &p.WorkspaceID, &p.PersonID, &p.Name, &p.TotalAmountCents,
		&p.Installments, &p.RemainingInstallments,
		&p.CreatedAt, &p.DeletedAt); err != nil {
		return nil, err
	}
	return &p, nil
}

func (r *PurchasePlanRepo) Create(ctx context.Context, p *domain.PurchasePlan) error {
	q := `INSERT INTO finance.purchase_plans
	      (id, workspace_id, person_id, name, total_amount_cents,
	       installments, remaining_installments)
	      VALUES ($1, $2, $3, $4, $5, $6, $7)
	      RETURNING ` + purchasePlanCols
	row := postgres.Conn(ctx, r.pool).QueryRow(ctx, q,
		p.ID, p.WorkspaceID, p.PersonID, p.Name, p.TotalAmountCents,
		p.Installments, p.RemainingInstallments)
	got, err := scanPurchasePlan(row)
	if err != nil {
		if isFKViolation(err) {
			return domain.Invalid("person_id does not exist")
		}
		return fmt.Errorf("create purchase plan: %w", err)
	}
	*p = *got
	return nil
}

func (r *PurchasePlanRepo) FindByID(ctx context.Context, workspaceID, id uuid.UUID) (*domain.PurchasePlan, error) {
	q := `SELECT ` + purchasePlanCols + ` FROM finance.purchase_plans
	      WHERE id = $1 AND workspace_id = $2 AND deleted_at IS NULL`
	row := postgres.Conn(ctx, r.pool).QueryRow(ctx, q, id, workspaceID)
	p, err := scanPurchasePlan(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.NotFound("purchase plan")
	}
	return p, err
}

func (r *PurchasePlanRepo) List(ctx context.Context, workspaceID uuid.UUID, f ports.PurchasePlanFilter) ([]domain.PurchasePlan, error) {
	q := `SELECT ` + purchasePlanCols + ` FROM finance.purchase_plans
	      WHERE workspace_id = $1 AND deleted_at IS NULL
	      ORDER BY created_at DESC, id DESC
	      LIMIT $2 OFFSET $3`
	rows, err := postgres.Conn(ctx, r.pool).Query(ctx, q, workspaceID, f.Limit, f.Offset)
	if err != nil {
		return nil, fmt.Errorf("list purchase plans: %w", err)
	}
	defer rows.Close()
	out := make([]domain.PurchasePlan, 0)
	for rows.Next() {
		p, err := scanPurchasePlan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// CancelFutureInstallments soft-deletes every live installment of the
// plan whose status is `scheduled` and then updates the parent's
// remaining_installments to reflect the live not-paid count. Paid
// installments (REALIZED per totals-contract §2.2) are preserved as
// history. The caller wraps this in a tx so partial cancels never land.
func (r *PurchasePlanRepo) CancelFutureInstallments(ctx context.Context, workspaceID, planID uuid.UUID) (int64, error) {
	conn := postgres.Conn(ctx, r.pool)

	// Reject if the plan doesn't exist in this workspace or is already
	// soft-deleted. Touching only the FROM via an UPDATE…FROM keeps the
	// workspace guard load-bearing for the soft-delete step itself.
	tag, err := conn.Exec(ctx, `
		UPDATE finance.transactions t
		   SET deleted_at = now()
		  FROM finance.purchase_plans p
		 WHERE t.plan_id      = p.id
		   AND t.plan_id      = $2
		   AND t.workspace_id = $1
		   AND p.workspace_id = $1
		   AND p.deleted_at IS NULL
		   AND t.deleted_at IS NULL
		   AND t.status        = 'scheduled'`, workspaceID, planID)
	if err != nil {
		return 0, fmt.Errorf("cancel installments: %w", err)
	}
	deleted := tag.RowsAffected()

	// Recompute remaining_installments from the live non-paid, non-deleted
	// installments. This stays correct even if a caller had previously
	// PATCH'd installments to paid (which we do not eagerly track).
	resTag, err := conn.Exec(ctx, `
		UPDATE finance.purchase_plans
		   SET remaining_installments = COALESCE((
		         SELECT COUNT(*)::int FROM finance.transactions t
		          WHERE t.plan_id      = $2
		            AND t.workspace_id = $1
		            AND t.deleted_at IS NULL
		            AND t.status <> 'paid'
		       ), 0)
		 WHERE id = $2 AND workspace_id = $1 AND deleted_at IS NULL`,
		workspaceID, planID)
	if err != nil {
		return 0, fmt.Errorf("recompute remaining: %w", err)
	}
	if resTag.RowsAffected() == 0 {
		return 0, domain.NotFound("purchase plan")
	}
	return deleted, nil
}
