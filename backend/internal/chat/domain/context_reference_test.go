package domain

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

// The type convention is what the resolver registry keys on and what the
// frontend groups by, so a change to it is a change to two other systems.
func TestContextReferenceTypeAcceptsProviderDotEntity(t *testing.T) {
	for _, s := range []string{
		"job_radar.opportunity",
		"github.repository",
		"calendar.event",
		"a.b",
		"finance.transaction",
	} {
		if !ContextReferenceType(s).Valid() {
			t.Errorf("%q was rejected", s)
		}
	}
}

func TestContextReferenceTypeRefusesEverythingElse(t *testing.T) {
	for _, s := range []string{
		"",
		"jobradar",                  // no entity: names an owner, not a thing
		"job_radar.opportunity.get", // that is a TOOL name, not a type
		"Job_Radar.opportunity",     // uppercase
		"job-radar.opportunity",     // hyphen
		"job_radar.",
		".opportunity",
		"1radar.opportunity", // must start with a letter
		strings.Repeat("a", 65) + ".b",
	} {
		if ContextReferenceType(s).Valid() {
			t.Errorf("%q was accepted", s)
		}
	}
}

// A tool name must never validate as a reference type. The two vocabularies
// are adjacent by design and the third segment is what separates "a thing"
// from "a thing you can do to it".
func TestToolNamesAreNotReferenceTypes(t *testing.T) {
	for _, s := range []string{
		"job_radar.opportunity.get",
		"job_radar.opportunity.move",
		"github.repository.list",
	} {
		if ContextReferenceType(s).Valid() {
			t.Errorf("tool name %q validated as a reference type", s)
		}
		if !ValidToolName(s) {
			t.Errorf("%q should still be a valid tool name", s)
		}
	}
}

func TestProviderIsTheFirstSegment(t *testing.T) {
	if got := ContextReferenceType("job_radar.opportunity").Provider(); got != "job_radar" {
		t.Fatalf("Provider() = %q", got)
	}
	if got := ContextReferenceType("nonsense").Provider(); got != "" {
		t.Fatalf("Provider() on a malformed type = %q, want empty", got)
	}
}

func TestValidateRefusesAMissingID(t *testing.T) {
	r := ContextReference{Type: "job_radar.opportunity", ID: "  "}
	if err := r.Validate(); err == nil {
		t.Fatal("a blank id was accepted")
	}
}

func TestValidateBoundsTheStrings(t *testing.T) {
	base := ContextReference{Type: "job_radar.opportunity", ID: "x"}

	tooLongID := base
	tooLongID.ID = strings.Repeat("a", 201)
	if err := tooLongID.Validate(); err == nil {
		t.Error("an oversized id was accepted")
	}

	tooLongLabel := base
	tooLongLabel.Label = strings.Repeat("a", 141)
	if err := tooLongLabel.Validate(); err == nil {
		t.Error("an oversized label was accepted")
	}

	tooLongSubtitle := base
	tooLongSubtitle.Subtitle = strings.Repeat("a", 141)
	if err := tooLongSubtitle.Validate(); err == nil {
		t.Error("an oversized subtitle was accepted")
	}
}

func TestValidateBoundsTheCount(t *testing.T) {
	refs := make([]ContextReference, MaxContextReferences+1)
	for i := range refs {
		refs[i] = ContextReference{Type: "job_radar.opportunity", ID: string(rune('a' + i))}
	}
	if err := ValidateContextReferences(refs); err == nil {
		t.Fatal("a list above the ceiling was accepted")
	}
}

// An empty list and an absent one are the same input.
func TestValidateAcceptsNothingAttached(t *testing.T) {
	if err := ValidateContextReferences(nil); err != nil {
		t.Errorf("nil was rejected: %v", err)
	}
	if err := ValidateContextReferences([]ContextReference{}); err != nil {
		t.Errorf("an empty list was rejected: %v", err)
	}
}

func TestKeyIsTypeAndIDAndNotTheLabel(t *testing.T) {
	a := ContextReference{Type: "job_radar.opportunity", ID: "1", Label: "Acme · Backend"}
	b := ContextReference{Type: "job_radar.opportunity", ID: "1", Label: "renamed since"}
	if a.Key() != b.Key() {
		t.Fatal("the label changed the identity; it must not")
	}
	c := ContextReference{Type: "github.repository", ID: "1"}
	if a.Key() == c.Key() {
		t.Fatal("two types share one key")
	}
}

func TestDedupeKeepsTheFirstAndPreservesOrder(t *testing.T) {
	got := DedupeContextReferences([]ContextReference{
		{Type: "job_radar.opportunity", ID: "1", Label: "first"},
		{Type: "job_radar.opportunity", ID: "2"},
		{Type: "job_radar.opportunity", ID: "1", Label: "second"},
	})
	if len(got) != 2 {
		t.Fatalf("got %d, want 2", len(got))
	}
	if got[0].ID != "1" || got[0].Label != "first" {
		t.Errorf("the first occurrence was not the one kept: %+v", got[0])
	}
	if got[1].ID != "2" {
		t.Errorf("order was not preserved: %+v", got)
	}
}

/* ── the separation from TurnReference ───────────────────────────────── */

// The two concepts share a word and must never share a type. This test is
// the tripwire: it fails if somebody makes an entity type valid as a
// reference KIND, which is the change that would let an entity id start
// scoping capabilities.
func TestAnEntityTypeIsNotAReferenceKind(t *testing.T) {
	if ReferenceKind("job_radar.opportunity").Valid() {
		t.Fatal("an entity type validated as a TurnReference kind; " +
			"TurnReference scopes TOOLS and must not accept entities")
	}
	if !ReferenceKindTool.Valid() {
		t.Fatal("the tool kind stopped being valid")
	}
}

// A message may not carry subjects on the assistant side: attaching is the
// user's act, and a model appearing to have chosen its own subject is a
// claim the record must not be able to make.
func TestOnlyAUserTurnMayCarryContextReferences(t *testing.T) {
	m := &Message{
		WorkspaceID:       uuid.New(),
		ConversationID:    uuid.New(),
		Role:              RoleAssistant,
		ContextReferences: []ContextReference{{Type: "job_radar.opportunity", ID: "x"}},
	}
	if err := m.Validate(); err == nil {
		t.Fatal("an assistant turn carried context references")
	}
}
