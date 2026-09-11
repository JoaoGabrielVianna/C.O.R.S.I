package domain

import (
	"time"

	"github.com/google/uuid"
)

// maxContentBytes bounds a single user turn. The HTTP layer already caps
// the whole body at 1 MiB; this is the domain saying what a *message* is,
// independent of transport.
const maxContentBytes = 100_000

// MessageKind says whether a row is part of the conversation or an
// accounting receipt for something that merely cost money.
//
// ── Why this distinction lives on the message and not elsewhere ────────
// Because consumption is derived from this table and must stay that way.
// Every usage aggregation reads assistant rows here, and the daily budget
// sums the same rows; an operation that spent tokens without writing one
// would be invisible to both. So a billable operation that is not a turn
// writes a row that is not a message, and this is what tells them apart.
//
// The rule every reader follows, and it is short:
//
//	conversation semantics  →  KindTurn only
//	accounting semantics    →  both kinds
type MessageKind string

const (
	// KindTurn is part of the exchange: it is shown in the transcript, it
	// is replayed to the model, and truncating a thread removes it.
	KindTurn MessageKind = "turn"
	// KindAuxiliary is a receipt. It records what a billable operation
	// consumed and nothing else — see the CHECK in migration 0014, which
	// forbids it from carrying any content at all.
	KindAuxiliary MessageKind = "auxiliary"
)

func (k MessageKind) Valid() bool { return k == KindTurn || k == KindAuxiliary }

// OrTurn resolves the zero value.
//
// A Message built without mentioning kind is a turn, which keeps every
// existing construction site correct without being edited and matches what
// the column's default does to every row written before it existed.
func (k MessageKind) OrTurn() MessageKind {
	if k == "" {
		return KindTurn
	}
	return k
}

func (k MessageKind) String() string { return string(k) }

type Message struct {
	ID             uuid.UUID `json:"id"`
	WorkspaceID    uuid.UUID `json:"workspace_id"`
	ConversationID uuid.UUID `json:"conversation_id"`
	Role           Role      `json:"role"`
	// Kind separates a turn from an accounting receipt. Empty means turn;
	// see MessageKind.OrTurn.
	Kind    MessageKind `json:"kind,omitempty"`
	Content string      `json:"content"`
	// Reasoning is the model's chain of thought, when it emitted one. Kept
	// apart from Content because it is displayed differently and is never
	// replayed to the provider on a later turn.
	Reasoning   string `json:"reasoning,omitempty"`
	ReasoningMS int    `json:"reasoning_ms,omitempty"`
	// Model is the model that actually produced this turn, stamped when the
	// turn was written. An agent's model can be changed afterwards; this
	// column is what keeps yesterday's answer from claiming to have come
	// from today's configuration.
	Model            string `json:"model,omitempty"`
	PromptTokens     int    `json:"prompt_tokens"`
	CompletionTokens int    `json:"completion_tokens"`
	// UsageSource qualifies the two counts above. Read it before them:
	// "unknown" means they are placeholders, not measurements.
	UsageSource UsageSource `json:"usage_source,omitempty"`
	// The rate card applied to this turn, and the resulting cost, frozen at
	// the moment it happened. Nil means unknown — never zero, which would
	// read as free. Historical cost is answered from here and never by
	// consulting the current price list.
	InputCostPerToken  *float64 `json:"input_cost_per_token"`
	OutputCostPerToken *float64 `json:"output_cost_per_token"`
	Cost               *float64 `json:"cost"`
	// EstimatedPromptTokens is what the Context Builder predicted the input
	// would be, before the call. Kept beside PromptTokens so the heuristic
	// can be checked against the provider's real number.
	EstimatedPromptTokens *int `json:"estimated_prompt_tokens,omitempty"`
	// ── What the input was made of, when the provider said ─────────────
	//
	// Nil is ABSENT, never zero. A gateway that reports no cache counts and
	// a turn that read nothing from cache are different facts, and a cache
	// hit rate computed over turns that were never measured would be a
	// confident average of unknowns. Read UsageSource first, then these:
	// on an estimated or unknown turn they are always nil, because nothing
	// local can estimate them.
	//
	// These three are columns rather than report fields because they are
	// what a cost question aggregates over. The full per-call detail,
	// including the counts that only diagnose the gateway, lives in
	// ContextReport.Rounds[].Usage.
	CacheReadTokens     *int `json:"cache_read_tokens,omitempty"`
	CacheCreationTokens *int `json:"cache_creation_tokens,omitempty"`
	// ReasoningTokens is part of the OUTPUT bill, not the input one. It is
	// already inside CompletionTokens; this says how much of it was thought
	// rather than answer.
	ReasoningTokens *int `json:"reasoning_tokens,omitempty"`
	// ContextReport is the account of what this turn actually carried,
	// stamped when it happened.
	//
	// Nil means no report was recorded — every user turn, and every
	// assistant turn written before the column existed. That is different
	// from a report saying nothing was sent, and readers must not collapse
	// the two.
	//
	// It is a snapshot on purpose. Nothing in it points at a memory, a
	// source or an agent setting, so there is nothing to "resolve" against
	// today's data and no way for a past turn to start describing the
	// present. See domain/context_report.go.
	ContextReport *ContextReport `json:"context_report,omitempty"`
	// References is the capability selection the user made for THIS turn,
	// frozen as it read when they made it.
	//
	// It lives on the user turn because that is whose act it was, and
	// because the user turn is written before anything can fail — a turn
	// whose provider call died still keeps an honest record of what was
	// attached to it.
	//
	// Nil means no explicit selection was made, which is every turn ever
	// sent before this column existed and every turn sent without one since.
	// It is NOT "nothing was available": absence is the legacy behaviour, in
	// which every authorized tool is exposed. See domain/reference.go.
	References []TurnReference `json:"references,omitempty"`
	// ContextReferences is what this turn was ABOUT: the entities the user
	// attached to it.
	//
	// A different fact from References, and next to it on purpose so the
	// difference is impossible to miss while reading: that field holds
	// CAPABILITIES and narrows what the turn may do; this one holds
	// ENTITIES and changes no capability whatsoever. See
	// domain/context_reference.go.
	//
	// Like References it lives on the user turn, because attaching is the
	// user's act. Nil means nothing was attached.
	ContextReferences []ContextReference `json:"context_references,omitempty"`
	FinishReason      string             `json:"finish_reason,omitempty"`
	Error             string             `json:"error,omitempty"`
	CreatedAt         time.Time          `json:"created_at"`
	Seq               int64              `json:"seq"`
}

func (m *Message) Validate() error {
	if m.WorkspaceID == uuid.Nil {
		return Invalid("workspace_id required")
	}
	if m.ConversationID == uuid.Nil {
		return Invalid("conversation_id required")
	}
	if !m.Role.Valid() {
		return Invalid("role must be user or assistant")
	}
	if !m.Kind.OrTurn().Valid() {
		return Invalid("kind must be turn or auxiliary")
	}
	// A receipt is an assistant-side record with no words. Both halves are
	// load-bearing, and both are also enforced by a CHECK in migration
	// 0014: assistant, because usage counts assistant rows and a receipt
	// that was not one would be invisible to the budget; empty, because
	// that is what makes a forgotten `kind = 'turn'` filter cosmetic
	// instead of a leak of auxiliary text into a conversation.
	if m.Kind.OrTurn() == KindAuxiliary {
		if m.Role != RoleAssistant {
			return Invalid("an auxiliary message must have role assistant")
		}
		if m.Content != "" || m.Reasoning != "" {
			return Invalid("an auxiliary message carries no content")
		}
	}
	// An assistant turn may legitimately be empty — a stream that was
	// aborted before the first token still gets persisted, carrying its
	// finish_reason. Only the user is required to actually say something.
	if m.Role == RoleUser && m.Content == "" {
		return Invalid("content required")
	}
	if len(m.Content) > maxContentBytes {
		return Invalid("content is too long")
	}
	if m.UsageSource != "" && !m.UsageSource.Valid() {
		return Invalid("usage_source is not one of unknown, provider, estimated")
	}
	// A selection is something a person made, so it can only hang off the
	// turn they wrote. An assistant row carrying one would be the model
	// appearing to have chosen its own capabilities, which is the single
	// claim this feature must never be able to make.
	if len(m.References) > 0 && m.Role != RoleUser {
		return Invalid("only a user turn may carry references")
	}
	// The same rule, for the same reason: attaching a subject is a person's
	// act. An assistant row carrying one would be the model appearing to
	// have decided what the conversation was about.
	if len(m.ContextReferences) > 0 && m.Role != RoleUser {
		return Invalid("only a user turn may carry context references")
	}
	if err := ValidateContextReferences(m.ContextReferences); err != nil {
		return err
	}
	if err := ValidateTurnReferences(m.References); err != nil {
		return err
	}
	return nil
}
