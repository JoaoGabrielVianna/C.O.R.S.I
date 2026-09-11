package domain

import (
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
)

type Conversation struct {
	ID            uuid.UUID  `json:"id"`
	WorkspaceID   uuid.UUID  `json:"workspace_id"`
	AgentID       uuid.UUID  `json:"agent_id"`
	Title         string     `json:"title"`
	LastMessageAt *time.Time `json:"last_message_at,omitempty"`
	// ContextReferences is what this whole thread is about, set when the
	// conversation is opened from an entity — "Conversar com Scout" from a
	// Job Radar card.
	//
	// ── Why the thread holds it and not only the first message ─────────
	// Because "essa vaga" has to keep working on the fourth turn, after a
	// reload, and in a client that was not the one that opened it. A
	// subject attached to a single message would be a subject the
	// conversation forgets as soon as the user asks a follow-up.
	//
	// It is seeded at creation and not edited afterwards in this build.
	// Nil means the thread was opened from nothing in particular, which is
	// every conversation ever started from the composer.
	ContextReferences []ContextReference `json:"context_references,omitempty"`
	CreatedAt         time.Time          `json:"created_at"`
	UpdatedAt         time.Time          `json:"updated_at"`
	DeletedAt         *time.Time         `json:"deleted_at,omitempty"`
}

func (c *Conversation) Validate() error {
	if c.WorkspaceID == uuid.Nil {
		return Invalid("workspace_id required")
	}
	if c.AgentID == uuid.Nil {
		return Invalid("agent_id required")
	}
	if len(c.Title) > 200 {
		return Invalid("title must be <= 200 chars")
	}
	return ValidateContextReferences(c.ContextReferences)
}

// maxTitleRunes keeps derived titles readable in a narrow sidebar. Measured
// in runes, not bytes, so accented Portuguese doesn't get cut short.
const maxTitleRunes = 60

// DeriveTitle builds a thread title from its first user message.
//
// Deliberately not an LLM call: a title is worth zero tokens and zero
// latency, and the first line of what you asked is almost always a better
// label than a model's summary of it.
func DeriveTitle(firstMessage string) string {
	flat := strings.Join(strings.FieldsFunc(firstMessage, func(r rune) bool {
		return r == '\n' || r == '\r' || r == '\t'
	}), " ")
	flat = strings.Join(strings.Fields(flat), " ")
	if flat == "" {
		return "Nova conversa"
	}

	runes := []rune(flat)
	if len(runes) <= maxTitleRunes {
		return flat
	}
	// Cut on the last word boundary inside the budget so the title doesn't
	// end mid-word; fall back to a hard cut for text with no spaces.
	cut := runes[:maxTitleRunes]
	for i := len(cut) - 1; i > maxTitleRunes/2; i-- {
		if unicode.IsSpace(cut[i]) {
			return strings.TrimRight(string(cut[:i]), " ") + "…"
		}
	}
	return string(cut) + "…"
}
