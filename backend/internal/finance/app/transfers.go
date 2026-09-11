package app

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/finance/domain"
)

// CreateTransferInput is the application-level shape of a transfer
// command. A transfer is intentionally NOT a new entity — the persistence
// shape is two finance.transactions rows linked by a shared
// transfer_pair_id. The source category must be `expense` and the
// destination category must be `income`; the service derives each leg's
// `type` from its category exactly like CreateTransaction does, so the
// composite (category_id, type) FK keeps holding.
type CreateTransferInput struct {
	WorkspaceID    uuid.UUID
	FromCategoryID uuid.UUID
	ToCategoryID   uuid.UUID
	FromAccountID  *uuid.UUID
	ToAccountID    *uuid.UUID
	AmountCents    int64
	OccurredAt     time.Time
	Description    string
	Notes          *string
}

// CreateTransfer atomically writes the two legs of a transfer inside a
// single tx. Both rows share the same transfer_pair_id; either both land
// or neither does. The legs are excluded from /transactions/totals by the
// repo (transfer_pair_id IS NULL filter) so creating a transfer leaves the
// overview totals unchanged — proven by the integration test.
//
// Defaults match CreateTransaction (status=paid, source=manual, notes="")
// except payment_method, which is forced to `transfer` since that is the
// only payment_method that semantically describes an account-to-account
// move.
func (s *Service) CreateTransfer(ctx context.Context, in CreateTransferInput) (*domain.TransferPair, error) {
	if in.WorkspaceID == uuid.Nil {
		return nil, domain.Invalid("workspace_id required")
	}
	if in.FromCategoryID == uuid.Nil || in.ToCategoryID == uuid.Nil {
		return nil, domain.Invalid("from_category_id and to_category_id required")
	}
	if in.FromCategoryID == in.ToCategoryID {
		return nil, domain.Invalid("from_category_id and to_category_id must differ")
	}
	if in.AmountCents <= 0 {
		return nil, domain.Invalid("amount_cents must be > 0")
	}
	if in.OccurredAt.IsZero() {
		return nil, domain.Invalid("occurred_at required")
	}

	fromCat, err := s.repos.Categories.FindByID(ctx, in.WorkspaceID, in.FromCategoryID)
	if err != nil {
		return nil, err
	}
	if fromCat.Type != domain.EntryTypeExpense {
		return nil, domain.Invalid("from_category must be an expense category")
	}
	toCat, err := s.repos.Categories.FindByID(ctx, in.WorkspaceID, in.ToCategoryID)
	if err != nil {
		return nil, err
	}
	if toCat.Type != domain.EntryTypeIncome {
		return nil, domain.Invalid("to_category must be an income category")
	}

	pairID := uuid.New()
	desc := strings.TrimSpace(in.Description)
	notes := strings.TrimSpace(derefOrString(in.Notes, ""))
	method := domain.PaymentMethodTransfer

	from := &domain.Transaction{
		ID:             uuid.New(),
		WorkspaceID:    in.WorkspaceID,
		AccountID:      in.FromAccountID,
		CategoryID:     in.FromCategoryID,
		Type:           domain.EntryTypeExpense,
		AmountCents:    in.AmountCents,
		Description:    desc,
		OccurredAt:     in.OccurredAt,
		Status:         domain.TransactionStatusPaid,
		PaymentMethod:  method,
		Source:         domain.TransactionSourceManual,
		Notes:          notes,
		TransferPairID: &pairID,
	}
	to := &domain.Transaction{
		ID:             uuid.New(),
		WorkspaceID:    in.WorkspaceID,
		AccountID:      in.ToAccountID,
		CategoryID:     in.ToCategoryID,
		Type:           domain.EntryTypeIncome,
		AmountCents:    in.AmountCents,
		Description:    desc,
		OccurredAt:     in.OccurredAt,
		Status:         domain.TransactionStatusPaid,
		PaymentMethod:  method,
		Source:         domain.TransactionSourceManual,
		Notes:          notes,
		TransferPairID: &pairID,
	}
	if err := from.Validate(); err != nil {
		return nil, err
	}
	if err := to.Validate(); err != nil {
		return nil, err
	}

	if err := s.txm.WithinTx(ctx, func(ctx context.Context) error {
		if err := s.repos.Transactions.Create(ctx, from); err != nil {
			return err
		}
		return s.repos.Transactions.Create(ctx, to)
	}); err != nil {
		return nil, err
	}

	return &domain.TransferPair{From: *from, To: *to}, nil
}

// DeleteTransfer soft-deletes both legs of a transfer atomically. The
// per-transaction DELETE endpoint rejects leg ids (409) so this is the
// only safe way to remove a transfer — without it a caller could orphan
// a pair by deleting one half.
//
// Not-found if zero rows match: either the pair never existed in this
// workspace, or both legs are already deleted.
func (s *Service) DeleteTransfer(ctx context.Context, workspaceID, pairID uuid.UUID) error {
	if workspaceID == uuid.Nil {
		return domain.Invalid("workspace_id required")
	}
	if pairID == uuid.Nil {
		return domain.Invalid("pair_id required")
	}
	return s.txm.WithinTx(ctx, func(ctx context.Context) error {
		n, err := s.repos.Transactions.SoftDeleteByTransferPair(ctx, workspaceID, pairID)
		if err != nil {
			return err
		}
		if n == 0 {
			return domain.NotFound("transfer")
		}
		return nil
	})
}
