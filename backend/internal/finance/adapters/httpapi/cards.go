package httpapi

import (
	"net/http"

	"github.com/corsi/backend/internal/finance/app"
	"github.com/corsi/backend/internal/finance/domain"
	"github.com/corsi/backend/internal/platform/apierror"
	"github.com/corsi/backend/internal/platform/render"
)

type createCardReq struct {
	Name        string             `json:"name"`
	Institution string             `json:"institution"`
	Network     domain.CardNetwork `json:"network"`
	Variant     string             `json:"variant"`
	Last4       string             `json:"last4"`
	LimitCents  int64              `json:"limit_cents"`
	ClosingDay  int                `json:"closing_day"`
	DueDay      int                `json:"due_day"`
}

func (h *Handler) createCard(w http.ResponseWriter, r *http.Request) {
	wsID, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	var req createCardReq
	if err := render.DecodeJSON(r, &req); err != nil {
		apierror.Write(w, h.log, apierror.New(http.StatusBadRequest, "malformed_body", err.Error()))
		return
	}
	c, err := h.svc.CreateCard(r.Context(), app.CreateCardInput{
		WorkspaceID: wsID,
		Name:        req.Name,
		Institution: req.Institution,
		Network:     req.Network,
		Variant:     req.Variant,
		Last4:       req.Last4,
		LimitCents:  req.LimitCents,
		ClosingDay:  req.ClosingDay,
		DueDay:      req.DueDay,
	})
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusCreated, c)
}

type updateCardReq struct {
	Name        *string             `json:"name,omitempty"`
	Institution *string             `json:"institution,omitempty"`
	Network     *domain.CardNetwork `json:"network,omitempty"`
	Variant     *string             `json:"variant,omitempty"`
	Last4       *string             `json:"last4,omitempty"`
	LimitCents  *int64              `json:"limit_cents,omitempty"`
	ClosingDay  *int                `json:"closing_day,omitempty"`
	DueDay      *int                `json:"due_day,omitempty"`
}

func (h *Handler) updateCard(w http.ResponseWriter, r *http.Request) {
	wsID, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		apierror.Write(w, h.log, apierror.New(http.StatusBadRequest, "invalid_id", "id must be a uuid"))
		return
	}
	var req updateCardReq
	if err := render.DecodeJSON(r, &req); err != nil {
		apierror.Write(w, h.log, apierror.New(http.StatusBadRequest, "malformed_body", err.Error()))
		return
	}
	c, err := h.svc.UpdateCard(r.Context(), app.UpdateCardInput{
		WorkspaceID: wsID,
		ID:          id,
		Name:        req.Name,
		Institution: req.Institution,
		Network:     req.Network,
		Variant:     req.Variant,
		Last4:       req.Last4,
		LimitCents:  req.LimitCents,
		ClosingDay:  req.ClosingDay,
		DueDay:      req.DueDay,
	})
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusOK, c)
}

// deleteCard archives the card (soft delete). v0.1 does not hard-delete.
func (h *Handler) deleteCard(w http.ResponseWriter, r *http.Request) {
	wsID, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		apierror.Write(w, h.log, apierror.New(http.StatusBadRequest, "invalid_id", "id must be a uuid"))
		return
	}
	if err := h.svc.ArchiveCard(r.Context(), wsID, id); err != nil {
		h.writeDomainErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) getCard(w http.ResponseWriter, r *http.Request) {
	wsID, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		apierror.Write(w, h.log, apierror.New(http.StatusBadRequest, "invalid_id", "id must be a uuid"))
		return
	}
	c, err := h.svc.GetCard(r.Context(), wsID, id)
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusOK, c)
}

func (h *Handler) listCards(w http.ResponseWriter, r *http.Request) {
	wsID, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	limit, offset := parseLimitOffset(r)
	items, err := h.svc.ListCards(r.Context(), app.ListCardsInput{
		WorkspaceID: wsID, Limit: limit, Offset: offset,
	})
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusOK, listEnvelope{Items: items, Limit: limit, Offset: offset})
}
