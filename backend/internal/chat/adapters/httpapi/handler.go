// Package httpapi is the driving adapter that exposes the chat bounded
// context over HTTP. Handlers parse DTOs, call the application service, and
// translate domain errors into HTTP error responses.
//
// Wire shape:
//
//	error responses: { "error": { "code": "...", "message": "..." } }
//	success reads:   the domain entity as JSON
//	list reads:      { "items": [...], "limit": N, "offset": N }
//
// The one exception is POST /conversations/{id}/messages, which answers
// with an event stream instead of a document. See stream.go.
package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/corsi/backend/internal/chat/app"
	"github.com/corsi/backend/internal/chat/domain"
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
	r.Get("/ping", h.ping)

	// Everything the workspace consumed, over an optional ?from/?to window.
	// The only usage route that belongs to no agent and no conversation,
	// which is why it sits at the root rather than under either.
	r.Get("/usage", h.workspaceUsage)

	r.Route("/providers", func(r chi.Router) {
		r.Post("/", h.createProvider)
		r.Get("/", h.listProviders)
		r.Get("/{id}", h.getProvider)
		r.Patch("/{id}", h.updateProvider)
		r.Delete("/{id}", h.deleteProvider)
		// Lists the catalog and, in doing so, verifies the credential.
		r.Get("/{id}/models", h.listProviderModels)
		// Real billed spend of this provider's key, read from the gateway.
		r.Get("/{id}/spend", h.providerSpend)
	})

	r.Route("/agents", func(r chi.Router) {
		r.Post("/", h.createAgent)
		r.Get("/", h.listAgents)
		r.Get("/{id}", h.getAgent)
		r.Patch("/{id}", h.updateAgent)
		r.Delete("/{id}", h.deleteAgent)
		// What every conversation this agent owns consumed and cost.
		r.Get("/{id}/usage", h.agentUsage)
		// What the agent remembers between conversations. Nested under the
		// agent because memory belongs to it — there is no address for a
		// memory that does not name its agent.
		r.Post("/{id}/memories", h.createMemory)
		r.Get("/{id}/memories", h.listMemories)
		r.Patch("/{id}/memories/{memoryId}", h.updateMemory)
		r.Delete("/{id}/memories/{memoryId}", h.deleteMemory)
		// Reference material the agent can consult. The list omits the text
		// of each source; the read by id is what returns a document.
		r.Post("/{id}/sources", h.createSource)
		r.Get("/{id}/sources", h.listSources)
		r.Get("/{id}/sources/{sourceId}", h.getSource)
		r.Patch("/{id}/sources/{sourceId}", h.updateSource)
		r.Delete("/{id}/sources/{sourceId}", h.deleteSource)
		// Where this agent stands against its daily limits, right now.
		// Configuration is written through PATCH /agents/{id}; this is the
		// read of what that configuration currently means.
		r.Get("/{id}/budget", h.agentBudget)
		// Which capabilities this agent may use. The catalogue comes from the
		// code registry; only the grant is stored, and only a grant can be
		// written — there is no route that creates a tool, because a tool is
		// code.
		r.Get("/{id}/tools", h.listAgentTools)
		r.Post("/{id}/tools", h.authorizeAgentTool)
		r.Delete("/{id}/tools/{toolName}", h.revokeAgentTool)
	})

	r.Route("/conversations", func(r chi.Router) {
		r.Post("/", h.createConversation)
		r.Get("/", h.listConversations)
		r.Get("/{id}", h.getConversation)
		r.Patch("/{id}", h.renameConversation)
		r.Delete("/{id}", h.deleteConversation)
		r.Get("/{id}/messages", h.listMessages)
		// Drops a user turn and everything after it, so regenerate and edit
		// replace a turn instead of appending a duplicate question.
		r.Delete("/{id}/messages/{seq}", h.truncateMessages)
		// What this one conversation consumed and cost.
		r.Get("/{id}/usage", h.conversationUsage)
		// The audit trail of the tools this thread ran. A separate read
		// rather than a field on every message: a conversation that never
		// used a tool must not pay a query to discover that, and the
		// transcript already knows from the turn's report whether there is
		// anything to fetch.
		r.Get("/{id}/tool-calls", h.listConversationToolCalls)
		// Streams the reply as Server-Sent Events rather than returning a
		// document. See sendMessage in stream.go.
		r.Post("/{id}/messages", h.sendMessage)
		// Continue a turn that stopped with work already done. Not a
		// message: it adds no question, it finishes answering the one
		// already there. See resumeMessage in stream.go.
		r.Post("/{id}/resume", h.resumeMessage)
		// Reads this thread as it stands and proposes what is worth
		// remembering. A POST because it spends money and writes a receipt,
		// and a sub-resource of the conversation because the conversation is
		// what it reads. It creates no memory: the candidates it returns are
		// saved, if at all, through the memory route the user confirms
		// against.
		r.Post("/{id}/memory-candidates", h.consolidateMemory)
		// Saves the candidates the user kept. Under the conversation
		// because provenance is the conversation, and separate from the
		// agent's own memory route because the two record different facts
		// about how a memory came to exist — one is the user's words, this
		// one is a proposal they approved.
		r.Post("/{id}/memories", h.confirmMemoryCandidates)
	})
}

func (h *Handler) ping(w http.ResponseWriter, _ *http.Request) {
	render.JSON(w, http.StatusOK, map[string]string{"module": "chat", "status": "ok"})
}

// --- helpers --------------------------------------------------------------

func (h *Handler) workspaceID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, ok := workspace.FromContext(r.Context())
	if !ok {
		apierror.Write(w, h.log, apierror.New(http.StatusForbidden, "workspace_missing", "no workspace in context"))
		return uuid.Nil, false
	}
	return id, true
}

// domainAPIError maps a domain error onto the wire error it should become.
// Split out from writeDomainErr so the SSE path can reuse the mapping to
// build an in-stream error frame.
func domainAPIError(err error) *apierror.Error {
	var de *domain.Error
	if !errors.As(err, &de) {
		var already *apierror.Error
		if errors.As(err, &already) {
			return already
		}
		return nil
	}
	// A domain error may carry its own wire code when one kind covers
	// several machine-readable reasons. Budget is the first: a client has to
	// tell "out of tokens" from "could not price this turn" without reading
	// the sentence.
	code := func(fallback string) string {
		if de.Code != "" {
			return de.Code
		}
		return fallback
	}

	switch de.Kind {
	case domain.KindInvalid:
		return apierror.New(http.StatusBadRequest, code("invalid"), de.Message)
	case domain.KindNotFound:
		return apierror.New(http.StatusNotFound, "not_found", de.Message)
	case domain.KindConflict:
		// Same rule as KindInvalid above: a kind that covers more than one
		// machine-readable reason may carry its own code. Every conflict
		// that does not set one still answers "conflict".
		return apierror.New(http.StatusConflict, code("conflict"), de.Message)
	case domain.KindUpstream:
		// The provider failed, not us. 502 keeps a bad API key or a
		// downed gateway out of this service's error budget.
		return apierror.New(http.StatusBadGateway, "upstream", de.Message)
	case domain.KindNotConfig:
		// Missing deployment config. 503 because retrying changes nothing
		// until an operator sets the variable.
		return apierror.New(http.StatusServiceUnavailable, "not_configured", de.Message)
	case domain.KindBudget:
		// The system refusing a turn under a limit its own user set. 429
		// because that is the standard shape of "not now, try later": the
		// request is well formed, the caller is authorised, nothing is in
		// conflict, and the condition clears with the passage of time.
		//
		// Emphatically NOT 502: the provider was never called, and putting a
		// deliberate refusal into the gateway's error budget would make an
		// intentional feature look like an outage.
		return apierror.New(http.StatusTooManyRequests, code("budget_exceeded"), de.Message)
	case domain.KindToolLoop:
		// The turn asked for tools more times than one turn may. 409 rather
		// than 429: waiting changes nothing, and repeating the same question
		// unchanged will reach the same ceiling. Emphatically not 500 —
		// nothing failed, this is the loop guard doing its job — and not 502,
		// which would put a deliberate stop into the gateway's error budget.
		return apierror.New(http.StatusConflict, code("tool_round_limit"), de.Message)
	default:
		return nil
	}
}

func (h *Handler) writeDomainErr(w http.ResponseWriter, err error) {
	if mapped := domainAPIError(err); mapped != nil {
		apierror.Write(w, h.log, mapped)
		return
	}
	apierror.Write(w, h.log, err)
}

func (h *Handler) badRequest(w http.ResponseWriter, msg string) {
	apierror.Write(w, h.log, apierror.New(http.StatusBadRequest, "invalid", msg))
}

// decode reads a JSON body, answering 400 itself on failure. Returns false
// when the caller should stop.
func (h *Handler) decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := render.DecodeJSON(r, dst); err != nil {
		h.badRequest(w, err.Error())
		return false
	}
	return true
}

// pathID parses the {id} URL param, answering 400 itself on failure.
func (h *Handler) pathID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	return h.pathUUID(w, r, "id")
}

// pathUUID is the same for any named param, which routes nested two levels
// deep need: `/agents/{id}/memories/{memoryId}` has two of them.
func (h *Handler) pathUUID(w http.ResponseWriter, r *http.Request, name string) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, name))
	if err != nil {
		h.badRequest(w, name+" must be a uuid")
		return uuid.Nil, false
	}
	return id, true
}

func parseLimitOffset(r *http.Request) (limit, offset int) {
	limit = 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}
	return
}

type listEnvelope struct {
	Items  any `json:"items"`
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
	// Total is how many rows the request matched before the page bounds.
	// Omitted by the routes that do not count, so an absent field reads as
	// "not counted" rather than as zero. Additive: the routes that never
	// carried it are byte-identical on the wire.
	Total *int64 `json:"total,omitempty"`
}
