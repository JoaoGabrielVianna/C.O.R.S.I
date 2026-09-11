package httpapi

import (
	"net/http"

	"github.com/corsi/backend/internal/chat/app"
	"github.com/corsi/backend/internal/platform/render"
)

// createProviderRequest carries the API key in plaintext — the only place
// it appears on the wire, and only inbound. It is sealed before storage and
// never echoed back; reads return api_key_hint instead.
type createProviderRequest struct {
	Name         string `json:"name"`
	BaseURL      string `json:"base_url"`
	APIKey       string `json:"api_key"`
	DefaultModel string `json:"default_model"`
}

func (h *Handler) createProvider(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	var req createProviderRequest
	if !h.decode(w, r, &req) {
		return
	}
	p, err := h.svc.CreateProvider(r.Context(), app.CreateProviderInput{
		WorkspaceID:  ws,
		Name:         req.Name,
		BaseURL:      req.BaseURL,
		APIKey:       req.APIKey,
		DefaultModel: req.DefaultModel,
	})
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusCreated, p)
}

// updateProviderRequest uses pointers throughout so PATCH semantics hold:
// an omitted field is untouched. api_key in particular must be omissible —
// the UI cannot re-send a key it is never allowed to read.
type updateProviderRequest struct {
	Name         *string `json:"name"`
	BaseURL      *string `json:"base_url"`
	APIKey       *string `json:"api_key"`
	DefaultModel *string `json:"default_model"`
}

func (h *Handler) updateProvider(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	id, ok := h.pathID(w, r)
	if !ok {
		return
	}
	var req updateProviderRequest
	if !h.decode(w, r, &req) {
		return
	}
	p, err := h.svc.UpdateProvider(r.Context(), app.UpdateProviderInput{
		WorkspaceID:  ws,
		ID:           id,
		Name:         req.Name,
		BaseURL:      req.BaseURL,
		APIKey:       req.APIKey,
		DefaultModel: req.DefaultModel,
	})
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusOK, p)
}

func (h *Handler) getProvider(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	id, ok := h.pathID(w, r)
	if !ok {
		return
	}
	p, err := h.svc.GetProvider(r.Context(), ws, id)
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusOK, p)
}

func (h *Handler) listProviders(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	limit, offset := parseLimitOffset(r)
	items, err := h.svc.ListProviders(r.Context(), ws, limit, offset)
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusOK, listEnvelope{Items: items, Limit: limit, Offset: offset})
}

func (h *Handler) deleteProvider(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	id, ok := h.pathID(w, r)
	if !ok {
		return
	}
	if err := h.svc.DeleteProvider(r.Context(), ws, id); err != nil {
		h.writeDomainErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) listProviderModels(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	id, ok := h.pathID(w, r)
	if !ok {
		return
	}
	models, err := h.svc.ListProviderModels(r.Context(), ws, id)
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusOK, map[string]any{"items": models})
}
