package domain

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

// RecurringOccurrence is ONE MONTH of a RecurringEntry.
//
// ══════════════════════════════════════════════════════════════════════
//
//	THE DEFINITION SAYS WHAT REPEATS
//	THE OCCURRENCE SAYS WHAT HAPPENED IN ONE MONTH
//	THE TRANSACTION SAYS MONEY MOVED
//
// ══════════════════════════════════════════════════════════════════════
//
// ── Why the paid state cannot be a column on the definition ────────────
// Because a boolean on `recurring_entries` has room for exactly one answer,
// and the question has one answer per month. Ticking "internet paid" in
// September and untick-ing it in October would leave no way at all to
// answer "was September's internet paid", and the answer would already have
// been overwritten by the time anybody asked. A monthly fact needs a
// monthly row.
//
// ── Why this is still not a Transaction ────────────────────────────────
// A transaction is money that moved, and the ledger is the only thing that
// may claim it did. Marking an occurrence paid is the OPERATOR asserting
// that an obligation was settled; it writes nothing to `finance.transactions`
// and creates no amount any total will count. The two facts are related and
// are kept apart, exactly as a recurring entry and a payment have always
// been kept apart in this module.
//
// TransactionID exists for the day the operator wants to say "this month's
// rent WAS that transaction". Nothing populates it yet, and nothing should
// until that linkage has a design. It is a column now rather than later
// because adding one to a table already holding somebody's financial
// history is a materially worse operation than adding it to an empty one --
// the same argument Transaction.ExternalSource already makes.
//
// ── What is frozen, and why freezing is the whole point ────────────────
// AmountCents and DueOn are copied from the definition at the moment the
// occurrence is created and are NEVER recomputed from it afterwards. That
// is what makes history hold: the rent rising in October does not rewrite
// September, and moving a due day does not move a due date that has already
// passed. Every edit path in this module has to respect it, and the
// application layer is where that is enforced.
type RecurringOccurrence struct {
	ID               uuid.UUID `json:"id"`
	WorkspaceID      uuid.UUID `json:"workspace_id"`
	RecurringEntryID uuid.UUID `json:"recurring_entry_id"`

	// Period is the month this row belongs to, and half of its identity:
	// one occurrence per (entry, period), enforced by a unique index.
	Period Period `json:"period"`
	// DueOn is the calendar day it fell due inside Period, already clamped
	// to a day the month has. A civil date, midnight UTC; see Period.DueOn.
	DueOn time.Time `json:"due_on"`

	// AmountCents is this month's amount, in integer cents, always
	// positive. The direction comes from the definition's category, exactly
	// as it does for a transaction, and is never stored here.
	AmountCents int64 `json:"amount_cents"`
	// AmountEstimated says this number is NOT a confirmed fact about this
	// month.
	//
	// Two different situations produce it, and they mean the same thing to
	// everyone downstream:
	//
	//  1. the definition's amount varies (an electricity bill), so what is
	//     here is the estimate until the real figure arrives;
	//  2. the month had already ended when the row was created, so the
	//     figure was RECONSTRUCTED from the definition as it stands today
	//     rather than recorded at the time.
	//
	// The second is the honest cost of being able to open a month that was
	// never opened. Showing it as a confirmed historical amount would be
	// the product inventing a past, so it is shown as an estimate and the
	// total says how much of itself is one.
	AmountEstimated bool `json:"amount_estimated"`

	Status OccurrenceStatus `json:"status"`
	// PaidAt is when it was settled, from the database's clock. Set exactly
	// when Status is paid, which the database also enforces.
	PaidAt *time.Time `json:"paid_at,omitempty"`
	// TransactionID is the future link to the ledger row. See the type
	// comment: nil today, always.
	TransactionID *uuid.UUID `json:"transaction_id,omitempty"`

	Origin    OccurrenceOrigin `json:"origin"`
	CreatedAt time.Time        `json:"created_at"`
	UpdatedAt time.Time        `json:"updated_at"`
}

/* ── the two small vocabularies ──────────────────────────────────────── */

// OccurrenceStatus is whether this month's obligation has been settled.
//
// Two values, and there is deliberately no `overdue`: being late is a fact
// about the due date and today, derived by Overdue, not a state somebody
// has to remember to write. A stored `overdue` would be correct only until
// the clock moved past it.
type OccurrenceStatus string

const (
	OccurrencePending OccurrenceStatus = "pending"
	OccurrencePaid    OccurrenceStatus = "paid"
)

func (s OccurrenceStatus) Valid() bool {
	return s == OccurrencePending || s == OccurrencePaid
}

func OccurrenceStatusNames() []string {
	return []string{string(OccurrencePending), string(OccurrencePaid)}
}

func ParseOccurrenceStatus(raw string) (OccurrenceStatus, error) {
	s := OccurrenceStatus(strings.ToLower(strings.TrimSpace(raw)))
	if !s.Valid() {
		return "", Invalid("status must be one of: " + strings.Join(OccurrenceStatusNames(), ", "))
	}
	return s, nil
}

// OccurrenceOrigin says who put this row here.
//
// `materialized` is the system, reading a month and filling in what the
// definitions imply. `manual` is the operator recording a month that the
// recurrence did not imply -- a one-off extra charge, a bill that came
// twice. Only the first is written today; the second exists so that
// capability does not need a migration.
type OccurrenceOrigin string

const (
	OriginMaterialized OccurrenceOrigin = "materialized"
	OriginManual       OccurrenceOrigin = "manual"
)

func (o OccurrenceOrigin) Valid() bool {
	return o == OriginMaterialized || o == OriginManual
}

/* ── construction ────────────────────────────────────────────────────── */

// NewOccurrence builds the occurrence a definition implies for one month.
//
// ── Why it takes `now`, and what it uses it for ────────────────────────
// For one decision only: whether this row is being created for the month
// the world is currently in, or for one that has already ended. A row for a
// past month is a reconstruction and is marked estimated; a row for the
// current month, from a definition whose amount does not vary, is the
// amount the operator actually expects to pay.
//
// `now` comes from the DATABASE's clock, like every other instant in this
// module. A process clock here would flip the estimated flag on the first
// day of a month for as long as the two disagreed.
//
// It does NOT check whether the entry is active in p. That is ActiveIn, and
// the caller has already had to ask it in order to know there is anything
// to build.
func NewOccurrence(f *RecurringEntry, p Period, loc *time.Location, now time.Time) (*RecurringOccurrence, error) {
	if f == nil {
		return nil, Invalid("a recurring entry is required")
	}
	if p.IsZero() {
		return nil, Invalid("period is required")
	}
	current := PeriodOf(now, loc)
	o := &RecurringOccurrence{
		ID:               uuid.New(),
		WorkspaceID:      f.WorkspaceID,
		RecurringEntryID: f.ID,
		Period:           p,
		DueOn:            f.DueOnIn(p),
		AmountCents:      f.AmountCents,
		// Confirmed only for a non-varying definition, in the month that is
		// actually happening. A past month is a reconstruction and a future
		// one is a projection; both are estimates, and saying so is cheaper
		// than explaining a total later.
		AmountEstimated: f.AmountVaries || !p.Equal(current),
		Status:          OccurrencePending,
		Origin:          OriginMaterialized,
	}
	if err := o.Validate(); err != nil {
		return nil, err
	}
	return o, nil
}

/* ── invariants ──────────────────────────────────────────────────────── */

func (o *RecurringOccurrence) Validate() error {
	if o.WorkspaceID == uuid.Nil {
		return Invalid("workspace_id required")
	}
	if o.RecurringEntryID == uuid.Nil {
		return Invalid("recurring_entry_id required")
	}
	if o.Period.IsZero() {
		return Invalid("period required")
	}
	if o.DueOn.IsZero() {
		return Invalid("due_on required")
	}
	// The due date has to be a day of the month this row is filed under.
	// Without this, time.Date's normalisation could put "31 February" on
	// 3 March and the row would carry a due date outside its own period --
	// see Period.DueOn.
	if !o.Period.Contains(o.DueOn) {
		return Invalid("due_on must fall inside the occurrence's own period")
	}
	if o.AmountCents <= 0 {
		return Invalid("amount_cents must be > 0")
	}
	if !o.Status.Valid() {
		return Invalid("status must be one of: " + strings.Join(OccurrenceStatusNames(), ", "))
	}
	if !o.Origin.Valid() {
		return Invalid("origin must be materialized or manual")
	}
	// Paid and the moment of payment are one fact in two fields, so they
	// agree or the row is refused. The database says the same thing; saying
	// it here too means a caller gets a domain error rather than a
	// constraint violation.
	if (o.Status == OccurrencePaid) != (o.PaidAt != nil) {
		return Invalid("a paid occurrence has a paid_at, and a pending one has none")
	}
	return nil
}

/* ── transitions ─────────────────────────────────────────────────────── */

// MarkPaid settles this month.
//
// `at` is the database's clock, for the reason every other timestamp in
// this module is. Marking an already-paid occurrence is refused rather than
// silently re-stamped: the second call would move a recorded payment date,
// and a date that moves is not a record.
func (o *RecurringOccurrence) MarkPaid(at time.Time) error {
	if o.Status == OccurrencePaid {
		return Conflict("this month is already marked paid")
	}
	if at.IsZero() {
		return Invalid("a payment time is required")
	}
	o.Status = OccurrencePaid
	o.PaidAt = &at
	return nil
}

// UnmarkPaid undoes a mistaken tick.
//
// ── What it does NOT do ────────────────────────────────────────────────
// It does not touch the ledger. If a transaction was ever linked here, the
// link is dropped and the TRANSACTION REMAINS: unticking is the operator
// correcting a statement about an obligation, not a claim that money
// un-moved. Deleting a transaction is a separate, explicit act in the place
// transactions are deleted, and a checkbox must never reach into the
// ledger.
//
// The amount is left exactly as it is, including AmountEstimated. A figure
// the operator confirmed stays confirmed; being wrong about whether it was
// PAID says nothing about whether it was the right amount.
func (o *RecurringOccurrence) UnmarkPaid() {
	o.Status = OccurrencePending
	o.PaidAt = nil
	o.TransactionID = nil
}

// SetAmountCents records what this month actually costs.
//
// It clears AmountEstimated, because somebody just said what the number is:
// the bill arrived, or the operator corrected a reconstruction. That is the
// only way the flag is ever cleared, and it is why the monthly total can
// report how much of itself is still a guess.
func (o *RecurringOccurrence) SetAmountCents(cents int64) error {
	if cents <= 0 {
		return Invalid("amount_cents must be > 0")
	}
	o.AmountCents = cents
	o.AmountEstimated = false
	return nil
}

// Overdue reports a pending obligation whose due date has passed.
//
// Derived, never stored, and `today` is a civil date from the reporting
// zone. A paid occurrence is never overdue however late it was settled:
// lateness that has already been resolved is not a thing to show.
func (o *RecurringOccurrence) Overdue(today time.Time) bool {
	if o.Status != OccurrencePending {
		return false
	}
	return o.DueOn.Before(time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC))
}

/* ── the month's totals ──────────────────────────────────────────────── */

// MonthlyCommitment is one month, totalled.
//
// ── Why the figures are separate and none of them is net ───────────────
// Because the question is "how much of this month is dealt with", and three
// numbers answer it: what is owed in total, what is settled, and what is
// left. A single percentage would hide the case that matters, which is one
// large bill outstanding among five small ones settled.
//
// EstimatedCents is the part of Committed that nobody has confirmed. It is
// reported so a total can be shown without being passed off as exact.
type MonthlyCommitment struct {
	Period Period `json:"period"`
	// Projection marks a month that has NOT been materialised because it
	// has not begun: the figures are computed from the definitions and no
	// row was written. Set by the caller that knows which month is current,
	// not by the summing below.
	Projection bool `json:"projection"`

	CommittedCents int64 `json:"committed_cents"`
	PaidCents      int64 `json:"paid_cents"`
	// RemainingCents is Committed minus Paid. It never goes negative: Paid
	// is a subset of Committed by construction, since both are summed from
	// the same rows.
	RemainingCents int64 `json:"remaining_cents"`
	// EstimatedCents is how much of CommittedCents is still an estimate.
	EstimatedCents int64 `json:"estimated_cents"`

	Count          int `json:"occurrence_count"`
	PaidCount      int `json:"paid_count"`
	PendingCount   int `json:"pending_count"`
	EstimatedCount int `json:"estimated_count"`
	OverdueCount   int `json:"overdue_count"`
}

// SummarizeOccurrences totals one month.
//
// A pure function over rows the caller has already read, so the arithmetic
// that a screen, a capability and a test all depend on exists once. It does
// not read categories, does not resolve names and does not know what month
// it is: everything it needs is in the rows.
func SummarizeOccurrences(p Period, occ []RecurringOccurrence, today time.Time) MonthlyCommitment {
	m := MonthlyCommitment{Period: p, Count: len(occ)}
	for i := range occ {
		o := &occ[i]
		m.CommittedCents += o.AmountCents
		// Counted whether or not it is settled. Ticking a bill without ever
		// entering the real figure leaves the month settled AND partly a
		// guess, and reporting it as exact would be the product deciding
		// the guess had become a fact.
		if o.AmountEstimated {
			m.EstimatedCents += o.AmountCents
			m.EstimatedCount++
		}
		if o.Status == OccurrencePaid {
			m.PaidCents += o.AmountCents
			m.PaidCount++
			continue
		}
		m.PendingCount++
		if o.Overdue(today) {
			m.OverdueCount++
		}
	}
	m.RemainingCents = m.CommittedCents - m.PaidCents
	return m
}
