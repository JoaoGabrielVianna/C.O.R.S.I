package domain

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

// Memories: the operator's durable knowledge.
//
// NOT chat.Memory, which is an agent's. See the package documentation in
// palace.go, which states the boundary in full.

/* ── the kinds ───────────────────────────────────────────────────────── */

// MemoryKind is what sort of knowledge this is.
//
// ── Why six and why they are not severity levels ───────────────────────
// They answer different questions, and the difference is what makes a
// listing by kind worth anything: "o que eu decidi" is not "o que eu
// aprendi" and neither is "o que eu prefiro". Importance is separate and
// orthogonal: a reflection can matter more than a fact.
type MemoryKind string

const (
	// MemoryFact: something that is true and worth not re-deriving.
	MemoryFact MemoryKind = "fact"
	// MemoryPreference: how the operator likes things. Stable, and the
	// reason a system stops asking the same question.
	MemoryPreference MemoryKind = "preference"
	// MemoryIdea: something worth doing or thinking about, not yet
	// committed to. An idea that gets worked on becomes an Artifact.
	MemoryIdea MemoryKind = "idea"
	// MemoryDecision: a choice that was made, which closes a question.
	// The only kind a DECISION_FOR relation may start at.
	MemoryDecision MemoryKind = "decision"
	// MemoryLearning: something found out the hard way.
	MemoryLearning MemoryKind = "learning"
	// MemoryReflection: the operator thinking about themselves. The kind
	// most likely to be private, and never treated differently for it:
	// sensitivity is its own field precisely so the kind does not have to
	// carry that meaning.
	MemoryReflection MemoryKind = "reflection"
)

var MemoryKinds = []MemoryKind{
	MemoryFact,
	MemoryPreference,
	MemoryIdea,
	MemoryDecision,
	MemoryLearning,
	MemoryReflection,
}

func (k MemoryKind) Valid() bool {
	switch k {
	case MemoryFact, MemoryPreference, MemoryIdea,
		MemoryDecision, MemoryLearning, MemoryReflection:
		return true
	}
	return false
}

func (k MemoryKind) String() string { return string(k) }

func MemoryKindNames() []string { return names(MemoryKinds) }

func ParseMemoryKind(raw string) (MemoryKind, error) {
	k := MemoryKind(fold(raw))
	if !k.Valid() {
		return "", Invalid("unknown memory kind %s; the kinds are %s",
			quoteToken(raw), strings.Join(MemoryKindNames(), ", "))
	}
	return k, nil
}

/* ── confidence ──────────────────────────────────────────────────────── */

// Confidence is how sure the operator is of what a memory says.
//
// ── Three words and not a number ───────────────────────────────────────
// A decimal produced by a model is false precision. Nobody can defend the
// difference between 0.72 and 0.78, two callers would calibrate the same
// belief differently, and every surface would round the value into three
// bands anyway. Storing the three bands is storing what is actually
// known.
type Confidence string

const (
	ConfidenceLow    Confidence = "low"
	ConfidenceMedium Confidence = "medium"
	ConfidenceHigh   Confidence = "high"
)

var Confidences = []Confidence{ConfidenceLow, ConfidenceMedium, ConfidenceHigh}

// DefaultConfidence is what a memory gets when nobody said. Medium,
// because a caller that did not state a confidence has not expressed
// doubt and has not expressed certainty either.
const DefaultConfidence = ConfidenceMedium

func (c Confidence) Valid() bool {
	switch c {
	case ConfidenceLow, ConfidenceMedium, ConfidenceHigh:
		return true
	}
	return false
}

func (c Confidence) String() string { return string(c) }

func ConfidenceNames() []string { return names(Confidences) }

func ParseConfidence(raw string) (Confidence, error) {
	c := Confidence(fold(raw))
	if !c.Valid() {
		return "", Invalid("unknown confidence %s; the levels are %s",
			quoteToken(raw), strings.Join(ConfidenceNames(), ", "))
	}
	return c, nil
}

/* ── the entity ──────────────────────────────────────────────────────── */

const (
	MaxMemoryContent = 20000
	// MaxMemorySummary bounds the one line a listing shows instead of an
	// excerpt. Five hundred characters is a sentence or three.
	MaxMemorySummary = 500

	// The importance scale. 1..5 rather than a flag, because "how much
	// does this matter" is what a listing ranks on, and a boolean forces
	// everything into important-or-not.
	MinImportance = 1
	MaxImportance = 5
	// DefaultImportance is the middle. A caller who did not rank
	// something has not called it trivial either.
	DefaultImportance = 3
)

// Memory is one thing the operator knows and wants kept.
type Memory struct {
	ID          uuid.UUID
	WorkspaceID uuid.UUID

	Kind    MemoryKind
	Content string
	// Summary is optional. Empty means a listing shows an excerpt of the
	// content instead. It is never generated here: whoever writes it
	// decides what it says, and a domain that summarised would be a
	// domain with an opinion about what mattered in the sentence.
	Summary string

	Importance int
	Confidence Confidence

	// OccurredAt is when the thing this memory is ABOUT happened.
	// Optional, because plenty of knowledge has no date: a preference did
	// not occur. Distinct from CreatedAt, which is when it was written
	// down, and the two are routinely far apart.
	OccurredAt *time.Time

	// Optional context. Both nil is an ordinary state: knowledge does not
	// have to belong to a project to be worth keeping.
	RoomID     *uuid.UUID
	ArtifactID *uuid.UUID

	Status      Lifecycle
	Sensitivity Sensitivity

	CreatedAt time.Time
	UpdatedAt time.Time
}

func (m *Memory) Validate() error {
	if m.WorkspaceID == uuid.Nil {
		return Invalid("a workspace is required")
	}
	if !m.Kind.Valid() {
		return Invalid("unknown memory kind %s; the kinds are %s",
			quoteToken(string(m.Kind)), strings.Join(MemoryKindNames(), ", "))
	}
	if strings.TrimSpace(m.Content) == "" {
		// A memory with no content is not a memory. Unlike an artifact,
		// which can legitimately exist as a title while it is being
		// worked on, this row IS the sentence.
		return Invalid("content is required")
	}
	if len([]rune(m.Content)) > MaxMemoryContent {
		return Invalid("content is longer than %d characters", MaxMemoryContent)
	}
	if len([]rune(m.Summary)) > MaxMemorySummary {
		return Invalid("summary is longer than %d characters", MaxMemorySummary)
	}
	if m.Importance < MinImportance || m.Importance > MaxImportance {
		return Invalid("importance must be between %d and %d", MinImportance, MaxImportance)
	}
	if !m.Confidence.Valid() {
		return Invalid("unknown confidence %s; the levels are %s",
			quoteToken(string(m.Confidence)), strings.Join(ConfidenceNames(), ", "))
	}
	if m.RoomID != nil && *m.RoomID == uuid.Nil {
		return Invalid("room_id is present but empty; omit it instead of sending an empty id")
	}
	if m.ArtifactID != nil && *m.ArtifactID == uuid.Nil {
		return Invalid("artifact_id is present but empty; omit it instead of sending an empty id")
	}
	if !m.Status.Valid() {
		return Invalid("unknown status %s; the statuses are %s",
			quoteToken(string(m.Status)), strings.Join(LifecycleNames(), ", "))
	}
	if !m.Sensitivity.Valid() {
		return Invalid("unknown sensitivity %s; the levels are %s",
			quoteToken(string(m.Sensitivity)), strings.Join(SensitivityNames(), ", "))
	}
	return nil
}

/* ── the change ──────────────────────────────────────────────────────── */

// MemoryChange is a partial edit of a memory.
//
// ── Why Kind IS editable here, when an artifact's is not ───────────────
// Because nothing hangs off a memory's kind. An artifact's kind decides
// whether entries exist; a memory's kind is a label on one sentence, and
// misclassification is likely precisely because a model does the
// labelling. Filing a decision as a fact and correcting it later should
// cost one edit, not a new row and an archived one.
//
// ── The one consequence, and the rule that settles it ──────────────────
// A DECISION_FOR relation requires its origin memory to be of kind
// decision, so reclassifying away from decision could leave such a
// relation standing. That case is DECIDED and the decision is normative:
// the reclassification is REFUSED while the relation exists, and the
// relation is never cascaded, dropped or converted to repair it. The
// correction is `palace.relation.remove` followed by the reclassification,
// in that order, by whoever meant both.
//
// The check cannot live here. This package loads nothing, and the rule is
// a fact about the graph rather than about the memory: the same edit is
// legal for a memory nobody linked and illegal for one somebody did. It
// belongs to the application service that holds both repositories, and it
// arrives in the slice that implements Relation. See the normative block
// at the top of relation.go.
type MemoryChange struct {
	Kind        *MemoryKind
	Content     *string
	Summary     *string
	Importance  *int
	Confidence  *Confidence
	Status      *Lifecycle
	Sensitivity *Sensitivity

	// The three-state edits of nullable fields. See OptionalRef and
	// OptionalTime.
	OccurredAt OptionalTime
	Room       OptionalRef
	Artifact   OptionalRef
}

func (c MemoryChange) Empty() bool {
	return c.Kind == nil && c.Content == nil && c.Summary == nil &&
		c.Importance == nil && c.Confidence == nil && c.Status == nil &&
		c.Sensitivity == nil && !c.OccurredAt.Touched() &&
		!c.Room.Touched() && !c.Artifact.Touched()
}

type MemoryChangeResult struct {
	PreviousKind        MemoryKind
	PreviousStatus      Lifecycle
	PreviousSensitivity Sensitivity

	KindChanged        bool
	ContentChanged     bool
	SummaryChanged     bool
	ImportanceChanged  bool
	ConfidenceChanged  bool
	StatusChanged      bool
	SensitivityChanged bool
	OccurredAtChanged  bool
	RoomChanged        bool
	ArtifactChanged    bool
}

func (r MemoryChangeResult) Unchanged() bool {
	return !r.KindChanged && !r.ContentChanged && !r.SummaryChanged &&
		!r.ImportanceChanged && !r.ConfidenceChanged && !r.StatusChanged &&
		!r.SensitivityChanged && !r.OccurredAtChanged &&
		!r.RoomChanged && !r.ArtifactChanged
}

// Apply mutates the memory and reports what actually moved.
func (m *Memory) Apply(c MemoryChange) MemoryChangeResult {
	res := MemoryChangeResult{
		PreviousKind:        m.Kind,
		PreviousStatus:      m.Status,
		PreviousSensitivity: m.Sensitivity,
	}
	if c.Kind != nil && *c.Kind != m.Kind {
		m.Kind = *c.Kind
		res.KindChanged = true
	}
	if c.Content != nil && *c.Content != m.Content {
		m.Content = *c.Content
		res.ContentChanged = true
	}
	if c.Summary != nil && *c.Summary != m.Summary {
		m.Summary = *c.Summary
		res.SummaryChanged = true
	}
	if c.Importance != nil && *c.Importance != m.Importance {
		m.Importance = *c.Importance
		res.ImportanceChanged = true
	}
	if c.Confidence != nil && *c.Confidence != m.Confidence {
		m.Confidence = *c.Confidence
		res.ConfidenceChanged = true
	}
	if c.Status != nil && *c.Status != m.Status {
		m.Status = *c.Status
		res.StatusChanged = true
	}
	if c.Sensitivity != nil && *c.Sensitivity != m.Sensitivity {
		m.Sensitivity = *c.Sensitivity
		res.SensitivityChanged = true
	}
	m.OccurredAt, res.OccurredAtChanged = c.OccurredAt.Resolve(m.OccurredAt)
	m.RoomID, res.RoomChanged = c.Room.Resolve(m.RoomID)
	m.ArtifactID, res.ArtifactChanged = c.Artifact.Resolve(m.ArtifactID)
	return res
}
