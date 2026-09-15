//go:build integration

// Palace, slice S3: evidence, provenance, and the sensitivity floor.
//
// The harness lives in palace_integration_test.go; this file is the same
// package and uses it.
package palace

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/palace/app"
	"github.com/corsi/backend/internal/palace/domain"
	"github.com/corsi/backend/internal/palace/ports"
)

/* ── seed helpers ────────────────────────────────────────────────────── */

func (e *env) source(ws uuid.UUID, content string, level domain.Sensitivity) *domain.Source {
	e.t.Helper()
	s, err := e.svc.CreateSource(e.ctx(), ws, app.CreateSourceInput{
		Kind:        domain.SourceText,
		Content:     content,
		Sensitivity: &level,
	})
	if err != nil {
		e.t.Fatalf("seed source: %v", err)
	}
	return s
}

func (e *env) memoryAt(ws uuid.UUID, content string, level domain.Sensitivity) *domain.Memory {
	e.t.Helper()
	return e.memory(ws, content, level, nil)
}

/* ══════════════════════════════════════════════════════════════════════
   Source · evidence, and its immutability
   ══════════════════════════════════════════════════════════════════════ */

func TestASourceIsWrittenAndReadBack(t *testing.T) {
	e := newEnv(t)
	captured := time.Date(2026, 9, 1, 14, 30, 0, 0, time.UTC)

	s, err := e.svc.CreateSource(e.ctx(), e.mine, app.CreateSourceInput{
		Kind:       domain.SourceVoiceTranscript,
		Content:    "transcrição da conversa de terça",
		CapturedAt: &captured,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := e.svc.GetSource(e.ctx(), e.mine, s.ID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got.Kind != domain.SourceVoiceTranscript {
		t.Errorf("kind = %q", got.Kind)
	}
	if !got.CapturedAt.Equal(captured) {
		t.Errorf("captured_at = %v, want %v", got.CapturedAt, captured)
	}
	if got.Sensitivity != domain.DefaultSensitivity {
		t.Errorf("sensitivity = %q, want the default", got.Sensitivity)
	}
}

func TestCapturedAtDefaultsToTheDatabaseClockAndNotThisProcess(t *testing.T) {
	// A transcript of yesterday's voice note is captured today, so the
	// field is the caller's to set. When they do not, the value has to
	// come from the one authority every other timestamp here comes from.
	e := newEnv(t)

	before, err := e.svc.Now(e.ctx())
	if err != nil {
		t.Fatalf("clock: %v", err)
	}
	s := e.source(e.mine, "algo colado agora", domain.SensitivityNormal)
	after, err := e.svc.Now(e.ctx())
	if err != nil {
		t.Fatalf("clock: %v", err)
	}

	if s.CapturedAt.Before(before) || s.CapturedAt.After(after) {
		t.Errorf("captured_at %v sits outside the window [%v, %v] read from the database",
			s.CapturedAt, before, after)
	}
}

func TestANeighboursSourceIsIndistinguishableFromOneThatNeverExisted(t *testing.T) {
	e := newEnv(t)
	theirs := e.source(e.theirs, "a evidência deles", domain.SensitivityNormal)
	fabricated := uuid.New()

	_, errReal := e.svc.GetSource(e.ctx(), e.mine, theirs.ID)
	_, errFake := e.svc.GetSource(e.ctx(), e.mine, fabricated)

	assertNotFound(t, "neighbour's source", errReal)
	assertNotFound(t, "fabricated id", errFake)

	realMsg := strings.Replace(errReal.Error(), theirs.ID.String(), "<id>", 1)
	fakeMsg := strings.Replace(errFake.Error(), fabricated.String(), "<id>", 1)
	if realMsg != fakeMsg {
		t.Errorf("the two answers differ:\n  real: %s\n  fake: %s", realMsg, fakeMsg)
	}
}

func TestAnExternalSourceMustNameWhereItCameFrom(t *testing.T) {
	e := newEnv(t)
	_, err := e.svc.CreateSource(e.ctx(), e.mine, app.CreateSourceInput{
		Kind: domain.SourceExternal, Content: "o que eu vi lá",
	})
	assertInvalid(t, "external source with no reference", err)

	if _, err := e.svc.CreateSource(e.ctx(), e.mine, app.CreateSourceInput{
		Kind: domain.SourceExternal, Content: "o que eu vi lá",
		ExternalRef: "https://example.com/post/1",
	}); err != nil {
		t.Fatalf("an external source naming its origin was refused: %v", err)
	}
}

// ══════════════════════════════════════════════════════════════════════
//
//	THERE IS NO PATH THAT EDITS A SOURCE
//
// ══════════════════════════════════════════════════════════════════════
//
// This cannot be asserted by calling something and checking it failed,
// because the point is that there is nothing to call. So it is asserted
// where the absence lives: the application service exposes no method, and
// the repository contains no UPDATE.
//
// A test that only read the source code would be weak on its own; paired
// with the compile-time fact that `app.Service` has no UpdateSource and
// `ports.SourceRepo` declares none, it is the readable half of a
// guarantee the type system already holds.
func TestNothingInThisContextCanEditASource(t *testing.T) {
	e := newEnv(t)
	s := e.source(e.mine, "a transcrição original", domain.SensitivityPrivate)

	// The evidence is still exactly what was written. Every operation
	// this slice offers has run against this workspace by now in other
	// tests; none of them can have touched it.
	got, err := e.svc.GetSource(e.ctx(), e.mine, s.ID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got.Content != "a transcrição original" {
		t.Errorf("content = %q, want the original", got.Content)
	}
	if got.Sensitivity != domain.SensitivityPrivate {
		t.Errorf("sensitivity = %q, want the original", got.Sensitivity)
	}
	if !got.UpdatedAt.Equal(got.CreatedAt) {
		t.Errorf("a source that was never edited has updated_at %v and created_at %v",
			got.UpdatedAt, got.CreatedAt)
	}
}

/* ══════════════════════════════════════════════════════════════════════
   Provenance · both ends in the same workspace
   ══════════════════════════════════════════════════════════════════════ */

func TestProvenanceLinksAMemoryToItsEvidence(t *testing.T) {
	e := newEnv(t)
	m := e.memoryAt(e.mine, "ele vai sair da empresa em março", domain.SensitivityNormal)
	s := e.source(e.mine, "transcrição da conversa", domain.SensitivityNormal)

	res, err := e.svc.LinkSource(e.ctx(), e.mine, m.ID, s.ID)
	if err != nil {
		t.Fatalf("link: %v", err)
	}
	if !res.Created {
		t.Error("the first link reported that it already existed")
	}

	sources, err := e.svc.SourcesFor(e.ctx(), e.mine, m.ID)
	if err != nil {
		t.Fatalf("read provenance: %v", err)
	}
	if len(sources) != 1 || sources[0].ID != s.ID {
		t.Fatalf("provenance returned %d sources, want the one that was linked", len(sources))
	}
}

func TestProvenanceRefusesAMemoryFromAnotherWorkspace(t *testing.T) {
	e := newEnv(t)
	theirMemory := e.memoryAt(e.theirs, "a memória deles", domain.SensitivityNormal)
	myMemory := e.memoryAt(e.mine, "minha memória", domain.SensitivityNormal)
	mySource := e.source(e.mine, "minha evidência", domain.SensitivityNormal)
	theirSource := e.source(e.theirs, "a evidência deles", domain.SensitivityNormal)

	// Their memory, my source.
	_, err := e.svc.LinkSource(e.ctx(), e.mine, theirMemory.ID, mySource.ID)
	assertNotFound(t, "linking a neighbour's memory", err)

	// My memory, their source.
	_, err = e.svc.LinkSource(e.ctx(), e.mine, myMemory.ID, theirSource.ID)
	assertNotFound(t, "linking a neighbour's source", err)

	// Both theirs.
	_, err = e.svc.LinkSource(e.ctx(), e.mine, theirMemory.ID, theirSource.ID)
	assertNotFound(t, "linking two of a neighbour's rows", err)

	// And nothing was written, in either workspace.
	for ws, id := range map[uuid.UUID]uuid.UUID{e.mine: myMemory.ID, e.theirs: theirMemory.ID} {
		sources, err := e.svc.SourcesFor(e.ctx(), ws, id)
		if err != nil {
			t.Fatalf("read provenance: %v", err)
		}
		if len(sources) != 0 {
			t.Errorf("a cross-workspace link was written: %d sources", len(sources))
		}
	}
}

func TestACrossWorkspaceLinkIsIndistinguishableFromAFabricatedOne(t *testing.T) {
	e := newEnv(t)
	m := e.memoryAt(e.mine, "minha memória", domain.SensitivityNormal)
	theirSource := e.source(e.theirs, "a evidência deles", domain.SensitivityNormal)
	fabricated := uuid.New()

	_, errReal := e.svc.LinkSource(e.ctx(), e.mine, m.ID, theirSource.ID)
	_, errFake := e.svc.LinkSource(e.ctx(), e.mine, m.ID, fabricated)

	assertNotFound(t, "neighbour's source", errReal)
	assertNotFound(t, "fabricated source", errFake)

	realMsg := strings.Replace(errReal.Error(), theirSource.ID.String(), "<id>", 1)
	fakeMsg := strings.Replace(errFake.Error(), fabricated.String(), "<id>", 1)
	if realMsg != fakeMsg {
		t.Errorf("the two answers differ:\n  real: %s\n  fake: %s", realMsg, fakeMsg)
	}
}

func TestLinkingTheSameEvidenceTwiceIsIdempotent(t *testing.T) {
	// The primary key is (memory_id, source_id), so a repeat cannot
	// duplicate a row. Reporting the difference is what lets a caller say
	// "já estava registrado" instead of claiming work it did not do.
	e := newEnv(t)
	m := e.memoryAt(e.mine, "uma conclusão", domain.SensitivityNormal)
	s := e.source(e.mine, "a evidência", domain.SensitivityNormal)

	first, err := e.svc.LinkSource(e.ctx(), e.mine, m.ID, s.ID)
	if err != nil {
		t.Fatalf("first link: %v", err)
	}
	second, err := e.svc.LinkSource(e.ctx(), e.mine, m.ID, s.ID)
	if err != nil {
		t.Fatalf("second link: %v", err)
	}

	if !first.Created {
		t.Error("the first link reported that it already existed")
	}
	if second.Created {
		t.Error("the second link reported that it created something")
	}

	sources, err := e.svc.SourcesFor(e.ctx(), e.mine, m.ID)
	if err != nil {
		t.Fatalf("read provenance: %v", err)
	}
	if len(sources) != 1 {
		t.Errorf("provenance has %d rows after linking twice, want 1", len(sources))
	}

	// And the row itself was not duplicated, checked in the table.
	var rows int
	if err := e.pool.QueryRow(e.ctx(), `
		SELECT count(*) FROM palace.memory_sources
		WHERE workspace_id = $1 AND memory_id = $2 AND source_id = $3`,
		e.mine, m.ID, s.ID).Scan(&rows); err != nil {
		t.Fatalf("count links: %v", err)
	}
	if rows != 1 {
		t.Errorf("the table holds %d links, want 1", rows)
	}
}

func TestProvenanceOfAFabricatedMemoryIsNotFoundRatherThanEmpty(t *testing.T) {
	// An empty list is a real and different answer: a memory nobody has
	// cited evidence for.
	e := newEnv(t)
	_, err := e.svc.SourcesFor(e.ctx(), e.mine, uuid.New())
	assertNotFound(t, "provenance of a fabricated memory", err)

	theirs := e.memoryAt(e.theirs, "a memória deles", domain.SensitivityNormal)
	_, err = e.svc.SourcesFor(e.ctx(), e.mine, theirs.ID)
	assertNotFound(t, "provenance of a neighbour's memory", err)

	mine := e.memoryAt(e.mine, "minha memória", domain.SensitivityNormal)
	sources, err := e.svc.SourcesFor(e.ctx(), e.mine, mine.ID)
	if err != nil {
		t.Fatalf("read provenance: %v", err)
	}
	if len(sources) != 0 {
		t.Errorf("a memory with no evidence reported %d sources", len(sources))
	}
}

func TestProvenanceIsOrderedByWhenTheBeliefWasBuilt(t *testing.T) {
	// Oldest link first, not oldest evidence: a transcript from last year
	// cited today is the most recent thing that happened to this memory.
	e := newEnv(t)
	m := e.memoryAt(e.mine, "uma conclusão", domain.SensitivityNormal)

	old := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	older, err := e.svc.CreateSource(e.ctx(), e.mine, app.CreateSourceInput{
		Kind: domain.SourceText, Content: "evidência antiga", CapturedAt: &old,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	recent := e.source(e.mine, "evidência recente", domain.SensitivityNormal)

	// Linked in the opposite order to their capture dates.
	if _, err := e.svc.LinkSource(e.ctx(), e.mine, m.ID, recent.ID); err != nil {
		t.Fatalf("link: %v", err)
	}
	if _, err := e.svc.LinkSource(e.ctx(), e.mine, m.ID, older.ID); err != nil {
		t.Fatalf("link: %v", err)
	}

	sources, err := e.svc.SourcesFor(e.ctx(), e.mine, m.ID)
	if err != nil {
		t.Fatalf("read provenance: %v", err)
	}
	if len(sources) != 2 {
		t.Fatalf("%d sources, want 2", len(sources))
	}
	if sources[0].ID != recent.ID {
		t.Error("provenance is ordered by capture date rather than by when the link was made")
	}
}

/* ══════════════════════════════════════════════════════════════════════
   The sensitivity floor
   ══════════════════════════════════════════════════════════════════════ */

func TestEvidenceEstablishesAFloorUnderTheMemoryThatCitesIt(t *testing.T) {
	// ── The leak this closes ───────────────────────────────────────────
	// A memory that rests on private evidence and is itself `normal`
	// appears in every default listing. The source does not, and that
	// asymmetry is the leak: the conclusion describes the material.
	e := newEnv(t)

	cases := []struct {
		memory, evidence domain.Sensitivity
		allowed          bool
	}{
		{domain.SensitivityNormal, domain.SensitivityNormal, true},
		{domain.SensitivityPrivate, domain.SensitivityNormal, true},
		{domain.SensitivityPrivate, domain.SensitivityPrivate, true},
		{domain.SensitivityHighlySensitive, domain.SensitivityHighlySensitive, true},
		{domain.SensitivityNormal, domain.SensitivityPrivate, false},
		{domain.SensitivityNormal, domain.SensitivityHighlySensitive, false},
		{domain.SensitivityPrivate, domain.SensitivityHighlySensitive, false},
	}

	for _, c := range cases {
		name := fmt.Sprintf("%s memory on %s evidence", c.memory, c.evidence)
		m := e.memoryAt(e.mine, "uma conclusão", c.memory)
		s := e.source(e.mine, "a evidência", c.evidence)

		_, err := e.svc.LinkSource(e.ctx(), e.mine, m.ID, s.ID)
		if c.allowed && err != nil {
			t.Errorf("%s was refused: %v", name, err)
		}
		if !c.allowed {
			if err == nil {
				t.Errorf("%s was allowed", name)
				continue
			}
			assertInvalid(t, name, err)
			// And nothing was written.
			sources, readErr := e.svc.SourcesFor(e.ctx(), e.mine, m.ID)
			if readErr != nil {
				t.Fatalf("read provenance: %v", readErr)
			}
			if len(sources) != 0 {
				t.Errorf("%s: a refused link was written anyway", name)
			}
		}
	}
}

func TestTheFloorRefusesRatherThanRaisingTheMemory(t *testing.T) {
	// Raising it would be the system deciding, silently, that something
	// the operator called ordinary is now private, and they would find
	// out when they went looking for it in a listing where it no longer
	// is.
	e := newEnv(t)
	m := e.memoryAt(e.mine, "uma conclusão", domain.SensitivityNormal)
	s := e.source(e.mine, "a evidência", domain.SensitivityPrivate)

	if _, err := e.svc.LinkSource(e.ctx(), e.mine, m.ID, s.ID); err == nil {
		t.Fatal("want a refusal")
	}

	after, err := e.svc.GetMemory(e.ctx(), e.mine, m.ID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if after.Sensitivity != domain.SensitivityNormal {
		t.Errorf("the memory was silently raised to %q", after.Sensitivity)
	}
}

func TestRaisingTheMemoryFirstIsTheWayThrough(t *testing.T) {
	// The refusal has to leave an actionable path, or it is a dead end.
	e := newEnv(t)
	m := e.memoryAt(e.mine, "uma conclusão", domain.SensitivityNormal)
	s := e.source(e.mine, "a evidência", domain.SensitivityPrivate)

	if _, err := e.svc.LinkSource(e.ctx(), e.mine, m.ID, s.ID); err == nil {
		t.Fatal("want a refusal first")
	}

	private := domain.SensitivityPrivate
	if _, err := e.svc.UpdateMemory(e.ctx(), e.mine, m.ID,
		domain.MemoryChange{Sensitivity: &private}); err != nil {
		t.Fatalf("raise: %v", err)
	}
	if _, err := e.svc.LinkSource(e.ctx(), e.mine, m.ID, s.ID); err != nil {
		t.Fatalf("after raising, the link was still refused: %v", err)
	}
}

// ══════════════════════════════════════════════════════════════════════
//
//	THE SECOND DOOR: RELABELLING A MEMORY THAT ALREADY CITES EVIDENCE
//
// ══════════════════════════════════════════════════════════════════════
//
// Linking refuses a memory below its evidence. Relabelling reaches the
// same state by another route: link a private source to a private
// memory, then call the memory `normal`, and the conclusion is in every
// default listing while the material behind it is withheld.
//
// A rule enforced at one door is not a rule.
func TestAMemoryCannotBeLoweredBelowTheEvidenceItAlreadyCites(t *testing.T) {
	e := newEnv(t)
	m := e.memoryAt(e.mine, "uma conclusão", domain.SensitivityPrivate)
	s := e.source(e.mine, "a evidência", domain.SensitivityPrivate)

	if _, err := e.svc.LinkSource(e.ctx(), e.mine, m.ID, s.ID); err != nil {
		t.Fatalf("link: %v", err)
	}

	normal := domain.SensitivityNormal
	_, err := e.svc.UpdateMemory(e.ctx(), e.mine, m.ID,
		domain.MemoryChange{Sensitivity: &normal})
	assertInvalid(t, "lowering below the evidence", err)

	after, err := e.svc.GetMemory(e.ctx(), e.mine, m.ID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if after.Sensitivity != domain.SensitivityPrivate {
		t.Errorf("the memory was lowered to %q anyway", after.Sensitivity)
	}
}

func TestTheFloorIsTheHighestOfSeveralSources(t *testing.T) {
	e := newEnv(t)
	m := e.memoryAt(e.mine, "uma conclusão", domain.SensitivityHighlySensitive)
	ordinary := e.source(e.mine, "evidência comum", domain.SensitivityNormal)
	private := e.source(e.mine, "evidência pessoal", domain.SensitivityPrivate)

	for _, s := range []*domain.Source{ordinary, private} {
		if _, err := e.svc.LinkSource(e.ctx(), e.mine, m.ID, s.ID); err != nil {
			t.Fatalf("link: %v", err)
		}
	}

	// Down to private: allowed, because private is the highest floor.
	privateLevel := domain.SensitivityPrivate
	if _, err := e.svc.UpdateMemory(e.ctx(), e.mine, m.ID,
		domain.MemoryChange{Sensitivity: &privateLevel}); err != nil {
		t.Fatalf("lowering to the floor was refused: %v", err)
	}

	// Down to normal: refused, because one source sits above it. The
	// ordinary source must not drag the floor down.
	normal := domain.SensitivityNormal
	_, err := e.svc.UpdateMemory(e.ctx(), e.mine, m.ID,
		domain.MemoryChange{Sensitivity: &normal})
	assertInvalid(t, "lowering below the highest source", err)
}

func TestAMemoryWithNoEvidenceCanBeLoweredFreely(t *testing.T) {
	// The floor exists only where there is evidence. A memory nobody
	// cited anything for is the operator's to label as they like.
	e := newEnv(t)
	m := e.memoryAt(e.mine, "uma conclusão", domain.SensitivityHighlySensitive)

	normal := domain.SensitivityNormal
	res, err := e.svc.UpdateMemory(e.ctx(), e.mine, m.ID,
		domain.MemoryChange{Sensitivity: &normal})
	if err != nil {
		t.Fatalf("lowering an uncited memory was refused: %v", err)
	}
	if !res.SensitivityChanged || res.Memory.Sensitivity != domain.SensitivityNormal {
		t.Errorf("the move was not applied: %+v", res.MemoryChangeResult)
	}
}

func TestRaisingAMemoryIsNeverRefusedByTheFloor(t *testing.T) {
	e := newEnv(t)
	m := e.memoryAt(e.mine, "uma conclusão", domain.SensitivityNormal)
	s := e.source(e.mine, "a evidência", domain.SensitivityNormal)
	if _, err := e.svc.LinkSource(e.ctx(), e.mine, m.ID, s.ID); err != nil {
		t.Fatalf("link: %v", err)
	}

	for _, level := range []domain.Sensitivity{domain.SensitivityPrivate, domain.SensitivityHighlySensitive} {
		l := level
		if _, err := e.svc.UpdateMemory(e.ctx(), e.mine, m.ID,
			domain.MemoryChange{Sensitivity: &l}); err != nil {
			t.Fatalf("raising to %q was refused: %v", level, err)
		}
	}
}

func TestEveryMemoryIsAtLeastAsWithheldAsItsEvidence(t *testing.T) {
	// The property the two doors exist to hold, checked as a property
	// rather than as a sequence of steps: after exercising both paths,
	// no memory in the workspace sits below anything it cites.
	e := newEnv(t)

	for _, level := range domain.Sensitivities {
		m := e.memoryAt(e.mine, "conclusão "+string(level), level)
		for _, evidence := range domain.Sensitivities {
			s := e.source(e.mine, "evidência "+string(evidence), evidence)
			// Refusals are expected and ignored; what matters is what got in.
			_, _ = e.svc.LinkSource(e.ctx(), e.mine, m.ID, s.ID)
		}
	}

	rows, err := e.pool.Query(e.ctx(), `
		SELECT m.sensitivity, s.sensitivity
		FROM palace.memory_sources ms
		JOIN palace.memories m ON m.workspace_id = ms.workspace_id AND m.id = ms.memory_id
		JOIN palace.sources  s ON s.workspace_id = ms.workspace_id AND s.id = ms.source_id
		WHERE ms.workspace_id = $1`, e.mine)
	if err != nil {
		t.Fatalf("read links: %v", err)
	}
	defer rows.Close()

	seen := 0
	for rows.Next() {
		var memory, evidence domain.Sensitivity
		if err := rows.Scan(&memory, &evidence); err != nil {
			t.Fatalf("scan: %v", err)
		}
		seen++
		if !memory.AtLeastAsRestrictiveAs(evidence) {
			t.Errorf("a %s memory rests on %s evidence", memory, evidence)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read links: %v", err)
	}
	if seen == 0 {
		t.Fatal("no links were written at all; the test proved nothing")
	}
}

/* ══════════════════════════════════════════════════════════════════════
   Memory → Artifact, now that there is an authority to resolve it
   ══════════════════════════════════════════════════════════════════════ */

func TestAMemoryCanNowNameAnArtifactOfItsOwnWorkspace(t *testing.T) {
	e := newEnv(t)
	a := e.artifact(e.mine, domain.ArtifactProject, "Palace", domain.SensitivityNormal, nil)

	m, err := e.svc.CreateMemory(e.ctx(), e.mine, app.CreateMemoryInput{
		Kind: domain.MemoryDecision, Content: "decidi usar tabela em vez de JSONB",
		ArtifactID: &a.ID,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if m.ArtifactID == nil || *m.ArtifactID != a.ID {
		t.Fatalf("the memory was not attached: %v", m.ArtifactID)
	}

	loaded, err := e.svc.GetMemory(e.ctx(), e.mine, m.ID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if loaded.ArtifactID == nil || *loaded.ArtifactID != a.ID {
		t.Errorf("the attachment did not survive the round trip: %v", loaded.ArtifactID)
	}
}

func TestAMemoryCannotNameANeighboursArtifact(t *testing.T) {
	e := newEnv(t)
	theirs := e.artifact(e.theirs, domain.ArtifactProject, "o projeto deles", domain.SensitivityNormal, nil)
	fabricated := uuid.New()

	_, errReal := e.svc.CreateMemory(e.ctx(), e.mine, app.CreateMemoryInput{
		Kind: domain.MemoryFact, Content: "tentativa", ArtifactID: &theirs.ID,
	})
	_, errFake := e.svc.CreateMemory(e.ctx(), e.mine, app.CreateMemoryInput{
		Kind: domain.MemoryFact, Content: "tentativa", ArtifactID: &fabricated,
	})

	// Not a constraint violation. The application layer resolves it and
	// answers not-found, identically to a fabricated id, which is what
	// the composite foreign key alone could not do.
	assertNotFound(t, "naming a neighbour's artifact", errReal)
	assertNotFound(t, "naming a fabricated artifact", errFake)

	realMsg := strings.Replace(errReal.Error(), theirs.ID.String(), "<id>", 1)
	fakeMsg := strings.Replace(errFake.Error(), fabricated.String(), "<id>", 1)
	if realMsg != fakeMsg {
		t.Errorf("the two answers differ:\n  real: %s\n  fake: %s", realMsg, fakeMsg)
	}
}

func TestAMemoryCannotBeMovedOntoANeighboursArtifact(t *testing.T) {
	e := newEnv(t)
	theirs := e.artifact(e.theirs, domain.ArtifactProject, "o projeto deles", domain.SensitivityNormal, nil)
	mine := e.memoryAt(e.mine, "minha memória", domain.SensitivityNormal)

	_, err := e.svc.UpdateMemory(e.ctx(), e.mine, mine.ID,
		domain.MemoryChange{Artifact: domain.SetRef(theirs.ID)})
	assertNotFound(t, "moving onto a neighbour's artifact", err)

	after, err := e.svc.GetMemory(e.ctx(), e.mine, mine.ID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if after.ArtifactID != nil {
		t.Errorf("the memory was attached to a neighbour's artifact: %v", after.ArtifactID)
	}
}

func TestAMemoryCanBeDetachedFromItsArtifact(t *testing.T) {
	// Letting go of a reference names no row, so there is nothing to
	// resolve and nothing that could belong to somebody else.
	e := newEnv(t)
	a := e.artifact(e.mine, domain.ArtifactProject, "Palace", domain.SensitivityNormal, nil)
	m, err := e.svc.CreateMemory(e.ctx(), e.mine, app.CreateMemoryInput{
		Kind: domain.MemoryFact, Content: "uma nota", ArtifactID: &a.ID,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	res, err := e.svc.UpdateMemory(e.ctx(), e.mine, m.ID,
		domain.MemoryChange{Artifact: domain.ClearRef()})
	if err != nil {
		t.Fatalf("detach: %v", err)
	}
	if !res.ArtifactChanged || res.Memory.ArtifactID != nil {
		t.Errorf("the memory was not detached: %v", res.Memory.ArtifactID)
	}
}

func TestAttachingAnArtifactDoesNotPropagateItsSensitivity(t *testing.T) {
	// ── Contextual link, not provenance ────────────────────────────────
	// Attaching a memory to an artifact says what it is ABOUT. Only
	// provenance says it RESTS ON something, and only provenance
	// constrains the level.
	//
	// The consequence is named rather than discovered: a normal memory
	// attached to a highly sensitive artifact does appear in default
	// listings. That is this design's answer, and this test is what makes
	// changing it a deliberate act.
	e := newEnv(t)
	secret := e.artifact(e.mine, domain.ArtifactProject, "projeto reservado",
		domain.SensitivityHighlySensitive, nil)

	m, err := e.svc.CreateMemory(e.ctx(), e.mine, app.CreateMemoryInput{
		Kind: domain.MemoryFact, Content: "uma observação comum", ArtifactID: &secret.ID,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if m.Sensitivity != domain.SensitivityNormal {
		t.Errorf("the memory was raised to %q by its artifact", m.Sensitivity)
	}

	got, total, err := e.svc.ListMemories(e.ctx(), e.mine, ports.MemoryFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 || total != 1 {
		t.Errorf("the memory did not stay in the default listing: %d rows, total %d", len(got), total)
	}

	// The artifact itself is still withheld, which is the asymmetry the
	// decision accepts.
	artifacts, _, err := e.svc.ListArtifacts(e.ctx(), e.mine, ports.ArtifactFilter{})
	if err != nil {
		t.Fatalf("list artifacts: %v", err)
	}
	if len(artifacts) != 0 {
		t.Errorf("the highly sensitive artifact appeared in a default listing")
	}
}

func TestFilingAMemoryInARoomStillDoesNotPropagateSensitivity(t *testing.T) {
	// The same boundary from the Room side, so neither contextual link
	// acquires provenance semantics by accident.
	e := newEnv(t)
	secret := e.room(e.mine, "Terapia", domain.SensitivityHighlySensitive)

	m := e.memory(e.mine, "uma nota comum", domain.SensitivityNormal, &secret.ID)
	if m.Sensitivity != domain.SensitivityNormal {
		t.Errorf("the memory was raised to %q by its room", m.Sensitivity)
	}
}

/* ══════════════════════════════════════════════════════════════════════
   Nothing new leaks
   ══════════════════════════════════════════════════════════════════════ */

func TestNoSourceOrProvenanceFailurePathCarriesContent(t *testing.T) {
	e := newEnv(t)
	normalMemory := e.memoryAt(e.mine, canary, domain.SensitivityNormal)
	privateSource := e.source(e.mine, canary, domain.SensitivityPrivate)
	theirSource := e.source(e.theirs, canary, domain.SensitivityNormal)

	cases := map[string]func() error{
		"invalid source kind": func() error {
			_, err := e.svc.CreateSource(e.ctx(), e.mine, app.CreateSourceInput{
				Kind: "audio", Content: canary,
			})
			return err
		},
		"source content too long": func() error {
			_, err := e.svc.CreateSource(e.ctx(), e.mine, app.CreateSourceInput{
				Kind: domain.SourceText, Content: canary + strings.Repeat("x", domain.MaxSourceContent),
			})
			return err
		},
		"external with no ref": func() error {
			_, err := e.svc.CreateSource(e.ctx(), e.mine, app.CreateSourceInput{
				Kind: domain.SourceExternal, Content: canary,
			})
			return err
		},
		"floor violation": func() error {
			_, err := e.svc.LinkSource(e.ctx(), e.mine, normalMemory.ID, privateSource.ID)
			return err
		},
		"foreign source": func() error {
			_, err := e.svc.LinkSource(e.ctx(), e.mine, normalMemory.ID, theirSource.ID)
			return err
		},
		"fabricated memory": func() error {
			_, err := e.svc.LinkSource(e.ctx(), e.mine, uuid.New(), privateSource.ID)
			return err
		},
	}

	for name, run := range cases {
		err := run()
		if err == nil {
			t.Errorf("%s: want a failure", name)
			continue
		}
		if strings.Contains(err.Error(), canary) {
			t.Errorf("%s: the failure quoted content: %v", name, err)
		}
	}

	if strings.Contains(e.logs.String(), canary) {
		t.Errorf("the service logged content: %s", e.logs.String())
	}
}

func TestSourcesLoadedFromPostgresStillRedactThemselves(t *testing.T) {
	e := newEnv(t)
	m := e.memoryAt(e.mine, "uma conclusão", domain.SensitivityHighlySensitive)
	s := e.source(e.mine, canary, domain.SensitivityHighlySensitive)
	if _, err := e.svc.LinkSource(e.ctx(), e.mine, m.ID, s.ID); err != nil {
		t.Fatalf("link: %v", err)
	}

	// Through the provenance read, which is the path that hands a whole
	// source to a caller.
	sources, err := e.svc.SourcesFor(e.ctx(), e.mine, m.ID)
	if err != nil {
		t.Fatalf("read provenance: %v", err)
	}
	if len(sources) != 1 {
		t.Fatalf("%d sources, want 1", len(sources))
	}

	raw, err := json.Marshal(sources[0])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), canary) {
		t.Errorf("a source loaded through provenance serialised its content: %s", raw)
	}
	if strings.Contains(fmt.Sprintf("%v", sources), canary) {
		t.Errorf("a slice of sources formatted their content")
	}
}

/* ══════════════════════════════════════════════════════════════════════
   palace.memory.link_source, through the application service
   ══════════════════════════════════════════════════════════════════════ */

// The capability is a thin exposure of LinkSource, so what it owes is that
// it changes nothing about the operation: the same isolation, the same
// floor, the same idempotency. The behaviour itself is proven above; these
// assert that nothing was re-implemented on the way out.
func TestTheLinkCapabilityCreatesNeitherEndAndEditsNeither(t *testing.T) {
	e := newEnv(t)
	m := e.memoryAt(e.mine, "uma conclusão", domain.SensitivityPrivate)
	s := e.source(e.mine, "a evidência", domain.SensitivityPrivate)

	before := e.countEverything()

	res, err := e.svc.LinkSource(e.ctx(), e.mine, m.ID, s.ID)
	if err != nil {
		t.Fatalf("link: %v", err)
	}
	if !res.Created {
		t.Error("the first link reported that it already existed")
	}

	after := e.countEverything()
	for table, n := range before {
		if table == "memory_sources" {
			continue
		}
		if after[table] != n {
			t.Errorf("linking changed %s: %d rows before, %d after", table, n, after[table])
		}
	}

	// Neither end was edited.
	gotMemory, err := e.svc.GetMemory(e.ctx(), e.mine, m.ID)
	if err != nil {
		t.Fatalf("read memory: %v", err)
	}
	if gotMemory.Content != m.Content || !gotMemory.UpdatedAt.Equal(m.UpdatedAt) {
		t.Error("linking edited the memory")
	}
	gotSource, err := e.svc.GetSource(e.ctx(), e.mine, s.ID)
	if err != nil {
		t.Fatalf("read source: %v", err)
	}
	if gotSource.Content != s.Content || !gotSource.UpdatedAt.Equal(s.UpdatedAt) {
		t.Error("linking edited the evidence")
	}
}
