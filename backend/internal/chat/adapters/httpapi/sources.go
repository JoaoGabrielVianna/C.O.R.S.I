package httpapi

import (
	"net/http"

	"github.com/corsi/backend/internal/chat/app"
	"github.com/corsi/backend/internal/chat/domain"
	"github.com/corsi/backend/internal/platform/render"
)

// Agent Sources over HTTP.
//
// Same nesting as memories, and for the same reason: a source belongs to
// its agent, so there is no address for one that does not name it.
//
// One asymmetry worth stating: the list does NOT carry `content`. A source
// is up to twenty thousand characters and a list is a list — it returns
// titles, sizes and state, and `GET .../sources/{sourceId}` returns the
// document when something actually needs to read it.

type createSourceRequest struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Content     string `json:"content"`
}

func (h *Handler) createSource(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	agentID, ok := h.pathID(w, r)
	if !ok {
		return
	}
	var req createSourceRequest
	if !h.decode(w, r, &req) {
		return
	}
	src, err := h.svc.CreateSource(r.Context(), app.CreateSourceInput{
		WorkspaceID: ws,
		AgentID:     agentID,
		Title:       req.Title,
		Description: req.Description,
		Content:     req.Content,
	})
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusCreated, src)
}

type updateSourceRequest struct {
	Title       *string `json:"title"`
	Description *string `json:"description"`
	Content     *string `json:"content"`
	Enabled     *bool   `json:"enabled"`
}

func (h *Handler) updateSource(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	agentID, ok := h.pathID(w, r)
	if !ok {
		return
	}
	id, ok := h.pathUUID(w, r, "sourceId")
	if !ok {
		return
	}
	var req updateSourceRequest
	if !h.decode(w, r, &req) {
		return
	}
	src, err := h.svc.UpdateSource(r.Context(), app.UpdateSourceInput{
		WorkspaceID: ws,
		AgentID:     agentID,
		ID:          id,
		Title:       req.Title,
		Description: req.Description,
		Content:     req.Content,
		Enabled:     req.Enabled,
	})
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusOK, src)
}

func (h *Handler) getSource(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	agentID, ok := h.pathID(w, r)
	if !ok {
		return
	}
	id, ok := h.pathUUID(w, r, "sourceId")
	if !ok {
		return
	}
	src, err := h.svc.GetSource(r.Context(), ws, agentID, id)
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusOK, src)
}

func (h *Handler) deleteSource(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	agentID, ok := h.pathID(w, r)
	if !ok {
		return
	}
	id, ok := h.pathUUID(w, r, "sourceId")
	if !ok {
		return
	}
	if err := h.svc.DeleteSource(r.Context(), ws, agentID, id); err != nil {
		h.writeDomainErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// sourceListEnvelope mirrors the memory one: the list shape plus the three
// numbers the page needs to explain itself. `total` above `limit` means the
// read was capped, which is the only way a cap is allowed to happen.
type sourceListEnvelope struct {
	Items            []domain.Source `json:"items"`
	Limit            int             `json:"limit"`
	Total            int64           `json:"total"`
	UsedCharacters   int             `json:"used_characters"`
	BudgetCharacters int             `json:"budget_characters"`
}

func (h *Handler) listSources(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	agentID, ok := h.pathID(w, r)
	if !ok {
		return
	}
	page, err := h.svc.ListSources(r.Context(), ws, agentID)
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusOK, sourceListEnvelope{
		Items:            page.Items,
		Limit:            page.Limit,
		Total:            page.Total,
		UsedCharacters:   page.UsedCharacters,
		BudgetCharacters: page.BudgetCharacters,
	})
}
