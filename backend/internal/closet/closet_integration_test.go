//go:build integration

// Closet — the foundation suite.
//
//	Run with: TEST_POSTGRES_DSN=... go test -tags=integration ./internal/closet/...
//
// The sentence this suite has to make convincing:
//
//	The operator can catalogue a real garment with its photographs, compose
//	a look out of pieces, save it, come back and find exactly the same
//	composition — and none of it is reachable from another workspace.
//
// What is REAL here: the Closet schema, domain, repository, service and
// HTTP routes, the image decoder, and Postgres. Nothing is faked: this
// module talks to no external system, so there is nothing to stand in for.
// The PNGs are encoded by the tests themselves, through the same standard
// library the server decodes with.
package closet

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"mime/multipart"
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

	"github.com/corsi/backend/internal/closet/domain"
	"github.com/corsi/backend/internal/platform/postgres"
	"github.com/corsi/backend/internal/platform/testdb"
	"github.com/corsi/backend/internal/platform/workspace"
)

/* ── harness ─────────────────────────────────────────────────────────── */

// ── Why this suite runs in a database of its own ──────────────────────
//
// Same reason the others do: it resets its schema before every test, and a
// reset inside the shared database is a reset another package's suite can
// be running against. Creating one database and dropping schemas inside it
// makes the isolation structural rather than a rule about which tests may
// run together.
const privateDBName = "corsi_test_closet"

// suiteDSN points at the database this package owns. Set by TestMain.
var suiteDSN string

func TestMain(m *testing.M) {
	admin := os.Getenv("TEST_POSTGRES_DSN")
	if admin == "" {
		// Nothing to set up; every test skips individually.
		os.Exit(m.Run())
	}

	d, cleanup, err := createPrivateDB(admin)
	if err != nil {
		panic("closet suite: " + err.Error())
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

	// Dropped first: a previous run killed mid-way leaves it behind, and a
	// stale database would be worse than no isolation at all.
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

	// This suite provisions its own database, so it is the party that knows
	// the database is disposable, and it says so at the moment it creates
	// it. That is what lets dsn() refuse anything else without carving out
	// an exception for this package.
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

// dsn is where the suite learns which database it may destroy. Same
// placement and same reason as every other suite: this is the only way to
// obtain one, so no destructive statement can be reached without it.
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
// runner would read finance's table, find it populated, and conclude there
// is nothing to apply — creating no schema at all.
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

func freshDB(t *testing.T, d string) {
	t.Helper()
	ctx := context.Background()

	conn, err := pgx.Connect(ctx, d)
	if err != nil {
		t.Fatalf("connect for reset: %v", err)
	}
	for _, stmt := range []string{
		`DROP SCHEMA IF EXISTS closet CASCADE`,
		`DROP TABLE IF EXISTS schema_migrations_closet`,
	} {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			_ = conn.Close(ctx)
			t.Fatalf("reset (%s): %v", stmt, err)
		}
	}
	if err := conn.Close(ctx); err != nil {
		t.Fatalf("close reset conn: %v", err)
	}

	m, err := migrate.New("file://../../migrations/closet", migrateDSN(t, d, "schema_migrations_closet"))
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

	// Two workspaces, always. Every isolation assertion reads wsA's data
	// back as wsB.
	wsA uuid.UUID
	wsB uuid.UUID
}

func newEnv(t *testing.T) *env {
	t.Helper()
	d := dsn(t)
	freshDB(t, d)

	ctx := context.Background()
	pool, err := postgres.Open(ctx, postgres.Config{
		DSN: d, MaxConns: 4, MinConns: 1,
		MaxConnLifetime: time.Hour, MaxConnIdleTime: 30 * time.Minute,
	})
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)

	log := slog.New(slog.NewJSONHandler(io.Discard, nil))

	// The module, wired exactly the way cmd/corsi wires it — including the
	// workspace middleware, which here reads a test header instead of
	// X-Workspace-Id so a test can be two tenants in one process. Everything
	// below the transport is production code.
	mod := New(Deps{
		Pool:   pool,
		Logger: log,
		WorkspaceMiddleware: func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				id, err := uuid.Parse(req.Header.Get("X-Test-Workspace-Id"))
				if err != nil {
					http.Error(w, "test header missing or invalid", http.StatusForbidden)
					return
				}
				next.ServeHTTP(w, req.WithContext(workspace.WithWorkspaceID(req.Context(), id)))
			})
		},
	})
	root := chi.NewRouter()
	mod.Register(root)

	return &env{t: t, r: root, pool: pool, wsA: uuid.New(), wsB: uuid.New()}
}

/* ── request helpers ─────────────────────────────────────────────────── */

func (e *env) do(ws uuid.UUID, method, path string, body any) *httptest.ResponseRecorder {
	e.t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			e.t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("X-Test-Workspace-Id", ws.String())
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	e.r.ServeHTTP(rec, req)
	return rec
}

// upload posts a multipart image. `filename` is under the test's control on
// purpose: several tests hand it something hostile, and the assertion is
// that it makes no difference whatsoever.
func (e *env) upload(ws, itemID uuid.UUID, view, filename string, raw []byte) *httptest.ResponseRecorder {
	e.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("file", filename)
	if err != nil {
		e.t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write(raw); err != nil {
		e.t.Fatalf("write form file: %v", err)
	}
	if err := mw.Close(); err != nil {
		e.t.Fatalf("close multipart: %v", err)
	}

	req := httptest.NewRequest(http.MethodPut,
		fmt.Sprintf("/closet/items/%s/images/%s", itemID, view), &buf)
	req.Header.Set("X-Test-Workspace-Id", ws.String())
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	e.r.ServeHTTP(rec, req)
	return rec
}

func decodeInto[T any](t *testing.T, rec *httptest.ResponseRecorder, want int) T {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, want, rec.Body.String())
	}
	var out T
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v; body: %s", err, rec.Body.String())
	}
	return out
}

type itemsPage struct {
	Items []domain.ClosetItem `json:"items"`
	Total int64               `json:"total"`
}

type looksPage struct {
	Items []domain.Look `json:"items"`
	Total int64         `json:"total"`
}

// pngBytes renders a real PNG. `seed` changes the pixels, which changes the
// digest — which is what lets a test tell "the same file again" from "a
// different file".
func pngBytes(t *testing.T, w, h, seed int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.RGBA{R: uint8(seed), G: uint8(seed * 2), B: 30, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

// createItem is the seed helper every test builds a wardrobe with. It goes
// through the HTTP surface rather than through SQL, so a test cannot seed a
// row the product itself could not create.
func (e *env) createItem(ws uuid.UUID, name, category string) domain.ClosetItem {
	e.t.Helper()
	rec := e.do(ws, http.MethodPost, "/closet/items", map[string]any{
		"name":          name,
		"category":      category,
		"primary_color": "preto",
	})
	return decodeInto[domain.ClosetItem](e.t, rec, http.StatusCreated)
}

/* ── the catalogue ───────────────────────────────────────────────────── */

func TestCatalogPublishesTheVocabularyTheServerEnforces(t *testing.T) {
	e := newEnv(t)

	type catalogResponse struct {
		Categories []struct {
			Category    string   `json:"category"`
			Slot        string   `json:"slot"`
			Views       []string `json:"views"`
			Composition []string `json:"composition"`
		} `json:"categories"`
		Slots []struct {
			Slot     string `json:"slot"`
			Capacity int    `json:"capacity"`
		} `json:"slots"`
		Occasions    []string `json:"occasions"`
		MaxLookItems int      `json:"max_look_items"`
	}

	got := decodeInto[catalogResponse](t, e.do(e.wsA, http.MethodGet, "/closet/catalog", nil), http.StatusOK)
	if len(got.Categories) != len(domain.Categories()) {
		t.Fatalf("catalog published %d categories, the domain declares %d",
			len(got.Categories), len(domain.Categories()))
	}

	// The point of publishing it: the frontend must not need its own copy.
	byName := map[string]int{}
	for i, c := range got.Categories {
		byName[c.Category] = i
	}
	watches, ok := byName["watches"]
	if !ok {
		t.Fatal("the catalog does not publish `watches`")
	}
	for _, v := range got.Categories[watches].Views {
		if strings.HasPrefix(v, "hanger") {
			t.Fatalf("the catalog offers a watch the view %q", v)
		}
	}
	if got.MaxLookItems <= 0 {
		t.Fatal("the catalog published no composition ceiling")
	}
}

/* ── items ───────────────────────────────────────────────────────────── */

func TestAPieceIsCreatedReadAndUpdated(t *testing.T) {
	e := newEnv(t)

	created := e.createItem(e.wsA, "Camiseta preta", "tops")
	if created.Status != domain.StatusActive {
		t.Fatalf("a new piece has status %q, want active", created.Status)
	}
	if created.Images == nil {
		t.Fatal("a new piece has a nil image list; it should be an empty one")
	}

	updated := decodeInto[domain.ClosetItem](t,
		e.do(e.wsA, http.MethodPatch, "/closet/items/"+created.ID.String(),
			map[string]any{"brand": "Uniqlo", "favorite": true}),
		http.StatusOK)
	if updated.Brand != "Uniqlo" || !updated.Favorite {
		t.Fatalf("the patch did not land: %+v", updated)
	}
	// A nil field means "leave it alone". Without that, a detail panel
	// saving one edited field would blank the five it did not send.
	if updated.Name != created.Name {
		t.Fatalf("name changed to %q on a patch that did not mention it", updated.Name)
	}
}

func TestAnUnknownCategoryIsRefusedWithTheListItWouldAccept(t *testing.T) {
	e := newEnv(t)
	rec := e.do(e.wsA, http.MethodPost, "/closet/items", map[string]any{
		"name": "Boné", "category": "hats", "primary_color": "preto",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "accessories") {
		t.Fatalf("the refusal does not name the categories it would accept: %s", rec.Body.String())
	}
}

func TestArchivingRemovesAPieceFromTheSelectorAndKeepsIt(t *testing.T) {
	e := newEnv(t)
	item := e.createItem(e.wsA, "Casaco velho", "layers")

	type archiveResponse struct {
		Item          domain.ClosetItem `json:"item"`
		LooksAffected int64             `json:"looks_affected"`
	}
	got := decodeInto[archiveResponse](t,
		e.do(e.wsA, http.MethodPost, "/closet/items/"+item.ID.String()+"/archive", nil),
		http.StatusOK)
	if got.Item.Status != domain.StatusArchived {
		t.Fatalf("status after archive = %q, want archived", got.Item.Status)
	}

	// Gone from the default listing…
	page := decodeInto[itemsPage](t, e.do(e.wsA, http.MethodGet, "/closet/items", nil), http.StatusOK)
	if page.Total != 0 {
		t.Fatalf("an archived piece is still in the default listing (total=%d)", page.Total)
	}
	// …and still there when asked for, because a saved look references it.
	withArchived := decodeInto[itemsPage](t,
		e.do(e.wsA, http.MethodGet, "/closet/items?include_archived=true", nil), http.StatusOK)
	if withArchived.Total != 1 {
		t.Fatalf("include_archived returned %d pieces, want 1", withArchived.Total)
	}

	// And it comes back.
	restored := decodeInto[domain.ClosetItem](t,
		e.do(e.wsA, http.MethodPost, "/closet/items/"+item.ID.String()+"/restore", nil),
		http.StatusOK)
	if restored.Status != domain.StatusActive {
		t.Fatalf("status after restore = %q, want active", restored.Status)
	}
}

func TestListingNarrowsByCategoryAndSearch(t *testing.T) {
	e := newEnv(t)
	e.createItem(e.wsA, "Camiseta preta", "tops")
	e.createItem(e.wsA, "Calça bege", "bottoms")
	e.createItem(e.wsA, "Tênis branco", "shoes")

	byCategory := decodeInto[itemsPage](t,
		e.do(e.wsA, http.MethodGet, "/closet/items?category=tops", nil), http.StatusOK)
	if byCategory.Total != 1 || byCategory.Items[0].Name != "Camiseta preta" {
		t.Fatalf("category filter returned %+v", byCategory)
	}

	bySearch := decodeInto[itemsPage](t,
		e.do(e.wsA, http.MethodGet, "/closet/items?search=BEGE", nil), http.StatusOK)
	if bySearch.Total != 1 || bySearch.Items[0].Name != "Calça bege" {
		t.Fatalf("search is not case-insensitive: %+v", bySearch)
	}
}

func TestAPieceOfAnotherWorkspaceIsNotFound(t *testing.T) {
	e := newEnv(t)
	item := e.createItem(e.wsA, "Camiseta preta", "tops")

	// Not 403. "Does not exist anywhere" and "exists in a workspace that is
	// not yours" are deliberately the same answer: telling them apart would
	// confirm another workspace's row.
	if rec := e.do(e.wsB, http.MethodGet, "/closet/items/"+item.ID.String(), nil); rec.Code != http.StatusNotFound {
		t.Fatalf("reading another workspace's piece returned %d, want 404", rec.Code)
	}
	if rec := e.do(e.wsB, http.MethodPatch, "/closet/items/"+item.ID.String(),
		map[string]any{"brand": "stolen"}); rec.Code != http.StatusNotFound {
		t.Fatalf("patching another workspace's piece returned %d, want 404", rec.Code)
	}
	page := decodeInto[itemsPage](t, e.do(e.wsB, http.MethodGet, "/closet/items", nil), http.StatusOK)
	if page.Total != 0 {
		t.Fatalf("workspace B sees %d of workspace A's pieces", page.Total)
	}
}

/* ── images ──────────────────────────────────────────────────────────── */

func TestAnImageIsAttachedAndServedBack(t *testing.T) {
	e := newEnv(t)
	item := e.createItem(e.wsA, "Camiseta preta", "tops")
	raw := pngBytes(t, 400, 500, 1)

	img := decodeInto[domain.ItemImage](t,
		e.upload(e.wsA, item.ID, "folded", "camiseta.png", raw), http.StatusOK)
	if img.View != domain.ViewFolded {
		t.Fatalf("view = %q, want folded", img.View)
	}
	if img.Width != 400 || img.Height != 500 {
		t.Fatalf("dimensions = %dx%d, want 400x500", img.Width, img.Height)
	}

	// The item read carries the image metadata and not the bytes.
	read := decodeInto[domain.ClosetItem](t,
		e.do(e.wsA, http.MethodGet, "/closet/items/"+item.ID.String(), nil), http.StatusOK)
	if len(read.Images) != 1 || read.Images[0].AssetID != img.AssetID {
		t.Fatalf("the piece does not carry its image: %+v", read.Images)
	}

	// The bytes come back byte-for-byte, with the type the server decided.
	rec := e.do(e.wsA, http.MethodGet, "/closet/assets/"+img.AssetID.String(), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("asset read returned %d; body: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != domain.ContentTypePNG {
		t.Fatalf("content type = %q, want %q", got, domain.ContentTypePNG)
	}
	if !bytes.Equal(rec.Body.Bytes(), raw) {
		t.Fatal("the bytes served back are not the bytes uploaded")
	}
	if got := rec.Header().Get("ETag"); got == "" {
		t.Fatal("the asset response carries no ETag")
	}
	if got := rec.Header().Get("Cache-Control"); !strings.Contains(got, "immutable") {
		t.Fatalf("cache-control = %q, want an immutable private cache", got)
	}
}

func TestAConditionalAssetRequestIsNotModified(t *testing.T) {
	e := newEnv(t)
	item := e.createItem(e.wsA, "Camiseta preta", "tops")
	img := decodeInto[domain.ItemImage](t,
		e.upload(e.wsA, item.ID, "folded", "x.png", pngBytes(t, 64, 64, 2)), http.StatusOK)

	first := e.do(e.wsA, http.MethodGet, "/closet/assets/"+img.AssetID.String(), nil)
	etag := first.Header().Get("ETag")

	req := httptest.NewRequest(http.MethodGet, "/closet/assets/"+img.AssetID.String(), nil)
	req.Header.Set("X-Test-Workspace-Id", e.wsA.String())
	req.Header.Set("If-None-Match", etag)
	rec := httptest.NewRecorder()
	e.r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotModified {
		t.Fatalf("a revalidation returned %d, want 304", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("a 304 carried %d bytes of body", rec.Body.Len())
	}
}

func TestAnAssetOfAnotherWorkspaceIsNotServed(t *testing.T) {
	e := newEnv(t)
	item := e.createItem(e.wsA, "Camiseta preta", "tops")
	img := decodeInto[domain.ItemImage](t,
		e.upload(e.wsA, item.ID, "folded", "x.png", pngBytes(t, 64, 64, 3)), http.StatusOK)

	// ══════════════════════════════════════════════════════════════════
	// The most important assertion in this file. The asset route is the
	// one address that returns raw bytes a browser renders, so a missing
	// workspace predicate here is a real leak rather than a confusing
	// listing. The id is handed over deliberately — holding it must not
	// be enough.
	// ══════════════════════════════════════════════════════════════════
	if rec := e.do(e.wsB, http.MethodGet, "/closet/assets/"+img.AssetID.String(), nil); rec.Code != http.StatusNotFound {
		t.Fatalf("workspace B read workspace A's image: status %d, %d bytes",
			rec.Code, rec.Body.Len())
	}
}

func TestUploadingToAnotherWorkspacesPieceIsRefusedBeforeAnythingIsStored(t *testing.T) {
	e := newEnv(t)
	item := e.createItem(e.wsA, "Camiseta preta", "tops")

	rec := e.upload(e.wsB, item.ID, "folded", "x.png", pngBytes(t, 64, 64, 4))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("uploading to another workspace's piece returned %d, want 404", rec.Code)
	}

	// And no orphaned bytes were written. The piece is resolved BEFORE the
	// image is decoded or stored, precisely so a rejected upload leaves
	// nothing behind.
	var assets int64
	if err := e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM closet.assets`).Scan(&assets); err != nil {
		t.Fatalf("count assets: %v", err)
	}
	if assets != 0 {
		t.Fatalf("a refused upload stored %d assets", assets)
	}
}

func TestAWatchCannotBeGivenAHangerView(t *testing.T) {
	e := newEnv(t)
	watch := e.createItem(e.wsA, "Seiko", "watches")

	rec := e.upload(e.wsA, watch.ID, "hanger_front", "x.png", pngBytes(t, 64, 64, 5))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a watch accepted a hanger view: status %d", rec.Code)
	}
	if rec := e.upload(e.wsA, watch.ID, "front", "x.png", pngBytes(t, 64, 64, 6)); rec.Code != http.StatusOK {
		t.Fatalf("a watch refused its own front view: status %d, body %s", rec.Code, rec.Body.String())
	}
}

func TestReAttachingAViewReplacesItRatherThanAccumulating(t *testing.T) {
	e := newEnv(t)
	item := e.createItem(e.wsA, "Camiseta preta", "tops")

	first := decodeInto[domain.ItemImage](t,
		e.upload(e.wsA, item.ID, "folded", "a.png", pngBytes(t, 64, 64, 7)), http.StatusOK)
	second := decodeInto[domain.ItemImage](t,
		e.upload(e.wsA, item.ID, "folded", "b.png", pngBytes(t, 80, 80, 8)), http.StatusOK)

	if first.AssetID == second.AssetID {
		t.Fatal("two different files resolved to one asset")
	}
	read := decodeInto[domain.ClosetItem](t,
		e.do(e.wsA, http.MethodGet, "/closet/items/"+item.ID.String(), nil), http.StatusOK)
	if len(read.Images) != 1 {
		t.Fatalf("the piece carries %d folded images, want 1", len(read.Images))
	}
	if read.Images[0].AssetID != second.AssetID {
		t.Fatal("the piece still points at the replaced image")
	}
}

func TestTheSameFileTwiceIsStoredOnce(t *testing.T) {
	e := newEnv(t)
	a := e.createItem(e.wsA, "Camiseta preta", "tops")
	b := e.createItem(e.wsA, "Camiseta preta II", "tops")
	raw := pngBytes(t, 64, 64, 9)

	first := decodeInto[domain.ItemImage](t, e.upload(e.wsA, a.ID, "folded", "x.png", raw), http.StatusOK)
	second := decodeInto[domain.ItemImage](t, e.upload(e.wsA, b.ID, "folded", "x.png", raw), http.StatusOK)

	if first.AssetID != second.AssetID {
		t.Fatal("the same bytes were stored twice")
	}
	var assets int64
	if err := e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM closet.assets`).Scan(&assets); err != nil {
		t.Fatalf("count assets: %v", err)
	}
	if assets != 1 {
		t.Fatalf("the assets table holds %d rows, want 1", assets)
	}
}

func TestDeduplicationDoesNotCrossWorkspaces(t *testing.T) {
	e := newEnv(t)
	a := e.createItem(e.wsA, "Camiseta", "tops")
	b := e.createItem(e.wsB, "Camiseta", "tops")
	raw := pngBytes(t, 64, 64, 10)

	first := decodeInto[domain.ItemImage](t, e.upload(e.wsA, a.ID, "folded", "x.png", raw), http.StatusOK)
	second := decodeInto[domain.ItemImage](t, e.upload(e.wsB, b.ID, "folded", "x.png", raw), http.StatusOK)

	// Content addressing saves space; it must never make one tenant's
	// upload resolve to a row another tenant can read. The workspace is
	// part of the unique key for exactly this.
	if first.AssetID == second.AssetID {
		t.Fatal("two workspaces uploading the same file share one asset row")
	}
}

func TestTheUploadedFilenameIsIrrelevant(t *testing.T) {
	e := newEnv(t)
	item := e.createItem(e.wsA, "Camiseta preta", "tops")
	raw := pngBytes(t, 64, 64, 11)

	// ══════════════════════════════════════════════════════════════════
	// There is no path traversal defence in this module because there is
	// no path. The filename is never read — not to build a location, not
	// to derive an extension, not to name the stored object.
	// ══════════════════════════════════════════════════════════════════
	// Two cases are deliberately NOT in this list, and both for the same
	// reason: they never reach this module. A NUL byte cannot survive a
	// multipart header, and an EMPTY filename makes Go's parser treat the
	// part as a form VALUE rather than a file — so in both cases the
	// refusal comes from the standard library, which never saw the filename
	// as a filename either. Including them would test Go's encoder.
	for _, filename := range []string{
		"../../../etc/passwd",
		"..\\..\\windows\\system32\\config\\sam",
		"/etc/shadow",
		"shirt.png.sh",
		".",
		strings.Repeat("a", 4000) + ".png",
	} {
		rec := e.upload(e.wsA, item.ID, "folded", filename, raw)
		if rec.Code != http.StatusOK {
			t.Fatalf("filename %q changed the outcome: status %d, body %s",
				filename, rec.Code, rec.Body.String())
		}
	}

	// Nothing under closet/ names a file, and the operator's string reached
	// no column.
	var assets int64
	if err := e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM closet.assets`).Scan(&assets); err != nil {
		t.Fatalf("count assets: %v", err)
	}
	if assets != 1 {
		t.Fatalf("repeated uploads of one file produced %d assets, want 1", assets)
	}
}

func TestAFileThatIsNotAnImageIsRefused(t *testing.T) {
	e := newEnv(t)
	item := e.createItem(e.wsA, "Camiseta preta", "tops")

	rec := e.upload(e.wsA, item.ID, "folded", "totally-a.png",
		[]byte("#!/bin/sh\necho this is not a png\n"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a shell script named .png was accepted: status %d", rec.Code)
	}
}

func TestAnOversizedUploadIsRefusedAsOversizedAndNotAsMalformed(t *testing.T) {
	e := newEnv(t)
	item := e.createItem(e.wsA, "Camiseta preta", "tops")

	// The two failures must not be collapsed. Telling somebody their
	// multipart body is too large when it is merely broken sends them off
	// to shrink an image that was never the problem — which is exactly the
	// defect this assertion was written to catch.
	oversized := bytes.Repeat([]byte{0x41}, domain.MaxImageBytes+2048)
	rec := e.upload(e.wsA, item.ID, "folded", "huge.png", oversized)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("an oversized upload returned %d, want 413; body %s", rec.Code, rec.Body.String())
	}

	// And nothing was stored on the way.
	var assets int64
	if err := e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM closet.assets`).Scan(&assets); err != nil {
		t.Fatalf("count assets: %v", err)
	}
	if assets != 0 {
		t.Fatalf("an oversized upload stored %d assets", assets)
	}
}

func TestRemovingAnImageKeepsTheAsset(t *testing.T) {
	e := newEnv(t)
	a := e.createItem(e.wsA, "Camiseta A", "tops")
	b := e.createItem(e.wsA, "Camiseta B", "tops")
	raw := pngBytes(t, 64, 64, 12)
	e.upload(e.wsA, a.ID, "folded", "x.png", raw)
	e.upload(e.wsA, b.ID, "folded", "x.png", raw)

	rec := e.do(e.wsA, http.MethodDelete, "/closet/items/"+a.ID.String()+"/images/folded", nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("removing an image returned %d; body %s", rec.Code, rec.Body.String())
	}

	// B shared the bytes. Deleting them with A's image row would have
	// blanked a photograph nobody touched.
	read := decodeInto[domain.ClosetItem](t,
		e.do(e.wsA, http.MethodGet, "/closet/items/"+b.ID.String(), nil), http.StatusOK)
	if len(read.Images) != 1 {
		t.Fatalf("the other piece lost its image: %+v", read.Images)
	}
	if rec := e.do(e.wsA, http.MethodGet, "/closet/assets/"+read.Images[0].AssetID.String(), nil); rec.Code != http.StatusOK {
		t.Fatalf("the shared asset is gone: status %d", rec.Code)
	}
}

/* ── looks ───────────────────────────────────────────────────────────── */

func TestALookIsComposedSavedAndReopenedIdentically(t *testing.T) {
	e := newEnv(t)
	top := e.createItem(e.wsA, "Camiseta preta", "tops")
	bottom := e.createItem(e.wsA, "Calça bege", "bottoms")
	shoes := e.createItem(e.wsA, "Tênis branco", "shoes")
	watch := e.createItem(e.wsA, "Seiko", "watches")

	created := decodeInto[domain.Look](t,
		e.do(e.wsA, http.MethodPost, "/closet/looks", map[string]any{
			"name":     "Sexta-feira",
			"occasion": "casual",
			"item_ids": []string{top.ID.String(), bottom.ID.String(), shoes.ID.String(), watch.ID.String()},
		}), http.StatusCreated)

	if len(created.Items) != 4 {
		t.Fatalf("the look was saved with %d pieces, want 4", len(created.Items))
	}

	// ══════════════════════════════════════════════════════════════════
	// The stop condition of this sprint, as one assertion: close the
	// screen, come back, get exactly the same composition.
	// ══════════════════════════════════════════════════════════════════
	reopened := decodeInto[domain.Look](t,
		e.do(e.wsA, http.MethodGet, "/closet/looks/"+created.ID.String(), nil), http.StatusOK)

	if len(reopened.Items) != len(created.Items) {
		t.Fatalf("reopened with %d pieces, saved with %d", len(reopened.Items), len(created.Items))
	}
	for i := range created.Items {
		got, want := reopened.Items[i], created.Items[i]
		if got.ItemID != want.ItemID || got.Slot != want.Slot || got.Position != want.Position {
			t.Fatalf("slot %d differs: reopened %+v, saved %+v", i, got, want)
		}
	}

	// And the pieces are hydrated, so a card can be drawn without the
	// caller resolving four uuids itself.
	for _, entry := range reopened.Items {
		if entry.Item == nil {
			t.Fatalf("slot %q came back without its piece", entry.Slot)
		}
	}

	// Composition order is the canonical one, not the order they were sent.
	wantOrder := []domain.Slot{domain.SlotTop, domain.SlotBottom, domain.SlotShoes, domain.SlotWatch}
	for i, want := range wantOrder {
		if reopened.Items[i].Slot != want {
			t.Fatalf("slot at index %d is %q, want %q", i, reopened.Items[i].Slot, want)
		}
	}
}

func TestSavingTwoPiecesOfOneSlotKeepsTheLast(t *testing.T) {
	e := newEnv(t)
	first := e.createItem(e.wsA, "Camiseta preta", "tops")
	second := e.createItem(e.wsA, "Camiseta branca", "tops")

	// The server re-runs the composition rules. A payload with two shirts
	// stores one shirt — the second — exactly as clicking two shirts does,
	// because it is the same code deciding.
	look := decodeInto[domain.Look](t,
		e.do(e.wsA, http.MethodPost, "/closet/looks", map[string]any{
			"name":     "Troca",
			"item_ids": []string{first.ID.String(), second.ID.String()},
		}), http.StatusCreated)

	if len(look.Items) != 1 {
		t.Fatalf("two tops produced %d slots, want 1", len(look.Items))
	}
	if look.Items[0].ItemID != second.ID {
		t.Fatal("the first top won; placing a second should replace it")
	}
}

func TestReplacingAndRemovingAPieceOfASavedLook(t *testing.T) {
	e := newEnv(t)
	top := e.createItem(e.wsA, "Camiseta preta", "tops")
	other := e.createItem(e.wsA, "Camiseta branca", "tops")
	shoes := e.createItem(e.wsA, "Tênis branco", "shoes")

	look := decodeInto[domain.Look](t,
		e.do(e.wsA, http.MethodPost, "/closet/looks", map[string]any{
			"name": "Base", "item_ids": []string{top.ID.String(), shoes.ID.String()},
		}), http.StatusCreated)

	// Replace the top, keep the shoes.
	swapped := decodeInto[domain.Look](t,
		e.do(e.wsA, http.MethodPut, "/closet/looks/"+look.ID.String()+"/items",
			map[string]any{"item_ids": []string{other.ID.String(), shoes.ID.String()}}),
		http.StatusOK)
	if len(swapped.Items) != 2 {
		t.Fatalf("the swap left %d slots, want 2", len(swapped.Items))
	}
	for _, entry := range swapped.Items {
		if entry.Slot == domain.SlotTop && entry.ItemID != other.ID {
			t.Fatal("the top slot still holds the old piece")
		}
	}

	// Remove the top entirely.
	emptied := decodeInto[domain.Look](t,
		e.do(e.wsA, http.MethodPut, "/closet/looks/"+look.ID.String()+"/items",
			map[string]any{"item_ids": []string{shoes.ID.String()}}),
		http.StatusOK)
	if len(emptied.Items) != 1 || emptied.Items[0].Slot != domain.SlotShoes {
		t.Fatalf("after removing the top the look holds %+v", emptied.Items)
	}

	// The removed piece is untouched: a look is a reference, not a copy.
	if rec := e.do(e.wsA, http.MethodGet, "/closet/items/"+top.ID.String(), nil); rec.Code != http.StatusOK {
		t.Fatalf("removing a piece from a look changed the piece: status %d", rec.Code)
	}
}

func TestAnArchivedPieceCannotEnterANewLook(t *testing.T) {
	e := newEnv(t)
	item := e.createItem(e.wsA, "Casaco doado", "layers")
	e.do(e.wsA, http.MethodPost, "/closet/items/"+item.ID.String()+"/archive", nil)

	rec := e.do(e.wsA, http.MethodPost, "/closet/looks", map[string]any{
		"name": "Impossível", "item_ids": []string{item.ID.String()},
	})
	// A conflict rather than a 400: the request is well formed and the
	// piece exists. What is wrong is the state it is in.
	if rec.Code != http.StatusConflict {
		t.Fatalf("an archived piece entered a look: status %d, body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "archived") {
		t.Fatalf("the refusal does not say why: %s", rec.Body.String())
	}

	// And nothing was created on the way.
	page := decodeInto[looksPage](t, e.do(e.wsA, http.MethodGet, "/closet/looks", nil), http.StatusOK)
	if page.Total != 0 {
		t.Fatalf("a refused composition left %d looks behind", page.Total)
	}
}

func TestALookSurvivesArchivingOneOfItsPieces(t *testing.T) {
	e := newEnv(t)
	top := e.createItem(e.wsA, "Camiseta preta", "tops")
	shoes := e.createItem(e.wsA, "Tênis branco", "shoes")

	look := decodeInto[domain.Look](t,
		e.do(e.wsA, http.MethodPost, "/closet/looks", map[string]any{
			"name": "Histórico", "item_ids": []string{top.ID.String(), shoes.ID.String()},
		}), http.StatusCreated)

	type archiveResponse struct {
		LooksAffected int64 `json:"looks_affected"`
	}
	got := decodeInto[archiveResponse](t,
		e.do(e.wsA, http.MethodPost, "/closet/items/"+top.ID.String()+"/archive", nil),
		http.StatusOK)
	if got.LooksAffected != 1 {
		t.Fatalf("archiving reported %d affected looks, want 1", got.LooksAffected)
	}

	// The look still resolves, and still has both slots. Archiving is not a
	// veto and it is not a cascade.
	reopened := decodeInto[domain.Look](t,
		e.do(e.wsA, http.MethodGet, "/closet/looks/"+look.ID.String(), nil), http.StatusOK)
	if len(reopened.Items) != 2 {
		t.Fatalf("the look lost a slot when a piece was archived: %+v", reopened.Items)
	}
	for _, entry := range reopened.Items {
		if entry.Item == nil {
			t.Fatalf("slot %q lost its piece", entry.Slot)
		}
	}
}

func TestAPieceThatDoesNotExistIsRefused(t *testing.T) {
	e := newEnv(t)
	rec := e.do(e.wsA, http.MethodPost, "/closet/looks", map[string]any{
		"name": "Fantasma", "item_ids": []string{uuid.New().String()},
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("a look referencing nothing returned %d, want 404", rec.Code)
	}
}

func TestAPieceOfAnotherWorkspaceCannotBeComposedIn(t *testing.T) {
	e := newEnv(t)
	theirs := e.createItem(e.wsB, "Camiseta alheia", "tops")

	// Same answer as a piece that never existed — the lookup is
	// workspace-scoped, so there is nothing to distinguish.
	rec := e.do(e.wsA, http.MethodPost, "/closet/looks", map[string]any{
		"name": "Roubo", "item_ids": []string{theirs.ID.String()},
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("composing another workspace's piece returned %d, want 404", rec.Code)
	}
}

func TestALookOfAnotherWorkspaceIsNotFound(t *testing.T) {
	e := newEnv(t)
	top := e.createItem(e.wsA, "Camiseta preta", "tops")
	look := decodeInto[domain.Look](t,
		e.do(e.wsA, http.MethodPost, "/closet/looks", map[string]any{
			"name": "Meu", "item_ids": []string{top.ID.String()},
		}), http.StatusCreated)

	for _, probe := range []struct {
		method string
		path   string
		body   any
	}{
		{http.MethodGet, "/closet/looks/" + look.ID.String(), nil},
		{http.MethodPatch, "/closet/looks/" + look.ID.String(), map[string]any{"name": "roubado"}},
		{http.MethodPut, "/closet/looks/" + look.ID.String() + "/items", map[string]any{"item_ids": []string{}}},
		{http.MethodPost, "/closet/looks/" + look.ID.String() + "/archive", nil},
	} {
		if rec := e.do(e.wsB, probe.method, probe.path, probe.body); rec.Code != http.StatusNotFound {
			t.Errorf("%s %s from another workspace returned %d, want 404",
				probe.method, probe.path, rec.Code)
		}
	}
}

func TestALookIsRenamedAndFavouritedWithoutTouchingItsComposition(t *testing.T) {
	e := newEnv(t)
	top := e.createItem(e.wsA, "Camiseta preta", "tops")
	look := decodeInto[domain.Look](t,
		e.do(e.wsA, http.MethodPost, "/closet/looks", map[string]any{
			"name": "Rascunho", "item_ids": []string{top.ID.String()},
		}), http.StatusCreated)

	updated := decodeInto[domain.Look](t,
		e.do(e.wsA, http.MethodPatch, "/closet/looks/"+look.ID.String(),
			map[string]any{"name": "Sexta", "favorite": true, "occasion": "date"}),
		http.StatusOK)

	if updated.Name != "Sexta" || !updated.Favorite || updated.Occasion != domain.OccasionDate {
		t.Fatalf("the patch did not land: %+v", updated)
	}
	if len(updated.Items) != 1 || updated.Items[0].ItemID != top.ID {
		t.Fatalf("renaming a look changed its composition: %+v", updated.Items)
	}
}

func TestArchivingALookRemovesItFromTheGalleryAndComesBack(t *testing.T) {
	e := newEnv(t)
	top := e.createItem(e.wsA, "Camiseta preta", "tops")
	look := decodeInto[domain.Look](t,
		e.do(e.wsA, http.MethodPost, "/closet/looks", map[string]any{
			"name": "Antigo", "item_ids": []string{top.ID.String()},
		}), http.StatusCreated)

	e.do(e.wsA, http.MethodPost, "/closet/looks/"+look.ID.String()+"/archive", nil)

	page := decodeInto[looksPage](t, e.do(e.wsA, http.MethodGet, "/closet/looks", nil), http.StatusOK)
	if page.Total != 0 {
		t.Fatalf("an archived look is still in the gallery (total=%d)", page.Total)
	}
	withArchived := decodeInto[looksPage](t,
		e.do(e.wsA, http.MethodGet, "/closet/looks?include_archived=true", nil), http.StatusOK)
	if withArchived.Total != 1 {
		t.Fatalf("include_archived returned %d looks, want 1", withArchived.Total)
	}

	restored := decodeInto[domain.Look](t,
		e.do(e.wsA, http.MethodPost, "/closet/looks/"+look.ID.String()+"/restore", nil), http.StatusOK)
	if restored.Status != domain.StatusActive {
		t.Fatalf("status after restore = %q, want active", restored.Status)
	}
}

func TestACompositionCannotExceedTheSlotsThatExist(t *testing.T) {
	e := newEnv(t)

	// One more accessory than every slot could ever hold.
	ids := make([]string, 0)
	for i := 0; i <= maxLookItemsForTest(); i++ {
		item := e.createItem(e.wsA, fmt.Sprintf("Acessório %d", i), "accessories")
		ids = append(ids, item.ID.String())
	}

	rec := e.do(e.wsA, http.MethodPost, "/closet/looks", map[string]any{
		"name": "Exagero", "item_ids": ids,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("an oversized composition returned %d, want 400", rec.Code)
	}
}

// maxLookItemsForTest mirrors app.MaxLookItems through the public catalogue
// rather than importing the app package, so the test reads the ceiling the
// same way a client does.
func maxLookItemsForTest() int {
	total := 0
	for _, def := range domain.Slots() {
		total += def.Capacity
	}
	return total
}

/* ── the schema itself ───────────────────────────────────────────────── */

func TestTheMigrationCreatesTheWholeContextAndNothingElse(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	rows, err := e.pool.Query(ctx, `
		SELECT table_name FROM information_schema.tables
		WHERE table_schema = 'closet' ORDER BY table_name`)
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	defer rows.Close()

	var got []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan table name: %v", err)
		}
		got = append(got, name)
	}
	want := []string{"assets", "item_images", "items", "look_items", "looks"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("closet schema holds %v, want %v", got, want)
	}
}

func TestAPieceCannotBeHardDeletedWhileALookUsesIt(t *testing.T) {
	e := newEnv(t)
	top := e.createItem(e.wsA, "Camiseta preta", "tops")
	e.do(e.wsA, http.MethodPost, "/closet/looks", map[string]any{
		"name": "Guarda", "item_ids": []string{top.ID.String()},
	})

	// The module offers no delete, on purpose. This asserts the database
	// would refuse one anyway — ON DELETE RESTRICT is what keeps a saved
	// look from silently losing a slot if a delete is ever added.
	_, err := e.pool.Exec(context.Background(),
		`DELETE FROM closet.items WHERE id = $1`, top.ID)
	if err == nil {
		t.Fatal("a piece referenced by a look was deleted; look_items is not RESTRICT")
	}
}
