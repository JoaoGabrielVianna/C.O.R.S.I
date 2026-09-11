package app

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/finance/domain"
	"github.com/corsi/backend/internal/finance/ports"
)

type CreateCardInput struct {
	WorkspaceID uuid.UUID
	Name        string
	Institution string
	Network     domain.CardNetwork
	Variant     string
	Last4       string
	LimitCents  int64
	ClosingDay  int
	DueDay      int
}

func (s *Service) CreateCard(ctx context.Context, in CreateCardInput) (*domain.Card, error) {
	c := &domain.Card{
		ID:          uuid.New(),
		WorkspaceID: in.WorkspaceID,
		Name:        strings.TrimSpace(in.Name),
		Institution: strings.TrimSpace(in.Institution),
		Network:     in.Network,
		Variant:     strings.TrimSpace(in.Variant),
		Last4:       in.Last4,
		LimitCents:  in.LimitCents,
		ClosingDay:  in.ClosingDay,
		DueDay:      in.DueDay,
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	if err := s.repos.Cards.Create(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

type UpdateCardInput struct {
	WorkspaceID uuid.UUID
	ID          uuid.UUID
	Name        *string
	Institution *string
	Network     *domain.CardNetwork
	Variant     *string
	Last4       *string
	LimitCents  *int64
	ClosingDay  *int
	DueDay      *int
}

func (s *Service) UpdateCard(ctx context.Context, in UpdateCardInput) (*domain.Card, error) {
	current, err := s.repos.Cards.FindByID(ctx, in.WorkspaceID, in.ID)
	if err != nil {
		return nil, err
	}
	if in.Name != nil {
		current.Name = strings.TrimSpace(*in.Name)
	}
	if in.Institution != nil {
		current.Institution = strings.TrimSpace(*in.Institution)
	}
	if in.Network != nil {
		current.Network = *in.Network
	}
	if in.Variant != nil {
		current.Variant = strings.TrimSpace(*in.Variant)
	}
	if in.Last4 != nil {
		current.Last4 = *in.Last4
	}
	if in.LimitCents != nil {
		current.LimitCents = *in.LimitCents
	}
	if in.ClosingDay != nil {
		current.ClosingDay = *in.ClosingDay
	}
	if in.DueDay != nil {
		current.DueDay = *in.DueDay
	}
	if err := current.Validate(); err != nil {
		return nil, err
	}
	if err := s.repos.Cards.Update(ctx, current); err != nil {
		return nil, err
	}
	return current, nil
}

// ArchiveCard performs a soft delete; v0.1 does not expose hard delete.
func (s *Service) ArchiveCard(ctx context.Context, workspaceID, id uuid.UUID) error {
	return s.repos.Cards.Archive(ctx, workspaceID, id)
}

func (s *Service) GetCard(ctx context.Context, workspaceID, id uuid.UUID) (*domain.Card, error) {
	return s.repos.Cards.FindByID(ctx, workspaceID, id)
}

type ListCardsInput struct {
	WorkspaceID uuid.UUID
	Limit       int
	Offset      int
}

func (s *Service) ListCards(ctx context.Context, in ListCardsInput) ([]domain.Card, error) {
	return s.repos.Cards.List(ctx, in.WorkspaceID, ports.CardFilter{Limit: in.Limit, Offset: in.Offset})
}
