package app

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/chat/domain"
	"github.com/corsi/backend/internal/chat/ports"
)

// Budget enforcement: the application half of domain/budget.go.
//
// The rules themselves are pure and live in the domain. This file does the
// two things the domain cannot: read what the agent has consumed today, and
// find out what the next turn would cost.
//
// ── Where the numbers come from ────────────────────────────────────────
// From the turns. `AgentUsage` over a one-day window is the same
// aggregation the statement and the Agent's usage page read, with the same
// treatment of unpriced turns — so there is one truth about what an agent
// spent, not a gate that keeps its own tally beside it.
//
// ── What an agent without limits costs ─────────────────────────────────
// Nothing. `Budget.Enabled()` is checked before any query runs, so a turn
// for an unlimited agent issues exactly the statements it issued before
// this file existed. That is asserted by TestTurnWithoutBudgetIsUnchanged.

// BudgetStatus is the configuration and the current standing, together.
//
// They are returned as one document because every surface that shows one
// needs the other — a limit without consumption is a number, and
// consumption without a limit is trivia — but they stay separate objects
// inside it, because only one of them is editable.
type BudgetStatus struct {
	Budget domain.Budget           `json:"budget"`
	Window BudgetWindow            `json:"window"`
	Usage  BudgetUsage             `json:"usage"`
	Status domain.BudgetEvaluation `json:"status"`
}

// BudgetWindow is the day being measured, echoed back so no reader has to
// guess which midnight this was.
type BudgetWindow struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
	// Timezone is always UTC in v1, and is stated rather than assumed. See
	// domain.UTCDay.
	Timezone string `json:"timezone"`
}

// BudgetUsage is what the window actually contains, limits aside.
type BudgetUsage struct {
	Tokens int64 `json:"tokens"`
	// KnownCostUSD is a floor whenever UnpricedTurns is above zero.
	KnownCostUSD  float64 `json:"known_cost_usd"`
	UnpricedTurns int64   `json:"unpriced_turns"`
	Turns         int64   `json:"turns"`
}

// AgentBudget reports where an agent stands today.
//
// `NextTurnFits` here is computed from the only side of a turn that can be
// known without a question: `agent.max_tokens`, the ceiling the gateway
// will not exceed. So a false means "not even the reply would fit", which
// is exactly the case worth warning about before someone types.
func (s *Service) AgentBudget(ctx context.Context, workspaceID, agentID uuid.UUID) (*BudgetStatus, error) {
	agent, err := s.repos.Agents.FindByID(ctx, workspaceID, agentID)
	if err != nil {
		return nil, err
	}
	usage, window, err := s.dailyUsage(ctx, workspaceID, agentID)
	if err != nil {
		return nil, err
	}

	// Pricing is reported as available when no cost limit is configured,
	// because the question does not arise. Asking the gateway here would be
	// a round trip to answer nothing.
	pricing := domain.Pricing{Available: true}
	if agent.Budget.DailyCostLimitUSD != nil {
		pricing = s.pricingFor(ctx, agent)
	}

	return &BudgetStatus{
		Budget: agent.Budget,
		Window: window,
		Usage: BudgetUsage{
			Tokens:        usage.Tokens,
			KnownCostUSD:  usage.KnownCostUSD,
			UnpricedTurns: usage.UnpricedTurns,
			Turns:         usage.turns,
		},
		Status: domain.Evaluate(agent.Budget, usage.DailyUsage,
			domain.TurnCeiling{MaxOutputTokens: agent.MaxTokens}, pricing),
	}, nil
}

// dailyUsageResult carries the domain value plus the one field only the
// status endpoint shows.
type dailyUsageResult struct {
	domain.DailyUsage
	turns int64
}

// dailyUsage reads what the agent consumed inside the current UTC day.
//
// It goes through AgentUsage, not a query of its own, so the gate and the
// statement can never disagree about what a day contains or about how an
// unpriced turn is counted.
func (s *Service) dailyUsage(ctx context.Context, workspaceID, agentID uuid.UUID) (dailyUsageResult, BudgetWindow, error) {
	from, to := domain.UTCDay(time.Now())
	window := BudgetWindow{From: from, To: to, Timezone: "UTC"}

	rows, err := s.repos.Messages.UsageByAgent(ctx, workspaceID, agentID,
		ports.UsageFilter{From: &from, To: &to})
	if err != nil {
		return dailyUsageResult{}, window, err
	}
	report := buildUsageReport(rows, ports.UsageFilter{From: &from, To: &to})

	return dailyUsageResult{
		DailyUsage: domain.DailyUsage{
			Tokens:        report.TotalTokens,
			KnownCostUSD:  report.EstimatedCost,
			UnpricedTurns: report.UnpricedMessages,
		},
		turns: report.Messages,
	}, window, nil
}

// pricingFor asks the gateway what the agent's model costs.
//
// Only called when a spend limit is configured. The price it finds is
// handed back to the turn so the post-turn lookup does not repeat it — a
// budgeted agent therefore makes the same number of gateway round trips as
// an unbudgeted one, not one more. See SendMessage.
func (s *Service) pricingFor(ctx context.Context, agent *domain.Agent) domain.Pricing {
	provider, err := s.repos.Providers.FindByID(ctx, agent.WorkspaceID, agent.ProviderID)
	if err != nil {
		return domain.Pricing{}
	}
	creds, err := s.credentialsFor(provider)
	if err != nil {
		return domain.Pricing{}
	}
	return pricingFrom(s.priceFor(ctx, creds, agent.Model))
}

func pricingFrom(p *ports.Price) domain.Pricing {
	if p == nil {
		return domain.Pricing{}
	}
	return domain.Pricing{
		Available:      true,
		InputPerToken:  p.InputCostPerToken,
		OutputPerToken: p.OutputCostPerToken,
	}
}

/* ── the gate ────────────────────────────────────────────────────────── */

// turnGate is the budget rule applied to a turn that may call the provider
// more than once.
//
// ── Why this type exists ───────────────────────────────────────────────
// The budget used to be evaluated once, because one turn was one call. Tools
// break that identity: a turn is now one call per tool round plus one to
// answer, and a limit checked only before the first of them would be a
// limit that tool calling walks straight around.
//
// So the gate is a value that survives the turn, and it is consulted before
// every additional call.
//
// ── The one thing that had to be added, and why ────────────────────────
// Consumption is derived from persisted turns (see dailyUsage), and this
// turn is not persisted until it ends. Re-reading the database between
// rounds would therefore return the same number every time and let a turn
// spend its whole budget in round two. `spent` is what this turn has
// consumed so far, held in memory, added to what the day already contained.
//
// ── What it still does not promise ─────────────────────────────────────
// Exactly what D5 promised and no more: a call that starts inside the limit
// may finish outside it. The overshoot is bounded by ONE provider call, not
// by the whole turn — which is the property this type exists to preserve.
type turnGate struct {
	budget  domain.Budget
	usage   domain.DailyUsage
	pricing domain.Pricing
	ceiling domain.TurnCeiling
	// price is the rate card the preflight read, handed on so the turn's
	// accounting does not read it a second time.
	price *ports.Price
	// est is the calibrated token estimator for this agent's model. It is
	// what the gate falls back on for a call whose usage frame never
	// arrived — the one path where a budget decision rests on a prediction
	// rather than on the provider's own number.
	est TokenEstimator
	// spent is what this turn has consumed so far, across the calls it has
	// already made and which nothing has written down yet.
	spentTokens int64
	spentCost   float64
}

// evaluate applies the rules to the day plus what this turn has spent.
//
// For an agent with no limits this is a pure function over zero values and
// returns a disabled evaluation: no query, no allocation, nothing.
func (g *turnGate) evaluate() domain.BudgetEvaluation {
	if !g.budget.Enabled() {
		return domain.BudgetEvaluation{}
	}
	used := domain.DailyUsage{
		Tokens:        g.usage.Tokens + g.spentTokens,
		KnownCostUSD:  g.usage.KnownCostUSD + g.spentCost,
		UnpricedTurns: g.usage.UnpricedTurns,
	}
	return domain.Evaluate(g.budget, used, g.ceiling, g.pricing)
}

// record folds one completed provider call into what the turn has spent.
//
// It uses the same accounting rules the turn will be persisted with, not a
// parallel tally: the tokens are the provider's where the provider reported
// them and the estimate otherwise, and the cost is those tokens at the rate
// card the preflight already read. A gate that counted differently from the
// statement would eventually disagree with it.
//
// A call that never opened records nothing. There is no measurement to make
// and inventing one would let a failed round eat a budget.
func (g *turnGate) record(round providerRound, usage *ports.Usage) {
	if !g.budget.Enabled() || !round.Opened {
		return
	}
	acct := newTurnAccounting(accountingInput{
		StreamOpened:          true,
		Usage:                 usage,
		Price:                 g.price,
		EstimatedPromptTokens: round.EstimatedPromptTokens,
		Content:               round.Content,
		Reasoning:             round.Reasoning,
		Estimator:             g.est,
	})
	g.spentTokens += int64(acct.PromptTokens) + int64(acct.CompletionTokens)
	if acct.Cost != nil {
		g.spentCost += *acct.Cost
	}
}

// budgetPreflight decides whether a turn may run, before anything is
// written and before the provider is called, and returns the gate that will
// decide the same thing again before every further call.
func (s *Service) budgetPreflight(
	ctx context.Context,
	agent *domain.Agent,
	creds ports.Credentials,
	ceiling domain.TurnCeiling,
) (*turnGate, error) {
	// The fast path, and the one that matters most: an agent with no limits
	// does no extra work at all.
	if !agent.Budget.Enabled() {
		return &turnGate{}, nil
	}

	usage, _, err := s.dailyUsage(ctx, agent.WorkspaceID, agent.ID)
	if err != nil {
		// The gate could not read what the agent has spent. Unlike memory
		// and sources, this does NOT degrade into carrying on: a limit that
		// silently stops applying when a query fails is not a limit. The
		// turn is refused and the reason says so.
		s.log.Error("budget preflight: could not read daily usage",
			"agent_id", agent.ID, "err", err)
		return nil, domain.BudgetExceeded(
			string(domain.BlockPricingUnavailable),
			"the daily usage of this agent could not be read, so its budget could not be checked; the turn was not sent")
	}

	var price *ports.Price
	pricing := domain.Pricing{Available: true}
	if agent.Budget.DailyCostLimitUSD != nil {
		price = s.priceFor(ctx, creds, agent.Model)
		pricing = pricingFrom(price)
	}

	return &turnGate{
		budget:  agent.Budget,
		usage:   usage.DailyUsage,
		pricing: pricing,
		ceiling: ceiling,
		price:   price,
		// Calibrated to this agent's model, and deliberately biased high:
		// this is the number the gate falls back on when a usage frame never
		// arrives, and a limit computed from an optimistic estimate is a
		// limit that does not hold. See the calibration note in context.go.
		est: EstimatorFor(agent.Model),
	}, nil
}

// budgetRefusal turns a blocked evaluation into the error the transport
// answers with.
//
// The message is written to be acted on rather than merely understood: it
// names the limit, what has been used against it, and the way out. The code
// beside it is what a client branches on.
func budgetRefusal(ev domain.BudgetEvaluation) error {
	switch ev.Reason {
	case domain.BlockTokens:
		return domain.BudgetExceeded(string(ev.Reason), fmt.Sprintf(
			"this agent has used %s of its %s daily token limit (UTC). Raise or remove the limit in Settings, or wait for the next day",
			formatTokens(ev.Tokens.Used), formatTokens(ev.Tokens.Limit)))
	case domain.BlockCost:
		return domain.BudgetExceeded(string(ev.Reason), fmt.Sprintf(
			"this agent has spent $%.4f of its $%.2f daily limit (UTC). Raise or remove the limit in Settings, or wait for the next day",
			ev.Cost.Used, ev.Cost.Limit))
	case domain.BlockPricingUnavailable:
		return domain.BudgetExceeded(string(ev.Reason),
			"this agent has a daily spending limit, and the price of its model could not be read, so the limit could not be applied. The turn was not sent. Remove the spending limit to keep working, or use a daily token limit, which does not depend on the price list")
	default:
		return nil
	}
}

func formatTokens(n float64) string {
	if n >= 1000 {
		return fmt.Sprintf("%.1fk", n/1000)
	}
	return fmt.Sprintf("%.0f", n)
}
