//go:build integration

// Tools Foundation.
//
// The suite that has to be convincing about one sentence: an agent can only
// use what it was explicitly granted, and the model does not get a vote.
// Everything else here — the loop, the accounting, the budget, the audit
// trail — exists because tool calling changes a turn from one provider call
// into several, and every property the module already had must survive that.
//
// The LLM port is the same scripted fake the rest of the module uses. What
// is real: the registry, the authorization store, the schema validator, the
// executor, the loop, the accounting, the budget gate, Postgres, and every
// route. What is not: the gateway. The specific external boundary of tool
// calling is declared unverified.
package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/chat/domain"
	"github.com/corsi/backend/internal/chat/ports"
)

/* ── helpers ─────────────────────────────────────────────────────────── */

// echoTool is the one tool every deployment has. Named here so a rename in
// the registry breaks this suite loudly instead of quietly testing nothing.
const echoTool = "system.echo"

type apiToolView struct {
	Name        string `json:"name"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Effect      string `json:"effect"`
	Internal    bool   `json:"internal"`
	Authorized  bool   `json:"authorized"`
	Schema      struct {
		Properties map[string]struct {
			Type        string `json:"type"`
			Description string `json:"description"`
			MaxLength   int    `json:"max_length"`
		} `json:"properties"`
		Required []string `json:"required"`
	} `json:"schema"`
}

type apiToolsReport struct {
	Items           []apiToolView `json:"items"`
	AuthorizedCount int           `json:"authorized_count"`
	Stale           []string      `json:"stale"`
}

func (e *env) agentTools(ws uuid.UUID, agentID string) apiToolsReport {
	e.t.Helper()
	rec := e.do("GET", "/chat/agents/"+agentID+"/tools", ws, nil)
	wantStatus(e.t, rec, http.StatusOK)
	return decode[apiToolsReport](e.t, rec)
}

func (e *env) authorizeTool(ws uuid.UUID, agentID, name string) apiToolsReport {
	e.t.Helper()
	rec := e.do("POST", "/chat/agents/"+agentID+"/tools", ws,
		map[string]any{"tool_name": name})
	wantStatus(e.t, rec, http.StatusOK)
	return decode[apiToolsReport](e.t, rec)
}

func (r apiToolsReport) view(t *testing.T, name string) apiToolView {
	t.Helper()
	for _, v := range r.Items {
		if v.Name == name {
			return v
		}
	}
	t.Fatalf("the catalogue has no %s: %+v", name, r.Items)
	return apiToolView{}
}

type apiToolCall struct {
	ID             string  `json:"id"`
	MessageID      string  `json:"message_id"`
	ConversationID string  `json:"conversation_id"`
	Round          int     `json:"round"`
	ProviderCallID string  `json:"provider_call_id"`
	ToolName       string  `json:"tool_name"`
	Arguments      *string `json:"arguments"`
	Result         *string `json:"result"`
	Status         string  `json:"status"`
	ErrorCode      string  `json:"error_code"`
	ErrorMessage   string  `json:"error_message"`
	DurationMS     int     `json:"duration_ms"`
	Redacted       bool    `json:"redacted"`
	CreatedAt      string  `json:"created_at"`
}

func (e *env) toolCalls(ws uuid.UUID, conversationID string) []apiToolCall {
	e.t.Helper()
	rec := e.do("GET", "/chat/conversations/"+conversationID+"/tool-calls", ws, nil)
	wantStatus(e.t, rec, http.StatusOK)
	return decode[struct {
		Items []apiToolCall `json:"items"`
	}](e.t, rec).Items
}

// askEcho is the script of a provider call that requests the echo tool.
func askEcho(id, text string) []ports.StreamEvent {
	return askTool(id, echoTool, `{"text":`+quote(text)+`}`)
}

// askTool is the same for any name and any argument string, including ones
// the schema will refuse.
func askTool(id, name, args string) []ports.StreamEvent {
	return []ports.StreamEvent{
		{FinishReason: "tool_calls",
			ToolCalls: []domain.ToolCall{{ID: id, Name: domain.ToolName(name), Arguments: args}},
			Usage:     &ports.Usage{PromptTokens: 30, CompletionTokens: 8}},
	}
}

// answer is the script of a provider call that just replies.
func answer(text string, prompt, completion int) []ports.StreamEvent {
	return []ports.StreamEvent{
		{Delta: text},
		{FinishReason: "stop", Usage: &ports.Usage{PromptTokens: prompt, CompletionTokens: completion}},
	}
}

func quote(s string) string {
	raw, _ := json.Marshal(s)
	return string(raw)
}

// grantedAgent seeds the usual chain and authorizes the echo tool on it.
func (e *env) grantedAgent(ws uuid.UUID) seeded {
	e.t.Helper()
	s := e.seed(ws)
	e.authorizeTool(ws, s.agentID, echoTool)
	return s
}

/* ── the catalogue ───────────────────────────────────────────────────── */

// The registry is the source of which tools exist, and it is code. The API
// reports the catalogue per agent, because "which tools exist" is never the
// interesting question — "which may THIS agent use" is.
func TestToolCatalogueComesFromTheRegistry(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)

	report := e.agentTools(e.wsA, s.agentID)
	if len(report.Items) == 0 {
		t.Fatal("the catalogue is empty; the built-in registry has at least one tool")
	}
	echo := report.view(t, echoTool)
	if echo.Title == "" || echo.Description == "" {
		t.Error("a tool must carry a human name and a description")
	}
	if echo.Effect != string(domain.EffectRead) {
		t.Errorf("effect = %q, want read", echo.Effect)
	}
	if !echo.Internal {
		t.Error("system.echo must be declared internal: it is a test instrument")
	}
	if _, ok := echo.Schema.Properties["text"]; !ok {
		t.Errorf("the declared schema has no `text` property: %+v", echo.Schema)
	}
}

// DENY by default, and the default is the absence of a row. A brand new
// agent has nothing, and no migration invented anything for the agents that
// existed before this feature.
func TestANewAgentHasNoAuthorizedTools(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)

	report := e.agentTools(e.wsA, s.agentID)
	if report.AuthorizedCount != 0 {
		t.Fatalf("authorized_count = %d, want 0 for a new agent", report.AuthorizedCount)
	}
	for _, v := range report.Items {
		if v.Authorized {
			t.Errorf("%s is authorized on a new agent", v.Name)
		}
	}
}

func TestAuthorizeAndRevoke(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)

	report := e.authorizeTool(e.wsA, s.agentID, echoTool)
	if !report.view(t, echoTool).Authorized || report.AuthorizedCount != 1 {
		t.Fatalf("after authorizing: %+v", report)
	}

	// Granting twice is the same state as granting once.
	report = e.authorizeTool(e.wsA, s.agentID, echoTool)
	if report.AuthorizedCount != 1 {
		t.Fatalf("a second grant produced authorized_count = %d", report.AuthorizedCount)
	}

	rec := e.do("DELETE", "/chat/agents/"+s.agentID+"/tools/"+echoTool, e.wsA, nil)
	wantStatus(t, rec, http.StatusNoContent)
	if e.agentTools(e.wsA, s.agentID).AuthorizedCount != 0 {
		t.Fatal("the grant survived a revoke")
	}

	// Revoking what was never granted is the requested state, not an error.
	rec = e.do("DELETE", "/chat/agents/"+s.agentID+"/tools/"+echoTool, e.wsA, nil)
	wantStatus(t, rec, http.StatusNoContent)
}

// The API authorizes capabilities the backend already has. It cannot be
// used to describe one it does not.
func TestAuthorizingAnUnknownToolIsRefused(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)

	rec := e.do("POST", "/chat/agents/"+s.agentID+"/tools", e.wsA,
		map[string]any{"tool_name": "github.repository.read"})
	wantErrorCode(t, rec, http.StatusNotFound, "not_found")

	rec = e.do("POST", "/chat/agents/"+s.agentID+"/tools", e.wsA,
		map[string]any{"tool_name": "NotAValidName"})
	wantErrorCode(t, rec, http.StatusBadRequest, "invalid")

	if e.agentTools(e.wsA, s.agentID).AuthorizedCount != 0 {
		t.Fatal("a refused grant left a row behind")
	}
}

// A grant belongs to one agent. Authorizing a tool on one does not hand it
// to the next.
func TestAuthorizationIsPerAgent(t *testing.T) {
	e := newEnv(t)
	first := e.seed(e.wsA)
	second := e.newAgent(e.wsA, first.providerID, "outro agente")

	e.authorizeTool(e.wsA, first.agentID, echoTool)

	if e.agentTools(e.wsA, second).AuthorizedCount != 0 {
		t.Fatal("authorizing one agent authorized another")
	}
}

// Every tool route is workspace-scoped, like every other route in the
// module. An agent id from a URL selects nothing it does not own.
func TestToolRoutesAreWorkspaceScoped(t *testing.T) {
	e := newEnv(t)
	s := e.grantedAgent(e.wsA)

	rec := e.do("GET", "/chat/agents/"+s.agentID+"/tools", e.wsB, nil)
	wantErrorCode(t, rec, http.StatusNotFound, "not_found")

	rec = e.do("POST", "/chat/agents/"+s.agentID+"/tools", e.wsB,
		map[string]any{"tool_name": echoTool})
	wantErrorCode(t, rec, http.StatusNotFound, "not_found")

	rec = e.do("DELETE", "/chat/agents/"+s.agentID+"/tools/"+echoTool, e.wsB, nil)
	wantErrorCode(t, rec, http.StatusNotFound, "not_found")

	// And the grant in wsA is untouched by any of it.
	if e.agentTools(e.wsA, s.agentID).AuthorizedCount != 1 {
		t.Fatal("a cross-workspace request changed the real grant")
	}
}

// A grant is configuration of its agent and outlives nothing. Unlike a
// memory or a source it does not block the delete, and it does not stay
// behind pointing at a row no interface can reach.
func TestDeletingAnAgentRemovesItsGrants(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)
	other := e.newAgent(e.wsA, s.providerID, "descartável")
	e.authorizeTool(e.wsA, other, echoTool)

	rec := e.do("DELETE", "/chat/agents/"+other, e.wsA, nil)
	wantStatus(t, rec, http.StatusNoContent)

	var n int64
	err := e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM chat.agent_tools WHERE agent_id = $1`, other).Scan(&n)
	if err != nil {
		t.Fatalf("count grants: %v", err)
	}
	if n != 0 {
		t.Fatalf("%d grants survived the agent that owned them", n)
	}
}

/* ── the declaration on the wire ─────────────────────────────────────── */

// The regression that matters most in this batch: an agent with no tools
// behaves exactly as it did before tools existed. Same request, same number
// of provider calls, no `tools` field.
func TestAgentWithoutToolsIsUnchanged(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)

	e.send(e.wsA, s.conversationID, "olá")

	if e.llm.streamCalls != 1 {
		t.Fatalf("the provider was called %d times, want exactly one", e.llm.streamCalls)
	}
	if len(e.llm.lastRequest.Tools) != 0 {
		t.Fatalf("tools were declared for an agent that has none: %+v", e.llm.lastRequest.Tools)
	}
	turns := e.assistantTurns(e.wsA, s.conversationID)
	if len(turns) != 1 || turns[0].FinishReason != "stop" {
		t.Fatalf("turn = %+v", turns)
	}
	if turns[0].ContextReport == nil {
		t.Fatal("the turn lost its context report")
	}
	if turns[0].ContextReport.hasBlock("tools") {
		t.Error("a tools block was reported for an agent with no tools")
	}
	if len(turns[0].ContextReport.Rounds) != 0 {
		t.Error("a single-call turn must not carry a rounds array")
	}
}

// Tools are declared only when they are authorized, and what is declared is
// what was granted.
func TestToolsAreDeclaredOnlyWhenAuthorized(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)

	e.send(e.wsA, s.conversationID, "antes")
	if len(e.llm.lastRequest.Tools) != 0 {
		t.Fatal("tools were declared before anything was granted")
	}

	e.authorizeTool(e.wsA, s.agentID, echoTool)
	e.send(e.wsA, s.conversationID, "depois")

	declared := e.llm.lastRequest.Tools
	if len(declared) != 1 || declared[0].Name != echoTool {
		t.Fatalf("declared = %+v, want exactly the granted tool", declared)
	}

	// And revoking takes effect on the very next turn, not on a restart.
	rec := e.do("DELETE", "/chat/agents/"+s.agentID+"/tools/"+echoTool, e.wsA, nil)
	wantStatus(t, rec, http.StatusNoContent)
	e.send(e.wsA, s.conversationID, "depois de revogar")
	if len(e.llm.lastRequest.Tools) != 0 {
		t.Fatal("a revoked tool was still declared")
	}
}

// The declaration costs prompt tokens on every call of the turn, so the
// report counts it. A report that itemised five kinds of message and
// omitted the schema would understate exactly the turns that cost most.
func TestTheToolDeclarationIsReportedAsContext(t *testing.T) {
	e := newEnv(t)
	s := e.grantedAgent(e.wsA)

	e.send(e.wsA, s.conversationID, "olá")

	report := e.assistantTurns(e.wsA, s.conversationID)[0].ContextReport
	block := report.block(t, "tools")
	if block.Items != 1 {
		t.Errorf("tools block items = %d, want 1", block.Items)
	}
	if block.Characters <= 0 || block.EstimatedTokens <= 0 {
		t.Errorf("the tools block reported no cost: %+v", block)
	}
	sum := 0
	for _, b := range report.Blocks {
		sum += b.Characters
	}
	if sum != report.TotalCharacters {
		t.Errorf("total_characters = %d, but the blocks add up to %d", report.TotalCharacters, sum)
	}
}

/* ── the loop ────────────────────────────────────────────────────────── */

// The whole chain, end to end: the model asks, the backend validates,
// authorizes and executes, the result goes back, and the model answers.
func TestToolCallRunsAndTheModelAnswersAfterwards(t *testing.T) {
	e := newEnv(t)
	s := e.grantedAgent(e.wsA)
	e.llm.rounds = [][]ports.StreamEvent{
		askEcho("call_1", "olá João"),
		answer("a ferramenta devolveu: olá João", 60, 12),
	}

	e.send(e.wsA, s.conversationID, "ecoa isto")

	if e.llm.streamCalls != 2 {
		t.Fatalf("the provider was called %d times, want 2 (ask, then answer)", e.llm.streamCalls)
	}

	// The second request has to carry the model's own request back and the
	// tool's answer, correlated by the id the model chose.
	second := e.llm.requests[1].Messages
	var ask, result ports.ChatMessage
	for _, m := range second {
		if len(m.ToolCalls) > 0 {
			ask = m
		}
		if m.Role == "tool" {
			result = m
		}
	}
	if len(ask.ToolCalls) != 1 || ask.Role != "assistant" {
		t.Fatalf("the second call did not echo the assistant's tool request: %+v", second)
	}
	if result.ToolCallID != ask.ToolCalls[0].ID {
		t.Fatalf("tool_call_id = %q, want the id of the call it answers (%q)",
			result.ToolCallID, ask.ToolCalls[0].ID)
	}
	if !strings.Contains(result.Content, "olá João") {
		t.Fatalf("the tool result did not reach the model: %q", result.Content)
	}
	// The tools stay declared on the continuation, or the model cannot
	// legally be shown the exchange it is being asked to read.
	if len(e.llm.requests[1].Tools) != 1 {
		t.Error("the continuation dropped the tool declaration")
	}

	// One assistant turn in the transcript, not three: a turn that ran a
	// tool is still one answer.
	turns := e.assistantTurns(e.wsA, s.conversationID)
	if len(turns) != 1 {
		t.Fatalf("%d assistant turns, want 1", len(turns))
	}
	if !strings.Contains(turns[0].Content, "olá João") {
		t.Fatalf("content = %q", turns[0].Content)
	}
	if turns[0].FinishReason != "stop" {
		t.Errorf("finish_reason = %q, want stop", turns[0].FinishReason)
	}
}

// A tool round is dead air on the wire: the model stops talking and the
// backend runs something. The `tool` frames are the only thing that can
// tell a reader that from a hung gateway while it is happening.
func TestToolProgressIsStreamed(t *testing.T) {
	e := newEnv(t)
	s := e.grantedAgent(e.wsA)
	e.llm.rounds = [][]ports.StreamEvent{
		{{Delta: "vou verificar"},
			{FinishReason: "tool_calls",
				ToolCalls: []domain.ToolCall{{ID: "call_1", Name: echoTool, Arguments: `{"text":"a"}`}},
				Usage:     &ports.Usage{PromptTokens: 20, CompletionTokens: 5}}},
		answer("pronto", 60, 10),
	}

	rec := e.do("POST", "/chat/conversations/"+s.conversationID+"/messages", e.wsA,
		map[string]any{"content": "usa a ferramenta"})
	wantStatus(t, rec, http.StatusOK)

	frames := parseSSE(t, rec.Body.String())
	var tools []map[string]any
	for _, f := range frames {
		if f.event != "tool" {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(f.data), &payload); err != nil {
			t.Fatalf("tool frame is not JSON: %s", f.data)
		}
		tools = append(tools, payload)
	}
	if len(tools) != 2 {
		t.Fatalf("%d tool frames, want a running and a finished one: %v", len(tools), eventNames(frames))
	}
	if tools[0]["status"] != "running" || tools[1]["status"] != "ok" {
		t.Fatalf("statuses = %v, %v", tools[0]["status"], tools[1]["status"])
	}
	if tools[0]["name"] != echoTool || tools[0]["call_id"] != "call_1" {
		t.Fatalf("frame = %v", tools[0])
	}
	// No payloads on the live channel. The arguments and the result are in
	// the audit trail, behind a read the user asks for — which is what lets
	// that record be redactable later.
	for _, f := range tools {
		if _, present := f["arguments"]; present {
			t.Fatal("a tool frame carried the arguments")
		}
		if _, present := f["result"]; present {
			t.Fatal("a tool frame carried the result")
		}
	}
}

// A turn with no tools sends exactly the frames it always sent. A client
// that has never heard of `tool` sees nothing new.
func TestATurnWithoutToolsEmitsNoToolFrames(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)

	rec := e.do("POST", "/chat/conversations/"+s.conversationID+"/messages", e.wsA,
		map[string]any{"content": "olá"})
	wantStatus(t, rec, http.StatusOK)

	for _, name := range eventNames(parseSSE(t, rec.Body.String())) {
		if name == "tool" {
			t.Fatal("a turn with no tools emitted a tool frame")
		}
	}
}

// The model asking is a request, never a permission. A tool that exists and
// was not granted is refused, and the refusal goes back to the model so it
// can explain rather than the turn dying.
func TestAnUnauthorizedToolIsRefusedEvenWhenTheModelAsks(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA) // deliberately NOT granted
	e.llm.rounds = [][]ports.StreamEvent{
		askEcho("call_1", "olá"),
		answer("não tenho permissão para isso", 40, 8),
	}

	e.send(e.wsA, s.conversationID, "usa a ferramenta")

	calls := e.toolCalls(e.wsA, s.conversationID)
	if len(calls) != 1 {
		t.Fatalf("%d audit rows, want the refusal recorded", len(calls))
	}
	if calls[0].Status == "ok" || calls[0].ErrorCode != string(domain.ToolErrNotAuthorized) {
		t.Fatalf("audit row = %+v, want a tool_not_authorized refusal", calls[0])
	}
	if calls[0].Result != nil {
		t.Error("a refused call recorded a result")
	}

	// The refusal reached the model, and the turn finished normally.
	var toolMsg string
	for _, m := range e.llm.requests[1].Messages {
		if m.Role == "tool" {
			toolMsg = m.Content
		}
	}
	if !strings.Contains(toolMsg, string(domain.ToolErrNotAuthorized)) {
		t.Fatalf("the model was told %q, want the refusal code", toolMsg)
	}
	turns := e.assistantTurns(e.wsA, s.conversationID)
	if len(turns) != 1 || turns[0].FinishReason != "stop" {
		t.Fatalf("the turn did not survive the refusal: %+v", turns)
	}
}

// A hallucinated name is told apart from a refusal, because the two lead a
// model to do different things.
func TestAnUnknownToolIsReportedAsNotFound(t *testing.T) {
	e := newEnv(t)
	s := e.grantedAgent(e.wsA)
	e.llm.rounds = [][]ports.StreamEvent{
		askTool("call_1", "system.nonexistent", `{}`),
		answer("essa ferramenta não existe", 40, 8),
	}

	e.send(e.wsA, s.conversationID, "usa algo que não existe")

	calls := e.toolCalls(e.wsA, s.conversationID)
	if len(calls) != 1 || calls[0].ErrorCode != string(domain.ToolErrNotFound) {
		t.Fatalf("audit = %+v, want tool_not_found", calls)
	}
}

// Arguments are validated against the declared schema before anything runs,
// and the model is told precisely what was wrong so it can fix it.
func TestInvalidArgumentsAreRefusedBeforeExecution(t *testing.T) {
	cases := []struct {
		name string
		args string
		want string
	}{
		{"malformed JSON", `{"text":`, "not a JSON object"},
		{"a missing required field", `{}`, "text is required"},
		{"the wrong type", `{"text":42}`, "must be a string"},
		{"an unknown property", `{"text":"a","path":"/etc/passwd"}`, "unknown argument path"},
		{"a payload above the ceiling", `{"text":"` + strings.Repeat("x", domain.MaxToolArgumentsBytes) + `"}`, "byte limit"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			s := e.grantedAgent(e.wsA)
			e.llm.rounds = [][]ports.StreamEvent{
				askTool("call_1", echoTool, c.args),
				answer("corrigindo", 40, 8),
			}

			e.send(e.wsA, s.conversationID, "chama errado")

			calls := e.toolCalls(e.wsA, s.conversationID)
			if len(calls) != 1 {
				t.Fatalf("%d audit rows, want 1", len(calls))
			}
			if calls[0].ErrorCode != string(domain.ToolErrInvalidArguments) {
				t.Fatalf("error_code = %q, want tool_invalid_arguments", calls[0].ErrorCode)
			}
			if !strings.Contains(calls[0].ErrorMessage, c.want) {
				t.Fatalf("message = %q, want it to contain %q", calls[0].ErrorMessage, c.want)
			}
		})
	}
}

// A model may ask for several tools at once. Each is validated and
// authorized on its own, and each gets its own answer correlated by its own
// id.
func TestSeveralToolsInOneRound(t *testing.T) {
	e := newEnv(t)
	s := e.grantedAgent(e.wsA)
	e.llm.rounds = [][]ports.StreamEvent{
		{{FinishReason: "tool_calls", ToolCalls: []domain.ToolCall{
			{ID: "call_a", Name: echoTool, Arguments: `{"text":"a"}`},
			{ID: "call_b", Name: echoTool, Arguments: `{"text":"b"}`},
		}, Usage: &ports.Usage{PromptTokens: 30, CompletionTokens: 10}}},
		answer("a e b", 70, 6),
	}

	e.send(e.wsA, s.conversationID, "duas de uma vez")

	ids := map[string]string{}
	for _, m := range e.llm.requests[1].Messages {
		if m.Role == "tool" {
			ids[m.ToolCallID] = m.Content
		}
	}
	if len(ids) != 2 {
		t.Fatalf("the continuation carried %d tool results, want 2: %v", len(ids), ids)
	}
	if !strings.Contains(ids["call_a"], `"a"`) || !strings.Contains(ids["call_b"], `"b"`) {
		t.Fatalf("results are correlated to the wrong ids: %v", ids)
	}
	if len(e.toolCalls(e.wsA, s.conversationID)) != 2 {
		t.Fatal("both calls must be audited")
	}
}

/* ── loop protection ─────────────────────────────────────────────────── */

// A model that keeps asking is stopped, and stopped in a way the user can
// read. The point is not that the loop ends; it is that it ends at a known
// number of paid provider calls.
// maxToolRoundsForTest is the ceiling as the tests assert it.
//
// It is a SECOND, independent statement of the number rather than a
// reference to app.maxToolRounds, and deliberately: a test that imported
// the constant would keep passing if somebody changed it, which is the one
// change this assertion exists to notice. Raising the ceiling means editing
// it here too, on purpose.
//
//	4 provider calls  ·  3 rounds of tool execution  ·  the 4th refused
const maxToolRoundsForTest = 4

func TestToolRoundLimitStopsTheLoop(t *testing.T) {
	e := newEnv(t)
	s := e.grantedAgent(e.wsA)
	// One script, replayed forever: the model never stops asking.
	e.llm.rounds = [][]ports.StreamEvent{askEcho("call_x", "de novo")}

	rec := e.do("POST", "/chat/conversations/"+s.conversationID+"/messages", e.wsA,
		map[string]any{"content": "entra em loop"})
	// The stream is open by now — the first `tool` frame opened it — so the
	// stop is reported inside it, which is this module's contract for any
	// failure after the headers are gone.
	wantStatus(t, rec, http.StatusOK)
	frames := parseSSE(t, rec.Body.String())
	f, ok := frameOf(frames, "error")
	if !ok {
		t.Fatalf("the turn ended without telling the user: %v", eventNames(frames))
	}
	if !strings.Contains(f.data, string(domain.ToolErrRoundLimit)) {
		t.Fatalf("error frame = %s, want the round limit named", f.data)
	}

	// Four rounds of asking, and no fifth call: the ceiling is checked
	// before the tools run, so the round that cannot be answered is not paid
	// for either.
	if e.llm.streamCalls != maxToolRoundsForTest {
		t.Fatalf("the provider was called %d times, want the ceiling of %d",
			e.llm.streamCalls, maxToolRoundsForTest)
	}

	turns := e.assistantTurns(e.wsA, s.conversationID)
	if len(turns) != 1 || turns[0].FinishReason != string(domain.FinishToolRoundLimit) {
		t.Fatalf("finish_reason = %+v, want tool_round_limit recorded on the turn", turns)
	}

	// ── Three rounds RAN, and the fourth is recorded as refused ────────
	//
	// NO REQUESTED TOOL DISAPPEARS SILENTLY.
	//
	// One audit row per request: three executions and one call the runtime
	// turned away before it did anything. The refusal used to be missing
	// entirely, which meant the model's last request left no trace at all
	// and a receipt could not say that one more had been asked for.
	calls := e.toolCalls(e.wsA, s.conversationID)
	if len(calls) != maxToolRoundsForTest {
		t.Fatalf("%d audit rows, want %d: %d executions and one refusal",
			len(calls), maxToolRoundsForTest, maxToolRoundsForTest-1)
	}
	executed, refused := 0, 0
	for _, c := range calls {
		switch c.Status {
		case string(domain.ToolCallOK):
			executed++
		case string(domain.ToolCallNotExecuted):
			refused++
			if c.ErrorCode != string(domain.ToolErrRoundLimit) {
				t.Errorf("the refused call carries error_code %q, want %q",
					c.ErrorCode, domain.ToolErrRoundLimit)
			}
			if c.Result != nil {
				t.Errorf("the refused call carries a result: %q", *c.Result)
			}
		default:
			t.Errorf("unexpected status %q", c.Status)
		}
	}
	if executed != maxToolRoundsForTest-1 {
		t.Errorf("%d executions, want %d (the last round never ran)",
			executed, maxToolRoundsForTest-1)
	}
	if refused != 1 {
		t.Errorf("%d refusals recorded, want 1", refused)
	}
}

// The same stop, once tokens have already been streamed. There is no status
// code left to send, so it arrives as an `error` frame inside the stream —
// the module's existing contract for a failure after the headers are gone.
func TestTheRoundLimitIsReportedInStreamOnceTokensHaveFlowed(t *testing.T) {
	e := newEnv(t)
	s := e.grantedAgent(e.wsA)
	e.llm.rounds = [][]ports.StreamEvent{{
		{Delta: "deixa eu verificar"},
		{FinishReason: "tool_calls",
			ToolCalls: []domain.ToolCall{{ID: "call_x", Name: echoTool, Arguments: `{"text":"x"}`}},
			Usage:     &ports.Usage{PromptTokens: 20, CompletionTokens: 5}},
	}}

	rec := e.do("POST", "/chat/conversations/"+s.conversationID+"/messages", e.wsA,
		map[string]any{"content": "entra em loop"})
	wantStatus(t, rec, http.StatusOK)

	frames := parseSSE(t, rec.Body.String())
	f, ok := frameOf(frames, "error")
	if !ok {
		t.Fatalf("the turn ended without telling the user: %v", eventNames(frames))
	}
	if !strings.Contains(f.data, string(domain.ToolErrRoundLimit)) {
		t.Fatalf("error frame = %s, want the round limit named", f.data)
	}
	// Whatever streamed is still kept: a half-written answer is stored with
	// the reason it stopped, exactly as for any other mid-turn failure.
	turns := e.assistantTurns(e.wsA, s.conversationID)
	if len(turns) != 1 || !strings.Contains(turns[0].Content, "deixa eu verificar") {
		t.Fatalf("turn = %+v, want the streamed text preserved", turns)
	}
}

// A turn that never asks for a tool never enters the loop a second time,
// even with tools authorized.
func TestZeroToolCallsIsOneProviderCall(t *testing.T) {
	e := newEnv(t)
	s := e.grantedAgent(e.wsA)

	e.send(e.wsA, s.conversationID, "só responde")

	if e.llm.streamCalls != 1 {
		t.Fatalf("the provider was called %d times, want 1", e.llm.streamCalls)
	}
	if len(e.toolCalls(e.wsA, s.conversationID)) != 0 {
		t.Fatal("a turn that asked for nothing recorded a tool call")
	}
}

/* ── accounting ──────────────────────────────────────────────────────── */

// Every provider call was billed, so every provider call is counted. A turn
// that recorded only its last call would understate itself by exactly the
// part tools added.
func TestEveryProviderCallOfATurnIsAccountedFor(t *testing.T) {
	e := newEnv(t)
	s := e.grantedAgent(e.wsA)
	e.llm.rounds = [][]ports.StreamEvent{
		askEcho("call_1", "x"),
		answer("pronto", 90, 20),
	}

	e.send(e.wsA, s.conversationID, "usa a ferramenta")

	turn := e.assistantTurns(e.wsA, s.conversationID)[0]
	// 30 + 90 prompt, 8 + 20 completion, from the two scripted usage frames.
	if turn.PromptTokens != 120 || turn.CompletionTokens != 28 {
		t.Fatalf("tokens = %d/%d, want the sum of both calls (120/28)",
			turn.PromptTokens, turn.CompletionTokens)
	}
	if turn.UsageSource != "provider" {
		t.Errorf("usage_source = %q, want provider: both calls reported usage", turn.UsageSource)
	}
	// The fixture prices test-model at 1e-6 in and 3e-6 out.
	want := 120*1e-6 + 28*3e-6
	if turn.Cost == nil || *turn.Cost < want*0.999 || *turn.Cost > want*1.001 {
		t.Fatalf("cost = %v, want %v", turn.Cost, want)
	}

	// And the breakdown survives, so "why" is answerable and not only "how
	// much".
	rounds := turn.ContextReport.Rounds
	if len(rounds) != 2 {
		t.Fatalf("%d rounds reported, want 2", len(rounds))
	}
	if rounds[0].PromptTokens != 30 || rounds[1].PromptTokens != 90 {
		t.Fatalf("per-round prompt tokens = %d, %d", rounds[0].PromptTokens, rounds[1].PromptTokens)
	}
	if rounds[0].FinishReason != "tool_calls" || rounds[1].FinishReason != "stop" {
		t.Errorf("round finish reasons = %q, %q", rounds[0].FinishReason, rounds[1].FinishReason)
	}
	if len(rounds[0].Tools) != 1 || rounds[0].Tools[0].Status != "ok" {
		t.Errorf("round 1 tools = %+v", rounds[0].Tools)
	}
	if rounds[1].AddedCharacters <= 0 {
		t.Error("the second call carried the tool exchange and reported adding nothing")
	}
	// The rounds add up to the turn.
	sum := 0
	for _, r := range rounds {
		sum += r.PromptTokens + r.CompletionTokens
	}
	if sum != turn.PromptTokens+turn.CompletionTokens {
		t.Fatalf("rounds sum to %d, turn records %d", sum, turn.PromptTokens+turn.CompletionTokens)
	}
}

// If one call had to be estimated, the total is an estimate. Reporting
// `provider` because most of it was exact would claim a precision the
// number does not have.
func TestAMultiCallTurnTakesItsWeakestUsageSource(t *testing.T) {
	e := newEnv(t)
	s := e.grantedAgent(e.wsA)
	e.llm.rounds = [][]ports.StreamEvent{
		askEcho("call_1", "x"),
		// No usage frame on the second call.
		{{Delta: "pronto"}, {FinishReason: "stop"}},
	}

	e.send(e.wsA, s.conversationID, "usa a ferramenta")

	turn := e.assistantTurns(e.wsA, s.conversationID)[0]
	if turn.UsageSource != "estimated" {
		t.Fatalf("usage_source = %q, want estimated: one call was not measured", turn.UsageSource)
	}
	rounds := turn.ContextReport.Rounds
	if rounds[0].UsageSource != "provider" || rounds[1].UsageSource != "estimated" {
		t.Fatalf("per-round sources = %q, %q; the breakdown must stay exact where it can",
			rounds[0].UsageSource, rounds[1].UsageSource)
	}
}

// A tool round that fails to reach the model does not erase what the
// earlier rounds measured.
func TestAFailedLaterCallKeepsWhatEarlierCallsMeasured(t *testing.T) {
	e := newEnv(t)
	s := e.grantedAgent(e.wsA)
	e.llm.rounds = [][]ports.StreamEvent{askEcho("call_1", "x")}
	e.llm.beforeEvent = func(int) {
		// After the first call has been served, make the next one fail to
		// open. The fake records the request before checking openErr, so the
		// switch has to happen while the first stream is being read.
		e.llm.openErr = domain.Upstream("gateway caiu")
	}

	rec := e.do("POST", "/chat/conversations/"+s.conversationID+"/messages", e.wsA,
		map[string]any{"content": "usa a ferramenta"})
	// The stream is already open — the tool frames opened it — so the
	// gateway failure is reported inside it, carrying the same `upstream`
	// code a first-round failure carries.
	wantStatus(t, rec, http.StatusOK)
	f, ok := frameOf(parseSSE(t, rec.Body.String()), "error")
	if !ok || !strings.Contains(f.data, "upstream") {
		t.Fatalf("the gateway failure was not reported: %s", rec.Body.String())
	}

	turn := e.assistantTurns(e.wsA, s.conversationID)[0]
	if turn.PromptTokens != 30 || turn.CompletionTokens != 8 {
		t.Fatalf("tokens = %d/%d, want the first call's real numbers kept",
			turn.PromptTokens, turn.CompletionTokens)
	}
	if turn.UsageSource != "provider" {
		t.Errorf("usage_source = %q, want provider", turn.UsageSource)
	}
	if turn.FinishReason != "error" || turn.Error == "" {
		t.Errorf("the failure was not recorded: finish=%q error=%q", turn.FinishReason, turn.Error)
	}
}

/* ── budget ──────────────────────────────────────────────────────────── */

// The property this batch had to protect: a tool round is a provider call,
// and a limit that only applied to the first one would be a limit tool
// calling walks around.
func TestToolRoundsDoNotBypassTheBudget(t *testing.T) {
	e := newEnv(t)
	// The day starts empty, so the preflight lets the turn begin. The first
	// call then consumes 38 tokens, which reaches the limit — and the second
	// call must not happen.
	s := e.seedWith(e.wsA, map[string]any{"daily_token_limit": 35})
	e.authorizeTool(e.wsA, s.agentID, echoTool)
	e.llm.rounds = [][]ports.StreamEvent{
		askEcho("call_1", "x"),
		answer("nunca chego aqui", 500, 500),
	}

	rec := e.do("POST", "/chat/conversations/"+s.conversationID+"/messages", e.wsA,
		map[string]any{"content": "usa a ferramenta"})
	// The refusal carries the same code a first-call refusal carries. A tool
	// round is a provider call, gated by the same rule with the same answer;
	// only the delivery differs, because the stream is already open.
	wantStatus(t, rec, http.StatusOK)
	frames := parseSSE(t, rec.Body.String())
	f, ok := frameOf(frames, "error")
	if !ok {
		t.Fatalf("the refusal was not reported: %v", eventNames(frames))
	}
	if !strings.Contains(f.data, string(domain.BlockTokens)) {
		t.Fatalf("error frame = %s, want the budget reason", f.data)
	}

	// A gate reading only the database — which does not contain this turn
	// yet — would have found a day with zero tokens in it and let the second
	// call through. The gate counts what the turn has already spent, so it
	// does not.
	if e.llm.streamCalls != 1 {
		t.Fatalf("the provider was called %d times; the second round was not gated", e.llm.streamCalls)
	}
	// And the refusal is on the turn's record, not only on the wire.
	turn := e.assistantTurns(e.wsA, s.conversationID)[0]
	if !strings.Contains(turn.Error, "daily token limit") {
		t.Fatalf("the stored turn does not say why it stopped: %q", turn.Error)
	}
}

// A budget that has room lets the whole turn run, so the gate is not simply
// refusing everything.
func TestARoomyBudgetLetsTheWholeLoopRun(t *testing.T) {
	e := newEnv(t)
	s := e.seedWith(e.wsA, map[string]any{"daily_token_limit": 100000})
	e.authorizeTool(e.wsA, s.agentID, echoTool)
	e.llm.rounds = [][]ports.StreamEvent{
		askEcho("call_1", "x"),
		answer("pronto", 60, 10),
	}

	e.send(e.wsA, s.conversationID, "usa a ferramenta")

	if e.llm.streamCalls != 2 {
		t.Fatalf("the provider was called %d times, want 2", e.llm.streamCalls)
	}
}

// An agent with no limits pays nothing for the gate, exactly as before:
// the budget query never runs.
func TestAnUnbudgetedAgentWithToolsDoesNoBudgetWork(t *testing.T) {
	e := newEnv(t)
	s := e.grantedAgent(e.wsA)
	e.llm.rounds = [][]ports.StreamEvent{
		askEcho("call_1", "x"),
		answer("pronto", 60, 10),
	}
	before := e.llm.keyInfoCalls

	e.send(e.wsA, s.conversationID, "usa a ferramenta")

	if e.llm.keyInfoCalls != before {
		t.Fatal("the local gate consulted the gateway's own budget")
	}
}

/* ── persistence and audit ───────────────────────────────────────────── */

// The audit trail has to answer, later: which tool, which call, what input,
// what result, worked or not, when, for which agent, in which conversation,
// in which turn.
func TestTheAuditTrailAnswersWhatHappened(t *testing.T) {
	e := newEnv(t)
	s := e.grantedAgent(e.wsA)
	e.llm.rounds = [][]ports.StreamEvent{
		askEcho("call_abc", "olá"),
		answer("pronto", 60, 10),
	}

	e.send(e.wsA, s.conversationID, "usa a ferramenta")

	calls := e.toolCalls(e.wsA, s.conversationID)
	if len(calls) != 1 {
		t.Fatalf("%d audit rows, want 1", len(calls))
	}
	c := calls[0]
	if c.ToolName != echoTool {
		t.Errorf("tool_name = %q", c.ToolName)
	}
	if c.ProviderCallID != "call_abc" {
		t.Errorf("provider_call_id = %q, want the gateway's own id", c.ProviderCallID)
	}
	if c.Round != 1 {
		t.Errorf("round = %d", c.Round)
	}
	if c.Status != "ok" || c.ErrorCode != "" {
		t.Errorf("status = %q / %q", c.Status, c.ErrorCode)
	}
	if c.Arguments == nil || !strings.Contains(*c.Arguments, "olá") {
		t.Errorf("arguments = %v, want what the model actually sent", c.Arguments)
	}
	if c.Result == nil || !strings.Contains(*c.Result, "olá") {
		t.Errorf("result = %v, want what the tool returned", c.Result)
	}
	if c.Redacted {
		t.Error("nothing redacts anything yet; the flag must default to false")
	}
	if c.ConversationID != s.conversationID {
		t.Errorf("conversation_id = %q", c.ConversationID)
	}
	// The turn it belongs to.
	turn := e.assistantTurns(e.wsA, s.conversationID)[0]
	if c.MessageID != turn.ID {
		t.Errorf("message_id = %q, want the assistant turn %q", c.MessageID, turn.ID)
	}
	if c.CreatedAt == "" {
		t.Error("created_at is empty")
	}
}

// The trail survives a reload, which is the whole reason it is a table and
// not a field on a streamed frame.
func TestToolCallsSurviveAReload(t *testing.T) {
	e := newEnv(t)
	s := e.grantedAgent(e.wsA)
	e.llm.rounds = [][]ports.StreamEvent{askEcho("call_1", "x"), answer("pronto", 60, 10)}
	e.send(e.wsA, s.conversationID, "usa a ferramenta")

	// A fresh stack over the same database: nothing in memory carries over.
	reloaded := newEnvOn(t, dsn(t))
	reloaded.wsA = e.wsA
	if len(reloaded.toolCalls(e.wsA, s.conversationID)) != 1 {
		t.Fatal("the audit trail did not survive a restart")
	}
	turn := reloaded.assistantTurns(e.wsA, s.conversationID)[0]
	if turn.ContextReport == nil || len(turn.ContextReport.Rounds) != 2 {
		t.Fatal("the round breakdown did not survive a restart")
	}
}

// A tool call belongs to its turn. Regenerate replaces the turn, and the
// calls go with it rather than outliving the answer they produced.
func TestTruncatingATurnRemovesItsToolCalls(t *testing.T) {
	e := newEnv(t)
	s := e.grantedAgent(e.wsA)
	e.llm.rounds = [][]ports.StreamEvent{askEcho("call_1", "x"), answer("pronto", 60, 10)}
	e.send(e.wsA, s.conversationID, "usa a ferramenta")

	messages := e.messages(e.wsA, s.conversationID)
	first := messages[0] // the user turn
	rec := e.do("DELETE",
		"/chat/conversations/"+s.conversationID+"/messages/"+itoa64(first.Seq), e.wsA, nil)
	wantStatus(t, rec, http.StatusOK)

	if n := len(e.toolCalls(e.wsA, s.conversationID)); n != 0 {
		t.Fatalf("%d tool calls outlived the turn they belonged to", n)
	}
	// And the row is gone, not merely unreachable: the message was hard
	// deleted and the cascade is what carries the calls with it.
	var n int64
	if err := e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM chat.tool_calls WHERE conversation_id = $1`,
		s.conversationID).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatalf("%d tool call rows remain after the turn was replaced", n)
	}
}

// Deleting a conversation makes its tool calls unreachable, exactly as it
// makes its messages unreachable — the delete is soft, by the module's
// existing design, so the rows stay and every read stops resolving them.
//
// The test states that plainly rather than asserting a cascade that does
// not fire: a soft delete that claimed to remove rows would be the kind of
// half-truth this suite exists to catch.
func TestDeletingAConversationHidesItsToolCalls(t *testing.T) {
	e := newEnv(t)
	s := e.grantedAgent(e.wsA)
	e.llm.rounds = [][]ports.StreamEvent{askEcho("call_1", "x"), answer("pronto", 60, 10)}
	e.send(e.wsA, s.conversationID, "usa a ferramenta")

	rec := e.do("DELETE", "/chat/conversations/"+s.conversationID, e.wsA, nil)
	wantStatus(t, rec, http.StatusNoContent)

	rec = e.do("GET", "/chat/conversations/"+s.conversationID+"/tool-calls", e.wsA, nil)
	wantErrorCode(t, rec, http.StatusNotFound, "not_found")
}

// The audit read is workspace-scoped like everything else.
func TestToolCallsAreWorkspaceScoped(t *testing.T) {
	e := newEnv(t)
	s := e.grantedAgent(e.wsA)
	e.llm.rounds = [][]ports.StreamEvent{askEcho("call_1", "x"), answer("pronto", 60, 10)}
	e.send(e.wsA, s.conversationID, "usa a ferramenta")

	rec := e.do("GET", "/chat/conversations/"+s.conversationID+"/tool-calls", e.wsB, nil)
	wantErrorCode(t, rec, http.StatusNotFound, "not_found")
}

// The credential never appears in the audit trail, the report, or the
// transcript. It never has anywhere else in this module, and a new table is
// exactly where that stops being true by accident.
func TestNothingAboutAToolLeaksTheCredential(t *testing.T) {
	e := newEnv(t)
	s := e.grantedAgent(e.wsA)
	e.llm.rounds = [][]ports.StreamEvent{askEcho("call_1", "x"), answer("pronto", 60, 10)}
	e.send(e.wsA, s.conversationID, "usa a ferramenta")

	rec := e.do("GET", "/chat/conversations/"+s.conversationID+"/tool-calls", e.wsA, nil)
	if strings.Contains(rec.Body.String(), testAPIKey) {
		t.Fatal("the audit trail leaked the provider key")
	}
	rec = e.do("GET", "/chat/conversations/"+s.conversationID+"/messages", e.wsA, nil)
	if strings.Contains(rec.Body.String(), testAPIKey) {
		t.Fatal("the transcript leaked the provider key")
	}
	rec = e.do("GET", "/chat/agents/"+s.agentID+"/tools", e.wsA, nil)
	if strings.Contains(rec.Body.String(), testAPIKey) {
		t.Fatal("the tool catalogue leaked the provider key")
	}
}

// itoa64 keeps the truncate test readable without importing strconv for one
// call.
func itoa64(n int64) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

/* ── internal tools in production (Release Closure) ──────────────────── */

// The release gate for `system.echo`, asserted through the routes.
//
// The owner's decision is that it stays useful for testing and stops being
// something a user can see or grant. The mechanism is the registry, so this
// test builds the stack the way production does — nothing configured — and
// checks the three surfaces that would otherwise leak it.
func TestInternalToolsAreNotOfferedInProduction(t *testing.T) {
	e := newEnv(t, withProductionToolCatalogue())
	s := e.seed(e.wsA)

	// 1. It is not in the catalogue, so no interface can render it.
	report := e.agentTools(e.wsA, s.agentID)
	for _, v := range report.Items {
		if v.Name == echoTool {
			t.Fatalf("%s is listed in a production catalogue", echoTool)
		}
		if v.Internal {
			t.Fatalf("%s is internal and reached the catalogue", v.Name)
		}
	}

	// 2. It cannot be granted. Not "the button is hidden" — the API refuses.
	rec := e.do("POST", "/chat/agents/"+s.agentID+"/tools", e.wsA,
		map[string]any{"tool_name": echoTool})
	wantErrorCode(t, rec, http.StatusNotFound, "not_found")
	if e.agentTools(e.wsA, s.agentID).AuthorizedCount != 0 {
		t.Fatal("a refused grant left a row behind")
	}

	// 3. The model asking for it by name is refused, and the turn survives.
	e.llm.rounds = [][]ports.StreamEvent{
		askEcho("call_1", "olá"),
		answer("essa ferramenta não existe aqui", 40, 8),
	}
	e.send(e.wsA, s.conversationID, "usa a ferramenta interna")

	calls := e.toolCalls(e.wsA, s.conversationID)
	if len(calls) != 1 || calls[0].ErrorCode != string(domain.ToolErrNotFound) {
		t.Fatalf("audit = %+v, want tool_not_found", calls)
	}
	// And nothing was declared to the model in the first place.
	if len(e.llm.requests[0].Tools) != 0 {
		t.Fatalf("a production turn declared %+v", e.llm.requests[0].Tools)
	}
}

// A grant that survives a deploy which turned the diagnostics off is inert:
// it is reported as stale, it authorizes nothing, and the turn declares
// nothing. This is the upgrade path from a machine where the flag was on.
func TestAGrantForANowInternalToolIsInertNotDangerous(t *testing.T) {
	d := dsn(t)
	freshDB(t, d)

	// A machine with the flag on: the grant is created normally.
	dev := newEnvOn(t, d)
	s := dev.seed(dev.wsA)
	dev.authorizeTool(dev.wsA, s.agentID, echoTool)

	// The same database, served by a production build.
	prod := newEnvOn(t, d, withProductionToolCatalogue())
	prod.wsA = dev.wsA

	report := prod.agentTools(prod.wsA, s.agentID)
	if report.AuthorizedCount != 0 {
		t.Fatalf("authorized_count = %d; a grant for an absent tool must count for nothing",
			report.AuthorizedCount)
	}
	// Surfaced rather than swallowed, so it can be cleaned up.
	if len(report.Stale) != 1 || report.Stale[0] != echoTool {
		t.Fatalf("stale = %v, want the orphaned grant reported", report.Stale)
	}

	prod.llm.rounds = [][]ports.StreamEvent{
		askEcho("call_1", "olá"),
		answer("não consigo", 40, 8),
	}
	prod.send(prod.wsA, s.conversationID, "usa a ferramenta")

	if len(prod.llm.requests[0].Tools) != 0 {
		t.Fatalf("a stale grant caused a declaration: %+v", prod.llm.requests[0].Tools)
	}
	calls := prod.toolCalls(prod.wsA, s.conversationID)
	if len(calls) != 1 || calls[0].ErrorCode != string(domain.ToolErrNotFound) {
		t.Fatalf("audit = %+v, want the call refused", calls)
	}
}

/* ── grounding policy (Tool Grounding) ───────────────────────────────── */

// systemMessagesOf is what the turn actually instructed the model with.
func systemMessagesOf(req ports.CompletionRequest) []string {
	var out []string
	for _, m := range req.Messages {
		if m.Role == "system" {
			out = append(out, m.Content)
		}
	}
	return out
}

func containsPolicy(req ports.CompletionRequest) bool {
	for _, m := range systemMessagesOf(req) {
		if strings.Contains(m, "capabilities that look things up") {
			return true
		}
	}
	return false
}

// An agent with capabilities is told how to use them; an agent without any
// is charged nothing for advice it cannot follow.
//
// This is the whole design of the gate, asserted through the real route
// rather than the builder, because the thing that must be true is what
// reaches the gateway.
func TestGroundingPolicyReachesTheProviderOnlyWithCapabilities(t *testing.T) {
	e := newEnv(t)

	granted := e.grantedAgent(e.wsA)
	e.llm.reply("pronto", 10, 3)
	e.send(e.wsA, granted.conversationID, "e aí")
	if !containsPolicy(e.llm.lastRequest) {
		t.Fatal("an agent with a tool was not told how to ground its answers")
	}

	bare := e.seed(e.wsA)
	e.llm.reply("pronto", 10, 3)
	e.send(e.wsA, bare.conversationID, "e aí")
	if containsPolicy(e.llm.lastRequest) {
		t.Fatal("an agent with no tools was charged for a policy it cannot act on")
	}
}

// The agent this was reported against had an empty system prompt, so the
// model received no instructions of any kind. It does now.
func TestAnAgentWithNoPromptIsNoLongerUninstructed(t *testing.T) {
	e := newEnv(t)
	s := e.seedWith(e.wsA, map[string]any{"system_prompt": ""})
	e.authorizeTool(e.wsA, s.agentID, echoTool)

	e.llm.reply("pronto", 10, 3)
	e.send(e.wsA, s.conversationID, "e aí")

	systems := systemMessagesOf(e.llm.lastRequest)
	if len(systems) != 1 || !containsPolicy(e.llm.lastRequest) {
		t.Fatalf("system messages = %d, want exactly the policy", len(systems))
	}
}

// A turn scoped by `@` is still a turn that can verify something, so it is
// still told how. The scope narrows WHAT it may reach, never whether the
// rule about evidence applies.
func TestAScopedTurnIsStillGroundedAndStillScoped(t *testing.T) {
	e := newEnv(t, withExtraTools(probeTool{}))
	s := e.twoToolAgent(e.wsA)

	e.llm.reply("pronto", 10, 3)
	rec := e.sendWith(e.wsA, s.conversationID, "só o echo",
		[]map[string]any{ref("tool", echoTool)})
	wantStatus(t, rec, http.StatusOK)

	if !containsPolicy(e.llm.lastRequest) {
		t.Fatal("a scoped turn lost the grounding policy")
	}
	// And the scope is untouched: exactly one tool, the selected one.
	wantNames(t, e.declaredTools(1), echoTool)
}

// Every round of a tool loop carries the same instructions. A policy that
// applied only to the first call would be advice the model forgets exactly
// when it is deciding whether to look again.
func TestThePolicySurvivesEveryRoundOfATurn(t *testing.T) {
	e := newEnv(t)
	s := e.grantedAgent(e.wsA)

	e.llm.rounds = [][]ports.StreamEvent{
		askEcho("call_1", "primeiro"),
		askEcho("call_2", "segundo"),
		answer("terminei", 60, 8),
	}
	e.send(e.wsA, s.conversationID, "usa o echo duas vezes")

	if e.llm.streamCalls != 3 {
		t.Fatalf("provider calls = %d, want 3", e.llm.streamCalls)
	}
	for i, req := range e.llm.requests {
		if !containsPolicy(req) {
			t.Fatalf("round %d went out without the policy", i+1)
		}
	}
}

// What a follow-up turn actually receives, asserted rather than assumed.
//
// ── Why this test exists ───────────────────────────────────────────────
// The reported failure was a second turn ("which of them impressed you?")
// answered by speculation. Part of the reason is visible only here: the
// tool RESULTS of the first turn are not replayed. The model sees its own
// prose answer and the question, and nothing else — which is precisely why
// a policy telling it to go and look is the fix rather than a nicety.
//
// This is a statement of current behaviour, pinned so a future change to
// history replay is a deliberate one.
func TestAFollowUpTurnReceivesProseAndNotTheToolResults(t *testing.T) {
	e := newEnv(t)
	s := e.grantedAgent(e.wsA)

	// Turn 1: the model uses a tool and then answers.
	e.llm.rounds = [][]ports.StreamEvent{
		askEcho("call_1", "segredo-do-resultado"),
		answer("aqui está a lista", 60, 8),
	}
	e.send(e.wsA, s.conversationID, "liste tudo")

	// Turn 2: an ordinary follow-up.
	e.llm.rounds = nil
	e.llm.reply("depende", 10, 3)
	e.send(e.wsA, s.conversationID, "quais deles?")

	req := e.llm.lastRequest
	var replayed []string
	for _, m := range req.Messages {
		if m.Role == "user" || m.Role == "assistant" {
			replayed = append(replayed, m.Content)
		}
	}
	joined := strings.Join(replayed, "\n")

	// The question and the answer survive.
	if !strings.Contains(joined, "liste tudo") || !strings.Contains(joined, "aqui está a lista") {
		t.Fatalf("the previous exchange did not reach the follow-up: %q", joined)
	}
	// The evidence does not.
	if strings.Contains(joined, "segredo-do-resultado") {
		t.Fatal("tool results are being replayed; history semantics changed " +
			"and the grounding analysis needs revisiting")
	}
	for _, m := range req.Messages {
		if m.Role == "tool" {
			t.Fatal("a tool message survived into a later turn")
		}
	}
	// And the capabilities are still declared, so it CAN go and look again.
	if len(req.Tools) == 0 {
		t.Fatal("the follow-up turn was offered no way to verify anything")
	}
}
