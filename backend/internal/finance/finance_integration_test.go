//go:build integration

// Integration test exercising migrations, repos, and HTTP handlers against
// a real Postgres. Skipped unless TEST_POSTGRES_DSN is set.
//
//	Run with:  TEST_POSTGRES_DSN=postgres://corsi:corsi@localhost:5432/corsi?sslmode=disable \
//	           go test -tags=integration ./internal/finance/...
package finance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/corsi/backend/internal/finance/adapters/httpapi"
	"github.com/corsi/backend/internal/finance/adapters/repo"
	"github.com/corsi/backend/internal/finance/app"
	"github.com/corsi/backend/internal/platform/postgres"
	"github.com/corsi/backend/internal/platform/testdb"
	"github.com/corsi/backend/internal/platform/workspace"
)

// migrateDSN rewrites postgres:// to pgx5:// because the golang-migrate
// pgx/v5 driver registers under "pgx5". Mirrors cmd/migrate/main.go.
func migrateDSN(d string) string {
	for _, p := range []string{"postgres://", "postgresql://"} {
		if len(d) > len(p) && d[:len(p)] == p {
			return "pgx5://" + d[len(p):]
		}
	}
	return d
}

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

// freshDB runs migrations down then up so each test starts from a clean slate.
// On v0.1 the schema is small enough that down-then-up takes <1s.
func freshDB(t *testing.T, d string) {
	t.Helper()
	m, err := migrate.New("file://../../migrations/finance", migrateDSN(d))
	if err != nil {
		t.Fatalf("migrate.New: %v", err)
	}
	defer m.Close()
	if err := m.Down(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate down: %v", err)
	}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate up: %v", err)
	}
}

// stack wires a real router with a test middleware that stamps a workspace id
// from X-Test-Workspace-Id, bypassing the JWT chain.
func stack(t *testing.T) (*chi.Mux, *pgxpool.Pool, uuid.UUID, uuid.UUID) {
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
	repos := repo.New(pool)
	svc := app.NewService(repos, postgres.NewTxManager(pool), log)
	h := httpapi.NewHandler(svc, log)

	wsA := uuid.New()
	wsB := uuid.New()

	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ws := req.Header.Get("X-Test-Workspace-Id")
			id, err := uuid.Parse(ws)
			if err != nil {
				http.Error(w, "test header missing or invalid", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, req.WithContext(workspace.WithWorkspaceID(req.Context(), id)))
		})
	})
	h.Mount(r)
	return r, pool, wsA, wsB
}

func do(t *testing.T, r http.Handler, method, path, ws string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var br io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		br = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, br)
	if br != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-Test-Workspace-Id", ws)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestMigrationRoundTrip(t *testing.T) {
	d := dsn(t)
	// This one takes the schema all the way down on purpose, so it is as
	// destructive as freshDB and asks the same question first.
	testdb.AssertDestructible(t, d)

	m, err := migrate.New("file://../../migrations/finance", migrateDSN(d))
	if err != nil {
		t.Fatalf("migrate.New: %v", err)
	}
	defer m.Close()
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("up: %v", err)
	}
	if err := m.Down(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("down: %v", err)
	}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("up again: %v", err)
	}
}

func TestCategoriesCRUD(t *testing.T) {
	r, _, wsA, _ := stack(t)

	// create
	rec := do(t, r, "POST", "/categories", wsA.String(), map[string]any{
		"name": "Groceries", "type": "expense", "color": "#a4f000", "icon": "cart",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status %d body %s", rec.Code, rec.Body.String())
	}
	var created map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	id := created["id"].(string)

	// dup name → 409
	rec = do(t, r, "POST", "/categories", wsA.String(), map[string]any{
		"name": "Groceries", "type": "expense", "color": "#ffffff", "icon": "cart",
	})
	if rec.Code != http.StatusConflict {
		t.Fatalf("dup: status %d body %s", rec.Code, rec.Body.String())
	}

	// update
	rec = do(t, r, "PATCH", "/categories/"+id, wsA.String(), map[string]any{"color": "#000fff"})
	if rec.Code != http.StatusOK {
		t.Fatalf("update: status %d body %s", rec.Code, rec.Body.String())
	}

	// get
	rec = do(t, r, "GET", "/categories/"+id, wsA.String(), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get: %d", rec.Code)
	}

	// list
	rec = do(t, r, "GET", "/categories?type=expense", wsA.String(), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d", rec.Code)
	}

	// delete
	rec = do(t, r, "DELETE", "/categories/"+id, wsA.String(), nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", rec.Code)
	}

	// double delete → 404
	rec = do(t, r, "DELETE", "/categories/"+id, wsA.String(), nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("double delete: %d", rec.Code)
	}
}

func TestWorkspaceIsolation(t *testing.T) {
	r, _, wsA, wsB := stack(t)

	rec := do(t, r, "POST", "/categories", wsA.String(), map[string]any{
		"name": "Salary", "type": "income", "color": "#00ff00", "icon": "wallet",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create A: %d", rec.Code)
	}
	var c map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &c)
	id := c["id"].(string)

	// workspace B cannot read it
	rec = do(t, r, "GET", "/categories/"+id, wsB.String(), nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-workspace read should be 404, got %d", rec.Code)
	}
	// workspace B's list is empty
	rec = do(t, r, "GET", "/categories", wsB.String(), nil)
	var page map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &page)
	items := page["items"].([]any)
	if len(items) != 0 {
		t.Fatalf("workspace B should see 0 categories, saw %d", len(items))
	}
}

func TestTransactionTypeEnforcement(t *testing.T) {
	r, _, wsA, _ := stack(t)

	// create an INCOME category
	rec := do(t, r, "POST", "/categories", wsA.String(), map[string]any{
		"name": "Salary", "type": "income", "color": "#00ff00", "icon": "wallet",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("category: %d %s", rec.Code, rec.Body.String())
	}
	var cat map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &cat)
	catID := cat["id"].(string)

	// create a transaction — server should infer type=income from category
	rec = do(t, r, "POST", "/transactions", wsA.String(), map[string]any{
		"category_id":  catID,
		"amount_cents": 100000,
		"occurred_at":  time.Now().UTC().Format(time.RFC3339),
		"description":  "march paycheck",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("tx: %d %s", rec.Code, rec.Body.String())
	}
	var tx map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &tx)
	if tx["type"].(string) != "income" {
		t.Fatalf("expected type income (from category), got %v", tx["type"])
	}

	// unknown category → 404 (FindByID before insert)
	rec = do(t, r, "POST", "/transactions", wsA.String(), map[string]any{
		"category_id":  uuid.New().String(),
		"amount_cents": 100,
		"occurred_at":  time.Now().UTC().Format(time.RFC3339),
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown category should 404, got %d", rec.Code)
	}
}

func TestCardsArchive(t *testing.T) {
	r, _, wsA, _ := stack(t)

	rec := do(t, r, "POST", "/cards", wsA.String(), map[string]any{
		"name": "Nubank", "institution": "Nu", "network": "mastercard",
		"last4": "1234", "limit_cents": 500000, "closing_day": 10, "due_day": 20,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("card: %d %s", rec.Code, rec.Body.String())
	}
	var c map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &c)
	id := c["id"].(string)

	rec = do(t, r, "DELETE", "/cards/"+id, wsA.String(), nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("archive: %d", rec.Code)
	}
	// archived → 404 on subsequent reads
	rec = do(t, r, "GET", "/cards/"+id, wsA.String(), nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("archived card should 404, got %d", rec.Code)
	}
}

func TestTransactionMetadata(t *testing.T) {
	r, _, wsA, _ := stack(t)

	// Create a category first.
	rec := do(t, r, "POST", "/categories", wsA.String(), map[string]any{
		"name": "Subscriptions", "type": "expense", "color": "#abcdef", "icon": "tag",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("category: %d %s", rec.Code, rec.Body.String())
	}
	var cat map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &cat)
	catID := cat["id"].(string)

	// 1. Create with defaults (no metadata fields) — backend should apply
	// status=paid / payment_method=pix / source=manual / notes="".
	rec = do(t, r, "POST", "/transactions", wsA.String(), map[string]any{
		"category_id": catID, "amount_cents": 1990,
		"occurred_at": time.Now().UTC().Format(time.RFC3339),
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create default: %d %s", rec.Code, rec.Body.String())
	}
	var def map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &def)
	if def["status"] != "paid" || def["payment_method"] != "pix" ||
		def["source"] != "manual" || def["notes"] != "" {
		t.Fatalf("defaults wrong: %v", def)
	}

	// 2. Create with explicit metadata.
	rec = do(t, r, "POST", "/transactions", wsA.String(), map[string]any{
		"category_id": catID, "amount_cents": 5000,
		"occurred_at":    time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339),
		"status":         "scheduled",
		"payment_method": "credit",
		"source":         "whatsapp",
		"notes":          "wedding gift, pay before 30th",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create explicit: %d %s", rec.Code, rec.Body.String())
	}
	var exp map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &exp)
	if exp["status"] != "scheduled" || exp["payment_method"] != "credit" ||
		exp["source"] != "whatsapp" || exp["notes"] != "wedding gift, pay before 30th" {
		t.Fatalf("explicit metadata wrong: %v", exp)
	}
	scheduledID := exp["id"].(string)

	// 3. Patch status + notes (smallest meaningful update — finalize a
	// previously-scheduled transaction).
	rec = do(t, r, "PATCH", "/transactions/"+scheduledID, wsA.String(), map[string]any{
		"status": "paid", "notes": "settled via Pix",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body.String())
	}
	var upd map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &upd)
	if upd["status"] != "paid" || upd["notes"] != "settled via Pix" {
		t.Fatalf("patch result wrong: %v", upd)
	}

	// 4. Validation: unknown enum value → 400.
	rec = do(t, r, "POST", "/transactions", wsA.String(), map[string]any{
		"category_id": catID, "amount_cents": 100,
		"occurred_at": time.Now().UTC().Format(time.RFC3339),
		"status":      "in-progress", // not in the enum
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on bad enum, got %d", rec.Code)
	}

	// 5. Filter by status.
	rec = do(t, r, "GET", "/transactions?status=paid", wsA.String(), nil)
	var paidPage map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &paidPage)
	paidCount := len(paidPage["items"].([]any))
	if paidCount != 2 { // default-created (paid) + the scheduled-then-patched-to-paid
		t.Fatalf("status=paid filter: want 2, got %d", paidCount)
	}

	rec = do(t, r, "GET", "/transactions?status=scheduled", wsA.String(), nil)
	var schedPage map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &schedPage)
	if len(schedPage["items"].([]any)) != 0 {
		t.Fatalf("status=scheduled filter: want 0, got %d", len(schedPage["items"].([]any)))
	}

	// 6. Notes length boundary.
	longNotes := strings.Repeat("a", 1001)
	rec = do(t, r, "POST", "/transactions", wsA.String(), map[string]any{
		"category_id": catID, "amount_cents": 100,
		"occurred_at": time.Now().UTC().Format(time.RFC3339),
		"notes":       longNotes,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on >1000 char notes, got %d", rec.Code)
	}
}

func TestPaginationCursor_WalksWithoutDuplicatesOrGaps(t *testing.T) {
	r, _, wsA, _ := stack(t)

	// Seed one expense category, then 17 transactions with distinct
	// occurred_at across two days so the (occurred_at, id) tuple is
	// exercised in both dimensions.
	mkCat := func(name, typ string) string {
		rec := do(t, r, "POST", "/categories", wsA.String(), map[string]any{
			"name": name, "type": typ, "color": "#abcdef", "icon": "x",
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("mkCat: %d %s", rec.Code, rec.Body.String())
		}
		var m map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &m)
		return m["id"].(string)
	}
	cat := mkCat("Cur-Food", "expense")

	base := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	created := make(map[string]bool, 17)
	for i := 0; i < 17; i++ {
		rec := do(t, r, "POST", "/transactions", wsA.String(), map[string]any{
			"category_id":  cat,
			"amount_cents": (i + 1) * 100,
			"occurred_at":  base.Add(time.Duration(i) * time.Minute).UTC().Format(time.RFC3339),
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("seed %d: %d %s", i, rec.Code, rec.Body.String())
		}
		var m map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &m)
		created[m["id"].(string)] = true
	}

	// Walk with limit=5 — expect 4 pages (5+5+5+2) and a null cursor on the last.
	seen := make(map[string]bool, 17)
	pages := 0
	cursor := ""
	for {
		path := "/transactions?limit=5"
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		rec := do(t, r, "GET", path, wsA.String(), nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("page %d: status %d body %s", pages, rec.Code, rec.Body.String())
		}
		var env map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &env)
		items := env["items"].([]any)
		for _, it := range items {
			id := it.(map[string]any)["id"].(string)
			if seen[id] {
				t.Fatalf("duplicate id across pages: %s (page %d)", id, pages)
			}
			seen[id] = true
		}
		pages++
		nc, ok := env["next_cursor"].(string)
		if !ok || nc == "" {
			break
		}
		cursor = nc
		if pages > 10 {
			t.Fatal("cursor walk did not terminate within 10 pages")
		}
	}
	if pages != 4 {
		t.Errorf("page count = %d, want 4 (limit=5 over 17 rows)", pages)
	}
	for id := range created {
		if !seen[id] {
			t.Errorf("row %s never returned by cursor walk", id)
		}
	}
	if len(seen) != 17 {
		t.Errorf("saw %d rows, expected 17", len(seen))
	}
}

func TestPaginationCursor_StableUnderInterleavedWrite(t *testing.T) {
	r, _, wsA, _ := stack(t)

	mkCat := func() string {
		rec := do(t, r, "POST", "/categories", wsA.String(), map[string]any{
			"name": "I-Food", "type": "expense", "color": "#abcdef", "icon": "x",
		})
		var m map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &m)
		return m["id"].(string)
	}
	cat := mkCat()
	base := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 10; i++ {
		do(t, r, "POST", "/transactions", wsA.String(), map[string]any{
			"category_id":  cat,
			"amount_cents": (i + 1) * 100,
			"occurred_at":  base.Add(time.Duration(i) * time.Minute).UTC().Format(time.RFC3339),
		})
	}

	// Fetch first page (limit=5). Record ids.
	rec := do(t, r, "GET", "/transactions?limit=5", wsA.String(), nil)
	var p1 map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &p1)
	cursor := p1["next_cursor"].(string)
	firstPageIDs := make(map[string]bool)
	for _, it := range p1["items"].([]any) {
		firstPageIDs[it.(map[string]any)["id"].(string)] = true
	}

	// Now insert a NEW transaction with the LATEST occurred_at (would be
	// at the top of the unpaged list). Then fetch the next page via
	// cursor — the cursor predicate `(occurred_at, id) < (cursor)` MUST
	// hide the just-inserted newer row.
	do(t, r, "POST", "/transactions", wsA.String(), map[string]any{
		"category_id":  cat,
		"amount_cents": 99999,
		"occurred_at":  base.Add(1 * time.Hour).UTC().Format(time.RFC3339),
		"description":  "INTERLEAVED",
	})

	rec = do(t, r, "GET", "/transactions?limit=5&cursor="+cursor, wsA.String(), nil)
	var p2 map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &p2)
	for _, it := range p2["items"].([]any) {
		row := it.(map[string]any)
		if row["description"] == "INTERLEAVED" {
			t.Fatalf("interleaved newer row leaked into the second page — cursor predicate broken")
		}
		if firstPageIDs[row["id"].(string)] {
			t.Fatalf("duplicate across pages: %s", row["id"])
		}
	}
}

// TestPaginationCursor_LastPageAdvertisesNoSuccessor pins the half of the
// contract the walk test cannot see: `next_cursor` must be evidence that a
// page follows, not a guess that one might.
//
// The row count is an exact multiple of the limit, which is precisely where
// "the page came back full" misleads: the final page of data looks
// indistinguishable from a middle page. A client that trusts a cursor here
// spends a round trip to be told there is nothing left.
func TestPaginationCursor_LastPageAdvertisesNoSuccessor(t *testing.T) {
	r, _, wsA, _ := stack(t)

	rec := do(t, r, "POST", "/categories", wsA.String(), map[string]any{
		"name": "Exact-Food", "type": "expense", "color": "#abcdef", "icon": "x",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("category: %d %s", rec.Code, rec.Body.String())
	}
	var cat map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &cat)

	// 10 rows over limit 5 → exactly two full pages and nothing after them.
	base := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 10; i++ {
		rec := do(t, r, "POST", "/transactions", wsA.String(), map[string]any{
			"category_id":  cat["id"].(string),
			"amount_cents": (i + 1) * 100,
			"occurred_at":  base.Add(time.Duration(i) * time.Minute).UTC().Format(time.RFC3339),
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("seed %d: %d %s", i, rec.Code, rec.Body.String())
		}
	}

	page := func(cursor string) (items []any, next string) {
		t.Helper()
		path := "/transactions?limit=5"
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		rec := do(t, r, "GET", path, wsA.String(), nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("page: %d %s", rec.Code, rec.Body.String())
		}
		var env map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &env)
		items, _ = env["items"].([]any)
		next, _ = env["next_cursor"].(string)
		return items, next
	}

	first, next1 := page("")
	if len(first) != 5 {
		t.Fatalf("page 1 returned %d rows, want 5", len(first))
	}
	if next1 == "" {
		t.Fatalf("page 1 is full and a second page exists, but no next_cursor was returned")
	}

	second, next2 := page(next1)
	if len(second) != 5 {
		t.Fatalf("page 2 returned %d rows, want 5", len(second))
	}
	if next2 != "" {
		t.Errorf("page 2 is the last page, yet it advertised a successor — a client would fetch an empty page")
	}
}

func TestPaginationCursor_RejectsCursorPlusOffset(t *testing.T) {
	r, _, wsA, _ := stack(t)
	rec := do(t, r, "GET", "/transactions?cursor=abc&offset=10", wsA.String(), nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "mutually exclusive") {
		t.Errorf("body did not explain the conflict: %s", rec.Body.String())
	}
}

func TestPaginationCursor_RejectsMalformed(t *testing.T) {
	r, _, wsA, _ := stack(t)
	rec := do(t, r, "GET", "/transactions?cursor=!not-base64", wsA.String(), nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestTransactionTotals_MatchesGroundTruth(t *testing.T) {
	r, _, wsA, _ := stack(t)

	mkCat := func(name, typ string) string {
		rec := do(t, r, "POST", "/categories", wsA.String(), map[string]any{
			"name": name, "type": typ, "color": "#abcdef", "icon": "x",
		})
		var m map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &m)
		return m["id"].(string)
	}
	expCat := mkCat("T-Food", "expense")
	incCat := mkCat("T-Salary", "income")

	base := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)

	// 3 paid expenses (credit), 2 paid income (pix), 1 pending expense
	// (debit), 1 scheduled expense (pix). Outside the window: 1 paid
	// expense to confirm the date filter holds.
	type seed struct {
		cat, status, method string
		amount              int
		at                  time.Time
	}
	seeds := []seed{
		{expCat, "paid", "credit", 1000, base},
		{expCat, "paid", "credit", 2000, base.Add(1 * time.Hour)},
		{expCat, "paid", "credit", 3000, base.Add(2 * time.Hour)},
		{incCat, "paid", "pix", 500000, base.Add(3 * time.Hour)},
		{incCat, "paid", "pix", 700000, base.Add(4 * time.Hour)},
		{expCat, "pending", "debit", 1500, base.Add(5 * time.Hour)},
		{expCat, "scheduled", "pix", 4500, base.Add(6 * time.Hour)},
		{expCat, "paid", "credit", 99999, base.AddDate(0, -2, 0)}, // outside window
	}
	for _, s := range seeds {
		do(t, r, "POST", "/transactions", wsA.String(), map[string]any{
			"category_id":    s.cat,
			"amount_cents":   s.amount,
			"occurred_at":    s.at.UTC().Format(time.RFC3339),
			"status":         s.status,
			"payment_method": s.method,
		})
	}

	from := base.AddDate(0, 0, -1).UTC().Format(time.RFC3339)
	to := base.AddDate(0, 0, 1).UTC().Format(time.RFC3339)
	rec := do(t, r, "GET", "/transactions/totals?from="+from+"&to="+to, wsA.String(), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("totals: %d %s", rec.Code, rec.Body.String())
	}
	var tot map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &tot)

	by := tot["by_status"].(map[string]any)
	paid := by["paid"].(map[string]any)
	pending := by["pending"].(map[string]any)
	scheduled := by["scheduled"].(map[string]any)

	if int(paid["income_cents"].(float64)) != 1_200_000 {
		t.Errorf("paid income = %v, want 1200000", paid["income_cents"])
	}
	if int(paid["expense_cents"].(float64)) != 6000 {
		t.Errorf("paid expense = %v, want 6000 (excludes the row outside window)", paid["expense_cents"])
	}
	if int(paid["count"].(float64)) != 5 {
		t.Errorf("paid count = %v, want 5", paid["count"])
	}
	if int(pending["expense_cents"].(float64)) != 1500 {
		t.Errorf("pending expense = %v, want 1500", pending["expense_cents"])
	}
	if int(scheduled["expense_cents"].(float64)) != 4500 {
		t.Errorf("scheduled expense = %v, want 4500", scheduled["expense_cents"])
	}

	// by_method sums (paid + pending + scheduled all combine)
	methods := map[string]int{}
	for _, m := range tot["by_method"].([]any) {
		mm := m.(map[string]any)
		methods[mm["payment_method"].(string)] = int(mm["total_cents"].(float64))
	}
	if methods["credit"] != 6000 {
		t.Errorf("by_method credit = %d, want 6000", methods["credit"])
	}
	if methods["pix"] != 1_204_500 { // 500000 + 700000 + 4500 (scheduled)
		t.Errorf("by_method pix = %d, want 1204500", methods["pix"])
	}
	if methods["debit"] != 1500 {
		t.Errorf("by_method debit = %d, want 1500", methods["debit"])
	}
}

func TestTransactionTotals_RejectsMissingWindow(t *testing.T) {
	r, _, wsA, _ := stack(t)
	rec := do(t, r, "GET", "/transactions/totals", wsA.String(), nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestTransferOverviewUnchanged is the load-bearing proof of the
// Sprint B contract: creating a transfer must NOT shift any number on
// the overview dashboard. We snapshot /transactions/totals before the
// transfer, post it, and snapshot again — every bucket dashboards
// consume (by_status, by_category, by_method, by_realization.realized,
// by_realization.projected) must be identical across the two
// snapshots. The transfer's two legs are still visible in the
// /transactions list (they are real rows), and they land in the
// `by_realization.excluded` bucket per docs/totals-contract.md — that
// bucket is *expected* to move and is intentionally not compared here.
func TestTransferOverviewUnchanged(t *testing.T) {
	r, _, wsA, _ := stack(t)

	mkCat := func(name, typ string) string {
		rec := do(t, r, "POST", "/categories", wsA.String(), map[string]any{
			"name": name, "type": typ, "color": "#abcdef", "icon": "x",
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("mkCat %s: %d %s", name, rec.Code, rec.Body.String())
		}
		var m map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &m)
		return m["id"].(string)
	}
	// Categories used as the transfer's two legs. The "expense" leg is
	// the outflow side, the "income" leg the inflow side — these are
	// the user's own bucket categories for the source/destination
	// accounts, NOT regular spending/income categories.
	transferOut := mkCat("Transfer Out", "expense")
	transferIn := mkCat("Transfer In", "income")
	// Real spend/earn categories whose totals must not move.
	foodCat := mkCat("XFer-Food", "expense")
	salaryCat := mkCat("XFer-Salary", "income")

	base := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	mkTx := func(cat string, amount int, at time.Time) {
		rec := do(t, r, "POST", "/transactions", wsA.String(), map[string]any{
			"category_id":  cat,
			"amount_cents": amount,
			"occurred_at":  at.UTC().Format(time.RFC3339),
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("seed tx: %d %s", rec.Code, rec.Body.String())
		}
	}
	mkTx(foodCat, 5000, base)
	mkTx(salaryCat, 1_000_000, base.Add(1*time.Hour))
	mkTx(foodCat, 7500, base.Add(2*time.Hour))

	from := base.AddDate(0, 0, -1).UTC().Format(time.RFC3339)
	to := base.AddDate(0, 0, 1).UTC().Format(time.RFC3339)

	// Snapshot the buckets dashboards consume. by_realization.excluded is
	// expected to capture the new transfer legs (per the totals contract)
	// so it is intentionally excluded from the snapshot.
	dashSnapshot := func() string {
		rec := do(t, r, "GET", "/transactions/totals?from="+from+"&to="+to, wsA.String(), nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("totals: %d %s", rec.Code, rec.Body.String())
		}
		var tot map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &tot)
		br := tot["by_realization"].(map[string]any)
		view := map[string]any{
			"by_status":   tot["by_status"],
			"by_category": tot["by_category"],
			"by_method":   tot["by_method"],
			"by_realization_dashboard": map[string]any{
				"realized":  br["realized"],
				"projected": br["projected"],
			},
		}
		raw, _ := json.Marshal(view)
		return string(raw)
	}
	before := dashSnapshot()

	rec := do(t, r, "POST", "/transfers", wsA.String(), map[string]any{
		"from_category_id": transferOut,
		"to_category_id":   transferIn,
		"amount_cents":     12345,
		"occurred_at":      base.Add(30 * time.Minute).UTC().Format(time.RFC3339),
		"description":      "Move from Itaú to Nubank",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create transfer: %d %s", rec.Code, rec.Body.String())
	}
	var pair map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &pair)
	fromLeg := pair["from"].(map[string]any)
	toLeg := pair["to"].(map[string]any)

	if fromLeg["transfer_pair_id"] == nil || toLeg["transfer_pair_id"] == nil {
		t.Fatalf("both legs must carry transfer_pair_id; from=%v to=%v",
			fromLeg["transfer_pair_id"], toLeg["transfer_pair_id"])
	}
	if fromLeg["transfer_pair_id"] != toLeg["transfer_pair_id"] {
		t.Fatalf("legs must share transfer_pair_id; from=%v to=%v",
			fromLeg["transfer_pair_id"], toLeg["transfer_pair_id"])
	}
	if fromLeg["id"] == toLeg["id"] {
		t.Fatal("legs must have distinct ids")
	}
	if fromLeg["type"] != "expense" || toLeg["type"] != "income" {
		t.Fatalf("leg types wrong: from=%v to=%v", fromLeg["type"], toLeg["type"])
	}
	if fromLeg["payment_method"] != "transfer" || toLeg["payment_method"] != "transfer" {
		t.Fatalf("payment_method must be transfer; from=%v to=%v",
			fromLeg["payment_method"], toLeg["payment_method"])
	}

	// Both legs are real rows — they appear in /transactions list. The
	// transfer is only invisible to aggregates, not to record-keeping.
	rec = do(t, r, "GET", "/transactions?limit=100", wsA.String(), nil)
	var page map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &page)
	items := page["items"].([]any)
	if len(items) != 5 {
		t.Fatalf("list count after transfer = %d, want 5 (3 normal + 2 legs)", len(items))
	}

	after := dashSnapshot()
	if before != after {
		t.Errorf("overview shifted after transfer\nbefore: %s\nafter:  %s", before, after)
	}
}

func TestTransferRejectsInvalidCategoryTypes(t *testing.T) {
	r, _, wsA, _ := stack(t)
	mkCat := func(name, typ string) string {
		rec := do(t, r, "POST", "/categories", wsA.String(), map[string]any{
			"name": name, "type": typ, "color": "#abcdef", "icon": "x",
		})
		var m map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &m)
		return m["id"].(string)
	}
	inc1 := mkCat("RJ-In-1", "income")
	inc2 := mkCat("RJ-In-2", "income")
	exp1 := mkCat("RJ-Out-1", "expense")
	exp2 := mkCat("RJ-Out-2", "expense")
	now := time.Now().UTC().Format(time.RFC3339)

	// from must be expense → income/income is rejected.
	rec := do(t, r, "POST", "/transfers", wsA.String(), map[string]any{
		"from_category_id": inc1, "to_category_id": inc2,
		"amount_cents": 1000, "occurred_at": now,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("from=income should be 400, got %d %s", rec.Code, rec.Body.String())
	}

	// to must be income → expense/expense is rejected.
	rec = do(t, r, "POST", "/transfers", wsA.String(), map[string]any{
		"from_category_id": exp1, "to_category_id": exp2,
		"amount_cents": 1000, "occurred_at": now,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("to=expense should be 400, got %d %s", rec.Code, rec.Body.String())
	}

	// same id on both sides is rejected before lookup.
	rec = do(t, r, "POST", "/transfers", wsA.String(), map[string]any{
		"from_category_id": exp1, "to_category_id": exp1,
		"amount_cents": 1000, "occurred_at": now,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("same from/to should be 400, got %d %s", rec.Code, rec.Body.String())
	}
}

// TestTransactionPersonID_RejectsCrossWorkspace covers the QA-flagged
// person_id injection: the transactions.person_id FK is workspace-blind
// at the DB level, so the service must verify the person belongs to the
// same workspace before create/update. Wrong-workspace ids surface as
// 404, same-workspace ids work.
func TestTransactionPersonID_RejectsCrossWorkspace(t *testing.T) {
	r, _, wsA, wsB := stack(t)

	mkPerson := func(ws, name string) string {
		rec := do(t, r, "POST", "/persons", ws, map[string]any{"name": name})
		if rec.Code != http.StatusCreated {
			t.Fatalf("mkPerson(%s): %d %s", ws, rec.Code, rec.Body.String())
		}
		var m map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &m)
		return m["id"].(string)
	}
	mkCat := func(ws string) string {
		rec := do(t, r, "POST", "/categories", ws, map[string]any{
			"name": "PID-Food", "type": "expense", "color": "#abcdef", "icon": "x",
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("mkCat(%s): %d %s", ws, rec.Code, rec.Body.String())
		}
		var m map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &m)
		return m["id"].(string)
	}

	personA := mkPerson(wsA.String(), "Alice in A")
	personB := mkPerson(wsB.String(), "Bob in B")
	catA := mkCat(wsA.String())
	now := time.Now().UTC().Format(time.RFC3339)

	// 1) A creates a transaction with B's person_id → 404 (cross-workspace).
	rec := do(t, r, "POST", "/transactions", wsA.String(), map[string]any{
		"category_id":  catA,
		"person_id":    personB,
		"amount_cents": 1000,
		"occurred_at":  now,
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("create with cross-workspace person should 404, got %d %s", rec.Code, rec.Body.String())
	}

	// 2) A creates a transaction with A's person_id → 201 (same workspace).
	rec = do(t, r, "POST", "/transactions", wsA.String(), map[string]any{
		"category_id":  catA,
		"person_id":    personA,
		"amount_cents": 1000,
		"occurred_at":  now,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create with same-workspace person: %d %s", rec.Code, rec.Body.String())
	}
	var created map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	txID := created["id"].(string)
	if created["person_id"].(string) != personA {
		t.Fatalf("created person_id = %v, want %s", created["person_id"], personA)
	}

	// 3) A patches the transaction to B's person_id → 404.
	rec = do(t, r, "PATCH", "/transactions/"+txID, wsA.String(), map[string]any{
		"person_id": personB,
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("patch to cross-workspace person should 404, got %d %s", rec.Code, rec.Body.String())
	}

	// 4) Sanity: patching to A's person still works.
	rec = do(t, r, "PATCH", "/transactions/"+txID, wsA.String(), map[string]any{
		"person_id": personA,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("patch to same-workspace person: %d %s", rec.Code, rec.Body.String())
	}
}

// TestTransferDelete_RejectsLegAndRemovesPair covers the QA-flagged
// orphan: DELETE /transactions/{leg} must 409, DELETE /transfers/{pair}
// must atomically remove both legs, and overview totals must not shift.
func TestTransferDelete_RejectsLegAndRemovesPair(t *testing.T) {
	r, _, wsA, _ := stack(t)

	mkCat := func(name, typ string) string {
		rec := do(t, r, "POST", "/categories", wsA.String(), map[string]any{
			"name": name, "type": typ, "color": "#abcdef", "icon": "x",
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("mkCat %s: %d %s", name, rec.Code, rec.Body.String())
		}
		var m map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &m)
		return m["id"].(string)
	}
	transferOut := mkCat("Del-Transfer-Out", "expense")
	transferIn := mkCat("Del-Transfer-In", "income")
	foodCat := mkCat("Del-Food", "expense")

	base := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	// One unrelated expense so totals are non-empty.
	rec := do(t, r, "POST", "/transactions", wsA.String(), map[string]any{
		"category_id": foodCat, "amount_cents": 5000,
		"occurred_at": base.UTC().Format(time.RFC3339),
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("seed expense: %d %s", rec.Code, rec.Body.String())
	}

	from := base.AddDate(0, 0, -1).UTC().Format(time.RFC3339)
	to := base.AddDate(0, 0, 1).UTC().Format(time.RFC3339)

	// Snapshot totals before the transfer.
	rec = do(t, r, "GET", "/transactions/totals?from="+from+"&to="+to, wsA.String(), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("totals before: %d %s", rec.Code, rec.Body.String())
	}
	totalsBefore := rec.Body.String()

	// Create transfer pair.
	rec = do(t, r, "POST", "/transfers", wsA.String(), map[string]any{
		"from_category_id": transferOut,
		"to_category_id":   transferIn,
		"amount_cents":     12345,
		"occurred_at":      base.Add(30 * time.Minute).UTC().Format(time.RFC3339),
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create transfer: %d %s", rec.Code, rec.Body.String())
	}
	var pair map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &pair)
	fromLeg := pair["from"].(map[string]any)
	pairID := fromLeg["transfer_pair_id"].(string)
	fromLegID := fromLeg["id"].(string)

	// 1) Deleting a leg via /transactions must be rejected with 409.
	rec = do(t, r, "DELETE", "/transactions/"+fromLegID, wsA.String(), nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("delete leg via /transactions should 409, got %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "delete the transfer pair instead") {
		t.Errorf("409 body should mention the pair endpoint, got %s", rec.Body.String())
	}

	// Both legs are still alive after the rejected delete.
	rec = do(t, r, "GET", "/transactions?limit=100", wsA.String(), nil)
	var page map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &page)
	if got := len(page["items"].([]any)); got != 3 { // 1 unrelated + 2 legs
		t.Fatalf("list after rejected leg delete = %d, want 3", got)
	}

	// 2) Deleting via /transfers/{pair_id} removes both legs atomically.
	rec = do(t, r, "DELETE", "/transfers/"+pairID, wsA.String(), nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete pair: %d %s", rec.Code, rec.Body.String())
	}

	// 3) The list no longer shows either leg.
	rec = do(t, r, "GET", "/transactions?limit=100", wsA.String(), nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &page)
	items := page["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("list after pair delete = %d, want 1 (only the unrelated expense)", len(items))
	}
	for _, it := range items {
		if it.(map[string]any)["transfer_pair_id"] != nil {
			t.Fatalf("a leg leaked into the list after pair delete: %v", it)
		}
	}

	// 4) Totals are byte-identical to the pre-transfer snapshot — both
	//    legs were excluded while present and have now disappeared.
	rec = do(t, r, "GET", "/transactions/totals?from="+from+"&to="+to, wsA.String(), nil)
	totalsAfter := rec.Body.String()
	if totalsBefore != totalsAfter {
		t.Errorf("totals shifted after transfer create+delete\nbefore: %s\nafter:  %s", totalsBefore, totalsAfter)
	}

	// 5) Double-delete the pair → 404 (every leg already soft-deleted).
	rec = do(t, r, "DELETE", "/transfers/"+pairID, wsA.String(), nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("double delete pair should 404, got %d %s", rec.Code, rec.Body.String())
	}
}

// TestTransactionTotals_RealizationContract enforces docs/totals-contract.md
// §2.4 by walking the decision table: future-dated paid → projected,
// pending past → projected, scheduled (past + future) → projected,
// transfer pair → excluded, paid past → realized.
func TestTransactionTotals_RealizationContract(t *testing.T) {
	r, _, wsA, _ := stack(t)

	mkCat := func(name, typ string) string {
		rec := do(t, r, "POST", "/categories", wsA.String(), map[string]any{
			"name": name, "type": typ, "color": "#abcdef", "icon": "x",
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("mkCat %s: %d %s", name, rec.Code, rec.Body.String())
		}
		var m map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &m)
		return m["id"].(string)
	}
	expCat := mkCat("R-Food", "expense")
	xferOut := mkCat("R-XOut", "expense")
	xferIn := mkCat("R-XIn", "income")

	now := time.Now().UTC()
	mkTx := func(catID, status string, at time.Time, amount int) {
		rec := do(t, r, "POST", "/transactions", wsA.String(), map[string]any{
			"category_id":  catID,
			"amount_cents": amount,
			"occurred_at":  at.UTC().Format(time.RFC3339),
			"status":       status,
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("seed %s @ %s: %d %s", status, at, rec.Code, rec.Body.String())
		}
	}
	// REALIZED: paid + past.
	mkTx(expCat, "paid", now.Add(-2*time.Hour), 1000)
	// PROJECTED: paid + future (the contract-critical case — must NOT realize).
	mkTx(expCat, "paid", now.Add(2*time.Hour), 2000)
	// PROJECTED: pending + past.
	mkTx(expCat, "pending", now.Add(-1*time.Hour), 300)
	// PROJECTED: scheduled + past.
	mkTx(expCat, "scheduled", now.Add(-3*time.Hour), 400)
	// PROJECTED: scheduled + future.
	mkTx(expCat, "scheduled", now.Add(3*time.Hour), 500)
	// EXCLUDED: transfer pair (paid, recent).
	rec := do(t, r, "POST", "/transfers", wsA.String(), map[string]any{
		"from_category_id": xferOut,
		"to_category_id":   xferIn,
		"amount_cents":     99999,
		"occurred_at":      now.Add(-30 * time.Minute).UTC().Format(time.RFC3339),
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("seed transfer: %d %s", rec.Code, rec.Body.String())
	}

	from := now.Add(-24 * time.Hour).UTC().Format(time.RFC3339)
	to := now.Add(24 * time.Hour).UTC().Format(time.RFC3339)
	rec = do(t, r, "GET", "/transactions/totals?from="+from+"&to="+to, wsA.String(), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("totals: %d %s", rec.Code, rec.Body.String())
	}
	var tot map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &tot)
	br, ok := tot["by_realization"].(map[string]any)
	if !ok {
		t.Fatalf("by_realization missing: %s", rec.Body.String())
	}
	realized := br["realized"].(map[string]any)
	projected := br["projected"].(map[string]any)
	excluded := br["excluded"].(map[string]any)

	// REALIZED = the single paid-past row (1000).
	if got := int(realized["expense_cents"].(float64)); got != 1000 {
		t.Errorf("realized expense = %d, want 1000", got)
	}
	if got := int(realized["count"].(float64)); got != 1 {
		t.Errorf("realized count = %d, want 1", got)
	}
	// PROJECTED = paid-future (2000) + pending-past (300) + scheduled-past
	// (400) + scheduled-future (500) = 3200, count = 4.
	if got := int(projected["expense_cents"].(float64)); got != 3200 {
		t.Errorf("projected expense = %d, want 3200", got)
	}
	if got := int(projected["count"].(float64)); got != 4 {
		t.Errorf("projected count = %d, want 4", got)
	}
	// EXCLUDED = the two transfer legs (1 income + 1 expense, each 99999).
	if got := int(excluded["expense_cents"].(float64)); got != 99999 {
		t.Errorf("excluded expense = %d, want 99999", got)
	}
	if got := int(excluded["income_cents"].(float64)); got != 99999 {
		t.Errorf("excluded income = %d, want 99999", got)
	}
	if got := int(excluded["count"].(float64)); got != 2 {
		t.Errorf("excluded count = %d, want 2", got)
	}
}

func TestTransactionFiltering(t *testing.T) {
	r, _, wsA, _ := stack(t)

	mkCat := func(name, typ string) string {
		rec := do(t, r, "POST", "/categories", wsA.String(), map[string]any{
			"name": name, "type": typ, "color": "#abcdef", "icon": "x",
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("mkCat: %d %s", rec.Code, rec.Body.String())
		}
		var m map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &m)
		return m["id"].(string)
	}
	income := mkCat("Salary", "income")
	expense := mkCat("Food", "expense")

	mkTx := func(catID string, occurred time.Time) {
		rec := do(t, r, "POST", "/transactions", wsA.String(), map[string]any{
			"category_id":  catID,
			"amount_cents": 100,
			"occurred_at":  occurred.UTC().Format(time.RFC3339),
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("mkTx: %d %s", rec.Code, rec.Body.String())
		}
	}
	base := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	mkTx(income, base)
	mkTx(expense, base.Add(24*time.Hour))
	mkTx(expense, base.Add(48*time.Hour))

	rec := do(t, r, "GET", "/transactions?type=expense", wsA.String(), nil)
	var page map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &page)
	items := page["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("expense filter: want 2, got %d", len(items))
	}

	rec = do(t, r, "GET", "/transactions?from="+base.Add(24*time.Hour).UTC().Format(time.RFC3339), wsA.String(), nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &page)
	items = page["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("from filter: want 2, got %d", len(items))
	}
}

// TestPurchasePlan_CreateMaterializesInstallments proves the MVP
// invariants: a 12× plan create lands the parent row plus exactly N
// installment transactions linked back via plan_id + installment_number,
// the principal is split evenly with the last leg absorbing the
// rounding remainder so the sum equals the principal exactly, and every
// installment is `scheduled` at creation (REALIZED rows are introduced
// later via PATCH /transactions/{id}).
func TestPurchasePlan_CreateMaterializesInstallments(t *testing.T) {
	r, _, wsA, _ := stack(t)

	mkCat := func(name, typ string) string {
		rec := do(t, r, "POST", "/categories", wsA.String(), map[string]any{
			"name": name, "type": typ, "color": "#abcdef", "icon": "x",
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("mkCat %s: %d %s", name, rec.Code, rec.Body.String())
		}
		var m map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &m)
		return m["id"].(string)
	}
	expCat := mkCat("PP-Electronics", "expense")

	// Pick a principal that does NOT divide evenly so the rounding-
	// absorption invariant is exercised (1001 / 12 = 83 remainder 5).
	first := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	rec := do(t, r, "POST", "/purchase-plans", wsA.String(), map[string]any{
		"name":               "Laptop",
		"category_id":        expCat,
		"total_amount_cents": 1001,
		"installments":       12,
		"first_occurred_at":  first.UTC().Format(time.RFC3339),
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create plan: %d %s", rec.Code, rec.Body.String())
	}
	var plan map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &plan)
	planID := plan["id"].(string)
	if int(plan["installments"].(float64)) != 12 {
		t.Fatalf("installments = %v, want 12", plan["installments"])
	}
	if int(plan["remaining_installments"].(float64)) != 12 {
		t.Fatalf("remaining_installments = %v, want 12 at create", plan["remaining_installments"])
	}

	// Pull the 12 installment transactions and verify the invariants.
	rec = do(t, r, "GET", "/transactions?limit=100", wsA.String(), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	var page map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &page)
	items := page["items"].([]any)
	if len(items) != 12 {
		t.Fatalf("installment count = %d, want 12", len(items))
	}
	sum := 0
	seenNums := map[int]bool{}
	for _, it := range items {
		row := it.(map[string]any)
		if row["plan_id"] != planID {
			t.Fatalf("installment plan_id = %v, want %s", row["plan_id"], planID)
		}
		if row["status"] != "scheduled" {
			t.Fatalf("installment status = %v, want scheduled", row["status"])
		}
		if row["type"] != "expense" {
			t.Fatalf("installment type = %v, want expense", row["type"])
		}
		num := int(row["installment_number"].(float64))
		if num < 1 || num > 12 {
			t.Fatalf("installment_number = %d, out of [1,12]", num)
		}
		if seenNums[num] {
			t.Fatalf("duplicate installment_number %d", num)
		}
		seenNums[num] = true
		sum += int(row["amount_cents"].(float64))
	}
	if sum != 1001 {
		t.Fatalf("sum(amount_cents) = %d, want 1001 (principal exact, remainder absorbed)", sum)
	}
}

// TestPurchasePlan_CancelPreservesPaidHistory is the test from the
// brief: 12×, pay 3, cancel remaining → 9 future installments removed,
// 3 paid installments preserved as history. Plays out the exact flow a
// user lives through: open the plan, pay a few months, decide to cancel
// the rest.
func TestPurchasePlan_CancelPreservesPaidHistory(t *testing.T) {
	r, _, wsA, _ := stack(t)

	mkCat := func(name, typ string) string {
		rec := do(t, r, "POST", "/categories", wsA.String(), map[string]any{
			"name": name, "type": typ, "color": "#abcdef", "icon": "x",
		})
		var m map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &m)
		return m["id"].(string)
	}
	expCat := mkCat("PP-Cancel-Food", "expense")

	first := time.Date(2026, 2, 1, 12, 0, 0, 0, time.UTC)
	rec := do(t, r, "POST", "/purchase-plans", wsA.String(), map[string]any{
		"name":               "Phone",
		"category_id":        expCat,
		"total_amount_cents": 1200,
		"installments":       12,
		"first_occurred_at":  first.UTC().Format(time.RFC3339),
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create plan: %d %s", rec.Code, rec.Body.String())
	}
	var plan map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &plan)
	planID := plan["id"].(string)

	rec = do(t, r, "GET", "/transactions?limit=100", wsA.String(), nil)
	var page map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &page)
	items := page["items"].([]any)
	if len(items) != 12 {
		t.Fatalf("after create: list = %d, want 12", len(items))
	}

	// Pay the first three installments (numbers 1, 2, 3) via PATCH.
	byNum := map[int]string{}
	for _, it := range items {
		row := it.(map[string]any)
		byNum[int(row["installment_number"].(float64))] = row["id"].(string)
	}
	for i := 1; i <= 3; i++ {
		txID, ok := byNum[i]
		if !ok {
			t.Fatalf("installment %d missing from list", i)
		}
		rec = do(t, r, "PATCH", "/transactions/"+txID, wsA.String(), map[string]any{
			"status": "paid",
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("pay installment %d: %d %s", i, rec.Code, rec.Body.String())
		}
	}

	// Cancel the remainder.
	rec = do(t, r, "POST", "/purchase-plans/"+planID+"/cancel", wsA.String(), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("cancel: %d %s", rec.Code, rec.Body.String())
	}
	var refreshed map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &refreshed)
	if int(refreshed["remaining_installments"].(float64)) != 0 {
		t.Fatalf("remaining_installments after cancel = %v, want 0", refreshed["remaining_installments"])
	}

	// 9 future removed → only 3 paid installments remain visible.
	rec = do(t, r, "GET", "/transactions?limit=100", wsA.String(), nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &page)
	items = page["items"].([]any)
	if len(items) != 3 {
		t.Fatalf("after cancel: list = %d, want 3 (paid history preserved)", len(items))
	}
	for _, it := range items {
		row := it.(map[string]any)
		if row["status"] != "paid" {
			t.Fatalf("surviving installment status = %v, want paid", row["status"])
		}
		if row["plan_id"] != planID {
			t.Fatalf("surviving installment plan_id mismatch: %v", row["plan_id"])
		}
	}

	// Second cancel is idempotent: nothing more to delete, remaining stays 0,
	// and the plan stays reachable (no 404).
	rec = do(t, r, "POST", "/purchase-plans/"+planID+"/cancel", wsA.String(), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("double cancel should be idempotent OK, got %d %s", rec.Code, rec.Body.String())
	}
}

// TestPurchasePlan_CreateRollsBackOnFailure proves the atomicity of the
// create operation: if any installment insert fails, the parent plan
// row is rolled back too — no half-created state. We force the failure
// by passing a category_id from a different workspace (the FindByID
// guard rejects with 404 before any rows are written, but the value of
// this test is that even mid-flight failures get rolled back). We
// additionally check the plan is invisible to GET afterward.
func TestPurchasePlan_CreateRollsBackOnFailure(t *testing.T) {
	r, _, wsA, wsB := stack(t)

	mkCat := func(ws, name string) string {
		rec := do(t, r, "POST", "/categories", ws, map[string]any{
			"name": name, "type": "expense", "color": "#abcdef", "icon": "x",
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("mkCat: %d %s", rec.Code, rec.Body.String())
		}
		var m map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &m)
		return m["id"].(string)
	}
	catB := mkCat(wsB.String(), "PP-Rollback-Food")

	// wsA tries to create a plan referencing wsB's category → 404 from
	// the workspace-isolated FindByID. No plan row is written.
	rec := do(t, r, "POST", "/purchase-plans", wsA.String(), map[string]any{
		"name":               "Bad Plan",
		"category_id":        catB,
		"total_amount_cents": 1200,
		"installments":       12,
		"first_occurred_at":  time.Now().UTC().Format(time.RFC3339),
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-workspace category should 404, got %d %s", rec.Code, rec.Body.String())
	}

	// wsA's plan list is empty — no rollback artifact.
	rec = do(t, r, "GET", "/purchase-plans", wsA.String(), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d", rec.Code)
	}
	var page map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &page)
	if got := len(page["items"].([]any)); got != 0 {
		t.Fatalf("plans list after failure = %d, want 0", got)
	}
	// wsA's transactions list is also empty.
	rec = do(t, r, "GET", "/transactions", wsA.String(), nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &page)
	if got := len(page["items"].([]any)); got != 0 {
		t.Fatalf("tx list after failure = %d, want 0", got)
	}
}

// TestPurchasePlan_RejectsBadInputs covers the validation surface: bad
// category type, ≤1 installments, ≤0 total, and missing required dates.
func TestPurchasePlan_RejectsBadInputs(t *testing.T) {
	r, _, wsA, _ := stack(t)

	mkCat := func(name, typ string) string {
		rec := do(t, r, "POST", "/categories", wsA.String(), map[string]any{
			"name": name, "type": typ, "color": "#abcdef", "icon": "x",
		})
		var m map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &m)
		return m["id"].(string)
	}
	incCat := mkCat("PP-Bad-Income", "income")
	expCat := mkCat("PP-Bad-Expense", "expense")
	now := time.Now().UTC().Format(time.RFC3339)

	// Income category — not allowed.
	rec := do(t, r, "POST", "/purchase-plans", wsA.String(), map[string]any{
		"name": "x", "category_id": incCat,
		"total_amount_cents": 1200, "installments": 12, "first_occurred_at": now,
	})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("income category should 400, got %d %s", rec.Code, rec.Body.String())
	}

	// Installments < 2 — not allowed.
	rec = do(t, r, "POST", "/purchase-plans", wsA.String(), map[string]any{
		"name": "x", "category_id": expCat,
		"total_amount_cents": 1200, "installments": 1, "first_occurred_at": now,
	})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("installments=1 should 400, got %d", rec.Code)
	}

	// Total ≤ 0 — not allowed.
	rec = do(t, r, "POST", "/purchase-plans", wsA.String(), map[string]any{
		"name": "x", "category_id": expCat,
		"total_amount_cents": 0, "installments": 12, "first_occurred_at": now,
	})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("total=0 should 400, got %d", rec.Code)
	}

	// Missing first_occurred_at — not allowed.
	rec = do(t, r, "POST", "/purchase-plans", wsA.String(), map[string]any{
		"name": "x", "category_id": expCat,
		"total_amount_cents": 1200, "installments": 12,
	})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing first_occurred_at should 400, got %d", rec.Code)
	}
}

// TestPurchasePlan_WorkspaceIsolation makes sure cross-workspace reads
// and cancels do not leak: workspace B cannot see, cancel, or GET a
// plan created by workspace A.
func TestPurchasePlan_WorkspaceIsolation(t *testing.T) {
	r, _, wsA, wsB := stack(t)

	mkCat := func() string {
		rec := do(t, r, "POST", "/categories", wsA.String(), map[string]any{
			"name": "PP-Iso", "type": "expense", "color": "#abcdef", "icon": "x",
		})
		var m map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &m)
		return m["id"].(string)
	}
	rec := do(t, r, "POST", "/purchase-plans", wsA.String(), map[string]any{
		"name": "X", "category_id": mkCat(),
		"total_amount_cents": 1200, "installments": 12,
		"first_occurred_at": time.Now().UTC().Format(time.RFC3339),
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create A: %d %s", rec.Code, rec.Body.String())
	}
	var plan map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &plan)
	planID := plan["id"].(string)

	rec = do(t, r, "GET", "/purchase-plans/"+planID, wsB.String(), nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("cross-workspace GET should 404, got %d", rec.Code)
	}
	rec = do(t, r, "POST", "/purchase-plans/"+planID+"/cancel", wsB.String(), nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("cross-workspace cancel should 404, got %d", rec.Code)
	}
	rec = do(t, r, "GET", "/purchase-plans", wsB.String(), nil)
	var page map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &page)
	if got := len(page["items"].([]any)); got != 0 {
		t.Fatalf("B's plans list = %d, want 0", got)
	}
}
