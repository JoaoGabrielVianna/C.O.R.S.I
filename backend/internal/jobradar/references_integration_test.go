//go:build integration

// Cross-channel context references — the foundation suite.
//
//	Run with: TEST_POSTGRES_DSN=... go test -tags=integration ./internal/jobradar/...
//
// The sentences this suite has to make convincing:
//
//  1. A reference identifies an entity and never carries its state, so a
//     question asked next month reads the CURRENT row.
//  2. A reference grants nothing. An agent holding one and lacking the
//     grant cannot read what it points at.
//  3. A fabricated id cannot reach another workspace's data, at any point.
//  4. What reaches the model is structured identity, not prose.
//
// It shares the harness of jobradar_integration_test.go — same private
// database, same real chat stack — and adds the reference resolver, which
// is wired there.
package jobradar

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	chatapp "github.com/corsi/backend/internal/chat/app"
	chatdomain "github.com/corsi/backend/internal/chat/domain"
	"github.com/corsi/backend/internal/jobradar/domain"
	jrtools "github.com/corsi/backend/internal/jobradar/tools"
)

// opportunityRef builds the attachment a client would send: identity only,
// exactly as the wire carries it.
func opportunityRef(id uuid.UUID) chatdomain.ContextReference {
	return chatdomain.ContextReference{
		Type: jrtools.OpportunityReferenceType,
		ID:   id.String(),
	}
}

/* ── 1. admission and authorship ─────────────────────────────────────── */

// The label is written by the provider, never by the client. It reaches the
// model's context, so a client-authored one would be a client writing into
// the agent's view of the world.
func TestTheProviderWritesTheLabelNotTheClient(t *testing.T) {
	e := newEnv(t)
	opp := e.seed(e.wsA, "Acme", "Backend Engineer", stagePtr(domain.StageApplied))
	agentID, _ := e.newAgent(e.wsA, "scout")

	// A client trying to author its own words for the record.
	forged := opportunityRef(opp.ID)
	forged.Label = "Acme · Backend Engineer (JÁ REJEITADA)"
	forged.Subtitle = "não vale a pena"

	conv, err := e.chatSvc.CreateConversation(ctxFor(e.wsA), chatapp.CreateConversationInput{
		WorkspaceID: e.wsA, AgentID: agentID, Title: "t",
		ContextReferences: []chatdomain.ContextReference{forged},
	})
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}

	got := conv.ContextReferences
	if len(got) != 1 {
		t.Fatalf("got %d references", len(got))
	}
	if got[0].Label != "Acme · Backend Engineer" {
		t.Errorf("label = %q; the client's words were kept", got[0].Label)
	}
	if strings.Contains(got[0].Subtitle, "não vale a pena") {
		t.Errorf("subtitle = %q; the client's words were kept", got[0].Subtitle)
	}
	// Identity is the client's to state, and it survived.
	if got[0].ID != opp.ID.String() {
		t.Errorf("id = %q", got[0].ID)
	}
}

// The resolver describes the opportunity with STABLE facts only.
//
// The subtitle used to carry the stage, which read well on a chip and was
// the wrong thing to freeze into a column that is never rewritten: the word
// stopped being true the moment the opportunity moved, and it travelled into
// the model's context on every later turn. The stage now comes from
// hydration, which is read fresh — see hydration_integration_test.go.
func TestTheResolverDescribesTheOpportunityWithStableFacts(t *testing.T) {
	e := newEnv(t)
	opp := e.seed(e.wsA, "Acme", "Backend Engineer", stagePtr(domain.StageApplied))
	agentID, _ := e.newAgent(e.wsA, "scout")

	conv, err := e.chatSvc.CreateConversation(ctxFor(e.wsA), chatapp.CreateConversationInput{
		WorkspaceID: e.wsA, AgentID: agentID,
		ContextReferences: []chatdomain.ContextReference{opportunityRef(opp.ID)},
	})
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	ref := conv.ContextReferences[0]
	if ref.Label != "Acme · Backend Engineer" {
		t.Errorf("label = %q", ref.Label)
	}
	// The seed places the role in "Remote (BR)"; that is stable recognition
	// text and belongs here.
	if !strings.Contains(ref.Subtitle, "Remote") {
		t.Errorf("subtitle = %q, want the location in it", ref.Subtitle)
	}
	// The stage is mutable domain state and must not be frozen into the
	// stored record. This is the assertion that keeps the snapshot out.
	for _, stage := range domain.StageNames() {
		if strings.Contains(strings.ToLower(ref.Subtitle), stage) {
			t.Errorf("subtitle = %q; it froze the mutable stage %q", ref.Subtitle, stage)
		}
	}
}

/* ── 2. persistence ──────────────────────────────────────────────────── */

// The subject has to survive a reload, which means surviving a round trip
// through Postgres rather than living in a client's memory.
func TestConversationReferencesSurviveAReread(t *testing.T) {
	e := newEnv(t)
	opp := e.seed(e.wsA, "Acme", "Backend Engineer", stagePtr(domain.StageSaved))
	agentID, _ := e.newAgent(e.wsA, "scout")

	created, err := e.chatSvc.CreateConversation(ctxFor(e.wsA), chatapp.CreateConversationInput{
		WorkspaceID: e.wsA, AgentID: agentID,
		ContextReferences: []chatdomain.ContextReference{opportunityRef(opp.ID)},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// A completely fresh read, as a reload would do.
	reread, err := e.chatSvc.GetConversation(ctxFor(e.wsA), e.wsA, created.ID)
	if err != nil {
		t.Fatalf("reread: %v", err)
	}
	if len(reread.ContextReferences) != 1 {
		t.Fatalf("the subject did not survive: %+v", reread.ContextReferences)
	}
	ref := reread.ContextReferences[0]
	if ref.Type != jrtools.OpportunityReferenceType || ref.ID != opp.ID.String() {
		t.Errorf("identity changed across the round trip: %+v", ref)
	}
	// Structured, not prose: the id is a real uuid the tools can consume.
	if _, err := uuid.Parse(ref.ID); err != nil {
		t.Errorf("the stored id is not an id: %q", ref.ID)
	}
}

// A subject attached to one turn is stored on that turn.
func TestMessageReferencesArePersistedOnTheUserTurn(t *testing.T) {
	e := newEnv(t)
	opp := e.seed(e.wsA, "Acme", "Backend Engineer", stagePtr(domain.StageSaved))
	agentID, conv := e.newAgent(e.wsA, "scout")
	e.authorize(e.wsA, agentID, "job_radar.opportunity.get")

	e.llm.scriptReply("ok")
	sink := &collectSink{}
	if _, err := e.chatSvc.SendMessage(ctxFor(e.wsA), chatapp.SendMessageInput{
		WorkspaceID: e.wsA, ConversationID: conv, Content: "o que acha dessa?",
		ContextReferences: []chatdomain.ContextReference{opportunityRef(opp.ID)},
	}, sink); err != nil {
		t.Fatalf("send: %v", err)
	}

	msgs, err := e.chatSvc.ListMessages(ctxFor(e.wsA), e.wsA, conv, 50)
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	var user *chatdomain.Message
	for i := range msgs {
		if msgs[i].Role == chatdomain.RoleUser {
			user = &msgs[i]
		}
	}
	if user == nil {
		t.Fatal("no user turn")
	}
	if len(user.ContextReferences) != 1 || user.ContextReferences[0].ID != opp.ID.String() {
		t.Fatalf("the turn did not keep its subject: %+v", user.ContextReferences)
	}
	// And it must NOT have been filed as a capability selection.
	if len(user.References) != 0 {
		t.Errorf("an entity leaked into the capability selection: %+v", user.References)
	}
}

/* ── 3. what reaches the model ───────────────────────────────────────── */

// The model must receive identity it can act on, and be told not to trust
// the description. Both halves are asserted because either alone fails: id
// without the warning invites a stale answer, warning without id makes the
// tool call impossible.
func TestTheModelReceivesStructuredIdentity(t *testing.T) {
	e := newEnv(t)
	opp := e.seed(e.wsA, "Acme", "Backend Engineer", stagePtr(domain.StageApplied))
	agentID, _ := e.newAgent(e.wsA, "scout")
	e.authorize(e.wsA, agentID, "job_radar.opportunity.get")

	conv := e.newConversationWith(e.wsA, agentID, opportunityRef(opp.ID))

	e.llm.scriptReply("ok")
	e.turn(e.wsA, conv, "em que estágio está essa vaga?")

	sent := e.llm.requests[len(e.llm.requests)-1]
	var system string
	for _, m := range sent.Messages {
		if m.Role == "system" {
			system += m.Content + "\n"
		}
	}

	if !strings.Contains(system, opp.ID.String()) {
		t.Fatalf("the opportunity id never reached the model:\n%s", system)
	}
	if !strings.Contains(system, jrtools.OpportunityReferenceType.String()) {
		t.Errorf("the reference type never reached the model")
	}
	if !strings.Contains(system, "Acme · Backend Engineer") {
		t.Errorf("the label never reached the model")
	}
	// Identity is identity: the block must not restate mutable domain state
	// as though it were part of the item's name. The present stage reaches
	// the model through the reference-state block instead, read this turn —
	// see hydration_integration_test.go.
	identity := systemBlock(sent, "They identify what is being discussed")
	if identity == "" {
		t.Fatalf("the identity block was not sent:\n%s", system)
	}
	for _, stage := range domain.StageNames() {
		if strings.Contains(strings.ToLower(identity), stage) {
			t.Errorf("the identity block carries the mutable stage %q:\n%s", stage, identity)
		}
	}
}

// The subject of the THREAD reaches every turn, not only the first. This is
// what makes "e agora?" work three messages later.
func TestTheThreadSubjectReachesALaterTurn(t *testing.T) {
	e := newEnv(t)
	opp := e.seed(e.wsA, "Acme", "Backend Engineer", stagePtr(domain.StageApplied))
	agentID, _ := e.newAgent(e.wsA, "scout")
	e.authorize(e.wsA, agentID, "job_radar.opportunity.get")
	conv := e.newConversationWith(e.wsA, agentID, opportunityRef(opp.ID))

	e.llm.scriptReply("primeira")
	e.turn(e.wsA, conv, "oi")
	e.llm.streamCalls = 0
	e.llm.scriptReply("segunda")
	// A follow-up that attaches nothing at all.
	e.turn(e.wsA, conv, "e agora?")

	sent := e.llm.requests[len(e.llm.requests)-1]
	var system string
	for _, m := range sent.Messages {
		if m.Role == "system" {
			system += m.Content + "\n"
		}
	}
	if !strings.Contains(system, opp.ID.String()) {
		t.Fatalf("the thread's subject was absent from a later turn:\n%s", system)
	}
}

// The cost has to appear in the account. A block that reached the model
// without being reported would be this feature's first silent charge.
func TestTheReferencesBlockIsReported(t *testing.T) {
	e := newEnv(t)
	opp := e.seed(e.wsA, "Acme", "Backend Engineer", stagePtr(domain.StageSaved))
	agentID, _ := e.newAgent(e.wsA, "scout")
	conv := e.newConversationWith(e.wsA, agentID, opportunityRef(opp.ID))

	e.llm.scriptReply("ok")
	e.turn(e.wsA, conv, "oi")

	msgs, err := e.chatSvc.ListMessages(ctxFor(e.wsA), e.wsA, conv, 50)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var report *chatdomain.ContextReport
	for i := range msgs {
		if msgs[i].ContextReport != nil {
			report = msgs[i].ContextReport
		}
	}
	if report == nil {
		t.Fatal("no context report")
	}
	block, ok := report.Block(chatdomain.BlockContextReferences)
	if !ok {
		t.Fatal("the references block was sent without being reported")
	}
	if block.Items != 1 {
		t.Errorf("block items = %d, want 1", block.Items)
	}
	if block.Characters == 0 {
		t.Error("the block was reported as free")
	}
}

/* ── 4. a reference grants nothing ───────────────────────────────────── */

// The invariant with the sharpest edge: holding a subject must not let an
// agent read what it points at.
func TestAReferenceDoesNotGrantTheTool(t *testing.T) {
	e := newEnv(t)
	opp := e.seed(e.wsA, "Acme", "Backend Engineer", stagePtr(domain.StageApplied))
	// No grants at all.
	agentID, _ := e.newAgent(e.wsA, "ungranted")
	conv := e.newConversationWith(e.wsA, agentID, opportunityRef(opp.ID))

	// The model tries anyway, which is the only way to test the gate.
	e.llm.scriptToolCall("job_radar.opportunity.get",
		`{"opportunity_id":"`+opp.ID.String()+`"}`)
	sink := e.turn(e.wsA, conv, "em que estágio está essa vaga?")

	ev, ok := sink.finished("job_radar.opportunity.get")
	if !ok {
		t.Fatal("no terminal tool event")
	}
	if ev.ErrorCode != string(chatdomain.ToolErrNotAuthorized) {
		t.Fatalf("code = %q, want %s — the reference granted the capability",
			ev.ErrorCode, chatdomain.ToolErrNotAuthorized)
	}

	// The turn still had its subject: the reference survives the refusal.
	// What it must NOT have is the entity's state.
	sent := e.llm.requests[0]
	var system string
	for _, m := range sent.Messages {
		if m.Role == "system" {
			system += m.Content + "\n"
		}
	}
	if !strings.Contains(system, opp.ID.String()) {
		t.Error("the subject disappeared along with the authorization")
	}
	// The one state-ish word the block carries is the frozen subtitle. The
	// notes, salary and description must be nowhere near the context.
	if strings.Contains(system, "Plataforma de pagamentos") {
		t.Error("entity content reached an agent with no grant")
	}
}

// The tools themselves are unchanged by a reference being present: an
// agent with a grant still executes, and one without still does not.
func TestAGrantedAgentStillReadsThroughTheTool(t *testing.T) {
	e := newEnv(t)
	opp := e.seed(e.wsA, "Acme", "Backend Engineer", stagePtr(domain.StageApplied))
	agentID, _ := e.newAgent(e.wsA, "scout")
	e.authorize(e.wsA, agentID, "job_radar.opportunity.get")
	conv := e.newConversationWith(e.wsA, agentID, opportunityRef(opp.ID))

	e.llm.scriptToolCall("job_radar.opportunity.get",
		`{"opportunity_id":"`+opp.ID.String()+`"}`)
	sink := e.turn(e.wsA, conv, "em que estágio está essa vaga?")

	ev, ok := sink.finished("job_radar.opportunity.get")
	if !ok || ev.Status != "ok" {
		t.Fatalf("the granted read did not succeed: %+v", ev)
	}
}

/* ── 5. workspace isolation ──────────────────────────────────────────── */

// A fabricated id naming another workspace's row must be refused at
// ATTACH time, so it never becomes a stored reference at all.
func TestAForeignIDCannotBeAttachedToAConversation(t *testing.T) {
	e := newEnv(t)
	// Belongs to workspace A.
	opp := e.seed(e.wsA, "Acme", "Backend Engineer", stagePtr(domain.StageApplied))
	// The attacker is in workspace B.
	agentID, _ := e.newAgent(e.wsB, "intruder")

	_, err := e.chatSvc.CreateConversation(ctxFor(e.wsB), chatapp.CreateConversationInput{
		WorkspaceID: e.wsB, AgentID: agentID,
		ContextReferences: []chatdomain.ContextReference{opportunityRef(opp.ID)},
	})
	if err == nil {
		t.Fatal("a conversation was created holding another workspace's entity")
	}
	var de *chatdomain.Error
	if !errorsAs(err, &de) || de.Code != chatdomain.CodeContextReferenceNotFound {
		t.Fatalf("error = %v, want %s", err, chatdomain.CodeContextReferenceNotFound)
	}
	// Indistinguishable from an id that never existed: a distinct
	// "forbidden" would confirm the row exists somewhere.
	if strings.Contains(strings.ToLower(de.Message), "acme") ||
		strings.Contains(strings.ToLower(de.Message), "forbidden") {
		t.Errorf("the refusal leaked something about the row: %q", de.Message)
	}
}

func TestAForeignIDCannotBeAttachedToAMessage(t *testing.T) {
	e := newEnv(t)
	opp := e.seed(e.wsA, "Acme", "Backend Engineer", stagePtr(domain.StageApplied))
	agentID, conv := e.newAgent(e.wsB, "intruder")
	e.authorize(e.wsB, agentID, "job_radar.opportunity.get")

	sink := &collectSink{}
	_, err := e.chatSvc.SendMessage(ctxFor(e.wsB), chatapp.SendMessageInput{
		WorkspaceID: e.wsB, ConversationID: conv, Content: "o que acha dessa?",
		ContextReferences: []chatdomain.ContextReference{opportunityRef(opp.ID)},
	}, sink)
	if err == nil {
		t.Fatal("a turn was accepted carrying another workspace's entity")
	}

	// And nothing was written: a refused attachment must not leave a turn.
	msgs, listErr := e.chatSvc.ListMessages(ctxFor(e.wsB), e.wsB, conv, 50)
	if listErr != nil {
		t.Fatalf("list: %v", listErr)
	}
	if len(msgs) != 0 {
		t.Fatalf("the refused turn left %d messages behind", len(msgs))
	}
}

// An unknown type is told apart from a bad id, so a client ahead of the
// server learns which of the two it is.
func TestAnUnknownReferenceTypeIsRefusedAsSuch(t *testing.T) {
	e := newEnv(t)
	agentID, _ := e.newAgent(e.wsA, "scout")

	_, err := e.chatSvc.CreateConversation(ctxFor(e.wsA), chatapp.CreateConversationInput{
		WorkspaceID: e.wsA, AgentID: agentID,
		ContextReferences: []chatdomain.ContextReference{
			{Type: "calendar.event", ID: uuid.New().String()},
		},
	})
	if err == nil {
		t.Fatal("a type with no resolver was accepted")
	}
	var de *chatdomain.Error
	if !errorsAs(err, &de) || de.Code != chatdomain.CodeContextReferenceTypeUnknown {
		t.Fatalf("error = %v, want %s", err, chatdomain.CodeContextReferenceTypeUnknown)
	}
}

func TestAMalformedIDIsRefused(t *testing.T) {
	e := newEnv(t)
	agentID, _ := e.newAgent(e.wsA, "scout")

	_, err := e.chatSvc.CreateConversation(ctxFor(e.wsA), chatapp.CreateConversationInput{
		WorkspaceID: e.wsA, AgentID: agentID,
		ContextReferences: []chatdomain.ContextReference{
			{Type: jrtools.OpportunityReferenceType, ID: "not-a-uuid"},
		},
	})
	if err == nil {
		t.Fatal("a malformed id was accepted")
	}
}

/* ── 6. stale entities ───────────────────────────────────────────────── */

// A conversation outlives its subject. Deleting the opportunity must leave
// the thread readable, with the reference marked rather than removed.
func TestADeletedEntityLeavesTheConversationReadable(t *testing.T) {
	e := newEnv(t)
	opp := e.seed(e.wsA, "Acme", "Backend Engineer", stagePtr(domain.StageApplied))
	agentID, _ := e.newAgent(e.wsA, "scout")
	conv := e.newConversationWith(e.wsA, agentID, opportunityRef(opp.ID))

	if err := e.svc.DeleteOpportunity(ctxFor(e.wsA), e.wsA, opp.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	reread, err := e.chatSvc.GetConversation(ctxFor(e.wsA), e.wsA, conv)
	if err != nil {
		t.Fatalf("the conversation became unreadable: %v", err)
	}
	if len(reread.ContextReferences) != 1 {
		t.Fatalf("the reference was dropped, rewriting the past: %+v", reread.ContextReferences)
	}
	ref := reread.ContextReferences[0]
	if !ref.Unavailable {
		t.Error("the reference was not marked unavailable")
	}
	// The historical label is kept: it is what the chip said at the time.
	if ref.Label != "Acme · Backend Engineer" {
		t.Errorf("the historical label was lost: %q", ref.Label)
	}
}

/* ── 7. freshness ────────────────────────────────────────────────────── */

// The property the whole design turns on: the reference identifies, the
// tool reports. A stage that changes after the reference was attached must
// be what the agent reads.
func TestTheToolReportsTheCurrentStateNotTheAttachedOne(t *testing.T) {
	e := newEnv(t)
	opp := e.seed(e.wsA, "Acme", "Backend Engineer", stagePtr(domain.StageApplied))
	agentID, _ := e.newAgent(e.wsA, "scout")
	e.authorize(e.wsA, agentID, "job_radar.opportunity.get")

	// Attached while the opportunity is `applied`. Nothing about that word
	// is stored — the reference is identity — which is exactly why the tool
	// is the thing that has to report the present.
	conv := e.newConversationWith(e.wsA, agentID, opportunityRef(opp.ID))
	stored, err := e.chatSvc.GetConversation(ctxFor(e.wsA), e.wsA, conv)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	// The domain moves on.
	if _, err := e.svc.MoveOpportunity(ctxFor(e.wsA), e.wsA, opp.ID, domain.StageInterview); err != nil {
		t.Fatalf("move: %v", err)
	}

	// The agent reads through the tool, with the id from the reference.
	e.llm.scriptToolCall("job_radar.opportunity.get",
		`{"opportunity_id":"`+stored.ContextReferences[0].ID+`"}`)
	sink := e.turn(e.wsA, conv, "e agora?")

	ev, ok := sink.finished("job_radar.opportunity.get")
	if !ok || ev.Status != "ok" {
		t.Fatalf("the read failed: %+v", ev)
	}

	// What came back on the wire, which is what the model actually saw.
	records, err := e.chatSvc.ConversationToolCalls(ctxFor(e.wsA), e.wsA, conv)
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	var result string
	for i := range records {
		if records[i].ToolName == "job_radar.opportunity.get" && records[i].Result != nil {
			result = *records[i].Result
		}
	}
	if !strings.Contains(result, `"stage":"interview"`) {
		t.Fatalf("the tool reported a stale stage: %s", result)
	}
	if strings.Contains(result, `"stage":"applied"`) {
		t.Fatalf("the tool reported the attached stage rather than the current one: %s", result)
	}
}
