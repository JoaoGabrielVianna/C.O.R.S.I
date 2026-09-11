package app

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/chat/domain"
)

// Agent Sources: the application half of the reference material an agent
// can consult.
//
// Structurally the same shape as memories.go, and that is on purpose: the
// two are different capabilities with one ownership rule. Every operation
// resolves the agent through the repository rather than trusting the id in
// the URL, so "belongs to another workspace" and "was removed" are the same
// 404.

// SourceListLimit caps one page of sources.
//
// There is no client-controlled paging. Sources are hand-written reference
// documents, and an agent with two hundred of them is a different problem
// than pagination. The cap exists so an unbounded read cannot happen, and
// the total is returned alongside so reaching it is never silent.
const SourceListLimit = 200

type CreateSourceInput struct {
	WorkspaceID uuid.UUID
	AgentID     uuid.UUID
	Title       string
	Description string
	Content     string
}

func (s *Service) CreateSource(ctx context.Context, in CreateSourceInput) (*domain.Source, error) {
	agent, err := s.repos.Agents.FindByID(ctx, in.WorkspaceID, in.AgentID)
	if err != nil {
		return nil, err
	}

	src := &domain.Source{
		ID:          uuid.New(),
		WorkspaceID: in.WorkspaceID,
		AgentID:     agent.ID,
		Title:       strings.TrimSpace(in.Title),
		Description: strings.TrimSpace(in.Description),
		// Content is NOT trimmed of its interior shape — only of the
		// leading and trailing whitespace a paste tends to carry. The
		// paragraphs are part of what a document says.
		Content: strings.TrimSpace(in.Content),
		// New sources are on. One that had to be switched on after writing
		// it would be a step whose only purpose is to be forgotten.
		Enabled: true,
	}
	if err := src.Validate(); err != nil {
		return nil, err
	}
	if err := s.repos.Sources.Create(ctx, src); err != nil {
		return nil, err
	}
	s.decorateSource(src)
	return src, nil
}

type UpdateSourceInput struct {
	WorkspaceID uuid.UUID
	AgentID     uuid.UUID
	ID          uuid.UUID
	Title       *string
	Description *string
	Content     *string
	Enabled     *bool
}

func (s *Service) UpdateSource(ctx context.Context, in UpdateSourceInput) (*domain.Source, error) {
	current, err := s.sourceOfAgent(ctx, in.WorkspaceID, in.AgentID, in.ID)
	if err != nil {
		return nil, err
	}
	if in.Title != nil {
		current.Title = strings.TrimSpace(*in.Title)
	}
	if in.Description != nil {
		current.Description = strings.TrimSpace(*in.Description)
	}
	if in.Content != nil {
		current.Content = strings.TrimSpace(*in.Content)
	}
	if in.Enabled != nil {
		current.Enabled = *in.Enabled
	}
	if err := current.Validate(); err != nil {
		return nil, err
	}
	if err := s.repos.Sources.Update(ctx, current); err != nil {
		return nil, err
	}
	s.decorateSource(current)
	return current, nil
}

func (s *Service) DeleteSource(ctx context.Context, workspaceID, agentID, id uuid.UUID) error {
	if _, err := s.sourceOfAgent(ctx, workspaceID, agentID, id); err != nil {
		return err
	}
	return s.repos.Sources.SoftDelete(ctx, workspaceID, id)
}

// GetSource reads one source with its text.
//
// It exists because the list deliberately does not carry content: the
// editor asks for the document when it opens it. See SourceRepo.ListByAgent.
func (s *Service) GetSource(ctx context.Context, workspaceID, agentID, id uuid.UUID) (*domain.Source, error) {
	src, err := s.sourceOfAgent(ctx, workspaceID, agentID, id)
	if err != nil {
		return nil, err
	}
	s.decorateSource(src)
	return src, nil
}

// SourcePage is one agent's reference material, plus the numbers that make
// the list legible.
type SourcePage struct {
	Items []domain.Source
	Total int64
	// UsedCharacters is what the selected sources would contribute to the
	// next turn, header included — the same figure the context builder
	// charges.
	UsedCharacters   int
	BudgetCharacters int
	Limit            int
}

// ListSources is the management read, with each source marked according to
// whether it would actually reach the model.
//
// The marking is what turns a list of documents into a decision: without
// it, a source that the budget is silently leaving out looks exactly like
// one that is being used on every turn.
func (s *Service) ListSources(ctx context.Context, workspaceID, agentID uuid.UUID) (*SourcePage, error) {
	if _, err := s.repos.Agents.FindByID(ctx, workspaceID, agentID); err != nil {
		return nil, err
	}
	items, err := s.repos.Sources.ListByAgent(ctx, workspaceID, agentID, SourceListLimit)
	if err != nil {
		return nil, err
	}
	total, err := s.repos.Sources.CountByAgent(ctx, workspaceID, agentID)
	if err != nil {
		return nil, err
	}

	// The selection runs over the enabled subset, in the order the list is
	// already in — the same order both repository reads sort by.
	//
	// These rows carry no Content, and the measurement is still exact:
	// sourceCost falls back to Characters, which the database counted. See
	// sourceContentChars.
	enabled := make([]domain.Source, 0, len(items))
	for _, src := range items {
		if src.Enabled {
			enabled = append(enabled, src)
		}
	}
	selected, _ := SelectSources(enabled, SourcesBudgetChars)

	inContext := make(map[uuid.UUID]struct{}, len(selected))
	used := 0
	for _, src := range selected {
		inContext[src.ID] = struct{}{}
		used += sourceCost(src)
	}
	if len(selected) > 0 {
		used += len([]rune(sourcesHeader))
	}

	for i := range items {
		_, ok := inContext[items[i].ID]
		items[i].InContext = ok
		items[i].EstimatedTokens = EstimateTokens(items[i].Characters)
		items[i].Oversized = SourceExceedsBudget(items[i], SourcesBudgetChars)
	}

	return &SourcePage{
		Items:            items,
		Total:            total,
		UsedCharacters:   used,
		BudgetCharacters: SourcesBudgetChars,
		Limit:            SourceListLimit,
	}, nil
}

// decorateSource fills the read-time fields on a single record, so one
// source answers the same questions the list does.
func (s *Service) decorateSource(src *domain.Source) {
	src.Characters = len([]rune(src.Content))
	src.EstimatedTokens = EstimateTokens(src.Characters)
	src.Oversized = SourceExceedsBudget(*src, SourcesBudgetChars)
	// InContext is deliberately NOT set here. Answering it for one record
	// means measuring it against every other enabled source, which is a
	// question about the collection and is answered by ListSources. A
	// single-record read that guessed would be worse than one that does not
	// claim to know.
}

func (s *Service) sourceOfAgent(ctx context.Context, workspaceID, agentID, id uuid.UUID) (*domain.Source, error) {
	if _, err := s.repos.Agents.FindByID(ctx, workspaceID, agentID); err != nil {
		return nil, err
	}
	src, err := s.repos.Sources.FindByID(ctx, workspaceID, id)
	if err != nil {
		return nil, err
	}
	if src.AgentID != agentID {
		return nil, domain.NotFound("source")
	}
	return src, nil
}

// sourcesForTurn reads the agent's reference material, reporting a failure
// as a degraded turn rather than a refused one — the same policy memory
// follows, for the same reason: an unreachable auxiliary table must cost
// the turn its context, never its answer.
func (s *Service) sourcesForTurn(ctx context.Context, workspaceID, agentID uuid.UUID) ([]domain.Source, bool) {
	sources, err := s.repos.Sources.ListForContext(ctx, workspaceID, agentID)
	if err != nil {
		s.log.Warn("read agent sources for turn", "agent_id", agentID, "err", err)
		return nil, true
	}
	return sources, false
}
