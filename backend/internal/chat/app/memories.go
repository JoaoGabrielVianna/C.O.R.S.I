package app

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/chat/domain"
)

// Agent Memory: the application half of what an agent remembers.
//
// Every operation here starts by resolving the agent through the repository
// rather than trusting the id in the URL. Agents are soft-deleted and
// workspace-scoped, so the lookup is what turns "an agent that belongs to
// someone else" and "an agent that was removed" into the same 404 — an id
// in a URL is never authorisation.

// MemoryListLimit caps one page of memories.
//
// There is no client-controlled paging: memory is hand-curated, and an agent
// with five hundred of them is already a different problem than paging.
// The cap exists so an unbounded read cannot happen, and CountMemories is
// returned alongside so reaching it is never silent.
const MemoryListLimit = 500

type CreateMemoryInput struct {
	WorkspaceID uuid.UUID
	AgentID     uuid.UUID
	Content     string
	// SourceConversationID marks the memory as captured inside a thread.
	// Nil means it was typed on the Memory page.
	SourceConversationID *uuid.UUID
	SourceMessageSeq     *int64
	Pinned               bool
}

// CreateMemory records one thing the agent should remember.
//
// It never calls the provider. That is not an implementation detail, it is
// the promise the capture mechanism makes: saving a memory costs zero
// tokens and asks the model nothing. TestSavingAMemoryNeverCallsTheProvider
// is what keeps it true.
//
// This is the path every client uses, and everything it writes is the
// user's own doing: the text came from a form or from a message they chose.
// So `model_proposed` is false here, written out rather than left to the
// zero value, and CreateMemoryInput has no field that could say otherwise.
// A request cannot manufacture provenance it did not earn — see
// CreateProposedMemory for the only path that can.
func (s *Service) CreateMemory(ctx context.Context, in CreateMemoryInput) (*domain.Memory, error) {
	return s.createMemory(ctx, in, false)
}

// CreateProposedMemoryInput is a memory a model composed and the user
// approved.
//
// It is a different type from CreateMemoryInput, and the difference is the
// point: SourceConversationID is a value rather than a pointer, because
// there is no such thing as a proposal that came from nowhere. The domain
// refuses the combination anyway; requiring it here means the caller cannot
// even express it.
type CreateProposedMemoryInput struct {
	WorkspaceID          uuid.UUID
	AgentID              uuid.UUID
	Content              string
	SourceConversationID uuid.UUID
	SourceMessageSeq     *int64
}

// CreateProposedMemory records a memory whose words came from the model.
//
// ── Why this is a separate entry point ─────────────────────────────────
// The alternative was one function with a boolean on its input. That
// boolean would then be settable by whoever built the input, which is every
// caller including the HTTP layer, and the guarantee would rest on nobody
// ever wiring it through. A distinct function named for what it does is
// reachable only by code that means it, and its callers are greppable.
//
// It has no transport in this version and it is not supposed to: the flow
// that proposes candidates is the next batch's. What exists here is the
// seam that flow will call, with the provenance rule already enforced and
// already tested, so the batch that adds the operation does not also get to
// decide what honesty means.
//
// Like CreateMemory, it calls no provider. Whatever asked the model
// anything did so before reaching here, and paid for it through the
// budget-gated path that recorded a receipt.
func (s *Service) CreateProposedMemory(ctx context.Context, in CreateProposedMemoryInput) (*domain.Memory, error) {
	conversationID := in.SourceConversationID
	return s.createMemory(ctx, CreateMemoryInput{
		WorkspaceID:          in.WorkspaceID,
		AgentID:              in.AgentID,
		Content:              in.Content,
		SourceConversationID: &conversationID,
		SourceMessageSeq:     in.SourceMessageSeq,
	}, true)
}

// createMemory is the shared body. `modelProposed` is a parameter of this
// unexported function and of nothing else, so the only way to pass true is
// to be CreateProposedMemory.
func (s *Service) createMemory(ctx context.Context, in CreateMemoryInput, modelProposed bool) (*domain.Memory, error) {
	agent, err := s.repos.Agents.FindByID(ctx, in.WorkspaceID, in.AgentID)
	if err != nil {
		return nil, err
	}

	origin := domain.OriginManual
	var conversationID *uuid.UUID
	var messageSeq *int64

	if in.SourceConversationID != nil {
		// Resolved rather than trusted: a memory whose provenance points at
		// another agent's thread, or at nothing at all, would make the
		// "why does it know this?" answer a lie.
		conv, err := s.repos.Conversations.FindByID(ctx, in.WorkspaceID, *in.SourceConversationID)
		if err != nil {
			return nil, err
		}
		if conv.AgentID != agent.ID {
			return nil, domain.Invalid("the source conversation belongs to another agent")
		}
		origin = domain.OriginConversation
		conversationID = &conv.ID
		messageSeq = in.SourceMessageSeq
	}

	m := &domain.Memory{
		ID:                   uuid.New(),
		WorkspaceID:          in.WorkspaceID,
		AgentID:              agent.ID,
		Content:              strings.TrimSpace(in.Content),
		Origin:               origin,
		SourceConversationID: conversationID,
		SourceMessageSeq:     messageSeq,
		// New memories are on. A memory saved and then not used would be a
		// puzzle, and turning one off is one click away.
		Enabled:       true,
		Pinned:        in.Pinned,
		ModelProposed: modelProposed,
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	if err := s.repos.Memories.Create(ctx, m); err != nil {
		return nil, err
	}
	return m, nil
}

type UpdateMemoryInput struct {
	WorkspaceID uuid.UUID
	AgentID     uuid.UUID
	ID          uuid.UUID
	Content     *string
	Enabled     *bool
	Pinned      *bool
}

// UpdateMemory edits the mutable half. Origin and provenance are not in the
// input: they record what happened, and rewriting the text of a memory does
// not change where it came from.
func (s *Service) UpdateMemory(ctx context.Context, in UpdateMemoryInput) (*domain.Memory, error) {
	current, err := s.memoryOfAgent(ctx, in.WorkspaceID, in.AgentID, in.ID)
	if err != nil {
		return nil, err
	}
	if in.Content != nil {
		current.Content = strings.TrimSpace(*in.Content)
	}
	if in.Enabled != nil {
		current.Enabled = *in.Enabled
	}
	if in.Pinned != nil {
		current.Pinned = *in.Pinned
	}
	if err := current.Validate(); err != nil {
		return nil, err
	}
	if err := s.repos.Memories.Update(ctx, current); err != nil {
		return nil, err
	}
	return current, nil
}

func (s *Service) DeleteMemory(ctx context.Context, workspaceID, agentID, id uuid.UUID) error {
	if _, err := s.memoryOfAgent(ctx, workspaceID, agentID, id); err != nil {
		return err
	}
	return s.repos.Memories.SoftDelete(ctx, workspaceID, id)
}

// MemoryPage is one agent's memory, plus the two numbers that make the list
// legible: how many exist, and how much of the budget the selection spends.
type MemoryPage struct {
	Items []domain.Memory
	Total int64
	// UsedCharacters is what the selected memories would contribute to the
	// next turn, header included — the same figure the context builder
	// charges.
	UsedCharacters   int
	BudgetCharacters int
	Limit            int
}

// ListMemories is the management read, with each item marked according to
// whether it would actually reach the model.
//
// The marking is the point of the screen. Without it the page is a list of
// things the user believes the agent knows, and the ones the budget quietly
// dropped look exactly like the ones it kept.
func (s *Service) ListMemories(ctx context.Context, workspaceID, agentID uuid.UUID) (*MemoryPage, error) {
	if _, err := s.repos.Agents.FindByID(ctx, workspaceID, agentID); err != nil {
		return nil, err
	}
	items, err := s.repos.Memories.ListByAgent(ctx, workspaceID, agentID, MemoryListLimit)
	if err != nil {
		return nil, err
	}
	total, err := s.repos.Memories.CountByAgent(ctx, workspaceID, agentID)
	if err != nil {
		return nil, err
	}

	// The selection runs over the enabled subset in the order the list is
	// already in, which is the order the repository sorts both reads by.
	enabled := make([]domain.Memory, 0, len(items))
	for _, m := range items {
		if m.Enabled {
			enabled = append(enabled, m)
		}
	}
	selected, _ := SelectMemories(enabled, MemoryBudgetChars)

	inContext := make(map[uuid.UUID]struct{}, len(selected))
	for _, m := range selected {
		inContext[m.ID] = struct{}{}
	}
	for i := range items {
		_, ok := inContext[items[i].ID]
		items[i].InContext = ok
	}

	used := 0
	if len(selected) > 0 {
		used = len([]rune(RenderMemoryBlock(selected)))
	}

	return &MemoryPage{
		Items:            items,
		Total:            total,
		UsedCharacters:   used,
		BudgetCharacters: MemoryBudgetChars,
		Limit:            MemoryListLimit,
	}, nil
}

// memoryOfAgent resolves a memory through its agent, so a memory id from
// one agent's URL cannot address another agent's row. Memory belongs to the
// agent, and the address says so.
func (s *Service) memoryOfAgent(ctx context.Context, workspaceID, agentID, id uuid.UUID) (*domain.Memory, error) {
	if _, err := s.repos.Agents.FindByID(ctx, workspaceID, agentID); err != nil {
		return nil, err
	}
	m, err := s.repos.Memories.FindByID(ctx, workspaceID, id)
	if err != nil {
		return nil, err
	}
	if m.AgentID != agentID {
		return nil, domain.NotFound("memory")
	}
	return m, nil
}

// memoriesForTurn reads what the agent remembers, and reports a failure as
// a degraded turn rather than a refused one.
//
// The trade is deliberate: an unreachable side table costing the user their
// answer would be a worse outcome than an answer composed with less
// context. The loss is not silent — it reaches the ContextReport as
// ReasonUnavailable and is logged at warn.
func (s *Service) memoriesForTurn(ctx context.Context, workspaceID, agentID uuid.UUID) ([]domain.Memory, bool) {
	memories, err := s.repos.Memories.ListForContext(ctx, workspaceID, agentID)
	if err != nil {
		s.log.Warn("read agent memory for turn", "agent_id", agentID, "err", err)
		return nil, true
	}
	return memories, false
}
