package repo

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/corsi/backend/internal/finance/domain"
	"github.com/corsi/backend/internal/finance/ports"
	"github.com/corsi/backend/internal/platform/postgres"
)

type ImportBatchRepo struct {
	pool *pgxpool.Pool
}

func NewImportBatchRepo(pool *pgxpool.Pool) *ImportBatchRepo {
	return &ImportBatchRepo{pool: pool}
}

const batchCols = `id, workspace_id, source_label, account_scope, import_source_id,
	format, status, content_sha256, row_count, created_count, skipped_count,
	created_at, committed_at`

func scanBatch(row pgx.Row) (*domain.ImportBatch, error) {
	var b domain.ImportBatch
	err := row.Scan(&b.ID, &b.WorkspaceID, &b.SourceLabel, &b.AccountScope, &b.ImportSourceID,
		&b.Format, &b.Status, &b.ContentSHA256, &b.RowCount, &b.CreatedCount, &b.SkippedCount,
		&b.CreatedAt, &b.CommittedAt)
	if err != nil {
		return nil, err
	}
	return &b, nil
}

func (r *ImportBatchRepo) CreateBatch(ctx context.Context, b *domain.ImportBatch) error {
	row := postgres.Conn(ctx, r.pool).QueryRow(ctx, `
		INSERT INTO finance.import_batches
		  (id, workspace_id, source_label, account_scope, import_source_id,
		   format, content_sha256, row_count)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		RETURNING `+batchCols,
		b.ID, b.WorkspaceID, b.SourceLabel, b.AccountScope, b.ImportSourceID,
		b.Format, b.ContentSHA256, b.RowCount)
	got, err := scanBatch(row)
	if err != nil {
		return err
	}
	*b = *got
	return nil
}

func (r *ImportBatchRepo) FindBatch(ctx context.Context, workspaceID, id uuid.UUID) (*domain.ImportBatch, error) {
	// Workspace in the predicate, not checked after the read: a batch from
	// another workspace must be indistinguishable from one that never
	// existed, or the 404 becomes an existence oracle.
	row := postgres.Conn(ctx, r.pool).QueryRow(ctx,
		`SELECT `+batchCols+` FROM finance.import_batches WHERE id=$1 AND workspace_id=$2`, id, workspaceID)
	b, err := scanBatch(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.NotFound("import batch not found")
		}
		return nil, err
	}
	return b, nil
}

func (r *ImportBatchRepo) MarkCommitted(ctx context.Context, workspaceID, id uuid.UUID, created, skipped int) error {
	ct, err := postgres.Conn(ctx, r.pool).Exec(ctx, `
		UPDATE finance.import_batches
		   SET status='committed', committed_at=now(), created_count=$3, skipped_count=$4
		 WHERE id=$1 AND workspace_id=$2 AND status='prepared'`,
		id, workspaceID, created, skipped)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		// Either it does not exist here, or it was already committed. Both
		// are refusals; neither says which.
		return domain.Conflict("import batch is not awaiting commit")
	}
	return nil
}

func (r *ImportBatchRepo) InsertRows(ctx context.Context, rows []domain.ImportRow) error {
	if len(rows) == 0 {
		return nil
	}
	// One statement for the whole file, via unnest.
	//
	// CopyFrom would be the faster tool and is deliberately not used: it is
	// not on postgres.Querier, so it would open its own connection and
	// escape any transaction the caller had started. A staging write that
	// survived a rolled-back prepare would leave rows nobody can reconcile.
	n := len(rows)
	ids := make([]uuid.UUID, n)
	batchIDs := make([]uuid.UUID, n)
	wsIDs := make([]uuid.UUID, n)
	lineNos := make([]int32, n)
	occurred := make([]*time.Time, n)
	amounts := make([]*int64, n)
	descs := make([]string, n)
	dirs := make([]*string, n)
	groups := make([]string, n)
	states := make([]string, n)
	details := make([]string, n)
	strategies := make([]*string, n)
	values := make([]*string, n)
	// uuid.UUID has a value-receiver Valuer, so a nil *uuid.UUID panics
	// inside pgx rather than encoding as NULL. Strings with an explicit
	// cast keep the NULL representable.
	cats := make([]*string, n)

	for i, x := range rows {
		ids[i], batchIDs[i], wsIDs[i] = x.ID, x.BatchID, x.WorkspaceID
		lineNos[i] = int32(x.LineNumber)
		occurred[i], amounts[i] = x.OccurredAt, x.AmountCents
		descs[i], groups[i] = x.Description, x.GroupKey
		states[i], details[i] = string(x.State), x.StateDetail
		values[i] = x.IdentityValue
		if x.CategoryID != nil {
			c := x.CategoryID.String()
			cats[i] = &c
		}
		if x.Direction != nil {
			d := string(*x.Direction)
			dirs[i] = &d
		}
		if x.IdentityStrategy != nil {
			st := string(*x.IdentityStrategy)
			strategies[i] = &st
		}
	}

	_, err := postgres.Conn(ctx, r.pool).Exec(ctx, `
		INSERT INTO finance.import_rows
		  (id, batch_id, workspace_id, line_number, occurred_at, amount_cents,
		   description, direction, group_key, state, state_detail,
		   identity_strategy, identity_value, category_id)
		SELECT * FROM unnest(
		  $1::uuid[], $2::uuid[], $3::uuid[], $4::int[], $5::timestamptz[], $6::bigint[],
		  $7::text[], $8::finance.entry_type[], $9::text[], $10::finance.import_row_state[],
		  $11::text[], $12::finance.import_identity_strategy[], $13::text[], $14::uuid[])`,
		ids, batchIDs, wsIDs, lineNos, occurred, amounts, descs, dirs, groups,
		states, details, strategies, values, cats)
	return err
}

func (r *ImportBatchRepo) ListRows(ctx context.Context, workspaceID, batchID uuid.UUID) ([]domain.ImportRow, error) {
	rows, err := postgres.Conn(ctx, r.pool).Query(ctx, `
		SELECT id, batch_id, workspace_id, line_number, occurred_at, amount_cents,
		       description, direction, group_key, state, state_detail,
		       identity_strategy, identity_value, category_id, transaction_id
		  FROM finance.import_rows
		 WHERE batch_id=$1 AND workspace_id=$2
		 ORDER BY line_number`, batchID, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.ImportRow{}
	for rows.Next() {
		var x domain.ImportRow
		if err := rows.Scan(&x.ID, &x.BatchID, &x.WorkspaceID, &x.LineNumber, &x.OccurredAt,
			&x.AmountCents, &x.Description, &x.Direction, &x.GroupKey, &x.State,
			&x.StateDetail, &x.IdentityStrategy, &x.IdentityValue, &x.CategoryID,
			&x.TransactionID); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (r *ImportBatchRepo) Reconcile(ctx context.Context, workspaceID, batchID uuid.UUID) (domain.ImportReconciliation, error) {
	var rec domain.ImportReconciliation
	err := postgres.Conn(ctx, r.pool).QueryRow(ctx, `
		SELECT
		  (SELECT row_count FROM finance.import_batches WHERE id=$1 AND workspace_id=$2),
		  count(*) FILTER (WHERE state='ready'),
		  count(*) FILTER (WHERE state='duplicate'),
		  count(*) FILTER (WHERE state='ambiguous_duplicate'),
		  count(*) FILTER (WHERE state='unresolved_category'),
		  count(*) FILTER (WHERE state='ambiguous_internal'),
		  count(*) FILTER (WHERE state='invalid')
		FROM finance.import_rows WHERE batch_id=$1 AND workspace_id=$2`,
		batchID, workspaceID).
		Scan(&rec.RowCount, &rec.Ready, &rec.Duplicate, &rec.AmbiguousDuplicate,
			&rec.UnresolvedCategory, &rec.AmbiguousInternal, &rec.Invalid)
	if err != nil {
		return rec, err
	}
	return rec, nil
}

func (r *ImportBatchRepo) UnresolvedGroups(ctx context.Context, workspaceID, batchID uuid.UUID, limit int) ([]ports.ImportGroup, error) {
	rows, err := postgres.Conn(ctx, r.pool).Query(ctx, `
		SELECT group_key, count(*), min(description), min(direction::text)
		  FROM finance.import_rows
		 WHERE batch_id=$1 AND workspace_id=$2 AND state='unresolved_category'
		 GROUP BY group_key
		 ORDER BY count(*) DESC, group_key
		 LIMIT $3`, batchID, workspaceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ports.ImportGroup{}
	for rows.Next() {
		var g ports.ImportGroup
		var dir string
		if err := rows.Scan(&g.GroupKey, &g.Rows, &g.Sample, &dir); err != nil {
			return nil, err
		}
		g.Direction = domain.EntryType(dir)
		out = append(out, g)
	}
	return out, rows.Err()
}

func (r *ImportBatchRepo) ResolveGroup(ctx context.Context, workspaceID, batchID uuid.UUID, groupKey string, categoryID uuid.UUID) (int64, error) {
	// The category is resolved through a subquery scoped to the SAME
	// workspace, so a category id belonging to somebody else cannot be
	// attached to these rows even if the caller names it correctly.
	ct, err := postgres.Conn(ctx, r.pool).Exec(ctx, `
		UPDATE finance.import_rows r
		   SET category_id = c.id, state = 'ready', state_detail = '', updated_at = now()
		  FROM finance.categories c
		 WHERE r.batch_id = $1
		   AND r.workspace_id = $2
		   AND r.group_key = $3
		   AND r.state = 'unresolved_category'
		   AND c.id = $4
		   AND c.workspace_id = $2
		   AND c.deleted_at IS NULL
		   AND c.type = r.direction`,
		batchID, workspaceID, groupKey, categoryID)
	if err != nil {
		return 0, err
	}
	return ct.RowsAffected(), nil
}

// MaterialiseReady is the whole import, as one statement.
//
// ── Why INSERT ... SELECT and not a loop ───────────────────────────────
// A loop of 150 inserts is 150 round trips inside a 10 second tool
// timeout, and every one of them is a place for the set to be half
// written. This is one statement inside one transaction: either the ledger
// gained these rows or it gained none.
//
// ── Why ON CONFLICT DO NOTHING is not "silent" ─────────────────────────
// The conflict target is the external identity index, and a conflict here
// means another import committed the same identity between this batch's
// preview and its commit. The rows that lost are not discarded quietly:
// they are the difference between eligible and created, reported as
// skipped_concurrent, and their staging rows keep a NULL transaction_id
// which is exactly the evidence that they did not enter.
func (r *ImportBatchRepo) MaterialiseReady(ctx context.Context, workspaceID, batchID uuid.UUID, source string) (int, error) {
	conn := postgres.Conn(ctx, r.pool)
	rows, err := conn.Query(ctx, `
		WITH eligible AS (
		    SELECT r.id AS row_id, r.occurred_at, r.amount_cents, r.description,
		           r.direction, r.category_id, r.identity_value
		      FROM finance.import_rows r
		     WHERE r.batch_id = $1 AND r.workspace_id = $2 AND r.state = 'ready'
		       AND r.transaction_id IS NULL
		), inserted AS (
		    INSERT INTO finance.transactions
		      (workspace_id, category_id, type, amount_cents, description, occurred_at,
		       status, payment_method, source, external_source, external_id)
		    SELECT $2, e.category_id, e.direction, e.amount_cents, e.description,
		           e.occurred_at, 'paid', 'pix', 'import', $3, e.identity_value
		      FROM eligible e
		    ON CONFLICT (workspace_id, external_source, external_id)
		      WHERE external_id IS NOT NULL AND deleted_at IS NULL
		      DO NOTHING
		    RETURNING id, external_id
		)
		UPDATE finance.import_rows r
		   SET transaction_id = i.id, updated_at = now()
		  FROM inserted i
		 WHERE r.batch_id = $1 AND r.workspace_id = $2 AND r.identity_value = i.external_id
		RETURNING r.id`, batchID, workspaceID, source)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		n++
	}
	return n, rows.Err()
}

func (r *ImportBatchRepo) ExistingIdentities(ctx context.Context, workspaceID uuid.UUID, source string, ids []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(ids) == 0 {
		return out, nil
	}
	// One query for the whole file. Deliberately scoped to this workspace
	// AND this source: dedup never reaches across a workspace boundary, and
	// an identity minted by another institution is a different namespace.
	rows, err := postgres.Conn(ctx, r.pool).Query(ctx, `
		SELECT external_id FROM finance.transactions
		 WHERE workspace_id=$1 AND external_source=$2 AND deleted_at IS NULL
		   AND external_id = ANY($3)`, workspaceID, source, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// LatestPrepared answers "the batch we were just talking about".
//
// Scoped to the workspace and to status='prepared', ordered by creation.
// It is a convenience for the caller, never a widening of access: a batch
// belonging to somebody else is not reachable through it, because the
// predicate is the same one FindBatch uses.
func (r *ImportBatchRepo) LatestPrepared(ctx context.Context, workspaceID uuid.UUID) (*domain.ImportBatch, error) {
	row := postgres.Conn(ctx, r.pool).QueryRow(ctx,
		`SELECT `+batchCols+` FROM finance.import_batches
		  WHERE workspace_id=$1 AND status='prepared'
		  ORDER BY created_at DESC LIMIT 1`, workspaceID)
	b, err := scanBatch(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.NotFound("no prepared statement is waiting in this workspace")
		}
		return nil, err
	}
	return b, nil
}

// GroupAlreadyResolvedTo reports whether every row of a group has already
// been assigned this exact category.
//
// It exists so ResolveImportGroup can be idempotent. A model that repeats
// a classification it already made is not making an error, and answering
// it with a failure taught one to conclude the batch had disappeared.
func (r *ImportBatchRepo) GroupAlreadyResolvedTo(ctx context.Context, workspaceID, batchID uuid.UUID, groupKey string, categoryID uuid.UUID) (bool, error) {
	var total, matching int
	err := postgres.Conn(ctx, r.pool).QueryRow(ctx, `
		SELECT count(*), count(*) FILTER (WHERE category_id = $4 AND state = 'ready')
		  FROM finance.import_rows
		 WHERE batch_id = $1 AND workspace_id = $2 AND group_key = $3`,
		batchID, workspaceID, groupKey, categoryID).Scan(&total, &matching)
	if err != nil {
		return false, err
	}
	return total > 0 && total == matching, nil
}

// FindPreparedByContent returns the batch already staged for this exact
// statement, if one is still awaiting a decision.
//
// ── Why re-preparing must not create a second batch ────────────────────
// Because a model re-derives its state every turn: finance capabilities
// are Confidential, so the audit rows a later turn's evidence is built
// from carry no result, and nothing tells it that it already prepared and
// classified this file. Left alone, each turn stacks another batch and
// throws away the classifications made in the previous one, and with three
// tool rounds per turn the commit is never reached.
//
// Keyed on the content hash and the account scope, both of which the
// operator's own statement determines, so "the same file again" is a fact
// rather than a guess.
func (r *ImportBatchRepo) FindPreparedByContent(ctx context.Context, workspaceID uuid.UUID, accountScope, sha string) (*domain.ImportBatch, error) {
	row := postgres.Conn(ctx, r.pool).QueryRow(ctx,
		`SELECT `+batchCols+` FROM finance.import_batches
		  WHERE workspace_id=$1 AND account_scope=$2 AND content_sha256=$3 AND status='prepared'
		  ORDER BY created_at DESC LIMIT 1`, workspaceID, accountScope, sha)
	b, err := scanBatch(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return b, nil
}
