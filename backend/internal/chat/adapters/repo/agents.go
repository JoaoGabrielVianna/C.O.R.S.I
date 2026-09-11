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

type AgentRepo struct {
	pool *pgxpool.Pool
}

func NewAgentRepo(pool *pgxpool.Pool) *AgentRepo {
	return &AgentRepo{pool: pool}
}

const agentCols = `id, workspace_id, provider_id, name, description, system_prompt, model,
                   temperature, max_tokens, history_limit, accent,
                   daily_token_limit, daily_cost_limit_usd,
                   memory_policy_mode, memory_policy_notes,
                   created_at, updated_at, deleted_at`

const (
	agentNameTaken   = "an agent with this name already exists in the workspace"
	agentBadProvider = "provider_id does not reference an existing provider"
)

func scanAgent(row pgx.Row) (*domain.Agent, error) {
	var a domain.Agent
	if err := row.Scan(&a.ID, &a.WorkspaceID, &a.ProviderID, &a.Name, &a.Description, &a.SystemPrompt,
		&a.Model, &a.Temperature, &a.MaxTokens, &a.HistoryLimit, &a.Accent,
		&a.Budget.DailyTokenLimit, &a.Budget.DailyCostLimitUSD,
		&a.MemoryPolicy.Mode, &a.MemoryPolicy.Notes,
		&a.CreatedAt, &a.UpdatedAt, &a.DeletedAt); err != nil {
		return nil, err
	}
	return &a, nil
}

func (r *AgentRepo) Create(ctx context.Context, a *domain.Agent) error {
	q := `INSERT INTO chat.agents
	      (id, workspace_id, provider_id, name, description, system_prompt, model,
	       temperature, max_tokens, history_limit, accent,
	       daily_token_limit, daily_cost_limit_usd,
	       memory_policy_mode, memory_policy_notes)
	      VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
	      RETURNING ` + agentCols
	row := postgres.Conn(ctx, r.pool).QueryRow(ctx, q,
		a.ID, a.WorkspaceID, a.ProviderID, a.Name, a.Description, a.SystemPrompt, a.Model,
		a.Temperature, a.MaxTokens, a.HistoryLimit, a.Accent,
		a.Budget.DailyTokenLimit, a.Budget.DailyCostLimitUSD,
		a.MemoryPolicy.Mode, a.MemoryPolicy.Notes)
	got, err := scanAgent(row)
	if err != nil {
		return mapPgError(err, agentNameTaken, agentBadProvider)
	}
	*a = *got
	return nil
}

func (r *AgentRepo) Update(ctx context.Context, a *domain.Agent) error {
	q := `UPDATE chat.agents
	      SET provider_id = $3, name = $4, description = $5, system_prompt = $6, model = $7,
	          temperature = $8, max_tokens = $9, history_limit = $10, accent = $11,
	          daily_token_limit = $12, daily_cost_limit_usd = $13,
	          memory_policy_mode = $14, memory_policy_notes = $15, updated_at = now()
	      WHERE id = $1 AND workspace_id = $2 AND deleted_at IS NULL
	      RETURNING ` + agentCols
	row := postgres.Conn(ctx, r.pool).QueryRow(ctx, q,
		a.ID, a.WorkspaceID, a.ProviderID, a.Name, a.Description, a.SystemPrompt, a.Model,
		a.Temperature, a.MaxTokens, a.HistoryLimit, a.Accent,
		a.Budget.DailyTokenLimit, a.Budget.DailyCostLimitUSD,
		a.MemoryPolicy.Mode, a.MemoryPolicy.Notes)
	got, err := scanAgent(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.NotFound("agent")
		}
		return mapPgError(err, agentNameTaken, agentBadProvider)
	}
	*a = *got
	return nil
}

func (r *AgentRepo) SoftDelete(ctx context.Context, workspaceID, id uuid.UUID) error {
	q := `UPDATE chat.agents SET deleted_at = now()
	      WHERE id = $1 AND workspace_id = $2 AND deleted_at IS NULL`
	tag, err := postgres.Conn(ctx, r.pool).Exec(ctx, q, id, workspaceID)
	if err != nil {
		return fmt.Errorf("delete agent: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.NotFound("agent")
	}
	return nil
}

func (r *AgentRepo) FindByID(ctx context.Context, workspaceID, id uuid.UUID) (*domain.Agent, error) {
	q := `SELECT ` + agentCols + ` FROM chat.agents
	      WHERE id = $1 AND workspace_id = $2 AND deleted_at IS NULL`
	a, err := scanAgent(postgres.Conn(ctx, r.pool).QueryRow(ctx, q, id, workspaceID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.NotFound("agent")
		}
		return nil, fmt.Errorf("find agent: %w", err)
	}
	return a, nil
}

func (r *AgentRepo) List(ctx context.Context, workspaceID uuid.UUID, f ports.ListFilter) ([]domain.Agent, error) {
	q := `SELECT ` + agentCols + ` FROM chat.agents
	      WHERE workspace_id = $1 AND deleted_at IS NULL
	      ORDER BY created_at ASC
	      LIMIT $2 OFFSET $3`
	rows, err := postgres.Conn(ctx, r.pool).Query(ctx, q, workspaceID, f.Limit, f.Offset)
	if err != nil {
		return nil, fmt.Errorf("list agents: %w", err)
	}
	defer rows.Close()

	agents := make([]domain.Agent, 0)
	for rows.Next() {
		a, err := scanAgent(rows)
		if err != nil {
			return nil, fmt.Errorf("scan agent: %w", err)
		}
		agents = append(agents, *a)
	}
	return agents, rows.Err()
}

// CountConversations reports how many live threads belong to this agent, so
// a refused delete can say what is in the way.
func (r *AgentRepo) CountConversations(ctx context.Context, workspaceID, id uuid.UUID) (int, error) {
	q := `SELECT count(*) FROM chat.conversations
	      WHERE agent_id = $1 AND workspace_id = $2 AND deleted_at IS NULL`
	var n int
	if err := postgres.Conn(ctx, r.pool).QueryRow(ctx, q, id, workspaceID).Scan(&n); err != nil {
		return 0, fmt.Errorf("count conversations for agent: %w", err)
	}
	return n, nil
}

// CountMemories does the same for what the agent remembers. Memory is
// reachable only through its agent, so a soft-deleted agent would take its
// memories out of every interface while leaving the rows behind.
func (r *AgentRepo) CountMemories(ctx context.Context, workspaceID, id uuid.UUID) (int, error) {
	return r.countDependents(ctx, "chat.agent_memories", workspaceID, id)
}

// CountSources is the same guard for reference material, for the same
// reason. One rule for every dependency of an agent.
func (r *AgentRepo) CountSources(ctx context.Context, workspaceID, id uuid.UUID) (int, error) {
	return r.countDependents(ctx, "chat.agent_sources", workspaceID, id)
}

// countDependents is the shared body of the two guards above. `table` is a
// package constant at every call site, never anything that came in from a
// request — this is not a place to start passing identifiers around.
func (r *AgentRepo) countDependents(ctx context.Context, table string, workspaceID, id uuid.UUID) (int, error) {
	q := `SELECT count(*) FROM ` + table + `
	      WHERE agent_id = $1 AND workspace_id = $2 AND deleted_at IS NULL`
	var n int
	if err := postgres.Conn(ctx, r.pool).QueryRow(ctx, q, id, workspaceID).Scan(&n); err != nil {
		return 0, fmt.Errorf("count %s for agent: %w", table, err)
	}
	return n, nil
}
