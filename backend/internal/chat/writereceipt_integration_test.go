//go:build integration

// Write receipts: what the SYSTEM knows about a turn, as opposed to what
// the model said about it.
//
// ── The incident this suite exists for ─────────────────────────────────
// A live financial agent answered "8 transações importadas" in a turn that
// made ZERO tool calls, against a ledger holding zero transactions. The
// runtime was correct and the person was misinformed, because the product
// renders assistant prose and the ABSENCE of a tool call is invisible.
//
// Every test below asserts against the receipt, never against the words.

package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/chat/domain"
	"github.com/corsi/backend/internal/chat/ports"
)

/* ── a write that fails, for case C ──────────────────────────────────── */

type brokenWriteTool struct{}

const brokenWriteToolName = "system.brokenwrite"

func (brokenWriteTool) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:        brokenWriteToolName,
		Title:       "Broken write",
		Description: "Fails while changing a thing.",
		Effect:      domain.EffectWrite,
		Internal:    true,
		Schema: domain.ToolSchema{
			Properties: map[string]domain.ToolProperty{
				"value": {Type: domain.TypeString, MaxLength: 100},
			},
		},
	}
}

func (brokenWriteTool) Execute(_ context.Context, _ map[string]any) (domain.ToolOutput, error) {
	return nil, domain.ToolError(domain.ToolErrExecutionFailed, "the write did not complete")
}

type apiWriteExecution struct {
	ToolCallID string `json:"tool_call_id"`
	Capability string `json:"capability"`
	Status     string `json:"status"`
	ErrorCode  string `json:"error_code"`
}

type apiWriteReceipt struct {
	MessageID string              `json:"message_id"`
	Writes    []apiWriteExecution `json:"writes"`
	Executed  int                 `json:"executed"`
	Failed    int                 `json:"failed"`
	Refused   int                 `json:"refused"`
}

// receipts reads the transcript's receipts the way a client would.
func (e *env) receipts(ws uuid.UUID, conversationID string) map[string]apiWriteReceipt {
	e.t.Helper()
	rec := e.do("GET", "/chat/conversations/"+conversationID+"/messages", ws, nil)
	wantStatus(e.t, rec, http.StatusOK)
	return decode[struct {
		WriteReceipts map[string]apiWriteReceipt `json:"write_receipts"`
	}](e.t, rec).WriteReceipts
}

// theOnlyReceipt returns the single assistant turn's receipt.
func theOnlyReceipt(t *testing.T, got map[string]apiWriteReceipt) apiWriteReceipt {
	t.Helper()
	if len(got) != 1 {
		t.Fatalf("%d receipts, want exactly one assistant turn", len(got))
	}
	for _, r := range got {
		return r
	}
	return apiWriteReceipt{}
}

func askMutate(id, value string) []ports.StreamEvent {
	return askTool(id, mutateToolName, `{"value":"`+value+`"}`)
}

func askBrokenWrite(id string) []ports.StreamEvent {
	return askTool(id, brokenWriteToolName, `{"value":"x"}`)
}

/* ── A · a real write is reported as EXECUTED ────────────────────────── */

func TestAWriteThatRanIsReportedAsExecuted(t *testing.T) {
	e := newEnv(t, withExtraTools(mutateTool{}))
	s := e.seed(e.wsA)
	e.authorizeTool(e.wsA, s.agentID, mutateToolName)
	e.llm.rounds = [][]ports.StreamEvent{
		askMutate("call_1", "a"),
		answer("pronto, alterei", 40, 8),
	}

	e.send(e.wsA, s.conversationID, "altera isso")

	r := theOnlyReceipt(t, e.receipts(e.wsA, s.conversationID))
	if r.Executed != 1 || r.Failed != 0 || r.Refused != 0 {
		t.Fatalf("receipt = %+v, want exactly one execution", r)
	}
	if len(r.Writes) != 1 || r.Writes[0].Status != string(domain.WriteExecuted) {
		t.Fatalf("writes = %+v", r.Writes)
	}
	if r.Writes[0].Capability != mutateToolName {
		t.Errorf("capability = %q", r.Writes[0].Capability)
	}
}

/* ── B · the incident: a claim with no call ──────────────────────────── */

// The model says it did the thing and calls nothing. The receipt is the
// only thing standing between that sentence and a person believing it.
func TestAClaimWithoutAToolCallProducesNoExecution(t *testing.T) {
	e := newEnv(t, withExtraTools(mutateTool{}))
	s := e.seed(e.wsA)
	e.authorizeTool(e.wsA, s.agentID, mutateToolName)
	// One round, pure prose. This is verbatim the shape of the incident.
	e.scriptNext(answer("8 transações importadas", 40, 8))

	e.send(e.wsA, s.conversationID, "importa")

	if n := len(e.toolCalls(e.wsA, s.conversationID)); n != 0 {
		t.Fatalf("%d tool calls, want none", n)
	}
	r := theOnlyReceipt(t, e.receipts(e.wsA, s.conversationID))
	// The receipt EXISTS and says nothing ran. An absent key would be
	// indistinguishable from a transcript that was never asked about, and
	// a client would have nothing to render.
	if r.Executed != 0 || len(r.Writes) != 0 {
		t.Fatalf("receipt = %+v, want an explicit nothing", r)
	}
	if r.MessageID == "" {
		t.Fatal("the receipt must name the turn it is about")
	}
}

/* ── C · a write that ran and failed ─────────────────────────────────── */

func TestAFailedWriteIsNeverReportedAsExecuted(t *testing.T) {
	e := newEnv(t, withExtraTools(brokenWriteTool{}))
	s := e.seed(e.wsA)
	e.authorizeTool(e.wsA, s.agentID, brokenWriteToolName)
	e.llm.rounds = [][]ports.StreamEvent{
		askBrokenWrite("call_1"),
		answer("não consegui", 40, 8),
	}

	e.send(e.wsA, s.conversationID, "altera")

	r := theOnlyReceipt(t, e.receipts(e.wsA, s.conversationID))
	if r.Executed != 0 || r.Failed != 1 {
		t.Fatalf("receipt = %+v, want one failure and no execution", r)
	}
	if r.Writes[0].Status != string(domain.WriteFailed) {
		t.Fatalf("status = %q", r.Writes[0].Status)
	}
}

/* ── D · a revoked capability ────────────────────────────────────────── */

func TestARevokedWriteIsRefusedAndNeverConfirmed(t *testing.T) {
	e := newEnv(t, withExtraTools(mutateTool{}))
	s := e.seed(e.wsA) // deliberately not granted
	e.llm.rounds = [][]ports.StreamEvent{
		askMutate("call_1", "a"),
		answer("não tenho permissão", 40, 8),
	}

	e.send(e.wsA, s.conversationID, "altera")

	r := theOnlyReceipt(t, e.receipts(e.wsA, s.conversationID))
	if r.Executed != 0 || r.Refused != 1 {
		t.Fatalf("receipt = %+v, want a refusal and no execution", r)
	}
	if r.Writes[0].Status != string(domain.WriteNotExecuted) {
		t.Fatalf("status = %q, want NOT_EXECUTED", r.Writes[0].Status)
	}
	if r.Writes[0].ErrorCode != string(domain.ToolErrNotAuthorized) {
		t.Errorf("error code = %q", r.Writes[0].ErrorCode)
	}
}

/* ── E · Confidential stays redacted, the receipt still works ────────── */

type confidentialWriteTool struct{}

const confidentialWriteToolName = "system.secretwrite"

func (confidentialWriteTool) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:         confidentialWriteToolName,
		Title:        "Confidential write",
		Description:  "Changes a thing, and what it changed must not be kept.",
		Effect:       domain.EffectWrite,
		Confidential: true,
		Internal:     true,
		Schema: domain.ToolSchema{
			Properties: map[string]domain.ToolProperty{
				"value": {Type: domain.TypeString, MaxLength: 100},
			},
		},
	}
}

func (confidentialWriteTool) Execute(_ context.Context, _ map[string]any) (domain.ToolOutput, error) {
	return domain.ToolOutput{"amount_cents": 899000}, nil
}

// A receipt must be usable for a capability whose payload is withheld,
// because those are exactly the capabilities that move money. It carries
// what ran and how it ended, and nothing about what it touched.
func TestAConfidentialWriteIsRedactedAndStillProducesAReceipt(t *testing.T) {
	e := newEnv(t, withExtraTools(confidentialWriteTool{}))
	s := e.seed(e.wsA)
	e.authorizeTool(e.wsA, s.agentID, confidentialWriteToolName)
	e.llm.rounds = [][]ports.StreamEvent{
		askTool("call_1", confidentialWriteToolName, `{"value":"899000"}`),
		answer("registrado", 40, 8),
	}

	e.send(e.wsA, s.conversationID, "registra")

	calls := e.toolCalls(e.wsA, s.conversationID)
	if len(calls) != 1 || !calls[0].Redacted {
		t.Fatalf("audit row = %+v, want it redacted", calls)
	}
	if calls[0].Arguments != nil || calls[0].Result != nil {
		t.Fatal("a Confidential capability leaked its payload into the audit")
	}

	r := theOnlyReceipt(t, e.receipts(e.wsA, s.conversationID))
	if r.Executed != 1 {
		t.Fatalf("receipt = %+v, want the execution confirmed", r)
	}
	// And the receipt itself carries no payload, so it can be rendered,
	// logged or returned without depending on redaction elsewhere.
	if r.Writes[0].Capability != confidentialWriteToolName {
		t.Errorf("capability = %q", r.Writes[0].Capability)
	}
}

/* ── F · receipts do not cross conversations or workspaces ───────────── */

func TestReceiptsDoNotCrossConversationsOrWorkspaces(t *testing.T) {
	e := newEnv(t, withExtraTools(mutateTool{}))
	a := e.seed(e.wsA)
	e.authorizeTool(e.wsA, a.agentID, mutateToolName)
	e.llm.rounds = [][]ports.StreamEvent{
		askMutate("call_1", "a"),
		answer("feito", 40, 8),
	}
	e.send(e.wsA, a.conversationID, "altera")

	if theOnlyReceipt(t, e.receipts(e.wsA, a.conversationID)).Executed != 1 {
		t.Fatal("the write was not recorded where it happened")
	}

	// A second conversation in the SAME workspace sees nothing of it.
	other := e.newConversation(e.wsA, a.agentID)
	e.scriptNext(answer("olá", 40, 8))
	e.send(e.wsA, other, "oi")
	if r := theOnlyReceipt(t, e.receipts(e.wsA, other)); r.Executed != 0 {
		t.Fatalf("a receipt crossed conversations: %+v", r)
	}

	// And workspace B cannot read the first conversation at all.
	rec := e.do("GET", "/chat/conversations/"+a.conversationID+"/messages", e.wsB, nil)
	if rec.Code == http.StatusOK {
		t.Fatal("workspace B read another workspace's transcript")
	}
}

/* ── the stream carries it too ───────────────────────────────────────── */

// So a live client never has to infer from prose whether anything changed.
func TestTheDoneFrameCarriesTheTurnsReceipt(t *testing.T) {
	e := newEnv(t, withExtraTools(mutateTool{}))
	s := e.seed(e.wsA)
	e.authorizeTool(e.wsA, s.agentID, mutateToolName)
	e.llm.rounds = [][]ports.StreamEvent{
		askMutate("call_1", "a"),
		answer("feito", 40, 8),
	}

	rec := e.do("POST", "/chat/conversations/"+s.conversationID+"/messages", e.wsA,
		map[string]any{"content": "altera"})
	wantStatus(t, rec, http.StatusOK)

	var receipt *apiWriteReceipt
	for _, ev := range parseSSE(t, rec.Body.String()) {
		if ev.event != "done" {
			continue
		}
		var payload struct {
			WriteReceipt *apiWriteReceipt `json:"write_receipt"`
		}
		if err := json.Unmarshal([]byte(ev.data), &payload); err != nil {
			t.Fatalf("decode done frame: %v", err)
		}
		receipt = payload.WriteReceipt
	}
	if receipt == nil {
		t.Fatal("the done frame carried no receipt")
	}
	if receipt.Executed != 1 {
		t.Fatalf("receipt = %+v", *receipt)
	}
}

/* ── the live frame and the record agree ─────────────────────────────── */

// A refusal used to read `error` on the stream and `not_executed` in the
// audit: two records of one event, disagreeing about what happened. The
// frame now carries the outcome's own status.
func TestTheStreamAndTheAuditDescribeARefusalTheSameWay(t *testing.T) {
	e := newEnv(t, withExtraTools(mutateTool{}))
	s := e.seed(e.wsA) // deliberately not granted
	e.llm.rounds = [][]ports.StreamEvent{
		askMutate("call_1", "a"),
		answer("não tenho permissão", 40, 8),
	}

	rec := e.do("POST", "/chat/conversations/"+s.conversationID+"/messages", e.wsA,
		map[string]any{"content": "altera"})
	wantStatus(t, rec, http.StatusOK)

	var frameStatus string
	for _, ev := range parseSSE(t, rec.Body.String()) {
		if ev.event != "tool" {
			continue
		}
		var f struct {
			Status string `json:"status"`
		}
		if err := json.Unmarshal([]byte(ev.data), &f); err != nil {
			t.Fatalf("decode tool frame: %v", err)
		}
		if f.Status != "running" {
			frameStatus = f.Status
		}
	}
	calls := e.toolCalls(e.wsA, s.conversationID)
	if len(calls) != 1 {
		t.Fatalf("%d audit rows", len(calls))
	}
	if frameStatus != calls[0].Status {
		t.Fatalf("stream said %q and the audit says %q", frameStatus, calls[0].Status)
	}
	if frameStatus != string(domain.ToolCallNotExecuted) {
		t.Fatalf("status = %q, want not_executed", frameStatus)
	}
}

// A read that ran is not a change, and must never produce a write receipt.
func TestAReadToolProducesNoWriteReceipt(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)
	e.authorizeTool(e.wsA, s.agentID, echoTool)
	e.llm.rounds = [][]ports.StreamEvent{
		askEcho("call_1", "olá"),
		answer("li isso", 40, 8),
	}

	e.send(e.wsA, s.conversationID, "lê")

	if n := len(e.toolCalls(e.wsA, s.conversationID)); n != 1 {
		t.Fatalf("%d tool calls, want the read recorded", n)
	}
	r := theOnlyReceipt(t, e.receipts(e.wsA, s.conversationID))
	if r.Executed != 0 || len(r.Writes) != 0 {
		t.Fatalf("receipt = %+v, want a read to leave it empty", r)
	}
}

/* ── historical rows, recorded before effect existed ─────────────────── */

// Rows written before the effect column existed are backfilled to `read`.
//
// Not a guess dressed as data: the effect of a past call is genuinely
// unknown, and `read` is the value that claims the least. The consequence
// is the one that matters — no historical turn can be presented as a
// confirmed write on the strength of a default — and it is asserted here
// rather than left to the migration's comment.
func TestHistoricalToolCallsAreNeverPresentedAsConfirmedWrites(t *testing.T) {
	e := newEnv(t, withExtraTools(mutateTool{}))
	s := e.seed(e.wsA)
	e.authorizeTool(e.wsA, s.agentID, mutateToolName)
	e.llm.rounds = [][]ports.StreamEvent{
		askMutate("call_1", "a"),
		answer("feito", 40, 8),
	}
	e.send(e.wsA, s.conversationID, "altera")

	if theOnlyReceipt(t, e.receipts(e.wsA, s.conversationID)).Executed != 1 {
		t.Fatal("the write was not recorded")
	}

	// Reproduce a pre-migration row: the backfill default, applied to a
	// call that really was a write.
	if _, err := e.pool.Exec(context.Background(),
		`UPDATE chat.tool_calls SET effect = 'read' WHERE workspace_id = $1`, e.wsA); err != nil {
		t.Fatal(err)
	}
	r := theOnlyReceipt(t, e.receipts(e.wsA, s.conversationID))
	if r.Executed != 0 || len(r.Writes) != 0 {
		t.Fatalf("receipt = %+v; a row with no recorded effect must not confirm a write", r)
	}
	// And the audit row itself is still readable, which is the other half
	// of the promise: history does not become unreadable, it becomes
	// unconfirmable.
	if calls := e.toolCalls(e.wsA, s.conversationID); len(calls) != 1 || calls[0].ToolName != mutateToolName {
		t.Fatalf("the historical row stopped being readable: %+v", calls)
	}
}
