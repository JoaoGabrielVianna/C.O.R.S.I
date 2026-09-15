package domain

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

// Artifact items: the structured state of an artifact.

// MaxItemText bounds one entry. Two thousand characters is a long task
// and a short paragraph; an entry that wants more is an artifact of its
// own, and the body is where prose belongs.
const MaxItemText = 2000

// ArtifactItem is one entry of a list, one step of a plan, one task of a
// project.
//
// ── Why this is a row and not a line in a JSONB document ───────────────
// Three reasons, and the third was paid for already. A row can be counted
// and indexed without decoding a document. A row cannot be silently
// REPLACED by a caller that round-trips the whole object, which matters
// the moment a model is one of the writers: an edit built from a reading
// three turns old would drop every entry added since. And a row cannot
// drift in shape, whereas `releases.snapshots` stores documents, its
// shape changed, and 125 items across 6 rows that are immutable by
// trigger now render blank.
//
// ── Why there is no nesting ────────────────────────────────────────────
// No parent item, no subtasks. A checklist whose entries have entries is
// a plan, and a plan is an artifact. Adding depth later is one nullable
// column; removing it after people have used it is not.
type ArtifactItem struct {
	ID          uuid.UUID
	WorkspaceID uuid.UUID
	ArtifactID  uuid.UUID

	// Position orders the entries. Not unique, deliberately: a unique
	// constraint would make reordering a multi-statement dance to avoid
	// colliding with itself halfway through, and a checklist is not worth
	// that. Ties break on id, so two identical reads return an identical
	// order.
	Position int
	Text     string
	Done     bool

	CreatedAt time.Time
	UpdatedAt time.Time
}

func (i *ArtifactItem) Validate() error {
	if i.WorkspaceID == uuid.Nil {
		return Invalid("a workspace is required")
	}
	if i.ArtifactID == uuid.Nil {
		// An entry with no artifact is an entry of nothing. It would be
		// unreachable through the only read that exists for it.
		return Invalid("an artifact is required")
	}
	text := strings.TrimSpace(i.Text)
	if text == "" {
		return Invalid("text is required")
	}
	if len([]rune(text)) > MaxItemText {
		return Invalid("text is longer than %d characters", MaxItemText)
	}
	if i.Position < 0 {
		// Matches the CHECK. A negative position would sort before
		// everything forever, which is a way of pinning an entry that
		// nobody asked for and nothing documents.
		return Invalid("position must be zero or greater")
	}
	return nil
}

/* ── the change ──────────────────────────────────────────────────────── */

// ItemChange is a partial edit of one entry.
type ItemChange struct {
	Text     *string
	Done     *bool
	Position *int
}

func (c ItemChange) Empty() bool {
	return c.Text == nil && c.Done == nil && c.Position == nil
}

// ItemChangeResult reports what moved.
//
// PreviousDone is carried because "marquei como feito" and "já estava
// feito" are different answers to the same request, and only the first
// one is work.
type ItemChangeResult struct {
	PreviousDone bool

	TextChanged     bool
	DoneChanged     bool
	PositionChanged bool
}

func (r ItemChangeResult) Unchanged() bool {
	return !r.TextChanged && !r.DoneChanged && !r.PositionChanged
}

// Apply mutates the entry and reports what actually moved.
func (i *ArtifactItem) Apply(c ItemChange) ItemChangeResult {
	res := ItemChangeResult{PreviousDone: i.Done}
	if c.Text != nil && strings.TrimSpace(*c.Text) != i.Text {
		i.Text = strings.TrimSpace(*c.Text)
		res.TextChanged = true
	}
	if c.Done != nil && *c.Done != i.Done {
		i.Done = *c.Done
		res.DoneChanged = true
	}
	if c.Position != nil && *c.Position != i.Position {
		i.Position = *c.Position
		res.PositionChanged = true
	}
	return res
}
