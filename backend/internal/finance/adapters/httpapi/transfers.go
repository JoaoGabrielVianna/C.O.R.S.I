package httpapi

import (
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/finance/app"
	"github.com/corsi/backend/internal/platform/apierror"
	"github.com/corsi/backend/internal/platform/render"
)

// createTransferReq is the wire shape for POST /transfers. The handler
// hands it straight to Service.CreateTransfer, which validates that the
// from/to categories are expense/income respectively and atomically
// writes both legs inside a single tx.
type createTransferReq struct {
	FromCategoryID uuid.UUID  `json:"from_category_id"`
	ToCategoryID   uuid.UUID  `json:"to_category_id"`
	FromAccountID  *uuid.UUID `json:"from_account_id,omitempty"`
	ToAccountID    *uuid.UUID `json:"to_account_id,omitempty"`
	AmountCents    int64      `json:"amount_cents"`
	OccurredAt     time.Time  `json:"occurred_at"`
	Description    string     `json:"description"`
	Notes          *string    `json:"notes,omitempty"`
}

func (h *Handler) deleteTransfer(w http.ResponseWriter, r *http.Request) {
	wsID, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	pairID, err := parseUUIDParam(r, "pair_id")
	if err != nil {
		apierror.Write(w, h.log, apierror.New(http.StatusBadRequest, "invalid_id", "pair_id must be a uuid"))
		return
	}
	if err := h.svc.DeleteTransfer(r.Context(), wsID, pairID); err != nil {
		h.writeDomainErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) createTransfer(w http.ResponseWriter, r *http.Request) {
	wsID, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	var req createTransferReq
	if err := render.DecodeJSON(r, &req); err != nil {
		apierror.Write(w, h.log, apierror.New(http.StatusBadRequest, "malformed_body", err.Error()))
		return
	}
	pair, err := h.svc.CreateTransfer(r.Context(), app.CreateTransferInput{
		WorkspaceID:    wsID,
		FromCategoryID: req.FromCategoryID,
		ToCategoryID:   req.ToCategoryID,
		FromAccountID:  req.FromAccountID,
		ToAccountID:    req.ToAccountID,
		AmountCents:    req.AmountCents,
		OccurredAt:     req.OccurredAt,
		Description:    req.Description,
		Notes:          req.Notes,
	})
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusCreated, pair)
}
