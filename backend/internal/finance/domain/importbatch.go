package domain

import (
	"time"

	"github.com/google/uuid"
)

// ── Two kinds of claim, and why the difference is load-bearing ─────────
//
// IdentityStrong is an identifier the institution issued and stands behind:
// an OFX FITID, a bank's own row id. If two exports carry the same strong
// id for the same account, they are the same event. That is a fact about
// the world, not an inference about it.
//
// IdentityHeuristic is a hash WE derive from whatever fields the export
// happened to include. It is a conservative dedup strategy and it is NEVER
// proof of identity, because the fields cannot distinguish these two
// situations:
//
//	one coffee, exported twice
//	two coffees, same shop, same price, same afternoon
//
// Both produce the same account, date, amount and description. A system
// that treated a heuristic match as identity would silently discard the
// second coffee, and the operator would never learn that a real purchase
// is missing from their ledger.
//
// So the rule this type exists to hold: a strong match is an ANSWER and
// may skip; a heuristic match is a QUESTION and must be asked.
type IdentityStrategy string

const (
	IdentityStrong    IdentityStrategy = "strong"
	IdentityHeuristic IdentityStrategy = "heuristic"
)

func (s IdentityStrategy) Valid() bool {
	return s == IdentityStrong || s == IdentityHeuristic
}

type ImportFormat string

const ImportFormatCSV ImportFormat = "csv"

func (f ImportFormat) Valid() bool { return f == ImportFormatCSV }

type ImportBatchStatus string

const (
	ImportBatchPrepared  ImportBatchStatus = "prepared"
	ImportBatchCommitted ImportBatchStatus = "committed"
	ImportBatchDiscarded ImportBatchStatus = "discarded"
)

// ImportRowState is what the preview decided about one line.
//
// Every state below is here because a concrete case demanded it, and the
// set is closed: a line is always in exactly one, so the six counts sum to
// the number of lines parsed and nothing can vanish between the file and
// the report.
type ImportRowState string

const (
	// RowReady has a category, a valid amount and a valid date, and no
	// identity that already exists. It is the only state commit imports.
	RowReady ImportRowState = "ready"
	// RowDuplicate matched a STRONG identity already in the ledger. Safe to
	// skip without asking, because the institution says it is the same event.
	RowDuplicate ImportRowState = "duplicate"
	// RowAmbiguousDuplicate matched only heuristically. It is NOT imported
	// and NOT discarded: the operator decides, because the alternative is
	// deleting a real purchase on a guess.
	RowAmbiguousDuplicate ImportRowState = "ambiguous_duplicate"
	// RowUnresolvedCategory parsed cleanly and has nowhere to go. The
	// importer never invents a category.
	RowUnresolvedCategory ImportRowState = "unresolved_category"
	// RowAmbiguousInternal looks like money moving between the operator's
	// own accounts. A statement shows ONE leg, and Finance models a transfer
	// as two rows with opposite categories, so inferring the counterparty
	// would be inventing half an event.
	RowAmbiguousInternal ImportRowState = "ambiguous_internal"
	// RowInvalid could not be parsed. It never becomes a Transaction, and
	// the schema enforces that by requiring the parsed fields on every
	// other state.
	RowInvalid ImportRowState = "invalid"
)

// ImportBatch is one act of importing: what arrived, and where each line
// went. It outlives the tool call that produced it because every finance
// capability is Confidential, so a reconciliation kept only in a tool
// result would be redacted out of the audit the moment it was written.
type ImportBatch struct {
	ID          uuid.UUID `json:"id"`
	WorkspaceID uuid.UUID `json:"workspace_id"`
	SourceLabel string    `json:"source_label"`
	// AccountScope is the derived namespace, `import-source:<uuid>`. It is
	// what lands in transactions.external_source, and no caller supplies it.
	AccountScope string `json:"account_scope"`
	// ImportSourceID is the row that namespace was derived from. Nullable
	// only because batches staged before import sources existed keep the
	// scope they were staged with; nothing rewrites them.
	ImportSourceID *uuid.UUID `json:"import_source_id,omitempty"`

	Format ImportFormat      `json:"format"`
	Status ImportBatchStatus `json:"status"`
	// ContentSHA256 is operational evidence that the text being committed
	// is the text that was previewed. It is NEVER an identity for a
	// transaction: the same file may legitimately be imported again after a
	// delete, and two different files may carry the same transactions.
	ContentSHA256 string `json:"content_sha256"`

	RowCount     int `json:"row_count"`
	CreatedCount int `json:"created_count"`
	SkippedCount int `json:"skipped_count"`

	CreatedAt   time.Time  `json:"created_at"`
	CommittedAt *time.Time `json:"committed_at,omitempty"`
}

type ImportRow struct {
	ID          uuid.UUID `json:"id"`
	BatchID     uuid.UUID `json:"batch_id"`
	WorkspaceID uuid.UUID `json:"workspace_id"`
	LineNumber  int       `json:"line_number"`

	OccurredAt  *time.Time `json:"occurred_at,omitempty"`
	AmountCents *int64     `json:"amount_cents,omitempty"`
	Description string     `json:"description"`
	Direction   *EntryType `json:"direction,omitempty"`

	// GroupKey is the normalised description. Resolution happens per group
	// because a tool schema is a flat object of scalars: it can carry one
	// group key, not a map of 150 descriptions to categories.
	GroupKey string `json:"group_key"`

	State       ImportRowState `json:"state"`
	StateDetail string         `json:"state_detail"`

	IdentityStrategy *IdentityStrategy `json:"identity_strategy,omitempty"`
	IdentityValue    *string           `json:"identity_value,omitempty"`

	CategoryID    *uuid.UUID `json:"category_id,omitempty"`
	TransactionID *uuid.UUID `json:"transaction_id,omitempty"`
}

// ImportReconciliation is the arithmetic the operator is owed: every line
// that arrived is in exactly one bucket, and the buckets sum to the file.
type ImportReconciliation struct {
	RowCount int `json:"row_count"`

	Ready              int `json:"ready"`
	Duplicate          int `json:"duplicate"`
	AmbiguousDuplicate int `json:"ambiguous_duplicate"`
	UnresolvedCategory int `json:"unresolved_category"`
	AmbiguousInternal  int `json:"ambiguous_internal"`
	Invalid            int `json:"invalid"`
}

// Balances reports whether the states account for every parsed line. A
// false here is a bug in this module, not bad input, which is why callers
// treat it as an internal error rather than a validation message.
func (r ImportReconciliation) Balances() bool {
	return r.RowCount == r.Ready+r.Duplicate+r.AmbiguousDuplicate+
		r.UnresolvedCategory+r.AmbiguousInternal+r.Invalid
}

func (r ImportReconciliation) Sum() int {
	return r.Ready + r.Duplicate + r.AmbiguousDuplicate +
		r.UnresolvedCategory + r.AmbiguousInternal + r.Invalid
}

// ImportResult is what commit reports. Created plus SkippedConcurrent must
// equal the number of rows that were eligible when the commit began: a row
// that lost a race to a concurrent import is skipped, never lost.
type ImportResult struct {
	BatchID           uuid.UUID            `json:"batch_id"`
	EligibleReady     int                  `json:"eligible_ready"`
	Created           int                  `json:"created"`
	SkippedConcurrent int                  `json:"skipped_concurrent"`
	Reconciliation    ImportReconciliation `json:"reconciliation"`
}
