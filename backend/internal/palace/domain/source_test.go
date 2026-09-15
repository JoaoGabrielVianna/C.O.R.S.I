package domain

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func validSource() *Source {
	return &Source{
		ID:          uuid.New(),
		WorkspaceID: uuid.New(),
		Kind:        SourceText,
		Content:     "Anotação colada da conversa de terça",
		CapturedAt:  time.Date(2026, 9, 10, 14, 0, 0, 0, time.UTC),
		Sensitivity: DefaultSensitivity,
	}
}

func TestAWellFormedSourceValidates(t *testing.T) {
	if err := validSource().Validate(); err != nil {
		t.Fatalf("a valid source was refused: %v", err)
	}
}

func TestEveryDeclaredSourceKindIsAccepted(t *testing.T) {
	for _, k := range SourceKinds {
		s := validSource()
		s.Kind = k
		if k.RequiresExternalRef() {
			s.ExternalRef = "https://example.com/post/1"
		}
		if err := s.Validate(); err != nil {
			t.Errorf("kind %q was refused: %v", k, err)
		}
	}
}

func TestEvidenceThatCarriesNothingIsRefused(t *testing.T) {
	// If all the caller has is a link, the content is what was seen at
	// that link. Requiring it forces them to state it rather than store a
	// pointer to something that may not answer later.
	s := validSource()
	s.Content = "  "
	assertInvalid(t, s.Validate(), "content")
}

func TestAnExternalSourceMustSayWhichSystemItCameFrom(t *testing.T) {
	// Evidence about somebody else's system that cannot say WHICH system
	// is not provenance.
	s := validSource()
	s.Kind = SourceExternal
	s.ExternalRef = ""
	assertInvalid(t, s.Validate(), "external_ref")

	s.ExternalRef = "https://www.threads.net/@corsi/post/abc"
	if err := s.Validate(); err != nil {
		t.Fatalf("an external source naming its origin was refused: %v", err)
	}
}

func TestOnlyExternalRequiresAReference(t *testing.T) {
	for _, k := range SourceKinds {
		want := k == SourceExternal
		if got := k.RequiresExternalRef(); got != want {
			t.Errorf("%q.RequiresExternalRef() = %v, want %v", k, got, want)
		}
	}
}

func TestAnInternalSourceMayStillNameAReference(t *testing.T) {
	// A system event that names the thing it observed is better
	// provenance than one that does not, and nothing should discourage it.
	s := validSource()
	s.Kind = SourceSystemEvent
	s.ExternalRef = "finance.import.commit"
	if err := s.Validate(); err != nil {
		t.Fatalf("an internal source with a reference was refused: %v", err)
	}
}

func TestSourceContentAndReferenceAreBounded(t *testing.T) {
	s := validSource()
	s.Content = strings.Repeat("ç", MaxSourceContent+1)
	assertInvalid(t, s.Validate(), "content")

	s = validSource()
	s.ExternalRef = strings.Repeat("u", MaxSourceExternalRef+1)
	assertInvalid(t, s.Validate(), "external_ref")
}

func TestASourceMustSayWhenTheEvidenceWasProduced(t *testing.T) {
	// A zero instant would sort as the year one and make "what did I
	// capture this week" quietly wrong.
	s := validSource()
	s.CapturedAt = time.Time{}
	assertInvalid(t, s.Validate(), "captured_at")
}

func TestCapturedAtIsNotTheSameFactAsCreatedAt(t *testing.T) {
	// A transcript of yesterday's voice note is captured today. The two
	// being separate fields is what stops everything imported landing in
	// the present.
	s := validSource()
	s.Kind = SourceVoiceTranscript
	s.CapturedAt = time.Date(2025, 1, 4, 8, 0, 0, 0, time.UTC)
	s.CreatedAt = time.Date(2026, 9, 13, 8, 0, 0, 0, time.UTC)
	if err := s.Validate(); err != nil {
		t.Fatalf("evidence older than its row was refused: %v", err)
	}
}

func TestASourceWithoutAWorkspaceIsRefused(t *testing.T) {
	s := validSource()
	s.WorkspaceID = uuid.Nil
	assertInvalid(t, s.Validate(), "workspace")
}

func TestASourceRefusesAnUnknownKind(t *testing.T) {
	s := validSource()
	s.Kind = "audio"
	assertInvalid(t, s.Validate(), "kind")

	_, err := ParseSourceKind("audio")
	if err == nil {
		t.Fatal("want a refusal")
	}
	for _, name := range SourceKindNames() {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("refusal does not name %q: %v", name, err)
		}
	}
}
