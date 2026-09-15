package domain

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// ══════════════════════════════════════════════════════════════════════
//
//	NO VALIDATION FAILURE MAY QUOTE STORED CONTENT
//
// ══════════════════════════════════════════════════════════════════════
//
// ── Why this is a test and not a review habit ──────────────────────────
// Because every failure this package produces is copied twice on its way
// out. It reaches the model as the body of a `tool` message, and it is
// written to `chat.tool_calls.error_message`, which is NOT covered by the
// redaction that `ToolDefinition.Confidential` performs: that flag drops
// `arguments` and `result` and leaves the error text in the clear.
//
// So `Invalid("content %q is too long", m.Content)` would persist a
// memory's full text in the audit trail of a capability specifically
// marked as one whose payload must not be kept. The line looks harmless
// at review time and is the whole leak.
//
// Every case below puts the canary in every free-text field and then
// triggers a refusal. The canary must never come back.

// longCanary is content that is both over a limit and recognisable, so
// the "too long" messages are exercised with something worth leaking.
func longCanary(n int) string {
	return canary + strings.Repeat("x", n)
}

func TestNoRoomRefusalQuotesTheRoom(t *testing.T) {
	base := func() *Room {
		return &Room{
			WorkspaceID: uuid.New(),
			Name:        canary,
			Description: canary,
			Status:      LifecycleActive,
			Sensitivity: SensitivityHighlySensitive,
		}
	}
	cases := map[string]func(*Room){
		"no workspace":        func(r *Room) { r.WorkspaceID = uuid.Nil },
		"empty name":          func(r *Room) { r.Name = "   " },
		"name too long":       func(r *Room) { r.Name = longCanary(MaxRoomName) },
		"description to long": func(r *Room) { r.Description = longCanary(MaxRoomDescription) },
		"unknown status":      func(r *Room) { r.Status = "gone" },
		"unknown level":       func(r *Room) { r.Sensitivity = "secret" },
	}
	for name, mutate := range cases {
		r := base()
		mutate(r)
		assertNoLeak(t, name, r.Validate())
	}
}

func TestNoArtifactRefusalQuotesTheArtifact(t *testing.T) {
	base := func() *Artifact {
		return &Artifact{
			WorkspaceID: uuid.New(),
			Kind:        ArtifactNote,
			Title:       canary,
			Body:        canary,
			Status:      LifecycleActive,
			Sensitivity: SensitivityHighlySensitive,
		}
	}
	zero := uuid.Nil
	cases := map[string]func(*Artifact){
		"no workspace":   func(a *Artifact) { a.WorkspaceID = uuid.Nil },
		"unknown kind":   func(a *Artifact) { a.Kind = "checklist" },
		"empty title":    func(a *Artifact) { a.Title = " " },
		"title too long": func(a *Artifact) { a.Title = longCanary(MaxArtifactTitle) },
		"body too long":  func(a *Artifact) { a.Body = longCanary(MaxArtifactBody) },
		"zero room":      func(a *Artifact) { a.RoomID = &zero },
		"unknown status": func(a *Artifact) { a.Status = "gone" },
		"unknown level":  func(a *Artifact) { a.Sensitivity = "secret" },
	}
	for name, mutate := range cases {
		a := base()
		mutate(a)
		assertNoLeak(t, name, a.Validate())
	}
}

func TestNoItemRefusalQuotesTheItem(t *testing.T) {
	base := func() *ArtifactItem {
		return &ArtifactItem{
			WorkspaceID: uuid.New(),
			ArtifactID:  uuid.New(),
			Text:        canary,
		}
	}
	cases := map[string]func(*ArtifactItem){
		"no workspace":      func(i *ArtifactItem) { i.WorkspaceID = uuid.Nil },
		"no artifact":       func(i *ArtifactItem) { i.ArtifactID = uuid.Nil },
		"empty text":        func(i *ArtifactItem) { i.Text = "  " },
		"text too long":     func(i *ArtifactItem) { i.Text = longCanary(MaxItemText) },
		"negative position": func(i *ArtifactItem) { i.Position = -3 },
	}
	for name, mutate := range cases {
		i := base()
		mutate(i)
		assertNoLeak(t, name, i.Validate())
	}
}

func TestNoMemoryRefusalQuotesTheMemory(t *testing.T) {
	base := func() *Memory {
		return &Memory{
			WorkspaceID: uuid.New(),
			Kind:        MemoryReflection,
			Content:     canary,
			Summary:     canary,
			Importance:  DefaultImportance,
			Confidence:  DefaultConfidence,
			Status:      LifecycleActive,
			Sensitivity: SensitivityHighlySensitive,
		}
	}
	zero := uuid.Nil
	cases := map[string]func(*Memory){
		"no workspace":       func(m *Memory) { m.WorkspaceID = uuid.Nil },
		"unknown kind":       func(m *Memory) { m.Kind = "grudge" },
		"empty content":      func(m *Memory) { m.Content = "   " },
		"content too long":   func(m *Memory) { m.Content = longCanary(MaxMemoryContent) },
		"summary too long":   func(m *Memory) { m.Summary = longCanary(MaxMemorySummary) },
		"importance too low": func(m *Memory) { m.Importance = 0 },
		"importance high":    func(m *Memory) { m.Importance = 99 },
		"bad confidence":     func(m *Memory) { m.Confidence = "0.72" },
		"zero room":          func(m *Memory) { m.RoomID = &zero },
		"zero artifact":      func(m *Memory) { m.ArtifactID = &zero },
		"unknown status":     func(m *Memory) { m.Status = "gone" },
		"unknown level":      func(m *Memory) { m.Sensitivity = "secret" },
	}
	for name, mutate := range cases {
		m := base()
		mutate(m)
		assertNoLeak(t, name, m.Validate())
	}
}

func TestNoSourceRefusalQuotesTheEvidence(t *testing.T) {
	// The sharpest case in this file: a source is a raw transcript. If any
	// refusal quotes one, the audit trail ends up holding the recording.
	base := func() *Source {
		return &Source{
			WorkspaceID: uuid.New(),
			Kind:        SourceVoiceTranscript,
			Content:     canary,
			ExternalRef: canary,
			CapturedAt:  time.Date(2026, 9, 13, 10, 0, 0, 0, time.UTC),
			Sensitivity: SensitivityHighlySensitive,
		}
	}
	cases := map[string]func(*Source){
		"no workspace":     func(s *Source) { s.WorkspaceID = uuid.Nil },
		"unknown kind":     func(s *Source) { s.Kind = "audio" },
		"empty content":    func(s *Source) { s.Content = " " },
		"content too long": func(s *Source) { s.Content = longCanary(MaxSourceContent) },
		"ref too long":     func(s *Source) { s.ExternalRef = longCanary(MaxSourceExternalRef) },
		"external needs a ref": func(s *Source) {
			s.Kind = SourceExternal
			s.ExternalRef = ""
		},
		"no captured_at": func(s *Source) { s.CapturedAt = time.Time{} },
		"unknown level":  func(s *Source) { s.Sensitivity = "secret" },
	}
	for name, mutate := range cases {
		s := base()
		mutate(s)
		assertNoLeak(t, name, s.Validate())
	}
}

func TestNoSessionRefusalQuotesTheSummary(t *testing.T) {
	now := time.Date(2026, 9, 13, 10, 0, 0, 0, time.UTC)
	base := func() *Session {
		return &Session{
			WorkspaceID:    uuid.New(),
			Status:         SessionOpen,
			Summary:        canary,
			StartedAt:      now,
			LastActivityAt: now,
		}
	}
	zero := uuid.Nil
	cases := map[string]func(*Session){
		"no workspace":     func(s *Session) { s.WorkspaceID = uuid.Nil },
		"unknown status":   func(s *Session) { s.Status = "paused" },
		"summary too long": func(s *Session) { s.Summary = longCanary(MaxSessionSummary) },
		"no started_at":    func(s *Session) { s.StartedAt = time.Time{} },
		"no activity":      func(s *Session) { s.LastActivityAt = time.Time{} },
		"open but closed":  func(s *Session) { s.ClosedAt = &now },
		"closed but open": func(s *Session) {
			s.Status = SessionClosed
			s.ClosedAt = nil
		},
		"zero room":     func(s *Session) { s.ActiveRoomID = &zero },
		"zero artifact": func(s *Session) { s.ActiveArtifactID = &zero },
	}
	for name, mutate := range cases {
		s := base()
		mutate(s)
		assertNoLeak(t, name, s.Validate())
	}
}

func TestNoRelationRefusalQuotesAnything(t *testing.T) {
	// A relation holds no content, so this one is cheap. It is here so the
	// day somebody adds a note field to a relation, the guard already
	// exists.
	id := uuid.New()
	cases := map[string]*Relation{
		"no workspace": {FromType: EntityMemory, FromID: id, Kind: RelationRelatedTo, ToType: EntityMemory, ToID: uuid.New()},
		"bad kind":     {WorkspaceID: uuid.New(), FromType: EntityMemory, FromID: id, Kind: RelationKind(canary), ToType: EntityMemory, ToID: uuid.New()},
		"bad from":     {WorkspaceID: uuid.New(), FromType: EntityType(canary), FromID: id, Kind: RelationRelatedTo, ToType: EntityMemory, ToID: uuid.New()},
		"bad shape":    {WorkspaceID: uuid.New(), FromType: EntityRoom, FromID: id, Kind: RelationSupersedes, ToType: EntityMemory, ToID: uuid.New()},
		"reflexive":    {WorkspaceID: uuid.New(), FromType: EntityMemory, FromID: id, Kind: RelationSupersedes, ToType: EntityMemory, ToID: id},
	}
	for name, r := range cases {
		err := r.Validate()
		if err == nil {
			t.Errorf("%s: want a refusal", name)
			continue
		}
		// The two "bad" cases DO echo the rejected vocabulary token, which
		// is the documented exception: a model told only "invalid" sends
		// the same word again. What matters is that the echo is bounded,
		// which TestQuoteTokenBoundsWhatAnErrorEchoes covers.
		if name == "bad kind" || name == "bad from" {
			continue
		}
		if strings.Contains(err.Error(), canary) {
			t.Errorf("%s: refusal leaked: %v", name, err)
		}
	}
}

func TestADecisionOriginRefusalNamesKindsAndNotContent(t *testing.T) {
	r := relation(EntityMemory, RelationDecisionFor, EntityArtifact)
	m := &Memory{
		WorkspaceID: uuid.New(),
		Kind:        MemoryReflection,
		Content:     canary,
		Summary:     canary,
	}
	assertNoLeak(t, "decision origin", r.ValidateDecisionOrigin(m))
	assertNoLeak(t, "unresolved origin", r.ValidateDecisionOrigin(nil))
}

/* ── the assertion ───────────────────────────────────────────────────── */

// assertNoLeak requires a refusal that carries no content.
//
// It insists on a refusal as well, because a case that silently started
// VALIDATING would otherwise pass this test forever while testing
// nothing.
func assertNoLeak(t *testing.T, name string, err error) {
	t.Helper()
	if err == nil {
		t.Errorf("%s: want a refusal, got nil", name)
		return
	}
	if strings.Contains(err.Error(), canary) {
		t.Errorf("%s: the refusal quoted stored content, which reaches "+
			"chat.tool_calls.error_message in the clear: %v", name, err)
	}
}

func TestNoProvenanceRefusalQuotesEitherSide(t *testing.T) {
	// The sharpest pairing in the context: a memory's conclusion and the
	// transcript behind it, both handed to a rule that refuses them.
	m := &Memory{
		WorkspaceID: uuid.New(),
		Kind:        MemoryReflection,
		Content:     canary,
		Summary:     canary,
		Importance:  DefaultImportance,
		Confidence:  DefaultConfidence,
		Status:      LifecycleActive,
		Sensitivity: SensitivityNormal,
	}
	s := &Source{
		WorkspaceID: uuid.New(),
		Kind:        SourceVoiceTranscript,
		Content:     canary,
		ExternalRef: canary,
		CapturedAt:  time.Date(2026, 9, 13, 10, 0, 0, 0, time.UTC),
		Sensitivity: SensitivityHighlySensitive,
	}

	assertNoLeak(t, "floor refusal", m.MayRestOn(s))
	assertNoLeak(t, "floor refusal, direct",
		ValidateSensitivityFloor(SensitivityNormal, SensitivityHighlySensitive))
	assertNoLeak(t, "unresolved evidence", m.MayRestOn(nil))
	assertNoLeak(t, "unknown evidence level",
		ValidateSensitivityFloor(SensitivityNormal, Sensitivity(canary)))

	ms := &MemorySource{}
	assertNoLeak(t, "link with no workspace", ms.Validate())
}
