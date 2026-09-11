package domain

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

// Memory is one thing an agent was told to remember between conversations.
//
// ── Why it belongs to the agent ────────────────────────────────────────
// Not to the workspace, and not to the conversation. A memory of the
// content agent leaking into the finance agent is the exact opposite of
// "specialised agents", and a memory that dies with its thread is just
// history under another name. The consequence is accepted: the same fact
// may have to be saved twice, in two agents.
//
// ── Why provenance is weak ─────────────────────────────────────────────
// SourceConversationID may point at a conversation that no longer exists,
// or be nil on a memory whose Origin is nevertheless OriginConversation.
// That is not a broken record: it is the rule that a memory outlives the
// thread it was learnt in, and the interface is expected to say "the
// conversation is no longer available" rather than fail.
type Memory struct {
	ID          uuid.UUID `json:"id"`
	WorkspaceID uuid.UUID `json:"workspace_id"`
	AgentID     uuid.UUID `json:"agent_id"`
	Content     string    `json:"content"`

	Origin MemoryOrigin `json:"origin"`
	// SourceConversationID is set when the memory was captured inside a
	// thread and that thread has not been hard-deleted since.
	SourceConversationID *uuid.UUID `json:"source_conversation_id,omitempty"`
	// SourceMessageSeq is a hint, never a guarantee: messages are
	// hard-deleted by edit and regenerate, so the turn it names may be gone
	// while the conversation is still there.
	SourceMessageSeq *int64 `json:"source_message_seq,omitempty"`

	Enabled bool `json:"enabled"`
	Pinned  bool `json:"pinned"`

	// ModelProposed says a model composed these words and the user approved
	// them, rather than the user having written them.
	//
	// A different question from Origin, which says *where* the capture
	// happened. Both a proposal and a sentence the user highlighted
	// themselves are captured in a conversation; only one of them is text
	// the user did not write. Approving is not authoring, and a system that
	// cannot tell them apart cannot answer "did I say this, or did it?"
	//
	// It is never accepted from a request. See app.CreateMemory, which
	// always writes false, and app.CreateProposedMemory, which is the only
	// path that writes true.
	ModelProposed bool `json:"model_proposed"`

	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	DeletedAt *time.Time `json:"deleted_at,omitempty"`

	// SourceConversationTitle is resolved at read time by a join, and is
	// never a column on this row. Nil with a non-nil SourceConversationID
	// means the conversation was deleted: the id survived, the thread did
	// not. Writes ignore it.
	SourceConversationTitle *string `json:"source_conversation_title,omitempty"`

	// InContext says whether this memory would enter the next turn, given
	// the current budget. It is computed by SelectMemories at read time —
	// the same function the context builder uses — so the count the page
	// shows and the block the model receives cannot drift apart.
	InContext bool `json:"in_context"`
}

// MemoryOrigin says how a memory was captured. Both values that exist
// describe a deliberate human act, because v1 has no mechanism by which a
// model saves anything: that needs function calling, which is Tools.
type MemoryOrigin string

const (
	// OriginManual is a memory typed straight into the Memory page.
	OriginManual MemoryOrigin = "manual"
	// OriginConversation is a memory captured from a thread, whether from a
	// message action or the /lembrar command.
	OriginConversation MemoryOrigin = "conversation"
)

// MaxMemoryContent mirrors the CHECK constraint. A memory is a distilled
// fact; anything longer is a document, and documents are Sources.
const MaxMemoryContent = 2000

func (m *Memory) Validate() error {
	if m.WorkspaceID == uuid.Nil {
		return Invalid("workspace_id required")
	}
	if m.AgentID == uuid.Nil {
		return Invalid("agent_id required")
	}
	content := strings.TrimSpace(m.Content)
	if content == "" {
		return Invalid("content required")
	}
	// Counted in runes: the CHECK is on length(), which Postgres measures in
	// characters too. Using len() here would reject accented Portuguese
	// hundreds of characters before the database would.
	if len([]rune(content)) > MaxMemoryContent {
		return Invalid("content must be <= 2000 chars")
	}
	switch m.Origin {
	case OriginManual, OriginConversation:
	default:
		return Invalid("origin must be manual or conversation")
	}
	// A manual memory naming a source conversation would be a record that
	// contradicts itself, and provenance is only worth anything if it can
	// be trusted.
	if m.Origin == OriginManual && m.SourceConversationID != nil {
		return Invalid("a manual memory cannot name a source conversation")
	}
	// There is nowhere else a model could have proposed it from. A
	// manually-typed memory marked as the model's words would be the same
	// kind of self-contradicting record as the line above.
	if m.ModelProposed && m.Origin != OriginConversation {
		return Invalid("a model-proposed memory must come from a conversation")
	}
	return nil
}
