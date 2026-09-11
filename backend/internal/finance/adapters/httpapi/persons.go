package httpapi

import (
	"net/http"

	"github.com/corsi/backend/internal/finance/app"
	"github.com/corsi/backend/internal/platform/apierror"
	"github.com/corsi/backend/internal/platform/render"
)

type createPersonReq struct {
	Name  string `json:"name"`
	Notes string `json:"notes"`
}

func (h *Handler) createPerson(w http.ResponseWriter, r *http.Request) {
	wsID, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	var req createPersonReq
	if err := render.DecodeJSON(r, &req); err != nil {
		apierror.Write(w, h.log, apierror.New(http.StatusBadRequest, "malformed_body", err.Error()))
		return
	}
	p, err := h.svc.CreatePerson(r.Context(), app.CreatePersonInput{
		WorkspaceID: wsID,
		Name:        req.Name,
		Notes:       req.Notes,
	})
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusCreated, p)
}

type updatePersonReq struct {
	Name  *string `json:"name,omitempty"`
	Notes *string `json:"notes,omitempty"`
}

func (h *Handler) updatePerson(w http.ResponseWriter, r *http.Request) {
	wsID, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		apierror.Write(w, h.log, apierror.New(http.StatusBadRequest, "invalid_id", "id must be a uuid"))
		return
	}
	var req updatePersonReq
	if err := render.DecodeJSON(r, &req); err != nil {
		apierror.Write(w, h.log, apierror.New(http.StatusBadRequest, "malformed_body", err.Error()))
		return
	}
	p, err := h.svc.UpdatePerson(r.Context(), app.UpdatePersonInput{
		WorkspaceID: wsID,
		ID:          id,
		Name:        req.Name,
		Notes:       req.Notes,
	})
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusOK, p)
}

func (h *Handler) deletePerson(w http.ResponseWriter, r *http.Request) {
	wsID, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		apierror.Write(w, h.log, apierror.New(http.StatusBadRequest, "invalid_id", "id must be a uuid"))
		return
	}
	if err := h.svc.DeletePerson(r.Context(), wsID, id); err != nil {
		h.writeDomainErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) getPerson(w http.ResponseWriter, r *http.Request) {
	wsID, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		apierror.Write(w, h.log, apierror.New(http.StatusBadRequest, "invalid_id", "id must be a uuid"))
		return
	}
	p, err := h.svc.GetPerson(r.Context(), wsID, id)
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusOK, p)
}

func (h *Handler) listPersons(w http.ResponseWriter, r *http.Request) {
	wsID, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	limit, offset := parseLimitOffset(r)
	items, err := h.svc.ListPersons(r.Context(), app.ListPersonsInput{
		WorkspaceID: wsID, Limit: limit, Offset: offset,
	})
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusOK, listEnvelope{Items: items, Limit: limit, Offset: offset})
}
