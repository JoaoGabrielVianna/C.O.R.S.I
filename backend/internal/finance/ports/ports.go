// Package ports declares the boundary interfaces of the finance bounded
// context.
//
// Driven (outbound) ports: the contracts the application layer requires
// from infrastructure. Implementations live under internal/finance/adapters.
//
// Each repository operates per-workspace; every query takes a workspace id
// and filters server-side. Soft-deleted rows are filtered out by default —
// hard reads of archived rows are not part of v0.1.
package ports

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/finance/domain"
)

// Clock is the authority on the current instant for this bounded context.
//
// It exists because the totals contract classifies REALIZED against `now()`
// evaluated by Postgres. A caller that stamps a transaction from its own
// process clock introduces a second authority, and the disagreement — even
// a few hundred milliseconds of it — files a payment made a moment ago as
// PROJECTED. See adapters/repo/clock.go for the failure in full.
type Clock interface {
	Now(ctx context.Context) (time.Time, error)
}

type CategoryFilter struct {
	Type   *domain.EntryType
	Limit  int
	Offset int
}

type CategoryRepo interface {
	Create(ctx context.Context, c *domain.Category) error
	Update(ctx context.Context, c *domain.Category) error
	SoftDelete(ctx context.Context, workspaceID, id uuid.UUID) error
	FindByID(ctx context.Context, workspaceID, id uuid.UUID) (*domain.Category, error)
	List(ctx context.Context, workspaceID uuid.UUID, f CategoryFilter) ([]domain.Category, error)
}

// TransactionOrder names the orderings the list query supports.
//
// ── Why an ordering is a filter concern and not a caller concern ───────
// Because "my largest expenses" is a question about the whole matching set,
// not about the page. A caller that could only read the most recent rows
// would have to page the entire window into memory and sort it there, which
// is the exact shape this module refuses everywhere else: an aggregation
// done by whoever asked, over data they had to download first.
//
// The zero value is OrderRecent, so every caller written before this type
// existed keeps the ordering it already had.
type TransactionOrder string

const (
	// OrderRecent: (occurred_at DESC, id DESC). The canonical feed order,
	// and the one the keyset cursor encodes.
	OrderRecent TransactionOrder = ""
	// OrderLargest: (amount_cents DESC, id DESC). Answers "what were the
	// biggest ones" without reading the window.
	//
	// Cursor paging is NOT available in this ordering: the cursor tuple is
	// (occurred_at, id), which does not identify a position in an
	// amount-ordered sequence. The application layer refuses the
	// combination rather than returning a page that silently skips rows.
	OrderLargest TransactionOrder = "largest"
)

func (o TransactionOrder) Valid() bool {
	return o == OrderRecent || o == OrderLargest
}

type TransactionFilter struct {
	Type       *domain.EntryType
	CategoryID *uuid.UUID
	Status     *domain.TransactionStatus
	From       *time.Time
	To         *time.Time
	// Search matches the description, case-insensitively, as a substring.
	// Empty means no text predicate.
	//
	// Description only, and deliberately: notes are the field a person uses
	// for things they did not want on the row itself, and widening a search
	// into them would make a query for "mercado" return rows whose only
	// connection is a private annotation.
	Search string
	// MinAmountCents / MaxAmountCents bound the amount, inclusive. Both nil
	// means no bound. They are int64 cents like every other amount in this
	// context — there is no second money type at this boundary.
	MinAmountCents *int64
	MaxAmountCents *int64
	Order          TransactionOrder
	Limit          int
	Offset         int

	// CursorOccurredAt + CursorID, when both set, switch the list query
	// to `(occurred_at, id) < (CursorOccurredAt, CursorID)` instead of
	// applying Offset. Cursor + Offset are mutually exclusive — the HTTP
	// layer is responsible for rejecting requests that send both.
	CursorOccurredAt *time.Time
	CursorID         *uuid.UUID
}

type TransactionTotalsFilter struct {
	From time.Time
	To   time.Time
}

// TotalsBucket aggregates income + expense within one status bucket.
type TotalsBucket struct {
	IncomeCents  int64
	ExpenseCents int64
	Count        int64
}

type CategoryTotal struct {
	CategoryID uuid.UUID
	Type       domain.EntryType
	TotalCents int64
	Count      int64
}

type MethodTotal struct {
	PaymentMethod domain.PaymentMethod
	TotalCents    int64
	Count         int64
}

// RealizationBucket names the contract-level buckets defined in
// docs/totals-contract.md (REALIZED / PROJECTED / EXCLUDED). It is the
// classification dashboards must consume; raw `status` is for filters only.
type RealizationBucket string

const (
	RealizationRealized  RealizationBucket = "realized"
	RealizationProjected RealizationBucket = "projected"
	RealizationExcluded  RealizationBucket = "excluded"
)

// TransactionTotals carries dashboard-grade aggregations over a window.
// Computed server-side so callers don't have to fetch every row.
//
// ByStatus groups by raw status — kept for back-compat with filter-driven
// callers. ByRealization is the canonical, contract-defined grouping every
// new consumer must use (see docs/totals-contract.md): realized/projected
// classify by (status, occurred_at vs now); excluded captures transfer
// legs so dashboards never double-count account-to-account moves.
type TransactionTotals struct {
	From          time.Time
	To            time.Time
	ByStatus      map[domain.TransactionStatus]TotalsBucket
	ByRealization map[RealizationBucket]TotalsBucket
	ByCategory    []CategoryTotal
	ByMethod      []MethodTotal
}

type TransactionRepo interface {
	Create(ctx context.Context, t *domain.Transaction) error
	Update(ctx context.Context, t *domain.Transaction) error
	SoftDelete(ctx context.Context, workspaceID, id uuid.UUID) error
	// SoftDeleteByTransferPair soft-deletes every (still-live) leg sharing
	// the given pair_id and returns how many rows were touched. The caller
	// is responsible for wrapping the call in a tx and for treating a zero
	// count as not_found.
	SoftDeleteByTransferPair(ctx context.Context, workspaceID, pairID uuid.UUID) (int64, error)
	FindByID(ctx context.Context, workspaceID, id uuid.UUID) (*domain.Transaction, error)
	List(ctx context.Context, workspaceID uuid.UUID, f TransactionFilter) ([]domain.Transaction, error)
	Totals(ctx context.Context, workspaceID uuid.UUID, f TransactionTotalsFilter) (TransactionTotals, error)
}

type CardFilter struct {
	Limit  int
	Offset int
}

type CardRepo interface {
	Create(ctx context.Context, c *domain.Card) error
	Update(ctx context.Context, c *domain.Card) error
	Archive(ctx context.Context, workspaceID, id uuid.UUID) error
	FindByID(ctx context.Context, workspaceID, id uuid.UUID) (*domain.Card, error)
	List(ctx context.Context, workspaceID uuid.UUID, f CardFilter) ([]domain.Card, error)
}

type PersonFilter struct {
	Limit  int
	Offset int
}

type PersonRepo interface {
	Create(ctx context.Context, p *domain.Person) error
	Update(ctx context.Context, p *domain.Person) error
	SoftDelete(ctx context.Context, workspaceID, id uuid.UUID) error
	FindByID(ctx context.Context, workspaceID, id uuid.UUID) (*domain.Person, error)
	List(ctx context.Context, workspaceID uuid.UUID, f PersonFilter) ([]domain.Person, error)
}

type PurchasePlanFilter struct {
	Limit  int
	Offset int
}

type PurchasePlanRepo interface {
	// Create inserts the parent plan row. The N installment rows are
	// inserted by the caller via TransactionRepo inside the same tx so
	// the create operation is atomic — either the plan AND all
	// installments land, or neither does.
	Create(ctx context.Context, p *domain.PurchasePlan) error
	FindByID(ctx context.Context, workspaceID, id uuid.UUID) (*domain.PurchasePlan, error)
	List(ctx context.Context, workspaceID uuid.UUID, f PurchasePlanFilter) ([]domain.PurchasePlan, error)
	// CancelFutureInstallments soft-deletes every live installment row
	// of the given plan whose status is `scheduled` (i.e. not yet paid),
	// then updates remaining_installments to reflect the live count. It
	// returns how many installment rows were soft-deleted. A return of
	// zero is treated as "nothing to cancel" by the caller; the plan
	// itself is not soft-deleted (history must be reachable).
	CancelFutureInstallments(ctx context.Context, workspaceID, planID uuid.UUID) (int64, error)
}

/* ── recurring entries ───────────────────────────────────────────────── */

// RecurringEntryFilter narrows a listing of recurring entries.
//
// ActiveOnly false lists everything live, true lists only what is running
// now. The distinction matters because a paused or ended entry is still a
// record worth reading — "quais eu cancelei" is a real question — while
// "quanto entra e sai por mês" must not count it.
type RecurringEntryFilter struct {
	CategoryID *uuid.UUID
	ActiveOnly bool
	Limit      int
	Offset     int
}

type RecurringEntryRepo interface {
	Create(ctx context.Context, f *domain.RecurringEntry) error
	Update(ctx context.Context, f *domain.RecurringEntry) error
	SoftDelete(ctx context.Context, workspaceID, id uuid.UUID) error
	FindByID(ctx context.Context, workspaceID, id uuid.UUID) (*domain.RecurringEntry, error)
	List(ctx context.Context, workspaceID uuid.UUID, f RecurringEntryFilter) ([]domain.RecurringEntry, error)
}

// ── Statement import ───────────────────────────────────────────────────

// ImportBatchRepo persists the staging area. Nothing it writes is visible
// to any financial read: a batch is work in progress, not a ledger entry.
type ImportBatchRepo interface {
	CreateBatch(ctx context.Context, b *domain.ImportBatch) error
	FindBatch(ctx context.Context, workspaceID, id uuid.UUID) (*domain.ImportBatch, error)
	MarkCommitted(ctx context.Context, workspaceID, id uuid.UUID, created, skipped int) error

	InsertRows(ctx context.Context, rows []domain.ImportRow) error
	ListRows(ctx context.Context, workspaceID, batchID uuid.UUID) ([]domain.ImportRow, error)
	Reconcile(ctx context.Context, workspaceID, batchID uuid.UUID) (domain.ImportReconciliation, error)

	// UnresolvedGroups reports the distinct normalised descriptions still
	// waiting for a category, with how many lines each covers, so the model
	// can propose a small number of categories instead of 150.
	UnresolvedGroups(ctx context.Context, workspaceID, batchID uuid.UUID, limit int) ([]ImportGroup, error)

	// ResolveGroup assigns a category to every unresolved row in one group
	// and moves them to ready. Returns how many rows moved.
	ResolveGroup(ctx context.Context, workspaceID, batchID uuid.UUID, groupKey string, categoryID uuid.UUID) (int64, error)

	// MaterialiseReady inserts the ready rows into finance.transactions in
	// ONE set-based statement, skipping anything whose external identity is
	// already taken, and links each staged row to the transaction it became.
	MaterialiseReady(ctx context.Context, workspaceID, batchID uuid.UUID, source string) (created int, err error)

	// ExistingStrongIDs and ExistingHeuristicIDs answer "which of these
	// identities does the ledger already hold?" in one round trip rather
	// than one query per line.
	ExistingIdentities(ctx context.Context, workspaceID uuid.UUID, source string, ids []string) (map[string]bool, error)

	// LatestPrepared is the most recent batch still awaiting a decision in
	// this workspace. It exists because a 36-character id has to survive
	// several conversational turns inside a model's context, and it does
	// not: the first live run of this flow ended with a fabricated uuid.
	LatestPrepared(ctx context.Context, workspaceID uuid.UUID) (*domain.ImportBatch, error)

	// GroupAlreadyResolvedTo makes resolution idempotent. Repeating a
	// classification is not an error, and answering one with a failure
	// taught a live model to conclude its batch had vanished.
	GroupAlreadyResolvedTo(ctx context.Context, workspaceID, batchID uuid.UUID, groupKey string, categoryID uuid.UUID) (bool, error)

	// FindPreparedByContent returns nil, nil when this statement is not
	// already staged. Re-preparing the same file must reuse the batch, or a
	// model that re-derives its state each turn discards its own work.
	FindPreparedByContent(ctx context.Context, workspaceID uuid.UUID, accountScope, sha string) (*domain.ImportBatch, error)
}

// ImportGroup is one normalised description awaiting a category.
type ImportGroup struct {
	GroupKey  string
	Rows      int
	Sample    string
	Direction domain.EntryType
}
