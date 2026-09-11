package app

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/finance/domain"
	"github.com/corsi/backend/internal/finance/ports"
)

type CreateTransactionInput struct {
	WorkspaceID uuid.UUID
	AccountID   *uuid.UUID
	CategoryID  uuid.UUID
	PersonID    *uuid.UUID
	AmountCents int64
	Description string
	OccurredAt  time.Time
	// All four metadata fields are optional on the wire; the service applies
	// safe defaults (paid / pix / manual / "") when callers omit them, so
	// older clients keep working unchanged.
	Status        *domain.TransactionStatus
	PaymentMethod *domain.PaymentMethod
	Source        *domain.TransactionSource
	Notes         *string
	// ExternalSource + ExternalID mark a row that came from somewhere else.
	//
	// No capability and no HTTP handler sets them today: they exist so a
	// future import can be idempotent without a constraint being retrofitted
	// onto a populated table. See domain.Transaction.
	ExternalSource *string
	ExternalID     *string
}

// CreateTransaction derives `type` from the referenced category so the API
// only requires `category_id` + amount + occurred_at, and so the type can
// never drift from the category's type. The DB composite FK is a belt around
// these suspenders.
func (s *Service) CreateTransaction(ctx context.Context, in CreateTransactionInput) (*domain.Transaction, error) {
	cat, err := s.repos.Categories.FindByID(ctx, in.WorkspaceID, in.CategoryID)
	if err != nil {
		return nil, err
	}
	// person_id FK is workspace-blind at the DB level (any workspace's person
	// would satisfy it), so the service guards isolation by resolving the
	// person in the same workspace before insert. Wrong-workspace → not_found.
	if in.PersonID != nil {
		if _, err := s.repos.Persons.FindByID(ctx, in.WorkspaceID, *in.PersonID); err != nil {
			return nil, err
		}
	}
	t := &domain.Transaction{
		ID:            uuid.New(),
		WorkspaceID:   in.WorkspaceID,
		AccountID:     in.AccountID,
		CategoryID:    in.CategoryID,
		Type:          cat.Type,
		PersonID:      in.PersonID,
		AmountCents:   in.AmountCents,
		Description:   strings.TrimSpace(in.Description),
		OccurredAt:    in.OccurredAt,
		Status:        derefOr(in.Status, domain.TransactionStatusPaid),
		PaymentMethod: derefOr(in.PaymentMethod, domain.PaymentMethodPix),
		Source:        derefOr(in.Source, domain.TransactionSourceManual),
		Notes:         strings.TrimSpace(derefOrString(in.Notes, "")),

		ExternalSource: in.ExternalSource,
		ExternalID:     in.ExternalID,
	}
	if err := t.Validate(); err != nil {
		return nil, err
	}
	if err := s.repos.Transactions.Create(ctx, t); err != nil {
		return nil, err
	}
	return t, nil
}

func derefOr[T ~string](p *T, fallback T) T {
	if p == nil {
		return fallback
	}
	return *p
}

func derefOrString(p *string, fallback string) string {
	if p == nil {
		return fallback
	}
	return *p
}

type UpdateTransactionInput struct {
	WorkspaceID   uuid.UUID
	ID            uuid.UUID
	AccountID     *uuid.UUID
	CategoryID    *uuid.UUID
	PersonID      *uuid.UUID
	AmountCents   *int64
	Description   *string
	OccurredAt    *time.Time
	Status        *domain.TransactionStatus
	PaymentMethod *domain.PaymentMethod
	Source        *domain.TransactionSource
	Notes         *string

	// Explicit clears so PATCH can unset nullable fields.
	ClearAccountID bool
	ClearPersonID  bool
}

func (s *Service) UpdateTransaction(ctx context.Context, in UpdateTransactionInput) (*domain.Transaction, error) {
	current, err := s.repos.Transactions.FindByID(ctx, in.WorkspaceID, in.ID)
	if err != nil {
		return nil, err
	}

	if in.ClearAccountID {
		current.AccountID = nil
	} else if in.AccountID != nil {
		current.AccountID = in.AccountID
	}
	if in.ClearPersonID {
		current.PersonID = nil
	} else if in.PersonID != nil {
		if _, err := s.repos.Persons.FindByID(ctx, in.WorkspaceID, *in.PersonID); err != nil {
			return nil, err
		}
		current.PersonID = in.PersonID
	}
	if in.CategoryID != nil && *in.CategoryID != current.CategoryID {
		cat, err := s.repos.Categories.FindByID(ctx, in.WorkspaceID, *in.CategoryID)
		if err != nil {
			return nil, err
		}
		current.CategoryID = cat.ID
		current.Type = cat.Type
	}
	if in.AmountCents != nil {
		current.AmountCents = *in.AmountCents
	}
	if in.Description != nil {
		current.Description = strings.TrimSpace(*in.Description)
	}
	if in.OccurredAt != nil {
		current.OccurredAt = *in.OccurredAt
	}
	if in.Status != nil {
		current.Status = *in.Status
	}
	if in.PaymentMethod != nil {
		current.PaymentMethod = *in.PaymentMethod
	}
	if in.Source != nil {
		current.Source = *in.Source
	}
	if in.Notes != nil {
		current.Notes = strings.TrimSpace(*in.Notes)
	}
	if err := current.Validate(); err != nil {
		return nil, err
	}
	if err := s.repos.Transactions.Update(ctx, current); err != nil {
		return nil, err
	}
	return current, nil
}

func (s *Service) DeleteTransaction(ctx context.Context, workspaceID, id uuid.UUID) error {
	// Reject leg-deletion via the per-transaction endpoint — deleting one
	// leg would leave the pair orphaned (one half visible, one half gone)
	// and break the transfer invariant. Callers must hit DELETE
	// /finance/transfers/{pair_id} instead, which removes both legs in a tx.
	current, err := s.repos.Transactions.FindByID(ctx, workspaceID, id)
	if err != nil {
		return err
	}
	if current.TransferPairID != nil {
		return domain.Conflict("delete the transfer pair instead")
	}
	return s.repos.Transactions.SoftDelete(ctx, workspaceID, id)
}

// Now is the instant this bounded context reckons by.
//
// Exposed on the application service, rather than left to each caller's
// process clock, because the REALIZED/PROJECTED split is computed against
// the database's `now()`. A surface that needs to stamp "this is happening
// now" has to ask the same clock the classification will use, or it will
// occasionally record the present as the future. See ports.Clock.
func (s *Service) Now(ctx context.Context) (time.Time, error) {
	return s.repos.Clock.Now(ctx)
}

func (s *Service) GetTransaction(ctx context.Context, workspaceID, id uuid.UUID) (*domain.Transaction, error) {
	return s.repos.Transactions.FindByID(ctx, workspaceID, id)
}

type ListTransactionsInput struct {
	WorkspaceID uuid.UUID
	Type        *domain.EntryType
	CategoryID  *uuid.UUID
	Status      *domain.TransactionStatus
	From        *time.Time
	To          *time.Time
	// Search, the amount bounds and Order narrow and reorder the same
	// query the feed already runs. They live here rather than in a caller
	// because the questions they answer — "the biggest ones", "anything
	// around R$ 199", "that Amazon purchase" — are about the whole matching
	// set, and a caller that had to download the window to answer them
	// would be a second aggregation with its own idea of the rules.
	Search         string
	MinAmountCents *int64
	MaxAmountCents *int64
	Order          ports.TransactionOrder
	Limit          int
	Offset         int
	// Optional cursor. Mutually exclusive with Offset — the HTTP layer
	// rejects requests that send both.
	CursorOccurredAt *time.Time
	CursorID         *uuid.UUID
}

// ListTransactionsResult bundles a page of results with the next-page
// cursor (or nil when this was the final page).
type ListTransactionsResult struct {
	Items      []domain.Transaction
	NextCursor *string
}

func (s *Service) ListTransactions(ctx context.Context, in ListTransactionsInput) (ListTransactionsResult, error) {
	// Read one row past the page. That surplus row is never returned to the
	// caller: it exists only as *evidence* that another page follows.
	//
	// The alternative — treating "the page came back full" as the signal —
	// is a guess, and it is wrong exactly when the remaining row count is a
	// multiple of the limit: the final page of data looks full, advertises a
	// successor, and the client spends a round trip to discover an empty
	// page. Reading limit+1 turns the guess into a fact.
	if !in.Order.Valid() {
		return ListTransactionsResult{}, domain.Invalid("unsupported ordering")
	}
	// ── Why the combination is refused rather than reconciled ───────────
	// The cursor encodes (occurred_at, id), which is a position in the
	// recency sequence and means nothing in an amount-ordered one. Applying
	// it anyway would return a page that quietly drops rows, and a page that
	// silently drops rows is worse than an error: the caller sums it.
	if in.Order != ports.OrderRecent && (in.CursorOccurredAt != nil || in.CursorID != nil) {
		return ListTransactionsResult{}, domain.Invalid("cursor paging is only available in the default ordering")
	}
	if in.MinAmountCents != nil && in.MaxAmountCents != nil && *in.MinAmountCents > *in.MaxAmountCents {
		return ListTransactionsResult{}, domain.Invalid("min_amount_cents must be <= max_amount_cents")
	}

	fetch := in.Limit
	if fetch > 0 {
		fetch++
	}

	items, err := s.repos.Transactions.List(ctx, in.WorkspaceID, ports.TransactionFilter{
		Type:             in.Type,
		CategoryID:       in.CategoryID,
		Status:           in.Status,
		From:             in.From,
		To:               in.To,
		Search:           in.Search,
		MinAmountCents:   in.MinAmountCents,
		MaxAmountCents:   in.MaxAmountCents,
		Order:            in.Order,
		Limit:            fetch,
		Offset:           in.Offset,
		CursorOccurredAt: in.CursorOccurredAt,
		CursorID:         in.CursorID,
	})
	if err != nil {
		return ListTransactionsResult{}, err
	}

	hasMore := in.Limit > 0 && len(items) > in.Limit
	if hasMore {
		items = items[:in.Limit]
	}

	// next_cursor answers "where do I resume?", so it is emitted whenever a
	// next page exists — including on the very first request, which carries
	// no cursor of its own. Gating emission on the *request* already having
	// a cursor is what made cursor pagination unreachable: there
	// was no way to obtain the first one.
	//
	// Offset-mode callers get it too. The field is optional on the wire, so
	// they may ignore it; for those that want to stop shifting under
	// concurrent writes, it is the migration path to keyset paging.
	var next *string
	// Only the recency ordering has a cursor, because only it has a tuple
	// that identifies a position. Emitting one here for OrderLargest would
	// hand back a token that resumes a DIFFERENT sequence.
	if hasMore && in.Order == ports.OrderRecent {
		last := items[len(items)-1]
		c := EncodeCursor(Cursor{OccurredAt: last.OccurredAt, ID: last.ID})
		next = &c
	}
	return ListTransactionsResult{Items: items, NextCursor: next}, nil
}

// GetTransactionTotalsInput / Totals are the read-side aggregation API.
// Both ends of the window are required; the service caps the span at
// 366 days so a malicious or careless caller can't ask for years.
type GetTransactionTotalsInput struct {
	WorkspaceID uuid.UUID
	From        time.Time
	To          time.Time
}

func (s *Service) GetTransactionTotals(ctx context.Context, in GetTransactionTotalsInput) (ports.TransactionTotals, error) {
	if in.From.IsZero() || in.To.IsZero() {
		return ports.TransactionTotals{}, domain.Invalid("from and to are required")
	}
	if !in.From.Before(in.To) {
		return ports.TransactionTotals{}, domain.Invalid("from must be before to")
	}
	if in.To.Sub(in.From) > 366*24*time.Hour {
		return ports.TransactionTotals{}, domain.Invalid("totals window must be <= 366 days")
	}
	return s.repos.Transactions.Totals(ctx, in.WorkspaceID, ports.TransactionTotalsFilter{
		From: in.From,
		To:   in.To,
	})
}
