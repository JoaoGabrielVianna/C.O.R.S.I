// Package domain is the Palace bounded context: the operator's persistent
// memory and the structures that organise it.
//
// ══════════════════════════════════════════════════════════════════════
//
//	Palace Memory is not Agent Memory. Palace Source is not Agent Source.
//
// ══════════════════════════════════════════════════════════════════════
//
// There are two unrelated things in this system called Memory and two
// called Source, and confusing them is the single most likely mistake a
// future session can make here. It is the same shape of mistake that
// `threads` versus `meta_threads` already carries a warning for.
//
//	chat.Memory        The AGENT's memory. A short fact one agent was told
//	                   to keep between conversations, scoped to THAT agent
//	                   and injected into the prompt under a token budget.
//	                   It is configuration of an assistant's behaviour.
//
//	palace.Memory      THIS. The OPERATOR's knowledge. A durable fact,
//	                   decision, preference or reflection about their own
//	                   life and work, scoped to the WORKSPACE, read on
//	                   demand and never injected by budget.
//
//	chat.Source        Reference material an agent may consult, spent
//	                   against the context budget.
//
//	palace.Source      THIS. EVIDENCE. The raw text, transcript or event a
//	                   palace memory was derived FROM. It answers "why do
//	                   I believe this", not "what may the agent read".
//
// Nothing converts one into the other, in either direction, and adding a
// mechanism that did would collapse a distinction the whole design rests
// on. Nothing in this package imports, names or knows about Agents, Chat,
// conversations or any agent.
//
// ── Where the intelligence is not ──────────────────────────────────────
// Here. This package holds STATE and the rules that keep it well formed.
// It does not decide what deserves to be remembered, does not summarise,
// does not rank and does not consolidate. When a person says "guarda que
// eu decidi parar de aceitar reunião antes das dez", the agent composes
// the sentence and this context stores the result, with the kind and the
// importance it was told. A domain that judged what mattered would be a
// domain with an opinion about somebody's life.
//
// ── What is deliberately absent ────────────────────────────────────────
// No embedding, no vector, no similarity, no ranking model, no reminder,
// no scheduler, no person entity and no notion of sharing. Each is a
// decision this cut did not make.
//
// This package imports nothing from the platform and nothing from any
// other module. It is plain data and the rules over it.
package domain

import (
	"strconv"
	"strings"
)

/* ── the lifecycle every durable entity shares ───────────────────────── */

// Lifecycle is whether something is in use or has been retired.
//
// ── Why one type and not RoomStatus, ArtifactStatus, MemoryStatus ──────
// Because the three would be identical: the same two words, the same
// meaning, the same parser, written three times. A vocabulary repeated
// across files is a vocabulary that drifts, and the drift shows up as one
// entity accepting a spelling the others reject.
//
// The day one of them genuinely needs a third state, this type splits
// into the one that grew and the ones that did not. That is a smaller
// change than keeping three identical parsers honest in the meantime.
//
// ── Why archived and not deleted ───────────────────────────────────────
// Archiving is the ORGANISING act, and it is the only one this context
// offers. "Not this, for now" and "never this" are different sentences,
// and a system that only has the second one makes people keep clutter
// because the alternative is destruction.
type Lifecycle string

const (
	// LifecycleActive: in use.
	LifecycleActive Lifecycle = "active"
	// LifecycleArchived: retired and kept. Still readable by id, still
	// the endpoint of every relation it had.
	LifecycleArchived Lifecycle = "archived"
)

// Lifecycles is the vocabulary in its natural order.
//
// The same two names appear in the CHECK constraints of
// migrations/palace/0001_init.up.sql. That is not two sources of truth:
// this package is the only copy that VALIDATES, and the constraints are
// backstops that refuse a row this package would never build.
var Lifecycles = []Lifecycle{LifecycleActive, LifecycleArchived}

// DefaultLifecycle is where anything new lands when nobody said.
const DefaultLifecycle = LifecycleActive

func (l Lifecycle) Valid() bool {
	return l == LifecycleActive || l == LifecycleArchived
}

func (l Lifecycle) String() string { return string(l) }

// Archived is a named question rather than a comparison spelled out at
// every call site.
func (l Lifecycle) Archived() bool { return l == LifecycleArchived }

// LifecycleNames is the vocabulary as plain strings, for a schema
// description or an error message. Built from the same slice, so a state
// added later cannot be added to the enum and forgotten in what the model
// is told.
func LifecycleNames() []string { return names(Lifecycles) }

// ParseLifecycle turns caller input into a lifecycle state.
//
// Lenient about case and surrounding space, strict about everything else.
// The callers are a person and a model, and both will send "Archived" or
// " active ". What it will NOT do is guess: "archive" is not "archived",
// and an approximate match here would retire something nobody asked to
// retire.
func ParseLifecycle(raw string) (Lifecycle, error) {
	l := Lifecycle(fold(raw))
	if !l.Valid() {
		return "", Invalid("unknown status %s; the statuses are %s",
			quoteToken(raw), strings.Join(LifecycleNames(), ", "))
	}
	return l, nil
}

/* ── what a relation may point at ────────────────────────────────────── */

// EntityType names the kinds of thing a Relation can connect.
//
// ── Why Source is not one of them ──────────────────────────────────────
// Because provenance is not a relation. A memory's evidence is stored in
// `palace.memory_sources`, a typed table where the database guarantees
// both ends exist and belong to the same workspace. Relations are
// polymorphic and therefore cannot carry a foreign key of any sort, so
// putting the one link with a hard integrity requirement in there would
// mean a fabricated source id inserting cleanly and pointing at nothing.
// One mechanism, not two. See relation.go.
//
// ── Why ArtifactItem is not one of them either ─────────────────────────
// An item has no identity outside the artifact that holds it. Nothing has
// asked to relate to one, and a relation to a checklist line would be a
// link that outlives its own subject.
type EntityType string

const (
	EntityRoom     EntityType = "room"
	EntityArtifact EntityType = "artifact"
	EntityMemory   EntityType = "memory"
)

var EntityTypes = []EntityType{EntityRoom, EntityArtifact, EntityMemory}

func (e EntityType) Valid() bool {
	switch e {
	case EntityRoom, EntityArtifact, EntityMemory:
		return true
	}
	return false
}

func (e EntityType) String() string { return string(e) }

func EntityTypeNames() []string { return names(EntityTypes) }

func ParseEntityType(raw string) (EntityType, error) {
	e := EntityType(fold(raw))
	if !e.Valid() {
		return "", Invalid("unknown entity type %s; the types are %s",
			quoteToken(raw), strings.Join(EntityTypeNames(), ", "))
	}
	return e, nil
}

/* ── the shared parsing helpers ──────────────────────────────────────── */

// stringer is what every closed vocabulary in this package satisfies. It
// exists so `names` can be written once instead of once per enum.
type stringer interface{ ~string }

// names renders a vocabulary as plain strings, in declaration order.
func names[T stringer](vals []T) []string {
	out := make([]string, len(vals))
	for i, v := range vals {
		out[i] = string(v)
	}
	return out
}

// joinNames renders a vocabulary into an error message. It is a thin
// wrapper over strings.Join so that files stating a rule do not each
// import strings for one call.
func joinNames(vals []string) string { return strings.Join(vals, ", ") }

// fold normalises caller input before it is compared against a
// vocabulary: trimmed and lowercased, and nothing else. In particular it
// does not strip punctuation or collapse inner space, because a token
// that needed either was not a token.
func fold(raw string) string { return strings.ToLower(strings.TrimSpace(raw)) }

// maxEchoedToken bounds how much of a rejected value an error message may
// quote back.
//
// ── Why an error message needs a bound at all ──────────────────────────
// Because the callers are a person and a model, and a model that
// misunderstands a schema will eventually put a paragraph where a word
// belongs. Every error this package produces reaches `chat.tool_calls`
// as `error_message` and travels back to the model, and this context
// stores things the operator would not want copied anywhere. Quoting the
// first forty characters is enough to show what was wrong with the token;
// quoting all of it would be a channel through which content leaks out of
// the one place that is supposed to hold it.
//
// See errors.go for the rule this serves.
const maxEchoedToken = 40

// quoteToken renders a rejected value for an error message, bounded.
func quoteToken(raw string) string {
	r := []rune(strings.TrimSpace(raw))
	if len(r) > maxEchoedToken {
		return strconv.Quote(string(r[:maxEchoedToken]) + "…")
	}
	return strconv.Quote(string(r))
}
