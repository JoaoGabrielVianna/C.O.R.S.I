package domain

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

// PurchasePlan models a single purchase paid across N installments. The
// installments themselves live as ordinary Transactions tagged with
// plan_id + installment_number; the plan record is the parent.
//
// See docs/totals-contract.md §3: each installment is classified
// realized/projected by the same rules as any transaction. The plan in
// aggregate is never classified on its own.
type PurchasePlan struct {
	ID                    uuid.UUID  `json:"id"`
	WorkspaceID           uuid.UUID  `json:"workspace_id"`
	PersonID              *uuid.UUID `json:"person_id,omitempty"`
	Name                  string     `json:"name"`
	TotalAmountCents      int64      `json:"total_amount_cents"`
	Installments          int        `json:"installments"`
	RemainingInstallments int        `json:"remaining_installments"`
	CreatedAt             time.Time  `json:"created_at"`
	DeletedAt             *time.Time `json:"deleted_at,omitempty"`
}

func (p *PurchasePlan) Validate() error {
	if p.WorkspaceID == uuid.Nil {
		return Invalid("workspace_id required")
	}
	if n := strings.TrimSpace(p.Name); n == "" || len(n) > 80 {
		return Invalid("name must be 1..80 chars")
	}
	if p.TotalAmountCents <= 0 {
		return Invalid("total_amount_cents must be > 0")
	}
	if p.Installments < 2 || p.Installments > 360 {
		return Invalid("installments must be between 2 and 360")
	}
	if p.RemainingInstallments < 0 || p.RemainingInstallments > p.Installments {
		return Invalid("remaining_installments out of range")
	}
	return nil
}
