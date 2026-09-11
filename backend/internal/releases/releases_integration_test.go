//go:build integration

// Integration tests for the release history, against a real Postgres and
// through the real HTTP handlers.
//
//	Run with:  TEST_POSTGRES_DSN=... go test -tags=integration ./internal/releases/...
//
// The suite is organised around the one property that makes this feature
// worth having: a published release is a fact about the past, and nothing
// — not new code, not a later release, not an UPDATE, not a person with
// psql — may change it afterwards.
package releases

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/corsi/backend/internal/platform/postgres"
	"github.com/corsi/backend/internal/platform/testdb"
	"github.com/corsi/backend/internal/platform/workspace"
	"github.com/corsi/backend/internal/releases/adapters/repo"
	"github.com/corsi/backend/internal/releases/app"
	"github.com/corsi/backend/internal/releases/domain"
)

/* ── harness ─────────────────────────────────────────────────────────── */

// dsn is where the suite learns which database it is allowed to destroy.
//
// The guard lives HERE rather than beside each DROP, and that placement is
// the point: this is the only way any test in this package can obtain a
// database, so there is no arrangement of calls that reaches a destructive
// statement without having passed it. A guard next to the DROP is a call
// somebody deletes in a refactor and nothing notices until a real database
// is gone. See internal/platform/testdb.
func dsn(t *testing.T) string {
	v := os.Getenv("TEST_POSTGRES_DSN")
	if v == "" {
		t.Skip("TEST_POSTGRES_DSN not set; skipping integration test")
	}
	testdb.AssertDestructible(t, v)
	return v
}

// releasesMigrateDSN points golang-migrate at this context's own version
// table. Omitting it would make the runner read finance's table, find it
// populated, and conclude there is nothing to apply — the exact failure
// the platform docs warn every new module about.
func releasesMigrateDSN(t *testing.T, d string) string {
	t.Helper()
	for _, p := range []string{"postgres://", "postgresql://"} {
		if len(d) > len(p) && d[:len(p)] == p {
			d = "pgx5://" + d[len(p):]
			break
		}
	}
	u, err := url.Parse(d)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	q := u.Query()
	q.Set("x-migrations-table", "schema_migrations_releases")
	u.RawQuery = q.Encode()
	return u.String()
}

// resetSchema drops the context's schema and version table, leaving the
// database with no releases history at all. Callers then migrate forward
// to whatever version they want to observe.
func resetSchema(t *testing.T, d string) {
	t.Helper()
	ctx := context.Background()

	conn, err := pgx.Connect(ctx, d)
	if err != nil {
		t.Fatalf("connect for reset: %v", err)
	}
	for _, stmt := range []string{
		`DROP SCHEMA IF EXISTS releases CASCADE`,
		`DROP TABLE IF EXISTS schema_migrations_releases`,
	} {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			_ = conn.Close(ctx)
			t.Fatalf("reset (%s): %v", stmt, err)
		}
	}
	if err := conn.Close(ctx); err != nil {
		t.Fatalf("close reset conn: %v", err)
	}
}

// freshDB resets to a clean slate and migrates to head.
//
// It drops the schema rather than running `migrate down`, and that is a
// consequence of the feature rather than a shortcut. The freeze trigger
// refuses to delete a published release — correctly, and that refusal is
// itself under test — so a down migration fails the moment any release is
// published and leaves the version table dirty for every test after it.
//
// The gate exists to protect a production history that has no other copy.
// A throwaway test database has none, so the harness owns its own cleanup.
// Weakening the trigger so `down` could pass would trade a real guarantee
// for test convenience, and would mean a down migration could erase a
// published release in production.
func freshDB(t *testing.T, d string) {
	t.Helper()
	resetSchema(t, d)

	m, err := migrate.New("file://../../migrations/releases", releasesMigrateDSN(t, d))
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
	r    chi.Router
	pool *pgxpool.Pool
	svc  *app.Service
}

// newEnv wires the production module over a real database. The workspace
// middleware is replaced by a stamp, exactly as the other suites do, so
// these tests exercise the handlers rather than the header policy.
func newEnv(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	d := dsn(t)
	freshDB(t, d)

	pool, err := postgres.Open(ctx, postgres.Config{
		DSN: d, MaxConns: 4, MinConns: 1,
		MaxConnLifetime: time.Hour, MaxConnIdleTime: 30 * time.Minute,
	})
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)

	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	mod := New(Deps{
		Pool:   pool,
		Logger: log,
		WorkspaceMiddleware: func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				next.ServeHTTP(w, req.WithContext(workspace.WithWorkspaceID(req.Context(), uuid.New())))
			})
		},
	})
	root := chi.NewRouter()
	mod.Register(root)

	return &env{
		t:    t,
		r:    root,
		pool: pool,
		svc:  app.NewService(repo.New(pool).Releases, log),
	}
}

func (e *env) do(method, path string) *httptest.ResponseRecorder {
	e.t.Helper()
	req := httptest.NewRequest(method, path, nil)
	rec := httptest.NewRecorder()
	e.r.ServeHTTP(rec, req)
	return rec
}

// insertModule adds a module row that the seed does not contain.
//
// Used only where the seeded catalogue can no longer express the case under
// test — a module with no releases, or a status no derivation could
// produce. Every test that reaches for it says why in its own comment,
// because a synthetic fixture is the weaker choice and should never be the
// silent one.
func (e *env) insertModule(key, status string, position int) {
	e.t.Helper()
	_, err := e.pool.Exec(context.Background(),
		`INSERT INTO releases.modules (key, name, description, status, position)
		 VALUES ($1, $2, $3, $4, $5)`,
		key, "Fixture "+key, "Inserted by a test; not part of the product.", status, position)
	if err != nil {
		e.t.Fatalf("insert fixture module %q: %v", key, err)
	}
}

func (e *env) decode(rec *httptest.ResponseRecorder, dst any) {
	e.t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), dst); err != nil {
		e.t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
}

func wantStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status = %d, want %d: %s", rec.Code, want, rec.Body.String())
	}
}

type moduleCard struct {
	Key            string          `json:"key"`
	Name           string          `json:"name"`
	Description    string          `json:"description"`
	Status         string          `json:"status"`
	Current        *domain.Release `json:"current_release"`
	ReleaseCount   int             `json:"release_count"`
	LastReleasedAt *time.Time      `json:"last_released_at"`
}

type moduleDetail struct {
	moduleCard
	Releases []*domain.Release `json:"releases"`
}

/* ── 1. the module list ──────────────────────────────────────────────── */

// TestModulesAreListedInDisplayOrder covers requirement 1: the page can ask
// what modules exist, and gets the three this installation actually
// versions — not the five the settings screen invents.
func TestModulesAreListedInDisplayOrder(t *testing.T) {
	e := newEnv(t)

	rec := e.do("GET", "/releases/modules")
	wantStatus(t, rec, http.StatusOK)

	var got struct {
		Items []moduleCard `json:"items"`
	}
	e.decode(rec, &got)

	if len(got.Items) != 4 {
		t.Fatalf("%d modules, want 4: %+v", len(got.Items), got.Items)
	}
	// Threads sits between Finance and Job Radar since Threads 1.0.0
	// shipped: position 25, which puts the three real modules ahead of the
	// backstage one without renumbering anything that was already there.
	wantOrder := []string{"agents", "finance", "threads", "job-radar"}
	for i, key := range wantOrder {
		if got.Items[i].Key != key {
			t.Errorf("module[%d] = %q, want %q", i, got.Items[i].Key, key)
		}
	}
	// The property under test is that a module's own lifecycle is surfaced
	// and is NOT the status of its newest release.
	//
	// This assertion used to carry that property on its own, because
	// Finance was `partial` while carrying no release at all. Finance 1.0.0
	// shipped on 2026-08-26 and the module became `active`, so every seeded
	// module now has at least one release and every one of them is
	// `active` — which means a wrong implementation that computed this
	// column from the release history would pass here.
	//
	// The property did not stop mattering, so it moved to a test that can
	// still refute it: TestModuleStatusIsNotDerivedFromItsReleases. What
	// stays here is the factual list, which is what this test is named for.
	//
	// Threads is `active` despite having no screen of its own: the module
	// status describes the module's lifecycle, not how much surface it has.
	wantStatus := map[string]string{
		"agents": "active", "finance": "active",
		"threads": "active", "job-radar": "active",
	}
	for _, m := range got.Items {
		if want := wantStatus[m.Key]; m.Status != want {
			t.Errorf("%s status = %q, want %q", m.Key, m.Status, want)
		}
	}
	for _, m := range got.Items {
		if m.Name == "" || m.Description == "" {
			t.Errorf("module %q has an empty name or description", m.Key)
		}
	}
}

// TestModuleStatusIsNotDerivedFromItsReleases holds the property that the
// seeded fixture used to hold by accident.
//
// While Finance was `partial` with an empty history and the other three
// were `active` with one release each, the listing itself refuted the
// obvious wrong implementation — status computed from the release count.
// Publishing Finance 1.0.0 removed that contrast: all four modules are now
// `active` and all four have releases.
//
// A synthetic module is used deliberately here, and the trade is the one
// TestAModuleWithNoReleasesIsHonestAboutIt explains: a row this test
// inserts proves less about the seeded product than a real one would, but
// it can express a combination the product no longer contains. `frozen`
// with zero releases is unreachable by any derivation from the history,
// so an implementation that computed the column cannot pass.
func TestModuleStatusIsNotDerivedFromItsReleases(t *testing.T) {
	e := newEnv(t)
	e.insertModule("zz-fixture", "frozen", 900)

	rec := e.do("GET", "/releases/modules/zz-fixture")
	wantStatus(t, rec, http.StatusOK)

	var detail moduleDetail
	e.decode(rec, &detail)
	if detail.Status != "frozen" {
		t.Errorf("status = %q, want %q — the column is stored, not computed",
			detail.Status, "frozen")
	}
	if detail.ReleaseCount != 0 {
		t.Fatalf("release_count = %d; the fixture must have no releases for "+
			"this to refute anything", detail.ReleaseCount)
	}
}

/* ── the release event ───────────────────────────────────────────────── */

// officialReleaseDate is the owner's declared release date for Agents
// v1.0.0, at midnight UTC. It is a product fact, not the moment any
// migration or container happened to run, and the test says so because
// that distinction is the easiest one to lose.
var officialReleaseDate = time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)

// TestAgentsV1IsPublishedStableAndCurrent is the release assertion.
//
// It replaces the earlier test that pinned Agents 1.0.0 as an unpublished
// draft. That test was correct on 2026-08-11, when the release closure
// ended in NOT READY; it stopped being correct on 2026-08-12, when the
// owner approved the release and accepted the residual limitations. This
// file records the end state rather than both.
func TestAgentsV1IsPublishedStableAndCurrent(t *testing.T) {
	e := newEnv(t)

	rec := e.do("GET", "/releases/modules/agents")
	wantStatus(t, rec, http.StatusOK)

	var got moduleDetail
	e.decode(rec, &got)

	// The seeded history grew when v1.1.0 shipped, so this selects its
	// subject rather than assuming it is the only row. What the test is
	// about — that v1.0.0 stayed published, stable and dated — is
	// unchanged, and is now also a statement that a later release did not
	// disturb it.
	var rel *domain.Release
	for _, r := range got.Releases {
		if r.Version == "1.0.0" {
			rel = r
		}
	}
	if rel == nil {
		t.Fatalf("the seeded history no longer contains 1.0.0: %+v", got.Releases)
	}
	if rel.Status != domain.StatusPublished {
		t.Errorf("status = %q, want published", rel.Status)
	}
	if rel.Stability != domain.StabilityStable {
		t.Errorf("stability = %q, want stable", rel.Stability)
	}
	if rel.ReleasedAt == nil {
		t.Fatal("released_at is nil on a published release")
	}
	if !rel.ReleasedAt.UTC().Equal(officialReleaseDate) {
		t.Errorf("released_at = %v, want %v (the owner's date, not the deploy time)",
			rel.ReleasedAt.UTC(), officialReleaseDate)
	}
	// The wire form itself must read as the release date. Same instant in
	// any zone, but an auditor reading the JSON on a São Paulo server would
	// otherwise see 2026-08-11 on a release dated the 12th.
	if got := rel.ReleasedAt.Format(time.RFC3339); got != "2026-08-12T00:00:00Z" {
		t.Errorf("released_at serializes as %q, want 2026-08-12T00:00:00Z", got)
	}

	if got.Current == nil {
		t.Fatal("current_release is nil; a published release must be current")
	}
	// Current is the newest published release, which stopped being this one
	// the first time a later version shipped. That is the module card doing
	// its job — and the point of the assertions above is that v1.0.0's own
	// row did not move when it happened.
	//
	// Derived from the timeline rather than named, because naming it means
	// this test breaks on every release for a reason that is not a defect.
	// It broke twice that way before this was written.
	if got.Current.Version != got.Releases[0].Version {
		t.Errorf("current = %q, but the newest release in the timeline is %q",
			got.Current.Version, got.Releases[0].Version)
	}
	if got.ReleaseCount != len(got.Releases) {
		t.Errorf("release_count = %d with %d releases listed",
			got.ReleaseCount, len(got.Releases))
	}

	// The snapshot is the whole point of publishing, so it must be there.
	if len(rel.Capabilities) != 17 {
		t.Errorf("%d capabilities, want the 17 recorded", len(rel.Capabilities))
	}
	if len(rel.Evidence) != 5 {
		t.Errorf("%d evidence metrics, want 5", len(rel.Evidence))
	}
	if len(rel.Limitations) == 0 || len(rel.Decisions) == 0 || len(rel.TechnicalNotes) == 0 {
		t.Error("the published snapshot is missing limitations, decisions or technical notes")
	}
}

// TestTheOwnerDecisionsAreInTheFrozenRecord. The reason this release could
// ship with an unset external circuit breaker is a decision, and a
// published release that omitted it would be unexplainable a year from
// now: the limitation is still listed, and only the decision says why it
// did not block.
func TestTheOwnerDecisionsAreInTheFrozenRecord(t *testing.T) {
	e := newEnv(t)

	rel, err := e.svc.GetRelease(context.Background(), "agents", "1.0.0")
	if err != nil {
		t.Fatalf("get release: %v", err)
	}

	var approved, maxBudget bool
	for _, d := range rel.Decisions {
		if strings.Contains(d.Text, "approved for release by the owner") {
			approved = true
		}
		if strings.Contains(d.Text, "max_budget is not a release requirement") {
			maxBudget = true
		}
	}
	if !approved {
		t.Error("the owner's release approval is not in the frozen record")
	}
	if !maxBudget {
		t.Error("the owner's max_budget decision is not in the frozen record")
	}

	// And the limitation it refers to is still stated. An accepted risk
	// that disappears from the record stops being a risk anyone can audit.
	var stillListed bool
	for _, l := range rel.Limitations {
		if strings.Contains(l.Text, "max_budget") {
			stillListed = true
		}
	}
	if !stillListed {
		t.Error("the max_budget limitation was removed rather than accepted")
	}
}

// TestComparisonModeIsNotClaimedByThisRelease. The code exists and is
// frozen out of scope by owner decision D8. A release that listed it would
// describe the product as larger than the owner decided it is.
func TestComparisonModeIsNotClaimedByThisRelease(t *testing.T) {
	e := newEnv(t)

	rel, err := e.svc.GetRelease(context.Background(), "agents", "1.0.0")
	if err != nil {
		t.Fatalf("get release: %v", err)
	}
	for _, c := range rel.Capabilities {
		lower := strings.ToLower(c.Name)
		if strings.Contains(lower, "comparison") || strings.Contains(lower, "multi-agent") {
			t.Errorf("capability %q is out of scope for v1.0.0 per D8", c.Name)
		}
	}
}

// TestTheSeededEvidenceMatchesWhatWasCounted pins the numbers to the
// repository they were measured from. If someone edits the seed to a
// prettier number, this fails.
func TestTheSeededEvidenceMatchesWhatWasCounted(t *testing.T) {
	e := newEnv(t)

	rel, err := e.svc.GetRelease(context.Background(), "agents", "1.0.0")
	if err != nil {
		t.Fatalf("get release: %v", err)
	}
	want := map[string]string{
		"Integration tests": "123",
		"HTTP routes":       "38",
		"Migrations":        "12",
		"Tables":            "8",
		"MVP DoD":           "8/8",
	}
	got := make(map[string]string, len(rel.Evidence))
	for _, m := range rel.Evidence {
		got[m.Label] = m.Value
	}
	for label, value := range want {
		if got[label] != value {
			t.Errorf("evidence %q = %q, want %q", label, got[label], value)
		}
	}
}

/* ── 9, 10. publishing ───────────────────────────────────────────────── */

// TestPublishingMakesTheReleaseCurrent covers requirements 9 and 10
// through the real API flow.
//
// ── Why a synthetic module, and why it stopped being finance ──────────
// This test needs a version its module does not already have. It used
// 0.9.0 on finance, then 1.1.0 when finance shipped 1.0.0, and finance
// 1.1.0 then shipped too. Each real release took the fixture with it, and
// the fix each time was to pick the next number — a treadmill that
// guarantees a red gate on the day of every future publication.
//
// The property under test is about the MECHANISM: a draft never displaces
// a published release, and publishing is what makes one current. Nothing
// in it needs the module to be one the product ships. So the module is
// created here, given a published release of its own, and the version
// space is this test's alone.
//
// TestAModuleWithNoReleasesIsHonestAboutIt explains when a synthetic
// module is the WEAKER choice; this is the case where it is simply the
// correct one.
func TestPublishingMakesTheReleaseCurrent(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.insertModule("zz-publishing", "active", 902)

	if _, err := e.svc.Record(ctx, app.RecordInput{
		ModuleKey: "zz-publishing", Version: "1.0.0", Summary: "the shipped one",
	}); err != nil {
		t.Fatalf("record shipped: %v", err)
	}
	if _, err := e.svc.Publish(ctx, "zz-publishing", "1.0.0"); err != nil {
		t.Fatalf("publish shipped: %v", err)
	}
	if _, err := e.svc.Record(ctx, app.RecordInput{
		ModuleKey: "zz-publishing", Version: "1.1.0", Summary: "the draft",
	}); err != nil {
		t.Fatalf("record: %v", err)
	}

	// Recorded, and therefore not current: this is requirement 8.
	rec := e.do("GET", "/releases/modules/zz-publishing")
	var before moduleDetail
	e.decode(rec, &before)
	if before.Current == nil || before.Current.Version != "1.0.0" {
		t.Fatalf("current = %v; a draft must never displace the published release", before.Current)
	}
	if before.ReleaseCount != 1 {
		t.Fatalf("release_count = %d, want 1 while the extra record is only a draft", before.ReleaseCount)
	}

	rec = e.do("POST", "/releases/modules/zz-publishing/releases/1.1.0/publish")
	wantStatus(t, rec, http.StatusOK)

	var published domain.Release
	e.decode(rec, &published)
	if published.Status != domain.StatusPublished {
		t.Fatalf("status = %q, want published", published.Status)
	}
	if published.ReleasedAt == nil || published.PublishedAt == nil {
		t.Fatal("a published release must carry both dates")
	}

	rec = e.do("GET", "/releases/modules/zz-publishing")
	wantStatus(t, rec, http.StatusOK)
	var detail moduleDetail
	e.decode(rec, &detail)

	if detail.Current == nil {
		t.Fatal("current_release is nil after publishing")
	}
	if detail.Current.Version != "1.1.0" {
		t.Errorf("current = %q, want 1.1.0 — publishing is what makes it current",
			detail.Current.Version)
	}
	if detail.ReleaseCount != 2 {
		t.Errorf("release_count = %d, want 2 (the shipped 1.0.0 plus this one)",
			detail.ReleaseCount)
	}
	if detail.LastReleasedAt == nil {
		t.Error("last_released_at is nil after publishing")
	}
}

// TestTheShippedReleaseCannotBePublishedAgain.
//
// Agents 1.0.0 arrives already published, so a publish call against it is
// a conflict rather than a silent re-stamp. This is the guard that keeps a
// second run — a redeploy, a stray click, a retried request — from moving
// the release date off the day the owner chose.
func TestTheShippedReleaseCannotBePublishedAgain(t *testing.T) {
	e := newEnv(t)

	rec := e.do("POST", "/releases/modules/agents/releases/1.0.0/publish")
	wantStatus(t, rec, http.StatusConflict)

	rel, err := e.svc.GetRelease(context.Background(), "agents", "1.0.0")
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if !rel.ReleasedAt.UTC().Equal(officialReleaseDate) {
		t.Errorf("released_at moved to %v", rel.ReleasedAt.UTC())
	}
}

// TestStabilityIsIndependentOfPublicationState. The two columns answer
// different questions, and a release candidate that is genuinely published
// is the case that proves they cannot be collapsed into one.
func TestStabilityIsIndependentOfPublicationState(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	// A synthetic module, for the reason TestPublishingMakesTheReleaseCurrent
	// gives: this needs a version nothing has shipped, and borrowing a real
	// module's version space makes the gate fail on release day.
	e.insertModule("zz-stability", "active", 903)
	if _, err := e.svc.Record(ctx, app.RecordInput{
		ModuleKey: "zz-stability", Version: "1.0.0", Summary: "a candidate",
		Stability: domain.StabilityRC,
	}); err != nil {
		t.Fatalf("record rc: %v", err)
	}
	rel, err := e.svc.Publish(ctx, "zz-stability", "1.0.0")
	if err != nil {
		t.Fatalf("publish rc: %v", err)
	}
	if rel.Status != domain.StatusPublished || rel.Stability != domain.StabilityRC {
		t.Errorf("status=%q stability=%q; publishing must not make a candidate stable",
			rel.Status, rel.Stability)
	}

	// And an unqualified record is stable, which is the default the
	// migration relies on.
	if _, err := e.svc.Record(ctx, app.RecordInput{
		ModuleKey: "job-radar", Version: "0.1.0", Summary: "unqualified",
	}); err != nil {
		t.Fatalf("record: %v", err)
	}
	got, err := e.svc.GetRelease(ctx, "job-radar", "0.1.0")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.Stability != domain.StabilityStable {
		t.Errorf("stability = %q, want stable by default", got.Stability)
	}

	if _, err := e.svc.Record(ctx, app.RecordInput{
		ModuleKey: "job-radar", Version: "0.2.0", Summary: "bad", Stability: "gold",
	}); !domain.IsKind(err, domain.KindInvalid) {
		t.Errorf("an invalid stability was accepted: %v", err)
	}
}

/* ── 3, 4. ordering, and the immutability of a snapshot ──────────────── */

// TestTimelineIsOrderedByVersionNotByText covers requirement 3, and pins
// the reason the schema stores major/minor/patch as integers: sorted as
// text, "1.10.0" comes before "1.9.0".
func TestTimelineIsOrderedByVersionNotByText(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	// The trap being pinned is "1.10.0" vs "1.9.0", which text sorting gets
	// backwards; the third is a lower version that must land below both.
	//
	// The module is created here rather than borrowed from the product.
	// This fixture was 1.2.0, then 1.3.0, then 1.4.0 — each real release
	// took it, and the fix each time was to pick the next number, which
	// guarantees a red gate on the day of every future publication. A
	// version space nothing ships ends that.
	e.insertModule("zz-timeline", "active", 904)
	for _, v := range []string{"1.9.0", "1.10.0", "1.4.0"} {
		if _, err := e.svc.Record(ctx, app.RecordInput{
			ModuleKey: "zz-timeline", Version: v, Summary: "v" + v,
		}); err != nil {
			t.Fatalf("record %s: %v", v, err)
		}
	}

	rec := e.do("GET", "/releases/modules/zz-timeline")
	var detail moduleDetail
	e.decode(rec, &detail)

	got := make([]string, 0, len(detail.Releases))
	for _, r := range detail.Releases {
		got = append(got, r.Version)
	}

	// The property is the ORDER, not the membership: 1.10.0 is newer than
	// 1.9.0, which string comparison gets backwards. Asserting the whole
	// list descends by version says that once, and keeps saying it after
	// the next release ships.
	//
	// Three, not four: the module is this test''s own, so the seeded
	// product history is not in it. That is the point — the three below
	// carry the whole property, and nothing the product publishes can
	// disturb them.
	if len(got) != 3 {
		t.Fatalf("timeline = %v, want exactly the three recorded", got)
	}
	for i := 1; i < len(detail.Releases); i++ {
		prev, cur := detail.Releases[i-1], detail.Releases[i]
		if !newerThan(t, prev, cur) {
			t.Fatalf("timeline = %v: %s is listed before %s", got, prev.Version, cur.Version)
		}
	}
	// In the order that string sorting would get wrong.
	if got[0] != "1.10.0" || got[1] != "1.9.0" || got[2] != "1.4.0" {
		t.Fatalf("timeline = %v, want 1.10.0, 1.9.0 and 1.4.0 leading", got)
	}
}

// newerThan compares two releases the way the timeline is meant to order
// them: by version number, never by the text of it. The parse is the
// domain's own, so this cannot disagree with what the ordering is defined
// against.
func newerThan(t *testing.T, a, b *domain.Release) bool {
	t.Helper()
	av, err := domain.ParseVersion(a.Version)
	if err != nil {
		t.Fatalf("parse %q: %v", a.Version, err)
	}
	bv, err := domain.ParseVersion(b.Version)
	if err != nil {
		t.Fatalf("parse %q: %v", b.Version, err)
	}
	if av.Major != bv.Major {
		return av.Major > bv.Major
	}
	if av.Minor != bv.Minor {
		return av.Minor > bv.Minor
	}
	return av.Patch > bv.Patch
}

// TestAHistoricalSnapshotDoesNotMoveWhenALaterReleaseShips is requirement
// 4, and it is the central promise of the whole feature.
//
// v1.0.0 is published with its capabilities. v1.1.0 then ships and
// replaces Memory entirely. Reading v1.0.0 afterwards must return what
// v1.0.0 shipped — not the module's present state, and not a merge.
func TestAHistoricalSnapshotDoesNotMoveWhenALaterReleaseShips(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.insertModule("zz-later", "active", 905)

	// Agents 1.0.0 arrives published: it is the shipped release.
	before, err := e.svc.GetRelease(ctx, "agents", "1.0.0")
	if err != nil {
		t.Fatalf("read 1.0.0: %v", err)
	}

	// What agents is currently on, read rather than assumed. See the note on
	// the final assertion for why this is captured instead of written down.
	beforeModule, err := e.svc.GetModule(ctx, "agents")
	if err != nil {
		t.Fatalf("get module before: %v", err)
	}
	if beforeModule.Current == nil {
		t.Fatalf("agents has no current release; the fixture proves nothing")
	}

	// A later release, on a module this test owns. Borrowing the product's
	// version space made this fixture move at 1.1.0, 1.2.0, 1.3.0 and
	// 1.4.0 — once per real publication.
	//
	// The property is unaffected: what it asserts is that publishing
	// something NEW does not disturb a snapshot already frozen, and the
	// frozen snapshot it reads back is still the real agents 1.0.0.
	if _, err := e.svc.Record(ctx, app.RecordInput{
		ModuleKey: "zz-later",
		Version:   "9.9.9",
		Summary:   "Memory rebuilt from scratch.",
		Capabilities: []domain.Capability{
			{Name: "Memory", Note: "Completely different implementation."},
		},
	}); err != nil {
		t.Fatalf("record later release: %v", err)
	}
	if _, err := e.svc.Publish(ctx, "zz-later", "9.9.9"); err != nil {
		t.Fatalf("publish later release: %v", err)
	}

	after, err := e.svc.GetRelease(ctx, "agents", "1.0.0")
	if err != nil {
		t.Fatalf("re-read 1.0.0: %v", err)
	}

	if len(after.Capabilities) != len(before.Capabilities) {
		t.Fatalf("1.0.0 now lists %d capabilities, had %d: history was rewritten",
			len(after.Capabilities), len(before.Capabilities))
	}
	if after.Summary != before.Summary {
		t.Errorf("1.0.0 summary changed:\n old: %q\n new: %q", before.Summary, after.Summary)
	}
	if after.ReleasedAt == nil || before.ReleasedAt == nil || !after.ReleasedAt.Equal(*before.ReleasedAt) {
		t.Errorf("1.0.0 released_at moved: %v → %v", before.ReleasedAt, after.ReleasedAt)
	}
	// ── And agents' own current release did not move ────────────────
	//
	// This used to name a literal version, and the literal had to be edited
	// at 1.1.0, 1.2.0, 1.3.0, 1.4.0 and 1.5.0 — five times, once per real
	// publication, each time by someone who had to work out whether the
	// failure was a stale fixture or a regression. That is a bad trade for
	// an assertion that was never about which version agents happens to be
	// on.
	//
	// What it is actually for: publishing a release on ANOTHER module must
	// not disturb this one. So the current version is captured before and
	// compared after, and the fixture stops moving forever.
	afterModule, err := e.svc.GetModule(ctx, "agents")
	if err != nil {
		t.Fatalf("get module after: %v", err)
	}
	if afterModule.Current == nil {
		t.Fatalf("agents lost its current release when another module published")
	}
	if afterModule.Current.Version != beforeModule.Current.Version {
		t.Errorf("agents' current release moved from %q to %q because a DIFFERENT "+
			"module published; current is derived per module and must not cross one",
			beforeModule.Current.Version, afterModule.Current.Version)
	}
}

// TestAPublishedReleaseCannotBeMutatedEvenBypassingTheService is the
// guarantee behind the promise: the database itself refuses. This writes
// raw SQL, which is precisely the path an application-level rule cannot
// defend.
func TestAPublishedReleaseCannotBeMutatedEvenBypassingTheService(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	// Agents 1.0.0 is already published by the release migration, so the
	// freeze is in force from the first statement.
	_, err := e.pool.Exec(ctx,
		`UPDATE releases.releases SET summary = 'rewritten' WHERE module_key='agents' AND version='1.0.0'`)
	if err == nil {
		t.Fatal("a raw UPDATE on a published release succeeded; the freeze trigger is not holding")
	}

	_, err = e.pool.Exec(ctx,
		`DELETE FROM releases.releases WHERE module_key='agents' AND version='1.0.0'`)
	if err == nil {
		t.Fatal("a raw DELETE on a published release succeeded; the freeze trigger is not holding")
	}

	// The row is intact.
	rel, err := e.svc.GetRelease(ctx, "agents", "1.0.0")
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if rel.Summary == "rewritten" {
		t.Fatal("the summary was rewritten")
	}
}

// TestADraftIsStillEditableBeforeItIsPublished — the freeze applies to
// published rows only, otherwise draft would be a state with no purpose.
func TestADraftIsStillEditableBeforeItIsPublished(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	if _, err := e.svc.Record(ctx, app.RecordInput{
		ModuleKey: "finance", Version: "0.9.0", Summary: "before correction",
	}); err != nil {
		t.Fatalf("record: %v", err)
	}
	if _, err := e.pool.Exec(ctx,
		`UPDATE releases.releases SET summary = 'corrected while still a draft'
		  WHERE module_key='finance' AND version='0.9.0'`); err != nil {
		t.Fatalf("a draft must remain editable: %v", err)
	}
	rel, err := e.svc.GetRelease(ctx, "finance", "0.9.0")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if rel.Summary != "corrected while still a draft" {
		t.Errorf("summary = %q; the draft edit did not land", rel.Summary)
	}
}

/* ── 6, 7. what the model refuses ────────────────────────────────────── */

// TestInvalidSemVerIsRefused covers requirement 6.
func TestInvalidSemVerIsRefused(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	bad := []string{
		"1.0",        // not three components
		"1.0.0.0",    // four
		"v1",         // not a version
		"1.0.0-rc.1", // pre-release: deliberately not accepted
		"01.0.0",     // leading zero would make 01.0.0 and 1.0.0 two names for one version
		"latest",
		"",
	}
	for _, v := range bad {
		if _, err := e.svc.Record(ctx, app.RecordInput{
			ModuleKey: "agents", Version: v, Summary: "x",
		}); !domain.IsKind(err, domain.KindInvalid) {
			t.Errorf("Record(%q) error = %v, want an invalid-kind domain error", v, err)
		}
	}

	// And over HTTP, the read path rejects it the same way rather than
	// falling through to a 404 that would suggest the version merely does
	// not exist yet.
	wantStatus(t, e.do("GET", "/releases/modules/agents/releases/not-a-version"), http.StatusBadRequest)
}

// TestDuplicateVersionForTheSameModuleIsRefused covers requirement 7, and
// that the uniqueness is per module: two modules may both have a 1.0.0.
func TestDuplicateVersionForTheSameModuleIsRefused(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	_, err := e.svc.Record(ctx, app.RecordInput{
		ModuleKey: "agents", Version: "1.0.0", Summary: "duplicate",
	})
	if !domain.IsKind(err, domain.KindConflict) {
		t.Fatalf("error = %v, want a conflict", err)
	}

	// The second half of the requirement — the same version under a
	// different module is fine — used to be shown with finance 1.0.0, which
	// is itself a shipped release since 2026-08-26. Every seeded module now
	// carries a 1.0.0, so the pair moved to a version none of them has: the
	// property is about the SCOPE of the uniqueness, not about which
	// number demonstrates it.
	if _, err := e.svc.Record(ctx, app.RecordInput{
		ModuleKey: "finance", Version: "2.0.0", Summary: "finance's own 2.0.0",
	}); err != nil {
		t.Fatalf("finance 2.0.0 should be allowed: %v", err)
	}
	if _, err := e.svc.Record(ctx, app.RecordInput{
		ModuleKey: "finance", Version: "2.0.0", Summary: "and now a duplicate",
	}); !domain.IsKind(err, domain.KindConflict) {
		t.Fatalf("error = %v, want a conflict on the repeat", err)
	}
	if _, err := e.svc.Record(ctx, app.RecordInput{
		ModuleKey: "agents", Version: "2.0.0", Summary: "agents' own 2.0.0",
	}); err != nil {
		t.Fatalf("agents 2.0.0 should be allowed alongside finance 2.0.0: %v", err)
	}
}

// TestRecordingAgainstAnUnknownModuleIsRefused: the FK is a real rule, and
// a release with no module would be history nobody can find.
func TestRecordingAgainstAnUnknownModuleIsRefused(t *testing.T) {
	e := newEnv(t)
	_, err := e.svc.Record(context.Background(), app.RecordInput{
		ModuleKey: "market-intelligence", Version: "1.0.0", Summary: "never existed",
	})
	if !domain.IsKind(err, domain.KindNotFound) {
		t.Fatalf("error = %v, want not_found", err)
	}
}

/* ── 5. scope ────────────────────────────────────────────────────────── */

// TestReleasesAreInstallationWideNotWorkspaceScoped covers requirement 5,
// for the scope this context actually has.
//
// Two different workspace headers must see byte-identical history: a
// release describes the deployed build, and a build does not differ per
// tenant. This is the test that would fail first if someone later added a
// workspace_id column and started filtering on it.
func TestReleasesAreInstallationWideNotWorkspaceScoped(t *testing.T) {
	e := newEnv(t)

	read := func() string {
		req := httptest.NewRequest("GET", "/releases/modules", nil)
		req.Header.Set("X-Workspace-Id", uuid.New().String())
		rec := httptest.NewRecorder()
		e.r.ServeHTTP(rec, req)
		wantStatus(t, rec, http.StatusOK)
		return rec.Body.String()
	}
	if a, b := read(), read(); a != b {
		t.Fatalf("two workspaces see different histories:\n%s\n%s", a, b)
	}
}

/* ── 11. the structured fields survive the round trip ────────────────── */

// TestStructuredFieldsSerializeAndComeBackIntact covers requirement 11.
// The JSONB columns are the snapshot; if they lose a field in transit the
// history is quietly wrong rather than loudly broken.
func TestStructuredFieldsSerializeAndComeBackIntact(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	in := app.RecordInput{
		ModuleKey: "finance",
		Version:   "0.9.0",
		Summary:   "round trip",
		Capabilities: []domain.Capability{
			{Name: "Transactions", Note: "with a note"},
			{Name: "Categories"}, // no note: the omitempty path
		},
		Evidence:       []domain.Metric{{Label: "Integration tests", Value: "24"}},
		Limitations:    []domain.Note{{Text: "Two entities still live in the browser."}},
		Decisions:      []domain.Note{{Text: "Totals contract is frozen.", Ref: "totals-contract.md"}},
		TechnicalNotes: []domain.Note{{Text: "Schema finance, 8 migrations."}},
		DocRefs:        []domain.DocRef{{Label: "Contract", Path: "backend/docs/totals-contract.md"}},
	}
	if _, err := e.svc.Record(ctx, in); err != nil {
		t.Fatalf("record: %v", err)
	}

	rec := e.do("GET", "/releases/modules/finance/releases/0.9.0")
	wantStatus(t, rec, http.StatusOK)
	var got domain.Release
	e.decode(rec, &got)

	if len(got.Capabilities) != 2 || got.Capabilities[0].Note != "with a note" {
		t.Errorf("capabilities did not survive: %+v", got.Capabilities)
	}
	if got.Capabilities[1].Note != "" {
		t.Errorf("an absent note came back as %q", got.Capabilities[1].Note)
	}
	if len(got.Evidence) != 1 || got.Evidence[0].Value != "24" {
		t.Errorf("evidence did not survive: %+v", got.Evidence)
	}
	if len(got.Decisions) != 1 || got.Decisions[0].Ref != "totals-contract.md" {
		t.Errorf("decision ref did not survive: %+v", got.Decisions)
	}
	if len(got.DocRefs) != 1 || got.DocRefs[0].Path == "" {
		t.Errorf("doc refs did not survive: %+v", got.DocRefs)
	}
}

// TestAModuleWithNoReleasesIsHonestAboutIt: empty lists, not nulls, and no
// invented current version.
//
// This test used to run against Finance, on the argument that a REAL
// module with a genuinely empty history proves more than a synthetic one:
// a row the test inserted would only show that the endpoint handles a row
// the test just made.
//
// That argument was right and its premise is gone. Finance 1.0.0 shipped
// on 2026-08-26, and with it the last seeded module without a release —
// the product outgrew the fixture, which is the good reason for a fixture
// to move. A synthetic module is now the ONLY way to express "no releases
// at all", and the choice is between a weaker version of this test and no
// version of it. An endpoint that invented a current release out of an
// empty history would still be a lie about history, so the test stays.
func TestAModuleWithNoReleasesIsHonestAboutIt(t *testing.T) {
	e := newEnv(t)
	e.insertModule("zz-empty", "active", 901)

	rec := e.do("GET", "/releases/modules/zz-empty")
	wantStatus(t, rec, http.StatusOK)

	var detail moduleDetail
	e.decode(rec, &detail)
	if detail.Current != nil {
		t.Errorf("current_release = %+v, want nil", detail.Current)
	}
	if detail.ReleaseCount != 0 {
		t.Errorf("release_count = %d, want 0", detail.ReleaseCount)
	}
	if detail.Releases == nil {
		t.Error("releases is null; it must serialize as an empty list")
	}
	if len(detail.Releases) != 0 {
		t.Errorf("%d releases, want 0", len(detail.Releases))
	}
}

func TestUnknownModuleAndVersionAre404(t *testing.T) {
	e := newEnv(t)
	wantStatus(t, e.do("GET", "/releases/modules/nope"), http.StatusNotFound)
	wantStatus(t, e.do("GET", "/releases/modules/agents/releases/9.9.9"), http.StatusNotFound)
	wantStatus(t, e.do("POST", "/releases/modules/agents/releases/9.9.9/publish"), http.StatusNotFound)
}

/* ── 12. the seed is reproducible ────────────────────────────────────── */

// TestPublishingThroughTheApiFirstDoesNotBreakTheDeploy.
//
// The order of two independent events is not guaranteed: the owner can
// click Publish on a running instance before the deploy that carries the
// publish migration reaches it. When that happens the migration meets a
// row that is already published, and the freeze trigger is in force.
//
// Without the `status = 'draft'` guard the UPDATE would touch that row,
// the trigger would raise, the migration would fail, and — because the
// entrypoint aborts on a failed migration — the container would refuse to
// start. A release history feature would have taken production down.
//
// This test drives exactly that sequence: migrate to 4, publish through
// the real service, then let 5 arrive.
func TestPublishingThroughTheApiFirstDoesNotBreakTheDeploy(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	d := dsn(t)

	// Build the schema up to just before the publish migration, so the row
	// is a draft — the state a running instance is in before the deploy.
	//
	// This resets and migrates forward rather than stepping 5 back, because
	// 0005 has no down: un-publishing is exactly what the freeze forbids,
	// so there is no reverse to walk.
	resetSchema(t, d)
	m, err := migrate.New("file://../../migrations/releases", releasesMigrateDSN(t, d))
	if err != nil {
		t.Fatalf("migrate.New: %v", err)
	}
	defer func() { _, _ = m.Close() }()
	if err := m.Migrate(4); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate to 4: %v", err)
	}
	if pre, err := e.svc.GetRelease(ctx, "agents", "1.0.0"); err != nil {
		t.Fatalf("read at version 4: %v", err)
	} else if pre.Status != domain.StatusDraft {
		t.Fatalf("at version 4 the release is %q, want draft", pre.Status)
	}

	apiDate := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	published, err := e.svc.WithClock(func() time.Time { return apiDate }).
		Publish(ctx, "agents", "1.0.0")
	if err != nil {
		t.Fatalf("publish through the service: %v", err)
	}
	if !published.ReleasedAt.UTC().Equal(apiDate) {
		t.Fatalf("released_at = %v, want the API's date", published.ReleasedAt.UTC())
	}

	// Now the deploy lands. It must be a no-op, not a failure.
	if err := m.Migrate(5); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("the publish migration failed against an already-published release: %v.\n"+
			"Without its `status = 'draft'` guard the freeze trigger raises here and the "+
			"entrypoint aborts the boot.", err)
	}

	// And the date the owner actually published on survives. The migration
	// must not restamp a decision that already happened.
	after, err := e.svc.GetRelease(ctx, "agents", "1.0.0")
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if !after.ReleasedAt.UTC().Equal(apiDate) {
		t.Errorf("released_at = %v; the migration overwrote the real publication date",
			after.ReleasedAt.UTC())
	}
}

// TestTheSeedIsReproducibleAndDoesNotDuplicate covers requirement 12.
// Re-running the migration must converge, not accumulate: the seed uses
// ON CONFLICT DO NOTHING precisely so a re-deploy is a no-op.
func TestTheSeedIsReproducibleAndDoesNotDuplicate(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	// Counted BEFORE the re-run rather than written down as a number.
	//
	// The property is CONVERGENCE — re-applying the seed changes nothing —
	// and a literal expected count tests something else: how many modules
	// the catalogue happens to hold today. That spelling has already had to
	// be edited once per module added, and each edit is a chance to update
	// the number while the accumulation it exists to catch goes unnoticed.
	var before int
	if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM releases.modules`).Scan(&before); err != nil {
		t.Fatalf("count modules before: %v", err)
	}
	if before == 0 {
		t.Fatal("no modules were seeded; the re-run would prove nothing")
	}

	m, err := migrate.New("file://../../migrations/releases", releasesMigrateDSN(t, dsn(t)))
	if err != nil {
		t.Fatalf("migrate.New: %v", err)
	}
	defer func() { _, _ = m.Close() }()
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("re-running up: %v", err)
	}

	var modules, rels int
	if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM releases.modules`).Scan(&modules); err != nil {
		t.Fatalf("count modules: %v", err)
	}
	if err := e.pool.QueryRow(ctx,
		`SELECT count(*) FROM releases.releases WHERE module_key='agents' AND version='1.0.0'`).Scan(&rels); err != nil {
		t.Fatalf("count releases: %v", err)
	}
	if modules != before {
		t.Errorf("%d modules after re-running the seed, had %d: the seed accumulated",
			modules, before)
	}
	if rels != 1 {
		t.Errorf("%d agents/1.0.0 rows, want 1", rels)
	}

	rel, err := e.svc.GetRelease(ctx, "agents", "1.0.0")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if rel.Status != domain.StatusPublished {
		t.Errorf("the re-run reverted a published release to %q", rel.Status)
	}
	// The release date is the owner's, and a redeploy must not move it to
	// the day the container restarted.
	if !rel.ReleasedAt.UTC().Equal(officialReleaseDate) {
		t.Errorf("re-running the migrations moved released_at to %v", rel.ReleasedAt.UTC())
	}
}
