package repo

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/corsi/backend/internal/chat/domain"
	"github.com/corsi/backend/internal/chat/ports"
	"github.com/corsi/backend/internal/platform/postgres"
)

type ConversationRepo struct {
	pool *pgxpool.Pool
}

func NewConversationRepo(pool *pgxpool.Pool) *ConversationRepo {
	return &ConversationRepo{pool: pool}
}

const conversationCols = `id, workspace_id, agent_id, title, last_message_at,
                          context_references, created_at, updated_at, deleted_at`

func scanConversation(row pgx.Row) (*domain.Conversation, error) {
	var c domain.Conversation
	if err := row.Scan(&c.ID, &c.WorkspaceID, &c.AgentID, &c.Title, &c.LastMessageAt,
		&c.ContextReferences, &c.CreatedAt, &c.UpdatedAt, &c.DeletedAt); err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *ConversationRepo) Create(ctx context.Context, c *domain.Conversation) error {
	q := `INSERT INTO chat.conversations (id, workspace_id, agent_id, title, context_references)
	      VALUES ($1, $2, $3, $4, $5)
	      RETURNING ` + conversationCols
	row := postgres.Conn(ctx, r.pool).QueryRow(ctx, q, c.ID, c.WorkspaceID, c.AgentID, c.Title,
		// NULL rather than `[]` when a thread was opened from nothing in
		// particular — the same distinction nullableReferences protects.
		nullableContextReferences(c.ContextReferences))
	got, err := scanConversation(row)
	if err != nil {
		return mapPgError(err, "conversation already exists", "agent_id does not reference an existing agent")
	}
	*c = *got
	return nil
}

func (r *ConversationRepo) Rename(ctx context.Context, workspaceID, id uuid.UUID, title string) (*domain.Conversation, error) {
	q := `UPDATE chat.conversations SET title = $3, updated_at = now()
	      WHERE id = $1 AND workspace_id = $2 AND deleted_at IS NULL
	      RETURNING ` + conversationCols
	c, err := scanConversation(postgres.Conn(ctx, r.pool).QueryRow(ctx, q, id, workspaceID, title))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.NotFound("conversation")
		}
		return nil, fmt.Errorf("rename conversation: %w", err)
	}
	return c, nil
}

func (r *ConversationRepo) SoftDelete(ctx context.Context, workspaceID, id uuid.UUID) error {
	q := `UPDATE chat.conversations SET deleted_at = now()
	      WHERE id = $1 AND workspace_id = $2 AND deleted_at IS NULL`
	tag, err := postgres.Conn(ctx, r.pool).Exec(ctx, q, id, workspaceID)
	if err != nil {
		return fmt.Errorf("delete conversation: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.NotFound("conversation")
	}
	return nil
}

func (r *ConversationRepo) FindByID(ctx context.Context, workspaceID, id uuid.UUID) (*domain.Conversation, error) {
	q := `SELECT ` + conversationCols + ` FROM chat.conversations
	      WHERE id = $1 AND workspace_id = $2 AND deleted_at IS NULL`
	c, err := scanConversation(postgres.Conn(ctx, r.pool).QueryRow(ctx, q, id, workspaceID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.NotFound("conversation")
		}
		return nil, fmt.Errorf("find conversation: %w", err)
	}
	return c, nil
}

// conversationScope builds the predicate shared by List and Count, so the
// page and the total can never disagree about what they are looking at.
//
// workspace_id is first, unconditional, and not derived from anything the
// caller sent. An agent id arriving from a URL is appended to that scope,
// never substituted for it: it narrows rows the workspace already owns.
func conversationScope(workspaceID uuid.UUID, f ports.ConversationFilter) (string, []any) {
	where := `workspace_id = $1 AND deleted_at IS NULL`
	args := []any{workspaceID}
	if f.AgentID != nil {
		args = append(args, *f.AgentID)
		where += ` AND agent_id = $` + strconv.Itoa(len(args))
	}
	return where, args
}

// List orders by activity, newest first — the sidebar ordering. The
// coalesce mirrors the partial index laid down in 0004_conversations.
func (r *ConversationRepo) List(ctx context.Context, workspaceID uuid.UUID, f ports.ConversationFilter) ([]domain.Conversation, error) {
	where, args := conversationScope(workspaceID, f)
	args = append(args, f.Limit, f.Offset)
	q := `SELECT ` + conversationCols + ` FROM chat.conversations
	      WHERE ` + where + `
	      ORDER BY coalesce(last_message_at, created_at) DESC
	      LIMIT $` + strconv.Itoa(len(args)-1) + ` OFFSET $` + strconv.Itoa(len(args))
	rows, err := postgres.Conn(ctx, r.pool).Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list conversations: %w", err)
	}
	defer rows.Close()

	conversations := make([]domain.Conversation, 0)
	for rows.Next() {
		c, err := scanConversation(rows)
		if err != nil {
			return nil, fmt.Errorf("scan conversation: %w", err)
		}
		conversations = append(conversations, *c)
	}
	return conversations, rows.Err()
}

// Count answers how many rows the same scope holds, disregarding the page
// bounds. Deliberately a second query rather than a window function on the
// page: `count(*) OVER ()` returns nothing at all when the page is empty,
// which is precisely the case a caller asks the total about.
func (r *ConversationRepo) Count(ctx context.Context, workspaceID uuid.UUID, f ports.ConversationFilter) (int64, error) {
	where, args := conversationScope(workspaceID, f)
	q := `SELECT count(*) FROM chat.conversations WHERE ` + where
	var n int64
	if err := postgres.Conn(ctx, r.pool).QueryRow(ctx, q, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("count conversations: %w", err)
	}
	return n, nil
}

func (r *ConversationRepo) TouchActivity(ctx context.Context, workspaceID, id uuid.UUID) error {
	q := `UPDATE chat.conversations SET last_message_at = now(), updated_at = now()
	      WHERE id = $1 AND workspace_id = $2 AND deleted_at IS NULL`
	if _, err := postgres.Conn(ctx, r.pool).Exec(ctx, q, id, workspaceID); err != nil {
		return fmt.Errorf("touch conversation: %w", err)
	}
	return nil
}
