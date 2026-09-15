package domain

import (
	"strings"

	"github.com/google/uuid"
)

// Effect references: which entity a write actually touched.
//
// ══════════════════════════════════════════════════════════════════════
//
//	RECEIPT PROVES EXECUTION
//	EFFECT REF IDENTIFIES THE EFFECT
//	READ PROVES STATE
//
// ══════════════════════════════════════════════════════════════════════
//
// ── The incident this exists to close ──────────────────────────────────
// A turn created a Room and an Artifact, then hit the round ceiling before
// adding the two items it had been asked for. The user asked it to try
// again. The next turn created a SECOND Room and a SECOND Artifact, leaving
// the first pair orphaned.
//
// The model was not ignoring its receipt. It had one, and it was correct:
//
//	EXECUTED     palace.room.create x1
//	EXECUTED     palace.artifact.create x1
//	NOT_EXECUTED palace.artifact.item.add x2 tool_round_limit
//
// It knew the creates had run. It had no way whatsoever to say WHICH room
// or WHICH artifact, because the capabilities are Confidential, so their
// payloads are not kept and are never replayed — and the interrupted turn's
// own prose was cut off before it named anything.
//
// So the receipt proved the verb and not the object. Proving execution
// without being able to identify what was executed is not enough to make a
// retry safe, and the cost of that gap is duplicated state.
//
// ── What a ref is allowed to be ────────────────────────────────────────
// A type and an opaque id. Nothing else, ever. It exists so a later turn
// can say "the thing I already made is THIS one" and then go and READ it;
// it does not exist to carry what the thing is called or what it contains.
//
// ── Why the id must be a UUID in this version ──────────────────────────
// Because a free-text id is a channel, and a channel that reaches the model
// while bypassing redaction is the one thing this type must not become. A
// capability under pressure to be helpful could put `"Presentes pra
// Namorada"` in an id field, and nothing downstream would know it was
// content rather than an identifier.
//
// Requiring a UUID makes that structurally impossible rather than merely
// discouraged: a name cannot parse as one. It is deliberately the most
// conservative rule that still solves the incident, and widening it — to
// admit, say, a numeric external id — is a decision that has to re-examine
// this boundary rather than a patch to a regex. Until then, a capability
// whose identity is not a UUID reports NO ref, and the fallback is safe by
// construction: no ref means the resume has to read, which is the behaviour
// we already require.

// EffectRef names the entity one write touched.
type EffectRef struct {
	// Type is the KIND of thing, in the owning module's own vocabulary:
	// "room", "artifact", "memory". Lowercase, short, and never a vendor.
	Type string `json:"type"`
	// ID is the opaque identifier. See the note above on why it is a UUID.
	ID string `json:"id"`
}

// maxEffectTypeLength bounds the type. Forty is far above any real word and
// low enough that the field cannot become a sentence.
const maxEffectTypeLength = 40

// Valid reports whether this ref may be recorded and shown to a model.
//
// Invalid refs are DROPPED rather than repaired. A ref that has to be fixed
// up is a ref nobody checked, and the failure mode of a wrong identifier is
// a resume that confidently continues the wrong entity.
func (r EffectRef) Valid() bool {
	t := r.Type
	if t == "" || len(t) > maxEffectTypeLength {
		return false
	}
	for i := 0; i < len(t); i++ {
		c := t[i]
		ok := (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_'
		if !ok {
			return false
		}
	}
	if _, err := uuid.Parse(r.ID); err != nil {
		return false
	}
	return true
}

// String is the form a model reads: `artifact:0c7e…`.
//
// One token, unambiguous, and impossible to confuse with prose because the
// half after the colon is a UUID.
func (r EffectRef) String() string { return r.Type + ":" + r.ID }

// ParseEffectRef reads the stored form back. Used by the repository, which
// keeps the two halves in separate columns, and by tests.
func ParseEffectRef(s string) (EffectRef, bool) {
	i := strings.IndexByte(s, ':')
	if i <= 0 {
		return EffectRef{}, false
	}
	r := EffectRef{Type: s[:i], ID: s[i+1:]}
	if !r.Valid() {
		return EffectRef{}, false
	}
	return r, true
}
