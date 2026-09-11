package app

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/corsi/backend/internal/chat/domain"
	"github.com/corsi/backend/internal/chat/ports"
)

// What the model reads, and what the account says it cost. Both, together,
// because a block that reaches the model without appearing in the report is
// a silent charge, and hydration runs on every turn.

func subject(id, label, subtitle string) domain.ContextReference {
	return domain.ContextReference{
		Type: "job_radar.opportunity", ID: id, Label: label, Subtitle: subtitle,
	}
}

func hydratedCurrent(ref domain.ContextReference, fields ...ports.ReferenceStateField) HydratedReference {
	return HydratedReference{Reference: ref, Status: domain.HydrationCurrent, Fields: fields}
}

func field(name, value string) ports.ReferenceStateField {
	return ports.ReferenceStateField{Name: name, Value: value}
}

/* ── the block the model reads ───────────────────────────────────────── */

func TestTheStateBlockCarriesTheFieldsAndTheIdentity(t *testing.T) {
	ref := subject("3e83eeca", "Acme · Backend Engineer", "Remote (BR)")
	got := RenderReferenceStateBlock([]HydratedReference{
		hydratedCurrent(ref, field("stage", "interview"), field("location", "Remote (BR)")),
	})

	for _, want := range []string{
		"Acme · Backend Engineer", // which item
		"job_radar.opportunity",   // of what kind
		"3e83eeca",                // the id, so a follow-up tool call needs no correlation by label
		"stage: interview",
		"location: Remote (BR)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the block does not carry %q:\n%s", want, got)
		}
	}
}

// The precedence claim is the block's whole job against a history that
// disagrees. It is a tiebreak rather than the mechanism — the mechanism is
// that the state is present at all — but a block that did not say which of
// the two is newer would be asking the model to guess.
func TestTheStateBlockClaimsPrecedenceOverTheHistory(t *testing.T) {
	got := RenderReferenceStateBlock([]HydratedReference{
		hydratedCurrent(subject("id", "Acme · Backend Engineer", ""), field("stage", "interview")),
	})
	for _, want := range []string{"MORE RECENT", "your own earlier answers", "read just now"} {
		if !strings.Contains(got, want) {
			t.Errorf("the block does not say %q:\n%s", want, got)
		}
	}
}

// Each way of failing gets its own sentence, because they license different
// answers to the user. Collapsing them would have the model announce a
// deletion that was really a revoked grant.
func TestEachFailureTellsTheModelSomethingDifferent(t *testing.T) {
	ref := subject("id", "Acme · Backend Engineer", "")
	cases := []struct {
		status domain.HydrationStatus
		want   string
		reject string
	}{
		{domain.HydrationUnauthorized, "not authorized", "could not be found"},
		{domain.HydrationUnavailable, "could not be found", "not authorized"},
		// The failed sentence says "do not say it was removed" on purpose:
		// announcing a deletion that did not happen is the worse of the two
		// mistakes by a wide margin.
		{domain.HydrationFailed, "the read did not succeed", "not authorized"},
	}
	for _, c := range cases {
		got := RenderReferenceStateBlock([]HydratedReference{{Reference: ref, Status: c.status}})
		if !strings.Contains(got, c.want) {
			t.Errorf("%s: missing %q:\n%s", c.status, c.want, got)
		}
		if strings.Contains(got, c.reject) {
			t.Errorf("%s: says %q, which is a different fact:\n%s", c.status, c.reject, got)
		}
		// Every failure tells the model what NOT to do, because silence is
		// what it fills from the history.
		if !strings.Contains(got, "NOT AVAILABLE") {
			t.Errorf("%s: does not mark the state as absent:\n%s", c.status, got)
		}
	}
}

// A field value spanning lines would break the one-field-per-line shape and
// let a provider's text be read as another field.
func TestAMultilineFieldStaysOnOneLine(t *testing.T) {
	got := RenderReferenceStateBlock([]HydratedReference{
		hydratedCurrent(subject("id", "L", ""), field("next_action", "call them\nthen email")),
	})
	if strings.Contains(got, "call them\nthen email") {
		t.Errorf("a field value kept its newline:\n%s", got)
	}
	if !strings.Contains(got, "call them then email") {
		t.Errorf("the value was lost rather than flattened:\n%s", got)
	}
}

func TestAnEmptyHydrationRendersNothing(t *testing.T) {
	if got := RenderReferenceStateBlock(nil); got != "" {
		t.Errorf("an empty list produced a header on its own:\n%s", got)
	}
}

/* ── identity and state do not contradict each other ─────────────────── */

// The subtitle is frozen recognition text. When the same subject's present
// state is in the very next block, printing it too would put two answers to
// one question in front of the model — which is the failure this batch
// exists to remove, not a warning to be worded around.
func TestTheIdentityBlockWithholdsAFrozenSubtitleWhenStateIsShown(t *testing.T) {
	ref := subject("id", "Acme · Backend Engineer", "applied · Remote (BR)")

	withState := RenderContextReferencesBlock(
		[]domain.ContextReference{ref},
		hydratedKeys([]HydratedReference{hydratedCurrent(ref, field("stage", "interview"))}),
	)
	if strings.Contains(withState, "applied") {
		t.Errorf("the frozen subtitle was printed beside a live state block:\n%s", withState)
	}
	if !strings.Contains(withState, "Acme · Backend Engineer") {
		t.Errorf("the label was withheld along with the subtitle:\n%s", withState)
	}

	// With nothing hydrated the subtitle is the only recognition text there
	// is, so it is printed — and the warning about its age comes with it.
	alone := RenderContextReferencesBlock([]domain.ContextReference{ref}, nil)
	if !strings.Contains(alone, "applied · Remote (BR)") {
		t.Errorf("the subtitle was withheld with no state block to replace it:\n%s", alone)
	}
	if !strings.Contains(alone, "may be out of date") {
		t.Errorf("a frozen description was printed with no warning about its age:\n%s", alone)
	}
}

// The warning is charged only when there is something to warn about. A
// sentence telling the model to distrust a description that is not on the
// page is a cost with no referent.
func TestTheStalenessWarningIsAbsentWhenNoDescriptionWasPrinted(t *testing.T) {
	ref := subject("id", "Acme · Backend Engineer", "")
	got := RenderContextReferencesBlock([]domain.ContextReference{ref}, nil)
	if strings.Contains(got, "may be out of date") {
		t.Errorf("the block warns about a description it did not print:\n%s", got)
	}
	// Identity still reaches the model, which is the block's actual job.
	if !strings.Contains(got, "job_radar.opportunity") || !strings.Contains(got, "id") {
		t.Errorf("identity did not reach the model:\n%s", got)
	}
}

/* ── the account ─────────────────────────────────────────────────────── */

// Every character that reached the model is counted, and the block's own
// Items counts the subjects that carried STATE — not the lines, and not the
// one message they were rendered into.
func TestTheStateBlockIsChargedForExactlyWhatItSends(t *testing.T) {
	a := subject("a", "Acme · Backend Engineer", "Remote (BR)")
	b := subject("b", "Globex · SRE", "São Paulo")
	hydrated := []HydratedReference{
		hydratedCurrent(a, field("stage", "interview")),
		{Reference: b, Status: domain.HydrationUnauthorized},
	}

	built := BuildContext(ContextInput{
		Agent:              &domain.Agent{SystemPrompt: "you help"},
		ContextReferences:  []domain.ContextReference{a, b},
		HydratedReferences: hydrated,
	})

	block, ok := built.Report.Block(domain.BlockReferenceState)
	if !ok {
		t.Fatal("the state block was sent without being reported")
	}
	// One subject carried state; the other was refused and is an exclusion.
	if block.Items != 1 {
		t.Errorf("items = %d, want 1", block.Items)
	}
	if want := utf8.RuneCountInString(RenderReferenceStateBlock(hydrated)); block.Characters != want {
		t.Errorf("characters = %d, want %d — the account and the wire disagree",
			block.Characters, want)
	}
	if len(block.Exclusions) != 1 ||
		block.Exclusions[0].Reason != domain.ReasonUnauthorized ||
		block.Exclusions[0].Items != 1 {
		t.Fatalf("exclusions = %+v, want one %q for one item",
			block.Exclusions, domain.ReasonUnauthorized)
	}
	// The refusal sentence is not free: it went on the wire.
	if block.Exclusions[0].Characters != 0 {
		t.Errorf("the exclusion claims %d characters; what was excluded is the STATE, "+
			"which put nothing on the wire — the sentence saying so is in the block's own count",
			block.Exclusions[0].Characters)
	}
}

// Unavailable and failed are two sentences to the model and one reason in
// the report: the reader's next action is the same either way, and the
// count that matters is "how many subjects ran without their state".
func TestUnavailableAndFailedAreOneReasonInTheReport(t *testing.T) {
	a := subject("a", "A", "")
	b := subject("b", "B", "")
	built := BuildContext(ContextInput{
		ContextReferences: []domain.ContextReference{a, b},
		HydratedReferences: []HydratedReference{
			{Reference: a, Status: domain.HydrationUnavailable},
			{Reference: b, Status: domain.HydrationFailed},
		},
	})
	block, _ := built.Report.Block(domain.BlockReferenceState)
	if len(block.Exclusions) != 1 ||
		block.Exclusions[0].Reason != domain.ReasonUnavailable ||
		block.Exclusions[0].Items != 2 {
		t.Fatalf("exclusions = %+v, want one %q covering 2 items",
			block.Exclusions, domain.ReasonUnavailable)
	}
}

// A turn with no subjects is byte-identical to what it was before hydration
// existed, which is every turn of almost every agent in this system.
func TestATurnWithNoSubjectsCarriesNoStateBlock(t *testing.T) {
	built := BuildContext(ContextInput{Agent: &domain.Agent{SystemPrompt: "you help"}})
	if _, ok := built.Report.Block(domain.BlockReferenceState); ok {
		t.Error("a turn with no subjects reported a state block")
	}
	for _, m := range built.Messages {
		if strings.Contains(m.Content, "Current state of the attached items") {
			t.Error("a turn with no subjects was sent a state block")
		}
	}
}

// State sits immediately after identity. They are one thought split across
// two blocks for accounting, and a reader — human or model — should not have
// memory and sources wedged between "which opportunity" and "what it is
// doing now".
func TestStateFollowsIdentityImmediately(t *testing.T) {
	ref := subject("id", "Acme · Backend Engineer", "Remote (BR)")
	built := BuildContext(ContextInput{
		Agent:              &domain.Agent{SystemPrompt: "you help"},
		Memories:           []domain.Memory{{Content: "prefere remoto"}},
		ContextReferences:  []domain.ContextReference{ref},
		HydratedReferences: []HydratedReference{hydratedCurrent(ref, field("stage", "interview"))},
	})

	identity, state := -1, -1
	for i, m := range built.Messages {
		switch {
		case strings.Contains(m.Content, "They identify what is being discussed"):
			identity = i
		case strings.Contains(m.Content, "Current state of the attached items"):
			state = i
		}
	}
	if identity < 0 || state < 0 {
		t.Fatalf("blocks missing: identity=%d state=%d", identity, state)
	}
	if state != identity+1 {
		t.Errorf("state is at %d and identity at %d; they must be adjacent", state, identity)
	}
}

/* ── the status vocabulary ───────────────────────────────────────────── */

// Only one status means the state was actually read. The distinction is
// derived in one place so no caller can decide a failure is close enough.
func TestOnlyCurrentCountsAsObtained(t *testing.T) {
	if !domain.HydrationCurrent.Obtained() {
		t.Error("a current reading does not count as obtained")
	}
	for _, s := range []domain.HydrationStatus{
		domain.HydrationUnauthorized, domain.HydrationUnavailable, domain.HydrationFailed,
	} {
		if s.Obtained() {
			t.Errorf("%q counts as obtained", s)
		}
	}
}
