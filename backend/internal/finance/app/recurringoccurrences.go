package app

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/finance/domain"
	"github.com/corsi/backend/internal/finance/ports"
)

// Monthly commitment: what a month owes, what it has settled, what is left.
//
// ══════════════════════════════════════════════════════════════════════
//
//	ONE SERVICE, TWO SURFACES, ONE ANSWER
//
// ══════════════════════════════════════════════════════════════════════
//
// Everything below is reached identically by the HTTP handler and by the
// Finance capabilities. There is no agent-specific path and no screen-
// specific path, which is what makes "quanto falta pagar esse mês" have the
// same answer wherever it is asked. A second orchestration would be a
// second place for the materialisation rules to drift.
//
// ── What this view is NOT ──────────────────────────────────────────────
// It is not part of the frozen totals contract and no figure here is
// comparable with `GetTransactionTotals`. That contract classifies rows in
// `finance.transactions`; an occurrence is not one, and marking one paid
// writes nothing to the ledger. The two readings sit side by side and are
// never added: an obligation that was both ticked here and recorded as a
// transaction appears in both, and summing them counts it twice.
//
// It is also not `GetRecurringSummary`. That answers "quanto sai por mês"
// for planning and divides an annual premium by twelve. This answers "o que
// tenho a pagar em janeiro" and puts the whole premium in January. Both are
// right about their own question.

/* ── reading a month ─────────────────────────────────────────────────── */

type GetMonthlyCommitmentInput struct {
	WorkspaceID uuid.UUID
	// Period is the month to read. The ZERO Period means "the current
	// month", resolved from the database's clock in the reporting zone.
	//
	// Zero-means-current rather than a separate boolean: a caller that
	// omitted the month wants the one that is happening, and there is no
	// second thing it could have meant.
	Period domain.Period
}

// MonthlyCommitmentLine is one obligation as a month shows it.
//
// It carries the occurrence, plus the recognition a person needs to know
// WHICH bill this is: the description and the category name, read from the
// definition. The definition is not embedded whole — a screen showing
// September must not accidentally render October's amount because the
// definition it was handed has already moved on.
type MonthlyCommitmentLine struct {
	Occurrence domain.RecurringOccurrence
	// RecurringEntryID is what a write capability targets, together with
	// the period. See MarkOccurrencePaid on why the pair and not the
	// occurrence's own id.
	RecurringEntryID uuid.UUID
	Description      string
	CategoryID       uuid.UUID
	CategoryName     string
	// Direction is read from the category, never stored on the occurrence.
	Direction domain.EntryType
	// Overdue is DERIVED here, from the authoritative today. It is not a
	// stored status, because "late" stops being true the moment it is
	// settled and starts being true without anything having been written.
	Overdue bool
}

// UnplaceableReason is the closed vocabulary of "this definition cannot be
// put in a month".
type UnplaceableReason string

// MissingDueMonth: an annual entry written before `due_month` existed. It
// is real, it is active, and nothing here knows which month it falls in.
// Guessing would put somebody's yearly bill in a month they never agreed
// to, so it is reported instead.
const MissingDueMonth UnplaceableReason = "missing_due_month"

// UnplaceableEntry names a definition a month could not place.
//
// It carries an id, a description and a machine-readable reason, and
// nothing else: no amount, no category, no dates. Enough for a surface to
// say "this needs a due month" and offer to fix it, and not enough to be
// worth anything to anybody who should not be reading it.
type UnplaceableEntry struct {
	RecurringEntryID uuid.UUID
	Description      string
	Reason           UnplaceableReason
}

// MonthlyCommitmentView is the whole answer to "how is this month going".
type MonthlyCommitmentView struct {
	// Totals is computed by the domain over exactly the Lines below, so a
	// caller can never be handed figures that describe a different set of
	// rows than the ones it is rendering.
	Totals domain.MonthlyCommitment
	Lines  []MonthlyCommitmentLine
	// Unplaceable is what this month could not account for. Non-empty
	// means the totals are INCOMPLETE and a surface has to say so.
	Unplaceable []UnplaceableEntry
	// Today and TimeZone are the authoritative date this view was cut
	// against. Returned always, for the reason the tools' windowFields
	// gives: a caller that does not know what day it is will describe the
	// month wrongly in prose while the numbers are right.
	Today    time.Time
	TimeZone string
}

// GetMonthlyCommitment reads one month, materialising it if it has begun.
//
// ══════════════════════════════════════════════════════════════════════
//
//	READ-THROUGH MATERIALISATION: THE PAST AND THE PRESENT ARE ROWS,
//	THE FUTURE IS ARITHMETIC
//
// ══════════════════════════════════════════════════════════════════════
//
// ── Why lazily, on read, and not on a schedule ─────────────────────────
// Because this platform has no scheduler and adding one for a single
// operator would buy a new silent failure: a month with no rows because the
// process was down at midnight on the 1st, indistinguishable from a month
// with nothing in it. The read is the moment the system is provably alive
// and provably knows what month it is.
//
// ── Why the future is never written ────────────────────────────────────
// Because a row for a month that has not begun is an obligation nobody has
// incurred yet, at an amount nobody has agreed to, that every later edit of
// the definition would then have to chase. It is the same "invent money
// nobody confirmed" failure migration 0009 refused, one step removed. So a
// future month is computed, labelled a projection, and thrown away.
//
// ── Why an existing row is never recomputed ────────────────────────────
// EnsureMissing inserts what is absent and touches nothing that is present.
// Re-reading September after the rent rises returns September's rent, which
// is the entire point of the table existing.
func (s *Service) GetMonthlyCommitment(ctx context.Context, in GetMonthlyCommitmentInput) (MonthlyCommitmentView, error) {
	if in.WorkspaceID == uuid.Nil {
		return MonthlyCommitmentView{}, domain.Invalid("workspace_id required")
	}

	// The authoritative instant, from Postgres. Everything downstream —
	// which month is current, what "today" means for overdue, the stamp a
	// payment would get — is cut from this one reading. See ports.Clock.
	now, err := s.repos.Clock.Now(ctx)
	if err != nil {
		return MonthlyCommitmentView{}, err
	}
	current := domain.PeriodOf(now, s.loc)
	period := in.Period
	if period.IsZero() {
		period = current
	}
	today := s.todayIn(now)

	entries, err := s.repos.RecurringEntries.List(ctx, in.WorkspaceID,
		ports.RecurringEntryFilter{Limit: recurringEntryScanLimit})
	if err != nil {
		return MonthlyCommitmentView{}, err
	}

	// Split the definitions three ways: the ones this month owes, the ones
	// it does not, and the ones nothing can place.
	//
	// An unplaceable definition must NOT fail the request. A single legacy
	// annual row would otherwise take the whole month's answer down, which
	// is a far worse outcome than a month that reports it is incomplete.
	applicable := make([]domain.RecurringEntry, 0, len(entries))
	var unplaceable []UnplaceableEntry
	for i := range entries {
		f := entries[i]
		if f.NeedsDueMonth() && f.Status == domain.StatusActive && !f.EndedBy(now) {
			unplaceable = append(unplaceable, UnplaceableEntry{
				RecurringEntryID: f.ID,
				Description:      f.Description,
				Reason:           MissingDueMonth,
			})
			continue
		}
		if f.ActiveIn(period, s.loc) {
			applicable = append(applicable, f)
		}
	}

	view := MonthlyCommitmentView{
		Unplaceable: unplaceable,
		Today:       today,
		TimeZone:    s.loc.String(),
	}

	var occurrences []domain.RecurringOccurrence
	if period.After(current) {
		// ── Projection ──────────────────────────────────────────────
		// Built and returned, never stored.
		occurrences, err = s.projectMonth(applicable, period, now)
		if err != nil {
			return MonthlyCommitmentView{}, err
		}
		// ── And stripped of their identities ────────────────────────
		// NewOccurrence mints an id because a row that is about to be
		// inserted needs one. These are not going to be inserted, so the
		// id is a value that resolves to nothing — and an identifier
		// that resolves to nothing is worse than none, because a caller
		// holding one will use it. Every surface downstream treats a nil
		// id as "this month is not a row yet", which is exactly true.
		for i := range occurrences {
			occurrences[i].ID = uuid.Nil
		}
	} else {
		// ── Materialisation ─────────────────────────────────────────
		// In ONE transaction, so a month is never half filled in: a
		// partially materialised month has a total that is wrong and
		// looks right.
		if err := s.txm.WithinTx(ctx, func(ctx context.Context) error {
			want, err := s.projectMonth(applicable, period, now)
			if err != nil {
				return err
			}
			if _, err := s.repos.RecurringOccurrences.EnsureMissing(ctx, want); err != nil {
				return err
			}
			// Re-read rather than return what was just built: the rows
			// that already existed are the authoritative ones, and they
			// are exactly the ones `want` does not describe.
			occurrences, err = s.repos.RecurringOccurrences.ListByPeriod(ctx, in.WorkspaceID, period)
			return err
		}); err != nil {
			return MonthlyCommitmentView{}, err
		}
	}

	view.Totals = domain.SummarizeOccurrences(period, occurrences, today)
	view.Totals.Projection = period.After(current)

	lines, err := s.decorate(ctx, in.WorkspaceID, occurrences, entries, today)
	if err != nil {
		return MonthlyCommitmentView{}, err
	}
	view.Lines = lines
	return view, nil
}

// recurringEntryScanLimit bounds the definitions one month considers.
//
// The same ceiling GetRecurringSummary already uses. A person with more
// than five hundred distinct recurring obligations is not a case this
// product has, and a bound that is stated is better than a query that
// silently grows.
const recurringEntryScanLimit = 500

// projectMonth builds what the definitions imply for a month.
//
// Shared by both branches on purpose: the projection a future month returns
// and the rows a current month inserts are produced by the same code, so
// "what October will look like" and "what October became" cannot disagree
// for any reason other than time passing.
func (s *Service) projectMonth(entries []domain.RecurringEntry, p domain.Period, now time.Time) ([]domain.RecurringOccurrence, error) {
	out := make([]domain.RecurringOccurrence, 0, len(entries))
	for i := range entries {
		o, err := domain.NewOccurrence(&entries[i], p, s.loc, now)
		if err != nil {
			return nil, err
		}
		out = append(out, *o)
	}
	return out, nil
}

// decorate attaches the recognition a person needs to each occurrence.
//
// The categories are read ONCE and indexed rather than joined per row, the
// same arrangement the tools' categoryIndex uses. An occurrence whose
// definition or category cannot be resolved still appears, with the labels
// missing: the amount is correct and the id is correct, and dropping a real
// obligation because its label was unavailable would make the total wrong
// in order to make the list tidy.
func (s *Service) decorate(
	ctx context.Context, workspaceID uuid.UUID,
	occurrences []domain.RecurringOccurrence, entries []domain.RecurringEntry, today time.Time,
) ([]MonthlyCommitmentLine, error) {
	byEntry := make(map[uuid.UUID]*domain.RecurringEntry, len(entries))
	for i := range entries {
		byEntry[entries[i].ID] = &entries[i]
	}
	cats, err := s.repos.Categories.List(ctx, workspaceID, ports.CategoryFilter{Limit: recurringEntryScanLimit})
	if err != nil {
		return nil, err
	}
	byCategory := make(map[uuid.UUID]domain.Category, len(cats))
	for _, c := range cats {
		byCategory[c.ID] = c
	}

	lines := make([]MonthlyCommitmentLine, 0, len(occurrences))
	for i := range occurrences {
		o := occurrences[i]
		line := MonthlyCommitmentLine{
			Occurrence:       o,
			RecurringEntryID: o.RecurringEntryID,
			Overdue:          o.Overdue(today),
		}
		if f, ok := byEntry[o.RecurringEntryID]; ok {
			line.Description = f.Description
			line.CategoryID = f.CategoryID
			if c, ok := byCategory[f.CategoryID]; ok {
				line.CategoryName = c.Name
				line.Direction = c.Type
			}
		}
		lines = append(lines, line)
	}
	return lines, nil
}

// todayIn is the current calendar date in the reporting zone, as a civil
// date at midnight UTC — the form Occurrence.Overdue and Period.DueOn both
// speak. See domain/period.go on why a date is not an instant.
func (s *Service) todayIn(now time.Time) time.Time {
	d := now.In(s.loc)
	return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC)
}

/* ── writing one month ───────────────────────────────────────────────── */

// OccurrenceRef names one month of one recurring entry.
//
// ── Why the pair and not the occurrence's own id ───────────────────────
// Because the pair is what a caller can KNOW. A model has just read a month
// and holds the definition's id and the month it asked for; an occurrence
// id is a third opaque value it would have to carry correctly through a
// turn, and the one thing this module has watched a model do with an opaque
// value it half-remembers is invent a plausible one.
//
// The pair is also idempotent under retry in a way an id is not: re-issuing
// "mark the internet's September paid" addresses the same row however many
// times it arrives, which is exactly what a retried agent call needs.
type OccurrenceRef struct {
	WorkspaceID      uuid.UUID
	RecurringEntryID uuid.UUID
	Period           domain.Period
}

// validate checks what a caller must supply.
//
// The PERIOD is deliberately not among them. A zero Period means "the
// current month", resolved from the database clock in settleableOccurrence
// — the same rule GetMonthlyCommitment follows, and for the same reason:
// nothing in a conversational turn tells a model what month it is, so a
// month it supplied unprompted would be one it composed. "Paguei a
// internet" means this month, and the server is what knows which one that
// is.
func (r OccurrenceRef) validate() error {
	if r.WorkspaceID == uuid.Nil {
		return domain.Invalid("workspace_id required")
	}
	if r.RecurringEntryID == uuid.Nil {
		return domain.Invalid("recurring_entry_id required")
	}
	return nil
}

// MarkOccurrencePaid settles one month of one obligation.
//
// ══════════════════════════════════════════════════════════════════════
//
//	MARK PAID, NOT TOGGLE
//
// ══════════════════════════════════════════════════════════════════════
//
// ── Why there is no toggle anywhere in this module ─────────────────────
// Because a toggle's meaning depends on a state the caller cannot see at
// the moment it calls. A retried HTTP request, a re-sent agent tool call, a
// double-tap on a phone: with a toggle, the second one UNDOES the first,
// and neither the user nor the model has any way to notice. Marking paid
// twice here is a no-op that succeeds, so a retry is always safe and always
// means what it says.
//
// The UI may look like a checkbox. The commands underneath are MARK PAID
// and MARK PENDING.
//
// ── What it deliberately does not do ───────────────────────────────────
// It writes NO transaction, sets no transaction_id, and does not touch the
// definition or the amount. Ticking a bill is the operator asserting that
// an obligation was settled; the ledger records money that moved, and only
// the ledger may claim it did.
//
// ── Why paid_at is not an argument ─────────────────────────────────────
// Same reason `occurred_at` is not one at the agent boundary: nothing in a
// turn tells a model what day it is, so a date it supplied would be one it
// composed. The instant comes from Postgres, which is also the clock the
// month was cut against.
func (s *Service) MarkOccurrencePaid(ctx context.Context, ref OccurrenceRef) (*domain.RecurringOccurrence, error) {
	if err := ref.validate(); err != nil {
		return nil, err
	}
	var out *domain.RecurringOccurrence
	err := s.txm.WithinTx(ctx, func(ctx context.Context) error {
		now, o, err := s.settleableOccurrence(ctx, ref)
		if err != nil {
			return err
		}
		// Already settled: a safe no-op, NOT a toggle. The recorded
		// payment date is left exactly where it is, because a date that
		// moves on a retry is not a record.
		if o.Status == domain.OccurrencePaid {
			out = o
			return nil
		}
		if err := o.MarkPaid(now); err != nil {
			return err
		}
		if err := s.repos.RecurringOccurrences.Update(ctx, o); err != nil {
			return err
		}
		out = o
		return nil
	})
	return out, err
}

// UnmarkOccurrencePaid undoes a mistaken tick.
//
// Idempotent in the same way and for the same reasons. It NEVER deletes a
// transaction and NEVER changes the amount: being wrong about whether a
// bill was paid says nothing about whether it was the right figure, and a
// checkbox must not be able to reach into the ledger.
func (s *Service) UnmarkOccurrencePaid(ctx context.Context, ref OccurrenceRef) (*domain.RecurringOccurrence, error) {
	if err := ref.validate(); err != nil {
		return nil, err
	}
	var out *domain.RecurringOccurrence
	err := s.txm.WithinTx(ctx, func(ctx context.Context) error {
		_, o, err := s.settleableOccurrence(ctx, ref)
		if err != nil {
			return err
		}
		if o.Status == domain.OccurrencePending {
			out = o
			return nil
		}
		o.UnmarkPaid()
		if err := s.repos.RecurringOccurrences.Update(ctx, o); err != nil {
			return err
		}
		out = o
		return nil
	})
	return out, err
}

type SetOccurrenceAmountInput struct {
	OccurrenceRef
	AmountCents int64
}

// SetOccurrenceAmount records what a month actually cost.
//
// This is the variable-amount path: the electricity bill arrives, and the
// estimate the month was materialised with is replaced by the real figure.
// Clearing AmountEstimated is what lets the monthly total report how much
// of itself is still a guess.
//
// ── Why an already-paid month is REFUSED rather than updated ───────────
// Because at that point two facts are on record — an amount and a
// settlement — and silently moving the first would leave "paid R$ 420" and
// "R$ 437,20 due" in the same row with nothing saying which the operator
// agreed to. The rule chosen is the one that cannot be wrong without
// somebody noticing: unmark it, set the amount, mark it paid again. Three
// deliberate acts, each with its own receipt, instead of one that quietly
// rewrites a settled month.
//
// It does NOT touch the definition's default amount. "A luz veio 437,20
// esse mês" is a fact about this month; the recurrence's estimate is a
// separate thing the operator changes when they mean to.
func (s *Service) SetOccurrenceAmount(ctx context.Context, in SetOccurrenceAmountInput) (*domain.RecurringOccurrence, error) {
	if err := in.validate(); err != nil {
		return nil, err
	}
	if in.AmountCents <= 0 {
		return nil, domain.Invalid("amount_cents must be > 0")
	}
	var out *domain.RecurringOccurrence
	err := s.txm.WithinTx(ctx, func(ctx context.Context) error {
		_, o, err := s.settleableOccurrence(ctx, in.OccurrenceRef)
		if err != nil {
			return err
		}
		if o.Status == domain.OccurrencePaid {
			return domain.Conflict(
				"this month is already marked paid, so its amount is not changed in place; " +
					"mark it pending first, set the amount, then mark it paid again")
		}
		if err := o.SetAmountCents(in.AmountCents); err != nil {
			return err
		}
		if err := s.repos.RecurringOccurrences.Update(ctx, o); err != nil {
			return err
		}
		out = o
		return nil
	})
	return out, err
}

// settleableOccurrence resolves one month and refuses the ones that may not
// be written.
//
// The single gate all three writes pass through, so "a month that has not
// begun cannot be paid" is one rule in one place rather than three copies
// free to disagree. It returns the authoritative instant alongside the row,
// because the caller that settles needs the same reading that decided the
// month was settleable.
func (s *Service) settleableOccurrence(ctx context.Context, ref OccurrenceRef) (time.Time, *domain.RecurringOccurrence, error) {
	now, err := s.repos.Clock.Now(ctx)
	if err != nil {
		return time.Time{}, nil, err
	}
	current := domain.PeriodOf(now, s.loc)
	// An omitted month is THIS month, from the database's clock. See
	// OccurrenceRef.validate: "paguei a internet" names no month, and the
	// server is the only party that knows which one it is.
	period := ref.Period
	if period.IsZero() {
		period = current
	}
	// ── A projection has nothing to write to ────────────────────────
	// A future month is arithmetic, not rows. Refused explicitly rather
	// than left to fail as a not-found, because the two mean different
	// things: "that month has not begun" is a fact the caller can act on,
	// and "no such row" would invite it to go and create one.
	if period.After(current) {
		return time.Time{}, nil, domain.Invalid(
			"this month has not begun, so it has no recorded obligations yet; " +
				"a future month is a projection and nothing in it can be marked")
	}
	o, err := s.repos.RecurringOccurrences.FindByEntryPeriod(ctx, ref.WorkspaceID, ref.RecurringEntryID, period)
	if err != nil {
		return time.Time{}, nil, err
	}
	return now, o, nil
}
