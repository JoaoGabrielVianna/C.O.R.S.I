//go:build integration

// Statement import, end to end through the tool boundary.
//
// Every test here goes through the same door a model would: the registry,
// the workspace context, the tool's Execute. Nothing reaches into the
// application service to set up a state the tools could not have produced.

package tools

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/finance/app"
	"github.com/corsi/backend/internal/finance/domain"
)

/* ── helpers ─────────────────────────────────────────────────────────── */

// findOrSeedCategory is idempotent: category names are unique per
// workspace among live rows, so a second call must not try to create.
func (e *env) findOrSeedCategory(ws uuid.UUID, name string, ty domain.EntryType) *domain.Category {
	e.t.Helper()
	cats, err := e.svc.ListCategories(context.Background(), app.ListCategoriesInput{WorkspaceID: ws, Limit: 200})
	if err != nil {
		e.t.Fatalf("list categories: %v", err)
	}
	for i := range cats {
		if strings.EqualFold(cats[i].Name, name) {
			return &cats[i]
		}
	}
	return e.seedCategory(ws, name, ty)
}

func (e *env) categoryCount(ws uuid.UUID) int {
	var n int
	_ = e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM finance.categories WHERE workspace_id=$1 AND deleted_at IS NULL`, ws).Scan(&n)
	return n
}

// importSource registers a source once per label and returns its id.
//
// The tests name sources by a short label the way an operator would; the
// identity the dedup namespace is built from is the uuid this returns, and
// no test can spell it.
func (e *env) importSource(t *testing.T, ws uuid.UUID, label string) string {
	t.Helper()
	listed := e.execute(t, ws, ImportSourceListTool, map[string]any{})
	if items, ok := listed["items"].([]any); ok {
		for _, it := range items {
			m := it.(map[string]any)
			if m["label"] == label {
				return m["id"].(string)
			}
		}
	}
	out := e.execute(t, ws, ImportSourceCreateTool, map[string]any{
		"kind": "card", "institution": "Fixture Bank", "label": label,
	})
	return impStr(t, out, "id")
}

func (e *env) prepare(t *testing.T, ws uuid.UUID, label, csv string) map[string]any {
	t.Helper()
	return e.execute(t, ws, ImportPrepareTool, map[string]any{
		"import_source_id": e.importSource(t, ws, label),
		"source_label":     "fixture",
		"content":          csv,
	})
}

func impNum(t *testing.T, out map[string]any, key string) int {
	t.Helper()
	v, ok := out[key]
	if !ok {
		t.Fatalf("key %q missing from %v", key, out)
	}
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	t.Fatalf("key %q is %T, not a number", key, v)
	return 0
}

func impStr(t *testing.T, out map[string]any, key string) string {
	t.Helper()
	s, _ := out[key].(string)
	if s == "" {
		t.Fatalf("key %q missing or empty in %v", key, out)
	}
	return s
}

// resolveAll assigns a category to every unresolved group, creating one
// category per group through the ordinary capability.
func (e *env) resolveAll(t *testing.T, ws uuid.UUID, out map[string]any) map[string]any {
	t.Helper()
	batchID := impStr(t, out, "batch_id")
	// The output is round-tripped through JSON, exactly as the model
	// receives it, so the slice arrives as []any rather than the Go type
	// the tool built.
	raw, _ := out["unresolved_groups"].([]any)
	for i, item := range raw {
		_ = i
		g, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("group %d is %T", i, item)
		}
		dir := domain.EntryType(g["direction"].(string))
		// Named after the group so a test that resolves twice reuses the
		// same category instead of colliding with its own fixture.
		name := fmt.Sprintf("Cat %s", g["group_key"])
		cat := e.findOrSeedCategory(ws, name, dir)
		out = e.execute(t, ws, ImportResolveGroupTool, map[string]any{
			"batch_id":    batchID,
			"group_key":   g["group_key"],
			"category_id": cat.ID.String(),
		})
	}
	return out
}

const basicCSV = `date,description,amount
2026-08-05,SALARIO EMPRESA,8000.00
2026-08-06,MERCADO CENTRAL,-150.00
2026-08-07,CAFE DA ESQUINA,-8.50
2026-08-07,CAFE DA ESQUINA,-8.50
2026-08-08,TRANSFERENCIA ENTRE CONTAS,-500.00
2026-08-09,LINHA QUEBRADA,nao-e-numero
`

/* ── prepare writes nothing to the ledger ────────────────────────────── */

func TestPrepareCreatesNoTransaction(t *testing.T) {
	e := newEnv(t)

	before := e.liveCount(e.wsA)
	beforeTotals := e.execute(t, e.wsA, SummaryGetTool, map[string]any{"period": "this_month"})

	out := e.prepare(t, e.wsA, "acct-1", basicCSV)

	if got := e.liveCount(e.wsA); got != before {
		t.Fatalf("prepare created %d transactions; it must create none", got-before)
	}
	if !out["nothing_was_written"].(bool) {
		t.Error("prepare must report that nothing was written")
	}
	afterTotals := e.execute(t, e.wsA, SummaryGetTool, map[string]any{"period": "this_month"})
	if fmt.Sprint(beforeTotals["realized"]) != fmt.Sprint(afterTotals["realized"]) {
		t.Errorf("summary moved across a prepare: %v then %v",
			beforeTotals["realized"], afterTotals["realized"])
	}
}

// The reconciliation must account for every line in the file. A line that
// is not in a bucket is a line the operator was never told about.
func TestReconciliationBalancesBeforeCommit(t *testing.T) {
	e := newEnv(t)
	out := e.prepare(t, e.wsA, "acct-1", basicCSV)

	total := impNum(t, out, "row_count")
	sum := impNum(t, out, "ready") + impNum(t, out, "already_imported") +
		impNum(t, out, "maybe_duplicate") + impNum(t, out, "needs_category") +
		impNum(t, out, "maybe_internal") + impNum(t, out, "invalid")
	if total != 6 {
		t.Fatalf("row_count = %d, want the 6 data lines", total)
	}
	if sum != total {
		t.Fatalf("states sum to %d but the file had %d lines: a line vanished", sum, total)
	}
	if !out["reconciles"].(bool) {
		t.Error("the batch must report that it reconciles")
	}
}

func TestInvalidLineNeverBecomesATransaction(t *testing.T) {
	e := newEnv(t)
	out := e.prepare(t, e.wsA, "acct-1", basicCSV)
	if impNum(t, out, "invalid") != 1 {
		t.Fatalf("invalid = %d, want 1", impNum(t, out, "invalid"))
	}
	out = e.resolveAll(t, e.wsA, out)
	res := e.execute(t, e.wsA, ImportCommitTool, map[string]any{"batch_id": impStr(t, out, "batch_id")})

	staged := res["still_staged"].(map[string]any)
	if fmt.Sprint(staged["invalid"]) != "1" {
		t.Errorf("the invalid line must remain staged, got %v", staged["invalid"])
	}
	rows := e.liveCount(e.wsA)
	if rows != impNum(t, res, "created") {
		t.Errorf("ledger has %d rows but commit created %d", rows, impNum(t, res, "created"))
	}
}

/* ── the ambiguity rules ─────────────────────────────────────────────── */

// Two identical purchases on the same day are two purchases. Collapsing
// them would delete a real transaction nobody would ever miss.
func TestTwoIdenticalPurchasesOnTheSameDayBothImport(t *testing.T) {
	e := newEnv(t)
	out := e.resolveAll(t, e.wsA, e.prepare(t, e.wsA, "acct-1", basicCSV))
	res := e.execute(t, e.wsA, ImportCommitTool, map[string]any{"batch_id": impStr(t, out, "batch_id")})
	if impNum(t, res, "created") < 4 {
		t.Fatalf("created = %d; both coffees must be in", impNum(t, res, "created"))
	}
	var coffees int
	rows, _ := e.pool.Query(context.Background(),
		`SELECT count(*) FROM finance.transactions
		  WHERE workspace_id=$1 AND description='CAFE DA ESQUINA' AND deleted_at IS NULL`, e.wsA)
	defer rows.Close()
	if rows.Next() {
		_ = rows.Scan(&coffees)
	}
	if coffees != 2 {
		t.Fatalf("the ledger holds %d coffees, want 2", coffees)
	}
}

// A possible internal movement is a question, never a classification.
func TestPossibleInternalMovementIsNotImported(t *testing.T) {
	e := newEnv(t)
	out := e.prepare(t, e.wsA, "acct-1", basicCSV)
	if impNum(t, out, "maybe_internal") != 1 {
		t.Fatalf("maybe_internal = %d, want the transfer line", impNum(t, out, "maybe_internal"))
	}
	out = e.resolveAll(t, e.wsA, out)
	e.execute(t, e.wsA, ImportCommitTool, map[string]any{"batch_id": impStr(t, out, "batch_id")})

	var n int
	row := e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM finance.transactions
		  WHERE workspace_id=$1 AND description LIKE 'TRANSFER%' AND deleted_at IS NULL`, e.wsA)
	_ = row.Scan(&n)
	if n != 0 {
		t.Fatalf("an ambiguous internal movement entered the ledger as %d ordinary rows", n)
	}
}

// A heuristic match is a QUESTION. It must not be silently skipped and it
// must not be silently imported.
func TestHeuristicRepeatBecomesAmbiguousDuplicateNotASilentSkip(t *testing.T) {
	e := newEnv(t)
	csv := "date,description,amount\n2026-08-10,MERCADO,-50.00\n"
	out := e.resolveAll(t, e.wsA, e.prepare(t, e.wsA, "acct-1", csv))
	e.execute(t, e.wsA, ImportCommitTool, map[string]any{"batch_id": impStr(t, out, "batch_id")})

	again := e.prepare(t, e.wsA, "acct-1", csv)
	if impNum(t, again, "maybe_duplicate") != 1 {
		t.Fatalf("maybe_duplicate = %d, want 1", impNum(t, again, "maybe_duplicate"))
	}
	if impNum(t, again, "already_imported") != 0 {
		t.Error("a fingerprint match must not be reported as a certain duplicate")
	}
	if _, ok := again["maybe_duplicate_note"]; !ok {
		t.Error("the ambiguity must be explained, not just counted")
	}
	// And committing it imports nothing: ambiguity is not eligible.
	res := e.execute(t, e.wsA, ImportCommitTool, map[string]any{"batch_id": impStr(t, again, "batch_id")})
	if impNum(t, res, "created") != 0 {
		t.Fatalf("created = %d from a batch whose only line was ambiguous", impNum(t, res, "created"))
	}
}

/* ── strong identity ─────────────────────────────────────────────────── */

func TestStrongIdentityMakesReimportCreateNothing(t *testing.T) {
	e := newEnv(t)
	csv := "date,description,amount,fitid\n" +
		"2026-08-10,MERCADO,-50.00,BANK-1\n" +
		"2026-08-11,PADARIA,-12.30,BANK-2\n"
	out := e.resolveAll(t, e.wsA, e.prepare(t, e.wsA, "acct-1", csv))
	first := e.execute(t, e.wsA, ImportCommitTool, map[string]any{"batch_id": impStr(t, out, "batch_id")})
	if impNum(t, first, "created") != 2 {
		t.Fatalf("first import created %d, want 2", impNum(t, first, "created"))
	}

	again := e.prepare(t, e.wsA, "acct-1", csv)
	if impNum(t, again, "already_imported") != 2 {
		t.Fatalf("already_imported = %d, want 2", impNum(t, again, "already_imported"))
	}
	res := e.execute(t, e.wsA, ImportCommitTool, map[string]any{"batch_id": impStr(t, again, "batch_id")})
	if impNum(t, res, "created") != 0 {
		t.Fatalf("re-import created %d transactions", impNum(t, res, "created"))
	}
	if got := e.liveCount(e.wsA); got != 2 {
		t.Fatalf("ledger holds %d rows after two imports of the same file", got)
	}
}

func TestPartiallyOverlappingFileImportsOnlyTheNewLines(t *testing.T) {
	e := newEnv(t)
	first := "date,description,amount,fitid\n2026-08-10,MERCADO,-50.00,B1\n"
	out := e.resolveAll(t, e.wsA, e.prepare(t, e.wsA, "acct-1", first))
	e.execute(t, e.wsA, ImportCommitTool, map[string]any{"batch_id": impStr(t, out, "batch_id")})

	wider := first + "2026-08-11,PADARIA,-12.30,B2\n"
	out2 := e.resolveAll(t, e.wsA, e.prepare(t, e.wsA, "acct-1", wider))
	res := e.execute(t, e.wsA, ImportCommitTool, map[string]any{"batch_id": impStr(t, out2, "batch_id")})
	if impNum(t, res, "created") != 1 {
		t.Fatalf("created = %d, want only the new line", impNum(t, res, "created"))
	}
	if got := e.liveCount(e.wsA); got != 2 {
		t.Fatalf("ledger holds %d, want 2", got)
	}
}

// A strong identity and a fingerprint are different namespaces. The same
// transaction arriving first without and then with a bank id must not be
// silently merged: nothing proves they are the same event.
func TestFingerprintAndStrongIdentityAreNotReconciled(t *testing.T) {
	e := newEnv(t)
	noID := "date,description,amount\n2026-08-10,MERCADO,-50.00\n"
	out := e.resolveAll(t, e.wsA, e.prepare(t, e.wsA, "acct-1", noID))
	e.execute(t, e.wsA, ImportCommitTool, map[string]any{"batch_id": impStr(t, out, "batch_id")})

	withID := "date,description,amount,fitid\n2026-08-10,MERCADO,-50.00,B1\n"
	again := e.prepare(t, e.wsA, "acct-1", withID)
	if impNum(t, again, "already_imported") != 0 {
		t.Error("a bank id must not be assumed equal to a fingerprint we invented")
	}
	if impNum(t, again, "maybe_duplicate") != 0 {
		t.Error("the strong row is compared in its own namespace only")
	}
	if impNum(t, again, "ready")+impNum(t, again, "needs_category") != 1 {
		t.Fatalf("the strong row should stand on its own: %v", again)
	}
}

func TestDescriptionChangeBreaksHeuristicDedupAndIsDocumented(t *testing.T) {
	e := newEnv(t)
	a := "date,description,amount\n2026-08-10,MERCADO CENTRAL,-50.00\n"
	out := e.resolveAll(t, e.wsA, e.prepare(t, e.wsA, "acct-1", a))
	e.execute(t, e.wsA, ImportCommitTool, map[string]any{"batch_id": impStr(t, out, "batch_id")})

	b := "date,description,amount\n2026-08-10,MERCADO CENTRAL LTDA,-50.00\n"
	again := e.prepare(t, e.wsA, "acct-1", b)
	// This is the documented limitation, asserted so it cannot change in
	// silence: without a bank identifier, a rewritten description is a new
	// transaction as far as the fingerprint can tell.
	if impNum(t, again, "maybe_duplicate") != 0 {
		t.Error("unexpected: the fingerprint survived a description change")
	}
	if impNum(t, again, "needs_category")+impNum(t, again, "ready") != 1 {
		t.Fatalf("the renamed line should look new: %v", again)
	}
}

func TestSoftDeletedRowDoesNotBlockReimport(t *testing.T) {
	e := newEnv(t)
	csv := "date,description,amount,fitid\n2026-08-10,MERCADO,-50.00,B1\n"
	out := e.resolveAll(t, e.wsA, e.prepare(t, e.wsA, "acct-1", csv))
	e.execute(t, e.wsA, ImportCommitTool, map[string]any{"batch_id": impStr(t, out, "batch_id")})

	_, err := e.pool.Exec(context.Background(),
		`UPDATE finance.transactions SET deleted_at=now() WHERE workspace_id=$1`, e.wsA)
	if err != nil {
		t.Fatal(err)
	}
	// Declared behaviour: the unique index is partial on deleted_at, so a
	// removed row stops blocking. Re-importing is an explicit request to
	// bring it back, and the alternative would block it forever with no
	// way for the operator to see why.
	again := e.resolveAll(t, e.wsA, e.prepare(t, e.wsA, "acct-1", csv))
	res := e.execute(t, e.wsA, ImportCommitTool, map[string]any{"batch_id": impStr(t, again, "batch_id")})
	if impNum(t, res, "created") != 1 {
		t.Fatalf("created = %d after the row was deleted, want 1", impNum(t, res, "created"))
	}
}

/* ── categories ──────────────────────────────────────────────────────── */

func TestImporterNeverCreatesACategory(t *testing.T) {
	e := newEnv(t)
	before := e.categoryCount(e.wsA)
	out := e.prepare(t, e.wsA, "acct-1", basicCSV)
	if e.categoryCount(e.wsA) != before {
		t.Fatal("prepare created a category")
	}
	if impNum(t, out, "needs_category") == 0 {
		t.Fatal("with no categories, lines must be unresolved rather than filed somewhere")
	}
	// Commit with everything unresolved imports nothing.
	res := e.execute(t, e.wsA, ImportCommitTool, map[string]any{"batch_id": impStr(t, out, "batch_id")})
	if impNum(t, res, "created") != 0 {
		t.Fatalf("created = %d with no category resolved", impNum(t, res, "created"))
	}
	if e.categoryCount(e.wsA) != before {
		t.Fatal("commit created a category")
	}
}

func TestResolveGroupRefusesACategoryOfTheWrongDirection(t *testing.T) {
	e := newEnv(t)
	out := e.prepare(t, e.wsA, "acct-1", "date,description,amount\n2026-08-06,MERCADO,-150.00\n")
	income := e.seedCategory(e.wsA, "Salario", domain.EntryTypeIncome)
	_, err := e.executeErr(e.wsA, ImportResolveGroupTool, map[string]any{
		"batch_id": impStr(t, out, "batch_id"), "group_key": "MERCADO",
		"category_id": income.ID.String(),
	})
	if err == nil {
		t.Fatal("an income category must not be attachable to an expense line")
	}
}

func TestResolveGroupRefusesACategoryFromAnotherWorkspace(t *testing.T) {
	e := newEnv(t)
	out := e.prepare(t, e.wsA, "acct-1", "date,description,amount\n2026-08-06,MERCADO,-150.00\n")
	foreign := e.seedCategory(e.wsB, "Mercado", domain.EntryTypeExpense)
	_, err := e.executeErr(e.wsA, ImportResolveGroupTool, map[string]any{
		"batch_id": impStr(t, out, "batch_id"), "group_key": "MERCADO",
		"category_id": foreign.ID.String(),
	})
	if err == nil {
		t.Fatal("a category from another workspace must not be usable, even named correctly")
	}
}

/* ── workspace isolation ─────────────────────────────────────────────── */

func TestBatchOfAnotherWorkspaceIsIndistinguishableFromAFabricatedOne(t *testing.T) {
	e := newEnv(t)
	out := e.prepare(t, e.wsA, "acct-1", basicCSV)
	batchID := impStr(t, out, "batch_id")

	_, foreignErr := e.executeErr(e.wsB, ImportCommitTool, map[string]any{"batch_id": batchID})
	_, fakeErr := e.executeErr(e.wsB, ImportCommitTool, map[string]any{"batch_id": uuid.NewString()})
	if foreignErr == nil || fakeErr == nil {
		t.Fatal("both must be refused")
	}
	if foreignErr.Error() != fakeErr.Error() {
		t.Fatalf("existence leaked: %q vs %q", foreignErr, fakeErr)
	}
}

func TestDedupNeverCrossesWorkspaces(t *testing.T) {
	e := newEnv(t)
	csv := "date,description,amount,fitid\n2026-08-10,MERCADO,-50.00,SAME-ID\n"
	outA := e.resolveAll(t, e.wsA, e.prepare(t, e.wsA, "acct-1", csv))
	e.execute(t, e.wsA, ImportCommitTool, map[string]any{"batch_id": impStr(t, outA, "batch_id")})

	outB := e.prepare(t, e.wsB, "acct-1", csv)
	if impNum(t, outB, "already_imported") != 0 {
		t.Fatal("workspace B deduplicated against workspace A's identity")
	}
	outB = e.resolveAll(t, e.wsB, outB)
	res := e.execute(t, e.wsB, ImportCommitTool, map[string]any{"batch_id": impStr(t, outB, "batch_id")})
	if impNum(t, res, "created") != 1 {
		t.Fatalf("workspace B created %d, want its own copy", impNum(t, res, "created"))
	}
}

/* ── commit semantics ────────────────────────────────────────────────── */

func TestCommitIsRefusedTwice(t *testing.T) {
	e := newEnv(t)
	out := e.resolveAll(t, e.wsA, e.prepare(t, e.wsA, "acct-1", basicCSV))
	id := impStr(t, out, "batch_id")
	e.execute(t, e.wsA, ImportCommitTool, map[string]any{"batch_id": id})
	if _, err := e.executeErr(e.wsA, ImportCommitTool, map[string]any{"batch_id": id}); err == nil {
		t.Fatal("committing the same batch twice must be refused")
	}
	if got := e.liveCount(e.wsA); got != 4 {
		t.Fatalf("ledger holds %d rows after a double commit attempt", got)
	}
}

func TestCentsSurviveTheWholePath(t *testing.T) {
	e := newEnv(t)
	csv := "date,description,amount\n2026-08-10,PRECISO,-1234.56\n2026-08-11,CENTAVO,-0.01\n"
	out := e.resolveAll(t, e.wsA, e.prepare(t, e.wsA, "acct-1", csv))
	e.execute(t, e.wsA, ImportCommitTool, map[string]any{"batch_id": impStr(t, out, "batch_id")})

	rows, err := e.pool.Query(context.Background(),
		`SELECT amount_cents FROM finance.transactions
		  WHERE workspace_id=$1 AND deleted_at IS NULL ORDER BY amount_cents`, e.wsA)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []int64
	for rows.Next() {
		var c int64
		_ = rows.Scan(&c)
		got = append(got, c)
	}
	// Read as integers straight from Postgres: a formatting round trip
	// could hide a lost cent, so no rendering is involved.
	if len(got) != 2 || got[0] != 1 || got[1] != 123456 {
		t.Fatalf("amount_cents = %v, want [1 123456]", got)
	}
}

func TestImportedRowsCarrySourceImportAndTheDerivedNamespace(t *testing.T) {
	e := newEnv(t)
	id := e.importSource(t, e.wsA, "acct-9")
	out := e.resolveAll(t, e.wsA, e.prepare(t, e.wsA, "acct-9", "date,description,amount\n2026-08-10,MERCADO,-50.00\n"))
	e.execute(t, e.wsA, ImportCommitTool, map[string]any{"batch_id": impStr(t, out, "batch_id")})

	var source, extSource string
	err := e.pool.QueryRow(context.Background(),
		`SELECT source::text, external_source FROM finance.transactions
		  WHERE workspace_id=$1 AND deleted_at IS NULL`, e.wsA).Scan(&source, &extSource)
	if err != nil {
		t.Fatal(err)
	}
	if source != "import" {
		t.Errorf("source = %q, want import", source)
	}
	// The namespace is derived from the source's id. Nothing the caller
	// wrote appears in it.
	if extSource != "import-source:"+id {
		t.Errorf("external_source = %q, want the derived namespace", extSource)
	}
}

func TestOversizedStatementIsRefusedWholeNotTruncated(t *testing.T) {
	e := newEnv(t)
	var b strings.Builder
	b.WriteString("date,description,amount\n")
	for i := 0; i < maxRowsForTest+1; i++ {
		fmt.Fprintf(&b, "2026-08-10,LINHA %d,-1.00\n", i)
	}
	if _, err := e.executeErr(e.wsA, ImportPrepareTool, map[string]any{
		"source_label": "big", "account_scope": "acct-1", "content": b.String(),
	}); err == nil {
		t.Fatal("an oversized statement must be refused, never silently truncated")
	}
	if e.liveCount(e.wsA) != 0 {
		t.Fatal("a refused statement wrote rows")
	}
}

const maxRowsForTest = 1000

/* ── the batch id the model does not have to remember ────────────────── */

// A 36-character uuid has to survive several conversational turns inside a
// model's context. In the first live run of this flow it did not: the
// model produced a string that was not a uuid, and the commit was refused.
// Refusing was right; failing an import for that reason was not.
func TestOmittedBatchIDMeansTheLatestPrepared(t *testing.T) {
	e := newEnv(t)
	out := e.resolveAll(t, e.wsA, e.prepare(t, e.wsA, "acct-1", basicCSV))
	want := impStr(t, out, "batch_id")

	res := e.execute(t, e.wsA, ImportCommitTool, map[string]any{})
	if got := impStr(t, res, "batch_id"); got != want {
		t.Fatalf("committed %s, want the batch just prepared %s", got, want)
	}
	if impNum(t, res, "created") == 0 {
		t.Fatal("the defaulted commit imported nothing")
	}
}

// Omitting is correct; guessing is not. A malformed id must fail rather
// than fall back, or a typo would import a statement nobody named.
func TestMalformedBatchIDIsRefusedRatherThanDefaulted(t *testing.T) {
	e := newEnv(t)
	e.resolveAll(t, e.wsA, e.prepare(t, e.wsA, "acct-1", basicCSV))
	if _, err := e.executeErr(e.wsA, ImportCommitTool, map[string]any{"batch_id": "not-a-uuid"}); err == nil {
		t.Fatal("a malformed batch_id must be refused, never silently replaced")
	}
	if e.liveCount(e.wsA) != 0 {
		t.Fatal("a refused commit wrote rows")
	}
}

// The default is workspace-scoped: it can never reach across.
func TestDefaultBatchNeverCrossesWorkspaces(t *testing.T) {
	e := newEnv(t)
	e.resolveAll(t, e.wsA, e.prepare(t, e.wsA, "acct-1", basicCSV))
	if _, err := e.executeErr(e.wsB, ImportCommitTool, map[string]any{}); err == nil {
		t.Fatal("workspace B committed workspace A's prepared batch by omitting the id")
	}
	if e.liveCount(e.wsB) != 0 {
		t.Fatal("workspace B gained rows")
	}
}

// Repeating a classification is not an error.
//
// The live run that found this had the model re-issue five resolve calls
// it had already made; every one failed, and the model concluded from the
// failures that its batch no longer existed. An idempotent operation that
// reports failure teaches a caller something false.
func TestResolvingTheSameGroupTwiceIsANoOpNotAnError(t *testing.T) {
	e := newEnv(t)
	out := e.prepare(t, e.wsA, "acct-1", "date,description,amount\n2026-08-06,MERCADO,-150.00\n")
	cat := e.seedCategory(e.wsA, "Mercado", domain.EntryTypeExpense)
	args := map[string]any{
		"batch_id": impStr(t, out, "batch_id"), "group_key": "MERCADO",
		"category_id": cat.ID.String(),
	}
	first := e.execute(t, e.wsA, ImportResolveGroupTool, args)
	second := e.execute(t, e.wsA, ImportResolveGroupTool, args)
	if impNum(t, first, "ready") != 1 || impNum(t, second, "ready") != 1 {
		t.Fatalf("ready went %d then %d", impNum(t, first, "ready"), impNum(t, second, "ready"))
	}
}

// Re-resolving to a DIFFERENT category is still refused: the rows are
// already ready, so nothing moves, and silently accepting would suggest a
// reclassification that did not happen.
func TestResolvingAResolvedGroupToAnotherCategoryIsRefused(t *testing.T) {
	e := newEnv(t)
	out := e.prepare(t, e.wsA, "acct-1", "date,description,amount\n2026-08-06,MERCADO,-150.00\n")
	a := e.seedCategory(e.wsA, "Mercado", domain.EntryTypeExpense)
	b := e.seedCategory(e.wsA, "Outra", domain.EntryTypeExpense)
	id := impStr(t, out, "batch_id")
	e.execute(t, e.wsA, ImportResolveGroupTool, map[string]any{
		"batch_id": id, "group_key": "MERCADO", "category_id": a.ID.String()})
	if _, err := e.executeErr(e.wsA, ImportResolveGroupTool, map[string]any{
		"batch_id": id, "group_key": "MERCADO", "category_id": b.ID.String()}); err == nil {
		t.Fatal("reclassifying an already-ready group must not report a success it did not perform")
	}
}

// The preview carries the workspace's categories.
//
// Not a convenience. Finance capabilities are Confidential, so the audit
// records that feed a later turn's evidence are redacted, and a model
// cannot see what it observed in an earlier turn. It therefore re-derives
// the whole flow every turn, and a turn allows three tool rounds: preview,
// resolutions, commit. A separate category listing consumes the round the
// commit needed, which is how a live run looped forever without importing.
func TestPreviewCarriesTheCategoriesSoNoRoundIsWastedOnThem(t *testing.T) {
	e := newEnv(t)
	e.seedCategory(e.wsA, "Mercado", domain.EntryTypeExpense)
	e.seedCategory(e.wsA, "Salario", domain.EntryTypeIncome)

	out := e.prepare(t, e.wsA, "acct-1", basicCSV)
	raw, ok := out["categories"].([]any)
	if !ok || len(raw) != 2 {
		t.Fatalf("categories = %v, want the workspace's two", out["categories"])
	}
	first := raw[0].(map[string]any)
	for _, k := range []string{"id", "name", "direction"} {
		if first[k] == nil || first[k] == "" {
			t.Errorf("category entry is missing %q: %v", k, first)
		}
	}
	// And it stays a listing, not a decision: a category whose name matches
	// nothing in the file must not attach itself to anything.
	if impNum(t, out, "needs_category") == 0 {
		t.Error("carrying the categories must not classify anything by itself")
	}
}

// Only an EXACT normalised name resolves automatically. Anything looser
// files real money under a label the operator never chose.
func TestOnlyAnExactCategoryNameResolvesWithoutBeingAsked(t *testing.T) {
	e := newEnv(t)
	e.seedCategory(e.wsA, "Mercado", domain.EntryTypeExpense)
	out := e.prepare(t, e.wsA, "acct-1", "date,description,amount\n2026-08-06,MERCADO CENTRAL,-150.00\n")
	if impNum(t, out, "ready") != 0 {
		t.Fatal("\"MERCADO CENTRAL\" was filed under \"Mercado\" by a substring match")
	}
	exact := e.prepare(t, e.wsA, "acct-2", "date,description,amount\n2026-08-06,Mercado,-150.00\n")
	if impNum(t, exact, "ready") != 1 {
		t.Fatalf("an exact name should resolve: %v", exact)
	}
}

// Preparing the same statement twice resumes the same batch.
//
// A model re-derives its state every turn, because Confidential
// capabilities leave no result in the audit rows the evidence replay is
// built from. If each re-preparation created a fresh batch, the
// classifications made a turn ago would be silently thrown away, and with
// three tool rounds per turn the commit would never be reached. That is
// not a hypothetical: it is what a live run did, twice.
func TestRePreparingTheSameStatementResumesTheSameBatch(t *testing.T) {
	e := newEnv(t)
	first := e.prepare(t, e.wsA, "acct-1", basicCSV)
	id := impStr(t, first, "batch_id")

	resolved := e.resolveAll(t, e.wsA, first)
	if impNum(t, resolved, "ready") == 0 {
		t.Fatal("nothing was resolved")
	}

	again := e.prepare(t, e.wsA, "acct-1", basicCSV)
	if impStr(t, again, "batch_id") != id {
		t.Fatal("re-preparing the same file created a second batch and discarded the classifications")
	}
	if impNum(t, again, "ready") != impNum(t, resolved, "ready") {
		t.Fatalf("ready went from %d to %d across a re-preparation",
			impNum(t, resolved, "ready"), impNum(t, again, "ready"))
	}
	var batches int
	_ = e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM finance.import_batches WHERE workspace_id=$1`, e.wsA).Scan(&batches)
	if batches != 1 {
		t.Fatalf("%d batches exist, want 1", batches)
	}
}

// A different statement is a different batch, even for the same account.
func TestADifferentStatementIsADifferentBatch(t *testing.T) {
	e := newEnv(t)
	a := e.prepare(t, e.wsA, "acct-1", "date,description,amount\n2026-08-06,MERCADO,-150.00\n")
	b := e.prepare(t, e.wsA, "acct-1", "date,description,amount\n2026-08-07,PADARIA,-12.00\n")
	if impStr(t, a, "batch_id") == impStr(t, b, "batch_id") {
		t.Fatal("two different statements shared a batch")
	}
}

// A committed batch is finished. Re-preparing the same file after an
// import starts a new one, which is what lets the duplicate detection run
// and report that everything is already in the ledger.
func TestRePreparingAfterCommitStartsAFreshBatch(t *testing.T) {
	e := newEnv(t)
	out := e.resolveAll(t, e.wsA, e.prepare(t, e.wsA, "acct-1", basicCSV))
	id := impStr(t, out, "batch_id")
	e.execute(t, e.wsA, ImportCommitTool, map[string]any{"batch_id": id})

	again := e.prepare(t, e.wsA, "acct-1", basicCSV)
	if impStr(t, again, "batch_id") == id {
		t.Fatal("a committed batch was reused")
	}
}

// ── Blocker 1: the identity namespace is issued, not spelled ────────
//
// The defect: account_scope was free text, and a live model produced
// "nubank-4242" in one turn and "nubank-cartao-4242" in the next for the
// same card, importing the same statement twice.
//
// The fix is not a better prompt. The argument stopped existing: prepare
// takes an id this database issued, and external_source is derived from
// it. These tests are the eight proofs the fix was asked to carry.

// 1 and 2. The same source, selected from different conversations and
// described with different words, is the same namespace.
func TestSameSourceDifferentWordsIsTheSameNamespace(t *testing.T) {
	e := newEnv(t)
	id := e.importSource(t, e.wsA, "Nubank")
	csv := "date,description,amount,fitid\n2026-08-10,MERCADO,-50.00,B1\n"

	first := e.execute(t, e.wsA, ImportPrepareTool, map[string]any{
		"import_source_id": id, "source_label": "extrato nubank agosto", "content": csv})
	e.resolveAll(t, e.wsA, first)
	e.execute(t, e.wsA, ImportCommitTool, map[string]any{"batch_id": impStr(t, first, "batch_id")})

	// A different label, the wording a model varied last time. It is
	// display only and cannot reach the identity.
	second := e.execute(t, e.wsA, ImportPrepareTool, map[string]any{
		"import_source_id": id, "source_label": "nubank cartao 4242 agosto", "content": csv})
	if impNum(t, second, "already_imported") != 1 {
		t.Fatalf("the same statement under the same source was not recognised: %v", second)
	}
	res := e.execute(t, e.wsA, ImportCommitTool, map[string]any{"batch_id": impStr(t, second, "batch_id")})
	if impNum(t, res, "created") != 0 {
		t.Fatalf("created %d on re-import", impNum(t, res, "created"))
	}
	if got := e.liveCount(e.wsA); got != 1 {
		t.Fatalf("ledger holds %d rows, want 1", got)
	}
}

// 6. Another source in the same workspace does not collide: two real
// accounts may legitimately carry the same bank identifier.
func TestADifferentSourceInTheSameWorkspaceDoesNotCollide(t *testing.T) {
	e := newEnv(t)
	csv := "date,description,amount,fitid\n2026-08-10,MERCADO,-50.00,B1\n"
	a := e.resolveAll(t, e.wsA, e.prepare(t, e.wsA, "Nubank", csv))
	e.execute(t, e.wsA, ImportCommitTool, map[string]any{"batch_id": impStr(t, a, "batch_id")})

	b := e.prepare(t, e.wsA, "Itau", csv)
	if impNum(t, b, "already_imported") != 0 {
		t.Fatal("a second account was deduplicated against the first")
	}
	b = e.resolveAll(t, e.wsA, b)
	res := e.execute(t, e.wsA, ImportCommitTool, map[string]any{"batch_id": impStr(t, b, "batch_id")})
	if impNum(t, res, "created") != 1 || e.liveCount(e.wsA) != 2 {
		t.Fatalf("created %d, ledger %d", impNum(t, res, "created"), e.liveCount(e.wsA))
	}
}

// 7. A source id from another workspace is not reachable, and is refused
// exactly as a fabricated one is.
func TestImportSourceOfAnotherWorkspaceIsNotUsable(t *testing.T) {
	e := newEnv(t)
	foreign := e.importSource(t, e.wsB, "Nubank")
	csv := "date,description,amount\n2026-08-10,MERCADO,-50.00\n"

	_, foreignErr := e.executeErr(e.wsA, ImportPrepareTool, map[string]any{
		"import_source_id": foreign, "content": csv})
	_, fakeErr := e.executeErr(e.wsA, ImportPrepareTool, map[string]any{
		"import_source_id": uuid.NewString(), "content": csv})
	if foreignErr == nil || fakeErr == nil {
		t.Fatal("both must be refused")
	}
	if foreignErr.Error() != fakeErr.Error() {
		t.Fatalf("existence leaked: %q vs %q", foreignErr, fakeErr)
	}
}

// 8. No text the model supplies participates in the namespace. Asserted
// against the stored value, which is what dedup actually matches on.
func TestModelSuppliedTextNeverEntersTheIdentityNamespace(t *testing.T) {
	e := newEnv(t)
	id := e.importSource(t, e.wsA, "Nubank")
	out := e.resolveAll(t, e.wsA, e.execute(t, e.wsA, ImportPrepareTool, map[string]any{
		"import_source_id": id,
		"source_label":     "QUALQUER COISA QUE O MODELO ESCREVA",
		"content":          "date,description,amount\n2026-08-10,MERCADO,-50.00\n",
	}))
	e.execute(t, e.wsA, ImportCommitTool, map[string]any{"batch_id": impStr(t, out, "batch_id")})

	var external string
	if err := e.pool.QueryRow(context.Background(),
		`SELECT external_source FROM finance.transactions
		  WHERE workspace_id=$1 AND deleted_at IS NULL`, e.wsA).Scan(&external); err != nil {
		t.Fatal(err)
	}
	if external != "import-source:"+id {
		t.Fatalf("external_source = %q, want the derived namespace", external)
	}
	if strings.Contains(external, "QUALQUER") {
		t.Fatal("model text reached the identity")
	}
}

// Renaming a source must not make its history look new: the namespace is
// derived from the id, and the label is not part of it.
func TestRenamingASourceDoesNotOrphanItsHistory(t *testing.T) {
	e := newEnv(t)
	id := e.importSource(t, e.wsA, "Nubank")
	csv := "date,description,amount,fitid\n2026-08-10,MERCADO,-50.00,B1\n"
	out := e.resolveAll(t, e.wsA, e.execute(t, e.wsA, ImportPrepareTool, map[string]any{
		"import_source_id": id, "content": csv}))
	e.execute(t, e.wsA, ImportCommitTool, map[string]any{"batch_id": impStr(t, out, "batch_id")})

	if _, err := e.pool.Exec(context.Background(),
		`UPDATE finance.import_sources SET label='Nubank Platinum' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	again := e.execute(t, e.wsA, ImportPrepareTool, map[string]any{
		"import_source_id": id, "content": csv})
	if impNum(t, again, "already_imported") != 1 {
		t.Fatalf("a rename detached the source from its own history: %v", again)
	}
}
