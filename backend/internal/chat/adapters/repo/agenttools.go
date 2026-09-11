package repo

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/corsi/backend/internal/chat/domain"
	"github.com/corsi/backend/internal/platform/postgres"
)

// AgentToolRepo stores which tools an agent was granted.
//
// Every statement is scoped by workspace_id as well as agent_id, even the
// ones where the agent id alone would identify the row. That is not
// belt-and-braces: an agent id arriving from a URL must be unable to select
// or modify anything in another workspace, and the way to guarantee that is
// for the predicate to be in the query rather than in a check somebody
// remembered to write first.
type AgentToolRepo struct {
	pool *pgxpool.Pool
}

func NewAgentToolRepo(pool *pgxpool.Pool) *AgentToolRepo {
	return &AgentToolRepo{pool: pool}
}

// Authorize grants a tool, idempotently.
//
// ON CONFLICT DO NOTHING rather than read-then-insert: the unique
// constraint is the arbiter, so two concurrent grants of the same tool
// converge on one row instead of one of them failing.
func (r *AgentToolRepo) Authorize(ctx context.Context, workspaceID, agentID uuid.UUID, name domain.ToolName) error {
	q := `INSERT INTO chat.agent_tools (workspace_id, agent_id, tool_name)
	      VALUES ($1, $2, $3)
	      ON CONFLICT (agent_id, tool_name) DO NOTHING`
	_, err := postgres.Conn(ctx, r.pool).Exec(ctx, q, workspaceID, agentID, string(name))
	if err != nil {
		return mapPgError(err, "this tool is already authorized",
			"agent_id does not reference an existing agent")
	}
	return nil
}

// Revoke removes a grant. Removing one that does not exist is success: the
// caller asked for a state, and the state is what they get.
func (r *AgentToolRepo) Revoke(ctx context.Context, workspaceID, agentID uuid.UUID, name domain.ToolName) error {
	q := `DELETE FROM chat.agent_tools
	      WHERE workspace_id = $1 AND agent_id = $2 AND tool_name = $3`
	if _, err := postgres.Conn(ctx, r.pool).Exec(ctx, q, workspaceID, agentID, string(name)); err != nil {
		return fmt.Errorf("revoke agent tool: %w", err)
	}
	return nil
}

// ListByAgent returns the granted names in name order.
//
// Ordered in SQL rather than in Go because the order reaches the model: the
// declaration array is built from this slice, and two identical turns must
// produce an identical request body. An unordered read would make that
// depend on the physical order of rows.
func (r *AgentToolRepo) ListByAgent(ctx context.Context, workspaceID, agentID uuid.UUID) ([]domain.ToolName, error) {
	q := `SELECT tool_name FROM chat.agent_tools
	      WHERE workspace_id = $1 AND agent_id = $2
	      ORDER BY tool_name`
	rows, err := postgres.Conn(ctx, r.pool).Query(ctx, q, workspaceID, agentID)
	if err != nil {
		return nil, fmt.Errorf("list agent tools: %w", err)
	}
	defer rows.Close()

	out := make([]domain.ToolName, 0, 8)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scan agent tool: %w", err)
		}
		out = append(out, domain.ToolName(name))
	}
	return out, rows.Err()
}

func (r *AgentToolRepo) CountByAgent(ctx context.Context, workspaceID, agentID uuid.UUID) (int64, error) {
	q := `SELECT count(*) FROM chat.agent_tools
	      WHERE workspace_id = $1 AND agent_id = $2`
	var n int64
	if err := postgres.Conn(ctx, r.pool).QueryRow(ctx, q, workspaceID, agentID).Scan(&n); err != nil {
		return 0, fmt.Errorf("count agent tools: %w", err)
	}
	return n, nil
}

// RevokeAll drops every grant of one agent.
//
// Called when the agent is removed. Unlike memories and sources, an
// authorization does not block the deletion and is not preserved: it is not
// content the user authored, it is configuration *of* the agent, and it
// means nothing without it. Leaving the rows behind would be dead data
// pointing at something no interface can reach — and the agent delete is
// soft, so the RESTRICT that would normally catch it never fires.
func (r *AgentToolRepo) RevokeAll(ctx context.Context, workspaceID, agentID uuid.UUID) error {
	q := `DELETE FROM chat.agent_tools WHERE workspace_id = $1 AND agent_id = $2`
	if _, err := postgres.Conn(ctx, r.pool).Exec(ctx, q, workspaceID, agentID); err != nil {
		return fmt.Errorf("revoke all agent tools: %w", err)
	}
	return nil
}
