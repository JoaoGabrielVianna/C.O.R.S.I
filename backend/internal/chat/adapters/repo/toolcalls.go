package repo

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/corsi/backend/internal/chat/domain"
	"github.com/corsi/backend/internal/platform/postgres"
)

// ToolCallRepo is the write-once audit trail of tool execution.
//
// No Update and no Delete. Rows leave only with the message they belong to,
// through the cascade — which is what truncate and regenerate already do to
// the answer these calls produced.
type ToolCallRepo struct {
	pool *pgxpool.Pool
}

func NewToolCallRepo(pool *pgxpool.Pool) *ToolCallRepo {
	return &ToolCallRepo{pool: pool}
}

const toolCallCols = `id, workspace_id, conversation_id, message_id, round,
                      provider_call_id, tool_name, arguments, result, redacted,
                      status, effect, external, effect_type, effect_id,
                      error_code, error_message, duration_ms, created_at`

func scanToolCall(row pgx.Row) (*domain.ToolCallRecord, error) {
	var r domain.ToolCallRecord
	var effectType, effectID *string
	if err := row.Scan(&r.ID, &r.WorkspaceID, &r.ConversationID, &r.MessageID, &r.Round,
		&r.ProviderCallID, &r.ToolName, &r.Arguments, &r.Result, &r.Redacted,
		&r.Status, &r.Effect, &r.External, &effectType, &effectID,
		&r.ErrorCode, &r.ErrorMessage, &r.DurationMS, &r.CreatedAt); err != nil {
		return nil, err
	}
	r.EffectRef = readEffectRef(effectType, effectID)
	return &r, nil
}

// CreateMany writes a turn's calls together.
//
// One statement with a batched VALUES list rather than a loop: they are
// written inside the turn's write-back, which has a deadline of its own, and
// a round trip per call would spend that deadline on latency.
func (r *ToolCallRepo) CreateMany(ctx context.Context, records []domain.ToolCallRecord) error {
	if len(records) == 0 {
		return nil
	}

	const perRow = 17
	args := make([]any, 0, len(records)*perRow)
	q := `INSERT INTO chat.tool_calls
	      (workspace_id, conversation_id, message_id, round, provider_call_id,
	       tool_name, arguments, result, redacted, status, effect, external,
	       effect_type, effect_id,
	       error_code, error_message, duration_ms) VALUES `

	for i, rec := range records {
		if i > 0 {
			q += ", "
		}
		base := i * perRow
		ph := make([]string, perRow)
		for j := range ph {
			ph[j] = fmt.Sprintf("$%d", base+j+1)
		}
		q += "(" + strings.Join(ph, ", ") + ")"
		// Both halves or neither, which is what the CHECK enforces too. A
		// ref that failed validation upstream arrives here as nil and is
		// stored as two nulls: no identity reported, never a partial one.
		var effectType, effectID *string
		if rec.EffectRef != nil && rec.EffectRef.Valid() {
			t, id := rec.EffectRef.Type, rec.EffectRef.ID
			effectType, effectID = &t, &id
		}
		args = append(args,
			rec.WorkspaceID, rec.ConversationID, rec.MessageID, rec.Round,
			rec.ProviderCallID, string(rec.ToolName), rec.Arguments, rec.Result,
			rec.Redacted, string(rec.Status), string(rec.Effect), rec.External,
			effectType, effectID,
			string(rec.ErrorCode), rec.ErrorMessage, rec.DurationMS)
	}

	if _, err := postgres.Conn(ctx, r.pool).Exec(ctx, q, args...); err != nil {
		return mapPgError(err, "this tool call is already recorded",
			"message_id does not reference an existing message")
	}
	return nil
}

// ListByConversation returns a thread's calls in the order they happened.
//
// created_at, then round, then id.
//
// ── Why `round` is in there, and why it was not before ─────────────────
// A turn's calls are written by one CreateMany, in one statement, so they
// all take the same `now()` — the transaction's timestamp, not the moment
// each executor ran. `created_at` therefore does not order calls WITHIN a
// turn, and the id tie-break is a random uuid: stable between two reads of
// the same rows, and unrelated to the sequence they happened in.
//
// That was invisible while a turn made one call. It stops being invisible
// the moment a turn chains them — search, then open the file it found —
// because the transcript would show the read before the search that
// produced it. `round` is the number the loop already records for exactly
// this question, so ordering by it is reading the fact rather than
// inferring it. The id stays last: two calls in the SAME round were asked
// for together and have no order of their own, and something has to make
// the answer repeatable.
func (r *ToolCallRepo) ListByConversation(ctx context.Context, workspaceID, conversationID uuid.UUID, limit int) ([]domain.ToolCallRecord, error) {
	q := `SELECT ` + toolCallCols + ` FROM chat.tool_calls
	      WHERE workspace_id = $1 AND conversation_id = $2
	      ORDER BY created_at, round, id
	      LIMIT $3`
	rows, err := postgres.Conn(ctx, r.pool).Query(ctx, q, workspaceID, conversationID, limit)
	if err != nil {
		return nil, fmt.Errorf("list tool calls: %w", err)
	}
	defer rows.Close()

	return collectToolCalls(rows)
}

// ListByMessages returns the calls filed under the given turns.
//
// Same ordering as ListByConversation, and for the same reason: created_at
// does not separate calls made inside one turn, so `round` is what puts a
// search before the file read it produced.
//
// Both the workspace and the conversation are in the WHERE clause even
// though the message ids alone would already select the right rows. Filtering
// server-side on every scope is what makes cross-workspace and
// cross-conversation isolation a property of the query rather than of the
// caller's diligence — and this query's result goes into a prompt, which is
// the last place to rely on diligence.
func (r *ToolCallRepo) ListByMessages(ctx context.Context, workspaceID, conversationID uuid.UUID, messageIDs []uuid.UUID) ([]domain.ToolCallRecord, error) {
	if len(messageIDs) == 0 {
		return nil, nil
	}
	q := `SELECT ` + toolCallCols + ` FROM chat.tool_calls
	      WHERE workspace_id = $1 AND conversation_id = $2 AND message_id = ANY($3)
	      ORDER BY created_at, round, id`
	rows, err := postgres.Conn(ctx, r.pool).Query(ctx, q, workspaceID, conversationID, messageIDs)
	if err != nil {
		return nil, fmt.Errorf("list tool calls by message: %w", err)
	}
	defer rows.Close()

	return collectToolCalls(rows)
}

func collectToolCalls(rows pgx.Rows) ([]domain.ToolCallRecord, error) {
	out := make([]domain.ToolCallRecord, 0, 8)
	for rows.Next() {
		rec, err := scanToolCall(rows)
		if err != nil {
			return nil, fmt.Errorf("scan tool call: %w", err)
		}
		out = append(out, *rec)
	}
	return out, rows.Err()
}

// WriteReceiptsFor builds the write receipt of each assistant turn named.
//
// ── Why this reads the audit and not the registry ──────────────────────
// Because a receipt is a statement about a turn that already happened, and
// the registry describes the build running now. `effect` is stored beside
// the outcome for exactly this read.
//
// Arguments and results are not selected at all. A Confidential capability
// therefore produces the same receipt as any other, and this can be shown
// to a person, logged, or returned by an API without any of it depending
// on redaction having been done correctly somewhere else.
func (r *ToolCallRepo) WriteReceiptsFor(ctx context.Context, workspaceID uuid.UUID, messageIDs []uuid.UUID) (map[uuid.UUID]domain.WriteReceipt, error) {
	out := make(map[uuid.UUID]domain.WriteReceipt, len(messageIDs))
	// Every message asked about gets a receipt, including an empty one.
	// "Nothing was executed" has to be a statement rather than a missing
	// key, or a surface that renders this would show nothing at all for the
	// turn that fabricated a claim.
	for _, id := range messageIDs {
		out[id] = domain.NewWriteReceipt(id, nil)
	}
	if len(messageIDs) == 0 {
		return out, nil
	}
	rows, err := postgres.Conn(ctx, r.pool).Query(ctx, `
		SELECT id, message_id, tool_name, status, effect, effect_type, effect_id,
		       error_code, duration_ms, created_at
		  FROM chat.tool_calls
		 WHERE workspace_id = $1 AND message_id = ANY($2) AND effect = 'write'
		 ORDER BY message_id, round, id`, workspaceID, messageIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	grouped := map[uuid.UUID][]domain.ToolCallRecord{}
	for rows.Next() {
		var rec domain.ToolCallRecord
		var effectType, effectID *string
		if err := rows.Scan(&rec.ID, &rec.MessageID, &rec.ToolName, &rec.Status,
			&rec.Effect, &effectType, &effectID,
			&rec.ErrorCode, &rec.DurationMS, &rec.CreatedAt); err != nil {
			return nil, err
		}
		rec.EffectRef = readEffectRef(effectType, effectID)
		grouped[rec.MessageID] = append(grouped[rec.MessageID], rec)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for id, recs := range grouped {
		out[id] = domain.NewWriteReceipt(id, recs)
	}
	return out, nil
}

// ReadReceiptsFor is the read-side twin of WriteReceiptsFor.
//
// ── Why it reads the audit and not the registry ────────────────────────
// Same reason. A receipt is a statement about a turn that already
// happened; the registry describes the build running now. `external` is
// stored beside the outcome for exactly this read.
//
// Arguments and results are not selected. A Confidential capability
// therefore produces the same receipt as any other, and none of this
// depends on redaction having been done correctly somewhere else — which
// matters more here than for writes, because an external read's payload is
// somebody's metrics.
//
// `available` is not a column and is not asked for here: it is a
// presentation gate about the CURRENT configuration, decided a layer up.
// See domain.ReadReceipt.Available.
func (r *ToolCallRepo) ReadReceiptsFor(ctx context.Context, workspaceID uuid.UUID, messageIDs []uuid.UUID, available bool) (map[uuid.UUID]domain.ReadReceipt, error) {
	out := make(map[uuid.UUID]domain.ReadReceipt, len(messageIDs))
	// Every message asked about gets a receipt, including an empty one.
	// NO_EXTERNAL_READ has to be a statement rather than a missing key, or
	// a surface would show nothing at all for the turn that invented a
	// follower count.
	for _, id := range messageIDs {
		out[id] = domain.NewReadReceipt(id, available, nil)
	}
	if len(messageIDs) == 0 {
		return out, nil
	}
	rows, err := postgres.Conn(ctx, r.pool).Query(ctx, `
		SELECT id, message_id, tool_name, status, external, error_code, duration_ms, created_at
		  FROM chat.tool_calls
		 WHERE workspace_id = $1 AND message_id = ANY($2) AND external
		 ORDER BY message_id, round, id`, workspaceID, messageIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	grouped := map[uuid.UUID][]domain.ToolCallRecord{}
	for rows.Next() {
		var rec domain.ToolCallRecord
		if err := rows.Scan(&rec.ID, &rec.MessageID, &rec.ToolName, &rec.Status,
			&rec.External, &rec.ErrorCode, &rec.DurationMS, &rec.CreatedAt); err != nil {
			return nil, err
		}
		grouped[rec.MessageID] = append(grouped[rec.MessageID], rec)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for id, recs := range grouped {
		out[id] = domain.NewReadReceipt(id, available, recs)
	}
	return out, nil
}

// readEffectRef rebuilds a ref from its two columns.
//
// Anything that does not satisfy the domain contract comes back nil. The
// database already refuses a malformed one — `effect_id` is a uuid and a
// CHECK bounds the type — so this is the second lock on a door that should
// not open: an identifier that cannot be trusted must read as absent, never
// as approximate, because a resume follows it.
func readEffectRef(effectType, effectID *string) *domain.EffectRef {
	if effectType == nil || effectID == nil {
		return nil
	}
	ref := domain.EffectRef{Type: *effectType, ID: *effectID}
	if !ref.Valid() {
		return nil
	}
	return &ref
}
