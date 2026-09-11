package repo

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/corsi/backend/internal/chat/domain"
	"github.com/corsi/backend/internal/platform/postgres"
)

type SourceRepo struct {
	pool *pgxpool.Pool
}

func NewSourceRepo(pool *pgxpool.Pool) *SourceRepo {
	return &SourceRepo{pool: pool}
}

const sourceCols = `id, workspace_id, agent_id, title, description, content, enabled,
                    created_at, updated_at, deleted_at`

const sourceBadAgent = "agent_id does not reference an existing agent"

// sourceOrder is the priority a source is consumed in: most recently
// updated first, with the id breaking ties.
//
// The tie-break is not decoration. Two sources saved in the same
// transaction share a timestamp, and without a second key Postgres is free
// to return them in either order — which would make the block that reaches
// the model, and therefore the answer, vary between two identical turns.
// Predictability is the whole selection policy of v1; a non-deterministic
// order would quietly undo it.
const sourceOrder = ` ORDER BY updated_at DESC, id`

func scanSource(row pgx.Row) (*domain.Source, error) {
	var s domain.Source
	if err := row.Scan(&s.ID, &s.WorkspaceID, &s.AgentID, &s.Title, &s.Description,
		&s.Content, &s.Enabled, &s.CreatedAt, &s.UpdatedAt, &s.DeletedAt); err != nil {
		return nil, err
	}
	s.Characters = len([]rune(s.Content))
	return &s, nil
}

func (r *SourceRepo) Create(ctx context.Context, s *domain.Source) error {
	q := `INSERT INTO chat.agent_sources
	      (id, workspace_id, agent_id, title, description, content, enabled)
	      VALUES ($1, $2, $3, $4, $5, $6, $7)
	      RETURNING ` + sourceCols
	row := postgres.Conn(ctx, r.pool).QueryRow(ctx, q,
		s.ID, s.WorkspaceID, s.AgentID, s.Title, s.Description, s.Content, s.Enabled)
	got, err := scanSource(row)
	if err != nil {
		return mapPgError(err, "this source already exists", sourceBadAgent)
	}
	*s = *got
	return nil
}

func (r *SourceRepo) Update(ctx context.Context, s *domain.Source) error {
	q := `UPDATE chat.agent_sources
	      SET title = $3, description = $4, content = $5, enabled = $6, updated_at = now()
	      WHERE id = $1 AND workspace_id = $2 AND deleted_at IS NULL
	      RETURNING ` + sourceCols
	row := postgres.Conn(ctx, r.pool).QueryRow(ctx, q,
		s.ID, s.WorkspaceID, s.Title, s.Description, s.Content, s.Enabled)
	got, err := scanSource(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.NotFound("source")
		}
		return mapPgError(err, "this source already exists", sourceBadAgent)
	}
	*s = *got
	return nil
}

func (r *SourceRepo) SoftDelete(ctx context.Context, workspaceID, id uuid.UUID) error {
	q := `UPDATE chat.agent_sources SET deleted_at = now()
	      WHERE id = $1 AND workspace_id = $2 AND deleted_at IS NULL`
	tag, err := postgres.Conn(ctx, r.pool).Exec(ctx, q, id, workspaceID)
	if err != nil {
		return fmt.Errorf("delete source: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.NotFound("source")
	}
	return nil
}

func (r *SourceRepo) FindByID(ctx context.Context, workspaceID, id uuid.UUID) (*domain.Source, error) {
	q := `SELECT ` + sourceCols + ` FROM chat.agent_sources
	      WHERE id = $1 AND workspace_id = $2 AND deleted_at IS NULL`
	s, err := scanSource(postgres.Conn(ctx, r.pool).QueryRow(ctx, q, id, workspaceID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.NotFound("source")
		}
		return nil, fmt.Errorf("find source: %w", err)
	}
	return s, nil
}

// ListByAgent is the management read, and it deliberately leaves Content
// behind.
//
// A source is up to twenty thousand characters; two hundred of them is four
// megabytes of text nobody on that page is going to read. What the page
// needs is the title, the size and the state — so the size comes back as
// `length(content)`, computed in the database, and the text stays there
// until the editor asks for one record by id.
//
// The count is `length()`, which Postgres measures in characters, matching
// the rune count the budget spends. Returning `octet_length` here would
// overstate every accented source.
func (r *SourceRepo) ListByAgent(ctx context.Context, workspaceID, agentID uuid.UUID, limit int) ([]domain.Source, error) {
	q := `SELECT id, workspace_id, agent_id, title, description, enabled,
	             length(content), created_at, updated_at, deleted_at
	      FROM chat.agent_sources
	      WHERE workspace_id = $1 AND agent_id = $2 AND deleted_at IS NULL` +
		sourceOrder + ` LIMIT $3`
	rows, err := postgres.Conn(ctx, r.pool).Query(ctx, q, workspaceID, agentID, limit)
	if err != nil {
		return nil, fmt.Errorf("list sources: %w", err)
	}
	defer rows.Close()

	sources := make([]domain.Source, 0)
	for rows.Next() {
		var s domain.Source
		if err := rows.Scan(&s.ID, &s.WorkspaceID, &s.AgentID, &s.Title, &s.Description,
			&s.Enabled, &s.Characters, &s.CreatedAt, &s.UpdatedAt, &s.DeletedAt); err != nil {
			return nil, fmt.Errorf("scan source: %w", err)
		}
		sources = append(sources, s)
	}
	return sources, rows.Err()
}

func (r *SourceRepo) CountByAgent(ctx context.Context, workspaceID, agentID uuid.UUID) (int64, error) {
	q := `SELECT count(*) FROM chat.agent_sources
	      WHERE workspace_id = $1 AND agent_id = $2 AND deleted_at IS NULL`
	var n int64
	if err := postgres.Conn(ctx, r.pool).QueryRow(ctx, q, workspaceID, agentID).Scan(&n); err != nil {
		return 0, fmt.Errorf("count sources: %w", err)
	}
	return n, nil
}

// ListForContext is the turn's read: only what can reach the model, with
// the text, in the order the budget spends it.
func (r *SourceRepo) ListForContext(ctx context.Context, workspaceID, agentID uuid.UUID) ([]domain.Source, error) {
	q := `SELECT ` + sourceCols + ` FROM chat.agent_sources
	      WHERE workspace_id = $1 AND agent_id = $2 AND deleted_at IS NULL AND enabled` +
		sourceOrder
	rows, err := postgres.Conn(ctx, r.pool).Query(ctx, q, workspaceID, agentID)
	if err != nil {
		return nil, fmt.Errorf("list sources for context: %w", err)
	}
	defer rows.Close()

	sources := make([]domain.Source, 0)
	for rows.Next() {
		s, err := scanSource(rows)
		if err != nil {
			return nil, fmt.Errorf("scan source: %w", err)
		}
		sources = append(sources, *s)
	}
	return sources, rows.Err()
}
