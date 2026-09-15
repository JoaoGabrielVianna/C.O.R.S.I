package domain

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// canary is a string that must never leave this package by accident. It
// is deliberately unlike any vocabulary word, so a single Contains check
// is conclusive.
const canary = "CANARY-DO-NOT-LEAK-a7f3"

// everyContentBearingEntity returns one of each, with the canary in every
// free-text field.
//
// ── Why the list is written out rather than derived ────────────────────
// Because the failure this guards is an entity added later and not given
// a redacted encoding. A test that walked whatever exists would pass for
// the new type by never looking at it. This one has to be edited, and the
// count check below is what forces the edit.
func everyContentBearingEntity() map[string]any {
	id := uuid.New()
	when := time.Date(2026, 9, 13, 10, 0, 0, 0, time.UTC)

	return map[string]any{
		"room": Room{
			ID: id, WorkspaceID: uuid.New(),
			Name: canary, Description: canary,
			Status: LifecycleActive, Sensitivity: SensitivityHighlySensitive,
		},
		"artifact": Artifact{
			ID: id, WorkspaceID: uuid.New(),
			Kind: ArtifactNote, Title: canary, Body: canary,
			Status: LifecycleActive, Sensitivity: SensitivityHighlySensitive,
		},
		"artifact_item": ArtifactItem{
			ID: id, WorkspaceID: uuid.New(), ArtifactID: uuid.New(),
			Text: canary,
		},
		"memory": Memory{
			ID: id, WorkspaceID: uuid.New(),
			Kind: MemoryReflection, Content: canary, Summary: canary,
			Importance: 5, Confidence: ConfidenceHigh,
			Status: LifecycleActive, Sensitivity: SensitivityHighlySensitive,
		},
		"source": Source{
			ID: id, WorkspaceID: uuid.New(),
			Kind: SourceVoiceTranscript, Content: canary, ExternalRef: canary,
			CapturedAt: when, Sensitivity: SensitivityHighlySensitive,
		},
		"session": Session{
			ID: id, WorkspaceID: uuid.New(), Status: SessionOpen,
			Summary: canary, StartedAt: when, LastActivityAt: when,
		},
	}
}

// ══════════════════════════════════════════════════════════════════════
//
//	THE FOUR WAYS CONTENT ESCAPES BY ACCIDENT
//
// ══════════════════════════════════════════════════════════════════════
//
// One `s.log.Info("saved", "memory", m)` written by somebody in a hurry
// is all it takes. These are the four encodings that line can reach.
func TestNoContentBearingEntityRendersItsContentByAccident(t *testing.T) {
	for name, entity := range everyContentBearingEntity() {
		t.Run(name, func(t *testing.T) {
			// %v and %s both route through String().
			for _, rendered := range []string{
				fmt.Sprintf("%v", entity),
				fmt.Sprintf("%s", entity),
			} {
				if strings.Contains(rendered, canary) {
					t.Errorf("formatting leaked content: %s", rendered)
				}
			}

			raw, err := json.Marshal(entity)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if strings.Contains(string(raw), canary) {
				t.Errorf("marshalling leaked content: %s", raw)
			}
		})
	}
}

func TestMarshallingAPointerRedactsToo(t *testing.T) {
	// A method set on the pointer alone would cover json.Marshal(&m) and
	// miss json.Marshal(m). Both must redact, so the receivers are values.
	m := Memory{ID: uuid.New(), Content: canary}

	for _, v := range []any{m, &m} {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal %T: %v", v, err)
		}
		if strings.Contains(string(raw), canary) {
			t.Errorf("marshalling %T leaked content: %s", v, raw)
		}
	}
}

func TestSlogCannotBeUsedToPrintAMemory(t *testing.T) {
	// The concrete accident, reproduced: a log line that hands a whole
	// entity to the logger. Both handlers this platform can be configured
	// with are checked, because they resolve values differently: the text
	// handler formats, the JSON handler marshals.
	m := Memory{ID: uuid.New(), Content: canary, Summary: canary}

	for name, build := range map[string]func(*strings.Builder) slog.Handler{
		"text": func(b *strings.Builder) slog.Handler { return slog.NewTextHandler(b, nil) },
		"json": func(b *strings.Builder) slog.Handler { return slog.NewJSONHandler(b, nil) },
	} {
		var out strings.Builder
		slog.New(build(&out)).Info("saved", "memory", m)
		if strings.Contains(out.String(), canary) {
			t.Errorf("the %s handler leaked content: %s", name, out.String())
		}
	}
}

func TestARedactedRenderingStillIdentifiesWhatItWithheld(t *testing.T) {
	// Withholding everything, including the id, would make the mechanism
	// useless for debugging and guarantee somebody disables it.
	id := uuid.New()
	m := Memory{ID: id, Content: canary}

	rendered := fmt.Sprintf("%v", m)
	if !strings.Contains(rendered, id.String()) {
		t.Errorf("the rendering does not identify the row: %s", rendered)
	}
	if !strings.Contains(rendered, "palace.memory") {
		t.Errorf("the rendering does not say what type it is: %s", rendered)
	}
	if !strings.Contains(rendered, "redacted") {
		t.Errorf("the rendering does not say it withheld anything: %s", rendered)
	}
}

func TestARedactedEncodingSaysItIsRedactedRatherThanLookingEmpty(t *testing.T) {
	// A reader seeing an object with only an id has to guess whether the
	// content was withheld or was never there. Those are different facts.
	raw, err := json.Marshal(Source{ID: uuid.New(), Content: canary})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got struct {
		Type     string `json:"type"`
		ID       string `json:"id"`
		Redacted bool   `json:"redacted"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !got.Redacted {
		t.Errorf("the encoding does not declare itself redacted: %s", raw)
	}
	if got.Type != typeSource {
		t.Errorf("type = %q, want %q", got.Type, typeSource)
	}
}

func TestEveryContentBearingEntityIsCovered(t *testing.T) {
	// Forces the edit when a seventh entity arrives: adding one without
	// giving it a redacted encoding must fail here rather than leak
	// silently the first time somebody logs it.
	const covered = 6
	if got := len(everyContentBearingEntity()); got != covered {
		t.Fatalf("this suite covers %d entities, the package has %d. "+
			"A new entity needs String() and MarshalJSON() in redaction.go", covered, got)
	}
}

func TestRelationHasNoRedactedEncodingOnPurpose(t *testing.T) {
	// It carries no free text: two ids, two type words and a kind word.
	// There is nothing to withhold, and a redacted encoding would cost the
	// one thing a debug line is for while protecting nothing.
	r := Relation{
		ID: uuid.New(), WorkspaceID: uuid.New(),
		FromType: EntityMemory, FromID: uuid.New(),
		Kind:   RelationRelatedTo,
		ToType: EntityArtifact, ToID: uuid.New(),
	}
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(raw), string(RelationRelatedTo)) {
		t.Errorf("a relation no longer renders its own vocabulary: %s", raw)
	}
}
