//go:build integration

// R2 · the streaming route, under the middleware production actually runs.
//
// ══════════════════════════════════════════════════════════════════════
//
//	LONG-LIVED CHAT STREAMS MUST NOT INHERIT THE GENERIC REQUEST DEADLINE
//
// ══════════════════════════════════════════════════════════════════════
//
// ── The blind spot this file closes ────────────────────────────────────
// Every other test in this suite mounts a bare `chi.NewRouter()`. The
// production stack — request id, access log, recovery and the generic
// request deadline — was therefore never in the chain, and a 30-second
// deadline killed 14 real turns over two weeks without a single test being
// able to see it. R1 found the gap; this file is the part that would have
// failed.
//
// ── Why it does not wait thirty seconds ────────────────────────────────
// The composition is production's and only the DURATION is a parameter:
// `httpserver.NewRouterWithTimeout` is what `NewRouter` calls, and
// `NewRouter` passes `DefaultRequestTimeout`. The thirty is pinned in
// platform/httpserver/timeout_test.go; what is pinned here is that the
// streaming route does not inherit whatever the number is, and that an
// ordinary route beside it still does.
//
// Every test below FAILS against the pre-R2 router, where the deadline
// reached every route.

package chat

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/corsi/backend/internal/chat/domain"
	"github.com/corsi/backend/internal/chat/ports"
	"github.com/corsi/backend/internal/platform/httpserver"
)

// streamBudget is the generic deadline the production stack is composed
// with for these tests. Small enough to be outlived deliberately, large
// enough that a slow database does not decide the outcome.
const streamBudget = 300 * time.Millisecond

// aSlowTurn scripts an answer that takes longer to produce than the generic
// request deadline allows.
//
// The wait is inside the provider fake, between two deltas, which is where
// a real model's thinking time lands: the stream is open, tokens have
// already been delivered, and the turn is doing exactly what it is supposed
// to do when the clock used to kill it.
func aSlowTurn(e *env, overrun time.Duration) {
	e.llm.script = []ports.StreamEvent{
		{Delta: "Primeiro pedaço da resposta."},
		{Delta: " E o resto, depois de pensar um bom tempo."},
		{FinishReason: "stop", Usage: &ports.Usage{PromptTokens: 50, CompletionTokens: 20}},
	}
	e.llm.beforeEvent = func(i int) {
		if i == 1 {
			time.Sleep(overrun)
		}
	}
}

/* ── A · the streaming route outlives the generic deadline ───────────── */

// The regression, stated as plainly as it can be.
//
// A turn that takes longer than the generic request deadline finishes, in
// full, and is recorded as having finished. Before R2 this same turn came
// back `aborted` with half an answer.
func TestAStreamingTurnOutlivesTheGenericRequestDeadline(t *testing.T) {
	e := newEnv(t, withProductionRouter(streamBudget))
	s := e.seed(e.wsA)
	aSlowTurn(e, streamBudget*2)

	started := time.Now()
	rec := e.do("POST", "/chat/conversations/"+s.conversationID+"/messages", e.wsA,
		map[string]any{"content": "leva o tempo que precisar"})
	wantStatus(t, rec, http.StatusOK)

	// The turn really did outlive the deadline; otherwise this asserts
	// nothing at all.
	if elapsed := time.Since(started); elapsed < streamBudget {
		t.Fatalf("the turn took %s, under the %s deadline — it was never at risk", elapsed, streamBudget)
	}

	msgs := e.messages(e.wsA, s.conversationID)
	if len(msgs) != 2 {
		t.Fatalf("transcript has %d messages, want the question and the answer", len(msgs))
	}
	turn := msgs[1]
	if turn.FinishReason != string(domain.FinishStop) {
		t.Fatalf("finish_reason = %q, want %q: a turn that simply took its time is not an interruption",
			turn.FinishReason, domain.FinishStop)
	}
	if turn.Content != "Primeiro pedaço da resposta. E o resto, depois de pensar um bom tempo." {
		t.Fatalf("assistant content = %q, want the whole answer", turn.Content)
	}
	// It ran to the end, so the gateway's closing frame arrived and the
	// usage is measured rather than estimated. The live incident's
	// signature, inverted.
	if turn.UsageSource != string(domain.UsageProvider) {
		t.Fatalf("usage_source = %q, want %q", turn.UsageSource, domain.UsageProvider)
	}
}

// The same property for the other streaming route. A resume is a paid
// provider call that streams for as long as a turn does, and a fix that
// covered only `/messages` would leave the continuation of an interrupted
// turn to be interrupted by the same clock.
func TestAResumeAlsoOutlivesTheGenericRequestDeadline(t *testing.T) {
	e := newEnv(t, withProductionRouter(streamBudget), withExtraTools(countingTool{}))
	s := e.seed(e.wsA)
	e.authorizeTool(e.wsA, s.agentID, countingToolName)
	countingToolRuns.Store(0)

	// A turn that reaches the ceiling, which is the resumable terminal this
	// product has had all along.
	e.llm.rounds = [][]ports.StreamEvent{askTool("call_r", countingToolName, `{"value":"x"}`)}
	wantStatus(t, e.do("POST", "/chat/conversations/"+s.conversationID+"/messages", e.wsA,
		map[string]any{"content": "escreve em loop"}), http.StatusOK)

	interrupted := e.messages(e.wsA, s.conversationID)[1]
	if interrupted.FinishReason != string(domain.FinishToolRoundLimit) {
		t.Fatalf("fixture finish_reason = %q, want the ceiling", interrupted.FinishReason)
	}

	// And a continuation that takes longer than the deadline.
	e.llm.rounds = nil
	aSlowTurn(e, streamBudget*2)

	started := time.Now()
	rec := e.do("POST", "/chat/conversations/"+s.conversationID+"/resume", e.wsA,
		map[string]any{"message_id": interrupted.ID})
	wantStatus(t, rec, http.StatusOK)

	if elapsed := time.Since(started); elapsed < streamBudget {
		t.Fatalf("the resume took %s, under the %s deadline — it was never at risk", elapsed, streamBudget)
	}
	msgs := e.messages(e.wsA, s.conversationID)
	if last := msgs[len(msgs)-1]; last.FinishReason != string(domain.FinishStop) {
		t.Fatalf("the continuation ended %q, want %q", last.FinishReason, domain.FinishStop)
	}
}

/* ── B · ordinary routes still get the deadline ──────────────────────── */

// The exemption is two routes, not the product.
//
// `DO NOT globally remove HTTP protection merely to fix chat` — so an
// ordinary chat route, under the same router, in the same test, must still
// carry the generic deadline. Asserted on the context the handler sees,
// because an ordinary chat route is fast and would never hit the clock on
// its own.
func TestAnOrdinaryChatRouteStillCarriesTheGenericDeadline(t *testing.T) {
	e := newEnv(t, withProductionRouter(streamBudget))

	var (
		plainHasDeadline  bool
		plainBudget       time.Duration
		streamHasDeadline = true
	)
	// Two probes mounted on the SAME production router the chat module is
	// on: one ordinary, one under the very middleware the streaming routes
	// opt into. Registered before anything is served, and compared with
	// each other, because the claim is a DIFFERENCE between two routes of
	// one router rather than a property of either alone.
	e.r.Get("/probe/plain", func(_ http.ResponseWriter, r *http.Request) {
		d, ok := r.Context().Deadline()
		plainHasDeadline, plainBudget = ok, time.Until(d)
	})
	e.r.With(httpserver.WithoutRequestDeadline).
		Get("/probe/stream", func(_ http.ResponseWriter, r *http.Request) {
			_, streamHasDeadline = r.Context().Deadline()
		})

	s := e.seed(e.wsA)
	e.do("GET", "/probe/plain", e.wsA, nil)
	e.do("GET", "/probe/stream", e.wsA, nil)

	if !plainHasDeadline {
		t.Fatal("an ordinary route lost the generic deadline: the fix removed protection instead of scoping it")
	}
	if plainBudget <= 0 || plainBudget > streamBudget {
		t.Fatalf("the ordinary route's budget is %s, want at most %s", plainBudget, streamBudget)
	}
	if streamHasDeadline {
		t.Fatal("an exempted route still carries a deadline")
	}

	// And the real ordinary chat routes still answer normally under it.
	wantStatus(t, e.do("GET", "/chat/conversations/"+s.conversationID+"/messages", e.wsA, nil),
		http.StatusOK)
}

/* ── C · cancellation still reaches the runtime ──────────────────────── */

// ══════════════════════════════════════════════════════════════════════
//
//	REMOVING THE DEADLINE MUST NOT MAKE TURNS IMMORTAL
//
// ══════════════════════════════════════════════════════════════════════
//
// This is the test that earns the change. WithoutRequestDeadline
// re-sources cancellation rather than dropping it, and a mistake there
// would be invisible in every other test in this file: the turns would
// simply finish, which is what they are supposed to do.
//
// So: the client hangs up, under the production stack, and the turn stops.
func TestClientCancellationStillReachesTheRuntimeUnderTheProductionStack(t *testing.T) {
	e := newEnv(t, withProductionRouter(streamBudget))
	s := e.seed(e.wsA)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	e.llm.script = []ports.StreamEvent{
		{Delta: "primeiro pedaço"},
		{Delta: ", segundo pedaço"},
		{Delta: ", terceiro pedaço que o leitor nunca vê"},
		{FinishReason: "stop", Usage: &ports.Usage{PromptTokens: 10, CompletionTokens: 9}},
	}
	e.llm.beforeEvent = func(i int) {
		if i == 1 {
			cancel()
		}
	}

	_ = e.doCtx(ctx, "POST", "/chat/conversations/"+s.conversationID+"/messages", e.wsA,
		map[string]any{"content": "pode parar"})

	turn := e.messages(e.wsA, s.conversationID)[1]
	if turn.FinishReason != string(domain.FinishAborted) {
		t.Fatalf("finish_reason = %q, want %q: a hung-up client must still end the turn",
			turn.FinishReason, domain.FinishAborted)
	}
	if strings.Contains(turn.Content, "terceiro") {
		t.Fatal("the turn kept pulling tokens after the reader was gone")
	}
	// A cancellation is not a deadline, even through a middleware that
	// re-sources cancellation. See httpserver.WithoutRequestDeadline, which
	// cancels WITH the original cause for exactly this reason.
	if turn.FinishReason == string(domain.FinishDeadline) {
		t.Fatal("a client hang-up was recorded as an infrastructure deadline")
	}
}

/* ── D · the distinction survives the production stack ───────────────── */

// A deadline on the CLIENT's own context — an upstream proxy, a caller with
// a timeout — reaches the runtime as a deadline and not as a cancellation,
// even though WithoutRequestDeadline re-sources cancellation in between.
//
// This is the assertion that makes `context.Cause` load-bearing rather than
// decorative: with a plain WithCancel it fails, and the R1 collapse is back
// one layer up.
func TestAnUpstreamDeadlineIsStillADeadlineUnderTheProductionStack(t *testing.T) {
	e := newEnv(t, withProductionRouter(time.Hour))
	s := e.seed(e.wsA)

	// The router's own budget is an hour, so nothing it does can be the
	// cause. The deadline is the caller's.
	const callerBudget = 300 * time.Millisecond
	aSlowTurn(e, callerBudget*2)

	ctx, cancel := context.WithTimeout(context.Background(), callerBudget)
	defer cancel()
	_ = e.doCtx(ctx, "POST", "/chat/conversations/"+s.conversationID+"/messages", e.wsA,
		map[string]any{"content": "o cliente tem pressa"})

	turn := e.messages(e.wsA, s.conversationID)[1]
	if turn.FinishReason != string(domain.FinishDeadline) {
		t.Fatalf("finish_reason = %q, want %q: the cause was a deadline and must survive the exemption",
			turn.FinishReason, domain.FinishDeadline)
	}
}
