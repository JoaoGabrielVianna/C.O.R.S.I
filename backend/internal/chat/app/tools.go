package app

import (
	"context"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/chat/domain"
	"github.com/corsi/backend/internal/chat/ports"
)

// Tool authorization: which capabilities an agent may use.
//
// ── The rule, stated once ──────────────────────────────────────────────
// DENY by default, at every layer, with no exception anywhere:
//
//	not in the registry        → denied (it does not exist)
//	in the registry, no grant  → denied (it exists and this agent may not)
//	grant for a name the
//	  registry does not know   → denied, and the stale grant is reported
//
// The model is never consulted about this. A tool call arriving from the
// provider is a *request*, and the fact that we declared a tool on the way
// out is not a permission on the way back: the grant is re-read from the
// store on the turn that uses it, and checked again per call. That
// redundancy is deliberate. It is what makes revoking a tool take effect on
// the very next turn rather than on the next restart, and it is what a
// prompt-injected call runs into.

// AgentToolView is one registry tool as an agent sees it: what it is, and
// whether this agent may use it.
//
// The whole registry is returned, not just the granted part, because the
// question the settings page asks is "what could this agent do?" and the
// answer has to include the things it currently cannot.
type AgentToolView struct {
	domain.ToolDefinition
	Authorized bool `json:"authorized"`
}

// AgentToolsReport is the answer to "which tools can this agent use?".
type AgentToolsReport struct {
	Items []AgentToolView `json:"items"`
	// AuthorizedCount is how many of Items are on. Computed here rather than
	// counted by each reader, so the number the settings page shows and the
	// number the turn acts on come from one place.
	AuthorizedCount int `json:"authorized_count"`
	// Stale lists grants whose tool no longer exists in this build — a name
	// that was authorized before a deploy removed it.
	//
	// Surfaced instead of swallowed. A row nobody can see is a row nobody
	// can clean up, and the alternative reading ("the agent has a tool we
	// cannot run") is exactly the kind of quiet divergence between
	// configuration and reality this module refuses everywhere else. It
	// grants nothing: a stale name resolves to no executor, so it is denied
	// by the same rule that denies an unknown one.
	Stale []domain.ToolName `json:"stale,omitempty"`
}

// AgentTools reports the catalogue as it stands for one agent.
func (s *Service) AgentTools(ctx context.Context, workspaceID, agentID uuid.UUID) (*AgentToolsReport, error) {
	// The agent is resolved first so an id from another workspace answers
	// 404 rather than "no tools authorized", which would be a true sentence
	// about a false premise.
	if _, err := s.repos.Agents.FindByID(ctx, workspaceID, agentID); err != nil {
		return nil, err
	}

	granted, err := s.repos.AgentTools.ListByAgent(ctx, workspaceID, agentID)
	if err != nil {
		return nil, err
	}
	grantedSet := make(map[domain.ToolName]bool, len(granted))
	for _, n := range granted {
		grantedSet[n] = true
	}

	defs := s.tools.Definitions()
	report := &AgentToolsReport{Items: make([]AgentToolView, 0, len(defs))}
	known := make(map[domain.ToolName]bool, len(defs))
	for _, d := range defs {
		known[d.Name] = true
		authorized := grantedSet[d.Name]
		if authorized {
			report.AuthorizedCount++
		}
		report.Items = append(report.Items, AgentToolView{ToolDefinition: d, Authorized: authorized})
	}
	for _, n := range granted {
		if !known[n] {
			report.Stale = append(report.Stale, n)
		}
	}
	return report, nil
}

// AuthorizeTool grants one tool to one agent.
//
// The name is checked against the registry before the row is written. A
// grant for something that does not exist would be a permission with no
// referent — it could never be exercised, would show up as stale the moment
// it was created, and would make the API accept typos silently.
func (s *Service) AuthorizeTool(ctx context.Context, workspaceID, agentID uuid.UUID, name domain.ToolName) error {
	if _, err := s.repos.Agents.FindByID(ctx, workspaceID, agentID); err != nil {
		return err
	}
	if !name.Valid() {
		return domain.Invalid("tool_name is not a valid tool name")
	}
	if _, ok := s.tools.Lookup(name); !ok {
		return domain.NotFound("tool " + name.String())
	}
	return s.repos.AgentTools.Authorize(ctx, workspaceID, agentID, name)
}

// RevokeTool removes a grant.
//
// It does NOT check the registry. Revoking is how a stale grant gets
// cleaned up, and requiring the tool to still exist would make the only
// rows that need removing the only ones that cannot be removed.
func (s *Service) RevokeTool(ctx context.Context, workspaceID, agentID uuid.UUID, name domain.ToolName) error {
	if _, err := s.repos.Agents.FindByID(ctx, workspaceID, agentID); err != nil {
		return err
	}
	return s.repos.AgentTools.Revoke(ctx, workspaceID, agentID, name)
}

// MaxToolCallsPerConversation bounds the audit read.
//
// Exported because the transport reports it as the `limit` of the list
// envelope, and that field means "the cap the server applied". A hand-typed
// copy in the handler would be the same number in two places, free to
// disagree on the first change.
//
// A turn runs at most `maxToolRounds` rounds, and a round asks for a handful
// of tools; five hundred rows is far more than a readable thread produces
// and small enough to draw. A thread that reaches it is one whose oldest
// tool calls have scrolled out of any reasonable transcript anyway.
const MaxToolCallsPerConversation = 500

// ConversationToolCalls reads the audit trail of one thread.
//
// The conversation is resolved first, so an id from another workspace
// answers 404 rather than an empty list — an empty list would be a true
// sentence about a thread the caller does not own.
func (s *Service) ConversationToolCalls(ctx context.Context, workspaceID, conversationID uuid.UUID) ([]domain.ToolCallRecord, error) {
	if _, err := s.repos.Conversations.FindByID(ctx, workspaceID, conversationID); err != nil {
		return nil, err
	}
	return s.repos.ToolCalls.ListByConversation(ctx, workspaceID, conversationID, MaxToolCallsPerConversation)
}

/* ── the turn's read ─────────────────────────────────────────────────── */

// authorizedTools resolves what one turn may declare to the model.
//
// Returns the definitions in registry order, which is name order, so two
// identical turns build an identical request body.
//
// ── The fast path ──────────────────────────────────────────────────────
// An agent with no grants costs one indexed read of a table whose rows it
// does not have, and then nothing: no registry walk, no serialisation, no
// `tools` field on the wire. That is the whole of what this feature costs
// an agent that does not use it.
//
// ── Why a failure here is fatal and not a degradation ──────────────────
// Memory and Sources degrade: a turn that cannot read them answers with
// less context, which is worse but honest. Tools cannot take that trade in
// either direction. Carrying on with no tools would silently turn a turn
// that needed a capability into one that invents an answer instead; and the
// authorization read is the same read the per-call check depends on, so a
// failure here is a failure of the thing that says no. It refuses.
func (s *Service) authorizedTools(ctx context.Context, workspaceID, agentID uuid.UUID) ([]domain.ToolDefinition, error) {
	granted, err := s.repos.AgentTools.ListByAgent(ctx, workspaceID, agentID)
	if err != nil {
		return nil, err
	}
	if len(granted) == 0 {
		return nil, nil
	}
	out := make([]domain.ToolDefinition, 0, len(granted))
	for _, name := range granted {
		t, ok := s.tools.Lookup(name)
		if !ok {
			// A grant left behind by a deploy that removed the tool. Not an
			// error for the turn — the agent simply cannot use what is not
			// there — but it is logged, because the row will keep showing up
			// until someone revokes it.
			s.log.Warn("authorized tool is not in the registry",
				"agent_id", agentID, "tool", name)
			continue
		}
		out = append(out, t.Definition())
	}
	return out, nil
}

// isAuthorized answers the per-call question, against the names the turn
// read from the store.
//
// It takes the definitions the turn is working with rather than querying
// again: re-reading per call would let a revoke land in the middle of a
// turn and produce a turn that declared a tool and then refused it, which
// is a confusing state to explain and no safer. The grant is re-read on
// every turn, which is the guarantee that matters.
func isAuthorized(defs []domain.ToolDefinition, name domain.ToolName) (domain.ToolDefinition, bool) {
	for _, d := range defs {
		if d.Name == name {
			return d, true
		}
	}
	return domain.ToolDefinition{}, false
}

// toolRegistryOrEmpty guarantees the service always has a registry.
//
// A nil registry would make every turn panic on the authorization read
// rather than simply having no tools, and the zero value of "no tools
// configured" has to be a working system.
type emptyRegistry struct{}

func (emptyRegistry) Lookup(domain.ToolName) (ports.Tool, bool) { return nil, false }
func (emptyRegistry) Definitions() []domain.ToolDefinition      { return nil }

// WriteReceipts is the product's answer to "did that turn actually change
// anything?", for a set of assistant turns.
//
// ── Why this exists as a first-class read ──────────────────────────────
// A live financial agent answered "8 transações importadas" in a turn that
// made zero tool calls, against a ledger holding zero transactions. The
// runtime was right and the person was misinformed, because the absence of
// a tool call is invisible where the answer is rendered.
//
// So the answer stops being an absence. Every surface that reports a
// change routes through this, and the model's sentence is not one of its
// inputs.
func (s *Service) WriteReceipts(ctx context.Context, workspaceID uuid.UUID, messageIDs []uuid.UUID) (map[uuid.UUID]domain.WriteReceipt, error) {
	return s.repos.ToolCalls.WriteReceiptsFor(ctx, workspaceID, messageIDs)
}

// ReadReceipts is the product's answer to "did that turn actually read
// anything outside this product?".
//
// ── Why `available` is computed here ───────────────────────────────────
// The three statuses are derived from execution records and are statements
// about the past. `available` is not: it asks whether this agent has any
// external capability AT ALL, which is a fact about the configuration
// right now, and it exists only so a surface can stay quiet for agents
// where the absence of an external read is meaningless.
//
// Deriving it from the agent's present grants is therefore correct AND
// deliberately weaker than the rest of the receipt. It is a presentation
// gate; nothing about what a turn may claim depends on it.
// Taking the CONVERSATION rather than the agent, because that is what both
// callers hold and because resolving one to the other is this layer's job,
// not a handler's.
func (s *Service) ReadReceipts(ctx context.Context, workspaceID, conversationID uuid.UUID, messageIDs []uuid.UUID) (map[uuid.UUID]domain.ReadReceipt, error) {
	conv, err := s.repos.Conversations.FindByID(ctx, workspaceID, conversationID)
	if err != nil {
		return nil, err
	}
	available, err := s.hasExternalCapability(ctx, workspaceID, conv.AgentID)
	if err != nil {
		return nil, err
	}
	return s.repos.ToolCalls.ReadReceiptsFor(ctx, workspaceID, messageIDs, available)
}

// hasExternalCapability reports whether this agent may read anything
// outside the product.
//
// Grants intersected with the registry, in that order: a grant for a tool
// this build no longer has is a stale row, not a capability — the same
// rule authorizedTools follows.
func (s *Service) hasExternalCapability(ctx context.Context, workspaceID, agentID uuid.UUID) (bool, error) {
	granted, err := s.repos.AgentTools.ListByAgent(ctx, workspaceID, agentID)
	if err != nil {
		return false, err
	}
	for _, name := range granted {
		if tool, ok := s.tools.Lookup(name); ok && tool.Definition().External {
			return true, nil
		}
	}
	return false, nil
}
