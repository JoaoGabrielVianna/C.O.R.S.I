package domain

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

/* ── OptionalRef ─────────────────────────────────────────────────────── */

func TestTheZeroOptionalRefMeansUntouched(t *testing.T) {
	// A Change built without mentioning the field must mean "leave it
	// alone". If the zero value meant anything else, every partial edit
	// would silently detach the references it did not name.
	var o OptionalRef
	if o.Touched() {
		t.Error("the zero OptionalRef reports touched")
	}
	if o != KeepRef() {
		t.Error("the zero OptionalRef differs from KeepRef()")
	}
}

func TestOptionalRefKeepsTheStoredValueWhenUntouched(t *testing.T) {
	id := uuid.New()
	got, changed := KeepRef().Resolve(&id)
	if changed {
		t.Error("an untouched edit reported a change")
	}
	if got == nil || *got != id {
		t.Errorf("Resolve dropped the stored value: %v", got)
	}
}

func TestOptionalRefSetsAndClears(t *testing.T) {
	stored := uuid.New()
	target := uuid.New()

	got, changed := SetRef(target).Resolve(&stored)
	if !changed || got == nil || *got != target {
		t.Errorf("SetRef did not point at the target: %v changed=%v", got, changed)
	}

	got, changed = ClearRef().Resolve(&stored)
	if !changed || got != nil {
		t.Errorf("ClearRef did not detach: %v changed=%v", got, changed)
	}
}

func TestOptionalRefReportsNoChangeWhenItSetsWhatIsAlreadyThere(t *testing.T) {
	// This is what lets a caller say "it was already filed there" instead
	// of claiming work it did not do.
	id := uuid.New()
	if _, changed := SetRef(id).Resolve(&id); changed {
		t.Error("setting the stored value reported a change")
	}
	if _, changed := ClearRef().Resolve(nil); changed {
		t.Error("clearing an already-empty reference reported a change")
	}
}

func TestOptionalRefResolvesToAPointerTheCallerDoesNotAlreadyHold(t *testing.T) {
	// Resolve must hand back a copy. Aliasing the caller's variable would
	// let the entity change under whoever holds it, from a line that does
	// not mention the entity at all.
	target := uuid.New()
	got, _ := SetRef(target).Resolve(nil)
	if got == nil {
		t.Fatal("SetRef resolved to nothing")
	}
	if got == &target {
		t.Fatal("Resolve handed back the caller's own pointer")
	}
	*got = uuid.Nil
	if target == uuid.Nil {
		t.Error("mutating the resolved value reached the caller's variable")
	}
}

func TestOptionalRefIDAnswersOnlyWhenATargetWasRequested(t *testing.T) {
	// This is what an application service calls to know which row it must
	// resolve in the workspace before the edit may proceed.
	if _, ok := KeepRef().ID(); ok {
		t.Error("an untouched edit offered a target to resolve")
	}
	if _, ok := ClearRef().ID(); ok {
		t.Error("a clearing edit offered a target to resolve")
	}
	want := uuid.New()
	got, ok := SetRef(want).ID()
	if !ok || got != want {
		t.Errorf("ID() = %v, %v; want %v, true", got, ok, want)
	}
}

func TestClearedIsDistinctFromUntouched(t *testing.T) {
	if KeepRef().Cleared() {
		t.Error("an untouched edit reports cleared")
	}
	if !ClearRef().Cleared() {
		t.Error("a clearing edit does not report cleared")
	}
	if SetRef(uuid.New()).Cleared() {
		t.Error("a setting edit reports cleared")
	}
}

/* ── OptionalTime ────────────────────────────────────────────────────── */

func TestTheZeroOptionalTimeMeansUntouched(t *testing.T) {
	var o OptionalTime
	if o.Touched() {
		t.Error("the zero OptionalTime reports touched")
	}
}

func TestOptionalTimeSetsAndClears(t *testing.T) {
	stored := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	target := time.Date(2026, 9, 13, 8, 30, 0, 0, time.UTC)

	got, changed := SetTime(target).Resolve(&stored)
	if !changed || got == nil || !got.Equal(target) {
		t.Errorf("SetTime did not apply: %v changed=%v", got, changed)
	}

	got, changed = ClearTime().Resolve(&stored)
	if !changed || got != nil {
		t.Errorf("ClearTime did not detach: %v changed=%v", got, changed)
	}

	got, changed = KeepTime().Resolve(&stored)
	if changed || got == nil || !got.Equal(stored) {
		t.Errorf("KeepTime moved the value: %v changed=%v", got, changed)
	}
}

func TestOptionalTimeComparesInstantsAndNotRepresentations(t *testing.T) {
	// The same moment in two locations is the same moment. Comparing with
	// == would report a change on every round trip through Postgres, which
	// stamps everything UTC.
	utc := time.Date(2026, 9, 13, 15, 0, 0, 0, time.UTC)
	elsewhere := utc.In(time.FixedZone("BRT", -3*60*60))

	if _, changed := SetTime(elsewhere).Resolve(&utc); changed {
		t.Error("the same instant in another location reported a change")
	}
}
