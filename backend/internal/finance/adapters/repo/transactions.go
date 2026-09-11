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

type TransactionRepo struct {
	pool *pgxpool.Pool
}

func NewTransactionRepo(pool *pgxpool.Pool) *TransactionRepo {
	return &TransactionRepo{pool: pool}
}

const txCols = `id, workspace_id, account_id, category_id, type, person_id,
                amount_cents, description, occurred_at,
                status, payment_method, source, notes,
                transfer_pair_id, plan_id, installment_number,
                external_source, external_id,
                created_at, updated_at, deleted_at`

func scanTx(row pgx.Row) (*domain.Transaction, error) {
	var t domain.Transaction
	if err := row.Scan(&t.ID, &t.WorkspaceID, &t.AccountID, &t.CategoryID, &t.Type, &t.PersonID,
		&t.AmountCents, &t.Description, &t.OccurredAt,
		&t.Status, &t.PaymentMethod, &t.Source, &t.Notes,
		&t.TransferPairID, &t.PlanID, &t.InstallmentNumber,
		&t.ExternalSource, &t.ExternalID,
		&t.CreatedAt, &t.UpdatedAt, &t.DeletedAt); err != nil {
		return nil, err
	}
	return &t, nil
}

func (r *TransactionRepo) Create(ctx context.Context, t *domain.Transaction) error {
	q := `INSERT INTO finance.transactions
	      (id, workspace_id, account_id, category_id, type, person_id,
	       amount_cents, description, occurred_at,
	       status, payment_method, source, notes,
	       transfer_pair_id, plan_id, installment_number,
	       external_source, external_id)
	      VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)
	      RETURNING ` + txCols
	row := postgres.Conn(ctx, r.pool).QueryRow(ctx, q,
		t.ID, t.WorkspaceID, t.AccountID, t.CategoryID, t.Type, t.PersonID,
		t.AmountCents, t.Description, t.OccurredAt,
		t.Status, t.PaymentMethod, t.Source, t.Notes,
		t.TransferPairID, t.PlanID, t.InstallmentNumber,
		t.ExternalSource, t.ExternalID)
	got, err := scanTx(row)
	if err != nil {
		return mapFKViolation(err)
	}
	*t = *got
	return nil
}

func (r *TransactionRepo) Update(ctx context.Context, t *domain.Transaction) error {
	q := `UPDATE finance.transactions
	      SET account_id = $3, category_id = $4, type = $5, person_id = $6,
	          amount_cents = $7, description = $8, occurred_at = $9,
	          status = $10, payment_method = $11, source = $12, notes = $13,
	          updated_at = now()
	      WHERE id = $1 AND workspace_id = $2 AND deleted_at IS NULL
	      RETURNING ` + txCols
	row := postgres.Conn(ctx, r.pool).QueryRow(ctx, q,
		t.ID, t.WorkspaceID, t.AccountID, t.CategoryID, t.Type, t.PersonID,
		t.AmountCents, t.Description, t.OccurredAt,
		t.Status, t.PaymentMethod, t.Source, t.Notes)
	got, err := scanTx(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.NotFound("transaction")
		}
		return mapFKViolation(err)
	}
	*t = *got
	return nil
}

func (r *TransactionRepo) SoftDelete(ctx context.Context, workspaceID, id uuid.UUID) error {
	q := `UPDATE finance.transactions SET deleted_at = now()
	      WHERE id = $1 AND workspace_id = $2 AND deleted_at IS NULL`
	tag, err := postgres.Conn(ctx, r.pool).Exec(ctx, q, id, workspaceID)
	if err != nil {
		return fmt.Errorf("soft delete transaction: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.NotFound("transaction")
	}
	return nil
}

// SoftDeleteByTransferPair soft-deletes every live row sharing pairID
// inside the workspace. Caller must wrap in a tx so the two legs land
// together. The (workspace_id, transfer_pair_id) partial index makes the
// lookup index-only on the small transfer-pair slice.
func (r *TransactionRepo) SoftDeleteByTransferPair(ctx context.Context, workspaceID, pairID uuid.UUID) (int64, error) {
	q := `UPDATE finance.transactions SET deleted_at = now()
	      WHERE workspace_id = $1
	        AND transfer_pair_id = $2
	        AND deleted_at IS NULL`
	tag, err := postgres.Conn(ctx, r.pool).Exec(ctx, q, workspaceID, pairID)
	if err != nil {
		return 0, fmt.Errorf("soft delete transfer pair: %w", err)
	}
	return tag.RowsAffected(), nil
}

func (r *TransactionRepo) FindByID(ctx context.Context, workspaceID, id uuid.UUID) (*domain.Transaction, error) {
	q := `SELECT ` + txCols + ` FROM finance.transactions
	      WHERE id = $1 AND workspace_id = $2 AND deleted_at IS NULL`
	row := postgres.Conn(ctx, r.pool).QueryRow(ctx, q, id, workspaceID)
	t, err := scanTx(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.NotFound("transaction")
	}
	return t, err
}

func (r *TransactionRepo) List(ctx context.Context, workspaceID uuid.UUID, f ports.TransactionFilter) ([]domain.Transaction, error) {
	var (
		where = []string{"workspace_id = $1", "deleted_at IS NULL"}
		args  = []any{workspaceID}
	)
	if f.Type != nil {
		args = append(args, *f.Type)
		where = append(where, fmt.Sprintf("type = $%d", len(args)))
	}
	if f.CategoryID != nil {
		args = append(args, *f.CategoryID)
		where = append(where, fmt.Sprintf("category_id = $%d", len(args)))
	}
	if f.Status != nil {
		args = append(args, *f.Status)
		where = append(where, fmt.Sprintf("status = $%d", len(args)))
	}
	if f.From != nil {
		args = append(args, *f.From)
		where = append(where, fmt.Sprintf("occurred_at >= $%d", len(args)))
	}
	if f.To != nil {
		args = append(args, *f.To)
		where = append(where, fmt.Sprintf("occurred_at < $%d", len(args)))
	}
	// Text predicate on the description. ILIKE with both wildcards supplied
	// as part of the ARGUMENT, never concatenated into the SQL — the value
	// stays a parameter, so a description containing a quote is a search
	// term and not a statement.
	//
	// The two LIKE metacharacters are escaped first. Without that, a user
	// searching for "100%" would match every row, which is a wrong answer
	// rather than an injection, and wrong answers about money are the thing
	// this module exists to prevent.
	if s := strings.TrimSpace(f.Search); s != "" {
		args = append(args, "%"+escapeLike(s)+"%")
		where = append(where, fmt.Sprintf("description ILIKE $%d ESCAPE '\\'", len(args)))
	}
	if f.MinAmountCents != nil {
		args = append(args, *f.MinAmountCents)
		where = append(where, fmt.Sprintf("amount_cents >= $%d", len(args)))
	}
	if f.MaxAmountCents != nil {
		args = append(args, *f.MaxAmountCents)
		where = append(where, fmt.Sprintf("amount_cents <= $%d", len(args)))
	}
	// Cursor predicate: tuple comparison against the canonical (occurred_at,
	// id) ordering. Strictly less so the page after the cursor row is
	// returned. The (workspace_id, occurred_at DESC, id) partial index
	// already supports this — no new index needed.
	useCursor := f.CursorOccurredAt != nil && f.CursorID != nil
	if useCursor {
		args = append(args, *f.CursorOccurredAt)
		tsIdx := len(args)
		args = append(args, *f.CursorID)
		idIdx := len(args)
		where = append(where, fmt.Sprintf("(occurred_at, id) < ($%d, $%d)", tsIdx, idIdx))
	}

	args = append(args, f.Limit)
	limitIdx := len(args)

	// The ordering is chosen from a closed set of literals — never from
	// caller text — so no value reaching this function can become SQL.
	orderBy := "occurred_at DESC, id DESC"
	if f.Order == ports.OrderLargest {
		orderBy = "amount_cents DESC, id DESC"
	}

	q := `SELECT ` + txCols + ` FROM finance.transactions
	      WHERE ` + strings.Join(where, " AND ") + `
	      ORDER BY ` + orderBy + `
	      LIMIT $` + fmt.Sprint(limitIdx)
	if !useCursor {
		args = append(args, f.Offset)
		q += ` OFFSET $` + fmt.Sprint(len(args))
	}

	rows, err := postgres.Conn(ctx, r.pool).Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list transactions: %w", err)
	}
	defer rows.Close()
	out := make([]domain.Transaction, 0)
	for rows.Next() {
		t, err := scanTx(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// Totals computes per-status (income/expense), per-category, per-method
// aggregations over a window. Three GROUP BY queries against the existing
// (workspace_id, occurred_at DESC, id) partial index — no joins.
//
// Each query is independent; we don't wrap in a single CTE/snapshot
// because dashboards tolerate ~ms-scale row-count drift across the three
// buckets. Tighten later if it ever matters.
//
// Transfer legs (transfer_pair_id IS NOT NULL) are excluded from every
// bucket. A transfer is money moving between two of the user's own
// accounts — counting either leg as income or expense would inflate the
// overview. The exclusion lives here, not at write time, because the
// underlying rows must still appear in List / FindByID.
func (r *TransactionRepo) Totals(ctx context.Context, workspaceID uuid.UUID, f ports.TransactionTotalsFilter) (ports.TransactionTotals, error) {
	conn := postgres.Conn(ctx, r.pool)
	args := []any{workspaceID, f.From, f.To}

	out := ports.TransactionTotals{
		From:          f.From,
		To:            f.To,
		ByStatus:      make(map[domain.TransactionStatus]ports.TotalsBucket, 3),
		ByRealization: make(map[ports.RealizationBucket]ports.TotalsBucket, 3),
	}
	// Seed all three buckets so callers can always read paid/pending/scheduled.
	for _, s := range []domain.TransactionStatus{
		domain.TransactionStatusPaid,
		domain.TransactionStatusPending,
		domain.TransactionStatusScheduled,
	} {
		out.ByStatus[s] = ports.TotalsBucket{}
	}
	for _, b := range []ports.RealizationBucket{
		ports.RealizationRealized,
		ports.RealizationProjected,
		ports.RealizationExcluded,
	} {
		out.ByRealization[b] = ports.TotalsBucket{}
	}

	// 1) by_status × type.
	const qByStatus = `
		SELECT status, type, COALESCE(SUM(amount_cents), 0), COUNT(*)
		  FROM finance.transactions
		 WHERE workspace_id = $1
		   AND deleted_at IS NULL
		   AND transfer_pair_id IS NULL
		   AND occurred_at >= $2
		   AND occurred_at <  $3
		 GROUP BY status, type`
	rows, err := conn.Query(ctx, qByStatus, args...)
	if err != nil {
		return out, fmt.Errorf("totals by_status: %w", err)
	}
	for rows.Next() {
		var s domain.TransactionStatus
		var ty domain.EntryType
		var amt int64
		var n int64
		if err := rows.Scan(&s, &ty, &amt, &n); err != nil {
			rows.Close()
			return out, fmt.Errorf("scan by_status: %w", err)
		}
		b := out.ByStatus[s]
		if ty == domain.EntryTypeIncome {
			b.IncomeCents += amt
		} else {
			b.ExpenseCents += amt
		}
		b.Count += n
		out.ByStatus[s] = b
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return out, fmt.Errorf("rows by_status: %w", err)
	}
	rows.Close()

	// 1b) by_realization × type — contract semantics (docs/totals-contract.md):
	//     EXCLUDED  = transfer leg (checked first, regardless of status/date)
	//     REALIZED  = status='paid'  AND occurred_at <= now()
	//     PROJECTED = everything else not excluded (pending, scheduled, or
	//                 future-dated paid).
	// `now()` is the Postgres server clock — same source the rest of the
	// table uses for created_at/updated_at, so the classification is
	// consistent within a single request.
	const qByRealization = `
		SELECT CASE
		         WHEN transfer_pair_id IS NOT NULL THEN 'excluded'
		         WHEN status = 'paid' AND occurred_at <= now() THEN 'realized'
		         ELSE 'projected'
		       END AS bucket,
		       type,
		       COALESCE(SUM(amount_cents), 0),
		       COUNT(*)
		  FROM finance.transactions
		 WHERE workspace_id = $1
		   AND deleted_at IS NULL
		   AND occurred_at >= $2
		   AND occurred_at <  $3
		 GROUP BY bucket, type`
	rows, err = conn.Query(ctx, qByRealization, args...)
	if err != nil {
		return out, fmt.Errorf("totals by_realization: %w", err)
	}
	for rows.Next() {
		var bucket string
		var ty domain.EntryType
		var amt int64
		var n int64
		if err := rows.Scan(&bucket, &ty, &amt, &n); err != nil {
			rows.Close()
			return out, fmt.Errorf("scan by_realization: %w", err)
		}
		key := ports.RealizationBucket(bucket)
		b := out.ByRealization[key]
		if ty == domain.EntryTypeIncome {
			b.IncomeCents += amt
		} else {
			b.ExpenseCents += amt
		}
		b.Count += n
		out.ByRealization[key] = b
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return out, fmt.Errorf("rows by_realization: %w", err)
	}
	rows.Close()

	// 2) by_category × type.
	const qByCategory = `
		SELECT category_id, type, COALESCE(SUM(amount_cents), 0), COUNT(*)
		  FROM finance.transactions
		 WHERE workspace_id = $1
		   AND deleted_at IS NULL
		   AND transfer_pair_id IS NULL
		   AND occurred_at >= $2
		   AND occurred_at <  $3
		 GROUP BY category_id, type
		 ORDER BY COALESCE(SUM(amount_cents), 0) DESC`
	rows, err = conn.Query(ctx, qByCategory, args...)
	if err != nil {
		return out, fmt.Errorf("totals by_category: %w", err)
	}
	for rows.Next() {
		var c ports.CategoryTotal
		if err := rows.Scan(&c.CategoryID, &c.Type, &c.TotalCents, &c.Count); err != nil {
			rows.Close()
			return out, fmt.Errorf("scan by_category: %w", err)
		}
		out.ByCategory = append(out.ByCategory, c)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return out, fmt.Errorf("rows by_category: %w", err)
	}
	rows.Close()

	// 3) by_method.
	const qByMethod = `
		SELECT payment_method, COALESCE(SUM(amount_cents), 0), COUNT(*)
		  FROM finance.transactions
		 WHERE workspace_id = $1
		   AND deleted_at IS NULL
		   AND transfer_pair_id IS NULL
		   AND occurred_at >= $2
		   AND occurred_at <  $3
		 GROUP BY payment_method
		 ORDER BY COALESCE(SUM(amount_cents), 0) DESC`
	rows, err = conn.Query(ctx, qByMethod, args...)
	if err != nil {
		return out, fmt.Errorf("totals by_method: %w", err)
	}
	for rows.Next() {
		var m ports.MethodTotal
		if err := rows.Scan(&m.PaymentMethod, &m.TotalCents, &m.Count); err != nil {
			rows.Close()
			return out, fmt.Errorf("scan by_method: %w", err)
		}
		out.ByMethod = append(out.ByMethod, m)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return out, fmt.Errorf("rows by_method: %w", err)
	}
	rows.Close()

	return out, nil
}

// mapFKViolation surfaces FK errors as domain.Invalid because they almost
// always mean "the (category_id, type) pair you sent does not match an
// existing category". We don't differentiate other FKs here because v0.1
// only has the categories FK.
func mapFKViolation(err error) error {
	if isUniqueViolation(err) {
		// Today the only unique constraints a write can hit are the external
		// identity and the plan installment pair. Both mean "this already
		// exists", which is what Conflict says.
		return domain.Conflict("a transaction with this identity already exists")
	}
	if isFKViolation(err) {
		return domain.Invalid("category_id does not exist or type does not match the category")
	}
	return err
}

// escapeLike neutralises the two LIKE metacharacters so a search term is
// matched literally.
//
// The escape character is a backslash, declared explicitly with ESCAPE at
// the call site rather than relying on the server default — the default is
// backslash today and is a setting, and a search that silently stops
// escaping is a filter that silently stops filtering.
func escapeLike(s string) string {
	r := strings.NewReplacer(`\\`, `\\\\`, "%", `\\%`, "_", `\\_`)
	return r.Replace(s)
}
