package repo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/corsi/backend/internal/finance/domain"
	"github.com/corsi/backend/internal/finance/ports"
	"github.com/corsi/backend/internal/platform/postgres"
)

// Recurring occurrences, in Postgres.
//
// ── Where the month lives on the wire ──────────────────────────────────
// `period` is a DATE column holding the month's FIRST DAY, and
// domain.Period is a year and a month with no zone at all. The crossing
// happens in exactly two places in this file: Period.FirstDay() on the way
// down, and domain.PeriodOf(t, time.UTC) on the way back.
//
// UTC on the way back is not a reporting decision and must not be read as
// one. A DATE carries no instant, pgx hands it over as midnight UTC, and
// reading those Y/M/D components in any other zone would move some of them
// to the previous month. The reporting zone decides which month a caller
// ASKS for; it has no business in how that month is spelled in storage.
type RecurringOccurrenceRepo struct {
	pool *pgxpool.Pool
}

// The port and the adapter, checked by the compiler.
//
// The other repositories in this package predate the ports file and are
// held by the service as concrete types, so nothing forces them to agree
// with their interfaces. This one says so out loud: the port is where the
// contract is argued, and a signature that drifts from it should fail a
// build rather than a review.
var _ ports.RecurringOccurrenceRepo = (*RecurringOccurrenceRepo)(nil)

func NewRecurringOccurrenceRepo(pool *pgxpool.Pool) *RecurringOccurrenceRepo {
	return &RecurringOccurrenceRepo{pool: pool}
}

// The columns, qualified, because every read joins the definition.
const occurrenceCols = `o.id, o.workspace_id, o.recurring_entry_id, o.period, o.due_on,
                        o.amount_cents, o.amount_estimated, o.status, o.paid_at,
                        o.transaction_id, o.origin, o.created_at, o.updated_at`

func scanOccurrence(row pgx.Row) (*domain.RecurringOccurrence, error) {
	var (
		o      domain.RecurringOccurrence
		period time.Time
	)
	if err := row.Scan(&o.ID, &o.WorkspaceID, &o.RecurringEntryID, &period, &o.DueOn,
		&o.AmountCents, &o.AmountEstimated, &o.Status, &o.PaidAt,
		&o.TransactionID, &o.Origin, &o.CreatedAt, &o.UpdatedAt); err != nil {
		return nil, err
	}
	o.Period = domain.PeriodOf(period, time.UTC)
	return &o, nil
}

// ListByPeriod reads one month.
//
// The join through `recurring_entries` is not decoration: it is what makes
// a soft-deleted definition take its months out of every reading at once,
// rather than leaving each caller to remember a filter it cannot see the
// need for.
func (r *RecurringOccurrenceRepo) ListByPeriod(
	ctx context.Context, workspaceID uuid.UUID, p domain.Period,
) ([]domain.RecurringOccurrence, error) {
	if p.IsZero() {
		return nil, domain.Invalid("period required")
	}
	q := `SELECT ` + occurrenceCols + `
	        FROM finance.recurring_occurrences o
	        JOIN finance.recurring_entries e
	          ON e.id = o.recurring_entry_id AND e.deleted_at IS NULL
	       WHERE o.workspace_id = $1 AND o.period = $2
	       ORDER BY o.due_on, o.id`
	rows, err := postgres.Conn(ctx, r.pool).Query(ctx, q, workspaceID, p.FirstDay())
	if err != nil {
		return nil, fmt.Errorf("list recurring occurrences: %w", err)
	}
	defer rows.Close()
	out := make([]domain.RecurringOccurrence, 0)
	for rows.Next() {
		o, err := scanOccurrence(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *o)
	}
	return out, rows.Err()
}

// EnsureMissing inserts what is absent, in ONE statement.
//
// ── Why one statement with arrays rather than a loop ───────────────────
// Because a loop is N round trips and, worse, N chances to be interrupted
// halfway: a month half materialised is a month whose total is wrong and
// looks right. One statement either lands or does not, and it participates
// in whatever transaction the caller opened.
//
// ── Why the conflict target is the pair and not the id ─────────────────
// The id is fresh on every attempt, so it can never conflict. The identity
// of an occurrence is (definition, month), which is what the unique index
// is on, and what makes the second concurrent writer a no-op.
func (r *RecurringOccurrenceRepo) EnsureMissing(
	ctx context.Context, occ []domain.RecurringOccurrence,
) (int, error) {
	if len(occ) == 0 {
		return 0, nil
	}

	n := len(occ)
	ids := make([]uuid.UUID, n)
	workspaces := make([]uuid.UUID, n)
	entries := make([]uuid.UUID, n)
	periods := make([]time.Time, n)
	dueOn := make([]time.Time, n)
	amounts := make([]int64, n)
	estimated := make([]bool, n)
	statuses := make([]string, n)
	origins := make([]string, n)

	for i := range occ {
		o := &occ[i]
		// Validated here rather than trusted, because this is the one write
		// path that takes a slice: a single malformed element would
		// otherwise be diagnosed as a constraint violation on a statement
		// carrying dozens of rows, with nothing saying which.
		if err := o.Validate(); err != nil {
			return 0, err
		}
		if o.ID == uuid.Nil {
			o.ID = uuid.New()
		}
		ids[i] = o.ID
		workspaces[i] = o.WorkspaceID
		entries[i] = o.RecurringEntryID
		periods[i] = o.Period.FirstDay()
		dueOn[i] = o.DueOn
		amounts[i] = o.AmountCents
		estimated[i] = o.AmountEstimated
		statuses[i] = string(o.Status)
		origins[i] = string(o.Origin)
	}

	q := `INSERT INTO finance.recurring_occurrences
	        (id, workspace_id, recurring_entry_id, period, due_on,
	         amount_cents, amount_estimated, status, origin)
	      SELECT * FROM unnest(
	        $1::uuid[], $2::uuid[], $3::uuid[], $4::date[], $5::date[],
	        $6::bigint[], $7::boolean[],
	        $8::text[]::finance.occurrence_status[],
	        $9::text[]::finance.occurrence_origin[])
	      ON CONFLICT (recurring_entry_id, period) DO NOTHING`

	tag, err := postgres.Conn(ctx, r.pool).Exec(ctx, q,
		ids, workspaces, entries, periods, dueOn, amounts, estimated, statuses, origins)
	if err != nil {
		if isFKViolation(err) {
			return 0, domain.Invalid("recurring_entry_id does not exist")
		}
		return 0, fmt.Errorf("materialise recurring occurrences: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// FindByEntryPeriod resolves one month of one definition.
func (r *RecurringOccurrenceRepo) FindByEntryPeriod(
	ctx context.Context, workspaceID, entryID uuid.UUID, p domain.Period,
) (*domain.RecurringOccurrence, error) {
	if p.IsZero() {
		return nil, domain.Invalid("period required")
	}
	q := `SELECT ` + occurrenceCols + `
	        FROM finance.recurring_occurrences o
	        JOIN finance.recurring_entries e
	          ON e.id = o.recurring_entry_id AND e.deleted_at IS NULL
	       WHERE o.workspace_id = $1 AND o.recurring_entry_id = $2 AND o.period = $3`
	o, err := scanOccurrence(postgres.Conn(ctx, r.pool).QueryRow(ctx, q, workspaceID, entryID, p.FirstDay()))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.NotFound("recurring occurrence")
	}
	return o, err
}

// Update writes the mutable part of one occurrence.
//
// ── What the statement deliberately does not SET ───────────────────────
// `period`, `due_on` and `recurring_entry_id`. Those are the row's
// identity and its frozen due date. Leaving them out of the statement
// means a caller that modified them on the struct, by accident or by a
// handler that copied a request body over a loaded row, changes nothing --
// which is a stronger guarantee than a rule somebody has to remember, and
// it is the same reason the amount is frozen in the first place.
func (r *RecurringOccurrenceRepo) Update(ctx context.Context, o *domain.RecurringOccurrence) error {
	if err := o.Validate(); err != nil {
		return err
	}
	q := `UPDATE finance.recurring_occurrences
	         SET amount_cents = $3, amount_estimated = $4, status = $5,
	             paid_at = $6, transaction_id = $7, updated_at = now()
	       WHERE id = $1 AND workspace_id = $2
	   RETURNING id, workspace_id, recurring_entry_id, period, due_on,
	             amount_cents, amount_estimated, status, paid_at,
	             transaction_id, origin, created_at, updated_at`
	var (
		got    domain.RecurringOccurrence
		period time.Time
	)
	err := postgres.Conn(ctx, r.pool).QueryRow(ctx, q,
		o.ID, o.WorkspaceID, o.AmountCents, o.AmountEstimated, o.Status,
		o.PaidAt, o.TransactionID,
	).Scan(&got.ID, &got.WorkspaceID, &got.RecurringEntryID, &period, &got.DueOn,
		&got.AmountCents, &got.AmountEstimated, &got.Status, &got.PaidAt,
		&got.TransactionID, &got.Origin, &got.CreatedAt, &got.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.NotFound("recurring occurrence")
		}
		return fmt.Errorf("update recurring occurrence: %w", err)
	}
	got.Period = domain.PeriodOf(period, time.UTC)
	*o = got
	return nil
}

// ApplyDefinition carries an edited recurrence into one PENDING month.
//
// The only statement in this package that writes `due_on`, and the reason
// Update does not: see the port. The `status = 'pending'` predicate is a
// second guard, independent of the application's own check, so a settled
// month cannot be rewritten even by a caller that forgot to look.
//
// A miss is reported as a CONFLICT rather than a not-found, because by the
// time this runs the row has already been resolved: the only way to match
// nothing is for it to have been settled in between.
func (r *RecurringOccurrenceRepo) ApplyDefinition(ctx context.Context, o *domain.RecurringOccurrence) error {
	if err := o.Validate(); err != nil {
		return err
	}
	if o.Status != domain.OccurrencePending {
		return domain.Conflict("only a pending month takes an edited recurrence")
	}
	q := `UPDATE finance.recurring_occurrences
	         SET amount_cents = $3, amount_estimated = $4, due_on = $5, updated_at = now()
	       WHERE id = $1 AND workspace_id = $2 AND status = 'pending'
	   RETURNING id, workspace_id, recurring_entry_id, period, due_on,
	             amount_cents, amount_estimated, status, paid_at,
	             transaction_id, origin, created_at, updated_at`
	var (
		got    domain.RecurringOccurrence
		period time.Time
	)
	err := postgres.Conn(ctx, r.pool).QueryRow(ctx, q,
		o.ID, o.WorkspaceID, o.AmountCents, o.AmountEstimated, o.DueOn,
	).Scan(&got.ID, &got.WorkspaceID, &got.RecurringEntryID, &period, &got.DueOn,
		&got.AmountCents, &got.AmountEstimated, &got.Status, &got.PaidAt,
		&got.TransactionID, &got.Origin, &got.CreatedAt, &got.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Conflict("this month is no longer pending, so it did not take the change")
		}
		return fmt.Errorf("apply definition to occurrence: %w", err)
	}
	got.Period = domain.PeriodOf(period, time.UTC)
	*o = got
	return nil
}
