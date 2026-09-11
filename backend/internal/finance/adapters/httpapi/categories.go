package httpapi

import (
	"net/http"

	"github.com/corsi/backend/internal/finance/app"
	"github.com/corsi/backend/internal/finance/domain"
	"github.com/corsi/backend/internal/platform/apierror"
	"github.com/corsi/backend/internal/platform/render"
)

type createCategoryReq struct {
	Name  string           `json:"name"`
	Type  domain.EntryType `json:"type"`
	Color string           `json:"color"`
	Icon  string           `json:"icon"`
}

func (h *Handler) createCategory(w http.ResponseWriter, r *http.Request) {
	wsID, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	var req createCategoryReq
	if err := render.DecodeJSON(r, &req); err != nil {
		apierror.Write(w, h.log, apierror.New(http.StatusBadRequest, "malformed_body", err.Error()))
		return
	}
	c, err := h.svc.CreateCategory(r.Context(), app.CreateCategoryInput{
		WorkspaceID: wsID,
		Name:        req.Name,
		Type:        req.Type,
		Color:       req.Color,
		Icon:        req.Icon,
	})
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusCreated, c)
}

type updateCategoryReq struct {
	Name  *string `json:"name,omitempty"`
	Color *string `json:"color,omitempty"`
	Icon  *string `json:"icon,omitempty"`
}

func (h *Handler) updateCategory(w http.ResponseWriter, r *http.Request) {
	wsID, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		apierror.Write(w, h.log, apierror.New(http.StatusBadRequest, "invalid_id", "id must be a uuid"))
		return
	}
	var req updateCategoryReq
	if err := render.DecodeJSON(r, &req); err != nil {
		apierror.Write(w, h.log, apierror.New(http.StatusBadRequest, "malformed_body", err.Error()))
		return
	}
	c, err := h.svc.UpdateCategory(r.Context(), app.UpdateCategoryInput{
		WorkspaceID: wsID,
		ID:          id,
		Name:        req.Name,
		Color:       req.Color,
		Icon:        req.Icon,
	})
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusOK, c)
}

func (h *Handler) deleteCategory(w http.ResponseWriter, r *http.Request) {
	wsID, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		apierror.Write(w, h.log, apierror.New(http.StatusBadRequest, "invalid_id", "id must be a uuid"))
		return
	}
	if err := h.svc.DeleteCategory(r.Context(), wsID, id); err != nil {
		h.writeDomainErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) getCategory(w http.ResponseWriter, r *http.Request) {
	wsID, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		apierror.Write(w, h.log, apierror.New(http.StatusBadRequest, "invalid_id", "id must be a uuid"))
		return
	}
	c, err := h.svc.GetCategory(r.Context(), wsID, id)
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusOK, c)
}

func (h *Handler) listCategories(w http.ResponseWriter, r *http.Request) {
	wsID, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	limit, offset := parseLimitOffset(r)
	in := app.ListCategoriesInput{WorkspaceID: wsID, Limit: limit, Offset: offset}
	if v := r.URL.Query().Get("type"); v != "" {
		et := domain.EntryType(v)
		if !et.Valid() {
			apierror.Write(w, h.log, apierror.New(http.StatusBadRequest, "invalid", "type must be income or expense"))
			return
		}
		in.Type = &et
	}
	items, err := h.svc.ListCategories(r.Context(), in)
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusOK, listEnvelope{Items: items, Limit: limit, Offset: offset})
}
