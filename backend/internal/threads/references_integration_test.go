//go:build integration

// Threads as a Context Reference subject, and its hydration.
//
//	Run with: TEST_POSTGRES_DSN=... go test -tags=integration ./internal/threads/...
//
// This is the part of the sprint that had to be justified rather than
// assumed. The question was whether Threads could become a reference
// provider as a SMALL, NATURAL extension of what already exists, provable
// without any new UI. It could: the module implements two interfaces Agents
// already declares, the composition root gained one line, and everything
// below is exercised through the same public API a screen would use.
//
// The sentences this suite has to make true:
//
//  1. a conversation can be ABOUT a thread, and the backend authors what
//     that thread is called;
//  2. the reference grants NOTHING — a resolver is not a capability;
//  3. the present text and status reach the turn without the model
//     remembering to look, which matters more here than anywhere else,
//     because content is rewritten several times inside one conversation;
//  4. hydration is gated on the SAME grant the get tool is gated on;
//  5. a thread from another workspace, a deleted one and a malformed id are
//     one answer, so hydration cannot be used to probe.
//
// It shares the harness of threads_integration_test.go.
package threads

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	chatapp "github.com/corsi/backend/internal/chat/app"
	chatdomain "github.com/corsi/backend/internal/chat/domain"
	chatports "github.com/corsi/backend/internal/chat/ports"
	"github.com/corsi/backend/internal/threads/domain"
	thtools "github.com/corsi/backend/internal/threads/tools"
)

// threadRef is what a client sends: a type and an id, and nothing it could
// have authored itself.
func threadRef(id uuid.UUID) chatdomain.ContextReference {
	return chatdomain.ContextReference{Type: thtools.ThreadReferenceType, ID: id.String()}
}

/* ── reading what the turn actually sent ─────────────────────────────── */

func systemBlock(req chatports.CompletionRequest, marker string) string {
	for _, m := range req.Messages {
		if m.Role == "system" && strings.Contains(m.Content, marker) {
			return m.Content
		}
	}
	return ""
}

// stateBlockOf is the reference-state block of one request, by its header.
func stateBlockOf(req chatports.CompletionRequest) string {
	return systemBlock(req, "Current state of the attached items")
}

// wireOf flattens everything the model received, for the assertions that
// are about the whole prompt rather than one block: "this word appears
// nowhere at all".
func wireOf(req chatports.CompletionRequest) string {
	var b strings.Builder
	for _, m := range req.Messages {
		b.WriteString(m.Role)
		b.WriteString(": ")
		b.WriteString(m.Content)
		b.WriteString("\n")
	}
	return b.String()
}

func lastRequest(e *env) chatports.CompletionRequest {
	return e.llm.requests[len(e.llm.requests)-1]
}

/* ── 1. registration ─────────────────────────────────────────────────── */

// The type is well formed under the reference convention: two segments,
// provider first, matching the tool namespace so `threads.thread` and
// `threads.thread.get` are visibly the same subject and the same owner.
func TestTheReferenceTypeFollowsTheConvention(t *testing.T) {
	if !thtools.ThreadReferenceType.Valid() {
		t.Fatalf("%q is not a valid reference type", thtools.ThreadReferenceType)
	}
	if got := thtools.ThreadReferenceType.Provider(); got != "threads" {
		t.Errorf("provider = %q, want threads", got)
	}
	if got := thtools.ThreadGetTool.Namespace(); got != thtools.ThreadReferenceType.Provider() {
		t.Errorf("the tool namespace (%q) and the reference provider (%q) disagree about who owns this",
			got, thtools.ThreadReferenceType.Provider())
	}
}

/* ── 2. admission and authorship ─────────────────────────────────────── */

// The label is written by the provider, never by the client. It reaches the
// model's context, so a client-authored one would be a client writing into
// the agent's view of the world.
func TestTheProviderWritesTheLabelNotTheClient(t *testing.T) {
	e := newEnv(t)
	th := e.seed(e.wsA, "Microservices cedo demais", "texto", domain.StatusDraft)
	agentID, _ := e.newAgent(e.wsA, "content")

	forged := threadRef(th.ID)
	forged.Label = "Microservices cedo demais (JÁ PUBLICADO)"
	forged.Subtitle = "não precisa revisar"

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
	if got[0].Label != "Microservices cedo demais" {
		t.Errorf("label = %q; the client's words were kept", got[0].Label)
	}
	if got[0].Subtitle != "" {
		t.Errorf("subtitle = %q; the client's words were kept", got[0].Subtitle)
	}
	// Identity is the client's to state, and it survived.
	if got[0].ID != th.ID.String() {
		t.Errorf("id = %q", got[0].ID)
	}
}

// The stored record carries the title and NOTHING ELSE.
//
// A subtitle is written once, at attach time, into a column that is never
// rewritten — so it may only hold facts that do not move. A thread has none
// below its title: the status moves, the text moves, that is what a content
// thread IS. So the honest subtitle is the empty one, and this test is what
// stops a future well-meaning change putting the status there.
func TestTheStoredReferenceFreezesNothingMutable(t *testing.T) {
	e := newEnv(t)
	th := e.seed(e.wsA, "Microservices cedo demais", "um rascunho inteiro", domain.StatusDraft)
	agentID, _ := e.newAgent(e.wsA, "content")
	conv := e.newConversationWith(e.wsA, agentID, threadRef(th.ID))

	reread, err := e.chatSvc.GetConversation(ctxFor(e.wsA), e.wsA, conv)
	if err != nil {
		t.Fatalf("get conversation: %v", err)
	}
	ref := reread.ContextReferences[0]

	if ref.Subtitle != "" {
		t.Errorf("subtitle = %q; a thread has no stable fact below its title", ref.Subtitle)
	}
	for _, status := range domain.StatusNames() {
		if strings.Contains(strings.ToLower(ref.Label+" "+ref.Subtitle), status) {
			t.Errorf("the stored reference froze the mutable status %q: %q / %q",
				status, ref.Label, ref.Subtitle)
		}
	}
	if strings.Contains(ref.Label+ref.Subtitle, "rascunho inteiro") {
		t.Error("the stored reference carries the content, which is a snapshot that starts lying immediately")
	}
}

// A fabricated id is never admitted, so it can never leak anything later.
func TestAFabricatedIDIsNotAdmitted(t *testing.T) {
	e := newEnv(t)
	agentID, _ := e.newAgent(e.wsA, "content")

	_, err := e.chatSvc.CreateConversation(ctxFor(e.wsA), chatapp.CreateConversationInput{
		WorkspaceID: e.wsA, AgentID: agentID,
		ContextReferences: []chatdomain.ContextReference{threadRef(uuid.New())},
	})
	if err == nil {
		t.Fatal("a reference to a thread that does not exist was admitted")
	}
}

// Another workspace's thread is refused exactly as a fabricated one is: the
// difference would confirm that the row exists somewhere.
func TestAnotherWorkspacesThreadCannotBeAttached(t *testing.T) {
	e := newEnv(t)
	mine := e.seed(e.wsA, "Conteúdo do A", "texto", domain.StatusDraft)
	agentID, _ := e.newAgent(e.wsB, "content")

	_, crossErr := e.chatSvc.CreateConversation(ctxFor(e.wsB), chatapp.CreateConversationInput{
		WorkspaceID: e.wsB, AgentID: agentID,
		ContextReferences: []chatdomain.ContextReference{threadRef(mine.ID)},
	})
	if crossErr == nil {
		t.Fatal("wsB attached wsA's thread")
	}
	_, fakeErr := e.chatSvc.CreateConversation(ctxFor(e.wsB), chatapp.CreateConversationInput{
		WorkspaceID: e.wsB, AgentID: agentID,
		ContextReferences: []chatdomain.ContextReference{threadRef(uuid.New())},
	})
	if fakeErr == nil {
		t.Fatal("a fabricated id was admitted")
	}

	var a, b *chatdomain.Error
	if errorsAs(crossErr, &a) && errorsAs(fakeErr, &b) && a.Code != b.Code {
		t.Errorf("a cross-workspace reference (%q) is distinguishable from a fabricated one (%q)",
			a.Code, b.Code)
	}
}

/* ── 3. a reference is not a capability ──────────────────────────────── */

// THE INVARIANT. An agent holding a reference and no grant knows the
// subject's TITLE — which the user just told it by attaching it — and
// cannot read a word of the draft.
func TestAReferenceGrantsNothing(t *testing.T) {
	e := newEnv(t)
	th := e.seed(e.wsA, "Microservices cedo demais", "SEGREDO: o corpo do rascunho", domain.StatusReview)
	agentID, _ := e.newAgent(e.wsA, "content")
	conv := e.newConversationWith(e.wsA, agentID, threadRef(th.ID))

	e.llm.scriptReply("ok")
	e.turn(e.wsA, conv, "sobre o que estamos falando?")

	wire := wireOf(lastRequest(e))
	// The name is there: the user attached it.
	if !strings.Contains(wire, "Microservices cedo demais") {
		t.Errorf("the subject's title did not reach the turn:\n%s", wire)
	}
	// The state is not.
	if strings.Contains(wire, "SEGREDO") {
		t.Errorf("an unauthorized agent received the content:\n%s", wire)
	}
	if strings.Contains(stateBlockOf(lastRequest(e)), "review") {
		t.Error("an unauthorized agent received the status")
	}

	// And the tool itself still refuses, which is the same answer arriving
	// through the other door.
	e.llm.scriptToolCall(thtools.ThreadGetTool, `{"thread_id":"`+th.ID.String()+`"}`)
	ev, _ := e.turn(e.wsA, conv, "lê o texto").finished(thtools.ThreadGetTool)
	if ev.ErrorCode != string(chatdomain.ToolErrNotAuthorized) {
		t.Errorf("error code = %q, want %q", ev.ErrorCode, chatdomain.ToolErrNotAuthorized)
	}
}

/* ── 4. hydration ────────────────────────────────────────────────────── */

// An authorized turn receives the thread's present state without the model
// having asked for it.
func TestAnAuthorizedTurnReceivesTheCurrentState(t *testing.T) {
	e := newEnv(t)
	th := e.seed(e.wsA, "Microservices cedo demais", "o rascunho atual", domain.StatusDraft)
	agentID, _ := e.newAgent(e.wsA, "content")
	e.authorize(e.wsA, agentID, thtools.ThreadGetTool)
	conv := e.newConversationWith(e.wsA, agentID, threadRef(th.ID))

	e.llm.scriptReply("ok")
	e.turn(e.wsA, conv, "em que pé está esse conteúdo?")

	state := stateBlockOf(lastRequest(e))
	if state == "" {
		t.Fatalf("no reference-state block was sent:\n%s", wireOf(lastRequest(e)))
	}
	for _, want := range []string{"status: draft", "o rascunho atual", th.ID.String()} {
		if !strings.Contains(state, want) {
			t.Errorf("the state block is missing %q:\n%s", want, state)
		}
	}
	// No tool round was needed. That is the point: freshness stopped
	// depending on the model's discretion.
	if len(e.llm.requests) != 1 {
		t.Errorf("the turn made %d provider calls; hydration should need none", len(e.llm.requests))
	}
}

// THE REPRODUCTION, in the shape that matters most for content: draft A is
// in the transcript in full, draft B is in the database, and the turn has
// to carry B.
//
// This failure is worse here than anywhere else in the system. A model that
// edits from its own earlier message does not merely answer with a stale
// word — it hands back a rewrite of the OLD text, silently discarding every
// change since, and the user reads a plausible piece with their last three
// edits gone.
func TestTheNextTurnReadsTheCurrentDraftAndNotItsOwnEarlierOne(t *testing.T) {
	e := newEnv(t)
	th := e.seed(e.wsA, "Microservices cedo demais", "DRAFT_A: o texto original", domain.StatusDraft)
	agentID, _ := e.newAgent(e.wsA, "content")
	e.authorize(e.wsA, agentID, thtools.ThreadGetTool, thtools.ThreadUpdateTool)
	conv := e.newConversationWith(e.wsA, agentID, threadRef(th.ID))

	// Turn 1: the agent quotes draft A, and that text is now in the
	// transcript forever. This is the stale evidence a model prefers.
	e.llm.scriptReply("O texto atual é: DRAFT_A: o texto original")
	e.turn(e.wsA, conv, "me mostra o texto")

	if first := stateBlockOf(lastRequest(e)); !strings.Contains(first, "DRAFT_A") {
		t.Fatalf("turn 1 did not carry the draft:\n%s", first)
	}

	// The thread moves — here through the tool, exactly as a real
	// conversation would move it.
	e.execute(t, e.wsA, thtools.ThreadUpdateTool, map[string]any{
		"thread_id": th.ID.String(), "content": "DRAFT_B: reescrito", "status": "review",
	})

	// Turn 2. The history still says DRAFT_A; the block must say DRAFT_B.
	e.llm.scriptReply("ok")
	e.turn(e.wsA, conv, "e agora?")

	state := stateBlockOf(lastRequest(e))
	if !strings.Contains(state, "DRAFT_B") {
		t.Errorf("the second turn did not carry the current draft:\n%s", state)
	}
	if strings.Contains(state, "DRAFT_A") {
		t.Errorf("the state block still carries the superseded draft:\n%s", state)
	}
	if !strings.Contains(state, "status: review") {
		t.Errorf("the state block did not carry the current status:\n%s", state)
	}
	// The old text is still in the CONVERSATION — nothing rewrites history —
	// and that is precisely why the block has to exist.
	if !strings.Contains(wireOf(lastRequest(e)), "DRAFT_A") {
		t.Error("the transcript was rewritten; hydration must add, never edit the past")
	}
	// Zero tool calls in the whole conversation.
	if len(e.llm.requests) != 1 {
		t.Errorf("turn 2 made %d provider calls", len(e.llm.requests))
	}
}

// Hydration is gated on the SAME grant the get tool is gated on. Revoke it
// and the freshness stops on the next turn, for the same reason the tool
// call stops.
func TestRevokingTheGetGrantStopsHydration(t *testing.T) {
	e := newEnv(t)
	th := e.seed(e.wsA, "Microservices cedo demais", "MARCADOR_DE_CONTEUDO", domain.StatusDraft)
	agentID, _ := e.newAgent(e.wsA, "content")
	e.authorize(e.wsA, agentID, thtools.ThreadGetTool)
	conv := e.newConversationWith(e.wsA, agentID, threadRef(th.ID))

	e.llm.scriptReply("ok")
	e.turn(e.wsA, conv, "primeiro turno")
	if !strings.Contains(stateBlockOf(lastRequest(e)), "MARCADOR_DE_CONTEUDO") {
		t.Fatal("the authorized turn did not hydrate")
	}

	if err := e.chatSvc.RevokeTool(ctxFor(e.wsA), e.wsA, agentID, thtools.ThreadGetTool); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	e.llm.scriptReply("ok")
	e.turn(e.wsA, conv, "segundo turno")
	if strings.Contains(wireOf(lastRequest(e)), "MARCADOR_DE_CONTEUDO") {
		t.Error("hydration survived the revoke; it is a way around authorization")
	}
	// The reference itself is untouched: the agent still knows what the
	// conversation is about, and still cannot read it.
	if !strings.Contains(wireOf(lastRequest(e)), "Microservices cedo demais") {
		t.Error("revoking a tool grant removed the subject from the conversation")
	}
}

// A grant on some OTHER threads capability does not authorize hydration.
// The policy names one tool, and it is the one whose read this is.
func TestAnUnrelatedGrantDoesNotAuthorizeHydration(t *testing.T) {
	e := newEnv(t)
	th := e.seed(e.wsA, "Microservices cedo demais", "MARCADOR_DE_CONTEUDO", domain.StatusDraft)
	agentID, _ := e.newAgent(e.wsA, "content")
	// Everything except get.
	e.authorize(e.wsA, agentID, thtools.ThreadListTool, thtools.ThreadCreateTool,
		thtools.ThreadUpdateTool, thtools.ThreadDeleteTool)
	conv := e.newConversationWith(e.wsA, agentID, threadRef(th.ID))

	e.llm.scriptReply("ok")
	e.turn(e.wsA, conv, "e aí?")
	if strings.Contains(wireOf(lastRequest(e)), "MARCADOR_DE_CONTEUDO") {
		t.Error("a grant on another capability hydrated the content")
	}
}

// What is read is never written back. The stored reference is identity, and
// a hydration that persisted would recreate the frozen-snapshot bug in a
// new place.
func TestHydrationIsNeverPersisted(t *testing.T) {
	e := newEnv(t)
	th := e.seed(e.wsA, "Microservices cedo demais", "MARCADOR_DE_CONTEUDO", domain.StatusDraft)
	agentID, _ := e.newAgent(e.wsA, "content")
	e.authorize(e.wsA, agentID, thtools.ThreadGetTool)
	conv := e.newConversationWith(e.wsA, agentID, threadRef(th.ID))

	e.llm.scriptReply("ok")
	e.turn(e.wsA, conv, "um")
	e.llm.scriptReply("ok")
	e.turn(e.wsA, conv, "dois")

	// Read the column directly: whatever the application layer would do on
	// the way out cannot hide a write that happened.
	var raw string
	if err := e.pool.QueryRow(ctxFor(e.wsA),
		`SELECT coalesce(context_references::text, '') FROM chat.conversations WHERE id = $1`, conv,
	).Scan(&raw); err != nil {
		t.Fatalf("read the column: %v", err)
	}
	if strings.Contains(raw, "MARCADOR_DE_CONTEUDO") {
		t.Errorf("hydrated content was written into the stored reference: %s", raw)
	}
	for _, status := range domain.StatusNames() {
		if strings.Contains(strings.ToLower(raw), `"`+status+`"`) {
			t.Errorf("a hydrated status was persisted: %s", raw)
		}
	}
	if !strings.Contains(raw, th.ID.String()) {
		t.Errorf("the identity was lost: %s", raw)
	}
}

// A thread with no text says so rather than being silent about it. A model
// reading an absent field has to guess whether the thread is blank or
// whether hydration failed — and that is the difference between writing the
// first draft and rewriting one it cannot see.
func TestAnEmptyThreadHydratesAsExplicitlyEmpty(t *testing.T) {
	e := newEnv(t)
	th := e.seed(e.wsA, "Uma ideia sem texto", "", domain.StatusIdea)
	agentID, _ := e.newAgent(e.wsA, "content")
	e.authorize(e.wsA, agentID, thtools.ThreadGetTool)
	conv := e.newConversationWith(e.wsA, agentID, threadRef(th.ID))

	e.llm.scriptReply("ok")
	e.turn(e.wsA, conv, "e aí?")

	state := stateBlockOf(lastRequest(e))
	if !strings.Contains(state, "empty") {
		t.Errorf("an empty thread did not declare itself:\n%s", state)
	}
}

// A thread too large to carry every turn is DESCRIBED, never truncated into
// the prompt. A truncated draft is worse than no draft: the model would
// rewrite from it and hand back a shortened piece believing it was
// complete.
func TestAnOversizedThreadIsDescribedRatherThanTruncated(t *testing.T) {
	e := newEnv(t)
	long := strings.Repeat("palavra ", 1200) // ~9600 characters
	th := e.seed(e.wsA, "Ensaio longo", long, domain.StatusDraft)
	agentID, _ := e.newAgent(e.wsA, "content")
	e.authorize(e.wsA, agentID, thtools.ThreadGetTool)
	conv := e.newConversationWith(e.wsA, agentID, threadRef(th.ID))

	e.llm.scriptReply("ok")
	e.turn(e.wsA, conv, "e aí?")

	state := stateBlockOf(lastRequest(e))
	if strings.Contains(state, long[:2000]) {
		t.Error("an oversized draft was carried into the prompt")
	}
	if !strings.Contains(state, "threads.thread.get") {
		t.Errorf("the state block does not say how to read the full text:\n%s", state)
	}
	// The status is still fresh — the size of the text does not cost the
	// caller the one-word fact.
	if !strings.Contains(state, "status: draft") {
		t.Errorf("the status was lost with the content:\n%s", state)
	}
}

/* ── 5. stale subjects ───────────────────────────────────────────────── */

// A conversation outlives its subject. Deleting the thread must leave the
// conversation readable, with the reference marked rather than removed: a
// conversation that worked on a piece for a week does not stop having
// worked on it when the row is soft-deleted.
func TestADeletedThreadLeavesTheConversationReadable(t *testing.T) {
	e := newEnv(t)
	th := e.seed(e.wsA, "Microservices cedo demais", "texto", domain.StatusDraft)
	agentID, _ := e.newAgent(e.wsA, "content")
	conv := e.newConversationWith(e.wsA, agentID, threadRef(th.ID))

	if err := e.svc.DeleteThread(ctxFor(e.wsA), e.wsA, th.ID); err != nil {
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
	if ref.Label != "Microservices cedo demais" {
		t.Errorf("the historical label was lost: %q", ref.Label)
	}
}

// And a deleted subject hydrates nothing, without breaking the turn.
func TestADeletedThreadHydratesNothingAndTheTurnStillRuns(t *testing.T) {
	e := newEnv(t)
	th := e.seed(e.wsA, "Microservices cedo demais", "MARCADOR_DE_CONTEUDO", domain.StatusDraft)
	agentID, _ := e.newAgent(e.wsA, "content")
	e.authorize(e.wsA, agentID, thtools.ThreadGetTool)
	conv := e.newConversationWith(e.wsA, agentID, threadRef(th.ID))

	if err := e.svc.DeleteThread(ctxFor(e.wsA), e.wsA, th.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	e.llm.scriptReply("ok")
	sink := e.turn(e.wsA, conv, "e aí?")
	if sink.text.String() == "" {
		t.Error("the turn produced nothing after its subject was removed")
	}
	if strings.Contains(wireOf(lastRequest(e)), "MARCADOR_DE_CONTEUDO") {
		t.Error("a deleted thread was still hydrated")
	}
}

// errorsAs is errors.As, named here so this file reads the same as the rest
// of the module.
func errorsAs(err error, target **chatdomain.Error) bool {
	for err != nil {
		if de, ok := err.(*chatdomain.Error); ok {
			*target = de
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
