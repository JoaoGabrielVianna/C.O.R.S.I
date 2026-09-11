package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/finance/domain"
	"github.com/corsi/backend/internal/finance/ports"
)

// CreatePurchasePlanInput is the wire of a "single purchase → N
// installments" command. The service splits `total_amount_cents` across
// `installments` evenly (the last installment absorbs the rounding
// remainder so the sum equals the principal exactly — invariant §5.4 of
// docs/totals-contract.md). All installment rows are created with
// `status = scheduled`; the user marks them paid one by one via PATCH
// /transactions/{id}.
type CreatePurchasePlanInput struct {
	WorkspaceID      uuid.UUID
	PersonID         *uuid.UUID
	CategoryID       uuid.UUID
	AccountID        *uuid.UUID
	Name             string
	TotalAmountCents int64
	Installments     int
	FirstOccurredAt  time.Time
	// PaymentMethod defaults to credit — installment plans almost always
	// ride a credit-card. Callers can override (e.g., a financed deal
	// paid by debit-equivalent transfers).
	PaymentMethod *domain.PaymentMethod
	Source        *domain.TransactionSource
	Notes         string
}

// CreatePurchasePlan writes the parent plan row plus N installment
// transactions atomically inside one tx. Rollback semantics: if any
// installment insert fails (FK, CHECK, db down), the plan row is rolled
// back too — there is no observable half-created state.
func (s *Service) CreatePurchasePlan(ctx context.Context, in CreatePurchasePlanInput) (*domain.PurchasePlan, error) {
	if in.WorkspaceID == uuid.Nil {
		return nil, domain.Invalid("workspace_id required")
	}
	if in.CategoryID == uuid.Nil {
		return nil, domain.Invalid("category_id required")
	}
	if in.FirstOccurredAt.IsZero() {
		return nil, domain.Invalid("first_occurred_at required")
	}
	if in.Installments < 2 || in.Installments > 360 {
		return nil, domain.Invalid("installments must be between 2 and 360")
	}
	if in.TotalAmountCents <= 0 {
		return nil, domain.Invalid("total_amount_cents must be > 0")
	}

	cat, err := s.repos.Categories.FindByID(ctx, in.WorkspaceID, in.CategoryID)
	if err != nil {
		return nil, err
	}
	if cat.Type != domain.EntryTypeExpense {
		return nil, domain.Invalid("category must be an expense category")
	}
	if in.PersonID != nil {
		if _, err := s.repos.Persons.FindByID(ctx, in.WorkspaceID, *in.PersonID); err != nil {
			return nil, err
		}
	}

	plan := &domain.PurchasePlan{
		ID:                    uuid.New(),
		WorkspaceID:           in.WorkspaceID,
		PersonID:              in.PersonID,
		Name:                  strings.TrimSpace(in.Name),
		TotalAmountCents:      in.TotalAmountCents,
		Installments:          in.Installments,
		RemainingInstallments: in.Installments,
	}
	if err := plan.Validate(); err != nil {
		return nil, err
	}

	method := derefOr(in.PaymentMethod, domain.PaymentMethodCredit)
	source := derefOr(in.Source, domain.TransactionSourceManual)
	notes := strings.TrimSpace(in.Notes)

	// Even split with the last row absorbing the rounding remainder, so
	// sum(installment_amount) == TotalAmountCents exactly.
	base := in.TotalAmountCents / int64(in.Installments)
	remainder := in.TotalAmountCents - base*int64(in.Installments)

	installments := make([]*domain.Transaction, in.Installments)
	for i := 0; i < in.Installments; i++ {
		amount := base
		if i == in.Installments-1 {
			amount += remainder
		}
		num := i + 1
		desc := fmt.Sprintf("%s %d/%d", plan.Name, num, in.Installments)
		installments[i] = &domain.Transaction{
			ID:                uuid.New(),
			WorkspaceID:       in.WorkspaceID,
			AccountID:         in.AccountID,
			CategoryID:        cat.ID,
			Type:              cat.Type, // expense
			PersonID:          in.PersonID,
			AmountCents:       amount,
			Description:       desc,
			OccurredAt:        in.FirstOccurredAt.AddDate(0, i, 0),
			Status:            domain.TransactionStatusScheduled,
			PaymentMethod:     method,
			Source:            source,
			Notes:             notes,
			PlanID:            &plan.ID,
			InstallmentNumber: &num,
		}
		if err := installments[i].Validate(); err != nil {
			return nil, err
		}
	}

	if err := s.txm.WithinTx(ctx, func(ctx context.Context) error {
		if err := s.repos.PurchasePlans.Create(ctx, plan); err != nil {
			return err
		}
		for _, t := range installments {
			if err := s.repos.Transactions.Create(ctx, t); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return plan, nil
}

func (s *Service) GetPurchasePlan(ctx context.Context, workspaceID, id uuid.UUID) (*domain.PurchasePlan, error) {
	return s.repos.PurchasePlans.FindByID(ctx, workspaceID, id)
}

type ListPurchasePlansInput struct {
	WorkspaceID uuid.UUID
	Limit       int
	Offset      int
}

func (s *Service) ListPurchasePlans(ctx context.Context, in ListPurchasePlansInput) ([]domain.PurchasePlan, error) {
	return s.repos.PurchasePlans.List(ctx, in.WorkspaceID, ports.PurchasePlanFilter{Limit: in.Limit, Offset: in.Offset})
}

// CancelPurchasePlan soft-deletes every still-scheduled installment of
// the plan inside a single tx. Paid installments are preserved as
// history (contract §3 + §5.4 — the realized side of the plan must not
// retroactively disappear). The plan record itself stays live so the
// history endpoint can still resolve `plan_id` references.
//
// Returns the refreshed plan (remaining_installments will be the count
// of any pending/scheduled rows still alive — normally 0 after cancel).
func (s *Service) CancelPurchasePlan(ctx context.Context, workspaceID, planID uuid.UUID) (*domain.PurchasePlan, error) {
	if workspaceID == uuid.Nil {
		return nil, domain.Invalid("workspace_id required")
	}
	if planID == uuid.Nil {
		return nil, domain.Invalid("plan_id required")
	}
	var refreshed *domain.PurchasePlan
	if err := s.txm.WithinTx(ctx, func(ctx context.Context) error {
		if _, err := s.repos.PurchasePlans.CancelFutureInstallments(ctx, workspaceID, planID); err != nil {
			return err
		}
		p, err := s.repos.PurchasePlans.FindByID(ctx, workspaceID, planID)
		if err != nil {
			return err
		}
		refreshed = p
		return nil
	}); err != nil {
		return nil, err
	}
	return refreshed, nil
}
