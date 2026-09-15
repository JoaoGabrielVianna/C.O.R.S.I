//go:build integration

// Palace, slice S2: rooms and memories against a real Postgres.
//
//	Run with: TEST_POSTGRES_DSN=... go test -tags=integration ./internal/palace/...
//
// The sentences this suite has to make convincing:
//
//	One workspace cannot see, reach, move or reference another's rows,
//	and every way of failing to find something gives the same answer.
//
//	Highly sensitive content stays out of a listing that did not ask for
//	it, and out of the count beside it.
//
//	Nothing this context can be made to fail at reports the content it
//	was failing on.
//
//	The relation matrix in Go and the CHECK in Postgres accept and reject
//	exactly the same shapes.
//
// What is REAL here: the Palace schema, domain, repositories and
// application service, and Postgres. What is absent: tools, agents and
// HTTP, because slice S2 has none. There is nothing faked.
//
// ── Why this file lives in internal/palace and not in app ──────────────
// Because the outermost thing it exercises is the application service,
// and the suite also has to reach the raw database for the two checks
// that are about the schema itself: the sanitised error and the relation
// matrix. A directory whose only Go file is a build-tagged test is not
// matched by an untagged `./...`, so this costs nothing when the tag is
// off, and the file is where the next slice's wiring will want it.
package palace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/corsi/backend/internal/palace/adapters/repo"
	"github.com/corsi/backend/internal/palace/app"
	"github.com/corsi/backend/internal/palace/domain"
	"github.com/corsi/backend/internal/palace/ports"
	"github.com/corsi/backend/internal/platform/postgres"
	"github.com/corsi/backend/internal/platform/testdb"
)

/* ── harness ─────────────────────────────────────────────────────────── */

// canary is content that must never come back out of an error or a log.
const canary = "CANARY-DO-NOT-LEAK-a7f3"

// ── Why this suite runs in a database of its own ──────────────────────
//
// It drops and re-applies the `palace` schema before every test.
// Resetting a schema in the shared database would pull it out from under
// whichever suite is running concurrently in another process. So it
// creates its own database, once, and resets inside it: the same
// arrangement, and the same reasoning, as the Threads and Job Radar
// suites.
const privateDBName = "corsi_test_palace"

var suiteDSN string

func TestMain(m *testing.M) {
	admin := os.Getenv("TEST_POSTGRES_DSN")
	if admin == "" {
		// Nothing to set up; every test skips individually.
		os.Exit(m.Run())
	}

	d, cleanup, err := createPrivateDB(admin)
	if err != nil {
		panic("palace suite: " + err.Error())
	}
	suiteDSN = d

	code := m.Run()
	cleanup()
	os.Exit(code)
}

func createPrivateDB(admin string) (string, func(), error) {
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		return "", nil, err
	}
	defer func() { _ = conn.Close(ctx) }()

	// Dropped first: a previous run killed mid-way leaves it behind, and
	// a stale database would be worse than no isolation at all.
	if _, err := conn.Exec(ctx, `DROP DATABASE IF EXISTS `+privateDBName+` WITH (FORCE)`); err != nil {
		return "", nil, err
	}
	if _, err := conn.Exec(ctx, `CREATE DATABASE `+privateDBName); err != nil {
		return "", nil, err
	}

	u, err := url.Parse(admin)
	if err != nil {
		return "", nil, err
	}
	u.Path = "/" + privateDBName
	target := u.String()

	// This suite provisions its own database, so it is the party that
	// knows the database is disposable, and it says so at the moment it
	// creates it. That is what lets dsn() refuse anything else without
	// carving out an exception for this package.
	if err := testdb.Mark(ctx, target); err != nil {
		return "", nil, err
	}

	return target, func() {
		c, err := pgx.Connect(context.Background(), admin)
		if err != nil {
			return
		}
		defer func() { _ = c.Close(context.Background()) }()
		_, _ = c.Exec(context.Background(), `DROP DATABASE IF EXISTS `+privateDBName+` WITH (FORCE)`)
	}, nil
}

// dsn is where the suite learns which database it may destroy. This is
// the only way to obtain one, so no destructive statement can be reached
// without it.
func dsn(t *testing.T) string {
	if suiteDSN == "" {
		t.Skip("TEST_POSTGRES_DSN not set; skipping integration test")
	}
	testdb.AssertDestructible(t, suiteDSN)
	return suiteDSN
}

// migrateDSN points golang-migrate at this context's own version table.
//
// Omitting the table is the documented way to break a new module: the
// runner would read finance's table, find it populated, and conclude
// there is nothing to apply, creating no schema at all.
func migrateDSN(t *testing.T, d, table string) string {
	t.Helper()
	for _, p := range []string{"postgres://", "postgresql://"} {
		if strings.HasPrefix(d, p) {
			d = "pgx5://" + strings.TrimPrefix(d, p)
			break
		}
	}
	u, err := url.Parse(d)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	q := u.Query()
	q.Set("x-migrations-table", table)
	u.RawQuery = q.Encode()
	return u.String()
}

// freshDB drops the schema this suite touches and migrates it forward.
func freshDB(t *testing.T, d string) {
	t.Helper()
	ctx := context.Background()

	conn, err := pgx.Connect(ctx, d)
	if err != nil {
		t.Fatalf("connect for reset: %v", err)
	}
	for _, stmt := range []string{
		`DROP SCHEMA IF EXISTS palace CASCADE`,
		`DROP TABLE IF EXISTS schema_migrations_palace`,
	} {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			_ = conn.Close(ctx)
			t.Fatalf("reset (%s): %v", stmt, err)
		}
	}
	if err := conn.Close(ctx); err != nil {
		t.Fatalf("close reset conn: %v", err)
	}

	m, err := migrate.New("file://../../migrations/palace", migrateDSN(t, d, "schema_migrations_palace"))
	if err != nil {
		t.Fatalf("migrate.New: %v", err)
	}
	defer func() { _, _ = m.Close() }()
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate up: %v", err)
	}
}

type env struct {
	t    *testing.T
	pool *pgxpool.Pool

	svc   *app.Service
	repos *repo.Repos

	// logs captures everything the service writes, so a test can assert
	// that content never reached it.
	logs *strings.Builder

	// Two workspaces, because one workspace cannot prove isolation.
	// `mine` is the caller in every test; `theirs` is the neighbour whose
	// rows must be unreachable.
	mine   uuid.UUID
	theirs uuid.UUID
}

func newEnv(t *testing.T) *env {
	t.Helper()
	d := dsn(t)
	freshDB(t, d)

	// Eight connections rather than four, because one test starts eight
	// sessions concurrently to prove that the race produces an answer
	// instead of a unique violation. With a smaller pool the goroutines
	// queue on a connection and the race the test exists for never
	// happens, which would leave it passing while proving nothing.
	pool, err := postgres.Open(context.Background(), postgres.Config{DSN: d, MaxConns: 8})
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)

	var logs strings.Builder
	log := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	repos := repo.New(pool)
	return &env{
		t:     t,
		pool:  pool,
		repos: repos,
		// MustNewService rather than NewService: the Deps below is complete
		// by construction, and a failure would be a bug in this harness
		// rather than anything a test can cause. TestTheServiceRefusesAn
		// IncompleteDeps is where the refusal itself is proven.
		svc: app.MustNewService(app.Deps{
			Rooms:      repos.Rooms,
			Memories:   repos.Memories,
			Artifacts:  repos.Artifacts,
			Items:      repos.Items,
			Sources:    repos.Sources,
			Provenance: repos.Provenance,
			Relations:  repos.Relations,
			Sessions:   repos.Sessions,
			Clock:      repos.Clock,
			Logger:     log,
		}),
		logs:   &logs,
		mine:   uuid.New(),
		theirs: uuid.New(),
	}
}

func (e *env) ctx() context.Context { return context.Background() }

// room creates one room in a workspace, through the application service,
// which is the same path any caller uses.
func (e *env) room(ws uuid.UUID, name string, level domain.Sensitivity) *domain.Room {
	e.t.Helper()
	r, err := e.svc.CreateRoom(e.ctx(), ws, app.CreateRoomInput{
		Name:        name,
		Sensitivity: &level,
	})
	if err != nil {
		e.t.Fatalf("seed room: %v", err)
	}
	return r
}

func (e *env) memory(ws uuid.UUID, content string, level domain.Sensitivity, roomID *uuid.UUID) *domain.Memory {
	e.t.Helper()
	m, err := e.svc.CreateMemory(e.ctx(), ws, app.CreateMemoryInput{
		Kind:        domain.MemoryFact,
		Content:     content,
		Sensitivity: &level,
		RoomID:      roomID,
	})
	if err != nil {
		e.t.Fatalf("seed memory: %v", err)
	}
	return m
}

// assertNotFound requires this context's not-found, which is the one
// answer three different absences share.
func assertNotFound(t *testing.T, what string, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: want not_found, got success", what)
	}
	var de *domain.Error
	if !errors.As(err, &de) {
		t.Fatalf("%s: error is %T, want *domain.Error: %v", what, err, err)
	}
	if de.Kind != domain.KindNotFound {
		t.Fatalf("%s: kind = %q, want %q (%v)", what, de.Kind, domain.KindNotFound, err)
	}
}

/* ══════════════════════════════════════════════════════════════════════
   A · One workspace cannot reach another's rows
   ══════════════════════════════════════════════════════════════════════ */

func TestANeighboursRoomIsIndistinguishableFromOneThatNeverExisted(t *testing.T) {
	e := newEnv(t)
	theirs := e.room(e.theirs, "A sala deles", domain.SensitivityNormal)
	fabricated := uuid.New()

	// The two failures must be the same failure. Any difference between
	// them, a kind, a wording, a code, is a way to confirm that somebody
	// else's row exists.
	_, errReal := e.svc.GetRoom(e.ctx(), e.mine, theirs.ID)
	_, errFake := e.svc.GetRoom(e.ctx(), e.mine, fabricated)

	assertNotFound(t, "neighbour's room", errReal)
	assertNotFound(t, "fabricated id", errFake)

	realMsg := strings.Replace(errReal.Error(), theirs.ID.String(), "<id>", 1)
	fakeMsg := strings.Replace(errFake.Error(), fabricated.String(), "<id>", 1)
	if realMsg != fakeMsg {
		t.Errorf("the two answers differ:\n  real: %s\n  fake: %s", realMsg, fakeMsg)
	}
}

func TestANeighboursMemoryIsIndistinguishableFromOneThatNeverExisted(t *testing.T) {
	e := newEnv(t)
	theirs := e.memory(e.theirs, "A memória deles", domain.SensitivityNormal, nil)
	fabricated := uuid.New()

	_, errReal := e.svc.GetMemory(e.ctx(), e.mine, theirs.ID)
	_, errFake := e.svc.GetMemory(e.ctx(), e.mine, fabricated)

	assertNotFound(t, "neighbour's memory", errReal)
	assertNotFound(t, "fabricated id", errFake)

	realMsg := strings.Replace(errReal.Error(), theirs.ID.String(), "<id>", 1)
	fakeMsg := strings.Replace(errFake.Error(), fabricated.String(), "<id>", 1)
	if realMsg != fakeMsg {
		t.Errorf("the two answers differ:\n  real: %s\n  fake: %s", realMsg, fakeMsg)
	}
}

func TestANeighboursRowsAreAbsentFromEveryListing(t *testing.T) {
	e := newEnv(t)
	e.room(e.theirs, "A sala deles", domain.SensitivityNormal)
	e.memory(e.theirs, "A memória deles", domain.SensitivityNormal, nil)
	mine := e.room(e.mine, "A minha sala", domain.SensitivityNormal)

	rooms, roomTotal, err := e.svc.ListRooms(e.ctx(), e.mine, ports.RoomFilter{})
	if err != nil {
		t.Fatalf("list rooms: %v", err)
	}
	if len(rooms) != 1 || rooms[0].ID != mine.ID {
		t.Errorf("the room listing returned %d rows, want only this workspace's", len(rooms))
	}
	if roomTotal != 1 {
		t.Errorf("room total = %d, want 1", roomTotal)
	}

	memories, memTotal, err := e.svc.ListMemories(e.ctx(), e.mine, ports.MemoryFilter{})
	if err != nil {
		t.Fatalf("list memories: %v", err)
	}
	if len(memories) != 0 || memTotal != 0 {
		t.Errorf("the memory listing returned %d rows and a total of %d, want none",
			len(memories), memTotal)
	}
}

func TestANeighboursRoomCannotBeEdited(t *testing.T) {
	e := newEnv(t)
	theirs := e.room(e.theirs, "A sala deles", domain.SensitivityNormal)

	archived := domain.LifecycleArchived
	_, err := e.svc.UpdateRoom(e.ctx(), e.mine, theirs.ID, domain.RoomChange{Status: &archived})
	assertNotFound(t, "editing a neighbour's room", err)

	// And it really was not touched. An UPDATE that matched zero rows and
	// reported success is the failure this asserts against.
	after, err := e.svc.GetRoom(e.ctx(), e.theirs, theirs.ID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if after.Status != domain.LifecycleActive {
		t.Errorf("the neighbour's room was archived by a stranger: %q", after.Status)
	}
}

func TestANeighboursMemoryCannotBeEdited(t *testing.T) {
	e := newEnv(t)
	theirs := e.memory(e.theirs, "A memória deles", domain.SensitivityNormal, nil)

	replacement := "reescrito por um estranho"
	_, err := e.svc.UpdateMemory(e.ctx(), e.mine, theirs.ID, domain.MemoryChange{Content: &replacement})
	assertNotFound(t, "editing a neighbour's memory", err)

	after, err := e.svc.GetMemory(e.ctx(), e.theirs, theirs.ID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if after.Content != "A memória deles" {
		t.Errorf("the neighbour's memory was rewritten by a stranger")
	}
}

/* ══════════════════════════════════════════════════════════════════════
   B · A reference is resolved in the caller's workspace
   ══════════════════════════════════════════════════════════════════════ */

func TestAMemoryCannotBeFiledInANeighboursRoom(t *testing.T) {
	e := newEnv(t)
	theirs := e.room(e.theirs, "A sala deles", domain.SensitivityNormal)
	fabricated := uuid.New()

	_, errReal := e.svc.CreateMemory(e.ctx(), e.mine, app.CreateMemoryInput{
		Kind: domain.MemoryFact, Content: "tentativa", RoomID: &theirs.ID,
	})
	_, errFake := e.svc.CreateMemory(e.ctx(), e.mine, app.CreateMemoryInput{
		Kind: domain.MemoryFact, Content: "tentativa", RoomID: &fabricated,
	})

	// Not a constraint violation, not a database error: the same
	// not-found a fabricated id gets. The composite foreign key would
	// also refuse the row, and its refusal has a different shape, which
	// is exactly what somebody would use to probe for the existence of a
	// neighbour's room.
	assertNotFound(t, "filing into a neighbour's room", errReal)
	assertNotFound(t, "filing into a fabricated room", errFake)

	realMsg := strings.Replace(errReal.Error(), theirs.ID.String(), "<id>", 1)
	fakeMsg := strings.Replace(errFake.Error(), fabricated.String(), "<id>", 1)
	if realMsg != fakeMsg {
		t.Errorf("the two answers differ:\n  real: %s\n  fake: %s", realMsg, fakeMsg)
	}
}

func TestAMemoryCannotBeMovedIntoANeighboursRoom(t *testing.T) {
	e := newEnv(t)
	theirs := e.room(e.theirs, "A sala deles", domain.SensitivityNormal)
	mine := e.memory(e.mine, "minha memória", domain.SensitivityNormal, nil)

	_, err := e.svc.UpdateMemory(e.ctx(), e.mine, mine.ID,
		domain.MemoryChange{Room: domain.SetRef(theirs.ID)})
	assertNotFound(t, "moving into a neighbour's room", err)

	after, err := e.svc.GetMemory(e.ctx(), e.mine, mine.ID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if after.RoomID != nil {
		t.Errorf("the memory was filed into a neighbour's room: %v", after.RoomID)
	}
}

func TestAMemoryIsFiledInItsOwnWorkspacesRoom(t *testing.T) {
	e := newEnv(t)
	mine := e.room(e.mine, "Carreira", domain.SensitivityNormal)

	m := e.memory(e.mine, "uma decisão", domain.SensitivityNormal, &mine.ID)
	if m.RoomID == nil || *m.RoomID != mine.ID {
		t.Fatalf("the memory was not filed: %v", m.RoomID)
	}

	// And the listing can narrow by it.
	got, total, err := e.svc.ListMemories(e.ctx(), e.mine, ports.MemoryFilter{RoomID: &mine.ID})
	if err != nil {
		t.Fatalf("list by room: %v", err)
	}
	if len(got) != 1 || total != 1 {
		t.Errorf("listing by room returned %d rows, total %d, want 1 and 1", len(got), total)
	}
}

func TestDetachingARoomNeedsNoResolution(t *testing.T) {
	// Letting go of a reference names no row, so there is nothing to
	// resolve and nothing that could belong to somebody else.
	e := newEnv(t)
	mine := e.room(e.mine, "Carreira", domain.SensitivityNormal)
	m := e.memory(e.mine, "uma decisão", domain.SensitivityNormal, &mine.ID)

	res, err := e.svc.UpdateMemory(e.ctx(), e.mine, m.ID,
		domain.MemoryChange{Room: domain.ClearRef()})
	if err != nil {
		t.Fatalf("detach: %v", err)
	}
	if !res.RoomChanged || res.Memory.RoomID != nil {
		t.Errorf("the memory was not detached: %v", res.Memory.RoomID)
	}
}

/* ══════════════════════════════════════════════════════════════════════
   C · Highly sensitive content stays out of a broad listing
   ══════════════════════════════════════════════════════════════════════ */

func TestNormalAndPrivateAreVisibleAndHighlySensitiveIsNot(t *testing.T) {
	e := newEnv(t)
	normal := e.room(e.mine, "Carreira", domain.SensitivityNormal)
	private := e.room(e.mine, "Finanças", domain.SensitivityPrivate)
	hidden := e.room(e.mine, "Terapia", domain.SensitivityHighlySensitive)

	rooms, total, err := e.svc.ListRooms(e.ctx(), e.mine, ports.RoomFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	seen := map[uuid.UUID]bool{}
	for _, r := range rooms {
		seen[r.ID] = true
	}
	if !seen[normal.ID] {
		t.Error("a normal room was withheld")
	}
	if !seen[private.ID] {
		t.Error("a PRIVATE room was withheld; private is visible to its own workspace")
	}
	if seen[hidden.ID] {
		t.Error("a highly sensitive room appeared in a listing that did not ask for it")
	}

	// ── The count must agree with the list ─────────────────────────────
	// A total of 3 above a list of 2 announces the existence of the row
	// being withheld, which is the leak the withholding exists to stop.
	if total != 2 {
		t.Errorf("total = %d, want 2; the count does not apply the same rule as the list", total)
	}
}

func TestTheOptInAdmitsHighlySensitiveIntoBothTheListAndTheCount(t *testing.T) {
	e := newEnv(t)
	e.room(e.mine, "Carreira", domain.SensitivityNormal)
	hidden := e.room(e.mine, "Terapia", domain.SensitivityHighlySensitive)

	rooms, total, err := e.svc.ListRooms(e.ctx(), e.mine, ports.RoomFilter{
		Sensitive: ports.Sensitive{IncludeHighlySensitive: true},
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rooms) != 2 || total != 2 {
		t.Fatalf("with the opt-in: %d rows, total %d, want 2 and 2", len(rooms), total)
	}

	var found bool
	for _, r := range rooms {
		if r.ID == hidden.ID {
			found = true
		}
	}
	if !found {
		t.Error("the opt-in did not admit the highly sensitive room")
	}
}

func TestTheSameRuleGovernsMemories(t *testing.T) {
	e := newEnv(t)
	e.memory(e.mine, "uma nota comum", domain.SensitivityNormal, nil)
	e.memory(e.mine, "uma nota pessoal", domain.SensitivityPrivate, nil)
	e.memory(e.mine, "uma nota muito pessoal", domain.SensitivityHighlySensitive, nil)

	got, total, err := e.svc.ListMemories(e.ctx(), e.mine, ports.MemoryFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 || total != 2 {
		t.Errorf("default listing: %d rows, total %d, want 2 and 2", len(got), total)
	}

	got, total, err = e.svc.ListMemories(e.ctx(), e.mine, ports.MemoryFilter{
		Sensitive: ports.Sensitive{IncludeHighlySensitive: true},
	})
	if err != nil {
		t.Fatalf("list with opt-in: %v", err)
	}
	if len(got) != 3 || total != 3 {
		t.Errorf("opted-in listing: %d rows, total %d, want 3 and 3", len(got), total)
	}
}

func TestTheDefaultFilterWithholdsWithoutBeingAsked(t *testing.T) {
	// The zero value is the safe answer. A surface built later that
	// forgets this field withholds the most private content rather than
	// publishing it.
	var f ports.MemoryFilter
	if f.IncludeHighlySensitive {
		t.Fatal("the zero filter opts in")
	}

	e := newEnv(t)
	e.memory(e.mine, "muito pessoal", domain.SensitivityHighlySensitive, nil)

	got, total, err := e.svc.ListMemories(e.ctx(), e.mine, f)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 0 || total != 0 {
		t.Errorf("the zero filter returned %d rows and a total of %d", len(got), total)
	}
}

func TestAnIdStillReadsAHighlySensitiveRow(t *testing.T) {
	// The rule is about broad listings. A caller holding an id already
	// knows the row exists, and withholding it would be a puzzle rather
	// than a protection.
	e := newEnv(t)
	hidden := e.memory(e.mine, "muito pessoal", domain.SensitivityHighlySensitive, nil)

	got, err := e.svc.GetMemory(e.ctx(), e.mine, hidden.ID)
	if err != nil {
		t.Fatalf("a highly sensitive memory could not be read by id: %v", err)
	}
	if got.Content != "muito pessoal" {
		t.Errorf("content = %q", got.Content)
	}
}

func TestTheOptInDoesNotReachAcrossWorkspaces(t *testing.T) {
	// The most dangerous combination: the flag that admits the most
	// private content, used by the wrong workspace.
	e := newEnv(t)
	e.room(e.theirs, "A terapia deles", domain.SensitivityHighlySensitive)
	e.memory(e.theirs, "a nota deles", domain.SensitivityHighlySensitive, nil)

	rooms, roomTotal, err := e.svc.ListRooms(e.ctx(), e.mine, ports.RoomFilter{
		Sensitive: ports.Sensitive{IncludeHighlySensitive: true},
	})
	if err != nil {
		t.Fatalf("list rooms: %v", err)
	}
	memories, memTotal, err := e.svc.ListMemories(e.ctx(), e.mine, ports.MemoryFilter{
		Sensitive: ports.Sensitive{IncludeHighlySensitive: true},
	})
	if err != nil {
		t.Fatalf("list memories: %v", err)
	}

	if len(rooms) != 0 || roomTotal != 0 || len(memories) != 0 || memTotal != 0 {
		t.Errorf("the opt-in crossed a workspace boundary: %d rooms (total %d), %d memories (total %d)",
			len(rooms), roomTotal, len(memories), memTotal)
	}
}

/* ══════════════════════════════════════════════════════════════════════
   D · Updates that change nothing
   ══════════════════════════════════════════════════════════════════════ */

func TestAnEmptyUpdateIsRefusedBeforeAnythingIsRead(t *testing.T) {
	e := newEnv(t)
	r := e.room(e.mine, "Carreira", domain.SensitivityNormal)
	m := e.memory(e.mine, "uma nota", domain.SensitivityNormal, nil)

	_, err := e.svc.UpdateRoom(e.ctx(), e.mine, r.ID, domain.RoomChange{})
	assertInvalid(t, "empty room update", err)

	_, err = e.svc.UpdateMemory(e.ctx(), e.mine, m.ID, domain.MemoryChange{})
	assertInvalid(t, "empty memory update", err)
}

func TestAnEmptyUpdateIsRefusedEvenForARowThatDoesNotExist(t *testing.T) {
	// Refused BEFORE the read, so the answer says what is wrong with the
	// request rather than sending the caller looking for a row.
	e := newEnv(t)
	_, err := e.svc.UpdateRoom(e.ctx(), e.mine, uuid.New(), domain.RoomChange{})
	assertInvalid(t, "empty update of a missing room", err)
}

func TestAnEditThatMovesNothingDoesNotTouchTheClock(t *testing.T) {
	// `updated_at` orders every listing. An edit that did nothing and
	// stamped it would announce itself as the most recent work.
	e := newEnv(t)
	r := e.room(e.mine, "Carreira", domain.SensitivityNormal)
	before := r.UpdatedAt

	time.Sleep(10 * time.Millisecond)

	same := "Carreira"
	res, err := e.svc.UpdateRoom(e.ctx(), e.mine, r.ID, domain.RoomChange{Name: &same})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if !res.Unchanged() {
		t.Fatalf("resending the stored name reported a change: %+v", res.RoomChangeResult)
	}

	after, err := e.svc.GetRoom(e.ctx(), e.mine, r.ID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !after.UpdatedAt.Equal(before) {
		t.Errorf("updated_at moved on a no-op: %v to %v", before, after.UpdatedAt)
	}
}

func TestARealEditMovesTheClockAndReportsBothSides(t *testing.T) {
	e := newEnv(t)
	r := e.room(e.mine, "Carreira", domain.SensitivityNormal)
	before := r.UpdatedAt

	time.Sleep(10 * time.Millisecond)

	private := domain.SensitivityPrivate
	res, err := e.svc.UpdateRoom(e.ctx(), e.mine, r.ID, domain.RoomChange{Sensitivity: &private})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if !res.SensitivityChanged || res.PreviousSensitivity != domain.SensitivityNormal {
		t.Errorf("the move was misreported: %+v", res.RoomChangeResult)
	}
	if !res.Room.UpdatedAt.After(before) {
		t.Errorf("updated_at did not move: %v to %v", before, res.Room.UpdatedAt)
	}

	// And it is now withheld from a default listing, which is the whole
	// point of the edit.
	rooms, total, err := e.svc.ListRooms(e.ctx(), e.mine, ports.RoomFilter{
		Sensitive: ports.Sensitive{IncludeHighlySensitive: false},
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rooms) != 1 || total != 1 {
		t.Errorf("a private room should still be visible: %d rows, total %d", len(rooms), total)
	}
}

/* ══════════════════════════════════════════════════════════════════════
   E · Nothing reports the content it failed on
   ══════════════════════════════════════════════════════════════════════ */

func TestAConstraintViolationDoesNotHandOutTheRow(t *testing.T) {
	// ── What is actually being guarded, stated precisely ───────────────
	// A CHECK violation makes Postgres answer with a Message that names
	// the constraint and a DETAIL that is the ENTIRE ROW. Measured
	// against pgx v5.7.2: `err.Error()`, `%+v` and both slog handlers all
	// route through PgError.Error() and never touch Detail, so they do
	// not leak. `json.Marshal(pgErr)` publishes every exported field,
	// Detail included.
	//
	// So the leak is not the printing, it is the REACHABILITY: wrapping
	// with %w leaves the *pgconn.PgError recoverable by errors.As, which
	// is what error-handling code does, and the row is one json.Marshal
	// away from there. A Palace error ends up in
	// `chat.tool_calls.error_message`, which Confidential does not
	// redact, so this context does not get to have that path unreviewed.
	//
	// The repository is called directly, past the domain validation that
	// would normally refuse this, because the CHECK constraints exist
	// precisely as a backstop for the case where validation did not run.
	e := newEnv(t)

	bad := &domain.Memory{
		WorkspaceID: e.mine,
		Kind:        domain.MemoryReflection,
		Content:     canary,
		Summary:     canary,
		Importance:  99, // outside 1..5; the CHECK will fire
		Confidence:  domain.DefaultConfidence,
		Status:      domain.LifecycleActive,
		Sensitivity: domain.SensitivityHighlySensitive,
	}

	err := e.repos.Memories.Create(e.ctx(), e.mine, bad)
	if err == nil {
		t.Fatal("the database accepted an importance of 99")
	}

	// ── 1. The guarantee: nothing above can get the PgError back ───────
	var leaked *pgconn.PgError
	if errors.As(err, &leaked) {
		t.Errorf("the repository handed out a *pgconn.PgError; its Detail is %q", leaked.Detail)
	}

	// ── 2. And the obvious surfaces are clean ──────────────────────────
	if strings.Contains(err.Error(), canary) {
		t.Errorf("the error text carried the row's content: %v", err)
	}
	if raw, mErr := json.Marshal(err.Error()); mErr == nil && strings.Contains(string(raw), canary) {
		t.Errorf("the serialised error carried the row's content: %s", raw)
	}

	// ── 3. What it must still say, or nobody can debug it ──────────────
	if !strings.Contains(err.Error(), "memories_importance_check") {
		t.Errorf("the error does not name the rule that was broken: %v", err)
	}

	// ── 4. The guard is not vacuous ────────────────────────────────────
	// Run the same statement raw and confirm that Postgres really does
	// put the content where we say it does. If this ever stops being
	// true, the guard above can be reconsidered on evidence instead of
	// being kept out of habit.
	_, rawErr := e.pool.Exec(e.ctx(), `
		INSERT INTO palace.memories (workspace_id, kind, content, importance)
		VALUES ($1, 'reflection', $2, 99)`, e.mine, canary)
	var direct *pgconn.PgError
	if !errors.As(rawErr, &direct) {
		t.Fatalf("the raw statement did not produce a PgError: %v", rawErr)
	}
	if !strings.Contains(direct.Detail, canary) {
		t.Errorf("Postgres no longer puts the row in DETAIL (%q); re-examine safeDBError", direct.Detail)
	}
	if payload, mErr := json.Marshal(direct); mErr == nil && !strings.Contains(string(payload), canary) {
		t.Errorf("marshalling a raw PgError no longer publishes the row; re-examine safeDBError")
	}
}

func TestNoApplicationFailurePathCarriesContent(t *testing.T) {
	e := newEnv(t)
	theirs := e.room(e.theirs, "A sala deles", domain.SensitivityNormal)

	// Each of these fails for a different reason, and every one of them
	// is handed content worth leaking.
	cases := map[string]func() error{
		"invalid memory": func() error {
			_, err := e.svc.CreateMemory(e.ctx(), e.mine, app.CreateMemoryInput{
				Kind: "grudge", Content: canary, Summary: canary,
			})
			return err
		},
		"content too long": func() error {
			_, err := e.svc.CreateMemory(e.ctx(), e.mine, app.CreateMemoryInput{
				Kind:    domain.MemoryFact,
				Content: canary + strings.Repeat("x", domain.MaxMemoryContent),
			})
			return err
		},
		"foreign room": func() error {
			_, err := e.svc.CreateMemory(e.ctx(), e.mine, app.CreateMemoryInput{
				Kind: domain.MemoryFact, Content: canary, RoomID: &theirs.ID,
			})
			return err
		},
		"invalid room": func() error {
			_, err := e.svc.CreateRoom(e.ctx(), e.mine, app.CreateRoomInput{Name: ""})
			return err
		},
		"room name too long": func() error {
			_, err := e.svc.CreateRoom(e.ctx(), e.mine, app.CreateRoomInput{
				Name: canary + strings.Repeat("x", domain.MaxRoomName),
			})
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
}

func TestNothingTheServiceLogsCarriesContent(t *testing.T) {
	// A guard rather than a discovery: this layer logs nothing today. It
	// is here so the first log line somebody adds is checked by a test
	// that already exists, rather than by whoever happens to review it.
	e := newEnv(t)

	r := e.room(e.mine, canary, domain.SensitivityHighlySensitive)
	e.memory(e.mine, canary, domain.SensitivityHighlySensitive, &r.ID)

	summary := canary
	if _, err := e.svc.UpdateMemory(e.ctx(), e.mine, uuid.New(),
		domain.MemoryChange{Summary: &summary}); err == nil {
		t.Fatal("want a failure for a missing memory")
	}
	_, _, _ = e.svc.ListMemories(e.ctx(), e.mine, ports.MemoryFilter{
		Search: canary, Sensitive: ports.Sensitive{IncludeHighlySensitive: true},
	})

	if strings.Contains(e.logs.String(), canary) {
		t.Errorf("the service logged content: %s", e.logs.String())
	}
}

func TestAnEntityHandedToAnEncoderStillRedactsItself(t *testing.T) {
	// The last line of defence, checked end to end: an entity that came
	// out of Postgres, not one built in a test, must redact itself the
	// same way. A repository that returned some other type, or a scan
	// that filled a different struct, would quietly lose this.
	e := newEnv(t)
	m := e.memory(e.mine, canary, domain.SensitivityHighlySensitive, nil)

	loaded, err := e.svc.GetMemory(e.ctx(), e.mine, m.ID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	raw, err := json.Marshal(loaded)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), canary) {
		t.Errorf("a memory loaded from Postgres serialised its content: %s", raw)
	}
	if strings.Contains(fmt.Sprintf("%v", loaded), canary) {
		t.Errorf("a memory loaded from Postgres formatted its content")
	}
}

/* ══════════════════════════════════════════════════════════════════════
   F · The workspace argument is the authority
   ══════════════════════════════════════════════════════════════════════ */

func TestARepositoryRefusesAnEntityFromAnotherWorkspace(t *testing.T) {
	// The bug this catches: a service that loaded an entity for one
	// workspace and saved it while serving another. Both lines look
	// correct on their own, and the result is somebody's private memory
	// filed in a stranger's palace.
	e := newEnv(t)
	mine := e.memory(e.mine, "minha", domain.SensitivityNormal, nil)

	err := e.repos.Memories.Update(e.ctx(), e.theirs, mine)
	if err == nil {
		t.Fatal("the repository saved an entity under a workspace that does not own it")
	}
	if !strings.Contains(err.Error(), "different workspace") {
		t.Errorf("the refusal does not say what went wrong: %v", err)
	}
	// Neither workspace id is in the message: which two operators were
	// confused is a fact about two people, and this error goes somewhere
	// this package cannot see.
	for _, id := range []uuid.UUID{e.mine, e.theirs} {
		if strings.Contains(err.Error(), id.String()) {
			t.Errorf("the refusal named a workspace: %v", err)
		}
	}
}

func TestARepositoryRefusesAnAbsentWorkspace(t *testing.T) {
	e := newEnv(t)
	r := &domain.Room{Name: "x", Status: domain.LifecycleActive, Sensitivity: domain.SensitivityNormal}

	if err := e.repos.Rooms.Create(e.ctx(), uuid.Nil, r); err == nil {
		t.Fatal("the repository wrote a room with no workspace")
	}
}

/* ══════════════════════════════════════════════════════════════════════
   G · The clock is the database's
   ══════════════════════════════════════════════════════════════════════ */

func TestTheClockIsThePostgresClockAndNotThisProcess(t *testing.T) {
	e := newEnv(t)

	now, err := e.svc.Now(e.ctx())
	if err != nil {
		t.Fatalf("clock: %v", err)
	}
	if now.IsZero() {
		t.Fatal("the clock returned the zero instant")
	}

	// Compared against the same authority every row is stamped from: a
	// room written moments earlier cannot have been created after the
	// clock was read.
	r := e.room(e.mine, "Carreira", domain.SensitivityNormal)
	after, err := e.svc.Now(e.ctx())
	if err != nil {
		t.Fatalf("clock: %v", err)
	}
	if r.CreatedAt.Before(now) {
		t.Errorf("a row written after the clock was read is stamped before it: %v < %v",
			r.CreatedAt, now)
	}
	if r.CreatedAt.After(after) {
		t.Errorf("a row is stamped after a clock read that followed it: %v > %v",
			r.CreatedAt, after)
	}
}

/* ══════════════════════════════════════════════════════════════════════
   H · The relation matrix, in Go and in Postgres, from one table
   ══════════════════════════════════════════════════════════════════════ */

// relationShapeCases is the third statement both copies of the matrix are
// measured against.
//
// ── Why the table is written out here rather than derived ──────────────
// Deriving it from `relationMatrix` would prove only that Postgres agrees
// with Go, which is worth having and is not the whole requirement: a
// shape dropped from BOTH would then pass silently. Written out, the
// suite also fails when either copy stops supporting a shape this version
// is supposed to support.
//
// It is the same set as the table in domain/relation_test.go, and it has
// to be: if the two drift, one of the two suites goes red.
var relationShapeCases = map[string]bool{
	"related_to:room→room":         true,
	"related_to:artifact→artifact": true,
	"related_to:memory→memory":     true,
	"related_to:memory→artifact":   true,
	"decision_for:memory→artifact": true,
	"decision_for:memory→room":     true,
	"mentions:memory→artifact":     true,
	"mentions:artifact→artifact":   true,
	"supersedes:memory→memory":     true,
	"supersedes:artifact→artifact": true,
}

func TestTheDomainMatrixAndTheDatabaseCheckAcceptExactlyTheSameShapes(t *testing.T) {
	// ── Why Relation can be exercised before it has a repository ───────
	// Because `palace.relations` carries no foreign key: the endpoints
	// are polymorphic and Postgres cannot reference one of three tables.
	// So a row can be inserted with fabricated endpoint ids, and what the
	// database is being asked is exactly the question this test has: does
	// `relations_shape` admit this pairing. Nothing about the row's
	// meaning is being tested, and nothing of Relation is being
	// implemented ahead of its slice.
	e := newEnv(t)
	ws := e.mine

	accepted := 0
	for _, kind := range domain.RelationKinds {
		for _, from := range domain.EntityTypes {
			for _, to := range domain.EntityTypes {
				key := string(kind) + ":" + string(from) + "→" + string(to)
				want := relationShapeCases[key]

				// The Go answer.
				rel := &domain.Relation{
					WorkspaceID: ws,
					FromType:    from,
					FromID:      uuid.New(),
					Kind:        kind,
					ToType:      to,
					ToID:        uuid.New(),
				}
				goErr := rel.Validate()

				// The Postgres answer, from the same case.
				_, sqlErr := e.pool.Exec(e.ctx(), `
					INSERT INTO palace.relations
						(workspace_id, from_type, from_id, kind, to_type, to_id)
					VALUES ($1, $2, $3, $4, $5, $6)`,
					ws, string(from), rel.FromID, string(kind), string(to), rel.ToID)

				goOK, sqlOK := goErr == nil, sqlErr == nil

				if goOK != sqlOK {
					t.Errorf("%s: the domain says %v and Postgres says %v (go: %v, sql: %v)",
						key, goOK, sqlOK, goErr, sqlErr)
				}
				if goOK != want {
					t.Errorf("%s: the domain accepted=%v, want %v (%v)", key, goOK, want, goErr)
				}
				if sqlOK != want {
					t.Errorf("%s: Postgres accepted=%v, want %v (%v)", key, sqlOK, want, sqlErr)
				}
				if !sqlOK && !strings.Contains(fmt.Sprint(sqlErr), "relations_shape") {
					t.Errorf("%s: Postgres refused it for the wrong reason: %v", key, sqlErr)
				}
				if want {
					accepted++
				}
			}
		}
	}

	if accepted != len(relationShapeCases) {
		t.Fatalf("walked %d accepted shapes, the table declares %d",
			accepted, len(relationShapeCases))
	}
	if total := len(domain.RelationKinds) * len(domain.EntityTypes) * len(domain.EntityTypes); total != 36 {
		t.Fatalf("the vocabularies changed size: %d combinations, want 36", total)
	}
}

func TestBothCopiesRefuseAReflexiveRelation(t *testing.T) {
	e := newEnv(t)
	id := uuid.New()

	rel := &domain.Relation{
		WorkspaceID: e.mine,
		FromType:    domain.EntityMemory, FromID: id,
		Kind:   domain.RelationSupersedes,
		ToType: domain.EntityMemory, ToID: id,
	}
	if err := rel.Validate(); err == nil {
		t.Error("the domain accepted a relation pointing at itself")
	}

	_, err := e.pool.Exec(e.ctx(), `
		INSERT INTO palace.relations (workspace_id, from_type, from_id, kind, to_type, to_id)
		VALUES ($1, 'memory', $2, 'supersedes', 'memory', $2)`, e.mine, id)
	if err == nil {
		t.Error("Postgres accepted a relation pointing at itself")
	}
	if err != nil && !strings.Contains(err.Error(), "relations_not_reflexive") {
		t.Errorf("Postgres refused it for the wrong reason: %v", err)
	}
}

/* ══════════════════════════════════════════════════════════════════════
   I · The column the application layer does not offer yet
   ══════════════════════════════════════════════════════════════════════ */

func TestTheRepositoryRoundTripsAnArtifactReferenceItIsNotYetGiven(t *testing.T) {
	// CreateMemoryInput has no ArtifactID, because there is no Artifact
	// repository to resolve one against and this context does not accept
	// a reference it cannot resolve in the caller's workspace.
	//
	// The repository, however, must already read and write the column, or
	// the day Artifact arrives every read is silently lossy. The artifact
	// row here is a raw fixture, not an implementation: it exists so the
	// composite foreign key has something to point at.
	e := newEnv(t)

	artifactID := uuid.New()
	if _, err := e.pool.Exec(e.ctx(), `
		INSERT INTO palace.artifacts (id, workspace_id, kind, title)
		VALUES ($1, $2, 'note', 'fixture')`, artifactID, e.mine); err != nil {
		t.Fatalf("seed artifact fixture: %v", err)
	}

	m := &domain.Memory{
		WorkspaceID: e.mine,
		Kind:        domain.MemoryFact,
		Content:     "uma nota sobre o artefato",
		Importance:  domain.DefaultImportance,
		Confidence:  domain.DefaultConfidence,
		Status:      domain.LifecycleActive,
		Sensitivity: domain.SensitivityNormal,
		ArtifactID:  &artifactID,
	}
	if err := e.repos.Memories.Create(e.ctx(), e.mine, m); err != nil {
		t.Fatalf("create: %v", err)
	}

	loaded, err := e.svc.GetMemory(e.ctx(), e.mine, m.ID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if loaded.ArtifactID == nil || *loaded.ArtifactID != artifactID {
		t.Errorf("artifact_id did not survive the round trip: %v", loaded.ArtifactID)
	}
}

func TestAMemoryCannotReferenceANeighboursArtifactEvenThroughTheRepository(t *testing.T) {
	// The composite foreign key is the backstop under the application
	// layer's resolution. With no Artifact repository yet, it is the only
	// thing standing between a fabricated id and a cross-workspace
	// reference, so it is worth proving it holds.
	e := newEnv(t)

	theirArtifact := uuid.New()
	if _, err := e.pool.Exec(e.ctx(), `
		INSERT INTO palace.artifacts (id, workspace_id, kind, title)
		VALUES ($1, $2, 'note', 'fixture')`, theirArtifact, e.theirs); err != nil {
		t.Fatalf("seed artifact fixture: %v", err)
	}

	m := &domain.Memory{
		WorkspaceID: e.mine,
		Kind:        domain.MemoryFact,
		Content:     canary,
		Importance:  domain.DefaultImportance,
		Confidence:  domain.DefaultConfidence,
		Status:      domain.LifecycleActive,
		Sensitivity: domain.SensitivityNormal,
		ArtifactID:  &theirArtifact,
	}
	err := e.repos.Memories.Create(e.ctx(), e.mine, m)
	if err == nil {
		t.Fatal("a memory was filed against a neighbour's artifact")
	}
	if strings.Contains(err.Error(), canary) {
		t.Errorf("the refusal carried the memory's content: %v", err)
	}
	if !strings.Contains(err.Error(), "memories_artifact_fk") {
		t.Errorf("the refusal does not name the rule that held: %v", err)
	}
}

/* ══════════════════════════════════════════════════════════════════════
   J · Listing mechanics
   ══════════════════════════════════════════════════════════════════════ */

func TestAListingIsNarrowedByKindStatusImportanceAndText(t *testing.T) {
	e := newEnv(t)
	high := 5
	if _, err := e.svc.CreateMemory(e.ctx(), e.mine, app.CreateMemoryInput{
		Kind: domain.MemoryDecision, Content: "parei de aceitar reunião cedo",
		Importance: &high,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	e.memory(e.mine, "uma ideia solta", domain.SensitivityNormal, nil)

	decision := domain.MemoryDecision
	got, total, err := e.svc.ListMemories(e.ctx(), e.mine, ports.MemoryFilter{Kind: &decision})
	if err != nil {
		t.Fatalf("by kind: %v", err)
	}
	if len(got) != 1 || total != 1 {
		t.Errorf("by kind: %d rows, total %d, want 1", len(got), total)
	}

	got, _, err = e.svc.ListMemories(e.ctx(), e.mine, ports.MemoryFilter{MinImportance: 5})
	if err != nil {
		t.Fatalf("by importance: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("by importance floor: %d rows, want 1", len(got))
	}

	got, _, err = e.svc.ListMemories(e.ctx(), e.mine, ports.MemoryFilter{Search: "REUNIÃO"})
	if err != nil {
		t.Fatalf("by search: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("case-insensitive search: %d rows, want 1", len(got))
	}

	archived := domain.LifecycleArchived
	got, _, err = e.svc.ListMemories(e.ctx(), e.mine, ports.MemoryFilter{Status: &archived})
	if err != nil {
		t.Fatalf("by status: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("nothing is archived yet, got %d rows", len(got))
	}
}

func TestALimitIsBoundedRatherThanRefused(t *testing.T) {
	// A caller asking for more than the ceiling wants as much as it can
	// have, and an error would teach it nothing it can act on.
	e := newEnv(t)
	for i := 0; i < 3; i++ {
		e.memory(e.mine, fmt.Sprintf("nota %d", i), domain.SensitivityNormal, nil)
	}

	got, total, err := e.svc.ListMemories(e.ctx(), e.mine, ports.MemoryFilter{
		Page: ports.Page{Limit: 100000},
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 3 || total != 3 {
		t.Errorf("%d rows, total %d, want 3", len(got), total)
	}
}

func TestATruncatedListingStillReportsTheWholeTotal(t *testing.T) {
	// What stops a model that received a page believing it saw
	// everything.
	e := newEnv(t)
	for i := 0; i < 4; i++ {
		e.memory(e.mine, fmt.Sprintf("nota %d", i), domain.SensitivityNormal, nil)
	}

	got, total, err := e.svc.ListMemories(e.ctx(), e.mine, ports.MemoryFilter{
		Page: ports.Page{Limit: 2},
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("%d rows, want 2", len(got))
	}
	if total != 4 {
		t.Errorf("total = %d, want 4", total)
	}
}

func TestArchivingKeepsTheRowReadable(t *testing.T) {
	// Archiving is the organising act, and it is not deletion: "o que eu
	// guardava em março" has to keep having an answer.
	e := newEnv(t)
	r := e.room(e.mine, "Carreira", domain.SensitivityNormal)

	archived := domain.LifecycleArchived
	if _, err := e.svc.UpdateRoom(e.ctx(), e.mine, r.ID, domain.RoomChange{Status: &archived}); err != nil {
		t.Fatalf("archive: %v", err)
	}

	got, err := e.svc.GetRoom(e.ctx(), e.mine, r.ID)
	if err != nil {
		t.Fatalf("an archived room became unreadable: %v", err)
	}
	if got.Status != domain.LifecycleArchived {
		t.Errorf("status = %q", got.Status)
	}

	// And an unfiltered listing still shows it: hiding retired rooms
	// would make "onde é que eu guardei aquilo" unanswerable.
	rooms, _, err := e.svc.ListRooms(e.ctx(), e.mine, ports.RoomFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rooms) != 1 {
		t.Errorf("an archived room disappeared from an unfiltered listing")
	}
}

/* ── shared assertion ────────────────────────────────────────────────── */

func assertInvalid(t *testing.T, what string, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: want invalid, got success", what)
	}
	var de *domain.Error
	if !errors.As(err, &de) {
		t.Fatalf("%s: error is %T, want *domain.Error: %v", what, err, err)
	}
	if de.Kind != domain.KindInvalid {
		t.Errorf("%s: kind = %q, want %q", what, de.Kind, domain.KindInvalid)
	}
}
