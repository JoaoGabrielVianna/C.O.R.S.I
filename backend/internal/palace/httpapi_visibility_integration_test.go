//go:build integration

// Palace, slice S2: the surface's eligibility predicate, proved closed.
//
//	Run with: TEST_POSTGRES_DSN=... go test -tags=integration ./internal/palace/...
//
// ══════════════════════════════════════════════════════════════════════
//
//	IF THE SURFACE CANNOT LIST IT, THE SURFACE CANNOT COUNT IT,
//	SEARCH IT, REVEAL IT BY DEEP LINK, OR INFER IT THROUGH CONTAINMENT
//
// ══════════════════════════════════════════════════════════════════════
//
// S1 proved the read-by-id half. This file proves the other four, and it
// proves them against ONE fixture rather than five, because the point is
// that the five agree. A suite that built a different world for each
// question could pass while the five predicates quietly diverged, which is
// exactly the failure this slice exists to prevent.
//
// The withheld rows all carry a canary in their text, so a leak through
// any surface is a substring match rather than a matter of reading the
// right field carefully.
package palace

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/palace/domain"
	"github.com/corsi/backend/internal/palace/ports"
)

/* ── the matrix ──────────────────────────────────────────────────────── */

// matrix is every combination that decides eligibility, in one workspace.
//
// Naming: `visible*` is expected to reach the surface, `withheld*` is
// expected never to. A test that has to think about which is which has a
// fixture problem, not a test problem.
type matrix struct {
	visibleRoom  *domain.Room
	withheldRoom *domain.Room

	// In the visible room.
	visibleArtifact  *domain.Artifact
	withheldArtifact *domain.Artifact // highly sensitive itself
	archivedArtifact *domain.Artifact

	// In the withheld room. Both say `normal` about themselves and both
	// must disappear: this is D1.
	containedArtifact *domain.Artifact
	containedArchived *domain.Artifact

	// Filed nowhere. Contained in nothing, so evaluated on its own rules.
	unfiledArtifact *domain.Artifact

	visibleMemory *domain.Memory
	// Normal, filed in the VISIBLE room, but about an artifact that lives
	// in the withheld one. This is D1.1, and it is the case a room-scoped
	// predicate would miss.
	transitiveMemory *domain.Memory
	// Highly sensitive itself, attached to a perfectly visible artifact.
	withheldMemory *domain.Memory
	// Normal, filed directly in the withheld room.
	containedMemory *domain.Memory
	unfiledMemory   *domain.Memory
}

// withheldText is carried by every row that must never surface, and it is
// also the search term the search tests use.
const withheldText = "CONTIDO-CANARY-4f2b"

func (e *env) matrix() matrix {
	e.t.Helper()

	var m matrix
	m.visibleRoom = e.room(e.mine, "Ateliê de Marcenaria", domain.SensitivityNormal)
	m.withheldRoom = e.room(e.mine, "Terapia", domain.SensitivityHighlySensitive)

	m.visibleArtifact = e.artifact(e.mine, domain.ArtifactList,
		"Lista de presentes", domain.SensitivityNormal, &m.visibleRoom.ID)
	m.withheldArtifact = e.artifact(e.mine, domain.ArtifactNote,
		"Segredo "+withheldText, domain.SensitivityHighlySensitive, &m.visibleRoom.ID)
	m.archivedArtifact = e.artifact(e.mine, domain.ArtifactList,
		"Lista antiga", domain.SensitivityNormal, &m.visibleRoom.ID)
	e.archiveArtifact(e.mine, m.archivedArtifact.ID)

	m.containedArtifact = e.artifact(e.mine, domain.ArtifactList,
		"Lista comum "+withheldText, domain.SensitivityNormal, &m.withheldRoom.ID)
	m.containedArchived = e.artifact(e.mine, domain.ArtifactNote,
		"Nota antiga "+withheldText, domain.SensitivityNormal, &m.withheldRoom.ID)
	e.archiveArtifact(e.mine, m.containedArchived.ID)

	m.unfiledArtifact = e.artifact(e.mine, domain.ArtifactNote,
		"Ainda sem sala", domain.SensitivityNormal, nil)

	m.visibleMemory = e.memoryAbout(e.mine, "ela prefere caneca a xícara",
		domain.SensitivityNormal, &m.visibleRoom.ID, &m.visibleArtifact.ID)
	m.transitiveMemory = e.memoryAbout(e.mine, "o que concluí daquilo "+withheldText,
		domain.SensitivityNormal, &m.visibleRoom.ID, &m.containedArtifact.ID)
	m.withheldMemory = e.memoryAbout(e.mine, "muito privado "+withheldText,
		domain.SensitivityHighlySensitive, &m.visibleRoom.ID, &m.visibleArtifact.ID)
	m.containedMemory = e.memoryAbout(e.mine, "nota comum "+withheldText,
		domain.SensitivityNormal, &m.withheldRoom.ID, nil)
	m.unfiledMemory = e.memoryAbout(e.mine, "conhecimento sem sala",
		domain.SensitivityNormal, nil, nil)

	return m
}

func (e *env) archiveArtifact(ws, id uuid.UUID) {
	e.t.Helper()
	archived := domain.LifecycleArchived
	if _, err := e.svc.UpdateArtifact(e.ctx(), ws, id,
		domain.ArtifactChange{Status: &archived}); err != nil {
		e.t.Fatalf("archive artifact: %v", err)
	}
}

/* ══════════════════════════════════════════════════════════════════════
   A · Listings and their totals agree, over the whole matrix
   ══════════════════════════════════════════════════════════════════════ */

func TestTheArtifactListingReturnsExactlyTheEligibleRows(t *testing.T) {
	e := newEnv(t)
	s := e.mount()
	m := e.matrix()

	rows, total := s.as(e.mine, "/palace/artifacts").
		requireStatus(t, http.StatusOK, "artifact listing").
		listOf(t, "artifact listing")
	got := ids(rows, "artifact_id")

	// Eligible: visible in a visible room, and unfiled. Nothing else.
	for _, want := range []*domain.Artifact{m.visibleArtifact, m.unfiledArtifact} {
		if !got[want.ID.String()] {
			t.Errorf("an eligible artifact is missing: %s", want.ID)
		}
	}
	for name, hidden := range map[string]*domain.Artifact{
		"highly sensitive itself":      m.withheldArtifact,
		"normal, inside withheld room": m.containedArtifact,
		"archived":                     m.archivedArtifact,
		"archived inside withheld":     m.containedArchived,
	} {
		if got[hidden.ID.String()] {
			t.Errorf("a row that must not appear did (%s): %s", name, hidden.ID)
		}
	}
	if total != 2 {
		t.Fatalf("total = %v, want 2; the count must agree with the list", total)
	}
}

func TestTheMemoryListingReturnsExactlyTheEligibleRows(t *testing.T) {
	e := newEnv(t)
	s := e.mount()
	m := e.matrix()

	rows, total := s.as(e.mine, "/palace/memories").
		requireStatus(t, http.StatusOK, "memory listing").
		listOf(t, "memory listing")
	got := ids(rows, "memory_id")

	for _, want := range []*domain.Memory{m.visibleMemory, m.unfiledMemory} {
		if !got[want.ID.String()] {
			t.Errorf("an eligible memory is missing: %s", want.ID)
		}
	}
	for name, hidden := range map[string]*domain.Memory{
		"highly sensitive itself":               m.withheldMemory,
		"normal, about an artifact in withheld": m.transitiveMemory,
		"normal, filed in withheld room":        m.containedMemory,
	} {
		if got[hidden.ID.String()] {
			t.Errorf("a row that must not appear did (%s): %s", name, hidden.ID)
		}
	}
	if total != 2 {
		t.Fatalf("total = %v, want 2", total)
	}
}

// TestNoListingBodyEverCarriesWithheldText is the blunt instrument, and it
// is worth having next to the precise ones: whatever the shape of a future
// regression, the canary shows up in the bytes.
func TestNoListingBodyEverCarriesWithheldText(t *testing.T) {
	e := newEnv(t)
	s := e.mount()
	m := e.matrix()

	for _, path := range []string{
		"/palace/overview",
		"/palace/rooms",
		"/palace/rooms?status=archived",
		"/palace/artifacts",
		"/palace/artifacts?status=archived",
		"/palace/artifacts?room_id=none",
		"/palace/artifacts?room_id=" + m.visibleRoom.ID.String(),
		"/palace/memories",
		"/palace/memories?status=archived",
		"/palace/memories?room_id=none",
		"/palace/memories?room_id=" + m.visibleRoom.ID.String(),
		"/palace/artifacts/" + m.visibleArtifact.ID.String(),
		"/palace/rooms/" + m.visibleRoom.ID.String(),
	} {
		res := s.as(e.mine, path)
		if strings.Contains(res.body, withheldText) {
			t.Errorf("GET %s leaked withheld content: %s", path, res.body)
		}
		if strings.Contains(res.body, "Terapia") {
			t.Errorf("GET %s leaked the withheld room's name: %s", path, res.body)
		}
	}
}

/* ══════════════════════════════════════════════════════════════════════
   B · List and Count cannot disagree
   ══════════════════════════════════════════════════════════════════════ */

// TestTotalMatchesTheRowsAcrossEveryScope walks the listings with a page
// big enough to hold everything, so `total` and `len(items)` must be equal.
//
// ── Why this is a test and not an observation ──────────────────────────
// Because `Count` is a second statement. It shares a predicate builder
// with `List` today, and the day somebody writes a bespoke count is the day
// the two can differ by exactly the rows that are being withheld. A
// mismatch here is that bug, whatever caused it.
func TestTotalMatchesTheRowsAcrossEveryScope(t *testing.T) {
	e := newEnv(t)
	s := e.mount()
	m := e.matrix()

	for _, path := range []string{
		"/palace/rooms?limit=100",
		"/palace/rooms?status=archived&limit=100",
		"/palace/artifacts?limit=100",
		"/palace/artifacts?status=archived&limit=100",
		"/palace/artifacts?room_id=none&limit=100",
		"/palace/artifacts?room_id=" + m.visibleRoom.ID.String() + "&limit=100",
		"/palace/artifacts?kind=list&limit=100",
		"/palace/artifacts?search=lista&limit=100",
		"/palace/memories?limit=100",
		"/palace/memories?status=archived&limit=100",
		"/palace/memories?room_id=none&limit=100",
		"/palace/memories?room_id=" + m.visibleRoom.ID.String() + "&limit=100",
		"/palace/memories?artifact_id=" + m.visibleArtifact.ID.String() + "&limit=100",
		"/palace/memories?min_importance=1&limit=100",
		"/palace/memories?search=caneca&limit=100",
	} {
		rows, total := s.as(e.mine, path).
			requireStatus(t, http.StatusOK, path).
			listOf(t, path)
		if float64(len(rows)) != total {
			t.Errorf("GET %s: %d rows but total %v; the count and the listing "+
				"disagree, and the difference is what is being withheld",
				path, len(rows), total)
		}
	}
}

/* ══════════════════════════════════════════════════════════════════════
   C · Search is not a second visibility model
   ══════════════════════════════════════════════════════════════════════ */

// TestSearchCannotFindWithheldContent is the requirement stated exactly:
// a term present ONLY inside withheld rows produces zero items, zero
// total, and no indication that anything was skipped.
func TestSearchCannotFindWithheldContent(t *testing.T) {
	e := newEnv(t)
	s := e.mount()
	m := e.matrix()

	for _, path := range []string{
		"/palace/artifacts?search=" + withheldText,
		"/palace/artifacts?status=archived&search=" + withheldText,
		"/palace/artifacts?room_id=none&search=" + withheldText,
		"/palace/memories?search=" + withheldText,
		"/palace/memories?status=archived&search=" + withheldText,
		"/palace/rooms?search=Terapia",
	} {
		res := s.as(e.mine, path).requireStatus(t, http.StatusOK, path)
		rows, total := res.listOf(t, path)
		if len(rows) != 0 || total != 0 {
			t.Errorf("GET %s found withheld content: %d rows, total %v", path, len(rows), total)
		}
		for _, marker := range []string{"hidden", "omitted", "withheld", "redacted"} {
			if strings.Contains(strings.ToLower(res.body), marker) {
				t.Errorf("GET %s hints that something was withheld (%q): %s", path, marker, res.body)
			}
		}
	}

	// And the same search term inside an ELIGIBLE row is found, so the
	// zeroes above are the predicate working rather than the search being
	// broken.
	eligible := e.artifact(e.mine, domain.ArtifactNote, "agora visível "+withheldText,
		domain.SensitivityNormal, &m.visibleRoom.ID)
	rows, total := s.as(e.mine, "/palace/artifacts?search="+withheldText).
		requireStatus(t, http.StatusOK, "search after adding an eligible match").
		listOf(t, "search after adding an eligible match")
	if !ids(rows, "artifact_id")[eligible.ID.String()] || total != 1 {
		t.Fatalf("the search found %d rows (total %v); it should find exactly the eligible one",
			len(rows), total)
	}
}

/* ══════════════════════════════════════════════════════════════════════
   D · The overview counts only what the surface could reveal
   ══════════════════════════════════════════════════════════════════════ */

func TestTheOverviewCountsOnlyEligibleRows(t *testing.T) {
	e := newEnv(t)
	s := e.mount()
	m := e.matrix()

	body := s.as(e.mine, "/palace/overview").
		requireStatus(t, http.StatusOK, "overview").
		decode(t, "overview")

	rooms, ok := body["rooms"].([]any)
	if !ok {
		t.Fatalf("no rooms array: %s", body)
	}
	if len(rooms) != 1 {
		t.Fatalf("the overview shows %d rooms, want 1: the withheld room must not be one",
			len(rooms))
	}
	if body["room_total"] != float64(1) {
		t.Fatalf("room_total = %v, want 1", body["room_total"])
	}

	room := rooms[0].(map[string]any)
	if room["room_id"] != m.visibleRoom.ID.String() {
		t.Fatalf("the wrong room is on the map: %v", room["room_id"])
	}
	// One eligible active artifact (the highly sensitive one and the
	// archived one are both out), one eligible active memory, one archived.
	if room["artifact_count"] != float64(1) {
		t.Errorf("artifact_count = %v, want 1", room["artifact_count"])
	}
	if room["memory_count"] != float64(1) {
		t.Errorf("memory_count = %v, want 1", room["memory_count"])
	}
	if room["archived_count"] != float64(1) {
		t.Errorf("archived_count = %v, want 1", room["archived_count"])
	}

	unfiled, ok := body["unfiled"].(map[string]any)
	if !ok {
		t.Fatalf("no unfiled block: %s", body)
	}
	if unfiled["artifact_count"] != float64(1) {
		t.Errorf("unfiled artifact_count = %v, want 1", unfiled["artifact_count"])
	}
	if unfiled["archived_count"] != float64(0) {
		t.Errorf("unfiled archived_count = %v, want 0", unfiled["archived_count"])
	}
}

// TestTheOverviewCountsAgreeWithWhatTheListingsReturn is the closure
// check: every number on the map has to be a promise the surface can
// honour when somebody opens it.
func TestTheOverviewCountsAgreeWithWhatTheListingsReturn(t *testing.T) {
	e := newEnv(t)
	s := e.mount()
	m := e.matrix()

	body := s.as(e.mine, "/palace/overview").
		requireStatus(t, http.StatusOK, "overview").
		decode(t, "overview")
	room := body["rooms"].([]any)[0].(map[string]any)

	for _, tc := range []struct {
		field string
		path  string
	}{
		{"artifact_count", "/palace/artifacts?limit=100&room_id=" + m.visibleRoom.ID.String()},
		{"archived_count", "/palace/artifacts?limit=100&status=archived&room_id=" + m.visibleRoom.ID.String()},
		{"memory_count", "/palace/memories?limit=100&room_id=" + m.visibleRoom.ID.String()},
	} {
		rows, total := s.as(e.mine, tc.path).
			requireStatus(t, http.StatusOK, tc.path).
			listOf(t, tc.path)
		if room[tc.field] != total {
			t.Errorf("%s says %v but %s returns a total of %v",
				tc.field, room[tc.field], tc.path, total)
		}
		if float64(len(rows)) != total {
			t.Errorf("%s: %d rows against a total of %v", tc.path, len(rows), total)
		}
	}

	unfiled := body["unfiled"].(map[string]any)
	_, unfiledTotal := s.as(e.mine, "/palace/artifacts?limit=100&room_id=none").
		requireStatus(t, http.StatusOK, "unfiled listing").
		listOf(t, "unfiled listing")
	if unfiled["artifact_count"] != unfiledTotal {
		t.Errorf("unfiled artifact_count = %v but the listing totals %v",
			unfiled["artifact_count"], unfiledTotal)
	}
}

func TestTheRoomDetailCountsAgreeWithTheOverview(t *testing.T) {
	e := newEnv(t)
	s := e.mount()
	m := e.matrix()

	detail := s.as(e.mine, "/palace/rooms/"+m.visibleRoom.ID.String()).
		requireStatus(t, http.StatusOK, "room detail").
		decode(t, "room detail")
	overview := s.as(e.mine, "/palace/overview").
		requireStatus(t, http.StatusOK, "overview").
		decode(t, "overview")
	room := overview["rooms"].([]any)[0].(map[string]any)

	for _, field := range []string{"artifact_count", "memory_count", "archived_count"} {
		if detail[field] != room[field] {
			t.Errorf("%s: detail says %v, overview says %v; two reads of the same "+
				"fact must not disagree", field, detail[field], room[field])
		}
	}
}

/* ══════════════════════════════════════════════════════════════════════
   E · Reversibility: this is visibility inheritance, not sensitivity
   ══════════════════════════════════════════════════════════════════════ */

// TestMovingContentOutOfAWithheldRoomRestoresItWithoutRelabelling is the
// proof that nothing here mutates a label.
//
// Both the artifact AND the memory that depends on it come back, and
// neither row's sensitivity moved. If this ever fails while the withholding
// tests still pass, the implementation has started copying sensitivity
// instead of inheriting visibility, and that is a different product.
func TestMovingContentOutOfAWithheldRoomRestoresItWithoutRelabelling(t *testing.T) {
	e := newEnv(t)
	s := e.mount()
	m := e.matrix()

	beforeArtifact := m.containedArtifact.Sensitivity
	beforeMemory := m.transitiveMemory.Sensitivity

	// Both are invisible while the containment holds.
	artifacts, _ := s.as(e.mine, "/palace/artifacts?limit=100").
		requireStatus(t, http.StatusOK, "before").listOf(t, "before")
	if ids(artifacts, "artifact_id")[m.containedArtifact.ID.String()] {
		t.Fatal("the contained artifact was visible before the move")
	}
	memories, _ := s.as(e.mine, "/palace/memories?limit=100").
		requireStatus(t, http.StatusOK, "before").listOf(t, "before")
	if ids(memories, "memory_id")[m.transitiveMemory.ID.String()] {
		t.Fatal("the transitive memory was visible before the move")
	}

	// One edit: the artifact changes rooms. Nothing touches the memory, and
	// nothing touches any sensitivity.
	moved, err := e.svc.UpdateArtifact(e.ctx(), e.mine, m.containedArtifact.ID,
		domain.ArtifactChange{Room: domain.SetRef(m.visibleRoom.ID)})
	if err != nil {
		t.Fatalf("move artifact: %v", err)
	}
	if moved.Artifact.Sensitivity != beforeArtifact {
		t.Fatalf("the move changed the artifact's sensitivity from %q to %q",
			beforeArtifact, moved.Artifact.Sensitivity)
	}

	artifacts, _ = s.as(e.mine, "/palace/artifacts?limit=100").
		requireStatus(t, http.StatusOK, "after").listOf(t, "after")
	if !ids(artifacts, "artifact_id")[m.containedArtifact.ID.String()] {
		t.Error("the artifact did not become eligible after leaving the withheld room")
	}

	// The memory comes back too, without anybody editing the memory. That
	// is the transitive rule releasing, which is what makes it inheritance.
	memories, _ = s.as(e.mine, "/palace/memories?limit=100").
		requireStatus(t, http.StatusOK, "after").listOf(t, "after")
	if !ids(memories, "memory_id")[m.transitiveMemory.ID.String()] {
		t.Error("the memory about it did not become eligible; the transitive rule " +
			"is not releasing when the containment does")
	}

	// And on the record: neither sensitivity moved, read back from storage.
	reloadedArtifact, err := e.svc.GetArtifact(e.ctx(), e.mine, m.containedArtifact.ID)
	if err != nil {
		t.Fatalf("reload artifact: %v", err)
	}
	if reloadedArtifact.Sensitivity != beforeArtifact {
		t.Errorf("stored artifact sensitivity is %q, was %q",
			reloadedArtifact.Sensitivity, beforeArtifact)
	}
	reloadedMemory, err := e.svc.GetMemory(e.ctx(), e.mine, m.transitiveMemory.ID)
	if err != nil {
		t.Fatalf("reload memory: %v", err)
	}
	if reloadedMemory.Sensitivity != beforeMemory {
		t.Errorf("stored memory sensitivity is %q, was %q",
			reloadedMemory.Sensitivity, beforeMemory)
	}
}

// TestRelabellingTheRoomAloneRestoresItsContents is the same proof from the
// other side: nothing about the contained rows changes at all, and they
// become eligible because the CONTAINER did.
func TestRelabellingTheRoomAloneRestoresItsContents(t *testing.T) {
	e := newEnv(t)
	s := e.mount()
	m := e.matrix()

	normal := domain.SensitivityNormal
	if _, err := e.svc.UpdateRoom(e.ctx(), e.mine, m.withheldRoom.ID,
		domain.RoomChange{Sensitivity: &normal}); err != nil {
		t.Fatalf("relabel room: %v", err)
	}

	artifacts, _ := s.as(e.mine, "/palace/artifacts?limit=100").
		requireStatus(t, http.StatusOK, "after relabel").listOf(t, "after relabel")
	got := ids(artifacts, "artifact_id")
	if !got[m.containedArtifact.ID.String()] {
		t.Error("the contained artifact is still withheld after its room became normal")
	}

	memories, _ := s.as(e.mine, "/palace/memories?limit=100").
		requireStatus(t, http.StatusOK, "after relabel").listOf(t, "after relabel")
	gotMemories := ids(memories, "memory_id")
	if !gotMemories[m.containedMemory.ID.String()] {
		t.Error("the memory filed in that room is still withheld")
	}
	if !gotMemories[m.transitiveMemory.ID.String()] {
		t.Error("the memory about the contained artifact is still withheld")
	}
	// The one that is withheld on its own account stays withheld: relabelling
	// a container must not promote what was never about the container.
	if gotMemories[m.withheldMemory.ID.String()] {
		t.Error("a highly sensitive memory surfaced because an unrelated room was relabelled")
	}
}

/* ══════════════════════════════════════════════════════════════════════
   F · Archived, across the combinations
   ══════════════════════════════════════════════════════════════════════ */

// TestArchivedObeysContainmentToo guards the combination most likely to be
// forgotten: the archive affordance is a second listing, and a second
// listing is a second chance to drop the predicate.
func TestArchivedObeysContainmentToo(t *testing.T) {
	e := newEnv(t)
	s := e.mount()
	m := e.matrix()

	rows, total := s.as(e.mine, "/palace/artifacts?status=archived&limit=100").
		requireStatus(t, http.StatusOK, "archived listing").
		listOf(t, "archived listing")
	got := ids(rows, "artifact_id")

	if !got[m.archivedArtifact.ID.String()] {
		t.Error("the archived artifact in a visible room is unreachable")
	}
	if got[m.containedArchived.ID.String()] {
		t.Error("an archived artifact inside a withheld room appeared in the archive")
	}
	if total != 1 {
		t.Errorf("archived total = %v, want 1", total)
	}

	// And the archived row inside the withheld room is not reachable by id
	// either, which is the deep-link half of the same rule.
	s.as(e.mine, "/palace/artifacts/"+m.containedArchived.ID.String()).
		requireStatus(t, http.StatusNotFound, "archived, contained, by id")
}

/* ══════════════════════════════════════════════════════════════════════
   G · Tallies
   ══════════════════════════════════════════════════════════════════════ */

func TestArtifactRowsCarryTheirEntryTally(t *testing.T) {
	e := newEnv(t)
	s := e.mount()

	room := e.room(e.mine, "Dojang", domain.SensitivityNormal)
	withEntries := e.artifact(e.mine, domain.ArtifactList, "Equipamentos", domain.SensitivityNormal, &room.ID)
	empty := e.artifact(e.mine, domain.ArtifactNote, "Uma nota", domain.SensitivityNormal, &room.ID)

	for i := 0; i < 3; i++ {
		e.item(e.mine, withEntries.ID, "item "+string(rune('a'+i)))
	}
	done := e.item(e.mine, withEntries.ID, "item feito")
	yes := true
	if _, err := e.svc.UpdateItem(e.ctx(), e.mine, withEntries.ID, done.ID,
		domain.ItemChange{Done: &yes}); err != nil {
		t.Fatalf("tick item: %v", err)
	}

	rows, _ := s.as(e.mine, "/palace/artifacts?limit=100").
		requireStatus(t, http.StatusOK, "listing with tallies").
		listOf(t, "listing with tallies")

	byID := map[string]map[string]any{}
	for _, row := range rows {
		byID[row["artifact_id"].(string)] = row
	}

	full := byID[withEntries.ID.String()]
	if full["item_count"] != float64(4) || full["item_done_count"] != float64(1) {
		t.Errorf("tally = %v of %v, want 1 of 4", full["item_done_count"], full["item_count"])
	}
	// An artifact with no entries reports two zeroes rather than omitting
	// the fields: this count was computed, it just came out zero.
	none := byID[empty.ID.String()]
	if none["item_count"] != float64(0) || none["item_done_count"] != float64(0) {
		t.Errorf("empty artifact tally = %v of %v, want 0 of 0",
			none["item_done_count"], none["item_count"])
	}
}

/* ══════════════════════════════════════════════════════════════════════
   H · The Core is still the Core
   ══════════════════════════════════════════════════════════════════════ */

// TestTheCoreListingsAreUnchangedByTheSurfacePredicate is invariant I-F,
// checked at the level the capabilities actually use.
//
// The tools call ListArtifacts and ListMemories with filters that do not
// set InheritRoomVisibility. If those started withholding, an authorized
// agent would silently lose access to the operator's own record, which is
// a regression this slice must not cause while closing the surface.
func TestTheCoreListingsAreUnchangedByTheSurfacePredicate(t *testing.T) {
	e := newEnv(t)
	m := e.matrix()

	active := domain.LifecycleActive
	artifacts, total, err := e.svc.ListArtifacts(e.ctx(), e.mine, ports.ArtifactFilter{
		Status: &active,
		Page:   ports.Page{Limit: 100},
	})
	if err != nil {
		t.Fatalf("core artifact listing: %v", err)
	}
	var sawContained bool
	for _, a := range artifacts {
		if a.ID == m.containedArtifact.ID {
			sawContained = true
		}
	}
	if !sawContained {
		t.Error("the Core listing stopped returning an artifact in a highly sensitive " +
			"room; the surface rule must be opt-in")
	}
	if total != int64(len(artifacts)) {
		t.Errorf("core listing: %d rows against total %v", len(artifacts), total)
	}

	memories, _, err := e.svc.ListMemories(e.ctx(), e.mine, ports.MemoryFilter{
		Status: &active,
		Page:   ports.Page{Limit: 100},
	})
	if err != nil {
		t.Fatalf("core memory listing: %v", err)
	}
	var sawTransitive bool
	for _, mem := range memories {
		if mem.ID == m.transitiveMemory.ID {
			sawTransitive = true
		}
	}
	if !sawTransitive {
		t.Error("the Core listing stopped returning a memory about a contained artifact")
	}
}

/* ══════════════════════════════════════════════════════════════════════
   I · Isolation still holds through every new read
   ══════════════════════════════════════════════════════════════════════ */

func TestTheNewReadsNeverCrossAWorkspace(t *testing.T) {
	e := newEnv(t)
	s := e.mount()

	theirRoom := e.room(e.theirs, "A sala deles", domain.SensitivityNormal)
	e.artifact(e.theirs, domain.ArtifactList, "O que eles guardam", domain.SensitivityNormal, &theirRoom.ID)
	e.memoryAbout(e.theirs, "o que eles sabem", domain.SensitivityNormal, &theirRoom.ID, nil)

	body := s.as(e.mine, "/palace/overview").
		requireStatus(t, http.StatusOK, "overview").decode(t, "overview")
	if rooms := body["rooms"].([]any); len(rooms) != 0 {
		t.Fatalf("the overview shows %d rooms of a neighbour's workspace", len(rooms))
	}
	if body["room_total"] != float64(0) {
		t.Fatalf("room_total = %v, want 0", body["room_total"])
	}

	for _, path := range []string{"/palace/artifacts", "/palace/memories", "/palace/rooms"} {
		rows, total := s.as(e.mine, path).
			requireStatus(t, http.StatusOK, path).listOf(t, path)
		if len(rows) != 0 || total != 0 {
			t.Errorf("GET %s returned %d rows (total %v) from another workspace",
				path, len(rows), total)
		}
	}

	// Scoping to their room is not-found, not an empty page: an empty page
	// would say "that room is empty" about a room that is not theirs to ask
	// about.
	s.as(e.mine, "/palace/artifacts?room_id="+theirRoom.ID.String()).
		requireStatus(t, http.StatusNotFound, "scoped to a neighbour's room")
	s.as(e.mine, "/palace/memories?room_id="+theirRoom.ID.String()).
		requireStatus(t, http.StatusNotFound, "memories scoped to a neighbour's room")
}
