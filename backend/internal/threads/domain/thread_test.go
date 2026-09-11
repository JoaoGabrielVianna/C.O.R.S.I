package domain

import (
	"strings"
	"testing"
)

/* ── the status vocabulary ───────────────────────────────────────────── */

// The enum and the list must not drift. StatusNames() is what the tool
// schemas describe themselves with, so a status that exists in the type and
// not in the slice would be a state the model is never told about — and
// therefore one the user can never reach through a conversation.
func TestEveryStatusIsInTheCanonicalList(t *testing.T) {
	for _, s := range []Status{
		StatusIdea, StatusDraft, StatusReview, StatusPublished, StatusArchived,
	} {
		if !s.Valid() {
			t.Errorf("%q is a declared status and does not validate", s)
		}
		found := false
		for _, listed := range Statuses {
			if listed == s {
				found = true
			}
		}
		if !found {
			t.Errorf("%q is a declared status and is missing from Statuses", s)
		}
	}
	if len(Statuses) != len(StatusNames()) {
		t.Fatalf("Statuses and StatusNames disagree: %d vs %d", len(Statuses), len(StatusNames()))
	}
}

// The lifecycle is deliberately five states. This test is the gate on that
// decision: a sixth added without a conversation fails here, which is the
// moment to decide whether the vocabulary should grow.
func TestTheLifecycleIsFiveStates(t *testing.T) {
	if len(Statuses) != 5 {
		t.Fatalf("the lifecycle has %d states, want 5: %v", len(Statuses), StatusNames())
	}
}

// Lenient about case and space, because the callers are a person and a
// model and both will send "Draft" or " draft ".
func TestParseStatusAcceptsWhatACallerActuallySends(t *testing.T) {
	for _, raw := range []string{"draft", "Draft", "DRAFT", "  draft  "} {
		got, err := ParseStatus(raw)
		if err != nil {
			t.Fatalf("ParseStatus(%q): %v", raw, err)
		}
		if got != StatusDraft {
			t.Errorf("ParseStatus(%q) = %q, want draft", raw, got)
		}
	}
}

// And strict about everything else: an approximate match would move real
// work to a state nobody asked for.
func TestParseStatusRefusesToGuess(t *testing.T) {
	for _, raw := range []string{"", "reviewing", "publish", "done", "ready", "idea "} {
		if raw == "idea " {
			// Trailing space IS accepted — this entry documents the line
			// between leniency and guessing rather than asserting a refusal.
			if _, err := ParseStatus(raw); err != nil {
				t.Errorf("ParseStatus(%q) should trim, got %v", raw, err)
			}
			continue
		}
		if _, err := ParseStatus(raw); err == nil {
			t.Errorf("ParseStatus(%q) was accepted; approximate matches must be refused", raw)
		}
	}
}

// `ready` was considered and dropped. If it comes back it must come back
// through the enum, not through a parser that quietly tolerates it.
func TestReadyIsNotAStatus(t *testing.T) {
	if _, err := ParseStatus("ready"); err == nil {
		t.Fatal("'ready' parsed; it is not part of this lifecycle")
	}
}

// The refusal names the vocabulary, so a model that sent a wrong status
// learns what the right ones are instead of guessing again.
func TestAnUnknownStatusIsExplained(t *testing.T) {
	_, err := ParseStatus("scheduled")
	if err == nil {
		t.Fatal("expected a refusal")
	}
	for _, name := range StatusNames() {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("the refusal does not mention %q: %v", name, err)
		}
	}
}

/* ── validation ──────────────────────────────────────────────────────── */

func TestAThreadNeedsATitle(t *testing.T) {
	for _, title := range []string{"", "   ", "\n\t"} {
		th := &Thread{Title: title, Status: StatusIdea}
		if err := th.Validate(); err == nil {
			t.Errorf("a thread titled %q was accepted", title)
		}
	}
}

// Content is optional. A captured idea can be a title and nothing else, and
// refusing that would refuse the first thing this module is asked to do.
func TestAThreadMayHaveNoContent(t *testing.T) {
	th := &Thread{Title: "Microservices cedo demais", Status: StatusIdea}
	if err := th.Validate(); err != nil {
		t.Fatalf("an idea with no text was refused: %v", err)
	}
}

func TestBoundsAreEnforced(t *testing.T) {
	long := strings.Repeat("a", MaxTitle+1)
	if err := (&Thread{Title: long, Status: StatusIdea}).Validate(); err == nil {
		t.Error("an over-long title was accepted")
	}
	body := strings.Repeat("a", MaxContent+1)
	if err := (&Thread{Title: "ok", Content: body, Status: StatusIdea}).Validate(); err == nil {
		t.Error("over-long content was accepted")
	}
}

// The bounds count RUNES, not bytes. A title of accented Portuguese is not
// half as long as an ASCII one.
func TestBoundsCountCharactersNotBytes(t *testing.T) {
	th := &Thread{Title: strings.Repeat("ç", MaxTitle), Status: StatusIdea}
	if err := th.Validate(); err != nil {
		t.Fatalf("a title of exactly %d characters was refused: %v", MaxTitle, err)
	}
}

func TestAnUnknownStatusIsRefusedOnWrite(t *testing.T) {
	th := &Thread{Title: "ok", Status: Status("scheduled")}
	if err := th.Validate(); err == nil {
		t.Fatal("a thread with an invented status was accepted")
	}
}

/* ── the change ──────────────────────────────────────────────────────── */

func str(s string) *string { return &s }
func st(s Status) *Status  { return &s }
func base() *Thread {
	return &Thread{Title: "Microservices cedo demais", Content: "draft A", Status: StatusDraft}
}

// Absent means unchanged. This is the whole grammar of an update, and it is
// what stops "marca como review" from touching a word of the text.
func TestAnAbsentFieldIsNotTouched(t *testing.T) {
	th := base()
	res := th.Apply(Change{Status: st(StatusReview)})

	if th.Content != "draft A" {
		t.Errorf("content = %q; a status change rewrote the text", th.Content)
	}
	if th.Title != "Microservices cedo demais" {
		t.Errorf("title = %q; a status change renamed the piece", th.Title)
	}
	if res.ContentChanged || res.TitleChanged {
		t.Errorf("the result claims a change that did not happen: %+v", res)
	}
	if !res.StatusChanged || res.PreviousStatus != StatusDraft {
		t.Errorf("the status move was not reported: %+v", res)
	}
}

// A change that sets a field to the value it already holds moved nothing,
// and saying so is what stops a model reporting work it did not do.
func TestSendingTheSameValueIsNotAChange(t *testing.T) {
	th := base()
	res := th.Apply(Change{Content: str("draft A"), Status: st(StatusDraft)})
	if !res.Unchanged() {
		t.Fatalf("a no-op edit reported movement: %+v", res)
	}
}

func TestAnEmptyChangeIsEmpty(t *testing.T) {
	if !(Change{}).Empty() {
		t.Fatal("a change with no fields did not report itself empty")
	}
	if (Change{Status: st(StatusIdea)}).Empty() {
		t.Fatal("a change carrying a status reported itself empty")
	}
}

// Content replaces wholesale. It is not appended to, not merged, and not
// diffed — the tool contract says "the complete new text", and this is that
// promise in the domain.
func TestContentIsReplacedNotMerged(t *testing.T) {
	th := base()
	th.Apply(Change{Content: str("draft B, entirely different")})
	if th.Content != "draft B, entirely different" {
		t.Fatalf("content = %q", th.Content)
	}
}

// A title arrives from a model and may carry padding. It is trimmed on the
// way in, so two threads do not differ by a space nobody can see.
func TestATitleIsTrimmedOnTheWayIn(t *testing.T) {
	th := base()
	res := th.Apply(Change{Title: str("  Novo título  ")})
	if th.Title != "Novo título" {
		t.Fatalf("title = %q, want trimmed", th.Title)
	}
	if !res.TitleChanged {
		t.Error("the rename was not reported")
	}
	// And padding around the SAME title is not a change.
	res = th.Apply(Change{Title: str(" Novo título ")})
	if res.TitleChanged {
		t.Error("re-sending the same title with different padding reported a rename")
	}
}

// Every transition is legal, in both directions. Content genuinely goes
// backwards — a published post pulled back to draft to be reworked — and a
// state machine here would make the system refuse the truth.
func TestEveryTransitionIsAllowed(t *testing.T) {
	for _, from := range Statuses {
		for _, to := range Statuses {
			th := &Thread{Title: "t", Status: from}
			th.Apply(Change{Status: st(to)})
			if th.Status != to {
				t.Errorf("%s → %s was refused", from, to)
			}
		}
	}
}
