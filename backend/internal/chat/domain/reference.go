package domain

import "strings"

// References: what the user explicitly attached to ONE turn.
//
// ── The distinction this file exists to protect ────────────────────────
// A grant (see AgentTool) says what an agent MAY do, ever, until somebody
// revokes it. A reference says what the user chose to put in front of the
// agent for a single turn. They are different facts with different
// lifetimes, and collapsing them is the mistake the whole design is
// arranged to make impossible:
//
//	authorization  →  a decision about the agent, stored in agent_tools
//	reference      →  a decision about this turn, stored with the message
//
// A reference NEVER grants anything. The only thing it can do is narrow,
// for one turn, a set that was already allowed. The rule, stated once:
//
//	not authorized                    → refused, whatever the client sent
//	authorized, no explicit selection → legacy: everything is exposed
//	authorized, selected              → exposed
//	authorized, not selected, in a
//	  turn that made a selection      → withheld from THIS turn only
//
// ── Why the label is stored and not resolved ───────────────────────────
// Because the transcript has to keep telling the truth after the catalogue
// changes. A turn that selected a tool which was later revoked, renamed or
// deleted from the build must still read "you selected X here", and the
// only way to answer that without consulting a catalogue that has moved on
// is to freeze the words when the turn happened. It is the same reason
// ContextReport is a snapshot and Message.Model is stamped.
//
// What is frozen is deliberately minimal — identity plus the display label
// — and NOT a copy of the definition. A stored schema would be a second
// description of an executor, free to contradict the code, which is exactly
// what migration 0012 refused to create.

// ReferenceKind names the family a reference belongs to.
//
// One value today, and only one, because only one family actually exists:
// the tools an agent is authorized to use. Sources and Memory are
// deliberately absent — both already enter a turn through the Context
// Builder under their own budget, and selecting one for a turn would be a
// new override semantics rather than the scoping this type describes.
//
// A constant with no producer is a promise the code does not keep, so the
// set grows when a family does, not before.
type ReferenceKind string

const (
	// ReferenceKindTool attaches ONE capability by its canonical name.
	//
	// Still the family the record is written in — see the note on
	// ReferenceKindIntegration — and still what an audit reads.
	ReferenceKindTool ReferenceKind = "tool"

	// ReferenceKindIntegration attaches every capability of one PROVIDER
	// that this agent is already authorized to use.
	//
	// ── Why the wire gained a family and the record did not ────────────
	// Because they answer different questions. "GitHub" is what the person
	// chose; the seven capabilities that were in scope is what the turn
	// actually had. A record that stored only "GitHub" would be answering
	// the first question and would silently change its answer to the second
	// one the day an eighth GitHub tool ships — an old turn would start
	// looking as though it could have used a capability that did not exist
	// when it ran.
	//
	// So this kind is accepted on the way in and EXPANDED: what gets frozen
	// onto the message is one ReferenceKindTool per capability that was
	// really exposed. The interface groups them back together for display,
	// from the namespace, which is derivation and not a second record.
	//
	// The ID is a namespace (`github`), not a tool name. It grants nothing:
	// the expansion intersects with the grants, so a provider whose
	// capabilities are all unauthorized selects nothing and is refused —
	// exactly as naming an unauthorized tool is.
	ReferenceKindIntegration ReferenceKind = "integration"
)

func (k ReferenceKind) Valid() bool {
	return k == ReferenceKindTool || k == ReferenceKindIntegration
}

func (k ReferenceKind) String() string { return string(k) }

// MaxTurnReferences bounds one turn's selection.
//
// Sixteen. The ceiling is not about screen space — it is the same rule as
// every other bound in this module: a list the client controls is a list an
// operator cannot budget for. A selection is drawn from what the agent is
// authorized to use, which is a handful of capabilities; sixteen is far
// more than any real turn selects and small enough that a malformed request
// is refused rather than walked.
const MaxTurnReferences = 16

// maxReferenceLabelLength bounds the frozen display label. Tool titles are
// a few words; this is the ceiling that keeps a hostile or buggy producer
// from turning a snapshot into a payload.
const maxReferenceLabelLength = 120

// TurnReference is one capability the user attached to one turn.
//
// ID is the canonical, machine-readable identity — for a tool, its
// ToolName. It is what a later reader compares, and it is never a display
// string: the label may be rewritten by a deploy, the identity may not.
type TurnReference struct {
	Kind ReferenceKind `json:"kind"`
	ID   string        `json:"id"`
	// Label is how it read at the moment it was selected. Written by the
	// backend from its own registry, never taken from the request — a client
	// that could name a capability could otherwise also name it whatever it
	// liked in the permanent record.
	Label string `json:"label"`
}

func (r TurnReference) Validate() error {
	if !r.Kind.Valid() {
		return ReferenceRejected(CodeReferenceKindUnknown,
			"reference kind "+quoteForMessage(r.Kind.String())+" is not a kind this system knows")
	}
	if strings.TrimSpace(r.ID) == "" {
		return ReferenceRejected(CodeReferenceInvalid, "reference id is required")
	}
	if len(r.Label) > maxReferenceLabelLength {
		return ReferenceRejected(CodeReferenceInvalid, "reference label is too long")
	}
	return nil
}

// ValidateTurnReferences checks a whole selection.
//
// An empty selection is valid and means exactly what an absent one means:
// no explicit selection was made. The two are never told apart — see the
// note on SendMessageInput.References.
func ValidateTurnReferences(refs []TurnReference) error {
	if len(refs) > MaxTurnReferences {
		return ReferenceRejected(CodeReferenceInvalid,
			"a turn may carry at most "+itoa(MaxTurnReferences)+" references")
	}
	for _, r := range refs {
		if err := r.Validate(); err != nil {
			return err
		}
	}
	return nil
}

/* ── the refusals ────────────────────────────────────────────────────── */

// The machine-readable reasons a selection is refused. All four are
// KindInvalid and therefore 400: the turn is rejected before the question
// is persisted and before the provider is called, so nothing about the
// conversation changed and retrying the same body will fail the same way.
//
// Emphatically not 502 and not a silent downgrade to an ordinary turn: the
// interface said a capability was attached, and a turn that quietly ran
// without it would make that statement false while looking successful.
const (
	// CodeReferenceInvalid: malformed — no id, too many, label too long.
	CodeReferenceInvalid = "reference_invalid"
	// CodeReferenceKindUnknown: a family this build does not implement.
	// Told apart from the others so a client that is ahead of the server
	// learns that, rather than concluding the capability was deleted.
	CodeReferenceKindUnknown = "reference_kind_unknown"
	// CodeReferenceUnknownTool: no tool of that name exists in this build.
	CodeReferenceUnknownTool = "reference_unknown_tool"
	// CodeReferenceNotAuthorized: the tool exists and this agent may not use
	// it. Distinct from unknown for the same reason ToolErrNotAuthorized is
	// distinct from ToolErrNotFound — "it is not there" and "you may not
	// have it" call for different actions from whoever reads it.
	CodeReferenceNotAuthorized = "reference_not_authorized"
)

// ReferenceRejected refuses a turn over its selection.
func ReferenceRejected(code, msg string) *Error {
	return &Error{Kind: KindInvalid, Message: msg, Code: code}
}

// quoteForMessage renders an untrusted string inside an error sentence
// without letting it run on. The message is echoed back to the client that
// sent it, so it is bounded here rather than trusted.
func quoteForMessage(s string) string {
	const max = 40
	if len(s) > max {
		s = s[:max] + "…"
	}
	if s == "" {
		return `""`
	}
	return `"` + s + `"`
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
