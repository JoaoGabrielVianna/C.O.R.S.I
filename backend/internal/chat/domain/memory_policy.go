package domain

import "strings"

// Memory Policy: what an agent is allowed to be asked about its own memory.
//
// ── What it is, stated narrowly ────────────────────────────────────────
// A permission plus an addendum. It says whether this agent may be asked to
// read a conversation and propose what is worth remembering, and it carries
// whatever extra guidance the user wants that proposal to follow.
//
// ── What it is NOT ─────────────────────────────────────────────────────
// It is not a second system prompt, and it never reaches an ordinary turn.
// The rules about what deserves to be remembered — stable preferences,
// long-term goals, decisions, recurring constraints, and the things that do
// not qualify — are the product's, they ship in code, and a user editing
// their agent does not get to delete them. Notes are an addition to that
// base, never a replacement for it.
//
// Nothing in this version consumes the policy: the operation it governs is
// the next version's. What exists here is the configuration and its
// contract, so that operation has something to obey rather than something
// to invent.

// MemoryPolicyMode is the closed set of what an agent may be asked to do
// with its memory.
//
// Two values, and no automatic one. A mode the schema accepts and no code
// implements would be configuration describing behaviour that does not
// exist — the same reason the tool registry has no table of tools.
type MemoryPolicyMode string

const (
	// MemoryPolicyOff refuses the request outright. The agent is never
	// asked to look at a conversation, and no provider call is made.
	MemoryPolicyOff MemoryPolicyMode = "off"
	// MemoryPolicyOnRequest allows it, and only when the user asks. The
	// agent never starts on its own: there is no schedule, no background
	// pass, and no turn in which it decides to remember something.
	MemoryPolicyOnRequest MemoryPolicyMode = "on_request"
)

func (m MemoryPolicyMode) Valid() bool {
	return m == MemoryPolicyOff || m == MemoryPolicyOnRequest
}

func (m MemoryPolicyMode) String() string { return string(m) }

// DefaultMemoryPolicyMode is what an agent has when nobody has decided.
//
// Permission, not activity: `on_request` costs nothing until a person types
// a command, and the operation it permits is one they started. Defaulting
// to `off` would answer a deliberate request with a refusal nobody
// configured, which reads as a broken feature rather than as a policy.
const DefaultMemoryPolicyMode = MemoryPolicyOnRequest

// MaxMemoryPolicyNotes bounds the addendum.
//
// A thousand characters is a paragraph of guidance, not a document. The
// ceiling exists because these characters are destined for a prompt, and an
// unbounded field aimed at a prompt is an unbounded bill.
const MaxMemoryPolicyNotes = 1000

// MemoryPolicy is the agent's stance on proposing memories.
type MemoryPolicy struct {
	Mode MemoryPolicyMode `json:"mode"`
	// Notes is the user's addition to the policy the product ships. Empty
	// is the ordinary case and means the base rules stand alone.
	Notes string `json:"notes"`
}

// DefaultMemoryPolicy is what an agent gets when its creator said nothing
// about memory at all.
//
// A function rather than a method that fills in blanks, and the difference
// is load-bearing: "no policy was mentioned" is a decision to make once, at
// construction. "A policy was mentioned and its mode is empty" is a
// malformed request, and a defaulting method would quietly rescue it into
// something the client never asked for.
func DefaultMemoryPolicy() MemoryPolicy {
	return MemoryPolicy{Mode: DefaultMemoryPolicyMode}
}

// Proposes reports whether this agent may be asked to propose memories.
//
// A named question rather than a comparison spelled out at each call site:
// when a third mode exists, the callers that only care about "may it or
// not" do not have to be found and edited one by one.
func (p MemoryPolicy) Proposes() bool { return p.Mode == MemoryPolicyOnRequest }

func (p MemoryPolicy) Validate() error {
	if !p.Mode.Valid() {
		return Invalid("memory_policy.mode must be off or on_request")
	}
	if len([]rune(p.Notes)) > MaxMemoryPolicyNotes {
		return Invalid("memory_policy.notes must be <= 1000 chars")
	}
	return nil
}

// Normalize trims the addendum and nothing else. Guidance made only of
// whitespace is no guidance, and storing it would put invisible characters
// into a prompt. It does not fill in a missing mode — see
// DefaultMemoryPolicy for why that is a separate decision.
func (p MemoryPolicy) Normalize() MemoryPolicy {
	p.Notes = strings.TrimSpace(p.Notes)
	return p
}
