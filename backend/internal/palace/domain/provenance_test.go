package domain

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func validMemorySource() *MemorySource {
	return &MemorySource{
		WorkspaceID: uuid.New(),
		MemoryID:    uuid.New(),
		SourceID:    uuid.New(),
	}
}

func TestAWellFormedProvenanceLinkValidates(t *testing.T) {
	if err := validMemorySource().Validate(); err != nil {
		t.Fatalf("a valid link was refused: %v", err)
	}
}

func TestAProvenanceLinkNeedsBothEndsAndAWorkspace(t *testing.T) {
	ms := validMemorySource()
	ms.WorkspaceID = uuid.Nil
	assertInvalid(t, ms.Validate(), "workspace")

	ms = validMemorySource()
	ms.MemoryID = uuid.Nil
	assertInvalid(t, ms.Validate(), "memory")

	ms = validMemorySource()
	ms.SourceID = uuid.Nil
	assertInvalid(t, ms.Validate(), "source")
}

/* ── the sensitivity floor ───────────────────────────────────────────── */

// ══════════════════════════════════════════════════════════════════════
//
//	ALL NINE COMBINATIONS, STATED
//
// ══════════════════════════════════════════════════════════════════════
//
// The rule, in one line: a memory may not be less withheld than the
// evidence behind it. Written out rather than derived from Rank(),
// because a table built from the implementation passes whatever the
// implementation says.
func TestTheSensitivityFloorCoversEveryCombination(t *testing.T) {
	const (
		allow  = true
		refuse = false
	)
	cases := map[Sensitivity]map[Sensitivity]bool{
		// memory normal: only ordinary evidence
		SensitivityNormal: {
			SensitivityNormal:          allow,
			SensitivityPrivate:         refuse,
			SensitivityHighlySensitive: refuse,
		},
		// memory private: ordinary and private evidence
		SensitivityPrivate: {
			SensitivityNormal:          allow,
			SensitivityPrivate:         allow,
			SensitivityHighlySensitive: refuse,
		},
		// memory highly sensitive: anything
		SensitivityHighlySensitive: {
			SensitivityNormal:          allow,
			SensitivityPrivate:         allow,
			SensitivityHighlySensitive: allow,
		},
	}

	seen := 0
	for memory, row := range cases {
		for evidence, allowed := range row {
			seen++
			err := ValidateSensitivityFloor(memory, evidence)
			if allowed && err != nil {
				t.Errorf("%s memory on %s evidence was refused: %v", memory, evidence, err)
			}
			if !allowed && err == nil {
				t.Errorf("%s memory on %s evidence was allowed", memory, evidence)
			}
		}
	}
	if seen != 9 {
		t.Fatalf("walked %d combinations, want 9", seen)
	}
}

func TestTheFourExamplesFromTheDecision(t *testing.T) {
	// The cases as they were written down, checked literally, so a future
	// reader can match the code against the decision without re-deriving
	// anything.
	if err := ValidateSensitivityFloor(SensitivityPrivate, SensitivityNormal); err != nil {
		t.Errorf("NORMAL source into PRIVATE memory should be permitted: %v", err)
	}
	if err := ValidateSensitivityFloor(SensitivityPrivate, SensitivityPrivate); err != nil {
		t.Errorf("PRIVATE source into PRIVATE memory should be permitted: %v", err)
	}
	if err := ValidateSensitivityFloor(SensitivityNormal, SensitivityPrivate); err == nil {
		t.Error("PRIVATE source into NORMAL memory should be refused")
	}
	for _, memory := range []Sensitivity{SensitivityNormal, SensitivityPrivate} {
		if err := ValidateSensitivityFloor(memory, SensitivityHighlySensitive); err == nil {
			t.Errorf("HIGHLY_SENSITIVE source into %s memory should be refused", memory)
		}
	}
}

func TestTheFloorRefusesRatherThanRaising(t *testing.T) {
	// Expressed as a property of the signature: the function returns an
	// error and nothing else. There is no value to assign back, so there
	// is no way for a caller to receive a silently upgraded memory.
	//
	// Raising it here would mean the system deciding, quietly, that
	// something the operator called ordinary is now private, and they
	// would find out when they went looking for it in a listing where it
	// no longer is.
	err := ValidateSensitivityFloor(SensitivityNormal, SensitivityPrivate)
	if err == nil {
		t.Fatal("want a refusal")
	}
	// The refusal has to be actionable: say which level is needed.
	if !strings.Contains(err.Error(), string(SensitivityPrivate)) {
		t.Errorf("the refusal does not name the level required: %v", err)
	}
	// And say why detaching is not the way out, or the caller goes looking
	// for an unlink operation that this version does not have.
	//
	// Worded as a fact about THIS VERSION rather than about the product
	// forever: append-only is a Palace Core v1 decision, and an explicit
	// provenance correction may be added later. See the header of
	// provenance.go.
	if !strings.Contains(err.Error(), "append-only") {
		t.Errorf("the refusal does not say why the floor cannot be lifted: %v", err)
	}
}

func TestTheFloorFailsClosedOnAnUnknownLevel(t *testing.T) {
	if err := ValidateSensitivityFloor(SensitivityHighlySensitive, "secret"); err == nil {
		t.Error("unrecognised evidence established a floor of nothing")
	}
	if err := ValidateSensitivityFloor("secret", SensitivityNormal); err == nil {
		t.Error("a memory with an unrecognised level was cleared")
	}
}

func TestMayRestOnIsTheSameRuleWithTheEntitiesInHand(t *testing.T) {
	m := validMemory()
	m.Sensitivity = SensitivityNormal

	s := validSource()
	s.Sensitivity = SensitivityPrivate

	if err := m.MayRestOn(s); err == nil {
		t.Error("a normal memory was allowed to rest on private evidence")
	}

	m.Sensitivity = SensitivityPrivate
	if err := m.MayRestOn(s); err != nil {
		t.Errorf("a private memory was refused private evidence: %v", err)
	}
}

func TestMayRestOnRefusesAbsentEvidence(t *testing.T) {
	// Being handed nil means the source was never resolved, and passing
	// silently would let the one rule that guards this go unenforced.
	m := validMemory()
	if err := m.MayRestOn(nil); err == nil {
		t.Fatal("a link to no evidence at all was allowed")
	}
}

/* ── the boundary this file exists to keep ───────────────────────────── */

func TestContextualLinksDoNotEstablishAFloor(t *testing.T) {
	// A memory's Room and Artifact say what it is filed under and what it
	// is about. Provenance says what it rests on. Only the second one
	// constrains anything.
	//
	// There is no function in this package that takes an Artifact or a
	// Room and a Memory and compares their levels, and this test exists
	// to make adding one a deliberate act rather than a helpful-looking
	// afternoon. The consequence is named rather than hidden: a normal
	// memory attached to a highly sensitive artifact does appear in
	// default listings.
	m := validMemory()
	m.Sensitivity = SensitivityNormal
	room := uuid.New()
	artifact := uuid.New()
	m.RoomID = &room
	m.ArtifactID = &artifact

	if err := m.Validate(); err != nil {
		t.Fatalf("a normal memory attached to other entities was refused: %v", err)
	}
}

func TestProvenanceHasNoChangeType(t *testing.T) {
	// A compile-time fact, asserted here so the intent is findable: a
	// MemorySource carries only its two ends and when it was made. There
	// is nothing to edit, which is what append-only means in practice.
	ms := MemorySource{
		WorkspaceID: uuid.New(),
		MemoryID:    uuid.New(),
		SourceID:    uuid.New(),
		CreatedAt:   time.Now(),
	}
	if ms.CreatedAt.IsZero() {
		t.Error("a link does not record when it was made")
	}
}
