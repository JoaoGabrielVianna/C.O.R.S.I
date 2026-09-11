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

type createRecurringReq struct {
	Description string                     `json:"description"`
	AmountCents int64                      `json:"amount_cents"`
	CategoryID  uuid.UUID                  `json:"category_id"`
	PersonID    *uuid.UUID                 `json:"person_id,omitempty"`
	DueDay      int                        `json:"due_day"`
	Recurrence  *domain.RecurringFrequency `json:"recurrence,omitempty"`
	StartsAt    *time.Time                 `json:"starts_at,omitempty"`
	Notes       *string                    `json:"notes,omitempty"`
}

func (h *Handler) createRecurringEntry(w http.ResponseWriter, r *http.Request) {
	wsID, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	var req createRecurringReq
	if err := render.DecodeJSON(r, &req); err != nil {
		apierror.Write(w, h.log, apierror.New(http.StatusBadRequest, "malformed_body", err.Error()))
		return
	}
	f, err := h.svc.CreateRecurringEntry(r.Context(), app.CreateRecurringEntryInput{
		WorkspaceID: wsID, Description: req.Description, AmountCents: req.AmountCents,
		CategoryID: req.CategoryID, PersonID: req.PersonID, DueDay: req.DueDay,
		Recurrence: req.Recurrence, StartsAt: req.StartsAt, Notes: req.Notes,
	})
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusCreated, f)
}

type updateRecurringReq struct {
	Description *string                    `json:"description,omitempty"`
	AmountCents *int64                     `json:"amount_cents,omitempty"`
	CategoryID  *uuid.UUID                 `json:"category_id,omitempty"`
	PersonID    *uuid.UUID                 `json:"person_id,omitempty"`
	ClearPerson bool                       `json:"clear_person_id,omitempty"`
	DueDay      *int                       `json:"due_day,omitempty"`
	Recurrence  *domain.RecurringFrequency `json:"recurrence,omitempty"`
	Status      *domain.RecurringStatus    `json:"status,omitempty"`
	EndsAt      *time.Time                 `json:"ends_at,omitempty"`
	ClearEndsAt bool                       `json:"clear_ends_at,omitempty"`
	Notes       *string                    `json:"notes,omitempty"`
}

func (h *Handler) updateRecurringEntry(w http.ResponseWriter, r *http.Request) {
	wsID, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		apierror.Write(w, h.log, apierror.New(http.StatusBadRequest, "invalid_id", "id must be a uuid"))
		return
	}
	var req updateRecurringReq
	if err := render.DecodeJSON(r, &req); err != nil {
		apierror.Write(w, h.log, apierror.New(http.StatusBadRequest, "malformed_body", err.Error()))
		return
	}
	f, err := h.svc.UpdateRecurringEntry(r.Context(), app.UpdateRecurringEntryInput{
		WorkspaceID: wsID, ID: id,
		Description: req.Description, AmountCents: req.AmountCents, CategoryID: req.CategoryID,
		PersonID: req.PersonID, ClearPerson: req.ClearPerson, DueDay: req.DueDay,
		Recurrence: req.Recurrence, Status: req.Status,
		EndsAt: req.EndsAt, ClearEndsAt: req.ClearEndsAt, Notes: req.Notes,
	})
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusOK, f)
}

func (h *Handler) deleteRecurringEntry(w http.ResponseWriter, r *http.Request) {
	wsID, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		apierror.Write(w, h.log, apierror.New(http.StatusBadRequest, "invalid_id", "id must be a uuid"))
		return
	}
	if err := h.svc.DeleteRecurringEntry(r.Context(), wsID, id); err != nil {
		h.writeDomainErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) getRecurringEntry(w http.ResponseWriter, r *http.Request) {
	wsID, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		apierror.Write(w, h.log, apierror.New(http.StatusBadRequest, "invalid_id", "id must be a uuid"))
		return
	}
	f, err := h.svc.GetRecurringEntry(r.Context(), wsID, id)
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusOK, f)
}

func (h *Handler) listRecurringEntries(w http.ResponseWriter, r *http.Request) {
	wsID, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	limit, offset := parseLimitOffset(r)
	in := app.ListRecurringEntriesInput{
		WorkspaceID: wsID, Limit: limit, Offset: offset,
		ActiveOnly: r.URL.Query().Get("active") == "true",
	}
	if v := r.URL.Query().Get("category"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			apierror.Write(w, h.log, apierror.New(http.StatusBadRequest, "invalid_category", "category must be a uuid"))
			return
		}
		in.CategoryID = &id
	}
	items, err := h.svc.ListRecurringEntries(r.Context(), in)
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusOK, listEnvelope{Items: items, Limit: limit, Offset: offset})
}

// recurringSummaryResp is the monthly-commitment reading.
//
// Deliberately its own endpoint rather than a field on /transactions/totals:
// that contract classifies rows in `finance.transactions`, and a commitment
// is not one. See app.MonthlyCommitment.
type recurringLineResp struct {
	ID           uuid.UUID `json:"id"`
	Description  string    `json:"description"`
	CategoryID   uuid.UUID `json:"category_id"`
	Direction    string    `json:"direction"`
	AmountCents  int64     `json:"amount_cents"`
	MonthlyCents int64     `json:"monthly_cents"`
	Recurrence   string    `json:"recurrence"`
	DueDay       int       `json:"due_day"`
}

type recurringSummaryResp struct {
	At                  time.Time           `json:"at"`
	IncomeMonthlyCents  int64               `json:"income_monthly_cents"`
	ExpenseMonthlyCents int64               `json:"expense_monthly_cents"`
	NetMonthlyCents     int64               `json:"net_monthly_cents"`
	Items               []recurringLineResp `json:"items"`
}

func (h *Handler) getRecurringSummary(w http.ResponseWriter, r *http.Request) {
	wsID, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	c, err := h.svc.GetRecurringSummary(r.Context(), wsID)
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	out := recurringSummaryResp{
		At:                  c.At,
		IncomeMonthlyCents:  c.IncomeMonthlyCents,
		ExpenseMonthlyCents: c.ExpenseMonthlyCents,
		NetMonthlyCents:     c.NetMonthlyCents,
		Items:               []recurringLineResp{},
	}
	for _, l := range c.Lines {
		out.Items = append(out.Items, recurringLineResp{
			ID: l.Entry.ID, Description: l.Entry.Description,
			CategoryID: l.Entry.CategoryID, Direction: string(l.Direction),
			AmountCents: l.Entry.AmountCents, MonthlyCents: l.MonthlyCents,
			Recurrence: string(l.Entry.Recurrence), DueDay: l.Entry.DueDay,
		})
	}
	render.JSON(w, http.StatusOK, out)
}
