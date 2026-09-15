package domain

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

// Artifacts: the living objects of the palace, the things that evolve.

/* ── the kinds ───────────────────────────────────────────────────────── */

// ArtifactKind is what an artifact IS.
//
// Four, and the list is closed. The same four names appear in the CHECK
// constraint of migrations/palace/0001_init.up.sql; this package is the
// only copy that validates.
type ArtifactKind string

const (
	// ArtifactProject: something being built, with an outcome in mind.
	ArtifactProject ArtifactKind = "project"
	// ArtifactList: a set of entries. Shopping, reading, candidates.
	ArtifactList ArtifactKind = "list"
	// ArtifactPlan: ordered steps toward something.
	ArtifactPlan ArtifactKind = "plan"
	// ArtifactNote: prose. The kind for something that is written rather
	// than decomposed.
	ArtifactNote ArtifactKind = "note"
)

var ArtifactKinds = []ArtifactKind{
	ArtifactProject,
	ArtifactList,
	ArtifactPlan,
	ArtifactNote,
}

func (k ArtifactKind) Valid() bool {
	switch k {
	case ArtifactProject, ArtifactList, ArtifactPlan, ArtifactNote:
		return true
	}
	return false
}

func (k ArtifactKind) String() string { return string(k) }

func ArtifactKindNames() []string { return names(ArtifactKinds) }

func ParseArtifactKind(raw string) (ArtifactKind, error) {
	k := ArtifactKind(fold(raw))
	if !k.Valid() {
		return "", Invalid("unknown artifact kind %s; the kinds are %s",
			quoteToken(raw), strings.Join(ArtifactKindNames(), ", "))
	}
	return k, nil
}

// AcceptsItems reports whether this kind may carry structured entries.
//
// ── Why a note may not ─────────────────────────────────────────────────
// Because a note is prose by definition: what it says is its body, and a
// checklist hanging off it would be a second, competing statement of what
// the note contains, free to disagree with the sentence above it. The
// other three decompose by nature, so entries are the point rather than
// an accessory.
//
// ── Why this is a rule rather than a convention ────────────────────────
// A caller that adds an entry to a note has misunderstood what it built,
// and the useful moment to say so is the moment it tries. Relaxing this
// later is one line and breaks nothing; adding it later would orphan
// entries that already exist.
func (k ArtifactKind) AcceptsItems() bool {
	switch k {
	case ArtifactProject, ArtifactList, ArtifactPlan:
		return true
	}
	return false
}

/* ── the entity ──────────────────────────────────────────────────────── */

const (
	MaxArtifactTitle = 200
	// MaxArtifactBody is the ceiling every other long-text column in this
	// system carries (chat.agent_sources.content, threads.content,
	// jobradar.description). One number, so the limit is a property of
	// the platform rather than a guess made per table.
	MaxArtifactBody = 20000
)

// Artifact is one living object: a project, a list, a plan, a note.
//
// ── Why the prose and the structure are both here ──────────────────────
// `Body` is what the artifact SAYS; ArtifactItem rows are what it
// CONTAINS. A plan has both: a paragraph explaining what it is for, and
// the steps. Forcing one to carry the other would mean either a list with
// its entries mashed into a paragraph, which nothing can count, or a note
// with its prose split into fake entries, which nothing can read.
type Artifact struct {
	ID          uuid.UUID
	WorkspaceID uuid.UUID

	// RoomID is optional. Nil is an artifact that has not been filed,
	// which is a real and common state: the thing exists before anybody
	// decides where it belongs.
	RoomID *uuid.UUID

	// Kind is decided at creation and never changes. See ArtifactChange.
	Kind  ArtifactKind
	Title string
	Body  string

	Status      Lifecycle
	Sensitivity Sensitivity

	CreatedAt time.Time
	UpdatedAt time.Time
}

func (a *Artifact) Validate() error {
	if a.WorkspaceID == uuid.Nil {
		return Invalid("a workspace is required")
	}
	if !a.Kind.Valid() {
		return Invalid("unknown artifact kind %s; the kinds are %s",
			quoteToken(string(a.Kind)), strings.Join(ArtifactKindNames(), ", "))
	}
	title := NormalizeName(a.Title)
	if title == "" {
		return Invalid("a title is required")
	}
	if len([]rune(title)) > MaxArtifactTitle {
		return Invalid("title is longer than %d characters", MaxArtifactTitle)
	}
	if len([]rune(a.Body)) > MaxArtifactBody {
		return Invalid("body is longer than %d characters", MaxArtifactBody)
	}
	if a.RoomID != nil && *a.RoomID == uuid.Nil {
		// A present-but-zero reference is a caller that built a pointer to
		// nothing. It would satisfy the composite foreign key against no
		// row and fail far from here.
		return Invalid("room_id is present but empty; omit it instead of sending an empty id")
	}
	if !a.Status.Valid() {
		return Invalid("unknown status %s; the statuses are %s",
			quoteToken(string(a.Status)), strings.Join(LifecycleNames(), ", "))
	}
	if !a.Sensitivity.Valid() {
		return Invalid("unknown sensitivity %s; the levels are %s",
			quoteToken(string(a.Sensitivity)), strings.Join(SensitivityNames(), ", "))
	}
	return nil
}

/* ── the change ──────────────────────────────────────────────────────── */

// ArtifactChange is a partial edit of an artifact.
//
// ── Why Kind is not in here ────────────────────────────────────────────
// Because the kind decides what the object IS, and the structured state
// hanging off it was built under that answer. A list turned into a note
// would keep entries that nothing renders; a note turned into a list
// would be a list nobody wrote entries for. Re-filing means creating the
// right thing and archiving the wrong one, which leaves both facts on the
// record instead of rewriting history.
type ArtifactChange struct {
	Title       *string
	Body        *string
	Status      *Lifecycle
	Sensitivity *Sensitivity
	// Room is the three-state edit of a nullable reference: untouched,
	// set, cleared. See OptionalRef, which makes "set and cleared at the
	// same time" impossible to express.
	Room OptionalRef
}

func (c ArtifactChange) Empty() bool {
	return c.Title == nil && c.Body == nil && c.Status == nil &&
		c.Sensitivity == nil && !c.Room.Touched()
}

type ArtifactChangeResult struct {
	PreviousStatus      Lifecycle
	PreviousSensitivity Sensitivity

	TitleChanged       bool
	BodyChanged        bool
	StatusChanged      bool
	SensitivityChanged bool
	RoomChanged        bool
}

func (r ArtifactChangeResult) Unchanged() bool {
	return !r.TitleChanged && !r.BodyChanged && !r.StatusChanged &&
		!r.SensitivityChanged && !r.RoomChanged
}

// Apply mutates the artifact and reports what actually moved.
func (a *Artifact) Apply(c ArtifactChange) ArtifactChangeResult {
	res := ArtifactChangeResult{
		PreviousStatus:      a.Status,
		PreviousSensitivity: a.Sensitivity,
	}
	if c.Title != nil && NormalizeName(*c.Title) != a.Title {
		a.Title = NormalizeName(*c.Title)
		res.TitleChanged = true
	}
	if c.Body != nil && *c.Body != a.Body {
		a.Body = *c.Body
		res.BodyChanged = true
	}
	if c.Status != nil && *c.Status != a.Status {
		a.Status = *c.Status
		res.StatusChanged = true
	}
	if c.Sensitivity != nil && *c.Sensitivity != a.Sensitivity {
		a.Sensitivity = *c.Sensitivity
		res.SensitivityChanged = true
	}
	a.RoomID, res.RoomChanged = c.Room.Resolve(a.RoomID)
	return res
}
