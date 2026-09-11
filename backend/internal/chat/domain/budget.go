package domain

import "time"

// Daily budget for one agent: how much it may consume, and how close it is.
//
// ── What this file decides, and what it does not ───────────────────────
// Everything here is a pure function of numbers that were measured
// elsewhere. It does not read the database, does not call the gateway, and
// does not know what a turn is. That is what makes the whole enforcement
// policy testable without either.
//
// ── The promise, stated exactly ────────────────────────────────────────
// Decision D5 fixes what the product may claim, and it is deliberately not
// "you will never exceed your limit":
//
//	budget still available  →  the turn runs
//	budget already reached  →  the turn is refused, before the provider
//
// A turn that starts inside the limit may finish outside it, because what
// it will consume is not knowable until it has. The overshoot is therefore
// bounded by one turn's ceiling, and that ceiling is computed and reported
// (TurnCeiling) rather than left to the imagination.
//
// This is why Evaluate blocks on `used >= limit` and NOT on
// `used + ceiling > limit`. The second reads stricter and is worse: with a
// 4k max_tokens ceiling and a 100k limit it starts refusing turns at 96k,
// so the effective limit silently becomes a number nobody configured. The
// fit check still happens — it is reported as NextTurnFits, and it warns.

// Budget is what an agent is allowed to spend in a day. A nil limit is no
// limit; zero is a limit of zero, which freezes the agent on purpose.
type Budget struct {
	DailyTokenLimit   *int     `json:"daily_token_limit"`
	DailyCostLimitUSD *float64 `json:"daily_cost_limit_usd"`
}

// Enabled reports whether anything at all is being enforced. An agent with
// no limits must behave exactly as it did before budgets existed, and this
// is the check that guarantees the gate does no work for it.
func (b Budget) Enabled() bool {
	return b.DailyTokenLimit != nil || b.DailyCostLimitUSD != nil
}

func (b Budget) Validate() error {
	if b.DailyTokenLimit != nil && *b.DailyTokenLimit < 0 {
		return Invalid("daily_token_limit must be >= 0")
	}
	if b.DailyCostLimitUSD != nil && *b.DailyCostLimitUSD < 0 {
		return Invalid("daily_cost_limit_usd must be >= 0")
	}
	return nil
}

/* ── the day ─────────────────────────────────────────────────────────── */

// UTCDay returns the half-open window [from, to) of the UTC day containing
// t.
//
// ── Why UTC, said plainly ──────────────────────────────────────────────
// The platform has no timezone anywhere: not on a workspace, not on a user,
// not in config. `ports.UsageFilter` documents that omission on purpose and
// makes the HTTP caller supply the window, because the browser is the only
// party that knows where the person is.
//
// A server-side gate has no caller to ask. The options were to invent a
// preference (a Platform capability this batch is not allowed to add), to
// use the server's local zone (which silently follows the deploy region and
// would move everyone's midnight when the host moves), or to pick a fixed
// one and say so.
//
// UTC, declared. A budget day may not line up with the user's calendar day,
// and that is a stated property rather than an accident — the surfaces that
// show it say "UTC" out loud.
func UTCDay(t time.Time) (from, to time.Time) {
	u := t.UTC()
	from = time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
	return from, from.AddDate(0, 0, 1)
}

/* ── the inputs ──────────────────────────────────────────────────────── */

// DailyUsage is what an agent has already consumed inside the window.
//
// It is derived from the turns themselves — the same rows the statement and
// the Inspector read — so there is one number, not a counter that has to be
// kept in step with reality.
type DailyUsage struct {
	Tokens int64
	// KnownCostUSD sums only the turns that recorded a cost. It is a floor,
	// never a total, whenever UnpricedTurns is above zero.
	KnownCostUSD float64
	// UnpricedTurns is how many turns in the window consumed something whose
	// cost is unknown. It is the size of the gap in KnownCostUSD, and it is
	// reported rather than absorbed: adding NULL as zero would tell the user
	// they spent less than we know they did.
	UnpricedTurns int64
}

// TurnCeiling is the most a single turn could possibly consume.
//
// EstimatedPromptTokens is the Context Builder's figure, which is an
// estimate. MaxOutputTokens is `agent.max_tokens`, which is not: the domain
// requires it to be 1..200000, it is sent to the gateway on every request,
// and the gateway will not exceed it. So the output side of this ceiling is
// a real ceiling, and only the input side is approximate.
type TurnCeiling struct {
	EstimatedPromptTokens int
	MaxOutputTokens       int
}

func (c TurnCeiling) Tokens() int64 {
	return int64(c.EstimatedPromptTokens) + int64(c.MaxOutputTokens)
}

// CostUSD is the ceiling in money, given a rate card. Nil price means the
// turn cannot be priced at all — which is a different answer from zero, and
// Evaluate treats it as such.
func (c TurnCeiling) CostUSD(inputPerToken, outputPerToken float64) float64 {
	return float64(c.EstimatedPromptTokens)*inputPerToken +
		float64(c.MaxOutputTokens)*outputPerToken
}

/* ── the outputs ─────────────────────────────────────────────────────── */

// BudgetState is how one dimension is doing against its limit.
//
// Four values, not six. "Critical" and "unknown" were considered and
// rejected as states because they are not alternatives to the others:
// uncertainty about cost can coexist with any of these, so it is carried
// beside them as BudgetEvaluation.Certain rather than folded in. A state
// machine whose values can be simultaneously true is not a state machine.
type BudgetState string

const (
	// BudgetDisabled: no limit configured. Nothing is enforced or shown.
	BudgetDisabled BudgetState = "disabled"
	BudgetOK       BudgetState = "ok"
	// BudgetWarning: at or past the warning threshold, still running.
	BudgetWarning BudgetState = "warning"
	// BudgetBlocked: the limit is reached. The next turn is refused.
	BudgetBlocked BudgetState = "blocked"
)

// WarningThreshold is where a dimension starts saying something without
// stopping anything. 80% is the figure the approved design fixed
// (usage-and-budgets.md §4.3); it is a presentation threshold and never a
// rule — the only rule is used versus limit.
const WarningThreshold = 0.8

// BudgetBlockReason says which rule refused a turn, in a form a client can
// branch on without parsing prose.
type BudgetBlockReason string

const (
	BlockNone BudgetBlockReason = ""
	// BlockTokens: the daily token limit is reached.
	BlockTokens BudgetBlockReason = "token_limit_reached"
	// BlockCost: the daily spend limit is reached, on known cost alone.
	BlockCost BudgetBlockReason = "cost_limit_reached"
	// BlockPricingUnavailable: a spend limit is configured and the next turn
	// cannot be priced, so the rule cannot be applied. See Evaluate.
	BlockPricingUnavailable BudgetBlockReason = "pricing_unavailable"
)

// BudgetDimension is one limit and how far into it the day has gone.
type BudgetDimension struct {
	Limit     float64     `json:"limit"`
	Used      float64     `json:"used"`
	Remaining float64     `json:"remaining"`
	Percent   float64     `json:"percent"`
	State     BudgetState `json:"state"`
}

// BudgetEvaluation is the whole answer: whether the turn may run, why not,
// and where each dimension stands.
type BudgetEvaluation struct {
	Enabled bool              `json:"enabled"`
	Blocked bool              `json:"blocked"`
	Reason  BudgetBlockReason `json:"reason,omitempty"`
	// Tokens and Cost are nil when that dimension has no limit.
	Tokens *BudgetDimension `json:"tokens,omitempty"`
	Cost   *BudgetDimension `json:"cost,omitempty"`
	// UnpricedTurns is carried up so no surface has to pretend the money
	// figure is complete when it is not.
	UnpricedTurns int64 `json:"unpriced_turns"`
	// Certain is false when the window contains turns whose cost is unknown.
	// It does not block: the gap is historical and bounded, and refusing
	// every turn for the rest of the day because one earlier turn went
	// unpriced would be a self-inflicted outage. It is surfaced instead.
	Certain bool `json:"certain"`
	// NextTurnFits reports whether the ceiling the caller described would
	// still fit inside every configured limit.
	//
	// Informational, by D5: a false here warns, it never refuses.
	//
	// Read it as a one-sided answer, because the ceiling the gate can
	// describe is one-sided. `agent.max_tokens` is a real ceiling — the
	// gateway will not exceed it — but the prompt side is not known before
	// the question is written, so the gate passes zero for it. So:
	//
	//	false  →  the next turn will certainly overshoot
	//	true   →  it may still overshoot, on the input side
	//
	// Anything stronger would need the context, and building the context
	// needs the question persisted — which is exactly what a refusal is
	// supposed to avoid.
	NextTurnFits *bool `json:"next_turn_fits,omitempty"`
}

/* ── the rule ────────────────────────────────────────────────────────── */

// Pricing is what the gate knows about the cost of the next turn.
//
// Available false is not "the price is zero". It is "we asked and could not
// find out", which is the case Evaluate refuses on when money is being
// enforced.
type Pricing struct {
	Available      bool
	InputPerToken  float64
	OutputPerToken float64
}

// Evaluate applies the budget rules. Pure, and the only place they live.
//
// Order matters: tokens are checked before cost, because the token rule
// holds without a rate card and the cost rule does not. An agent whose
// gateway is unreachable still has a working token limit.
func Evaluate(b Budget, u DailyUsage, ceiling TurnCeiling, p Pricing) BudgetEvaluation {
	ev := BudgetEvaluation{
		Enabled:       b.Enabled(),
		UnpricedTurns: u.UnpricedTurns,
		Certain:       u.UnpricedTurns == 0,
	}
	if !ev.Enabled {
		return ev
	}

	fits := true

	if b.DailyTokenLimit != nil {
		limit := float64(*b.DailyTokenLimit)
		dim := dimension(limit, float64(u.Tokens))
		ev.Tokens = &dim
		if dim.State == BudgetBlocked {
			ev.Blocked, ev.Reason = true, BlockTokens
		}
		if float64(u.Tokens)+float64(ceiling.Tokens()) > limit {
			fits = false
		}
	}

	if b.DailyCostLimitUSD != nil {
		limit := *b.DailyCostLimitUSD
		dim := dimension(limit, u.KnownCostUSD)
		ev.Cost = &dim
		if dim.State == BudgetBlocked && !ev.Blocked {
			ev.Blocked, ev.Reason = true, BlockCost
		}

		// A spend limit that cannot be applied is not a spend limit. If the
		// next turn has no price, letting it through would mean the limit
		// silently stops existing exactly when the gateway is having a bad
		// day — which is when a runaway turn is most likely.
		//
		// Fail-closed, and only here: the token limit above is untouched by
		// this, so an agent that wants a guarantee independent of the rate
		// card gets one by setting tokens.
		if !p.Available && !ev.Blocked {
			ev.Blocked, ev.Reason = true, BlockPricingUnavailable
		}
		if p.Available && u.KnownCostUSD+ceiling.CostUSD(p.InputPerToken, p.OutputPerToken) > limit {
			fits = false
		}
	}

	ev.NextTurnFits = &fits
	return ev
}

func dimension(limit, used float64) BudgetDimension {
	d := BudgetDimension{Limit: limit, Used: used, Remaining: limit - used}
	if d.Remaining < 0 {
		d.Remaining = 0
	}
	switch {
	case limit <= 0:
		// A limit of zero is reached the moment it exists. Percent would be
		// a division by zero, so it is reported as fully consumed, which is
		// what it is.
		d.Percent = 1
		d.State = BudgetBlocked
	default:
		d.Percent = used / limit
		switch {
		case used >= limit:
			d.State = BudgetBlocked
		case d.Percent >= WarningThreshold:
			d.State = BudgetWarning
		default:
			d.State = BudgetOK
		}
	}
	return d
}
