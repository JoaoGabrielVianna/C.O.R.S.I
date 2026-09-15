package domain

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func relation(from EntityType, kind RelationKind, to EntityType) *Relation {
	return &Relation{
		ID:          uuid.New(),
		WorkspaceID: uuid.New(),
		FromType:    from,
		FromID:      uuid.New(),
		Kind:        kind,
		ToType:      to,
		ToID:        uuid.New(),
	}
}

// ══════════════════════════════════════════════════════════════════════
//
//	THE MATRIX, EXHAUSTIVELY
//
// ══════════════════════════════════════════════════════════════════════
//
// Every kind against every pairing of entity types: 4 × 3 × 3 = 36 cases,
// and the test states the expected answer for all of them rather than
// asking the matrix what it thinks. A table driven from relationMatrix
// itself would pass no matter what the matrix said.
func TestTheRelationMatrixAcceptsExactlyTheseThirtySixAnswers(t *testing.T) {
	allowed := map[string]bool{
		// related_to: an undirected association.
		"related_to:room→room":         true,
		"related_to:artifact→artifact": true,
		"related_to:memory→memory":     true,
		"related_to:memory→artifact":   true,
		// decision_for: a decision governs a thing. Origin is always a
		// memory; the reverse direction is meaningless.
		"decision_for:memory→artifact": true,
		"decision_for:memory→room":     true,
		// mentions: this names that.
		"mentions:memory→artifact":   true,
		"mentions:artifact→artifact": true,
		// supersedes: this replaces that, and only within one type.
		"supersedes:memory→memory":     true,
		"supersedes:artifact→artifact": true,
	}

	seen := 0
	for _, kind := range RelationKinds {
		for _, from := range EntityTypes {
			for _, to := range EntityTypes {
				seen++
				key := string(kind) + ":" + string(from) + "→" + string(to)
				want := allowed[key]

				if got := ShapeAllowed(kind, from, to); got != want {
					t.Errorf("ShapeAllowed(%s) = %v, want %v", key, got, want)
				}

				err := relation(from, kind, to).Validate()
				if want && err != nil {
					t.Errorf("%s was refused: %v", key, err)
				}
				if !want && err == nil {
					t.Errorf("%s was accepted, want refused", key)
				}
			}
		}
	}
	if seen != len(RelationKinds)*len(EntityTypes)*len(EntityTypes) {
		t.Fatalf("walked %d combinations", seen)
	}
	if seen != 36 {
		t.Fatalf("the vocabularies changed size: %d combinations, want 36. "+
			"Add the new pairings to this table deliberately", seen)
	}
}

func TestNothingRelatesToItself(t *testing.T) {
	// Supersedes especially: a row that replaced itself would make "what
	// is current" unanswerable.
	id := uuid.New()
	r := relation(EntityMemory, RelationSupersedes, EntityMemory)
	r.FromID, r.ToID = id, id

	assertInvalid(t, r.Validate(), "cannot point at the thing it starts from")
}

func TestTheSameIdUnderDifferentTypesIsNotSelfReference(t *testing.T) {
	// Ids are per-table, so a memory and an artifact could in principle
	// carry the same uuid. Refusing that pairing would refuse a legitimate
	// link for a coincidence.
	id := uuid.New()
	r := relation(EntityMemory, RelationRelatedTo, EntityArtifact)
	r.FromID, r.ToID = id, id

	if err := r.Validate(); err != nil {
		t.Fatalf("a cross-type link sharing an id was refused: %v", err)
	}
}

func TestARelationNeedsBothEndsAndAWorkspace(t *testing.T) {
	r := relation(EntityMemory, RelationRelatedTo, EntityArtifact)
	r.WorkspaceID = uuid.Nil
	assertInvalid(t, r.Validate(), "workspace")

	r = relation(EntityMemory, RelationRelatedTo, EntityArtifact)
	r.FromID = uuid.Nil
	assertInvalid(t, r.Validate(), "origin id")

	r = relation(EntityMemory, RelationRelatedTo, EntityArtifact)
	r.ToID = uuid.Nil
	assertInvalid(t, r.Validate(), "target id")
}

func TestAnUnknownWordIsReportedAsAnUnknownWordAndNotAsABadShape(t *testing.T) {
	// The order of the checks matters: told "that shape is not allowed", a
	// model goes looking for a different shape instead of fixing its typo.
	r := relation(EntityMemory, "decides", EntityArtifact)
	assertInvalid(t, r.Validate(), "relation kind")

	r = relation("note", RelationRelatedTo, EntityArtifact)
	assertInvalid(t, r.Validate(), "origin")

	r = relation(EntityMemory, RelationRelatedTo, "source")
	assertInvalid(t, r.Validate(), "target")
}

func TestARefusedShapeNamesTheOnesThatWouldWork(t *testing.T) {
	r := relation(EntityRoom, RelationSupersedes, EntityMemory)
	err := r.Validate()
	if err == nil {
		t.Fatal("want a refusal")
	}
	for _, shape := range AllowedShapes(RelationSupersedes) {
		if !strings.Contains(err.Error(), shape.String()) {
			t.Errorf("refusal does not offer %q: %v", shape, err)
		}
	}
}

/* ── the rule the matrix cannot express ──────────────────────────────── */

func TestDecisionForMayOnlyStartAtADecision(t *testing.T) {
	r := relation(EntityMemory, RelationDecisionFor, EntityArtifact)

	decision := validMemory()
	decision.Kind = MemoryDecision
	if err := r.ValidateDecisionOrigin(decision); err != nil {
		t.Fatalf("a decision was refused as the origin: %v", err)
	}

	for _, k := range MemoryKinds {
		if k == MemoryDecision {
			continue
		}
		m := validMemory()
		m.Kind = k
		if err := r.ValidateDecisionOrigin(m); err == nil {
			t.Errorf("a memory of kind %q was accepted as a decision_for origin", k)
		}
	}
}

func TestDecisionForRefusesAnUnresolvedOrigin(t *testing.T) {
	// The application service resolves the origin in the workspace anyway,
	// since that is how isolation is enforced for a row with no foreign
	// key. Being handed nil means the check was skipped, and silently
	// passing would let the one rule the matrix cannot express go
	// unenforced.
	r := relation(EntityMemory, RelationDecisionFor, EntityArtifact)
	if err := r.ValidateDecisionOrigin(nil); err == nil {
		t.Fatal("an unresolved origin was accepted")
	}
}

func TestOtherKindsDoNotCareAboutTheOriginMemory(t *testing.T) {
	for _, kind := range RelationKinds {
		if kind == RelationDecisionFor {
			continue
		}
		r := relation(EntityMemory, kind, EntityMemory)
		if err := r.ValidateDecisionOrigin(nil); err != nil {
			t.Errorf("%q demanded an origin memory: %v", kind, err)
		}
	}
}

/* ── the matrix is internally consistent ─────────────────────────────── */

func TestEveryKindHasAtLeastOneShape(t *testing.T) {
	// A kind with no shape is a word the model is offered and can never
	// use successfully.
	for _, kind := range RelationKinds {
		if len(AllowedShapes(kind)) == 0 {
			t.Errorf("relation kind %q permits no shape at all", kind)
		}
	}
}

func TestEveryShapeUsesADeclaredEntityType(t *testing.T) {
	for _, kind := range RelationKinds {
		for _, s := range AllowedShapes(kind) {
			if !s.From.Valid() || !s.To.Valid() {
				t.Errorf("kind %q permits %q, which names a type that is not in the vocabulary", kind, s)
			}
		}
	}
}

func TestAllowedShapesHandsBackACopy(t *testing.T) {
	// A caller that could sort or truncate the returned slice would be
	// editing the matrix for everybody.
	before := AllowedShapes(RelationRelatedTo)
	got := AllowedShapes(RelationRelatedTo)
	got[0] = RelationShape{From: EntityRoom, To: EntityMemory}

	after := AllowedShapes(RelationRelatedTo)
	if after[0] != before[0] {
		t.Error("mutating the returned slice reached the matrix")
	}
}

func TestShapeDescriptionIsBuiltFromTheMatrix(t *testing.T) {
	// So a pairing added later reaches the model's schema description the
	// day it is added, rather than whenever somebody remembers a string.
	desc := ShapeDescription(RelationRelatedTo)
	for _, s := range AllowedShapes(RelationRelatedTo) {
		if !strings.Contains(desc, s.String()) {
			t.Errorf("ShapeDescription omits %q: %s", s, desc)
		}
	}
}

/* ── what is deliberately not here ───────────────────────────────────── */

func TestBelongsToAndDerivedFromAreNotRelationKinds(t *testing.T) {
	// Belonging is a column (Artifact.RoomID, Memory.RoomID,
	// Memory.ArtifactID). Provenance is palace.memory_sources. Restoring
	// either here would be a second way to state a fact that already has
	// one, free to disagree with it.
	for _, absent := range []RelationKind{"belongs_to", "derived_from"} {
		if absent.Valid() {
			t.Errorf("%q is a relation kind again; see the header of relation.go", absent)
		}
		if _, err := ParseRelationKind(string(absent)); err == nil {
			t.Errorf("%q parsed successfully", absent)
		}
	}
}

/* ── the frozen direction ────────────────────────────────────────────── */

// ══════════════════════════════════════════════════════════════════════
//
//	NEW SUPERSEDES OLD
//
// ══════════════════════════════════════════════════════════════════════
//
// Both ends are the same type, so nothing about the row reveals which
// way it was meant. A reader that guesses backwards does not get an
// error: it gets a coherent, confident, inverted history in which the
// abandoned option is the current decision. This test is the freeze.
func TestSupersedesReadsFromNewToOld(t *testing.T) {
	// Memory B "Backend será Go" SUPERSEDES Memory A "Talvez usar Python".
	backendGo := uuid.New()   // the new decision
	maybePython := uuid.New() // the one it replaced

	r := Relation{
		WorkspaceID: uuid.New(),
		FromType:    EntityMemory, FromID: backendGo,
		Kind:   RelationSupersedes,
		ToType: EntityMemory, ToID: maybePython,
	}
	if err := r.Validate(); err != nil {
		t.Fatalf("the canonical example was refused: %v", err)
	}

	newer, older, ok := r.Superseding()
	if !ok {
		t.Fatal("a supersedes relation did not report its two ends")
	}
	if newer != backendGo {
		t.Error("the FROM end is not the newer one; the direction has been inverted")
	}
	if older != maybePython {
		t.Error("the TO end is not the older one; the direction has been inverted")
	}
}

func TestOnlySupersedesHasANewerAndAnOlderEnd(t *testing.T) {
	// There is no newer end of a MENTIONS, and answering as though there
	// were would invite exactly the misreading the freeze exists to stop.
	for _, kind := range RelationKinds {
		if kind == RelationSupersedes {
			continue
		}
		r := Relation{FromType: EntityMemory, FromID: uuid.New(),
			Kind: kind, ToType: EntityArtifact, ToID: uuid.New()}
		if _, _, ok := r.Superseding(); ok {
			t.Errorf("%q reported a superseding direction", kind)
		}
	}
}

func TestSupersedesDoesNotImplyArchivingTheOlderEnd(t *testing.T) {
	// A compile-time and structural fact, asserted so the intent is
	// findable: nothing in this package takes a relation and returns a
	// lifecycle change. Superseded work is routinely kept active on
	// purpose, because the rejected option is part of why the decision
	// was made.
	r := Relation{
		WorkspaceID: uuid.New(),
		FromType:    EntityMemory, FromID: uuid.New(),
		Kind:   RelationSupersedes,
		ToType: EntityMemory, ToID: uuid.New(),
	}
	newer, older, ok := r.Superseding()
	if !ok || newer == uuid.Nil || older == uuid.Nil {
		t.Fatal("the ends were not reported")
	}
	// The relation carries no lifecycle field of its own, in either
	// direction. If one appears here, this test should stop compiling.
	if r.Kind != RelationSupersedes {
		t.Error("the kind moved")
	}
}

/* ── endpoints must be alive to receive a NEW relation ───────────────── */

func TestANewRelationRefusesAnArchivedEndpoint(t *testing.T) {
	for _, entity := range EntityTypes {
		if err := ValidateEndpointAlive(entity, LifecycleActive); err != nil {
			t.Errorf("an active %s was refused: %v", entity, err)
		}
		err := ValidateEndpointAlive(entity, LifecycleArchived)
		if err == nil {
			t.Errorf("an archived %s was accepted as a relation endpoint", entity)
			continue
		}
		// The refusal has to be actionable: say what to do about it.
		if !strings.Contains(err.Error(), string(LifecycleActive)) {
			t.Errorf("the refusal does not say to restore it: %v", err)
		}
		if !strings.Contains(err.Error(), string(entity)) {
			t.Errorf("the refusal does not say which end: %v", err)
		}
	}
}

func TestAnUnrecognisedStatusFailsClosedAsAnEndpoint(t *testing.T) {
	if err := ValidateEndpointAlive(EntityMemory, "gone"); err == nil {
		t.Error("an unrecognised status was accepted as alive")
	}
}
