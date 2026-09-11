package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/corsi/backend/internal/chat/app"
	"github.com/corsi/backend/internal/chat/domain"
	"github.com/corsi/backend/internal/platform/render"
)

// The tool surface.
//
// Three routes, and the shape of them is the authorization model made
// visible: the catalogue is read per agent (because "which tools exist" is
// never the interesting question — "which may THIS agent use" is), and the
// only writes are granting and revoking one known name.
//
// There is deliberately no route that creates a tool. A tool is code; the
// API authorizes capabilities the backend already has, and an endpoint that
// accepted an arbitrary name and schema would be an endpoint that lets a
// client describe an executor that does not exist.

func (h *Handler) listAgentTools(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	id, ok := h.pathID(w, r)
	if !ok {
		return
	}
	report, err := h.svc.AgentTools(r.Context(), ws, id)
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusOK, report)
}

type authorizeToolRequest struct {
	ToolName string `json:"tool_name"`
}

// authorizeAgentTool grants one tool.
//
// PUT-like semantics on a POST: granting twice is the same state as
// granting once, and the response is the catalogue rather than the row, so
// a client renders the new truth without a second round trip.
func (h *Handler) authorizeAgentTool(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	id, ok := h.pathID(w, r)
	if !ok {
		return
	}
	var req authorizeToolRequest
	if !h.decode(w, r, &req) {
		return
	}
	if err := h.svc.AuthorizeTool(r.Context(), ws, id, domain.ToolName(req.ToolName)); err != nil {
		h.writeDomainErr(w, err)
		return
	}
	report, err := h.svc.AgentTools(r.Context(), ws, id)
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusOK, report)
}

// revokeAgentTool removes one grant.
//
// The name is a path segment rather than a body, because DELETE with a body
// is a request several intermediaries feel free to strip. Dots are legal in
// a path segment, so the canonical name travels unencoded.
func (h *Handler) revokeAgentTool(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	id, ok := h.pathID(w, r)
	if !ok {
		return
	}
	name := chi.URLParam(r, "toolName")
	if name == "" {
		h.badRequest(w, "toolName is required")
		return
	}
	if err := h.svc.RevokeTool(r.Context(), ws, id, domain.ToolName(name)); err != nil {
		h.writeDomainErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// listConversationToolCalls is the audit read.
//
// Scoped to one conversation because that is the unit the transcript
// renders, and bounded because a long thread with tools could otherwise
// return thousands of rows to draw a few badges.
func (h *Handler) listConversationToolCalls(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	id, ok := h.pathID(w, r)
	if !ok {
		return
	}
	items, err := h.svc.ConversationToolCalls(r.Context(), ws, id)
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	// `limit` is the cap the server applied, which is the whole meaning of
	// the field everywhere else in this API — a client compares it against
	// what it received to know whether the list was cut. Reporting
	// len(items) would have said "the cap was 0" on an empty thread, which
	// is a false statement about the server's own behaviour.
	render.JSON(w, http.StatusOK, listEnvelope{
		Items:  items,
		Limit:  app.MaxToolCallsPerConversation,
		Offset: 0,
	})
}
