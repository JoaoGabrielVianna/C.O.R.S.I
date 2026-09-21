//go:build integration

// Telegram T1 — the acceptance and security assertions.
//
// The harness is in telegram_integration_test.go. This file is only the
// claims.
package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	chatdomain "github.com/corsi/backend/internal/chat/domain"
	tgdomain "github.com/corsi/backend/internal/integrations/telegram/domain"
	tgports "github.com/corsi/backend/internal/integrations/telegram/ports"
)

/* ══════════════════════════════════════════════════════════════════════
   §15 — THE AGENT SWITCHING ACCEPTANCE FLOW
   ══════════════════════════════════════════════════════════════════════ */

// TestSwitchingAgentsKeepsTheirConversationsApart walks the exact flow the
// brief specifies, and then reads the DATABASE rather than the bot's
// replies to decide whether it worked.
//
// ── Why the assertion is on the transcript and the prompt ──────────────
// "No context crossover" is a claim about what each agent can SEE. A test
// that only checked "the bot answered" would pass on an implementation
// that reused one conversation and swapped the agent on it — which is the
// exact mistake this design exists to prevent, and which is invisible
// from the outside until an agent quotes something it was never told.
//
// So there are two independent checks:
//
//  1. the conversations are different rows, and each holds only its own
//     turns;
//  2. the MESSAGES SENT TO THE PROVIDER on a Palace turn contain nothing
//     the operator said to Scout.
func TestSwitchingAgentsKeepsTheirConversationsApart(t *testing.T) {
	e := newEnv(t)
	palace := e.seedAgent(e.wsA, "Palace")
	scout := e.seedAgent(e.wsA, "Scout")

	// 1. a paired Telegram user starts with Palace
	e.pair(e.wsA, operatorUserID, operatorChatID)
	e.selectAgent(operatorUserID, operatorChatID, "Palace")

	// 2-4. a message goes through Palace's runtime and is persisted
	e.llm.script(answer("Guardado."))
	e.tg.reset()
	e.send(operatorUserID, operatorChatID, "Guarde que preciso comprar café amanhã.")

	if got := e.tg.last(t).Text; !strings.Contains(got, "Guardado.") {
		t.Fatalf("Palace's answer did not reach telegram: %q", got)
	}
	palaceConv := e.conversationOf(operatorChatID, palace)
	if palaceConv == uuid.Nil {
		t.Fatal("no conversation was mapped for Palace")
	}
	palaceTurns := e.transcript(e.wsA, palaceConv)
	if len(palaceTurns) != 2 {
		t.Fatalf("Palace's conversation has %d turns, want the question and the answer", len(palaceTurns))
	}
	if palaceTurns[0].Role != chatdomain.RoleUser ||
		!strings.Contains(palaceTurns[0].Content, "comprar café") {
		t.Fatalf("the user turn was not persisted normally: %+v", palaceTurns[0])
	}
	if palaceTurns[1].Role != chatdomain.RoleAssistant || palaceTurns[1].Content != "Guardado." {
		t.Fatalf("the assistant turn was not persisted normally: %+v", palaceTurns[1])
	}

	// 5-7. /agents, select Scout, ask it something
	e.selectAgent(operatorUserID, operatorChatID, "Scout")
	e.llm.script(answer("Três vagas novas."))
	e.tg.reset()
	e.send(operatorUserID, operatorChatID, "Quais vagas apareceram hoje?")

	// 8. Scout got its OWN conversation
	scoutConv := e.conversationOf(operatorChatID, scout)
	if scoutConv == uuid.Nil {
		t.Fatal("no conversation was mapped for Scout")
	}
	if scoutConv == palaceConv {
		t.Fatal("Scout reused Palace's conversation; switching agents must never move a thread")
	}

	// ── The crossover check that actually matters ───────────────────
	//
	// What the model SAW on Scout's turn. Nothing the operator told
	// Palace may be in it.
	scoutPrompt := e.llm.lastPrompt(t)
	for _, m := range scoutPrompt {
		if strings.Contains(m.Content, "comprar café") {
			t.Fatalf("CONTEXT CROSSOVER: Palace's message reached Scout's prompt:\n%q", m.Content)
		}
	}

	// 9-11. back to Palace, another message, and conversation A resumes
	e.selectAgent(operatorUserID, operatorChatID, "Palace")
	e.llm.script(answer("Sim, o café."))
	e.tg.reset()
	e.send(operatorUserID, operatorChatID, "O que eu pedi para guardar?")

	if again := e.conversationOf(operatorChatID, palace); again != palaceConv {
		t.Fatalf("returning to Palace opened a new conversation (%s, was %s)", again, palaceConv)
	}

	// Palace's window carries its own history, and only its own.
	palacePrompt := e.llm.lastPrompt(t)
	var sawCoffee, sawJobs bool
	for _, m := range palacePrompt {
		if strings.Contains(m.Content, "comprar café") {
			sawCoffee = true
		}
		if strings.Contains(m.Content, "vagas") {
			sawJobs = true
		}
	}
	if !sawCoffee {
		t.Fatal("Palace did not resume its own conversation: its earlier turn is not in the window")
	}
	if sawJobs {
		t.Fatal("CONTEXT CROSSOVER: Scout's message reached Palace's prompt")
	}

	// And the rows agree: four turns in Palace's thread, two in Scout's.
	if n := len(e.transcript(e.wsA, palaceConv)); n != 4 {
		t.Fatalf("Palace's conversation has %d turns, want 4", n)
	}
	if n := len(e.transcript(e.wsA, scoutConv)); n != 2 {
		t.Fatalf("Scout's conversation has %d turns, want 2", n)
	}
}

// TestTelegramWritesOrdinaryCorsiMessages.
//
// Telegram must not be a second runtime, and the observable form of that
// claim is that its turns are indistinguishable from the web client's:
// same table, same roles, same model stamp, same usage accounting.
func TestTelegramWritesOrdinaryCorsiMessages(t *testing.T) {
	e := newEnv(t)
	agent := e.seedAgent(e.wsA, "Palace")
	e.pair(e.wsA, operatorUserID, operatorChatID)
	e.selectAgent(operatorUserID, operatorChatID, "Palace")

	e.llm.script(answer("resposta"))
	e.send(operatorUserID, operatorChatID, "pergunta sintética")

	turns := e.transcript(e.wsA, e.conversationOf(operatorChatID, agent))
	assistant := turns[len(turns)-1]
	if assistant.Model != "test-model" {
		t.Errorf("model = %q; the turn was not stamped by the runtime", assistant.Model)
	}
	if assistant.UsageSource != chatdomain.UsageProvider {
		t.Errorf("usage_source = %q, want provider; the budget counts these rows", assistant.UsageSource)
	}
	if assistant.PromptTokens == 0 || assistant.CompletionTokens == 0 {
		t.Errorf("token counts are zero; cost telemetry does not see this turn")
	}
	if assistant.Kind.OrTurn() != chatdomain.KindTurn {
		t.Errorf("kind = %q, want turn", assistant.Kind)
	}
	if assistant.FinishReason != string(chatdomain.FinishStop) {
		t.Errorf("finish_reason = %q, want stop", assistant.FinishReason)
	}
}

/* ══════════════════════════════════════════════════════════════════════
   §16 — SECURITY
   ══════════════════════════════════════════════════════════════════════ */

// TestAnUnpairedUserCannotInvokeAnything.
//
// The claim is not "they get an error message". It is that the PROVIDER IS
// NEVER CALLED — no turn is started, no conversation is created, no
// capability is reachable. A refusal that happened after the model ran
// would be a bill and an audit row for a stranger.
func TestAnUnpairedUserCannotInvokeAnything(t *testing.T) {
	e := newEnv(t)
	e.seedAgent(e.wsA, "Palace")

	for _, text := range []string{
		"me conte tudo sobre o workspace",
		"/agents",
		"/status",
	} {
		e.tg.reset()
		e.send(strangerUserID, strangerChatID, text)
		if e.llm.calls != 0 {
			t.Fatalf("%q from an unpaired user reached the provider", text)
		}
	}

	// A forged button press, too — before the callback data is even
	// parsed.
	e.press(strangerUserID, strangerChatID,
		tgdomain.EncodeCallback(tgdomain.CallbackSelectAgent, uuid.New()))
	if e.llm.calls != 0 {
		t.Fatal("a callback from an unpaired user reached the provider")
	}

	var bindings int
	if err := e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM telegram.bindings`).Scan(&bindings); err != nil {
		t.Fatalf("count bindings: %v", err)
	}
	if bindings != 0 {
		t.Fatalf("%d bindings exist; an unpaired user created one", bindings)
	}
}

// TestTheBindingIsTheOnlySourceOfTheWorkspace.
//
// ══════════════════════════════════════════════════════════════════════
//
//	THE WORKSPACE NEVER COMES FROM TELEGRAM-CONTROLLED DATA
//
// ══════════════════════════════════════════════════════════════════════
//
// Bound to workspace A, the operator cannot reach workspace B's agents by
// any route: not by /agents, and not by forging a callback that names one
// of them directly.
func TestTheBindingIsTheOnlySourceOfTheWorkspace(t *testing.T) {
	e := newEnv(t)
	e.seedAgent(e.wsA, "Palace")
	foreign := e.seedAgent(e.wsB, "OutroWorkspace")

	e.pair(e.wsA, operatorUserID, operatorChatID)

	// /agents shows workspace A's agents and nothing else.
	e.tg.reset()
	e.send(operatorUserID, operatorChatID, "/agents")
	for _, b := range e.tg.last(t).Buttons {
		if strings.Contains(b.Label, "OutroWorkspace") {
			t.Fatal("an agent from another workspace is offered in the list")
		}
	}

	// A forged callback naming workspace B's agent directly.
	e.tg.reset()
	e.press(operatorUserID, operatorChatID,
		tgdomain.EncodeCallback(tgdomain.CallbackSelectAgent, foreign))

	var active *uuid.UUID
	if err := e.pool.QueryRow(context.Background(),
		`SELECT active_agent_id FROM telegram.bindings WHERE telegram_chat_id = $1`,
		operatorChatID).Scan(&active); err != nil {
		t.Fatalf("read binding: %v", err)
	}
	if active != nil && *active == foreign {
		t.Fatal("A FORGED CALLBACK SELECTED AN AGENT IN ANOTHER WORKSPACE")
	}
	if e.conversationOf(operatorChatID, foreign) != uuid.Nil {
		t.Fatal("a conversation was opened against another workspace's agent")
	}
}

// TestAForgedResumeCannotReachAnotherConversation.
//
// The structural claim: a resume callback carries a MESSAGE id and no
// conversation, so the conversation is resolved from the binding's active
// agent. Naming a message that belongs to a different thread — here, one
// in another workspace entirely — cannot make the runtime act on that
// thread, because there is no field in which to say so.
func TestAForgedResumeCannotReachAnotherConversation(t *testing.T) {
	e := newEnv(t)
	e.seedAgent(e.wsA, "Palace")
	victimAgent := e.seedAgent(e.wsB, "Vítima")

	// A real conversation in workspace B, with a real turn in it.
	victimConv, err := e.chatSvc.CreateConversation(context.Background(),
		createConversationIn(e.wsB, victimAgent))
	if err != nil {
		t.Fatalf("create victim conversation: %v", err)
	}
	e.llm.script(answer("conteúdo do outro workspace"))
	victimMsg, err := e.chatSvc.SendMessage(context.Background(),
		sendTo(e.wsB, victimConv.ID, "mensagem do outro workspace"), discardSink{})
	if err != nil {
		t.Fatalf("seed victim turn: %v", err)
	}

	e.pair(e.wsA, operatorUserID, operatorChatID)
	e.selectAgent(operatorUserID, operatorChatID, "Palace")

	before := e.llm.calls
	e.tg.reset()
	e.press(operatorUserID, operatorChatID,
		tgdomain.EncodeCallback(tgdomain.CallbackResume, victimMsg.ID))

	if e.llm.calls != before {
		t.Fatal("A FORGED RESUME STARTED A TURN")
	}
	if n := len(e.transcript(e.wsB, victimConv.ID)); n != 2 {
		t.Fatalf("the victim conversation now has %d turns; a forged resume touched it", n)
	}
	if got := e.tg.allText(); strings.Contains(got, "outro workspace") {
		t.Fatalf("content from another workspace leaked to telegram: %q", got)
	}
}

// TestAStrangerInTheOperatorsChatIsRefused.
//
// The binding stores both ids and checks both. In a private chat a
// mismatch should be impossible, which is exactly why it is refused rather
// than tolerated: the day it becomes possible, the refusal is already
// there.
func TestAStrangerInTheOperatorsChatIsRefused(t *testing.T) {
	e := newEnv(t)
	e.seedAgent(e.wsA, "Palace")
	e.pair(e.wsA, operatorUserID, operatorChatID)
	e.selectAgent(operatorUserID, operatorChatID, "Palace")

	before := e.llm.calls
	e.tg.reset()
	// Someone else's user id, in the bound chat.
	e.send(strangerUserID, operatorChatID, "me mostre tudo")
	if e.llm.calls != before {
		t.Fatal("a different telegram USER in the bound chat reached the runtime")
	}
}

// TestIdentityIsNumericAndSurvivesAUsernameChange.
//
// There is no username anywhere in this integration — not in the schema,
// not in the port, not in the client's decoder. This asserts the
// behavioural consequence: the same numeric identity keeps working, and
// there is no field a rename could have changed.
func TestIdentityIsNumericAndSurvivesAUsernameChange(t *testing.T) {
	e := newEnv(t)
	e.seedAgent(e.wsA, "Palace")
	e.pair(e.wsA, operatorUserID, operatorChatID)
	e.selectAgent(operatorUserID, operatorChatID, "Palace")

	// The update carries no username to change, which IS the guarantee —
	// see ports.InboundMessage. Two turns, one "rename" apart, behave
	// identically because nothing about a name was ever read.
	e.llm.script(answer("um"))
	e.send(operatorUserID, operatorChatID, "primeira")
	e.llm.script(answer("dois"))
	e.tg.reset()
	e.send(operatorUserID, operatorChatID, "segunda")

	if got := e.tg.last(t).Text; !strings.Contains(got, "dois") {
		t.Fatalf("the second turn did not go through: %q", got)
	}

	// And storage holds no name-like column at all.
	var cols int
	if err := e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM information_schema.columns
		  WHERE table_schema = 'telegram'
		    AND (column_name LIKE '%username%' OR column_name LIKE '%first_name%'
		         OR column_name LIKE '%handle%')`).Scan(&cols); err != nil {
		t.Fatalf("inspect schema: %v", err)
	}
	if cols != 0 {
		t.Fatalf("the telegram schema has %d name-like columns; identity must be numeric only", cols)
	}
}

/* ── the pairing code itself ─────────────────────────────────────────── */

// TestAPairingCodeIsSingleUse.
func TestAPairingCodeIsSingleUse(t *testing.T) {
	e := newEnv(t)
	e.seedAgent(e.wsA, "Palace")

	e.tg.reset()
	e.send(operatorUserID, operatorChatID, "/start")
	code := extractCode(t, e.tg.last(t).Text)

	if _, err := e.tgSvc.ConfirmPairing(context.Background(), e.wsA, code); err != nil {
		t.Fatalf("first confirmation: %v", err)
	}
	// Revoke, so the second attempt fails on the CODE rather than on the
	// "already bound" check — which is the property under test.
	if err := e.tgSvc.RevokePairing(context.Background(), operatorChatID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := e.tgSvc.ConfirmPairing(context.Background(), e.wsA, code); err == nil {
		t.Fatal("A CONSUMED PAIRING CODE WAS ACCEPTED A SECOND TIME")
	}
}

// TestAnExpiredCodeIsRefused, driven through the real SQL rather than the
// domain predicate: expiry is evaluated inside the redeeming statement,
// and that is the copy that matters.
func TestAnExpiredCodeIsRefused(t *testing.T) {
	e := newEnv(t)
	e.tg.reset()
	e.send(operatorUserID, operatorChatID, "/start")
	code := extractCode(t, e.tg.last(t).Text)

	if _, err := e.pool.Exec(context.Background(),
		`UPDATE telegram.pairing_codes SET expires_at = now() - interval '1 minute'`); err != nil {
		t.Fatalf("age the code: %v", err)
	}
	if _, err := e.tgSvc.ConfirmPairing(context.Background(), e.wsA, code); err == nil {
		t.Fatal("AN EXPIRED PAIRING CODE WAS ACCEPTED")
	}
}

// TestTheCodeIsNeverStoredInPlaintext.
//
// Read straight out of the table the operator's backups contain.
func TestTheCodeIsNeverStoredInPlaintext(t *testing.T) {
	e := newEnv(t)
	e.tg.reset()
	e.send(operatorUserID, operatorChatID, "/start")
	code := tgdomain.NormalizeCode(extractCode(t, e.tg.last(t).Text))

	rows, err := e.pool.Query(context.Background(),
		`SELECT code_hash::text FROM telegram.pairing_codes`)
	if err != nil {
		t.Fatalf("read codes: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var stored string
		if err := rows.Scan(&stored); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if strings.Contains(strings.ToUpper(stored), code) {
			t.Fatal("THE PAIRING CODE IS IN THE DATABASE IN A READABLE FORM")
		}
	}
}

// TestRepeatedStartLeavesOneLiveCode.
func TestRepeatedStartLeavesOneLiveCode(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 3; i++ {
		e.send(operatorUserID, operatorChatID, "/start")
	}
	var live int
	if err := e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM telegram.pairing_codes WHERE consumed_at IS NULL`).Scan(&live); err != nil {
		t.Fatalf("count: %v", err)
	}
	if live != 1 {
		t.Fatalf("%d live codes after three /start; want exactly 1", live)
	}
}

// TestTheBotTokenNeverReachesPersistenceOrAMessage.
//
// The deployment secret, checked against every place this integration
// writes: its own schema, and the operator's phone.
func TestTheBotTokenNeverReachesPersistenceOrAMessage(t *testing.T) {
	e := newEnv(t)
	e.seedAgent(e.wsA, "Palace")
	e.pair(e.wsA, operatorUserID, operatorChatID)
	e.selectAgent(operatorUserID, operatorChatID, "Palace")
	e.llm.script(answer("olá"))
	e.send(operatorUserID, operatorChatID, "oi")

	if strings.Contains(e.tg.allText(), fixtureBotToken) {
		t.Fatal("THE BOT TOKEN WAS SENT TO TELEGRAM AS MESSAGE CONTENT")
	}

	// Every text-ish column in the telegram schema, and the chat
	// transcript, scanned for the literal token.
	for _, q := range []string{
		`SELECT coalesce(string_agg(t::text, ' '), '') FROM telegram.bindings t`,
		`SELECT coalesce(string_agg(t::text, ' '), '') FROM telegram.chat_agents t`,
		`SELECT coalesce(string_agg(t::text, ' '), '') FROM telegram.pairing_codes t`,
		`SELECT coalesce(string_agg(t::text, ' '), '') FROM telegram.update_cursor t`,
		`SELECT coalesce(string_agg(content, ' '), '') FROM chat.messages`,
	} {
		var dump string
		if err := e.pool.QueryRow(context.Background(), q).Scan(&dump); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		if strings.Contains(dump, fixtureBotToken) {
			t.Fatalf("THE BOT TOKEN IS PERSISTED — found by: %s", q)
		}
	}
}

/* ══════════════════════════════════════════════════════════════════════
   §9 — CONCURRENCY
   ══════════════════════════════════════════════════════════════════════ */

// TestOneTurnPerChat.
//
// The second message arrives while the first is still running. The rule is
// refusal, not queueing: it must not start a second turn against the same
// conversation, and it must say so.
func TestOneTurnPerChat(t *testing.T) {
	e := newEnv(t)
	agent := e.seedAgent(e.wsA, "Palace")
	e.pair(e.wsA, operatorUserID, operatorChatID)
	e.selectAgent(operatorUserID, operatorChatID, "Palace")

	// A turn that takes long enough to still be running when the second
	// message arrives. The gate is the fake gateway's own latency, which
	// is the honest way to hold a turn open.
	release := make(chan struct{})
	e.llm.hold(release, answer("resposta lenta"))

	started := make(chan struct{})
	go func() {
		close(started)
		e.send(operatorUserID, operatorChatID, "primeira mensagem")
	}()
	<-started
	waitUntil(t, func() bool { return e.llm.inFlight() }, 3*time.Second,
		"the first turn never reached the provider")

	e.tg.reset()
	e.send(operatorUserID, operatorChatID, "segunda mensagem")
	busyReply := e.tg.allText()

	close(release)
	waitUntil(t, func() bool { return !e.llm.inFlight() }, 5*time.Second,
		"the first turn never finished")

	if !strings.Contains(strings.ToLower(busyReply), "processando") {
		t.Fatalf("the second message was not refused with a 'still working' reply: %q", busyReply)
	}

	// And the decisive part: the second message left NO turn behind.
	turns := e.transcript(e.wsA, e.conversationOf(operatorChatID, agent))
	for _, m := range turns {
		if strings.Contains(m.Content, "segunda mensagem") {
			t.Fatal("the refused message was persisted as a turn; it must not reach the runtime at all")
		}
	}
	if len(turns) != 2 {
		t.Fatalf("the conversation has %d turns, want exactly the first exchange", len(turns))
	}
}

/* ══════════════════════════════════════════════════════════════════════
   §10 — SAFE RESUME
   ══════════════════════════════════════════════════════════════════════ */

// TestAnInterruptedTurnOffersContinueAndResumesThroughTheRuntime.
//
// ══════════════════════════════════════════════════════════════════════
//
//	NO HIDDEN EXECUTION AFTER TURN FAILURE
//	THE BUTTON IS A RESUME, NOT THE WORD "CONTINUE"
//
// ══════════════════════════════════════════════════════════════════════
//
// The turn is driven into the tool round ceiling, which is the terminal
// guaranteed to leave work both done and pending. Then:
//
//   - the persisted turn is delivered even though the turn FAILED;
//   - a [Continuar] button is attached;
//   - pressing it writes NO user message — which is the whole difference
//     between continuing and starting over;
//   - pressing it a second time is refused, by the runtime.
func TestAnInterruptedTurnOffersContinueAndResumesThroughTheRuntime(t *testing.T) {
	e := newEnv(t)
	agent := e.seedAgent(e.wsA, "Palace")
	grantEcho(t, e, e.wsA, agent)

	e.pair(e.wsA, operatorUserID, operatorChatID)
	e.selectAgent(operatorUserID, operatorChatID, "Palace")

	// Four provider calls that all ask for a tool drives the ceiling.
	e.llm.script(
		askTool("c1", "system.echo", `{"text":"um"}`),
		askTool("c2", "system.echo", `{"text":"dois"}`),
		askTool("c3", "system.echo", `{"text":"três"}`),
		askTool("c4", "system.echo", `{"text":"quatro"}`),
	)
	e.tg.reset()
	e.send(operatorUserID, operatorChatID, "faça a coisa toda")

	conv := e.conversationOf(operatorChatID, agent)
	turns := e.transcript(e.wsA, conv)
	interrupted := turns[len(turns)-1]
	if interrupted.FinishReason != string(chatdomain.FinishToolRoundLimit) {
		t.Fatalf("finish_reason = %q, want tool_round_limit; the fixture did not reach the ceiling",
			interrupted.FinishReason)
	}

	// The failed turn was still DELIVERED, with its button.
	last := e.tg.last(t)
	if len(last.Buttons) != 1 || last.Buttons[0].Label != "Continuar" {
		t.Fatalf("no [Continuar] button under an interrupted turn: %+v", last.Buttons)
	}
	cb, err := tgdomain.DecodeCallback(last.Buttons[0].Data)
	if err != nil {
		t.Fatalf("the button's callback data does not decode: %v", err)
	}
	if cb.Kind != tgdomain.CallbackResume || cb.ID != interrupted.ID {
		t.Fatalf("the button names %v/%s, want a resume of %s", cb.Kind, cb.ID, interrupted.ID)
	}

	// ── Press it ────────────────────────────────────────────────────
	userTurnsBefore := countUserTurns(turns)
	e.llm.script(answer("terminei o que faltava"))
	e.tg.reset()
	e.press(operatorUserID, operatorChatID, last.Buttons[0].Data)

	after := e.transcript(e.wsA, conv)
	if got := countUserTurns(after); got != userTurnsBefore {
		t.Fatalf("the resume wrote %d new user turn(s); a resume must write NONE — "+
			"that is the difference between continuing and 'try again'",
			got-userTurnsBefore)
	}
	resumed := after[len(after)-1]
	if resumed.Role != chatdomain.RoleAssistant || resumed.Content != "terminei o que faltava" {
		t.Fatalf("the resumed turn was not persisted: %+v", resumed)
	}
	if !strings.Contains(e.tg.allText(), "terminei o que faltava") {
		t.Fatal("the resumed answer did not reach telegram")
	}

	// ── The resume block came from the audit trail, not from prose ──
	//
	// ══════════════════════════════════════════════════════════════
	//
	//	NO TOOL RECEIPT, NO EXECUTION CLAIM
	//
	// ══════════════════════════════════════════════════════════════
	//
	// The interrupted turn ran `system.echo` three times — and echo's
	// EFFECT IS A READ. So the block must say "nothing was completed",
	// because a receipt answers "did anything CHANGE" and a listing that
	// ran is not a change.
	//
	// That is the assertion worth making: the block is derived from
	// execution records with a declared effect, not from the fact that
	// tool calls happened. A block that named echo here would mean
	// Telegram had inherited a resume that reports reads as completed
	// work, which is how a continuation decides not to redo something it
	// never did.
	var sawResumeBlock bool
	for _, m := range e.llm.lastPrompt(t) {
		if !strings.Contains(m.Content, "CONTINUING a previous attempt") {
			continue
		}
		sawResumeBlock = true
		if !strings.Contains(m.Content, "nothing was completed") {
			t.Errorf("a read-effect capability was reported as a completed write:\n%s", m.Content)
		}
		if !strings.Contains(m.Content, "tool_round_limit") {
			t.Errorf("the block does not carry the runtime's own terminal reason:\n%s", m.Content)
		}
	}
	if !sawResumeBlock {
		t.Fatal("the continuing turn was not given the runtime's resume block")
	}

	// ── Pressing again is refused, idempotently ─────────────────────
	before := e.llm.calls
	e.tg.reset()
	e.press(operatorUserID, operatorChatID, last.Buttons[0].Data)
	if e.llm.calls != before {
		t.Fatal("a second press of [Continuar] started another turn")
	}
	if got := strings.ToLower(e.tg.allText()); !strings.Contains(got, "não pode mais ser continuado") {
		t.Fatalf("the second press was not refused clearly: %q", e.tg.allText())
	}
}

// TestAnOrdinaryTurnOffersNoContinueButton. The button is not decoration:
// it must appear only where the runtime says a resume is possible.
func TestAnOrdinaryTurnOffersNoContinueButton(t *testing.T) {
	e := newEnv(t)
	e.seedAgent(e.wsA, "Palace")
	e.pair(e.wsA, operatorUserID, operatorChatID)
	e.selectAgent(operatorUserID, operatorChatID, "Palace")

	e.llm.script(answer("pronto"))
	e.tg.reset()
	e.send(operatorUserID, operatorChatID, "oi")

	if bs := e.tg.last(t).Buttons; len(bs) != 0 {
		t.Fatalf("a normal turn offered %+v", bs)
	}
}

/* ══════════════════════════════════════════════════════════════════════
   §11 — LONG RESPONSES
   ══════════════════════════════════════════════════════════════════════ */

// TestALongAnswerIsOneCorsiMessageAndSeveralTelegramMessages.
//
// ══════════════════════════════════════════════════════════════════════
//
//	SPLIT THE TRANSPORT, NEVER THE RECORD
//
// ══════════════════════════════════════════════════════════════════════
//
// Writing four `chat.messages` rows because Telegram wanted four bubbles
// would corrupt the transcript for every other surface: the web client
// would render four answers and the context window would replay four
// assistant turns.
func TestALongAnswerIsOneCorsiMessageAndSeveralTelegramMessages(t *testing.T) {
	e := newEnv(t)
	agent := e.seedAgent(e.wsA, "Palace")
	e.pair(e.wsA, operatorUserID, operatorChatID)
	e.selectAgent(operatorUserID, operatorChatID, "Palace")

	// ~12k characters of realistic Portuguese with paragraph structure.
	long := strings.Repeat("Um parágrafo de teste com acentuação: ação, coração, é só. ", 200)
	e.llm.script(answer(long))
	e.tg.reset()
	e.send(operatorUserID, operatorChatID, "escreva bastante")

	sent := e.tg.messages()
	if len(sent) < 3 {
		t.Fatalf("a %d-character answer produced %d telegram messages; it should have been split",
			len(long), len(sent))
	}

	// ONE persisted assistant turn, whole.
	turns := e.transcript(e.wsA, e.conversationOf(operatorChatID, agent))
	var assistants []chatdomain.Message
	for _, m := range turns {
		if m.Role == chatdomain.RoleAssistant {
			assistants = append(assistants, m)
		}
	}
	if len(assistants) != 1 {
		t.Fatalf("%d assistant messages persisted; telegram's transport must not fragment the record",
			len(assistants))
	}
	if assistants[0].Content != long {
		t.Fatalf("the persisted answer was truncated: %d chars, want %d",
			len(assistants[0].Content), len(long))
	}

	// ── Nothing lost on the wire either ────────────────────────────
	//
	// Compared on the NON-WHITESPACE content, which is exactly the
	// guarantee the chunker offers: it may drop the space or newline AT a
	// boundary it chose — that is what keeps a bubble from starting with
	// a blank line — and may drop nothing else. Comparing raw strings
	// would be asserting a guarantee that was never made; comparing
	// lengths would pass on a chunker that reordered.
	var joined strings.Builder
	for _, m := range sent {
		joined.WriteString(m.Text)
	}
	if got, want := dropSpace(joined.String()), dropSpace(long); got != want {
		t.Fatalf("content was lost between the persisted answer and the telegram messages: "+
			"%d non-space runes on the wire, %d persisted", len([]rune(got)), len([]rune(want)))
	}
}

/* ══════════════════════════════════════════════════════════════════════
   §8 — THE RECEIPTS REACH THIS SURFACE
   ══════════════════════════════════════════════════════════════════════ */

// TestTheWriteReceiptReachesTelegram.
//
// The defect being guarded against, restated: a finance agent reported
// "8 transações importadas" in a turn with zero tool calls. Every layer
// behaved; the PRODUCT rendered prose with nothing beside it.
//
// This asserts that a turn which executed a capability says so on the
// phone, and — the half that actually matters — that a turn which executed
// NOTHING does not.
func TestTheWriteReceiptReachesTelegram(t *testing.T) {
	e := newEnv(t)
	agent := e.seedAgent(e.wsA, "Palace")
	grantEcho(t, e, e.wsA, agent)
	e.pair(e.wsA, operatorUserID, operatorChatID)
	e.selectAgent(operatorUserID, operatorChatID, "Palace")

	// A turn that calls nothing, while CLAIMING to have written.
	e.llm.script(answer("Pronto, gravei 8 transações."))
	e.tg.reset()
	e.send(operatorUserID, operatorChatID, "importe o extrato")

	if got := e.tg.allText(); strings.Contains(got, "escritas:") {
		t.Fatalf("a turn with no tool calls rendered a write receipt: %q", got)
	}
	// The claim is still delivered — this product does not censor the
	// model — and the receipt is what would contradict it. Asserting the
	// prose arrives keeps the guarantee honest about its own limits.
	if !strings.Contains(e.tg.allText(), "gravei 8 transações") {
		t.Fatal("the answer itself was not delivered")
	}

	turns := e.transcript(e.wsA, e.conversationOf(operatorChatID, agent))
	receipts, err := e.chatSvc.WriteReceipts(context.Background(), e.wsA,
		[]uuid.UUID{turns[len(turns)-1].ID})
	if err != nil {
		t.Fatalf("write receipts: %v", err)
	}
	if r := receipts[turns[len(turns)-1].ID]; r.Confirmed() {
		t.Fatal("the runtime reported a confirmed write for a turn that called nothing")
	}
}

// TestTheReadReceiptReachesTelegram.
//
// The inverted twin, and the inversion is the point: for an external READ
// the dangerous state is the SILENCE. An agent that COULD have read
// externally and did not must say so.
//
// `Available` is the domain's own presentation gate and is `false` for an
// agent with no external capability — which is every agent in this suite,
// because the internal diagnostic tools are not External. So the assertion
// is the conservative one the gate implies: no label where absence is
// meaningless.
func TestTheReadReceiptGateIsRespected(t *testing.T) {
	e := newEnv(t)
	agent := e.seedAgent(e.wsA, "Palace")
	grantEcho(t, e, e.wsA, agent)
	e.pair(e.wsA, operatorUserID, operatorChatID)
	e.selectAgent(operatorUserID, operatorChatID, "Palace")

	e.llm.script(answer("1.535 seguidores, 40.055 views."))
	e.tg.reset()
	e.send(operatorUserID, operatorChatID, "como estão meus números?")

	turns := e.transcript(e.wsA, e.conversationOf(operatorChatID, agent))
	last := turns[len(turns)-1]
	rr, err := e.chatSvc.ReadReceipts(context.Background(), e.wsA,
		e.conversationOf(operatorChatID, agent), []uuid.UUID{last.ID})
	if err != nil {
		t.Fatalf("read receipts: %v", err)
	}
	got := rr[last.ID]
	if got.Status != chatdomain.NoExternalRead {
		t.Fatalf("status = %q, want NO_EXTERNAL_READ for a turn that called nothing", got.Status)
	}
	// This agent holds no External capability, so the gate says the
	// absence is not worth rendering — and Telegram respects it.
	if got.Available {
		t.Fatal("this fixture agent should hold no external capability")
	}
	if strings.Contains(e.tg.allText(), "leitura externa") {
		t.Fatal("the read receipt was rendered where the domain's gate says it is meaningless")
	}
}

/* ══════════════════════════════════════════════════════════════════════
   §13 — ERRORS
   ══════════════════════════════════════════════════════════════════════ */

// TestNothingInternalReachesThePhone.
//
// Driven by deleting the agent out from under a selected chat, which is a
// real, reachable failure. Whatever the phone receives must carry no ids,
// no SQL, no stack trace and no Go error text.
func TestNothingInternalReachesThePhone(t *testing.T) {
	e := newEnv(t)
	agent := e.seedAgent(e.wsA, "Palace")
	e.pair(e.wsA, operatorUserID, operatorChatID)
	e.selectAgent(operatorUserID, operatorChatID, "Palace")

	// A REAL gateway failure, whose text carries every kind of thing that
	// must not travel. `err.Error()` on a phone is how a connection string
	// ends up in somebody's chat history, so the failure is manufactured
	// to contain one.
	secretish := "pq: SELECT * FROM chat.providers; dsn=postgres://corsi:hunter2@db:5432 " +
		"goroutine 42 panic workspace=" + e.wsA.String() + " agent=" + agent.String()
	e.llm.failNext(errors.New(secretish))

	e.tg.reset()
	e.send(operatorUserID, operatorChatID, "oi?")

	text := e.tg.allText()
	for _, forbidden := range []string{
		agent.String(), e.wsA.String(), "SELECT", "postgres://", "hunter2",
		"goroutine", "panic", "chat.providers", "pq:", "dsn=",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("internal detail %q reached the phone: %q", forbidden, text)
		}
	}
	if strings.TrimSpace(text) == "" {
		t.Fatal("the operator was told nothing at all; a swallowed failure is worse than a vague one")
	}
}

// TestAGroupChatIsRefused. Groups are out of scope, and a group is a room
// full of people who are not the operator.
func TestAGroupChatIsRefused(t *testing.T) {
	e := newEnv(t)
	e.seedAgent(e.wsA, "Palace")
	e.pair(e.wsA, operatorUserID, operatorChatID)

	before := e.llm.calls
	e.tg.reset()
	if err := e.tgSvc.HandleUpdate(context.Background(), tgports.Update{
		UpdateID: nextUpdateID(),
		Message: &tgports.InboundMessage{
			MessageID: 1, TelegramUserID: operatorUserID,
			TelegramChatID: operatorChatID, ChatType: "supergroup", Text: "oi",
		},
	}); err != nil {
		t.Logf("HandleUpdate: %v", err)
	}
	if e.llm.calls != before {
		t.Fatal("a group message reached the runtime")
	}
}

/* ── §6 — DISABLING THE INTEGRATION ──────────────────────────────────── */

// TestTheSchemaExistsEvenWithTheIntegrationOff.
//
// The entrypoint applies this timeline unconditionally, so enabling
// Telegram later is one environment variable and not a migration the
// operator has to remember. The tables being empty is the whole footprint
// of "off".
func TestTheSchemaExistsEvenWithTheIntegrationOff(t *testing.T) {
	e := newEnv(t)
	for _, rel := range []string{
		"telegram.bindings", "telegram.chat_agents",
		"telegram.pairing_codes", "telegram.update_cursor",
	} {
		var exists bool
		if err := e.pool.QueryRow(context.Background(),
			`SELECT to_regclass($1) IS NOT NULL`, rel).Scan(&exists); err != nil {
			t.Fatalf("check %s: %v", rel, err)
		}
		if !exists {
			t.Fatalf("relation %s is missing", rel)
		}
	}
}

/* ── helpers used only by the assertions above ───────────────────────── */

func countUserTurns(msgs []chatdomain.Message) int {
	n := 0
	for _, m := range msgs {
		if m.Role == chatdomain.RoleUser {
			n++
		}
	}
	return n
}

// dropSpace removes every space, newline and tab, which is the measure
// "no content loss" is defined on. See domain.Chunk.
func dropSpace(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\n', '\r', '\t':
			return -1
		}
		return r
	}, s)
}

func waitUntil(t *testing.T, cond func() bool, limit time.Duration, msg string) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal(msg)
}

// compile-time proof the port's types are the ones the suite drives.
var _ = tgports.Update{}

/* ══════════════════════════════════════════════════════════════════════
   THE REGRESSION THE FIRST LIVE WRITE FOUND
   ══════════════════════════════════════════════════════════════════════ */

// TestARealCapabilityResolvesItsWorkspaceThroughTelegram.
//
// ══════════════════════════════════════════════════════════════════════
//
//	A DRIVING ADAPTER MUST STAMP THE WORKSPACE
//
// ══════════════════════════════════════════════════════════════════════
//
// ── The incident ───────────────────────────────────────────────────────
// The first real write sent from a phone — `palace.artifact.create` — was
// refused with "this call arrived without a workspace, so no Palace data
// could be resolved". Everything upstream had worked: the binding
// resolved, the grant held, the model asked for exactly the right
// capability with exactly the right arguments.
//
// The cause: a capability reads its workspace from the CONTEXT
// (palace/tools.workspaceOf, and the identical function in github,
// jobradar, threads and metathreads), and the context is stamped by the
// HTTP middleware. Telegram has no request, so nothing stamped it.
// `SendMessageInput.WorkspaceID` scopes the repository reads and never
// reaches the tool executor.
//
// ── Why the suite did not catch it ─────────────────────────────────────
// Because it registered only the internal diagnostics, and `system.echo`
// is the one capability in the product that never calls workspaceOf. The
// tool loop was exercised end to end and proved nothing about resolution.
// That is the real lesson, and it is why Palace is now part of the
// harness: a tool test whose tool needs nothing tests the tool, not the
// seam.
//
// This test fails without the stamp in telegramRuntime.scoped.
func TestARealCapabilityResolvesItsWorkspaceThroughTelegram(t *testing.T) {
	e := newEnv(t)
	agent := e.seedAgent(e.wsA, "Palace")
	if err := e.chatSvc.AuthorizeTool(context.Background(), e.wsA, agent,
		chatdomain.ToolName("palace.room.create")); err != nil {
		t.Fatalf("authorize palace.room.create: %v", err)
	}
	e.pair(e.wsA, operatorUserID, operatorChatID)
	e.selectAgent(operatorUserID, operatorChatID, "Palace")

	e.llm.script(
		askTool("c1", "palace.room.create", `{"name":"SMOKE-REGRESSION","description":"sintético"}`),
		answer("Criei."),
	)
	e.tg.reset()
	e.send(operatorUserID, operatorChatID, "crie a sala de teste")

	// ── The capability actually ran and actually wrote ──────────────
	var rooms int
	if err := e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM palace.rooms WHERE workspace_id = $1 AND name = 'SMOKE-REGRESSION'`,
		e.wsA).Scan(&rooms); err != nil {
		t.Fatalf("count rooms: %v", err)
	}
	if rooms != 1 {
		t.Fatalf("%d rooms created; the capability could not resolve its workspace "+
			"— telegramRuntime.scoped is the stamp that makes this pass", rooms)
	}

	// ── And the receipt says so, from execution records ─────────────
	turns := e.transcript(e.wsA, e.conversationOf(operatorChatID, agent))
	last := turns[len(turns)-1]
	got, err := e.chatSvc.WriteReceipts(context.Background(), e.wsA, []uuid.UUID{last.ID})
	if err != nil {
		t.Fatalf("write receipts: %v", err)
	}
	r := got[last.ID]
	if !r.Confirmed() || r.Executed != 1 || r.Failed != 0 {
		t.Fatalf("receipt = executed %d / failed %d / refused %d, want exactly one execution",
			r.Executed, r.Failed, r.Refused)
	}
	// EffectRef: the receipt must identify WHAT was created, which is what
	// makes a resume able to act on it instead of creating a second one.
	if len(r.Writes) != 1 || r.Writes[0].Ref == nil {
		t.Fatalf("the executed write carries no effect ref: %+v", r.Writes)
	}

	// ── And the footer reached the phone ────────────────────────────
	if !strings.Contains(e.tg.allText(), "escritas: 1 executada(s)") {
		t.Fatalf("the write receipt was not rendered on the phone: %q", e.tg.allText())
	}
}

// TestAWorkspacelessContextStillRefuses.
//
// The other half, and the half that must NOT be fixed by the stamp: a
// capability reached without a workspace refuses rather than defaulting.
// There is no fallback workspace that is not somebody's real record.
//
// Driven by calling the runtime adapter directly with a bare context,
// bypassing `scoped` — which is precisely what the broken path did.
func TestAWorkspacelessContextStillRefuses(t *testing.T) {
	e := newEnv(t)
	agent := e.seedAgent(e.wsA, "Palace")
	if err := e.chatSvc.AuthorizeTool(context.Background(), e.wsA, agent,
		chatdomain.ToolName("palace.room.create")); err != nil {
		t.Fatalf("authorize: %v", err)
	}
	conv, err := e.chatSvc.CreateConversation(context.Background(),
		createConversationIn(e.wsA, agent))
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}

	e.llm.script(
		askTool("c1", "palace.room.create", `{"name":"NAO-DEVE-EXISTIR","description":"x"}`),
		answer("pronto"),
	)
	// Straight at the chat service with an UNSTAMPED context.
	msg, err := e.chatSvc.SendMessage(context.Background(),
		sendTo(e.wsA, conv.ID, "crie"), discardSink{})
	if err != nil {
		t.Logf("SendMessage: %v", err)
	}

	var rooms int
	if err := e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM palace.rooms WHERE name = 'NAO-DEVE-EXISTIR'`).Scan(&rooms); err != nil {
		t.Fatalf("count: %v", err)
	}
	if rooms != 0 {
		t.Fatal("a capability wrote without a workspace in context; the refusal must never become a default")
	}
	// The receipt reports the failure rather than hiding it, which is why
	// the live incident was a bug and not a silent corruption.
	got, err := e.chatSvc.WriteReceipts(context.Background(), e.wsA, []uuid.UUID{msg.ID})
	if err != nil {
		t.Fatalf("receipts: %v", err)
	}
	if r := got[msg.ID]; r.Confirmed() {
		t.Fatal("the receipt confirmed a write that never happened")
	}
}
