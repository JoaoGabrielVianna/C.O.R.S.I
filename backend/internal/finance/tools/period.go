package tools

import (
	"strings"
	"time"
)

// Time at the agent boundary.
//
// ── The problem this file exists for ───────────────────────────────────
// Nothing in a turn tells the model what today is. There is no date in the
// system prompt, none in the grounding policy, and none in the context
// builder — verified, not assumed. A model asked "quanto gastei este mês"
// therefore has no way to compute a window, and a model asked to record
// something "hoje" has no way to produce a date. Left to itself it will
// produce a confident one anyway, drawn from its training, and every number
// that follows will be about the wrong weeks.
//
// So the model is never asked for a date it does not have. It names a
// PERIOD — a word it does know, because the user said it — and this file
// resolves that word against the server clock, in the configured reporting
// zone. Explicit `from`/`to` remain available for the cases a word cannot
// express, and those are calendar dates rather than instants for the same
// reason: a model that must produce an RFC3339 timestamp will produce a
// timezone offset it invented.
//
// Every read result then STATES the window it used and today's date, so the
// model can say "de 1 a 26 de agosto" and be right, and so the next turn
// has the date it was missing.

// dateLayout is the only date form these tools accept or emit.
const dateLayout = "2006-01-02"

// Period names a window a person would say out loud.
//
// Closed set, and closed on purpose: each one is a rule about calendar
// boundaries, and a rule that is not written here would be one the model
// improvises.
type Period string

const (
	PeriodToday       Period = "today"
	PeriodYesterday   Period = "yesterday"
	PeriodThisWeek    Period = "this_week"
	PeriodLastWeek    Period = "last_week"
	PeriodThisMonth   Period = "this_month"
	PeriodLastMonth   Period = "last_month"
	PeriodLast7Days   Period = "last_7_days"
	PeriodLast30Days  Period = "last_30_days"
	PeriodLast90Days  Period = "last_90_days"
	PeriodThisYear    Period = "this_year"
	PeriodLast12Month Period = "last_12_months"
)

// periodNames is the vocabulary, in the order it is offered to the model.
// Named once so the schema description and the parser cannot disagree.
func periodNames() []string {
	return []string{
		string(PeriodToday), string(PeriodYesterday),
		string(PeriodThisWeek), string(PeriodLastWeek),
		string(PeriodThisMonth), string(PeriodLastMonth),
		string(PeriodLast7Days), string(PeriodLast30Days), string(PeriodLast90Days),
		string(PeriodThisYear), string(PeriodLast12Month),
	}
}

func periodDescription(prefix string) string {
	return prefix + " One of: " + strings.Join(periodNames(), ", ") + "."
}

// Window is a half-open interval [From, To), which is how every existing
// finance query already reads a range: `occurred_at >= from AND < to`.
//
// Half-open rather than inclusive because the alternative needs an
// end-of-day sentinel, and every sentinel is one rounding decision away
// from dropping the last transaction of the month.
type Window struct {
	From time.Time
	To   time.Time
	// Label is the period word this came from, or "custom".
	Label string
}

// Days is the window's span in whole days, used only for reporting.
func (w Window) Days() int {
	return int(w.To.Sub(w.From).Hours() / 24)
}

// clock is the source of "now".
//
// ── Why it is injected and not time.Now ────────────────────────────────
// Two reasons, and the second one is not about testing.
//
// A test can pin the day and assert on the boundaries a period produces;
// otherwise the only test possible is one that recomputes the same
// arithmetic and agrees with itself, including when both are wrong.
//
// And in production this is the DATABASE's clock, not the API process's.
// The finance contract classifies REALIZED against `now()` evaluated by
// Postgres, so a transaction stamped from a different clock can land in the
// future by the amount the two disagree — and a payment recorded a moment
// ago then reports as PROJECTED, which is how "quanto gastei hoje" answers
// zero seconds after recording an expense. See ports.Clock.
type clock func() time.Time

// resolver cuts periods in one zone, against one clock.
type resolver struct {
	loc *time.Location
	now clock
}

// today is the current calendar date in the reporting zone.
func (r resolver) today() time.Time {
	n := r.now().In(r.loc)
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, r.loc)
}

// resolvePeriod turns a period word into a window.
//
// ── The boundary rules, stated so they are reviewable ──────────────────
//
//	today            [today 00:00, tomorrow 00:00)
//	yesterday        [yesterday 00:00, today 00:00)
//	this_week        [Monday 00:00, next Monday 00:00)
//	last_week        the seven days before this_week
//	this_month       [1st 00:00, 1st of next month 00:00)
//	last_month       the whole previous calendar month
//	last_7_days      [today-6 00:00, tomorrow 00:00)   — includes today
//	last_30_days     [today-29 00:00, tomorrow 00:00)  — includes today
//	last_90_days     [today-89 00:00, tomorrow 00:00)  — includes today
//	this_year        [Jan 1 00:00, Jan 1 next year 00:00)
//	last_12_months   [same day 12 months ago 00:00, tomorrow 00:00)
//
// The week starts on MONDAY. That is the ISO rule and the one the operator
// lives by; a Sunday-start week would move every weekly figure by two days
// without anything in the answer saying so.
//
// The rolling windows INCLUDE today, because "os últimos 7 dias" said out
// loud means the week up to and including now, not a week that stopped
// yesterday.
func (r resolver) resolvePeriod(p Period) (Window, bool) {
	today := r.today()
	tomorrow := today.AddDate(0, 0, 1)
	w := Window{Label: string(p)}

	switch p {
	case PeriodToday:
		w.From, w.To = today, tomorrow
	case PeriodYesterday:
		w.From, w.To = today.AddDate(0, 0, -1), today
	case PeriodThisWeek:
		w.From = startOfWeek(today)
		w.To = w.From.AddDate(0, 0, 7)
	case PeriodLastWeek:
		w.To = startOfWeek(today)
		w.From = w.To.AddDate(0, 0, -7)
	case PeriodThisMonth:
		w.From = time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, r.loc)
		w.To = w.From.AddDate(0, 1, 0)
	case PeriodLastMonth:
		w.To = time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, r.loc)
		w.From = w.To.AddDate(0, -1, 0)
	case PeriodLast7Days:
		w.From, w.To = today.AddDate(0, 0, -6), tomorrow
	case PeriodLast30Days:
		w.From, w.To = today.AddDate(0, 0, -29), tomorrow
	case PeriodLast90Days:
		w.From, w.To = today.AddDate(0, 0, -89), tomorrow
	case PeriodThisYear:
		w.From = time.Date(today.Year(), 1, 1, 0, 0, 0, 0, r.loc)
		w.To = w.From.AddDate(1, 0, 0)
	case PeriodLast12Month:
		w.From, w.To = tomorrow.AddDate(0, -12, 0), tomorrow
	default:
		return Window{}, false
	}
	return w, true
}

// startOfWeek is the Monday at or before d.
func startOfWeek(d time.Time) time.Time {
	// time.Weekday counts Sunday as 0; Monday-based offset is (day+6)%7.
	offset := (int(d.Weekday()) + 6) % 7
	return d.AddDate(0, 0, -offset)
}

// parseDate reads a YYYY-MM-DD calendar date as midnight in the reporting
// zone.
//
// Date-only, never a timestamp, and that is the contract: an RFC3339 value
// carries an offset, the model has no way to know the right one, and a
// window built from an invented offset is wrong by hours at both ends.
func (r resolver) parseDate(s string) (time.Time, bool) {
	t, err := time.ParseInLocation(dateLayout, strings.TrimSpace(s), r.loc)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// customWindow builds [from 00:00, to+1day 00:00) from two calendar dates,
// so `to` names a day that is INCLUDED.
//
// Inclusive because that is what a person means by "de 1 a 15": excluding
// the 15th would drop a day from every hand-written range, and the mistake
// would look like a small number rather than like an error.
func (r resolver) customWindow(fromRaw, toRaw string) (Window, bool) {
	from, ok := r.parseDate(fromRaw)
	if !ok {
		return Window{}, false
	}
	to, ok := r.parseDate(toRaw)
	if !ok {
		return Window{}, false
	}
	if to.Before(from) {
		return Window{}, false
	}
	return Window{From: from, To: to.AddDate(0, 0, 1), Label: "custom"}, true
}

// occurredAt turns an optional calendar date into the instant a
// transaction is stamped with.
//
// ── Omitted means NOW, and that is not a fabricated default ────────────
// The user said "paguei", in the present, in a conversation happening now.
// The server clock is the most truthful reading of when that was available
// to this system, and it is strictly better than the alternative — a date
// the model produced without being told today's — which is the failure
// this whole file exists to avoid.
//
// ── A given date is stamped at MIDDAY, not midnight ────────────────────
// Because midnight sits exactly on a day boundary. Any consumer reading the
// row in a different zone — a UTC export, a future report, a screen open on
// a laptop set to another country — flips it to the neighbouring day. Midday
// is twelve hours from either edge, so the calendar day survives being read
// in any zone within twelve hours of this one — which is every zone the
// operator's data realistically reaches, UTC above all. The time of day was
// never information the user gave, so nothing is lost by choosing it.
func (r resolver) occurredAt(dateRaw string) (time.Time, bool) {
	if strings.TrimSpace(dateRaw) == "" {
		return r.now(), true
	}
	d, ok := r.parseDate(dateRaw)
	if !ok {
		return time.Time{}, false
	}
	return time.Date(d.Year(), d.Month(), d.Day(), 12, 0, 0, 0, r.loc), true
}
