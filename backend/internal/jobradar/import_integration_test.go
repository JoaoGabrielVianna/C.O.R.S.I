//go:build integration

// The import boundary — identity, history and the clock.
//
//	Run with: TEST_POSTGRES_DSN=... go test -tags=integration ./internal/jobradar/...
//
// Every assertion here goes through the real HTTP route, because the thing
// being proved is a property of the boundary a browser will actually call.
// Reaching into the repository would test a different program.
//
// The fixtures are written here rather than taken from the browser document
// found on this machine: that record is ambiguous development data, and a
// test that depended on it would be a test that could not run anywhere else.
package jobradar

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

/* ── fixtures ────────────────────────────────────────────────────────── */

// The three moments fixture A moved through. Fixed, historical, and far
// enough back that "now" could never be mistaken for one of them.
var (
	tSaved     = time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)
	tApplied   = time.Date(2026, 3, 9, 14, 30, 0, 0, time.UTC)
	tInterview = time.Date(2026, 4, 1, 11, 15, 0, 0, time.UTC)
	tCreated   = time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
	tUpdated   = time.Date(2026, 4, 1, 11, 15, 0, 0, time.UTC)
)

func ms(t time.Time) int64 { return t.UnixMilli() }

// fixtureA is the full case: a real timeline, a historical clock, and a
// company with a website.
func fixtureA() map[string]any {
	return map[string]any{
		"legacy_id":      "op_fixture_a",
		"company":        "Company A",
		"company_domain": "company-a.example",
		"role":           "Backend Engineer",
		"salary":         "R$ 20k",
		"location":       "Remote (BR)",
		"stack":          []string{"Go", "Postgres"},
		"source":         "manual",
		"posted_at":      ms(tCreated),
		"stage":          "interview",
		"tracked_at":     ms(tSaved),
		// stage_entered_at agrees with the last visit; they describe the
		// same event and a record where they disagree is a record that
		// contradicts itself.
		"stage_entered_at": ms(tInterview),
		"created_at":       ms(tCreated),
		"updated_at":       ms(tUpdated),
		"history": []map[string]any{
			{"stage": "saved", "at": ms(tSaved)},
			{"stage": "applied", "at": ms(tApplied)},
			{"stage": "interview", "at": ms(tInterview)},
		},
	}
}

// fixtureB is A's employer and A's job title with a DIFFERENT legacy id:
// two genuinely distinct postings, which must both survive.
func fixtureB() map[string]any {
	return map[string]any{
		"legacy_id": "op_fixture_b",
		"company":   "Company A",
		"role":      "Backend Engineer",
		"location":  "São Paulo",
		"stage":     "saved",
		// A record with no history at all is legitimate: it never moved.
		"tracked_at":       ms(tSaved),
		"stage_entered_at": ms(tSaved),
	}
}

// fixtureD carries a stage this domain does not have. The legacy document
// on this machine really does contain `recruiter`.
func fixtureD() map[string]any {
	return map[string]any{
		"legacy_id":        "op_fixture_d",
		"company":          "Company D",
		"role":             "Platform Engineer",
		"stage":            "applied",
		"tracked_at":       ms(tSaved),
		"stage_entered_at": ms(tApplied),
		"history": []map[string]any{
			{"stage": "saved", "at": ms(tSaved)},
			{"stage": "recruiter", "at": ms(tApplied)},
		},
	}
}

/* ── helpers ─────────────────────────────────────────────────────────── */

type importResponse struct {
	CreatedCount         int `json:"created_count"`
	AlreadyImportedCount int `json:"already_imported_count"`
	FailedCount          int `json:"failed_count"`
	AlreadyImported      []struct {
		Index    int    `json:"index"`
		LegacyID string `json:"legacy_id"`
		ID       string `json:"id"`
	} `json:"already_imported"`
	Failed []struct {
		Index   int    `json:"index"`
		Company string `json:"company"`
		Role    string `json:"role"`
		Error   string `json:"error"`
	} `json:"failed"`
	Created []struct {
		ID string `json:"id"`
	} `json:"created"`
}

func (e *env) importItems(t *testing.T, ws uuid.UUID, items ...map[string]any) importResponse {
	t.Helper()
	rec := e.do(http.MethodPost, "/job-radar/opportunities/import", ws,
		map[string]any{"items": items})
	if rec.Code != http.StatusOK {
		t.Fatalf("import: status %d body %s", rec.Code, rec.Body.String())
	}
	var out importResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode import result: %v", err)
	}
	return out
}

// liveCount reads through the API, not the database: the question is what
// the product shows, and a count taken from SQL could pass while the read
// path disagreed.
func (e *env) liveCount(t *testing.T, ws uuid.UUID) int {
	t.Helper()
	rec := e.do(http.MethodGet, "/job-radar/opportunities", ws, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: status %d", rec.Code)
	}
	var out struct {
		Total int `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	return out.Total
}

/* ── 1. identity and retry ───────────────────────────────────────────── */

// The requirement in one test: import, import the same thing again, and end
// with what you started with.
func TestAReimportOfTheSameDocumentCreatesNothing(t *testing.T) {
	e := newEnv(t)

	first := e.importItems(t, e.wsA, fixtureA(), fixtureB())
	if first.CreatedCount != 2 || first.FailedCount != 0 {
		t.Fatalf("first import: created=%d failed=%d %+v",
			first.CreatedCount, first.FailedCount, first.Failed)
	}
	if n := e.liveCount(t, e.wsA); n != 2 {
		t.Fatalf("after import #1 the database holds %d, want 2", n)
	}

	second := e.importItems(t, e.wsA, fixtureA(), fixtureB())
	if second.CreatedCount != 0 {
		t.Errorf("a retry created %d rows", second.CreatedCount)
	}
	if second.AlreadyImportedCount != 2 {
		t.Errorf("already_imported = %d, want 2 — a retry that reports nothing "+
			"reads as a failure rather than as the no-op it is",
			second.AlreadyImportedCount)
	}
	if n := e.liveCount(t, e.wsA); n != 2 {
		t.Fatalf("after import #2 the database holds %d, want 2", n)
	}

	// The recognition names the row it points at, so a caller can say which
	// opportunity a legacy record became.
	for _, a := range second.AlreadyImported {
		if a.ID == "" || a.LegacyID == "" {
			t.Errorf("already-imported entry is missing identity: %+v", a)
		}
	}
}

// The property the identity scheme exists to protect: company+role is NOT
// the key, so two real postings for the same job survive as two rows.
func TestTwoPostingsWithTheSameCompanyAndRoleBothSurvive(t *testing.T) {
	e := newEnv(t)

	res := e.importItems(t, e.wsA, fixtureA(), fixtureB())
	if res.CreatedCount != 2 {
		t.Fatalf("created = %d, want 2 — a dedupe on company+role would have "+
			"collapsed two legitimate postings", res.CreatedCount)
	}
	if res.Created[0].ID == res.Created[1].ID {
		t.Fatal("both postings resolved to one row")
	}
	if n := e.liveCount(t, e.wsA); n != 2 {
		t.Fatalf("live count = %d, want 2", n)
	}
}

// Partial failure plus retry, which is the shape a real migration takes:
// fix the bad record, send the document again, and the good ones do not
// double.
func TestARetryAfterAPartialFailureDoesNotDuplicateTheGoodItems(t *testing.T) {
	e := newEnv(t)

	first := e.importItems(t, e.wsA, fixtureA(), fixtureD(), fixtureB())
	if first.CreatedCount != 2 || first.FailedCount != 1 {
		t.Fatalf("created=%d failed=%d, want 2 and 1: a bad record must not "+
			"block the good ones", first.CreatedCount, first.FailedCount)
	}
	if n := e.liveCount(t, e.wsA); n != 2 {
		t.Fatalf("live count = %d, want 2", n)
	}

	// The same document again, with D still broken.
	second := e.importItems(t, e.wsA, fixtureA(), fixtureD(), fixtureB())
	if second.CreatedCount != 0 || second.AlreadyImportedCount != 2 || second.FailedCount != 1 {
		t.Fatalf("retry: created=%d already=%d failed=%d",
			second.CreatedCount, second.AlreadyImportedCount, second.FailedCount)
	}
	if n := e.liveCount(t, e.wsA); n != 2 {
		t.Fatalf("a retry changed the row count to %d", n)
	}
}

// A record with no legacy id could never be recognised on a retry, so it is
// refused rather than imported anonymously.
func TestARecordWithNoLegacyIDIsRefused(t *testing.T) {
	e := newEnv(t)
	anonymous := fixtureB()
	delete(anonymous, "legacy_id")

	res := e.importItems(t, e.wsA, anonymous)
	if res.CreatedCount != 0 || res.FailedCount != 1 {
		t.Fatalf("created=%d failed=%d, want 0 and 1", res.CreatedCount, res.FailedCount)
	}
	if !strings.Contains(res.Failed[0].Error, "legacy id") {
		t.Errorf("the refusal does not explain itself: %q", res.Failed[0].Error)
	}
}

// Identity is per workspace. The same legacy document imported into two
// workspaces is two separate pipelines, not one shared row.
func TestImportIdentityIsScopedToTheWorkspace(t *testing.T) {
	e := newEnv(t)

	if res := e.importItems(t, e.wsA, fixtureA()); res.CreatedCount != 1 {
		t.Fatalf("workspace A: created = %d", res.CreatedCount)
	}
	res := e.importItems(t, e.wsB, fixtureA())
	if res.CreatedCount != 1 || res.AlreadyImportedCount != 0 {
		t.Fatalf("workspace B: created=%d already=%d — identity leaked across "+
			"workspaces", res.CreatedCount, res.AlreadyImportedCount)
	}
	if n := e.liveCount(t, e.wsA); n != 1 {
		t.Errorf("workspace A now holds %d", n)
	}
}

/* ── 2. history ──────────────────────────────────────────────────────── */

// The whole timeline, not one synthetic event for wherever the record
// happens to be now.
func TestTheFullStageHistoryIsPreserved(t *testing.T) {
	e := newEnv(t)
	res := e.importItems(t, e.wsA, fixtureA())
	if res.CreatedCount != 1 {
		t.Fatalf("created = %d: %+v", res.CreatedCount, res.Failed)
	}

	rec := e.do(http.MethodGet, "/job-radar/opportunities/"+res.Created[0].ID, e.wsA, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get: status %d", rec.Code)
	}
	var got struct {
		History []struct {
			FromStage  *string   `json:"from_stage"`
			ToStage    string    `json:"to_stage"`
			OccurredAt time.Time `json:"occurred_at"`
		} `json:"history"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(got.History) != 3 {
		t.Fatalf("history has %d events, want 3 — a single synthetic event is "+
			"the loss this batch exists to close: %+v", len(got.History), got.History)
	}
	want := []struct {
		from *string
		to   string
		at   time.Time
	}{
		{nil, "saved", tSaved},
		{strptr("saved"), "applied", tApplied},
		{strptr("applied"), "interview", tInterview},
	}
	for i, w := range want {
		g := got.History[i]
		if (g.FromStage == nil) != (w.from == nil) ||
			(g.FromStage != nil && *g.FromStage != *w.from) {
			t.Errorf("event %d from_stage = %v, want %v", i, deref(g.FromStage), deref(w.from))
		}
		if g.ToStage != w.to {
			t.Errorf("event %d to_stage = %q, want %q", i, g.ToStage, w.to)
		}
		if !g.OccurredAt.UTC().Equal(w.at) {
			t.Errorf("event %d occurred_at = %s, want %s", i, g.OccurredAt.UTC(), w.at)
		}
	}
	// The first event has no predecessor: entering the pipeline comes from
	// Discover, and naming `saved` as its own `from` would invent a visit.
	if got.History[0].FromStage != nil {
		t.Error("the first entry into the pipeline was given a predecessor it never had")
	}
}

// A record that never moved gets the one event that describes it, which is
// what the importer always did and remains correct.
func TestARecordWithNoHistoryStillGetsItsEntryEvent(t *testing.T) {
	e := newEnv(t)
	res := e.importItems(t, e.wsA, fixtureB())
	rec := e.do(http.MethodGet, "/job-radar/opportunities/"+res.Created[0].ID, e.wsA, nil)

	var got struct {
		History []struct {
			FromStage *string `json:"from_stage"`
			ToStage   string  `json:"to_stage"`
		} `json:"history"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if len(got.History) != 1 || got.History[0].ToStage != "saved" || got.History[0].FromStage != nil {
		t.Fatalf("history = %+v, want one entry event into saved", got.History)
	}
}

/* ── 3. unknown stages ───────────────────────────────────────────────── */

// The rule with the sharpest edge: a stage this system does not have is not
// mapped to a plausible one. It fails, by name, and nothing is written.
func TestAnUnknownHistoricalStageFailsTheItemInsteadOfBeingMapped(t *testing.T) {
	e := newEnv(t)

	res := e.importItems(t, e.wsA, fixtureD())
	if res.CreatedCount != 0 || res.FailedCount != 1 {
		t.Fatalf("created=%d failed=%d, want 0 and 1", res.CreatedCount, res.FailedCount)
	}
	msg := res.Failed[0].Error
	if !strings.Contains(msg, "recruiter") {
		t.Errorf("the failure does not name the offending stage: %q", msg)
	}
	// It must not have been quietly turned into the nearest thing.
	if strings.Contains(msg, "mapped") || strings.Contains(msg, "converted") {
		t.Errorf("the failure suggests a silent mapping happened: %q", msg)
	}
	if n := e.liveCount(t, e.wsA); n != 0 {
		t.Fatalf("a refused item left %d rows behind", n)
	}
}

// A timeline that ends somewhere other than the record's current stage
// describes two different pasts. Neither is invented over the other.
func TestAHistoryThatDisagreesWithTheCurrentStageIsRefused(t *testing.T) {
	e := newEnv(t)
	item := fixtureA()
	item["stage"] = "offer" // the history still ends at interview

	res := e.importItems(t, e.wsA, item)
	if res.CreatedCount != 0 || res.FailedCount != 1 {
		t.Fatalf("created=%d failed=%d", res.CreatedCount, res.FailedCount)
	}
	if !strings.Contains(res.Failed[0].Error, "will not guess") {
		t.Errorf("the refusal does not say why it refuses: %q", res.Failed[0].Error)
	}
}

/* ── 4. the clock ────────────────────────────────────────────────────── */

// The migration is a statement about a past. Letting the server stamp
// `now()` would reset every "12 days in Applied" to zero on migration day.
func TestHistoricalTimestampsSurviveTheImport(t *testing.T) {
	e := newEnv(t)
	res := e.importItems(t, e.wsA, fixtureA())
	id := res.Created[0].ID

	// Through the API.
	rec := e.do(http.MethodGet, "/job-radar/opportunities/"+id, e.wsA, nil)
	var got struct {
		Opportunity struct {
			CreatedAt time.Time `json:"created_at"`
			UpdatedAt time.Time `json:"updated_at"`
		} `json:"opportunity"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.Opportunity.CreatedAt.UTC().Equal(tCreated) {
		t.Errorf("created_at = %s, want %s", got.Opportunity.CreatedAt.UTC(), tCreated)
	}
	if !got.Opportunity.UpdatedAt.UTC().Equal(tUpdated) {
		t.Errorf("updated_at = %s, want %s", got.Opportunity.UpdatedAt.UTC(), tUpdated)
	}

	// And independently, in the column itself.
	var dbCreated, dbUpdated time.Time
	if err := e.pool.QueryRow(ctxFor(e.wsA),
		`SELECT created_at, updated_at FROM jobradar.opportunities WHERE id = $1`,
		id).Scan(&dbCreated, &dbUpdated); err != nil {
		t.Fatalf("read columns: %v", err)
	}
	if !dbCreated.UTC().Equal(tCreated) || !dbUpdated.UTC().Equal(tUpdated) {
		t.Errorf("stored clock = (%s, %s), want (%s, %s)",
			dbCreated.UTC(), dbUpdated.UTC(), tCreated, tUpdated)
	}
}

// An ordinary create must NOT be able to backdate itself. The power lives
// on the import path and nowhere else.
func TestAnOrdinaryCreateCannotBackdateItself(t *testing.T) {
	e := newEnv(t)
	before := time.Now().UTC().Add(-time.Minute)

	rec := e.do(http.MethodPost, "/job-radar/opportunities", e.wsA, map[string]any{
		"company":    "Company Z",
		"role":       "Nobody",
		"created_at": ms(tCreated),
		"updated_at": ms(tCreated),
	})
	// The decoder rejects unknown fields outright, which is the strongest
	// possible answer: the capability is not merely ignored, it does not
	// exist on this route.
	if rec.Code == http.StatusOK || rec.Code == http.StatusCreated {
		var got struct {
			Opportunity struct {
				CreatedAt time.Time `json:"created_at"`
			} `json:"opportunity"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &got)
		if got.Opportunity.CreatedAt.Before(before) {
			t.Fatalf("an ordinary create backdated itself to %s", got.Opportunity.CreatedAt)
		}
	}
}

/* ── 5. the company ──────────────────────────────────────────────────── */

func TestTheCompanyDomainIsCarriedAndNotDuplicated(t *testing.T) {
	e := newEnv(t)

	// A and B are the same employer; only A knows the website.
	if res := e.importItems(t, e.wsA, fixtureA(), fixtureB()); res.CreatedCount != 2 {
		t.Fatalf("created = %d", res.CreatedCount)
	}

	rec := e.do(http.MethodGet, "/job-radar/companies", e.wsA, nil)
	var got struct {
		Items []struct {
			Name   string `json:"name"`
			Domain string `json:"domain"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode companies: %v", err)
	}

	matches := 0
	for _, c := range got.Items {
		if c.Name != "Company A" {
			continue
		}
		matches++
		if c.Domain != "company-a.example" {
			t.Errorf("domain = %q, want the one the document carried", c.Domain)
		}
	}
	if matches != 1 {
		t.Fatalf("Company A appears %d times, want exactly one row", matches)
	}
}

// A blank domain must not erase one that is already there. The legacy
// document is not a more authoritative source than the current record.
func TestABlankDomainDoesNotEraseAnExistingOne(t *testing.T) {
	e := newEnv(t)

	if res := e.importItems(t, e.wsA, fixtureA()); res.CreatedCount != 1 {
		t.Fatalf("setup: created = %d", res.CreatedCount)
	}
	// B is the same employer with no domain at all.
	if res := e.importItems(t, e.wsA, fixtureB()); res.CreatedCount != 1 {
		t.Fatalf("second import: created = %d", res.CreatedCount)
	}

	var domain string
	if err := e.pool.QueryRow(ctxFor(e.wsA),
		`SELECT domain FROM jobradar.companies WHERE workspace_id = $1 AND name = 'Company A'`,
		e.wsA).Scan(&domain); err != nil {
		t.Fatalf("read company: %v", err)
	}
	if domain != "company-a.example" {
		t.Fatalf("domain = %q; a blank overwrote a real value", domain)
	}
}

func strptr(s string) *string { return &s }

func deref(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}
