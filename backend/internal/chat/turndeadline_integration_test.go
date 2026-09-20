//go:build integration

// R1 diagnosis · R2 correction: the interrupted turn nobody interrupted.
//
// ══════════════════════════════════════════════════════════════════════
//
//	A CLOCK IS NOT A PERSON
//
// ══════════════════════════════════════════════════════════════════════
//
// ── The incident, from the live database ───────────────────────────────
// A Palace turn on 2026-09-20 wrote three memories, streamed 1.661
// characters of answer, and ended. It was persisted with
// `finish_reason = "aborted"`, empty `error`, and `usage_source =
// "estimated"` on its second provider call, because the gateway's closing
// usage frame never arrived. The interface rendered "Resposta
// interrompida." and offered nothing else, so the operator typed
// "Resposta interrompida. continue" into the composer — a NEW QUESTION,
// which is the exact route Safe Resume was built to remove.
//
// It reached the round ceiling: NO. It made two provider calls and ran one
// round of tools. What it did was run for 30.2 seconds, against a
// 30-second deadline the router put on every request in the product.
// Fourteen turns in that database died between 30.2s and 31.3s; not one of
// the 246 turns that finished normally took longer than 30s.
//
// ── What this file asserts after R2 ────────────────────────────────────
// R1's version of this file pinned the defect: a deadline and a user stop
// were persisted identically, and neither could be continued. Both
// assertions are now INVERTED, because both were the defect:
//
//	a deadline        →  "deadline"  →  resumable  →  Continue offered
//	the user stopping →  "aborted"   →  not resumable
//
// The reproduction itself is unchanged. What changed is the runtime, and
// these are the same inputs producing different, correct, records.
//
// The thirty seconds are pinned in platform/httpserver/timeout_test.go,
// where the decision lives; the route no longer inheriting them is pinned
// in turnstream_integration_test.go, under the production stack.

package chat

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/corsi/backend/internal/chat/app"
	"github.com/corsi/backend/internal/chat/domain"
	"github.com/corsi/backend/internal/chat/ports"
)

// confidentialPayload is the argument the fixtures send to the write
// capability, and the string the confidentiality assertions look for.
//
// Deliberately synthetic and deliberately unmistakable. The incident that
// produced this file happened inside a private conversation, and a fixture
// has no use for what was said in it: everything these tests need is
// metadata — counts, timings and terminal reasons — which is the vocabulary
// R1 established as the safe one. A value nobody would ever write by
// accident is also the better leak detector.
const confidentialPayload = "carga-confidencial-que-nunca-pode-voltar"

// deadlineBudget is how long the reproduced request is allowed to take.
// Generous on purpose: what has to be deterministic is that the SECOND
// provider call outlives it, not that the first one is fast.
const deadlineBudget = time.Second

// runInterruptedByDeadline drives the incident's exact shape: a write
// executes, the answer starts streaming, and the request's deadline expires
// between two deltas.
//
// The overrun is scripted from inside the provider fake rather than timed
// from outside, so the test never races its own clock.
func runInterruptedByDeadline(t *testing.T, e *env, s seeded) {
	t.Helper()

	e.llm.rounds = [][]ports.StreamEvent{
		// Round 1 — the model asks to change something, and it really runs.
		askTool("call_deadline_1", countingToolName, `{"value":"`+confidentialPayload+`"}`),
		// Round 2 — the answer. The closing frame is never delivered.
		{
			{Delta: "Primeira parte da resposta."},
			{Delta: " E a segunda parte, que o leitor nunca recebe."},
			{FinishReason: "stop", Usage: &ports.Usage{PromptTokens: 120, CompletionTokens: 40}},
		},
	}
	e.llm.beforeEvent = func(i int) {
		// The overrun, placed after the first delta of the second call so
		// the turn is mid-answer with a write already behind it.
		if e.llm.streamCalls == 2 && i == 1 {
			time.Sleep(deadlineBudget + 200*time.Millisecond)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), deadlineBudget)
	defer cancel()

	_ = e.doCtx(ctx, "POST", "/chat/conversations/"+s.conversationID+"/messages", e.wsA,
		map[string]any{"content": "faz a alteração e me conta"})

	if ctx.Err() != context.DeadlineExceeded {
		t.Fatalf("the request ended as %v, want a deadline — this test is no longer reproducing the incident", ctx.Err())
	}
}

// runStoppedByUser is the same turn ended the other way: somebody pressed
// stop. Same shape, same write, different terminal.
func runStoppedByUser(t *testing.T, e *env, s seeded) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	e.llm.rounds = [][]ports.StreamEvent{
		askTool("call_stop_1", countingToolName, `{"value":"`+confidentialPayload+`"}`),
		{
			{Delta: "Primeira parte da resposta."},
			{Delta: " E a segunda parte, que o leitor nunca recebe."},
			{FinishReason: "stop", Usage: &ports.Usage{PromptTokens: 120, CompletionTokens: 40}},
		},
	}
	e.llm.beforeEvent = func(i int) {
		if e.llm.streamCalls == 2 && i == 1 {
			cancel()
		}
	}

	_ = e.doCtx(ctx, "POST", "/chat/conversations/"+s.conversationID+"/messages", e.wsA,
		map[string]any{"content": "faz a alteração e me conta"})
}

// withWrite seeds a conversation whose agent may run one write capability.
func withWrite(t *testing.T) (*env, seeded) {
	t.Helper()
	e := newEnv(t, withExtraTools(countingTool{}))
	s := e.seed(e.wsA)
	e.authorizeTool(e.wsA, s.agentID, countingToolName)
	countingToolRuns.Store(0)
	return e, s
}

/* ── A · the write really ran, and the record survives ───────────────── */

// ══════════════════════════════════════════════════════════════════════
//
//	NO TOOL RECEIPT, NO EXECUTION CLAIM — and the receipt survives
//
// ══════════════════════════════════════════════════════════════════════
//
// The turn died to the transport, so the first thing to establish is that
// nothing it had already done was lost. The write-back runs on a context
// detached from the request precisely for this, and it holds: the message
// is persisted, the audit row is persisted, and the receipt says one write
// executed.
func TestADeadlineLeavesTheExecutedWriteOnTheRecord(t *testing.T) {
	e, s := withWrite(t)
	runInterruptedByDeadline(t, e, s)

	msgs := e.messages(e.wsA, s.conversationID)
	if len(msgs) != 2 {
		t.Fatalf("transcript has %d messages, want the question and the interrupted turn", len(msgs))
	}
	turn := msgs[1]

	// Everything the provider had already produced is kept, including the
	// chunk whose delivery failed — a token that was paid for is not thrown
	// away because the reader stopped listening. What is NOT reached is the
	// closing frame, and that is the signature the live incident carries.
	if turn.Content != "Primeira parte da resposta. E a segunda parte, que o leitor nunca recebe." {
		t.Fatalf("assistant content = %q, want everything the provider produced before the deadline", turn.Content)
	}
	// The gateway's terminal usage frame never arrives, so the turn is
	// costed from an estimate. In the live database every one of the
	// fourteen 30-second turns reads exactly this way on its last round,
	// and every turn that finished normally reads "provider".
	if turn.UsageSource != string(domain.UsageEstimated) {
		t.Fatalf("usage_source = %q, want %q: a turn cut mid-stream never receives the closing usage frame",
			turn.UsageSource, domain.UsageEstimated)
	}
	r := theOnlyReceipt(t, e.receipts(e.wsA, s.conversationID))
	if r.Executed != 1 {
		t.Fatalf("receipt.executed = %d, want 1: the write ran before the deadline and must stay reported",
			r.Executed)
	}
	if r.Failed != 0 || r.Refused != 0 {
		t.Fatalf("receipt reports %d failed and %d refused; the deadline is not a tool outcome",
			r.Failed, r.Refused)
	}
	if n := len(e.toolCalls(e.wsA, s.conversationID)); n != 1 {
		t.Fatalf("%d audit rows, want 1", n)
	}
}

/* ── B · the distinction · §3 and §7D ────────────────────────────────── */

// ══════════════════════════════════════════════════════════════════════
//
//	AN INFRASTRUCTURE DEADLINE IS NOT PERSISTED AS A USER STOP
//
// ══════════════════════════════════════════════════════════════════════
//
// R1's assertion here was that these two were IDENTICAL, and that was the
// defect: `context.DeadlineExceeded` and `context.Canceled` both reached
// the runtime as `ctx.Err() != nil` and both became `aborted`, so a clock
// killing a working turn was filed as a person changing their mind — and
// the interface said so, to a person who had not.
//
// Same two inputs, two records.
func TestADeadlineAndAUserStopArePersistedDifferently(t *testing.T) {
	byDeadline := func() apiMessage {
		e, s := withWrite(t)
		runInterruptedByDeadline(t, e, s)
		return e.messages(e.wsA, s.conversationID)[1]
	}()

	byUserStop := func() apiMessage {
		e, s := withWrite(t)
		runStoppedByUser(t, e, s)
		return e.messages(e.wsA, s.conversationID)[1]
	}()

	if byDeadline.FinishReason != string(domain.FinishDeadline) {
		t.Fatalf("a transport deadline was recorded as %q, want %q",
			byDeadline.FinishReason, domain.FinishDeadline)
	}
	if byUserStop.FinishReason != string(domain.FinishAborted) {
		t.Fatalf("a user stop was recorded as %q, want %q",
			byUserStop.FinishReason, domain.FinishAborted)
	}
	if byUserStop.FinishReason == byDeadline.FinishReason {
		t.Fatal("the two are still collapsed into one terminal reason")
	}
	// Neither is a failure, and neither fabricates an error message. The
	// distinction is in the reason, not in a string somebody has to read.
	if byDeadline.Error != "" || byUserStop.Error != "" {
		t.Fatalf("an error text was invented: deadline=%q stop=%q", byDeadline.Error, byUserStop.Error)
	}
	// Both did the same real work, and both report it.
	if byDeadline.Content == "" || byUserStop.Content == "" {
		t.Fatal("a turn that streamed text persisted none of it")
	}
}

/* ── C · the per-round finish reason · §5 ────────────────────────────── */

// ══════════════════════════════════════════════════════════════════════
//
//	A ROUND THAT WAS CUT OFF DID NOT RETURN "stop"
//
// ══════════════════════════════════════════════════════════════════════
//
// The runtime used to initialise the round's finish reason to `stop` the
// moment the stream opened, so a call killed mid-answer was recorded as
// having finished cleanly. The live incident's report says it in as many
// words: round 2, `"finish_reason": "stop"`, on a turn the database also
// records as interrupted.
//
// Absence is now absence. The round that really ended reports what the
// gateway said; the round that was cut reports nothing, and the field is
// omitted rather than filled in with a guess.
func TestACutRoundReportsNoProviderFinishReason(t *testing.T) {
	e, s := withWrite(t)
	runInterruptedByDeadline(t, e, s)

	turn := e.messages(e.wsA, s.conversationID)[1]
	if turn.ContextReport == nil {
		t.Fatal("the interrupted turn carries no context report")
	}
	rounds := turn.ContextReport.Rounds
	if len(rounds) != 2 {
		t.Fatalf("%d rounds in the report, want 2", len(rounds))
	}
	// Round 1 really did end, and the gateway really did say why.
	if rounds[0].FinishReason != string(domain.FinishToolCalls) {
		t.Errorf("round 1 finish_reason = %q, want %q",
			rounds[0].FinishReason, domain.FinishToolCalls)
	}
	// Round 2 was cut before the gateway named anything.
	if rounds[1].FinishReason != "" {
		t.Fatalf("round 2 claims the provider finished with %q; it was cut off and said nothing",
			rounds[1].FinishReason)
	}
}

// The other half, so the fix cannot be "never report a finish reason": a
// round the gateway really did terminate still carries what it said.
func TestACompletedRoundStillReportsWhatTheProviderSaid(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)
	e.llm.script = []ports.StreamEvent{
		{Delta: "resposta inteira"},
		{FinishReason: "length", Usage: &ports.Usage{PromptTokens: 10, CompletionTokens: 4}},
	}
	wantStatus(t, e.do("POST", "/chat/conversations/"+s.conversationID+"/messages", e.wsA,
		map[string]any{"content": "responde"}), http.StatusOK)

	turn := e.messages(e.wsA, s.conversationID)[1]
	if turn.FinishReason != string(domain.FinishLength) {
		t.Fatalf("finish_reason = %q, want %q: the provider's own reason still reaches the row",
			turn.FinishReason, domain.FinishLength)
	}
}

/* ── D · Safe Resume, per terminal · §4 and §7E ──────────────────────── */

// ══════════════════════════════════════════════════════════════════════
//
//	RECOVERABLE INFRASTRUCTURE INTERRUPTION CAN BE CONTINUED
//
// ══════════════════════════════════════════════════════════════════════
//
// The turn wrote something and was killed by a clock. Nobody chose that,
// the work is half done, and everything a safe continuation needs was
// already being persisted on this path — the receipt, the effect ref and
// the original question. What was missing was permission.
func TestADeadlineWithWritesCanBeContinued(t *testing.T) {
	e, s := withWrite(t)
	runInterruptedByDeadline(t, e, s)

	interrupted := e.messages(e.wsA, s.conversationID)[1]
	executedBefore := countingToolRuns.Load()

	// The continuation answers the original question, knowing what ran.
	e.llm.beforeEvent = nil
	e.llm.rounds = nil
	e.llm.script = []ports.StreamEvent{
		{Delta: "A alteração já estava feita."},
		{FinishReason: "stop", Usage: &ports.Usage{PromptTokens: 200, CompletionTokens: 12}},
	}
	rec := e.do("POST", "/chat/conversations/"+s.conversationID+"/resume", e.wsA,
		map[string]any{"message_id": interrupted.ID})
	wantStatus(t, rec, http.StatusOK)

	// NO DUPLICATE WRITE. The whole reason resume exists.
	if got := countingToolRuns.Load(); got != executedBefore {
		t.Fatalf("the capability ran %d more time(s) on the continuation; the executed write was repeated",
			got-executedBefore)
	}

	msgs := e.messages(e.wsA, s.conversationID)
	// A resume invents no question: one user turn, two answers.
	users := 0
	for _, m := range msgs {
		if m.Role == string(domain.RoleUser) {
			users++
		}
	}
	if users != 1 {
		t.Fatalf("%d user turns after a resume, want 1: a continuation asks nothing new", users)
	}
	if last := msgs[len(msgs)-1]; last.FinishReason != string(domain.FinishStop) {
		t.Fatalf("the continuation ended %q", last.FinishReason)
	}

	// Repeated resume is impossible: the interrupted turn is no longer last.
	again := e.do("POST", "/chat/conversations/"+s.conversationID+"/resume", e.wsA,
		map[string]any{"message_id": interrupted.ID})
	wantErrorCode(t, again, http.StatusBadRequest, "invalid")
}

// The model is told what ran, with identity and without content.
//
// The ref is what stops a continuation creating a second copy of something;
// the absence of anything else is what keeps a Confidential capability
// confidential through a failure it never anticipated.
func TestTheContinuedDeadlineTurnIsToldWhatAlreadyRan(t *testing.T) {
	e, s := withWrite(t)
	runInterruptedByDeadline(t, e, s)

	interrupted := e.messages(e.wsA, s.conversationID)[1]
	e.llm.beforeEvent = nil
	e.llm.rounds = nil
	e.llm.script = []ports.StreamEvent{
		{Delta: "pronto"},
		{FinishReason: "stop", Usage: &ports.Usage{PromptTokens: 200, CompletionTokens: 2}},
	}
	wantStatus(t, e.do("POST", "/chat/conversations/"+s.conversationID+"/resume", e.wsA,
		map[string]any{"message_id": interrupted.ID}), http.StatusOK)

	var block string
	for _, m := range e.llm.lastRequest.Messages {
		if strings.Contains(m.Content, "CONTINUING a previous attempt") {
			block = m.Content
		}
	}
	if block == "" {
		t.Fatal("the continuing turn received no resume block")
	}
	if !strings.Contains(block, string(domain.WriteExecuted)) {
		t.Fatalf("the block does not report the executed write:\n%s", block)
	}
	if !strings.Contains(block, string(domain.FinishDeadline)) {
		t.Fatalf("the block does not say why the attempt stopped:\n%s", block)
	}
	// The argument the model sent to that write. It must not come back.
	if strings.Contains(block, confidentialPayload) {
		t.Fatalf("the resume block replayed a tool payload:\n%s", block)
	}
}

// ══════════════════════════════════════════════════════════════════════
//
//	A TURN THE USER STOPPED DOES NOT OFFER TO FINISH ITSELF
//
// ══════════════════════════════════════════════════════════════════════
//
// The premise that excluded `aborted` was wrong about deadlines and right
// about this: somebody asked for the turn to stop, and a product that then
// offered to carry on would be arguing with them.
func TestAUserStoppedTurnIsStillRefusedByResume(t *testing.T) {
	e, s := withWrite(t)
	runStoppedByUser(t, e, s)

	turn := e.messages(e.wsA, s.conversationID)[1]
	if turn.FinishReason != string(domain.FinishAborted) {
		t.Fatalf("finish_reason = %q, want %q", turn.FinishReason, domain.FinishAborted)
	}
	rec := e.do("POST", "/chat/conversations/"+s.conversationID+"/resume", e.wsA,
		map[string]any{"message_id": turn.ID})
	wantErrorCode(t, rec, http.StatusBadRequest, "invalid")

	// The work it did is still on the record, and still honest. Not being
	// resumable is a product decision, not an erasure.
	if r := theOnlyReceipt(t, e.receipts(e.wsA, s.conversationID)); r.Executed != 1 {
		t.Fatalf("receipt.executed = %d, want 1", r.Executed)
	}
}

// The resumable set, stated as a table so a change to it has to come here.
func TestOnlyTheCeilingAndTheDeadlineAreResumable(t *testing.T) {
	for _, c := range []struct {
		reason domain.FinishReason
		want   bool
	}{
		{domain.FinishToolRoundLimit, true},
		{domain.FinishDeadline, true},
		{domain.FinishAborted, false},
		{domain.FinishError, false},
		{domain.FinishLength, false},
		{domain.FinishStop, false},
	} {
		if got := app.ResumableFinish(c.reason); got != c.want {
			t.Errorf("ResumableFinish(%q) = %v, want %v", c.reason, got, c.want)
		}
	}
}
