package domain

import (
	"time"

	"github.com/google/uuid"
)

type Transaction struct {
	ID            uuid.UUID         `json:"id"`
	WorkspaceID   uuid.UUID         `json:"workspace_id"`
	AccountID     *uuid.UUID        `json:"account_id,omitempty"`
	CategoryID    uuid.UUID         `json:"category_id"`
	Type          EntryType         `json:"type"`
	PersonID      *uuid.UUID        `json:"person_id,omitempty"`
	AmountCents   int64             `json:"amount_cents"`
	Description   string            `json:"description"`
	OccurredAt    time.Time         `json:"occurred_at"`
	Status        TransactionStatus `json:"status"`
	PaymentMethod PaymentMethod     `json:"payment_method"`
	Source        TransactionSource `json:"source"`
	Notes         string            `json:"notes"`
	// TransferPairID, when set, marks this row as one leg of a transfer.
	// Both legs share the same UUID. The Totals query excludes rows where
	// it is NOT NULL so transfers never inflate the overview.
	TransferPairID *uuid.UUID `json:"transfer_pair_id,omitempty"`
	// PlanID + InstallmentNumber bind the row to a parent PurchasePlan.
	// Both are NULL on every non-installment row, which
	// `transactions_plan_installment_chk` enforces.
	//
	// The pair is UNIQUE per plan among live rows — see
	// `transactions_plan_installment_unique`. This comment previously
	// claimed the uniqueness while the schema had only a non-unique index,
	// so the invariant the code relied on was not one the database held.
	PlanID            *uuid.UUID `json:"plan_id,omitempty"`
	InstallmentNumber *int       `json:"installment_number,omitempty"`
	// ExternalSource + ExternalID say where this row came from, when it
	// came from anywhere.
	//
	// ── What they are for, and what they are NOT ────────────────────────
	// They are the identity a future import would match on so the same
	// statement line cannot land twice. Nothing populates them today: no
	// import capability exists, no bulk endpoint exists, and
	// `finance.transaction.create` does not accept them — a transaction
	// recorded from a conversation has neither.
	//
	// They exist now because adding the unique index later, to a table
	// already holding a person's financial history, is a materially worse
	// operation than adding it to one that does not.
	//
	// ── Why a source and not just an id ─────────────────────────────────
	// "Row 4471" is an identity WITHIN a system. A bank statement, a card
	// export and a spreadsheet each number their rows from one and will
	// collide. The pair is the identity; either half alone is a guess.
	// The database refuses one without the other.
	ExternalSource *string    `json:"external_source,omitempty"`
	ExternalID     *string    `json:"external_id,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	DeletedAt      *time.Time `json:"deleted_at,omitempty"`
}

func (t *Transaction) Validate() error {
	if t.WorkspaceID == uuid.Nil {
		return Invalid("workspace_id required")
	}
	if t.CategoryID == uuid.Nil {
		return Invalid("category_id required")
	}
	if !t.Type.Valid() {
		return Invalid("type must be income or expense")
	}
	if t.AmountCents <= 0 {
		return Invalid("amount_cents must be > 0")
	}
	if len(t.Description) > 280 {
		return Invalid("description must be <= 280 chars")
	}
	if t.OccurredAt.IsZero() {
		return Invalid("occurred_at required")
	}
	if !t.Status.Valid() {
		return Invalid("status must be paid, pending or scheduled")
	}
	if !t.PaymentMethod.Valid() {
		return Invalid("payment_method must be debit, credit, pix, cash or transfer")
	}
	if !t.Source.Valid() {
		return Invalid("source must be manual, whatsapp, import or ai")
	}
	if len(t.Notes) > 1000 {
		return Invalid("notes must be <= 1000 chars")
	}
	// Both halves of the external identity, or neither. The database says
	// the same thing; saying it here too means a caller gets a domain error
	// rather than a constraint violation.
	if (t.ExternalSource == nil) != (t.ExternalID == nil) {
		return Invalid("external_source and external_id must be set together")
	}
	if t.ExternalSource != nil {
		if n := len(*t.ExternalSource); n < 1 || n > 60 {
			return Invalid("external_source must be 1..60 chars")
		}
		if n := len(*t.ExternalID); n < 1 || n > 200 {
			return Invalid("external_id must be 1..200 chars")
		}
	}
	return nil
}
