package domain

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func validOccurrence() *RecurringOccurrence {
	p := MustPeriod(2026, time.September)
	return &RecurringOccurrence{
		ID:               uuid.New(),
		WorkspaceID:      uuid.New(),
		RecurringEntryID: uuid.New(),
		Period:           p,
		DueOn:            p.DueOn(15),
		AmountCents:      12990,
		Status:           OccurrencePending,
		Origin:           OriginMaterialized,
	}
}

func TestRecurringOccurrenceValidate(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*RecurringOccurrence)
		wantErr bool
	}{
		{name: "a pending occurrence", mutate: func(*RecurringOccurrence) {}},
		{
			name: "a paid occurrence with its moment",
			mutate: func(o *RecurringOccurrence) {
				at := time.Date(2026, time.September, 14, 10, 0, 0, 0, time.UTC)
				o.Status, o.PaidAt = OccurrencePaid, &at
			},
		},
		{
			name:    "no workspace",
			mutate:  func(o *RecurringOccurrence) { o.WorkspaceID = uuid.Nil },
			wantErr: true,
		},
		{
			name:    "no definition",
			mutate:  func(o *RecurringOccurrence) { o.RecurringEntryID = uuid.Nil },
			wantErr: true,
		},
		{
			name:    "no period",
			mutate:  func(o *RecurringOccurrence) { o.Period = Period{} },
			wantErr: true,
		},
		{
			name: "a due date in the following month",
			// The exact shape time.Date's normalisation produces from
			// "31 February", and the reason Period.DueOn clamps instead.
			mutate:  func(o *RecurringOccurrence) { o.DueOn = time.Date(2026, time.October, 3, 0, 0, 0, 0, time.UTC) },
			wantErr: true,
		},
		{
			name:    "a due date in the previous month",
			mutate:  func(o *RecurringOccurrence) { o.DueOn = time.Date(2026, time.August, 31, 0, 0, 0, 0, time.UTC) },
			wantErr: true,
		},
		{
			name:    "a zero amount",
			mutate:  func(o *RecurringOccurrence) { o.AmountCents = 0 },
			wantErr: true,
		},
		{
			name: "a negative amount",
			// Direction comes from the definition's category. A sign here
			// would be a second way to say it, free to disagree.
			mutate:  func(o *RecurringOccurrence) { o.AmountCents = -12990 },
			wantErr: true,
		},
		{
			name:    "an unknown status",
			mutate:  func(o *RecurringOccurrence) { o.Status = OccurrenceStatus("overdue") },
			wantErr: true,
		},
		{
			name:    "an unknown origin",
			mutate:  func(o *RecurringOccurrence) { o.Origin = OccurrenceOrigin("imported") },
			wantErr: true,
		},
		{
			name:    "paid with no moment of payment",
			mutate:  func(o *RecurringOccurrence) { o.Status = OccurrencePaid },
			wantErr: true,
		},
		{
			name: "pending with a moment of payment",
			mutate: func(o *RecurringOccurrence) {
				at := time.Now()
				o.PaidAt = &at
			},
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := validOccurrence()
			tc.mutate(o)
			err := o.Validate()
			if tc.wantErr && err == nil {
				t.Fatal("Validate accepted it")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("Validate refused it: %v", err)
			}
		})
	}
}

// TestNewOccurrenceEstimatedFlag is where fixed, variable, reconstructed
// and projected amounts are told apart.
//
// Only one combination produces a CONFIRMED figure: a definition whose
// amount does not vary, in the month that is actually happening. Everything
// else is a guess of some kind and is marked as one.
func TestNewOccurrenceEstimatedFlag(t *testing.T) {
	loc := saoPaulo(t)
	now := time.Date(2026, time.September, 20, 10, 0, 0, 0, loc)

	cases := []struct {
		name    string
		varies  bool
		period  string
		want    bool
		because string
	}{
		{name: "fixed, current month", varies: false, period: "2026-09", want: false,
			because: "the definition's amount is what the operator expects to pay this month"},
		{name: "variable, current month", varies: true, period: "2026-09", want: true,
			because: "the real figure has not arrived yet"},
		{name: "fixed, past month", varies: false, period: "2026-08", want: true,
			because: "reconstructed from today's definition, not recorded in August"},
		{name: "variable, past month", varies: true, period: "2026-08", want: true,
			because: "reconstructed AND varying"},
		{name: "fixed, far past month", varies: false, period: "2026-02", want: true,
			because: "reconstruction does not get more reliable with distance"},
		{name: "fixed, future month", varies: false, period: "2026-10", want: true,
			because: "a month that has not begun is a projection"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := validEntry()
			f.AmountVaries = tc.varies
			f.StartsAt = time.Date(2026, time.January, 1, 12, 0, 0, 0, loc)
			p, err := ParsePeriod(tc.period)
			if err != nil {
				t.Fatalf("ParsePeriod: %v", err)
			}
			o, err := NewOccurrence(f, p, loc, now)
			if err != nil {
				t.Fatalf("NewOccurrence: %v", err)
			}
			if o.AmountEstimated != tc.want {
				t.Fatalf("AmountEstimated = %v, want %v: %s", o.AmountEstimated, tc.want, tc.because)
			}
		})
	}
}

// A newly materialised occurrence copies the definition and owes nothing
// to it afterwards.
func TestNewOccurrenceFreezesTheDefinition(t *testing.T) {
	loc := saoPaulo(t)
	now := time.Date(2026, time.September, 20, 10, 0, 0, 0, loc)

	f := validEntry()
	f.AmountCents = 250000
	f.DueDay = 31
	p := MustPeriod(2026, time.September)

	o, err := NewOccurrence(f, p, loc, now)
	if err != nil {
		t.Fatalf("NewOccurrence: %v", err)
	}
	if o.AmountCents != 250000 {
		t.Fatalf("the occurrence holds %d cents, want 250000", o.AmountCents)
	}
	if got := o.DueOn.Format("2006-01-02"); got != "2026-09-30" {
		t.Fatalf("due day 31 in September landed on %s, want 2026-09-30", got)
	}
	if o.Status != OccurrencePending || o.PaidAt != nil {
		t.Fatal("a fresh occurrence is not pending")
	}
	if o.Origin != OriginMaterialized {
		t.Fatalf("origin is %q", o.Origin)
	}
	if o.TransactionID != nil {
		t.Fatal("a fresh occurrence is linked to a transaction")
	}
	if o.WorkspaceID != f.WorkspaceID || o.RecurringEntryID != f.ID {
		t.Fatal("the occurrence does not belong to its definition and workspace")
	}

	// Moving the definition afterwards leaves the row exactly as it was.
	// Nothing recomputes it, and this is the whole of historical truth.
	f.AmountCents = 300000
	f.DueDay = 5
	if o.AmountCents != 250000 {
		t.Fatal("changing the definition's amount rewrote an existing occurrence")
	}
	if got := o.DueOn.Format("2006-01-02"); got != "2026-09-30" {
		t.Fatal("changing the definition's due day rewrote an existing occurrence")
	}
}

func TestNewOccurrenceRefusesNonsense(t *testing.T) {
	loc := saoPaulo(t)
	now := time.Now()
	if _, err := NewOccurrence(nil, MustPeriod(2026, time.September), loc, now); err == nil {
		t.Fatal("NewOccurrence accepted a nil definition")
	}
	if _, err := NewOccurrence(validEntry(), Period{}, loc, now); err == nil {
		t.Fatal("NewOccurrence accepted the zero period")
	}
}

// Paying, and unpaying, and what unpaying must not touch.
func TestOccurrencePaidTransitions(t *testing.T) {
	at := time.Date(2026, time.September, 14, 10, 0, 0, 0, time.UTC)

	o := validOccurrence()
	if err := o.MarkPaid(at); err != nil {
		t.Fatalf("MarkPaid: %v", err)
	}
	if o.Status != OccurrencePaid || o.PaidAt == nil || !o.PaidAt.Equal(at) {
		t.Fatal("MarkPaid did not settle the month")
	}
	if err := o.Validate(); err != nil {
		t.Fatalf("a paid occurrence does not validate: %v", err)
	}

	// Paying twice is refused rather than re-stamped: the second call would
	// move a recorded payment date, and a date that moves is not a record.
	later := at.Add(48 * time.Hour)
	if err := o.MarkPaid(later); err == nil {
		t.Fatal("MarkPaid accepted an already-paid occurrence")
	}
	if !o.PaidAt.Equal(at) {
		t.Fatal("a refused second payment still moved the date")
	}
	if err := o.MarkPaid(time.Time{}); err == nil {
		t.Fatal("MarkPaid accepted a zero time")
	}

	// A link the future will write, to prove unticking drops it.
	txID := uuid.New()
	o.TransactionID = &txID
	o.AmountEstimated = false
	o.AmountCents = 13500

	o.UnmarkPaid()
	if o.Status != OccurrencePending || o.PaidAt != nil {
		t.Fatal("UnmarkPaid did not return the month to pending")
	}
	if o.TransactionID != nil {
		t.Fatal("UnmarkPaid left a transaction linked to an unpaid month")
	}
	// The amount is a separate fact and survives. Being wrong about whether
	// it was paid says nothing about whether it was the right number.
	if o.AmountCents != 13500 || o.AmountEstimated {
		t.Fatal("UnmarkPaid disturbed the amount")
	}
	if err := o.Validate(); err != nil {
		t.Fatalf("an unticked occurrence does not validate: %v", err)
	}

	// And unticking an already-pending month is simply a no-op.
	o.UnmarkPaid()
	if o.Status != OccurrencePending {
		t.Fatal("unticking twice broke the row")
	}
}

// Setting an amount is how an estimate becomes a confirmed figure.
func TestOccurrenceSetAmountClearsTheEstimate(t *testing.T) {
	o := validOccurrence()
	o.AmountEstimated = true

	if err := o.SetAmountCents(42350); err != nil {
		t.Fatalf("SetAmountCents: %v", err)
	}
	if o.AmountCents != 42350 {
		t.Fatalf("the amount is %d cents", o.AmountCents)
	}
	if o.AmountEstimated {
		t.Fatal("confirming an amount left it marked as an estimate")
	}

	for _, bad := range []int64{0, -1, -42350} {
		if err := o.SetAmountCents(bad); err == nil {
			t.Fatalf("SetAmountCents accepted %d", bad)
		}
	}
	if o.AmountCents != 42350 {
		t.Fatal("a refused amount was written anyway")
	}
}

func TestOccurrenceOverdue(t *testing.T) {
	day := func(d int) time.Time { return time.Date(2026, time.September, d, 0, 0, 0, 0, time.UTC) }

	cases := []struct {
		name   string
		status OccurrenceStatus
		today  time.Time
		want   bool
	}{
		{name: "pending, before the due date", status: OccurrencePending, today: day(10), want: false},
		{name: "pending, on the due date", status: OccurrencePending, today: day(15), want: false},
		{name: "pending, the day after", status: OccurrencePending, today: day(16), want: true},
		{name: "pending, long after", status: OccurrencePending, today: day(30), want: true},
		// Lateness that has been resolved is not a thing to show.
		{name: "paid, long after the due date", status: OccurrencePaid, today: day(30), want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := validOccurrence() // due on the 15th
			if tc.status == OccurrencePaid {
				at := day(28)
				o.Status, o.PaidAt = OccurrencePaid, &at
			}
			if got := o.Overdue(tc.today); got != tc.want {
				t.Fatalf("Overdue = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestSummarizeOccurrences is the arithmetic every surface will depend on.
func TestSummarizeOccurrences(t *testing.T) {
	p := MustPeriod(2026, time.September)
	today := time.Date(2026, time.September, 20, 0, 0, 0, 0, time.UTC)

	paid := func(cents int64, dueDay int) RecurringOccurrence {
		at := time.Date(2026, time.September, dueDay, 9, 0, 0, 0, time.UTC)
		o := *validOccurrence()
		o.AmountCents, o.DueOn, o.Status, o.PaidAt = cents, p.DueOn(dueDay), OccurrencePaid, &at
		return o
	}
	pending := func(cents int64, dueDay int, estimated bool) RecurringOccurrence {
		o := *validOccurrence()
		o.AmountCents, o.DueOn, o.AmountEstimated = cents, p.DueOn(dueDay), estimated
		return o
	}

	t.Run("an empty month is zeros, not nils", func(t *testing.T) {
		m := SummarizeOccurrences(p, nil, today)
		if m.CommittedCents != 0 || m.PaidCents != 0 || m.RemainingCents != 0 ||
			m.EstimatedCents != 0 || m.Count != 0 || m.PaidCount != 0 ||
			m.PendingCount != 0 || m.EstimatedCount != 0 || m.OverdueCount != 0 {
			t.Fatalf("an empty month summarised as %+v", m)
		}
		if !m.Period.Equal(p) {
			t.Fatal("the summary lost its own period")
		}
	})

	t.Run("the sprint's own example", func(t *testing.T) {
		// Rent 2500 paid, internet 130 paid, electricity 420 pending and
		// estimated, gym 170 pending and overdue.
		occ := []RecurringOccurrence{
			paid(250000, 5),
			paid(13000, 15),
			pending(42000, 25, true),
			pending(17000, 10, false),
		}
		m := SummarizeOccurrences(p, occ, today)

		if m.CommittedCents != 322000 {
			t.Fatalf("committed = %d, want 322000", m.CommittedCents)
		}
		if m.PaidCents != 263000 {
			t.Fatalf("paid = %d, want 263000", m.PaidCents)
		}
		if m.RemainingCents != 59000 {
			t.Fatalf("remaining = %d, want 59000", m.RemainingCents)
		}
		if m.RemainingCents != m.CommittedCents-m.PaidCents {
			t.Fatal("remaining is not committed minus paid")
		}
		if m.EstimatedCents != 42000 {
			t.Fatalf("estimated = %d, want 42000", m.EstimatedCents)
		}
		if m.Count != 4 || m.PaidCount != 2 || m.PendingCount != 2 {
			t.Fatalf("counts are %d total, %d paid, %d pending", m.Count, m.PaidCount, m.PendingCount)
		}
		// One estimate among the four: the electricity bill.
		if m.EstimatedCount != 1 {
			t.Fatalf("estimated count = %d, want 1", m.EstimatedCount)
		}
		// Only the gym: due on the 10th, unpaid, and today is the 20th.
		if m.OverdueCount != 1 {
			t.Fatalf("overdue = %d, want 1", m.OverdueCount)
		}
		if m.Projection {
			t.Fatal("summing rows decided the month was a projection")
		}
	})

	t.Run("everything paid leaves nothing remaining", func(t *testing.T) {
		m := SummarizeOccurrences(p, []RecurringOccurrence{paid(250000, 5), paid(13000, 15)}, today)
		if m.RemainingCents != 0 || m.PendingCount != 0 || m.OverdueCount != 0 {
			t.Fatalf("a fully settled month summarised as %+v", m)
		}
	})

	t.Run("a paid estimate still counts as an estimate", func(t *testing.T) {
		// The bill was ticked without the real figure ever being entered,
		// so the total is settled and still partly a guess. Reporting it as
		// exact would be the product deciding the guess had become a fact.
		o := paid(42000, 25)
		o.AmountEstimated = true
		m := SummarizeOccurrences(p, []RecurringOccurrence{o}, today)
		if m.EstimatedCents != 42000 || m.EstimatedCount != 1 {
			t.Fatalf("estimated = %d cents across %d rows, want 42000 across 1",
				m.EstimatedCents, m.EstimatedCount)
		}
		if m.PaidCents != 42000 || m.RemainingCents != 0 {
			t.Fatalf("paid = %d, remaining = %d", m.PaidCents, m.RemainingCents)
		}
	})
}
