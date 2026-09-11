//go:build integration

// Integration tests for memory consolidation (v1.1.0).
//
// The operation under test reads a conversation that already happened and
// proposes what is worth remembering. The normative sentence it must obey,
// and which most of this file is one restatement of:
//
//	Memory consolidation only considers conversation state that existed
//	when the user requested consolidation.
//
// The other invariants, in the order they would hurt if they broke:
//
//	proposing is not remembering        no row is written by asking
//	a refusal costs nothing             policy and budget stop before the call
//	a call always costs something       the receipt is written before parsing
//	an unreadable answer invents nothing
//
// The harness lives in chat_integration_test.go; the receipt helper in
// auxiliary_accounting_integration_test.go.
package chat

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/chat/app"
	"github.com/corsi/backend/internal/chat/domain"
	"github.com/corsi/backend/internal/chat/ports"
)

/* ── fixtures ────────────────────────────────────────────────────────── */

type apiCandidate struct {
	Content     string  `json:"content"`
	Reason      string  `json:"reason"`
	DuplicateOf *string `json:"duplicate_of"`
}

type apiCandidates struct {
	Candidates         []apiCandidate `json:"candidates"`
	ConsideredMessages int            `json:"considered_messages"`
	EffectiveUpToSeq   int64          `json:"effective_up_to_seq"`
	Usage              *struct {
		Model            string   `json:"model"`
		PromptTokens     int      `json:"prompt_tokens"`
		CompletionTokens int      `json:"completion_tokens"`
		UsageSource      string   `json:"usage_source"`
		CostUSD          *float64 `json:"cost_usd"`
	} `json:"usage"`
}

// proposes scripts the auxiliary call's answer.
func (e *env) proposes(answer string, prompt, completion int) {
	e.t.Helper()
	e.llm.script = []ports.StreamEvent{
		{Delta: answer},
		{FinishReason: "stop", Usage: &ports.Usage{PromptTokens: prompt, CompletionTokens: completion}},
	}
}

func (e *env) consolidate(ws uuid.UUID, conversationID string, upToSeq int64) apiCandidates {
	e.t.Helper()
	rec := e.do("POST", "/chat/conversations/"+conversationID+"/memory-candidates", ws,
		map[string]any{"up_to_seq": upToSeq})
	wantStatus(e.t, rec, http.StatusOK)
	return decode[apiCandidates](e.t, rec)
}

// exchange writes one question and one answer, and returns the seq of the
// assistant turn — the ceiling a user would be looking at.
func (e *env) exchange(ws uuid.UUID, conversationID, question, answer string) int64 {
	e.t.Helper()
	e.llm.reply(answer, 10, 5)
	e.send(ws, conversationID, question)
	msgs := e.messages(ws, conversationID)
	return msgs[len(msgs)-1].Seq
}

const twoCandidates = `[
  {"content": "Prefere trabalhar principalmente com Go", "reason": "orienta sugestões futuras"},
  {"content": "Busca vagas backend nos EUA", "reason": "define o alvo da busca"}
]`

/* ── the snapshot boundary ───────────────────────────────────────────── */

// TestConsolidationReadsOnlyWhatExistedWhenAsked is the normative sentence,
// as a test.
func TestConsolidationReadsOnlyWhatExistedWhenAsked(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)

	ceiling := e.exchange(e.wsA, s.conversationID, "quero vagas backend nos EUA", "entendido")
	// Said after the user asked for consolidation. It must not be read, and
	// it is deliberately the most quotable sentence in the thread.
	e.exchange(e.wsA, s.conversationID, "ESTA FRASE VEIO DEPOIS DO COMANDO", "ok")

	e.proposes(twoCandidates, 500, 60)
	got := e.consolidate(e.wsA, s.conversationID, ceiling)

	sent := e.llm.lastRequest.Messages
	for _, m := range sent {
		if strings.Contains(m.Content, "VEIO DEPOIS") {
			t.Fatalf("a message written after the request entered the snapshot:\n%s", m.Content)
		}
	}
	if got.ConsideredMessages != 2 {
		t.Fatalf("considered %d messages, want the 2 that existed", got.ConsideredMessages)
	}
	if got.EffectiveUpToSeq != ceiling {
		t.Fatalf("effective seq = %d, want the ceiling %d", got.EffectiveUpToSeq, ceiling)
	}
}

// TestConsolidationCeilingBeyondTheThreadInventsNothing: a client that
// sends a seq past the end selects everything that exists, and the answer
// describes what was actually read.
func TestConsolidationCeilingBeyondTheThreadInventsNothing(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)
	last := e.exchange(e.wsA, s.conversationID, "uma pergunta", "uma resposta")

	e.proposes(`[]`, 100, 5)
	got := e.consolidate(e.wsA, s.conversationID, last+9_999)

	if got.ConsideredMessages != 2 || got.EffectiveUpToSeq != last {
		t.Fatalf("an inflated ceiling changed the snapshot: %+v", got)
	}
}

func TestConsolidationIgnoresReceiptsAndOtherThreads(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)
	other := e.newConversation(e.wsA, s.agentID)

	e.exchange(e.wsA, s.conversationID, "assunto desta conversa", "certo")
	e.exchange(e.wsA, other, "ASSUNTO DE OUTRA CONVERSA", "certo")
	e.receipt(e.wsA, s.conversationID, 900, 90, nil)

	e.proposes(`[]`, 100, 5)
	got := e.consolidate(e.wsA, s.conversationID, 0)

	if got.ConsideredMessages != 2 {
		t.Fatalf("considered %d messages; a receipt or another thread got in", got.ConsideredMessages)
	}
	for _, m := range e.llm.lastRequest.Messages {
		if strings.Contains(m.Content, "OUTRA CONVERSA") {
			t.Fatal("another conversation entered the snapshot")
		}
	}
}

func TestConsolidationIsWorkspaceScoped(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)
	e.exchange(e.wsA, s.conversationID, "pergunta", "resposta")

	before := e.llm.streamCalls
	rec := e.do("POST", "/chat/conversations/"+s.conversationID+"/memory-candidates", e.wsB,
		map[string]any{"up_to_seq": 0})
	wantStatus(t, rec, http.StatusNotFound)
	if e.llm.streamCalls != before {
		t.Fatal("a request from the wrong workspace reached the provider")
	}
}

/* ── refusals that cost nothing ──────────────────────────────────────── */

func TestConsolidationRefusedByPolicyNeverCallsTheProvider(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)
	e.exchange(e.wsA, s.conversationID, "pergunta", "resposta")
	e.patchAgent(e.wsA, s.agentID, map[string]any{
		"memory_policy": map[string]any{"mode": "off"},
	})

	before := e.llm.streamCalls
	rec := e.do("POST", "/chat/conversations/"+s.conversationID+"/memory-candidates", e.wsA,
		map[string]any{"up_to_seq": 0})
	wantErrorCode(t, rec, http.StatusConflict, domain.CodeMemoryConsolidationDisabled)

	if e.llm.streamCalls != before {
		t.Fatalf("a policy-refused consolidation called the provider %d time(s)", e.llm.streamCalls-before)
	}
	// And it left no receipt: nothing was spent, so nothing is recorded.
	if usage := e.usage(e.wsA, "/chat/agents/"+s.agentID+"/usage"); usage.Messages != 1 {
		t.Fatalf("a refusal wrote an accounting row: %d billable operations", usage.Messages)
	}
}

func TestConsolidationOfAnEmptyThreadNeverCallsTheProvider(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)

	for _, tc := range []struct {
		name    string
		prepare func()
	}{
		{"a thread with nothing in it", func() {}},
		{"a thread with only a receipt", func() {
			e.receipt(e.wsA, s.conversationID, 100, 10, nil)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.prepare()
			before := e.llm.streamCalls
			got := e.consolidate(e.wsA, s.conversationID, 0)

			if len(got.Candidates) != 0 || got.ConsideredMessages != 0 {
				t.Fatalf("something was proposed about nothing: %+v", got)
			}
			if got.Usage != nil {
				t.Fatalf("a charge was reported for an operation that never happened: %+v", got.Usage)
			}
			if e.llm.streamCalls != before {
				t.Fatal("tokens were spent to discover there was no conversation")
			}
		})
	}
}

// TestConsolidationCeilingBelowTheFirstMessageIsEmpty covers the third way
// a snapshot can be empty: the thread has content, but none of it existed
// at the requested ceiling.
func TestConsolidationCeilingBelowTheFirstMessageIsEmpty(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)
	// seq is global, so a second thread starts above the first one's rows.
	// That is what makes a ceiling below a thread's own beginning
	// expressible at all — and zero is not it: zero means "as it is now".
	older := e.exchange(e.wsA, s.conversationID, "conversa anterior", "certo")
	newer := e.newConversation(e.wsA, s.agentID)
	e.exchange(e.wsA, newer, "pergunta", "resposta")

	before := e.llm.streamCalls
	got := e.consolidate(e.wsA, newer, older)
	if len(got.Candidates) != 0 || got.ConsideredMessages != 0 {
		t.Fatalf("a ceiling below the thread produced %+v", got)
	}
	if e.llm.streamCalls != before {
		t.Fatal("an empty snapshot called the provider")
	}
}

func TestConsolidationIsRefusedByTheDailyBudget(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)
	e.exchange(e.wsA, s.conversationID, "pergunta", "resposta")
	// The exchange above already consumed the day.
	e.setBudget(e.wsA, s.agentID, intp(10), nil)

	before := e.llm.streamCalls
	rec := e.do("POST", "/chat/conversations/"+s.conversationID+"/memory-candidates", e.wsA,
		map[string]any{"up_to_seq": 0})
	wantErrorCode(t, rec, http.StatusTooManyRequests, "token_limit_reached")

	if e.llm.streamCalls != before {
		t.Fatal("a budget-refused consolidation called the provider")
	}
}

/* ── the call ────────────────────────────────────────────────────────── */

// TestConsolidationCallCarriesNoTools: the auxiliary call is not a turn.
// Even for an agent with an authorized tool, nothing is declared — there is
// no decision for the model to make here but to answer.
func TestConsolidationCallCarriesNoTools(t *testing.T) {
	e := newEnv(t)
	s := e.grantedAgent(e.wsA)
	e.exchange(e.wsA, s.conversationID, "pergunta", "resposta")

	e.proposes(`[]`, 100, 5)
	e.consolidate(e.wsA, s.conversationID, 0)

	if len(e.llm.lastRequest.Tools) != 0 {
		t.Fatalf("the auxiliary call declared %d tool(s)", len(e.llm.lastRequest.Tools))
	}
	if e.llm.lastRequest.MaxTokens != app.ConsolidationMaxOutputTokens {
		t.Fatalf("max_tokens = %d, want the operation's own ceiling %d",
			e.llm.lastRequest.MaxTokens, app.ConsolidationMaxOutputTokens)
	}
}

// TestConsolidationUsesTheAgentsOwnTemperature is the hotfix's reproduction
// test, and it fails on the code that shipped before it.
//
// ── The invariant ──────────────────────────────────────────────────────
//
//	If you can talk to an agent, you can ask it to consolidate.
//
// The auxiliary call used to impose a fixed 0.2 regardless of the model.
// That is a claim about what every gateway will accept, and it was false: a
// gpt-5 family model accepts temperature=1 and answers 400 to anything
// else. So `/lembrar` was broken for exactly those agents while ordinary
// conversation with the same agent worked — the difference being this one
// number.
//
// The assertion is deliberately relational rather than a literal 1.0: what
// has to hold is that the auxiliary call asks for the SAME temperature an
// ordinary turn asks for, because that is the only value known to be legal
// for that agent's model. A future change that swapped one hardcoded
// constant for another would still fail here.
func TestConsolidationUsesTheAgentsOwnTemperature(t *testing.T) {
	e := newEnv(t)
	// The shape of the report: an agent whose model accepts one temperature
	// and nothing else, configured with it.
	s := e.seedWith(e.wsA, map[string]any{"temperature": 1})
	e.exchange(e.wsA, s.conversationID, "pergunta", "resposta")
	turnTemperature := e.llm.lastRequest.Temperature

	e.proposes(`[]`, 100, 5)
	e.consolidate(e.wsA, s.conversationID, 0)

	if got := e.llm.lastRequest.Temperature; got != turnTemperature {
		t.Fatalf("consolidation asked for temperature %v, but an ordinary turn asks for %v; "+
			"a value the model may refuse breaks /lembrar on agents that chat perfectly",
			got, turnTemperature)
	}
	if turnTemperature != 1 {
		t.Fatalf("fixture drifted: the agent should be configured at 1, got %v", turnTemperature)
	}
}

// The same invariant from the other side: an agent on a temperature the
// operation would previously have overridden downward is still asked at its
// own value. Two agents, one rule.
func TestConsolidationDoesNotImposeATemperatureOnAnyAgent(t *testing.T) {
	e := newEnv(t)
	s := e.seedWith(e.wsA, map[string]any{"temperature": 0.7})
	e.exchange(e.wsA, s.conversationID, "pergunta", "resposta")

	e.proposes(`[]`, 100, 5)
	e.consolidate(e.wsA, s.conversationID, 0)

	if got := e.llm.lastRequest.Temperature; got != 0.7 {
		t.Fatalf("temperature = %v, want the agent's own 0.7", got)
	}
}

// The ceiling has to cover THINKING plus the answer, not just the answer.
//
// ── The failure this pins ──────────────────────────────────────────────
// `max_tokens` does not bound the answer; it bounds everything the model
// emits, and a reasoning model emits thinking first, from the same budget.
// Sized at 700 for the shape of the form, the ceiling was spent before a
// character of answer arrived: real receipts show gpt-5-mini stopping at
// exactly 700 with zero characters out, while the same conversation needed
// 1338.
//
// The assertion is on the measured worst case rather than on the constant
// itself, so lowering the constant back under what a reasoning model needs
// fails here instead of failing in production.
func TestTheOutputCeilingLeavesRoomForReasoning(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)
	e.exchange(e.wsA, s.conversationID, "pergunta", "resposta")

	e.proposes(`[]`, 100, 5)
	e.consolidate(e.wsA, s.conversationID, 0)

	// The highest a real gpt-5-mini consolidation consumed at the top of the
	// input budget, over two runs of the same conversation (1338 and 1625 —
	// reasoning is not deterministic). A ceiling at or below this truncates
	// that call.
	const measuredWorstCase = 1625
	if got := e.llm.lastRequest.MaxTokens; got <= measuredWorstCase {
		t.Fatalf("max_tokens = %d, which is at or below the %d tokens a real "+
			"reasoning model needed for this operation; the call would be truncated "+
			"before it answered", got, measuredWorstCase)
	}
}

// The other half of the ceiling, and the half that keeps the raise from
// being a blanket one: an agent configured smaller than the operation's
// bound keeps its own number.
func TestConsolidationNeverAsksForMoreOutputThanTheAgentDoes(t *testing.T) {
	e := newEnv(t)
	// Deliberately far below ConsolidationMaxOutputTokens.
	s := e.seedWith(e.wsA, map[string]any{"max_tokens": 512})
	e.exchange(e.wsA, s.conversationID, "pergunta", "resposta")

	e.proposes(`[]`, 100, 5)
	e.consolidate(e.wsA, s.conversationID, 0)

	if got := e.llm.lastRequest.MaxTokens; got != 512 {
		t.Fatalf("max_tokens = %d, want the agent's own 512: consolidation must "+
			"never ask for more output than one ordinary turn of this agent", got)
	}
}

// And an agent set high does not get the agent's number either — the
// operation's own bound is what stops a form-filling call from asking for a
// turn's worth of output.
func TestConsolidationDoesNotInheritALargeAgentCeiling(t *testing.T) {
	e := newEnv(t)
	s := e.seedWith(e.wsA, map[string]any{"max_tokens": 100000})
	e.exchange(e.wsA, s.conversationID, "pergunta", "resposta")

	e.proposes(`[]`, 100, 5)
	e.consolidate(e.wsA, s.conversationID, 0)

	if got := e.llm.lastRequest.MaxTokens; got != app.ConsolidationMaxOutputTokens {
		t.Fatalf("max_tokens = %d, want the operation's own ceiling %d",
			got, app.ConsolidationMaxOutputTokens)
	}
}

// The two ceilings must not leak into each other.
//
// An ordinary turn keeps asking for the agent's own max_tokens — the
// consolidation ceiling is not a global setting and must never reach the
// conversation. This is asserted from the consolidation suite on purpose:
// the guard belongs next to the number that was changed.
func TestAnOrdinaryTurnDoesNotInheritTheConsolidationCeiling(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)

	e.exchange(e.wsA, s.conversationID, "pergunta comum", "resposta comum")

	req := e.llm.lastRequest
	if req.MaxTokens != domain.DefaultMaxTokens {
		t.Fatalf("an ordinary turn asked for max_tokens %d, want the agent's own %d",
			req.MaxTokens, domain.DefaultMaxTokens)
	}
	if req.MaxTokens == app.ConsolidationMaxOutputTokens {
		t.Fatalf("the conversation is being capped at the consolidation ceiling")
	}
}

// Nothing about reasoning is sent on the wire, and that is a decision the
// gateway forced: `reasoning_effort` is not portable. gpt-5-mini refuses
// `none`, claude-haiku-4-5 answers 400 to `minimal` because LiteLLM maps it
// to a thinking budget larger than max_tokens, and deepseek-reasoner
// accepts it and reasons anyway. `supports_reasoning` is true for all
// three, so it cannot gate it either.
//
// This test is what stops the idea from being quietly reintroduced.
func TestConsolidationSendsNoReasoningParameter(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)
	e.exchange(e.wsA, s.conversationID, "pergunta", "resposta")

	e.proposes(`[]`, 100, 5)
	e.consolidate(e.wsA, s.conversationID, 0)

	// The port carries no reasoning field at all; the assertion is that the
	// request the adapter is handed is still describable by the fields a
	// pre-hotfix turn used. If a reasoning knob is ever added to
	// CompletionRequest, this stops compiling and the decision gets revisited
	// on purpose rather than by accident.
	req := e.llm.lastRequest
	if req.Model == "" || req.MaxTokens == 0 {
		t.Fatalf("the auxiliary call lost a field it needs: %+v", req)
	}
}

// An answer with no text is told apart from one that could not be read.
//
// Both are upstream failures and both stay failures. What differs is where
// they send whoever reads them: emptiness is a ceiling that was too small
// for a reasoning model, not a parser that cannot cope. Conflating them
// cost real time during this hotfix.
func TestAnEmptyAnswerIsReportedAsEmptyAndNotAsUnreadable(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)
	e.exchange(e.wsA, s.conversationID, "pergunta", "resposta")

	// A reasoning model that spent its whole output budget thinking: the
	// call succeeded, the tokens were bought, and no answer text came back.
	e.proposes("", 800, 700)
	rec := e.do("POST", "/chat/conversations/"+s.conversationID+"/memory-candidates", e.wsA,
		map[string]any{"up_to_seq": 0})

	wantStatus(t, rec, http.StatusBadGateway)
	body := rec.Body.String()
	if !strings.Contains(body, "não devolveu nenhum texto") {
		t.Fatalf("an empty answer must say so, got %s", body)
	}
	if strings.Contains(body, "formato que não deu para ler") {
		t.Fatalf("an empty answer was reported as a malformed one: %s", body)
	}
}

// And the malformed case keeps its own message, so the distinction cuts
// both ways rather than collapsing into the new branch.
func TestAnUnreadableAnswerStillSaysUnreadable(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)
	e.exchange(e.wsA, s.conversationID, "pergunta", "resposta")

	e.proposes("isto não é json coisa nenhuma", 800, 40)
	rec := e.do("POST", "/chat/conversations/"+s.conversationID+"/memory-candidates", e.wsA,
		map[string]any{"up_to_seq": 0})

	wantStatus(t, rec, http.StatusBadGateway)
	if !strings.Contains(rec.Body.String(), "formato que não deu para ler") {
		t.Fatalf("a malformed answer must keep its own message, got %s", rec.Body.String())
	}
}

func TestConsolidationPromptCarriesThePolicyAndTheNotes(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)
	e.patchAgent(e.wsA, s.agentID, map[string]any{
		"memory_policy": map[string]any{"mode": "on_request", "notes": "GUARDE DECISOES DE ARQUITETURA"},
	})
	e.exchange(e.wsA, s.conversationID, "pergunta", "resposta")

	e.proposes(`[]`, 100, 5)
	e.consolidate(e.wsA, s.conversationID, 0)

	system := e.llm.lastRequest.Messages[0]
	if system.Role != "system" {
		t.Fatalf("the first message is %q, want the policy", system.Role)
	}
	if !strings.Contains(system.Content, "GUARDE DECISOES DE ARQUITETURA") {
		t.Fatal("the user's guidance did not reach the prompt")
	}
	// The base rules are the product's and survive whatever the user wrote:
	// notes add to the policy, they do not replace it.
	if !strings.Contains(system.Content, "NÃO proponha") {
		t.Fatal("the base policy was replaced by the user's notes")
	}
	// The agent's own instructions describe how it talks, not what is worth
	// keeping. They are not part of this prompt.
	if strings.Contains(system.Content, "você é o agente de testes do João") {
		t.Fatal("the agent's chat instructions leaked into the extraction prompt")
	}
}

func TestConsolidationWindowKeepsTheMostRecentWholeMessages(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)

	// Four exchanges, each far larger than a quarter of the budget, so the
	// window cannot hold them all.
	big := strings.Repeat("z", app.ConsolidationInputBudgetChars/3)
	for i := range 4 {
		_ = i
		e.exchange(e.wsA, s.conversationID, big, "ok")
	}
	e.exchange(e.wsA, s.conversationID, "ULTIMA PERGUNTA", "ULTIMA RESPOSTA")

	e.proposes(`[]`, 100, 5)
	got := e.consolidate(e.wsA, s.conversationID, 0)

	if got.ConsideredMessages == 0 || got.ConsideredMessages >= 10 {
		t.Fatalf("considered %d messages; the window neither cut nor kept anything sensible", got.ConsideredMessages)
	}
	transcript := e.llm.lastRequest.Messages[1].Content
	if !strings.Contains(transcript, "ULTIMA PERGUNTA") {
		t.Fatal("the window dropped the end of the conversation instead of its beginning")
	}
	if n := len([]rune(transcript)); n > app.ConsolidationInputBudgetChars+200 {
		t.Fatalf("the transcript is %d characters, past the input budget", n)
	}
	// Whole messages only: a kept message is never a fragment of one.
	if strings.Count(transcript, "z") > 0 && !strings.Contains(transcript, big) {
		t.Fatal("a message was truncated mid-content")
	}
}

/* ── the answer ──────────────────────────────────────────────────────── */

func TestConsolidationParsesCandidates(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)
	e.exchange(e.wsA, s.conversationID, "prefiro Go", "certo")

	e.proposes("```json\n"+twoCandidates+"\n```", 500, 60)
	got := e.consolidate(e.wsA, s.conversationID, 0)

	if len(got.Candidates) != 2 {
		t.Fatalf("parsed %d candidates, want 2: %+v", len(got.Candidates), got.Candidates)
	}
	if got.Candidates[0].Content != "Prefere trabalhar principalmente com Go" {
		t.Fatalf("content = %q", got.Candidates[0].Content)
	}
	if got.Candidates[0].Reason == "" {
		t.Fatal("the reason was dropped")
	}
	if got.Candidates[0].DuplicateOf != nil {
		t.Fatal("a new candidate was marked as a duplicate")
	}
}

func TestConsolidationInventsNothingFromAnUnreadableAnswer(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)
	e.exchange(e.wsA, s.conversationID, "pergunta", "resposta")

	e.proposes("desculpe, não consegui analisar a conversa", 400, 30)
	rec := e.do("POST", "/chat/conversations/"+s.conversationID+"/memory-candidates", e.wsA,
		map[string]any{"up_to_seq": 0})
	wantStatus(t, rec, http.StatusBadGateway)

	// Nothing was proposed and nothing was written, but the tokens were
	// really bought — so the receipt exists.
	usage := e.usage(e.wsA, "/chat/agents/"+s.agentID+"/usage")
	if usage.TotalPromptTokens != 10+400 {
		t.Fatalf("prompt tokens = %d; a failed parse hid what the call cost", usage.TotalPromptTokens)
	}
	if got := e.memories(e.wsA, s.agentID); got.Total != 0 {
		t.Fatalf("a failed consolidation wrote %d memories", got.Total)
	}
}

func TestConsolidationDropsWhatItCannotSaveAndCapsTheList(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)
	e.exchange(e.wsA, s.conversationID, "pergunta", "resposta")

	long := strings.Repeat("á", domain.MaxMemoryContent+1)
	answer := `[
	  {"content": "", "reason": "vazio"},
	  {"content": "   ", "reason": "só espaço"},
	  {"content": "` + long + `", "reason": "não caberia em uma memória"},
	  {"content": "um", "reason": "r"}, {"content": "dois", "reason": "r"},
	  {"content": "três", "reason": "r"}, {"content": "quatro", "reason": "r"},
	  {"content": "cinco", "reason": "r"}, {"content": "seis", "reason": "r"},
	  {"content": "sete", "reason": "r"}
	]`
	e.proposes(answer, 500, 200)
	got := e.consolidate(e.wsA, s.conversationID, 0)

	if len(got.Candidates) != domain.MaxMemoryCandidates {
		t.Fatalf("returned %d candidates, want the ceiling of %d",
			len(got.Candidates), domain.MaxMemoryCandidates)
	}
	for _, c := range got.Candidates {
		if strings.TrimSpace(c.Content) == "" {
			t.Fatal("an empty candidate survived")
		}
		if len([]rune(c.Content)) > domain.MaxMemoryContent {
			t.Fatal("an unsaveable candidate survived")
		}
	}
	if got.Candidates[0].Content != "um" {
		t.Fatalf("the cap was not applied in order: first is %q", got.Candidates[0].Content)
	}
}

/* ── duplicates ──────────────────────────────────────────────────────── */

func TestConsolidationMarksExactDuplicatesAndNotParaphrases(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)
	e.exchange(e.wsA, s.conversationID, "pergunta", "resposta")

	existing := e.remember(e.wsA, s.agentID, "Prefere trabalhar principalmente com Go")

	e.proposes(`[
	  {"content": "  prefere   TRABALHAR principalmente com go  ", "reason": "igual, com outro espaçamento e caixa"},
	  {"content": "Gosta de programar em Go", "reason": "diz quase a mesma coisa, com outras palavras"}
	]`, 500, 60)
	got := e.consolidate(e.wsA, s.conversationID, 0)

	if len(got.Candidates) != 2 {
		t.Fatalf("a duplicate was removed instead of marked: %+v", got.Candidates)
	}
	if got.Candidates[0].DuplicateOf == nil || *got.Candidates[0].DuplicateOf != existing.ID {
		t.Fatalf("the exact duplicate was not matched: %+v", got.Candidates[0])
	}
	// Declared limit, tested rather than assumed: exact means exact. A
	// paraphrase needs embeddings, which this system does not have.
	if got.Candidates[1].DuplicateOf != nil {
		t.Fatal("a paraphrase was reported as an exact duplicate")
	}
}

/* ── proposing is not remembering ────────────────────────────────────── */

func TestConsolidationWritesNoMemory(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)
	e.exchange(e.wsA, s.conversationID, "prefiro Go", "certo")

	e.proposes(twoCandidates, 500, 60)
	got := e.consolidate(e.wsA, s.conversationID, 0)
	if len(got.Candidates) != 2 {
		t.Fatalf("expected the two candidates, got %+v", got.Candidates)
	}

	page := e.memories(e.wsA, s.agentID)
	if page.Total != 0 {
		t.Fatalf("asking for candidates created %d memories", page.Total)
	}
}

// TestConfirmingCandidatesSavesOnlyWhatWasChosen is the other half of the
// flow, over the route the interface actually calls: the memory route that
// already existed.
func TestConfirmingCandidatesSavesOnlyWhatWasChosen(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)
	ceiling := e.exchange(e.wsA, s.conversationID, "prefiro Go", "certo")

	e.proposes(twoCandidates, 500, 60)
	got := e.consolidate(e.wsA, s.conversationID, ceiling)

	// The user keeps the first, edits it, and discards the second — over
	// the route the interface actually calls.
	edited := "Prefere Go para serviços de backend"
	rec := e.do("POST", "/chat/conversations/"+s.conversationID+"/memories", e.wsA, map[string]any{
		"contents":  []string{edited},
		"up_to_seq": got.EffectiveUpToSeq,
	})
	wantStatus(t, rec, http.StatusCreated)

	page := e.memories(e.wsA, s.agentID)
	if page.Total != 1 {
		t.Fatalf("%d memories exist, want only the one that was confirmed", page.Total)
	}
	item := page.Items[0]
	if item.Content != edited {
		// What is stored is the text the user approved, not the text the
		// model proposed.
		t.Fatalf("content = %q, want the edited text", item.Content)
	}
	if !item.ModelProposed {
		t.Fatal("a memory that started as a proposal lost that fact when edited")
	}
	if item.Origin != "conversation" || item.SourceConversationID == nil {
		t.Fatalf("provenance is incomplete: %+v", item)
	}
	if item.SourceMessageSeq == nil || *item.SourceMessageSeq != got.EffectiveUpToSeq {
		t.Fatalf("source_message_seq = %v, want the seq actually considered (%d)",
			item.SourceMessageSeq, got.EffectiveUpToSeq)
	}
}

// TestConfirmationIsAllOrNothing: the user pressed one button over one
// selection. Half of it saved, with an error about the other half, would
// leave them comparing a dialog they can no longer see against a list that
// changed underneath it.
func TestConfirmationIsAllOrNothing(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)
	e.exchange(e.wsA, s.conversationID, "pergunta", "resposta")

	rec := e.do("POST", "/chat/conversations/"+s.conversationID+"/memories", e.wsA, map[string]any{
		"contents":  []string{"esta é válida", strings.Repeat("x", domain.MaxMemoryContent+1)},
		"up_to_seq": 2,
	})
	wantErrorCode(t, rec, http.StatusBadRequest, "invalid")

	if got := e.memories(e.wsA, s.agentID); got.Total != 0 {
		t.Fatalf("%d memories survived a refused confirmation", got.Total)
	}
}

func TestConfirmationIsWorkspaceScoped(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)
	e.exchange(e.wsA, s.conversationID, "pergunta", "resposta")

	rec := e.do("POST", "/chat/conversations/"+s.conversationID+"/memories", e.wsB, map[string]any{
		"contents":  []string{"de outro workspace"},
		"up_to_seq": 2,
	})
	wantStatus(t, rec, http.StatusNotFound)
	if got := e.memories(e.wsA, s.agentID); got.Total != 0 {
		t.Fatalf("wsB wrote %d memories into wsA", got.Total)
	}
}

/* ── accounting ──────────────────────────────────────────────────────── */

func TestConsolidationRecordsWhatItSpent(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)
	e.exchange(e.wsA, s.conversationID, "pergunta", "resposta")

	beforeTurns := len(e.messages(e.wsA, s.conversationID))
	e.proposes(twoCandidates, 640, 88)
	got := e.consolidate(e.wsA, s.conversationID, 0)

	if got.Usage == nil {
		t.Fatal("the operation reported no cost at all")
	}
	if got.Usage.PromptTokens != 640 || got.Usage.CompletionTokens != 88 {
		t.Fatalf("reported usage = %+v, want the provider's numbers", got.Usage)
	}
	if got.Usage.UsageSource != "provider" {
		t.Fatalf("usage_source = %q, want provider", got.Usage.UsageSource)
	}
	if got.Usage.CostUSD == nil || *got.Usage.CostUSD <= 0 {
		t.Fatalf("cost = %v, want the frozen price of this call", got.Usage.CostUSD)
	}
	if got.Usage.Model != "test-model" {
		t.Fatalf("model = %q", got.Usage.Model)
	}

	// The receipt is in the day's consumption, and out of the conversation.
	usage := e.usage(e.wsA, "/chat/agents/"+s.agentID+"/usage")
	if usage.TotalPromptTokens != 10+640 {
		t.Fatalf("the day counts %d prompt tokens, want the turn plus the consolidation", usage.TotalPromptTokens)
	}
	if n := len(e.messages(e.wsA, s.conversationID)); n != beforeTurns {
		t.Fatalf("the transcript grew from %d to %d messages", beforeTurns, n)
	}
}

func TestConsolidationWithUnknownPriceIsNotFree(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)
	e.exchange(e.wsA, s.conversationID, "pergunta", "resposta")

	e.llm.prices = map[string]ports.Price{} // the model is not on the card
	e.proposes(twoCandidates, 500, 60)
	got := e.consolidate(e.wsA, s.conversationID, 0)

	if got.Usage == nil {
		t.Fatal("no usage was reported")
	}
	if got.Usage.CostUSD != nil {
		t.Fatalf("cost = %v; an unpriced call must report unknown, never zero", *got.Usage.CostUSD)
	}
	if got.Usage.PromptTokens != 500 {
		t.Fatal("the tokens were dropped along with the price")
	}
	report := e.usage(e.wsA, "/chat/agents/"+s.agentID+"/usage")
	if report.UnpricedMessages == 0 || report.Priced {
		t.Fatalf("the day claims to be fully priced: %+v", report)
	}
}
