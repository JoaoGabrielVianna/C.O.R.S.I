//go:build integration

// Tool depth — S8B, part A.
//
// ══════════════════════════════════════════════════════════════════════
//
//	THE CEILING BOUNDS DEPTH, NOT VOLUME
//
// ══════════════════════════════════════════════════════════════════════
//
// ── What the live battery showed ───────────────────────────────────────
// `maxToolRounds = 3` permits three provider calls and therefore only TWO
// rounds of tool execution — one indirection. The most ordinary operation
// a knowledge base has needs two:
//
//	"marca o arroz como comprado"
//	   artifact.list  →  artifact.get  →  item.update
//
// three DEPENDENT steps, because the item's id does not exist until the
// second call has returned. The third was refused every time. The same
// shape made `relation.create` structurally unreachable by the natural
// path: both endpoints must exist before the relation can name them, so
// creating them consumed the budget the relation itself needed. Zero
// relations were created in the whole battery.
//
// It was never about VOLUME. The same battery watched ONE round carry five
// independent `item.add` calls, because a model emits independent calls in
// parallel. What the ceiling bounds is how many times the model may look at
// a result before deciding the next call.
//
// So the ceiling moved to four: four provider calls, three execution
// rounds, two indirections. It did not go away, and this file proves both
// halves — the third step now fits, and a fourth is still refused with
// everything before it intact.
//
// ── Why these tests live in the Palace suite ───────────────────────────
// Because the chain has to run against real capabilities and real rows to
// mean anything. Every call below goes through the registry, the grant
// check, the schema validation, the executor and Postgres. What is scripted
// is the model's decision to make each call, which is the one thing a fake
// gateway has to supply; the ids it uses are the ids the earlier calls
// really produced.
package palace

import (
	"testing"

	"github.com/google/uuid"

	chatapp "github.com/corsi/backend/internal/chat/app"
	chatdomain "github.com/corsi/backend/internal/chat/domain"
	"github.com/corsi/backend/internal/palace/tools"
)

// maxToolRoundsForTest is the ceiling as this suite asserts it.
//
// Stated independently rather than imported from the chat package, and
// deliberately: a test that read the constant would keep passing if
// somebody changed it, which is the one change these assertions exist to
// notice.
//
//	4 provider calls  ·  3 rounds of tool execution  ·  the 4th refused
const maxToolRoundsForTest = 4

// turnExpectingStop drives a turn that is EXPECTED to end badly.
//
// `agentEnv.turn` fatals on an error, which is right for every other test
// in this package: a turn that failed is a broken fixture. Here the failure
// is the subject, so it is returned instead of ending the test — and the
// turn's message is still returned, because the whole point of the S6.2
// work is that a stopped turn still persists what it did.
func (e *agentEnv) turnExpectingStop(ws, convID uuid.UUID, content string) (*sink, *chatdomain.Message, error) {
	e.t.Helper()
	s := &sink{}
	msg, err := e.chatSvc.SendMessage(ctxFor(ws), chatapp.SendMessageInput{
		WorkspaceID: ws, ConversationID: convID, Content: content,
	}, s)
	return s, msg, err
}

// aListWithOneItem seeds the state the workflow operates on, through the
// runtime rather than through SQL: a list, and one entry on it.
//
// Two turns, because that is a create and an add and they are two things
// the user said. It leaves the ids the chain will really use.
func (e *agentEnv) aListWithOneItem(ws, conv uuid.UUID, title, text string) (artifactID, itemID string) {
	e.t.Helper()

	e.llm.script([]call{{tools.ArtifactCreateTool,
		`{"kind":"list","title":` + quoteJSON(title) + `}`}})
	e.turn(ws, conv, "cria a lista")
	created := e.toolResult(e.t, tools.ArtifactCreateTool)
	artifact, _ := created["artifact"].(map[string]any)
	if artifact == nil {
		e.t.Fatalf("create returned no artifact: %v", created)
	}
	artifactID, _ = artifact["artifact_id"].(string)
	if artifactID == "" {
		e.t.Fatalf("create returned no id: %v", artifact)
	}

	e.llm.script([]call{{tools.ItemAddTool,
		`{"artifact_id":"` + artifactID + `","text":` + quoteJSON(text) + `}`}})
	e.turn(ws, conv, "adiciona um item")
	added := e.toolResult(e.t, tools.ItemAddTool)
	item, _ := added["item"].(map[string]any)
	if item == nil {
		e.t.Fatalf("add returned no item: %v", added)
	}
	itemID, _ = item["item_id"].(string)
	if itemID == "" {
		e.t.Fatalf("add returned no item id: %v", added)
	}
	return artifactID, itemID
}

/* ── A · the workflow that did not fit ───────────────────────────────── */

// "Marca o arroz como comprado", as three dependent executions in one turn.
//
// This is the exact shape the battery could not complete. Every step is a
// real capability against real rows; what the ceiling used to refuse was
// the third one.
func TestADependentThreeStepWorkflowNowFitsInOneTurn(t *testing.T) {
	e := newAgentEnv(t)
	_, conv := e.newPalaceAgent(e.mine)
	artifactID, itemID := e.aListWithOneItem(e.mine, conv, "Compras", "arroz")

	// discover → read → act, three rounds, then the answer.
	e.llm.script(
		[]call{{tools.ArtifactListTool, `{"kind":"list"}`}},
		[]call{{tools.ArtifactGetTool, `{"artifact_id":"` + artifactID + `"}`}},
		[]call{{tools.ItemUpdateTool,
			`{"artifact_id":"` + artifactID + `","item_id":"` + itemID + `","done":true}`}},
	)
	s, msg := e.turn(e.mine, conv, "marca o arroz como comprado")

	// ── The turn ended by ANSWERING, not by being stopped ──────────────
	if msg.FinishReason != string(chatdomain.FinishStop) {
		t.Fatalf("finish_reason = %q, want %q: the chain was cut short",
			msg.FinishReason, chatdomain.FinishStop)
	}
	if msg.Content == "" {
		t.Error("the turn produced no final answer")
	}
	if e.llm.streamCalls != maxToolRoundsForTest {
		t.Errorf("%d provider calls, want %d: three tool rounds and the answer",
			e.llm.streamCalls, maxToolRoundsForTest)
	}

	// ── All three executed, none refused ───────────────────────────────
	for _, name := range []chatdomain.ToolName{
		tools.ArtifactListTool, tools.ArtifactGetTool, tools.ItemUpdateTool,
	} {
		ev, ok := s.finished(name)
		if !ok {
			t.Fatalf("%s never finished", name)
		}
		if ev.Status != string(chatdomain.ToolCallOK) {
			t.Fatalf("%s ended %q (%s); the third step is the one that used "+
				"to be refused", name, ev.Status, ev.ErrorCode)
		}
	}

	// ── And the change is really in Postgres ───────────────────────────
	//
	// The assertion that makes this a workflow test rather than an
	// arithmetic one: a turn that ran three rounds and changed nothing
	// would satisfy everything above.
	if r := e.writeReceipt(e.mine, msg); r.Executed != 1 || r.Refused != 0 {
		t.Fatalf("receipt executed=%d failed=%d refused=%d, want exactly one write",
			r.Executed, r.Failed, r.Refused)
	}
	e.llm.script([]call{{tools.ArtifactGetTool, `{"artifact_id":"` + artifactID + `"}`}})
	e.turn(e.mine, conv, "como está a lista?")
	got := e.toolResult(t, tools.ArtifactGetTool)
	items, _ := got["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("%d entries, want 1: %v", len(items), got)
	}
	entry, _ := items[0].(map[string]any)
	if entry["done"] != true {
		t.Fatalf("the entry is still not done: %v; the workflow ran and "+
			"changed nothing", entry)
	}
}

// The other shape the ceiling made unreachable: a relation needs both of
// its endpoints to exist first, so creating them used to consume the budget
// the relation itself needed. Three rounds, and no relation was ever
// created in the entire live battery.
func TestCreatingBothEndpointsAndTheRelationFitsInOneTurn(t *testing.T) {
	e := newAgentEnv(t)
	_, conv := e.newPalaceAgent(e.mine)

	e.llm.script(
		[]call{{tools.ArtifactCreateTool,
			`{"kind":"project","title":"Migração de banco"}`}},
		[]call{{tools.MemoryCreateTool,
			`{"kind":"decision","content":"Vamos usar Go no serviço novo."}`}},
		// The third round can only be written once the two ids exist, which
		// is why it never ran before.
		[]call{{tools.RelationListTool, `{}`}},
	)
	s, msg := e.turn(e.mine, conv, "registra o projeto e a decisão, e relaciona")

	if msg.FinishReason != string(chatdomain.FinishStop) {
		t.Fatalf("finish_reason = %q, want the turn to have answered", msg.FinishReason)
	}
	for _, name := range []chatdomain.ToolName{
		tools.ArtifactCreateTool, tools.MemoryCreateTool, tools.RelationListTool,
	} {
		ev, ok := s.finished(name)
		if !ok || ev.Status != string(chatdomain.ToolCallOK) {
			t.Fatalf("%s ended %+v; three dependent rounds must fit", name, ev)
		}
	}
	// Two writes ran and the third round was a read, so the receipt says
	// two — which is the number the ceiling used to make impossible.
	if r := e.writeReceipt(e.mine, msg); r.Executed != 2 || r.Refused != 0 {
		t.Fatalf("receipt executed=%d refused=%d, want two writes and no refusal",
			r.Executed, r.Refused)
	}
}

/* ── B · a fourth layer is still refused, and refused honestly ───────── */

// The ceiling moved; it did not go away.
//
// A fourth dependent step is stopped, and everything S6.2 established about
// how a turn is stopped still holds: the work already done is real and
// visible, the refused call is audited as NOT_EXECUTED rather than
// vanishing, and the receipt counts it as refused rather than executed.
func TestAFourthDependentStepIsRefusedWithEverythingBeforeItIntact(t *testing.T) {
	e := newAgentEnv(t)
	_, conv := e.newPalaceAgent(e.mine)
	artifactID, itemID := e.aListWithOneItem(e.mine, conv, "Compras", "arroz")

	// Four rounds asked for; three may run.
	e.llm.script(
		[]call{{tools.ArtifactListTool, `{"kind":"list"}`}},
		[]call{{tools.ArtifactGetTool, `{"artifact_id":"` + artifactID + `"}`}},
		[]call{{tools.ItemUpdateTool,
			`{"artifact_id":"` + artifactID + `","item_id":"` + itemID + `","done":true}`}},
		[]call{{tools.ItemRemoveTool,
			`{"artifact_id":"` + artifactID + `","item_id":"` + itemID + `"}`}},
	)
	s, msg, err := e.turnExpectingStop(e.mine, conv, "marca e depois remove")

	if err == nil {
		t.Fatal("the fourth round was not refused; the ceiling is gone")
	}
	if msg == nil {
		t.Fatal("a stopped turn persisted nothing")
	}
	if msg.FinishReason != string(chatdomain.FinishToolRoundLimit) {
		t.Fatalf("finish_reason = %q, want %q",
			msg.FinishReason, chatdomain.FinishToolRoundLimit)
	}
	if e.llm.streamCalls != maxToolRoundsForTest {
		t.Errorf("%d provider calls, want %d", e.llm.streamCalls, maxToolRoundsForTest)
	}

	// ── The three that were permitted really ran ───────────────────────
	for _, name := range []chatdomain.ToolName{
		tools.ArtifactListTool, tools.ArtifactGetTool, tools.ItemUpdateTool,
	} {
		ev, ok := s.finished(name)
		if !ok || ev.Status != string(chatdomain.ToolCallOK) {
			t.Fatalf("%s ended %+v, want ok", name, ev)
		}
	}

	// ── The fourth is recorded, not vanished ───────────────────────────
	//
	// NO REQUESTED TOOL DISAPPEARS SILENTLY.
	ev, ok := s.finished(tools.ItemRemoveTool)
	if !ok {
		t.Fatal("the refused call produced no terminal frame at all")
	}
	if ev.Status != string(chatdomain.ToolCallNotExecuted) {
		t.Errorf("the refused call reported %q, want %q",
			ev.Status, chatdomain.ToolCallNotExecuted)
	}
	if ev.ErrorCode != string(chatdomain.ToolErrRoundLimit) {
		t.Errorf("the refused call carries error_code %q, want %q",
			ev.ErrorCode, chatdomain.ToolErrRoundLimit)
	}

	// ── The receipt counts it as refused, never as executed ────────────
	r := e.writeReceipt(e.mine, msg)
	if r.Executed != 1 {
		t.Errorf("executed = %d, want 1: only item.update really wrote", r.Executed)
	}
	if r.Refused != 1 {
		t.Errorf("refused = %d, want 1: the removal the ceiling turned away", r.Refused)
	}

	// ── The side effect of round three survived the stop ───────────────
	//
	// The turn failed AFTER a real write. The write is not rolled back,
	// and pretending otherwise is what would make the audit a lie.
	e.llm.script([]call{{tools.ArtifactGetTool, `{"artifact_id":"` + artifactID + `"}`}})
	e.turn(e.mine, conv, "como ficou?")
	got := e.toolResult(t, tools.ArtifactGetTool)
	items, _ := got["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("%d entries, want 1: the refused removal must not have run: %v",
			len(items), got)
	}
	entry, _ := items[0].(map[string]any)
	if entry["done"] != true {
		t.Error("the write from round three was lost when the turn stopped")
	}
}

// quoteJSON is a JSON string literal, for building scripted arguments.
func quoteJSON(s string) string {
	out := make([]rune, 0, len(s)+2)
	out = append(out, '"')
	for _, r := range s {
		switch r {
		case '"', '\\':
			out = append(out, '\\', r)
		default:
			out = append(out, r)
		}
	}
	return string(append(out, '"'))
}
