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
	// said is the day that is stored, and Period.DueOn clamps it to a day
	// the month actually has when a month is finally named.
	DueDay     int                `json:"due_day"`
	Recurrence RecurringFrequency `json:"recurrence"`
	// DueMonth is WHICH MONTH an annual obligation falls in, 1..12. It is
	// nil on a monthly entry and required on an annual one.
	//
	// ── Why it is not derived from StartsAt ─────────────────────────────
	// Because they answer different questions and the answers differ all
	// the time. StartsAt says WHEN THIS DEFINITION BECAME APPLICABLE: the
	// day somebody wrote the obligation down, which is usually the day they
	// happened to be tidying their finances. DueMonth says WHEN THE MONEY
	// IS DUE: January, for an IPVA, no matter that it was recorded in
	// September.
	//
	// Deriving one from the other would file an annual obligation in the
	// month of its own data entry, and the only way to correct it would be
	// to falsify the date the record began. Two facts, two columns.
	//
	// ── Nil on an annual entry, and why that state exists at all ────────
	// It is a row written before this column did: the migration adds the
	// column nullable, because it cannot invent a due month for annual
	// rows it finds and must not guess one. Such an entry is READABLE and
	// INERT — OccursIn returns false for it, so it materialises nothing —
	// and Validate refuses to write it again until a month is supplied.
	// See NeedsDueMonth.
	DueMonth *int            `json:"due_month,omitempty"`
	Status   RecurringStatus `json:"status"`
	// AmountVaries says the amount is not the same every time: an
	// electricity bill, a water bill, a card invoice.
	//
	// It does NOT make AmountCents optional. The amount stays required and
	// becomes an ESTIMATE, which is what a monthly view needs in order to
	// show a total at all before the real figure arrives. What the flag
	// buys is honesty about that total: an occurrence born from a varying
	// definition is marked estimated, and the screen can say how much of
	// the number it is showing has not been confirmed yet.
	AmountVaries bool      `json:"amount_varies"`
	StartsAt     time.Time `json:"starts_at"`
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
	// ── The pairing rule between recurrence and due_month ───────────────
	// Enforced here rather than only in the database, because the database
	// deliberately holds only the half that is provable against rows that
	// already exist: `monthly implies NULL` is true of every legacy row, so
	// it is a CHECK; `annual implies NOT NULL` is not, because a row
	// predating the column would violate it and the migration has no
	// truthful value to fill in. This is where the other half lives, and it
	// is why no NEW or EDITED annual entry can lack a due month.
	switch f.Recurrence {
	case RecurrenceMonthly:
		if f.DueMonth != nil {
			return Invalid("due_month applies only to an annual recurring entry; " +
				"a monthly one falls due every month")
		}
	case RecurrenceAnnual:
		if f.DueMonth == nil {
			return Invalid("due_month is required on an annual recurring entry: " +
				"say which month it falls due, 1 to 12")
		}
		if *f.DueMonth < 1 || *f.DueMonth > 12 {
			return Invalid("due_month must be 1..12")
		}
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

// NeedsDueMonth reports an annual entry written before due_month existed.
//
// It is the one state the domain can observe but not repair. Such a row is
// read normally and contributes nothing to any month, because guessing when
// an annual bill falls is exactly the kind of invention this module refuses
// elsewhere. A surface that finds one asks the operator; it does not
// default to January, and it does not default to the month the row was
// created in.
func (f *RecurringEntry) NeedsDueMonth() bool {
	return f.Recurrence == RecurrenceAnnual && f.DueMonth == nil
}

// OccursIn reports whether this entry's FREQUENCY puts an obligation in p.
//
// Monthly is every month, by definition. Annual is the one month named by
// DueMonth and no other, which is what makes the annual amount land whole
// in January rather than smeared across twelve months — see the note on
// MonthlyEquivalentCents about why the recurring SUMMARY does the opposite
// and why the two readings must not be added.
//
// It says nothing about dates, status or cancellation. That is ActiveIn.
func (f *RecurringEntry) OccursIn(p Period) bool {
	switch f.Recurrence {
	case RecurrenceMonthly:
		return true
	case RecurrenceAnnual:
		if f.DueMonth == nil {
			return false
		}
		return time.Month(*f.DueMonth) == p.Month()
	default:
		return false
	}
}

// ActiveIn reports whether this entry owes an occurrence in the month p.
//
// ══════════════════════════════════════════════════════════════════════
//
//	THE UNIT OF COMPARISON IS THE MONTH, NOT THE INSTANT
//
// ══════════════════════════════════════════════════════════════════════
//
// ── Why the boundaries are inclusive at both ends ──────────────────────
// The month CONTAINING StartsAt counts, and the month containing EndsAt
// counts.
//
// Writing a gym down on the 20th, due on the 5th, does not mean September
// was free: the obligation existed, and skipping the partial month would
// silently drop a real one. It is materialised pending, shows as overdue,
// and one click settles it — which is a state the operator can see and
// correct, unlike an absence.
//
// Cancelling on the 15th is the mirror. September's bill was owed and
// almost certainly paid; October's was not. Excluding the month a
// cancellation falls in would erase the last month of every subscription
// the operator ever had.
//
// Symmetry is the point. A rule that included the starting month but
// excluded the ending one would be two rules, and the second is the one
// nobody remembers when reading a total that looks slightly wrong.
//
// ── Why this is not ActiveAt with a different argument ─────────────────
// ActiveAt answers "is this being charged right now", which is what the
// recurring summary needs. ActiveIn answers "does this month have a bill",
// which is what a monthly view needs. For the current month they usually
// agree; for August read in September they do not, and the one that would
// be wrong is the instant-based one.
func (f *RecurringEntry) ActiveIn(p Period, loc *time.Location) bool {
	if f.Status != StatusActive {
		return false
	}
	if p.IsZero() || !f.OccursIn(p) {
		return false
	}
	if p.Before(PeriodOf(f.StartsAt, loc)) {
		return false
	}
	if f.EndsAt != nil && p.After(PeriodOf(*f.EndsAt, loc)) {
		return false
	}
	return true
}

// DueOnIn is the calendar day this entry falls due inside p, clamped to a
// day the month has. A civil date; see Period.DueOn.
func (f *RecurringEntry) DueOnIn(p Period) time.Time {
	return p.DueOn(f.DueDay)
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
//
// ── This is NOT what a monthly commitment shows, and that is deliberate ──
// RecurringSummary answers "quanto entra e quanto sai por mês", a planning
// question, so an annual premium is worth a twelfth of itself in all twelve
// months. A MONTHLY COMMITMENT answers "quanto tenho a pagar em janeiro",
// a question about one month's bills, so the same premium is worth its FULL
// amount in the month it falls due and nothing in the other eleven — see
// OccursIn.
//
// Both are right about their own question and the two must never be added
// or compared. The contract this function serves has consumers today and is
// left exactly as it was; the monthly reading is new and sits beside it.
func (f *RecurringEntry) MonthlyEquivalentCents() int64 {
	if f.Recurrence == RecurrenceAnnual {
		return f.AmountCents / 12
	}
	return f.AmountCents
}
