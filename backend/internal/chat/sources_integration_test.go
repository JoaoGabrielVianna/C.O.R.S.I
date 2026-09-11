//go:build integration

// Integration tests for Agent Sources, against a real Postgres
// and the scripted LLM fake the rest of the suite uses.
//
// The harness — newEnv, seed, do, decode, fakeLLM — lives in
// chat_integration_test.go. This file adds only the invariants Sources
// introduces, which are the eight acceptance criteria for Sources:
//
//	an enabled source reaches the model                    §10.1
//	a disabled one does not                                §10.2
//	one agent's sources never reach another                §10.3
//	the block never exceeds the budget, and cuts report    §10.4
//	an oversized source is excluded, never truncated       §10.5
//	an unreadable sources table degrades, never refuses    §10.6
//	workspace isolation, like every other table            §10.7
//	one token heuristic, shared by the page and the report §10.8
package chat

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/chat/app"
	"github.com/corsi/backend/internal/chat/domain"
	"github.com/corsi/backend/internal/chat/ports"
)

/* ── fixtures ────────────────────────────────────────────────────────── */

type apiSource struct {
	ID              string `json:"id"`
	AgentID         string `json:"agent_id"`
	Title           string `json:"title"`
	Description     string `json:"description"`
	Content         string `json:"content"`
	Enabled         bool   `json:"enabled"`
	Characters      int    `json:"characters"`
	EstimatedTokens int    `json:"estimated_tokens"`
	InContext       bool   `json:"in_context"`
	Oversized       bool   `json:"oversized"`
}

type sourcePage struct {
	Items            []apiSource `json:"items"`
	Limit            int         `json:"limit"`
	Total            int64       `json:"total"`
	UsedCharacters   int         `json:"used_characters"`
	BudgetCharacters int         `json:"budget_characters"`
}

func (e *env) addSource(ws uuid.UUID, agentID, title, content string) apiSource {
	e.t.Helper()
	rec := e.do("POST", "/chat/agents/"+agentID+"/sources", ws,
		map[string]any{"title": title, "content": content})
	wantStatus(e.t, rec, http.StatusCreated)
	return decode[apiSource](e.t, rec)
}

func (e *env) sources(ws uuid.UUID, agentID string) sourcePage {
	e.t.Helper()
	rec := e.do("GET", "/chat/agents/"+agentID+"/sources", ws, nil)
	wantStatus(e.t, rec, http.StatusOK)
	return decode[sourcePage](e.t, rec)
}

// sourcesBlockOf returns the system message the sources block produced on
// the last turn, or "" when the turn carried none.
//
// Identified by its header rather than by position, so a change in
// composition order fails the test that asserts order instead of silently
// breaking every test in this file.
func sourcesBlockOf(req ports.CompletionRequest) string {
	for _, m := range req.Messages {
		if m.Role == "system" && strings.HasPrefix(m.Content, "Material de referência") {
			return m.Content
		}
	}
	return ""
}

/* ── §10.1 and §10.2  enabled reaches the model, disabled does not ───── */

func TestEnabledSourceReachesTheModel(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)

	e.addSource(e.wsA, s.agentID, "Estratégia de conteúdo", "O canal publica às terças.")
	e.send(e.wsA, s.conversationID, "quando eu publico?")

	block := sourcesBlockOf(e.llm.lastRequest)
	if block == "" {
		t.Fatalf("the turn carried no sources block: %+v", e.llm.lastRequest.Messages)
	}
	// The title is included on purpose: it is the anchor the model cites by.
	if !strings.Contains(block, "## Estratégia de conteúdo") {
		t.Fatalf("the title did not reach the model: %q", block)
	}
	if !strings.Contains(block, "O canal publica às terças.") {
		t.Fatalf("the content did not reach the model: %q", block)
	}
}

func TestDisabledSourceIsKeptAndNotSent(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)

	on := e.addSource(e.wsA, s.agentID, "Ligada", "conteúdo que fica")
	off := e.addSource(e.wsA, s.agentID, "Desligada", "conteúdo que não vai")

	rec := e.do("PATCH", "/chat/agents/"+s.agentID+"/sources/"+off.ID, e.wsA,
		map[string]any{"enabled": false})
	wantStatus(t, rec, http.StatusOK)

	e.send(e.wsA, s.conversationID, "e agora?")
	block := sourcesBlockOf(e.llm.lastRequest)
	if !strings.Contains(block, "conteúdo que fica") {
		t.Fatalf("the enabled source did not reach the model: %q", block)
	}
	if strings.Contains(block, "conteúdo que não vai") || strings.Contains(block, "## Desligada") {
		t.Fatalf("a disabled source reached the model: %q", block)
	}

	// Still listed, still editable, and honest about not being used.
	page := e.sources(e.wsA, s.agentID)
	if page.Total != 2 {
		t.Fatalf("total = %d, want the disabled one still counted", page.Total)
	}
	for _, src := range page.Items {
		switch src.ID {
		case on.ID:
			if !src.InContext {
				t.Fatalf("the enabled source is not marked as used")
			}
		case off.ID:
			if src.Enabled || src.InContext {
				t.Fatalf("the disabled source reads as used: %+v", src)
			}
		}
	}

	// Turning it back on is the other half of "reversible".
	rec = e.do("PATCH", "/chat/agents/"+s.agentID+"/sources/"+off.ID, e.wsA,
		map[string]any{"enabled": true})
	wantStatus(t, rec, http.StatusOK)
	e.send(e.wsA, s.conversationID, "e agora?")
	if !strings.Contains(sourcesBlockOf(e.llm.lastRequest), "conteúdo que não vai") {
		t.Fatalf("re-enabling did not put the source back in the context")
	}
}

/* ── §10.3  sources do not leak between agents ───────────────────────── */

func TestSourcesAreScopedToTheirAgent(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)
	other := e.newAgent(e.wsA, s.providerID, "Outro agente")
	otherConv := e.newConversation(e.wsA, other)

	mine := e.addSource(e.wsA, s.agentID, "Só do primeiro", "segredo do primeiro agente")

	if got := e.sources(e.wsA, other).Items; len(got) != 0 {
		t.Fatalf("the other agent lists %d sources, want none", len(got))
	}

	e.send(e.wsA, otherConv, "o que você sabe?")
	if block := sourcesBlockOf(e.llm.lastRequest); block != "" {
		t.Fatalf("another agent's turn carried a sources block: %q", block)
	}

	// The same isolation on every single-record route: a source id is not
	// addressable through another agent's URL.
	for _, call := range []struct{ method, path string }{
		{"GET", "/chat/agents/" + other + "/sources/" + mine.ID},
		{"PATCH", "/chat/agents/" + other + "/sources/" + mine.ID},
		{"DELETE", "/chat/agents/" + other + "/sources/" + mine.ID},
	} {
		var body any
		if call.method == "PATCH" {
			body = map[string]any{"title": "sequestrada"}
		}
		rec := e.do(call.method, call.path, e.wsA, body)
		wantErrorCode(t, rec, http.StatusNotFound, "not_found")
	}
}

/* ── §10.4 and §10.5  the budget cuts whole documents, and reports ───── */

// TestSourcesBudgetCutsWholeDocuments drives the real stack past the
// ceiling and checks the two things that matter on the wire: the block
// never exceeds the budget, and no document arrives cut in half.
//
// The unit test in app/context_test.go owns the arithmetic. This one owns
// the claim that the arithmetic is what production runs.
func TestSourcesBudgetCutsWholeDocuments(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)

	// Each source is a fifth of the budget, so the sixth cannot fit.
	const each = app.SourcesBudgetChars / 5
	for i := 0; i < 8; i++ {
		e.addSource(e.wsA, s.agentID, "Doc "+string(rune('A'+i)), strings.Repeat(string(rune('a'+i)), each))
	}

	e.send(e.wsA, s.conversationID, "pergunta")
	block := sourcesBlockOf(e.llm.lastRequest)
	if got := len([]rune(block)); got > app.SourcesBudgetChars {
		t.Fatalf("sources block is %d characters, over the %d ceiling", got, app.SourcesBudgetChars)
	}

	// Whole documents only: every body is either the full run or absent.
	for _, body := range sourceBodies(t, block) {
		if len([]rune(body)) != each {
			t.Fatalf("a source arrived cut: %d characters, want %d", len([]rune(body)), each)
		}
	}

	page := e.sources(e.wsA, s.agentID)
	used := 0
	for _, src := range page.Items {
		if src.InContext {
			used++
		}
	}
	if used == 0 || used >= 8 {
		t.Fatalf("in_context marks %d of 8; want a real cut reported", used)
	}
	if page.UsedCharacters != len([]rune(block)) {
		t.Fatalf("page reports %d characters used, the wire carried %d",
			page.UsedCharacters, len([]rune(block)))
	}
	if page.BudgetCharacters != app.SourcesBudgetChars {
		t.Fatalf("page reports a %d budget, want %d", page.BudgetCharacters, app.SourcesBudgetChars)
	}
}

// TestOversizedSourceIsStoredAndNeverTruncated is §10.5, and it is the rule
// that dictated the whole selection design.
//
// A source may legitimately be larger than the block can carry: the domain
// allows 20.000 characters and the block carries 12.000. That source is not
// an error and is not cut — it is stored, listed, editable, flagged, and
// left out.
func TestOversizedSourceIsStoredAndNeverTruncated(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)

	body := strings.Repeat("z", domain.MaxSourceContent)
	huge := e.addSource(e.wsA, s.agentID, "Grande demais", body)
	small := e.addSource(e.wsA, s.agentID, "Cabe", "documento curto")

	e.send(e.wsA, s.conversationID, "pergunta")
	block := sourcesBlockOf(e.llm.lastRequest)

	// Not one character of it, and not a shortened version either.
	if strings.Contains(block, "z") {
		t.Fatalf("the oversized source leaked into the block")
	}
	if !strings.Contains(block, "documento curto") {
		t.Fatalf("the oversized source blocked the one that fits: %q", block)
	}

	page := e.sources(e.wsA, s.agentID)
	for _, src := range page.Items {
		switch src.ID {
		case huge.ID:
			if src.Characters != domain.MaxSourceContent {
				t.Fatalf("stored characters = %d, want %d", src.Characters, domain.MaxSourceContent)
			}
			if src.InContext {
				t.Fatalf("the oversized source claims to be in context")
			}
			if !src.Oversized {
				t.Fatalf("the oversized source is not flagged; the user has no way to know why it never appears")
			}
		case small.ID:
			if !src.InContext || src.Oversized {
				t.Fatalf("the small source is misreported: %+v", src)
			}
		}
	}

	// And it is still fully readable and editable — storage was not the
	// thing that failed.
	rec := e.do("GET", "/chat/agents/"+s.agentID+"/sources/"+huge.ID, e.wsA, nil)
	wantStatus(t, rec, http.StatusOK)
	got := decode[apiSource](t, rec)
	if got.Content != body {
		t.Fatalf("stored content came back changed: %d characters, want %d",
			len([]rune(got.Content)), len([]rune(body)))
	}
}

/* ── §10.6  an unreadable sources table degrades the turn ────────────── */

// TestSourcesReadFailureDegradesTheTurn removes the table under a live
// stack and sends a message.
//
// Same policy as memory: an unreachable auxiliary table must cost the turn
// its context, never its answer.
func TestSourcesReadFailureDegradesTheTurn(t *testing.T) {
	d := dsn(t)
	e := newEnv(t)
	s := e.seed(e.wsA)
	e.addSource(e.wsA, s.agentID, "Nunca lida", "conteúdo que não será lido")

	t.Cleanup(func() { freshDB(t, d) })
	if _, err := e.pool.Exec(t.Context(), `DROP TABLE chat.agent_sources`); err != nil {
		t.Fatalf("drop sources table: %v", err)
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
	if sourcesBlockOf(e.llm.lastRequest) != "" {
		t.Fatalf("a sources block was built from an unreadable table")
	}
	if e.llm.streamCalls == 0 {
		t.Fatalf("the provider was never called; the turn did not happen")
	}
}

/* ── §10.7  workspace isolation ──────────────────────────────────────── */

func TestSourcesWorkspaceIsolation(t *testing.T) {
	e := newEnv(t)
	a := e.seed(e.wsA)
	mine := e.addSource(e.wsA, a.agentID, "Só de A", "conteúdo do workspace A")

	// The agent itself is invisible from B, so every route under it is a
	// 404 rather than an empty list — the honest answer: from B, that agent
	// never existed.
	for _, call := range []struct{ method, path string }{
		{"GET", "/chat/agents/" + a.agentID + "/sources"},
		{"POST", "/chat/agents/" + a.agentID + "/sources"},
		{"GET", "/chat/agents/" + a.agentID + "/sources/" + mine.ID},
		{"PATCH", "/chat/agents/" + a.agentID + "/sources/" + mine.ID},
		{"DELETE", "/chat/agents/" + a.agentID + "/sources/" + mine.ID},
	} {
		var body any
		if call.method == "POST" || call.method == "PATCH" {
			body = map[string]any{"title": "invasão", "content": "invasão"}
		}
		rec := e.do(call.method, call.path, e.wsB, body)
		wantErrorCode(t, rec, http.StatusNotFound, "not_found")
	}

	// And B's own agent, in B's own workspace, sees nothing of A's.
	b := e.seed(e.wsB)
	if got := e.sources(e.wsB, b.agentID).Items; len(got) != 0 {
		t.Fatalf("workspace B sees %d sources, want none", len(got))
	}
	e.send(e.wsB, b.conversationID, "o que você sabe?")
	if block := sourcesBlockOf(e.llm.lastRequest); block != "" {
		t.Fatalf("workspace B's turn carried A's sources: %q", block)
	}
}

/* ── §10.8  one token heuristic ──────────────────────────────────────── */

// TestSourceTokenEstimateIsTheModuleHeuristic: the number the page shows
// and the number the ContextReport counts must come from the same function.
// Two estimates of one thing is one estimate plus a bug report.
func TestSourceTokenEstimateIsTheModuleHeuristic(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)
	// Accented on purpose: a byte count would disagree with a rune count
	// here, and the disagreement is exactly what this asserts against.
	content := strings.Repeat("é", 401)
	src := e.addSource(e.wsA, s.agentID, "Acentuada", content)

	if src.Characters != 401 {
		t.Fatalf("characters = %d, want 401 — the count is in characters, not bytes", src.Characters)
	}
	if want := app.EstimateTokens(401); src.EstimatedTokens != want {
		t.Fatalf("estimated_tokens = %d, want %d from app.EstimateTokens",
			src.EstimatedTokens, want)
	}

	listed := e.sources(e.wsA, s.agentID).Items[0]
	if listed.Characters != src.Characters || listed.EstimatedTokens != src.EstimatedTokens {
		t.Fatalf("the list and the record disagree: %+v vs %+v", listed, src)
	}
	// The list omits the text on purpose; the size must survive that.
	if listed.Content != "" {
		t.Fatalf("the list carried %d characters of content; it is a list, not a dump",
			len([]rune(listed.Content)))
	}
}

/* ── ordering ────────────────────────────────────────────────────────── */

// TestSourceOrderIsDeterministic: the block the model receives must be the
// same for two identical turns, and must match the order the page shows.
//
// Most recently updated first. Touching a source moves it to the front, and
// that is the only thing that reorders the block.
func TestSourceOrderIsDeterministic(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)

	first := e.addSource(e.wsA, s.agentID, "Primeira", "a")
	e.addSource(e.wsA, s.agentID, "Segunda", "b")
	third := e.addSource(e.wsA, s.agentID, "Terceira", "c")

	wantOrder := func(label string, want ...string) {
		t.Helper()
		e.send(e.wsA, s.conversationID, "pergunta")
		got := sourceTitles(t, sourcesBlockOf(e.llm.lastRequest))
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("%s: block order = %v, want %v", label, got, want)
		}
		listed := make([]string, 0, 3)
		for _, src := range e.sources(e.wsA, s.agentID).Items {
			listed = append(listed, src.Title)
		}
		if strings.Join(listed, ",") != strings.Join(want, ",") {
			t.Fatalf("%s: the page shows %v while the model receives %v", label, listed, want)
		}
	}

	wantOrder("newest first", "Terceira", "Segunda", "Primeira")

	// Two identical turns produce an identical block.
	before := sourcesBlockOf(e.llm.lastRequest)
	e.send(e.wsA, s.conversationID, "pergunta")
	if sourcesBlockOf(e.llm.lastRequest) != before {
		t.Fatalf("two identical turns produced different blocks")
	}

	// Touching a source moves it to the front, on both surfaces at once.
	rec := e.do("PATCH", "/chat/agents/"+s.agentID+"/sources/"+first.ID, e.wsA,
		map[string]any{"content": "a, revisada"})
	wantStatus(t, rec, http.StatusOK)
	wantOrder("after an edit", "Primeira", "Terceira", "Segunda")

	_ = third
}

/* ── CRUD and the guards ─────────────────────────────────────────────── */

func TestSourceLifecycle(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)

	t.Run("created enabled, with the size resolved", func(t *testing.T) {
		src := e.addSource(e.wsA, s.agentID, "Nova fonte", "corpo do documento")
		if !src.Enabled {
			t.Fatalf("a new source arrives switched off: %+v", src)
		}
		if src.Characters != len([]rune("corpo do documento")) {
			t.Fatalf("characters = %d", src.Characters)
		}
	})

	t.Run("the description is stored and never sent", func(t *testing.T) {
		rec := e.do("POST", "/chat/agents/"+s.agentID+"/sources", e.wsA, map[string]any{
			"title":       "Com descrição",
			"description": "nota para o humano",
			"content":     "corpo",
		})
		wantStatus(t, rec, http.StatusCreated)
		if got := decode[apiSource](t, rec); got.Description != "nota para o humano" {
			t.Fatalf("description = %q", got.Description)
		}
		e.send(e.wsA, s.conversationID, "pergunta")
		if strings.Contains(sourcesBlockOf(e.llm.lastRequest), "nota para o humano") {
			t.Fatalf("the description reached the model")
		}
	})

	t.Run("editing rewrites the document", func(t *testing.T) {
		src := e.addSource(e.wsA, s.agentID, "Para editar", "versão um")
		rec := e.do("PATCH", "/chat/agents/"+s.agentID+"/sources/"+src.ID, e.wsA,
			map[string]any{"title": "Editada", "content": "versão dois"})
		wantStatus(t, rec, http.StatusOK)
		got := decode[apiSource](t, rec)
		if got.Title != "Editada" || got.Content != "versão dois" {
			t.Fatalf("edit did not apply: %+v", got)
		}
		if got.Characters != len([]rune("versão dois")) {
			t.Fatalf("characters not recomputed after the edit: %d", got.Characters)
		}
	})

	t.Run("deleting removes it from the list and from the context", func(t *testing.T) {
		src := e.addSource(e.wsA, s.agentID, "Para apagar", "conteúdo efêmero")
		rec := e.do("DELETE", "/chat/agents/"+s.agentID+"/sources/"+src.ID, e.wsA, nil)
		wantStatus(t, rec, http.StatusNoContent)

		for _, item := range e.sources(e.wsA, s.agentID).Items {
			if item.ID == src.ID {
				t.Fatalf("the deleted source is still listed")
			}
		}
		e.send(e.wsA, s.conversationID, "pergunta")
		if strings.Contains(sourcesBlockOf(e.llm.lastRequest), "conteúdo efêmero") {
			t.Fatalf("the deleted source still reaches the model")
		}
		rec = e.do("DELETE", "/chat/agents/"+s.agentID+"/sources/"+src.ID, e.wsA, nil)
		wantErrorCode(t, rec, http.StatusNotFound, "not_found")
	})

	t.Run("validation", func(t *testing.T) {
		cases := []struct {
			name string
			body map[string]any
		}{
			{"empty title", map[string]any{"title": "  ", "content": "corpo"}},
			{"empty content", map[string]any{"title": "Título", "content": "   "}},
			{"title too long", map[string]any{"title": strings.Repeat("t", 121), "content": "corpo"}},
			{"content too long", map[string]any{"title": "T", "content": strings.Repeat("c", domain.MaxSourceContent+1)}},
			{"description too long", map[string]any{"title": "T", "content": "c", "description": strings.Repeat("d", 281)}},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				rec := e.do("POST", "/chat/agents/"+s.agentID+"/sources", e.wsA, tc.body)
				wantErrorCode(t, rec, http.StatusBadRequest, "invalid")
			})
		}
	})

	t.Run("a source under an agent that does not exist is a 404", func(t *testing.T) {
		rec := e.do("POST", "/chat/agents/"+uuid.NewString()+"/sources", e.wsA,
			map[string]any{"title": "órfã", "content": "corpo"})
		wantErrorCode(t, rec, http.StatusNotFound, "not_found")
	})

	t.Run("a source id that is not a uuid is a 400", func(t *testing.T) {
		rec := e.do("GET", "/chat/agents/"+s.agentID+"/sources/abc", e.wsA, nil)
		wantErrorCode(t, rec, http.StatusBadRequest, "invalid")
	})
}

// TestAgentWithSourcesCannotBeDeleted: one rule for every dependency of an
// agent. Conversations, memories and sources all refuse, and all say what
// is in the way.
func TestAgentWithSourcesCannotBeDeleted(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)
	agent := e.newAgent(e.wsA, s.providerID, "Agente com fontes")
	src := e.addSource(e.wsA, agent, "Uma fonte", "corpo")

	rec := e.do("DELETE", "/chat/agents/"+agent, e.wsA, nil)
	wantErrorCode(t, rec, http.StatusConflict, "conflict")

	wantStatus(t, e.do("DELETE", "/chat/agents/"+agent+"/sources/"+src.ID, e.wsA, nil),
		http.StatusNoContent)
	wantStatus(t, e.do("DELETE", "/chat/agents/"+agent, e.wsA, nil), http.StatusNoContent)
}

/* ── the two blocks together ─────────────────────────────────────────── */

// TestMemoryAndSourcesCoexist is the composition assertion the two batches
// share: both blocks present, in the fixed order, each with its own budget,
// and neither borrowing from the other.
func TestMemoryAndSourcesCoexist(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)

	e.remember(e.wsA, s.agentID, "prefere respostas diretas")
	e.addSource(e.wsA, s.agentID, "Guia editorial", "sempre citar a fonte")

	e.send(e.wsA, s.conversationID, "como eu escrevo isso?")

	msgs := e.llm.lastRequest.Messages
	if len(msgs) != 4 {
		t.Fatalf("wire has %d messages, want instructions, memory, sources, question", len(msgs))
	}
	if msgs[0].Role != "system" || !strings.Contains(msgs[0].Content, "agente de testes") {
		t.Fatalf("first message is not the instructions: %+v", msgs[0])
	}
	if !strings.HasPrefix(msgs[1].Content, "O que você deve lembrar") {
		t.Fatalf("second message is not memory: %q", msgs[1].Content)
	}
	if !strings.HasPrefix(msgs[2].Content, "Material de referência") {
		t.Fatalf("third message is not sources: %q", msgs[2].Content)
	}
	if msgs[3].Role != "user" {
		t.Fatalf("last message is not the question: %+v", msgs[3])
	}

	// Each block answers to its own ceiling. A shared pool would let a big
	// document push out the memories, which is not what either page says.
	if app.MemoryBudgetChars == app.SourcesBudgetChars {
		t.Fatalf("the two budgets became the same number; the ratio is the design")
	}
}

// TestTurnWithoutSourcesIsUnchanged: the compatibility claim of this batch.
// An agent with no sources sends exactly what it sent before they existed.
func TestTurnWithoutSourcesIsUnchanged(t *testing.T) {
	e := newEnv(t)
	s := e.seed(e.wsA)

	e.send(e.wsA, s.conversationID, "bom dia")

	msgs := e.llm.lastRequest.Messages
	if len(msgs) != 2 {
		t.Fatalf("wire has %d messages, want just the instructions and the question", len(msgs))
	}
	if sourcesBlockOf(e.llm.lastRequest) != "" {
		t.Fatalf("an agent with no sources sent a sources block")
	}
}

/* ── helpers ─────────────────────────────────────────────────────────── */

// sourceBodies splits the block into the text of each document.
func sourceBodies(t *testing.T, block string) []string {
	t.Helper()
	out := make([]string, 0)
	for _, chunk := range strings.Split(block, "\n\n## ")[1:] {
		_, body, ok := strings.Cut(chunk, "\n")
		if !ok {
			t.Fatalf("a source arrived without a body: %q", chunk)
		}
		out = append(out, body)
	}
	return out
}

// sourceTitles is the same split, keeping the headings.
func sourceTitles(t *testing.T, block string) []string {
	t.Helper()
	out := make([]string, 0)
	for _, chunk := range strings.Split(block, "\n\n## ")[1:] {
		title, _, ok := strings.Cut(chunk, "\n")
		if !ok {
			t.Fatalf("a source arrived without a body: %q", chunk)
		}
		out = append(out, title)
	}
	return out
}
