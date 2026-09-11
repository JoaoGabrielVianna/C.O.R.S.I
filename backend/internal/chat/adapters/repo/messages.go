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

type MessageRepo struct {
	pool *pgxpool.Pool
}

func NewMessageRepo(pool *pgxpool.Pool) *MessageRepo {
	return &MessageRepo{pool: pool}
}

// conversationRows is the predicate that separates the conversation from
// the receipts kept in the same table.
//
// Written once and appended by every read that means "the thread": the
// transcript, the window replayed to the model, the lookup that backs
// truncation, and the truncation itself. The usage aggregations below
// deliberately do NOT carry it — they count what was consumed, and a
// receipt is consumed spend. See migration 0014.
//
// It is a constant rather than four copies of the same clause because the
// day someone adds a fifth conversation read, the thing that has to be
// noticed is that this exists.
const conversationRows = ` AND kind = 'turn'`

const messageCols = `id, workspace_id, conversation_id, role, kind, content, reasoning, reasoning_ms,
                     model, prompt_tokens, completion_tokens, usage_source,
                     input_cost_per_token, output_cost_per_token, cost, estimated_prompt_tokens,
                     cache_read_tokens, cache_creation_tokens, reasoning_tokens,
                     context_report, turn_references, context_references, finish_reason, error, created_at, seq`

func scanMessage(row pgx.Row) (*domain.Message, error) {
	var m domain.Message
	if err := row.Scan(&m.ID, &m.WorkspaceID, &m.ConversationID, &m.Role, &m.Kind, &m.Content,
		&m.Reasoning, &m.ReasoningMS, &m.Model,
		&m.PromptTokens, &m.CompletionTokens, &m.UsageSource,
		&m.InputCostPerToken, &m.OutputCostPerToken, &m.Cost, &m.EstimatedPromptTokens,
		&m.CacheReadTokens, &m.CacheCreationTokens, &m.ReasoningTokens,
		&m.ContextReport, &m.References, &m.ContextReferences,
		&m.FinishReason, &m.Error, &m.CreatedAt, &m.Seq); err != nil {
		return nil, err
	}
	return &m, nil
}

func (r *MessageRepo) Create(ctx context.Context, m *domain.Message) error {
	q := `INSERT INTO chat.messages
	      (id, workspace_id, conversation_id, role, kind, content, reasoning, reasoning_ms,
	       model, prompt_tokens, completion_tokens, usage_source,
	       input_cost_per_token, output_cost_per_token, cost, estimated_prompt_tokens,
	       cache_read_tokens, cache_creation_tokens, reasoning_tokens,
	       context_report, turn_references, context_references, finish_reason, error)
	      VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24)
	      RETURNING ` + messageCols
	row := postgres.Conn(ctx, r.pool).QueryRow(ctx, q,
		m.ID, m.WorkspaceID, m.ConversationID, m.Role, m.Kind.OrTurn(), m.Content, m.Reasoning, m.ReasoningMS,
		m.Model, m.PromptTokens, m.CompletionTokens, m.UsageSource.OrUnknown(),
		m.InputCostPerToken, m.OutputCostPerToken, m.Cost, m.EstimatedPromptTokens,
		// nil goes in as NULL: the provider said nothing, which is not the
		// same as saying zero. See migration 0019.
		m.CacheReadTokens, m.CacheCreationTokens, m.ReasoningTokens,
		// nil goes in as SQL NULL, not as `[]`. The two would read back the
		// same in Go, but only one of them is an honest row: a turn without a
		// selection made none, and a stored empty array would claim it made
		// an empty one.
		m.ContextReport, nullableReferences(m.References),
		nullableContextReferences(m.ContextReferences), m.FinishReason, m.Error)
	got, err := scanMessage(row)
	if err != nil {
		return mapPgError(err, "message already exists", "conversation_id does not reference an existing conversation")
	}
	*m = *got
	return nil
}

// nullableReferences keeps "no selection" out of the database as NULL.
//
// pgx would happily encode an empty slice as `[]`, and Go would read it
// back as an empty slice again — so nothing would break, and the column
// would quietly stop being able to tell "this turn made no selection" from
// "this turn selected nothing", on every row ever written. They are the
// same behaviour and a different fact, and only one of them ever happens.
func nullableReferences(refs []domain.TurnReference) any {
	if len(refs) == 0 {
		return nil
	}
	return refs
}

// nullableContextReferences is the same rule for the entities a turn was
// about. Same reasoning, different column: NULL means nothing was
// attached, and an empty array would claim an empty attachment was made.
func nullableContextReferences(refs []domain.ContextReference) any {
	if len(refs) == 0 {
		return nil
	}
	return refs
}

// ListRecent returns the trailing `limit` messages in chronological order.
//
// The inner query takes the newest rows (seq DESC, which the index serves
// directly); the outer one flips them back into reading order. Selecting
// ASC with an offset would mean counting the whole thread first.
func (r *MessageRepo) ListRecent(ctx context.Context, workspaceID, conversationID uuid.UUID, limit int) ([]domain.Message, error) {
	q := `SELECT ` + messageCols + ` FROM (
	          SELECT ` + messageCols + ` FROM chat.messages
	          WHERE conversation_id = $1 AND workspace_id = $2` + conversationRows + `
	          ORDER BY seq DESC
	          LIMIT $3
	      ) recent
	      ORDER BY seq ASC`
	rows, err := postgres.Conn(ctx, r.pool).Query(ctx, q, conversationID, workspaceID, limit)
	if err != nil {
		return nil, fmt.Errorf("list messages: %w", err)
	}
	defer rows.Close()

	messages := make([]domain.Message, 0, limit)
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		messages = append(messages, *m)
	}
	return messages, rows.Err()
}

// ListUpToSeq returns the trailing `limit` turns of a conversation at or
// before `upToSeq`, in reading order.
//
// ── Why this is not ListRecent with an argument ────────────────────────
// They answer different questions. ListRecent answers "what is the end of
// this thread", which is what a transcript and a turn's replay window both
// want. This answers "what did this thread contain at the moment somebody
// asked", which is what consolidation wants — and the difference is the
// entire safety property of that operation: a message written after the
// request must not be able to enter it.
//
// The ceiling is applied in SQL rather than by filtering afterwards, so a
// row that arrives between the read and the filter cannot slip in.
func (r *MessageRepo) ListUpToSeq(ctx context.Context, workspaceID, conversationID uuid.UUID, upToSeq int64, limit int) ([]domain.Message, error) {
	q := `SELECT ` + messageCols + ` FROM (
	          SELECT ` + messageCols + ` FROM chat.messages
	          WHERE conversation_id = $1 AND workspace_id = $2 AND seq <= $3` + conversationRows + `
	          ORDER BY seq DESC
	          LIMIT $4
	      ) snapshot
	      ORDER BY seq ASC`
	rows, err := postgres.Conn(ctx, r.pool).Query(ctx, q, conversationID, workspaceID, upToSeq, limit)
	if err != nil {
		return nil, fmt.Errorf("list messages up to seq: %w", err)
	}
	defer rows.Close()

	messages := make([]domain.Message, 0, limit)
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		messages = append(messages, *m)
	}
	return messages, rows.Err()
}

// DeleteFromSeq removes a message and everything after it in the thread.
//
// This backs regenerate and edit: both replace a turn rather than append a
// second copy of the same question. A hard delete is right here — the rows
// are being replaced, not archived, and leaving them soft-deleted would
// keep them in the history the model replays.
//
// Receipts in the range SURVIVE, and that is the point of the predicate
// rather than an accident of sharing it. Regenerating an answer replaces
// turns; it does not un-spend what an unrelated auxiliary operation cost
// earlier in the same thread. Deleting those rows would quietly reduce the
// day's accounted spend, which is the one direction accounting must never
// move on its own.
func (r *MessageRepo) DeleteFromSeq(ctx context.Context, workspaceID, conversationID uuid.UUID, seq int64) (int64, error) {
	q := `DELETE FROM chat.messages
	      WHERE conversation_id = $1 AND workspace_id = $2 AND seq >= $3` + conversationRows
	tag, err := postgres.Conn(ctx, r.pool).Exec(ctx, q, conversationID, workspaceID, seq)
	if err != nil {
		return 0, fmt.Errorf("delete messages from seq: %w", err)
	}
	return tag.RowsAffected(), nil
}

// FindBySeq resolves one message by its ordering key, so callers can check
// what they are about to truncate before doing it.
//
// A receipt is not found by this, and answers NotFound like any seq that
// names nothing. It is the honest reply: the caller is asking which turn
// sits at that position, and a receipt is not a turn — reporting it as one
// would let an interface offer to regenerate an accounting record.
func (r *MessageRepo) FindBySeq(ctx context.Context, workspaceID, conversationID uuid.UUID, seq int64) (*domain.Message, error) {
	q := `SELECT ` + messageCols + ` FROM chat.messages
	      WHERE conversation_id = $1 AND workspace_id = $2 AND seq = $3` + conversationRows
	m, err := scanMessage(postgres.Conn(ctx, r.pool).QueryRow(ctx, q, conversationID, workspaceID, seq))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.NotFound("message")
		}
		return nil, fmt.Errorf("find message by seq: %w", err)
	}
	return m, nil
}

// Usage aggregation.
//
// Only assistant turns carry usage (the user turn is recorded before the
// model runs), so every tally filters to role = 'assistant' and groups by
// the model stamped on the row — which is the model that produced it, not
// whatever its agent is configured with now.
//
// These queries do NOT filter `kind`, and the omission is the design. An
// auxiliary receipt is an assistant-side record of tokens that were really
// bought, so it belongs in every total and in the daily gate that reads
// them. Adding `kind = 'turn'` here would create spend the budget cannot
// see, which is the exact failure migration 0014 exists to prevent.
//
// Cost is SUMmed from the figure each turn froze. This query must never
// join a price list: the moment it does, a rate change rewrites history.
const usageSelect = `SELECT COALESCE(m.model, ''),
                            COALESCE(SUM(m.prompt_tokens), 0),
                            COALESCE(SUM(m.completion_tokens), 0),
                            COUNT(*),
                            COUNT(*) FILTER (WHERE m.usage_source = 'provider'),
                            COUNT(*) FILTER (WHERE m.usage_source = 'estimated'),
                            COUNT(*) FILTER (WHERE m.usage_source = 'unknown'),
                            -- SUM skips NULLs, so an unpriced turn adds
                            -- nothing rather than adding a fictional zero.
                            -- The count beside it says how much is missing.
                            COALESCE(SUM(m.cost), 0),
                            COUNT(*) FILTER (WHERE m.cost IS NULL),
                            -- A unit rate is only reportable for the group
                            -- when the group agrees on it. COUNT DISTINCT
                            -- ignores NULLs, so unpriced turns do not make
                            -- a consistent rate look inconsistent.
                            CASE WHEN COUNT(DISTINCT m.input_cost_per_token) = 1
                                 THEN MIN(m.input_cost_per_token) END,
                            CASE WHEN COUNT(DISTINCT m.output_cost_per_token) = 1
                                 THEN MIN(m.output_cost_per_token) END,
                            -- SUM skips NULLs here for the same reason it
                            -- does for cost: a turn the gateway never
                            -- measured must not contribute a fictional zero
                            -- to a cache figure. The count beside them says
                            -- how many turns the sums actually cover, which
                            -- is what makes a hit rate honest instead of an
                            -- average over unknowns.
                            COALESCE(SUM(m.cache_read_tokens), 0),
                            COALESCE(SUM(m.cache_creation_tokens), 0),
                            COALESCE(SUM(m.reasoning_tokens), 0),
                            COUNT(*) FILTER (WHERE m.cache_read_tokens IS NOT NULL
                                                OR m.cache_creation_tokens IS NOT NULL)`

func scanUsageRows(rows pgx.Rows) ([]ports.ModelUsage, error) {
	defer rows.Close()
	out := make([]ports.ModelUsage, 0, 4)
	for rows.Next() {
		var u ports.ModelUsage
		if err := rows.Scan(&u.Model, &u.PromptTokens, &u.CompletionTokens, &u.Messages,
			&u.ProviderMessages, &u.EstimatedMessages, &u.UnknownMessages,
			&u.Cost, &u.UnpricedMessages,
			&u.InputCostPerToken, &u.OutputCostPerToken,
			&u.CacheReadTokens, &u.CacheCreationTokens, &u.ReasoningTokens,
			&u.CacheMeasuredMessages); err != nil {
			return nil, fmt.Errorf("scan usage: %w", err)
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// window appends the time bounds to a usage query, if it has any.
//
// The comparison is on m.created_at — the moment the turn happened. Half
// open, [from, to), so a day boundary belongs to exactly one day. Bounds
// are optional and independent; neither one is the lifetime total.
func window(q string, args []any, f ports.UsageFilter) (string, []any) {
	if f.From != nil {
		args = append(args, *f.From)
		q += fmt.Sprintf(" AND m.created_at >= $%d", len(args))
	}
	if f.To != nil {
		args = append(args, *f.To)
		q += fmt.Sprintf(" AND m.created_at < $%d", len(args))
	}
	return q, args
}

func (r *MessageRepo) UsageByConversation(ctx context.Context, workspaceID, conversationID uuid.UUID, f ports.UsageFilter) ([]ports.ModelUsage, error) {
	q, args := window(usageSelect+` FROM chat.messages m
	      WHERE m.conversation_id = $1 AND m.workspace_id = $2 AND m.role = 'assistant'`,
		[]any{conversationID, workspaceID}, f)
	rows, err := postgres.Conn(ctx, r.pool).Query(ctx, q+" GROUP BY m.model", args...)
	if err != nil {
		return nil, fmt.Errorf("usage by conversation: %w", err)
	}
	return scanUsageRows(rows)
}

func (r *MessageRepo) UsageByAgent(ctx context.Context, workspaceID, agentID uuid.UUID, f ports.UsageFilter) ([]ports.ModelUsage, error) {
	q, args := window(usageSelect+` FROM chat.messages m
	      JOIN chat.conversations c ON c.id = m.conversation_id
	      WHERE c.agent_id = $1 AND m.workspace_id = $2 AND m.role = 'assistant'`,
		[]any{agentID, workspaceID}, f)
	rows, err := postgres.Conn(ctx, r.pool).Query(ctx, q+" GROUP BY m.model", args...)
	if err != nil {
		return nil, fmt.Errorf("usage by agent: %w", err)
	}
	return scanUsageRows(rows)
}

// UsageByWorkspace is the level a daily total is asked at, and the reason
// messages_workspace_created_idx exists: without it this is a scan of the
// whole table on every read.
//
// It reads chat.messages directly rather than joining conversations —
// workspace_id is stamped on the message itself, and a turn whose thread
// was soft-deleted still cost what it cost.
func (r *MessageRepo) UsageByWorkspace(ctx context.Context, workspaceID uuid.UUID, f ports.UsageFilter) ([]ports.ModelUsage, error) {
	q, args := window(usageSelect+` FROM chat.messages m
	      WHERE m.workspace_id = $1 AND m.role = 'assistant'`,
		[]any{workspaceID}, f)
	rows, err := postgres.Conn(ctx, r.pool).Query(ctx, q+" GROUP BY m.model", args...)
	if err != nil {
		return nil, fmt.Errorf("usage by workspace: %w", err)
	}
	return scanUsageRows(rows)
}
