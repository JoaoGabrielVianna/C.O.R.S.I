package domain

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

// validEntry is a monthly recurring entry that passes Validate, so each
// test can break exactly one thing and know that is what it broke.
func validEntry() *RecurringEntry {
	return &RecurringEntry{
		ID:          uuid.New(),
		WorkspaceID: uuid.New(),
		Description: "assinatura",
		AmountCents: 12990,
		CategoryID:  uuid.New(),
		DueDay:      15,
		Recurrence:  RecurrenceMonthly,
		Status:      StatusActive,
		StartsAt:    time.Date(2026, time.January, 10, 12, 0, 0, 0, time.UTC),
	}
}

func intp(n int) *int { return &n }

// TestRecurringEntryDueMonthPairing is the invariant the database
// deliberately holds only half of.
//
// `monthly implies NULL` is a CHECK, because it is true of every row that
// existed before the column did. `annual implies NOT NULL` cannot be, for
// the same reason in reverse: a legacy annual row would violate it and the
// migration has no truthful month to fill in. So it lives here, and this is
// the test that says nothing new gets written without one.
func TestRecurringEntryDueMonthPairing(t *testing.T) {
	cases := []struct {
		name       string
		recurrence RecurringFrequency
		dueMonth   *int
		wantErr    bool
	}{
		{name: "monthly without a due month", recurrence: RecurrenceMonthly, dueMonth: nil},
		{name: "monthly WITH a due month is refused", recurrence: RecurrenceMonthly, dueMonth: intp(3), wantErr: true},

		{name: "annual in january", recurrence: RecurrenceAnnual, dueMonth: intp(1)},
		{name: "annual in december", recurrence: RecurrenceAnnual, dueMonth: intp(12)},
		{name: "annual without a due month is refused", recurrence: RecurrenceAnnual, dueMonth: nil, wantErr: true},
		{name: "annual in month zero is refused", recurrence: RecurrenceAnnual, dueMonth: intp(0), wantErr: true},
		{name: "annual in month thirteen is refused", recurrence: RecurrenceAnnual, dueMonth: intp(13), wantErr: true},
		{name: "annual in a negative month is refused", recurrence: RecurrenceAnnual, dueMonth: intp(-1), wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := validEntry()
			f.Recurrence = tc.recurrence
			f.DueMonth = tc.dueMonth
			err := f.Validate()
			if tc.wantErr && err == nil {
				t.Fatal("Validate accepted it")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("Validate refused it: %v", err)
			}
		})
	}
}

// A legacy annual row is readable, inert, and says what it needs.
//
// Inert is the load-bearing half: an annual obligation with no recorded due
// month contributes to NO month, because the alternative is this module
// picking one, and a guessed month is a bill that appears in a total the
// operator never agreed to.
func TestAnnualEntryWithoutDueMonthIsInert(t *testing.T) {
	f := validEntry()
	f.Recurrence = RecurrenceAnnual
	f.DueMonth = nil

	if !f.NeedsDueMonth() {
		t.Fatal("a legacy annual entry does not report that it needs a due month")
	}
	for m := time.January; m <= time.December; m++ {
		p := MustPeriod(2026, m)
		if f.OccursIn(p) {
			t.Fatalf("it claims to occur in %s", p)
		}
		if f.ActiveIn(p, time.UTC) {
			t.Fatalf("it claims to be active in %s", p)
		}
	}
	// And it cannot be written again in that state.
	if err := f.Validate(); err == nil {
		t.Fatal("Validate accepted an annual entry with no due month")
	}

	// A monthly entry never reports the condition.
	if validEntry().NeedsDueMonth() {
		t.Fatal("a monthly entry claims to need a due month")
	}
}

// An annual obligation belongs to exactly one month of the year.
func TestAnnualEntryOccursOnlyInItsDueMonth(t *testing.T) {
	f := validEntry()
	f.Recurrence = RecurrenceAnnual
	f.DueMonth = intp(int(time.January))

	for m := time.January; m <= time.December; m++ {
		p := MustPeriod(2026, m)
		want := m == time.January
		if got := f.OccursIn(p); got != want {
			t.Fatalf("%s: OccursIn = %v, want %v", p, got, want)
		}
	}
	// And in every year, not only the first.
	for _, y := range []int{2026, 2027, 2030} {
		if !f.OccursIn(MustPeriod(y, time.January)) {
			t.Fatalf("it does not occur in January %d", y)
		}
	}

	// A monthly entry occurs in all twelve.
	m := validEntry()
	for month := time.January; month <= time.December; month++ {
		if !m.OccursIn(MustPeriod(2026, month)) {
			t.Fatalf("a monthly entry does not occur in %s", month)
		}
	}
}

// TestRecurringEntryActiveIn is the materialization predicate.
//
// The boundaries are inclusive at BOTH ends: the month containing StartsAt
// owes an occurrence, and so does the month containing EndsAt. See the
// argument on ActiveIn.
func TestRecurringEntryActiveIn(t *testing.T) {
	loc := saoPaulo(t)
	date := func(y int, m time.Month, d, h int) time.Time {
		return time.Date(y, m, d, h, 0, 0, 0, loc)
	}
	tp := func(p *time.Time) *time.Time { return p }
	when := func(y int, m time.Month, d, h int) *time.Time {
		v := date(y, m, d, h)
		return tp(&v)
	}

	cases := []struct {
		name     string
		status   RecurringStatus
		startsAt time.Time
		endsAt   *time.Time
		period   string
		want     bool
	}{
		{
			name: "an ordinary month in the middle", status: StatusActive,
			startsAt: date(2026, time.January, 10, 12), period: "2026-06", want: true,
		},
		{
			name: "the month the definition began, created mid-month", status: StatusActive,
			// Written down on the 20th, due on the 15th: the obligation
			// existed, so the month is owed and shows up overdue.
			startsAt: date(2026, time.September, 20, 12), period: "2026-09", want: true,
		},
		{
			name: "the month before it began", status: StatusActive,
			startsAt: date(2026, time.September, 20, 12), period: "2026-08", want: false,
		},
		{
			name: "the month after it began", status: StatusActive,
			startsAt: date(2026, time.September, 20, 12), period: "2026-10", want: true,
		},
		{
			name: "the month it was cancelled in is still owed", status: StatusActive,
			startsAt: date(2026, time.January, 10, 12),
			endsAt:   when(2026, time.September, 15, 12), period: "2026-09", want: true,
		},
		{
			name: "the month after cancellation is not", status: StatusActive,
			startsAt: date(2026, time.January, 10, 12),
			endsAt:   when(2026, time.September, 15, 12), period: "2026-10", want: false,
		},
		{
			name: "a month before cancellation is still owed", status: StatusActive,
			startsAt: date(2026, time.January, 10, 12),
			endsAt:   when(2026, time.September, 15, 12), period: "2026-03", want: true,
		},
		{
			name: "cancelled on the first of the month", status: StatusActive,
			// Still inclusive. A rule that excluded this month would be a
			// second rule, and the asymmetry is what nobody remembers.
			startsAt: date(2026, time.January, 10, 12),
			endsAt:   when(2026, time.September, 1, 0), period: "2026-09", want: true,
		},
		{
			name: "started and cancelled inside one month", status: StatusActive,
			startsAt: date(2026, time.September, 3, 12),
			endsAt:   when(2026, time.September, 28, 12), period: "2026-09", want: true,
		},
		{
			name: "paused stops every month", status: StatusPaused,
			startsAt: date(2026, time.January, 10, 12), period: "2026-06", want: false,
		},
		{
			name: "the zone decides the starting month", status: StatusActive,
			// 21:00 on 31 August in São Paulo is already September in UTC.
			// Read in the reporting zone it is August, so August is owed.
			startsAt: date(2026, time.August, 31, 21), period: "2026-08", want: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := validEntry()
			f.Status = tc.status
			f.StartsAt = tc.startsAt
			f.EndsAt = tc.endsAt
			p, err := ParsePeriod(tc.period)
			if err != nil {
				t.Fatalf("ParsePeriod: %v", err)
			}
			if got := f.ActiveIn(p, loc); got != tc.want {
				t.Fatalf("ActiveIn(%s) = %v, want %v", tc.period, got, tc.want)
			}
		})
	}
}

// The zero period is never active, whatever the entry says.
func TestZeroPeriodIsNeverActive(t *testing.T) {
	if validEntry().ActiveIn(Period{}, time.UTC) {
		t.Fatal("an entry claims to be active in the zero period")
	}
}

// DueOnIn is the clamped day, and it is inside the month it was asked for.
func TestRecurringEntryDueOnIn(t *testing.T) {
	f := validEntry()
	f.DueDay = 31

	cases := map[string]string{
		"2026-01": "2026-01-31",
		"2026-02": "2026-02-28",
		"2028-02": "2028-02-29",
		"2026-04": "2026-04-30",
	}
	for period, want := range cases {
		p, err := ParsePeriod(period)
		if err != nil {
			t.Fatalf("ParsePeriod: %v", err)
		}
		if got := f.DueOnIn(p).Format("2006-01-02"); got != want {
			t.Fatalf("%s due day 31 landed on %s, want %s", period, got, want)
		}
	}
}

// The monthly-equivalent reading is untouched by any of this, and it is a
// DIFFERENT number from what a monthly commitment shows for the same entry.
func TestAnnualMonthlyEquivalentIsNotTheMonthlyCommitment(t *testing.T) {
	f := validEntry()
	f.Recurrence = RecurrenceAnnual
	f.DueMonth = intp(int(time.January))
	f.AmountCents = 120000 // R$ 1.200 a year

	if got := f.MonthlyEquivalentCents(); got != 10000 {
		t.Fatalf("the monthly equivalent is %d cents, want 10000", got)
	}
	// In January the commitment is the whole thing, and in the other
	// eleven months it is nothing. Neither figure is the other, and they
	// are never added.
	if !f.OccursIn(MustPeriod(2026, time.January)) {
		t.Fatal("the annual entry does not occur in its own due month")
	}
	if f.OccursIn(MustPeriod(2026, time.February)) {
		t.Fatal("the annual entry occurs in a month that is not its due month")
	}
}
