package app

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/chat/domain"
	"github.com/corsi/backend/internal/chat/ports"
	"github.com/corsi/backend/internal/platform/secrets"
)

type CreateProviderInput struct {
	WorkspaceID  uuid.UUID
	Name         string
	BaseURL      string
	APIKey       string
	DefaultModel string
}

func (s *Service) CreateProvider(ctx context.Context, in CreateProviderInput) (*domain.Provider, error) {
	if err := s.requireSealer(); err != nil {
		return nil, err
	}
	key := strings.TrimSpace(in.APIKey)
	if key == "" {
		return nil, domain.Invalid("api_key required")
	}

	cipher, err := s.sealer.Seal(key)
	if err != nil {
		return nil, err
	}

	p := &domain.Provider{
		ID:           uuid.New(),
		WorkspaceID:  in.WorkspaceID,
		Name:         strings.TrimSpace(in.Name),
		BaseURL:      domain.NormalizeBaseURL(in.BaseURL),
		APIKeyCipher: cipher,
		APIKeyHint:   secrets.Hint(key),
		DefaultModel: strings.TrimSpace(in.DefaultModel),
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	if err := s.repos.Providers.Create(ctx, p); err != nil {
		return nil, err
	}
	return p, nil
}

type UpdateProviderInput struct {
	WorkspaceID uuid.UUID
	ID          uuid.UUID
	Name        *string
	BaseURL     *string
	// APIKey is nil when the caller is editing other fields and leaving the
	// stored credential alone — the common case, since the UI can never
	// display the current key to echo back.
	APIKey       *string
	DefaultModel *string
}

func (s *Service) UpdateProvider(ctx context.Context, in UpdateProviderInput) (*domain.Provider, error) {
	current, err := s.repos.Providers.FindByID(ctx, in.WorkspaceID, in.ID)
	if err != nil {
		return nil, err
	}
	if in.Name != nil {
		current.Name = strings.TrimSpace(*in.Name)
	}
	if in.BaseURL != nil {
		current.BaseURL = domain.NormalizeBaseURL(*in.BaseURL)
	}
	if in.DefaultModel != nil {
		current.DefaultModel = strings.TrimSpace(*in.DefaultModel)
	}
	if in.APIKey != nil {
		if err := s.requireSealer(); err != nil {
			return nil, err
		}
		key := strings.TrimSpace(*in.APIKey)
		if key == "" {
			return nil, domain.Invalid("api_key cannot be set to an empty string; omit the field to keep the current key")
		}
		cipher, err := s.sealer.Seal(key)
		if err != nil {
			return nil, err
		}
		current.APIKeyCipher = cipher
		current.APIKeyHint = secrets.Hint(key)
	}
	if err := current.Validate(); err != nil {
		return nil, err
	}
	if err := s.repos.Providers.Update(ctx, current); err != nil {
		return nil, err
	}
	return current, nil
}

// DeleteProvider soft-deletes, refusing while agents still point at it.
//
// The FK is ON DELETE RESTRICT, but a soft delete is an UPDATE and would
// sail straight past it, leaving agents aimed at a credential that reads
// as missing. Checking here turns that into a 409 the UI can explain.
func (s *Service) DeleteProvider(ctx context.Context, workspaceID, id uuid.UUID) error {
	if _, err := s.repos.Providers.FindByID(ctx, workspaceID, id); err != nil {
		return err
	}
	n, err := s.repos.Providers.CountAgents(ctx, workspaceID, id)
	if err != nil {
		return err
	}
	if n > 0 {
		return domain.Conflict("this provider is still used by agents; delete or repoint them first")
	}
	return s.repos.Providers.SoftDelete(ctx, workspaceID, id)
}

func (s *Service) GetProvider(ctx context.Context, workspaceID, id uuid.UUID) (*domain.Provider, error) {
	return s.repos.Providers.FindByID(ctx, workspaceID, id)
}

func (s *Service) ListProviders(ctx context.Context, workspaceID uuid.UUID, limit, offset int) ([]domain.Provider, error) {
	return s.repos.Providers.List(ctx, workspaceID, clampList(limit, offset))
}

// ListProviderModels asks the endpoint what it serves. It doubles as the
// connection test: it is the cheapest call that exercises the base URL and
// the key together, so a green result here means a send will reach someone.
func (s *Service) ListProviderModels(ctx context.Context, workspaceID, id uuid.UUID) ([]ports.Model, error) {
	p, err := s.repos.Providers.FindByID(ctx, workspaceID, id)
	if err != nil {
		return nil, err
	}
	creds, err := s.credentialsFor(p)
	if err != nil {
		return nil, err
	}
	return s.llm.Models(ctx, creds)
}
