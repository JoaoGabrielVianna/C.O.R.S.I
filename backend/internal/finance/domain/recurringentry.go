package domain

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

// RecurringEntry is money that repeats: rent, a subscription, a gym — and
// a salary.
//
// ── Why "entry", and why not "fixed expense" ───────────────────────────
// This schema already calls a directional money item an ENTRY:
// `finance.entry_type` is the income/expense enum, and both Category and
// Transaction carry it. A recurring entry is a thing with an entry type
// that repeats.
//
// It was called FixedExpense, which could only ever hold money going out.
// That made a salary unrepresentable and pointed at a `fixed_incomes`
// table beside it — two tables, two sets of operations, two capabilities,
// for one concept. The direction was never the concept; recurrence was.
//
// ── What separates it from a Transaction, and why that line is load-bearing ──
// A Transaction is money that moved, or is scheduled to move on a named
// date. A RecurringEntry is a statement that something REPEATS. It carries
// a due DAY rather than a date, and it does not end until someone ends it.
//
// Nothing in this package turns one into the other. Generating twelve
// transactions a year from a recurrence would put money in the ledger that
// nobody confirmed, and every total would then include amounts that may
// never have happened — indistinguishable, afterwards, from the real ones.
// A payment against a recurrence is recorded the same way any other payment
// is: as a Transaction, when it happens.
//
// ── Where the direction comes from ─────────────────────────────────────
// From the CATEGORY, read through it, never stored here. Transactions keep
// a `type` copy because a composite foreign key makes the database enforce
// its agreement with the category; a recurring entry has no such pair, so
// a stored copy would be free to drift the day a category is
// recategorised. See Service.recurringDirection.
type RecurringFrequency string

const (
	RecurrenceMonthly RecurringFrequency = "monthly"
	RecurrenceAnnual  RecurringFrequency = "annual"
)

func (r RecurringFrequency) Valid() bool {
	return r == RecurrenceMonthly || r == RecurrenceAnnual
}

// RecurrenceNames is the vocabulary, named once so a schema description and
// a parser cannot disagree about it.
func RecurrenceNames() []string {
	return []string{string(RecurrenceMonthly), string(RecurrenceAnnual)}
}

func ParseRecurrence(raw string) (RecurringFrequency, error) {
	r := RecurringFrequency(strings.ToLower(strings.TrimSpace(raw)))
	if !r.Valid() {
		return "", Invalid("recurrence must be one of: " + strings.Join(RecurrenceNames(), ", "))
	}
	return r, nil
}

// RecurringStatus says whether the entry is currently active.
//
// Two values, and `ended` is deliberately NOT one of them: a commitment
// that stopped is expressed by EndsAt, not by a third status. One fact,
// one place. A row with EndsAt in the past has ended regardless of what a
// status column would have said, and two fields that can disagree about
// the same thing is how a paused-and-cancelled row becomes unanswerable.
type RecurringStatus string

const (
	// StatusActive: currently recurring.
	StatusActive RecurringStatus = "active"
	// StatusPaused: temporarily not running, but not cancelled — the entry
	// is expected to resume.
	StatusPaused RecurringStatus = "paused"
)

func (s RecurringStatus) Valid() bool {
	return s == StatusActive || s == StatusPaused
}

func RecurringStatusNames() []string {
	return []string{string(StatusActive), string(StatusPaused)}
}

func ParseRecurringStatus(raw string) (RecurringStatus, error) {
	s := RecurringStatus(strings.ToLower(strings.TrimSpace(raw)))
	if !s.Valid() {
		return "", Invalid("status must be one of: " + strings.Join(RecurringStatusNames(), ", "))
	}
	return s, nil
}

type RecurringEntry struct {
	ID          uuid.UUID  `json:"id"`
	WorkspaceID uuid.UUID  `json:"workspace_id"`
	Description string     `json:"description"`
	AmountCents int64      `json:"amount_cents"`
	CategoryID  uuid.UUID  `json:"category_id"`
	PersonID    *uuid.UUID `json:"person_id,omitempty"`
	// DueDay is 1..31. A commitment due on the 31st in a month that has 30
	// days is a presentation question, not a storage one: the day the user
	// said is the day that is stored.
	DueDay     int                `json:"due_day"`
	Recurrence RecurringFrequency `json:"recurrence"`
	Status     RecurringStatus    `json:"status"`
	StartsAt   time.Time          `json:"starts_at"`
	// EndsAt records a CANCELLATION without destroying the history of
	// having had the commitment. Nil means it is still recurring.
	EndsAt    *time.Time `json:"ends_at,omitempty"`
	Notes     string     `json:"notes"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	DeletedAt *time.Time `json:"deleted_at,omitempty"`
}

func (f *RecurringEntry) Validate() error {
	if f.WorkspaceID == uuid.Nil {
		return Invalid("workspace_id required")
	}
	if d := strings.TrimSpace(f.Description); d == "" || len(d) > 280 {
		return Invalid("description must be 1..280 chars")
	}
	if f.AmountCents <= 0 {
		return Invalid("amount_cents must be > 0")
	}
	if f.CategoryID == uuid.Nil {
		return Invalid("category_id required")
	}
	if f.DueDay < 1 || f.DueDay > 31 {
		return Invalid("due_day must be 1..31")
	}
	if !f.Recurrence.Valid() {
		return Invalid("recurrence must be monthly or annual")
	}
	if !f.Status.Valid() {
		return Invalid("status must be active or paused")
	}
	if f.StartsAt.IsZero() {
		return Invalid("starts_at required")
	}
	if f.EndsAt != nil && f.EndsAt.Before(f.StartsAt) {
		return Invalid("ends_at must not be before starts_at")
	}
	if len(f.Notes) > 1000 {
		return Invalid("notes must be <= 1000 chars")
	}
	return nil
}

// EndedBy reports whether the commitment had stopped recurring at t.
//
// A method rather than a field: "has it ended" is a question about a moment,
// and the only moment that matters is the one being asked about. Storing the
// answer would freeze it.
func (f *RecurringEntry) EndedBy(t time.Time) bool {
	return f.EndsAt != nil && !f.EndsAt.After(t)
}

// ActiveAt reports whether this entry is running at t: active, started,
// and not ended.
//
// This is the predicate behind "quanto recebo e quanto pago por mês".
// Paused is excluded because a paused entry is not running — that is the
// whole difference between pausing and merely existing.
func (f *RecurringEntry) ActiveAt(t time.Time) bool {
	if f.Status != StatusActive {
		return false
	}
	if f.StartsAt.After(t) {
		return false
	}
	return !f.EndedBy(t)
}

// MonthlyEquivalentCents is what this entry is worth per month.
//
// ── Why an annual entry is divided rather than ignored ─────────────────
// Because the question "quanto entra e quanto sai por mês" is asked to plan
// a month, and an annual insurance premium — or a yearly bonus — is part of
// that answer even in the eleven months it does not occur. Reporting only
// the monthly ones would understate by exactly the amount that is easiest
// to forget.
//
// Integer division, and the remainder is dropped: at most 11 centavos a
// year per annual entry. Rounding up would overstate every answer by a
// little, forever; carrying the remainder would need a place to carry it
// to. The division happens here, once, so no caller invents its own.
func (f *RecurringEntry) MonthlyEquivalentCents() int64 {
	if f.Recurrence == RecurrenceAnnual {
		return f.AmountCents / 12
	}
	return f.AmountCents
}
