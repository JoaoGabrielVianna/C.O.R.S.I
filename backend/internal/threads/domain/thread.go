// Package domain is the Threads bounded context: units of content work and
// the lifecycle they move through.
//
// ── What a Thread is ───────────────────────────────────────────────────
// A durable piece of creative work in progress. It starts as a sentence
// somebody did not want to lose and ends, if it ends, as something that was
// published. It is worked on across many sittings and, in this product,
// across many conversations.
//
// ── What a Thread is NOT ───────────────────────────────────────────────
// A chat conversation. The two are related exactly once, and loosely: a
// conversation may work on a thread, and the thread outlives it. Nothing in
// this package imports, names or knows about Agents, Chat, conversations or
// any agent — see the note in module.go.
//
// ── Where the intelligence is not ──────────────────────────────────────
// Here. This package holds STATE and the rules that keep it well formed. It
// does not rewrite a hook, judge a paragraph or decide that a draft is
// good. When a user says "deixa mais provocativo", the agent produces the
// new text and this context stores the result — never the instruction. That
// boundary is the reason `content` is one opaque string with a length bound
// and no structure: a domain that tried to model a hook would be a domain
// with an opinion about writing.
//
// This package imports nothing from the platform and nothing from any other
// module. It is plain data and the rules over it.
package domain

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

/* ── the lifecycle ───────────────────────────────────────────────────── */

// Status is where a piece of content sits in its life.
//
// ── This list is canonical, and it is the only copy that validates ─────
// The same five names appear in the CHECK constraint of
// migrations/threads/0001_init.up.sql. That is not two sources of truth:
// the constraint is a backstop that refuses a row this package would never
// build. Every WRITE path validates here and nowhere else — in particular
// the tools package must not declare its own list; it calls ParseStatus.
type Status string

const (
	// StatusIdea: captured, not written. "Guarda essa ideia."
	StatusIdea Status = "idea"
	// StatusDraft: there is text and it is being worked on.
	StatusDraft Status = "draft"
	// StatusReview: the writing is finished and is waiting to be read.
	StatusReview Status = "review"
	// StatusPublished: it went out. Only the user can put a thread here —
	// see the tool description; nothing in this system observes a post.
	StatusPublished Status = "published"
	// StatusArchived: retired without being published, and kept. Distinct
	// from deletion: archiving says "not this", deleting says "never this".
	StatusArchived Status = "archived"
)

// Statuses is the lifecycle in its natural order.
//
// ── Why five and not the six that were proposed ────────────────────────
// A `ready` between review and published was considered and dropped. With a
// single operator and no approval workflow, "marca para revisão" and "está
// pronto" are the same person making the same judgement minutes apart, and
// a state distinguished only by which sentence happened to be said is a
// state that gets used inconsistently — which makes every later question
// asked of it ("what can I post?") quietly wrong. Adding it is one CHECK
// constraint and one constant, and that is a cheaper change than un-
// teaching a habit.
//
// ── Why the order is presentation and not permission ───────────────────
// There is no state machine, deliberately, and for the reason Job Radar
// gives about its stages: real content goes backwards. A published post is
// pulled back to draft to be reworked, an archived idea comes back to life
// when it suddenly matters, a review round sends something to draft.
// Encoding an order would mean the system refusing the truth because it
// disagreed with a diagram.
var Statuses = []Status{
	StatusIdea,
	StatusDraft,
	StatusReview,
	StatusPublished,
	StatusArchived,
}

func (s Status) Valid() bool {
	switch s {
	case StatusIdea, StatusDraft, StatusReview, StatusPublished, StatusArchived:
		return true
	}
	return false
}

func (s Status) String() string { return string(s) }

// StatusNames is the vocabulary as plain strings, for a schema description
// or an error message. Built from the same slice, so a status added later
// cannot be added to the enum and forgotten in the model's instructions.
func StatusNames() []string {
	out := make([]string, len(Statuses))
	for i, s := range Statuses {
		out[i] = string(s)
	}
	return out
}

// ParseStatus turns caller input into a status, or explains what was wrong.
//
// Lenient about case and surrounding space, strict about everything else.
// The callers are a person and a model, and both will send "Draft" or
// " draft "; refusing those would be pedantry that teaches the model
// nothing. What it will NOT do is guess — "reviewing" is not "review", and
// an approximate match here would move a real piece of work to a state
// nobody asked for.
func ParseStatus(raw string) (Status, error) {
	s := Status(strings.ToLower(strings.TrimSpace(raw)))
	if !s.Valid() {
		return "", Invalid("unknown status %q; the statuses are %s",
			raw, strings.Join(StatusNames(), ", "))
	}
	return s, nil
}

/* ── the entity ──────────────────────────────────────────────────────── */

const (
	// MaxTitle bounds the handle. 200 is what every other short name in
	// this system carries.
	MaxTitle = 200
	// MaxContent bounds the work. 20000 is the ceiling every other long-text
	// column here uses — chat.agent_sources.content, an opportunity's
	// description — so the limit is a property of the platform rather than a
	// number guessed per table.
	MaxContent = 20000
)

// Thread is one unit of content work.
//
// ── Why there is no channel, format, tag, schedule or metric ───────────
// Because none of them has been asked a question yet. "Transforma isso num
// post de LinkedIn" produces LinkedIn-shaped text, and the text is what
// carries that fact today. A `channel` column would be a second place to
// state it, free to disagree with the content the moment the same piece is
// reshaped for somewhere else. The trigger for adding one is a real
// question this shape cannot answer — "which of these are LinkedIn posts"
// asked by a person who has enough of them for the answer to matter — and
// until then a field that is written and never read is a field that will be
// wrong.
type Thread struct {
	ID          uuid.UUID `json:"id"`
	WorkspaceID uuid.UUID `json:"-"`

	Title   string `json:"title"`
	Content string `json:"content"`
	Status  Status `json:"status"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Validate checks a thread that is about to be written.
//
// It validates SHAPE and not the story: an idea with three thousand words
// of content is fine, and so is a published thread whose text is one line.
// The only things refused are values that would make the record unreadable
// or unbounded.
func (t *Thread) Validate() error {
	title := strings.TrimSpace(t.Title)
	if title == "" {
		// A content item nobody can name is one nobody can ask for again —
		// and asking for it again by name is the entire interaction this
		// module exists to support.
		return Invalid("a title is required")
	}
	if len([]rune(title)) > MaxTitle {
		return Invalid("title is longer than %d characters", MaxTitle)
	}
	if len([]rune(t.Content)) > MaxContent {
		return Invalid("content is longer than %d characters", MaxContent)
	}
	if !t.Status.Valid() {
		return Invalid("unknown status %q; the statuses are %s",
			t.Status, strings.Join(StatusNames(), ", "))
	}
	return nil
}

// NormalizeTitle trims a title. It does not change case: the stored casing
// is what was written, and a title is a name.
func NormalizeTitle(raw string) string { return strings.TrimSpace(raw) }

/* ── the change ──────────────────────────────────────────────────────── */

// Change is a partial edit of a thread: each field is nil when the caller
// is not touching it.
//
// ── Why nil-means-unchanged, and not a full replacement ────────────────
// Because every caller that edits content edits ONE aspect of it. "Muda o
// hook" rewrites the content and leaves the title and the status alone;
// "marca como review" moves the status and must not touch a word of the
// text. A full-replacement contract would make the caller resend the fields
// it is not changing, and the day it resends a stale copy of a 4000-
// character draft — because it read the thread three turns ago — the edit
// silently reverts work. Absent is the only value that cannot do that.
//
// ── Why it is not a patch language ─────────────────────────────────────
// Three optional fields is not a DSL. There is no path syntax, no operation
// verb, no ordering: the whole grammar is "these fields, this value". A
// mutable field added later adds a pointer here and an argument to the
// tool, and nothing learns a new dialect.
type Change struct {
	Title   *string
	Content *string
	Status  *Status
}

// Empty reports whether this change would change nothing.
func (c Change) Empty() bool {
	return c.Title == nil && c.Content == nil && c.Status == nil
}

// Apply mutates the thread and reports what actually moved.
//
// ── Why it reports rather than just doing ──────────────────────────────
// Because the caller that asked for the edit is the only party that can
// still see both sides of it, and a confirmation built from that is
// checkable: "status: review → published" can be verified by a reader,
// "done" cannot. It is the same reason Job Radar's MoveResult carries the
// previous stage.
func (t *Thread) Apply(c Change) ChangeResult {
	res := ChangeResult{PreviousStatus: t.Status}
	if c.Title != nil && NormalizeTitle(*c.Title) != t.Title {
		t.Title = NormalizeTitle(*c.Title)
		res.TitleChanged = true
	}
	if c.Content != nil && *c.Content != t.Content {
		t.Content = *c.Content
		res.ContentChanged = true
	}
	if c.Status != nil && *c.Status != t.Status {
		t.Status = *c.Status
		res.StatusChanged = true
	}
	return res
}

// ChangeResult is what a completed edit reports back.
//
// Unchanged is true when every field the caller sent already held the value
// it sent. The edit is then a no-op, and saying so is what stops a model
// reporting work it did not do — the same honesty `move` owes when an
// opportunity was already at the requested stage.
type ChangeResult struct {
	PreviousStatus Status
	TitleChanged   bool
	ContentChanged bool
	StatusChanged  bool
}

func (r ChangeResult) Unchanged() bool {
	return !r.TitleChanged && !r.ContentChanged && !r.StatusChanged
}
