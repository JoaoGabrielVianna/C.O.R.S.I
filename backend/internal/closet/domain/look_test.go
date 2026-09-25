package domain

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

// tick hands out strictly increasing instants, so "the order they were
// chosen" is a real ordering rather than a tie the sort does not promise.
func tick() func() time.Time {
	base := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	n := 0
	return func() time.Time {
		n++
		return base.Add(time.Duration(n) * time.Second)
	}
}

func place(t *testing.T, c *Composition, slot Slot, id uuid.UUID, now time.Time) []LookItem {
	t.Helper()
	replaced, err := c.Place(slot, id, now)
	if err != nil {
		t.Fatalf("Place(%s) failed: %v", slot, err)
	}
	return replaced
}

func TestPlacingASecondPieceInASingleSlotReplacesTheFirst(t *testing.T) {
	t.Parallel()
	// ══════════════════════════════════════════════════════════════════
	// This is the interaction the whole module exists to get right:
	// clicking a second shirt SWAPS the shirt. It does not stack.
	// ══════════════════════════════════════════════════════════════════
	now := tick()
	c := NewComposition(nil)
	first, second := uuid.New(), uuid.New()

	if replaced := place(t, c, SlotTop, first, now()); len(replaced) != 0 {
		t.Fatalf("placing into an empty slot displaced %d pieces", len(replaced))
	}
	replaced := place(t, c, SlotTop, second, now())

	if len(replaced) != 1 || replaced[0].ItemID != first {
		t.Fatalf("placing a second top did not report the first as replaced: %+v", replaced)
	}
	items := c.Items()
	if len(items) != 1 {
		t.Fatalf("the composition holds %d tops, want 1", len(items))
	}
	if items[0].ItemID != second {
		t.Fatalf("the slot holds %s, want the second piece %s", items[0].ItemID, second)
	}
}

func TestAMultiOccupancySlotAccumulatesUntilItIsFull(t *testing.T) {
	t.Parallel()
	now := tick()
	c := NewComposition(nil)

	capacity, ok := SlotCapacity(SlotAccessory)
	if !ok {
		t.Fatal("accessory is not a slot")
	}
	ids := make([]uuid.UUID, capacity)
	for i := range ids {
		ids[i] = uuid.New()
		if replaced := place(t, c, SlotAccessory, ids[i], now()); len(replaced) != 0 {
			t.Fatalf("accessory %d displaced something before the slot was full", i)
		}
	}
	if got := len(c.Items()); got != capacity {
		t.Fatalf("the slot holds %d accessories, want %d", got, capacity)
	}

	// One past capacity evicts the OLDEST, so a person adding a fifth
	// accessory gets a fifth accessory rather than a refusal they have to go
	// and resolve.
	overflow := uuid.New()
	replaced := place(t, c, SlotAccessory, overflow, now())
	if len(replaced) != 1 || replaced[0].ItemID != ids[0] {
		t.Fatalf("overflow evicted %+v, want the first accessory %s", replaced, ids[0])
	}
	if got := len(c.Items()); got != capacity {
		t.Fatalf("after overflow the slot holds %d, want %d", got, capacity)
	}
}

func TestPositionsStayDenseAfterARemoval(t *testing.T) {
	t.Parallel()
	// Position is part of the primary key. A slot left at 0,2 would make the
	// next append collide or skip forever.
	now := tick()
	c := NewComposition(nil)
	a, b, d := uuid.New(), uuid.New(), uuid.New()
	place(t, c, SlotAccessory, a, now())
	place(t, c, SlotAccessory, b, now())
	place(t, c, SlotAccessory, d, now())

	if !c.Remove(b) {
		t.Fatal("Remove reported that a present piece was absent")
	}
	for i, entry := range c.Items() {
		if entry.Position != i {
			t.Fatalf("position %d at index %d; positions are not dense: %+v",
				entry.Position, i, c.Items())
		}
	}
}

func TestTheSamePieceCannotBeInOneLookTwice(t *testing.T) {
	t.Parallel()
	now := tick()
	c := NewComposition(nil)
	id := uuid.New()
	place(t, c, SlotAccessory, id, now())
	place(t, c, SlotAccessory, id, now())

	if got := len(c.Items()); got != 1 {
		t.Fatalf("the same piece was filed %d times, want 1", got)
	}
}

func TestPlacingAPieceIntoADifferentSlotMovesIt(t *testing.T) {
	t.Parallel()
	// The caller asked for this garment in this position. That is a
	// statement about where it goes, not an error.
	now := tick()
	c := NewComposition(nil)
	id := uuid.New()
	place(t, c, SlotTop, id, now())
	place(t, c, SlotLayer, id, now())

	items := c.Items()
	if len(items) != 1 {
		t.Fatalf("moving a piece left %d entries, want 1", len(items))
	}
	if items[0].Slot != SlotLayer {
		t.Fatalf("the piece is in %q, want %q", items[0].Slot, SlotLayer)
	}
}

func TestRemovingAPieceThatIsNotThereIsANoOp(t *testing.T) {
	t.Parallel()
	c := NewComposition(nil)
	if c.Remove(uuid.New()) {
		t.Fatal("Remove claimed to have removed something from an empty composition")
	}
}

func TestPlaceRefusesAnUnknownSlot(t *testing.T) {
	t.Parallel()
	c := NewComposition(nil)
	if _, err := c.Place(Slot("hat"), uuid.New(), time.Now()); err == nil {
		t.Fatal("Place accepted a slot that does not exist")
	}
}

func TestItemsComeBackInCompositionOrder(t *testing.T) {
	t.Parallel()
	// The order is the one slotOrder declares, not the order things were
	// placed — a look re-opened next year has to draw the same way.
	now := tick()
	c := NewComposition(nil)
	watch, shoes, top := uuid.New(), uuid.New(), uuid.New()
	place(t, c, SlotWatch, watch, now())
	place(t, c, SlotTop, top, now())
	place(t, c, SlotShoes, shoes, now())

	want := []Slot{SlotTop, SlotShoes, SlotWatch}
	got := c.Items()
	if len(got) != len(want) {
		t.Fatalf("composition holds %d pieces, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Slot != want[i] {
			t.Fatalf("slot at index %d is %q, want %q (full order: %+v)",
				i, got[i].Slot, want[i], got)
		}
	}
}

func TestClearSlotEmptiesOnlyThatSlot(t *testing.T) {
	t.Parallel()
	now := tick()
	c := NewComposition(nil)
	place(t, c, SlotAccessory, uuid.New(), now())
	place(t, c, SlotAccessory, uuid.New(), now())
	place(t, c, SlotTop, uuid.New(), now())

	if removed := c.ClearSlot(SlotAccessory); removed != 2 {
		t.Fatalf("ClearSlot removed %d, want 2", removed)
	}
	items := c.Items()
	if len(items) != 1 || items[0].Slot != SlotTop {
		t.Fatalf("ClearSlot touched the wrong slot: %+v", items)
	}
}

func TestLookValidateRefusesWhatWouldBeUnreadable(t *testing.T) {
	t.Parallel()
	base := func() *Look {
		return &Look{Name: "Sexta", Occasion: OccasionCasual, Status: StatusActive}
	}
	if err := base().Validate(); err != nil {
		t.Fatalf("a plain look was refused: %v", err)
	}

	empty := base()
	empty.Name = "   "
	if err := empty.Validate(); err == nil {
		t.Fatal("a look with a blank name was accepted")
	}

	unknown := base()
	unknown.Occasion = Occasion("brunch")
	if err := unknown.Validate(); err == nil {
		t.Fatal("a look with an unknown occasion was accepted")
	}
}

func TestALookWithOnePieceIsValid(t *testing.T) {
	t.Parallel()
	// A draft is a real thing. Refusing it would mean the builder could not
	// save work in progress.
	l := &Look{Name: "Em construção", Occasion: OccasionOther, Status: StatusActive}
	if err := l.Validate(); err != nil {
		t.Fatalf("a one-piece look was refused: %v", err)
	}
}
