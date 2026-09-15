package domain

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func validArtifact() *Artifact {
	return &Artifact{
		ID:          uuid.New(),
		WorkspaceID: uuid.New(),
		Kind:        ArtifactList,
		Title:       "Leituras de 2026",
		Body:        "O que quero ler este ano",
		Status:      DefaultLifecycle,
		Sensitivity: DefaultSensitivity,
	}
}

func TestAWellFormedArtifactValidates(t *testing.T) {
	if err := validArtifact().Validate(); err != nil {
		t.Fatalf("a valid artifact was refused: %v", err)
	}
}

func TestEveryDeclaredArtifactKindIsAccepted(t *testing.T) {
	for _, k := range ArtifactKinds {
		a := validArtifact()
		a.Kind = k
		if err := a.Validate(); err != nil {
			t.Errorf("kind %q was refused: %v", k, err)
		}
	}
}

func TestAnArtifactRefusesAnUnknownKind(t *testing.T) {
	a := validArtifact()
	a.Kind = "checklist"
	assertInvalid(t, a.Validate(), "kind")
}

func TestParseArtifactKindNamesEveryKindInItsRefusal(t *testing.T) {
	_, err := ParseArtifactKind("checklist")
	if err == nil {
		t.Fatal("want a refusal")
	}
	for _, name := range ArtifactKindNames() {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("refusal does not name %q: %v", name, err)
		}
	}
}

func TestAnArtifactNeedsATitleAndBoundsItsBody(t *testing.T) {
	a := validArtifact()
	a.Title = "  "
	assertInvalid(t, a.Validate(), "title")

	a = validArtifact()
	a.Body = strings.Repeat("x", MaxArtifactBody+1)
	assertInvalid(t, a.Validate(), "body")
}

func TestAnArtifactMayHaveAnEmptyBody(t *testing.T) {
	// A list is its entries; the prose is optional. An artifact that had
	// to carry a paragraph before it could exist would be one nobody
	// creates in the moment they think of it.
	a := validArtifact()
	a.Body = ""
	if err := a.Validate(); err != nil {
		t.Fatalf("an artifact with no body was refused: %v", err)
	}
}

func TestAnUnfiledArtifactIsValidButAZeroRoomIdIsNot(t *testing.T) {
	// Nil is a real state: the thing exists before anybody decides where
	// it belongs. A present-but-zero pointer is a caller that built a
	// reference to nothing, and it would fail against the composite
	// foreign key far from here.
	a := validArtifact()
	a.RoomID = nil
	if err := a.Validate(); err != nil {
		t.Fatalf("an unfiled artifact was refused: %v", err)
	}

	zero := uuid.Nil
	a.RoomID = &zero
	assertInvalid(t, a.Validate(), "room_id")
}

/* ── what may carry entries ──────────────────────────────────────────── */

func TestOnlyTheKindsThatDecomposeAcceptEntries(t *testing.T) {
	// A note is prose by definition: what it says is its body, and a
	// checklist hanging off it would be a second, competing statement of
	// what the note contains.
	want := map[ArtifactKind]bool{
		ArtifactProject: true,
		ArtifactList:    true,
		ArtifactPlan:    true,
		ArtifactNote:    false,
	}
	for _, k := range ArtifactKinds {
		expected, declared := want[k]
		if !declared {
			t.Fatalf("kind %q has no decision about entries; add one", k)
		}
		if got := k.AcceptsItems(); got != expected {
			t.Errorf("%q.AcceptsItems() = %v, want %v", k, got, expected)
		}
	}
}

/* ── the change ──────────────────────────────────────────────────────── */

func TestAnArtifactChangeCannotRestateTheKind(t *testing.T) {
	// Expressed as a compile-time fact rather than a runtime check: there
	// is no Kind field on ArtifactChange, so re-filing means creating the
	// right thing and archiving the wrong one, which leaves both facts on
	// the record.
	a := validArtifact()
	before := a.Kind
	title := "Outro título"

	a.Apply(ArtifactChange{Title: &title})

	if a.Kind != before {
		t.Errorf("Kind moved to %q through an ordinary edit", a.Kind)
	}
}

func TestAnEmptyArtifactChangeIsRecognisable(t *testing.T) {
	if !(ArtifactChange{}).Empty() {
		t.Error("the zero ArtifactChange does not report empty")
	}
	// An untouched OptionalRef must not count as content, or every empty
	// edit would be accepted and move updated_at.
	if (ArtifactChange{Room: KeepRef()}).Empty() != true {
		t.Error("a change holding only KeepRef() does not report empty")
	}
	if (ArtifactChange{Room: ClearRef()}).Empty() {
		t.Error("a change that detaches the room reports empty")
	}
}

func TestFilingAndUnfilingAnArtifact(t *testing.T) {
	a := validArtifact()
	room := uuid.New()

	res := a.Apply(ArtifactChange{Room: SetRef(room)})
	if !res.RoomChanged || a.RoomID == nil || *a.RoomID != room {
		t.Fatalf("the artifact was not filed: %v changed=%v", a.RoomID, res.RoomChanged)
	}

	res = a.Apply(ArtifactChange{Room: ClearRef()})
	if !res.RoomChanged || a.RoomID != nil {
		t.Fatalf("the artifact was not unfiled: %v changed=%v", a.RoomID, res.RoomChanged)
	}
}

func TestAnArtifactEditThatNamesNoRoomLeavesTheRoomAlone(t *testing.T) {
	// The failure this guards: a partial edit that silently detaches every
	// reference it did not mention.
	a := validArtifact()
	room := uuid.New()
	a.RoomID = &room
	body := "novo corpo"

	res := a.Apply(ArtifactChange{Body: &body})

	if res.RoomChanged {
		t.Error("an edit that did not mention the room reported moving it")
	}
	if a.RoomID == nil || *a.RoomID != room {
		t.Errorf("RoomID = %v, want it untouched", a.RoomID)
	}
}

func TestAnArtifactChangeReportsExactlyWhatMoved(t *testing.T) {
	a := validArtifact()
	sameTitle := a.Title
	newBody := "outro corpo"
	archived := LifecycleArchived

	res := a.Apply(ArtifactChange{
		Title:  &sameTitle,
		Body:   &newBody,
		Status: &archived,
	})

	if res.TitleChanged {
		t.Error("resending the stored title was reported as a change")
	}
	if !res.BodyChanged || !res.StatusChanged {
		t.Errorf("a real change went unreported: %+v", res)
	}
	if res.Unchanged() {
		t.Error("an edit that moved two fields reported unchanged")
	}
	if res.PreviousStatus != LifecycleActive {
		t.Errorf("PreviousStatus = %q, want %q", res.PreviousStatus, LifecycleActive)
	}
}
