// Package httpapi exposes the releases context over HTTP.
//
// Wire shape matches the rest of the platform:
//
//	error responses: { "error": { "code": "...", "message": "..." } }
//	list reads:      { "items": [...] }
//
// The list envelope here carries no limit/offset. Paging a release history
// would be a contract promising a page size this product does not apply:
// the whole timeline of a module is a handful of rows and is always
// returned whole. Publishing a `limit` the server does not enforce is the
// same class of untruth the tool-calls envelope was corrected for.
package httpapi

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/corsi/backend/internal/platform/apierror"
	"github.com/corsi/backend/internal/platform/render"
	"github.com/corsi/backend/internal/releases/app"
	"github.com/corsi/backend/internal/releases/domain"
)

type Handler struct {
	svc *app.Service
	log *slog.Logger
}

func NewHandler(svc *app.Service, log *slog.Logger) *Handler {
	return &Handler{svc: svc, log: log}
}

func (h *Handler) Mount(r chi.Router) {
	r.Get("/modules", h.listModules)
	r.Route("/modules/{key}", func(r chi.Router) {
		r.Get("/", h.getModule)
		r.Get("/releases/{version}", h.getRelease)
		// Publishing is the one irreversible action in this context, so it
		// is a POST on an explicit sub-resource rather than a PATCH of a
		// status field. A verb that reads as "publish" is harder to invoke
		// by accident than a generic update that happens to set a string.
		r.Post("/releases/{version}/publish", h.publishRelease)
	})
}

func (h *Handler) listModules(w http.ResponseWriter, r *http.Request) {
	items, err := h.svc.ListModules(r.Context())
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) getModule(w http.ResponseWriter, r *http.Request) {
	detail, err := h.svc.GetModule(r.Context(), chi.URLParam(r, "key"))
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusOK, detail)
}

func (h *Handler) getRelease(w http.ResponseWriter, r *http.Request) {
	rel, err := h.svc.GetRelease(r.Context(), chi.URLParam(r, "key"), chi.URLParam(r, "version"))
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusOK, rel)
}

func (h *Handler) publishRelease(w http.ResponseWriter, r *http.Request) {
	rel, err := h.svc.Publish(r.Context(), chi.URLParam(r, "key"), chi.URLParam(r, "version"))
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusOK, rel)
}

func (h *Handler) writeDomainErr(w http.ResponseWriter, err error) {
	var de *domain.Error
	if !errors.As(err, &de) {
		apierror.Write(w, h.log, err)
		return
	}
	switch de.Kind {
	case domain.KindInvalid:
		apierror.Write(w, h.log, apierror.New(http.StatusBadRequest, "invalid", de.Message))
	case domain.KindNotFound:
		apierror.Write(w, h.log, apierror.New(http.StatusNotFound, "not_found", de.Message))
	case domain.KindConflict:
		apierror.Write(w, h.log, apierror.New(http.StatusConflict, "conflict", de.Message))
	default:
		apierror.Write(w, h.log, err)
	}
}
