//go:build integration

package metathreads

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	chatapp "github.com/corsi/backend/internal/chat/app"

	chatdomain "github.com/corsi/backend/internal/chat/domain"
	mttools "github.com/corsi/backend/internal/integrations/metathreads/tools"
	threadstools "github.com/corsi/backend/internal/threads/tools"
)

/*
External read grounding, through the real turn loop.

── The failure ────────────────────────────────────────────────────────
A content agent answered "1.535 seguidores, 40.055 views" in a turn with
ZERO tool calls. The true figures were 163 and 224. Every layer behaved;
the person read invented numbers because the product renders prose and the
absence of a read is silent.

── What is proved here ────────────────────────────────────────────────
That the receipt derives from EXECUTION and contradicts the prose. The
scripted model below says the same false sentence the real one did — and
the receipt beside it says NO_EXTERNAL_READ either way.
*/

// receiptFor runs a turn and returns the evidence state the product would
// show beside it.
func (e *env) receiptFor(convID uuid.UUID, prompt string) chatdomain.ReadReceipt {
	e.t.Helper()
	sink := &collectSink{}
	msg, err := e.chatSvc.SendMessage(ctxFor(e.wsA), chatapp.SendMessageInput{
		WorkspaceID: e.wsA, ConversationID: convID, Content: prompt,
	}, sink)
	if err != nil {
		e.t.Fatalf("send message: %v", err)
	}
	got, err := e.chatSvc.ReadReceipts(ctxFor(e.wsA), e.wsA, convID, []uuid.UUID{msg.ID})
	if err != nil {
		e.t.Fatalf("read receipts: %v", err)
	}
	return got[msg.ID]
}

/* ── the false claim ─────────────────────────────────────────────────── */

// THE regression. The model produces the exact sentence that shipped, and
// the receipt refuses to corroborate it.
func TestAFabricatedMetricGetsNoExternalEvidence(t *testing.T) {
	e := newEnv(t)
	e.connect(e.wsA)
	agentID, conv := e.newAgent(e.wsA, "content")
	e.authorize(e.wsA, agentID, allMetaTools...)

	// Word for word what the live agent said, with no tool call behind it.
	e.llm.scriptReply("Métricas gerais da conta: Seguidores: 1.535, Views: 40.055, Likes: 1.375")
	r := e.receiptFor(conv, "quantos seguidores eu tenho?")

	if r.Status != chatdomain.NoExternalRead {
		t.Fatalf("status = %q, want %q", r.Status, chatdomain.NoExternalRead)
	}
	if r.Verifiable() {
		t.Fatal("a turn that called nothing was reported verifiable")
	}
	// And the product knows the absence is meaningful here, because this
	// agent could have read.
	if !r.Available {
		t.Error("available = false for an agent holding six external capabilities")
	}
	// The receipt carries no trace of the sentence, which is the point:
	// the two are derived from different things.
	if len(r.Reads) != 0 {
		t.Errorf("%d reads recorded for a turn with none", len(r.Reads))
	}
}

// A real read makes the same kind of answer verifiable.
func TestARealReadMakesTheTurnVerifiable(t *testing.T) {
	e := newEnv(t)
	e.connect(e.wsA)
	agentID, conv := e.newAgent(e.wsA, "content")
	e.authorize(e.wsA, agentID, allMetaTools...)

	e.llm.scriptCalls(chatdomain.ToolCall{
		Name: mttools.ProfileInsightsTool, Arguments: `{}`,
	})
	r := e.receiptFor(conv, "quantos seguidores eu tenho?")

	if r.Status != chatdomain.VerifiedExternalRead || !r.Verifiable() {
		t.Fatalf("status = %q", r.Status)
	}
	if r.Verified != 1 {
		t.Errorf("verified = %d", r.Verified)
	}
	if r.Reads[0].Source != "meta_threads" {
		t.Errorf("source = %q", r.Reads[0].Source)
	}
	// No payload rode along.
	if strings.Contains(r.Reads[0].Capability.String(), "163") {
		t.Error("the receipt carries a value")
	}
}

// A failed read is reported as failed — never as verified, and never as
// the silence a fabricated answer produces.
func TestAFailedReadIsReportedAsFailedNotAsSilence(t *testing.T) {
	e := newEnv(t)
	e.connect(e.wsA)
	agentID, conv := e.newAgent(e.wsA, "content")
	e.authorize(e.wsA, agentID, allMetaTools...)
	e.meta.failOn("/threads_insights", 500, `{"error":{"message":"An unknown error occurred","code":1}}`)

	e.llm.scriptCalls(chatdomain.ToolCall{Name: mttools.ProfileInsightsTool, Arguments: `{}`})
	r := e.receiptFor(conv, "quantos seguidores eu tenho?")

	if r.Status != chatdomain.FailedExternalRead {
		t.Fatalf("status = %q, want %q", r.Status, chatdomain.FailedExternalRead)
	}
	if r.Verifiable() {
		t.Fatal("a failed read was reported verifiable")
	}
	if r.Failed != 1 {
		t.Errorf("failed = %d", r.Failed)
	}
}

/* ── ours is not theirs ──────────────────────────────────────────────── */

// Reading our OWN pipeline is not evidence about Meta. A turn full of
// internal calls still carries NO_EXTERNAL_READ, because "you have three
// drafts" and "you have 163 followers" are different kinds of claim.
func TestInternalReadsDoNotCountAsExternalEvidence(t *testing.T) {
	e := newEnv(t)
	e.connect(e.wsA)
	agentID, conv := e.newAgent(e.wsA, "content")
	e.authorize(e.wsA, agentID, allMetaTools...)
	e.authorize(e.wsA, agentID, allThreadTools...)

	e.llm.scriptCalls(chatdomain.ToolCall{
		Name: threadstools.ThreadListTool, Arguments: `{}`,
	})
	r := e.receiptFor(conv, "o que eu tenho em rascunho?")

	if r.Status != chatdomain.NoExternalRead {
		t.Fatalf("status = %q; an internal read was counted as external", r.Status)
	}
}

// An agent with no external capability produces the same status and is
// marked unavailable, so a surface can stay quiet where the absence means
// nothing.
func TestAnAgentWithNoExternalCapabilityIsMarkedUnavailable(t *testing.T) {
	e := newEnv(t)
	agentID, conv := e.newAgent(e.wsA, "internal only")
	e.authorize(e.wsA, agentID, allThreadTools...)

	e.llm.scriptReply("ok")
	r := e.receiptFor(conv, "oi")

	if r.Status != chatdomain.NoExternalRead {
		t.Fatalf("status = %q", r.Status)
	}
	if r.Available {
		t.Error("available = true for an agent with no external capability")
	}
}

/* ── evidence does not travel ────────────────────────────────────────── */

// A read in one turn does not make the NEXT turn verified. This is the
// mechanism behind the real failure: the model had read that account
// earlier and treated the figures as still known.
func TestEvidenceDoesNotCarryToTheNextTurn(t *testing.T) {
	e := newEnv(t)
	e.connect(e.wsA)
	agentID, conv := e.newAgent(e.wsA, "content")
	e.authorize(e.wsA, agentID, allMetaTools...)

	e.llm.scriptCalls(chatdomain.ToolCall{Name: mttools.ProfileInsightsTool, Arguments: `{}`})
	if first := e.receiptFor(conv, "quantos seguidores?"); !first.Verifiable() {
		t.Fatalf("the first turn did not read: %q", first.Status)
	}

	// Same conversation, next turn, answering from what it just saw.
	e.llm.scriptReply("Como eu disse, você tem 163 seguidores.")
	second := e.receiptFor(conv, "e agora?")

	if second.Status != chatdomain.NoExternalRead {
		t.Fatalf("the second turn inherited the first turn's evidence: %q", second.Status)
	}
	if second.Verifiable() {
		t.Fatal("evidence carried across turns")
	}
}

// Nor across conversations.
func TestEvidenceDoesNotCrossConversations(t *testing.T) {
	e := newEnv(t)
	e.connect(e.wsA)
	agentID, convA := e.newAgent(e.wsA, "content")
	e.authorize(e.wsA, agentID, allMetaTools...)

	e.llm.scriptCalls(chatdomain.ToolCall{Name: mttools.ProfileInsightsTool, Arguments: `{}`})
	if r := e.receiptFor(convA, "quantos seguidores?"); !r.Verifiable() {
		t.Fatalf("setup: %q", r.Status)
	}

	convB := e.newConversation(e.wsA, agentID)
	e.llm.scriptReply("Você tem 163 seguidores.")
	if r := e.receiptFor(convB, "quantos seguidores?"); r.Verifiable() {
		t.Fatal("a read in one conversation verified a turn in another")
	}
}

/* ── what the model is told ──────────────────────────────────────────── */

// The declaration the model receives names the constraint. Defence in
// depth: the receipt holds whether or not the model reads this.
func TestAnExternalCapabilityDeclaresItselfToTheModel(t *testing.T) {
	for _, tool := range mttools.New(nil) {
		def := tool.Definition()
		if !def.External {
			t.Errorf("%s reads Meta and does not declare itself external", def.Name)
		}
		declared := def.DeclaredDescription()
		if !strings.Contains(declared, "OUTSIDE this product") {
			t.Errorf("%s: the declaration does not warn the model: %s", def.Name, declared)
		}
		if !strings.Contains(declared, "THIS turn") {
			t.Errorf("%s: the declaration does not require a current call", def.Name)
		}
	}
	// And an internal capability says none of it.
	for _, tool := range threadstools.New(nil) {
		if tool.Definition().External {
			t.Errorf("%s is internal and declares itself external", tool.Definition().Name)
		}
		if strings.Contains(tool.Definition().DeclaredDescription(), "OUTSIDE this product") {
			t.Errorf("%s carries the external notice", tool.Definition().Name)
		}
	}
}
