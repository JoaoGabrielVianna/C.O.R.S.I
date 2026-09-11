package httpapi

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/chat/app"
	"github.com/corsi/backend/internal/chat/domain"
	"github.com/corsi/backend/internal/platform/render"
)

type createAgentRequest struct {
	ProviderID   uuid.UUID `json:"provider_id"`
	Name         string    `json:"name"`
	Description  string    `json:"description"`
	SystemPrompt string    `json:"system_prompt"`
	Model        string    `json:"model"`
	Temperature  *float32  `json:"temperature"`
	MaxTokens    *int      `json:"max_tokens"`
	HistoryLimit *int      `json:"history_limit"`
	Accent       string    `json:"accent"`
	// Daily limits. Absent or null means no limit; zero is a limit of zero.
	DailyTokenLimit   *int     `json:"daily_token_limit"`
	DailyCostLimitUSD *float64 `json:"daily_cost_limit_usd"`
	// MemoryPolicy is nested here, unlike the two flat budget fields above,
	// and deliberately so: it is nested in the agent this route returns, and
	// a create body that disagreed in shape with the document it produces is
	// a contract a client has to learn twice.
	MemoryPolicy *memoryPolicyRequest `json:"memory_policy"`
}

// memoryPolicyRequest is the wire form of the policy, on create and on
// update alike.
//
// There is no `model_proposed` anywhere near this file, and there is no
// route that accepts one. Provenance is decided by which application entry
// point ran, never by a request body — see app.CreateProposedMemory.
type memoryPolicyRequest struct {
	Mode  string `json:"mode"`
	Notes string `json:"notes"`
}

// memoryPolicyInput maps an absent object to "leave it alone" and a present
// one to a whole policy. The mode string is passed through unvalidated on
// purpose: the domain owns the vocabulary, so an unknown value comes back
// as the domain's own message instead of a second, drifting copy of it
// written here.
func memoryPolicyInput(req *memoryPolicyRequest) *domain.MemoryPolicy {
	if req == nil {
		return nil
	}
	return &domain.MemoryPolicy{
		Mode:  domain.MemoryPolicyMode(req.Mode),
		Notes: req.Notes,
	}
}

func (h *Handler) createAgent(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	var req createAgentRequest
	if !h.decode(w, r, &req) {
		return
	}
	a, err := h.svc.CreateAgent(r.Context(), app.CreateAgentInput{
		WorkspaceID:  ws,
		ProviderID:   req.ProviderID,
		Name:         req.Name,
		Description:  req.Description,
		SystemPrompt: req.SystemPrompt,
		Model:        req.Model,
		Temperature:  req.Temperature,
		MaxTokens:    req.MaxTokens,
		HistoryLimit: req.HistoryLimit,
		Accent:       req.Accent,
		Budget: domain.Budget{
			DailyTokenLimit:   req.DailyTokenLimit,
			DailyCostLimitUSD: req.DailyCostLimitUSD,
		},
		MemoryPolicy: memoryPolicyInput(req.MemoryPolicy),
	})
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusCreated, a)
}

type updateAgentRequest struct {
	ProviderID   *uuid.UUID `json:"provider_id"`
	Name         *string    `json:"name"`
	Description  *string    `json:"description"`
	SystemPrompt *string    `json:"system_prompt"`
	Model        *string    `json:"model"`
	Temperature  *float32   `json:"temperature"`
	MaxTokens    *int       `json:"max_tokens"`
	HistoryLimit *int       `json:"history_limit"`
	Accent       *string    `json:"accent"`
	// Budget is absent when the request does not mention it, and present
	// with null members when a limit is being removed. The two are different
	// requests, which is why it is a pointer to a struct of pointers.
	Budget *budgetRequest `json:"budget"`
	// Absent leaves the policy untouched. Present replaces it whole.
	MemoryPolicy *memoryPolicyRequest `json:"memory_policy"`
}

type budgetRequest struct {
	DailyTokenLimit   *int     `json:"daily_token_limit"`
	DailyCostLimitUSD *float64 `json:"daily_cost_limit_usd"`
}

func (h *Handler) updateAgent(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	id, ok := h.pathID(w, r)
	if !ok {
		return
	}
	var req updateAgentRequest
	if !h.decode(w, r, &req) {
		return
	}
	a, err := h.svc.UpdateAgent(r.Context(), app.UpdateAgentInput{
		WorkspaceID:  ws,
		ID:           id,
		ProviderID:   req.ProviderID,
		Name:         req.Name,
		Description:  req.Description,
		SystemPrompt: req.SystemPrompt,
		Model:        req.Model,
		Temperature:  req.Temperature,
		MaxTokens:    req.MaxTokens,
		HistoryLimit: req.HistoryLimit,
		Accent:       req.Accent,
		Budget:       budgetInput(req.Budget),
		MemoryPolicy: memoryPolicyInput(req.MemoryPolicy),
	})
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusOK, a)
}

// budgetInput turns "the request mentioned budget" into a value the service
// can tell apart from "it did not". Omitting the object leaves the limits
// alone; sending it with null members removes them.
func budgetInput(b *budgetRequest) *domain.Budget {
	if b == nil {
		return nil
	}
	return &domain.Budget{
		DailyTokenLimit:   b.DailyTokenLimit,
		DailyCostLimitUSD: b.DailyCostLimitUSD,
	}
}

func (h *Handler) agentBudget(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	id, ok := h.pathID(w, r)
	if !ok {
		return
	}
	status, err := h.svc.AgentBudget(r.Context(), ws, id)
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusOK, status)
}

func (h *Handler) getAgent(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	id, ok := h.pathID(w, r)
	if !ok {
		return
	}
	a, err := h.svc.GetAgent(r.Context(), ws, id)
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusOK, a)
}

func (h *Handler) listAgents(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	limit, offset := parseLimitOffset(r)
	items, err := h.svc.ListAgents(r.Context(), ws, limit, offset)
	if err != nil {
		h.writeDomainErr(w, err)
		return
	}
	render.JSON(w, http.StatusOK, listEnvelope{Items: items, Limit: limit, Offset: offset})
}

func (h *Handler) deleteAgent(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.workspaceID(w, r)
	if !ok {
		return
	}
	id, ok := h.pathID(w, r)
	if !ok {
		return
	}
	if err := h.svc.DeleteAgent(r.Context(), ws, id); err != nil {
		h.writeDomainErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
