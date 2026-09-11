package tools

import (
	"testing"
	"time"
)

// Periods are resolved against a PINNED clock here.
//
// A test that called time.Now would have to recompute the same arithmetic
// to know what to expect, and would then agree with the implementation by
// construction — including when both are wrong. Fixing the day makes each
// boundary a literal a reader can check against a calendar.

// Wednesday, 26 August 2026, 23:40 in São Paulo. Chosen because it is
// already the 27th in UTC: every assertion below would move by a day if the
// resolver used the server's zone instead of the reporting one, which is
// the failure this design exists to prevent.
func pinned(t *testing.T) resolver {
	t.Helper()
	loc, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	now := time.Date(2026, 8, 26, 23, 40, 0, 0, loc)
	if now.UTC().Day() != 27 {
		t.Fatalf("fixture no longer straddles midnight UTC: %s", now.UTC())
	}
	return resolver{loc: loc, now: func() time.Time { return now }}
}

func TestPeriodBoundaries(t *testing.T) {
	r := pinned(t)
	const f = "2006-01-02"
	cases := []struct {
		period   Period
		from, to string // `to` is exclusive, as stored in Window
	}{
		{PeriodToday, "2026-08-26", "2026-08-27"},
		{PeriodYesterday, "2026-08-25", "2026-08-26"},
		// 26 Aug 2026 is a Wednesday; the week runs Monday 24 to Monday 31.
		{PeriodThisWeek, "2026-08-24", "2026-08-31"},
		{PeriodLastWeek, "2026-08-17", "2026-08-24"},
		{PeriodThisMonth, "2026-08-01", "2026-09-01"},
		{PeriodLastMonth, "2026-07-01", "2026-08-01"},
		// Rolling windows include today, so seven days ends tomorrow.
		{PeriodLast7Days, "2026-08-20", "2026-08-27"},
		{PeriodLast30Days, "2026-07-28", "2026-08-27"},
		{PeriodLast90Days, "2026-05-29", "2026-08-27"},
		{PeriodThisYear, "2026-01-01", "2027-01-01"},
		{PeriodLast12Month, "2025-08-27", "2026-08-27"},
	}
	for _, c := range cases {
		w, ok := r.resolvePeriod(c.period)
		if !ok {
			t.Fatalf("%s did not resolve", c.period)
		}
		if got := w.From.Format(f); got != c.from {
			t.Errorf("%s from = %s, want %s", c.period, got, c.from)
		}
		if got := w.To.Format(f); got != c.to {
			t.Errorf("%s to = %s, want %s", c.period, got, c.to)
		}
		if w.From.Location() != r.loc {
			t.Errorf("%s: window is not in the reporting zone", c.period)
		}
	}
}

// The month a period cuts is the one the OPERATOR is living in, not the one
// UTC has already rolled into. With the clock pinned to 23:40 local on the
// 26th — 02:40 UTC on the 27th — "today" must still be the 26th.
func TestTodayFollowsTheReportingZoneAndNotUTC(t *testing.T) {
	r := pinned(t)
	if got := r.today().Format("2006-01-02"); got != "2026-08-26" {
		t.Fatalf("today = %s, want 2026-08-26 (UTC would say the 27th)", got)
	}
	w, _ := r.resolvePeriod(PeriodToday)
	// The window must open at local midnight, which is 03:00 UTC.
	if got := w.From.UTC().Format(time.RFC3339); got != "2026-08-26T03:00:00Z" {
		t.Fatalf("today window opens at %s, want 2026-08-26T03:00:00Z", got)
	}
}

func TestUnknownPeriodIsRefused(t *testing.T) {
	r := pinned(t)
	for _, bad := range []Period{"", "this_fortnight", "ontem", "LAST_MONTH "} {
		if _, ok := r.resolvePeriod(bad); ok {
			t.Errorf("period %q resolved and should not have", bad)
		}
	}
}

// A hand-written range names days a person means to INCLUDE at both ends.
func TestCustomWindowIncludesTheLastDay(t *testing.T) {
	r := pinned(t)
	w, ok := r.customWindow("2026-08-01", "2026-08-15")
	if !ok {
		t.Fatal("custom window did not resolve")
	}
	if got := w.From.Format("2006-01-02"); got != "2026-08-01" {
		t.Fatalf("from = %s", got)
	}
	// Exclusive end is the 16th, which is what makes the 15th included.
	if got := w.To.Format("2006-01-02"); got != "2026-08-16" {
		t.Fatalf("to = %s, want 2026-08-16 so the 15th is included", got)
	}
	if w.Days() != 15 {
		t.Fatalf("days = %d, want 15", w.Days())
	}
	// A single day is a valid range, not an empty one.
	one, ok := r.customWindow("2026-08-10", "2026-08-10")
	if !ok || one.Days() != 1 {
		t.Fatalf("single-day window: ok=%v days=%d", ok, one.Days())
	}
}

func TestCustomWindowRefusesBadInput(t *testing.T) {
	r := pinned(t)
	cases := [][2]string{
		{"2026-08-15", "2026-08-01"},           // reversed
		{"15/08/2026", "2026-08-20"},           // not ISO
		{"2026-08-01T00:00:00Z", "2026-08-20"}, // a timestamp, not a date
		{"", "2026-08-20"},
		{"2026-13-01", "2026-13-05"}, // no such month
	}
	for _, c := range cases {
		if _, ok := r.customWindow(c[0], c[1]); ok {
			t.Errorf("customWindow(%q, %q) was accepted", c[0], c[1])
		}
	}
}

// An omitted date means NOW; a given one is stamped at midday local, so the
// calendar day survives being read in any other zone.
func TestOccurredAt(t *testing.T) {
	r := pinned(t)

	now, ok := r.occurredAt("")
	if !ok {
		t.Fatal("empty date was refused")
	}
	if !now.Equal(r.now()) {
		t.Fatalf("omitted date = %s, want the current instant", now)
	}

	given, ok := r.occurredAt("2026-08-10")
	if !ok {
		t.Fatal("valid date was refused")
	}
	if given.Hour() != 12 || given.Location() != r.loc {
		t.Fatalf("given date = %s, want midday in %s", given, r.loc)
	}
	// The property that matters: read in any zone within twelve hours of
	// the reporting one — which covers every zone this operator's data is
	// realistically read in, UTC above all — it is still the 10th of August.
	// Midnight would have failed the very first of these.
	for _, zone := range []string{"UTC", "Europe/Lisbon", "America/Los_Angeles", "Africa/Nairobi"} {
		loc, err := time.LoadLocation(zone)
		if err != nil {
			continue
		}
		if got := given.In(loc).Format("2006-01-02"); got != "2026-08-10" {
			t.Errorf("in %s the date reads %s, want 2026-08-10", zone, got)
		}
	}

	if _, ok := r.occurredAt("10/08/2026"); ok {
		t.Error("a non-ISO date was accepted")
	}
}
