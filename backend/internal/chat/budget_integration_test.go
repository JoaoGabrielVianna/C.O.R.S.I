//go:build integration

// Integration tests for Budgets.
//
// The rules themselves are unit-tested in domain/budget_test.go, where they
// are pure. What this file asserts is everything the rules cannot: that the
// day is read from real turns, that a refusal happens before the provider
// is touched, that nothing is written when a turn is refused, and that an
// agent with no limits is left completely alone.
//
// The harness lives in chat_integration_test.go.
package chat

import (
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/chat/ports"
)

/* ── helpers ─────────────────────────────────────────────────────────── */

type apiBudgetDimension struct {
	Limit     float64 `json:"limit"`
	Used      float64 `json:"used"`
	Remaining float64 `json:"remaining"`
	Percent   float64 `json:"percent"`
	State     string  `json:"state"`
}

type apiBudgetStatus struct {
	Budget struct {
		DailyTokenLimit   *int     `json:"daily_token_limit"`
		DailyCostLimitUSD *float64 `json:"daily_cost_limit_usd"`
	} `json:"budget"`
	Window struct {
		From     time.Time `json:"from"`
		To       time.Time `json:"to"`
		Timezone string    `json:"timezone"`
	} `json:"window"`
	Usage struct {
		Tokens        int64   `json:"tokens"`
		KnownCostUSD  float64 `json:"known_cost_usd"`
		UnpricedTurns int64   `json:"unpriced_turns"`
		Turns         int64   `json:"turns"`
	} `json:"usage"`
	Status struct {
		Enabled       bool                `json:"enabled"`
		Blocked       bool                `json:"blocked"`
		Reason        string              `json:"reason"`
		Tokens        *apiBudgetDimension `json:"tokens"`
		Cost          *apiBudgetDimension `json:"cost"`
		UnpricedTurns int64               `json:"unpriced_turns"`
		Certain       bool                `json:"certain"`
		NextTurnFits  *bool               `json:"next_turn_fits"`
	} `json:"status"`
}

func (e *env) budget(ws uuid.UUID, agentID string) apiBudgetStatus {
	e.t.Helper()
	rec := e.do("GET", "/chat/agents/"+agentID+"/budget", ws, nil)
	wantStatus(e.t, rec, http.StatusOK)
	return decode[apiBudgetStatus](e.t, rec)
}

// setBudget writes the two limits. A nil member removes that limit.
func (e *env) setBudget(ws uuid.UUID, agentID string, tokenLimit *int, costLimit *float64) {
	e.t.Helper()
	rec := e.do("PATCH", "/chat/agents/"+agentID, ws, map[string]any{
		"budget": map[string]any{
			"daily_token_limit":    tokenLimit,
			"daily_cost_limit_usd": costLimit,
		},
	})
	wantStatus(e.t, rec, http.StatusOK)
}

func intp(n int) *int         { return &n }
func f64p(v float64) *float64 { return &v }

// wantBlocked asserts a send was refused for the given machine-readable
// reason, and — the invariant that matters most — that the provider was
// never called.
func (e *env) wantBlocked(ws uuid.UUID, conversationID, wantCode string) string {
	e.t.Helper()
	before := e.llm.streamCalls

	rec := e.do("POST", "/chat/conversations/"+conversationID+"/messages", ws,
		map[string]any{"content": "isto não deve ser enviado"})
	wantErrorCode(e.t, rec, http.StatusTooManyRequests, wantCode)

	if e.llm.streamCalls != before {
		e.t.Fatalf("a refused turn called the provider %d time(s)", e.llm.streamCalls-before)
	}
	body := decode[struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}](e.t, rec)
	return body.Error.Message
}

/* ── an agent with no budget is untouched ────────────────────────────── */

// TestTurnWithoutBudgetIsUnchanged is the compatibility promise of this
// batch, and the first thing that must remain true.
func TestTurnWithoutBudgetIsUnchanged(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)

	e.send(e.wsA, s.conversationID, "bom dia")

	turns := e.assistantTurns(e.wsA, s.conversationID)
	if len(turns) != 1 || turns[0].Content == "" {
		t.Fatalf("a turn for an unbudgeted agent did not behave normally: %+v", turns)
	}

	status := e.budget(e.wsA, s.agentID)
	if status.Status.Enabled || status.Status.Blocked {
		t.Fatalf("an agent with no limits reports an active budget: %+v", status.Status)
	}
	if status.Status.Tokens != nil || status.Status.Cost != nil {
		t.Fatalf("dimensions were reported for an agent with no limits")
	}
	// Consumption is still readable — the absence of a limit is not the
	// absence of a number.
	if status.Usage.Tokens == 0 {
		t.Fatalf("usage was not reported for an unbudgeted agent")
	}
}

/* ── configuration ───────────────────────────────────────────────────── */

func TestBudgetConfiguration(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)

	t.Run("token only", func(t *testing.T) {
		e.setBudget(e.wsA, s.agentID, intp(5000), nil)
		st := e.budget(e.wsA, s.agentID)
		if st.Budget.DailyTokenLimit == nil || *st.Budget.DailyTokenLimit != 5000 {
			t.Fatalf("token limit = %v", st.Budget.DailyTokenLimit)
		}
		if st.Budget.DailyCostLimitUSD != nil {
			t.Fatalf("a cost limit appeared from nowhere: %v", *st.Budget.DailyCostLimitUSD)
		}
		if st.Status.Tokens == nil || st.Status.Cost != nil {
			t.Fatalf("dimensions do not match the configuration: %+v", st.Status)
		}
	})

	t.Run("money only", func(t *testing.T) {
		e.setBudget(e.wsA, s.agentID, nil, f64p(2.5))
		st := e.budget(e.wsA, s.agentID)
		if st.Budget.DailyTokenLimit != nil {
			t.Fatalf("a token limit survived being removed")
		}
		if st.Budget.DailyCostLimitUSD == nil || *st.Budget.DailyCostLimitUSD != 2.5 {
			t.Fatalf("cost limit = %v", st.Budget.DailyCostLimitUSD)
		}
	})

	t.Run("both", func(t *testing.T) {
		e.setBudget(e.wsA, s.agentID, intp(100), f64p(1))
		st := e.budget(e.wsA, s.agentID)
		if st.Status.Tokens == nil || st.Status.Cost == nil {
			t.Fatalf("both limits configured, both dimensions expected: %+v", st.Status)
		}
	})

	t.Run("removing both goes back to no budget", func(t *testing.T) {
		e.setBudget(e.wsA, s.agentID, nil, nil)
		st := e.budget(e.wsA, s.agentID)
		if st.Status.Enabled {
			t.Fatalf("the budget survived being cleared: %+v", st)
		}
	})

	t.Run("omitting the object leaves the limits alone", func(t *testing.T) {
		e.setBudget(e.wsA, s.agentID, intp(777), nil)
		// A patch that does not mention budget at all.
		rec := e.do("PATCH", "/chat/agents/"+s.agentID, e.wsA, map[string]any{"description": "outra coisa"})
		wantStatus(t, rec, http.StatusOK)

		st := e.budget(e.wsA, s.agentID)
		if st.Budget.DailyTokenLimit == nil || *st.Budget.DailyTokenLimit != 777 {
			t.Fatalf("an unrelated patch cleared the budget: %v", st.Budget.DailyTokenLimit)
		}
	})

	t.Run("negative limits are refused", func(t *testing.T) {
		rec := e.do("PATCH", "/chat/agents/"+s.agentID, e.wsA, map[string]any{
			"budget": map[string]any{"daily_token_limit": -1},
		})
		wantErrorCode(t, rec, http.StatusBadRequest, "invalid")
	})

	t.Run("a budget can be set at creation", func(t *testing.T) {
		rec := e.do("POST", "/chat/agents", e.wsA, map[string]any{
			"provider_id":          s.providerID,
			"name":                 "Com budget " + uuid.NewString()[:8],
			"model":                "test-model",
			"daily_token_limit":    2000,
			"daily_cost_limit_usd": 0.5,
		})
		wantStatus(t, rec, http.StatusCreated)
		created := decode[map[string]any](t, rec)
		st := e.budget(e.wsA, created["id"].(string))
		if st.Budget.DailyTokenLimit == nil || *st.Budget.DailyTokenLimit != 2000 {
			t.Fatalf("the limit did not survive creation: %+v", st.Budget)
		}
	})
}

/* ── the token gate ──────────────────────────────────────────────────── */

// TestTokenGateBlocksWhenTheLimitIsReached drives real turns until the
// limit is reached, then checks the next one is refused before the provider.
func TestTokenGateBlocksWhenTheLimitIsReached(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)
	e.llm.reply("resposta", 400, 100) // 500 tokens per turn

	// A limit of 1200 leaves room for two turns and not three.
	e.setBudget(e.wsA, s.agentID, intp(1200), nil)

	e.send(e.wsA, s.conversationID, "primeira")
	if st := e.budget(e.wsA, s.agentID); st.Status.Blocked {
		t.Fatalf("blocked after one turn of 500 against a 1200 limit: %+v", st.Status)
	}
	e.send(e.wsA, s.conversationID, "segunda")

	// 1000 of 1200 used: past the warning threshold, still running.
	st := e.budget(e.wsA, s.agentID)
	if st.Usage.Tokens != 1000 {
		t.Fatalf("usage = %d tokens, want 1000", st.Usage.Tokens)
	}
	if st.Status.Tokens.State != "warning" {
		t.Fatalf("state = %q at 83%%, want warning", st.Status.Tokens.State)
	}
	if st.Status.Blocked {
		t.Fatalf("blocked below the limit")
	}

	// The third turn takes it past 1200. It is allowed — the budget had not
	// been reached when it started — and that is the overshoot D5 permits.
	e.send(e.wsA, s.conversationID, "terceira")
	st = e.budget(e.wsA, s.agentID)
	if st.Usage.Tokens != 1500 {
		t.Fatalf("usage = %d, want 1500", st.Usage.Tokens)
	}
	if !st.Status.Blocked || st.Status.Reason != "token_limit_reached" {
		t.Fatalf("not blocked after passing the limit: %+v", st.Status)
	}

	// And now the gate refuses, without calling the provider.
	msg := e.wantBlocked(e.wsA, s.conversationID, "token_limit_reached")
	if msg == "" {
		t.Fatalf("the refusal carried no message")
	}

	// Nothing was written: the thread is exactly as it was.
	if got := len(e.messages(e.wsA, s.conversationID)); got != 6 {
		t.Fatalf("transcript has %d messages; a refused turn left something behind", got)
	}
}

// TestTokenGateAtExactlyTheLimit pins the boundary the rule turns on.
func TestTokenGateAtExactlyTheLimit(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)
	e.llm.reply("resposta", 400, 100)

	e.setBudget(e.wsA, s.agentID, intp(500), nil)
	e.send(e.wsA, s.conversationID, "primeira") // exactly 500

	st := e.budget(e.wsA, s.agentID)
	if st.Usage.Tokens != 500 || !st.Status.Blocked {
		t.Fatalf("reaching the limit exactly did not block: %+v", st)
	}
	e.wantBlocked(e.wsA, s.conversationID, "token_limit_reached")
}

// TestZeroTokenLimitFreezesTheAgent: zero is a limit, and it is the only
// configuration that refuses the very first turn.
func TestZeroTokenLimitFreezesTheAgent(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)
	e.setBudget(e.wsA, s.agentID, intp(0), nil)

	e.wantBlocked(e.wsA, s.conversationID, "token_limit_reached")
	if got := len(e.messages(e.wsA, s.conversationID)); got != 0 {
		t.Fatalf("a frozen agent recorded %d messages", got)
	}
}

/* ── the money gate ──────────────────────────────────────────────────── */

func TestCostGateBlocksWhenTheLimitIsReached(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)
	// Rates in the harness: 1e-6 in, 3e-6 out. 1000/1000 = $0.004 per turn.
	e.llm.reply("resposta", 1000, 1000)

	e.setBudget(e.wsA, s.agentID, nil, f64p(0.006))
	e.send(e.wsA, s.conversationID, "primeira") // $0.004

	st := e.budget(e.wsA, s.agentID)
	if !nearly(st.Usage.KnownCostUSD, 0.004) {
		t.Fatalf("known cost = %v, want 0.004", st.Usage.KnownCostUSD)
	}
	if st.Status.Blocked {
		t.Fatalf("blocked at $0.004 of $0.006")
	}

	e.send(e.wsA, s.conversationID, "segunda") // $0.008 total, past the limit
	if st = e.budget(e.wsA, s.agentID); !st.Status.Blocked || st.Status.Reason != "cost_limit_reached" {
		t.Fatalf("not blocked past the cost limit: %+v", st.Status)
	}
	e.wantBlocked(e.wsA, s.conversationID, "cost_limit_reached")
}

// TestCostGateFailsClosedWithoutPricing is the rule that separates a spend
// limit from a decoration: if the next turn cannot be priced, the limit
// cannot be applied, so the turn does not run.
func TestCostGateFailsClosedWithoutPricing(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)
	e.setBudget(e.wsA, s.agentID, nil, f64p(10))

	// The gateway forgets how to price. Nothing has been spent yet.
	e.llm.prices = map[string]ports.Price{}

	msg := e.wantBlocked(e.wsA, s.conversationID, "pricing_unavailable")
	if msg == "" {
		t.Fatalf("the refusal carried no message")
	}
}

// TestTokenGateKeepsWorkingWithoutPricing is the other half: the token
// limit does not depend on a rate card, so a gateway that cannot price must
// not disable it — nor block on its behalf.
func TestTokenGateKeepsWorkingWithoutPricing(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)
	e.llm.reply("resposta", 100, 100)
	e.setBudget(e.wsA, s.agentID, intp(1000), nil)
	e.llm.prices = map[string]ports.Price{}

	// Allowed: tokens are what is limited, and there is room.
	e.send(e.wsA, s.conversationID, "primeira")
	if turns := e.assistantTurns(e.wsA, s.conversationID); len(turns) != 1 {
		t.Fatalf("a token-limited agent was blocked because pricing was unavailable")
	}
	// And the turn is recorded as unpriced, not as free.
	turns := e.assistantTurns(e.wsA, s.conversationID)
	if turns[0].Cost != nil {
		t.Fatalf("a turn with no rate card recorded a cost of %v", *turns[0].Cost)
	}
}

// TestUnpricedHistoryIsSurfacedNotAbsorbed: a day containing a turn nobody
// could price reports the gap instead of hiding it, and does not block on
// it by itself.
func TestUnpricedHistoryIsSurfacedNotAbsorbed(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)
	e.llm.reply("resposta", 1000, 1000)

	// One turn with no rate card: consumed, unpriced.
	e.llm.prices = map[string]ports.Price{}
	e.send(e.wsA, s.conversationID, "sem preço")

	// The rate card comes back, and a limit is set.
	e.llm.prices = map[string]ports.Price{"test-model": {InputCostPerToken: 1e-6, OutputCostPerToken: 3e-6}}
	e.setBudget(e.wsA, s.agentID, nil, f64p(10))

	st := e.budget(e.wsA, s.agentID)
	if st.Usage.UnpricedTurns != 1 {
		t.Fatalf("unpriced turns = %d, want 1", st.Usage.UnpricedTurns)
	}
	if st.Status.Certain {
		t.Fatalf("a window containing an unpriced turn reported itself as certain")
	}
	if st.Usage.KnownCostUSD != 0 {
		t.Fatalf("known cost = %v; the unpriced turn must contribute nothing, not zero-as-a-fact", st.Usage.KnownCostUSD)
	}
	if st.Status.Blocked {
		t.Fatalf("historical uncertainty blocked the agent on its own: %+v", st.Status)
	}
	// And the agent keeps working.
	e.send(e.wsA, s.conversationID, "com preço")
}

/* ── the day ─────────────────────────────────────────────────────────── */

// TestBudgetWindowIsTheUTCDay checks the window the status reports and,
// more importantly, that a turn from outside it does not count.
func TestBudgetWindowIsTheUTCDay(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)
	e.llm.reply("resposta", 400, 100)
	e.setBudget(e.wsA, s.agentID, intp(1000), nil)

	e.send(e.wsA, s.conversationID, "hoje")

	st := e.budget(e.wsA, s.agentID)
	if st.Window.Timezone != "UTC" {
		t.Fatalf("timezone = %q, want UTC stated explicitly", st.Window.Timezone)
	}
	if st.Window.To.Sub(st.Window.From) != 24*time.Hour {
		t.Fatalf("window is %v long", st.Window.To.Sub(st.Window.From))
	}
	if st.Usage.Tokens != 500 {
		t.Fatalf("today = %d tokens, want 500", st.Usage.Tokens)
	}

	// Move that turn to yesterday. It must leave the window entirely.
	if _, err := e.pool.Exec(t.Context(),
		`UPDATE chat.messages SET created_at = created_at - interval '1 day'`); err != nil {
		t.Fatalf("age the turn: %v", err)
	}
	st = e.budget(e.wsA, s.agentID)
	if st.Usage.Tokens != 0 {
		t.Fatalf("yesterday's turn still counts against today: %d tokens", st.Usage.Tokens)
	}
	if st.Status.Blocked {
		t.Fatalf("blocked by consumption outside the window")
	}

	// Exactly at the lower bound: inclusive.
	if _, err := e.pool.Exec(t.Context(),
		`UPDATE chat.messages SET created_at = date_trunc('day', now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'`); err != nil {
		t.Fatalf("move the turn to midnight: %v", err)
	}
	if st = e.budget(e.wsA, s.agentID); st.Usage.Tokens != 500 {
		t.Fatalf("a turn at exactly 00:00 UTC is not counted in its own day: %d", st.Usage.Tokens)
	}
}

/* ── isolation ───────────────────────────────────────────────────────── */

// TestBudgetIsScopedToItsAgentAndWorkspace: consumption never crosses an
// agent boundary, and never crosses a workspace boundary.
func TestBudgetIsScopedToItsAgentAndWorkspace(t *testing.T) {
	e := newEnv(t)
	a := e.seed(e.wsA)
	e.llm.reply("resposta", 400, 100)

	other := e.newAgent(e.wsA, a.providerID, "Outro agente")
	otherConv := e.newConversation(e.wsA, other)

	e.setBudget(e.wsA, a.agentID, intp(1000), nil)
	e.setBudget(e.wsA, other, intp(1000), nil)

	// Two turns on the first agent.
	e.send(e.wsA, a.conversationID, "uma")
	e.send(e.wsA, a.conversationID, "duas")

	if st := e.budget(e.wsA, other); st.Usage.Tokens != 0 {
		t.Fatalf("another agent's consumption leaked in: %d tokens", st.Usage.Tokens)
	}
	// And that agent can still run.
	e.send(e.wsA, otherConv, "eu ainda posso")

	// Workspace B cannot even see the budget of workspace A's agent.
	rec := e.do("GET", "/chat/agents/"+a.agentID+"/budget", e.wsB, nil)
	wantErrorCode(t, rec, http.StatusNotFound, "not_found")

	// Its own agent starts from zero.
	b := e.seed(e.wsB)
	if st := e.budget(e.wsB, b.agentID); st.Usage.Tokens != 0 {
		t.Fatalf("workspace B sees %d tokens of workspace A's consumption", st.Usage.Tokens)
	}
}

/* ── the refusal contract ────────────────────────────────────────────── */

// TestBudgetRefusalIsNotAnUpstreamError: the provider was never called, so
// the failure must not be dressed as the provider's.
func TestBudgetRefusalIsNotAnUpstreamError(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)
	e.setBudget(e.wsA, s.agentID, intp(0), nil)

	rec := e.do("POST", "/chat/conversations/"+s.conversationID+"/messages", e.wsA,
		map[string]any{"content": "pergunta"})

	if rec.Code == http.StatusBadGateway {
		t.Fatalf("a budget refusal was reported as an upstream failure")
	}
	wantErrorCode(t, rec, http.StatusTooManyRequests, "token_limit_reached")
	if e.llm.streamCalls != 0 {
		t.Fatalf("the provider was called %d times for a refused turn", e.llm.streamCalls)
	}
}

// TestBudgetStatusReportsTheFit: the informational signal, which warns and
// never refuses.
func TestBudgetStatusReportsTheFit(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)
	e.llm.reply("resposta", 400, 100)

	// max_tokens defaults to 4096, so a 1000-token limit cannot hold one
	// full turn even when nothing has been spent.
	e.setBudget(e.wsA, s.agentID, intp(1000), nil)

	st := e.budget(e.wsA, s.agentID)
	if st.Status.NextTurnFits == nil {
		t.Fatalf("the fit was not reported")
	}
	// The agent's max_tokens is 4096 by default, which alone exceeds a
	// 1000-token limit. A gate that forgot the output ceiling would report
	// this as fitting.
	if *st.Status.NextTurnFits {
		t.Fatalf("a 4096-token reply was reported as fitting inside a 1000-token limit; the output ceiling was not counted")
	}
	if st.Status.Blocked {
		t.Fatalf("the fit signal refused a turn; it is informational: %+v", st.Status)
	}
	// And the turn really does run.
	e.send(e.wsA, s.conversationID, "mesmo assim")
	if len(e.assistantTurns(e.wsA, s.conversationID)) != 1 {
		t.Fatalf("the turn was refused")
	}
}

/* ── the two layers stay apart ───────────────────────────────────────── */

// TestLocalBudgetNeverConsultsTheGatewayBudget.
//
// There are two budgets and they are not the same thing. The gateway's
// `max_budget` on the virtual key is an external hard stop that protects
// even against our own bugs; the agent's daily limit is an operational
// control inside this system. X1 found the gateway one to be `null` on a
// real key, which is exactly why the local gate must not lean on it.
//
// The proof is negative and structural: a budgeted turn must never call
// /key/info. If it ever did, an unreachable gateway or an unset max_budget
// would start changing what our own limit does.
func TestLocalBudgetNeverConsultsTheGatewayBudget(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)
	e.llm.reply("resposta", 400, 100)
	e.setBudget(e.wsA, s.agentID, intp(1000), f64p(5))

	before := e.llm.keyInfoCalls

	e.send(e.wsA, s.conversationID, "uma pergunta")
	e.budget(e.wsA, s.agentID)
	// And a refused turn, which is where a lazy implementation might reach
	// for the gateway to "double check".
	e.setBudget(e.wsA, s.agentID, intp(0), nil)
	e.wantBlocked(e.wsA, s.conversationID, "token_limit_reached")

	if e.llm.keyInfoCalls != before {
		t.Fatalf("the local budget path called /key/info %d time(s); the two budget layers must stay separate",
			e.llm.keyInfoCalls-before)
	}
}
