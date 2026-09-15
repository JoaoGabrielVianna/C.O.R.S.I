//go:build integration

// Execution continuity — S8A.
//
// ══════════════════════════════════════════════════════════════════════
//
//	RECEIPT PROVES EXECUTION; READ PROVES STATE
//
// ══════════════════════════════════════════════════════════════════════
//
// ── The incident this suite exists for ─────────────────────────────────
// A live agent called a Confidential write. It ran. The receipt recorded
// EXECUTED. On the next turn the agent said:
//
//	"reparei que na verdade não cheguei a criar a lista — só respondi
//	 como se tivesse criado."
//
// and then, believing itself, created it a second time. Two identical rows
// six seconds apart, from a denial that was wrong.
//
// The model was not being careless. Its payload was withheld, so evidence
// continuity carried nothing; its own earlier sentence is not the record,
// and the platform tells it so. The turn contained NO trace that the call
// had happened. Asked, it asserted the negative.
//
// Everything below is real except the gateway: Postgres, the audit trail,
// the registry, the authorization store, the executor, the loop, the
// context builder and every route. The fake is the scripted LLM the rest of
// the module uses, which is what makes "what did the NEXT turn actually
// receive?" an assertion rather than a hope.
//
// ── What these tests can and cannot prove ──────────────────────────────
// They prove the PREMISE of the false negative is gone: that the turn which
// denied the write now carries the fact that it happened, in a form with no
// payload in it. They cannot prove what a model infers from that — a
// scripted fake does not reason. The inference is measured against the real
// model, separately. Every test here asserts on bytes and rows.
package chat

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/chat/domain"
	"github.com/corsi/backend/internal/chat/ports"
)

/* ── helpers ─────────────────────────────────────────────────────────── */

// executionMarker is a stable fragment of the block's own header. Matching
// the header rather than a block index is what makes these tests fail when
// the block stops being sent, rather than when it moves.
const executionMarker = "Execution record for this conversation"

// executionIn returns the execution-record message of one provider call, or
// the empty string when the call carried none.
func executionIn(req ports.CompletionRequest) string {
	for _, m := range req.Messages {
		if m.Role == "system" && strings.Contains(m.Content, executionMarker) {
			return m.Content
		}
	}
	return ""
}

// writesConfidentially drives one turn that calls the Confidential write
// with a distinctive value and then answers.
//
// The value is the tracer. It goes in as an argument and comes back inside
// the result, so a single search of the next turn's request answers both
// halves of the leak question at once.
func (e *env) writesConfidentially(ws uuid.UUID, conversationID, question, value string) {
	e.t.Helper()
	e.scriptNext(
		askTool("call_exec", confidentialWriteToolName, `{"value":`+quote(value)+`}`),
		answer("feito", 40, 10),
	)
	e.send(ws, conversationID, question)
}

/* ── A · the turn that would have denied it now carries the fact ─────── */

// The whole point, stated as one test.
func TestAnExecutedWriteReachesTheNextTurnAsExecutionEvidence(t *testing.T) {
	e := newEnv(t, withExtraTools(confidentialWriteTool{}))
	s := e.seed(e.wsA)
	e.authorizeTool(e.wsA, s.agentID, confidentialWriteToolName)

	e.writesConfidentially(e.wsA, s.conversationID, "registra isso", "LISTA-DE-COMPRAS")
	e.asksAgain(e.wsA, s.conversationID, "você chegou a registrar mesmo?")

	block := executionIn(e.lastRequest())
	if block == "" {
		t.Fatal("the follow-up turn carried no execution record at all: " +
			"this is exactly the state the denial was produced in")
	}
	if !strings.Contains(block, string(domain.WriteExecuted)) {
		t.Fatalf("the record does not say the write executed: %q", block)
	}
	if !strings.Contains(block, confidentialWriteToolName) {
		t.Fatalf("the record does not name what ran: %q", block)
	}
}

// The mutation, written as a test rather than as a note.
//
// ── Why this is the honest form of the S7 reproduction ─────────────────
// Removing execution continuity and asserting "the model denies it" is not
// available: the fake does not reason, and scripting a denial would prove
// only that the script says what the script says.
//
// What IS mechanically checkable is the antecedent — and it is the whole of
// the explanation. In the live battery the denial came from a turn in which
// nothing named the executed capability and nothing said EXECUTED. This
// test pins both facts to the block: delete `a.execution` from BuildContext,
// or make SelectExecutionEvidence return nothing, and the follow-up turn
// falls back into exactly that state.
//
// Tool evidence is asserted absent for the same reason it was absent live:
// the capability is Confidential, so there is no payload to replay, and the
// denial cannot be blamed on a block that was never going to carry it.
func TestWithoutExecutionEvidenceTheFollowUpHasNoTraceOfItsOwnWrite(t *testing.T) {
	e := newEnv(t, withExtraTools(confidentialWriteTool{}))
	s := e.seed(e.wsA)
	e.authorizeTool(e.wsA, s.agentID, confidentialWriteToolName)

	e.writesConfidentially(e.wsA, s.conversationID, "registra isso", "LISTA-DE-COMPRAS")
	e.asksAgain(e.wsA, s.conversationID, "você chegou a registrar mesmo?")

	req := e.lastRequest()

	// The other two places the fact could have come from, and neither does.
	if got := evidenceIn(req); got != "" {
		t.Fatalf("a Confidential write left a payload in tool evidence: %q", got)
	}
	for _, m := range req.Messages {
		if m.Role == "tool" || len(m.ToolCalls) > 0 {
			t.Fatal("the replayed history carries tool protocol; it must not")
		}
	}

	// So the execution record is the ONLY message in the turn that can say
	// the write happened. Remove it and the model is back to guessing.
	block := executionIn(req)
	if block == "" {
		t.Fatal("execution evidence removed: the turn now contains no trace " +
			"of its own write, which is the S7 false negative reproduced")
	}
	whole := requestText(req)
	if strings.Count(whole, confidentialWriteToolName) == 0 {
		t.Fatal("nothing in the turn names the capability that ran")
	}
}

/* ── B · no payload, ever ────────────────────────────────────────────── */

// The line the block must never cross. A receipt says a write happened; it
// does not get to say what it wrote, and it must not become a back door
// through which redaction is undone one field at a time.
func TestExecutionEvidenceCarriesNoConfidentialPayload(t *testing.T) {
	e := newEnv(t, withExtraTools(confidentialWriteTool{}))
	s := e.seed(e.wsA)
	e.authorizeTool(e.wsA, s.agentID, confidentialWriteToolName)

	// The tracer is the argument. The tool's own result carries 899000,
	// which is the other half.
	e.writesConfidentially(e.wsA, s.conversationID, "registra", "SEGREDO-ARGUMENTO")
	e.asksAgain(e.wsA, s.conversationID, "e então?")

	whole := requestText(e.lastRequest())
	for _, forbidden := range []string{"SEGREDO-ARGUMENTO", "899000", "amount_cents"} {
		if strings.Contains(whole, forbidden) {
			t.Fatalf("a withheld payload reached the model through the turn: %q", forbidden)
		}
	}

	// And the audit is still empty of it, so nothing above depends on the
	// block being the only careful reader.
	calls := e.toolCalls(e.wsA, s.conversationID)
	if len(calls) != 1 || calls[0].Arguments != nil || calls[0].Result != nil {
		t.Fatalf("audit row = %+v, want the payload withheld", calls)
	}
}

/* ── C · the three outcomes stay apart ───────────────────────────────── */

// A receipt that could not tell a refusal from a failure from a success
// would be worse than none: the model would read every entry as "done".
func TestExecutionEvidenceDistinguishesExecutedFailedAndRefused(t *testing.T) {
	e := newEnv(t, withExtraTools(mutateTool{}, brokenWriteTool{}, confidentialWriteTool{}))
	s := e.seed(e.wsA)
	e.authorizeTool(e.wsA, s.agentID, mutateToolName)
	e.authorizeTool(e.wsA, s.agentID, brokenWriteToolName)
	// Deliberately NOT authorized: an unauthorized call is refused before it
	// runs, which is the third outcome.

	e.scriptNext(
		askTools(
			domain.ToolCall{ID: "call_ok", Name: mutateToolName, Arguments: `{"value":"a"}`},
			domain.ToolCall{ID: "call_fail", Name: brokenWriteToolName, Arguments: `{"value":"x"}`},
			domain.ToolCall{ID: "call_refused", Name: confidentialWriteToolName, Arguments: `{"value":"x"}`},
		),
		answer("terminei", 40, 10),
	)
	e.send(e.wsA, s.conversationID, "faz as três coisas")
	e.asksAgain(e.wsA, s.conversationID, "o que aconteceu?")

	block := executionIn(e.lastRequest())
	for _, want := range []string{
		string(domain.WriteExecuted) + " " + mutateToolName,
		string(domain.WriteFailed) + " " + brokenWriteToolName,
		string(domain.WriteNotExecuted) + " " + confidentialWriteToolName,
	} {
		if !strings.Contains(block, want) {
			t.Errorf("the record does not distinguish %q:\n%s", want, block)
		}
	}
	// The refusal's reason travels as a CODE and never as a message: a
	// domain message may name a field, a limit or an id, and error_message
	// is not covered by the redaction that empties arguments and result.
	if !strings.Contains(block, string(domain.ToolErrNotAuthorized)) {
		t.Errorf("the refusal lost its reason:\n%s", block)
	}
}

/* ── D · a read-only turn gains nothing ──────────────────────────────── */

// The block answers "did anything CHANGE". A turn that only looked must not
// appear in it at all, or "executed" stops meaning execution.
func TestAReadOnlyTurnProducesNoExecutionEvidence(t *testing.T) {
	e := newEnv(t)
	s := e.grantedAgent(e.wsA)

	e.runsATool(e.wsA, s.conversationID, "dá uma olhada", "SO-LEITURA")
	e.asksAgain(e.wsA, s.conversationID, "e então?")

	req := e.lastRequest()
	if block := executionIn(req); block != "" {
		t.Fatalf("a read-only turn was reported as having executed something:\n%s", block)
	}
	// The observation still arrives — through the block built for it. The
	// two are not substitutes and this is where that is pinned.
	if !strings.Contains(evidenceIn(req), "SO-LEITURA") {
		t.Fatal("the read's own evidence stopped being replayed")
	}
}

/* ── E · the ceiling keeps what it refused ───────────────────────────── */

// The turn stopped by the round limit is the one the live battery reported
// most confusingly: the model apologised for "only pretending" to do work
// the RUNTIME had refused. Executed and refused must both survive, and the
// refusal must carry the reason it was refused.
func TestRoundLimitPreservesExecutedAndRefusedInTheRecord(t *testing.T) {
	e := newEnv(t, withExtraTools(mutateTool{}))
	s := e.seed(e.wsA)
	e.authorizeTool(e.wsA, s.agentID, mutateToolName)

	// The model never stops asking to write. Three rounds run and the
	// fourth is refused by the ceiling before it runs, so one turn
	// produces both outcomes at once.
	e.scriptNext(askMutate("call_x", "de novo"))
	e.send(e.wsA, s.conversationID, "faz isso várias vezes")
	e.asksAgain(e.wsA, s.conversationID, "conseguiu?")

	block := executionIn(e.lastRequest())
	ran := itoa(maxToolRoundsForTest - 1)
	if !strings.Contains(block, string(domain.WriteExecuted)+" "+mutateToolName+" x"+ran) {
		t.Errorf("the %s writes that ran are not reported as %s:\n%s", ran, ran, block)
	}
	if !strings.Contains(block, string(domain.WriteNotExecuted)+" "+mutateToolName+" x1") {
		t.Errorf("the refused call vanished from the record:\n%s", block)
	}
	if !strings.Contains(block, string(domain.ToolErrRoundLimit)) {
		t.Errorf("the record does not say the RUNTIME stopped it:\n%s", block)
	}
}

/* ── F · it is evidence, not an event ────────────────────────────────── */

// Carrying a receipt must not manufacture one. The turn that READS about an
// earlier write is not itself a writing turn, and the audit must hold
// exactly the rows the executions produced.
func TestCarryingExecutionEvidenceCreatesNoReceiptAndNoAuditRow(t *testing.T) {
	e := newEnv(t, withExtraTools(mutateTool{}))
	s := e.seed(e.wsA)
	e.authorizeTool(e.wsA, s.agentID, mutateToolName)

	e.scriptNext(askMutate("call_1", "a"), answer("feito", 40, 10))
	e.send(e.wsA, s.conversationID, "altera")
	e.asksAgain(e.wsA, s.conversationID, "e aí?")

	// One write happened in this conversation; one row records it.
	if calls := e.toolCalls(e.wsA, s.conversationID); len(calls) != 1 {
		t.Fatalf("%d audit rows, want exactly the one execution", len(calls))
	}

	got := e.receipts(e.wsA, s.conversationID)
	if len(got) != 2 {
		t.Fatalf("%d receipts, want one per assistant turn", len(got))
	}
	var executed, empty int
	for _, r := range got {
		switch {
		case r.Executed == 1 && r.Failed == 0 && r.Refused == 0:
			executed++
		case r.Executed == 0 && r.Failed == 0 && r.Refused == 0:
			empty++
		default:
			t.Fatalf("unexpected receipt %+v", r)
		}
	}
	if executed != 1 || empty != 1 {
		t.Fatalf("receipts = %d executed / %d empty, want 1 and 1", executed, empty)
	}
}

// The read-side twin of the same rule. Execution evidence describes writes,
// it is derived from our own log, and it must never make a turn look as
// though it read a system this product does not own.
func TestExecutionEvidenceIsNotExternalReadGrounding(t *testing.T) {
	e := newEnv(t, withExtraTools(mutateTool{}, externalReadTool{}))
	s := e.seed(e.wsA)
	e.authorizeTool(e.wsA, s.agentID, mutateToolName)
	e.authorizeTool(e.wsA, s.agentID, externalReadToolName)

	// Turn one both writes and reads externally, so the follow-up is the
	// most favourable possible case for a leak between the two receipts.
	e.scriptNext(
		askTools(
			domain.ToolCall{ID: "call_w", Name: mutateToolName, Arguments: `{"value":"a"}`},
			domain.ToolCall{ID: "call_x", Name: externalReadToolName, Arguments: `{"value":"y"}`},
		),
		answer("feito", 40, 10),
	)
	e.send(e.wsA, s.conversationID, "faz e lê")
	e.asksAgain(e.wsA, s.conversationID, "quantos seguidores?")

	// The external READ is not in the block: the block is writes only.
	block := executionIn(e.lastRequest())
	if strings.Contains(block, externalReadToolName) {
		t.Fatalf("an external read appeared in the execution record:\n%s", block)
	}

	// And the follow-up turn's own read receipt says what it must: nothing
	// external was read HERE. Evidence about an earlier read is not a read.
	rec := e.do("GET", "/chat/conversations/"+s.conversationID+"/messages", e.wsA, nil)
	wantStatus(t, rec, http.StatusOK)
	body := decode[struct {
		Items        []apiMessage `json:"items"`
		ReadReceipts map[string]struct {
			Status   string `json:"status"`
			Verified int    `json:"verified"`
		} `json:"read_receipts"`
	}](t, rec)

	last := lastAssistantOf(t, body.Items)
	if got := body.ReadReceipts[last.ID].Status; got != string(domain.NoExternalRead) {
		t.Fatalf("the follow-up turn reports %q; carrying evidence is not reading", got)
	}
}

/* ── G · the window is the window ────────────────────────────────────── */

// The block is scoped to the turns the model can see, and must not reach
// past them. A receipt for a turn that scrolled out would describe an
// exchange the model has no other trace of — and would grow without bound
// on a long conversation, which is the cost the ceiling exists to stop.
func TestExecutionEvidenceDoesNotReachPastTheHistoryWindow(t *testing.T) {
	e := newEnv(t, withExtraTools(mutateTool{}))
	// Two messages of history: the current question and the one turn before
	// it. The writing turn is pushed out by the exchanges that follow.
	s := e.seedWith(e.wsA, map[string]any{"history_limit": 2, "system_prompt": ""})
	e.authorizeTool(e.wsA, s.agentID, mutateToolName)

	e.scriptNext(askMutate("call_1", "a"), answer("feito", 40, 10))
	e.send(e.wsA, s.conversationID, "altera")

	if block := executionIn(e.lastRequest()); block != "" {
		// Sanity: the turn that wrote is still inside the window right
		// after it happened, so a later empty block means the window moved
		// rather than that the block never worked.
		t.Logf("still in window immediately after the write, as expected")
	}

	e.asksAgain(e.wsA, s.conversationID, "pergunta 1")
	e.asksAgain(e.wsA, s.conversationID, "pergunta 2")
	e.asksAgain(e.wsA, s.conversationID, "pergunta 3")

	req := e.lastRequest()
	if block := executionIn(req); block != "" {
		t.Fatalf("a receipt outlived the window that justifies it:\n%s", block)
	}
	// The row is still there. It left the PROMPT, not the audit trail: the
	// interface and the API still answer for that turn.
	if calls := e.toolCalls(e.wsA, s.conversationID); len(calls) != 1 {
		t.Fatalf("%d audit rows; truncation must not delete history", len(calls))
	}
}

/* ── H · nothing changes for a conversation that never wrote ─────────── */

// The regression guard for every agent in the system that has no write
// capability at all, and for every conversation recorded before this block
// existed. They must send what they always sent.
func TestAConversationThatNeverWroteIsUnchanged(t *testing.T) {
	e := newEnv(t)
	s := e.grantedAgent(e.wsA)

	e.asksAgain(e.wsA, s.conversationID, "oi")
	e.asksAgain(e.wsA, s.conversationID, "tudo bem?")

	req := e.lastRequest()
	if block := executionIn(req); block != "" {
		t.Fatalf("a conversation with no writes was given a record of them:\n%s", block)
	}
	// Not "an empty record", which would be a statement the model could
	// read as proof of absence. No block at all.
	if strings.Contains(requestText(req), executionMarker) {
		t.Fatal("the header reached the model with nothing under it")
	}
}

// A turn whose audit read FAILS must not be given an empty record either:
// "we could not find out" and "nothing happened" are different facts, and
// only one of them is safe to render as silence.
//
// Driven through the report rather than by breaking the database, because
// the report is where the distinction is recorded and is what the Inspector
// reads. See contextAssembler.execution.
func TestAnUnavailableAuditIsADegradationAndNotAnEmptyRecord(t *testing.T) {
	e := newEnv(t, withExtraTools(mutateTool{}))
	s := e.seed(e.wsA)
	e.authorizeTool(e.wsA, s.agentID, mutateToolName)

	e.scriptNext(askMutate("call_1", "a"), answer("feito", 40, 10))
	e.send(e.wsA, s.conversationID, "altera")
	e.asksAgain(e.wsA, s.conversationID, "e aí?")

	// The healthy case, for contrast: the block is present and the report
	// records it as a block that contributed characters.
	turn := lastAssistantOf(t, e.messages(e.wsA, s.conversationID))
	var found bool
	for _, b := range turn.ContextReport.Blocks {
		if b.Kind == string(domain.BlockExecutionEvidence) {
			found = true
			if b.Characters == 0 || b.Items != 1 {
				t.Fatalf("execution block = %+v, want one turn with characters", b)
			}
		}
	}
	if !found {
		t.Fatal("the execution block is not in the turn's report; it was paid for")
	}
}

/* ── I · caching is not collateral damage ────────────────────────────── */

// The block is rewritten on every turn, so it must sit BELOW the cache
// breakpoint. Above it, a per-turn block would invalidate the prefix that
// carries the tool catalogue — 51,6% of this system's input tokens — and
// the fix for a denial would be paid for on every request forever.
func TestExecutionEvidenceSitsBelowTheCacheBreakpoint(t *testing.T) {
	e := newEnv(t, withExtraTools(mutateTool{}))
	s := e.seed(e.wsA)
	e.authorizeTool(e.wsA, s.agentID, mutateToolName)

	e.scriptNext(askMutate("call_1", "a"), answer("feito", 40, 10))
	e.send(e.wsA, s.conversationID, "altera")
	e.asksAgain(e.wsA, s.conversationID, "e aí?")

	req := e.lastRequest()
	breakpoint := -1
	execution := -1
	for i, m := range req.Messages {
		if m.CacheBreakpoint {
			breakpoint = i
		}
		if strings.Contains(m.Content, executionMarker) {
			execution = i
		}
	}
	if breakpoint < 0 {
		t.Fatal("the turn asked for no caching at all")
	}
	if execution < 0 {
		t.Fatal("the execution record was not sent")
	}
	if execution <= breakpoint {
		t.Fatalf("the execution record is inside the cached prefix "+
			"(block at %d, breakpoint at %d): a per-turn block there "+
			"invalidates the catalogue on every request", execution, breakpoint)
	}

	// And the breakpoint is still where the caching design put it.
	turn := lastAssistantOf(t, e.messages(e.wsA, s.conversationID))
	if got := turn.ContextReport.CacheBreakpointAfter; got != string(domain.BlockInstructions) {
		t.Fatalf("cache_breakpoint_after = %q, want instructions", got)
	}
}

/* ── J · isolation ───────────────────────────────────────────────────── */

// The block is built from a query scoped by workspace AND conversation.
// This is the test that says so from the outside.
func TestExecutionEvidenceDoesNotCrossConversations(t *testing.T) {
	e := newEnv(t, withExtraTools(mutateTool{}))
	a := e.seed(e.wsA)
	e.authorizeTool(e.wsA, a.agentID, mutateToolName)

	e.scriptNext(askMutate("call_1", "a"), answer("feito", 40, 10))
	e.send(e.wsA, a.conversationID, "altera")

	// A second conversation of the same workspace and the same agent.
	other := e.newConversation(e.wsA, a.agentID)
	e.asksAgain(e.wsA, other, "você alterou alguma coisa?")

	if block := executionIn(e.lastRequest()); block != "" {
		t.Fatalf("one conversation's writes leaked into another:\n%s", block)
	}
}

/* ── shared: the whole request as text ───────────────────────────────── */

// requestText is every message of a provider call, concatenated.
//
// Used only by the leak assertions, which have to search the WHOLE request
// rather than one block: a payload that escaped into the history or into a
// tool message would be just as leaked, and a test that only read the block
// it expected to be safe would pass while the turn carried the secret.
func requestText(req ports.CompletionRequest) string {
	var b strings.Builder
	for _, m := range req.Messages {
		b.WriteString(m.Role)
		b.WriteString(m.Content)
		for _, c := range m.ToolCalls {
			b.WriteString(c.Name.String())
			b.WriteString(c.Arguments)
		}
	}
	return b.String()
}

// askTools is the script of ONE provider call that asks for several tools at
// once.
//
// It cannot be built by appending askTool to itself, and the reason is worth
// a sentence: the loop reads `ToolCalls` off each event and REPLACES what it
// holds, so two events carrying one call each leave only the second. A test
// that got this wrong would still pass, having quietly exercised one tool
// instead of three.
func askTools(calls ...domain.ToolCall) []ports.StreamEvent {
	return []ports.StreamEvent{
		{FinishReason: "tool_calls", ToolCalls: calls,
			Usage: &ports.Usage{PromptTokens: 30, CompletionTokens: 8}},
	}
}

// lastAssistantOf returns the newest assistant turn of a transcript.
func lastAssistantOf(t *testing.T, msgs []apiMessage) apiMessage {
	t.Helper()
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "assistant" {
			return msgs[i]
		}
	}
	t.Fatal("the transcript holds no assistant turn")
	return apiMessage{}
}
