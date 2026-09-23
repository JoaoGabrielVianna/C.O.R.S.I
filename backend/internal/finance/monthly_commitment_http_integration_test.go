//go:build integration

// Monthly commitment over HTTP.
//
// The routes the future screen will drive, exercised through the real chi
// router, the real middleware contract and the real handlers. What this
// file adds over the application-layer suite is the wire: route ordering,
// the period in a path segment, the two verbs on /pay, and the fact that
// no route exists through which an occurrence could be created or removed.
package finance

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/corsi/backend/internal/finance/domain"
)

/* ── harness ─────────────────────────────────────────────────────────── */

// commitmentStack seeds one monthly recurring entry and hands back the
// router, so each test starts from a month that has something in it.
func commitmentStack(t *testing.T) (chi.Router, *pgxpool.Pool, uuid.UUID, uuid.UUID, uuid.UUID) {
	t.Helper()
	r, pool, wsA, wsB := stack(t)

	catID := uuid.New()
	entryID := uuid.New()
	ctx := context.Background()
	if _, err := pool.Exec(ctx,
		`INSERT INTO finance.categories (id, workspace_id, name, type, color, icon)
		 VALUES ($1,$2,'Fixture','expense','#123456','tag')`, catID, wsA); err != nil {
		t.Fatalf("seed category: %v", err)
	}
	// Backdated a year so every month this suite asks about is one the
	// definition was already applicable in.
	if _, err := pool.Exec(ctx,
		`INSERT INTO finance.recurring_entries
		   (id, workspace_id, description, amount_cents, category_id, due_day,
		    recurrence, status, starts_at)
		 VALUES ($1,$2,'fixture recurring',250000,$3,5,'monthly','active', now() - interval '1 year')`,
		entryID, wsA, catID); err != nil {
		t.Fatalf("seed recurring entry: %v", err)
	}
	return r, pool, wsA, wsB, entryID
}

// serverMonth is the month the DATABASE is in, in the zone the test stack
// was built with (UTC — see stack()). Read from Postgres, never from this
// process, so an assertion cannot disagree with the handler it is testing.
func serverMonth(t *testing.T, pool *pgxpool.Pool) domain.Period {
	t.Helper()
	var now time.Time
	if err := pool.QueryRow(context.Background(), `SELECT now()`).Scan(&now); err != nil {
		t.Fatalf("clock: %v", err)
	}
	return domain.PeriodOf(now, time.UTC)
}

func decodeCommitment(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	return out
}

func numField(t *testing.T, m map[string]any, key string) int64 {
	t.Helper()
	v, ok := m[key].(float64)
	if !ok {
		t.Fatalf("%q is %T, want a number", key, m[key])
	}
	return int64(v)
}

/* ── GET /commitment ─────────────────────────────────────────────────── */

func TestHTTPCommitmentCurrentMonth(t *testing.T) {
	r, pool, wsA, _, entryID := commitmentStack(t)

	rec := do(t, r, "GET", "/recurring-entries/commitment", wsA.String(), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	body := decodeCommitment(t, rec.Body.Bytes())

	if got := body["period"]; got != serverMonth(t, pool).String() {
		t.Fatalf("period is %v, want the server's current month", got)
	}
	if body["is_projection"] != false {
		t.Fatal("the current month came back as a projection")
	}
	if body["today"] == "" || body["time_zone"] == "" {
		t.Fatalf("the month carries no authoritative date: %v", body)
	}
	if got := numField(t, body, "committed_cents"); got != 250000 {
		t.Fatalf("committed_cents is %d", got)
	}
	if got := numField(t, body, "remaining_cents"); got != 250000 {
		t.Fatalf("remaining_cents is %d", got)
	}
	// Every count the future screen needs, present rather than inferred.
	for _, k := range []string{"occurrence_count", "paid_count", "pending_count",
		"estimated_count", "overdue_count"} {
		if _, ok := body[k]; !ok {
			t.Fatalf("the response has no %s", k)
		}
	}
	items, ok := body["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("items is %v", body["items"])
	}
	item := items[0].(map[string]any)
	if item["recurring_entry_id"] != entryID.String() {
		t.Fatalf("the item names %v", item["recurring_entry_id"])
	}
	if item["status"] != "pending" || item["occurrence_id"] == nil {
		t.Fatalf("the item reads %v", item)
	}
}

func TestHTTPCommitmentHistoricalAndFuturePeriods(t *testing.T) {
	r, pool, wsA, _, _ := commitmentStack(t)
	current := serverMonth(t, pool)

	past := do(t, r, "GET", "/recurring-entries/commitment?period="+current.Prev().String(), wsA.String(), nil)
	if past.Code != http.StatusOK {
		t.Fatalf("past: status %d body %s", past.Code, past.Body.String())
	}
	pastBody := decodeCommitment(t, past.Body.Bytes())
	if pastBody["period"] != current.Prev().String() {
		t.Fatalf("the past month came back as %v", pastBody["period"])
	}
	if pastBody["is_projection"] != false {
		t.Fatal("a past month claims to be a projection")
	}
	// Reconstructed after the fact, so it says so.
	if numField(t, pastBody, "estimated_count") != 1 {
		t.Fatalf("a reconstructed month reports %d estimates", numField(t, pastBody, "estimated_count"))
	}

	future := do(t, r, "GET", "/recurring-entries/commitment?period="+current.Next().String(), wsA.String(), nil)
	if future.Code != http.StatusOK {
		t.Fatalf("future: status %d body %s", future.Code, future.Body.String())
	}
	futureBody := decodeCommitment(t, future.Body.Bytes())
	if futureBody["is_projection"] != true {
		t.Fatal("a month that has not begun did not declare itself a projection")
	}
	// A projection has no rows, so its items carry no id to address.
	items := futureBody["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("the projection has %d items", len(items))
	}
	if _, present := items[0].(map[string]any)["occurrence_id"]; present {
		t.Fatal("a projected item carries an occurrence id that resolves to nothing")
	}

	var rows int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM finance.recurring_occurrences WHERE period = $1`,
		current.Next().FirstDay()).Scan(&rows); err != nil {
		t.Fatalf("count: %v", err)
	}
	if rows != 0 {
		t.Fatalf("reading a future month over HTTP wrote %d rows", rows)
	}
}

func TestHTTPCommitmentRejectsAMalformedPeriod(t *testing.T) {
	r, _, wsA, _, _ := commitmentStack(t)
	for _, bad := range []string{"2026-9", "2026-13", "2026-00", "2026-09-01", "setembro", "202-06"} {
		rec := do(t, r, "GET", "/recurring-entries/commitment?period="+bad, wsA.String(), nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("period=%q returned %d, want 400: %s", bad, rec.Code, rec.Body.String())
		}
	}
}

func TestHTTPCommitmentWorkspaceIsolation(t *testing.T) {
	r, _, wsA, wsB, entryID := commitmentStack(t)

	a := decodeCommitment(t, do(t, r, "GET", "/recurring-entries/commitment", wsA.String(), nil).Body.Bytes())
	if numField(t, a, "committed_cents") != 250000 {
		t.Fatal("A cannot see its own month")
	}
	b := decodeCommitment(t, do(t, r, "GET", "/recurring-entries/commitment", wsB.String(), nil).Body.Bytes())
	if numField(t, b, "committed_cents") != 0 || numField(t, b, "occurrence_count") != 0 {
		t.Fatalf("B sees A's obligations: %v", b)
	}

	// And B cannot settle A's month by naming it.
	path := "/recurring-entries/" + entryID.String() + "/occurrences/" + a["period"].(string) + "/pay"
	if rec := do(t, r, "POST", path, wsB.String(), nil); rec.Code == http.StatusOK {
		t.Fatalf("workspace B settled workspace A's month: %s", rec.Body.String())
	}
	after := decodeCommitment(t, do(t, r, "GET", "/recurring-entries/commitment", wsA.String(), nil).Body.Bytes())
	if numField(t, after, "paid_cents") != 0 {
		t.Fatal("A's month was settled by B")
	}
}

/* ── pay / unpay ─────────────────────────────────────────────────────── */

func TestHTTPPayAndUnpayAreIdempotent(t *testing.T) {
	r, pool, wsA, _, entryID := commitmentStack(t)
	current := serverMonth(t, pool).String()
	// The month has to be read before it can be settled: materialisation
	// belongs to the read, and there is no route that creates a row.
	do(t, r, "GET", "/recurring-entries/commitment", wsA.String(), nil)

	path := "/recurring-entries/" + entryID.String() + "/occurrences/" + current + "/pay"

	rec := do(t, r, "POST", path, wsA.String(), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("pay: status %d body %s", rec.Code, rec.Body.String())
	}
	paid := decodeCommitment(t, rec.Body.Bytes())
	if paid["status"] != "paid" || paid["paid_at"] == nil {
		t.Fatalf("pay returned %v", paid)
	}
	stamp := paid["paid_at"]

	// Paying again succeeds and changes nothing, including the date. A
	// retried request must never undo the first.
	for i := 0; i < 3; i++ {
		again := do(t, r, "POST", path, wsA.String(), nil)
		if again.Code != http.StatusOK {
			t.Fatalf("repeat pay %d: status %d body %s", i+1, again.Code, again.Body.String())
		}
		body := decodeCommitment(t, again.Body.Bytes())
		if body["status"] != "paid" {
			t.Fatalf("repeat pay %d toggled it to %v", i+1, body["status"])
		}
		if body["paid_at"] != stamp {
			t.Fatalf("repeat pay %d moved the payment date", i+1)
		}
	}
	month := decodeCommitment(t, do(t, r, "GET", "/recurring-entries/commitment", wsA.String(), nil).Body.Bytes())
	if numField(t, month, "paid_count") != 1 || numField(t, month, "remaining_cents") != 0 {
		t.Fatalf("after four pays the month says %v", month)
	}

	// Unpay, repeatedly, with the same guarantee.
	if rec := do(t, r, "DELETE", path, wsA.String(), nil); rec.Code != http.StatusOK {
		t.Fatalf("unpay: status %d body %s", rec.Code, rec.Body.String())
	}
	for i := 0; i < 3; i++ {
		again := do(t, r, "DELETE", path, wsA.String(), nil)
		if again.Code != http.StatusOK {
			t.Fatalf("repeat unpay %d: status %d body %s", i+1, again.Code, again.Body.String())
		}
		if body := decodeCommitment(t, again.Body.Bytes()); body["status"] != "pending" {
			t.Fatalf("repeat unpay %d toggled it to %v", i+1, body["status"])
		}
	}
	final := decodeCommitment(t, do(t, r, "GET", "/recurring-entries/commitment", wsA.String(), nil).Body.Bytes())
	if numField(t, final, "paid_count") != 0 || numField(t, final, "remaining_cents") != 250000 {
		t.Fatalf("after four unpays the month says %v", final)
	}
}

func TestHTTPPatchOccurrenceAmount(t *testing.T) {
	r, pool, wsA, _, entryID := commitmentStack(t)
	current := serverMonth(t, pool).String()
	do(t, r, "GET", "/recurring-entries/commitment", wsA.String(), nil)

	base := "/recurring-entries/" + entryID.String() + "/occurrences/" + current
	rec := do(t, r, "PATCH", base, wsA.String(), map[string]any{"amount_cents": 43720})
	if rec.Code != http.StatusOK {
		t.Fatalf("patch: status %d body %s", rec.Code, rec.Body.String())
	}
	body := decodeCommitment(t, rec.Body.Bytes())
	if numField(t, body, "amount_cents") != 43720 || body["amount_estimated"] != false {
		t.Fatalf("patch returned %v", body)
	}
	// The month agrees, and the DEFINITION does not move.
	month := decodeCommitment(t, do(t, r, "GET", "/recurring-entries/commitment", wsA.String(), nil).Body.Bytes())
	if numField(t, month, "committed_cents") != 43720 {
		t.Fatalf("the month commits %d", numField(t, month, "committed_cents"))
	}
	var defaultCents int64
	if err := pool.QueryRow(context.Background(),
		`SELECT amount_cents FROM finance.recurring_entries WHERE id = $1`, entryID).Scan(&defaultCents); err != nil {
		t.Fatalf("reload definition: %v", err)
	}
	if defaultCents != 250000 {
		t.Fatalf("patching one month changed the recurrence's default to %d", defaultCents)
	}

	// A body with no amount is a bad request, not a silent no-op.
	if rec := do(t, r, "PATCH", base, wsA.String(), map[string]any{}); rec.Code != http.StatusBadRequest {
		t.Fatalf("an empty patch returned %d", rec.Code)
	}
	// A settled month is refused rather than rewritten.
	do(t, r, "POST", base+"/pay", wsA.String(), nil)
	if rec := do(t, r, "PATCH", base, wsA.String(), map[string]any{"amount_cents": 1}); rec.Code != http.StatusConflict {
		t.Fatalf("patching a paid month returned %d, want 409: %s", rec.Code, rec.Body.String())
	}
}

func TestHTTPFutureMonthCannotBeMutated(t *testing.T) {
	r, pool, wsA, _, entryID := commitmentStack(t)
	future := serverMonth(t, pool).Next().String()
	do(t, r, "GET", "/recurring-entries/commitment?period="+future, wsA.String(), nil)

	base := "/recurring-entries/" + entryID.String() + "/occurrences/" + future
	for _, c := range []struct {
		method string
		path   string
		body   any
	}{
		{"POST", base + "/pay", nil},
		{"DELETE", base + "/pay", nil},
		{"PATCH", base, map[string]any{"amount_cents": 1000}},
	} {
		rec := do(t, r, c.method, c.path, wsA.String(), c.body)
		if rec.Code == http.StatusOK {
			t.Fatalf("%s %s succeeded against a month that has not begun", c.method, c.path)
		}
	}
	var rows int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM finance.recurring_occurrences`).Scan(&rows); err != nil {
		t.Fatalf("count: %v", err)
	}
	if rows != 0 {
		t.Fatalf("a refused mutation against a projection wrote %d rows", rows)
	}
}

// TestHTTPHasNoOccurrenceCreateOrDelete is the negative-surface claim.
//
// Materialisation belongs to the application service and a month that
// happened does not stop having happened, so there must be no route
// through which either could be done directly. Asserted rather than
// assumed, because a route added later would be added by someone who never
// read the argument.
func TestHTTPHasNoOccurrenceCreateOrDelete(t *testing.T) {
	r, pool, wsA, _, entryID := commitmentStack(t)
	current := serverMonth(t, pool).String()
	do(t, r, "GET", "/recurring-entries/commitment", wsA.String(), nil)

	base := "/recurring-entries/" + entryID.String() + "/occurrences"
	cases := []struct{ method, path string }{
		// No create, under any shape a caller might try.
		{"POST", base},
		{"POST", base + "/" + current},
		{"PUT", base + "/" + current},
		// No delete of the occurrence itself. DELETE exists on /pay only,
		// where it means "unsettle", not "remove the month".
		{"DELETE", base + "/" + current},
		{"DELETE", base},
	}
	for _, c := range cases {
		rec := do(t, r, c.method, c.path, wsA.String(), map[string]any{"amount_cents": 1000})
		if rec.Code != http.StatusMethodNotAllowed && rec.Code != http.StatusNotFound {
			t.Fatalf("%s %s returned %d; that route should not exist", c.method, c.path, rec.Code)
		}
	}

	// And the month is still exactly where it was.
	var rows int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM finance.recurring_occurrences`).Scan(&rows); err != nil {
		t.Fatalf("count: %v", err)
	}
	if rows != 1 {
		t.Fatalf("the table holds %d rows after the probes", rows)
	}
}

// TestHTTPApplyToPeriodIsReachableOverTheWire is the regression for a
// field the service supported and the wire did not.
//
// ── The defect, and why every other test missed it ─────────────────────
// `apply_to_period` was designed, implemented and covered at the
// application layer, and the frontend sent it. The HTTP DTO never declared
// it. `render.DecodeJSON` calls `DisallowUnknownFields`, so the request did
// not merely ignore the instruction — it failed the WHOLE edit with a 400,
// and the local beta was the first thing to notice.
//
// The lesson is the shape of the gap rather than the typo: a field tested
// only through `app.Service` is a field with no proof that anything outside
// this process can reach it. So this test drives the wire.
func TestHTTPApplyToPeriodIsReachableOverTheWire(t *testing.T) {
	r, pool, wsA, _, entryID := commitmentStack(t)
	current := serverMonth(t, pool).String()
	do(t, r, "GET", "/recurring-entries/commitment", wsA.String(), nil) // materialise

	// Without it: the definition changes, the month does not.
	rec := do(t, r, "PATCH", "/recurring-entries/"+entryID.String(), wsA.String(),
		map[string]any{"amount_cents": 260000})
	if rec.Code != http.StatusOK {
		t.Fatalf("plain edit: status %d body %s", rec.Code, rec.Body.String())
	}
	month := decodeCommitment(t, do(t, r, "GET", "/recurring-entries/commitment", wsA.String(), nil).Body.Bytes())
	if got := numField(t, month, "committed_cents"); got != 250000 {
		t.Fatalf("a plain definition edit moved the month to %d", got)
	}

	// With it: the current, still-pending month takes the new figure.
	rec = do(t, r, "PATCH", "/recurring-entries/"+entryID.String(), wsA.String(),
		map[string]any{"amount_cents": 260000, "apply_to_period": current})
	if rec.Code != http.StatusOK {
		t.Fatalf("apply_to_period was refused over the wire: status %d body %s",
			rec.Code, rec.Body.String())
	}
	month = decodeCommitment(t, do(t, r, "GET", "/recurring-entries/commitment", wsA.String(), nil).Body.Bytes())
	if got := numField(t, month, "committed_cents"); got != 260000 {
		t.Fatalf("apply_to_period left the month at %d", got)
	}

	// A malformed month is a 400 that names the problem, not a silently
	// ignored instruction and not the current month by accident.
	if rec := do(t, r, "PATCH", "/recurring-entries/"+entryID.String(), wsA.String(),
		map[string]any{"amount_cents": 270000, "apply_to_period": "2026-9"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("a malformed apply_to_period returned %d, want 400", rec.Code)
	}

	// And the refusals still hold over the wire.
	past := serverMonth(t, pool).Prev().String()
	if rec := do(t, r, "PATCH", "/recurring-entries/"+entryID.String(), wsA.String(),
		map[string]any{"amount_cents": 270000, "apply_to_period": past}); rec.Code == http.StatusOK {
		t.Fatal("apply_to_period rewrote a past month over the wire")
	}
}

// The other two new definition fields have to be reachable too, for the
// same reason and by the same strict decoder: a field the service accepts
// and the DTO omits is a field nothing outside this process can set.
func TestHTTPDefinitionAcceptsDueMonthAndAmountVaries(t *testing.T) {
	r, _, wsA, _, entryID := commitmentStack(t)
	path := "/recurring-entries/" + entryID.String()

	if rec := do(t, r, "PATCH", path, wsA.String(),
		map[string]any{"amount_varies": true}); rec.Code != http.StatusOK {
		t.Fatalf("amount_varies over the wire: status %d body %s", rec.Code, rec.Body.String())
	}
	// Becoming annual requires naming the month it falls due in.
	if rec := do(t, r, "PATCH", path, wsA.String(),
		map[string]any{"recurrence": "annual", "due_month": 1}); rec.Code != http.StatusOK {
		t.Fatalf("due_month over the wire: status %d body %s", rec.Code, rec.Body.String())
	}
	// And going back to monthly drops it, because a monthly entry that also
	// names a month is a row the domain refuses.
	if rec := do(t, r, "PATCH", path, wsA.String(),
		map[string]any{"recurrence": "monthly", "clear_due_month": true}); rec.Code != http.StatusOK {
		t.Fatalf("clear_due_month over the wire: status %d body %s", rec.Code, rec.Body.String())
	}
}

// The two readings live side by side under the same prefix and neither
// shadows the other. `/summary` and `/commitment` are literal segments and
// `/{id}` is a uuid, so chi has to keep all three apart.
func TestHTTPCommitmentDoesNotShadowTheOtherRoutes(t *testing.T) {
	r, _, wsA, _, entryID := commitmentStack(t)

	if rec := do(t, r, "GET", "/recurring-entries/summary", wsA.String(), nil); rec.Code != http.StatusOK {
		t.Fatalf("/summary returned %d: %s", rec.Code, rec.Body.String())
	}
	if rec := do(t, r, "GET", "/recurring-entries/commitment", wsA.String(), nil); rec.Code != http.StatusOK {
		t.Fatalf("/commitment returned %d: %s", rec.Code, rec.Body.String())
	}
	if rec := do(t, r, "GET", "/recurring-entries/"+entryID.String(), wsA.String(), nil); rec.Code != http.StatusOK {
		t.Fatalf("/{id} returned %d: %s", rec.Code, rec.Body.String())
	}
	if rec := do(t, r, "GET", "/recurring-entries", wsA.String(), nil); rec.Code != http.StatusOK {
		t.Fatalf("the listing returned %d: %s", rec.Code, rec.Body.String())
	}
}
