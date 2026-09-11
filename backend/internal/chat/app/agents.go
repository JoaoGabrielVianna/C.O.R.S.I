package app

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/chat/domain"
)

type CreateAgentInput struct {
	WorkspaceID  uuid.UUID
	ProviderID   uuid.UUID
	Name         string
	Description  string
	SystemPrompt string
	// Model is optional: blank inherits the provider's default_model, so
	// creating an agent needs nothing but a name.
	Model        string
	Temperature  *float32
	MaxTokens    *int
	HistoryLimit *int
	Accent       string
	// Budget is optional at creation. An agent born without limits behaves
	// exactly as agents did before budgets existed.
	Budget domain.Budget
	// MemoryPolicy nil means the caller said nothing about memory, and the
	// agent is born with the documented default. A non-nil policy is used
	// as given and validated as given: naming the field and leaving its
	// mode empty is a malformed request, not a request for the default.
	MemoryPolicy *domain.MemoryPolicy
}

func (s *Service) CreateAgent(ctx context.Context, in CreateAgentInput) (*domain.Agent, error) {
	provider, err := s.repos.Providers.FindByID(ctx, in.WorkspaceID, in.ProviderID)
	if err != nil {
		return nil, err
	}

	model := strings.TrimSpace(in.Model)
	if model == "" {
		model = provider.DefaultModel
	}
	accent := strings.TrimSpace(in.Accent)
	if accent == "" {
		accent = domain.DefaultAccent
	}

	a := &domain.Agent{
		ID:           uuid.New(),
		WorkspaceID:  in.WorkspaceID,
		ProviderID:   in.ProviderID,
		Name:         strings.TrimSpace(in.Name),
		Description:  strings.TrimSpace(in.Description),
		SystemPrompt: in.SystemPrompt,
		Model:        model,
		Temperature:  derefOr(in.Temperature, domain.DefaultTemperature),
		MaxTokens:    derefOr(in.MaxTokens, domain.DefaultMaxTokens),
		HistoryLimit: derefOr(in.HistoryLimit, domain.DefaultHistoryLimit),
		Accent:       accent,
		Budget:       in.Budget,
		MemoryPolicy: policyOrDefault(in.MemoryPolicy),
	}
	if err := a.Validate(); err != nil {
		return nil, err
	}
	if err := s.repos.Agents.Create(ctx, a); err != nil {
		return nil, err
	}
	return a, nil
}

type UpdateAgentInput struct {
	WorkspaceID  uuid.UUID
	ID           uuid.UUID
	ProviderID   *uuid.UUID
	Name         *string
	Description  *string
	SystemPrompt *string
	Model        *string
	Temperature  *float32
	MaxTokens    *int
	HistoryLimit *int
	Accent       *string
	// Budget nil leaves the limits untouched; non-nil replaces both, so a
	// member set to nil inside it removes that limit. "Not mentioned" and
	// "set to no limit" are different requests and stay different here.
	Budget *domain.Budget
	// MemoryPolicy nil leaves the policy exactly as it was. Same rule as
	// every other field: omission is not a reset. A request that mentions
	// the policy replaces it whole, which is why the members inside are
	// values and not a second layer of pointers — there is no partial
	// policy worth expressing between "leave it" and "here it is".
	MemoryPolicy *domain.MemoryPolicy
}

func (s *Service) UpdateAgent(ctx context.Context, in UpdateAgentInput) (*domain.Agent, error) {
	current, err := s.repos.Agents.FindByID(ctx, in.WorkspaceID, in.ID)
	if err != nil {
		return nil, err
	}
	if in.ProviderID != nil {
		// Verified through the repo rather than left to the FK so that
		// pointing an agent at another workspace's provider reads as
		// "not found" instead of a constraint violation.
		if _, err := s.repos.Providers.FindByID(ctx, in.WorkspaceID, *in.ProviderID); err != nil {
			return nil, err
		}
		current.ProviderID = *in.ProviderID
	}
	if in.Name != nil {
		current.Name = strings.TrimSpace(*in.Name)
	}
	if in.Description != nil {
		current.Description = strings.TrimSpace(*in.Description)
	}
	if in.SystemPrompt != nil {
		current.SystemPrompt = *in.SystemPrompt
	}
	if in.Model != nil {
		current.Model = strings.TrimSpace(*in.Model)
	}
	if in.Temperature != nil {
		current.Temperature = *in.Temperature
	}
	if in.MaxTokens != nil {
		current.MaxTokens = *in.MaxTokens
	}
	if in.HistoryLimit != nil {
		current.HistoryLimit = *in.HistoryLimit
	}
	if in.Accent != nil {
		current.Accent = strings.TrimSpace(*in.Accent)
	}
	if in.Budget != nil {
		current.Budget = *in.Budget
	}
	if in.MemoryPolicy != nil {
		current.MemoryPolicy = in.MemoryPolicy.Normalize()
	}
	if err := current.Validate(); err != nil {
		return nil, err
	}
	if err := s.repos.Agents.Update(ctx, current); err != nil {
		return nil, err
	}
	return current, nil
}

// DeleteAgent soft-deletes, refusing while conversations still reference
// it — same reasoning as DeleteProvider: a soft delete does not trip the
// FK, and an orphaned thread would fail only when someone tried to reply
// in it.
func (s *Service) DeleteAgent(ctx context.Context, workspaceID, id uuid.UUID) error {
	if _, err := s.repos.Agents.FindByID(ctx, workspaceID, id); err != nil {
		return err
	}
	n, err := s.repos.Agents.CountConversations(ctx, workspaceID, id)
	if err != nil {
		return err
	}
	if n > 0 {
		return domain.Conflict("this agent still has conversations; delete them first")
	}
	// Memory and Sources are reachable only through their agent. Removing
	// the agent while either points at it would take those rows out of
	// every interface and leave them behind — the orphan the RESTRICT was
	// meant to stop and cannot, because the delete is soft.
	//
	// One rule for every dependency, rather than a different answer per
	// table: refuse, name what is in the way, and let the user decide.
	mem, err := s.repos.Agents.CountMemories(ctx, workspaceID, id)
	if err != nil {
		return err
	}
	if mem > 0 {
		return domain.Conflict("this agent still has memories; delete them first")
	}
	src, err := s.repos.Agents.CountSources(ctx, workspaceID, id)
	if err != nil {
		return err
	}
	if src > 0 {
		return domain.Conflict("this agent still has sources; delete them first")
	}
	// Tool grants are removed rather than blocking the delete, and the
	// asymmetry with memories and sources is deliberate. Those are content
	// the user wrote and would be destroyed silently; a grant is
	// configuration OF this agent, it holds nothing, and it means nothing
	// without the agent it names. Left behind it would be a row no interface
	// can reach — the agent delete is soft, so the RESTRICT that would
	// normally catch an orphan never fires.
	if err := s.repos.AgentTools.RevokeAll(ctx, workspaceID, id); err != nil {
		return err
	}
	return s.repos.Agents.SoftDelete(ctx, workspaceID, id)
}

func (s *Service) GetAgent(ctx context.Context, workspaceID, id uuid.UUID) (*domain.Agent, error) {
	return s.repos.Agents.FindByID(ctx, workspaceID, id)
}

func (s *Service) ListAgents(ctx context.Context, workspaceID uuid.UUID, limit, offset int) ([]domain.Agent, error) {
	return s.repos.Agents.List(ctx, workspaceID, clampList(limit, offset))
}

// policyOrDefault applies the birth default in exactly one place.
func policyOrDefault(p *domain.MemoryPolicy) domain.MemoryPolicy {
	if p == nil {
		return domain.DefaultMemoryPolicy()
	}
	return p.Normalize()
}

func derefOr[T any](p *T, fallback T) T {
	if p == nil {
		return fallback
	}
	return *p
}
