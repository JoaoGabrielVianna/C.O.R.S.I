package domain

import (
	"encoding/json"
	"testing"
	"time"
)

// The reporting zone these tests reason in. São Paulo is UTC-3 with no DST
// today, which is exactly what makes it useful here: an instant late in the
// evening is a different calendar day in UTC, so a month boundary read in
// the wrong zone is visible rather than theoretical.
func saoPaulo(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	return loc
}

func TestParsePeriod(t *testing.T) {
	cases := []struct {
		name      string
		in        string
		wantYear  int
		wantMonth time.Month
		wantErr   bool
	}{
		{name: "canonical", in: "2026-09", wantYear: 2026, wantMonth: time.September},
		{name: "january", in: "2026-01", wantYear: 2026, wantMonth: time.January},
		{name: "december", in: "2026-12", wantYear: 2026, wantMonth: time.December},
		{name: "surrounding space is trimmed", in: "  2026-09  ", wantYear: 2026, wantMonth: time.September},

		// Every one of these is a caller that is ALMOST right, which is the
		// only interesting kind. A lenient parser accepts several of them
		// and produces a second identity for a month that already has one.
		{name: "unpadded month", in: "2026-9", wantErr: true},
		{name: "month zero", in: "2026-00", wantErr: true},
		{name: "month thirteen", in: "2026-13", wantErr: true},
		{name: "a date, not a month", in: "2026-09-01", wantErr: true},
		{name: "short year", in: "202-06", wantErr: true},
		{name: "slash separator", in: "2026/09", wantErr: true},
		{name: "letters", in: "20x6-09", wantErr: true},
		{name: "empty", in: "", wantErr: true},
		{name: "year below the floor", in: "0001-01", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParsePeriod(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParsePeriod(%q) accepted it as %s", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParsePeriod(%q): %v", tc.in, err)
			}
			if got.Year() != tc.wantYear || got.Month() != tc.wantMonth {
				t.Fatalf("ParsePeriod(%q) = %d-%02d, want %d-%02d",
					tc.in, got.Year(), got.Month(), tc.wantYear, tc.wantMonth)
			}
		})
	}
}

// Whatever a Period was built from, it writes itself one way.
func TestPeriodRoundTripsThroughItsCanonicalForm(t *testing.T) {
	for _, in := range []string{"2026-01", "2026-09", "2026-12", "1970-01", "9999-12"} {
		p, err := ParsePeriod(in)
		if err != nil {
			t.Fatalf("ParsePeriod(%q): %v", in, err)
		}
		if got := p.String(); got != in {
			t.Fatalf("%q round-tripped to %q", in, got)
		}
		raw, err := json.Marshal(p)
		if err != nil {
			t.Fatalf("marshal %s: %v", in, err)
		}
		if string(raw) != `"`+in+`"` {
			t.Fatalf("%q marshalled as %s", in, raw)
		}
		var back Period
		if err := json.Unmarshal(raw, &back); err != nil {
			t.Fatalf("unmarshal %s: %v", raw, err)
		}
		if !back.Equal(p) {
			t.Fatalf("%s did not survive a JSON round trip", in)
		}
	}
}

// The zero value is not a month and never pretends to be one.
func TestZeroPeriodIsNotAMonth(t *testing.T) {
	var p Period
	if !p.IsZero() {
		t.Fatal("the zero Period does not report itself as zero")
	}
	if p.String() != "" {
		t.Fatalf("the zero Period renders as %q", p.String())
	}
}

// PeriodOf is where an instant becomes a month, and the zone decides.
func TestPeriodOfReadsTheInstantInTheGivenZone(t *testing.T) {
	loc := saoPaulo(t)

	// 21:00 on 31 August in São Paulo is already 00:00 on 1 September in
	// UTC. A monthly view that read this instant in the wrong zone would
	// file August's last evening under September.
	instant := time.Date(2026, time.August, 31, 21, 0, 0, 0, loc)

	if got := PeriodOf(instant, loc); got.String() != "2026-08" {
		t.Fatalf("in São Paulo the instant is %s, want 2026-08", got)
	}
	if got := PeriodOf(instant, time.UTC); got.String() != "2026-09" {
		t.Fatalf("in UTC the same instant is %s, want 2026-09", got)
	}
}

// TestPeriodDueOnClamps is the deterministic due-date rule.
//
// The failure it exists to prevent is not a wrong day, it is a wrong
// MONTH: time.Date normalises 31 February into 3 March, which would put a
// due date outside the period the row is filed under.
func TestPeriodDueOnClamps(t *testing.T) {
	cases := []struct {
		name   string
		period string
		dueDay int
		want   string
	}{
		{name: "31 february, common year", period: "2026-02", dueDay: 31, want: "2026-02-28"},
		{name: "31 february, leap year", period: "2028-02", dueDay: 31, want: "2028-02-29"},
		{name: "30 february, common year", period: "2026-02", dueDay: 30, want: "2026-02-28"},
		{name: "29 february, common year", period: "2026-02", dueDay: 29, want: "2026-02-28"},
		{name: "29 february, leap year is untouched", period: "2028-02", dueDay: 29, want: "2028-02-29"},
		{name: "31 april", period: "2026-04", dueDay: 31, want: "2026-04-30"},
		{name: "31 june", period: "2026-06", dueDay: 31, want: "2026-06-30"},
		{name: "31 september", period: "2026-09", dueDay: 31, want: "2026-09-30"},
		{name: "31 november", period: "2026-11", dueDay: 31, want: "2026-11-30"},
		{name: "31 january is untouched", period: "2026-01", dueDay: 31, want: "2026-01-31"},
		{name: "31 december is untouched", period: "2026-12", dueDay: 31, want: "2026-12-31"},
		{name: "an ordinary day is untouched", period: "2026-02", dueDay: 5, want: "2026-02-05"},
		{name: "the first is untouched", period: "2026-02", dueDay: 1, want: "2026-02-01"},
		{name: "the last day exactly", period: "2026-04", dueDay: 30, want: "2026-04-30"},

		// Defensive: RecurringEntry refuses these at its own boundary, and
		// this function still has to return a date inside its month.
		{name: "below the floor is clamped up", period: "2026-02", dueDay: 0, want: "2026-02-01"},
		{name: "negative is clamped up", period: "2026-02", dueDay: -7, want: "2026-02-01"},
		{name: "far above is clamped down", period: "2026-02", dueDay: 99, want: "2026-02-28"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, err := ParsePeriod(tc.period)
			if err != nil {
				t.Fatalf("ParsePeriod(%q): %v", tc.period, err)
			}
			got := p.DueOn(tc.dueDay)
			if s := got.Format("2006-01-02"); s != tc.want {
				t.Fatalf("%s due day %d landed on %s, want %s", tc.period, tc.dueDay, s, tc.want)
			}
			// The rule that actually matters: whatever was clamped, the
			// result is a day of the month it was asked about.
			if !p.Contains(got) {
				t.Fatalf("%s due day %d produced %s, which is outside its own period",
					tc.period, tc.dueDay, got.Format("2006-01-02"))
			}
		})
	}
}

func TestPeriodLastDay(t *testing.T) {
	cases := []struct {
		period string
		want   int
	}{
		{"2026-01", 31}, {"2026-02", 28}, {"2028-02", 29},
		// 2000 is divisible by 400 and IS a leap year; 2100 is divisible by
		// 100 and is NOT. Both are in the table because a hand-written leap
		// rule gets one of them wrong, and this one is not hand-written.
		{"2000-02", 29}, {"2100-02", 28},
		{"2026-03", 31}, {"2026-04", 30}, {"2026-06", 30},
		{"2026-09", 30}, {"2026-11", 30}, {"2026-12", 31},
	}
	for _, tc := range cases {
		t.Run(tc.period, func(t *testing.T) {
			p, err := ParsePeriod(tc.period)
			if err != nil {
				t.Fatalf("ParsePeriod: %v", err)
			}
			if got := p.LastDay(); got != tc.want {
				t.Fatalf("%s has %d days, want %d", tc.period, got, tc.want)
			}
		})
	}
}

// Start and End are the half-open interval every finance range query uses.
func TestPeriodBoundsAreHalfOpen(t *testing.T) {
	loc := saoPaulo(t)
	p := MustPeriod(2026, time.September)

	start, end := p.Start(loc), p.End(loc)
	if got := start.Format(time.RFC3339); got != "2026-09-01T00:00:00-03:00" {
		t.Fatalf("start is %s", got)
	}
	if got := end.Format(time.RFC3339); got != "2026-10-01T00:00:00-03:00" {
		t.Fatalf("end is %s", got)
	}
	// The last instant of the month is inside; the bound itself is not.
	last := end.Add(-time.Nanosecond)
	if PeriodOf(last, loc).String() != "2026-09" {
		t.Fatal("the last instant before the bound is not in the month")
	}
	if PeriodOf(end, loc).String() != "2026-10" {
		t.Fatal("the exclusive bound is not the next month")
	}
}

func TestPeriodArithmeticAndOrdering(t *testing.T) {
	sep := MustPeriod(2026, time.September)
	dec := MustPeriod(2026, time.December)

	if got := sep.Next().String(); got != "2026-10" {
		t.Fatalf("next after 2026-09 is %s", got)
	}
	if got := sep.Prev().String(); got != "2026-08" {
		t.Fatalf("previous before 2026-09 is %s", got)
	}
	// Across the year boundary in both directions.
	if got := dec.Next().String(); got != "2027-01" {
		t.Fatalf("next after 2026-12 is %s", got)
	}
	if got := MustPeriod(2026, time.January).Prev().String(); got != "2025-12" {
		t.Fatalf("previous before 2026-01 is %s", got)
	}
	if got := sep.AddMonths(-12).String(); got != "2025-09" {
		t.Fatalf("twelve months before 2026-09 is %s", got)
	}

	if !sep.Before(dec) || !dec.After(sep) {
		t.Fatal("September does not sort before December")
	}
	if !sep.Equal(MustPeriod(2026, time.September)) {
		t.Fatal("two Septembers of the same year are not equal")
	}
	// Ordering across years, where comparing month numbers alone is wrong.
	if !MustPeriod(2025, time.December).Before(MustPeriod(2026, time.January)) {
		t.Fatal("December 2025 does not sort before January 2026")
	}
}
