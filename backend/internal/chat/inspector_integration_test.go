//go:build integration

// Integration tests for the Context Inspector.
//
// The Inspector is a read of something that must already be true: the
// account of what a turn carried, stamped when it happened. So almost
// everything here is really one assertion in different clothes —
//
//	the report describes the turn as it was, and nothing that happens
//	afterwards can change what it says.
//
// The harness lives in chat_integration_test.go.
package chat

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/chat/app"
	"github.com/corsi/backend/internal/chat/domain"
	"github.com/corsi/backend/internal/chat/ports"
)

/* ── helpers ─────────────────────────────────────────────────────────── */

// lastReport is the report of the newest assistant turn in a thread, read
// back through the transcript route — which is the path a page reload
// takes, and therefore the only one that proves the report survives one.
func (e *env) lastReport(ws uuid.UUID, conversationID string) *apiContextReport {
	e.t.Helper()
	turns := e.assistantTurns(ws, conversationID)
	if len(turns) == 0 {
		e.t.Fatalf("conversation has no assistant turn")
	}
	last := turns[len(turns)-1]
	if last.ContextReport == nil {
		e.t.Fatalf("the assistant turn carries no context report: %+v", last)
	}
	return last.ContextReport
}

/* ── the report belongs to the turn ──────────────────────────────────── */

// TestContextReportIsStampedOnTheTurn is the whole batch in one test: the
// report exists, it is attached to the assistant message, it survives being
// read back from the database, and the user turn beside it has none.
func TestContextReportIsStampedOnTheTurn(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)
	e.remember(e.wsA, s.agentID, "prefere respostas diretas")
	e.addSource(e.wsA, s.agentID, "Guia editorial", "sempre citar a fonte")

	e.send(e.wsA, s.conversationID, "como eu escrevo isso?")

	msgs := e.messages(e.wsA, s.conversationID)
	if len(msgs) != 2 {
		t.Fatalf("transcript has %d messages, want the question and the answer", len(msgs))
	}
	if msgs[0].Role != "user" || msgs[0].ContextReport != nil {
		t.Fatalf("a user turn carries a context report: %+v", msgs[0].ContextReport)
	}
	report := msgs[1].ContextReport
	if report == nil {
		t.Fatalf("the assistant turn carries no context report")
	}

	// All five blocks, in composition order. This turn exercises every
	// producer at once: instructions, memory, sources, and the question —
	// history is absent because nothing preceded the question.
	kinds := make([]string, 0, len(report.Blocks))
	for _, b := range report.Blocks {
		kinds = append(kinds, b.Kind)
	}
	want := "instructions,memory,sources,current_message"
	if strings.Join(kinds, ",") != want {
		t.Fatalf("blocks = %v, want %s", kinds, want)
	}

	for _, b := range report.Blocks {
		if b.Characters <= 0 || b.EstimatedTokens <= 0 {
			t.Fatalf("block %s reports %d characters and %d tokens", b.Kind, b.Characters, b.EstimatedTokens)
		}
	}
}

// TestReportTotalsAddUp: the numbers on screen are added by a reader, so
// the ones on the wire have to add up. A total that is not the sum of its
// parts is a total nobody can check.
func TestReportTotalsAddUp(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)
	e.remember(e.wsA, s.agentID, "um fato")
	e.addSource(e.wsA, s.agentID, "Documento", "corpo do documento")
	e.send(e.wsA, s.conversationID, "primeira")
	e.send(e.wsA, s.conversationID, "segunda")

	report := e.lastReport(e.wsA, s.conversationID)

	sumChars, sumTokens := 0, 0
	for _, b := range report.Blocks {
		sumChars += b.Characters
		sumTokens += b.EstimatedTokens
	}
	if sumChars != report.TotalCharacters {
		t.Fatalf("blocks sum to %d characters, total says %d", sumChars, report.TotalCharacters)
	}
	if sumTokens != report.TotalEstimatedTokens {
		t.Fatalf("blocks sum to %d tokens, total says %d", sumTokens, report.TotalEstimatedTokens)
	}

	// And the report's total is the same number the accounting column
	// froze. Two figures describing one estimate must not drift apart.
	turns := e.assistantTurns(e.wsA, s.conversationID)
	last := turns[len(turns)-1]
	if last.EstimatedPromptTokens == nil {
		t.Fatalf("the turn has no estimated_prompt_tokens")
	}
	if *last.EstimatedPromptTokens != report.TotalEstimatedTokens {
		t.Fatalf("estimated_prompt_tokens = %d, report total = %d",
			*last.EstimatedPromptTokens, report.TotalEstimatedTokens)
	}
}

/* ── historical truth ────────────────────────────────────────────────── */

// TestReportIsAHistoricalSnapshot is the reason the report is stored rather
// than recomputed, and the single most important test in this file.
//
// After a turn happens, everything that fed it is changed: the memory is
// deleted, the source is deleted, the instructions are rewritten and the
// history limit is lowered. A report derived from the current state would
// now describe a turn that never happened. This one must not move.
func TestReportIsAHistoricalSnapshot(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)
	memory := e.remember(e.wsA, s.agentID, "um fato que será apagado")
	source := e.addSource(e.wsA, s.agentID, "Fonte", "material que será apagado")

	e.send(e.wsA, s.conversationID, "pergunta")
	before := e.lastReport(e.wsA, s.conversationID)

	// Everything the turn was made of, changed or destroyed.
	wantStatus(t, e.do("DELETE", "/chat/agents/"+s.agentID+"/memories/"+memory.ID, e.wsA, nil),
		http.StatusNoContent)
	wantStatus(t, e.do("DELETE", "/chat/agents/"+s.agentID+"/sources/"+source.ID, e.wsA, nil),
		http.StatusNoContent)
	wantStatus(t, e.do("PATCH", "/chat/agents/"+s.agentID, e.wsA, map[string]any{
		"system_prompt": "instruções completamente diferentes, e muito mais longas do que as anteriores",
		"history_limit": 1,
	}), http.StatusOK)

	after := e.lastReport(e.wsA, s.conversationID)

	beforeJSON, _ := json.Marshal(before)
	afterJSON, _ := json.Marshal(after)
	if string(beforeJSON) != string(afterJSON) {
		t.Fatalf("the report changed after its inputs did:\n before %s\n after  %s", beforeJSON, afterJSON)
	}

	// Named explicitly, because "it did not change" is easy to satisfy by
	// accident if the blocks were empty to begin with.
	if !after.hasBlock("memory") || !after.hasBlock("sources") {
		t.Fatalf("the snapshot lost the blocks whose data was deleted: %+v", after.Blocks)
	}
	if after.block(t, "memory").Items == 0 || after.block(t, "sources").Items == 0 {
		t.Fatalf("the snapshot reports zero items for deleted data: %+v", after.Blocks)
	}
}

// TestReportSurvivesAgentReconfiguration is the same claim from the other
// side: a turn answered under one instruction set keeps reporting that set's
// size, even when the agent is rewritten to something much longer.
func TestReportSurvivesAgentReconfiguration(t *testing.T) {
	e := newEnv(t)
	s := e.seedWith(e.wsA, map[string]any{"system_prompt": "curto"})

	e.send(e.wsA, s.conversationID, "pergunta")
	before := e.lastReport(e.wsA, s.conversationID).block(t, "instructions")

	long := strings.Repeat("instruções muito mais longas. ", 100)
	wantStatus(t, e.do("PATCH", "/chat/agents/"+s.agentID, e.wsA,
		map[string]any{"system_prompt": long}), http.StatusOK)

	after := e.lastReport(e.wsA, s.conversationID).block(t, "instructions")
	if after.Characters != before.Characters {
		t.Fatalf("instructions block moved from %d to %d characters after the agent was edited",
			before.Characters, after.Characters)
	}
	if after.Characters != len([]rune("curto")) {
		t.Fatalf("instructions block = %d characters, want the prompt that was actually sent",
			after.Characters)
	}
}

/* ── the done frame ──────────────────────────────────────────────────── */

// TestDoneFrameCarriesTheReport: the Inspector must work immediately after
// a turn as well as after a reload, and both paths must show the same
// thing. The frame carries the message, and the report rides on it — no
// second shape, no second endpoint.
func TestDoneFrameCarriesTheReport(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)
	e.remember(e.wsA, s.agentID, "um fato")

	rec := e.do("POST", "/chat/conversations/"+s.conversationID+"/messages", e.wsA,
		map[string]any{"content": "pergunta"})
	wantStatus(t, rec, http.StatusOK)

	frames := parseSSE(t, rec.Body.String())
	// The four frame types are unchanged. Growing the message object is
	// additive; inventing a fifth event would not have been.
	for _, f := range frames {
		switch f.event {
		case "reasoning", "delta", "done", "error":
		default:
			t.Fatalf("unexpected SSE event %q; the contract has four", f.event)
		}
	}

	done, ok := frameOf(frames, "done")
	if !ok {
		t.Fatalf("no done frame: %v", eventNames(frames))
	}
	var payload struct {
		Message apiMessage `json:"message"`
	}
	if err := json.Unmarshal([]byte(done.data), &payload); err != nil {
		t.Fatalf("decode done frame: %v", err)
	}
	if payload.Message.ContextReport == nil {
		t.Fatalf("the done frame carries no context report: %s", done.data)
	}

	// Identical to what a reload would show.
	streamed, _ := json.Marshal(payload.Message.ContextReport)
	stored, _ := json.Marshal(e.lastReport(e.wsA, s.conversationID))
	if string(streamed) != string(stored) {
		t.Fatalf("the streamed report differs from the stored one:\n live  %s\n saved %s",
			streamed, stored)
	}
}

/* ── estimated versus actual ─────────────────────────────────────────── */

// TestEstimatedAndActualAreBothKept: the Inspector's central comparison.
// The estimate made before the call and the number the provider reported
// after it are two different measurements, and both survive.
func TestEstimatedAndActualAreBothKept(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)
	e.llm.reply("resposta", 977, 123)

	e.send(e.wsA, s.conversationID, "pergunta")

	turns := e.assistantTurns(e.wsA, s.conversationID)
	turn := turns[len(turns)-1]

	if turn.UsageSource != "provider" {
		t.Fatalf("usage_source = %q, want provider", turn.UsageSource)
	}
	if turn.PromptTokens != 977 || turn.CompletionTokens != 123 {
		t.Fatalf("provider counts = %d/%d, want 977/123", turn.PromptTokens, turn.CompletionTokens)
	}
	if turn.EstimatedPromptTokens == nil {
		t.Fatalf("the estimate was dropped once the provider reported")
	}
	// They are different numbers, and that is the point: the estimate is
	// not overwritten by the measurement.
	if *turn.EstimatedPromptTokens == turn.PromptTokens {
		t.Fatalf("the fixture no longer distinguishes the two; pick a provider count that differs")
	}
	if turn.ContextReport.TotalEstimatedTokens != *turn.EstimatedPromptTokens {
		t.Fatalf("report total %d != estimated_prompt_tokens %d",
			turn.ContextReport.TotalEstimatedTokens, *turn.EstimatedPromptTokens)
	}
}

// TestEstimateSurvivesWhenTheProviderReportsNothing: a gateway that sends
// no usage frame must not erase what the builder knew. The turn falls back
// to the estimate, marked as one.
func TestEstimateSurvivesWhenTheProviderReportsNothing(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)
	e.llm.script = e.llm.script[:0]
	e.llm.reply("resposta", 0, 0)
	// A reply with a usage frame of zeros is not the same as no usage
	// frame; drop the frame entirely.
	e.llm.script = e.llm.script[:1]

	e.send(e.wsA, s.conversationID, "pergunta")

	turns := e.assistantTurns(e.wsA, s.conversationID)
	turn := turns[len(turns)-1]
	if turn.UsageSource != "estimated" {
		t.Fatalf("usage_source = %q, want estimated", turn.UsageSource)
	}
	if turn.ContextReport == nil || turn.ContextReport.TotalEstimatedTokens <= 0 {
		t.Fatalf("the report lost its estimate: %+v", turn.ContextReport)
	}
	if turn.EstimatedPromptTokens == nil {
		t.Fatalf("estimated_prompt_tokens is nil on a turn with no provider usage")
	}
}

/* ── cost ────────────────────────────────────────────────────────────── */

// TestCostIsKnownOrExplicitlyUnknown: the accounting semantics the Inspector
// must not undo. NULL is not zero, and a surface that renders "$0.00" for
// "we do not know" is lying about a number people spend money on.
func TestCostIsKnownOrExplicitlyUnknown(t *testing.T) {
	t.Run("known", func(t *testing.T) {
		e := newEnv(t)
		s := e.seed(e.wsA)
		e.llm.reply("resposta", 1000, 500)
		e.send(e.wsA, s.conversationID, "pergunta")

		turns := e.assistantTurns(e.wsA, s.conversationID)
		turn := turns[len(turns)-1]
		if turn.Cost == nil {
			t.Fatalf("cost is nil on a turn with a known rate card")
		}
		if !nearly(*turn.Cost, 1000*1e-6+500*3e-6) {
			t.Fatalf("cost = %v", *turn.Cost)
		}
		if turn.InputCostPerToken == nil || turn.OutputCostPerToken == nil {
			t.Fatalf("the rates were not frozen with the turn: %+v", turn)
		}
	})

	t.Run("unknown stays nil, never zero", func(t *testing.T) {
		e := newEnv(t)
		s := e.seed(e.wsA)
		e.llm.prices = map[string]ports.Price{} // the gateway knows no price
		e.llm.reply("resposta", 1000, 500)
		e.send(e.wsA, s.conversationID, "pergunta")

		turns := e.assistantTurns(e.wsA, s.conversationID)
		turn := turns[len(turns)-1]
		if turn.Cost != nil {
			t.Fatalf("cost = %v, want nil — an unpriced turn is unknown, not free", *turn.Cost)
		}
		if turn.InputCostPerToken != nil || turn.OutputCostPerToken != nil {
			t.Fatalf("rates were invented for a turn with no price list: %+v", turn)
		}
		// The tokens are still known; only the money is not.
		if turn.PromptTokens != 1000 {
			t.Fatalf("prompt tokens = %d, want the provider's number", turn.PromptTokens)
		}
	})
}

/* ── exclusions ──────────────────────────────────────────────────────── */

// TestReportExplainsExclusions: the backend is the authority on why
// something was left out, and the reasons reach the wire distinct.
func TestReportExplainsExclusions(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)

	// Memory over its ceiling: several items, one of which cannot fit.
	for i := 0; i < 4; i++ {
		e.remember(e.wsA, s.agentID, strings.Repeat(string(rune('a'+i)), app.MemoryBudgetChars/3))
	}
	// Sources: one that loses the race, one that could never fit.
	half := app.SourcesBudgetChars / 2
	e.addSource(e.wsA, s.agentID, "Primeira", strings.Repeat("p", half))
	e.addSource(e.wsA, s.agentID, "Segunda", strings.Repeat("q", half))
	e.addSource(e.wsA, s.agentID, "Gigante", strings.Repeat("z", domain.MaxSourceContent))

	e.send(e.wsA, s.conversationID, "pergunta")
	report := e.lastReport(e.wsA, s.conversationID)

	mem := report.block(t, "memory")
	if len(mem.Exclusions) != 1 || mem.Exclusions[0].Reason != "budget" || mem.Exclusions[0].Items == 0 {
		t.Fatalf("memory exclusions = %+v, want items dropped for budget", mem.Exclusions)
	}

	src := report.block(t, "sources")
	reasons := map[string]int{}
	for _, ex := range src.Exclusions {
		reasons[ex.Reason] = ex.Items
	}
	if reasons["budget"] != 1 {
		t.Fatalf("sources exclusions = %+v, want one dropped for budget", src.Exclusions)
	}
	if reasons["too_large"] != 1 {
		t.Fatalf("sources exclusions = %+v, want one reported as too_large — a source bigger than the whole block is not the same problem as one that lost the race",
			src.Exclusions)
	}
}

/* ── warnings ────────────────────────────────────────────────────────── */

// TestReportRecordsMemoryDegradation closes the gap memory injection opened:
// degrades instead of failing the turn, and until now nothing downstream
// could tell that had happened.
func TestReportRecordsMemoryDegradation(t *testing.T) {
	d := dsn(t)
	e := newEnv(t)
	s := e.seed(e.wsA)
	e.remember(e.wsA, s.agentID, "um fato que não será lido")

	t.Cleanup(func() { freshDB(t, d) })
	if _, err := e.pool.Exec(t.Context(), `DROP TABLE chat.agent_memories`); err != nil {
		t.Fatalf("drop memories table: %v", err)
	}

	e.send(e.wsA, s.conversationID, "responde mesmo assim?")

	report := e.lastReport(e.wsA, s.conversationID)
	mem := report.block(t, "memory")
	if mem.Items != 0 || mem.Characters != 0 {
		t.Fatalf("memory block = %+v, want nothing carried", mem)
	}
	if len(mem.Exclusions) != 1 || mem.Exclusions[0].Reason != "unavailable" {
		t.Fatalf("memory exclusions = %+v, want one unavailable", mem.Exclusions)
	}
	// The turn still happened, which is the whole point of degrading.
	turns := e.assistantTurns(e.wsA, s.conversationID)
	if len(turns) == 0 || turns[len(turns)-1].Content == "" {
		t.Fatalf("the turn produced no answer")
	}
}

func TestReportRecordsSourcesDegradation(t *testing.T) {
	d := dsn(t)
	e := newEnv(t)
	s := e.seed(e.wsA)
	e.addSource(e.wsA, s.agentID, "Nunca lida", "conteúdo")

	t.Cleanup(func() { freshDB(t, d) })
	if _, err := e.pool.Exec(t.Context(), `DROP TABLE chat.agent_sources`); err != nil {
		t.Fatalf("drop sources table: %v", err)
	}

	e.send(e.wsA, s.conversationID, "responde mesmo assim?")

	src := e.lastReport(e.wsA, s.conversationID).block(t, "sources")
	if len(src.Exclusions) != 1 || src.Exclusions[0].Reason != "unavailable" {
		t.Fatalf("sources exclusions = %+v, want one unavailable", src.Exclusions)
	}
}

// TestReportRecordsBothDegradations: two failures are two warnings. A
// reader has to be able to tell "memory was missing" from "everything
// auxiliary was missing".
func TestReportRecordsBothDegradations(t *testing.T) {
	d := dsn(t)
	e := newEnv(t)
	s := e.seed(e.wsA)

	t.Cleanup(func() { freshDB(t, d) })
	for _, table := range []string{"chat.agent_memories", "chat.agent_sources"} {
		if _, err := e.pool.Exec(t.Context(), `DROP TABLE `+table); err != nil {
			t.Fatalf("drop %s: %v", table, err)
		}
	}

	e.send(e.wsA, s.conversationID, "pergunta")

	report := e.lastReport(e.wsA, s.conversationID)
	degraded := map[string]bool{}
	for _, b := range report.Blocks {
		for _, ex := range b.Exclusions {
			if ex.Reason == "unavailable" {
				degraded[b.Kind] = true
			}
		}
	}
	if !degraded["memory"] || !degraded["sources"] {
		t.Fatalf("degraded blocks = %v, want both memory and sources", degraded)
	}
}

/* ── lifecycle ───────────────────────────────────────────────────────── */

// TestRegenerateProducesItsOwnReport: the report belongs to the turn that
// was executed. Regenerating deletes the old answer and runs a new one, so
// the new answer must carry a new account rather than inherit the old one.
func TestRegenerateProducesItsOwnReport(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)
	memory := e.remember(e.wsA, s.agentID, "um fato presente no primeiro turno")

	e.send(e.wsA, s.conversationID, "pergunta")
	first := e.lastReport(e.wsA, s.conversationID)
	if !first.hasBlock("memory") {
		t.Fatalf("the first turn carried no memory block")
	}

	// Between the two runs, the memory is removed. The regenerated turn is
	// a different turn and must say so.
	wantStatus(t, e.do("DELETE", "/chat/agents/"+s.agentID+"/memories/"+memory.ID, e.wsA, nil),
		http.StatusNoContent)

	// Regenerate: cut from the user turn and ask again.
	userTurn := e.messages(e.wsA, s.conversationID)[0]
	rec := e.do("DELETE",
		"/chat/conversations/"+s.conversationID+"/messages/"+itoa(userTurn.Seq), e.wsA, nil)
	wantStatus(t, rec, http.StatusOK)
	e.send(e.wsA, s.conversationID, "pergunta")

	second := e.lastReport(e.wsA, s.conversationID)
	if second.hasBlock("memory") {
		t.Fatalf("the regenerated turn reused the old report: it still claims a memory block")
	}

	// Exactly one assistant turn remains, and exactly one report with it —
	// the truncated turn took its account with it rather than leaving an
	// orphan behind.
	turns := e.assistantTurns(e.wsA, s.conversationID)
	if len(turns) != 1 {
		t.Fatalf("conversation has %d assistant turns after regenerate, want 1", len(turns))
	}
	if turns[0].ContextReport == nil {
		t.Fatalf("the regenerated turn has no report")
	}
}

// TestFailedTurnStillCarriesItsReport: a turn the provider refused never
// reached the model, and the account of what it was about to send is
// exactly what explains the failure.
func TestFailedTurnStillCarriesItsReport(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)
	e.remember(e.wsA, s.agentID, "um fato")
	e.llm.openErr = domain.Upstream("the gateway refused the request")

	rec := e.do("POST", "/chat/conversations/"+s.conversationID+"/messages", e.wsA,
		map[string]any{"content": "pergunta"})
	wantStatus(t, rec, http.StatusBadGateway)

	turns := e.assistantTurns(e.wsA, s.conversationID)
	if len(turns) != 1 {
		t.Fatalf("the failed turn was not recorded")
	}
	if turns[0].ContextReport == nil {
		t.Fatalf("the failed turn carries no report; nothing explains what it tried to send")
	}
	if !turns[0].ContextReport.hasBlock("memory") {
		t.Fatalf("the report of the failed turn lost its memory block")
	}
}

/* ── isolation and secrecy ───────────────────────────────────────────── */

// TestReportIsWorkspaceScoped: the report is read through the transcript,
// so it inherits that route's scope — and this is what says so out loud.
func TestReportIsWorkspaceScoped(t *testing.T) {
	e := newEnv(t)
	a := e.seed(e.wsA)
	e.remember(e.wsA, a.agentID, "segredo do workspace A")
	e.send(e.wsA, a.conversationID, "pergunta")

	rec := e.do("GET", "/chat/conversations/"+a.conversationID+"/messages", e.wsB, nil)
	wantErrorCode(t, rec, http.StatusNotFound, "not_found")
}

// TestReportCarriesNoContentAndNoCredential is the privacy invariant.
//
// The report is counts and reasons. It must never carry the text of the
// instructions, the memories, the sources or the conversation — and it must
// never carry anything from the provider credential, which is the one
// secret this module holds.
func TestReportCarriesNoContentAndNoCredential(t *testing.T) {
	e := newEnv(t)
	s := e.seedWith(e.wsA, map[string]any{"system_prompt": "INSTRUCAOSECRETA"})
	e.remember(e.wsA, s.agentID, "MEMORIASECRETA")
	e.addSource(e.wsA, s.agentID, "FONTETITULO", "FONTECONTEUDO")

	e.send(e.wsA, s.conversationID, "PERGUNTASECRETA")

	raw, err := json.Marshal(e.lastReport(e.wsA, s.conversationID))
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	for _, secret := range []string{
		"INSTRUCAOSECRETA", "MEMORIASECRETA", "FONTETITULO", "FONTECONTEUDO", "PERGUNTASECRETA",
		testAPIKey, "gateway.invalid", "api_key", "base_url",
	} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("the report leaked %q: %s", secret, raw)
		}
	}

	// It is not empty either — the point is that it counts without copying.
	// The block is larger than the memory itself because the block's header
	// is paid for too; what matters is that the memory was measured.
	var report apiContextReport
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatalf("decode: %v", err)
	}
	mem := report.block(t, "memory")
	if mem.Items != 1 || mem.Characters < len([]rune("MEMORIASECRETA")) {
		t.Fatalf("memory block does not count the memory it refused to copy: %+v", mem)
	}
}

/* ── rows written before the column existed ──────────────────────────── */

// TestTurnsPredatingTheReportColumnReadBackAsUnrecorded: absent is not
// empty. A turn from before this migration has no account of itself, and
// the read path has to say "not recorded" rather than "nothing was sent".
func TestTurnsPredatingTheReportColumnReadBackAsUnrecorded(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)
	e.send(e.wsA, s.conversationID, "pergunta")

	// Simulate the pre-migration state for this row.
	if _, err := e.pool.Exec(t.Context(),
		`UPDATE chat.messages SET context_report = NULL WHERE role = 'assistant'`); err != nil {
		t.Fatalf("clear report: %v", err)
	}

	turns := e.assistantTurns(e.wsA, s.conversationID)
	if len(turns) != 1 {
		t.Fatalf("expected one assistant turn")
	}
	if turns[0].ContextReport != nil {
		t.Fatalf("a NULL report read back as %+v", turns[0].ContextReport)
	}
	// And the rest of the row is untouched: losing the account does not
	// lose the turn.
	if turns[0].Content == "" {
		t.Fatalf("the turn lost its content")
	}
}
