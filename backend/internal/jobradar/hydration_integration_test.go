//go:build integration

// Reference hydration — the freshness suite.
//
//	Run with: TEST_POSTGRES_DSN=... go test -tags=integration ./internal/jobradar/...
//
// The sentence this suite exists to make true, and which two rounds of
// prompt engineering could not:
//
//	the history says applied · the entity says interview · the turn
//	carries interview, whether or not the model thought to look.
//
// And the three that keep it from being a hole:
//
//  1. hydration is gated on the SAME grant the tool call is gated on, so a
//     reference still grants nothing;
//  2. what is read is never written back, so the reference stays identity;
//  3. an entity from another workspace, a deleted one and a malformed id
//     are one answer, so hydration cannot be used to probe.
//
// It shares the harness of jobradar_integration_test.go.
package jobradar

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	chatapp "github.com/corsi/backend/internal/chat/app"
	chatdomain "github.com/corsi/backend/internal/chat/domain"
	chatports "github.com/corsi/backend/internal/chat/ports"
	"github.com/corsi/backend/internal/jobradar/domain"
	jrtools "github.com/corsi/backend/internal/jobradar/tools"
)

/* ── reading what the turn actually sent ─────────────────────────────── */

// systemBlock returns the ONE system message containing a marker.
//
// Assertions are made against a single block rather than against every
// system message concatenated, because the two reference blocks sit next to
// each other and say deliberately different things. A test that searched the
// whole prompt for "interview" would pass while the word was in the wrong
// block, which is precisely the bug this batch fixes.
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
// are about the whole prompt rather than about one block: "this word must
// appear nowhere at all".
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

/* ── 1. the turn carries the present ─────────────────────────────────── */

// An authorized turn receives the entity's state without the model having
// asked for it.
func TestAnAuthorizedTurnReceivesTheCurrentState(t *testing.T) {
	e := newEnv(t)
	opp := e.seed(e.wsA, "Acme", "Backend Engineer", stagePtr(domain.StageApplied))
	agentID, _ := e.newAgent(e.wsA, "scout")
	e.authorize(e.wsA, agentID, jrtools.OpportunityGetTool.String())
	conv := e.newConversationWith(e.wsA, agentID, opportunityRef(opp.ID))

	e.llm.scriptReply("ok")
	e.turn(e.wsA, conv, "em que estágio está essa vaga?")

	state := stateBlockOf(e.llm.requests[len(e.llm.requests)-1])
	if state == "" {
		t.Fatalf("no reference-state block was sent:\n%s", wireOf(e.llm.requests[0]))
	}
	if !strings.Contains(state, "stage: applied") {
		t.Errorf("the present stage was not in the block:\n%s", state)
	}
	if !strings.Contains(state, opp.ID.String()) {
		t.Errorf("the state block does not say which item it describes:\n%s", state)
	}
	// No tool round was needed for the model to have this. That is the
	// point: freshness stopped depending on the model's discretion.
	if len(e.llm.requests) != 1 {
		t.Errorf("the turn made %d provider calls; hydration should need none",
			len(e.llm.requests))
	}
}

// THE REPRODUCTION. The exact sequence Sprint 2 failed on, in one
// conversation, with the entity moving between the turns.
func TestTheSecondTurnReadsTheCurrentStageAndNotItsOwnEarlierAnswer(t *testing.T) {
	e := newEnv(t)
	opp := e.seed(e.wsA, "Acme", "Backend Engineer", stagePtr(domain.StageApplied))
	agentID, _ := e.newAgent(e.wsA, "scout")
	e.authorize(e.wsA, agentID, jrtools.OpportunityGetTool.String())
	conv := e.newConversationWith(e.wsA, agentID, opportunityRef(opp.ID))

	// Turn 1: the agent answers `applied`, and that sentence is now in the
	// transcript forever. This is the stale evidence the model preferred.
	e.llm.scriptReply("A vaga está no estágio applied.")
	e.turn(e.wsA, conv, "em que estágio está essa vaga?")

	first := stateBlockOf(e.llm.requests[len(e.llm.requests)-1])
	if !strings.Contains(first, "stage: applied") {
		t.Fatalf("setup: turn one did not carry the stage:\n%s", first)
	}

	// The domain moves, outside the conversation entirely.
	if _, err := e.svc.MoveOpportunity(ctxFor(e.wsA), e.wsA, opp.ID, domain.StageInterview); err != nil {
		t.Fatalf("move: %v", err)
	}

	// Turn 2: same conversation, no new attachment, nothing named.
	e.llm.streamCalls = 0
	e.llm.scriptReply("A vaga está no estágio interview.")
	e.turn(e.wsA, conv, "e agora?")

	sent := e.llm.requests[len(e.llm.requests)-1]
	state := stateBlockOf(sent)
	if state == "" {
		t.Fatalf("the second turn carried no state block:\n%s", wireOf(sent))
	}
	if !strings.Contains(state, "stage: interview") {
		t.Fatalf("the second turn carried a stale stage:\n%s", state)
	}
	if strings.Contains(state, "stage: applied") {
		t.Fatalf("the state block asserts a stage the entity has left:\n%s", state)
	}

	// And the transcript was NOT rewritten to get there. The model's own
	// earlier sentence is still in the history, exactly as it was said —
	// which is the honest version of this fix and the harder one: the turn
	// wins on precedence, not by deleting the past.
	wire := wireOf(sent)
	if !strings.Contains(wire, "A vaga está no estágio applied.") {
		t.Error("the earlier answer was removed from the history; transcript fidelity was traded for the test")
	}
	// The block says which of the two is newer, in words, because the model
	// is reading both.
	if !strings.Contains(state, "MORE RECENT") {
		t.Errorf("the state block does not claim precedence over the history:\n%s", state)
	}
}

/* ── 2. hydration is not a bypass ────────────────────────────────────── */

// The invariant with the sharpest edge, restated for hydration: an agent
// without the grant gets NO state, and is told so rather than left to
// invent one.
func TestHydrationIsRefusedWithoutTheGrant(t *testing.T) {
	e := newEnv(t)
	opp := e.seed(e.wsA, "Acme", "Backend Engineer", stagePtr(domain.StageApplied))
	// No grants at all.
	agentID, _ := e.newAgent(e.wsA, "ungranted")
	conv := e.newConversationWith(e.wsA, agentID, opportunityRef(opp.ID))

	e.llm.scriptReply("não consigo verificar")
	e.turn(e.wsA, conv, "em que estágio está essa vaga?")

	sent := e.llm.requests[len(e.llm.requests)-1]
	state := stateBlockOf(sent)
	if state == "" {
		t.Fatalf("the subject vanished along with the authorization:\n%s", wireOf(sent))
	}
	if !strings.Contains(state, "not authorized") {
		t.Errorf("the model was not told why it has no state:\n%s", state)
	}

	// Nothing about the entity's mutable state reached the prompt, through
	// this block or any other.
	wire := strings.ToLower(wireOf(sent))
	for _, stage := range domain.StageNames() {
		if strings.Contains(wire, "stage: "+stage) {
			t.Errorf("an unauthorized turn was handed the stage %q:\n%s", stage, state)
		}
	}
	// The subject stays recognisable. The user named it by attaching it, so
	// its NAME was never the agent's to be granted.
	if !strings.Contains(wireOf(sent), "Acme · Backend Engineer") {
		t.Error("the refusal took the subject's identity with it")
	}
}

// Revoking mid-conversation stops hydration on the very next turn, the same
// guarantee tool execution already gives, because it is the same grant.
func TestRevokingTheCapabilityStopsHydrationOnTheNextTurn(t *testing.T) {
	e := newEnv(t)
	opp := e.seed(e.wsA, "Acme", "Backend Engineer", stagePtr(domain.StageApplied))
	agentID, _ := e.newAgent(e.wsA, "scout")
	e.authorize(e.wsA, agentID, jrtools.OpportunityGetTool.String())
	conv := e.newConversationWith(e.wsA, agentID, opportunityRef(opp.ID))

	e.llm.scriptReply("applied")
	e.turn(e.wsA, conv, "em que estágio está essa vaga?")
	if !strings.Contains(stateBlockOf(e.llm.requests[len(e.llm.requests)-1]), "stage: applied") {
		t.Fatal("setup: the granted turn carried no state")
	}

	if err := e.chatSvc.RevokeTool(ctxFor(e.wsA), e.wsA, agentID, jrtools.OpportunityGetTool); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	e.llm.streamCalls = 0
	e.llm.scriptReply("não consigo verificar")
	e.turn(e.wsA, conv, "e agora?")

	sent := e.llm.requests[len(e.llm.requests)-1]
	state := stateBlockOf(sent)
	if !strings.Contains(state, "not authorized") {
		t.Fatalf("hydration survived the revoke:\n%s", state)
	}
	if strings.Contains(state, "stage: applied") {
		t.Fatalf("a revoked agent was still handed the state:\n%s", state)
	}
}

/* ── 3. hydration is never persisted ─────────────────────────────────── */

// The reference is identity. What hydration reads exists for one turn and
// is discarded; if it were written back the reference would become the
// snapshot the whole design refuses.
func TestHydrationIsNeverPersistedOntoTheReference(t *testing.T) {
	e := newEnv(t)
	opp := e.seed(e.wsA, "Acme", "Backend Engineer", stagePtr(domain.StageApplied))
	agentID, _ := e.newAgent(e.wsA, "scout")
	e.authorize(e.wsA, agentID, jrtools.OpportunityGetTool.String())
	conv := e.newConversationWith(e.wsA, agentID, opportunityRef(opp.ID))

	e.llm.scriptReply("ok")
	e.turn(e.wsA, conv, "em que estágio está essa vaga?")
	if _, err := e.svc.MoveOpportunity(ctxFor(e.wsA), e.wsA, opp.ID, domain.StageInterview); err != nil {
		t.Fatalf("move: %v", err)
	}
	e.llm.streamCalls = 0
	e.llm.scriptReply("ok")
	e.turn(e.wsA, conv, "e agora?")

	// Read the column, not the service: this is an assertion about what is
	// on disk, and a service that recomputed something for display would
	// hide the very thing being checked.
	var raw string
	err := e.pool.QueryRow(ctxFor(e.wsA),
		`SELECT coalesce(context_references::text, '') FROM chat.conversations WHERE id = $1`,
		conv).Scan(&raw)
	if err != nil {
		t.Fatalf("read the stored column: %v", err)
	}
	if !strings.Contains(raw, opp.ID.String()) {
		t.Fatalf("the stored reference lost its identity: %s", raw)
	}
	for _, stage := range domain.StageNames() {
		if strings.Contains(strings.ToLower(raw), stage) {
			t.Errorf("hydrated state was written back into the reference (%q): %s", stage, raw)
		}
	}
}

/* ── 4. the account ──────────────────────────────────────────────────── */

// Hydrated context is paid for on every turn, so it has to appear in the
// account. A recurring charge invisible to the Inspector would be worse
// than a one-off one.
func TestHydrationIsReportedInTheContextAccount(t *testing.T) {
	e := newEnv(t)
	opp := e.seed(e.wsA, "Acme", "Backend Engineer", stagePtr(domain.StageApplied))
	agentID, _ := e.newAgent(e.wsA, "scout")
	e.authorize(e.wsA, agentID, jrtools.OpportunityGetTool.String())
	conv := e.newConversationWith(e.wsA, agentID, opportunityRef(opp.ID))

	e.llm.scriptReply("ok")
	e.turn(e.wsA, conv, "oi")

	block, ok := reportBlockOf(t, e, conv, chatdomain.BlockReferenceState)
	if !ok {
		t.Fatal("the state block reached the model without being reported")
	}
	if block.Items != 1 {
		t.Errorf("items = %d, want 1", block.Items)
	}
	if block.Characters == 0 {
		t.Error("the block was reported as free")
	}
	if len(block.Exclusions) != 0 {
		t.Errorf("an authorized read reported exclusions: %+v", block.Exclusions)
	}
}

// A refused read is reported as a refusal, told apart from an outage,
// because they call for different actions from whoever reads the report.
func TestARefusedHydrationIsReportedAsUnauthorized(t *testing.T) {
	e := newEnv(t)
	opp := e.seed(e.wsA, "Acme", "Backend Engineer", stagePtr(domain.StageApplied))
	agentID, _ := e.newAgent(e.wsA, "ungranted")
	conv := e.newConversationWith(e.wsA, agentID, opportunityRef(opp.ID))

	e.llm.scriptReply("ok")
	e.turn(e.wsA, conv, "oi")

	block, ok := reportBlockOf(t, e, conv, chatdomain.BlockReferenceState)
	if !ok {
		t.Fatal("no reference-state block in the report")
	}
	if block.Items != 0 {
		t.Errorf("items = %d; a refused subject carried no state", block.Items)
	}
	if len(block.Exclusions) != 1 ||
		block.Exclusions[0].Reason != chatdomain.ReasonUnauthorized ||
		block.Exclusions[0].Items != 1 {
		t.Fatalf("exclusions = %+v, want one %q for one item",
			block.Exclusions, chatdomain.ReasonUnauthorized)
	}
	// The sentence telling the model it has no state is still charged: it
	// went on the wire like everything else.
	if block.Characters == 0 {
		t.Error("the refusal sentence was reported as free")
	}
}

// reportBlockOf reads one block out of the last assistant turn's stored
// report.
func reportBlockOf(t *testing.T, e *env, conv uuid.UUID, kind chatdomain.BlockKind) (chatdomain.ContextBlock, bool) {
	t.Helper()
	msgs, err := e.chatSvc.ListMessages(ctxFor(e.wsA), e.wsA, conv, 50)
	if err != nil {
		t.Fatalf("list messages: %v", err)
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
	return report.Block(kind)
}

/* ── 5. entities that are gone, foreign, or not entities ─────────────── */

// A deleted entity must not fall back on anything said about it earlier.
func TestHydrationOfADeletedEntityCarriesNoState(t *testing.T) {
	e := newEnv(t)
	opp := e.seed(e.wsA, "Acme", "Backend Engineer", stagePtr(domain.StageApplied))
	agentID, _ := e.newAgent(e.wsA, "scout")
	e.authorize(e.wsA, agentID, jrtools.OpportunityGetTool.String())
	conv := e.newConversationWith(e.wsA, agentID, opportunityRef(opp.ID))

	if err := e.svc.DeleteOpportunity(ctxFor(e.wsA), e.wsA, opp.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	e.llm.scriptReply("ok")
	e.turn(e.wsA, conv, "e agora?")

	sent := e.llm.requests[len(e.llm.requests)-1]
	state := stateBlockOf(sent)
	if !strings.Contains(state, "could not be found") {
		t.Fatalf("a deleted entity did not report as missing:\n%s", state)
	}
	if strings.Contains(strings.ToLower(wireOf(sent)), "stage: applied") {
		t.Error("the old state was carried for an entity that no longer exists")
	}

	block, ok := reportBlockOf(t, e, conv, chatdomain.BlockReferenceState)
	if !ok || len(block.Exclusions) != 1 ||
		block.Exclusions[0].Reason != chatdomain.ReasonUnavailable {
		t.Errorf("exclusions = %+v, want one %q", block.Exclusions, chatdomain.ReasonUnavailable)
	}
}

// hydratorUnderTest is the provider seam, reached directly.
//
// The three inputs below never survive admission, so a turn cannot be used
// to test them: a foreign id, a deleted id and a malformed one are all
// refused before a conversation exists. Asking the provider itself is the
// only way to assert that the floor holds even if something above it one
// day stops holding.
func hydratorUnderTest(t *testing.T, e *env) chatports.ContextReferenceHydrator {
	t.Helper()
	h, ok := jrtools.NewReferenceResolver(e.svc).(chatports.ContextReferenceHydrator)
	if !ok {
		t.Fatal("the Job Radar resolver does not hydrate")
	}
	return h
}

// Workspace isolation, at the hydration door specifically. An id is
// untrusted input at every read, not only at the one that admitted it.
func TestAForeignEntityCannotBeHydrated(t *testing.T) {
	e := newEnv(t)
	opp := e.seed(e.wsA, "Acme", "Backend Engineer", stagePtr(domain.StageApplied))

	state, found, err := hydratorUnderTest(t, e).Hydrate(
		ctxFor(e.wsB), e.wsB, opportunityRef(opp.ID))
	if err != nil {
		t.Fatalf("a foreign id produced an error rather than an answer: %v", err)
	}
	if found {
		t.Fatalf("another workspace's entity was hydrated: %+v", state.Fields)
	}
	if len(state.Fields) != 0 {
		t.Errorf("fields leaked on a not-found: %+v", state.Fields)
	}
}

// Not-found and malformed answer the same way, so the difference cannot be
// used to probe for rows.
func TestAMalformedAndAMissingIDHydrateAlike(t *testing.T) {
	e := newEnv(t)
	h := hydratorUnderTest(t, e)

	malformed := chatdomain.ContextReference{
		Type: jrtools.OpportunityReferenceType, ID: "not-a-uuid",
	}
	if _, found, err := h.Hydrate(ctxFor(e.wsA), e.wsA, malformed); found || err != nil {
		t.Errorf("malformed id: found=%v err=%v, want false/nil", found, err)
	}

	missing := opportunityRef(uuid.New())
	if _, found, err := h.Hydrate(ctxFor(e.wsA), e.wsA, missing); found || err != nil {
		t.Errorf("missing id: found=%v err=%v, want false/nil", found, err)
	}
}

// A type this provider does not own is not this provider's to answer for,
// even when it is asked directly.
func TestAForeignTypeIsNotHydratedByJobRadar(t *testing.T) {
	e := newEnv(t)
	h := hydratorUnderTest(t, e)

	if _, declared := h.HydrationPolicy("calendar.event"); declared {
		t.Error("Job Radar declared a policy for a type it does not own")
	}
	other := chatdomain.ContextReference{Type: "calendar.event", ID: uuid.New().String()}
	if _, found, err := h.Hydrate(ctxFor(e.wsA), e.wsA, other); found || err != nil {
		t.Errorf("found=%v err=%v, want false/nil", found, err)
	}
}

/* ── 6. the policy is declared, not hardcoded ────────────────────────── */

// The policy names a REAL tool. If it named anything else, hydration would
// be gated on a grant no operator could give and no page could show, which
// is the same as being gated on nothing an operator can see.
func TestTheDeclaredCapabilityIsARealGrantableTool(t *testing.T) {
	e := newEnv(t)
	policy, declared := hydratorUnderTest(t, e).HydrationPolicy(jrtools.OpportunityReferenceType)
	if !declared {
		t.Fatal("the opportunity type declares no freshness policy")
	}
	if policy.Freshness != chatports.FreshnessPerTurn {
		t.Errorf("freshness = %q, want %q", policy.Freshness, chatports.FreshnessPerTurn)
	}
	if _, ok := e.registry.Lookup(policy.Capability); !ok {
		t.Fatalf("the policy names %q, which is not a tool in this build", policy.Capability)
	}
	// And it is the read tool, not the write one. Hydration must never be
	// able to authorize itself through something that changes data.
	def, _ := e.registry.Lookup(policy.Capability)
	if def.Definition().Effect != chatdomain.EffectRead {
		t.Errorf("hydration is gated on a %q tool", def.Definition().Effect)
	}
}

/* ── 7. the other reference concept is untouched ─────────────────────── */

// Regression. TurnReference is the capability selector behind `@`, and it
// must keep working exactly as it did — a batch about entity state has no
// business changing what a turn may do.
func TestTheCapabilitySelectorStillNarrowsATurn(t *testing.T) {
	e := newEnv(t)
	opp := e.seed(e.wsA, "Acme", "Backend Engineer", stagePtr(domain.StageApplied))
	agentID, _ := e.newAgent(e.wsA, "scout")
	e.authorize(e.wsA, agentID,
		jrtools.OpportunityGetTool.String(), "job_radar.opportunity.list")
	conv := e.newConversationWith(e.wsA, agentID, opportunityRef(opp.ID))

	e.llm.scriptReply("ok")
	sink := &collectSink{}
	if _, err := e.chatSvc.SendMessage(ctxFor(e.wsA), chatapp.SendMessageInput{
		WorkspaceID: e.wsA, ConversationID: conv, Content: "e agora?",
		// The `@` path: a capability, by name, narrowing this turn.
		References: []chatdomain.TurnReference{{
			Kind: chatdomain.ReferenceKindTool,
			ID:   jrtools.OpportunityGetTool.String(),
		}},
	}, sink); err != nil {
		t.Fatalf("send: %v", err)
	}

	sent := e.llm.requests[len(e.llm.requests)-1]
	if len(sent.Tools) != 1 || sent.Tools[0].Name != jrtools.OpportunityGetTool {
		t.Fatalf("the selection did not narrow the turn: %+v", sent.Tools)
	}
	// The entity reference is untouched by the capability selection: it is
	// a different column, a different concept, and still present.
	if stateBlockOf(sent) == "" {
		t.Error("narrowing the capabilities dropped the conversation's subject")
	}
}

// A turn narrowed AWAY from the hydration capability does not hydrate
// through the back door: hydration is never more than what the turn itself
// could do.
func TestATurnNarrowedAwayFromTheCapabilityDoesNotHydrate(t *testing.T) {
	e := newEnv(t)
	opp := e.seed(e.wsA, "Acme", "Backend Engineer", stagePtr(domain.StageApplied))
	agentID, _ := e.newAgent(e.wsA, "scout")
	e.authorize(e.wsA, agentID,
		jrtools.OpportunityGetTool.String(), "job_radar.opportunity.list")
	conv := e.newConversationWith(e.wsA, agentID, opportunityRef(opp.ID))

	e.llm.scriptReply("ok")
	sink := &collectSink{}
	if _, err := e.chatSvc.SendMessage(ctxFor(e.wsA), chatapp.SendMessageInput{
		WorkspaceID: e.wsA, ConversationID: conv, Content: "e agora?",
		References: []chatdomain.TurnReference{{
			Kind: chatdomain.ReferenceKindTool,
			ID:   "job_radar.opportunity.list",
		}},
	}, sink); err != nil {
		t.Fatalf("send: %v", err)
	}

	state := stateBlockOf(e.llm.requests[len(e.llm.requests)-1])
	if !strings.Contains(state, "not authorized") {
		t.Fatalf("a turn scoped away from the read capability still hydrated:\n%s", state)
	}
}
