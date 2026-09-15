// Package ports declares the driven side of the Palace context.
//
// ══════════════════════════════════════════════════════════════════════
//
//	EVERY OPERATION TAKES A WORKSPACE, AND IT IS THE FIRST PARAMETER
//
// ══════════════════════════════════════════════════════════════════════
//
// Not a field read off the entity, not a value the implementation looks
// up, not something a caller is trusted to have checked. An argument, in
// the same position, on every method, including the ones that also carry
// an entity that happens to know its own workspace.
//
// ── Why the redundancy is deliberate ───────────────────────────────────
// `Create(ctx, workspaceID, r)` names the workspace twice: once as the
// argument and once inside `r.WorkspaceID`. That looks like a smell and
// is the safer arrangement, because the alternative is a signature in
// which the workspace is INVISIBLE at the call site. A service that
// loaded an entity for one workspace and saved it while serving another
// would compile, read fine, and file somebody's private memory in a
// stranger's palace. With the argument present, the two can be compared,
// and the implementations do compare them: see adapters/repo, where a
// mismatch is a loud failure rather than a silent write.
//
// The rule the implementations owe, and it is not negotiable: the
// workspace predicate is in the SQL. Not in a check afterwards, not in a
// filter over the result, not in a comment. An id that arrives from a
// model must not be able to select or move a row it does not own, and the
// only way to guarantee that is for the WHERE clause to say so.
//
// ── One answer for three different absences ────────────────────────────
// A row that never existed, a row that was deleted, and a row that
// belongs to another workspace are ONE answer: `domain.NotFound`. Telling
// them apart would let a fabricated id be used to confirm the existence
// of another workspace's record, which is the only thing an attacker
// without access actually needs.
package ports

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/palace/domain"
)

/* ── the clock ───────────────────────────────────────────────────────── */

// Clock is the authority on the current instant for this bounded context.
//
// ── Why a port, for something a process can read locally ───────────────
// The same reason Finance has one, and Palace inherits the argument
// rather than re-deriving it: `created_at` and `updated_at` on every
// Palace row are stamped by Postgres, and a `Session.last_activity_at`
// or a resolved "hoje" computed from THIS process's clock would be a
// second authority. Two clocks only have to disagree by a fraction of a
// second for a record written a moment ago to sort into the future, and
// nothing errors when they do.
//
// ── What reads it in this slice, stated honestly: nothing ──────────────
// Room and Memory carry exactly two timestamps that the system sets, and
// both are stamped inside the SQL that writes the row, which is already
// the database's clock and is strictly better than passing one in. The
// port exists and is exercised because the surfaces that DO need to stamp
// a moment (a session's activity, a tool resolving a relative date) must
// find one authority already here rather than reaching for time.Now the
// day they arrive.
type Clock interface {
	Now(ctx context.Context) (time.Time, error)
}

/* ── the shared shape of a listing ───────────────────────────────────── */

// Page bounds a listing. Zero means the repository's default; a limit
// above the repository's ceiling is lowered to it rather than refused,
// because a caller asking for too much wants as much as it can have.
type Page struct {
	Limit  int
	Offset int
}

// Sensitive is the opt-in that governs what a listing may show.
//
// ══════════════════════════════════════════════════════════════════════
//
//	NORMAL AND PRIVATE ARE VISIBLE. HIGHLY_SENSITIVE IS NOT, UNLESS ASKED
//
// ══════════════════════════════════════════════════════════════════════
//
// ── Why the field is named after the level it admits ───────────────────
// Because `include_sensitive` would be a lie about two thirds of the
// vocabulary. `private` content is sensitive and IS visible to its own
// workspace by default: hiding it would make most of this context
// invisible to the capability that exists to search it, and would push
// every genuinely private note up to `highly_sensitive` just to be
// labelled, until the level that hides things stopped meaning anything.
//
// The flag admits exactly one level, so it is named after that level. A
// caller reading `IncludeHighlySensitive` knows what it is turning on
// without opening the sensitivity vocabulary.
//
// ── Why false is the zero value ────────────────────────────────────────
// The safe answer is the one a caller gets by saying nothing. A filter
// built by a future surface that forgot this field withholds the most
// private content rather than publishing it.
//
// ── What it does NOT do ────────────────────────────────────────────────
// It does not gate `FindByID`. A caller holding an id already knows the
// row exists, and refusing to return it would turn a direct read into a
// puzzle while protecting nothing: the id came from somewhere the caller
// was allowed to look. The rule is about BROAD listings, which is where
// content enters a context nobody asked for it to be in.
type Sensitive struct {
	IncludeHighlySensitive bool
}

/* ── rooms ───────────────────────────────────────────────────────────── */

// RoomFilter narrows a listing of rooms.
//
// ── Why these fields and not a query language ──────────────────────────
// They are the questions actually asked: "quais salas eu tenho", "o que
// eu arquivei", "aquela de carreira". A general filter grammar would be a
// search engine, which this sprint is explicitly not building, and every
// field here maps to an index that exists.
//
// The zero value lists the workspace's live, non-highly-sensitive rooms,
// most recently touched first.
type RoomFilter struct {
	// Status narrows to one lifecycle state. Nil means both, archived
	// included: a listing that quietly hid retired rooms would make "onde
	// é que eu guardei aquilo" unanswerable.
	Status *domain.Lifecycle
	// Search matches the name or the description, case-insensitively and
	// by substring. Substring rather than exact because the caller is
	// usually a model working from "aquela sala de carreira", and
	// requiring the stored wording would make a correct question fail.
	Search string

	Sensitive
	Page
}

type RoomRepo interface {
	// List returns matching rooms, most recently updated first.
	List(ctx context.Context, workspaceID uuid.UUID, f RoomFilter) ([]*domain.Room, error)
	// Count is how many rows the same filter matches, ignoring Limit and
	// Offset, so a truncated listing can say how much it is not showing.
	//
	// It applies the SAME sensitivity rule as List. A count that included
	// withheld rows would report "23 salas" above a list of 22 and
	// announce the existence of the one being withheld, which is the leak
	// the withholding exists to prevent.
	Count(ctx context.Context, workspaceID uuid.UUID, f RoomFilter) (int64, error)
	// FindByID returns one live room, or a not-found error. The workspace
	// is part of the lookup, never a check afterwards.
	FindByID(ctx context.Context, workspaceID, id uuid.UUID) (*domain.Room, error)
	// Exists answers whether this workspace has this room, without
	// reading it.
	//
	// ── Why this is not just FindByID with the result thrown away ──────
	// Because the caller is resolving a REFERENCE: the application service
	// checking that a memory may point at a room needs one bit, and
	// loading the room would pull its name and description into a code
	// path that has no business holding them. A row read is a row that can
	// be logged by accident.
	Exists(ctx context.Context, workspaceID, id uuid.UUID) (bool, error)
	// StatusOf answers whether this room is active or archived, without
	// reading it.
	//
	// ── Why this is not Exists with an extra field ─────────────────────
	// Because the two answer different questions and the callers are
	// different. A CONTEXTUAL reference (filing a memory in a room) cares
	// only that the room is this workspace's; a RELATION endpoint must
	// also be live, because drawing a fresh line to something retired is
	// either a mistake or a sign it should be active again. Collapsing
	// them would force one caller to ignore half the answer.
	//
	// A miss is not-found, on the same terms as FindByID.
	StatusOf(ctx context.Context, workspaceID, id uuid.UUID) (domain.Lifecycle, error)
	// Create writes a new room. The workspace argument is the authority;
	// see the package note.
	Create(ctx context.Context, workspaceID uuid.UUID, r *domain.Room) error
	// Update writes the mutable fields of a room.
	//
	// It takes the already-changed entity because the domain decided what
	// moved: see Room.Apply. A repository that took a RoomChange and built
	// its own SET clause would be a second place that knows what editing a
	// room means, and the two would disagree the first time a field was
	// added to one of them.
	Update(ctx context.Context, workspaceID uuid.UUID, r *domain.Room) error
}

/* ── memories ────────────────────────────────────────────────────────── */

// MemoryFilter narrows a listing of memories.
//
// The zero value lists the workspace's live, non-highly-sensitive
// memories, most recently touched first.
type MemoryFilter struct {
	// Kind narrows to one sort of knowledge. Nil means every kind.
	Kind *domain.MemoryKind
	// Status narrows to one lifecycle state. Nil means both.
	Status *domain.Lifecycle
	// RoomID narrows to what was filed in one room. Nil means everywhere,
	// including the memories that were never filed.
	RoomID *uuid.UUID
	// MinImportance is a floor, not a sort. Zero means no floor.
	//
	// ── Why a floor and not an ordering ────────────────────────────────
	// Because "o que realmente importa" is a question about the whole
	// matching set, and ordering by importance would bury the thing
	// written this morning under a four-year-old five. The listing stays
	// in recency order, which is what a person means by "o que eu
	// guardei", and the floor is how they narrow it.
	MinImportance int
	// Search matches the content or the summary, case-insensitively and
	// by substring.
	Search string

	Sensitive
	Page
}

type MemoryRepo interface {
	List(ctx context.Context, workspaceID uuid.UUID, f MemoryFilter) ([]*domain.Memory, error)
	// Count applies the same sensitivity rule as List, for the reason
	// RoomRepo.Count gives.
	Count(ctx context.Context, workspaceID uuid.UUID, f MemoryFilter) (int64, error)
	FindByID(ctx context.Context, workspaceID, id uuid.UUID) (*domain.Memory, error)
	// StatusOf answers whether this memory is active or archived, without
	// reading it. See RoomRepo.StatusOf.
	StatusOf(ctx context.Context, workspaceID, id uuid.UUID) (domain.Lifecycle, error)
	// KindOf answers what sort of knowledge this is, without reading it.
	//
	// The narrow read the decision_for rule needs: whether a memory may
	// be the origin of that relation depends on one word, and loading the
	// memory to find out would pull the operator's sentence into a path
	// whose output is a yes or a no.
	KindOf(ctx context.Context, workspaceID, id uuid.UUID) (domain.MemoryKind, error)
	Create(ctx context.Context, workspaceID uuid.UUID, m *domain.Memory) error
	Update(ctx context.Context, workspaceID uuid.UUID, m *domain.Memory) error
}

/* ── artifacts ───────────────────────────────────────────────────────── */

// ArtifactFilter narrows a listing of artifacts.
//
// The zero value lists the workspace's live, non-highly-sensitive
// artifacts, most recently touched first.
type ArtifactFilter struct {
	// Kind narrows to one sort of object. Nil means every kind.
	Kind *domain.ArtifactKind
	// Status narrows to one lifecycle state. Nil means both.
	Status *domain.Lifecycle
	// RoomID narrows to what was filed in one room. Nil means everywhere,
	// including the artifacts that were never filed.
	RoomID *uuid.UUID
	// Search matches the title or the body, case-insensitively and by
	// substring.
	Search string

	Sensitive
	Page
}

type ArtifactRepo interface {
	List(ctx context.Context, workspaceID uuid.UUID, f ArtifactFilter) ([]*domain.Artifact, error)
	Count(ctx context.Context, workspaceID uuid.UUID, f ArtifactFilter) (int64, error)
	FindByID(ctx context.Context, workspaceID, id uuid.UUID) (*domain.Artifact, error)
	// Exists answers whether this workspace has this artifact, without
	// reading it. Used to resolve a REFERENCE, where the caller wants one
	// bit and has no business holding the title and the body. See
	// RoomRepo.Exists.
	Exists(ctx context.Context, workspaceID, id uuid.UUID) (bool, error)
	// KindOf answers what sort of artifact this is, without reading it.
	//
	// ── Why this is not Exists with an extra field ─────────────────────
	// Because it answers a different question and one caller needs
	// exactly it: deciding whether an entry may be added depends on the
	// kind and on nothing else, and `AcceptsItems` is the rule. Loading
	// the artifact to read one word would pull its body into a path whose
	// only job is a yes or no.
	//
	// A miss is not-found, on the same terms as FindByID.
	KindOf(ctx context.Context, workspaceID, id uuid.UUID) (domain.ArtifactKind, error)
	// StatusOf answers whether this artifact is active or archived,
	// without reading it. See RoomRepo.StatusOf for why it is separate
	// from Exists.
	StatusOf(ctx context.Context, workspaceID, id uuid.UUID) (domain.Lifecycle, error)
	Create(ctx context.Context, workspaceID uuid.UUID, a *domain.Artifact) error
	// Update writes the mutable fields. It does NOT write `kind`: the
	// kind is decided at creation and the domain has no Change field for
	// it. See ArtifactChange.
	Update(ctx context.Context, workspaceID uuid.UUID, a *domain.Artifact) error
}

/* ── artifact items ──────────────────────────────────────────────────── */

// ItemRepo is the structured state of an artifact.
//
// ══════════════════════════════════════════════════════════════════════
//
//	AN ENTRY IS ADDRESSED BY WORKSPACE, ARTIFACT AND ID. ALL THREE
//
// ══════════════════════════════════════════════════════════════════════
//
// Every method takes the artifact as well as the entry, and every query
// puts both in the WHERE clause. The id alone would be enough to find the
// row, and that is precisely the problem: a caller holding an entry id
// from one checklist could tick a box on another, or on a stranger's,
// and the statement would succeed.
//
// The cost is one extra predicate. What it buys is that an entry id from
// the wrong artifact is not-found, indistinguishable from one that never
// existed, which is the same answer every other absence in this context
// gives.
type ItemRepo interface {
	// ListByArtifact returns one artifact's live entries, in position
	// order, ties broken by id.
	ListByArtifact(ctx context.Context, workspaceID, artifactID uuid.UUID, p Page) ([]*domain.ArtifactItem, error)
	CountByArtifact(ctx context.Context, workspaceID, artifactID uuid.UUID) (int64, error)
	FindByID(ctx context.Context, workspaceID, artifactID, itemID uuid.UUID) (*domain.ArtifactItem, error)
	// Create writes a new entry.
	//
	// position is the requested slot, or nil to append after the last
	// live entry of this artifact. The entry's own Position field is
	// IGNORED on the way in and filled from what was written on the way
	// out, because appending is resolved inside the INSERT: two callers
	// reading the current maximum and then writing it would both write
	// the same number.
	Create(ctx context.Context, workspaceID, artifactID uuid.UUID, item *domain.ArtifactItem, position *int) error
	Update(ctx context.Context, workspaceID, artifactID uuid.UUID, item *domain.ArtifactItem) error
	// SoftDelete removes an entry from the checklist without destroying
	// it.
	//
	// Soft, unlike every other `deleted_at` in this schema, because this
	// one IS written in v1: taking a line off a list is ordinary use. It
	// is still not a hard delete, because an entry may be the subject of
	// a memory tomorrow.
	SoftDelete(ctx context.Context, workspaceID, artifactID, itemID uuid.UUID) error
}

/* ── sources ─────────────────────────────────────────────────────────── */

// SourceRepo stores evidence.
//
// ══════════════════════════════════════════════════════════════════════
//
//	THERE IS NO Update, AND ITS ABSENCE IS THE MECHANISM
//
// ══════════════════════════════════════════════════════════════════════
//
// A source answers "what was actually said or seen". Evidence that can be
// rewritten after the fact answers nothing: the moment a transcript can
// be corrected in place, no memory resting on it can be audited, because
// the thing it rested on is gone.
//
// This is not enforced by a check somebody could forget to write. It is
// enforced by there being no method: nothing above this interface can
// reach a statement that edits a source, because no such statement is
// exposed. A corrected transcript is a NEW source, and the memory can
// cite both.
//
// The same goes for its sensitivity, deliberately. Relabelling evidence
// is a real need and the day it is asked for it gets its own narrow
// operation, which can then be reasoned about on its own: it would move
// the floor under every memory already citing it, and that is a decision,
// not a field update.
type SourceRepo interface {
	FindByID(ctx context.Context, workspaceID, id uuid.UUID) (*domain.Source, error)
	// SensitivityOf answers how withheld one source is, without reading
	// it.
	//
	// The narrow read the provenance path needs: establishing the floor
	// under a memory depends on the source's level and on nothing else,
	// and loading a transcript to read one word would put it in a code
	// path that has no use for it. Same argument as ArtifactRepo.KindOf.
	SensitivityOf(ctx context.Context, workspaceID, id uuid.UUID) (domain.Sensitivity, error)
	Create(ctx context.Context, workspaceID uuid.UUID, s *domain.Source) error
}

/* ── provenance ──────────────────────────────────────────────────────── */

// ProvenanceRepo is the memory_sources table: which evidence a memory
// rests on.
//
// ── Why the interface is named for the concept ─────────────────────────
// Because "memory source" reads like a field on a memory, and this is not
// that. It is the historical record of what a conclusion was drawn from,
// and every method here exists to serve that question.
//
// ── Append-only in v1, with no Unlink and no Update ────────────────────
// Enforced the same way SourceRepo enforces immutability: by the absence
// of a method. See domain/provenance.go for why the record's value is
// that it is not routinely tidied, and for the explicit note that this
// is a Palace Core v1 scope decision: a deliberate, named correction may
// be added later for the case where the wrong evidence was cited.
type ProvenanceRepo interface {
	// Link records that a memory rests on a source.
	//
	// Idempotent, and it says which happened: created is false when the
	// link was already there. The primary key is (memory_id, source_id),
	// so a repeat cannot duplicate a row, and reporting the difference is
	// what lets a caller say "já estava registrado" instead of claiming
	// work it did not do.
	Link(ctx context.Context, workspaceID, memoryID, sourceID uuid.UUID) (created bool, err error)
	// SourcesFor returns the evidence behind one memory, oldest link
	// first.
	//
	// ── Why there is no sensitivity filter here ────────────────────────
	// Because there cannot be anything to withhold. Every link passes the
	// floor rule, so a memory is always at least as withheld as every
	// source behind it; a caller that reached the memory has already been
	// admitted to something at least as restricted. Filtering would
	// withhold rows that are by construction less sensitive than the
	// thing the caller is already holding.
	SourcesFor(ctx context.Context, workspaceID, memoryID uuid.UUID) ([]*domain.Source, error)
	// SourceSensitivities returns the distinct levels of the evidence
	// behind one memory, at most three rows.
	//
	// ── Why it returns levels rather than the floor ────────────────────
	// Because the ORDER is a domain rule. A `CASE WHEN sensitivity = …`
	// in SQL would be a second copy of it, free to disagree the day a
	// fourth level exists. The repository reports what it found;
	// domain.MaxSensitivity decides which is highest.
	SourceSensitivities(ctx context.Context, workspaceID, memoryID uuid.UUID) ([]domain.Sensitivity, error)
}

/* ── sessions ────────────────────────────────────────────────────────── */

// SessionRepo stores the short-term working context.
//
// ══════════════════════════════════════════════════════════════════════
//
//	AT MOST ONE OPEN SESSION PER WORKSPACE, AND IT IS A v1 RESTRICTION
//
// ══════════════════════════════════════════════════════════════════════
//
// The partial unique index `sessions_one_open_idx` is what makes "the
// current working context" a question with one answer: there is no tie
// to break, so a read needs no ordering rule that somebody would have to
// keep consistent across surfaces.
//
// It is NOT a permanent property of the concept. Concurrent channels are
// a real direction, and they need independent sessions: two channels
// sharing one focus would each silently redirect the other. Relaxing it
// means dropping the index and giving a Session a channel identity.
// Nothing here may be built on "there is exactly one session" as though
// it were a truth about the domain.
//
// ── There is no List and no FindByID ───────────────────────────────────
// v1 answers one question about sessions: what is open now. A history of
// closed sessions is a real feature with real questions attached (what
// is it for, how far back, what does it show) and none of them have been
// asked. The rows are kept, so the reads can be added; offering them
// before anybody wants them would be a surface nobody is testing.
type SessionRepo interface {
	// FindOpen returns the workspace's open session, or not-found when
	// there is none.
	//
	// Not-found rather than a nil session, on the same terms as every
	// other read here: absence is one answer, and the caller above
	// decides whether it is an error or an ordinary state.
	FindOpen(ctx context.Context, workspaceID uuid.UUID) (*domain.Session, error)
	// StartOpen returns the workspace's open session, opening one if
	// there is none.
	//
	// ══════════════════════════════════════════════════════════════════
	//
	//	IT NEVER SURFACES A UNIQUE VIOLATION
	//
	// ══════════════════════════════════════════════════════════════════
	//
	// created is false when a session was already open, and the session
	// returned is that one. Two callers racing get the same session and
	// exactly one of them is told it created it.
	//
	// ── Why the implementation cannot be one statement ─────────────────
	// A plain insert would raise 23505 for the loser of the race, which
	// is a database error leaking a concurrency detail into a use case
	// that has a perfectly good answer. An `ON CONFLICT DO NOTHING`
	// returns no row instead of raising, which is better, and still not
	// enough on its own: under READ COMMITTED the rest of that statement
	// sees the snapshot from before the winner committed, so it cannot
	// read the row that was just created. The implementation therefore
	// reads, inserts, and on a conflict reads again in a FRESH
	// statement, which is the only way to see it.
	//
	// ── What it must NOT do ────────────────────────────────────────────
	// Touch `last_activity_at` when it merely found one. Finding out that
	// a session exists is not working in it.
	StartOpen(ctx context.Context, workspaceID uuid.UUID) (session *domain.Session, created bool, err error)
	// Update writes the mutable fields of a session: its focus, its
	// summary, its status and both clocks.
	//
	// It takes the already-changed entity because the domain decided what
	// moved and when, exactly as every other Update here does. See
	// Session.Apply and Session.Close.
	Update(ctx context.Context, workspaceID uuid.UUID, s *domain.Session) error
}

/* ── relations ───────────────────────────────────────────────────────── */

// RelationDirection selects which end of a relation an anchor sits at.
//
// The zero value is both, because "what is connected to this" is the
// question people actually ask; direction is the refinement.
type RelationDirection string

const (
	// DirectionAny matches an anchor at either end.
	DirectionAny RelationDirection = ""
	// DirectionFrom matches relations that START at the anchor.
	DirectionFrom RelationDirection = "from"
	// DirectionTo matches relations that POINT AT the anchor.
	DirectionTo RelationDirection = "to"
)

func (d RelationDirection) Valid() bool {
	switch d {
	case DirectionAny, DirectionFrom, DirectionTo:
		return true
	}
	return false
}

func (d RelationDirection) String() string {
	if d == DirectionAny {
		return "any"
	}
	return string(d)
}

// RelationAnchor is the entity a listing is about.
//
// ── Why a struct and not two nilable fields on the filter ──────────────
// Because a type without an id, or an id without a type, is meaningless:
// a uuid alone cannot say whether it names a memory or an artifact, and
// the two tables can legitimately carry the same value. Paired in one
// optional struct, the half-filled state cannot be built.
type RelationAnchor struct {
	Type domain.EntityType
	ID   uuid.UUID
}

// RelationFilter narrows a listing of relations.
//
// ══════════════════════════════════════════════════════════════════════
//
//	THIS IS AN ADJACENCY READ. IT IS NOT A GRAPH QUERY
//
// ══════════════════════════════════════════════════════════════════════
//
// It answers "which edges touch this node", in one statement, with no
// recursion. There is no depth, no path, no expansion and no recursive
// CTE behind it, and none should be added without a question that this
// shape cannot answer.
//
// ── Why that restraint is worth stating in a type comment ──────────────
// Because the cheap version of "show me what is related" is one join
// away from the expensive version, and the expensive version arrives by
// accident: somebody follows the results one level, then two, then adds
// a `depth` argument because the caller wanted it. At that point the
// listing is a traversal with an unbounded cost and an unbounded result,
// and it is the kind of read that quietly becomes the reason a page is
// slow. Following an edge is the CALLER's act, one listing at a time.
//
// The zero value lists every relation in the workspace, most recently
// created first.
type RelationFilter struct {
	// Anchor narrows to the relations touching one entity. Nil means
	// every relation in the workspace.
	Anchor *RelationAnchor
	// Direction refines where the anchor sits. Ignored when Anchor is
	// nil, because there is nothing for a direction to be relative to.
	Direction RelationDirection
	// Kind narrows to one sort of link. Nil means every kind.
	Kind *domain.RelationKind

	Page
}

// RelationRepo stores the controlled links between Palace entities.
//
// ── Why there is no Update ─────────────────────────────────────────────
// A relation carries no field worth editing: its whole content is two
// endpoints and a kind, and changing any of them makes it a different
// statement. A semantic change is Delete plus Create, which leaves both
// acts on the record instead of rewriting what was once asserted.
//
// ── Why Delete is physical, unlike everything else here ────────────────
// Because a relation is an assertion rather than a thing. Archiving
// exists so "not this, for now" can be said about content somebody made;
// an edge that turned out to be wrong is not content, it is a claim that
// should not have been filed, and keeping a tombstone for it would mean
// every reader having to know which edges are real. Relations get no
// lifecycle in v1 for the same reason.
//
// The entities at either end are NEVER touched by any of this. Removing
// an edge removes the edge.
type RelationRepo interface {
	List(ctx context.Context, workspaceID uuid.UUID, f RelationFilter) ([]*domain.Relation, error)
	Count(ctx context.Context, workspaceID uuid.UUID, f RelationFilter) (int64, error)
	FindByID(ctx context.Context, workspaceID, id uuid.UUID) (*domain.Relation, error)
	// Create writes the relation, or returns the one that was already
	// there.
	//
	// created is false when an identical relation existed. The unique
	// index over (workspace, from, kind, to) is what decides, in one
	// statement, so a repeat cannot duplicate a row and cannot race with
	// itself. Reporting the difference is what lets a caller say "já
	// estava registrado" instead of claiming work it did not do, and
	// returning the EXISTING row means the caller still gets an id it can
	// remove.
	Create(ctx context.Context, workspaceID uuid.UUID, r *domain.Relation) (created bool, err error)
	// Delete removes one edge, physically. Not-found when there was none,
	// so removing twice is distinguishable from removing once.
	Delete(ctx context.Context, workspaceID, id uuid.UUID) error
	// HasDecisionFor answers whether a memory is the ORIGIN of any
	// decision_for relation.
	//
	// The narrow read that guards reclassification: a memory that governs
	// something as a decision may not stop being a decision while it
	// does. One bit, no rows loaded, no join.
	HasDecisionFor(ctx context.Context, workspaceID, memoryID uuid.UUID) (bool, error)
}
