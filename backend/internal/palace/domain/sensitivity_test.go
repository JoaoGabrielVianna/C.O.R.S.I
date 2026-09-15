package domain

import (
	"strings"
	"testing"
)

func TestSensitivityAcceptsOnlyItsThreeLevels(t *testing.T) {
	for _, s := range Sensitivities {
		if !s.Valid() {
			t.Errorf("declared level %q reports invalid", s)
		}
	}
	for _, bad := range []Sensitivity{"", "secret", "public", "HIGHLY_SENSITIVE", "sensitive"} {
		if bad.Valid() {
			t.Errorf("level %q reports valid", bad)
		}
	}
}

func TestParseSensitivityRefusesTheNearMiss(t *testing.T) {
	// "sensitive" is not "highly_sensitive". Guessing in this direction
	// would file something the operator called sensitive at a level that
	// shows it in every listing.
	if _, err := ParseSensitivity("sensitive"); err == nil {
		t.Fatal("`sensitive` was accepted as `highly_sensitive`")
	}
	if _, err := ParseSensitivity("  Highly_Sensitive "); err != nil {
		t.Fatalf("case and space should be forgiven: %v", err)
	}
}

func TestParseSensitivityNamesEveryLevelInItsRefusal(t *testing.T) {
	_, err := ParseSensitivity("classified")
	if err == nil {
		t.Fatal("want a refusal")
	}
	for _, name := range SensitivityNames() {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("refusal does not name %q: %v", name, err)
		}
	}
}

// The predicate every listing routes through. If this is wrong, either
// private content disappears from the surface that exists to find it, or
// the most private content appears in a listing nobody asked to see it
// in.
func TestOnlyHighlySensitiveIsWithheldFromABroadListing(t *testing.T) {
	cases := map[Sensitivity]bool{
		SensitivityNormal:          false,
		SensitivityPrivate:         false,
		SensitivityHighlySensitive: true,
	}
	for level, want := range cases {
		if got := level.HiddenFromBroadListing(); got != want {
			t.Errorf("%q.HiddenFromBroadListing() = %v, want %v", level, got, want)
		}
	}
}

func TestEveryDeclaredLevelHasADecisionAboutListings(t *testing.T) {
	// Guards the day a fourth level is added: it has to be considered
	// here rather than silently defaulting to visible.
	if len(Sensitivities) != 3 {
		t.Fatalf("the vocabulary has %d levels; HiddenFromBroadListing and the "+
			"include_highly_sensitive opt-in were designed for 3 and must be revisited",
			len(Sensitivities))
	}
}

func TestTheDefaultLevelIsOrdinaryAndNotCautious(t *testing.T) {
	// Defaulting to private would label everything private, which has the
	// same effect as labelling nothing.
	if DefaultSensitivity != SensitivityNormal {
		t.Errorf("DefaultSensitivity = %q, want %q", DefaultSensitivity, SensitivityNormal)
	}
	if DefaultSensitivity.HiddenFromBroadListing() {
		t.Error("the default level is hidden from listings, which makes new content invisible")
	}
}

/* ── the order ───────────────────────────────────────────────────────── */

func TestTheLevelsAreOrderedByExposure(t *testing.T) {
	// normal < private < highly_sensitive. If this order is wrong, the
	// provenance floor admits exactly the case it exists to refuse.
	if !(SensitivityNormal.Rank() < SensitivityPrivate.Rank()) {
		t.Error("normal does not rank below private")
	}
	if !(SensitivityPrivate.Rank() < SensitivityHighlySensitive.Rank()) {
		t.Error("private does not rank below highly_sensitive")
	}
}

func TestAtLeastAsRestrictiveAsCoversEveryPair(t *testing.T) {
	// All nine, stated rather than derived: a table built from Rank()
	// would pass whatever Rank() said.
	want := map[Sensitivity]map[Sensitivity]bool{
		SensitivityNormal: {
			SensitivityNormal: true, SensitivityPrivate: false, SensitivityHighlySensitive: false,
		},
		SensitivityPrivate: {
			SensitivityNormal: true, SensitivityPrivate: true, SensitivityHighlySensitive: false,
		},
		SensitivityHighlySensitive: {
			SensitivityNormal: true, SensitivityPrivate: true, SensitivityHighlySensitive: true,
		},
	}
	for level, row := range want {
		for other, expected := range row {
			if got := level.AtLeastAsRestrictiveAs(other); got != expected {
				t.Errorf("%q.AtLeastAsRestrictiveAs(%q) = %v, want %v", level, other, got, expected)
			}
		}
	}
}

func TestAnUnknownLevelFailsClosedInBothDirections(t *testing.T) {
	// A permission predicate that said yes on a value it did not
	// recognise would be wrong in the one direction that costs something.
	bad := Sensitivity("secret")
	if bad.Rank() != -1 {
		t.Errorf("an unknown level ranks %d, want -1", bad.Rank())
	}
	if bad.AtLeastAsRestrictiveAs(SensitivityNormal) {
		t.Error("an unknown level was accepted as restrictive enough")
	}
	if SensitivityHighlySensitive.AtLeastAsRestrictiveAs(bad) {
		t.Error("an unknown level on the right-hand side was cleared")
	}
}

func TestTheOrderIsDeclaredAndNotBorrowedFromTheSlice(t *testing.T) {
	// `Sensitivities` is in ascending order today, which is a convenience
	// for rendering and not a promise. This guards the day somebody
	// reorders it for a screen: the rule that decides what may be
	// withheld must not move with it.
	for _, level := range Sensitivities {
		if level.Rank() < 0 {
			t.Errorf("declared level %q has no rank", level)
		}
	}
	if len(Sensitivities) != 3 {
		t.Fatalf("the vocabulary has %d levels; Rank() was written for 3", len(Sensitivities))
	}
}

func TestMaxSensitivityFindsTheMostWithheld(t *testing.T) {
	cases := []struct {
		name  string
		in    []Sensitivity
		want  Sensitivity
		found bool
	}{
		{"none", nil, DefaultSensitivity, false},
		{"one", []Sensitivity{SensitivityPrivate}, SensitivityPrivate, true},
		{"ascending", []Sensitivity{SensitivityNormal, SensitivityPrivate}, SensitivityPrivate, true},
		{"descending", []Sensitivity{SensitivityHighlySensitive, SensitivityNormal}, SensitivityHighlySensitive, true},
		{"all three", Sensitivities, SensitivityHighlySensitive, true},
	}
	for _, c := range cases {
		got, found := MaxSensitivity(c.in)
		if found != c.found {
			t.Errorf("%s: found = %v, want %v", c.name, found, c.found)
		}
		if found && got != c.want {
			t.Errorf("%s: max = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestAnUnknownLevelPoisonsTheMaximum(t *testing.T) {
	// A floor computed from a value nobody recognises is not a floor. The
	// unknown value is returned so the caller refuses rather than
	// settling for the highest level it did recognise.
	got, found := MaxSensitivity([]Sensitivity{SensitivityNormal, "secret"})
	if !found {
		t.Fatal("a non-empty set reported nothing")
	}
	if got.Valid() {
		t.Errorf("max = %q, want the unrecognised value back", got)
	}
}
