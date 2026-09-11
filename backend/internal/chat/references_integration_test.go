//go:build integration

// Contextual capabilities — Agents v1.1.0.
//
// The suite that has to be convincing about two sentences:
//
//	a selection can only ever REMOVE, never grant;
//	a turn with no selection behaves exactly as it did before this existed.
//
// Everything else here follows from those two. The registry, the
// authorization store, the scoping, the loop, the accounting, the budget
// gate, Postgres and every route are real; only the gateway is faked, which
// is the same boundary tool calling declared unverified and the same one this
// batch does not touch.
package chat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/chat/domain"
	"github.com/corsi/backend/internal/chat/ports"
)

/* ── a second real tool, for the tests that need two ─────────────────── */

// probeTool exists because scoping is only observable with more than one
// capability in play: "expose A and not B" cannot be stated, let alone
// tested, against a catalogue of one.
//
// It arrives through tools.Options.Extra — the documented seam a real
// integration will one day use — rather than by adding a second diagnostic
// to the production registry. A tool nobody asked for would be a product
// change smuggled in as test scaffolding.
type probeTool struct{}

const probeToolName = "system.probe"

func (probeTool) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:        probeToolName,
		Title:       "Probe",
		Description: "Returns a fixed marker. A second diagnostic, so scoping can be observed.",
		Effect:      domain.EffectRead,
		Internal:    true,
		Schema: domain.ToolSchema{
			Properties: map[string]domain.ToolProperty{
				"note": {Type: domain.TypeString, MaxLength: 200},
			},
		},
	}
}

func (probeTool) Execute(_ context.Context, _ map[string]any) (domain.ToolOutput, error) {
	return domain.ToolOutput{"probe": "ok"}, nil
}

/* ── helpers ─────────────────────────────────────────────────────────── */

// ref is one entry of the wire selection. Identity only — the label of the
// stored record is the server's to write, and a client that sent one would
// get a 400 from the strict decoder.
func ref(kind, id string) map[string]any {
	return map[string]any{"kind": kind, "id": id}
}

// sendWith runs a turn carrying an explicit selection and returns the raw
// recorder, because half these tests are about the refusal rather than the
// answer.
func (e *env) sendWith(ws uuid.UUID, conversationID, content string, refs []map[string]any) *httptest.ResponseRecorder {
	e.t.Helper()
	body := map[string]any{"content": content}
	if refs != nil {
		body["references"] = refs
	}
	return e.do("POST", "/chat/conversations/"+conversationID+"/messages", ws, body)
}

// twoToolAgent seeds the chain and authorizes both diagnostics, which is
// the smallest world in which "some, not all" is a statement.
func (e *env) twoToolAgent(ws uuid.UUID) seeded {
	e.t.Helper()
	s := e.seed(ws)
	e.authorizeTool(ws, s.agentID, echoTool)
	e.authorizeTool(ws, s.agentID, probeToolName)
	return s
}

// declaredTools is what the nth provider call (1-based) declared, by name.
func (e *env) declaredTools(call int) []string {
	e.t.Helper()
	if call > len(e.llm.requests) {
		e.t.Fatalf("provider call %d never happened; there were %d", call, len(e.llm.requests))
	}
	var out []string
	for _, d := range e.llm.requests[call-1].Tools {
		out = append(out, d.Name.String())
	}
	return out
}

func wantNames(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("tools = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("tools = %v, want %v", got, want)
		}
	}
}

// userTurns filters a transcript down to the rows a selection can hang off.
func (e *env) userTurns(ws uuid.UUID, conversationID string) []apiMessage {
	e.t.Helper()
	var out []apiMessage
	for _, m := range e.messages(ws, conversationID) {
		if m.Role == "user" {
			out = append(out, m)
		}
	}
	return out
}

/* ── 1. no selection is the legacy turn, unchanged ───────────────────── */

// The backward-compatibility test, and the one that would fail loudest if
// scoping ever leaked into the default path.
func TestNoSelectionDeclaresEveryAuthorizedTool(t *testing.T) {
	e := newEnv(t, withExtraTools(probeTool{}))
	s := e.twoToolAgent(e.wsA)

	e.llm.reply("pronto", 10, 3)
	e.send(e.wsA, s.conversationID, "sem seleção")

	// Registry order, which is name order: system.echo before system.probe.
	wantNames(t, e.declaredTools(1), echoTool, probeToolName)

	// And nothing is recorded, because nothing was selected. A stored `[]`
	// here would be the transcript claiming the user made an empty choice.
	turns := e.userTurns(e.wsA, s.conversationID)
	if len(turns) != 1 {
		t.Fatalf("user turns = %d, want 1", len(turns))
	}
	if turns[0].References != nil {
		t.Fatalf("references = %v, want absent on a turn that made no selection", turns[0].References)
	}
}

// An explicit empty array is the same input as no array at all. Telling
// them apart would create a difference nobody could see and everybody would
// eventually get wrong.
func TestAnEmptySelectionIsNoSelection(t *testing.T) {
	e := newEnv(t, withExtraTools(probeTool{}))
	s := e.twoToolAgent(e.wsA)

	e.llm.reply("pronto", 10, 3)
	rec := e.sendWith(e.wsA, s.conversationID, "seleção vazia", []map[string]any{})
	wantStatus(t, rec, http.StatusOK)

	wantNames(t, e.declaredTools(1), echoTool, probeToolName)
	if got := e.userTurns(e.wsA, s.conversationID)[0].References; got != nil {
		t.Fatalf("references = %v, want absent", got)
	}
}

// The agent nobody granted anything to. It was the fast path before this
// batch and it has to stay one: no `tools` field on the wire at all.
func TestAnAgentWithNoToolsIsUntouched(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)

	e.llm.reply("pronto", 10, 3)
	e.send(e.wsA, s.conversationID, "olá")

	if got := e.llm.requests[0].Tools; len(got) != 0 {
		t.Fatalf("tools = %v, want none declared", got)
	}
}

/* ── 2. a selection narrows, and only narrows ────────────────────────── */

func TestOneSelectedToolIsTheOnlyOneDeclared(t *testing.T) {
	e := newEnv(t, withExtraTools(probeTool{}))
	s := e.twoToolAgent(e.wsA)

	e.llm.reply("pronto", 10, 3)
	rec := e.sendWith(e.wsA, s.conversationID, "só o echo",
		[]map[string]any{ref("tool", echoTool)})
	wantStatus(t, rec, http.StatusOK)

	wantNames(t, e.declaredTools(1), echoTool)
}

func TestTwoSelectedToolsAreBothDeclared(t *testing.T) {
	e := newEnv(t, withExtraTools(probeTool{}))
	s := e.twoToolAgent(e.wsA)

	e.llm.reply("pronto", 10, 3)
	// Client order is reversed on purpose: the request body must be a
	// function of WHAT was selected, not of the order the chips happened to
	// be added in, or two identical turns build two different prompts.
	rec := e.sendWith(e.wsA, s.conversationID, "os dois",
		[]map[string]any{ref("tool", probeToolName), ref("tool", echoTool)})
	wantStatus(t, rec, http.StatusOK)

	wantNames(t, e.declaredTools(1), echoTool, probeToolName)
}

// Selecting the same capability twice is selecting it once. Harmless, so
// deduplicated rather than refused.
func TestASelectionIsDeduplicated(t *testing.T) {
	e := newEnv(t, withExtraTools(probeTool{}))
	s := e.twoToolAgent(e.wsA)

	e.llm.reply("pronto", 10, 3)
	rec := e.sendWith(e.wsA, s.conversationID, "duas vezes o mesmo",
		[]map[string]any{ref("tool", echoTool), ref("tool", echoTool)})
	wantStatus(t, rec, http.StatusOK)

	wantNames(t, e.declaredTools(1), echoTool)
	if got := e.userTurns(e.wsA, s.conversationID)[0].References; len(got) != 1 {
		t.Fatalf("stored references = %v, want one", got)
	}
}

/* ── 3. a selection never grants ─────────────────────────────────────── */

// The security property, stated as a request: a tool that exists and was
// never granted cannot be attached, and the attempt costs nothing.
func TestSelectingAnUnauthorizedToolRefusesTheTurn(t *testing.T) {
	e := newEnv(t, withExtraTools(probeTool{}))
	s := e.seed(e.wsA)
	e.authorizeTool(e.wsA, s.agentID, echoTool) // probe is deliberately NOT granted

	rec := e.sendWith(e.wsA, s.conversationID, "me dá o probe",
		[]map[string]any{ref("tool", probeToolName)})
	wantErrorCode(t, rec, http.StatusBadRequest, domain.CodeReferenceNotAuthorized)

	if e.llm.streamCalls != 0 {
		t.Fatalf("provider calls = %d, want 0: a refused selection must never reach the gateway",
			e.llm.streamCalls)
	}
	// And nothing was written. The composer still holds the text, and the
	// thread is exactly as it was.
	if got := e.messages(e.wsA, s.conversationID); len(got) != 0 {
		t.Fatalf("messages = %d, want 0: a refused turn is not a turn", len(got))
	}
}

func TestSelectingANonexistentToolRefusesTheTurn(t *testing.T) {
	e := newEnv(t)
	s := e.grantedAgent(e.wsA)

	rec := e.sendWith(e.wsA, s.conversationID, "inventei uma",
		[]map[string]any{ref("tool", "github.repository.read")})
	wantErrorCode(t, rec, http.StatusBadRequest, domain.CodeReferenceUnknownTool)

	if e.llm.streamCalls != 0 {
		t.Fatalf("provider calls = %d, want 0", e.llm.streamCalls)
	}
	if got := e.messages(e.wsA, s.conversationID); len(got) != 0 {
		t.Fatalf("messages = %d, want 0", len(got))
	}
}

// A name the registry could not hold even in principle. Same refusal as any
// other unknown one: from here there is nothing of that name to attach, and
// telling a client which flavour of impossible it chose helps nobody.
func TestSelectingAMalformedToolNameRefusesTheTurn(t *testing.T) {
	e := newEnv(t)
	s := e.grantedAgent(e.wsA)

	rec := e.sendWith(e.wsA, s.conversationID, "nome inválido",
		[]map[string]any{ref("tool", "NOT a tool name")})
	wantErrorCode(t, rec, http.StatusBadRequest, domain.CodeReferenceUnknownTool)
	if e.llm.streamCalls != 0 {
		t.Fatalf("provider calls = %d, want 0", e.llm.streamCalls)
	}
}

// A family this build does not implement. Told apart from a missing tool so
// a client that is ahead of the server learns that, instead of concluding
// the capability was deleted.
func TestAnUnknownReferenceKindRefusesTheTurn(t *testing.T) {
	e := newEnv(t)
	s := e.grantedAgent(e.wsA)

	for _, kind := range []string{"source", "memory", "agent", ""} {
		rec := e.sendWith(e.wsA, s.conversationID, "família inexistente",
			[]map[string]any{ref(kind, "qualquer-coisa")})
		wantErrorCode(t, rec, http.StatusBadRequest, domain.CodeReferenceKindUnknown)
	}
	if e.llm.streamCalls != 0 {
		t.Fatalf("provider calls = %d, want 0", e.llm.streamCalls)
	}
	if got := e.messages(e.wsA, s.conversationID); len(got) != 0 {
		t.Fatalf("messages = %d, want 0", len(got))
	}
}

// The label is the server's. A client that tries to author the permanent
// record is refused by the strict decoder rather than quietly ignored —
// silently dropping a field a caller asserted is how two sides come to
// believe different things about the same row.
func TestAClientMayNotWriteTheStoredLabel(t *testing.T) {
	e := newEnv(t)
	s := e.grantedAgent(e.wsA)

	rec := e.sendWith(e.wsA, s.conversationID, "rótulo meu",
		[]map[string]any{{"kind": "tool", "id": echoTool, "label": "GitHub"}})
	wantStatus(t, rec, http.StatusBadRequest)
	if e.llm.streamCalls != 0 {
		t.Fatalf("provider calls = %d, want 0", e.llm.streamCalls)
	}
}

// Scoping narrows the turn's whole capability set, not just the
// declaration. A model that asks for a withheld tool anyway — a
// hallucination, or a prompt injection reading a name off the history —
// is refused by the same per-call gate that refuses an ungranted one.
func TestAWithheldToolCannotBeCalledEvenIfTheModelAsks(t *testing.T) {
	e := newEnv(t, withExtraTools(probeTool{}))
	s := e.twoToolAgent(e.wsA)

	e.llm.rounds = [][]ports.StreamEvent{
		askTool("call_1", probeToolName, `{"note":"tentando"}`),
		answer("não consegui", 40, 5),
	}
	rec := e.sendWith(e.wsA, s.conversationID, "só o echo",
		[]map[string]any{ref("tool", echoTool)})
	wantStatus(t, rec, http.StatusOK)

	calls := e.toolCalls(e.wsA, s.conversationID)
	if len(calls) != 1 {
		t.Fatalf("tool calls = %d, want 1", len(calls))
	}
	if calls[0].Status == "ok" || calls[0].ErrorCode != string(domain.ToolErrNotAuthorized) {
		t.Fatalf("call = %+v, want a not-authorized refusal", calls[0])
	}
}

/* ── 4. the scope holds for the whole turn ───────────────────────────── */

// A turn is several provider calls now. The selection was made once, for
// the turn, and a later round must not quietly recover what the first one
// was scoped out of — a continuation that widened capability would be a
// way around the whole mechanism.
func TestEveryRoundOfATurnCarriesTheSameScope(t *testing.T) {
	e := newEnv(t, withExtraTools(probeTool{}))
	s := e.twoToolAgent(e.wsA)

	e.llm.rounds = [][]ports.StreamEvent{
		askEcho("call_1", "primeiro"),
		askEcho("call_2", "segundo"),
		answer("terminei", 60, 8),
	}
	rec := e.sendWith(e.wsA, s.conversationID, "usa o echo duas vezes",
		[]map[string]any{ref("tool", echoTool)})
	wantStatus(t, rec, http.StatusOK)

	if e.llm.streamCalls != 3 {
		t.Fatalf("provider calls = %d, want 3", e.llm.streamCalls)
	}
	for call := 1; call <= 3; call++ {
		wantNames(t, e.declaredTools(call), echoTool)
	}
}

/* ── 5. the record, and what it survives ─────────────────────────────── */

func TestTheSelectionIsStoredOnTheTurnAndSurvivesAReload(t *testing.T) {
	e := newEnv(t, withExtraTools(probeTool{}))
	s := e.twoToolAgent(e.wsA)

	e.llm.reply("pronto", 10, 3)
	rec := e.sendWith(e.wsA, s.conversationID, "com seleção",
		[]map[string]any{ref("tool", echoTool)})
	wantStatus(t, rec, http.StatusOK)

	// Read back through the transcript route, which is what a reload does.
	turns := e.userTurns(e.wsA, s.conversationID)
	if len(turns) != 1 {
		t.Fatalf("user turns = %d, want 1", len(turns))
	}
	got := turns[0].References
	if len(got) != 1 {
		t.Fatalf("references = %v, want one", got)
	}
	if got[0].Kind != "tool" || got[0].ID != echoTool {
		t.Fatalf("reference = %+v, want the canonical identity", got[0])
	}
	// The label is frozen from this build's own definition, not echoed from
	// the request — the request never carried one.
	if got[0].Label != "Echo" {
		t.Fatalf("label = %q, want the title the registry had at the time", got[0].Label)
	}

	// The assistant turn carries none. A selection is the user's act.
	for _, m := range e.assistantTurns(e.wsA, s.conversationID) {
		if m.References != nil {
			t.Fatalf("assistant turn carries references: %v", m.References)
		}
	}
}

// The historical-truth test. Everything the label could be resolved against
// is taken away afterwards, and the record still reports what happened.
func TestHistoryKeepsASelectionAfterTheGrantIsRevoked(t *testing.T) {
	e := newEnv(t, withExtraTools(probeTool{}))
	s := e.twoToolAgent(e.wsA)

	e.llm.reply("pronto", 10, 3)
	rec := e.sendWith(e.wsA, s.conversationID, "enquanto podia",
		[]map[string]any{ref("tool", echoTool)})
	wantStatus(t, rec, http.StatusOK)

	// The world moves on: the grant is revoked.
	del := e.do("DELETE", "/chat/agents/"+s.agentID+"/tools/"+echoTool, e.wsA, nil)
	wantStatus(t, del, http.StatusNoContent)
	report := e.agentTools(e.wsA, s.agentID)
	if report.view(t, echoTool).Authorized {
		t.Fatalf("the grant should be gone")
	}

	// The turn still says what it said. A transcript that resolved its
	// labels against the catalogue as it stands now would have to render
	// this turn as having selected nothing, which is a lie about the past.
	got := e.userTurns(e.wsA, s.conversationID)[0].References
	if len(got) != 1 || got[0].ID != echoTool || got[0].Label != "Echo" {
		t.Fatalf("references = %+v, want the frozen record", got)
	}
}

// The lifecycle is the message's, and it comes from the column being on the
// row rather than from a cascade anybody has to remember to write. Nothing
// can be orphaned, because there is no second row to orphan.
func TestTruncatingATurnTakesItsSelectionWithIt(t *testing.T) {
	e := newEnv(t, withExtraTools(probeTool{}))
	s := e.twoToolAgent(e.wsA)

	e.llm.reply("primeira", 10, 3)
	wantStatus(t, e.sendWith(e.wsA, s.conversationID, "com seleção",
		[]map[string]any{ref("tool", echoTool)}), http.StatusOK)

	turns := e.userTurns(e.wsA, s.conversationID)
	seq := turns[0].Seq

	del := e.do("DELETE", "/chat/conversations/"+s.conversationID+"/messages/"+itoa64(seq), e.wsA, nil)
	wantStatus(t, del, http.StatusOK)

	if got := e.messages(e.wsA, s.conversationID); len(got) != 0 {
		t.Fatalf("messages = %d, want 0 after truncating from the first turn", len(got))
	}
	// No row anywhere still points at the message that is gone.
	var orphans int
	if err := e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM chat.messages
		 WHERE conversation_id = $1 AND turn_references IS NOT NULL`,
		s.conversationID).Scan(&orphans); err != nil {
		t.Fatalf("count references: %v", err)
	}
	if orphans != 0 {
		t.Fatalf("rows with a selection = %d, want 0", orphans)
	}
}

// Regenerate replaces a turn. The replacement is a new act, and it carries
// whatever the composer sent this time — not what the deleted turn carried.
func TestARegeneratedTurnDoesNotInheritTheOldSelection(t *testing.T) {
	e := newEnv(t, withExtraTools(probeTool{}))
	s := e.twoToolAgent(e.wsA)

	e.llm.reply("primeira", 10, 3)
	wantStatus(t, e.sendWith(e.wsA, s.conversationID, "pergunta",
		[]map[string]any{ref("tool", echoTool)}), http.StatusOK)

	seq := e.userTurns(e.wsA, s.conversationID)[0].Seq
	wantStatus(t, e.do("DELETE",
		"/chat/conversations/"+s.conversationID+"/messages/"+itoa64(seq), e.wsA, nil), http.StatusOK)

	// The same question, asked again with no selection: the legacy turn.
	e.llm.reply("segunda", 10, 3)
	e.send(e.wsA, s.conversationID, "pergunta")

	if got := e.userTurns(e.wsA, s.conversationID)[0].References; got != nil {
		t.Fatalf("references = %v, want absent: the selection was not resent", got)
	}
	wantNames(t, e.declaredTools(2), echoTool, probeToolName)
}

/* ── 6. what the report says about it ────────────────────────────────── */

// The Inspector must be able to say "1 of 2" without holding names and
// without asking the grants as they stand today, which is a different
// question about a different moment.
func TestTheReportRecordsWhatTheScopeWithheld(t *testing.T) {
	e := newEnv(t, withExtraTools(probeTool{}))
	s := e.twoToolAgent(e.wsA)

	e.llm.reply("pronto", 10, 3)
	wantStatus(t, e.sendWith(e.wsA, s.conversationID, "só o echo",
		[]map[string]any{ref("tool", echoTool)}), http.StatusOK)

	turns := e.assistantTurns(e.wsA, s.conversationID)
	if len(turns) != 1 || turns[0].ContextReport == nil {
		t.Fatalf("want one assistant turn with a report")
	}
	block := turns[0].ContextReport.block(t, "tools")
	if block.Items != 1 {
		t.Fatalf("tools block items = %d, want 1 exposed", block.Items)
	}
	var withheld int
	for _, ex := range block.Exclusions {
		if ex.Reason == "not_selected" {
			withheld = ex.Items
		}
	}
	if withheld != 1 {
		t.Fatalf("not_selected items = %d, want 1", withheld)
	}
	if block.Characters == 0 {
		t.Fatalf("the exposed tool still costs characters")
	}
}

// A turn with no selection carries no such exclusion. The report of an
// ordinary turn is byte-for-byte the report it was before this batch.
func TestAnUnscopedTurnReportsNoExclusion(t *testing.T) {
	e := newEnv(t, withExtraTools(probeTool{}))
	s := e.twoToolAgent(e.wsA)

	e.llm.reply("pronto", 10, 3)
	e.send(e.wsA, s.conversationID, "sem seleção")

	block := e.assistantTurns(e.wsA, s.conversationID)[0].ContextReport.block(t, "tools")
	if block.Items != 2 {
		t.Fatalf("tools block items = %d, want 2", block.Items)
	}
	if len(block.Exclusions) != 0 {
		t.Fatalf("exclusions = %+v, want none", block.Exclusions)
	}
}

/* ── 7. nothing else moved ───────────────────────────────────────────── */

// Scoping is a decision about which capabilities are declared. It is not a
// discount, not a surcharge, and not a second budget: the money and the
// tokens are counted exactly as they were.
func TestScopingDoesNotDisturbAccounting(t *testing.T) {
	e := newEnv(t, withExtraTools(probeTool{}))
	s := e.twoToolAgent(e.wsA)

	e.llm.prices = map[string]ports.Price{"test-model": {InputCostPerToken: 1e-6, OutputCostPerToken: 3e-6}}
	e.llm.reply("pronto", 100, 10)
	wantStatus(t, e.sendWith(e.wsA, s.conversationID, "com seleção",
		[]map[string]any{ref("tool", echoTool)}), http.StatusOK)

	turn := e.assistantTurns(e.wsA, s.conversationID)[0]
	if turn.PromptTokens != 100 || turn.CompletionTokens != 10 {
		t.Fatalf("usage = %d/%d, want 100/10", turn.PromptTokens, turn.CompletionTokens)
	}
	if turn.UsageSource != "provider" {
		t.Fatalf("usage_source = %q, want provider", turn.UsageSource)
	}
	if turn.Cost == nil || !nearly(*turn.Cost, 100*1e-6+10*3e-6) {
		t.Fatalf("cost = %v, want the ordinary figure", turn.Cost)
	}
}

// The budget refuses before the selection is even looked at, and a refused
// selection is refused before the budget is consulted. Neither gate
// swallows the other, and neither one calls the provider.
func TestABudgetRefusalIsUnaffectedByASelection(t *testing.T) {
	e := newEnv(t, withExtraTools(probeTool{}))
	s := e.seed(e.wsA)
	e.authorizeTool(e.wsA, s.agentID, echoTool)
	e.setBudget(e.wsA, s.agentID, intp(0), nil)

	rec := e.sendWith(e.wsA, s.conversationID, "com seleção",
		[]map[string]any{ref("tool", echoTool)})
	wantErrorCode(t, rec, http.StatusTooManyRequests, "token_limit_reached")
	if e.llm.streamCalls != 0 {
		t.Fatalf("provider calls = %d, want 0", e.llm.streamCalls)
	}
	if got := e.messages(e.wsA, s.conversationID); len(got) != 0 {
		t.Fatalf("messages = %d, want 0", len(got))
	}
}

// A selection is scoped to its own workspace by the same rule everything
// else is: the conversation is resolved first, so a reference cannot reach
// across into an agent it does not own.
func TestASelectionCannotReachAnotherWorkspace(t *testing.T) {
	e := newEnv(t, withExtraTools(probeTool{}))
	s := e.twoToolAgent(e.wsA)

	rec := e.sendWith(e.wsB, s.conversationID, "de outro workspace",
		[]map[string]any{ref("tool", echoTool)})
	wantStatus(t, rec, http.StatusNotFound)
	if e.llm.streamCalls != 0 {
		t.Fatalf("provider calls = %d, want 0", e.llm.streamCalls)
	}
}

/* ── selecting a whole integration ───────────────────────────────────── */

// The `@` menu offers providers now, not capabilities. What travels is a
// namespace; what the turn gets is the intersection with its grants; what
// the record keeps is the individual capabilities that were really exposed.
//
// integrationRef is one such selection on the wire.
func integrationRef(namespace string) map[string]any { return ref("integration", namespace) }

// E · selecting an integration scopes the turn to that integration.
func TestSelectingAnIntegrationExposesItsAuthorizedCapabilities(t *testing.T) {
	e := newEnv(t, withExtraTools(probeTool{}))
	s := e.twoToolAgent(e.wsA)

	e.scriptNext(answer("ok", 40, 10))
	rec := e.sendWith(e.wsA, s.conversationID, "olá", []map[string]any{integrationRef("system")})
	wantStatus(t, rec, http.StatusOK)

	// Both system capabilities, because both are granted.
	wantNames(t, e.declaredTools(1), echoTool, probeToolName)
}

// F · a selection still cannot grant. The provider is named, but only what
// the operator authorized comes through.
func TestSelectingAnIntegrationDoesNotGrantItsUnauthorizedCapabilities(t *testing.T) {
	e := newEnv(t, withExtraTools(probeTool{}))
	s := e.seed(e.wsA)
	// One of the two, deliberately.
	e.authorizeTool(e.wsA, s.agentID, echoTool)

	e.scriptNext(answer("ok", 40, 10))
	rec := e.sendWith(e.wsA, s.conversationID, "olá", []map[string]any{integrationRef("system")})
	wantStatus(t, rec, http.StatusOK)

	wantNames(t, e.declaredTools(1), echoTool)
}

// And a provider with nothing granted is refused rather than silently
// scoping the turn to nothing — the same rule an unauthorized tool follows.
func TestSelectingAnIntegrationWithNoGrantsIsRefused(t *testing.T) {
	e := newEnv(t, withExtraTools(probeTool{}))
	s := e.seed(e.wsA)

	rec := e.sendWith(e.wsA, s.conversationID, "olá", []map[string]any{integrationRef("system")})
	wantErrorCode(t, rec, http.StatusBadRequest, "reference_not_authorized")

	// Refused before anything happened: no turn, no provider call.
	if got := len(e.messages(e.wsA, s.conversationID)); got != 0 {
		t.Fatalf("the conversation has %d messages after a refusal", got)
	}
}

// An integration this build has never heard of is a different sentence, so
// a client that is ahead of the server learns that rather than concluding
// the operator revoked something.
func TestSelectingAnIntegrationThatDoesNotExistIsRefusedAsUnknown(t *testing.T) {
	e := newEnv(t)
	s := e.grantedAgent(e.wsA)

	rec := e.sendWith(e.wsA, s.conversationID, "olá", []map[string]any{integrationRef("linear")})
	wantErrorCode(t, rec, http.StatusBadRequest, "reference_unknown_tool")
}

// G and H · one provider's selection never reaches another's capabilities.
func TestAnIntegrationSelectionDoesNotCrossProviders(t *testing.T) {
	e := newEnv(t, withExtraTools(probeTool{}, mutateTool{}))
	s := e.seed(e.wsA)
	e.authorizeTool(e.wsA, s.agentID, echoTool)
	e.authorizeTool(e.wsA, s.agentID, mutateToolName)

	// `system` covers echo and mutate — they share a namespace — so this
	// asserts the boundary with a provider that does NOT.
	e.scriptNext(answer("ok", 40, 10))
	rec := e.sendWith(e.wsA, s.conversationID, "olá", []map[string]any{integrationRef("system")})
	wantStatus(t, rec, http.StatusOK)

	for _, name := range e.declaredTools(1) {
		if !strings.HasPrefix(name, "system.") {
			t.Fatalf("selecting `system` exposed %q", name)
		}
	}
}

// M and N · what is frozen is the capabilities, not the group.
//
// This is the property that keeps an old turn honest: the record names the
// capabilities that were really in scope, so a provider gaining an eighth
// one tomorrow cannot make yesterday's turn look as though it had it.
func TestAnIntegrationSelectionFreezesTheCapabilitiesItExpandedTo(t *testing.T) {
	e := newEnv(t, withExtraTools(probeTool{}))
	s := e.twoToolAgent(e.wsA)

	e.scriptNext(answer("ok", 40, 10))
	rec := e.sendWith(e.wsA, s.conversationID, "olá", []map[string]any{integrationRef("system")})
	wantStatus(t, rec, http.StatusOK)

	turns := e.messages(e.wsA, s.conversationID)
	stored := turns[0].References
	if len(stored) != 2 {
		t.Fatalf("stored references = %+v, want one per exposed capability", stored)
	}
	for _, r := range stored {
		if r.Kind != "tool" {
			t.Fatalf("stored a %q reference; the record is capabilities", r.Kind)
		}
		if r.Label == "" {
			t.Fatalf("stored no label for %s", r.ID)
		}
	}
	names := []string{stored[0].ID, stored[1].ID}
	wantNames(t, names, echoTool, probeToolName)
}

// The record is what was exposed, so a turn scoped to a provider whose
// grants are partial names only the partial set — for ever.
func TestTheFrozenSetIsWhatWasExposedAndNotWhatTheProviderOffers(t *testing.T) {
	e := newEnv(t, withExtraTools(probeTool{}))
	s := e.seed(e.wsA)
	e.authorizeTool(e.wsA, s.agentID, echoTool)

	e.scriptNext(answer("ok", 40, 10))
	rec := e.sendWith(e.wsA, s.conversationID, "olá", []map[string]any{integrationRef("system")})
	wantStatus(t, rec, http.StatusOK)

	stored := e.messages(e.wsA, s.conversationID)[0].References
	if len(stored) != 1 || stored[0].ID != echoTool {
		t.Fatalf("stored = %+v, want only the capability that was exposed", stored)
	}

	// Granting the other one now changes nothing about the turn that ran.
	e.authorizeTool(e.wsA, s.agentID, probeToolName)
	after := e.messages(e.wsA, s.conversationID)[0].References
	if len(after) != 1 || after[0].ID != echoTool {
		t.Fatalf("the stored scope changed to %+v when a grant was added", after)
	}
}

// J · two providers in one turn.
func TestTwoIntegrationsSelectedTogetherExposeBoth(t *testing.T) {
	e := newEnv(t, withExtraTools(probeTool{}))
	s := e.twoToolAgent(e.wsA)

	e.scriptNext(answer("ok", 40, 10))
	rec := e.sendWith(e.wsA, s.conversationID, "olá",
		[]map[string]any{integrationRef("system"), integrationRef("system")})
	wantStatus(t, rec, http.StatusOK)

	// The same provider twice is the same provider once.
	wantNames(t, e.declaredTools(1), echoTool, probeToolName)
	if got := len(e.messages(e.wsA, s.conversationID)[0].References); got != 2 {
		t.Fatalf("stored %d references for a duplicated selection", got)
	}
}

// I · no selection is still the legacy turn.
func TestNoSelectionStillDeclaresEverythingAuthorized(t *testing.T) {
	e := newEnv(t, withExtraTools(probeTool{}))
	s := e.twoToolAgent(e.wsA)

	e.scriptNext(answer("ok", 40, 10))
	e.send(e.wsA, s.conversationID, "olá")

	wantNames(t, e.declaredTools(1), echoTool, probeToolName)
	if got := e.messages(e.wsA, s.conversationID)[0].References; len(got) != 0 {
		t.Fatalf("references = %v, want none on a turn that made no selection", got)
	}
}

// O · the report still tells the truth about a scoped turn.
func TestTheReportCountsAnIntegrationScopeAsTheCapabilitiesItExposed(t *testing.T) {
	e := newEnv(t, withExtraTools(probeTool{}, mutateTool{}))
	s := e.seed(e.wsA)
	e.authorizeTool(e.wsA, s.agentID, echoTool)
	e.authorizeTool(e.wsA, s.agentID, probeToolName)
	e.authorizeTool(e.wsA, s.agentID, mutateToolName)

	e.scriptNext(answer("ok", 40, 10))
	// `system` covers all three, so scope with a selection that covers two
	// of them by naming them individually — the wire still accepts that.
	rec := e.sendWith(e.wsA, s.conversationID, "olá",
		[]map[string]any{ref("tool", echoTool), ref("tool", probeToolName)})
	wantStatus(t, rec, http.StatusOK)

	turns := e.assistantTurns(e.wsA, s.conversationID)
	block := turns[len(turns)-1].ContextReport.block(t, "tools")
	if block.Items != 2 {
		t.Fatalf("tools block reports %d declared, want 2", block.Items)
	}
	if !hasExclusion(block, "not_selected", 1) {
		t.Fatalf("exclusions = %+v, want one withheld", block.Exclusions)
	}
}
