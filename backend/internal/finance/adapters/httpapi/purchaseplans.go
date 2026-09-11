package httpapi

import (
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/finance/app"
	"github.com/corsi/backend/internal/finance/domain"
	"github.com/corsi/backend/internal/platform/apierror"
	"github.com/corsi/backend/internal/platform/render"
)

// createPurchasePlanReq is the wire shape for POST /purchase-plans. The
// handler hands it straight to Service.CreatePurchasePlan, which writes
// the parent plan row and the N installment transactions inside one tx
// — see the service comment for rollback semantics.
type createPurchasePlanReq struct {
	CategoryID       uuid.UUID                 `json:"category_id"`
	PersonID         *uuid.UUID                `json:"person_id,omitempty"`
	AccountID        *uuid.UUID                `json:"account_id,omitempty"`
	Name             string                    `json:"name"`
	TotalAmountCents int64                     `json:"total_amount_cents"`
	Installments     int                       `json:"installments"`
	FirstOccurredAt  time.Time                 `json:"first_occurred_at"`
	PaymentMethod    *domain.PaymentMethod     `json:"payment_method,omitempty"`
	Source           *domain.TransactionSource `json:"source,omitempty"`
	Notes            string                    `json:"notes,omitempty"`
}

func (h *Handler) createPurchasePlan(w http.ResponseWriter, r *http.Request) {
	wsID, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	var req createPurchasePlanReq
	if err := render.DecodeJSON(r, &req); err != nil {
		apierror.Write(w, h.log, apierror.New(http.StatusBadRequest, "malformed_body", err.Error()))
		return
	}
	p, err := h.svc.CreatePurchasePlan(r.Context(), app.CreatePurchasePlanInput{
		WorkspaceID:      wsID,
		PersonID:         req.PersonID,
		CategoryID:       req.CategoryID,
		AccountID:        req.AccountID,
		Name:             req.Name,
		TotalAmountCents: req.TotalAmountCents,
		Installments:     req.Installments,
		FirstOccurredAt:  req.FirstOccurredAt,
		PaymentMethod:    req.PaymentMethod,
		Source:           req.Source,
		Notes:            req.Notes,
	})
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusCreated, p)
}

func (h *Handler) getPurchasePlan(w http.ResponseWriter, r *http.Request) {
	wsID, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		apierror.Write(w, h.log, apierror.New(http.StatusBadRequest, "invalid_id", "id must be a uuid"))
		return
	}
	p, err := h.svc.GetPurchasePlan(r.Context(), wsID, id)
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusOK, p)
}

func (h *Handler) listPurchasePlans(w http.ResponseWriter, r *http.Request) {
	wsID, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	limit, offset := parseLimitOffset(r)
	items, err := h.svc.ListPurchasePlans(r.Context(), app.ListPurchasePlansInput{
		WorkspaceID: wsID, Limit: limit, Offset: offset,
	})
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusOK, listEnvelope{Items: items, Limit: limit, Offset: offset})
}

func (h *Handler) cancelPurchasePlan(w http.ResponseWriter, r *http.Request) {
	wsID, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		apierror.Write(w, h.log, apierror.New(http.StatusBadRequest, "invalid_id", "id must be a uuid"))
		return
	}
	p, err := h.svc.CancelPurchasePlan(r.Context(), wsID, id)
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusOK, p)
}
