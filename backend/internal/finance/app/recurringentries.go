package app

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/finance/domain"
	"github.com/corsi/backend/internal/finance/ports"
)

// Recurring entries.
//
// ── The one rule this file exists to hold ──────────────────────────────
// Creating a recurring entry does NOT create a transaction, and nothing
// here writes to the transactions table. A recurrence says something
// repeats; a transaction says money moved. Materialising the first into the
// second would put amounts in the ledger that nobody confirmed, and after
// the fact they would be indistinguishable from real ones in every total
// this module computes.
//
// So the two are related only by the question a person asks about them
// together, and that question is answered by reading both — see
// RecurringSummary below.

type CreateRecurringEntryInput struct {
	WorkspaceID uuid.UUID
	Description string
	AmountCents int64
	CategoryID  uuid.UUID
	PersonID    *uuid.UUID
	DueDay      int
	Recurrence  *domain.RecurringFrequency
	StartsAt    *time.Time
	Notes       *string
}

func (s *Service) CreateRecurringEntry(ctx context.Context, in CreateRecurringEntryInput) (*domain.RecurringEntry, error) {
	// The category is resolved inside the workspace before anything is
	// written — the same guard CreateTransaction applies, and for the same
	// reason: the database FK is workspace-blind, so another workspace's
	// category would satisfy it.
	cat, err := s.repos.Categories.FindByID(ctx, in.WorkspaceID, in.CategoryID)
	if err != nil {
		return nil, err
	}
	// ── Both directions are allowed, and the category decides which ─────
	// A recurring entry is money that repeats — a salary as much as a rent.
	// The direction is NOT stored: it is read through the category, the
	// same way a transaction's `type` is derived from the category it
	// references. Refusing income here is what made a salary
	// unrepresentable and pointed at a second table beside this one.
	if in.PersonID != nil {
		if _, err := s.repos.Persons.FindByID(ctx, in.WorkspaceID, *in.PersonID); err != nil {
			return nil, err
		}
	}

	// ── Why the start date comes from the database ──────────────────────
	// Because `ActiveAt` compares it against the database's clock, and the
	// two must be the same clock. Stamped from this process instead, a
	// entry created a moment ago starts in the database's FUTURE
	// whenever the API's clock runs even milliseconds ahead — and it is then
	// excluded from the monthly total it was just added to, silently. That
	// is the same defect the realized/projected split had; see ports.Clock.
	startsAt, err := s.repos.Clock.Now(ctx)
	if err != nil {
		return nil, err
	}
	if in.StartsAt != nil {
		startsAt = *in.StartsAt
	}
	f := &domain.RecurringEntry{
		ID:          uuid.New(),
		WorkspaceID: in.WorkspaceID,
		Description: strings.TrimSpace(in.Description),
		AmountCents: in.AmountCents,
		CategoryID:  cat.ID,
		PersonID:    in.PersonID,
		DueDay:      in.DueDay,
		Recurrence:  derefOr(in.Recurrence, domain.RecurrenceMonthly),
		Status:      domain.StatusActive,
		StartsAt:    startsAt,
		Notes:       strings.TrimSpace(derefOrString(in.Notes, "")),
	}
	if err := f.Validate(); err != nil {
		return nil, err
	}
	if err := s.repos.RecurringEntries.Create(ctx, f); err != nil {
		return nil, err
	}
	return f, nil
}

type UpdateRecurringEntryInput struct {
	WorkspaceID uuid.UUID
	ID          uuid.UUID
	Description *string
	AmountCents *int64
	CategoryID  *uuid.UUID
	PersonID    *uuid.UUID
	DueDay      *int
	Recurrence  *domain.RecurringFrequency
	Status      *domain.RecurringStatus
	Notes       *string
	// EndsAt records a cancellation. ClearEndsAt reinstates a commitment
	// that was cancelled — both are explicit so "omitted" can keep meaning
	// "unchanged".
	EndsAt      *time.Time
	ClearEndsAt bool
	ClearPerson bool
}

func (s *Service) UpdateRecurringEntry(ctx context.Context, in UpdateRecurringEntryInput) (*domain.RecurringEntry, error) {
	current, err := s.repos.RecurringEntries.FindByID(ctx, in.WorkspaceID, in.ID)
	if err != nil {
		return nil, err
	}
	if in.Description != nil {
		current.Description = strings.TrimSpace(*in.Description)
	}
	if in.AmountCents != nil {
		current.AmountCents = *in.AmountCents
	}
	if in.CategoryID != nil && *in.CategoryID != current.CategoryID {
		cat, err := s.repos.Categories.FindByID(ctx, in.WorkspaceID, *in.CategoryID)
		if err != nil {
			return nil, err
		}
		current.CategoryID = cat.ID
	}
	if in.ClearPerson {
		current.PersonID = nil
	} else if in.PersonID != nil {
		if _, err := s.repos.Persons.FindByID(ctx, in.WorkspaceID, *in.PersonID); err != nil {
			return nil, err
		}
		current.PersonID = in.PersonID
	}
	if in.DueDay != nil {
		current.DueDay = *in.DueDay
	}
	if in.Recurrence != nil {
		current.Recurrence = *in.Recurrence
	}
	if in.Status != nil {
		current.Status = *in.Status
	}
	if in.ClearEndsAt {
		current.EndsAt = nil
	} else if in.EndsAt != nil {
		current.EndsAt = in.EndsAt
	}
	if in.Notes != nil {
		current.Notes = strings.TrimSpace(*in.Notes)
	}
	if err := current.Validate(); err != nil {
		return nil, err
	}
	if err := s.repos.RecurringEntries.Update(ctx, current); err != nil {
		return nil, err
	}
	return current, nil
}

// DeleteRecurringEntry removes the record entirely (soft).
//
// ── Why this is not how a cancellation is recorded ─────────────────────
// Cancelling a gym membership is a fact about the future: it was real until
// a date and is not after it. That is EndsAt, and the row stays, so a
// question about last March still has an answer. Deleting is for an entry
// that should never have been written down — a typo, a duplicate — and it
// takes the history with it.
func (s *Service) DeleteRecurringEntry(ctx context.Context, workspaceID, id uuid.UUID) error {
	return s.repos.RecurringEntries.SoftDelete(ctx, workspaceID, id)
}

func (s *Service) GetRecurringEntry(ctx context.Context, workspaceID, id uuid.UUID) (*domain.RecurringEntry, error) {
	return s.repos.RecurringEntries.FindByID(ctx, workspaceID, id)
}

type ListRecurringEntriesInput struct {
	WorkspaceID uuid.UUID
	CategoryID  *uuid.UUID
	ActiveOnly  bool
	Limit       int
	Offset      int
}

func (s *Service) ListRecurringEntries(ctx context.Context, in ListRecurringEntriesInput) ([]domain.RecurringEntry, error) {
	return s.repos.RecurringEntries.List(ctx, in.WorkspaceID, ports.RecurringEntryFilter{
		CategoryID: in.CategoryID,
		ActiveOnly: in.ActiveOnly,
		Limit:      in.Limit,
		Offset:     in.Offset,
	})
}

/* ── the aggregate ───────────────────────────────────────────────────── */

// RecurringLine is one entry as it contributes to a monthly figure.
type RecurringLine struct {
	Entry domain.RecurringEntry
	// Direction is read from the entry's category. It is not stored on the
	// entry — see the note in domain/recurringentry.go.
	Direction domain.EntryType
	// MonthlyCents is the amount normalised to a month: the amount itself
	// for a monthly entry, a twelfth for an annual one. Always positive;
	// the direction says which way it points.
	MonthlyCents int64
}

// RecurringSummary is what repeats every month, in both directions.
//
// ── Why income and expense are separate fields and not one signed sum ──
// Because "quanto entra e quanto sai" is two facts, and a person planning a
// month needs both. A single net figure hides the case that matters most:
// R$ 8.000 in and R$ 7.900 out nets to R$ 100, and so does R$ 200 in and
// R$ 100 out. Net is offered as well, computed here so no caller subtracts
// two large integers in prose.
//
// ── Why this is NOT part of GetTransactionTotals ───────────────────────
// Because that function answers a question with a frozen contract —
// realized, projected, excluded, over transactions — and
// docs/totals-contract.md is explicit that its buckets classify rows in
// `finance.transactions`. A recurring entry is not a row there. Folding it
// in would change what `realized` and `projected` have meant since the
// contract was frozen, silently, for every consumer that already reads
// them.
//
// The two readings can be presented side by side; they cannot be added.
// A recurrence already paid this month is ALSO a transaction, and summing
// both would count it twice.
type RecurringSummary struct {
	// At is the moment the entry set was evaluated.
	At time.Time
	// IncomeMonthlyCents / ExpenseMonthlyCents are the normalised totals per
	// direction. NetMonthlyCents is income minus expense and may be negative.
	IncomeMonthlyCents  int64
	ExpenseMonthlyCents int64
	NetMonthlyCents     int64
	Lines               []RecurringLine
}

// GetRecurringSummary reads every entry running right now.
func (s *Service) GetRecurringSummary(ctx context.Context, workspaceID uuid.UUID) (RecurringSummary, error) {
	// The database's clock, not the process's — the same single-clock rule
	// the realized/projected split follows. See ports.Clock.
	now, err := s.repos.Clock.Now(ctx)
	if err != nil {
		return RecurringSummary{}, err
	}
	items, err := s.repos.RecurringEntries.List(ctx, workspaceID, ports.RecurringEntryFilter{Limit: 500})
	if err != nil {
		return RecurringSummary{}, err
	}

	// The direction of every entry comes from its category, so the
	// categories are read once and indexed rather than joined per row.
	//
	// An entry whose category cannot be resolved is SKIPPED rather than
	// guessed at: it has no direction, and defaulting it to expense would
	// quietly subtract somebody's salary.
	cats, err := s.repos.Categories.List(ctx, workspaceID, ports.CategoryFilter{Limit: 500})
	if err != nil {
		return RecurringSummary{}, err
	}
	direction := make(map[uuid.UUID]domain.EntryType, len(cats))
	for _, c := range cats {
		direction[c.ID] = c.Type
	}

	out := RecurringSummary{At: now}
	for i := range items {
		f := items[i]
		// The domain decides what "running now" means, so this loop and the
		// SQL predicate in the repository cannot drift apart into two
		// different definitions.
		if !f.ActiveAt(now) {
			continue
		}
		dir, ok := direction[f.CategoryID]
		if !ok {
			continue
		}
		monthly := f.MonthlyEquivalentCents()
		if dir == domain.EntryTypeIncome {
			out.IncomeMonthlyCents += monthly
		} else {
			out.ExpenseMonthlyCents += monthly
		}
		out.Lines = append(out.Lines, RecurringLine{Entry: f, Direction: dir, MonthlyCents: monthly})
	}
	out.NetMonthlyCents = out.IncomeMonthlyCents - out.ExpenseMonthlyCents
	return out, nil
}
