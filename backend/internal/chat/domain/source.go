package domain

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

// Source is reference material an agent can consult: a named piece of text
// the user wrote or pasted on purpose.
//
// ── Source is not Memory ───────────────────────────────────────────────
// The boundary is normative, not stylistic. Memory answers "what should
// this agent remember about me?" and is short, distilled, and usually born
// inside a conversation. Source answers "what material can this agent
// consult?" and is long, titled, and written deliberately.
//
// The practical test: if it
// would fit on one line and probably came out of a conversation, it is a
// Memory; if it has a title and paragraphs, it is a Source.
//
// Nothing converts one into the other. There is no mechanism that promotes
// a source to memory or distils a memory out of a source, and adding one
// would collapse a distinction the whole design rests on.
//
// ── Why there is no provenance ─────────────────────────────────────────
// A memory carries a weak reference to the conversation it was learnt in,
// because "why does the agent know this?" is a real question about it. A
// source has no such origin: it was written. There is nothing to resolve
// and nothing to lose when a conversation is deleted.
type Source struct {
	ID          uuid.UUID `json:"id"`
	WorkspaceID uuid.UUID `json:"workspace_id"`
	AgentID     uuid.UUID `json:"agent_id"`
	Title       string    `json:"title"`
	// Description is metadata for the human. It never reaches the model:
	// sending a description of a text that is itself being sent is paying
	// twice for the same information.
	Description string `json:"description"`
	Content     string `json:"content"`
	Enabled     bool   `json:"enabled"`

	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	DeletedAt *time.Time `json:"deleted_at,omitempty"`

	// Characters is the exact rune count of Content, resolved at read time.
	// It is what makes the size of a source visible before turning it on,
	// and it is counted server-side so the number on screen is the number
	// the budget spends. Writes ignore it.
	Characters int `json:"characters"`

	// EstimatedTokens is Characters through the module's one heuristic. It
	// is an estimate and is named one; see app.EstimateTokens.
	EstimatedTokens int `json:"estimated_tokens"`

	// InContext says whether this source would enter the next turn, given
	// the current budget. Computed at read time by the same function the
	// context builder uses, so the page and the wire cannot disagree.
	InContext bool `json:"in_context"`

	// Oversized says this source cannot fit the block on its own, whatever
	// else is turned off. It is a different fact from InContext being
	// false, and collapsing the two would leave the user turning other
	// sources off forever waiting for this one to appear.
	Oversized bool `json:"oversized"`
}

// Limits mirror the CHECK constraints, so validation fails in the domain
// with a message rather than in Postgres with a constraint name.
const (
	MaxSourceTitle       = 120
	MaxSourceDescription = 280
	// MaxSourceContent is the ceiling the module already chose for long
	// user-written text (agents.system_prompt). Note it is deliberately
	// larger than the Sources context budget: a source may legitimately be
	// worth storing and too big to send, and §4.1 of the spec requires that
	// case to be visible rather than silently truncated.
	MaxSourceContent = 20000
)

func (s *Source) Validate() error {
	if s.WorkspaceID == uuid.Nil {
		return Invalid("workspace_id required")
	}
	if s.AgentID == uuid.Nil {
		return Invalid("agent_id required")
	}
	title := strings.TrimSpace(s.Title)
	if title == "" {
		return Invalid("title required")
	}
	// Counted in runes, like the CHECK: len() would reject accented
	// Portuguese well before the database would.
	if len([]rune(title)) > MaxSourceTitle {
		return Invalid("title must be <= 120 chars")
	}
	if len([]rune(s.Description)) > MaxSourceDescription {
		return Invalid("description must be <= 280 chars")
	}
	if strings.TrimSpace(s.Content) == "" {
		return Invalid("content required")
	}
	if len([]rune(s.Content)) > MaxSourceContent {
		return Invalid("content must be <= 20000 chars")
	}
	return nil
}
