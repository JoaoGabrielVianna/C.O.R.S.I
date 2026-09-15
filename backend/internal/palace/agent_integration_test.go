//go:build integration

// Palace × the Agents runtime: slice S6.
//
//	Run with: TEST_POSTGRES_DSN=... go test -tags=integration ./internal/palace/...
//
// ══════════════════════════════════════════════════════════════════════
//
//	THE SENTENCE THIS SUITE HAS TO MAKE CONVINCING
//
// ══════════════════════════════════════════════════════════════════════
//
//	An agent holding the Palace grants, and nothing else, can capture and
//	retrieve the operator's context through the real runtime; an agent
//	without them cannot; and nothing a turn reports about what it changed
//	comes from the model's prose.
//
// What is REAL here: the Palace schema, domain, repositories, application
// service and tools; the Agents tool registry; the authorization store;
// the schema validator; the four-gate executor; the turn loop; the audit
// trail; the write and read receipts; and Postgres.
//
// What is faked: the LLM, and only the LLM. The point is to control which
// tool calls arrive, not to test that a model produces them. The tool
// EXECUTOR is the real one: every call below goes through registry
// lookup, grant check, schema validation and the same Execute the
// production binary would run. A real model against a real gateway is a
// separate exercise and is reported as such.
package palace

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	chatreferences "github.com/corsi/backend/internal/chat/adapters/references"
	chatrepo "github.com/corsi/backend/internal/chat/adapters/repo"
	chattools "github.com/corsi/backend/internal/chat/adapters/tools"
	chatapp "github.com/corsi/backend/internal/chat/app"
	chatdomain "github.com/corsi/backend/internal/chat/domain"
	chatports "github.com/corsi/backend/internal/chat/ports"
	"github.com/corsi/backend/internal/palace/app"
	"github.com/corsi/backend/internal/palace/domain"
	"github.com/corsi/backend/internal/palace/ports"
	"github.com/corsi/backend/internal/palace/tools"
	"github.com/corsi/backend/internal/platform/postgres"
	"github.com/corsi/backend/internal/platform/secrets"
	"github.com/corsi/backend/internal/platform/workspace"
)

/* ── harness ─────────────────────────────────────────────────────────── */

// testSecretsKey is exactly 32 bytes before encoding, which is what
// AES-256 needs. It seals the provider credential this suite creates;
// nothing here depends on its value.
var testSecretsKey = base64.StdEncoding.EncodeToString([]byte("corsi-test-key-not-a-real-secret"))

// agentEnv is the Palace env plus the whole Agents stack.
type agentEnv struct {
	*env

	chatSvc  *chatapp.Service
	registry *chattools.Registry
	llm      *fakeLLM
}

func newAgentEnv(t *testing.T) *agentEnv {
	t.Helper()
	e := newEnv(t)

	// The chat schema too: this suite runs the turn loop, which writes
	// conversations, messages and the audit trail.
	migrateChat(t, dsn(t))

	log := slog.New(slog.NewJSONHandler(io.Discard, nil))

	// Wired exactly as cmd/corsi wires it, including the seam the Palace
	// tools arrive through. `Internal: false` is the production
	// catalogue, so nothing here depends on system.echo.
	registry := chattools.MustNew(chattools.Options{Extra: tools.New(e.svc)})
	sealer, err := secrets.New(secrets.Config{Key: testSecretsKey})
	if err != nil {
		t.Fatalf("build sealer: %v", err)
	}
	llm := newFakeLLM()
	chatSvc := chatapp.NewService(chatrepo.New(e.pool), postgres.NewTxManager(e.pool),
		llm, sealer, registry, chatreferences.MustNew(), log)

	return &agentEnv{env: e, chatSvc: chatSvc, registry: registry, llm: llm}
}

// ctxFor builds the context a tool executes under: one carrying a
// workspace, exactly as the middleware produces for a real request.
func ctxFor(ws uuid.UUID) context.Context {
	return workspace.WithWorkspaceID(context.Background(), ws)
}

// newPalaceAgent applies the blueprint: it creates the agent the way an
// operator would and grants exactly the capabilities Palace declares.
//
// ── Why this is the test and not a provisioner ─────────────────────────
// Nothing in the product creates this agent. An agent row needs a
// provider credential that only the operator can supply, so no migration
// and no start-up hook could write one. What tools/agent.go holds is the
// BLUEPRINT, and this function is what keeps it from drifting into a
// description of something nobody can build: it applies it through the
// real chat application service, with the real authorization path.
func (e *agentEnv) newPalaceAgent(ws uuid.UUID) (agentID, conversationID uuid.UUID) {
	e.t.Helper()
	ctx := ctxFor(ws)

	provider, err := e.chatSvc.CreateProvider(ctx, chatapp.CreateProviderInput{
		WorkspaceID: ws, Name: "fixture", BaseURL: "https://gateway.invalid",
		APIKey: "sk-test", DefaultModel: "test-model",
	})
	if err != nil {
		e.t.Fatalf("create provider: %v", err)
	}
	agent, err := e.chatSvc.CreateAgent(ctx, chatapp.CreateAgentInput{
		WorkspaceID:  ws,
		ProviderID:   provider.ID,
		Name:         tools.AgentName,
		Description:  tools.AgentDescription,
		SystemPrompt: tools.AgentInstructions,
	})
	if err != nil {
		e.t.Fatalf("create agent: %v", err)
	}
	for _, name := range tools.Capabilities() {
		if err := e.chatSvc.AuthorizeTool(ctx, ws, agent.ID, name); err != nil {
			e.t.Fatalf("authorize %s: %v", name, err)
		}
	}
	conv, err := e.chatSvc.CreateConversation(ctx, chatapp.CreateConversationInput{
		WorkspaceID: ws, AgentID: agent.ID, Title: "palace",
	})
	if err != nil {
		e.t.Fatalf("create conversation: %v", err)
	}
	return agent.ID, conv.ID
}

// newBareAgent creates an agent with NO grants, which is the default:
// presence in the registry is availability, not permission.
func (e *agentEnv) newBareAgent(ws uuid.UUID) (agentID, conversationID uuid.UUID) {
	e.t.Helper()
	ctx := ctxFor(ws)

	provider, err := e.chatSvc.CreateProvider(ctx, chatapp.CreateProviderInput{
		WorkspaceID: ws, Name: "bare", BaseURL: "https://gateway.invalid",
		APIKey: "sk-test", DefaultModel: "test-model",
	})
	if err != nil {
		e.t.Fatalf("create provider: %v", err)
	}
	agent, err := e.chatSvc.CreateAgent(ctx, chatapp.CreateAgentInput{
		WorkspaceID: ws, ProviderID: provider.ID, Name: "sem grants",
		SystemPrompt: "you have no capabilities",
	})
	if err != nil {
		e.t.Fatalf("create agent: %v", err)
	}
	conv, err := e.chatSvc.CreateConversation(ctx, chatapp.CreateConversationInput{
		WorkspaceID: ws, AgentID: agent.ID, Title: "bare",
	})
	if err != nil {
		e.t.Fatalf("create conversation: %v", err)
	}
	return agent.ID, conv.ID
}

/* ── the fake LLM ────────────────────────────────────────────────────── */

// call is one tool call a scripted round asks for.
type call struct {
	name chatdomain.ToolName
	args string
}

// fakeLLM replays a scripted sequence of rounds. Round n is what the nth
// provider call returns, which is how a tool-calling turn is expressed:
// the early rounds ask for tools, the last answers in prose.
type fakeLLM struct {
	rounds      [][]chatports.StreamEvent
	streamCalls int
	// requests is every body the loop sent. It is how a test reads what a
	// capability HANDED BACK: see agentEnv.toolResult.
	requests []chatports.CompletionRequest
}

func newFakeLLM() *fakeLLM {
	f := &fakeLLM{}
	f.scriptReply("ok")
	return f
}

func (f *fakeLLM) scriptReply(text string) {
	f.streamCalls = 0
	f.requests = nil
	f.rounds = [][]chatports.StreamEvent{proseRound(text)}
}

// script builds a turn out of rounds of tool calls, followed by prose.
//
// Each inner slice is one ROUND, so `script([]call{a}, []call{b})` is a
// turn that calls a, sees its result, calls b, sees its result, and then
// answers. That is what makes the round limit reachable on purpose.
func (f *fakeLLM) script(rounds ...[]call) {
	f.streamCalls = 0
	f.requests = nil
	f.rounds = nil
	for i, round := range rounds {
		calls := make([]chatdomain.ToolCall, 0, len(round))
		for j, c := range round {
			calls = append(calls, chatdomain.ToolCall{
				ID:        "call_" + itoa(i) + "_" + itoa(j),
				Name:      c.name,
				Arguments: c.args,
			})
		}
		f.rounds = append(f.rounds, []chatports.StreamEvent{{
			FinishReason: "tool_calls",
			Usage:        &chatports.Usage{PromptTokens: 20, CompletionTokens: 10},
			ToolCalls:    calls,
		}})
	}
	f.rounds = append(f.rounds, proseRound("pronto"))
}

func proseRound(text string) []chatports.StreamEvent {
	return []chatports.StreamEvent{
		{Delta: text},
		{FinishReason: "stop", Usage: &chatports.Usage{PromptTokens: 30, CompletionTokens: 8}},
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [8]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

func (f *fakeLLM) Stream(_ context.Context, req chatports.CompletionRequest) (chatports.Stream, error) {
	f.streamCalls++
	f.requests = append(f.requests, req)
	idx := f.streamCalls - 1
	if idx >= len(f.rounds) {
		idx = len(f.rounds) - 1
	}
	return &fakeStream{script: f.rounds[idx]}, nil
}

func (f *fakeLLM) Models(context.Context, chatports.Credentials) ([]chatports.Model, error) {
	return []chatports.Model{{ID: "test-model"}}, nil
}

func (f *fakeLLM) ModelPrices(context.Context, chatports.Credentials) (map[string]chatports.Price, error) {
	return map[string]chatports.Price{"test-model": {InputCostPerToken: 1e-6, OutputCostPerToken: 3e-6}}, nil
}

func (f *fakeLLM) KeyInfo(context.Context, chatports.Credentials) (chatports.KeySpend, error) {
	return chatports.KeySpend{KeyAlias: "fixture"}, nil
}

type fakeStream struct {
	script []chatports.StreamEvent
	i      int
}

func (s *fakeStream) Recv() (chatports.StreamEvent, error) {
	if s.i >= len(s.script) {
		return chatports.StreamEvent{}, io.EOF
	}
	ev := s.script[s.i]
	s.i++
	return ev, nil
}

func (s *fakeStream) Close() error { return nil }

/* ── running a turn ──────────────────────────────────────────────────── */

type sink struct {
	text  strings.Builder
	tools []chatapp.ToolEvent
}

func (c *sink) Reasoning(string) error { return nil }
func (c *sink) Delta(t string) error   { c.text.WriteString(t); return nil }
func (c *sink) Tool(ev chatapp.ToolEvent) error {
	c.tools = append(c.tools, ev)
	return nil
}

// finished returns the terminal event for a capability, which is the one
// carrying ok/error and the error code.
func (c *sink) finished(name chatdomain.ToolName) (chatapp.ToolEvent, bool) {
	for i := len(c.tools) - 1; i >= 0; i-- {
		if c.tools[i].Name == string(name) && c.tools[i].Status != "running" {
			return c.tools[i], true
		}
	}
	return chatapp.ToolEvent{}, false
}

// toolResult decodes what a capability handed back TO THE MODEL.
//
// ══════════════════════════════════════════════════════════════════════
//
//	WHY THIS READS THE PROVIDER REQUEST AND NOT THE EVENT STREAM
//
// ══════════════════════════════════════════════════════════════════════
//
// ToolEvent carries a call id, a name, a status and an error code, and
// deliberately no result: a capability's payload does not leave the
// runtime on the live frame. For Palace the audit trail does not carry it
// either, because every capability here is Confidential.
//
// So the only place a result exists, as a fact about a turn that already
// happened, is the `tool` message the loop put in the NEXT provider
// request. Reading it there is not a workaround: it is the strongest
// available assertion, because it is literally what the model was told.
//
// It also demonstrates the cost of Confidential in one line. A test can
// see this; a later turn cannot, which is why an agent re-reads through a
// capability instead of reusing what it saw.
func (e *agentEnv) toolResult(t *testing.T, name chatdomain.ToolName) map[string]any {
	t.Helper()

	// The call id is what correlates a result with the request that asked
	// for it; matching on name alone would pick the wrong one in a turn
	// that called the same capability twice.
	var wanted string
	for _, req := range e.llm.requests {
		for _, msg := range req.Messages {
			for _, tc := range msg.ToolCalls {
				if tc.Name == name && wanted == "" {
					wanted = tc.ID
				}
			}
		}
	}
	if wanted == "" {
		t.Fatalf("%s was never asked for", name)
	}

	for _, req := range e.llm.requests {
		for _, msg := range req.Messages {
			if msg.Role != "tool" || msg.ToolCallID != wanted {
				continue
			}
			var out map[string]any
			if err := json.Unmarshal([]byte(msg.Content), &out); err != nil {
				t.Fatalf("%s result is not JSON: %v (%q)", name, err, msg.Content)
			}
			return out
		}
	}
	t.Fatalf("%s produced no result the model could see", name)
	return nil
}

// turn runs one real turn through the real loop and returns the assistant
// message it produced.
func (e *agentEnv) turn(ws, convID uuid.UUID, content string) (*sink, *chatdomain.Message) {
	e.t.Helper()
	s := &sink{}
	msg, err := e.chatSvc.SendMessage(ctxFor(ws), chatapp.SendMessageInput{
		WorkspaceID: ws, ConversationID: convID, Content: content,
	}, s)
	if err != nil {
		e.t.Fatalf("send message: %v", err)
	}
	return s, msg
}

// writeReceipt reads the turn's answer to "did anything change?", derived
// from the audit records and never from the model's prose.
func (e *agentEnv) writeReceipt(ws uuid.UUID, msg *chatdomain.Message) chatdomain.WriteReceipt {
	e.t.Helper()
	receipts, err := e.chatSvc.WriteReceipts(ctxFor(ws), ws, []uuid.UUID{msg.ID})
	if err != nil {
		e.t.Fatalf("write receipts: %v", err)
	}
	return receipts[msg.ID]
}

func (e *agentEnv) readReceipt(ws, convID uuid.UUID, msg *chatdomain.Message) chatdomain.ReadReceipt {
	e.t.Helper()
	receipts, err := e.chatSvc.ReadReceipts(ctxFor(ws), ws, convID, []uuid.UUID{msg.ID})
	if err != nil {
		e.t.Fatalf("read receipts: %v", err)
	}
	return receipts[msg.ID]
}

/* ══════════════════════════════════════════════════════════════════════
   Authorization
   ══════════════════════════════════════════════════════════════════════ */

func TestAnAgentWithoutGrantsCannotReachPalace(t *testing.T) {
	// Presence in the registry is availability, not permission. The
	// default is deny and the absence of a row is the denial.
	e := newAgentEnv(t)
	_, conv := e.newBareAgent(e.mine)

	e.llm.script([]call{{tools.RoomCreateTool, `{"name":"Carreira"}`}})
	s, msg := e.turn(e.mine, conv, "cria uma sala")

	ev, ok := s.finished(tools.RoomCreateTool)
	if !ok {
		t.Fatal("the capability produced no terminal event")
	}
	if ev.ErrorCode != string(chatdomain.ToolErrNotAuthorized) {
		t.Errorf("error code = %q, want %q", ev.ErrorCode, chatdomain.ToolErrNotAuthorized)
	}

	// Nothing was written.
	_, total, err := e.svc.ListRooms(ctxFor(e.mine), e.mine, ports.RoomFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 0 {
		t.Errorf("%d rooms exist after an unauthorized call", total)
	}

	// And the turn says so: refused is not executed.
	receipt := e.writeReceipt(e.mine, msg)
	if receipt.Confirmed() {
		t.Error("a refused write produced a confirmed receipt")
	}
	if receipt.Refused != 1 {
		t.Errorf("refused = %d, want 1", receipt.Refused)
	}
	if receipt.Executed != 0 {
		t.Errorf("executed = %d, want 0", receipt.Executed)
	}
}

func TestThePalaceAgentGetsExactlyItsOwnTwentyFourGrants(t *testing.T) {
	e := newAgentEnv(t)
	agentID, _ := e.newPalaceAgent(e.mine)

	report, err := e.chatSvc.AgentTools(ctxFor(e.mine), e.mine, agentID)
	if err != nil {
		t.Fatalf("read the agent's catalogue: %v", err)
	}
	if report.AuthorizedCount != 24 {
		t.Fatalf("the agent holds %d grants, want 24", report.AuthorizedCount)
	}
	if len(report.Stale) != 0 {
		t.Errorf("the agent holds grants that resolve to nothing: %v", report.Stale)
	}

	held := map[chatdomain.ToolName]bool{}
	for _, item := range report.Items {
		if !item.Authorized {
			continue
		}
		held[item.Name] = true
		if item.Name.Namespace() != "palace" {
			t.Errorf("the Palace agent holds %q, which belongs to %q",
				item.Name, item.Name.Namespace())
		}
	}
	for _, want := range tools.Capabilities() {
		if !held[want] {
			t.Errorf("the blueprint names %q and the agent does not hold it", want)
		}
	}

	// Nothing from another module arrived by accident. Named explicitly,
	// because the failure this guards is a composition root that handed
	// the wrong slice to the wrong agent. The catalogue the report walks
	// contains only Palace in this suite, so the assertion is also that
	// no foreign capability crept into the registry itself.
	for _, item := range report.Items {
		if !item.Authorized {
			continue
		}
		for _, foreign := range []string{"finance", "threads", "github", "meta_threads", "job_radar", "system"} {
			if item.Name.Namespace() == foreign {
				t.Errorf("the Palace agent was granted %q", item.Name)
			}
		}
	}
}

/* ══════════════════════════════════════════════════════════════════════
   The smoke: a real flow through the real runtime
   ══════════════════════════════════════════════════════════════════════ */

// ══════════════════════════════════════════════════════════════════════
//
//	CREATE A LIST, ADD AN ENTRY, READ IT BACK
//
// ══════════════════════════════════════════════════════════════════════
//
// Three turns rather than one, because that is what the round limit
// permits and what a real conversation looks like. Every call goes
// through the real executor: registry lookup, grant check, schema
// validation, Execute.
func TestAnAgentCapturesAndRetrievesAListThroughTheRuntime(t *testing.T) {
	e := newAgentEnv(t)
	_, conv := e.newPalaceAgent(e.mine)

	// ── Turn one: create the list ──────────────────────────────────────
	e.llm.script([]call{{tools.ArtifactCreateTool,
		`{"kind":"list","title":"Leituras de 2026"}`}})
	_, msg := e.turn(e.mine, conv, "cria uma lista de leituras")

	created := e.toolResult(t, tools.ArtifactCreateTool)
	artifact, _ := created["artifact"].(map[string]any)
	if artifact == nil {
		t.Fatalf("create returned no artifact: %v", created)
	}
	artifactID, _ := artifact["artifact_id"].(string)
	if artifactID == "" {
		t.Fatalf("create returned no id: %v", artifact)
	}
	if artifact["kind"] != "list" {
		t.Errorf("kind = %v, want list", artifact["kind"])
	}

	// The turn's own record says a write happened, and it says so from
	// the audit rows rather than from the sentence the model wrote.
	receipt := e.writeReceipt(e.mine, msg)
	if !receipt.Confirmed() || receipt.Executed != 1 {
		t.Errorf("write receipt: executed=%d failed=%d refused=%d",
			receipt.Executed, receipt.Failed, receipt.Refused)
	}

	// ── Turn two: add an entry ─────────────────────────────────────────
	e.llm.script([]call{{tools.ItemAddTool,
		`{"artifact_id":"` + artifactID + `","text":"Ler o capítulo 3"}`}})
	_, msg = e.turn(e.mine, conv, "adiciona ler o capítulo 3")

	added := e.toolResult(t, tools.ItemAddTool)
	if added["created"] != true {
		t.Errorf("the entry was not created: %v", added)
	}
	if !e.writeReceipt(e.mine, msg).Confirmed() {
		t.Error("adding an entry produced no confirmed write")
	}

	// ── Turn three: read the list back ─────────────────────────────────
	e.llm.script([]call{{tools.ArtifactGetTool, `{"artifact_id":"` + artifactID + `"}`}})
	_, msg = e.turn(e.mine, conv, "o que tem na lista?")

	got := e.toolResult(t, tools.ArtifactGetTool)
	items, _ := got["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("the list came back with %d entries, want 1: %v", len(items), got)
	}
	entry, _ := items[0].(map[string]any)
	if entry["text"] != "Ler o capítulo 3" {
		t.Errorf("entry text = %v", entry["text"])
	}

	// A read changes nothing, and the receipt says so.
	if r := e.writeReceipt(e.mine, msg); r.Confirmed() {
		t.Error("a read turn reported a confirmed write")
	}
}

// ══════════════════════════════════════════════════════════════════════
//
//	OPEN AND FOCUS A WORKING CONTEXT
//
// ══════════════════════════════════════════════════════════════════════
func TestAnAgentOpensAndFocusesAWorkingContextThroughTheRuntime(t *testing.T) {
	e := newAgentEnv(t)
	_, conv := e.newPalaceAgent(e.mine)

	// Two calls in ONE round: the loop executes both before returning to
	// the model, which is how a turn does more than one thing without
	// spending two rounds.
	e.llm.script([]call{
		{tools.SessionStartTool, `{}`},
		{tools.ArtifactCreateTool, `{"kind":"project","title":"Palace"}`},
	})
	e.turn(e.mine, conv, "vamos trabalhar no Palace")

	started := e.toolResult(t, tools.SessionStartTool)
	if started["created"] != true {
		t.Errorf("the session was not opened: %v", started)
	}
	project := e.toolResult(t, tools.ArtifactCreateTool)["artifact"].(map[string]any)
	projectID := project["artifact_id"].(string)

	// Focus it.
	e.llm.script([]call{{tools.SessionFocusTool,
		`{"artifact_id":"` + projectID + `","summary":"desenhando o Palace"}`}})
	e.turn(e.mine, conv, "foca nisso")

	focused := e.toolResult(t, tools.SessionFocusTool)
	if focused["changed"] != true {
		t.Errorf("the focus did not move: %v", focused)
	}

	// Read it back through the capability the model would use.
	e.llm.script([]call{{tools.SessionGetTool, `{}`}})
	e.turn(e.mine, conv, "no que eu estou?")

	current := e.toolResult(t, tools.SessionGetTool)
	if current["open"] != true {
		t.Fatalf("the session is not reported open: %v", current)
	}
	session := current["session"].(map[string]any)
	if session["active_artifact_id"] != projectID {
		t.Errorf("active artifact = %v, want %s", session["active_artifact_id"], projectID)
	}
	// Focusing an artifact does not set the room, even though the runtime
	// is the one place a propagation would be easy to add by accident.
	if session["active_room_id"] != nil {
		t.Errorf("focusing an object set the area to %v", session["active_room_id"])
	}
}

// ══════════════════════════════════════════════════════════════════════
//
//	AN INVALID WRITE PRODUCES NO FALSE RECEIPT
//
// ══════════════════════════════════════════════════════════════════════
//
// This is the failure the whole receipt mechanism exists for: a live
// financial agent once answered "8 transações importadas" in a turn with
// zero tool calls. Here the model asks for something the domain refuses
// and then claims success in prose; what the product reports must come
// from the audit rows.
func TestAnInvalidWriteIsNotReportedAsAnExecution(t *testing.T) {
	e := newAgentEnv(t)
	_, conv := e.newPalaceAgent(e.mine)

	// A note carries no entries, and AcceptsItems is the authority.
	e.llm.script([]call{{tools.ArtifactCreateTool, `{"kind":"note","title":"Uma nota"}`}})
	e.turn(e.mine, conv, "cria uma nota")
	noteID := e.toolResult(t, tools.ArtifactCreateTool)["artifact"].(map[string]any)["artifact_id"].(string)

	e.llm.rounds = [][]chatports.StreamEvent{
		{{
			FinishReason: "tool_calls",
			Usage:        &chatports.Usage{PromptTokens: 20, CompletionTokens: 10},
			ToolCalls: []chatdomain.ToolCall{{
				ID: "call_x", Name: tools.ItemAddTool,
				Arguments: `{"artifact_id":"` + noteID + `","text":"isso não cabe"}`,
			}},
		}},
		// The model then claims it worked. It is wrong, and the product
		// must not repeat it as a confirmation.
		proseRound("Pronto, adicionei a entrada na nota."),
	}
	e.llm.streamCalls = 0

	s, msg := e.turn(e.mine, conv, "adiciona uma entrada na nota")

	ev, ok := s.finished(tools.ItemAddTool)
	if !ok {
		t.Fatal("the capability produced no terminal event")
	}
	if ev.Status != "error" {
		t.Errorf("status = %q, want error", ev.Status)
	}
	if ev.ErrorCode != string(chatdomain.ToolErrInvalidArguments) {
		t.Errorf("error code = %q", ev.ErrorCode)
	}

	// The model said it worked. The receipt says otherwise, and the
	// receipt never reads the message.
	if !strings.Contains(s.text.String(), "adicionei") {
		t.Fatal("the fixture no longer has the model claiming success")
	}
	receipt := e.writeReceipt(e.mine, msg)
	if receipt.Confirmed() {
		t.Error("a failed write produced a confirmed receipt")
	}
	if receipt.Failed != 1 {
		t.Errorf("failed = %d, want 1", receipt.Failed)
	}
	if receipt.Executed != 0 {
		t.Errorf("executed = %d, want 0", receipt.Executed)
	}

	// And nothing was written.
	_, total, err := e.svc.ListItems(ctxFor(e.mine), e.mine, mustParse(t, noteID), ports.Page{})
	if err == nil && total != 0 {
		t.Errorf("%d entries exist on a note", total)
	}
}

/* ══════════════════════════════════════════════════════════════════════
   Receipts and grounding
   ══════════════════════════════════════════════════════════════════════ */

// ══════════════════════════════════════════════════════════════════════
//
//	A PALACE READ IS NEVER A VERIFIED EXTERNAL READ
//
// ══════════════════════════════════════════════════════════════════════
//
// Palace reads our own Postgres. A claim about our own state is checkable
// against our own database; the receipt exists for claims about systems
// this product does not own, and saying a Palace read verified one would
// be the mechanism lying in the direction it was built to prevent.
func TestPalaceReadsNeverProduceAVerifiedExternalRead(t *testing.T) {
	e := newAgentEnv(t)
	_, conv := e.newPalaceAgent(e.mine)

	e.llm.script([]call{
		{tools.MemoryListTool, `{}`},
		{tools.RoomListTool, `{}`},
		{tools.ArtifactListTool, `{}`},
	})
	_, msg := e.turn(e.mine, conv, "o que eu tenho guardado?")

	receipt := e.readReceipt(e.mine, conv, msg)
	if receipt.Status == chatdomain.VerifiedExternalRead {
		t.Error("a turn of Palace reads was reported as verified external evidence")
	}
	if receipt.Verified != 0 {
		t.Errorf("verified = %d, want 0", receipt.Verified)
	}
	if len(receipt.Reads) != 0 {
		t.Errorf("%d external reads were recorded for a turn that read only Palace", len(receipt.Reads))
	}
	if receipt.Status != chatdomain.NoExternalRead {
		t.Errorf("status = %q, want %q", receipt.Status, chatdomain.NoExternalRead)
	}
}

// ══════════════════════════════════════════════════════════════════════
//
//	THE AUDIT KEEPS THE FACT AND NOT THE PAYLOAD
//
// ══════════════════════════════════════════════════════════════════════
func TestPalaceToolCallsAreRecordedRedacted(t *testing.T) {
	e := newAgentEnv(t)
	_, conv := e.newPalaceAgent(e.mine)

	// Content worth leaking, on both sides: it is in the arguments the
	// model sent and in the result the capability returned.
	e.llm.script([]call{{tools.MemoryCreateTool,
		`{"kind":"reflection","content":"` + canary + `","sensitivity":"highly_sensitive"}`}})
	_, msg := e.turn(e.mine, conv, "anota isso")

	rows, err := e.pool.Query(e.ctx(), `
		SELECT tool_name, arguments, result, redacted, status, effect, external
		FROM chat.tool_calls
		WHERE workspace_id = $1 AND message_id = $2`, e.mine, msg.ID)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	defer rows.Close()

	seen := 0
	for rows.Next() {
		var (
			name               string
			arguments, result  *string
			redacted, external bool
			status, effect     string
		)
		if err := rows.Scan(&name, &arguments, &result, &redacted, &status, &effect, &external); err != nil {
			t.Fatalf("scan: %v", err)
		}
		seen++

		if !redacted {
			t.Errorf("%s was recorded unredacted", name)
		}
		if arguments != nil {
			t.Errorf("%s kept its arguments: %q", name, *arguments)
		}
		if result != nil {
			t.Errorf("%s kept its result: %q", name, *result)
		}
		// What the trail is FOR is untouched: which capability, how it
		// ended, what it was.
		if status != "ok" {
			t.Errorf("%s status = %q", name, status)
		}
		if effect != "write" {
			t.Errorf("%s effect = %q, want write", name, effect)
		}
		if external {
			t.Errorf("%s was recorded as external", name)
		}
	}
	if seen != 1 {
		t.Fatalf("%d audit rows, want 1", seen)
	}

	// And the canary is nowhere in the table at all.
	var leaked int
	if err := e.pool.QueryRow(e.ctx(), `
		SELECT count(*) FROM chat.tool_calls
		WHERE workspace_id = $1
		  AND (coalesce(arguments, '') LIKE '%' || $2 || '%'
		    OR coalesce(result, '') LIKE '%' || $2 || '%'
		    OR coalesce(error_message, '') LIKE '%' || $2 || '%')`,
		e.mine, canary).Scan(&leaked); err != nil {
		t.Fatalf("scan for leaks: %v", err)
	}
	if leaked != 0 {
		t.Errorf("%d audit rows carry the content", leaked)
	}
}

/* ══════════════════════════════════════════════════════════════════════
   Outputs
   ══════════════════════════════════════════════════════════════════════ */

func TestToolOutputsDoNotBypassEntityRedaction(t *testing.T) {
	// The tools build their outputs field by field, which is what the
	// entities' redaction is for: a surface that wants to expose
	// something has to name it. This proves the outputs are real content
	// (so nothing is accidentally redacting the product) while the
	// entities themselves still refuse a generic encoder.
	e := newAgentEnv(t)
	_, conv := e.newPalaceAgent(e.mine)

	e.llm.script([]call{{tools.MemoryCreateTool,
		`{"kind":"decision","content":"decidi usar Go"}`}})
	e.turn(e.mine, conv, "anota")

	out := e.toolResult(t, tools.MemoryCreateTool)
	memory := out["memory"].(map[string]any)
	if memory["content"] != "decidi usar Go" {
		t.Errorf("the tool output did not carry the content it was asked for: %v", memory)
	}
	// The output is a map the tool built, not an encoded entity: an
	// encoded entity would say `"redacted":true` and carry nothing.
	if _, redacted := memory["redacted"]; redacted {
		t.Error("the tool handed an entity to an encoder instead of naming its fields")
	}

	// Meanwhile the entity itself still redacts.
	id := mustParse(t, memory["memory_id"].(string))
	loaded, err := e.svc.GetMemory(ctxFor(e.mine), e.mine, id)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	raw, err := json.Marshal(loaded)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), "decidi usar Go") {
		t.Errorf("the entity serialised its content: %s", raw)
	}
}

func TestAListingWithholdsHighlySensitiveAndAnExplicitGetDoesNot(t *testing.T) {
	e := newAgentEnv(t)
	_, conv := e.newPalaceAgent(e.mine)

	// Created one at a time, so each result is unambiguous: toolResult
	// correlates by call id, and a turn that called the same capability
	// twice would make "which one" a question this test does not need to
	// answer.
	e.llm.script([]call{{tools.MemoryCreateTool, `{"kind":"fact","content":"comum"}`}})
	e.turn(e.mine, conv, "anota a primeira")

	e.llm.script([]call{{tools.MemoryCreateTool,
		`{"kind":"reflection","content":"muito pessoal","sensitivity":"highly_sensitive"}`}})
	e.turn(e.mine, conv, "anota a segunda")

	hidden := e.toolResult(t, tools.MemoryCreateTool)["memory"].(map[string]any)
	if hidden["sensitivity"] != "highly_sensitive" {
		t.Fatalf("the second record is not highly sensitive: %v", hidden)
	}
	hiddenID, _ := hidden["memory_id"].(string)
	if hiddenID == "" {
		t.Fatal("the highly sensitive record was not created")
	}

	// A default listing shows one of the two, and its count agrees.
	e.llm.script([]call{{tools.MemoryListTool, `{}`}})
	e.turn(e.mine, conv, "o que eu guardei?")
	listed := e.toolResult(t, tools.MemoryListTool)
	if n := len(listed["memories"].([]any)); n != 1 {
		t.Errorf("the default listing returned %d records, want 1", n)
	}
	if listed["matching_total"] != float64(1) {
		t.Errorf("matching_total = %v, want 1", listed["matching_total"])
	}

	// The opt-in admits it.
	e.llm.script([]call{{tools.MemoryListTool, `{"include_highly_sensitive":true}`}})
	e.turn(e.mine, conv, "inclui as sensíveis")
	listed = e.toolResult(t, tools.MemoryListTool)
	if n := len(listed["memories"].([]any)); n != 2 {
		t.Errorf("the opted-in listing returned %d records, want 2", n)
	}

	// And an explicit get of the workspace's own record returns it.
	e.llm.script([]call{{tools.MemoryGetTool, `{"memory_id":"` + hiddenID + `"}`}})
	e.turn(e.mine, conv, "abre aquela")
	got := e.toolResult(t, tools.MemoryGetTool)
	if got["memory"].(map[string]any)["content"] != "muito pessoal" {
		t.Errorf("an explicit get did not return the record: %v", got)
	}
}

/* ══════════════════════════════════════════════════════════════════════
   Provenance is never implicit
   ══════════════════════════════════════════════════════════════════════ */

// ══════════════════════════════════════════════════════════════════════
//
//	NOTHING TURNS A MESSAGE INTO EVIDENCE
//
// ══════════════════════════════════════════════════════════════════════
//
// There is no bridge between a chat message and a palace source, and no
// capability creates one as a side effect. A memory written through the
// runtime has no evidence behind it unless somebody deliberately put some
// there, and a source created on its own is attached to nothing.
func TestNoCapabilityCreatesProvenanceImplicitly(t *testing.T) {
	e := newAgentEnv(t)
	_, conv := e.newPalaceAgent(e.mine)

	e.llm.script([]call{
		{tools.MemoryCreateTool, `{"kind":"decision","content":"decidi X"}`},
		{tools.SourceCreateTool, `{"kind":"text","content":"algo que o usuário colou"}`},
	})
	e.turn(e.mine, conv, "anota isso e guarda o texto")

	memoryID := e.toolResult(t, tools.MemoryCreateTool)["memory"].(map[string]any)["memory_id"].(string)

	// Creating a source in the same turn did not attach it to anything.
	var links int
	if err := e.pool.QueryRow(e.ctx(),
		`SELECT count(*) FROM palace.memory_sources WHERE workspace_id = $1`, e.mine).Scan(&links); err != nil {
		t.Fatalf("count links: %v", err)
	}
	if links != 0 {
		t.Errorf("%d provenance links were created without anybody asking", links)
	}

	// And the record says plainly that it rests on nothing, rather than
	// leaving the model to guess.
	e.llm.script([]call{{tools.MemoryGetTool, `{"memory_id":"` + memoryID + `"}`}})
	e.turn(e.mine, conv, "por que eu acho isso?")
	got := e.toolResult(t, tools.MemoryGetTool)
	if got["evidence_count"] != float64(0) {
		t.Errorf("evidence_count = %v, want 0", got["evidence_count"])
	}
	note, _ := got["evidence_note"].(string)
	if !strings.Contains(note, "do not invent") {
		t.Errorf("the empty-evidence note does not warn against inventing one: %q", note)
	}
}

/* ══════════════════════════════════════════════════════════════════════
   The round limit, as evidence
   ══════════════════════════════════════════════════════════════════════ */

// ══════════════════════════════════════════════════════════════════════
//
//	EVIDENCE: maxToolRounds = 3 PERMITS TWO ROUNDS OF TOOLS, NOT THREE
//
// ══════════════════════════════════════════════════════════════════════
//
// This slice does not change the constant. What it does is make the
// consequence concrete, because the measured behaviour and the constant's
// own documentation do not agree, and a future decision about batching,
// tool routing or schema shape should start from the measurement.
//
// ── The arithmetic, from app/send.go ───────────────────────────────────
// `round` counts PROVIDER CALLS, starting at 1, and the ceiling is
// checked after the model asks for tools and before they run:
//
//	provider call 1  asks for tools   1 >= 4 is false  → tools RUN
//	provider call 2  asks for tools   2 >= 4 is false  → tools RUN
//	provider call 3  asks for tools   3 >= 4 is false  → tools RUN
//	provider call 4  asks for tools   4 >= 4 is TRUE   → turn stops
//
// So a turn executes at most THREE rounds of tools.
//
// ── What S8B changed, and what it did not ──────────────────────────────
// The ceiling was three, permitting two execution rounds, and this test
// recorded the consequence: a chain of three DEPENDENT steps — discover,
// read, act — had its third step refused every time. That is fixed; see
// tooldepth_integration_test.go, where the chain now completes.
//
// What is NOT fixed is what this test measures, and the distinction is the
// point: the ceiling bounds DEPTH, and unitary writes consume it like
// depth even though they are not. Four independent entries still do not
// fit in one turn, because each one is scripted as its own round. The
// number moved from two to three; the shape of the failure did not.
//
// ── Why Palace feels this sooner than anything else ────────────────────
// Entries are unitary, because the tool schema is a flat object of
// scalars and encoding a list inside a string would be a contract the
// validator does not enforce (see items.go). So "adiciona arroz, feijão,
// café e açúcar" is four writes, and a turn can do two of them.
//
// ── What the product does at that point, and it is not graceful ────────
// ToolErrRoundLimit is the one tool error that is NOT handed back to the
// model, deliberately: handing it back would invite the loop it exists to
// stop. The turn therefore ends in an ERROR rather than in prose, so the
// user gets no answer at all for the two entries that were really
// written. They are in the database and nothing tells them so.
func TestAPalaceFlowOfSeveralStepsIsCutOffByTheRoundLimit(t *testing.T) {
	e := newAgentEnv(t)
	_, conv := e.newPalaceAgent(e.mine)

	artifact, err := e.svc.CreateArtifact(ctxFor(e.mine), e.mine, app.CreateArtifactInput{
		Kind: domain.ArtifactList, Title: "Compras",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	id := artifact.ID.String()

	// Five rounds of one entry each. The first three run; the fourth is
	// where the loop stops.
	e.llm.script(
		[]call{{tools.ItemAddTool, `{"artifact_id":"` + id + `","text":"arroz"}`}},
		[]call{{tools.ItemAddTool, `{"artifact_id":"` + id + `","text":"feijão"}`}},
		[]call{{tools.ItemAddTool, `{"artifact_id":"` + id + `","text":"café"}`}},
		[]call{{tools.ItemAddTool, `{"artifact_id":"` + id + `","text":"açúcar"}`}},
		[]call{{tools.ItemAddTool, `{"artifact_id":"` + id + `","text":"sal"}`}},
	)
	// ── The limit is FATAL to the turn, and that is the finding ────────
	// ToolErrRoundLimit is the one tool error the loop does not hand back
	// to the model: handing it back would invite exactly the loop it
	// exists to stop. So the turn does not end in prose, it ends in an
	// error, and the user gets no answer at all for the work that DID
	// happen.
	s := &sink{}
	_, err = e.chatSvc.SendMessage(ctxFor(e.mine), chatapp.SendMessageInput{
		WorkspaceID: e.mine, ConversationID: conv,
		Content: "adiciona arroz, feijão, café e açúcar",
	}, s)
	if err == nil {
		t.Fatal("the turn did not reach the round limit; the evidence this test exists " +
			"to produce is missing")
	}
	if !strings.Contains(err.Error(), "more than 4 times") {
		t.Fatalf("the turn failed for another reason: %v", err)
	}

	// ── Exactly three entries are real ─────────────────────────────────
	// Three, not four. If this ever reads 4, the ceiling changed and the
	// evidence below is stale: update the decision, not the number.
	const roundsThatExecute = 3

	items, total, err := e.svc.ListItems(ctxFor(e.mine), e.mine, artifact.ID, ports.Page{})
	if err != nil {
		t.Fatalf("list items: %v", err)
	}
	if total != roundsThatExecute {
		t.Errorf("%d entries were written, want %d; the arithmetic in this test's header "+
			"no longer describes app/send.go", total, roundsThatExecute)
	}
	texts := make([]string, 0, len(items))
	for _, i := range items {
		texts = append(texts, i.Text)
	}

	t.Logf(`ROUND LIMIT EVIDENCE
  asked for : 5 unitary writes in one turn
  executed  : %d (%v)
  outcome   : the turn ENDED IN AN ERROR, so the user is told nothing about
              the %d entries that were really written
  measured  : maxToolRounds=4 permits %d rounds of tool execution, because
              `+"`round`"+` counts provider calls from 1 and the ceiling is checked
              before the tools run
  note      : S8B raised the ceiling from 3 to 4, which fixed the DEPENDENT
              three-step chain. This shape is different and still bounded:
              unitary writes spend depth they do not need
  cause     : entries are unitary, because the tool schema is a flat object
              of scalars and encoding a list inside a string would be a
              contract the validator does not enforce`,
		total, texts, total, roundsThatExecute)

	// ── And the ones that ran were reported as running ─────────────────
	// The live frames are the only record the user gets for this turn,
	// since it ends in an error rather than in a message.
	executed := 0
	for _, ev := range s.tools {
		if ev.Name == string(tools.ItemAddTool) && ev.Status == "ok" {
			executed++
		}
	}
	if executed != roundsThatExecute {
		t.Errorf("%d capabilities reported ok, want %d", executed, roundsThatExecute)
	}
}

/* ── small helpers ───────────────────────────────────────────────────── */

func mustParse(t *testing.T, raw string) uuid.UUID {
	t.Helper()
	id, err := uuid.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return id
}

// migrateChat applies the chat schema, which this suite needs because it
// runs the real turn loop: conversations, messages and the audit trail
// all live there.
//
// Reset first, like the Palace schema: this suite owns its database and
// every test starts from nothing. See freshDB.
func migrateChat(t *testing.T, d string) {
	t.Helper()
	ctx := context.Background()

	conn, err := pgx.Connect(ctx, d)
	if err != nil {
		t.Fatalf("connect for chat reset: %v", err)
	}
	for _, stmt := range []string{
		`DROP SCHEMA IF EXISTS chat CASCADE`,
		`DROP TABLE IF EXISTS schema_migrations_chat`,
	} {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			_ = conn.Close(ctx)
			t.Fatalf("reset chat (%s): %v", stmt, err)
		}
	}
	if err := conn.Close(ctx); err != nil {
		t.Fatalf("close chat reset conn: %v", err)
	}

	m, err := migrate.New("file://../../migrations/chat", migrateDSN(t, d, "schema_migrations_chat"))
	if err != nil {
		t.Fatalf("migrate.New(chat): %v", err)
	}
	defer func() { _, _ = m.Close() }()
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate up (chat): %v", err)
	}
}

/* ══════════════════════════════════════════════════════════════════════
   Provenance through the runtime
   ══════════════════════════════════════════════════════════════════════ */

// ══════════════════════════════════════════════════════════════════════
//
//	THE ONLY WAY AN AGENT CITES EVIDENCE
//
// ══════════════════════════════════════════════════════════════════════
//
// Three deliberate steps, and it takes three turns because each is its own
// capability: keep the record, store the material, cite one for the other.
// There is no argument on create that does it in one, precisely so that a
// half-finished citation is visible as a missing step rather than as a
// record that was supposed to rest on something and does not.
func TestAnAgentCitesEvidenceThroughTheRuntime(t *testing.T) {
	e := newAgentEnv(t)
	_, conv := e.newPalaceAgent(e.mine)

	e.llm.script([]call{{tools.MemoryCreateTool,
		`{"kind":"decision","content":"vou sair da empresa em março","sensitivity":"private"}`}})
	e.turn(e.mine, conv, "anota essa decisão")
	memoryID := e.toolResult(t, tools.MemoryCreateTool)["memory"].(map[string]any)["memory_id"].(string)

	e.llm.script([]call{{tools.SourceCreateTool,
		`{"kind":"text","content":"colei aqui a conversa em que eu disse isso","sensitivity":"private"}`}})
	e.turn(e.mine, conv, "guarda o texto que eu colei")
	sourceID := e.toolResult(t, tools.SourceCreateTool)["source"].(map[string]any)["source_id"].(string)

	// ── The citation ───────────────────────────────────────────────────
	e.llm.script([]call{{tools.MemoryLinkSourceTool,
		`{"memory_id":"` + memoryID + `","source_id":"` + sourceID + `"}`}})
	_, msg := e.turn(e.mine, conv, "essa decisão veio daquele texto")

	linked := e.toolResult(t, tools.MemoryLinkSourceTool)
	if linked["created"] != true {
		t.Errorf("the link was not created: %v", linked)
	}
	if !e.writeReceipt(e.mine, msg).Confirmed() {
		t.Error("citing evidence produced no confirmed write")
	}

	// ── And the record now says what it rests on ───────────────────────
	e.llm.script([]call{{tools.MemoryGetTool, `{"memory_id":"` + memoryID + `"}`}})
	e.turn(e.mine, conv, "por que eu acho isso?")
	got := e.toolResult(t, tools.MemoryGetTool)
	if got["evidence_count"] != float64(1) {
		t.Fatalf("evidence_count = %v, want 1", got["evidence_count"])
	}
}

func TestTheLinkCapabilityIsIdempotentThroughTheRuntime(t *testing.T) {
	e := newAgentEnv(t)
	_, conv := e.newPalaceAgent(e.mine)

	m := e.memoryAt(e.mine, "uma conclusão", domain.SensitivityNormal)
	s := e.source(e.mine, "a evidência", domain.SensitivityNormal)
	args := `{"memory_id":"` + m.ID.String() + `","source_id":"` + s.ID.String() + `"}`

	e.llm.script([]call{{tools.MemoryLinkSourceTool, args}})
	e.turn(e.mine, conv, "cita")
	if first := e.toolResult(t, tools.MemoryLinkSourceTool); first["created"] != true {
		t.Errorf("the first citation reported that it already existed: %v", first)
	}

	e.llm.script([]call{{tools.MemoryLinkSourceTool, args}})
	e.turn(e.mine, conv, "cita de novo")
	second := e.toolResult(t, tools.MemoryLinkSourceTool)
	if second["created"] != false || second["already_existed"] != true {
		t.Errorf("the second citation claimed work it did not do: %v", second)
	}

	var rows int
	if err := e.pool.QueryRow(e.ctx(),
		`SELECT count(*) FROM palace.memory_sources WHERE workspace_id = $1`, e.mine).Scan(&rows); err != nil {
		t.Fatalf("count: %v", err)
	}
	if rows != 1 {
		t.Errorf("%d links after citing twice, want 1", rows)
	}
}

func TestTheLinkCapabilityRefusesADowngradeAndACrossWorkspaceCitation(t *testing.T) {
	e := newAgentEnv(t)
	_, conv := e.newPalaceAgent(e.mine)

	ordinary := e.memoryAt(e.mine, "uma conclusão comum", domain.SensitivityNormal)
	privateSource := e.source(e.mine, "material pessoal", domain.SensitivityPrivate)
	theirSource := e.source(e.theirs, "material deles", domain.SensitivityNormal)

	// The floor: an ordinary record cannot rest on private evidence.
	e.llm.script([]call{{tools.MemoryLinkSourceTool,
		`{"memory_id":"` + ordinary.ID.String() + `","source_id":"` + privateSource.ID.String() + `"}`}})
	s, msg := e.turn(e.mine, conv, "cita")
	ev, ok := s.finished(tools.MemoryLinkSourceTool)
	if !ok || ev.Status != "error" {
		t.Fatalf("the floor did not refuse the citation: %+v", ev)
	}
	if e.writeReceipt(e.mine, msg).Confirmed() {
		t.Error("a refused citation produced a confirmed write")
	}

	// The workspace boundary: a neighbour's evidence is not found.
	e.llm.script([]call{{tools.MemoryLinkSourceTool,
		`{"memory_id":"` + ordinary.ID.String() + `","source_id":"` + theirSource.ID.String() + `"}`}})
	s, _ = e.turn(e.mine, conv, "cita a deles")
	ev, ok = s.finished(tools.MemoryLinkSourceTool)
	if !ok || ev.Status != "error" {
		t.Fatalf("a cross-workspace citation was accepted: %+v", ev)
	}

	// Nothing was written by either.
	var rows int
	if err := e.pool.QueryRow(e.ctx(),
		`SELECT count(*) FROM palace.memory_sources`).Scan(&rows); err != nil {
		t.Fatalf("count: %v", err)
	}
	if rows != 0 {
		t.Errorf("%d links were written by refused citations", rows)
	}
}

func TestTheLinkCapabilityIsAuditedRedacted(t *testing.T) {
	e := newAgentEnv(t)
	_, conv := e.newPalaceAgent(e.mine)

	m := e.memoryAt(e.mine, canary, domain.SensitivityHighlySensitive)
	src := e.source(e.mine, canary, domain.SensitivityHighlySensitive)

	e.llm.script([]call{{tools.MemoryLinkSourceTool,
		`{"memory_id":"` + m.ID.String() + `","source_id":"` + src.ID.String() + `"}`}})
	_, msg := e.turn(e.mine, conv, "cita")

	var (
		arguments, result *string
		redacted, ext     bool
		status, effect    string
	)
	if err := e.pool.QueryRow(e.ctx(), `
		SELECT arguments, result, redacted, status, effect, external
		FROM chat.tool_calls
		WHERE workspace_id = $1 AND message_id = $2 AND tool_name = $3`,
		e.mine, msg.ID, string(tools.MemoryLinkSourceTool),
	).Scan(&arguments, &result, &redacted, &status, &effect, &ext); err != nil {
		t.Fatalf("read audit: %v", err)
	}
	if !redacted || arguments != nil || result != nil {
		t.Error("the citation was recorded unredacted")
	}
	if status != "ok" || effect != "write" || ext {
		t.Errorf("audit row = status %q effect %q external %v", status, effect, ext)
	}
}
