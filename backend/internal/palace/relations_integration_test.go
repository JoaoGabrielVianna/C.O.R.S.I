//go:build integration

// Palace, slice S4: relations.
//
// The harness lives in palace_integration_test.go; this file is the same
// package and uses it.
package palace

import (
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/palace/app"
	"github.com/corsi/backend/internal/palace/domain"
	"github.com/corsi/backend/internal/palace/ports"
)

/* ── seed helpers ────────────────────────────────────────────────────── */

// endpoints builds live entities in a workspace, so a test can pick
// whichever end a shape needs.
//
// ── Why there are TWO of each type ─────────────────────────────────────
// Four of the ten permitted shapes join two entities of the SAME type:
// room→room, artifact→artifact (twice) and memory→memory. With one of
// each, those cases would put the same id on both ends and be refused as
// reflexive, which is a different rule and would make the matrix walk
// silently prove less than it claims.
type endpoints struct {
	room, room2         uuid.UUID
	artifact, artifact2 uuid.UUID
	memory, memory2     uuid.UUID
	// decision is a memory of kind `decision`, which is the only thing a
	// decision_for may start at.
	decision uuid.UUID
}

func (e *env) endpoints(ws uuid.UUID, label string) endpoints {
	e.t.Helper()
	mem := func(suffix string) uuid.UUID {
		return e.memory(ws, "memória "+label+suffix, domain.SensitivityNormal, nil).ID
	}
	decision, err := e.svc.CreateMemory(e.ctx(), ws, app.CreateMemoryInput{
		Kind: domain.MemoryDecision, Content: "decisão " + label,
	})
	if err != nil {
		e.t.Fatalf("seed decision: %v", err)
	}
	return endpoints{
		room:      e.room(ws, "sala "+label, domain.SensitivityNormal).ID,
		room2:     e.room(ws, "sala "+label+" 2", domain.SensitivityNormal).ID,
		artifact:  e.artifact(ws, domain.ArtifactProject, "artefato "+label, domain.SensitivityNormal, nil).ID,
		artifact2: e.artifact(ws, domain.ArtifactProject, "artefato "+label+" 2", domain.SensitivityNormal, nil).ID,
		memory:    mem(""),
		memory2:   mem(" 2"),
		decision:  decision.ID,
	}
}

// pick returns the id of an entity of type t.
//
// slot selects which of the two, so a same-type shape gets two distinct
// live entities rather than the same one twice. asDecisionOrigin asks for
// the memory whose kind is `decision`, which decision_for requires.
func (p endpoints) pick(t domain.EntityType, slot int, asDecisionOrigin bool) uuid.UUID {
	switch t {
	case domain.EntityRoom:
		if slot == 1 {
			return p.room2
		}
		return p.room
	case domain.EntityArtifact:
		if slot == 1 {
			return p.artifact2
		}
		return p.artifact
	case domain.EntityMemory:
		if asDecisionOrigin {
			return p.decision
		}
		if slot == 1 {
			return p.memory2
		}
		return p.memory
	}
	return uuid.Nil
}

func (e *env) relate(ws uuid.UUID, from domain.EntityType, fromID uuid.UUID, kind domain.RelationKind, to domain.EntityType, toID uuid.UUID) *domain.Relation {
	e.t.Helper()
	res, err := e.svc.CreateRelation(e.ctx(), ws, app.CreateRelationInput{
		FromType: from, FromID: fromID, Kind: kind, ToType: to, ToID: toID,
	})
	if err != nil {
		e.t.Fatalf("seed relation: %v", err)
	}
	return res.Relation
}

/* ══════════════════════════════════════════════════════════════════════
   The matrix, end to end through the service
   ══════════════════════════════════════════════════════════════════════ */

func TestEveryPermittedShapeCreatesAndEveryOtherIsRefused(t *testing.T) {
	// The domain matrix and the SQL CHECK already agree (see
	// TestTheDomainMatrixAndTheDatabaseCheckAcceptExactlyTheSameShapes).
	// This walks the same 36 combinations through the whole stack, with
	// real endpoints that exist and are live, so a shape that the matrix
	// permits but the service cannot actually write shows up here.
	e := newEnv(t)
	ends := e.endpoints(e.mine, "A")

	created := 0
	for _, kind := range domain.RelationKinds {
		for _, from := range domain.EntityTypes {
			for _, to := range domain.EntityTypes {
				want := domain.ShapeAllowed(kind, from, to)
				name := fmt.Sprintf("%s:%s→%s", kind, from, to)

				// Slot 0 for the origin, slot 1 for the target, so a
				// same-type shape gets two distinct live entities and is
				// judged by the matrix rather than by the reflexive rule.
				isDecisionOrigin := kind == domain.RelationDecisionFor
				fromID := ends.pick(from, 0, isDecisionOrigin)
				toID := ends.pick(to, 1, false)
				if fromID == toID {
					t.Fatalf("%s: the fixture put the same entity on both ends", name)
				}

				res, err := e.svc.CreateRelation(e.ctx(), e.mine, app.CreateRelationInput{
					FromType: from, FromID: fromID, Kind: kind, ToType: to, ToID: toID,
				})

				if want {
					if err != nil {
						t.Errorf("%s was refused: %v", name, err)
						continue
					}
					if !res.Created {
						t.Errorf("%s reported that it already existed", name)
					}
					if res.Relation.ID == uuid.Nil {
						t.Errorf("%s came back without an id", name)
					}
					created++
					continue
				}
				if err == nil {
					t.Errorf("%s was accepted", name)
				}
			}
		}
	}
	if created != 10 {
		t.Fatalf("created %d relations, want the 10 shapes the matrix permits", created)
	}
}

/* ══════════════════════════════════════════════════════════════════════
   Both endpoints are resolved in the workspace
   ══════════════════════════════════════════════════════════════════════ */

func TestAnEndpointFromAnotherWorkspaceIsIndistinguishableFromAFabricatedOne(t *testing.T) {
	// ── Why this matters more here than anywhere else ──────────────────
	// `palace.relations` carries no foreign key: the endpoints are
	// polymorphic and Postgres cannot reference one of three tables.
	// Every other table in this context has a composite key standing
	// behind the service; this one has nothing. Whatever the service
	// fails to check is not checked at all.
	e := newEnv(t)
	mine := e.endpoints(e.mine, "minha")
	theirs := e.endpoints(e.theirs, "deles")
	fabricated := uuid.New()

	// Their memory as the origin.
	_, errTheirOrigin := e.svc.CreateRelation(e.ctx(), e.mine, app.CreateRelationInput{
		FromType: domain.EntityMemory, FromID: theirs.memory,
		Kind:   domain.RelationRelatedTo,
		ToType: domain.EntityMemory, ToID: mine.memory,
	})
	_, errFakeOrigin := e.svc.CreateRelation(e.ctx(), e.mine, app.CreateRelationInput{
		FromType: domain.EntityMemory, FromID: fabricated,
		Kind:   domain.RelationRelatedTo,
		ToType: domain.EntityMemory, ToID: mine.memory,
	})
	assertNotFound(t, "a neighbour's entity as the origin", errTheirOrigin)
	assertNotFound(t, "a fabricated origin", errFakeOrigin)
	assertSameAnswer(t, "origin", errTheirOrigin, errFakeOrigin, theirs.memory, fabricated)

	// Their memory as the target.
	_, errTheirTarget := e.svc.CreateRelation(e.ctx(), e.mine, app.CreateRelationInput{
		FromType: domain.EntityMemory, FromID: mine.memory,
		Kind:   domain.RelationRelatedTo,
		ToType: domain.EntityMemory, ToID: theirs.memory,
	})
	_, errFakeTarget := e.svc.CreateRelation(e.ctx(), e.mine, app.CreateRelationInput{
		FromType: domain.EntityMemory, FromID: mine.memory,
		Kind:   domain.RelationRelatedTo,
		ToType: domain.EntityMemory, ToID: fabricated,
	})
	assertNotFound(t, "a neighbour's entity as the target", errTheirTarget)
	assertNotFound(t, "a fabricated target", errFakeTarget)
	assertSameAnswer(t, "target", errTheirTarget, errFakeTarget, theirs.memory, fabricated)

	// And nothing was written, in either workspace.
	for _, ws := range []uuid.UUID{e.mine, e.theirs} {
		_, total, err := e.svc.ListRelations(e.ctx(), ws, ports.RelationFilter{})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if total != 0 {
			t.Errorf("a cross-workspace relation was written: %d rows", total)
		}
	}
}

func TestBothEndsAreResolvedAndNotJustTheFirst(t *testing.T) {
	// The failure this guards: a service that checks the origin, finds it
	// fine, and writes without ever asking about the target.
	e := newEnv(t)
	mine := e.endpoints(e.mine, "minha")

	_, err := e.svc.CreateRelation(e.ctx(), e.mine, app.CreateRelationInput{
		FromType: domain.EntityMemory, FromID: mine.memory,
		Kind:   domain.RelationRelatedTo,
		ToType: domain.EntityArtifact, ToID: uuid.New(),
	})
	assertNotFound(t, "a valid origin with a fabricated target", err)
}

func TestANeighboursRelationCannotBeReadOrRemoved(t *testing.T) {
	e := newEnv(t)
	theirs := e.endpoints(e.theirs, "deles")
	rel := e.relate(e.theirs, domain.EntityMemory, theirs.memory,
		domain.RelationRelatedTo, domain.EntityArtifact, theirs.artifact)

	_, err := e.svc.GetRelation(e.ctx(), e.mine, rel.ID)
	assertNotFound(t, "reading a neighbour's relation", err)

	assertNotFound(t, "removing a neighbour's relation",
		e.svc.RemoveRelation(e.ctx(), e.mine, rel.ID))

	// And it is still there, in the workspace that owns it.
	if _, err := e.svc.GetRelation(e.ctx(), e.theirs, rel.ID); err != nil {
		t.Errorf("the neighbour's relation was removed by a stranger: %v", err)
	}
}

func TestANeighboursRelationsAreAbsentFromListings(t *testing.T) {
	e := newEnv(t)
	theirs := e.endpoints(e.theirs, "deles")
	e.relate(e.theirs, domain.EntityMemory, theirs.memory,
		domain.RelationRelatedTo, domain.EntityArtifact, theirs.artifact)

	got, total, err := e.svc.ListRelations(e.ctx(), e.mine, ports.RelationFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 0 || total != 0 {
		t.Errorf("a neighbour's relations appeared: %d rows, total %d", len(got), total)
	}
}

/* ══════════════════════════════════════════════════════════════════════
   Lifecycle: archived endpoints refuse NEW edges, and keep old ones
   ══════════════════════════════════════════════════════════════════════ */

func TestAnArchivedEndpointRefusesANewRelation(t *testing.T) {
	e := newEnv(t)
	ends := e.endpoints(e.mine, "A")

	archived := domain.LifecycleArchived
	if _, err := e.svc.UpdateArtifact(e.ctx(), e.mine, ends.artifact,
		domain.ArtifactChange{Status: &archived}); err != nil {
		t.Fatalf("archive: %v", err)
	}

	// As the target.
	_, err := e.svc.CreateRelation(e.ctx(), e.mine, app.CreateRelationInput{
		FromType: domain.EntityMemory, FromID: ends.memory,
		Kind:   domain.RelationRelatedTo,
		ToType: domain.EntityArtifact, ToID: ends.artifact,
	})
	assertInvalid(t, "a new relation to an archived artifact", err)

	// As the origin.
	other := e.artifact(e.mine, domain.ArtifactNote, "outro", domain.SensitivityNormal, nil)
	_, err = e.svc.CreateRelation(e.ctx(), e.mine, app.CreateRelationInput{
		FromType: domain.EntityArtifact, FromID: ends.artifact,
		Kind:   domain.RelationMentions,
		ToType: domain.EntityArtifact, ToID: other.ID,
	})
	assertInvalid(t, "a new relation from an archived artifact", err)
}

func TestAnArchivedMemoryOrRoomAlsoRefusesANewRelation(t *testing.T) {
	e := newEnv(t)
	ends := e.endpoints(e.mine, "A")
	archived := domain.LifecycleArchived

	if _, err := e.svc.UpdateMemory(e.ctx(), e.mine, ends.memory,
		domain.MemoryChange{Status: &archived}); err != nil {
		t.Fatalf("archive memory: %v", err)
	}
	if _, err := e.svc.UpdateRoom(e.ctx(), e.mine, ends.room,
		domain.RoomChange{Status: &archived}); err != nil {
		t.Fatalf("archive room: %v", err)
	}

	_, err := e.svc.CreateRelation(e.ctx(), e.mine, app.CreateRelationInput{
		FromType: domain.EntityMemory, FromID: ends.memory,
		Kind:   domain.RelationRelatedTo,
		ToType: domain.EntityArtifact, ToID: ends.artifact,
	})
	assertInvalid(t, "a new relation from an archived memory", err)

	_, err = e.svc.CreateRelation(e.ctx(), e.mine, app.CreateRelationInput{
		FromType: domain.EntityMemory, FromID: ends.decision,
		Kind:   domain.RelationDecisionFor,
		ToType: domain.EntityRoom, ToID: ends.room,
	})
	assertInvalid(t, "a new relation to an archived room", err)
}

// ══════════════════════════════════════════════════════════════════════
//
//	ARCHIVING DOES NOT TOUCH THE EDGES THAT ALREADY EXIST
//
// ══════════════════════════════════════════════════════════════════════
//
// The history of what was connected is not invalidated by somebody
// tidying up, and a cascade would silently destroy the record that often
// explains why the thing was archived at all.
func TestExistingRelationsSurviveArchivingAnEndpoint(t *testing.T) {
	e := newEnv(t)
	ends := e.endpoints(e.mine, "A")
	rel := e.relate(e.mine, domain.EntityMemory, ends.memory,
		domain.RelationRelatedTo, domain.EntityArtifact, ends.artifact)

	archived := domain.LifecycleArchived
	if _, err := e.svc.UpdateArtifact(e.ctx(), e.mine, ends.artifact,
		domain.ArtifactChange{Status: &archived}); err != nil {
		t.Fatalf("archive: %v", err)
	}

	// Still readable by id.
	if _, err := e.svc.GetRelation(e.ctx(), e.mine, rel.ID); err != nil {
		t.Errorf("archiving an endpoint made the relation unreadable: %v", err)
	}

	// Still listed, from either end.
	for _, anchor := range []ports.RelationAnchor{
		{Type: domain.EntityMemory, ID: ends.memory},
		{Type: domain.EntityArtifact, ID: ends.artifact},
	} {
		a := anchor
		got, total, err := e.svc.ListRelations(e.ctx(), e.mine, ports.RelationFilter{Anchor: &a})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(got) != 1 || total != 1 {
			t.Errorf("anchored at %s: %d rows, total %d, want 1", a.Type, len(got), total)
		}
	}
}

func TestArchivingAnEndpointRemovesNoEdges(t *testing.T) {
	// Stated against the table itself, so a cascade added in SQL later
	// fails here rather than being discovered when a record goes missing.
	e := newEnv(t)
	ends := e.endpoints(e.mine, "A")
	e.relate(e.mine, domain.EntityMemory, ends.memory,
		domain.RelationRelatedTo, domain.EntityArtifact, ends.artifact)

	var before int
	if err := e.pool.QueryRow(e.ctx(),
		`SELECT count(*) FROM palace.relations WHERE workspace_id = $1`, e.mine).Scan(&before); err != nil {
		t.Fatalf("count: %v", err)
	}

	archived := domain.LifecycleArchived
	for _, archive := range []func() error{
		func() error {
			_, err := e.svc.UpdateArtifact(e.ctx(), e.mine, ends.artifact,
				domain.ArtifactChange{Status: &archived})
			return err
		},
		func() error {
			_, err := e.svc.UpdateMemory(e.ctx(), e.mine, ends.memory,
				domain.MemoryChange{Status: &archived})
			return err
		},
	} {
		if err := archive(); err != nil {
			t.Fatalf("archive: %v", err)
		}
	}

	var after int
	if err := e.pool.QueryRow(e.ctx(),
		`SELECT count(*) FROM palace.relations WHERE workspace_id = $1`, e.mine).Scan(&after); err != nil {
		t.Fatalf("count: %v", err)
	}
	if after != before {
		t.Errorf("archiving cascaded: %d edges before, %d after", before, after)
	}
}

/* ══════════════════════════════════════════════════════════════════════
   decision_for, now executable
   ══════════════════════════════════════════════════════════════════════ */

func TestDecisionForRefusesAMemoryThatIsNotADecision(t *testing.T) {
	e := newEnv(t)
	ends := e.endpoints(e.mine, "A")

	for _, kind := range domain.MemoryKinds {
		if kind == domain.MemoryDecision {
			continue
		}
		m, err := e.svc.CreateMemory(e.ctx(), e.mine, app.CreateMemoryInput{
			Kind: kind, Content: "uma memória de tipo " + string(kind),
		})
		if err != nil {
			t.Fatalf("seed: %v", err)
		}

		_, err = e.svc.CreateRelation(e.ctx(), e.mine, app.CreateRelationInput{
			FromType: domain.EntityMemory, FromID: m.ID,
			Kind:   domain.RelationDecisionFor,
			ToType: domain.EntityArtifact, ToID: ends.artifact,
		})
		assertInvalid(t, "decision_for from a "+string(kind), err)
	}

	// And the decision itself works.
	if _, err := e.svc.CreateRelation(e.ctx(), e.mine, app.CreateRelationInput{
		FromType: domain.EntityMemory, FromID: ends.decision,
		Kind:   domain.RelationDecisionFor,
		ToType: domain.EntityArtifact, ToID: ends.artifact,
	}); err != nil {
		t.Fatalf("decision_for from a decision was refused: %v", err)
	}
}

// ══════════════════════════════════════════════════════════════════════
//
//	A MEMORY THAT GOVERNS SOMETHING CANNOT STOP BEING A DECISION
//
// ══════════════════════════════════════════════════════════════════════
//
// The normative rule, executable now that Relation has a repository. The
// fix is never a cascade: the operator asked to change one thing, and a
// system that also destroyed a link they did not mention would be
// deciding on their behalf about the record of why something was done.
func TestAMemoryWithALiveDecisionForCannotChangeKind(t *testing.T) {
	e := newEnv(t)
	ends := e.endpoints(e.mine, "A")
	e.relate(e.mine, domain.EntityMemory, ends.decision,
		domain.RelationDecisionFor, domain.EntityArtifact, ends.artifact)

	for _, kind := range domain.MemoryKinds {
		if kind == domain.MemoryDecision {
			continue
		}
		k := kind
		_, err := e.svc.UpdateMemory(e.ctx(), e.mine, ends.decision,
			domain.MemoryChange{Kind: &k})
		assertInvalid(t, "reclassifying to "+string(kind), err)
	}

	// Still a decision, and still governing.
	after, err := e.svc.GetMemory(e.ctx(), e.mine, ends.decision)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if after.Kind != domain.MemoryDecision {
		t.Errorf("the memory was reclassified to %q anyway", after.Kind)
	}
}

func TestTheRefusalDoesNotCascadeTheRelation(t *testing.T) {
	e := newEnv(t)
	ends := e.endpoints(e.mine, "A")
	rel := e.relate(e.mine, domain.EntityMemory, ends.decision,
		domain.RelationDecisionFor, domain.EntityArtifact, ends.artifact)

	fact := domain.MemoryFact
	if _, err := e.svc.UpdateMemory(e.ctx(), e.mine, ends.decision,
		domain.MemoryChange{Kind: &fact}); err == nil {
		t.Fatal("want a refusal")
	}

	// The relation is untouched: not removed, not converted.
	got, err := e.svc.GetRelation(e.ctx(), e.mine, rel.ID)
	if err != nil {
		t.Fatalf("the refused edit removed the relation: %v", err)
	}
	if got.Kind != domain.RelationDecisionFor {
		t.Errorf("the relation was converted to %q", got.Kind)
	}
}

func TestRemovingTheRelationIsWhatUnlocksTheReclassification(t *testing.T) {
	// The documented flow, end to end:
	//
	//	palace.relation.remove  →  memory.update(kind = …)
	e := newEnv(t)
	ends := e.endpoints(e.mine, "A")
	rel := e.relate(e.mine, domain.EntityMemory, ends.decision,
		domain.RelationDecisionFor, domain.EntityArtifact, ends.artifact)

	fact := domain.MemoryFact
	if _, err := e.svc.UpdateMemory(e.ctx(), e.mine, ends.decision,
		domain.MemoryChange{Kind: &fact}); err == nil {
		t.Fatal("want a refusal while the relation stands")
	}

	if err := e.svc.RemoveRelation(e.ctx(), e.mine, rel.ID); err != nil {
		t.Fatalf("remove: %v", err)
	}

	res, err := e.svc.UpdateMemory(e.ctx(), e.mine, ends.decision,
		domain.MemoryChange{Kind: &fact})
	if err != nil {
		t.Fatalf("after removing the relation the reclassification was still refused: %v", err)
	}
	if !res.KindChanged || res.Memory.Kind != domain.MemoryFact {
		t.Errorf("the reclassification did not apply: %+v", res.MemoryChangeResult)
	}
	if res.PreviousKind != domain.MemoryDecision {
		t.Errorf("PreviousKind = %q, want %q", res.PreviousKind, domain.MemoryDecision)
	}
}

func TestAMemoryWithoutADecisionForChangesKindFreely(t *testing.T) {
	// The guard costs one narrow read and only when the kind is moving
	// away from `decision`. It must not refuse an ordinary edit.
	e := newEnv(t)
	ends := e.endpoints(e.mine, "A")

	fact := domain.MemoryFact
	if _, err := e.svc.UpdateMemory(e.ctx(), e.mine, ends.decision,
		domain.MemoryChange{Kind: &fact}); err != nil {
		t.Fatalf("an uncommitted decision could not be reclassified: %v", err)
	}
}

func TestADecisionThatGovernsSomethingCanStillBeEditedOtherwise(t *testing.T) {
	// The rule freezes the KIND, not the memory. Content, importance and
	// sensitivity all still move.
	e := newEnv(t)
	ends := e.endpoints(e.mine, "A")
	e.relate(e.mine, domain.EntityMemory, ends.decision,
		domain.RelationDecisionFor, domain.EntityArtifact, ends.artifact)

	content := "decisão revisada"
	importance := 5
	decision := domain.MemoryDecision
	res, err := e.svc.UpdateMemory(e.ctx(), e.mine, ends.decision, domain.MemoryChange{
		Content: &content, Importance: &importance, Kind: &decision,
	})
	if err != nil {
		t.Fatalf("an ordinary edit of a governing decision was refused: %v", err)
	}
	if !res.ContentChanged || !res.ImportanceChanged {
		t.Errorf("the edit did not apply: %+v", res.MemoryChangeResult)
	}
	if res.KindChanged {
		t.Error("resending `decision` was reported as a kind change")
	}
}

/* ══════════════════════════════════════════════════════════════════════
   SUPERSEDES: the frozen direction, through the stack
   ══════════════════════════════════════════════════════════════════════ */

func TestNewSupersedesOldSurvivesTheRoundTrip(t *testing.T) {
	// Memory B "Backend será Go" SUPERSEDES Memory A "Talvez usar Python".
	//
	// Both ends are memories, so nothing in the row reveals which way it
	// was meant: a reader that guesses backwards gets a coherent,
	// confident, inverted history with no error anywhere.
	e := newEnv(t)

	maybePython, err := e.svc.CreateMemory(e.ctx(), e.mine, app.CreateMemoryInput{
		Kind: domain.MemoryIdea, Content: "Talvez usar Python",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	backendGo, err := e.svc.CreateMemory(e.ctx(), e.mine, app.CreateMemoryInput{
		Kind: domain.MemoryDecision, Content: "Backend será Go",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	rel := e.relate(e.mine, domain.EntityMemory, backendGo.ID,
		domain.RelationSupersedes, domain.EntityMemory, maybePython.ID)

	loaded, err := e.svc.GetRelation(e.ctx(), e.mine, rel.ID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	newer, older, ok := loaded.Superseding()
	if !ok {
		t.Fatal("the loaded relation did not report its two ends")
	}
	if newer != backendGo.ID {
		t.Error("after a round trip through Postgres, the FROM end is not the newer one")
	}
	if older != maybePython.ID {
		t.Error("after a round trip through Postgres, the TO end is not the older one")
	}

	// And the listing tells the two directions apart, which is how a
	// reader answers "what did this replace" versus "what replaced this".
	from := ports.RelationAnchor{Type: domain.EntityMemory, ID: backendGo.ID}
	replaced, _, err := e.svc.ListRelations(e.ctx(), e.mine, ports.RelationFilter{
		Anchor: &from, Direction: ports.DirectionFrom,
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(replaced) != 1 || replaced[0].ToID != maybePython.ID {
		t.Error("anchored FROM the new decision, the listing does not show what it replaced")
	}

	to := ports.RelationAnchor{Type: domain.EntityMemory, ID: backendGo.ID}
	replacedBy, _, err := e.svc.ListRelations(e.ctx(), e.mine, ports.RelationFilter{
		Anchor: &to, Direction: ports.DirectionTo,
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(replacedBy) != 0 {
		t.Error("the new decision was reported as having been replaced by something")
	}
}

func TestSupersedesDoesNotArchiveTheOlderEnd(t *testing.T) {
	// The relation records semantics; lifecycle stays an explicit act.
	// Superseded work is routinely kept active on purpose, because the
	// rejected option is part of why the decision was made.
	e := newEnv(t)
	older := e.memory(e.mine, "Talvez usar Python", domain.SensitivityNormal, nil)
	newer := e.memory(e.mine, "Backend será Go", domain.SensitivityNormal, nil)

	e.relate(e.mine, domain.EntityMemory, newer.ID,
		domain.RelationSupersedes, domain.EntityMemory, older.ID)

	got, err := e.svc.GetMemory(e.ctx(), e.mine, older.ID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got.Status != domain.LifecycleActive {
		t.Errorf("the superseded memory was archived automatically: %q", got.Status)
	}
}

func TestASelfRelationIsStillRefused(t *testing.T) {
	e := newEnv(t)
	ends := e.endpoints(e.mine, "A")

	_, err := e.svc.CreateRelation(e.ctx(), e.mine, app.CreateRelationInput{
		FromType: domain.EntityMemory, FromID: ends.memory,
		Kind:   domain.RelationSupersedes,
		ToType: domain.EntityMemory, ToID: ends.memory,
	})
	assertInvalid(t, "a relation pointing at itself", err)
}

/* ══════════════════════════════════════════════════════════════════════
   Duplicates, removal, and what removal does not touch
   ══════════════════════════════════════════════════════════════════════ */

func TestCreatingTheSameRelationTwiceIsDeterministic(t *testing.T) {
	e := newEnv(t)
	ends := e.endpoints(e.mine, "A")

	in := app.CreateRelationInput{
		FromType: domain.EntityMemory, FromID: ends.memory,
		Kind:   domain.RelationRelatedTo,
		ToType: domain.EntityArtifact, ToID: ends.artifact,
	}

	first, err := e.svc.CreateRelation(e.ctx(), e.mine, in)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := e.svc.CreateRelation(e.ctx(), e.mine, in)
	if err != nil {
		t.Fatalf("second: %v", err)
	}

	if !first.Created {
		t.Error("the first create reported that it already existed")
	}
	if second.Created {
		t.Error("the second create reported that it created something")
	}
	// The same edge, with the same id, so a caller that meant to remove
	// it still can.
	if second.Relation.ID != first.Relation.ID {
		t.Errorf("the second create returned a different id: %s vs %s",
			second.Relation.ID, first.Relation.ID)
	}

	var rows int
	if err := e.pool.QueryRow(e.ctx(),
		`SELECT count(*) FROM palace.relations WHERE workspace_id = $1`, e.mine).Scan(&rows); err != nil {
		t.Fatalf("count: %v", err)
	}
	if rows != 1 {
		t.Errorf("the table holds %d edges after creating the same one twice, want 1", rows)
	}
}

func TestRemovingARelationTwiceIsNotFound(t *testing.T) {
	e := newEnv(t)
	ends := e.endpoints(e.mine, "A")
	rel := e.relate(e.mine, domain.EntityMemory, ends.memory,
		domain.RelationRelatedTo, domain.EntityArtifact, ends.artifact)

	if err := e.svc.RemoveRelation(e.ctx(), e.mine, rel.ID); err != nil {
		t.Fatalf("remove: %v", err)
	}
	assertNotFound(t, "removing twice", e.svc.RemoveRelation(e.ctx(), e.mine, rel.ID))
	assertNotFound(t, "reading a removed relation",
		func() error { _, err := e.svc.GetRelation(e.ctx(), e.mine, rel.ID); return err }())
}

func TestRemovingARelationIsPhysicalAndLeavesNoRow(t *testing.T) {
	// A relation is an assertion, not content. Archiving exists so
	// somebody can say "not this, for now" about something they made; an
	// edge that should not have been filed is not a thing to retire, and
	// a tombstone would mean every reader having to know which edges are
	// real.
	e := newEnv(t)
	ends := e.endpoints(e.mine, "A")
	rel := e.relate(e.mine, domain.EntityMemory, ends.memory,
		domain.RelationRelatedTo, domain.EntityArtifact, ends.artifact)

	if err := e.svc.RemoveRelation(e.ctx(), e.mine, rel.ID); err != nil {
		t.Fatalf("remove: %v", err)
	}

	var rows int
	if err := e.pool.QueryRow(e.ctx(),
		`SELECT count(*) FROM palace.relations WHERE id = $1`, rel.ID).Scan(&rows); err != nil {
		t.Fatalf("count: %v", err)
	}
	if rows != 0 {
		t.Errorf("the row survived the removal: %d rows", rows)
	}
}

// ══════════════════════════════════════════════════════════════════════
//
//	REMOVING AN EDGE REMOVES THE EDGE
//
// ══════════════════════════════════════════════════════════════════════
func TestRemovingARelationTouchesNeitherEntity(t *testing.T) {
	e := newEnv(t)
	ends := e.endpoints(e.mine, "A")
	rel := e.relate(e.mine, domain.EntityMemory, ends.decision,
		domain.RelationDecisionFor, domain.EntityArtifact, ends.artifact)

	memoryBefore, err := e.svc.GetMemory(e.ctx(), e.mine, ends.decision)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	artifactBefore, err := e.svc.GetArtifact(e.ctx(), e.mine, ends.artifact)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	if err := e.svc.RemoveRelation(e.ctx(), e.mine, rel.ID); err != nil {
		t.Fatalf("remove: %v", err)
	}

	memoryAfter, err := e.svc.GetMemory(e.ctx(), e.mine, ends.decision)
	if err != nil {
		t.Fatalf("the memory disappeared with the edge: %v", err)
	}
	artifactAfter, err := e.svc.GetArtifact(e.ctx(), e.mine, ends.artifact)
	if err != nil {
		t.Fatalf("the artifact disappeared with the edge: %v", err)
	}

	if memoryAfter.Kind != memoryBefore.Kind ||
		memoryAfter.Status != memoryBefore.Status ||
		memoryAfter.Sensitivity != memoryBefore.Sensitivity ||
		memoryAfter.Content != memoryBefore.Content ||
		!memoryAfter.UpdatedAt.Equal(memoryBefore.UpdatedAt) {
		t.Error("removing the edge changed the memory")
	}
	if artifactAfter.Status != artifactBefore.Status ||
		artifactAfter.Title != artifactBefore.Title ||
		!artifactAfter.UpdatedAt.Equal(artifactBefore.UpdatedAt) {
		t.Error("removing the edge changed the artifact")
	}
}

/* ══════════════════════════════════════════════════════════════════════
   Listing: adjacency, never traversal
   ══════════════════════════════════════════════════════════════════════ */

// ══════════════════════════════════════════════════════════════════════
//
//	A LISTING RETURNS THE EDGES THAT TOUCH THE ANCHOR. NOTHING FURTHER
//
// ══════════════════════════════════════════════════════════════════════
//
// A → B and B → C. Anchored at A, the answer is one edge. If it is two,
// the read has started walking the graph, which is unbounded in cost and
// returns entities nobody asked about through a chain nobody reviewed.
func TestAListingIsAdjacencyAndNotTraversal(t *testing.T) {
	e := newEnv(t)
	a := e.artifact(e.mine, domain.ArtifactNote, "A", domain.SensitivityNormal, nil)
	b := e.artifact(e.mine, domain.ArtifactNote, "B", domain.SensitivityNormal, nil)
	c := e.artifact(e.mine, domain.ArtifactNote, "C", domain.SensitivityNormal, nil)

	e.relate(e.mine, domain.EntityArtifact, a.ID, domain.RelationMentions, domain.EntityArtifact, b.ID)
	e.relate(e.mine, domain.EntityArtifact, b.ID, domain.RelationMentions, domain.EntityArtifact, c.ID)

	anchor := ports.RelationAnchor{Type: domain.EntityArtifact, ID: a.ID}
	got, total, err := e.svc.ListRelations(e.ctx(), e.mine, ports.RelationFilter{Anchor: &anchor})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 || total != 1 {
		t.Fatalf("anchored at A: %d rows, total %d, want 1; the listing walked the graph",
			len(got), total)
	}
	if got[0].ToID != b.ID {
		t.Errorf("anchored at A the edge does not point at B: %s", got[0].ToID)
	}

	// B touches both, which is the honest adjacency answer.
	anchorB := ports.RelationAnchor{Type: domain.EntityArtifact, ID: b.ID}
	got, total, err = e.svc.ListRelations(e.ctx(), e.mine, ports.RelationFilter{Anchor: &anchorB})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 || total != 2 {
		t.Errorf("anchored at B: %d rows, total %d, want 2", len(got), total)
	}
}

func TestAListingIsNarrowedByDirectionAndKind(t *testing.T) {
	e := newEnv(t)
	ends := e.endpoints(e.mine, "A")
	other := e.artifact(e.mine, domain.ArtifactNote, "outro", domain.SensitivityNormal, nil)

	e.relate(e.mine, domain.EntityMemory, ends.memory,
		domain.RelationRelatedTo, domain.EntityArtifact, ends.artifact)
	e.relate(e.mine, domain.EntityMemory, ends.decision,
		domain.RelationDecisionFor, domain.EntityArtifact, ends.artifact)
	e.relate(e.mine, domain.EntityArtifact, other.ID,
		domain.RelationMentions, domain.EntityArtifact, ends.artifact)

	anchor := ports.RelationAnchor{Type: domain.EntityArtifact, ID: ends.artifact}

	// Every edge touching the artifact: three, all pointing at it.
	_, total, err := e.svc.ListRelations(e.ctx(), e.mine, ports.RelationFilter{Anchor: &anchor})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 3 {
		t.Errorf("any direction: total %d, want 3", total)
	}

	// Only the ones pointing AT it: still three.
	_, total, err = e.svc.ListRelations(e.ctx(), e.mine, ports.RelationFilter{
		Anchor: &anchor, Direction: ports.DirectionTo,
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 3 {
		t.Errorf("direction=to: total %d, want 3", total)
	}

	// Only the ones starting AT it: none.
	_, total, err = e.svc.ListRelations(e.ctx(), e.mine, ports.RelationFilter{
		Anchor: &anchor, Direction: ports.DirectionFrom,
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 0 {
		t.Errorf("direction=from: total %d, want 0", total)
	}

	// Narrowed by kind.
	decisionFor := domain.RelationDecisionFor
	_, total, err = e.svc.ListRelations(e.ctx(), e.mine, ports.RelationFilter{
		Anchor: &anchor, Kind: &decisionFor,
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 1 {
		t.Errorf("kind=decision_for: total %d, want 1", total)
	}
}

func TestAnUnanchoredListingReturnsTheWorkspacesEdges(t *testing.T) {
	e := newEnv(t)
	ends := e.endpoints(e.mine, "A")
	e.relate(e.mine, domain.EntityMemory, ends.memory,
		domain.RelationRelatedTo, domain.EntityArtifact, ends.artifact)

	got, total, err := e.svc.ListRelations(e.ctx(), e.mine, ports.RelationFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 || total != 1 {
		t.Errorf("%d rows, total %d, want 1", len(got), total)
	}
}

func TestAFabricatedAnchorListsNothingRatherThanFailing(t *testing.T) {
	// Relations can legitimately outlive their endpoints: this table has
	// no foreign key and nothing cascades, so requiring the anchor to
	// resolve would make exactly those edges unreadable. An empty list
	// leaks nothing, because a neighbour's entity and one that never
	// existed produce the same answer.
	e := newEnv(t)
	anchor := ports.RelationAnchor{Type: domain.EntityMemory, ID: uuid.New()}

	got, total, err := e.svc.ListRelations(e.ctx(), e.mine, ports.RelationFilter{Anchor: &anchor})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 0 || total != 0 {
		t.Errorf("%d rows, total %d, want none", len(got), total)
	}
}

func TestAListingRefusesAnUnknownAnchorTypeOrDirection(t *testing.T) {
	e := newEnv(t)
	anchor := ports.RelationAnchor{Type: "source", ID: uuid.New()}

	_, _, err := e.svc.ListRelations(e.ctx(), e.mine, ports.RelationFilter{Anchor: &anchor})
	assertInvalid(t, "anchor of an unknown type", err)

	good := ports.RelationAnchor{Type: domain.EntityMemory, ID: uuid.New()}
	_, _, err = e.svc.ListRelations(e.ctx(), e.mine, ports.RelationFilter{
		Anchor: &good, Direction: "sideways",
	})
	assertInvalid(t, "unknown direction", err)
}

/* ══════════════════════════════════════════════════════════════════════
   Nothing new leaks
   ══════════════════════════════════════════════════════════════════════ */

func TestNoRelationFailurePathCarriesContent(t *testing.T) {
	e := newEnv(t)
	ends := e.endpoints(e.mine, "A")

	// Entities whose content is the canary, so every refusal below is
	// handed something worth leaking.
	secretMemory := e.memory(e.mine, canary, domain.SensitivityHighlySensitive, nil)
	secretArtifact := e.artifact(e.mine, domain.ArtifactNote, canary,
		domain.SensitivityHighlySensitive, nil)

	archived := domain.LifecycleArchived
	if _, err := e.svc.UpdateArtifact(e.ctx(), e.mine, secretArtifact.ID,
		domain.ArtifactChange{Status: &archived}); err != nil {
		t.Fatalf("archive: %v", err)
	}

	cases := map[string]func() error{
		"bad shape": func() error {
			_, err := e.svc.CreateRelation(e.ctx(), e.mine, app.CreateRelationInput{
				FromType: domain.EntityRoom, FromID: ends.room,
				Kind:   domain.RelationSupersedes,
				ToType: domain.EntityMemory, ToID: secretMemory.ID,
			})
			return err
		},
		"archived endpoint": func() error {
			_, err := e.svc.CreateRelation(e.ctx(), e.mine, app.CreateRelationInput{
				FromType: domain.EntityMemory, FromID: secretMemory.ID,
				Kind:   domain.RelationRelatedTo,
				ToType: domain.EntityArtifact, ToID: secretArtifact.ID,
			})
			return err
		},
		"not a decision": func() error {
			_, err := e.svc.CreateRelation(e.ctx(), e.mine, app.CreateRelationInput{
				FromType: domain.EntityMemory, FromID: secretMemory.ID,
				Kind:   domain.RelationDecisionFor,
				ToType: domain.EntityArtifact, ToID: ends.artifact,
			})
			return err
		},
		"fabricated endpoint": func() error {
			_, err := e.svc.CreateRelation(e.ctx(), e.mine, app.CreateRelationInput{
				FromType: domain.EntityMemory, FromID: uuid.New(),
				Kind:   domain.RelationRelatedTo,
				ToType: domain.EntityMemory, ToID: secretMemory.ID,
			})
			return err
		},
		"self relation": func() error {
			_, err := e.svc.CreateRelation(e.ctx(), e.mine, app.CreateRelationInput{
				FromType: domain.EntityMemory, FromID: secretMemory.ID,
				Kind:   domain.RelationSupersedes,
				ToType: domain.EntityMemory, ToID: secretMemory.ID,
			})
			return err
		},
		"removing a fabricated relation": func() error {
			return e.svc.RemoveRelation(e.ctx(), e.mine, uuid.New())
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

	// The reclassification refusal, which names a relation kind and a
	// memory kind and must name no sentence.
	e.relate(e.mine, domain.EntityMemory, ends.decision,
		domain.RelationDecisionFor, domain.EntityArtifact, ends.artifact)
	fact := domain.MemoryFact
	_, err := e.svc.UpdateMemory(e.ctx(), e.mine, ends.decision, domain.MemoryChange{Kind: &fact})
	if err == nil {
		t.Fatal("want a refusal")
	}
	if strings.Contains(err.Error(), canary) {
		t.Errorf("the reclassification refusal quoted content: %v", err)
	}

	if strings.Contains(e.logs.String(), canary) {
		t.Errorf("the service logged content: %s", e.logs.String())
	}
}

/* ── shared assertion ────────────────────────────────────────────────── */

// assertSameAnswer requires that two refusals differ only in the id they
// name. Any other difference is a way to tell a neighbour's row from a
// fabricated one.
func assertSameAnswer(t *testing.T, what string, real, fake error, realID, fakeID uuid.UUID) {
	t.Helper()
	realMsg := strings.Replace(real.Error(), realID.String(), "<id>", 1)
	fakeMsg := strings.Replace(fake.Error(), fakeID.String(), "<id>", 1)
	if realMsg != fakeMsg {
		t.Errorf("%s: the two answers differ:\n  real: %s\n  fake: %s", what, realMsg, fakeMsg)
	}
}
