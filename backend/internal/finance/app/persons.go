package app

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/finance/domain"
	"github.com/corsi/backend/internal/finance/ports"
)

type CreatePersonInput struct {
	WorkspaceID uuid.UUID
	Name        string
	Notes       string
}

func (s *Service) CreatePerson(ctx context.Context, in CreatePersonInput) (*domain.Person, error) {
	p := &domain.Person{
		ID:          uuid.New(),
		WorkspaceID: in.WorkspaceID,
		Name:        strings.TrimSpace(in.Name),
		Notes:       in.Notes,
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	if err := s.repos.Persons.Create(ctx, p); err != nil {
		return nil, err
	}
	return p, nil
}

type UpdatePersonInput struct {
	WorkspaceID uuid.UUID
	ID          uuid.UUID
	Name        *string
	Notes       *string
}

func (s *Service) UpdatePerson(ctx context.Context, in UpdatePersonInput) (*domain.Person, error) {
	current, err := s.repos.Persons.FindByID(ctx, in.WorkspaceID, in.ID)
	if err != nil {
		return nil, err
	}
	if in.Name != nil {
		current.Name = strings.TrimSpace(*in.Name)
	}
	if in.Notes != nil {
		current.Notes = *in.Notes
	}
	if err := current.Validate(); err != nil {
		return nil, err
	}
	if err := s.repos.Persons.Update(ctx, current); err != nil {
		return nil, err
	}
	return current, nil
}

func (s *Service) DeletePerson(ctx context.Context, workspaceID, id uuid.UUID) error {
	return s.repos.Persons.SoftDelete(ctx, workspaceID, id)
}

func (s *Service) GetPerson(ctx context.Context, workspaceID, id uuid.UUID) (*domain.Person, error) {
	return s.repos.Persons.FindByID(ctx, workspaceID, id)
}

type ListPersonsInput struct {
	WorkspaceID uuid.UUID
	Limit       int
	Offset      int
}

func (s *Service) ListPersons(ctx context.Context, in ListPersonsInput) ([]domain.Person, error) {
	return s.repos.Persons.List(ctx, in.WorkspaceID, ports.PersonFilter{Limit: in.Limit, Offset: in.Offset})
}
