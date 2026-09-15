//go:build integration

// Palace, slice S3: artifacts and their entries.
//
// The harness lives in palace_integration_test.go; this file is the same
// package and uses it.
package palace

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/palace/app"
	"github.com/corsi/backend/internal/palace/domain"
	"github.com/corsi/backend/internal/palace/ports"
)

/* ── seed helpers ────────────────────────────────────────────────────── */

func (e *env) artifact(ws uuid.UUID, kind domain.ArtifactKind, title string, level domain.Sensitivity, roomID *uuid.UUID) *domain.Artifact {
	e.t.Helper()
	a, err := e.svc.CreateArtifact(e.ctx(), ws, app.CreateArtifactInput{
		Kind:        kind,
		Title:       title,
		RoomID:      roomID,
		Sensitivity: &level,
	})
	if err != nil {
		e.t.Fatalf("seed artifact: %v", err)
	}
	return a
}

func (e *env) item(ws, artifactID uuid.UUID, text string) *domain.ArtifactItem {
	e.t.Helper()
	i, err := e.svc.AddItem(e.ctx(), ws, artifactID, app.AddItemInput{Text: text})
	if err != nil {
		e.t.Fatalf("seed item: %v", err)
	}
	return i
}

/* ══════════════════════════════════════════════════════════════════════
   Artifact · isolation and references
   ══════════════════════════════════════════════════════════════════════ */

func TestAnArtifactCannotBeFiledInANeighboursRoom(t *testing.T) {
	e := newEnv(t)
	theirs := e.room(e.theirs, "A sala deles", domain.SensitivityNormal)
	fabricated := uuid.New()

	_, errReal := e.svc.CreateArtifact(e.ctx(), e.mine, app.CreateArtifactInput{
		Kind: domain.ArtifactNote, Title: "tentativa", RoomID: &theirs.ID,
	})
	_, errFake := e.svc.CreateArtifact(e.ctx(), e.mine, app.CreateArtifactInput{
		Kind: domain.ArtifactNote, Title: "tentativa", RoomID: &fabricated,
	})

	// Not a constraint violation: the same not-found a fabricated id
	// gets. The composite foreign key would also refuse the row, and its
	// refusal has a different shape, which is what somebody would use to
	// probe for a neighbour's room.
	assertNotFound(t, "filing into a neighbour's room", errReal)
	assertNotFound(t, "filing into a fabricated room", errFake)

	realMsg := strings.Replace(errReal.Error(), theirs.ID.String(), "<id>", 1)
	fakeMsg := strings.Replace(errFake.Error(), fabricated.String(), "<id>", 1)
	if realMsg != fakeMsg {
		t.Errorf("the two answers differ:\n  real: %s\n  fake: %s", realMsg, fakeMsg)
	}
}

func TestAnArtifactCannotBeMovedIntoANeighboursRoom(t *testing.T) {
	e := newEnv(t)
	theirs := e.room(e.theirs, "A sala deles", domain.SensitivityNormal)
	mine := e.artifact(e.mine, domain.ArtifactNote, "minha nota", domain.SensitivityNormal, nil)

	_, err := e.svc.UpdateArtifact(e.ctx(), e.mine, mine.ID,
		domain.ArtifactChange{Room: domain.SetRef(theirs.ID)})
	assertNotFound(t, "moving into a neighbour's room", err)

	after, err := e.svc.GetArtifact(e.ctx(), e.mine, mine.ID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if after.RoomID != nil {
		t.Errorf("the artifact was filed into a neighbour's room: %v", after.RoomID)
	}
}

func TestANeighboursArtifactIsIndistinguishableFromOneThatNeverExisted(t *testing.T) {
	e := newEnv(t)
	theirs := e.artifact(e.theirs, domain.ArtifactList, "a lista deles", domain.SensitivityNormal, nil)
	fabricated := uuid.New()

	_, errReal := e.svc.GetArtifact(e.ctx(), e.mine, theirs.ID)
	_, errFake := e.svc.GetArtifact(e.ctx(), e.mine, fabricated)

	assertNotFound(t, "neighbour's artifact", errReal)
	assertNotFound(t, "fabricated id", errFake)

	realMsg := strings.Replace(errReal.Error(), theirs.ID.String(), "<id>", 1)
	fakeMsg := strings.Replace(errFake.Error(), fabricated.String(), "<id>", 1)
	if realMsg != fakeMsg {
		t.Errorf("the two answers differ:\n  real: %s\n  fake: %s", realMsg, fakeMsg)
	}
}

func TestANeighboursArtifactCannotBeEdited(t *testing.T) {
	e := newEnv(t)
	theirs := e.artifact(e.theirs, domain.ArtifactNote, "a nota deles", domain.SensitivityNormal, nil)

	replacement := "reescrito por um estranho"
	_, err := e.svc.UpdateArtifact(e.ctx(), e.mine, theirs.ID,
		domain.ArtifactChange{Body: &replacement})
	assertNotFound(t, "editing a neighbour's artifact", err)

	after, err := e.svc.GetArtifact(e.ctx(), e.theirs, theirs.ID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if after.Body != "" {
		t.Error("the neighbour's artifact was rewritten by a stranger")
	}
}

func TestANeighboursArtifactsAreAbsentFromListings(t *testing.T) {
	e := newEnv(t)
	e.artifact(e.theirs, domain.ArtifactNote, "a nota deles", domain.SensitivityNormal, nil)
	mine := e.artifact(e.mine, domain.ArtifactPlan, "meu plano", domain.SensitivityNormal, nil)

	got, total, err := e.svc.ListArtifacts(e.ctx(), e.mine, ports.ArtifactFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 || total != 1 || got[0].ID != mine.ID {
		t.Errorf("the listing returned %d rows (total %d), want only this workspace's", len(got), total)
	}
}

/* ══════════════════════════════════════════════════════════════════════
   Artifact · kind, lifecycle, sensitivity, empty update
   ══════════════════════════════════════════════════════════════════════ */

func TestAnArtifactKindCannotBeChangedAfterCreation(t *testing.T) {
	// Two layers would have to grow a field for this to become possible:
	// ArtifactChange has no Kind, and the repository's UPDATE has no
	// `kind` in its SET clause. This asserts the outcome end to end.
	e := newEnv(t)
	a := e.artifact(e.mine, domain.ArtifactNote, "uma nota", domain.SensitivityNormal, nil)

	title := "outro título"
	if _, err := e.svc.UpdateArtifact(e.ctx(), e.mine, a.ID,
		domain.ArtifactChange{Title: &title}); err != nil {
		t.Fatalf("update: %v", err)
	}

	after, err := e.svc.GetArtifact(e.ctx(), e.mine, a.ID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if after.Kind != domain.ArtifactNote {
		t.Errorf("kind moved to %q through an ordinary edit", after.Kind)
	}
}

func TestAnEmptyArtifactUpdateIsRefused(t *testing.T) {
	e := newEnv(t)
	a := e.artifact(e.mine, domain.ArtifactList, "uma lista", domain.SensitivityNormal, nil)

	_, err := e.svc.UpdateArtifact(e.ctx(), e.mine, a.ID, domain.ArtifactChange{})
	assertInvalid(t, "empty artifact update", err)
}

func TestArchivingAnArtifactKeepsItReadable(t *testing.T) {
	e := newEnv(t)
	a := e.artifact(e.mine, domain.ArtifactProject, "um projeto", domain.SensitivityNormal, nil)

	archived := domain.LifecycleArchived
	res, err := e.svc.UpdateArtifact(e.ctx(), e.mine, a.ID,
		domain.ArtifactChange{Status: &archived})
	if err != nil {
		t.Fatalf("archive: %v", err)
	}
	if !res.StatusChanged || res.PreviousStatus != domain.LifecycleActive {
		t.Errorf("the move was misreported: %+v", res.ArtifactChangeResult)
	}

	got, err := e.svc.GetArtifact(e.ctx(), e.mine, a.ID)
	if err != nil {
		t.Fatalf("an archived artifact became unreadable: %v", err)
	}
	if got.Status != domain.LifecycleArchived {
		t.Errorf("status = %q", got.Status)
	}
}

func TestArtifactSensitivityGovernsListingsTheSameWay(t *testing.T) {
	e := newEnv(t)
	e.artifact(e.mine, domain.ArtifactNote, "comum", domain.SensitivityNormal, nil)
	e.artifact(e.mine, domain.ArtifactNote, "pessoal", domain.SensitivityPrivate, nil)
	hidden := e.artifact(e.mine, domain.ArtifactNote, "muito pessoal", domain.SensitivityHighlySensitive, nil)

	got, total, err := e.svc.ListArtifacts(e.ctx(), e.mine, ports.ArtifactFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 || total != 2 {
		t.Errorf("default listing: %d rows, total %d, want 2 and 2", len(got), total)
	}

	got, total, err = e.svc.ListArtifacts(e.ctx(), e.mine, ports.ArtifactFilter{
		Sensitive: ports.Sensitive{IncludeHighlySensitive: true},
	})
	if err != nil {
		t.Fatalf("list with opt-in: %v", err)
	}
	if len(got) != 3 || total != 3 {
		t.Errorf("opted-in listing: %d rows, total %d, want 3 and 3", len(got), total)
	}

	// Still reachable by id, because the rule is about broad listings.
	if _, err := e.svc.GetArtifact(e.ctx(), e.mine, hidden.ID); err != nil {
		t.Errorf("a highly sensitive artifact could not be read by id: %v", err)
	}
}

func TestArtifactListingIsNarrowedByKindAndRoomAndText(t *testing.T) {
	e := newEnv(t)
	room := e.room(e.mine, "Carreira", domain.SensitivityNormal)
	e.artifact(e.mine, domain.ArtifactList, "Leituras de 2026", domain.SensitivityNormal, &room.ID)
	e.artifact(e.mine, domain.ArtifactNote, "Uma nota solta", domain.SensitivityNormal, nil)

	list := domain.ArtifactList
	got, total, err := e.svc.ListArtifacts(e.ctx(), e.mine, ports.ArtifactFilter{Kind: &list})
	if err != nil {
		t.Fatalf("by kind: %v", err)
	}
	if len(got) != 1 || total != 1 {
		t.Errorf("by kind: %d rows, total %d, want 1", len(got), total)
	}

	got, _, err = e.svc.ListArtifacts(e.ctx(), e.mine, ports.ArtifactFilter{RoomID: &room.ID})
	if err != nil {
		t.Fatalf("by room: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("by room: %d rows, want 1", len(got))
	}

	got, _, err = e.svc.ListArtifacts(e.ctx(), e.mine, ports.ArtifactFilter{Search: "LEITURAS"})
	if err != nil {
		t.Fatalf("by search: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("case-insensitive search: %d rows, want 1", len(got))
	}
}

/* ══════════════════════════════════════════════════════════════════════
   ArtifactItem · AcceptsItems is the authority
   ══════════════════════════════════════════════════════════════════════ */

func TestOnlyTheKindsThatDecomposeAcceptEntries(t *testing.T) {
	e := newEnv(t)

	for _, kind := range domain.ArtifactKinds {
		a := e.artifact(e.mine, kind, "artefato "+string(kind), domain.SensitivityNormal, nil)
		_, err := e.svc.AddItem(e.ctx(), e.mine, a.ID, app.AddItemInput{Text: "uma entrada"})

		if kind.AcceptsItems() {
			if err != nil {
				t.Errorf("kind %q refused an entry: %v", kind, err)
			}
			continue
		}
		if err == nil {
			t.Errorf("kind %q accepted an entry", kind)
			continue
		}
		assertInvalid(t, "entry on a "+string(kind), err)
	}
}

func TestAnEntryOnANoteIsRefusedAndSaysWhichKindsWouldWork(t *testing.T) {
	// The named case, checked on its own because it is the one the rule
	// exists for: a note is prose, and a checklist hanging off it would
	// be a second statement of what the note contains.
	e := newEnv(t)
	note := e.artifact(e.mine, domain.ArtifactNote, "uma nota", domain.SensitivityNormal, nil)

	_, err := e.svc.AddItem(e.ctx(), e.mine, note.ID, app.AddItemInput{Text: "uma entrada"})
	assertInvalid(t, "entry on a note", err)

	// A model told only "refused" tries again. It has to learn what would
	// work, and the list is built from AcceptsItems rather than written
	// out, so it cannot go stale.
	for _, kind := range domain.ArtifactKinds {
		if !kind.AcceptsItems() {
			continue
		}
		if !strings.Contains(err.Error(), string(kind)) {
			t.Errorf("the refusal does not offer %q: %v", kind, err)
		}
	}
	if strings.Contains(err.Error(), string(domain.ArtifactNote)+",") {
		t.Errorf("the refusal offers the kind it just refused: %v", err)
	}
}

func TestAnEntryCannotBeAddedToANeighboursArtifact(t *testing.T) {
	e := newEnv(t)
	theirs := e.artifact(e.theirs, domain.ArtifactList, "a lista deles", domain.SensitivityNormal, nil)
	fabricated := uuid.New()

	_, errReal := e.svc.AddItem(e.ctx(), e.mine, theirs.ID, app.AddItemInput{Text: "entrada"})
	_, errFake := e.svc.AddItem(e.ctx(), e.mine, fabricated, app.AddItemInput{Text: "entrada"})

	assertNotFound(t, "entry on a neighbour's artifact", errReal)
	assertNotFound(t, "entry on a fabricated artifact", errFake)

	realMsg := strings.Replace(errReal.Error(), theirs.ID.String(), "<id>", 1)
	fakeMsg := strings.Replace(errFake.Error(), fabricated.String(), "<id>", 1)
	if realMsg != fakeMsg {
		t.Errorf("the two answers differ:\n  real: %s\n  fake: %s", realMsg, fakeMsg)
	}
}

/* ══════════════════════════════════════════════════════════════════════
   ArtifactItem · an entry is addressed by all three ids
   ══════════════════════════════════════════════════════════════════════ */

func TestAnEntryFromAnotherArtifactCannotBeReachedByItsIdAlone(t *testing.T) {
	// ── The bug this closes ────────────────────────────────────────────
	// An entry id is a plain uuid. With `WHERE id = $1`, a caller holding
	// one from a checklist could tick a box on a different checklist and
	// the statement would succeed. Both lists belong to the same
	// workspace here, so nothing but the artifact predicate stands
	// between them.
	e := newEnv(t)
	listA := e.artifact(e.mine, domain.ArtifactList, "Lista A", domain.SensitivityNormal, nil)
	listB := e.artifact(e.mine, domain.ArtifactList, "Lista B", domain.SensitivityNormal, nil)
	entry := e.item(e.mine, listA.ID, "uma entrada da lista A")

	// Read it through the wrong artifact.
	_, err := e.svc.GetItem(e.ctx(), e.mine, listB.ID, entry.ID)
	assertNotFound(t, "reading A's entry through B", err)

	// Tick it through the wrong artifact.
	done := true
	_, err = e.svc.UpdateItem(e.ctx(), e.mine, listB.ID, entry.ID, domain.ItemChange{Done: &done})
	assertNotFound(t, "ticking A's entry through B", err)

	// Remove it through the wrong artifact.
	err = e.svc.RemoveItem(e.ctx(), e.mine, listB.ID, entry.ID)
	assertNotFound(t, "removing A's entry through B", err)

	// And it really was not touched by any of the three.
	after, err := e.svc.GetItem(e.ctx(), e.mine, listA.ID, entry.ID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if after.Done {
		t.Error("the entry was ticked through the wrong artifact")
	}
}

func TestAnEntryFromAnotherWorkspaceCannotBeReached(t *testing.T) {
	e := newEnv(t)
	theirList := e.artifact(e.theirs, domain.ArtifactList, "a lista deles", domain.SensitivityNormal, nil)
	theirEntry := e.item(e.theirs, theirList.ID, "uma entrada deles")

	// Even naming their artifact correctly, the workspace is wrong.
	_, err := e.svc.GetItem(e.ctx(), e.mine, theirList.ID, theirEntry.ID)
	assertNotFound(t, "reading a neighbour's entry", err)

	done := true
	_, err = e.svc.UpdateItem(e.ctx(), e.mine, theirList.ID, theirEntry.ID,
		domain.ItemChange{Done: &done})
	assertNotFound(t, "ticking a neighbour's entry", err)

	err = e.svc.RemoveItem(e.ctx(), e.mine, theirList.ID, theirEntry.ID)
	assertNotFound(t, "removing a neighbour's entry", err)

	after, err := e.svc.GetItem(e.ctx(), e.theirs, theirList.ID, theirEntry.ID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if after.Done {
		t.Error("a neighbour's entry was ticked by a stranger")
	}
}

/* ══════════════════════════════════════════════════════════════════════
   ArtifactItem · ordinary use
   ══════════════════════════════════════════════════════════════════════ */

func TestEntriesAppendInOrderWhenNoSlotIsAsked(t *testing.T) {
	// A person adding to a list means "at the end", and making them
	// compute an index would be asking them to know how long it is.
	e := newEnv(t)
	list := e.artifact(e.mine, domain.ArtifactList, "Leituras", domain.SensitivityNormal, nil)

	var positions []int
	for i := 0; i < 3; i++ {
		item := e.item(e.mine, list.ID, fmt.Sprintf("livro %d", i))
		positions = append(positions, item.Position)
	}
	for i, p := range positions {
		if p != i {
			t.Errorf("entry %d landed at position %d, want %d", i, p, i)
		}
	}

	got, total, err := e.svc.ListItems(e.ctx(), e.mine, list.ID, ports.Page{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 3 || total != 3 {
		t.Fatalf("%d entries, total %d, want 3", len(got), total)
	}
	for i, item := range got {
		if item.Position != i {
			t.Errorf("entry at index %d has position %d", i, item.Position)
		}
	}
}

func TestAnEntryCanBeGivenAnExplicitSlot(t *testing.T) {
	e := newEnv(t)
	list := e.artifact(e.mine, domain.ArtifactList, "Leituras", domain.SensitivityNormal, nil)

	first := 0
	item, err := e.svc.AddItem(e.ctx(), e.mine, list.ID, app.AddItemInput{
		Text: "vem antes", Position: &first,
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if item.Position != 0 {
		t.Errorf("position = %d, want 0", item.Position)
	}
}

func TestTickingAnEntryReportsWhetherItWasAlreadyTicked(t *testing.T) {
	e := newEnv(t)
	list := e.artifact(e.mine, domain.ArtifactList, "Leituras", domain.SensitivityNormal, nil)
	entry := e.item(e.mine, list.ID, "ler o capítulo 3")

	done := true
	res, err := e.svc.UpdateItem(e.ctx(), e.mine, list.ID, entry.ID, domain.ItemChange{Done: &done})
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if !res.DoneChanged || res.PreviousDone {
		t.Errorf("first tick misreported: %+v", res.ItemChangeResult)
	}

	res, err = e.svc.UpdateItem(e.ctx(), e.mine, list.ID, entry.ID, domain.ItemChange{Done: &done})
	if err != nil {
		t.Fatalf("second tick: %v", err)
	}
	if !res.Unchanged() || !res.PreviousDone {
		t.Errorf("a second tick reported work: %+v", res.ItemChangeResult)
	}
}

func TestAnEmptyEntryUpdateIsRefused(t *testing.T) {
	e := newEnv(t)
	list := e.artifact(e.mine, domain.ArtifactList, "Leituras", domain.SensitivityNormal, nil)
	entry := e.item(e.mine, list.ID, "uma entrada")

	_, err := e.svc.UpdateItem(e.ctx(), e.mine, list.ID, entry.ID, domain.ItemChange{})
	assertInvalid(t, "empty entry update", err)
}

func TestRemovingAnEntryTwiceIsNotFoundRatherThanSilentSuccess(t *testing.T) {
	// What lets a caller tell "I took it off" from "it was already gone".
	e := newEnv(t)
	list := e.artifact(e.mine, domain.ArtifactList, "Leituras", domain.SensitivityNormal, nil)
	entry := e.item(e.mine, list.ID, "uma entrada")

	if err := e.svc.RemoveItem(e.ctx(), e.mine, list.ID, entry.ID); err != nil {
		t.Fatalf("remove: %v", err)
	}
	assertNotFound(t, "removing twice", e.svc.RemoveItem(e.ctx(), e.mine, list.ID, entry.ID))

	got, total, err := e.svc.ListItems(e.ctx(), e.mine, list.ID, ports.Page{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 0 || total != 0 {
		t.Errorf("a removed entry is still listed: %d rows, total %d", len(got), total)
	}
}

func TestListingEntriesOfAFabricatedArtifactIsNotFoundRatherThanEmpty(t *testing.T) {
	// "This artifact has no entries" and "there is no such artifact" are
	// different answers, and a caller that received an empty list for a
	// fabricated id would go on believing the artifact exists.
	e := newEnv(t)
	theirs := e.artifact(e.theirs, domain.ArtifactList, "a lista deles", domain.SensitivityNormal, nil)

	_, _, err := e.svc.ListItems(e.ctx(), e.mine, uuid.New(), ports.Page{})
	assertNotFound(t, "listing entries of a fabricated artifact", err)

	_, _, err = e.svc.ListItems(e.ctx(), e.mine, theirs.ID, ports.Page{})
	assertNotFound(t, "listing entries of a neighbour's artifact", err)

	// And an artifact that really has none answers with an empty list.
	mine := e.artifact(e.mine, domain.ArtifactList, "minha lista", domain.SensitivityNormal, nil)
	got, total, err := e.svc.ListItems(e.ctx(), e.mine, mine.ID, ports.Page{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 0 || total != 0 {
		t.Errorf("an empty list reported %d rows, total %d", len(got), total)
	}
}

/* ══════════════════════════════════════════════════════════════════════
   Nothing new leaks
   ══════════════════════════════════════════════════════════════════════ */

func TestNoArtifactOrEntryFailurePathCarriesContent(t *testing.T) {
	e := newEnv(t)
	theirRoom := e.room(e.theirs, "A sala deles", domain.SensitivityNormal)
	note := e.artifact(e.mine, domain.ArtifactNote, "uma nota", domain.SensitivityHighlySensitive, nil)
	list := e.artifact(e.mine, domain.ArtifactList, "uma lista", domain.SensitivityNormal, nil)

	cases := map[string]func() error{
		"invalid kind": func() error {
			_, err := e.svc.CreateArtifact(e.ctx(), e.mine, app.CreateArtifactInput{
				Kind: "checklist", Title: canary, Body: canary,
			})
			return err
		},
		"body too long": func() error {
			_, err := e.svc.CreateArtifact(e.ctx(), e.mine, app.CreateArtifactInput{
				Kind: domain.ArtifactNote, Title: canary,
				Body: canary + strings.Repeat("x", domain.MaxArtifactBody),
			})
			return err
		},
		"foreign room": func() error {
			_, err := e.svc.CreateArtifact(e.ctx(), e.mine, app.CreateArtifactInput{
				Kind: domain.ArtifactNote, Title: canary, RoomID: &theirRoom.ID,
			})
			return err
		},
		"entry on a note": func() error {
			_, err := e.svc.AddItem(e.ctx(), e.mine, note.ID, app.AddItemInput{Text: canary})
			return err
		},
		"entry text too long": func() error {
			_, err := e.svc.AddItem(e.ctx(), e.mine, list.ID, app.AddItemInput{
				Text: canary + strings.Repeat("x", domain.MaxItemText),
			})
			return err
		},
		"entry on a fabricated artifact": func() error {
			_, err := e.svc.AddItem(e.ctx(), e.mine, uuid.New(), app.AddItemInput{Text: canary})
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

func TestArtifactsAndEntriesLoadedFromPostgresStillRedactThemselves(t *testing.T) {
	e := newEnv(t)
	a := e.artifact(e.mine, domain.ArtifactList, canary, domain.SensitivityHighlySensitive, nil)
	entry := e.item(e.mine, a.ID, canary)

	loadedArtifact, err := e.svc.GetArtifact(e.ctx(), e.mine, a.ID)
	if err != nil {
		t.Fatalf("read artifact: %v", err)
	}
	loadedItem, err := e.svc.GetItem(e.ctx(), e.mine, a.ID, entry.ID)
	if err != nil {
		t.Fatalf("read entry: %v", err)
	}

	for name, v := range map[string]any{"artifact": loadedArtifact, "entry": loadedItem} {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("%s: marshal: %v", name, err)
		}
		if strings.Contains(string(raw), canary) {
			t.Errorf("%s serialised its content: %s", name, raw)
		}
		if strings.Contains(fmt.Sprintf("%v", v), canary) {
			t.Errorf("%s formatted its content", name)
		}
	}
}
