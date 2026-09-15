//go:build integration

// Turn failure: what survives, and what the consumer is told.
//
// ══════════════════════════════════════════════════════════════════════
//
//	NO REQUESTED TOOL DISAPPEARS SILENTLY
//	NO HIDDEN EXECUTION AFTER TURN FAILURE
//
// ══════════════════════════════════════════════════════════════════════
//
// Two defects, one shape. A turn can die AFTER it has already changed
// something: the ceiling is reached, the budget refuses another call, the
// gateway drops the stream. In every one of those the tools that ran
// really ran, the audit rows are written and the assistant message is
// persisted.
//
// What used to happen to that state:
//
//	the model's last request       vanished. The ceiling broke before the
//	                               execution loop, so the calls it asked
//	                               for left no row anywhere, and a receipt
//	                               could not say one had been refused.
//
//	the persisted message          was dropped by the transport. `done` is
//	                               the only frame carrying the receipts,
//	                               and it was emitted only on success, so
//	                               a consumer reading the stream saw
//	                               executions reported as nothing at all.
//
// Neither was a lie about what happened; both were silence about it. The
// existing guarantee, NO TOOL RECEIPT NO EXECUTION CLAIM, is untouched
// and is asserted here too: a refused call must never count as executed.

package chat

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/corsi/backend/internal/chat/domain"
	"github.com/corsi/backend/internal/chat/ports"
)

/* ── fixtures ────────────────────────────────────────────────────────── */

// countingTool records how many times it was actually RUN, which is the
// only way to tell "the refused round was not executed" from "the refused
// round was executed and then recorded as refused". The audit alone
// cannot distinguish those two.
type countingTool struct{}

const countingToolName = "system.counting"

var countingToolRuns atomic.Int64

func (countingTool) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:        countingToolName,
		Title:       "Counting",
		Description: "Counts how many times it really ran.",
		Effect:      domain.EffectWrite,
		Internal:    true,
		Schema: domain.ToolSchema{
			Properties: map[string]domain.ToolProperty{
				"value": {Type: domain.TypeString, MaxLength: 100},
			},
		},
	}
}

func (countingTool) Execute(_ context.Context, _ map[string]any) (domain.ToolOutput, error) {
	countingToolRuns.Add(1)
	return domain.ToolOutput{"ran": true}, nil
}

// externalReadTool declares itself as reading a system this product does
// not own, which is what makes a turn eligible to be reported as verified
// external evidence.
type externalReadTool struct{}

const externalReadToolName = "system.externalread"

func (externalReadTool) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:        externalReadToolName,
		Title:       "External read",
		Description: "Reads a system this product does not own.",
		Effect:      domain.EffectRead,
		External:    true,
		Internal:    true,
		Schema: domain.ToolSchema{
			Properties: map[string]domain.ToolProperty{
				"value": {Type: domain.TypeString, MaxLength: 100},
			},
		},
	}
}

func (externalReadTool) Execute(_ context.Context, _ map[string]any) (domain.ToolOutput, error) {
	return domain.ToolOutput{"followers": 163}, nil
}

// armingWriteTool is a successful write that runs a hook afterwards.
//
// ── Why a hook is the only way to script a mid-turn gateway failure ────
// The fake provider returns `recvErr` whenever a stream drains, which is
// every round. Setting it up front makes the FIRST call fail, before any
// tool has run, and that is a different scenario: nothing was executed,
// nothing was persisted, and the transport correctly answers with a
// status code.
//
// What this file needs is a failure in round two, AFTER round one really
// wrote something. A tool runs exactly in that window, so the hook arms
// the failure from inside the write it is testing the survival of.
type armingWriteTool struct{ arm func() }

const armingWriteToolName = "system.armingwrite"

func (armingWriteTool) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:        armingWriteToolName,
		Title:       "Arming write",
		Description: "Changes a thing, then arms whatever the test asked for.",
		Effect:      domain.EffectWrite,
		Internal:    true,
		Schema: domain.ToolSchema{
			Properties: map[string]domain.ToolProperty{
				"value": {Type: domain.TypeString, MaxLength: 100},
			},
		},
	}
}

func (h armingWriteTool) Execute(_ context.Context, _ map[string]any) (domain.ToolOutput, error) {
	if h.arm != nil {
		h.arm()
	}
	return domain.ToolOutput{"changed": true}, nil
}

/* ══════════════════════════════════════════════════════════════════════
   R1 · a refused round is still a round that was asked for
   ══════════════════════════════════════════════════════════════════════ */

// ══════════════════════════════════════════════════════════════════════
//
//	A REFUSED WRITE COUNTS AS REFUSED, NEVER AS EXECUTED
//
// ══════════════════════════════════════════════════════════════════════
//
// Every permitted round writes, and the one past the ceiling is asked for
// and stopped. The receipt has to say exactly that: the executions that
// really ran, and one refusal. Reporting the refusal as executed would be
// the failure the receipt exists to prevent; reporting the executions and
// nothing else is the silence this change closes.
func TestTheRoundLimitRecordsTheRefusedWritesAsRefused(t *testing.T) {
	e := newEnv(t, withExtraTools(mutateTool{}))
	s := e.seed(e.wsA)
	e.authorizeTool(e.wsA, s.agentID, mutateToolName)
	// One script, replayed forever: the model never stops asking to write.
	e.llm.rounds = [][]ports.StreamEvent{askMutate("call_x", "de novo")}

	rec := e.do("POST", "/chat/conversations/"+s.conversationID+"/messages", e.wsA,
		map[string]any{"content": "escreve em loop"})
	wantStatus(t, rec, http.StatusOK)

	// ── The audit ──────────────────────────────────────────────────────
	calls := e.toolCalls(e.wsA, s.conversationID)
	if len(calls) != maxToolRoundsForTest {
		t.Fatalf("%d audit rows, want %d: the executions and the refusal",
			len(calls), maxToolRoundsForTest)
	}
	var executed, refused int
	for _, c := range calls {
		switch c.Status {
		case string(domain.ToolCallOK):
			executed++
		case string(domain.ToolCallNotExecuted):
			refused++
			if c.ErrorCode != string(domain.ToolErrRoundLimit) {
				t.Errorf("refused call error_code = %q, want %q", c.ErrorCode, domain.ToolErrRoundLimit)
			}
			// Nothing ran, so nothing is invented: no result, and no time
			// spent.
			if c.Result != nil {
				t.Errorf("the refused call carries a result: %q", *c.Result)
			}
			if c.DurationMS != 0 {
				t.Errorf("the refused call claims %dms of work", c.DurationMS)
			}
		default:
			t.Errorf("unexpected status %q", c.Status)
		}
	}
	if executed != maxToolRoundsForTest-1 || refused != 1 {
		t.Fatalf("%d executed and %d refused, want %d and 1",
			executed, refused, maxToolRoundsForTest-1)
	}

	// ── The receipt ────────────────────────────────────────────────────
	r := theOnlyReceipt(t, e.receipts(e.wsA, s.conversationID))
	if r.Executed != maxToolRoundsForTest-1 {
		t.Errorf("executed = %d, want %d: only the writes that really ran",
			r.Executed, maxToolRoundsForTest-1)
	}
	if r.Refused != 1 {
		t.Errorf("refused = %d, want 1: the write the ceiling turned away", r.Refused)
	}
	if r.Failed != 0 {
		t.Errorf("failed = %d, want 0: nothing broke", r.Failed)
	}
	if len(r.Writes) != maxToolRoundsForTest {
		t.Fatalf("%d write executions on the receipt, want %d",
			len(r.Writes), maxToolRoundsForTest)
	}
	var sawRefusal bool
	for _, w := range r.Writes {
		if w.Status == string(domain.WriteNotExecuted) {
			sawRefusal = true
			if w.ErrorCode != string(domain.ToolErrRoundLimit) {
				t.Errorf("the refused write carries error_code %q", w.ErrorCode)
			}
		}
	}
	if !sawRefusal {
		t.Error("the receipt does not name the refused write")
	}
}

// The ceiling must not execute the round it refuses. If it did, the
// audit would be honest and the side effect would still be one more than
// the user asked a turn to be capable of.
func TestTheRefusedRoundExecutesNoSideEffect(t *testing.T) {
	e := newEnv(t, withExtraTools(countingTool{}))
	s := e.seed(e.wsA)
	e.authorizeTool(e.wsA, s.agentID, countingToolName)
	countingToolRuns.Store(0)
	e.llm.rounds = [][]ports.StreamEvent{askTool("call_x", countingToolName, `{"value":"x"}`)}

	rec := e.do("POST", "/chat/conversations/"+s.conversationID+"/messages", e.wsA,
		map[string]any{"content": "conta"})
	wantStatus(t, rec, http.StatusOK)

	// One audit row per request, and one execution FEWER than that: the
	// round the ceiling refuses must leave no side effect behind it.
	if got := countingToolRuns.Load(); got != int64(maxToolRoundsForTest-1) {
		t.Fatalf("the capability ran %d times, want %d: the refused round must not execute",
			got, maxToolRoundsForTest-1)
	}
	if n := len(e.toolCalls(e.wsA, s.conversationID)); n != maxToolRoundsForTest {
		t.Fatalf("%d audit rows, want %d", n, maxToolRoundsForTest)
	}
}

// ══════════════════════════════════════════════════════════════════════
//
//	A REFUSED EXTERNAL READ IS NOT EVIDENCE
//
// ══════════════════════════════════════════════════════════════════════
//
// The read receipt counts only calls that SUCCEEDED. A round-limit
// refusal is recorded with `external` true, because the capability
// declared itself external, and it must still never make the turn
// verified: nothing was read.
func TestARefusedExternalReadNeverVerifiesTheTurn(t *testing.T) {
	e := newEnv(t, withExtraTools(externalReadTool{}))
	s := e.seed(e.wsA)
	e.authorizeTool(e.wsA, s.agentID, externalReadToolName)
	e.llm.rounds = [][]ports.StreamEvent{askTool("call_x", externalReadToolName, `{"value":"x"}`)}

	rec := e.do("POST", "/chat/conversations/"+s.conversationID+"/messages", e.wsA,
		map[string]any{"content": "lê lá fora em loop"})
	wantStatus(t, rec, http.StatusOK)

	frames := parseSSE(t, rec.Body.String())
	done, ok := frameOf(frames, "done")
	if !ok {
		t.Fatalf("no final state was delivered: %v", eventNames(frames))
	}
	// The permitted rounds really read, so the turn IS verified, and the
	// refused one must not inflate the count.
	verified := int64(maxToolRoundsForTest - 1)
	if !strings.Contains(done.data, `"status":"`+string(domain.VerifiedExternalRead)+`"`) {
		t.Fatalf("read receipt = %s, want the real reads verified", done.data)
	}
	if !strings.Contains(done.data, `"verified":`+itoa(verified)) {
		t.Fatalf("read receipt = %s, want verified=%d and not %d",
			done.data, verified, verified+1)
	}
}

/* ══════════════════════════════════════════════════════════════════════
   R2 · a failed turn still delivers what it persisted
   ══════════════════════════════════════════════════════════════════════ */

// ══════════════════════════════════════════════════════════════════════
//
//	done THEN error, ON EVERY MID-TURN TERMINAL FAILURE
//
// ══════════════════════════════════════════════════════════════════════
//
// Three different failures, one rule. The rule is "was a final state
// persisted", not "which error was it": a condition keyed on the tool
// loop would have fixed the ceiling and left the budget refusal and the
// gateway failure exactly as broken, which is how three instances of one
// bug become one fix and two survivors.
func TestEveryMidTurnFailureDeliversTheFinalStateBeforeTheError(t *testing.T) {
	cases := map[string]struct {
		setup    func(e *env) seeded
		wantCode string
	}{
		// The tool loop's own ceiling.
		"round limit": {
			setup: func(e *env) seeded {
				s := e.seed(e.wsA)
				e.authorizeTool(e.wsA, s.agentID, mutateToolName)
				e.llm.rounds = [][]ports.StreamEvent{askMutate("call_x", "de novo")}
				return s
			},
			wantCode: string(domain.ToolErrRoundLimit),
		},
		// The budget, refusing the round AFTER one already wrote.
		"budget refusal after a write": {
			setup: func(e *env) seeded {
				s := e.seedWith(e.wsA, map[string]any{"daily_token_limit": 35})
				e.authorizeTool(e.wsA, s.agentID, mutateToolName)
				e.llm.rounds = [][]ports.StreamEvent{
					askMutate("call_1", "x"),
					answer("nunca chego aqui", 500, 500),
				}
				return s
			},
			wantCode: string(domain.BlockTokens),
		},
		// The gateway, dropping the stream after a write.
		"gateway failure after a write": {
			setup: func(e *env) seeded {
				s := e.seed(e.wsA)
				e.authorizeTool(e.wsA, s.agentID, armingWriteToolName)
				e.llm.rounds = [][]ports.StreamEvent{
					askTool("call_1", armingWriteToolName, `{"value":"x"}`),
					answer("nunca chego aqui", 40, 8),
				}
				return s
			},
			wantCode: "upstream",
		},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			// The hook is bound before the env exists and resolved after,
			// because the tool has to be registered at construction and the
			// thing it arms belongs to the env.
			var arm func()
			e := newEnv(t, withExtraTools(
				mutateTool{},
				armingWriteTool{arm: func() { arm() }},
			))
			arm = func() { e.llm.recvErr = domain.Upstream("the gateway hung up mid-stream") }
			s := c.setup(e)

			rec := e.do("POST", "/chat/conversations/"+s.conversationID+"/messages", e.wsA,
				map[string]any{"content": "faz alguma coisa"})
			wantStatus(t, rec, http.StatusOK)

			frames := parseSSE(t, rec.Body.String())
			names := eventNames(frames)

			// ── The order, which is the contract ───────────────────────
			doneAt, errAt := indexOf(names, "done"), indexOf(names, "error")
			if doneAt < 0 {
				t.Fatalf("no final state was delivered: %v", names)
			}
			if errAt < 0 {
				t.Fatalf("the failure was not reported: %v", names)
			}
			if doneAt > errAt {
				t.Fatalf("frames = %v, want done before error", names)
			}

			// ── And the final state carries the receipts ───────────────
			done, _ := frameOf(frames, "done")
			for _, want := range []string{`"write_receipt"`, `"read_receipt"`, `"message"`} {
				if !strings.Contains(done.data, want) {
					t.Errorf("done frame carries no %s: %s", want, done.data)
				}
			}

			// ── done is not a claim of success ─────────────────────────
			// The message inside it says how the turn ended, and the
			// error frame that follows says why.
			errFrame, _ := frameOf(frames, "error")
			if !strings.Contains(errFrame.data, c.wantCode) {
				t.Errorf("error frame = %s, want %q named", errFrame.data, c.wantCode)
			}

			// ── The write that really happened is on the record ────────
			r := theOnlyReceipt(t, e.receipts(e.wsA, s.conversationID))
			if r.Executed < 1 {
				t.Errorf("receipt = %+v, want the write that ran counted", r)
			}
		})
	}
}

// ══════════════════════════════════════════════════════════════════════
//
//	THE RULE IS NOT KEYED ON THE KIND OF ERROR
//
// ══════════════════════════════════════════════════════════════════════
//
// Asserted as a property over the three failures above: they carry three
// different error kinds and all three deliver a final state. A `if
// KindToolLoop` in the transport would pass the first subtest and fail
// the other two.
func TestTheFinalStateRuleIsNotSpecificToTheToolLoop(t *testing.T) {
	// The budget refusal is KindBudget and the gateway failure is
	// KindUpstream; neither is KindToolLoop, and both must deliver.
	for name, build := range map[string]func(e *env) seeded{
		"budget": func(e *env) seeded {
			s := e.seedWith(e.wsA, map[string]any{"daily_token_limit": 35})
			e.authorizeTool(e.wsA, s.agentID, mutateToolName)
			e.llm.rounds = [][]ports.StreamEvent{
				askMutate("call_1", "x"),
				answer("nunca", 500, 500),
			}
			return s
		},
		"upstream": func(e *env) seeded {
			s := e.seed(e.wsA)
			e.authorizeTool(e.wsA, s.agentID, armingWriteToolName)
			e.llm.rounds = [][]ports.StreamEvent{
				askTool("call_1", armingWriteToolName, `{"value":"x"}`),
				answer("nunca", 40, 8),
			}
			return s
		},
	} {
		t.Run(name, func(t *testing.T) {
			var arm func()
			e := newEnv(t, withExtraTools(
				mutateTool{},
				armingWriteTool{arm: func() { arm() }},
			))
			arm = func() { e.llm.recvErr = domain.Upstream("gone") }
			s := build(e)
			rec := e.do("POST", "/chat/conversations/"+s.conversationID+"/messages", e.wsA,
				map[string]any{"content": "x"})
			wantStatus(t, rec, http.StatusOK)

			if _, ok := frameOf(parseSSE(t, rec.Body.String()), "done"); !ok {
				t.Fatalf("%s delivered no final state; the rule is keyed on the error kind", name)
			}
		})
	}
}

// ══════════════════════════════════════════════════════════════════════
//
//	A FAILURE WITH NOTHING PERSISTED GETS NO ARTIFICIAL done
//
// ══════════════════════════════════════════════════════════════════════
//
// The first call never reached the model, so there is no final state to
// hand over and there is still a status code left to send. Emitting an
// empty `done` here would be inventing a delivery.
func TestAFailureBeforeAnythingIsPersistedGetsNoFinalState(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)
	e.llm.openErr = domain.Upstream("the gateway refused the connection")

	rec := e.do("POST", "/chat/conversations/"+s.conversationID+"/messages", e.wsA,
		map[string]any{"content": "pergunta que nem sai"})

	// No frames at all: the stream never opened, so this is an ordinary
	// error response with a status code.
	if rec.Code == http.StatusOK {
		t.Fatalf("status = %d, want a failure status: nothing was streamed", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "event: done") {
		t.Fatalf("a turn with no persisted state delivered a done frame: %s", rec.Body.String())
	}
}

// ══════════════════════════════════════════════════════════════════════
//
//	ABORT DOES NOT REGRESS
//
// ══════════════════════════════════════════════════════════════════════
//
// A cancelled turn already ended correctly: it sets no error, so
// SendMessage returns a message and nil, and `done` was always emitted.
// This asserts it still is, and that no error frame appeared beside it.
func TestACancelledTurnIsStillNotAFailure(t *testing.T) {
	// The frames cannot be asserted here: the client hung up, so whatever
	// the recorder holds was written to somebody who is gone. What IS
	// observable is the record, and it is what distinguishes the abort
	// path from the three failures above: SendMessage returns no error,
	// so `done` was always emitted and no `error` frame was ever produced.
	//
	// The regression this guards is an abort being reclassified as a
	// failure while generalising the transport.
	e := newEnv(t)
	s := e.seed(e.wsA)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	e.llm.script = []ports.StreamEvent{
		{Delta: "primeiro pedaço"},
		{Delta: ", segundo pedaço"},
	}
	e.llm.beforeEvent = func(i int) {
		if i == 1 {
			cancel()
		}
	}

	rec := e.doCtx(ctx, "POST", "/chat/conversations/"+s.conversationID+"/messages", e.wsA,
		map[string]any{"content": "pergunta interrompida"})
	_ = rec

	turns := e.assistantTurns(e.wsA, s.conversationID)
	if len(turns) != 1 {
		t.Fatalf("%d assistant turns, want 1", len(turns))
	}
	if turns[0].FinishReason != string(domain.FinishAborted) {
		t.Fatalf("finish_reason = %q, want aborted", turns[0].FinishReason)
	}
	// An abort carries no error text, which is exactly how the runtime
	// says "this was not a failure".
	if turns[0].Error != "" {
		t.Errorf("a cancelled turn was recorded as failing: %q", turns[0].Error)
	}
}

/* ── helpers ─────────────────────────────────────────────────────────── */

func indexOf(names []string, want string) int {
	for i, n := range names {
		if n == want {
			return i
		}
	}
	return -1
}
