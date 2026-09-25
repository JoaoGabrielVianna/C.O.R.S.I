package domain

import (
	"strings"
	"testing"
)

// The catalog's invariants are also checked by init(), which panics. This
// suite exists so a broken entry is reported as a named failing test rather
// than as a package that refuses to load — and so the REASONS are stated
// where somebody adding a category will read them.

func TestEveryCategoryFillsARealSlot(t *testing.T) {
	t.Parallel()
	for _, def := range Categories() {
		if !def.Slot.Valid() {
			t.Errorf("category %q fills slot %q, which is not in the slot vocabulary",
				def.Category, def.Slot)
		}
	}
}

func TestEveryCompositionPreferenceIsAViewTheCategoryCanCarry(t *testing.T) {
	t.Parallel()
	// The failure this prevents: a category that prefers an angle it is not
	// allowed to have renders nothing in the builder, forever, and looks
	// like a broken image rather than a broken vocabulary.
	for _, def := range Categories() {
		allowed := map[ImageView]bool{}
		for _, v := range def.Views {
			allowed[v] = true
		}
		for _, v := range def.Composition {
			if !allowed[v] {
				t.Errorf("category %q prefers %q for composition but cannot carry it",
					def.Category, v)
			}
		}
	}
}

func TestEverySlotIsFilledByAtLeastOneCategory(t *testing.T) {
	t.Parallel()
	// A slot no category fills is a row the builder draws empty and nobody
	// can ever fill — which is what a rename of one side without the other
	// produces.
	filled := map[Slot]bool{}
	for _, def := range Categories() {
		filled[def.Slot] = true
	}
	for _, def := range Slots() {
		if !filled[def.Slot] {
			t.Errorf("slot %q is filled by no category", def.Slot)
		}
	}
}

func TestAWatchCannotHangOnAHanger(t *testing.T) {
	t.Parallel()
	// The single clearest statement of why views are per-category.
	if _, err := ParseView(CategoryWatches, "hanger_front"); err == nil {
		t.Fatal("a watch accepted a hanger view")
	}
	if _, err := ParseView(CategoryWatches, "front"); err != nil {
		t.Fatalf("a watch refused its own front view: %v", err)
	}
	if _, err := ParseView(CategoryTops, "hanger_front"); err != nil {
		t.Fatalf("a top refused a hanger view: %v", err)
	}
}

func TestParseViewNamesTheViewsItWouldHaveAccepted(t *testing.T) {
	t.Parallel()
	// An error that only says "no" makes the caller go and read the source.
	_, err := ParseView(CategoryShoes, "folded")
	if err == nil {
		t.Fatal("shoes accepted a folded view")
	}
	for _, want := range []string{"side", "top", "front"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q: %s", want, err)
		}
	}
}

func TestParseViewIsLenientAboutCaseAndSpace(t *testing.T) {
	t.Parallel()
	got, err := ParseView(CategoryTops, "  Folded ")
	if err != nil {
		t.Fatalf("unexpected refusal: %v", err)
	}
	if got != ViewFolded {
		t.Fatalf("view = %q, want %q", got, ViewFolded)
	}
}

func TestParseCategoryRefusesANearMiss(t *testing.T) {
	t.Parallel()
	// "top" is not "tops". Guessing here would file a garment under a
	// category nobody chose, which is worse than an error.
	if _, err := ParseCategory("top"); err == nil {
		t.Fatal("ParseCategory guessed that \"top\" meant \"tops\"")
	}
	if _, err := ParseCategory(" TOPS "); err != nil {
		t.Fatalf("ParseCategory refused a differently-cased known category: %v", err)
	}
}

func TestParseSlotRefusesANearMiss(t *testing.T) {
	t.Parallel()
	if _, err := ParseSlot("shoe"); err == nil {
		t.Fatal("ParseSlot guessed that \"shoe\" meant \"shoes\"")
	}
}

func TestAccessoryIsTheOnlyMultiOccupancySlot(t *testing.T) {
	t.Parallel()
	// Not a style preference: Composition.Place evicts when a slot is full,
	// and which slots that applies to is the whole behaviour of the builder.
	for _, def := range Slots() {
		want := 1
		if def.Slot == SlotAccessory {
			want = 4
		}
		if def.Capacity != want {
			t.Errorf("slot %q capacity = %d, want %d", def.Slot, def.Capacity, want)
		}
	}
}

func TestParseOccasionTreatsEmptyAsOther(t *testing.T) {
	t.Parallel()
	// The field is optional on every surface that offers it. Failing on
	// empty would force a choice, and the field would fill up with whichever
	// option sat first in a dropdown.
	got, err := ParseOccasion("")
	if err != nil {
		t.Fatalf("unexpected refusal: %v", err)
	}
	if got != OccasionOther {
		t.Fatalf("occasion = %q, want %q", got, OccasionOther)
	}
	if _, err := ParseOccasion("brunch"); err == nil {
		t.Fatal("ParseOccasion accepted an unknown occasion")
	}
}
