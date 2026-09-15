package domain

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func validItem() *ArtifactItem {
	return &ArtifactItem{
		ID:          uuid.New(),
		WorkspaceID: uuid.New(),
		ArtifactID:  uuid.New(),
		Position:    0,
		Text:        "Ler o capítulo 3",
	}
}

func TestAWellFormedItemValidates(t *testing.T) {
	if err := validItem().Validate(); err != nil {
		t.Fatalf("a valid item was refused: %v", err)
	}
}

func TestAnItemWithoutAnArtifactIsRefused(t *testing.T) {
	// An entry of nothing would be unreachable through the only read that
	// exists for it.
	i := validItem()
	i.ArtifactID = uuid.Nil
	assertInvalid(t, i.Validate(), "artifact")
}

func TestAnItemWithoutAWorkspaceIsRefused(t *testing.T) {
	i := validItem()
	i.WorkspaceID = uuid.Nil
	assertInvalid(t, i.Validate(), "workspace")
}

func TestAnItemNeedsTextAndBoundsIt(t *testing.T) {
	i := validItem()
	i.Text = "   "
	assertInvalid(t, i.Validate(), "text")

	i = validItem()
	i.Text = strings.Repeat("ç", MaxItemText+1)
	assertInvalid(t, i.Validate(), "text")

	i = validItem()
	i.Text = strings.Repeat("ç", MaxItemText)
	if err := i.Validate(); err != nil {
		t.Fatalf("%d accented characters were refused: %v", MaxItemText, err)
	}
}

func TestAnItemPositionCannotBeNegative(t *testing.T) {
	// A negative position would sort before everything forever, which is a
	// way of pinning an entry that nobody asked for and nothing documents.
	i := validItem()
	i.Position = -1
	assertInvalid(t, i.Validate(), "position")
}

/* ── the change ──────────────────────────────────────────────────────── */

func TestAnEmptyItemChangeIsRecognisable(t *testing.T) {
	if !(ItemChange{}).Empty() {
		t.Error("the zero ItemChange does not report empty")
	}
	done := true
	if (ItemChange{Done: &done}).Empty() {
		t.Error("a change that ticks the box reports empty")
	}
}

func TestTickingAnItemReportsWhetherItWasAlreadyTicked(t *testing.T) {
	// "marquei como feito" and "já estava feito" are different answers to
	// the same request, and only the first one is work.
	i := validItem()
	done := true

	res := i.Apply(ItemChange{Done: &done})
	if !res.DoneChanged || res.PreviousDone {
		t.Errorf("first tick misreported: %+v", res)
	}

	res = i.Apply(ItemChange{Done: &done})
	if res.DoneChanged {
		t.Error("ticking an already-ticked entry reported a change")
	}
	if !res.PreviousDone {
		t.Error("PreviousDone did not carry the state the caller found")
	}
	if !res.Unchanged() {
		t.Error("a second tick reported movement")
	}
}

func TestItemTextIsTrimmedOnEdit(t *testing.T) {
	i := validItem()
	padded := "  Ler o capítulo 4  "

	res := i.Apply(ItemChange{Text: &padded})

	if !res.TextChanged {
		t.Fatal("the text change went unreported")
	}
	if i.Text != "Ler o capítulo 4" {
		t.Errorf("Text = %q, want it trimmed", i.Text)
	}
}

func TestResendingTheStoredItemTextIsNotAChange(t *testing.T) {
	i := validItem()
	same := "  " + i.Text + "  "

	if res := i.Apply(ItemChange{Text: &same}); res.TextChanged {
		t.Error("resending the stored text, padded, was reported as a change")
	}
}

func TestMovingAnItemReportsThePositionChange(t *testing.T) {
	i := validItem()
	third := 3

	res := i.Apply(ItemChange{Position: &third})

	if !res.PositionChanged || i.Position != 3 {
		t.Errorf("the move was not applied: position=%d %+v", i.Position, res)
	}
}
