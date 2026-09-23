package domain

import (
	"strconv"
	"strings"
	"time"
)

// A calendar month, as an identity.
//
// ══════════════════════════════════════════════════════════════════════
//
//	A MONTH IS A NAME, NOT A RANGE OF INSTANTS
//
// ══════════════════════════════════════════════════════════════════════
//
// ── Why this is a type and not a string, nor a time.Time ───────────────
// A monthly obligation is filed under a month, and that month has to mean
// exactly one thing in the database, in the API, in a tool argument and in
// a screen. Two representations are two chances to disagree.
//
// A bare string would let "2026-9", "2026-09-01" and "Sep 2026" all reach
// storage, and the first month somebody wrote one of them is the month that
// silently gets a second row. A bare time.Time is worse: it carries a day
// and an hour that nothing here means, so "September" and "September 17th
// at 14:03" become indistinguishable to every function that takes one, and
// the 17th is what ends up in the unique index.
//
// So the fields are UNEXPORTED. The only ways to obtain a Period are to
// parse one, to derive one from an instant in a named zone, or to name a
// year and a month outright, and all three validate. A zero Period is
// invalid and says so.
//
// ── The one canonical form ─────────────────────────────────────────────
// `YYYY-MM`, zero-padded. That is what String, MarshalJSON and every
// boundary emit, and it is the only thing ParsePeriod accepts.
//
// ── Where the zone enters, and where it must not ───────────────────────
// A Period has NO zone. "September 2026" is a name, and naming it does not
// require knowing where anybody is standing.
//
// A zone is needed only to cross between a month and an INSTANT, which is
// why Start, End and PeriodOf take a *time.Location and the rest do not.
// That crossing happens at exactly one place in production, the finance
// reporting zone (FINANCE_TIMEZONE), for the reason period.go in the tools
// package already gives: the screens used to cut months in the browser's
// zone and a conversation has no browser.
type Period struct {
	year  int
	month time.Month
}

// periodLayout is the only form a Period is written in or read from.
const periodLayout = "2006-01"

// The bounds a year must fall inside.
//
// Not arbitrary paranoia: the failure they catch is a model or a URL
// producing `0001-01` or `202-06`, which would otherwise become a real row
// with a real unique key and be indistinguishable from an intended one
// afterwards. Wide enough that no real recurring obligation is refused.
const (
	minPeriodYear = 1970
	maxPeriodYear = 9999
)

// NewPeriod names a month outright.
func NewPeriod(year int, month time.Month) (Period, error) {
	if year < minPeriodYear || year > maxPeriodYear {
		return Period{}, Invalid("period year must be between " +
			strconv.Itoa(minPeriodYear) + " and " + strconv.Itoa(maxPeriodYear))
	}
	if month < time.January || month > time.December {
		return Period{}, Invalid("period month must be 1..12")
	}
	return Period{year: year, month: month}, nil
}

// MustPeriod is NewPeriod for a literal the caller knows is valid.
//
// For fixtures and constants only. It panics, which is right for a
// programming error and wrong for anything that came from outside: input
// goes through ParsePeriod, which returns a domain error a caller can hand
// to a person.
func MustPeriod(year int, month time.Month) Period {
	p, err := NewPeriod(year, month)
	if err != nil {
		panic("domain.MustPeriod: " + err.Error())
	}
	return p
}

// ParsePeriod reads the canonical form, and only the canonical form.
//
// ── Why it is strict about things a lenient parser would accept ────────
// `time.Parse` with this layout accepts "2026-9" and quietly reads a
// four-digit year out of shorter input in some cases. Both would produce a
// Period that is almost right, which is the worst outcome available: the
// month lands, the identity differs from the one a canonical writer would
// produce, and the unique index no longer means what it is there to mean.
//
// A date is refused rather than truncated for the same reason. "2026-09-17"
// is a caller that thinks it is talking about a day, and answering it with
// a month would be this function deciding what they meant.
func ParsePeriod(raw string) (Period, error) {
	s := strings.TrimSpace(raw)
	if len(s) != len(periodLayout) || s[4] != '-' {
		return Period{}, Invalid("period must be a calendar month as YYYY-MM")
	}
	for i, c := range s {
		if i == 4 {
			continue
		}
		if c < '0' || c > '9' {
			return Period{}, Invalid("period must be a calendar month as YYYY-MM")
		}
	}
	t, err := time.Parse(periodLayout, s)
	if err != nil {
		return Period{}, Invalid("period must be a calendar month as YYYY-MM")
	}
	return NewPeriod(t.Year(), t.Month())
}

// PeriodOf is the month an instant falls in, as seen from loc.
//
// The zone is required and is not defaulted. An instant one hour before
// midnight on the last day of August is August in São Paulo and September
// in UTC, and a function that picked one on the caller's behalf would put
// a month's worth of obligations in the wrong month twice a year for
// nobody's benefit.
func PeriodOf(t time.Time, loc *time.Location) Period {
	if loc == nil {
		loc = time.UTC
	}
	in := t.In(loc)
	return Period{year: in.Year(), month: in.Month()}
}

func (p Period) Year() int         { return p.year }
func (p Period) Month() time.Month { return p.month }

// IsZero reports the uninitialised value. It is never a valid month.
func (p Period) IsZero() bool { return p.year == 0 }

// String is the canonical form: `2026-09`.
func (p Period) String() string {
	if p.IsZero() {
		return ""
	}
	return time.Date(p.year, p.month, 1, 0, 0, 0, 0, time.UTC).Format(periodLayout)
}

// MarshalJSON / UnmarshalJSON keep the wire form canonical without any
// caller having to remember to convert.
func (p Period) MarshalJSON() ([]byte, error) {
	return []byte(`"` + p.String() + `"`), nil
}

func (p *Period) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	parsed, err := ParsePeriod(s)
	if err != nil {
		return err
	}
	*p = parsed
	return nil
}

// FirstDay is the month's first calendar day, as a DATE.
//
// ── Why midnight UTC, in a module that cares about zones ───────────────
// Because this is a CIVIL DATE and not an instant. It is what goes into a
// `date` column, which stores a year, a month and a day and nothing else.
// Building it at midnight UTC means the Y/M/D that pgx writes are exactly
// the ones named here, and the value that comes back scans to the same
// three numbers.
//
// It must never be used as "the moment the month began". That is Start,
// which takes a zone precisely because the two are different questions.
func (p Period) FirstDay() time.Time {
	return time.Date(p.year, p.month, 1, 0, 0, 0, 0, time.UTC)
}

// LastDay is how many days the month has: 28, 29, 30 or 31.
//
// Day zero of the following month is the last day of this one, which is the
// standard trick and is also the only one that gets February 2028 right
// without a leap-year rule written here to be got wrong.
func (p Period) LastDay() int {
	return time.Date(p.year, p.month+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

// Start is the first instant of the month in loc.
func (p Period) Start(loc *time.Location) time.Time {
	if loc == nil {
		loc = time.UTC
	}
	return time.Date(p.year, p.month, 1, 0, 0, 0, 0, loc)
}

// End is the first instant of the FOLLOWING month in loc: the exclusive
// bound of the half-open interval [Start, End).
//
// Half-open for the reason the tools' Window already gives: an inclusive
// end needs an end-of-day sentinel, and every sentinel is one rounding
// decision away from dropping the last row of the month.
func (p Period) End(loc *time.Location) time.Time {
	return p.Start(loc).AddDate(0, 1, 0)
}

// DueOn is the day a `due_day` falls on inside this month, CLAMPED to the
// last day the month actually has.
//
// ══════════════════════════════════════════════════════════════════════
//
//	DAY 31 IN FEBRUARY IS THE LAST OF FEBRUARY, NEVER THE FIRST OF MARCH
//
// ══════════════════════════════════════════════════════════════════════
//
// ── Why clamping, and why it is deterministic ──────────────────────────
// A person who says "vence dia 31" means the end of the month, and every
// month has one. The alternative Go gives for free is time.Date's
// normalisation, which turns 31 February into 3 March: a due date in the
// WRONG MONTH, on a row whose whole identity is the month it belongs to.
// That is not an edge case to be tolerated, it is a row filed under
// something it is not.
//
//	due_day 31, Feb 2026  →  2026-02-28
//	due_day 31, Feb 2028  →  2028-02-29   (leap)
//	due_day 31, Apr 2026  →  2026-04-30
//	due_day  5, Feb 2026  →  2026-02-05   (untouched)
//
// The result is a CIVIL DATE, midnight UTC, for the reason FirstDay gives.
//
// A day outside 1..31 is clamped rather than refused: RecurringEntry
// already refuses one at the boundary, and this function's job is to
// produce a date that is inside the month no matter what it is handed.
func (p Period) DueOn(dueDay int) time.Time {
	last := p.LastDay()
	switch {
	case dueDay < 1:
		dueDay = 1
	case dueDay > last:
		dueDay = last
	}
	return time.Date(p.year, p.month, dueDay, 0, 0, 0, 0, time.UTC)
}

// Contains reports whether a civil date falls inside this month.
func (p Period) Contains(day time.Time) bool {
	return day.Year() == p.year && day.Month() == p.month
}

// AddMonths moves n months forward, or backward when n is negative.
func (p Period) AddMonths(n int) Period {
	t := time.Date(p.year, p.month, 1, 0, 0, 0, 0, time.UTC).AddDate(0, n, 0)
	return Period{year: t.Year(), month: t.Month()}
}

func (p Period) Next() Period { return p.AddMonths(1) }
func (p Period) Prev() Period { return p.AddMonths(-1) }

// Compare orders two months: negative, zero or positive.
func (p Period) Compare(q Period) int {
	if p.year != q.year {
		if p.year < q.year {
			return -1
		}
		return 1
	}
	switch {
	case p.month < q.month:
		return -1
	case p.month > q.month:
		return 1
	default:
		return 0
	}
}

func (p Period) Before(q Period) bool { return p.Compare(q) < 0 }
func (p Period) After(q Period) bool  { return p.Compare(q) > 0 }
func (p Period) Equal(q Period) bool  { return p.Compare(q) == 0 }
