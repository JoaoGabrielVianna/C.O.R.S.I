//go:build integration

// Integration tests for Agent Memory, against a real Postgres and
// the same scripted LLM fake the rest of the suite uses.
//
// The harness — newEnv, seed, do, decode, fakeLLM — lives in
// chat_integration_test.go. This file only adds the invariants Memory
// introduces, and they are the ones that would destroy the capability if
// they broke quietly:
//
//	a memory learnt in one thread is used in the next     §13.1
//	deleting the thread does not delete the memory        §13.2
//	one agent's memory never reaches another agent        §13.3
//	a memory that is off is a memory that is not sent     §13.4
//	the budget cuts whole items and says that it did      §13.5
//	an unreadable memory table degrades, never refuses    §13.6
//	workspace isolation, like every other table           §13.7
//	saving a memory never calls the provider              §13.8
//
// The section numbers are the acceptance criteria this file enforces.
package chat

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/chat/app"
	"github.com/corsi/backend/internal/chat/ports"
)

/* ── fixtures ────────────────────────────────────────────────────────── */

type apiMemory struct {
	ID                      string  `json:"id"`
	AgentID                 string  `json:"agent_id"`
	Content                 string  `json:"content"`
	Origin                  string  `json:"origin"`
	SourceConversationID    *string `json:"source_conversation_id"`
	SourceConversationTitle *string `json:"source_conversation_title"`
	SourceMessageSeq        *int64  `json:"source_message_seq"`
	Enabled                 bool    `json:"enabled"`
	Pinned                  bool    `json:"pinned"`
	InContext               bool    `json:"in_context"`
	// ModelProposed says the words came from a model the user approved
	// rather than from the user. Added in v1.1.0; every capture
	// mechanism that existed before it writes false.
	ModelProposed bool `json:"model_proposed"`
}

type memoryPage struct {
	Items            []apiMemory `json:"items"`
	Limit            int         `json:"limit"`
	Total            int64       `json:"total"`
	UsedCharacters   int         `json:"used_characters"`
	BudgetCharacters int         `json:"budget_characters"`
}

// remember creates a memory the way the Memory page does: no provenance.
func (e *env) remember(ws uuid.UUID, agentID, content string) apiMemory {
	e.t.Helper()
	rec := e.do("POST", "/chat/agents/"+agentID+"/memories", ws,
		map[string]any{"content": content})
	wantStatus(e.t, rec, http.StatusCreated)
	return decode[apiMemory](e.t, rec)
}

// rememberFrom creates a memory the way the chat does: from a thread, which
// is what makes the "why does it know this?" question answerable.
func (e *env) rememberFrom(ws uuid.UUID, agentID, conversationID, content string) apiMemory {
	e.t.Helper()
	rec := e.do("POST", "/chat/agents/"+agentID+"/memories", ws, map[string]any{
		"content":                content,
		"source_conversation_id": conversationID,
	})
	wantStatus(e.t, rec, http.StatusCreated)
	return decode[apiMemory](e.t, rec)
}

func (e *env) memories(ws uuid.UUID, agentID string) memoryPage {
	e.t.Helper()
	rec := e.do("GET", "/chat/agents/"+agentID+"/memories", ws, nil)
	wantStatus(e.t, rec, http.StatusOK)
	return decode[memoryPage](e.t, rec)
}

// memoryBlockOf returns the system message the memory block produced on the
// last turn, or "" when the turn carried no memory at all.
//
// It identifies the block by its header rather than by position, so a change
// in composition order fails the tests that assert order instead of quietly
// breaking every test in this file.
func memoryBlockOf(req ports.CompletionRequest) string {
	for _, m := range req.Messages {
		if m.Role == "system" && strings.HasPrefix(m.Content, "O que você deve lembrar") {
			return m.Content
		}
	}
	return ""
}

/* ── §13.1  a memory crosses conversations ───────────────────────────── */

// TestMemoryLearntInOneConversationIsUsedInTheNext is the requirement, in
// one test: the user says "remember this" in one thread and the agent knows
// it in a thread that did not exist yet when it was said.
//
// This is the whole reason Memory is not conversation history.
func TestMemoryLearntInOneConversationIsUsedInTheNext(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)

	e.rememberFrom(e.wsA, s.agentID, s.conversationID,
		"No C.O.R.S.I., Agents vem antes de Finance")

	// A second thread, created after the memory existed.
	other := e.newConversation(e.wsA, s.agentID)
	e.send(e.wsA, other, "por onde eu começo?")

	block := memoryBlockOf(e.llm.lastRequest)
	if block == "" {
		t.Fatalf("the second conversation carried no memory block: %+v", e.llm.lastRequest.Messages)
	}
	if !strings.Contains(block, "Agents vem antes de Finance") {
		t.Fatalf("memory block = %q, want the saved fact", block)
	}

	// And it sits where the builder says it does: after the instructions,
	// before anything the conversation contributed.
	roles := make([]string, 0, len(e.llm.lastRequest.Messages))
	for _, m := range e.llm.lastRequest.Messages {
		roles = append(roles, m.Role)
	}
	if len(roles) < 3 || roles[0] != "system" || roles[1] != "system" || roles[2] != "user" {
		t.Fatalf("wire roles = %v, want instructions, memory, then the question", roles)
	}
}

/* ── §13.2  the memory outlives its origin ───────────────────────────── */

// TestMemorySurvivesTheDeletionOfItsConversation is the rule that dictated
// the schema: a weak reference, so a memory captured in a thread is not
// collateral damage when that thread is deleted.
//
// It also covers what the interface must be able to render afterwards: the
// origin is reported as gone, not as an error and not as a manual memory.
func TestMemorySurvivesTheDeletionOfItsConversation(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)

	// Give the thread a title, so "the title disappeared" is a real change
	// and not the empty string it started as.
	e.send(e.wsA, s.conversationID, "planejamento do v1")
	saved := e.rememberFrom(e.wsA, s.agentID, s.conversationID, "o canal publica às terças")

	before := e.memories(e.wsA, s.agentID).Items
	if len(before) != 1 || before[0].SourceConversationTitle == nil {
		t.Fatalf("before deletion the origin should resolve: %+v", before)
	}
	if *before[0].SourceConversationTitle != "planejamento do v1" {
		t.Fatalf("origin title = %q", *before[0].SourceConversationTitle)
	}

	wantStatus(t, e.do("DELETE", "/chat/conversations/"+s.conversationID, e.wsA, nil),
		http.StatusNoContent)

	after := e.memories(e.wsA, s.agentID).Items
	if len(after) != 1 {
		t.Fatalf("the memory did not survive its conversation: %+v", after)
	}
	if after[0].ID != saved.ID {
		t.Fatalf("memory id changed: %q → %q", saved.ID, after[0].ID)
	}
	// The id is still there — a soft delete leaves the row — and the title
	// is not. That pair is exactly what the interface renders as "the
	// conversation is no longer available".
	if after[0].SourceConversationTitle != nil {
		t.Fatalf("origin title still resolves after the deletion: %q", *after[0].SourceConversationTitle)
	}
	if after[0].Origin != "conversation" {
		t.Fatalf("origin = %q, want it to stay conversation: a lost thread does not make a memory manual", after[0].Origin)
	}

	// And it is still used, which is the half a nil title could hide.
	other := e.newConversation(e.wsA, s.agentID)
	e.send(e.wsA, other, "quando eu publico?")
	if !strings.Contains(memoryBlockOf(e.llm.lastRequest), "terças") {
		t.Fatalf("the orphaned memory stopped reaching the model")
	}
}

/* ── §13.3  memory does not leak between agents ──────────────────────── */

// TestMemoryIsScopedToItsAgent is the reason memory belongs to the agent
// rather than to the workspace. A leak here is the difference between
// "specialised agents" and "every agent knows everything".
func TestMemoryIsScopedToItsAgent(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)
	other := e.newAgent(e.wsA, s.providerID, "Outro agente")
	otherConv := e.newConversation(e.wsA, other)

	e.remember(e.wsA, s.agentID, "segredo do primeiro agente")

	if got := e.memories(e.wsA, other).Items; len(got) != 0 {
		t.Fatalf("the other agent lists %d memories, want none", len(got))
	}

	e.send(e.wsA, otherConv, "o que você sabe?")
	if block := memoryBlockOf(e.llm.lastRequest); block != "" {
		t.Fatalf("another agent's turn carried a memory block: %q", block)
	}

	// The same isolation on the write path: a memory id from one agent is
	// not addressable through another agent's URL.
	mine := e.memories(e.wsA, s.agentID).Items[0]
	rec := e.do("PATCH", "/chat/agents/"+other+"/memories/"+mine.ID, e.wsA,
		map[string]any{"content": "sequestrada"})
	wantErrorCode(t, rec, http.StatusNotFound, "not_found")
	rec = e.do("DELETE", "/chat/agents/"+other+"/memories/"+mine.ID, e.wsA, nil)
	wantErrorCode(t, rec, http.StatusNotFound, "not_found")

	// A memory cannot be given provenance in another agent's thread either.
	rec = e.do("POST", "/chat/agents/"+other+"/memories", e.wsA, map[string]any{
		"content":                "de onde isso veio?",
		"source_conversation_id": s.conversationID,
	})
	wantErrorCode(t, rec, http.StatusBadRequest, "invalid")
}

/* ── §13.4  off is not deleted ───────────────────────────────────────── */

// TestDisabledMemoryIsKeptAndNotSent: turning a memory off is a reversible
// experiment, and it has to actually stop reaching the model or the
// experiment answers nothing.
func TestDisabledMemoryIsKeptAndNotSent(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)

	on := e.remember(e.wsA, s.agentID, "fato que fica")
	off := e.remember(e.wsA, s.agentID, "fato desligado")

	rec := e.do("PATCH", "/chat/agents/"+s.agentID+"/memories/"+off.ID, e.wsA,
		map[string]any{"enabled": false})
	wantStatus(t, rec, http.StatusOK)

	e.send(e.wsA, s.conversationID, "e agora?")
	block := memoryBlockOf(e.llm.lastRequest)
	if !strings.Contains(block, "fato que fica") {
		t.Fatalf("the enabled memory did not reach the model: %q", block)
	}
	if strings.Contains(block, "fato desligado") {
		t.Fatalf("a disabled memory reached the model: %q", block)
	}

	// Still listed, still editable, and honest about not being used.
	page := e.memories(e.wsA, s.agentID)
	if page.Total != 2 {
		t.Fatalf("total = %d, want the disabled one still counted", page.Total)
	}
	for _, m := range page.Items {
		switch m.ID {
		case on.ID:
			if !m.InContext {
				t.Fatalf("the enabled memory is not marked as used")
			}
		case off.ID:
			if m.Enabled || m.InContext {
				t.Fatalf("the disabled memory reads as used: %+v", m)
			}
		}
	}

	// Turning it back on is the other half of "reversible".
	rec = e.do("PATCH", "/chat/agents/"+s.agentID+"/memories/"+off.ID, e.wsA,
		map[string]any{"enabled": true})
	wantStatus(t, rec, http.StatusOK)
	e.send(e.wsA, s.conversationID, "e agora?")
	if !strings.Contains(memoryBlockOf(e.llm.lastRequest), "fato desligado") {
		t.Fatalf("re-enabling did not put the memory back in the context")
	}
}

/* ── §13.5  the budget cuts whole items, and reports it ──────────────── */

// TestMemoryBudgetCutsWholeItems fills the block past its ceiling through
// the real stack and checks the two things that matter on the wire: the
// block never exceeds the budget, and no memory arrives cut in half.
//
// The unit test in app/context_test.go owns the arithmetic. This one owns
// the claim that the arithmetic is what production runs.
func TestMemoryBudgetCutsWholeItems(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)

	// Each memory is a fifth of the budget, so the tenth cannot fit.
	const each = app.MemoryBudgetChars / 5
	for i := 0; i < 10; i++ {
		e.remember(e.wsA, s.agentID, strings.Repeat(string(rune('a'+i)), each))
	}

	e.send(e.wsA, s.conversationID, "pergunta")
	block := memoryBlockOf(e.llm.lastRequest)
	if got := len([]rune(block)); got > app.MemoryBudgetChars {
		t.Fatalf("memory block is %d characters, over the %d ceiling", got, app.MemoryBudgetChars)
	}

	// Whole items only: every bullet is either the full run or absent.
	for _, line := range memoryBullets(t, block) {
		if len([]rune(line)) != each {
			t.Fatalf("a memory arrived cut: %d characters, want %d", len([]rune(line)), each)
		}
	}

	// And the page says how much of what was saved is actually in use.
	page := e.memories(e.wsA, s.agentID)
	used := 0
	for _, m := range page.Items {
		if m.InContext {
			used++
		}
	}
	if used == 0 || used >= 10 {
		t.Fatalf("in_context marks %d of 10; want a real cut reported", used)
	}
	if page.UsedCharacters != len([]rune(block)) {
		t.Fatalf("page reports %d characters used, the wire carried %d",
			page.UsedCharacters, len([]rune(block)))
	}
	if page.BudgetCharacters != app.MemoryBudgetChars {
		t.Fatalf("page reports a %d budget, want %d", page.BudgetCharacters, app.MemoryBudgetChars)
	}
}

// TestPinnedMemoryWinsTheBudget: `pinned` is the control offered in place of
// a relevance score, so it has to actually decide who survives a cut.
func TestPinnedMemoryWinsTheBudget(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)

	// Each memory is a third of the budget, so the third one cannot fit.
	const each = app.MemoryBudgetChars / 3
	pinned := e.remember(e.wsA, s.agentID, strings.Repeat("p", each))
	newer := e.remember(e.wsA, s.agentID, strings.Repeat("q", each))
	newest := e.remember(e.wsA, s.agentID, strings.Repeat("r", each))

	rec := e.do("PATCH", "/chat/agents/"+s.agentID+"/memories/"+pinned.ID, e.wsA,
		map[string]any{"pinned": true})
	wantStatus(t, rec, http.StatusOK)
	// Touch the other two afterwards, so the pinned one is now the OLDEST
	// by updated_at. Without `pinned DESC` in the ordering it would be the
	// one dropped; with it, it is the one that cannot be dropped.
	for _, id := range []string{newer.ID, newest.ID} {
		wantStatus(t, e.do("PATCH", "/chat/agents/"+s.agentID+"/memories/"+id, e.wsA,
			map[string]any{"enabled": true}), http.StatusOK)
	}

	e.send(e.wsA, s.conversationID, "pergunta")
	bullets := memoryBullets(t, memoryBlockOf(e.llm.lastRequest))
	if len(bullets) != 2 {
		t.Fatalf("block carries %d memories, want the two that fit", len(bullets))
	}
	if !strings.HasPrefix(bullets[0], "p") {
		t.Fatalf("first bullet starts with %q, want the pinned memory", bullets[0][:1])
	}
	for _, b := range bullets {
		if strings.HasPrefix(b, "q") {
			t.Fatalf("the oldest unpinned memory survived over the newest")
		}
	}
}

// memoryBullets splits the block into its items, dropping the header.
//
// Asserting on a substring of the whole block would be a trap: the header
// itself contains most letters of the alphabet, so `strings.Index(block,
// "q")` finds the "q" of "O que" and answers a question nobody asked.
func memoryBullets(t *testing.T, block string) []string {
	t.Helper()
	if block == "" {
		return nil
	}
	parts := strings.Split(block, "\n- ")
	if len(parts) < 2 {
		t.Fatalf("memory block carries no items: %q", block)
	}
	return parts[1:]
}

/* ── §13.6  an unreadable memory table degrades the turn ─────────────── */

// TestMemoryReadFailureDegradesTheTurn removes the table under a live stack
// and sends a message.
//
// The trade being asserted: an unreachable side table must cost the turn its
// memory, never its answer. A 500 here would mean one broken migration takes
// the whole product down instead of one of its inputs.
func TestMemoryReadFailureDegradesTheTurn(t *testing.T) {
	d := dsn(t)
	e := newEnv(t)
	s := e.seed(e.wsA)
	e.remember(e.wsA, s.agentID, "um fato que não será lido")

	// Put the schema back for whatever runs next, even though every test
	// here starts from freshDB.
	t.Cleanup(func() { freshDB(t, d) })
	if _, err := e.pool.Exec(t.Context(), `DROP TABLE chat.agent_memories`); err != nil {
		t.Fatalf("drop memories table: %v", err)
	}

	rec := e.do("POST", "/chat/conversations/"+s.conversationID+"/messages", e.wsA,
		map[string]any{"content": "responde mesmo assim?"})
	wantStatus(t, rec, http.StatusOK)

	frames := parseSSE(t, rec.Body.String())
	if _, isErr := frameOf(frames, "error"); isErr {
		t.Fatalf("the turn failed instead of degrading: %v", eventNames(frames))
	}
	if _, ok := frameOf(frames, "done"); !ok {
		t.Fatalf("no done frame: %v", eventNames(frames))
	}
	if memoryBlockOf(e.llm.lastRequest) != "" {
		t.Fatalf("a memory block was built from an unreadable table")
	}
	if e.llm.streamCalls == 0 {
		t.Fatalf("the provider was never called; the turn did not happen")
	}
}

/* ── §13.7  workspace isolation ──────────────────────────────────────── */

// TestMemoryWorkspaceIsolation reads workspace A's memory from workspace B,
// the same assertion every other table in this module carries.
func TestMemoryWorkspaceIsolation(t *testing.T) {
	e := newEnv(t)
	a := e.seed(e.wsA)
	e.remember(e.wsA, a.agentID, "só de A")

	// The agent itself is invisible from B, so every route under it is a
	// 404 rather than an empty list — which is the honest answer: from B,
	// that agent never existed.
	for _, call := range []struct{ method, path string }{
		{"GET", "/chat/agents/" + a.agentID + "/memories"},
		{"POST", "/chat/agents/" + a.agentID + "/memories"},
	} {
		var body any
		if call.method == "POST" {
			body = map[string]any{"content": "invasão"}
		}
		rec := e.do(call.method, call.path, e.wsB, body)
		wantErrorCode(t, rec, http.StatusNotFound, "not_found")
	}

	// And B's own agent, in B's own workspace, remembers nothing of A's.
	b := e.seed(e.wsB)
	if got := e.memories(e.wsB, b.agentID).Items; len(got) != 0 {
		t.Fatalf("workspace B sees %d memories, want none", len(got))
	}
	e.send(e.wsB, b.conversationID, "o que você sabe?")
	if block := memoryBlockOf(e.llm.lastRequest); block != "" {
		t.Fatalf("workspace B's turn carried A's memory: %q", block)
	}
}

/* ── §13.8  saving costs nothing ─────────────────────────────────────── */

// TestSavingAMemoryNeverCallsTheProvider is the promise the whole capture
// design rests on: /lembrar and the message action write a row and ask the
// model nothing.
//
// The frontend intercepts /lembrar before it becomes a request, so what is
// verifiable here is the half that lives on this side: the endpoint it calls
// instead of sending a message never reaches the LLM port.
func TestSavingAMemoryNeverCallsTheProvider(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)

	before := e.llm.streamCalls
	e.remember(e.wsA, s.agentID, "salvo sem gastar token")
	e.rememberFrom(e.wsA, s.agentID, s.conversationID, "este também")
	e.memories(e.wsA, s.agentID)

	if e.llm.streamCalls != before {
		t.Fatalf("saving a memory called the provider %d times", e.llm.streamCalls-before)
	}
}

/* ── CRUD, provenance and the guards ─────────────────────────────────── */

func TestMemoryLifecycle(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)

	t.Run("a manual memory has no origin to show", func(t *testing.T) {
		m := e.remember(e.wsA, s.agentID, "prefere respostas diretas")
		if m.Origin != "manual" {
			t.Fatalf("origin = %q, want manual", m.Origin)
		}
		if m.SourceConversationID != nil {
			t.Fatalf("a manual memory names a conversation: %+v", m)
		}
		if !m.Enabled || m.Pinned {
			t.Fatalf("defaults wrong: %+v", m)
		}
	})

	t.Run("a memory from a thread keeps the turn it came from", func(t *testing.T) {
		rec := e.do("POST", "/chat/agents/"+s.agentID+"/memories", e.wsA, map[string]any{
			"content":                "veio da conversa",
			"source_conversation_id": s.conversationID,
			"source_message_seq":     7,
		})
		wantStatus(t, rec, http.StatusCreated)
		m := decode[apiMemory](t, rec)
		if m.Origin != "conversation" || m.SourceConversationID == nil {
			t.Fatalf("provenance missing: %+v", m)
		}
		if m.SourceMessageSeq == nil || *m.SourceMessageSeq != 7 {
			t.Fatalf("source_message_seq = %v, want 7", m.SourceMessageSeq)
		}
	})

	t.Run("editing the text does not rewrite where it came from", func(t *testing.T) {
		m := e.rememberFrom(e.wsA, s.agentID, s.conversationID, "texto original")
		rec := e.do("PATCH", "/chat/agents/"+s.agentID+"/memories/"+m.ID, e.wsA,
			map[string]any{"content": "texto corrigido"})
		wantStatus(t, rec, http.StatusOK)
		got := decode[apiMemory](t, rec)
		if got.Content != "texto corrigido" {
			t.Fatalf("content = %q", got.Content)
		}
		if got.Origin != "conversation" || got.SourceConversationID == nil {
			t.Fatalf("editing erased the provenance: %+v", got)
		}
	})

	t.Run("deleting removes it from the list and from the context", func(t *testing.T) {
		m := e.remember(e.wsA, s.agentID, "para apagar")
		rec := e.do("DELETE", "/chat/agents/"+s.agentID+"/memories/"+m.ID, e.wsA, nil)
		wantStatus(t, rec, http.StatusNoContent)

		for _, item := range e.memories(e.wsA, s.agentID).Items {
			if item.ID == m.ID {
				t.Fatalf("the deleted memory is still listed")
			}
		}
		e.send(e.wsA, s.conversationID, "pergunta")
		if strings.Contains(memoryBlockOf(e.llm.lastRequest), "para apagar") {
			t.Fatalf("the deleted memory still reaches the model")
		}
		// Deleting twice is a 404, not a second success.
		rec = e.do("DELETE", "/chat/agents/"+s.agentID+"/memories/"+m.ID, e.wsA, nil)
		wantErrorCode(t, rec, http.StatusNotFound, "not_found")
	})

	t.Run("empty content is refused", func(t *testing.T) {
		rec := e.do("POST", "/chat/agents/"+s.agentID+"/memories", e.wsA,
			map[string]any{"content": "   "})
		wantErrorCode(t, rec, http.StatusBadRequest, "invalid")
	})

	t.Run("content past the ceiling is refused", func(t *testing.T) {
		rec := e.do("POST", "/chat/agents/"+s.agentID+"/memories", e.wsA,
			map[string]any{"content": strings.Repeat("x", 2001)})
		wantErrorCode(t, rec, http.StatusBadRequest, "invalid")
	})

	t.Run("a memory under an agent that does not exist is a 404", func(t *testing.T) {
		rec := e.do("POST", "/chat/agents/"+uuid.NewString()+"/memories", e.wsA,
			map[string]any{"content": "órfã"})
		wantErrorCode(t, rec, http.StatusNotFound, "not_found")
	})

	t.Run("a memory id that is not a uuid is a 400", func(t *testing.T) {
		rec := e.do("DELETE", "/chat/agents/"+s.agentID+"/memories/abc", e.wsA, nil)
		wantErrorCode(t, rec, http.StatusBadRequest, "invalid")
	})
}

// TestAgentWithMemoriesCannotBeDeleted: agents are soft-deleted, so the
// ON DELETE RESTRICT never fires and the application layer has to ask. An
// agent removed with memories attached would take them out of every
// interface and leave the rows behind.
func TestAgentWithMemoriesCannotBeDeleted(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)
	agent := e.newAgent(e.wsA, s.providerID, "Agente com memória")
	m := e.remember(e.wsA, agent, "um fato")

	rec := e.do("DELETE", "/chat/agents/"+agent, e.wsA, nil)
	wantErrorCode(t, rec, http.StatusConflict, "conflict")

	wantStatus(t, e.do("DELETE", "/chat/agents/"+agent+"/memories/"+m.ID, e.wsA, nil),
		http.StatusNoContent)
	wantStatus(t, e.do("DELETE", "/chat/agents/"+agent, e.wsA, nil), http.StatusNoContent)
}

/* ── the list contract the Brain reads ───────────────────────────────── */

// TestMemoryListIsTheGraphSource states what the Memory page and the Brain
// view are allowed to draw from, because the graph is derived from this
// response and from nothing else.
//
// Everything the Brain renders as a node or an edge has to be a field here.
// If a relation cannot be read off this payload it does not exist in v1 —
// there is no second source, no inference, and no model call behind the
// picture. This test is where that claim is anchored.
func TestMemoryListIsTheGraphSource(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)
	e.send(e.wsA, s.conversationID, "planejamento do v1")

	e.rememberFrom(e.wsA, s.agentID, s.conversationID, "veio da conversa")
	e.remember(e.wsA, s.agentID, "veio da página")

	rec := e.do("GET", "/chat/agents/"+s.agentID+"/memories", e.wsA, nil)
	wantStatus(t, rec, http.StatusOK)

	var raw struct {
		Items []map[string]json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(raw.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(raw.Items))
	}

	// The fields a node or an edge may be built from. A rename breaks this
	// before it breaks a picture nobody is testing.
	required := []string{"id", "agent_id", "content", "origin", "enabled", "pinned",
		"in_context", "created_at", "updated_at"}
	for _, item := range raw.Items {
		for _, f := range required {
			if _, ok := item[f]; !ok {
				t.Fatalf("memory payload is missing %q: %v", f, keysOf(item))
			}
		}
	}

	// Provenance is present on the one that has it and absent on the one
	// that does not — which is what makes the origin node in the graph a
	// derived fact rather than a decoration.
	page := e.memories(e.wsA, s.agentID)
	var fromThread, manual int
	for _, m := range page.Items {
		switch m.Origin {
		case "conversation":
			fromThread++
			if m.SourceConversationID == nil || m.SourceConversationTitle == nil {
				t.Fatalf("a memory from a thread has no resolvable origin: %+v", m)
			}
		case "manual":
			manual++
			if m.SourceConversationID != nil {
				t.Fatalf("a manual memory names a conversation: %+v", m)
			}
		default:
			t.Fatalf("unknown origin %q — the graph has no node type for it", m.Origin)
		}
	}
	if fromThread != 1 || manual != 1 {
		t.Fatalf("origins = %d from a thread, %d manual; want one of each", fromThread, manual)
	}
}

func keysOf(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
