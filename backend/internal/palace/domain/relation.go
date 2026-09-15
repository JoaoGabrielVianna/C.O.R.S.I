package domain

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

// Relations: controlled links between Palace entities.
//
// ══════════════════════════════════════════════════════════════════════
//
//	WHAT IS DELIBERATELY NOT A RELATION
//
// ══════════════════════════════════════════════════════════════════════
//
// BELONGS_TO is absent. Belonging is a COLUMN: Artifact.RoomID,
// Memory.RoomID, Memory.ArtifactID. Two ways to say the same thing is the
// arrangement where, on the day they disagree, an artifact is in two
// rooms and no listing is right. It is the same reason Job Radar has a
// nullable `stage` instead of a stage beside an `is_tracked` boolean.
//
// DERIVED_FROM is absent. Provenance is `palace.memory_sources`, a typed
// table where the database guarantees both ends exist and belong to the
// same workspace. This table is polymorphic and therefore cannot carry a
// foreign key of any sort, so the one link with a hard integrity
// requirement, the answer to "why do I believe this", must not live here:
// a fabricated source id would insert cleanly and point at nothing.
//
// Both are additive to restore, and neither should be restored without a
// question that the columns cannot answer.
//
// ══════════════════════════════════════════════════════════════════════
//
//	NORMATIVE, FOR THE SLICE THAT IMPLEMENTS RELATION
//
// ══════════════════════════════════════════════════════════════════════
//
// Relation has no repository and no application service yet. Two
// decisions about it are settled, and they are recorded here so the slice
// that builds it inherits them rather than re-deciding them.
//
// ── 1. A decision that governs something cannot stop being a decision ──
// A Memory that is the ORIGIN of a live DECISION_FOR relation may NOT be
// reclassified to another MemoryKind while that relation exists. The
// reclassification is refused; the relation is untouched.
//
// Explicitly NOT allowed as the fix: cascading the delete, dropping the
// relation, converting it to RELATED_TO, or any other silent repair. The
// operator asked to change one thing, and a system that also destroyed a
// link they did not mention would be deciding on their behalf about a
// record of why something was done. The correction is two explicit steps:
// remove the relation, then reclassify.
//
// This is why MemoryChange keeps its Kind field. Nothing about the rule
// makes the kind immutable in general: it makes it immutable WHILE a
// particular relation stands, which is a fact about the graph and not
// about the memory. The check therefore belongs to the application
// service that can see both, and it cannot be written in this package,
// which loads nothing. See the note on MemoryChange.
//
// ── 2. Relation needs removal, and does not need update ────────────────
// The rule above is only livable if a relation can be taken back, so the
// Palace Core target includes:
//
//	palace.relation.create    write
//	palace.relation.list      read
//	palace.relation.remove    write
//
// and deliberately no `palace.relation.update`. A relation carries no
// field worth editing: its whole content is two endpoints and a kind, and
// changing any of them makes it a different statement. A semantic change
// is remove plus create, which leaves both acts on the record instead of
// rewriting what was once asserted.
//
// With this, the Palace Core target is 23 capabilities.

/* ── the kinds ───────────────────────────────────────────────────────── */

// RelationKind is what one entity says about another.
type RelationKind string

const (
	// RelationRelatedTo: an undirected association. The weakest link, and
	// the one to reach for when nothing more specific is true.
	RelationRelatedTo RelationKind = "related_to"
	// RelationDecisionFor: this decision governs that thing. Only a
	// Memory of kind `decision` may be its origin, which is the one rule
	// the matrix below cannot express on its own.
	RelationDecisionFor RelationKind = "decision_for"
	// RelationMentions: this names that. Weaker than related_to in
	// intent: it records an appearance, not a connection somebody drew.
	RelationMentions RelationKind = "mentions"
	// RelationSupersedes: this replaces that. The only kind that changes
	// what "current" means, which is why it may never be reflexive.
	//
	// ══════════════════════════════════════════════════════════════════
	//
	//	THE DIRECTION IS FROZEN: NEW SUPERSEDES OLD
	//
	// ══════════════════════════════════════════════════════════════════
	//
	//	from  =  the NEW thing, the replacement, what holds now
	//	to    =  the OLD thing, what it replaced
	//
	// Read aloud, the row is a sentence in that order:
	//
	//	Memory B "Backend será Go"  SUPERSEDES  Memory A "Talvez Python"
	//	         ^ from                                   ^ to
	//
	// ── Why this has to be frozen rather than inferred ─────────────────
	// Because both ends are the same type, so nothing about the row
	// reveals which way it was meant. A reader that guesses backwards
	// does not get an error: it gets a coherent, confident, inverted
	// history in which the abandoned option is the current decision. That
	// is the worst shape a bug can have here, and no test can catch it
	// after the fact because both readings are internally consistent.
	//
	// Timestamps do not settle it either. A memory written years ago can
	// supersede one written last week, when somebody finds the older
	// note and decides it was right all along.
	//
	// See Relation.Superseding, which is the one place any caller should
	// get these two ends from.
	//
	// ── What this does NOT do ──────────────────────────────────────────
	// Recording that B supersedes A does not archive A. The relation
	// states semantics; lifecycle stays an explicit act. Superseded work
	// is routinely kept active on purpose: the rejected option is part of
	// why the decision was made.
	RelationSupersedes RelationKind = "supersedes"
)

var RelationKinds = []RelationKind{
	RelationRelatedTo,
	RelationDecisionFor,
	RelationMentions,
	RelationSupersedes,
}

func (k RelationKind) Valid() bool {
	switch k {
	case RelationRelatedTo, RelationDecisionFor, RelationMentions, RelationSupersedes:
		return true
	}
	return false
}

func (k RelationKind) String() string { return string(k) }

func RelationKindNames() []string { return names(RelationKinds) }

func ParseRelationKind(raw string) (RelationKind, error) {
	k := RelationKind(fold(raw))
	if !k.Valid() {
		return "", Invalid("unknown relation kind %s; the kinds are %s",
			quoteToken(raw), strings.Join(RelationKindNames(), ", "))
	}
	return k, nil
}

/* ── the closed matrix ───────────────────────────────────────────────── */

// RelationShape is one permitted pairing: what may be on each end.
type RelationShape struct {
	From EntityType
	To   EntityType
}

func (s RelationShape) String() string { return string(s.From) + " → " + string(s.To) }

// relationMatrix is the complete list of pairings this context permits,
// per kind. A pairing absent from here is refused.
//
// ── Why a closed matrix and not "any type to any type" ─────────────────
// Because an open graph accepts every statement, including the ones that
// mean nothing, and it is a model that writes most of these. A room that
// supersedes a memory, an artifact that is a decision for a room: both
// are grammatical, neither is a fact, and once written they are
// indistinguishable from the real links when something reads the graph
// back. Refusing them at the point of writing is the only moment anyone
// still knows what was meant.
//
// The same matrix is spelled as a CHECK constraint in
// migrations/palace/0001_init.up.sql. This map is the only copy that
// VALIDATES; the constraint is a backstop that refuses a row this package
// would never build.
var relationMatrix = map[RelationKind][]RelationShape{
	RelationRelatedTo: {
		{From: EntityRoom, To: EntityRoom},
		{From: EntityArtifact, To: EntityArtifact},
		{From: EntityMemory, To: EntityMemory},
		{From: EntityMemory, To: EntityArtifact},
	},
	RelationDecisionFor: {
		{From: EntityMemory, To: EntityArtifact},
		{From: EntityMemory, To: EntityRoom},
	},
	RelationMentions: {
		{From: EntityMemory, To: EntityArtifact},
		{From: EntityArtifact, To: EntityArtifact},
	},
	RelationSupersedes: {
		{From: EntityMemory, To: EntityMemory},
		{From: EntityArtifact, To: EntityArtifact},
	},
}

// AllowedShapes returns the pairings a kind permits, for a schema
// description or an error message. The slice is rebuilt per call so a
// caller cannot reorder the matrix's own copy.
func AllowedShapes(k RelationKind) []RelationShape {
	src := relationMatrix[k]
	out := make([]RelationShape, len(src))
	copy(out, src)
	return out
}

// ShapeDescription renders a kind's pairings as one readable phrase. It
// is built from the matrix, so a pairing added later reaches the model's
// schema description the day it is added rather than whenever somebody
// remembers to edit a string.
func ShapeDescription(k RelationKind) string {
	shapes := relationMatrix[k]
	out := make([]string, len(shapes))
	for i, s := range shapes {
		out[i] = s.String()
	}
	return strings.Join(out, ", ")
}

// ShapeAllowed reports whether the matrix permits this exact pairing.
func ShapeAllowed(k RelationKind, from, to EntityType) bool {
	for _, s := range relationMatrix[k] {
		if s.From == from && s.To == to {
			return true
		}
	}
	return false
}

/* ── the entity ──────────────────────────────────────────────────────── */

// Relation is one controlled link.
//
// ── Why there is no foreign key behind these ids ───────────────────────
// Because the endpoints are polymorphic and Postgres cannot reference
// "one of three tables". The integrity the database cannot enforce is
// enforced one layer up: the application service resolves BOTH endpoints
// in the caller's workspace before inserting, and a row whose endpoint it
// could not resolve is never written. That resolution is also what stops
// a relation from being a way to confirm that another workspace's row
// exists.
type Relation struct {
	ID          uuid.UUID
	WorkspaceID uuid.UUID

	FromType EntityType
	FromID   uuid.UUID
	Kind     RelationKind
	ToType   EntityType
	ToID     uuid.UUID

	CreatedAt time.Time
}

// Validate checks everything about a relation that can be known without
// loading the entities it points at.
//
// The order of the checks is the order of the failures worth telling
// apart: the vocabularies first, so an unknown word is reported as an
// unknown word rather than as an impossible pairing; then the ids; then
// reflexivity; then the matrix.
func (r *Relation) Validate() error {
	if r.WorkspaceID == uuid.Nil {
		return Invalid("a workspace is required")
	}
	if !r.Kind.Valid() {
		return Invalid("unknown relation kind %s; the kinds are %s",
			quoteToken(string(r.Kind)), strings.Join(RelationKindNames(), ", "))
	}
	if !r.FromType.Valid() {
		return Invalid("unknown entity type %s for the origin; the types are %s",
			quoteToken(string(r.FromType)), strings.Join(EntityTypeNames(), ", "))
	}
	if !r.ToType.Valid() {
		return Invalid("unknown entity type %s for the target; the types are %s",
			quoteToken(string(r.ToType)), strings.Join(EntityTypeNames(), ", "))
	}
	if r.FromID == uuid.Nil {
		return Invalid("an origin id is required")
	}
	if r.ToID == uuid.Nil {
		return Invalid("a target id is required")
	}
	if r.FromType == r.ToType && r.FromID == r.ToID {
		// Nothing relates to itself, and supersedes especially: a row that
		// replaced itself would make "what is current" unanswerable.
		return Invalid("a relation cannot point at the thing it starts from")
	}
	if !ShapeAllowed(r.Kind, r.FromType, r.ToType) {
		return Invalid("a %s relation cannot go from %s to %s; the allowed shapes are %s",
			r.Kind, r.FromType, r.ToType, ShapeDescription(r.Kind))
	}
	return nil
}

// Superseding names the two ends of a SUPERSEDES relation.
//
// ── Why an accessor rather than "everyone knows from is the new one" ───
// Because both ends are the same type and nothing in the row says which
// is which. A caller that reads them in the wrong order renders an
// inverted history with total confidence and no error anywhere, so the
// direction is spent once, here, and every reader takes it from this
// function instead of re-deriving it from a field name.
//
// ok is false for any other kind: there is no newer and older end of a
// MENTIONS, and answering as though there were would invite exactly the
// misreading this exists to stop.
func (r Relation) Superseding() (newer, older uuid.UUID, ok bool) {
	if r.Kind != RelationSupersedes {
		return uuid.Nil, uuid.Nil, false
	}
	return r.FromID, r.ToID, true
}

/* ── the endpoint rules ──────────────────────────────────────────────── */

// ValidateEndpointAlive refuses an archived entity as the end of a NEW
// relation.
//
// ── Why archiving blocks the creation and not the reading ──────────────
// Archiving says "not this, for now". Drawing a fresh line to something
// that was retired is either a mistake or a sign that it should be
// active again, and both are better answered by a refusal than by a
// record nobody will be able to interpret later.
//
// Relations that ALREADY exist are untouched when an endpoint is
// archived: they stay listable, and nothing cascades. The history of
// what was connected is not invalidated by somebody tidying up, and a
// cascade would silently destroy the record that explains why the thing
// was archived in the first place.
//
// ── Why this is a domain function and not an if in the service ─────────
// So the wording and the rule live with the vocabulary they are about,
// and so the service reads as a sequence of named checks rather than as
// a pile of comparisons.
func ValidateEndpointAlive(entity EntityType, status Lifecycle) error {
	if !status.Valid() {
		return Invalid("the %s at one end carries an unrecognised status", entity)
	}
	if status.Archived() {
		return Invalid(
			"the %s at one end is %s, so no new relation may be drawn to it; "+
				"restore it to %s first",
			entity, LifecycleArchived, LifecycleActive)
	}
	return nil
}

// ValidateDecisionOrigin enforces the one rule the matrix cannot express
// alone: DECISION_FOR may only start at a Memory whose kind is decision.
//
// ── Why it is separate from Validate ───────────────────────────────────
// Because it needs the entity, and this package does not load anything.
// The application service resolves the origin in the caller's workspace
// anyway, since that is how isolation is enforced for a row with no
// foreign key, so this check costs no extra read. Calling it is therefore
// not an optional extra: the memory is already in hand.
//
// A kind other than decision_for is not this function's business, and it
// says so by returning nil rather than by insisting on being called only
// in the right case.
func (r Relation) ValidateDecisionOrigin(from *Memory) error {
	if r.Kind != RelationDecisionFor {
		return nil
	}
	if from == nil {
		return Invalid("a %s relation must have its origin memory resolved before it can be validated",
			RelationDecisionFor)
	}
	if from.Kind != MemoryDecision {
		return Invalid("a %s relation must start at a memory of kind %s, and this one is %s",
			RelationDecisionFor, MemoryDecision, from.Kind)
	}
	return nil
}
