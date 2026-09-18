//go:build integration

// Palace, slice S3: relation neighbours, resolved under the surface's own
// rules and never under their own.
//
//	Run with: TEST_POSTGRES_DSN=... go test -tags=integration ./internal/palace/...
//
// ══════════════════════════════════════════════════════════════════════
//
//	RELATIONS CONSUME SURFACE VISIBILITY. THEY DO NOT DEFINE IT
//
// ══════════════════════════════════════════════════════════════════════
//
// The sentences this suite has to make convincing:
//
//	A withheld anchor answers not-found, and the question "what is
//	connected to it" is never asked.
//
//	A neighbour withheld by its own level, by its room, or by the room of
//	the artifact it describes, disappears completely, and nothing in the
//	answer counts it.
//
//	SUPERSEDES keeps the direction the domain froze, read from the domain
//	and not from the column names.
//
//	An archived neighbour is SHOWN and labelled, because a history with
//	its retired links removed is a history with holes in it.
package palace

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/palace/app"
	"github.com/corsi/backend/internal/palace/domain"
	"github.com/corsi/backend/internal/palace/ports"
)

/* ── reading the answer ──────────────────────────────────────────────── */

// neighbours is the decoded response, in the shape the assertions want.
type neighbours struct {
	Supersedes   []map[string]any `json:"supersedes"`
	SupersededBy []map[string]any `json:"superseded_by"`
	Mentions     []map[string]any `json:"mentions"`
	MentionedBy  []map[string]any `json:"mentioned_by"`
	Related      []map[string]any `json:"related"`
	Decisions    []map[string]any `json:"decisions"`
}

func (s *surface) neighboursOf(t *testing.T, ws, artifactID uuid.UUID) (neighbours, string) {
	t.Helper()
	res := s.as(ws, "/palace/artifacts/"+artifactID.String()+"/neighbors").
		requireStatus(t, http.StatusOK, "neighbours")
	var out neighbours
	if err := json.Unmarshal([]byte(res.body), &out); err != nil {
		t.Fatalf("neighbours: %v (%s)", err, res.body)
	}
	return out, res.body
}

// idSet pulls whichever id key a group carries.
func idSet(rows []map[string]any) map[string]bool {
	out := map[string]bool{}
	for _, row := range rows {
		for _, key := range []string{"artifact_id", "memory_id", "id"} {
			if v, ok := row[key].(string); ok {
				out[v] = true
			}
		}
	}
	return out
}

// memoryDecision seeds a memory of the one kind DECISION_FOR may start at.
func (e *env) memoryDecision(ws uuid.UUID, content string, level domain.Sensitivity, roomID *uuid.UUID) *domain.Memory {
	e.t.Helper()
	m, err := e.svc.CreateMemory(e.ctx(), ws, app.CreateMemoryInput{
		Kind:        domain.MemoryDecision,
		Content:     content,
		Sensitivity: &level,
		RoomID:      roomID,
	})
	if err != nil {
		e.t.Fatalf("seed decision: %v", err)
	}
	return m
}

/* ══════════════════════════════════════════════════════════════════════
   A · The anchor is resolved first, and alone
   ══════════════════════════════════════════════════════════════════════ */

// TestNeighboursOfAWithheldAnchorAreNotFound covers the four ways an
// anchor fails, and they must all answer identically.
//
// The withheld anchors here HAVE neighbours, and visible ones at that. If
// the handler read the relations before resolving the anchor, a bug that
// returned them would be caught; a bug that merely wasted the read would
// not, which is why the ordering is also asserted at the app layer below.
func TestNeighboursOfAWithheldAnchorAreNotFound(t *testing.T) {
	e := newEnv(t)
	s := e.mount()

	visibleRoom := e.room(e.mine, "Ateliê", domain.SensitivityNormal)
	withheldRoom := e.room(e.mine, "Terapia", domain.SensitivityHighlySensitive)
	companion := e.artifact(e.mine, domain.ArtifactNote, "Vizinho visível",
		domain.SensitivityNormal, &visibleRoom.ID)

	// Anchor 1: highly sensitive on its own account.
	byLevel := e.artifact(e.mine, domain.ArtifactNote, "Segredo",
		domain.SensitivityHighlySensitive, &visibleRoom.ID)
	// Anchor 2: perfectly normal, inside a withheld room. This is D1.
	byRoom := e.artifact(e.mine, domain.ArtifactList, "Lista comum",
		domain.SensitivityNormal, &withheldRoom.ID)
	// Anchor 3: a neighbour's.
	theirRoom := e.room(e.theirs, "A sala deles", domain.SensitivityNormal)
	theirs := e.artifact(e.theirs, domain.ArtifactNote, "O deles",
		domain.SensitivityNormal, &theirRoom.ID)

	// Both withheld anchors really do have a visible neighbour.
	e.relate(e.mine, domain.EntityArtifact, byLevel.ID, domain.RelationRelatedTo,
		domain.EntityArtifact, companion.ID)
	e.relate(e.mine, domain.EntityArtifact, byRoom.ID, domain.RelationRelatedTo,
		domain.EntityArtifact, companion.ID)

	fabricated := uuid.New()
	missing := s.as(e.mine, "/palace/artifacts/"+fabricated.String()+"/neighbors").
		requireStatus(t, http.StatusNotFound, "fabricated anchor")

	for _, tc := range []struct {
		what string
		id   uuid.UUID
	}{
		{"highly sensitive anchor", byLevel.ID},
		{"normal anchor in a withheld room", byRoom.ID},
		{"a neighbour workspace's anchor", theirs.ID},
	} {
		res := s.as(e.mine, "/palace/artifacts/"+tc.id.String()+"/neighbors").
			requireStatus(t, http.StatusNotFound, tc.what)
		if normalizeID(res.body, tc.id) != normalizeID(missing.body, fabricated) {
			t.Errorf("%s answers %q but a fabricated id answers %q; the difference "+
				"confirms the artifact exists", tc.what, res.body, missing.body)
		}
		if strings.Contains(res.body, "Vizinho visível") {
			t.Errorf("%s leaked a neighbour: %s", tc.what, res.body)
		}
	}
}

// TestNoRelationIsReadForAWithheldAnchor asserts the ORDER, not just the
// outcome.
//
// It drives the application layer with a relation repository that records
// whether it was touched. A handler that read the edges first and filtered
// afterwards would still answer 404 and pass every test above; this is the
// one that says the question was never asked.
func TestNoRelationIsReadForAWithheldAnchor(t *testing.T) {
	e := newEnv(t)

	withheldRoom := e.room(e.mine, "Terapia", domain.SensitivityHighlySensitive)
	anchor := e.artifact(e.mine, domain.ArtifactList, "Lista comum",
		domain.SensitivityNormal, &withheldRoom.ID)

	spy := &countingRelations{RelationRepo: e.repos.Relations}
	svc := app.MustNewService(app.Deps{
		Rooms:      e.repos.Rooms,
		Memories:   e.repos.Memories,
		Artifacts:  e.repos.Artifacts,
		Items:      e.repos.Items,
		Sources:    e.repos.Sources,
		Provenance: e.repos.Provenance,
		Relations:  spy,
		Sessions:   e.repos.Sessions,
		Clock:      e.repos.Clock,
	})

	_, err := svc.ArtifactNeighbors(e.ctx(), e.mine, anchor.ID,
		ports.Visibility{InheritRoomVisibility: true})
	assertNotFound(t, "neighbours of a withheld anchor", err)

	if spy.listCalls != 0 {
		t.Fatalf("the relation repository was read %d times for an anchor that is "+
			"not visible; the edges must not be touched at all", spy.listCalls)
	}
}

// countingRelations records reads and delegates everything else.
type countingRelations struct {
	ports.RelationRepo
	listCalls int
}

func (c *countingRelations) List(ctx context.Context, workspaceID uuid.UUID, f ports.RelationFilter) ([]*domain.Relation, error) {
	c.listCalls++
	return c.RelationRepo.List(ctx, workspaceID, f)
}

/* ══════════════════════════════════════════════════════════════════════
   B · SUPERSEDES keeps the frozen direction
   ══════════════════════════════════════════════════════════════════════ */

// TestSupersedesIsReportedInTheFrozenDirection is the inversion detector.
//
// ── Why it reads the chain from BOTH ends ──────────────────────────────
// Because an implementation that swapped the two keys would be internally
// consistent when read from one end only. Asserting from A that it
// supersedes B, and from B that it is superseded by A, makes the two
// answers constrain each other: a swap breaks both at once, and it cannot
// be made to pass by writing the fixture the other way round.
func TestSupersedesIsReportedInTheFrozenDirection(t *testing.T) {
	e := newEnv(t)
	s := e.mount()

	room := e.room(e.mine, "Decisões", domain.SensitivityNormal)
	oldOne := e.artifact(e.mine, domain.ArtifactNote, "Talvez Python",
		domain.SensitivityNormal, &room.ID)
	newOne := e.artifact(e.mine, domain.ArtifactNote, "Backend será Go",
		domain.SensitivityNormal, &room.ID)

	// The row reads as a sentence: NEW supersedes OLD.
	e.relate(e.mine, domain.EntityArtifact, newOne.ID, domain.RelationSupersedes,
		domain.EntityArtifact, oldOne.ID)

	fromNew, body := s.neighboursOf(t, e.mine, newOne.ID)
	if !idSet(fromNew.Supersedes)[oldOne.ID.String()] {
		t.Errorf("the replacement does not report superseding the old one: %s", body)
	}
	if len(fromNew.SupersededBy) != 0 {
		t.Errorf("the replacement reports being superseded; the direction is inverted: %s", body)
	}

	fromOld, body := s.neighboursOf(t, e.mine, oldOne.ID)
	if !idSet(fromOld.SupersededBy)[newOne.ID.String()] {
		t.Errorf("the old one does not report being superseded: %s", body)
	}
	if len(fromOld.Supersedes) != 0 {
		t.Errorf("the old one reports superseding something; the direction is inverted: %s", body)
	}

	// And the titles travel with the direction, so a reader cannot get a
	// coherent but backwards history out of this response.
	if fromNew.Supersedes[0]["title"] != "Talvez Python" {
		t.Errorf("supersedes names %v, want the OLD artifact", fromNew.Supersedes[0]["title"])
	}
	if fromOld.SupersededBy[0]["title"] != "Backend será Go" {
		t.Errorf("superseded_by names %v, want the NEW artifact", fromOld.SupersededBy[0]["title"])
	}
}

/* ══════════════════════════════════════════════════════════════════════
   C · Containment, through every relation kind
   ══════════════════════════════════════════════════════════════════════ */

// TestAWithheldNeighbourDisappearsFromEveryGroup covers the three ways a
// neighbour can be ineligible, across the relation kinds an artifact
// anchor can carry.
func TestAWithheldNeighbourDisappearsFromEveryGroup(t *testing.T) {
	e := newEnv(t)
	s := e.mount()

	visibleRoom := e.room(e.mine, "Ateliê", domain.SensitivityNormal)
	withheldRoom := e.room(e.mine, "Terapia", domain.SensitivityHighlySensitive)
	anchor := e.artifact(e.mine, domain.ArtifactProject, "O projeto",
		domain.SensitivityNormal, &visibleRoom.ID)

	// 1. Visible: must appear.
	visible := e.artifact(e.mine, domain.ArtifactNote, "Nota visível",
		domain.SensitivityNormal, &visibleRoom.ID)
	// 2. Withheld by its own level.
	byLevel := e.artifact(e.mine, domain.ArtifactNote, "Segredo "+withheldText,
		domain.SensitivityHighlySensitive, &visibleRoom.ID)
	// 3. D1: normal, inside a withheld room.
	byRoom := e.artifact(e.mine, domain.ArtifactNote, "Nota contida "+withheldText,
		domain.SensitivityNormal, &withheldRoom.ID)

	// 4. D1.1: a NORMAL memory in a VISIBLE room, about the artifact that
	// lives in the withheld room. Every row in the chain says normal about
	// itself except the last.
	transitive := e.memoryAbout(e.mine, "o que concluí daquilo "+withheldText,
		domain.SensitivityNormal, &visibleRoom.ID, &byRoom.ID)
	// 5. A visible memory, for contrast.
	visibleMemory := e.memoryAbout(e.mine, "algo que eu sei",
		domain.SensitivityNormal, &visibleRoom.ID, &visible.ID)

	for _, other := range []uuid.UUID{visible.ID, byLevel.ID, byRoom.ID} {
		e.relate(e.mine, domain.EntityArtifact, anchor.ID, domain.RelationRelatedTo,
			domain.EntityArtifact, other)
		e.relate(e.mine, domain.EntityArtifact, anchor.ID, domain.RelationMentions,
			domain.EntityArtifact, other)
	}
	for _, mem := range []uuid.UUID{transitive.ID, visibleMemory.ID} {
		e.relate(e.mine, domain.EntityMemory, mem, domain.RelationRelatedTo,
			domain.EntityArtifact, anchor.ID)
		e.relate(e.mine, domain.EntityMemory, mem, domain.RelationMentions,
			domain.EntityArtifact, anchor.ID)
	}

	got, body := s.neighboursOf(t, e.mine, anchor.ID)

	if strings.Contains(body, withheldText) {
		t.Fatalf("the response carries withheld content: %s", body)
	}
	if strings.Contains(body, "Terapia") {
		t.Fatalf("the response names the withheld room: %s", body)
	}

	related := idSet(got.Related)
	mentions := idSet(got.Mentions)
	mentionedBy := idSet(got.MentionedBy)

	if !related[visible.ID.String()] || !mentions[visible.ID.String()] {
		t.Errorf("the visible artifact neighbour is missing: %s", body)
	}
	if !related[visibleMemory.ID.String()] || !mentionedBy[visibleMemory.ID.String()] {
		t.Errorf("the visible memory neighbour is missing: %s", body)
	}

	for name, hidden := range map[string]uuid.UUID{
		"withheld by its own level":               byLevel.ID,
		"D1: normal inside withheld room":         byRoom.ID,
		"D1.1: memory about a contained artifact": transitive.ID,
	} {
		if related[hidden.String()] || mentions[hidden.String()] || mentionedBy[hidden.String()] {
			t.Errorf("a neighbour that must not appear did (%s): %s", name, hidden)
		}
		if strings.Contains(body, hidden.String()) {
			t.Errorf("the response carries the id of a withheld neighbour (%s): %s", name, body)
		}
	}
}

// TestNothingInTheAnswerCountsAWithheldNeighbour is the side-channel
// check. Ten edges, three eligible neighbours, and the response must know
// about three.
func TestNothingInTheAnswerCountsAWithheldNeighbour(t *testing.T) {
	e := newEnv(t)
	s := e.mount()

	visibleRoom := e.room(e.mine, "Ateliê", domain.SensitivityNormal)
	withheldRoom := e.room(e.mine, "Terapia", domain.SensitivityHighlySensitive)
	anchor := e.artifact(e.mine, domain.ArtifactProject, "O projeto",
		domain.SensitivityNormal, &visibleRoom.ID)

	var eligible []uuid.UUID
	for i := 0; i < 3; i++ {
		a := e.artifact(e.mine, domain.ArtifactNote, "Visível "+string(rune('A'+i)),
			domain.SensitivityNormal, &visibleRoom.ID)
		eligible = append(eligible, a.ID)
		e.relate(e.mine, domain.EntityArtifact, anchor.ID, domain.RelationRelatedTo,
			domain.EntityArtifact, a.ID)
	}
	for i := 0; i < 7; i++ {
		a := e.artifact(e.mine, domain.ArtifactNote, "Contido "+string(rune('A'+i))+" "+withheldText,
			domain.SensitivityNormal, &withheldRoom.ID)
		e.relate(e.mine, domain.EntityArtifact, anchor.ID, domain.RelationRelatedTo,
			domain.EntityArtifact, a.ID)
	}

	got, body := s.neighboursOf(t, e.mine, anchor.ID)

	if len(got.Related) != 3 {
		t.Fatalf("related has %d entries, want exactly the 3 eligible: %s", len(got.Related), body)
	}
	for _, id := range eligible {
		if !idSet(got.Related)[id.String()] {
			t.Errorf("an eligible neighbour is missing: %s", id)
		}
	}

	// Ten edges exist. Nothing in the body may say so, under any spelling.
	for _, marker := range []string{
		"total", "count", "hidden", "omitted", "withheld", "truncated", "\"10\"", ": 10",
	} {
		if strings.Contains(strings.ToLower(body), strings.ToLower(marker)) {
			t.Errorf("the response hints at what was dropped (%q): %s", marker, body)
		}
	}
}

/* ══════════════════════════════════════════════════════════════════════
   D · Archived neighbours are shown, and labelled
   ══════════════════════════════════════════════════════════════════════ */

// TestAnArchivedNeighbourIsShownMarked is D3 at the relation boundary.
//
// ── Why the relation is created BEFORE the archiving ───────────────────
// Because the domain refuses a NEW relation to an archived endpoint, which
// is the right rule and not the one under test here: the question is what
// happens to an edge that already existed when one end was later retired.
func TestAnArchivedNeighbourIsShownMarked(t *testing.T) {
	e := newEnv(t)
	s := e.mount()

	room := e.room(e.mine, "Dojang", domain.SensitivityNormal)
	anchor := e.artifact(e.mine, domain.ArtifactProject, "Faixa preta",
		domain.SensitivityNormal, &room.ID)
	retired := e.artifact(e.mine, domain.ArtifactList, "Equipamento velho",
		domain.SensitivityNormal, &room.ID)

	e.relate(e.mine, domain.EntityArtifact, anchor.ID, domain.RelationSupersedes,
		domain.EntityArtifact, retired.ID)
	e.archiveArtifact(e.mine, retired.ID)

	got, body := s.neighboursOf(t, e.mine, anchor.ID)
	if len(got.Supersedes) != 1 {
		t.Fatalf("the archived neighbour vanished; a supersedes chain with its retired "+
			"links removed is a history with holes in it: %s", body)
	}
	entry := got.Supersedes[0]
	if entry["artifact_id"] != retired.ID.String() {
		t.Fatalf("wrong neighbour: %v", entry)
	}
	if entry["status"] != string(domain.LifecycleArchived) {
		t.Errorf("status = %v, want archived; the caller has to be told rather than "+
			"left to assume the neighbour is active", entry["status"])
	}
}

// TestAnArchivedNeighbourInAWithheldRoomStillDisappears: being visible in
// the inspector is about lifecycle, and containment still prevails.
func TestAnArchivedNeighbourInAWithheldRoomStillDisappears(t *testing.T) {
	e := newEnv(t)
	s := e.mount()

	room := e.room(e.mine, "Dojang", domain.SensitivityNormal)
	withheldRoom := e.room(e.mine, "Terapia", domain.SensitivityHighlySensitive)
	anchor := e.artifact(e.mine, domain.ArtifactProject, "Faixa preta",
		domain.SensitivityNormal, &room.ID)
	contained := e.artifact(e.mine, domain.ArtifactNote, "Nota contida "+withheldText,
		domain.SensitivityNormal, &withheldRoom.ID)

	e.relate(e.mine, domain.EntityArtifact, anchor.ID, domain.RelationSupersedes,
		domain.EntityArtifact, contained.ID)
	e.archiveArtifact(e.mine, contained.ID)

	got, body := s.neighboursOf(t, e.mine, anchor.ID)
	if len(got.Supersedes) != 0 {
		t.Fatalf("an archived neighbour inside a withheld room appeared: %s", body)
	}
	if strings.Contains(body, withheldText) {
		t.Fatalf("withheld content in the body: %s", body)
	}
}

/* ══════════════════════════════════════════════════════════════════════
   E · Classification across the matrix
   ══════════════════════════════════════════════════════════════════════ */

func TestEveryRelationKindLandsInTheRightGroup(t *testing.T) {
	e := newEnv(t)
	s := e.mount()

	room := e.room(e.mine, "Ateliê", domain.SensitivityNormal)
	anchor := e.artifact(e.mine, domain.ArtifactProject, "O projeto",
		domain.SensitivityNormal, &room.ID)

	mentioned := e.artifact(e.mine, domain.ArtifactNote, "Citado", domain.SensitivityNormal, &room.ID)
	mentioner := e.artifact(e.mine, domain.ArtifactNote, "Cita", domain.SensitivityNormal, &room.ID)
	peer := e.artifact(e.mine, domain.ArtifactNote, "Par", domain.SensitivityNormal, &room.ID)
	decision := e.memoryDecision(e.mine, "decidi usar Go", domain.SensitivityNormal, &room.ID)
	teller := e.memoryAbout(e.mine, "isso menciona o projeto", domain.SensitivityNormal, &room.ID, nil)

	e.relate(e.mine, domain.EntityArtifact, anchor.ID, domain.RelationMentions,
		domain.EntityArtifact, mentioned.ID)
	e.relate(e.mine, domain.EntityArtifact, mentioner.ID, domain.RelationMentions,
		domain.EntityArtifact, anchor.ID)
	e.relate(e.mine, domain.EntityArtifact, anchor.ID, domain.RelationRelatedTo,
		domain.EntityArtifact, peer.ID)
	e.relate(e.mine, domain.EntityMemory, decision.ID, domain.RelationDecisionFor,
		domain.EntityArtifact, anchor.ID)
	e.relate(e.mine, domain.EntityMemory, teller.ID, domain.RelationMentions,
		domain.EntityArtifact, anchor.ID)

	got, body := s.neighboursOf(t, e.mine, anchor.ID)

	if !idSet(got.Mentions)[mentioned.ID.String()] || len(got.Mentions) != 1 {
		t.Errorf("mentions = %v, want only the artifact this one names: %s", got.Mentions, body)
	}
	mentionedBy := idSet(got.MentionedBy)
	if !mentionedBy[mentioner.ID.String()] || !mentionedBy[teller.ID.String()] {
		t.Errorf("mentioned_by is missing an entry: %s", body)
	}
	if !idSet(got.Related)[peer.ID.String()] || len(got.Related) != 1 {
		t.Errorf("related = %v: %s", got.Related, body)
	}
	if !idSet(got.Decisions)[decision.ID.String()] || len(got.Decisions) != 1 {
		t.Errorf("decisions = %v: %s", got.Decisions, body)
	}

	// The mixed groups say which read to follow.
	for _, entry := range got.MentionedBy {
		typ, _ := entry["type"].(string)
		if typ != string(domain.EntityArtifact) && typ != string(domain.EntityMemory) {
			t.Errorf("a mixed entry has type %q: %s", typ, body)
		}
		if label, _ := entry["label"].(string); strings.TrimSpace(label) == "" {
			t.Errorf("a mixed entry has no label: %v", entry)
		}
	}
}

// TestAnArtifactWithNoRelationsAnswersSixEmptyArrays keeps "nothing is
// connected" distinguishable from "this server does not report that".
func TestAnArtifactWithNoRelationsAnswersSixEmptyArrays(t *testing.T) {
	e := newEnv(t)
	s := e.mount()

	room := e.room(e.mine, "Ateliê", domain.SensitivityNormal)
	lonely := e.artifact(e.mine, domain.ArtifactNote, "Sozinho", domain.SensitivityNormal, &room.ID)

	res := s.as(e.mine, "/palace/artifacts/"+lonely.ID.String()+"/neighbors").
		requireStatus(t, http.StatusOK, "no relations")
	for _, key := range []string{
		"supersedes", "superseded_by", "mentions", "mentioned_by", "related", "decisions",
	} {
		if !strings.Contains(res.body, `"`+key+`":[]`) {
			t.Errorf("key %q is missing or null: %s", key, res.body)
		}
	}
	if strings.Contains(res.body, "null") {
		t.Errorf("a group encoded as null: %s", res.body)
	}
}

/* ══════════════════════════════════════════════════════════════════════
   F · The Core is untouched
   ══════════════════════════════════════════════════════════════════════ */

// TestTheBatchResolveIsUnrestrictedWithoutTheSurfaceRules is invariant I-F
// at the new port: `Visibility{}` behaves like the Core, so nothing an
// existing caller does changes.
func TestTheBatchResolveIsUnrestrictedWithoutTheSurfaceRules(t *testing.T) {
	e := newEnv(t)

	withheldRoom := e.room(e.mine, "Terapia", domain.SensitivityHighlySensitive)
	contained := e.artifact(e.mine, domain.ArtifactNote, "Nota contida",
		domain.SensitivityNormal, &withheldRoom.ID)

	withRules, err := e.repos.Artifacts.ListByIDs(e.ctx(), e.mine,
		[]uuid.UUID{contained.ID}, ports.Visibility{InheritRoomVisibility: true})
	if err != nil {
		t.Fatalf("resolve with rules: %v", err)
	}
	if len(withRules) != 0 {
		t.Error("the batch resolve returned a contained artifact under surface rules")
	}

	withoutRules, err := e.repos.Artifacts.ListByIDs(e.ctx(), e.mine,
		[]uuid.UUID{contained.ID}, ports.Visibility{})
	if err != nil {
		t.Fatalf("resolve without rules: %v", err)
	}
	if len(withoutRules) != 1 {
		t.Error("the zero-value Visibility is not the Core's posture; an existing " +
			"caller would silently lose access")
	}

	// And an empty input is not a query at all.
	none, err := e.repos.Artifacts.ListByIDs(e.ctx(), e.mine, nil, ports.Visibility{})
	if err != nil || len(none) != 0 {
		t.Errorf("an empty id set returned %v, %v", none, err)
	}
}
