package domain

import (
	"testing"
	"time"
)

func tokens(n int) *int      { return &n }
func usd(v float64) *float64 { return &v }
func priced() Pricing        { return Pricing{Available: true, InputPerToken: 1e-6, OutputPerToken: 5e-6} }
func unpriced() Pricing      { return Pricing{} }
func turn(in, out int) TurnCeiling {
	return TurnCeiling{EstimatedPromptTokens: in, MaxOutputTokens: out}
}

/* ── the day ─────────────────────────────────────────────────────────── */

// TestUTCDayIsHalfOpen pins the window shape. Half open is what makes a
// midnight belong to exactly one day; a closed upper bound would count the
// boundary turn twice, once in each day.
func TestUTCDayIsHalfOpen(t *testing.T) {
	// Deliberately in a zone ahead of UTC: the day must be decided by the
	// instant, not by the zone the value happens to carry.
	tokyo := time.FixedZone("JST", 9*3600)
	at := time.Date(2026, 8, 10, 6, 30, 0, 0, tokyo) // 2026-08-09 21:30 UTC

	from, to := UTCDay(at)

	if from.Location() != time.UTC || to.Location() != time.UTC {
		t.Fatalf("window is not in UTC: %v .. %v", from, to)
	}
	if got := from.Format(time.RFC3339); got != "2026-08-09T00:00:00Z" {
		t.Fatalf("from = %s, want the UTC day of the instant, not of the local clock", got)
	}
	if got := to.Format(time.RFC3339); got != "2026-08-10T00:00:00Z" {
		t.Fatalf("to = %s", got)
	}
	if to.Sub(from) != 24*time.Hour {
		t.Fatalf("window is %v long", to.Sub(from))
	}

	// The boundary instants themselves.
	if f, _ := UTCDay(from); !f.Equal(from) {
		t.Fatalf("the first instant of a day belongs to a different day")
	}
	if f, _ := UTCDay(to); !f.Equal(to) {
		t.Fatalf("the upper bound belongs to the window it closes")
	}
}

/* ── no budget ───────────────────────────────────────────────────────── */

// TestEvaluateWithoutLimitsDoesNothing is the compatibility promise: an
// agent with no limits is not merely allowed, it is not evaluated.
func TestEvaluateWithoutLimitsDoesNothing(t *testing.T) {
	ev := Evaluate(Budget{}, DailyUsage{Tokens: 1_000_000, KnownCostUSD: 999}, turn(100, 4096), unpriced())

	if ev.Enabled || ev.Blocked {
		t.Fatalf("an agent with no limits was evaluated: %+v", ev)
	}
	if ev.Tokens != nil || ev.Cost != nil {
		t.Fatalf("dimensions were reported for an agent with no limits: %+v", ev)
	}
	if ev.NextTurnFits != nil {
		t.Fatalf("a fit was computed against limits that do not exist")
	}
}

/* ── token limit ─────────────────────────────────────────────────────── */

func TestTokenLimit(t *testing.T) {
	b := Budget{DailyTokenLimit: tokens(1000)}

	cases := []struct {
		name    string
		used    int64
		blocked bool
		state   BudgetState
	}{
		{"well under", 100, false, BudgetOK},
		{"just under the warning threshold", 799, false, BudgetOK},
		{"at the warning threshold", 800, false, BudgetWarning},
		{"one short of the limit", 999, false, BudgetWarning},
		// The boundary the whole rule turns on: reaching the limit blocks.
		{"exactly at the limit", 1000, true, BudgetBlocked},
		{"past the limit", 1500, true, BudgetBlocked},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ev := Evaluate(b, DailyUsage{Tokens: tc.used}, turn(0, 100), priced())
			if ev.Blocked != tc.blocked {
				t.Fatalf("blocked = %v, want %v (used %d of 1000)", ev.Blocked, tc.blocked, tc.used)
			}
			if ev.Tokens.State != tc.state {
				t.Fatalf("state = %q, want %q", ev.Tokens.State, tc.state)
			}
			if tc.blocked && ev.Reason != BlockTokens {
				t.Fatalf("reason = %q, want %q", ev.Reason, BlockTokens)
			}
		})
	}
}

// TestTokenLimitDoesNotBlockOnFit is D5, asserted directly.
//
// A turn whose ceiling would overshoot is still allowed, because the budget
// has not been reached. Blocking here instead would make a 1000-token limit
// behave like a 600-token one, and nobody configured 600.
func TestTokenLimitDoesNotBlockOnFit(t *testing.T) {
	b := Budget{DailyTokenLimit: tokens(1000)}
	// 600 used, and the next turn could take 400 prompt + 4096 output.
	ev := Evaluate(b, DailyUsage{Tokens: 600}, turn(400, 4096), priced())

	if ev.Blocked {
		t.Fatalf("a turn was refused although the budget was not reached: %+v", ev)
	}
	if ev.NextTurnFits == nil || *ev.NextTurnFits {
		t.Fatalf("the overshoot was not reported: NextTurnFits = %v", ev.NextTurnFits)
	}
	if ev.Tokens.Remaining != 400 {
		t.Fatalf("remaining = %v, want 400", ev.Tokens.Remaining)
	}
}

// TestTokenLimitCountsTheOutputCeiling: the fit check must include what the
// model is allowed to write, not only what we are about to send. A gate that
// forgot max_tokens would call a turn "fitting" when the reply alone can
// blow the limit.
func TestTokenLimitCountsTheOutputCeiling(t *testing.T) {
	b := Budget{DailyTokenLimit: tokens(1000)}

	withOutput := Evaluate(b, DailyUsage{Tokens: 500}, turn(100, 4096), priced())
	if *withOutput.NextTurnFits {
		t.Fatalf("500 used + 100 prompt + 4096 output was reported as fitting in 1000")
	}

	withoutOutput := Evaluate(b, DailyUsage{Tokens: 500}, turn(100, 0), priced())
	if !*withoutOutput.NextTurnFits {
		t.Fatalf("the fixture is wrong: 500 + 100 should fit in 1000")
	}
}

// TestZeroLimitFreezes: zero is a limit, not an absence. An agent set to
// zero refuses every turn, which is a legitimate way to park it.
func TestZeroLimitFreezes(t *testing.T) {
	ev := Evaluate(Budget{DailyTokenLimit: tokens(0)}, DailyUsage{}, turn(0, 1), priced())
	if !ev.Blocked || ev.Reason != BlockTokens {
		t.Fatalf("a zero limit did not block: %+v", ev)
	}
	if ev.Tokens.Percent != 1 {
		t.Fatalf("percent = %v; a zero limit is fully consumed, not a division by zero", ev.Tokens.Percent)
	}
}

/* ── cost limit ──────────────────────────────────────────────────────── */

func TestCostLimit(t *testing.T) {
	b := Budget{DailyCostLimitUSD: usd(2.0)}

	cases := []struct {
		name    string
		spent   float64
		blocked bool
		state   BudgetState
	}{
		{"well under", 0.10, false, BudgetOK},
		{"at the warning threshold", 1.60, false, BudgetWarning},
		{"exactly at the limit", 2.00, true, BudgetBlocked},
		{"past the limit", 2.50, true, BudgetBlocked},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ev := Evaluate(b, DailyUsage{KnownCostUSD: tc.spent}, turn(10, 10), priced())
			if ev.Blocked != tc.blocked {
				t.Fatalf("blocked = %v, want %v", ev.Blocked, tc.blocked)
			}
			if ev.Cost.State != tc.state {
				t.Fatalf("state = %q, want %q", ev.Cost.State, tc.state)
			}
			if tc.blocked && ev.Reason != BlockCost {
				t.Fatalf("reason = %q, want %q", ev.Reason, BlockCost)
			}
		})
	}
}

// TestCostLimitFailsClosedWithoutPricing is the sharpest rule in the file.
//
// A spend limit that cannot be applied is not a spend limit. Letting the
// turn through would mean the limit silently stops existing exactly when
// the gateway is having a bad day — which is when a runaway turn is most
// likely. So: refuse, and say which of the two it was.
func TestCostLimitFailsClosedWithoutPricing(t *testing.T) {
	ev := Evaluate(Budget{DailyCostLimitUSD: usd(2.0)}, DailyUsage{KnownCostUSD: 0.01},
		turn(10, 10), unpriced())

	if !ev.Blocked {
		t.Fatalf("an unpriceable turn was allowed under a spend limit: %+v", ev)
	}
	if ev.Reason != BlockPricingUnavailable {
		t.Fatalf("reason = %q, want %q", ev.Reason, BlockPricingUnavailable)
	}
}

// TestTokenLimitSurvivesMissingPricing is the other half of that rule, and
// the reason the two dimensions are evaluated in this order: the token
// limit does not depend on a rate card, so it must keep working when the
// rate card does not.
func TestTokenLimitSurvivesMissingPricing(t *testing.T) {
	b := Budget{DailyTokenLimit: tokens(1000)}

	under := Evaluate(b, DailyUsage{Tokens: 100}, turn(10, 10), unpriced())
	if under.Blocked {
		t.Fatalf("a token-limited agent was blocked because pricing was unavailable: %+v", under)
	}

	over := Evaluate(b, DailyUsage{Tokens: 1000}, turn(10, 10), unpriced())
	if !over.Blocked || over.Reason != BlockTokens {
		t.Fatalf("the token limit stopped working without a rate card: %+v", over)
	}
}

// TestTokenBlockWinsOverPricing: when both would refuse, the reason given
// is the one the user can act on most directly.
func TestTokenBlockWinsOverPricing(t *testing.T) {
	b := Budget{DailyTokenLimit: tokens(10), DailyCostLimitUSD: usd(2.0)}
	ev := Evaluate(b, DailyUsage{Tokens: 100}, turn(1, 1), unpriced())

	if ev.Reason != BlockTokens {
		t.Fatalf("reason = %q, want the token limit that is genuinely exhausted", ev.Reason)
	}
}

/* ── uncertainty ─────────────────────────────────────────────────────── */

// TestUnpricedTurnsAreSurfacedNotAbsorbed.
//
// Known spend understates whenever the day contains a turn nobody could
// price. That gap is reported — it must never be quietly counted as zero,
// which would tell the user they spent less than we know they did.
//
// It does NOT block on its own: the gap is historical and bounded, and
// refusing every turn for the rest of the day because one earlier turn went
// unpriced would be a self-inflicted outage, not a safety feature.
func TestUnpricedTurnsAreSurfacedNotAbsorbed(t *testing.T) {
	ev := Evaluate(Budget{DailyCostLimitUSD: usd(2.0)},
		DailyUsage{KnownCostUSD: 0.40, UnpricedTurns: 3}, turn(10, 10), priced())

	if ev.Blocked {
		t.Fatalf("historical uncertainty blocked a turn on its own: %+v", ev)
	}
	if ev.Certain {
		t.Fatalf("a window with 3 unpriced turns reported itself as certain")
	}
	if ev.UnpricedTurns != 3 {
		t.Fatalf("unpriced turns = %d, want 3", ev.UnpricedTurns)
	}
	if ev.Cost.Used != 0.40 {
		t.Fatalf("used = %v; the unpriced turns must not have been counted as anything", ev.Cost.Used)
	}
}

// TestRealZeroCostIsCertain: a day that genuinely cost nothing is not the
// same as a day whose cost is unknown, and that distinction has to
// survive into the budget projection.
func TestRealZeroCostIsCertain(t *testing.T) {
	ev := Evaluate(Budget{DailyCostLimitUSD: usd(2.0)},
		DailyUsage{KnownCostUSD: 0, UnpricedTurns: 0}, turn(10, 10), priced())

	if !ev.Certain {
		t.Fatalf("a fully priced day at zero cost reported as uncertain")
	}
	if ev.Blocked {
		t.Fatalf("a day that cost nothing was blocked: %+v", ev)
	}
}

/* ── independence ────────────────────────────────────────────────────── */

// TestDimensionsAreIndependent: four configurations, and each reports only
// what it configured.
func TestDimensionsAreIndependent(t *testing.T) {
	u := DailyUsage{Tokens: 10, KnownCostUSD: 0.01}

	none := Evaluate(Budget{}, u, turn(1, 1), priced())
	if none.Tokens != nil || none.Cost != nil {
		t.Fatalf("no limits reported dimensions")
	}

	onlyTokens := Evaluate(Budget{DailyTokenLimit: tokens(100)}, u, turn(1, 1), priced())
	if onlyTokens.Tokens == nil || onlyTokens.Cost != nil {
		t.Fatalf("a token-only budget reported a cost dimension")
	}

	onlyCost := Evaluate(Budget{DailyCostLimitUSD: usd(1)}, u, turn(1, 1), priced())
	if onlyCost.Tokens != nil || onlyCost.Cost == nil {
		t.Fatalf("a cost-only budget reported a token dimension")
	}

	both := Evaluate(Budget{DailyTokenLimit: tokens(100), DailyCostLimitUSD: usd(1)}, u, turn(1, 1), priced())
	if both.Tokens == nil || both.Cost == nil {
		t.Fatalf("a full budget did not report both dimensions")
	}
}

func TestBudgetValidate(t *testing.T) {
	if err := (Budget{DailyTokenLimit: tokens(-1)}).Validate(); err == nil {
		t.Fatalf("a negative token limit was accepted")
	}
	if err := (Budget{DailyCostLimitUSD: usd(-0.5)}).Validate(); err == nil {
		t.Fatalf("a negative cost limit was accepted")
	}
	if err := (Budget{DailyTokenLimit: tokens(0), DailyCostLimitUSD: usd(0)}).Validate(); err != nil {
		t.Fatalf("zero is a legitimate limit: %v", err)
	}
	if err := (Budget{}).Validate(); err != nil {
		t.Fatalf("no limits is legitimate: %v", err)
	}
}
