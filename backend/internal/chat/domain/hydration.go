package domain

// The four outcomes of asking a provider for a subject's present state.
//
// ── Why this is a closed vocabulary in the domain ──────────────────────
// Because three of the four are things the model is TOLD, in words, and the
// difference between them changes what it is allowed to say. "I could not
// check" and "it no longer exists" and "I am not allowed to check" are three
// different sentences to a user, and a runtime that collapsed them would
// either alarm somebody about a deleted record that is merely unauthorized,
// or reassure somebody about a record that is gone.
//
// It is also what the context report records as an exclusion reason, which
// is how an operator answers "did this turn have the current state?" from a
// stored row rather than from a log line that has since rotated away.
type HydrationStatus string

const (
	// HydrationCurrent: the provider answered and the fields are the
	// entity's state as of the start of this turn.
	HydrationCurrent HydrationStatus = "current"

	// HydrationUnauthorized: the capability the provider named for this type
	// is not granted to this agent.
	//
	// The reference stays, the subject stays recognisable, and NOTHING about
	// the entity's state is read — not by a tool, not by a resolver, not by
	// any other door. This is the status that carries the invariant: a
	// reference is not a capability, so attaching one to a conversation
	// cannot teach an agent something a revoked grant would have refused it.
	HydrationUnauthorized HydrationStatus = "unauthorized"

	// HydrationUnavailable: the provider looked and this workspace has no
	// such entity. Deleted, never existed, or someone else's — one status for
	// all three, the same rule Resolve follows, so a fabricated id learns
	// nothing from the difference.
	HydrationUnavailable HydrationStatus = "unavailable"

	// HydrationFailed: the provider could not be asked. A database that is
	// down, a timeout, an adapter fault.
	//
	// Told apart from Unavailable because they are opposite facts about the
	// entity: one says it is not there, the other says we do not know. The
	// model is told the second as "could not be read", never as "removed",
	// because announcing a deletion that did not happen is the worse of the
	// two mistakes by a wide margin.
	HydrationFailed HydrationStatus = "failed"
)

// Obtained reports whether this status carries actual state.
//
// The one place the distinction is derived, so a caller cannot decide that
// one of the three failures is "close enough" to current.
func (s HydrationStatus) Obtained() bool { return s == HydrationCurrent }

func (s HydrationStatus) String() string { return string(s) }
