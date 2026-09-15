//go:build integration

// Safe resume — the first User Beta incident, reproduced and closed.
//
// ══════════════════════════════════════════════════════════════════════
//
//	CONTINUE THE PENDING WORK; NEVER REPEAT AN EXECUTED WRITE
//
// ══════════════════════════════════════════════════════════════════════
//
// ── What actually happened ─────────────────────────────────────────────
// The user asked for a list of gifts and said to be creative with the Room.
// One turn ran:
//
//	round 1  palace.room.list          ok      (a READ)
//	round 2  palace.room.create        ok
//	round 3  palace.artifact.create    ok
//	round 4  palace.artifact.item.add  x2      NOT_EXECUTED · tool_round_limit
//
// The user typed "Try again". It went in as an ordinary user message —
// which is all the product offered — and "again" means start over. The next
// turn created a SECOND Room and a SECOND Artifact. Both pairs are still in
// the operator's database.
//
// The model was not ignoring its receipt. The receipt was correct and
// anonymous: every Palace capability is Confidential, so the payloads were
// never replayed, and the interrupted prose was cut off before it named
// anything. It knew two creates had run and had no way to say which.
package palace

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	chatapp "github.com/corsi/backend/internal/chat/app"
	chatdomain "github.com/corsi/backend/internal/chat/domain"
	chatports "github.com/corsi/backend/internal/chat/ports"
	"github.com/corsi/backend/internal/palace/ports"
	"github.com/corsi/backend/internal/palace/tools"
)

// theIncident scripts the interrupted turn exactly as it happened.
//
// Four rounds asked for, three permitted: a read, two creates, and the two
// item.adds the ceiling turns away.
// The first round also emits PROSE, exactly as the live model did:
//
//	"Vou criar uma room pra esse tipo de coisa e a lista dentro dela."
//
// That detail is load-bearing and its absence hid a real defect. Without
// it the interrupted turn is persisted with empty content, the context
// builder drops it as an empty turn, and the wire happens to end on a user
// message for a reason that has nothing to do with resume. The live run
// had prose, the turn was replayed, and every resume 400d.
func (e *agentEnv) theIncident(conv uuid.UUID) (*sink, *chatdomain.Message, error) {
	e.t.Helper()
	e.llm.scriptWithProse(
		"Vou criar uma room pra esse tipo de coisa e a lista dentro dela.",
		[]call{{tools.RoomListTool, `{}`}},
		[]call{{tools.RoomCreateTool, `{"name":"Cafuné & Mimos"}`}},
		[]call{{tools.ArtifactCreateTool,
			`{"kind":"list","title":"Presentes pra Namorada"}`}},
		[]call{
			{tools.ItemAddTool, `{"artifact_id":"` + uuid.Nil.String() + `","text":"Body splash"}`},
			{tools.ItemAddTool, `{"artifact_id":"` + uuid.Nil.String() + `","text":"Kit de perfumes"}`},
		},
	)
	return e.turnExpectingStop(e.mine, conv,
		"Crie uma Lista, presentes para Namorada ou mimos, pode ser criativo ao criar a room")
}

func (e *agentEnv) palaceCounts() map[string]int {
	e.t.Helper()
	ctx := ctxFor(e.mine)
	out := map[string]int{}
	_, rooms, err := e.svc.ListRooms(ctx, e.mine, ports.RoomFilter{})
	if err != nil {
		e.t.Fatalf("list rooms: %v", err)
	}
	out["rooms"] = int(rooms)
	_, artifacts, err := e.svc.ListArtifacts(ctx, e.mine, ports.ArtifactFilter{})
	if err != nil {
		e.t.Fatalf("list artifacts: %v", err)
	}
	out["artifacts"] = int(artifacts)
	return out
}

/* ── A · the incident, and the resume that closes it ─────────────────── */

func TestTheInterruptedTurnIsContinuedInsteadOfRepeated(t *testing.T) {
	e := newAgentEnv(t)
	_, conv := e.newPalaceAgent(e.mine)

	// ── The interrupted attempt ────────────────────────────────────────
	_, interrupted, err := e.theIncident(conv)
	if err == nil {
		t.Fatal("the fixture no longer reaches the ceiling")
	}
	if interrupted.FinishReason != string(chatdomain.FinishToolRoundLimit) {
		t.Fatalf("finish_reason = %q", interrupted.FinishReason)
	}

	before := e.palaceCounts()
	if before["rooms"] != 1 || before["artifacts"] != 1 {
		t.Fatalf("after the interrupted turn: %+v, want one of each", before)
	}

	// ── The receipt now IDENTIFIES what it did ─────────────────────────
	receipt := e.writeReceipt(e.mine, interrupted)
	if receipt.Executed != 2 || receipt.Refused != 2 {
		t.Fatalf("receipt executed=%d refused=%d, want 2 and 2",
			receipt.Executed, receipt.Refused)
	}
	var roomRef, artifactRef *chatdomain.EffectRef
	for _, w := range receipt.Writes {
		if w.Status != chatdomain.WriteExecuted {
			continue
		}
		switch w.Capability {
		case tools.RoomCreateTool:
			roomRef = w.Ref
		case tools.ArtifactCreateTool:
			artifactRef = w.Ref
		}
	}
	if roomRef == nil || artifactRef == nil {
		t.Fatal("the executed creates carry no effect ref; a resume would be " +
			"back to knowing a create ran and not which one")
	}
	if roomRef.Type != "room" || artifactRef.Type != "artifact" {
		t.Fatalf("refs = %s / %s", roomRef, artifactRef)
	}

	// ── The continuation ───────────────────────────────────────────────
	//
	// It adds the two items the ceiling refused, and NOTHING else. A model
	// that started over would call room.create again; this script is what a
	// correctly continuing model does.
	e.llm.script([]call{
		{tools.ItemAddTool, `{"artifact_id":"` + artifactRef.ID + `","text":"Body splash"}`},
		{tools.ItemAddTool, `{"artifact_id":"` + artifactRef.ID + `","text":"Kit de perfumes"}`},
	})
	s := &sink{}
	resumed, err := e.chatSvc.ResumeTurn(ctxFor(e.mine), chatapp.ResumeInput{
		WorkspaceID:    e.mine,
		ConversationID: conv,
		MessageID:      interrupted.ID,
	}, s)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if resumed.FinishReason != string(chatdomain.FinishStop) {
		t.Fatalf("the continuation ended %q", resumed.FinishReason)
	}

	// ── THE ASSERTION THE INCIDENT IS ABOUT ────────────────────────────
	after := e.palaceCounts()
	if after["rooms"] != 1 {
		t.Fatalf("%d rooms, want exactly 1: the incident created a second one",
			after["rooms"])
	}
	if after["artifacts"] != 1 {
		t.Fatalf("%d artifacts, want exactly 1: the incident created a second one",
			after["artifacts"])
	}

	artifactID, err := uuid.Parse(artifactRef.ID)
	if err != nil {
		t.Fatalf("bad ref: %v", err)
	}
	items, total, err := e.svc.ListItems(ctxFor(e.mine), e.mine, artifactID, ports.Page{})
	if err != nil {
		t.Fatalf("list items: %v", err)
	}
	if total != 2 {
		t.Fatalf("%d items, want the 2 the ceiling refused: %+v", total, items)
	}

	// ── And no create ran in the continuation ──────────────────────────
	for _, ev := range s.tools {
		if ev.Status == "running" {
			continue
		}
		switch chatdomain.ToolName(ev.Name) {
		case tools.RoomCreateTool, tools.ArtifactCreateTool:
			t.Fatalf("the continuation called %s; it must continue, not repeat", ev.Name)
		}
	}
	if r := e.writeReceipt(e.mine, resumed); r.Executed != 2 {
		t.Fatalf("continuation receipt executed=%d, want exactly the 2 items", r.Executed)
	}
}

/* ── B · the resume block carries identity and no content ────────────── */

// The confidentiality boundary, asserted on the bytes the model receives.
//
// The fixture deliberately uses a room name and a list title that would be
// unmistakable if they leaked: a resume that carried them would be undoing
// redaction through a side door.
func TestTheResumeBlockCarriesRefsAndNeverContent(t *testing.T) {
	e := newAgentEnv(t)
	_, conv := e.newPalaceAgent(e.mine)

	_, interrupted, _ := e.theIncident(conv)
	e.llm.script([]call{{tools.ArtifactListTool, `{}`}})
	if _, err := e.chatSvc.ResumeTurn(ctxFor(e.mine), chatapp.ResumeInput{
		WorkspaceID: e.mine, ConversationID: conv, MessageID: interrupted.ID,
	}, &sink{}); err != nil {
		t.Fatalf("resume: %v", err)
	}

	block := resumeBlockIn(t, e)

	// It identifies.
	if !strings.Contains(block, "ref=room:") || !strings.Contains(block, "ref=artifact:") {
		t.Fatalf("the block names no entity:\n%s", block)
	}
	if !strings.Contains(block, string(chatdomain.WriteExecuted)) ||
		!strings.Contains(block, string(chatdomain.WriteNotExecuted)) {
		t.Fatalf("executed and refused are not distinguishable:\n%s", block)
	}
	if !strings.Contains(block, string(chatdomain.ToolErrRoundLimit)) {
		t.Fatalf("the block does not say why it stopped:\n%s", block)
	}

	// And it says nothing else.
	for _, forbidden := range []string{
		"Cafuné", "Mimos", "Presentes pra Namorada", "Body splash", "Kit de perfumes",
	} {
		if strings.Contains(block, forbidden) {
			t.Fatalf("the resume block leaked content: %q", forbidden)
		}
	}
}

// The whole request, not just the block: a payload that escaped into the
// history or a tool message would be just as leaked.
func TestNoConfidentialContentReachesAContinuingTurn(t *testing.T) {
	e := newAgentEnv(t)
	_, conv := e.newPalaceAgent(e.mine)

	_, interrupted, _ := e.theIncident(conv)
	e.llm.script([]call{{tools.ArtifactListTool, `{}`}})
	// `script` resets the recorder, so the continuation's FIRST provider
	// call is request 0 — and it is the only one that proves anything. By
	// its second call the model has legitimately READ the artifact, and the
	// title is in the tool result because that is what a read returns. An
	// assertion over the last request would fail on correct behaviour,
	// which is how this test failed the first time it ran.
	if _, err := e.chatSvc.ResumeTurn(ctxFor(e.mine), chatapp.ResumeInput{
		WorkspaceID: e.mine, ConversationID: conv, MessageID: interrupted.ID,
	}, &sink{}); err != nil {
		t.Fatalf("resume: %v", err)
	}

	var whole strings.Builder
	for _, m := range e.llm.requests[0].Messages {
		whole.WriteString(m.Content)
	}
	for _, forbidden := range []string{"Cafuné", "Presentes pra Namorada", "Body splash"} {
		if strings.Contains(whole.String(), forbidden) {
			t.Fatalf("a Confidential payload reached the continuation: %q", forbidden)
		}
	}
}

/* ── C · a write with no ref must never be given one ─────────────────── */

// The fallback the design depends on.
//
// `relation.create` deliberately reports no ref — its identity is the pair
// of endpoints it already carries, not a new uuid. A resume after it must
// say so honestly rather than inventing an identifier, because a made-up
// ref is worse than none: the continuation would follow it.
func TestAWriteWithoutARefIsReportedWithoutOne(t *testing.T) {
	e := newAgentEnv(t)
	_, conv := e.newPalaceAgent(e.mine)

	// Three rounds of writes, the last one a relation, then a refused
	// fourth so the turn becomes resumable.
	e.llm.script(
		[]call{{tools.ArtifactCreateTool, `{"kind":"project","title":"P"}`}},
		[]call{{tools.MemoryCreateTool, `{"kind":"decision","content":"D"}`}},
		[]call{{tools.RoomCreateTool, `{"name":"R"}`}},
		[]call{{tools.RoomCreateTool, `{"name":"R2"}`}},
	)
	_, interrupted, err := e.turnExpectingStop(e.mine, conv, "faz tudo")
	if err == nil {
		t.Fatal("the fixture did not reach the ceiling")
	}

	receipt := e.writeReceipt(e.mine, interrupted)
	var withRef, withoutRef int
	for _, w := range receipt.Writes {
		if w.Status != chatdomain.WriteExecuted {
			continue
		}
		if w.Ref != nil {
			withRef++
			if _, err := uuid.Parse(w.Ref.ID); err != nil {
				t.Fatalf("%s reported a non-uuid ref %q", w.Capability, w.Ref.ID)
			}
		} else {
			withoutRef++
		}
	}
	if withRef == 0 {
		t.Fatal("no capability reported a ref; the fixture proves nothing")
	}

	e.llm.script([]call{{tools.ArtifactListTool, `{}`}})
	if _, err := e.chatSvc.ResumeTurn(ctxFor(e.mine), chatapp.ResumeInput{
		WorkspaceID: e.mine, ConversationID: conv, MessageID: interrupted.ID,
	}, &sink{}); err != nil {
		t.Fatalf("resume: %v", err)
	}
	block := resumeBlockIn(t, e)

	// Every `ref=` on an ENTRY line is a real uuid. Nothing is approximated.
	//
	// Entries are the bullets; the header also contains the characters
	// `ref=` because it explains what one is, and matching that prose was
	// how this test failed the first time it ran.
	for _, line := range strings.Split(block, "\n") {
		if !strings.HasPrefix(line, "- ") {
			continue
		}
		i := strings.Index(line, "ref=")
		if i < 0 {
			continue
		}
		ref := strings.TrimSpace(line[i+len("ref="):])
		if _, ok := chatdomain.ParseEffectRef(ref); !ok {
			t.Fatalf("the block carries a ref that is not a type and a uuid: %q", ref)
		}
	}
	// And the instruction for the ref-less case is present, so a model that
	// meets one is told what to do instead of guessing.
	if !strings.Contains(block, "no ref") {
		t.Fatalf("the block does not say what to do about a write with no ref:\n%s", block)
	}
}

// The provider's own rule, which no fake gateway enforces and which made
// every live resume fail with a 400 until it was fixed.
//
//	"This model does not support assistant message prefill.
//	 The conversation must end with a user message."
//
// Providers hoist `system` messages out of the list, so the resume block
// does not count as the trailing message. What decides it is the last
// non-system entry — and on a resume that has to be the question being
// answered again, not the interrupted attempt at answering it.
func TestAContinuingTurnDoesNotEndOnAnAssistantMessage(t *testing.T) {
	e := newAgentEnv(t)
	_, conv := e.newPalaceAgent(e.mine)

	_, interrupted, _ := e.theIncident(conv)
	e.llm.script([]call{{tools.ArtifactListTool, `{}`}})
	if _, err := e.chatSvc.ResumeTurn(ctxFor(e.mine), chatapp.ResumeInput{
		WorkspaceID: e.mine, ConversationID: conv, MessageID: interrupted.ID,
	}, &sink{}); err != nil {
		t.Fatalf("resume: %v", err)
	}

	msgs := e.llm.requests[0].Messages
	last := ""
	for _, m := range msgs {
		if m.Role != "system" {
			last = m.Role
		}
	}
	if last != "user" {
		t.Fatalf("the continuation's last non-system message is %q; Anthropic "+
			"refuses that as an assistant prefill and every live resume 400s", last)
	}

	// And the interrupted attempt is not replayed at all: its half-finished
	// prose would invite the model to continue the sentence instead of the
	// work.
	for _, m := range msgs {
		if m.Role == "assistant" && strings.Contains(m.Content, "Vou criar uma room") {
			t.Fatal("the interrupted turn was replayed into the continuation")
		}
	}
}

/* ── D · what a resume refuses ───────────────────────────────────────── */

// A resume is bound to ONE attempt, and only the last one. Anything else
// would let a client replay history out of order, acting on a plan the user
// has since changed.
func TestResumeRefusesAnythingButTheLastInterruptedTurn(t *testing.T) {
	e := newAgentEnv(t)
	_, conv := e.newPalaceAgent(e.mine)

	// An ordinary, completed turn cannot be continued: nothing stopped.
	e.llm.script([]call{{tools.RoomCreateTool, `{"name":"Sala"}`}})
	_, ok := e.turn(e.mine, conv, "cria uma sala")
	if _, err := e.chatSvc.ResumeTurn(ctxFor(e.mine), chatapp.ResumeInput{
		WorkspaceID: e.mine, ConversationID: conv, MessageID: ok.ID,
	}, &sink{}); err == nil {
		t.Fatal("a turn that finished normally was resumable")
	}

	// An interrupted turn that is no longer last cannot be continued
	// either: the conversation moved on.
	_, interrupted, _ := e.theIncident(conv)
	e.llm.script([]call{{tools.ArtifactListTool, `{}`}})
	e.turn(e.mine, conv, "deixa pra lá, outra coisa")
	if _, err := e.chatSvc.ResumeTurn(ctxFor(e.mine), chatapp.ResumeInput{
		WorkspaceID: e.mine, ConversationID: conv, MessageID: interrupted.ID,
	}, &sink{}); err == nil {
		t.Fatal("a superseded turn was resumable")
	}
}

// A resume writes NO user message. "Try again" as a message is the thing
// this whole slice exists to replace.
func TestAResumeAddsNoQuestionToTheTranscript(t *testing.T) {
	e := newAgentEnv(t)
	_, conv := e.newPalaceAgent(e.mine)

	_, interrupted, _ := e.theIncident(conv)
	before := e.userTurnCount(conv)

	e.llm.script([]call{{tools.ArtifactListTool, `{}`}})
	if _, err := e.chatSvc.ResumeTurn(ctxFor(e.mine), chatapp.ResumeInput{
		WorkspaceID: e.mine, ConversationID: conv, MessageID: interrupted.ID,
	}, &sink{}); err != nil {
		t.Fatalf("resume: %v", err)
	}

	if after := e.userTurnCount(conv); after != before {
		t.Fatalf("the transcript gained %d user turns; a resume asks nothing new",
			after-before)
	}
}

/* ── E · ordinary exhaustion is untouched ────────────────────────────── */

// The resume path must not have softened the ceiling. A turn that is never
// continued behaves exactly as S8B left it.
func TestExhaustionStillStopsATurnThatIsNeverResumed(t *testing.T) {
	e := newAgentEnv(t)
	_, conv := e.newPalaceAgent(e.mine)

	s, interrupted, err := e.theIncident(conv)
	if err == nil {
		t.Fatal("the ceiling did not fire")
	}
	if interrupted.FinishReason != string(chatdomain.FinishToolRoundLimit) {
		t.Fatalf("finish_reason = %q", interrupted.FinishReason)
	}
	refused := 0
	for _, ev := range s.tools {
		if ev.Status == string(chatdomain.ToolCallNotExecuted) &&
			ev.ErrorCode == string(chatdomain.ToolErrRoundLimit) {
			refused++
		}
	}
	if refused != 2 {
		t.Fatalf("%d refusals reported, want 2", refused)
	}
	if r := e.writeReceipt(e.mine, interrupted); r.Executed != 2 || r.Refused != 2 {
		t.Fatalf("receipt = %+v", r)
	}
}

/* ── helpers ─────────────────────────────────────────────────────────── */

// resumeBlockIn returns the resume block of the most recent provider call.
const resumeMarker = "You are CONTINUING a previous attempt"

func resumeBlockIn(t *testing.T, e *agentEnv) string {
	t.Helper()
	req := e.llm.requests[len(e.llm.requests)-1]
	for _, m := range req.Messages {
		if m.Role == "system" && strings.Contains(m.Content, resumeMarker) {
			return m.Content
		}
	}
	t.Fatal("the continuing turn carried no resume block")
	return ""
}

func (e *agentEnv) userTurnCount(conv uuid.UUID) int {
	e.t.Helper()
	msgs, err := e.chatSvc.ListMessages(ctxFor(e.mine), e.mine, conv, 200)
	if err != nil {
		e.t.Fatalf("list messages: %v", err)
	}
	n := 0
	for _, m := range msgs {
		if m.Role == chatdomain.RoleUser {
			n++
		}
	}
	return n
}

// scriptWithProse is `script` with text on the FIRST round, which is what a
// real model does: it says what it is about to do and then asks for tools.
//
// It exists because the difference is not cosmetic, and its absence hid a
// real defect for a whole slice. A turn whose rounds carry no text is
// persisted with EMPTY content, and the context builder then drops it as an
// empty turn — so a fixture built with plain `script` cannot exercise
// anything that depends on the interrupted turn actually being replayed.
// The live model emitted prose, the turn WAS replayed, and every resume
// came back 400.
func (f *fakeLLM) scriptWithProse(text string, rounds ...[]call) {
	f.script(rounds...)
	if len(f.rounds) > 0 {
		f.rounds[0] = append([]chatports.StreamEvent{{Delta: text}}, f.rounds[0]...)
	}
}
