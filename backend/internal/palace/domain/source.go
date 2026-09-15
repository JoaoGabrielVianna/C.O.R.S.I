package domain

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

// Sources: the evidence a memory was derived from.
//
// NOT chat.Source, which is reference material an agent may consult. See
// the package documentation in palace.go.

/* ── the kinds ───────────────────────────────────────────────────────── */

// SourceKind is where a piece of evidence came from.
type SourceKind string

const (
	// SourceText: something written or pasted. The ordinary case.
	SourceText SourceKind = "text"
	// SourceVoiceTranscript: the text of something spoken. Kept distinct
	// from `text` because a transcript carries transcription error, and a
	// reader deciding how much to trust a sentence needs to know which
	// one it is reading.
	SourceVoiceTranscript SourceKind = "voice_transcript"
	// SourceSystemEvent: something this product observed happening.
	SourceSystemEvent SourceKind = "system_event"
	// SourceExternal: evidence about a system we do not own. It requires
	// an ExternalRef, because evidence about somebody else's system that
	// cannot say WHICH system is not provenance.
	//
	// Note what this does NOT do: it does not make the memory derived
	// from it a verified external claim. That guarantee belongs to
	// ReadReceipt and depends on a capability having run in the turn.
	// A source is a record of what was seen once; it ages.
	SourceExternal SourceKind = "external"
)

var SourceKinds = []SourceKind{
	SourceText,
	SourceVoiceTranscript,
	SourceSystemEvent,
	SourceExternal,
}

func (k SourceKind) Valid() bool {
	switch k {
	case SourceText, SourceVoiceTranscript, SourceSystemEvent, SourceExternal:
		return true
	}
	return false
}

func (k SourceKind) String() string { return string(k) }

func SourceKindNames() []string { return names(SourceKinds) }

func ParseSourceKind(raw string) (SourceKind, error) {
	k := SourceKind(fold(raw))
	if !k.Valid() {
		return "", Invalid("unknown source kind %s; the kinds are %s",
			quoteToken(raw), strings.Join(SourceKindNames(), ", "))
	}
	return k, nil
}

// RequiresExternalRef reports whether this kind must name where it came
// from.
func (k SourceKind) RequiresExternalRef() bool { return k == SourceExternal }

/* ── the entity ──────────────────────────────────────────────────────── */

const (
	MaxSourceContent     = 20000
	MaxSourceExternalRef = 1000
)

// Source is one piece of evidence.
//
// ══════════════════════════════════════════════════════════════════════
//
//	A SOURCE IS NEVER EDITED
//
// ══════════════════════════════════════════════════════════════════════
//
// There is no Change type in this file and no update capability, and
// neither is an omission. A source answers "what was actually said or
// seen", and evidence that can be rewritten after the fact answers
// nothing: the moment a transcript can be corrected in place, no memory
// resting on it can be audited, because the thing it rested on is gone.
//
// A corrected transcript is a NEW source. The memory can be linked to
// both, and the disagreement between the two is itself information.
//
// What CAN change is the sensitivity of a source, and it deliberately
// cannot here either: relabelling evidence is a real need, and the day it
// is asked for it gets its own narrow operation rather than a general
// update that also happens to allow rewriting the content.
type Source struct {
	ID          uuid.UUID
	WorkspaceID uuid.UUID

	Kind    SourceKind
	Content string
	// ExternalRef says where it came from when that is outside this
	// product: a URL, a message id, an event name. Required for
	// SourceExternal, optional otherwise.
	ExternalRef string

	// CapturedAt is when the evidence was PRODUCED, which is not when the
	// row was written. A transcript of yesterday's voice note is captured
	// today, and confusing the two would put every imported thing in the
	// present.
	CapturedAt time.Time

	Sensitivity Sensitivity

	CreatedAt time.Time
	UpdatedAt time.Time
}

func (s *Source) Validate() error {
	if s.WorkspaceID == uuid.Nil {
		return Invalid("a workspace is required")
	}
	if !s.Kind.Valid() {
		return Invalid("unknown source kind %s; the kinds are %s",
			quoteToken(string(s.Kind)), strings.Join(SourceKindNames(), ", "))
	}
	if strings.TrimSpace(s.Content) == "" {
		// Evidence that carries nothing is not evidence. If all the caller
		// has is a link, the content is what was seen at that link, and
		// requiring it forces them to state it rather than store a pointer
		// to something that may not answer later.
		return Invalid("content is required")
	}
	if len([]rune(s.Content)) > MaxSourceContent {
		return Invalid("content is longer than %d characters", MaxSourceContent)
	}
	if len([]rune(s.ExternalRef)) > MaxSourceExternalRef {
		return Invalid("external_ref is longer than %d characters", MaxSourceExternalRef)
	}
	if s.Kind.RequiresExternalRef() && strings.TrimSpace(s.ExternalRef) == "" {
		return Invalid("a source of kind %s must say where it came from in external_ref",
			SourceExternal)
	}
	if s.CapturedAt.IsZero() {
		// A zero instant would sort as the year one and make "what did I
		// capture this week" quietly wrong.
		return Invalid("captured_at is required")
	}
	if !s.Sensitivity.Valid() {
		return Invalid("unknown sensitivity %s; the levels are %s",
			quoteToken(string(s.Sensitivity)), strings.Join(SensitivityNames(), ", "))
	}
	return nil
}
