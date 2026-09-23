//go:build integration

// Recurring occurrences, against a real Postgres.
//
// The sentence this file has to make convincing:
//
//	A month can be materialised lazily, repeatedly, and by two readers at
//	the same instant, and what comes out is exactly one row per obligation
//	per month — which, once written, nothing recomputes.
//
// What is REAL here: the migration, the schema, its constraints, the
// domain, the repository and Postgres. Nothing is faked, and nothing goes
// through the application service, because the primitives below are what
// that service will be built out of and this is where they are proved.
package finance

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/corsi/backend/internal/finance/adapters/repo"
	"github.com/corsi/backend/internal/finance/app"
	"github.com/corsi/backend/internal/finance/domain"
	"github.com/corsi/backend/internal/platform/postgres"
)

/* ── harness ─────────────────────────────────────────────────────────── */

type occEnv struct {
	t     *testing.T
	pool  *pgxpool.Pool
	repos *repo.Repositories
	// svc is here for the one claim that belongs to the application layer
	// rather than to storage: the domain invariants are checked on the way
	// IN, and the repositories in this module are deliberately dumb
	// writers. Asserting "a bad row is refused" against a repository would
	// be asserting it against the wrong thing.
	svc *app.Service
	loc *time.Location
	// Two workspaces, always. Every isolation assertion reads wsA's months
	// back as wsB.
	wsA uuid.UUID
	wsB uuid.UUID
}

func newOccEnv(t *testing.T) *occEnv {
	t.Helper()
	d := dsn(t)
	freshDB(t, d)

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, d)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)

	loc, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	repos := repo.New(pool)
	return &occEnv{
		t: t, pool: pool, repos: repos, loc: loc,
		svc: app.NewService(repos, postgres.NewTxManager(pool),
			slog.New(slog.NewJSONHandler(io.Discard, nil)), loc),
		wsA: uuid.New(), wsB: uuid.New(),
	}
}

// seedCategory and seedEntry go through the repositories, so a fixture
// cannot be a shape the product is unable to produce.
func (e *occEnv) seedCategory(ws uuid.UUID) *domain.Category {
	e.t.Helper()
	c := &domain.Category{
		ID: uuid.New(), WorkspaceID: ws, Name: "Fixture " + uuid.NewString()[:8],
		Type: domain.EntryTypeExpense, Color: "#123456", Icon: "tag",
	}
	if err := e.repos.Categories.Create(context.Background(), c); err != nil {
		e.t.Fatalf("seed category: %v", err)
	}
	return c
}

// seedEntry creates a monthly recurring definition. Amounts and
// descriptions are synthetic throughout this file: no operator data is
// read, written or referenced anywhere in it.
func (e *occEnv) seedEntry(ws uuid.UUID, cat *domain.Category, cents int64, dueDay int) *domain.RecurringEntry {
	e.t.Helper()
	f := &domain.RecurringEntry{
		ID: uuid.New(), WorkspaceID: ws, Description: "fixture recurring",
		AmountCents: cents, CategoryID: cat.ID, DueDay: dueDay,
		Recurrence: domain.RecurrenceMonthly, Status: domain.StatusActive,
		StartsAt: time.Date(2026, time.January, 1, 12, 0, 0, 0, e.loc),
	}
	if err := f.Validate(); err != nil {
		e.t.Fatalf("fixture does not validate: %v", err)
	}
	if err := e.repos.RecurringEntries.Create(context.Background(), f); err != nil {
		e.t.Fatalf("seed recurring entry: %v", err)
	}
	return f
}

// materialise is the read-through step the application service will make:
// build what the definitions imply for a month, and insert what is absent.
func (e *occEnv) materialise(ws uuid.UUID, p domain.Period, now time.Time, entries ...*domain.RecurringEntry) int {
	e.t.Helper()
	var want []domain.RecurringOccurrence
	for _, f := range entries {
		if !f.ActiveIn(p, e.loc) {
			continue
		}
		o, err := domain.NewOccurrence(f, p, e.loc, now)
		if err != nil {
			e.t.Fatalf("build occurrence: %v", err)
		}
		want = append(want, *o)
	}
	created, err := e.repos.RecurringOccurrences.EnsureMissing(context.Background(), want)
	if err != nil {
		e.t.Fatalf("EnsureMissing: %v", err)
	}
	return created
}

func (e *occEnv) list(ws uuid.UUID, p domain.Period) []domain.RecurringOccurrence {
	e.t.Helper()
	got, err := e.repos.RecurringOccurrences.ListByPeriod(context.Background(), ws, p)
	if err != nil {
		e.t.Fatalf("ListByPeriod: %v", err)
	}
	return got
}

// rawCount reads the table directly, past every Go type in between. A
// readback through the same repository that wrote the rows would agree
// with a consistent mistake; this does not.
func (e *occEnv) rawCount(ws uuid.UUID) int {
	e.t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM finance.recurring_occurrences WHERE workspace_id = $1`, ws).Scan(&n); err != nil {
		e.t.Fatalf("count: %v", err)
	}
	return n
}

/* ── the tests ───────────────────────────────────────────────────────── */

// Materialising a month twice produces one set of rows, not two.
func TestMaterialisationIsIdempotent(t *testing.T) {
	e := newOccEnv(t)
	cat := e.seedCategory(e.wsA)
	rent := e.seedEntry(e.wsA, cat, 250000, 5)
	gym := e.seedEntry(e.wsA, cat, 17000, 10)

	p := domain.MustPeriod(2026, time.September)
	now := time.Date(2026, time.September, 20, 10, 0, 0, 0, e.loc)

	if created := e.materialise(e.wsA, p, now, rent, gym); created != 2 {
		t.Fatalf("first materialisation created %d rows, want 2", created)
	}
	// Every subsequent read of the same month must create nothing at all.
	for i := 0; i < 3; i++ {
		if created := e.materialise(e.wsA, p, now, rent, gym); created != 0 {
			t.Fatalf("re-reading the month created %d rows", created)
		}
	}
	if n := e.rawCount(e.wsA); n != 2 {
		t.Fatalf("the table holds %d rows for two obligations in one month", n)
	}
	if got := len(e.list(e.wsA, p)); got != 2 {
		t.Fatalf("the month lists %d occurrences", got)
	}
}

// TestConcurrentMaterialisationProducesOneRow is the claim that makes lazy
// materialisation safe without a scheduler or a lock.
//
// Eight goroutines open the same month at the same instant. Every one of
// them finds it empty, every one of them inserts, and the unique index on
// (recurring_entry_id, period) decides. Exactly one row may exist
// afterwards, and the created counts must add up to exactly one: a
// primitive that reported two creations for one row would make an
// application layer believe it had materialised a month twice.
func TestConcurrentMaterialisationProducesOneRow(t *testing.T) {
	e := newOccEnv(t)
	cat := e.seedCategory(e.wsA)
	rent := e.seedEntry(e.wsA, cat, 250000, 5)

	p := domain.MustPeriod(2026, time.September)
	now := time.Date(2026, time.September, 20, 10, 0, 0, 0, e.loc)

	const readers = 8
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		total   int
		failure error
		start   = make(chan struct{})
	)
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			o, err := domain.NewOccurrence(rent, p, e.loc, now)
			if err != nil {
				mu.Lock()
				failure = err
				mu.Unlock()
				return
			}
			<-start // every goroutine races from the same instant
			created, err := e.repos.RecurringOccurrences.EnsureMissing(
				context.Background(), []domain.RecurringOccurrence{*o})
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failure = err
				return
			}
			total += created
		}()
	}
	close(start)
	wg.Wait()

	if failure != nil {
		t.Fatalf("a concurrent materialisation failed instead of yielding: %v", failure)
	}
	if total != 1 {
		t.Fatalf("%d readers reported %d rows created between them, want exactly 1", readers, total)
	}
	if n := e.rawCount(e.wsA); n != 1 {
		t.Fatalf("the table holds %d rows after a concurrent race", n)
	}
}

// TestExistingOccurrenceSurvivesTheDefinitionChanging is historical truth.
//
// ══════════════════════════════════════════════════════════════════════
//
//	EDITING A DEFINITION MUST NOT REWRITE A MONTH THAT EXISTS
//
// ══════════════════════════════════════════════════════════════════════
//
// September is materialised and settled. The rent then rises and the due
// day moves. Re-reading September, which is what happens every time the
// screen is opened, must find the row exactly as it was: the old amount,
// the old due date, still paid. October, materialised afterwards, is the
// one that carries the new figures.
func TestExistingOccurrenceSurvivesTheDefinitionChanging(t *testing.T) {
	e := newOccEnv(t)
	ctx := context.Background()
	cat := e.seedCategory(e.wsA)
	rent := e.seedEntry(e.wsA, cat, 250000, 5)

	sep := domain.MustPeriod(2026, time.September)
	oct := domain.MustPeriod(2026, time.October)
	now := time.Date(2026, time.September, 20, 10, 0, 0, 0, e.loc)

	e.materialise(e.wsA, sep, now, rent)
	before := e.list(e.wsA, sep)
	if len(before) != 1 {
		t.Fatalf("September has %d occurrences", len(before))
	}

	// Settle it, the way the operator will.
	paidAt := time.Date(2026, time.September, 4, 9, 0, 0, 0, time.UTC)
	settled := before[0]
	if err := settled.MarkPaid(paidAt); err != nil {
		t.Fatalf("MarkPaid: %v", err)
	}
	if err := e.repos.RecurringOccurrences.Update(ctx, &settled); err != nil {
		t.Fatalf("Update: %v", err)
	}

	// The rent rises and the due day moves.
	rent.AmountCents = 300000
	rent.DueDay = 12
	if err := e.repos.RecurringEntries.Update(ctx, rent); err != nil {
		t.Fatalf("update definition: %v", err)
	}

	// Re-reading September is what the screen does on every open.
	nowLater := time.Date(2026, time.October, 2, 10, 0, 0, 0, e.loc)
	if created := e.materialise(e.wsA, sep, nowLater, rent); created != 0 {
		t.Fatalf("re-reading a materialised month created %d rows", created)
	}
	after := e.list(e.wsA, sep)
	if len(after) != 1 {
		t.Fatalf("September now has %d occurrences", len(after))
	}
	if after[0].AmountCents != 250000 {
		t.Fatalf("September's amount became %d cents; the definition rewrote history", after[0].AmountCents)
	}
	if got := after[0].DueOn.Format("2006-01-02"); got != "2026-09-05" {
		t.Fatalf("September's due date moved to %s", got)
	}
	if after[0].Status != domain.OccurrencePaid || after[0].PaidAt == nil {
		t.Fatal("September stopped being paid")
	}
	if !after[0].PaidAt.Equal(paidAt) {
		t.Fatalf("the payment date moved to %s", after[0].PaidAt)
	}

	// October is where the new figures land.
	e.materialise(e.wsA, oct, nowLater, rent)
	octRows := e.list(e.wsA, oct)
	if len(octRows) != 1 {
		t.Fatalf("October has %d occurrences", len(octRows))
	}
	if octRows[0].AmountCents != 300000 {
		t.Fatalf("October's amount is %d cents, want the new 300000", octRows[0].AmountCents)
	}
	if got := octRows[0].DueOn.Format("2006-01-02"); got != "2026-10-12" {
		t.Fatalf("October's due date is %s, want the new 2026-10-12", got)
	}
	if octRows[0].Status != domain.OccurrencePending {
		t.Fatal("a freshly materialised month is not pending")
	}
}

// One workspace's months are invisible and untouchable from another.
func TestOccurrenceWorkspaceIsolation(t *testing.T) {
	e := newOccEnv(t)
	ctx := context.Background()

	catA := e.seedCategory(e.wsA)
	entryA := e.seedEntry(e.wsA, catA, 250000, 5)
	catB := e.seedCategory(e.wsB)
	entryB := e.seedEntry(e.wsB, catB, 9900, 20)

	p := domain.MustPeriod(2026, time.September)
	now := time.Date(2026, time.September, 20, 10, 0, 0, 0, e.loc)
	e.materialise(e.wsA, p, now, entryA)
	e.materialise(e.wsB, p, now, entryB)

	a := e.list(e.wsA, p)
	b := e.list(e.wsB, p)
	if len(a) != 1 || len(b) != 1 {
		t.Fatalf("workspace A sees %d and B sees %d", len(a), len(b))
	}
	if a[0].AmountCents == b[0].AmountCents {
		t.Fatal("the two workspaces are looking at the same row")
	}

	// Resolving A's month as B finds nothing, and vice versa.
	if _, err := e.repos.RecurringOccurrences.FindByEntryPeriod(ctx, e.wsB, entryA.ID, p); err == nil {
		t.Fatal("workspace B resolved workspace A's occurrence")
	}
	if _, err := e.repos.RecurringOccurrences.FindByEntryPeriod(ctx, e.wsA, entryB.ID, p); err == nil {
		t.Fatal("workspace A resolved workspace B's occurrence")
	}

	// And writing it as the wrong workspace changes nothing. The row is
	// addressed by its real id, so only the workspace predicate stands
	// between B and A's money.
	stolen := a[0]
	stolen.WorkspaceID = e.wsB
	stolen.AmountCents = 1
	if err := e.repos.RecurringOccurrences.Update(ctx, &stolen); err == nil {
		t.Fatal("workspace B updated workspace A's occurrence")
	}
	if got := e.list(e.wsA, p); got[0].AmountCents != 250000 {
		t.Fatalf("A's amount became %d cents after B's attempt", got[0].AmountCents)
	}
}

// A soft-deleted definition takes its months out of every reading, without
// any caller having to remember a filter.
func TestOccurrencesOfASoftDeletedDefinitionDisappear(t *testing.T) {
	e := newOccEnv(t)
	ctx := context.Background()
	cat := e.seedCategory(e.wsA)
	kept := e.seedEntry(e.wsA, cat, 250000, 5)
	removed := e.seedEntry(e.wsA, cat, 17000, 10)

	p := domain.MustPeriod(2026, time.September)
	now := time.Date(2026, time.September, 20, 10, 0, 0, 0, e.loc)
	e.materialise(e.wsA, p, now, kept, removed)
	if got := len(e.list(e.wsA, p)); got != 2 {
		t.Fatalf("the month lists %d occurrences", got)
	}

	if err := e.repos.RecurringEntries.SoftDelete(ctx, e.wsA, removed.ID); err != nil {
		t.Fatalf("soft delete: %v", err)
	}

	got := e.list(e.wsA, p)
	if len(got) != 1 || got[0].RecurringEntryID != kept.ID {
		t.Fatalf("the month lists %d occurrences after a soft delete", len(got))
	}
	// The row itself is not destroyed: a soft delete is reversible and
	// deleting somebody's payment history is not this operation's job.
	if n := e.rawCount(e.wsA); n != 2 {
		t.Fatalf("the table holds %d rows; a soft delete destroyed data", n)
	}
}

// A back-filled month is reconstructed from today's definition and is
// marked as such, all the way through storage.
func TestBackfilledMonthIsStoredAsEstimated(t *testing.T) {
	e := newOccEnv(t)
	cat := e.seedCategory(e.wsA)
	rent := e.seedEntry(e.wsA, cat, 250000, 5)

	now := time.Date(2026, time.September, 20, 10, 0, 0, 0, e.loc)
	past := domain.MustPeriod(2026, time.June)
	current := domain.MustPeriod(2026, time.September)

	e.materialise(e.wsA, past, now, rent)
	e.materialise(e.wsA, current, now, rent)

	if got := e.list(e.wsA, past); !got[0].AmountEstimated {
		t.Fatal("a month reconstructed after the fact is stored as a confirmed amount")
	}
	if got := e.list(e.wsA, current); got[0].AmountEstimated {
		t.Fatal("the current month of a fixed obligation is stored as an estimate")
	}

	// Nothing is materialised before the definition began.
	beforeStart := domain.MustPeriod(2025, time.December)
	if created := e.materialise(e.wsA, beforeStart, now, rent); created != 0 {
		t.Fatalf("%d rows were created for a month before the definition started", created)
	}
	if got := len(e.list(e.wsA, beforeStart)); got != 0 {
		t.Fatalf("a month before the definition started holds %d occurrences", got)
	}
}

// The two write paths, through storage: settling a month, unsettling it,
// and confirming a varying amount.
func TestOccurrenceUpdateRoundTrips(t *testing.T) {
	e := newOccEnv(t)
	ctx := context.Background()
	cat := e.seedCategory(e.wsA)
	bill := e.seedEntry(e.wsA, cat, 42000, 20)
	bill.AmountVaries = true
	if err := e.repos.RecurringEntries.Update(ctx, bill); err != nil {
		t.Fatalf("update definition: %v", err)
	}

	p := domain.MustPeriod(2026, time.September)
	now := time.Date(2026, time.September, 10, 10, 0, 0, 0, e.loc)
	e.materialise(e.wsA, p, now, bill)

	o := e.list(e.wsA, p)[0]
	if !o.AmountEstimated {
		t.Fatal("a varying obligation was materialised as a confirmed amount")
	}

	// The bill arrives: the real figure replaces the estimate.
	if err := o.SetAmountCents(45817); err != nil {
		t.Fatalf("SetAmountCents: %v", err)
	}
	if err := e.repos.RecurringOccurrences.Update(ctx, &o); err != nil {
		t.Fatalf("Update: %v", err)
	}
	reloaded := e.list(e.wsA, p)[0]
	if reloaded.AmountCents != 45817 || reloaded.AmountEstimated {
		t.Fatalf("after confirming, the row holds %d cents estimated=%v",
			reloaded.AmountCents, reloaded.AmountEstimated)
	}

	// Settle it.
	paidAt := time.Date(2026, time.September, 19, 11, 0, 0, 0, time.UTC)
	if err := reloaded.MarkPaid(paidAt); err != nil {
		t.Fatalf("MarkPaid: %v", err)
	}
	if err := e.repos.RecurringOccurrences.Update(ctx, &reloaded); err != nil {
		t.Fatalf("Update: %v", err)
	}
	settled, err := e.repos.RecurringOccurrences.FindByEntryPeriod(ctx, e.wsA, bill.ID, p)
	if err != nil {
		t.Fatalf("FindByEntryPeriod: %v", err)
	}
	if settled.Status != domain.OccurrencePaid || settled.PaidAt == nil || !settled.PaidAt.Equal(paidAt) {
		t.Fatalf("the settled row reads back as %+v", settled)
	}

	// Unsettle it. The amount survives; only the paid state moves.
	settled.UnmarkPaid()
	if err := e.repos.RecurringOccurrences.Update(ctx, settled); err != nil {
		t.Fatalf("Update: %v", err)
	}
	final := e.list(e.wsA, p)[0]
	if final.Status != domain.OccurrencePending || final.PaidAt != nil {
		t.Fatal("unticking did not return the month to pending")
	}
	if final.AmountCents != 45817 || final.AmountEstimated {
		t.Fatalf("unticking disturbed the amount: %d cents estimated=%v",
			final.AmountCents, final.AmountEstimated)
	}
	if final.TransactionID != nil {
		t.Fatal("the row came back linked to a transaction")
	}
}

// Update cannot move a month or its frozen due date, however a caller
// mangles the struct it hands over.
func TestOccurrenceUpdateCannotMoveTheMonth(t *testing.T) {
	e := newOccEnv(t)
	ctx := context.Background()
	cat := e.seedCategory(e.wsA)
	rent := e.seedEntry(e.wsA, cat, 250000, 5)

	sep := domain.MustPeriod(2026, time.September)
	now := time.Date(2026, time.September, 20, 10, 0, 0, 0, e.loc)
	e.materialise(e.wsA, sep, now, rent)

	o := e.list(e.wsA, sep)[0]
	o.Period = domain.MustPeriod(2026, time.October)
	o.DueOn = domain.MustPeriod(2026, time.October).DueOn(28)
	o.AmountCents = 999
	if err := e.repos.RecurringOccurrences.Update(ctx, &o); err != nil {
		t.Fatalf("Update: %v", err)
	}

	// The amount, which IS mutable, moved. The identity did not.
	stillSeptember := e.list(e.wsA, sep)
	if len(stillSeptember) != 1 {
		t.Fatalf("September now holds %d occurrences", len(stillSeptember))
	}
	if stillSeptember[0].AmountCents != 999 {
		t.Fatal("the mutable field did not change")
	}
	if got := stillSeptember[0].DueOn.Format("2006-01-02"); got != "2026-09-05" {
		t.Fatalf("the frozen due date moved to %s", got)
	}
	if got := len(e.list(e.wsA, domain.MustPeriod(2026, time.October))); got != 0 {
		t.Fatalf("October acquired %d occurrences from an update", got)
	}
	// And the struct the caller holds was corrected from the row, so it
	// cannot go on believing it moved anything.
	if !o.Period.Equal(sep) {
		t.Fatalf("Update returned a struct still claiming period %s", o.Period)
	}
}

// An annual obligation materialises in its due month and in no other.
func TestAnnualEntryMaterialisesOnlyInItsDueMonth(t *testing.T) {
	e := newOccEnv(t)
	ctx := context.Background()
	cat := e.seedCategory(e.wsA)

	january := 1
	ipva := &domain.RecurringEntry{
		ID: uuid.New(), WorkspaceID: e.wsA, Description: "fixture annual",
		AmountCents: 120000, CategoryID: cat.ID, DueDay: 10,
		Recurrence: domain.RecurrenceAnnual, DueMonth: &january,
		Status:   domain.StatusActive,
		StartsAt: time.Date(2026, time.January, 1, 12, 0, 0, 0, e.loc),
	}
	if err := ipva.Validate(); err != nil {
		t.Fatalf("fixture does not validate: %v", err)
	}
	if err := e.repos.RecurringEntries.Create(ctx, ipva); err != nil {
		t.Fatalf("create annual entry: %v", err)
	}
	// The column round-trips, which is what makes the rest meaningful.
	reloaded, err := e.repos.RecurringEntries.FindByID(ctx, e.wsA, ipva.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.DueMonth == nil || *reloaded.DueMonth != 1 {
		t.Fatalf("due_month did not survive storage: %v", reloaded.DueMonth)
	}

	now := time.Date(2026, time.September, 20, 10, 0, 0, 0, e.loc)
	for m := time.January; m <= time.December; m++ {
		p := domain.MustPeriod(2026, m)
		e.materialise(e.wsA, p, now, reloaded)
		want := 0
		if m == time.January {
			want = 1
		}
		if got := len(e.list(e.wsA, p)); got != want {
			t.Fatalf("%s holds %d occurrences, want %d", p, got, want)
		}
	}

	// And the whole amount lands in that one month, not a twelfth of it.
	jan := e.list(e.wsA, domain.MustPeriod(2026, time.January))[0]
	if jan.AmountCents != 120000 {
		t.Fatalf("January holds %d cents, want the full 120000", jan.AmountCents)
	}
	if reloaded.MonthlyEquivalentCents() != 10000 {
		t.Fatal("the recurring-summary reading changed; that contract was supposed to be untouched")
	}
}

// A legacy annual row, exactly as the migration leaves one: readable,
// inert, and never guessed at.
func TestLegacyAnnualRowMaterialisesNothing(t *testing.T) {
	e := newOccEnv(t)
	ctx := context.Background()
	cat := e.seedCategory(e.wsA)

	// Written through SQL on purpose: this is a row the domain now refuses
	// to produce, and the point is that one already in the database stays
	// readable rather than breaking every read around it.
	id := uuid.New()
	// starts_at is pinned rather than left to default to now(), so the
	// months below are months the definition was actually applicable in.
	// Without it the row would materialise nothing for the trivial reason
	// that it had not started yet, and the test would pass without
	// exercising the thing it is about.
	startsAt := time.Date(2026, time.January, 1, 12, 0, 0, 0, e.loc)
	if _, err := e.pool.Exec(ctx,
		`INSERT INTO finance.recurring_entries
		   (id, workspace_id, description, amount_cents, category_id, due_day,
		    recurrence, status, starts_at)
		 VALUES ($1,$2,'fixture legacy annual',120000,$3,10,'annual','active',$4)`,
		id, e.wsA, cat.ID, startsAt); err != nil {
		t.Fatalf("insert legacy row: %v", err)
	}

	legacy, err := e.repos.RecurringEntries.FindByID(ctx, e.wsA, id)
	if err != nil {
		t.Fatalf("a legacy annual row is unreadable: %v", err)
	}
	if !legacy.NeedsDueMonth() {
		t.Fatal("the legacy row does not report that it needs a due month")
	}

	now := time.Date(2026, time.September, 20, 10, 0, 0, 0, e.loc)
	for m := time.January; m <= time.December; m++ {
		p := domain.MustPeriod(2026, m)
		if created := e.materialise(e.wsA, p, now, legacy); created != 0 {
			t.Fatalf("a legacy annual row materialised %d rows in %s", created, p)
		}
	}
	if n := e.rawCount(e.wsA); n != 0 {
		t.Fatalf("a due month was invented for a legacy row: %d occurrences exist", n)
	}

	// And it cannot be written back through the application layer until
	// somebody supplies a month.
	//
	// Asserted against the SERVICE and not the repository, deliberately:
	// the repositories in this module are dumb writers and the domain
	// rules are applied on the way in, so a repository that refused this
	// would be a second place where write rules live. The refusal the
	// operator meets is this one.
	newDesc := "fixture legacy annual, edited"
	if _, err := e.svc.UpdateRecurringEntry(ctx, app.UpdateRecurringEntryInput{
		WorkspaceID: e.wsA, ID: legacy.ID, Description: &newDesc,
	}); err == nil {
		t.Fatal("a legacy annual row was saved again with no due month")
	}

	// Supplying one is all it takes, and then it behaves like any other.
	january := 1
	fixed, err := e.svc.UpdateRecurringEntry(ctx, app.UpdateRecurringEntryInput{
		WorkspaceID: e.wsA, ID: legacy.ID, DueMonth: &january,
	})
	if err != nil {
		t.Fatalf("supplying a due month did not repair the row: %v", err)
	}
	if fixed.NeedsDueMonth() {
		t.Fatal("the row still reports that it needs a due month")
	}
	if created := e.materialise(e.wsA, domain.MustPeriod(2026, time.January), now, fixed); created != 1 {
		t.Fatalf("the repaired row materialised %d occurrences in January, want 1", created)
	}
}

// The application layer refuses to CREATE an annual entry with no due
// month, which is the half of the pairing rule the database cannot hold.
func TestCreatingAnAnnualEntryRequiresADueMonth(t *testing.T) {
	e := newOccEnv(t)
	ctx := context.Background()
	cat := e.seedCategory(e.wsA)
	annual := domain.RecurrenceAnnual

	if _, err := e.svc.CreateRecurringEntry(ctx, app.CreateRecurringEntryInput{
		WorkspaceID: e.wsA, Description: "fixture annual", AmountCents: 120000,
		CategoryID: cat.ID, DueDay: 10, Recurrence: &annual,
	}); err == nil {
		t.Fatal("an annual entry was created with no due month")
	}

	// And a monthly one is refused a due month, which the database also
	// holds as a CHECK. Both halves, from the one place a caller meets.
	monthly := domain.RecurrenceMonthly
	march := 3
	if _, err := e.svc.CreateRecurringEntry(ctx, app.CreateRecurringEntryInput{
		WorkspaceID: e.wsA, Description: "fixture monthly", AmountCents: 1000,
		CategoryID: cat.ID, DueDay: 10, Recurrence: &monthly, DueMonth: &march,
	}); err == nil {
		t.Fatal("a monthly entry was created with a due month")
	}

	// With a month, it goes through and round-trips.
	january := 1
	created, err := e.svc.CreateRecurringEntry(ctx, app.CreateRecurringEntryInput{
		WorkspaceID: e.wsA, Description: "fixture annual", AmountCents: 120000,
		CategoryID: cat.ID, DueDay: 10, Recurrence: &annual, DueMonth: &january,
		AmountVaries: true,
	})
	if err != nil {
		t.Fatalf("a well-formed annual entry was refused: %v", err)
	}
	reloaded, err := e.repos.RecurringEntries.FindByID(ctx, e.wsA, created.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.DueMonth == nil || *reloaded.DueMonth != 1 || !reloaded.AmountVaries {
		t.Fatalf("the new columns did not survive storage: due_month=%v amount_varies=%v",
			reloaded.DueMonth, reloaded.AmountVaries)
	}
}

// The due-day clamp survives storage, which is where an unclamped date
// would become a row filed under the wrong month.
func TestClampedDueDateRoundTripsThroughStorage(t *testing.T) {
	e := newOccEnv(t)
	cat := e.seedCategory(e.wsA)
	entry := e.seedEntry(e.wsA, cat, 50000, 31)
	now := time.Date(2026, time.September, 20, 10, 0, 0, 0, e.loc)

	cases := map[string]string{
		"2026-01": "2026-01-31",
		"2026-02": "2026-02-28",
		"2028-02": "2028-02-29",
		"2026-04": "2026-04-30",
		"2026-09": "2026-09-30",
	}
	for period, want := range cases {
		p, err := domain.ParsePeriod(period)
		if err != nil {
			t.Fatalf("ParsePeriod: %v", err)
		}
		e.materialise(e.wsA, p, now, entry)
		got := e.list(e.wsA, p)
		if len(got) != 1 {
			t.Fatalf("%s holds %d occurrences", period, len(got))
		}
		if s := got[0].DueOn.Format("2006-01-02"); s != want {
			t.Fatalf("%s stored a due date of %s, want %s", period, s, want)
		}
		if !got[0].Period.Equal(p) {
			t.Fatalf("%s read its period back as %s", period, got[0].Period)
		}
	}
}
