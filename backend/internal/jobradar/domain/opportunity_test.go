package domain

import (
	"strings"
	"testing"
	"time"
)

func TestParseStageAcceptsTheCanonicalNames(t *testing.T) {
	for _, name := range StageNames() {
		got, err := ParseStage(name)
		if err != nil {
			t.Fatalf("ParseStage(%q) returned %v", name, err)
		}
		if string(got) != name {
			t.Fatalf("ParseStage(%q) = %q", name, got)
		}
	}
}

func TestParseStageIsLenientAboutCaseAndSpace(t *testing.T) {
	// The callers are a person typing and a model generating. Both produce
	// these, and refusing them teaches neither anything.
	for _, raw := range []string{"Applied", " applied ", "APPLIED"} {
		got, err := ParseStage(raw)
		if err != nil {
			t.Fatalf("ParseStage(%q) returned %v", raw, err)
		}
		if got != StageApplied {
			t.Fatalf("ParseStage(%q) = %q, want applied", raw, got)
		}
	}
}

func TestParseStageRefusesAnythingElse(t *testing.T) {
	// "apply" is the frontend's Discover-column wording and is NOT a stage.
	// Accepting it by fuzzy match would move a real opportunity somewhere
	// nobody asked for, which is the failure this strictness prevents.
	for _, raw := range []string{"", "apply", "app", "interviewing", "hired", "saved!"} {
		if _, err := ParseStage(raw); err == nil {
			t.Fatalf("ParseStage(%q) was accepted", raw)
		}
	}
}

// The error must name the valid stages. A model told only "invalid" retries
// with another guess; a model told the vocabulary corrects itself.
func TestParseStageErrorListsTheVocabulary(t *testing.T) {
	_, err := ParseStage("nonsense")
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, name := range StageNames() {
		if !strings.Contains(err.Error(), name) {
			t.Fatalf("error %q does not mention stage %q", err.Error(), name)
		}
	}
}

/* ── the move ────────────────────────────────────────────────────────── */

func TestMoveFromDiscoverCreatesTracking(t *testing.T) {
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	o := &Opportunity{Role: "Backend Engineer"}

	result, err := o.Move(StageApplied, now)
	if err != nil {
		t.Fatalf("Move: %v", err)
	}
	if o.Tracking == nil {
		t.Fatal("tracking was not created")
	}
	if o.Tracking.Stage != StageApplied {
		t.Fatalf("stage = %q", o.Tracking.Stage)
	}
	// Entering the pipeline sets both clocks to the same instant.
	if !o.Tracking.TrackedAt.Equal(now) || !o.Tracking.StageEnteredAt.Equal(now) {
		t.Fatalf("clocks = %v / %v, want %v", o.Tracking.TrackedAt, o.Tracking.StageEnteredAt, now)
	}
	// There is no previous stage: this was not a move between stages.
	if result.PreviousStage != nil {
		t.Fatalf("previous stage = %v, want nil", *result.PreviousStage)
	}
	if result.Unchanged {
		t.Fatal("a first entry into the pipeline is not unchanged")
	}
}

func TestMoveBetweenStagesResetsOnlyTheStageClock(t *testing.T) {
	tracked := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	later := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)

	o := &Opportunity{
		Role:     "Backend Engineer",
		Tracking: &Tracking{Stage: StageSaved, TrackedAt: tracked, StageEnteredAt: tracked},
	}

	result, err := o.Move(StageApplied, later)
	if err != nil {
		t.Fatalf("Move: %v", err)
	}
	// This is the distinction the two timestamps exist for: how long in the
	// pipeline, versus how long in THIS stage.
	if !o.Tracking.TrackedAt.Equal(tracked) {
		t.Fatalf("tracked_at moved to %v; it must survive every later move", o.Tracking.TrackedAt)
	}
	if !o.Tracking.StageEnteredAt.Equal(later) {
		t.Fatalf("stage_entered_at = %v, want %v", o.Tracking.StageEnteredAt, later)
	}
	if result.PreviousStage == nil || *result.PreviousStage != StageSaved {
		t.Fatalf("previous stage = %v, want saved", result.PreviousStage)
	}
}

// Moving to the stage a record is already in must not restart its clock.
// The stage clock is what "this application has gone quiet for 12 days" is
// computed from, and a redundant move silently resetting it would erase
// exactly the signal the board exists to surface.
func TestMoveToTheSameStageChangesNothing(t *testing.T) {
	entered := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	later := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)

	o := &Opportunity{
		Tracking: &Tracking{Stage: StageApplied, TrackedAt: entered, StageEnteredAt: entered},
	}

	result, err := o.Move(StageApplied, later)
	if err != nil {
		t.Fatalf("Move: %v", err)
	}
	if !result.Unchanged {
		t.Fatal("result should report Unchanged")
	}
	if !o.Tracking.StageEnteredAt.Equal(entered) {
		t.Fatalf("stage clock restarted to %v", o.Tracking.StageEnteredAt)
	}
}

// Every transition is legal, including backwards. A real search reverses.
func TestMoveAllowsBackwardsTransitions(t *testing.T) {
	now := time.Now().UTC()
	o := &Opportunity{Tracking: &Tracking{Stage: StageOffer, TrackedAt: now, StageEnteredAt: now}}

	if _, err := o.Move(StageRejected, now); err != nil {
		t.Fatalf("offer → rejected: %v", err)
	}
	if _, err := o.Move(StageInterview, now); err != nil {
		t.Fatalf("rejected → interview: %v", err)
	}
}

func TestMoveRefusesAnUnknownStage(t *testing.T) {
	o := &Opportunity{}
	if _, err := o.Move(PipelineStage("hired"), time.Now()); err == nil {
		t.Fatal("an unknown stage was accepted")
	}
	if o.Tracking != nil {
		t.Fatal("a refused move must not create tracking")
	}
}

/* ── validation ──────────────────────────────────────────────────────── */

func TestValidateRequiresARole(t *testing.T) {
	o := &Opportunity{Source: "manual"}
	if err := o.Validate(); err == nil {
		t.Fatal("an opportunity with no role was accepted")
	}
	o.Role = "   "
	if err := o.Validate(); err == nil {
		t.Fatal("a whitespace-only role was accepted")
	}
}

func TestValidateBoundsMatchPercent(t *testing.T) {
	for _, n := range []int{-1, 101} {
		o := &Opportunity{Role: "Engineer", Source: "manual", MatchPercent: &n}
		if err := o.Validate(); err == nil {
			t.Fatalf("match percent %d was accepted", n)
		}
	}
	ok := 73
	o := &Opportunity{Role: "Engineer", Source: "manual", MatchPercent: &ok}
	if err := o.Validate(); err != nil {
		t.Fatalf("match percent 73 was refused: %v", err)
	}
}

// Nil match percent is "nobody scored this" and must stay valid — it is
// the normal state of a manually entered posting.
func TestValidateAcceptsAnUnscoredOpportunity(t *testing.T) {
	o := &Opportunity{Role: "Engineer", Source: "manual"}
	if err := o.Validate(); err != nil {
		t.Fatalf("an unscored opportunity was refused: %v", err)
	}
}

func TestNormalizeStackDropsEmptiesAndKeepsOrder(t *testing.T) {
	got := NormalizeStack([]string{" Go ", "", "  ", "Postgres", "React"})
	want := []string{"Go", "Postgres", "React"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestNormalizeCompanyNameRefusesEmpty(t *testing.T) {
	if _, err := NormalizeCompanyName("   "); err == nil {
		t.Fatal("a blank company name was accepted")
	}
	// Casing is preserved: the stored name is what appears on screen.
	got, err := NormalizeCompanyName("  Stripe ")
	if err != nil {
		t.Fatalf("NormalizeCompanyName: %v", err)
	}
	if got != "Stripe" {
		t.Fatalf("got %q, want %q", got, "Stripe")
	}
}

func TestStageReportsWhetherTheRecordIsTracked(t *testing.T) {
	discover := &Opportunity{}
	if _, tracked := discover.Stage(); tracked {
		t.Fatal("a record with no tracking reported a stage")
	}
	now := time.Now()
	pipeline := &Opportunity{Tracking: &Tracking{Stage: StageOffer, TrackedAt: now, StageEnteredAt: now}}
	s, tracked := pipeline.Stage()
	if !tracked || s != StageOffer {
		t.Fatalf("Stage() = %q, %v", s, tracked)
	}
}
