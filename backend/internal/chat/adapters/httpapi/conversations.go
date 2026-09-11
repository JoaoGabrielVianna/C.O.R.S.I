package httpapi

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/corsi/backend/internal/chat/app"
	"github.com/corsi/backend/internal/chat/domain"
	"github.com/corsi/backend/internal/platform/render"
)

type createConversationRequest struct {
	AgentID uuid.UUID `json:"agent_id"`
	Title   string    `json:"title"`
	// ContextReferences seeds what the thread is about, for a conversation
	// opened from an entity rather than from the composer. Identity only;
	// see contextReferenceRequest.
	ContextReferences []contextReferenceRequest `json:"context_references"`
}

func (h *Handler) createConversation(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	var req createConversationRequest
	if !h.decode(w, r, &req) {
		return
	}
	c, err := h.svc.CreateConversation(r.Context(), app.CreateConversationInput{
		WorkspaceID:       ws,
		AgentID:           req.AgentID,
		Title:             req.Title,
		ContextReferences: toContextReferences(req.ContextReferences),
	})
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusCreated, c)
}

type renameConversationRequest struct {
	Title string `json:"title"`
}

func (h *Handler) renameConversation(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	id, ok := h.pathID(w, r)
	if !ok {
		return
	}
	var req renameConversationRequest
	if !h.decode(w, r, &req) {
		return
	}
	c, err := h.svc.RenameConversation(r.Context(), ws, id, req.Title)
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusOK, c)
}

func (h *Handler) getConversation(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	id, ok := h.pathID(w, r)
	if !ok {
		return
	}
	c, err := h.svc.GetConversation(r.Context(), ws, id)
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusOK, c)
}

// listConversations pages the workspace's threads, optionally narrowed to
// one agent with ?agent_id=.
//
// A filter on the collection rather than a route of its own: "this agent's
// conversations" is a subset of "the conversations", and a second endpoint
// would be a second place for the workspace scope to be got wrong.
func (h *Handler) listConversations(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	var agentID uuid.NullUUID
	if raw := r.URL.Query().Get("agent_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			h.badRequest(w, "agent_id must be a uuid")
			return
		}
		agentID = uuid.NullUUID{UUID: id, Valid: true}
	}
	limit, offset := parseLimitOffset(r)
	page, err := h.svc.ListConversations(r.Context(), app.ListConversationsInput{
		WorkspaceID: ws,
		AgentID:     agentID,
		Limit:       limit,
		Offset:      offset,
	})
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusOK, listEnvelope{
		Items:  page.Items,
		Limit:  page.Limit,
		Offset: page.Offset,
		Total:  &page.Total,
	})
}

func (h *Handler) deleteConversation(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	id, ok := h.pathID(w, r)
	if !ok {
		return
	}
	if err := h.svc.DeleteConversation(r.Context(), ws, id); err != nil {
		h.writeDomainErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// truncateMessages drops the message at {seq} and everything after it.
// Answers with how many rows went, so the client can tell a no-op from a
// real cut without refetching.
func (h *Handler) truncateMessages(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	id, ok := h.pathID(w, r)
	if !ok {
		return
	}
	seq, err := strconv.ParseInt(chi.URLParam(r, "seq"), 10, 64)
	if err != nil || seq < 1 {
		h.badRequest(w, "seq must be a positive integer")
		return
	}
	deleted, err := h.svc.TruncateFrom(r.Context(), ws, id, seq)
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusOK, map[string]int64{"deleted": deleted})
}

func (h *Handler) listMessages(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	id, ok := h.pathID(w, r)
	if !ok {
		return
	}
	limit := 0 // service applies its own ceiling
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	items, err := h.svc.ListMessages(r.Context(), ws, id, limit)
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	// ── Write receipts ride along with the transcript ───────────────
	//
	// A turn that changed something and a turn that merely SAID so are
	// identical in `items`: both are assistant prose. A live financial
	// agent reported "8 transações importadas" in a turn with zero tool
	// calls, and nothing in this response could have contradicted it.
	//
	// Now something can. Every assistant turn gets a receipt, including an
	// empty one, so a client renders a confirmed change from a fact rather
	// than from a sentence.
	//
	// Degrades rather than fails: a transcript is still worth reading
	// without its receipts, and the client shows nothing as confirmed when
	// they are missing — which errs the safe way.
	ids := make([]uuid.UUID, 0, len(items))
	for _, m := range items {
		if m.Role == domain.RoleAssistant {
			ids = append(ids, m.ID)
		}
	}
	payload := map[string]any{
		"items": items,
		"limit": app.TranscriptLimit(limit),
	}
	if receipts, rerr := h.svc.WriteReceipts(r.Context(), ws, ids); rerr != nil {
		h.log.Warn("read write receipts", "conversation_id", id, "err", rerr)
	} else {
		payload["write_receipts"] = receipts
	}
	// The read-side twin, so a reload shows the same evidence state the
	// live frame did. Built from the same audit rows, so the two cannot
	// disagree about a turn that read nothing.
	if receipts, rerr := h.svc.ReadReceipts(r.Context(), ws, id, ids); rerr != nil {
		h.log.Warn("read read receipts", "conversation_id", id, "err", rerr)
	} else {
		payload["read_receipts"] = receipts
	}
	render.JSON(w, http.StatusOK, payload)
}
