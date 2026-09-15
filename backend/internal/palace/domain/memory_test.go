package domain

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func validMemory() *Memory {
	return &Memory{
		ID:          uuid.New(),
		WorkspaceID: uuid.New(),
		Kind:        MemoryDecision,
		Content:     "Decidi não aceitar reunião antes das dez",
		Importance:  DefaultImportance,
		Confidence:  DefaultConfidence,
		Status:      DefaultLifecycle,
		Sensitivity: DefaultSensitivity,
	}
}

func TestAWellFormedMemoryValidates(t *testing.T) {
	if err := validMemory().Validate(); err != nil {
		t.Fatalf("a valid memory was refused: %v", err)
	}
}

func TestEveryDeclaredMemoryKindIsAccepted(t *testing.T) {
	for _, k := range MemoryKinds {
		m := validMemory()
		m.Kind = k
		if err := m.Validate(); err != nil {
			t.Errorf("kind %q was refused: %v", k, err)
		}
	}
}

func TestTheMemoryKindVocabularyIsTheSixThatWereDecided(t *testing.T) {
	// A guard on the vocabulary itself: adding a seventh kind has to be a
	// decision somebody makes here, not a constant that appears.
	want := []MemoryKind{
		MemoryFact, MemoryPreference, MemoryIdea,
		MemoryDecision, MemoryLearning, MemoryReflection,
	}
	if len(MemoryKinds) != len(want) {
		t.Fatalf("MemoryKinds has %d entries, want %d", len(MemoryKinds), len(want))
	}
	for i, k := range want {
		if MemoryKinds[i] != k {
			t.Errorf("MemoryKinds[%d] = %q, want %q", i, MemoryKinds[i], k)
		}
	}
}

func TestAMemoryIsItsContentSoContentIsRequired(t *testing.T) {
	// Unlike an artifact, which can legitimately exist as a title while it
	// is being worked on, this row IS the sentence.
	m := validMemory()
	m.Content = "   "
	assertInvalid(t, m.Validate(), "content")
}

func TestMemoryContentAndSummaryAreBounded(t *testing.T) {
	m := validMemory()
	m.Content = strings.Repeat("ç", MaxMemoryContent+1)
	assertInvalid(t, m.Validate(), "content")

	m = validMemory()
	m.Summary = strings.Repeat("ç", MaxMemorySummary+1)
	assertInvalid(t, m.Validate(), "summary")

	m = validMemory()
	m.Summary = ""
	if err := m.Validate(); err != nil {
		t.Fatalf("a memory with no summary was refused: %v", err)
	}
}

func TestImportanceStaysInsideItsScale(t *testing.T) {
	for _, n := range []int{MinImportance, 3, MaxImportance} {
		m := validMemory()
		m.Importance = n
		if err := m.Validate(); err != nil {
			t.Errorf("importance %d was refused: %v", n, err)
		}
	}
	for _, n := range []int{0, -1, MaxImportance + 1, 99} {
		m := validMemory()
		m.Importance = n
		assertInvalid(t, m.Validate(), "importance")
	}
}

func TestConfidenceIsThreeWordsAndNotANumber(t *testing.T) {
	// A decimal produced by a model is false precision. The type is a
	// closed vocabulary, so "0.72" cannot be stored at all.
	for _, c := range Confidences {
		m := validMemory()
		m.Confidence = c
		if err := m.Validate(); err != nil {
			t.Errorf("confidence %q was refused: %v", c, err)
		}
	}
	for _, bad := range []Confidence{"", "0.72", "very high", "certain"} {
		m := validMemory()
		m.Confidence = bad
		assertInvalid(t, m.Validate(), "confidence")
	}
}

func TestAMemoryNeedsNoDateBecausePlentyOfKnowledgeHasNone(t *testing.T) {
	// A preference did not occur.
	m := validMemory()
	m.OccurredAt = nil
	if err := m.Validate(); err != nil {
		t.Fatalf("a memory with no occurred_at was refused: %v", err)
	}

	when := time.Date(2026, 8, 30, 9, 0, 0, 0, time.UTC)
	m.OccurredAt = &when
	if err := m.Validate(); err != nil {
		t.Fatalf("a dated memory was refused: %v", err)
	}
}

func TestAMemoryRefusesAZeroContextReference(t *testing.T) {
	zero := uuid.Nil

	m := validMemory()
	m.RoomID = &zero
	assertInvalid(t, m.Validate(), "room_id")

	m = validMemory()
	m.ArtifactID = &zero
	assertInvalid(t, m.Validate(), "artifact_id")
}

func TestAMemoryCarriesItsOwnSensitivityIndependentOfItsKind(t *testing.T) {
	// A reflection is the kind most likely to be private, and it is never
	// treated differently for it: sensitivity is its own field precisely
	// so the kind does not have to carry that meaning.
	m := validMemory()
	m.Kind = MemoryReflection
	m.Sensitivity = SensitivityNormal
	if err := m.Validate(); err != nil {
		t.Fatalf("an ordinary reflection was refused: %v", err)
	}

	m.Kind = MemoryFact
	m.Sensitivity = SensitivityHighlySensitive
	if err := m.Validate(); err != nil {
		t.Fatalf("a highly sensitive fact was refused: %v", err)
	}
}

/* ── the change ──────────────────────────────────────────────────────── */

func TestAnEmptyMemoryChangeIsRecognisable(t *testing.T) {
	if !(MemoryChange{}).Empty() {
		t.Error("the zero MemoryChange does not report empty")
	}
	// Every optional field must be counted, or an edit that only detaches
	// a room would be mistaken for an empty one and refused.
	if (MemoryChange{Room: ClearRef()}).Empty() {
		t.Error("a change that detaches the room reports empty")
	}
	if (MemoryChange{OccurredAt: ClearTime()}).Empty() {
		t.Error("a change that clears the date reports empty")
	}
	if (MemoryChange{Artifact: SetRef(uuid.New())}).Empty() {
		t.Error("a change that attaches an artifact reports empty")
	}
}

func TestEveryMemoryFieldIsCountedByEmpty(t *testing.T) {
	// The failure this guards: a field added to MemoryChange and not to
	// Empty(), which makes an edit that only touches it look like a
	// request to change nothing.
	kind := MemoryFact
	text := "x"
	n := 4
	conf := ConfidenceHigh
	life := LifecycleArchived
	sens := SensitivityPrivate

	for name, c := range map[string]MemoryChange{
		"kind":        {Kind: &kind},
		"content":     {Content: &text},
		"summary":     {Summary: &text},
		"importance":  {Importance: &n},
		"confidence":  {Confidence: &conf},
		"status":      {Status: &life},
		"sensitivity": {Sensitivity: &sens},
		"occurred_at": {OccurredAt: SetTime(time.Now())},
		"room":        {Room: SetRef(uuid.New())},
		"artifact":    {Artifact: SetRef(uuid.New())},
	} {
		if c.Empty() {
			t.Errorf("a change touching only %s reports empty", name)
		}
	}
}

func TestAMemoryKindCanBeCorrected(t *testing.T) {
	// Nothing hangs off a memory's kind, and misclassification is likely
	// precisely because a model does the labelling. Filing a decision as a
	// fact and correcting it should cost one edit.
	m := validMemory()
	m.Kind = MemoryFact
	decision := MemoryDecision

	res := m.Apply(MemoryChange{Kind: &decision})

	if !res.KindChanged || m.Kind != MemoryDecision {
		t.Fatalf("the kind was not corrected: %q %+v", m.Kind, res)
	}
	if res.PreviousKind != MemoryFact {
		t.Errorf("PreviousKind = %q, want %q", res.PreviousKind, MemoryFact)
	}
}

func TestAMemoryEditTouchesOnlyWhatWasSent(t *testing.T) {
	m := validMemory()
	room := uuid.New()
	m.RoomID = &room
	when := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	m.OccurredAt = &when
	newContent := "Decidi não aceitar reunião antes das onze"

	res := m.Apply(MemoryChange{Content: &newContent})

	if !res.ContentChanged {
		t.Error("the content change went unreported")
	}
	if res.RoomChanged || m.RoomID == nil || *m.RoomID != room {
		t.Error("an edit that did not mention the room moved it")
	}
	if res.OccurredAtChanged || m.OccurredAt == nil || !m.OccurredAt.Equal(when) {
		t.Error("an edit that did not mention the date moved it")
	}
	if res.ImportanceChanged || res.ConfidenceChanged || res.KindChanged {
		t.Errorf("fields nobody sent were reported as moved: %+v", res)
	}
}

func TestClearingAMemorysDateIsDistinctFromLeavingItAlone(t *testing.T) {
	m := validMemory()
	when := time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC)
	m.OccurredAt = &when

	res := m.Apply(MemoryChange{OccurredAt: KeepTime()})
	if res.OccurredAtChanged || m.OccurredAt == nil {
		t.Fatal("KeepTime dropped the stored date")
	}

	res = m.Apply(MemoryChange{OccurredAt: ClearTime()})
	if !res.OccurredAtChanged || m.OccurredAt != nil {
		t.Fatalf("ClearTime did not remove the date: %v %+v", m.OccurredAt, res)
	}
}

func TestAMemoryEditThatMovesNothingSaysSo(t *testing.T) {
	m := validMemory()
	sameKind := m.Kind
	sameContent := m.Content
	sameImportance := m.Importance

	res := m.Apply(MemoryChange{
		Kind:       &sameKind,
		Content:    &sameContent,
		Importance: &sameImportance,
	})

	if !res.Unchanged() {
		t.Errorf("an edit that moved nothing reported movement: %+v", res)
	}
}
