// Package httpapi exposes the Meta Threads integration's management
// surface.
//
// ── What this API is for, and what it is not ───────────────────────────
// It answers one question: is this workspace connected to Meta Threads, and
// what may that credential do? It is deliberately not a Meta proxy. There
// is no route that fetches an arbitrary Meta URL, no route that reads a
// post, and no route an agent uses — agents reach Meta Threads through
// tools, which call the application service directly and never through
// HTTP.
//
// ── Read this list twice ───────────────────────────────────────────────
// Five routes. Three of them write, and all three write to OUR table: one
// stores a credential, one replaces it with a refreshed one, one destroys
// it. Nothing here writes to Meta, and no handler could — the API port
// declares no write verb to call.
package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/corsi/backend/internal/integrations/metathreads/app"
	"github.com/corsi/backend/internal/integrations/metathreads/domain"
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
	// The state of the connection. Safe to call on every page load: it reads
	// one row and talks to nobody.
	r.Get("/connection", h.status)
	// Where a person is sent to approve the connection. A GET that builds a
	// URL and stores nothing — the browser does the redirecting, because a
	// server-side 302 would make this endpoint indistinguishable from an
	// open redirect to anything a caller named.
	r.Get("/authorize-url", h.authorizeURL)
	// Redeems the authorization code and stores the sealed credential.
	r.Post("/connection", h.connect)
	// Extends the stored long-lived token.
	r.Post("/connection/refresh", h.refresh)
	// Destroys the credential. See repo.Disconnect on why the cipher is
	// overwritten rather than merely orphaned.
	r.Delete("/connection", h.disconnect)
}

func (h *Handler) workspaceID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	ws, ok := workspace.FromContext(r.Context())
	if !ok || ws == uuid.Nil {
		apierror.Write(w, h.log, apierror.New(http.StatusForbidden,
			"workspace_required", "a workspace is required"))
		return uuid.Nil, false
	}
	return ws, true
}

func (h *Handler) status(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	conn, err := h.svc.Status(r.Context(), ws)
	if err != nil {
		var de *domain.Error
		if errors.As(err, &de) && de.Kind == domain.KindNotConnected {
			// Not connected is a STATE, not an error: a page asking "am I
			// connected" and receiving a 404 has to treat a normal answer as
			// a failure. 200 with connected:false is the honest shape.
			render.JSON(w, http.StatusOK, map[string]any{
				"connected":  false,
				"configured": h.svc.Configured(),
			})
			return
		}
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusOK, map[string]any{
		"connected":  true,
		"configured": h.svc.Configured(),
		"connection": conn,
	})
}

func (h *Handler) authorizeURL(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.workspaceID(w, r); !ok {
		return
	}
	// `state` is the caller's to choose and ours to hand back untouched: it
	// is how the client recognises its own callback. This endpoint never
	// generates one, because a value the server invented would have to be
	// stored somewhere to be checked, and nothing here has a session.
	url, err := h.svc.AuthorizationURL(r.URL.Query().Get("state"))
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusOK, map[string]any{"authorization_url": url})
}

type connectRequest struct {
	Code        string `json:"code"`
	RedirectURI string `json:"redirect_uri"`
}

func (h *Handler) connect(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	var req connectRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&req); err != nil {
		apierror.Write(w, h.log, apierror.New(http.StatusBadRequest,
			"invalid", "the request body is not valid JSON"))
		return
	}
	conn, err := h.svc.Connect(r.Context(), app.ConnectInput{
		WorkspaceID: ws, Code: req.Code, RedirectURI: req.RedirectURI,
	})
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	// The Connection type has json:"-" on the cipher, so the token cannot
	// reach this body. Only the hint does.
	render.JSON(w, http.StatusCreated, map[string]any{"connection": conn})
}

func (h *Handler) refresh(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	conn, err := h.svc.Refresh(r.Context(), ws)
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusOK, map[string]any{"connection": conn})
}

func (h *Handler) disconnect(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	if err := h.svc.Disconnect(r.Context(), ws); err != nil {
		h.writeDomainErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// writeDomainErr maps this integration's vocabulary onto HTTP.
//
// The codes are prefixed so a client can branch on them without parsing
// prose, and the statuses are chosen for what the caller should DO:
// reconnect, wait, or tell an operator to configure the deployment.
func (h *Handler) writeDomainErr(w http.ResponseWriter, err error) {
	code := func(s string) string { return "meta_threads_" + s }
	var de *domain.Error
	if !errors.As(err, &de) {
		apierror.Write(w, h.log, err)
		return
	}
	switch de.Kind {
	case domain.KindInvalid:
		apierror.Write(w, h.log, apierror.New(http.StatusBadRequest, code("invalid"), de.Message))
	case domain.KindNotConnected:
		apierror.Write(w, h.log, apierror.New(http.StatusNotFound, code("not_connected"), de.Message))
	case domain.KindNotFound:
		apierror.Write(w, h.log, apierror.New(http.StatusNotFound, code("not_found"), de.Message))
	case domain.KindScopeMissing:
		apierror.Write(w, h.log, apierror.New(http.StatusConflict, code("scope_missing"), de.Message))
	case domain.KindTokenExpired:
		apierror.Write(w, h.log, apierror.New(http.StatusConflict, code("token_expired"), de.Message))
	case domain.KindNotConfigured:
		apierror.Write(w, h.log, apierror.New(http.StatusServiceUnavailable, code("not_configured"), de.Message))
	case domain.KindUpstream:
		apierror.Write(w, h.log, apierror.New(http.StatusBadGateway, code("upstream"), de.Message))
	default:
		apierror.Write(w, h.log, err)
	}
}

/* ── Meta platform callbacks ─────────────────────────────────────────── */

// MountPublic mounts the two endpoints META itself calls.
//
// ══════════════════════════════════════════════════════════════════════
//
//	These are the only UNAUTHENTICATED routes in this integration
//
// ══════════════════════════════════════════════════════════════════════
//
// ── Why they are outside the workspace middleware ──────────────────────
// Because Meta cannot send an `X-Workspace-Id`. It has never heard of a
// workspace and never will: a platform callback names an APP-SCOPED USER.
// Mounting these behind the workspace guard would make them 401 in any
// deployment that requires the header — that is, in production — and the
// operator would discover it as a silently unhonoured deletion request.
//
// ── What replaces the workspace as the authorization ───────────────────
// The HMAC signature. Every one of these requests is verified against the
// app secret before a single row is read, and the account it names is
// matched exactly. An unsigned or wrongly-signed POST reaches nothing.
// See domain.ParseSignedRequest — that function IS the authentication of
// this surface.
//
// ── Why they live under /integrations/meta-threads ─────────────────────
// So one public tunnel to the frontend serves everything: the dev proxy
// already forwards this prefix to the API, so Meta's callbacks arrive
// without a second tunnel and without the backend being exposed directly.
func (h *Handler) MountPublic(r chi.Router) {
	// Meta pings this when a user removes the app without visiting us.
	r.Post("/deauthorize", h.deauthorize)
	// Meta pings this when a user asks for their data to be deleted.
	r.Post("/data-deletion", h.dataDeletion)
	// The human-readable status page the deletion contract requires the
	// response to point at. A GET, and the only HTML this backend serves.
	r.Get("/data-deletion", h.dataDeletionStatus)
}

// signedRequestOf reads Meta's parameter from a form-encoded POST.
//
// Meta sends `application/x-www-form-urlencoded` with a single
// `signed_request` field. The body is bounded before parsing: this is a
// public endpoint and an unbounded ParseForm on one is a memory tap.
func signedRequestOf(w http.ResponseWriter, r *http.Request) (string, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		return "", false
	}
	return r.PostFormValue("signed_request"), true
}

// deauthorize handles Meta's deauthorization ping.
//
// ── Why the response body is empty ─────────────────────────────────────
// Meta documents no required response for this callback, only that the URL
// is pinged. 200 with nothing in it is the honest answer: there is no
// status for the user to check, because the app was already removed on
// their side before we heard about it.
func (h *Handler) deauthorize(w http.ResponseWriter, r *http.Request) {
	raw, ok := signedRequestOf(w, r)
	if !ok {
		apierror.Write(w, h.log, apierror.New(http.StatusBadRequest,
			"meta_threads_invalid", "the callback body could not be read"))
		return
	}
	if _, err := h.svc.Deauthorize(r.Context(), raw); err != nil {
		h.writeCallbackErr(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// dataDeletion handles Meta's data deletion request.
//
// ── The response Meta requires ─────────────────────────────────────────
// `{"url": …, "confirmation_code": …}`, where the url gives the person a
// human-readable explanation of the status of their request. Both fields
// are mandatory; a 200 with anything else fails Meta's own check.
func (h *Handler) dataDeletion(w http.ResponseWriter, r *http.Request) {
	raw, ok := signedRequestOf(w, r)
	if !ok {
		apierror.Write(w, h.log, apierror.New(http.StatusBadRequest,
			"meta_threads_invalid", "the callback body could not be read"))
		return
	}
	result, err := h.svc.DeleteUserData(r.Context(), raw)
	if err != nil {
		h.writeCallbackErr(w, err)
		return
	}

	// Built from the request Meta actually made, so a deployment reached
	// through a tunnel returns its public address and one behind a domain
	// returns that. A configured constant here would be a second copy of
	// the public origin, free to disagree with the one in use.
	status := statusURL(r, result.ConfirmationCode)
	render.JSON(w, http.StatusOK, map[string]any{
		"url":               status,
		"confirmation_code": result.ConfirmationCode,
	})
}

// statusURL is where the person can read what happened.
func statusURL(r *http.Request, code string) string {
	scheme := "https"
	// Behind the dev proxy and behind a tunnel the hop to us is plain HTTP;
	// the public leg is what the user will follow, and Meta requires it to
	// be HTTPS. The forwarded header is what says which one this was.
	if forwarded := r.Header.Get("X-Forwarded-Proto"); forwarded != "" {
		scheme = forwarded
	} else if r.TLS == nil && strings.HasPrefix(r.Host, "localhost") {
		scheme = "http"
	}
	return scheme + "://" + r.Host + "/integrations/meta-threads/data-deletion?code=" +
		url.QueryEscape(code)
}

// dataDeletionStatus is the page the deletion response points at.
//
// ── Why it does not look the code up ───────────────────────────────────
// Because there is nothing to look up. The deletion is synchronous and
// total — the only personal data this integration holds is the sealed
// credential, and it is destroyed inside the callback that issued this
// code. Storing codes in order to answer "is it done yet" would keep a new
// record about somebody who just asked to be forgotten, to tell them
// something that is true of every request.
//
// So the page states the policy plainly and echoes the code back. It is
// deliberately plain text: this is read by a person Meta sent here, not by
// our SPA, and it must render with no session, no workspace and no bundle.
func (h *Handler) dataDeletionStatus(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	if len(code) > 64 {
		code = code[:64]
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	// The code is echoed through a text/plain response with nosniff, so a
	// crafted value cannot become markup in the reader's browser.
	_, _ = io.WriteString(w, "C.O.R.S.I. — Meta Threads data deletion\n"+
		"\n"+
		"Status: COMPLETE.\n"+
		"\n"+
		"Deletion requests are carried out immediately, when the request\n"+
		"arrives. There is no queue and nothing pending.\n"+
		"\n"+
		"What this application stored about your Meta Threads account: an\n"+
		"access credential, and the username and profile picture shown\n"+
		"beside it. Posts, metrics and replies were never stored — they were\n"+
		"read from Meta each time they were needed and never written down.\n"+
		"\n"+
		"If an account was linked, that credential has been destroyed and the\n"+
		"link removed. If none was linked, there was nothing to delete.\n"+
		"\n"+
		"Confirmation code: "+code+"\n")
}

// writeCallbackErr answers Meta.
//
// ── Why a bad signature is 400 and not 401 ─────────────────────────────
// Because there is no credential to challenge and no realm to name. The
// request is malformed for this endpoint — it failed verification — and
// 400 says exactly that without inviting a retry with an Authorization
// header that would never exist.
func (h *Handler) writeCallbackErr(w http.ResponseWriter, err error) {
	var de *domain.Error
	if errors.As(err, &de) && de.Kind == domain.KindInvalid {
		// The message names the shape that failed, never the secret and
		// never the value that was signed.
		apierror.Write(w, h.log, apierror.New(http.StatusBadRequest,
			"meta_threads_invalid", de.Message))
		return
	}
	apierror.Write(w, h.log, err)
}
