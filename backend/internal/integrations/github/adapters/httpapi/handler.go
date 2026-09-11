// Package httpapi exposes the GitHub integration's management surface.
//
// ── What this API is for, and what it is not ───────────────────────────
// It answers one question: what can C.O.R.S.I. reach through GitHub, and
// what has the operator allowed it to use? It is deliberately not a GitHub
// proxy. There is no route that fetches an arbitrary GitHub URL, no route
// that reads a file, and no route an agent uses — agents reach GitHub
// through tools, which go through the application service directly and
// never through HTTP.
//
// ── Read this list twice ───────────────────────────────────────────────
// Six routes. Two of them write, and both write to OUR tables: one stores a
// credential, one replaces the authorized set. Nothing here writes to
// GitHub, and there is no handler that could — the API port has no write
// verb to call.
package httpapi

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/corsi/backend/internal/integrations/github/app"
	"github.com/corsi/backend/internal/integrations/github/domain"
	"github.com/corsi/backend/internal/platform/apierror"
	"github.com/corsi/backend/internal/platform/render"
	"github.com/corsi/backend/internal/platform/workspace"
)

type Handler struct {
	svc *app.Service
	log *slog.Logger
}

func NewHandler(svc *app.Service, log *slog.Logger) *Handler {
	return &Handler{svc: svc, log: log}
}

func (h *Handler) Mount(r chi.Router) {
	// The state of the connection and what it is allowed to read. Safe to
	// call on every page load: it reads two tables and talks to nobody.
	r.Get("/connection", h.status)
	// Stores a credential, after proving it works. See app.Connect.
	r.Post("/connection", h.connect)
	// Removes the credential and, by cascade, every repository
	// authorization that depended on it.
	r.Delete("/connection", h.disconnect)

	// What the credential can reach, each marked with whether it is
	// authorized. This one DOES talk to GitHub, which is why it is a
	// separate route from /connection: a page must be able to render
	// without it.
	r.Get("/repositories", h.availableRepositories)
	// Replaces the authorized set with exactly what is sent.
	r.Put("/repositories", h.setRepositories)

	// Recent commits across the authorized set.
	r.Get("/activity", h.activity)
}

func (h *Handler) status(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	out, err := h.svc.Status(r.Context(), ws)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	render.JSON(w, http.StatusOK, out)
}

type connectRequest struct {
	// Token is the credential. It exists on this struct for exactly the
	// duration of one request: it is never logged, never echoed, and never
	// stored except sealed. The response returns the connection, whose
	// TokenCipher is json:"-" and whose only trace is a four-character hint.
	Token string `json:"token"`
	// APIBaseURL is optional and defaults to github.com. Present so a GitHub
	// Enterprise host is configuration rather than a redeploy.
	APIBaseURL string `json:"api_base_url,omitempty"`
}

func (h *Handler) connect(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	var req connectRequest
	if !h.decode(w, r, &req) {
		return
	}
	conn, err := h.svc.Connect(r.Context(), app.ConnectInput{
		WorkspaceID: ws,
		Token:       req.Token,
		APIBaseURL:  req.APIBaseURL,
	})
	if err != nil {
		h.writeErr(w, err)
		return
	}
	// The connection, not a bare 201: the caller needs the account identity
	// to render the connected card, and a second round trip to learn who it
	// just connected as would be a request that can fail after a success.
	render.JSON(w, http.StatusCreated, conn)
}

func (h *Handler) disconnect(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	if err := h.svc.Disconnect(r.Context(), ws); err != nil {
		h.writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) availableRepositories(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	out, err := h.svc.AvailableRepositories(r.Context(), ws)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	render.JSON(w, http.StatusOK, out)
}

type setRepositoriesRequest struct {
	// Repositories is the WHOLE authorized set, as owner/name strings. Not a
	// delta: the operator edits a set, and sending an add and a revoke as
	// two requests creates a window in which the stored set is neither.
	Repositories []string `json:"repositories"`
}

func (h *Handler) setRepositories(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	var req setRepositoriesRequest
	if !h.decode(w, r, &req) {
		return
	}
	out, err := h.svc.SetAuthorizedRepositories(r.Context(), ws, req.Repositories)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	// The new status, so the page renders the truth it just created rather
	// than the truth it hoped for.
	render.JSON(w, http.StatusOK, out)
}

func (h *Handler) activity(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	items, err := h.svc.RecentActivity(r.Context(), ws)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	render.JSON(w, http.StatusOK, map[string]any{
		"items": items,
		// The ceilings the server applied, so a short feed reads as a bounded
		// one rather than as a quiet repository.
		"repositories_read": app.MaxActivityRepositories,
		"per_repository":    app.ActivityCommitsPerRepo,
	})
}

/* ── helpers ─────────────────────────────────────────────────────────── */

func (h *Handler) workspaceID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, ok := workspace.FromContext(r.Context())
	if !ok {
		apierror.Write(w, h.log, apierror.New(http.StatusForbidden,
			"workspace_missing", "no workspace in context"))
		return uuid.Nil, false
	}
	return id, true
}

func (h *Handler) decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := render.DecodeJSON(r, dst); err != nil {
		apierror.Write(w, h.log, apierror.New(http.StatusBadRequest, "invalid", err.Error()))
		return false
	}
	return true
}

// writeErr maps the integration's error vocabulary onto HTTP.
//
// ── Why 401 and 429 are not what you might expect ──────────────────────
// GitHub refusing OUR credential is not the CALLER being unauthenticated,
// and answering 401 would tell a browser to re-authenticate against
// C.O.R.S.I. — the wrong action entirely. It is a stored configuration that
// stopped working, which is a 409: the request is fine, the caller is
// allowed, and the workspace is in a state that does not permit the
// operation until a human fixes it. The machine-readable code carries the
// real reason.
//
// A rate limit, on the other hand, IS a 429: the standard shape of "not
// now, try later", clearing with the passage of time. The same reasoning
// the chat module applied to budget refusals.
func (h *Handler) writeErr(w http.ResponseWriter, err error) {
	var de *domain.Error
	if !errors.As(err, &de) {
		apierror.Write(w, h.log, err)
		return
	}
	code := func(fallback string) string {
		if de.Code != "" {
			return de.Code
		}
		return fallback
	}
	switch de.Kind {
	case domain.KindInvalid:
		apierror.Write(w, h.log, apierror.New(http.StatusBadRequest, code("invalid"), de.Message))
	case domain.KindNotFound:
		apierror.Write(w, h.log, apierror.New(http.StatusNotFound, code("not_found"), de.Message))
	case domain.KindConflict:
		apierror.Write(w, h.log, apierror.New(http.StatusConflict, code("conflict"), de.Message))
	case domain.KindUnauthorized:
		apierror.Write(w, h.log, apierror.New(http.StatusConflict, code("github_unauthorized"), de.Message))
	case domain.KindRateLimited:
		apierror.Write(w, h.log, apierror.New(http.StatusTooManyRequests, code("github_rate_limited"), de.Message))
	case domain.KindUpstream:
		// GitHub failed, not us. 502 keeps a third party's outage out of
		// this service's error budget.
		apierror.Write(w, h.log, apierror.New(http.StatusBadGateway, code("upstream"), de.Message))
	case domain.KindNotConfig:
		apierror.Write(w, h.log, apierror.New(http.StatusServiceUnavailable, code("not_configured"), de.Message))
	default:
		apierror.Write(w, h.log, err)
	}
}
