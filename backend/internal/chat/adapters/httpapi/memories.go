package httpapi

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/chat/app"
	"github.com/corsi/backend/internal/chat/domain"
	"github.com/corsi/backend/internal/platform/render"
)

// Agent Memory over HTTP.
//
// Every route is nested under its agent, including the ones that address a
// single memory. That is not decoration: memory belongs to the agent, and an
// address that could reach a memory without naming its agent would be an
// address that disagrees with the model.

type createMemoryRequest struct {
	Content string `json:"content"`
	// SourceConversationID marks the memory as captured inside a thread —
	// the message action and /lembrar both send it. Absent means it was
	// typed on the Memory page.
	SourceConversationID *uuid.UUID `json:"source_conversation_id"`
	SourceMessageSeq     *int64     `json:"source_message_seq"`
	Pinned               bool       `json:"pinned"`
}

func (h *Handler) createMemory(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	agentID, ok := h.pathID(w, r)
	if !ok {
		return
	}
	var req createMemoryRequest
	if !h.decode(w, r, &req) {
		return
	}
	m, err := h.svc.CreateMemory(r.Context(), app.CreateMemoryInput{
		WorkspaceID:          ws,
		AgentID:              agentID,
		Content:              req.Content,
		SourceConversationID: req.SourceConversationID,
		SourceMessageSeq:     req.SourceMessageSeq,
		Pinned:               req.Pinned,
	})
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusCreated, m)
}

type updateMemoryRequest struct {
	Content *string `json:"content"`
	Enabled *bool   `json:"enabled"`
	Pinned  *bool   `json:"pinned"`
}

func (h *Handler) updateMemory(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	agentID, ok := h.pathID(w, r)
	if !ok {
		return
	}
	id, ok := h.pathUUID(w, r, "memoryId")
	if !ok {
		return
	}
	var req updateMemoryRequest
	if !h.decode(w, r, &req) {
		return
	}
	m, err := h.svc.UpdateMemory(r.Context(), app.UpdateMemoryInput{
		WorkspaceID: ws,
		AgentID:     agentID,
		ID:          id,
		Content:     req.Content,
		Enabled:     req.Enabled,
		Pinned:      req.Pinned,
	})
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusOK, m)
}

func (h *Handler) deleteMemory(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	agentID, ok := h.pathID(w, r)
	if !ok {
		return
	}
	id, ok := h.pathUUID(w, r, "memoryId")
	if !ok {
		return
	}
	if err := h.svc.DeleteMemory(r.Context(), ws, agentID, id); err != nil {
		h.writeDomainErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// memoryListEnvelope is the list envelope plus the three numbers the Memory
// page needs to explain itself.
//
// It is additive over the module's list shape: `items` and `limit` mean what
// they mean everywhere else. `total` says how many exist, so a capped read
// is never a silent cut; `used_characters` against `budget_characters` is
// what turns "8 memories" into "3 of 8 are actually being used", which is
// the only place the user finds out that some of what they saved is not
// reaching the model.
type memoryListEnvelope struct {
	Items            []domain.Memory `json:"items"`
	Limit            int             `json:"limit"`
	Total            int64           `json:"total"`
	UsedCharacters   int             `json:"used_characters"`
	BudgetCharacters int             `json:"budget_characters"`
}

func (h *Handler) listMemories(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	agentID, ok := h.pathID(w, r)
	if !ok {
		return
	}
	page, err := h.svc.ListMemories(r.Context(), ws, agentID)
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusOK, memoryListEnvelope{
		Items:            page.Items,
		Limit:            page.Limit,
		Total:            page.Total,
		UsedCharacters:   page.UsedCharacters,
		BudgetCharacters: page.BudgetCharacters,
	})
}

/* ── consolidation ───────────────────────────────────────────────────── */

// consolidateMemoryRequest is the snapshot the client is asking about.
//
// `up_to_seq` is the last message the user could see when they typed the
// command. It is a ceiling the server applies in SQL, never a promise the
// server makes back: what actually entered the operation is reported as
// `considered_messages` and `effective_up_to_seq`.
//
// Absent or zero means "the thread as it is now", which is the same
// guarantee by another route — a message written after the read does not
// exist yet as far as this operation is concerned.
type consolidateMemoryRequest struct {
	UpToSeq int64 `json:"up_to_seq"`
}

func (h *Handler) consolidateMemory(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	id, ok := h.pathID(w, r)
	if !ok {
		return
	}
	var req consolidateMemoryRequest
	if !h.decode(w, r, &req) {
		return
	}
	out, err := h.svc.ConsolidateMemory(r.Context(), app.ConsolidateMemoryInput{
		WorkspaceID:    ws,
		ConversationID: id,
		UpToSeq:        req.UpToSeq,
	})
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	// 200 and not 201: nothing was created. The whole point of the
	// operation is that it produces a proposal and leaves no row behind.
	render.JSON(w, http.StatusOK, out)
}

// confirmMemoryCandidatesRequest is the set the user kept.
//
// Texts, not ids: a candidate has no id because it was never stored, and
// the user is free to edit one before accepting it. What arrives here is
// what they approved.
type confirmMemoryCandidatesRequest struct {
	Contents []string `json:"contents"`
	UpToSeq  int64    `json:"up_to_seq"`
}

func (h *Handler) confirmMemoryCandidates(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	id, ok := h.pathID(w, r)
	if !ok {
		return
	}
	var req confirmMemoryCandidatesRequest
	if !h.decode(w, r, &req) {
		return
	}
	saved, err := h.svc.ConfirmCandidates(r.Context(), app.ConfirmCandidatesInput{
		WorkspaceID:    ws,
		ConversationID: id,
		Contents:       req.Contents,
		UpToSeq:        req.UpToSeq,
	})
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusCreated, map[string]any{"items": saved})
}
