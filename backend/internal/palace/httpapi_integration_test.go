//go:build integration

// Palace, slice S1: the operator's read surface, over real HTTP.
//
//	Run with: TEST_POSTGRES_DSN=... go test -tags=integration ./internal/palace/...
//
// The sentences this suite has to make convincing:
//
//	Nothing highly sensitive leaves this surface, and there is no way to
//	ask it to.
//
//	Something filed in a room this surface withholds is withheld with it,
//	whatever the thing itself says about its own sensitivity, and moving
//	it somewhere visible brings it back without editing it.
//
//	Every way of failing to find something gives the same answer, byte for
//	byte, whether the row was withheld, belongs to a neighbour, or never
//	existed.
//
//	A response carries the content it names and never a redacted entity.
//
//	An absent `status` means active, and the Core's "either state" default
//	is not reachable from here.
//
// What is REAL here: the module built the way the composition root builds
// it, the workspace middleware, chi, the handler, the application service,
// the repositories and Postgres. Nothing is faked. The one thing not
// exercised is the binary's own `main`, which does nothing but call the
// same two functions this harness calls.
package palace

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/corsi/backend/internal/palace/app"
	"github.com/corsi/backend/internal/palace/domain"
	"github.com/corsi/backend/internal/palace/ports"
	"github.com/corsi/backend/internal/platform/workspace"
)

/* ── harness ─────────────────────────────────────────────────────────── */

// surfaceCanary is content that must never appear on this surface. It goes
// into the withheld rows, so any leak is greppable rather than a matter of
// reading a field name carefully.
const surfaceCanary = "WITHHELD-CANARY-9c1d"

type surface struct {
	t      *testing.T
	server *httptest.Server
}

// mount builds the module exactly as the composition root does, guards it
// with the real workspace middleware in its production posture, and serves
// it over a real listener.
//
// ── Why RequireHeader is true here ─────────────────────────────────────
// Because the dev sentinel would substitute a workspace for a request that
// did not name one, and this suite has a test whose whole point is that an
// unnamed request is refused. Running the middleware in the lax posture
// would make that test pass for the wrong reason, and then keep passing
// after somebody deleted the check.
func (e *env) mount() *surface {
	e.t.Helper()

	mod := New(Deps{
		Pool:                e.pool,
		Logger:              slog.New(slog.NewJSONHandler(e.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		WorkspaceMiddleware: workspace.Middleware(workspace.Config{RequireHeader: true}, nil),
	})

	r := chi.NewRouter()
	mod.Register(r)

	srv := httptest.NewServer(r)
	e.t.Cleanup(srv.Close)
	return &surface{t: e.t, server: srv}
}

type response struct {
	status int
	body   string
}

// get issues a request as a workspace. A nil workspace sends no header at
// all, which is how the 401 path is reached.
func (s *surface) get(ws *uuid.UUID, path string) response {
	s.t.Helper()

	req, err := http.NewRequest(http.MethodGet, s.server.URL+path, nil)
	if err != nil {
		s.t.Fatalf("build request: %v", err)
	}
	if ws != nil {
		req.Header.Set(workspace.HeaderName, ws.String())
	}

	res, err := s.server.Client().Do(req)
	if err != nil {
		s.t.Fatalf("GET %s: %v", path, err)
	}
	defer func() { _ = res.Body.Close() }()

	raw, err := io.ReadAll(res.Body)
	if err != nil {
		s.t.Fatalf("read body: %v", err)
	}
	return response{status: res.StatusCode, body: string(raw)}
}

func (s *surface) as(ws uuid.UUID, path string) response { return s.get(&ws, path) }

func (r response) requireStatus(t *testing.T, want int, what string) response {
	t.Helper()
	if r.status != want {
		t.Fatalf("%s: status %d, want %d (body: %s)", what, r.status, want, r.body)
	}
	return r
}

func (r response) decode(t *testing.T, what string) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(r.body), &out); err != nil {
		t.Fatalf("%s: body is not a json object: %v (%s)", what, err, r.body)
	}
	return out
}

// listOf pulls the rows and the total out of a listing envelope.
func (r response) listOf(t *testing.T, what string) ([]map[string]any, float64) {
	t.Helper()
	body := r.decode(t, what)
	rawItems, ok := body["items"].([]any)
	if !ok {
		t.Fatalf("%s: no items array in %s", what, r.body)
	}
	total, ok := body["total"].(float64)
	if !ok {
		t.Fatalf("%s: no total in %s", what, r.body)
	}
	rows := make([]map[string]any, 0, len(rawItems))
	for _, raw := range rawItems {
		row, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("%s: an item is not an object in %s", what, r.body)
		}
		rows = append(rows, row)
	}
	return rows, total
}

func ids(rows []map[string]any, key string) map[string]bool {
	out := map[string]bool{}
	for _, row := range rows {
		if v, ok := row[key].(string); ok {
			out[v] = true
		}
	}
	return out
}

// memoryAbout seeds a memory that points at an artifact, which the shared
// helper cannot express.
func (e *env) memoryAbout(ws uuid.UUID, content string, level domain.Sensitivity, roomID, artifactID *uuid.UUID) *domain.Memory {
	e.t.Helper()
	m, err := e.svc.CreateMemory(e.ctx(), ws, app.CreateMemoryInput{
		Kind:        domain.MemoryFact,
		Content:     content,
		Sensitivity: &level,
		RoomID:      roomID,
		ArtifactID:  artifactID,
	})
	if err != nil {
		e.t.Fatalf("seed memory: %v", err)
	}
	return m
}

func (e *env) archiveRoom(ws, id uuid.UUID) {
	e.t.Helper()
	archived := domain.LifecycleArchived
	if _, err := e.svc.UpdateRoom(e.ctx(), ws, id, domain.RoomChange{Status: &archived}); err != nil {
		e.t.Fatalf("archive room: %v", err)
	}
}

/* ══════════════════════════════════════════════════════════════════════
   A · The workspace is required, on every route
   ══════════════════════════════════════════════════════════════════════ */

// everyRoute is the mounted surface, as paths. Kept in one place so a
// route added later without a workspace test is a route this list forgot,
// which is easier to notice in review than a missing test file.
func everyRoute(roomID, artifactID, memoryID uuid.UUID) []string {
	return []string{
		"/palace/overview",
		"/palace/rooms",
		"/palace/rooms/" + roomID.String(),
		"/palace/artifacts",
		"/palace/artifacts?room_id=" + roomID.String(),
		"/palace/artifacts?room_id=none",
		"/palace/artifacts/" + artifactID.String(),
		"/palace/memories",
		"/palace/memories?room_id=" + roomID.String(),
		"/palace/memories/" + memoryID.String(),
	}
}

func TestEveryPalaceRouteRefusesARequestWithNoWorkspace(t *testing.T) {
	e := newEnv(t)
	s := e.mount()

	room := e.room(e.mine, "Ateliê", domain.SensitivityNormal)
	artifact := e.artifact(e.mine, domain.ArtifactList, "Ferramentas", domain.SensitivityNormal, &room.ID)
	memory := e.memory(e.mine, "uma lembrança", domain.SensitivityNormal, &room.ID)

	for _, path := range everyRoute(room.ID, artifact.ID, memory.ID) {
		res := s.get(nil, path)
		if res.status != http.StatusUnauthorized {
			t.Errorf("GET %s with no workspace: status %d, want 401 (body: %s)",
				path, res.status, res.body)
		}
		if strings.Contains(res.body, "Ferramentas") || strings.Contains(res.body, "Ateliê") {
			t.Errorf("GET %s with no workspace returned content: %s", path, res.body)
		}
	}
}

func TestANeighboursRowsAreNotFoundOverHTTP(t *testing.T) {
	e := newEnv(t)
	s := e.mount()

	theirRoom := e.room(e.theirs, "A sala deles", domain.SensitivityNormal)
	theirArtifact := e.artifact(e.theirs, domain.ArtifactNote, "O projeto deles", domain.SensitivityNormal, &theirRoom.ID)
	theirMemory := e.memory(e.theirs, "o que eles sabem", domain.SensitivityNormal, &theirRoom.ID)

	fabricated := uuid.New()

	// Each pair must be identical, body included. Any difference is a way
	// to confirm that somebody else's row exists.
	for _, pair := range []struct {
		what  string
		real  string
		faked string
	}{
		{"room", "/palace/rooms/" + theirRoom.ID.String(), "/palace/rooms/" + fabricated.String()},
		{"artifact", "/palace/artifacts/" + theirArtifact.ID.String(), "/palace/artifacts/" + fabricated.String()},
		{"memory", "/palace/memories/" + theirMemory.ID.String(), "/palace/memories/" + fabricated.String()},
	} {
		got := s.as(e.mine, pair.real).requireStatus(t, http.StatusNotFound, pair.what+" of a neighbour")
		want := s.as(e.mine, pair.faked).requireStatus(t, http.StatusNotFound, pair.what+" fabricated")

		if normalizeID(got.body, theirRoom.ID, theirArtifact.ID, theirMemory.ID) !=
			normalizeID(want.body, fabricated, fabricated, fabricated) {
			t.Errorf("%s: a neighbour's row answers %q and a fabricated id answers %q; "+
				"the difference confirms the row exists", pair.what, got.body, want.body)
		}
	}
}

// normalizeID blanks the ids out of a not-found body, so two answers that
// differ only in the id they echo compare equal.
func normalizeID(body string, ids ...uuid.UUID) string {
	for _, id := range ids {
		body = strings.ReplaceAll(body, id.String(), "<id>")
	}
	return body
}

/* ══════════════════════════════════════════════════════════════════════
   B · Highly sensitive content never leaves, and cannot be asked for
   ══════════════════════════════════════════════════════════════════════ */

func TestTheRoomListingWithholdsHighlySensitiveRoomsAndDoesNotCountThem(t *testing.T) {
	e := newEnv(t)
	s := e.mount()

	ordinary := e.room(e.mine, "Ateliê de Marcenaria", domain.SensitivityNormal)
	private := e.room(e.mine, "Carreira", domain.SensitivityPrivate)
	withheld := e.room(e.mine, "Terapia "+surfaceCanary, domain.SensitivityHighlySensitive)

	rows, total := s.as(e.mine, "/palace/rooms").
		requireStatus(t, http.StatusOK, "room listing").
		listOf(t, "room listing")

	got := ids(rows, "room_id")
	if !got[ordinary.ID.String()] || !got[private.ID.String()] {
		t.Fatalf("normal and private rooms must be visible; got %v", got)
	}
	if got[withheld.ID.String()] {
		t.Fatal("a highly sensitive room appeared in the listing")
	}
	// The total is the half people forget. A count of three over a list of
	// two announces the existence of the third.
	if total != 2 {
		t.Fatalf("total = %v, want 2; a total that counts what the list withholds "+
			"announces exactly what the withholding is for", total)
	}
}

// TestTheSurfaceHasNoSensitivityOptIn is the catch for the whole privacy
// posture: not that the default is safe, but that there is no parameter to
// change it. Every spelling a caller might reach for is tried.
func TestTheSurfaceHasNoSensitivityOptIn(t *testing.T) {
	e := newEnv(t)
	s := e.mount()

	room := e.room(e.mine, "Ateliê", domain.SensitivityNormal)
	withheldRoom := e.room(e.mine, "Terapia "+surfaceCanary, domain.SensitivityHighlySensitive)
	e.artifact(e.mine, domain.ArtifactNote, "Segredo "+surfaceCanary, domain.SensitivityHighlySensitive, &room.ID)

	attempts := []string{
		"?include_highly_sensitive=true",
		"?include_highly_sensitive=1",
		"?includeHighlySensitive=true",
		"?sensitivity=highly_sensitive",
		"?include_sensitive=true",
	}

	for _, q := range attempts {
		rooms, roomTotal := s.as(e.mine, "/palace/rooms"+q).
			requireStatus(t, http.StatusOK, "rooms"+q).
			listOf(t, "rooms"+q)
		if ids(rooms, "room_id")[withheldRoom.ID.String()] || roomTotal != 1 {
			t.Errorf("GET /palace/rooms%s admitted withheld content (total=%v)", q, roomTotal)
		}

		artifacts, artifactTotal := s.as(e.mine, "/palace/artifacts?room_id="+room.ID.String()+"&"+strings.TrimPrefix(q, "?")).
			requireStatus(t, http.StatusOK, "artifacts"+q).
			listOf(t, "artifacts"+q)
		if len(artifacts) != 0 || artifactTotal != 0 {
			t.Errorf("GET /palace/artifacts%s admitted withheld content (total=%v)", q, artifactTotal)
		}
	}
}

func TestAHighlySensitiveEntityIsNotFoundByDeepLink(t *testing.T) {
	e := newEnv(t)
	s := e.mount()

	room := e.room(e.mine, "Terapia "+surfaceCanary, domain.SensitivityHighlySensitive)
	visible := e.room(e.mine, "Ateliê", domain.SensitivityNormal)
	artifact := e.artifact(e.mine, domain.ArtifactNote, "Segredo "+surfaceCanary, domain.SensitivityHighlySensitive, &visible.ID)
	memory := e.memoryAbout(e.mine, "algo muito privado "+surfaceCanary, domain.SensitivityHighlySensitive, &visible.ID, nil)

	fabricated := uuid.New()

	for _, tc := range []struct {
		what     string
		withheld string
		real     uuid.UUID
		missing  string
	}{
		{"room", "/palace/rooms/" + room.ID.String(), room.ID, "/palace/rooms/" + fabricated.String()},
		{"artifact", "/palace/artifacts/" + artifact.ID.String(), artifact.ID, "/palace/artifacts/" + fabricated.String()},
		{"memory", "/palace/memories/" + memory.ID.String(), memory.ID, "/palace/memories/" + fabricated.String()},
	} {
		res := s.as(e.mine, tc.withheld).requireStatus(t, http.StatusNotFound, tc.what+" deep link")
		if strings.Contains(res.body, surfaceCanary) {
			t.Errorf("%s: the refusal quoted withheld content: %s", tc.what, res.body)
		}
		if strings.Contains(res.body, "forbidden") || strings.Contains(res.body, "403") {
			t.Errorf("%s: the refusal admits the row exists: %s", tc.what, res.body)
		}

		// The refusal has to be the SAME refusal a fabricated id gets, body
		// included. A status code that matches while the message differs is
		// still a probe: send an id, read the wording, learn whether the row
		// is there. The ids are blanked so two answers that differ only in
		// the id they echo compare equal.
		missing := s.as(e.mine, tc.missing).
			requireStatus(t, http.StatusNotFound, tc.what+" fabricated")
		gotBody := normalizeID(res.body, tc.real)
		wantBody := normalizeID(missing.body, fabricated)
		if gotBody != wantBody {
			t.Errorf("%s: a withheld row answers %q and a fabricated id answers %q; "+
				"the difference is a way to confirm the row exists", tc.what, gotBody, wantBody)
		}
	}
}

/* ══════════════════════════════════════════════════════════════════════
   C · Visual containment inherits visibility, not sensitivity
   ══════════════════════════════════════════════════════════════════════ */

// TestAnArtifactInAWithheldRoomIsNotFoundEvenThoughItIsNormal is the
// decision this slice exists to make real. The artifact says `normal`
// about itself, and it is still withheld, because its container is.
func TestAnArtifactInAWithheldRoomIsNotFoundEvenThoughItIsNormal(t *testing.T) {
	e := newEnv(t)
	s := e.mount()

	withheldRoom := e.room(e.mine, "Terapia", domain.SensitivityHighlySensitive)
	artifact := e.artifact(e.mine, domain.ArtifactList, "Lista comum "+surfaceCanary,
		domain.SensitivityNormal, &withheldRoom.ID)

	// The artifact's own level really is normal. If this ever fails, the
	// test below is proving something else.
	if artifact.Sensitivity != domain.SensitivityNormal {
		t.Fatalf("seed: artifact sensitivity is %q, want normal", artifact.Sensitivity)
	}

	res := s.as(e.mine, "/palace/artifacts/"+artifact.ID.String()).
		requireStatus(t, http.StatusNotFound, "artifact in a withheld room")
	if strings.Contains(res.body, surfaceCanary) {
		t.Fatalf("the refusal quoted the withheld title: %s", res.body)
	}

	// And the room itself is unreachable, so there is no listing that could
	// have shown it either.
	s.as(e.mine, "/palace/artifacts?room_id="+withheldRoom.ID.String()).
		requireStatus(t, http.StatusNotFound, "listing scoped to a withheld room")
}

// TestMovingAnArtifactToAVisibleRoomMakesItEligibleWithoutRelabelling is
// the other half of the same decision: the rule is about CONTAINMENT, so
// undoing the containment undoes the rule, and nothing about the row was
// edited to achieve it.
func TestMovingAnArtifactToAVisibleRoomMakesItEligibleWithoutRelabelling(t *testing.T) {
	e := newEnv(t)
	s := e.mount()

	withheldRoom := e.room(e.mine, "Terapia", domain.SensitivityHighlySensitive)
	visibleRoom := e.room(e.mine, "Ateliê de Marcenaria", domain.SensitivityNormal)
	artifact := e.artifact(e.mine, domain.ArtifactList, "Lista de presentes",
		domain.SensitivityNormal, &withheldRoom.ID)

	s.as(e.mine, "/palace/artifacts/"+artifact.ID.String()).
		requireStatus(t, http.StatusNotFound, "before the move")

	moved, err := e.svc.UpdateArtifact(e.ctx(), e.mine, artifact.ID, domain.ArtifactChange{
		Room: domain.SetRef(visibleRoom.ID),
	})
	if err != nil {
		t.Fatalf("move artifact: %v", err)
	}

	// The sensitivity was not touched. This is the assertion that keeps the
	// policy from quietly becoming a relabelling.
	if moved.Artifact.Sensitivity != domain.SensitivityNormal {
		t.Fatalf("the move changed sensitivity to %q; containment must never relabel",
			moved.Artifact.Sensitivity)
	}

	body := s.as(e.mine, "/palace/artifacts/"+artifact.ID.String()).
		requireStatus(t, http.StatusOK, "after the move").
		decode(t, "after the move")
	if body["title"] != "Lista de presentes" {
		t.Fatalf("after the move the artifact is still not readable: %v", body)
	}
	if body["sensitivity"] != string(domain.SensitivityNormal) {
		t.Fatalf("sensitivity on the wire is %v, want normal", body["sensitivity"])
	}
}

// TestAMemoryAboutAnArtifactInAWithheldRoomIsNotFound is the transitive
// case. The memory is filed in a VISIBLE room and is itself normal; what
// withholds it is the artifact it describes.
func TestAMemoryAboutAnArtifactInAWithheldRoomIsNotFound(t *testing.T) {
	e := newEnv(t)
	s := e.mount()

	withheldRoom := e.room(e.mine, "Terapia", domain.SensitivityHighlySensitive)
	visibleRoom := e.room(e.mine, "Ateliê", domain.SensitivityNormal)
	hidden := e.artifact(e.mine, domain.ArtifactNote, "Nota reservada", domain.SensitivityNormal, &withheldRoom.ID)

	memory := e.memoryAbout(e.mine, "o que eu concluí sobre aquilo "+surfaceCanary,
		domain.SensitivityNormal, &visibleRoom.ID, &hidden.ID)

	res := s.as(e.mine, "/palace/memories/"+memory.ID.String()).
		requireStatus(t, http.StatusNotFound, "memory about a withheld artifact")
	if strings.Contains(res.body, surfaceCanary) {
		t.Fatalf("the refusal quoted the memory: %s", res.body)
	}
}

func TestAMemoryFiledInAWithheldRoomIsNotFound(t *testing.T) {
	e := newEnv(t)
	s := e.mount()

	withheldRoom := e.room(e.mine, "Terapia", domain.SensitivityHighlySensitive)
	memory := e.memory(e.mine, "uma nota comum "+surfaceCanary, domain.SensitivityNormal, &withheldRoom.ID)

	s.as(e.mine, "/palace/memories/"+memory.ID.String()).
		requireStatus(t, http.StatusNotFound, "memory in a withheld room")
}

// TestUnfiledRowsRemainEligible guards the other direction: containment
// only withholds when there IS a container. A row filed nowhere is an
// ordinary state, not a suspicious one.
func TestUnfiledRowsRemainEligible(t *testing.T) {
	e := newEnv(t)
	s := e.mount()

	artifact := e.artifact(e.mine, domain.ArtifactNote, "Ainda sem sala", domain.SensitivityNormal, nil)
	memory := e.memoryAbout(e.mine, "conhecimento sem sala", domain.SensitivityNormal, nil, nil)

	body := s.as(e.mine, "/palace/artifacts/"+artifact.ID.String()).
		requireStatus(t, http.StatusOK, "unfiled artifact").
		decode(t, "unfiled artifact")
	if body["title"] != "Ainda sem sala" {
		t.Fatalf("unfiled artifact not readable: %v", body)
	}
	if body["room_id"] != nil {
		t.Fatalf("room_id = %v, want null", body["room_id"])
	}

	s.as(e.mine, "/palace/memories/"+memory.ID.String()).
		requireStatus(t, http.StatusOK, "unfiled memory")
}

// TestTheCoreIsUnaffectedByTheSurfacePolicy proves the policy is additive.
// The capabilities read through GetArtifact, and they must still see what
// they saw before this slice existed.
func TestTheCoreIsUnaffectedByTheSurfacePolicy(t *testing.T) {
	e := newEnv(t)

	withheldRoom := e.room(e.mine, "Terapia", domain.SensitivityHighlySensitive)
	artifact := e.artifact(e.mine, domain.ArtifactList, "Lista comum", domain.SensitivityNormal, &withheldRoom.ID)

	// The Core read, which is what a tool uses: unchanged, still returns it.
	if _, err := e.svc.GetArtifact(e.ctx(), e.mine, artifact.ID); err != nil {
		t.Fatalf("the Core read stopped working: %v; the surface policy must be additive", err)
	}
	// The surface read, with the surface's own rules: refuses.
	_, err := e.svc.ArtifactForSurface(e.ctx(), e.mine, artifact.ID, ports.Visibility{
		InheritRoomVisibility: true,
	})
	assertNotFound(t, "artifact for surface", err)

	// And a caller that opts into neither rule sees the Core's behaviour,
	// which is what keeps `Visibility{}` from being a trap for a future
	// reader who expects the zero value to mean "no extra rules".
	if _, err := e.svc.ArtifactForSurface(e.ctx(), e.mine, artifact.ID, ports.Visibility{}); err != nil {
		t.Fatalf("with no surface rules the read should behave like the Core: %v", err)
	}
}

/* ══════════════════════════════════════════════════════════════════════
   D · Lifecycle: an absent status means active
   ══════════════════════════════════════════════════════════════════════ */

func TestAnAbsentStatusMeansActiveAndNotBoth(t *testing.T) {
	e := newEnv(t)
	s := e.mount()

	live := e.room(e.mine, "Ateliê", domain.SensitivityNormal)
	retired := e.room(e.mine, "Sala antiga", domain.SensitivityNormal)
	e.archiveRoom(e.mine, retired.ID)

	// The Core's filter returns both when nobody said. The surface must not
	// inherit that: a projection of what somebody is working on does not
	// include what they explicitly retired.
	rows, total := s.as(e.mine, "/palace/rooms").
		requireStatus(t, http.StatusOK, "default listing").
		listOf(t, "default listing")
	got := ids(rows, "room_id")
	if !got[live.ID.String()] {
		t.Fatal("the active room is missing from the default listing")
	}
	if got[retired.ID.String()] {
		t.Fatal("an archived room appeared in a listing that did not ask for one")
	}
	if total != 1 {
		t.Fatalf("total = %v, want 1", total)
	}

	// And archived is still reachable, because "onde foi que eu guardei
	// aquilo" has to keep having an answer.
	rows, total = s.as(e.mine, "/palace/rooms?status=archived").
		requireStatus(t, http.StatusOK, "archived listing").
		listOf(t, "archived listing")
	if !ids(rows, "room_id")[retired.ID.String()] || total != 1 {
		t.Fatalf("archived listing did not return the archived room: %v (total %v)", rows, total)
	}
}

func TestAnArchivedArtifactIsOutOfTheDefaultListing(t *testing.T) {
	e := newEnv(t)
	s := e.mount()

	room := e.room(e.mine, "Dojang", domain.SensitivityNormal)
	live := e.artifact(e.mine, domain.ArtifactProject, "Faixa preta", domain.SensitivityNormal, &room.ID)
	retired := e.artifact(e.mine, domain.ArtifactList, "Equipamento velho", domain.SensitivityNormal, &room.ID)

	archived := domain.LifecycleArchived
	if _, err := e.svc.UpdateArtifact(e.ctx(), e.mine, retired.ID,
		domain.ArtifactChange{Status: &archived}); err != nil {
		t.Fatalf("archive artifact: %v", err)
	}

	rows, total := s.as(e.mine, "/palace/artifacts?room_id="+room.ID.String()).
		requireStatus(t, http.StatusOK, "default artifact listing").
		listOf(t, "default artifact listing")
	got := ids(rows, "artifact_id")
	if !got[live.ID.String()] || got[retired.ID.String()] || total != 1 {
		t.Fatalf("default listing = %v (total %v), want only the active artifact", got, total)
	}

	rows, _ = s.as(e.mine, "/palace/artifacts?room_id="+room.ID.String()+"&status=archived").
		requireStatus(t, http.StatusOK, "archived artifact listing").
		listOf(t, "archived artifact listing")
	if !ids(rows, "artifact_id")[retired.ID.String()] {
		t.Fatal("the archived artifact is unreachable")
	}
}

func TestAnUnknownStatusIsARefusalAndNotAGuess(t *testing.T) {
	e := newEnv(t)
	s := e.mount()

	res := s.as(e.mine, "/palace/rooms?status=archive").
		requireStatus(t, http.StatusBadRequest, "misspelled status")
	if !strings.Contains(res.body, "invalid") {
		t.Fatalf("want an invalid-argument refusal, got %s", res.body)
	}
}

/* ══════════════════════════════════════════════════════════════════════
   E · The artifact listing is room scoped, on purpose
   ══════════════════════════════════════════════════════════════════════ */

// TestTheArtifactListingAcceptsEveryScope replaces the S1 test that
// required `room_id`. The containment rule now lives in the predicate, so
// all three shapes are answerable and all three are safe.
func TestTheArtifactListingAcceptsEveryScope(t *testing.T) {
	e := newEnv(t)
	s := e.mount()

	room := e.room(e.mine, "Ateliê", domain.SensitivityNormal)
	filed := e.artifact(e.mine, domain.ArtifactList, "Ferramentas", domain.SensitivityNormal, &room.ID)
	loose := e.artifact(e.mine, domain.ArtifactNote, "Ainda sem sala", domain.SensitivityNormal, nil)

	all, allTotal := s.as(e.mine, "/palace/artifacts").
		requireStatus(t, http.StatusOK, "unscoped listing").
		listOf(t, "unscoped listing")
	got := ids(all, "artifact_id")
	if !got[filed.ID.String()] || !got[loose.ID.String()] || allTotal != 2 {
		t.Fatalf("unscoped listing = %v (total %v), want both artifacts", got, allTotal)
	}

	scoped, scopedTotal := s.as(e.mine, "/palace/artifacts?room_id="+room.ID.String()).
		requireStatus(t, http.StatusOK, "room scoped").
		listOf(t, "room scoped")
	if !ids(scoped, "artifact_id")[filed.ID.String()] || scopedTotal != 1 {
		t.Fatalf("room scoped = %v (total %v), want only the filed artifact", scoped, scopedTotal)
	}

	unfiled, unfiledTotal := s.as(e.mine, "/palace/artifacts?room_id=none").
		requireStatus(t, http.StatusOK, "unfiled").
		listOf(t, "unfiled")
	if !ids(unfiled, "artifact_id")[loose.ID.String()] || unfiledTotal != 1 {
		t.Fatalf("unfiled = %v (total %v), want only the loose artifact", unfiled, unfiledTotal)
	}

	s.as(e.mine, "/palace/artifacts?room_id=not-a-uuid").
		requireStatus(t, http.StatusBadRequest, "malformed room id")
}

// TestTheMemoryListingIsMountedAndScoped replaces the S1 test that pinned
// its absence. It exists now because the transitive predicate does.
func TestTheMemoryListingIsMountedAndScoped(t *testing.T) {
	e := newEnv(t)
	s := e.mount()

	room := e.room(e.mine, "Ateliê", domain.SensitivityNormal)
	artifact := e.artifact(e.mine, domain.ArtifactList, "Ferramentas", domain.SensitivityNormal, &room.ID)
	about := e.memoryAbout(e.mine, "ela prefere caneca", domain.SensitivityNormal, &room.ID, &artifact.ID)
	loose := e.memoryAbout(e.mine, "conhecimento solto", domain.SensitivityNormal, nil, nil)

	all, total := s.as(e.mine, "/palace/memories").
		requireStatus(t, http.StatusOK, "memory listing").
		listOf(t, "memory listing")
	got := ids(all, "memory_id")
	if !got[about.ID.String()] || !got[loose.ID.String()] || total != 2 {
		t.Fatalf("memory listing = %v (total %v), want both", got, total)
	}

	byArtifact, artifactTotal := s.as(e.mine, "/palace/memories?artifact_id="+artifact.ID.String()).
		requireStatus(t, http.StatusOK, "scoped to an artifact").
		listOf(t, "scoped to an artifact")
	if !ids(byArtifact, "memory_id")[about.ID.String()] || artifactTotal != 1 {
		t.Fatalf("artifact scoped = %v (total %v)", byArtifact, artifactTotal)
	}

	byRoom, roomTotal := s.as(e.mine, "/palace/memories?room_id="+room.ID.String()).
		requireStatus(t, http.StatusOK, "scoped to a room").
		listOf(t, "scoped to a room")
	if !ids(byRoom, "memory_id")[about.ID.String()] || roomTotal != 1 {
		t.Fatalf("room scoped = %v (total %v)", byRoom, roomTotal)
	}

	unfiled, unfiledTotal := s.as(e.mine, "/palace/memories?room_id=none").
		requireStatus(t, http.StatusOK, "unfiled memories").
		listOf(t, "unfiled memories")
	if !ids(unfiled, "memory_id")[loose.ID.String()] || unfiledTotal != 1 {
		t.Fatalf("unfiled memories = %v (total %v)", unfiled, unfiledTotal)
	}
}

func TestNoWriteVerbIsServed(t *testing.T) {
	e := newEnv(t)
	s := e.mount()
	room := e.room(e.mine, "Ateliê", domain.SensitivityNormal)

	for _, method := range []string{http.MethodPost, http.MethodPatch, http.MethodPut, http.MethodDelete} {
		req, err := http.NewRequest(method, s.server.URL+"/palace/rooms/"+room.ID.String(), nil)
		if err != nil {
			t.Fatalf("build request: %v", err)
		}
		req.Header.Set(workspace.HeaderName, e.mine.String())
		res, err := s.server.Client().Do(req)
		if err != nil {
			t.Fatalf("%s: %v", method, err)
		}
		_ = res.Body.Close()
		if res.StatusCode < 400 {
			t.Errorf("%s /palace/rooms/{id} answered %d; this surface is read only",
				method, res.StatusCode)
		}
	}
}

/* ══════════════════════════════════════════════════════════════════════
   F · The DTO boundary, over real responses
   ══════════════════════════════════════════════════════════════════════ */

// TestNoResponseIsARedactedEntity is the behavioural half of the boundary
// the wire types enforce structurally.
//
// A handler that returned the entity instead of the wire type would not
// crash and would not leak: the entity redacts itself, so the response
// would carry an id and the word `redacted` and nothing else. That is a
// SAFE failure and a silent one. This makes it loud.
func TestNoResponseIsARedactedEntity(t *testing.T) {
	e := newEnv(t)
	s := e.mount()

	room := e.room(e.mine, "Ateliê de Marcenaria", domain.SensitivityNormal)
	artifact := e.artifact(e.mine, domain.ArtifactList, "Lista de presentes", domain.SensitivityNormal, &room.ID)
	e.item(e.mine, artifact.ID, "caneca azul")
	memory := e.memoryAbout(e.mine, "ela prefere caneca a xícara", domain.SensitivityNormal, &room.ID, &artifact.ID)

	for _, tc := range []struct {
		path string
		want string
	}{
		{"/palace/rooms", "Ateliê de Marcenaria"},
		{"/palace/rooms/" + room.ID.String(), "Ateliê de Marcenaria"},
		{"/palace/artifacts?room_id=" + room.ID.String(), "Lista de presentes"},
		{"/palace/artifacts/" + artifact.ID.String(), "caneca azul"},
		{"/palace/memories/" + memory.ID.String(), "ela prefere caneca a xícara"},
	} {
		res := s.as(e.mine, tc.path).requireStatus(t, http.StatusOK, tc.path)
		if strings.Contains(res.body, `"redacted"`) {
			t.Errorf("GET %s returned a redacted entity instead of a wire type: %s",
				tc.path, res.body)
		}
		if !strings.Contains(res.body, tc.want) {
			t.Errorf("GET %s did not carry %q: %s", tc.path, tc.want, res.body)
		}
	}
}

/* ══════════════════════════════════════════════════════════════════════
   G · Paging
   ══════════════════════════════════════════════════════════════════════ */

func TestAListingReportsItsWindowAndItsUnboundedTotal(t *testing.T) {
	e := newEnv(t)
	s := e.mount()

	for i := 0; i < 5; i++ {
		e.room(e.mine, "Sala "+string(rune('A'+i)), domain.SensitivityNormal)
	}

	body := s.as(e.mine, "/palace/rooms?limit=2&offset=1").
		requireStatus(t, http.StatusOK, "windowed listing").
		decode(t, "windowed listing")

	if body["limit"] != float64(2) || body["offset"] != float64(1) {
		t.Fatalf("the window is not echoed back: %v", body)
	}
	if body["total"] != float64(5) {
		t.Fatalf("total = %v, want 5; the total must ignore the window", body["total"])
	}
	if got := len(body["items"].([]any)); got != 2 {
		t.Fatalf("got %d items, want 2", got)
	}
}

// TestANegativeOffsetIsClampedRatherThanFatal guards a real failure mode:
// a negative OFFSET reaches Postgres as a syntax-level refusal and would
// surface as a 500 for what is a typo in a query string.
func TestANegativeOffsetIsClampedRatherThanFatal(t *testing.T) {
	e := newEnv(t)
	s := e.mount()
	e.room(e.mine, "Ateliê", domain.SensitivityNormal)

	body := s.as(e.mine, "/palace/rooms?offset=-5").
		requireStatus(t, http.StatusOK, "negative offset").
		decode(t, "negative offset")
	if body["offset"] != float64(0) {
		t.Fatalf("offset = %v, want 0", body["offset"])
	}
}

// TestArtifactEntriesPaginateRatherThanTruncate is the entry-level version
// of the same honesty rule: a checklist longer than the ceiling is the
// operator's work, and dropping its tail silently would make the surface
// lie about how much there is.
func TestArtifactEntriesPaginateRatherThanTruncate(t *testing.T) {
	e := newEnv(t)
	s := e.mount()

	room := e.room(e.mine, "Dojang", domain.SensitivityNormal)
	artifact := e.artifact(e.mine, domain.ArtifactList, "Equipamentos", domain.SensitivityNormal, &room.ID)
	const entries = 7
	for i := 0; i < entries; i++ {
		e.item(e.mine, artifact.ID, "item "+string(rune('a'+i)))
	}

	body := s.as(e.mine, "/palace/artifacts/"+artifact.ID.String()+"?item_limit=3&item_offset=3").
		requireStatus(t, http.StatusOK, "paged entries").
		decode(t, "paged entries")

	if body["item_total"] != float64(entries) {
		t.Fatalf("item_total = %v, want %d", body["item_total"], entries)
	}
	items, ok := body["items"].([]any)
	if !ok || len(items) != 3 {
		t.Fatalf("got %v entries, want 3", body["items"])
	}
	if body["item_offset"] != float64(3) {
		t.Fatalf("item_offset = %v, want 3", body["item_offset"])
	}
}

/* ══════════════════════════════════════════════════════════════════════
   H · Nothing this surface can be made to fail at reports content
   ══════════════════════════════════════════════════════════════════════ */

func TestNoFailureOnThisSurfaceEchoesStoredContent(t *testing.T) {
	e := newEnv(t)
	s := e.mount()

	room := e.room(e.mine, canary, domain.SensitivityHighlySensitive)
	artifact := e.artifact(e.mine, domain.ArtifactNote, canary, domain.SensitivityHighlySensitive, nil)
	memory := e.memory(e.mine, canary, domain.SensitivityHighlySensitive, nil)

	for _, path := range []string{
		"/palace/rooms/" + room.ID.String(),
		"/palace/artifacts/" + artifact.ID.String(),
		"/palace/memories/" + memory.ID.String(),
		"/palace/artifacts?room_id=" + room.ID.String(),
		"/palace/rooms?status=nonsense",
		"/palace/artifacts?room_id=" + room.ID.String() + "&kind=nonsense",
		"/palace/rooms/not-a-uuid",
	} {
		res := s.as(e.mine, path)
		if strings.Contains(res.body, canary) {
			t.Errorf("GET %s echoed stored content: %s", path, res.body)
		}
	}

	if strings.Contains(e.logs.String(), canary) {
		t.Error("stored content reached the logs")
	}
}
