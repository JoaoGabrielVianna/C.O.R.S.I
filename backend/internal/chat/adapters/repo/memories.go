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

type MemoryRepo struct {
	pool *pgxpool.Pool
}

func NewMemoryRepo(pool *pgxpool.Pool) *MemoryRepo {
	return &MemoryRepo{pool: pool}
}

const memoryCols = `id, workspace_id, agent_id, content, origin, source_conversation_id,
                    source_message_seq, enabled, pinned, model_proposed,
                    created_at, updated_at, deleted_at`

const memoryBadAgent = "agent_id does not reference an existing agent"

// selectionOrder is the priority a memory is consumed in: pinned first, then
// most recently updated. It is written once and used by both reads so the
// Memory page lists rows in the order the budget will actually spend them —
// a list that disagrees with the selection makes the "3 of 8 used" counter a
// riddle instead of an explanation.
const selectionOrder = ` ORDER BY pinned DESC, updated_at DESC, id`

func scanMemory(row pgx.Row) (*domain.Memory, error) {
	var m domain.Memory
	if err := row.Scan(&m.ID, &m.WorkspaceID, &m.AgentID, &m.Content, &m.Origin,
		&m.SourceConversationID, &m.SourceMessageSeq, &m.Enabled, &m.Pinned, &m.ModelProposed,
		&m.CreatedAt, &m.UpdatedAt, &m.DeletedAt); err != nil {
		return nil, err
	}
	return &m, nil
}

func (r *MemoryRepo) Create(ctx context.Context, m *domain.Memory) error {
	q := `INSERT INTO chat.agent_memories
	      (id, workspace_id, agent_id, content, origin, source_conversation_id,
	       source_message_seq, enabled, pinned, model_proposed)
	      VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	      RETURNING ` + memoryCols
	row := postgres.Conn(ctx, r.pool).QueryRow(ctx, q,
		m.ID, m.WorkspaceID, m.AgentID, m.Content, m.Origin, m.SourceConversationID,
		m.SourceMessageSeq, m.Enabled, m.Pinned, m.ModelProposed)
	got, err := scanMemory(row)
	if err != nil {
		return mapPgError(err, "this memory already exists", memoryBadAgent)
	}
	*m = *got
	return nil
}

// Update rewrites the mutable half of a memory. Origin and provenance are
// absent on purpose: they record what happened, and editing the text of a
// memory does not change where it came from.
func (r *MemoryRepo) Update(ctx context.Context, m *domain.Memory) error {
	q := `UPDATE chat.agent_memories
	      SET content = $3, enabled = $4, pinned = $5, updated_at = now()
	      WHERE id = $1 AND workspace_id = $2 AND deleted_at IS NULL
	      RETURNING ` + memoryCols
	row := postgres.Conn(ctx, r.pool).QueryRow(ctx, q,
		m.ID, m.WorkspaceID, m.Content, m.Enabled, m.Pinned)
	got, err := scanMemory(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.NotFound("memory")
		}
		return mapPgError(err, "this memory already exists", memoryBadAgent)
	}
	*m = *got
	return nil
}

func (r *MemoryRepo) SoftDelete(ctx context.Context, workspaceID, id uuid.UUID) error {
	q := `UPDATE chat.agent_memories SET deleted_at = now()
	      WHERE id = $1 AND workspace_id = $2 AND deleted_at IS NULL`
	tag, err := postgres.Conn(ctx, r.pool).Exec(ctx, q, id, workspaceID)
	if err != nil {
		return fmt.Errorf("delete memory: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.NotFound("memory")
	}
	return nil
}

func (r *MemoryRepo) FindByID(ctx context.Context, workspaceID, id uuid.UUID) (*domain.Memory, error) {
	q := `SELECT ` + memoryCols + ` FROM chat.agent_memories
	      WHERE id = $1 AND workspace_id = $2 AND deleted_at IS NULL`
	m, err := scanMemory(postgres.Conn(ctx, r.pool).QueryRow(ctx, q, id, workspaceID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.NotFound("memory")
		}
		return nil, fmt.Errorf("find memory: %w", err)
	}
	return m, nil
}

// ListByAgent is the management read: every live memory of one agent, with
// the origin resolved.
//
// The join is LEFT and carries `c.deleted_at IS NULL` in its ON clause, not
// in the WHERE. That is the whole provenance rule in one line: a memory whose
// conversation was deleted still comes back, with a null title, and the
// interface reads that as "the conversation is no longer available". Moving
// the predicate into the WHERE would silently drop those memories from the
// page — which is precisely the outcome the design forbids.
func (r *MemoryRepo) ListByAgent(ctx context.Context, workspaceID, agentID uuid.UUID, limit int) ([]domain.Memory, error) {
	q := `SELECT m.id, m.workspace_id, m.agent_id, m.content, m.origin,
	             m.source_conversation_id, m.source_message_seq, m.enabled, m.pinned,
	             m.model_proposed, m.created_at, m.updated_at, m.deleted_at, c.title
	      FROM chat.agent_memories m
	      LEFT JOIN chat.conversations c
	             ON c.id = m.source_conversation_id
	            AND c.workspace_id = m.workspace_id
	            AND c.deleted_at IS NULL
	      WHERE m.workspace_id = $1 AND m.agent_id = $2 AND m.deleted_at IS NULL
	      ORDER BY m.pinned DESC, m.updated_at DESC, m.id
	      LIMIT $3`
	rows, err := postgres.Conn(ctx, r.pool).Query(ctx, q, workspaceID, agentID, limit)
	if err != nil {
		return nil, fmt.Errorf("list memories: %w", err)
	}
	defer rows.Close()

	memories := make([]domain.Memory, 0)
	for rows.Next() {
		var m domain.Memory
		if err := rows.Scan(&m.ID, &m.WorkspaceID, &m.AgentID, &m.Content, &m.Origin,
			&m.SourceConversationID, &m.SourceMessageSeq, &m.Enabled, &m.Pinned,
			&m.ModelProposed, &m.CreatedAt, &m.UpdatedAt, &m.DeletedAt,
			&m.SourceConversationTitle); err != nil {
			return nil, fmt.Errorf("scan memory: %w", err)
		}
		memories = append(memories, m)
	}
	return memories, rows.Err()
}

func (r *MemoryRepo) CountByAgent(ctx context.Context, workspaceID, agentID uuid.UUID) (int64, error) {
	q := `SELECT count(*) FROM chat.agent_memories
	      WHERE workspace_id = $1 AND agent_id = $2 AND deleted_at IS NULL`
	var n int64
	if err := postgres.Conn(ctx, r.pool).QueryRow(ctx, q, workspaceID, agentID).Scan(&n); err != nil {
		return 0, fmt.Errorf("count memories: %w", err)
	}
	return n, nil
}

// ListForContext is the turn's read: only what can reach the model, in the
// order the budget spends it. No join — the model is never told where a
// memory came from, so paying for provenance on every turn would buy nothing.
func (r *MemoryRepo) ListForContext(ctx context.Context, workspaceID, agentID uuid.UUID) ([]domain.Memory, error) {
	q := `SELECT ` + memoryCols + ` FROM chat.agent_memories
	      WHERE workspace_id = $1 AND agent_id = $2 AND deleted_at IS NULL AND enabled` +
		selectionOrder
	rows, err := postgres.Conn(ctx, r.pool).Query(ctx, q, workspaceID, agentID)
	if err != nil {
		return nil, fmt.Errorf("list memories for context: %w", err)
	}
	defer rows.Close()

	memories := make([]domain.Memory, 0)
	for rows.Next() {
		m, err := scanMemory(rows)
		if err != nil {
			return nil, fmt.Errorf("scan memory: %w", err)
		}
		memories = append(memories, *m)
	}
	return memories, rows.Err()
}
