package repo

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/corsi/backend/internal/chat/domain"
	"github.com/corsi/backend/internal/chat/ports"
	"github.com/corsi/backend/internal/platform/postgres"
)

type ProviderRepo struct {
	pool *pgxpool.Pool
}

func NewProviderRepo(pool *pgxpool.Pool) *ProviderRepo {
	return &ProviderRepo{pool: pool}
}

const providerCols = `id, workspace_id, name, base_url, api_key_cipher, api_key_hint,
                      default_model, created_at, updated_at, deleted_at`

const providerNameTaken = "a provider with this name already exists in the workspace"

func scanProvider(row pgx.Row) (*domain.Provider, error) {
	var p domain.Provider
	if err := row.Scan(&p.ID, &p.WorkspaceID, &p.Name, &p.BaseURL, &p.APIKeyCipher, &p.APIKeyHint,
		&p.DefaultModel, &p.CreatedAt, &p.UpdatedAt, &p.DeletedAt); err != nil {
		return nil, err
	}
	return &p, nil
}

func (r *ProviderRepo) Create(ctx context.Context, p *domain.Provider) error {
	q := `INSERT INTO chat.providers
	      (id, workspace_id, name, base_url, api_key_cipher, api_key_hint, default_model)
	      VALUES ($1, $2, $3, $4, $5, $6, $7)
	      RETURNING ` + providerCols
	row := postgres.Conn(ctx, r.pool).QueryRow(ctx, q,
		p.ID, p.WorkspaceID, p.Name, p.BaseURL, p.APIKeyCipher, p.APIKeyHint, p.DefaultModel)
	got, err := scanProvider(row)
	if err != nil {
		return mapPgError(err, providerNameTaken, "invalid provider reference")
	}
	*p = *got
	return nil
}

func (r *ProviderRepo) Update(ctx context.Context, p *domain.Provider) error {
	q := `UPDATE chat.providers
	      SET name = $3, base_url = $4, api_key_cipher = $5, api_key_hint = $6,
	          default_model = $7, updated_at = now()
	      WHERE id = $1 AND workspace_id = $2 AND deleted_at IS NULL
	      RETURNING ` + providerCols
	row := postgres.Conn(ctx, r.pool).QueryRow(ctx, q,
		p.ID, p.WorkspaceID, p.Name, p.BaseURL, p.APIKeyCipher, p.APIKeyHint, p.DefaultModel)
	got, err := scanProvider(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.NotFound("provider")
		}
		return mapPgError(err, providerNameTaken, "invalid provider reference")
	}
	*p = *got
	return nil
}

// SoftDelete marks the provider deleted. Agents reference providers with
// ON DELETE RESTRICT, but a soft delete does not trip that constraint —
// so the application layer refuses the delete while agents still point
// here. See app.DeleteProvider.
func (r *ProviderRepo) SoftDelete(ctx context.Context, workspaceID, id uuid.UUID) error {
	q := `UPDATE chat.providers SET deleted_at = now()
	      WHERE id = $1 AND workspace_id = $2 AND deleted_at IS NULL`
	tag, err := postgres.Conn(ctx, r.pool).Exec(ctx, q, id, workspaceID)
	if err != nil {
		return fmt.Errorf("delete provider: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.NotFound("provider")
	}
	return nil
}

func (r *ProviderRepo) FindByID(ctx context.Context, workspaceID, id uuid.UUID) (*domain.Provider, error) {
	q := `SELECT ` + providerCols + ` FROM chat.providers
	      WHERE id = $1 AND workspace_id = $2 AND deleted_at IS NULL`
	p, err := scanProvider(postgres.Conn(ctx, r.pool).QueryRow(ctx, q, id, workspaceID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.NotFound("provider")
		}
		return nil, fmt.Errorf("find provider: %w", err)
	}
	return p, nil
}

func (r *ProviderRepo) List(ctx context.Context, workspaceID uuid.UUID, f ports.ListFilter) ([]domain.Provider, error) {
	q := `SELECT ` + providerCols + ` FROM chat.providers
	      WHERE workspace_id = $1 AND deleted_at IS NULL
	      ORDER BY created_at ASC
	      LIMIT $2 OFFSET $3`
	rows, err := postgres.Conn(ctx, r.pool).Query(ctx, q, workspaceID, f.Limit, f.Offset)
	if err != nil {
		return nil, fmt.Errorf("list providers: %w", err)
	}
	defer rows.Close()

	providers := make([]domain.Provider, 0)
	for rows.Next() {
		p, err := scanProvider(rows)
		if err != nil {
			return nil, fmt.Errorf("scan provider: %w", err)
		}
		providers = append(providers, *p)
	}
	return providers, rows.Err()
}

// CountAgents reports how many live agents depend on this provider. Used to
// give a refused delete an actionable message instead of a bare conflict.
func (r *ProviderRepo) CountAgents(ctx context.Context, workspaceID, id uuid.UUID) (int, error) {
	q := `SELECT count(*) FROM chat.agents
	      WHERE provider_id = $1 AND workspace_id = $2 AND deleted_at IS NULL`
	var n int
	if err := postgres.Conn(ctx, r.pool).QueryRow(ctx, q, id, workspaceID).Scan(&n); err != nil {
		return 0, fmt.Errorf("count agents for provider: %w", err)
	}
	return n, nil
}
