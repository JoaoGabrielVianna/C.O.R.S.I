package domain

import (
	"strings"
	"testing"
)

/* ── lifecycle ───────────────────────────────────────────────────────── */

func TestLifecycleAcceptsOnlyItsTwoStates(t *testing.T) {
	for _, l := range Lifecycles {
		if !l.Valid() {
			t.Errorf("declared lifecycle %q reports invalid", l)
		}
	}
	for _, bad := range []Lifecycle{"", "deleted", "Active", "archive", "open"} {
		if bad.Valid() {
			t.Errorf("lifecycle %q reports valid", bad)
		}
	}
}

func TestParseLifecycleForgivesCaseAndSpaceAndNothingElse(t *testing.T) {
	for _, raw := range []string{"active", "ACTIVE", " Active ", "\tarchived\n"} {
		if _, err := ParseLifecycle(raw); err != nil {
			t.Errorf("ParseLifecycle(%q) = %v, want it accepted", raw, err)
		}
	}
	// "archive" is one letter from "archived", and guessing would retire
	// something nobody asked to retire.
	for _, raw := range []string{"", "archive", "act", "arquivado", "ative"} {
		if _, err := ParseLifecycle(raw); err == nil {
			t.Errorf("ParseLifecycle(%q) was accepted, want refused", raw)
		}
	}
}

func TestParseLifecycleNamesEveryStateInItsRefusal(t *testing.T) {
	_, err := ParseLifecycle("nonsense")
	if err == nil {
		t.Fatal("want a refusal")
	}
	// A model told only "invalid" learns nothing and sends the same word
	// again.
	for _, name := range LifecycleNames() {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("refusal does not name %q: %v", name, err)
		}
	}
}

func TestLifecycleNamesFollowTheEnum(t *testing.T) {
	got := LifecycleNames()
	if len(got) != len(Lifecycles) {
		t.Fatalf("LifecycleNames() has %d entries, the enum has %d", len(got), len(Lifecycles))
	}
	for i, l := range Lifecycles {
		if got[i] != string(l) {
			t.Errorf("LifecycleNames()[%d] = %q, want %q", i, got[i], l)
		}
	}
}

func TestArchivedIsTheOnlyRetiredState(t *testing.T) {
	if LifecycleActive.Archived() {
		t.Error("active reports archived")
	}
	if !LifecycleArchived.Archived() {
		t.Error("archived does not report archived")
	}
}

/* ── entity types ────────────────────────────────────────────────────── */

func TestEntityTypeAcceptsOnlyTheThreeRelatableThings(t *testing.T) {
	for _, e := range EntityTypes {
		if !e.Valid() {
			t.Errorf("declared entity type %q reports invalid", e)
		}
	}
	// `source` is deliberately absent: provenance is memory_sources, not a
	// relation. `artifact_item` is absent because an item has no identity
	// outside its artifact.
	for _, bad := range []EntityType{"", "source", "artifact_item", "session", "Room"} {
		if bad.Valid() {
			t.Errorf("entity type %q reports valid", bad)
		}
	}
}

func TestParseEntityTypeRefusesSource(t *testing.T) {
	if _, err := ParseEntityType("source"); err == nil {
		t.Fatal("`source` was accepted as a relation endpoint; provenance is memory_sources")
	}
}

/* ── the bound on what an error may quote back ───────────────────────── */

func TestQuoteTokenBoundsWhatAnErrorEchoes(t *testing.T) {
	long := strings.Repeat("a", maxEchoedToken*3)
	got := quoteToken(long)
	if len([]rune(got)) > maxEchoedToken+4 {
		// +4 covers the two quotes and the ellipsis.
		t.Errorf("quoteToken echoed %d runes, want at most %d", len([]rune(got)), maxEchoedToken+4)
	}
	if !strings.Contains(got, "…") {
		t.Errorf("a truncated echo does not say it was truncated: %s", got)
	}
}

func TestQuoteTokenKeepsAShortTokenWhole(t *testing.T) {
	if got := quoteToken(" arquivado "); got != `"arquivado"` {
		t.Errorf("quoteToken = %s, want %q", got, "arquivado")
	}
}

func TestAParserRefusalIsBoundedEvenWhenHandedAParagraph(t *testing.T) {
	// A model that misunderstands a schema will eventually put a paragraph
	// where a word belongs. The refusal must not carry the paragraph into
	// chat.tool_calls.error_message, which Confidential does not redact.
	paragraph := strings.Repeat("segredo ", 200)
	for name, parse := range map[string]func(string) (string, error){
		"lifecycle":   func(s string) (string, error) { v, err := ParseLifecycle(s); return string(v), err },
		"sensitivity": func(s string) (string, error) { v, err := ParseSensitivity(s); return string(v), err },
		"memory kind": func(s string) (string, error) { v, err := ParseMemoryKind(s); return string(v), err },
	} {
		_, err := parse(paragraph)
		if err == nil {
			t.Fatalf("%s: want a refusal", name)
		}
		if len([]rune(err.Error())) > 400 {
			t.Errorf("%s: refusal is %d runes long, which means it carried the input",
				name, len([]rune(err.Error())))
		}
	}
}

/* ── the error vocabulary ────────────────────────────────────────────── */

func TestTheTwoErrorKindsCarryTheirKind(t *testing.T) {
	if got := Invalid("x").Kind; got != KindInvalid {
		t.Errorf("Invalid() kind = %q", got)
	}
	if got := NotFound("x").Kind; got != KindNotFound {
		t.Errorf("NotFound() kind = %q", got)
	}
	if msg := Invalid("importance must be between %d and %d", 1, 5).Error(); !strings.Contains(msg, "1 and 5") {
		t.Errorf("Invalid() did not format: %s", msg)
	}
}
