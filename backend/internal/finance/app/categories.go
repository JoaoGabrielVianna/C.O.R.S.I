package app

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/finance/domain"
	"github.com/corsi/backend/internal/finance/ports"
)

type CreateCategoryInput struct {
	WorkspaceID uuid.UUID
	Name        string
	Type        domain.EntryType
	Color       string
	Icon        string
}

func (s *Service) CreateCategory(ctx context.Context, in CreateCategoryInput) (*domain.Category, error) {
	c := &domain.Category{
		ID:          uuid.New(),
		WorkspaceID: in.WorkspaceID,
		Name:        strings.TrimSpace(in.Name),
		Type:        in.Type,
		Color:       strings.ToLower(in.Color),
		Icon:        strings.TrimSpace(in.Icon),
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	if err := s.repos.Categories.Create(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

type UpdateCategoryInput struct {
	WorkspaceID uuid.UUID
	ID          uuid.UUID
	Name        *string
	Color       *string
	Icon        *string
}

func (s *Service) UpdateCategory(ctx context.Context, in UpdateCategoryInput) (*domain.Category, error) {
	current, err := s.repos.Categories.FindByID(ctx, in.WorkspaceID, in.ID)
	if err != nil {
		return nil, err
	}
	if in.Name != nil {
		current.Name = strings.TrimSpace(*in.Name)
	}
	if in.Color != nil {
		current.Color = strings.ToLower(*in.Color)
	}
	if in.Icon != nil {
		current.Icon = strings.TrimSpace(*in.Icon)
	}
	if err := current.Validate(); err != nil {
		return nil, err
	}
	if err := s.repos.Categories.Update(ctx, current); err != nil {
		return nil, err
	}
	return current, nil
}

func (s *Service) DeleteCategory(ctx context.Context, workspaceID, id uuid.UUID) error {
	return s.repos.Categories.SoftDelete(ctx, workspaceID, id)
}

func (s *Service) GetCategory(ctx context.Context, workspaceID, id uuid.UUID) (*domain.Category, error) {
	return s.repos.Categories.FindByID(ctx, workspaceID, id)
}

type ListCategoriesInput struct {
	WorkspaceID uuid.UUID
	Type        *domain.EntryType
	Limit       int
	Offset      int
}

func (s *Service) ListCategories(ctx context.Context, in ListCategoriesInput) ([]domain.Category, error) {
	return s.repos.Categories.List(ctx, in.WorkspaceID, ports.CategoryFilter{
		Type:   in.Type,
		Limit:  in.Limit,
		Offset: in.Offset,
	})
}
