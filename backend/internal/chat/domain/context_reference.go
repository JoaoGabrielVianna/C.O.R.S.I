package domain

import (
	"strings"
	"unicode/utf8"
)

// Context references: WHAT a conversation is about.
//
// ── Why this is not TurnReference, and must never become it ────────────
// The two words are nearly the same and the concepts are not. Read them
// side by side:
//
//	TurnReference     a CAPABILITY the user attached to one turn. Its id is
//	                  a tool name. Its only effect is to NARROW the tools
//	                  that turn declares. See reference.go.
//	ContextReference  an ENTITY the conversation is about. Its id is a row
//	                  in some other bounded context. It changes no
//	                  capability at all.
//
// Collapsing them would not be a naming compromise, it would be a bug with
// a compile error hiding it: scopeTools intersects a selection with the
// agent's grants and REFUSES anything that is not an authorized tool, so an
// opportunity arriving as a TurnReference would be rejected as an unknown
// tool — and if that check were loosened to let it through, an entity id
// would start silently deciding which capabilities a turn exposes.
//
// So they are separate types, separate columns, and separate paths through
// the turn. The only thing they share is the word "reference" in English.
//
// ── What this type is allowed to carry ─────────────────────────────────
// Identity, and enough words to recognise it on a screen. NOT the entity.
// The canonical state of an opportunity lives in Job Radar and is read
// through Job Radar's tools, every time it is needed. A reference that
// carried a copy would be a snapshot that starts lying the moment the
// entity moves — which is precisely the failure the freshness rule exists
// to prevent:
//
//	reference  →  identifies       →  job_radar.opportunity/<uuid>
//	tool       →  reads the state  →  stage = interview (now, not then)
//
// ── Why nothing here knows about a card ────────────────────────────────
// A channel decides how to draw this. Web draws a card, a text channel
// draws a line of text, and both are reading the same three fields. There
// is no `render_as`, no component name, and no HTML — a field like that
// would make the domain unable to describe a conversation happening
// somewhere React does not run.

// ContextReferenceType is the kind of entity a reference points at.
//
// The convention is the tool namespace convention, deliberately:
//
//	provider.entity        job_radar.opportunity
//	                       github.repository
//	                       calendar.event
//
// Two segments, lowercase, `[a-z][a-z0-9_]*`. The first segment is the
// PROVIDER — the bounded context that owns the entity and is the authority
// on it — and it is what the resolver registry keys on. Sharing the shape
// with tool names is not decoration: `job_radar.opportunity` and
// `job_radar.opportunity.get` are visibly the same subject and the same
// owner, which is the relationship a reader has to hold in their head.
type ContextReferenceType string

// maxContextReferenceTypeLength bounds the type. Same ceiling as a tool
// name, for the same reason: it is an identifier that travels on the wire
// and into a stored record.
const maxContextReferenceTypeLength = 64

func (t ContextReferenceType) Valid() bool {
	s := string(t)
	if s == "" || len(s) > maxContextReferenceTypeLength {
		return false
	}
	segments := strings.Split(s, ".")
	// Exactly two. A single segment names a provider without saying what
	// kind of thing is being pointed at, and three would be ambiguous
	// against the tool convention, where the third segment is an ACTION.
	if len(segments) != 2 {
		return false
	}
	for _, seg := range segments {
		if seg == "" || seg[0] < 'a' || seg[0] > 'z' {
			return false
		}
		for i := 0; i < len(seg); i++ {
			c := seg[i]
			if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_') {
				return false
			}
		}
	}
	return true
}

func (t ContextReferenceType) String() string { return string(t) }

// Provider is the bounded context that owns the entity: the first segment.
//
// It exists as a method rather than a stored field so it cannot disagree
// with the type. A struct carrying both would eventually carry
// `type: "job_radar.opportunity", provider: "github"`, and nothing would
// notice.
func (t ContextReferenceType) Provider() string {
	if i := strings.Index(string(t), "."); i > 0 {
		return string(t)[:i]
	}
	return ""
}

const (
	// MaxContextReferences bounds one attachment list.
	//
	// Eight. Smaller than MaxTurnReferences because these are subjects, not
	// capabilities: a message about eight different opportunities is not a
	// message anybody writes, and the ceiling is what keeps a client from
	// turning the context block into a channel it controls.
	MaxContextReferences = 8

	// maxContextReferenceIDLength bounds the entity id. A uuid is 36; the
	// slack is for providers whose ids are not uuids (a repository full
	// name, a calendar event id) without letting an id become a payload.
	maxContextReferenceIDLength = 200

	// maxContextReferenceLabelLength bounds the recognition text.
	maxContextReferenceLabelLength = 140

	// maxContextReferenceSubtitleLength bounds the one optional display
	// line. Deliberately short: it is a hint for recognition, and a field
	// that could hold a paragraph would be a snapshot of the entity wearing
	// a display name.
	maxContextReferenceSubtitleLength = 140
)

// ContextReference is one entity a conversation or a turn is about.
type ContextReference struct {
	Type ContextReferenceType `json:"type"`
	// ID is the entity's identity inside its provider. Opaque here: this
	// package never parses it, never validates its shape beyond a length,
	// and never assumes it is a uuid. Only the provider knows what its own
	// ids look like.
	//
	// It is UNTRUSTED INPUT. Holding one grants nothing and proves nothing;
	// it is resolved, workspace-scoped, by the provider before it is stored
	// or shown. See ports.ContextReferenceResolver.
	ID string `json:"id"`
	// Label is how the entity reads to a person: "Acme · Backend Engineer".
	//
	// Written by the BACKEND from the provider's own data, never taken from
	// the request. A client that could author this could write anything into
	// the permanent record and into the model's context — the same rule
	// TurnReference.Label follows, for the same reason.
	Label string `json:"label"`
	// Subtitle is one optional line of extra recognition ("Applied"). It is
	// a display hint and explicitly NOT state the model should reason from:
	// it is frozen at attach time, and the tools are what report the
	// present. Empty when the provider has nothing worth adding.
	Subtitle string `json:"subtitle,omitempty"`
	// Unavailable marks a reference whose entity could not be resolved when
	// it was last read: deleted, archived, or no longer visible to this
	// workspace.
	//
	// ── Why this is a field and not a dropped row ──────────────────────
	// Because a conversation that discussed something for three weeks does
	// not stop having discussed it when the row is deleted. Removing the
	// reference would rewrite the past; keeping it with this flag lets every
	// channel say "this is no longer available" while the transcript stays
	// true. It is never persisted as true — it is computed on read, so a
	// reference that becomes available again simply is.
	Unavailable bool `json:"unavailable,omitempty"`
}

// Key is the canonical identity of a reference, for dedupe and comparison.
// Type and id together; the label is display and never participates.
func (r ContextReference) Key() string { return string(r.Type) + ":" + r.ID }

// Validate checks one reference as it arrives from a client.
//
// It validates SHAPE only. Whether the entity exists, and whether this
// workspace may see it, is a question only the provider can answer, and it
// is asked separately — see the resolver. Both checks are mandatory and
// neither substitutes for the other.
func (r ContextReference) Validate() error {
	if !r.Type.Valid() {
		return ContextReferenceRejected(CodeContextReferenceTypeInvalid,
			"reference type "+quoteForMessage(r.Type.String())+
				" is not a valid type; it must read like job_radar.opportunity")
	}
	if strings.TrimSpace(r.ID) == "" {
		return ContextReferenceRejected(CodeContextReferenceInvalid,
			"reference id is required")
	}
	if len(r.ID) > maxContextReferenceIDLength {
		return ContextReferenceRejected(CodeContextReferenceInvalid,
			"reference id is too long")
	}
	if utf8.RuneCountInString(r.Label) > maxContextReferenceLabelLength {
		return ContextReferenceRejected(CodeContextReferenceInvalid,
			"reference label is too long")
	}
	if utf8.RuneCountInString(r.Subtitle) > maxContextReferenceSubtitleLength {
		return ContextReferenceRejected(CodeContextReferenceInvalid,
			"reference subtitle is too long")
	}
	return nil
}

// ValidateContextReferences checks a whole attachment list.
//
// An empty list and an absent one are the same input and mean the same
// thing: this turn is about nothing in particular.
func ValidateContextReferences(refs []ContextReference) error {
	if len(refs) > MaxContextReferences {
		return ContextReferenceRejected(CodeContextReferenceInvalid,
			"a message may carry at most "+itoa(MaxContextReferences)+" context references")
	}
	for _, r := range refs {
		if err := r.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// DedupeContextReferences keeps the first occurrence of each identity,
// preserving order.
//
// Order is preserved because it is the order a person attached them, and
// sorting would make the chips move between reads. Dedupe rather than
// refuse: attaching the same subject twice is a harmless way of saying one
// true thing twice, and it happens naturally when a conversation seeded
// with an entity also sends it on a turn.
func DedupeContextReferences(refs []ContextReference) []ContextReference {
	if len(refs) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(refs))
	out := make([]ContextReference, 0, len(refs))
	for _, r := range refs {
		if seen[r.Key()] {
			continue
		}
		seen[r.Key()] = true
		out = append(out, r)
	}
	return out
}

/* ── the refusals ────────────────────────────────────────────────────── */

// The machine-readable reasons an attachment is refused. All are
// KindInvalid and therefore 400, matching how a bad tool selection is
// handled: the request is rejected before anything is persisted and before
// the provider is called.
const (
	// CodeContextReferenceInvalid: malformed — no id, too many, too long.
	CodeContextReferenceInvalid = "context_reference_invalid"
	// CodeContextReferenceTypeInvalid: the type does not read like a type.
	CodeContextReferenceTypeInvalid = "context_reference_type_invalid"
	// CodeContextReferenceTypeUnknown: a well-formed type with no resolver
	// in this build. Told apart from malformed so a client that is ahead of
	// the server learns that, rather than concluding it sent garbage.
	CodeContextReferenceTypeUnknown = "context_reference_type_unknown"
	// CodeContextReferenceNotFound: the provider has no such entity for this
	// workspace.
	//
	// ── Why there is no "forbidden" alongside this ─────────────────────
	// Because a reference to another workspace's row and a reference to
	// something that never existed must be indistinguishable. A distinct
	// "forbidden" would confirm the row exists somewhere, which is exactly
	// the fact a fabricated id is fishing for.
	CodeContextReferenceNotFound = "context_reference_not_found"
)

// ContextReferenceRejected refuses a request over its attachments.
func ContextReferenceRejected(code, msg string) *Error {
	return &Error{Kind: KindInvalid, Message: msg, Code: code}
}
