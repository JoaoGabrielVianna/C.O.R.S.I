//go:build integration

// Monthly commitment, through the application service.
//
// The sentence this file has to make convincing:
//
//	"O que falta pagar esse mês" has ONE answer, computed by the server
//	from rows the server wrote, and no edit of a recurrence can change
//	what a month that already happened says.
//
// Everything here goes through app.Service, because that is what both the
// HTTP handler and the Finance capabilities call. The layer below it is
// proved in recurring_occurrences_integration_test.go; this proves the
// orchestration, the refusals and the idempotency.
//
// No operator data is read, written or referenced. Every fixture is
// synthetic and lives in a database this suite creates and destroys.
package finance

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/finance/app"
	"github.com/corsi/backend/internal/finance/domain"
)

/* ── harness ─────────────────────────────────────────────────────────── */

// commitEnv reuses occEnv: same pool, same repositories, same service, same
// two workspaces. The helpers below are the ones that only make sense once
// there is an application service to drive.
type commitEnv struct{ *occEnv }

func newCommitEnv(t *testing.T) *commitEnv { return &commitEnv{newOccEnv(t)} }

// nowPeriod is the month the DATABASE thinks it is in, in the reporting
// zone. Every assertion about "the current month" is cut from this and
// never from the test process's clock, because the service is cut from the
// same reading and a test with its own clock would pass at 09:00 and fail
// at 22:00 on the last day of a month.
func (e *commitEnv) nowPeriod(t *testing.T) domain.Period {
	t.Helper()
	now, err := e.svc.Now(context.Background())
	if err != nil {
		t.Fatalf("clock: %v", err)
	}
	return domain.PeriodOf(now, e.loc)
}

func (e *commitEnv) month(t *testing.T, ws uuid.UUID, p domain.Period) app.MonthlyCommitmentView {
	t.Helper()
	v, err := e.svc.GetMonthlyCommitment(context.Background(), app.GetMonthlyCommitmentInput{
		WorkspaceID: ws, Period: p,
	})
	if err != nil {
		t.Fatalf("GetMonthlyCommitment(%s): %v", p, err)
	}
	return v
}

func (e *commitEnv) ref(ws uuid.UUID, entry *domain.RecurringEntry, p domain.Period) app.OccurrenceRef {
	return app.OccurrenceRef{WorkspaceID: ws, RecurringEntryID: entry.ID, Period: p}
}

// lineFor finds one obligation in a month's answer.
func lineFor(v app.MonthlyCommitmentView, entryID uuid.UUID) (app.MonthlyCommitmentLine, bool) {
	for _, l := range v.Lines {
		if l.RecurringEntryID == entryID {
			return l, true
		}
	}
	return app.MonthlyCommitmentLine{}, false
}

// seedEntryAt creates a definition that started in a given month, so a test
// can talk about the past without waiting for time to pass.
func (e *commitEnv) seedEntryAt(
	ws uuid.UUID, cat *domain.Category, cents int64, dueDay int, startsAt time.Time,
) *domain.RecurringEntry {
	e.t.Helper()
	f := e.seedEntry(ws, cat, cents, dueDay)
	f.StartsAt = startsAt
	if err := e.repos.RecurringEntries.Update(context.Background(), f); err != nil {
		e.t.Fatalf("backdate definition: %v", err)
	}
	return f
}

/* ── 1..3 · lazy materialization, idempotent, concurrent ─────────────── */

func TestCommitmentMaterialisesTheCurrentMonthLazily(t *testing.T) {
	e := newCommitEnv(t)
	cat := e.seedCategory(e.wsA)
	rent := e.seedEntry(e.wsA, cat, 250000, 5)
	gym := e.seedEntry(e.wsA, cat, 17000, 10)
	current := e.nowPeriod(t)

	if n := e.rawCount(e.wsA); n != 0 {
		t.Fatalf("the month was materialised before anybody read it: %d rows", n)
	}

	v := e.month(t, e.wsA, current)
	if len(v.Lines) != 2 {
		t.Fatalf("the month has %d lines, want 2", len(v.Lines))
	}
	if n := e.rawCount(e.wsA); n != 2 {
		t.Fatalf("reading the month wrote %d rows, want 2", n)
	}
	if v.Totals.Projection {
		t.Fatal("the current month came back as a projection")
	}
	if v.TimeZone != e.loc.String() {
		t.Fatalf("the month reports zone %q, want %q", v.TimeZone, e.loc.String())
	}
	if v.Today.IsZero() {
		t.Fatal("the month came back with no authoritative today")
	}

	// Recognition, not just ids: a person has to know WHICH bill this is.
	l, ok := lineFor(v, rent.ID)
	if !ok || l.Description != "fixture recurring" || l.CategoryName != cat.Name {
		t.Fatalf("the rent line carries no recognition: %+v", l)
	}
	if l.Direction != domain.EntryTypeExpense {
		t.Fatalf("direction is %q, want expense", l.Direction)
	}
	if _, ok := lineFor(v, gym.ID); !ok {
		t.Fatal("the gym is missing from the month")
	}
}

func TestCommitmentRepeatedReadIsIdempotent(t *testing.T) {
	e := newCommitEnv(t)
	cat := e.seedCategory(e.wsA)
	e.seedEntry(e.wsA, cat, 250000, 5)
	current := e.nowPeriod(t)

	first := e.month(t, e.wsA, current)
	for i := 0; i < 4; i++ {
		again := e.month(t, e.wsA, current)
		if len(again.Lines) != len(first.Lines) {
			t.Fatalf("read %d returned %d lines, first returned %d", i+2, len(again.Lines), len(first.Lines))
		}
		if again.Totals.CommittedCents != first.Totals.CommittedCents {
			t.Fatal("the committed total moved between two reads of the same month")
		}
		if again.Lines[0].Occurrence.ID != first.Lines[0].Occurrence.ID {
			t.Fatal("re-reading the month produced a different row")
		}
	}
	if n := e.rawCount(e.wsA); n != 1 {
		t.Fatalf("five reads of one month produced %d rows", n)
	}
}

// Six readers open the same month at the same instant, through the service.
// One row per obligation, and every caller sees the same month.
func TestCommitmentConcurrentReadsProduceOneMonth(t *testing.T) {
	e := newCommitEnv(t)
	cat := e.seedCategory(e.wsA)
	e.seedEntry(e.wsA, cat, 250000, 5)
	e.seedEntry(e.wsA, cat, 17000, 10)
	current := e.nowPeriod(t)

	const readers = 6
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		results []app.MonthlyCommitmentView
		failure error
		start   = make(chan struct{})
	)
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			v, err := e.svc.GetMonthlyCommitment(context.Background(), app.GetMonthlyCommitmentInput{
				WorkspaceID: e.wsA, Period: current,
			})
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failure = err
				return
			}
			results = append(results, v)
		}()
	}
	close(start)
	wg.Wait()

	if failure != nil {
		t.Fatalf("a concurrent read failed instead of yielding: %v", failure)
	}
	if n := e.rawCount(e.wsA); n != 2 {
		t.Fatalf("%d concurrent readers produced %d rows, want 2", readers, n)
	}
	for i, v := range results {
		if len(v.Lines) != 2 || v.Totals.CommittedCents != 267000 {
			t.Fatalf("reader %d saw %d lines totalling %d", i, len(v.Lines), v.Totals.CommittedCents)
		}
	}
}

/* ── 4..5 · past reconstruction, future projection ───────────────────── */

func TestCommitmentPastMonthIsReconstructedAsEstimated(t *testing.T) {
	e := newCommitEnv(t)
	cat := e.seedCategory(e.wsA)
	current := e.nowPeriod(t)
	past := current.Prev()
	rent := e.seedEntryAt(e.wsA, cat, 250000, 5, past.Prev().Start(e.loc))

	v := e.month(t, e.wsA, past)
	l, ok := lineFor(v, rent.ID)
	if !ok {
		t.Fatal("the past month has no rent")
	}
	if !l.Occurrence.AmountEstimated {
		t.Fatal("a month reconstructed after the fact is presented as confirmed history")
	}
	if v.Totals.EstimatedCents != 250000 || v.Totals.EstimatedCount != 1 {
		t.Fatalf("the totals do not report the reconstruction: %d cents across %d rows",
			v.Totals.EstimatedCents, v.Totals.EstimatedCount)
	}
	// It IS persisted: a past month is rows, unlike a future one.
	if n := e.rawCount(e.wsA); n != 1 {
		t.Fatalf("a past month persisted %d rows, want 1", n)
	}
}

func TestCommitmentFutureMonthWritesNothing(t *testing.T) {
	e := newCommitEnv(t)
	cat := e.seedCategory(e.wsA)
	rent := e.seedEntry(e.wsA, cat, 250000, 5)
	future := e.nowPeriod(t).Next()

	v := e.month(t, e.wsA, future)
	if !v.Totals.Projection {
		t.Fatal("a month that has not begun did not declare itself a projection")
	}
	if len(v.Lines) != 1 || v.Totals.CommittedCents != 250000 {
		t.Fatalf("the projection has %d lines totalling %d", len(v.Lines), v.Totals.CommittedCents)
	}
	if !v.Lines[0].Occurrence.AmountEstimated {
		t.Fatal("a projected amount is presented as confirmed")
	}
	// The one thing that must not happen.
	if n := e.rawCount(e.wsA); n != 0 {
		t.Fatalf("reading a future month wrote %d rows", n)
	}
	// And the projected rows carry NO id, so nothing can mistake one for a
	// row it could address.
	if v.Lines[0].Occurrence.ID != uuid.Nil {
		t.Fatalf("a projected occurrence carries an id (%s) that resolves to nothing",
			v.Lines[0].Occurrence.ID)
	}

	// Reading it repeatedly still writes nothing.
	e.month(t, e.wsA, future)
	e.month(t, e.wsA, future)
	if n := e.rawCount(e.wsA); n != 0 {
		t.Fatalf("three reads of a future month wrote %d rows", n)
	}
	_ = rent
}

/* ── 6..7 · annual placement, and the one nothing can place ──────────── */

func TestCommitmentAnnualLandsWholeInItsDueMonth(t *testing.T) {
	e := newCommitEnv(t)
	ctx := context.Background()
	cat := e.seedCategory(e.wsA)
	current := e.nowPeriod(t)

	// Due in the month AFTER the current one, so the two assertions below
	// are about different months whatever month the suite runs in.
	dueMonth := int(current.Next().Month())
	annual := domain.RecurrenceAnnual
	ipva, err := e.svc.CreateRecurringEntry(ctx, app.CreateRecurringEntryInput{
		WorkspaceID: e.wsA, Description: "fixture annual", AmountCents: 120000,
		CategoryID: cat.ID, DueDay: 10, Recurrence: &annual, DueMonth: &dueMonth,
	})
	if err != nil {
		t.Fatalf("create annual: %v", err)
	}
	// Backdated so the due month in a future year is not before it started.
	ipva.StartsAt = current.Prev().Prev().Start(e.loc)
	if err := e.repos.RecurringEntries.Update(ctx, ipva); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	if v := e.month(t, e.wsA, current); len(v.Lines) != 0 || v.Totals.CommittedCents != 0 {
		t.Fatalf("the annual entry landed in a month that is not its due month: %+v", v.Totals)
	}
	v := e.month(t, e.wsA, current.Next())
	if len(v.Lines) != 1 {
		t.Fatalf("the due month has %d lines, want 1", len(v.Lines))
	}
	// The WHOLE amount, not a twelfth. The twelfth is what
	// recurring_entry.summary reports, and the two are never added.
	if v.Totals.CommittedCents != 120000 {
		t.Fatalf("the due month commits %d cents, want the full 120000", v.Totals.CommittedCents)
	}
	summary, err := e.svc.GetRecurringSummary(ctx, e.wsA)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if summary.ExpenseMonthlyCents != 10000 {
		t.Fatalf("the recurring summary reports %d, want the untouched 10000",
			summary.ExpenseMonthlyCents)
	}
}

// A legacy annual row reports itself and takes nothing else down with it.
func TestCommitmentReportsUnplaceableWithoutFailing(t *testing.T) {
	e := newCommitEnv(t)
	ctx := context.Background()
	cat := e.seedCategory(e.wsA)
	rent := e.seedEntry(e.wsA, cat, 250000, 5)
	current := e.nowPeriod(t)

	legacyID := uuid.New()
	if _, err := e.pool.Exec(ctx,
		`INSERT INTO finance.recurring_entries
		   (id, workspace_id, description, amount_cents, category_id, due_day,
		    recurrence, status, starts_at)
		 VALUES ($1,$2,'fixture legacy annual',120000,$3,10,'annual','active',$4)`,
		legacyID, e.wsA, cat.ID, current.Prev().Start(e.loc)); err != nil {
		t.Fatalf("insert legacy row: %v", err)
	}

	// The month still answers. A single unplaceable definition taking the
	// whole request down would be far worse than a month that says it is
	// incomplete.
	v := e.month(t, e.wsA, current)
	if len(v.Lines) != 1 || v.Lines[0].RecurringEntryID != rent.ID {
		t.Fatalf("the month has %d lines; the legacy row was materialised somewhere", len(v.Lines))
	}
	if len(v.Unplaceable) != 1 {
		t.Fatalf("the month reports %d unplaceable definitions, want 1", len(v.Unplaceable))
	}
	u := v.Unplaceable[0]
	if u.RecurringEntryID != legacyID || u.Reason != app.MissingDueMonth {
		t.Fatalf("the unplaceable entry reads %+v", u)
	}
	if u.Description == "" {
		t.Fatal("the unplaceable entry carries nothing a person could recognise")
	}
	// It is in no month at all, not merely in a different one.
	for m := time.January; m <= time.December; m++ {
		p := domain.MustPeriod(current.Year(), m)
		if p.After(current) {
			continue
		}
		for _, l := range e.month(t, e.wsA, p).Lines {
			if l.RecurringEntryID == legacyID {
				t.Fatalf("a due month was invented for the legacy row: %s", p)
			}
		}
	}
}

/* ── 8..9 · totals and overdue ───────────────────────────────────────── */

func TestCommitmentTotalsAreExact(t *testing.T) {
	e := newCommitEnv(t)
	ctx := context.Background()
	cat := e.seedCategory(e.wsA)
	current := e.nowPeriod(t)

	rent := e.seedEntry(e.wsA, cat, 250000, 1)
	internet := e.seedEntry(e.wsA, cat, 13000, 15)
	power := e.seedEntry(e.wsA, cat, 42000, 28)
	gym := e.seedEntry(e.wsA, cat, 17000, 1)

	power.AmountVaries = true
	if err := e.repos.RecurringEntries.Update(ctx, power); err != nil {
		t.Fatalf("mark varying: %v", err)
	}

	e.month(t, e.wsA, current) // materialise
	for _, f := range []*domain.RecurringEntry{rent, internet} {
		if _, err := e.svc.MarkOccurrencePaid(ctx, e.ref(e.wsA, f, current)); err != nil {
			t.Fatalf("mark paid: %v", err)
		}
	}

	v := e.month(t, e.wsA, current)
	tot := v.Totals
	if tot.CommittedCents != 322000 {
		t.Fatalf("committed = %d, want 322000", tot.CommittedCents)
	}
	if tot.PaidCents != 263000 {
		t.Fatalf("paid = %d, want 263000", tot.PaidCents)
	}
	if tot.RemainingCents != 59000 || tot.RemainingCents != tot.CommittedCents-tot.PaidCents {
		t.Fatalf("remaining = %d, want 59000", tot.RemainingCents)
	}
	if tot.EstimatedCents != 42000 || tot.EstimatedCount != 1 {
		t.Fatalf("estimated = %d cents across %d rows, want 42000 across 1",
			tot.EstimatedCents, tot.EstimatedCount)
	}
	if tot.Count != 4 || tot.PaidCount != 2 || tot.PendingCount != 2 {
		t.Fatalf("counts: %d total, %d paid, %d pending", tot.Count, tot.PaidCount, tot.PendingCount)
	}
	// The totals describe exactly the lines returned, never a wider set.
	var summed int64
	for _, l := range v.Lines {
		summed += l.Occurrence.AmountCents
	}
	if summed != tot.CommittedCents {
		t.Fatalf("the lines add to %d but the total says %d", summed, tot.CommittedCents)
	}
	_ = gym
}

func TestCommitmentOverdueIsDerivedFromAuthoritativeToday(t *testing.T) {
	e := newCommitEnv(t)
	ctx := context.Background()
	cat := e.seedCategory(e.wsA)
	current := e.nowPeriod(t)

	now, err := e.svc.Now(ctx)
	if err != nil {
		t.Fatalf("clock: %v", err)
	}
	today := now.In(e.loc).Day()
	if today == 1 || today == current.LastDay() {
		t.Skip("today sits on a month boundary; there is no day both before and after it")
	}

	overdue := e.seedEntry(e.wsA, cat, 17000, today-1)
	upcoming := e.seedEntry(e.wsA, cat, 13000, today+1)
	onToday := e.seedEntry(e.wsA, cat, 5000, today)

	v := e.month(t, e.wsA, current)
	byEntry := map[uuid.UUID]app.MonthlyCommitmentLine{}
	for _, l := range v.Lines {
		byEntry[l.RecurringEntryID] = l
	}
	if !byEntry[overdue.ID].Overdue {
		t.Fatal("a pending bill whose due date has passed is not overdue")
	}
	if byEntry[upcoming.ID].Overdue {
		t.Fatal("a bill due later this month is already overdue")
	}
	// Due TODAY is not late. A person with the day to pay has not missed it.
	if byEntry[onToday.ID].Overdue {
		t.Fatal("a bill due today is reported overdue")
	}
	if v.Totals.OverdueCount != 1 {
		t.Fatalf("overdue count is %d, want 1", v.Totals.OverdueCount)
	}

	// Settling it stops it being late, without anything being written about
	// lateness.
	if _, err := e.svc.MarkOccurrencePaid(ctx, e.ref(e.wsA, overdue, current)); err != nil {
		t.Fatalf("mark paid: %v", err)
	}
	if got := e.month(t, e.wsA, current).Totals.OverdueCount; got != 0 {
		t.Fatalf("overdue count is %d after settling the late bill", got)
	}
}

/* ── 10 · workspace isolation ────────────────────────────────────────── */

func TestCommitmentWorkspaceIsolation(t *testing.T) {
	e := newCommitEnv(t)
	ctx := context.Background()
	catA := e.seedCategory(e.wsA)
	catB := e.seedCategory(e.wsB)
	entryA := e.seedEntry(e.wsA, catA, 250000, 5)
	e.seedEntry(e.wsB, catB, 9900, 20)
	current := e.nowPeriod(t)

	a := e.month(t, e.wsA, current)
	b := e.month(t, e.wsB, current)
	if a.Totals.CommittedCents != 250000 || b.Totals.CommittedCents != 9900 {
		t.Fatalf("A commits %d and B commits %d", a.Totals.CommittedCents, b.Totals.CommittedCents)
	}
	if len(a.Lines) != 1 || len(b.Lines) != 1 {
		t.Fatal("a workspace sees the other's obligations")
	}

	// B cannot settle A's month, even naming A's entry exactly.
	if _, err := e.svc.MarkOccurrencePaid(ctx, app.OccurrenceRef{
		WorkspaceID: e.wsB, RecurringEntryID: entryA.ID, Period: current,
	}); err == nil {
		t.Fatal("workspace B marked workspace A's obligation paid")
	}
	if got := e.month(t, e.wsA, current); got.Totals.PaidCents != 0 {
		t.Fatal("A's month was settled by B")
	}
	// And B cannot read it by naming it either.
	if _, err := e.svc.SetOccurrenceAmount(ctx, app.SetOccurrenceAmountInput{
		OccurrenceRef: app.OccurrenceRef{
			WorkspaceID: e.wsB, RecurringEntryID: entryA.ID, Period: current,
		},
		AmountCents: 1,
	}); err == nil {
		t.Fatal("workspace B set the amount of workspace A's obligation")
	}
}

/* ── 11..16 · the write verbs ────────────────────────────────────────── */

// TestMarkPaidIsIdempotentAndNeverToggles is the retry-safety claim.
//
// A re-sent HTTP request, a re-issued tool call after a timeout, a
// double-tap on a phone: with toggle semantics the second one UNDOES the
// first and nobody can tell. Here the second is a no-op that succeeds, and
// the recorded payment date does not move.
func TestMarkPaidIsIdempotentAndNeverToggles(t *testing.T) {
	e := newCommitEnv(t)
	ctx := context.Background()
	cat := e.seedCategory(e.wsA)
	rent := e.seedEntry(e.wsA, cat, 250000, 5)
	current := e.nowPeriod(t)
	e.month(t, e.wsA, current)

	first, err := e.svc.MarkOccurrencePaid(ctx, e.ref(e.wsA, rent, current))
	if err != nil {
		t.Fatalf("mark paid: %v", err)
	}
	if first.Status != domain.OccurrencePaid || first.PaidAt == nil {
		t.Fatalf("the first mark left it %+v", first)
	}
	// The stamp comes from Postgres, not from this process: it has to sit
	// inside the window the database itself reports.
	before, err := e.svc.Now(ctx)
	if err != nil {
		t.Fatalf("clock: %v", err)
	}
	if first.PaidAt.After(before) {
		t.Fatalf("paid_at %s is in the database's future (%s)", first.PaidAt, before)
	}

	stamp := *first.PaidAt
	for i := 0; i < 3; i++ {
		again, err := e.svc.MarkOccurrencePaid(ctx, e.ref(e.wsA, rent, current))
		if err != nil {
			t.Fatalf("repeat %d failed: %v", i+1, err)
		}
		if again.Status != domain.OccurrencePaid {
			t.Fatalf("repeat %d toggled it back to %q", i+1, again.Status)
		}
		if !again.PaidAt.Equal(stamp) {
			t.Fatalf("repeat %d moved the recorded payment date to %s", i+1, again.PaidAt)
		}
	}
	if got := e.month(t, e.wsA, current).Totals; got.PaidCount != 1 || got.PaidCents != 250000 {
		t.Fatalf("after four marks the month says %d paid rows totalling %d",
			got.PaidCount, got.PaidCents)
	}
}

func TestMarkPendingIsIdempotentAndKeepsTheAmount(t *testing.T) {
	e := newCommitEnv(t)
	ctx := context.Background()
	cat := e.seedCategory(e.wsA)
	power := e.seedEntry(e.wsA, cat, 42000, 20)
	power.AmountVaries = true
	if err := e.repos.RecurringEntries.Update(ctx, power); err != nil {
		t.Fatalf("mark varying: %v", err)
	}
	current := e.nowPeriod(t)
	e.month(t, e.wsA, current)
	ref := e.ref(e.wsA, power, current)

	if _, err := e.svc.SetOccurrenceAmount(ctx, app.SetOccurrenceAmountInput{
		OccurrenceRef: ref, AmountCents: 43720,
	}); err != nil {
		t.Fatalf("set amount: %v", err)
	}
	if _, err := e.svc.MarkOccurrencePaid(ctx, ref); err != nil {
		t.Fatalf("mark paid: %v", err)
	}

	undone, err := e.svc.UnmarkOccurrencePaid(ctx, ref)
	if err != nil {
		t.Fatalf("mark pending: %v", err)
	}
	if undone.Status != domain.OccurrencePending || undone.PaidAt != nil {
		t.Fatalf("mark pending left it %+v", undone)
	}
	// The confirmed figure survives. Being wrong about whether it was paid
	// says nothing about whether it was the right number.
	if undone.AmountCents != 43720 || undone.AmountEstimated {
		t.Fatalf("mark pending disturbed the amount: %d estimated=%v",
			undone.AmountCents, undone.AmountEstimated)
	}
	if undone.TransactionID != nil {
		t.Fatal("mark pending left a transaction linked")
	}

	for i := 0; i < 3; i++ {
		again, err := e.svc.UnmarkOccurrencePaid(ctx, ref)
		if err != nil {
			t.Fatalf("repeat %d failed: %v", i+1, err)
		}
		if again.Status != domain.OccurrencePending {
			t.Fatalf("repeat %d toggled it to %q", i+1, again.Status)
		}
		if again.AmountCents != 43720 {
			t.Fatalf("repeat %d changed the amount", i+1)
		}
	}
}

// A projection has nothing to write to, and says so rather than 404-ing.
func TestFutureMonthCannotBeWritten(t *testing.T) {
	e := newCommitEnv(t)
	ctx := context.Background()
	cat := e.seedCategory(e.wsA)
	rent := e.seedEntry(e.wsA, cat, 250000, 5)
	future := e.nowPeriod(t).Next()
	e.month(t, e.wsA, future) // reading it first must not change the answer

	ref := e.ref(e.wsA, rent, future)
	if _, err := e.svc.MarkOccurrencePaid(ctx, ref); err == nil {
		t.Fatal("a month that has not begun was marked paid")
	}
	if _, err := e.svc.UnmarkOccurrencePaid(ctx, ref); err == nil {
		t.Fatal("a month that has not begun was marked pending")
	}
	if _, err := e.svc.SetOccurrenceAmount(ctx, app.SetOccurrenceAmountInput{
		OccurrenceRef: ref, AmountCents: 1000,
	}); err == nil {
		t.Fatal("a month that has not begun had its amount set")
	}
	if n := e.rawCount(e.wsA); n != 0 {
		t.Fatalf("a refused write against a projection wrote %d rows", n)
	}
}

// A month nobody has opened has no row, and the refusal says so rather
// than inventing one.
func TestWritingAnUnreadMonthIsNotFound(t *testing.T) {
	e := newCommitEnv(t)
	ctx := context.Background()
	cat := e.seedCategory(e.wsA)
	rent := e.seedEntry(e.wsA, cat, 250000, 5)
	current := e.nowPeriod(t)

	if _, err := e.svc.MarkOccurrencePaid(ctx, e.ref(e.wsA, rent, current)); err == nil {
		t.Fatal("a month that was never read was marked paid")
	}
	if n := e.rawCount(e.wsA); n != 0 {
		t.Fatalf("a refused write materialised %d rows", n)
	}
}

/* ── 17..18 · variable amount ────────────────────────────────────────── */

func TestSetAmountClearsTheEstimateAndTouchesNothingElse(t *testing.T) {
	e := newCommitEnv(t)
	ctx := context.Background()
	cat := e.seedCategory(e.wsA)
	power := e.seedEntry(e.wsA, cat, 42000, 20)
	power.AmountVaries = true
	if err := e.repos.RecurringEntries.Update(ctx, power); err != nil {
		t.Fatalf("mark varying: %v", err)
	}
	current := e.nowPeriod(t)

	before := e.month(t, e.wsA, current)
	if !before.Lines[0].Occurrence.AmountEstimated {
		t.Fatal("a varying obligation was materialised as confirmed")
	}
	dueOn := before.Lines[0].Occurrence.DueOn

	got, err := e.svc.SetOccurrenceAmount(ctx, app.SetOccurrenceAmountInput{
		OccurrenceRef: e.ref(e.wsA, power, current), AmountCents: 43720,
	})
	if err != nil {
		t.Fatalf("set amount: %v", err)
	}
	if got.AmountCents != 43720 || got.AmountEstimated {
		t.Fatalf("the month holds %d cents estimated=%v", got.AmountCents, got.AmountEstimated)
	}
	if got.Status != domain.OccurrencePending {
		t.Fatal("confirming an amount also marked the bill paid")
	}
	if !got.DueOn.Equal(dueOn) {
		t.Fatal("confirming an amount moved the due date")
	}

	// The DEFINITION's default is untouched: "a luz veio 437,20" is a fact
	// about one bill, not a change to what the recurrence usually costs.
	reloaded, err := e.repos.RecurringEntries.FindByID(ctx, e.wsA, power.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.AmountCents != 42000 {
		t.Fatalf("the recurrence's default became %d cents", reloaded.AmountCents)
	}

	for _, bad := range []int64{0, -1} {
		if _, err := e.svc.SetOccurrenceAmount(ctx, app.SetOccurrenceAmountInput{
			OccurrenceRef: e.ref(e.wsA, power, current), AmountCents: bad,
		}); err == nil {
			t.Fatalf("SetOccurrenceAmount accepted %d", bad)
		}
	}
}

// A settled month is not rewritten in place.
//
// Two facts are already on record — an amount and a settlement — and moving
// one of them silently would leave the row self-contradictory. The rule is
// the one that cannot be wrong without somebody noticing: unmark, set,
// mark again.
func TestPaidOccurrenceAmountCannotBeSilentlyEdited(t *testing.T) {
	e := newCommitEnv(t)
	ctx := context.Background()
	cat := e.seedCategory(e.wsA)
	power := e.seedEntry(e.wsA, cat, 42000, 20)
	current := e.nowPeriod(t)
	e.month(t, e.wsA, current)
	ref := e.ref(e.wsA, power, current)

	if _, err := e.svc.MarkOccurrencePaid(ctx, ref); err != nil {
		t.Fatalf("mark paid: %v", err)
	}
	if _, err := e.svc.SetOccurrenceAmount(ctx, app.SetOccurrenceAmountInput{
		OccurrenceRef: ref, AmountCents: 43720,
	}); err == nil {
		t.Fatal("a paid month had its amount rewritten in place")
	}
	if got := e.month(t, e.wsA, current); got.Totals.PaidCents != 42000 {
		t.Fatalf("the refused edit changed the month anyway: %d", got.Totals.PaidCents)
	}

	// The documented way round: three deliberate acts, each undoable.
	if _, err := e.svc.UnmarkOccurrencePaid(ctx, ref); err != nil {
		t.Fatalf("mark pending: %v", err)
	}
	if _, err := e.svc.SetOccurrenceAmount(ctx, app.SetOccurrenceAmountInput{
		OccurrenceRef: ref, AmountCents: 43720,
	}); err != nil {
		t.Fatalf("set amount after unmarking: %v", err)
	}
	if _, err := e.svc.MarkOccurrencePaid(ctx, ref); err != nil {
		t.Fatalf("mark paid again: %v", err)
	}
	if got := e.month(t, e.wsA, current); got.Totals.PaidCents != 43720 {
		t.Fatalf("after the round trip the month says %d", got.Totals.PaidCents)
	}
}

/* ── 19..23 · editing a definition ───────────────────────────────────── */

func TestDefinitionEditLeavesTheMonthAloneByDefault(t *testing.T) {
	e := newCommitEnv(t)
	ctx := context.Background()
	cat := e.seedCategory(e.wsA)
	rent := e.seedEntry(e.wsA, cat, 250000, 5)
	current := e.nowPeriod(t)
	e.month(t, e.wsA, current)

	raised := int64(300000)
	newDay := 12
	if _, err := e.svc.UpdateRecurringEntry(ctx, app.UpdateRecurringEntryInput{
		WorkspaceID: e.wsA, ID: rent.ID, AmountCents: &raised, DueDay: &newDay,
	}); err != nil {
		t.Fatalf("update definition: %v", err)
	}

	v := e.month(t, e.wsA, current)
	if v.Totals.CommittedCents != 250000 {
		t.Fatalf("the current month became %d cents without being asked", v.Totals.CommittedCents)
	}
	if got := v.Lines[0].Occurrence.DueOn.Day(); got != 5 {
		t.Fatalf("the due date moved to the %dth without being asked", got)
	}
}

func TestApplyToPeriodTouchesOnlyTheCurrentPendingMonth(t *testing.T) {
	e := newCommitEnv(t)
	ctx := context.Background()
	cat := e.seedCategory(e.wsA)
	current := e.nowPeriod(t)
	past := current.Prev()
	rent := e.seedEntryAt(e.wsA, cat, 250000, 5, past.Prev().Start(e.loc))

	e.month(t, e.wsA, past)
	e.month(t, e.wsA, current)

	raised := int64(300000)
	newDay := 12
	if _, err := e.svc.UpdateRecurringEntry(ctx, app.UpdateRecurringEntryInput{
		WorkspaceID: e.wsA, ID: rent.ID, AmountCents: &raised, DueDay: &newDay,
		ApplyToPeriod: &current,
	}); err != nil {
		t.Fatalf("update with apply_to_period: %v", err)
	}

	now := e.month(t, e.wsA, current)
	if now.Totals.CommittedCents != 300000 {
		t.Fatalf("the current month commits %d, want the new 300000", now.Totals.CommittedCents)
	}
	if got := now.Lines[0].Occurrence.DueOn.Day(); got != 12 {
		t.Fatalf("the current month's due day is %d, want 12", got)
	}
	// The new default is still a DEFAULT, not a bill that arrived.
	if now.Lines[0].Occurrence.AmountEstimated {
		t.Fatal("a fixed obligation's new default was marked an estimate")
	}

	// The past month did not move. This is the whole point.
	then := e.month(t, e.wsA, past)
	if then.Totals.CommittedCents != 250000 {
		t.Fatalf("the past month became %d cents", then.Totals.CommittedCents)
	}
	if got := then.Lines[0].Occurrence.DueOn.Day(); got != 5 {
		t.Fatalf("the past month's due day moved to %d", got)
	}
}

func TestApplyToPeriodRefusesPastFutureAndPaid(t *testing.T) {
	e := newCommitEnv(t)
	ctx := context.Background()
	cat := e.seedCategory(e.wsA)
	current := e.nowPeriod(t)
	past := current.Prev()
	future := current.Next()
	rent := e.seedEntryAt(e.wsA, cat, 250000, 5, past.Prev().Start(e.loc))
	e.month(t, e.wsA, past)
	e.month(t, e.wsA, current)

	raised := int64(300000)
	edit := func(p domain.Period) error {
		_, err := e.svc.UpdateRecurringEntry(ctx, app.UpdateRecurringEntryInput{
			WorkspaceID: e.wsA, ID: rent.ID, AmountCents: &raised, ApplyToPeriod: &p,
		})
		return err
	}

	if err := edit(past); err == nil {
		t.Fatal("apply_to_period rewrote a past month")
	}
	if err := edit(future); err == nil {
		t.Fatal("apply_to_period touched a month that has not begun")
	}
	// A refused apply must take the DEFINITION change with it: half of the
	// edit landing is worse than neither half.
	reloaded, err := e.repos.RecurringEntries.FindByID(ctx, e.wsA, rent.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.AmountCents != 250000 {
		t.Fatalf("a refused apply_to_period still changed the definition to %d", reloaded.AmountCents)
	}

	// And a settled month is refused too.
	if _, err := e.svc.MarkOccurrencePaid(ctx, e.ref(e.wsA, rent, current)); err != nil {
		t.Fatalf("mark paid: %v", err)
	}
	if err := edit(current); err == nil {
		t.Fatal("apply_to_period rewrote a month that was already paid")
	}
	if got := e.month(t, e.wsA, current); got.Totals.PaidCents != 250000 {
		t.Fatalf("the refused apply changed the paid month to %d", got.Totals.PaidCents)
	}
}

// Moving an annual obligation's due month does not move the months it
// already has. Structural rather than a rule: apply_to_period resolves one
// row and never inserts or deletes.
func TestAnnualDueMonthChangeDoesNotMoveHistory(t *testing.T) {
	e := newCommitEnv(t)
	ctx := context.Background()
	cat := e.seedCategory(e.wsA)
	current := e.nowPeriod(t)

	// Due last month, so there is a settled history to try to move.
	past := current.Prev()
	dueMonth := int(past.Month())
	annual := domain.RecurrenceAnnual
	ipva, err := e.svc.CreateRecurringEntry(ctx, app.CreateRecurringEntryInput{
		WorkspaceID: e.wsA, Description: "fixture annual", AmountCents: 120000,
		CategoryID: cat.ID, DueDay: 10, Recurrence: &annual, DueMonth: &dueMonth,
	})
	if err != nil {
		t.Fatalf("create annual: %v", err)
	}
	ipva.StartsAt = past.Prev().Start(e.loc)
	if err := e.repos.RecurringEntries.Update(ctx, ipva); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	if v := e.month(t, e.wsA, past); len(v.Lines) != 1 {
		t.Fatalf("the due month has %d lines, want 1", len(v.Lines))
	}
	if _, err := e.svc.MarkOccurrencePaid(ctx, e.ref(e.wsA, ipva, past)); err != nil {
		t.Fatalf("mark paid: %v", err)
	}

	// Move the due month forward.
	moved := int(current.Next().Month())
	if _, err := e.svc.UpdateRecurringEntry(ctx, app.UpdateRecurringEntryInput{
		WorkspaceID: e.wsA, ID: ipva.ID, DueMonth: &moved,
	}); err != nil {
		t.Fatalf("move due month: %v", err)
	}

	// The settled month keeps its row, its amount and its paid state.
	then := e.month(t, e.wsA, past)
	if len(then.Lines) != 1 || then.Totals.PaidCents != 120000 {
		t.Fatalf("the historical due month now reads %+v", then.Totals)
	}
	// And the new due month acquires one the ordinary way.
	ahead := e.month(t, e.wsA, current.Next())
	if len(ahead.Lines) != 1 || !ahead.Totals.Projection {
		t.Fatalf("the new due month reads %+v", ahead.Totals)
	}
}
