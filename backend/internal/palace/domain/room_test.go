package domain

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

// validRoom is the baseline every case below mutates one field of, so a
// test names exactly the thing it is about.
func validRoom() *Room {
	return &Room{
		ID:          uuid.New(),
		WorkspaceID: uuid.New(),
		Name:        "Carreira",
		Description: "Onde moram as decisões de trabalho",
		Status:      DefaultLifecycle,
		Sensitivity: DefaultSensitivity,
	}
}

func TestAWellFormedRoomValidates(t *testing.T) {
	if err := validRoom().Validate(); err != nil {
		t.Fatalf("a valid room was refused: %v", err)
	}
}

func TestARoomWithoutAWorkspaceIsRefused(t *testing.T) {
	// A row with no workspace belongs to nobody, and every read in this
	// context filters on the workspace: it would be written and never
	// seen again.
	r := validRoom()
	r.WorkspaceID = uuid.Nil
	assertInvalid(t, r.Validate(), "workspace")
}

func TestARoomNeedsAName(t *testing.T) {
	r := validRoom()
	r.Name = "   "
	assertInvalid(t, r.Validate(), "name")
}

func TestARoomNameIsBoundedInRunesNotBytes(t *testing.T) {
	// Counted in runes, like the CHECK, which Postgres measures in
	// characters. Using len() would reject accented Portuguese hundreds of
	// characters before the database would.
	r := validRoom()
	r.Name = strings.Repeat("ç", MaxRoomName)
	if err := r.Validate(); err != nil {
		t.Fatalf("%d accented characters were refused: %v", MaxRoomName, err)
	}
	r.Name = strings.Repeat("ç", MaxRoomName+1)
	assertInvalid(t, r.Validate(), "name")
}

func TestARoomDescriptionIsBounded(t *testing.T) {
	r := validRoom()
	r.Description = strings.Repeat("x", MaxRoomDescription+1)
	assertInvalid(t, r.Validate(), "description")
}

func TestARoomRefusesAnUnknownStatusOrLevel(t *testing.T) {
	r := validRoom()
	r.Status = "deleted"
	assertInvalid(t, r.Validate(), "status")

	r = validRoom()
	r.Sensitivity = "secret"
	assertInvalid(t, r.Validate(), "sensitivity")
}

func TestARoomCarriesItsOwnSensitivity(t *testing.T) {
	// A room's NAME can disclose what it contains before anything inside
	// it is read. "Terapia" is a disclosure on its own.
	r := validRoom()
	r.Sensitivity = SensitivityHighlySensitive
	if err := r.Validate(); err != nil {
		t.Fatalf("a highly sensitive room was refused: %v", err)
	}
	if !r.Sensitivity.HiddenFromBroadListing() {
		t.Error("a highly sensitive room would still appear in a broad listing")
	}
}

/* ── the change ──────────────────────────────────────────────────────── */

func TestAnEmptyRoomChangeIsRecognisable(t *testing.T) {
	if !(RoomChange{}).Empty() {
		t.Error("the zero RoomChange does not report empty")
	}
	name := "x"
	if (RoomChange{Name: &name}).Empty() {
		t.Error("a change with a name reports empty")
	}
}

func TestApplyingARoomChangeTouchesOnlyWhatWasSent(t *testing.T) {
	// "Arquiva a sala de carreira" moves the status and must not touch a
	// word of the description.
	r := validRoom()
	before := *r
	archived := LifecycleArchived

	res := r.Apply(RoomChange{Status: &archived})

	if r.Name != before.Name || r.Description != before.Description {
		t.Error("a status change rewrote a field it was not given")
	}
	if !res.StatusChanged || res.NameChanged || res.DescriptionChanged {
		t.Errorf("the result misreports what moved: %+v", res)
	}
	if res.PreviousStatus != before.Status {
		t.Errorf("PreviousStatus = %q, want %q", res.PreviousStatus, before.Status)
	}
}

func TestARoomChangeThatSetsWhatIsAlreadyThereReportsUnchanged(t *testing.T) {
	// What stops a model reporting work it did not do. It is also what
	// stops updated_at moving, which would reorder every listing.
	r := validRoom()
	same := r.Name
	status := r.Status

	res := r.Apply(RoomChange{Name: &same, Status: &status})

	if !res.Unchanged() {
		t.Errorf("an edit that moved nothing reported movement: %+v", res)
	}
}

func TestARoomChangeReportsBothSidesOfASensitivityMove(t *testing.T) {
	// "normal → private" can be verified by a reader; "done" cannot.
	r := validRoom()
	private := SensitivityPrivate

	res := r.Apply(RoomChange{Sensitivity: &private})

	if !res.SensitivityChanged {
		t.Fatal("the sensitivity move was not reported")
	}
	if res.PreviousSensitivity != SensitivityNormal {
		t.Errorf("PreviousSensitivity = %q, want %q", res.PreviousSensitivity, SensitivityNormal)
	}
	if r.Sensitivity != SensitivityPrivate {
		t.Errorf("Sensitivity = %q, want %q", r.Sensitivity, SensitivityPrivate)
	}
}

func TestApplyingARoomNameTrimsItWithoutChangingCase(t *testing.T) {
	r := validRoom()
	padded := "  Saúde Mental  "

	r.Apply(RoomChange{Name: &padded})

	if r.Name != "Saúde Mental" {
		t.Errorf("Name = %q, want the trimmed original casing", r.Name)
	}
}

/* ── shared assertion ────────────────────────────────────────────────── */

// assertInvalid checks that err is this package's invalid error and that
// it names the field it is about, so a caller reading it can act.
func assertInvalid(t *testing.T, err error, mentions string) {
	t.Helper()
	if err == nil {
		t.Fatalf("want an invalid error mentioning %q, got nil", mentions)
	}
	de, ok := err.(*Error)
	if !ok {
		t.Fatalf("error is %T, want *domain.Error: %v", err, err)
	}
	if de.Kind != KindInvalid {
		t.Errorf("kind = %q, want %q", de.Kind, KindInvalid)
	}
	if !strings.Contains(de.Message, mentions) {
		t.Errorf("message %q does not mention %q", de.Message, mentions)
	}
}
